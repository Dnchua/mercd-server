// fate.go FATE350-01：M1 命运星盘（艾琳的小屋 / 主人公的日常）领域定义与词条池。
//
// 协议与客户端证据（mlk-capture-201711 + Assembly-CSharp.dll 反编译）：
//   - MainCharaDailyPage：请求 /users/function_portal + /fate/fate_buff_summary；
//     FateInfoParts 读 fate_buff_summary.summary_list（空时显引导底图，非空显 3 条加成）；
//     FateButtonNewParts 读 fate_buff_summary.disable_show_fate_new（false=显示 New 气泡，true=隐藏）。
//   - FateAstrolabePage：请求 /fate/show + 依赖缓存 user_red_coin；
//     FateAstrolabeLeftParts 播轮盘旋转动画（2.0s）；
//     FateAstrolabeRightParts 读 fate.{daily_luck_desc,roll_all_price,roll_inner_price,today_roll_times,fate_list}：
//   - today_roll_times == 0：rollAllButton 显示免费，rollInnerButton 禁用（isEnabled=false）；
//   - today_roll_times >= 1：rollAllButton 消耗 18 钻（roll_all_price），rollInnerButton 消耗 10 钻（roll_inner_price）；
//   - DoRollAll / DoRollInner 请求 /fate/roll?type=<all|inner>&confirm=<false|true>；
//   - 当响应 temp_fate_roll_result == false 且顶层 astrolabe_need_confirm == true 时，
//     客户端经 ResponseFilter 弹 yes_no 确认框，确认后自动重发 confirm=true。
//   - 本轮范围跳过主角光环与天赋树：fate_buff_summary.summary_list[i].desc 中主角光环分量固定为 +0 / +0.00%。
package cn35game

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	mathrand "math/rand"
	"time"
)

// 命运星盘业务错误定义。
var (
	// ErrFateInnerRequiresFirstRoll 当日尚未进行免费首抽（today_roll_times==0）时不允许只转内盘。
	ErrFateInnerRequiresFirstRoll = errors.New("cn35game: 请先进行今日首次星盘占卜")
	// ErrFateNeedConfirm 当前星盘含极佳运势（amazing）且请求未带 confirm=true。
	ErrFateNeedConfirm = errors.New("cn35game: 当前星盘含极佳运势，需二次确认覆盖")
	// ErrFateInsufficientCoin 钻石余额不足。
	ErrFateInsufficientCoin = errors.New("cn35game: 红钻不足")
)

// FateRules 命运星盘价格与周期常量（对齐 .mlk 抓包真值）。
var FateRules = struct {
	RollAllPrice   int
	RollInnerPrice int
	RefreshHour    int
	SlotCount      int
}{
	RollAllPrice:   18,
	RollInnerPrice: 10,
	RefreshHour:    4,
	SlotCount:      3,
}

// FatePeriodKey 计算命运星盘的东八区日周期键（凌晨 04:00 刷新边界，同 dailyShopPeriodKey）。
func FatePeriodKey(now time.Time) string {
	return now.In(dailyShopLoc).
		Add(-time.Duration(FateRules.RefreshHour) * time.Hour).
		Format("2006-01-02")
}

// FateDailyLuckPool 每日运势签文池（前三句取自 .mlk 实机抓包样本）。
var FateDailyLuckPool = []string{
	"如果感到迷茫，那就去依赖他人吧",
	"再积极一点可能会有好事发生哦",
	"今天和许久未见的朋友联系一下吧",
	"偶尔放慢脚步，也能发现路边的风景",
	"保持平时的步调，好运自然会降临",
}

// FateDailyLuckDesc 按 (userID, periodKey) 确定性选取当日运势签文，同日多次打开恒定。
func FateDailyLuckDesc(userID, periodKey string) string {
	if len(FateDailyLuckPool) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte("cn35-fate-luck|" + userID + "|" + periodKey))
	idx := binary.BigEndian.Uint64(sum[:8]) % uint64(len(FateDailyLuckPool))
	return FateDailyLuckPool[idx]
}

// FateBuffSpec 定义一种候选星盘词条及其三档品质数值范围。
// IsPercent=true 时 Value 为小数比率（如 0.116 表示 11.60%）；
// IsPercent=false 时 Value 为整数增量（如 7 表示 +7）。
type FateBuffSpec struct {
	ID         string
	Name       string
	DescPrefix string
	IsPercent  bool
	// 百分比词条以 bp（万分比，如 300=3.00%, 1160=11.60%）为单位按 Step 步进；
	// 整数词条以实际整数值为单位按 Step=1 步进。
	NormalMin  int
	NormalMax  int
	GoodMin    int
	GoodMax    int
	AmazingMin int
	AmazingMax int
	Step       int
}

// FateBuffCatalog 22 种候选星盘词条全集（枚举 ID 与名称对齐 .mlk /main_chara_buff/upgrade_infos 与 /fate/show）。
var FateBuffCatalog = []FateBuffSpec{
	// 1~3：火系同伴
	{ID: "unit_fire_hp", Name: "火之生命", DescPrefix: "火系同伴体力增加", IsPercent: true, NormalMin: 300, NormalMax: 600, GoodMin: 620, GoodMax: 1200, AmazingMin: 1220, AmazingMax: 1800, Step: 20},
	{ID: "unit_fire_atk", Name: "火之力量", DescPrefix: "火系同伴攻击增加", IsPercent: true, NormalMin: 300, NormalMax: 600, GoodMin: 620, GoodMax: 1200, AmazingMin: 1220, AmazingMax: 1800, Step: 20},
	{ID: "unit_fire_rate", Name: "火之士气", DescPrefix: "火系同伴救援出现率增加", IsPercent: true, NormalMin: 500, NormalMax: 1000, GoodMin: 1050, GoodMax: 2000, AmazingMin: 2050, AmazingMax: 3000, Step: 50},
	// 4~6：水系同伴
	{ID: "unit_water_hp", Name: "水之生命", DescPrefix: "水系同伴体力增加", IsPercent: true, NormalMin: 300, NormalMax: 600, GoodMin: 620, GoodMax: 1200, AmazingMin: 1220, AmazingMax: 1800, Step: 20},
	{ID: "unit_water_atk", Name: "水之力量", DescPrefix: "水系同伴攻击增加", IsPercent: true, NormalMin: 300, NormalMax: 600, GoodMin: 620, GoodMax: 1200, AmazingMin: 1220, AmazingMax: 1800, Step: 20},
	{ID: "unit_water_rate", Name: "水之士气", DescPrefix: "水系同伴救援出现率增加", IsPercent: true, NormalMin: 500, NormalMax: 1000, GoodMin: 1050, GoodMax: 2000, AmazingMin: 2050, AmazingMax: 3000, Step: 50},
	// 7~9：风系同伴
	{ID: "unit_wind_hp", Name: "风之生命", DescPrefix: "风系同伴体力增加", IsPercent: true, NormalMin: 300, NormalMax: 600, GoodMin: 620, GoodMax: 1200, AmazingMin: 1220, AmazingMax: 1800, Step: 20},
	{ID: "unit_wind_atk", Name: "风之力量", DescPrefix: "风系同伴攻击增加", IsPercent: true, NormalMin: 300, NormalMax: 600, GoodMin: 620, GoodMax: 1200, AmazingMin: 1220, AmazingMax: 1800, Step: 20},
	{ID: "unit_wind_rate", Name: "风之士气", DescPrefix: "风系同伴救援出现率增加", IsPercent: true, NormalMin: 500, NormalMax: 1000, GoodMin: 1050, GoodMax: 2000, AmazingMin: 2050, AmazingMax: 3000, Step: 50},
	// 10~12：光系同伴
	{ID: "unit_light_hp", Name: "光之生命", DescPrefix: "光系同伴体力增加", IsPercent: true, NormalMin: 300, NormalMax: 600, GoodMin: 620, GoodMax: 1200, AmazingMin: 1220, AmazingMax: 1800, Step: 20},
	{ID: "unit_light_atk", Name: "光之力量", DescPrefix: "光系同伴攻击增加", IsPercent: true, NormalMin: 300, NormalMax: 600, GoodMin: 620, GoodMax: 1200, AmazingMin: 1220, AmazingMax: 1800, Step: 20},
	{ID: "unit_light_rate", Name: "光之士气", DescPrefix: "光系同伴救援出现率增加", IsPercent: true, NormalMin: 500, NormalMax: 1000, GoodMin: 1050, GoodMax: 2000, AmazingMin: 2050, AmazingMax: 3000, Step: 50},
	// 13~15：暗系同伴
	{ID: "unit_dark_hp", Name: "暗之生命", DescPrefix: "暗系同伴体力增加", IsPercent: true, NormalMin: 300, NormalMax: 600, GoodMin: 620, GoodMax: 1200, AmazingMin: 1220, AmazingMax: 1800, Step: 20},
	{ID: "unit_dark_atk", Name: "暗之力量", DescPrefix: "暗系同伴攻击增加", IsPercent: true, NormalMin: 300, NormalMax: 600, GoodMin: 620, GoodMax: 1200, AmazingMin: 1220, AmazingMax: 1800, Step: 20},
	{ID: "unit_dark_rate", Name: "暗之士气", DescPrefix: "暗系同伴救援出现率增加", IsPercent: true, NormalMin: 500, NormalMax: 1000, GoodMin: 1050, GoodMax: 2000, AmazingMin: 2050, AmazingMax: 3000, Step: 50},
	// 16~19：公会战 GP 与个人贡献（注：原服协议拼写即为 guild_battle_attak_gp）
	{ID: "guild_battle_attak_gp", Name: "圣战信仰", DescPrefix: "公会战每次出击获得的GP增加", IsPercent: false, NormalMin: 10, NormalMax: 25, GoodMin: 30, GoodMax: 60, AmazingMin: 65, AmazingMax: 100, Step: 5},
	{ID: "guild_battle_help_gp", Name: "声援信仰", DescPrefix: "公会战每次声援获得的GP增加", IsPercent: false, NormalMin: 2, NormalMax: 5, GoodMin: 6, GoodMax: 12, AmazingMin: 13, AmazingMax: 20, Step: 1},
	{ID: "guild_battle_pray_gp", Name: "祈祷信仰", DescPrefix: "公会战每次祈祷获得的GP增加", IsPercent: false, NormalMin: 5, NormalMax: 10, GoodMin: 11, GoodMax: 20, AmazingMin: 21, AmazingMax: 30, Step: 1},
	{ID: "guild_battle_per_con", Name: "贡献信仰", DescPrefix: "公会战每次获得的个人贡献增加", IsPercent: true, NormalMin: 500, NormalMax: 1000, GoodMin: 1050, GoodMax: 2000, AmazingMin: 2050, AmazingMax: 3000, Step: 50},
	// 20~22：金币掉落与泉水产出
	{ID: "gold_drop_main", Name: "探险财运", DescPrefix: "主线探险获得的金币增加", IsPercent: true, NormalMin: 500, NormalMax: 1500, GoodMin: 1600, GoodMax: 3000, AmazingMin: 3100, AmazingMax: 5000, Step: 100},
	{ID: "gold_drop_daily", Name: "日常财运", DescPrefix: "每日探险获得的金币增加", IsPercent: true, NormalMin: 500, NormalMax: 1500, GoodMin: 1600, GoodMax: 3000, AmazingMin: 3100, AmazingMax: 5000, Step: 100},
	{ID: "gold_drop_spring", Name: "泉水财运", DescPrefix: "不可思议之泉获得的金币增加", IsPercent: true, NormalMin: 500, NormalMax: 1500, GoodMin: 1600, GoodMax: 3000, AmazingMin: 3100, AmazingMax: 5000, Step: 100},
}

var fateBuffByID = func() map[string]FateBuffSpec {
	m := make(map[string]FateBuffSpec, len(FateBuffCatalog))
	for _, s := range FateBuffCatalog {
		m[s.ID] = s
	}
	return m
}()

// RollableFateBuffCatalog 实际进入随机抽奖池的 17 种词条。
// 说明：FateBuffCatalog 保留完整 22 种枚举以兼容 .mlk 抓包字典与历史记录反查；
// 但当前版本未启用战斗随机救援同伴出场（help_table 固定为空表），故将五系救援率词条
// （unit_*_rate）移出随机抽取池，确保玩家付费抽到的每一条词条均具备 100% 服务端数值实效。
var RollableFateBuffCatalog = func() []FateBuffSpec {
	out := make([]FateBuffSpec, 0, len(FateBuffCatalog))
	for _, s := range FateBuffCatalog {
		switch s.ID {
		case "unit_fire_rate", "unit_water_rate", "unit_wind_rate", "unit_light_rate", "unit_dark_rate":
			continue
		}
		out = append(out, s)
	}
	return out
}()

// FateBuffSpecByID 按词条 ID 查定义。
func FateBuffSpecByID(id string) (FateBuffSpec, bool) {
	s, ok := fateBuffByID[id]
	return s, ok
}

// FateEntry 单条星盘词条（JSON 键严格对齐 .mlk fate_show.fate_list[i]）。
type FateEntry struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Desc       string  `json:"desc"`
	DescPrefix string  `json:"desc_prefix"`
	DescSuffix string  `json:"desc_suffix"`
	Rank       string  `json:"rank"`
	Value      float64 `json:"value"`
}

// FateState 玩家当日星盘状态快照。
type FateState struct {
	UserID         string
	PeriodKey      string
	DailyLuckDesc  string
	TodayRollTimes int
	FateList       []FateEntry
}

// FateRollResult 一次 /fate/roll 的事务执行结果。
type FateRollResult struct {
	State       *FateState
	CostCoin    int64
	BalanceCoin int64
}

// FateStore 命运星盘读写面接口（由 cn35store.Store 实现）。
type FateStore interface {
	// FateState 读取玩家在 periodKey 下的星盘状态；若无记录或跨周期，返回重置后的初始未转状态。
	FateState(userID, periodKey string) (*FateState, error)
	// RollFate 单事务执行星盘转动：
	//   - rollType="all"：today_roll_times==0 免费（扣 0 钻），否则扣 18 钻；重抽 3 条不重复词条与数值；
	//   - rollType="inner"：要求 today_roll_times>=1，扣 10 钻；保留当前 3 条词条 ID，仅重抽品质与数值；
	//   - 当已有词条含 rank=="amazing" 且 !confirm 时，不扣费不改库，返回 ErrFateNeedConfirm。
	RollFate(userID, periodKey, rollType string, confirm bool, rng *mathrand.Rand) (*FateRollResult, error)
}

// FormatFateEntry 根据词条定义、品质档位与内部刻度值组装 FateEntry。
// 百分比词条 rawUnits 为万分比整数（如 1160 -> Value=0.116, DescSuffix="11.60%"）；
// 整数词条 rawUnits 为实际整数值（如 7 -> Value=7, DescSuffix="7"）。
func FormatFateEntry(spec FateBuffSpec, rank string, rawUnits int) FateEntry {
	var suffix string
	var val float64
	if spec.IsPercent {
		pct := float64(rawUnits) / 100.0
		suffix = fmt.Sprintf("%.2f%%", pct)
		val = math.Round(float64(rawUnits)) / 10000.0
	} else {
		suffix = fmt.Sprintf("%d", rawUnits)
		val = float64(rawUnits)
	}
	return FateEntry{
		ID:         spec.ID,
		Name:       spec.Name,
		Desc:       spec.DescPrefix + suffix,
		DescPrefix: spec.DescPrefix,
		DescSuffix: suffix,
		Rank:       rank,
		Value:      val,
	}
}

func rollOneRankAndValue(rng *mathrand.Rand, spec FateBuffSpec) FateEntry {
	p := rng.Intn(100)
	rank := "normal"
	minV, maxV := spec.NormalMin, spec.NormalMax
	switch {
	case p >= 90:
		rank = "amazing"
		minV, maxV = spec.AmazingMin, spec.AmazingMax
	case p >= 60:
		rank = "good"
		minV, maxV = spec.GoodMin, spec.GoodMax
	}
	step := spec.Step
	if step <= 0 {
		step = 1
	}
	steps := (maxV - minV) / step
	raw := minV
	if steps > 0 {
		raw = minV + rng.Intn(steps+1)*step
	}
	return FormatFateEntry(spec, rank, raw)
}

// RollFateEntries 生成一次转盘后的 3 条词条列表。
func RollFateEntries(rng *mathrand.Rand, rollType string, current []FateEntry) ([]FateEntry, error) {
	if rng == nil {
		rng = mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
	}
	switch rollType {
	case "all":
		n := len(RollableFateBuffCatalog)
		perm := rng.Perm(n)
		out := make([]FateEntry, 0, FateRules.SlotCount)
		for i := 0; i < FateRules.SlotCount && i < n; i++ {
			spec := RollableFateBuffCatalog[perm[i]]
			out = append(out, rollOneRankAndValue(rng, spec))
		}
		return out, nil
	case "inner":
		if len(current) != FateRules.SlotCount {
			return nil, ErrFateInnerRequiresFirstRoll
		}
		out := make([]FateEntry, FateRules.SlotCount)
		for i, cur := range current {
			spec, ok := FateBuffSpecByID(cur.ID)
			if !ok {
				spec = RollableFateBuffCatalog[i%len(RollableFateBuffCatalog)]
			}
			out[i] = rollOneRankAndValue(rng, spec)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: 未知星盘转动类型 %q", ErrInvalidRequest, rollType)
	}
}

// FateHasAmazing 判断当前词条列表中是否含有极佳运势（rank == "amazing"）。
func FateHasAmazing(entries []FateEntry) bool {
	for _, e := range entries {
		if e.Rank == "amazing" {
			return true
		}
	}
	return false
}

// FateShowWire 构造 /fate/show 与 /fate/roll 中的 data.fate 对象（严格对齐 .mlk）。
func FateShowWire(st *FateState) map[string]any {
	list := make([]any, 0, len(st.FateList))
	for _, e := range st.FateList {
		var val any = e.Value
		if spec, ok := FateBuffSpecByID(e.ID); ok && !spec.IsPercent {
			val = int64(math.Round(e.Value))
		}
		list = append(list, map[string]any{
			"id":          e.ID,
			"name":        e.Name,
			"desc":        e.Desc,
			"desc_prefix": e.DescPrefix,
			"desc_suffix": e.DescSuffix,
			"rank":        e.Rank,
			"value":       val,
		})
	}
	return map[string]any{
		"daily_luck_desc":  st.DailyLuckDesc,
		"roll_all_price":   FateRules.RollAllPrice,
		"roll_inner_price": FateRules.RollInnerPrice,
		"today_roll_times": st.TodayRollTimes,
		"fate_list":        list,
	}
}

// FateBuffSummaryWire 构造 /fate/fate_buff_summary 与 /fate/roll 中的 data.fate_buff_summary 对象。
// 本轮跳过主角光环，光环部分固定展示为 +0.00%（百分比词条）或 +0（整数词条）。
func FateBuffSummaryWire(st *FateState) map[string]any {
	summary := make([]any, 0, len(st.FateList))
	if st.TodayRollTimes > 0 {
		for _, e := range st.FateList {
			auraZero := "+0.00%"
			if spec, ok := FateBuffSpecByID(e.ID); ok && !spec.IsPercent {
				auraZero = "+0"
			}
			summary = append(summary, map[string]any{
				"total":       e.DescPrefix,
				"total_value": "+" + e.DescSuffix,
				"desc":        fmt.Sprintf("主角光环[eb8900]%s[-]，星盘[eb8900]+%s[-]", auraZero, e.DescSuffix),
				"rank":        e.Rank,
			})
		}
	}
	return map[string]any{
		"summary_list":          summary,
		"disable_show_fate_new": st.TodayRollTimes > 0,
	}
}

// FateBonusMap 将激活的星盘词条转为 id -> value 映射，供泉水、关卡结算、公会战与同伴面板读取。
func FateBonusMap(entries []FateEntry) map[string]float64 {
	m := make(map[string]float64, len(entries))
	for _, e := range entries {
		m[e.ID] += e.Value
	}
	return m
}

// ApplyFateRateBonus 按星盘百分比词条放大基础整数收益（向下取整，rate<=0 原样返回）。
func ApplyFateRateBonus(base int64, rate float64) int64 {
	if base <= 0 || rate <= 0 {
		return base
	}
	return int64(math.Floor(float64(base)*(1.0+rate) + 1e-9))
}

// ApplyUnitFateBuffs 将玩家当日星盘中的五系同伴属性加成合入同伴文档（hp / at）。
// 属性映射：1=fire, 2=water, 3=wind, 4=light, 5=dark。
func ApplyUnitFateBuffs(doc map[string]any, attrID int, entries []FateEntry) {
	if len(entries) == 0 || doc == nil {
		return
	}
	var hpKey, atkKey string
	switch attrID {
	case 1:
		hpKey, atkKey = "unit_fire_hp", "unit_fire_atk"
	case 2:
		hpKey, atkKey = "unit_water_hp", "unit_water_atk"
	case 3:
		hpKey, atkKey = "unit_wind_hp", "unit_wind_atk"
	case 4:
		hpKey, atkKey = "unit_light_hp", "unit_light_atk"
	case 5:
		hpKey, atkKey = "unit_dark_hp", "unit_dark_atk"
	default:
		return
	}
	bonuses := FateBonusMap(entries)
	if hpRate := bonuses[hpKey]; hpRate > 0 {
		doc["hp"] = int(math.Round(docF(doc, "hp", 0) * (1.0 + hpRate)))
	}
	if atkRate := bonuses[atkKey]; atkRate > 0 {
		doc["at"] = int(math.Round(docF(doc, "at", 0) * (1.0 + atkRate)))
	}
}
