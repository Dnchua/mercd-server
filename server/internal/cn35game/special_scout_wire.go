// special_scout_wire.go CN3.5 每日轮换特殊招募、契约书 6 合 1 兑换与星级信券接口。
package cn35game

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

const gachaCurrencyScoutBook = "scout_book_1"

// registerSpecialScoutRoutes 注册特殊招募、星级契约书兑换与星级信可用列表端点。
func registerSpecialScoutRoutes(register func(string, string, string, httpapi.Handler), dep Deps) {
	register(http.MethodGet, "/gachas/available_unit_list",
		"CN 3.5.0 招募可获名单列表（含当期特殊招募与 5★ 展示）",
		gachaAvailableUnitListHandler(dep))
	register(http.MethodPost, "/gachas/available_unit_list",
		"CN 3.5.0 招募可获名单列表 POST 形态",
		gachaAvailableUnitListHandler(dep))

	for _, ep := range []struct {
		path string
		name string
		key  string
	}{
		{"/gachas/pickup_gacha/", "gachas_pickup_gacha", "pickup_gacha"},
		{"/gachas/attr_gacha", "gachas_attr_gacha", "pickup_gacha"},
		{"/gachas/weapon_gacha/", "gachas_weapon_gacha", "weapon_gacha"},
		{"/gachas/dia_only_gacha/", "gachas_dia_only_gacha", "dia_only_gacha"},
	} {
		h := specialScoutBannerHandler(dep, ep.name, ep.key)
		register(http.MethodGet, ep.path, "CN 3.5.0 每日轮换特殊招募页面（"+ep.key+"）", h)
		register(http.MethodPost, ep.path, "CN 3.5.0 每日轮换特殊招募页面 POST 形态（"+ep.key+"）", h)
	}

	register(http.MethodGet, "/gachas/choose_country_only_page/",
		"CN 3.5.0 每日轮换属国/主题特殊招募页（country_pick_page）",
		specialChoosePageHandler(dep, "gachas_choose_country_only_page"))
	register(http.MethodPost, "/gachas/choose_country_only_page/",
		"CN 3.5.0 每日轮换属国/主题特殊招募页 POST 形态",
		specialChoosePageHandler(dep, "gachas_choose_country_only_page"))

	register(http.MethodGet, "/gachas/choose_attr_page/",
		"CN 3.5.0 每日轮换特殊招募入口页（Home/country_pick_page）",
		specialChoosePageHandler(dep, "gachas_choose_attr_page"))
	register(http.MethodPost, "/gachas/choose_attr_page/",
		"CN 3.5.0 每日轮换特殊招募入口页 POST 形态",
		specialChoosePageHandler(dep, "gachas_choose_attr_page"))

	register(http.MethodGet, "/gachas/show/",
		"CN 3.5.0 特殊招募总览（box/boys/girls）",
		specialScoutShowHandler(dep))
	register(http.MethodPost, "/gachas/show/",
		"CN 3.5.0 特殊招募总览 POST 形态",
		specialScoutShowHandler(dep))

	register(http.MethodGet, "/gachas/ticket_available_list",
		"CN 3.5.0 ★3/★4/★5 信可用列表读面",
		ticketAvailableListHandler(dep))
	register(http.MethodPost, "/gachas/ticket_available_list",
		"CN 3.5.0 ★3/★4/★5 信可用列表 POST 形态",
		ticketAvailableListHandler(dep))

	register(http.MethodGet, "/gachas/exchange_scout_book",
		"CN 3.5.0 星级契约书 6 合 1 兑换（?item_id=100015|100011）",
		exchangeScoutBookHandler(dep))
	register(http.MethodPost, "/gachas/exchange_scout_book",
		"CN 3.5.0 星级契约书 6 合 1 兑换 POST 形态",
		exchangeScoutBookHandler(dep))
}

func decorateSpecialPoolDoc(doc map[string]any, p gachaPoolRules) map[string]any {
	if doc == nil {
		return nil
	}
	doc["bg_merge"] = map[string]any{
		"asset_name": "common/bg_scout_campaign_170803",
	}
	if p.Limited && p.BeginAt > 0 && p.EndAt > 0 {
		doc["time_text"] = formatWindowTimeRange(p.BeginAt, p.EndAt)
	}
	return doc
}

func specialScoutBasePayload(dep Deps, now time.Time) (map[string]any, *ActiveGachaConfig, *gachaPoolRules, map[string]any, error) {
	cfg, th, err := resolveRuntimeGachaConfig(dep, now)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	data := map[string]any{
		"gachas":               []any{},
		"gacha_warnings":       []any{},
		"gacha_button_options": map[string]any{},
		"gachas_extra_button":  map[string]any{"breadshop_button_visible": true},
	}
	if cfg == nil {
		return data, nil, nil, nil, nil
	}
	data["gachas"] = gachaPoolDocuments(cfg)
	data["gacha_button_options"] = gachaButtonOptions(cfg)
	avail, err := gachaAvailableUnitsDocs(dep, cfg)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for k, v := range avail {
		data[k] = v
	}
	var spRule *gachaPoolRules
	var spDoc map[string]any
	if th != nil {
		if r, ok := cfg.Rules.poolByID(th.PoolID); ok {
			rCopy := r
			spRule = &rCopy
			if d, dok := gachaPoolDoc(rCopy); dok {
				spDoc = decorateSpecialPoolDoc(d, rCopy)
			}
		}
	}
	return data, cfg, spRule, spDoc, nil
}

func gachaAvailableUnitListHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		poolID := firstNonEmpty(req.Query.Get("id"), req.Form.Get("id"))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "gachas_available_unit_list", "pool_id": poolID,
		}
		now := specialScoutNow(dep)
		data, cfg, spRule, _, err := specialScoutBasePayload(dep, now)
		if err != nil {
			return nil, err
		}
		if spec, ok := ticketPoolSpecs[poolID]; ok {
			// 信池（11/12/13）：GachaAvailableList 经 ticket_gacha_1x 的
			// available_list_option 读 available_<id>（原服录播契约）。
			docs, derr := ticketAvailableUnitDocs(dep, cfg, spec)
			if derr != nil {
				return nil, derr
			}
			data["available_"+poolID] = docs
		}
		avail5 := []any{}
		if cfg != nil {
			targetID := poolID
			if targetID == "" && spRule != nil {
				targetID = spRule.PoolID
			}
			if targetID == "" {
				targetID = "2"
			}
			key := gachaAvailableUnitsKey(targetID)
			if rows, ok := data[key].([]any); ok {
				for _, raw := range rows {
					row, _ := raw.(map[string]any)
					if row == nil {
						continue
					}
					if r, _ := asFloatTestFree(row["rare_type_id"]); int(r) == gachaRarityUR {
						avail5 = append(avail5, row)
					}
				}
			}
		}
		data["available_5"] = avail5
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

func asFloatTestFree(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	}
	return 0, false
}

func specialScoutBannerHandler(dep Deps, endpoint, key string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": endpoint,
		}
		now := specialScoutNow(dep)
		data, _, _, spDoc, err := specialScoutBasePayload(dep, now)
		if err != nil {
			return nil, err
		}
		list := []any{}
		if spDoc != nil {
			list = append(list, spDoc)
		}
		data[key] = list
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

func specialChoosePageHandler(dep Deps, endpoint string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": endpoint,
		}
		now := specialScoutNow(dep)
		data, cfg, spRule, spDoc, err := specialScoutBasePayload(dep, now)
		if err != nil {
			return nil, err
		}
		countryPage := map[string]any{}
		pickupList := []any{}
		if cfg != nil && spRule != nil && spDoc != nil {
			slotIdx, _, _ := SpecialScoutWindow(now)
			th := SpecialScoutRotation[slotIdx]
			pid, _ := strconv.Atoi(spRule.PoolID)
			countryPage = map[string]any{
				"id":             pid,
				"pool_id":        spRule.PoolID,
				"name":           spRule.Name,
				"kind":           string(th.Kind),
				"description":    spRule.Description,
				"began_at":       spRule.BeginAt,
				"ended_at":       spRule.EndAt,
				"time_text":      formatWindowTimeRange(spRule.BeginAt, spRule.EndAt),
				"rotation_total": len(SpecialScoutRotation),
				"bg_asset_name":  "common/bg_scout_campaign_170803",
			}
			pickupList = append(pickupList, spDoc)
		}
		data["country_pick_page"] = countryPage
		data["pickup_gacha"] = pickupList
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

func specialScoutShowHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "gachas_show",
		}
		now := specialScoutNow(dep)
		data, cfg, _, _, err := specialScoutBasePayload(dep, now)
		if err != nil {
			return nil, err
		}
		_, beganAt, endedAt := SpecialScoutWindow(now)
		buildGenderList := func(poolID string) []any {
			if cfg == nil {
				return []any{}
			}
			th, ok := specialScoutThemeByID(poolID)
			if !ok {
				return []any{}
			}
			rule, _, ok := buildSpecialScoutPoolForTheme(dep, cfg, th, false, beganAt, endedAt)
			if !ok {
				return []any{}
			}
			doc, dok := gachaPoolDoc(rule)
			if !dok {
				return []any{}
			}
			return []any{decorateSpecialPoolDoc(doc, rule)}
		}
		data["box_gacha"] = []any{}
		data["boys_gacha"] = buildGenderList("132")
		data["girls_gacha"] = buildGenderList("133")
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

// ticketGachaDoc 组装一条 ★3/★4/★5 信池文档。字段集与取值逐项对齐原服录播
// mlk-capture-201711 /gachas/ticket_available_list（13 份响应完全一致，原作观测）：
// data 下按 ticket_gacha_<id> 平铺三个对象（非数组）——UserItemsV2 页
// AbailableUnitListTicketGacha{HR,SR,UR} 按钮 data="ticket_gacha_1x" 直读该缓存键，
// OnClick 再以 option_key=available_list_option 深并入 GachaAvailableList。
func ticketGachaDoc(spec ticketPoolSpec) map[string]any {
	ball := []any{1, 3}
	if spec.Rare == 5 {
		ball = []any{1, 2}
	}
	availKey := "available_" + spec.PoolID
	return map[string]any{
		"id":                    spec.NumID,
		"name":                  spec.Name,
		"type":                  "Gacha::TicketOnly",
		"description":           spec.Description,
		"list_type_id":          spec.NumID,
		"max_execute_bonus":     0,
		"point":                 0,
		"price":                 0,
		"price_map":             map[string]any{},
		"ticket_count":          spec.CostCount,
		"max_executable_count":  1,
		"choice_unit_count":     1,
		"choice_unit_count_map": map[string]any{},
		"enable":                1,
		"ball_type_id_array":    ball,
		"purchase_limit_option": map[string]any{},
		"available_list_option": map[string]any{
			"parts": map[string]any{
				"ListParts": map[string]any{"data_key": availKey},
				"ChoiceRateButton": map[string]any{
					"widgets": map[string]any{
						"button": map[string]any{
							"OnClick": map[string]any{
								"url": "/gachas/show_choice_rate?id=" + spec.PoolID,
							},
						},
					},
				},
			},
			"requireData": map[string]any{
				availKey: map[string]any{"path": "/gachas/available_unit_list?id=" + spec.PoolID},
			},
		},
	}
}

// ticketAvailableUnitDocs 信池可获名单：池 "2" 已发布成员中目标星级的单位
// （与 grantPoolUnitByItem 抽取集合同源，名单即中奖集合）。
func ticketAvailableUnitDocs(dep Deps, cfg *ActiveGachaConfig, spec ticketPoolSpec) ([]any, error) {
	docs := []any{}
	if cfg == nil || dep.Catalog == nil {
		return docs, nil
	}
	for _, gu := range cfg.MembersByPool["2"] {
		if gu.RarityID != spec.Rare*10 {
			continue
		}
		id, err := strconv.Atoi(gu.UnitID)
		if err != nil {
			continue
		}
		u, err := dep.Catalog.Unit(id)
		if err != nil || u == nil {
			continue
		}
		d, err := buildUnitDoc(UnitRecord{UnitID: id, Level: 1}, u)
		if err != nil {
			return nil, fmt.Errorf("cn35 信池名单单位 %d 组装失败: %w", id, err)
		}
		delete(d, "unit_detail_message")
		docs = append(docs, d)
	}
	return docs, nil
}

func ticketAvailableListHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "gachas_ticket_available_list",
		}
		data := make(map[string]any, len(ticketPoolSpecs))
		for _, pid := range []string{"11", "12", "13"} {
			data["ticket_gacha_"+pid] = ticketGachaDoc(ticketPoolSpecs[pid])
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

func exchangeScoutBookHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		rawID := firstNonEmpty(req.Query.Get("item_id"), req.Form.Get("item_id"),
			req.Query.Get("id"), req.Form.Get("id"))
		itemID, idErr := strconv.Atoi(rawID)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "gachas_exchange_scout_book", "item_id": itemID,
		}
		if idErr != nil {
			return cn35wire.MarshalJSON(gachaError("兑换失败", "契约书编号无效。"))
		}
		spec, ok := scoutBookExchangeSpec[itemID]
		if !ok {
			return cn35wire.MarshalJSON(gachaError(
				"兑换失败", "仅支持 6 本 3星契约书合成 4星契约书，或 6 本 4星契约书合成 5星契约书。"))
		}
		exStore := itemExchangeStoreFor(dep)
		if exStore == nil {
			return cn35wire.MarshalJSON(gachaError("兑换失败", "服务端未装配契约书兑换写面。"))
		}
		if err := exStore.ExchangePlayerItem(req.UserID, spec.FromKey, spec.ToKey, spec.Cost); err != nil {
			switch {
			case errors.Is(err, ErrItemInsufficient):
				return cn35wire.MarshalJSON(gachaError("数量不足",
					fmt.Sprintf("%s不足 %d 本，无法合成 %s。", spec.FromMsg, spec.Cost, spec.ToMsg)))
			case errors.Is(err, ErrItemNotFound), errors.Is(err, ErrInvalidRequest):
				return cn35wire.MarshalJSON(gachaError("兑换失败", "契约书兑换参数无效。"))
			}
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 契约书兑换后快照失败: %w", err)
		}
		numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
		data := map[string]any{
			"user":           snap.User,
			"user_coin":      snap.UserCoin,
			"exchanged_from": spec.FromKey,
			"exchanged_to":   spec.ToKey,
			"cost_count":     spec.Cost,
		}
		itemsData, err := userItemsData(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		for k, v := range itemsData {
			data[k] = v
		}
		c.Decoded["exchanged"] = true
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
			"modal": map[string]any{
				"type":  "ok",
				"title": "合成成功",
				"body":  fmt.Sprintf("已消耗 %d 本%s，成功合成 1 本%s！", spec.Cost, spec.FromMsg, spec.ToMsg),
			},
		})
	})
}

// executeTicketGachaSettle 处理 /gachas/execute?id=11|12|13（5 枚 ★3/★4/★5 信单抽，
// 客户端上行不携带 count 参数）。
func executeTicketGachaSettle(
	dep Deps, c *httpapi.Ctx, req *cn35wire.GameRequest,
	poolID, countRaw, useScoutBook string,
) ([]byte, bool, error) {
	if useScoutBook != "" && useScoutBook != "false" && useScoutBook != "0" {
		return gachaReject("招募参数非法", "星级信招募不支持附带契约书参数。")
	}
	if countRaw != "" && countRaw != "1" {
		return gachaReject("招募参数非法", "星级信每次仅可招募 1 次。")
	}
	spec, ok := ticketPoolSpecs[poolID]
	if !ok {
		return gachaReject("招募暂未开放", "该招募已结束或不存在。")
	}
	raw, acted, err := grantPoolUnitByItem(dep, c, req, spec.ItemKey, spec.Name, spec.CostCount, spec.Rare, false)
	if err == nil && acted {
		c.Decoded["gacha_count"] = 1
		c.Decoded["ticket_pool"] = spec.PoolID
	}
	return raw, acted, err
}
