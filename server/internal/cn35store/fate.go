// fate.go FATE350-01：M1 命运星盘（艾琳的小屋）SQLite 持久化实现。
//
// 事务约定：
//   - FateState 只读：当无记录或库内 period_key != 请求 periodKey 时，
//     按未转盘初始态（today_roll_times=0, fate_list=[]）返回，不产生写事务；
//   - RollFate 单事务：读取当前周期状态 → 校验 inner 首抽前置与 amazing 覆盖确认 →
//     条件 UPDATE 扣减 coin（首抽 all 为 0 钻，后续 all 为 18 钻，inner 为 10 钻） →
//     UPSERT cn35_player_fate → INSERT cn35_player_fate_ledger。
package cn35store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	mathrand "math/rand"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var _ cn35game.FateStore = (*Store)(nil)

func loadFateStateRow(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, userID, periodKey string) (*cn35game.FateState, error) {
	st := &cn35game.FateState{
		UserID:         userID,
		PeriodKey:      periodKey,
		DailyLuckDesc:  cn35game.FateDailyLuckDesc(userID, periodKey),
		TodayRollTimes: 0,
		FateList:       []cn35game.FateEntry{},
	}
	var dbPeriod, luckDesc, listJSON string
	var rollTimes int
	err := q.QueryRow(`SELECT period_key,daily_luck_desc,today_roll_times,fate_list_json
		FROM cn35_player_fate WHERE user_id=?`, userID).
		Scan(&dbPeriod, &luckDesc, &rollTimes, &listJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cn35store: 读取星盘状态失败: %w", err)
	}
	// 跨周期（东八区凌晨 04:00 刷新后）自动按新周期零值返回。
	if dbPeriod != periodKey {
		return st, nil
	}
	if luckDesc != "" {
		st.DailyLuckDesc = luckDesc
	}
	st.TodayRollTimes = rollTimes
	if listJSON != "" {
		var entries []cn35game.FateEntry
		if uerr := json.Unmarshal([]byte(listJSON), &entries); uerr == nil && entries != nil {
			st.FateList = entries
		}
	}
	return st, nil
}

// fateBonusMapTx 在事务内读取玩家当前周期已激活的星盘加成表（无记录或未转盘返回空 map）。
func (s *Store) fateBonusMapTx(tx *sql.Tx, userID, periodKey string) map[string]float64 {
	st, err := loadFateStateRow(tx, userID, periodKey)
	if err != nil || st == nil || st.TodayRollTimes <= 0 || len(st.FateList) == 0 {
		return nil
	}
	return cn35game.FateBonusMap(st.FateList)
}

// FateState 见 cn35game.FateStore。
func (s *Store) FateState(userID, periodKey string) (*cn35game.FateState, error) {
	if userID == "" || periodKey == "" {
		return nil, fmt.Errorf("%w: userID=%q periodKey=%q", cn35game.ErrInvalidRequest, userID, periodKey)
	}
	return loadFateStateRow(s.db, userID, periodKey)
}

// RollFate 见 cn35game.FateStore。
func (s *Store) RollFate(userID, periodKey, rollType string, confirm bool, rng *mathrand.Rand) (*cn35game.FateRollResult, error) {
	if userID == "" || periodKey == "" {
		return nil, fmt.Errorf("%w: userID=%q periodKey=%q", cn35game.ErrInvalidRequest, userID, periodKey)
	}
	if rollType != "all" && rollType != "inner" {
		return nil, fmt.Errorf("%w: 非法星盘转动类型 %q", cn35game.ErrInvalidRequest, rollType)
	}
	var out *cn35game.FateRollResult
	err := s.withTx(func(tx *sql.Tx) error {
		cur, err := loadFateStateRow(tx, userID, periodKey)
		if err != nil {
			return err
		}
		if rollType == "inner" && (cur.TodayRollTimes <= 0 || len(cur.FateList) != cn35game.FateRules.SlotCount) {
			return cn35game.ErrFateInnerRequiresFirstRoll
		}
		if cur.TodayRollTimes > 0 && cn35game.FateHasAmazing(cur.FateList) && !confirm {
			return cn35game.ErrFateNeedConfirm
		}
		var cost int64
		switch rollType {
		case "all":
			if cur.TodayRollTimes > 0 {
				cost = int64(cn35game.FateRules.RollAllPrice)
			}
		case "inner":
			cost = int64(cn35game.FateRules.RollInnerPrice)
		}
		if cost > 0 {
			res, err := tx.Exec(`UPDATE cn35_player_balances
				SET amount=amount-?,updated_at=?
				WHERE user_id=? AND currency='coin' AND amount>=?`,
				cost, nowUTC(), userID, cost)
			if err != nil {
				return fmt.Errorf("cn35store: 星盘扣费失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrFateInsufficientCoin
			}
		}
		nextEntries, err := cn35game.RollFateEntries(rng, rollType, cur.FateList)
		if err != nil {
			return err
		}
		rawJSON, err := json.Marshal(nextEntries)
		if err != nil {
			return err
		}
		nextTimes := cur.TodayRollTimes + 1
		now := nowUTC()
		if _, err := tx.Exec(`INSERT INTO cn35_player_fate
			(user_id,period_key,daily_luck_desc,today_roll_times,fate_list_json,updated_at)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT(user_id) DO UPDATE SET
				period_key=excluded.period_key,
				daily_luck_desc=excluded.daily_luck_desc,
				today_roll_times=excluded.today_roll_times,
				fate_list_json=excluded.fate_list_json,
				updated_at=excluded.updated_at`,
			userID, periodKey, cur.DailyLuckDesc, nextTimes, string(rawJSON), now); err != nil {
			return fmt.Errorf("cn35store: 写入星盘状态失败: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO cn35_player_fate_ledger
			(user_id,period_key,roll_type,roll_seq,cost_coin,fate_list_json,created_at)
			VALUES (?,?,?,?,?,?,?)`,
			userID, periodKey, rollType, nextTimes, cost, string(rawJSON), now); err != nil {
			return fmt.Errorf("cn35store: 写入星盘台账失败: %w", err)
		}
		bal := balanceOfTx(tx, userID, "coin")
		out = &cn35game.FateRollResult{
			State: &cn35game.FateState{
				UserID:         userID,
				PeriodKey:      periodKey,
				DailyLuckDesc:  cur.DailyLuckDesc,
				TodayRollTimes: nextTimes,
				FateList:       nextEntries,
			},
			CostCoin:    cost,
			BalanceCoin: bal,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
