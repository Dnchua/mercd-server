// cn35seeddb.go 构建期单机预置种子库（seed.sqlite3 + seed_meta.json）生成器。
//
//	mercdb cn35-seed-db -out <path>/seed.sqlite3 [-meta <path>/seed_meta.json] [-assets DIR] [-cards DIR]
//
// 背景与语义：
//   - 服务端保留按序号递增的 SQL 迁移链（0001..NNNN），服务于后续联机服务版
//     的在线平滑升级与存量库演进。
//   - Android 单机包（32 位 armeabi-v7a + libhoudini.so + wazero 纯 Go SQLite）
//     若在全新安装首启时从零执行数十条 SQL 迁移并逐行导入近万条名册/资源行，
//     耗时达 30~100s 且易触发 houdini/wazero 栈展开崩溃。
//   - 因此单机版在构建期把截止当前最新 SQL 迁移版本的 schema + 初始 NPC 公会池 +
//     名册/卡池首版发布 + 资源副本/徽章商品首版发布合成为单一紧凑的 seed.sqlite3
//     （经 VACUUM INTO 固化为无 WAL 尾文件的单文件库），并在库内（schema_migrations、
//     PRAGMA user_version、cn35_admin_audit）及同目录 seed_meta.json 中明确记录
//     截止的 SQL 迁移版本（cutoff_migration）。
//   - 客户端首启若 state/mercd35.sqlite3 不存在，直接复制 seed.sqlite3 就位；
//     后续若 APK 升级带来 cutoff_migration 之后的新迁移，database.Migrate 仍会
//     按 schema_migrations 增量执行新 SQL，不丢失玩家存档。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35roster"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/database"
)

// seedMigrationDigest 记录打入种子库的单条 SQL 迁移摘要。
type seedMigrationDigest struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// seedDBMeta 落盘到 seed_meta.json 并同步写入 cn35_admin_audit，备注种子库截止的 SQL 版本。
type seedDBMeta struct {
	Description          string                `json:"description"`
	CutoffMigration      string                `json:"cutoff_migration"`
	CutoffMigrationNum   int                   `json:"cutoff_migration_number"`
	CutoffMigrationSHA   string                `json:"cutoff_migration_sha256"`
	MigrationCount       int                   `json:"migration_count"`
	SeedDBFile           string                `json:"seed_db_file"`
	SeedDBSize           int64                 `json:"seed_db_size"`
	SeedDBSHA256         string                `json:"seed_db_sha256"`
	UnitsVersion         string                `json:"units_version"`
	MonstersVersion      string                `json:"monsters_version"`
	GachaVersion         int64                 `json:"gacha_version"`
	GachaDigest          string                `json:"gacha_digest"`
	ResourceConfigVer    int64                 `json:"resource_config_version"`
	ResourceConfigDigest string                `json:"resource_config_digest"`
	Note                 string                `json:"note"`
	Migrations           []seedMigrationDigest `json:"migrations"`
}

func cn35SeedDB(args []string) error {
	fs := flag.NewFlagSet("cn35-seed-db", flag.ContinueOnError)
	out := fs.String("out", "", "输出种子库路径（如 client/decoded/assets/backend/seed.sqlite3）")
	metaOut := fs.String("meta", "", "输出种子库元数据 JSON 路径（默认与 -out 同目录的 seed_meta.json）")
	assets := fs.String("assets", "assets/cn350", "CN3.5 fixture 目录")
	cards := fs.String("cards", "assets/shared/cards", "cardcatalog 目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("usage: mercdb cn35-seed-db -out <path>/seed.sqlite3 [-meta <path>/seed_meta.json] [-assets DIR] [-cards DIR]")
	}
	metaPath := *metaOut
	if metaPath == "" {
		metaPath = filepath.Join(filepath.Dir(*out), "seed_meta.json")
	}

	assetsDir := resolveAsset(*assets)
	cardsDir := resolveAsset(*cards)

	cat, err := cardcatalog.Load(cardsDir)
	if err != nil {
		return fmt.Errorf("加载卡牌目录失败: %w", err)
	}
	in, _, err := cn35roster.LoadImport(assetsDir, cardsDir)
	if err != nil {
		return fmt.Errorf("加载名册清单失败: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "mercdb-cn35-seed-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	workDB := filepath.Join(tmpDir, "work.sqlite3")
	st, err := cn35store.Open(workDB)
	if err != nil {
		return fmt.Errorf("初始化临时种子库失败: %w", err)
	}
	defer st.Close()

	// 1. 初始化单机 NPC 公会经济开关与默认 NPC 公会池。
	if err := st.ConfigureGuildEconomy(false, false); err != nil {
		return fmt.Errorf("配置公会经济开关失败: %w", err)
	}
	if err := st.ConfigureGuildSolo(cn35store.DefaultGuildSoloSettings); err != nil {
		return fmt.Errorf("初始化单机 NPC 公会池失败: %w", err)
	}

	// 2. 导入名册并发布首版双卡池配置。
	gachaVer, seeded, err := cn35game.SeedGachaRoster(st, in, "seed")
	if err != nil {
		return fmt.Errorf("种子化名册/卡池失败: %w", err)
	}
	if !seeded || gachaVer == nil {
		return fmt.Errorf("名册/卡池未产生首发版本")
	}

	// 3. 发布首版资源副本与徽章商品配置。
	resVer, resDigest, err := st.PublishResourceConfig("seed", "user-design-2026-09-30 首版发布",
		func(monsterID int) error {
			_, err := cat.Monster(monsterID)
			return err
		})
	if err != nil {
		return fmt.Errorf("发布首版资源配置失败: %w", err)
	}

	// 4. 核对全部 SQL 迁移状态，提取截止的 SQL 版本信息。
	ctx := context.Background()
	ms, err := database.Status(ctx, st.DB())
	if err != nil {
		return fmt.Errorf("读取迁移状态失败: %w", err)
	}
	if len(ms) == 0 {
		return fmt.Errorf("没有任何 SQL 迁移记录")
	}
	mList := make([]seedMigrationDigest, 0, len(ms))
	for _, m := range ms {
		if !m.Applied {
			return fmt.Errorf("存在未应用的迁移: %s", m.Name)
		}
		mList = append(mList, seedMigrationDigest{
			Name:   m.Name,
			SHA256: m.SHA256,
		})
	}
	last := ms[len(ms)-1]
	cutoffNum, _ := strconv.Atoi(last.Name[:4])
	cmeta := cat.Meta()
	noteText := fmt.Sprintf("CN3.5 单机预置种子库，已合入 0001..%s 共 %d 条 SQL 迁移及初始公会池/名册卡池/资源配置；后续新增迁移在客户端升级后按 schema_migrations 增量执行",
		last.Name, len(ms))

	// 5. 在数据库内部备注截止 SQL 版本（PRAGMA user_version + cn35_admin_audit）。
	if _, err := st.DB().Exec(fmt.Sprintf("PRAGMA user_version = %d", cutoffNum)); err != nil {
		return fmt.Errorf("写入 PRAGMA user_version 失败: %w", err)
	}
	auditDetail, err := json.Marshal(map[string]any{
		"cutoff_migration":        last.Name,
		"cutoff_migration_number": cutoffNum,
		"cutoff_migration_sha256": last.SHA256,
		"migration_count":         len(ms),
		"units_version":           cmeta.UnitsVersion,
		"monsters_version":        cmeta.MonstersVersion,
		"gacha_version":           gachaVer.Version,
		"gacha_digest":            gachaVer.Digest,
		"resource_config_version": resVer,
		"resource_config_digest":  resDigest,
		"note":                    noteText,
	})
	if err != nil {
		return err
	}
	if _, err := st.DB().Exec(
		`INSERT INTO cn35_admin_audit(actor, action, target, detail, created_at) VALUES(?, ?, ?, ?, ?)`,
		rosterActor, "seed.db.build", last.Name, string(auditDetail), time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("写入种子库构建审计记录失败: %w", err)
	}

	// 6. 用 VACUUM INTO 导出紧凑单文件 SQLite（无 WAL/SHM 附属文件）。
	if err := os.MkdirAll(filepath.Dir(*out), 0755); err != nil {
		return err
	}
	if err := os.Remove(*out); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := database.Backup(ctx, st.DB(), *out); err != nil {
		return fmt.Errorf("导出单文件种子库失败: %w", err)
	}

	dbBytes, err := os.ReadFile(*out)
	if err != nil {
		return err
	}
	dbSum := sha256.Sum256(dbBytes)

	meta := seedDBMeta{
		Description:          "CN3.5 单机版预置 SQLite 种子库元数据（记录截止的 SQL 迁移版本与初始静态发布摘要）",
		CutoffMigration:      last.Name,
		CutoffMigrationNum:   cutoffNum,
		CutoffMigrationSHA:   last.SHA256,
		MigrationCount:       len(ms),
		SeedDBFile:           filepath.Base(*out),
		SeedDBSize:           int64(len(dbBytes)),
		SeedDBSHA256:         hex.EncodeToString(dbSum[:]),
		UnitsVersion:         cmeta.UnitsVersion,
		MonstersVersion:      cmeta.MonstersVersion,
		GachaVersion:         gachaVer.Version,
		GachaDigest:          gachaVer.Digest,
		ResourceConfigVer:    resVer,
		ResourceConfigDigest: resDigest,
		Note:                 noteText,
		Migrations:           mList,
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	metaJSON = append(metaJSON, '\n')
	if err := os.MkdirAll(filepath.Dir(metaPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(metaPath, metaJSON, 0644); err != nil {
		return fmt.Errorf("写入种子库元数据失败: %w", err)
	}

	fmt.Printf("seed_db=%s size=%d sha256=%s cutoff_migration=%s (%d migrations) meta=%s\n",
		*out, meta.SeedDBSize, meta.SeedDBSHA256[:16], meta.CutoffMigration, meta.MigrationCount, metaPath)
	return nil
}
