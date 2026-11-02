// daily_shop_wire.go SHOP350-01：面包店（RedDiamondShop，「神秘的面包店」）
// 读面与每日领取写面。
//
// 端点：
//
//	GET|POST /red_diamond_shop/         商品列表+店铺信息
//	GET|POST /red_diamond_shop/buy      购买（?id=<goods_id>）
//	GET|POST /red_diamond_shop/refresh  手动刷新库存（?remain=<秒>）
//
// 客户端契约（ilspy 取证）：
//   - RedDiamondShopListParts.cs：activeTabId=0 时行集取
//     cache.Get("red_diamond_shop")（tab1 为 red_diamond_shop_fixed；
//     consts.red_diamond_shop_v2 未下发时组件自隐，仅 tab0 生效）。
//   - RedDiamondShopListRow.cs：行键 id/name/description/count(>1 显 ×N)/
//     bought(置灰「已购买」)/unlimited(存在且真→数量弹层)/cost_type
//     (0=钻 1=金币 2=红钻)/cost/type_name+content_id→UIUtil.IconAssetName。
//     cost=0 时余额校验 `num < 0` 恒假 → 0 元购可通过客户端检查；
//     无 unlimited → Yes/No 确认弹层后 SendRequest()（不带 count）。
//   - 购买上行 GET /red_diamond_shop/buy?id=<id>[&count=<n>]；判定
//     _json.result：false→按 msg 弹错误框，true→ResponseFilter+
//     UpdateDataByResponse（data 键平铺入缓存即时刷新）。
//   - RedDiamondShopPage.cs：red_diamond_shop_info.refresh_time 驱动
//     库存刷新倒计时（归零自动 FreeRefresh→GET /red_diamond_shop/）；
//     refresh_cost/refresh_cost_type 为手动刷新价（0=免费，走
//     /red_diamond_shop/refresh?remain=<剩余秒>）。
package cn35game

import (
	"errors"
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// dailyShopLoc 每日周期判定时区（国服东八区，无夏令时；口径同 guildLoc）。
var dailyShopLoc = time.FixedZone("UTC+8", 8*3600)

// dailyShopPeriodKey 本周期键：东八区 now-RefreshHour 的日期。
// 4:00 刷新 → 4:00 前归属上一日。
func dailyShopPeriodKey(now time.Time) string {
	return now.In(dailyShopLoc).
		Add(-time.Duration(DailyShopRules.RefreshHour) * time.Hour).
		Format("2006-01-02")
}

// dailyShopNextRefreshSec 距下一次东八区 RefreshHour 点的秒数（>0）。
func dailyShopNextRefreshSec(now time.Time) int64 {
	local := now.In(dailyShopLoc)
	next := time.Date(local.Year(), local.Month(), local.Day(),
		DailyShopRules.RefreshHour, 0, 0, 0, dailyShopLoc)
	if !local.Before(next) {
		next = next.Add(24 * time.Hour)
	}
	// 上取整：不足 1 秒时归 1，避免客户端在刷新点前读到 0 提前刷新。
	return int64((next.Sub(local) + time.Second - 1) / time.Second)
}

// dailyShopRows 商品行集：bought 取账号本周期的领取计数。
func dailyShopRows(dep Deps, userID, periodKey string) ([]any, error) {
	claims, err := dep.DailyShop.DailyShopClaims(userID, periodKey)
	if err != nil {
		return nil, err
	}
	r := DailyShopRules
	row := map[string]any{
		"id":          r.GoodsID,
		"name":        r.Name,
		"description": r.Description,
		"count":       r.Coin, // ×100 展示（领取量即行数量）
		"bought":      claims[r.GoodsKey] >= r.DailyLimit,
		"cost_type":   0, // 货币图标=钻石，0 元购
		"cost":        0,
		"type_name":   "coin",
		"content_id":  0,
	}
	return []any{row}, nil
}

// dailyShopData 列表+店铺信息文档（buy/refresh 回推同构）。
func dailyShopData(dep Deps, userID string, now time.Time) (map[string]any, error) {
	rows, err := dailyShopRows(dep, userID, dailyShopPeriodKey(now))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"red_diamond_shop":       rows,
		"red_diamond_shop_fixed": []any{},
		"red_diamond_shop_info": map[string]any{
			"refresh_time":              dailyShopNextRefreshSec(now),
			"refresh_cost_type":         0,
			"refresh_cost":              0,
			"expression_change_enabled": true,
		},
	}, nil
}

// functionPortalHandler /users/function_portal：主人公の日常页右上
// 活动入口列表（function_portal_list）+ 页面杂项键（function_portal）。
// 客户端契约（取证）：
//   - RightListParts（ListParts）：行键 texture_asset_name→banner 图、
//     show_page_message→RowClick 跳转 PageMessage（base 是 Resources
//     预制体路径而非 conf 页键/类名；页预制体不存在时客户端报
//     NullReferenceException 静默无反应——无定制预制体的页统一用
//     缺省 Common/Pages/Basic，RedDiamondShop 有专用预制体）。
//   - MainCharaDailyIconParts：function_portal.has_new_main_chara_daily
//     控制入口红点；ButtonsParts label.key 读
//     chara_buff_button_key/fate_button_key，disable_key 读
//     chara_buff_button_disable/fate_button_disable。
//
// banner 素材在远端资源叠加层下发（server-state-cn35/remote_assets/common）。
// 入口集对齐原版主人公の日常右列五项（实机视频取证）；目标页的
// requireData 多数仍空态（页可开、内容待实现）——链路见
// docs/架构解析/13-主人公日常入口与二级跳转链路.md。
var functionPortalList = []map[string]any{
	{
		"id":                 2,
		"name":               "皇家学院",
		"texture_asset_name": "common/bnr_college",
		"show_page_message":  "name=CollegeSelectInterface&base=Common/Pages/Basic&method=Stack",
	},
	{
		"id":                 3,
		"name":               "羁绊之屋",
		"texture_asset_name": "common/bnr_unit_friendship",
		"show_page_message":  "name=UnitFriendshipMainPage&base=Common/Pages/Basic&method=Stack",
	},
	{
		"id":                 DailyShopRules.GoodsID,
		"name":               "每日面包店",
		"texture_asset_name": "common/bnr_red_diamond_shop",
		"show_page_message":  "name=RedDiamondShop&base=Common/Pages/RedDiamondShopPage&method=Stack",
	},
	{
		"id":                 4,
		"name":               "真爱礼堂",
		"texture_asset_name": "common/bnr_true_love",
		"show_page_message":  "name=TrueLoveMainPage&base=Common/Pages/Basic&method=Stack",
	},
	{
		"id":                 5,
		"name":               "同伴交换所",
		"texture_asset_name": "common/bnr_unit_exchange_150201",
		"show_page_message":  "name=UnitExchange&base=Common/Pages/Basic&method=Stack",
	},
}

func functionPortalHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "function_portal",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"function_portal": map[string]any{
					"has_new_main_chara_daily":  false,
					"chara_buff_button_key":     "真·男主光环",
					"fate_button_key":           "艾琳的小屋",
					"chara_buff_button_disable": false,
					"fate_button_disable":       false,
				},
				"function_portal_list": func() []any {
					rows := make([]any, len(functionPortalList))
					for i, r := range functionPortalList {
						rows[i] = r
					}
					return rows
				}(),
			},
		})
	})
}

// redDiamondShopHandler /red_diamond_shop/ 商品列表读面（兼 FreeRefresh）。
func redDiamondShopHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		data, err := dailyShopData(dep, req.UserID, time.Now())
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "red_diamond_shop",
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// redDiamondShopBuyHandler /red_diamond_shop/buy?id=<goods_id>[&count=<n>]：
// 领取写面。响应契约 {result:bool, msg?, data?}（RedDiamondShopListRow.cs
// SendRequest/IsValidResponse）——不走 status:success 信封，result=false
// 时客户端按 msg 弹「购买失败」框。
func redDiamondShopBuyHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		goodsID, idErr := strconv.Atoi(firstNonEmpty(req.Query.Get("id"), req.Form.Get("id")))
		countStr := firstNonEmpty(req.Query.Get("count"), req.Form.Get("count"))
		count := 0
		var cntErr error
		if countStr != "" {
			count, cntErr = strconv.Atoi(countStr)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "red_diamond_shop_buy",
			"goods_id": goodsID,
		}
		resultErr := func(msg string) ([]byte, error) {
			return cn35wire.MarshalJSON(map[string]any{
				"result": false, "msg": msg})
		}
		if idErr != nil || cntErr != nil || goodsID != DailyShopRules.GoodsID ||
			count < 0 || count > DailyShopRules.DailyLimit {
			return resultErr("商品参数无效。")
		}
		r := DailyShopRules
		balance, err := dep.DailyShop.ClaimDailyPack(req.UserID, r.GoodsKey,
			dailyShopPeriodKey(time.Now()), r.Coin, r.DailyLimit)
		if err != nil {
			switch {
			case errors.Is(err, ErrDailyShopClaimed):
				return resultErr("今日已领取，凌晨 4 点刷新后可再来。")
			case errors.Is(err, ErrDailyShopGoodsNotFound):
				return resultErr("该商品当前不可购买。")
			case errors.Is(err, ErrInvalidRequest):
				return resultErr("购买请求不合法。")
			}
			return nil, err
		}
		data, err := dailyShopData(dep, req.UserID, time.Now())
		if err != nil {
			return nil, err
		}
		data["user_coin"] = map[string]any{"amount": balance}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		data["user"] = snap.User
		c.Decoded["coin"] = balance
		return cn35wire.MarshalJSON(map[string]any{
			"result": true, "data": data})
	})
}

// redDiamondShopRefreshHandler /red_diamond_shop/refresh?remain=<秒>：
// 手动刷新按钮契约 {result:bool,msg?,data?}。每日商品无随机货池——库存
// 由周期决定，免费刷新仅回推最新行集（refresh_cost=0 客户端不收钱）。
func redDiamondShopRefreshHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		data, err := dailyShopData(dep, req.UserID, time.Now())
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "red_diamond_shop_refresh",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"result": true, "data": data})
	})
}
