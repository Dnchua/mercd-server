// signin.go 每月 28 格签到的领域定义：LOGIN350-02。
//
// ## 业务口径（用户 2026-10-20 给定）
//
// 28 格月签到：普通 24 次各 50 钻石 + 25 徽章 + 1 份培养奖励；第 7/14/21 次
// 各 200 钻石 + 200 徽章 + 较高阶培养素材；第 28 次 200 钻石 + 300 徽章 +
// 高阶培养素材。全月合计 2,000 钻石 / 1,500 徽章 / 28 份培养奖励，金币额外
// 约 300,000。用户明确：公会建设素材不入月签，BP 不入月签；允许月底补齐漏签，
// 29～31 天月份多出的日期用作补签缓冲。
//
// ## 解锁与补签
//
// 第 N 格在第 N 个日历日解锁（月界按东八区 (now-4h)，与面包店等同口径），
// 解锁后到当月结束前都可补领——因此不必每天全勤；但 2 月只有 28 天，第 28 格
// 的窗口只有 1 天（无缓冲），与用户描述一致。
//
// ## 属性轮换（用户 2026-10-20 决策：不做真「自选」）
//
// 客户端没有「多选一」纯配置 UI，用户选择「按周期轮换固定属性」。素材节点按
// node 顺序取第 materialIndex 个素材节点，属性 = (materialIndex-1+monthShift)%5，
// monthShift = (year*12+month)%5，使每月起始属性错开、长期覆盖火水风光暗五属性。
//
// ## 数值来源
//
// 仓库内没有进化消耗表（evolution_map.need 服务端下发空表、unit_upgrade_config
// 未开放），素材数量是按仓库现有真实成本做的**重建运营初值**：魔宠强化每件素材
// +0.02 Size（unit_ops.go）、符文强化花 +2.0（skills.go）、★5 Lv1→70 金币
// 278,250（exp_table.go:25）。见 notes/proposed/2026-10-20-cn35-spring-signin.md。
package cn35game

import (
	"errors"
	"time"
)

// ErrSigninNotReady 本月该节点已领、或节点尚未解锁/已超过 28 格。
var ErrSigninNotReady = errors.New("cn35game: 签到节点当前不可领取")

// ErrSigninInvalidNode 节点号非法（非 1..28）。
var ErrSigninInvalidNode = errors.New("cn35game: 签到节点号非法")

// SigninNodeCount 每月的签到节点数。
const SigninNodeCount = 28

// SigninDayRefreshHour 日界：东八区 04:00（与面包店/会战等同口径）。
const SigninDayRefreshHour = 4

// signinRewardKind 培养奖励形态；0 表示该节点不发素材。
type signinRewardKind int

const (
	signinKindNone signinRewardKind = iota
	signinKindGold
	signinKindSeed
	signinKindBud
	signinKindFlower
)

// signinNodeSpec 单个节点的固定奖励（属性在计算时轮换）。
type signinNodeSpec struct {
	Coin  int64
	Medal int64
	Kind  signinRewardKind
	Count int64
}

// SigninRules 月签到规则汇总（便于 admin/测试引用）。
var SigninRules = struct {
	NodeCount      int
	DayRefreshHour int
	CoinTotal      int64
	MedalTotal     int64
	GoldTotal      int64
	RewardCount    int
}{
	NodeCount:      SigninNodeCount,
	DayRefreshHour: SigninDayRefreshHour,
	CoinTotal:      2000,
	MedalTotal:     1500,
	GoldTotal:      300000,
	RewardCount:    28,
}

// signinGoldNodes 金币日（12 天，全月 300,000）。
var signinGoldNodes = []int{2, 4, 6, 9, 11, 13, 16, 18, 20, 23, 25, 27}

// signinSeedNodes 种子日（4 天）。
var signinSeedNodes = []int{1, 8, 15, 22}

// signinBudNodes 花蕾日（4 天）。
var signinBudNodes = []int{3, 10, 17, 24}

// signinFlowerNodes 花日（4 天）。
var signinFlowerNodes = []int{5, 12, 19, 26}

// signinWeeklyNodes 周节点（3 天）。
var signinWeeklyNodes = []int{7, 14, 21}

// signinNodeSpecs 28 格固定奖励表（0 基下标 = node-1）。
var signinNodeSpecs = buildSigninNodeSpecs()

func buildSigninNodeSpecs() [SigninNodeCount]signinNodeSpec {
	var specs [SigninNodeCount]signinNodeSpec
	// 普通日：50 钻 + 25 徽章 + 1 份培养奖励。
	for _, n := range signinGoldNodes {
		specs[n-1] = signinNodeSpec{Coin: 50, Medal: 25, Kind: signinKindGold, Count: 25000}
	}
	for _, n := range signinSeedNodes {
		specs[n-1] = signinNodeSpec{Coin: 50, Medal: 25, Kind: signinKindSeed, Count: 5}
	}
	for _, n := range signinBudNodes {
		specs[n-1] = signinNodeSpec{Coin: 50, Medal: 25, Kind: signinKindBud, Count: 3}
	}
	for _, n := range signinFlowerNodes {
		specs[n-1] = signinNodeSpec{Coin: 50, Medal: 25, Kind: signinKindFlower, Count: 1}
	}
	// 周节点：200 钻 + 200 徽章 + 较高阶素材（花 ×4）。
	for _, n := range signinWeeklyNodes {
		specs[n-1] = signinNodeSpec{Coin: 200, Medal: 200, Kind: signinKindFlower, Count: 4}
	}
	// 第 28 格：200 钻 + 300 徽章 + 高阶素材（花 ×6）。
	specs[SigninNodeCount-1] = signinNodeSpec{Coin: 200, Medal: 300, Kind: signinKindFlower, Count: 6}
	return specs
}

// SigninReward 一个节点的实际发奖（属性已按当月轮换确定）。
type SigninReward struct {
	Node          int
	Coin          int64 // 钻石（cn35_player_balances.currency=coin）
	Medal         int64 // 徽章（currency=medal）
	Gold          int64 // 金币（currency=gacha_point）
	ItemContentID int64 // 培养素材 content_id（0=无素材）
	ItemCount     int64
}

// SigninMonth 返回东八区 (now-4h) 的月份键（YYYY-MM）与当日（1..31）。
func SigninMonth(nowSec int64) (monthKey string, day int) {
	local := time.Unix(nowSec, 0).In(springLoc).
		Add(-time.Duration(SigninDayRefreshHour) * time.Hour)
	return local.Format("2006-01"), local.Day()
}

// SigninNodeReward 计算第 node 格（1..28）的实际奖励；属性按 (materialIndex-1+
// monthShift)%5 轮换。非法 node 返回零值。
func SigninNodeReward(node, nowSec int64) SigninReward {
	if node < 1 || node > SigninNodeCount {
		return SigninReward{}
	}
	spec := signinNodeSpecs[node-1]
	r := SigninReward{Node: int(node), Coin: spec.Coin, Medal: spec.Medal}
	if spec.Kind == signinKindGold {
		r.Gold = spec.Count
		return r
	}
	if spec.Kind == signinKindNone || spec.Count <= 0 {
		return r
	}
	local := time.Unix(nowSec, 0).In(springLoc).
		Add(-time.Duration(SigninDayRefreshHour) * time.Hour)
	monthShift := (local.Year()*12 + int(local.Month())) % 5
	materialIndex := 0
	for i := int64(1); i <= node; i++ {
		if signinNodeSpecs[i-1].Kind != signinKindGold && signinNodeSpecs[i-1].Kind != signinKindNone {
			materialIndex++
		}
	}
	attr := (materialIndex - 1 + monthShift) % 5 // 0=火 1=水 2=风 3=光 4=暗
	form := 1                                    // 1=种子 2=花蕾 3=花
	switch spec.Kind {
	case signinKindBud:
		form = 2
	case signinKindFlower:
		form = 3
	}
	r.ItemContentID = int64(attr + 1 + (form-1)*5)
	r.ItemCount = spec.Count
	return r
}

// SigninNodeOpen 第 node 格在当前日历日是否已解锁（node<=min(28, day)）。
func SigninNodeOpen(node, day int) bool {
	return node >= 1 && node <= SigninNodeCount && node <= day
}

// SigninState 某个账号当月的签到状态（读面）。
type SigninState struct {
	MonthKey     string
	Day          int
	ClaimedNodes []int
}

// SigninClaimed 该账号本月是否已领过某节点。
func (st *SigninState) SigninClaimed(node int) bool {
	for _, n := range st.ClaimedNodes {
		if n == node {
			return true
		}
	}
	return false
}

// SigninClaimResult 一次签到领取结果。Granted=false 表示不可领（零写入）。
type SigninClaimResult struct {
	Granted bool
	Reward  SigninReward
}

// SigninStore 月签到读/领面（cn35store.Store 实现）。
type SigninStore interface {
	SigninState(userID string, nowSec int64) (*SigninState, error)
	// ClaimSignin 单事务：节点合法性/解锁/未领校验 → 台账 INSERT（主键兜底
	// 并发与重放）→ 钻石/徽章/金币/素材入账。不可领返回 ErrSigninNotReady。
	ClaimSignin(userID string, nowSec int64, node int) (*SigninClaimResult, error)
}
