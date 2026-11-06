// inventory_wire.go PLAY350-07：账号库存（InventoryStore）+ 模板数值
// （cardcatalog）→ user_units/user_monsters 生产形态字段映射。
//
// 证据基线：handoffs/03/inventory-contract.md（3.5 消费代码）+
// assets/shared/reference/serve_quest_execute_1.json 内嵌生产记录（id=152 单位、
// id=1 魔宠模板）。要点：
//   - id/leader_unit_id 等比较字段发 JSON NUMBER（i() 对 STRING 恒 0）。
//   - rare_type_id=rare*10、rare_type_count=rare、rare_type_name="╂"×rare、
//     attack_ms=aspd*1000、asset_name="unit/unit_{id}"（生产记录实测）。
//   - country_id 必须下发 JSON NUMBER：客户端 Unit.CalcCountrySpBonusRate
//     以 i() 读 country_id 统计同国人数加成初始 SP（i() 对 STRING 恒 0）；
//     Enum(key,id) 做 id→name 时两侧经 s() 比较，数值 id 亦可命中。
//     已知国家采用客户端筛选配置的 ID，其他国家为重建值；同包下发枚举。
//   - sex_id 仅经 cache.Enum 做 id→name：以 gender 数字串为 id 下发，
//     同包附 sex 枚举行。
//   - cost=100*rare【推断·重建策略】：生产样本 ★5 单位 152 cost=500
//     （召唤扣 cost，非原服逐位值）。
//   - bonus_sp=20*rare*(dup+5)【实机证据】：招募结果页 SP加成 栏
//     ★4 dup0=400/dup15=1600、★2 dup15=800（用户验收截图），即
//     基础 100*rare + 每觉醒 +20*rare。客户端 PowerupButton 按
//     bonus_sp×同国加成率计入初始 SP——读面与战斗同口径。
//   - max_level：仅 rare5=70 有生产证据（同 fixture）；其余星级按
//     10*rare+20 插值（未证，显示用）。
//   - uuid 为单位实例键：客户端 Quest.AddUnit/Unit.cs:267 以它作 unitMap
//     键（编队上场、死亡移除都按 uuid 寻址），缺键全员撞 "" 只能派出一位；
//     由 unit_id 确定性派生（详见 unitInstanceUUID 注释）。
//   - 战斗行为字段（act_type_id/act_target_id/hit_frame/multi_hit_count 等）
//     来自内嵌 unit_battle_behavior.json：官方样本覆盖 + 按武器重建默认，
//     祭司暂按普通攻击（用户决策 2026-09-28）；缺省单位 Idle 不出手。
//   - 缺字段省略键（不填零）：skill 等 catalog 未建模值不下发。
package cn35game

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

// attrName element→中文属性名（README 枚举：1炎2水3风4光5暗）。
var attrName = map[int]string{1: "炎", 2: "水", 3: "风", 4: "光", 5: "暗"}

// genderName sex_id→显示名（真实国服枚举：1男 2女 3不明）。
var genderName = map[int]string{1: "男", 2: "女", 3: "不明"}

// wikiGenderToSexID catalog gender（1不明 2男 3女）→ 国服 sex_id（1男 2女 3不明）。
func wikiGenderToSexID(g int) int {
	switch g {
	case 2:
		return 1
	case 3:
		return 2
	default:
		return 3
	}
}

func unitMaxUpgradeLevel(rare int) int {
	switch rare {
	case 2:
		return 90
	case 3:
		return 100
	case 4:
		return 110
	case 5:
		return 145
	default:
		return 0
	}
}

func unitMaxPassiveSlotCount(rare int) int {
	switch rare {
	case 1:
		return 1
	case 2:
		return 2
	case 3:
		return 3
	case 4:
		return 4
	case 5:
		return 6
	default:
		return 1
	}
}

func unitPassiveSlotStartLevel(rare int) int {
	switch rare {
	case 1:
		return 80
	case 2:
		return 60
	case 3:
		return 40
	case 4:
		return 20
	case 5:
		return 15
	default:
		return 80
	}
}

func unitSellPrice(rare, dup int) int {
	base := 100
	switch rare {
	case 2:
		base = 500
	case 3:
		base = 5000
	case 4:
		base = 20000
	case 5:
		base = 50000
	}
	if dup < 0 {
		dup = 0
	}
	return base * (4 + 3*dup) / 4
}

// growthTypeName catalog type→成长类型名。生产证据：152 type=3↔晩成
// （serve_quest_execute_1.json hp_growth_type_name）；1早熟/2平均为 MLK
// 三段式常识推断（未证，显示用）。
var growthTypeName = map[int]string{1: "早熟", 2: "平均", 3: "晩成"}

// weaponTypeName weapon_id→武器类型名【事实】：serve_app_conf_android.json
// 同伴筛选器 options 逐值（"请选择要筛选的武器类型"：斩击1/突击2/打击3/
// 远程4/魔法5/枪弹6/治疗7）。客户端 UserUnitDetail.cs:327 经
// cache.Enum("weapon", weapon_id) 映射显示名——同包须下发 weapon 枚举行。
var weaponTypeName = map[int]string{
	1: "斩击", 2: "突击", 3: "打击", 4: "远程", 5: "魔法", 6: "枪弹", 7: "治疗",
}

// buildUnitDoc 组装一条 user_units 行：实例态（库存）+ 模板面（catalog）。
// 模板缺失 → 调用方先查 catalog 报错；实例态越界（dup 不在 [0,max]）→ 返回
// 明确错误（不静默截断，C0.1）。
//
// 数值面（statmodel）：hp/at 是该单位「当前等级+觉醒」下的面板终值，客户端
// 直接读进战斗、不做等级换算（Piece.cs:716-717、PieceParameter.cs:93）；
// 同时下发成长区间（hp_min/hp_max/at_min/at_max/growth_type_id/base_max_level）
// 与满觉端点（hp_15_max/at_15_max，0 时客户端显示空白）供升级弹窗预览。
//
// max_level 语义（C0.2 冻结）：= current_max_level = base_max_level + dup×5。
// 证据：UnitListRow.IsTrainingable()/UnitLevelupModal.CanLevelup() 用它作为
// 正常等级训练的上界（UnitListRow.cs:340-343、UnitLevelupModal.cs:94,208-219）；
// 若取 base_max_level，dup=15 的实例在 Lv70 即被判满级，与同文档下发的
// max_duplicated_count/hp_15_max 自相矛盾。未证：原服逐位一致；替换点即此行。
//
// 战斗行为字段（act_type_id/act_target_id/hit_frame/multi_hit_count 等）
// 由 applyUnitBattleBehavior 按 unit_battle_behavior.json 下发：官方样本
// 覆盖 + 按武器重建默认，祭司暂按普通攻击（用户决策 2026-09-28）。
//
// 字段语义与证据见 docs/策划案/CN3.5数值体系.md §3/§4/§11.2。
func buildUnitDoc(rec UnitRecord, u *cardcatalog.Unit) (map[string]any, error) {
	// 纪念服覆盖层（memorial_masterdata.json unit_text[<id>] 的 *_override 键）：
	// 命中的字段压过国服 catalog，用于「纪念服已汉化名称/称号/简介/档案」与
	// 「以纪念服满觉满级终值反解出的 Lv1 基础值」。未命中则完全沿用 catalog。
	ut := memorialUnitText(u.ID)
	name, title := u.Name, u.Title
	if v, ok := memorialUnitTextOverride(u.ID, "main_name"); ok {
		name = v
	}
	if v, ok := memorialUnitTextOverride(u.ID, "prefix_name"); ok {
		title = v
	}
	growthType, hpMin, atMin := u.Type, u.Life, u.Atk
	if v, ok := memorialUnitIntOverride(u.ID, "type_override"); ok {
		growthType = v
	}
	if v, ok := memorialUnitIntOverride(u.ID, "life_override"); ok {
		hpMin = v
	}
	if v, ok := memorialUnitIntOverride(u.ID, "atk_override"); ok {
		atMin = v
	}
	dispName := name
	if title != "" {
		// 称号+名：Title 自带方括号，直接拼接。客户端 DecordReturn 不解 \[
		// 转义（Document.cs），反斜杠会原样上屏（09 实机验收发现）。
		dispName = title + name
	}
	panel, err := statmodel.ComputeUnitPanel(statmodel.UnitInput{
		Type:            growthType,          // units.json.type 与 growth_type_id 同序（type=3↔晩成）
		Rare:            u.Rare,              // 决定 base_max_level 与觉醒上限
		HpMin:           hpMin,               // life ↔ hp_min（Lv1 基础值）
		AtMin:           atMin,               // atk ↔ at_min
		Level:           rec.Level,           // 实例态：当前等级
		DuplicatedCount: rec.DuplicatedCount, // 实例态：已觉醒次数
	})
	if err != nil {
		return nil, fmt.Errorf("cn35 单位 %d 实例态非法（level=%d dup=%d）: %w",
			rec.UnitID, rec.Level, rec.DuplicatedCount, err)
	}
	doc := map[string]any{
		"id": rec.UnitID,
		// uuid：客户端 Quest.AddUnit/Unit.cs:267 以它为 unitMap 键寻址同伴，
		// 缺失时全员键撞 ""，战斗中只能派出第一位。
		"uuid": unitInstanceUUID(rec.UnitID),
		"name": dispName,
		// prefix_name 是纯称号文字：剥掉 Title 中全部 [] 括号字符。
		// 不能 strings.Trim("[]")——它只削两端，❤/㈱/㊥ 前缀挡住左括号
		// 时会留下裸 "["（尾部 "]" 已被削），字符串内不平衡括号会击穿
		// 客户端 JSONObject 解析（gacha350 实机 Now-Loading 事故）。
		"prefix_name": strings.NewReplacer("[", "", "]", "").Replace(title),
		"main_name":   name,
		"level":       rec.Level,
		// max_level = 当前觉醒上限（C0.2）。避免与 base_max_level 混用。
		"max_level": panel.CurrentMaxLevel,
		"hp":        panel.HP, // 面板终值（非 Lv1 基础值）
		"at":        panel.At, // 面板终值
		// 成长区间 + 满觉端点：升级弹窗预览消费（UnitLevelupModal/AdvancedUnitLevelModal）。
		"hp_min":               panel.HpMin,
		"hp_max":               panel.HpMax,
		"at_min":               panel.AtMin,
		"at_max":               panel.AtMax,
		"growth_type_id":       panel.GrowthTypeID,
		"base_max_level":       panel.BaseMaxLevel,
		"max_duplicated_count": panel.MaxDuplicatedCount,
		"hp_15_max":            panel.HP15Max,
		"at_15_max":            panel.At15Max,
		"rare_type_id":         u.Rare * 10,
		"rare_type_count":      u.Rare,
		"rare_type_name":       strings.Repeat("╂", u.Rare),
		// SP/召唤经济字段：cost=100*rare（生产样本 ★5=500，重建策略）；
		// bonus_sp=100*rare+20*rare*dup（招募结果页实机证据，见文件头）。
		// 客户端 UnitControllerPanel 按 cost 扣召唤/士气升级 SP，
		// PowerupButton 按 bonus_sp×同国加成率计入初始 SP。
		"cost":             100 * u.Rare,
		"bonus_sp":         20 * u.Rare * (rec.DuplicatedCount + 5),
		"attr_id":          u.Element,
		"attr_name":        attrName[u.Element],
		"weapon_id":        u.Weapon,
		"aarea":            u.AArea,
		"reach":            u.AArea,
		"duplicated_count": rec.DuplicatedCount,
		// can_duplicate：觉醒书 boost 选人按钮可用判定
		// （UserUnitsListRow/UseClick→Stack select 读 document.b("can_duplicate")，
		// false 弹“好感度招募角色无法使用觉醒之书”）。
		"can_duplicate": rec.DuplicatedCount < panel.MaxDuplicatedCount,
		"protected":     rec.Protected,
		"newcomer":      rec.Newcomer,
		// 潜能与成长/槽位基线字段（对齐真实国服录包契约）。
		"upgrade_level":                  1,
		"max_upgrade_level":              unitMaxUpgradeLevel(u.Rare),
		"skill_slot_count":               SkillSlotCap(u.Rare, rec.DuplicatedCount),
		"max_skill_slot_count":           1 + u.Rare,
		"passive_skill_slot_count":       0,
		"max_passive_skill_slot_count":   unitMaxPassiveSlotCount(u.Rare),
		"passive_skill_slot_start_level": unitPassiveSlotStartLevel(u.Rare),
		// 战斗演算与抗性/特殊词条默认值（applyUnitSkills 在符文佩戴时累加/改写）。
		"quick":             1,
		"fire_guard_rate":   1,
		"water_guard_rate":  1,
		"wind_guard_rate":   1,
		"light_guard_rate":  1,
		"dark_guard_rate":   1,
		"behind_guard_rate": 1,
		"penetrate_rate":    0,
		"critical_rate":     0,
		"healing_rate":      0,
		"dodge_rate":        0,
		"endure_rate":       0,
		"guts_rate_2":       0,
		"guts_rate_3":       0,
		"sorcery_rate":      0,
		"sorcery_rate_2":    0,
		"revive_rate":       0,
		"revive_rate_2":     0,
		"fixate_rate":       0,
		"berserk_rate":      0,
		// 展示与其他协议必需字段。
		"price":                      unitSellPrice(u.Rare, rec.DuplicatedCount),
		"bar_offset_y":               45,
		"cutin_x":                    -80.0,
		"cutin_y":                    -120.0,
		"cutin_scale":                1.3,
		"effect_x":                   0.0,
		"effect_y":                   0.0,
		"bonus_sp_add":               0,
		"created_at":                 1509018011,
		"monster_id":                 0,
		"monster_scale":              1.0,
		"story_id":                   nil,
		"is_evolutable":              false,
		"evolution_map":              map[string]any{"id": 0, "need": map[string]any{}},
		"evolution_map_desc":         map[string]any{},
		"monster_uuid":               rec.MonsterUUID,
		"monster_scale_category_str": "标准",
		"form_rank":                  unitFormRank(u.AArea),
		"skills":                     []any{},
		"passive_skills":             []any{},
		"talent_skill":               map[string]any{},
		"truelove_info":              map[string]any{"in_battle": false, "is_truelove": false},
		"asset_name":                 fmt.Sprintf("unit/unit_%d", u.ID),
		"icon_asset_name":            fmt.Sprintf("unit/unit_square_%d", u.ID),
		"rectangle_icon_asset_name":  fmt.Sprintf("unit/unit_rect_%d", u.ID),
		"unit_detail_message": fmt.Sprintf(
			"name=UserUnitDetail&base=Common/Pages/UserUnitDetail&method=Stack&id=%d", u.ID),
	}
	if v, ok := ut["reach"].(float64); ok && v > 0 {
		doc["reach"] = v
	}
	if v, ok := ut["speed"].(float64); ok && v > 0 {
		doc["speed"] = v
	} else if u.MSpd != nil {
		doc["speed"] = *u.MSpd
	}
	if v, ok := ut["attack_ms"].(float64); ok && v > 0 {
		doc["attack_ms"] = int(v)
	} else if u.ASPd != nil {
		doc["attack_ms"] = int(*u.ASPd * 1000)
	}
	if v, ok := ut["taghness"].(float64); ok && v > 0 {
		doc["taghness"] = v
	} else if u.Tenacity != nil {
		doc["taghness"] = *u.Tenacity
	}
	if v, ok := ut["attackable_count"].(float64); ok && v > 0 {
		doc["attackable_count"] = int(v)
	} else if u.ANum != nil {
		doc["attackable_count"] = *u.ANum
	}
	if u.Hits != nil {
		doc["hits"] = *u.Hits
	}
	setRate(doc, "fire_rate", u.Rates.Fire)
	setRate(doc, "water_rate", u.Rates.Aqua)
	setRate(doc, "wind_rate", u.Rates.Wind)
	setRate(doc, "light_rate", u.Rates.Light)
	setRate(doc, "dark_rate", u.Rates.Dark)
	for _, rk := range []string{"fire_rate", "water_rate", "wind_rate", "light_rate", "dark_rate"} {
		if v, ok := ut[rk].(float64); ok && v > 0 {
			doc[rk] = v
		} else if _, has := doc[rk]; !has {
			doc[rk] = 1.0
		}
	}
	if v, ok := ut["age"]; ok && v != nil {
		if f, fok := v.(float64); fok {
			doc["age"] = int(f)
		} else {
			doc["age"] = v
		}
	} else if u.Age != nil {
		if n, aerr := strconv.Atoi(strings.TrimSpace(*u.Age)); aerr == nil {
			doc["age"] = n
		} else {
			doc["age"] = *u.Age
		}
	} else {
		doc["age"] = "??"
	}
	if v, ok := ut["age_sort"].(float64); ok && v > 0 {
		doc["age_sort"] = int(v)
	} else if ai, ok := doc["age"].(int); ok && ai > 0 {
		doc["age_sort"] = ai
	} else {
		doc["age_sort"] = 999
	}
	if v, ok := ut["sex_id"].(float64); ok && v > 0 {
		doc["sex_id"] = int(v)
	} else if u.Gender != nil {
		doc["sex_id"] = wikiGenderToSexID(*u.Gender)
	} else {
		doc["sex_id"] = 3
	}
	if v, ok := ut["story_id"].(float64); ok && v > 0 {
		doc["story_id"] = int(v)
	}
	if n, ok := growthTypeName[growthType]; ok {
		doc["hp_growth_type_name"] = n
	}
	if u.Country != "" && u.CountryID > 0 {
		// country_id 发 NUMBER（同国 SP 加成按 i() 计数，STRING 恒 0）；
		// 显示名经同包 country 枚举行 {id:数值,name:中文名} 命中。
		doc["country_id"] = u.CountryID
	}
	// 介绍/档案文本：纪念版作为根本来源优先，缺则沿用 catalog。
	if w, ok := ut["weapon_label"].(string); ok && w != "" {
		doc["weapon"] = w
	}
	career := u.Career
	if v, ok := memorialUnitTextOverride(u.ID, "profession_override"); ok {
		career = v
	} else if career == "" {
		career, _ = ut["profession"].(string)
	}
	doc["profession"] = career
	interest := u.Interest
	if v, ok := memorialUnitTextOverride(u.ID, "favorite_override"); ok {
		interest = v
	} else if interest == "" {
		interest, _ = ut["favorite"].(string)
	}
	doc["favorite"] = interest
	nature := u.Nature
	if v, ok := memorialUnitTextOverride(u.ID, "personality_override"); ok {
		nature = v
	} else if nature == "" {
		nature, _ = ut["personality"].(string)
	}
	doc["personality"] = nature
	remark := u.Remark
	if v, ok := memorialUnitTextOverride(u.ID, "description_override"); ok {
		remark = v
	} else if remark == "" {
		remark, _ = ut["description"].(string)
	}
	doc["description"] = remark
	if u.Obtain != "" {
		doc["obtain"] = u.Obtain
	}
	// 战斗行为字段（act_type_id/act_target_id/hit_frame/multi_hit_count/
	// attack_range 与特效键）：客户端 BaseAI/TriggerRange 依其驱动出手，
	// 缺省 act_target_id="" 时 targets 恒空、单位永远 Idle。
	applyUnitBattleBehavior(doc, u)
	if doc["act_type_id"] == "heal" {
		doc["act_type_name"] = "治疗"
	} else {
		doc["act_type_name"] = "普通攻击"
	}
	for k, v := range map[string]any{
		"attack_effect_multi":      false,
		"attack_effect_second_key": "",
		"attack_stage_effect":      "",
		"target_effect_ground":     false,
		"target_effect_multi":      false,
		"target_effect_second_key": "",
	} {
		if _, has := doc[k]; !has {
			doc[k] = v
		}
	}
	if v, ok := ut["effect_x"].(float64); ok {
		doc["effect_x"] = v
	}
	if v, ok := ut["effect_y"].(float64); ok {
		doc["effect_y"] = v
	}
	return doc, nil
}

func unitFormRank(aarea int) string {
	switch {
	case aarea <= 50:
		return "前排"
	case aarea <= 150:
		return "中排"
	default:
		return "后排"
	}
}

// unitInstanceUUID 单位实例 uuid 派生：cn35_player_units 主键为
// (user_id, unit_id)，同账号每 unit_id 仅一行，故由 unit_id 确定性派生
// 即可保证账号内唯一且跨请求稳定。
// 【重建策略】"unit-<id>" 非原服 uuid 格式，仅保证唯一性与稳定性。
func unitInstanceUUID(unitID int) string {
	return fmt.Sprintf("unit-%d", unitID)
}

// buildMonsterDoc 组装一条 user_monsters 行。
//
// Size 面（STAT350-04，C0.3）：
//   - `scale`（展示用）与 `real_scale`（推进式计算用，客户端预览同口径）分别
//     取库存值（十进制字符串原样下发）；面板 hp/at/seed_at 用 real_scale 计算
//     （回退顺序 real_scale → scale → 1.000）。
//   - 库存无值（NULL）时为「显式 Size=1.000 降级」：仍按 1.000 计算面板，
//     缺口在 07 验收矩阵登记；不写伪值、不发伪布尔。
//   - `scale_limit`/`is_rare` 未证（NULL）时**省略键**，不填 0/false 伪造。
//   - 客户端消费：Monster.GetScaleCategory（scale/is_rare/scale_limit/real_scale）、
//     MonsterPowerupModal.InitDisplay（hp/seed_at）、UpdateLabel（base_hp/base_seed_at）。
// MonsterBatch 返回魔宠的同批排重标记（对齐国服 2017-11 抓包 guilds_monsters.json / preset_data.json）：
//   - 普通魔宠返回 -1（客户端 checkSelectMonsterLimit 对 batch < 0 直接放行，允许防卫槽同时配置不同普通魔宠）；
//   - 五系融合培育稀有魔宠（培育方式：X之蛋+Y，id 100005..100045）按系列返回固定非负批次号，
//     客户端 GuildMonsterButtonsListComponent / GuildDeckButtonsListComponent 据此拦截同系列多只上阵（「不能设定 两样的魔宠」）。
func MonsterBatch(monsterID int) int {
	switch {
	case monsterID >= 100005 && monsterID <= 100008: // 摩布拉（水之蛋+飞拉）
		return 100
	case monsterID >= 100009 && monsterID <= 100012: // 赫黎安提斯（风之蛋+皮卡）
		return 2
	case monsterID >= 100018 && monsterID <= 100021: // 洛菲阿斯（暗之蛋+司坎德）
		return 3
	case monsterID >= 100038 && monsterID <= 100041: // 黛尼斯托（火之蛋+诺滋风）
		return 1
	case monsterID >= 100042 && monsterID <= 100045: // 梅赫莲恩（光之蛋+针锋）
		return 4
	default:
		return -1
	}
}

func buildMonsterDoc(rec MonsterRecord, m *cardcatalog.Monster) (map[string]any, error) {
	// 有效 Size：展示用与推进用分别回退，最后落到显式降级值 1.000。
	// 实机证据（2026-09-30）：客户端 Document.dec/f 按数值解析，字符串态
	// 会 FormatException 并令强化按钮无响应——必须以 JSON 数值下发。
	_, displayScaleF, err := effectiveScale(rec.Scale, rec.RealScale)
	if err != nil {
		return nil, fmt.Errorf("cn35 魔宠 %s scale 非法: %w", rec.UUID, err)
	}
	_, progScale, err := effectiveScale(rec.RealScale, rec.Scale)
	if err != nil {
		return nil, fmt.Errorf("cn35 魔宠 %s real_scale 非法: %w", rec.UUID, err)
	}
	batch := MonsterBatch(rec.MonsterID)
	formulaID := 0
	if batch >= 0 {
		formulaID = 1
	}
	doc := map[string]any{
		"uuid":             rec.UUID,
		"id":               rec.MonsterID,
		"monster_id":       rec.MonsterID,
		"user_id":          "",
		"name":             m.Name,
		"level":            rec.Level,
		"max_level":        99, // 魔宠等级上限：无生产证据，UI 显示用（未证）
		"rare_type_id":     m.Rare * 10,
		"rare_type_count":  m.Rare,
		"rare_type_name":   strings.Repeat("╂", m.Rare),
		"attr_id":          m.Element,
		"attr_name":        attrName[m.Element],
		"skin_id":          m.Skin,
		"type_id":          m.Skin,
		"category_str":     "普通",
		"batch":            batch,
		"formula_id":       formulaID,
		"hardness_id":      2,
		"price":            m.Rare * 100,
		"skill_sort_order": 0,
		"sklsp":            m.SklSP,
		// 种子技 SP 消耗：客户端 SeedControllerPanel.onInit 读 cost 扣 SP
		// （生产样本魔宠 1 sklsp=250 ↔ cost=250 同值锚定）。
		"cost":                 m.SklSP,
		"protected":            rec.Protected,
		"asset_name":           fmt.Sprintf("monster/monster_%d", m.ID),
		"seed_skill_id":        0, // 占位；下方按模板 skill 文本解析覆盖
		"seed_skill_value":     0.0,
		"seed_skill_value_20":  0.0,
		"seed_skill_value_30":  0.0,
		"seed_skill_value_35":  0.0,
		"seed_skill_value2":    0.0,
		"seed_skill_value2_20": 0.0,
		"seed_skill_value2_30": 0.0,
		"seed_skill_value2_35": 0.0,
		// Size 展示值：库存值换算为数值下发（客户端按 dec/f 解析，非字符串），
		// 缺省为显式降级 1.000。
		"scale":      displayScaleF,
		"real_scale": progScale,
		// 行为基线字段（对齐真实抓包 user_monsters 行）。
		"act_type_id":              "normal",
		"act_target_id":            "reachable_units",
		"attack_range":             30.0,
		"hit_frame":                20,
		"multi_hit_count":          1,
		"multi_hit_interval":       0.0,
		"attack_se":                "se_attack_strike",
		"attack_effect":            "",
		"attack_effect_x":          0.0,
		"attack_effect_y":          0.0,
		"attack_effect_multi":      false,
		"attack_effect_second_key": "",
		"attack_stage_effect":      "",
		"target_effect":            "effects/effect_hit_slash",
		"target_effect_delay":      0.0,
		"target_effect_ground":     false,
		"target_effect_multi":      false,
		"target_effect_second_key": "",
		"effect_x":                 0.0,
		"effect_y":                 0.0,
		"front_offset_x_base":      0.0,
		"offset_y_base":            0.0,
		"knockback_damage_rate":    0.1,
		"knockback_local_x":        55.0,
		"run_b_frame1":             0,
		"run_b_frame2":             nil,
		"run_e_frame1":             0,
		"run_e_frame2":             nil,
		"damage_xys":               []any{},
		"act_skill_config": map[string]any{
			"call_atk_unit_count": 0,
			"call_damge_delay":    0,
			"call_p1":             0,
			"call_p2":             0,
			"call_p3":             0,
			"call_p4":             0,
			"call_round":          0,
			"call_round_interval": 0,
			"trigger_type":        0,
			"trigger_val":         0,
			"type":                0,
			"warning_time":        0,
		},
	}
	// scale_limit/is_rare 未证时省略键（不填伪值）。
	if rec.ScaleLimit != nil {
		limitF, lerr := strconv.ParseFloat(strings.TrimSpace(*rec.ScaleLimit), 64)
		if lerr != nil {
			return nil, fmt.Errorf("cn35 魔宠 %s scale_limit 非法: %w", rec.UUID, lerr)
		}
		doc["scale_limit"] = limitF
	}
	if rec.IsRare != nil {
		doc["is_rare"] = *rec.IsRare
	}
	// 介绍/出没地：国服真实录包中文优先，其次 CN catalog，缺则纪念版补缺。
	mt := memorialMonsterText(m.ID)
	if n, ok := mt["name"].(string); ok && n != "" {
		doc["name"] = n
	}
	if d, ok := mt["description"].(string); ok && d != "" {
		doc["description"] = d
	} else if m.Remark != "" {
		doc["description"] = m.Remark
	}
	if o, ok := mt["obtain"].(string); ok && o != "" {
		doc["appear_stage_names"] = o
	} else if m.Obtain != "" {
		doc["appear_stage_names"] = m.Obtain
	}
	if m.Skill != "" {
		doc["skill"] = m.Skill
	}
	// 种子技：客户端按 user_monsters 行自算全部效果（SWFQuestCutin
	// ShowCutin/ShowAttack 读 seed_skill_id/value/value2）；id 由模板
	// skill 文本的技能名解析，效果值取文本实数或占位默认表
	// （PROPOSED，见 cn35game/monster_seed.go）。id=0=无种子技。
	if skID, skV, skV2 := MonsterSeedSkill(m); skID != 0 {
		doc["seed_skill_id"] = skID
		doc["seed_skill_value"] = skV
		doc["seed_skill_value2"] = skV2
	}
	// 召唤 cutin 构图默认（生产样本：cutin_x=-90 cutin_y=-100
	// cutin_scale=1.3；未取证逐怪差分前按该默认下发）。
	doc["cutin_x"] = -90
	doc["cutin_y"] = -100
	doc["cutin_scale"] = 1.3
	if rec.UnitID != nil {
		doc["unit_id"] = *rec.UnitID
	}
	// 数值：纪念版主数据优先（根本来源），缺口回退 catalog。
	mfill := memorialMonsterFill(m.ID)
	mentry, mcovered := memorialMonsterEntry(m.ID)
	if m.SklCD != nil {
		doc["sklcd"] = *m.SklCD
		doc["call_interval_ms"] = int(float64(float32(*m.SklCD)) * 1000)
	} else if v, ok := mfill["sklcd"]; ok {
		doc["sklcd"] = v
		doc["call_interval_ms"] = mentry["call_interval_ms"]
	} else if mcovered && mentry["call_interval_ms"] != nil {
		doc["call_interval_ms"] = mentry["call_interval_ms"]
	}
	if mcovered && mentry["cost"] != nil {
		doc["cost"], doc["sklsp"] = mentry["cost"], mentry["cost"]
	} else if m.SklSP == 0 {
		if v, ok := mfill["cost"]; ok {
			doc["cost"], doc["sklsp"] = v, v
		}
	}
	if m.SArea != nil {
		doc["sarea"] = *m.SArea
	}
	if mcovered && mentry["speed"] != nil {
		doc["speed"] = mentry["speed"]
	} else if m.MSpd != nil && *m.MSpd > 0 {
		doc["speed"] = *m.MSpd
	} else if v, ok := mfill["speed"]; ok {
		doc["speed"] = v
	}
	if mcovered && mentry["attack_ms"] != nil {
		doc["attack_ms"] = mentry["attack_ms"]
	} else if m.ASPd != nil && *m.ASPd > 0 && *m.ASPd <= 10 {
		doc["attack_ms"] = int(*m.ASPd * 1000)
	} else if v, ok := mfill["attack_ms"]; ok {
		doc["attack_ms"] = v
	}
	if mcovered {
		doc["reach"] = mentry["reach"]
	} else if m.AArea != nil {
		doc["reach"] = *m.AArea
	}
	if mcovered && mentry["taghness"] != nil {
		doc["taghness"] = mentry["taghness"]
	} else if m.Tenacity != nil && *m.Tenacity > 0 {
		doc["taghness"] = *m.Tenacity
	} else if v, ok := mfill["taghness"]; ok {
		doc["taghness"] = v
	}
	if mcovered && mentry["attackable_count"] != nil {
		doc["attackable_count"] = mentry["attackable_count"]
	} else if m.ANum != nil && *m.ANum >= 1 {
		doc["attackable_count"] = *m.ANum
	} else if v, ok := mfill["attackable_count"]; ok {
		doc["attackable_count"] = v
	}
	if mcovered && mentry["base_scale"] != nil {
		doc["base_scale"] = mentry["base_scale"]
	} else {
		doc["base_scale"] = 1.0
	}
	if mcovered {
		for _, k := range []string{
			"attack_range", "hit_frame", "multi_hit_count", "multi_hit_interval",
			"attack_se", "attack_effect", "attack_effect_x", "attack_effect_y",
			"target_effect", "target_effect_delay", "effect_x", "effect_y",
			"front_offset_x_base", "offset_y_base", "run_b_frame1", "run_e_frame1",
		} {
			if v, ok := mentry[k]; ok && v != nil {
				doc[k] = v
			}
		}
	}
	// 魔宠 Size 面板：纪念版 base_hp/base_seed_at 优先（218/218 零误差），缺口回退 catalog。
	if v, ok := mentry["base_hp"].(float64); ok && v > 0 {
		base := int(v)
		doc["base_hp"] = base
		doc["hp"] = statmodel.MonsterHP(base, progScale)
	} else if m.Life != nil && *m.Life > 0 {
		doc["base_hp"] = *m.Life
		doc["hp"] = statmodel.MonsterHP(*m.Life, progScale)
	} else if v, ok := mentry["hp"].(float64); ok && v > 0 {
		base := int(v)
		doc["base_hp"] = base
		doc["hp"] = statmodel.MonsterHP(base, progScale)
	}
	if v, ok := mentry["base_seed_at"].(float64); ok && v > 0 {
		base := int(v)
		doc["base_seed_at"] = base
		at := statmodel.MonsterAt(base, progScale)
		doc["at"] = at
		doc["seed_at"] = at
	} else if m.Atk != nil && *m.Atk > 0 {
		doc["base_seed_at"] = *m.Atk
		at := statmodel.MonsterAt(*m.Atk, progScale)
		doc["at"] = at
		doc["seed_at"] = at
	} else if v, ok := mentry["at"].(float64); ok && v > 0 {
		base := int(v)
		doc["base_seed_at"] = base
		at := statmodel.MonsterAt(base, progScale)
		doc["at"] = at
		doc["seed_at"] = at
	}
	setRate(doc, "fire_rate", m.Rates.Fire)
	setRate(doc, "water_rate", m.Rates.Aqua)
	setRate(doc, "wind_rate", m.Rates.Wind)
	setRate(doc, "light_rate", m.Rates.Light)
	setRate(doc, "dark_rate", m.Rates.Dark)
	for _, rk := range []string{"fire_rate", "water_rate", "wind_rate", "light_rate", "dark_rate"} {
		if mcovered {
			if v, ok := mentry[rk].(float64); ok && v > 0 {
				doc[rk] = v
			}
		}
		if _, has := doc[rk]; !has {
			doc[rk] = 1.0
		}
	}
	return doc, nil
}

// MonsterSizeDegraded 库存无 Size 时读面采用的显式降级值（C0.3）。
const MonsterSizeDegraded = "1.000"

// effectiveScale 取有效 Size：primary 优先，其次 fallback，都缺则显式降级
// 1.000。返回十进制字符串原样（保形态）与精确解析值；非法数值明确报错。
func effectiveScale(primary, fallback *string) (string, float64, error) {
	for _, p := range []*string{primary, fallback} {
		if p == nil {
			continue
		}
		v := strings.TrimSpace(*p)
		if v == "" {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "", 0, fmt.Errorf("非十进制字符串 %q: %w", v, err)
		}
		return v, f, nil
	}
	f, _ := strconv.ParseFloat(MonsterSizeDegraded, 64)
	return MonsterSizeDegraded, f, nil
}

func setRate(doc map[string]any, key string, v *float64) {
	if v != nil {
		doc[key] = *v
	}
}

// enumDocs 组装 Enum() 消费的旁路枚举行：country（id=cardcatalog 派生
// 的数值 CountryID、name=中文名，与 country_id 的 NUMBER 下发配对；
// 客户端 Enum 经 s() 两侧比较命中）/sex（id=gender 数字串）。
func enumDocs(units []UnitRecord, cat UnitCatalog) map[string]any {
	countryIDs := map[int]string{} // CountryID → 中文名
	sexIDs := map[int]bool{}
	weapons := map[string]bool{}
	for _, r := range units {
		if u, err := cat.Unit(r.UnitID); err == nil {
			if u.Country != "" && u.CountryID > 0 {
				name := cardcatalog.CountryDisplayName(u.CountryID, u.Country)
				if existing, ok := countryIDs[u.CountryID]; !ok || name < existing {
					countryIDs[u.CountryID] = name
				}
			}
			ut := memorialUnitText(u.ID)
			if v, ok := ut["sex_id"].(float64); ok && v > 0 {
				sexIDs[int(v)] = true
			} else if u.Gender != nil {
				sexIDs[wikiGenderToSexID(*u.Gender)] = true
			} else {
				sexIDs[3] = true
			}
			if _, ok := weaponTypeName[u.Weapon]; ok {
				weapons[strconv.Itoa(u.Weapon)] = true
			}
		}
	}
	type countryRow struct {
		id   int
		name string
	}
	rows := make([]countryRow, 0, len(countryIDs))
	for id, name := range countryIDs {
		rows = append(rows, countryRow{id, name})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	countryList := []any{}
	for _, r := range rows {
		countryList = append(countryList, map[string]any{"id": r.id, "name": r.name})
	}
	sexList := []any{}
	for _, sid := range []int{1, 2, 3} {
		if sexIDs[sid] || len(sexIDs) == 0 {
			sexList = append(sexList, map[string]any{"id": sid, "name": genderName[sid]})
		}
	}
	// weapon 枚举行：UserUnitDetail 经 cache.Enum("weapon", s("weapon_id"))
	// 取武器类型名；id 与 weapon_id 同形态发字符串（Enum 两侧 s() 比较）。
	weaponList := []any{}
	for _, w := range sortedStrKeys(weapons) {
		wi, _ := strconv.Atoi(w)
		weaponList = append(weaponList, map[string]any{"id": w, "name": weaponTypeName[wi]})
	}
	return map[string]any{"country": countryList, "sex": sexList, "weapon": weaponList}
}

func sortedStrKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// numericStringToNumber preset_data 出包修正：leader_unit_id/helper_unit_id
// 等"DB 存 TEXT、契约要 NUMBER"的键统一转整（s() 兼容，i() 必须）。
func numericFieldsToNumber(user map[string]any, keys ...string) {
	for _, k := range keys {
		if s, ok := user[k].(string); ok {
			if n, err := strconv.Atoi(s); err == nil {
				user[k] = n
			}
		}
	}
}

// inventoryDataFor 读会话账号库存并组装 data 键集合
// （user_units/user_monsters + country/sex 枚举行）。账号从 req.UserID
// 来（DecodeGameRequest 已验证会话），不接受请求参数指定账号。
// 库存行引用不存在模板 → 明确数据错误（不静默跳过伪造空档）；
// Inventory/Catalog 未装配（-mem 桩）→ 空列表合法返回。
func inventoryDataFor(dep Deps, c *httpapi.Ctx, userID string) (map[string]any, error) {
	empty := map[string]any{"user_units": []any{}, "user_monsters": []any{}}
	if dep.Inventory == nil || dep.Catalog == nil {
		return empty, nil
	}
	urecs, err := dep.Inventory.Units(userID)
	if err != nil {
		return nil, fmt.Errorf("cn35 库存单位读取失败: %w", err)
	}
	activeRecs := make([]UnitRecord, 0, len(urecs))
	for _, r := range urecs {
		if r.Waiting {
			continue
		}
		activeRecs = append(activeRecs, r)
	}
	mrecs, err := dep.Inventory.Monsters(userID)
	if err != nil {
		return nil, fmt.Errorf("cn35 库存魔宠读取失败: %w", err)
	}
	// RUNE350-01：装配 Skills 时按宿主分组符文库存，buildUnitDoc 出包后
	// 合入槽位字段与战斗加成（读面与 execute 同口径，Piece.cs 消费字段）。
	skillsByUnit, err := skillsByUnitFor(dep, userID)
	if err != nil {
		return nil, err
	}
	monsters := []any{}
	monsterByUUID := map[string]map[string]any{}
	for _, r := range mrecs {
		m, merr := dep.Catalog.Monster(r.MonsterID)
		if merr != nil {
			return nil, fmt.Errorf("cn35 库存魔宠模板缺失 monster_id=%d uuid=%s: %w",
				r.MonsterID, r.UUID, merr)
		}
		mdoc, derr := buildMonsterDoc(r, m)
		if derr != nil {
			return nil, derr
		}
		mdoc["user_id"] = userID
		monsters = append(monsters, mdoc)
		monsterByUUID[r.UUID] = mdoc
	}
	units := []any{}
	for _, r := range activeRecs {
		u, uerr := dep.Catalog.Unit(r.UnitID)
		if uerr != nil {
			return nil, fmt.Errorf("cn35 库存单位模板缺失 unit_id=%d: %w", r.UnitID, uerr)
		}
		udoc, derr := buildUnitDoc(r, u)
		if derr != nil {
			return nil, derr
		}
		if r.MonsterUUID != "" {
			if mdoc, ok := monsterByUUID[r.MonsterUUID]; ok {
				udoc["monster_id"] = mdoc["id"]
				udoc["monster_scale"] = mdoc["scale"]
			}
		}
		applyUnitSkills(udoc, u.Rare, r.DuplicatedCount, skillsByUnit[r.UnitID])
		units = append(units, udoc)
	}
	data := map[string]any{"user_units": units, "user_monsters": monsters}
	for k, v := range enumDocs(activeRecs, dep.Catalog) {
		data[k] = v
	}
	c.Decoded = map[string]any{
		"cn35": true, "uid": userID,
		"units": len(units), "monsters": len(monsters),
	}
	return data, nil
}

// userUnitsHandler GET|POST /user_units：同伴列表。响应捆绑
// user_monsters——UserUnitDetail/UserMonsterDetail 的 WaitForData 都等
// 双键，单向请求也必须把对方键写进缓存（handoffs/03 §消费链）。
// 同时对齐真实抓包（/user_units/）附带 user_decks 与 user_passive_skills。
func userUnitsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		data, err := inventoryDataFor(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		if dep.Decks != nil {
			decks, derr := dep.Decks.Decks(req.UserID)
			if derr != nil {
				return nil, fmt.Errorf("cn35 user_units 编队读取失败: %w", derr)
			}
			data["user_decks"] = DecksWire(decks)
		}
		data["user_passive_skills"] = []any{}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// userMonstersHandler GET|POST /user_monsters：魔宠列表（同一组装面）。
func userMonstersHandler(dep Deps) httpapi.Handler {
	return userUnitsHandler(dep)
}
