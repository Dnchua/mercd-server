// monster_seed.go 魔宠（种子）两个运营面的实现：
//
//  1. 掉落体型（STAT350-04 后续）：原作掉落魔宠体型为随机值，强化（培育）
//     只能推进到客户端常量边界 1.70，掉落即 >1.70 的个体天然不可强化
//     （monster_powerup.go ErrMonsterScaleCap 已实现该口径）。此前 seed
//     入账不带 Size → 读面显式降级 1.000，与玩家反馈"掉落全是 1.0"一致。
//     体型区间/分布属数值，集中为下方占位常量（PROPOSED），待数值 agent
//     校准；is_rare/scale_limit 稀有路径未取证，不落伪值。
//  2. 种子技下发：客户端 SWFQuestCutin/SeedControllerPanel 全部技能效果
//     由 user_monsters 行的 seed_skill_id/seed_skill_value(/value2) 驱动
//     （客户端自算，服务端只须下发正确值；ilspy 取证见 change-note）。
//     skill 类型由 monsters.json `skill` 文本中的日文技能名/中文类型词
//     解析；效果值优先取文本中给出的实数（如"提高31.1%"），占位 nnn/N%
//     落到 SeedSkillValueDefault 占位表（PROPOSED，待数值 agent 校准）。
package cn35game

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

// MonsterDropScaleM 掉落体型千分比区间【PROPOSED·数值占位】。
// 原作普通魔宠强化封顶 1.70（客户端 scaleTable[2]），掉落可越界
// （玩家口径"突破上限只能靠掉落"）。稀有（is_rare/scale_limit）
// 路径未取证，不参与本摇奖。
var MonsterDropScaleM = struct {
	Min int
	Max int
}{Min: 1000, Max: 1800}

// RollMonsterDropScale 摇一个掉落体型并返回读面形态的三位小数字符串
// （"1.000"–"1.800"，均匀分布占位）。每次调用新建加密种子源（与
// 掉落判定同口径，非预测要求低）。
func RollMonsterDropScale() string {
	m := MonsterDropScaleM.Min + newDropRand().Intn(MonsterDropScaleM.Max-MonsterDropScaleM.Min+1)
	return scaleMString(m)
}

func scaleMString(m int) string {
	return strconv.Itoa(m/1000) + "." + pad3(m%1000)
}

func pad3(v int) string {
	s := strconv.Itoa(v)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

// ---- 种子技 ----

// 技能类型 id = 客户端 SeedSkill.Type 枚举（ilspy：
// 1-5 属性魂,6 治愈,7 威慑,8 麻痹,9 SP掉落,10 士气,
// 11-15 属性破,16-20 正领域,21-23 武器魂,24-33 双色魂,
// 34-43 双色破,44-48 反领域,49 国别魂,50 麻痹2,51 延时）。
const (
	seedSkillHeal      = 6
	seedSkillTerror    = 7
	seedSkillParalysis = 8
	seedSkillSPDrop    = 9
	seedSkillGutsUp    = 10
	seedSkillCountry   = 49
	seedSkillParalyze2 = 50
	seedSkillExtend    = 51
)

// SeedSkillValueDefault 各技能类型的效果值占位【PROPOSED·数值占位】。
// [0]=seed_skill_value、[1]=seed_skill_value2。含义按客户端消费：
// 魂/破/领域类=比例（客户端 +1 乘算）；治愈=回复量+上限提升比例；
// 麻痹=秒；威慑=攻击下降比例；SP掉落=掉率加成；士气=级数；延时=秒。
// 文本含实数时优先用实数，本表仅兜底。
var SeedSkillValueDefault = map[int][2]float64{
	1: {0.25, 0}, 2: {0.25, 0}, 3: {0.25, 0}, 4: {0.25, 0}, 5: {0.25, 0},
	6:  {5000, 0.10},
	7:  {0.30, 0},
	8:  {3.0, 0},
	9:  {0.30, 0},
	10: {3, 0},
	11: {0.25, 0}, 12: {0.25, 0}, 13: {0.25, 0}, 14: {0.25, 0}, 15: {0.25, 0},
	16: {0.25, 0}, 17: {0.25, 0}, 18: {0.25, 0}, 19: {0.25, 0}, 20: {0.25, 0},
	21: {0.25, 0}, 22: {0.25, 0}, 23: {0.25, 0},
	24: {0.25, 0}, 25: {0.25, 0}, 26: {0.25, 0}, 27: {0.25, 0},
	28: {0.25, 0}, 29: {0.25, 0}, 30: {0.25, 0}, 31: {0.25, 0},
	32: {0.25, 0}, 33: {0.25, 0},
	34: {0.25, 0}, 35: {0.25, 0}, 36: {0.25, 0}, 37: {0.25, 0},
	38: {0.25, 0}, 39: {0.25, 0}, 40: {0.25, 0}, 41: {0.25, 0},
	42: {0.25, 0}, 43: {0.25, 0},
	44: {0.25, 0}, 45: {0.25, 0}, 46: {0.25, 0}, 47: {0.25, 0}, 48: {0.25, 0},
	49: {0.25, 0},
	50: {3.0, 0},
	51: {20, 0},
}

// 日文技能名 → 类型 id（skill 文本半角括号内为官方技能名）。
var jpSeedSkillID = map[string]int{
	"ファイアソウル": 1, "アクアソウル": 2, "ウィンドソウル": 3,
	"ライトソウル": 4, "ダークソウル": 5,
	"ファイアブレイク": 11, "アクアブレイク": 12, "ウィンドブレイク": 13,
	"ライトブレイク": 14, "ダークブレイク": 15,
	"ファイアフィブル": 16, "アクアフィブル": 17, "ウィンドフィブル": 18,
	"ライトフィブル": 19, "ダークフィブル": 20,
	"スラッシュソウル": 21, "スタブソウル": 22, "ストライクソウル": 23,
	"スチームソウル": 24, "ブラストソウル": 25, "フレアソウル": 26,
	"マグマソウル": 27, "ウェイブソウル": 28, "フロストソウル": 29,
	"アシッドソウル": 30, "ゲイルソウル": 31, "ストームソウル": 32,
	"カオスソウル":   33,
	"スチームブレイク": 34, "ブラストブレイク": 35, "フレアブレイク": 36,
	"マグマブレイク": 37, "ウェイブブレイク": 38, "フロストブレイク": 39,
	"アシッドブレイク": 40, "ゲイルブレイク": 41, "ストームブレイク": 42,
	"カオスブレイク":   43,
	"ファイアイクリーズ": 44, "アクアイクリーズ": 45, "ウィンドイクリーズ": 46,
	"ライトイクリーズ": 47, "ダークイクリーズ": 48,
	"カントリーソウル": 49,
	"パライズアサルト": 50,
}

// 中文属性字 → element（与 attr_id 同序：火水土风光暗=1..5，异体字归并）。
var cnElement = map[rune]int{
	'火': 1, '炎': 1, '水': 2, '风': 3, '風': 3, '光': 4, '暗': 5, '闇': 5,
}

var (
	reFullParen = regexp.MustCompile(`（([^（）()]{1,8})）`)
	rePercent   = regexp.MustCompile(`(\d+(?:\.\d+)?)[%％]`)
	reHealNum   = regexp.MustCompile(`恢复(\d+)`)
	reGutsNum   = regexp.MustCompile(`提升(\d+)级`)
	reSeconds   = regexp.MustCompile(`(?:持续|延长)(\d+(?:\.\d+)?)秒`)
)

// 双色对 → 类型 id 基数：魂=+23、破=+33（元素序对 (a<b) → 1..10）。
func dualSkillID(a, b, kind int) int {
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	// 无序元素对 → 1..10 序号（与 SeedSkill.Type 24-33/34-43 同序）。
	idx := map[[2]int]int{
		{1, 2}: 1, {1, 3}: 2, {1, 4}: 3, {1, 5}: 4,
		{2, 3}: 5, {2, 4}: 6, {2, 5}: 7,
		{3, 4}: 8, {3, 5}: 9, {4, 5}: 10,
	}[[2]int{lo, hi}]
	if idx == 0 {
		return 0
	}
	if kind == 2 { // 破
		return 33 + idx
	}
	return 23 + idx // 魂
}

// MonsterSeedSkill 由魔宠模板解析种子技配置。
// 返回 id=0 表示该魔宠无种子技（客户端 SeedSkill.Type.NONE，召唤照常、
// 无加成）。value/value2 优先取文本实数，占位回落 SeedSkillValueDefault。
//
// 类型识别优先级：目录结构化 seed 字段（客户端枚举真值，可消歧
// 同名异义的日文名，如单/双次麻痹共用 パライズアサルト）→ 文本内
// 日文官方技能名（半角括号内或冒号后裸名）→ 中文类型词。
func MonsterSeedSkill(m *cardcatalog.Monster) (id int, value, value2 float64) {
	if m == nil || m.Skill == "" || m.Skill == "暂无" {
		return 0, 0, 0
	}
	s := m.Skill
	// 1) 目录 seed 字段即客户端 SeedSkill.Type 枚举，优先级最高。
	if m.Seed != nil && *m.Seed != 0 {
		id = *m.Seed
	}
	// 2) 整串匹配已知日文官方技能名（覆盖半角括号与冒号后裸名）。
	if id == 0 {
		id = jpNameInText(s)
	}
	// 3) 中文类型词（含全角括号双色对）。
	if id == 0 {
		id = cnSeedSkillID(s, m.Element)
	}
	if id == 0 {
		return 0, 0, 0
	}
	value, value2 = seedSkillValueFromText(id, s)
	def := SeedSkillValueDefault[id]
	if value == 0 {
		value = def[0]
	}
	if value2 == 0 {
		value2 = def[1]
	}
	return id, value, value2
}

// cnSeedSkillID 中文类型词 → 类型 id；elem 为模板属性（无属性名限定的
// 属性魂/破/领域按魔宠自身元素归位——同种不同元素各配同名技能的文本
// 证据见 monsters.json 89-96 等条）。
func cnSeedSkillID(s string, elem int) int {
	switch {
	case containsAny(s, "双色魂", "双色破"):
		a, b := cnPair(s)
		kind := 1
		if containsAny(s, "双色破") {
			kind = 2
		}
		return dualSkillID(a, b, kind)
	case containsAny(s, "弓斩魂", "斩魂", "斩击属性", "长弓属性", "远程属性"):
		return 21 // 斩击+长弓/远程 → SLASH_SOUL
	case containsAny(s, "法突魂", "刺魂", "突击属性", "魔法属性"):
		return 22 // 突击+魔法 → STAB_SOUL
	case containsAny(s, "铳打魂", "打魂", "打击属性", "铳枪属性", "枪弹属性"):
		return 23 // 打击+铳枪/枪弹 → STRIKE_SOUL
	case containsAny(s, "双麻痹", "麻痹攻击"):
		return seedSkillParalyze2
	case containsAny(s, "麻痹"):
		return seedSkillParalysis
	case containsAny(s, "治愈"):
		return seedSkillHeal
	case containsAny(s, "威慑"):
		return seedSkillTerror
	case containsAny(s, "幸运"):
		return seedSkillSPDrop
	case containsAny(s, "士气"):
		return seedSkillGutsUp
	case containsAny(s, "延长时间"):
		return seedSkillExtend
	case containsAny(s, "国别魂"):
		return seedSkillCountry
	case containsAny(s, "反领域"):
		return 43 + elem // 44-48
	case containsAny(s, "正领域"):
		return 15 + elem // 16-20
	case containsAny(s, "属性魂", "属性ソウル"):
		return elem // 1-5
	case containsAny(s, "属性破", "ブレイク"):
		return 10 + elem // 11-15
	}
	// 无括号的中文双色简写（"火光破"/"光暗魂"等）。
	head := headWord(s)
	if hr := []rune(head); len(hr) >= 2 {
		a, aok := cnElement[hr[0]]
		b, bok := cnElement[hr[1]]
		if aok && bok {
			kind := 0
			if containsAny(s, "破") {
				kind = 2
			} else if containsAny(s, "魂") {
				kind = 1
			}
			if kind != 0 {
				return dualSkillID(a, b, kind)
			}
		}
	}
	// 日文技能名直接作词头且无括号（"ダークソウル：…"）。
	for name, v := range jpSeedSkillID {
		if strings.Contains(head, name) {
			return v
		}
	}
	return 0
}

// jpSeedSkillNames 按长度降序的日文技能名表（最长优先，防前缀抢占）。
var jpSeedSkillNames = func() []string {
	names := make([]string, 0, len(jpSeedSkillID))
	for n := range jpSeedSkillID {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		return len(names[i]) > len(names[j])
	})
	return names
}()

// jpNameInText 在文本任意位置匹配已知日文官方技能名（半角括号内
// "(アクアソウル)" 与冒号后裸名 "2属性ソウル：アシッドソウル（水闇）"
// 均覆盖），返回对应类型 id，未命中返回 0。
func jpNameInText(s string) int {
	for _, n := range jpSeedSkillNames {
		if strings.Contains(s, n) {
			return jpSeedSkillID[n]
		}
	}
	return 0
}

// headWord 取文本首个"："或"："前的词头（如"火光破：xxx"→"火光破"）。
func headWord(s string) string {
	for i, r := range s {
		if r == '：' || r == ':' || r == ' ' {
			return s[:i]
		}
	}
	return s
}

// cnPair 取全角括号内两个属性字（"（水风）"→2,3）。
func cnPair(s string) (int, int) {
	m := reFullParen.FindStringSubmatch(s)
	if m == nil {
		return 0, 0
	}
	var els []int
	for _, r := range m[1] {
		if e, ok := cnElement[r]; ok {
			els = append(els, e)
		}
	}
	if len(els) != 2 {
		return 0, 0
	}
	return els[0], els[1]
}

// seedSkillValueFromText 文本实数优先：含具体百分数/数值时用实数，
// 占位 nnn/N%/数秒 返回 0 走默认表。
func seedSkillValueFromText(id int, s string) (float64, float64) {
	switch {
	case id == seedSkillHeal:
		var v1, v2 float64
		if m := reHealNum.FindStringSubmatch(s); m != nil {
			v1, _ = strconv.ParseFloat(m[1], 64)
		}
		if m := rePercent.FindStringSubmatch(s); m != nil {
			v2, _ = strconv.ParseFloat(m[1], 64)
			v2 /= 100
		}
		return v1, v2
	case id == seedSkillGutsUp:
		if m := reGutsNum.FindStringSubmatch(s); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			return v, 0
		}
	case id == seedSkillParalysis, id == seedSkillParalyze2, id == seedSkillExtend:
		if m := reSeconds.FindStringSubmatch(s); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			return v, 0
		}
	default:
		if m := rePercent.FindStringSubmatch(s); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			return v / 100, 0
		}
	}
	return 0, 0
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
