// gacha_migrate_0056_test.go migrate-0056 存量库卡池补入：8 个官方样本
// 单位（unit_overrides 行为配置视同已配置）合格者置 enabled 并按稀有度
// 归属补入草稿成员后重发；其他成员/权重不变；幂等二次执行不发布。
package cn35store

import (
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var officialSampleIDsForTest = []int{16, 152, 156, 218, 296, 336, 348, 375}

// activeGachaMembers 读激活版本号与成员明细：pool → unit → {weight, rare}。
func activeGachaMembers(t *testing.T, s *Store) (int64, map[string]map[int][2]int) {
	t.Helper()
	var version int64
	if err := s.DB().QueryRow(`SELECT version FROM cn35_gacha_release WHERE release_id=1`).
		Scan(&version); err != nil {
		t.Fatalf("读激活版本: %v", err)
	}
	out := map[string]map[int][2]int{}
	rows, err := s.DB().Query(`SELECT pool_id,unit_id,weight,rare
		FROM cn35_gacha_pool_version_members WHERE version=?`, version)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var pid string
		var uid, w, rare int
		if err := rows.Scan(&pid, &uid, &w, &rare); err != nil {
			t.Fatal(err)
		}
		if out[pid] == nil {
			out[pid] = map[int][2]int{}
		}
		out[pid][uid] = [2]int{w, rare}
	}
	return version, out
}

// seedLegacySampleExcluded 构造"修复前"存量库：正常种子后把 8 个官方样本
// 单位移出草稿成员并发布、roster enabled 置 0。
func seedLegacySampleExcluded(t *testing.T, s *Store) {
	t.Helper()
	if _, _, err := cn35game.SeedGachaRoster(s, testImport(t), "seed"); err != nil {
		t.Fatalf("种子化: %v", err)
	}
	excluded := map[int]bool{}
	for _, id := range officialSampleIDsForTest {
		excluded[id] = true
	}
	ms, err := s.GachaDraftMembers()
	if err != nil {
		t.Fatal(err)
	}
	for pid, list := range ms {
		var kept []cn35game.GachaDraftMember
		for _, m := range list {
			if !excluded[m.UnitID] {
				kept = append(kept, m)
			}
		}
		if err := s.ReplaceDraftMembers(pid, kept, "admin"); err != nil {
			t.Fatalf("剔除样本成员 池%s: %v", pid, err)
		}
	}
	if _, err := s.PublishGachaDraft("admin", "legacy-no-samples"); err != nil {
		t.Fatalf("发布旧口径: %v", err)
	}
	no := false
	if err := s.SetRosterFlags(officialSampleIDsForTest, &no, nil,
		"legacy-disable", "admin"); err != nil {
		t.Fatalf("禁用样本单位: %v", err)
	}
}

func TestAddGachaOfficialSampleUnits(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedLegacySampleExcluded(t, s)

	_, before := activeGachaMembers(t, s)
	for pid, set := range before {
		for _, id := range officialSampleIDsForTest {
			if _, ok := set[id]; ok {
				t.Fatalf("旧库池 %s 不应含样本单位 %d", pid, id)
			}
		}
	}

	v, published, err := s.AddGachaOfficialSampleUnits("migrate-0056",
		"官方样本 8 单位补入卡池", cn35game.OfficialSampleUnitIDs(),
		cn35game.GachaSeedPoolsForRare)
	if err != nil {
		t.Fatalf("迁移: %v", err)
	}
	if !published || v == nil {
		t.Fatal("有新增成员应发布新版本")
	}
	afterVer, after := activeGachaMembers(t, s)
	if afterVer != v.Version {
		t.Fatalf("生效指针=%d 未指向新版本 %d", afterVer, v.Version)
	}

	// 池 "2" 新增 6 个 5 星（weight=1, rare=50），且不进金币池。
	for _, id := range []int{152, 156, 296, 336, 348, 375} {
		wr, ok := after["2"][id]
		if !ok {
			t.Fatalf("池2缺样本单位 %d", id)
		}
		if wr != [2]int{1, 50} {
			t.Fatalf("池2单位 %d weight/rare=%v want {1,50}", id, wr)
		}
		if _, ok := after["3"][id]; ok {
			t.Fatalf("5 星样本 %d 不应进金币池", id)
		}
	}
	// 池 "3" 新增 16/218（1 星，rare=10）。
	for _, id := range []int{16, 218} {
		wr, ok := after["3"][id]
		if !ok {
			t.Fatalf("池3缺样本单位 %d", id)
		}
		if wr != [2]int{1, 10} {
			t.Fatalf("池3单位 %d weight/rare=%v want {1,10}", id, wr)
		}
	}
	// 其余成员与权重与迁移前完全一致。
	for pid, set := range before {
		for id, wr := range set {
			got, ok := after[pid][id]
			if !ok || got != wr {
				t.Fatalf("池 %s 成员 %d 被改动: before=%v after=%v ok=%v", pid, id, wr, got, ok)
			}
		}
	}
	if len(after["2"]) != len(before["2"])+6 || len(after["3"]) != len(before["3"])+2 {
		t.Fatalf("成员数增量不符: 池2 %d→%d 池3 %d→%d",
			len(before["2"]), len(after["2"]), len(before["3"]), len(after["3"]))
	}

	// 8 个 roster enabled=1。
	for _, id := range officialSampleIDsForTest {
		var en int
		if err := s.DB().QueryRow(`SELECT enabled FROM cn35_unit_roster WHERE unit_id=?`, id).
			Scan(&en); err != nil {
			t.Fatal(err)
		}
		if en != 1 {
			t.Fatalf("单位 %d enabled=%d want 1", id, en)
		}
	}
	// 迁移标记已落。
	marks, err := s.GachaMigrationMarkers()
	if err != nil {
		t.Fatal(err)
	}
	if !marks["migrate-0056"] {
		t.Fatal("审计缺 migrate-0056 标记")
	}

	// 幂等：二次执行不发布新版本、激活版本号不变。
	v2, published2, err := s.AddGachaOfficialSampleUnits("migrate-0056",
		"官方样本 8 单位补入卡池", cn35game.OfficialSampleUnitIDs(),
		cn35game.GachaSeedPoolsForRare)
	if err != nil {
		t.Fatalf("二次迁移: %v", err)
	}
	if published2 || v2 != nil {
		t.Fatal("幂等二次执行不应发布新版本")
	}
	ver3, _ := activeGachaMembers(t, s)
	if ver3 != afterVer {
		t.Fatalf("二次执行激活版本 %d→%d 不应变化", afterVer, ver3)
	}
}
