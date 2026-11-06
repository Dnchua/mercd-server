// signin_wire.go LOGIN350-02：每月 28 格签到读/领接口。
//
// 端点：
//
//	GET|POST /accu_login_activity/          页面 AccLoginRewardsActivity 的
//	  requireData（app_conf data_key=acc_login_rewards / path=/accu_login_activity/）
//	  → data.user + data.acc_login_rewards（必须是数组）
//	GET|POST /accu_login_activity/receive?node=N  行内签到按钮
//	  （LoginRewardListRow.cs:87-100：GET baseURL + "/" + right_content.msg）
//	  → data.user + data.acc_login_rewards + modal（成功/不可领文案）
//
// 行文档形状（LoginRewardListRow.cs:28-55 逐字段消费）：
//
//	{day, rewards:[3 × {type_name, content_id, count}], right_content:{type,text,msg}}
//
// rewards **必须恰好 3 项**（客户端按下标 0..2 取，少于 3 会抛异常）；
// right_content.type=="text" 显示文案、否则显示按钮，按钮文案取 .text，
// 点击 URL = baseURL + "/" + .msg（msg 不带前导斜杠）。
package cn35game

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// signinRowDoc 组装单行（day + 3 奖励 + 右侧状态）。
func signinRowDoc(node int, claimed bool, day int, nowSec int64) map[string]any {
	reward := SigninNodeReward(int64(node), nowSec)
	rows := []any{
		map[string]any{"type_name": "coin", "content_id": 0, "count": reward.Coin},
		map[string]any{"type_name": "medal", "content_id": 0, "count": reward.Medal},
	}
	if reward.Gold > 0 {
		rows = append(rows, map[string]any{
			"type_name": "gacha_point", "content_id": 0, "count": reward.Gold})
	} else {
		rows = append(rows, map[string]any{
			"type_name":  "powerup_item",
			"content_id": reward.ItemContentID,
			"count":      reward.ItemCount,
		})
	}
	right := map[string]any{}
	switch {
	case claimed:
		right = map[string]any{"type": "text", "text": "已领取"}
	case SigninNodeOpen(node, day):
		right = map[string]any{
			"type": "button", "text": "签到",
			"msg": "accu_login_activity/receive?node=" + strconv.Itoa(node),
		}
	default:
		right = map[string]any{"type": "text", "text": "未开启"}
	}
	return map[string]any{
		"day":           "第" + strconv.Itoa(node) + "天",
		"rewards":       rows,
		"right_content": right,
	}
}

// signinRowsDoc 组装 28 行列表（data.acc_login_rewards 必须为数组）。
func signinRowsDoc(state *SigninState, nowSec int64) []any {
	rows := make([]any, 0, SigninNodeCount)
	for node := 1; node <= SigninNodeCount; node++ {
		rows = append(rows, signinRowDoc(node, state != nil && state.SigninClaimed(node),
			dayOf(state), nowSec))
	}
	return rows
}

func dayOf(state *SigninState) int {
	if state == nil {
		return 0
	}
	return state.Day
}

// signinRewardSummary 领取成功弹窗文案。
func signinRewardSummary(r SigninReward) string {
	body := fmt.Sprintf("钻石 ×%s\n徽章 ×%s", formatThousands(r.Coin), formatThousands(r.Medal))
	if r.Gold > 0 {
		body += "\n金币 ×" + formatThousands(r.Gold)
	}
	if r.ItemContentID > 0 && r.ItemCount > 0 {
		body += "\n培养素材 ×" + formatThousands(r.ItemCount)
	}
	return body
}

// signinShowHandler GET|POST /accu_login_activity/：签到列表读面（只读）。
func signinShowHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		nowSec := time.Now().Unix()
		state, err := dep.Signin.SigninState(req.UserID, nowSec)
		if err != nil {
			return nil, err
		}
		snap, err := loginBonusSnapshot(dep, req)
		if err != nil {
			return nil, err
		}
		rows := signinRowsDoc(state, nowSec)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "accu_login_activity",
			"month": state.MonthKey, "day": state.Day,
			"claimed": len(state.ClaimedNodes), "rows": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"user":              snap.User,
				"acc_login_rewards": rows,
			},
		})
	})
}

// signinClaimHandler GET|POST /accu_login_activity/receive?node=N：签到领取。
// 不可领时回刷新后的列表 + OK 弹窗（客户端 ResponseFilter 先行缓存数据，
// 随后行组件 RefreshPage 整页重拉）。
func signinClaimHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		nowSec := time.Now().Unix()
		raw := firstNonEmpty(req.Query.Get("node"), req.Form.Get("node"))
		node, perr := strconv.Atoi(raw)
		modalTitle := "签到奖励"
		modalBody := ""
		granted := false
		var reward SigninReward
		switch {
		case perr != nil || node < 1 || node > SigninNodeCount:
			modalBody = "签到数据异常，请重新进入签到页。"
		default:
			res, err := dep.Signin.ClaimSignin(req.UserID, nowSec, node)
			switch {
			case err == nil:
				granted = true
				reward = res.Reward
				modalBody = "签到成功\n" + signinRewardSummary(res.Reward)
			case errors.Is(err, ErrSigninNotReady), errors.Is(err, ErrSigninInvalidNode):
				modalBody = "该奖励今日已领取或尚未开启。"
			default:
				return nil, fmt.Errorf("cn35 签到领取失败: %w", err)
			}
		}
		state, err := dep.Signin.SigninState(req.UserID, nowSec)
		if err != nil {
			return nil, err
		}
		snap, err := loginBonusSnapshot(dep, req)
		if err != nil {
			return nil, err
		}
		rows := signinRowsDoc(state, nowSec)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "accu_login_activity_receive",
			"node": node, "granted": granted,
			"coin": reward.Coin, "medal": reward.Medal, "gold": reward.Gold,
			"item_content_id": reward.ItemContentID, "item_count": reward.ItemCount,
		}
		data := map[string]any{
			"user":              snap.User,
			"acc_login_rewards": rows,
		}
		if granted && reward.ItemContentID > 0 && reward.ItemCount > 0 {
			data["user_items"] = nil
			data["user_items_hash"] = nil
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": data,
			"modal": map[string]any{
				"type": "ok", "title": modalTitle, "body": modalBody,
			},
		})
	})
}
