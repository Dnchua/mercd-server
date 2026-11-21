// fate_wire.go FATE350-02：M1 命运星盘（艾琳的小屋 / 主人公的日常）HTTP 协议处理层。
//
// 端点：
//   - GET|POST /fate/show               艾琳的小屋星盘详情读面（data.fate）
//   - GET|POST /fate/fate_buff_summary  主人公的日常星盘加成汇总读面（data.fate_buff_summary）
//   - GET|POST /fate/roll               转动星盘写面（?type=all|inner&confirm=false|true）
package cn35game

import (
	"errors"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func fateNow(dep Deps) time.Time {
	if dep.FateClock != nil {
		return dep.FateClock()
	}
	if dep.GuildClock != nil {
		return dep.GuildClock()
	}
	return time.Now()
}

// functionPortalDoc 组装 /users/function_portal 与 /fate/roll 共用的 data.function_portal 对象。
// 当玩家当日尚未进行免费星盘首抽（today_roll_times==0）时，has_new_main_chara_daily 置为 true；
// 本轮放弃主角光环，chara_buff_button_disable 置为 true（客户端将「真·男主光环」按钮置灰禁用）。
func functionPortalDoc(dep Deps, userID string) map[string]any {
	hasNew := false
	if dep.Fate != nil && userID != "" {
		if st, err := dep.Fate.FateState(userID, FatePeriodKey(fateNow(dep))); err == nil && st != nil {
			hasNew = st.TodayRollTimes == 0
		}
	}
	return map[string]any{
		"has_new_main_chara_daily":  hasNew,
		"chara_buff_button_disable": true,
		"chara_buff_button_key":     "真·男主光环",
		"fate_button_disable":       false,
		"fate_button_key":           "艾琳的小屋",
	}
}

// fateEntriesFor 供库存/战斗结算读取玩家当日生效的星盘词条列表；未装配或出错时返回空切片。
func fateEntriesFor(dep Deps, userID string) []FateEntry {
	if dep.Fate == nil || userID == "" {
		return nil
	}
	st, err := dep.Fate.FateState(userID, FatePeriodKey(fateNow(dep)))
	if err != nil || st == nil || st.TodayRollTimes <= 0 {
		return nil
	}
	return st.FateList
}

// fateShowHandler GET|POST /fate/show：星盘详情读面。
func fateShowHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		periodKey := FatePeriodKey(fateNow(dep))
		st, err := dep.Fate.FateState(req.UserID, periodKey)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "fate_show",
			"period": periodKey, "today_roll_times": st.TodayRollTimes,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"fate": FateShowWire(st),
			},
		})
	})
}

// fateBuffSummaryHandler GET|POST /fate/fate_buff_summary：主人公日常页星盘汇总读面。
func fateBuffSummaryHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		periodKey := FatePeriodKey(fateNow(dep))
		st, err := dep.Fate.FateState(req.UserID, periodKey)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "fate_buff_summary",
			"period": periodKey, "today_roll_times": st.TodayRollTimes,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"fate_buff_summary": FateBuffSummaryWire(st),
			},
		})
	})
}

// fateRollHandler GET|POST /fate/roll?type=<all|inner>&confirm=<false|true>：星盘转动写面。
func fateRollHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		rollType := strings.TrimSpace(firstNonEmpty(req.Query.Get("type"), req.Form.Get("type")))
		confirmStr := strings.TrimSpace(firstNonEmpty(req.Query.Get("confirm"), req.Form.Get("confirm")))
		confirm := strings.EqualFold(confirmStr, "true") || confirmStr == "1"
		periodKey := FatePeriodKey(fateNow(dep))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "fate_roll",
			"type": rollType, "confirm": confirm, "period": periodKey,
		}
		res, err := dep.Fate.RollFate(req.UserID, periodKey, rollType, confirm, dep.BattleRand)
		if err != nil {
			switch {
			case errors.Is(err, ErrFateNeedConfirm):
				return cn35wire.MarshalJSON(map[string]any{
					"astrolabe_need_confirm": true,
					"modal": map[string]any{
						"title": "确认",
						"type":  "yes_no",
						"body":  "当前星盘中含有极佳运势，重新转动将覆盖现有加成，是否确认转动？",
					},
					"data": map[string]any{
						"temp_fate_roll_result": false,
					},
				})
			case errors.Is(err, ErrFateInsufficientCoin):
				return cn35wire.MarshalJSON(map[string]any{
					"modal": map[string]any{
						"title": "提示",
						"body":  "红钻不足，无法转动星盘。",
					},
					"data": map[string]any{
						"temp_fate_roll_result": false,
					},
				})
			case errors.Is(err, ErrFateInnerRequiresFirstRoll), errors.Is(err, ErrInvalidRequest):
				return cn35wire.MarshalJSON(map[string]any{
					"modal": map[string]any{
						"title": "提示",
						"body":  "请先进行今日首次星盘占卜。",
					},
					"data": map[string]any{
						"temp_fate_roll_result": false,
					},
				})
			}
			return nil, err
		}
		c.Decoded["today_roll_times"] = res.State.TodayRollTimes
		c.Decoded["cost_coin"] = res.CostCoin
		c.Decoded["balance_coin"] = res.BalanceCoin
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"fate":                  FateShowWire(res.State),
				"temp_fate_roll_result": true,
				"user_red_coin":         map[string]any{"amount": res.BalanceCoin},
				"user_coin":             map[string]any{"amount": res.BalanceCoin},
				"fate_buff_summary":     FateBuffSummaryWire(res.State),
				"function_portal":       functionPortalDoc(dep, req.UserID),
				// 原服通过下发 user_units: null 触发客户端 DataCache.Add -> Expire("user_units")，
				// 使玩家随后打开同伴页时重新请求 /user_units/ 拉取叠加最新星盘加成后的面板。
				"user_units": nil,
			},
		})
	})
}
