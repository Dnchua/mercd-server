// battle_wire_test.go STORY350-07 剧情战斗写链协议回放测试：
// 真实 cn35store(SQLite) × cardcatalog × cn35wire 加密通道，走
// execute→ap_use→log_battle→result/lose/continue 全链，
// 断言 modal 校验、AP 幂等、结算入账、already_received、双账号隔离与重启保持。
package cn35game_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// battleServer 装配战斗链全依赖（真实 SQLite + catalog + 战斗写面）。
func battleServer(t *testing.T, stateDir string) (*httptest.Server, *cn35store.Store) {
	return battleServerSeed(t, stateDir, 0)
}

// battleServerSeed 同 battleServer，可指定战斗摇奖 rand 源 seed
// （剧情 seed 掉落/随机背景确定性回放：43 首抽 Intn(10000)<200）。
func battleServerSeed(t *testing.T, stateDir string, randSeed int64) (*httptest.Server, *cn35store.Store) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:   st,
		Sessions:   st,
		Inventory:  st,
		Decks:      st,
		Quests:     st,
		Battles:    st,
		Catalog:    cat,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
		// 固定 seed：剧情 seed 掉落（seed0 首次 Intn(10000)=3274 ≥ 200 不掉）
		// 与随机背景确定化，避免回放测试随概率抖动。
		BattleRand: mathrand.New(mathrand.NewSource(randSeed)),
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	return ts, st
}

func gameJSON(t *testing.T, base, path, uid, sk, s0, s1, query, form string) map[string]any {
	t.Helper()
	plain := gameCall(t, base, path, uid, sk, s0, s1, query, form)
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatalf("响应 JSON: %v: %.300s", err, plain)
	}
	return doc
}

func seedBattleUnits(t *testing.T, st *cn35store.Store, uid string) {
	t.Helper()
	for _, id := range []int{336, 348} {
		if err := st.WithTx(func(tx *sql.Tx) error {
			return cn35store.InsertUnitTx(tx, uid, cn35game.UnitRecord{UnitID: id, Level: 10})
		}); err != nil {
			t.Fatalf("插入单位 %d: %v", id, err)
		}
	}
}

func setCoin(t *testing.T, st *cn35store.Store, uid string, amount int64) {
	t.Helper()
	if _, err := st.DB().Exec(`UPDATE cn35_player_balances SET amount=? WHERE user_id=? AND currency='coin'`, amount, uid); err != nil {
		t.Fatal(err)
	}
}

func setAPNow(t *testing.T, st *cn35store.Store, uid string, ap int) {
	t.Helper()
	if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET ap=? WHERE user_id=?`, ap, uid); err != nil {
		t.Fatal(err)
	}
}

// urlQueryEscape 表单值转义（log_battle 的 JSON 上行用）。
func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			b.WriteString("%" + strings.ToUpper(fmt.Sprintf("%02x", c)))
		}
	}
	return b.String()
}

// TestExecuteContract execute 成功路径：quest0 oracle 键集 + 会话 URL +
// difficulty 回显 + data.user + clear_count + 单位/魔宠文档。
func TestExecuteContract(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServer(t, dir)
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-b-exec")
	seedBattleUnits(t, st, uid)

	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348&page_message=name%3DQuestSelect", "_method=GET")
	// 契约必带键。
	for _, k := range []string{"quest", "appear_table", "ap_use_url", "result_url",
		"lose_url", "next_page_message", "units", "monsters", "main_character",
		"asset_names", "battle_log", "data"} {
		if _, ok := doc[k]; !ok {
			t.Fatalf("execute 缺键 %s: %v", k, doc)
		}
	}
	q := doc["quest"].(map[string]any)
	if q["difficulty_id"] != "normal" || q["use_ap"].(float64) != 5 {
		t.Fatalf("quest 难度回显/AP: %v", q)
	}
	if q["id"].(float64) != 1 || q["name"] == "" {
		t.Fatalf("quest 字段: %v", q)
	}
	if _, ok := q["powerup_conf"].(map[string]any)["1"]; !ok {
		t.Fatalf("powerup_conf 缺失: %v", q["powerup_conf"])
	}
	// 三 URL 同 bid 且含 ?（客户端直接 &拼接）。
	var bid string
	for _, k := range []string{"ap_use_url", "result_url", "lose_url"} {
		u := doc[k].(string)
		if !strings.Contains(u, "?bid=") {
			t.Fatalf("%s 缺 bid: %s", k, u)
		}
		b := strings.SplitN(u, "bid=", 2)[1]
		if bid == "" {
			bid = b
		} else if bid != b {
			t.Fatalf("三 URL bid 不一致: %v", doc)
		}
	}
	if doc["battle_log"] != true {
		t.Fatalf("battle_log 应为 true: %v", doc["battle_log"])
	}
	if doc["clear_count"].(float64) != 0 {
		t.Fatalf("clear_count: %v", doc["clear_count"])
	}
	units := doc["units"].([]any)
	// 新手起始五卡保底后，新建默认编队 = 队长 + 其余四张起始卡（336 升序
	// 排在起始卡之后不入编）。
	if len(units) != 5 {
		t.Fatalf("units 应为 party_id=001 对应队伍成员: %v", units)
	}
	// 编队 ≥2 位时每位 uuid 非空且互不相同（客户端 unitMap 以 uuid 为键，
	// 撞键会导致只能派出第一位）。
	seenUUIDs := map[string]bool{}
	for _, r := range units {
		u, _ := r.(map[string]any)["uuid"].(string)
		if u == "" {
			t.Fatalf("units[] uuid 为空: %v", r)
		}
		if seenUUIDs[u] {
			t.Fatalf("units[] uuid 重复 %q: %v", u, units)
		}
		seenUUIDs[u] = true
	}
	if _, ok := doc["monsters"].(map[string]any)["1"]; !ok {
		t.Fatalf("monsters 缺 id1: %v", doc["monsters"])
	}
	// 参战单位非空特效须进 asset_names（客户端 ShowEffect 按 bundleObjects
	// 键播放，bundle 由 asset_names 加载）；348 target_effect=effect_hit，
	// 336 特效键全空不下发。整表无重复项。
	assetNames := doc["asset_names"].([]any)
	seenAsset := map[string]bool{}
	foundHitFx := false
	for _, a := range assetNames {
		s := a.(string)
		if seenAsset[s] {
			t.Fatalf("asset_names 重复项 %q: %v", s, assetNames)
		}
		seenAsset[s] = true
		if s == "effect_ss/effect_hit" {
			foundHitFx = true
		}
	}
	if !foundHitFx {
		t.Fatalf("asset_names 缺参战单位特效 effect_ss/effect_hit: %v", assetNames)
	}
	// data.user 快照注入。
	user := doc["data"].(map[string]any)["user"].(map[string]any)
	if user["ap"].(float64) != 20 || user["quest_id"].(float64) != 1 {
		t.Fatalf("data.user: %v", user)
	}
	if user["int_ap_updated_at"] == nil || user["current_ap_diff"].(float64) != 0 {
		t.Fatalf("execute AP 锚点/current_ap_diff: %v", user)
	}
	// execute 仅建档；既有协议由后续 use_ap 请求扣点。
	var persistedAP int
	if err := st.DB().QueryRow(`SELECT ap FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&persistedAP); err != nil {
		t.Fatal(err)
	}
	if persistedAP != 20 {
		t.Fatalf("execute 响应前不应扣 AP，got %d", persistedAP)
	}
	// hard 难度：use_ap=10 且 difficulty_id 原样回显（客户端池命中键），
	// 数值按 battle.difficulty.hard 缩放（story-01 难度分表）。
	d2 := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=hard&party_id=001", "_method=GET")
	q2 := d2["quest"].(map[string]any)
	if q2["difficulty_id"] != "hard" || q2["use_ap"].(float64) != 10 {
		t.Fatalf("hard 难度回显: %v", q2)
	}
	// hard 出怪 hp 应为 normal×1.8（quest1 方案基值 1120→2016）。
	at := doc["appear_table"].(map[string]any)["1"].(map[string]any)
	normalHP := 0
	for _, ev := range at {
		p := ev.(map[string]any)["params"].(map[string]any)
		if hp := p["hp"].(float64); hp > 0 {
			normalHP = int(hp)
			break
		}
	}
	at2 := d2["appear_table"].(map[string]any)["1"].(map[string]any)
	foundScaled := false
	for _, ev := range at2 {
		p := ev.(map[string]any)["params"].(map[string]any)
		if int(p["hp"].(float64)) == normalHP*18/10 {
			foundScaled = true
		}
	}
	if !foundScaled || normalHP <= 0 {
		t.Fatalf("hard 难度 hp 缩放缺失: normal=%d hard=%v", normalHP, at2)
	}
}

// TestExecuteSeedMonsterAssets 参战魔宠资源声明（2026-09-28 实机阻塞：
// SeedControllerPanel.onInit / SWFQuestCutin.ShowCutin 按 user_monsters 缓存
// 的 id 直查 quest.textures["monster/monster_btn_<id>"|"monster_large_ns_<id>"]）。
// 玩家装备但不在 appear_table 的魔宠必须进 asset_names；与敌方同 id 不重复；
// 不下发场上件 monster/monster_<id>（AddMonster 只读 waveAppearTable）。
func TestExecuteSeedMonsterAssets(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServer(t, dir)
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-b-seed")
	seedBattleUnits(t, st, uid)
	for _, id := range []int{152, 161} {
		if err := st.WithTx(func(tx *sql.Tx) error {
			return cn35store.InsertUnitTx(tx, uid, cn35game.UnitRecord{UnitID: id, Level: 10})
		}); err != nil {
			t.Fatal(err)
		}
	}
	equip := func(uuid string, monsterID, hostUnit int) {
		if err := st.WithTx(func(tx *sql.Tx) error {
			return cn35store.InsertMonsterTx(tx, uid, cn35game.MonsterRecord{
				UUID: uuid, MonsterID: monsterID, Level: 1})
		}); err != nil {
			t.Fatalf("插入魔宠 %s: %v", uuid, err)
		}
		if err := st.EquipMonster(uid, uuid, hostUnit); err != nil {
			t.Fatalf("装备魔宠 %s: %v", uuid, err)
		}
	}
	// 34/39 不在 quest1 appear_table（敌方=1,2）；1 与敌方同 id 验去重。
	equip("m-34", 34, 161)
	equip("m-39", 39, 348)
	equip("m-01", 1, 152)

	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001", "_method=GET")
	raw, ok := doc["asset_names"].([]any)
	if !ok {
		t.Fatalf("asset_names: %v", doc)
	}
	count := map[string]int{}
	for _, a := range raw {
		count[a.(string)]++
	}
	for _, want := range []string{
		"monster/monster_btn_34", "monster/monster_large_ns_34",
		"monster/monster_btn_39", "monster/monster_large_ns_39",
		"monster/monster_btn_1", "monster/monster_large_ns_1",
	} {
		if count[want] != 1 {
			t.Fatalf("asset_names 应恰好含一次 %s: %v", want, raw)
		}
	}
	if count["monster/monster_34"] != 0 || count["monster/monster_39"] != 0 {
		t.Fatalf("参战种子不下发场上件: %v", raw)
	}
}

// TestExecuteValidationModals 校验失败全走 modal 键（客户端弹窗回退）。
func TestExecuteValidationModals(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServer(t, dir)
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-b-modal")
	seedBattleUnits(t, st, uid)

	cases := []struct {
		name, path, query string
	}{
		{"未知关卡", "/quests/execute/99.json", "mode=quest&id=99&difficulty_id=normal"},
		{"难度非法", "/quests/execute/1.json", "mode=quest&id=1&difficulty_id=impossible"},
		{"缺难度", "/quests/execute/1.json", "mode=quest&id=1"},
		{"锁定关", "/quests/execute/2.json", "mode=quest&id=2&difficulty_id=normal"},
		{"模式不符", "/quests/execute/1.json", "mode=raid&id=1&difficulty_id=normal"},
		{"id不一致", "/quests/execute/1.json", "mode=quest&id=2&difficulty_id=normal"},
	}
	for _, tc := range cases {
		doc := gameJSON(t, ts.URL, tc.path, uid, sk, s0, s1, tc.query, "_method=GET")
		modal, ok := doc["modal"].(map[string]any)
		if !ok {
			t.Fatalf("%s 应回 modal: %v", tc.name, doc)
		}
		if modal["title"] == "" || modal["body"] == "" {
			t.Fatalf("%s modal 缺文案: %v", tc.name, modal)
		}
	}
	// unit_ids 是全库存提示，伪造/缺失内容不能改写服务端持久队伍。
	validWithForgedInventory := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=999999", "_method=GET")
	if validWithForgedInventory["quest"] == nil {
		t.Fatalf("execute 不应使用客户端全库存 unit_ids 代替存储队伍: %v", validWithForgedInventory)
	}
	// AP 不足（unit_ids 全库存合法，AP=1<2）。
	setAPNow(t, st, uid, 1)
	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001", "_method=GET")
	if doc["modal"] == nil {
		t.Fatalf("AP 不足应回 modal: %v", doc)
	}
}

// TestBattleChainWire 全链：execute→ap_use(幂等)→log_battle→result(结算)→
// 重放 already_received→再刷（clear 累加）。
func TestBattleChainWire(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServer(t, dir)
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-b-chain")
	seedBattleUnits(t, st, uid)

	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348", "_method=GET")
	bid := strings.SplitN(doc["ap_use_url"].(string), "bid=", 2)[1]

	// ap_use：AP 20→15（quest1 normal use_ap=5）；重复调用不重复扣。
	ap1 := gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+bid, "_method=GET")
	if ap1["status"] != "success" {
		t.Fatalf("ap_use: %v", ap1)
	}
	if ap := ap1["data"].(map[string]any)["user"].(map[string]any)["ap"].(float64); ap != 15 {
		t.Fatalf("ap_use 后 AP 应为 15: %v", ap)
	}
	ap2 := gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+bid, "_method=GET")
	if ap2["status"] != "success" {
		t.Fatalf("重复 ap_use: %v", ap2)
	}
	var apDB int
	if err := st.DB().QueryRow(`SELECT ap FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&apDB); err != nil || apDB != 15 {
		t.Fatalf("重复 ap_use 重复扣点: ap=%d err=%v", apDB, err)
	}

	// log_battle：battle_record 归档（POST 表单）。
	lb := gameJSON(t, ts.URL, "/quests/log_battle", uid, sk, s0, s1, "",
		"_method=POST&battle_record="+urlQueryEscape(`{"battleId":1,"waves":[{"1":[]}]}`))
	var nLog int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_battle_logs WHERE user_id=? AND quest_id=1`, uid).Scan(&nLog); err != nil || nLog != 1 {
		t.Fatalf("log_battle 归档: n=%d err=%v resp=%v", nLog, err, lb)
	}

	// result 首结算：success + data.user(quest_id=2/ap=15) + quests_1 解锁推进 +
	// clear_count=1 + 奖励入账（gacha_point +300=quest1 normal 金币档；
	// seed 未摇中不掉落——BattleRand seed0 首次 Intn(10000)=3274≥200）。
	// 起始 gacha_point 余额读库取真值，不再硬编码（发放管线会改）。
	var gp0 int64
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&gp0); err != nil {
		t.Fatal(err)
	}
	res := gameJSON(t, ts.URL, "/quests/result", uid, sk, s0, s1, "bid="+bid+"&time=45.5&mc=0", "_method=GET")
	if res["status"] != "success" {
		t.Fatalf("result: %v", res)
	}
	if res["spend_time"].(float64) != 45 {
		t.Fatalf("spend_time 应回显用时: %v", res["spend_time"])
	}
	data := res["data"].(map[string]any)
	u := data["user"].(map[string]any)
	// LOGIN350-02 起升级 AP 叠加增量（不再补满）：战斗中先扣 5 AP → 15，
	// 本次结算 exp 50 → level2（max_ap 21，增量 +1）→ ap=16。
	if u["quest_id"].(float64) != 2 || u["ap"].(float64) != 16 {
		t.Fatalf("结算后 user 状态: %v", u)
	}
	q1list := data["quests_1"].([]any)
	if len(q1list) != 2 {
		t.Fatalf("结算后 quests_1 应解锁 quest1+2: %v", q1list)
	}
	var gp int64
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&gp); err != nil {
		t.Fatal(err)
	}
	if gp != gp0+300 {
		t.Fatalf("gacha_point 入账: base=%d got=%d", gp0, gp)
	}
	var ga int64
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='guild_asset'`, uid).Scan(&ga); err != nil {
		t.Fatal(err)
	}
	if ga != 100 {
		t.Fatalf("guild_asset 入账（quest1 normal 资材档 100）: %d", ga)
	}
	var mn int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_player_monsters WHERE user_id=?`, uid).Scan(&mn); err != nil {
		t.Fatal(err)
	}
	if mn != 0 {
		t.Fatalf("seed 未摇中不应掉落魔宠: %d", mn)
	}
	// 重放 → already_received，零二次入账。
	res2 := gameJSON(t, ts.URL, "/quests/result", uid, sk, s0, s1, "bid="+bid+"&time=45.5&mc=0", "_method=GET")
	if res2["status"] != "already_received" {
		t.Fatalf("重复 result: %v", res2)
	}
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&gp); err != nil || gp != gp0+300 {
		t.Fatalf("重复入账: %d", gp)
	}
	var clears int
	if err := st.DB().QueryRow(`SELECT clear_count FROM cn35_quest_clears WHERE user_id=? AND quest_id=1 AND difficulty='normal'`, uid).Scan(&clears); err != nil || clears != 1 {
		t.Fatalf("重复计通关: %d", clears)
	}
	// 再刷同一关：quest1 仍解锁（id<=前沿），clear→2。
	doc2 := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001", "_method=GET")
	if doc2["clear_count"].(float64) != 1 {
		t.Fatalf("再刷时 clear_count 应为 1: %v", doc2["clear_count"])
	}
	// execute exp = 会话口径（难度 profile），非 battle.Exp 基值。
	if doc2["exp"].(float64) != 100 {
		t.Fatalf("execute exp 应为会话经验值: %v", doc2["exp"])
	}
	bid2 := strings.SplitN(doc2["ap_use_url"].(string), "bid=", 2)[1]
	gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+bid2, "_method=GET")
	res3 := gameJSON(t, ts.URL, "/quests/result", uid, sk, s0, s1, "bid="+bid2+"&time=50&mc=0", "_method=GET")
	if res3["status"] != "success" {
		t.Fatalf("再刷结算: %v", res3)
	}
	if err := st.DB().QueryRow(`SELECT clear_count FROM cn35_quest_clears WHERE user_id=? AND quest_id=1 AND difficulty='normal'`, uid).Scan(&clears); err != nil || clears != 2 {
		t.Fatalf("再刷 clear 应为 2: %d", clears)
	}
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&gp); err != nil || gp != gp0+600 {
		t.Fatalf("再刷入账: %d", gp)
	}
}

// TestStorySeedDropAndBackground story-01 接入验收（2026-10-01 Note）：
// quest4 Boss 关 seed_drop=魔宠14（normal chance_bp=800）；注入 seed43
// （首次 Intn(10000)<200<800 必中）→ execute 快照含 {key:seed,id:14} 且
// 注入 14 实际出现的怪位；结算入 cn35_player_monsters；背景随机池内下发。
func TestStorySeedDropAndBackground(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServerSeed(t, dir, 43)
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-b-seeddrop")
	seedBattleUnits(t, st, uid)
	// quest4 是 Boss 关（seed_drop 魔宠14/800bp）；前沿推进到 4 解锁。
	if _, err := st.DB().Exec(`UPDATE cn35_quest_progress
		SET stage_id=1,area_id=1,quest_id=4,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}

	doc := gameJSON(t, ts.URL, "/quests/execute/4.json", uid, sk, s0, s1,
		"mode=quest&id=4&difficulty_id=normal&party_id=001", "_method=GET")
	q := doc["quest"].(map[string]any)
	bgID := int(q["background_id"].(float64))
	if bgID <= 0 {
		t.Fatalf("background_id 缺失: %v", q)
	}
	foundBg := false
	for _, a := range doc["asset_names"].([]any) {
		if a.(string) == fmt.Sprintf("background/background%d_1", bgID) {
			foundBg = true
		}
	}
	if !foundBg {
		t.Fatalf("asset_names 缺下发背景 background%d_1", bgID)
	}

	// seed 奖励必须注入到魔宠 14 实际出现的怪位（Quest4 Boss 末波）。
	foundSeed := false
	for _, wv := range doc["appear_table"].(map[string]any) {
		for _, ev := range wv.(map[string]any) {
			e := ev.(map[string]any)
			for _, rv := range e["rewards"].([]any) {
				r := rv.(map[string]any)
				if r["key"] == "seed" {
					if int(e["id"].(float64)) != 14 || int(r["id"].(float64)) != 14 {
						t.Fatalf("seed 掉落挂在非本怪位: entry.id=%v reward=%v", e["id"], r)
					}
					foundSeed = true
				}
			}
		}
	}
	if !foundSeed {
		t.Fatalf("seed43 应摇中魔宠14掉落: %v", doc["appear_table"])
	}
	bid := strings.SplitN(doc["ap_use_url"].(string), "bid=", 2)[1]
	gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+bid, "_method=GET")
	res := gameJSON(t, ts.URL, "/quests/result", uid, sk, s0, s1,
		"bid="+bid+"&time=99&mc=0", "_method=GET")
	if res["status"] != "success" {
		t.Fatalf("result: %v", res)
	}
	var mn, mid int
	if err := st.DB().QueryRow(`SELECT count(*),coalesce(max(monster_id),0)
		FROM cn35_player_monsters WHERE user_id=?`, uid).Scan(&mn, &mid); err != nil {
		t.Fatal(err)
	}
	if mn != 1 || mid != 14 {
		t.Fatalf("掉落实例应入账魔宠14: count=%d id=%d", mn, mid)
	}
	// user_monsters 即时回推（客户端魔宠面板不重启可见）。
	if _, ok := res["data"].(map[string]any)["user_monsters"]; !ok {
		t.Fatalf("掉落结算应回推 user_monsters: %v", res["data"])
	}
}

// TestBattleLoseContinueWire lose→continue→result 链 + Escape 不可续。
func TestBattleLoseContinueWire(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServer(t, dir)
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-b-lose")
	seedBattleUnits(t, st, uid)
	setCoin(t, st, uid, 2)

	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001", "_method=GET")
	bid := strings.SplitN(doc["ap_use_url"].(string), "bid=", 2)[1]
	gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+bid, "_method=GET")
	// 败北（escaped=true → 可续）。
	lose := gameJSON(t, ts.URL, "/quests/lose", uid, sk, s0, s1,
		"bid="+bid+"&time=88&mc=2&escaped=true&damage%5B3%5D=999", "_method=POST")
	if lose["status"] != "success" {
		t.Fatalf("lose: %v", lose)
	}
	// 首败标志经快照下发。
	pd := gameJSON(t, ts.URL, "/users/preset_data.json", uid, sk, s0, s1, "", "_method=GET")
	ff := pd["data"].(map[string]any)["user"].(map[string]any)["first_flag"].(map[string]any)
	if ff["failure"].(float64) != 1 {
		t.Fatalf("first_flag.failure: %v", ff)
	}
	// continue：扣 1 钻（2→1）。
	cont := gameJSON(t, ts.URL, "/quests/continue", uid, sk, s0, s1, "", "_method=GET")
	if cont["status"] != "success" {
		t.Fatalf("continue: %v", cont)
	}
	coin := cont["data"].(map[string]any)["user_coin"].(map[string]any)["amount"].(float64)
	if coin != 1 {
		t.Fatalf("续战后 coin: %v", coin)
	}
	// 续战后可正常结算。
	res := gameJSON(t, ts.URL, "/quests/result", uid, sk, s0, s1, "bid="+bid+"&time=200&mc=0", "_method=GET")
	if res["status"] != "success" {
		t.Fatalf("续战后 result: %v", res)
	}
	// 再 continue：无 lost 会话 → error。
	cont2 := gameJSON(t, ts.URL, "/quests/continue", uid, sk, s0, s1, "", "_method=GET")
	if cont2["status"] == "success" {
		t.Fatalf("无可续会话应 error: %v", cont2)
	}
	// Escape（无 escaped）→ 不可续。
	doc2 := gameJSON(t, ts.URL, "/quests/execute/2.json", uid, sk, s0, s1,
		"mode=quest&id=2&difficulty_id=normal&party_id=001", "_method=GET")
	bid2 := strings.SplitN(doc2["ap_use_url"].(string), "bid=", 2)[1]
	gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+bid2, "_method=GET")
	gameJSON(t, ts.URL, "/quests/lose", uid, sk, s0, s1, "bid="+bid2+"&time=10&mc=5", "_method=POST")
	cont3 := gameJSON(t, ts.URL, "/quests/continue", uid, sk, s0, s1, "", "_method=GET")
	if cont3["status"] == "success" {
		t.Fatalf("Escape 会话不得可续: %v", cont3)
	}
}

// TestBattleCrossAccountWire B 重放 A 的 bid：全端点 status=error。
func TestBattleCrossAccountWire(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServer(t, dir)
	defer ts.Close()
	defer st.Close()
	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "dev-b-iso-A")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "dev-b-iso-B")
	seedBattleUnits(t, st, uidA)

	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uidA, skA, s0A, s1A,
		"mode=quest&id=1&difficulty_id=normal&party_id=001", "_method=GET")
	bid := strings.SplitN(doc["ap_use_url"].(string), "bid=", 2)[1]
	for _, tc := range []struct{ path, query, form string }{
		{"/quests/use_ap", "bid=" + bid, "_method=GET"},
		{"/quests/result", "bid=" + bid + "&time=1&mc=0", "_method=GET"},
		{"/quests/lose", "bid=" + bid + "&time=1&mc=0&escaped=true", "_method=POST"},
	} {
		r := gameJSON(t, ts.URL, tc.path, uidB, skB, s0B, s1B, tc.query, tc.form)
		if r["status"] == "success" {
			t.Fatalf("B 重放 A 的 bid 不得成功: %s → %v", tc.path, r)
		}
	}
	// A 侧未被污染：正常走完。
	gameJSON(t, ts.URL, "/quests/use_ap", uidA, skA, s0A, s1A, "bid="+bid, "_method=GET")
	res := gameJSON(t, ts.URL, "/quests/result", uidA, skA, s0A, s1A, "bid="+bid+"&time=9&mc=0", "_method=GET")
	if res["status"] != "success" {
		t.Fatalf("A 结算应成功: %v", res)
	}
	// B 的进度/资源仍初始。
	uB := gameJSON(t, ts.URL, "/users/preset_data.json", uidB, skB, s0B, s1B, "", "_method=GET")["data"].(map[string]any)["user"].(map[string]any)
	if uB["quest_id"].(float64) != 1 || uB["ap"].(float64) != 20 {
		t.Fatalf("B 被污染: %v", uB)
	}
}

// TestBattleRestartWire 重启保持：会话跨重启可结算（bid 持久化）。
func TestBattleRestartWire(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServer(t, dir)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-b-restart")
	seedBattleUnits(t, st, uid)
	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001", "_method=GET")
	bid := strings.SplitN(doc["ap_use_url"].(string), "bid=", 2)[1]
	gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+bid, "_method=GET")
	ts.Close()
	st.Close()

	// “重启”：同 state 目录重开（会话/密钥/台账全在 SQLite）。
	ts2, st2 := battleServer(t, dir)
	defer ts2.Close()
	defer st2.Close()
	res := gameJSON(t, ts2.URL, "/quests/result", uid, sk, s0, s1, "bid="+bid+"&time=30&mc=0", "_method=GET")
	if res["status"] != "success" {
		t.Fatalf("重启后结算: %v", res)
	}
	u := res["data"].(map[string]any)["user"].(map[string]any)
	// LOGIN350-02 起升级 AP 叠加增量：重启后结算同样落到 level2 → ap=16。
	if u["quest_id"].(float64) != 2 || u["ap"].(float64) != 16 {
		t.Fatalf("重启后结算状态: %v", u)
	}
	res2 := gameJSON(t, ts2.URL, "/quests/result", uid, sk, s0, s1, "bid="+bid+"&time=30&mc=0", "_method=GET")
	if res2["status"] != "already_received" {
		t.Fatalf("重启后重放: %v", res2)
	}
}
