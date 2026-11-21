package cn35game

import (
	"strings"
	"testing"
)

// TestNormalizeGachaDescriptionNewlines CN3.5 客户端只认值内三字符 `\\n`
// （DecordReturn + GachaIndexParts.cs:419 两级替换），两字符 `\n` 会显示为字面。
func TestNormalizeGachaDescriptionNewlines(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`a\nb`, `a\\nb`},
		{`a\\nb`, `a\\nb`},
		{`\nhead`, `\\nhead`},
		{`a\nb\\nc\n`, `a\\nb\\nc\\n`},
		{"plain", "plain"},
	} {
		if got := NormalizeGachaDescriptionNewlines(tc.in); got != tc.want {
			t.Fatalf("Normalize(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
	if !strings.Contains(ScoutDiscountPool2Description, `\\n`) ||
		NormalizeGachaDescriptionNewlines(ScoutDiscountPool2Description) != ScoutDiscountPool2Description {
		t.Fatal("池 2 新文案必须已是归一形态")
	}
}

func TestGachaDiscountLabel(t *testing.T) {
	for _, tc := range []struct {
		paid, total int
		want        string
	}{
		{9, 10, "享9折"},
		{8, 10, "享8折"},
		{7, 9, "付7抽9"},
		{10, 10, "无折扣"},
	} {
		if got := gachaDiscountLabel(tc.paid, tc.total); got != tc.want {
			t.Fatalf("label(%d,%d)=%q want %q", tc.paid, tc.total, got, tc.want)
		}
	}
}

// TestUpgradePool2ScoutDiscountDef 只升级仍为旧口径 10/0 的钻石池 2，
// 管理员自定义值与文案保留（仅换行归一）。
func TestUpgradePool2ScoutDiscountDef(t *testing.T) {
	legacy := GachaPoolDef{PoolID: "2", Name: legacyPool2Name, Description: legacyPool2Description,
		PriceCoin: 5, MaxExec: 10}
	if !UpgradePool2ScoutDiscountDef(&legacy) || legacy.MaxExec != 9 || legacy.MaxBonus != 1 ||
		legacy.Name != ScoutDiscountPool2Name || legacy.Description != ScoutDiscountPool2Description {
		t.Fatalf("旧口径未升级: %+v", legacy)
	}
	custom := GachaPoolDef{PoolID: "2", Name: "自定义", Description: `自定义\n文案`, PriceCoin: 5, MaxExec: 5}
	UpgradePool2ScoutDiscountDef(&custom)
	if custom.MaxExec != 5 || custom.MaxBonus != 0 || custom.Name != "自定义" || custom.Description != `自定义\\n文案` {
		t.Fatalf("自定义池被覆盖: %+v", custom)
	}
	gold := GachaPoolDef{PoolID: "3", PricePoint: 200, MaxExec: 10, Description: `x\ny`}
	UpgradePool2ScoutDiscountDef(&gold)
	if gold.MaxExec != 10 || gold.MaxBonus != 0 || gold.Description != `x\\ny` {
		t.Fatalf("金币池只应做描述归一: %+v", gold)
	}
}
