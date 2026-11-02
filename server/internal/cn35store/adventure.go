// adventure.go LOGIN350-03：登录冒险「图图历险记」的 SQLite 实现。
//
// ## 事务不变式
//
// `AdventureState` 是**读面 + 当日发放**的单一事务（设计 Note §设计增量 2：
// 「当日首次拉取时若位图未置位则写礼物盒并置位，同事务」）：
//
//	确保当前轮次（无则建、走完则滚动）
//	→ 读本轮位图/台账
//	→ 当日未发放时：INSERT 台账（(user_id,round_index,day) 主键兜底并发与重放）
//	   → 再 INSERT cn35_user_presents（礼物盒内容池）
//	→ 当日首次则推进 last_shown_date（today_days 当日一次性开关）
//
// 任一失败整体回滚，不产生「发了礼物没记台账」或「记了台账没发礼物」。
//
// ## 并发与重放
//
// 台账 INSERT 用 `ON CONFLICT DO NOTHING` + RowsAffected 判定：并发首拉时只有
// 一个事务真正插入，其余看到 0 行即跳过礼物写入（不重复发放）。响应丢失重放
// 同理——同日重复请求既不补发也不会拿不到当日礼物（礼物已在内容池里）。
//
// ## 时钟
//
// 一律用入参 nowSec（东八区自然日口径，见 cn35game.AdventureDayKeyAt），
// 跨天/时钟对抗向量由测试注入 nowSec 覆盖（LA-09）。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// 编译期断言：Store 满足登录冒险读/发面契约。
var _ cn35game.AdventureStore = (*Store)(nil)

// adventureConfigRefRow 轮次配置排序行（启用集合）。
type adventureConfigRefRow struct {
	rewardConfigID int
	sort           int
}

// enabledAdventureConfigsTx 返回启用中的轮次配置（(sort, reward_config_id) 升序）。
func enabledAdventureConfigsTx(tx *sql.Tx) ([]adventureConfigRefRow, error) {
	rows, err := tx.Query(`SELECT reward_config_id,sort FROM cn35_adventure_configs
		WHERE enabled=1 ORDER BY sort ASC, reward_config_id ASC`)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cn35store: 冒险轮次配置读取失败: %w", err)
	}
	defer rows.Close()
	var out []adventureConfigRefRow
	for rows.Next() {
		var r adventureConfigRefRow
		if err := rows.Scan(&r.rewardConfigID, &r.sort); err != nil {
			return nil, fmt.Errorf("cn35store: 扫描冒险轮次配置: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cn35store: 遍历冒险轮次配置: %w", err)
	}
	return out, nil
}

// loadAdventureConfigTx 按 reward_config_id 装配一套配置（含日表并校验）。
//
// 与「启用集合」解耦：进行中的轮次即使配置后来被下架，仍按其快照读完，
// 避免玩家地图中途消失。
func loadAdventureConfigTx(tx *sql.Tx, rewardConfigID int) (*cn35game.AdventureConfig, error) {
	cfg := &cn35game.AdventureConfig{RewardConfigID: rewardConfigID}
	var pathJSON string
	err := tx.QueryRow(`SELECT duration_days,path_json,background_override,
			stamper_override,right_title_overrdie,path_indicator
		FROM cn35_adventure_configs WHERE reward_config_id=?`, rewardConfigID).
		Scan(&cfg.DurationDays, &pathJSON, &cfg.BackgroundOverride,
			&cfg.StamperOverride, &cfg.RightTitleOverrdie, &cfg.PathIndicator)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // 配置不存在：调用方决定自愈还是空态
	}
	if err != nil {
		return nil, fmt.Errorf("cn35store: 冒险轮次配置读取失败: %w", err)
	}
	path, err := cn35game.ParseAdventurePath(pathJSON)
	if err != nil {
		return nil, err
	}
	cfg.Path = path

	rows, err := tx.Query(`SELECT day,title,pos,special,scale,
			display_type_id,display_type_name,display_content_id,display_count,display_unit_rare,
			grant_type,grant_content_id,grant_amount
		FROM cn35_adventure_days WHERE reward_config_id=? ORDER BY day ASC`, rewardConfigID)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 冒险日表读取失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d cn35game.AdventureDayConfig
		var special int
		if err := rows.Scan(&d.Day, &d.Title, &d.Pos, &special, &d.Scale,
			&d.DisplayTypeID, &d.DisplayTypeName, &d.DisplayContentID, &d.DisplayCount,
			&d.DisplayUnitRare, &d.GrantType, &d.GrantContentID, &d.GrantAmount); err != nil {
			return nil, fmt.Errorf("cn35store: 扫描冒险日表: %w", err)
		}
		d.Special = special != 0
		cfg.Days = append(cfg.Days, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cn35store: 遍历冒险日表: %w", err)
	}
	if err := cn35game.ValidateAdventureConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// nextAdventureConfigID 轮次滚动时选下一套配置（按 (sort,id) 循环）。
//
// 只有一套配置时返回自身（轮次仍滚动：新 begin_date_i / 新位图，奖励表相同）。
func nextAdventureConfigID(refs []adventureConfigRefRow, current int) int {
	if len(refs) == 0 {
		return current
	}
	idx := -1
	for i, r := range refs {
		if r.rewardConfigID == current {
			idx = i
			break
		}
	}
	if idx < 0 {
		return refs[0].rewardConfigID
	}
	return refs[(idx+1)%len(refs)].rewardConfigID
}

// AdventureState 见 cn35game.AdventureStore。
func (s *Store) AdventureState(userID string, nowSec int64) (*cn35game.AdventureState, *cn35game.AdventureConfig, error) {
	if userID == "" || nowSec <= 0 {
		return nil, nil, fmt.Errorf("%w: userID=%q now=%d", cn35game.ErrInvalidRequest, userID, nowSec)
	}
	today := cn35game.AdventureDayKeyAt(nowSec)
	var (
		state *cn35game.AdventureState
		cfg   *cn35game.AdventureConfig
	)
	err := s.withTx(func(tx *sql.Tx) error {
		// 账号存在性（台账/轮次表外键也会兜，先给出明确错误）。
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_accounts WHERE user_id=?`, userID).Scan(&n); err != nil {
			return fmt.Errorf("cn35store: 冒险账号读取失败: %w", err)
		}
		if n == 0 {
			return cn35game.ErrNoAccount
		}

		refs, err := enabledAdventureConfigsTx(tx)
		if err != nil {
			return err
		}

		// 当前轮次行（可能不存在）。
		var (
			roundIndex    int
			rewardConfID  int
			beginDate     string
			durationDays  int
			lastShownDate string
		)
		err = tx.QueryRow(`SELECT round_index,reward_config_id,begin_date,duration_days,last_shown_date
			FROM cn35_adventure_user_rounds WHERE user_id=?`, userID).
			Scan(&roundIndex, &rewardConfID, &beginDate, &durationDays, &lastShownDate)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if len(refs) == 0 {
				return nil // 未配置任何轮次：空态回落
			}
			roundIndex, rewardConfID, beginDate = 0, refs[0].rewardConfigID, today
			lastShownDate = ""
		case err != nil:
			return fmt.Errorf("cn35store: 冒险轮次读取失败: %w", err)
		}

		// 装配轮次配置；进行中轮次配置被删则自愈为当前启用配置（begin 重置为今天）。
		cfg, err = loadAdventureConfigTx(tx, rewardConfID)
		if err != nil {
			return err
		}
		if cfg == nil {
			if len(refs) == 0 {
				return nil
			}
			rewardConfID, beginDate = refs[0].rewardConfigID, today
			lastShownDate = ""
			cfg, err = loadAdventureConfigTx(tx, rewardConfID)
			if err != nil {
				return err
			}
			if cfg == nil {
				return nil
			}
		}
		durationDays = cfg.DurationDays

		// 轮次滚动：走完 duration 天后（currentDay > duration）次日开新轮。
		currentDay, err := cn35game.AdventureCurrentDay(beginDate, nowSec)
		if err != nil {
			return err
		}
		if currentDay > durationDays {
			roundIndex++
			beginDate = today
			nextID := nextAdventureConfigID(refs, rewardConfID)
			nextCfg, err := loadAdventureConfigTx(tx, nextID)
			if err != nil {
				return err
			}
			if nextCfg != nil {
				rewardConfID, cfg = nextID, nextCfg
			}
			durationDays = cfg.DurationDays
			// 新轮从今天开始；last_shown_date 一并清空以允许今日弹窗。
			lastShownDate = ""
			currentDay = 1
		}

		// 落库轮次行（首次建/滚动/自愈都走 UPSERT）。
		if _, err := tx.Exec(`INSERT INTO cn35_adventure_user_rounds
			(user_id,round_index,reward_config_id,begin_date,duration_days,last_shown_date,updated_at)
			VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(user_id) DO UPDATE SET
				round_index=excluded.round_index,
				reward_config_id=excluded.reward_config_id,
				begin_date=excluded.begin_date,
				duration_days=excluded.duration_days,
				last_shown_date=excluded.last_shown_date,
				updated_at=excluded.updated_at`,
			userID, roundIndex, rewardConfID, beginDate, durationDays, lastShownDate, nowUTC()); err != nil {
			return fmt.Errorf("cn35store: 冒险轮次写入失败: %w", err)
		}

		// 本轮已发放天（位图真源）。
		claimed, err := adventureClaimedDaysTx(tx, userID, roundIndex)
		if err != nil {
			return err
		}
		claimedSet := make(map[int]bool, len(claimed))
		for _, d := range claimed {
			claimedSet[d] = true
		}

		state = &cn35game.AdventureState{
			RoundIndex:     roundIndex,
			RewardConfigID: rewardConfID,
			BeginDate:      beginDate,
			DurationDays:   durationDays,
			CurrentDay:     currentDay,
			ClaimedDays:    claimed,
		}

		// 当日奖励发放：仅当日在轮内且未发过。
		if day, ok := cfg.AdventureDay(currentDay); ok && !claimedSet[currentDay] {
			granted, uuid, err := adventureGrantTx(tx, userID, roundIndex, cfg, day, nowSec)
			if err != nil {
				return err
			}
			if granted {
				state.GrantedDay = currentDay
				state.PresentUUID = uuid
				state.ClaimedDays = append(state.ClaimedDays, currentDay)
			}
		}

		// today_days 当日一次性开关：同一东八区自然日只给一次数字。
		if lastShownDate != today {
			if _, err := tx.Exec(`UPDATE cn35_adventure_user_rounds
				SET last_shown_date=?,updated_at=? WHERE user_id=?`,
				today, nowUTC(), userID); err != nil {
				return fmt.Errorf("cn35store: 冒险当日弹窗标记写入失败: %w", err)
			}
			state.TodayShown = true
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if state == nil || cfg == nil {
		return nil, nil, nil // 未配置轮次：空态回落
	}
	return state, cfg, nil
}

// adventureClaimedDaysTx 读本轮台账已发放天（升序）。
func adventureClaimedDaysTx(tx *sql.Tx, userID string, roundIndex int) ([]int, error) {
	rows, err := tx.Query(`SELECT day FROM cn35_adventure_claims
		WHERE user_id=? AND round_index=? ORDER BY day ASC`, userID, roundIndex)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 冒险台账读取失败: %w", err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var d int
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("cn35store: 扫描冒险台账: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cn35store: 遍历冒险台账: %w", err)
	}
	return out, nil
}

// adventureGrantTx 单事务内「台账先插（并发闸门）→ 礼物盒内容池行」。
//
// 返回 granted=false 表示同轮同日已由并发请求发放（或主键冲突），本次零写入。
// item 类型在事务内核对目录（itemKeyByContentTx）：
// 内容不存在即报错整体回滚——不留「记了台账但礼物盒里没有东西」的半态。
func adventureGrantTx(tx *sql.Tx, userID string, roundIndex int,
	cfg *cn35game.AdventureConfig, day cn35game.AdventureDayConfig, nowSec int64) (bool, string, error) {
	uuid := "adv-" + randHex(16)
	res, err := tx.Exec(`INSERT INTO cn35_adventure_claims
		(user_id,round_index,day,reward_config_id,granted_at,grant_type,grant_content_id,grant_amount,present_uuid)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(user_id,round_index,day) DO NOTHING`,
		userID, roundIndex, day.Day, cfg.RewardConfigID, nowSec,
		day.GrantType, day.GrantContentID, day.GrantAmount, uuid)
	if err != nil {
		return false, "", fmt.Errorf("cn35store: 冒险发放台账写入失败: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, "", fmt.Errorf("cn35store: 冒险发放台账影响行数读取失败: %w", err)
	}
	if affected == 0 {
		return false, "", nil // 并发/重放：本次不重复发放
	}
	// item 发放内容必须存在于物品目录（不存在 → 回滚，配置错误可见）。
	if day.GrantType == "item" {
		if _, err := itemKeyByContentTx(tx, int(day.GrantContentID)); err != nil {
			return false, "", fmt.Errorf("cn35store: 冒险奖励物品目录缺失 content_id=%d: %w",
				day.GrantContentID, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO cn35_user_presents
		(uuid,user_id,content_type,content_id,amount,expire_at,received_at,created_at)
		VALUES (?,?,?,?,?,0,0,?)`,
		uuid, userID, day.GrantType, day.GrantContentID, day.GrantAmount, nowSec); err != nil {
		return false, "", fmt.Errorf("cn35store: 冒险礼物写入失败: %w", err)
	}
	return true, uuid, nil
}

// adventureClaimRows 该账号某轮台账行数（测试/审计用）。
func adventureClaimRows(db *sql.DB, userID string, roundIndex int) (int, error) {
	var n int
	err := db.QueryRow(`SELECT count(*) FROM cn35_adventure_claims
		WHERE user_id=? AND round_index=?`, userID, roundIndex).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
