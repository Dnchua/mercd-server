// battle.go STORY350-07：正式剧情战斗生命周期的 SQLite 事务编排。
//
// 全部写面都在 withTx（BEGIN IMMEDIATE）单事务内，方法即事务边界：
//   - OpenBattle      顶替同关同难度滞留会话 + TTL 清扫 + 建档（含 AP 预检）。
//   - PayBattleAP     prepared→ap_paid + 条件扣 AP（ap>=cost）原子提交；
//     非 prepared 态幂等返回——重复 ap_use 零二次扣点。
//   - SettleBattle    ap_paid|continued→cleared：clear_count+1、进度推进、
//     奖励入账（余额/魔宠掉落）、exp/level、台账回填；
//     终态会话（cleared/lost/abandoned）一律 Already 幂等返回；
//     prepared 态兜底先补扣 AP（防跳过 ap_use 的异常顺序）。
//   - LoseBattle      active→lost：战报（time/mc/damage/escaped）+首败标志；
//     escaped=false（Escape 主动放弃）→ 会话 continuable 置否。
//   - ContinueBattle  取最近 lost+continuable → coin 条件扣 1 → continued。
//   - LogBattle       battle_record 归档（不参与结算）。
//
// 账号隔离：所有读写 WHERE user_id 绑定；bid 伪造/跨账号 → ErrBattleNotFound。
package cn35store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// battleScanCols 与 scanBattle 对应的列序。
const battleScanCols = `battle_id,user_id,quest_id,area_id,difficulty,party_id,status,
	ap_cost,rewards_json,exp,next_stage_id,next_area_id,next_quest_id,
	continuable,continue_count,escaped,report_time,rest_mc,damage_json,
	created_at,updated_at,settled_at,config_version`

// scanBattle 读一行会话（NULL 安全）。
func scanBattle(row interface{ Scan(...any) error }) (*cn35game.BattleSession, error) {
	var s cn35game.BattleSession
	var rewards, damage sql.NullString
	var reportTime sql.NullFloat64
	var restMC sql.NullInt64
	var settledAt sql.NullString
	var continuable, escaped int64
	if err := row.Scan(&s.BattleID, &s.UserID, &s.QuestID, &s.AreaID, &s.Difficulty,
		&s.PartyID, &s.Status, &s.APCost, &rewards, &s.Exp,
		&s.NextStageID, &s.NextAreaID, &s.NextQuestID,
		&continuable, &s.ContinueCount, &escaped, &reportTime, &restMC, &damage,
		&s.CreatedAt, &s.UpdatedAt, &settledAt, &s.ConfigVersion); err != nil {
		return nil, err
	}
	s.Continuable, s.Escaped = continuable != 0, escaped != 0
	s.HasReportTime, s.ReportTime = reportTime.Valid, reportTime.Float64
	s.HasRestMC, s.RestMC = restMC.Valid, int(restMC.Int64)
	s.DamageJSON, s.SettledAt = damage.String, settledAt.String
	if rewards.Valid && rewards.String != "" {
		if err := json.Unmarshal([]byte(rewards.String), &s.Rewards); err != nil {
			return nil, fmt.Errorf("cn35store: 会话奖励快照损坏 bid=%s: %w", s.BattleID, err)
		}
	}
	return &s, nil
}

// battleSessionTx 在事务内按 bid 读会话；归属不符 → ErrBattleNotFound。
func battleSessionTx(tx *sql.Tx, userID, battleID string) (*cn35game.BattleSession, error) {
	s, err := scanBattle(tx.QueryRow(`SELECT `+battleScanCols+`
		FROM cn35_battle_sessions WHERE battle_id=?`, battleID))
	if err == sql.ErrNoRows {
		return nil, cn35game.ErrBattleNotFound
	}
	if err != nil {
		return nil, err
	}
	if s.UserID != userID {
		return nil, cn35game.ErrBattleNotFound
	}
	return s, nil
}

// OpenBattle 见 cn35game.BattleStore。建档前同事务完成：
//  1. TTL 清扫：本账号滞留超过 apTTLSeconds 的 prepared/ap_paid 会话→abandoned。
//  2. 顶替：本账号同关同难度的 prepared/ap_paid/lost/continued→abandoned。
//  3. AP 预检（并发兜底；execute 的展示层校验在前置读做）。
func (s *Store) OpenBattle(sess *cn35game.BattleSession, apTTLSeconds int64) error {
	rewardsJSON, err := json.Marshal(sess.Rewards)
	if err != nil {
		return err
	}
	insufficient := false
	err = s.withTx(func(tx *sql.Tx) error {
		now := s.clockNowUTC()
		ap, _, _, err := s.recoverAPTx(tx, sess.UserID, now)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: 账号 %s 无档案", cn35game.ErrBattleNotFound, sess.UserID)
		} else if err != nil {
			return err
		}
		if ap < sess.APCost {
			// 提交已自然恢复的 AP，即使当前仍不足以建战斗会话。
			insufficient = true
			return nil
		}
		return openBattleTx(tx, sess, apTTLSeconds, now, rewardsJSON)
	})
	if err != nil {
		return err
	}
	if insufficient {
		return cn35game.ErrBattleAP
	}
	return nil
}

// OpenBattleForParty resolves the one-based wheel ordinal against the persisted
// display order, validates slots/ownership, then performs AP recovery and
// prepared-session insertion under the same BEGIN IMMEDIATE transaction.
// 返回的魔宠实例是参战单位当前装备的种子（unit.monster_uuid →
// cn35_player_monsters），与编队解析同事务，保证 execute 下发的
// units[].monster_uuid 与 asset_names 引用一致。
func (s *Store) OpenBattleForParty(sess *cn35game.BattleSession, apTTLSeconds int64,
	ordinal, unitLimit int) ([]cn35game.UnitRecord, []cn35game.MonsterRecord, error) {
	if ordinal < 1 {
		return nil, nil, cn35game.ErrDeckNotFound
	}
	rewardsJSON, err := json.Marshal(sess.Rewards)
	if err != nil {
		return nil, nil, err
	}
	var selected []cn35game.UnitRecord
	var seeds []cn35game.MonsterRecord
	insufficient := false
	err = s.withTx(func(tx *sql.Tx) error {
		if err := s.ensureDefaultDecksTx(tx, sess.UserID); err != nil {
			return err
		}
		partyID, records, monsters, err := resolvePartyTx(tx, sess.UserID, ordinal, unitLimit)
		if err != nil {
			return err
		}
		sess.PartyID = partyID
		selected = records
		seeds = monsters
		// RUNE350-01：收益系符文（经验28/金币29）在开战快照冻结时
		// 乘算进 sess.Exp/gacha_point 奖励——结算只读会话快照，战斗中
		// 换装不影响本局收益。
		runes, err := partySkillRowsTx(tx, sess.UserID, records)
		if err != nil {
			return err
		}
		if cn35game.ApplyCollectRunes(sess, runes) {
			if rewardsJSON, err = json.Marshal(sess.Rewards); err != nil {
				return err
			}
		}
		// RUNE350-03：符文本逐怪符文摇奖在队伍解析同事务完成——收集
		// 符文（skill_id 30）因子按参战装备符文乘算，摇中明细记
		// sess.RuneDrops（appear_table 注入用）并入账级 skill 奖励
		// 冻结进 rewards 快照。
		if sess.RuneSpec != nil {
			drops := cn35game.RollRuneDrops(sess.RuneSpec,
				cn35game.RuneCollectFactor(runes))
			if len(drops) > 0 {
				sess.RuneDrops = drops
				for _, d := range drops {
					sess.Rewards = append(sess.Rewards, cn35game.BattleReward{
						Key: "skill", ID: d.SkillID, Count: 1, Level: d.Level,
					})
				}
				if rewardsJSON, err = json.Marshal(sess.Rewards); err != nil {
					return err
				}
			}
		}
		now := s.clockNowUTC()
		ap, _, _, err := s.recoverAPTx(tx, sess.UserID, now)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: 账号 %s 无档案", cn35game.ErrBattleNotFound, sess.UserID)
		}
		if err != nil {
			return err
		}
		if ap < sess.APCost {
			// 与既有 OpenBattle 一致：提交自然恢复，即使无法开战。
			insufficient = true
			return nil
		}
		return openBattleTx(tx, sess, apTTLSeconds, now, rewardsJSON)
	})
	if err != nil {
		return nil, nil, err
	}
	if insufficient {
		return nil, nil, cn35game.ErrBattleAP
	}
	return selected, seeds, nil
}

func resolvePartyTx(tx *sql.Tx, userID string, ordinal, unitLimit int) (string, []cn35game.UnitRecord, []cn35game.MonsterRecord, error) {
	rows, err := tx.Query(`SELECT party_id FROM cn35_player_decks
		WHERE user_id=? ORDER BY display_order,party_id`, userID)
	if err != nil {
		return ``, nil, nil, err
	}
	partyID := ``
	position := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return ``, nil, nil, err
		}
		position++
		if position == ordinal {
			partyID = id
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ``, nil, nil, err
	}
	if err := rows.Close(); err != nil {
		return ``, nil, nil, err
	}
	if partyID == `` {
		return ``, nil, nil, cn35game.ErrDeckNotFound
	}

	slotRows, err := tx.Query(`SELECT slot_index,unit_id FROM cn35_player_deck_slots
		WHERE user_id=? AND party_id=? ORDER BY slot_index`, userID, partyID)
	if err != nil {
		return ``, nil, nil, err
	}
	slots := make([]sql.NullInt64, cn35game.DefaultDeckSlots)
	seenSlots := make([]bool, cn35game.DefaultDeckSlots)
	for slotRows.Next() {
		var index int
		var unit sql.NullInt64
		if err := slotRows.Scan(&index, &unit); err != nil {
			slotRows.Close()
			return ``, nil, nil, err
		}
		if index < 0 || index >= len(slots) || seenSlots[index] {
			slotRows.Close()
			return ``, nil, nil, cn35game.ErrDeckSlotInvalid
		}
		seenSlots[index], slots[index] = true, unit
	}
	if err := slotRows.Err(); err != nil {
		slotRows.Close()
		return ``, nil, nil, err
	}
	if err := slotRows.Close(); err != nil {
		return ``, nil, nil, err
	}
	for _, ok := range seenSlots {
		if !ok {
			return ``, nil, nil, cn35game.ErrDeckSlotInvalid
		}
	}

	records := make([]cn35game.UnitRecord, 0, len(slots))
	seenUnits := map[int]bool{}
	for _, slot := range slots {
		if !slot.Valid {
			continue
		}
		unitID := int(slot.Int64)
		if unitID <= 0 {
			return ``, nil, nil, cn35game.ErrDeckUnitNotOwned
		}
		if seenUnits[unitID] {
			return ``, nil, nil, cn35game.ErrDeckDuplicate
		}
		seenUnits[unitID] = true
		var rec cn35game.UnitRecord
		var protected, newcomer int
		err := tx.QueryRow(`SELECT unit_id,level,duplicated_count,protected,newcomer,monster_uuid
			FROM cn35_player_units WHERE user_id=? AND unit_id=?`, userID, unitID).
			Scan(&rec.UnitID, &rec.Level, &rec.DuplicatedCount, &protected, &newcomer, &rec.MonsterUUID)
		if errors.Is(err, sql.ErrNoRows) {
			return ``, nil, nil, cn35game.ErrDeckUnitNotOwned
		}
		if err != nil {
			return ``, nil, nil, err
		}
		rec.Protected, rec.Newcomer = protected != 0, newcomer != 0
		records = append(records, rec)
	}
	if len(records) == 0 {
		return ``, nil, nil, cn35game.ErrDeckEmpty
	}
	if unitLimit > 0 && len(records) > unitLimit {
		return ``, nil, nil, cn35game.ErrDeckUnitCount
	}
	monsters, err := partyMonstersTx(tx, userID, records)
	if err != nil {
		return ``, nil, nil, err
	}
	return partyID, records, monsters, nil
}

// partyMonstersTx 取参战单位当前装备的魔宠实例行（unit.monster_uuid →
// cn35_player_monsters.uuid）。Size 列不加载——调用方只消费 UUID/MonsterID
// 做资源声明。uuid 悬空（如装备写面之外的手工数据）按客户端同款语义跳过：
// user_monsters 缓存查不到实例时种子面板自行置不可用，而不是崩字典键。
func partyMonstersTx(tx *sql.Tx, userID string, units []cn35game.UnitRecord) ([]cn35game.MonsterRecord, error) {
	var out []cn35game.MonsterRecord
	for _, u := range units {
		if u.MonsterUUID == "" {
			continue
		}
		var r cn35game.MonsterRecord
		var host sql.NullInt64
		var prot int
		err := tx.QueryRow(`SELECT uuid,monster_id,unit_id,level,protected
			FROM cn35_player_monsters WHERE user_id=? AND uuid=?`, userID, u.MonsterUUID).
			Scan(&r.UUID, &r.MonsterID, &host, &r.Level, &prot)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if host.Valid {
			v := int(host.Int64)
			r.UnitID = &v
		}
		r.Protected = prot != 0
		out = append(out, r)
	}
	return out, nil
}

// partySkillRowsTx 读编队单位已装备符文的库存行（unit_id 命中 records）。
func partySkillRowsTx(tx *sql.Tx, userID string, units []cn35game.UnitRecord) ([]cn35game.PlayerSkill, error) {
	if len(units) == 0 {
		return nil, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(units)), ",")
	args := make([]any, 0, len(units)+1)
	args = append(args, userID)
	for _, u := range units {
		args = append(args, u.UnitID)
	}
	rows, err := tx.Query(`SELECT `+skillCols+`
		FROM cn35_player_skills WHERE user_id=? AND unit_id IN (`+marks+`)`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.PlayerSkill{}
	for rows.Next() {
		sk, err := scanSkillRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

func openBattleTx(tx *sql.Tx, sess *cn35game.BattleSession, apTTLSeconds int64,
	now time.Time, rewardsJSON []byte) error {
	stamp := now.UTC().Format(time.RFC3339Nano)
	cutoff := now.Add(-time.Duration(apTTLSeconds) * time.Second).Format(`2006-01-02T15:04:05.000Z`)
	if _, err := tx.Exec(`UPDATE cn35_battle_sessions SET status='abandoned',updated_at=?
		WHERE user_id=? AND status IN ('prepared','ap_paid') AND updated_at<?`,
		stamp, sess.UserID, cutoff); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE cn35_battle_sessions SET status='abandoned',updated_at=?
		WHERE user_id=? AND quest_id=? AND difficulty=?
		AND status IN ('prepared','ap_paid','lost','continued')`,
		stamp, sess.UserID, sess.QuestID, sess.Difficulty); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO cn35_battle_sessions
		(battle_id,user_id,quest_id,area_id,difficulty,party_id,status,ap_cost,
		 rewards_json,exp,next_stage_id,next_area_id,next_quest_id,continuable,
		 created_at,updated_at,config_version)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.BattleID, sess.UserID, sess.QuestID, sess.AreaID, sess.Difficulty,
		sess.PartyID, cn35game.BattlePrepared, sess.APCost, string(rewardsJSON),
		sess.Exp, sess.NextStageID, sess.NextAreaID, sess.NextQuestID,
		boolInt(sess.Continuable), stamp, stamp, sess.ConfigVersion)
	return err
}

// BattleSession 见 cn35game.BattleStore。
func (s *Store) BattleSession(userID, battleID string) (*cn35game.BattleSession, error) {
	var out *cn35game.BattleSession
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		out, err = battleSessionTx(tx, userID, battleID)
		return err
	})
	return out, err
}

// PayBattleAP 见 cn35game.BattleStore。
func (s *Store) PayBattleAP(userID, battleID string) (*cn35game.BattleSession, error) {
	var sess *cn35game.BattleSession
	insufficient := false
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		sess, err = battleSessionTx(tx, userID, battleID)
		if err != nil {
			return err
		}
		if sess.Status != cn35game.BattlePrepared {
			return nil // 已付/终态：幂等空转，客户端门禁 status=success
		}
		charged, err := s.debitAPTx(tx, userID, sess.APCost, s.clockNowUTC())
		if err != nil {
			return err
		}
		if !charged {
			insufficient = true
			return nil
		}
		res, err := tx.Exec(`UPDATE cn35_battle_sessions
			SET status='ap_paid',updated_at=? WHERE battle_id=? AND status='prepared'`,
			nowUTC(), battleID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("%w: 会话状态迁移竞争 bid=%s", cn35game.ErrBattleState, battleID)
		}
		sess.Status = cn35game.BattleAPPaid
		return nil
	})
	if err != nil {
		return nil, err
	}
	if insufficient {
		return nil, cn35game.ErrBattleAP
	}
	return sess, nil
}

// debitAPTx 先在扣点事务中补算，再用条件更新避免 AP 透支。
// false,nil 表示恢复已提交但可用 AP 仍不足，调用方应提交事务并返回 ErrBattleAP。
func (s *Store) debitAPTx(tx *sql.Tx, userID string, cost int, now time.Time) (bool, error) {
	ap, _, _, err := s.recoverAPTx(tx, userID, now)
	if err == sql.ErrNoRows {
		return false, fmt.Errorf("%w: 账号 %s 无档案", cn35game.ErrBattleNotFound, userID)
	}
	if err != nil {
		return false, err
	}
	if cost <= 0 {
		return true, nil
	}
	if ap < cost {
		return false, nil
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	res, err := tx.Exec(`UPDATE cn35_player_profiles
		SET ap=ap-?,ap_updated_at=?,updated_at=? WHERE user_id=? AND ap>=?`,
		cost, stamp, stamp, userID, cost)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	return true, nil
}

// SettleBattle 见 cn35game.BattleStore。
func (s *Store) SettleBattle(userID, battleID string, rep cn35game.BattleReport) (*cn35game.BattleSettleResult, error) {
	res := &cn35game.BattleSettleResult{}
	insufficient := false
	err := s.withTx(func(tx *sql.Tx) error {
		sess, err := battleSessionTx(tx, userID, battleID)
		if err != nil {
			return err
		}
		sess.ReportTime, sess.HasReportTime = rep.Time, true
		sess.RestMC, sess.HasRestMC = rep.RestMC, true
		switch sess.Status {
		case cn35game.BattleCleared, cn35game.BattleLost, cn35game.BattleAbandoned:
			res.Already = true
			res.Session = sess
			return nil
		case cn35game.BattlePrepared:
			// 跳过 ap_use 的异常顺序：在结算同事务补扣（保证 AP 恰好扣一次）。
			charged, err := s.debitAPTx(tx, userID, sess.APCost, s.clockNowUTC())
			if err != nil {
				return err
			}
			if !charged {
				insufficient = true
				return nil
			}
			sess.Status = cn35game.BattleAPPaid
		}
		if sess.Status != cn35game.BattleAPPaid && sess.Status != cn35game.BattleContinued {
			return fmt.Errorf("%w: bid=%s status=%s", cn35game.ErrBattleState, battleID, sess.Status)
		}
		// 1) 会话迁移 + 战报回填（条件 UPDATE 双保险）。
		dmg, _ := json.Marshal(rep.Damage)
		up, err := tx.Exec(`UPDATE cn35_battle_sessions
			SET status='cleared',report_time=?,rest_mc=?,damage_json=?,settled_at=?,updated_at=?
			WHERE battle_id=? AND status IN ('ap_paid','continued','prepared')`,
			rep.Time, rep.RestMC, string(dmg), nowUTC(), nowUTC(), battleID)
		if err != nil {
			return err
		}
		if n, _ := up.RowsAffected(); n != 1 {
			res.Already = true
			res.Session = sess
			return nil
		}
		sess.Status = cn35game.BattleCleared
		sess.SettledAt = nowUTC()
		// 2) 通关计数 +1（首通标记由结算后计数==1 判定）。
		clearCount, err := incrementClearTx(tx, userID, sess.QuestID, sess.Difficulty)
		if err != nil {
			return err
		}
		res.ClearCount, res.FirstClear = clearCount, clearCount == 1
		// 3) 剧情进度推进：仅普通剧情会话参与；资源副本 ConfigVersion>0
		// 只记录通关计数/奖励台账，不触碰剧情前沿，避免 900xx 命名空间污染。
		if sess.ConfigVersion == 0 {
			if _, err = tx.Exec(`UPDATE cn35_quest_progress
				SET stage_id=?,area_id=?,quest_id=?,updated_at=?
				WHERE user_id=? AND quest_id=?`,
				sess.NextStageID, sess.NextAreaID, sess.NextQuestID, nowUTC(),
				userID, sess.QuestID); err != nil {
				return err
			}
		}
		// 4) 奖励入账（execute 快照；key 白名单持久化，其余仅回显）。
		for _, r := range sess.Rewards {
			switch r.Key {
			case "gacha_point", "guild_asset":
				if err := creditBalanceTx(tx, userID, r.Key, int64(r.Count)); err != nil {
					return err
				}
			case "item":
				// ITEM350-06：item_key 在开战快照中固化，按绑定版本入账，
				// 不随开战后目录停用而失效；旧快照（无 key）退回
				// content_id 实时解析（停用/缺失跳过，数据库错误回滚）。
				key := r.ItemKey
				if key == "" {
					var kerr error
					key, kerr = itemKeyByContentTx(tx, r.ID)
					switch {
					case errors.Is(kerr, cn35game.ErrItemNotFound):
						continue
					case kerr != nil:
						return kerr
					}
				}
				if err := creditItemTx(tx, userID, key, r.Count); err != nil {
					return err
				}
			case "seed":
				for i := 0; i < r.Count; i++ {
					uuid := "mon" + randHex(16)
					// 掉落体型随机（原作口径：强化仅能推进至 1.70，
					// 掉落可越界且越界个体天然不可强化）。区间/分布
					// 为 cn35game.MonsterDropScaleM 占位值（PROPOSED）。
					scale := cn35game.RollMonsterDropScale()
					if err := InsertMonsterTx(tx, userID, cn35game.MonsterRecord{
						UUID: uuid, MonsterID: r.ID, Level: 1,
						Scale: &scale, RealScale: &scale,
					}); err != nil {
						return err
					}
					res.MonsterUUIDs = append(res.MonsterUUIDs, uuid)
				}
			case "skill":
				// RUNE350-03：符文本符文入账——开战快照冻结
				// skill_id+魔力品质，目录外 id（数据异常）跳过不阻塞结算。
				if _, ok := cn35game.SkillCatalog[r.ID]; !ok {
					continue
				}
				for i := 0; i < r.Count; i++ {
					if _, err := grantSkillTx(tx, userID, r.ID, r.Level); err != nil {
						return err
					}
				}
			}
		}
		res.Rewards = append([]cn35game.BattleReward(nil), sess.Rewards...)
		// 4b) 资源副本结算台账（ITEM350-06）：仅 ConfigVersion>0 的资源会话
		// 入账；quest_key/request_json 均来自开战绑定的不可变版本快照。
		if sess.ConfigVersion > 0 {
			questKey, err := resourceQuestKeyForVersionTx(tx, sess.ConfigVersion, sess.QuestID)
			if err != nil {
				return err
			}
			rj, _ := json.Marshal(sess.Rewards)
			reqJSON, _ := json.Marshal(map[string]any{
				"quest_id": sess.QuestID, "config_version": sess.ConfigVersion,
			})
			if _, err := tx.Exec(`INSERT INTO cn35_resource_reward_events
				(user_id,request_key,operation,request_json,quest_key,config_version,
				 result,rewards_json,created_at)
				VALUES (?,?,'battle_clear',?,?,?,'cleared',?,?)
				ON CONFLICT(user_id,request_key) DO NOTHING`,
				userID, battleID, string(reqJSON), questKey, sess.ConfigVersion,
				string(rj), nowUTC()); err != nil {
				return err
			}
		}
		// 5) exp/level：按用户等级经验表升级（cn35game.ApplyUserExp；曲线来源
		// 与证据见 cn35game/user_exp_table.go、2026-10-01 Note）。
		if sess.Exp > 0 {
			if err := addExpTx(tx, userID, sess.Exp); err != nil {
				return err
			}
		}
		res.Session = sess
		return nil
	})
	if err != nil {
		return nil, err
	}
	if insufficient {
		return nil, cn35game.ErrBattleAP
	}
	return res, nil
}

// incrementClearTx 通关计数 +1，返回新值。
func incrementClearTx(tx *sql.Tx, userID string, questID int, difficulty string) (int, error) {
	if _, err := tx.Exec(`INSERT INTO cn35_quest_clears
		(user_id,quest_id,difficulty,clear_count,updated_at) VALUES (?,?,?,1,?)
		ON CONFLICT(user_id,quest_id,difficulty)
		DO UPDATE SET clear_count=clear_count+1,updated_at=excluded.updated_at`,
		userID, questID, difficulty, nowUTC()); err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRow(`SELECT clear_count FROM cn35_quest_clears
		WHERE user_id=? AND quest_id=? AND difficulty=?`, userID, questID, difficulty).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// creditBalanceTx 余额入账（行不存在则建）。
func creditBalanceTx(tx *sql.Tx, userID, currency string, delta int64) error {
	if delta <= 0 {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO cn35_player_balances (user_id,currency,amount,updated_at)
		VALUES (?,?,?,?)
		ON CONFLICT(user_id,currency) DO UPDATE SET amount=amount+excluded.amount,updated_at=excluded.updated_at`,
		userID, currency, delta, nowUTC())
	return err
}

// addExpTx 经验入账与升级：按用户等级经验表逐级扣减（cn35game.ApplyUserExp，
// 取代 story350-07「max_exp 恒 100 顺延」临时口径）；同事务维护
// max_exp=新等级需求（满级保持末档）。值无变化不写行。
func addExpTx(tx *sql.Tx, userID string, gain int) error {
	var level, exp, ap int
	if err := tx.QueryRow(`SELECT level,exp,ap FROM cn35_player_profiles WHERE user_id=?`,
		userID).Scan(&level, &exp, &ap); err != nil {
		return err
	}
	newLevel, newExp := cn35game.ApplyUserExp(level, exp, gain)
	if newLevel == level && newExp == exp {
		return nil
	}
	now := nowUTC()
	if newLevel > level {
		// 升级：max_ap=level+19。AP 按用户 2026-10-20 更正口径**叠加增量**
		// （旧口径「直接补满」作废）：newAP = 旧AP + (新等级上限-旧等级上限)。
		// 触顶时按 recoverAPTx 同口径重置恢复锚点，否则保留原锚点继续恢复。
		oldMax := cn35game.UserMaxAP(level)
		newMax := cn35game.UserMaxAP(newLevel)
		newAP := ap + (newMax - oldMax)
		if newAP < 0 {
			newAP = 0
		}
		if newAP > newMax {
			newAP = newMax
		}
		if newAP >= newMax {
			_, err := tx.Exec(`UPDATE cn35_player_profiles
				SET level=?,exp=?,max_exp=?,max_ap=?,ap=?,ap_updated_at=?,updated_at=?
				WHERE user_id=?`,
				newLevel, newExp, cn35game.UserNeedExpCapped(newLevel),
				newMax, newAP, now, now, userID)
			return err
		}
		_, err := tx.Exec(`UPDATE cn35_player_profiles
			SET level=?,exp=?,max_exp=?,max_ap=?,ap=?,updated_at=?
			WHERE user_id=?`,
			newLevel, newExp, cn35game.UserNeedExpCapped(newLevel),
			newMax, newAP, now, userID)
		return err
	}
	_, err := tx.Exec(`UPDATE cn35_player_profiles SET level=?,exp=?,max_exp=?,updated_at=? WHERE user_id=?`,
		newLevel, newExp, cn35game.UserNeedExpCapped(newLevel), now, userID)
	return err
}

// LoseBattle 见 cn35game.BattleStore。
func (s *Store) LoseBattle(userID, battleID string, rep cn35game.BattleReport) (*cn35game.BattleSession, error) {
	var sess *cn35game.BattleSession
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		sess, err = battleSessionTx(tx, userID, battleID)
		if err != nil {
			return err
		}
		switch sess.Status {
		case cn35game.BattlePrepared, cn35game.BattleAPPaid, cn35game.BattleContinued:
		default:
			return nil // 终态幂等
		}
		// escaped=false 是客户端 Escape() 主动放弃路径 → 本会话不可续
		// （Lose() 败北才带 escaped=true，字段方向存疑——按现象实现）。
		continuable := sess.Continuable && rep.Escaped
		dmg, _ := json.Marshal(rep.Damage)
		if _, err := tx.Exec(`UPDATE cn35_battle_sessions
			SET status='lost',escaped=?,continuable=?,report_time=?,rest_mc=?,damage_json=?,updated_at=?
			WHERE battle_id=?`,
			boolInt(rep.Escaped), boolInt(continuable), rep.Time, rep.RestMC,
			string(dmg), nowUTC(), battleID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cn35_player_flags (user_id,quest_failed,updated_at)
			VALUES (?,1,?)
			ON CONFLICT(user_id) DO UPDATE SET quest_failed=1,updated_at=excluded.updated_at`,
			userID, nowUTC()); err != nil {
			return err
		}
		sess.Status = cn35game.BattleLost
		sess.Escaped = rep.Escaped
		sess.Continuable = continuable
		return nil
	})
	return sess, err
}

// ContinueBattle 见 cn35game.BattleStore。续战固定消耗 1 钻（客户端埋点
// cost=1/revenue_cost=1/currency=diamond，SWFQuestLose.cs Continue()）。
func (s *Store) ContinueBattle(userID string) (*cn35game.BattleSession, int64, error) {
	var sess *cn35game.BattleSession
	var coin int64
	err := s.withTx(func(tx *sql.Tx) error {
		row := tx.QueryRow(`SELECT `+battleScanCols+` FROM cn35_battle_sessions
			WHERE user_id=? AND status='lost' AND continuable=1
			ORDER BY updated_at DESC, created_at DESC LIMIT 1`, userID)
		var err error
		sess, err = scanBattle(row)
		if err == sql.ErrNoRows {
			coin = coinBalanceTx(tx, userID)
			return cn35game.ErrNoContinuable
		}
		if err != nil {
			return err
		}
		up, err := tx.Exec(`UPDATE cn35_player_balances
			SET amount=amount-1,updated_at=? WHERE user_id=? AND currency='coin' AND amount>=1`,
			nowUTC(), userID)
		if err != nil {
			return err
		}
		if n, _ := up.RowsAffected(); n != 1 {
			coin = coinBalanceTx(tx, userID)
			return cn35game.ErrBattleCoin
		}
		if _, err := tx.Exec(`UPDATE cn35_battle_sessions
			SET status='continued',continue_count=continue_count+1,updated_at=?
			WHERE battle_id=? AND status='lost'`,
			nowUTC(), sess.BattleID); err != nil {
			return err
		}
		sess.Status = cn35game.BattleContinued
		sess.ContinueCount++
		coin = coinBalanceTx(tx, userID)
		return nil
	})
	return sess, coin, err
}

func coinBalanceTx(tx *sql.Tx, userID string) int64 {
	var n int64
	_ = tx.QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency='coin'`, userID).Scan(&n)
	return n
}

// LogBattle 见 cn35game.BattleStore。
func (s *Store) LogBattle(userID, battleID string, questID int, record string) error {
	return s.withTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO cn35_battle_logs
			(user_id,battle_id,quest_id,record_json,created_at) VALUES (?,?,?,?,?)`,
			userID, battleID, questID, record, nowUTC())
		return err
	})
}
