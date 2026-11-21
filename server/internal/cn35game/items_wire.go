// items_wire.go ITEM350-02/07/09：物品背包读面、徽章交易所与觉醒写面。
//
// 端点：
//
//	GET|POST /user_items/                  道具列表（真实库存投影）
//	GET|POST /medal_items/                 徽章商品列表（生效版本）
//	GET|POST /medal_items/exchange         徽章兑换（?id=<goods_id>&count=<n>
//	                                       [&request_key=<幂等键>]）
//	GET|POST /user_units/awake             觉醒（?id=<unit_id>[&count=<n>]，暂定契约）
//	GET|POST /user_items/use/              物品使用（?id=<content_id>&count=<n>）
//
// user_items_hash 分类键取目录 bag_category（分类语义见 0021 迁移：
// 1=觉醒之书 2=进化素材 3=同伴外型 4=背景 5=音乐 6=其他 7=礼物匣）。
// 交易所：客户端 DataCache.UpdateDataByResponse 平铺 data 键，
// ExchangeListParts 经 data_key="medal_right_hash" 绑定文档后按
// item_list/powerup_item_list/story_unit_list/rare_medal_item_list/
// friend_medal_item_list 五个分组切页签（ExchangeListParts.cs:82-88），
// 左侧面板绑 medal_unit_list。/user_units/awake 路径与参数为重建值。
package cn35game

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

// itemRowDoc 背包行文档（客户端 UserItemsListRow.cs 契约）。
//
// 使用按钮可用条件 stock_count>0（type=10005/数量限定的特例除外）；
// UseClick 按 code 分派：restore_ap/bp_heal/heal/rush/gift_box→
// /user_items/use/?id=&count=1（id 为 content_id），boost→选人过滤
// （rare_type_id=档星级×10）+count_modal_hash→/user_units/awake?
// id={选中同伴}&count=N，未登记 code→默认 use 请求。count_modal_hash
// 中的 {key} 占位符由客户端用选中同伴 docHash 字段回填；modal.body
// 里的 ::count:: 替换为选择数量。无使用途径的物品下发 hide_button
// 隐藏按钮（客户端隐藏而非灰置）。
func itemRowDoc(pi PlayerItem) map[string]any {
	category := pi.Def.BagCategory
	if category == "" {
		category = "1"
	}
	rareTypeID := pi.Def.Rare * 10
	if rareTypeID <= 0 {
		rareTypeID = 10
	}
	doc := map[string]any{
		"id":                    pi.Def.ContentID,
		"item_id":               pi.Def.ContentID,
		"item_key":              pi.ItemKey,
		"name":                  pi.Def.Name,
		"num":                   pi.Quantity,
		"count":                 pi.Quantity,
		"asset_id":              pi.Def.ContentID,
		"asset_name":            pi.Def.IconAssetName,
		"icon_asset_name":       pi.Def.IconAssetName,
		"attached":              false,
		"bg_id":                 "",
		"bgm":                   false,
		"btn_label":             "使用",
		"use_label":             "使用",
		"code":                  "item",
		"compose_cost_cnt":      0,
		"compose_target_id":     0,
		"content_type_id":       3,
		"decompose_btn_disable": false,
		"decompose_btn_label":   "分解",
		"decompose_btn_offset":  false,
		"decompose_cost_cnt":    0,
		"decompose_target_id":   0,
		"description":           pi.Def.Name,
		"hide_button":           false,
		"item_type":             1,
		"order_num":             pi.Def.ContentID,
		"rare_type_id":          rareTypeID,
		"type":                  1,
		"upper": map[string]any{
			"asset_name": pi.Def.IconAssetName,
			"count":      0,
			"id":         pi.Def.ContentID,
		},
		"category": category,
		"rare":     pi.Def.Rare,
		"element":  pi.Def.Element,
		"form":     pi.Def.Form,
		// stock_count/count_label/count_title_label：数量展示与使用按钮
		// 可用判定（UserItemsListRow.cs:50-79,127-131）。
		"stock_count":       pi.Quantity,
		"count_label":       strconv.Itoa(pi.Quantity),
		"count_title_label": "持有数：",
		"count_modal_hash": map[string]any{
			"title": "使用" + pi.Def.Name,
			"icon": map[string]any{
				"asset_name": pi.Def.IconAssetName,
				"x":          0,
				"y":          0,
			},
			"body": map[string]any{
				"price":       0,
				"text_format": "",
				"x":           -80,
				"y":           60,
			},
			"body2": map[string]any{
				"scaleX": 10,
				"scaleY": 10,
				"text":   "",
				"y":      60,
			},
			"count": map[string]any{
				"min": 1, "max": 1, "step": 1, "default": 1, "digit": 0,
			},
			"button": map[string]any{
				"label": "使用",
				"style": "yellow",
				"url":   "/user_units/awake?id={id}",
			},
			"modal": map[string]any{
				"title": "使用确认",
				"body":  "让[eb8900]{name}[-]的\n觉醒等级成长吗\n\n[14adcc]觉醒限制从 {duplicated_count}[-]成长到[14adcc]{max_duplicated_count}[-]\n（使用 ::count:: 本）",
			},
		},
	}
	if pi.Def.Kind != "book" || pi.Def.Element > 0 {
		doc["attr_id"] = pi.Def.Element
	}
	switch pi.Def.Kind {
	case "book":
		// boost 路径：客户端按 rare_type_id 过滤同星级未满觉醒同伴，
		// 选中后以 count_modal_hash 弹数量选择，确认后请求
		// /user_units/awake?id={id}&count=N。
		doc["code"] = "boost"
		doc["description"] = "可使对应星级的同伴觉醒1次"
	case "consumable":
		switch pi.ItemKey {
		case "ap_potion":
			doc["code"] = "restore_ap"
			doc["description"] = "恢复全部AP"
		case "bp_potion":
			doc["code"] = "bp_heal"
			doc["description"] = "恢复全部BP"
		case "power_stone":
			doc["id"] = 200016
			doc["item_id"] = 200016
			doc["content_type_id"] = 2
			doc["item_type"] = 0
			doc["rare_type_id"] = 10
			doc["order_num"] = 200016
			doc["code"] = "power_stone"
			doc["asset_id"] = nil
			doc["asset_name"] = "common_icon/item_200016"
			doc["icon_asset_name"] = "common_icon/item_200016"
			doc["description"] = "可以使两位相同星级的同伴互换等级及觉醒数的神奇石头"
			doc["hide_button"] = false
		case "scout_book_3star", "scout_book_4star", "scout_book_5star":
			orderNum := pi.Def.ContentID
			if pi.ItemKey == "scout_book_3star" {
				orderNum = 100010
			}
			doc["id"] = pi.Def.ContentID
			doc["item_id"] = pi.Def.ContentID
			doc["content_type_id"] = 2
			doc["code"] = "scout_book"
			doc["order_num"] = orderNum
			doc["asset_id"] = nil
			doc["asset_name"] = pi.Def.IconAssetName
			doc["icon_asset_name"] = pi.Def.IconAssetName
			doc["description"] = fmt.Sprintf("可以招募一位★%d同伴", pi.Def.Rare)
			doc["hide_button"] = false
		case "gacha_ticket_11", "gacha_ticket_12", "gacha_ticket_13":
			tid, _ := strconv.Atoi(strings.TrimPrefix(pi.ItemKey, "gacha_ticket_"))
			doc["id"] = tid
			doc["item_id"] = tid
			doc["content_type_id"] = 2
			doc["code"] = "ticket"
			doc["order_num"] = tid
			doc["asset_id"] = nil
			doc["asset_name"] = pi.Def.IconAssetName
			doc["icon_asset_name"] = pi.Def.IconAssetName
			doc["description"] = fmt.Sprintf("集齐5枚可招募一名★%d同伴", pi.Def.Rare)
			doc["hide_button"] = false
		default:
			doc["hide_button"] = true
		}
	default:
		// 素材等无使用途径的物品：隐藏按钮而非灰置。
		doc["code"] = "powerup_item"
		doc["hide_button"] = true
		if pi.ItemKey == "evolve_item_1" {
			doc["id"] = 1
			doc["item_id"] = 1
			doc["content_type_id"] = 107
			doc["item_type"] = 3
			doc["rare_type_id"] = 10
			doc["order_num"] = 929
			doc["code"] = "evolve_item"
			doc["asset_id"] = nil
			doc["asset_name"] = "common_icon/evolve_item_1"
			doc["icon_asset_name"] = "common_icon/evolve_item_1"
			doc["description"] = "用于提炼进化之卵的道具"
		} else if strings.HasPrefix(pi.ItemKey, "evolve_fancy_item_") {
			eggIDStr := strings.TrimPrefix(pi.ItemKey, "evolve_fancy_item_")
			eggID, _ := strconv.Atoi(eggIDStr)
			doc["id"] = eggID
			doc["item_id"] = eggID
			doc["content_type_id"] = 106
			doc["item_type"] = 4
			doc["rare_type_id"] = 10
			doc["order_num"] = 930 + eggID
			doc["code"] = "evolve_fancy_item"
			doc["asset_id"] = nil
			doc["asset_name"] = "common_icon/" + pi.ItemKey
			doc["icon_asset_name"] = "common_icon/" + pi.ItemKey
			doc["description"] = "用于魔宠融合的进化之卵"
		}
	}
	return doc
}

// userItemsData 背包读面投影：user_items 平铺 + user_items_hash 按
// bag_category 分组。写面回推与 /user_items/ 同源（ITEM350-13），
// 保证结算/礼物/兑换/觉醒后客户端缓存即时刷新。
func userItemsData(dep Deps, userID string) (map[string]any, error) {
	itemStore := dep.Items
	if itemStore == nil {
		if is, ok := dep.Accounts.(ItemStore); ok {
			itemStore = is
		}
	}
	if itemStore == nil {
		return map[string]any{
			"user_items":      []any{},
			"user_items_hash": map[string]any{},
		}, nil
	}
	items, err := itemStore.PlayerItems(userID)
	if err != nil {
		return nil, fmt.Errorf("cn35 物品库存读取失败: %w", err)
	}
	flat := []any{}
	hash := map[string]any{}
	for _, pi := range items {
		if !pi.Def.Enabled {
			continue
		}
		doc := itemRowDoc(pi)
		flat = append(flat, doc)
		cat := doc["category"].(string)
		if cat == "" {
			cat = "1"
		}
		bucket, _ := hash[cat].([]any)
		hash[cat] = append(bucket, doc)
	}
	if len(hash) == 0 {
		for _, cat := range []string{"1", "2", "3", "4", "5", "6", "7"} {
			hash[cat] = []any{}
		}
	}
	return map[string]any{
		"user_items":      flat,
		"user_items_hash": hash,
	}, nil
}

func defaultUserBgmOptions() map[string]any {
	return map[string]any{
		"title":         "背景音乐设置",
		"selected_rgba": "255,255,255,255",
		"default_track_names": map[string]any{
			"Home": "bgm_00001",
		},
		"options": []any{
			map[string]any{
				"id":              1,
				"order_num":       1,
				"content_type_id": 3,
				"rare_type_id":    10,
				"name":            "梅露可物语",
				"title":           "默认背景音乐",
				"description":     "主页默认背景音乐",
				"code":            "bgm",
				"bgm":             true,
				"ignore_color":    false,
				"asset_id":        "00001",
				"asset_name":      "bgm/bgm_00001",
				"icon_asset_name": "common/icon_bgm",
				"page_message":    "",
				"prefab":          "",
			},
		},
	}
}

// userItemsHandler /user_items/ 真实读面。
func userItemsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		data, err := userItemsData(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		data["user_bgm_options"] = defaultUserBgmOptions()
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_items",
			"items": len(data["user_items"].([]any)),
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// medalGoodsDoc 徽章商品行（暂定字段集：id=goods_id、item_id=content_id、
// name/price/asset_name/count=可购标识）。
//
// price=0 的免费商品下发 price=1：ExchangeImageButton.cs:144-155 仅在
// price/p_price/friend_price 其一 >0 时才发起 /medal_items/exchange，
// 0 会被静默吞掉点击。服务端结算仍按快照 Price=0 实扣，display 与扣费
// 不一致是客户端下行下限造成的既定偏差，行文案以 price_str="免费" 明示。
func medalGoodsDoc(g MedalGoodDraft, def ItemDef) map[string]any {
	wirePrice := g.Price
	if wirePrice <= 0 {
		wirePrice = 1
	}
	name := g.Name
	if (g.ItemKey == "ap_potion" || g.ItemKey == "bp_potion") && def.Name != "" {
		name = def.Name
	}
	doc := map[string]any{
		"id":         g.GoodsID,
		"goods_id":   g.GoodsID,
		"item_id":    def.ContentID,
		"name":       name,
		"price":      wirePrice,
		"price_name": "徽章",
		"currency":   "medal",
		// 客户端取图字段是 icon_asset_name（ExchangeImageButton /
		// ShowItemCountSelectFrameModal 实机取证）；asset_name 兼容保留。
		"asset_name":      def.IconAssetName,
		"icon_asset_name": def.IconAssetName,
		"sort":            g.Sort,
	}
	if g.Price <= 0 {
		doc["price_str"] = "免费"
	}
	return doc
}

// medalGoodsGroup 商品行页签归属：显式 group_key 优先；空值按物品 kind
// 归组（material→素材页、其余→道具页），与 0023 前行为一致。
func medalGoodsGroup(g MedalGoodDraft, def ItemDef) string {
	switch g.Group {
	case "item":
		return "item_list"
	case "powerup":
		return "powerup_item_list"
	case "story_unit":
		return "story_unit_list"
	case "rare_medal":
		return "rare_medal_item_list"
	case "friend_medal":
		return "friend_medal_item_list"
	}
	if def.Kind == "material" {
		return "powerup_item_list"
	}
	return "item_list"
}

// medalItemsData 组装交易所列表文档：文档键与 ExchangeListParts.cs:82-88
// 的分组名一致；分组归属见 medalGoodsGroup（默认：书籍→item_list、素材→
// powerup_item_list；显式 group_key 可投放到稀有徽章等其余页签——
// rare_medal_item_list 非空即让客户端切四按钮模式）。
func medalItemsData(dep Deps) (map[string]any, error) {
	goods, err := dep.Medal.MedalGoods()
	if err != nil {
		return nil, err
	}
	defs, err := dep.Items.ItemCatalog()
	if err != nil {
		return nil, err
	}
	byKey := map[string]ItemDef{}
	for _, d := range defs {
		byKey[d.ItemKey] = d
	}
	lists := map[string][]any{
		"item_list":              {},
		"powerup_item_list":      {},
		"story_unit_list":        {},
		"rare_medal_item_list":   {},
		"friend_medal_item_list": {},
	}
	for _, g := range goods {
		if !g.Enabled {
			continue
		}
		def, ok := byKey[g.ItemKey]
		if !ok || !def.Enabled {
			continue
		}
		doc := medalGoodsDoc(g, def)
		group := medalGoodsGroup(g, def)
		lists[group] = append(lists[group], doc)
	}
	out := make(map[string]any, len(lists))
	for k, v := range lists {
		out[k] = v
	}
	return out, nil
}

// medalItemsHandler /medal_items/ 商品列表读面。
func medalItemsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		doc, err := medalItemsData(dep)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "medal_items",
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{
			"medal_items":      doc,
			"medal_right_hash": doc,
			"medal_unit_list":  []any{},
		}})
	})
}

// medalExchangeHandler /medal_items/exchange?id=&count=[&request_key=]：
// 单事务扣徽章加书（ExchangeImageButton.cs:130 契约形态）。
func medalExchangeHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		goodsID, idErr := strconv.Atoi(firstNonEmpty(req.Query.Get("id"), req.Form.Get("id")))
		count := 1
		var cntErr error
		if raw := firstNonEmpty(req.Query.Get("count"), req.Form.Get("count")); raw != "" {
			count, cntErr = strconv.Atoi(raw)
		}
		requestKey := firstNonEmpty(req.Query.Get("request_key"), req.Form.Get("request_key"))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "medal_exchange",
			"goods_id": goodsID, "count": count,
		}
		if idErr != nil || cntErr != nil || goodsID <= 0 || count <= 0 || count > 999 {
			return cn35wire.MarshalJSON(trainingError(
				"无法兑换", "商品参数无效，请重新打开兑换页面。"))
		}
		res, err := dep.Medal.ExchangeMedal(req.UserID, goodsID, count, requestKey)
		if err != nil {
			switch {
			case errors.Is(err, ErrGoodsNotFound):
				return cn35wire.MarshalJSON(trainingError("无法兑换", "该商品当前不可兑换。"))
			case errors.Is(err, ErrMedalInsufficient):
				return cn35wire.MarshalJSON(trainingError("徽章不足", "持有的徽章不足以完成兑换。"))
			case errors.Is(err, ErrPurchaseLimit):
				return cn35wire.MarshalJSON(trainingError("无法兑换", "该商品已达限购上限。"))
			case errors.Is(err, ErrDuplicateRequestConflict), errors.Is(err, ErrInvalidRequest):
				return cn35wire.MarshalJSON(trainingError("无法兑换", "兑换请求不合法。"))
			}
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 兑换后快照失败: %w", err)
		}
		itemsData, err := userItemsData(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		goodsDoc, err := medalItemsData(dep)
		if err != nil {
			return nil, err
		}
		c.Decoded["medal"] = res.Medal
		data := map[string]any{
			"user":             snap.User,
			"medal_items":      goodsDoc,
			"medal_right_hash": goodsDoc,
			"medal_unit_list":  []any{},
			"duplicate":        res.Duplicate,
		}
		for k, v := range itemsData {
			data[k] = v
		}
		// RUNE350-01：符文商品兑换后回推最新符文列表，客户端缓存即时
		// 刷新（等价 user_items 回推路径）。
		if dep.Skills != nil {
			skData, skErr := userSkillsData(dep, req.UserID)
			if skErr != nil {
				return nil, skErr
			}
			for k, v := range skData {
				data[k] = v
			}
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

// unitAwakeHandler /user_units/awake?id=<unit_id>[&count=<n>]（暂定契约）：
// 每次消耗 awakening_book_<rare> × count 本，duplicated_count+count；上限取
// statmodel.MaxDuplicatedCount(rare)（重建值，非原服事实）。count 缺省 1。
func unitAwakeHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		unitID, idErr := strconv.Atoi(firstNonEmpty(req.Query.Get("id"), req.Form.Get("id")))
		count := 1
		var cntErr error
		if raw := firstNonEmpty(req.Query.Get("count"), req.Form.Get("count")); raw != "" {
			count, cntErr = strconv.Atoi(raw)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_units_awake",
			"unit_id": unitID,
		}
		if dep.Awaken == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"觉醒未启用", "服务端未装配觉醒写面。"))
		}
		if idErr != nil || unitID <= 0 || cntErr != nil || count <= 0 || count > 999 {
			return cn35wire.MarshalJSON(trainingError(
				"无法觉醒", "参数无效，请重新打开同伴界面。"))
		}
		_, found, err := findUnitRecord(dep, req.UserID, unitID)
		if err != nil {
			return nil, err
		}
		if !found {
			return cn35wire.MarshalJSON(trainingError(
				"无法觉醒", "该同伴不在当前账号库存中。"))
		}
		u, err := dep.Catalog.Unit(unitID)
		if err != nil {
			return nil, fmt.Errorf("cn35 觉醒目标模板缺失 unit_id=%d: %w", unitID, err)
		}
		res, err := dep.Awaken.AwakenUnit(req.UserID, unitID, u.Rare,
			statmodel.MaxDuplicatedCount(u.Rare), count)
		if err != nil {
			switch {
			case errors.Is(err, ErrAwakeCap):
				return cn35wire.MarshalJSON(trainingError(
					"已达觉醒上限", "该同伴的觉醒次数已达上限。"))
			case errors.Is(err, ErrItemInsufficient):
				return cn35wire.MarshalJSON(trainingError(
					"觉醒书不足", "缺少对应星级的觉醒书，可在徽章交易所兑换。"))
			case errors.Is(err, ErrAwakeUnitNotFound), errors.Is(err, ErrItemNotFound):
				return cn35wire.MarshalJSON(trainingError(
					"无法觉醒", "请求与当前状态不符，请重新打开界面。"))
			}
			return nil, err
		}
		data, err := inventoryDataFor(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 觉醒后快照失败: %w", err)
		}
		numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
		data["user"] = snap.User
		itemsData, err := userItemsData(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		for k, v := range itemsData {
			data[k] = v
		}
		data["duplicate"] = res.Duplicate
		data["albums"] = nil
		c.Decoded["awakened"] = true
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

// useItemHandler /user_items/use/?id=<content_id>&count=<n>（重建契约）：
// 药水类消耗品使用写面。restore_ap/bp_heal/heal/rush/gift_box 等 code 的
// 客户端上行统一走此端点（UserItemsListRow.cs UseClick，id 为 content_id、
// count 恒发 1）。失败回 modal 弹窗（ResponseFilter 依赖 modal 键复位 UI）。
// 成功后回推 data.user + user_items(_hash)，客户端缓存即时刷新。
func useItemHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		rawID := firstNonEmpty(req.Query.Get("id"), req.Form.Get("id"))
		var contentID int
		var idErr error
		switch rawID {
		case "potion_ap", "ap_potion":
			contentID = 201
		case "potion_bp", "bp_potion":
			contentID = 202
		case "coin_ap":
			contentID = 203
		default:
			contentID, idErr = strconv.Atoi(rawID)
		}
		count := 1
		var cntErr error
		if raw := firstNonEmpty(req.Query.Get("count"), req.Form.Get("count")); raw != "" {
			count, cntErr = strconv.Atoi(raw)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_items_use",
			"content_id": contentID, "count": count,
		}
		if idErr == nil && isScoutBookContentID(contentID) {
			return useScoutBookItem(dep, c, req, contentID, count)
		}
		itemUse := dep.ItemUse
		if itemUse == nil {
			if iu, ok := dep.Accounts.(ItemUseStore); ok {
				itemUse = iu
			}
		}
		if itemUse == nil {
			return cn35wire.MarshalJSON(trainingError(
				"使用失败", "服务端未装配物品使用写面。"))
		}
		if idErr != nil || cntErr != nil || contentID <= 0 || count <= 0 || count > 999 {
			return cn35wire.MarshalJSON(trainingError(
				"使用失败", "参数无效，请重新打开物品界面。"))
		}
		res, err := itemUse.UseItem(req.UserID, contentID, count)
		if err != nil {
			switch {
			case errors.Is(err, ErrItemNotFound):
				return cn35wire.MarshalJSON(trainingError("使用失败", "该物品不存在。"))
			case errors.Is(err, ErrItemNotUsable):
				return cn35wire.MarshalJSON(trainingError("使用失败", "该物品不能使用。"))
			case errors.Is(err, ErrItemInsufficient):
				return cn35wire.MarshalJSON(trainingError("使用失败", "物品数量不足。"))
			case errors.Is(err, ErrInvalidRequest):
				return cn35wire.MarshalJSON(trainingError("使用失败", "参数无效，请重新打开物品界面。"))
			}
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 物品使用后快照失败: %w", err)
		}
		numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
		data := map[string]any{
			"user":          snap.User,
			"user_coin":     snap.UserCoin,
			"used_item_key": res.ItemKey,
			"count":         res.Count,
			"duplicate":     res.Duplicate,
		}
		itemsData, err := userItemsData(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		for k, v := range itemsData {
			data[k] = v
		}
		c.Decoded["used"] = true
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
			"modal": map[string]any{
				"title": "使用成功",
				"body":  "已使用该道具。",
			},
		})
	})
}

// unitComposeUpgradeItemHandler /user_units/compose_upgrade_item：
// 潜能素材合成写面（UserItemsListRow.cs:206，回推 data.user + user_items(_hash) + modal）。
func unitComposeUpgradeItemHandler(dep Deps, decompose bool) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		endpoint := "user_units_compose_upgrade_item"
		title, body := "合成完成", "已完成素材合成操作。"
		if decompose {
			endpoint = "user_units_decompose_upgrade_item"
			title, body = "分解完成", "已完成素材分解操作。"
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": endpoint,
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 %s 快照失败: %w", endpoint, err)
		}
		numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
		data := map[string]any{
			"user": snap.User,
		}
		if dep.Items != nil {
			itemsData, ierr := userItemsData(dep, req.UserID)
			if ierr != nil {
				return nil, ierr
			}
			for k, v := range itemsData {
				data[k] = v
			}
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": data,
			"modal": map[string]any{
				"title": title,
				"body":  body,
			},
		})
	})
}

var scoutBookItemSpecs = map[int]struct {
	ItemKey  string
	BookName string
	Rare     int
}{
	100015: {ItemKey: "scout_book_3star", BookName: "3星契约书（★★★）", Rare: 3},
	100011: {ItemKey: "scout_book_4star", BookName: "4星契约书（★★★★）", Rare: 4},
	100012: {ItemKey: "scout_book_5star", BookName: "5星契约书（★★★★★）", Rare: 5},
}

func isScoutBookContentID(contentID int) bool {
	_, ok := scoutBookItemSpecs[contentID]
	return ok
}

func itemExchangeStoreFor(dep Deps) ItemExchangeStore {
	if dep.ItemExchange != nil {
		return dep.ItemExchange
	}
	if es, ok := dep.Items.(ItemExchangeStore); ok {
		return es
	}
	if es, ok := dep.ItemUse.(ItemExchangeStore); ok {
		return es
	}
	if es, ok := dep.Accounts.(ItemExchangeStore); ok {
		return es
	}
	return nil
}

func useScoutBookItem(dep Deps, c *httpapi.Ctx, req *cn35wire.GameRequest, contentID, count int) ([]byte, error) {
	spec := scoutBookItemSpecs[contentID]
	if count != 1 {
		return cn35wire.MarshalJSON(trainingError(
			"使用失败", "星级契约书每次仅可使用 1 本。"))
	}
	body, _, err := grantPoolUnitByItem(dep, c, req, spec.ItemKey, spec.BookName, 1, spec.Rare, true)
	if err == nil {
		c.Decoded["used"] = true
		c.Decoded["used_item_key"] = spec.ItemKey
	}
	return body, err
}

// grantPoolUnitByItem 共享的契约书/星级信发卡结算原语：按目标星级从池 "2"
// 合格成员中等概率抽 1 人，单事务扣减道具并结算单位（或满觉转徽章）。
func grantPoolUnitByItem(
	dep Deps, c *httpapi.Ctx, req *cn35wire.GameRequest,
	itemKey, displayName string, costCount, rare int, withModal bool,
) ([]byte, bool, error) {
	exStore := itemExchangeStoreFor(dep)
	if exStore == nil {
		b, _ := cn35wire.MarshalJSON(trainingError("招募失败", "服务端未装配契约书兑换存储。"))
		return b, false, nil
	}
	cfg, err := gachaActiveConfig(dep)
	if err != nil {
		return nil, false, err
	}
	if cfg == nil || len(cfg.MembersByPool["2"]) == 0 {
		b, _ := cn35wire.MarshalJSON(trainingError("招募暂未开放", "卡池配置尚未发布，请稍后再试。"))
		return b, false, nil
	}
	rarityID := rare * 10
	cands := make([]int, 0, 128)
	for _, gu := range cfg.MembersByPool["2"] {
		if gu.RarityID != rarityID {
			continue
		}
		if uid, perr := strconv.Atoi(gu.UnitID); perr == nil && uid > 0 {
			cands = append(cands, uid)
		}
	}
	if len(cands) == 0 {
		b, _ := cn35wire.MarshalJSON(trainingError("招募失败", "暂无对应星级的可招募同伴。"))
		return b, false, nil
	}
	nBig, rerr := rand.Int(rand.Reader, big.NewInt(int64(len(cands))))
	if rerr != nil {
		return nil, false, fmt.Errorf("cn35 道具发卡随机选取失败: %w", rerr)
	}
	unitID := cands[int(nBig.Int64())]

	dupCap := cfg.Rules.DuplicateMaxCount
	unitName := fmt.Sprintf("ID:%d", unitID)
	if dep.Catalog != nil {
		if u, uerr := dep.Catalog.Unit(unitID); uerr == nil && u != nil {
			if mc := statmodel.MaxDuplicatedCount(u.Rare); mc < dupCap {
				dupCap = mc
			}
			if u.Name != "" {
				unitName = u.Name
			}
		}
	}
	compMedal := cfg.Rules.DuplicateCompensation[rarityID].Medal

	grantRes, err := exStore.UseItemGrantUnit(req.UserID, itemKey, costCount, unitID, dupCap, compMedal)
	if err != nil {
		switch {
		case errors.Is(err, ErrItemNotFound):
			b, _ := cn35wire.MarshalJSON(trainingError("招募失败", "该招募道具不存在。"))
			return b, false, nil
		case errors.Is(err, ErrItemInsufficient):
			b, _ := cn35wire.MarshalJSON(trainingError("数量不足", fmt.Sprintf("%s数量不足（需要 %d）。", displayName, costCount)))
			return b, false, nil
		case errors.Is(err, ErrInvalidRequest):
			b, _ := cn35wire.MarshalJSON(trainingError("招募失败", "请求参数无效。"))
			return b, false, nil
		}
		return nil, false, err
	}

	var entrySkills []PlayerSkill
	if !grantRes.Newcomer {
		skillsByUnit, skErr := skillsByUnitFor(dep, req.UserID)
		if skErr != nil {
			return nil, true, skErr
		}
		entrySkills = skillsByUnit[unitID]
	}
	entry, err := gachaResultEntryDoc(dep, grantRes.UnitID, grantRes.Level,
		grantRes.DupBefore, grantRes.DupAfter, grantRes.Newcomer, entrySkills)
	if err != nil {
		return nil, true, err
	}
	data, err := inventoryDataFor(dep, c, req.UserID)
	if err != nil {
		return nil, true, err
	}
	snap, err := dep.Accounts.Snapshot(req.UserID)
	if err != nil {
		return nil, true, fmt.Errorf("cn35 道具发卡后快照失败: %w", err)
	}
	numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
	data["user"] = snap.User
	data["user_coin"] = snap.UserCoin
	data["temp_gacha_result"] = []any{entry}
	data["temp_gacha_reward"] = []any{}
	data["albums"] = nil
	data["gachas_extra_button"] = map[string]any{"breadshop_button_visible": true}
	itemsData, err := userItemsData(dep, req.UserID)
	if err != nil {
		return nil, true, err
	}
	for k, v := range itemsData {
		data[k] = v
	}

	resp := map[string]any{
		"status": "success",
		"data":   data,
	}
	if withModal {
		bodyText := fmt.Sprintf("使用%s招募到了【%s】！", displayName, unitName)
		if grantRes.CompensatedMedal > 0 {
			bodyText += fmt.Sprintf("（已满觉醒，转化为 %d 枚徽章）", grantRes.CompensatedMedal)
		}
		resp["modal"] = map[string]any{
			"title": "招募成功",
			"body":  bodyText,
		}
	}
	raw, merr := cn35wire.MarshalJSON(resp)
	return raw, true, merr
}
