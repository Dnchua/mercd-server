package cn35game

import (
	mathrand "math/rand"
	"testing"
)

func TestRollBattleTimezones(t *testing.T) {
	table := map[string]any{"1": map[string]any{}, "2": map[string]any{}, "3": map[string]any{}, "10": map[string]any{}}
	rnd := mathrand.New(mathrand.NewSource(1))
	if got := rollBattleTimezones(BattleTimezoneConfig{}, table, rnd); got != nil {
		t.Fatalf("未启用应返回 nil: %v", got)
	}
	if rnd.Int63() != mathrand.New(mathrand.NewSource(1)).Int63() {
		t.Fatal("未启用不应消耗随机源")
	}
	if got := rollBattleTimezones(BattleTimezoneConfig{NightRate: 1}, table, rnd); got["1"] != 3 || got["10"] != 3 {
		t.Fatalf("NightRate=1 应全夜: %v", got)
	}
	got := rollBattleTimezones(BattleTimezoneConfig{AdvanceRate: 1}, table, rnd)
	if got["1"] != 1 || got["2"] != 2 || got["3"] != 3 || got["10"] != 3 {
		t.Fatalf("AdvanceRate=1 应逐波推进且封顶夜: %v", got)
	}
	cfg := NormalizeBattleTimezoneConfig(BattleTimezoneConfig{DuskRate: 0.3, NightRate: 0.2, AdvanceRate: 0.3})
	for i := 0; i < 500; i++ {
		got := rollBattleTimezones(cfg, table, rnd)
		prev := 0
		for _, w := range []string{"1", "2", "3", "10"} {
			if got[w] < prev || got[w] < 1 || got[w] > 3 {
				t.Fatalf("时段非法或回退: %v", got)
			}
			prev = got[w]
		}
	}
}

func TestAppearTableWithTimezoneRewards(t *testing.T) {
	entry := func(gp int) map[string]any {
		return map[string]any{"id": 1, "rewards": []any{
			map[string]any{"key": "gacha_point", "count": gp},
			map[string]any{"key": "guild_asset", "count": 10},
			map[string]any{"key": "power", "count": 140},
		}}
	}
	table := map[string]any{
		"1": map[string]any{"2000": entry(37)},
		"2": map[string]any{"2000": entry(37), "4000": map[string]any{"type": "boss_appear"}},
		"3": map[string]any{"2000": entry(37)},
	}
	out := appearTableWithTimezoneRewards(table, map[string]int{"1": 1, "2": 2, "3": 3})
	got := AggregateRewards(out)
	want := map[string]int{"gacha_point": 37 + 44 + 74, "guild_asset": 10 + 12 + 20, "power": 420}
	for _, r := range got {
		if want[r.Key] != r.Count {
			t.Fatalf("%s = %d, want %d", r.Key, r.Count, want[r.Key])
		}
	}
	if table["3"].(map[string]any)["2000"].(map[string]any)["rewards"].([]any)[0].(map[string]any)["count"] != 37 {
		t.Fatal("不应改写入参")
	}
	rs := timezoneScaledRewards([]BattleReward{{Key: "gacha_point", Count: 300}, {Key: "seed", ID: 5, Count: 1}}, TimezoneNight)
	if rs[0].Count != 600 || rs[1].Count != 1 {
		t.Fatalf("timezoneScaledRewards: %+v", rs)
	}
}
