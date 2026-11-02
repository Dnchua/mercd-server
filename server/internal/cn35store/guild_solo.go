// guild_solo.go GUILDLIFE350-04/05/06：单机 NPC 公会池（迁移 0037）。
//
// 设计真源：notes/proposed/2026-10-27-cn35-guild-lifecycle-solo.md +
// plans/cn350-guild-lifecycle-solo-2026-10-27/README.md「单机体验规格」。
//
// 边界（红线）：
//   - NPC 是独立身份域（cn35_npc_identities/cn35_guild_npc_members/
//     cn35_guild_npc_defense），不建 cn35_accounts、不写玩家余额/库存/奖励链；
//     真人成员的 user_id 语义保持不动。
//   - 候选池是**有界持久池**：稳定 guild_id/npc_id（gnpcNNNN / npcNNNNAA），
//     加入后席位永久占用，空位不足时才补一批（受 max_guilds 上限约束），
//     不随搜索无限建数据。
//   - 名称/等级/防卫等数值是**重建运营初值**，不是国服原服数据；客户端契约
//     只约束字段形状（docs/协议对接/cn350/公会战-客户端契约取证.md）。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// GuildSoloSettings 单机 NPC 公会域配置（进程配置 → 落库状态）。
//
// 开关关闭后：停止补池与「可加入候选」推荐，但既有 NPC 公会仍可读、可退出，
// 不删除真人数据与历史（设计规格第 6 条）。
type GuildSoloSettings struct {
	Enabled        bool
	CandidateCount int // 期望的「仍有空位」候选公会数
	OpponentCount  int // 期望的持久对手公会数（公会战以敌会身份参战）
	MaxGuilds      int // NPC 公会总量上限（有界补充）
}

// DefaultGuildSoloSettings 重建运营初值：3 个候选（19/20）+ 3 个对手（20/20）。
var DefaultGuildSoloSettings = GuildSoloSettings{
	Enabled: true, CandidateCount: 3, OpponentCount: 3, MaxGuilds: 24,
}

// guildSoloNPCNames NPC 成员昵称池（重建昵称，非原服玩家数据）。
// 与 guildSoloNPCUnits 的步长互素，保证同公会 20 个成员不重名/不重头像。
var guildSoloNPCNames = []string{
	"月见", "柚子茶", "小满", "星屑", "阿岚", "风铃", "青柠", "拾光",
	"南栀", "辰砂", "白露", "远山", "知夏", "若月", "青岚", "晴川",
	"半夏", "疏影", "望舒", "云岫", "白桦", "千夏", "银烛", "秋刀",
	"半糖", "木槿", "空山", "浮舟", "暖阳", "雾岛", "悠人", "朝雾",
	"山茶", "猫町", "川屿", "九十九", "霜月", "灯里", "涡潮", "砂糖",
}

// guildSoloNPCUnits NPC 头像单位（client/decoded 已打包 unit_<id>，
// 且 cards/units.json rare>=3 的可用头像；避免未知素材回退空图标）。
var guildSoloNPCUnits = []int{
	97, 98, 99, 100, 101, 102, 103, 104, 105, 106, 107, 108,
	109, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 120,
}

// guildSoloGuildNames 候选/对手公会名（≤8 字，与客户端建会长度校验同量级）。
var guildSoloGuildNames = []string{
	"月影旅团", "星屑之约", "晨曦小屋", "白鸟庭院", "远山茶会", "夜航船",
	"青空剧団", "砂糖工房", "风见鸡", "海鸣馆", "灯之森", "银线亭",
	"朝雾里", "猫的集会", "云上漫步", "晚钟教会", "木灵之家", "回响之丘",
	"雪解庄", "琥珀町", "风待草", "千年树", "露草舍",
}

var guildSoloPlayTypes = []string{"1", "2", "3"}
var guildSoloRankIDs = []string{"1", "2", "3", "4", "5"}

var guildSoloRecruitTexts = []string{
	"随时欢迎新人，轻松游玩即可。",
	"每晚一起打会战，互相加油！",
	"休闲向公会，只求一起拿奖励。",
	"长期在线，欢迎来坐坐。",
}

// guildSoloDefenseMonsterIDs NPC 防卫魔宠模板池（已证可用的魔宠 id，见
// GuildRules.TrashMonsterIDs 同源；不依赖玩家库存）。
var guildSoloDefenseMonsterIDs = []int{159, 226, 46, 305, 22, 113, 18, 10}

// normalizeGuildSoloSettings 收敛配额到安全区间（防止无限建数据）。
func normalizeGuildSoloSettings(s GuildSoloSettings) GuildSoloSettings {
	if s.CandidateCount < 0 {
		s.CandidateCount = 0
	}
	if s.CandidateCount > 12 {
		s.CandidateCount = 12
	}
	if s.OpponentCount < 0 {
		s.OpponentCount = 0
	}
	if s.OpponentCount > 6 {
		s.OpponentCount = 6
	}
	if s.MaxGuilds < 1 {
		s.MaxGuilds = 1
	}
	if s.MaxGuilds > 60 {
		s.MaxGuilds = 60
	}
	if s.MaxGuilds < s.CandidateCount+s.OpponentCount {
		s.MaxGuilds = s.CandidateCount + s.OpponentCount
	}
	return s
}

// soloStateTx 读单机域状态（含池批次序号）。
func soloStateTx(tx *sql.Tx) (GuildSoloSettings, int, error) {
	var row struct {
		enabled, cand, opp, max, seq int
	}
	err := tx.QueryRow(`SELECT enabled,candidate_count,opponent_count,max_guilds,seq
		FROM cn35_guild_solo_state WHERE id=1`).Scan(
		&row.enabled, &row.cand, &row.opp, &row.max, &row.seq)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultGuildSoloSettings, 0, nil
	}
	if err != nil {
		return GuildSoloSettings{}, 0, err
	}
	return GuildSoloSettings{
		Enabled:        row.enabled != 0,
		CandidateCount: row.cand,
		OpponentCount:  row.opp,
		MaxGuilds:      row.max,
	}, row.seq, nil
}

// GuildSoloState 读当前单机域状态（诊断/管理面用）。
func (s *Store) GuildSoloState() (GuildSoloSettings, error) {
	var out GuildSoloSettings
	err := s.withTx(func(tx *sql.Tx) error {
		cfg, _, err := soloStateTx(tx)
		out = cfg
		return err
	})
	return out, err
}

// ConfigureGuildSolo 落库进程配置并补池（启动时调用；配置是本次运行的权威）。
func (s *Store) ConfigureGuildSolo(cfg GuildSoloSettings) error {
	cfg = normalizeGuildSoloSettings(cfg)
	return s.withTx(func(tx *sql.Tx) error {
		enabled := 0
		if cfg.Enabled {
			enabled = 1
		}
		if _, err := tx.Exec(`UPDATE cn35_guild_solo_state SET
			enabled=?,candidate_count=?,opponent_count=?,max_guilds=?,updated_at=?
			WHERE id=1`, enabled, cfg.CandidateCount, cfg.OpponentCount,
			cfg.MaxGuilds, nowUTC()); err != nil {
			return err
		}
		return ensureGuildSoloPoolTx(tx)
	})
}

// EnsureGuildSoloPool 惰性补池（搜索/入会/会战读面入口兜底调用，幂等）。
func (s *Store) EnsureGuildSoloPool() error {
	return s.withTx(func(tx *sql.Tx) error { return ensureGuildSoloPoolTx(tx) })
}

// guildMemberCountTx 公会在籍人数 = 真人成员 + NPC 成员（容量判定唯一真源）。
func guildMemberCountTx(tx *sql.Tx, guildID string) (int, error) {
	var real, npc int
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_members WHERE guild_id=?`,
		guildID).Scan(&real); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_npc_members WHERE guild_id=?`,
		guildID).Scan(&npc); err != nil {
		return 0, err
	}
	return real + npc, nil
}

// soloGuildCountTx 现有 NPC 公会总数。
func soloGuildCountTx(tx *sql.Tx) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM cn35_guilds WHERE source='npc'`).Scan(&n)
	return n, err
}

// soloVacantCandidateCountTx 「仍有空位」的候选公会数（< MaxMembers）。
func soloVacantCandidateCountTx(tx *sql.Tx) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM cn35_guilds g
		WHERE g.source='npc' AND g.pool_role='candidate' AND
		(SELECT count(*) FROM cn35_guild_members m WHERE m.guild_id=g.guild_id)
		+ (SELECT count(*) FROM cn35_guild_npc_members n WHERE n.guild_id=g.guild_id)
		< ?`, cn35game.GuildRules.MaxMembers).Scan(&n)
	return n, err
}

// soloOpponentCountTx 对手公会数。
func soloOpponentCountTx(tx *sql.Tx) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM cn35_guilds
		WHERE source='npc' AND pool_role='opponent'`).Scan(&n)
	return n, err
}

// ensureGuildSoloPoolTx 有界补池：先保证对手公会数，再保证「有空位候选」数；
// 总量不超过 max_guilds，单次调用最多补到配置数量（不无限扩张）。
func ensureGuildSoloPoolTx(tx *sql.Tx) error {
	cfg, seq, err := soloStateTx(tx)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	total, err := soloGuildCountTx(tx)
	if err != nil {
		return err
	}
	// 对手公会优先（公会战匹配依赖足够独立敌会）。
	opp, err := soloOpponentCountTx(tx)
	if err != nil {
		return err
	}
	for opp < cfg.OpponentCount && total < cfg.MaxGuilds {
		seq++
		if err := seedSoloGuildTx(tx, seq, true); err != nil {
			return err
		}
		opp++
		total++
	}
	// 候选公会（19/20，一个真人空位）。
	vac, err := soloVacantCandidateCountTx(tx)
	if err != nil {
		return err
	}
	for vac < cfg.CandidateCount && total < cfg.MaxGuilds {
		seq++
		if err := seedSoloGuildTx(tx, seq, false); err != nil {
			return err
		}
		vac++
		total++
	}
	_, err = tx.Exec(`UPDATE cn35_guild_solo_state SET seq=?,updated_at=? WHERE id=1`,
		seq, nowUTC())
	return err
}

// seedSoloGuildTx 建一个 NPC 公会：guild 行 + 身份 + NPC 成员 + 防卫 + 设施。
// opponent=true → 20 名 NPC（无真人空位，仅作会战敌会）；false → 19 名 NPC
// （1 个真人空位，可免审加入）。
func seedSoloGuildTx(tx *sql.Tx, seq int, opponent bool) error {
	guildID := fmt.Sprintf("gnpc%04d", seq)
	role := "candidate"
	npcSlots := cn35game.GuildRules.MaxMembers - 1
	approval := "1" // auto_approval=true：搜索行显示「加入」按钮
	if opponent {
		role = "opponent"
		npcSlots = cn35game.GuildRules.MaxMembers
		approval = "2"
	}
	name := guildSoloGuildNames[(seq*5)%len(guildSoloGuildNames)]
	recruit := guildSoloRecruitTexts[seq%len(guildSoloRecruitTexts)]
	playType := guildSoloPlayTypes[seq%len(guildSoloPlayTypes)]
	rankID := guildSoloRankIDs[seq%len(guildSoloRankIDs)]
	rating := int64(600 + (seq*137)%1400)
	gpTotal := int64((seq * 1100) % 7000)
	now := nowUTC()
	if _, err := tx.Exec(`INSERT INTO cn35_guilds
		(guild_id,name,play_type_id,approval_type_id,recruit_text,memo,shifts,
		 battle_setting_text,rating,rank_id,gp,gp_total,created_at,updated_at,
		 source,pool_role,pool_batch)
		VALUES (?,?,?,?,?,?,127,?,?,?,0,?,?,?,'npc',?,?)`,
		guildID, name, playType, approval, recruit, "重建 NPC 公会（单机模式）",
		"每天 12:00 / 20:00 会战", rating, rankID, gpTotal, now, now,
		role, seq); err != nil {
		return err
	}
	for idx := 0; idx < npcSlots; idx++ {
		npcID := fmt.Sprintf("npc%04d%02d", seq, idx)
		npcName := guildSoloNPCNames[(seq*3+idx*7)%len(guildSoloNPCNames)]
		level := int64(12 + (seq*5+idx*13)%48)
		unitID := fmt.Sprintf("%d", guildSoloNPCUnits[(seq*2+idx*5)%len(guildSoloNPCUnits)])
		memberRole := 1
		switch {
		case idx == 0:
			memberRole = 3
		case idx <= 2:
			memberRole = 2
		}
		if _, err := tx.Exec(`INSERT INTO cn35_npc_identities
			(npc_id,name,level,leader_unit_id,detail_message,created_at)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT(npc_id) DO NOTHING`,
			npcID, npcName, level, unitID, "", now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cn35_guild_npc_members
			(guild_id,npc_id,role,joined_at) VALUES (?,?,?,?)
			ON CONFLICT(guild_id,npc_id) DO NOTHING`,
			guildID, npcID, memberRole, now); err != nil {
			return err
		}
		// 每名 NPC 1~2 只防卫魔宠（会长/副长 2 只）；等级为重建初值。
		defCount := 1
		if memberRole >= 2 {
			defCount = 2
		}
		for slot := 0; slot < defCount; slot++ {
			mid := guildSoloDefenseMonsterIDs[(seq+idx+slot)%len(guildSoloDefenseMonsterIDs)]
			level := 1 + (idx*3+slot)%30
			if _, err := tx.Exec(`INSERT INTO cn35_guild_npc_defense
				(guild_id,npc_id,slot,monster_id,level) VALUES (?,?,?,?,?)
				ON CONFLICT(guild_id,npc_id,slot) DO NOTHING`,
				guildID, npcID, slot, mid, level); err != nil {
				return err
			}
		}
	}
	// 设施：重建等级（非原服数值），使公会详情/BUFF 有可见内容。
	for i, typeID := range []string{"hp", "sp", "taghness", "at"} {
		level := 2 + (seq+i)%5
		point := int64(level) * cn35game.GuildRules.EquipPointPerLevel
		if _, err := tx.Exec(`INSERT INTO cn35_guild_equipment
			(guild_id,type_id,level,point,updated_at) VALUES (?,?,?,?,?)
			ON CONFLICT(guild_id,type_id) DO NOTHING`,
			guildID, typeID, level, point, now); err != nil {
			return err
		}
	}
	return nil
}

// defenseSnapshotTx 目标公会防卫快照（真人成员 + NPC 成员合并；出战冻结用）。
// NPC 防卫不带玩家魔宠实例：MonsterUUID 用 "npc:<npc_id>:<slot>" 合成，
// 只用于战斗表现与快照，不进入任何玩家库存/资产链。
func defenseSnapshotTx(tx *sql.Tx, guildID string) ([]cn35game.GWDefenseMonster, error) {
	out := []cn35game.GWDefenseMonster{}
	rows, err := tx.Query(`SELECT d.user_id,d.slot,d.monster_uuid,m.monster_id,m.level
		FROM cn35_guild_defense d
		JOIN cn35_player_monsters m ON m.user_id=d.user_id AND m.uuid=d.monster_uuid
		JOIN cn35_guild_members mem ON mem.user_id=d.user_id
		WHERE mem.guild_id=? ORDER BY d.user_id,d.slot`, guildID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d cn35game.GWDefenseMonster
		if err = rows.Scan(&d.OwnerUserID, &d.Slot, &d.MonsterUUID, &d.MonsterID, &d.Level); err != nil {
			rows.Close()
			return nil, err
		}
		d.GuildID = guildID
		out = append(out, d)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	npcRows, err := tx.Query(`SELECT npc_id,slot,monster_id,level
		FROM cn35_guild_npc_defense WHERE guild_id=? ORDER BY npc_id,slot`, guildID)
	if err != nil {
		return nil, err
	}
	defer npcRows.Close()
	for npcRows.Next() {
		var d cn35game.GWDefenseMonster
		var npcID string
		if err = npcRows.Scan(&npcID, &d.Slot, &d.MonsterID, &d.Level); err != nil {
			return nil, err
		}
		d.OwnerUserID = npcID
		d.MonsterUUID = fmt.Sprintf("npc:%s:%d", npcID, d.Slot)
		d.GuildID = guildID
		d.IsNPC = true
		out = append(out, d)
	}
	return out, npcRows.Err()
}

// guildSoloVacancy 供搜索/入会判定：返回 (占据人数, 是否可加入)。
func guildSoloVacancy(count int) bool { return count < cn35game.GuildRules.MaxMembers }

// isSoloGuildNameLike 关键词匹配（与服务端搜索同口径：名称子串）。
func isSoloGuildNameLike(name, keyword string) bool {
	if keyword == "" {
		return true
	}
	return strings.Contains(name, keyword)
}
