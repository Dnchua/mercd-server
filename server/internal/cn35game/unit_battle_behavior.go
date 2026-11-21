// unit_battle_behavior.go 同伴战斗行为字段下发（PieceParameter/TriggerRange/
// BaseAI 消费）。units.json（wiki 数据源）不含 act_type_id/act_target_id/
// hit_frame/multi_hit_count/attack_range 等行为字段；缺省时客户端
// act_target_id=="" → BaseAI.UpdateTargets 无分支命中、targets 恒空，
// 单位永远 Idle 不出手；multi_hit_count=0 → 伤害循环 0 次。
//
// 数据来源（unit_battle_behavior.json + memorial_masterdata.json，编译期内嵌）：
//   - unit_overrides：8 个官方样本逐值拷贝（原包 Config/quest0.json
//     help_table/units 与 serve_quest_execute_1.json）。
//   - unit_memorial：JP 纪念版 UnitMasterData 逐单位行为字段（1627 覆盖；
//     act_target_id 按 actType 推导：attack→reachable_monsters、
//     heal→reachable_units——纪念版 257 个回復武器单位全为 Heal，
//     解决此前"祭司治疗未证"）。
//   - weapon_defaults：【重建值·非原服】按武器取代表值；CN 独占单位
//     （202 个）与 knockback_*（纪念版无此字段）走此层。
//
// 派生细节与出处见两份 JSON 的 _provenance 字段。
package cn35game

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

//go:embed unit_battle_behavior.json
var unitBattleBehaviorJSON []byte

// unitBattleBehaviorTable 行为字段表：JSON 数值保留为 float64 原样下发
// （客户端 f()/i() 均可读），不做二次建模。
type unitBattleBehaviorTable struct {
	WeaponDefaults map[string]map[string]any `json:"weapon_defaults"`
	UnitOverrides  map[string]map[string]any `json:"unit_overrides"`
}

var (
	unitBattleBehaviorOnce sync.Once
	unitBattleBehaviorData *unitBattleBehaviorTable
)

// battleBehaviorTable 懒解析内嵌 JSON；解析失败 panic——文件编译期内嵌，
// 解析失败属编程错误，没有可恢复路径。
func battleBehaviorTable() *unitBattleBehaviorTable {
	unitBattleBehaviorOnce.Do(func() {
		var t unitBattleBehaviorTable
		if err := json.Unmarshal(unitBattleBehaviorJSON, &t); err != nil {
			panic(fmt.Sprintf("unit_battle_behavior.json 解析失败（内嵌资源损坏）: %v", err))
		}
		unitBattleBehaviorData = &t
	})
	return unitBattleBehaviorData
}

// UnitBehaviorConfigured 报告该单位是否有行为数值配置：官方样本
// unit_overrides 命中（下发时优先级最高，见 applyUnitBattleBehavior a 层）
// 或 unit_memorial 条目存在。卡池准入与发布校验用本口径——否则 8 个
// 官方样本单位虽有真值行为仍会被"无 unit_memorial 条目"误挡在池外。
func UnitBehaviorConfigured(unitID int) bool {
	if _, ok := battleBehaviorTable().UnitOverrides[strconv.Itoa(unitID)]; ok {
		return true
	}
	return MemorialUnitConfigured(unitID)
}

// OfficialSampleUnitIDs 返回 unit_overrides 官方样本单位 ID（升序）。
func OfficialSampleUnitIDs() []int {
	tbl := battleBehaviorTable()
	ids := make([]int, 0, len(tbl.UnitOverrides))
	for k := range tbl.UnitOverrides {
		if id, err := strconv.Atoi(k); err == nil {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// elementEffectName attr_id → 特效资源名段（target_effect 模板 {element} 占位）。
var elementEffectName = map[int]string{
	1: "fire", 2: "water", 3: "wind", 4: "light", 5: "dark",
}

// applyUnitBattleBehavior 把战斗行为字段写入单位文档：
//
//	a. unit_overrides 命中 u.ID → 官方样本逐值原样下发，结束。
//	b. weapon_defaults[u.Weapon] 铺底（武器不在 1..7 回退 "3"）——
//	   knockback_* 等纪念版没有的键靠这一层；target_effect 仍含
//	   {element} 时按 attr_id 替换，element 不在 1..5 置 ""。
//	c. unit_memorial 命中 → 纪念版逐单位行为字段覆盖铺底值
//	   （multi_hit_count/interval 为真值，不再走 wiki hits 派生——
//	   wiki hits 已被 348 号证伪：官方 4、wiki 3、纪念版 4）。
//	d. 纪念版未覆盖（CN 独占）→ multi_hit_count 走 wiki hits 派生：
//	   u.Hits ≥1 取之；否则 Raw["hits"] 数字串 ≥1 取之；其余 → 1。
//	multi_hit_count==1 时 multi_hit_interval 强制 0（与样本一致）。
func applyUnitBattleBehavior(doc map[string]any, u *cardcatalog.Unit) {
	tbl := battleBehaviorTable()

	// a. 官方样本覆盖：逐值原样，不再走默认派生。
	if ov, ok := tbl.UnitOverrides[strconv.Itoa(u.ID)]; ok {
		for k, v := range ov {
			doc[k] = v
		}
		return
	}

	// b. 武器默认行铺底（含 knockback_* 重建值）。
	def, ok := tbl.WeaponDefaults[strconv.Itoa(u.Weapon)]
	if !ok {
		def = tbl.WeaponDefaults["3"]
	}
	for k, v := range def {
		doc[k] = v
	}

	// c. 纪念版层覆盖（act/range/hit_frame/multi_hit/se/effects/offsets
	//    逐单位真值；knockback 键不在其中→自动继承 b 层估值）。
	mem, covered := memorialUnitBattle(u.ID)
	if covered {
		for k, v := range mem {
			doc[k] = v
		}
	}
	if te, ok := doc["target_effect"].(string); ok && strings.Contains(te, "{element}") {
		if name, ok := elementEffectName[u.Element]; ok {
			doc["target_effect"] = strings.ReplaceAll(te, "{element}", name)
		} else {
			doc["target_effect"] = ""
		}
	}
	if covered {
		// 纪念版 multi_hit_count 为真值；mhit==1 时 interval 归零
		// 维持官方样本规则。
		if n, ok := doc["multi_hit_count"].(float64); ok && n == 1 {
			doc["multi_hit_interval"] = 0.0
		}
		return
	}

	// d. CN 独占单位：multi_hit_count 走 wiki hits 派生，兜底 1。
	n := 0
	if u.Hits != nil && *u.Hits >= 1 {
		n = *u.Hits
	}
	if n == 0 {
		if s, ok := u.Raw["hits"].(string); ok {
			if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v >= 1 {
				n = v
			}
		}
	}
	if n == 0 {
		n = 1
	}
	doc["multi_hit_count"] = n
	if n == 1 {
		doc["multi_hit_interval"] = 0.0
	}
}
