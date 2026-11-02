// guild_storage_wire_test.go 蓄水库 HTTP 级回放测试（GUILDCLOSE350 GC-03）。
//
// 走真实加密通道（cn35wire/加密 POST + requireData 形态），断言：
//   - /guilds/bp_storage 确认态 body 逐字（等级/千分位余额/上限/提示句）；
//   - 确认态带 type=yes_no + url，拒绝态**不带** type/url（客户端 OK 弹窗）；
//   - /guilds/takeover_bp_storage 成功态 {"data":{"user":<全量快照>},"modal":完成}；
//   - 取出后 user.bp 已加上取出量、guild_asset_count 不变；
//   - 重复请求（同缺省幂等键）不重复扣池、不重复回 BP；
//   - 拒绝态同样下发 data.user（原服该端点顶层恒有 data/user）。
package cn35game_test

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// storageServer 蓄水库面最小依赖装配（真实 SQLite + 公会读写面）。
func storageServer(t *testing.T, stateDir string) (*httptest.Server, *cn35store.Store) {
	t.Helper()
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	reg := httpapi.NewRegistry()
	if n := cn35game.Register(reg, cn35game.Deps{
		Accounts:   st,
		Sessions:   st,
		Guild:      st,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
	}); n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(srv.Handler()), st
}

// guildStorageModal 从回包取 modal 文档。
func guildStorageModal(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	m, ok := doc["modal"].(map[string]any)
	if !ok {
		t.Fatalf("回包缺 modal 对象: %v", doc)
	}
	return m
}

func TestGuildBPStorageHTTPLayer(t *testing.T) {
	dir := t.TempDir()
	ts, st := storageServer(t, dir)
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-t3-storage")

	// --- 准备：真实公会 + 原服样本状态（bp_storage Lv.79 / 池 47,345 / bp=0）---
	g, err := st.CreateGuild(uid, "蓄水会", "2", "1", "", "")
	if err != nil {
		t.Fatalf("CreateGuild: %v", err)
	}
	if _, err = st.DB().Exec(`INSERT INTO cn35_guild_equipment
		(guild_id,type_id,level,point,updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(guild_id,type_id) DO UPDATE SET point=excluded.point`,
		g.ID, "bp_storage", 79, 28562863, "2026-10-28"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`INSERT INTO cn35_guild_bp_storage
		(guild_id,amount,updated_at) VALUES (?,?,?)
		ON CONFLICT(guild_id) DO UPDATE SET amount=excluded.amount`,
		g.ID, 47345, "2026-10-28"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`UPDATE cn35_player_profiles SET bp=0,max_bp=600 WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	var assetBefore int64
	// 先给一笔公会资材，让「guild_asset_count 不变」的断言有意义。
	if _, err = st.DB().Exec(`INSERT INTO cn35_player_balances
		(user_id,currency,amount,updated_at) VALUES (?,'guild_asset',1234,'2026-10-28')
		ON CONFLICT(user_id,currency) DO UPDATE SET amount=1234`, uid); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency='guild_asset'`, uid).Scan(&assetBefore); err != nil {
		t.Fatal(err)
	}

	// --- 确认态：body 逐字对齐原服样本 ---
	doc := gameJSON(t, ts.URL, "/guilds/bp_storage", uid, sk, s0, s1, "", "")
	modal := guildStorageModal(t, doc)
	if modal["title"] != "确认" {
		t.Fatalf("title = %v", modal["title"])
	}
	if modal["type"] != "yes_no" {
		t.Fatalf("确认态必须 type=yes_no（客户端双按钮分支）: %v", modal)
	}
	if modal["url"] != "/guilds/takeover_bp_storage" {
		t.Fatalf("url = %v", modal["url"])
	}
	wantBody := "蓄水库 (Lv.79)\n47,345/49,000\n\n要取出BP吗？"
	if modal["body"] != wantBody {
		t.Fatalf("确认 body 非逐字:\n got=%q\nwant=%q", modal["body"], wantBody)
	}

	// --- 取出成功态：data.user 全量 + 完成 modal ---
	doc = gameJSON(t, ts.URL, "/guilds/takeover_bp_storage", uid, sk, s0, s1, "", "")
	modal = guildStorageModal(t, doc)
	if modal["title"] != "完成" || modal["body"] != "从蓄水库中取出了600BP。" {
		t.Fatalf("完成 modal 非逐字: %v", modal)
	}
	if _, ok := modal["type"]; ok {
		t.Fatalf("完成态 modal 不应带 type（原服样本无 type → OK 弹窗）: %v", modal)
	}
	data, ok := doc["data"].(map[string]any)
	if !ok {
		t.Fatalf("缺 data 对象: %v", doc)
	}
	user, ok := data["user"].(map[string]any)
	if !ok {
		t.Fatalf("缺 data.user 对象: %v", data)
	}
	// data.user 必须是 preset_data 同源的全量快照，不是 {bp:...} 残缺对象。
	if len(user) < 20 {
		t.Fatalf("data.user 键数过少（疑似手写残缺对象）: %d keys", len(user))
	}
	for _, k := range []string{"id", "name", "level", "exp", "max_exp", "ap", "max_ap",
		"bp", "max_bp", "medal", "gacha_point", "leader_unit_id",
		"login_bonus_receivable_sec", "guild_name", "guild_asset_count",
		"exp_table", "need_exp"} {
		if _, ok := user[k]; !ok {
			t.Fatalf("data.user 缺原服键 %q", k)
		}
	}
	if bp, _ := user["bp"].(float64); bp != 600 {
		t.Fatalf("data.user.bp = %v, want 600（已加上取出量）", user["bp"])
	}
	if maxBP, _ := user["max_bp"].(float64); maxBP != 600 {
		t.Fatalf("data.user.max_bp = %v", user["max_bp"])
	}
	if got, _ := user["guild_asset_count"].(float64); int64(got) != assetBefore {
		t.Fatalf("guild_asset_count 变了: %v → %d", user["guild_asset_count"], assetBefore)
	}
	// DB 侧同事务生效：池 47,345→46,745，BP 0→600。
	var pool, bp int64
	if err = st.DB().QueryRow(`SELECT amount FROM cn35_guild_bp_storage WHERE guild_id=?`, g.ID).Scan(&pool); err != nil {
		t.Fatal(err)
	}
	if err = st.DB().QueryRow(`SELECT bp FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&bp); err != nil {
		t.Fatal(err)
	}
	if pool != 46745 || bp != 600 {
		t.Fatalf("取出未落库: pool=%d bp=%d", pool, bp)
	}

	// --- 重复请求：同缺省幂等键 → 不重复扣 ---
	doc = gameJSON(t, ts.URL, "/guilds/takeover_bp_storage", uid, sk, s0, s1, "", "")
	modal = guildStorageModal(t, doc)
	if modal["title"] != "完成" {
		t.Fatalf("重试应回放原成功形态: %v", modal)
	}
	var pool2, bp2 int64
	_ = st.DB().QueryRow(`SELECT amount FROM cn35_guild_bp_storage WHERE guild_id=?`, g.ID).Scan(&pool2)
	_ = st.DB().QueryRow(`SELECT bp FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&bp2)
	if pool2 != pool || bp2 != bp {
		t.Fatalf("重复请求重复扣: pool=%d bp=%d", pool2, bp2)
	}

	// --- 拒绝态（BP 已满 + 池非空）：body 逐字、无 type/url，但仍带 data.user ---
	doc = gameJSON(t, ts.URL, "/guilds/bp_storage", uid, sk, s0, s1, "", "")
	modal = guildStorageModal(t, doc)
	wantRefuse := "蓄水库 (Lv.79)\n46,745/49,000\n\n现在，无法从蓄水库中取出BP。"
	if modal["body"] != wantRefuse {
		t.Fatalf("拒绝 body 非逐字:\n got=%q\nwant=%q", modal["body"], wantRefuse)
	}
	if _, ok := modal["type"]; ok {
		t.Fatalf("拒绝态不得带 type（原服走 OK 弹窗）: %v", modal)
	}
	if _, ok := modal["url"]; ok {
		t.Fatalf("拒绝态不得带 url: %v", modal)
	}
	// 换一个显式幂等键，绕开上一笔的重放窗口，才真正走到「无处可取」的拒绝路径。
	doc = gameJSON(t, ts.URL, "/guilds/takeover_bp_storage", uid, sk, s0, s1,
		"op_key=http-refuse", "")
	modal = guildStorageModal(t, doc)
	if _, ok := doc["data"].(map[string]any); !ok {
		t.Fatalf("拒绝态也必须下发 data.user: %v", doc)
	}
	if modal["title"] != "无法取出" {
		t.Fatalf("拒绝 modal title = %v", modal["title"])
	}
	if pool3, bp3 := pool, bp; true {
		_ = st.DB().QueryRow(`SELECT amount FROM cn35_guild_bp_storage WHERE guild_id=?`, g.ID).Scan(&pool3)
		_ = st.DB().QueryRow(`SELECT bp FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&bp3)
		if pool3 != pool || bp3 != bp {
			t.Fatalf("拒绝不应改状态: pool=%d bp=%d", pool3, bp3)
		}
	}
}

// TestGuildBPStorageHTTPNotMember 未入会走明确弹窗（不 500、不改任何状态）。
func TestGuildBPStorageHTTPNotMember(t *testing.T) {
	dir := t.TempDir()
	ts, st := storageServer(t, dir)
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-t3-nomember")
	doc := gameJSON(t, ts.URL, "/guilds/bp_storage", uid, sk, s0, s1, "", "")
	modal := guildStorageModal(t, doc)
	if modal["title"] != "无法操作" {
		t.Fatalf("未入会应回无法操作弹窗: %v", modal)
	}
	// takeover 未入会：仍要有 data.user（原服顶层形状），且没有完成弹窗。
	doc = gameJSON(t, ts.URL, "/guilds/takeover_bp_storage", uid, sk, s0, s1, "", "")
	if _, ok := doc["data"].(map[string]any); !ok {
		t.Fatalf("未入会也必须下发 data.user: %v", doc)
	}
	if m := guildStorageModal(t, doc); m["title"] == "完成" {
		t.Fatalf("未入会不应回取出成功: %v", m)
	}
}
