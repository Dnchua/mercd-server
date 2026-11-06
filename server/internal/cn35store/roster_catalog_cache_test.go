// roster_catalog_cache_test.go rosterCatalog 进程内模板快照：命中不查库、
// 同进程写入后刷新、无模板回退静态目录、整表读失败退回逐行读、并发读写安全。
package cn35store

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// countingStore 统计逐行模板查询次数（快照路径应为 0）。
type countingStore struct {
	*Store
	perRow  atomic.Int64
	bulk    atomic.Int64
	bulkErr error
}

func (c *countingStore) UnitTemplate(id int) (*cn35game.UnitTemplate, bool, error) {
	c.perRow.Add(1)
	return c.Store.UnitTemplate(id)
}

func (c *countingStore) AllUnitTemplates() ([]*cn35game.UnitTemplate, error) {
	c.bulk.Add(1)
	if c.bulkErr != nil {
		return nil, c.bulkErr
	}
	return c.Store.AllUnitTemplates()
}

// perRowOnly 只实现逐行读面（未实现 UnitTemplateBulkReader 的旧读者）。
type perRowOnly struct{ s *Store }

func (p perRowOnly) UnitTemplate(id int) (*cn35game.UnitTemplate, bool, error) {
	return p.s.UnitTemplate(id)
}

func openCacheStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "cn35.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func saveTpl(t *testing.T, st *Store, id int, name string) {
	t.Helper()
	if err := st.SaveUnitTemplate(&cn35game.UnitTemplate{
		UnitID: id, Name: name, Rare: 3, Element: 1, Weapon: 1, Life: 100, Atk: 10,
		RawJSON: `{"hits":"1"}`,
	}, "test", "cache"); err != nil {
		t.Fatalf("SaveUnitTemplate(%d): %v", id, err)
	}
}

func TestRosterCatalogSnapshot(t *testing.T) {
	st := openCacheStore(t)
	base := testCatalog(t)
	ids := base.UnitIDs()
	if len(ids) < 2 {
		t.Fatal("静态目录单位不足")
	}
	withTpl, noTpl := ids[0], ids[1]
	saveTpl(t, st, withTpl, "缓存测试A")

	cs := &countingStore{Store: st}
	cat := cn35game.NewRosterCatalog(base, cs)

	// 命中：模板值生效、重复读取同一共享对象、零逐行查询、只整表读一次。
	u1, err := cat.Unit(withTpl)
	if err != nil || u1.Name != "缓存测试A" {
		t.Fatalf("模板覆盖未生效: %+v err=%v", u1, err)
	}
	if u1.Raw["hits"] != "1" {
		t.Fatalf("raw_json 未解析: %v", u1.Raw)
	}
	for i := 0; i < 100; i++ {
		u2, _ := cat.Unit(withTpl)
		if u2 != u1 {
			t.Fatal("快照命中应返回同一只读对象")
		}
	}
	if n := cs.perRow.Load(); n != 0 {
		t.Fatalf("快照路径不应逐行查库，实际 %d 次", n)
	}
	if n := cs.bulk.Load(); n != 1 {
		t.Fatalf("整表读取应只 1 次，实际 %d 次", n)
	}

	// 回退：无模板行 → 静态目录对象。
	want, _ := base.Unit(noTpl)
	if got, err := cat.Unit(noTpl); err != nil || got != want {
		t.Fatalf("无模板应回退静态目录: got=%p want=%p err=%v", got, want, err)
	}
	if _, err := cat.Unit(-1); err == nil {
		t.Fatal("未知单位应报错")
	}

	// 刷新：同进程写入提交后下一次读取即见新值（新增与修改都覆盖）。
	saveTpl(t, st, withTpl, "缓存测试A2")
	saveTpl(t, st, noTpl, "缓存测试B")
	if u, _ := cat.Unit(withTpl); u.Name != "缓存测试A2" {
		t.Fatalf("修改后未刷新: %q", u.Name)
	}
	if u, _ := cat.Unit(noTpl); u.Name != "缓存测试B" {
		t.Fatalf("新增模板后未刷新: %q", u.Name)
	}
	if n := cs.perRow.Load(); n != 0 {
		t.Fatalf("刷新也不应逐行查库，实际 %d 次", n)
	}
}

func TestRosterCatalogBulkFailureFallsBackPerRow(t *testing.T) {
	st := openCacheStore(t)
	base := testCatalog(t)
	id := base.UnitIDs()[0]
	saveTpl(t, st, id, "逐行回退")

	cs := &countingStore{Store: st, bulkErr: errors.New("boom")}
	cat := cn35game.NewRosterCatalog(base, cs)
	if u, err := cat.Unit(id); err != nil || u.Name != "逐行回退" {
		t.Fatalf("整表读失败应退回逐行读: %+v err=%v", u, err)
	}
	if cs.perRow.Load() != 1 {
		t.Fatalf("应逐行读 1 次，实际 %d", cs.perRow.Load())
	}
	// 故障恢复后下一次调用重建快照。
	cs.bulkErr = nil
	cat.Unit(id)
	before := cs.perRow.Load()
	cat.Unit(id)
	if cs.perRow.Load() != before {
		t.Fatal("恢复后应走快照")
	}
}

func TestRosterCatalogPerRowReaderUnchanged(t *testing.T) {
	st := openCacheStore(t)
	base := testCatalog(t)
	id := base.UnitIDs()[0]
	saveTpl(t, st, id, "旧读者")
	cat := cn35game.NewRosterCatalog(base, perRowOnly{st})
	if u, err := cat.Unit(id); err != nil || u.Name != "旧读者" {
		t.Fatalf("逐行读者行为应不变: %+v err=%v", u, err)
	}
}

// 并发读与写入刷新交错（配合 -race）：读者永远拿到某一代的完整值。
func TestRosterCatalogConcurrentReadWrite(t *testing.T) {
	st := openCacheStore(t)
	base := testCatalog(t)
	id := base.UnitIDs()[0]
	saveTpl(t, st, id, "v0")
	cat := cn35game.NewRosterCatalog(base, st)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				u, err := cat.Unit(id)
				if err != nil || u.Name == "" {
					t.Errorf("并发读异常: %+v err=%v", u, err)
					return
				}
			}
		}()
	}
	for i := 1; i <= 20; i++ {
		saveTpl(t, st, id, "v"+string(rune('0'+i%10)))
	}
	saveTpl(t, st, id, "final")
	close(stop)
	wg.Wait()
	if u, _ := cat.Unit(id); u.Name != "final" {
		t.Fatalf("最终值应为 final: %q", u.Name)
	}
}

