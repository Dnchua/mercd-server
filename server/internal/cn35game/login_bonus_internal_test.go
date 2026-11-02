// login_bonus_internal_test.go LOGIN350-02：泉水累积纯函数与月签到规则表
// （白盒：锚点推导、上限、按月/日界、28 格合计与属性轮换）。
package cn35game

import (
	"testing"
	"time"
)

func TestSpringAnchorSec(t *testing.T) {
	cases := []struct {
		name                        string
		receivable, created, nowSec int64
		want                        int64
	}{
		{"已领过：锚点=可领时刻-3600", 5000, 100, 9000, 1400},
		{"历史 0 值回落到建号时间", 0, 777, 9000, 777},
		{"建号时间也缺失回落到 now", 0, 0, 9000, 9000},
	}
	for _, tc := range cases {
		if got := SpringAnchorSec(tc.receivable, tc.created, tc.nowSec); got != tc.want {
			t.Fatalf("%s: anchor = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestSpringPending(t *testing.T) {
	const anchor = int64(1_000_000)
	cases := []struct {
		name            string
		hours           int64
		gold, guild, bp int64
	}{
		{"不足 1 小时无产出", 0, 0, 0, 0},
		{"3599 秒无产出", 0, 0, 0, 0},
		{"1 小时首档", 1, 1000, 1, 5},
		{"24 小时 BP 到顶", 24, 24000, 24, 120},
		{"48 小时金币/素材到顶", 48, 48000, 48, 120},
		{"50 小时全部封顶", 50, 48000, 48, 120},
	}
	for _, tc := range cases {
		now := anchor + tc.hours*3600
		if tc.name == "3599 秒无产出" {
			now = anchor + 3599
		}
		gold, guild, bp := SpringPending(anchor, now)
		if gold != tc.gold || guild != tc.guild || bp != tc.bp {
			t.Fatalf("%s: pending = %d/%d/%d, want %d/%d/%d",
				tc.name, gold, guild, bp, tc.gold, tc.guild, tc.bp)
		}
	}
	if SpringReady(0, 0, 0) {
		t.Fatal("零待领量不应可领")
	}
	if !SpringReady(0, 0, 5) {
		t.Fatal("只要任一类有量即可领")
	}
}

func TestSigninMonthBoundary(t *testing.T) {
	cases := []struct {
		name      string
		now       time.Time
		wantMonth string
		wantDay   int
	}{
		{"4:00 前归上一日",
			time.Date(2026, 11, 1, 3, 59, 0, 0, springLoc), "2026-10", 31},
		{"4:00 起归当日",
			time.Date(2026, 11, 1, 4, 0, 0, 0, springLoc), "2026-11", 1},
		{"UTC 同一瞬间按东八区",
			time.Date(2026, 10, 19, 20, 30, 0, 0, time.UTC), "2026-10", 20},
	}
	for _, tc := range cases {
		month, day := SigninMonth(tc.now.Unix())
		if month != tc.wantMonth || day != tc.wantDay {
			t.Fatalf("%s: %s/%d, want %s/%d", tc.name, month, day, tc.wantMonth, tc.wantDay)
		}
	}
}

func TestSigninNodeRewardTable(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, springLoc).Unix()
	var coin, medal, gold int64
	materialNodes, rewardNodes := 0, 0
	for node := int64(1); node <= SigninNodeCount; node++ {
		r := SigninNodeReward(node, now)
		coin += r.Coin
		medal += r.Medal
		gold += r.Gold
		if r.Gold > 0 || r.ItemContentID > 0 {
			rewardNodes++
		}
		if r.ItemContentID > 0 {
			materialNodes++
			if r.ItemContentID < 1 || r.ItemContentID > 15 {
				t.Fatalf("node %d 素材 content_id=%d 越界", node, r.ItemContentID)
			}
			if r.ItemCount <= 0 {
				t.Fatalf("node %d 素材数量=%d", node, r.ItemCount)
			}
		}
	}
	if coin != SigninRules.CoinTotal || medal != SigninRules.MedalTotal ||
		gold != SigninRules.GoldTotal {
		t.Fatalf("全月合计 = 钻 %d/徽章 %d/金币 %d, want %d/%d/%d",
			coin, medal, gold, SigninRules.CoinTotal, SigninRules.MedalTotal, SigninRules.GoldTotal)
	}
	if rewardNodes != SigninRules.RewardCount {
		t.Fatalf("培养奖励份数 = %d, want %d", rewardNodes, SigninRules.RewardCount)
	}
	if materialNodes != 16 {
		t.Fatalf("素材节点数 = %d, want 16（其余 12 格是金币）", materialNodes)
	}
	// 普通日 50/25；周节点 200/200；第 28 格 200/300。
	if r := SigninNodeReward(1, now); r.Coin != 50 || r.Medal != 25 {
		t.Fatalf("普通日 = %+v", r)
	}
	if r := SigninNodeReward(7, now); r.Coin != 200 || r.Medal != 200 || r.ItemCount != 4 {
		t.Fatalf("周节点 = %+v", r)
	}
	if r := SigninNodeReward(28, now); r.Coin != 200 || r.Medal != 300 || r.ItemCount != 6 {
		t.Fatalf("第 28 格 = %+v", r)
	}
}

func TestSigninAttributeRotation(t *testing.T) {
	oct := time.Date(2026, 10, 20, 10, 0, 0, 0, springLoc).Unix()
	nov := time.Date(2026, 11, 20, 10, 0, 0, 0, springLoc).Unix()
	a := SigninNodeReward(1, oct)
	b := SigninNodeReward(1, nov)
	if a.ItemContentID == b.ItemContentID {
		t.Fatalf("相邻月份的起始属性应错开: %d vs %d", a.ItemContentID, b.ItemContentID)
	}
	// 同一月份内 16 个素材节点覆盖五属性（每种至少 2 次）。
	seen := map[int64]int{}
	for node := int64(1); node <= SigninNodeCount; node++ {
		r := SigninNodeReward(node, oct)
		if r.ItemContentID > 0 {
			seen[r.ItemContentID]++
		}
	}
	if len(seen) < 5 {
		t.Fatalf("素材属性覆盖不足: %v", seen)
	}
}

func TestSigninNodeOpen(t *testing.T) {
	cases := []struct {
		node, day int
		want      bool
	}{
		{1, 1, true},
		{28, 28, true},
		{28, 27, false},
		{29, 30, false},
		{0, 30, false},
		{5, 4, false},
		{5, 31, true},
	}
	for _, tc := range cases {
		if got := SigninNodeOpen(tc.node, tc.day); got != tc.want {
			t.Fatalf("SigninNodeOpen(%d,%d) = %v, want %v", tc.node, tc.day, got, tc.want)
		}
	}
}
