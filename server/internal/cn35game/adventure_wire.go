// adventure_wire.go LOGIN350-03：登录冒险「图图历险记」读面 + 当日发放。
//
// 端点：GET|POST /adventure/（app_conf `pages.LoginAdventure.requireData`
// `LoginAdventureData.path = /adventure/`；客户端 Home 加载完成即拉，
// Common.cs:1588；之后 Home 轮播部件每秒补弹一次，CarouselBannerParts.cs:276）。
//
// ## 响应形状（逐字段对应客户端解析，LoginAdventurePage.cs:213-310）
//
//	data.LoginAdventureData:
//	  adventure_info.adventure:            ← ParseObj("adventure", AdventureInfo) 必填
//	    adventure_info: {"N": true…}       ← 位图（已发放的天；缺键按 false，:838-841）
//	    begin_date_i: int                  ← 轮次起始东八区 0 点 Unix 秒（必填）
//	    reward_config_id: int              ← 轮次配置 id（必填）
//	    user_id: int                       ← 数值化账号（必填；口径见 AdventureUserIDNum）
//	  today_days: string                   ← 当日弹窗开关：数字 ⇒ 弹；非数字 ⇒ 跳过
//	  server_date_i: int                   ← 服务端当前 Unix 秒（客户端据此推次日 0 点）
//	  setting:
//	    days: [{title,pos,special,scale,rewards:[{type_id,type_name,content_id,count,extend}]}]
//	    path: [x0,y0,x1,y1…]               ← 空/缺会让客户端 NRE 或回 defaultPath
//	    background_override/stamper_override/right_title_overrdie/path_indicator（可选）
//
// 必填性来自客户端的解析器实现（`ParseInt/ParseString/ParseArray/ParseFloatArary`
// 在字段缺失时的分支），故服务端只在装配期校验一次配置自洽性
// （cn35game.ValidateAdventureConfig），运行期直接按配置下发。
//
// ## 空态
//
// `Deps.Adventure` 未装配或库中未配置任何轮次 → 回 adventureEmptyStateData()
// （结构完整空态，客户端显式跳过分支，不报错）。运营未排期即不弹窗。
package cn35game

import (
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// adventureNowSec 读面/发放共用的服务端时钟（测试可注入 Deps.AdventureClock）。
//
// 客户端跨天重查由 `server_date_i` 推出的次日 0 点驱动（:634-652），故这里
// 必须用服务端时间：改客户端本地时间不能提早/跳格/多领（LA-09）。
func adventureNowSec(dep Deps) int64 {
	if dep.AdventureClock != nil {
		return dep.AdventureClock().Unix()
	}
	return time.Now().Unix()
}

// adventureDayDoc 组装单日格：展示形状只渲染 rewards[0]（:881-887），
// 但 rewards 必须是数组且元素字段齐全（SingleReward.Load 缺字段即 false）。
func adventureDayDoc(d AdventureDayConfig) map[string]any {
	reward := map[string]any{
		"type_id":    d.DisplayTypeID,
		"type_name":  d.DisplayTypeName,
		"content_id": d.DisplayContentID,
		"count":      d.DisplayCount,
	}
	// extend.unit_rare 只在有稀有度时下发：客户端仅对
	// type_name=="unit" && content_id==-1 读它渲染稀有度标签（:902-907）。
	if d.DisplayUnitRare > 0 {
		reward["extend"] = map[string]any{"unit_rare": d.DisplayUnitRare}
	}
	return map[string]any{
		"title":   d.Title,
		"pos":     d.Pos,
		"special": d.Special,
		"scale":   d.Scale,
		"rewards": []any{reward},
	}
}

// adventureSettingDoc 组装 setting（days + path + 非空覆盖项）。
func adventureSettingDoc(cfg *AdventureConfig) map[string]any {
	days := make([]any, 0, len(cfg.Days))
	for _, d := range cfg.Days {
		days = append(days, adventureDayDoc(d))
	}
	path := make([]any, 0, len(cfg.Path))
	for _, v := range cfg.Path {
		path = append(path, v)
	}
	setting := map[string]any{"days": days, "path": path}
	// 覆盖项：客户端 ParseString 未命中不算失败，故空值不下发（保持载荷最小）。
	overrides := []struct {
		key string
		val string
	}{
		{"background_override", cfg.BackgroundOverride},
		{"stamper_override", cfg.StamperOverride},
		{"right_title_overrdie", cfg.RightTitleOverrdie},
		{"path_indicator", cfg.PathIndicator},
	}
	for _, o := range overrides {
		if o.val != "" {
			setting[o.key] = o.val
		}
	}
	return setting
}

// adventureDataDoc 组装完整 LoginAdventureData（读面真源）。
func adventureDataDoc(dep Deps, uid string, st *AdventureState, cfg *AdventureConfig, nowSec int64) map[string]any {
	bitmap := make(map[string]any, len(st.ClaimedDays))
	for _, d := range st.ClaimedDays {
		bitmap[strconv.Itoa(d)] = true
	}
	// begin_date_i：东八区自然日 0 点 Unix 秒；坏数据回 0 会让客户端把
	// currentDay 算成「1970 起的天数」，故解析失败即视为配置错误（上层已校验）。
	beginUnix, err := AdventureDayStartUnix(st.BeginDate)
	if err != nil {
		beginUnix = 0
	}
	return map[string]any{
		"adventure_info": map[string]any{
			"adventure": map[string]any{
				"adventure_info":   bitmap,
				"begin_date_i":     beginUnix,
				"reward_config_id": cfg.RewardConfigID,
				"user_id":          AdventureUserIDNum(uid),
			},
		},
		"today_days":    AdventureCurrentDayText(st),
		"server_date_i": nowSec,
		"setting":       adventureSettingDoc(cfg),
	}
}

// adventureHandler GET|POST /adventure/：真实读面 + 当日首次发放。
//
// 「读面即发放」是设计口径（Note §设计增量 2）：客户端页面没有领取按钮，
// 奖励在当日首次拉取时由服务端写入 `/user_presents/` 内容池并由页面展示时
// 回刷礼物盒（LoginAdventurePage.cs:782-791）。发放与台账/当日弹窗标记
// 同事务，见 cn35store.AdventureState。
func adventureHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		nowSec := adventureNowSec(dep)
		if dep.Adventure == nil {
			// 未装配（旧进程/未启用）：保持历史空态，不报错。
			c.Decoded = map[string]any{
				"cn35": true, "uid": req.UserID, "endpoint": "adventure",
				"mode": "empty_state", "reason": "store_not_wired",
			}
			return cn35wire.MarshalJSON(map[string]any{"data": adventureEmptyStateData(req.UserID)})
		}
		st, cfg, err := dep.Adventure.AdventureState(req.UserID, nowSec)
		if err != nil {
			return nil, err
		}
		if st == nil || cfg == nil {
			c.Decoded = map[string]any{
				"cn35": true, "uid": req.UserID, "endpoint": "adventure",
				"mode": "empty_state", "reason": "no_config",
			}
			return cn35wire.MarshalJSON(map[string]any{"data": adventureEmptyStateData(req.UserID)})
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "adventure",
			"mode": "live", "round_index": st.RoundIndex,
			"reward_config_id": cfg.RewardConfigID, "duration_days": cfg.DurationDays,
			"current_day": st.CurrentDay, "today_shown": st.TodayShown,
			"granted_day": st.GrantedDay, "claimed": len(st.ClaimedDays),
			"begin_date": st.BeginDate, "present_uuid": st.PresentUUID,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"LoginAdventureData": adventureDataDoc(dep, req.UserID, st, cfg, nowSec),
			},
		})
	})
}
