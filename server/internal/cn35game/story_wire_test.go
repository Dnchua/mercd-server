// story_wire_test.go 故事阅读三接口的协议回放测试（设计真源
// notes/proposed/2026-09-28-cn35-story-reader.md）：
// 真实 stories fixture（assets/cn350/stories/，APK 提取产物）×
// 真实 cn35wire 加密通道（httptest + cn35protocol 编解码），验证
//   - /chapters/ 章节行 + chapter_select_option 深合并键；
//   - /stories/list?chapter_id=1 → stories_1 十行、order_num 升序、
//     story_read_additional_message 无 PageMessage 非法字符；
//   - /stories/read/<id>.json 加密下发顶层 story/asset_names/actress_xys
//     （无 data 信封），未知 id → 404；
//   - fixture 缺失时 /chapters/ 回落空数组。
//
// catalog 校验负例单测见 story_internal_test.go（包内）。
package cn35game_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// storyServer 装配内存账号/会话 + 指定 AssetsDir 的测试服。
func storyServer(t *testing.T, assetsDir string) *httptest.Server {
	t.Helper()
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:   cn35game.NewMemoryAccounts(),
		Sessions:   cn35wire.NewMemoryStore(),
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  assetsDir,
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func storyAssetsDir(t *testing.T) string {
	if _, err := os.Stat(filepath.Join("..", "..", "assets", "cn350", "stories", "index.json")); os.IsNotExist(err) {
		t.Skip("需要用户自备的故事fixture；未验证原作故事回放")
	}

	t.Helper()
	dir := filepath.Join("..", "..", "assets", "cn350")
	return dir
}

// TestChaptersReal 章节读面：/chapters/ 至少返回第 1 章，行内
// chapter_select_option 携带 Stories 页深合并所需的 requireData/parts 键。
func TestChaptersReal(t *testing.T) {
	ts := storyServer(t, storyAssetsDir(t))
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-st-ch")

	data := gameData(t, ts.URL, "/chapters/", uid, sk, s0, s1, "")
	chapters := docList(t, data, "chapters")
	if len(chapters) < 1 {
		t.Fatalf("应至少有第 1 章: %v", chapters)
	}
	ch := chapters[0].(map[string]any)
	if ch["id"].(float64) != 1 || ch["name"] != "第1章" {
		t.Fatalf("章节行字段: %v", ch)
	}
	if ch["background_asset_name"] != "9s_btnLarge_basic" {
		t.Fatalf("background_asset_name: %v", ch["background_asset_name"])
	}
	opt, ok := ch["chapter_select_option"].(map[string]any)
	if !ok {
		t.Fatalf("缺 chapter_select_option: %v", ch)
	}
	rd, ok := opt["requireData"].(map[string]any)["stories_1"].(map[string]any)
	if !ok || rd["path"] != "/stories/list?chapter_id=1" {
		t.Fatalf("chapter_select_option.requireData: %v", opt)
	}
	parts := opt["parts"].(map[string]any)
	if parts["ListParts"].(map[string]any)["data_key"] != "stories_1" {
		t.Fatalf("ListParts.data_key: %v", parts)
	}
	if parts["Header"].(map[string]any)["title_right_label"] != "第1章" {
		t.Fatalf("Header.title_right_label: %v", parts)
	}
}

// TestStoriesList /stories/list?chapter_id=1 → stories_1 至少包含前 10 话并按
// order_num 升序；additional_message 形态合法（&/= 不残留进 title）。
func TestStoriesList(t *testing.T) {
	ts := storyServer(t, storyAssetsDir(t))
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-st-list")

	data := gameData(t, ts.URL, "/stories/list", uid, sk, s0, s1, "chapter_id=1")
	rows := docList(t, data, "stories_1")
	if len(rows) < 10 {
		t.Fatalf("stories_1 应至少 10 话: %d", len(rows))
	}
	msgRe := regexp.MustCompile(`^id=\d+&chapter_id=1(&title=[^&=]*)?$`)
	prevOrder := 0
	var row4 map[string]any
	for _, r := range rows {
		m := r.(map[string]any)
		order := int(m["order_num"].(float64))
		if order <= prevOrder {
			t.Fatalf("stories_1 未按 order_num 升序: prev=%d cur=%d", prevOrder, order)
		}
		prevOrder = order
		if m["chapter_id"].(float64) != 1 {
			t.Fatalf("行字段 chapter_id: %v", m)
		}
		if m["id"].(float64) == 4 {
			row4 = m
		}
		if m["sub_story"] != false || m["unit_asset_name"] != "" {
			t.Fatalf("sub_story/unit_asset_name 恒值: %v", m)
		}
		msg, _ := m["story_read_additional_message"].(string)
		if !msgRe.MatchString(msg) {
			t.Fatalf("additional_message 形态非法（&/= 残留）: %q", msg)
		}
	}
	// story4「启程」带 title 参数：续读/标题栏依赖它。
	if row4 == nil {
		t.Fatal("stories_1 缺少 id=4")
	}
	if !strings.Contains(row4["story_read_additional_message"].(string), "&title=启程") {
		t.Fatalf("story4 additional_message 缺 title: %v",
			row4["story_read_additional_message"])
	}

	// 非法/缺失 chapter_id → data.stories 空表；未知数字章 → stories_N 空表。
	for _, q := range []string{"", "chapter_id=", "chapter_id=abc"} {
		d := gameData(t, ts.URL, "/stories/list", uid, sk, s0, s1, q)
		if l := docList(t, d, "stories"); len(l) != 0 {
			t.Fatalf("query=%q 应回 stories 空表: %v", q, d)
		}
	}
	d99 := gameData(t, ts.URL, "/stories/list", uid, sk, s0, s1, "chapter_id=99")
	if l := docList(t, d99, "stories_99"); len(l) != 0 {
		t.Fatalf("未知章应回 stories_99 空表: %v", d99)
	}
}

// TestStoryRead /stories/read/<id>.json 经加密通道下发原文：
// 顶层 story/asset_names/actress_xys（无 data 信封）；未知 id/非法路径 → 404。
func TestStoryRead(t *testing.T) {
	ts := storyServer(t, storyAssetsDir(t))
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-st-read")

	plain := gameCall(t, ts.URL, "/stories/read/4.json", uid, sk, s0, s1,
		"id=4&chapter_id=1&title=%E5%90%AF%E7%A8%8B", "_method=GET")
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatalf("剧本响应非 JSON: %.200s err=%v", plain, err)
	}
	if _, bad := doc["data"]; bad {
		t.Fatalf("剧本响应不得加 data 信封: %v", doc)
	}
	story, ok := doc["story"].(map[string]any)
	if !ok || story["id"].(float64) != 4 {
		t.Fatalf("story.id: %v", doc["story"])
	}
	if story["title"] != "启程" {
		t.Fatalf("story.title: %v", story["title"])
	}
	if _, ok := doc["asset_names"].([]any); !ok {
		t.Fatalf("asset_names 缺失: %v", doc)
	}
	if _, ok := doc["actress_xys"].(map[string]any); !ok {
		t.Fatalf("actress_xys 缺失: %v", doc)
	}

	// 未命中 id / 非法路径 → 404（明文小 JSON，非加密 200）。
	for _, p := range []string{
		"/stories/read/999.json", "/stories/read/abc.json", "/stories/read/4",
	} {
		if got := gameCallMaybeErr(t, ts.URL, p, uid, sk, s0, s1, ""); got != nil {
			t.Fatalf("%s 应 404，得到: %.200s", p, got)
		}
	}
}

// TestStoryCatalogMissing fixture 缺失（AssetsDir 无 stories/）时
// /chapters/ 回落空数组，不报错。
func TestStoryCatalogMissing(t *testing.T) {
	ts := storyServer(t, t.TempDir()) // 空目录：无 stories/ 也无清单
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-st-empty")

	data := gameData(t, ts.URL, "/chapters/", uid, sk, s0, s1, "")
	if l := docList(t, data, "chapters"); len(l) != 0 {
		t.Fatalf("无 fixture 时 /chapters/ 应空数组: %v", l)
	}
	// 列表端点同形回落：数字但无该章 → stories_1 空表。
	d := gameData(t, ts.URL, "/stories/list", uid, sk, s0, s1, "chapter_id=1")
	if l := docList(t, d, "stories_1"); len(l) != 0 {
		t.Fatalf("无 fixture 时 stories_1 应空: %v", d)
	}
	// read 未命中 → 404。
	if got := gameCallMaybeErr(t, ts.URL, "/stories/read/4.json", uid, sk, s0, s1, ""); got != nil {
		t.Fatalf("无 fixture 时 read 应 404，得到: %.200s", got)
	}
}

// TestStory54MemorialChapter2 第二章纪念版转换剧情（story_54「图图的不适」，
// tools 侧由纪念版 StoryYamlData_54 转换生成）在 RemoteAssets 生效清单下可见：
// 其立绘 94/95/10011 仅存在于远端叠加（remote_assets/storyactress/），
// 基线清单不含 → 纯基线装配下该话被校验剔除、第 2 章整章隐藏（见
// TestChaptersReal 仍断言 1 章）。本测试以真实 remote_assets 目录扫描验证
// 生产路径：/chapters/ 出第 2 章、/stories/list?chapter_id=2 出 story54 行、
// /stories/read/54.json 下发 21 场景原文且 actress_xys 齐全。
func TestStory54MemorialChapter2(t *testing.T) {
	assetsDir := storyAssetsDir(t)
	remoteDir := filepath.Join("..", "..", "server-state-cn35", "remote_assets")
	if _, err := os.Stat(remoteDir); err != nil {
		remoteDir = filepath.Join("..", "..", "..", "inputs", "files")
		if _, err2 := os.Stat(remoteDir); err2 != nil {
			t.Skipf("remote_assets 与 inputs/files 目录均不存在（非全量工作区）: %v", err)
		}
	}
	ra := cn35game.NewRemoteAssets(remoteDir, assetsDir, t.TempDir(), nil)
	if err := ra.Scan(); err != nil {
		t.Fatalf("RemoteAssets 扫描失败: %v", err)
	}

	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:     cn35game.NewMemoryAccounts(),
		Sessions:     cn35wire.NewMemoryStore(),
		ServerHost:   "127.0.0.1:18091",
		AssetsDir:    assetsDir,
		RemoteAssets: ra,
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-st-ch2")

	// /chapters/ → 至少含第 1/2 章（全量提取时为 11 章）。
	data := gameData(t, ts.URL, "/chapters/", uid, sk, s0, s1, "")
	chapters := docList(t, data, "chapters")
	if len(chapters) < 2 {
		t.Fatalf("remote 装配后应至少 2 章: %v", chapters)
	}
	ch2 := chapters[1].(map[string]any)
	if ch2["id"].(float64) != 2 || ch2["name"] != "第2章" {
		t.Fatalf("第 2 章字段: %v", ch2)
	}

	// /stories/list?chapter_id=2 → stories_2 包含 story54 行。
	l := gameData(t, ts.URL, "/stories/list", uid, sk, s0, s1, "chapter_id=2")
	rows := docList(t, l, "stories_2")
	if len(rows) < 1 {
		t.Fatalf("stories_2 应至少 1 行: %v", rows)
	}
	var m map[string]any
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["id"].(float64) == 54 {
			m = row
			break
		}
	}
	if m == nil || m["title"] != "图图的不适" {
		t.Fatalf("story54 行字段: %v", m)
	}

	// /stories/read/54.json → 下发原文：21 场景、立绘/背景/BGM 资源名、
	// actress_xys 含远端立绘 10011。
	plain := gameCall(t, ts.URL, "/stories/read/54.json", uid, sk, s0, s1,
		"id=54&chapter_id=2&title=%E5%9B%BE%E5%9B%BE%E7%9A%84%E4%B8%8D%E9%80%82",
		"_method=GET")
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatalf("story54 响应非 JSON: %.200s err=%v", plain, err)
	}
	story, ok := doc["story"].(map[string]any)
	if !ok || story["id"].(float64) != 54 {
		t.Fatalf("story54 story.id: %v", doc["story"])
	}
	scenes, _ := story["scenes_map_array"].([]any)
	if len(scenes) != 21 {
		t.Fatalf("story54 场景数应 21: %d", len(scenes))
	}
	xys, _ := doc["actress_xys"].(map[string]any)
	if _, ok := xys["actress_xy_10011"]; !ok {
		t.Fatalf("actress_xys 缺 10011: %v", doc["actress_xys"])
	}
}
