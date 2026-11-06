package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35store"
)

func TestCN35SeedDB(t *testing.T) {
	dir := t.TempDir()
	outDB := filepath.Join(dir, "seed.sqlite3")
	outMeta := filepath.Join(dir, "seed_meta.json")
	assets := filepath.Join("..", "..", "assets", "cn350")
	cards := filepath.Join("..", "..", "assets", "shared", "cards")

	if err := cn35SeedDB([]string{
		"-out", outDB,
		"-meta", outMeta,
		"-assets", assets,
		"-cards", cards,
	}); err != nil {
		t.Fatalf("cn35SeedDB failed: %v", err)
	}

	rawMeta, err := os.ReadFile(outMeta)
	if err != nil {
		t.Fatalf("read seed_meta.json: %v", err)
	}
	var meta seedDBMeta
	if err := json.Unmarshal(rawMeta, &meta); err != nil {
		t.Fatalf("unmarshal seed_meta.json: %v", err)
	}
	if meta.CutoffMigration == "" || meta.CutoffMigrationNum < 49 {
		t.Fatalf("unexpected cutoff migration: %+v", meta)
	}
	if meta.MigrationCount != len(meta.Migrations) || meta.MigrationCount < 49 {
		t.Fatalf("unexpected migration count: %d vs %d", meta.MigrationCount, len(meta.Migrations))
	}
	if meta.GachaVersion < 1 || meta.ResourceConfigVer < 1 {
		t.Fatalf("expected published gacha & resource config versions, got gacha=%d res=%d",
			meta.GachaVersion, meta.ResourceConfigVer)
	}

	// 验证不能残留 -wal / -shm 附属文件。
	for _, ext := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(outDB + ext); !os.IsNotExist(err) {
			t.Fatalf("seed db must not have %s sidecar", ext)
		}
	}

	// 打开生成的 seed.sqlite3 验证库内截止 SQL 版本备注及已发布配置。
	st, err := cn35store.Open(outDB)
	if err != nil {
		t.Fatalf("open generated seed db: %v", err)
	}
	defer st.Close()

	var userVer int
	if err := st.DB().QueryRow(`PRAGMA user_version`).Scan(&userVer); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	if userVer != meta.CutoffMigrationNum {
		t.Fatalf("PRAGMA user_version = %d, want %d", userVer, meta.CutoffMigrationNum)
	}

	var target, detail string
	if err := st.DB().QueryRow(
		`SELECT target, detail FROM cn35_admin_audit WHERE action='seed.db.build' ORDER BY audit_id DESC LIMIT 1`,
	).Scan(&target, &detail); err != nil {
		t.Fatalf("query seed.db.build audit: %v", err)
	}
	if target != meta.CutoffMigration {
		t.Fatalf("audit target = %q, want %q", target, meta.CutoffMigration)
	}

	gcfg, err := st.ActiveGachaConfig()
	if err != nil || gcfg == nil {
		t.Fatalf("expected active gacha config in seed db, err=%v cfg=%v", err, gcfg)
	}
	rcfg, err := st.ActiveResourceConfig()
	if err != nil || rcfg == nil {
		t.Fatalf("expected active resource config in seed db, err=%v cfg=%v", err, rcfg)
	}
}

