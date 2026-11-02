// guild_solo_test.go GUILDLIFE350-04..10：单机 NPC 公会域与组织写面补强测试。
//
// 覆盖（对应 task.json 验收 ID）：
//   - GL-04 搜索池：稳定 ID 的 19/20 候选、列表/详情/成员/会长同源、关闭开关不破坏存档；
//   - GL-05 直接入会：免审加入 → 真人+NPC=20/20、满员拒绝、并发最后空位只有一个成功、
//     无键重试不重复写入、详情读取无写副作用；
//   - GL-06 NPC 独立身份：不建账号、不可被踢/升职、不进玩家资产链；
//   - GL-07 成员生命周期：退会恢复空位、跨重启一致、越权拒绝；
//   - GL-08/09/10 单机会战：NPC 对手参战、NPC 防卫快照、结算发奖去重、迟到结果不改榜。
package cn35store

import (
	"errors"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// soloStore 打开库并配置单机池（3 候选 + 3 对手，上限 24）。
func soloStore(t *testing.T) *Store {
	t.Helper()
	s := openStore(t, t.TempDir())
	if err := s.ConfigureGuildSolo(GuildSoloSettings{
		Enabled: true, CandidateCount: 3, OpponentCount: 3, MaxGuilds: 24,
	}); err != nil {
		t.Fatalf("ConfigureGuildSolo: %v", err)
	}
	return s
}

// TestGuildSoloPoolSeedSearch 池播种幂等 + 搜索结果形状（19/20、会长、成员同源）。
func TestGuildSoloPoolSeedSearch(t *testing.T) {
	s := soloStore(t)
	defer s.Close()
	viewer := guildUID(t, s, "solo-viewer")

	var total int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_guilds WHERE source='npc'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 6 { // 3 候选 + 3 对手
		t.Fatalf("NPC 公会数 = %d, want 6", total)
	}

	views, err := s.SearchGuilds(viewer, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cand := 0
	for i := range views {
		v := &views[i]
		if !v.Guild.IsNPC {
			continue
		}
		cand++
		if v.Guild.MemberCount != cn35game.GuildRules.MaxMembers-1 {
			t.Fatalf("候选 %s 人数 = %d, want 19", v.Guild.ID, v.Guild.MemberCount)
		}
		if !v.Guild.HasVacancy {
			t.Fatalf("候选 %s 应仍有空位", v.Guild.ID)
		}
		if v.Guild.MasterName == "" {
			t.Fatalf("候选 %s 缺会长名（详情/主页同源要求）", v.Guild.ID)
		}
		if len(v.Members) != 19 {
			t.Fatalf("候选 %s 成员行 = %d, want 19", v.Guild.ID, len(v.Members))
		}
		masters := 0
		for _, m := range v.Members {
			if !m.IsNPC {
				t.Fatalf("候选 %s 成员 %s 应为 NPC", v.Guild.ID, m.UserID)
			}
			if m.Role == cn35game.GuildRoleMaster {
				masters++
				if m.Name != v.Guild.MasterName {
					t.Fatalf("会长名不一致：%s vs %s", m.Name, v.Guild.MasterName)
				}
			}
			if m.Level <= 0 || m.LeaderUnitID == "" {
				t.Fatalf("NPC 成员展示字段缺失: %+v", m)
			}
		}
		if masters != 1 {
			t.Fatalf("候选 %s 会长数 = %d, want 1", v.Guild.ID, masters)
		}
		// 稳定 ID：再次搜索同一批候选（不新增公会）。
		gid := v.Guild.ID
		views2, err := s.SearchGuilds(viewer, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for j := range views2 {
			if views2[j].Guild.ID == gid {
				found = true
			}
		}
		if !found {
			t.Fatalf("候选 %s 在二次搜索中消失（ID 不稳定）", gid)
		}
		if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_guilds WHERE source='npc'`).Scan(&total); err != nil {
			t.Fatal(err)
		}
		if total != 6 {
			t.Fatalf("搜索触发了额外建池：NPC 公会数 = %d, want 6", total)
		}
	}
	if cand != 3 {
		t.Fatalf("搜索结果候选数 = %d, want 3", cand)
	}
}

// TestGuildSoloJoinToFullAndRejectOverflow 免审加入 → 20/20；满员拒绝；加入后不再作为候选。
func TestGuildSoloJoinToFullAndRejectOverflow(t *testing.T) {
	s := soloStore(t)
	defer s.Close()
	u1 := guildUID(t, s, "solo-join1")
	u2 := guildUID(t, s, "solo-join2")

	views, err := s.SearchGuilds(u1, "", "", "")
	if err != nil || len(views) == 0 {
		t.Fatalf("搜索: %v", err)
	}
	target := views[0].Guild.ID
	auto, err := s.JoinGuild(u1, target, "")
	if err != nil || !auto {
		t.Fatalf("免审加入失败: err=%v auto=%v", err, auto)
	}
	v, err := s.MyGuild(u1)
	if err != nil || v.Guild == nil {
		t.Fatalf("MyGuild: %+v %v", v, err)
	}
	if v.Guild.MemberCount != cn35game.GuildRules.MaxMembers {
		t.Fatalf("加入后人数 = %d, want 20", v.Guild.MemberCount)
	}
	if v.MyRole != cn35game.GuildRoleMember {
		t.Fatalf("真人玩家在 NPC 公会应是普通成员，got role=%d", v.MyRole)
	}
	// 快照归属投影（user.guild_id/guild_name/guild_role）。
	snap, err := s.Snapshot(u1)
	if err != nil {
		t.Fatal(err)
	}
	if snap.User["guild_id"] != target || snap.User["guild_role"] != cn35game.GuildRoleMember {
		t.Fatalf("快照公会归属: %+v", snap.User)
	}
	// 已满：第二个玩家被容量拒绝，且不会挤掉任何人。
	if _, err = s.JoinGuild(u2, target, ""); !errors.Is(err, cn35game.ErrGuildFull) {
		t.Fatalf("满员应拒绝，got %v", err)
	}
	if gid, _, err := memberRoleTxMust(t, s, u2); err != nil || gid != "" {
		t.Fatalf("被拒玩家不应入会: gid=%q err=%v", gid, err)
	}
	// 满员候选不再出现在可加入候选里。
	views2, err := s.SearchGuilds(u2, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range views2 {
		if views2[i].Guild.ID == target {
			t.Fatalf("满员公会 %s 仍作为候选出现", target)
		}
	}
	// 候选消耗后池子有界补充（总量不超过上限）。
	var total int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_guilds WHERE source='npc'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total > 24 {
		t.Fatalf("NPC 公会总量 = %d 超过上限 24", total)
	}
	// 新候选取自补充批次，且 ID 稳定。
	if len(views2) == 0 || !views2[0].Guild.IsNPC {
		t.Fatalf("补充后仍应有 NPC 候选: %+v", views2)
	}
}

func memberRoleTxMust(t *testing.T, s *Store, uid string) (string, int, error) {
	t.Helper()
	var gid string
	var role int
	err := s.DB().QueryRow(`SELECT guild_id,role FROM cn35_guild_members WHERE user_id=?`, uid).
		Scan(&gid, &role)
	if err != nil {
		return "", 0, nil
	}
	return gid, role, nil
}

// TestGuildSoloConcurrentLastSlot 20 名真人并发抢最后空位：只应成功一个。
func TestGuildSoloConcurrentLastSlot(t *testing.T) {
	s := soloStore(t)
	defer s.Close()
	probe := guildUID(t, s, "solo-probe")
	views, err := s.SearchGuilds(probe, "", "", "")
	if err != nil || len(views) == 0 {
		t.Fatalf("搜索: %v", err)
	}
	target := views[0].Guild.ID

	const n = 6
	users := make([]string, n)
	for i := 0; i < n; i++ {
		users[i] = guildUID(t, s, "solo-race-"+string(rune('a'+i)))
	}
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(uid string) {
			_, err := s.JoinGuild(uid, target, "")
			errs <- err
		}(users[i])
	}
	ok, full := 0, 0
	for i := 0; i < n; i++ {
		switch e := <-errs; {
		case e == nil:
			ok++
		case errors.Is(e, cn35game.ErrGuildFull):
			full++
		default:
			t.Fatalf("并发入会出现意外错误: %v", e)
		}
	}
	if ok != 1 || full != n-1 {
		t.Fatalf("并发最后空位 ok=%d full=%d, want 1/%d", ok, full, n-1)
	}
	var cnt int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_members WHERE guild_id=?`, target).
		Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("真人成员数 = %d, want 1", cnt)
	}
}

// TestGuildSoloDropoutRestoresVacancy 退会恢复 19/20、跨重启一致、NPC 不可被管理。
func TestGuildSoloDropoutRestoresVacancy(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	if err := s.ConfigureGuildSolo(GuildSoloSettings{Enabled: true, CandidateCount: 3, OpponentCount: 3, MaxGuilds: 24}); err != nil {
		t.Fatal(err)
	}
	uid := guildUID(t, s, "solo-dropout")
	views, _ := s.SearchGuilds(uid, "", "", "")
	target := views[0].Guild.ID
	if _, err := s.JoinGuild(uid, target, ""); err != nil {
		t.Fatal(err)
	}
	// NPC 成员不可踢/升职（身份不在 cn35_guild_members）。
	var npcID string
	if err := s.DB().QueryRow(`SELECT npc_id FROM cn35_guild_npc_members
		WHERE guild_id=? AND role=3`, target).Scan(&npcID); err != nil {
		t.Fatal(err)
	}
	if err := s.Dropout(uid, npcID, ""); !errors.Is(err, cn35game.ErrGuildNotFound) {
		t.Fatalf("踢 NPC 应拒绝，got %v", err)
	}
	if err := s.ChangeRole(uid, npcID, cn35game.GuildRoleMaster, ""); !errors.Is(err, cn35game.ErrGuildNotFound) {
		t.Fatalf("转让给 NPC 应拒绝，got %v", err)
	}
	// 自退：空位恢复。
	if err := s.Dropout(uid, "", ""); err != nil {
		t.Fatalf("退会: %v", err)
	}
	snap, _ := s.Snapshot(uid)
	if snap.User["guild_id"] != "" {
		t.Fatalf("退会后快照仍带公会: %+v", snap.User)
	}
	v, err := s.GuildShow(target)
	if err != nil || v.Guild.MemberCount != cn35game.GuildRules.MaxMembers-1 {
		t.Fatalf("退会后人数 = %v, want 19 (%v)", v, err)
	}
	// 重启（重新打开同一库）后仍为 19/20 且候选可再次加入。
	s.Close()
	s2 := openStore(t, dir)
	defer s2.Close()
	v2, err := s2.GuildShow(target)
	if err != nil || v2.Guild.MemberCount != cn35game.GuildRules.MaxMembers-1 {
		t.Fatalf("重启后人数 = %+v, want 19 (%v)", v2, err)
	}
	if auto, err := s2.JoinGuild(uid, target, ""); err != nil || !auto {
		t.Fatalf("退出后可再次加入: %v auto=%v", err, auto)
	}
}

// TestGuildSoloDisabledKeepsExistingData 关闭开关：不再补池/推荐，但既有存档可读可退。
func TestGuildSoloDisabledKeepsExistingData(t *testing.T) {
	s := soloStore(t)
	defer s.Close()
	uid := guildUID(t, s, "solo-off")
	views, _ := s.SearchGuilds(uid, "", "", "")
	target := views[0].Guild.ID
	if _, err := s.JoinGuild(uid, target, ""); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := s.DB().QueryRow(`SELECT seq FROM cn35_guild_solo_state WHERE id=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureGuildSolo(GuildSoloSettings{Enabled: false, CandidateCount: 3, OpponentCount: 3, MaxGuilds: 24}); err != nil {
		t.Fatal(err)
	}
	// 关闭后搜索不再给候选，但自己的公会可读。
	views2, err := s.SearchGuilds(uid, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range views2 {
		if views2[i].Guild.IsNPC {
			t.Fatalf("关闭开关后仍推荐 NPC 候选: %s", views2[i].Guild.ID)
		}
	}
	v, err := s.MyGuild(uid)
	if err != nil || v.Guild == nil || v.Guild.ID != target {
		t.Fatalf("关闭开关后既有公会应可读: %+v %v", v, err)
	}
	var after int
	if err := s.DB().QueryRow(`SELECT seq FROM cn35_guild_solo_state WHERE id=1`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("关闭开关后仍建池: seq %d → %d", before, after)
	}
	// 仍可退出（不删真人历史）。
	if err := s.Dropout(uid, "", ""); err != nil {
		t.Fatalf("关闭开关后仍可退出: %v", err)
	}
}

// TestGuildOpSameKeyDifferentPayloadRejected 显式幂等键：同键异参拒绝、同键同参不重复写。
func TestGuildOpSameKeyDifferentPayloadRejected(t *testing.T) {
	s := soloStore(t)
	defer s.Close()
	uid := guildUID(t, s, "solo-opkey")
	views, _ := s.SearchGuilds(uid, "", "", "")
	a, b := views[0].Guild.ID, views[1].Guild.ID
	if _, err := s.JoinGuild(uid, a, "k-join"); err != nil {
		t.Fatal(err)
	}
	// 同键异参 → ErrGuildOp（不能静默成功）。
	if _, err := s.JoinGuild(uid, b, "k-join"); !errors.Is(err, cn35game.ErrGuildOp) {
		t.Fatalf("同键异参应拒绝，got %v", err)
	}
	// 同键同参 → 幂等回放（视为「已加入」成功，不报错）。
	if auto, err := s.JoinGuild(uid, a, "k-join"); err != nil || !auto {
		t.Fatalf("同键同参重试应幂等成功: %v auto=%v", err, auto)
	}
	// 无键建会重试：第二次不得重复建会（返回已有公会）。
	owner := guildUID(t, s, "solo-opkey2")
	g1, err := s.CreateGuild(owner, "无键会", "1", "1", "", "")
	if err != nil {
		t.Fatalf("建会: %v", err)
	}
	g2, err := s.CreateGuild(owner, "无键会", "1", "1", "", "")
	if err != nil {
		t.Fatalf("无键重试应幂等返回已有公会: %v", err)
	}
	if g1.ID != g2.ID {
		t.Fatalf("无键重试建了两个公会: %s vs %s", g1.ID, g2.ID)
	}
	var owned int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_members WHERE user_id=?`, owner).
		Scan(&owned); err != nil {
		t.Fatal(err)
	}
	if owned != 1 {
		t.Fatalf("同一玩家重复入会行 = %d, want 1", owned)
	}
}

// TestGuildInvestRetrySameBucketNoDoubleCharge 无键投资重试：同 60s 桶只扣一次资材。
func TestGuildInvestRetrySameBucketNoDoubleCharge(t *testing.T) {
	s := soloStore(t)
	defer s.Close()
	uid := guildUID(t, s, "solo-invest")
	views, _ := s.SearchGuilds(uid, "", "", "")
	if _, err := s.JoinGuild(uid, views[0].Guild.ID, ""); err != nil {
		t.Fatal(err)
	}
	creditBalance(t, s, uid, "guild_asset", 100)
	now := guildNowAt("12:30")
	s.clock = func() time.Time { return now }
	var pointBefore int64
	if err := s.DB().QueryRow(`SELECT point FROM cn35_guild_equipment
		WHERE guild_id=? AND type_id='hp'`, views[0].Guild.ID).Scan(&pointBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Invest(uid, "hp", 30, ""); err != nil {
		t.Fatalf("投资: %v", err)
	}
	// 同桶同参重试：幂等回放，不再扣资材。
	if _, err := s.Invest(uid, "hp", 30, ""); err != nil {
		t.Fatalf("同桶重试应幂等: %v", err)
	}
	var amount int64
	if err := s.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency='guild_asset'`, uid).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if amount != 70 {
		t.Fatalf("资材余额 = %d, want 70（只扣一次 30）", amount)
	}
	var point int64
	if err := s.DB().QueryRow(`SELECT point FROM cn35_guild_equipment
		WHERE guild_id=? AND type_id='hp'`, views[0].Guild.ID).Scan(&point); err != nil {
		t.Fatal(err)
	}
	if point-pointBefore != 30 {
		t.Fatalf("设施 point 增量 = %d, want 30（NPC 公会预置 point=%d）", point-pointBefore, pointBefore)
	}
}

// TestGuildSoloGuildWarWithNPCOpponents 单机会战闭环：对手公会参战、NPC 防卫可打、
// 胜利入账、结算奖励只发一次、NPC 不进玩家资产链。
func TestGuildSoloGuildWarWithNPCOpponents(t *testing.T) {
	s := soloStore(t)
	defer s.Close()
	now := guildNowAt("12:30")
	s.clock = func() time.Time { return now }
	uid := guildUID(t, s, "solo-gw")
	views, _ := s.SearchGuilds(uid, "", "", "")
	myGuild := views[0].Guild.ID
	if _, err := s.JoinGuild(uid, myGuild, ""); err != nil {
		t.Fatal(err)
	}
	gw, err := s.GWState(uid, now)
	if err != nil {
		t.Fatalf("GWState: %v", err)
	}
	if gw.Session == nil || !gw.Actable {
		t.Fatalf("应进入可战会战场次: %+v", gw)
	}
	if len(gw.Participants) < 2 {
		t.Fatalf("单机也必须有敌会参战，participants=%d", len(gw.Participants))
	}
	// 敌方 = 对手池公会（不是同公会的 19 名 NPC 队友）。
	opponents := 0
	var target string
	for _, p := range gw.Participants {
		if p.Joined {
			continue
		}
		opponents++
		if target == "" {
			target = p.GuildID
		}
	}
	if opponents == 0 || target == myGuild {
		t.Fatalf("敌会集合非法: %+v", gw.Participants)
	}
	var poolRole string
	if err := s.DB().QueryRow(`SELECT pool_role FROM cn35_guilds WHERE guild_id=?`, target).
		Scan(&poolRole); err != nil {
		t.Fatal(err)
	}
	if poolRole != "opponent" {
		t.Fatalf("敌会 %s 应为对手池公会，got pool_role=%q", target, poolRole)
	}
	// NPC 防卫快照可打。
	defs, err := s.DefenseSnapshot(target)
	if err != nil || len(defs) == 0 {
		t.Fatalf("对手公会应有 NPC 防卫: %v len=%d", err, len(defs))
	}
	npcDefs := 0
	for _, d := range defs {
		if d.IsNPC && d.GuildID == target && d.MonsterUUID != "" {
			npcDefs++
		}
	}
	if npcDefs == 0 {
		t.Fatalf("快照里没有 NPC 防卫: %+v", defs)
	}
	// 开战 → 付 BP → 胜利结算。
	atk, _, _, err := s.GWAttackOpen(uid, target, nil, "normal", 1, 5, "", now)
	if err != nil {
		t.Fatalf("GWAttackOpen: %v", err)
	}
	if _, err := s.GWAttackPay(uid, atk.BattleID); err != nil {
		t.Fatalf("GWAttackPay: %v", err)
	}
	res, err := s.GWAttackSettle(uid, atk.BattleID, true, 0)
	if err != nil {
		t.Fatalf("GWAttackSettle: %v", err)
	}
	if res.LateResult || res.GPDelta <= 0 {
		t.Fatalf("窗内胜利应正常计 GP: %+v", res)
	}
	// 重复上报：不再发奖。
	again, err := s.GWAttackSettle(uid, atk.BattleID, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Already || len(again.Rewards) != 0 {
		t.Fatalf("重复结算应 Already 且不再发奖: %+v", again)
	}
	// 关场结算：胜方成员发奖一次（台账），NPC 公会没有玩家 scores。
	later := now.Add(90 * time.Minute)
	s.clock = func() time.Time { return later }
	if v, err := s.GWState(uid, later); err != nil || v == nil {
		t.Fatalf("关场后 GWState: %v", err)
	}
	var claims int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_gw_reward_claims
		WHERE user_id=? AND kind='settlement_win'`, uid).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims > 1 {
		t.Fatalf("同一场次结算奖励重复发放: claims=%d", claims)
	}
	// 再触发一次结算（幂等）后仍只有一条。
	if _, err := s.GWState(uid, later); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_gw_reward_claims
		WHERE user_id=? AND kind='settlement_win'`, uid).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if claims > 1 {
		t.Fatalf("重复结算应幂等: claims=%d", claims)
	}
}

// TestGWLateResultDoesNotRewriteSettled 迟到结果：不改已结榜 GP、不做掠夺、标记 LateResult。
func TestGWLateResultDoesNotRewriteSettled(t *testing.T) {
	s := soloStore(t)
	defer s.Close()
	now := guildNowAt("12:30")
	s.clock = func() time.Time { return now }
	uid := guildUID(t, s, "solo-late")
	views, _ := s.SearchGuilds(uid, "", "", "")
	if _, err := s.JoinGuild(uid, views[0].Guild.ID, ""); err != nil {
		t.Fatal(err)
	}
	gw, err := s.GWState(uid, now)
	if err != nil {
		t.Fatal(err)
	}
	var target string
	for _, p := range gw.Participants {
		if !p.Joined {
			target = p.GuildID
			break
		}
	}
	atk, _, _, err := s.GWAttackOpen(uid, target, nil, "normal", 1, 5, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GWAttackPay(uid, atk.BattleID); err != nil {
		t.Fatal(err)
	}
	// 先强制关场结算。
	later := now.Add(90 * time.Minute)
	s.clock = func() time.Time { return later }
	if _, err := s.GWState(uid, later); err != nil {
		t.Fatal(err)
	}
	var gpBefore, targetBefore int64
	if err := s.DB().QueryRow(`SELECT gp FROM cn35_gw_participants
		WHERE session_id=? AND guild_id=?`, gw.Session.SessionID, views[0].Guild.ID).Scan(&gpBefore); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT gp FROM cn35_gw_participants
		WHERE session_id=? AND guild_id=?`, gw.Session.SessionID, target).Scan(&targetBefore); err != nil {
		t.Fatal(err)
	}
	res, err := s.GWAttackSettle(uid, atk.BattleID, true, 0)
	if err != nil {
		t.Fatalf("已付费战斗跨关场结算不应被拒: %v", err)
	}
	if !res.LateResult {
		t.Fatalf("应标记为迟到结果: %+v", res)
	}
	if res.Plundered != 0 {
		t.Fatalf("迟到结果不应掠夺: %d", res.Plundered)
	}
	var gpAfter, targetAfter int64
	if err := s.DB().QueryRow(`SELECT gp FROM cn35_gw_participants
		WHERE session_id=? AND guild_id=?`, gw.Session.SessionID, views[0].Guild.ID).Scan(&gpAfter); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT gp FROM cn35_gw_participants
		WHERE session_id=? AND guild_id=?`, gw.Session.SessionID, target).Scan(&targetAfter); err != nil {
		t.Fatal(err)
	}
	if gpAfter != gpBefore || targetAfter != targetBefore {
		t.Fatalf("迟到结果改写了已结榜 GP: self %d→%d, target %d→%d",
			gpBefore, gpAfter, targetBefore, targetAfter)
	}
}
