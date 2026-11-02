// guild_social_test.go GUILDCLOSE350 GC-07/GC-08：公会社交面持久化回归。
//
// 覆盖：等待邀请登记/列表/筛选、邀请权限与容量、重复邀请幂等、过期邀请收敛、
// 单机 NPC 候补邀请入会、重启恢复、会战留言写入/读取/短时去重、成员行补充
// 数据（最后活动时间/贡献点数）。全部使用临时目录库，不碰 server-state-cn35。
package cn35store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// socialStoreNow 固定时钟（与 guild_test.go 的会战窗口无关，仅用于 TTL 判定）。
func socialStoreNow() time.Time {
	return time.Date(2026, 10, 28, 12, 45, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
}

func TestGuildWaitingRegisterListInvite(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	u1 := guildUID(t, s, "social-master")
	u2 := guildUID(t, s, "social-waiter")
	g, err := s.CreateGuild(u1, "社交测试会", "2", "1", "", "")
	if err != nil {
		t.Fatalf("CreateGuild: %v", err)
	}
	now := socialStoreNow()

	// 未登记时列表里没有 u2（NPC 候补是单机扩展，单独断言）。
	users, err := s.WaitingList(u1, "", cn35game.GuildWaitingTTL, now, true)
	if err != nil {
		t.Fatalf("WaitingList: %v", err)
	}
	for _, u := range users {
		if u.UserID == u2 {
			t.Fatal("未登记的账号不应出现在等待列表")
		}
	}

	// 登记 + 重复登记（改方针）都成功；幂等键不可混用同键异参。
	if err := s.WaitingAdd(u2, "3", ""); err != nil {
		t.Fatalf("WaitingAdd: %v", err)
	}
	if err := s.WaitingAdd(u2, "3", ""); err != nil {
		t.Fatalf("WaitingAdd 重试: %v", err)
	}
	if err := s.WaitingAdd(u2, "1", ""); err != nil {
		t.Fatalf("WaitingAdd 改方针: %v", err)
	}
	if err := s.WaitingAdd(u2, "2", "fixed-key"); err != nil {
		t.Fatalf("WaitingAdd 显式键: %v", err)
	}
	if err := s.WaitingAdd(u2, "3", "fixed-key"); !errors.Is(err, cn35game.ErrGuildOp) {
		t.Fatalf("同键异参应拒绝, got=%v", err)
	}
	if ok, err := s.WaitingState(u2); err != nil || !ok {
		t.Fatalf("WaitingState=%v,%v", ok, err)
	}

	users, err = s.WaitingList(u1, "", cn35game.GuildWaitingTTL, now, true)
	if err != nil {
		t.Fatalf("WaitingList: %v", err)
	}
	found := false
	for _, u := range users {
		if u.UserID == u2 {
			found = true
			if u.PlayTypeID != "2" || u.Level <= 0 || u.Name == "" {
				t.Fatalf("候补行字段不完整: %+v", u)
			}
		}
	}
	if !found {
		t.Fatal("登记后应出现在等待列表")
	}
	// 方针筛选：只留「精益求精(3)」时应看不到期望 2 的 u2。
	users, err = s.WaitingList(u1, "3", cn35game.GuildWaitingTTL, now, true)
	if err != nil {
		t.Fatalf("WaitingList filter: %v", err)
	}
	for _, u := range users {
		if u.UserID == u2 {
			t.Fatal("方针筛选未生效")
		}
	}

	// 邀请：真人目标 → pending invite，被邀请人能查到该公会。
	subID, err := s.InviteUser(u1, u2, "", now)
	if err != nil || subID == "" {
		t.Fatalf("InviteUser: %q %v", subID, err)
	}
	// 重复邀请幂等（同一 sub_id，不报错、不产生第二条 pending）。
	subID2, err := s.InviteUser(u1, u2, "", now)
	if err != nil || subID2 != subID {
		t.Fatalf("重复邀请应幂等: %q %q %v", subID, subID2, err)
	}
	ids, err := s.InvitedGuildIDs(u2, cn35game.GuildWaitingTTL, now)
	if err != nil || len(ids) != 1 || ids[0] != g.ID {
		t.Fatalf("InvitedGuildIDs=%v,%v", ids, err)
	}
	// 被邀请人接受（store 原语：DecideSubscription approve）→ 入会；
	// HTTP 侧的接受入口是原服 join_approve_url（/guilds/join/?guild_id=）。
	if err := s.DecideSubscription(u2, subID, true, ""); err != nil {
		t.Fatalf("接受邀请: %v", err)
	}
	v, err := s.MyGuild(u2)
	if err != nil || v.Guild == nil || v.Guild.ID != g.ID {
		t.Fatalf("接受邀请后应在会: %+v %v", v, err)
	}
	// 已入会后收到的邀请全部收敛为 cancelled。
	ids, err = s.InvitedGuildIDs(u2, cn35game.GuildWaitingTTL, now)
	if err != nil || len(ids) != 0 {
		t.Fatalf("入会后不应再有邀请: %v %v", ids, err)
	}
	// 已入会者不得登记等待邀请。
	if err := s.WaitingAdd(u2, "1", ""); !errors.Is(err, cn35game.ErrGuildJoined) {
		t.Fatalf("已入会登记应拒绝, got=%v", err)
	}
}

func TestGuildWaitingInviteRules(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	master := guildUID(t, s, "rule-master")
	member := guildUID(t, s, "rule-member")
	outsider := guildUID(t, s, "rule-outsider")
	other := guildUID(t, s, "rule-other")
	now := socialStoreNow()
	g, err := s.CreateGuild(master, "规则会", "1", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.JoinGuild(member, g.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateGuild(other, "别家会", "1", "1", "", ""); err != nil {
		t.Fatal(err)
	}

	// 1) 权限：普通成员不能邀请。
	if _, err := s.InviteUser(member, outsider, "", now); !errors.Is(err, cn35game.ErrGuildNoPerm) {
		t.Fatalf("成员邀请应 ErrGuildNoPerm, got=%v", err)
	}
	// 2) 不能邀请自己。
	if _, err := s.InviteUser(master, master, "", now); !errors.Is(err, cn35game.ErrGuildSocialSelf) {
		t.Fatalf("自邀应 ErrGuildSocialSelf, got=%v", err)
	}
	// 3) 目标已入会。
	if _, err := s.InviteUser(master, member, "", now); !errors.Is(err, cn35game.ErrGuildJoined) {
		t.Fatalf("邀已入会者应 ErrGuildJoined, got=%v", err)
	}
	// 4) 目标不存在。
	if _, err := s.InviteUser(master, "no-such-user", "", now); !errors.Is(err, cn35game.ErrGuildSocialUser) {
		t.Fatalf("不存在目标应 ErrGuildSocialUser, got=%v", err)
	}
	// 5) 未入会者发起邀请。
	if _, err := s.InviteUser(outsider, master, "", now); !errors.Is(err, cn35game.ErrGuildNoMember) {
		t.Fatalf("非成员邀请应 ErrGuildNoMember, got=%v", err)
	}
	// 6) 满员：塞满 20 席（真人 19 + 会长 1）后邀请必须拒绝。
	for i := 1; i < cn35game.GuildRules.MaxMembers; i++ {
		if _, err := s.DB().Exec(`INSERT INTO cn35_guild_members
			(guild_id,user_id,role,joined_at) VALUES (?,?,1,?)`,
			g.ID, "fill-"+time.Duration(i).String(), nowUTC()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InviteUser(master, outsider, "", now); !errors.Is(err, cn35game.ErrGuildFull) {
		t.Fatalf("满员邀请应 ErrGuildFull, got=%v", err)
	}
}

func TestGuildWaitingInviteExpiry(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	master := guildUID(t, s, "exp-master")
	waiter := guildUID(t, s, "exp-waiter")
	now := socialStoreNow()
	g, err := s.CreateGuild(master, "过期会", "1", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// 直接写一条 30 天前的 pending 邀请（模拟原服「时间过的太久」）。
	old := now.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().Exec(`INSERT INTO cn35_guild_subscriptions
		(sub_id,guild_id,user_id,kind,status,created_at)
		VALUES ('sub-old',?,?,'invite','pending',?)`, g.ID, waiter, old); err != nil {
		t.Fatal(err)
	}
	ids, err := s.InvitedGuildIDs(waiter, cn35game.GuildWaitingTTL, now)
	if err != nil {
		t.Fatalf("InvitedGuildIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("过期邀请不应下发: %v", ids)
	}
	var status string
	if err := s.DB().QueryRow(`SELECT status FROM cn35_guild_subscriptions
		WHERE sub_id='sub-old'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("过期邀请应收敛为 cancelled, got=%q", status)
	}
}

func TestGuildWaitingNPCCandidateInvite(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	master := guildUID(t, s, "npc-master")
	now := socialStoreNow()
	g, err := s.CreateGuild(master, "候补会", "1", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// 单机域默认启用（未落状态行 → DefaultGuildSoloSettings）→ 列表非空。
	users, err := s.WaitingList(master, "", cn35game.GuildWaitingTTL, now, true)
	if err != nil {
		t.Fatalf("WaitingList: %v", err)
	}
	if len(users) == 0 {
		t.Fatal("单机域应补出 NPC 待邀请候补")
	}
	var target cn35game.WaitingUser
	for _, u := range users {
		if u.IsNPC {
			target = u
			break
		}
	}
	if target.UserID == "" {
		t.Fatalf("候补行缺 NPC 标记: %+v", users)
	}
	// 邀请 NPC 候补 → 邀请即入会（单机扩展），席位落在 NPC 成员表。
	if _, err := s.InviteUser(master, target.UserID, "", now); err != nil {
		t.Fatalf("邀请 NPC 候补: %v", err)
	}
	v, err := s.MyGuild(master)
	if err != nil {
		t.Fatal(err)
	}
	// 会长 1 + NPC 1（新会没有 NPC 成员）。
	if len(v.Members) != 2 {
		t.Fatalf("邀请后成员数=%d, 期望 2", len(v.Members))
	}
	// 再次邀请同一 NPC → 已入会。
	if _, err := s.InviteUser(master, target.UserID, "", now); !errors.Is(err, cn35game.ErrGuildJoined) {
		t.Fatalf("二次邀请应 ErrGuildJoined, got=%v", err)
	}
	// 该 NPC 已从候补列表消失。
	users, err = s.WaitingList(master, "", cn35game.GuildWaitingTTL, now, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.UserID == target.UserID {
			t.Fatal("已入会的候补不应再出现在列表")
		}
	}
	_ = g
}

// TestGuildWaitingPoolConfigGate 单机 NPC 候补池的**独立配置**门控：
// 配置关 / solo 关都必须退化成「只有真人登记队列」（原服语义），且 max 生效。
func TestGuildWaitingPoolConfigGate(t *testing.T) {
	now := socialStoreNow()

	// 1) 配置关：不注入任何 NPC 候补。
	s := openStore(t, t.TempDir())
	defer s.Close()
	master := guildUID(t, s, "gate-master")
	if _, err := s.CreateGuild(master, "门控会", "1", "1", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureWaitingPool(cn35game.GuildWaitingPoolSettings{Enabled: false}); err != nil {
		t.Fatalf("ConfigureWaitingPool: %v", err)
	}
	users, err := s.WaitingList(master, "", cn35game.GuildWaitingTTL, now, true)
	if err != nil {
		t.Fatalf("WaitingList: %v", err)
	}
	for _, u := range users {
		if u.IsNPC {
			t.Fatalf("配置关闭时不得注入 NPC 候补: %+v", u)
		}
	}
	// 配置回开 → 重新兜底注入。
	if err := s.ConfigureWaitingPool(cn35game.GuildWaitingPoolSettings{Enabled: true, Max: 2}); err != nil {
		t.Fatal(err)
	}
	users, err = s.WaitingList(master, "", cn35game.GuildWaitingTTL, now, true)
	if err != nil {
		t.Fatal(err)
	}
	npc := 0
	for _, u := range users {
		if u.IsNPC {
			npc++
		}
	}
	if npc == 0 || npc > 2 {
		t.Fatalf("配置开启后 NPC 候补数=%d, 期望 1..2", npc)
	}
	// 上限收敛（Normalize）。
	if err := s.ConfigureWaitingPool(cn35game.GuildWaitingPoolSettings{Enabled: true, Max: 999}); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.WaitingPoolSettings()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Max != cn35game.GuildWaitingPoolMaxLimit {
		t.Fatalf("Max 未收敛: %d", cfg.Max)
	}

	// 2) solo 域关：即使候补池配置开启也不注入。
	s2 := openStore(t, t.TempDir())
	defer s2.Close()
	master2 := guildUID(t, s2, "gate-master2")
	if _, err := s2.CreateGuild(master2, "门控会2", "1", "1", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s2.ConfigureGuildSolo(cn35storeSoloDisabled()); err != nil {
		t.Fatalf("ConfigureGuildSolo: %v", err)
	}
	if err := s2.ConfigureWaitingPool(cn35game.GuildWaitingPoolSettings{Enabled: true, Max: 6}); err != nil {
		t.Fatal(err)
	}
	users, err = s2.WaitingList(master2, "", cn35game.GuildWaitingTTL, now, true)
	if err != nil {
		t.Fatalf("WaitingList: %v", err)
	}
	for _, u := range users {
		if u.IsNPC {
			t.Fatalf("solo 域关闭时不得注入 NPC 候补: %+v", u)
		}
	}
}

// cn35storeSoloDisabled solo 域关闭但保留既有池配额（只改 enabled）。
func cn35storeSoloDisabled() GuildSoloSettings {
	cfg := DefaultGuildSoloSettings
	cfg.Enabled = false
	return cfg
}

// TestGuildWaitingViewerPrivacy viewer 维度过滤（Lead 复核要求）：
// 未入会 viewer 只能看到 NPC 候补（重建身份域），**不得**看到真人登记行；
// 会内 viewer 才能看到真人登记队列。
func TestGuildWaitingViewerPrivacy(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	master := guildUID(t, s, "priv-master")
	waiter := guildUID(t, s, "priv-waiter")
	outsider := guildUID(t, s, "priv-outsider")
	now := socialStoreNow()
	if _, err := s.CreateGuild(master, "隐私会", "1", "1", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitingAdd(waiter, "2", ""); err != nil {
		t.Fatal(err)
	}
	// 会内 viewer：看得到真人登记行。
	users, err := s.WaitingList(master, "", cn35game.GuildWaitingTTL, now, true)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, u := range users {
		if u.UserID == waiter {
			seen = true
		}
	}
	if !seen {
		t.Fatal("会内 viewer 应看到真人登记行")
	}
	// 未入会 viewer：看不到任何真人登记行（NPC 候补不受影响）。
	users, err = s.WaitingList(outsider, "", cn35game.GuildWaitingTTL, now, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if !u.IsNPC {
			t.Fatalf("未入会 viewer 不得看到真人登记行: %+v", u)
		}
	}
}

// TestGuildSocialRestartPersistence 重启恢复：登记与邀请在重开库后仍可读。
func TestGuildSocialRestartPersistence(t *testing.T) {
	dir := t.TempDir()
	now := socialStoreNow()
	s := openStore(t, dir)
	master := guildUID(t, s, "restart-master")
	waiter := guildUID(t, s, "restart-waiter")
	g, err := s.CreateGuild(master, "重启会", "1", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WaitingAdd(waiter, "4", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InviteUser(master, waiter, "", now); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := Open(filepath.Join(dir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("重开库: %v", err)
	}
	defer s2.Close()
	ids, err := s2.InvitedGuildIDs(waiter, cn35game.GuildWaitingTTL, now)
	if err != nil || len(ids) != 1 || ids[0] != g.ID {
		t.Fatalf("重启后邀请丢失: %v %v", ids, err)
	}
	users, err := s2.WaitingList(master, "4", cn35game.GuildWaitingTTL, now, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range users {
		if u.UserID == waiter {
			found = true
		}
	}
	if !found {
		t.Fatal("重启后等待登记丢失")
	}
}

// socialRoomFixture 落一个会战场次 + 本会参战行（留言房间与归属校验的前置）。
func socialRoomFixture(t *testing.T, s *Store, guildID, roomID string) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO cn35_gw_sessions
		(session_id,date_key,field_index,group_key,status,created_at)
		VALUES (?,?,6,'g','open',?)`,
		roomID, "2026-10-28", nowUTC()); err != nil {
		t.Fatalf("插入场次: %v", err)
	}
	if _, err := s.DB().Exec(`INSERT INTO cn35_gw_participants
		(session_id,guild_id,color,gp,rank_in_group,seed_powerup_rate)
		VALUES (?,?,'ff4848',0,1,0)`, roomID, guildID); err != nil {
		t.Fatalf("插入参战行: %v", err)
	}
}

func TestGuildBattleComments(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	defer s.Close()
	uid := guildUID(t, s, "comment-user")
	other := guildUID(t, s, "comment-other")
	now := socialStoreNow()
	g, err := s.CreateGuild(uid, "留言会", "1", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	room := "sess-room-1"
	socialRoomFixture(t, s, g.ID, room)

	// 空房间：无留言 → 计数 0。
	latest, rows, err := s.BattleCommentFeed(room, 100)
	if err != nil || latest != 0 || len(rows) != 0 {
		t.Fatalf("空留言读面: %d %d %v", latest, len(rows), err)
	}
	// 无房间（未入场次）→ 空读、不报错。
	if latest, rows, err = s.BattleCommentFeed("", 100); err != nil || latest != 0 || len(rows) != 0 {
		t.Fatalf("空房间 id 读面: %d %d %v", latest, len(rows), err)
	}
	// 写入两条。
	c1, err := s.AddBattleComment(uid, room, "第一句话", now)
	if err != nil {
		t.Fatalf("AddBattleComment: %v", err)
	}
	// 同人同文短时去重：同一条，不新增行。
	c1dup, err := s.AddBattleComment(uid, room, "第一句话", now.Add(time.Second))
	if err != nil || c1dup.ID != c1.ID {
		t.Fatalf("短时去重失败: %+v %v", c1dup, err)
	}
	if _, err := s.AddBattleComment(uid, room, "第二句话", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	latest, rows, err = s.BattleCommentFeed(room, 100)
	if err != nil || len(rows) != 2 || latest != now.Unix()+2 {
		t.Fatalf("读面: latest=%d rows=%d err=%v", latest, len(rows), err)
	}
	// 越权：非本会成员不能在他人房间留言。
	if _, err := s.AddBattleComment(other, room, "捣乱", now); !errors.Is(err, cn35game.ErrGuildNoMember) {
		t.Fatalf("非成员留言应 ErrGuildNoMember, got=%v", err)
	}
	// 本会成员但该场次不是本会参战 → ErrGWNoSession。
	otherGuild, err := s.CreateGuild(other, "别家留言会", "1", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = otherGuild
	if _, err := s.AddBattleComment(other, room, "跨会留言", now); !errors.Is(err, cn35game.ErrGWNoSession) {
		t.Fatalf("未参战场次应 ErrGWNoSession, got=%v", err)
	}
	// 内容校验（1–110 字，不放宽）。
	if _, err := s.AddBattleComment(uid, room, "   ", now); !errors.Is(err, cn35game.ErrGuildSocialComment) {
		t.Fatalf("空留言应拒绝, got=%v", err)
	}
	long := ""
	for i := 0; i < cn35game.BattleCommentMaxRunes+1; i++ {
		long += "あ"
	}
	if _, err := s.AddBattleComment(uid, room, long, now); !errors.Is(err, cn35game.ErrGuildSocialComment) {
		t.Fatalf("超长留言应拒绝, got=%v", err)
	}
	// 空房间 id（客户端未带 battle_id 且未回落到场次时）。
	if _, err := s.AddBattleComment(uid, "", "hi", now); !errors.Is(err, cn35game.ErrGWNoSession) {
		t.Fatalf("空房间应 ErrGWNoSession, got=%v", err)
	}
	s.Close()

	// 重启恢复：留言仍在。
	s2, err := Open(filepath.Join(dir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	latest2, rows2, err := s2.BattleCommentFeed(room, 100)
	if err != nil || len(rows2) != 2 || latest2 != latest {
		t.Fatalf("重启后留言: latest=%d rows=%d err=%v", latest2, len(rows2), err)
	}
}

func TestGuildMemberExtras(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	u1 := guildUID(t, s, "extras-master")
	u2 := guildUID(t, s, "extras-member")
	g, err := s.CreateGuild(u1, "补充数据会", "1", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.JoinGuild(u2, g.ID, ""); err != nil {
		t.Fatal(err)
	}
	// 贡献点数：一条会战个人分数行。
	if _, err := s.DB().Exec(`INSERT INTO cn35_gw_member_scores
		(session_id,user_id,guild_id,attack_count,win_count,gp_earned,updated_at)
		VALUES ('sess-x',?,?,3,2,777,?)`, u2, g.ID, nowUTC()); err != nil {
		t.Fatal(err)
	}
	loggedAt, point, err := s.GuildMemberExtras(g.ID)
	if err != nil {
		t.Fatalf("GuildMemberExtras: %v", err)
	}
	if point[u2] != 777 {
		t.Fatalf("贡献点数=%d, 期望 777", point[u2])
	}
	if loggedAt[u1] == 0 || loggedAt[u2] == 0 {
		t.Fatalf("最后活动时间缺失: %+v", loggedAt)
	}
}
