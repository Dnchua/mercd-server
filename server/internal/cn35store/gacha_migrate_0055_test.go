// gacha_migrate_0055_test.go migrate-0055 存量库卡池升级：只改激活快照、
// 不连带发布未发布草稿、管理员自定义值不覆盖、无改动也落执行标记。
package cn35store

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

const (
	legacyPool2NameForTest = "钻石招募"
	legacyPool2DescForTest = `使用钻石招募同伴。\n每连续 10 次至少获得 1 名 SR 以上。`
	legacyPool3DescForTest = `使用金币招募同伴。\n本池无保底，招募概率固定。`
)

// seedLegacyGacha 构造 M6 口径的存量库：池 2 为 10/0 + 旧文案，池 3 旧单反斜杠描述。
func seedLegacyGacha(t *testing.T, s *Store, pool2Exec int) {
	t.Helper()
	if _, _, err := cn35game.SeedGachaRoster(s, testImport(t), "seed"); err != nil {
		t.Fatalf("种子化: %v", err)
	}
	pools, err := s.GachaPools()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pools {
		switch p.PoolID {
		case "2":
			p.Name, p.Description = legacyPool2NameForTest, legacyPool2DescForTest
			p.MaxExec, p.MaxBonus = pool2Exec, 0
		case "3":
			p.Description = legacyPool3DescForTest
		default:
			continue
		}
		if err := s.SaveGachaPoolDef(p, "admin"); err != nil {
			t.Fatalf("回写旧口径池 %s: %v", p.PoolID, err)
		}
	}
	if _, err := s.PublishGachaDraft("admin", "legacy"); err != nil {
		t.Fatalf("发布旧口径: %v", err)
	}
}

func activePool(t *testing.T, s *Store, id string) (cn35game.GachaPoolDef, int) {
	t.Helper()
	cfg, err := s.ActiveGachaConfig()
	if err != nil || cfg == nil {
		t.Fatalf("激活配置: %v nil=%v", err, cfg == nil)
	}
	for _, d := range cn35game.GachaPoolDocumentsForTest(cfg) {
		if idStrForTest(d["id"]) == id {
			return cn35game.GachaPoolDef{
				PoolID:      id,
				Name:        d["name"].(string),
				Description: d["description"].(string),
				MaxExec:     d["max_executable_count"].(int),
				MaxBonus:    d["max_execute_bonus"].(int),
			}, len(cfg.MembersByPool[id])
		}
	}
	t.Fatalf("激活配置缺池 %s", id)
	return cn35game.GachaPoolDef{}, 0
}

func idStrForTest(v any) string {
	switch x := v.(type) {
	case int:
		return strconv.Itoa(x)
	case string:
		return x
	}
	return ""
}

func draftPool(t *testing.T, s *Store, id string) cn35game.GachaPoolDef {
	t.Helper()
	pools, err := s.GachaPools()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pools {
		if p.PoolID == id {
			return p
		}
	}
	t.Fatalf("草稿缺池 %s", id)
	return cn35game.GachaPoolDef{}
}

func TestUpgradeGachaPool2DiscountLegacyActiveOnly(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedLegacyGacha(t, s, 10)
	_, legacy3Members := activePool(t, s, "3")

	// 管理员未发布的草稿编辑：金币池去掉一名成员。迁移不得把它带上线。
	ms, err := s.GachaDraftMembers()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceDraftMembers("3", append([]cn35game.GachaDraftMember{}, ms["3"][1:]...), "admin"); err != nil {
		t.Fatal(err)
	}

	v, published, err := s.UpgradeGachaPool2Discount("migrate-0055", "test")
	if err != nil {
		t.Fatalf("升级: %v", err)
	}
	if !published || v == nil {
		t.Fatal("旧口径激活快照应发布升级版本")
	}
	p2, _ := activePool(t, s, "2")
	if p2.MaxExec != 9 || p2.MaxBonus != 1 ||
		p2.Name != cn35game.ScoutDiscountPool2Name || p2.Description != cn35game.ScoutDiscountPool2Description {
		t.Fatalf("激活池 2 未按 9+1 升级: %+v", p2)
	}
	p3, got3Members := activePool(t, s, "3")
	if p3.Description != `使用金币招募同伴。\\n本池无保底，招募概率固定。` {
		t.Fatalf("激活池 3 描述未做换行归一: %q", p3.Description)
	}
	if got3Members != legacy3Members {
		t.Fatalf("未发布的草稿成员被连带上线: 激活金币池成员=%d want %d", got3Members, legacy3Members)
	}
	if d2 := draftPool(t, s, "2"); d2.MaxExec != 9 || d2.MaxBonus != 1 {
		t.Fatalf("草稿池 2 未同步 9+1: %+v", d2)
	}
	if d3 := draftPool(t, s, "3"); !strings.Contains(d3.Description, `\\n`) {
		t.Fatalf("草稿池 3 描述未归一: %q", d3.Description)
	}
	ms2, _ := s.GachaDraftMembers()
	if len(ms2["3"]) != len(ms["3"])-1 {
		t.Fatalf("草稿成员应原样保留: %d want %d", len(ms2["3"]), len(ms["3"])-1)
	}
	marks, err := s.GachaMigrationMarkers()
	if err != nil || !marks["migrate-0055"] {
		t.Fatalf("应落 migrate-0055 标记: %v %v", marks, err)
	}

	// 再次执行：快照已是新口径，无改动不发布。
	if _, again, err := s.UpgradeGachaPool2Discount("migrate-0055", "test"); err != nil || again {
		t.Fatalf("重复执行不应再发布: published=%v err=%v", again, err)
	}
}

func TestUpgradeGachaPool2DiscountKeepsAdminCustom(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedLegacyGacha(t, s, 5) // 管理员自定义 5 连 0 赠
	if _, _, err := s.UpgradeGachaPool2Discount("migrate-0055", "test"); err != nil {
		t.Fatal(err)
	}
	p2, _ := activePool(t, s, "2")
	if p2.MaxExec != 5 || p2.MaxBonus != 0 {
		t.Fatalf("管理员自定义 max_exec/max_bonus 被覆盖: %+v", p2)
	}
	if d2 := draftPool(t, s, "2"); d2.MaxExec != 5 || d2.MaxBonus != 0 {
		t.Fatalf("草稿池 2 自定义值被覆盖: %+v", d2)
	}
	marks, _ := s.GachaMigrationMarkers()
	if !marks["migrate-0055"] {
		t.Fatal("即使池 2 未升级也应落执行标记")
	}
}

func TestUpgradeGachaPool2DiscountFreshSeedMarksOnly(t *testing.T) {
	s := openStore(t, t.TempDir())
	if _, _, err := cn35game.SeedGachaRoster(s, testImport(t), "seed"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ActiveGachaConfig()
	_, published, err := s.UpgradeGachaPool2Discount("migrate-0055", "test")
	if err != nil || published {
		t.Fatalf("新种子库无需发布: published=%v err=%v", published, err)
	}
	after, _ := s.ActiveGachaConfig()
	if before.Version != after.Version {
		t.Fatalf("激活版本不应变化: %d -> %d", before.Version, after.Version)
	}
	marks, _ := s.GachaMigrationMarkers()
	if !marks["migrate-0055"] {
		t.Fatal("无改动也必须落 migrate-0055 标记，防止管理员后续改回 10/0 后被重启覆盖")
	}
}
