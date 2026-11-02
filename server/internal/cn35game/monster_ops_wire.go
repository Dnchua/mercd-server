// monster_ops_wire.go P0 写面（2026-09-28 Note）：/user_monsters/on、
// off、protect 协议端点 + 未取证魔宠写面的明确拒绝。
//
// 客户端契约（ilspy-cs 取证）：
//
//	/user_monsters/on?uuid=<uuid>&unit_id=<unit id>
//	    （UserUnitDetail.cs:566、UserMonsterDetail.cs:390）
//	/user_monsters/off?uuid=<uuid>&unit_id=<unit id>
//	    （UserUnitDetail.cs:542/602、UserMonsterDetail.cs:366/414）
//	/user_monsters/off?uuid=<uuid>
//	    （MonsterButtonsListComponent.cs:382，不带宿主形态）
//	/user_monsters/protect?uuids[]=<uuid>&protected=<true|"fasle">
//	    （UserMonsterDetail.cs:515-516——客户端解保护时发拼写错误的
//	    "fasle"，兼容处理：非 "true" 一律按 false）
//
// 响应约定与 unit_ops 相同：成功 data 回推库存+快照；失败 modal。
package cn35game

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func monsterOnHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuid := firstNonEmpty(req.Query.Get("uuid"), req.Form.Get("uuid"))
		rawID := firstNonEmpty(req.Query.Get("unit_id"), req.Form.Get("unit_id"))
		unitID, idErr := strconv.Atoi(rawID)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_on",
			"uuid": uuid, "unit_id": unitID,
		}
		if dep.MonsterOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配魔宠写面。"))
		}
		if uuid == "" || idErr != nil || unitID <= 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "魔宠或同伴参数无效。"))
		}
		if err := dep.MonsterOps.EquipMonster(req.UserID, uuid, unitID); err != nil {
			switch {
			case errors.Is(err, ErrMonsterNotOwned):
				return cn35wire.MarshalJSON(trainingError(
					"无法装备", "该魔宠不在当前账号库存中。"))
			case errors.Is(err, ErrUnitNotOwned):
				return cn35wire.MarshalJSON(trainingError(
					"无法装备", "目标同伴不在当前账号库存中。"))
			}
			return nil, err
		}
		return unitOpsSuccess(dep, c, req)
	})
}

func monsterOffHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuid := firstNonEmpty(req.Query.Get("uuid"), req.Form.Get("uuid"))
		rawID := firstNonEmpty(req.Query.Get("unit_id"), req.Form.Get("unit_id"))
		unitID := -1 // 未指定宿主形态（MonsterButtonsListComponent.cs:382）
		if rawID != "" {
			var idErr error
			unitID, idErr = strconv.Atoi(rawID)
			if idErr != nil {
				c.Decoded = map[string]any{
					"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_off",
					"uuid": uuid, "unit_id": rawID,
				}
				return cn35wire.MarshalJSON(trainingError(
					"参数非法", "同伴参数无效。"))
			}
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_off",
			"uuid": uuid, "unit_id": unitID,
		}
		if dep.MonsterOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配魔宠写面。"))
		}
		if uuid == "" {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "魔宠参数无效。"))
		}
		if err := dep.MonsterOps.UnequipMonster(req.UserID, uuid, unitID); err != nil {
			switch {
			case errors.Is(err, ErrMonsterNotOwned):
				return cn35wire.MarshalJSON(trainingError(
					"无法卸下", "该魔宠不在当前账号库存中。"))
			case errors.Is(err, ErrMonsterNotEquipped):
				return cn35wire.MarshalJSON(trainingError(
					"无法卸下", "该魔宠当前未装备在同伴上。"))
			case errors.Is(err, ErrMonsterHostMismatch):
				return cn35wire.MarshalJSON(trainingError(
					"无法卸下", "该魔宠未装备在指定的同伴上。"))
			}
			return nil, err
		}
		return unitOpsSuccess(dep, c, req)
	})
}

func monsterProtectHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuids := collectUUIDs(req)
		rawProt := firstNonEmpty(req.Query.Get("protected"), req.Form.Get("protected"))
		// 客户端解保护时发拼写错误 "fasle"（UserMonsterDetail.cs:518）：
		// "true"→true，其余非空值（含 "fasle"/"false"）一律按 false。
		protected := rawProt == "true"
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_protect",
			"uuids": uuids, "protected": rawProt,
		}
		if dep.MonsterOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配魔宠写面。"))
		}
		if len(uuids) == 0 || rawProt == "" {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "魔宠保护参数无效。"))
		}
		if err := dep.MonsterOps.SetMonsterProtected(req.UserID, uuids, protected); err != nil {
			if errors.Is(err, ErrMonsterNotOwned) {
				return cn35wire.MarshalJSON(trainingError(
					"无法设置", "包含不在当前账号库存中的魔宠。"))
			}
			return nil, err
		}
		return unitOpsSuccess(dep, c, req)
	})
}

// monsterPowerupHandler /user_monsters/powerup?uuid=&powerup_item_id=&count=
// （MonsterPowerupModal.cs:184）：消耗 count 个素材，Size 按客户端步长推进。
func monsterPowerupHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuid := firstNonEmpty(req.Query.Get("uuid"), req.Form.Get("uuid"))
		itemID, itemErr := strconv.Atoi(firstNonEmpty(
			req.Query.Get("powerup_item_id"), req.Form.Get("powerup_item_id")))
		count := 1
		var cntErr error
		if raw := firstNonEmpty(req.Query.Get("count"), req.Form.Get("count")); raw != "" {
			count, cntErr = strconv.Atoi(raw)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_powerup",
			"uuid": uuid, "powerup_item_id": itemID, "count": count,
		}
		if dep.MonsterOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配魔宠写面。"))
		}
		if uuid == "" || itemErr != nil || itemID <= 0 ||
			cntErr != nil || count <= 0 || count > 999 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "强化参数无效，请重新打开魔宠界面。"))
		}
		// 素材档位校验需要魔宠模板属性（ScaleCategory type=attr_id）；
		// 库存行未命中时仍进入 store，由归属校验报 NotOwned。
		attrID := 0
		mrecs, merr := dep.Inventory.Monsters(req.UserID)
		if merr != nil {
			return nil, fmt.Errorf("cn35 魔宠库存读取失败: %w", merr)
		}
		for _, r := range mrecs {
			if r.UUID != uuid {
				continue
			}
			m, cerr := dep.Catalog.Monster(r.MonsterID)
			if cerr != nil {
				return nil, fmt.Errorf("cn35 魔宠模板缺失 monster_id=%d: %w", r.MonsterID, cerr)
			}
			attrID = m.Element
			break
		}
		res, err := dep.MonsterOps.MonsterPowerup(req.UserID, uuid, attrID, itemID, count)
		if err != nil {
			switch {
			case errors.Is(err, ErrMonsterNotOwned):
				return cn35wire.MarshalJSON(trainingError(
					"无法强化", "该魔宠不在当前账号库存中。"))
			case errors.Is(err, ErrItemNotFound):
				return cn35wire.MarshalJSON(trainingError(
					"无法强化", "该素材不可用于强化。"))
			case errors.Is(err, ErrItemInsufficient):
				return cn35wire.MarshalJSON(trainingError(
					"素材不足", "持有的进化素材不足以完成强化。"))
			case errors.Is(err, ErrMonsterScaleCap):
				return cn35wire.MarshalJSON(trainingError(
					"已达上限", "该魔宠的体型已达强化上限。"))
			case errors.Is(err, ErrMonsterRarePowerup):
				return cn35wire.MarshalJSON(trainingError(
					"无法强化", "该魔宠的强化暂未开放。"))
			case errors.Is(err, ErrMonsterPowerupItem):
				return cn35wire.MarshalJSON(trainingError(
					"素材不符", "该素材与魔宠的属性或体型档位不匹配。"))
			}
			return nil, err
		}
		// 客户端把响应写回 user_monsters 缓存行：scale/real_scale 必须是
		// JSON 数值（Document.dec 无法解析字符串，同 inventory_wire 下发）。
		// 校验由 store 保证为十进制字符串，这里转换失败按服务端错误处理。
		rs, rerr := strconv.ParseFloat(strings.TrimSpace(res.RealScale), 64)
		if rerr != nil {
			return nil, fmt.Errorf("cn35 powerup 回推 scale 非法: %w", rerr)
		}
		c.Decoded["scale"] = rs
		return unitOpsSuccess(dep, c, req)
	})
}

// collectUUIDs 归集 uuids[]/uuids 两种键的数组与标量形态。
func collectUUIDs(req *cn35wire.GameRequest) []string {
	var out []string
	for _, key := range []string{"uuids[]", "uuids"} {
		for _, src := range []map[string][]string{req.Query, req.Form} {
			for _, v := range src[key] {
				v = strings.TrimSpace(v)
				if v != "" {
					out = append(out, v)
				}
			}
		}
	}
	// 单 uuid 形态兜底（sell 等路径用 uuids[]，详情页卸下用 uuid）。
	if len(out) == 0 {
		if v := firstNonEmpty(req.Query.Get("uuid"), req.Form.Get("uuid")); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// monsterEvolveSelectInfoHandler /user_monsters/evolve_select_info：
// 魔宠进化选择页静态说明信息（UserMonsters requireData.user_monsters_evolve_select_info）。
func monsterEvolveSelectInfoHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_evolve_select_info",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"user_monsters_evolve_select_info": map[string]any{
					"egg_btn_label":     "进化之卵",
					"help_body":         "可选择消耗同类魔宠或进化之卵来提升王宠体型上限。",
					"help_title":        "魔宠进化说明",
					"monster_btn_label": "同类魔宠",
					"step1_asset_name":  "common/monster_evolve_step1",
					"step1_desc":        "选择需要进化的王宠",
					"step1_title":       "第一步",
					"step2_asset_name":  "common/monster_evolve_step2",
					"step2_desc":        "选择进化素材完成进化",
					"step2_title":       "第二步",
					"title":             "魔宠进化",
				},
			},
		})
	})
}

// monsterUnsupportedHandler 未取证魔宠写面的明确拒绝（替代 501/伪造成功）。
// 路径→未证原因（客户端行号见 cn35game.go 注册注释）：
func monsterUnsupportedHandler(dep Deps) httpapi.Handler {
	monstersH := userMonstersHandler(dep)
	return func(c *httpapi.Ctx) error {
		if c.R.URL.Path == "/user_monsters/" {
			return monstersH(c)
		}
		return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
			path := c.R.URL.Path
			title, body := "功能未启用", "该功能尚未在本地服务端实现。"
			switch {
			case strings.Contains(path, "sell"):
				title, body = "魔宠出售未启用", "魔宠出售价格规则未取证，暂不开放出售。"
			case strings.Contains(path, "powerup"):
				title, body = "魔宠强化未启用", "魔宠强化规则与消耗素材配置未取证，暂不开放强化。"
			case strings.Contains(path, "share"):
				title, body = "魔宠分享未启用", "魔宠分享规则未取证，暂不开放分享。"
			case strings.Contains(path, "evolve"):
				title, body = "魔宠进化未启用", "魔宠进化映射与消耗配置未取证，暂不开放进化。"
			}
			c.Decoded = map[string]any{
				"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_unsupported",
				"path": path, "unsupported": true,
			}
			return cn35wire.MarshalJSON(map[string]any{
				"status": "error",
				"modal":  map[string]any{"type": "ok", "title": title, "body": body},
			})
		})(c)
	}
}
