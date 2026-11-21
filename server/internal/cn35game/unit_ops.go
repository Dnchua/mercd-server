// unit_ops.go P0 写面（2026-09-28 Note）：同伴队长/助战/保护位 + 魔宠
// 装备/卸下/保护位的领域契约。
//
// 归属语义：所有"目标不在本账号"的判定统一归一为 NotOwned 哨兵——
// 不向客户端区分"对象不存在"与"属他人账号"，避免提供探测面。
package cn35game

import "errors"

var (
	// ErrUnitNotOwned 会话账号未持有该 unit_id（含跨账号请求）。
	ErrUnitNotOwned = errors.New("cn35game: 目标同伴不在本账号库存")
	// ErrUnitRoleProtected 队长或助战同伴不可移入待机所。
	ErrUnitRoleProtected = errors.New("cn35game: 队长或助战同伴不可待机")
	// ErrUnitAlreadyWaiting 目标同伴已处于待机所。
	ErrUnitAlreadyWaiting = errors.New("cn35game: 目标同伴已在待机所中")
	// ErrUnitNotWaiting 目标同伴不在待机所中，不可执行同行召回。
	ErrUnitNotWaiting = errors.New("cn35game: 目标同伴不在待机所中")
	// ErrMonsterNotOwned 会话账号未持有该魔宠 uuid（含跨账号请求）。
	ErrMonsterNotOwned = errors.New("cn35game: 目标魔宠不在本账号库存")
	// ErrMonsterNotEquipped off 请求命中了未装备的魔宠。
	ErrMonsterNotEquipped = errors.New("cn35game: 目标魔宠未装备")
	// ErrMonsterHostMismatch off 请求指定的宿主与魔宠实际宿主不一致。
	ErrMonsterHostMismatch = errors.New("cn35game: 魔宠宿主与请求不一致")
	// ErrMonsterScaleCap 魔宠已达 Size 上限（scale_limit 封顶）。
	ErrMonsterScaleCap = errors.New("cn35game: 魔宠已达强化上限")
	// ErrMonsterRarePowerup 稀有魔宠（is_rare=1）强化：成本表
	// rare_monster_category_config 未下发，不得按普通价成交。
	ErrMonsterRarePowerup = errors.New("cn35game: 稀有魔宠强化配置未下发")
	// ErrMonsterPowerupItem 强化素材与魔宠属性/体型档位不符
	// （ScaleCategory：≤1.35 种子 / ≤1.55 花蕾 / ≤1.70 花）。
	ErrMonsterPowerupItem = errors.New("cn35game: 强化素材与魔宠档位不符")
	// ErrUnitSellProtected 同伴已锁定、担任队长/助战或不可出售。
	ErrUnitSellProtected = errors.New("cn35game: 目标同伴已锁定、担任队长/助战或不可出售")
	// ErrUnitSellMinKeep 出售后同行同伴数量不得少于 5 名（UnitButtonsListComponent.cs:646）。
	ErrUnitSellMinKeep = errors.New("cn35game: 出售后同行同伴数量不得少于 5 名")
	// ErrUnitEvolutionInvalid 目标同伴不可进化或等级未满足进化条件。
	ErrUnitEvolutionInvalid = errors.New("cn35game: 目标同伴不可进化或未满足进化等级")
	// ErrUnitEvolutionOwned 已持有进化后的目标同伴。
	ErrUnitEvolutionOwned = errors.New("cn35game: 已持有进化后的目标同伴")
	// ErrUnitExchangeInvalid 能力互换目标不合法或无等级/觉醒差异。
	ErrUnitExchangeInvalid = errors.New("cn35game: 能力互换目标不合法或无等级/觉醒差异")
	// ErrCurrencyInsufficient 金币或钻石余额不足。
	ErrCurrencyInsufficient = errors.New("cn35game: 货币余额不足")
	// ErrMonsterSellProtected 目标魔宠已锁定、已装备或处于公会防卫。
	ErrMonsterSellProtected = errors.New("cn35game: 目标魔宠已锁定、已装备或处于公会防卫")
	// ErrMonsterEvoInvalid 魔宠进化或融合参数/素材条件不满足。
	ErrMonsterEvoInvalid = errors.New("cn35game: 魔宠进化或融合参数/素材条件不满足")
)

// UnitOpsStore 同伴写面。
//
// 实现必须在**单个事务**内完成归属校验与写入。leader/helper 两字段
// 各自独立（UserUnitDetail.cs:346-360 分别比较，无互斥证据）——
// 同一 unit_id 可同时占据两槽位，实现不得互清。
type UnitOpsStore interface {
	// SetLeaderUnitID 写 cn35_player_profiles.leader_unit_id。
	SetLeaderUnitID(userID string, unitID int) error
	// SetHelperUnitID 写 cn35_player_profiles.helper_unit_id（迁移 0016）。
	SetHelperUnitID(userID string, unitID int) error
	// SetUnitProtected 写 cn35_player_units.protected。
	SetUnitProtected(userID string, unitID int, protected bool) error
	// WaitUnits 批量将同行同伴移入待机所（/albums/wait）：
	// 同事务校验非队长/助战、归属且当前同行，自动卸下符文/魔宠并移出编队。
	WaitUnits(userID string, unitIDs []int) error
	// BackUnits 批量将待机所同伴召回同行列表（/albums/back）。
	BackUnits(userID string, unitIDs []int) error
	// SellUnits 批量出售同行同伴（/user_units/sell）：
	// 同事务校验非队长/助战、未保护、出售后同行数 >= 5，自动卸下符文/魔宠、
	// 清理编队槽位并入账 gacha_point。
	SellUnits(userID string, unitIDs []int, prices map[int]int64) (int64, error)
	// EvolveUnit 同伴进化（/user_units/evolution）：
	// 同事务校验等级 >= reqBaseMaxLv、扣除进化素材、将 fromUnitID 进化为 toUnitID
	// （level=1，duplicated_count 折半继承），并级联更新编队/队长/助战/魔宠/符文宿主。
	EvolveUnit(userID string, fromUnitID, toUnitID, reqBaseMaxLv int, needItems map[string]int) (UnitRecord, UnitRecord, error)
	// ExchangeUnitAbility 同伴能力互换（/unit_ability_exchange/exchange_unit_ability）：
	// 同事务校验双方同行且存在等级或觉醒数差异，优先扣除 1 颗能力互换石（power_stone），
	// 不足则扣除 10 钻（coin），互换双方 level 与 duplicated_count。
	ExchangeUnitAbility(userID string, unitAID, unitBID int) (UnitRecord, UnitRecord, error)
}

// MonsterOpsStore 魔宠写面。装备关系在 monsters.unit_id 与
// units.monster_uuid 两列间维护，实现必须同事务更新双侧。
type MonsterOpsStore interface {
	// EquipMonster 魔宠装上同伴：两对象都必须属本账号；魔宠原宿主与
	// 目标同伴原魔宠在同一事务内清下。
	EquipMonster(userID, uuid string, unitID int) error
	// UnequipMonster 魔宠卸下：unitID<0 表示未指定宿主（按实际宿主卸）；
	// unitID>=0 时必须与实际宿主一致，否则 ErrMonsterHostMismatch。
	UnequipMonster(userID, uuid string, unitID int) error
	// SetMonsterProtected 逐 uuid 写 protected；任一不属本账号整体回滚。
	SetMonsterProtected(userID string, uuids []string, protected bool) error
	// MonsterPowerup 魔宠强化（MonsterPowerupModal.cs:184 契约）。
	MonsterPowerup(userID, uuid string, attrID, powerupItemID, count int) (*MonsterPowerupResult, error)
	// SellMonsters 批量出售魔宠（/user_monsters/sell）：
	// 同事务校验未装备、未保护、非公会防卫魔宠，删除并入账 gacha_point。
	SellMonsters(userID string, uuids []string, prices map[string]int64) (int64, error)
	// EvolveEgg 提炼进化之卵（/user_monsters/evolve_egg）：
	// 同事务消耗指定数量同属性 1★ 完全体魔宠 + 神秘水晶 + 金币，产出对应进化之卵。
	EvolveEgg(userID string, attrID, consumeMonsterCount int, eligibleMonsterIDs map[int]bool) (int, []string, error)
	// EvolveMonster 魔宠融合进化（/user_monsters/evolve）：
	// 同事务消耗主魔宠 + 副素材魔宠 + 进化之卵 + 金币，生成目标融合 4★ 魔宠。
	EvolveMonster(userID, mainUUID string, subUUIDs []string, eggItemID, targetMonsterID int, targetScaleLimit string, consumeGold int64, addFusionDegrees int) (MonsterRecord, error)
}

// MonsterPowerupResult 强化结果：写入后的 Size（十进制字符串与读面同形态）。
type MonsterPowerupResult struct {
	UUID      string
	Scale     string
	RealScale string
	// ChargedCount 实际扣费的素材件数（封顶截断时可能小于请求 count）。
	ChargedCount int
	// Duplicate 并发同参数在途去重命中：复用首个结果，未二次扣物品。
	Duplicate bool
}
