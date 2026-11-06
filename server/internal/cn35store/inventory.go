// inventory.go cn35 同伴/魔宠库存的 SQLite 读写原语（PLAY350-06）。
//
// 读取面实现 cn35game.InventoryStore（07 消费）；写入一律收 *sql.Tx——
// 库存行 + cn35_inventory_grants 台账行必须在调用方（08）的同一个事务里
// 提交，本文件不自行开启事务，避免部分提交。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// ErrGrantKeyConflict 台账键冲突：同 (user_id,grant_key,unit_id) 已登记——
// 语义上是"该发放已应用"，供 08 幂等短路判定。
var ErrGrantKeyConflict = errors.New("cn35store: 发放台账键已存在")

// 编译期断言：Store 满足 07 消费的库存读取面。
var _ cn35game.InventoryStore = (*Store)(nil)

// Units 实现 cn35game.InventoryStore：按 unit_id 升序返回账号单位库存（含同行与待机所）。
func (s *Store) Units(userID string) ([]cn35game.UnitRecord, error) {
	withWait := s.unitWaitingColumn()
	cols := `unit_id,level,duplicated_count,protected,newcomer,monster_uuid`
	if withWait {
		cols += `,is_waiting`
	}
	rows, err := s.db.Query(`SELECT `+cols+`
		FROM cn35_player_units WHERE user_id=? ORDER BY unit_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.UnitRecord{}
	for rows.Next() {
		var r cn35game.UnitRecord
		var prot, newcom, wait int
		dest := []any{&r.UnitID, &r.Level, &r.DuplicatedCount, &prot, &newcom, &r.MonsterUUID}
		if withWait {
			dest = append(dest, &wait)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		r.Protected = prot != 0
		r.Newcomer = newcom != 0
		r.Waiting = wait != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// unitWaitingColumn 检测 0046 的 is_waiting 列是否在位（结果缓存一次）。
// Open 路径经 Migrate 到最新 → 恒 true；OpenReadOnly 打开旧库 → false。
func (s *Store) unitWaitingColumn() bool {
	s.waitColOnce.Do(func() {
		var n int
		err := s.db.QueryRow(
			`SELECT count(*) FROM pragma_table_info('cn35_player_units') WHERE name='is_waiting'`).Scan(&n)
		s.hasWaitCol = err == nil && n == 1
	})
	return s.hasWaitCol
}

// Monsters 实现 cn35game.InventoryStore：按 uuid 升序返回账号魔宠库存。
// Size 列（0009）可空：NULL 原样映射为 nil（未证/降级），读面自行按显式
// Size=1.000 处理，不由存储层代填。
//
// 兼容：只读打开**未应用 0009** 的旧库（如 06 的目标库只读预览）时该列集
// 不存在，此时退回旧列集并把 Size 一律置 nil——不报错、不迁移、不代填。
func (s *Store) Monsters(userID string) ([]cn35game.MonsterRecord, error) {
	withSize := s.monsterSizeColumns()
	cols := `uuid,monster_id,unit_id,level,protected`
	if withSize {
		cols += `,scale,real_scale,scale_limit,is_rare`
	}
	rows, err := s.db.Query(`SELECT `+cols+`
		FROM cn35_player_monsters WHERE user_id=? ORDER BY uuid`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.MonsterRecord{}
	for rows.Next() {
		var r cn35game.MonsterRecord
		var unitID sql.NullInt64
		var prot int
		var scale, realScale, scaleLimit sql.NullString
		var isRare sql.NullInt64
		dest := []any{&r.UUID, &r.MonsterID, &unitID, &r.Level, &prot}
		if withSize {
			dest = append(dest, &scale, &realScale, &scaleLimit, &isRare)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		if unitID.Valid {
			v := int(unitID.Int64)
			r.UnitID = &v
		}
		r.Protected = prot != 0
		r.Scale = nullStrPtr(scale)
		r.RealScale = nullStrPtr(realScale)
		r.ScaleLimit = nullStrPtr(scaleLimit)
		if isRare.Valid {
			b := isRare.Int64 != 0
			r.IsRare = &b
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullStrPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

// monsterSizeColumns 检测 0009 的 Size 列是否在位（结果缓存一次）。
// Open 路径经 Migrate 到最新 → 恒 true；OpenReadOnly 打开旧库 → false。
func (s *Store) monsterSizeColumns() bool {
	s.sizeColOnce.Do(func() {
		var n int
		err := s.db.QueryRow(
			`SELECT count(*) FROM pragma_table_info('cn35_player_monsters') WHERE name='scale'`).Scan(&n)
		s.hasSizeCol = err == nil && n == 1
	})
	return s.hasSizeCol
}

// InsertUnitTx 事务内插入一条单位库存行；模板有效性已由调用方
// （cn35game.ValidateUnitTemplates）保证，此处只落实例态。
func InsertUnitTx(tx *sql.Tx, userID string, r cn35game.UnitRecord) error {
	_, err := tx.Exec(`INSERT INTO cn35_player_units
		(user_id,unit_id,level,duplicated_count,protected,newcomer,monster_uuid,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		userID, r.UnitID, r.Level, r.DuplicatedCount, boolInt(r.Protected),
		boolInt(r.Newcomer), r.MonsterUUID, nowUTC(), nowUTC())
	if err != nil {
		return fmt.Errorf("cn35store: 插入单位 %d 失败: %w", r.UnitID, err)
	}
	return nil
}

// InsertMonsterTx 事务内插入一条魔宠库存行（uuid 账号内唯一；
// unit_id 非空时经复合 FK 校验宿主归属同账号）。
// Size 列（0009）可空：nil 写入 NULL（未证/降级），不由存储层代填 1.0。
func InsertMonsterTx(tx *sql.Tx, userID string, r cn35game.MonsterRecord) error {
	var unit any
	if r.UnitID != nil {
		unit = *r.UnitID
	}
	var isRare any
	if r.IsRare != nil {
		isRare = boolInt(*r.IsRare)
	}
	_, err := tx.Exec(`INSERT INTO cn35_player_monsters
		(user_id,uuid,monster_id,unit_id,level,protected,scale,real_scale,scale_limit,is_rare,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		userID, r.UUID, r.MonsterID, unit, r.Level, boolInt(r.Protected),
		nullableStr(r.Scale), nullableStr(r.RealScale), nullableStr(r.ScaleLimit), isRare,
		nowUTC(), nowUTC())
	if err != nil {
		return fmt.Errorf("cn35store: 插入魔宠 %s 失败: %w", r.UUID, err)
	}
	return nil
}

func nullableStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// RecordGrantTx 事务内登记发放台账；(user_id,grant_key,unit_id) 冲突
// 返回 ErrGrantKeyConflict。
func RecordGrantTx(tx *sql.Tx, userID, grantKey string, unitID int) error {
	_, err := tx.Exec(`INSERT INTO cn35_inventory_grants (user_id,grant_key,unit_id,created_at)
		VALUES (?,?,?,?)`, userID, grantKey, unitID, nowUTC())
	if isIDConflict(err) {
		return ErrGrantKeyConflict
	}
	if err != nil {
		return fmt.Errorf("cn35store: 登记台账 %s/%d 失败: %w", grantKey, unitID, err)
	}
	return nil
}

// GrantAppliedTx 事务内查询该 grant_key 已登记的 unit_id 集合
// （幂等短路：集合等于待发放集 → 跳过插入）。
func GrantAppliedTx(tx *sql.Tx, userID, grantKey string) (map[int]bool, error) {
	rows, err := tx.Query(`SELECT unit_id FROM cn35_inventory_grants WHERE user_id=? AND grant_key=?`,
		userID, grantKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// WithTx 暴露受控写事务（BEGIN IMMEDIATE 同 withTx）：跨域写——如 08 的
// "库存行+台账行+可选 profile 更新"——在同一个 tx 完成或整体回滚。
func (s *Store) WithTx(fn func(*sql.Tx) error) error { return s.withTx(fn) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
