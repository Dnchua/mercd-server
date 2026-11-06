// battle_wire.go STORY350-07：正式剧情战斗全链处理器。
//
// 端点（契约见 story350-06 evidence/endpoint-seq.md + field-matrix.md）：
//
//	POST /quests/execute/<id>.json   execute 下发（前缀路由；_method=GET）
//	GET  /quests/use_ap?bid=         AP 扣点（客户端 3s 重试至 success）
//	GET  /quests/result?bid=         结算（10s 重试至 success/already_received）
//	POST /quests/lose?bid=           败北上报（mode=quest 不读响应体）
//	GET  /quests/continue            1 钻续战（零参数，最近 lost 会话）
//	POST /quests/log_battle          battle_record 归档（fire-and-forget）
//
// 校验失败一律回 200 + modal 键（客户端 ResponseFilter 弹窗并回退关卡
// 选择页），不裸 4xx——裸错误客户端会当作通信故障重试。
// bid 伪造/跨账号 → ErrBattleNotFound → status:"error"（客户端进入其
// 重试循环即拒绝语义；合法客户端不会走到）。
package cn35game

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// battleAPTTLSeconds 滞留会话（prepared/ap_paid 未完结）的回收窗口；
// 超窗会话在下次 execute 同事务被 abandoned（懒清扫，不起后台任务）。
const battleAPTTLSeconds = 24 * 3600

// questExecutePath 匹配 /quests/execute/<数字id>.json（前缀路由内自取 id）。
var questExecutePath = regexp.MustCompile(`^/quests/execute/(\d+)\.json$`)

// questModal 校验失败响应：客户端 ResponseFilter 弹 OK 框并回退。
// 仅用已证字段（title/body/current_page_close_and_back，Common.cs:2103+）。
func questModal(title, body string) map[string]any {
	return map[string]any{"modal": map[string]any{
		"type": "ok", "title": title, "body": body,
		"current_page_close_and_back": true,
	}}
}

// questNoAPModal AP 不足响应：下发原生 modal.type="no_ap"（Common.cs:2112-2116、
// NoApModal.cs），呼出客户端内置 AP 回复确认框（元气之水 / 钻石双按钮）。
func questNoAPModal(dep Deps, userID string, fromMessage bool) map[string]any {
	itemStore := dep.Items
	if itemStore == nil {
		if is, ok := dep.Accounts.(ItemStore); ok {
			itemStore = is
		}
	}
	var potionCount int64
	if itemStore != nil {
		if items, err := itemStore.PlayerItems(userID); err == nil {
			for _, pi := range items {
				if pi.ItemKey == "ap_potion" && pi.Quantity > 0 {
					potionCount = int64(pi.Quantity)
					break
				}
			}
		}
	}
	var coinAmount int64
	data := map[string]any{}
	if dep.Accounts != nil {
		if snap, err := dep.Accounts.Snapshot(userID); err == nil && snap != nil {
			numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
			data["user"] = snap.User
			data["user_coin"] = snap.UserCoin
			if snap.UserCoin != nil {
				coinAmount = int64(numOf(snap.UserCoin["amount"]))
			}
		}
	}
	out := map[string]any{
		"modal": map[string]any{
			"type":  "no_ap",
			"title": "体力不足",
			"body":  "AP 不足，无法开始战斗。",
			"no_ap": map[string]any{
				"from_message": fromMessage,
				"item":         potionCount,
				"coin":         coinAmount,
				"url": map[string]any{
					"item": "/user_items/use/?id=201&count=1",
					"coin": "/user_items/use/?id=coin_ap&count=1",
				},
			},
		},
	}
	if len(data) > 0 {
		out["data"] = data
	}
	return out
}

// usersCheckAPHandler /users/check_ap：消息栏 AP 回复按钮（Messages.cs:746 ApHealButton）
// 触发的原生 NoApModal 弹窗。
func usersCheckAPHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "users_check_ap",
		}
		return cn35wire.MarshalJSON(questNoAPModal(dep, req.UserID, true))
	})
}

// statusDoc 组装 {"status":...} 简表响应。
func statusDoc(status string) map[string]any {
	return map[string]any{"status": status}
}

// difficultyIndex difficulty_id → difficulty_names 的索引键（"0/1/2"）。
func difficultyIndex(diff string) string {
	switch diff {
	case "normal":
		return "0"
	case "hard":
		return "1"
	case "very_hard":
		return "2"
	}
	return "0"
}

// derefBool 可空布尔字段取默认。
func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// newBattleID 生成不可猜会话 token（URL 下发，绑定账号）。
func newBattleID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "bq" + hex.EncodeToString(b[:])
}

// ---- execute ----

// questExecuteHandler /quests/execute/<id>.json：全量校验 → 建档 → 下发
// 战斗文档。响应形状以 quest0.json 为 oracle，动态段（三 URL/user/units/
// appear_table/clear_count）由会话与账号状态注入。
func questExecuteHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		matched := questExecutePath.FindStringSubmatch(c.R.URL.Path)
		if matched == nil {
			return nil, fmt.Errorf("execute 路径非法: %s", c.R.URL.Path)
		}
		pathID, _ := strconv.Atoi(matched[1])
		q := req.Query
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "quest_execute",
			"quest_id": pathID, "mode": q.Get("mode"),
			"difficulty_id": q.Get("difficulty_id"), "party_id": q.Get("party_id"),
		}
		// 校验链：mode → id 一致 → 关卡存在 → 难度 → 解锁 → 编队 → 事务内 AP。
		if mode := q.Get("mode"); mode != "" && mode != "quest" {
			return cn35wire.MarshalJSON(questModal("无法战斗", "不支持的战斗模式"))
		}
		if idParam := q.Get("id"); idParam != "" {
			if n, err := strconv.Atoi(idParam); err != nil || n != pathID {
				return cn35wire.MarshalJSON(questModal("无法战斗", "关卡参数不一致"))
			}
		}
		cat, err := loadMergedCatalog(dep)
		if err != nil {
			return nil, err
		}
		quest := cat.questByID(pathID)
		if quest == nil || quest.Battle == nil {
			return cn35wire.MarshalJSON(questModal("无法战斗", "关卡不存在"))
		}
		diff := q.Get("difficulty_id")
		if quest.NoDifficulty || quest.AlwaysOpen {
			// 资源副本是单档副本：缺省按 normal；显式传其他难度拒绝，
			// 不把 hard/very_hard 结算计数混进进度文档。
			if diff != "" && diff != "normal" {
				return cn35wire.MarshalJSON(questModal("无法战斗", "该副本不区分难度"))
			}
			diff = "normal"
		}
		apCost, ok := quest.UseAPMap[diff]
		if !ok || diff == "" {
			return cn35wire.MarshalJSON(questModal("无法战斗", "难度参数非法"))
		}
		prog, err := questProgressFor(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		if !quest.AlwaysOpen &&
			(!cat.questCatalog.storyQuestID(prog.QuestID) || quest.ID > prog.QuestID) {
			return cn35wire.MarshalJSON(questModal("无法战斗", "关卡尚未解锁"))
		}
		// party_id 来自轮盘行名 001/002/...，不是 parties map key。
		// unit_ids 是客户端传来的全库存，不参与队伍解析或授权。
		ordinal, err := ParsePartyOrdinal(q.Get("party_id"))
		if err != nil {
			return cn35wire.MarshalJSON(questModal("无法战斗", "编队编号无效"))
		}
		// 建档事务是 AP 预检的权威边界；独立快照预检会和 use_ap/其他
		// 并发请求形成 TOCTOU。响应快照仍在 OpenBattle 后、use_ap 前读取。
		next := cat.nextQuest(quest.ID)
		nextStage, nextArea, nextQuest := prog.StageID, prog.AreaID, prog.QuestID
		if quest.ID == prog.QuestID && next != nil {
			nextArea, nextQuest = next.AreaID, next.ID
			if na := cat.areaByID(next.AreaID); na != nil {
				nextStage = na.StageID
			}
		}
		// 出怪材质化：紧凑项合成 params + 难度 hp/at 千分比缩放
		// （story-01 三档难度分表，2026-10-01 Note）。
		prof := battleProfileFor(quest.Battle, diff)
		appearTable, err := materializeAppearTable(quest.Battle, prof, dep.Catalog)
		if err != nil {
			return nil, fmt.Errorf("quest %d 出怪合成失败: %w", quest.ID, err)
		}
		rewards := AggregateRewards(appearTable)
		rnd := battleRand(dep)
		// 难度固定奖励（金币/资材）与剧情魔宠掉落均进会话快照，
		// 并注入 appear_table 供客户端逐怪/结算画面展示。
		fixed := fixedBattleRewards(prof)
		var seedReward *BattleReward
		if quest.Battle.SeedDrop != nil {
			seedReward = rollStorySeedDrop(quest.Battle.SeedDrop, diff, rnd)
			if seedReward != nil {
				rewards = append(rewards, *seedReward)
			}
		}
		rewards = append(rewards, fixed...)
		// 资源副本：按开战时生效版本摇掉落，快照随会话冻结（ITEM350-06）。
		// ConfigVersion 只给资源会话；剧情关不能落入资源奖励台账。
		// rolled 保留摇中明细，execute 文档把它注入 appear_table 下发——
		// 客户端结算画面只渲染 appear_table 声明的掉落，不注入则胜利后
		// 看不到素材/金币（服务端已入账但不展示）。
		configVersion := int64(0)
		var rolled []BattleReward
		if drops, ok := cat.drops[quest.ID]; ok {
			configVersion = cat.version
			rolled = rollResourceRewards(drops, cat.itemContent, rnd)
			rewards = append(rewards, rolled...)
		}
		// RUNE350-03 符文本：掉落判定需要参战符文（收集乘算），冻结点
		// 移到 OpenBattleForParty 同事务——这里只注入带 seed 的规格
		// 副本，不落 rewards。
		var runeSpec *RuneDropSpec
		if spec, ok := cat.runeSpecs[quest.ID]; ok {
			cp := *spec
			cp.Seed = rnd.Int63()
			runeSpec = &cp
			configVersion = cat.version
		}
		bgID := pickBattleBackground(quest, rnd)
		sess := &BattleSession{
			BattleID: newBattleID(), UserID: req.UserID,
			QuestID: quest.ID, AreaID: quest.AreaID, Difficulty: diff,
			Status: BattlePrepared, APCost: apCost,
			Rewards:     rewards,
			Exp:         prof.Exp,
			NextStageID: nextStage, NextAreaID: nextArea, NextQuestID: nextQuest,
			Continuable:   derefBool(quest.Continuable, true),
			ConfigVersion: configVersion,
			RuneSpec:      runeSpec,
		}
		deckBattles, ok := dep.Battles.(DeckBattleStore)
		if !ok {
			return cn35wire.MarshalJSON(questModal("无法战斗", "编队服务不可用"))
		}
		records, seeds, err := deckBattles.OpenBattleForParty(sess, battleAPTTLSeconds,
			ordinal, quest.UnitCountLimit)
		if err != nil {
			switch {
			case errors.Is(err, ErrBattleAP):
				return cn35wire.MarshalJSON(questNoAPModal(dep, req.UserID, false))
			case errors.Is(err, ErrDeckNotFound):
				return cn35wire.MarshalJSON(questModal("无法战斗", "编队编号无效"))
			case errors.Is(err, ErrDeckSlotInvalid):
				return cn35wire.MarshalJSON(questModal("无法战斗", "编队槽位无效"))
			case errors.Is(err, ErrDeckUnitNotOwned):
				return cn35wire.MarshalJSON(questModal("无法战斗", "编队包含未持有的同伴"))
			case errors.Is(err, ErrDeckDuplicate):
				return cn35wire.MarshalJSON(questModal("无法战斗", "编队包含重复同伴"))
			case errors.Is(err, ErrDeckEmpty):
				return cn35wire.MarshalJSON(questModal("无法战斗", "编队为空，无法开始战斗"))
			case errors.Is(err, ErrDeckUnitCount):
				return cn35wire.MarshalJSON(questModal("无法战斗", "编队人数超限"))
			default:
				return nil, err
			}
		}
		c.Decoded["party_id"] = sess.PartyID
		c.Decoded["party_ordinal"] = ordinal
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		c.Decoded["battle_id"] = sess.BattleID
		// 符文本摇中符文挂回各自怪位（客户端按怪位/结算画面渲染
		// spawnEntry.rewards 里的 {key:"skill",id}）。
		appearTable = appearTableWithRuneDrops(appearTable, sess.RuneDrops)
		doc, err := buildExecuteDoc(dep, cat.questCatalog, quest, diff, sess,
			records, seeds, snap, prog, appearTable,
			append(append([]BattleReward{}, fixed...), rolled...), seedReward, bgID)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(doc)
	})
}

// buildExecuteDoc 拼 execute 响应文档（quest0.json 键集为 oracle）。
// seeds 为参战单位装备的魔宠实例（与会话建档同事务解析）。
func buildExecuteDoc(dep Deps, cat *questCatalog, quest *catalogQuest,
	diff string, sess *BattleSession, records []UnitRecord, seeds []MonsterRecord,
	snap *PlayerSnapshot, prog *QuestProgress, appearTable map[string]any,
	injected []BattleReward, seedReward *BattleReward, bgID int) (map[string]any, error) {
	battle := quest.Battle
	// injected（难度固定奖励 + 资源副本摇中掉落）注入 appear_table 供客户端
	// 掉落/结算展示；注入在材质化副本上进行，不污染目录对象
	// （AggregateRewards 已在注入前完成）。seed 奖励挂到该魔宠实际出现的
	// 怪位，胜利结算画面才能指认掉落来源。
	if seedReward != nil {
		appearTable = appearTableWithSeedReward(appearTable, *seedReward)
	}
	if len(injected) > 0 {
		appearTable = appearTableWithRewards(appearTable, injected)
	}
	// quest 子文档：catalog 字段 + 难度上下文回显 + 目录级默认。
	diffName := quest.DifficultyNames[difficultyIndex(diff)]
	questDoc := map[string]any{
		"id": quest.ID, "name": quest.Name,
		"mode":                   "quest",
		"use_ap":                 sess.APCost,
		"use_ap_map":             quest.UseAPMap,
		"difficulty_id":          diff,
		"difficulty_name":        diffName,
		"difficulty_option_name": "",
		"background_id":          bgID,
		"initial_power":          quest.InitialPower,
		"unit_count_limit":       quest.UnitCountLimit,
		"seed_atack_rate":        quest.SeedAtackRate,
		"unit_help_rate":         quest.UnitHelpRate,
		"continuable":            sess.Continuable,
		"wave_conf":              quest.WaveConf,
		"background":             cat.Defaults.Background,
		"powerup_conf":           cat.Defaults.PowerupConf,
	}
	// 武器优势倍率按难度表下发（客户端在战内按 difficulty_id 查表；
	// 符文本等副本上调斩/铳率时经此通路）。
	if len(quest.WeaponRateHash) > 0 {
		questDoc["weapon_rate_hash"] = quest.WeaponRateHash
	}
	// monsters：appear_table 引用模板 → 战斗文档。
	monsters := map[string]any{}
	for _, mid := range MonsterIDsInAppearTable(appearTable) {
		m, err := dep.Catalog.Monster(mid)
		if err != nil {
			return nil, fmt.Errorf("quest %d 魔宠模板缺失 %d: %w", quest.ID, mid, err)
		}
		monsters[strconv.Itoa(mid)] = BattleMonsterDoc(m)
	}
	// units：账号库存单位 → 战斗面板文档（buildUnitDoc 同 user_units 形状）。
	// RUNE350-01：装备符文加成合入 Piece.cs 消费字段（hp/at/attack_ms/
	// speed/taghness/*_rate 等），与列表读面同口径生效。
	skillsByUnit, err := skillsByUnitFor(dep, sess.UserID)
	if err != nil {
		return nil, err
	}
	// RUNE350-02：收益系符文显示面——结算画面只渲染 appear_table 的
	// 金币掉落条与顶层 exp，快照加成（OpenBattleForParty 已乘进
	// sess.Rewards/sess.Exp）必须同步进出包，否则实机上看不到收益
	// 提升。partyRunes 仅取参战单位装备的符文，与快照口径一致。
	var partyRunes []PlayerSkill
	for _, r := range records {
		partyRunes = append(partyRunes, skillsByUnit[r.UnitID]...)
	}
	_, goldFactor := CollectRuneFactors(partyRunes)
	if goldFactor > 1 {
		goldTarget := 0
		for _, r := range sess.Rewards {
			if r.Key == "gacha_point" {
				goldTarget += r.Count
			}
		}
		appearTable = scaleAppearTableRewards(appearTable, "gacha_point",
			goldFactor, goldTarget)
	}
	units := make([]any, 0, len(records))
	for _, r := range records {
		cu, err := dep.Catalog.Unit(r.UnitID)
		if err != nil {
			return nil, err
		}
		udoc, err := buildUnitDoc(r, cu)
		if err != nil {
			return nil, err
		}
		applyUnitSkills(udoc, cu.Rare, r.DuplicatedCount, skillsByUnit[r.UnitID])
		units = append(units, udoc)
	}
	// asset_names：单位/敌方魔宠三件套 + 参战魔宠贴图 + 公共段 + 背景/BGM + 关追加。
	assets := append([]string{}, cat.Defaults.AssetNamesCommon...)
	for _, r := range records {
		assets = append(assets,
			fmt.Sprintf("unit/unit_%d", r.UnitID),
			fmt.Sprintf("unit/unit_btn_%d", r.UnitID),
			fmt.Sprintf("unit/unit_large_%d", r.UnitID))
	}
	// 参战单位攻击/受击特效：客户端 ShowEffect 仅在 quest.bundleObjects
	// 含该键时播放，bundle 由 asset_names 加载；effect_ss/effect_pd 均为
	// APK assets/unity 内置资源。非空才去重追加（override 样本可为空串）。
	declaredAsset := map[string]bool{}
	for _, a := range assets {
		declaredAsset[a] = true
	}
	for _, ud := range units {
		udoc, _ := ud.(map[string]any)
		for _, k := range []string{"attack_effect", "target_effect"} {
			e, _ := udoc[k].(string)
			if e == "" || declaredAsset[e] {
				continue
			}
			declaredAsset[e] = true
			assets = append(assets, e)
		}
	}
	declaredMonster := map[int]bool{}
	for _, mid := range MonsterIDsInAppearTable(appearTable) {
		declaredMonster[mid] = true
		assets = append(assets,
			fmt.Sprintf("monster/monster_%d", mid),
			fmt.Sprintf("monster/monster_btn_%d", mid),
			fmt.Sprintf("monster/monster_large_ns_%d", mid))
	}
	// 参战魔宠（玩家种子）：SeedControllerPanel.onInit 与召唤 cutin
	// （SWFQuestCutin.ShowCutin）按 user_monsters 缓存的 id 直查
	// quest.textures["monster/monster_btn_<id>"] / ["monster_large_ns_<id>"]，
	// 缺键即 KeyNotFoundException。种子不生成场上魔宠件（AddMonster 只读
	// waveAppearTable），故只声明两张贴图，不下发 monster/monster_<id>。
	for _, m := range seeds {
		if declaredMonster[m.MonsterID] {
			continue
		}
		declaredMonster[m.MonsterID] = true
		assets = append(assets,
			fmt.Sprintf("monster/monster_btn_%d", m.MonsterID),
			fmt.Sprintf("monster/monster_large_ns_%d", m.MonsterID))
	}
	assets = append(assets,
		fmt.Sprintf("background/background%d_1", bgID),
		battleBgm(cat, battle))
	assets = append(assets, battle.AssetNamesExtra...)
	// help/timezone：无 NPC 援军，按波次发空表/默认时区。
	helpTable := map[string]any{}
	timezone := map[string]any{}
	for wkey := range appearTable {
		helpTable[wkey] = map[string]any{}
		timezone[wkey] = 1
	}
	bgm := battleBgm(cat, battle)
	bid := sess.BattleID
	userDoc := map[string]any{}
	for k, v := range snap.User {
		userDoc[k] = v
	}
	userDoc["exp_table"] = UserExpWindowForGain(
		int(numOf(snap.User["level"])),
		int(numOf(snap.User["exp"])),
		sess.Exp,
	)
	doc := map[string]any{
		"user":           userDoc,
		"quest":          questDoc,
		"units":          units,
		"monsters":       monsters,
		"main_character": cat.Defaults.MainCharacter,
		"appear_table":   appearTable,
		"help_table":     helpTable,
		"timezone_table": timezone,
		"asset_names":    assets,
		"bgm_name":       bgm,
		// exp 必须发会话口径（难度 profile ×收益符文加成），与结算
		// 入账一致；此前发 battle.Exp 基值导致困难/极难结算屏显示
		// 经验低于实收。
		"exp":               sess.Exp,
		"clear_count":       prog.Clears[quest.ID][diff],
		"ap_use_url":        "/quests/use_ap?bid=" + bid,
		"result_url":        "/quests/result?bid=" + bid,
		"lose_url":          "/quests/lose?bid=" + bid,
		"next_page_message": battleNextPage(cat, battle),
		"battle_log":        derefBool(cat.Defaults.BattleLog, false),
		"data":              map[string]any{"user": userDoc},
	}
	return doc, nil
}

// scaleAppearTableRewards 返回把 appear_table 全部怪位 rewards[] 中
// key==rewardKey 的 count 乘算 factor 后的副本：逐条 round 后对首条
// 校正 delta，保证显示合计与快照入账额（ApplyCollectRunes 乘算后的
// sess.Rewards 合计）一致。顺序确定（波次/怪位/条目升序遍历），
// 只拷贝被修改的怪位节点，目录对象不被改写。
func scaleAppearTableRewards(appearTable map[string]any, rewardKey string,
	factor float64, target int) map[string]any {
	var waves []int
	for w := range appearTable {
		if n, err := strconv.Atoi(w); err == nil {
			waves = append(waves, n)
		}
	}
	sort.Ints(waves)
	type hit struct {
		count int
	}
	walk := func(each func(h *hit) bool) int {
		n := 0
		for _, w := range waves {
			wm, _ := appearTable[strconv.Itoa(w)].(map[string]any)
			var mss []int
			for ms := range wm {
				if m, err := strconv.Atoi(ms); err == nil {
					mss = append(mss, m)
				}
			}
			sort.Ints(mss)
			for _, ms := range mss {
				entry, ok := wm[strconv.Itoa(ms)].(map[string]any)
				if !ok {
					continue
				}
				rawRewards, _ := entry["rewards"].([]any)
				for _, rv := range rawRewards {
					rm, ok := rv.(map[string]any)
					if !ok || rm["key"] != rewardKey {
						continue
					}
					h := &hit{count: int(numOf(rm["count"]))}
					if !each(h) {
						return n
					}
					n++
				}
			}
		}
		return n
	}
	counts := []int{}
	walk(func(h *hit) bool {
		counts = append(counts, int(math.Round(float64(h.count)*factor)))
		return true
	})
	if len(counts) == 0 {
		return appearTable
	}
	sum := 0
	for _, c := range counts {
		sum += c
	}
	counts[0] += target - sum
	out := map[string]any{}
	idx := 0
	for _, w := range waves {
		wkey := strconv.Itoa(w)
		wm, ok := appearTable[wkey].(map[string]any)
		if !ok {
			out[wkey] = appearTable[wkey]
			continue
		}
		var mss []int
		for ms := range wm {
			if n, err := strconv.Atoi(ms); err == nil {
				mss = append(mss, n)
			}
		}
		sort.Ints(mss)
		newWave := map[string]any{}
		for skey, ev := range wm {
			entry, ok := ev.(map[string]any)
			if !ok {
				newWave[skey] = ev
				continue
			}
			rawRewards, _ := entry["rewards"].([]any)
			var nr []any
			changed := false
			for _, rv := range rawRewards {
				rm, ok := rv.(map[string]any)
				if !ok || rm["key"] != rewardKey {
					nr = append(nr, rv)
					continue
				}
				cp := map[string]any{}
				for k, v := range rm {
					cp[k] = v
				}
				cp["count"] = counts[idx]
				idx++
				changed = true
				nr = append(nr, cp)
			}
			if !changed {
				newWave[skey] = ev
				continue
			}
			cp := map[string]any{}
			for k, v := range entry {
				cp[k] = v
			}
			cp["rewards"] = nr
			newWave[skey] = cp
		}
		out[wkey] = newWave
	}
	return out
}

// appearTableWithRuneDrops 符文本摇中符文注入指定怪位 rewards[]
// （客户端按 spawnEntry.rewards 的 {key:"skill",id} 渲染逐怪/结算掉落，
// 品质 level 不展示——品质留存服务端 BattleReward.Level 快照）。
// 只拷贝被修改的波次/怪位节点，目录对象不被改写。
func appearTableWithRuneDrops(appearTable map[string]any, drops []RuneDropResult) map[string]any {
	if len(drops) == 0 {
		return appearTable
	}
	type slotKey struct{ w, ms int }
	bySlot := map[slotKey][]int{}
	for _, d := range drops {
		k := slotKey{d.Wave, d.Ms}
		bySlot[k] = append(bySlot[k], d.SkillID)
	}
	out := map[string]any{}
	for wkey, wv := range appearTable {
		wm, ok := wv.(map[string]any)
		wn, err := strconv.Atoi(wkey)
		if !ok || err != nil {
			out[wkey] = wv
			continue
		}
		newWave := map[string]any{}
		for skey, ev := range wm {
			entry, ok := ev.(map[string]any)
			sn, serr := strconv.Atoi(skey)
			ids, hit := bySlot[slotKey{wn, sn}]
			if !ok || serr != nil || !hit {
				newWave[skey] = ev
				continue
			}
			cp := map[string]any{}
			for k, v := range entry {
				cp[k] = v
			}
			nr, _ := entry["rewards"].([]any)
			nr = append([]any{}, nr...)
			for _, id := range ids {
				nr = append(nr, map[string]any{"key": "skill", "id": id})
			}
			cp["rewards"] = nr
			newWave[skey] = cp
		}
		out[wkey] = newWave
	}
	return out
}

// appearTableWithRewards 返回注入摇中掉落后的 appear_table 副本：奖励按序
// 轮转挂到最高波次的各怪位 rewards[]（客户端逐怪掉落与胜利结算画面均读
// spawnEntry.rewards，见 AppearTableDocument.SetReward）。只拷贝被修改的
// 波次/怪位节点，目录对象不被改写。
func appearTableWithRewards(appearTable map[string]any, rolled []BattleReward) map[string]any {
	maxWave, slots := -1, []int{}
	var waveMap map[string]any
	for wkey, wv := range appearTable {
		n, err := strconv.Atoi(wkey)
		if err != nil {
			continue
		}
		wm, ok := wv.(map[string]any)
		if !ok || n < maxWave {
			continue
		}
		if n > maxWave {
			maxWave, slots, waveMap = n, []int{}, wm
		}
	}
	if waveMap == nil {
		return appearTable
	}
	for skey := range waveMap {
		if n, err := strconv.Atoi(skey); err == nil {
			slots = append(slots, n)
		}
	}
	sort.Ints(slots)
	if len(slots) == 0 {
		return appearTable
	}
	perSlot := make([][]any, len(slots))
	for i, r := range rolled {
		rd := map[string]any{"key": r.Key, "count": r.Count}
		if r.ID != 0 {
			rd["id"] = r.ID
		}
		perSlot[i%len(slots)] = append(perSlot[i%len(slots)], rd)
	}
	newWave := map[string]any{}
	for skey, ev := range waveMap {
		entry, ok := ev.(map[string]any)
		slot, _ := strconv.Atoi(skey)
		if !ok {
			newWave[skey] = ev
			continue
		}
		cp := map[string]any{}
		for k, v := range entry {
			cp[k] = v
		}
		for i, s := range slots {
			if s == slot && len(perSlot[i]) > 0 {
				existing, _ := cp["rewards"].([]any)
				cp["rewards"] = append(perSlot[i], existing...)
			}
		}
		newWave[skey] = cp
	}
	out := map[string]any{}
	for k, v := range appearTable {
		out[k] = v
	}
	out[strconv.Itoa(maxWave)] = newWave
	return out
}

func battleBgm(cat *questCatalog, b *catalogBattle) string {
	if b.BgmName != "" {
		return b.BgmName
	}
	if cat.Defaults.BgmName != "" {
		return cat.Defaults.BgmName
	}
	return "sound/B-4"
}

func battleNextPage(cat *questCatalog, b *catalogBattle) string {
	if b.NextPageMessage != "" {
		return b.NextPageMessage
	}
	if cat.Defaults.NextPageMessage != "" {
		return cat.Defaults.NextPageMessage
	}
	return "name=AreaSelect"
}

// ---- ap_use ----

// questUseAPHandler /quests/use_ap：prepared→ap_paid 条件扣点。
// 重放/终态会话一律 success（不再扣点）；扣点失败回 status:"error"+modal
// （客户端 3s 重试门禁即拒绝语义——AP 不足按契约应在 execute 拦截）。
func questUseAPHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "quest_use_ap",
			"bid": bid, "quest_help_enable": req.Query.Get("quest_help_enable"),
		}
		if bid == "" {
			return cn35wire.MarshalJSON(statusDoc("error"))
		}
		sess, err := dep.Battles.PayBattleAP(req.UserID, bid)
		switch {
		case err == ErrBattleNotFound || err == ErrBattleState:
			return cn35wire.MarshalJSON(statusDoc("error"))
		case err == ErrBattleAP:
			return cn35wire.MarshalJSON(map[string]any{
				"status": "error",
				"modal":  questModal("体力不足", "AP 不足，无法开始战斗。")["modal"],
			})
		case err != nil:
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		c.Decoded["status"] = sess.Status
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   map[string]any{"user": snap.User},
		})
	})
}

// ---- result ----

// questResultHandler /quests/result：单事务结算。首次 success +
// 已证下发键（next_page_message/data.user/data.quests_<area>）；重放与
// 终态会话 already_received；伪造/跨账号 error。
func questResultHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		rep := BattleReport{
			Time:   queryFloat(req.Query, "time"),
			RestMC: int(queryFloat(req.Query, "mc")),
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "quest_result",
			"bid": bid, "time": rep.Time, "mc": rep.RestMC,
		}
		if bid == "" {
			return cn35wire.MarshalJSON(statusDoc("error"))
		}
		res, err := dep.Battles.SettleBattle(req.UserID, bid, rep)
		switch {
		case err == ErrBattleNotFound || err == ErrBattleState:
			return cn35wire.MarshalJSON(statusDoc("error"))
		case err == ErrBattleAP:
			return cn35wire.MarshalJSON(map[string]any{
				"status": "error",
				"modal":  questModal("体力不足", "AP 不足，无法开始战斗。")["modal"],
			})
		case err != nil:
			return nil, err
		}
		if res.Already {
			if res.Session != nil && res.Session.Status == BattleCleared {
				data := map[string]any{}
				for _, r := range res.Session.Rewards {
					switch r.Key {
					case "skill":
						data["user_skills"] = nil
					case "item":
						data["user_items"] = nil
						data["user_items_hash"] = nil
					case "seed":
						data["monster_albums"] = nil
						if inv, ierr := inventoryDataFor(dep, c, req.UserID); ierr == nil {
							data["user_monsters"] = inv["user_monsters"]
							data["user_units"] = inv["user_units"]
						}
					}
				}
				if len(data) > 0 {
					return cn35wire.MarshalJSON(map[string]any{
						"status": "already_received",
						"data":   data,
					})
				}
			}
			return cn35wire.MarshalJSON(statusDoc("already_received"))
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		// data 回推：user 最新态 + 本区关卡表（clear/解锁即时反映）。
		data := map[string]any{"user": snap.User}
		if cat, err := loadMergedCatalog(dep); err == nil {
			if quests := unlockedQuestDocs(cat.questCatalog, dep, req.UserID, res.Session.AreaID); quests != nil {
				data[fmt.Sprintf("quests_%d", res.Session.AreaID)] = quests
			}
			if len(res.MonsterUUIDs) > 0 {
				data["monster_albums"] = nil
				if inv, err := inventoryDataFor(dep, c, req.UserID); err == nil {
					data["user_monsters"] = inv["user_monsters"]
					data["user_units"] = inv["user_units"]
				}
			}
		}
		// ITEM350-13 / RUNE350：item 入账后回推背包；skill 入账后按原服契约下发
		// user_skills:null 使客户端 DataCache.Add 触发 Expire("user_skills")，
		// 切回符文页时经 requireData 自动重拉 /user_skills/。
		for _, r := range res.Rewards {
			switch r.Key {
			case "item":
				if dep.Items != nil {
					if itemsData, ierr := userItemsData(dep, req.UserID); ierr == nil {
						for k, v := range itemsData {
							data[k] = v
						}
					}
				} else {
					data["user_items"] = nil
					data["user_items_hash"] = nil
				}
			case "skill":
				data["user_skills"] = nil
			}
		}
		c.Decoded["clear_count"] = res.ClearCount
		return cn35wire.MarshalJSON(map[string]any{
			"status":            "success",
			"data":              data,
			"next_page_message": resultNextPage(dep, res.Session),
			// 结算屏展示键（SWFQuestClear.cs:381-407 已证消费）：
			// spend_time 回显客户端上行的战斗秒数。total_gp/message
			// 等暂无真值来源的键不下发（客户端有键才渲染对应行）。
			"spend_time": int(rep.Time),
		})
	})
}

// resultNextPage 结算页跳转（覆盖 execute 时的值；读会话关卡的配置）。
func resultNextPage(dep Deps, sess *BattleSession) string {
	cat, err := loadMergedCatalog(dep)
	if err == nil {
		if q := cat.questByID(sess.QuestID); q != nil && q.Battle != nil {
			return battleNextPage(cat.questCatalog, q.Battle)
		}
		return cat.Defaults.NextPageMessage
	}
	return "name=AreaSelect"
}

// unlockedQuestDocs 组 quests_<areaId> 键值：本区已解锁关卡的 questDoc 列表。
// 返回 nil 表示目录不可用/区不存在（不阻断结算主响应）。
func unlockedQuestDocs(cat *questCatalog, dep Deps, userID string, areaID int) []any {
	if cat.areaByID(areaID) == nil {
		return nil
	}
	prog, err := questProgressFor(dep, userID)
	if err != nil {
		return nil
	}
	out := []any{}
	for _, q := range cat.questsOfArea(areaID) {
		if !cat.questUnlocked(q, prog) {
			continue
		}
		out = append(out, questDoc(q, areaID, prog))
	}
	return out
}

// ---- lose ----

// questLoseHandler /quests/lose：败北上报。active→lost 记战报+首败标志；
// 终态幂等。mode=quest 客户端不读响应体，仍回 status:"success"。
func questLoseHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		rep := BattleReport{
			Time:    queryFloat(req.Query, "time"),
			RestMC:  int(queryFloat(req.Query, "mc")),
			Escaped: req.Query.Get("escaped") == "true",
			Damage:  parseDamageParams(req.Query),
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "quest_lose",
			"bid": bid, "time": rep.Time, "mc": rep.RestMC, "escaped": rep.Escaped,
			"damage_keys": len(rep.Damage),
		}
		if bid == "" {
			return cn35wire.MarshalJSON(statusDoc("error"))
		}
		sess, err := dep.Battles.LoseBattle(req.UserID, bid, rep)
		switch {
		case err == ErrBattleNotFound:
			return cn35wire.MarshalJSON(statusDoc("error"))
		case err != nil:
			return nil, err
		}
		c.Decoded["status"] = sess.Status
		return cn35wire.MarshalJSON(statusDoc("success"))
	})
}

// queryFloat 容错读 query 数值参数（缺失/非数字 → 0）。
func queryFloat(q map[string][]string, key string) float64 {
	vs, ok := q[key]
	if !ok || len(vs) == 0 {
		return 0
	}
	f, _ := strconv.ParseFloat(vs[0], 64)
	return f
}

// parseDamageParams 收集 damage[<key>]=<int> 战报段（SWFQuestLose.cs:253+）。
func parseDamageParams(q map[string][]string) map[string]int64 {
	out := map[string]int64{}
	for k, vs := range q {
		if !strings.HasPrefix(k, "damage[") || !strings.HasSuffix(k, "]") || len(vs) == 0 {
			continue
		}
		if n, err := strconv.ParseInt(vs[0], 10, 64); err == nil {
			out[k[len("damage["):len(k)-1]] = n
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---- continue ----

// questContinueHandler /quests/continue：零参数，最近 lost+continuable
// 会话扣 1 钻转 continued。成功回 data.user_coin.amount（客户端
// Continue() 直接消费）；ErrNoContinuable/ErrBattleCoin → 非 success
// （客户端语义=弹 CoinShop 引导框，SWFQuestLose.cs:637+）。
func questContinueHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "quest_continue",
		}
		sess, coin, err := dep.Battles.ContinueBattle(req.UserID)
		switch {
		case err == ErrNoContinuable || err == ErrBattleCoin:
			return cn35wire.MarshalJSON(map[string]any{
				"status": "error",
				"data":   map[string]any{"user_coin": map[string]any{"amount": coin}},
			})
		case err != nil:
			return nil, err
		}
		c.Decoded["battle_id"] = sess.BattleID
		c.Decoded["continue_count"] = sess.ContinueCount
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   map[string]any{"user_coin": map[string]any{"amount": coin}},
		})
	})
}

// ---- log_battle ----

// questLogBattleHandler /quests/log_battle：battle_record JSON 归档
// （客户端回调为空 delegate，仅记账）。battleId 客户端侧为关卡 id。
func questLogBattleHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		record := req.Form.Get("battle_record")
		questID := 0
		var doc map[string]any
		if err := json.Unmarshal([]byte(record), &doc); err == nil {
			questID = int(numOf(doc["battleId"]))
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "quest_log_battle",
			"quest_id": questID, "record_bytes": len(record),
		}
		if record == "" {
			return cn35wire.MarshalJSON(map[string]any{})
		}
		if err := dep.Battles.LogBattle(req.UserID, "", questID, record); err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{})
	})
}
