// inventory.go CN 3.5.0 同伴/魔宠库存的领域类型与读取接口（PLAY350-06）。
//
// 字段语义锚定 handoffs/03/inventory-contract.md：
//   - UnitRecord.UnitID 即 user_units[].id = 模板 ID（行唯一性 = 账号+模板，
//     duplicated_count 表达堆叠；实例级区分不在本层）。
//   - MonsterRecord.UUID 为实例主键；UnitID 为装备宿主（nil=未装备）。
//   - 模板数值不落库：持久行只存实例态（level/duplicated/protected/…），
//     模板面由 cardcatalog 在 07 组装响应时合入。
//
// 写原语不在本接口：跨域写需要 *sql.Tx 贯穿，由 cn35store 暴露
// WithTx + InsertUnitTx/InsertMonsterTx/RecordGrantTx/GrantAppliedTx
// 供 08 发放在一个事务里完成"库存行 + 台账行"。
package cn35game

import (
	"errors"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

// UnitRecord 一条账号单位库存行（对应 cn35_player_units）。
type UnitRecord struct {
	UnitID          int    // 模板 ID（>0，cardcatalog.Unit 必须存在）
	Level           int    // >=1
	DuplicatedCount int    // 堆叠数 >=0
	Protected       bool   // 锁定
	Newcomer        bool   // 新获得标记
	MonsterUUID     string // 装备中的魔宠实例 uuid（""=未装备）
	Waiting         bool   // 待机所状态（true=待机中，不进入 /user_units/ 同行列表）
}

// MonsterRecord 一条账号魔宠库存行（对应 cn35_player_monsters）。
//
// Size 字段（STAT350-04，migration 0009）：以十进制字符串持久化（避免二进制
// 浮点漂移）。nil = 未证/降级——读面按显式 Size=1.000 处理并登记缺口，
// 不写伪值、不发伪布尔。
type MonsterRecord struct {
	UUID       string // 实例 ID（非空，账号内唯一）
	MonsterID  int    // 模板 ID（>0，cardcatalog.Monster 必须存在）
	UnitID     *int   // 装备宿主 user_units.unit_id；nil=未装备
	Level      int    // >=1
	Protected  bool
	Scale      *string // scale：展示用 Size（十进制字符串）
	RealScale  *string // real_scale：推进式计算用 Size（客户端预览同口径）
	ScaleLimit *string // scale_limit：强化上限（未证则 nil）
	IsRare     *bool   // is_rare：客户端档位表选择（7 档/4 档；未证则 nil）
}

// InventoryStore 库存读取面（07 消费：preset_data 注入 + 列表接口）。
// 空库存返回空切片，不算错误。
type InventoryStore interface {
	// Units 按账号列出单位库存（按 unit_id 升序，客户端排序在本地）。
	Units(userID string) ([]UnitRecord, error)
	// Monsters 按账号列出魔宠库存（按 uuid 升序）。
	Monsters(userID string) ([]MonsterRecord, error)
}

// ErrUnknownTemplate 模板 ID 在 cardcatalog 中不存在（授予前置校验失败）。
var ErrUnknownTemplate = errors.New("cn35game: 模板 ID 不在 cardcatalog")

// ValidateUnitTemplates 授予前校验：模板存在 + rare>0；任一失败即拒绝整批。
func ValidateUnitTemplates(cat *cardcatalog.Catalog, recs []UnitRecord) error {
	for _, r := range recs {
		if r.UnitID <= 0 || r.Level <= 0 || r.DuplicatedCount < 0 {
			return fmt.Errorf("%w: 非法单位初值 unit_id=%d level=%d dup=%d",
				ErrUnknownTemplate, r.UnitID, r.Level, r.DuplicatedCount)
		}
		if _, err := cat.Unit(r.UnitID); err != nil {
			return fmt.Errorf("%w: unit %d", ErrUnknownTemplate, r.UnitID)
		}
	}
	return nil
}

// ValidateMonsterTemplates 同上（魔宠）。
func ValidateMonsterTemplates(cat *cardcatalog.Catalog, recs []MonsterRecord) error {
	for _, r := range recs {
		if r.UUID == "" || r.MonsterID <= 0 || r.Level <= 0 {
			return fmt.Errorf("%w: 非法魔宠初值 uuid=%q monster_id=%d", ErrUnknownTemplate, r.UUID, r.MonsterID)
		}
		if _, err := cat.Monster(r.MonsterID); err != nil {
			return fmt.Errorf("%w: monster %d", ErrUnknownTemplate, r.MonsterID)
		}
	}
	return nil
}
