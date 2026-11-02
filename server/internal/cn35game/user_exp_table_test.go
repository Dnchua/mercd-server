// user_exp_table_test.go 用户等级经验表：数据胶囊全表比对、抽查向量与结算口径。
// 曲线来源与证据等级见 user_exp_table.go 文件头与
// notes/proposed/2026-10-01-cn35-user-exp-curve.md。
package cn35game

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// loadUserExpCapsule 读取数据胶囊 TSV（level/need_exp/total_exp），返回全表。
func loadUserExpCapsule(t *testing.T) (need []int, total []int) {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "assets", "shared", "reference", "user_level_exp_table.tsv"))
	if err != nil {
		t.Fatalf("打开数据胶囊: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "level\t") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			t.Fatalf("胶囊行格式非法: %q", line)
		}
		lv, err1 := strconv.Atoi(parts[0])
		n, err2 := strconv.Atoi(parts[1])
		tot, err3 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || err3 != nil {
			t.Fatalf("胶囊行数值非法: %q", line)
		}
		if lv != len(need)+1 {
			t.Fatalf("胶囊等级断号: %d（期望 %d）", lv, len(need)+1)
		}
		need = append(need, n)
		total = append(total, tot)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("读胶囊: %v", err)
	}
	if len(need) != UserLevelMax {
		t.Fatalf("胶囊行数 = %d, want %d", len(need), UserLevelMax)
	}
	return need, total
}

// TestUserNeedExpTableMatchesCapsule 常量表与数据胶囊 2100 项逐值一致，
// 且胶囊自身累计链自洽（total(L+1)=total(L)+need(L)）——防转录/再生成漂移。
func TestUserNeedExpTableMatchesCapsule(t *testing.T) {
	need, total := loadUserExpCapsule(t)
	for lv, n := range need {
		if userNeedExpTable[lv] != n {
			t.Fatalf("level %d: 表值 %d != 胶囊 %d", lv+1, userNeedExpTable[lv], n)
		}
	}
	if total[0] != 0 {
		t.Fatalf("1 级累计应为 0, got %d", total[0])
	}
	for lv := 1; lv < UserLevelMax; lv++ {
		if total[lv] != total[lv-1]+need[lv-1] {
			t.Fatalf("胶囊累计链断裂 at level %d", lv+1)
		}
	}
}

// TestUserNeedExpVectors 抽查向量（与 wiki 原文数值对照）。
func TestUserNeedExpVectors(t *testing.T) {
	vectors := map[int]int{
		1: 100, 2: 100, 5: 100, 6: 400, 7: 1000, 8: 1166,
		99: 27310, 100: 27690, 101: 28072,
		999: 1178410, 1000: 1180590, 1001: 1200000, 1099: 1200000,
		1100: 1500000, 1199: 1500000, 1200: 1800000,
		1999: 3900000, 2000: 3900000, 2001: 4200000, 2099: 4200000,
		2100: 0,                 // 封顶无下一级
		0:    0, 2101: 0, -1: 0, // 越界
	}
	for lv, want := range vectors {
		if got := UserNeedExp(lv); got != want {
			t.Fatalf("UserNeedExp(%d) = %d, want %d", lv, got, want)
		}
	}
}

// TestUserNeedExpCappedVectors 落库/协议口径：满级回退末档，恒 >0。
func TestUserNeedExpCappedVectors(t *testing.T) {
	if got := UserNeedExpCapped(2100); got != 4200000 {
		t.Fatalf("UserNeedExpCapped(2100) = %d, want 4200000", got)
	}
	if got := UserNeedExpCapped(2101); got != 4200000 {
		t.Fatalf("UserNeedExpCapped(2101) = %d, want 4200000", got)
	}
	for lv := 1; lv <= UserLevelMax; lv++ {
		if got := UserNeedExpCapped(lv); got <= 0 {
			t.Fatalf("UserNeedExpCapped(%d) = %d, 应恒 >0", lv, got)
		}
	}
}

// TestUserExpWindowVectors 结算条等级需求窗口：本级起 32 档（窗口须覆盖
// 单场跨级，见 UserExpWindow 注释）；前缀值与生产抓包
// serve_quest_execute_1.json 的 [100,100] 逐值一致。
func TestUserExpWindowVectors(t *testing.T) {
	vectors := map[int][2]int64{
		1:    {100, 100},
		2:    {100, 100},
		5:    {100, 400},
		6:    {400, 1000},
		2098: {4200000, 4200000},
	}
	for lv, want := range vectors {
		got := UserExpWindow(lv)
		if got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("UserExpWindow(%d) 前缀 = %v, want %v", lv, got[:2], want)
		}
	}
	if got := UserExpWindow(1); len(got) != userExpWindowSize {
		t.Fatalf("UserExpWindow(1) 长度 = %d, want %d", len(got), userExpWindowSize)
	}
	// 表尾截到末档非 0 需求（needExpTable 末位 0 占位不可作除法分母）。
	if got := UserExpWindow(2099); len(got) != 1 || got[0] != 4200000 {
		t.Fatalf("UserExpWindow(2099) = %v, want [4200000]", got)
	}
	if got := UserExpWindow(2100); len(got) != 1 || got[0] != 4200000 {
		t.Fatalf("UserExpWindow(2100) = %v, want [4200000]（封顶回退末档）", got)
	}
}

// TestApplyUserExpVectors 结算入账：单级、跨多级、跨入封顶、封顶冻结。
func TestApplyUserExpVectors(t *testing.T) {
	vectors := []struct {
		name               string
		level, exp, gain   int
		wantLevel, wantExp int
	}{
		{"教程余量单级", 1, 50, 60, 2, 10},
		{"恰好差 1 点不升", 1, 99, 0, 1, 99},
		{"恰好整档", 1, 0, 100, 2, 0},
		{"连升 5 级", 1, 50, 550, 6, 100},
		{"跨入封顶保留余数", 2099, 4199995, 10, 2100, 5},
		{"封顶冻结", 2100, 5, 999999, 2100, 5},
		{"零增益", 6, 100, 0, 6, 100},
	}
	for _, v := range vectors {
		gotLevel, gotExp := ApplyUserExp(v.level, v.exp, v.gain)
		if gotLevel != v.wantLevel || gotExp != v.wantExp {
			t.Fatalf("%s: ApplyUserExp(%d,%d,%d) = (%d,%d), want (%d,%d)",
				v.name, v.level, v.exp, v.gain, gotLevel, gotExp, v.wantLevel, v.wantExp)
		}
	}
}

// TestStubAccountMatchesCurve 教程上下文桩（accounts.go）与经验表口径一致：
// 1 级需求 100、余量 50 落在 [0,need)。
func TestStubAccountMatchesCurve(t *testing.T) {
	if stubLevel != 1 {
		t.Fatalf("stubLevel = %d, 口径固定 1 级", stubLevel)
	}
	if stubMaxExp != UserNeedExp(stubLevel) {
		t.Fatalf("stubMaxExp = %d, 与经验表 need(1)=%d 不一致", stubMaxExp, UserNeedExp(stubLevel))
	}
	if stubExp < 0 || stubExp >= UserNeedExp(stubLevel) {
		t.Fatalf("stubExp = %d, 应在 [0,%d) 内", stubExp, UserNeedExp(stubLevel))
	}
}
