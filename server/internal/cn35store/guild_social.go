// guild_social.go GUILDCLOSE350 GC-07/GC-08：公会社交面持久化（迁移 0040）。
//
// 三条真源：
//  1. 成员原路径 /guilds/users/ 的行补充数据（最后活动时间、公会贡献点数）；
//  2. 等待邀请：本人登记队列（cn35_guild_waiting_users）+ 单机扩展 NPC 候补池
//     （cn35_guild_waiting_candidates）+ 邀请动作（复用 cn35_guild_subscriptions
//     的 kind='invite' pending 行，接口 0028 部分唯一索引已就绪）；
//  3. 会战留言（cn35_battle_comments）。
//
// 事务纪律（docs/后端数据库与Go工作流.md）：
//   - 每个写面在 withTx 内完成「幂等键登记 + 前置校验 + 写入」；
//   - 幂等键复用 recordGuildOpTx（同键同参=重放成功、同键异参显式拒绝）；
//   - 邀请的权限/容量/重复/过期判定全在事务内，不靠 handler 兜底。
//
// 与其它 taker 的边界：不写 cn35_guilds/cn35_guild_members 的既有列语义；
// NPC 候补只有「被邀请入会」时才落到 cn35_npc_identities +
// cn35_guild_npc_members（既有 NPC 身份域），候补阶段只在本表。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// 编译期断言：*Store 满足社交面接口。
var _ cn35game.GuildSocialStore = (*Store)(nil)

// 单机扩展：NPC 待邀请候补 id 命名空间（与 guild_solo.go 的 npcNNNNAA 不冲突）。
const (
	waitingCandidatePrefix = "npcc"
	// battleCommentDedupeWindow 同人同文短时去重窗口（双击/网络重试安全）〔推断〕。
	battleCommentDedupeWindow = 10 * time.Second
)

// normalizePlayType 归一化公会方针 id：""（未指定）/0..4 合法。
func normalizePlayType(id string) (string, error) {
	id = strings.TrimSpace(id)
	switch id {
	case "", "0", "1", "2", "3", "4":
		return id, nil
	}
	return "", cn35game.ErrGuildSocialPlayType
}

// parseStoreTime 解析库内时间列（统一 nowUTC() 的 RFC3339Nano）。
//
// 注意：不能用字符串比较判断先后——RFC3339Nano 会省略小数末尾的 0，
// "…:24Z" 与 "…:24.5Z" 的字典序与时间序相反。过期判定一律解析后比较。
func parseStoreTime(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// storeTimeUnix 时间列 → Unix 秒（解析失败为 0）。
func storeTimeUnix(raw string) int64 {
	if t, ok := parseStoreTime(raw); ok {
		return t.Unix()
	}
	return 0
}

// playTypeFilter 解析 play_type_id 过滤串（客户端筛选器下发 "0,1,2,3,4"）。
// 空串或含全部取值 → nil（不过滤）。
func playTypeFilter(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out[part] = true
	}
	if len(out) == 0 || len(out) >= len(guildPlayTypeIDSet()) {
		return nil
	}
	return out
}

// guildPlayTypeIDSet 选项取值集合（与 cn35game.guildPlayTypeNames 同域）。
func guildPlayTypeIDSet() map[string]bool {
	return map[string]bool{"0": true, "1": true, "2": true, "3": true, "4": true}
}

// socialUserName 账号显示名（user_name 优先，其次 account_name，最后 user_id）。
func socialUserNameTx(tx *sql.Tx, userID string) string {
	var name string
	_ = tx.QueryRow(`SELECT COALESCE(NULLIF(user_name,''),NULLIF(account_name,''),user_id)
		FROM cn35_accounts WHERE user_id=?`, userID).Scan(&name)
	if name == "" {
		name = userID
	}
	return name
}

// ---- 成员原路径补充数据 ----

// GuildMemberExtras /guilds/users/ 行补充：成员最后活动时间 + 公会贡献点数。
//
// loggedAt：真人成员取 cn35_player_profiles.updated_at（本工程唯一的
// 「最后写入时间」来源，作为最后活动时间〔推断〕；NPC 成员取入会时间）。
// point：cn35_gw_member_scores 在该公会全部场次的 gp_earned 累计〔推断：
// 原服「攻击公会点数/公会内贡献排行」的统计周期未取证〕。
func (s *Store) GuildMemberExtras(guildID string) (map[string]int64, map[string]int64, error) {
	loggedAt := map[string]int64{}
	point := map[string]int64{}
	err := s.withTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT m.user_id, COALESCE(p.updated_at,''), m.joined_at
			FROM cn35_guild_members m
			LEFT JOIN cn35_player_profiles p ON p.user_id=m.user_id
			WHERE m.guild_id=?`, guildID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var uid, updated, joined string
			if err = rows.Scan(&uid, &updated, &joined); err != nil {
				rows.Close()
				return err
			}
			at := storeTimeUnix(updated)
			if at == 0 {
				at = storeTimeUnix(joined)
			}
			loggedAt[uid] = at
		}
		if err = rows.Close(); err != nil {
			return err
		}
		npcRows, err := tx.Query(`SELECT npc_id, joined_at FROM cn35_guild_npc_members
			WHERE guild_id=?`, guildID)
		if err != nil {
			return err
		}
		for npcRows.Next() {
			var npcID, joined string
			if err = npcRows.Scan(&npcID, &joined); err != nil {
				npcRows.Close()
				return err
			}
			loggedAt[npcID] = storeTimeUnix(joined)
		}
		if err = npcRows.Close(); err != nil {
			return err
		}
		ptRows, err := tx.Query(`SELECT user_id, SUM(gp_earned) FROM cn35_gw_member_scores
			WHERE guild_id=? GROUP BY user_id`, guildID)
		if err != nil {
			return err
		}
		for ptRows.Next() {
			var uid string
			var gp int64
			if err = ptRows.Scan(&uid, &gp); err != nil {
				ptRows.Close()
				return err
			}
			point[uid] = gp
		}
		return ptRows.Close()
	})
	return loggedAt, point, err
}

// ---- 等待邀请：本人登记 ----

// WaitingAdd 本人登记/更新「等待邀请」（客户端 /waiting_users/add.json）。
//
// 规则：已入会账号不得登记（ErrGuildJoined）；重复登记=更新期望方针。
// 幂等键缺省为 "waiting_add:<play_type_id>"——同参重试命中重放，改方针是
// 不同键的新请求（不会被「同键异参」误拒）。
func (s *Store) WaitingAdd(userID, playTypeID, opKey string) error {
	pt, err := normalizePlayType(playTypeID)
	if err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error {
		// 状态校验先于幂等键登记：登记后入会的账号再来登记必须被拒，不能被
		// 「同参重放 → 成功」短路（否则等于给已入会账号补一个成功回执）。
		guildID, _, err := memberRoleTx(tx, userID)
		if err != nil {
			return err
		}
		if guildID != "" {
			return cn35game.ErrGuildJoined
		}
		if err := recordGuildOpTx(tx, userID,
			opKeyOr(opKey, "waiting_add:"+pt), "waiting_add", pt); err != nil &&
			!errors.Is(err, errOpReplay) {
			return err
		}
		now := nowUTC()
		_, err = tx.Exec(`INSERT INTO cn35_guild_waiting_users
			(user_id,play_type_id,created_at,updated_at) VALUES (?,?,?,?)
			ON CONFLICT(user_id) DO UPDATE SET
				play_type_id=excluded.play_type_id, updated_at=excluded.updated_at`,
			userID, pt, now, now)
		return err
	})
}

// WaitingState 本人是否仍在等待邀请队列（user.is_waiting）。
func (s *Store) WaitingState(userID string) (bool, error) {
	var waiting bool
	err := s.withTx(func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_waiting_users
			WHERE user_id=?`, userID).Scan(&n); err != nil {
			if isMissingTable(err) {
				return nil
			}
			return err
		}
		waiting = n > 0
		return nil
	})
	return waiting, err
}

// waitingPoolSettingsTx 读单机「NPC 待邀请候补池」独立配置（cn35_guild_social_config）。
//
// 表不存在（未升级的旧库）时按默认值处理，读面不因此报错。
func waitingPoolSettingsTx(tx *sql.Tx) (cn35game.GuildWaitingPoolSettings, error) {
	out := cn35game.DefaultGuildWaitingPoolSettings
	var enabled, max int
	err := tx.QueryRow(`SELECT waiting_pool_enabled,waiting_pool_max
		FROM cn35_guild_social_config WHERE id=1`).Scan(&enabled, &max)
	if errors.Is(err, sql.ErrNoRows) || isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Enabled = enabled != 0
	out.Max = max
	return cn35game.NormalizeGuildWaitingPoolSettings(out), nil
}

// WaitingPoolSettings 读 NPC 候补池配置（诊断/测试/运营面）。
func (s *Store) WaitingPoolSettings() (cn35game.GuildWaitingPoolSettings, error) {
	var out cn35game.GuildWaitingPoolSettings
	err := s.withTx(func(tx *sql.Tx) error {
		cfg, err := waitingPoolSettingsTx(tx)
		out = cfg
		return err
	})
	return out, err
}

// ConfigureWaitingPool 落库 NPC 候补池配置（独立命名配置，不动 solo 总开关）。
func (s *Store) ConfigureWaitingPool(cfg cn35game.GuildWaitingPoolSettings) error {
	cfg = cn35game.NormalizeGuildWaitingPoolSettings(cfg)
	enabled := 0
	if cfg.Enabled {
		enabled = 1
	}
	return s.withTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO cn35_guild_social_config
			(id,waiting_pool_enabled,waiting_pool_max,updated_at)
			VALUES (1,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
				waiting_pool_enabled=excluded.waiting_pool_enabled,
				waiting_pool_max=excluded.waiting_pool_max,
				updated_at=excluded.updated_at`,
			enabled, cfg.Max, nowUTC())
		return err
	})
}

// ensureWaitingCandidatesTx 单机域 NPC 候补有界补池（幂等）。
//
// 生效条件 = solo 域开关 AND 候补池独立配置 enabled：任一为关即**不注入**
// （WaitingList 也不会读既有候补行，保证「关掉就是原服语义：只有真人队列」）。
// 等级/头像/昵称是重建初值，不是原服数据（复用 guild_solo.go 的昵称/头像池，
// 避免第二套素材清单）。
func (s *Store) ensureWaitingCandidatesTx(tx *sql.Tx) (bool, error) {
	solo, _, err := soloStateTx(tx)
	if err != nil {
		return false, err
	}
	pool, err := waitingPoolSettingsTx(tx)
	if err != nil {
		return false, err
	}
	if !solo.Enabled || !pool.Enabled || pool.Max == 0 {
		return false, nil
	}
	var available, total int
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_waiting_candidates c
		WHERE NOT EXISTS (SELECT 1 FROM cn35_guild_npc_members n WHERE n.npc_id=c.npc_id)`,
	).Scan(&available); err != nil {
		return false, err
	}
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_waiting_candidates`).Scan(&total); err != nil {
		return false, err
	}
	now := nowUTC()
	for i := available; i < pool.Max; i++ {
		seq := total + (i - available) + 1
		npcID := fmt.Sprintf("%s%04d", waitingCandidatePrefix, seq)
		name := guildSoloNPCNames[(seq*7+i*11)%len(guildSoloNPCNames)]
		level := int64(8 + (seq*13+i*17)%40)
		unitID := fmt.Sprintf("%d", guildSoloNPCUnits[(seq*3+i*7)%len(guildSoloNPCUnits)])
		playType := guildSoloPlayTypes[(seq+i)%len(guildSoloPlayTypes)]
		if _, err := tx.Exec(`INSERT INTO cn35_guild_waiting_candidates
			(npc_id,name,level,leader_unit_id,play_type_id,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?) ON CONFLICT(npc_id) DO NOTHING`,
			npcID, name, level, unitID, playType, now, now); err != nil {
			return false, err
		}
	}
	return true, nil
}

// realWaitingRow 真人登记行 + 过期判定用的 updated_at 原文。
type realWaitingRow struct {
	u         cn35game.WaitingUser
	updatedAt string
}

// WaitingList 待邀请候补列表 = 真人登记 ∪ 单机 NPC 候补。
//
// 过滤：排除本人、排除已入会（真人成员表/NPC 成员表）、排除超过 ttl 的登记；
// play_type_filter 支持逗号集合（客户端筛选器）。排序：登记/建池时间升序
// （页面 reverse=true，最新在最上）。
func (s *Store) WaitingList(viewerID, playTypeFilterRaw string, ttl time.Duration,
	now time.Time, includeReal bool) ([]cn35game.WaitingUser, error) {
	filter := playTypeFilter(playTypeFilterRaw)
	cutoff := now.Add(-ttl)
	out := []cn35game.WaitingUser{}
	err := s.withTx(func(tx *sql.Tx) error {
		injected, err := s.ensureWaitingCandidatesTx(tx)
		if err != nil {
			return err
		}
		// 真人登记行：未入会 viewer 一律不查（隐私收敛，见 wire 注释）。
		regs := []realWaitingRow{}
		if includeReal {
			rows, qerr := tx.Query(`SELECT w.user_id,
				COALESCE(NULLIF(a.user_name,''),NULLIF(a.account_name,''),w.user_id),
				COALESCE(p.level,1), COALESCE(p.leader_unit_id,''), w.play_type_id,
				COALESCE(p.updated_at,''), w.created_at, w.updated_at
				FROM cn35_guild_waiting_users w
				JOIN cn35_accounts a ON a.user_id=w.user_id
				LEFT JOIN cn35_player_profiles p ON p.user_id=w.user_id
				WHERE w.user_id<>?
				  AND NOT EXISTS (SELECT 1 FROM cn35_guild_members m WHERE m.user_id=w.user_id)
				ORDER BY w.created_at`, viewerID)
			if qerr != nil {
				return qerr
			}
			for rows.Next() {
				var r realWaitingRow
				var level int64
				var loggedAt, created, updated string
				if qerr = rows.Scan(&r.u.UserID, &r.u.Name, &level, &r.u.LeaderUnitID,
					&r.u.PlayTypeID, &loggedAt, &created, &updated); qerr != nil {
					rows.Close()
					return qerr
				}
				r.u.Level = level
				r.u.CreatedAt = storeTimeUnix(created)
				r.u.LoggedAt = storeTimeUnix(loggedAt)
				if r.u.LoggedAt == 0 {
					r.u.LoggedAt = r.u.CreatedAt
				}
				r.updatedAt = updated
				regs = append(regs, r)
			}
			if qerr = rows.Close(); qerr != nil {
				return qerr
			}
		}
		for _, r := range regs {
			// 过期：登记后长期未再刷新（ttl）→ 不进入可邀请列表。
			if t, ok := parseStoreTime(r.updatedAt); ok && t.Before(cutoff) {
				continue
			}
			if filter != nil && !filter[r.u.PlayTypeID] {
				continue
			}
			out = append(out, r.u)
		}
		if !injected {
			// 配置关闭：只返回真人登记队列（原服语义），不读既有候补行。
			return nil
		}
		npcRows, err := tx.Query(`SELECT c.npc_id,c.name,c.level,c.leader_unit_id,
			c.play_type_id,c.created_at
			FROM cn35_guild_waiting_candidates c
			WHERE NOT EXISTS (SELECT 1 FROM cn35_guild_npc_members n WHERE n.npc_id=c.npc_id)
			ORDER BY c.created_at`)
		if err != nil {
			return err
		}
		defer npcRows.Close()
		for npcRows.Next() {
			var u cn35game.WaitingUser
			var level int64
			var created string
			if err = npcRows.Scan(&u.UserID, &u.Name, &level, &u.LeaderUnitID,
				&u.PlayTypeID, &created); err != nil {
				return err
			}
			if filter != nil && !filter[u.PlayTypeID] {
				continue
			}
			u.IsNPC = true
			u.Level = level
			u.CreatedAt = storeTimeUnix(created)
			u.LoggedAt = u.CreatedAt // NPC 候补无活动模型，用建池时间
			out = append(out, u)
		}
		return npcRows.Err()
	})
	return out, err
}

// InviteUser 邀请候补入会。
//
// 规则（事务内顺序判定，全部显式报错，不静默成功）：
//  1. 邀请者必须已入会且职位 ≥ 副会长（客户端 GuildTop.cs:485-494
//     UpdateDisplayForGuildRole 对 Role.User 禁用邀请/搜索按钮，[推断]服务端同规则）；
//  2. 不能邀请自己；
//  3. 目标已入会（真人/NPC 任一成员表）→ ErrGuildJoined；
//  4. 公会满员（真人+NPC 席位）→ ErrGuildFull；
//  5. 真人目标：必须存在账号；已有 pending invite → 幂等成功返回同一 sub_id；
//  6. NPC 候补：无「接受邀请」能力，邀请即入会〔单机扩展〕，同事务写
//     cn35_npc_identities + cn35_guild_npc_members；
//  7. 目标既非账号也非候补 → ErrGuildSocialUser。
func (s *Store) InviteUser(actorID, targetUserID, opKey string, now time.Time) (string, error) {
	targetUserID = strings.TrimSpace(targetUserID)
	if targetUserID == "" {
		return "", cn35game.ErrGuildSocialUser
	}
	if targetUserID == actorID {
		return "", cn35game.ErrGuildSocialSelf
	}
	subID := ""
	err := s.withTx(func(tx *sql.Tx) error {
		guildID, role, err := memberRoleTx(tx, actorID)
		if err != nil {
			return err
		}
		if guildID == "" {
			return cn35game.ErrGuildNoMember
		}
		if role < cn35game.GuildRoleSubMaster {
			return cn35game.ErrGuildNoPerm
		}
		payload := guildID + ":" + targetUserID
		replay := false
		if err := recordGuildOpTx(tx, actorID,
			opKeyOr(opKey, "invite:"+payload), "invite_user", payload); err != nil {
			if !errors.Is(err, errOpReplay) {
				return err
			}
			replay = true // 同参重放：继续按当前状态判定（可能已入会/已满员）
		}
		if replay {
			if sub, ok, err := pendingInviteTx(tx, guildID, targetUserID); err != nil {
				return err
			} else if ok {
				subID = sub
				return nil
			}
		}
		// 目标已是成员（真人或 NPC）→ 已入会。
		if n, err := memberCountByUserTx(tx, targetUserID); err != nil {
			return err
		} else if n > 0 {
			return cn35game.ErrGuildJoined
		}
		count, err := guildMemberCountTx(tx, guildID)
		if err != nil {
			return err
		}
		if count >= cn35game.GuildRules.MaxMembers {
			return cn35game.ErrGuildFull
		}
		// 真人目标：建 pending invite（被邀者在 /waiting_users/recruit 看到公会行，
		// 走原服 join_approve_url 接受）。
		var exists int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_accounts WHERE user_id=?`,
			targetUserID).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			if sub, ok, err := pendingInviteTx(tx, guildID, targetUserID); err != nil {
				return err
			} else if ok {
				subID = sub
				return nil
			}
			subID = newGuildSubID()
			_, err = tx.Exec(`INSERT INTO cn35_guild_subscriptions
				(sub_id,guild_id,user_id,kind,status,created_at)
				VALUES (?,?,?,'invite','pending',?)`,
				subID, guildID, targetUserID, nowUTC())
			return err
		}
		// NPC 候补：邀请即入会（单机扩展）。
		var name, leader, playType, created string
		var level int64
		err = tx.QueryRow(`SELECT name,level,leader_unit_id,play_type_id,created_at
			FROM cn35_guild_waiting_candidates WHERE npc_id=?`, targetUserID).Scan(
			&name, &level, &leader, &playType, &created)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrGuildSocialUser
		}
		if err != nil {
			return err
		}
		stamp := nowUTC()
		if _, err := tx.Exec(`INSERT INTO cn35_npc_identities
			(npc_id,name,level,leader_unit_id,detail_message,created_at)
			VALUES (?,?,?,?,?,?) ON CONFLICT(npc_id) DO NOTHING`,
			targetUserID, name, level, leader, "", created); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cn35_guild_npc_members
			(guild_id,npc_id,role,joined_at) VALUES (?,?,1,?)
			ON CONFLICT(guild_id,npc_id) DO NOTHING`,
			guildID, targetUserID, stamp); err != nil {
			return err
		}
		// 入会即离开等待队列。
		if _, err := tx.Exec(`DELETE FROM cn35_guild_waiting_users WHERE user_id=?`,
			targetUserID); err != nil {
			return err
		}
		return nil
	})
	return subID, err
}

// memberCountByUserTx 该 id（真人或 NPC）是否已是任何公会的成员。
func memberCountByUserTx(tx *sql.Tx, id string) (int, error) {
	var real, npc int
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_members WHERE user_id=?`,
		id).Scan(&real); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_npc_members WHERE npc_id=?`,
		id).Scan(&npc); err != nil {
		return 0, err
	}
	return real + npc, nil
}

// pendingInviteTx 查 (guild,target) 的 pending 邀请。
func pendingInviteTx(tx *sql.Tx, guildID, target string) (string, bool, error) {
	var sub string
	err := tx.QueryRow(`SELECT sub_id FROM cn35_guild_subscriptions
		WHERE guild_id=? AND user_id=? AND kind='invite' AND status='pending'`,
		guildID, target).Scan(&sub)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return sub, true, nil
}

// InvitedGuildIDs 本人收到的、仍未过期的公会邀请所属公会。
//
// 同步收敛（同事务）：本人已入会 → 其余 pending invite 全部 cancelled；
// 邀请已过 ttl / 公会已解散 / 公会已满员 → 该条 cancelled 且不下发
// （页面上留着点不动的「申请」按钮比不显示更糟）。
func (s *Store) InvitedGuildIDs(userID string, ttl time.Duration, now time.Time) ([]string, error) {
	cutoff := now.Add(-ttl)
	out := []string{}
	err := s.withTx(func(tx *sql.Tx) error {
		guildID, _, err := memberRoleTx(tx, userID)
		if err != nil {
			return err
		}
		if guildID != "" {
			_, err = tx.Exec(`UPDATE cn35_guild_subscriptions
				SET status='cancelled',decided_at=?
				WHERE user_id=? AND kind='invite' AND status='pending'`, nowUTC(), userID)
			return err
		}
		rows, err := tx.Query(`SELECT s.sub_id,s.guild_id,s.created_at,g.guild_id
			FROM cn35_guild_subscriptions s
			LEFT JOIN cn35_guilds g ON g.guild_id=s.guild_id
			WHERE s.user_id=? AND s.kind='invite' AND s.status='pending'
			ORDER BY s.created_at`, userID)
		if err != nil {
			return err
		}
		type inv struct {
			subID, guildID, created string
			guildExists             bool
		}
		invs := []inv{}
		for rows.Next() {
			var it inv
			var gid sql.NullString
			if err = rows.Scan(&it.subID, &it.guildID, &it.created, &gid); err != nil {
				rows.Close()
				return err
			}
			it.guildExists = gid.Valid && gid.String != ""
			invs = append(invs, it)
		}
		if err = rows.Close(); err != nil {
			return err
		}
		for _, it := range invs {
			expired := false
			if t, ok := parseStoreTime(it.created); ok && t.Before(cutoff) {
				expired = true
			}
			if !it.guildExists || expired {
				if _, err := tx.Exec(`UPDATE cn35_guild_subscriptions
					SET status='cancelled',decided_at=? WHERE sub_id=? AND status='pending'`,
					nowUTC(), it.subID); err != nil {
					return err
				}
				continue
			}
			count, err := guildMemberCountTx(tx, it.guildID)
			if err != nil {
				return err
			}
			if count >= cn35game.GuildRules.MaxMembers {
				if _, err := tx.Exec(`UPDATE cn35_guild_subscriptions
					SET status='cancelled',decided_at=? WHERE sub_id=? AND status='pending'`,
					nowUTC(), it.subID); err != nil {
					return err
				}
				continue
			}
			out = append(out, it.guildID)
		}
		return nil
	})
	return out, err
}

// ---- 会战留言 ----

// BattleCommentFeed 会战留言房间读取（roomID = 会战场次 id）。
//
// 房间口径：原服 `guild.battle_id` 与会战场次同源（guildDocExtra.BattleID），
// 因此房间 = cn35_gw_sessions.session_id，同一场次内全公会共用（客户端聊天室
// 语义即「本场会战的公会频道」）。latest 为该房间最新留言 Unix 秒（无留言为 0），
// rows 按写入顺序升序。
func (s *Store) BattleCommentFeed(roomID string, limit int) (
	int64, []cn35game.BattleComment, error) {
	latest := int64(0)
	out := []cn35game.BattleComment{}
	if roomID == "" {
		return latest, out, nil
	}
	err := s.withTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT c.comment_id,c.battle_id,c.user_id,c.user_name,
			COALESCE(p.leader_unit_id,''), c.body,c.created_at
			FROM cn35_battle_comments c
			LEFT JOIN cn35_player_profiles p ON p.user_id=c.user_id
			WHERE c.battle_id=? ORDER BY c.rowid`, roomID)
		if err != nil {
			if isMissingTable(err) {
				return nil
			}
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var bc cn35game.BattleComment
			var unitID, created string
			if err = rows.Scan(&bc.ID, &bc.BattleID, &bc.UserID, &bc.UserName,
				&unitID, &bc.Body, &created); err != nil {
				return err
			}
			bc.UnitID = socialLeaderUnitIDInt(unitID)
			bc.CreatedAt = storeTimeUnix(created)
			if bc.CreatedAt > latest {
				latest = bc.CreatedAt
			}
			out = append(out, bc)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		// limit 保留最新 N 条（客户端只渲染最后 50 条）。
		if limit > 0 && len(out) > limit {
			out = out[len(out)-limit:]
		}
		return nil
	})
	return latest, out, err
}

// AddBattleComment 写入一条会战留言。
//
// 校验（顺序即错误优先级）：
//  1. 内容 1..110 字符（客户端输入上限 110，BattleMessages.cs:59）→ ErrGuildSocialComment；
//  2. roomID 必须存在且调用者所属公会**参战该场次**（cn35_gw_participants）
//     → ErrGWNoSession；
//  3. 同人同文 10s 内去重（双击/网络重试安全，不是「同键异参」场景——
//     留言没有 op_key）〔推断〕。
func (s *Store) AddBattleComment(userID, roomID, body string, now time.Time) (
	cn35game.BattleComment, error) {
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > cn35game.BattleCommentMaxRunes {
		return cn35game.BattleComment{}, cn35game.ErrGuildSocialComment
	}
	if roomID == "" {
		return cn35game.BattleComment{}, cn35game.ErrGWNoSession
	}
	out := cn35game.BattleComment{}
	err := s.withTx(func(tx *sql.Tx) error {
		gid, _, err := memberRoleTx(tx, userID)
		if err != nil {
			return err
		}
		if gid == "" {
			return cn35game.ErrGuildNoMember
		}
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_gw_participants
			WHERE session_id=? AND guild_id=?`, roomID, gid).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return cn35game.ErrGWNoSession
		}
		name := socialUserNameTx(tx, userID)
		// 短时同文去重。
		var dupID, dupCreated string
		err = tx.QueryRow(`SELECT comment_id,created_at FROM cn35_battle_comments
			WHERE battle_id=? AND user_id=? AND body=? ORDER BY rowid DESC LIMIT 1`,
			roomID, userID, body).Scan(&dupID, &dupCreated)
		if err == nil {
			if t, ok := parseStoreTime(dupCreated); ok && now.Sub(t) < battleCommentDedupeWindow {
				var leader string
				_ = tx.QueryRow(`SELECT COALESCE(leader_unit_id,'') FROM cn35_player_profiles
					WHERE user_id=?`, userID).Scan(&leader)
				out = cn35game.BattleComment{
					ID: dupID, BattleID: roomID, UserID: userID,
					UserName: name, Body: body, CreatedAt: t.Unix(),
					UnitID: socialLeaderUnitIDInt(leader),
				}
				return nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		stamp := now.UTC()
		id := "bc" + randHex(14)
		if _, err := tx.Exec(`INSERT INTO cn35_battle_comments
			(comment_id,battle_id,guild_id,user_id,user_name,body,created_at)
			VALUES (?,?,?,?,?,?,?)`,
			id, roomID, gid, userID, name, body, stamp.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		var leader string
		_ = tx.QueryRow(`SELECT COALESCE(leader_unit_id,'') FROM cn35_player_profiles
			WHERE user_id=?`, userID).Scan(&leader)
		out = cn35game.BattleComment{
			ID: id, BattleID: roomID, UserID: userID, UserName: name,
			Body: body, CreatedAt: stamp.Unix(), UnitID: socialLeaderUnitIDInt(leader),
		}
		return nil
	})
	return out, err
}

// socialLeaderUnitIDInt 头像单位（profiles.leader_unit_id TEXT）→ int64。
func socialLeaderUnitIDInt(raw string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
