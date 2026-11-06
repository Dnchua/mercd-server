// unit_ops_wire.go P0 写面（2026-09-28 Note）：/user_units/set_leader、
// set_helper、protect 协议端点。
//
// 客户端契约（UserUnitDetail.cs ilspy-cs 取证）：
//
//	/user_units/set_leader?id=<unit id>      （:441-442，回调另带 card_id 埋点）
//	/user_units/set_helper?id=<unit id>      （:460-461）
//	/user_units/protect?unit_id=<id>&protected=<true|false>  （:683-685）
//
// 响应约定与 training 相同：成功回推 data.user_units/user_monsters +
// data.user/data.user_coin（UpdateDataByResponse 消费）；失败
// status:error + modal（ResponseFilter 弹窗）。
package cn35game

import (
	"errors"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// unitSetRoleHandler set_leader/set_helper 共形：参数 id（set_leader 的
// 回调 body 还带 card_id，两种形态都接受）。
func unitSetRoleHandler(dep Deps, role string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		raw := firstNonEmpty(
			req.Query.Get("id"), req.Form.Get("id"),
			req.Query.Get("card_id"), req.Form.Get("card_id"))
		unitID, idErr := strconv.Atoi(raw)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_units_set_" + role,
			"unit_id": unitID,
		}
		if dep.UnitOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配同伴写面。"))
		}
		if idErr != nil || unitID <= 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "同伴参数无效，请重新打开同伴详情。"))
		}
		var serr error
		if role == "leader" {
			serr = dep.UnitOps.SetLeaderUnitID(req.UserID, unitID)
		} else {
			serr = dep.UnitOps.SetHelperUnitID(req.UserID, unitID)
		}
		if serr != nil {
			if errors.Is(serr, ErrUnitNotOwned) {
				return cn35wire.MarshalJSON(trainingError(
					"无法设置", "该同伴不在当前账号库存中。"))
			}
			return nil, serr
		}
		return unitOpsSuccess(dep, c, req)
	})
}

// unitProtectHandler /user_units/protect：protected 严格校验 true/false。
func unitProtectHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		rawID := firstNonEmpty(req.Query.Get("unit_id"), req.Form.Get("unit_id"))
		unitID, idErr := strconv.Atoi(rawID)
		rawProt := firstNonEmpty(req.Query.Get("protected"), req.Form.Get("protected"))
		protected, ok := parseStrictBool(rawProt)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_units_protect",
			"unit_id": unitID, "protected": rawProt,
		}
		if dep.UnitOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配同伴写面。"))
		}
		if idErr != nil || unitID <= 0 || !ok {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "保护参数无效，请重新打开同伴详情。"))
		}
		if err := dep.UnitOps.SetUnitProtected(req.UserID, unitID, protected); err != nil {
			if errors.Is(err, ErrUnitNotOwned) {
				return cn35wire.MarshalJSON(trainingError(
					"无法设置", "该同伴不在当前账号库存中。"))
			}
			return nil, err
		}
		return unitOpsSuccess(dep, c, req)
	})
}

// parseStrictBool 只接受客户端真实发出的 "true"/"false"。
func parseStrictBool(v string) (bool, bool) {
	switch v {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// unitOpsSuccess 写面成功响应：回推客户端 UpdateDataByResponse 所需键。
func unitOpsSuccess(dep Deps, c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
	data, err := writeBackData(dep, c, req.UserID)
	if err != nil {
		return nil, err
	}
	return cn35wire.MarshalJSON(map[string]any{
		"status": "success",
		"data":   data,
	})
}

// writeBackData 组装成功响应 data 段：最新库存 + 账号快照
// （leader/helper TEXT→NUMBER 修正与 preset_data 同路）。
func writeBackData(dep Deps, c *httpapi.Ctx, userID string) (map[string]any, error) {
	data, err := inventoryDataFor(dep, c, userID)
	if err != nil {
		return nil, err
	}
	snap, err := dep.Accounts.Snapshot(userID)
	if err != nil {
		return nil, err
	}
	numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
	data["user"] = snap.User
	data["user_coin"] = snap.UserCoin
	data["albums"] = nil
	// ITEM350-13：写面回推背包最新投影，领取礼物/物品操作后客户端
	// 返回背包即读到最新 user_items_hash。
	if dep.Items != nil {
		itemsData, ierr := userItemsData(dep, userID)
		if ierr != nil {
			return nil, ierr
		}
		for k, v := range itemsData {
			data[k] = v
		}
	}
	return data, nil
}
