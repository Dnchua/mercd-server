package cn35store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// M10 癒术士大赛 / 公会训练持久层（迁移 0057）。届/周/日判定一律取 Store 时钟；
// 会话状态条件更新保证 lc_used/rc_used/attack/lose 重放与并发只生效一次。

func newLadderBid() string { return "lb" + randHex(14) }
func newRobBid() string    { return "rbt" + randHex(13) }
func newRobRecID() string  { return "rr" + randHex(14) }

type pvpAccount struct {
	name   string
	level  int
	leader int
	guild  string
}

func pvpAccountTx(tx *sql.Tx, userID string) (pvpAccount, error) {
	var a pvpAccount
	var leader string
	err := tx.QueryRow(`SELECT COALESCE(NULLIF(a.user_name,''),'旅人'), p.level, p.leader_unit_id,
		COALESCE((SELECT g.name FROM cn35_guild_members m JOIN cn35_guilds g ON g.guild_id=m.guild_id
			WHERE m.user_id=a.user_id), '')
		FROM cn35_accounts a JOIN cn35_player_profiles p ON p.user_id=a.user_id
		WHERE a.user_id=?`, userID).Scan(&a.name, &a.level, &leader, &a.guild)
	if errors.Is(err, sql.ErrNoRows) {
		return a, cn35game.ErrPvPOpponent
	}
	a.leader, _ = strconv.Atoi(leader)
	return a, err
}

func weekEndOf(weekKey string) time.Time {
	wi, _ := strconv.Atoi(strings.TrimPrefix(weekKey, "W"))
	return cn35game.LadderRules.SeasonAnchor.Add(time.Duration(wi+1) * 7 * 24 * time.Hour)
}

// ---- 癒术士大赛 ----

type ladderRow struct {
	season, week, day                           string
	score, best, atkWin, atkTotal, defWin       int
	defTotal, weekBattles, dayUsed, dayDefended int
	potions                                     [3]int
}

// ensureLadderProfileTx 读档并按当前届/周/日归一：跨周时为有出战记录的上周写结算台账，
// 跨届清零积分，跨日清零次数。
func ensureLadderProfileTx(tx *sql.Tx, userID string, now time.Time) (*ladderRow, error) {
	per := cn35game.LadderPeriodAt(now)
	day := cn35game.PvPDayKey(now)
	r := &ladderRow{}
	err := tx.QueryRow(`SELECT season_key,week_key,day_key,score,best_win_score,atk_win,atk_total,
		def_win,def_total,week_battles,day_used,day_defended,potion_1,potion_2,potion_3
		FROM cn35_ladder_profiles WHERE user_id=?`, userID).Scan(&r.season, &r.week, &r.day,
		&r.score, &r.best, &r.atkWin, &r.atkTotal, &r.defWin, &r.defTotal, &r.weekBattles,
		&r.dayUsed, &r.dayDefended, &r.potions[0], &r.potions[1], &r.potions[2])
	if errors.Is(err, sql.ErrNoRows) {
		r = &ladderRow{season: per.SeasonKey, week: per.WeekKey, day: day}
		_, err = tx.Exec(`INSERT INTO cn35_ladder_profiles (user_id,season_key,week_key,day_key,updated_at)
			VALUES (?,?,?,?,?)`, userID, per.SeasonKey, per.WeekKey, day, nowUTC())
		return r, err
	}
	if err != nil {
		return nil, err
	}
	changed := false
	if r.week != per.WeekKey {
		if r.weekBattles > 0 {
			tier := cn35game.LadderTierOf(r.score)
			raw, _ := json.Marshal(cn35game.LadderRules.TierReward[tier])
			if _, err := tx.Exec(`INSERT OR IGNORE INTO cn35_ladder_settlements
				(user_id,week_key,season_key,score,tier,reward_json,claimed,expires_at,created_at)
				VALUES (?,?,?,?,?,?,0,?,?)`, userID, r.week, r.season, r.score, tier, string(raw),
				weekEndOf(r.week).Add(cn35game.LadderRules.RewardTTL).Unix(), now.Unix()); err != nil {
				return nil, err
			}
		}
		if r.season != per.SeasonKey {
			r.score, r.best = 0, 0
			r.season = per.SeasonKey
		}
		r.week, r.weekBattles = per.WeekKey, 0
		changed = true
	}
	if r.day != day {
		r.day, r.dayUsed, r.dayDefended = day, 0, 0
		changed = true
	}
	if changed {
		if err := saveLadderRowTx(tx, userID, r); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func saveLadderRowTx(tx *sql.Tx, userID string, r *ladderRow) error {
	_, err := tx.Exec(`UPDATE cn35_ladder_profiles SET season_key=?,week_key=?,day_key=?,score=?,
		best_win_score=?,atk_win=?,atk_total=?,def_win=?,def_total=?,week_battles=?,day_used=?,
		day_defended=?,potion_1=?,potion_2=?,potion_3=?,updated_at=? WHERE user_id=?`,
		r.season, r.week, r.day, r.score, r.best, r.atkWin, r.atkTotal, r.defWin, r.defTotal,
		r.weekBattles, r.dayUsed, r.dayDefended, r.potions[0], r.potions[1], r.potions[2], nowUTC(), userID)
	return err
}

func (r *ladderRow) profile() cn35game.LadderProfile {
	return cn35game.LadderProfile{Score: r.score, BestWinScore: r.best, AtkWin: r.atkWin,
		AtkTotal: r.atkTotal, DefWin: r.defWin, DefTotal: r.defTotal, DayUsed: r.dayUsed,
		DayDefended: r.dayDefended, Potions: r.potions}
}

func ladderGrowTx(tx *sql.Tx, userID string) (map[[2]int]int, map[[2]int]int, error) {
	steps, spent := map[[2]int]int{}, map[[2]int]int{}
	rows, err := tx.Query(`SELECT grow_type,attr_id,steps,spent FROM cn35_ladder_grow WHERE user_id=?`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t, a, st, sp int
		if err := rows.Scan(&t, &a, &st, &sp); err != nil {
			return nil, nil, err
		}
		steps[[2]int{t, a}], spent[[2]int{t, a}] = st, sp
	}
	return steps, spent, rows.Err()
}

func ladderDeckUUIDsTx(tx *sql.Tx, userID string) ([5]string, error) {
	var out [5]string
	rows, err := tx.Query(`SELECT slot_index,monster_uuid FROM cn35_ladder_decks WHERE user_id=?`, userID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var i int
		var u string
		if err := rows.Scan(&i, &u); err != nil {
			return out, err
		}
		if i >= 0 && i < 5 {
			out[i] = u
		}
	}
	return out, rows.Err()
}

// fillDeck 按持有魔宠解析 5 格 uuid（悬空 uuid 视为空格）。必须在事务外调用。
func (s *Store) fillDeck(userID string, uuids [5]string) ([5]*cn35game.MonsterRecord, error) {
	var deck [5]*cn35game.MonsterRecord
	ms, err := s.Monsters(userID)
	if err != nil {
		return deck, err
	}
	byUUID := map[string]cn35game.MonsterRecord{}
	for _, m := range ms {
		byUUID[m.UUID] = m
	}
	for i, u := range uuids {
		if m, ok := byUUID[u]; ok && u != "" {
			m := m
			deck[i] = &m
		}
	}
	return deck, nil
}

func (s *Store) LadderView(userID string) (*cn35game.LadderView, error) {
	now := s.clockNowUTC()
	v := &cn35game.LadderView{}
	var uuids [5]string
	err := s.withTx(func(tx *sql.Tx) error {
		r, err := ensureLadderProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		v.Profile = r.profile()
		if v.Grow, v.Spent, err = ladderGrowTx(tx, userID); err != nil {
			return err
		}
		if uuids, err = ladderDeckUUIDsTx(tx, userID); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT week_key,score,tier,reward_json,expires_at FROM cn35_ladder_settlements
			WHERE user_id=? AND claimed=0 AND expires_at>? ORDER BY week_key`, userID, now.Unix())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var st cn35game.LadderSettlement
			var raw string
			if err := rows.Scan(&st.WeekKey, &st.Score, &st.Tier, &raw, &st.ExpiresAt); err != nil {
				return err
			}
			_ = json.Unmarshal([]byte(raw), &st.Reward)
			v.Pending = append(v.Pending, st)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	v.Deck, err = s.fillDeck(userID, uuids)
	return v, err
}

func (s *Store) LadderSetDeck(userID string, slot int, uuid string) error {
	if slot < 0 || slot > 4 || uuid == "" {
		return cn35game.ErrPvPMonster
	}
	return s.withTx(func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_player_monsters WHERE user_id=? AND uuid=?`,
			userID, uuid).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return cn35game.ErrPvPMonster
		}
		if _, err := tx.Exec(`DELETE FROM cn35_ladder_decks WHERE user_id=? AND (monster_uuid=? OR slot_index=?)`,
			userID, uuid, slot); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO cn35_ladder_decks (user_id,slot_index,monster_uuid,updated_at)
			VALUES (?,?,?,?)`, userID, slot, uuid, nowUTC())
		return err
	})
}

func (s *Store) LadderRemoveDeck(userID string, slot int) error {
	_, err := s.db.Exec(`DELETE FROM cn35_ladder_decks WHERE user_id=? AND slot_index=?`, userID, slot)
	return err
}

func (s *Store) LadderPlayers(userID string, limit int) ([]cn35game.PvPOpponent, error) {
	rows, err := s.db.Query(`SELECT DISTINCT d.user_id FROM cn35_ladder_decks d
		JOIN cn35_player_profiles p ON p.user_id=d.user_id
		WHERE d.user_id<>? AND p.level>=? ORDER BY d.user_id`, userID, cn35game.LadderRules.MinLevel)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := []cn35game.PvPOpponent{}
	for _, id := range ids {
		o, err := s.LadderPlayer(userID, id)
		if err != nil {
			return nil, err
		}
		if o.Deck == ([5]*cn35game.MonsterRecord{}) {
			continue
		}
		out = append(out, *o)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *Store) LadderPlayer(userID, opponentID string) (*cn35game.PvPOpponent, error) {
	if opponentID == userID {
		return nil, cn35game.ErrPvPSelf
	}
	now := s.clockNowUTC()
	o := &cn35game.PvPOpponent{UserID: opponentID}
	var uuids [5]string
	err := s.withTx(func(tx *sql.Tx) error {
		a, err := pvpAccountTx(tx, opponentID)
		if err != nil {
			return err
		}
		o.Name, o.Level, o.LeaderUnitID, o.GuildName = a.name, a.level, a.leader, a.guild
		r, err := ensureLadderProfileTx(tx, opponentID, now)
		if err != nil {
			return err
		}
		o.Score = r.score
		if o.Grow, _, err = ladderGrowTx(tx, opponentID); err != nil {
			return err
		}
		uuids, err = ladderDeckUUIDsTx(tx, opponentID)
		return err
	})
	if err != nil {
		return nil, err
	}
	o.Deck, err = s.fillDeck(opponentID, uuids)
	return o, err
}

func (s *Store) LadderOpen(userID string, opp *cn35game.PvPOpponent, difficulty string, ordinal int,
	drops cn35game.LadderReward) (*cn35game.LadderSession, []cn35game.UnitRecord, []cn35game.MonsterRecord, error) {
	if _, ok := cn35game.LadderDifficulties[difficulty]; !ok {
		return nil, nil, nil, cn35game.ErrPvPDifficulty
	}
	if opp == nil || opp.UserID == userID {
		return nil, nil, nil, cn35game.ErrPvPSelf
	}
	now := s.clockNowUTC()
	var sess *cn35game.LadderSession
	var units []cn35game.UnitRecord
	var seeds []cn35game.MonsterRecord
	err := s.withTx(func(tx *sql.Tx) error {
		r, err := ensureLadderProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		if r.dayUsed >= cn35game.LadderRules.DailyFree {
			return cn35game.ErrPvPNoCount
		}
		var deckN int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_ladder_decks d
			JOIN cn35_player_monsters m ON m.user_id=d.user_id AND m.uuid=d.monster_uuid
			WHERE d.user_id=?`, userID).Scan(&deckN); err != nil {
			return err
		}
		if deckN == 0 {
			return cn35game.ErrPvPNoDefense
		}
		if err := s.ensureDefaultDecksTx(tx, userID); err != nil {
			return err
		}
		_, u, m, err := resolvePartyTx(tx, userID, ordinal, 5)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM cn35_ladder_sessions WHERE user_id=? AND status='prepared'`, userID); err != nil {
			return err
		}
		raw, _ := json.Marshal(drops)
		kind := "player"
		if opp.NPC {
			kind = "npc"
		}
		sess = &cn35game.LadderSession{BattleID: newLadderBid(), UserID: userID, OpponentID: opp.UserID,
			OpponentNPC: opp.NPC, OpponentName: opp.Name, OpponentScore: opp.Score,
			Difficulty: difficulty, Rewards: drops, Status: "prepared"}
		_, err = tx.Exec(`INSERT INTO cn35_ladder_sessions (battle_id,user_id,opponent_id,opponent_kind,
			opponent_name,opponent_score,difficulty_id,rewards_json,status,created_at)
			VALUES (?,?,?,?,?,?,?,?,'prepared',?)`, sess.BattleID, userID, opp.UserID, kind, opp.Name,
			opp.Score, difficulty, string(raw), now.Unix())
		units, seeds = u, m
		return err
	})
	return sess, units, seeds, err
}

func readLadderSessionTx(tx *sql.Tx, userID, battleID string) (*cn35game.LadderSession, error) {
	var ss cn35game.LadderSession
	var kind, raw string
	err := tx.QueryRow(`SELECT battle_id,user_id,opponent_id,opponent_kind,opponent_name,opponent_score,
		difficulty_id,rewards_json,status,score_delta FROM cn35_ladder_sessions WHERE battle_id=? AND user_id=?`,
		battleID, userID).Scan(&ss.BattleID, &ss.UserID, &ss.OpponentID, &kind, &ss.OpponentName,
		&ss.OpponentScore, &ss.Difficulty, &raw, &ss.Status, &ss.ScoreDelta)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, cn35game.ErrPvPSession
	}
	if err != nil {
		return nil, err
	}
	ss.OpponentNPC = kind == "npc"
	_ = json.Unmarshal([]byte(raw), &ss.Rewards)
	return &ss, nil
}

func (s *Store) LadderPay(userID, battleID string) (*cn35game.LadderSession, error) {
	now := s.clockNowUTC()
	var out *cn35game.LadderSession
	err := s.withTx(func(tx *sql.Tx) error {
		ss, err := readLadderSessionTx(tx, userID, battleID)
		if err != nil {
			return err
		}
		out = ss
		if ss.Status != "prepared" {
			return nil
		}
		r, err := ensureLadderProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		if r.dayUsed >= cn35game.LadderRules.DailyFree {
			return cn35game.ErrPvPNoCount
		}
		res, err := tx.Exec(`UPDATE cn35_ladder_sessions SET status='paid' WHERE battle_id=? AND status='prepared'`, battleID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrPvPState
		}
		r.dayUsed++
		r.atkTotal++
		r.weekBattles++
		ss.Status = "paid"
		return saveLadderRowTx(tx, userID, r)
	})
	return out, err
}

func (s *Store) LadderSettle(userID, battleID string, win bool) (*cn35game.LadderSettleResult, error) {
	now := s.clockNowUTC()
	res := &cn35game.LadderSettleResult{Win: win}
	err := s.withTx(func(tx *sql.Tx) error {
		ss, err := readLadderSessionTx(tx, userID, battleID)
		if err != nil {
			return err
		}
		res.Session = ss
		r, err := ensureLadderProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		res.Score = r.score
		switch ss.Status {
		case "won", "lost":
			res.Already, res.Win, res.Gain = true, ss.Status == "won", ss.ScoreDelta
			return nil
		case "prepared":
			if win {
				return cn35game.ErrPvPState
			}
		}
		wasPaid := ss.Status == "paid"
		status, gain := "lost", 0
		if win {
			status = "won"
			gain = cn35game.LadderScoreGain(r.score, ss.OpponentScore, ss.Difficulty)
		}
		upd, err := tx.Exec(`UPDATE cn35_ladder_sessions SET status=?,score_delta=?,settled_at=?
			WHERE battle_id=? AND status=?`, status, gain, now.Unix(), battleID, ss.Status)
		if err != nil {
			return err
		}
		if n, _ := upd.RowsAffected(); n != 1 {
			return cn35game.ErrPvPState
		}
		if win {
			r.score += gain
			r.atkWin++
			if ss.OpponentScore > r.best {
				r.best = ss.OpponentScore
			}
			for i := 0; i < 3; i++ {
				r.potions[i] += ss.Rewards.Potions[i]
			}
			if err := creditBalanceTx(tx, userID, "gacha_point", int64(ss.Rewards.GachaPoint)); err != nil {
				return err
			}
			if err := creditBalanceTx(tx, userID, "guild_asset", int64(ss.Rewards.GuildAsset)); err != nil {
				return err
			}
			res.Gain, res.Score = gain, r.score
			ss.ScoreDelta = gain
		}
		ss.Status = status
		if err := saveLadderRowTx(tx, userID, r); err != nil {
			return err
		}
		if ss.OpponentNPC || !wasPaid {
			return nil
		}
		o, err := ensureLadderProfileTx(tx, ss.OpponentID, now)
		if err != nil {
			return err
		}
		o.defTotal++
		o.dayDefended++
		if win {
			o.score -= res.Gain / 2
			if o.score < 0 {
				o.score = 0
			}
		} else {
			o.defWin++
		}
		return saveLadderRowTx(tx, ss.OpponentID, o)
	})
	return res, err
}

func (s *Store) LadderGrowUpgrade(userID string, growType, attr, count int) error {
	var tab *cn35game.LadderGrowTab
	for i := range cn35game.LadderGrowTabs {
		if cn35game.LadderGrowTabs[i].Type == growType {
			tab = &cn35game.LadderGrowTabs[i]
		}
	}
	if tab == nil || attr < 1 || attr > 5 || count < 1 {
		return cn35game.ErrPvPInvalidArg
	}
	now := s.clockNowUTC()
	return s.withTx(func(tx *sql.Tx) error {
		r, err := ensureLadderProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		steps, spent, err := ladderGrowTx(tx, userID)
		if err != nil {
			return err
		}
		k := [2]int{growType, attr}
		if steps[k]+count > tab.MaxRate/tab.Step {
			return cn35game.ErrPvPGrowFull
		}
		cost := cn35game.LadderGrowPrice(steps, growType, attr) * count
		if r.potions[growType-1] < cost {
			return cn35game.ErrPvPPotion
		}
		r.potions[growType-1] -= cost
		if _, err := tx.Exec(`INSERT INTO cn35_ladder_grow (user_id,grow_type,attr_id,steps,spent,updated_at)
			VALUES (?,?,?,?,?,?) ON CONFLICT(user_id,grow_type,attr_id) DO UPDATE
			SET steps=excluded.steps,spent=excluded.spent,updated_at=excluded.updated_at`,
			userID, growType, attr, steps[k]+count, spent[k]+cost, nowUTC()); err != nil {
			return err
		}
		return saveLadderRowTx(tx, userID, r)
	})
}

func (s *Store) LadderGrowReset(userID string, growType int) error {
	if growType < 1 || growType > 3 {
		return cn35game.ErrPvPInvalidArg
	}
	now := s.clockNowUTC()
	return s.withTx(func(tx *sql.Tx) error {
		r, err := ensureLadderProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		var refund int
		if err := tx.QueryRow(`SELECT COALESCE(SUM(spent),0) FROM cn35_ladder_grow WHERE user_id=? AND grow_type=?`,
			userID, growType).Scan(&refund); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM cn35_ladder_grow WHERE user_id=? AND grow_type=?`, userID, growType); err != nil {
			return err
		}
		r.potions[growType-1] += refund
		return saveLadderRowTx(tx, userID, r)
	})
}

func (s *Store) LadderRanking(limit int) ([]cn35game.LadderRankRow, error) {
	per := cn35game.LadderPeriodAt(s.clockNowUTC())
	rows, err := s.db.Query(`SELECT l.user_id, COALESCE(NULLIF(a.user_name,''),'旅人'), p.level, p.leader_unit_id, l.score
		FROM cn35_ladder_profiles l JOIN cn35_accounts a ON a.user_id=l.user_id
		JOIN cn35_player_profiles p ON p.user_id=l.user_id
		WHERE l.season_key=? AND l.score>0 ORDER BY l.score DESC, l.updated_at ASC, l.user_id LIMIT ?`,
		per.SeasonKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.LadderRankRow{}
	for rows.Next() {
		var r cn35game.LadderRankRow
		var leader string
		if err := rows.Scan(&r.UserID, &r.Name, &r.Level, &leader, &r.Score); err != nil {
			return nil, err
		}
		r.LeaderUnitID, _ = strconv.Atoi(leader)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) LadderClaimRewards(userID string) ([]cn35game.LadderSettlement, error) {
	now := s.clockNowUTC()
	var out []cn35game.LadderSettlement
	err := s.withTx(func(tx *sql.Tx) error {
		if _, err := ensureLadderProfileTx(tx, userID, now); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT week_key,score,tier,reward_json,expires_at FROM cn35_ladder_settlements
			WHERE user_id=? AND claimed=0 AND expires_at>? ORDER BY week_key`, userID, now.Unix())
		if err != nil {
			return err
		}
		for rows.Next() {
			var st cn35game.LadderSettlement
			var raw string
			if err := rows.Scan(&st.WeekKey, &st.Score, &st.Tier, &raw, &st.ExpiresAt); err != nil {
				rows.Close()
				return err
			}
			_ = json.Unmarshal([]byte(raw), &st.Reward)
			out = append(out, st)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(out) == 0 {
			return cn35game.ErrPvPNoReward
		}
		for i, st := range out {
			res, err := tx.Exec(`UPDATE cn35_ladder_settlements SET claimed=1 WHERE user_id=? AND week_key=? AND claimed=0`,
				userID, st.WeekKey)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrPvPNoReward
			}
			if err := creditBalanceTx(tx, userID, "gacha_point", int64(st.Reward.GachaPoint)); err != nil {
				return err
			}
			if err := creditBalanceTx(tx, userID, "medal", int64(st.Reward.Medal)); err != nil {
				return err
			}
			out[i].Claimed = true
		}
		return nil
	})
	return out, err
}

// pvpUserFields Snapshot 用：lc/max_lc/rc/max_rc/potions 投影（只读，不归一写库）。
func (s *Store) pvpUserFields(userID string, user map[string]any) {
	now := s.clockNowUTC()
	day := cn35game.PvPDayKey(now)
	var lDay string
	var used int
	var p [3]int64
	err := s.db.QueryRow(`SELECT day_key,day_used,potion_1,potion_2,potion_3 FROM cn35_ladder_profiles WHERE user_id=?`,
		userID).Scan(&lDay, &used, &p[0], &p[1], &p[2])
	if err == nil {
		if lDay != day {
			used = 0
		}
		left := cn35game.LadderRules.DailyFree - used
		if left < 0 {
			left = 0
		}
		user["lc"] = int64(left)
		user["max_lc"] = int64(cn35game.LadderRules.DailyFree)
		user["potions"] = map[string]any{"1": p[0], "2": p[1], "3": p[2]}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	var rDay string
	var rc int
	err = s.db.QueryRow(`SELECT day_key,rc_left FROM cn35_rob_profiles WHERE user_id=?`, userID).Scan(&rDay, &rc)
	if errors.Is(err, sql.ErrNoRows) || err == nil && rDay != day {
		rc = 0
	} else if err != nil {
		return
	}
	user["rc"] = int64(rc)
	user["max_rc"] = int64(cn35game.RobRules.MaxRC)
}

// ---- 公会训练 ----

type robRow struct {
	score, rc, tickets, atkWin, atkTotal, defWin, defTotal int
	day                                                    string
}

func ensureRobProfileTx(tx *sql.Tx, userID string, now time.Time) (*robRow, error) {
	day := cn35game.PvPDayKey(now)
	r := &robRow{}
	err := tx.QueryRow(`SELECT score,day_key,rc_left,tickets_bought,atk_win,atk_total,def_win,def_total
		FROM cn35_rob_profiles WHERE user_id=?`, userID).Scan(&r.score, &r.day, &r.rc, &r.tickets,
		&r.atkWin, &r.atkTotal, &r.defWin, &r.defTotal)
	if errors.Is(err, sql.ErrNoRows) {
		r = &robRow{score: cn35game.RobRules.StartScore, day: day}
		_, err = tx.Exec(`INSERT INTO cn35_rob_profiles (user_id,score,day_key,updated_at) VALUES (?,?,?,?)`,
			userID, r.score, day, nowUTC())
		return r, err
	}
	if err != nil {
		return nil, err
	}
	if r.day != day {
		r.day, r.rc, r.tickets = day, 0, 0
		if err := saveRobRowTx(tx, userID, r); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func saveRobRowTx(tx *sql.Tx, userID string, r *robRow) error {
	_, err := tx.Exec(`UPDATE cn35_rob_profiles SET score=?,day_key=?,rc_left=?,tickets_bought=?,atk_win=?,
		atk_total=?,def_win=?,def_total=?,updated_at=? WHERE user_id=?`, r.score, r.day, r.rc, r.tickets,
		r.atkWin, r.atkTotal, r.defWin, r.defTotal, nowUTC(), userID)
	return err
}

func (r *robRow) profile() cn35game.RobProfile {
	return cn35game.RobProfile{Score: r.score, RCLeft: r.rc, TicketsBought: r.tickets,
		AtkWin: r.atkWin, AtkTotal: r.atkTotal, DefWin: r.defWin, DefTotal: r.defTotal}
}

// robDecksTx 读攻/守编队格，只保留当前仍持有且不在待机所的同伴。
func robDecksTx(tx *sql.Tx, userID, partyType string) ([2][5]int, error) {
	var out [2][5]int
	rows, err := tx.Query(`SELECT d.party_id,d.slot_index,d.unit_id FROM cn35_rob_decks d
		JOIN cn35_player_units u ON u.user_id=d.user_id AND u.unit_id=d.unit_id AND u.is_waiting=0
		WHERE d.user_id=? AND d.party_type=?`, userID, partyType)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p, i, u int
		if err := rows.Scan(&p, &i, &u); err != nil {
			return out, err
		}
		if p >= 1 && p <= 2 && i >= 0 && i < 5 {
			out[p-1][i] = u
		}
	}
	return out, rows.Err()
}

func deckFull(d [2][5]int) bool {
	for _, p := range d {
		for _, u := range p {
			if u == 0 {
				return false
			}
		}
	}
	return true
}

func robUnitsTx(tx *sql.Tx, userID string, d [2][5]int) ([]cn35game.UnitRecord, error) {
	out := []cn35game.UnitRecord{}
	for _, p := range d {
		for _, id := range p {
			if id == 0 {
				continue
			}
			var r cn35game.UnitRecord
			var prot, newc int
			if err := tx.QueryRow(`SELECT unit_id,level,duplicated_count,protected,newcomer,monster_uuid
				FROM cn35_player_units WHERE user_id=? AND unit_id=? AND is_waiting=0`, userID, id).
				Scan(&r.UnitID, &r.Level, &r.DuplicatedCount, &prot, &newc, &r.MonsterUUID); err != nil {
				return nil, err
			}
			r.Protected, r.Newcomer = prot != 0, newc != 0
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Store) RobView(userID string) (*cn35game.RobView, error) {
	now := s.clockNowUTC()
	v := &cn35game.RobView{}
	err := s.withTx(func(tx *sql.Tx) error {
		r, err := ensureRobProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		v.Profile = r.profile()
		if v.Atk, err = robDecksTx(tx, userID, "atk"); err != nil {
			return err
		}
		if v.Def, err = robDecksTx(tx, userID, "def"); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT record_id,attacker_id,attacker_win,score_delta,revenged,created_at
			FROM cn35_rob_records WHERE defender_id=? ORDER BY created_at DESC, record_id LIMIT ?`,
			userID, cn35game.RobRules.RecordMax)
		if err != nil {
			return err
		}
		for rows.Next() {
			var rec cn35game.RobRecord
			var win, rev int
			if err := rows.Scan(&rec.RecordID, &rec.AttackerID, &win, &rec.ScoreDelta, &rev, &rec.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			rec.AttackerWin, rec.Revenged = win != 0, rev != 0
			v.Records = append(v.Records, rec)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for i := range v.Records {
			a, err := pvpAccountTx(tx, v.Records[i].AttackerID)
			if err != nil && !errors.Is(err, cn35game.ErrPvPOpponent) {
				return err
			}
			v.Records[i].AttackerName, v.Records[i].AttackerLvl = a.name, a.level
			v.Records[i].AttackerLead, v.Records[i].AttackerGuild = a.leader, a.guild
		}
		return nil
	})
	return v, err
}

func (s *Store) RobSetDeck(userID, partyType string, partyID, slot, unitID int, on bool, weaponOf func(unitID int) int) error {
	if (partyType != "atk" && partyType != "def") || partyID < 1 || partyID > 2 || slot < 0 || slot > 4 {
		return cn35game.ErrPvPUnit
	}
	return s.withTx(func(tx *sql.Tx) error {
		if !on {
			_, err := tx.Exec(`DELETE FROM cn35_rob_decks WHERE user_id=? AND party_type=? AND party_id=? AND slot_index=?`,
				userID, partyType, partyID, slot)
			return err
		}
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_player_units WHERE user_id=? AND unit_id=? AND is_waiting=0`,
			userID, unitID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return cn35game.ErrPvPUnit
		}
		if weaponOf != nil {
			w := weaponOf(unitID)
			if limit, ok := cn35game.RobRules.CareerLimit[w]; ok {
				d, err := robDecksTx(tx, userID, partyType)
				if err != nil {
					return err
				}
				cnt := 0
				for p := 0; p < 2; p++ {
					for i, u := range d[p] {
						if u == 0 || u == unitID || (p == partyID-1 && i == slot) {
							continue
						}
						if weaponOf(u) == w {
							cnt++
						}
					}
				}
				if cnt >= limit {
					return cn35game.ErrPvPCareerLimit
				}
			}
		}
		if _, err := tx.Exec(`DELETE FROM cn35_rob_decks WHERE user_id=? AND party_type=?
			AND (unit_id=? OR (party_id=? AND slot_index=?))`, userID, partyType, unitID, partyID, slot); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO cn35_rob_decks (user_id,party_type,party_id,slot_index,unit_id,updated_at)
			VALUES (?,?,?,?,?,?)`, userID, partyType, partyID, slot, unitID, nowUTC())
		return err
	})
}

func (s *Store) RobBuyTicket(userID string, ticketID int) error {
	now := s.clockNowUTC()
	return s.withTx(func(tx *sql.Tx) error {
		r, err := ensureRobProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		if ticketID < 1 || ticketID > len(cn35game.RobRules.TicketPrices) || ticketID != r.tickets+1 || r.rc > 0 {
			return cn35game.ErrPvPTicket
		}
		if price := cn35game.RobRules.TicketPrices[ticketID-1]; price > 0 {
			res, err := tx.Exec(`UPDATE cn35_player_balances SET amount=amount-?,updated_at=?
				WHERE user_id=? AND currency='gacha_point' AND amount>=?`, price, nowUTC(), userID, price)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrPvPGold
			}
		}
		r.tickets = ticketID
		r.rc = cn35game.RobRules.MaxRC
		return saveRobRowTx(tx, userID, r)
	})
}

func (s *Store) RobPlayers(userID string, limit int) ([]cn35game.PvPOpponent, error) {
	rows, err := s.db.Query(`SELECT user_id FROM cn35_rob_decks WHERE party_type='def' AND user_id<>?
		GROUP BY user_id HAVING count(*)=10 ORDER BY user_id`, userID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := []cn35game.PvPOpponent{}
	for _, id := range ids {
		o, err := s.RobPlayer(userID, id)
		if errors.Is(err, cn35game.ErrPvPOpponent) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *Store) RobPlayer(userID, opponentID string) (*cn35game.PvPOpponent, error) {
	if opponentID == userID {
		return nil, cn35game.ErrPvPSelf
	}
	now := s.clockNowUTC()
	o := &cn35game.PvPOpponent{UserID: opponentID}
	err := s.withTx(func(tx *sql.Tx) error {
		a, err := pvpAccountTx(tx, opponentID)
		if err != nil {
			return err
		}
		o.Name, o.Level, o.LeaderUnitID, o.GuildName = a.name, a.level, a.leader, a.guild
		r, err := ensureRobProfileTx(tx, opponentID, now)
		if err != nil {
			return err
		}
		o.Score = r.score
		d, err := robDecksTx(tx, opponentID, "def")
		if err != nil {
			return err
		}
		if !deckFull(d) {
			return cn35game.ErrPvPOpponent
		}
		o.DefUnits, err = robUnitsTx(tx, opponentID, d)
		return err
	})
	if err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Store) RobOpen(userID string, opp *cn35game.PvPOpponent, revengeID string) (*cn35game.RobSession, []cn35game.UnitRecord, []cn35game.MonsterRecord, error) {
	if opp == nil || opp.UserID == userID {
		return nil, nil, nil, cn35game.ErrPvPSelf
	}
	now := s.clockNowUTC()
	var sess *cn35game.RobSession
	var units []cn35game.UnitRecord
	var seeds []cn35game.MonsterRecord
	err := s.withTx(func(tx *sql.Tx) error {
		r, err := ensureRobProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		if r.rc <= 0 {
			return cn35game.ErrPvPNoCount
		}
		d, err := robDecksTx(tx, userID, "atk")
		if err != nil {
			return err
		}
		if !deckFull(d) {
			return cn35game.ErrPvPAttackDeck
		}
		if units, err = robUnitsTx(tx, userID, d); err != nil {
			return err
		}
		if seeds, err = partyMonstersTx(tx, userID, units); err != nil {
			return err
		}
		if revengeID != "" {
			var n int
			if err := tx.QueryRow(`SELECT count(*) FROM cn35_rob_records WHERE record_id=? AND defender_id=?
				AND attacker_id=? AND attacker_win=1 AND revenged=0`, revengeID, userID, opp.UserID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				revengeID = ""
			}
		}
		if _, err := tx.Exec(`DELETE FROM cn35_rob_sessions WHERE user_id=? AND status='prepared'`, userID); err != nil {
			return err
		}
		kind := "player"
		if opp.NPC {
			kind = "npc"
		}
		sess = &cn35game.RobSession{BattleID: newRobBid(), UserID: userID, OpponentID: opp.UserID,
			OpponentNPC: opp.NPC, OpponentName: opp.Name, OpponentScore: opp.Score, RevengeID: revengeID,
			Status: "prepared"}
		_, err = tx.Exec(`INSERT INTO cn35_rob_sessions (battle_id,user_id,opponent_id,opponent_kind,opponent_name,
			opponent_score,revenge_record_id,status,created_at) VALUES (?,?,?,?,?,?,?,'prepared',?)`,
			sess.BattleID, userID, opp.UserID, kind, opp.Name, opp.Score, revengeID, now.Unix())
		return err
	})
	return sess, units, seeds, err
}

func readRobSessionTx(tx *sql.Tx, userID, battleID string) (*cn35game.RobSession, error) {
	var ss cn35game.RobSession
	var kind string
	err := tx.QueryRow(`SELECT battle_id,user_id,opponent_id,opponent_kind,opponent_name,opponent_score,
		revenge_record_id,status,score_delta FROM cn35_rob_sessions WHERE battle_id=? AND user_id=?`,
		battleID, userID).Scan(&ss.BattleID, &ss.UserID, &ss.OpponentID, &kind, &ss.OpponentName,
		&ss.OpponentScore, &ss.RevengeID, &ss.Status, &ss.ScoreDelta)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, cn35game.ErrPvPSession
	}
	ss.OpponentNPC = kind == "npc"
	return &ss, err
}

func (s *Store) RobPay(userID, battleID string) (*cn35game.RobSession, error) {
	now := s.clockNowUTC()
	var out *cn35game.RobSession
	err := s.withTx(func(tx *sql.Tx) error {
		ss, err := readRobSessionTx(tx, userID, battleID)
		if err != nil {
			return err
		}
		out = ss
		if ss.Status != "prepared" {
			return nil
		}
		r, err := ensureRobProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		if r.rc <= 0 {
			return cn35game.ErrPvPNoCount
		}
		res, err := tx.Exec(`UPDATE cn35_rob_sessions SET status='paid' WHERE battle_id=? AND status='prepared'`, battleID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrPvPState
		}
		r.rc--
		r.atkTotal++
		ss.Status = "paid"
		return saveRobRowTx(tx, userID, r)
	})
	return out, err
}

func (s *Store) RobSettle(userID, battleID string, win bool) (*cn35game.RobSettleResult, error) {
	now := s.clockNowUTC()
	res := &cn35game.RobSettleResult{Win: win}
	err := s.withTx(func(tx *sql.Tx) error {
		ss, err := readRobSessionTx(tx, userID, battleID)
		if err != nil {
			return err
		}
		res.Session = ss
		r, err := ensureRobProfileTx(tx, userID, now)
		if err != nil {
			return err
		}
		res.Score = r.score
		switch ss.Status {
		case "won", "lost":
			res.Already, res.Win, res.Gain = true, ss.Status == "won", ss.ScoreDelta
			return nil
		case "prepared":
			if win {
				return cn35game.ErrPvPState
			}
		}
		wasPaid := ss.Status == "paid"
		gain := 0
		status := "lost"
		if win {
			status = "won"
			gain = cn35game.RobScoreGain(r.score, ss.OpponentScore)
		}
		upd, err := tx.Exec(`UPDATE cn35_rob_sessions SET status=?,score_delta=?,settled_at=? WHERE battle_id=? AND status=?`,
			status, gain, now.Unix(), battleID, ss.Status)
		if err != nil {
			return err
		}
		if n, _ := upd.RowsAffected(); n != 1 {
			return cn35game.ErrPvPState
		}
		ss.Status, ss.ScoreDelta = status, gain
		if win {
			r.score += gain
			r.atkWin++
			res.Gain, res.Score = gain, r.score
			if ss.RevengeID != "" {
				if _, err := tx.Exec(`UPDATE cn35_rob_records SET revenged=1 WHERE record_id=?`, ss.RevengeID); err != nil {
					return err
				}
			}
		}
		if err := saveRobRowTx(tx, userID, r); err != nil {
			return err
		}
		if ss.OpponentNPC || !wasPaid {
			return nil
		}
		o, err := ensureRobProfileTx(tx, ss.OpponentID, now)
		if err != nil {
			return err
		}
		o.defTotal++
		loss := 0
		if win {
			loss = gain / 2
			o.score -= loss
			if o.score < 0 {
				o.score = 0
			}
		} else {
			o.defWin++
		}
		if err := saveRobRowTx(tx, ss.OpponentID, o); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO cn35_rob_records (record_id,defender_id,attacker_id,attacker_win,score_delta,created_at)
			VALUES (?,?,?,?,?,?)`, newRobRecID(), ss.OpponentID, userID, boolInt(win), -loss, now.Unix())
		return err
	})
	return res, err
}

var _ cn35game.PvPStore = (*Store)(nil)
