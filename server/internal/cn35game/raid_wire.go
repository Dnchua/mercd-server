package cn35game

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func raidWireNow(dep Deps) int64 {
	if dep.RaidClock != nil {
		return dep.RaidClock().Unix()
	}
	if dep.GuildClock != nil {
		return dep.GuildClock().Unix()
	}
	return time.Now().Unix()
}

func raidRollFloat(dep Deps) float64 {
	if dep.RaidRand != nil {
		return dep.RaidRand()
	}
	return rand.Float64()
}

func raidBossMeta(dep Deps, monsterID int) RaidMonsterMeta {
	cfg := NormalizeRaidConfig(dep.RaidConfig)
	if monsterID <= 0 {
		monsterID = cfg.BossMonsterID
	}
	meta := RaidMonsterMeta{
		MonsterID: monsterID,
		Name:      "莫鲁菲涅",
		AttrID:    4,
	}
	if dep.Catalog != nil {
		if m, err := dep.Catalog.Monster(monsterID); err == nil && m != nil {
			if m.Name != "" {
				meta.Name = m.Name
			}
			if m.Element >= 1 && m.Element <= 5 {
				meta.AttrID = m.Element
			}
		}
	}
	return meta
}

func raidAttrLabel(attrID int) string {
	switch attrID {
	case 1:
		return "火属性"
	case 2:
		return "水属性"
	case 3:
		return "风属性"
	case 4:
		return "光属性"
	case 5:
		return "暗属性"
	default:
		return "光属性"
	}
}

func raidDifficultyName(diff string) string {
	switch diff {
	case "normal":
		return "急击"
	case "hard":
		return "强击"
	case "very_hard":
		return "猛击"
	default:
		return "急击"
	}
}

func raidAttackMessage(raidID string) string {
	if raidID == "" {
		return ""
	}
	return "name=RaidQuestSelect&type=raid&method=Stack&additional_require_data_path=" +
		url.QueryEscape("/raid_monsters/show?id="+url.QueryEscape(raidID))
}

func raidMemberOption(raidID string) map[string]any {
	return map[string]any{
		"requireData": map[string]any{
			"temp_raid_damage_list": map[string]any{
				"path":         "/raid_monsters/damage_list?id=" + url.QueryEscape(raidID),
				"force_update": true,
			},
		},
	}
}

func raidInstanceDoc(r *RaidInstance, listKind string, now int64) map[string]any {
	if r == nil {
		return map[string]any{}
	}
	expAt := r.ExpiredAt
	if listKind == "history" || r.Status != "active" {
		if r.FinishedAt > 0 && r.FinishedAt < now {
			expAt = r.FinishedAt
		} else {
			expAt = 0
		}
	}
	attacked := r.Attacked
	switch listKind {
	case "assist":
		attacked = r.ViewerAttacked
	case "history":
		attacked = true
	}
	return map[string]any{
		"id":             r.RaidID,
		"level":          r.Level,
		"point":          r.CurrentPoint,
		"max_point":      r.MaxPoint,
		"initial_power":  r.InitialPower,
		"expired_at":     expAt,
		"attacked":       attacked,
		"help_guild":     r.HelpGuild,
		"help_community": r.HelpCommunity,
		"is_defeated":    r.Status == "defeated",
		"user_id":        r.OwnerUserID,
		"user_name":      r.OwnerName,
		"icon_name":      fmt.Sprintf("monster/monster_square_%d", r.MonsterID),
		"member_option":  raidMemberOption(r.RaidID),
		"attack_message": raidAttackMessage(r.RaidID),
		"monster": map[string]any{
			"id":      r.MonsterID,
			"name":    r.MonsterName,
			"attr_id": r.AttrID,
		},
	}
}

// raidAppearDoc 构建 /quests/result 触发暴走强袭弹窗（RaidAppearModal.cs）的 data.raid_monster 文档。
func raidAppearDoc(r *RaidInstance, now int64) map[string]any {
	doc := raidInstanceDoc(r, "mine", now)
	if r == nil {
		return doc
	}
	doc["id"] = r.RaidID
	doc["message"] = fmt.Sprintf("Lv.%d %s 突然出现！\n是否立即前往击退？", r.Level, r.MonsterName)
	doc["giveup_confirm"] = "确定要放走当前遭遇的暴走魔宠吗？\n放走后将无法获得击退奖励。"
	doc["next_page_message"] = "name=Raid&base=Common/Pages/Raid&method=Stack"
	return doc
}

func buildRaidOverviewData(ov *RaidOverview, now int64) map[string]any {
	if ov == nil {
		ov = &RaidOverview{Profile: RaidProfile{RaidLv: 1}}
	}
	myDoc := map[string]any{}
	hasRaidInt := 0
	attackMsg := ""
	if ov.MyRaid != nil && ov.MyRaid.Status == "active" && ov.MyRaid.ExpiredAt > now {
		myDoc = raidInstanceDoc(ov.MyRaid, "mine", now)
		hasRaidInt = 1
		attackMsg = raidAttackMessage(ov.MyRaid.RaidID)
	}
	toList := func(in []RaidInstance, listKind string) []any {
		out := make([]any, 0, len(in))
		for i := range in {
			cp := in[i]
			out = append(out, raidInstanceDoc(&cp, listKind, now))
		}
		return out
	}
	condDoc := map[string]any{
		"lv":                  ov.Profile.RaidLv,
		"pt":                  ov.Profile.RaidPt,
		"has_raid":            hasRaidInt,
		"no_raid_label":       "通关探险关卡即有机会遭遇暴走魔宠！",
		"raid_attack_message": attackMsg,
	}
	return map[string]any{
		"raid_monsters":                myDoc,
		"raid_condition":               condDoc,
		"my_raid_monster":              myDoc,
		"guild_help_raid_monsters":     toList(ov.GuildHelps, "assist"),
		"community_help_raid_monsters": toList(ov.CommunityHelp, "assist"),
		"my_history_raid_monsters":     toList(ov.MyHistory, "history"),
	}
}

func raidDifficultyOptionsDoc(assistFirstHit bool) map[string]any {
	out := map[string]any{}
	for _, diff := range []string{"normal", "hard", "very_hard"} {
		t, cost, _ := RaidDifficultySpec(diff, assistFirstHit)
		out[diff] = map[string]any{
			"time_limit": strconv.Itoa(t),
			"cost":       strconv.Itoa(cost),
		}
	}
	return out
}

func raidPtBonusRowsDoc(dep Deps, pt int64, used map[string]int) []any {
	meta := raidBossMeta(dep, 0)
	goods := RaidPtShopCatalog(meta.MonsterID, meta.Name)
	rows := make([]any, 0, (len(goods)+1)/2)
	fillSlot := func(dst map[string]any, suffix string, g RaidPtShopGood) {
		rem := g.Limit - used[g.SlotID]
		if rem < 0 {
			rem = 0
		}
		afford := int(pt / int64(g.Price))
		maxBuy := rem
		if afford < maxBuy {
			maxBuy = afford
		}
		disabled := rem <= 0 || pt < int64(g.Price)
		btnLabel := "交换"
		if rem <= 0 {
			btnLabel = "已换完"
		} else if pt < int64(g.Price) {
			btnLabel = "pt不足"
		}
		if maxBuy < 1 {
			maxBuy = 1
		}
		dst["icon_asset_name"+suffix] = g.IconAssetName
		dst["name"+suffix] = g.Name
		dst["description"+suffix] = fmt.Sprintf("必要pt: %d", g.Price)
		dst["pt_str"+suffix] = fmt.Sprintf("必要pt: %d", g.Price)
		dst["button_label"+suffix] = btnLabel
		dst["exchange_button_disable"+suffix] = disabled
		dst["rest_exchange_text"+suffix] = fmt.Sprintf("还可交换 %d 次", rem)
		dst["count"+suffix] = maxBuy
		dst["price"+suffix] = g.Price
		dst["confirm_text"+suffix] = fmt.Sprintf("使用 {0} 击退pt交换 %s 吗？\n（当前最多可交换 {1} 次）", g.Name)
		dst["exchange_url"+suffix] = "/raid_monsters/exchange?slot_id=" + url.QueryEscape(g.SlotID) + "&count="
	}
	for i := 0; i < len(goods); i += 2 {
		row := map[string]any{}
		fillSlot(row, "_0", goods[i])
		if i+1 < len(goods) {
			fillSlot(row, "_1", goods[i+1])
		} else {
			row["icon_asset_name_1"] = ""
			row["name_1"] = ""
			row["description_1"] = ""
			row["pt_str_1"] = ""
			row["button_label_1"] = "-"
			row["exchange_button_disable_1"] = true
			row["rest_exchange_text_1"] = ""
			row["count_1"] = 0
			row["price_1"] = 0
			row["confirm_text_1"] = ""
			row["exchange_url_1"] = ""
		}
		rows = append(rows, row)
	}
	return rows
}

func raidHelpMessageDoc(m RaidHelpMessage, now int64) map[string]any {
	leaderID := m.LeaderUnitID
	if leaderID <= 0 {
		leaderID = 152
	}
	createdAt := m.CreatedAt
	if createdAt <= 0 {
		createdAt = now
	}
	return map[string]any{
		"id":              m.ID,
		"type_id":         m.Category,
		"text":            fmt.Sprintf("【暴走协力】Lv.%d %s 出现，请求支援！", m.Level, m.MonsterName),
		"user_id":         m.OwnerUserID,
		"user_name":       m.OwnerName,
		"user_short_name": m.OwnerName,
		"unit_id":         strconv.Itoa(leaderID),
		"raid_id":         m.RaidID,
		"addition":        guildComma(m.CurrentPoint),
		"created_at":      createdAt,
		"helped_at":       0,
		"help_id":         "",
		"small_text":      "",
		"stamp":           "",
		"private":         false,
		"team_id":         "",
		"entry_url":       "",
	}
}

// MergeRaidHelpMessages 将活跃暴走协力求援消息合并进 data.messages 字典。
func MergeRaidHelpMessages(dst map[string]any, list []RaidHelpMessage, now int64) {
	if dst == nil {
		return
	}
	if _, ok := dst["community_raid_call"]; !ok {
		dst["community_raid_call"] = []any{}
	}
	appendDoc := func(cat string, item map[string]any) {
		cur, _ := dst[cat].([]any)
		dst[cat] = append(cur, item)
	}
	for _, m := range list {
		doc := raidHelpMessageDoc(m, now)
		appendDoc(m.Category, doc)
		if m.Category == "community_comment" {
			appendDoc("community_raid_call", doc)
		}
	}
}

func raidModal(title, body string) map[string]any {
	return map[string]any{
		"modal": map[string]any{
			"title": title,
			"body":  body,
		},
	}
}

func raidErrModal(err error) map[string]any {
	switch {
	case errors.Is(err, ErrGWBP):
		return raidModal("BP不足", "BP不足，无法出击")
	case errors.Is(err, ErrRaidExpired):
		return raidModal("暴走已结束", "该暴走魔宠已超过时间期限并撤离")
	case errors.Is(err, ErrRaidAlreadyDone):
		return raidModal("暴走已结束", "该暴走魔宠已被击退或放走")
	case errors.Is(err, ErrRaidNotFound):
		return raidModal("无法操作", "未找到对应的暴走魔宠")
	case errors.Is(err, ErrRaidNotAttacked):
		return raidModal("无法求援", "请先亲自出击一次后再发送协力请求")
	case errors.Is(err, ErrRaidNoGuild):
		return raidModal("无法求援", "加入公会后才能向公会发送协力请求")
	case errors.Is(err, ErrRaidNotOwner):
		return raidModal("无法操作", "只能放走自己发现的暴走魔宠")
	case errors.Is(err, ErrRaidSoldOut):
		return raidModal("无法交换", "该商品已达交换次数上限")
	case errors.Is(err, ErrRaidNoPt):
		return raidModal("无法交换", "击退pt不足")
	case errors.Is(err, ErrRaidForbidden):
		return raidModal("无法操作", "该暴走魔宠尚未向您所在的公会或聊天室请求协力")
	case errors.Is(err, ErrDeckEmpty):
		return raidModal("无法战斗", "编队为空，无法开始战斗")
	case errors.Is(err, ErrDeckNotFound):
		return raidModal("无法战斗", "编队编号无效")
	default:
		return raidModal("无法操作", err.Error())
	}
}

func buildRaidExecuteDoc(dep Deps, sess *RaidBattleSession, inst *RaidInstance,
	units []UnitRecord, seeds []MonsterRecord, snap *PlayerSnapshot) (map[string]any, error) {
	cat, err := loadMergedCatalog(dep)
	if err != nil {
		return nil, err
	}
	fac := GuildFacilityLevelsForUser(dep, sess.UserID)

	skillsByUnit, err := skillsByUnitFor(dep, sess.UserID)
	if err != nil {
		return nil, err
	}
	fateEntries := fateEntriesFor(dep, sess.UserID)
	unitDocs := make([]any, 0, len(units))
	for _, r := range units {
		cu, err := dep.Catalog.Unit(r.UnitID)
		if err != nil {
			return nil, err
		}
		udoc, err := buildUnitDoc(r, cu)
		if err != nil {
			return nil, err
		}
		baseHP, _ := udoc["hp"].(int)
		baseAT, _ := udoc["at"].(int)
		applyUnitSkills(udoc, cu.Rare, r.DuplicatedCount, skillsByUnit[r.UnitID])
		ApplyUnitFateBuffs(udoc, cu.Element, fateEntries)
		ApplyUnitFacilityBuff(udoc, baseHP, baseAT, fac.HP, fac.AT)
		unitDocs = append(unitDocs, udoc)
	}

	var bossCat *cardcatalog.Monster
	if dep.Catalog != nil {
		bossCat, _ = dep.Catalog.Monster(inst.MonsterID)
	}
	var bossDoc map[string]any
	if bossCat != nil {
		bossDoc = BattleMonsterDoc(bossCat)
	} else {
		bossDoc = map[string]any{
			"id":          inst.MonsterID,
			"name":        inst.MonsterName,
			"attr_id":     inst.AttrID,
			"asset_name":  fmt.Sprintf("monster/monster_%d", inst.MonsterID),
			"skill_level": 1,
		}
	}
	monsters := map[string]any{
		strconv.Itoa(inst.MonsterID): bossDoc,
	}

	safeHP := RaidBattleSafeHP(inst.CurrentPoint)
	bossAtk := 1200 + inst.Level*40
	bossAppearTable := map[string]any{
		"1": []any{
			map[string]any{
				"id":              inst.MonsterID,
				"interval_sec":    0,
				"gauge":           true,
				"calc_damage_key": "raid",
				"current_hp":      safeHP,
				"params": map[string]any{
					"hp":   safeHP,
					"life": safeHP,
					"at":   bossAtk,
					"size": 1.35,
				},
			},
		},
	}

	rates := RaidWeaponRates(inst.Level)
	rateHash := map[string]any{
		"normal":    rates,
		"hard":      rates,
		"very_hard": rates,
	}
	initPower := inst.InitialPower + fac.InitialSPBonus()
	if initPower < 1000 {
		initPower = 1000
	}
	questDoc := map[string]any{
		"id":               inst.RaidID,
		"name":             fmt.Sprintf("%s Lv.%d", inst.MonsterName, inst.Level),
		"mode":             "raid",
		"use_ap":           sess.BPCost,
		"difficulty_id":    sess.DifficultyID,
		"difficulty_name":  raidDifficultyName(sess.DifficultyID),
		"background_id":    1,
		"initial_power":    initPower,
		"unit_count_limit": 5,
		"seed_atack_rate":  1.0,
		"unit_help_rate":   1.0,
		"continuable":      false,
		"wave_conf": map[string]any{
			"1": map[string]any{
				"length":     850,
				"time_limit": sess.TimeLimit,
			},
		},
		"background":       cat.Defaults.Background,
		"powerup_conf":     cat.Defaults.PowerupConf,
		"weapon_rate_hash": rateHash,
	}
	for _, w := range gwWeaponRateNames {
		questDoc[w+"_rate"] = rates[w]
	}

	assets := append([]string{}, cat.Defaults.AssetNamesCommon...)
	declared := map[string]bool{}
	for _, a := range assets {
		declared[a] = true
	}
	addAsset := func(a string) {
		if a == "" || declared[a] {
			return
		}
		declared[a] = true
		assets = append(assets, a)
	}
	for _, r := range units {
		addAsset(fmt.Sprintf("unit/unit_%d", r.UnitID))
		addAsset(fmt.Sprintf("unit/unit_btn_%d", r.UnitID))
		addAsset(fmt.Sprintf("unit/unit_large_%d", r.UnitID))
	}
	for _, ud := range unitDocs {
		udoc, _ := ud.(map[string]any)
		for _, k := range []string{"attack_effect", "target_effect"} {
			if e, _ := udoc[k].(string); e != "" {
				addAsset(e)
			}
		}
	}
	addAsset(fmt.Sprintf("monster/monster_%d", inst.MonsterID))
	addAsset(fmt.Sprintf("monster/monster_btn_%d", inst.MonsterID))
	addAsset(fmt.Sprintf("monster/monster_large_ns_%d", inst.MonsterID))
	for _, m := range seeds {
		addAsset(fmt.Sprintf("monster/monster_btn_%d", m.MonsterID))
		addAsset(fmt.Sprintf("monster/monster_large_ns_%d", m.MonsterID))
	}
	addAsset("background/background1_1")
	addAsset(gwBattleBGM)

	qBid := "?bid=" + url.QueryEscape(sess.BattleID)
	helpCallable := inst.OwnerUserID == sess.UserID && (!inst.HelpGuild || !inst.HelpCommunity)

	doc := map[string]any{
		"user":              snap.User,
		"quest":             questDoc,
		"units":             unitDocs,
		"monsters":          monsters,
		"main_character":    CloneMainCharacterWithBuff(cat.Defaults.MainCharacter, fac.MainHPBonus()),
		"appear_table":      map[string]any{"1": map[string]any{}},
		"boss_appear_table": bossAppearTable,
		"help_table":        map[string]any{"1": map[string]any{}},
		"timezone_table":    map[string]any{"1": 1},
		"asset_names":       assets,
		"bgm_name":          gwBattleBGM,
		"exp":               0,
		"clear_count":       0,
		"flag_image_name":   gwFlagImageName,
		"rush_started":      nil,
		"rush_ended":        nil,
		"current_hp":        safeHP,
		"help_callable":     helpCallable,
		"ap_use_url":        "/raid_monsters/bp_use" + qBid,
		"result_url":        "/raid_monsters/result" + qBid,
		"lose_url":          "/raid_monsters/lose" + qBid,
		"next_page_message": "name=Raid&base=Common/Pages/Raid",
		"battle_log":        false,
		"data": map[string]any{
			"user": snap.User,
		},
	}
	if hb := fac.HelpLevelBase(); hb > 0 {
		doc["guild_asset_help_level_base"] = hb
	}
	return doc, nil
}

func raidPostSettleData(dep Deps, c *httpapi.Ctx, userID string, includeRewards bool, now int64) (map[string]any, error) {
	snap, err := dep.Accounts.Snapshot(userID)
	if err != nil {
		return nil, err
	}
	ov, err := dep.Raid.RaidOverview(userID, now)
	if err != nil {
		return nil, err
	}
	data := buildRaidOverviewData(ov, now)
	data["user"] = snap.User
	if includeRewards {
		if dep.Items != nil {
			if itemsData, ierr := userItemsData(dep, userID); ierr == nil {
				for k, v := range itemsData {
					data[k] = v
				}
			}
		}
		if dep.Inventory != nil && dep.Catalog != nil {
			if inv, ierr := inventoryDataFor(dep, c, userID); ierr == nil {
				data["user_monsters"] = inv["user_monsters"]
				data["user_units"] = inv["user_units"]
			}
		}
	}
	return data, nil
}

func parseRaidDamage(q url.Values) int64 {
	damages := parseDamageParams(q)
	if v, ok := damages["raid"]; ok && v > 0 {
		return v
	}
	if raw := strings.TrimSpace(q.Get("damage")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
		if f, err := strconv.ParseFloat(raw, 64); err == nil && f > 0 {
			return int64(f)
		}
	}
	return 0
}

func registerRaidRoutes(register func(method, path, note string, handler httpapi.Handler), dep Deps) {
	regBoth := func(path, note string, h httpapi.Handler) {
		register(http.MethodGet, path, note, h)
		register(http.MethodPost, path, note+" POST 形态", h)
	}
	regBoth("/raid_monsters/", "暴走魔宠主页读面（raid_condition/my_raid_monster/协力列表/日志）", raidIndexHandler(dep))
	regBoth("/raid_monsters/show", "暴走魔宠出击准备页（temp_raid_monster/battle_weapon_rate）", raidShowHandler(dep))
	regBoth("/raid_monsters/execute.json", "暴走魔宠出击开战（三档 BP/武器特攻/设施加成/单波 Boss）", raidExecuteHandler(dep))
	regBoth("/raid_monsters/bp_use", "暴走魔宠开战 BP 扣费", raidBPUseHandler(dep))
	regBoth("/raid_monsters/lose", "暴走魔宠限时/败北伤害结算（damage[raid]）", raidLoseHandler(dep))
	regBoth("/raid_monsters/result", "暴走魔宠击退胜利结算", raidResultHandler(dep))
	regBoth("/raid_monsters/call_help", "暴走魔宠发送协力求援（guild/community_topic）", raidCallHelpHandler(dep))
	regBoth("/raid_monsters/giveup", "暴走魔宠放走确认/执行", raidGiveupHandler(dep))
	regBoth("/raid_monsters/damage_list", "暴走魔宠协力者伤害排行", raidDamageListHandler(dep))
	regBoth("/raid_monsters/lv_bonus", "暴走魔宠击退 Lv 奖励表", raidLvBonusHandler(dep))
	regBoth("/raid_monsters/pt_bonus", "暴走魔宠击退 pt 交换所商品表", raidPtBonusHandler(dep))
	regBoth("/raid_monsters/exchange", "暴走魔宠击退 pt 交换所兑换写面", raidExchangeHandler(dep))
}

func raidIndexHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := raidWireNow(dep)
		ov, err := dep.Raid.RaidOverview(req.UserID, now)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "raid_monsters"}
		return cn35wire.MarshalJSON(map[string]any{
			"data": buildRaidOverviewData(ov, now),
		})
	})
}

func raidShowHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := raidWireNow(dep)
		raidID := strings.TrimSpace(req.Query.Get("id"))
		if raidID == "" {
			if ov, err := dep.Raid.RaidOverview(req.UserID, now); err == nil && ov.MyRaid != nil {
				raidID = ov.MyRaid.RaidID
			}
		}
		if raidID == "" {
			return cn35wire.MarshalJSON(raidErrModal(ErrRaidNotFound))
		}
		det, err := dep.Raid.RaidShow(req.UserID, raidID, now)
		if err != nil {
			return cn35wire.MarshalJSON(raidErrModal(err))
		}
		r := det.Instance
		tempDoc := map[string]any{
			"id":                 r.RaidID,
			"header_str":         fmt.Sprintf("%s(%s Lv%d)", r.MonsterName, raidAttrLabel(r.AttrID), r.Level),
			"user_name":          r.OwnerName,
			"level":              r.Level,
			"point":              r.CurrentPoint,
			"max_point":          r.MaxPoint,
			"initial_power":      r.InitialPower,
			"expired_at":         r.ExpiredAt,
			"member_option":      raidMemberOption(r.RaidID),
			"monster":            map[string]any{"id": r.MonsterID, "name": r.MonsterName, "attr_id": r.AttrID},
			"difficulty_options": raidDifficultyOptionsDoc(det.AssistFirstHit),
		}
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "raid_show", "raid_id": r.RaidID}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"temp_raid_monster":  tempDoc,
				"battle_weapon_rate": RaidWeaponRates(r.Level),
			},
		})
	})
}

func raidExecuteHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := raidWireNow(dep)
		q := req.Query
		raidID := strings.TrimSpace(q.Get("id"))
		if raidID == "" {
			if ov, err := dep.Raid.RaidOverview(req.UserID, now); err == nil && ov.MyRaid != nil {
				raidID = ov.MyRaid.RaidID
			}
		}
		diff := strings.TrimSpace(q.Get("difficulty_id"))
		if diff == "" {
			diff = "normal"
		}
		partyRaw := strings.TrimSpace(q.Get("party_id"))
		if partyRaw == "" {
			partyRaw = "001"
		}
		ordinal, err := ParsePartyOrdinal(partyRaw)
		if err != nil {
			return cn35wire.MarshalJSON(raidModal("无法战斗", "编队编号无效"))
		}
		sess, inst, units, seeds, err := dep.Raid.RaidOpenBattle(req.UserID, raidID, diff, ordinal, 5, now)
		if err != nil {
			return cn35wire.MarshalJSON(raidErrModal(err))
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		doc, err := buildRaidExecuteDoc(dep, sess, inst, units, seeds, snap)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "raid_execute",
			"raid_id": inst.RaidID, "battle_id": sess.BattleID, "bp_cost": sess.BPCost,
		}
		return cn35wire.MarshalJSON(doc)
	})
}

func raidBPUseHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := raidWireNow(dep)
		bid := strings.TrimSpace(req.Query.Get("bid"))
		sess, err := dep.Raid.RaidPayBP(req.UserID, bid, now)
		if err != nil {
			return cn35wire.MarshalJSON(raidErrModal(err))
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "raid_bp_use", "battle_id": sess.BattleID}
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   map[string]any{"user": snap.User},
		})
	})
}

func raidLoseHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := raidWireNow(dep)
		bid := strings.TrimSpace(req.Query.Get("bid"))
		dmg := parseRaidDamage(req.Query)
		meta := raidBossMeta(dep, 0)
		res, err := dep.Raid.RaidReportLose(req.UserID, bid, dmg, meta, now)
		if err != nil {
			return cn35wire.MarshalJSON(raidErrModal(err))
		}
		data, err := raidPostSettleData(dep, c, req.UserID, res.Defeated, now)
		if err != nil {
			return nil, err
		}
		status := "success"
		if res.Already {
			status = "already_received"
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "raid_lose",
			"battle_id": bid, "damage": res.DamageDealt, "defeated": res.Defeated,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status": status,
			"data":   data,
		})
	})
}

func raidResultHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := raidWireNow(dep)
		bid := strings.TrimSpace(req.Query.Get("bid"))
		meta := raidBossMeta(dep, 0)
		res, err := dep.Raid.RaidReportWin(req.UserID, bid, meta, now)
		if err != nil {
			return cn35wire.MarshalJSON(raidErrModal(err))
		}
		data, err := raidPostSettleData(dep, c, req.UserID, res.Defeated, now)
		if err != nil {
			return nil, err
		}
		status := "success"
		if res.Already {
			status = "already_received"
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "raid_result",
			"battle_id": bid, "defeated": res.Defeated, "cleared_lv": res.ClearedLv, "pt_earned": res.PtEarnedSelf,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status":            status,
			"data":              data,
			"next_page_message": "name=Raid&base=Common/Pages/Raid",
		})
	})
}

func raidCallHelpHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := raidWireNow(dep)
		helpType := strings.TrimSpace(req.Query.Get("raid_help_type"))
		cfg := NormalizeRaidConfig(dep.RaidConfig)
		meta := raidBossMeta(dep, cfg.BossMonsterID)
		inst, err := dep.Raid.RaidCallHelp(req.UserID, helpType, cfg.SoloAssist, meta, now)
		if err != nil {
			return cn35wire.MarshalJSON(raidErrModal(err))
		}
		ov, err := dep.Raid.RaidOverview(req.UserID, now)
		if err != nil {
			return nil, err
		}
		data := buildRaidOverviewData(ov, now)
		messages := make(map[string]any, len(AllMessageCategories())+1)
		for _, cat := range AllMessageCategories() {
			messages[cat] = []any{}
		}
		messages["community_raid_call"] = []any{}
		if helpMsgs, herr := dep.Raid.RaidHelpMessages(req.UserID, now); herr == nil {
			MergeRaidHelpMessages(messages, helpMsgs, now)
		}
		data["messages"] = messages
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "raid_call_help",
			"raid_id": inst.RaidID, "help_type": helpType,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"success": true,
			"data":    data,
			"modal": map[string]any{
				"title": "协力请求",
				"body":  "已发送暴走魔宠协力求援！",
			},
		})
	})
}

func raidGiveupHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := raidWireNow(dep)
		raidID := strings.TrimSpace(req.Query.Get("id"))
		if raidID == "" {
			if ov, err := dep.Raid.RaidOverview(req.UserID, now); err == nil && ov.MyRaid != nil {
				raidID = ov.MyRaid.RaidID
			}
		}
		if req.Query.Get("confirmed") != "true" {
			if raidID == "" {
				return cn35wire.MarshalJSON(raidErrModal(ErrRaidNotFound))
			}
			return cn35wire.MarshalJSON(map[string]any{
				"modal": map[string]any{
					"type":  "yes_no",
					"title": "放走暴走魔宠",
					"body":  "确定要放走当前遭遇的暴走魔宠吗？\n放走后将无法获得击退奖励。",
					"url":   "/raid_monsters/giveup?confirmed=true&id=" + url.QueryEscape(raidID),
				},
			})
		}
		if err := dep.Raid.RaidGiveup(req.UserID, raidID, now); err != nil {
			return cn35wire.MarshalJSON(raidErrModal(err))
		}
		ov, err := dep.Raid.RaidOverview(req.UserID, now)
		if err != nil {
			return nil, err
		}
		resp := map[string]any{
			"status": "success",
			"data":   buildRaidOverviewData(ov, now),
		}
		if req.Query.Get("no_page_message") != "true" {
			resp["page_message"] = "name=Raid&base=Common/Pages/Raid"
		}
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "raid_giveup", "raid_id": raidID}
		return cn35wire.MarshalJSON(resp)
	})
}

func raidDamageListHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := raidWireNow(dep)
		raidID := strings.TrimSpace(req.Query.Get("id"))
		if raidID == "" {
			if ov, err := dep.Raid.RaidOverview(req.UserID, now); err == nil && ov.MyRaid != nil {
				raidID = ov.MyRaid.RaidID
			}
		}
		if raidID == "" {
			return cn35wire.MarshalJSON(map[string]any{
				"data": map[string]any{"temp_raid_damage_list": []any{}},
			})
		}
		rows, err := dep.Raid.RaidDamageList(req.UserID, raidID, now)
		if err != nil {
			if errors.Is(err, ErrRaidForbidden) || errors.Is(err, ErrRaidNotFound) {
				return cn35wire.MarshalJSON(map[string]any{
					"data": map[string]any{"temp_raid_damage_list": []any{}},
				})
			}
			return nil, err
		}
		list := make([]any, 0, len(rows))
		for i, row := range rows {
			guildName := row.GuildName
			if guildName == "" {
				guildName = "-"
			}
			leaderID := row.LeaderUnitID
			if leaderID <= 0 {
				leaderID = 152
			}
			detailMsg := ""
			if !row.IsNPC && !strings.HasPrefix(row.UserID, "npc_helper_") {
				detailMsg = socialUserDetailMessage(row.UserID, true)
			}
			list = append(list, map[string]any{
				"rank":                   fmt.Sprintf("%d位", i+1),
				"name":                   row.UserName,
				"damage_str":             fmt.Sprintf("伤害 %s", guildComma(row.TotalDamage)),
				"status":                 fmt.Sprintf("%d\n%s", row.UserLevel, guildName),
				"square_icon_asset_name": fmt.Sprintf("unit/unit_square_%d", leaderID),
				"user_detail_message":    detailMsg,
			})
		}
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "raid_damage_list", "count": len(list)}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"temp_raid_damage_list": list,
			},
		})
	})
}

func raidLvBonusHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		lv, claimed, err := dep.Raid.RaidLvClaims(req.UserID)
		if err != nil {
			return nil, err
		}
		meta := raidBossMeta(dep, 0)
		catalog := RaidLvBonusCatalog(meta.MonsterID, meta.Name)
		list := make([]any, 0, len(catalog))
		for _, r := range catalog {
			overlay := ""
			if claimed[r.TargetLv] || lv > r.TargetLv {
				overlay = "common/chara_clear"
			}
			list = append(list, map[string]any{
				"name":               fmt.Sprintf("击退Lv.%d 达成奖励", r.TargetLv),
				"difficulty":         "",
				"title":              fmt.Sprintf("%s × %d", r.RewardName, r.Amount),
				"icon_asset_name":    r.IconAssetName,
				"overlay_asset_name": overlay,
			})
		}
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "raid_lv_bonus", "raid_lv": lv}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"raid_lv_bonus": list,
			},
		})
	})
}

func raidPtBonusHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		pt, used, err := dep.Raid.RaidPtShopState(req.UserID)
		if err != nil {
			return nil, err
		}
		rows := raidPtBonusRowsDoc(dep, pt, used)
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "raid_pt_bonus", "pt": pt}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"raid_pt_bonus": rows,
			},
		})
	})
}

func raidExchangeHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := raidWireNow(dep)
		slotID := strings.TrimSpace(req.Query.Get("slot_id"))
		count := 1
		if raw := strings.TrimSpace(req.Query.Get("count")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				count = n
			}
		}
		meta := raidBossMeta(dep, 0)
		var matched RaidPtShopGood
		for _, g := range RaidPtShopCatalog(meta.MonsterID, meta.Name) {
			if g.SlotID == slotID {
				matched = g
				break
			}
		}
		if matched.SlotID == "" {
			return cn35wire.MarshalJSON(raidErrModal(ErrRaidBadSlot))
		}
		newPt, newUsed, err := dep.Raid.RaidExchangePt(req.UserID, slotID, count, matched, now)
		if err != nil {
			return cn35wire.MarshalJSON(raidErrModal(err))
		}
		data, err := raidPostSettleData(dep, c, req.UserID, true, now)
		if err != nil {
			return nil, err
		}
		_, usedMap, err := dep.Raid.RaidPtShopState(req.UserID)
		if err == nil {
			data["raid_pt_bonus"] = raidPtBonusRowsDoc(dep, newPt, usedMap)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "raid_exchange",
			"slot_id": slotID, "count": count, "new_pt": newPt, "used": newUsed,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
			"modal": map[string]any{
				"title": "交换完成",
				"body":  fmt.Sprintf("已交换 %s × %d！", matched.Name, count),
			},
		})
	})
}
