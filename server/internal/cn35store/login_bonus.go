// login_bonus.go LOGIN350-02：不可思议之泉（时间累积）读/领面的 SQLite 实现。
//
// 事务不变式（与 daily_shop.go/gacha.go 同一套）：读面不写库；领取单事务
// 「读锚点与待领量 → 条件推进可领时刻（CAS）→ 三资源入账 → 领取台账」，
// 任一失败整体回滚。并发/重放由 CAS（WHERE login_bonus_receivable_sec=旧值）
// 兜底：只有一个请求能把可领时刻推进到 now+3600，其余返回 ErrLoginBonusNotReady。
//
// 这里**不用** Store.dedupe：dedupe 会把首个在途结果回放给并发重复请求，
// 客户端会把回放当成又一次有效发放（显示奖励清单但只入账一次）。本端点要求
// 并发/重放只产生一个"已发放"响应，其余走空 contents 倒计时分支。
//
// BP 按用户口径允许溢出持有：直接 bp = bp + n（0035 迁移已移除 bp<=max_bp 约束）。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var _ cn35game.SpringStore = (*Store)(nil)

// springProfile 读取泉水所需的档案字段（可领时刻列 + 建号时间 + 当前 BP）。
func springProfileTx(tx *sql.Tx, userID string) (receivableSec, createdSec int64, err error) {
	var createdAt string
	err = tx.QueryRow(`SELECT login_bonus_receivable_sec,created_at
		FROM cn35_player_profiles WHERE user_id=?`, userID).
		Scan(&receivableSec, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, cn35game.ErrNoAccount
	}
	if err != nil {
		return 0, 0, fmt.Errorf("cn35store: 泉水档案读取失败: %w", err)
	}
	t, perr := time.Parse(time.RFC3339Nano, createdAt)
	if perr != nil {
		return 0, 0, fmt.Errorf("cn35store: 无效建号时间锚点 user=%s: %w", userID, perr)
	}
	return receivableSec, t.Unix(), nil
}

// SpringState 见 cn35game.SpringStore。
func (s *Store) SpringState(userID string, nowSec int64) (*cn35game.SpringState, error) {
	if userID == "" || nowSec <= 0 {
		return nil, fmt.Errorf("%w: userID=%q now=%d", cn35game.ErrInvalidRequest, userID, nowSec)
	}
	var st *cn35game.SpringState
	err := s.withTx(func(tx *sql.Tx) error {
		receivable, created, err := springProfileTx(tx, userID)
		if err != nil {
			return err
		}
		anchor := cn35game.SpringAnchorSec(receivable, created, nowSec)
		gold, guild, bp := cn35game.SpringPending(anchor, nowSec)
		st = &cn35game.SpringState{
			AnchorSec:     anchor,
			Gold:          gold,
			GuildAsset:    guild,
			BattlePoint:   bp,
			ReceivableSec: anchor + cn35game.SpringIntervalSec,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return st, nil
}

// ClaimSpring 见 cn35game.SpringStore。
func (s *Store) ClaimSpring(userID string, nowSec int64) (*cn35game.SpringClaimResult, error) {
	if userID == "" || nowSec <= 0 {
		return nil, fmt.Errorf("%w: userID=%q now=%d", cn35game.ErrInvalidRequest, userID, nowSec)
	}
	res := &cn35game.SpringClaimResult{}
	err := s.withTx(func(tx *sql.Tx) error {
		receivable, created, err := springProfileTx(tx, userID)
		if err != nil {
			return err
		}
		anchor := cn35game.SpringAnchorSec(receivable, created, nowSec)
		gold, guild, bp := cn35game.SpringPending(anchor, nowSec)
		if !cn35game.SpringReady(gold, guild, bp) {
			return cn35game.ErrLoginBonusNotReady
		}
		next := nowSec + cn35game.SpringIntervalSec
		// CAS：只有把可领时刻从「读到的旧值」推进成功的请求才发奖。
		up, err := tx.Exec(`UPDATE cn35_player_profiles
			SET login_bonus_receivable_sec=?,updated_at=?
			WHERE user_id=? AND login_bonus_receivable_sec=?`,
			next, nowUTC(), userID, receivable)
		if err != nil {
			return fmt.Errorf("cn35store: 泉水可领时刻推进失败: %w", err)
		}
		if n, _ := up.RowsAffected(); n != 1 {
			return cn35game.ErrLoginBonusNotReady
		}
		if err := creditBalanceTx(tx, userID, "gacha_point", gold); err != nil {
			return fmt.Errorf("cn35store: 泉水金币入账失败: %w", err)
		}
		if err := creditBalanceTx(tx, userID, "guild_asset", guild); err != nil {
			return fmt.Errorf("cn35store: 泉水公会建设素材入账失败: %w", err)
		}
		// BP 允许溢出持有：不做 max_bp 截断。
		if bp > 0 {
			if _, err := tx.Exec(`UPDATE cn35_player_profiles
				SET bp=bp+?,updated_at=? WHERE user_id=?`, bp, nowUTC(), userID); err != nil {
				return fmt.Errorf("cn35store: 泉水 BP 入账失败: %w", err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO cn35_spring_claims
			(user_id,claimed_at,anchor_before_sec,gold,guild_asset,battle_point,next_receivable_sec)
			VALUES (?,?,?,?,?,?,?)`,
			userID, nowSec, anchor, gold, guild, bp, next); err != nil {
			return fmt.Errorf("cn35store: 泉水领取台账写入失败: %w", err)
		}
		res.Granted = true
		res.Gold, res.GuildAsset, res.BattlePoint = gold, guild, bp
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
