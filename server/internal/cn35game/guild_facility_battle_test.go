package cn35game_test

import (
	"database/sql"
	"encoding/json"
	"math"
	mathrand "math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func facilityTestServer(t *testing.T, now time.Time) (*httptest.Server, *cn35store.Store, *cardcatalog.Catalog) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(t.TempDir(), "facility.sqlite3"))
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
		Skills:     st,
		Guild:      st,
		Catalog:    cat,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
		BattleRand: mathrand.New(mathrand.NewSource(43)),
		GuildClock: func() time.Time { return now },
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, cat
}

func seedGuildEquipPoints(t *testing.T, st *cn35store.Store, guildID string, pts map[string]int64) {
	t.Helper()
	for typ, pt := range pts {
		lv := cn35game.GuildEquipLevel(pt)
		if _, err := st.DB().Exec(`INSERT INTO cn35_guild_equipment (guild_id,type_id,level,point,updated_at)
			VALUES (?,?,?,?,'2026-11-14T00:00:00Z')
			ON CONFLICT(guild_id,type_id) DO UPDATE SET level=excluded.level,point=excluded.point`,
			guildID, typ, lv, pt); err != nil {
			t.Fatalf("seed equip %s: %v", typ, err)
		}
	}
}

func TestApplyUnitFacilityBuffExactMLKSample(t *testing.T) {
	// mlk default/quests_execute_104.json.2.json 3 号位实测：
	// 未加符文基础攻击 10260，符文后 16823，训练所 Lv.84（+21%）→ 16823 + round(10260*0.21) = 18978。
	doc := map[string]any{"hp": 5000, "at": 16823}
	cn35game.ApplyUnitFacilityBuff(doc, 5000, 10260, 0, 84)
	if doc["at"] != 18978 {
		t.Fatalf("at = %v, want 18978", doc["at"])
	}
	if doc["hp"] != 5000 {
		t.Fatalf("hp = %v, want 5000", doc["hp"])
	}
}

func TestDailyQuestAndGuildBattleFacilityBuffs(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 45, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	ts, st, _ := facilityTestServer(t, now)

	// 1. 无公会玩家：基线面板，无 guild_asset_help_level_base。
	uidSolo, skSolo, s0Solo, s1Solo := establishSession(t, ts.URL, "fac-solo")
	seedBattleUnits(t, st, uidSolo)
	soloExec := gameJSON(t, ts.URL, "/quests/execute/1.json", uidSolo, skSolo, s0Solo, s1Solo,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348&page_message=name%3DQuestSelect", "_method=GET")
	baseInitPower := int(soloExec["quest"].(map[string]any)["initial_power"].(float64))
	soloMainHP := int(soloExec["main_character"].(map[string]any)["hp"].(float64))
	if soloMainHP != 1000 {
		t.Fatalf("solo main_character.hp = %d, want 1000", soloMainHP)
	}
	if _, hasHelp := soloExec["guild_asset_help_level_base"]; hasHelp {
		t.Fatalf("solo should not emit guild_asset_help_level_base: %v", soloExec["guild_asset_help_level_base"])
	}
	soloU0 := soloExec["units"].([]any)[0].(map[string]any)
	baseHP := int(soloU0["hp"].(float64))
	baseAT := int(soloU0["at"].(float64))

	// 2. mlk 锚点公会玩家（sp Lv.88 / hp Lv.79 / at Lv.84 / taghness Lv.70）+ 装备 30 级体力/攻击符文（+30%）。
	uidGuild, skG, s0G, s1G := establishSession(t, ts.URL, "fac-mlk")
	seedBattleUnits(t, st, uidGuild)
	targetUID := int(soloU0["id"].(float64))
	for i, skillID := range []int{1, 2} {
		if _, err := st.DB().Exec(`INSERT INTO cn35_player_skills
			(user_id,uuid,skill_id,level,base_level,protected,unit_id,created_at,updated_at)
			VALUES (?,?,?,30.0,30.0,0,?,'2026-11-14T00:00:00Z','2026-11-14T00:00:00Z')`,
			uidGuild, "fac-sk-"+string(rune('a'+i)), skillID, targetUID); err != nil {
			t.Fatalf("insert rune: %v", err)
		}
	}
	g, err := st.CreateGuild(uidGuild, "梅露可设施测试会", "2", "1", "", "")
	if err != nil {
		t.Fatalf("CreateGuild: %v", err)
	}
	seedGuildEquipPoints(t, st, g.ID, map[string]int64{
		"sp":       cn35game.GuildEquipThreshold(88),
		"hp":       cn35game.GuildEquipThreshold(79),
		"at":       cn35game.GuildEquipThreshold(84),
		"taghness": cn35game.GuildEquipThreshold(70),
	})

	guildExec := gameJSON(t, ts.URL, "/quests/execute/1.json", uidGuild, skG, s0G, s1G,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348&page_message=name%3DQuestSelect", "_method=GET")
	if got := int(guildExec["quest"].(map[string]any)["initial_power"].(float64)); got != baseInitPower+2200 {
		t.Fatalf("guild quest initial_power = %d, want %d", got, baseInitPower+2200)
	}
	if got := int(guildExec["main_character"].(map[string]any)["hp"].(float64)); got != 29000 {
		t.Fatalf("guild quest main_character.hp = %d, want 29000", got)
	}
	if got := int(guildExec["guild_asset_help_level_base"].(float64)); got != 3 {
		t.Fatalf("guild_asset_help_level_base = %d, want 3", got)
	}
	guildU0 := guildExec["units"].([]any)[0].(map[string]any)
	wantHP := int(math.Round(float64(baseHP)*1.3)) + int(math.Round(float64(baseHP)*0.1975))
	wantAT := int(math.Round(float64(baseAT)*1.3)) + int(math.Round(float64(baseAT)*0.21))
	if gotHP := int(guildU0["hp"].(float64)); gotHP != wantHP {
		t.Fatalf("guild unit hp = %d, want %d (base=%d)", gotHP, wantHP, baseHP)
	}
	if gotAT := int(guildU0["at"].(float64)); gotAT != wantAT {
		t.Fatalf("guild unit at = %d, want %d (base=%d)", gotAT, wantAT, baseAT)
	}

	// 3. 截图 Lv.100/101/90 设施同步生效于日常探险与公会战（Version=2）。
	seedGuildEquipPoints(t, st, g.ID, map[string]int64{
		"sp":       cn35game.GuildEquipThreshold(100),
		"hp":       cn35game.GuildEquipThreshold(100),
		"at":       cn35game.GuildEquipThreshold(101),
		"taghness": cn35game.GuildEquipThreshold(90),
	})
	snapExec := gameJSON(t, ts.URL, "/quests/execute/1.json", uidGuild, skG, s0G, s1G,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348&page_message=name%3DQuestSelect", "_method=GET")
	if got := int(snapExec["quest"].(map[string]any)["initial_power"].(float64)); got != baseInitPower+2500 {
		t.Fatalf("Lv.100 initial_power = %d, want %d", got, baseInitPower+2500)
	}
	if got := int(snapExec["main_character"].(map[string]any)["hp"].(float64)); got != 37000 {
		t.Fatalf("Lv.90 main_character.hp = %d, want 37000", got)
	}
	if got := int(snapExec["guild_asset_help_level_base"].(float64)); got != 4 {
		t.Fatalf("Lv.90 guild_asset_help_level_base = %d, want 4", got)
	}

	// 公会战出击（/battles/guild_quest_execute.json）同样吃到食堂 HP、helpBase=4 与基值设施加成。
	if err = st.WithTx(func(tx *sql.Tx) error {
		return cn35store.InsertMonsterTx(tx, uidGuild,
			cn35game.MonsterRecord{UUID: "fac-def-1", MonsterID: 159, Level: 1})
	}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetDefense(uidGuild, 0, "fac-def-1", ""); err != nil {
		t.Fatal(err)
	}
	gwView, err := st.GWState(uidGuild, now)
	if err != nil {
		t.Fatal(err)
	}
	targetG := gwView.Participants[1].GuildID
	gwExec := gameJSON(t, ts.URL, "/battles/guild_quest_execute.json", uidGuild, skG, s0G, s1G,
		"mode=battle&guild_id="+targetG+"&difficulty_id=normal&party_id=001", "_method=GET")
	if got := int(gwExec["main_character"].(map[string]any)["hp"].(float64)); got != 37000 {
		t.Fatalf("GW Lv.90 main_character.hp = %d, want 37000", got)
	}
	if got := int(gwExec["guild_asset_help_level_base"].(float64)); got != 4 {
		t.Fatalf("GW Lv.90 guild_asset_help_level_base = %d, want 4", got)
	}
	gwU0 := gwExec["units"].([]any)[0].(map[string]any)
	wantGWHP := int(math.Round(float64(baseHP)*1.3)) + int(math.Round(float64(baseHP)*0.25))
	wantGWAT := int(math.Round(float64(baseAT)*1.3)) + int(math.Round(float64(baseAT)*0.2525))
	if gotHP := int(gwU0["hp"].(float64)); gotHP != wantGWHP {
		t.Fatalf("GW Lv.100 unit hp = %d, want %d", gotHP, wantGWHP)
	}
	if gotAT := int(gwU0["at"].(float64)); gotAT != wantGWAT {
		t.Fatalf("GW Lv.101 unit at = %d, want %d", gotAT, wantGWAT)
	}
}

func TestQuestExecuteGuildFacilityReplayContract(t *testing.T) {
	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	ts, st, _ := facilityTestServer(t, now)

	// 1. 无公会基线，采集基础 initial_power、主人公 HP 与同伴未加算面板。
	uidSolo, skSolo, s0Solo, s1Solo := establishSession(t, ts.URL, "fac-replay-solo")
	seedBattleUnits(t, st, uidSolo)
	soloExec := gameJSON(t, ts.URL, "/quests/execute/1.json", uidSolo, skSolo, s0Solo, s1Solo,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348&page_message=name%3DQuestSelect", "_method=GET")
	baseInitPower := int(soloExec["quest"].(map[string]any)["initial_power"].(float64))
	soloMainHP := int(soloExec["main_character"].(map[string]any)["hp"].(float64))
	if soloMainHP != 1000 {
		t.Fatalf("baseline solo main_character.hp = %d, want 1000", soloMainHP)
	}
	soloU0 := soloExec["units"].([]any)[0].(map[string]any)
	baseHP := int(soloU0["hp"].(float64))
	baseAT := int(soloU0["at"].(float64))

	// 2. Lv.88 祭坛 / Lv.79 医疗所 / Lv.84 训练所 / Lv.70 食堂公会出击契约。
	uidG, skG, s0G, s1G := establishSession(t, ts.URL, "fac-replay-guild")
	seedBattleUnits(t, st, uidG)
	g, err := st.CreateGuild(uidG, "设施回放公会", "2", "1", "", "")
	if err != nil {
		t.Fatalf("CreateGuild: %v", err)
	}
	seedGuildEquipPoints(t, st, g.ID, map[string]int64{
		"sp":       cn35game.GuildEquipThreshold(88),
		"hp":       cn35game.GuildEquipThreshold(79),
		"at":       cn35game.GuildEquipThreshold(84),
		"taghness": cn35game.GuildEquipThreshold(70),
	})

	execDoc := gameJSON(t, ts.URL, "/quests/execute/1.json", uidG, skG, s0G, s1G,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348&page_message=name%3DQuestSelect", "_method=GET")
	questObj := execDoc["quest"].(map[string]any)
	if got := int(questObj["initial_power"].(float64)); got != baseInitPower+2200 {
		t.Fatalf("replay quest.initial_power = %d, want %d (+2200)", got, baseInitPower+2200)
	}
	mcObj := execDoc["main_character"].(map[string]any)
	if got := int(mcObj["hp"].(float64)); got != 29000 {
		t.Fatalf("replay main_character.hp = %d, want 29000", got)
	}
	if got := int(execDoc["guild_asset_help_level_base"].(float64)); got != 3 {
		t.Fatalf("replay guild_asset_help_level_base = %d, want 3", got)
	}
	// 全局目录模板不能被原地污染（再次用无公会玩家请求应仍为 soloMainHP）。
	if afterSolo := gameJSON(t, ts.URL, "/quests/execute/1.json", uidSolo, skSolo, s0Solo, s1Solo,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348&page_message=name%3DQuestSelect", "_method=GET"); int(afterSolo["main_character"].(map[string]any)["hp"].(float64)) != soloMainHP {
		t.Fatalf("solo main_character.hp leaked guild buff: got %v, want %d", afterSolo["main_character"].(map[string]any)["hp"], soloMainHP)
	}
	// 无符文基值百分比加算：+19.75% HP / +21% AT。
	u0 := execDoc["units"].([]any)[0].(map[string]any)
	wantHP := baseHP + int(math.Round(float64(baseHP)*0.1975))
	wantAT := baseAT + int(math.Round(float64(baseAT)*0.21))
	if gotHP := int(u0["hp"].(float64)); gotHP != wantHP {
		t.Fatalf("replay unit hp = %d, want %d (base=%d)", gotHP, wantHP, baseHP)
	}
	if gotAT := int(u0["at"].(float64)); gotAT != wantAT {
		t.Fatalf("replay unit at = %d, want %d (base=%d)", gotAT, wantAT, baseAT)
	}

	// 3. 若通过环境变量 MLK_CAPTURE_DIR 指定了本地只读 mlk 抓包目录，比对顶层与 quest/main_character 键集同构性及样本数值锚点。
	mlkRoot := os.Getenv("MLK_CAPTURE_DIR")
	if mlkRoot == "" {
		t.Log("skip external mlk capture comparison: MLK_CAPTURE_DIR not set")
		return
	}
	capturePath := filepath.Join(mlkRoot, "default", "quests_execute_104.json.2.json")
	rawCap, err := os.ReadFile(capturePath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Logf("skip external mlk capture comparison (not present): %s", capturePath)
			return
		}
		t.Fatalf("read mlk capture %s: %v", capturePath, err)
	}
	var capDoc map[string]any
	if err := json.Unmarshal(rawCap, &capDoc); err != nil {
		t.Fatalf("unmarshal mlk capture: %v", err)
	}
	if res, ok := capDoc["result"].(map[string]any); ok {
		capDoc = res
	}
	for _, k := range []string{"quest", "main_character", "units", "guild_asset_help_level_base"} {
		if _, exists := capDoc[k]; !exists {
			t.Fatalf("capture missing expected top-level key %q", k)
		}
		if _, exists := execDoc[k]; !exists {
			t.Fatalf("execDoc missing top-level key %q present in capture", k)
		}
	}
	if capQ, ok := capDoc["quest"].(map[string]any); ok {
		for k := range capQ {
			if _, exists := questObj[k]; !exists {
				t.Fatalf("execDoc.quest missing key %q present in mlk capture", k)
			}
		}
	}
	if capMC, ok := capDoc["main_character"].(map[string]any); ok {
		for k := range capMC {
			if _, exists := mcObj[k]; !exists {
				t.Fatalf("execDoc.main_character missing key %q present in mlk capture", k)
			}
		}
	}
}
