// daily_shop.go SHOP350-01：每日商店（面包店）领取台账的 SQLite 实现。
//
// 事务约定与 items.go 一致：方法即事务边界，账号隔离靠 WHERE user_id；
// (user_id,goods_key,period_key) 主键 + 条件 UPDATE 兜底并发限购。
package cn35store

import (
	"database/sql"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var _ cn35game.DailyShopStore = (*Store)(nil)

// DailyShopClaims 见 cn35game.DailyShopStore。
func (s *Store) DailyShopClaims(userID, periodKey string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT goods_key,count FROM cn35_daily_shop_claims
		WHERE user_id=? AND period_key=?`, userID, periodKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var c int
		if err = rows.Scan(&k, &c); err != nil {
			return nil, err
		}
		out[k] = c
	}
	return out, rows.Err()
}

// ClaimDailyPack 见 cn35game.DailyShopStore。并发同请求经 dedupe 在途
// 去重（双击/重传复用首个结果），持久限购由条件 UPDATE 兜底。
func (s *Store) ClaimDailyPack(userID, goodsKey, periodKey string, coin int64, limit int) (int64, error) {
	if coin <= 0 || limit <= 0 {
		return 0, fmt.Errorf("%w: coin=%d limit=%d",
			cn35game.ErrInvalidRequest, coin, limit)
	}
	key := fmt.Sprintf("%s|daily_shop|%s|%s", userID, goodsKey, periodKey)
	v, _, err := s.dedupe(key, func() (any, error) {
		return s.claimDailyPack(userID, goodsKey, periodKey, coin, limit)
	})
	if err != nil {
		return 0, err
	}
	return v.(int64), nil
}

func (s *Store) claimDailyPack(userID, goodsKey, periodKey string, coin int64, limit int) (int64, error) {
	var balance int64
	err := s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO cn35_daily_shop_claims
			(user_id,goods_key,period_key,count,created_at,updated_at)
			VALUES (?,?,?,0,?,?)`,
			userID, goodsKey, periodKey, nowUTC(), nowUTC()); err != nil {
			return err
		}
		up, err := tx.Exec(`UPDATE cn35_daily_shop_claims
			SET count=count+1,updated_at=?
			WHERE user_id=? AND goods_key=? AND period_key=? AND count+1<=?`,
			nowUTC(), userID, goodsKey, periodKey, limit)
		if err != nil {
			return err
		}
		if n, _ := up.RowsAffected(); n != 1 {
			return cn35game.ErrDailyShopClaimed
		}
		if err := creditBalanceTx(tx, userID, "coin", coin); err != nil {
			return err
		}
		balance = balanceOfTx(tx, userID, "coin")
		return nil
	})
	return balance, err
}

// BuyScoutBook 单事务扣除钻石兑换骑士契约书（scout_book_1）。
func (s *Store) BuyScoutBook(userID string, count, unitDiamondCost int64) (int64, int64, error) {
	if userID == "" || count <= 0 || unitDiamondCost <= 0 {
		return 0, 0, fmt.Errorf("%w: count=%d cost=%d",
			cn35game.ErrInvalidRequest, count, unitDiamondCost)
	}
	totalCost := count * unitDiamondCost
	var coinAfter, bookAfter int64
	err := s.withTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_accounts WHERE user_id=?`, userID).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 {
			return cn35game.ErrNoAccount
		}
		up, err := tx.Exec(`UPDATE cn35_player_balances
			SET amount=amount-?,updated_at=?
			WHERE user_id=? AND currency='coin' AND amount>=?`,
			totalCost, nowUTC(), userID, totalCost)
		if err != nil {
			return err
		}
		if n, _ := up.RowsAffected(); n != 1 {
			return cn35game.ErrDailyShopInsufficientCoin
		}
		if err := creditBalanceTx(tx, userID, "scout_book_1", count); err != nil {
			return err
		}
		coinAfter = balanceOfTx(tx, userID, "coin")
		bookAfter = balanceOfTx(tx, userID, "scout_book_1")
		return nil
	})
	return coinAfter, bookAfter, err
}
