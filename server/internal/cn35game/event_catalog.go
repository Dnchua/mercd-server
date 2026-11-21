// event_catalog.go M8 限时活动与周常探险静态目录（serve_event_catalog.json）。
//
// 目录由 server/tools/build_event_catalog.py 生成（证据与重建规则见
// docs/17-限时活动与周常探险回放统计.md、plans/cn35-m8-events-daily-2026-11-19）。
// 本文件负责：
//   - 加载与结构校验（id 号段、区域引用、周常每天 4 档、battle 形状）；
//   - 时间窗解析（命令行/环境变量覆盖 fixture default_window；默认关闭）；
//   - 合并进 mergedCatalog（Kind=event/daily，不进剧情推进链）；
//   - 活动推进边界：由通关记录推导（首个三难度均未通关的关卡），
//     不另建进度表，结算/重启后天然一致。
package cn35game

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const eventCatalogFile = "serve_event_catalog.json"

// 周常关卡号段（D5）：quest_id = dailyQuestBase + wday*10 + tier。
const dailyQuestBase = 80000

// EventWindow 活动开放时间窗 [OpenAt, CloseAt)。零值=关闭。
type EventWindow struct {
	OpenAt  time.Time
	CloseAt time.Time
}

// Configured 是否配置了有效窗口。
func (w EventWindow) Configured() bool {
	return !w.OpenAt.IsZero() && !w.CloseAt.IsZero() && w.CloseAt.After(w.OpenAt)
}

// Open now 是否落在窗口内。
func (w EventWindow) Open(now time.Time) bool {
	return w.Configured() && !now.Before(w.OpenAt) && now.Before(w.CloseAt)
}

// ParseEventWindows 解析 MERCD35_EVENT_WINDOWS / -event-windows：
// "44=2026-11-20T12:00:00+08:00/2026-12-04T11:00:00+08:00;45=..."。空串=无覆盖。
func ParseEventWindows(spec string) (map[int]EventWindow, error) {
	out := map[int]EventWindow{}
	for _, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idStr, rng, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("活动时间窗格式应为 <event_id>=<开始>/<结束>: %q", part)
		}
		id, err := strconv.Atoi(strings.TrimSpace(idStr))
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("活动时间窗 event_id 非法: %q", idStr)
		}
		a, b, ok := strings.Cut(rng, "/")
		if !ok {
			return nil, fmt.Errorf("活动时间窗缺少 / 分隔: %q", part)
		}
		openAt, err := time.Parse(time.RFC3339, strings.TrimSpace(a))
		if err != nil {
			return nil, fmt.Errorf("活动 %d 开始时间非 RFC3339: %w", id, err)
		}
		closeAt, err := time.Parse(time.RFC3339, strings.TrimSpace(b))
		if err != nil {
			return nil, fmt.Errorf("活动 %d 结束时间非 RFC3339: %w", id, err)
		}
		if !closeAt.After(openAt) {
			return nil, fmt.Errorf("活动 %d 结束时间必须晚于开始时间", id)
		}
		if _, dup := out[id]; dup {
			return nil, fmt.Errorf("活动 %d 时间窗重复配置", id)
		}
		out[id] = EventWindow{OpenAt: openAt, CloseAt: closeAt}
	}
	return out, nil
}

// EventPresent 完成率奖励的一件礼物（发到礼物盒）。
type EventPresent struct {
	ContentType string `json:"content_type"`
	ContentID   int64  `json:"content_id"`
	Amount      int64  `json:"amount"`
	// Label 结算提示文案（progress_rate_reward_message.body 拼接用）。
	Label string `json:"label,omitempty"`
}

// EventAreaClaim 一档区域完成率奖励。
type EventAreaClaim struct {
	EventID   int            `json:"event_id"`
	AreaID    int            `json:"area_id"`
	Threshold int            `json:"threshold"`
	Presents  []EventPresent `json:"presents"`
}

// EventStore 区域完成率奖励台账（0056 cn35_event_area_rewards）。
type EventStore interface {
	// EventAreaRewardClaims 已领档位集合，键为 [area_id, threshold]。
	EventAreaRewardClaims(userID string) (map[[2]int]bool, error)
	// ClaimEventAreaRewards 单事务：台账插入（主键幂等）+ 礼物盒写入；
	// 返回本次实际新发放的档位（重放/并发已领的不返回）。
	ClaimEventAreaRewards(userID string, claims []EventAreaClaim, nowSec int64) ([]EventAreaClaim, error)
}

type eventAreaReward struct {
	AreaID    int            `json:"area_id"`
	Threshold int            `json:"threshold"`
	Presents  []EventPresent `json:"presents"`
	Source    string         `json:"source"`
}

// eventQuest 目录关卡 + 周常素材掉落（复用资源本 ResourceDrop 语义）。
type eventQuest struct {
	catalogQuest
	Drops []ResourceDrop `json:"drops"`
}

type eventDef struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	StageID       int    `json:"stage_id"`
	AreaKey       string `json:"area_key"`
	DefaultWindow struct {
		OpenAt  string `json:"open_at"`
		CloseAt string `json:"close_at"`
	} `json:"default_window"`
	Areas       []catalogArea     `json:"areas"`
	Quests      []eventQuest      `json:"quests"`
	AreaRewards []eventAreaReward `json:"area_rewards"`
}

type pastEvent struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	AreaKey string `json:"area_key"`
}

type dailyDay struct {
	Wday    int          `json:"wday"`
	Name    string       `json:"name"`
	Theme   string       `json:"theme"`
	Element int          `json:"element"`
	Quests  []eventQuest `json:"quests"`
}

type eventCatalog struct {
	PastEvents []pastEvent `json:"past_events"`
	Events     []eventDef  `json:"events"`
	Daily      struct {
		SwitchTZ   string     `json:"switch_tz"`
		SwitchHour int        `json:"switch_hour"`
		Days       []dailyDay `json:"days"`
	} `json:"daily"`

	loc    *time.Location
	byID   map[int]*eventDef
	byArea map[int]*eventDef
	byWday map[int]*dailyDay
}

var eventCatalogCache struct {
	sync.Mutex
	path    string
	modTime time.Time
	size    int64
	cat     *eventCatalog
}

// loadEventCatalog 读取并校验活动目录；文件不存在返回 (nil, nil)（功能关闭）。
// 按 (path, mtime, size) 缓存，改动文件即生效。
func loadEventCatalog(assetsDir string) (*eventCatalog, error) {
	path := filepath.Join(assetsDir, eventCatalogFile)
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cn35 活动目录读取失败: %w", err)
	}
	eventCatalogCache.Lock()
	defer eventCatalogCache.Unlock()
	if eventCatalogCache.cat != nil && eventCatalogCache.path == path &&
		eventCatalogCache.modTime.Equal(fi.ModTime()) && eventCatalogCache.size == fi.Size() {
		return eventCatalogCache.cat, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cn35 活动目录读取失败: %w", err)
	}
	var cat eventCatalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("cn35 活动目录非法 JSON: %w", err)
	}
	if err := cat.validate(); err != nil {
		return nil, fmt.Errorf("cn35 活动目录校验失败: %w", err)
	}
	eventCatalogCache.path, eventCatalogCache.modTime, eventCatalogCache.size = path, fi.ModTime(), fi.Size()
	eventCatalogCache.cat = &cat
	return &cat, nil
}

func (c *eventCatalog) validate() error {
	tz := c.Daily.SwitchTZ
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.FixedZone("UTC+8", 8*3600)
	}
	c.loc = loc
	seenPast := map[int]bool{}
	for _, p := range c.PastEvents {
		if p.ID <= 0 || seenPast[p.ID] || p.Name == "" || !strings.HasPrefix(p.AreaKey, "areas_") {
			return fmt.Errorf("历史活动行非法/重复: %+v", p)
		}
		seenPast[p.ID] = true
	}
	c.byID, c.byArea, c.byWday = map[int]*eventDef{}, map[int]*eventDef{}, map[int]*dailyDay{}
	questIDs := map[int]bool{}
	var battleQuests []catalogQuest
	for i := range c.Events {
		ev := &c.Events[i]
		if ev.ID <= 0 || c.byID[ev.ID] != nil || seenPast[ev.ID] {
			return fmt.Errorf("活动 id 非法/重复: %d", ev.ID)
		}
		c.byID[ev.ID] = ev
		if ev.AreaKey != fmt.Sprintf("areas_%d", ev.StageID) {
			return fmt.Errorf("活动 %d area_key 与 stage_id 不符: %s", ev.ID, ev.AreaKey)
		}
		areas := map[int]bool{}
		for j := range ev.Areas {
			a := &ev.Areas[j]
			if a.ID <= 0 || c.byArea[a.ID] != nil || a.StageID != ev.StageID {
				return fmt.Errorf("活动 %d 区域非法: %d", ev.ID, a.ID)
			}
			a.EventID = ev.ID
			c.byArea[a.ID] = ev
			areas[a.ID] = true
		}
		for j := range ev.Quests {
			q := &ev.Quests[j].catalogQuest
			if q.ID <= 0 || q.ID >= dailyQuestBase || questIDs[q.ID] || !areas[q.AreaID] {
				return fmt.Errorf("活动 %d 关卡 id/区域非法: %d", ev.ID, q.ID)
			}
			if err := validateEventAP(q); err != nil {
				return err
			}
			questIDs[q.ID] = true
			q.Kind, q.EventID = "event", ev.ID
			battleQuests = append(battleQuests, *q)
		}
		sort.Slice(ev.Quests, func(a, b int) bool { return ev.Quests[a].ID < ev.Quests[b].ID })
		for _, r := range ev.AreaRewards {
			if !areas[r.AreaID] || (r.Threshold != 30 && r.Threshold != 60 && r.Threshold != 100) || len(r.Presents) == 0 {
				return fmt.Errorf("活动 %d 完成率奖励非法: %+v", ev.ID, r)
			}
			for _, p := range r.Presents {
				if p.ContentID <= 0 || p.Amount <= 0 || p.ContentType == "" {
					return fmt.Errorf("活动 %d 完成率奖励礼物非法: %+v", ev.ID, p)
				}
			}
		}
	}
	for i := range c.Daily.Days {
		d := &c.Daily.Days[i]
		if d.Wday < 0 || d.Wday > 6 || c.byWday[d.Wday] != nil {
			return fmt.Errorf("周常 wday 非法/重复: %d", d.Wday)
		}
		if len(d.Quests) != 4 {
			return fmt.Errorf("周常 wday=%d 必须恰好 4 档，实际 %d", d.Wday, len(d.Quests))
		}
		c.byWday[d.Wday] = d
		for tier := range d.Quests {
			q := &d.Quests[tier].catalogQuest
			if want := dailyQuestBase + d.Wday*10 + tier + 1; q.ID != want || questIDs[q.ID] {
				return fmt.Errorf("周常 wday=%d 第 %d 档 id 应为 %d，实际 %d", d.Wday, tier+1, want, q.ID)
			}
			if err := validateEventAP(q); err != nil {
				return err
			}
			questIDs[q.ID] = true
			q.Kind, q.Wday, q.AreaID = "daily", d.Wday, 0
			battleQuests = append(battleQuests, *q)
		}
	}
	if len(c.Daily.Days) != 0 && len(c.Daily.Days) != 7 {
		return fmt.Errorf("周常必须配置 7 天，实际 %d", len(c.Daily.Days))
	}
	shape := &questCatalog{Quests: battleQuests}
	return shape.validateBattleShape()
}

func validateEventAP(q *catalogQuest) error {
	for _, d := range questDifficulties {
		if q.UseAPMap[d] <= 0 {
			return fmt.Errorf("关卡 %d use_ap_map 缺难度键 %s", q.ID, d)
		}
	}
	if ap, ok := q.UseAPMap["impossible"]; ok && ap != q.UseAPMap["very_hard"] {
		return fmt.Errorf("关卡 %d 炼狱 AP 必须等于极难 AP", q.ID)
	}
	return nil
}

// validateRefs 启动期核对：出怪/种子掉落模板必须在卡牌目录。
func (c *eventCatalog) validateRefs(cards UnitCatalog) error {
	var qs []catalogQuest
	for _, ev := range c.Events {
		for _, q := range ev.Quests {
			qs = append(qs, q.catalogQuest)
		}
	}
	for _, d := range c.Daily.Days {
		for _, q := range d.Quests {
			qs = append(qs, q.catalogQuest)
		}
	}
	return (&questCatalog{Quests: qs}).validateBattleRefs(cards)
}

// ---- 运行期查询 ----

func eventClockNow(dep Deps) time.Time {
	if dep.EventClock != nil {
		return dep.EventClock()
	}
	return time.Now()
}

// eventWindow 生效时间窗：配置覆盖优先，其次 fixture default_window。
func eventWindow(dep Deps, ev *eventDef) EventWindow {
	if w, ok := dep.EventWindows[ev.ID]; ok {
		return w
	}
	openAt, err1 := time.Parse(time.RFC3339, ev.DefaultWindow.OpenAt)
	closeAt, err2 := time.Parse(time.RFC3339, ev.DefaultWindow.CloseAt)
	if err1 != nil || err2 != nil {
		return EventWindow{}
	}
	return EventWindow{OpenAt: openAt, CloseAt: closeAt}
}

// dailyWday 周常当天星期（switch_tz 时区自然日）。
func (c *eventCatalog) dailyWday(now time.Time) int {
	t := now.In(c.loc).Add(-time.Duration(c.Daily.SwitchHour) * time.Hour)
	return int(t.Weekday())
}

// dailyDayEnd 当天周常结束时刻（下一切日点前 1 秒），对齐 retriable_ended_at。
func (c *eventCatalog) dailyDayEnd(now time.Time) int64 {
	t := now.In(c.loc).Add(-time.Duration(c.Daily.SwitchHour) * time.Hour)
	start := time.Date(t.Year(), t.Month(), t.Day(), c.Daily.SwitchHour, 0, 0, 0, c.loc)
	return start.AddDate(0, 0, 1).Unix() - 1
}

// eventFrontier 活动推进边界：首个三难度均未通关的关卡；全通关时为末关。
func eventFrontier(ev *eventDef, prog *QuestProgress) (areaID, questID int) {
	if len(ev.Quests) == 0 {
		return 0, 0
	}
	for _, q := range ev.Quests {
		cleared := false
		for _, d := range questDifficulties {
			if prog.Clears[q.ID][d] > 0 {
				cleared = true
				break
			}
		}
		if !cleared {
			return q.AreaID, q.ID
		}
	}
	last := ev.Quests[len(ev.Quests)-1]
	return last.AreaID, last.ID
}

// eventAreaPercentage 区域完成率：已通关难度数 / (关卡数 × 3)（AreaSelectParts.cs:109-121 同口径）。
func eventAreaPercentage(ev *eventDef, areaID int, prog *QuestProgress) int {
	total, cleared := 0, 0
	for _, q := range ev.Quests {
		if q.AreaID != areaID {
			continue
		}
		for _, d := range questDifficulties {
			total++
			if prog.Clears[q.ID][d] > 0 {
				cleared++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return cleared * 100 / total
}

// mergeEventCatalog 把活动关与周常关并入合并目录（Kind 标记隔离剧情链）。
func (m *mergedCatalog) mergeEventCatalog(ec *eventCatalog) {
	if ec == nil {
		return
	}
	m.events = ec
	if m.eventDrops == nil {
		m.eventDrops = map[int][]ResourceDrop{}
	}
	for _, ev := range ec.Events {
		m.Areas = append(m.Areas, ev.Areas...)
		for _, q := range ev.Quests {
			m.Quests = append(m.Quests, q.catalogQuest)
		}
	}
	for _, d := range ec.Daily.Days {
		for _, q := range d.Quests {
			m.Quests = append(m.Quests, q.catalogQuest)
			if len(q.Drops) > 0 {
				m.eventDrops[q.ID] = append([]ResourceDrop(nil), q.Drops...)
			}
		}
	}
	sort.Slice(m.Areas, func(i, j int) bool { return m.Areas[i].ID < m.Areas[j].ID })
	m.areaMap = map[int]*catalogArea{}
	for i := range m.Areas {
		m.areaMap[m.Areas[i].ID] = &m.Areas[i]
	}
	sort.Slice(m.Quests, func(i, j int) bool { return m.Quests[i].ID < m.Quests[j].ID })
	m.questMap = map[int]*catalogQuest{}
	for i := range m.Quests {
		m.questMap[m.Quests[i].ID] = &m.Quests[i]
	}
}
