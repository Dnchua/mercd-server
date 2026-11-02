// store_test.go CN350-09 持久化验收：空库建到最新版、真实旧 schema 升级、
// 重启恢复、设备幂等、双账号隔离、token 轮换、受控错误与失败回滚。
package cn35store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/database"
)

func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(filepath.Join(dir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// applyLegacy 手工回放 0001/0002 + 台账行，模拟 0003 之前的真实旧库。
func applyLegacy(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_accounts.sql", "0002_player_core.sql"} {
		raw, err := os.ReadFile(filepath.Join("..", "database", "migrations", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err = tx.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("exec %s: %v", name, err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(raw))
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations VALUES (?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, name, sum); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// wantMigrations 当前主线迁移条数与最新迁移名（schema 版本断言真源）。
// 新增迁移时同步更新此处：数字 + 最新文件名。
//
// 2026-10-28 GUILDCLOSE350 批次：0038_cn35_guild_bp_storage（T3）、
// 迁移条数与最新迁移名改为从内嵌迁移目录动态取（GUILDCLOSE350 实施轮：
// 多人并行追加 0038—0041，硬编码常量会互相踩）。哨兵语义不变：
// 库内 schema_migrations 必须与内嵌迁移集合逐条一致、且无遗漏。
func wantMigrationSet(t *testing.T) (int, string) {
	t.Helper()
	names, err := database.EmbeddedMigrationNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("内嵌迁移为空")
	}
	return len(names), names[len(names)-1]
}

// assertLatestSchema 断言库已应用到当前最新迁移（数量 + 最新名称）。
func assertLatestSchema(t *testing.T, s *Store) {
	t.Helper()
	wantN, wantLast := wantMigrationSet(t)
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != wantN {
		t.Fatalf("migrations applied = %d, want %d", n, wantN)
	}
	var last string
	if err := s.DB().QueryRow(`SELECT name FROM schema_migrations ORDER BY name DESC LIMIT 1`).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if last != wantLast {
		t.Fatalf("最新迁移 = %s, want %s", last, wantLast)
	}
}

func TestEmptyDBCreatesLatestSchema(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	assertLatestSchema(t, s)
	for _, tbl := range []string{"cn35_accounts", "cn35_player_profiles", "cn35_player_balances", "cn35_sessions",
		"cn35_player_units", "cn35_player_monsters", "cn35_inventory_grants",
		"cn35_player_decks", "cn35_player_deck_slots",
		"cn35_quest_progress", "cn35_quest_clears",
		"cn35_battle_sessions", "cn35_battle_logs", "cn35_player_flags",
		"cn35_item_catalog", "cn35_player_items",
		"cn35_resource_quest_drafts", "cn35_resource_quest_drops",
		"cn35_resource_quest_waves", "cn35_medal_goods_drafts",
		"cn35_resource_config_versions", "cn35_resource_config_release",
		"cn35_resource_reward_events", "cn35_medal_purchase_totals", "cn35_medal_grants"} {
		if err := s.DB().QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&n); err != nil {
			t.Fatalf("%s 不存在: %v", tbl, err)
		}
	}
	// cn15 旧表同库共存但为空——版本隔离成立。
	if err := s.DB().QueryRow(`SELECT count(*) FROM accounts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("cn15 accounts 应为空表，got %d", n)
	}
}

func TestUpgradeFromLegacySchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mercd35.sqlite3")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	applyLegacy(t, db)
	// 旧库代表性 cn15 账号行：升级后必须原样保留且不被 cn35 初始化触碰。
	if _, err = db.Exec(`INSERT INTO accounts (he_account_id,user_id,encrypted_user_id,token,sk,user_name,password_hash,is_new_user,tutorial_finish,created_at,updated_at)
		VALUES ('old-he','old-uid','enc-old','tok','sk','旧玩家','',0,1,'2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openStore(t, dir)
	var cnt int
	if err := s.DB().QueryRow(`SELECT count(*) FROM accounts WHERE he_account_id='old-he'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatal("旧账号行丢失")
	}
	// cn15 触发器灌入的 1.5 种子 profile 留在旧表，cn35 表未受影响。
	if err := s.DB().QueryRow(`SELECT leader_unit_id FROM player_profiles WHERE user_id='old-uid'`).Scan(new(string)); err != nil {
		t.Fatal(err)
	}
	// 新库可正常建号且不触碰旧表。
	res, err := s.QuickLogin("dev-upgrade")
	if err != nil || !res.New {
		t.Fatalf("QuickLogin: %v new=%v", err, res.New)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM accounts`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("cn15 accounts 不应被 cn35 建号写入，got %d", cnt)
	}
}

func TestQuickLoginDeviceIdempotentAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	r1, err := s.QuickLogin("device-A")
	if err != nil || !r1.New {
		t.Fatalf("首登: %v new=%v", err, r1.New)
	}
	s.Close()

	// “重启”：同一文件重新打开（换 Store 实例）。
	s2 := openStore(t, dir)
	defer s2.Close()
	r2, err := s2.QuickLogin("device-A")
	if err != nil {
		t.Fatal(err)
	}
	if r2.New {
		t.Fatal("同设备重启后不应再建新账号")
	}
	if r2.HeAccountID != r1.HeAccountID {
		t.Fatalf("heAccountId 未保持: %s != %s", r2.HeAccountID, r1.HeAccountID)
	}
	if r2.Token == r1.Token {
		t.Fatal("token 应轮换")
	}
	// 旧 token 必须失效。
	if _, _, err = s2.ChooseServer(r1.HeAccountID, r1.Token, "1"); !errors.Is(err, cn35game.ErrBadCredential) {
		t.Fatalf("旧 token 应被拒，got %v", err)
	}
	if _, _, err = s2.ChooseServer(r2.HeAccountID, r2.Token, "1"); err != nil {
		t.Fatal(err)
	}
}

func TestTwoAccountsIsolation(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	r1, _ := s.QuickLogin("device-A")
	r2, _ := s.QuickLogin("device-B")
	if r1.HeAccountID == r2.HeAccountID {
		t.Fatal("两设备账号撞号")
	}
	u1, _, err := s.ChooseServer(r1.HeAccountID, r1.Token, "1")
	if err != nil {
		t.Fatal(err)
	}
	u2, _, err := s.ChooseServer(r2.HeAccountID, r2.Token, "1")
	if err != nil {
		t.Fatal(err)
	}
	if u1 == u2 {
		t.Fatal("两账号 userId 相同")
	}
	if err = s.UsersUpdate(u1, "旅者甲", false); err != nil {
		t.Fatal(err)
	}
	snap2, err := s.Snapshot(u2)
	if err != nil {
		t.Fatal(err)
	}
	if snap2.User["user_name"] == "旅者甲" || snap2.User["name"] == "旅者甲" {
		t.Fatal("账号 B 快照被 A 污染")
	}
	snap1, _ := s.Snapshot(u1)
	if snap1.User["user_name"] != "旅者甲" {
		t.Fatalf("账号 A 昵称未生效: %v", snap1.User["user_name"])
	}
}

// TestGuestAndRegisterShareDevice 回归 CN350-12 实机发现：0003 的
// cn35_accounts_device_key 对全部 device_id<>” 行唯一，同机 heRegister
// 必撞索引（客户端"登录失败"）。0004 起设备唯一仅约束纯游客行。
func TestGuestAndRegisterShareDevice(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	defer s.Close()

	guest, err := s.QuickLogin("dev-shared")
	if err != nil || !guest.New {
		t.Fatalf("游客建号: %v", err)
	}
	// 同机两个 HE 注册号都必须成功（验收判据：两个独立新账号）。
	rb, err := s.Register("acct-bbb", "pw-b", "dev-shared", "uc", "")
	if err != nil || !rb.New {
		t.Fatalf("同机注册 B: %v", err)
	}
	rc, err := s.Register("acct-ccc", "pw-c", "dev-shared", "uc", "")
	if err != nil || !rc.New {
		t.Fatalf("同机注册 C: %v", err)
	}
	if rb.HeAccountID == guest.HeAccountID || rc.HeAccountID == guest.HeAccountID || rb.HeAccountID == rc.HeAccountID {
		t.Fatal("账号标识撞车")
	}
	// 游客复登仍解析回游客行，不得被同机注册号劫持。
	back, err := s.QuickLogin("dev-shared")
	if err != nil || back.New || back.HeAccountID != guest.HeAccountID {
		t.Fatalf("游客复登错位: %+v err=%v", back, err)
	}
	// 注册号 chooseServer + 快照与游客隔离。
	ub, _, err := s.ChooseServer(rb.HeAccountID, rb.Token, "1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UsersUpdate(ub, "乙名", false); err != nil {
		t.Fatal(err)
	}
	ug, _, err := s.ChooseServer(guest.HeAccountID, back.Token, "1")
	if err != nil {
		t.Fatal(err)
	}
	snapG, err := s.Snapshot(ug)
	if err != nil {
		t.Fatal(err)
	}
	if snapG.User["user_name"] == "乙名" {
		t.Fatal("游客快照被注册号污染")
	}
}

func TestRegisterLoginAndNameRules(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	if !s.NameAvailable("heroine") {
		t.Fatal("新名应可注册")
	}
	r, err := s.Register("heroine", "pw-hash-from-client", "dev-1", "uc", "")
	if err != nil || !r.New {
		t.Fatalf("Register: %v", err)
	}
	if s.NameAvailable("heroine") {
		t.Fatal("已注册名应判占用")
	}
	if _, err = s.Register("heroine", "x", "dev-2", "uc", ""); !errors.Is(err, cn35game.ErrNameTaken) {
		t.Fatalf("重名注册应 ErrNameTaken，got %v", err)
	}
	if _, err = s.Login("heroine", "wrong-pwd", "dev-1"); !errors.Is(err, cn35game.ErrBadCredential) {
		t.Fatalf("错密码应 ErrBadCredential，got %v", err)
	}
	l, err := s.Login("heroine", "pw-hash-from-client", "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if l.HeAccountID != r.HeAccountID || l.New {
		t.Fatalf("复登应回同一账号且 New=false: %+v", l)
	}
	if _, err = s.Login("nobody", "x", "dev-1"); !errors.Is(err, cn35game.ErrNoAccount) {
		t.Fatalf("不存在账号应 ErrNoAccount，got %v", err)
	}
}

func TestSnapshotSeedsAndTutorialGate(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	r, _ := s.QuickLogin("dev-snap")
	uid, _, _ := s.ChooseServer(r.HeAccountID, r.Token, "1")
	snap, err := s.Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	u := snap.User
	checks := map[string]any{
		"level": int64(1), "exp": int64(50), "max_exp": int64(100),
		"ap": int64(20), "max_ap": int64(20), "bp": int64(300), "max_bp": int64(300),
		"leader_unit_id": "348", "gacha_point": int64(302), "name": "旅人",
		"quest_id": 1, // quest0 实测：IsTutorialUser/Home 引导门槛字段
	}
	for k, want := range checks {
		if u[k] != want {
			t.Fatalf("snapshot.user.%s = %v, want %v", k, u[k], want)
		}
	}
	if _, ok := u["game_status"]; ok {
		t.Fatal("教程中不得下发 game_status")
	}
	if amt := snap.UserCoin["amount"]; amt != int64(0) {
		t.Fatalf("初始 coin = %v, want 0", amt)
	}
	if err = s.UsersUpdate(uid, "", true); err != nil {
		t.Fatal(err)
	}
	snap2, _ := s.Snapshot(uid)
	if snap2.User["game_status"] != 2 {
		t.Fatalf("完成后 game_status 应为 2: %v", snap2.User["game_status"])
	}
	// 幂等：重复置位不报错。
	if err = s.UsersUpdate(uid, "", true); err != nil {
		t.Fatal(err)
	}
}

func TestSessionPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	sess := cn35wire.Session{UserID: "u1", SK: "sk1", S0: "0123456789abcdef0123456789abcdef", S1: "fedcba9876543210fedcba9876543210"}
	if err := s.Save(sess); err == nil {
		t.Fatal("会话不应挂在不存在的账号上（FK 约束）")
	}
	r, _ := s.QuickLogin("dev-sess")
	uid, sk, _ := s.ChooseServer(r.HeAccountID, r.Token, "1")
	sess.UserID, sess.SK = uid, sk
	if err := s.Save(sess); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2 := openStore(t, dir)
	defer s2.Close()
	got, err := s2.Load(uid)
	if err != nil {
		t.Fatalf("重启后会话丢失: %v", err)
	}
	if got.S0 != sess.S0 || got.S1 != sess.S1 || got.SK != sk {
		t.Fatal("会话字段不一致")
	}
	if _, err = s2.Load("nobody"); !errors.Is(err, cn35wire.ErrNoSession) {
		t.Fatalf("无会话应 ErrNoSession，got %v", err)
	}
	// chooseServer 重进覆盖会话（轮换 S0/S1）。
	sess.S0 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err = s2.Save(sess); err != nil {
		t.Fatal(err)
	}
	got, _ = s2.Load(uid)
	if got.S0 != sess.S0 {
		t.Fatal("会话覆盖未生效")
	}
}

func TestLastServerPersisted(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	r, _ := s.QuickLogin("dev-ls")
	if got := s.LastServer(r.HeAccountID); got != "" {
		t.Fatalf("新账号 last_server 应空，got %q", got)
	}
	if _, _, err := s.ChooseServer(r.HeAccountID, r.Token, "7"); err != nil {
		t.Fatal(err)
	}
	if got := s.LastServer(r.HeAccountID); got != "7" {
		t.Fatalf("last_server = %q, want 7", got)
	}
	s.Close()
	s2 := openStore(t, dir)
	defer s2.Close()
	if got := s2.LastServer(r.HeAccountID); got != "7" {
		t.Fatalf("重启后 last_server = %q", got)
	}
}

func TestFailedRegisterLeavesNoRows(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	tables := []string{"cn35_accounts", "cn35_player_profiles", "cn35_player_balances"}
	before := map[string]int{}
	for _, tbl := range tables {
		var c int
		if err := s.DB().QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&c); err != nil {
			t.Fatal(err)
		}
		before[tbl] = c
	}
	_, err := s.Register("", "pw", "dev-x", "uc", "")
	if !errors.Is(err, cn35game.ErrBadCredential) {
		t.Fatalf("空名注册应 ErrBadCredential，got %v", err)
	}
	var n int
	for _, tbl := range tables {
		if err = s.DB().QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != before[tbl] {
			t.Fatalf("%s 残留 %d 行（want %d，失败应整体回滚）", tbl, n, before[tbl])
		}
	}
}

func TestBuiltinTest001AccountSeededByMigration(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	r, err := s.Login("test001", "test001", "")
	if err != nil {
		t.Fatalf("Login test001: %v", err)
	}
	uid, _, err := s.ChooseServer(r.HeAccountID, r.Token, "1")
	if err != nil || uid != "681abf0d7485" {
		t.Fatalf("ChooseServer test001: uid=%s err=%v", uid, err)
	}
	snap, err := s.Snapshot(uid)
	if err != nil {
		t.Fatalf("Snapshot test001: %v", err)
	}
	if snap.User["level"] != int64(100) || snap.UserCoin["amount"] != int64(2000) || snap.User["game_status"] != 2 {
		t.Fatalf("unexpected test001 snapshot: level=%v coin=%v game_status=%v",
			snap.User["level"], snap.UserCoin["amount"], snap.User["game_status"])
	}
	units, err := s.Units(uid)
	if err != nil || len(units) != 1251 {
		t.Fatalf("test001 units=%d err=%v, want 1251", len(units), err)
	}
	monsters, err := s.Monsters(uid)
	if err != nil || len(monsters) != 235 {
		t.Fatalf("test001 monsters=%d err=%v, want 235", len(monsters), err)
	}
}

// TestUsersUpdateTxSemantics users/update 事务语义：
// 昵称+完成同事务、占用名整笔回滚（不留 finish 半写）、本号同名可用、重启持久。
func TestUsersUpdateTxSemantics(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	r1, _ := s.QuickLogin("dev-tx-A")
	r2, _ := s.QuickLogin("dev-tx-B")
	u1, _, _ := s.ChooseServer(r1.HeAccountID, r1.Token, "1")
	u2, _, _ := s.ChooseServer(r2.HeAccountID, r2.Token, "1")

	// A 完成 End 步（name+finish 一笔）
	if err := s.UsersUpdate(u1, "旅人甲", true); err != nil {
		t.Fatal(err)
	}
	// 本号同名复查可用（断线重试）
	if !s.NicknameAvailable(u1, "旅人甲") {
		t.Fatal("本人已持有名应可用")
	}
	// B 占用检查与整笔回滚
	if s.NicknameAvailable(u2, "旅人甲") {
		t.Fatal("他人已占名应不可用")
	}
	if err := s.UsersUpdate(u2, "旅人甲", true); !errors.Is(err, cn35game.ErrNameTaken) {
		t.Fatalf("占用名写应 ErrNameTaken，got %v", err)
	}
	snapB, _ := s.Snapshot(u2)
	if _, bad := snapB.User["game_status"]; bad {
		t.Fatal("失败事务不得留下 tutorial_finish 半写")
	}
	// 不存在账号
	if err := s.UsersUpdate("ghost", "x", true); !errors.Is(err, cn35game.ErrNoAccount) {
		t.Fatalf("无账号应 ErrNoAccount，got %v", err)
	}

	s.Close()
	s2 := openStore(t, dir)
	defer s2.Close()
	snap, err := s2.Snapshot(u1)
	if err != nil {
		t.Fatal(err)
	}
	if snap.User["game_status"] != 2 || snap.User["user_name"] != "旅人甲" {
		t.Fatalf("重启后教程终态/昵称未持久: %v", snap.User)
	}
}

// TestAccountBindGuestUpgrade 游客转正：就地写账密、heAccountId/user_id/进度
// 全部保留、token 轮换；转正行脱离游客谓词 → 同设备 QuickLogin 产出全新游客；
// 重启后账密可 heLogin。
func TestAccountBindGuestUpgrade(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)

	g, err := s.QuickLogin("dev-bind")
	if err != nil || !g.New {
		t.Fatalf("游客建号: %v", err)
	}
	guestUID, _, err := s.ChooseServer(g.HeAccountID, g.Token, "1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UsersUpdate(guestUID, "转正前昵称", true); err != nil {
		t.Fatal(err)
	}

	b, err := s.AccountBind(g.HeAccountID, g.Token, "hero-up", "pw-up", "dev-bind", "uc", "u@x.y")
	if err != nil {
		t.Fatalf("转正: %v", err)
	}
	if b.New || b.HeAccountID != g.HeAccountID {
		t.Fatalf("转正必须保留原 heAccountId 且非新建: %+v", b)
	}
	if b.Token == "" || b.Token == g.Token {
		t.Fatal("转正应轮换 token")
	}
	// user_id 与教程终态/昵称保留
	uid, _, err := s.ChooseServer(g.HeAccountID, b.Token, "1")
	if err != nil {
		t.Fatal(err)
	}
	if uid != guestUID {
		t.Fatalf("转正改变了 user_id: %s -> %s", guestUID, uid)
	}
	snap, err := s.Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	if snap.User["user_name"] != "转正前昵称" || snap.User["game_status"] != 2 {
		t.Fatalf("转正丢进度: %v", snap.User)
	}
	// 账密可 heLogin；占用判定生效
	l, err := s.Login("hero-up", "pw-up", "dev-bind")
	if err != nil || l.HeAccountID != g.HeAccountID {
		t.Fatalf("转正后 heLogin: %+v err=%v", l, err)
	}
	if s.NameAvailable("hero-up") {
		t.Fatal("转正名应判占用")
	}
	// 同设备再 QuickLogin 产出全新游客（转正行不再满足 device 谓词且不撞索引）
	ng, err := s.QuickLogin("dev-bind")
	if err != nil || !ng.New || ng.HeAccountID == g.HeAccountID {
		t.Fatalf("转正后同设备游客应为全新账号: %+v err=%v", ng, err)
	}
	// 客户端重试（同名同密）走继承分支幂等回本账号
	re, err := s.AccountBind(ng.HeAccountID, ng.Token, "hero-up", "pw-up", "dev-bind", "uc", "")
	if err != nil || re.HeAccountID != g.HeAccountID {
		t.Fatalf("绑定重试应回本账号: %+v err=%v", re, err)
	}
	s.Close()

	// 重启恢复：转正结果与账密在库，heLogin 命中同一账号。
	s2 := openStore(t, dir)
	defer s2.Close()
	l2, err := s2.Login("hero-up", "pw-up", "dev-bind")
	if err != nil || l2.HeAccountID != g.HeAccountID {
		t.Fatalf("重启后 heLogin: %+v err=%v", l2, err)
	}
}

// TestAccountBindTakeOver 换机继承：新设备游客凭据合法，提交已注册 HE 账密
// → 返回被继承账号身份（user_id 连续）；新设备游客号不被吞并。
func TestAccountBindTakeOver(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	defer s.Close()

	reg, err := s.Register("old-acc", "old-pw", "dev-A", "uc", "")
	if err != nil || !reg.New {
		t.Fatalf("注册: %v", err)
	}
	oldUID, _, err := s.ChooseServer(reg.HeAccountID, reg.Token, "1")
	if err != nil {
		t.Fatal(err)
	}

	nb, err := s.QuickLogin("dev-B")
	if err != nil || !nb.New {
		t.Fatalf("新设备游客: %v", err)
	}
	b, err := s.AccountBind(nb.HeAccountID, nb.Token, "old-acc", "old-pw", "dev-B", "uc", "")
	if err != nil {
		t.Fatalf("继承: %v", err)
	}
	if b.HeAccountID != reg.HeAccountID || b.New {
		t.Fatalf("继承应返回被继承账号且非新建: %+v want he=%s", b, reg.HeAccountID)
	}
	uid, _, err := s.ChooseServer(b.HeAccountID, b.Token, "1")
	if err != nil {
		t.Fatal(err)
	}
	if uid != oldUID {
		t.Fatalf("继承改变了 user_id: %s -> %s", oldUID, uid)
	}
	// 新设备游客号仍为未绑定游客，未被吞并
	gb, err := s.QuickLogin("dev-B")
	if err != nil || gb.New || gb.HeAccountID != nb.HeAccountID {
		t.Fatalf("新设备游客应保留: %+v err=%v", gb, err)
	}
	// 空凭据（未快速登录设备）同样可凭账密继承
	b2, err := s.AccountBind("", "", "old-acc", "old-pw", "dev-C", "uc", "")
	if err != nil || b2.HeAccountID != reg.HeAccountID {
		t.Fatalf("空凭据继承: %+v err=%v", b2, err)
	}
}

// TestAccountBindErrors 受控错误面：错密继承、幽灵/坏凭据转正、空名、
// 已绑重绑；同名同密重发幂等成功。
func TestAccountBindErrors(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	defer s.Close()

	g, err := s.QuickLogin("dev-err")
	if err != nil || !g.New {
		t.Fatal(err)
	}
	if _, err = s.Register("taken", "pw-t", "dev-x", "uc", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AccountBind(g.HeAccountID, g.Token, "taken", "wrong", "dev-err", "uc", ""); !errors.Is(err, cn35game.ErrBadCredential) {
		t.Fatalf("错密继承应 ErrBadCredential: %v", err)
	}
	if _, err = s.AccountBind("he-ghost", g.Token, "fresh-name", "pw", "dev-err", "uc", ""); !errors.Is(err, cn35game.ErrNoAccount) {
		t.Fatalf("幽灵账号转正应 ErrNoAccount: %v", err)
	}
	if _, err = s.AccountBind(g.HeAccountID, "bad-token", "fresh-name", "pw", "dev-err", "uc", ""); !errors.Is(err, cn35game.ErrBadCredential) {
		t.Fatalf("坏 token 转正应 ErrBadCredential: %v", err)
	}
	if _, err = s.AccountBind(g.HeAccountID, g.Token, "", "pw", "dev-err", "uc", ""); !errors.Is(err, cn35game.ErrBadCredential) {
		t.Fatalf("空名应 ErrBadCredential: %v", err)
	}
	// 转正后换绑新名 → ErrAlreadyBound；同名同密重发 → 幂等回本账号
	b, err := s.AccountBind(g.HeAccountID, g.Token, "first-name", "pw-1", "dev-err", "uc", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AccountBind(g.HeAccountID, b.Token, "second-name", "pw-2", "dev-err", "uc", ""); !errors.Is(err, cn35game.ErrAlreadyBound) {
		t.Fatalf("已绑重绑应 ErrAlreadyBound: %v", err)
	}
	again, err := s.AccountBind(g.HeAccountID, b.Token, "first-name", "pw-1", "dev-err", "uc", "")
	if err != nil || again.HeAccountID != g.HeAccountID {
		t.Fatalf("同名重绑应幂等: %+v err=%v", again, err)
	}
}

// TestUpdateMemoPersistence 0044 签名持久化：默认下发“请多多指教！”、
// UpdateMemo 写入后重启恢复、空串提交不覆盖既有签名、双玩家隔离与幽灵号拒绝。
func TestUpdateMemoPersistence(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)

	r1, err := s.QuickLogin("dev-memo-1")
	if err != nil {
		t.Fatal(err)
	}
	u1, _, err := s.ChooseServer(r1.HeAccountID, r1.Token, "1")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.QuickLogin("dev-memo-2")
	if err != nil {
		t.Fatal(err)
	}
	u2, _, err := s.ChooseServer(r2.HeAccountID, r2.Token, "1")
	if err != nil {
		t.Fatal(err)
	}

	snap0, err := s.Snapshot(u1)
	if err != nil {
		t.Fatal(err)
	}
	if snap0.User["memo"] != "请多多指教！" {
		t.Fatalf("初始默认 memo = %v, want 请多多指教！", snap0.User["memo"])
	}
	if snap0.User["bg_id"] != "" {
		t.Fatalf("初始默认 bg_id = %v, want 空串", snap0.User["bg_id"])
	}

	if err := s.UpdateMemo(u1, "Poco验收签名-U05"); err != nil {
		t.Fatalf("UpdateMemo: %v", err)
	}
	if err := s.UpdateMemo(u1, ""); err != nil {
		t.Fatalf("UpdateMemo 空串幂等: %v", err)
	}
	if err := s.UpdateMemo("ghost-uid", "x"); !errors.Is(err, cn35game.ErrNoAccount) {
		t.Fatalf("幽灵账号 UpdateMemo = %v, want ErrNoAccount", err)
	}
	s.Close()

	s2 := openStore(t, dir)
	defer s2.Close()
	snap1, err := s2.Snapshot(u1)
	if err != nil {
		t.Fatal(err)
	}
	if snap1.User["memo"] != "Poco验收签名-U05" {
		t.Fatalf("重启后 u1 memo = %v, want Poco验收签名-U05", snap1.User["memo"])
	}
	snap2, err := s2.Snapshot(u2)
	if err != nil {
		t.Fatal(err)
	}
	if snap2.User["memo"] != "请多多指教！" {
		t.Fatalf("重启后 u2 memo 被污染 = %v", snap2.User["memo"])
	}
}
