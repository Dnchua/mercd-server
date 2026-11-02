// grant_test.go PLAY350-08 五卡发放验收：首发放 5 张、二次零新增、
// 并发一笔生效、失败零部分、版本冲突拒绝、他账号无变化、
// 预拥有不覆盖、队长修正、重启保持。
package cn35store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func testCatalog(t *testing.T) *cardcatalog.Catalog {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return cat
}

func testGrantCfg() *cn35game.FiveStarConfig {
	return &cn35game.FiveStarConfig{
		GrantVersion: "play350-05-v1", LeaderUnitID: 348,
		Units: []cn35game.FiveStarUnit{
			{ID: 152, InitialLevel: 1}, {ID: 157, InitialLevel: 1},
			{ID: 161, InitialLevel: 1}, {ID: 230, InitialLevel: 1},
			{ID: 348, InitialLevel: 1, Role: "leader"},
		},
	}
}

func countTable(t *testing.T, s *Store, table, userID string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE user_id=?`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestGrantBasicIdempotent 首次 5 卡 + 二次幂等零新增。
func TestGrantBasicIdempotent(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "grant-a")
	cat, cfg := testCatalog(t), testGrantCfg()

	res, err := s.GrantFiveStar(uid, cfg, cat)
	if err != nil {
		t.Fatalf("首次发放: %v", err)
	}
	if len(res.Inserted) != 5 || len(res.UnitIDs) != 5 || res.Idempotent {
		t.Fatalf("结果: %+v", res)
	}
	if res.LeaderUnitID != 348 || res.LeaderChanged {
		t.Fatalf("队长: %+v", res) // 默认 leader=348 发放后即属主，无需改写
	}
	if n := countTable(t, s, "cn35_player_units", uid); n != 5 {
		t.Fatalf("units=%d", n)
	}
	if n := countTable(t, s, "cn35_inventory_grants", uid); n != 5 {
		t.Fatalf("grants=%d", n)
	}

	res2, err := s.GrantFiveStar(uid, cfg, cat)
	if err != nil {
		t.Fatalf("二次发放应幂等: %v", err)
	}
	if !res2.Idempotent || len(res2.Inserted) != 0 {
		t.Fatalf("幂等结果: %+v", res2)
	}
	if n := countTable(t, s, "cn35_player_units", uid); n != 5 {
		t.Fatalf("幂等后 units=%d", n)
	}
}

// TestGrantConcurrent 并发发放只有一笔生效，全部拿到一致结果。
func TestGrantConcurrent(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "grant-c")
	cat, cfg := testCatalog(t), testGrantCfg()

	const N = 6
	var wg sync.WaitGroup
	results := make([]*cn35game.GrantResult, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.GrantFiveStar(uid, cfg, cat)
		}(i)
	}
	wg.Wait()
	insertedTotal := 0
	for i := 0; i < N; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		insertedTotal += len(results[i].Inserted)
	}
	if insertedTotal != 5 {
		t.Fatalf("并发插入总数=%d（应恰 5）", insertedTotal)
	}
	if n := countTable(t, s, "cn35_player_units", uid); n != 5 {
		t.Fatalf("并发后 units=%d", n)
	}
	if n := countTable(t, s, "cn35_inventory_grants", uid); n != 5 {
		t.Fatalf("并发后 grants=%d", n)
	}
}

// TestGrantVersionConflict 同键不同模板集 → 拒绝且不再发。
func TestGrantVersionConflict(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "grant-v")
	cat, cfg := testCatalog(t), testGrantCfg()
	// 预登记同键异集台账（模拟旧版本发放残留）。
	if err := s.WithTx(func(tx *sql.Tx) error {
		return RecordGrantTx(tx, uid, cn35game.FiveStarGrantKey, 999)
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.GrantFiveStar(uid, cfg, cat)
	if !errors.Is(err, cn35game.ErrGrantVersionConflict) {
		t.Fatalf("应版本冲突: %v", err)
	}
	if n := countTable(t, s, "cn35_player_units", uid); n != 0 {
		t.Fatalf("冲突后仍发放: %d", n)
	}
}

// TestGrantUnknownAccount 账号不存在 → 报错零写入。
func TestGrantUnknownAccount(t *testing.T) {
	s := openStore(t, t.TempDir())
	var before int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cn35_inventory_grants`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err := s.GrantFiveStar("no-such-user", testGrantCfg(), testCatalog(t))
	if !errors.Is(err, cn35game.ErrGrantTarget) {
		t.Fatalf("应 ErrGrantTarget: %v", err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cn35_inventory_grants`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != before {
		t.Fatalf("失败发放留痕: got=%d want=%d", n, before)
	}
}

// TestGrantPreOwned 已合法拥有的模板不覆盖其行，台账仍完整登记。
func TestGrantPreOwned(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "grant-p")
	if err := s.WithTx(func(tx *sql.Tx) error {
		return InsertUnitTx(tx, uid, cn35game.UnitRecord{UnitID: 152, Level: 5})
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.GrantFiveStar(uid, testGrantCfg(), testCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Inserted) != 4 || len(res.AlreadyOwned) != 1 || res.AlreadyOwned[0] != 152 {
		t.Fatalf("结果: %+v", res)
	}
	var lv int
	if err := s.DB().QueryRow(`SELECT level FROM cn35_player_units WHERE user_id=? AND unit_id=152`,
		uid).Scan(&lv); err != nil || lv != 5 {
		t.Fatalf("预拥有行被覆盖: lv=%d err=%v", lv, err)
	}
	if n := countTable(t, s, "cn35_inventory_grants", uid); n != 5 {
		t.Fatalf("台账=%d", n)
	}
}

// TestGrantLeaderFix 悬空队长引用修正为礼包队长；已有效则不动。
func TestGrantLeaderFix(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "grant-l")
	if _, err := s.DB().Exec(`UPDATE cn35_player_profiles SET leader_unit_id='999' WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	res, err := s.GrantFiveStar(uid, testGrantCfg(), testCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	if !res.LeaderChanged || res.LeaderUnitID != 348 {
		t.Fatalf("队长修正: %+v", res)
	}
	var leader string
	if err := s.DB().QueryRow(`SELECT leader_unit_id FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&leader); err != nil || leader != "348" {
		t.Fatalf("库内 leader=%q err=%v", leader, err)
	}
}

// TestGrantIsolation 他账号不受影响；重启后台账保持。
func TestGrantIsolation(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uidA := seedAccount(t, s, "grant-ia")
	uidB := seedAccount(t, s, "grant-ib")
	if _, err := s.GrantFiveStar(uidA, testGrantCfg(), testCatalog(t)); err != nil {
		t.Fatal(err)
	}
	if n := countTable(t, s, "cn35_player_units", uidB); n != 0 {
		t.Fatalf("B 被波及: %d", n)
	}
	s.Close()

	// 重启：台账与库存保持。
	s2 := openStore(t, dir)
	applied, err := s2.GrantStatus(uidA, cn35game.FiveStarGrantKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 5 {
		t.Fatalf("重启后台账=%v", applied)
	}
	units, err := s2.Units(uidA)
	if err != nil || len(units) != 5 {
		t.Fatalf("重启后库存=%d err=%v", len(units), err)
	}
	// 07 读面视角：同实例键集合。
	ids := map[int]bool{}
	for _, u := range units {
		ids[u.UnitID] = true
	}
	for _, want := range []int{152, 157, 161, 230, 348} {
		if !ids[want] {
			t.Fatalf("缺卡 %d: %v", want, ids)
		}
	}
	s2.Close()
}

// TestGrantValidation 配置语义校验：非 5 张/重复/非五星/未知模板/leader 缺席。
func TestGrantValidation(t *testing.T) {
	cat := testCatalog(t)
	bad := func(name string, mutate func(*cn35game.FiveStarConfig)) {
		cfg := testGrantCfg()
		mutate(cfg)
		if err := cn35game.ValidateFiveStar(cat, cfg); err == nil {
			t.Fatalf("%s 应拒绝", name)
		}
	}
	bad("四张", func(c *cn35game.FiveStarConfig) { c.Units = c.Units[:4] })
	bad("重复模板", func(c *cn35game.FiveStarConfig) { c.Units[1].ID = 152 })
	bad("未知模板", func(c *cn35game.FiveStarConfig) { c.Units[0].ID = 999999 })
	bad("非五星", func(c *cn35game.FiveStarConfig) { c.Units[0].ID = 1 }) // catalog rare=1
	bad("leader 缺席", func(c *cn35game.FiveStarConfig) { c.LeaderUnitID = 999 })
	bad("等级非正", func(c *cn35game.FiveStarConfig) { c.Units[0].InitialLevel = 0 })
}

// TestGrantDevAccount 专用测试账号初始化幂等。
func TestGrantDevAccount(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid1, isNew, err := s.EnsureDevAccount("cn350-dev-test-account")
	if err != nil || uid1 == "" || !isNew {
		t.Fatalf("init: uid=%q new=%v err=%v", uid1, isNew, err)
	}
	uid2, isNew2, err := s.EnsureDevAccount("cn350-dev-test-account")
	if err != nil || uid2 != uid1 || isNew2 {
		t.Fatalf("重入幂等: uid=%q vs %q new=%v err=%v", uid1, uid2, isNew2, err)
	}
}
