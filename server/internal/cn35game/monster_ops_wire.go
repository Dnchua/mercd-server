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
			case errors.Is(err, ErrCurrencyInsufficient):
				return cn35wire.MarshalJSON(trainingError(
					"金币不足", "持有的金币不足以完成强化。"))
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
// 魔宠融合选择页静态说明信息（对齐 mlk 抓包 user_monsters_evolve_select_info.json）。
func monsterEvolveSelectInfoHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_evolve_select_info",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"user_monsters_evolve_select_info": map[string]any{
					"title":             "魔宠融合",
					"help_title":        "融合说明",
					"help_body":         "※只有完全体魔宠可以用来融合\n※消耗的完全体魔宠数量越多，融合出的蛋星级越高\n※蛋的星级越高，融合出高级宠的概率越高",
					"step1_title":       "Step1",
					"step1_asset_name":  "common_icon/evolve_fancy_egg",
					"step1_desc":        "消耗同属性1★完全体魔宠融合成蛋",
					"step2_title":       "Step2",
					"step2_asset_name":  "common_icon/evolve_fancy_monster",
					"step2_desc":        "将特定魔宠与蛋融合，有几率获得更高级的魔宠",
					"egg_btn_label":     "融合蛋",
					"monster_btn_label": "选择魔宠",
				},
			},
		})
	})
}

// rareMonsterCategoryConfigDocs 返回 6 档融合宠强化体型与单步消耗配置（对齐 MonsterPowerupModal.cs:61-65 与 ScaleCategory.cs:121-158）。
func rareMonsterCategoryConfigDocs() []any {
	return []any{
		map[string]any{"id": 1, "name": "成长期", "item_id": 1, "min_scale": "1.30", "max_scale": "1.54", "gold": 1000, "item": 2, "gacha_points": 1000, "powerup_item_cnt": 2},
		map[string]any{"id": 2, "name": "成熟期", "item_id": 1, "min_scale": "1.55", "max_scale": "1.69", "gold": 2000, "item": 2, "gacha_points": 2000, "powerup_item_cnt": 2},
		map[string]any{"id": 3, "name": "完全体", "item_id": 1, "min_scale": "1.70", "max_scale": "1.79", "gold": 5000, "item": 2, "gacha_points": 5000, "powerup_item_cnt": 2},
		map[string]any{"id": 4, "name": "完全体", "item_id": 1, "min_scale": "1.80", "max_scale": "1.89", "gold": 10000, "item": 3, "gacha_points": 10000, "powerup_item_cnt": 3},
		map[string]any{"id": 5, "name": "完全体", "item_id": 1, "min_scale": "1.90", "max_scale": "2.09", "gold": 20000, "item": 4, "gacha_points": 20000, "powerup_item_cnt": 4},
		map[string]any{"id": 6, "name": "完全体", "item_id": 1, "min_scale": "2.10", "max_scale": "2.30", "gold": 50000, "item": 5, "gacha_points": 50000, "powerup_item_cnt": 5},
	}
}

// rareMonsterCategoryConfigHandler GET|POST /user_monsters/rare_monster_category_config
func rareMonsterCategoryConfigHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_rare_monster_category_config",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"rare_monster_category_config": rareMonsterCategoryConfigDocs(),
			},
		})
	})
}

// monsterSellHandler GET|POST /user_monsters/sell?uuids[]=<uuid>
func monsterSellHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuids := collectUUIDs(req)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_sell",
			"uuids": uuids,
		}
		if dep.MonsterOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配魔宠写面。"))
		}
		if len(uuids) == 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "未指定要出售的魔宠。"))
		}
		mrecs, err := dep.Inventory.Monsters(req.UserID)
		if err != nil {
			return nil, err
		}
		prices := make(map[string]int64, len(mrecs))
		for _, r := range mrecs {
			if m, merr := dep.Catalog.Monster(r.MonsterID); merr == nil {
				prices[r.UUID] = int64(m.Rare * 100)
			}
		}
		if _, err := dep.MonsterOps.SellMonsters(req.UserID, uuids, prices); err != nil {
			switch {
			case errors.Is(err, ErrMonsterNotOwned):
				return cn35wire.MarshalJSON(trainingError(
					"无法出售", "所选魔宠不在当前账号库存中。"))
			case errors.Is(err, ErrMonsterSellProtected):
				return cn35wire.MarshalJSON(trainingError(
					"无法出售", "已保护、已装备或处于防卫中的魔宠不能出售。"))
			case errors.Is(err, ErrInvalidRequest):
				return cn35wire.MarshalJSON(trainingError(
					"参数非法", "魔宠出售参数无效。"))
			}
			return nil, err
		}
		return unitOpsSuccess(dep, c, req)
	})
}

func buildEggEvoConfigDoc(dep Deps, userID string) (map[string]any, error) {
	var gold int64
	evolveItem1Count := int64(0)
	if dep.Accounts != nil {
		if snap, err := dep.Accounts.Snapshot(userID); err == nil && snap != nil {
			if g, ok := snap.User["gacha_point"].(int64); ok {
				gold = g
			}
			if ei, ok := snap.User["evolve_items"].(map[string]any); ok {
				if c, ok := ei["1"].(int64); ok {
					evolveItem1Count = c
				}
			}
		}
	}
	fullScaleByAttr := map[int]int{1: 0, 2: 0, 3: 0, 4: 0, 5: 0}
	protectedFullByAttr := map[int]int{1: 0, 2: 0, 3: 0, 4: 0, 5: 0}
	if dep.Inventory != nil && dep.Catalog != nil {
		if mrecs, err := dep.Inventory.Monsters(userID); err == nil {
			for _, r := range mrecs {
				m, merr := dep.Catalog.Monster(r.MonsterID)
				if merr != nil || m.Rare != 1 {
					continue
				}
				_, progScale, serr := effectiveScale(r.RealScale, r.Scale)
				if serr != nil || progScale < 1.6995 {
					continue
				}
				if r.UnitID == nil && !r.Protected {
					fullScaleByAttr[m.Element]++
				} else {
					protectedFullByAttr[m.Element]++
				}
			}
		}
	}
	tabNames := map[int]string{1: "火", 2: "水", 3: "风", 4: "光", 5: "暗"}
	cfg := map[string]any{
		"gold": gold,
	}
	tiers := []struct {
		cnt     int
		crystal int
		coin    int
	}{
		{1, 1, 5000},
		{3, 2, 15000},
		{5, 3, 25000},
		{10, 5, 50000},
	}
	for idx := 0; idx < 5; idx++ {
		attrID := idx + 1
		evoList := make([]any, 0, 4)
		for tIdx, t := range tiers {
			eggID := attrID*100 + (tIdx + 1)
			evoList = append(evoList, map[string]any{
				"monster_count":               t.cnt,
				"consume_monster_count":       t.cnt,
				"evolve_item_count":           evolveItem1Count,
				"evolve_item_require_count":   t.crystal,
				"consume_coin":                t.coin,
				"consume_gold":                t.coin,
				"evolve_item_asset_name":      "common_icon/evolve_item_1",
				"egg_asset_name":              fmt.Sprintf("common_icon/evolve_fancy_item_%d", eggID),
				"btn_icon_asset_name":         fmt.Sprintf("common_icon/evolve_fancy_item_%d", eggID),
				"btn_disable_icon_asset_name": fmt.Sprintf("common_icon/evolve_fancy_item_%d", eggID),
			})
		}
		cfg[strconv.Itoa(idx)] = map[string]any{
			"name":                     tabNames[attrID],
			"full_scale_monster_count": fullScaleByAttr[attrID],
			"desc1":                    fmt.Sprintf("可用1★完全体魔宠：%d只", fullScaleByAttr[attrID]),
			"desc2":                    fmt.Sprintf("保护/装备中：%d只", protectedFullByAttr[attrID]),
			"desc3":                    fmt.Sprintf("持有金币：%d", gold),
			"desc4":                    fmt.Sprintf("持有神秘水晶：%d", evolveItem1Count),
			"evo_config":               evoList,
		}
	}
	return cfg, nil
}

// monsterShowEvolveBoardHandler GET|POST /user_monsters/show_evolve_board
func monsterShowEvolveBoardHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_show_evolve_board",
		}
		eggCfg, err := buildEggEvoConfigDoc(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"show_monster_evo": map[string]any{
					"egg_evo_config": eggCfg,
				},
			},
		})
	})
}

func evolveFancyEggDoc(eggID int) map[string]any {
	attrID := eggID / 100
	star := eggID % 100
	attrName := map[int]string{1: "火", 2: "水", 3: "风", 4: "光", 5: "暗"}[attrID]
	prefix := map[int]string{1: "普通的", 2: "稀有的", 3: "超稀有的", 4: "究极的"}[star]
	stars := strings.Repeat("★", star)
	return map[string]any{
		"id":              eggID,
		"name":            fmt.Sprintf("%s%s之蛋", prefix, attrName),
		"code":            "evolve_fancy_item",
		"star":            star,
		"rare_icon":       stars,
		"rare_type_id":    star * 10,
		"attr_id":         attrID,
		"item_type":       4,
		"content_type_id": 106,
		"icon_asset_name": fmt.Sprintf("common_icon/evolve_fancy_item_%d", eggID),
		"desc":            "蕴含融合魔宠进化之力的神秘之卵。",
	}
}

func fusionMonsterTarget(attrID, tier int) (int, string, string) {
	if tier < 1 {
		tier = 1
	} else if tier > 4 {
		tier = 4
	}
	var baseID int
	var name string
	switch attrID {
	case 1:
		baseID, name = 100037, "黛尼斯托"
	case 2:
		baseID, name = 100004, "摩布拉"
	case 3:
		baseID, name = 100008, "赫黎安提斯"
	case 4:
		baseID, name = 100041, "梅赫莲恩"
	default:
		baseID, name = 100017, "洛菲阿斯"
	}
	targetID := baseID + tier
	limitF := DefaultFusionMonsterScaleLimit(targetID)
	return targetID, name, fmt.Sprintf("%.3f", limitF)
}

// monsterEvolveEggHandler GET|POST /user_monsters/evolve_egg?attr_id=<1..5>&consume_monster_count=<1|3|5|10>
func monsterEvolveEggHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		attrRaw := firstNonEmpty(req.Query.Get("attr_id"), req.Form.Get("attr_id"))
		cntRaw := firstNonEmpty(req.Query.Get("consume_monster_count"), req.Form.Get("consume_monster_count"))
		attrID, attrErr := strconv.Atoi(attrRaw)
		cnt, cntErr := strconv.Atoi(cntRaw)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_evolve_egg",
			"attr_id": attrID, "consume_monster_count": cnt,
		}
		if dep.MonsterOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配魔宠写面。"))
		}
		if attrErr != nil || attrID < 1 || attrID > 5 || cntErr != nil ||
			(cnt != 1 && cnt != 3 && cnt != 5 && cnt != 10) {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "进化之卵融合参数无效。"))
		}
		eligibleIDs := map[int]bool{}
		mrecs, merr := dep.Inventory.Monsters(req.UserID)
		if merr != nil {
			return nil, merr
		}
		for _, r := range mrecs {
			if m, cerr := dep.Catalog.Monster(r.MonsterID); cerr == nil && m.Rare == 1 && m.Element == attrID {
				eligibleIDs[r.MonsterID] = true
			}
		}
		eggID, _, err := dep.MonsterOps.EvolveEgg(req.UserID, attrID, cnt, eligibleIDs)
		if err != nil {
			switch {
			case errors.Is(err, ErrMonsterEvoInvalid):
				return cn35wire.MarshalJSON(trainingError(
					"无法融合", "符合条件的未保护 1★ 完全体魔宠数量不足。"))
			case errors.Is(err, ErrItemInsufficient):
				return cn35wire.MarshalJSON(trainingError(
					"素材不足", "持有的神秘水晶不足以完成进化之卵融合。"))
			case errors.Is(err, ErrCurrencyInsufficient):
				return cn35wire.MarshalJSON(trainingError(
					"金币不足", "持有的金币不足以完成进化之卵融合。"))
			}
			return nil, err
		}
		data, err := writeBackData(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		eggDoc := evolveFancyEggDoc(eggID)
		data["evolve_fancy_item"] = eggDoc
		eggCfg, err := buildEggEvoConfigDoc(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		data["show_monster_evo"] = map[string]any{
			"egg_evo_config": eggCfg,
			"evo_egg": map[string]any{
				"is_success":     true,
				"egg_id":         eggID,
				"egg_asset_name": fmt.Sprintf("common_icon/evolve_fancy_item_%d", eggID),
				"title":          "融合成功",
				"desc":           fmt.Sprintf("获得了%s！", eggDoc["name"]),
			},
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// monsterEvolveMonsterSelectInfoHandler GET|POST /user_monsters/evolve_monster_select_info?uuid=<uuid>
func monsterEvolveMonsterSelectInfoHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuid := firstNonEmpty(req.Query.Get("uuid"), req.Form.Get("uuid"))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_evolve_monster_select_info",
			"uuid": uuid,
		}
		if dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配魔宠目录。"))
		}
		if uuid == "" {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "未指定主魔宠。"))
		}
		mrecs, err := dep.Inventory.Monsters(req.UserID)
		if err != nil {
			return nil, err
		}
		var target *MonsterRecord
		for i := range mrecs {
			if mrecs[i].UUID == uuid {
				target = &mrecs[i]
				break
			}
		}
		if target == nil {
			return cn35wire.MarshalJSON(trainingError(
				"无法融合", "所选主魔宠不在当前账号库存中。"))
		}
		m, cerr := dep.Catalog.Monster(target.MonsterID)
		if cerr != nil {
			return nil, cerr
		}
		attrID := m.Element
		if attrID < 1 || attrID > 5 {
			attrID = 1
		}
		_, displayScaleF, _ := effectiveScale(target.Scale, target.RealScale)
		catStr := monsterScaleCategoryStr(displayScaleF)
		baseGold := 2000
		switch catStr {
		case "成长期":
			baseGold = 5000
		case "成熟期", "完全体":
			baseGold = 10000
		}

		userEvoEgg := map[string]any{}
		userEvoItem := map[string]any{"1": int64(0)}
		var gold int64
		if dep.Accounts != nil {
			if snap, serr := dep.Accounts.Snapshot(req.UserID); serr == nil && snap != nil {
				if g, ok := snap.User["gacha_point"].(int64); ok {
					gold = g
				}
				if ef, ok := snap.User["evolve_fancy_items"].(map[string]any); ok {
					for k, v := range ef {
						userEvoEgg[k] = v
					}
				}
				if ei, ok := snap.User["evolve_items"].(map[string]any); ok {
					for k, v := range ei {
						userEvoItem[k] = v
					}
				}
			}
		}

		requireEggArr := make([]any, 0, 4)
		eggFancyList := make([]any, 0, 4)
		requiresList := []any{
			map[string]any{"type_id": 4, "content_id": 0, "count": baseGold},
		}
		for star := 1; star <= 4; star++ {
			eggID := attrID*100 + star
			if _, has := userEvoEgg[strconv.Itoa(eggID)]; !has {
				userEvoEgg[strconv.Itoa(eggID)] = int64(0)
			}
			doc := evolveFancyEggDoc(eggID)
			targetID, targetName, _ := fusionMonsterTarget(attrID, star)
			doc["target_image"] = fmt.Sprintf("monster/monster_square_%d", targetID)
			doc["best_monster_rate"] = map[string]any{
				"monster_id":         targetID,
				"monster_name":       targetName,
				"rate":               star * 25,
				"min_fusion_degrees": star * 3,
			}
			requireEggArr = append(requireEggArr, doc)
			eggFancyList = append(eggFancyList, map[string]any{
				"star": star,
				"id":   eggID,
				"name": doc["name"],
			})
			requiresList = append(requiresList, map[string]any{
				"type_id":    106,
				"content_id": eggID,
				"count":      1,
			})
		}
		firstTargetID, _, _ := fusionMonsterTarget(attrID, 1)
		sizeReq := 1.70
		if MonsterBatch(target.MonsterID) >= 0 {
			sizeReq = 1.55
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"show_monster_evo": map[string]any{
					"use_egg":         true,
					"gold":            gold,
					"user_evo_egg":    userEvoEgg,
					"user_evo_item":   userEvoItem,
					"require_egg_arr": requireEggArr,
					"formulas_hash": map[string]any{
						"monster_size_require":  sizeReq,
						"monster_cnt_max_limit": 5,
						"desc":                  "将魔宠与进化之卵融合，获得强力的融合魔宠并提升体型上限。",
						"target_image":          strconv.Itoa(firstTargetID),
						"requires":              requiresList,
						"target": []any{
							map[string]any{"type_id": 7, "content_id": firstTargetID, "count": 1},
						},
					},
				},
				"evolve_monster_select_info": map[string]any{
					"base_info": map[string]any{
						"desc":           "※参与融合的蛋星级越高，进化成更高级魔宠的概率越大",
						"consume_gold":   baseGold,
						"egg_fancy_list": eggFancyList,
					},
					"fusion_info": map[string]any{
						"desc":           "※选择同类完全体魔宠一起融合，可提升进化概率",
						"consume_gold":   5000,
						"egg_fancy_list": eggFancyList,
					},
				},
			},
		})
	})
}

// monsterEvolveHandler GET|POST /user_monsters/evolve?uuids=<uuid1|uuid2|...>&egg_item_id=<egg_id>
func monsterEvolveHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		rawUUIDs := collectUUIDs(req)
		var uuids []string
		for _, item := range rawUUIDs {
			for _, part := range strings.Split(item, "|") {
				part = strings.TrimSpace(part)
				if part != "" {
					uuids = append(uuids, part)
				}
			}
		}
		eggRaw := firstNonEmpty(req.Query.Get("egg_item_id"), req.Form.Get("egg_item_id"))
		eggID, eggErr := strconv.Atoi(eggRaw)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_monsters_evolve",
			"uuids": uuids, "egg_item_id": eggID,
		}
		if dep.MonsterOps == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配魔宠写面。"))
		}
		if len(uuids) == 0 || eggErr != nil || eggID <= 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "魔宠融合参数无效。"))
		}
		mrecs, merr := dep.Inventory.Monsters(req.UserID)
		if merr != nil {
			return nil, merr
		}
		var mainRec *MonsterRecord
		for i := range mrecs {
			if mrecs[i].UUID == uuids[0] {
				mainRec = &mrecs[i]
				break
			}
		}
		if mainRec == nil {
			return cn35wire.MarshalJSON(trainingError(
				"无法融合", "所选魔宠不在当前账号库存中。"))
		}
		if MonsterFormulaID(mainRec.MonsterID) == 0 {
			return cn35wire.MarshalJSON(trainingError(
				"无法融合", "所选魔宠不可参与融合。"))
		}
		m, cerr := dep.Catalog.Monster(mainRec.MonsterID)
		if cerr != nil {
			return nil, cerr
		}
		attrID := eggID / 100
		tier := eggID % 100
		if attrID < 1 || attrID > 5 || tier < 1 || tier > 4 || m.Element != attrID {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "进化之卵属性或星级与主魔宠不匹配。"))
		}
		targetMonsterID, targetName, targetScaleLimit := fusionMonsterTarget(attrID, tier)
		consumeGold := int64(len(uuids) * 10000)
		addFusion := tier * 3

		created, err := dep.MonsterOps.EvolveMonster(
			req.UserID, uuids[0], uuids[1:], eggID,
			targetMonsterID, targetScaleLimit, consumeGold, addFusion,
		)
		if err != nil {
			switch {
			case errors.Is(err, ErrMonsterNotOwned):
				return cn35wire.MarshalJSON(trainingError(
					"无法融合", "所选魔宠不在当前账号库存中。"))
			case errors.Is(err, ErrMonsterSellProtected), errors.Is(err, ErrMonsterEvoInvalid):
				return cn35wire.MarshalJSON(trainingError(
					"无法融合", "所选魔宠不满足融合条件或处于保护/装备状态。"))
			case errors.Is(err, ErrItemInsufficient):
				return cn35wire.MarshalJSON(trainingError(
					"素材不足", "持有的进化之卵数量不足。"))
			case errors.Is(err, ErrCurrencyInsufficient):
				return cn35wire.MarshalJSON(trainingError(
					"金币不足", "持有的金币不足以完成魔宠融合。"))
			}
			return nil, err
		}
		data, err := writeBackData(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		var evolveMonsterDoc any
		if mons, ok := data["user_monsters"].([]any); ok {
			for _, mon := range mons {
				if mMap, ok := mon.(map[string]any); ok {
					if fmt.Sprint(mMap["uuid"]) == created.UUID {
						evolveMonsterDoc = mMap
						break
					}
				}
			}
		}
		limitF, _ := strconv.ParseFloat(targetScaleLimit, 64)
		data["evolve_monster"] = evolveMonsterDoc
		data["is_success"] = true
		data["show_monster_evo"] = map[string]any{
			"evo_monster": map[string]any{
				"is_success":     true,
				"uuid":           created.UUID,
				"monster_id":     targetMonsterID,
				"scale_limit":    limitF,
				"is_up":          true,
				"fusion_degrees": created.FusionDegrees,
				"title":          "融合成功",
				"desc":           fmt.Sprintf("成功融合为 %s！", targetName),
			},
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
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
