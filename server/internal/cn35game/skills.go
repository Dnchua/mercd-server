// skills.go RUNE350-01：符文（客户端"skill"）目录、库存契约与战斗数值
// 应用规则。
//
// 客户端契约（ilspy-cs 取证）：
//   - Skill.cs：skill_id 1-37，level 为十进制魔力值；品质分档表
//     levelTable={15,22.5,27,30}，上限 33（輝=36 为合成产物段）；物品强化
//     仅品质档 1-3（level<27.001，consumeItemIds 1-15 按属性×种/蕾/花），
//     档 4+ 走 /user_skills/composite 符文合成：gain=(seed_level-26.5)/10，
//     同 skill_id 或强化用（id 32）种子 ×2，極優（30.001+）基座 ÷2。
//   - SkillButtonsListComponent.cs：装备/出售/强化页签读 user_skills
//     行文档；出售入账 user.gacha_point；卖出过滤 unit_id=="" 且
//     protected==false。
//   - SkillWithUnitListComponent/SkillWithUnitModal.cs：装备写面
//     /user_skills/set_all?unit_id=&uuids[]=（整体替换该同伴槽位集）；
//     同名 skill_id 不可重复装备；equipable_weapon_id 存在时须与单位
//     weapon_id 一致。
//   - 槽位（gamerch 510636）：上限 = 1+min(觉醒数, 稀有度)，
//     表上限 = 1+稀有度。
//   - Piece.cs:716-775：战斗读面板 hp/at/attack_ms/quick/speed/taghness
//     与 *_rate/*_guard_rate/behind_guard_rate——符文加成在服务端
//     buildUnitDoc 出包前合入这些字段即生效（battle_wire 与
//     inventory_wire 共用同一组装面）。
//
// 出售价格（gamerch 510636）：floor(150*sqrt(level/10) + 100*max(level-20,0)²)。
package cn35game

import (
	"errors"
	"fmt"
	"math"
)

// ---- 错误 ----

var (
	// ErrSkillNotOwned 符文不在本账号库存（uuid 未命中）。
	ErrSkillNotOwned = errors.New("cn35game: 符文不在本账号库存")
	// ErrSkillNotEquipable 符文不可装备（如强化用符文）。
	ErrSkillNotEquipable = errors.New("cn35game: 符文不可装备")
	// ErrSkillWeaponMismatch 职业限定符文的武器种与目标同伴不符。
	ErrSkillWeaponMismatch = errors.New("cn35game: 符文武器种限定不符")
	// ErrSkillDuplicateKind 同一 skill_id 已装备（同名不可重复）。
	ErrSkillDuplicateKind = errors.New("cn35game: 同名符文不可重复装备")
	// ErrSkillSlotFull 装备数超出槽位上限。
	ErrSkillSlotFull = errors.New("cn35game: 符文槽位已满")
	// ErrSkillProtected 符文已保护，不可出售/合成消耗。
	ErrSkillProtected = errors.New("cn35game: 符文已保护")
	// ErrSkillEquipped 已装备符文不可出售/合成消耗。
	ErrSkillEquipped = errors.New("cn35game: 符文装备中，请先卸下")
	// ErrSkillLevelCap 已达强化上限（33；合成可达段不入本写面）。
	ErrSkillLevelCap = errors.New("cn35game: 符文魔力已达上限")
	// ErrPowerupItemMismatch 强化素材与符文属性/品质档不符。
	ErrPowerupItemMismatch = errors.New("cn35game: 强化素材与符文不匹配")
	// ErrSkillCompositeBase 基座符文品质不足（需極良以上，level>=27.001）。
	ErrSkillCompositeBase = errors.New("cn35game: 基座符文品质不足，无法合成")
	// ErrSkillCompositeSeed 素材符文品质不足或为同一符文。
	ErrSkillCompositeSeed = errors.New("cn35game: 素材符文不可用于合成")
)

// SkillDef 符文种类定义（静态目录；CN 名取自 serve_app_conf UserSkills
// 筛选器文案，type_id 与筛选键一致）。
type SkillDef struct {
	SkillID int    // 客户端 skill_id（1-37）
	Name    string // CN 名（筛选器官方文案）
	TypeID  int    // 1数值系 2属性系 3守护系 4职业限定系 5特殊系
	// WeaponID 职业限定符文的 weapon_id 限定（0=不限）。武器种枚举同
	// catalog：1斩 2突 3打 4弓 5魔 6铳 7愈。
	WeaponID int
	// Equipable 是否可装备（强化用符文等素材型为 false）。
	Equipable bool
	Desc      string
}

// SkillCatalog 符文种类表。基础符文 1-25/27-31（有图标有 CN 名）；
// 26 与 32-37 未列入（无 CN 文案/图标或属上位合成域，未证）。
var SkillCatalog = map[int]SkillDef{
	1:  {1, "生命符文", 1, 0, true, "体力提升魔力值%"},
	2:  {2, "攻击符文", 1, 0, true, "攻击力提升魔力值%"},
	3:  {3, "迅捷符文", 1, 0, true, "攻击间隔缩短魔力值%"},
	4:  {4, "火之符文", 2, 0, true, "炎属性补正提升魔力值%"},
	5:  {5, "水之符文", 2, 0, true, "水属性补正提升魔力值%"},
	6:  {6, "风之符文", 2, 0, true, "风属性补正提升魔力值%"},
	7:  {7, "光之符文", 2, 0, true, "光属性补正提升魔力值%"},
	8:  {8, "暗之符文", 2, 0, true, "暗属性补正提升魔力值%"},
	9:  {9, "速度符文", 1, 0, true, "移动速度提升魔力值%"},
	10: {10, "韧性符文", 1, 0, true, "韧性提升"},
	11: {11, "火之守护", 3, 0, true, "受到的炎属性伤害降低魔力值%"},
	12: {12, "水之守护", 3, 0, true, "受到的水属性伤害降低魔力值%"},
	13: {13, "风之守护", 3, 0, true, "受到的风属性伤害降低魔力值%"},
	14: {14, "光之守护", 3, 0, true, "受到的光属性伤害降低魔力值%"},
	15: {15, "暗之守护", 3, 0, true, "受到的暗属性伤害降低魔力值%"},
	16: {16, "骑士符文", 5, 0, true, "后排同伴受到的伤害降低（魔力×2）%"},
	17: {17, "穿透符文", 4, 6, true, "攻击一定概率贯通（铳限定）"},
	18: {18, "暴击符文", 4, 4, true, "暴击率提升魔力值%（弓限定）"},
	19: {19, "治疗符文", 5, 0, true, "后退时回复量提升"},
	20: {20, "狂战士符文", 5, 0, true, "体力越少攻击越高"},
	21: {21, "防御符文", 3, 0, true, "受到的全属性伤害降低（魔力÷2）%"},
	22: {22, "魔法符文", 4, 5, true, "魔法阵蓄力加快（魔力÷4）%（魔法限定）"},
	23: {23, "回避符文", 5, 0, true, "回避率提升魔力值%"},
	24: {24, "根性符文", 5, 0, true, "受到致命伤害时概率以 1 体力留存"},
	25: {25, "士气符文", 5, 0, true, "初始士气提升"},
	27: {27, "复活符文", 4, 7, true, "战斗不能时概率复活（愈限定）"},
	28: {28, "经验符文", 5, 0, true, "获得经验值提升"},
	29: {29, "金币符文", 5, 0, true, "获得金币提升"},
	30: {30, "收集符文", 5, 0, true, "符文获得率提升"},
	31: {31, "魔宠符文", 5, 0, true, "魔宠获得率提升"},
}

// PlayerSkill 一条账号符文库存行（cn35_player_skills 投影）。
type PlayerSkill struct {
	UUID      string
	SkillID   int
	Level     float64 // 魔力值（客户端 level，十进制小数）
	BaseLevel float64 // base_level：入手时魔力（合成展示用）
	Protected bool
	UnitID    *int // 装备宿主 unit_id；nil=未装备
}

// SkillStore 符文库存读面 + 写面。方法即事务边界，账号隔离靠
// WHERE user_id 绑定；单位校验所需的槽位上限/武器种由 handler 侧
// 经 Inventory+Catalog 解析后传入（模板不在 DB）。
type SkillStore interface {
	// PlayerSkills 账号符文库存（按 skill_id,uuid 升序）。
	PlayerSkills(userID string) ([]PlayerSkill, error)
	// SkillGrant 发放符文（商店兑换/管理面共用）；返回新行。
	SkillGrant(userID string, skillID int, level float64) (*PlayerSkill, error)
	// SkillSetAll 整体替换某同伴的符文槽位集：uuids 为最终集合
	// （顺序即槽位顺序）。校验：全部为本账号符文、可装备、武器种
	// 匹配 weaponID、同 skill_id 不重复、数量<=slotCap；已在其他
	// 同伴上的符文迁移到本同伴。
	SkillSetAll(userID string, unitID, weaponID, slotCap int, uuids []string) error
	// SkillOff 卸下单枚符文（uuid 必须处于装备态）。
	SkillOff(userID, uuid string) error
	// SkillOffAll 卸下某同伴全部符文（/user_skills/off?all=true）。
	SkillOffAll(userID string, unitID int) error
	// SkillSell 批量出售：入账 gacha_point=ΣSellPrice；装备中/保护中
	// 符文拒绝。返回入账总额与扣后 gacha_point 余额。
	SkillSell(userID string, uuids []string) (total int64, balance int64, err error)
	// SkillProtect 批量保护/解保护。
	SkillProtect(userID string, uuids []string, protected bool) error
	// SkillPowerup 物品强化：消耗 powerup_item_id×count，level 按
	// 素材步长推进；品质档>3（level>=27.001）拒绝走物品强化。
	SkillPowerup(userID, uuid string, powerupItemID, count int) (newLevel float64, err error)
	// SkillComposite 符文合成：seed 符文消耗，基座品质需档 4+。
	SkillComposite(userID, uuid, seedUUID string) (newLevel float64, err error)
}

// ---- 品质/强化规则（gamerch 510636 + Skill.cs）----

// SkillMaxLevel 物品强化/显示上限（Skill.cs MAX_LEVEL=33）。
const SkillMaxLevel = 33.0

// SkillShopLevel 商店基础符文固定魔力（並品质下限：5.000）。
const SkillShopLevel = 5.0

// 品质阈值（levelTable）：<15 档1 並 / <22.5 档2 良 / <27 档3 特良 /
// <30 档4 極良 / <33 档5 極優 / <=35 超優 / 36 輝。
func SkillLevelCategory(level float64) int {
	switch {
	case level < 15:
		return 1
	case level < 22.5:
		return 2
	case level < 27:
		return 3
	case level < 30:
		return 4
	default:
		return 5
	}
}

// SkillCategoryName CN 品质名（UserSkills 品质筛选器文案；五档映射：
// 並/良→普通、特良→优秀、極良→精良、極優/超優→史诗、輝→传奇）。
func SkillCategoryName(level float64) string {
	switch {
	case level < 22.5:
		return "普通"
	case level < 27:
		return "优秀"
	case level < 30:
		return "精良"
	case level <= 35:
		return "史诗"
	default:
		return "传奇"
	}
}

// SkillSellPrice 出售入账价：floor(150*sqrt(lv/10)+100*max(lv-20,0)²)。
func SkillSellPrice(level float64) int64 {
	gain := 150 * math.Sqrt(level/10)
	if over := level - 20; over > 0 {
		gain += 100 * over * over
	}
	return int64(math.Floor(gain))
}

// SkillPowerupItemGain 物品强化步长：素材 content_id 1-15 =
// (炎/水/风/光/暗)×(种1-5/蕾6-10/花11-15)；档1 种+0.5/蕾+1.0/花+2.0，
// 档2 蕾+0.5/花+1.0，档3 花+0.5；档4+ 不可用（走合成）。
// 素材属性 = (content_id-1)%5+1 须与符文属性系一致（Skill.cs
// consumeItemIds 按 rune 属性选列）。attr=0 的数值/守护/特殊系符文
// 任意属性素材皆可（客户端对这类符文也按同规则陈列全部素材）。
// 返回步长与素材有效性。
func SkillPowerupItemGain(skillID int, level float64, powerupItemID int) (float64, bool) {
	if powerupItemID < 1 || powerupItemID > 15 {
		return 0, false
	}
	// 属性系符文（skill_id 4-8）只吃同属性素材：素材属性序号
	// (powerup_item_id-1)%5+1 须等于 skillID-3（炎1..暗5，Skill.cs
	// consumeItemIds 属性列）。非属性符文不限素材属性。
	if skillID >= 4 && skillID <= 8 && (powerupItemID-1)%5+1 != skillID-3 {
		return 0, false
	}
	cat := SkillLevelCategory(level)
	if cat > 3 {
		return 0, false
	}
	tier := (powerupItemID-1)/5 + 1 // 1种 2蕾 3花
	switch cat {
	case 1:
		switch tier {
		case 1:
			return 0.5, true
		case 2:
			return 1.0, true
		case 3:
			return 2.0, true
		}
	case 2:
		switch tier {
		case 2:
			return 0.5, true
		case 3:
			return 1.0, true
		}
	case 3:
		if tier == 3 {
			return 0.5, true
		}
	}
	return 0, false
}

// SkillCompositeGain 符文合成增益：(seed_level-26.5)/10，同 skill_id
// 或强化用（32）×2；基座極優（level>=30.001，档5）÷2。
func SkillCompositeGain(baseLevel, seedLevel float64, baseID, seedID int) float64 {
	gain := (seedLevel - 26.5) / 10
	if gain <= 0 {
		return 0
	}
	if seedID == baseID || seedID == 32 {
		gain *= 2
	}
	if SkillLevelCategory(baseLevel) >= 5 {
		gain /= 2
	}
	return gain
}

// ---- 战斗数值应用 ----

// attrRateKey skill_id→属性补正字段名（Skill.cs/Piece.cs 字段）。
var attrRateKey = map[int]string{
	4: "fire_rate", 5: "water_rate", 6: "wind_rate", 7: "light_rate", 8: "dark_rate",
}

// guardRateKey skill_id→属性守护字段名（默认 1，Piece.cs f(...,1)）。
var guardRateKey = map[int]string{
	11: "fire_guard_rate", 12: "water_guard_rate", 13: "wind_guard_rate",
	14: "light_guard_rate", 15: "dark_guard_rate",
}

func docF(doc map[string]any, key string, def float64) float64 {
	if v, ok := doc[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case int64:
			return float64(n)
		}
	}
	return def
}

// applyUnitSkills 把账号符文库存应用到一个 user_units/战斗单位文档：
//   - 槽位字段：skill_slot_count=1+min(dup,rare)，max_skill_slot_count=
//     1+rare（gamerch 槽位规则）；skills 为已装备符文 {uuid,skill_id}
//     列表——uuid 供 SkillWithUnitModal 按 cache 回放勾选态，skill_id
//     供 UserUnitDetail.SetIconRune 拼 "questdrop_skill_<id>" 精灵名；
//     rune_icon_name_<n> 供 UnitListRow 同伴列表行符文角标（装备槽
//     questdrop_skill_<id>、空槽 icon-runen_blank，超出槽位数不发）。
//   - 数值合入 Piece.cs 消费字段（hp/at/attack_ms/speed/taghness/
//     *_rate/*_guard_rate/behind_guard_rate 等），读面与战斗共用本函数，
//     保证列表显示值与实战值同口径。
//
// 未取证的字段缺键时按客户端默认值处理（guard 系默认 1、其余 rate 默认 0）；
// 士气/收集系（25,28-31）只影响探索收益，无 Piece 字段，此处不下发伪值。
func applyUnitSkills(doc map[string]any, rare, dup int, skills []PlayerSkill) {
	slotCap := 1 + dup
	if rare < dup {
		slotCap = 1 + rare
	}
	doc["skill_slot_count"] = slotCap
	doc["max_skill_slot_count"] = 1 + rare
	equipped := []any{}
	for _, s := range skills {
		if s.UnitID != nil {
			equipped = append(equipped, map[string]any{
				"uuid":      s.UUID,
				"skill_id":  s.SkillID,
				"level":     s.Level,
				"protected": s.Protected,
				"unit_id":   *s.UnitID,
			})
		}
	}
	doc["skills"] = equipped
	for i := 0; i < slotCap; i++ {
		name := "icon-runen_blank"
		if i < len(equipped) {
			name = fmt.Sprintf("questdrop_skill_%d", equipped[i].(map[string]any)["skill_id"])
		}
		doc[fmt.Sprintf("rune_icon_name_%d", i+1)] = name
	}
	// 韧性符文先于根性结算（endure 概率依赖 taghness 终值）。
	for _, s := range skills {
		if s.UnitID == nil {
			continue
		}
		if s.SkillID == 10 {
			doc["taghness"] = docF(doc, "taghness", 0) + s.Level
		}
	}
	for _, s := range skills {
		if s.UnitID == nil {
			continue
		}
		lv := s.Level
		switch s.SkillID {
		case 1:
			doc["hp"] = int(math.Round(docF(doc, "hp", 0) * (1 + lv/100)))
		case 2:
			doc["at"] = int(math.Round(docF(doc, "at", 0) * (1 + lv/100)))
		case 3:
			doc["attack_ms"] = int(math.Round(docF(doc, "attack_ms", 0) * (1 - lv/100)))
		case 9:
			doc["speed"] = docF(doc, "speed", 0) * (1 + lv/100)
		case 16:
			doc["behind_guard_rate"] = docF(doc, "behind_guard_rate", 1) * (1 - 2*lv/100)
		case 17:
			doc["penetrate_rate"] = docF(doc, "penetrate_rate", 0) + lv/100
		case 18:
			doc["critical_rate"] = docF(doc, "critical_rate", 0) + lv/100
		case 19:
			doc["healing_rate"] = docF(doc, "healing_rate", 0) + lv*0.04
		case 20:
			doc["berserk_rate"] = docF(doc, "berserk_rate", 0) + lv/100
		case 21:
			for _, k := range []string{"fire_guard_rate", "water_guard_rate",
				"wind_guard_rate", "light_guard_rate", "dark_guard_rate"} {
				doc[k] = docF(doc, k, 1) * (1 - lv/200)
			}
		case 22:
			doc["sorcery_rate"] = docF(doc, "sorcery_rate", 0) + lv/400
		case 23:
			doc["dodge_rate"] = docF(doc, "dodge_rate", 0) + lv/100
		case 24:
			t := docF(doc, "taghness", 0)
			if p := (t / 100) * (t / 100) * lv * 0.03; p > 0 {
				doc["endure_rate"] = math.Min(1, docF(doc, "endure_rate", 0)+p)
			}
		case 27:
			doc["revive_rate"] = docF(doc, "revive_rate", 0) + lv/100
			doc["revive_rate_2"] = docF(doc, "revive_rate_2", 0) + lv*lv/2000
		default:
			if k, ok := attrRateKey[s.SkillID]; ok {
				doc[k] = docF(doc, k, 0) * (1 + lv/100)
			}
			if k, ok := guardRateKey[s.SkillID]; ok {
				doc[k] = docF(doc, k, 1) * (1 - lv/100)
			}
		}
	}
}

// SkillSlotCap 槽位上限：1+min(觉醒数,稀有度)。
func SkillSlotCap(rare, dup int) int {
	if dup > rare {
		return 1 + rare
	}
	return 1 + dup
}

// validateSkillEquip 装备前校验集合（事务外可复算的规则部分）。
func validateSkillEquip(sk *PlayerSkill, def SkillDef, weaponID int) error {
	if !def.Equipable {
		return fmt.Errorf("%w: skill_id=%d", ErrSkillNotEquipable, sk.SkillID)
	}
	if def.WeaponID != 0 && def.WeaponID != weaponID {
		return fmt.Errorf("%w: skill_id=%d unit_weapon=%d",
			ErrSkillWeaponMismatch, sk.SkillID, weaponID)
	}
	return nil
}

// CollectRuneFactors 收益系符文乘算因子：经验（skill_id 28）与金币
// （skill_id 29）各自按参战单位装备的同类符文乘算
// （wiki：最终值 = 原值 × ∏(1+魔力值/650)，多件パーティイン乘算）。
// 供开战快照（OpenBattleForParty）与 execute 显示面（exp 字段、
// appear_table 金币掉落条）同口径复用。
func CollectRuneFactors(skills []PlayerSkill) (expFactor, goldFactor float64) {
	expFactor, goldFactor = 1.0, 1.0
	for _, sk := range skills {
		switch sk.SkillID {
		case 28:
			expFactor *= 1 + sk.Level/650
		case 29:
			goldFactor *= 1 + sk.Level/650
		}
	}
	return expFactor, goldFactor
}

// ApplyCollectRunes 收益系符文（コレクト系）在开战快照冻结时调整会话收益：
// skill_id 28 经验符文/29 金币符文，各自按参战单位装备的同类符文乘算
// （wiki：最终值 = 原值 × ∏(1+魔力值/650)，多件パーティイン乘算）。
// 金币即 gacha_point 奖励行。30 符文收集走符文本掉落乘算
// （rune_hunt.go RuneCollectFactor），31 魔宠收集无服务端掉落通路
// （シード確率不实现），二者均不上架、不参与此函数。
// 返回是否改动了会话（用于决定是否重写 rewards 快照）。
func ApplyCollectRunes(sess *BattleSession, skills []PlayerSkill) bool {
	expFactor, goldFactor := CollectRuneFactors(skills)
	changed := false
	if expFactor > 1 && sess.Exp > 0 {
		sess.Exp = int(math.Round(float64(sess.Exp) * expFactor))
		changed = true
	}
	if goldFactor > 1 {
		for i := range sess.Rewards {
			if sess.Rewards[i].Key == "gacha_point" {
				sess.Rewards[i].Count = int(math.Round(float64(sess.Rewards[i].Count) * goldFactor))
				changed = true
			}
		}
	}
	return changed
}
