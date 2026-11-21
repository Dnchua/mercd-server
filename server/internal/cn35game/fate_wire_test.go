// fate_wire_test.go FATE350-01..03：M1 命运星盘（跳过光环/天赋树）协议回放与 .mlk 双向契约测试。
package cn35game_test

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func fateFriendServer(t *testing.T, stateDir string, clock func() time.Time) (*httptest.Server, *cn35store.Store, *cardcatalog.Catalog) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	if clock != nil {
		st.SetClock(clock)
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
		Items:      st,
		Resources:  st,
		Medal:      st,
		Awaken:     st,
		DailyShop:  st,
		Skills:     st,
		Guild:      st,
		GuildClock: clock,
		Fate:       st,
		FateClock:  clock,
		Friends:    st,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	return ts, st, cat
}

func TestFateAstrolabeWireContract(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	ts, st, _ := fateFriendServer(t, t.TempDir(), func() time.Time { return now })
	defer ts.Close()
	defer st.Close()

	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-fate-01")

	// 给测试账号准备 50 钻石以覆盖内圈/全盘转动。
	if _, err := st.DB().Exec(`UPDATE cn35_player_balances SET amount=50 WHERE user_id=? AND currency='coin'`, uid); err != nil {
		t.Fatalf("预置钻石失败: %v", err)
	}

	// 1. BV-FATE-01：日常入口与未占卜态契约
	fp := gameData(t, ts.URL, "/users/function_portal", uid, sk, s0, s1, "")["function_portal"].(map[string]any)
	if fp["chara_buff_button_disable"] != true {
		t.Fatalf("放弃光环/天赋树时 chara_buff_button_disable 必须为 true: %v", fp)
	}
	if fp["has_new_main_chara_daily"] != true {
		t.Fatalf("今日未占卜时 has_new_main_chara_daily 应为 true: %v", fp)
	}

	// /users/preset_data 必须返回 user_red_coin 与 function_portal（供 FateAstrolabePage requireData 消费）
	preset := gameData(t, ts.URL, "/users/preset_data/", uid, sk, s0, s1, "")
	if preset["user_red_coin"] == nil {
		t.Fatalf("preset_data 缺少 user_red_coin: %v", preset)
	}
	if preset["user_red_coin"].(map[string]any)["amount"] != float64(50) {
		t.Fatalf("user_red_coin.amount = %v, want 50", preset["user_red_coin"])
	}

	show0 := gameData(t, ts.URL, "/fate/show", uid, sk, s0, s1, "")["fate"].(map[string]any)
	if show0["today_roll_times"] != float64(0) || show0["roll_inner_price"] != float64(10) || show0["roll_all_price"] != float64(18) {
		t.Fatalf("未占卜态 /fate/show 字段不符: %v", show0)
	}
	if len(show0["fate_list"].([]any)) != 0 {
		t.Fatalf("未占卜态 fate_list 应为空数组: %v", show0["fate_list"])
	}

	sum0 := gameData(t, ts.URL, "/fate/fate_buff_summary", uid, sk, s0, s1, "")["fate_buff_summary"].(map[string]any)
	if sum0["disable_show_fate_new"] != false {
		t.Fatalf("未占卜态 disable_show_fate_new 应为 false: %v", sum0)
	}
	if len(sum0["summary_list"].([]any)) != 0 {
		t.Fatalf("未占卜态 summary_list 应为空: %v", sum0)
	}

	// 2. BV-FATE-02：当日首次免费转动（type=all&confirm=false, cost=0）
	roll1 := gameJSON(t, ts.URL, "/fate/roll", uid, sk, s0, s1, "type=all&confirm=false", "_method=GET")
	r1Data := roll1["data"].(map[string]any)
	if r1Data["temp_fate_roll_result"] != true {
		t.Fatalf("首次免费占卜应返回 temp_fate_roll_result=true: %v", roll1)
	}
	if _, hasNullUnits := r1Data["user_units"]; !hasNullUnits || r1Data["user_units"] != nil {
		t.Fatalf("/fate/roll 必须下发 user_units:null 触发客户端 DataCache.Expire: %v", r1Data)
	}
	if r1Data["user_red_coin"].(map[string]any)["amount"] != float64(50) {
		t.Fatalf("首次免费占卜不应扣钻: %v", r1Data["user_red_coin"])
	}
	if r1Data["user_coin"] == nil || r1Data["user_coin"].(map[string]any)["amount"] != float64(50) {
		t.Fatalf("首次免费占卜必须同步回推 user_coin.amount=50: %v", r1Data["user_coin"])
	}
	show1 := r1Data["fate"].(map[string]any)
	if show1["today_roll_times"] != float64(1) {
		t.Fatalf("首转后 today_roll_times 应为 1: %v", show1)
	}
	fateList1 := show1["fate_list"].([]any)
	if len(fateList1) != 3 {
		t.Fatalf("首转后 fate_list 必须含 3 条词条: %v", fateList1)
	}
	ids1 := [3]string{
		fateList1[0].(map[string]any)["id"].(string),
		fateList1[1].(map[string]any)["id"].(string),
		fateList1[2].(map[string]any)["id"].(string),
	}
	if ids1[0] == ids1[1] || ids1[0] == ids1[2] || ids1[1] == ids1[2] {
		t.Fatalf("外圈三槽词条 ID 必须互不重复: %v", ids1)
	}

	// 3. BV-FATE-03：已占卜态的 fate_buff_summary 契约
	sum1 := gameData(t, ts.URL, "/fate/fate_buff_summary", uid, sk, s0, s1, "")["fate_buff_summary"].(map[string]any)
	if sum1["disable_show_fate_new"] != true {
		t.Fatalf("占卜后 disable_show_fate_new 应为 true: %v", sum1)
	}
	summaryList := sum1["summary_list"].([]any)
	if len(summaryList) != 3 {
		t.Fatalf("summary_list 应包含 3 条已生效词条: %v", summaryList)
	}
	desc1 := summaryList[0].(map[string]any)["desc"].(string)
	if !strings.Contains(desc1, "主角光环[eb8900]+0") || !strings.Contains(desc1, "星盘[eb8900]+") {
		t.Fatalf("summary desc 应包含主角光环零值占位与星盘加成: %q", desc1)
	}

	// 4. 将当前星盘第 1 条设为 rank="amazing" 验证 confirm=false 二次确认弹窗契约
	periodKey := cn35game.FatePeriodKey(now)
	stCur, err := st.FateState(uid, periodKey)
	if err != nil {
		t.Fatalf("读取 FateState 失败: %v", err)
	}
	stCur.FateList[0].Rank = "amazing"
	rawJSON, _ := json.Marshal(stCur.FateList)
	if _, err := st.DB().Exec(`UPDATE cn35_player_fate SET fate_list_json=? WHERE user_id=?`, string(rawJSON), uid); err != nil {
		t.Fatalf("更新 amazing 词条失败: %v", err)
	}
	confirmResp := gameJSON(t, ts.URL, "/fate/roll", uid, sk, s0, s1, "type=inner&confirm=false", "_method=GET")
	if confirmResp["astrolabe_need_confirm"] != true {
		t.Fatalf("含 amazing 词条且 confirm=false 时应返回 astrolabe_need_confirm=true: %v", confirmResp)
	}
	if confirmResp["modal"].(map[string]any)["type"] != "yes_no" {
		t.Fatalf("二次确认弹窗应为 yes_no: %v", confirmResp["modal"])
	}

	// 5. 带 confirm=true 转动内圈（type=inner, 扣 10 钻，3 个词条 ID 保持不变）
	rollInner := gameJSON(t, ts.URL, "/fate/roll", uid, sk, s0, s1, "type=inner&confirm=true", "_method=GET")
	rInnerData := rollInner["data"].(map[string]any)
	if rInnerData["temp_fate_roll_result"] != true {
		t.Fatalf("confirm=true 转动内圈应成功: %v", rollInner)
	}
	if rInnerData["user_red_coin"].(map[string]any)["amount"] != float64(40) {
		t.Fatalf("转动内圈应扣 10 钻（50 -> 40）: %v", rInnerData["user_red_coin"])
	}
	if rInnerData["user_coin"] == nil || rInnerData["user_coin"].(map[string]any)["amount"] != float64(40) {
		t.Fatalf("转动内圈必须同步回推 user_coin.amount=40: %v", rInnerData["user_coin"])
	}
	fateListInner := rInnerData["fate"].(map[string]any)["fate_list"].([]any)
	idsInner := [3]string{
		fateListInner[0].(map[string]any)["id"].(string),
		fateListInner[1].(map[string]any)["id"].(string),
		fateListInner[2].(map[string]any)["id"].(string),
	}
	if idsInner != ids1 {
		t.Fatalf("转动内圈必须保留原有 3 个词条 ID: got %v, want %v", idsInner, ids1)
	}

	// 6. 转动全盘（type=all&confirm=true, 扣 18 钻 -> 剩余 22 钻）
	rollAll := gameJSON(t, ts.URL, "/fate/roll", uid, sk, s0, s1, "type=all&confirm=true", "_method=GET")
	rAllData := rollAll["data"].(map[string]any)
	if rAllData["temp_fate_roll_result"] != true {
		t.Fatalf("转动全盘应成功: %v", rollAll)
	}
	if rAllData["user_red_coin"].(map[string]any)["amount"] != float64(22) {
		t.Fatalf("转动全盘应扣 18 钻（40 -> 22）: %v", rAllData["user_red_coin"])
	}
	if rAllData["user_coin"] == nil || rAllData["user_coin"].(map[string]any)["amount"] != float64(22) {
		t.Fatalf("转动全盘必须同步回推 user_coin.amount=22: %v", rAllData["user_coin"])
	}

	// 7. 钻石不足时拒绝并弹 modal
	if _, err := st.DB().Exec(`UPDATE cn35_player_balances SET amount=5 WHERE user_id=? AND currency='coin'`, uid); err != nil {
		t.Fatalf("设置钻石=5 失败: %v", err)
	}
	noCoin := gameJSON(t, ts.URL, "/fate/roll", uid, sk, s0, s1, "type=inner&confirm=true", "_method=GET")
	if noCoin["data"].(map[string]any)["temp_fate_roll_result"] != false || noCoin["modal"] == nil {
		t.Fatalf("钻石不足应返回 temp_fate_roll_result=false 与 modal: %v", noCoin)
	}

	// 8. 跨 04:00 UTC+8 周期刷新重置为未占卜态
	now = now.Add(24 * time.Hour)
	showNextDay := gameData(t, ts.URL, "/fate/show", uid, sk, s0, s1, "")["fate"].(map[string]any)
	if showNextDay["today_roll_times"] != float64(0) || len(showNextDay["fate_list"].([]any)) != 0 {
		t.Fatalf("跨日后应重置为免费未占卜态: %v", showNextDay)
	}
}

func TestFateBuffEffectApplication(t *testing.T) {
	// 验证同伴生命/攻击加成与泉水金币加成公式
	udoc := map[string]any{
		"hp": 2000,
		"at": 1000,
	}
	entries := []cn35game.FateEntry{
		{ID: "unit_fire_hp", Rank: "amazing", Value: 0.1},
		{ID: "unit_fire_atk", Rank: "good", Value: 0.08},
	}
	// 火属性同伴（element=1）应获得 +10% 生命与 +8% 攻击
	cn35game.ApplyUnitFateBuffs(udoc, 1, entries)
	if udoc["hp"] != 2200 || udoc["at"] != 1080 {
		t.Fatalf("火属性同伴加成错误: hp=%v (want 2200), at=%v (want 1080)", udoc["hp"], udoc["at"])
	}
	// 水属性同伴（element=2）不受火属性词条影响
	udocWater := map[string]any{"hp": 2000, "at": 1000}
	cn35game.ApplyUnitFateBuffs(udocWater, 2, entries)
	if udocWater["hp"] != 2000 || udocWater["at"] != 1000 {
		t.Fatalf("水属性同伴不应受火属性词条影响: %v", udocWater)
	}
	// 泉水金币 +30%
	if got := cn35game.ApplyFateRateBonus(1000, 0.3); got != 1300 {
		t.Fatalf("ApplyFateRateBonus(1000, 0.3) = %d, want 1300", got)
	}

	// 校验随机抽取池不含无实效的五系救援率词条（unit_*_rate）
	if len(cn35game.RollableFateBuffCatalog) != 17 {
		t.Fatalf("RollableFateBuffCatalog 长度应为 17, got %d", len(cn35game.RollableFateBuffCatalog))
	}
	for _, s := range cn35game.RollableFateBuffCatalog {
		if strings.HasSuffix(s.ID, "_rate") {
			t.Fatalf("RollableFateBuffCatalog 不应包含未启用救援系统的词条 %q", s.ID)
		}
	}
	for i := 0; i < 100; i++ {
		rolled, err := cn35game.RollFateEntries(nil, "all", nil)
		if err != nil {
			t.Fatalf("RollFateEntries 失败: %v", err)
		}
		for _, e := range rolled {
			if strings.HasSuffix(e.ID, "_rate") {
				t.Fatalf("RollFateEntries 随机抽到了无实效词条 %q", e.ID)
			}
		}
	}

	// 端到端验证：
	// (a) 主线探险与每日探险金币加成严格按 ConfigVersion 分流，互不叠加；
	// (b) 公会战个人贡献词条 guild_battle_per_con 放大 cn35_gw_member_scores.gp_earned；
	// (c) 公会战出击建档应用同伴星盘属性加成。
	now := time.Date(2026, 11, 7, 12, 45, 0, 0, time.FixedZone("CST", 8*3600))
	ts, st, _ := fateFriendServer(t, t.TempDir(), func() time.Time { return now })
	defer ts.Close()
	defer st.Close()

	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-fate-e2e")
	periodKey := cn35game.FatePeriodKey(now)

	// 注入同时含 gold_drop_main=20% 与 gold_drop_daily=50% 的星盘状态
	fateBoth, _ := json.Marshal([]cn35game.FateEntry{
		{ID: "gold_drop_main", Name: "探险财运", Rank: "good", Value: 0.20},
		{ID: "gold_drop_daily", Name: "日常财运", Rank: "amazing", Value: 0.50},
		{ID: "guild_battle_per_con", Name: "贡献信仰", Rank: "amazing", Value: 0.25},
	})
	if _, err := st.DB().Exec(`INSERT INTO cn35_player_fate (user_id,period_key,daily_luck_desc,today_roll_times,fate_list_json,updated_at)
		VALUES (?,?,?,1,?,'2026-11-07T00:00:00Z')`, uid, periodKey, "吉", string(fateBoth)); err != nil {
		t.Fatalf("写入测试星盘失败: %v", err)
	}

	// 主线会话（ConfigVersion == 0）只吃 gold_drop_main (+20%)，1000 -> 1200（若错误叠加会变成 1700）
	mainSess := &cn35game.BattleSession{
		BattleID: "bid-main-fate", UserID: uid, QuestID: 1, AreaID: 1, Difficulty: "normal",
		Status: cn35game.BattlePrepared, APCost: 1, ConfigVersion: 0,
		NextStageID: 1, NextAreaID: 1, NextQuestID: 1,
		Rewards: []cn35game.BattleReward{{Key: "gacha_point", Count: 1000}},
	}
	if _, _, err := st.OpenBattleForParty(mainSess, 600, 1, 5); err != nil {
		t.Fatalf("OpenBattleForParty(main) 失败: %v", err)
	}
	if mainSess.Rewards[0].Count != 1200 {
		t.Fatalf("主线探险金币只应享受 gold_drop_main(+20%%)=1200, got %d", mainSess.Rewards[0].Count)
	}

	// 每日资源副本会话（ConfigVersion > 0）只吃 gold_drop_daily (+50%)，1000 -> 1500
	dailySess := &cn35game.BattleSession{
		BattleID: "bid-daily-fate", UserID: uid, QuestID: 9001, AreaID: 901, Difficulty: "normal",
		Status: cn35game.BattlePrepared, APCost: 1, ConfigVersion: 1,
		NextStageID: 1, NextAreaID: 1, NextQuestID: 1,
		Rewards: []cn35game.BattleReward{{Key: "gacha_point", Count: 1000}},
	}
	if _, _, err := st.OpenBattleForParty(dailySess, 600, 1, 5); err != nil {
		t.Fatalf("OpenBattleForParty(daily) 失败: %v", err)
	}
	if dailySess.Rewards[0].Count != 1500 {
		t.Fatalf("每日探险金币只应享受 gold_drop_daily(+50%%)=1500, got %d", dailySess.Rewards[0].Count)
	}

	// 验证公会战个人贡献加成（guild_battle_per_con）与会战出击单位属性加成
	if err := st.ConfigureGuildSolo(cn35store.DefaultGuildSoloSettings); err != nil {
		t.Fatalf("ConfigureGuildSolo: %v", err)
	}
	if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET bp=3000 WHERE user_id=?`, uid); err != nil {
		t.Fatalf("设置 BP 失败: %v", err)
	}
	views, err := st.SearchGuilds(uid, "", "", "")
	if err != nil || len(views) == 0 {
		t.Fatalf("SearchGuilds: %v", err)
	}
	if _, err := st.JoinGuild(uid, views[0].Guild.ID, "fate-gw-join"); err != nil {
		t.Fatalf("JoinGuild: %v", err)
	}
	gw, err := st.GWState(uid, now)
	if err != nil {
		t.Fatalf("GWState: %v", err)
	}
	var targetGID string
	for _, p := range gw.Participants {
		if !p.Joined {
			targetGID = p.GuildID
			break
		}
	}
	// 先在无同伴属性星盘词条下执行一次会战出击，记录第 1 名参战单位的基础 hp/at
	gwDocBase := gameJSON(t, ts.URL, "/battles/guild_quest_execute.json", uid, sk, s0, s1,
		"mode=battle&guild_id="+targetGID+"&difficulty_id=normal&party_id=001", "_method=GET")
	u0Base := gwDocBase["units"].([]any)[0].(map[string]any)
	baseHP := u0Base["hp"].(float64)
	baseAT := u0Base["at"].(float64)
	// 结算掉该局以便发起下一局
	if _, err := st.GWAttackPay(uid, gwDocBase["data"].(map[string]any)["guild"].(map[string]any)["battle_id"].(string)); err != nil {
		t.Fatalf("GWAttackPay: %v", err)
	}
	if _, err := st.GWAttackSettle(uid, gwDocBase["data"].(map[string]any)["guild"].(map[string]any)["battle_id"].(string), false, 0); err != nil {
		t.Fatalf("GWAttackSettle: %v", err)
	}

	// 将星盘词条设为五系 HP/ATK 全 +10% 及 guild_battle_pray_gp=+10, guild_battle_per_con=+25%
	// 先验证祈祷：午场基础 PrayGP=10，加星盘 +10 得 gp=20，再乘 per_con(+25%) 得个人贡献 25
	var scoreBefore int64
	_ = st.DB().QueryRow(`SELECT COALESCE(SUM(gp_earned),0) FROM cn35_gw_member_scores WHERE user_id=?`, uid).Scan(&scoreBefore)
	fateGW, _ := json.Marshal([]cn35game.FateEntry{
		{ID: "guild_battle_pray_gp", Name: "祈祷信仰", Rank: "Normal", Value: 10},
		{ID: "guild_battle_per_con", Name: "贡献信仰", Rank: "amazing", Value: 0.25},
		{ID: "unit_fire_hp", Name: "火之生命", Rank: "good", Value: 0.10},
	})
	if _, err := st.DB().Exec(`UPDATE cn35_player_fate SET fate_list_json=? WHERE user_id=?`, string(fateGW), uid); err != nil {
		t.Fatalf("更新会战星盘失败: %v", err)
	}
	gpDelta, _, err := st.GWHelpPray(uid, "pray", "pray-fate-test", now)
	if err != nil {
		t.Fatalf("GWHelpPray: %v", err)
	}
	var scoreAfter int64
	if err := st.DB().QueryRow(`SELECT COALESCE(SUM(gp_earned),0) FROM cn35_gw_member_scores WHERE user_id=?`, uid).Scan(&scoreAfter); err != nil {
		t.Fatalf("查询个人贡献失败: %v", err)
	}
	wantContribDelta := cn35game.ApplyFateRateBonus(gpDelta, 0.25)
	if scoreAfter-scoreBefore != wantContribDelta || wantContribDelta <= gpDelta {
		t.Fatalf("guild_battle_per_con 个人贡献加成未生效: gpDelta=%d, scoreDelta=%d, want %d", gpDelta, scoreAfter-scoreBefore, wantContribDelta)
	}

	// 根据首位参战单位的 attr_id 注入对应属性 HP/ATK +10% 词条，验证 guild_quest_execute.json 单位面板生效
	attrID := int(u0Base["attr_id"].(float64))
	hpKey := map[int]string{1: "unit_fire_hp", 2: "unit_water_hp", 3: "unit_wind_hp", 4: "unit_light_hp", 5: "unit_dark_hp"}[attrID]
	atkKey := map[int]string{1: "unit_fire_atk", 2: "unit_water_atk", 3: "unit_wind_atk", 4: "unit_light_atk", 5: "unit_dark_atk"}[attrID]
	fateUnitGW, _ := json.Marshal([]cn35game.FateEntry{
		{ID: hpKey, Rank: "good", Value: 0.10},
		{ID: atkKey, Rank: "good", Value: 0.10},
		{ID: "guild_battle_per_con", Rank: "good", Value: 0.10},
	})
	if _, err := st.DB().Exec(`UPDATE cn35_player_fate SET fate_list_json=? WHERE user_id=?`, string(fateUnitGW), uid); err != nil {
		t.Fatalf("更新会战单位星盘失败: %v", err)
	}
	gwDocBuffed := gameJSON(t, ts.URL, "/battles/guild_quest_execute.json", uid, sk, s0, s1,
		"mode=battle&guild_id="+targetGID+"&difficulty_id=normal&party_id=001", "_method=GET")
	u0Buffed := gwDocBuffed["units"].([]any)[0].(map[string]any)
	if u0Buffed["hp"].(float64) <= baseHP || u0Buffed["at"].(float64) <= baseAT {
		t.Fatalf("公会战出击单位未生效星盘 HP/AT 加成: base=(%v,%v), buffed=(%v,%v)",
			baseHP, baseAT, u0Buffed["hp"], u0Buffed["at"])
	}
}
