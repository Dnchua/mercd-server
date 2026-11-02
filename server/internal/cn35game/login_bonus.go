// login_bonus.go 不可思议之泉（客户端 LoginBonus 页）域定义：LOGIN350-02。
//
// ## 业务口径（用户 2026-10-20 澄清，取代 LOGIN350-01 的「每日固定 1000 金币」）
//
// 泉水按时间累积金币（gacha_point）/公会建设素材（guild_asset）/BP 三种资源，
// 各自有每小时产量与泉水储存上限，**只要有量就能领**，也可以攒到上限后长期不领：
//
//	资源            每小时   每天    泉水上限   存满
//	金币 gold        1000    24000    48000    48h
//	公会建设素材      1       24       48      48h
//	BP               5      120      120      24h
//
// BP 按用户口径**允许溢出持有**（入账不受 max_bp 截断；0035 迁移已移除档案表的
// bp<=max_bp 约束）。
//
// ## 时间语义（客户端契约不变，服务端重建）
//
// 客户端 `LoginBonus.cs:170-173` 用 `user.login_bonus_receivable_sec -
// Common.GetServerTime()` 得倒计时，`IconSpringParts.cs:10-22` 用
// `<= now` 显示 Home 红点。因此该字段仍是「可领时刻 Unix 秒」：
//
//	可领时刻 = 累积起点 anchor + 3600s
//	anchor = 上次领取时刻；历史/建号值 0 时回落到 cn35_player_profiles.created_at
//
// 即长期未登录的账号首次进入时已接近/达到上限（符合「可以累积很久再领取」），
// 新账号从建号时刻起累积。领取后可领时刻推进到 now+3600s。
//
// ## 奖励来源
//
// 用户直接给定的产出表（上表），不是原服取证数值；原服刷新/产出未取证的部分
// 在本文件与 notes/proposed/2026-10-20-cn35-spring-signin.md 标注为重建规则。
package cn35game

import (
	"errors"
	"time"
)

// ErrLoginBonusNotReady 当前没有可领量（累积不足 1 小时且无残留）。
// 领取接口按客户端既有空态分支处理（contents=[] → 倒计时弹窗），
// 不映射为错误弹窗。
var ErrLoginBonusNotReady = errors.New("cn35game: 不可思议之泉当前没有可领取量")

// SpringIntervalSec 单位累积周期（秒）：三种资源都在 1 小时内至少产出 1 单位，
// 故「有量可领」的最短间隔为 1 小时。客户端倒计时也以此为准。
const SpringIntervalSec int64 = 3600

// SpringRules 泉水产出规则（用户 2026-10-20 给定值，见文件头）。
var SpringRules = struct {
	GoldPerHour  int64
	GoldCap      int64
	GuildPerHour int64
	GuildCap     int64
	BPPerHour    int64
	BPCap        int64
	IntervalSec  int64
}{
	GoldPerHour:  1000,
	GoldCap:      48000,
	GuildPerHour: 1,
	GuildCap:     48,
	BPPerHour:    5,
	BPCap:        120,
	IntervalSec:  SpringIntervalSec,
}

// SpringState 泉水当前累积状态（读面，不写库）。
type SpringState struct {
	AnchorSec     int64 // 累积起点（上次领取时刻；0 值已回落到 created_at）
	Gold          int64 // 待领金币
	GuildAsset    int64 // 待领公会建设素材
	BattlePoint   int64 // 待领 BP
	ReceivableSec int64 // 可领时刻 = anchor + 3600
}

// SpringClaimResult 一次领取结果。Granted=false 表示没有可领量，此时不产生任何写入。
type SpringClaimResult struct {
	Granted     bool
	Gold        int64
	GuildAsset  int64
	BattlePoint int64
}

// SpringStore 泉水读/领面（cn35store.Store 实现）。
type SpringStore interface {
	// SpringState 读当前累积状态；只读，不写库。
	SpringState(userID string, nowSec int64) (*SpringState, error)
	// ClaimSpring 单事务领取全部待领量：条件推进可领时刻（CAS）→ 三资源入账
	// → 领取台账。没有可领量或 CAS 失败返回 ErrLoginBonusNotReady 且零写入。
	// BP 入账不按 max_bp 截断（允许溢出持有）。
	ClaimSpring(userID string, nowSec int64) (*SpringClaimResult, error)
}

// springLoc 泉水/签到周期时区（国服东八区，无夏令时；口径同 dailyShopLoc）。
var springLoc = time.FixedZone("UTC+8", 8*3600)

// SpringAnchorSec 由档案列推出累积起点：login_bonus_receivable_sec>0 时
// anchor = receivable-3600；=0 时回落到账号创建时间（长期未登录即接近上限）。
// createdSec<=0 时回落到 nowSec。
func SpringAnchorSec(receivableSec, createdSec, nowSec int64) int64 {
	if receivableSec > SpringIntervalSec {
		return receivableSec - SpringIntervalSec
	}
	if createdSec > 0 {
		return createdSec
	}
	return nowSec
}

// SpringPending 按锚点算三种资源的待领量（各自独立封顶）。
func SpringPending(anchorSec, nowSec int64) (gold, guild, bp int64) {
	if nowSec <= anchorSec {
		return 0, 0, 0
	}
	hours := (nowSec - anchorSec) / SpringIntervalSec
	if hours <= 0 {
		return 0, 0, 0
	}
	gold = hours * SpringRules.GoldPerHour
	if gold > SpringRules.GoldCap {
		gold = SpringRules.GoldCap
	}
	guild = hours * SpringRules.GuildPerHour
	if guild > SpringRules.GuildCap {
		guild = SpringRules.GuildCap
	}
	bp = hours * SpringRules.BPPerHour
	if bp > SpringRules.BPCap {
		bp = SpringRules.BPCap
	}
	return gold, guild, bp
}

// SpringReady 任一类待领量 >0 即可领。
func SpringReady(gold, guild, bp int64) bool {
	return gold > 0 || guild > 0 || bp > 0
}
