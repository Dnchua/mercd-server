// memorial_masterdata_test.go 纪念版富化层单测：
// 单位 Heal 映射 / 官方样本优先 / 魔宠 execute 全量文档 / 文本补缺 /
// catalog 全量回归（act_target_id 非空、multi_hit_count≥1）。
package cn35game

import (
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

func loadTestCatalog(t *testing.T) *cardcatalog.Catalog {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	return cat
}

func docNum(t *testing.T, doc map[string]any, key string) float64 {
	t.Helper()
	switch v := doc[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		t.Fatalf("%s=%v（类型 %T，不是数值）", key, doc[key], doc[key])
		return 0
	}
}

// TestMemorialUnitHeal 单位 13（武器 7 祭司）：纪念版 actType=Heal →
// act_type_id=heal、act_target_id=reachable_units，逐单位行为真值覆盖
// 武器默认（此前"祭司行为未证"由纪念版 257 个回復单位解决）。
func TestMemorialUnitHeal(t *testing.T) {
	cat := loadTestCatalog(t)
	u, err := cat.Unit(13)
	if err != nil {
		t.Fatalf("模板 13: %v", err)
	}
	doc, err := buildUnitDoc(UnitRecord{UnitID: 13, Level: 1}, u)
	if err != nil {
		t.Fatalf("buildUnitDoc: %v", err)
	}
	if doc["act_type_id"] != "heal" {
		t.Fatalf("act_type_id=%v want heal", doc["act_type_id"])
	}
	if doc["act_target_id"] != "reachable_units" {
		t.Fatalf("act_target_id=%v want reachable_units", doc["act_target_id"])
	}
	if docNum(t, doc, "hit_frame") != 10 {
		t.Fatalf("hit_frame=%v want 10（纪念版真值，覆盖武器 7 默认 33）", doc["hit_frame"])
	}
	if docNum(t, doc, "attack_range") != 25 {
		t.Fatalf("attack_range=%v want 25", doc["attack_range"])
	}
	if doc["attack_se"] != "sound/S-26" {
		t.Fatalf("attack_se=%v want sound/S-26", doc["attack_se"])
	}
	if docNum(t, doc, "multi_hit_count") != 1 || docNum(t, doc, "multi_hit_interval") != 0 {
		t.Fatalf("multi_hit=%v/%v want 1/0", doc["multi_hit_count"], doc["multi_hit_interval"])
	}
}

// TestMemorialUnitDriftOfficialWins 官方样本优先于纪念版漂移值：
// 单位 156 官方 hit_frame=34 / attack_se=sound/S-23，
// 纪念版分别为 33 / sound/S-58——必须下发官方值。
func TestMemorialUnitDriftOfficialWins(t *testing.T) {
	cat := loadTestCatalog(t)
	u, err := cat.Unit(156)
	if err != nil {
		t.Fatalf("模板 156: %v", err)
	}
	doc, err := buildUnitDoc(UnitRecord{UnitID: 156, Level: 1}, u)
	if err != nil {
		t.Fatalf("buildUnitDoc: %v", err)
	}
	if docNum(t, doc, "hit_frame") != 34 {
		t.Fatalf("hit_frame=%v want 34（官方样本，纪念版=33 属漂移）", doc["hit_frame"])
	}
	if doc["attack_se"] != "sound/S-23" {
		t.Fatalf("attack_se=%v want sound/S-23（官方样本）", doc["attack_se"])
	}
}

// TestMemorialUnitTextFill 单位文本补缺：wiki remark 空的单位从
// 纪念版 unit_text 补 description（用户决策：翻译数据同步到服务端）。
func TestMemorialUnitTextFill(t *testing.T) {
	cat := loadTestCatalog(t)
	filled := 0
	for _, id := range cat.UnitIDs() {
		u, err := cat.Unit(id)
		if err != nil {
			t.Fatalf("Unit(%d): %v", id, err)
		}
		if u.Remark != "" || memorialUnitText(id) == nil {
			continue
		}
		doc, err := buildUnitDoc(UnitRecord{UnitID: id, Level: 1}, u)
		if err != nil {
			t.Fatalf("unit %d buildUnitDoc: %v", id, err)
		}
		if s, _ := doc["description"].(string); s == "" {
			t.Fatalf("unit %d wiki remark 空且纪念版有 description，但未补缺", id)
		}
		filled++
	}
	if filled == 0 {
		t.Fatal("没有任何单位走到文本补缺路径——unit_text 可能全空")
	}
	t.Logf("纪念版 description 补缺单位数=%d", filled)
}

// TestBattleMonsterDocOfficial 魔宠 1（官方样本怪）：official 层逐值
// 覆盖——reach=10（非 wiki aarea=20）、attack_ms=2400、
// call_interval_ms=21899、cost=250、knockback 真值、hardness_id=2；
// wiki 形键（mspd/aarea/tenacity）同时下发。
func TestBattleMonsterDocOfficial(t *testing.T) {
	cat := loadTestCatalog(t)
	m, err := cat.Monster(1)
	if err != nil {
		t.Fatalf("模板怪 1: %v", err)
	}
	doc := BattleMonsterDoc(m)
	if doc["act_target_id"] != "reachable_units_with_sidebase" {
		t.Fatalf("act_target_id=%v", doc["act_target_id"])
	}
	if docNum(t, doc, "reach") != 10 {
		t.Fatalf("reach=%v want 10（官方=reachValue；wiki aarea=20 不可用）", doc["reach"])
	}
	if docNum(t, doc, "attack_ms") != 2400 {
		t.Fatalf("attack_ms=%v want 2400（官方）", doc["attack_ms"])
	}
	if docNum(t, doc, "call_interval_ms") != 21899 {
		t.Fatalf("call_interval_ms=%v want 21899（官方 float32 截断值）", doc["call_interval_ms"])
	}
	if docNum(t, doc, "cost") != 250 {
		t.Fatalf("cost=%v want 250（官方）", doc["cost"])
	}
	if docNum(t, doc, "knockback_local_x") != 20 || docNum(t, doc, "knockback_damage_rate") != 0.35 {
		t.Fatalf("knockback=%v/%v want 20/0.35（官方真值）",
			doc["knockback_local_x"], doc["knockback_damage_rate"])
	}
	if docNum(t, doc, "hardness_id") != 2 || docNum(t, doc, "base_scale") != 1.3 {
		t.Fatalf("hardness_id=%v base_scale=%v want 2/1.3", doc["hardness_id"], doc["base_scale"])
	}
	if doc["rare_type_name"] != "╂" || doc["attr_name"] != "炎" {
		t.Fatalf("rare_type_name=%v attr_name=%v", doc["rare_type_name"], doc["attr_name"])
	}
	if docNum(t, doc, "run_b_frame1") != 2 || docNum(t, doc, "run_e_frame1") != 8 {
		t.Fatalf("run 帧=%v/%v want 2/8", doc["run_b_frame1"], doc["run_e_frame1"])
	}
	if doc["aarea"] != 20 {
		t.Fatalf("aarea=%v want 20（wiki 形键保留）", doc["aarea"])
	}
	if _, ok := doc["hp"]; !ok {
		t.Fatal("hp 缺键——官方怪无 wiki life 时应有纪念版基值兜底")
	}
}

// TestBattleMonsterDocMemorial 非官方纪念版怪：行为层为 memorial 真值，
// wiki 有效数值（mspd/aspd/anum）双形下发且覆盖 doc 形键。
func TestBattleMonsterDocMemorial(t *testing.T) {
	cat := loadTestCatalog(t)
	m, err := cat.Monster(3)
	if err != nil {
		t.Fatalf("模板怪 3: %v", err)
	}
	if _, ok := memorialTableData().MonsterOfficial["3"]; ok {
		t.Skip("怪 3 在官方样本层")
	}
	doc := BattleMonsterDoc(m)
	if doc["act_target_id"] != "reachable_units_with_sidebase" {
		t.Fatalf("act_target_id=%v", doc["act_target_id"])
	}
	entry, ok := memorialMonsterEntry(3)
	if !ok {
		t.Skip("怪 3 无纪念版条目")
	}
	if docNum(t, doc, "reach") != docNum(t, entry, "reach") {
		t.Fatalf("reach=%v want %v（纪念版 reachValue）", doc["reach"], entry["reach"])
	}
	if docNum(t, doc, "speed") != docNum(t, entry, "speed") {
		t.Fatalf("speed=%v want %v（纪念版主数据优先）", doc["speed"], entry["speed"])
	}
	if docNum(t, doc, "attackable_count") < 1 {
		t.Fatalf("attackable_count=%v", doc["attackable_count"])
	}
}

// TestBattleMonsterDocFullCatalog 全量魔宠回归：每个魔宠 doc 都带
// act_target_id（否则客户端 targets 恒空不打人）、hit_frame/attack_range/
// attackable_count 有效值（defaults 层保证 CN 独占怪也有全键）。
func TestBattleMonsterDocFullCatalog(t *testing.T) {
	cat := loadTestCatalog(t)
	ids := cat.MonsterIDs()
	if len(ids) != 1052 {
		t.Logf("catalog 魔宠数=%d（期望 1052，继续逐条断言）", len(ids))
	}
	for _, id := range ids {
		m, err := cat.Monster(id)
		if err != nil {
			t.Fatalf("Monster(%d): %v", id, err)
		}
		doc := BattleMonsterDoc(m)
		if s, _ := doc["act_target_id"].(string); s == "" {
			t.Fatalf("monster %d act_target_id 为空", id)
		}
		if doc["act_type_id"] != "attack" {
			t.Fatalf("monster %d act_type_id=%v", id, doc["act_type_id"])
		}
		for _, k := range []string{"attackable_count", "attack_ms", "hit_frame",
			"attack_range", "speed", "reach"} {
			if docNum(t, doc, k) <= 0 {
				t.Fatalf("monster %d %s=%v（缺键或非正——出手链会断）", id, k, doc[k])
			}
		}
		if docNum(t, doc, "multi_hit_count") < 1 {
			t.Fatalf("monster %d multi_hit_count=%v", id, doc["multi_hit_count"])
		}
	}
}

// TestMemorialUnitOverrideLayer 纪念服覆盖层：名称/称号/简介/档案/基础数值
// 命中 unit_text 的 *_override 键时压过国服 catalog（不改 units.json，
// 以保住 catalog SHA 与历史 grant 配置的可重放性）。
//
// 覆盖 1277（P3 边界单位，catalog 名字是日文「フィーユ」→ 汉化「菲优」）。
func TestMemorialUnitOverrideLayer(t *testing.T) {
	cat := loadTestCatalog(t)
	u, err := cat.Unit(1277)
	if err != nil {
		t.Fatalf("模板 1277: %v", err)
	}
	doc, err := buildUnitDoc(UnitRecord{UnitID: 1277, Level: 1}, u)
	if err != nil {
		t.Fatalf("buildUnitDoc: %v", err)
	}
	if got := doc["main_name"]; got != "菲优" {
		t.Fatalf("main_name=%v want 菲优（纪念服汉化覆盖）", got)
	}
	if got := doc["prefix_name"]; got != "月露翅之秋壳" {
		t.Fatalf("prefix_name=%v want 月露翅之秋壳", got)
	}
	if got := doc["name"]; got != "[月露翅之秋壳]菲优" {
		t.Fatalf("name=%v want [月露翅之秋壳]菲优", got)
	}
	if _, ok := doc["description"].(string); !ok {
		t.Fatalf("缺 description（纪念服简介应下发）")
	}
}

// TestMemorialUnitNumericOverride 数值覆盖层：152 的 atk 反解为 3900，
// 使满觉满级终值精确命中纪念服 maxAttack=12840（未覆盖时 11835）。
func TestMemorialUnitNumericOverride(t *testing.T) {
	cat := loadTestCatalog(t)
	u, err := cat.Unit(152)
	if err != nil {
		t.Fatalf("模板 152: %v", err)
	}
	doc, err := buildUnitDoc(UnitRecord{UnitID: 152, Level: 1}, u)
	if err != nil {
		t.Fatalf("buildUnitDoc: %v", err)
	}
	if docNum(t, doc, "at_min") != 3900 {
		t.Fatalf("at_min=%v want 3900（纪念服终值反解）", doc["at_min"])
	}
	if docNum(t, doc, "at_15_max") != 12840 {
		t.Fatalf("at_15_max=%v want 12840（纪念服 maxAttack）", doc["at_15_max"])
	}
	if docNum(t, doc, "hp_15_max") != 12078 {
		t.Fatalf("hp_15_max=%v want 12078（纪念服 maxHp）", doc["hp_15_max"])
	}
}
