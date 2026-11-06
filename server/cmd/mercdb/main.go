// Command mercdb manages the embedded schema used by mercd35.
// The default -db points at the CN 3.5.0 state store; pass -db explicitly for
// any other state directory.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/database"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mercdb:", err)
		os.Exit(1)
	}
}
func run() error {
	// PLAY350-08 五卡发放管理入口：独立子命令，不混入迁移 flagset。
	if len(os.Args) > 1 && os.Args[1] == "cn35-grant" {
		return cn35Grant(os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "cn35-roster-grant" {
		return cn35RosterGrant(os.Args[2:])
	}
	// GACHA350 真机验收余额发放入口（开发管理，不进 handler）。
	if len(os.Args) > 1 && os.Args[1] == "cn35-grant-balance" {
		return cn35GrantBalance(os.Args[2:])
	}
	// ROSTER350-06 名册/卡池治理入口（导入/种子/发布/回滚/审计）。
	if len(os.Args) > 1 && os.Args[1] == "cn35-roster" {
		return cn35Roster(os.Args[2:])
	}
	// 单机预置种子库生成入口（合入截止最新 SQL 迁移 + NPC 公会 + 名册卡池 + 资源配置）。
	if len(os.Args) > 1 && os.Args[1] == "cn35-seed-db" {
		return cn35SeedDB(os.Args[2:])
	}
	fs := flag.NewFlagSet("mercdb", flag.ContinueOnError)
	name := fs.String("name", "", "new migration name (lowercase snake_case)")
	dir := fs.String("dir", "internal/database/migrations", "SQL source directory for new")
	path := fs.String("db", "server-state-cn35/mercd35.sqlite3", "SQLite file")
	out := fs.String("out", "", "backup destination (must not exist)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: mercdb [-db PATH] [-out BACKUP] status|up|check|backup|new")
	}
	action := fs.Arg(0)
	if action == "new" {
		return newMigration(*dir, *name)
	}
	switch action {
	case "status", "up", "check", "backup":
	default:
		return fmt.Errorf("unknown command %q", action)
	}
	if action != "up" {
		if _, err := os.Stat(*path); err != nil {
			return err
		}
	}
	if action == "backup" && *out == "" {
		return fmt.Errorf("backup requires -out")
	}
	db, err := database.Open(*path)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	switch action {
	case "up":
		return database.Migrate(ctx, db)
	case "status":
		ms, err := database.Status(ctx, db)
		if err != nil {
			return err
		}
		for _, m := range ms {
			state := "pending"
			if m.Applied {
				state = "applied"
			}
			fmt.Printf("%s %s %s\n", state, m.Name, m.SHA256)
		}
	case "check":
		ms, err := database.Status(ctx, db)
		if err != nil {
			return err
		}
		for _, m := range ms {
			if !m.Applied {
				return fmt.Errorf("pending migration: %s", m.Name)
			}
		}
		rows, err := db.Query(`PRAGMA integrity_check`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var result string
			if err = rows.Scan(&result); err != nil {
				rows.Close()
				return err
			}
			if result != "ok" {
				rows.Close()
				return fmt.Errorf("integrity_check: %s", result)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		rows, err = db.Query(`PRAGMA foreign_key_check`)
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			return fmt.Errorf("foreign key violation")
		}
		if err = rows.Err(); err != nil {
			return err
		}
		// player-core invariants (migration 0002): every account owns exactly
		// one profile (PK guarantees uniqueness; missing row is the violation)
		// and at least the coin/gacha_point balance rows.
		var missing int
		if err = db.QueryRow(`SELECT count(*) FROM accounts a WHERE NOT EXISTS (
			SELECT 1 FROM player_profiles p WHERE p.user_id = a.user_id)`).Scan(&missing); err != nil {
			return err
		}
		if missing != 0 {
			return fmt.Errorf("player core: %d account(s) without profile", missing)
		}
		if err = db.QueryRow(`SELECT count(*) FROM accounts a WHERE NOT EXISTS (
			SELECT 1 FROM player_balances b WHERE b.user_id = a.user_id AND b.currency = 'coin')
			OR NOT EXISTS (
			SELECT 1 FROM player_balances b WHERE b.user_id = a.user_id AND b.currency = 'gacha_point')`).Scan(&missing); err != nil {
			return err
		}
		if missing != 0 {
			return fmt.Errorf("player core: %d account(s) without coin/gacha_point balances", missing)
		}
		fmt.Println("ok")
	case "backup":
		if err = os.MkdirAll(filepath.Dir(*out), 0700); err != nil {
			return err
		}
		if err = database.Backup(ctx, db, *out); err != nil {
			return err
		}
		fmt.Println(*out)
	}
	return nil
}

func newMigration(dir, name string) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(name) {
		return fmt.Errorf("-name must be lowercase snake_case")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	max := 0
	for _, e := range entries {
		if !regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+\.sql$`).MatchString(e.Name()) {
			return fmt.Errorf("invalid migration name %s", e.Name())
		}
		n, _ := strconv.Atoi(e.Name()[:4])
		if n > max {
			max = n
		}
	}
	if max >= 9999 {
		return fmt.Errorf("migration sequence exhausted")
	}
	path := filepath.Join(dir, fmt.Sprintf("%04d_%s.sql", max+1, name))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(f, "-- %s\n-- Describe affected routes, data backfill, verification and recovery in the PR.\n-- Transaction is managed by mercdb; do not add BEGIN/COMMIT or PRAGMA.\n", name)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Println(path)
	return nil
}
