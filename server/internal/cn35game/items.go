// items.go ITEM350-02/03/07/09：物品目录、账号库存、资源副本运营配置、
// 徽章交易所与觉醒消费的领域类型与接口契约。
//
// 设计真源：notes/proposed/2026-09-30-cn35-items-resources.md。
// 证据边界：15 个培养素材 content_id=1..15 由 PowerupItem.cs:52-66 证实；
// 觉醒书 content_id=101..104、user_items_hash 分类键归属、/medal_items/
// 分组字段与 /user_units/awake 上行路径均为暂定契约（待 ITEM350-01）。
package cn35game

import "errors"

var (
	// ErrItemNotFound 物品键/内容号不在目录或已停用。
	ErrItemNotFound = errors.New("cn35game: 物品不存在或已停用")
	// ErrItemInsufficient 玩家物品库存不足（条件扣减未命中）。
	ErrItemInsufficient = errors.New("cn35game: 物品库存不足")
	// ErrGoodsNotFound 徽章商品不存在/未上架。
	ErrGoodsNotFound = errors.New("cn35game: 商品不存在或已下架")
	// ErrMedalInsufficient 徽章余额不足。
	ErrMedalInsufficient = errors.New("cn35game: 徽章不足")
	// ErrAwakeCap 已达觉醒上限（duplicated_count 满）。
	ErrAwakeCap = errors.New("cn35game: 已达觉醒上限")
	// ErrAwakeUnitNotFound 觉醒目标不在本账号库存。
	ErrAwakeUnitNotFound = errors.New("cn35game: 觉醒目标不在本账号库存")
	// ErrDuplicateRequest 幂等键重放（请求摘要一致，已入账）。
	ErrDuplicateRequest = errors.New("cn35game: 请求已处理（幂等重放）")
	// ErrDuplicateRequestConflict 幂等键命中不同操作/参数（拒绝重放）。
	ErrDuplicateRequestConflict = errors.New("cn35game: 幂等键已用于不同请求")
	// ErrPurchaseLimit 商品达到账号累计限购上限。
	ErrPurchaseLimit = errors.New("cn35game: 商品已达限购上限")
	// ErrInvalidRequest 请求参数非法（商品 id/数量/幂等参数等）。
	ErrInvalidRequest = errors.New("cn35game: 请求参数非法")
	// ErrNoResourceConfig 资源副本尚无已发布版本。
	ErrNoResourceConfig = errors.New("cn35game: 资源副本未发布配置")
	// ErrResourceVersionUnknown 回滚目标版本不存在。
	ErrResourceVersionUnknown = errors.New("cn35game: 配置版本不存在")
	// ErrResourceDraft 草稿校验失败（发布前置门禁）。
	ErrResourceDraft = errors.New("cn35game: 资源副本草稿校验失败")
	// ErrItemNotUsable 物品存在但没有可用的使用效果。
	ErrItemNotUsable = errors.New("cn35game: 该物品不支持使用")
)

// ItemDef 物品目录行（cn35_item_catalog）。content_id 是客户端协议物品号；
// icon_asset_name 是 common_icon 资源键，两者独立。
type ItemDef struct {
	ItemKey       string `json:"item_key"`
	Kind          string `json:"kind"` // material|book|consumable
	ContentID     int    `json:"content_id"`
	Name          string `json:"name"`
	IconAssetName string `json:"icon_asset_name"`
	Element       int    `json:"element"`      // 1炎2水3风4光5暗；0=无属性
	Form          int    `json:"form"`         // 1种子/2花蕾/3花；0=无形态
	Rare          int    `json:"rare"`         // 书籍=对应星级；其余 0
	BagCategory   string `json:"bag_category"` // user_items_hash 分类键（1=觉醒之书 2=进化素材，见迁移 0021）
	Enabled       bool   `json:"enabled"`
}

// PlayerItem 账号库存行投影（含目录定义，便于读面组装）。
type PlayerItem struct {
	ItemKey  string  `json:"item_key"`
	Quantity int     `json:"quantity"`
	Def      ItemDef `json:"def"`
}

// ItemStore 物品目录与账号库存读面；写原语在 cn35store 事务方法上。
type ItemStore interface {
	// ItemCatalog 全量目录（含停用行；读面自行过滤 enabled）。
	ItemCatalog() ([]ItemDef, error)
	// PlayerItems 账号库存（quantity>0），按 item_key 升序。
	PlayerItems(userID string) ([]PlayerItem, error)
}

// ---- 资源副本/徽章商品运营配置 ----

// ResourceDrop 一条掉落规则：独立概率 chance_bp（万分比），命中后数量在
// [min_count,max_count] 闭区间等概率取值。
type ResourceDrop struct {
	Seq        int    `json:"seq"`
	RewardType string `json:"reward_type"` // item|gacha_point
	ItemKey    string `json:"item_key,omitempty"`
	ChanceBP   int    `json:"chance_bp"`
	MinCount   int    `json:"min_count"`
	MaxCount   int    `json:"max_count"`
}

// ResourceWave 一只出场怪物（hp_rate_bp/at_rate_bp 为千分比整倍率）。
// Hp/At/KillSp 为绝对终值覆盖（符文本怪模板基值不可靠时用）；0=按倍率路径。
type ResourceWave struct {
	WaveNo    int  `json:"wave_no"`
	Slot      int  `json:"slot"`
	MonsterID int  `json:"monster_id"`
	ScaleBP   int  `json:"scale_bp"`
	HpRateBP  int  `json:"hp_rate_bp"`
	AtRateBP  int  `json:"at_rate_bp"`
	IsBoss    bool `json:"is_boss,omitempty"`
	Hp        int  `json:"hp,omitempty"`
	At        int  `json:"at,omitempty"`
	KillSp    int  `json:"kill_sp,omitempty"`
}

// ResourceRuneDrop 符文本掉落配置：小怪/王宠独立基础概率（万分比），
// 四档品质权重各自合计 10000（並/良/特良/極良）。
type ResourceRuneDrop struct {
	MobChanceBP  int    `json:"mob_chance_bp"`
	BossChanceBP int    `json:"boss_chance_bp"`
	MobQBP       [4]int `json:"mob_q_bp"`
	BossQBP      [4]int `json:"boss_q_bp"`
}

// ResourceQuestDraft 一个资源关卡的草稿（quest_key 稳定键）。
type ResourceQuestDraft struct {
	QuestKey string `json:"quest_key"`
	QuestID  int    `json:"quest_id"`
	AreaID   int    `json:"area_id"`
	StageID  int    `json:"stage_id"`
	Name     string `json:"name"`
	AP       int    `json:"ap"`
	Exp      int    `json:"exp"`
	Sort     int    `json:"sort"`
	Open     bool   `json:"open"`
	// InitialPower 开局 SP（0=服务端默认 500）。
	InitialPower int `json:"initial_power,omitempty"`
	// PowerupRateBP 出场怪强化倍率千分比（0=默认 2500 即 2.5×）。
	PowerupRateBP int `json:"powerup_rate_bp,omitempty"`
	// TimeLimitSec 波次限时秒（0=不限时，wave_conf.time_limit）。
	TimeLimitSec int `json:"time_limit_sec,omitempty"`
	// SlashRateBP/GunRateBP 斩/铳武器优势倍率千分比（0=1.0×）；
	// weapon_rate_hash 按难度键下发。
	SlashRateBP int            `json:"slash_rate_bp,omitempty"`
	GunRateBP   int            `json:"gun_rate_bp,omitempty"`
	Drops       []ResourceDrop `json:"drops,omitempty"`
	Waves       []ResourceWave `json:"waves,omitempty"`
	// RuneDrop 符文本掉落配置（nil=非符文本）。
	RuneDrop *ResourceRuneDrop `json:"rune_drop,omitempty"`
}

// MedalGoodDraft 徽章商品草稿；PurchaseLimit=nil 表示常驻不限购。
// Price=0 为免费商品：客户端 ExchangeImageButton 只在 price>0 时才发起
// 兑换，故 wire 层对免费商品下发 price=1 的显示下限，服务端实扣仍为 0。
type MedalGoodDraft struct {
	GoodsKey      string `json:"goods_key"`
	GoodsID       int    `json:"goods_id"`
	ItemKey       string `json:"item_key"`
	Name          string `json:"name"`
	Price         int    `json:"price"`
	PurchaseLimit *int   `json:"purchase_limit"`
	Sort          int    `json:"sort"`
	// Group 交易所页签分组：''=按物品 kind 归组（material→素材页、其余→
	// 道具页），或显式 'item'/'powerup'/'story_unit'/'rare_medal'/
	// 'friend_medal'（对应 medal_right_hash 五个分组键）。
	Group   string `json:"group_key"`
	Enabled bool   `json:"enabled"`
}

// ResourceConfig 发布快照/草稿视图：quests+goods 同一版本语义。
// Version=0 表示草稿视图（未发布）。
type ResourceConfig struct {
	Version int64                `json:"version"`
	Quests  []ResourceQuestDraft `json:"quests"`
	Goods   []MedalGoodDraft     `json:"goods"`
	// Digest 快照内容哈希（sha256 hex）；草稿视图恒为空。
	Digest string `json:"digest,omitempty"`
}

// ResourceVersionRow 版本历史行（审计展示用）。
type ResourceVersionRow struct {
	Version   int64  `json:"version"`
	SHA256    string `json:"sha256"`
	Author    string `json:"author"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

// ResourceMonsterValidator 校验波次引用的魔宠模板可加载；实现通常是
// UnitCatalog.Monster(id)。nil 表示模板校验不可用，发布应失败而不是跳过。
type ResourceMonsterValidator func(monsterID int) error

// ResourceConfigReadStore 运行期只读面：战斗/商店读生效快照。
type ResourceConfigReadStore interface {
	// ActiveResourceConfig 生效版本快照；无发布返回 nil,nil（不算错误）。
	ActiveResourceConfig() (*ResourceConfig, error)
}

// ResourceAdminStore 草稿编辑 + 发布/回滚治理面（admin35 消费）。
type ResourceAdminStore interface {
	ResourceConfigReadStore
	// ResourceDrafts 当前草稿全量（Version=0）。
	ResourceDrafts() (*ResourceConfig, error)
	// SaveResourceQuest 草稿 upsert：quest 行 + drops/waves 整体替换。
	SaveResourceQuest(q ResourceQuestDraft, actor string) error
	// DeleteResourceQuest 草稿删除（连带 drops/waves）。
	DeleteResourceQuest(questKey, actor string) error
	// SaveMedalGood 商品草稿 upsert。
	SaveMedalGood(g MedalGoodDraft, actor string) error
	// DeleteMedalGood 商品草稿删除（生效快照不受影响）。
	DeleteMedalGood(goodsKey, actor string) error
	// ValidateResourceDrafts 返回发布前置校验错误；空切片表示可发布。
	ValidateResourceDrafts(validateMonster ResourceMonsterValidator) ([]string, error)
	// PublishResourceConfig 校验草稿 → 不可变快照 → 指针切换，同事务审计。
	PublishResourceConfig(author, note string, validateMonster ResourceMonsterValidator) (int64, string, error)
	// ActivateResourceVersion 指针回滚到既有版本。
	ActivateResourceVersion(version int64, actor, note string) error
	// ResourceVersions 版本历史（新→旧）与当前生效版本。
	ResourceVersions(limit int) ([]ResourceVersionRow, int64, error)
	// ResourceVersionsCount 版本总数（分页展示用）。
	ResourceVersionsCount() (int, error)
	// AuditLog 管理审计记录（资源/徽章页展示与追溯）。
	AuditLog(limit int) ([]AuditEntry, error)
}

// ---- 徽章交易 ----

// MedalExchangeResult 一次兑换的读回结果。
type MedalExchangeResult struct {
	GoodsID   int    `json:"goods_id"`
	ItemKey   string `json:"item_key"`
	Count     int    `json:"count"`
	Medal     int64  `json:"medal"`     // 扣后余额
	Duplicate bool   `json:"duplicate"` // request_key 命中台账（幂等重放）
}

// MedalStore 徽章商品读面 + 兑换写面。
type MedalStore interface {
	// MedalGoods 生效版本商品列表（无发布回退草稿为空集，不算错误）。
	MedalGoods() ([]MedalGoodDraft, error)
	// ExchangeMedal 单事务：商品启用→价格×count→medal 条件扣减→物品入账→
	// 台账。request_key 非空时按 (user_id,request_key) 幂等。
	ExchangeMedal(userID string, goodsID, count int, requestKey string) (*MedalExchangeResult, error)
}

// MedalGrantStore admin 徽章发放写面（幂等键 + 审计同事务）。
type MedalGrantStore interface {
	GrantMedal(userID string, amount int64, actor, reason, requestKey string) error
}

// ItemGrantStore admin 物品发放写面（幂等键 + 审计同事务）。
type ItemGrantStore interface {
	GrantItem(userID, itemKey string, count int, actor, reason, requestKey string) error
}

// ---- 觉醒（暂定契约）----

// AwakenResult 一次觉醒的读回结果。
type AwakenResult struct {
	UnitID          int  `json:"unit_id"`
	DuplicatedCount int  `json:"duplicated_count"`
	BookContentID   int  `json:"book_content_id"`
	Duplicate       bool `json:"duplicate"` // 并发重放命中在途去重
}

// AwakenStore 觉醒书消费写面（/user_units/awake，暂定契约）。
type AwakenStore interface {
	// AwakenUnit 单事务：归属→dup<maxDup→扣 awakening_book_<rare> 共 count 本→
	// duplicated_count+count（以 maxDup 封顶，超出部分按 ErrAwakeCap 拒绝）。
	// 书不存在视为 ErrItemNotFound。
	AwakenUnit(userID string, unitID, rare, maxDup, count int) (*AwakenResult, error)
}

// ---- 物品使用（/user_items/use/）----

// UseItemResult 一次物品使用的读回结果。
type UseItemResult struct {
	ItemKey   string `json:"item_key"`
	Count     int    `json:"count"`
	Duplicate bool   `json:"duplicate"` // 并发重放命中在途去重
}

// ItemUseStore 物品使用写面：按 content_id 解析目录行 → 校验可用 →
// 扣数量 → 应用效果（ap_potion/bp_potion 回满 AP/BP）。
type ItemUseStore interface {
	// UseItem 单事务。count<=0 或物品无效果 → ErrInvalidRequest/
	// ErrItemNotUsable；库存不足 → ErrItemInsufficient。
	UseItem(userID string, contentID, count int) (*UseItemResult, error)
}

// ItemUnitGrantResult 契约书/星级信等消耗道具发卡的单事务结算回执。
type ItemUnitGrantResult struct {
	UnitID           int  `json:"unit_id"`
	Level            int  `json:"level"`
	DupBefore        int  `json:"dup_before"`
	DupAfter         int  `json:"dup_after"`
	CompensatedMedal int  `json:"compensated_medal"`
	Newcomer         bool `json:"newcomer"`
}

// ItemExchangeStore 契约书 6 合 1 兑换与契约书/信券单事务发卡契约。
type ItemExchangeStore interface {
	// ExchangePlayerItem 单事务：扣 costCount 个 fromKey，发放 1 个 toKey。
	ExchangePlayerItem(userID, fromKey, toKey string, costCount int) error
	// UseItemGrantUnit 单事务：扣 costCount 个 itemKey，入库 1 名同伴（或满觉转徽章）。
	UseItemGrantUnit(userID, itemKey string, costCount, unitID, dupCap, compMedal int) (*ItemUnitGrantResult, error)
}
