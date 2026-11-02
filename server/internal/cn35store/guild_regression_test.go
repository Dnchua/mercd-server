package cn35store

import (
	"errors"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"sync"
	"testing"
	"time"
)

func gwFixture(t *testing.T, s *Store, n int) ([]string, []string, time.Time) {
	t.Helper()
	now := guildNowAt("12:30")
	s.clock = func() time.Time { return now }
	users, guilds := []string{}, []string{}
	for i := 0; i < n; i++ {
		uid := guildUID(t, s, string(rune('a'+i)))
		g, err := s.CreateGuild(uid, string(rune('甲'+i))+"会", "2", "1", "", "")
		if err != nil {
			t.Fatal(err)
		}
		uuid := "def-" + uid
		insertMonster(t, s, uid, uuid, 3)
		if err = s.SetDefense(uid, 0, uuid, ""); err != nil {
			t.Fatal(err)
		}
		users = append(users, uid)
		guilds = append(guilds, g.ID)
	}
	if _, err := s.GWState(users[0], now); err != nil {
		t.Fatal(err)
	}
	return users, guilds, now
}

// gwStartBP 合成账号初始 BP（与 cn35_player_profiles 默认值一致）。
const gwStartBP = 300

func gwBP(t *testing.T, s *Store, uid string) int {
	t.Helper()
	var bp int
	if err := s.DB().QueryRow(`SELECT bp FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&bp); err != nil {
		t.Fatal(err)
	}
	return bp
}

func TestGWRepeatedActionsAndConflict(t *testing.T) {
	s := openStore(t, t.TempDir())
	u, _, now := gwFixture(t, s, 2)
	for i := 0; i < 2; i++ {
		if _, _, err := s.GWHelpPray(u[0], "help", "", now); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, _, err := s.GWHelpPray(u[0], "pray", "", now); err != nil {
			t.Fatal(err)
		}
	}
	// BP 成本按原服录包口径：help_bp=50、pray_bp=15（旧占位 help=25 已废弃，
	// 见 plans/cn350-guild-live-closure-2026-10-28 阶段3与 GC-05）。
	f := cn35game.GuildRules.Fields[cn35game.GuildActableFieldIndex(now)]
	wantBP := gwStartBP - 2*f.HelpBP - 2*f.PrayBP
	v, err := s.GWState(u[0], now)
	if err != nil || v.Power != 800 || v.HelpCount != 2 || v.PrayCount != 2 || v.SeedPowerup != 1.04 || gwBP(t, s, u[0]) != wantBP {
		t.Fatalf("repeated actions: %+v bp=%d want=%d err=%v", v, gwBP(t, s, u[0]), wantBP, err)
	}
	if _, _, err = s.GWHelpPray(u[0], "help", "retry", now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err = s.GWHelpPray(u[0], "help", "retry", now); err != nil {
			t.Fatal(err)
		}
	}
	if gwBP(t, s, u[0]) != wantBP-f.HelpBP {
		t.Fatal("replay charged again")
	}
	if _, _, err = s.GWHelpPray(u[0], "pray", "retry", now); !errors.Is(err, cn35game.ErrGuildOp) {
		t.Fatalf("key conflict: %v", err)
	}
	if _, _, err = s.GWHelpPray(u[0], "invalid", "", now); !errors.Is(err, cn35game.ErrGuildOp) {
		t.Fatal(err)
	}
	s.DB().Exec(`UPDATE cn35_player_profiles SET bp=0 WHERE user_id=?`, u[0])
	before, _ := s.GWState(u[0], now)
	if _, _, err = s.GWHelpPray(u[0], "pray", "failed", now); !errors.Is(err, cn35game.ErrGWBP) {
		t.Fatal(err)
	}
	after, _ := s.GWState(u[0], now)
	if before.PrayCount != after.PrayCount {
		t.Fatal("failed action did not roll back")
	}
}

func TestGWOpenReplayMultiTargetAndRestart(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	u, g, now := gwFixture(t, s, 4)
	s.GWHelpPray(u[1], "help", "", now)
	s.GWHelpPray(u[2], "help", "", now)
	s.GWHelpPray(u[3], "help", "", now)
	var wg sync.WaitGroup
	bids := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, _, _, err := s.GWAttackOpen(u[0], g[1], g[2:], "normal", 1, 5, "", now)
			if err != nil {
				errs <- err
			} else {
				bids <- a.BattleID
			}
		}()
	}
	wg.Wait()
	close(bids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	bid := ""
	for b := range bids {
		if bid != "" && b != bid {
			t.Fatal("parallel replay created multiple attacks")
		}
		bid = b
	}
	if gwBP(t, s, u[0]) != 230 {
		t.Fatal("three targets should cost 70 once")
	}
	a, units, _, err := s.GWAttackOpen(u[0], g[1], g[2:], "normal", 1, 5, "explicit", now)
	if err != nil || a.BattleID != bid || len(a.DefenseSnapshot) != 3 || len(a.TargetGuildIDs) != 3 || len(units) == 0 {
		t.Fatalf("snapshot/replay: %+v %v", a, err)
	}
	if _, _, _, err = s.GWAttackOpen(u[0], g[1], nil, "normal", 1, 5, "explicit", now); !errors.Is(err, cn35game.ErrGuildOp) {
		t.Fatalf("same key different request: %v", err)
	}
	if _, _, _, err = s.GWAttackOpen(u[0], g[1], nil, "hard", 1, 5, "", now); !errors.Is(err, cn35game.ErrGWAttackState) {
		t.Fatalf("active conflict: %v", err)
	}
	s.Close()
	s = openStore(t, dir)
	s.clock = func() time.Time { return now }
	a, units, _, err = s.GWAttackOpen(u[0], g[1], g[2:], "normal", 1, 5, "explicit", now)
	if err != nil || a.BattleID != bid || len(units) == 0 || gwBP(t, s, u[0]) != 230 {
		t.Fatalf("restart replay: %+v %v", a, err)
	}
	if _, err = s.GWAttackPay(u[0], bid); err != nil {
		t.Fatal(err)
	}
	res, err := s.GWAttackSettle(u[0], bid, true, 0)
	// 每目标当场 GP 1000 × 普通 0.75% = 7，三目标合计 21。
	if err != nil || res.Plundered != 21 {
		t.Fatalf("multi plunder: %+v %v", res, err)
	}
	for _, gid := range g[1:] {
		var gp int64
		s.DB().QueryRow(`SELECT gp FROM cn35_gw_participants WHERE guild_id=?`, gid).Scan(&gp)
		if gp != 993 {
			t.Fatalf("target GP=%d", gp)
		}
	}
	res, err = s.GWAttackSettle(u[0], bid, true, 0)
	if err != nil || !res.Already {
		t.Fatal("settle replay")
	}
	if _, _, _, err = s.GWAttackOpen(u[0], g[1], nil, "hard", 1, 5, "", now); err != nil {
		t.Fatalf("new battle after terminal: %v", err)
	}
	if gwBP(t, s, u[0]) != 200 {
		t.Fatal("new battle did not cost 30")
	}
}

func TestGWEffectsFreezeAndEmptyExtraRollback(t *testing.T) {
	s := openStore(t, t.TempDir())
	u, g, now := gwFixture(t, s, 3)
	s.GWHelpPray(u[0], "help", "", now)
	s.GWHelpPray(u[1], "pray", "", now)
	creditBalance(t, s, u[0], "guild_asset", 1000)
	for _, typ := range []string{"sp", "hp", "at", "taghness"} {
		if _, err := s.Invest(u[0], typ, 200, ""); err != nil {
			t.Fatal(err)
		}
	}
	s.DB().Exec(`DELETE FROM cn35_guild_defense WHERE user_id=?`, u[2])
	before := gwBP(t, s, u[0])
	if _, _, _, err := s.GWAttackOpen(u[0], g[1], []string{g[2]}, "normal", 1, 5, "retry", now); !errors.Is(err, cn35game.ErrGWNoDefense) {
		t.Fatal(err)
	}
	if gwBP(t, s, u[0]) != before {
		t.Fatal("empty extra charged")
	}
	if err := s.SetDefense(u[2], 0, "def-"+u[2], ""); err != nil {
		t.Fatal(err)
	}
	a, _, _, err := s.GWAttackOpen(u[0], g[1], []string{g[2]}, "normal", 1, 5, "retry", now)
	// GC-04：效果等级由重建阈值曲线反解（200 point 在曲线上是 1 级，
	// T(2)=17,700），因此初始 SP = 基础 900 + 1×25，体力加成 = 1×0.25%。
	// 期望值全部由曲线/规则常量推导，不写魔法数。
	eqLevel := cn35game.GuildEquipLevel(200)
	if eqLevel != 1 {
		t.Fatalf("曲线自检: 200 point 应为 1 级，got %d", eqLevel)
	}
	wantSP := 900 + eqLevel*cn35game.GuildRules.EquipSPPerLevel
	wantHP := 1 + float64(eqLevel)*cn35game.GuildRules.EquipUnitBonusPerLevel
	if err != nil || a.Effects.InitialSP != wantSP || a.Effects.UnitHP != wantHP || a.DefenseSnapshot[0].HPBonus != 0.02 {
		t.Fatalf("effects: %+v %v", a, err)
	}
	s.GWHelpPray(u[0], "help", "", now)
	s.GWHelpPray(u[1], "pray", "", now)
	a2, _, _, err := s.GWAttackOpen(u[0], g[1], []string{g[2]}, "normal", 1, 5, "retry", now)
	if err != nil || a2.Effects != a.Effects || a2.DefenseSnapshot[0].HPBonus != 0.02 {
		t.Fatalf("not frozen: %+v %v", a2, err)
	}
}

func TestGWClockExpiryAndLiveRanks(t *testing.T) {
	s := openStore(t, t.TempDir())
	u, g, now := gwFixture(t, s, 2)
	// A past date must work exactly as a future date; no wall clock dependency.
	old := now.AddDate(-30, 0, 0)
	s.clock = func() time.Time { return old }
	v, err := s.GWState(u[0], old)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.GWHelpPray(u[1], "help", "", old); err != nil {
		t.Fatal(err)
	}
	v, err = s.GWState(u[0], old)
	if err != nil || v.Participants[0].GuildID != g[1] || v.Participants[0].RankInGroup != 1 || v.Participants[1].RankInGroup != 2 {
		t.Fatalf("live ranks: %+v %v", v, err)
	}
	a, _, _, err := s.GWAttackOpen(u[0], g[1], nil, "normal", 1, 5, "", old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GWAttackPay(u[0], a.BattleID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GWAttackSettle(u[0], a.BattleID, true, 0); err != nil {
		t.Fatalf("injected old clock ignored: %v", err)
	}
	a, _, _, err = s.GWAttackOpen(u[0], g[1], nil, "normal", 1, 5, "", old)
	if err != nil {
		t.Fatal(err)
	}
	later := old.Add(90 * time.Minute)
	s.clock = func() time.Time { return later }
	if _, err = s.GWAttackPay(u[0], a.BattleID); !errors.Is(err, cn35game.ErrGWAttackState) {
		t.Fatal(err)
	}
	state, err := s.GWAttackSession(u[0], a.BattleID)
	if err != nil || state.Status != "abandoned" {
		t.Fatalf("expiry rolled back: %+v %v", state, err)
	}
	v, err = s.GWState(u[0], later)
	if err != nil || v.Session == nil || v.Session.Status != "settled" || v.Actable {
		t.Fatalf("recent results: %+v %v", v, err)
	}
}

// 会战无战斗限时：bp_paid（已进入战斗）的出击在关场后仍可结算，
// 不能被 settleDueSessionsTx 一并废弃。
func TestGWSettleAfterWindowClose(t *testing.T) {
	s := openStore(t, t.TempDir())
	u, g, now := gwFixture(t, s, 2)
	// 窗口内开战并确认 BP（bp_paid=玩家已进入战斗）。
	a, _, _, err := s.GWAttackOpen(u[0], g[1], nil, "normal", 1, 5, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GWAttackPay(u[0], a.BattleID); err != nil {
		t.Fatal(err)
	}
	// 关场后再上报胜利：必须可结算且掠夺生效。
	later := now.Add(90 * time.Minute)
	s.clock = func() time.Time { return later }
	res, err := s.GWAttackSettle(u[0], a.BattleID, true, 0)
	if err != nil {
		t.Fatalf("bp_paid 跨关场结算被拒: %v", err)
	}
	if res == nil || res.GPDelta <= 0 {
		t.Fatalf("no reward: %+v", res)
	}
}
