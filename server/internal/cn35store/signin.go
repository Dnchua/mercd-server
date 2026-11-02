// signin.go LOGIN350-02：每月 28 格签到的 SQLite 实现。
//
// 事务不变式：读面只查台账；领取单事务「节点合法性/解锁/未领校验 →
// cn35_signin_claims INSERT（(user_id,month_key,node) 主键兜底并发与重放）→
// 钻石/徽章/金币 balance 入账 + 培养素材物品入账」，任一失败整体回滚。
//
// 并发/重放：同节点并发只有一个 INSERT 成功，另一个命中主键冲突返回
// ErrSigninNotReady 且不产生任何入账；跨账号/跨月天然隔离。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var _ cn35game.SigninStore = (*Store)(nil)

// SigninState 见 cn35game.SigninStore。
func (s *Store) SigninState(userID string, nowSec int64) (*cn35game.SigninState, error) {
	if userID == "" || nowSec <= 0 {
		return nil, fmt.Errorf("%w: userID=%q now=%d", cn35game.ErrInvalidRequest, userID, nowSec)
	}
	monthKey, day := cn35game.SigninMonth(nowSec)
	st := &cn35game.SigninState{MonthKey: monthKey, Day: day}
	err := s.withTx(func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_accounts WHERE user_id=?`, userID).Scan(&n); err != nil {
			return fmt.Errorf("cn35store: 签到账号读取失败: %w", err)
		}
		if n == 0 {
			return cn35game.ErrNoAccount
		}
		rows, err := tx.Query(`SELECT node FROM cn35_signin_claims
			WHERE user_id=? AND month_key=? ORDER BY node`, userID, monthKey)
		if err != nil {
			return fmt.Errorf("cn35store: 签到台账读取失败: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var node int
			if err := rows.Scan(&node); err != nil {
				return err
			}
			st.ClaimedNodes = append(st.ClaimedNodes, node)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return st, nil
}

// ClaimSignin 见 cn35game.SigninStore。
func (s *Store) ClaimSignin(userID string, nowSec int64, node int) (*cn35game.SigninClaimResult, error) {
	if userID == "" || nowSec <= 0 {
		return nil, fmt.Errorf("%w: userID=%q now=%d", cn35game.ErrInvalidRequest, userID, nowSec)
	}
	if node < 1 || node > cn35game.SigninNodeCount {
		return nil, cn35game.ErrSigninInvalidNode
	}
	monthKey, day := cn35game.SigninMonth(nowSec)
	if !cn35game.SigninNodeOpen(node, day) {
		return nil, cn35game.ErrSigninNotReady
	}
	reward := cn35game.SigninNodeReward(int64(node), nowSec)
	res := &cn35game.SigninClaimResult{Reward: reward}
	err := s.withTx(func(tx *sql.Tx) error {
		// 账号存在性（台账外键也会兜，先给出明确错误）。
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_accounts WHERE user_id=?`, userID).Scan(&n); err != nil {
			return fmt.Errorf("cn35store: 签到账号读取失败: %w", err)
		}
		if n == 0 {
			return cn35game.ErrNoAccount
		}
		if _, err := tx.Exec(`INSERT INTO cn35_signin_claims
			(user_id,month_key,node,claimed_at,coin,medal,gold,item_content_id,item_count)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			userID, monthKey, node, nowSec, reward.Coin, reward.Medal,
			reward.Gold, reward.ItemContentID, reward.ItemCount); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return cn35game.ErrSigninNotReady
			}
			return fmt.Errorf("cn35store: 签到台账写入失败: %w", err)
		}
		if err := creditBalanceTx(tx, userID, "coin", reward.Coin); err != nil {
			return fmt.Errorf("cn35store: 签到钻石入账失败: %w", err)
		}
		if err := creditBalanceTx(tx, userID, "medal", reward.Medal); err != nil {
			return fmt.Errorf("cn35store: 签到徽章入账失败: %w", err)
		}
		if err := creditBalanceTx(tx, userID, "gacha_point", reward.Gold); err != nil {
			return fmt.Errorf("cn35store: 签到金币入账失败: %w", err)
		}
		if reward.ItemContentID > 0 && reward.ItemCount > 0 {
			key, err := itemKeyByContentTx(tx, int(reward.ItemContentID))
			if err != nil {
				return fmt.Errorf("cn35store: 签到素材目录缺失 content_id=%d: %w",
					reward.ItemContentID, err)
			}
			if err := creditItemTx(tx, userID, key, int(reward.ItemCount)); err != nil {
				return fmt.Errorf("cn35store: 签到素材入账失败: %w", err)
			}
		}
		res.Granted = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// signinClaimedCountTx 事务内统计本月已领节点数（测试/审计用）。
func signinClaimedCountTx(tx *sql.Tx, userID, monthKey string) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM cn35_signin_claims
		WHERE user_id=? AND month_key=?`, userID, monthKey).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
