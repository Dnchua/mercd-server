// friends.go FRIEND350-01：M3 好友系统与友情徽章礼物盒的 SQLite 持久化实现。
package cn35store

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var _ cn35game.FriendStore = (*Store)(nil)

func resolveFriendSummaryTx(tx *sql.Tx, targetUID string, nowSec int64) (cn35game.FriendSummary, bool, error) {
	if npc, ok := cn35game.LookupNPCFriend(targetUID); ok {
		return cn35game.FriendSummary{
			UserID:    npc.UserID,
			Name:      npc.Name,
			UnitID:    npc.UnitID,
			Level:     npc.Level,
			GuildName: npc.GuildName,
			LoggedAt:  nowSec - 600,
		}, true, nil
	}
	var name, leaderStr, guildSnap string
	var level int
	err := tx.QueryRow(`SELECT a.user_name, p.level, p.leader_unit_id, p.guild_name_snapshot
		FROM cn35_accounts a JOIN cn35_player_profiles p ON p.user_id=a.user_id
		WHERE a.user_id=?`, targetUID).
		Scan(&name, &level, &leaderStr, &guildSnap)
	if errors.Is(err, sql.ErrNoRows) {
		return cn35game.FriendSummary{}, false, nil
	}
	if err != nil {
		return cn35game.FriendSummary{}, false, err
	}
	if name == "" {
		name = "旅人"
	}
	unitID, _ := strconv.Atoi(strings.TrimSpace(leaderStr))
	if unitID <= 0 {
		unitID = 348
	}
	var gname string
	_ = tx.QueryRow(`SELECT g.name FROM cn35_guild_members m
		JOIN cn35_guilds g ON g.guild_id=m.guild_id
		WHERE m.user_id=?`, targetUID).Scan(&gname)
	if gname == "" {
		gname = guildSnap
	}
	if gname == "" {
		gname = "无所属"
	}
	return cn35game.FriendSummary{
		UserID:    targetUID,
		Name:      name,
		UnitID:    unitID,
		Level:     level,
		GuildName: gname,
		LoggedAt:  nowSec - 120,
	}, true, nil
}

func playerCPTx(tx *sql.Tx, userID string) int64 {
	var amt int64
	err := tx.QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='cp'`, userID).Scan(&amt)
	if errors.Is(err, sql.ErrNoRows) {
		// 对齐 ApplyDefaultUserWireFields 默认 cp=120。
		return cn35game.FriendRules.MaxCP
	}
	if err != nil {
		return 0
	}
	return amt
}

func setPlayerCPTx(tx *sql.Tx, userID string, cp int64) error {
	_, err := tx.Exec(`INSERT INTO cn35_player_balances (user_id,currency,amount,updated_at)
		VALUES (?,'cp',?,?)
		ON CONFLICT(user_id,currency) DO UPDATE SET
			amount=excluded.amount,updated_at=excluded.updated_at`,
		userID, cp, nowUTC())
	return err
}

func lastGivePeriodTx(tx *sql.Tx, userID, friendUserID string) string {
	var p string
	_ = tx.QueryRow(`SELECT last_give_period FROM cn35_player_friend_give_log
		WHERE user_id=? AND friend_user_id=?`, userID, friendUserID).Scan(&p)
	return p
}

func (s *Store) friendIndexTx(tx *sql.Tx, userID, periodKey string) (*cn35game.FriendIndexState, error) {
	nowSec := s.clockNowUTC().Unix()
	rows, err := tx.Query(`SELECT friend_user_id, last_give_period
		FROM cn35_player_friends WHERE user_id=?
		ORDER BY created_at ASC, friend_user_id ASC`, userID)
	if err != nil {
		return nil, err
	}
	type pair struct {
		uid        string
		lastPeriod string
	}
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.uid, &p.lastPeriod); err != nil {
			rows.Close()
			return nil, err
		}
		pairs = append(pairs, p)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	friends := make([]cn35game.FriendSummary, 0, len(pairs))
	for _, p := range pairs {
		sum, ok, rerr := resolveFriendSummaryTx(tx, p.uid, nowSec)
		if rerr != nil {
			return nil, rerr
		}
		if !ok {
			continue
		}
		sum.CanGiveMedal = p.lastPeriod != periodKey && lastGivePeriodTx(tx, userID, p.uid) != periodKey
		friends = append(friends, sum)
	}
	var pkgCnt int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_friend_medal_gifts WHERE user_id=?`, userID).Scan(&pkgCnt); err != nil {
		return nil, err
	}
	medalBal := balanceOfTx(tx, userID, "friend_medal")
	return &cn35game.FriendIndexState{
		FriendCountLimit: cn35game.FriendRules.FriendCountLimit,
		FriendCount:      len(friends),
		FriendMedalCount: medalBal,
		MedalPackageCnt:  pkgCnt,
		Friends:          friends,
	}, nil
}

// FriendIndex 见 cn35game.FriendStore。
func (s *Store) FriendIndex(userID, periodKey string) (*cn35game.FriendIndexState, error) {
	var out *cn35game.FriendIndexState
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		out, err = s.friendIndexTx(tx, userID, periodKey)
		return err
	})
	return out, err
}

// FriendApplyList 见 cn35game.FriendStore。
func (s *Store) FriendApplyList(userID string) ([]cn35game.FriendSummary, int, error) {
	var out []cn35game.FriendSummary
	var friendCnt int
	err := s.withTx(func(tx *sql.Tx) error {
		nowSec := s.clockNowUTC().Unix()
		rows, err := tx.Query(`SELECT applicant_user_id FROM cn35_player_friend_applies
			WHERE target_user_id=? ORDER BY created_at DESC, applicant_user_id ASC`, userID)
		if err != nil {
			return err
		}
		var uids []string
		for rows.Next() {
			var u string
			if err := rows.Scan(&u); err != nil {
				rows.Close()
				return err
			}
			uids = append(uids, u)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		out = make([]cn35game.FriendSummary, 0, len(uids))
		for _, u := range uids {
			sum, ok, rerr := resolveFriendSummaryTx(tx, u, nowSec)
			if rerr != nil {
				return rerr
			}
			if ok {
				out = append(out, sum)
			}
		}
		return tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_friends WHERE user_id=?`, userID).Scan(&friendCnt)
	})
	return out, friendCnt, err
}

// SearchFriends 见 cn35game.FriendStore。
func (s *Store) SearchFriends(userID, queryUserID, queryName string) ([]cn35game.FriendSummary, error) {
	var out []cn35game.FriendSummary
	err := s.withTx(func(tx *sql.Tx) error {
		nowSec := s.clockNowUTC().Unix()
		existing := map[string]bool{userID: true}
		frows, err := tx.Query(`SELECT friend_user_id FROM cn35_player_friends WHERE user_id=?`, userID)
		if err != nil {
			return err
		}
		for frows.Next() {
			var f string
			if err := frows.Scan(&f); err != nil {
				frows.Close()
				return err
			}
			existing[f] = true
		}
		if err := frows.Close(); err != nil {
			return err
		}

		var candidateUIDs []string
		// 1) 查询真实玩家账号
		q := `SELECT user_id, user_name FROM cn35_accounts WHERE user_id != ?`
		args := []any{userID}
		if queryUserID != "" {
			q += ` AND user_id = ?`
			args = append(args, queryUserID)
		} else if queryName != "" {
			q += ` AND user_name LIKE ?`
			args = append(args, "%"+queryName+"%")
		}
		q += ` ORDER BY created_at DESC LIMIT 20`
		arows, err := tx.Query(q, args...)
		if err != nil {
			return err
		}
		for arows.Next() {
			var uid, uname string
			if err := arows.Scan(&uid, &uname); err != nil {
				arows.Close()
				return err
			}
			if !existing[uid] {
				candidateUIDs = append(candidateUIDs, uid)
				existing[uid] = true
			}
		}
		if err := arows.Close(); err != nil {
			return err
		}

		// 2) 匹配内置单机 NPC 冒险者
		for _, npc := range cn35game.DefaultNPCFriends {
			if existing[npc.UserID] {
				continue
			}
			if queryUserID != "" && npc.UserID != queryUserID {
				continue
			}
			if queryName != "" && !strings.Contains(npc.Name, queryName) {
				continue
			}
			candidateUIDs = append(candidateUIDs, npc.UserID)
			existing[npc.UserID] = true
		}

		out = make([]cn35game.FriendSummary, 0, len(candidateUIDs))
		for _, uid := range candidateUIDs {
			sum, ok, rerr := resolveFriendSummaryTx(tx, uid, nowSec)
			if rerr != nil {
				return rerr
			}
			if ok {
				out = append(out, sum)
			}
		}
		return nil
	})
	return out, err
}

// ApplyFriend 见 cn35game.FriendStore。
func (s *Store) ApplyFriend(userID, targetUserID string) error {
	if userID == "" || targetUserID == "" {
		return cn35game.ErrInvalidRequest
	}
	if userID == targetUserID {
		return cn35game.ErrFriendSelfOperation
	}
	return s.withTx(func(tx *sql.Tx) error {
		nowSec := s.clockNowUTC().Unix()
		_, ok, err := resolveFriendSummaryTx(tx, targetUserID, nowSec)
		if err != nil {
			return err
		}
		if !ok {
			return cn35game.ErrFriendTargetNotFound
		}
		var already int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_friends WHERE user_id=? AND friend_user_id=?`,
			userID, targetUserID).Scan(&already); err != nil {
			return err
		}
		if already > 0 {
			return cn35game.ErrFriendAlreadyExists
		}
		var myCnt int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_friends WHERE user_id=?`, userID).Scan(&myCnt); err != nil {
			return err
		}
		if myCnt >= cn35game.FriendRules.FriendCountLimit {
			return cn35game.ErrFriendLimitReached
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO cn35_player_friend_applies
			(target_user_id, applicant_user_id, created_at) VALUES (?,?,?)`,
			targetUserID, userID, nowSec); err != nil {
			return err
		}
		// 单机 NPC 互动：向 NPC 申请时，NPC 同步回发一条申请到玩家收件箱，方便玩家在「批准好友」页直接批准。
		if _, isNPC := cn35game.LookupNPCFriend(targetUserID); isNPC {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO cn35_player_friend_applies
				(target_user_id, applicant_user_id, created_at) VALUES (?,?,?)`,
				userID, targetUserID, nowSec); err != nil {
				return err
			}
		}
		return nil
	})
}

// AddFriend 见 cn35game.FriendStore。
func (s *Store) AddFriend(userID, applicantUserID, periodKey string) (*cn35game.FriendIndexState, error) {
	if userID == "" || applicantUserID == "" {
		return nil, cn35game.ErrInvalidRequest
	}
	if userID == applicantUserID {
		return nil, cn35game.ErrFriendSelfOperation
	}
	var out *cn35game.FriendIndexState
	err := s.withTx(func(tx *sql.Tx) error {
		nowSec := s.clockNowUTC().Unix()
		_, ok, err := resolveFriendSummaryTx(tx, applicantUserID, nowSec)
		if err != nil {
			return err
		}
		if !ok {
			return cn35game.ErrFriendTargetNotFound
		}
		var already int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_friends WHERE user_id=? AND friend_user_id=?`,
			userID, applicantUserID).Scan(&already); err != nil {
			return err
		}
		if already > 0 {
			return cn35game.ErrFriendAlreadyExists
		}
		var myCnt int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_friends WHERE user_id=?`, userID).Scan(&myCnt); err != nil {
			return err
		}
		if myCnt >= cn35game.FriendRules.FriendCountLimit {
			return cn35game.ErrFriendLimitReached
		}
		var hasApply int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_friend_applies
			WHERE target_user_id=? AND applicant_user_id=?`,
			userID, applicantUserID).Scan(&hasApply); err != nil {
			return err
		}
		if hasApply == 0 {
			return cn35game.ErrFriendApplyNotFound
		}
		if _, isNPC := cn35game.LookupNPCFriend(applicantUserID); !isNPC {
			var theirCnt int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_friends WHERE user_id=?`, applicantUserID).Scan(&theirCnt); err != nil {
				return err
			}
			if theirCnt >= cn35game.FriendRules.FriendCountLimit {
				return cn35game.ErrFriendLimitReached
			}
		}
		myLastGive := lastGivePeriodTx(tx, userID, applicantUserID)
		theirLastGive := lastGivePeriodTx(tx, applicantUserID, userID)
		if _, err := tx.Exec(`INSERT OR IGNORE INTO cn35_player_friends
			(user_id, friend_user_id, last_give_period, created_at) VALUES (?,?,?,?)`,
			userID, applicantUserID, myLastGive, nowSec); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO cn35_player_friends
			(user_id, friend_user_id, last_give_period, created_at) VALUES (?,?,?,?)`,
			applicantUserID, userID, theirLastGive, nowSec); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM cn35_player_friend_applies
			WHERE (target_user_id=? AND applicant_user_id=?) OR (target_user_id=? AND applicant_user_id=?)`,
			userID, applicantUserID, applicantUserID, userID); err != nil {
			return err
		}
		out, err = s.friendIndexTx(tx, userID, periodKey)
		return err
	})
	return out, err
}

// DeleteFriends 见 cn35game.FriendStore。
func (s *Store) DeleteFriends(userID string, targetUserIDs []string, periodKey string) (*cn35game.FriendIndexState, error) {
	var out *cn35game.FriendIndexState
	err := s.withTx(func(tx *sql.Tx) error {
		for _, target := range targetUserIDs {
			target = strings.TrimSpace(target)
			if target == "" {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM cn35_player_friends
				WHERE (user_id=? AND friend_user_id=?) OR (user_id=? AND friend_user_id=?)`,
				userID, target, target, userID); err != nil {
				return err
			}
		}
		var err error
		out, err = s.friendIndexTx(tx, userID, periodKey)
		return err
	})
	return out, err
}

// ClearFriendApplyList 见 cn35game.FriendStore。
func (s *Store) ClearFriendApplyList(userID string) error {
	_, err := s.db.Exec(`DELETE FROM cn35_player_friend_applies WHERE target_user_id=?`, userID)
	return err
}

func (s *Store) giveMedalToTargetsTx(tx *sql.Tx, userID string, targets []string, periodKey string) (int64, error) {
	nowSec := s.clockNowUTC().Unix()
	senderSum, _, err := resolveFriendSummaryTx(tx, userID, nowSec)
	if err != nil {
		return 0, err
	}
	for _, fuid := range targets {
		if _, err := tx.Exec(`UPDATE cn35_player_friends SET last_give_period=?
			WHERE user_id=? AND friend_user_id=?`, periodKey, userID, fuid); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO cn35_player_friend_give_log
			(user_id, friend_user_id, last_give_period, updated_at)
			VALUES (?,?,?,?)
			ON CONFLICT(user_id, friend_user_id) DO UPDATE SET
				last_give_period=excluded.last_give_period,
				updated_at=excluded.updated_at`,
			userID, fuid, periodKey, nowSec); err != nil {
			return 0, err
		}
		if npc, isNPC := cn35game.LookupNPCFriend(fuid); isNPC {
			// NPC 好友收到赠送后自动回赠 1 枚友情徽章至玩家礼盒。
			if _, err := tx.Exec(`INSERT INTO cn35_player_friend_medal_gifts
				(uuid, user_id, sender_user_id, sender_name, sender_unit_id, count, created_at_sec)
				VALUES (?,?,?,?,?,1,?)`,
				"fmg-"+randHex(16), userID, npc.UserID, npc.Name, npc.UnitID, nowSec); err != nil {
				return 0, err
			}
		} else {
			if _, err := tx.Exec(`INSERT INTO cn35_player_friend_medal_gifts
				(uuid, user_id, sender_user_id, sender_name, sender_unit_id, count, created_at_sec)
				VALUES (?,?,?,?,?,1,?)`,
				"fmg-"+randHex(16), fuid, userID, senderSum.Name, senderSum.UnitID, nowSec); err != nil {
				return 0, err
			}
		}
	}
	curCP := playerCPTx(tx, userID)
	rawGain := int64(len(targets)) * cn35game.FriendRules.CPPerGive
	nextCP := curCP + rawGain
	if nextCP > cn35game.FriendRules.MaxCP {
		nextCP = cn35game.FriendRules.MaxCP
	}
	gainedCP := nextCP - curCP
	if gainedCP < 0 {
		gainedCP = 0
	}
	if err := setPlayerCPTx(tx, userID, nextCP); err != nil {
		return 0, err
	}
	return gainedCP, nil
}

// GiveFriendMedal 见 cn35game.FriendStore。
func (s *Store) GiveFriendMedal(userID, friendUserID, periodKey string) (*cn35game.FriendGiveResult, error) {
	if userID == "" || friendUserID == "" || periodKey == "" {
		return nil, cn35game.ErrInvalidRequest
	}
	var out *cn35game.FriendGiveResult
	err := s.withTx(func(tx *sql.Tx) error {
		var lastPeriod string
		err := tx.QueryRow(`SELECT last_give_period FROM cn35_player_friends
			WHERE user_id=? AND friend_user_id=?`, userID, friendUserID).Scan(&lastPeriod)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrFriendTargetNotFound
		}
		if err != nil {
			return err
		}
		if lastPeriod == periodKey || lastGivePeriodTx(tx, userID, friendUserID) == periodKey {
			return cn35game.ErrFriendNothingToGive
		}
		var givenToday int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_friend_give_log
			WHERE user_id=? AND last_give_period=?`, userID, periodKey).Scan(&givenToday); err != nil {
			return err
		}
		if givenToday >= cn35game.FriendRules.FriendCountLimit {
			return cn35game.ErrFriendNothingToGive
		}
		gainedCP, err := s.giveMedalToTargetsTx(tx, userID, []string{friendUserID}, periodKey)
		if err != nil {
			return err
		}
		idx, err := s.friendIndexTx(tx, userID, periodKey)
		if err != nil {
			return err
		}
		out = &cn35game.FriendGiveResult{
			GivenCount: 1,
			GainedCP:   gainedCP,
			Index:      idx,
		}
		return nil
	})
	return out, err
}

// GiveAllFriendMedals 见 cn35game.FriendStore。
func (s *Store) GiveAllFriendMedals(userID, periodKey string, confirm bool) (*cn35game.FriendGiveResult, error) {
	if userID == "" || periodKey == "" {
		return nil, cn35game.ErrInvalidRequest
	}
	var out *cn35game.FriendGiveResult
	err := s.withTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT friend_user_id FROM cn35_player_friends
			WHERE user_id=? AND last_give_period != ?
			ORDER BY created_at ASC, friend_user_id ASC`, userID, periodKey)
		if err != nil {
			return err
		}
		var rawTargets []string
		for rows.Next() {
			var fuid string
			if err := rows.Scan(&fuid); err != nil {
				rows.Close()
				return err
			}
			rawTargets = append(rawTargets, fuid)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		var targets []string
		for _, fuid := range rawTargets {
			if lastGivePeriodTx(tx, userID, fuid) != periodKey {
				targets = append(targets, fuid)
			}
		}
		var givenToday int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_friend_give_log
			WHERE user_id=? AND last_give_period=?`, userID, periodKey).Scan(&givenToday); err != nil {
			return err
		}
		remainingQuota := cn35game.FriendRules.FriendCountLimit - givenToday
		if remainingQuota <= 0 {
			return cn35game.ErrFriendNothingToGive
		}
		if len(targets) > remainingQuota {
			targets = targets[:remainingQuota]
		}
		if len(targets) == 0 {
			return cn35game.ErrFriendNothingToGive
		}
		curCP := playerCPTx(tx, userID)
		rawGain := int64(len(targets)) * cn35game.FriendRules.CPPerGive
		if curCP+rawGain > cn35game.FriendRules.MaxCP && !confirm {
			return cn35game.ErrFriendGiveCPOverflow
		}
		gainedCP, err := s.giveMedalToTargetsTx(tx, userID, targets, periodKey)
		if err != nil {
			return err
		}
		idx, err := s.friendIndexTx(tx, userID, periodKey)
		if err != nil {
			return err
		}
		out = &cn35game.FriendGiveResult{
			GivenCount: len(targets),
			GainedCP:   gainedCP,
			Index:      idx,
		}
		return nil
	})
	return out, err
}

func queryFriendMedalGiftsTx(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, userID string) ([]cn35game.FriendMedalGift, error) {
	rows, err := q.Query(`SELECT uuid, created_at_sec, sender_user_id, sender_name, sender_unit_id, count
		FROM cn35_player_friend_medal_gifts WHERE user_id=?
		ORDER BY created_at_sec DESC, uuid ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cn35game.FriendMedalGift
	for rows.Next() {
		var g cn35game.FriendMedalGift
		if err := rows.Scan(&g.UUID, &g.CreatedAtSec, &g.SenderUserID, &g.SenderName, &g.SenderUnitID, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// FriendMedalGifts 见 cn35game.FriendStore。
func (s *Store) FriendMedalGifts(userID string) ([]cn35game.FriendMedalGift, error) {
	return queryFriendMedalGiftsTx(s.db, userID)
}

// AcquireFriendMedals 见 cn35game.FriendStore。
func (s *Store) AcquireFriendMedals(userID, uuid string) (*cn35game.FriendAcquireResult, error) {
	if userID == "" {
		return nil, cn35game.ErrInvalidRequest
	}
	var out *cn35game.FriendAcquireResult
	err := s.withTx(func(tx *sql.Tx) error {
		var total int64
		if uuid != "" {
			var cnt int64
			err := tx.QueryRow(`SELECT count FROM cn35_player_friend_medal_gifts
				WHERE user_id=? AND uuid=?`, userID, uuid).Scan(&cnt)
			if errors.Is(err, sql.ErrNoRows) {
				return cn35game.ErrFriendMedalGiftEmpty
			}
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM cn35_player_friend_medal_gifts WHERE user_id=? AND uuid=?`, userID, uuid); err != nil {
				return err
			}
			total = cnt
		} else {
			err := tx.QueryRow(`SELECT COALESCE(SUM(count),0) FROM cn35_player_friend_medal_gifts WHERE user_id=?`, userID).Scan(&total)
			if err != nil {
				return err
			}
			if total <= 0 {
				return cn35game.ErrFriendMedalGiftEmpty
			}
			if _, err := tx.Exec(`DELETE FROM cn35_player_friend_medal_gifts WHERE user_id=?`, userID); err != nil {
				return err
			}
		}
		if err := creditBalanceTx(tx, userID, "friend_medal", total); err != nil {
			return err
		}
		remain, err := queryFriendMedalGiftsTx(tx, userID)
		if err != nil {
			return err
		}
		medalBal := balanceOfTx(tx, userID, "friend_medal")
		out = &cn35game.FriendAcquireResult{
			AcquiredCount:    total,
			FriendMedalCount: medalBal,
			MedalPackageCnt:  len(remain),
			RemainingGifts:   remain,
		}
		return nil
	})
	return out, err
}
