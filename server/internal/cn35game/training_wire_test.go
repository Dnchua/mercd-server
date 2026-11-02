// training_wire_test.go STAT350-05A：/user_units/training 协议回放测试。
// 真实 SQLite + cardcatalog + 加密 game 通道，覆盖成功/越界/陈旧/余额不足/
// 跨账号/未启用子项拒绝/重启持久化/不触碰其他状态。
package cn35game_test

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35roster"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

// trainingServer 装配含 05A 写面的测试服务（全 store 注入，便于复用读面断言）。
func trainingServer(t *testing.T, stateDir string) (*httptest.Server, *cn35store.Store) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	// ROSTER350：抽卡执行面只认已发布版本——测试库先种子化（与
	// mercd35 启动路径同一份 SeedGachaRoster）。
	assetsDir := filepath.Join("..", "..", "assets", "cn350")
	in, _, err := cn35roster.LoadImport(assetsDir, filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("名册清单加载: %v", err)
	}
	if _, _, err := cn35game.SeedGachaRoster(st, in, "test"); err != nil {
		t.Fatalf("名册种子化: %v", err)
	}
	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		Accounts:    st,
		Sessions:    st,
		Inventory:   st,
		Decks:       st,
		Quests:      st,
		Battles:     st,
		Training:    st,
		Gacha:       st,
		GachaConfig: st,
		// P0 批（2026-09-28）：同伴/魔宠写面 + 礼物领取 + 读面内容池。
		UnitOps:        st,
		MonsterOps:     st,
		PresentReceive: st,
		Presents:       st,
		Infos:          st,
		Catalog:        cn35game.NewRosterCatalog(cat, st),
		ServerHost:     "127.0.0.1:18091",
		AssetsDir:      assetsDir,
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

// statmodelPanel 直接按 statmodel 计算 152 在 (level, dup) 的面板（期望值来源）。
func statmodelPanel(id, level, dup int) (statmodel.UnitPanel, error) {
	if id != 152 {
		return statmodel.UnitPanel{}, nil
	}
	// AtMin 用生产同源口径：buildUnitDoc 先过 memorial_masterdata.json
	// unit_text.152.atk_override=3900（data-align 2026-10-24），不是 catalog 3600。
	return statmodel.ComputeUnitPanel(statmodel.UnitInput{
		Type: 3, Rare: 5, HpMin: 3680, AtMin: 3900,
		Level: level, DuplicatedCount: dup,
	})
}

// TestTrainingHappyPath 152 Lv1→Lv2：扣 60 gacha_point（expTable 1-0），
// 只改 level，回推 data.user_units 的面板等于 statmodel 的 Lv2 输出。
func TestTrainingHappyPath(t *testing.T) {
	ts, st := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-train-ok")
	insertUnit(t, st, uid, 152, 1)

	wantCost, err := cn35game.UnitTrainingCost(1, 2)
	if err != nil || wantCost != 60 {
		t.Fatalf("成本 1→2 = %d err=%v want 60", wantCost, err)
	}
	doc := gameJSON(t, ts.URL, "/user_units/training", uid, sk, s0, s1,
		"id=152&before=1&after=2", "_method=GET")
	if doc["status"] != "success" {
		t.Fatalf("训练应成功: %v", doc)
	}
	if _, has := doc["modal"]; has {
		t.Fatalf("成功响应不得带 modal（客户端会当失败）: %v", doc)
	}
	data := doc["data"].(map[string]any)
	u152 := rowByID(t, data["user_units"].([]any), 152)
	if u152["level"].(float64) != 2 {
		t.Fatalf("回推 level=%v want 2", u152["level"])
	}
	// Lv2 面板须等于 statmodel 输出（不是 Lv1 值）。
	p2, err := statmodelPanel(152, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if u152["hp"].(float64) != float64(p2.HP) || u152["at"].(float64) != float64(p2.At) {
		t.Fatalf("回推面板 hp/at=%v/%v want %d/%d", u152["hp"], u152["at"], p2.HP, p2.At)
	}
	if u152["hp"].(float64) == 3680 {
		t.Fatalf("面板仍是 Lv1 基础值: %v", u152["hp"])
	}
	user := data["user"].(map[string]any)
	want := float64(302 - wantCost) // cn35store 建号种子 gacha_point=302
	if user["gacha_point"].(float64) != want {
		t.Fatalf("data.user.gacha_point=%v want %v", user["gacha_point"], want)
	}
	// 落库核对：level 更新、duplicated_count 未动、余额已扣。
	var level, dup int
	if err := st.DB().QueryRow(`SELECT level,duplicated_count FROM cn35_player_units
		WHERE user_id=? AND unit_id=152`, uid).Scan(&level, &dup); err != nil {
		t.Fatal(err)
	}
	if level != 2 || dup != 0 {
		t.Fatalf("落库 level=%d dup=%d want 2/0（training 不得改觉醒）", level, dup)
	}
	var bal int64
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&bal); err != nil {
		t.Fatal(err)
	}
	if bal != int64(302-wantCost) {
		t.Fatalf("落库 gacha_point=%d want %d", bal, 302-wantCost)
	}
}

// TestTrainingRejectsAboveCurrentMaxLevel after 超当前觉醒上限必须拒绝且不写库。
func TestTrainingRejectsAboveCurrentMaxLevel(t *testing.T) {
	ts, st := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-train-oor")
	insertUnit(t, st, uid, 152, 70) // dup=0 → current_max_level=70

	doc := gameJSON(t, ts.URL, "/user_units/training", uid, sk, s0, s1,
		"id=152&before=70&after=71", "_method=GET")
	if doc["status"] != "error" || doc["modal"] == nil {
		t.Fatalf("越界应 error+modal: %v", doc)
	}
	assertTrainingUnchanged(t, st, uid, 70, 302)
}

// TestTrainingRejectsStaleBefore before 与当前等级不符（重放/并发）必须拒绝。
func TestTrainingRejectsStaleBefore(t *testing.T) {
	ts, st := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-train-stale")
	insertUnit(t, st, uid, 152, 5)

	doc := gameJSON(t, ts.URL, "/user_units/training", uid, sk, s0, s1,
		"id=152&before=1&after=2", "_method=GET")
	if doc["status"] != "error" || doc["modal"] == nil {
		t.Fatalf("陈旧 before 应 error+modal: %v", doc)
	}
	assertTrainingUnchanged(t, st, uid, 5, 302)
}

// TestTrainingInsufficientNoPartialWrite 余额不足：整笔回滚（level 与余额都不变）。
func TestTrainingInsufficientNoPartialWrite(t *testing.T) {
	ts, st := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-train-poor")
	insertUnit(t, st, uid, 152, 1)
	if _, err := st.DB().Exec(`UPDATE cn35_player_balances SET amount=10
		WHERE user_id=? AND currency='gacha_point'`, uid); err != nil {
		t.Fatal(err)
	}
	doc := gameJSON(t, ts.URL, "/user_units/training", uid, sk, s0, s1,
		"id=152&before=1&after=2", "_method=GET")
	if doc["status"] != "error" || doc["modal"] == nil {
		t.Fatalf("余额不足应 error+modal: %v", doc)
	}
	assertTrainingUnchanged(t, st, uid, 1, 10)
}

// TestTrainingCrossAccountRejected 跨账号（未持有该单位）拒绝，且不泄漏他人状态。
func TestTrainingCrossAccountRejected(t *testing.T) {
	ts, st := trainingServer(t, t.TempDir())
	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "dev-train-a")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "dev-train-b")
	insertUnit(t, st, uidA, 152, 1)

	doc := gameJSON(t, ts.URL, "/user_units/training", uidB, skB, s0B, s1B,
		"id=152&before=1&after=2", "_method=GET")
	if doc["status"] != "error" || doc["modal"] == nil {
		t.Fatalf("跨账号应 error+modal: %v", doc)
	}
	assertTrainingUnchanged(t, st, uidA, 1, 302)
	// B 的库存仍为空。
	if got := gameData(t, ts.URL, "/user_units", uidB, skB, s0B, s1B, "")["user_units"].([]any); len(got) != 0 {
		t.Fatalf("B 不应看到 A 的单位: %v", got)
	}
	_ = skA
	_ = s0A
	_ = s1A
}

// TestTrainingRestartPersistence 训练后重启（重开库文件）等级保持。
func TestTrainingRestartPersistence(t *testing.T) {
	dir := t.TempDir()
	ts, st := trainingServer(t, dir)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-train-persist")
	insertUnit(t, st, uid, 152, 1)
	if doc := gameJSON(t, ts.URL, "/user_units/training", uid, sk, s0, s1,
		"id=152&before=1&after=2", "_method=GET"); doc["status"] != "success" {
		t.Fatalf("训练失败: %v", doc)
	}
	st.Close()
	st2, err := cn35store.Open(filepath.Join(dir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("重开库: %v", err)
	}
	defer st2.Close()
	recs, err := st2.Units(uid)
	if err != nil || len(recs) != 1 || recs[0].Level != 2 {
		t.Fatalf("重启后等级未保持: %v %v", recs, err)
	}
}

// TestGrowthUnsupportedEndpointsRejected 05B/05C/05D 闸门未过：未注册的
// /user_units/* 写面明确拒绝（modal），不得伪造成功。
func TestGrowthUnsupportedEndpointsRejected(t *testing.T) {
	ts, st := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-train-unsup")
	insertUnit(t, st, uid, 152, 1)
	for _, p := range []string{
		"/user_units/upgrade?unit_id=152&after_level=1",
		"/user_units/awake?id=152",
		"/user_units/powerup?id=152",
	} {
		doc := gameJSON(t, ts.URL, p, uid, sk, s0, s1, "", "_method=GET")
		if doc["status"] != "error" || doc["modal"] == nil {
			t.Fatalf("%s 应 error+modal（不得伪造成功）: %v", p, doc)
		}
	}
	assertTrainingUnchanged(t, st, uid, 1, 302)
}

// TestUnitTrainingCost 客户端 expTable 差值的边界与向量。
func TestUnitTrainingCost(t *testing.T) {
	cases := []struct {
		before, after, want int
		ok                  bool
	}{
		{1, 2, 60, true},
		{2, 3, 120, true},
		{1, 145, 1759500, true},
		{144, 145, 29000, true},
		{1, 1, 0, false},   // after 必须 > before
		{2, 1, 0, false},   // 递减
		{0, 1, 0, false},   // before 越界
		{1, 146, 0, false}, // after 超出表长
	}
	for _, c := range cases {
		got, err := cn35game.UnitTrainingCost(c.before, c.after)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("UnitTrainingCost(%d,%d)=%d,%v want %d,nil", c.before, c.after, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("UnitTrainingCost(%d,%d) 应报错，got %d", c.before, c.after, got)
		}
	}
}

// TestTrainingPlainQueryVariant 客户端以明文 query 拼 URL
// （`Setting.getBaseURL() + "/user_units/training?id=..&before=..&after=.."`），
// 服务端须走 decodeQueryParam 的明文分支（非 e= 加密段）。
func TestTrainingPlainQueryVariant(t *testing.T) {
	ts, st := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-train-plain")
	insertUnit(t, st, uid, 152, 1)

	plain := gameCall(t, ts.URL, "/user_units/training?id=152&before=1&after=2",
		uid, sk, s0, s1, "", "_method=GET")
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatalf("响应非 JSON: %v: %.200s", err, plain)
	}
	if doc["status"] != "success" {
		t.Fatalf("明文 query 请求应成功: %v", doc)
	}
	var level int
	if err := st.DB().QueryRow(`SELECT level FROM cn35_player_units
		WHERE user_id=? AND unit_id=152`, uid).Scan(&level); err != nil {
		t.Fatal(err)
	}
	if level != 2 {
		t.Fatalf("明文 query 请求未生效: level=%d", level)
	}
}

// assertTrainingUnchanged 断言训练失败后 level 与 gacha_point 都未变。
func assertTrainingUnchanged(t *testing.T, st *cn35store.Store, uid string, wantLevel int, wantPoint int64) {
	t.Helper()
	var level int
	if err := st.DB().QueryRow(`SELECT level FROM cn35_player_units
		WHERE user_id=? AND unit_id=152`, uid).Scan(&level); err != nil {
		t.Fatal(err)
	}
	if level != wantLevel {
		t.Fatalf("失败请求改动了 level: %d want %d", level, wantLevel)
	}
	var bal int64
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&bal); err != nil {
		t.Fatal(err)
	}
	if bal != wantPoint {
		t.Fatalf("失败请求改动了 gacha_point: %d want %d", bal, wantPoint)
	}
}
