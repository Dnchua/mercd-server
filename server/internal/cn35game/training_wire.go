// training_wire.go STAT350-05A：/user_units/training 协议端点。
//
// 客户端契约（UnitLevelupModal.cs:295-351）：
//
//	GET /user_units/training?id={id}&before={orgLevel}&after={level}
//	  成功：ResponseFilter 为 false → 播 SWFUnitLevelup 并
//	        cache.UpdateDataByResponse(responseDoc) —— 响应必须自带
//	        data.user_units（及适用时的 data.user / data.user_coin）。
//	  失败：{"status":"error","modal":{...}} → 弹窗并复位按钮。
//
// 校验（事务内，见 UnitTrainingStore）：归属 → before==当前 level（重放/并发
// 明确拒绝，不作隐式短路）→ after ∈ (before, current_max_level] → 扣
// gacha_point（成本 = expTable 差，同事务）→ 只更新 level。
package cn35game

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

func unitTrainingHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		unitID, idErr := strconv.Atoi(firstNonEmpty(req.Query.Get("id"), req.Form.Get("id")))
		before, beforeErr := strconv.Atoi(firstNonEmpty(req.Query.Get("before"), req.Form.Get("before")))
		after, afterErr := strconv.Atoi(firstNonEmpty(req.Query.Get("after"), req.Form.Get("after")))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_units_training",
			"unit_id": unitID, "before": before, "after": after,
		}
		if dep.Training == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"训练功能未启用", "服务端未装配养成写面。"))
		}
		if idErr != nil || beforeErr != nil || afterErr != nil || unitID <= 0 {
			return cn35wire.MarshalJSON(trainingError(
				"训练参数非法", "等级参数无效，请重新打开升级界面。"))
		}

		rec, found, err := findUnitRecord(dep, req.UserID, unitID)
		if err != nil {
			return nil, err
		}
		if !found {
			return cn35wire.MarshalJSON(trainingError(
				"无法升级", "该同伴不在当前账号库存中。"))
		}
		u, err := dep.Catalog.Unit(unitID)
		if err != nil {
			return nil, fmt.Errorf("cn35 训练目标模板缺失 unit_id=%d: %w", unitID, err)
		}
		// 当前觉醒上限（C0.2 口径）。dup 只由觉醒写面（05C，未启用）变更，
		// 故此处读取与事务内的 dup 一致；level 的并发由事务内 before 校验兜底。
		maxLevel := statmodel.CurrentMaxLevel(u.Rare, rec.DuplicatedCount)

		if before != rec.Level {
			return cn35wire.MarshalJSON(trainingError(
				"等级已变化", "该同伴的等级已更新，请重新打开升级界面。"))
		}
		if after <= before || after > maxLevel {
			return cn35wire.MarshalJSON(trainingError(
				"已达等级上限", fmt.Sprintf("目标等级须在 %d 到 %d 之间。", before+1, maxLevel)))
		}
		remaining, terr := dep.Training.TrainUnit(req.UserID, unitID, before, after, maxLevel)
		if terr != nil {
			switch {
			case errors.Is(terr, ErrTrainingPoint):
				return cn35wire.MarshalJSON(trainingError(
					"金币不足", "升级所需的强化点数不足。"))
			case errors.Is(terr, ErrTrainingStale), errors.Is(terr, ErrTrainingOutOfRange),
				errors.Is(terr, ErrTrainingUnitNotFound):
				return cn35wire.MarshalJSON(trainingError(
					"无法升级", "请求与当前状态不符，请重新打开升级界面。"))
			}
			return nil, terr
		}
		_ = remaining

		// 回推最新读面：client UpdateDataByResponse 只在 SWF 结束后入 cache，
		// 响应必须自带 data.user_units 与 data.user（gacha_point）。
		data, err := inventoryDataFor(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 训练后快照失败: %w", err)
		}
		numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
		data["user"] = snap.User
		data["user_coin"] = snap.UserCoin
		data["albums"] = nil
		c.Decoded["trained"] = true
		c.Decoded["after"] = after
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

// unitGrowthUnsupportedHandler /user_units/* 兜底：05B（潜能）/05C（觉醒）/
// 05D（魔宠强化）/其他未取证子项一律明确拒绝，**不伪造成功响应**。
// P0 批（2026-09-28 Note）按客户端实际调用路径细分原因文案。
//
// 路径对应关系：
//   - /user_units/upgrade    → 05B 潜能升级（需 unit_upgrade_config 来源 + 迁移）
//   - /user_units/awake*     → 05C 觉醒（上行路径未取证，只保留读面）
//   - /user_units/sell       → 出售（UserUnitDetail.cs:674-679；价格未取证）
//   - /user_units/powerup*   → 强化（素材/消耗配置未取证）
//   - /user_units/evolve*    → 进化族（映射/消耗配置未取证）
//   - dismantle/compose/decompose → 分解/合成（产出配置未取证）
//   - /user_units/share      → 分享（规则未取证）
//   - 其他                    → 未取证写面
func unitGrowthUnsupportedHandler(dep Deps) httpapi.Handler {
	unitsH := userUnitsHandler(dep)
	return func(c *httpapi.Ctx) error {
		if c.R.URL.Path == "/user_units/" {
			return unitsH(c)
		}
		return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
			path := c.R.URL.Path
			title, body := "功能未启用", "该功能尚未在本地服务端实现。"
			switch {
			case strings.Contains(path, "sell"):
				// sell?unit_id=&price=：price 由客户端回传，服务端无价格规则
				// 取证来源，不做零价假发放。
				title, body = "出售未启用", "出售价格未取证，暂不开放出售。"
			case strings.Contains(path, "upgrade"):
				title, body = "潜能强化未启用", "潜能配置来源尚未取证，暂不开放潜能强化。"
			case strings.Contains(path, "powerup"):
				title, body = "同伴强化未启用", "同伴强化规则与消耗配置未取证，暂不开放强化。"
			case strings.Contains(path, "awake") || strings.Contains(path, "duplicate"):
				title, body = "觉醒未启用", "觉醒上行路径尚未取证，暂不开放觉醒。"
			case strings.Contains(path, "evolve"):
				// evolve/evolve_egg/evolve_monster_select_info/show_evolve_board。
				title, body = "同伴进化未启用", "同伴进化映射与消耗配置未取证，暂不开放进化。"
			case strings.Contains(path, "dismantle"), strings.Contains(path, "decompose"),
				strings.Contains(path, "compose"):
				title, body = "分解/合成未启用", "分解/合成配置未取证，暂不开放相关操作。"
			case strings.Contains(path, "share"):
				title, body = "同伴分享未启用", "同伴分享规则未取证，暂不开放分享。"
			}
			c.Decoded = map[string]any{
				"cn35": true, "uid": req.UserID, "endpoint": "user_units_unsupported",
				"path": path, "unsupported": true,
			}
			return cn35wire.MarshalJSON(map[string]any{
				"status": "error",
				"modal":  map[string]any{"type": "ok", "title": title, "body": body},
			})
		})(c)
	}
}

// findUnitRecord 在会话账号同行库存中定位活跃 unit_id（待机所同伴不参与训练/符文装备/觉醒书消耗）。
func findUnitRecord(dep Deps, userID string, unitID int) (UnitRecord, bool, error) {
	recs, err := dep.Inventory.Units(userID)
	if err != nil {
		return UnitRecord{}, false, fmt.Errorf("cn35 训练读取库存失败: %w", err)
	}
	for _, r := range recs {
		if r.UnitID == unitID && !r.Waiting {
			return r, true, nil
		}
	}
	return UnitRecord{}, false, nil
}

// trainingError 失败响应：modal 键使客户端 ResponseFilter 返回 true（弹窗+复位）。
func trainingError(title, body string) map[string]any {
	return map[string]any{"status": "error", "modal": map[string]any{
		"type": "ok", "title": title, "body": body,
	}}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
