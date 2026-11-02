// Package database owns SQLite connections and immutable embedded migrations.
package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens a file database. Migrate is explicit so status/backup never upgrade it.
func Open(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	// file URI 路径必须以 '/' 开头：Windows 下 absolute 形如 C:/...，
	// 直接放进 Path 会得到 "file:C:/..."，驱动解析出非法 authority。
	slash := filepath.ToSlash(absolute)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	u := url.URL{Scheme: "file", Path: slash}
	q := u.Query()
	for _, p := range []string{"busy_timeout(5000)", "journal_mode(WAL)", "foreign_keys(ON)", "synchronous(FULL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type Migration struct {
	Name    string
	SHA256  string
	Applied bool
}

func scripts(source fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, err
	}
	var out []Migration
	valid := regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+\.sql$`)
	for _, e := range entries {
		if e.IsDir() || !valid.MatchString(e.Name()) {
			return nil, fmt.Errorf("invalid migration name: %s", e.Name())
		}
		raw, err := fs.ReadFile(source, e.Name())
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(raw)) == "" {
			return nil, fmt.Errorf("empty migration: %s", e.Name())
		}
		out = append(out, Migration{Name: e.Name(), SHA256: fmt.Sprintf("%x", sha256.Sum256(raw))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	for i, m := range out {
		if !strings.HasPrefix(m.Name, fmt.Sprintf("%04d_", i+1)) {
			return nil, fmt.Errorf("migration sequence gap or duplicate: %s", m.Name)
		}
	}
	return out, nil
}

// EmbeddedMigrationNames 返回内嵌迁移的有序文件名列表（只读，供测试与工具断言
// 「当前主线迁移条数与最新迁移」）。新增迁移后不需要再手改测试常量。
func EmbeddedMigrationNames() ([]string, error) {
	source, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	ms, err := scripts(source)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ms))
	for _, m := range ms {
		names = append(names, m.Name)
	}
	return names, nil
}

// Migrate applies pending scripts atomically with their checksum ledger. A failed
// script rolls back the entire batch. Changed/deleted history and newer DBs fail closed.
func Migrate(ctx context.Context, db *sql.DB) error {
	source, _ := fs.Sub(migrations, "migrations")
	return migrate(ctx, db, source)
}

func migrate(ctx context.Context, db *sql.DB, source fs.FS) error {
	ms, err := scripts(source)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		return err
	}
	if err = inspect(ctx, tx, ms); err != nil {
		return err
	}
	for _, m := range ms {
		if m.Applied {
			continue
		}
		raw, err := fs.ReadFile(source, m.Name)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(raw)); err != nil {
			return fmt.Errorf("migration %s: %w", m.Name, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, m.Name, m.SHA256); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func inspect(ctx context.Context, q queryer, ms []Migration) error {
	rows, err := q.QueryContext(ctx, `SELECT name,sha256 FROM schema_migrations ORDER BY name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var name, sum string
		if err = rows.Scan(&name, &sum); err != nil {
			return err
		}
		if i >= len(ms) || ms[i].Name != name || ms[i].SHA256 != sum {
			return fmt.Errorf("migration history mismatch at %s; restore matching binary/scripts, never edit applied SQL", name)
		}
		ms[i].Applied = true
		i++
	}
	return rows.Err()
}

// Status verifies the ledger without applying migrations, including an empty DB.
func Status(ctx context.Context, db *sql.DB) ([]Migration, error) {
	source, _ := fs.Sub(migrations, "migrations")
	ms, err := scripts(source)
	if err != nil {
		return nil, err
	}
	var exists int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists != 0 {
		err = inspect(ctx, db, ms)
	}
	return ms, err
}

// Backup produces a consistent standalone snapshot, including committed WAL data.
// It refuses an existing destination. Remove an interrupted partial backup manually.
func Backup(ctx context.Context, db *sql.DB, destination string) error {
	path, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}
