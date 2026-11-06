// cardcatalog_test.go PLAY350-04 目录加载与质量策略测试。
// 真实快照用例直接读 assets/shared/cards（仓库内相对路径）；
// 异常用例用 t.TempDir() 构造最小 JSON 对，不改动共享快照。
package cardcatalog_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

var realDir = filepath.Join("..", "..", "assets", "shared", "cards")

// writeDir 构造最小合法目录（SHA256SUMS + version + 两个 JSON）。
func writeDir(t *testing.T, units, monsters string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("units.json", units)
	write("monsters.json", monsters)
	sum := func(name string) string {
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		h := sha256.Sum256(raw)
		return fmt.Sprintf("%x", h[:])
	}
	write("SHA256SUMS", fmt.Sprintf("%s  units.json\n%s  monsters.json\n", sum("units.json"), sum("monsters.json")))
	write("units.json.version", "209901010000")
	write("monsters.json.version", "209901010000")
	return dir
}

func TestLoadRealSnapshot(t *testing.T) {
	if _, err := os.Stat(realDir); err != nil {
		t.Skipf("cards 快照不可用: %v", err)
	}
	c, err := cardcatalog.Load(realDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m := c.Meta()
	if m.UnitCount != 1829 || m.MonsterCount != 1052 {
		t.Fatalf("计数 %d/%d", m.UnitCount, m.MonsterCount)
	}
	if m.UnitsVersion != "202009041551" || m.MonstersVersion != "201906151559" {
		t.Fatalf("版本戳 %q/%q", m.UnitsVersion, m.MonstersVersion)
	}
	// 抽查 348（leader_unit_id 兼容值）与一条五星记录字段形态
	u, err := c.Unit(348)
	if err != nil {
		t.Fatalf("unit 348: %v", err)
	}
	if u.Name == "" || u.Rare == 0 {
		t.Fatalf("unit 348 字段: %+v", u)
	}
	// 乱序魔宠索引仍按 id 命中（README 已知 1066→822 乱序）
	if _, err := c.Monster(822); err != nil {
		t.Fatalf("monster 822: %v", err)
	}
	// 已知缺口：units 527 五系补正为"暂缺" → 指针 nil + Missing 记录
	u527, err := c.Unit(527)
	if err != nil {
		t.Fatalf("unit 527: %v", err)
	}
	if u527.Rates.Fire != nil {
		t.Fatalf("unit 527 fire 应为 nil（暂缺）: %v", *u527.Rates.Fire)
	}
	found := false
	for _, f := range u527.Missing {
		if f == "fire" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unit 527 Missing 缺 fire: %v", u527.Missing)
	}
	if len(c.Report().Warn) == 0 {
		t.Fatal("已知缺口应产生非空 Warn 报告")
	}
	// 排序键升序
	ids := c.UnitIDs()
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatal("UnitIDs 未升序")
		}
	}
}

// TestCountryIDDeterministic：未知国家使用不与客户端筛选值冲突的重建 ID。
func TestCountryIDDeterministic(t *testing.T) {
	dir := writeDir(t,
		`[{"id":5,"name":"B1","rare":1,"element":1,"weapon":1,"type":1,"life":1,"atk":1,"aarea":1,"country":"B国"},
		  {"id":3,"name":"A1","rare":1,"element":1,"weapon":1,"type":1,"life":1,"atk":1,"aarea":1,"country":"A国"},
		  {"id":7,"name":"A2","rare":1,"element":1,"weapon":1,"type":1,"life":1,"atk":1,"aarea":1,"country":"A国"},
		  {"id":12,"name":"B2","rare":1,"element":1,"weapon":1,"type":1,"life":1,"atk":1,"aarea":1,"country":"B国"},
		  {"id":9,"name":"N","rare":1,"element":1,"weapon":1,"type":1,"life":1,"atk":1,"aarea":1}]`,
		`[]`)
	c, err := cardcatalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cidOf := func(id int) int {
		u, err := c.Unit(id)
		if err != nil {
			t.Fatalf("unit %d: %v", id, err)
		}
		return u.CountryID
	}
	if cidOf(3) != 200000 || cidOf(7) != 200000 {
		t.Fatalf("同国应同 id=200000: %d/%d", cidOf(3), cidOf(7))
	}
	if cidOf(5) != 200001 || cidOf(12) != 200001 {
		t.Fatalf("B国应同 id=200001: %d/%d", cidOf(5), cidOf(12))
	}
	if cidOf(9) != 0 {
		t.Fatalf("无国家应为 0: %d", cidOf(9))
	}
	// 真实快照：152（沙漠之国）CountryID>0，且与同国单位同值。
	if _, err := os.Stat(realDir); err != nil {
		t.Skipf("cards 快照不可用: %v", err)
	}
	rc, err := cardcatalog.Load(realDir)
	if err != nil {
		t.Fatalf("Load(realDir): %v", err)
	}
	u152, err := rc.Unit(152)
	if err != nil {
		t.Fatal(err)
	}
	if u152.Country != "沙漠之国" || u152.CountryID != 10 {
		t.Fatalf("unit 152 CountryID: %+v", u152)
	}
}

func TestUnknownID(t *testing.T) {
	dir := writeDir(t,
		`[{"id":1,"name":"A","rare":3,"element":1,"weapon":1,"type":1,"life":100,"atk":100,"aarea":10}]`,
		`[{"id":7,"name":"M","rare":2,"element":2,"skill":"s"}]`)
	c, err := cardcatalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Unit(999); !errors.Is(err, cardcatalog.ErrUnknown) {
		t.Fatalf("未知 unit: %v", err)
	}
	if _, err := c.Monster(999); !errors.Is(err, cardcatalog.ErrUnknown) {
		t.Fatalf("未知 monster: %v", err)
	}
	u, _ := c.Unit(1)
	if u.Life != 100 || u.Name != "A" {
		t.Fatalf("unit: %+v", u)
	}
}

func TestDuplicateIDFatal(t *testing.T) {
	dir := writeDir(t,
		`[{"id":1,"name":"A","rare":1,"element":1,"weapon":1,"type":1,"life":1,"atk":1,"aarea":1},
		  {"id":1,"name":"B","rare":1,"element":1,"weapon":1,"type":1,"life":1,"atk":1,"aarea":1}]`,
		`[]`)
	_, err := cardcatalog.Load(dir)
	var le *cardcatalog.LoadError
	if !errors.As(err, &le) {
		t.Fatalf("重复 ID 应为致命错误: %v", err)
	}
}

func TestHashMismatchFatal(t *testing.T) {
	dir := writeDir(t, `[]`, `[]`)
	// 破坏 SHA256SUMS
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte("00  units.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cardcatalog.Load(dir); err == nil {
		t.Fatal("哈希不匹配应为致命错误")
	}
}

func TestBadStructureFatal(t *testing.T) {
	dir := writeDir(t, `{"not":"array"}`, `[]`)
	if _, err := cardcatalog.Load(dir); err == nil {
		t.Fatal("顶层非数组应为致命错误")
	}
}

func TestMissingRequiredFatal(t *testing.T) {
	dir := writeDir(t,
		`[{"id":1,"rare":3}]`, // 缺 name/element/weapon/type/life/atk/aarea
		`[]`)
	if _, err := cardcatalog.Load(dir); err == nil {
		t.Fatal("缺必需字段应为致命错误")
	}
}

func TestPlaceholderAndMissingAreWarn(t *testing.T) {
	dir := writeDir(t,
		`[{"id":1,"name":"A","rare":5,"element":1,"weapon":2,"type":1,"anum":2,"life":900,"atk":1200,"aarea":15,
		   "fire":"暂缺","aqua":1.0,"wind":1.4,"light":1.0,"dark":0.71,"hits":"3","age":"16","gender":2}]`,
		`[{"id":5,"name":"M","rare":4,"element":3,"skill":"造成nnn伤害","sklsp":10,"sklcd":1.5,"aarea":30},
		  {"id":2,"name":"B","rare":1,"element":1,"skill":"x","sklsp":1}]`)
	c, err := cardcatalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := c.Unit(1)
	if u.Rates.Fire != nil || u.Rates.Wind == nil || *u.Rates.Wind != 1.4 {
		t.Fatalf("rates: %+v", u.Rates)
	}
	if u.Hits == nil || *u.Hits != 3 {
		t.Fatalf("hits 数字串应解析为 3: %v", u.Hits)
	}
	if u.Age == nil || *u.Age != "16" {
		t.Fatalf("age: %v", u.Age)
	}
	// 乱序魔宠 id=2 仍可查
	if _, err := c.Monster(2); err != nil {
		t.Fatalf("乱序 monster: %v", err)
	}
	m5, _ := c.Monster(5)
	if !contains(m5.Missing, "mspd") {
		t.Fatalf("mspd 缺失应记入 Missing: %v", m5.Missing)
	}
	warn := c.Report().Warn
	if len(warn) == 0 {
		t.Fatal("暂缺/nnn 应产生警告")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
