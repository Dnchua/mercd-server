// pvp_wire_test.go M10 癒术士大赛与公会训练协议冒泡：真实 cn35store(SQLite) ×
// cardcatalog × cn35wire 加密通道。覆盖入口/consts、读面、编成、开战门禁、扣次数幂等、
// 胜负结算与对手扣分、魔宠增幅、周结算领取、入场券、攻守编队职业限制、复仇与重启持久化。
package cn35game_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func m10Server(t *testing.T, dbPath string, clock *m8Clock) (*httptest.Server, *cn35store.Store, *cardcatalog.Catalog) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(dbPath)
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	st.SetClock(clock.Now)
	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		Accounts: st, Sessions: st, Inventory: st, Decks: st, Quests: st, Battles: st,
		Items: st, ItemUse: st, Presents: st, PresentReceive: st, PvP: st,
		Catalog: cat, ServerHost: "127.0.0.1:18091",
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
		BattleRand: mathrand.New(mathrand.NewSource(10)),
		GuildClock: clock.Now,
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(srv.Handler()), st, cat
}

func m10Login(t *testing.T, ts *httptest.Server, st *cn35store.Store, device string, level int) m8Session {
	t.Helper()
	s := m8Login(t, ts, st, device)
	if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET level=? WHERE user_id=?`, level, s.uid); err != nil {
		t.Fatal(err)
	}
	return s
}

func m10Monster(t *testing.T, st *cn35store.Store, uid, uuid string, monsterID int) {
	t.Helper()
	scale := "1.500"
	if err := st.WithTx(func(tx *sql.Tx) error {
		return cn35store.InsertMonsterTx(tx, uid, cn35game.MonsterRecord{UUID: uuid, MonsterID: monsterID, Level: 1, Scale: &scale, RealScale: &scale})
	}); err != nil {
		t.Fatal(err)
	}
}

// m10Units 插入 10 名非治疗同伴（Lv1），返回 id。
func m10Units(t *testing.T, st *cn35store.Store, cat *cardcatalog.Catalog, uid string, skip map[int]bool) []int {
	t.Helper()
	out := []int{}
	for _, id := range cat.UnitIDs() {
		u, err := cat.Unit(id)
		if err != nil || u.Rare < 2 || u.Weapon == 7 || skip[id] {
			continue
		}
		if err := st.WithTx(func(tx *sql.Tx) error {
			return cn35store.InsertUnitTx(tx, uid, cn35game.UnitRecord{UnitID: id, Level: 1})
		}); err != nil {
			continue
		}
		out = append(out, id)
		if len(out) == 10 {
			break
		}
	}
	if len(out) != 10 {
		t.Fatalf("可用同伴不足 10: %d", len(out))
	}
	return out
}

func m10Int(t *testing.T, st *cn35store.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func m10Modal(doc map[string]any) bool { _, ok := doc["modal"]; return ok }

func bidOf(t *testing.T, u any) string {
	t.Helper()
	s, ok := u.(string)
	if !ok || !strings.Contains(s, "bid=") {
		t.Fatalf("缺会话 URL: %v", u)
	}
	return strings.SplitN(s, "bid=", 2)[1]
}

func TestM10EntryAndConsts(t *testing.T) {
	clock := &m8Clock{now: time.Now()}
	ts, st, _ := m10Server(t, filepath.Join(t.TempDir(), "m10.sqlite3"), clock)
	defer ts.Close()
	defer st.Close()
	a := m10Login(t, ts, st, "dev-m10-entry", 20)
	portal := m8Data(t, a.get("/users/function_portal", ""))
	names := map[string]bool{}
	for _, r := range portal["function_portal_list"].([]any) {
		names[r.(map[string]any)["name"].(string)] = true
	}
	if !names["ladder"] || !names["rob_battle"] {
		t.Fatalf("日常入口缺 M10 横幅: %v", names)
	}
	if fp := portal["function_portal"].(map[string]any); fp["chara_buff_button_disable"] != true {
		t.Fatalf("主角光环必须保持置灰（不复原）: %v", fp)
	}
	consts := m8Data(t, a.get("/users/preset_data.json", ""))["consts"].(map[string]any)
	ld, ok := consts["ladder_difficulty"].(map[string]any)
	if !ok || ld["very_hard"] != "积分x1.25" || ld["impossible_lock"] != true {
		t.Fatalf("consts.ladder_difficulty: %v", consts["ladder_difficulty"])
	}
	if cl, ok := consts["rob_battle_career_limit"].(map[string]any); !ok || cl["7"] != float64(2) {
		t.Fatalf("consts.rob_battle_career_limit: %v", consts["rob_battle_career_limit"])
	}
	for _, p := range []string{"/main_chara_buff/infos", "/main_chara_buff/upgrade_infos"} {
		if d := a.get(p, ""); m10Modal(d) {
			t.Fatalf("%s 应保持空态而非报错: %v", p, d)
		}
	}
}

func TestM10LadderFlow(t *testing.T) {
	clock := &m8Clock{now: time.Now()}
	db := filepath.Join(t.TempDir(), "m10.sqlite3")
	ts, st, _ := m10Server(t, db, clock)
	a := m10Login(t, ts, st, "dev-m10-la", 30)
	b := m10Login(t, ts, st, "dev-m10-lb", 30)
	low := m10Login(t, ts, st, "dev-m10-low", 5)

	// 主页：未设防御 → ladder_deck_empty=true，无待领奖励 → has_ladder_rewards="false"。
	main := m8Data(t, a.get("/ladders/show_ladder_info", ""))
	if main["has_ladder_rewards"] != "false" {
		t.Fatalf("has_ladder_rewards 应为字符串 false: %#v", main["has_ladder_rewards"])
	}
	if main["ladder_main_page_disable_key"].(map[string]any)["ladder_deck_empty"] != true {
		t.Fatalf("未设防御时应禁用开始挑战: %v", main["ladder_main_page_disable_key"])
	}
	page := main["ladder_main_page"].(map[string]any)
	if n := len(page["btn_list"].([]any)); n != 6 {
		t.Fatalf("btn_list 必须 6 项: %d", n)
	}
	// 未设防御直接开战被拒。
	if d := a.get("/ladders/ladder_quest_execute.json", "mode=ladder&difficulty_id=normal&user_id="+b.uid+"&party_id=001"); !m10Modal(d) {
		t.Fatalf("未设防御开战应弹窗: %v", d)
	}

	m10Monster(t, st, a.uid, "a-m1", 26)
	m10Monster(t, st, b.uid, "b-m1", 33)
	m10Monster(t, st, b.uid, "b-m2", 81)
	deck := m8Data(t, a.get("/ladders/set_deck", "party_index=0&uuid=a-m1"))
	if d := deck["ladder_deck"].(map[string]any)["0"].(map[string]any); d["uuid"] != "a-m1" {
		t.Fatalf("防御第 1 格: %v", d)
	}
	if rates := deck["ladder_deck_data"].(map[string]any)["powerup_rate"].([]any); len(rates) != 5 || rates[4] != 1.8 {
		t.Fatalf("powerup_rate: %v", rates)
	}
	b.get("/ladders/set_deck", "party_index=0&uuid=b-m1")
	b.get("/ladders/set_deck", "party_index=3&uuid=b-m2")
	// 同一魔宠换格：自动离开原格。
	b.get("/ladders/set_deck", "party_index=1&uuid=b-m2")
	if n := m10Int(t, st, `SELECT count(*) FROM cn35_ladder_decks WHERE user_id=?`, b.uid); n != 2 {
		t.Fatalf("B 防御格数: %d", n)
	}
	if d := a.get("/ladders/set_deck", "party_index=2&uuid=not-mine"); !m10Modal(d) {
		t.Fatalf("非本人魔宠应拒绝: %v", d)
	}

	cands := m8Data(t, a.get("/ladders/get_candidates", ""))["candidates"].([]any)
	if len(cands) != 20 {
		t.Fatalf("候选数: %d", len(cands))
	}
	foundB := false
	for _, c := range cands {
		add := c.(map[string]any)["message"].(map[string]any)["addition"].(string)
		if add == "user_id="+b.uid {
			foundB = true
		}
	}
	if !foundB {
		t.Fatal("候选应含真实账号 B")
	}

	// 前三名不足 3 人时以候选同源确定性 NPC 补足：每行恒 3 条、icon 非空、
	// 名出自 get_candidates 同名池（客户端缺行会露出 prefab 默认占位）。
	// 同名池取法：另起一服单账号（无其他防御者 → 20 名候选全为 NPC，
	// 覆盖完整 NPC 名表；本服候选会被真实玩家挤掉高序号 NPC 名）。
	npcClock := &m8Clock{now: time.Now()}
	npcTS, npcST, _ := m10Server(t, filepath.Join(t.TempDir(), "m10-npcnames.sqlite3"), npcClock)
	defer npcTS.Close()
	defer npcST.Close()
	u := m10Login(t, npcTS, npcST, "dev-m10-npc", 30)
	candNames := map[string]bool{}
	for _, c := range m8Data(t, u.get("/ladders/get_candidates", ""))["candidates"].([]any) {
		candNames[c.(map[string]any)["name"].(string)] = true
	}
	iconRe := regexp.MustCompile(`^unit/unit_square_\d+$`)
	infoRows := m8Data(t, a.get("/ladders/show_ladder_info", ""))["ladder_main_page"].(map[string]any)["ladder_info"].([]any)
	for i, row := range infoRows {
		pl := row.(map[string]any)["player_list"].([]any)
		if len(pl) != 3 {
			t.Fatalf("ladder_info[%d].player_list 必须恒 3 条: %d", i, len(pl))
		}
		for j, e := range pl {
			em := e.(map[string]any)
			if icon, _ := em["icon"].(string); !iconRe.MatchString(icon) {
				t.Fatalf("ladder_info[%d].player_list[%d].icon 非法: %q", i, j, icon)
			}
			if name, _ := em["name"].(string); !candNames[name] {
				t.Fatalf("ladder_info[%d].player_list[%d].name 非候选同名池: %q", i, j, name)
			}
		}
	}

	// 低等级门禁。
	m10Monster(t, st, low.uid, "low-m1", 26)
	low.get("/ladders/set_deck", "party_index=0&uuid=low-m1")
	if d := low.get("/ladders/ladder_quest_execute.json", "mode=ladder&difficulty_id=normal&user_id="+b.uid+"&party_id=001"); !m10Modal(d) {
		t.Fatalf("15 级以下应拒绝: %v", d)
	}

	// 开战 vs B（极难）。
	ex := a.get("/ladders/ladder_quest_execute.json", "mode=ladder&difficulty_id=very_hard&user_id="+b.uid+"&party_id=001")
	bosses := ex["boss_appear_table"].(map[string]any)["1"].([]any)
	if len(bosses) != 2 {
		t.Fatalf("boss 数应等于 B 防御格: %d", len(bosses))
	}
	b0 := bosses[0].(map[string]any)
	if b0["interval_sec"] != float64(40) || b0["powerup_rate"].(float64) != 1.5*15*1 {
		t.Fatalf("极难 boss 参数: %v", b0)
	}
	if b1 := bosses[1].(map[string]any); b1["powerup_rate"].(float64) != 1.5*15*1.2 {
		t.Fatalf("第 2 格倍率: %v", b1)
	}
	if n := len(ex["appear_table"].(map[string]any)["1"].(map[string]any)); n != 150 {
		t.Fatalf("杂兵数: %d", n)
	}
	if ex["is_ladder"] != true || ex["quest"].(map[string]any)["mode"] != "ladder" {
		t.Fatalf("execute 形状: %v", ex["quest"])
	}
	m10NoDupAssets(t, ex)
	for _, w := range ex["appear_table"].(map[string]any)["1"].(map[string]any) {
		for _, r := range w.(map[string]any)["rewards"].([]any) {
			if rm := r.(map[string]any); rm["type_id"] == float64(10005) {
				t.Fatal("掉落不得包含潜能叶子")
			}
		}
	}
	bid := bidOf(t, ex["ap_use_url"])
	for i := 0; i < 2; i++ {
		if d := a.get("/ladders/lc_used", "bid="+bid); d["status"] != "success" {
			t.Fatalf("lc_used: %v", d)
		}
	}
	if n := m10Int(t, st, `SELECT day_used FROM cn35_ladder_profiles WHERE user_id=?`, a.uid); n != 1 {
		t.Fatalf("lc_used 重放只扣一次: %d", n)
	}
	before := m10Int(t, st, `SELECT potion_1+potion_2+potion_3 FROM cn35_ladder_profiles WHERE user_id=?`, a.uid)
	res := a.get("/ladders/attack", "bid="+bid+"&time=80")
	if res["status"] != "success" || res["ladder_score"].(float64) <= 0 {
		t.Fatalf("attack: %v", res)
	}
	gain := int(res["ladder_score"].(float64))
	if want := cn35game.LadderScoreGain(0, 0, "very_hard"); gain != want {
		t.Fatalf("积分 %d want %d", gain, want)
	}
	if again := a.get("/ladders/attack", "bid="+bid+"&time=80"); again["status"] != "already_received" {
		t.Fatalf("重放应 already_received: %v", again)
	}
	if n := m10Int(t, st, `SELECT score FROM cn35_ladder_profiles WHERE user_id=?`, a.uid); n != gain {
		t.Fatalf("积分只记一次: %d", n)
	}
	after := m10Int(t, st, `SELECT potion_1+potion_2+potion_3 FROM cn35_ladder_profiles WHERE user_id=?`, a.uid)
	if after-before < cn35game.LadderRules.PotionMin || after-before > cn35game.LadderRules.PotionMax {
		t.Fatalf("药水增量 %d", after-before)
	}
	if n := m10Int(t, st, `SELECT def_total FROM cn35_ladder_profiles WHERE user_id=?`, b.uid); n != 1 {
		t.Fatalf("B 防守记录: %d", n)
	}
	if lc := m8Data(t, a.get("/users/preset_data.json", ""))["user"].(map[string]any)["lc"]; lc != float64(2) {
		t.Fatalf("user.lc: %v", lc)
	}

	// NPC 两场后次数用尽。
	npc := cn35game.NPCOpponentID("ladder", cn35game.LadderPeriodAt(clock.Now()).SeasonKey, 3)
	for i := 0; i < 2; i++ {
		e := a.get("/ladders/ladder_quest_execute.json", "mode=ladder&difficulty_id=normal&user_id="+npc+"&party_id=001")
		if m10Modal(e) {
			t.Fatalf("NPC 开战: %v", e)
		}
		nb := bidOf(t, e["ap_use_url"])
		a.get("/ladders/lc_used", "bid="+nb)
		if d := a.get("/ladders/lose", "bid="+nb+"&time=300"); d["status"] != "success" {
			t.Fatalf("lose: %v", d)
		}
	}
	if d := a.get("/ladders/ladder_quest_execute.json", "mode=ladder&difficulty_id=normal&user_id="+npc+"&party_id=001"); !m10Modal(d) {
		t.Fatalf("次数用尽应拒绝: %v", d)
	}
	if n := m10Int(t, st, `SELECT day_used FROM cn35_ladder_profiles WHERE user_id=?`, a.uid); n != 3 {
		t.Fatalf("day_used: %d", n)
	}

	// 魔宠增幅：价格 50，魔阻 +25，重置返还。
	st.DB().Exec(`UPDATE cn35_ladder_profiles SET potion_1=300 WHERE user_id=?`, a.uid)
	g := m8Data(t, a.get("/ladders/monster_grow_upgrade", "grow_type=1&attr_id=1&count=2"))["monster_grow"].(map[string]any)
	hp := g["tab_data_list"].(map[string]any)["hp"].(map[string]any)
	row1 := hp["list"].([]any)[0].(map[string]any)
	row2 := hp["list"].([]any)[1].(map[string]any)
	if hp["item_count"] != float64(200) || row1["rate"] != float64(40) || row2["price"] != float64(75) {
		t.Fatalf("增幅: count=%v rate=%v price2=%v", hp["item_count"], row1["rate"], row2["price"])
	}
	if d := a.get("/ladders/monster_grow_upgrade", "grow_type=1&attr_id=2&count=3"); !m10Modal(d) {
		t.Fatalf("药水不足应拒绝: %v", d)
	}
	a.get("/ladders/monster_grow_upgrade_reset", "tab_id=1")
	if n := m10Int(t, st, `SELECT potion_1 FROM cn35_ladder_profiles WHERE user_id=?`, a.uid); n != 300 {
		t.Fatalf("重置返还: %d", n)
	}

	// 周结算：跨过周二 21:00 后可领取，重复领取被拒。
	rank := m8Data(t, a.get("/ladders/show_rank", ""))["ladder_ranking_list"].(map[string]any)
	if rank["rank_list_data"].(map[string]any)["ladder_rank"].(map[string]any)["can_get_reward"] != false {
		t.Fatal("本周不应有可领奖励")
	}
	clock.Set(cn35game.LadderPeriodAt(clock.Now()).WeekEnd.Add(time.Hour))
	medal0 := m10Int(t, st, `SELECT COALESCE((SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='medal'),0)`, a.uid)
	if got := m8Data(t, a.get("/ladders/show_ladder_info", ""))["has_ladder_rewards"]; got != "true" {
		t.Fatalf("跨周应有待领奖励（字符串 true）: %#v", got)
	}
	claim := a.get("/ladders/fetch_rewards", "")
	if !m10Modal(claim) {
		t.Fatalf("领取应提示: %v", claim)
	}
	if got := m8Data(t, claim)["has_ladder_rewards"]; got != "false" {
		t.Fatalf("领取后 has_ladder_rewards 应为字符串 false: %#v", got)
	}
	tier := cn35game.LadderTierOf(gain)
	medal1 := m10Int(t, st, `SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='medal'`, a.uid)
	if medal1-medal0 != cn35game.LadderRules.TierReward[tier].Medal {
		t.Fatalf("徽章 %d→%d", medal0, medal1)
	}
	if d := a.get("/ladders/fetch_rewards", ""); m10Data(d) != nil && !m10Modal(d) {
		t.Fatalf("重复领取应拒绝: %v", d)
	}
	if n := m10Int(t, st, `SELECT count(*) FROM cn35_ladder_settlements WHERE user_id=? AND claimed=1`, a.uid); n != 1 {
		t.Fatalf("台账: %d", n)
	}

	// 重启持久化。
	ts.Close()
	st.Close()
	ts2, st2, _ := m10Server(t, db, clock)
	defer ts2.Close()
	defer st2.Close()
	if n := m10Int(t, st2, `SELECT score FROM cn35_ladder_profiles WHERE user_id=?`, a.uid); n != gain {
		t.Fatalf("重启后积分: %d", n)
	}
	if n := m10Int(t, st2, `SELECT count(*) FROM cn35_ladder_decks WHERE user_id=?`, b.uid); n != 2 {
		t.Fatalf("重启后防御格: %d", n)
	}
}

func m10Data(doc map[string]any) map[string]any {
	d, _ := doc["data"].(map[string]any)
	return d
}

func TestM10RobFlow(t *testing.T) {
	clock := &m8Clock{now: time.Now()}
	ts, st, cat := m10Server(t, filepath.Join(t.TempDir(), "m10.sqlite3"), clock)
	defer ts.Close()
	defer st.Close()
	a := m10Login(t, ts, st, "dev-m10-ra", 30)
	b := m10Login(t, ts, st, "dev-m10-rb", 30)
	aUnits := m10Units(t, st, cat, a.uid, map[int]bool{336: true, 348: true})
	bUnits := m10Units(t, st, cat, b.uid, map[int]bool{336: true, 348: true})
	fill := func(s m8Session, pt string, ids []int) {
		for i, id := range ids {
			d := s.get("/user_rob_decks/on", fmt.Sprintf("party_type=%s&party_id=%d&party_index=%d&unit_id=%d", pt, i/5+1, i%5, id))
			if m10Modal(d) {
				t.Fatalf("编队 %s: %v", pt, d)
			}
		}
	}
	fill(a, "atk", aUnits)
	fill(b, "def", bUnits)
	deck := m8Data(t, b.get("/user_rob_decks", "party_type=def"))["user_rob_def_decks"].(map[string]any)
	if p := deck["parties"].(map[string]any)["2"].([]any); len(p) != 5 || p[4] != fmt.Sprint(bUnits[9]) {
		t.Fatalf("防守第 2 队: %v", p)
	}

	// 治疗职业上限 2（服务端复核）。
	heals := []int{}
	for _, id := range cat.UnitIDs() {
		if u, _ := cat.Unit(id); u != nil && u.Weapon == 7 && u.Rare >= 2 {
			heals = append(heals, id)
			if len(heals) == 3 {
				break
			}
		}
	}
	if len(heals) == 3 {
		for i, id := range heals {
			st.WithTx(func(tx *sql.Tx) error {
				return cn35store.InsertUnitTx(tx, a.uid, cn35game.UnitRecord{UnitID: id, Level: 1})
			})
			d := a.get("/user_rob_decks/on", fmt.Sprintf("party_type=def&party_id=1&party_index=%d&unit_id=%d", i, id))
			if (i < 2) == m10Modal(d) {
				t.Fatalf("治疗第 %d 名: %v", i+1, d)
			}
		}
	}

	info := m8Data(t, a.get("/rob_battles/show_rob_battles_info", ""))
	enters := info["rob_battle_enter"].(map[string]any)["enters"].([]any)
	if e0 := enters[0].(map[string]any); e0["can_buy"] != true || enters[1].(map[string]any)["visible"] != false {
		t.Fatalf("入场券初态: %v", enters)
	}
	if d := a.get("/rob_battles/rob_battle_quest_execute.json", "mode=rob_battle&user_id="+b.uid); !m10Modal(d) {
		t.Fatalf("未购券开战应拒绝: %v", d)
	}
	bought := m8Data(t, a.get("/rob_battles/buy_rob_ticket", "ticket_id=1"))
	if e0 := bought["rob_battle_enter"].(map[string]any)["enters"].([]any)[0].(map[string]any); e0["can_enter"] != true {
		t.Fatalf("购券后应可入场: %v", e0)
	}
	if d := a.get("/rob_battles/buy_rob_ticket", "ticket_id=2"); !m10Modal(d) {
		t.Fatalf("rc>0 时不可再购: %v", d)
	}

	cands := m8Data(t, a.get("/rob_battles/get_candidates", ""))["rob_candidates"].([]any)
	if len(cands) != 10 {
		t.Fatalf("候选数: %d", len(cands))
	}
	ready := m8Data(t, a.get("/rob_battles/battle_ready", "user_id="+b.uid))["rob_battle_ready"].(map[string]any)
	if en := ready["enemy"].([]any); len(en) != 10 || en[0].(map[string]any)["monster_uuid"] != "" {
		t.Fatalf("enemy: %d", len(en))
	}
	ex := a.get("/rob_battles/rob_battle_quest_execute.json", "mode=rob_battle&user_id="+b.uid)
	if len(ex["units"].([]any)) != 10 || len(ex["enemy"].([]any)) != 10 {
		t.Fatalf("AutoQuest 文档: units=%v enemy=%v", len(ex["units"].([]any)), len(ex["enemy"].([]any)))
	}
	assets := strings.Join(toStrs(ex["asset_names"]), ",")
	if !strings.Contains(assets, "common/player_flag") || !strings.Contains(assets, "common/enemy_flag") {
		t.Fatal("asset_names 缺旗帜")
	}
	m10NoDupAssets(t, ex)
	bid := bidOf(t, ex["ap_use_url"])
	a.get("/rob_battles/rc_used", "bid="+bid)
	a.get("/rob_battles/rc_used", "bid="+bid)
	if n := m10Int(t, st, `SELECT rc_left FROM cn35_rob_profiles WHERE user_id=?`, a.uid); n != 4 {
		t.Fatalf("rc 只扣一次: %d", n)
	}
	res := a.get("/rob_battles/attack", "bid="+bid+"&time=60")
	if res["status"] != "success" || res["rob_battle_score"] != float64(30) {
		t.Fatalf("attack: %v", res)
	}
	if again := a.get("/rob_battles/attack", "bid="+bid); again["status"] != "already_received" {
		t.Fatalf("重放: %v", again)
	}
	if n := m10Int(t, st, `SELECT score FROM cn35_rob_profiles WHERE user_id=?`, a.uid); n != 1030 {
		t.Fatalf("A 积分: %d", n)
	}
	if n := m10Int(t, st, `SELECT score FROM cn35_rob_profiles WHERE user_id=?`, b.uid); n != 985 {
		t.Fatalf("B 积分: %d", n)
	}
	// B 复仇。
	rec := m8Data(t, b.get("/rob_battles/show_rob_battles_info", ""))["rob_battle_record"].([]any)
	if len(rec) != 1 || rec[0].(map[string]any)["btn_visible"] != true {
		t.Fatalf("战绩: %v", rec)
	}
	path := rec[0].(map[string]any)["message"].(map[string]any)["requireData"].(map[string]any)["rob_battle_ready"].(map[string]any)["path"].(string)
	revenge := strings.SplitN(path, "revenge_id=", 2)[1]
	fill(b, "atk", bUnits)
	fill(a, "def", aUnits)
	b.get("/rob_battles/buy_rob_ticket", "ticket_id=1")
	bex := b.get("/rob_battles/rob_battle_quest_execute.json", "mode=rob_battle&user_id="+a.uid+"&revenge_id="+revenge)
	bb := bidOf(t, bex["ap_use_url"])
	b.get("/rob_battles/rc_used", "bid="+bb)
	b.get("/rob_battles/attack", "bid="+bb)
	if n := m10Int(t, st, `SELECT revenged FROM cn35_rob_records WHERE record_id=?`, revenge); n != 1 {
		t.Fatal("复仇标记")
	}
	// NPC 败北：积分不变。
	npc := cn35game.NPCOpponentID("rob", cn35game.RobNPCPeriod(clock.Now()), 2)
	nex := a.get("/rob_battles/rob_battle_quest_execute.json", "mode=rob_battle&user_id="+npc)
	if m10Modal(nex) {
		t.Fatalf("NPC 开战: %v", nex)
	}
	nb := bidOf(t, nex["ap_use_url"])
	scoreBefore := m10Int(t, st, `SELECT score FROM cn35_rob_profiles WHERE user_id=?`, a.uid)
	a.get("/rob_battles/rc_used", "bid="+nb)
	a.get("/rob_battles/lose", "bid="+nb)
	if n := m10Int(t, st, `SELECT score FROM cn35_rob_profiles WHERE user_id=?`, a.uid); n != scoreBefore {
		t.Fatalf("败北后积分: %d", n)
	}
	if rc := m8Data(t, a.get("/users/preset_data.json", ""))["user"].(map[string]any)["rc"]; rc != float64(3) {
		t.Fatalf("user.rc: %v", rc)
	}
}

// m10NoDupAssets 断言 asset_names 无重复：客户端 Quest.OnPrepare 逐项
// textures.Add，重复即 ArgumentException 整战加载失败。
func m10NoDupAssets(t *testing.T, doc map[string]any) {
	t.Helper()
	seen := map[string]bool{}
	for _, a := range toStrs(doc["asset_names"]) {
		if seen[a] {
			t.Fatalf("asset_names 重复: %s", a)
		}
		seen[a] = true
	}
}

func toStrs(v any) []string {
	out := []string{}
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// TestM10SettleConcurrent 并发结算同一 bid：积分、药水只记一次。
func TestM10SettleConcurrent(t *testing.T) {
	clock := &m8Clock{now: time.Now()}
	ts, st, _ := m10Server(t, filepath.Join(t.TempDir(), "m10.sqlite3"), clock)
	defer ts.Close()
	defer st.Close()
	a := m10Login(t, ts, st, "dev-m10-cc", 30)
	m10Monster(t, st, a.uid, "cc-m1", 26)
	a.get("/ladders/set_deck", "party_index=0&uuid=cc-m1")
	npc := cn35game.NPCOpponentID("ladder", cn35game.LadderPeriodAt(clock.Now()).SeasonKey, 0)
	ex := a.get("/ladders/ladder_quest_execute.json", "mode=ladder&difficulty_id=hard&user_id="+npc+"&party_id=001")
	bid := bidOf(t, ex["ap_use_url"])
	a.get("/ladders/lc_used", "bid="+bid)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = st.LadderSettle(a.uid, bid, true)
		}()
	}
	wg.Wait()
	sess := m10Int(t, st, `SELECT score_delta FROM cn35_ladder_sessions WHERE battle_id=?`, bid)
	if n := m10Int(t, st, `SELECT score FROM cn35_ladder_profiles WHERE user_id=?`, a.uid); n != sess || n == 0 {
		t.Fatalf("并发结算积分 %d / 会话 %d", n, sess)
	}
	if n := m10Int(t, st, `SELECT atk_win FROM cn35_ladder_profiles WHERE user_id=?`, a.uid); n != 1 {
		t.Fatalf("atk_win: %d", n)
	}
}

// m10Capture 读 MLK_CAPTURE_DIR 下首个匹配 stem 的录播（只读，不入库）。
func m10Capture(t *testing.T, dir, stem string) map[string]any {
	t.Helper()
	for _, sub := range []string{"default", "1", "2", "3"} {
		matches, _ := filepath.Glob(filepath.Join(dir, sub, stem+"*.json"))
		for _, m := range matches {
			raw, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			var doc map[string]any
			if json.Unmarshal(raw, &doc) == nil {
				return doc
			}
		}
	}
	t.Fatalf("录播缺 %s", stem)
	return nil
}

// m10KeysSuperset 服务端对象键 ⊇ 录播对象键，并核对标量 JSON 类型一致（不比玩家值）。
func m10KeysSuperset(t *testing.T, name string, server, capture any) {
	t.Helper()
	cm, ok := capture.(map[string]any)
	if !ok {
		return
	}
	sm, ok := server.(map[string]any)
	if !ok {
		t.Fatalf("%s: 服务端不是对象: %T", name, server)
	}
	for k, cv := range cm {
		sv, ok := sm[k]
		if !ok {
			t.Errorf("%s 缺键 %q", name, k)
			continue
		}
		if cv == nil || sv == nil {
			continue
		}
		if fmt.Sprintf("%T", cv) != fmt.Sprintf("%T", sv) {
			t.Errorf("%s.%s 类型不匹配: 服务端=%T 录播=%T", name, k, sv, cv)
		}
	}
}

func m10First(v any) any {
	if l, ok := v.([]any); ok && len(l) > 0 {
		return l[0]
	}
	return nil
}

func TestM10CaptureReadFaceReplay(t *testing.T) {
	dir := os.Getenv("MLK_CAPTURE_DIR")
	if dir == "" {
		t.Skip("未设置 MLK_CAPTURE_DIR（只读解密录播目录）")
	}
	clock := &m8Clock{now: time.Now()}
	ts, st, cat := m10Server(t, filepath.Join(t.TempDir(), "m10.sqlite3"), clock)
	defer ts.Close()
	defer st.Close()
	a := m10Login(t, ts, st, "dev-m10-cap", 30)
	m10Monster(t, st, a.uid, "cap-m1", 26)
	a.get("/ladders/set_deck", "party_index=0&uuid=cap-m1")
	units := m10Units(t, st, cat, a.uid, map[int]bool{336: true, 348: true})
	for i, id := range units {
		a.get("/user_rob_decks/on", fmt.Sprintf("party_type=atk&party_id=%d&party_index=%d&unit_id=%d", i/5+1, i%5, id))
	}

	cap := m10Data(m10Capture(t, dir, "ladders_show_ladder_info"))
	srv := m8Data(t, a.get("/ladders/show_ladder_info", ""))
	m10KeysSuperset(t, "show_ladder_info", srv, cap)
	m10KeysSuperset(t, "ladder_main_page", srv["ladder_main_page"], cap["ladder_main_page"])
	cmp := cap["ladder_main_page"].(map[string]any)
	smp := srv["ladder_main_page"].(map[string]any)
	m10KeysSuperset(t, "ladder_info[0]", m10First(smp["ladder_info"]), m10First(cmp["ladder_info"]))
	m10KeysSuperset(t, "btn_list[0]", m10First(smp["btn_list"]), m10First(cmp["btn_list"]))

	cc := m10Data(m10Capture(t, dir, "ladders_get_candidates"))["candidates"]
	sc := m8Data(t, a.get("/ladders/get_candidates", ""))["candidates"]
	m10KeysSuperset(t, "ladder candidate", m10First(sc), m10First(cc))

	cd := m10Data(m10Capture(t, dir, "ladders_monsters"))
	sd := m8Data(t, a.get("/ladders/monsters", ""))
	m10KeysSuperset(t, "ladders_monsters", sd, cd)
	m10KeysSuperset(t, "ladder_deck_data", sd["ladder_deck_data"], cd["ladder_deck_data"])

	cg := m10Data(m10Capture(t, dir, "ladders_monster_grow_index"))["monster_grow"].(map[string]any)
	sg := m8Data(t, a.get("/ladders/monster_grow_index", ""))["monster_grow"].(map[string]any)
	m10KeysSuperset(t, "monster_grow", sg, cg)
	cgh := cg["tab_data_list"].(map[string]any)["hp"].(map[string]any)
	sgh := sg["tab_data_list"].(map[string]any)["hp"].(map[string]any)
	m10KeysSuperset(t, "monster_grow.hp", sgh, cgh)
	m10KeysSuperset(t, "monster_grow.hp.list[0]", m10First(sgh["list"]), m10First(cgh["list"]))
	if sgh["list"].([]any)[0].(map[string]any)["price"] != cgh["list"].([]any)[0].(map[string]any)["price"] {
		t.Error("增幅基础价格应与录播一致")
	}

	cr := m10Data(m10Capture(t, dir, "ladders_show_rank"))["ladder_ranking_list"].(map[string]any)
	sr := m8Data(t, a.get("/ladders/show_rank", ""))["ladder_ranking_list"].(map[string]any)
	m10KeysSuperset(t, "ladder_ranking_list", sr, cr)
	m10KeysSuperset(t, "ladder_rank", sr["rank_list_data"].(map[string]any)["ladder_rank"], cr["rank_list_data"].(map[string]any)["ladder_rank"])

	ce := m10Capture(t, dir, "ladders_ladder_quest_execute")
	npc := cn35game.NPCOpponentID("ladder", cn35game.LadderPeriodAt(clock.Now()).SeasonKey, 1)
	se := a.get("/ladders/ladder_quest_execute.json", "mode=ladder&difficulty_id=normal&user_id="+npc+"&party_id=001")
	m10KeysSuperset(t, "ladder execute", se, ce)
	m10KeysSuperset(t, "ladder execute.quest", se["quest"], ce["quest"])
	m10KeysSuperset(t, "ladder boss", m10First(se["boss_appear_table"].(map[string]any)["1"]), m10First(ce["boss_appear_table"].(map[string]any)["1"]))
	cq, sq := ce["quest"].(map[string]any), se["quest"].(map[string]any)
	for _, k := range []string{"initial_power", "difficulty_name", "continuable"} {
		if fmt.Sprint(cq[k]) != fmt.Sprint(sq[k]) {
			t.Errorf("quest.%s = %v want %v", k, sq[k], cq[k])
		}
	}
	if fmt.Sprint(ce["exp"]) != fmt.Sprint(se["exp"]) || fmt.Sprint(cq["wave_conf"]) != fmt.Sprint(sq["wave_conf"]) {
		t.Errorf("exp/wave_conf 与录播不一致: %v %v", se["exp"], sq["wave_conf"])
	}

	ci := m10Data(m10Capture(t, dir, "rob_battles_show_rob_battles_info"))
	si := m8Data(t, a.get("/rob_battles/show_rob_battles_info", ""))
	m10KeysSuperset(t, "rob info", si, ci)
	m10KeysSuperset(t, "rob_battle_enter", si["rob_battle_enter"], ci["rob_battle_enter"])
	m10KeysSuperset(t, "enters[0]", m10First(si["rob_battle_enter"].(map[string]any)["enters"]), m10First(ci["rob_battle_enter"].(map[string]any)["enters"]))
	ce0 := ci["rob_battle_enter"].(map[string]any)["enters"].([]any)
	se0 := si["rob_battle_enter"].(map[string]any)["enters"].([]any)
	for i := range ce0 {
		c, s := ce0[i].(map[string]any), se0[i].(map[string]any)
		if c["price"] != s["price"] || c["text"] != s["text"] || c["url"] != s["url"] {
			t.Errorf("入场券 %d 与录播不一致: %v / %v", i+1, s, c)
		}
	}
	crc := m10Data(m10Capture(t, dir, "rob_battles_get_candidates"))["rob_candidates"]
	src := m8Data(t, a.get("/rob_battles/get_candidates", ""))["rob_candidates"]
	m10KeysSuperset(t, "rob candidate", m10First(src), m10First(crc))
	m10KeysSuperset(t, "rob candidate.message", m10First(src).(map[string]any)["message"], m10First(crc).(map[string]any)["message"])

	cready := m10Data(m10Capture(t, dir, "rob_battles_battle_ready"))["rob_battle_ready"].(map[string]any)
	rnpc := cn35game.NPCOpponentID("rob", cn35game.RobNPCPeriod(clock.Now()), 0)
	sready := m8Data(t, a.get("/rob_battles/battle_ready", "user_id="+rnpc))["rob_battle_ready"].(map[string]any)
	m10KeysSuperset(t, "rob_battle_ready", sready, cready)
	enemyC := m10First(cready["enemy"]).(map[string]any)
	enemyS := m10First(sready["enemy"]).(map[string]any)
	for _, k := range []string{"id", "level", "hp", "at", "attr_id", "weapon_id", "reach", "attack_ms", "speed", "asset_name", "uuid"} {
		if _, ok := enemyC[k]; ok {
			if _, ok := enemyS[k]; !ok {
				t.Errorf("enemy 缺关键键 %q", k)
			}
		}
	}
	cdeck := m10Data(m10Capture(t, dir, "user_rob_decks"))
	sdeck := m8Data(t, a.get("/user_rob_decks", "party_type=atk"))
	for k, v := range cdeck {
		if k == "user_rob_atk_decks" {
			m10KeysSuperset(t, k, sdeck[k], v)
		}
	}
}

// TestM10ReviewRegressions 覆盖评审发现的边界回归：
// 1) 防御魔宠被删（悬空 UUID）后 LadderOpen 拒绝开战；
// 2) prepared 状态未扣次直接 lose 不污染真实对手防守统计与战绩；
// 3) 公会训练攻队同伴装备魔宠时 asset_names 与 monsters 补齐该魔宠；
// 4) 并发编入治疗同伴不超过上限 2。
func TestM10ReviewRegressions(t *testing.T) {
	clock := &m8Clock{now: time.Now()}
	ts, st, cat := m10Server(t, filepath.Join(t.TempDir(), "m10-reg.sqlite3"), clock)
	defer ts.Close()
	defer st.Close()
	a := m10Login(t, ts, st, "dev-m10-rega", 30)
	b := m10Login(t, ts, st, "dev-m10-regb", 30)

	// 1) 悬空防御魔宠 UUID 不得通过开战门禁。
	m10Monster(t, st, a.uid, "reg-a-m1", 26)
	m10Monster(t, st, b.uid, "reg-b-m1", 33)
	a.get("/ladders/set_deck", "party_index=0&uuid=reg-a-m1")
	b.get("/ladders/set_deck", "party_index=0&uuid=reg-b-m1")
	if _, err := st.DB().Exec(`DELETE FROM cn35_player_monsters WHERE user_id=? AND uuid='reg-a-m1'`, a.uid); err != nil {
		t.Fatal(err)
	}
	if d := a.get("/ladders/ladder_quest_execute.json", "mode=ladder&difficulty_id=normal&user_id="+b.uid+"&party_id=001"); !m10Modal(d) {
		t.Fatalf("悬空防御魔宠开战应弹窗拒绝: %v", d)
	}

	// 2) 癒术士大赛 prepared 状态未调 lc_used 直接 lose，不污染 B 的防守计数。
	m10Monster(t, st, a.uid, "reg-a-m2", 26)
	a.get("/ladders/set_deck", "party_index=0&uuid=reg-a-m2")
	ex := a.get("/ladders/ladder_quest_execute.json", "mode=ladder&difficulty_id=normal&user_id="+b.uid+"&party_id=001")
	bid := bidOf(t, ex["ap_use_url"])
	if d := a.get("/ladders/lose", "bid="+bid); d["status"] != "success" {
		t.Fatalf("prepared lose: %v", d)
	}
	if n := m10Int(t, st, `SELECT def_total FROM cn35_ladder_profiles WHERE user_id=?`, b.uid); n != 0 {
		t.Fatalf("未扣次 lose 不应增加对手 def_total: %d", n)
	}

	// 3) 公会训练攻队同伴装备魔宠 → asset_names 与 monsters 均包含该魔宠；且 prepared lose 不写战绩。
	aUnits := m10Units(t, st, cat, a.uid, map[int]bool{336: true, 348: true})
	bUnits := m10Units(t, st, cat, b.uid, map[int]bool{336: true, 348: true})
	for i, id := range aUnits {
		a.get("/user_rob_decks/on", fmt.Sprintf("party_type=atk&party_id=%d&party_index=%d&unit_id=%d", i/5+1, i%5, id))
	}
	for i, id := range bUnits {
		b.get("/user_rob_decks/on", fmt.Sprintf("party_type=def&party_id=%d&party_index=%d&unit_id=%d", i/5+1, i%5, id))
	}
	m10Monster(t, st, a.uid, "reg-seed-1", 81)
	if _, err := st.DB().Exec(`UPDATE cn35_player_units SET monster_uuid='reg-seed-1' WHERE user_id=? AND unit_id=?`, a.uid, aUnits[0]); err != nil {
		t.Fatal(err)
	}
	a.get("/rob_battles/buy_rob_ticket", "ticket_id=1")
	rex := a.get("/rob_battles/rob_battle_quest_execute.json", "mode=rob_battle&user_id="+b.uid)
	assets := strings.Join(toStrs(rex["asset_names"]), ",")
	if !strings.Contains(assets, "monster/monster_btn_81") || !strings.Contains(assets, "monster/monster_large_ns_81") {
		t.Fatalf("公会训练攻队装备魔宠应进入 asset_names: %s", assets)
	}
	if mons, ok := rex["monsters"].(map[string]any); !ok || mons["81"] == nil {
		t.Fatalf("公会训练攻队装备魔宠应进入 monsters 字典: %v", rex["monsters"])
	}
	rbid := bidOf(t, rex["ap_use_url"])
	if d := a.get("/rob_battles/lose", "bid="+rbid); d["status"] != "success" {
		t.Fatalf("rob prepared lose: %v", d)
	}
	if n := m10Int(t, st, `SELECT count(*) FROM cn35_rob_records WHERE defender_id=?`, b.uid); n != 0 {
		t.Fatalf("未扣次 lose 不应写入对手战绩: %d", n)
	}

	// 4) 并发编入 4 名治疗同伴，最终治疗人数不得超过 2。
	heals := []int{}
	for _, id := range cat.UnitIDs() {
		if u, _ := cat.Unit(id); u != nil && u.Weapon == 7 && u.Rare >= 2 {
			heals = append(heals, id)
			if len(heals) == 4 {
				break
			}
		}
	}
	if len(heals) == 4 {
		for _, id := range heals {
			_ = st.WithTx(func(tx *sql.Tx) error {
				return cn35store.InsertUnitTx(tx, a.uid, cn35game.UnitRecord{UnitID: id, Level: 1})
			})
		}
		weaponOf := func(id int) int {
			if u, _ := cat.Unit(id); u != nil {
				return u.Weapon
			}
			return 0
		}
		var wg sync.WaitGroup
		for i, id := range heals {
			wg.Add(1)
			go func(slot, uid int) {
				defer wg.Done()
				_ = st.RobSetDeck(a.uid, "def", 1, slot, uid, true, weaponOf)
			}(i, id)
		}
		wg.Wait()
		if n := m10Int(t, st, `SELECT count(*) FROM cn35_rob_decks WHERE user_id=? AND party_type='def'`, a.uid); n > 2 {
			t.Fatalf("并发编入治疗超过上限 2: %d", n)
		}
	}
}

// TestM10RobDeckColdCatalogNoDeadlock 回归（2026-11-21 线上 P0）：Catalog 为
// cn35game.NewRosterCatalog(cat, st)（与 cn35app 生产装配一致）且快照冷时，
// /user_rob_decks/on 不得在 RobSetDeck 的 tx 内回源 DB——SQLite
// MaxOpenConns(1)，tx 占住唯一连接后再走 rosterCatalog.snapshot →
// st.AllUnitTemplates 即死锁，其后所有请求全部挂起。修复后 weaponOf 只读
// tx 外预解的 map。用 raw cat 准备数据保证 rosterCatalog 快照保持冷。
func TestM10RobDeckColdCatalogNoDeadlock(t *testing.T) {
	clock := &m8Clock{now: time.Now()}
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(t.TempDir(), "m10-cold.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	st.SetClock(clock.Now)
	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		Accounts: st, Sessions: st, Inventory: st, Decks: st, Quests: st, Battles: st,
		Items: st, ItemUse: st, Presents: st, PresentReceive: st, PvP: st,
		Catalog:    cn35game.NewRosterCatalog(cat, st),
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
		BattleRand: mathrand.New(mathrand.NewSource(10)),
		GuildClock: clock.Now,
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())

	a := m10Login(t, ts, st, "dev-m10-cold", 30)
	units := m10Units(t, st, cat, a.uid, nil)

	done := make(chan map[string]any, 1)
	go func() {
		done <- a.get("/user_rob_decks/on",
			fmt.Sprintf("party_type=def&party_id=1&party_index=0&unit_id=%d", units[0]))
	}()
	select {
	case d := <-done:
		if m10Modal(d) {
			t.Fatalf("编成应成功: %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("/user_rob_decks/on 超时：疑似 tx 内回源 DB 死锁")
	}
	// 不在本测试 defer st.Close()/ts.Close()：回归失败（死锁）场景下两者会
	// 分别等 OpenTx/在途请求而自身挂死，掩盖超时报告。正常路径即时关闭。
	st.Close()
	ts.Close()
}
