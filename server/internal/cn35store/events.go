package cn35store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// EventAreaRewardClaims M8：已发放的区域完成率档位（0056）。
func (s *Store) EventAreaRewardClaims(userID string) (map[[2]int]bool, error) {
	rows, err := s.db.Query(`SELECT area_id,threshold FROM cn35_event_area_rewards WHERE user_id=?`, userID)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 活动完成率台账读取失败: %w", err)
	}
	defer rows.Close()
	out := map[[2]int]bool{}
	for rows.Next() {
		var a, t int
		if err := rows.Scan(&a, &t); err != nil {
			return nil, err
		}
		out[[2]int{a, t}] = true
	}
	return out, rows.Err()
}

// ClaimEventAreaRewards M8：单事务「台账先插（主键幂等闸门）→ 礼物盒写入」。
// 并发/重放下已领档位 RowsAffected=0，本次零写入且不返回。
func (s *Store) ClaimEventAreaRewards(userID string, claims []cn35game.EventAreaClaim, nowSec int64) ([]cn35game.EventAreaClaim, error) {
	var granted []cn35game.EventAreaClaim
	err := s.withTx(func(tx *sql.Tx) error {
		granted = nil
		for _, c := range claims {
			pj, _ := json.Marshal(c.Presents)
			res, err := tx.Exec(`INSERT INTO cn35_event_area_rewards
				(user_id,event_id,area_id,threshold,presents_json,granted_at)
				VALUES (?,?,?,?,?,?)
				ON CONFLICT(user_id,area_id,threshold) DO NOTHING`,
				userID, c.EventID, c.AreaID, c.Threshold, string(pj), nowSec)
			if err != nil {
				return fmt.Errorf("cn35store: 活动完成率台账写入失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue
			}
			for _, p := range c.Presents {
				if _, err := tx.Exec(`INSERT INTO cn35_user_presents
					(uuid,user_id,content_type,content_id,amount,expire_at,received_at,created_at)
					VALUES (?,?,?,?,?,0,0,?)`,
					"evt-"+randHex(16), userID, p.ContentType, p.ContentID, p.Amount, nowSec); err != nil {
					return fmt.Errorf("cn35store: 活动完成率礼物写入失败: %w", err)
				}
			}
			granted = append(granted, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return granted, nil
}
