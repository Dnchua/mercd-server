// guild_test.go GUILD350-02..09：公会/公会战域测试。
// 覆盖：建会/入会（直进+审批）/职位/退会、设施投资、防卫卡组、
// 会战匹配/应援祈祷/出战结算、幂等重放、跨账号隔离、重启持久化。
package cn35store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func guildUID(t *testing.T, s *Store, tag string) string {
	t.Helper()
	return seedAccount(t, s, "dev-guild-"+tag)
}

func guildNowAt(hm string) time.Time {
	hour, minute := 12, 30
	fmt.Sscanf(hm, "%d:%d", &hour, &minute)
	// Asia/Shanghai 固定窗口内的时钟（2026-10-05 周一 12:30 +08）。
	return time.Date(2026, 10, 5, hour, minute, 0, 0,
		time.FixedZone("Asia/Shanghai", 8*3600))
}

func creditBalance(t *testing.T, s *Store, uid, cur string, n int64) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO cn35_player_balances
		(user_id,currency,amount,updated_at) VALUES (?,?,?,?)
		ON CONFLICT(user_id,currency) DO UPDATE SET amount=amount+excluded.amount`,
		uid, cur, n, nowUTC()); err != nil {
		t.Fatal(err)
	}
}

func insertMonster(t *testing.T, s *Store, uid, uuid string, monsterID int) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO cn35_player_monsters
		(user_id,uuid,monster_id,level,protected,created_at,updated_at)
		VALUES (?,?,?,1,0,?,?)`, uid, uuid, monsterID, nowUTC(), nowUTC()); err != nil {
		t.Fatal(err)
	}
}

func TestGuildCreateJoinView(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	u1 := guildUID(t, s, "u1")
	u2 := guildUID(t, s, "u2")

	g, err := s.CreateGuild(u1, "测试公会", "2", "1", "", "")
	if err != nil {
		t.Fatalf("CreateGuild: %v", err)
	}
	if g.MasterName == "" {
		t.Fatal("CreateGuild 应带回会长名")
	}
	v, err := s.MyGuild(u1)
	if err != nil || v.Guild == nil {
		t.Fatalf("MyGuild: %+v %v", v, err)
	}
	if v.MyRole != cn35game.GuildRoleMaster || len(v.Members) != 1 {
		t.Fatalf("MyGuild role/members: %+v", v)
	}
	// 快照投影
	snap, err := s.Snapshot(u1)
	if err != nil {
		t.Fatal(err)
	}
	if snap.User["guild_id"] != g.ID || snap.User["guild_role"] != 3 {
		t.Fatalf("snapshot guild: %+v", snap.User)
	}
	// 免审直进
	auto, err := s.JoinGuild(u2, g.ID, "")
	if err != nil || !auto {
		t.Fatalf("JoinGuild auto: %v auto=%v", err, auto)
	}
	v2, _ := s.MyGuild(u2)
	if v2.MyRole != cn35game.GuildRoleMember {
		t.Fatalf("u2 role: %d", v2.MyRole)
	}
	// 重复入会拒绝
	// GUILDLIFE350-03：无键重复入会＝同参重试 → 幂等成功（不再报错），
	// 但仍不得产生第二条成员行。历史上这里断言 ErrGuildJoined。
	if auto, err := s.JoinGuild(u2, g.ID, ""); err != nil || !auto {
		t.Fatalf("重复入会应幂等成功: err=%v auto=%v", err, auto)
	}
	var memberRows int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_members WHERE user_id=?`, u2).
		Scan(&memberRows); err != nil || memberRows != 1 {
		t.Fatalf("重复入会产生 %d 条成员行: %v", memberRows, err)
	}
}

func TestGuildApplyApproveFlow(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	m := guildUID(t, s, "m")
	a := guildUID(t, s, "a")
	g, err := s.CreateGuild(m, "审批会", "2", "2", "", "")
	if err != nil {
		t.Fatal(err)
	}
	auto, err := s.JoinGuild(a, g.ID, "")
	if err != nil || auto {
		t.Fatalf("审批制不应直进: %v auto=%v", err, auto)
	}
	// 重复申请同键拒绝
	if _, err := s.JoinGuild(a, g.ID, ""); !errors.Is(err, cn35game.ErrGuildSubDecided) {
		t.Fatalf("重复申请应 ErrGuildSubDecided: %v", err)
	}
	view, _ := s.MyGuild(m)
	if len(view.Subscriptions) != 1 {
		t.Fatalf("pending subs: %+v", view.Subscriptions)
	}
	subID := view.Subscriptions[0].SubID
	// 非本公会成员审批拒绝
	outsider := guildUID(t, s, "o")
	if err := s.DecideSubscription(outsider, subID, true, ""); !errors.Is(err, cn35game.ErrGuildNoPerm) {
		t.Fatalf("外人审批应 ErrGuildNoPerm: %v", err)
	}
	if err := s.DecideSubscription(m, subID, true, ""); err != nil {
		t.Fatalf("批准失败: %v", err)
	}
	v2, _ := s.MyGuild(a)
	if v2.Guild == nil || v2.Guild.ID != g.ID {
		t.Fatal("批准后应已在会")
	}
	// 重放同一决定：GUILDLIFE350-03 起为幂等成功（申请已是 approved），
	// 但不得重复入会或改他人归属。
	if err := s.DecideSubscription(m, subID, true, ""); err != nil {
		t.Fatalf("重放审批应幂等成功: %v", err)
	}
}

// 二次拒绝：终态可多条，pending 唯一（0028 部分唯一索引）。
func TestGuildReapplyAfterReject(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	m := guildUID(t, s, "m")
	a := guildUID(t, s, "a")
	g, err := s.CreateGuild(m, "二拒会", "2", "2", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.JoinGuild(a, g.ID, ""); err != nil {
			t.Fatalf("第%d次申请: %v", i+1, err)
		}
		v, _ := s.MyGuild(m)
		if err := s.DecideSubscription(m, v.Subscriptions[0].SubID, false, ""); err != nil {
			t.Fatalf("第%d次拒绝: %v", i+1, err)
		}
	}
}

func TestGuildRolesAndDropout(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	m := guildUID(t, s, "m")
	a := guildUID(t, s, "a")
	b := guildUID(t, s, "b")
	g, _ := s.CreateGuild(m, "职位会", "2", "1", "", "")
	if _, err := s.JoinGuild(a, g.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JoinGuild(b, g.ID, ""); err != nil {
		t.Fatal(err)
	}
	// 成员无权任命副长
	if err := s.ChangeRole(a, b, cn35game.GuildRoleSubMaster, ""); !errors.Is(err, cn35game.ErrGuildNoPerm) {
		t.Fatalf("成员任命应 ErrGuildNoPerm: %v", err)
	}
	if err := s.ChangeRole(m, a, cn35game.GuildRoleSubMaster, ""); err != nil {
		t.Fatalf("任命副长: %v", err)
	}
	// 副长踢普通成员 OK
	if err := s.Dropout(a, b, ""); err != nil {
		t.Fatalf("副长踢人: %v", err)
	}
	v, _ := s.MyGuild(b)
	if v.Guild != nil {
		t.Fatal("b 应已被踢出")
	}
	// 禅让
	if err := s.ChangeRole(m, a, cn35game.GuildRoleMaster, ""); err != nil {
		t.Fatalf("禅让: %v", err)
	}
	v2, _ := s.MyGuild(m)
	if v2.MyRole != cn35game.GuildRoleMember {
		t.Fatalf("原会长应降为成员: %d", v2.MyRole)
	}
	// 会长自退→禅让最老成员；再退到空解散
	if err := s.Dropout(a, "", ""); err != nil {
		t.Fatalf("会长自退: %v", err)
	}
	v3, _ := s.MyGuild(m)
	if v3.MyRole != cn35game.GuildRoleMaster {
		t.Fatalf("应禅让给 m: role=%d", v3.MyRole)
	}
	if err := s.Dropout(m, "", ""); err != nil {
		t.Fatalf("末人退会: %v", err)
	}
	if _, err := s.GuildShow(g.ID); !errors.Is(err, cn35game.ErrGuildNotFound) {
		t.Fatalf("空公会应解散: %v", err)
	}
}

func TestGuildDefenseAndInvest(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	u := guildUID(t, s, "u")
	g, _ := s.CreateGuild(u, "防卫会", "2", "1", "", "")
	insertMonster(t, s, u, "mon-aaa", 1)
	insertMonster(t, s, u, "mon-bbb", 2)
	if err := s.SetDefense(u, 0, "mon-aaa", ""); err != nil {
		t.Fatalf("SetDefense: %v", err)
	}
	// 同一魔宠两槽拒绝
	if err := s.SetDefense(u, 1, "mon-aaa", ""); !errors.Is(err, cn35game.ErrGuildMonster) {
		t.Fatalf("同宠两槽应 ErrGuildMonster: %v", err)
	}
	// 他人魔宠拒绝
	other := guildUID(t, s, "o")
	if err := s.SetDefense(other, 0, "mon-bbb", ""); !errors.Is(err, cn35game.ErrGuildNoMember) {
		t.Fatalf("未入会先拒绝: %v", err)
	}
	slots, _ := s.DefenseOf(u)
	if len(slots) != 1 || slots[0].MonsterUUID != "mon-aaa" {
		t.Fatalf("defense: %+v", slots)
	}
	if err := s.SetDefense(u, 2, "mon-bbb", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDefense(u, "mon-bbb", ""); err != nil {
		t.Fatalf("RemoveDefense: %v", err)
	}
	slots, _ = s.DefenseOf(u)
	if len(slots) != 1 {
		t.Fatalf("移除后槽数: %+v", slots)
	}
	// 快照
	defSnap, err := s.DefenseSnapshot(g.ID)
	if err != nil || len(defSnap) != 1 {
		t.Fatalf("DefenseSnapshot: %+v %v", defSnap, err)
	}
	// 投资：资材不足拒绝
	if _, err := s.Invest(u, "hp", 50, ""); !errors.Is(err, cn35game.ErrGuildAsset) {
		t.Fatalf("资材不足应 ErrGuildAsset: %v", err)
	}
	creditBalance(t, s, u, "guild_asset", 500)
	view, err := s.Invest(u, "hp", 250, "")
	if err != nil {
		t.Fatalf("Invest: %v", err)
	}
	eq := view.Equipments["hp"]
	// GC-04：等级不再是「100 点/级」占位规则，而由重建阈值曲线反解。
	// 250 point 在曲线上仍是 1 级（T(2)=17,700；锚点 T(71)=12,700,000 等见
	// cn35game/guild_storage_wire.go）。断言由曲线推导，不写魔法数。
	wantLevel := cn35game.GuildEquipLevel(250)
	if wantLevel != 1 {
		t.Fatalf("曲线自检: 250 point 应为 1 级，got %d", wantLevel)
	}
	if eq == nil || eq.Point != 250 || eq.Level != wantLevel || eq.UntilPoint != cn35game.GuildEquipThreshold(2) {
		t.Fatalf("equip: %+v", eq)
	}
	var bal int64
	_ = s.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency='guild_asset'`, u).Scan(&bal)
	if bal != 250 {
		t.Fatalf("余额应为 250: %d", bal)
	}
}

func TestGWSessionHelpPrayAttackSettle(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	now := guildNowAt("12:30")
	s.clock = func() time.Time { return now }
	u1 := guildUID(t, s, "u1")
	u2 := guildUID(t, s, "u2")
	g1, _ := s.CreateGuild(u1, "甲会", "2", "1", "", "")
	g2, _ := s.CreateGuild(u2, "乙会", "2", "1", "", "")
	// shifts 默认全开（127）
	v1, err := s.GWState(u1, now)
	if err != nil || v1.Session == nil {
		t.Fatalf("GWState: %+v %v", v1, err)
	}
	if len(v1.Participants) != 2 {
		t.Fatalf("匹配应两公会同组: %+v", v1.Participants)
	}
	v2, _ := s.GWState(u2, now)
	if v2.Session == nil || v2.Session.SessionID != v1.Session.SessionID {
		t.Fatal("同组场次应一致")
	}
	// 未入会者无场次
	out := guildUID(t, s, "out")
	v3, _ := s.GWState(out, now)
	if v3.Session != nil {
		t.Fatal("未入会不应有场次")
	}
	// 应援
	creditBalance(t, s, u1, "guild_asset", 0) // no-op 保持余额行
	// 应援成本按原服录包口径 help_bp=50 / help_gp=1000（旧 25 为重建占位）。
	gp, cost, err := s.GWHelpPray(u1, "help", "help-one", now)
	if err != nil || gp != 1000 || cost != 50 {
		t.Fatalf("help: gp=%d cost=%d err=%v", gp, cost, err)
	}
	// 重放不重复入账
	if _, _, err := s.GWHelpPray(u1, "help", "help-one", now); err != nil {
		t.Fatalf("help 重放应幂等: %v", err)
	}
	var pgp int64
	_ = s.DB().QueryRow(`SELECT gp FROM cn35_gw_participants
		WHERE session_id=? AND guild_id=?`, v1.Session.SessionID, g1.ID).Scan(&pgp)
	if pgp != 1000 {
		t.Fatalf("公会 GP 应为 1000: %d", pgp)
	}
	// 乙会也攒 1000 当场 GP，供掠夺断言
	if _, _, err := s.GWHelpPray(u2, "help", "", now); err != nil {
		t.Fatalf("乙会 help: %v", err)
	}
	// 防卫快照准备
	insertMonster(t, s, u2, "mon-def", 3)
	if err := s.SetDefense(u2, 0, "mon-def", ""); err != nil {
		t.Fatal(err)
	}
	// 出战：BP 扣减 + 快照冻结
	atk, units, _, err := s.GWAttackOpen(u1, g2.ID, nil, "normal", 1, 5, "", now)
	if err != nil {
		t.Fatalf("GWAttackOpen: %v", err)
	}
	if len(atk.DefenseSnapshot) != 1 || len(units) == 0 {
		t.Fatalf("attack: %+v units=%d", atk.DefenseSnapshot, len(units))
	}
	// 打己方/外组拒绝
	if _, _, _, err := s.GWAttackOpen(u1, g1.ID, nil, "normal", 1, 5, "", now); !errors.Is(err, cn35game.ErrGWTarget) {
		t.Fatalf("打己方应 ErrGWTarget: %v", err)
	}
	// prepared 未走 use_ap 不可结算
	if _, err := s.GWAttackSettle(u1, atk.BattleID, true, 0); !errors.Is(err, cn35game.ErrGWAttackState) {
		t.Fatalf("prepared 直报应 ErrGWAttackState: %v", err)
	}
	// use_ap 幂等
	if _, err := s.GWAttackPay(u1, atk.BattleID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GWAttackPay(u1, atk.BattleID); err != nil {
		t.Fatal("use_ap 重放应成功")
	}
	// 跨账号读取同形拒绝
	if _, err := s.GWAttackSession(u2, atk.BattleID); !errors.Is(err, cn35game.ErrGWAttackNotFound) {
		t.Fatalf("跨账号应 ErrGWAttackNotFound: %v", err)
	}
	// 胜利结算
	res, err := s.GWAttackSettle(u1, atk.BattleID, true, 0)
	if err != nil || res.Already {
		t.Fatalf("settle: %+v %v", res, err)
	}
	if res.GPDelta != cn35game.GuildRules.AttackGPWin {
		t.Fatalf("GP: %d", res.GPDelta)
	}
	// 胜场掠夺：乙会当场 GP 1000 × 普通 0.75% = 7 转移给甲会
	if res.Plundered != 7 {
		t.Fatalf("plunder: %d want 7", res.Plundered)
	}
	var tgtGP int64
	_ = s.DB().QueryRow(`SELECT gp FROM cn35_gw_participants
		WHERE session_id=? AND guild_id=?`, v1.Session.SessionID, g2.ID).Scan(&tgtGP)
	if tgtGP != 993 {
		t.Fatalf("被掠方 GP 应为 993: %d", tgtGP)
	}
	// 重放 already
	res2, _ := s.GWAttackSettle(u1, atk.BattleID, true, 0)
	if res2 == nil || !res2.Already {
		t.Fatal("结算重放应 Already")
	}
	// 败北另一场
	atk2, _, _, err := s.GWAttackOpen(u1, g2.ID, nil, "hard", 1, 5, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GWAttackPay(u1, atk2.BattleID); err != nil {
		t.Fatal(err)
	}
	lres, err := s.GWAttackSettle(u1, atk2.BattleID, false, 0)
	if err != nil || lres.GPDelta != cn35game.GuildRules.AttackGPLose {
		t.Fatalf("lose settle: %+v %v", lres, err)
	}
	// 结算台账/惰性关场：越过窗口后 Session 关闭
	later := time.Date(2026, 10, 5, 14, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	v4, err := s.GWState(u1, later)
	if err != nil {
		t.Fatal(err)
	}
	if v4.Session == nil || v4.Session.Status != "settled" || v4.Actable {
		t.Fatal("窗口外应保留 settled 场次且不可操作")
	}
	var status string
	_ = s.DB().QueryRow(`SELECT status FROM cn35_gw_sessions WHERE session_id=?`,
		v1.Session.SessionID).Scan(&status)
	if status != "settled" {
		t.Fatalf("场次应 settled: %s", status)
	}
	var rank int
	_ = s.DB().QueryRow(`SELECT rank_in_group FROM cn35_gw_participants
		WHERE session_id=? AND guild_id=?`, v1.Session.SessionID, g1.ID).Scan(&rank)
	if rank != 1 {
		t.Fatalf("胜方应第一: %d", rank)
	}
	// 排行榜可读
	ranks, err := s.GWRankings()
	if err != nil || len(ranks) != 2 {
		t.Fatalf("rankings: %+v %v", ranks, err)
	}
}

func TestGWRestartPersistence(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	u := guildUID(t, s, "u")
	g, err := s.CreateGuild(u, "重启会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GWState(u, guildNowAt("12:30")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2 := openStore(t, dir)
	defer s2.Close()
	v, err := s2.MyGuild(u)
	if err != nil || v.Guild == nil || v.Guild.ID != g.ID {
		t.Fatalf("重启后公会丢失: %+v %v", v, err)
	}
	v2, err := s2.GWState(u, guildNowAt("12:30"))
	if err != nil || v2.Session == nil {
		t.Fatalf("重启后场次丢失: %+v %v", v2, err)
	}
}
