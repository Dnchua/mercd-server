package cn35game_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// TestExecuteFeverTimeWire 夜晚（Fever Time）开局：timezone_table 全 3、
// rush_started=true、声明 background<id>_3，金币/资材 ×2 且展示与入账一致。
func TestExecuteFeverTimeWire(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServerTZ(t, dir, 0, cn35game.BattleTimezoneConfig{NightRate: 1})
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-b-fever")
	seedBattleUnits(t, st, uid)

	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348", "_method=GET")
	tzTable := doc["timezone_table"].(map[string]any)
	appear := doc["appear_table"].(map[string]any)
	if len(tzTable) != len(appear) {
		t.Fatalf("timezone_table 波次应与 appear_table 一致: %v", tzTable)
	}
	for w, v := range tzTable {
		if v.(float64) != 3 {
			t.Fatalf("wave %s 时段应为夜(3): %v", w, v)
		}
	}
	if doc["rush_started"] != true || doc["rush_ended"] != false {
		t.Fatalf("rush_started/rush_ended: %v/%v", doc["rush_started"], doc["rush_ended"])
	}
	var bgs []string
	for _, a := range doc["asset_names"].([]any) {
		if s := a.(string); strings.HasPrefix(s, "background/") {
			bgs = append(bgs, s)
		}
	}
	bgID := int(doc["quest"].(map[string]any)["background_id"].(float64))
	if len(bgs) != 1 || bgs[0] != "background/background"+strconv.Itoa(bgID)+"_3" {
		t.Fatalf("夜晚背景声明: %v", bgs)
	}
	shown := map[string]int{}
	for _, wv := range appear {
		for _, ev := range wv.(map[string]any) {
			rs, _ := ev.(map[string]any)["rewards"].([]any)
			for _, rv := range rs {
				rm := rv.(map[string]any)
				if k := rm["key"].(string); k == "gacha_point" || k == "guild_asset" {
					shown[k] += int(rm["count"].(float64))
				}
			}
		}
	}
	// quest1 normal 固定档：金币 300 / 资材 100（白天口径，见 TestBattleChainWire）。
	if shown["gacha_point"] != 600 || shown["guild_asset"] != 200 {
		t.Fatalf("夜晚展示收益应 ×2: %v", shown)
	}
	bid := strings.SplitN(doc["ap_use_url"].(string), "bid=", 2)[1]
	gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+bid, "_method=GET")
	var gp0 int64
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&gp0); err != nil {
		t.Fatal(err)
	}
	if res := gameJSON(t, ts.URL, "/quests/result", uid, sk, s0, s1, "bid="+bid+"&time=30&mc=0", "_method=GET"); res["status"] != "success" {
		t.Fatalf("result: %v", res)
	}
	var gp, ga int64
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&gp); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='guild_asset'`, uid).Scan(&ga); err != nil {
		t.Fatal(err)
	}
	if gp-gp0 != 600 || ga != 200 {
		t.Fatalf("夜晚入账应与展示一致: gp+%d ga=%d", gp-gp0, ga)
	}
}

// TestExecuteDaytimeDefault 未配置时段随机：全程白天、rush_started=false、只声明 _1 背景。
func TestExecuteDaytimeDefault(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServer(t, dir)
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-b-day")
	seedBattleUnits(t, st, uid)
	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348", "_method=GET")
	for w, v := range doc["timezone_table"].(map[string]any) {
		if v.(float64) != 1 {
			t.Fatalf("wave %s 默认应为白天: %v", w, v)
		}
	}
	if doc["rush_started"] != false {
		t.Fatalf("rush_started 默认应为 false: %v", doc["rush_started"])
	}
	for _, a := range doc["asset_names"].([]any) {
		if s := a.(string); strings.HasPrefix(s, "background/") && !strings.HasSuffix(s, "_1") {
			t.Fatalf("白天不应声明其他时段背景: %s", s)
		}
	}
}
