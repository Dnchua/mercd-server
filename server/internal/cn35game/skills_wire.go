// skills_wire.go RUNE350-01：符文读面 /user_skills/ 与写面端点
// （set_all/off/sell/powerup/composite/protect）。
//
// 客户端契约（ilspy-cs 取证）：
//
//	GET|POST /user_skills/                  requireData.user_skills 列表
//	GET|POST /user_skills/set_all?unit_id=<id>&uuids[]=<uuid>...
//	    （SkillWithUnitListComponent.cs:270 / SkillWithUnitModal.cs:61，
//	    整体替换该同伴槽位集）
//	GET|POST /user_skills/off?uuid=<uuid>   单枚卸下
//	    （SkillButtonsListComponent 装备页 off 按钮）
//	GET|POST /user_skills/off?all=true&unit_id=<id>  全卸下
//	    （UserUnitDetail.cs:648）
//	GET|POST /user_skills/sell?uuids[]=<uuid>...
//	    （SkillButtonsListComponent.cs:671，入账 user.gacha_point）
//	GET|POST /user_skills/powerup?uuid=&powerup_item_id=&count=
//	    （SkillPowerupModal.cs:138）
//	GET|POST /user_skills/composite?uuid=&seed_uuid=
//	    （SkillPowerupModal.cs:155）
//	GET|POST /user_skills/protect?uuids[]=<uuid>&protected=<true|false>
//	    （row_config protect_icon.OnClick → protect_url + confirm_text）
//
// 行文档键（SkillButtonsListComponent/row_config 取证）：uuid、id、
// skill_id、name、description、level、base_level、type_id、category、
// price、price_str、sub_str、icon_asset_name、unit_icon_asset_name、
// protect_icon_name、confirm_text、protect_url、protected、unit_id
// （存在即已装备）、equipable、equipable_weapon_id（限定符文）、
// powerupable（未达强化上限）。
//
// id 语义（i() 只解 JSON NUMBER）：客户端把它当符文种类 id 用——
// SkillWithUnitListComponent 同名判定 i("id") 相等即视为同名符文、
// 符文孔贴图取 "skill/skill_"+i("id")，故 id 必须下发 skill_id
// 数值而非 uuid（uuid 字符串使 i() 恒 0：全部符文互相判同名、
// 贴图落到不存在的 skill_0）。实例标识走 uuid 键。
package cn35game

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// skillRowDoc 符文行文档。unit_id 发字符串（客户端 s("unit_id")=="" 判
// 未装备；装备中发十进制串）；level 必须 JSON NUMBER（Document.dec）。
// icon_asset_name 是 SetTexture 路径（UIUtil.IconAssetName "skill" →
// skill/skill_<id>）；unit_icon_asset_name 同（"unit" → unit_square）。
// row_config 无品质 label 挂件，品质并入 sub_str（品质筛选器仍读
// category 键）。
func skillRowDoc(sk PlayerSkill, unitIcons map[int]string) map[string]any {
	def, known := SkillCatalog[sk.SkillID]
	category := SkillCategoryName(sk.Level)
	doc := map[string]any{
		"uuid":            sk.UUID,
		"id":              sk.SkillID,
		"skill_id":        sk.SkillID,
		"level":           sk.Level,
		"base_level":      sk.BaseLevel,
		"price":           SkillSellPrice(sk.Level),
		"price_str":       fmt.Sprintf("%d", SkillSellPrice(sk.Level)),
		"sub_str":         fmt.Sprintf("魔力 %.3f/%s", sk.Level, category),
		"protected":       sk.Protected,
		"category":        category,
		"powerupable":     sk.Level < SkillMaxLevel,
		"icon_asset_name": fmt.Sprintf("skill/skill_%d", sk.SkillID),
	}
	if known {
		doc["name"] = def.Name
		doc["description"] = def.Desc
		doc["type_id"] = def.TypeID
		doc["equipable"] = def.Equipable
		if def.WeaponID != 0 {
			doc["equipable_weapon_id"] = def.WeaponID
		}
	} else {
		doc["name"] = fmt.Sprintf("符文%d", sk.SkillID)
		doc["description"] = ""
		doc["type_id"] = 5
		doc["equipable"] = true
	}
	if sk.Protected {
		doc["protect_icon_name"] = "icon_protect"
		doc["confirm_text"] = "要解除该符文的保护吗？"
	} else {
		doc["protect_icon_name"] = ""
		doc["confirm_text"] = "要保护该符文吗？保护后将无法出售或作为强化素材。"
	}
	doc["protect_url"] = fmt.Sprintf("/user_skills/protect?uuids[]=%s&protected=%v",
		sk.UUID, !sk.Protected)
	if sk.UnitID != nil {
		doc["unit_id"] = *sk.UnitID
		doc["unit_icon_asset_name"] = unitIcons[*sk.UnitID]
	}
	return doc
}

// userSkillsData 符文读面投影；装备中符文附宿主单位图标
// （unit_icon widget）。
func userSkillsData(dep Deps, userID string) (map[string]any, error) {
	skills, err := dep.Skills.PlayerSkills(userID)
	if err != nil {
		return nil, fmt.Errorf("cn35 符文库存读取失败: %w", err)
	}
	unitIcons := map[int]string{}
	if dep.Inventory != nil && dep.Catalog != nil {
		if recs, rerr := dep.Inventory.Units(userID); rerr == nil {
			for _, r := range recs {
				unitIcons[r.UnitID] = fmt.Sprintf("unit/unit_square_%d", r.UnitID)
			}
		}
	}
	list := []any{}
	for _, sk := range skills {
		list = append(list, skillRowDoc(sk, unitIcons))
	}
	return map[string]any{"user_skills": list}, nil
}

// skillWriteBackData 写面成功响应：最新符文列表 + 库存 + 账号快照
// （出售后 gacha_point 变化经 user 段回推）。
func skillWriteBackData(dep Deps, c *httpapi.Ctx, userID string) (map[string]any, error) {
	data, err := writeBackData(dep, c, userID)
	if err != nil {
		return nil, err
	}
	skData, err := userSkillsData(dep, userID)
	if err != nil {
		return nil, err
	}
	for k, v := range skData {
		data[k] = v
	}
	return data, nil
}

func skillSuccess(dep Deps, c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
	data, err := skillWriteBackData(dep, c, req.UserID)
	if err != nil {
		return nil, err
	}
	return cn35wire.MarshalJSON(map[string]any{"status": "success", "data": data})
}

// skillsByUnitFor 读账号符文库存并按装备宿主分组（仅装备中的符文）。
// dep.Skills 未装配 → 空表，不报错（写面端点单独做装配检查）。
func skillsByUnitFor(dep Deps, userID string) (map[int][]PlayerSkill, error) {
	byUnit := map[int][]PlayerSkill{}
	if dep.Skills == nil {
		return byUnit, nil
	}
	skills, err := dep.Skills.PlayerSkills(userID)
	if err != nil {
		return nil, fmt.Errorf("cn35 符文库存读取失败: %w", err)
	}
	for _, sk := range skills {
		if sk.UnitID != nil {
			byUnit[*sk.UnitID] = append(byUnit[*sk.UnitID], sk)
		}
	}
	return byUnit, nil
}

func userSkillsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		data, err := userSkillsData(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_skills",
			"skills": len(data["user_skills"].([]any)),
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// skillSetAllHandler /user_skills/set_all：整体替换目标同伴符文槽位。
// 槽位上限与武器种由 Inventory+Catalog 解析（模板不在 DB）。
func skillSetAllHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		unitID, idErr := strconv.Atoi(firstNonEmpty(
			req.Query.Get("unit_id"), req.Form.Get("unit_id")))
		uuids := collectUUIDs(req)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_skills_set_all",
			"unit_id": unitID, "uuids": uuids,
		}
		if dep.Skills == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配符文写面。"))
		}
		if idErr != nil || unitID <= 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "同伴参数无效，请重新打开同伴界面。"))
		}
		rec, found, err := findUnitRecord(dep, req.UserID, unitID)
		if err != nil {
			return nil, err
		}
		if !found {
			return cn35wire.MarshalJSON(trainingError(
				"无法装备", "该同伴不在当前账号库存中。"))
		}
		u, uerr := dep.Catalog.Unit(unitID)
		if uerr != nil {
			return nil, fmt.Errorf("cn35 单位模板缺失 unit_id=%d: %w", unitID, uerr)
		}
		err = dep.Skills.SkillSetAll(req.UserID, unitID, u.Weapon,
			SkillSlotCap(u.Rare, rec.DuplicatedCount), uuids)
		if err != nil {
			return cn35wire.MarshalJSON(skillError(err))
		}
		return skillSuccess(dep, c, req)
	})
}

func skillOffHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		if dep.Skills == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配符文写面。"))
		}
		if firstNonEmpty(req.Query.Get("all"), req.Form.Get("all")) == "true" {
			unitID, idErr := strconv.Atoi(firstNonEmpty(
				req.Query.Get("unit_id"), req.Form.Get("unit_id")))
			c.Decoded = map[string]any{
				"cn35": true, "uid": req.UserID, "endpoint": "user_skills_off_all",
				"unit_id": unitID,
			}
			if idErr != nil || unitID <= 0 {
				return cn35wire.MarshalJSON(trainingError(
					"参数非法", "同伴参数无效。"))
			}
			if err := dep.Skills.SkillOffAll(req.UserID, unitID); err != nil {
				return cn35wire.MarshalJSON(skillError(err))
			}
			return skillSuccess(dep, c, req)
		}
		uuid := firstNonEmpty(req.Query.Get("uuid"), req.Form.Get("uuid"))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_skills_off",
			"uuid": uuid,
		}
		if uuid == "" {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "符文参数无效。"))
		}
		if err := dep.Skills.SkillOff(req.UserID, uuid); err != nil {
			return cn35wire.MarshalJSON(skillError(err))
		}
		return skillSuccess(dep, c, req)
	})
}

func skillSellHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuids := collectUUIDs(req)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_skills_sell",
			"uuids": uuids,
		}
		if dep.Skills == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配符文写面。"))
		}
		if len(uuids) == 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "未选择要出售的符文。"))
		}
		total, _, err := dep.Skills.SkillSell(req.UserID, uuids)
		if err != nil {
			return cn35wire.MarshalJSON(skillError(err))
		}
		c.Decoded["sell_total"] = total
		return skillSuccess(dep, c, req)
	})
}

func skillProtectHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuids := collectUUIDs(req)
		rawProt := firstNonEmpty(req.Query.Get("protected"), req.Form.Get("protected"))
		protected := rawProt == "true"
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_skills_protect",
			"uuids": uuids, "protected": rawProt,
		}
		if dep.Skills == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配符文写面。"))
		}
		if len(uuids) == 0 || rawProt == "" {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "符文保护参数无效。"))
		}
		if err := dep.Skills.SkillProtect(req.UserID, uuids, protected); err != nil {
			return cn35wire.MarshalJSON(skillError(err))
		}
		return skillSuccess(dep, c, req)
	})
}

func skillPowerupHandler(dep Deps) httpapi.Handler {
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
			"cn35": true, "uid": req.UserID, "endpoint": "user_skills_powerup",
			"uuid": uuid, "powerup_item_id": itemID, "count": count,
		}
		if dep.Skills == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配符文写面。"))
		}
		if uuid == "" || itemErr != nil || itemID <= 0 ||
			cntErr != nil || count <= 0 || count > 999 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "强化参数无效，请重新打开符文界面。"))
		}
		newLevel, err := dep.Skills.SkillPowerup(req.UserID, uuid, itemID, count)
		if err != nil {
			return cn35wire.MarshalJSON(skillError(err))
		}
		c.Decoded["level"] = newLevel
		return skillSuccess(dep, c, req)
	})
}

func skillCompositeHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuid := firstNonEmpty(req.Query.Get("uuid"), req.Form.Get("uuid"))
		seedUUID := firstNonEmpty(req.Query.Get("seed_uuid"), req.Form.Get("seed_uuid"))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_skills_composite",
			"uuid": uuid, "seed_uuid": seedUUID,
		}
		if dep.Skills == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配符文写面。"))
		}
		if uuid == "" || seedUUID == "" || uuid == seedUUID {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "合成参数无效。"))
		}
		newLevel, err := dep.Skills.SkillComposite(req.UserID, uuid, seedUUID)
		if err != nil {
			return cn35wire.MarshalJSON(skillError(err))
		}
		c.Decoded["level"] = newLevel
		return skillSuccess(dep, c, req)
	})
}

// skillError 符文写面错误 → 客户端 modal。
func skillError(err error) map[string]any {
	switch {
	case errors.Is(err, ErrSkillNotOwned):
		return trainingError("无法操作", "该符文不在当前账号库存中。")
	case errors.Is(err, ErrUnitNotOwned):
		return trainingError("无法装备", "目标同伴不在当前账号库存中。")
	case errors.Is(err, ErrSkillNotEquipable):
		return trainingError("无法装备", "该符文不可装备。")
	case errors.Is(err, ErrSkillWeaponMismatch):
		return trainingError("无法装备", "该符文与同伴的武器种不匹配。")
	case errors.Is(err, ErrSkillDuplicateKind):
		return trainingError("无法装备", "同名符文不可重复装备。")
	case errors.Is(err, ErrSkillSlotFull):
		return trainingError("无法装备", "该同伴的符文槽位已满。")
	case errors.Is(err, ErrSkillProtected):
		return trainingError("无法操作", "包含已保护的符文。")
	case errors.Is(err, ErrSkillEquipped):
		return trainingError("无法操作", "装备中的符文请先卸下。")
	case errors.Is(err, ErrSkillLevelCap):
		return trainingError("已达上限", "该符文的魔力已达强化上限。")
	case errors.Is(err, ErrPowerupItemMismatch), errors.Is(err, ErrItemNotFound):
		return trainingError("素材不符", "该素材不可用于此符文强化。")
	case errors.Is(err, ErrItemInsufficient):
		return trainingError("素材不足", "持有的强化素材不足。")
	case errors.Is(err, ErrSkillCompositeBase):
		return trainingError("无法合成", "基座符文需極良以上品质。")
	case errors.Is(err, ErrSkillCompositeSeed):
		return trainingError("无法合成", "该符文不可作为合成素材。")
	}
	return trainingError("操作失败", "符文操作失败，请重试。")
}
