// special_scout.go CN3.5 每日轮换特殊招募（国服 Wiki 属性/武器/出身国别/性别限定顺序）与星级信配置。
package cn35game

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

// specialScoutLoc 特殊招募每日轮换判定时区（东八区，与面包店 dailyShopLoc 一致）。
var specialScoutLoc = time.FixedZone("UTC+8", 8*3600)

const specialScoutRefreshHour = 4

// SpecialScoutKind 特殊招募分类（对应国服 Wiki 特殊招募四大系列）。
type SpecialScoutKind string

const (
	SpecialScoutAttr    SpecialScoutKind = "attr"
	SpecialScoutWeapon  SpecialScoutKind = "weapon"
	SpecialScoutCountry SpecialScoutKind = "country"
	SpecialScoutGender  SpecialScoutKind = "gender"
)

// SpecialScoutTheme 定义一个国服特殊招募主题池。
type SpecialScoutTheme struct {
	PoolID      string
	Kind        SpecialScoutKind
	Name        string
	Description string
	Element     int
	Weapon      int
	Countries   []string
	Gender      int
}

// Match 判断同伴模板是否属于本特殊招募主题池（3★–5★ 资格筛选）。
func (t SpecialScoutTheme) Match(u *cardcatalog.Unit) bool {
	if u == nil {
		return false
	}
	switch t.Kind {
	case SpecialScoutAttr:
		return u.Element == t.Element
	case SpecialScoutWeapon:
		return u.Weapon == t.Weapon
	case SpecialScoutCountry:
		for _, c := range t.Countries {
			if u.Country == c {
				return true
			}
		}
		return false
	case SpecialScoutGender:
		return u.Gender != nil && *u.Gender == t.Gender
	default:
		return false
	}
}

// SpecialScoutRotation 国服 Wiki 特殊招募全系列 33 池每日轮换顺序：
//  1. 属性限定（5 池：火 → 水 → 风 → 光 → 暗）
//  2. 武器限定（7 池：斩击 → 突击 → 打击 → 弓箭 → 魔法 → 铳弹 → 回复）
//  3. 出身国别限定（19 池：按国服第一回国别活动实装顺序：沙漠→魔法→王国→动物→
//     常夏→和→死者→西部→点心→妖精→科学→植物→恐龙→空→少数民族与华夏→机械→雪→电→异邦）
//  4. 性别限定（2 池：男性限定 → 女性限定）
//
// 经 CN3.5 名册核验，全部 33 池在 951 名合格 3★–5★ 同伴中均同时涵盖 3★/4★/5★。
var SpecialScoutRotation = []SpecialScoutTheme{
	// 属性限定（5 池）
	{PoolID: "101", Kind: SpecialScoutAttr, Element: 1, Name: "炎属性限定招募", Description: "仅包含炎属性★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "102", Kind: SpecialScoutAttr, Element: 2, Name: "水属性限定招募", Description: "仅包含水属性★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "103", Kind: SpecialScoutAttr, Element: 3, Name: "风属性限定招募", Description: "仅包含风属性★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "104", Kind: SpecialScoutAttr, Element: 4, Name: "光属性限定招募", Description: "仅包含光属性★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "105", Kind: SpecialScoutAttr, Element: 5, Name: "暗属性限定招募", Description: "仅包含暗属性★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},

	// 武器限定（7 池）
	{PoolID: "106", Kind: SpecialScoutWeapon, Weapon: 1, Name: "斩击武器限定招募", Description: "仅包含斩击类★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "107", Kind: SpecialScoutWeapon, Weapon: 2, Name: "突击武器限定招募", Description: "仅包含突击类★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "108", Kind: SpecialScoutWeapon, Weapon: 3, Name: "打击武器限定招募", Description: "仅包含打击类★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "109", Kind: SpecialScoutWeapon, Weapon: 4, Name: "弓箭武器限定招募", Description: "仅包含弓箭类★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "110", Kind: SpecialScoutWeapon, Weapon: 5, Name: "魔法武器限定招募", Description: "仅包含魔法类★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "111", Kind: SpecialScoutWeapon, Weapon: 6, Name: "铳弹武器限定招募", Description: "仅包含铳弹类★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "112", Kind: SpecialScoutWeapon, Weapon: 7, Name: "回复武器限定招募", Description: "仅包含回复类★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},

	// 出身国别限定（19 池，按 Wiki/国服实装序）
	{PoolID: "113", Kind: SpecialScoutCountry, Countries: []string{"沙漠之国", "砂漠之国"}, Name: "沙漠之国限定招募", Description: "仅包含沙漠之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "114", Kind: SpecialScoutCountry, Countries: []string{"魔法之国"}, Name: "魔法之国限定招募", Description: "仅包含魔法之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "115", Kind: SpecialScoutCountry, Countries: []string{"王国"}, Name: "王国限定招募", Description: "仅包含王国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "116", Kind: SpecialScoutCountry, Countries: []string{"动物之国"}, Name: "动物之国限定招募", Description: "仅包含动物之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "117", Kind: SpecialScoutCountry, Countries: []string{"常夏之国"}, Name: "常夏之国限定招募", Description: "仅包含常夏之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "118", Kind: SpecialScoutCountry, Countries: []string{"和之国"}, Name: "和之国限定招募", Description: "仅包含和之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "119", Kind: SpecialScoutCountry, Countries: []string{"死者之国"}, Name: "死者之国限定招募", Description: "仅包含死者之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "120", Kind: SpecialScoutCountry, Countries: []string{"西部之国"}, Name: "西部之国限定招募", Description: "仅包含西部之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "121", Kind: SpecialScoutCountry, Countries: []string{"点心之国"}, Name: "点心之国限定招募", Description: "仅包含点心之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "122", Kind: SpecialScoutCountry, Countries: []string{"妖精之国"}, Name: "妖精之国限定招募", Description: "仅包含妖精之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "123", Kind: SpecialScoutCountry, Countries: []string{"科学之国"}, Name: "科学之国限定招募", Description: "仅包含科学之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "124", Kind: SpecialScoutCountry, Countries: []string{"植物之国"}, Name: "植物之国限定招募", Description: "仅包含植物之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "125", Kind: SpecialScoutCountry, Countries: []string{"恐龙之国"}, Name: "恐龙之国限定招募", Description: "仅包含恐龙之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "126", Kind: SpecialScoutCountry, Countries: []string{"空之国"}, Name: "空之国限定招募", Description: "仅包含空之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "127", Kind: SpecialScoutCountry, Countries: []string{"少数民族之国", "华夏之国"}, Name: "少数民族之国限定招募", Description: "仅包含少数民族及华夏之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "128", Kind: SpecialScoutCountry, Countries: []string{"机械之国"}, Name: "机械之国限定招募", Description: "仅包含机械之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "129", Kind: SpecialScoutCountry, Countries: []string{"雪之国"}, Name: "雪之国限定招募", Description: "仅包含雪之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "130", Kind: SpecialScoutCountry, Countries: []string{"电之国", "エレキ之国"}, Name: "电之国限定招募", Description: "仅包含电之国★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "131", Kind: SpecialScoutCountry, Countries: []string{"异邦"}, Name: "异邦限定招募", Description: "仅包含异邦★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},

	// 性别限定（2 池）
	{PoolID: "132", Kind: SpecialScoutGender, Gender: 2, Name: "男性同伴限定招募", Description: "仅包含男性★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
	{PoolID: "133", Kind: SpecialScoutGender, Gender: 3, Name: "女性同伴限定招募", Description: "仅包含女性★3～★5同伴的每日轮换特殊招募。\\\\n十连享9折优惠（9本契约书抽10次），共享限时保底。"},
}

// specialScoutNow 取特殊招募判定时刻（测试时钟优先，其次 GuildClock，默认 time.Now）。
func specialScoutNow(dep Deps) time.Time {
	if dep.SpecialScoutClock != nil {
		return dep.SpecialScoutClock()
	}
	if dep.GuildClock != nil {
		return dep.GuildClock()
	}
	return time.Now()
}

// SpecialScoutWindow 计算东八区每日 04:00 切日的当前周期下标与时间窗 [beganAt, endedAt]。
func SpecialScoutWindow(now time.Time) (slotIdx int, beganAt, endedAt int64) {
	local := now.In(specialScoutLoc)
	shifted := local.Add(-time.Duration(specialScoutRefreshHour) * time.Hour)
	dayStartShifted := time.Date(shifted.Year(), shifted.Month(), shifted.Day(), 0, 0, 0, 0, specialScoutLoc)
	began := dayStartShifted.Add(time.Duration(specialScoutRefreshHour) * time.Hour)
	beganAt = began.Unix()
	endedAt = began.Add(24*time.Hour).Unix() - 1
	epochDay := (beganAt + 8*3600 - int64(specialScoutRefreshHour)*3600) / 86400
	n := len(SpecialScoutRotation)
	slotIdx = ((int(epochDay) % n) + n) % n
	return slotIdx, beganAt, endedAt
}

// specialScoutThemeByID 按 PoolID 查找专题定义（"101".."133"）。
func specialScoutThemeByID(poolID string) (SpecialScoutTheme, bool) {
	for _, th := range SpecialScoutRotation {
		if th.PoolID == poolID {
			return th, true
		}
	}
	return SpecialScoutTheme{}, false
}

// buildSpecialScoutPoolForTheme 从常驻钻石池成员按主题筛选合格 3★–5★ 名单并构造池规则。
func buildSpecialScoutPoolForTheme(
	dep Deps, base *ActiveGachaConfig, th SpecialScoutTheme, limited bool, beganAt, endedAt int64,
) (gachaPoolRules, []gachaUnit, bool) {
	if base == nil || dep.Catalog == nil {
		return gachaPoolRules{}, nil, false
	}
	baseMembers := base.MembersByPool["2"]
	if len(baseMembers) == 0 {
		return gachaPoolRules{}, nil, false
	}
	var baseWeights map[string]int
	if dp, ok := base.Rules.poolByID("2"); ok {
		baseWeights = dp.RarityWeights
	}
	filtered := make([]gachaUnit, 0, len(baseMembers)/4)
	weights := make(map[string]int, len(baseMembers)/4)
	rareSeen := map[int]int{}
	for _, gu := range baseMembers {
		uid, err := strconv.Atoi(gu.UnitID)
		if err != nil {
			continue
		}
		u, uerr := dep.Catalog.Unit(uid)
		if uerr != nil || !th.Match(u) {
			continue
		}
		filtered = append(filtered, gu)
		rareSeen[gu.RarityID]++
		if w := baseWeights[gu.UnitID]; w > 0 {
			weights[gu.UnitID] = w
		} else {
			weights[gu.UnitID] = 1
		}
	}
	if rareSeen[gachaRarityUR] == 0 || rareSeen[gachaRaritySR] == 0 || rareSeen[gachaRarityHR] == 0 {
		return gachaPoolRules{}, nil, false
	}
	rule := gachaPoolRules{
		PoolID:             th.PoolID,
		Slot:               1,
		ListTypeID:         0,
		Limited:            limited,
		BeginAt:            beganAt,
		EndAt:              endedAt,
		Name:               th.Name,
		Description:        th.Description,
		Price:              gachaPrice{Coin: 5},
		MaxExecutableCount: 9,
		MaxExecuteBonus:    1,
		RarityWeights:      weights,
		PityGroup:          gachaPityGroupLimitedUP,
	}
	return rule, filtered, true
}

// resolveRuntimeGachaConfig 基于已发布常驻快照叠加当日轮换特殊招募池（挂载槽 1），
// 不修改传入的已发布快照对象。
func resolveRuntimeGachaConfig(dep Deps, now time.Time) (*ActiveGachaConfig, *SpecialScoutTheme, error) {
	base, err := gachaActiveConfig(dep)
	if err != nil || base == nil {
		return base, nil, err
	}
	slotIdx, beganAt, endedAt := SpecialScoutWindow(now)
	th := SpecialScoutRotation[slotIdx]
	spRule, spUnits, ok := buildSpecialScoutPoolForTheme(dep, base, th, true, beganAt, endedAt)
	if !ok {
		return base, nil, nil
	}
	// 若槽 1 已被后台人工占用则不强行覆盖。
	if _, occupied := base.Rules.slotFor(spRule.PoolID); occupied {
		return base, nil, nil
	}
	for _, p := range base.Rules.Pools {
		if p.Slot == 1 {
			return base, nil, nil
		}
	}

	out := &ActiveGachaConfig{
		Version:       base.Version,
		Digest:        base.Digest,
		MembersByPool: make(map[string][]gachaUnit, len(base.MembersByPool)+1),
		Rules: gachaRules{
			Version:               base.Rules.Version,
			Pools:                 make([]gachaPoolRules, 0, len(base.Rules.Pools)+1),
			Pity:                  make(map[string]gachaPityRules, len(base.Rules.Pity)+1),
			DuplicateCompensation: base.Rules.DuplicateCompensation,
			DuplicateMaxCount:     base.Rules.DuplicateMaxCount,
		},
	}
	for k, v := range base.MembersByPool {
		out.MembersByPool[k] = v
	}
	for k, v := range base.Rules.Pity {
		out.Rules.Pity[k] = v
	}
	if _, hasLimited := out.Rules.Pity[gachaPityGroupLimitedUP]; !hasLimited {
		pityBase, hasDia := out.Rules.Pity[gachaPityGroupDiamond]
		if !hasDia {
			pityBase = gachaPityRules{
				Enabled: true, URBasePercent: 2, URSoftStart: 50,
				URSoftStepPercent: 2, URHardPity: 80, SRPity: 10,
				NonURWeights: map[int]int{gachaRaritySR: 18, gachaRarityHR: 80},
			}
		}
		pityBase.UPGuarantee = false
		out.Rules.Pity[gachaPityGroupLimitedUP] = pityBase
	}
	out.MembersByPool[spRule.PoolID] = spUnits
	out.Rules.Pools = append(out.Rules.Pools, spRule)
	out.Rules.Pools = append(out.Rules.Pools, base.Rules.Pools...)
	return out, &th, nil
}

// ticketPoolSpec 定义 ★3/★4/★5 信池规格（/gachas/ticket_available_list 与 id=11..13 执行）。
type ticketPoolSpec struct {
	PoolID      string
	NumID       int
	ItemKey     string
	Name        string
	Description string
	Rare        int
	CostCount   int
}

var ticketPoolSpecs = map[string]ticketPoolSpec{
	"11": {
		PoolID: "11", NumID: 11, ItemKey: "gacha_ticket_11", Name: "★3 信", Rare: 3, CostCount: 5,
		Description: "集齐5枚可进行3星以上契约书招募，获得3星同伴的特殊的信",
	},
	"12": {
		PoolID: "12", NumID: 12, ItemKey: "gacha_ticket_12", Name: "★4 信", Rare: 4, CostCount: 5,
		Description: "集齐5枚可进行3星以上契约书招募，获得4星同伴的特殊的信",
	},
	"13": {
		PoolID: "13", NumID: 13, ItemKey: "gacha_ticket_13", Name: "★5 信", Rare: 5, CostCount: 5,
		Description: "集齐5枚可进行骑士契约书招募，获得5星同伴的特殊的信",
	},
}

// scoutBookExchangeSpec 定义 3★/4★ 契约书 6 合 1 合成规则。
var scoutBookExchangeSpec = map[int]struct {
	FromKey string
	ToKey   string
	Cost    int
	FromMsg string
	ToMsg   string
}{
	100015: {FromKey: "scout_book_3star", ToKey: "scout_book_4star", Cost: 6, FromMsg: "3星契约书", ToMsg: "4星契约书"},
	100011: {FromKey: "scout_book_4star", ToKey: "scout_book_5star", Cost: 6, FromMsg: "4星契约书", ToMsg: "5星契约书"},
}

// GachaResolvedPoolsForTest 暴露运行期（含每日轮换特殊招募池）池文档概要给测试使用。
func GachaResolvedPoolsForTest(dep Deps, now time.Time) ([]map[string]any, *SpecialScoutTheme, error) {
	cfg, th, err := resolveRuntimeGachaConfig(dep, now)
	if err != nil || cfg == nil {
		return nil, th, err
	}
	return GachaPoolDocumentsForTest(cfg), th, nil
}

// SpecialScoutRotationCountForTest 暴露轮换表长度以锁定 33 日轮换不遗漏。
func SpecialScoutRotationCountForTest() int {
	return len(SpecialScoutRotation)
}

func formatWindowTimeRange(beganAt, endedAt int64) string {
	b := time.Unix(beganAt, 0).In(specialScoutLoc)
	e := time.Unix(endedAt+1, 0).In(specialScoutLoc)
	return fmt.Sprintf("%d/%d %02d:00 结束", e.Month(), e.Day(), e.Hour()) + " (" + b.Format("01-02 15:04") + "起)"
}
