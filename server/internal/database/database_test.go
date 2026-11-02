package database

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMigrationLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src, _ := fs.Sub(migrations, "migrations")
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	// 「旧二进制打开新库」场景：用内嵌迁移集合 + 一条合成迁移模拟
	// 「本机已有一条更高编号的迁移」。集合从内嵌目录动态生成，避免每加一条
	// 迁移就要手改这段清单（GUILDCLOSE350 并行追加 0038—0041 时踩过）。
	names, err := EmbeddedMigrationNames()
	if err != nil {
		t.Fatal(err)
	}
	upgrade := fstest.MapFS{}
	for _, name := range names {
		raw, rerr := fs.ReadFile(src, name)
		if rerr != nil {
			t.Fatal(rerr)
		}
		upgrade[name] = &fstest.MapFile{Data: raw}
	}
	future := fmt.Sprintf("%04d_test.sql", len(names)+1)
	upgrade[future] = &fstest.MapFile{Data: []byte(`CREATE TABLE example(id INTEGER PRIMARY KEY); INSERT INTO example VALUES(42);`)}
	if err = migrate(ctx, db, upgrade); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db); err == nil {
		t.Fatal("old binary accepted newer DB")
	}
	upgrade[future].Data = []byte(`CREATE TABLE changed(id INTEGER);`)
	if err = migrate(ctx, db, upgrade); err == nil {
		t.Fatal("edited history accepted")
	}
}

func TestFailedBatchRollsBack(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	source := fstest.MapFS{"0001_test.sql": {Data: []byte(`CREATE TABLE example(id INTEGER);`)}, "0002_bad.sql": {Data: []byte(`INSERT INTO missing VALUES(1);`)}}
	if err = migrate(context.Background(), db, source); err == nil {
		t.Fatal("bad SQL succeeded")
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('example','schema_migrations')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial migration: %d %v", n, err)
	}
}

func TestBackupIncludesWALAndRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "source.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO legacy_imports VALUES ('test','digest',2,'now')`); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backup.sqlite3")
	if err = Backup(context.Background(), db, backup); err != nil {
		t.Fatal(err)
	}
	if err = Backup(context.Background(), db, backup); err == nil {
		t.Fatal("overwrote backup")
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var n int
	if err = restored.QueryRow(`SELECT row_count FROM legacy_imports WHERE name='test'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("backup content: %d %v", n, err)
	}
}

// TestPlayerCoreUpgradeBackfill 0001 真实库（两个代表性账号）升级 0002：
// 账号字段不变、每账号独立回填 profile + coin/gacha_point；后续 INSERT
// 账号由 trigger 同事务建档；初始化失败整批回滚；约束/外键/唯一拒绝
// 非法行。对应设计文档「Migration 与约束」验收段。
func TestPlayerCoreUpgradeBackfill(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src, _ := fs.Sub(migrations, "migrations")
	raw1, _ := fs.ReadFile(src, "0001_accounts.sql")
	raw2, _ := fs.ReadFile(src, "0002_player_core.sql")

	// 真实 0001 schema + 两个代表性账号（凭据/昵称各不相同）。
	if err = migrate(ctx, db, fstest.MapFS{"0001_accounts.sql": {Data: raw1}}); err != nil {
		t.Fatal(err)
	}
	insertAccount := func(heID, userID, name string) {
		t.Helper()
		_, err = db.ExecContext(ctx, `INSERT INTO accounts
			(he_account_id,user_id,encrypted_user_id,token,sk,user_name,is_new_user,tutorial_finish,created_at,updated_at)
			VALUES (?,?,?,?,?,?,1,0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
			heID, userID, "enc-"+userID, "tok-"+heID, "sk-"+heID, name)
		if err != nil {
			t.Fatal(err)
		}
	}
	insertAccount("CN-A", "u-alpha", "甲")
	insertAccount("CN-B", "u-beta", "乙")

	// 升级到 0002：两个账号各自独立回填。
	if err = migrate(ctx, db, fstest.MapFS{
		"0001_accounts.sql":    {Data: raw1},
		"0002_player_core.sql": {Data: raw2},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ userID, name string }{{"u-alpha", "甲"}, {"u-beta", "乙"}} {
		var level, ap, maxAP, bp, maxBP int
		var leader, guild string
		if err = db.QueryRowContext(ctx, `SELECT level,ap,max_ap,bp,max_bp,leader_unit_id,guild_name_snapshot
			FROM player_profiles WHERE user_id=?`, tc.userID).
			Scan(&level, &ap, &maxAP, &bp, &maxBP, &leader, &guild); err != nil {
			t.Fatalf("%s profile: %v", tc.userID, err)
		}
		if level != 1 || ap != 30 || maxAP != 30 || bp != 600 || maxBP != 600 || leader != "152" || guild != "小溪的大宇宙" {
			t.Fatalf("%s 回填值错误: level=%d ap=%d/%d bp=%d/%d leader=%q guild=%q", tc.userID, level, ap, maxAP, bp, maxBP, leader, guild)
		}
		var coin, gachaPoint int
		if err = db.QueryRowContext(ctx, `SELECT amount FROM player_balances WHERE user_id=? AND currency='coin'`, tc.userID).Scan(&coin); err != nil {
			t.Fatalf("%s coin: %v", tc.userID, err)
		}
		if err = db.QueryRowContext(ctx, `SELECT amount FROM player_balances WHERE user_id=? AND currency='gacha_point'`, tc.userID).Scan(&gachaPoint); err != nil {
			t.Fatalf("%s gacha_point: %v", tc.userID, err)
		}
		if coin != 3 || gachaPoint != 96343 {
			t.Fatalf("%s 余额回填错误: coin=%d gacha_point=%d", tc.userID, coin, gachaPoint)
		}
		var name string
		if err = db.QueryRowContext(ctx, `SELECT user_name FROM accounts WHERE user_id=?`, tc.userID).Scan(&name); err != nil || name != tc.name {
			t.Fatalf("%s 账号字段被改动: name=%q err=%v", tc.userID, name, err)
		}
	}

	// trigger：新 INSERT 账号同事务建档。
	insertAccount("CN-C", "u-gamma", "丙")
	var n int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM player_profiles WHERE user_id='u-gamma'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("trigger 未建 profile: %d %v", n, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM player_balances WHERE user_id='u-gamma'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("trigger 未建双余额: %d %v", n, err)
	}

	// 约束：负余额 / ap>max_ap / bp>max_bp / 外键 / 唯一 一律拒绝。
	violations := []string{
		`INSERT INTO player_balances VALUES ('u-alpha','coin',-1,'t')`,
		`INSERT INTO player_profiles VALUES ('u-none',1,0,100,0,30,'t',0,600,'1',0,'g','t','t')`,
		`UPDATE player_profiles SET ap=99 WHERE user_id='u-alpha'`,
		`UPDATE player_profiles SET bp=601 WHERE user_id='u-alpha'`,
		`INSERT INTO player_balances VALUES ('u-alpha','coin',0,'t')`,
	}
	for i, sql := range violations {
		if _, err = db.ExecContext(ctx, sql); err == nil {
			t.Fatalf("约束违规 #%d 未被拒绝: %s", i, sql)
		}
	}

	// 初始化失败整批回滚：拆掉 profile 表后 INSERT 账号 → trigger 失败，
	// 账号行不得残留。
	if _, err = db.ExecContext(ctx, `DROP TABLE player_profiles`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO accounts
		(he_account_id,user_id,encrypted_user_id,token,sk,user_name,is_new_user,tutorial_finish,created_at,updated_at)
		VALUES ('CN-D','u-delta','enc-u-delta','tok','sk','丁',1,0,'t','t')`); err == nil {
		t.Fatal("trigger 失败时账号 INSERT 竟然成功")
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE user_id='u-delta'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("初始化失败后账号残留: %d %v", n, err)
	}
}

func TestMigrationNames(t *testing.T) {
	for _, name := range []string{"0002_gap.sql", "bad.sql"} {
		if _, err := scripts(fstest.MapFS{name: {Data: []byte("SELECT 1;")}}); err == nil {
			t.Fatal("invalid sequence accepted", name)
		}
	}
}
