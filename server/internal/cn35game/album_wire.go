// album_wire.go ALBUM350-01：/albums/ 同伴图鉴真实读面（cn35-album-ui-restore）。
//
// 客户端契约（run/album-contract.md，ilspy-cs 取证）：
//   - UserAlbum 页 requireData.albums → /albums/，列表 data_key=albums。
//     计数器（AlbumButtonsListComponent.cs:340-345）：分子=非 hide 行、
//     分母=docList 全量 → 服务端必须返回**全目录**（catalog.UnitIDs），
//     未持有行带 hide=true（行渲染走 UnitListRow noRecordGo 占位，
//     UnitListRow.cs:322-336）。
//   - 行模式字段（AlbumButtonsListComponent）：
//     index 默认 / gallery 过滤 is_showable=="true" / wait 过滤
//     is_user_unit=="true" / back 过滤 is_waiting=="true"。三个键按
//     客户端 s() 字符串比较下发 "true"/"false"。
//   - 行筛选/排序字段（app_conf UserAlbum filter+SortOption）：sex_id/
//     rare_type_id/attr_id/weapon_id/country_id/at_max/hp_max/speed/
//     reach/taghness/age——复用 buildUnitDoc 已含字段。
//   - 点击行 → document.album_detail_message（序列化 PageMessage）→
//     AlbumUnitDetail。未持有单位（isUserUnit=false）走
//     cache.DataLoad("album_unit", data_path)（AlbumUnitDetail.cs:108-116），
//     data_path 为相对 baseURL 的 game 通路路径，响应 data 段逐键入缓存
//     （Common.cs:2091）→ /albums/album_unit?id=N 下发
//     {"data":{"album_unit":{...}}}。data_path 在 message 串内必须
//     %-escape（PageMessage 按 &/= 切分；MessageRow.cs:304 同法）。
//   - AlbumUnitDetail 消费 albums 行的 unit_alert/wait_alert(bool)、
//     max_duplicated_count(int)、waiting_unit_level、
//     waiting_unit_duplicated_count（待机单位信息；库存无 waiting 态，
//     waiting_* 键省略 → 客户端 s()/i() 缺省 "-"，不伪造）。
//
// 数值口径：与 /user_units/ 同源（buildUnitDoc + statmodel）：
//   - 持有行：库存实例态（level/duplicated/protected 实值）。
//   - 未持有行：Lv1/dup0 模板态（gacha 名单同法，gacha_wire.go:1073）；
//     行内容不渲染（hide），字段仅供筛选/排序/详情入口。
//   - max_duplicated_count=statmodel.MaxDuplicatedCount(rare)
//     （★1=5、★2-5=15【推断·社区资料】，与 user_units 读面同口径）。
//   - unit_alert/wait_alert 初值 false（无生产证据，保守常量）。
//   - 33（真阻塞）/343/344（无源）/12 缺模板候选：不在 catalog 即不出现在
//     目录（UnitIDs 不含），如实缺省，不伪造行。
//
// 写面（/albums/wait、/albums/back）本期不开放：cn35_player_units 无
// waiting 列（inventory.go UnitRecord 无该字段），需要迁移支撑，不做
// 无状态伪造成功。
package cn35game

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

// albumDetailPathPrefix 未持有单位图鉴详情端点（album_detail_message 的
// data_path 指向此处；?id=<unit id>）。
const albumDetailPathPrefix = "/albums/album_unit"

// escapePageMessageValue PageMessage 串内嵌值转义：客户端按 &/= 切分
// key=value（MessageRow.cs:304 以 WWW.EscapeURL 同法内嵌路径），服务端
// 以 url.QueryEscape 对齐（?/= & 全部 %XX，客户端 WWW.UnEscapeURL 还原）。
func escapePageMessageValue(v string) string {
	return url.QueryEscape(v)
}

// albumDetailMessage 组装行点击的 album_detail_message。
// 持有行：isUserUnit=true（详情走 user_units FindByKey，无需 data_path）。
// 未持有行：isUserUnit=false + data_path（详情走 album_unit DataLoad）。
func albumDetailMessage(unitID int, isUserUnit bool) string {
	m := fmt.Sprintf(
		"name=AlbumUnitDetail&base=Common/Pages/AlbumUnitDetail&method=Stack&id=%d&isUserUnit=%t&isWaiting=false",
		unitID, isUserUnit)
	if !isUserUnit {
		m += "&data_path=" + escapePageMessageValue(
			fmt.Sprintf("%s?id=%d", albumDetailPathPrefix, unitID))
	}
	return m
}

// buildAlbumRow 组装一条 albums 行：基础数值面复用 buildUnitDoc
// （模板态或库存实例态由调用方决定），叠加图鉴态字段。
func buildAlbumRow(base map[string]any, u *cardcatalog.Unit, owned bool, protected bool, rec UnitRecord) map[string]any {
	// 行点击入口换成 AlbumUnitDetail（user_units 的 unit_detail_message
	// 在图鉴页无消费方，置删避免误导）。
	delete(base, "unit_detail_message")
	if _, ok := base["age"].(int); !ok {
		base["age"] = 999
	}
	base["order_num"] = u.ID
	base["can_evolution_level"] = 0
	base["is_leader_unit"] = false
	base["user_unit_level"] = rec.Level
	base["user_unit_duplicated_count"] = rec.DuplicatedCount
	base["evolution_map"] = map[string]any{"id": 0, "need": map[string]any{"1": 1}}
	base["hide"] = !owned
	// is_user_unit/is_waiting/is_showable：客户端 s() 比较，字符串形态。
	if owned {
		base["is_user_unit"] = "true"
		base["is_showable"] = "true"
		base["protected"] = protected
	} else {
		base["is_user_unit"] = "false"
		base["is_showable"] = "false"
	}
	// 库存无 waiting 态（写面未开放）→ 恒 "false"（back 模式过滤后为空列表）。
	base["is_waiting"] = "false"
	base["unit_alert"] = false
	base["wait_alert"] = false
	base["max_duplicated_count"] = statmodel.MaxDuplicatedCount(u.Rare)
	// waiting_unit_level/waiting_unit_duplicated_count：无待机单位时省略
	// （客户端 s()/i() 缺省 "-"，AlbumUnitDetail.cs:279-296）。
	base["album_detail_message"] = albumDetailMessage(u.ID, owned)
	return base
}

func defaultUserBgOptions() map[string]any {
	return map[string]any{
		"title":         "从拥有的背景中选择",
		"selected_rgba": "0.996,0.996,0.6313,1",
		"options": []any{
			map[string]any{
				"id":              7,
				"order_num":       7,
				"content_type_id": 2,
				"rare_type_id":    10,
				"name":            "阿西尔的小巷(沙漠之国)",
				"title":           "阿西尔的小巷(沙漠之国)",
				"description":     "改变首页和个人主页的背景。",
				"code":            "background_12_2",
				"bgm":             false,
				"ignore_color":    false,
				"asset_id":        "12_2",
				"bg_id":           "12_2",
				"asset_name":      "common_icon/item_7",
				"icon_asset_name": "common_icon/item_7",
				"page_message":    "",
				"prefab":          "",
			},
		},
	}
}

// albumEnumDocs 图鉴页旁路枚举行：与 /user_units/ 的 enumDocs 同形，
// 但覆盖**全目录**（未持有单位的详情页 Enum("country"/"sex"/"weapon")
// 也要命中；持有行子集与 enumDocs 输出一致，缓存覆盖无回归）。
func albumEnumDocs(cat UnitCatalog) map[string]any {
	countryIDs := map[string]int{}
	sexes := map[string]bool{}
	weapons := map[string]bool{}
	for _, id := range cat.UnitIDs() {
		u, err := cat.Unit(id)
		if err != nil {
			continue
		}
		if u.Country != "" {
			countryIDs[u.Country] = u.CountryID
		}
		if u.Gender != nil {
			sexes[strconv.Itoa(*u.Gender)] = true
		}
		if _, ok := weaponTypeName[u.Weapon]; ok {
			weapons[strconv.Itoa(u.Weapon)] = true
		}
	}
	type countryRow struct {
		id   int
		name string
	}
	rows := make([]countryRow, 0, len(countryIDs))
	for name, id := range countryIDs {
		rows = append(rows, countryRow{id, name})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	countryList := []any{}
	for _, r := range rows {
		countryList = append(countryList, map[string]any{"id": r.id, "name": r.name})
	}
	sexList := []any{}
	for _, g := range sortedStrKeys(sexes) {
		gi, _ := strconv.Atoi(g)
		sexList = append(sexList, map[string]any{"id": g, "name": genderName[gi]})
	}
	weaponList := []any{}
	for _, w := range sortedStrKeys(weapons) {
		wi, _ := strconv.Atoi(w)
		weaponList = append(weaponList, map[string]any{"id": w, "name": weaponTypeName[wi]})
	}
	return map[string]any{"country": countryList, "sex": sexList, "weapon": weaponList}
}

// albumDataFor 组装图鉴读面 data 键集合。
// 库存行引用不存在模板 → 明确数据错误（与 inventoryDataFor 同策略）。
func albumDataFor(dep Deps, c *httpapi.Ctx, userID string) (map[string]any, error) {
	owned := map[int]UnitRecord{}
	hideCount, ownedCount := 0, 0
	if dep.Inventory != nil {
		urecs, err := dep.Inventory.Units(userID)
		if err != nil {
			return nil, fmt.Errorf("cn35 图鉴库存读取失败: %w", err)
		}
		for _, r := range urecs {
			owned[r.UnitID] = r
		}
	}
	albums := []any{}
	for _, id := range dep.Catalog.UnitIDs() {
		u, uerr := dep.Catalog.Unit(id)
		if uerr != nil {
			return nil, fmt.Errorf("cn35 图鉴模板缺失 unit_id=%d: %w", id, uerr)
		}
		var base map[string]any
		var derr error
		rec, isOwned := owned[id]
		if isOwned {
			base, derr = buildUnitDoc(rec, u)
		} else {
			// 未持有：Lv1/dup0 模板态（行内容 hide 不渲染，字段供筛选排序）。
			base, derr = buildUnitDoc(UnitRecord{UnitID: id, Level: 1}, u)
		}
		if derr != nil {
			return nil, derr
		}
		prot := false
		if isOwned {
			prot = rec.Protected
			ownedCount++
		} else {
			hideCount++
		}
		albums = append(albums, buildAlbumRow(base, u, isOwned, prot, rec))
	}
	data := map[string]any{
		"albums":          albums,
		"album_data":      map[string]any{"count": ownedCount, "total_count": len(albums)},
		"user_bg_options": defaultUserBgOptions(),
	}
	for k, v := range albumEnumDocs(dep.Catalog) {
		data[k] = v
	}
	c.Decoded = map[string]any{
		"cn35": true, "uid": userID, "endpoint": "albums",
		"total": len(albums), "owned": ownedCount, "hide": hideCount,
	}
	return data, nil
}

// albumsHandler GET|POST /albums/：图鉴全目录读面。
// 装配条件 Inventory+Catalog（cn35game.go）；未装配时 module_requiredata.go
// 空态桩兜底。
func albumsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		data, err := albumDataFor(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// albumUnitDetailHandler GET|POST /albums/album_unit?id=N 与 /albums/show_unit?unit_id=N：
// 未持有单位图鉴详情（AlbumUnitDetail data_path 目标）。数值面与 gacha 名单同法：
// Lv1/dup0 模板态 + 纪念覆盖层（buildUnitDoc 内处理）。
// 非法/未知 id → status:error + modal（客户端 ResponseFilter 弹窗后
// CancelShowPage，不静默伪造空文档）。
func albumUnitDetailHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		raw := firstNonEmpty(
			req.Query.Get("unit_id"), req.Form.Get("unit_id"),
			req.Query.Get("id"), req.Form.Get("id"),
		)
		if raw == "" {
			raw = "152"
		}
		unitID, idErr := strconv.Atoi(raw)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "albums_album_unit",
			"unit_id": unitID,
		}
		if dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配同伴模板目录。"))
		}
		if idErr != nil || unitID <= 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "图鉴详情参数无效，请返回图鉴列表重试。"))
		}
		u, uerr := dep.Catalog.Unit(unitID)
		if uerr != nil {
			return cn35wire.MarshalJSON(trainingError(
				"图鉴不存在", "该单位不在图鉴目录中。"))
		}
		doc, derr := buildUnitDoc(UnitRecord{UnitID: unitID, Level: 1}, u)
		if derr != nil {
			return nil, derr
		}
		delete(doc, "unit_detail_message")
		doc["evolution_map"] = map[string]any{}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"album_unit": doc},
		})
	})
}

const monsterAlbumDetailPathPrefix = "/monster_albums/show_monster"

// monsterAlbumsHandler GET|POST /monster_albums/：魔宠图鉴全目录读面。
func monsterAlbumsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		owned := map[int]float64{}
		if dep.Inventory != nil {
			mrecs, err := dep.Inventory.Monsters(req.UserID)
			if err != nil {
				return nil, fmt.Errorf("cn35 魔宠图鉴库存读取失败: %w", err)
			}
			for _, r := range mrecs {
				_, sc, serr := effectiveScale(r.Scale, r.RealScale)
				if serr != nil {
					sc = 1.0
				}
				if sc > owned[r.MonsterID] {
					owned[r.MonsterID] = sc
				}
			}
		}
		rows := []any{}
		ownedCount, hideCount := 0, 0
		for _, id := range dep.Catalog.MonsterIDs() {
			m, merr := dep.Catalog.Monster(id)
			if merr != nil {
				return nil, fmt.Errorf("cn35 魔宠图鉴模板缺失 monster_id=%d: %w", id, merr)
			}
			maxScale, isOwned := owned[id]
			if !isOwned {
				maxScale = 1.547
				hideCount++
			} else {
				ownedCount++
			}
			name := m.Name
			mt := memorialMonsterText(m.ID)
			if n, ok := mt["name"].(string); ok && n != "" {
				name = n
			}
			skID, _, _ := MonsterSeedSkill(m)
			showable := "false"
			if isOwned {
				showable = "true"
			}
			msg := fmt.Sprintf(
				"name=AlbumMonsterDetail&base=Common/Pages/AlbumMonsterDetail&method=Stack&id=%d&data_path=%s",
				m.ID, escapePageMessageValue(fmt.Sprintf("%s?monster_id=%d", monsterAlbumDetailPathPrefix, m.ID)))
			rows = append(rows, map[string]any{
				"id":                   m.ID,
				"order_num":            m.ID,
				"name":                 name,
				"attr_id":              m.Element,
				"rare_type_id":         m.Rare * 10,
				"rare_type_name":       strings.Repeat("╂", m.Rare),
				"type_id":              m.Skin,
				"hardness_id":          2,
				"seed_skill_id":        skID,
				"max_scale":            maxScale,
				"damage_xys":           []any{},
				"hide":                 !isOwned,
				"is_showable":          showable,
				"album_detail_message": msg,
			})
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "monster_albums",
			"total": len(rows), "owned": ownedCount, "hide": hideCount,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"monster_albums":  rows,
				"user_bg_options": defaultUserBgOptions(),
			},
		})
	})
}

// monsterAlbumDetailHandler GET|POST /monster_albums/show_monster：魔宠图鉴详情读面。
func monsterAlbumDetailHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		raw := firstNonEmpty(
			req.Query.Get("monster_id"), req.Form.Get("monster_id"),
			req.Query.Get("id"), req.Form.Get("id"),
		)
		if raw == "" {
			raw = "159"
		}
		monsterID, idErr := strconv.Atoi(raw)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "monster_albums_show_monster",
			"monster_id": monsterID,
		}
		if dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配魔宠模板目录。"))
		}
		if idErr != nil || monsterID <= 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "魔宠图鉴详情参数无效，请返回图鉴列表重试。"))
		}
		m, merr := dep.Catalog.Monster(monsterID)
		if merr != nil {
			return cn35wire.MarshalJSON(trainingError(
				"图鉴不存在", "该魔宠不在图鉴目录中。"))
		}
		doc, derr := buildMonsterDoc(MonsterRecord{UUID: "album", MonsterID: monsterID, Level: 1}, m)
		if derr != nil {
			return nil, derr
		}
		doc["act_skill_config"] = map[string]any{}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"album_monster": doc},
		})
	})
}
