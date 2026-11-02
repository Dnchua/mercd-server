package cn35game_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// LEGACY-ASSETS-02：真实 app_conf（v201710121006）Home.requireData 的四条依赖端点。
//
// 为什么必须存在：PageBase.WaitForAllDataLoaded（PageBase.cs:782-800）对任一
// requireData 失败即 ShowErrorModal + CancelShowPage；未注册路由的 501 被判为
// ProtocolError（JSONRequester.cs:432）→ Home 整体打不开。
// 实机 2026-09-27 复现「连接出错」即由这四个 501 造成。

func requiredataServer(t *testing.T, stateDir string) (*httptest.Server, *cn35store.Store) {
	t.Helper()
	assets := filepath.Join("..", "..", "assets", "cn350")
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:   st,
		Sessions:   st,
		Presents:   st,
		Infos:      st,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  assets,
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
	return ts, st
}

// TestHomeRequireDataEndpointsRegistered 断言真实 app_conf 的 Home.requireData
// 路径**全部已注册**。这是本批次的核心回归：缺任一条 Home 即被 CancelShowPage。
func TestHomeRequireDataEndpointsRegistered(t *testing.T) {
	assets := filepath.Join("..", "..", "assets", "cn350")
	raw, err := os.ReadFile(filepath.Join(assets, "serve_app_conf_android.json"))
	if err != nil {
		t.Skipf("跳过：读不到 app_conf（%v）", err)
	}
	var conf struct {
		Pages map[string]struct {
			RequireData map[string]struct {
				Path string `json:"path"`
			} `json:"requireData"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(raw, &conf); err != nil {
		t.Fatalf("解析 app_conf: %v", err)
	}
	home, ok := conf.Pages["Home"]
	if !ok {
		t.Fatal("app_conf 无 Home 页")
	}
	if len(home.RequireData) == 0 {
		t.Fatal("Home.requireData 为空")
	}

	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  assets,
	})
	registered := map[string]bool{}
	for _, r := range reg.Routes() {
		registered[r.Path] = true
	}

	for key, rd := range home.RequireData {
		path := rd.Path
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			t.Fatalf("Home.requireData[%s] 无 path", key)
		}
		// app_conf 的 path 是**逻辑路径**，客户端 DataLoad 可能再拼接后缀。
		// 实证：配置写 /users/preset_data，但设备抓到的真实请求是
		// /users/preset_data.json（Common.cs:643 硬编码带 .json）。
		// 故注册表比对时同时接受「原样」与「+.json」两种形态。
		if registered[path] || registered[path+".json"] {
			continue
		}
		t.Errorf("Home.requireData[%s] 路径 %q 未注册 → 客户端将 501/CancelShowPage", key, path)
	}
}

// TestUserPresentsEmptyStateShape 空池时 /user_presents/ 返回空数组。
//
// IconPresentParts.OnLoadOption 取 docList.Count，空数组 → 图标隐藏（合法空态）。
func TestUserPresentsEmptyStateShape(t *testing.T) {
	ts, st := requiredataServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-rd-presents")
	clearPresents(t, st, uid)

	data := gameData(t, ts.URL, "/user_presents/", uid, sk, s0, s1, "")
	list := docList(t, data, "user_presents")
	if len(list) != 0 {
		t.Fatalf("空池应返回空数组，得到 %d 条", len(list))
	}
}

// TestInfosEmptyStateShape 空池时 /infos/index 返回空数组且不下发 most_recently_info。
func TestInfosEmptyStateShape(t *testing.T) {
	ts, _ := requiredataServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-rd-infos")

	data := gameData(t, ts.URL, "/infos/index", uid, sk, s0, s1, "just_header=true")
	list := docList(t, data, "infos")
	if len(list) != 0 {
		t.Fatalf("空池应返回空数组，得到 %d 条", len(list))
	}
	if _, has := data["most_recently_info"]; has {
		t.Fatal("无公告时不应下发 most_recently_info（客户端据此显示红点）")
	}
}

// TestInfosWindowFilterAndOrder 公告按时间窗过滤、按 weight DESC 取首条。
func TestInfosWindowFilterAndOrder(t *testing.T) {
	ts, st := requiredataServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-rd-infos2")

	rows := []struct {
		id      int
		title   string
		beginAt int64
		endAt   int64
		weight  int
		enabled int
	}{
		{1, "已过期", 1000, 2000, 100, 1},
		{2, "生效中-低", 1000, 0, 1, 1},
		{3, "生效中-高", 1000, 0, 9, 1},
		{4, "已停用", 1000, 0, 99, 0},
	}
	for _, r := range rows {
		if _, err := st.DB().Exec(`INSERT INTO cn35_infos
			(id,category,title,body,begin_at,end_at,enabled,weight)
			VALUES (?,?,?,?,?,?,?,?)`,
			r.id, "info", r.title, "body", r.beginAt, r.endAt, r.enabled, r.weight); err != nil {
			t.Fatalf("插入公告: %v", err)
		}
	}

	data := gameData(t, ts.URL, "/infos/index", uid, sk, s0, s1, "")
	list := docList(t, data, "infos")
	if len(list) != 2 {
		t.Fatalf("应只剩 2 条生效公告（排除过期/停用），得到 %d 条：%v", len(list), list)
	}
	first, _ := list[0].(map[string]any)
	if first["title"] != "生效中-高" {
		t.Fatalf("首条应为 weight 最高者，得到 %v", first["title"])
	}
	mri, ok := data["most_recently_info"].(map[string]any)
	if !ok {
		t.Fatal("有公告时应下发 most_recently_info")
	}
	if mri["title"] != "生效中-高" {
		t.Fatalf("most_recently_info 应与首条一致，得到 %v", mri["title"])
	}
}

// TestUserPresentsFiltersReceivedAndExpired 待领礼物排除已领取与已过期。
func TestUserPresentsFiltersReceivedAndExpired(t *testing.T) {
	ts, st := requiredataServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-rd-presents2")
	clearPresents(t, st, uid)

	insert := func(uuid string, received, expire int64) {
		if _, err := st.DB().Exec(`INSERT INTO cn35_user_presents
			(uuid,user_id,content_type,content_id,amount,expire_at,received_at,created_at)
			VALUES (?,?,?,?,?,?,?,?)`,
			uuid, uid, "unit", 152, 1, expire, received, 1000); err != nil {
			t.Fatalf("插入礼物: %v", err)
		}
	}
	insert("p-unread-nolimit", 0, 0)
	insert("p-unread-future", 0, 99999999999)
	insert("p-expired", 0, 2000)
	insert("p-received", 555, 0)

	data := gameData(t, ts.URL, "/user_presents/", uid, sk, s0, s1, "")
	list := docList(t, data, "user_presents")
	if len(list) != 2 {
		t.Fatalf("应剩 2 条待领（排除已过期/已领取），得到 %d 条：%v", len(list), list)
	}
	for _, it := range list {
		m, _ := it.(map[string]any)
		if m["uuid"] == nil || m["uuid"] == "" {
			t.Fatalf("礼物行缺 uuid（客户端领取路径需要）：%v", m)
		}
	}
}

// TestUserPresentsIsolatedPerAccount 礼物按账号隔离（不串号）。
func TestUserPresentsIsolatedPerAccount(t *testing.T) {
	ts, st := requiredataServer(t, t.TempDir())
	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "dev-rd-iso-a")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "dev-rd-iso-b")
	clearPresents(t, st, uidA, uidB)

	if _, err := st.DB().Exec(`INSERT INTO cn35_user_presents
		(uuid,user_id,content_type,content_id,amount,expire_at,received_at,created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		"only-a", uidA, "unit", 152, 1, 0, 0, 1000); err != nil {
		t.Fatalf("插入礼物: %v", err)
	}

	a := docList(t, gameData(t, ts.URL, "/user_presents/", uidA, skA, s0A, s1A, ""), "user_presents")
	b := docList(t, gameData(t, ts.URL, "/user_presents/", uidB, skB, s0B, s1B, ""), "user_presents")
	if len(a) != 1 {
		t.Fatalf("账号 A 应看到 1 条，得到 %d", len(a))
	}
	if len(b) != 0 {
		t.Fatalf("账号 B 不应看到 A 的礼物，得到 %d 条", len(b))
	}
}

// TestAdventureAndChooseAttrEmptyStateAreSuccess 两条玩法端点返回成功空态
// （不再 501）。语义为「无内容」，原服字节未证 —— 见契约卡 22.6。
func TestAdventureAndChooseAttrEmptyStateAreSuccess(t *testing.T) {
	ts, _ := requiredataServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-rd-adv")

	data := gameData(t, ts.URL, "/adventure/", uid, sk, s0, s1, "")
	lad, ok := data["LoginAdventureData"].(map[string]any)
	if !ok {
		t.Fatalf("data.LoginAdventureData 缺失：%v", data)
	}
	// 空态必须让客户端 Load **成功**并落到 "already shown, skipped" 静默跳过分支：
	// 2026-10-26 实机证实，只有 adventure_info 而缺 `adventure` 子键时客户端走的是
	// Load 失败分支（每次冷启动打一条 E Unity），故这里逐层断言记录层必填字段。
	outer, ok := lad["adventure_info"].(map[string]any)
	if !ok {
		t.Fatalf("LoginAdventureData 缺 adventure_info：%v", lad)
	}
	adv, ok := outer["adventure"].(map[string]any)
	if !ok {
		t.Fatalf("adventure_info 缺 adventure 记录层（客户端 Load 会失败）：%v", outer)
	}
	for _, k := range []string{"adventure_info", "begin_date_i", "reward_config_id", "user_id"} {
		if _, has := adv[k]; !has {
			t.Fatalf("adventure 记录层缺必填字段 %s（客户端 Load 会失败）：%v", k, adv)
		}
	}
	if _, ok := adv["begin_date_i"].(float64); !ok {
		t.Fatalf("begin_date_i 必须是数值：%v", adv["begin_date_i"])
	}
	if _, has := lad["setting"]; !has {
		t.Fatalf("LoginAdventureData 缺 setting（客户端 Load 会失败）：%v", lad)
	}
	if lad["today_days"] != "" {
		t.Fatalf("today_days 应为空串使客户端跳过弹窗，得到 %v", lad["today_days"])
	}

	data2 := gameData(t, ts.URL, "/gachas/choose_attr_page/", uid, sk, s0, s1, "")
	if _, has := data2["country_pick_page"]; !has {
		t.Fatalf("data.country_pick_page 缺失：%v", data2)
	}
}
