// battle_test.go STORY350-07 战斗会话/结算持久化验收：建会话、AP 幂等扣点、
// 结算入账（通关计数/进度推进/奖励/经验）、败北/续战、双账号隔离、
// 重启保持、0006 旧库升级、约束白名单。
package cn35store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/database"
)

// battleFixture 建一个可结算的会话样本。
func battleFixture(userID string, questID, areaID int, apCost int) *cn35game.BattleSession {
	return &cn35game.BattleSession{
		BattleID: "bq-test-" + userID + "-" + string(rune('a'+questID)) + "-" + randHex(6),
		UserID:   userID, QuestID: questID, AreaID: areaID, Difficulty: "normal",
		Status: cn35game.BattlePrepared, APCost: apCost,
		Rewards: []cn35game.BattleReward{
			{Key: "gacha_point", Count: 20},
			{Key: "guild_asset", Count: 5},
			{Key: "seed", ID: 1, Count: 1},
			{Key: "power", Count: 90}, // 展示类：不落账
		},
		Exp:         60,
		NextStageID: 1, NextAreaID: 1, NextQuestID: questID + 1,
		Continuable: true,
	}
}

func battleAP(t *testing.T, s *Store, uid string) int {
	t.Helper()
	var ap int
	if err := s.DB().QueryRow(`SELECT ap FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&ap); err != nil {
		t.Fatal(err)
	}
	return ap
}

func battleCoin(t *testing.T, s *Store, uid string) int64 {
	t.Helper()
	var n int64
	_ = s.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='coin'`, uid).Scan(&n)
	return n
}

func balanceOf(t *testing.T, s *Store, uid, currency string) int64 {
	t.Helper()
	var n int64
	_ = s.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency=?`, uid, currency).Scan(&n)
	return n
}

func battleStatus(t *testing.T, s *Store, bid string) string {
	t.Helper()
	var st string
	if err := s.DB().QueryRow(`SELECT status FROM cn35_battle_sessions WHERE battle_id=?`, bid).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

// 建号→execute 建档→ap_use 扣点→result 结算全链（入库断言逐项）。
func TestBattleHappyPath(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-battle-happy")

	sess := battleFixture(uid, 1, 1, 2)
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatalf("OpenBattle: %v", err)
	}
	if battleStatus(t, s, sess.BattleID) != "prepared" {
		t.Fatal("建档应为 prepared")
	}
	// ap_use：扣 2（20→18），重复调用幂等不重复扣。
	paid, err := s.PayBattleAP(uid, sess.BattleID)
	if err != nil || paid.Status != "ap_paid" {
		t.Fatalf("PayBattleAP: %v %+v", err, paid)
	}
	if ap := battleAP(t, s, uid); ap != 18 {
		t.Fatalf("AP 应扣到 18: %d", ap)
	}
	if _, err := s.PayBattleAP(uid, sess.BattleID); err != nil {
		t.Fatalf("重复 ap_use 应幂等: %v", err)
	}
	if ap := battleAP(t, s, uid); ap != 18 {
		t.Fatalf("重复 ap_use 不得重复扣点: %d", ap)
	}
	// result 结算：clear+1、进度推进、奖励入账、exp 入账。
	gpBefore := balanceOf(t, s, uid, "gacha_point")
	res, err := s.SettleBattle(uid, sess.BattleID, cn35game.BattleReport{Time: 32.5, RestMC: 0})
	if err != nil || res.Already {
		t.Fatalf("SettleBattle: %v %+v", err, res)
	}
	if !res.FirstClear || res.ClearCount != 1 {
		t.Fatalf("首通计数: %+v", res)
	}
	if battleStatus(t, s, sess.BattleID) != "cleared" {
		t.Fatal("结算后应为 cleared")
	}
	p, err := s.QuestProgress(uid)
	if err != nil || p.QuestID != 2 {
		t.Fatalf("进度应推进到 quest2: %+v", p)
	}
	if p.Clears[1]["normal"] != 1 {
		t.Fatalf("clear_count 应为 1: %+v", p.Clears)
	}
	if got := balanceOf(t, s, uid, "gacha_point"); got != gpBefore+20 {
		t.Fatalf("gacha_point 入账: %d (结算前 %d)", got, gpBefore)
	}
	if got := balanceOf(t, s, uid, "guild_asset"); got != 5 {
		t.Fatalf("guild_asset 入账: %d", got)
	}
	if len(res.MonsterUUIDs) != 1 {
		t.Fatalf("seed 掉落: %v", res.MonsterUUIDs)
	}
	var mn int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_player_monsters WHERE user_id=? AND monster_id=1`, uid).Scan(&mn); err != nil || mn != 1 {
		t.Fatalf("seed 掉落未入库: n=%d err=%v", mn, err)
	}
	var lvl, exp int
	if err := s.DB().QueryRow(`SELECT level,exp FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&lvl, &exp); err != nil {
		t.Fatal(err)
	}
	if lvl != 2 || exp != 10 { // 初始 50+60=110 → level2 余 10（max_exp=100）
		t.Fatalf("exp/level 结算: level=%d exp=%d", lvl, exp)
	}
	// 重复 result → already_received，零二次入账。
	res2, err := s.SettleBattle(uid, sess.BattleID, cn35game.BattleReport{Time: 33, RestMC: 0})
	if err != nil || !res2.Already {
		t.Fatalf("重复结算应 already_received: %v %+v", err, res2)
	}
	if got := balanceOf(t, s, uid, "gacha_point"); got != gpBefore+20 {
		t.Fatalf("重复结算不得重复入账: %d", got)
	}
	if p2, _ := s.QuestProgress(uid); p2.Clears[1]["normal"] != 1 {
		t.Fatalf("重复结算不得重复计通关: %+v", p2.Clears)
	}
}

// AP 不足：execute 建档被拒；ap_use 并发不足被拒且不产生半扣。
func TestBattleAPGuards(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-battle-ap")
	if _, err := s.DB().Exec(`UPDATE cn35_player_profiles SET ap=1 WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	// 建档时 AP 已不足 → ErrBattleAP。
	if err := s.OpenBattle(battleFixture(uid, 1, 1, 2), 86400); !errors.Is(err, cn35game.ErrBattleAP) {
		t.Fatalf("建档应拒绝 AP 不足: %v", err)
	}
	// 足档但 ap_use 时已不足 → ErrBattleAP，会话保持 prepared。
	if _, err := s.DB().Exec(`UPDATE cn35_player_profiles SET ap=2 WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	sess := battleFixture(uid, 1, 1, 2)
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatalf("OpenBattle: %v", err)
	}
	if _, err := s.DB().Exec(`UPDATE cn35_player_profiles SET ap=0 WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PayBattleAP(uid, sess.BattleID); !errors.Is(err, cn35game.ErrBattleAP) {
		t.Fatalf("ap_use 应拒绝: %v", err)
	}
	if battleStatus(t, s, sess.BattleID) != "prepared" {
		t.Fatal("扣点失败会话应仍为 prepared")
	}
}

// lose→continue：escaped=true 的败北可续；扣 1 钻；continued 后可 result。
func TestBattleLoseContinue(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-battle-lose")
	if _, err := s.DB().Exec(`UPDATE cn35_player_balances SET amount=3 WHERE user_id=? AND currency='coin'`, uid); err != nil {
		t.Fatal(err)
	}
	sess := battleFixture(uid, 1, 1, 2)
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PayBattleAP(uid, sess.BattleID); err != nil {
		t.Fatal(err)
	}
	lost, err := s.LoseBattle(uid, sess.BattleID, cn35game.BattleReport{
		Time: 99.5, RestMC: 3, Escaped: true,
		Damage: map[string]int64{"3": 1234},
	})
	if err != nil || lost.Status != "lost" {
		t.Fatalf("LoseBattle: %v %+v", err, lost)
	}
	// 首败标志落库 → Snapshot.first_flag.failure=1。
	snap, err := s.Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	ff, _ := snap.User["first_flag"].(map[string]any)
	if ff["failure"] != int64(1) {
		t.Fatalf("first_flag.failure 应置 1: %v", snap.User["first_flag"])
	}
	// 续战扣 1 钻。
	cont, coin, err := s.ContinueBattle(uid)
	if err != nil {
		t.Fatalf("ContinueBattle: %v", err)
	}
	if cont.Status != "continued" || cont.ContinueCount != 1 || coin != 2 {
		t.Fatalf("续战结果: %+v coin=%d", cont, coin)
	}
	// 续战后再 result 正常结算。
	res, err := s.SettleBattle(uid, sess.BattleID, cn35game.BattleReport{Time: 200, RestMC: 0})
	if err != nil || res.Already || !res.FirstClear {
		t.Fatalf("续战后结算: %v %+v", err, res)
	}
	// 无 lost 会话 → ErrNoContinuable；钻不足 → ErrBattleCoin。
	if _, _, err := s.ContinueBattle(uid); !errors.Is(err, cn35game.ErrNoContinuable) {
		t.Fatalf("无可续会话应 ErrNoContinuable: %v", err)
	}
	sess2 := battleFixture(uid, 2, 1, 2)
	sess2.BattleID = "bq-lose2"
	if err := s.OpenBattle(sess2, 86400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PayBattleAP(uid, sess2.BattleID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoseBattle(uid, sess2.BattleID, cn35game.BattleReport{Escaped: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE cn35_player_balances SET amount=0 WHERE user_id=? AND currency='coin'`, uid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ContinueBattle(uid); !errors.Is(err, cn35game.ErrBattleCoin) {
		t.Fatalf("钻不足应 ErrBattleCoin: %v", err)
	}
}

// 主动放弃（无 escaped）→ 会话不可续；continue 报无可续。
func TestBattleEscapeNotContinuable(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-battle-esc")
	sess := battleFixture(uid, 1, 1, 2)
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PayBattleAP(uid, sess.BattleID); err != nil {
		t.Fatal(err)
	}
	lost, err := s.LoseBattle(uid, sess.BattleID, cn35game.BattleReport{Escaped: false})
	if err != nil || lost.Continuable {
		t.Fatalf("主动放弃应不可续: %v %+v", err, lost)
	}
	if _, _, err := s.ContinueBattle(uid); !errors.Is(err, cn35game.ErrNoContinuable) {
		t.Fatalf("Escape 会话不得可续: %v", err)
	}
}

// 双账号隔离：B 对 A 的 bid 全部 ErrBattleNotFound（会话/AP/结算/败北）。
func TestBattleAccountIsolation(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uidA := questUserID(t, s, "dev-battle-iso-A")
	uidB := questUserID(t, s, "dev-battle-iso-B")
	sess := battleFixture(uidA, 1, 1, 2)
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func() error{
		func() error { _, e := s.BattleSession(uidB, sess.BattleID); return e },
		func() error { _, e := s.PayBattleAP(uidB, sess.BattleID); return e },
		func() error { _, e := s.SettleBattle(uidB, sess.BattleID, cn35game.BattleReport{}); return e },
		func() error { _, e := s.LoseBattle(uidB, sess.BattleID, cn35game.BattleReport{}); return e },
	} {
		if err := fn(); !errors.Is(err, cn35game.ErrBattleNotFound) {
			t.Fatalf("跨账号应 ErrBattleNotFound: %v", err)
		}
	}
	// B 的 continue 也捞不到 A 的 lost 会话。
	if _, _, err := s.ContinueBattle(uidB); !errors.Is(err, cn35game.ErrNoContinuable) {
		t.Fatalf("B 无可续会话: %v", err)
	}
	// A 的会话未被污染。
	if battleStatus(t, s, sess.BattleID) != "prepared" {
		t.Fatal("跨账号调用不得改动 A 的会话")
	}
}

// 同关同难度重复 execute 顶替旧会话；顶替后的会话不可再结算。
func TestBattleSupersede(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-battle-super")
	s1 := battleFixture(uid, 1, 1, 2)
	if err := s.OpenBattle(s1, 86400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PayBattleAP(uid, s1.BattleID); err != nil {
		t.Fatal(err)
	}
	s2 := battleFixture(uid, 1, 1, 2)
	s2.BattleID = "bq-super-2"
	if err := s.OpenBattle(s2, 86400); err != nil {
		t.Fatal(err)
	}
	if battleStatus(t, s, s1.BattleID) != "abandoned" {
		t.Fatalf("同关再 execute 应顶替旧会话: %s", battleStatus(t, s, s1.BattleID))
	}
	// 被顶替会话 result → already_received（不重复发奖）。
	res, err := s.SettleBattle(uid, s1.BattleID, cn35game.BattleReport{})
	if err != nil || !res.Already {
		t.Fatalf("顶替会话结算应 already_received: %v %+v", err, res)
	}
}

// 跳过 ap_use 的 result：结算同事务补扣 AP（恰好一次）。
func TestBattleSettlePaysMissingAP(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-battle-skipap")
	sess := battleFixture(uid, 1, 1, 2)
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatal(err)
	}
	res, err := s.SettleBattle(uid, sess.BattleID, cn35game.BattleReport{Time: 10})
	if err != nil || res.Already {
		t.Fatalf("prepared 会话结算应补扣并落账: %v %+v", err, res)
	}
	// 升级 AP 叠加新等级完整上限（允许溢出）：补扣 2 AP（20→18）
	// 后 exp 60 → level2（叠加 +21）→ ap=39。
	if ap := battleAP(t, s, uid); ap != 39 {
		t.Fatalf("补扣 AP 应恰一次: %d", ap)
	}
}

// log_battle 归档（fire-and-forget 可重放）。
func TestBattleLogBattle(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-battle-log")
	if err := s.LogBattle(uid, "", 1, `{"battleId":1,"waves":[]}`); err != nil {
		t.Fatal(err)
	}
	if err := s.LogBattle(uid, "", 1, `{"battleId":1,"waves":[]}`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_battle_logs WHERE user_id=?`, uid).Scan(&n); err != nil || n != 2 {
		t.Fatalf("战报归档: n=%d err=%v", n, err)
	}
}

// 0006 旧库升级 0007：战斗三表就位、存量账号/进度不受影响。
func TestBattleUpgradeFrom0006(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mercd35.sqlite3")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	applyThrough0005(t, db)
	// 0006 手工补放（与真实升级路径一致：全量 SQL + 台账）。
	raw6, err := os.ReadFile(filepath.Join("..", "database", "migrations", "0006_cn35_quest_progress.sql"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(string(raw6)); err != nil {
		t.Fatalf("exec 0006: %v", err)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(raw6))
	if _, err = tx.Exec(`INSERT INTO schema_migrations VALUES (?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		"0006_cn35_quest_progress.sql", sum); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cn35_accounts
		(he_account_id,user_id,token,sk,user_name,account_name,password_hash,sdk_account_id,device_id,is_new_user,tutorial_finish,last_server_id,created_at,updated_at)
		VALUES ('he-b','uid-b','tok','sk','玩家乙','','','','dev-b',0,1,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cn35_player_profiles
		(user_id,level,exp,max_exp,ap,max_ap,ap_updated_at,bp,max_bp,leader_unit_id,login_bonus_receivable_sec,guild_name_snapshot,created_at,updated_at)
		VALUES ('uid-b',5,0,100,20,20,'2026-01-01T00:00:00Z',300,300,'348',0,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cn35_player_balances (user_id,currency,amount,updated_at)
		VALUES ('uid-b','coin',2,'2026-01-01T00:00:00Z'),('uid-b','gacha_point',0,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openStore(t, dir) // Open 内应用 0007
	var n int
	for _, tbl := range []string{"cn35_battle_sessions", "cn35_battle_logs", "cn35_player_flags"} {
		if err := s.DB().QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&n); err != nil {
			t.Fatalf("升级后缺表 %s: %v", tbl, err)
		}
	}
	// 旧账号全链仍工作（含续战）。
	if _, err := s.DB().Exec(`UPDATE cn35_quest_progress SET quest_id=1 WHERE user_id='uid-b'`); err != nil {
		t.Fatal(err)
	}
	sess := battleFixture("uid-b", 1, 1, 2)
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatalf("旧账号建档: %v", err)
	}
	if _, err := s.PayBattleAP("uid-b", sess.BattleID); err != nil {
		t.Fatalf("旧账号扣点: %v", err)
	}
	res, err := s.SettleBattle("uid-b", sess.BattleID, cn35game.BattleReport{Time: 5})
	if err != nil || res.Already {
		t.Fatalf("旧账号结算: %v %+v", err, res)
	}
}

// 重启持久化：会话/结算/首败标志/余额跨 Store 重开保持。
func TestBattleRestartPersistence(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-battle-restart")
	sess := battleFixture(uid, 1, 1, 2)
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PayBattleAP(uid, sess.BattleID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleBattle(uid, sess.BattleID, cn35game.BattleReport{Time: 3}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2 := openStore(t, dir)
	defer s2.Close()
	// 终态会话重放仍 already_received（台账不丢）。
	res, err := s2.SettleBattle(uid, sess.BattleID, cn35game.BattleReport{Time: 3})
	if err != nil || !res.Already {
		t.Fatalf("重启后重放应 already_received: %v %+v", err, res)
	}
	p, _ := s2.QuestProgress(uid)
	if p.QuestID != 2 || p.Clears[1]["normal"] != 1 {
		t.Fatalf("重启后进度/通关丢失: %+v", p)
	}
	// 升级 AP 叠加新等级完整上限（允许溢出）：level2 → ap=39（扣费 18 后 +21）。
	if battleAP(t, s2, uid) != 39 || balanceOf(t, s2, uid, "gacha_point") != 302+20 {
		t.Fatal("重启后资源余额丢失")
	}
}

// 约束：非法状态/难度/负计数被 CHECK 拒；无账号会话被 FK 拒。
func TestBattleTableConstraints(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := questUserID(t, s, "dev-battle-c")
	if _, err := s.DB().Exec(`INSERT INTO cn35_battle_sessions
		(battle_id,user_id,quest_id,area_id,difficulty,status,ap_cost,next_stage_id,next_area_id,next_quest_id,created_at,updated_at)
		VALUES ('x',?,1,1,'impossible','prepared',0,1,1,2,'t','t')`, uid); err == nil {
		t.Fatal("非法难度应被 CHECK 拒绝")
	}
	if _, err := s.DB().Exec(`INSERT INTO cn35_battle_sessions
		(battle_id,user_id,quest_id,area_id,difficulty,status,ap_cost,next_stage_id,next_area_id,next_quest_id,created_at,updated_at)
		VALUES ('x',?,1,1,'normal','bogus',0,1,1,2,'t','t')`, uid); err == nil {
		t.Fatal("非法状态应被 CHECK 拒绝")
	}
	if _, err := s.DB().Exec(`INSERT INTO cn35_battle_sessions
		(battle_id,user_id,quest_id,area_id,difficulty,status,ap_cost,next_stage_id,next_area_id,next_quest_id,created_at,updated_at)
		VALUES ('x','ghost',1,1,'normal','prepared',0,1,1,2,'t','t')`); err == nil {
		t.Fatal("无账号会话应被 FK 拒绝")
	}
}
