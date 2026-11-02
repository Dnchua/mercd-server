// memorial_masterdata.go 纪念版 MasterData 富化层（编译期内嵌）。
//
// 数据来自 JP 纪念版 UnitMasterData/MonsterMasterData（mercstoria-recovery
// 已解密真值，逐单位/逐魔宠），由 tools/build_memorial_enrichment.py 生成
// 并与官方样本对拍：8 单位 83 中/5 漂移、15 魔宠全中。
//
// 分层（高→低）：
//   - 官方样本：unit_battle_behavior.json unit_overrides（8 单位）、
//     monster_official（quest0 15 魔宠，逐值含 knockback）。
//   - 纪念版：unit_memorial / monster_memorial（行为字段 + 怪物
//     reach/call_interval_ms 覆盖下发）。
//   - 铺底：weapon_defaults / monster_defaults（knockback 重建值·非原服）。
//   - 文本补缺：unit_text / monster_text 只收 CN catalog 缺口条目。
//
// 纪念版没有的键：knockback_*、act_target_id（按 actType 推导）、
// attack_stage_effect、run_b/e_frame2；CN 独占 ID 走默认层。
package cn35game

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

//go:embed memorial_masterdata.json
var memorialMasterdataJSON []byte

// memorialTable 纪念版富化表；各 map 键为字符串模板 ID。
// 行为字段原样下发，不做二次建模（同 unit_battle_behavior 口径）。
type memorialTable struct {
	UnitMemorial    map[string]map[string]any `json:"unit_memorial"`
	UnitText        map[string]map[string]any `json:"unit_text"`
	MonsterDefaults map[string]any            `json:"monster_defaults"`
	MonsterMemorial map[string]map[string]any `json:"monster_memorial"`
	MonsterOfficial map[string]map[string]any `json:"monster_official"`
	MonsterText     map[string]map[string]any `json:"monster_text"`
}

var (
	memorialOnce sync.Once
	memorialData *memorialTable
)

// memorialTableData 懒解析内嵌 JSON；解析失败 panic——内嵌资源损坏属
// 编程错误，没有可恢复路径（同 battleBehaviorTable 口径）。
func memorialTableData() *memorialTable {
	memorialOnce.Do(func() {
		var t memorialTable
		if err := json.Unmarshal(memorialMasterdataJSON, &t); err != nil {
			panic(fmt.Sprintf("memorial_masterdata.json 解析失败（内嵌资源损坏）: %v", err))
		}
		memorialData = &t
	})
	return memorialData
}

// memorialUnitBattle 单位行为覆盖（纪念版）；CN 独占/8 官方样本未收录。
func memorialUnitBattle(unitID int) (map[string]any, bool) {
	e, ok := memorialTableData().UnitMemorial[strconv.Itoa(unitID)]
	return e, ok
}

// MemorialUnitConfigured 报告该单位在 memorial_masterdata.json 是否有
// 数值配置（unit_memorial 行为条目）。卡池成员的可抽资格之一：素材证据
// 齐全但无行为数值的单位进池后战斗中会退回缺省行为，必须挡在池外。
func MemorialUnitConfigured(unitID int) bool {
	_, ok := memorialUnitBattle(unitID)
	return ok
}

// memorialUnitText 单位文本补缺条目（description/profession/favorite/
// personality/age/sex_id），只含 CN catalog 缺口；无缺口返回 nil。
func memorialUnitText(unitID int) map[string]any {
	return memorialTableData().UnitText[strconv.Itoa(unitID)]
}

// memorialUnitTextOverride 取 `unit_text[<id>]` 下的**覆盖值**（纪念服汉化/数值覆盖）。
//
// 与 ut["description"] 这类「只补缺口」的旧键不同：`main_name`/`prefix_name`/
// `*_override` 是显式覆盖，命中即压过国服 catalog 值。用途是把纪念服已汉化的
// 名称/称号/简介/档案与「以纪念服终值反解出的 Lv1 基础值」下发到客户端，而
// 不改 `units.json`（它的 SHA 被 cardcatalog/grant 配置/审计输入同时钉住，
// 改动会让历史批次配置无法重放）。
func memorialUnitTextOverride(unitID int, key string) (string, bool) {
	e := memorialTableData().UnitText[strconv.Itoa(unitID)]
	if e == nil {
		return "", false
	}
	v, ok := e[key].(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// memorialUnitIntOverride 数值覆盖（life/atk/type），JSON 数字按 float64 解码。
func memorialUnitIntOverride(unitID int, key string) (int, bool) {
	e := memorialTableData().UnitText[strconv.Itoa(unitID)]
	if e == nil {
		return 0, false
	}
	v, ok := e[key].(float64)
	if !ok {
		return 0, false
	}
	return int(v), true
}

// memorialMonsterEntry 魔宠纪念版条目原始 map（含 catalog_fill 子表——
// 它是生成元数据不是下发键，应用层须跳过）。官方样本怪在 monster_official。
func memorialMonsterEntry(monsterID int) (map[string]any, bool) {
	e, ok := memorialTableData().MonsterMemorial[strconv.Itoa(monsterID)]
	return e, ok
}

// memorialMonsterFill 魔宠 catalog 缺口回填值（speed/attack_ms/
// attackable_count/cost/sklcd/taghness）；无缺口返回 nil。
func memorialMonsterFill(monsterID int) map[string]any {
	e, ok := memorialMonsterEntry(monsterID)
	if !ok {
		return nil
	}
	f, _ := e["catalog_fill"].(map[string]any)
	return f
}

// applyMemorialMonsterBattle 魔宠行为字段下发：
// monster_defaults 铺底 → monster_official 逐值（含 knockback）→
// 否则 monster_memorial（catalog_fill 子表跳过）。返回命中层名。
func applyMemorialMonsterBattle(doc map[string]any, monsterID int) string {
	t := memorialTableData()
	for k, v := range t.MonsterDefaults {
		doc[k] = v
	}
	id := strconv.Itoa(monsterID)
	if ov, ok := t.MonsterOfficial[id]; ok {
		for k, v := range ov {
			doc[k] = v
		}
		return "official"
	}
	if e, ok := t.MonsterMemorial[id]; ok {
		for k, v := range e {
			if k == "catalog_fill" {
				continue
			}
			doc[k] = v
		}
		return "memorial"
	}
	return "defaults"
}

// memorialMonsterText 魔宠文本补缺（description/obtain）。
func memorialMonsterText(monsterID int) map[string]any {
	return memorialTableData().MonsterText[strconv.Itoa(monsterID)]
}
