package cn35game

import "math"

// GuildFacilityLevels 玩家所属公会的常驻战斗设施有效等级（未入会则全 0）。
//
// 口径依据：
//   - sp（祭坛）：初始 SP + 25*Lv（mlk Lv.88=+2200，图 1 Lv.100=+2500）
//   - hp（医疗所）：同伴体力 + round(baseHP * 0.0025 * Lv)（mlk Lv.79=+19.75%，图 1 Lv.100=+25%）
//   - at（训练所）：同伴攻击力 + round(baseAT * 0.0025 * Lv)（mlk Lv.84=+21%，图 1 Lv.101=+25.25%）
//   - taghness（食堂）：主人公 HP + 400*Lv，Guts/救援基础等级 clamp(Lv/20, 1, 5)
//     （mlk Lv.70=+28000/Guts3，图 2 Lv.90=+36000/Guts4）
type GuildFacilityLevels struct {
	SP       int
	HP       int
	AT       int
	Taghness int
}

// GuildFacilityLevelsForUser 读取玩家所属公会的常驻四项设施等级（altar/medical/training/dining）；
// 当前 CN3.5 `temporary` 为空占位，实际生效设施仅为常驻四项；未入会或未装配 GuildStore 时返回全 0。
func GuildFacilityLevelsForUser(dep Deps, userID string) GuildFacilityLevels {
	if dep.Guild == nil || userID == "" {
		return GuildFacilityLevels{}
	}
	gv, err := dep.Guild.MyGuild(userID)
	if err != nil || gv == nil || gv.Guild == nil {
		return GuildFacilityLevels{}
	}
	levelOf := func(typeID string) int {
		if eq := gv.Equipments[typeID]; eq != nil && eq.Level > 0 {
			return eq.Level
		}
		return 1
	}
	return GuildFacilityLevels{
		SP:       levelOf("sp"),
		HP:       levelOf("hp"),
		AT:       levelOf("at"),
		Taghness: levelOf("taghness"),
	}
}

// InitialSPBonus 祭坛提供的探险初始 SP 增量。
func (f GuildFacilityLevels) InitialSPBonus() int {
	if f.SP <= 0 {
		return 0
	}
	return 25 * f.SP
}

// MainHPBonus 食堂提供的主人公 HP 增量。
func (f GuildFacilityLevels) MainHPBonus() int {
	if f.Taghness <= 0 {
		return 0
	}
	return 400 * f.Taghness
}

// HelpLevelBase 食堂提供的救援 Guts 基数（guild_asset_help_level_base）。
func (f GuildFacilityLevels) HelpLevelBase() int {
	return GuildFacilityHelpLevelBase(f.Taghness)
}

// GuildFacilityHelpLevelBase 按食堂等级计算 guild_asset_help_level_base（未入会为 0，入会后 1..5）。
func GuildFacilityHelpLevelBase(taghnessLv int) int {
	if taghnessLv <= 0 {
		return 0
	}
	g := taghnessLv / guildTaghnessGutsEvery
	if g < 1 {
		g = 1
	}
	if g > guildTaghnessGutsMax {
		g = guildTaghnessGutsMax
	}
	return g
}

// CloneMainCharacterWithBuff 浅拷贝 main_character 模板并在副本上叠加主人公 HP 增量，避免污染全局目录。
func CloneMainCharacterWithBuff(base map[string]any, hpBonus int) map[string]any {
	out := make(map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}
	if hpBonus > 0 {
		out["hp"] = int(numOf(base["hp"])) + hpBonus
	}
	return out
}

// ApplyUnitFacilityBuff 以符文/星盘加成前的基础面板（baseHP/baseAT）为基数，
// 将医疗所与训练所设施增量累加到终值面板上。
func ApplyUnitFacilityBuff(doc map[string]any, baseHP, baseAT, hpLv, atLv int) {
	if doc == nil {
		return
	}
	if hpLv > 0 {
		bonus := int(math.Round(float64(baseHP) * 0.0025 * float64(hpLv)))
		doc["hp"] = int(docF(doc, "hp", 0)) + bonus
	}
	if atLv > 0 {
		bonus := int(math.Round(float64(baseAT) * 0.0025 * float64(atLv)))
		doc["at"] = int(docF(doc, "at", 0)) + bonus
	}
}
