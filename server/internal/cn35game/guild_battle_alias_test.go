// guild_battle_alias_test.go GC-05 会战**原路径别名**真实 HTTP 回归：
//
//	/battles/attack   = 原服胜利上报（契约 method=POST，精确路径）
//	/battles/bp_use   = 原服 BP 付点
//
// 为什么单独一份：`mlk_guild_contract_test.go` 的通用循环对写端点没有原服 POST
// 体（contract.json `request_body_available=false`），只能打到"无 bid"的错误分支，
// 因此那 16 项 `/battles/attack` 检查是**错误分支的结构校验**，不能当成功路径
// 已验证。本文件用真实 bid 补齐 GC-09 要求的「新补写面即使不在 20 条录包中也
// 须有真实 HTTP 与事务回归」：
//
//	a) 成功上报的结构 + 入账；
//	b) 同一 bid 重复上报幂等（不重复加 GP、不重复发奖）；
//	c) 反复付点不重复扣 BP。
//
// 断言对象是**服务端语义**（幂等/入账/结构），不复刻原服动态值。
package cn35game_test

import (
	"database/sql"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// gwAliasEnv 会战别名回归的合成环境：真实 SQLite + 加密 HTTP + 固定时钟。
type gwAliasEnv struct {
	ts              *httptest.Server
	st              *cn35store.Store
	uid, sk, s0, s1 string
	target          string // 组内敌会 id
	now             time.Time
	t               *testing.T
}

// newGWAliasEnv 建库/建会/设防卫/起服务；时钟固定在原服午场（12:45）以命中
// `fields[5].actable`，不开系统时钟。
func newGWAliasEnv(t *testing.T) *gwAliasEnv {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := cn35store.Open(filepath.Join(t.TempDir(), "gw-alias.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err = st.ConfigureGuildSolo(cn35store.DefaultGuildSoloSettings); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 45, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	// store 侧时钟与 wire 侧必须一致：GWAttackPay / settleDueSessionsTx 走
	// store 的 clockNowUTC()，只固定 Deps.GuildClock 会让固定 12:45 造出的
	// 午场在真实时间过 13:00 后判过期（GC-10 实机轮发现的墙钟耦合）。
	st.SetClock(func() time.Time { return now })
	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		Accounts: st, Sessions: st, Inventory: st, Decks: st, Quests: st,
		Battles: st, Guild: st, Catalog: cat,
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
		ServerHost: "127.0.0.1:18092",
		GuildClock: func() time.Time { return now },
	})
	// 别名必须同时可按 POST 精确命中（契约 method=POST）。
	for _, p := range []string{"/battles/attack", "/battles/bp_use"} {
		if _, ok := reg.Lookup("POST", p); !ok {
			t.Fatalf("原路径未注册 POST: %s", p)
		}
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	env := &gwAliasEnv{ts: ts, st: st, now: now, t: t}
	env.uid, env.sk, env.s0, env.s1 = establishSession(t, ts.URL, "gw-alias-synthetic")
	seedBattleUnits(t, st, env.uid)
	if err = st.WithTx(func(tx *sql.Tx) error {
		return cn35store.InsertMonsterTx(tx, env.uid,
			cn35game.MonsterRecord{UUID: "alias-monster", MonsterID: 159, Level: 1})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`UPDATE cn35_player_profiles SET bp=3000 WHERE user_id=?`, env.uid); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`INSERT INTO cn35_player_balances(user_id,currency,amount,updated_at)
		VALUES (?,'guild_asset',0,'2026-10-28') ON CONFLICT(user_id,currency) DO UPDATE SET amount=0`,
		env.uid); err != nil {
		t.Fatal(err)
	}
	views, err := st.SearchGuilds(env.uid, "", "", "")
	if err != nil || len(views) == 0 {
		t.Fatalf("候选公会准备失败: %v", err)
	}
	if _, err = st.JoinGuild(env.uid, views[0].Guild.ID, "alias-join"); err != nil {
		t.Fatal(err)
	}
	if err = st.SetDefense(env.uid, 0, "alias-monster", "alias-defense"); err != nil {
		t.Fatal(err)
	}
	gw, err := st.GWState(env.uid, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range gw.Participants {
		if !p.Joined {
			env.target = p.GuildID
			break
		}
	}
	if env.target == "" {
		t.Fatal("测试准备缺独立敌会")
	}
	return env
}

// callURL 按 execute 下发的原始 URL 发起加密请求（客户端就是这样整串回发）。
func (e *gwAliasEnv) callURL(raw any) map[string]any {
	e.t.Helper()
	s, ok := raw.(string)
	if !ok || s == "" {
		e.t.Fatalf("下发 URL 缺失: %#v", raw)
	}
	u, err := url.Parse(s)
	if err != nil || u.Host != "" || u.Scheme != "" {
		e.t.Fatalf("下发 URL 非法: %q", s)
	}
	return gameJSON(e.t, e.ts.URL, u.Path, e.uid, e.sk, e.s0, e.s1, u.RawQuery, "_method=GET")
}

// openBattle 走 execute（真实开战，开战时即扣 BP）并返回文档。
func (e *gwAliasEnv) openBattle(opKey string) map[string]any {
	e.t.Helper()
	doc := gameJSON(e.t, e.ts.URL, "/battles/guild_quest_execute.json",
		e.uid, e.sk, e.s0, e.s1,
		"mode=battle&guild_id="+url.QueryEscape(e.target)+
			"&difficulty_id=normal&party_id=001&op_key="+opKey, "_method=GET")
	if _, ok := doc["quest"].(map[string]any); !ok {
		e.t.Fatalf("execute 未回 quest: %v", doc["modal"])
	}
	return doc
}

// snapshot 会战入账三元组：BP / 公会资材 / 本场个人 GP 台账。
func (e *gwAliasEnv) snapshot() [3]int64 {
	e.t.Helper()
	var s [3]int64
	for i, q := range []string{
		`SELECT bp FROM cn35_player_profiles WHERE user_id=?`,
		`SELECT COALESCE(amount,0) FROM cn35_player_balances WHERE user_id=? AND currency='guild_asset'`,
		`SELECT COALESCE(SUM(gp_earned),0) FROM cn35_gw_member_scores WHERE user_id=?`,
	} {
		if err := e.st.DB().QueryRow(q, e.uid).Scan(&s[i]); err != nil {
			e.t.Fatal(err)
		}
	}
	return s
}

// battleIDOf 从下发的 URL / 响应里取 bid 参数。
func battleIDOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	bid := u.Query().Get("bid")
	if bid == "" {
		t.Fatalf("URL 未带 bid: %s", rawURL)
	}
	return bid
}

// TestGWQuestExecuteOriginalShape execute 回包的原服硬值/结构（GC-05）。
// 只断言契约 equals 项与结构，不比对玩家动态值。
func TestGWQuestExecuteOriginalShape(t *testing.T) {
	env := newGWAliasEnv(t)
	doc := env.openBattle("alias-shape")
	quest := doc["quest"].(map[string]any)

	// 契约 equals 硬值。
	for k, want := range map[string]float64{
		"seed_atack_rate": 0.2, "help_base_sec": 25, "help_sec_level_rate": 0.1,
		"slash_rate": 1, "rush_rate": 1, "hit_rate": 1.3, "arrow_rate": 1.3,
		"magic_rate": 1, "gun_rate": 1, "heal_rate": 1,
	} {
		got, ok := quest[k].(float64)
		if !ok || got != want {
			t.Fatalf("quest.%s = %#v, want %v", k, quest[k], want)
		}
	}
	for k, want := range map[string]float64{
		"guild_asset_help_level_base": 3, "battle_rank_help_level_rate": 2,
		"battle_help_initial_level": 6,
	} {
		got, ok := doc[k].(float64)
		if !ok || got != want {
			t.Fatalf("%s = %#v, want %v", k, doc[k], want)
		}
	}
	// quest.id = 目标公会 id（string，原服 semantic），difficulty_name 按时段。
	if id, ok := quest["id"].(string); !ok || id != env.target {
		t.Fatalf("quest.id = %#v, want 目标会 %s", quest["id"], env.target)
	}
	if name, _ := quest["difficulty_name"].(string); name != "公会战(昼)" {
		t.Fatalf("difficulty_name = %q, want 公会战(昼)", name)
	}
	// weapon_rate_hash 四档 × 7 键，与平铺同值。
	hash, ok := quest["weapon_rate_hash"].(map[string]any)
	if !ok || len(hash) != 4 {
		t.Fatalf("weapon_rate_hash = %#v", quest["weapon_rate_hash"])
	}
	for _, d := range []string{"normal", "hard", "very_hard", "impossible"} {
		row, ok := hash[d].(map[string]any)
		if !ok {
			t.Fatalf("weapon_rate_hash.%s 缺", d)
		}
		for _, w := range []string{"slash", "rush", "hit", "arrow", "magic", "gun", "heal"} {
			if row[w+"_rate"] != quest[w+"_rate"] {
				t.Fatalf("weapon_rate_hash.%s.%s_rate 与平铺不一致", d, w)
			}
		}
	}
	// 原服精确路径 + 已带 query（客户端会在其后直接拼 &time=）。
	for k, wantPath := range map[string]string{
		"ap_use_url": "/battles/bp_use", "result_url": "/battles/attack",
	} {
		raw, _ := doc[k].(string)
		u, err := url.Parse(raw)
		if err != nil || u.Path != wantPath {
			t.Fatalf("%s = %q, want path %s", k, raw, wantPath)
		}
		if u.RawQuery == "" {
			t.Fatalf("%s 未带 query，客户端拼 &time= 会失效", k)
		}
	}
	// rush_* 显式 null（契约 want:"null"），seed_attribute_rates/flag_image_name 结构。
	for _, k := range []string{"rush_started", "rush_ended"} {
		if v, has := doc[k]; !has || v != nil {
			t.Fatalf("%s 必须显式 null: %#v", k, v)
		}
	}
	if doc["flag_image_name"] != "common/flag" {
		t.Fatalf("flag_image_name = %#v", doc["flag_image_name"])
	}
	rates, ok := doc["seed_attribute_rates"].(map[string]any)
	if !ok || rates["seed_at_rate"] != float64(1) || rates["cost_rate"] != float64(1) ||
		rates["call_interval_rate"] != float64(1) {
		t.Fatalf("seed_attribute_rates = %#v", doc["seed_attribute_rates"])
	}
	// data.guild 与 /guilds/ 同形（复用 guildDoc）+ battle_id/bp_storage_button。
	data, _ := doc["data"].(map[string]any)
	guild, ok := data["guild"].(map[string]any)
	if !ok {
		t.Fatalf("data.guild 缺失: %#v", data)
	}
	bid := battleIDOf(t, doc["result_url"].(string))
	if guild["battle_id"] != bid {
		t.Fatalf("data.guild.battle_id = %#v, want 本次 bid %s", guild["battle_id"], bid)
	}
	if guild["bp_storage_button"] != float64(1) {
		t.Fatalf("bp_storage_button = %#v", guild["bp_storage_button"])
	}
	// 帮战单位等级基准（客户端 initial_level）由此推导，必须自洽。
	if guild["id"] == "" || guild["name"] == "" {
		t.Fatalf("data.guild 缺 id/name: %#v", guild)
	}
}

// TestGWAttackAliasSuccessAndIdempotent /battles/attack 真实成功上报 + 重复上报幂等。
func TestGWAttackAliasSuccessAndIdempotent(t *testing.T) {
	env := newGWAliasEnv(t)
	doc := env.openBattle("alias-attack")
	if d := env.callURL(doc["ap_use_url"]); d["status"] != "success" {
		t.Fatalf("原服付点路径失败: %v", d["status"])
	}
	paid := env.snapshot()

	// 1) 成功上报：结构与入账。
	res := env.callURL(doc["result_url"])
	if res["status"] != "success" {
		t.Fatalf("/battles/attack status = %v", res["status"])
	}
	if _, ok := res["total_gp"].(float64); !ok {
		t.Fatalf("total_gp 必须是 number: %#v", res["total_gp"])
	}
	if v, has := res["party_event"]; !has || v != nil {
		t.Fatalf("party_event 必须是 JSON null: %#v", v)
	}
	data, ok := res["data"].(map[string]any)
	if !ok {
		t.Fatalf("data 缺失: %#v", res["data"])
	}
	messages, ok := data["messages"].(map[string]any)
	if !ok {
		t.Fatalf("data.messages 必须是 object: %#v", data["messages"])
	}
	for _, k := range []string{"guild_comment", "guild_help", "battle_activity",
		"community_comment", "community_help", "community_raid_call", "user_comment"} {
		if _, has := messages[k]; !has {
			t.Fatalf("data.messages 缺键 %s: %#v", k, messages)
		}
	}
	if _, ok := data["user"].(map[string]any); !ok {
		t.Fatalf("data.user 必须是 object")
	}
	if _, ok := data["battle_condition"].(map[string]any); !ok {
		t.Fatalf("data.battle_condition 必须是 object")
	}
	won := env.snapshot()
	if won[1] <= paid[1] {
		t.Fatalf("胜利未入账公会资材: %v -> %v", paid[1], won[1])
	}
	if won[2] <= paid[2] {
		t.Fatalf("胜利未入账个人 GP 台账: %v -> %v", paid[2], won[2])
	}
	// 2) 重复上报同一 bid：already_received 且不重复入账。
	if again := env.callURL(doc["result_url"]); again["status"] != "already_received" {
		t.Fatalf("重复上报 status = %v, want already_received", again["status"])
	}
	if env.snapshot() != won {
		t.Fatalf("重复上报重复入账: %v -> %v", won, env.snapshot())
	}
	// 3) 台账不重复：同一战斗只有一条 attack 动作记录。
	var n int
	if err := env.st.DB().QueryRow(`SELECT count(*) FROM cn35_gw_actions
		WHERE user_id=? AND op_key=?`, env.uid, "atk:"+battleIDOf(t, doc["result_url"].(string))).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("同一战斗动作台账 = %d 条, want 1", n)
	}
}

// TestGWBPUseAliasSuccessAndIdempotent /battles/bp_use 真实付点 + 重复付点不重复扣。
func TestGWBPUseAliasSuccessAndIdempotent(t *testing.T) {
	env := newGWAliasEnv(t)
	doc := env.openBattle("alias-bp")
	// BP 在开战（GWAttackOpen）时已扣；付点只做 prepared→bp_paid。
	opened := env.snapshot()
	if d := env.callURL(doc["ap_use_url"]); d["status"] != "success" {
		t.Fatalf("首次付点 status = %v", d["status"])
	}
	var status string
	if err := env.st.DB().QueryRow(`SELECT status FROM cn35_gw_attacks WHERE battle_id=?`,
		battleIDOf(t, doc["ap_use_url"].(string))).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "bp_paid" {
		t.Fatalf("付点后攻击状态 = %q, want bp_paid", status)
	}
	after := env.snapshot()
	for i := 0; i < 2; i++ {
		if d := env.callURL(doc["ap_use_url"]); d["status"] != "success" {
			t.Fatalf("重复付点 status = %v", d["status"])
		}
	}
	if env.snapshot() != after || after[0] != opened[0] {
		t.Fatalf("付点改变了 BP/资材/GP: 开战 %v 首次 %v 重复后 %v", opened, after, env.snapshot())
	}
}

// TestGWHelpPrayDataQuery 应援/祈祷回包的 data_query 增量（GC-05）。
//
// 原服 help 每次 -50 BP、pray 每次 -15 BP（fields[].help_bp/pray_bp，样本 BP 序列
// 400→350→300 / 30→15→0），data_query.user.bp 必须是**动作完成后**的 BP
// ——客户端靠它局部刷新顶栏（DataCache.AddWithQuery）。
func TestGWHelpPrayDataQuery(t *testing.T) {
	env := newGWAliasEnv(t)
	before := env.snapshot()[0]
	doc := gameJSON(t, env.ts.URL, "/battles/help", env.uid, env.sk, env.s0, env.s1,
		"op_key=alias-help", "_method=GET")
	q, ok := doc["data_query"].(map[string]any)
	if !ok {
		t.Fatalf("/battles/help 缺 data_query: %#v", doc["data_query"])
	}
	user, ok := q["user"].(map[string]any)
	if !ok {
		t.Fatalf("data_query.user 必须是 object: %#v", q["user"])
	}
	afterHelp := env.snapshot()[0]
	if got, _ := user["bp"].(float64); int64(got) != afterHelp {
		t.Fatalf("data_query.user.bp = %#v, want 动作后 BP %d", user["bp"], afterHelp)
	}
	if afterHelp != before-50 {
		t.Fatalf("应援后 BP %d, want %d-50", afterHelp, before)
	}
	if msg, _ := doc["animation_message"].(string); msg == "" {
		t.Fatal("应援缺 animation_message")
	}
	doc = gameJSON(t, env.ts.URL, "/battles/pray", env.uid, env.sk, env.s0, env.s1,
		"op_key=alias-pray", "_method=GET")
	user = doc["data_query"].(map[string]any)["user"].(map[string]any)
	afterPray := env.snapshot()[0]
	if got, _ := user["bp"].(float64); int64(got) != afterPray {
		t.Fatalf("data_query.user.bp = %#v, want 动作后 BP %d", user["bp"], afterPray)
	}
	if afterPray != afterHelp-15 {
		t.Fatalf("祈祷后 BP %d, want %d-15", afterPray, afterHelp)
	}
}
