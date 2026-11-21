// pvp.go M10：癒术士大赛（/ladders/*）与公会训练（/rob_battles/*）的规则、
// 领域类型与持久层接口。证据与重建值口径见
// docs/18-癒术士大赛与公会训练回放统计.md（下称 doc18）。
package cn35game

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	mathrand "math/rand"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrPvPLevel       = errors.New("cn35game: 等级不足，无法参加")
	ErrPvPNoCount     = errors.New("cn35game: 今日挑战次数已用完")
	ErrPvPNoDefense   = errors.New("cn35game: 未设置防御")
	ErrPvPAttackDeck  = errors.New("cn35game: 攻击编队未满员")
	ErrPvPOpponent    = errors.New("cn35game: 对手不存在")
	ErrPvPSelf        = errors.New("cn35game: 不能挑战自己")
	ErrPvPSession     = errors.New("cn35game: 对战会话不存在")
	ErrPvPState       = errors.New("cn35game: 对战会话状态不允许该操作")
	ErrPvPDifficulty  = errors.New("cn35game: 难度非法")
	ErrPvPMonster     = errors.New("cn35game: 魔宠不存在")
	ErrPvPUnit        = errors.New("cn35game: 同伴不存在")
	ErrPvPCareerLimit = errors.New("cn35game: 该职业编入人数已达上限")
	ErrPvPGrowFull    = errors.New("cn35game: 增幅已达上限")
	ErrPvPPotion      = errors.New("cn35game: 药水不足")
	ErrPvPTicket      = errors.New("cn35game: 当前不能购买该入场券")
	ErrPvPGold        = errors.New("cn35game: 金币不足")
	ErrPvPNoReward    = errors.New("cn35game: 没有可领取的排行奖励")
	ErrPvPInvalidArg  = errors.New("cn35game: 参数无效")
)

// pvpLoc 两个玩法的切日/结算时区（UTC+8）。
var pvpLoc = time.FixedZone("UTC+8", 8*3600)

// LadderRules 癒术士大赛规则。【原服观测】项见 doc18 §2.2，其余为【重建值】（doc18 §4）。
var LadderRules = struct {
	MinLevel      int
	DailyFree     int
	SlotRates     []float64
	SeasonAnchor  time.Time
	SeasonWeeks   int
	FirstSeasonNo int
	TrashIDs      []int
	TrashCount    int
	TrashStartMs  int
	TrashGapMs    int
	TrashPower    int
	BossPower     int
	WaveLength    int
	TimeLimitSec  int
	InitialPower  int
	Exp           int
	GuildAsset    int
	GachaPoint    int
	PotionMin     int
	PotionMax     int
	CandidateMax  int
	DefensePool   []int
	TierScore     map[string]int
	TierReward    map[string]LadderReward
	RewardTTL     time.Duration
	GrowBasePrice int
	GrowAntiPrice int
}{
	MinLevel:      15,
	DailyFree:     3,
	SlotRates:     []float64{1, 1.2, 1.4, 1.6, 1.8},
	SeasonAnchor:  time.Date(2017, 10, 31, 21, 0, 0, 0, pvpLoc),
	SeasonWeeks:   4,
	FirstSeasonNo: 32,
	TrashIDs:      []int{1, 41, 42, 43, 44},
	TrashCount:    150,
	TrashStartMs:  1000,
	TrashGapMs:    4000,
	TrashPower:    80,
	BossPower:     300,
	WaveLength:    3000,
	TimeLimitSec:  300,
	InitialPower:  1000,
	Exp:           25,
	GuildAsset:    50,
	GachaPoint:    30,
	PotionMin:     3,
	PotionMax:     8,
	CandidateMax:  20,
	DefensePool:   []int{26, 81, 88, 22, 95, 112, 109, 33, 100019, 355, 179, 100011},
	TierScore:     map[string]int{"SS": 1000, "S": 600, "A": 300, "B": 0},
	TierReward: map[string]LadderReward{
		"SS": {GachaPoint: 8000, Medal: 40},
		"S":  {GachaPoint: 4000, Medal: 30},
		"A":  {GachaPoint: 2500, Medal: 12},
		"B":  {GachaPoint: 1000, Medal: 8},
	},
	RewardTTL:     7 * 24 * time.Hour,
	GrowBasePrice: 50,
	GrowAntiPrice: 25,
}

// LadderTiers 分段展示顺序（ladder_info 行序；rank_index 即此下标）。
var LadderTiers = []string{"SS", "S", "A", "B"}

// LadderDifficulty 难度：防御魔宠出场间隔、boss 系数、积分倍率。
// 普通/极难的间隔与系数为【原服观测】，困难为【推断】；积分倍率取
// consts.ladder_difficulty 文案（原服观测）。
type LadderDifficulty struct {
	IntervalSec int
	Factor      float64
	ScoreMul    float64
}

var LadderDifficulties = map[string]LadderDifficulty{
	"normal":    {IntervalSec: 120, Factor: 1, ScoreMul: 1},
	"hard":      {IntervalSec: 80, Factor: 4, ScoreMul: 1.125},
	"very_hard": {IntervalSec: 40, Factor: 15, ScoreMul: 1.25},
}

// LadderDifficultyConsts preset_data.consts.ladder_difficulty（原服观测原值）。
func LadderDifficultyConsts() map[string]any {
	return map[string]any{
		"normal": "积分x1", "hard": "积分x1.125", "very_hard": "积分x1.25",
		"impossable": "积分x1.5", "impossible_lock": true,
	}
}

// LadderGrowTab 魔宠增幅页签（原服观测：grow_type 1 体力 / 2 攻击力 / 3 元素伤害）。
type LadderGrowTab struct {
	Type     int
	Key      string
	TabName  string
	ItemName string
	Icon     string
	MaxRate  int
	Step     int
	InfoFmt  string
}

var LadderGrowTabs = []LadderGrowTab{
	{Type: 1, Key: "hp", TabName: "体力", ItemName: "体力药水", Icon: "questdrop_potion_1", MaxRate: 1000, Step: 20, InfoFmt: "%s系防御魔宠体力增加"},
	{Type: 2, Key: "atk", TabName: "攻击力", ItemName: "攻击药水", Icon: "questdrop_potion_2", MaxRate: 1000, Step: 20, InfoFmt: "%s系防御魔宠攻击力增加"},
	{Type: 3, Key: "ele", TabName: "元素伤害", ItemName: "元素伤害药水", Icon: "questdrop_potion_3", MaxRate: 50, Step: 1, InfoFmt: "对%s系同伴伤害增加"},
}

// LadderGrowPrice 当前格升一级的药水价：基础 50 + 同页每个其他已升级属性 +25（魔阻，重建值）。
func LadderGrowPrice(steps map[[2]int]int, growType, attr int) int {
	other := 0
	for a := 1; a <= 5; a++ {
		if a != attr && steps[[2]int{growType, a}] > 0 {
			other++
		}
	}
	return LadderRules.GrowBasePrice + LadderRules.GrowAntiPrice*other
}

// RobRules 公会训练规则（doc18 §3.2/§4）。
var RobRules = struct {
	MaxRC        int
	TicketPrices []int64
	StartScore   int
	CareerLimit  map[int]int
	CandidateMax int
	RecordMax    int
	TimeLimitSec int
	WaveLength   int
}{
	MaxRC:        5,
	TicketPrices: []int64{0, 50000, 100000},
	StartScore:   1000,
	CareerLimit:  map[int]int{7: 2},
	CandidateMax: 10,
	RecordMax:    20,
	TimeLimitSec: 300,
	WaveLength:   3000,
}

// RobCareerLimitConsts preset_data.consts.rob_battle_career_limit（原服观测）。
func RobCareerLimitConsts() map[string]any {
	out := map[string]any{}
	for w, n := range RobRules.CareerLimit {
		out[fmt.Sprint(w)] = n
	}
	return out
}

// RobNPCPeriod 公会训练 NPC 阵容的日键（去掉 '-'，可嵌入 NPC id）。
func RobNPCPeriod(now time.Time) string { return strings.ReplaceAll(PvPDayKey(now), "-", "") }

// PvPDayKey 次数切日键（UTC+8 04:00，与星盘/面包店同口径）。
func PvPDayKey(now time.Time) string { return FatePeriodKey(now) }

// LadderPeriod 届/周信息。
type LadderPeriod struct {
	WeekIndex    int
	SeasonIndex  int
	WeekInSeason int
	SeasonNo     int
	WeekKey      string
	SeasonKey    string
	WeekEnd      time.Time
	SeasonEnd    time.Time
}

// LadderPeriodAt 以周二 21:00 为界的周序号与 28 天一届（重建值）。
func LadderPeriodAt(now time.Time) LadderPeriod {
	week := 7 * 24 * time.Hour
	d := now.Sub(LadderRules.SeasonAnchor)
	wi := int(math.Floor(float64(d) / float64(week)))
	si := int(math.Floor(float64(wi) / float64(LadderRules.SeasonWeeks)))
	return LadderPeriod{
		WeekIndex: wi, SeasonIndex: si,
		WeekInSeason: wi - si*LadderRules.SeasonWeeks,
		SeasonNo:     LadderRules.FirstSeasonNo + si,
		WeekKey:      fmt.Sprintf("W%d", wi),
		SeasonKey:    fmt.Sprintf("S%d", si),
		WeekEnd:      LadderRules.SeasonAnchor.Add(time.Duration(wi+1) * week),
		SeasonEnd:    LadderRules.SeasonAnchor.Add(time.Duration((si+1)*LadderRules.SeasonWeeks) * week),
	}
}

// LadderTierOf 积分分段（重建值）。
func LadderTierOf(score int) string {
	for _, t := range LadderTiers {
		if score >= LadderRules.TierScore[t] {
			return t
		}
	}
	return "B"
}

// LadderScoreGain 战胜积分：round(clamp(20+(对手-自己)/10, 5, 60) × 难度倍率)（重建值）。
func LadderScoreGain(me, opp int, diff string) int {
	base := 20 + float64(opp-me)/10
	base = math.Max(5, math.Min(60, base))
	mul := LadderDifficulties[diff].ScoreMul
	if mul == 0 {
		mul = 1
	}
	return int(math.Round(base * mul))
}

// RobScoreGain 公会训练战胜积分：clamp(30+(对手-自己)/20, 10, 60)（重建值；原服样本同分 +30）。
func RobScoreGain(me, opp int) int {
	v := 30 + float64(opp-me)/20
	return int(math.Round(math.Max(10, math.Min(60, v))))
}

// LadderReward 结算/掉落奖励。
type LadderReward struct {
	GachaPoint int    `json:"gacha_point,omitempty"`
	Medal      int    `json:"medal,omitempty"`
	GuildAsset int    `json:"guild_asset,omitempty"`
	Potions    [3]int `json:"potions,omitempty"`
}

// LadderRollDrops 预掷一场胜利掉落：药水 3–8 个（三种随机）+ 公会素材 + 金币（重建值，去叶子）。
func LadderRollDrops(r *mathrand.Rand) LadderReward {
	out := LadderReward{GuildAsset: LadderRules.GuildAsset, GachaPoint: LadderRules.GachaPoint}
	n := LadderRules.PotionMin + r.Intn(LadderRules.PotionMax-LadderRules.PotionMin+1)
	for i := 0; i < n; i++ {
		out.Potions[r.Intn(3)]++
	}
	return out
}

// PvPOpponent 对手（真实账号或 NPC 参赛者）。
type PvPOpponent struct {
	UserID       string
	NPC          bool
	Name         string
	Level        int
	LeaderUnitID int
	GuildName    string
	Score        int
	// 癒术士大赛：5 格防御魔宠（nil=空格）与增幅步数。
	Deck [5]*MonsterRecord
	Grow map[[2]int]int
	// 公会训练：防守 2 队 × 5 名同伴。
	DefUnits []UnitRecord
}

// LadderProfile 癒术士大赛账号档案（读面已按当前届/周/日归一）。
type LadderProfile struct {
	Score        int
	BestWinScore int
	AtkWin       int
	AtkTotal     int
	DefWin       int
	DefTotal     int
	DayUsed      int
	DayDefended  int
	Potions      [3]int
}

// LadderSettlement 周结算台账行。
type LadderSettlement struct {
	WeekKey   string
	Score     int
	Tier      string
	Reward    LadderReward
	Claimed   bool
	ExpiresAt int64
}

// LadderView 癒术士大赛读面聚合。
type LadderView struct {
	Profile LadderProfile
	Deck    [5]*MonsterRecord
	Grow    map[[2]int]int
	Spent   map[[2]int]int
	Pending []LadderSettlement
}

// LadderRankRow 排行行。
type LadderRankRow struct {
	UserID       string
	Name         string
	Level        int
	LeaderUnitID int
	Score        int
}

// LadderSession 癒术士大赛开战会话。
type LadderSession struct {
	BattleID      string
	UserID        string
	OpponentID    string
	OpponentNPC   bool
	OpponentName  string
	OpponentScore int
	Difficulty    string
	Rewards       LadderReward
	Status        string
	ScoreDelta    int
}

// LadderSettleResult 结算结果。
type LadderSettleResult struct {
	Session *LadderSession
	Already bool
	Win     bool
	Gain    int
	Score   int
}

// RobProfile 公会训练档案（已按当日归一）。
type RobProfile struct {
	Score         int
	RCLeft        int
	TicketsBought int
	AtkWin        int
	AtkTotal      int
	DefWin        int
	DefTotal      int
}

// RobRecord 被挑战记录（防守方视角）。
type RobRecord struct {
	RecordID      string
	AttackerID    string
	AttackerName  string
	AttackerLvl   int
	AttackerLead  int
	AttackerGuild string
	AttackerWin   bool
	ScoreDelta    int
	Revenged      bool
	CreatedAt     int64
}

// RobView 公会训练读面聚合。parties[type][party-1][slot] = unit_id（0=空）。
type RobView struct {
	Profile RobProfile
	Atk     [2][5]int
	Def     [2][5]int
	Records []RobRecord
}

// RobSession 公会训练开战会话。
type RobSession struct {
	BattleID      string
	UserID        string
	OpponentID    string
	OpponentNPC   bool
	OpponentName  string
	OpponentScore int
	RevengeID     string
	Status        string
	ScoreDelta    int
}

// RobSettleResult 结算结果。
type RobSettleResult struct {
	Session *RobSession
	Already bool
	Win     bool
	Gain    int
	Score   int
}

// PvPStore M10 持久层（cn35store.Store 实现，迁移 0057）。所有方法以 Store 时钟判定届/周/日。
type PvPStore interface {
	LadderView(userID string) (*LadderView, error)
	LadderSetDeck(userID string, slot int, uuid string) error
	LadderRemoveDeck(userID string, slot int) error
	LadderPlayers(userID string, limit int) ([]PvPOpponent, error)
	LadderPlayer(userID, opponentID string) (*PvPOpponent, error)
	LadderOpen(userID string, opp *PvPOpponent, difficulty string, ordinal int, drops LadderReward) (*LadderSession, []UnitRecord, []MonsterRecord, error)
	LadderPay(userID, battleID string) (*LadderSession, error)
	LadderSettle(userID, battleID string, win bool) (*LadderSettleResult, error)
	LadderGrowUpgrade(userID string, growType, attr, count int) error
	LadderGrowReset(userID string, growType int) error
	LadderRanking(limit int) ([]LadderRankRow, error)
	LadderClaimRewards(userID string) ([]LadderSettlement, error)

	RobView(userID string) (*RobView, error)
	RobSetDeck(userID, partyType string, partyID, slot, unitID int, on bool, weaponOf func(unitID int) int) error
	RobBuyTicket(userID string, ticketID int) error
	RobPlayers(userID string, limit int) ([]PvPOpponent, error)
	RobPlayer(userID, opponentID string) (*PvPOpponent, error)
	RobOpen(userID string, opp *PvPOpponent, revengeID string) (*RobSession, []UnitRecord, []MonsterRecord, error)
	RobPay(userID, battleID string) (*RobSession, error)
	RobSettle(userID, battleID string, win bool) (*RobSettleResult, error)
}

// ---- NPC 参赛者（确定性；doc18 §4） ----

var pvpNPCNames = []string{
	"见习癒术士", "流浪的癒术士", "王国骑士候补", "森林的旅人", "海港的少年",
	"沙漠商队护卫", "雪原的猎人", "魔法学徒", "铃之歌者", "钟楼的守望者",
	"花园的园丁", "星见塔学者", "古堡的执事", "草原牧羊人", "边境哨兵",
	"湖畔的诗人", "工坊的发明家", "神殿的巫女", "竞技场新秀", "远方的来客",
}

// NPCOpponentID NPC 参赛者 id（"npc-<玩法>-<届/日键>-<序号>"）。
func NPCOpponentID(kind, periodKey string, idx int) string {
	return fmt.Sprintf("npc-%s-%s-%d", kind, periodKey, idx)
}

// ParseNPCOpponentID 解析 NPC id（npc-<kind>-<period>-<idx>，period 不含 '-'）；非 NPC 返回 ok=false。
func ParseNPCOpponentID(id string) (kind, periodKey string, idx int, ok bool) {
	parts := strings.SplitN(id, "-", 4)
	if len(parts) != 4 || parts[0] != "npc" || parts[1] == "" || parts[2] == "" {
		return "", "", 0, false
	}
	i, err := strconv.Atoi(parts[3])
	if err != nil || i < 0 {
		return "", "", 0, false
	}
	return parts[1], parts[2], i, true
}

func pvpSeed(parts ...any) *mathrand.Rand {
	h := fnv.New64a()
	fmt.Fprint(h, parts...)
	return mathrand.New(mathrand.NewSource(int64(h.Sum64() & 0x7fffffffffffffff)))
}

// ladderNPCScoreOffsets NPC 积分相对玩家积分的偏移（20 个，重建值）。
var ladderNPCScoreOffsets = []int{-150, -120, -90, -60, -40, -20, -10, 0, 10, 20, 30, 40, 60, 80, 100, 120, 150, 180, 220, 260}

func npcName(kind string, idx int) string {
	return pvpNPCNames[idx%len(pvpNPCNames)]
}

func pickUnitIcons(pool []int, r *mathrand.Rand) int {
	if len(pool) == 0 {
		return 0
	}
	return pool[r.Intn(len(pool))]
}

// LadderNPC 生成第 idx 名 NPC 参赛者（届键决定阵容，积分随玩家积分浮动）。
// unitPool 为素材齐全的同伴 id（头像来源）。
func LadderNPC(cat UnitCatalog, unitPool []int, seasonKey string, idx, myScore, myLevel int) *PvPOpponent {
	r := pvpSeed("ladder", seasonKey, idx)
	off := ladderNPCScoreOffsets[idx%len(ladderNPCScoreOffsets)]
	score := myScore + off
	if score < 0 {
		score = 0
	}
	lvl := myLevel + r.Intn(21) - 10
	if lvl < LadderRules.MinLevel {
		lvl = LadderRules.MinLevel
	}
	o := &PvPOpponent{
		UserID: NPCOpponentID("ladder", seasonKey, idx), NPC: true,
		Name: npcName("ladder", idx), Level: lvl, Score: score, Grow: map[[2]int]int{},
	}
	if cat == nil {
		return o
	}
	o.LeaderUnitID = pickUnitIcons(unitPool, r)
	pool := []int{}
	for _, id := range LadderRules.DefensePool {
		if m, err := cat.Monster(id); err == nil && m != nil {
			pool = append(pool, id)
		}
	}
	if len(pool) == 0 {
		return o
	}
	n := 3 + r.Intn(3)
	for i := 0; i < n && i < 5; i++ {
		scale := fmt.Sprintf("%.3f", 1.3+r.Float64()*0.5)
		o.Deck[i] = &MonsterRecord{
			UUID:      fmt.Sprintf("%s-m%d", o.UserID, i),
			MonsterID: pool[r.Intn(len(pool))],
			Level:     1,
			Scale:     &scale,
			RealScale: &scale,
		}
	}
	return o
}

// RobNPC 生成第 idx 名公会训练 NPC（日键决定阵容）：从 unitPool（素材齐全的同伴）
// 抽 10 名，等级随玩家等级。
func RobNPC(cat UnitCatalog, unitPool []int, dayKey string, idx, myScore, myLevel int) *PvPOpponent {
	r := pvpSeed("rob", dayKey, idx)
	off := ladderNPCScoreOffsets[(idx*2)%len(ladderNPCScoreOffsets)]
	score := myScore + off
	if score < 0 {
		score = 0
	}
	lvl := myLevel + r.Intn(11) - 5
	if lvl < 1 {
		lvl = 1
	}
	o := &PvPOpponent{
		UserID: NPCOpponentID("rob", dayKey, idx), NPC: true,
		Name: npcName("rob", idx+7), Level: lvl, Score: score,
	}
	if cat == nil {
		return o
	}
	ids := append([]int{}, unitPool...)
	sort.Ints(ids)
	heal := 0
	seen := map[int]bool{}
	for tries := 0; len(o.DefUnits) < 10 && tries < 400 && len(ids) > 0; tries++ {
		id := ids[r.Intn(len(ids))]
		if seen[id] {
			continue
		}
		u, err := cat.Unit(id)
		if err != nil || u == nil || u.Rare < 2 || u.Rare > 5 {
			continue
		}
		if lim, ok := RobRules.CareerLimit[u.Weapon]; ok && heal >= lim {
			continue
		}
		if _, ok := RobRules.CareerLimit[u.Weapon]; ok {
			heal++
		}
		seen[id] = true
		unitLvl := 10 + lvl/2
		if unitLvl > 70 {
			unitLvl = 70
		}
		o.DefUnits = append(o.DefUnits, UnitRecord{UnitID: id, Level: unitLvl})
	}
	if len(o.DefUnits) > 0 {
		o.LeaderUnitID = o.DefUnits[0].UnitID
	}
	return o
}
