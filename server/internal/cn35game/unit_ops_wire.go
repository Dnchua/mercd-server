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
	"fmt"
	"strconv"
	"strings"

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
	if dep.Decks != nil {
		if decks, derr := dep.Decks.Decks(userID); derr == nil {
			data["user_decks"] = DecksWire(decks)
		}
	}
	if dep.Skills != nil {
		if skData, serr := userSkillsData(dep, userID); serr == nil {
			for k, v := range skData {
				data[k] = v
			}
		}
	}
	return data, nil
}

func collectSellUnitIDs(req *cn35wire.GameRequest) ([]int, error) {
	var rawList []string
	for _, key := range []string{"ids[]", "ids", "unit_ids[]", "unit_ids"} {
		for _, src := range []map[string][]string{req.Query, req.Form} {
			for _, v := range src[key] {
				for _, part := range strings.Split(v, ",") {
					part = strings.TrimSpace(part)
					if part != "" {
						rawList = append(rawList, part)
					}
				}
			}
		}
	}
	if len(rawList) == 0 {
		if v := firstNonEmpty(req.Query.Get("id"), req.Form.Get("id"), req.Query.Get("unit_id"), req.Form.Get("unit_id")); v != "" {
			rawList = append(rawList, strings.TrimSpace(v))
		}
	}
	if len(rawList) == 0 {
		return nil, ErrInvalidRequest
	}
	out := make([]int, 0, len(rawList))
	for _, s := range rawList {
		id, err := strconv.Atoi(s)
		if err != nil || id <= 0 {
			return nil, ErrInvalidRequest
		}
		out = append(out, id)
	}
	return out, nil
}

// unitSellHandler GET|POST /user_units/sell?ids[]=<id>（M5 T06）。
func unitSellHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		unitIDs, idErr := collectSellUnitIDs(req)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_units_sell",
			"unit_ids": unitIDs,
		}
		if dep.UnitOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配同伴写面。"))
		}
		if idErr != nil || len(unitIDs) == 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "请选择需要出售的同伴。"))
		}
		urecs, err := dep.Inventory.Units(req.UserID)
		if err != nil {
			return nil, err
		}
		prices := make(map[int]int64, len(urecs))
		for _, r := range urecs {
			if u, uerr := dep.Catalog.Unit(r.UnitID); uerr == nil {
				prices[r.UnitID] = int64(unitSellPrice(u.Rare, r.DuplicatedCount))
			}
		}
		if _, serr := dep.UnitOps.SellUnits(req.UserID, unitIDs, prices); serr != nil {
			switch {
			case errors.Is(serr, ErrUnitNotOwned):
				return cn35wire.MarshalJSON(trainingError(
					"无法出售", "包含不在当前同行列表中的同伴。"))
			case errors.Is(serr, ErrUnitSellProtected):
				return cn35wire.MarshalJSON(trainingError(
					"无法出售", "队长、助战或保护中的同伴无法出售。"))
			case errors.Is(serr, ErrUnitSellMinKeep):
				return cn35wire.MarshalJSON(trainingError(
					"无法出售", "同行同伴数量不能少于5名。"))
			case errors.Is(serr, ErrInvalidRequest):
				return cn35wire.MarshalJSON(trainingError(
					"参数非法", "出售同伴参数无效。"))
			}
			return nil, serr
		}
		return unitOpsSuccess(dep, c, req)
	})
}

// unitEvolutionHandler GET|POST /user_units/evolution?id=<unit_id>（M5 T08）。
func unitEvolutionHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		rawID := firstNonEmpty(req.Query.Get("id"), req.Form.Get("id"), req.Query.Get("unit_id"), req.Form.Get("unit_id"))
		unitID, idErr := strconv.Atoi(rawID)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_units_evolution",
			"unit_id": unitID,
		}
		if dep.UnitOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配同伴写面。"))
		}
		if idErr != nil || unitID <= 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "进化同伴参数无效。"))
		}
		u, uerr := dep.Catalog.Unit(unitID)
		if uerr != nil {
			return cn35wire.MarshalJSON(trainingError(
				"无法进化", "找不到该同伴模板。"))
		}
		evoRule, ok := UnitEvolutionRule(u.ID, u.Element)
		if !ok || evoRule.ToUnitID <= 0 {
			return cn35wire.MarshalJSON(trainingError(
				"无法进化", "该同伴不可进化。"))
		}
		targetU, terr := dep.Catalog.Unit(evoRule.ToUnitID)
		if terr != nil {
			return cn35wire.MarshalJSON(trainingError(
				"无法进化", "进化目标同伴模板缺失。"))
		}
		reqBaseMaxLv := 10*u.Rare + 20
		beforeRec, afterRec, err := dep.UnitOps.EvolveUnit(req.UserID, unitID, evoRule.ToUnitID, reqBaseMaxLv, evoRule.NeedInt)
		if err != nil {
			switch {
			case errors.Is(err, ErrUnitNotOwned):
				return cn35wire.MarshalJSON(trainingError(
					"无法进化", "该同伴不在当前同行库存中。"))
			case errors.Is(err, ErrUnitEvolutionInvalid):
				return cn35wire.MarshalJSON(trainingError(
					"无法进化", fmt.Sprintf("需要达到 Lv.%d 才可进化。", reqBaseMaxLv)))
			case errors.Is(err, ErrUnitEvolutionOwned):
				return cn35wire.MarshalJSON(trainingError(
					"无法进化", "已持有进化后的同伴。"))
			case errors.Is(err, ErrItemInsufficient), errors.Is(err, ErrItemNotFound):
				return cn35wire.MarshalJSON(trainingError(
					"素材不足", "进化所需的素材数量不足。"))
			}
			return nil, err
		}
		beforeDoc, berr := buildUnitDoc(beforeRec, u)
		if berr != nil {
			return nil, berr
		}
		afterDoc, aerr := buildUnitDoc(afterRec, targetU)
		if aerr != nil {
			return nil, aerr
		}
		data, werr := writeBackData(dep, c, req.UserID)
		if werr != nil {
			return nil, werr
		}
		data["evolved_user_unit"] = afterDoc
		data["evolution_before"] = beforeDoc
		data["evolution_after"] = afterDoc
		if sks, has := data["user_skills"]; has {
			data["skills"] = sks
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

func unitAbilityExchangeInfoDoc(dep Deps, userID string) map[string]any {
	stoneCount := 0
	if dep.Items != nil && userID != "" {
		if items, err := dep.Items.PlayerItems(userID); err == nil {
			for _, it := range items {
				if it.ItemKey == "power_stone" {
					stoneCount = it.Quantity
					break
				}
			}
		}
	}
	ruleBody := "1. 仅限两位相同星级的同行同伴进行能力互换；\n2. 互换后双方的等级与觉醒次数对调，潜能保持不变；\n3. 每次互换优先消耗1颗能力互换石，不足时消耗10颗钻石。"
	return map[string]any{
		"desc":              "通过消耗能力互换石（或10颗钻石），可以将两位相同星级同伴的等级与觉醒数互换。",
		"desc_title":        "能力互换说明",
		"desc_body":         ruleBody,
		"rule_title":        "能力互换说明",
		"rule_desc":         ruleBody,
		"item_consume_desc": "消耗：能力互换石 ×1（或 10 钻）",
		"stone_count":       stoneCount,
		"diamond":           10,
	}
}

// unitAbilityExchangeInfoHandler GET|POST /unit_ability_exchange/get_unit_ability_exchange_info（M5 T09）。
func unitAbilityExchangeInfoHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "unit_ability_exchange_info",
		}
		info := unitAbilityExchangeInfoDoc(dep, req.UserID)
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data": map[string]any{
				"changeAbilityInfo":          info,
				"unit_ability_exchange_info": info,
			},
		})
	})
}

// unitAbilityExchangeSelectHandler GET|POST /unit_ability_exchange/select_unit（M5 T09）。
func unitAbilityExchangeSelectHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		rawID := firstNonEmpty(req.Query.Get("unit_id"), req.Form.Get("unit_id"), req.Query.Get("id"), req.Form.Get("id"))
		unitID, _ := strconv.Atoi(rawID)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "unit_ability_exchange_select_unit",
			"unit_id": unitID,
		}
		invData, err := inventoryDataFor(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		allUnits, _ := invData["user_units"].([]any)
		targetRareTypeID := 0
		otherHasAbility := false
		if unitID > 0 && dep.Catalog != nil {
			if u, uerr := dep.Catalog.Unit(unitID); uerr == nil {
				targetRareTypeID = u.Rare * 10
			}
			for _, raw := range allUnits {
				if m, ok := raw.(map[string]any); ok {
					if uid, _ := m["id"].(int); uid == unitID {
						lv, _ := m["level"].(int)
						dup, _ := m["duplicated_count"].(int)
						otherHasAbility = lv > 1 || dup > 0
						break
					}
				}
			}
		}
		filtered := make([]any, 0, len(allUnits))
		for _, raw := range allUnits {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			uid, _ := m["id"].(int)
			if unitID > 0 && uid == unitID {
				continue
			}
			rt, _ := m["rare_type_id"].(int)
			if targetRareTypeID > 0 && rt != targetRareTypeID {
				continue
			}
			lv, _ := m["level"].(int)
			dup, _ := m["duplicated_count"].(int)
			if unitID > 0 && !otherHasAbility && lv <= 1 && dup <= 0 {
				continue
			}
			m["change_ability_enable"] = true
			filtered = append(filtered, m)
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data": map[string]any{
				"changeAbility_units": filtered,
				"select_units":        filtered,
				"user_units":          allUnits,
			},
		})
	})
}

// unitAbilityExchangeResultHandler GET|POST /unit_ability_exchange/get_unit_ability_exchange_result（M5 T09）。
func unitAbilityExchangeResultHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		unitAID, errA := strconv.Atoi(firstNonEmpty(req.Query.Get("unit_a_id"), req.Form.Get("unit_a_id")))
		unitBID, errB := strconv.Atoi(firstNonEmpty(req.Query.Get("unit_b_id"), req.Form.Get("unit_b_id")))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "unit_ability_exchange_result",
			"unit_a_id": unitAID, "unit_b_id": unitBID,
		}
		if dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配同伴目录。"))
		}
		if errA != nil || errB != nil || unitAID <= 0 || unitBID <= 0 || unitAID == unitBID {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "请选择两位不同的同星级同伴。"))
		}
		uA, cerrA := dep.Catalog.Unit(unitAID)
		uB, cerrB := dep.Catalog.Unit(unitBID)
		if cerrA != nil || cerrB != nil || uA.Rare != uB.Rare {
			return cn35wire.MarshalJSON(trainingError(
				"无法互换", "仅限相同星级的同伴进行能力互换。"))
		}
		urecs, err := dep.Inventory.Units(req.UserID)
		if err != nil {
			return nil, err
		}
		var recA, recB *UnitRecord
		for i := range urecs {
			if urecs[i].Waiting {
				continue
			}
			if urecs[i].UnitID == unitAID {
				recA = &urecs[i]
			} else if urecs[i].UnitID == unitBID {
				recB = &urecs[i]
			}
		}
		if recA == nil || recB == nil {
			return cn35wire.MarshalJSON(trainingError(
				"无法互换", "所选同伴不在当前同行列表中。"))
		}
		if recA.Level == recB.Level && recA.DuplicatedCount == recB.DuplicatedCount {
			return cn35wire.MarshalJSON(trainingError(
				"无法互换", "两位同伴的等级与觉醒数完全相同，无需互换。"))
		}
		beforeDocA, err := buildUnitDoc(*recA, uA)
		if err != nil {
			return nil, err
		}
		beforeDocB, err := buildUnitDoc(*recB, uB)
		if err != nil {
			return nil, err
		}
		afterRecA := *recA
		afterRecB := *recB
		afterRecA.Level, afterRecB.Level = recB.Level, recA.Level
		afterRecA.DuplicatedCount, afterRecB.DuplicatedCount = recB.DuplicatedCount, recA.DuplicatedCount
		afterDocA, err := buildUnitDoc(afterRecA, uA)
		if err != nil {
			return nil, err
		}
		afterDocB, err := buildUnitDoc(afterRecB, uB)
		if err != nil {
			return nil, err
		}
		info := unitAbilityExchangeInfoDoc(dep, req.UserID)
		consumeDesc := "消耗：钻石 ×10"
		if sc, ok := info["stone_count"].(int); ok && sc > 0 {
			consumeDesc = "消耗：能力互换石 ×1"
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data": map[string]any{
				"unit_ability_exchange_result": map[string]any{
					"consume_desc":  consumeDesc,
					"desc":          "确认将以下两位同伴的等级与觉醒数进行互换吗？",
					"unit_a":        afterDocA,
					"unit_b":        afterDocB,
					"before_unit_a": beforeDocA,
					"before_unit_b": beforeDocB,
				},
			},
		})
	})
}

// unitAbilityExchangeExecHandler GET|POST /unit_ability_exchange/exchange_unit_ability（M5 T09）。
func unitAbilityExchangeExecHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		unitAID, errA := strconv.Atoi(firstNonEmpty(req.Query.Get("unit_a_id"), req.Form.Get("unit_a_id")))
		unitBID, errB := strconv.Atoi(firstNonEmpty(req.Query.Get("unit_b_id"), req.Form.Get("unit_b_id")))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "unit_ability_exchange_exec",
			"unit_a_id": unitAID, "unit_b_id": unitBID,
		}
		if dep.UnitOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配同伴写面。"))
		}
		if errA != nil || errB != nil || unitAID <= 0 || unitBID <= 0 || unitAID == unitBID {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "请选择两位不同的同星级同伴。"))
		}
		uA, cerrA := dep.Catalog.Unit(unitAID)
		uB, cerrB := dep.Catalog.Unit(unitBID)
		if cerrA != nil || cerrB != nil || uA.Rare != uB.Rare {
			return cn35wire.MarshalJSON(trainingError(
				"无法互换", "仅限相同星级的同伴进行能力互换。"))
		}
		recA, recB, err := dep.UnitOps.ExchangeUnitAbility(req.UserID, unitAID, unitBID)
		if err != nil {
			switch {
			case errors.Is(err, ErrUnitNotOwned):
				return cn35wire.MarshalJSON(trainingError(
					"无法互换", "所选同伴不在当前同行列表中。"))
			case errors.Is(err, ErrUnitExchangeInvalid):
				return cn35wire.MarshalJSON(trainingError(
					"无法互换", "两位同伴的等级与觉醒数完全相同，无需互换。"))
			case errors.Is(err, ErrCurrencyInsufficient):
				return cn35wire.MarshalJSON(trainingError(
					"余额不足", "能力互换石或钻石不足，无法完成互换。"))
			}
			return nil, err
		}
		docA, err := buildUnitDoc(recA, uA)
		if err != nil {
			return nil, err
		}
		docB, err := buildUnitDoc(recB, uB)
		if err != nil {
			return nil, err
		}
		data, err := writeBackData(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		info := unitAbilityExchangeInfoDoc(dep, req.UserID)
		data["unit_ability_exchange_succ"] = true
		data["unit_ability_exchange_modal"] = map[string]any{
			"title": "能力互换",
			"body":  "同伴能力互换成功！",
		}
		data["unit_ability_exchange_units"] = []any{docA, docB}
		data["changeAbilityInfo"] = info
		data["unit_ability_exchange_info"] = info
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}
