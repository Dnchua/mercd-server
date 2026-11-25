// event_wire.go M8 限时活动与周常探险读面（PLAN E1–E4、E7）。
//
// 字段形状对齐 mlk-capture-201711 录播（docs/17 §3–§5）：
//   - /events/：历史活动列表 + 窗口内当期活动置顶；release_limit 历史行为
//     「当前+365 天」滚动值（偏离原服固定 2027-10-12，避免走 /events/release
//     扣徽章分支），当期行为窗口 close_at。
//   - /areas/event?event_id=N：当期活动窗口内下发全部区域/关卡，由
//     user_event_condition_<id> 推进边界交给客户端 ListFilterComponent 过滤；
//     窗口外/历史活动下发空区域与 {area_id:0, quest_id:0}（D1）。
//   - /quests/daily：daily_quests_<wday> 4 行（周日=0），按 Asia/Shanghai 0 点切日。
package cn35game

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// registerEventRoutes 登记 M8 读面；目录缺失/校验失败时不登记（保留空态）。
func registerEventRoutes(register func(method, path, note string, handler httpapi.Handler), dep Deps) {
	ec, err := loadEventCatalog(dep.AssetsDir)
	if err != nil {
		dep.log().Warn("CN3.5 活动目录校验失败，活动/周常读面保持空态", "err", err)
		return
	}
	if ec == nil {
		return
	}
	if dep.Catalog != nil {
		if err := ec.validateRefs(dep.Catalog); err != nil {
			dep.log().Warn("CN3.5 活动目录怪物引用校验失败，活动/周常读面保持空态", "err", err)
			return
		}
	}
	both := func(path, note string, h httpapi.Handler) {
		register(http.MethodGet, path, note, h)
		register(http.MethodPost, path, note+" POST 形态", h)
	}
	both("/events/", "CN 3.5.0 历史活动列表（M8 E1：42 条历史活动 + 窗口内当期活动）", eventsHandler(dep))
	both("/areas/event", "CN 3.5.0 活动区域（M8 E2：?event_id=N → areas_<stage> + quests_<area> + user_event_condition_N）", areasEventHandler(dep))
	both("/quests/daily", "CN 3.5.0 周常探险（M8 E4：daily_quests_<wday> 4 档）", dailyQuestsHandler(dep))
	both("/events/release", "CN 3.5.0 历史活动解锁（M8 E7：防御性兜底，不扣徽章）", eventReleaseHandler(dep))
}

func eventCondKey(eventID int) string { return fmt.Sprintf("user_event_condition_%d", eventID) }

// eventSelectOption 历史/当期活动行的 release_event_select_option（录播同形）。
func eventSelectOption(eventID int, areaKey string) map[string]any {
	cond := eventCondKey(eventID)
	path := fmt.Sprintf("/areas/event?event_id=%d", eventID)
	return map[string]any{
		"parts": map[string]any{
			"QustAreaSelect": map[string]any{
				"data_key": areaKey,
				"components": map[string]any{
					"filter":       map[string]any{"border": map[string]any{"data": cond, "key": "area_id"}},
					"quest_filter": map[string]any{"border": map[string]any{"data": cond, "key": "quest_id"}},
				},
			},
		},
		// force_update：推进边界随结算变化，缓存命中会让新解锁关卡不可见
		// （与 quest_wire.go stageDoc 同理；录播无此键，属服务端补强）。
		"requireData": map[string]any{
			areaKey: map[string]any{"path": path, "force_update": true},
			cond:    map[string]any{"path": path, "force_update": true},
		},
	}
}

// eventListRows 组装活动列表行（id/name/release_limit/release_event_select_option）：
// 时间窗内的当期活动按窗口关时刻，历史活动一律 rolling +365d。
// eventsHandler 与 stagesHandler 的 "events" 键共用（StageSelectListParts
// Tab.PastEvent 读 cache "events" 按 EventStageListRow 渲染 event/event_<id>）。
func eventListRows(dep Deps, ec *eventCatalog, now time.Time) (rows []any, current int) {
	rows = []any{}
	if ec == nil {
		return rows, 0
	}
	for i := range ec.Events {
		ev := &ec.Events[i]
		w := eventWindow(dep, ev)
		if !w.Open(now) {
			continue
		}
		current++
		rows = append(rows, map[string]any{
			"id": ev.ID, "name": ev.Name, "release_limit": w.CloseAt.Unix(),
			"release_event_select_option": eventSelectOption(ev.ID, ev.AreaKey),
		})
	}
	rolling := now.Add(365 * 24 * time.Hour).Unix()
	for _, p := range ec.PastEvents {
		rows = append(rows, map[string]any{
			"id": p.ID, "name": p.Name, "release_limit": rolling,
			"release_event_select_option": eventSelectOption(p.ID, p.AreaKey),
		})
	}
	return rows, current
}

func eventsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		ec, err := loadEventCatalog(dep.AssetsDir)
		if err != nil {
			return nil, err
		}
		rows, current := eventListRows(dep, ec, eventClockNow(dep))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "events",
			"rows": len(rows), "current": current,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{"events": rows}})
	})
}

// eventAreaDoc 活动区域行（录播字段集合）。
func eventAreaDoc(a catalogArea, ev *eventDef, prog *QuestProgress) map[string]any {
	return map[string]any{
		"id": a.ID, "name": a.Name, "description": a.Description, "stage_id": a.StageID,
		"monster_attr_id": nil, "achievement_key": nil,
		"percentage":    eventAreaPercentage(ev, a.ID, prog),
		"no_difficulty": false, "bonus_label": a.BonusLabel,
		"area_image_id": a.AreaImageID,
	}
}

// eventBattleQuestFields execute/列表共用的录播补充字段（use_cp/weather/七武器率等）。
func eventBattleQuestFields(q catalogQuest, retriableEndedAt int64) map[string]any {
	doc := map[string]any{
		"use_cp": 0, "weather": "",
		"slash_rate": 1.0, "rush_rate": 1.0, "hit_rate": 1.0, "arrow_rate": 1.0,
		"magic_rate": 1.0, "gun_rate": 1.0, "heal_rate": 1.0,
		"help_base_sec": 25, "help_sec_level_rate": 0.1,
		"save_current_quest": false,
		"retriable_ended_at": retriableEndedAt,
		"attr_id":            nil, "monster_label": nil, "boss_label": nil, "area_id": nil,
	}
	if n, err := strconv.Atoi(q.AttrID); err == nil && n > 0 {
		doc["attr_id"] = n
	}
	if q.MonsterLabel != "" {
		doc["monster_label"] = q.MonsterLabel
	}
	if q.BossLabel != "" {
		doc["boss_label"] = q.BossLabel
	}
	if q.AreaID > 0 {
		doc["area_id"] = q.AreaID
	}
	if q.CurrentQuestAssetName != "" {
		doc["current_quest_asset_name"] = q.CurrentQuestAssetName
	}
	if len(q.DifficultyNames) > 0 {
		doc["difficulty_names"] = q.DifficultyNames
	}
	return doc
}

// eventQuestDoc quests_<area> 活动关卡行：剧情 questDoc + 录播补充字段 +
// 录播形 quest_select_option（Header/Background/DifficultySelect）。
func eventQuestDoc(cat *questCatalog, q catalogQuest, prog *QuestProgress, closeAt int64) map[string]any {
	doc := questDoc(q, q.AreaID, prog)
	for k, v := range eventBattleQuestFields(q, closeAt) {
		doc[k] = v
	}
	doc["use_ap"] = q.UseAPMap["normal"]
	doc["background"] = cat.Defaults.Background
	doc["powerup_conf"] = cat.Defaults.PowerupConf
	dataKey := fmt.Sprintf("quests_%d", q.AreaID)
	doc["quest_select_option"] = map[string]any{
		"parts": map[string]any{
			"Header":     map[string]any{"title_right_label": q.Name},
			"Background": map[string]any{"texture_asset_name": "common/bg-quest_select_fs_128"},
			"DifficultySelect": map[string]any{
				"id": q.ID, "data_key": dataKey, "prefab": "Common/PageParts/QuestSelectParts",
			},
		},
		"requireData": map[string]any{
			dataKey: map[string]any{"path": fmt.Sprintf("/quests/?area_id=%d", q.AreaID), "force_update": true},
		},
	}
	return doc
}

// eventAreaQuestDocs 某活动区域全部关卡（窗口外返回空）。
func eventAreaQuestDocs(dep Deps, cat *mergedCatalog, ev *eventDef, areaID int, prog *QuestProgress) []any {
	out := []any{}
	w := eventWindow(dep, ev)
	if !w.Open(eventClockNow(dep)) {
		return out
	}
	for _, q := range ev.Quests {
		if q.AreaID == areaID {
			out = append(out, eventQuestDoc(cat.questCatalog, q.catalogQuest, prog, w.CloseAt.Unix()))
		}
	}
	return out
}

func areasEventHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		eventID, _ := strconv.Atoi(reqParam(req, "event_id"))
		cat, err := loadMergedCatalog(dep)
		if err != nil {
			return nil, err
		}
		cond := eventCondKey(eventID)
		data := map[string]any{cond: map[string]any{"area_id": 0, "quest_id": 0}}
		mode := "empty"
		var ev *eventDef
		if cat.events != nil {
			ev = cat.events.byID[eventID]
			if ev == nil {
				key := fmt.Sprintf("areas_%d", 1000+eventID)
				for _, p := range cat.events.PastEvents {
					if p.ID == eventID {
						key = p.AreaKey
					}
				}
				data[key] = []any{}
			}
		}
		if ev != nil {
			data[ev.AreaKey] = []any{}
			if eventWindow(dep, ev).Open(eventClockNow(dep)) {
				prog, err := questProgressFor(dep, req.UserID)
				if err != nil {
					return nil, fmt.Errorf("cn35 活动进度读取失败: %w", err)
				}
				// 读面对账：补发结算后未及时落账的完成率奖励（幂等）。
				if _, err := reconcileEventRewards(dep, ev, req.UserID, prog); err != nil {
					return nil, err
				}
				areas := []any{}
				for _, a := range ev.Areas {
					areas = append(areas, eventAreaDoc(a, ev, prog))
					data[fmt.Sprintf("quests_%d", a.ID)] = eventAreaQuestDocs(dep, cat, ev, a.ID, prog)
				}
				data[ev.AreaKey] = areas
				fa, fq := eventFrontier(ev, prog)
				data[cond] = map[string]any{"area_id": fa, "quest_id": fq}
				mode = "open"
			} else {
				mode = "closed"
			}
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "areas_event",
			"event_id": eventID, "mode": mode,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// dailyQuestDocs daily_quests_<wday> 4 行（录播字段集合）。
func dailyQuestDocs(cat *mergedCatalog, day *dailyDay, prog *QuestProgress, dayEnd int64) []any {
	out := []any{}
	for _, eq := range day.Quests {
		q := eq.catalogQuest
		doc := eventBattleQuestFields(q, dayEnd)
		for k, v := range map[string]any{
			"id": q.ID, "name": q.Name, "use_ap_map": q.UseAPMap, "use_ap": q.UseAPMap["normal"],
			"background_id": q.BackgroundID, "initial_power": q.InitialPower,
			"background": cat.Defaults.Background, "powerup_conf": cat.Defaults.PowerupConf,
			"wave_conf": q.WaveConf, "seed_atack_rate": q.SeedAtackRate,
			"continuable": derefBool(q.Continuable, true), "unit_count_limit": q.UnitCountLimit,
			"unit_help_rate": q.UnitHelpRate, "weapon_rate_hash": q.WeaponRateHash,
			"clear_count": prog.Clears[q.ID]["normal"], "wave_count": q.WaveCount,
		} {
			doc[k] = v
		}
		out = append(out, doc)
	}
	return out
}

func dailyQuestsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		cat, err := loadMergedCatalog(dep)
		if err != nil {
			return nil, err
		}
		data := map[string]any{}
		ec := cat.events
		now := eventClockNow(dep)
		today := -1
		if ec != nil && len(ec.byWday) > 0 {
			prog, err := questProgressFor(dep, req.UserID)
			if err != nil {
				return nil, fmt.Errorf("cn35 周常进度读取失败: %w", err)
			}
			today = ec.dailyWday(now)
			dayEnd := ec.dailyDayEnd(now)
			wdays := []int{today}
			// ?wday= 仅为读面兼容（app_conf DailyQuestSelect 变体写死 wday=6）；
			// 开战门禁始终按服务端当天判定。
			if w, err := strconv.Atoi(strings.TrimSpace(reqParam(req, "wday"))); err == nil && w >= 0 && w <= 6 && w != today {
				wdays = append(wdays, w)
			}
			for _, w := range wdays {
				if day := ec.byWday[w]; day != nil {
					data[fmt.Sprintf("daily_quests_%d", w)] = dailyQuestDocs(cat, day, prog, dayEnd)
				}
			}
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "quests_daily", "wday": today,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

func eventReleaseHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "events_release",
			"id": reqParam(req, "id"),
		}
		return cn35wire.MarshalJSON(statusDoc("success"))
	})
}

// reconcileEventRewards 发放已达成但未领取的区域完成率奖励（幂等）。
func reconcileEventRewards(dep Deps, ev *eventDef, userID string, prog *QuestProgress) ([]EventAreaClaim, error) {
	if dep.Events == nil || len(ev.AreaRewards) == 0 {
		return nil, nil
	}
	claimed, err := dep.Events.EventAreaRewardClaims(userID)
	if err != nil {
		return nil, fmt.Errorf("cn35 活动完成率奖励台账读取失败: %w", err)
	}
	var pending []EventAreaClaim
	for _, r := range ev.AreaRewards {
		if claimed[[2]int{r.AreaID, r.Threshold}] {
			continue
		}
		if eventAreaPercentage(ev, r.AreaID, prog) >= r.Threshold {
			pending = append(pending, EventAreaClaim{
				EventID: ev.ID, AreaID: r.AreaID, Threshold: r.Threshold, Presents: r.Presents,
			})
		}
	}
	if len(pending) == 0 {
		return nil, nil
	}
	granted, err := dep.Events.ClaimEventAreaRewards(userID, pending, eventClockNow(dep).Unix())
	if err != nil {
		return nil, fmt.Errorf("cn35 活动完成率奖励发放失败: %w", err)
	}
	return granted, nil
}

// progressRateRewardMessage 结算完成率奖励提示（主线录播 quests_result 同形）。
func progressRateRewardMessage(granted []EventAreaClaim) map[string]any {
	var parts []string
	for _, g := range granted {
		for _, p := range g.Presents {
			label := p.Label
			if label == "" {
				label = fmt.Sprintf("%s ×%d", p.ContentType, p.Amount)
			}
			parts = append(parts, label)
		}
	}
	return map[string]any{
		"title": "区域探险完成率奖励",
		"body":  "[eb8900]" + strings.Join(parts, "、") + "[-]，已经发送到礼盒中",
	}
}

// executeRetriableEndedAt execute 回显的 retriable_ended_at：活动=窗口结束，周常=当日结束。
func executeRetriableEndedAt(dep Deps, q *catalogQuest) int64 {
	ec, err := loadEventCatalog(dep.AssetsDir)
	if err != nil || ec == nil {
		return 0
	}
	now := eventClockNow(dep)
	if q.Kind == "daily" {
		return ec.dailyDayEnd(now)
	}
	if ev := ec.byID[q.EventID]; ev != nil {
		return eventWindow(dep, ev).CloseAt.Unix()
	}
	return 0
}

// eventModeReleased consts.event_mode_released：活动目录可用即开放历史活动页签。
func eventModeReleased(dep Deps) bool {
	ec, err := loadEventCatalog(dep.AssetsDir)
	return err == nil && ec != nil
}
