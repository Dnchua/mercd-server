package cn35game_test

import (
	"database/sql"
	"fmt"
	"math"
	mathrand "math/rand"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func raidTestServer(t *testing.T, now time.Time, cfg cn35game.RaidConfig, roll float64) (*httptest.Server, *cn35store.Store) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(t.TempDir(), "raid-wire.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err = st.ConfigureGuildSolo(cn35store.DefaultGuildSoloSettings); err != nil {
		t.Fatalf("ConfigureGuildSolo: %v", err)
	}
	st.SetClock(func() time.Time { return now })

	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		Accounts:   st,
		Sessions:   st,
		Inventory:  st,
		Decks:      st,
		Quests:     st,
		Battles:    st,
		Items:      st,
		Skills:     st,
		Guild:      st,
		Raid:       st,
		RaidConfig: cfg,
		Catalog:    cat,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
		BattleRand: mathrand.New(mathrand.NewSource(7)),
		GuildClock: func() time.Time { return now },
		RaidClock:  func() time.Time { return now },
		RaidRand:   func() float64 { return roll },
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

func TestRaidWireEndToEndAndScreenshotAnchors(t *testing.T) {
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	ts, st := raidTestServer(t, now, cn35game.RaidConfig{
		BossMonsterID: 688,
		TriggerRate:   1.0,
		TTLSeconds:    5400,
		SoloAssist:    true,
	}, 0.0)

	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "raid-wire-a")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "raid-wire-b")
	seedBattleUnits(t, st, uidA)
	seedBattleUnits(t, st, uidB)
	for _, u := range []string{uidA, uidB} {
		if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET ap=200, max_ap=200, bp=600, max_bp=600 WHERE user_id=?`, u); err != nil {
			t.Fatal(err)
		}
	}

	// 为 A 建会并设置 Lv.100 祭坛/医疗所、Lv.101 训练所、Lv.90 食堂；B 同会。
	g, err := st.CreateGuild(uidA, "梅露可暴走测试会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for typ, pt := range map[string]int64{
		"sp":       cn35game.GuildEquipThreshold(100),
		"hp":       cn35game.GuildEquipThreshold(100),
		"at":       cn35game.GuildEquipThreshold(101),
		"taghness": cn35game.GuildEquipThreshold(90),
	} {
		lv := cn35game.GuildEquipLevel(pt)
		if _, err := st.DB().Exec(`INSERT INTO cn35_guild_equipment (guild_id,type_id,level,point,updated_at)
			VALUES (?,?,?,?,'2026-11-14T00:00:00Z')
			ON CONFLICT(guild_id,type_id) DO UPDATE SET level=excluded.level,point=excluded.point`,
			g.ID, typ, lv, pt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB().Exec(`INSERT INTO cn35_guild_members (guild_id,user_id,role,joined_at) VALUES (?,?,1,'2026-11-14T00:00:00Z')`,
		g.ID, uidB); err != nil {
		t.Fatal(err)
	}

	// 1. /act_pages 包含 banner_raid/banner_raid_pt，且尚未遭遇时 act_has_new=false（未装配 Signin）。
	act0 := gameJSON(t, ts.URL, "/act_pages", uidA, skA, s0A, s1A, "", "_method=GET")
	act0Data := act0["data"].(map[string]any)
	if act0Data["act_has_new"] != false {
		t.Fatalf("expected act_has_new=false initially, got %v", act0Data["act_has_new"])
	}
	pagesRaw := act0Data["act_pages"].([]any)
	var hasRaidBanner, hasPtBanner bool
	for _, p := range pagesRaw {
		if p == "banner_raid" {
			hasRaidBanner = true
		}
		if p == "banner_raid_pt" {
			hasPtBanner = true
		}
	}
	if !hasRaidBanner || !hasPtBanner {
		t.Fatalf("act_pages missing raid banners: %v", pagesRaw)
	}

	// 2. 通关 AP>0 日常探险触发暴走强袭弹窗。
	qExec := gameJSON(t, ts.URL, "/quests/execute/1.json", uidA, skA, s0A, s1A,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348&page_message=name%3DQuestSelect", "_method=GET")
	uQAP, _ := url.Parse(qExec["ap_use_url"].(string))
	uQRes, _ := url.Parse(qExec["result_url"].(string))
	_ = gameJSON(t, ts.URL, uQAP.Path, uidA, skA, s0A, s1A, uQAP.RawQuery, "_method=GET")
	qRes := gameJSON(t, ts.URL, uQRes.Path, uidA, skA, s0A, s1A,
		uQRes.RawQuery+"&time=12.5&mc=0", "_method=GET")
	qData := qRes["data"].(map[string]any)
	rm, ok := qData["raid_monster"].(map[string]any)
	if !ok {
		t.Fatalf("/quests/result missing data.raid_monster: %v", qData)
	}
	raidID, _ := rm["id"].(string)
	if raidID == "" || rm["message"] == nil || rm["giveup_confirm"] == nil || rm["next_page_message"] != "name=Raid&base=Common/Pages/Raid&method=Stack" {
		t.Fatalf("unexpected raid_monster appear modal doc: %+v", rm)
	}

	// 遭遇后 /act_pages 的 act_has_new 点亮。
	act1 := gameJSON(t, ts.URL, "/act_pages", uidA, skA, s0A, s1A, "", "_method=GET")
	if act1["data"].(map[string]any)["act_has_new"] != true {
		t.Fatalf("expected act_has_new=true with active raid")
	}

	// 3. /raid_monsters/ 主页读面与 /raid_monsters/show。
	homeDoc := gameJSON(t, ts.URL, "/raid_monsters/", uidA, skA, s0A, s1A, "", "_method=GET")
	homeData := homeDoc["data"].(map[string]any)
	cond := homeData["raid_condition"].(map[string]any)
	if int(cond["has_raid"].(float64)) != 1 || int(cond["lv"].(float64)) != 1 {
		t.Fatalf("raid_condition = %+v", cond)
	}
	showDoc := gameJSON(t, ts.URL, "/raid_monsters/show", uidA, skA, s0A, s1A, "id="+url.QueryEscape(raidID), "_method=GET")
	showData := showDoc["data"].(map[string]any)
	tempMon := showData["temp_raid_monster"].(map[string]any)
	diffs := tempMon["difficulty_options"].(map[string]any)
	if diffs["normal"].(map[string]any)["cost"] != "30" || diffs["very_hard"].(map[string]any)["time_limit"] != "240" {
		t.Fatalf("difficulty_options = %+v", diffs)
	}
	rates := showData["battle_weapon_rate"].(map[string]any)
	if len(rates) != 7 {
		t.Fatalf("battle_weapon_rate len = %d", len(rates))
	}

	// 4. execute.json + bp_use + lose + call_help + damage_list。
	execDoc := gameJSON(t, ts.URL, "/raid_monsters/execute.json", uidA, skA, s0A, s1A,
		"id="+url.QueryEscape(raidID)+"&difficulty_id=normal&party_id=001&mode=raid", "_method=GET")
	if int(execDoc["main_character"].(map[string]any)["hp"].(float64)) != 37000 {
		t.Fatalf("raid main_character.hp = %v, want 37000 (Lv.90)", execDoc["main_character"].(map[string]any)["hp"])
	}
	if int(execDoc["quest"].(map[string]any)["initial_power"].(float64)) != 3500 {
		t.Fatalf("raid Lv.1 initial_power + Lv.100 altar = %v, want 3500", execDoc["quest"].(map[string]any)["initial_power"])
	}
	bossAppear := execDoc["boss_appear_table"].(map[string]any)["1"].([]any)[0].(map[string]any)
	if bossAppear["calc_damage_key"] != "raid" {
		t.Fatalf("calc_damage_key = %v, want raid", bossAppear["calc_damage_key"])
	}
	apURL, _ := execDoc["ap_use_url"].(string)
	parsedAP, _ := url.Parse(apURL)
	rbid := parsedAP.Query().Get("bid")
	_ = gameJSON(t, ts.URL, "/raid_monsters/bp_use", uidA, skA, s0A, s1A, "bid="+url.QueryEscape(rbid), "_method=GET")

	loseDoc := gameJSON(t, ts.URL, "/raid_monsters/lose", uidA, skA, s0A, s1A,
		"bid="+url.QueryEscape(rbid)+"&damage%5Braid%5D=12000&time=50&mc=0", "_method=GET")
	myAfterLose := loseDoc["data"].(map[string]any)["my_raid_monster"].(map[string]any)
	if int64(myAfterLose["point"].(float64)) != 18_000 || myAfterLose["attacked"] != true {
		t.Fatalf("after lose: %+v", myAfterLose)
	}

	helpGuild := gameJSON(t, ts.URL, "/raid_monsters/call_help", uidA, skA, s0A, s1A, "raid_help_type=guild", "_method=GET")
	if helpGuild["success"] != true {
		t.Fatalf("call_help guild = %+v", helpGuild)
	}
	helpComm := gameJSON(t, ts.URL, "/raid_monsters/call_help", uidA, skA, s0A, s1A, "raid_help_type=community_topic", "_method=GET")
	if helpComm["success"] != true {
		t.Fatalf("call_help community = %+v", helpComm)
	}

	// B 看主页可看到公会/聊天室协力，且出击首刀 BP=0。
	homeB := gameJSON(t, ts.URL, "/raid_monsters/", uidB, skB, s0B, s1B, "", "_method=GET")
	if len(homeB["data"].(map[string]any)["guild_help_raid_monsters"].([]any)) == 0 {
		t.Fatalf("B should see guild_help_raid_monsters")
	}
	execB := gameJSON(t, ts.URL, "/raid_monsters/execute.json", uidB, skB, s0B, s1B,
		"id="+url.QueryEscape(raidID)+"&difficulty_id=normal&party_id=001&mode=raid", "_method=GET")
	if int(execB["quest"].(map[string]any)["use_ap"].(float64)) != 0 {
		t.Fatalf("assist first hit normal use_ap = %v, want 0", execB["quest"].(map[string]any)["use_ap"])
	}

	dmgDoc := gameJSON(t, ts.URL, "/raid_monsters/damage_list", uidA, skA, s0A, s1A, "id="+url.QueryEscape(raidID), "_method=GET")
	dmgList := dmgDoc["data"].(map[string]any)["temp_raid_damage_list"].([]any)
	if len(dmgList) < 2 {
		t.Fatalf("expected >=2 damage rows (A + soloAssist NPC), got %v", dmgList)
	}

	// 5. A 直接 /result 击退 Boss -> 验证 lv_bonus 与 pt_bonus/_0,_1 与 exchange。
	execWin := gameJSON(t, ts.URL, "/raid_monsters/execute.json", uidA, skA, s0A, s1A,
		"id="+url.QueryEscape(raidID)+"&difficulty_id=very_hard&party_id=001&mode=raid", "_method=GET")
	uWin, _ := url.Parse(execWin["result_url"].(string))
	rbidWin := uWin.Query().Get("bid")
	_ = gameJSON(t, ts.URL, "/raid_monsters/bp_use", uidA, skA, s0A, s1A, "bid="+url.QueryEscape(rbidWin), "_method=GET")
	resDoc := gameJSON(t, ts.URL, "/raid_monsters/result", uidA, skA, s0A, s1A, "bid="+url.QueryEscape(rbidWin), "_method=GET")
	if resDoc["next_page_message"] != "name=Raid&base=Common/Pages/Raid" {
		t.Fatalf("result next_page_message = %v", resDoc["next_page_message"])
	}
	if int(resDoc["data"].(map[string]any)["raid_condition"].(map[string]any)["lv"].(float64)) != 2 {
		t.Fatalf("raid lv after win = %v, want 2", resDoc["data"].(map[string]any)["raid_condition"])
	}

	lvBonus := gameJSON(t, ts.URL, "/raid_monsters/lv_bonus", uidA, skA, s0A, s1A, "", "_method=GET")
	lvRows := lvBonus["data"].(map[string]any)["raid_lv_bonus"].([]any)
	if lvRows[0].(map[string]any)["overlay_asset_name"] != "common/chara_clear" {
		t.Fatalf("Lv.1 bonus should show chara_clear: %+v", lvRows[0])
	}

	if _, err := st.DB().Exec(`UPDATE cn35_raid_profiles SET raid_pt=1000 WHERE user_id=?`, uidA); err != nil {
		t.Fatal(err)
	}
	ptBonus := gameJSON(t, ts.URL, "/raid_monsters/pt_bonus", uidA, skA, s0A, s1A, "", "_method=GET")
	ptRows := ptBonus["data"].(map[string]any)["raid_pt_bonus"].([]any)
	if len(ptRows) != 4 {
		t.Fatalf("pt_bonus row count = %d, want 4", len(ptRows))
	}
	row0 := ptRows[0].(map[string]any)
	if row0["icon_asset_name_0"] == nil || row0["exchange_url_1"] == nil || !strings.Contains(row0["exchange_url_1"].(string), "slot_id=slot_2") {
		t.Fatalf("pt_bonus _0/_1 keys malformed: %+v", row0)
	}
	exDoc := gameJSON(t, ts.URL, "/raid_monsters/exchange", uidA, skA, s0A, s1A, "slot_id=slot_2&count=2", "_method=GET")
	if exDoc["status"] != "success" || int(exDoc["data"].(map[string]any)["raid_condition"].(map[string]any)["pt"].(float64)) != 800 {
		t.Fatalf("exchange = %+v", exDoc)
	}

	// 6. 新遭遇 -> giveup 未确认弹窗 & confirmed=true 放走。
	qExec2 := gameJSON(t, ts.URL, "/quests/execute/1.json", uidA, skA, s0A, s1A,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348&page_message=name%3DQuestSelect", "_method=GET")
	uQ2AP, _ := url.Parse(qExec2["ap_use_url"].(string))
	uQ2Res, _ := url.Parse(qExec2["result_url"].(string))
	_ = gameJSON(t, ts.URL, uQ2AP.Path, uidA, skA, s0A, s1A, uQ2AP.RawQuery, "_method=GET")
	qRes2 := gameJSON(t, ts.URL, uQ2Res.Path, uidA, skA, s0A, s1A, uQ2Res.RawQuery+"&time=8&mc=0", "_method=GET")
	raidID2 := qRes2["data"].(map[string]any)["raid_monster"].(map[string]any)["id"].(string)

	gAsk := gameJSON(t, ts.URL, "/raid_monsters/giveup", uidA, skA, s0A, s1A, "id="+url.QueryEscape(raidID2), "_method=GET")
	if gAsk["modal"].(map[string]any)["type"] != "yes_no" {
		t.Fatalf("unconfirmed giveup should return yes_no modal: %+v", gAsk)
	}
	gDo := gameJSON(t, ts.URL, "/raid_monsters/giveup", uidA, skA, s0A, s1A, "confirmed=true&id="+url.QueryEscape(raidID2), "_method=GET")
	if gDo["status"] != "success" || int(gDo["data"].(map[string]any)["raid_condition"].(map[string]any)["has_raid"].(float64)) != 0 {
		t.Fatalf("confirmed giveup = %+v", gDo)
	}

	// 7. 实机 Lv.233 莫鲁菲涅曲线与单场 20 亿血量安全封顶校验。
	max233 := cn35game.RaidMaxPoint(233)
	pow233 := cn35game.RaidInitialPower(233)
	if _, err := st.DB().Exec(`INSERT INTO cn35_raid_instances
		(raid_id,owner_user_id,guild_id,monster_id,monster_name,attr_id,level,max_point,current_point,initial_power,attacked,help_guild,help_community,status,spawned_at,expired_at,finished_at)
		VALUES ('raid_lv233',?,?,688,'莫鲁菲涅',4,233,?,?,?,0,0,0,'active',?,?,0)`,
		uidA, g.ID, max233, max233, pow233, now.Unix(), now.Unix()+5400); err != nil {
		t.Fatal(err)
	}
	show233 := gameJSON(t, ts.URL, "/raid_monsters/show", uidA, skA, s0A, s1A, "id=raid_lv233", "_method=GET")
	tm233 := show233["data"].(map[string]any)["temp_raid_monster"].(map[string]any)
	if int64(math.Round(tm233["point"].(float64))) != 10_059_161_640 || int(tm233["initial_power"].(float64)) != 312_500 {
		t.Fatalf("Lv.233 anchors mismatch: point=%v initial_power=%v", tm233["point"], tm233["initial_power"])
	}
	exec233 := gameJSON(t, ts.URL, "/raid_monsters/execute.json", uidA, skA, s0A, s1A,
		"id=raid_lv233&difficulty_id=very_hard&party_id=001&mode=raid", "_method=GET")
	if int64(exec233["current_hp"].(float64)) != cn35game.RaidSingleBattleHPCap {
		t.Fatalf("Lv.233 battle current_hp = %v, want %d", exec233["current_hp"], cn35game.RaidSingleBattleHPCap)
	}
	boss233 := exec233["boss_appear_table"].(map[string]any)["1"].([]any)[0].(map[string]any)["params"].(map[string]any)
	if int64(boss233["hp"].(float64)) != cn35game.RaidSingleBattleHPCap {
		t.Fatalf("Lv.233 boss params.hp = %v, want %d", boss233["hp"], cn35game.RaidSingleBattleHPCap)
	}
	_ = sql.ErrNoRows
}

func TestRaidWireReviewRegression(t *testing.T) {
	now := time.Date(2026, 11, 14, 16, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	ts, st := raidTestServer(t, now, cn35game.RaidConfig{
		BossMonsterID: 688,
		TriggerRate:   1.0,
		TTLSeconds:    5400,
		SoloAssist:    true,
	}, 0.0)

	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "raid-rev-a")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "raid-rev-b")
	uidC, skC, s0C, s1C := establishSession(t, ts.URL, "raid-rev-c")
	uidD, skD, s0D, s1D := establishSession(t, ts.URL, "raid-rev-d")
	for _, u := range []string{uidA, uidB, uidC, uidD} {
		seedBattleUnits(t, st, u)
		if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET ap=500, max_ap=500, bp=2000, max_bp=2000 WHERE user_id=?`, u); err != nil {
			t.Fatal(err)
		}
	}
	gA, err := st.CreateGuild(uidA, "评审甲会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO cn35_guild_members (guild_id,user_id,role,joined_at) VALUES (?,?,1,'2026-11-14T00:00:00Z')`,
		gA.ID, uidB); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateGuild(uidC, "评审乙会", "2", "1", "", ""); err != nil {
		t.Fatal(err)
	}

	// 1. /act_pages 包含 banner_raid / banner_raid_info / banner_raid_pt。
	actDoc := gameJSON(t, ts.URL, "/act_pages", uidA, skA, s0A, s1A, "", "_method=GET")
	pagesRaw := actDoc["data"].(map[string]any)["act_pages"].([]any)
	seenBanners := map[string]bool{}
	for _, p := range pagesRaw {
		if s, ok := p.(string); ok {
			seenBanners[s] = true
		}
	}
	for _, want := range []string{"banner_raid", "banner_raid_info", "banner_raid_pt"} {
		if !seenBanners[want] {
			t.Fatalf("act_pages missing %q: %v", want, pagesRaw)
		}
	}

	// 2. A 通关触发暴走，检验 /raid_monsters/ 中的 raid_attack_message / member_option / icon_name。
	qExec := gameJSON(t, ts.URL, "/quests/execute/1.json", uidA, skA, s0A, s1A,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348&page_message=name%3DQuestSelect", "_method=GET")
	uQAP, _ := url.Parse(qExec["ap_use_url"].(string))
	uQRes, _ := url.Parse(qExec["result_url"].(string))
	_ = gameJSON(t, ts.URL, uQAP.Path, uidA, skA, s0A, s1A, uQAP.RawQuery, "_method=GET")
	qRes := gameJSON(t, ts.URL, uQRes.Path, uidA, skA, s0A, s1A, uQRes.RawQuery+"&time=12.5&mc=0", "_method=GET")
	raidID := qRes["data"].(map[string]any)["raid_monster"].(map[string]any)["id"].(string)

	homeA := gameJSON(t, ts.URL, "/raid_monsters/", uidA, skA, s0A, s1A, "", "_method=GET")
	homeAData := homeA["data"].(map[string]any)
	condA := homeAData["raid_condition"].(map[string]any)
	myRaid := homeAData["my_raid_monster"].(map[string]any)
	wantPathEsc := url.QueryEscape("/raid_monsters/show?id=" + url.QueryEscape(raidID))
	for _, msg := range []string{condA["raid_attack_message"].(string), myRaid["attack_message"].(string)} {
		if !strings.Contains(msg, "additional_require_data_path="+wantPathEsc) {
			t.Fatalf("attack_message missing additional_require_data_path=%q: %q", wantPathEsc, msg)
		}
	}
	if myRaid["icon_name"] != "monster/monster_square_688" {
		t.Fatalf("my_raid_monster.icon_name = %v, want monster/monster_square_688", myRaid["icon_name"])
	}
	memOpt, ok := myRaid["member_option"].(map[string]any)
	if !ok {
		t.Fatalf("member_option must be JSON object, got %T (%v)", myRaid["member_option"], myRaid["member_option"])
	}
	reqData := memOpt["requireData"].(map[string]any)
	dmgReq := reqData["temp_raid_damage_list"].(map[string]any)
	if dmgReq["path"] != "/raid_monsters/damage_list?id="+url.QueryEscape(raidID) || dmgReq["force_update"] != true {
		t.Fatalf("unexpected member_option.requireData.temp_raid_damage_list: %+v", dmgReq)
	}

	// 未求援前，异会 C（或同会 B）查 damage_list 返回空列表。
	unauthDmg := gameJSON(t, ts.URL, "/raid_monsters/damage_list", uidC, skC, s0C, s1C, "id="+url.QueryEscape(raidID), "_method=GET")
	if got := len(unauthDmg["data"].(map[string]any)["temp_raid_damage_list"].([]any)); got != 0 {
		t.Fatalf("unauthorized damage_list should be empty, got len=%d", got)
	}

	// 3. A 自击后发公会求援（不发聊天室），检验 call_help.data.messages 与 /users/messages 公会隔离及三态 attacked。
	execA1 := gameJSON(t, ts.URL, "/raid_monsters/execute.json", uidA, skA, s0A, s1A,
		"id="+url.QueryEscape(raidID)+"&difficulty_id=normal&party_id=001&mode=raid", "_method=GET")
	uLoseA, _ := url.Parse(execA1["lose_url"].(string))
	bidA1 := uLoseA.Query().Get("bid")
	_ = gameJSON(t, ts.URL, "/raid_monsters/bp_use", uidA, skA, s0A, s1A, "bid="+url.QueryEscape(bidA1), "_method=GET")
	_ = gameJSON(t, ts.URL, "/raid_monsters/lose", uidA, skA, s0A, s1A,
		"bid="+url.QueryEscape(bidA1)+"&damage%5Braid%5D=5000&time=30&mc=0", "_method=GET")

	callGuild := gameJSON(t, ts.URL, "/raid_monsters/call_help", uidA, skA, s0A, s1A, "raid_help_type=guild", "_method=GET")
	callMsgs := callGuild["data"].(map[string]any)["messages"].(map[string]any)
	hasRaidIDInCat := func(msgs map[string]any, cat string) bool {
		arr, _ := msgs[cat].([]any)
		for _, it := range arr {
			if m, ok := it.(map[string]any); ok && m["raid_id"] == raidID {
				return true
			}
		}
		return false
	}
	if !hasRaidIDInCat(callMsgs, "guild_comment") {
		t.Fatalf("call_help.data.messages.guild_comment missing raid_id %s: %+v", raidID, callMsgs)
	}

	// 同会 B 调 /users/messages 可见 guild_comment；异会 C 不可见他会 guild_comment。
	umB1 := gameJSON(t, ts.URL, "/users/messages", uidB, skB, s0B, s1B, "", "_method=GET")
	if !hasRaidIDInCat(umB1["data"].(map[string]any)["messages"].(map[string]any), "guild_comment") {
		t.Fatalf("guildmate B /users/messages missing guild_comment for %s", raidID)
	}
	umC1 := gameJSON(t, ts.URL, "/users/messages", uidC, skC, s0C, s1C, "", "_method=GET")
	if hasRaidIDInCat(umC1["data"].(map[string]any)["messages"].(map[string]any), "guild_comment") {
		t.Fatalf("outsider C /users/messages must NOT see A guild_comment for %s", raidID)
	}

	// 发送聊天室求援（soloAssist=true 会注入 NPC 协力行），检验 C /users/messages 含 community_comment & community_raid_call。
	_ = gameJSON(t, ts.URL, "/raid_monsters/call_help", uidA, skA, s0A, s1A, "raid_help_type=community_topic", "_method=GET")
	umC2 := gameJSON(t, ts.URL, "/users/messages", uidC, skC, s0C, s1C, "", "_method=GET")
	umC2Msgs := umC2["data"].(map[string]any)["messages"].(map[string]any)
	if !hasRaidIDInCat(umC2Msgs, "community_comment") || !hasRaidIDInCat(umC2Msgs, "community_raid_call") {
		t.Fatalf("outsider C /users/messages missing community raid help after community_topic: %+v", umC2Msgs)
	}

	// B 出击前协力行 attacked==false；出击结算后协力行 attacked==true；击退后历史行 attacked==true。
	homeB0 := gameJSON(t, ts.URL, "/raid_monsters/", uidB, skB, s0B, s1B, "", "_method=GET")
	helpsB0 := homeB0["data"].(map[string]any)["guild_help_raid_monsters"].([]any)
	if len(helpsB0) != 1 || helpsB0[0].(map[string]any)["attacked"] != false {
		t.Fatalf("B assist before attack should have attacked=false: %+v", helpsB0)
	}
	execB := gameJSON(t, ts.URL, "/raid_monsters/execute.json", uidB, skB, s0B, s1B,
		"id="+url.QueryEscape(raidID)+"&difficulty_id=normal&party_id=001&mode=raid", "_method=GET")
	uLoseB, _ := url.Parse(execB["lose_url"].(string))
	bidB := uLoseB.Query().Get("bid")
	_ = gameJSON(t, ts.URL, "/raid_monsters/bp_use", uidB, skB, s0B, s1B, "bid="+url.QueryEscape(bidB), "_method=GET")
	_ = gameJSON(t, ts.URL, "/raid_monsters/lose", uidB, skB, s0B, s1B,
		"bid="+url.QueryEscape(bidB)+"&damage%5Braid%5D=3000&time=25&mc=0", "_method=GET")

	homeB1 := gameJSON(t, ts.URL, "/raid_monsters/", uidB, skB, s0B, s1B, "", "_method=GET")
	helpsB1 := homeB1["data"].(map[string]any)["guild_help_raid_monsters"].([]any)
	if len(helpsB1) != 1 || helpsB1[0].(map[string]any)["attacked"] != true {
		t.Fatalf("B assist after attack should have attacked=true: %+v", helpsB1)
	}

	// 4. damage_list 真人行 user_detail_message 跳 UserPage，NPC 行留空。
	dmgDoc := gameJSON(t, ts.URL, "/raid_monsters/damage_list", uidA, skA, s0A, s1A, "id="+url.QueryEscape(raidID), "_method=GET")
	dmgList := dmgDoc["data"].(map[string]any)["temp_raid_damage_list"].([]any)
	var sawHumanUserPage, sawEmptyNPC bool
	for _, item := range dmgList {
		row := item.(map[string]any)
		dm, _ := row["user_detail_message"].(string)
		if strings.HasPrefix(dm, "name=UserPage&base=Common/Pages/UserPage") {
			sawHumanUserPage = true
		} else if dm == "" {
			sawEmptyNPC = true
		} else {
			t.Fatalf("unexpected user_detail_message %q in row %+v", dm, row)
		}
	}
	if !sawHumanUserPage || !sawEmptyNPC {
		t.Fatalf("damage_list must contain both human UserPage and empty NPC row: sawHuman=%v sawNPC=%v rows=%+v", sawHumanUserPage, sawEmptyNPC, dmgList)
	}

	// A 收尾击退后，A 的 my_history_raid_monsters 历史行 attacked==true。
	execWin := gameJSON(t, ts.URL, "/raid_monsters/execute.json", uidA, skA, s0A, s1A,
		"id="+url.QueryEscape(raidID)+"&difficulty_id=very_hard&party_id=001&mode=raid", "_method=GET")
	uResWin, _ := url.Parse(execWin["result_url"].(string))
	bidWin := uResWin.Query().Get("bid")
	_ = gameJSON(t, ts.URL, "/raid_monsters/bp_use", uidA, skA, s0A, s1A, "bid="+url.QueryEscape(bidWin), "_method=GET")
	winDoc := gameJSON(t, ts.URL, "/raid_monsters/result", uidA, skA, s0A, s1A, "bid="+url.QueryEscape(bidWin), "_method=GET")
	logs := winDoc["data"].(map[string]any)["my_history_raid_monsters"].([]any)
	if len(logs) == 0 || logs[0].(map[string]any)["attacked"] != true {
		t.Fatalf("history log after defeat should have attacked=true: %+v", logs)
	}

	// 5. /raid_monsters/pt_bonus 双槽键完整（4 行 × 2 槽）。
	ptBonusDoc := gameJSON(t, ts.URL, "/raid_monsters/pt_bonus", uidA, skA, s0A, s1A, "", "_method=GET")
	ptRows := ptBonusDoc["data"].(map[string]any)["raid_pt_bonus"].([]any)
	if len(ptRows) != 4 {
		t.Fatalf("pt_bonus rows = %d, want 4", len(ptRows))
	}
	slotPrefixes := []string{
		"icon_asset_name", "name", "description", "pt_str",
		"button_label", "exchange_button_disable", "rest_exchange_text",
		"count", "price", "confirm_text", "exchange_url",
	}
	for idx, rawRow := range ptRows {
		row := rawRow.(map[string]any)
		for _, sfx := range []string{"_0", "_1"} {
			for _, pfx := range slotPrefixes {
				if _, ok := row[pfx+sfx]; !ok {
					t.Fatalf("pt_bonus row %d missing key %q", idx, pfx+sfx)
				}
			}
		}
	}

	// 6. Lv.233 Boss（10,059,161,640 HP）通过 HTTP 连续调用 /result 6 次才真正击退并晋升。
	max233 := cn35game.RaidMaxPoint(233)
	if _, err := st.DB().Exec(`INSERT INTO cn35_raid_instances
		(raid_id,owner_user_id,guild_id,monster_id,monster_name,attr_id,level,max_point,current_point,initial_power,attacked,help_guild,help_community,status,spawned_at,expired_at,finished_at)
		VALUES ('raid_wire_lv233',?,'',688,'莫鲁菲涅',4,233,?,?,?,0,0,0,'active',?,?,0)`,
		uidD, max233, max233, cn35game.RaidInitialPower(233), now.Unix(), now.Unix()+5400); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 6; round++ {
		execR := gameJSON(t, ts.URL, "/raid_monsters/execute.json", uidD, skD, s0D, s1D,
			"id=raid_wire_lv233&difficulty_id=very_hard&party_id=001&mode=raid", "_method=GET")
		uR, _ := url.Parse(execR["result_url"].(string))
		bidR := uR.Query().Get("bid")
		_ = gameJSON(t, ts.URL, "/raid_monsters/bp_use", uidD, skD, s0D, s1D, "bid="+url.QueryEscape(bidR), "_method=GET")
		resR := gameJSON(t, ts.URL, "/raid_monsters/result", uidD, skD, s0D, s1D, "bid="+url.QueryEscape(bidR), "_method=GET")
		d := resR["data"].(map[string]any)
		cond := d["raid_condition"].(map[string]any)
		if round < 6 {
			wantRemain := max233 - int64(round)*cn35game.RaidSingleBattleHPCap
			if int(cond["has_raid"].(float64)) != 1 || int(cond["lv"].(float64)) != 1 {
				t.Fatalf("round %d (<6) should stay active Lv.1: cond=%+v", round, cond)
			}
			myR := d["my_raid_monster"].(map[string]any)
			if int64(math.Round(myR["point"].(float64))) != wantRemain {
				t.Fatalf("round %d remain = %v, want %d", round, myR["point"], wantRemain)
			}
		} else {
			if int(cond["has_raid"].(float64)) != 0 || int(cond["lv"].(float64)) != 234 {
				t.Fatalf("round 6 should defeat Lv.233 boss and promote lv=234: cond=%+v", cond)
			}
			if int64(cond["pt"].(float64)) <= 0 {
				t.Fatalf("round 6 defeat should grant raid pt, cond=%+v", cond)
			}
			_ = fmt.Sprintf("verified 6-round Lv.233 defeat")
		}
	}
}
