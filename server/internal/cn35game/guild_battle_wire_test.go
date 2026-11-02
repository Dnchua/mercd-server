package cn35game

import (
	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"path/filepath"
	"testing"
)

// 会战 execute 文档：防卫魔宠走 boss_appear_table（击杀完即 Clear），
// 小怪走 appear_table 单波时间轴，难度间隔/数量按 GuildRules。
func TestGWExecuteEffectsAndBossTable(t *testing.T) {
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	dep := Deps{Catalog: cat, AssetsDir: filepath.Join("..", "..", "assets", "cn350")}
	rec := UnitRecord{UnitID: 348, Level: 1}
	atk := &GWAttack{BattleID: "test", UserID: "test", Difficulty: "normal", BPCost: 50,
		Effects: GWEffects{Version: 1, InitialSP: 925, UnitHP: 1.1, UnitAT: 1.2, UnitToughness: 1.3},
		DefenseSnapshot: []GWDefenseMonster{
			{OwnerUserID: "one", GuildID: "a", Slot: 0, MonsterID: 3, Level: 1, HPBonus: 0.1, ATBonus: 0.01},
			{OwnerUserID: "two", GuildID: "b", Slot: 0, MonsterID: 3, Level: 1},
		},
	}
	doc, err := buildGWExecuteDoc(dep, atk, []UnitRecord{rec}, nil, &PlayerSnapshot{User: map[string]any{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if doc["quest"].(map[string]any)["initial_power"] != 925 {
		t.Fatal("SP not applied")
	}
	// boss_appear_table：防卫快照成对出场（组内 interval_sec=0、组间=难度间隔）。
	bosses, ok := doc["boss_appear_table"].(map[string]any)["1"].([]any)
	if !ok || len(bosses) != 2 {
		t.Fatalf("boss_appear_table: %v", doc["boss_appear_table"])
	}
	b0 := bosses[0].(map[string]any)
	if b0["interval_sec"] != 0 || b0["gauge"] != true {
		t.Fatalf("boss0 应紧随且带血条: %+v", b0)
	}
	b1 := bosses[1].(map[string]any)
	if b1["interval_sec"] != GuildRules.DifficultyWaveIntervalSec["normal"] {
		t.Fatalf("boss1 间隔应为普通 120s: %+v", b1)
	}
	params := b0["params"].(map[string]any)
	// hp = 500*(1+0)*1.0*(1+0.1)=550；at = 100*1.0*(1+0.01)=101
	if params["hp"] != 550 || params["at"] != 101 {
		t.Fatalf("prayer not applied: %+v", params)
	}
	// appear_table 只剩小怪（无防卫魔宠 id=3），数量为 normal 口径 80。
	wave := doc["appear_table"].(map[string]any)["1"].(map[string]any)
	if len(wave) != GuildRules.DifficultyTrashTotal["normal"] {
		t.Fatalf("小怪数 = %d, want %d", len(wave), GuildRules.DifficultyTrashTotal["normal"])
	}
	for _, ev := range wave {
		if ev.(map[string]any)["id"] == 3 {
			t.Fatal("防卫魔宠不应进小怪 appear_table")
		}
	}
	// 无波次限时（无战斗总时长限制）。
	if _, has := doc["quest"].(map[string]any)["wave_conf"].(map[string]any)["1"].(map[string]any)["time_limit"]; has {
		t.Fatal("会战不应下发 time_limit")
	}
	u, _ := cat.Unit(348)
	base, err := buildUnitDoc(rec, u)
	if err != nil {
		t.Fatal(err)
	}
	got := doc["units"].([]any)[0].(map[string]any)
	if got["hp"] != int(float64(base["hp"].(int))*1.1) || got["at"] != int(float64(base["at"].(int))*1.2) {
		t.Fatalf("facilities not applied: %+v", got)
	}
	if toughness, ok := base["taghness"].(float64); ok && got["taghness"] != toughness*1.3 {
		t.Fatal("toughness not applied")
	}
	// 防卫魔宠素材必须在 asset_names 内（客户端 AddMonster 按 id 取包）。
	found := false
	for _, a := range doc["asset_names"].([]string) {
		if a == "monster/monster_3" {
			found = true
		}
	}
	if !found {
		t.Fatal("boss 魔宠素材缺失")
	}
	// Legacy attacks remain renderable with the original SP baseline.
	atk.Effects = GWEffects{}
	doc, err = buildGWExecuteDoc(dep, atk, []UnitRecord{rec}, nil, &PlayerSnapshot{User: map[string]any{}}, nil)
	if err != nil || doc["quest"].(map[string]any)["initial_power"] != 500 {
		t.Fatalf("legacy effects: %v", err)
	}
}

// quest.id = **目标公会 id**（GC-05 取证）：原服样本 b9b76114-… 同时是
// battle_condition.guilds 里的目标会 id 与 ap_use_url 的 guild_id 参数，契约
// 要求 string。此前按 battleID 前 8 位十六进制派生成数字，是为绕开客户端
// 文档池去重；复核 Quest.cs:637-643 后确认去重条件是
// `pageMessage.data["id"]` 与 quest.id 比较，而会战页消息只带 guild_id、不带
// id ⇒ 池恒为 null，不会复用上一场文档，故不再需要唯一 id。
func TestGWQuestDocIDIsTargetGuild(t *testing.T) {
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	dep := Deps{Catalog: cat, AssetsDir: filepath.Join("..", "..", "assets", "cn350")}
	rec := UnitRecord{UnitID: 348, Level: 1}
	mk := func(target string) map[string]any {
		atk := &GWAttack{BattleID: "bg1234567890abcdef1234", UserID: "t",
			Difficulty: "normal", TargetGuildID: target, TargetGuildIDs: []string{target}}
		doc, err := buildGWExecuteDoc(dep, atk, []UnitRecord{rec}, nil,
			&PlayerSnapshot{User: map[string]any{}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return doc["quest"].(map[string]any)
	}
	quest := mk("guild-target-a")
	if id, ok := quest["id"].(string); !ok || id != "guild-target-a" {
		t.Fatalf("quest.id = %#v, want 目标会 id 字符串", quest["id"])
	}
	// 同一目标、不同 battleID 也必须是同一 id（原服语义：id 是目标会，不是会话）。
	if again := mk("guild-target-a"); again["id"] != quest["id"] {
		t.Fatalf("quest.id 不应随 battleID 变化: %#v", again["id"])
	}
	if other := mk("guild-target-b"); other["id"] == quest["id"] {
		t.Fatal("不同目标会应产出不同 quest.id")
	}
	// name 形状："vs <会名>(<目标数>个公会)"；无 battle_condition 供名时用占位名。
	if name, _ := quest["name"].(string); name != "vs 目标公会(1个公会)" {
		t.Fatalf("quest.name = %q", name)
	}
}

// 防卫快照超过 BattleBossTotal 时截断为上限只数。
func TestGWBossCap(t *testing.T) {
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	dep := Deps{Catalog: cat, AssetsDir: filepath.Join("..", "..", "assets", "cn350")}
	snap := []GWDefenseMonster{}
	for i := 0; i < GuildRules.BattleBossTotal+3; i++ {
		snap = append(snap, GWDefenseMonster{OwnerUserID: "x", GuildID: "a", Slot: i % 5, MonsterID: 3, Level: 1})
	}
	atk := &GWAttack{BattleID: "t", UserID: "t", Difficulty: "very_hard", DefenseSnapshot: snap}
	list, err := gwBossAppearList(atk, dep)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != GuildRules.BattleBossTotal {
		t.Fatalf("boss 数 = %d, want %d", len(list), GuildRules.BattleBossTotal)
	}
}

// 多目标总量超上限时按目标轮转截断：每个被收费目标都须有
// 防卫怪出场（顺序截断会让后排目标不出场却仍被掠夺 GP）。
func TestGWBossCapMultiTargetRoundRobin(t *testing.T) {
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	dep := Deps{Catalog: cat, AssetsDir: filepath.Join("..", "..", "assets", "cn350")}
	snap := []GWDefenseMonster{}
	// 甲 12 只、乙 3 只：顺序截断会把乙整场吃掉。
	for i := 0; i < 12; i++ {
		snap = append(snap, GWDefenseMonster{OwnerUserID: "a" + string(rune('0'+i%10)), GuildID: "g-a", Slot: i % 5, MonsterID: 3, Level: 1})
	}
	for i := 0; i < 3; i++ {
		snap = append(snap, GWDefenseMonster{OwnerUserID: "b" + string(rune('0'+i)), GuildID: "g-b", Slot: i, MonsterID: 4, Level: 1})
	}
	atk := &GWAttack{BattleID: "t", UserID: "t", Difficulty: "very_hard", DefenseSnapshot: snap}
	list, err := gwBossAppearList(atk, dep)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != GuildRules.BattleBossTotal {
		t.Fatalf("boss 数 = %d, want %d", len(list), GuildRules.BattleBossTotal)
	}
	bCount := 0
	for _, e := range list {
		if e.(map[string]any)["id"] == 4 {
			bCount++
		}
	}
	if bCount == 0 {
		t.Fatal("目标乙的防卫怪被截断清空，但其 GP 仍会被掠夺")
	}
}
