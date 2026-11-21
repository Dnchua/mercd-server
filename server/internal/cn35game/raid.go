package cn35game

import (
	"errors"
	"fmt"
	"math"
)

const (
	// RaidSingleBattleHPCap 单场下发给客户端战斗的 Boss 血量安全上限（防止 32 位 EncryptInt 溢出）。
	RaidSingleBattleHPCap = int64(2_000_000_000)
	// RaidDefaultBossID 图 3 实机同款光王宠「莫鲁菲涅」。
	RaidDefaultBossID = 688
	// RaidDefaultTTLSeconds 原版暴走魔宠存活时长（90 分钟）。
	RaidDefaultTTLSeconds = int64(5400)
	// RaidDefaultTriggerRate 常规探险通关遭遇暴走魔宠默认概率（可经配置改为 1.0）。
	RaidDefaultTriggerRate = 0.5
)

var (
	ErrRaidNotFound       = errors.New("cn35: raid instance not found")
	ErrRaidExpired        = errors.New("cn35: raid instance expired")
	ErrRaidAlreadyDone    = errors.New("cn35: raid instance already finished")
	ErrRaidActiveExists   = errors.New("cn35: active self raid already exists")
	ErrRaidNotOwner       = errors.New("cn35: raid not owned by caller")
	ErrRaidNotAttacked    = errors.New("cn35: raid must be attacked before calling help")
	ErrRaidBadDifficulty  = errors.New("cn35: invalid raid difficulty")
	ErrRaidBadHelpType    = errors.New("cn35: invalid raid help type")
	ErrRaidNoGuild        = errors.New("cn35: guild required for guild raid help")
	ErrRaidBadSlot        = errors.New("cn35: invalid raid pt shop slot")
	ErrRaidSoldOut        = errors.New("cn35: raid pt shop limit reached")
	ErrRaidNoPt           = errors.New("cn35: insufficient raid pt")
	ErrRaidBadSession     = errors.New("cn35: invalid raid battle session")
	ErrRaidBPPaidRequired = errors.New("cn35: raid battle bp not paid")
	ErrRaidForbidden      = errors.New("cn35game: 无权访问该暴走魔宠")
)

// RaidConfig 暴走魔宠活动运行期参数。
type RaidConfig struct {
	BossMonsterID int
	TriggerRate   float64
	TTLSeconds    int64
	SoloAssist    bool
}

// NormalizeRaidConfig 补齐暴走配置默认值与合法区间。
func NormalizeRaidConfig(cfg RaidConfig) RaidConfig {
	if cfg.BossMonsterID <= 0 {
		cfg.BossMonsterID = RaidDefaultBossID
	}
	if cfg.TTLSeconds <= 0 {
		cfg.TTLSeconds = RaidDefaultTTLSeconds
	}
	if cfg.TriggerRate < 0 {
		cfg.TriggerRate = 0
	} else if cfg.TriggerRate > 1 {
		cfg.TriggerRate = 1
	}
	return cfg
}

// RaidMonsterMeta 由目录解析的 Boss 元数据（在事务外读取后传入 store）。
type RaidMonsterMeta struct {
	MonsterID int
	Name      string
	AttrID    int
}

// RaidProfile 玩家暴走个人状态。
type RaidProfile struct {
	UserID          string
	RaidLv          int
	RaidPt          int64
	DiscoveredCount int
	KilledCount     int
}

// RaidInstance 一只遭遇的暴走魔宠实例。
type RaidInstance struct {
	RaidID         string
	OwnerUserID    string
	OwnerName      string
	GuildID        string
	MonsterID      int
	MonsterName    string
	AttrID         int
	Level          int
	MaxPoint       int64
	CurrentPoint   int64
	InitialPower   int
	Attacked       bool
	ViewerAttacked bool
	HelpGuild      bool
	HelpCommunity  bool
	Status         string // active | defeated | giveup | expired
	SpawnedAt      int64
	ExpiredAt      int64
	FinishedAt     int64
}

// RaidOverview /raid_monsters/ 主页全量聚合视图。
type RaidOverview struct {
	Profile       RaidProfile
	MyRaid        *RaidInstance
	GuildHelps    []RaidInstance
	CommunityHelp []RaidInstance
	MyHistory     []RaidInstance
}

// RaidDetail /raid_monsters/show 准备页详情。
type RaidDetail struct {
	Instance       *RaidInstance
	AssistFirstHit bool
}

// RaidBattleSession 单次暴走战斗会话（prepare → bp_paid → won|lost）。
type RaidBattleSession struct {
	BattleID       string
	RaidID         string
	UserID         string
	DifficultyID   string
	TimeLimit      int
	BPCost         int
	FreeFirst      bool
	Status         string
	ReportedDamage int64
	BattleHP       int64
	PartyID        string
}

// RaidHelpMessage 暴走协力求援聊天流消息投影（供 /raid_monsters/call_help 与 /users/messages 消费）。
type RaidHelpMessage struct {
	ID           string
	Category     string // "guild_comment" | "community_comment"
	RaidID       string
	OwnerUserID  string
	OwnerName    string
	LeaderUnitID int
	Level        int
	MonsterName  string
	CurrentPoint int64
	CreatedAt    int64
}

// RaidSettleResult 暴走结算（lose 退场 / result 胜利）回执。
type RaidSettleResult struct {
	Session        *RaidBattleSession
	Instance       *RaidInstance
	Already        bool
	Defeated       bool
	DamageDealt    int64
	ClearedLv      int
	GrantedLvBonus *RaidLvReward
	PtEarnedSelf   int64
}

// RaidDamageRow 暴走协力者伤害排行行。
type RaidDamageRow struct {
	UserID       string
	UserName     string
	GuildName    string
	UserLevel    int64
	LeaderUnitID int
	TotalDamage  int64
	HitCount     int
	IsNPC        bool
}

// RaidLvReward 击退 Lv 里程碑奖励条目（reconstruction）。
type RaidLvReward struct {
	TargetLv      int
	RewardKind    string // gacha_point | medal | coin | item | monster
	RewardKey     string
	MonsterID     int
	Amount        int
	RewardName    string
	IconAssetName string
}

// RaidPtShopGood 击退 pt 交换所商品条目（reconstruction）。
type RaidPtShopGood struct {
	SlotID        string
	RewardKind    string // monster | item | gacha_point
	RewardKey     string
	MonsterID     int
	UnitAmount    int
	Name          string
	Price         int
	Limit         int
	IconAssetName string
}

var raidHPAnchors = []struct {
	lv int
	hp int64
}{
	{1, 30_000},
	{50, 15_000_000},
	{150, 500_000_000},
	{233, 10_059_161_640},
}

// RaidMaxPoint 分段对数线性血量曲线〔reconstruction〕，精确命中 Lv.233=10,059,161,640 锚点。
func RaidMaxPoint(lv int) int64 {
	if lv <= 1 {
		return raidHPAnchors[0].hp
	}
	for _, a := range raidHPAnchors {
		if a.lv == lv {
			return a.hp
		}
	}
	for i := 0; i < len(raidHPAnchors)-1; i++ {
		a, b := raidHPAnchors[i], raidHPAnchors[i+1]
		if lv > a.lv && lv < b.lv {
			ratio := float64(lv-a.lv) / float64(b.lv-a.lv)
			v := float64(a.hp) * math.Pow(float64(b.hp)/float64(a.hp), ratio)
			return int64(math.Round(v))
		}
	}
	// Lv > 233：沿末段倍率继续外推，并安全封顶避免 int64 溢出。
	a := raidHPAnchors[len(raidHPAnchors)-2]
	b := raidHPAnchors[len(raidHPAnchors)-1]
	stepRatio := math.Pow(float64(b.hp)/float64(a.hp), 1.0/float64(b.lv-a.lv))
	v := float64(b.hp) * math.Pow(stepRatio, float64(lv-b.lv))
	const maxHP = 900_000_000_000
	if math.IsInf(v, 0) || math.IsNaN(v) || v > maxHP {
		return maxHP
	}
	return int64(math.Round(v))
}

// RaidBattleSafeHP 单场战斗下发的 Boss HP 上限（封顶 2,000,000,000 防止客户端 32 位 EncryptInt 溢出）。
func RaidBattleSafeHP(currentPoint int64) int {
	if currentPoint <= 0 {
		return 1
	}
	if currentPoint > RaidSingleBattleHPCap {
		return int(RaidSingleBattleHPCap)
	}
	return int(currentPoint)
}

// RaidInitialPower 保底初始 SP 曲线〔reconstruction〕，精确命中 Lv.1=1000 与 Lv.233=312500（图 4）。
func RaidInitialPower(lv int) int {
	if lv <= 1 {
		return 1000
	}
	if lv >= 233 {
		return 312500
	}
	t := float64(lv-1) / float64(233-1)
	v := 1000.0 + (312500.0-1000.0)*t*t
	rounded := int(math.Round(v/100.0)) * 100
	if rounded < 1000 {
		return 1000
	}
	if rounded > 312500 {
		return 312500
	}
	return rounded
}

// RaidWeaponRates 7 武器特攻倍率（每 5 级轮换一组优势武器；整十级或 >=150 级全武器 1.0）。
func RaidWeaponRates(lv int) map[string]float64 {
	out := make(map[string]float64, len(gwWeaponRateNames))
	for _, k := range gwWeaponRateNames {
		out[k] = 1.0
	}
	if lv <= 0 || lv >= 150 || lv%10 == 0 {
		return out
	}
	bucket := ((lv - 1) / 5) % 5
	switch bucket {
	case 0:
		out["slash"], out["rush"] = 1.5, 1.5
	case 1:
		out["hit"], out["arrow"] = 1.5, 1.5
	case 2:
		out["magic"], out["gun"] = 1.5, 1.5
	case 3:
		out["slash"], out["magic"] = 1.5, 1.5
	case 4:
		out["rush"], out["arrow"] = 1.5, 1.5
	}
	return out
}

// RaidDifficultySpec 解析暴走三档出击规格与协力首刀 BP 减免（图 4 实证 + Wiki 规则）。
func RaidDifficultySpec(difficultyID string, assistFirstHit bool) (timeLimitSec, bpCost int, ok bool) {
	switch difficultyID {
	case "normal":
		bpCost = 30
		if assistFirstHit {
			bpCost = 0
		}
		return 160, bpCost, true
	case "hard":
		bpCost = 60
		if assistFirstHit {
			bpCost = 30
		}
		return 200, bpCost, true
	case "very_hard":
		bpCost = 100
		if assistFirstHit {
			bpCost = 60
		}
		return 240, bpCost, true
	default:
		return 0, 0, false
	}
}

// RaidPtFromDamage 按暴走活动规则将累计伤害折算为击退 pt（每 10000 伤 1pt，参与者有效出击保底 10pt）。
func RaidPtFromDamage(totalDamage int64) int64 {
	if totalDamage <= 0 {
		return 0
	}
	pt := totalDamage / 10000
	if pt < 10 {
		pt = 10
	}
	return pt
}

// RaidLvBonusCatalog 击退 Lv 阶段里程碑奖励清单（reconstruction）。
func RaidLvBonusCatalog(bossMonsterID int, bossName string) []RaidLvReward {
	if bossMonsterID <= 0 {
		bossMonsterID = RaidDefaultBossID
	}
	if bossName == "" {
		bossName = "莫鲁菲涅"
	}
	bossIcon := fmt.Sprintf("monster/monster_square_%d", bossMonsterID)
	return []RaidLvReward{
		{TargetLv: 1, RewardKind: "gacha_point", RewardKey: "gacha_point", Amount: 30000, RewardName: "金币", IconAssetName: "common_icon/gold"},
		{TargetLv: 3, RewardKind: "item", RewardKey: "ap_potion", Amount: 2, RewardName: "元气之水", IconAssetName: "common_icon/item_67"},
		{TargetLv: 5, RewardKind: "medal", RewardKey: "medal", Amount: 20, RewardName: "徽章", IconAssetName: "common_icon/medal"},
		{TargetLv: 10, RewardKind: "coin", RewardKey: "coin", Amount: 1, RewardName: "钻石", IconAssetName: "common_icon/diamond"},
		{TargetLv: 15, RewardKind: "item", RewardKey: "awakening_book_2", Amount: 1, RewardName: "二星觉醒书", IconAssetName: "common_icon/item_1"},
		{TargetLv: 20, RewardKind: "item", RewardKey: "awakening_book_3", Amount: 1, RewardName: "三星觉醒书", IconAssetName: "common_icon/item_2"},
		{TargetLv: 30, RewardKind: "coin", RewardKey: "coin", Amount: 2, RewardName: "钻石", IconAssetName: "common_icon/diamond"},
		{TargetLv: 40, RewardKind: "monster", MonsterID: bossMonsterID, Amount: 1, RewardName: bossName, IconAssetName: bossIcon},
		{TargetLv: 50, RewardKind: "coin", RewardKey: "coin", Amount: 3, RewardName: "钻石", IconAssetName: "common_icon/diamond"},
		{TargetLv: 75, RewardKind: "item", RewardKey: "awakening_book_4", Amount: 1, RewardName: "四星觉醒书", IconAssetName: "common_icon/item_3"},
		{TargetLv: 100, RewardKind: "coin", RewardKey: "coin", Amount: 5, RewardName: "钻石", IconAssetName: "common_icon/diamond"},
		{TargetLv: 150, RewardKind: "item", RewardKey: "awakening_book_5", Amount: 1, RewardName: "五星觉醒书", IconAssetName: "common_icon/item_4"},
		{TargetLv: 200, RewardKind: "coin", RewardKey: "coin", Amount: 10, RewardName: "钻石", IconAssetName: "common_icon/diamond"},
	}
}

// RaidPtShopCatalog 击退 pt 交换所商品表（8 项商品，wire 层两两成行按 _0/_1 下发）。
func RaidPtShopCatalog(bossMonsterID int, bossName string) []RaidPtShopGood {
	if bossMonsterID <= 0 {
		bossMonsterID = RaidDefaultBossID
	}
	if bossName == "" {
		bossName = "莫鲁菲涅"
	}
	bossIcon := fmt.Sprintf("monster/monster_square_%d", bossMonsterID)
	return []RaidPtShopGood{
		{SlotID: "slot_1", RewardKind: "monster", MonsterID: bossMonsterID, UnitAmount: 1, Name: bossName, Price: 500, Limit: 5, IconAssetName: bossIcon},
		{SlotID: "slot_2", RewardKind: "item", RewardKey: "ap_potion", UnitAmount: 1, Name: "元气之水", Price: 100, Limit: 10, IconAssetName: "common_icon/item_67"},
		{SlotID: "slot_3", RewardKind: "item", RewardKey: "awakening_book_2", UnitAmount: 1, Name: "二星觉醒书", Price: 300, Limit: 5, IconAssetName: "common_icon/item_1"},
		{SlotID: "slot_4", RewardKind: "item", RewardKey: "awakening_book_3", UnitAmount: 1, Name: "三星觉醒书", Price: 800, Limit: 3, IconAssetName: "common_icon/item_2"},
		{SlotID: "slot_5", RewardKind: "item", RewardKey: "awakening_book_4", UnitAmount: 1, Name: "四星觉醒书", Price: 2000, Limit: 2, IconAssetName: "common_icon/item_3"},
		{SlotID: "slot_6", RewardKind: "item", RewardKey: "awakening_book_5", UnitAmount: 1, Name: "五星觉醒书", Price: 5000, Limit: 1, IconAssetName: "common_icon/item_4"},
		{SlotID: "slot_7", RewardKind: "item", RewardKey: "powerup_item_14", UnitAmount: 1, Name: "光之花", Price: 150, Limit: 10, IconAssetName: "common_icon/powerup_item_14"},
		{SlotID: "slot_8", RewardKind: "gacha_point", RewardKey: "gacha_point", UnitAmount: 10000, Name: "金币 ×10,000", Price: 50, Limit: 50, IconAssetName: "common_icon/gold"},
	}
}

// RaidStore 暴走魔宠活动持久层接口（由 cn35store.Store 实现）。
type RaidStore interface {
	RaidOverview(userID string, now int64) (*RaidOverview, error)
	RaidHasActive(userID string, now int64) (bool, error)
	MaybeSpawnRaidOnQuestClear(userID string, meta RaidMonsterMeta, cfg RaidConfig, roll float64, now int64) (*RaidInstance, bool, error)
	RaidShow(userID, raidID string, now int64) (*RaidDetail, error)
	RaidOpenBattle(userID, raidID, difficulty string, ordinal, unitLimit int, now int64) (*RaidBattleSession, *RaidInstance, []UnitRecord, []MonsterRecord, error)
	RaidPayBP(userID, battleID string, now int64) (*RaidBattleSession, error)
	RaidReportLose(userID, battleID string, damage int64, bossMeta RaidMonsterMeta, now int64) (*RaidSettleResult, error)
	RaidReportWin(userID, battleID string, bossMeta RaidMonsterMeta, now int64) (*RaidSettleResult, error)
	RaidCallHelp(userID, helpType string, soloAssist bool, bossMeta RaidMonsterMeta, now int64) (*RaidInstance, error)
	RaidGiveup(userID, raidID string, now int64) error
	RaidDamageList(userID, raidID string, now int64) ([]RaidDamageRow, error)
	RaidHelpMessages(userID string, now int64) ([]RaidHelpMessage, error)
	RaidLvClaims(userID string) (int, map[int]bool, error)
	RaidPtShopState(userID string) (int64, map[string]int, error)
	RaidExchangePt(userID, slotID string, count int, good RaidPtShopGood, now int64) (int64, int, error)
}
