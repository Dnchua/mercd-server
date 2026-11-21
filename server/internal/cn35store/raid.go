package cn35store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func newRaidID() string    { return "raid" + randHex(12) }
func newRaidBid() string   { return "rb" + randHex(14) }
func newRaidMonID() string { return "mon" + randHex(16) }

// expireRaidsTx 将已到期的活跃暴走魔宠懒收敛为 expired。
func expireRaidsTx(tx *sql.Tx, now int64) error {
	_, err := tx.Exec(`UPDATE cn35_raid_instances
		SET status='expired', finished_at=expired_at
		WHERE status='active' AND expired_at<=?`, now)
	return err
}

func ensureRaidProfileTx(tx *sql.Tx, userID string) (cn35game.RaidProfile, error) {
	p := cn35game.RaidProfile{UserID: userID, RaidLv: 1}
	err := tx.QueryRow(`SELECT raid_lv,raid_pt,discovered_count,killed_count
		FROM cn35_raid_profiles WHERE user_id=?`, userID).
		Scan(&p.RaidLv, &p.RaidPt, &p.DiscoveredCount, &p.KilledCount)
	if errors.Is(err, sql.ErrNoRows) {
		if _, ierr := tx.Exec(`INSERT INTO cn35_raid_profiles
			(user_id,raid_lv,raid_pt,discovered_count,killed_count,updated_at)
			VALUES (?,1,0,0,0,?)`, userID, nowUTC()); ierr != nil {
			return p, ierr
		}
		return p, nil
	}
	return p, err
}

func readRaidProfileTx(tx *sql.Tx, userID string) (cn35game.RaidProfile, error) {
	p := cn35game.RaidProfile{UserID: userID, RaidLv: 1}
	err := tx.QueryRow(`SELECT raid_lv,raid_pt,discovered_count,killed_count
		FROM cn35_raid_profiles WHERE user_id=?`, userID).
		Scan(&p.RaidLv, &p.RaidPt, &p.DiscoveredCount, &p.KilledCount)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	return p, err
}

func scanRaidInstance(row interface{ Scan(...any) error }) (*cn35game.RaidInstance, error) {
	var r cn35game.RaidInstance
	var attacked, helpGuild, helpComm int
	err := row.Scan(
		&r.RaidID, &r.OwnerUserID, &r.OwnerName, &r.GuildID,
		&r.MonsterID, &r.MonsterName, &r.AttrID, &r.Level,
		&r.MaxPoint, &r.CurrentPoint, &r.InitialPower,
		&attacked, &helpGuild, &helpComm,
		&r.Status, &r.SpawnedAt, &r.ExpiredAt, &r.FinishedAt,
	)
	if err != nil {
		return nil, err
	}
	r.Attacked = attacked != 0
	r.HelpGuild = helpGuild != 0
	r.HelpCommunity = helpComm != 0
	if r.OwnerName == "" {
		r.OwnerName = r.OwnerUserID
	}
	return &r, nil
}

const raidInstanceSelectCols = `r.raid_id, r.owner_user_id, COALESCE(NULLIF(a.user_name,''), NULLIF(a.account_name,''), r.owner_user_id), r.guild_id,
	r.monster_id, r.monster_name, r.attr_id, r.level,
	r.max_point, r.current_point, r.initial_power,
	r.attacked, r.help_guild, r.help_community,
	r.status, r.spawned_at, r.expired_at, r.finished_at`

func readRaidInstanceTx(tx *sql.Tx, raidID string) (*cn35game.RaidInstance, error) {
	row := tx.QueryRow(`SELECT `+raidInstanceSelectCols+`
		FROM cn35_raid_instances r
		LEFT JOIN cn35_accounts a ON a.user_id=r.owner_user_id
		WHERE r.raid_id=?`, raidID)
	r, err := scanRaidInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, cn35game.ErrRaidNotFound
	}
	return r, err
}

func queryRaidInstancesTx(tx *sql.Tx, query string, args ...any) ([]cn35game.RaidInstance, error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.RaidInstance{}
	for rows.Next() {
		r, err := scanRaidInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// RaidOverview 聚合并返回玩家暴走主页全量状态（个人进度、自发强袭、公会协力、聊天室协力、击退日志）。
func (s *Store) RaidOverview(userID string, now int64) (*cn35game.RaidOverview, error) {
	var ov *cn35game.RaidOverview
	err := s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		prof, err := readRaidProfileTx(tx, userID)
		if err != nil {
			return err
		}
		myList, err := queryRaidInstancesTx(tx, `SELECT `+raidInstanceSelectCols+`
			FROM cn35_raid_instances r
			LEFT JOIN cn35_accounts a ON a.user_id=r.owner_user_id
			WHERE r.owner_user_id=? AND r.status='active' AND r.expired_at>?
			ORDER BY r.spawned_at DESC LIMIT 1`, userID, now)
		if err != nil {
			return err
		}
		var myRaid *cn35game.RaidInstance
		if len(myList) > 0 {
			cp := myList[0]
			myRaid = &cp
		}

		guildID, _, err := memberRoleTx(tx, userID)
		if err != nil {
			return err
		}
		guildHelps := []cn35game.RaidInstance{}
		if guildID != "" {
			guildHelps, err = queryRaidInstancesTx(tx, `SELECT `+raidInstanceSelectCols+`
				FROM cn35_raid_instances r
				LEFT JOIN cn35_accounts a ON a.user_id=r.owner_user_id
				WHERE r.status='active' AND r.expired_at>? AND r.help_guild=1 AND r.guild_id=?
				  AND r.owner_user_id<>?
				ORDER BY r.spawned_at DESC LIMIT 30`, now, guildID, userID)
			if err != nil {
				return err
			}
		}

		commHelps, err := queryRaidInstancesTx(tx, `SELECT `+raidInstanceSelectCols+`
			FROM cn35_raid_instances r
			LEFT JOIN cn35_accounts a ON a.user_id=r.owner_user_id
			WHERE r.status='active' AND r.expired_at>? AND r.help_community=1
			  AND r.owner_user_id<>?
			ORDER BY r.spawned_at DESC LIMIT 30`, now, userID)
		if err != nil {
			return err
		}

		history, err := queryRaidInstancesTx(tx, `SELECT `+raidInstanceSelectCols+`
			FROM cn35_raid_instances r
			LEFT JOIN cn35_accounts a ON a.user_id=r.owner_user_id
			WHERE r.status<>'active' AND (
				r.owner_user_id=?
				OR EXISTS (SELECT 1 FROM cn35_raid_participants rp WHERE rp.raid_id=r.raid_id AND rp.user_id=?)
			)
			ORDER BY COALESCE(NULLIF(r.finished_at,0), r.expired_at) DESC, r.spawned_at DESC
			LIMIT 30`, userID, userID)
		if err != nil {
			return err
		}

		attackedSet := map[string]bool{}
		if len(guildHelps) > 0 || len(commHelps) > 0 {
			pRows, perr := tx.Query(`SELECT raid_id FROM cn35_raid_participants
				WHERE user_id=? AND hit_count>0`, userID)
			if perr != nil {
				return perr
			}
			for pRows.Next() {
				var rid string
				if serr := pRows.Scan(&rid); serr != nil {
					pRows.Close()
					return serr
				}
				attackedSet[rid] = true
			}
			if perr := pRows.Err(); perr != nil {
				pRows.Close()
				return perr
			}
			if perr := pRows.Close(); perr != nil {
				return perr
			}
			for i := range guildHelps {
				guildHelps[i].ViewerAttacked = attackedSet[guildHelps[i].RaidID]
			}
			for i := range commHelps {
				commHelps[i].ViewerAttacked = attackedSet[commHelps[i].RaidID]
			}
		}

		ov = &cn35game.RaidOverview{
			Profile:       prof,
			MyRaid:        myRaid,
			GuildHelps:    guildHelps,
			CommunityHelp: commHelps,
			MyHistory:     history,
		}
		return nil
	})
	return ov, err
}

// RaidHasActive 判断玩家是否存在未结束的自发暴走魔宠（供活动红点使用）。
func (s *Store) RaidHasActive(userID string, now int64) (bool, error) {
	var has bool
	err := s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_raid_instances
			WHERE owner_user_id=? AND status='active' AND expired_at>?`,
			userID, now).Scan(&n); err != nil {
			return err
		}
		has = n > 0
		return nil
	})
	return has, err
}

// MaybeSpawnRaidOnQuestClear 在玩家通关 AP>0 关卡且无存活自发魔宠时，按触发概率遭遇新暴走魔宠。
func (s *Store) MaybeSpawnRaidOnQuestClear(userID string, meta cn35game.RaidMonsterMeta,
	cfg cn35game.RaidConfig, roll float64, now int64) (*cn35game.RaidInstance, bool, error) {
	cfg = cn35game.NormalizeRaidConfig(cfg)
	var out *cn35game.RaidInstance
	var spawned bool
	err := s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		prof, err := ensureRaidProfileTx(tx, userID)
		if err != nil {
			return err
		}
		var activeCnt int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_raid_instances
			WHERE owner_user_id=? AND status='active' AND expired_at>?`,
			userID, now).Scan(&activeCnt); err != nil {
			return err
		}
		if activeCnt > 0 || cfg.TriggerRate <= 0 || roll >= cfg.TriggerRate {
			return nil
		}
		guildID, _, err := memberRoleTx(tx, userID)
		if err != nil {
			return err
		}
		attrID := meta.AttrID
		if attrID < 1 || attrID > 5 {
			attrID = 4
		}
		name := meta.Name
		if name == "" {
			name = "莫鲁菲涅"
		}
		mid := meta.MonsterID
		if mid <= 0 {
			mid = cfg.BossMonsterID
		}
		maxPt := cn35game.RaidMaxPoint(prof.RaidLv)
		initPow := cn35game.RaidInitialPower(prof.RaidLv)
		rid := newRaidID()
		expAt := now + cfg.TTLSeconds
		if _, err := tx.Exec(`INSERT INTO cn35_raid_instances
			(raid_id,owner_user_id,guild_id,monster_id,monster_name,attr_id,level,
			 max_point,current_point,initial_power,attacked,help_guild,help_community,
			 status,spawned_at,expired_at,finished_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,0,0,0,'active',?,?,0)`,
			rid, userID, guildID, mid, name, attrID, prof.RaidLv,
			maxPt, maxPt, initPow, now, expAt); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE cn35_raid_profiles
			SET discovered_count=discovered_count+1, updated_at=? WHERE user_id=?`,
			nowUTC(), userID); err != nil {
			return err
		}
		inst, err := readRaidInstanceTx(tx, rid)
		if err != nil {
			return err
		}
		out = inst
		spawned = true
		return nil
	})
	return out, spawned, err
}

func canAccessRaidTx(tx *sql.Tx, inst *cn35game.RaidInstance, userID string, allowParticipant bool) (bool, error) {
	if inst == nil || userID == "" {
		return false, nil
	}
	if inst.OwnerUserID == userID || inst.HelpCommunity {
		return true, nil
	}
	if inst.HelpGuild && inst.GuildID != "" {
		gid, _, err := memberRoleTx(tx, userID)
		if err != nil {
			return false, err
		}
		if gid != "" && gid == inst.GuildID {
			return true, nil
		}
	}
	if allowParticipant {
		var cnt int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_raid_participants
			WHERE raid_id=? AND user_id=?`, inst.RaidID, userID).Scan(&cnt); err != nil {
			return false, err
		}
		if cnt > 0 {
			return true, nil
		}
	}
	return false, nil
}

func isAssistFirstHitTx(tx *sql.Tx, inst *cn35game.RaidInstance, userID string) (bool, error) {
	if inst == nil || inst.OwnerUserID == userID {
		return false, nil
	}
	var hitCnt, freeUsed int
	err := tx.QueryRow(`SELECT hit_count,free_first_used FROM cn35_raid_participants
		WHERE raid_id=? AND user_id=?`, inst.RaidID, userID).Scan(&hitCnt, &freeUsed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && (hitCnt > 0 || freeUsed != 0) {
		return false, nil
	}
	var paidFreeCnt int
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_raid_sessions
		WHERE raid_id=? AND user_id=? AND status='bp_paid' AND free_first=1`,
		inst.RaidID, userID).Scan(&paidFreeCnt); err != nil {
		return false, err
	}
	return paidFreeCnt == 0, nil
}

// RaidShow 读取暴走准备页所需的魔宠详情与协力首刀减免标记。
func (s *Store) RaidShow(userID, raidID string, now int64) (*cn35game.RaidDetail, error) {
	var out *cn35game.RaidDetail
	err := s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		inst, err := readRaidInstanceTx(tx, raidID)
		if err != nil {
			return err
		}
		allowed, err := canAccessRaidTx(tx, inst, userID, true)
		if err != nil {
			return err
		}
		if !allowed {
			return cn35game.ErrRaidForbidden
		}
		assistFirst, err := isAssistFirstHitTx(tx, inst, userID)
		if err != nil {
			return err
		}
		out = &cn35game.RaidDetail{
			Instance:       inst,
			AssistFirstHit: assistFirst,
		}
		return nil
	})
	return out, err
}

// RaidOpenBattle 创建一次暴走战斗会话并解析出战编队，校验生存态、BP 与难易度。
func (s *Store) RaidOpenBattle(userID, raidID, difficulty string, ordinal, unitLimit int,
	now int64) (*cn35game.RaidBattleSession, *cn35game.RaidInstance,
	[]cn35game.UnitRecord, []cn35game.MonsterRecord, error) {
	var sess *cn35game.RaidBattleSession
	var inst *cn35game.RaidInstance
	var units []cn35game.UnitRecord
	var seeds []cn35game.MonsterRecord
	err := s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		var err error
		inst, err = readRaidInstanceTx(tx, raidID)
		if err != nil {
			return err
		}
		allowed, err := canAccessRaidTx(tx, inst, userID, false)
		if err != nil {
			return err
		}
		if !allowed {
			return cn35game.ErrRaidForbidden
		}
		if inst.Status == "expired" || (inst.Status == "active" && inst.ExpiredAt <= now) {
			return cn35game.ErrRaidExpired
		}
		if inst.Status != "active" {
			return cn35game.ErrRaidAlreadyDone
		}
		assistFirst, err := isAssistFirstHitTx(tx, inst, userID)
		if err != nil {
			return err
		}
		timeLimit, bpCost, ok := cn35game.RaidDifficultySpec(difficulty, assistFirst)
		if !ok {
			return cn35game.ErrRaidBadDifficulty
		}
		if bpCost > 0 {
			bp, _, perr := profileBPTx(tx, userID)
			if perr != nil {
				return perr
			}
			if bp < int64(bpCost) {
				return cn35game.ErrGWBP
			}
		}
		if err = s.ensureDefaultDecksTx(tx, userID); err != nil {
			return err
		}
		partyID, u, m, err := resolvePartyTx(tx, userID, ordinal, unitLimit)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM cn35_raid_sessions
			WHERE raid_id=? AND user_id=? AND status='prepared'`, raidID, userID); err != nil {
			return err
		}
		battleHP := int64(cn35game.RaidBattleSafeHP(inst.CurrentPoint))
		rawParty, err := json.Marshal(struct {
			PartyID  string                   `json:"party_id"`
			BattleHP int64                    `json:"battle_hp"`
			Units    []cn35game.UnitRecord    `json:"units"`
			Seeds    []cn35game.MonsterRecord `json:"seeds"`
		}{PartyID: partyID, BattleHP: battleHP, Units: u, Seeds: m})
		if err != nil {
			return err
		}
		bid := newRaidBid()
		freeInt := boolInt(assistFirst)
		if _, err := tx.Exec(`INSERT INTO cn35_raid_sessions
			(battle_id,raid_id,user_id,difficulty_id,time_limit,bp_cost,free_first,
			 party_json,status,reported_damage,created_at,settled_at)
			VALUES (?,?,?,?,?,?,?,?,'prepared',0,?,0)`,
			bid, raidID, userID, difficulty, timeLimit, bpCost, freeInt, string(rawParty), now); err != nil {
			return err
		}
		sess = &cn35game.RaidBattleSession{
			BattleID:     bid,
			RaidID:       raidID,
			UserID:       userID,
			DifficultyID: difficulty,
			TimeLimit:    timeLimit,
			BPCost:       bpCost,
			FreeFirst:    assistFirst,
			Status:       "prepared",
			BattleHP:     battleHP,
			PartyID:      partyID,
		}
		units, seeds = u, m
		return nil
	})
	return sess, inst, units, seeds, err
}

func readRaidSessionTx(tx *sql.Tx, userID, battleID string) (*cn35game.RaidBattleSession, error) {
	var s cn35game.RaidBattleSession
	var freeInt int
	var partyJSON string
	err := tx.QueryRow(`SELECT battle_id,raid_id,user_id,difficulty_id,time_limit,bp_cost,
		free_first,party_json,status,reported_damage
		FROM cn35_raid_sessions WHERE battle_id=? AND user_id=?`, battleID, userID).
		Scan(&s.BattleID, &s.RaidID, &s.UserID, &s.DifficultyID, &s.TimeLimit, &s.BPCost,
			&freeInt, &partyJSON, &s.Status, &s.ReportedDamage)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, cn35game.ErrRaidBadSession
	}
	if err != nil {
		return nil, err
	}
	s.FreeFirst = freeInt != 0
	if partyJSON != "" {
		var meta struct {
			PartyID  string `json:"party_id"`
			BattleHP int64  `json:"battle_hp"`
		}
		if jerr := json.Unmarshal([]byte(partyJSON), &meta); jerr == nil {
			s.PartyID = meta.PartyID
			s.BattleHP = meta.BattleHP
		}
	}
	return &s, nil
}

// RaidPayBP 开战 BP 扣费（幂等重放不会重复扣减，同时锁定协力首刀减免名额）。
func (s *Store) RaidPayBP(userID, battleID string, now int64) (*cn35game.RaidBattleSession, error) {
	var out *cn35game.RaidBattleSession
	err := s.withTx(func(tx *sql.Tx) error {
		sess, err := readRaidSessionTx(tx, userID, battleID)
		if err != nil {
			return err
		}
		if sess.Status != "prepared" {
			out = sess
			return nil
		}
		if sess.BPCost > 0 {
			if err := bpSpendTx(tx, userID, sess.BPCost); err != nil {
				return err
			}
		}
		if sess.FreeFirst {
			if _, err := tx.Exec(`INSERT INTO cn35_raid_participants
				(raid_id,user_id,total_damage,hit_count,free_first_used,reward_claimed,pt_earned,last_hit_at)
				VALUES (?,?,0,0,1,0,0,?)
				ON CONFLICT(raid_id,user_id) DO UPDATE SET free_first_used=1`,
				sess.RaidID, userID, now); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE cn35_raid_sessions SET status='bp_paid' WHERE battle_id=?`,
			battleID); err != nil {
			return err
		}
		sess.Status = "bp_paid"
		out = sess
		return nil
	})
	return out, err
}

func upsertRaidParticipantHitTx(tx *sql.Tx, raidID, userID string, damage int64,
	freeFirst bool, now int64) error {
	freeUsed := boolInt(freeFirst)
	_, err := tx.Exec(`INSERT INTO cn35_raid_participants
		(raid_id,user_id,total_damage,hit_count,free_first_used,reward_claimed,pt_earned,last_hit_at)
		VALUES (?,?,?,1,?,0,0,?)
		ON CONFLICT(raid_id,user_id) DO UPDATE SET
			total_damage = cn35_raid_participants.total_damage + excluded.total_damage,
			hit_count = cn35_raid_participants.hit_count + 1,
			free_first_used = MAX(cn35_raid_participants.free_first_used, excluded.free_first_used),
			last_hit_at = excluded.last_hit_at`,
		raidID, userID, damage, freeUsed, now)
	return err
}

func grantRaidRewardTx(tx *sql.Tx, userID, rewardKind, rewardKey string, monsterID, amount int) error {
	if amount <= 0 {
		return nil
	}
	switch rewardKind {
	case "gacha_point", "medal", "coin":
		return creditBalanceTx(tx, userID, rewardKind, int64(amount))
	case "item":
		return creditItemTx(tx, userID, rewardKey, amount)
	case "monster":
		if monsterID <= 0 {
			return nil
		}
		for i := 0; i < amount; i++ {
			scale := cn35game.RollMonsterDropScale()
			if err := InsertMonsterTx(tx, userID, cn35game.MonsterRecord{
				UUID:      newRaidMonID(),
				MonsterID: monsterID,
				Level:     1,
				Scale:     &scale,
				RealScale: &scale,
			}); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("cn35store: unsupported raid reward kind %q", rewardKind)
	}
}

func settleRaidDefeatTx(tx *sql.Tx, inst *cn35game.RaidInstance, bossMeta cn35game.RaidMonsterMeta,
	callerID string, now int64, res *cn35game.RaidSettleResult) error {
	if _, err := tx.Exec(`UPDATE cn35_raid_instances
		SET status='defeated', current_point=0, attacked=1, finished_at=?
		WHERE raid_id=?`, now, inst.RaidID); err != nil {
		return err
	}
	inst.Status = "defeated"
	inst.CurrentPoint = 0
	inst.Attacked = true
	inst.FinishedAt = now

	ownerProf, err := ensureRaidProfileTx(tx, inst.OwnerUserID)
	if err != nil {
		return err
	}
	clearedLv := ownerProf.RaidLv
	if clearedLv < inst.Level {
		clearedLv = inst.Level
	}
	nextLv := clearedLv + 1
	if _, err := tx.Exec(`UPDATE cn35_raid_profiles
		SET raid_lv=?, killed_count=killed_count+1, updated_at=?
		WHERE user_id=?`, nextLv, nowUTC(), inst.OwnerUserID); err != nil {
		return err
	}
	res.Defeated = true
	res.ClearedLv = clearedLv

	// 发现者击退 Lv 奖励：优先使用当前实例自身王宠，未指定时再回退目录默认配置。
	bossID := inst.MonsterID
	if bossID <= 0 {
		bossID = bossMeta.MonsterID
	}
	bossName := inst.MonsterName
	if bossName == "" {
		bossName = bossMeta.Name
	}
	for _, milestone := range cn35game.RaidLvBonusCatalog(bossID, bossName) {
		if milestone.TargetLv != clearedLv {
			continue
		}
		var claimed int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_raid_lv_claims
			WHERE user_id=? AND target_lv=?`, inst.OwnerUserID, clearedLv).Scan(&claimed); err != nil {
			return err
		}
		if claimed == 0 {
			if _, err := tx.Exec(`INSERT INTO cn35_raid_lv_claims (user_id,target_lv,claimed_at)
				VALUES (?,?,?)`, inst.OwnerUserID, clearedLv, nowUTC()); err != nil {
				return err
			}
			if err := grantRaidRewardTx(tx, inst.OwnerUserID, milestone.RewardKind, milestone.RewardKey,
				milestone.MonsterID, milestone.Amount); err != nil {
				return err
			}
			cp := milestone
			res.GrantedLvBonus = &cp
		}
		break
	}

	// 为所有参战者按累计伤害结算击退 pt。
	rows, err := tx.Query(`SELECT user_id,total_damage,reward_claimed
		FROM cn35_raid_participants WHERE raid_id=?`, inst.RaidID)
	if err != nil {
		return err
	}
	type partClaim struct {
		uid     string
		dmg     int64
		claimed int
	}
	var parts []partClaim
	for rows.Next() {
		var p partClaim
		if err := rows.Scan(&p.uid, &p.dmg, &p.claimed); err != nil {
			rows.Close()
			return err
		}
		parts = append(parts, p)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, p := range parts {
		if p.claimed != 0 {
			continue
		}
		pt := cn35game.RaidPtFromDamage(p.dmg)
		if _, err := tx.Exec(`UPDATE cn35_raid_participants
			SET reward_claimed=1, pt_earned=? WHERE raid_id=? AND user_id=?`,
			pt, inst.RaidID, p.uid); err != nil {
			return err
		}
		if pt > 0 && !strings.HasPrefix(p.uid, "npc_helper_") {
			if _, err := ensureRaidProfileTx(tx, p.uid); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE cn35_raid_profiles
				SET raid_pt=raid_pt+?, updated_at=? WHERE user_id=?`,
				pt, nowUTC(), p.uid); err != nil {
				return err
			}
		}
		if p.uid == callerID {
			res.PtEarnedSelf = pt
		}
	}
	return nil
}

func sessionBattleCap(sess *cn35game.RaidBattleSession, currentPoint int64) int64 {
	if sess != nil && sess.BattleHP > 0 {
		return sess.BattleHP
	}
	return int64(cn35game.RaidBattleSafeHP(currentPoint))
}

// RaidReportLose 暴走限时结束或全灭退场结算（上报 damage[raid] 扣减 Boss 血量并解锁协力邀请；若恰好击杀则完成击退结算）。
func (s *Store) RaidReportLose(userID, battleID string, damage int64,
	bossMeta cn35game.RaidMonsterMeta, now int64) (*cn35game.RaidSettleResult, error) {
	if damage < 0 {
		damage = 0
	}
	res := &cn35game.RaidSettleResult{}
	err := s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		sess, err := readRaidSessionTx(tx, userID, battleID)
		if err != nil {
			return err
		}
		inst, err := readRaidInstanceTx(tx, sess.RaidID)
		if err != nil {
			return err
		}
		if sess.Status == "won" || sess.Status == "lost" {
			res.Session = sess
			res.Instance = inst
			res.Already = true
			return nil
		}
		if sess.Status != "bp_paid" {
			return cn35game.ErrRaidBPPaidRequired
		}
		if inst.Status != "active" {
			if _, err := tx.Exec(`UPDATE cn35_raid_sessions
				SET status='lost', reported_damage=0, settled_at=? WHERE battle_id=?`,
				now, battleID); err != nil {
				return err
			}
			sess.Status = "lost"
			res.Session = sess
			res.Instance = inst
			return nil
		}
		capHP := sessionBattleCap(sess, inst.CurrentPoint)
		clamped := damage
		if clamped > capHP {
			clamped = capHP
		}
		if clamped > inst.CurrentPoint {
			clamped = inst.CurrentPoint
		}
		remain := inst.CurrentPoint - clamped
		if err := upsertRaidParticipantHitTx(tx, inst.RaidID, userID, clamped, sess.FreeFirst, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE cn35_raid_sessions
			SET status='lost', reported_damage=?, settled_at=? WHERE battle_id=?`,
			clamped, now, battleID); err != nil {
			return err
		}
		sess.Status = "lost"
		sess.ReportedDamage = clamped
		res.Session = sess
		res.DamageDealt = clamped

		if remain == 0 {
			if err := settleRaidDefeatTx(tx, inst, bossMeta, userID, now, res); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(`UPDATE cn35_raid_instances
				SET current_point=?, attacked=1 WHERE raid_id=?`, remain, inst.RaidID); err != nil {
				return err
			}
			inst.CurrentPoint = remain
			inst.Attacked = true
		}
		res.Instance = inst
		return nil
	})
	return res, err
}

// RaidReportWin 暴走战斗击败本场 Boss 的胜利结算（若总血量 >20 亿封顶则只扣减本场封顶血量，剩余归零时才完成整只击退结算）。
func (s *Store) RaidReportWin(userID, battleID string,
	bossMeta cn35game.RaidMonsterMeta, now int64) (*cn35game.RaidSettleResult, error) {
	res := &cn35game.RaidSettleResult{}
	err := s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		sess, err := readRaidSessionTx(tx, userID, battleID)
		if err != nil {
			return err
		}
		inst, err := readRaidInstanceTx(tx, sess.RaidID)
		if err != nil {
			return err
		}
		if sess.Status == "won" || sess.Status == "lost" {
			res.Session = sess
			res.Instance = inst
			res.Already = true
			return nil
		}
		if sess.Status != "bp_paid" {
			return cn35game.ErrRaidBPPaidRequired
		}
		if inst.Status != "active" {
			if _, err := tx.Exec(`UPDATE cn35_raid_sessions
				SET status='won', reported_damage=0, settled_at=? WHERE battle_id=?`,
				now, battleID); err != nil {
				return err
			}
			sess.Status = "won"
			res.Session = sess
			res.Instance = inst
			return nil
		}
		capHP := sessionBattleCap(sess, inst.CurrentPoint)
		dealt := inst.CurrentPoint
		if dealt > capHP {
			dealt = capHP
		}
		remain := inst.CurrentPoint - dealt
		if err := upsertRaidParticipantHitTx(tx, inst.RaidID, userID, dealt, sess.FreeFirst, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE cn35_raid_sessions
			SET status='won', reported_damage=?, settled_at=? WHERE battle_id=?`,
			dealt, now, battleID); err != nil {
			return err
		}
		sess.Status = "won"
		sess.ReportedDamage = dealt
		res.Session = sess
		res.DamageDealt = dealt
		if remain == 0 {
			if err := settleRaidDefeatTx(tx, inst, bossMeta, userID, now, res); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(`UPDATE cn35_raid_instances
				SET current_point=?, attacked=1 WHERE raid_id=?`, remain, inst.RaidID); err != nil {
				return err
			}
			inst.CurrentPoint = remain
			inst.Attacked = true
		}
		res.Instance = inst
		return nil
	})
	return res, err
}

// RaidCallHelp 发送暴走协力请求（guild / community_topic）；需先自击至少一刀。
func (s *Store) RaidCallHelp(userID, helpType string, soloAssist bool,
	bossMeta cn35game.RaidMonsterMeta, now int64) (*cn35game.RaidInstance, error) {
	if helpType != "guild" && helpType != "community_topic" {
		return nil, cn35game.ErrRaidBadHelpType
	}
	var out *cn35game.RaidInstance
	err := s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		myList, err := queryRaidInstancesTx(tx, `SELECT `+raidInstanceSelectCols+`
			FROM cn35_raid_instances r
			LEFT JOIN cn35_accounts a ON a.user_id=r.owner_user_id
			WHERE r.owner_user_id=? AND r.status='active' AND r.expired_at>?
			ORDER BY r.spawned_at DESC LIMIT 1`, userID, now)
		if err != nil {
			return err
		}
		if len(myList) == 0 {
			return cn35game.ErrRaidNotFound
		}
		inst := myList[0]
		if !inst.Attacked {
			return cn35game.ErrRaidNotAttacked
		}
		if helpType == "guild" {
			gid, _, merr := memberRoleTx(tx, userID)
			if merr != nil {
				return merr
			}
			if gid == "" {
				return cn35game.ErrRaidNoGuild
			}
			if _, err := tx.Exec(`UPDATE cn35_raid_instances
				SET help_guild=1, guild_id=? WHERE raid_id=?`, gid, inst.RaidID); err != nil {
				return err
			}
			inst.HelpGuild = true
			inst.GuildID = gid
		} else {
			if _, err := tx.Exec(`UPDATE cn35_raid_instances
				SET help_community=1 WHERE raid_id=?`, inst.RaidID); err != nil {
				return err
			}
			inst.HelpCommunity = true
		}

		// 单人模拟协力助战开关：首次请求协力且 Boss 仍有 >1 点血量时，安排 NPC 支援削血。
		if soloAssist && inst.CurrentPoint > 1 {
			const npcID = "npc_helper_1"
			var cnt int
			if err := tx.QueryRow(`SELECT count(*) FROM cn35_raid_participants
				WHERE raid_id=? AND user_id=?`, inst.RaidID, npcID).Scan(&cnt); err != nil {
				return err
			}
			if cnt == 0 {
				assistDmg := inst.MaxPoint * 30 / 100
				if assistDmg < 1 {
					assistDmg = 1
				}
				if assistDmg >= inst.CurrentPoint {
					assistDmg = inst.CurrentPoint - 1
				}
				if assistDmg > 0 {
					if err := upsertRaidParticipantHitTx(tx, inst.RaidID, npcID, assistDmg, true, now); err != nil {
						return err
					}
					remain := inst.CurrentPoint - assistDmg
					if _, err := tx.Exec(`UPDATE cn35_raid_instances SET current_point=? WHERE raid_id=?`,
						remain, inst.RaidID); err != nil {
						return err
					}
					inst.CurrentPoint = remain
				}
			}
		}
		_ = bossMeta
		out = &inst
		return nil
	})
	return out, err
}

// RaidGiveup 放走自己发现的活跃暴走魔宠。
func (s *Store) RaidGiveup(userID, raidID string, now int64) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		if raidID == "" {
			err := tx.QueryRow(`SELECT raid_id FROM cn35_raid_instances
				WHERE owner_user_id=? AND status='active' AND expired_at>?
				ORDER BY spawned_at DESC LIMIT 1`, userID, now).Scan(&raidID)
			if errors.Is(err, sql.ErrNoRows) {
				return cn35game.ErrRaidNotFound
			}
			if err != nil {
				return err
			}
		}
		inst, err := readRaidInstanceTx(tx, raidID)
		if err != nil {
			return err
		}
		if inst.OwnerUserID != userID {
			return cn35game.ErrRaidNotOwner
		}
		if inst.Status != "active" {
			return cn35game.ErrRaidAlreadyDone
		}
		_, err = tx.Exec(`UPDATE cn35_raid_instances
			SET status='giveup', finished_at=? WHERE raid_id=?`, now, inst.RaidID)
		return err
	})
}

// RaidDamageList 按伤害降序返回指定暴走魔宠的参战协力者名单（校验调用者可见权限）。
func (s *Store) RaidDamageList(userID, raidID string, now int64) ([]cn35game.RaidDamageRow, error) {
	var out []cn35game.RaidDamageRow
	err := s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		inst, err := readRaidInstanceTx(tx, raidID)
		if err != nil {
			return err
		}
		allowed, err := canAccessRaidTx(tx, inst, userID, true)
		if err != nil {
			return err
		}
		if !allowed {
			return cn35game.ErrRaidForbidden
		}
		rows, err := tx.Query(`SELECT rp.user_id,
			COALESCE(NULLIF(a.user_name,''), NULLIF(a.account_name,''), rp.user_id),
			COALESCE(p.level, 1),
			COALESCE(p.leader_unit_id, ''),
			COALESCE(g.name, '-'),
			rp.total_damage, rp.hit_count
			FROM cn35_raid_participants rp
			LEFT JOIN cn35_accounts a ON a.user_id=rp.user_id
			LEFT JOIN cn35_player_profiles p ON p.user_id=rp.user_id
			LEFT JOIN cn35_guild_members gm ON gm.user_id=rp.user_id
			LEFT JOIN cn35_guilds g ON g.guild_id=gm.guild_id
			WHERE rp.raid_id=?
			ORDER BY rp.total_damage DESC, rp.last_hit_at ASC`, raidID)
		if err != nil {
			return err
		}
		defer rows.Close()
		out = []cn35game.RaidDamageRow{}
		for rows.Next() {
			var r cn35game.RaidDamageRow
			var leaderStr string
			if err := rows.Scan(&r.UserID, &r.UserName, &r.UserLevel, &leaderStr,
				&r.GuildName, &r.TotalDamage, &r.HitCount); err != nil {
				return err
			}
			r.LeaderUnitID = int(socialLeaderUnitIDInt(leaderStr))
			if strings.HasPrefix(r.UserID, "npc_helper_") {
				r.IsNPC = true
				r.UserName = "热心愈术士"
				r.UserLevel = 120
				r.GuildName = "公会援军"
			}
			if r.LeaderUnitID <= 0 {
				r.LeaderUnitID = 152
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// RaidHelpMessages 返回当前调用者可见的活跃暴走求援聊天消息（同公会隔离 + 聊天室公开）。
func (s *Store) RaidHelpMessages(userID string, now int64) ([]cn35game.RaidHelpMessage, error) {
	var out []cn35game.RaidHelpMessage
	err := s.withTx(func(tx *sql.Tx) error {
		if err := expireRaidsTx(tx, now); err != nil {
			return err
		}
		guildID, _, err := memberRoleTx(tx, userID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT r.raid_id, r.owner_user_id,
			COALESCE(NULLIF(a.user_name,''), NULLIF(a.account_name,''), r.owner_user_id),
			COALESCE(p.leader_unit_id, ''),
			r.guild_id, r.level, r.monster_name, r.current_point,
			r.help_guild, r.help_community, r.spawned_at
			FROM cn35_raid_instances r
			LEFT JOIN cn35_accounts a ON a.user_id=r.owner_user_id
			LEFT JOIN cn35_player_profiles p ON p.user_id=r.owner_user_id
			WHERE r.status='active' AND r.expired_at>? AND (r.help_guild=1 OR r.help_community=1)
			ORDER BY r.spawned_at DESC LIMIT 30`, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		out = []cn35game.RaidHelpMessage{}
		for rows.Next() {
			var (
				raidID, ownerUID, ownerName, leaderStr, rGuildID, monName string
				lv                                                        int
				curPt, spawnedAt                                          int64
				helpG, helpC                                              int
			)
			if err := rows.Scan(&raidID, &ownerUID, &ownerName, &leaderStr,
				&rGuildID, &lv, &monName, &curPt, &helpG, &helpC, &spawnedAt); err != nil {
				return err
			}
			leaderID := int(socialLeaderUnitIDInt(leaderStr))
			if leaderID <= 0 {
				leaderID = 152
			}
			if helpG != 0 && guildID != "" && rGuildID == guildID {
				out = append(out, cn35game.RaidHelpMessage{
					ID:           "raid-help-guild-" + raidID,
					Category:     "guild_comment",
					RaidID:       raidID,
					OwnerUserID:  ownerUID,
					OwnerName:    ownerName,
					LeaderUnitID: leaderID,
					Level:        lv,
					MonsterName:  monName,
					CurrentPoint: curPt,
					CreatedAt:    spawnedAt,
				})
			}
			if helpC != 0 {
				out = append(out, cn35game.RaidHelpMessage{
					ID:           "raid-help-comm-" + raidID,
					Category:     "community_comment",
					RaidID:       raidID,
					OwnerUserID:  ownerUID,
					OwnerName:    ownerName,
					LeaderUnitID: leaderID,
					Level:        lv,
					MonsterName:  monName,
					CurrentPoint: curPt,
					CreatedAt:    spawnedAt,
				})
			}
		}
		return rows.Err()
	})
	return out, err
}

// RaidLvClaims 返回玩家当前暴走 Lv 与已领取的里程碑等级集合。
func (s *Store) RaidLvClaims(userID string) (int, map[int]bool, error) {
	lv := 1
	claimed := map[int]bool{}
	err := s.withTx(func(tx *sql.Tx) error {
		prof, err := readRaidProfileTx(tx, userID)
		if err != nil {
			return err
		}
		lv = prof.RaidLv
		rows, err := tx.Query(`SELECT target_lv FROM cn35_raid_lv_claims WHERE user_id=?`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t int
			if err := rows.Scan(&t); err != nil {
				return err
			}
			claimed[t] = true
		}
		return rows.Err()
	})
	return lv, claimed, err
}

// RaidPtShopState 返回玩家当前击退 pt 与各交换所槽位已兑次数。
func (s *Store) RaidPtShopState(userID string) (int64, map[string]int, error) {
	var pt int64
	counts := map[string]int{}
	err := s.withTx(func(tx *sql.Tx) error {
		prof, err := readRaidProfileTx(tx, userID)
		if err != nil {
			return err
		}
		pt = prof.RaidPt
		rows, err := tx.Query(`SELECT slot_id,exchanged_count FROM cn35_raid_pt_exchanges
			WHERE user_id=?`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var slot string
			var c int
			if err := rows.Scan(&slot, &c); err != nil {
				return err
			}
			counts[slot] = c
		}
		return rows.Err()
	})
	return pt, counts, err
}

// RaidExchangePt 原子扣减击退 pt、累加限购计数并把商品发放到账号库存。
func (s *Store) RaidExchangePt(userID, slotID string, count int, good cn35game.RaidPtShopGood,
	now int64) (int64, int, error) {
	if good.SlotID == "" || good.SlotID != slotID {
		return 0, 0, cn35game.ErrRaidBadSlot
	}
	if count <= 0 {
		count = 1
	}
	var newPt int64
	var newUsed int
	err := s.withTx(func(tx *sql.Tx) error {
		prof, err := ensureRaidProfileTx(tx, userID)
		if err != nil {
			return err
		}
		var used int
		err = tx.QueryRow(`SELECT exchanged_count FROM cn35_raid_pt_exchanges
			WHERE user_id=? AND slot_id=?`, userID, slotID).Scan(&used)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if used+count > good.Limit {
			return cn35game.ErrRaidSoldOut
		}
		cost := int64(count) * int64(good.Price)
		if prof.RaidPt < cost {
			return cn35game.ErrRaidNoPt
		}
		newPt = prof.RaidPt - cost
		newUsed = used + count
		if _, err := tx.Exec(`UPDATE cn35_raid_profiles SET raid_pt=?, updated_at=?
			WHERE user_id=?`, newPt, nowUTC(), userID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cn35_raid_pt_exchanges (user_id,slot_id,exchanged_count,updated_at)
			VALUES (?,?,?,?)
			ON CONFLICT(user_id,slot_id) DO UPDATE SET
				exchanged_count=excluded.exchanged_count, updated_at=excluded.updated_at`,
			userID, slotID, newUsed, nowUTC()); err != nil {
			return err
		}
		_ = now
		return grantRaidRewardTx(tx, userID, good.RewardKind, good.RewardKey,
			good.MonsterID, count*good.UnitAmount)
	})
	return newPt, newUsed, err
}

// 编译期接口断言。
var _ cn35game.RaidStore = (*Store)(nil)
