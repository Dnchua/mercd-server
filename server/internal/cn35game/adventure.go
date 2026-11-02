// adventure.go 登录冒险「图图历险记」的领域定义：LOGIN350-03。
//
// ## 业务口径（设计来源）
//
// 设计：notes/proposed/2026-10-26-cn35-login-adventure.md；
// 验收：plans/cn350-login-adventure-2026-10-26/README.md（LA-01—15）。
//
// 客户端消费链（**静态源码取证**，行号指
// artifacts/tasks/toto-cn3.5.0-onboarding/run/ilspy-cs/LoginAdventurePage.cs）：
//
//   - 登录进 Home 后 requireData 拉 `/adventure/`（app_conf
//     `pages.LoginAdventure.requireData.LoginAdventureData.path`）；
//   - `LoginAdventureData.Load`（:299-310）要求 `adventure_info.adventure`
//     三层嵌套 + `today_days` + `setting`；缺任一必填即 Load 失败 → 不弹窗（:657-661）；
//   - `today_days` 解析为整数才弹 Stack 页（:664-669）——它是**当日弹窗开关**，
//     非数字即「already shown, skipped」；
//   - `CalculateDays`（:634-652）：`currentDay = 服务器日(东八区) - begin_date_i(东八区)
//     的自然日差 + 1`；`duration = setting.days.Count`；跨天重查由
//     `server_date_i` 推出的**次日 0 点**驱动；
//   - 页面无领取按钮（唯一交互 ClickClose :932-936），展示时刷
//     `/user_presents/`（:782-791）⇒ **奖励发放走礼物盒内容池**；
//   - 格子三态（:836-849）：`adventure_info["N"]==true` 或今天 → 绿 tag + 印章；
//     `N < currentDay` 且未置位 → 灰 tag（**错过不可补**）；其余常态；
//   - 走完一轮（`days.Count` 天）后次日登录开新轮（页内 HelpText 运营文案第 3 条）。
//
// ## 事实 / 重建分界（红线：未验证的推断不得写成事实）
//
// 事实（客户端静态取证）：上列字段、解析失败分支、三态判定、礼物盒消费点、
// 次日 0 点重查口径。
//
// 重建（本实现自定，**非原服数据**）：轮长（10 天）、奖励数值、special 日分布、
// 地图折线（复用客户端 defaultPath 的 10 节点蛇形坐标）、`reward_config_id`
// 取值域（正整数自增）、`user_id` 数值口径（见 AdventureUserIDNum）。
// 原服上述内容均未取证，契约卡（contracts/22）已按「重建，非原服取证」标注。
//
// ## 时间口径
//
// 一律用**东八区自然日**（`springLoc`，与 LOGIN350-02 泉水/签到同口径）；
// `server_date_i` 下发服务端当前 Unix 秒，客户端据此推出自己的跨天重查时刻，
// 因此改客户端本地时间不能提早/跳格（LA-09）。
package cn35game

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"time"
)

// ErrAdventureConfigInvalid 轮次配置不合法（轮长与日表不一致、
// pos 越界/乱序、奖励行非法等）。属服务端配置错误，与「未配置（空态）」不同：
// 空态合法回落，坏配置显式报错，不静默下发半张地图。
var ErrAdventureConfigInvalid = errors.New("cn35game: 登录冒险轮次配置非法")

// AdventureMaxDurationDays 单轮最大天数（与 0036 迁移 CHECK 一致）。
const AdventureMaxDurationDays = 60

// AdventureDayConfig 单日节点：展示形状 + 实际发放。
//
// 展示与发放拆开是刻意的：客户端页面只渲染 `rewards[0]`（:881-887），
// 而发放走礼物盒内容池，两者类型域不同（`unit/-1` 只是 scout_book 图标特例，
// 不是可发放的同伴）。拆列后发放口径可审计，不被展示口径污染。
type AdventureDayConfig struct {
	Day   int
	Title string
	// Pos 地图折线累计长度的归一化比例（GetPosByRatio 口径 0..1）。
	Pos     float64
	Special bool
	Scale   float64

	// 展示（对应客户端 SingleReward：type_id/type_name/content_id/count/extend.unit_rare）
	DisplayTypeID    int
	DisplayTypeName  string
	DisplayContentID int64
	DisplayCount     int64
	DisplayUnitRare  int64

	// 发放（cn35_user_presents 行内容；content_id 必须 >0，见 0015 CHECK）
	GrantType      string
	GrantContentID int64
	GrantAmount    int64
}

// AdventureConfig 一套轮次配置（轮长 + 地图 + 日表 + 覆盖项）。
type AdventureConfig struct {
	RewardConfigID     int
	DurationDays       int
	Path               []float64
	BackgroundOverride string
	StamperOverride    string
	RightTitleOverrdie string
	PathIndicator      string
	Days               []AdventureDayConfig // 下标 0 = 第 1 天
}

// AdventureDay 返回第 day 天（1 基）的配置；越界返回 false。
func (c *AdventureConfig) AdventureDay(day int) (AdventureDayConfig, bool) {
	if c == nil || day < 1 || day > len(c.Days) {
		return AdventureDayConfig{}, false
	}
	return c.Days[day-1], true
}

// ParseAdventurePath 解析 `cn35_adventure_configs.path_json`（坐标数组文本）。
func ParseAdventurePath(raw string) ([]float64, error) {
	var path []float64
	if err := json.Unmarshal([]byte(raw), &path); err != nil {
		return nil, fmt.Errorf("%w: path_json 解析失败: %v", ErrAdventureConfigInvalid, err)
	}
	return path, nil
}

// ValidateAdventureConfig 校验配置自洽性（轮长与日表、pos 单调、奖励行合法）。
//
// 客户端只按 `setting.days.Count` 定轮长（:642），再按 `days[i].pos` 定位格子；
// 日表与轮长不一致会在页面渲染时越界或漏格，故在装配期就拒绝。
func ValidateAdventureConfig(c *AdventureConfig) error {
	if c == nil {
		return fmt.Errorf("%w: 配置为空", ErrAdventureConfigInvalid)
	}
	if c.RewardConfigID <= 0 {
		return fmt.Errorf("%w: reward_config_id=%d", ErrAdventureConfigInvalid, c.RewardConfigID)
	}
	if c.DurationDays < 1 || c.DurationDays > AdventureMaxDurationDays {
		return fmt.Errorf("%w: duration_days=%d", ErrAdventureConfigInvalid, c.DurationDays)
	}
	if len(c.Days) != c.DurationDays {
		return fmt.Errorf("%w: 日表 %d 条与轮长 %d 不一致",
			ErrAdventureConfigInvalid, len(c.Days), c.DurationDays)
	}
	if len(c.Path) < 4 || len(c.Path)%2 != 0 {
		return fmt.Errorf("%w: path 需要至少 2 个坐标点，实际 %d 个数值",
			ErrAdventureConfigInvalid, len(c.Path))
	}
	prevPos := -1.0
	for i, d := range c.Days {
		if d.Day != i+1 {
			return fmt.Errorf("%w: 第 %d 行的 day=%d", ErrAdventureConfigInvalid, i+1, d.Day)
		}
		if d.Pos < 0 || d.Pos > 1 || d.Pos < prevPos {
			return fmt.Errorf("%w: day=%d pos=%v 越界或乱序", ErrAdventureConfigInvalid, d.Day, d.Pos)
		}
		prevPos = d.Pos
		if d.DisplayTypeName == "" || d.DisplayCount <= 0 {
			return fmt.Errorf("%w: day=%d 展示奖励非法", ErrAdventureConfigInvalid, d.Day)
		}
		if d.GrantType == "" || d.GrantContentID <= 0 || d.GrantAmount <= 0 {
			return fmt.Errorf("%w: day=%d 发放内容非法", ErrAdventureConfigInvalid, d.Day)
		}
		if d.Scale <= 0 {
			return fmt.Errorf("%w: day=%d scale=%v", ErrAdventureConfigInvalid, d.Day, d.Scale)
		}
	}
	return nil
}

// AdventureState 某账号**当前轮次**的进度快照（读面数据源）。
type AdventureState struct {
	RoundIndex     int
	RewardConfigID int
	BeginDate      string // 东八区自然日 YYYY-MM-DD
	DurationDays   int
	// CurrentDay 1 基；`(服务器日 - begin_date)` 的自然日差 + 1。
	CurrentDay int
	// ClaimedDays 本轮已发放（= 位图 true）的天，升序。
	ClaimedDays []int
	// TodayShown 本次请求是否为**当日首次**（true ⇒ 下发 today_days 数字 ⇒ 弹窗）。
	TodayShown bool
	// GrantedDay 本次请求实际发放的天（0 = 无发放；已发过/超出轮长均为 0）。
	GrantedDay int
	// PresentUUID 本次发放写入的礼物行 uuid（无发放为空）。
	PresentUUID string
}

// AdventureClaimed 位图查询：第 day 天（1 基）本轮是否已发放。
func (st *AdventureState) AdventureClaimed(day int) bool {
	if st == nil {
		return false
	}
	for _, d := range st.ClaimedDays {
		if d == day {
			return true
		}
	}
	return false
}

// AdventureStore 登录冒险读/发面（cn35store.Store 实现）。
//
// 语义约定（单事务）：确保当前轮次（含走完一轮后的滚动）→ 判定当日首次 →
// 当日奖励写入 `cn35_user_presents` 内容池与发放台账 → 返回读面状态。
// 未配置任何轮次时返回 (nil, nil, nil)：上层回**结构完整空态**（客户端
// "already shown, skipped" 分支，不报错）——运营未排期即不弹窗。
type AdventureStore interface {
	AdventureState(userID string, nowSec int64) (*AdventureState, *AdventureConfig, error)
}

// AdventureDayKeyAt 返回 nowSec 的东八区自然日键（YYYY-MM-DD）。
func AdventureDayKeyAt(nowSec int64) string {
	return time.Unix(nowSec, 0).In(springLoc).Format("2006-01-02")
}

// AdventureDayStartUnix 返回东八区某自然日 0 点的 Unix 秒（begin_date_i 口径）。
func AdventureDayStartUnix(dayKey string) (int64, error) {
	t, err := time.ParseInLocation("2006-01-02", dayKey, springLoc)
	if err != nil {
		return 0, fmt.Errorf("%w: begin_date=%q", ErrAdventureConfigInvalid, dayKey)
	}
	return t.Unix(), nil
}

// AdventureDayDiff 返回 to-from 的自然日差（东八区；to 早于 from 时为负）。
func AdventureDayDiff(fromKey, toKey string) (int, error) {
	from, err := time.ParseInLocation("2006-01-02", fromKey, springLoc)
	if err != nil {
		return 0, fmt.Errorf("%w: from=%q", ErrAdventureConfigInvalid, fromKey)
	}
	to, err := time.ParseInLocation("2006-01-02", toKey, springLoc)
	if err != nil {
		return 0, fmt.Errorf("%w: to=%q", ErrAdventureConfigInvalid, toKey)
	}
	return int(to.Sub(from).Hours() / 24), nil
}

// AdventureCurrentDay 本轮「今天是第几天」（1 基）。
//
// 与客户端 CalculateDays 同口径：自然日差 + 1；服务器日早于 begin（时钟回拨/
// 异常数据）时钳到 1，不产生 0 或负天。
func AdventureCurrentDay(beginDate string, nowSec int64) (int, error) {
	diff, err := AdventureDayDiff(beginDate, AdventureDayKeyAt(nowSec))
	if err != nil {
		return 0, err
	}
	if diff < 0 {
		return 1, nil
	}
	return diff + 1, nil
}

// AdventureUserIDNum 下发 `adventure_info.adventure.user_id` 的数值口径。
//
// 客户端 `ParseInt` 要求该字段存在且可解析为整数（:235）；而本服务端账号 id 是
// 16 位十六进制串（cn35_accounts.user_id），不能直接转整数。取 FNV-1a 32 位
// 并对 2^31 取模：**同账号恒定、跨账号几乎不撞**，仅用于契约字段填充。
// 原服该字段语义未取证（客户端页面全量未消费该值），故按「重建」标注。
func AdventureUserIDNum(userID string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte("cn35-adventure|" + userID))
	return int64(h.Sum32() & 0x7fffffff)
}

// AdventureCurrentDayText 当日弹窗开关文本：当次为当日首次 ⇒ 天数字符串
// （客户端 int.TryParse 成功 ⇒ 弹窗）；否则空串（"already shown, skipped"）。
func AdventureCurrentDayText(st *AdventureState) string {
	if st == nil || !st.TodayShown {
		return ""
	}
	return fmt.Sprintf("%d", st.CurrentDay)
}

// SortAdventureConfigsByOrder 按 (sort, reward_config_id) 升序排序配置
// （轮次滚动顺序的真源口径）。
func SortAdventureConfigsByOrder(cfgs []AdventureConfigRef) {
	sort.SliceStable(cfgs, func(i, j int) bool {
		if cfgs[i].Sort != cfgs[j].Sort {
			return cfgs[i].Sort < cfgs[j].Sort
		}
		return cfgs[i].RewardConfigID < cfgs[j].RewardConfigID
	})
}

// AdventureConfigRef 轮次配置的排序引用（装配期只需 id 与 sort）。
type AdventureConfigRef struct {
	RewardConfigID int
	Sort           int
}
