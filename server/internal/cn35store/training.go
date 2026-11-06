// training.go STAT350-05A：正常等级训练的 SQLite 写原语。
//
// 单事务不变式：归属/存在 → level==before（陈旧/重放拒绝）→ after<=maxLevel
// → gacha_point 条件扣减（不足即回滚）→ UPDATE level（带 level=before 条件，
// 影响行数必须为 1）。任一失败整体回滚，不产生部分写入。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// 编译期断言：Store 满足 05A 消费的训练写面。
var _ cn35game.UnitTrainingStore = (*Store)(nil)

// TrainUnit 见 cn35game.UnitTrainingStore。返回扣费后的 gacha_point 余额。
func (s *Store) TrainUnit(userID string, unitID, before, after, maxLevel int) (int64, error) {
	var remaining int64
	err := s.withTx(func(tx *sql.Tx) error {
		var level, dup int
		err := tx.QueryRow(`SELECT level,duplicated_count FROM cn35_player_units
			WHERE user_id=? AND unit_id=? AND is_waiting=0`, userID, unitID).Scan(&level, &dup)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrTrainingUnitNotFound
		}
		if err != nil {
			return err
		}
		if level != before {
			return cn35game.ErrTrainingStale
		}
		if after <= before || after > maxLevel {
			return cn35game.ErrTrainingOutOfRange
		}
		cost, err := cn35game.UnitTrainingCost(before, after)
		if err != nil {
			return err
		}
		if cost > 0 {
			up, err := tx.Exec(`UPDATE cn35_player_balances
				SET amount=amount-?,updated_at=?
				WHERE user_id=? AND currency='gacha_point' AND amount>=?`,
				cost, nowUTC(), userID, cost)
			if err != nil {
				return err
			}
			if n, _ := up.RowsAffected(); n != 1 {
				return cn35game.ErrTrainingPoint
			}
		}
		res, err := tx.Exec(`UPDATE cn35_player_units SET level=?,updated_at=?
			WHERE user_id=? AND unit_id=? AND level=?`,
			after, nowUTC(), userID, unitID, before)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrTrainingStale
		}
		var bal sql.NullInt64
		if err := tx.QueryRow(`SELECT amount FROM cn35_player_balances
			WHERE user_id=? AND currency='gacha_point'`, userID).Scan(&bal); err != nil {
			return fmt.Errorf("cn35store: 训练后读取 gacha_point 失败: %w", err)
		}
		remaining = bal.Int64
		return nil
	})
	return remaining, err
}
