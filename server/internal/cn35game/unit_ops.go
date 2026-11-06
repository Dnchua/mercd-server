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
	// MonsterPowerup 魔宠强化（MonsterPowerupModal.cs:184 契约）：
	// 消耗 powerup_item_id（素材 content_id）对应的背包素材，每次 1 件；
	// real_scale += step×执行次数（step=普通 0.02 / 新魔宠 0.01）。
	// attrID 为魔宠模板属性（1炎..5暗），素材 element 必须与之相等且
	// form 必须等于当前体型档位（≤1.35 种子/≤1.55 花蕾/≤1.70 花），
	// 不符 ErrMonsterPowerupItem；普通魔宠上限边界 1.70（id==4），
	// 越界（如战斗掉落超限）ErrMonsterScaleCap，封顶前只执行/只扣
	// 上限内步数（ChargedCount）；is_rare=1 拒绝 ErrMonsterRarePowerup；
	// 物品不足 ErrItemInsufficient、目标非素材 ErrItemNotFound。
	MonsterPowerup(userID, uuid string, attrID, powerupItemID, count int) (*MonsterPowerupResult, error)
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
