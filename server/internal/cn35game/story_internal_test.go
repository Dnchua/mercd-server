// story_internal_test.go 故事目录校验器单测（包内，直接调
// loadStoryCatalog/validateStoryDoc）：
// 缺资源 / 缺 actress_xy / story.id 冲突 / 空场景 / 目录键重复 /
// fixture 缺失——失败话全部剔除且进 diagnostics（设计真源
// notes/proposed/2026-09-28-cn35-story-reader.md §1）。
package cn35game

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 最小生效清单：覆盖 storyFixture 默认引用的两个资源（10003/10_2 有意不在内）。
var testAccepted = map[string]bool{
	"storyactress/storyactress10002":     true,
	"storybackground/storybackground2_2": true,
	"unit/unit_large_ns_414":             true,
	"sound/B-7":                          true,
}

// storyFixtureDoc 构造一话合法响应体（顶层 story/asset_names/actress_xys）。
func storyFixtureDoc(id, chapter, order int) map[string]any {
	return map[string]any{
		"story": map[string]any{
			"id": id, "chapter_id": chapter, "order_num": order,
			"title": "测试", "sub_title": "", "story_type_id": 1,
			"scenes_map_array": []any{
				map[string]any{
					"text":       "“测试台词”",
					"center":     map[string]any{"id": 10002, "expression": "tujo"},
					"background": "2", "timezone": "2",
				},
			},
		},
		"asset_names": []any{
			"storyactress/storyactress10002",
			"storybackground/storybackground2_2",
		},
		"actress_xys": map[string]any{
			"actress_xy_10002": map[string]any{"xy": map[string]any{"x": 0, "y": 0}},
		},
	}
}

// storyIndexEntryFor 与 storyFixtureDoc 对齐的 index.stories 条目。
func storyIndexEntryFor(id, chapter, order int) map[string]any {
	return map[string]any{
		"id": id, "textasset": "story" + strconv.Itoa(id),
		"source_file": "f", "source_sha256": "x",
		"chapter_id": chapter, "order_num": order,
		"title": "测试", "sub_title": "", "quest_id": 0,
		"read_timing_id": 1, "story_type_id": 1, "scene_count": 1,
		"provenance": "apk-textasset",
	}
}

// writeStoryFixture 在 dir/stories/ 写 index.json + 各 story_<id>.json。
// entries 为 index.stories 行；docs 为 id → 响应体 map（nil 则不写文件）。
func writeStoryFixture(t *testing.T, dir string, entries []map[string]any, docs map[int]map[string]any) {
	t.Helper()
	sd := filepath.Join(dir, "stories")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	idx := map[string]any{
		"chapters": []any{map[string]any{
			"id": 1, "name": "第1章", "background_asset_name": "bg",
			"provenance": "test"}},
		"stories":  entries,
		"excluded": []any{},
	}
	raw, _ := json.Marshal(idx)
	if err := os.WriteFile(filepath.Join(sd, "index.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for id, doc := range docs {
		raw, _ := json.Marshal(doc)
		name := filepath.Join(sd, "story_"+strconv.Itoa(id)+".json")
		if err := os.WriteFile(name, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func diagReasons(cat *storyCatalog) []string {
	out := []string{}
	for _, d := range cat.diagnostics {
		out = append(out, d.Reason)
	}
	return out
}

func hasDiag(cat *storyCatalog, substr string) bool {
	for _, d := range cat.diagnostics {
		if strings.Contains(d.Reason, substr) {
			return true
		}
	}
	return false
}

func TestLoadStoryCatalogOK(t *testing.T) {
	dir := t.TempDir()
	writeStoryFixture(t, dir,
		[]map[string]any{storyIndexEntryFor(1, 1, 1)},
		map[int]map[string]any{1: storyFixtureDoc(1, 1, 1)})
	cat := loadStoryCatalog(dir, testAccepted)
	if len(cat.diagnostics) != 0 {
		t.Fatalf("合法 fixture 不应有诊断: %v", diagReasons(cat))
	}
	if cat.stories[1] == nil || len(cat.chapters) != 1 {
		t.Fatalf("故事/章未加载: %+v", cat)
	}
	var top map[string]any
	if err := json.Unmarshal(cat.stories[1].payload, &top); err != nil {
		t.Fatalf("payload 非 JSON: %v", err)
	}
	if top["story"].(map[string]any)["id"].(float64) != 1 {
		t.Fatalf("payload story.id: %v", top)
	}
}

// TestLoadStoryCatalogMemorialFields 纪念版移植形态：scoutactress 读
// unit/unit_large_ns_<id>，bgm 与其包同列 asset_names 时通过。
func TestLoadStoryCatalogMemorialFields(t *testing.T) {
	dir := t.TempDir()
	doc := storyFixtureDoc(1, 1, 1)
	doc["asset_names"] = []any{
		"sound/B-7",
		"storyactress/storyactress10002",
		"storybackground/storybackground2_2",
		"unit/unit_large_ns_414",
	}
	doc["actress_xys"] = map[string]any{
		"actress_xy_10002": map[string]any{"xy": map[string]any{"x": 0, "y": 0}},
		"actress_xy_414":   map[string]any{"xy": map[string]any{"x": 0, "y": 0}},
	}
	doc["story"].(map[string]any)["scenes_map_array"] = []any{
		map[string]any{
			"text": "「测试台词」", "background": "2", "timezone": "2", "bgm": "sound/B-7",
			"window_type": "shout", "effect": "Shake",
			"left":  map[string]any{"id": 10002, "type": "navi", "expression": "tujo", "active": true},
			"right": map[string]any{"id": 414, "type": "scoutactress", "active": false},
		},
	}
	writeStoryFixture(t, dir,
		[]map[string]any{storyIndexEntryFor(1, 1, 1)},
		map[int]map[string]any{1: doc})
	cat := loadStoryCatalog(dir, testAccepted)
	if len(cat.diagnostics) != 0 || cat.stories[1] == nil {
		t.Fatalf("纪念版字段形态应通过: %v", diagReasons(cat))
	}
}

func TestLoadStoryCatalogRejections(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(doc map[string]any)
		wantSub string
	}{
		{"缺 asset_names 资源", func(d map[string]any) {
			d["asset_names"] = []any{"storyactress/storyactress999"}
		}, "未下发资源"},
		{"缺 actress_xy", func(d map[string]any) {
			d["actress_xys"] = map[string]any{}
		}, "actress_xy_10002"},
		{"缺 storyactress bundle", func(d map[string]any) {
			d["asset_names"] = []any{"storybackground/storybackground2_2"}
			d["actress_xys"] = map[string]any{"actress_xy_10003": map[string]any{}}
			d["story"].(map[string]any)["scenes_map_array"] = []any{
				map[string]any{"text": "“xy”",
					"center":     map[string]any{"id": 10003, "expression": "tujo"},
					"background": "2", "timezone": "2"},
			}
		}, "缺资源 storyactress/storyactress10003"},
		{"立绘包未列入 asset_names", func(d map[string]any) {
			d["asset_names"] = []any{"storybackground/storybackground2_2"}
		}, "未列入 asset_names"},
		{"立绘 expression 为空", func(d map[string]any) {
			d["story"].(map[string]any)["scenes_map_array"].([]any)[0].(map[string]any)["center"] = map[string]any{"id": 10002, "expression": ""}
		}, "expression 为空"},
		{"scoutactress 缺 unit_large_ns", func(d map[string]any) {
			d["actress_xys"] = map[string]any{"actress_xy_10002": map[string]any{}}
			d["story"].(map[string]any)["scenes_map_array"] = []any{
				map[string]any{"text": "“xy”",
					"center":     map[string]any{"id": 10002, "type": "scoutactress"},
					"background": "2", "timezone": "2"},
			}
		}, "缺资源 unit/unit_large_ns_10002"},
		{"BGM 未下发", func(d map[string]any) {
			d["story"].(map[string]any)["scenes_map_array"].([]any)[0].(map[string]any)["bgm"] = "sound/B-999"
		}, "缺 BGM 资源 sound/B-999"},
		{"BGM 未列入 asset_names", func(d map[string]any) {
			d["story"].(map[string]any)["scenes_map_array"].([]any)[0].(map[string]any)["bgm"] = "sound/B-7"
		}, "BGM sound/B-7 未列入 asset_names"},
		{"空场景", func(d map[string]any) {
			d["story"].(map[string]any)["scenes_map_array"] = []any{}
		}, "scenes_map_array"},
		{"短文本", func(d map[string]any) {
			d["story"].(map[string]any)["scenes_map_array"] = []any{
				map[string]any{"text": "x"},
			}
		}, "长度<2"},
		{"story 对象缺失", func(d map[string]any) {
			delete(d, "story")
		}, "story 对象缺失"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			doc := storyFixtureDoc(1, 1, 1)
			tc.mutate(doc)
			writeStoryFixture(t, dir,
				[]map[string]any{storyIndexEntryFor(1, 1, 1)},
				map[int]map[string]any{1: doc})
			cat := loadStoryCatalog(dir, testAccepted)
			if cat.stories[1] != nil {
				t.Fatalf("应剔除: %s", tc.name)
			}
			if !hasDiag(cat, tc.wantSub) {
				t.Fatalf("诊断缺 %q: %v", tc.wantSub, diagReasons(cat))
			}
			if len(cat.chapters) != 0 {
				t.Fatalf("无有效话的章不应下发: %v", cat.chapters)
			}
		})
	}
}

// TestLoadStoryCatalogIDConflict 文件 id 与 inner story.id 不符 → 剔除；
// index 内 id/(chapter,order) 重复 → 后者剔除。
func TestLoadStoryCatalogIDConflict(t *testing.T) {
	dir := t.TempDir()
	bad := storyFixtureDoc(2, 1, 2)
	bad["story"].(map[string]any)["id"] = 5 // inner id≠文件 id
	writeStoryFixture(t, dir,
		[]map[string]any{storyIndexEntryFor(2, 1, 2)},
		map[int]map[string]any{2: bad})
	cat := loadStoryCatalog(dir, testAccepted)
	if cat.stories[2] != nil || !hasDiag(cat, "与文件 id") {
		t.Fatalf("inner id 不符应剔除: %v", diagReasons(cat))
	}

	dir2 := t.TempDir()
	e1, e2 := storyIndexEntryFor(1, 1, 1), storyIndexEntryFor(1, 1, 1)
	writeStoryFixture(t, dir2, []map[string]any{e1, e2},
		map[int]map[string]any{1: storyFixtureDoc(1, 1, 1)})
	cat2 := loadStoryCatalog(dir2, testAccepted)
	if cat2.stories[1] == nil || !hasDiag(cat2, "冲突") {
		t.Fatalf("id 重复应有冲突诊断: %v", diagReasons(cat2))
	}

	dir3 := t.TempDir()
	e3, e4 := storyIndexEntryFor(1, 1, 1), storyIndexEntryFor(2, 1, 1) // 同 (1,1)
	writeStoryFixture(t, dir3, []map[string]any{e3, e4},
		map[int]map[string]any{
			1: storyFixtureDoc(1, 1, 1),
			2: storyFixtureDoc(2, 1, 1),
		})
	cat3 := loadStoryCatalog(dir3, testAccepted)
	if cat3.stories[1] == nil || cat3.stories[2] != nil ||
		!hasDiag(cat3, "目录键") {
		t.Fatalf("目录键重复应剔除后者: %v", diagReasons(cat3))
	}
}

// TestLoadStoryCatalogMissing 无 stories/ 或索引损坏 → 空 catalog 不 panic。
func TestLoadStoryCatalogMissing(t *testing.T) {
	cat := loadStoryCatalog(t.TempDir(), testAccepted)
	if len(cat.stories) != 0 || len(cat.chapters) != 0 {
		t.Fatal("缺目录应返回空 catalog")
	}
	dir := t.TempDir()
	sd := filepath.Join(dir, "stories")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "index.json"),
		[]byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat2 := loadStoryCatalog(dir, testAccepted)
	if len(cat2.stories) != 0 || !hasDiag(cat2, "非法 JSON") {
		t.Fatalf("坏索引应空 catalog + 诊断: %v", diagReasons(cat2))
	}
}
