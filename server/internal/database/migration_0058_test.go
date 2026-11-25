package database

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestStoryQuestIDRemap0058 旧本地目录（10 stage/160 关）升级原服目录的
// quest_id 重映射：旧 stage 6–10（id 81–160）按位置映射到原服 id；
// stage 1–5（id 1–80）与资源 900xx 空间不动；125–160 与目标区间重叠，
// 验证先 +900000 再映射不产生 PK 冲突。
func TestStoryQuestIDRemap0058(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	src, _ := fs.Sub(migrations, "migrations")
	names, err := EmbeddedMigrationNames()
	if err != nil {
		t.Fatal(err)
	}
	last := names[len(names)-1]
	if !strings.HasPrefix(last, "0058_") {
		t.Fatalf("最新迁移应为 0058，实际 %s", last)
	}
	// 模拟旧库：只应用 0058 之前的迁移。
	pre := fstest.MapFS{}
	for _, n := range names {
		if n == last {
			continue
		}
		raw, rerr := fs.ReadFile(src, n)
		if rerr != nil {
			t.Fatal(rerr)
		}
		pre[n] = &fstest.MapFile{Data: raw}
	}
	if err = migrate(ctx, db, pre); err != nil {
		t.Fatal(err)
	}

	must := func(query string, args ...any) {
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	now := "2026-11-01T00:00:00Z"
	must(`INSERT INTO cn35_accounts (he_account_id,user_id,token,sk,user_name,account_name,is_new_user,tutorial_finish,created_at,updated_at)
		VALUES ('he1','u1','t','s','n','old58',0,0,?,?)`, now, now)
	// 进度指针落在旧 stage8（quest 125）。
	must(`INSERT INTO cn35_quest_progress (user_id,stage_id,area_id,quest_id,hard_mode_stage_id,created_at,updated_at)
		VALUES ('u1',8,29,125,0,?,?)`, now, now)
	// 通关记录覆盖：不动段（1）、重叠段（125→217 且 81→125）、末段（160→300）。
	for _, qid := range []int{1, 81, 125, 160} {
		must(`INSERT INTO cn35_quest_clears (user_id,quest_id,difficulty,clear_count,updated_at)
			VALUES ('u1',?,'normal',1,?)`, qid, now)
	}
	must(`INSERT INTO cn35_battle_sessions (battle_id,user_id,quest_id,area_id,difficulty,party_id,status,
		ap_cost,rewards_json,exp,next_stage_id,next_area_id,next_quest_id,continuable,created_at,updated_at)
		VALUES ('b1','u1',160,40,'normal','001','cleared',15,'[]',300,9,33,97,1,?,?)`, now, now)
	must(`INSERT INTO cn35_battle_logs (user_id,battle_id,quest_id,record_json,created_at)
		VALUES ('u1','b1',97,'{}',?)`, now)

	if err = Migrate(ctx, db); err != nil {
		t.Fatalf("0058 迁移失败: %v", err)
	}

	var q int
	if err = db.QueryRow(`SELECT quest_id FROM cn35_quest_progress WHERE user_id='u1'`).Scan(&q); err != nil || q != 217 {
		t.Fatalf("progress 125→217: %d %v", q, err)
	}
	want := map[int]int{1: 1, 81: 125, 125: 217, 160: 300}
	for old, wantNew := range want {
		if err = db.QueryRow(`SELECT count(*) FROM cn35_quest_clears WHERE user_id='u1' AND quest_id=?`, wantNew).Scan(&q); err != nil || q != 1 {
			t.Fatalf("clears %d→%d: count=%d err=%v", old, wantNew, q, err)
		}
	}
	if err = db.QueryRow(`SELECT count(*) FROM cn35_quest_clears WHERE quest_id BETWEEN 900000 AND 999999`).Scan(&q); err != nil || q != 0 {
		t.Fatalf("clears 残留临时 id: %d", q)
	}
	var nq int
	if err = db.QueryRow(`SELECT quest_id,next_quest_id FROM cn35_battle_sessions WHERE battle_id='b1'`).Scan(&q, &nq); err != nil {
		t.Fatal(err)
	}
	if q != 300 || nq != 141 {
		t.Fatalf("session 160→300 / next 97→141: %d/%d", q, nq)
	}
	if err = db.QueryRow(`SELECT quest_id FROM cn35_battle_logs WHERE battle_id='b1'`).Scan(&q); err != nil || q != 141 {
		t.Fatalf("log 97→141: %d %v", q, err)
	}
	if err = Migrate(ctx, db); err != nil {
		t.Fatalf("重复迁移应幂等: %v", err)
	}
}
