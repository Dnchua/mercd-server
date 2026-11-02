// guild_battle_wire.go GUILD350-06/07：会战应援/祈祷/出战战斗面。
//
// 客户端路径（客户端事实）：
//   - GET /battles/help、/battles/pray（响应读 animation_message）
//   - GET /battles/guild_quest_execute.json?mode=battle&guild_id=&guild_id_N=&difficulty_id=&party_id=
//   - execute 文档内 ap_use_url/result_url/lose_url 由服务端下发（本会战链
//     用 /battles/ 前缀，与剧情 /quests/ 链隔离）。
package cn35game

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// ---- 原服会战 execute 固定配置（GUILDCLOSE350/GC-05） ----
//
// 取证：artifacts/mlk-capture-201711/decrypted/{1,2,3}/battles_guild_quest_execute.json.json
// （契约 source_sha256 90d319ac…；3 条记录 / 1 种响应体）。以下项是**配置常量**，
// 不是该玩家的动态战况值；每项的客户端消费方都已核对：
//   - help_base_sec / help_sec_level_rate：助战单位可用时间公式
//     `helpAvailableTime = help_base_sec + unit.level * help_sec_level_rate`
//     （Quest.cs:2296-2298，单位秒）。
//   - seed_atack_rate：客户端只做「是否为 0」判定，决定魔宠攻击开关显隐
//     （Quest.cs:993）；量纲在国服客户端无消费方 → 原服 0.2 按配置常量下发，
//     实际伤害/加成口径仍由 GuildRules 与内部战斗逻辑决定（本文件不碰战斗数值）。
//   - battle_rank_help_level_rate：**整型倍率**，乘到 unit.initial_level
//     （Quest.cs:2064-2066、2265-2267）。
//   - guild_asset_help_level_base：助战单位 initial_level 基准
//     （PowerupButton.cs:169-171、AutoQuestPowerupButton.cs:143-145）。
//   - battle_help_initial_level：客户端无消费方；原服样本 = base*rate = 6〔推断〕，
//     契约只校验类型+数值，按同一算式下发以保持自洽。
//   - bp_storage_button=1：原服 /guilds/ 与 execute 两处样本同值（显示蓄水库入口）。
//   - flag_image_name="common/flag"：客户端 `textures[key]` 直取已打包素材
//     （Quest.cs:1061，decoded/assets/unity/common/flag.Android.unity3d），
//     缺失会 KeyNotFoundException，因此必须用原服同值。
const (
	gwHelpBaseSec             = 25
	gwHelpSecLevelRate        = 0.1
	gwSeedAtackRate           = 0.2
	gwGuildAssetHelpLevelBase = 3
	gwBattleRankHelpLevelRate = 2
	gwBPStorageButton         = 1
	gwFlagImageName           = "common/flag"
	// gwBattleBGM 会战战斗 BGM：原服样本 `sound/B-6`，且该资源名在
	// server/assets/cn350/bundles.txt 内（已核，客户端可加载）。
	gwBattleBGM = "sound/B-6"
)

// gwWeaponRateNames 武器相性 7 键的客户端展示顺序（QuestSelectPartsBase.cs:96-101）。
var gwWeaponRateNames = []string{"slash", "rush", "hit", "arrow", "magic", "gun", "heal"}

// gwWeaponRates 会战武器相性**倍率**（不是百分数）：客户端取 dec(rate)*100 后拼
// "%" 显示（QuestSelectPartsBase.cs:106-108），故 1.3 → 130%、1.0 → 100%。
// 原服样本 hit/arrow=1.3、其余 1.0。
// 〔推断〕样本只覆盖一场（2017-11-29 夜场）；weapon_rate_hash 的 normal/hard/
// very_hard/impossible 四档在样本里完全相同，故按「全场一表」下发。缺口：
// 缺其它时段/难度样本，无法证明是否随时段变化。
var gwWeaponRates = map[string]float64{
	"slash": 1, "rush": 1, "hit": 1.3, "arrow": 1.3, "magic": 1, "gun": 1, "heal": 1,
}

// gwWeaponRateDifficulties weapon_rate_hash 四档键（原服样本顺序）。
var gwWeaponRateDifficulties = []string{"normal", "hard", "very_hard", "impossible"}

// gwWeaponRateHashDoc quest.weapon_rate_hash：难度 → 7 键倍率表。
func gwWeaponRateHashDoc() map[string]any {
	out := map[string]any{}
	for _, d := range gwWeaponRateDifficulties {
		row := map[string]any{}
		for _, n := range gwWeaponRateNames {
			row[n+"_rate"] = gwWeaponRates[n]
		}
		out[d] = row
	}
	return out
}

// gwQuestWeaponRateFields 把同一张表平铺进 quest（quest.slash_rate 等 7 键）。
// 原服两处同源同值：quest.<w>_rate 与 quest.weapon_rate_hash[当前难度][<w>_rate]。
func gwQuestWeaponRateFields(dst map[string]any) {
	for _, n := range gwWeaponRateNames {
		dst[n+"_rate"] = gwWeaponRates[n]
	}
}

// gwSeedAttributeRatesDoc seed_attribute_rates（魔宠/召唤物属性倍率）。
// 消费方：SWFQuestCutin.cs:538（seed_at_rate 乘魔宠伤害）、
// SeedControllerPanel.cs:63-65（cost_rate/call_interval_rate 乘召唤消耗与间隔）、
// SWFQuestCutin.cs:589/773（seed_skill_value_percent 为**字符串**百分比或 null，
// 客户端 s(key,"") 取空串，null 可容忍）。原服样本全为中性 1 / null。
func gwSeedAttributeRatesDoc() map[string]any {
	return map[string]any{
		"seed_at_rate": 1, "seed_skill_value_percent": nil,
		"call_interval_rate": 1, "cost_rate": 1,
	}
}

// gwMessageCategoryKeys 结算回包 data.messages 的 7 个消息类别键
// （battles_attack.json::data.messages，顺序同原服键集）。
var gwMessageCategoryKeys = []string{
	"guild_comment", "guild_help", "battle_activity",
	"community_comment", "community_help", "community_raid_call", "user_comment",
}

// gwBattleMessagesDoc 结算回包的 data.messages 容器：空数组 = 本轮无新消息。
// 公会留言/应援流水属消息域（GC-08），此处只保证结构完整，不伪造内容。
func gwBattleMessagesDoc() map[string]any {
	out := map[string]any{}
	for _, k := range gwMessageCategoryKeys {
		out[k] = []any{}
	}
	return out
}

// gwBattleGuildDoc execute 回包 data.guild：与 /guilds/ 的 guild 文档同形
// （复用 guildDoc 单一实现，不另造一份），仅补两个场次/玩家态键：
//   - battle_id：原服 = 玩家当前战斗会话 id（execute 回包 data.guild.battle_id
//     与同一录包的 user.battle_id 同值）；我们无 user.battle_id 字段，下发刚签发
//     的会战 bid〔推断：语义取「当前出击会话」〕。
//   - bp_storage_button：1 = 显示蓄水库入口（原服两处样本同值）。
func gwBattleGuildDoc(view *GuildView, battleID string) map[string]any {
	if view == nil || view.Guild == nil {
		return nil
	}
	doc := guildDoc(view)
	doc["battle_id"] = battleID
	doc["bp_storage_button"] = gwBPStorageButton
	return doc
}

// gwHelpAnimationMessage 原服 animation_message 文案形状：应援「<会名>的SP +400」、
// 祈祷「<会名>魔宠的强化率 +2%」（decrypted/1/battles_help.json、battles_pray.json）。
// 客户端只整串显示（GuildTop.cs:804/844），不解析数值。
func gwHelpAnimationMessage(action, guildName string) string {
	if guildName == "" {
		guildName = "公会"
	}
	if action == "pray" {
		return fmt.Sprintf("%s魔宠的强化率 +%d%%", guildName,
			int(math.Round(GuildRules.PrayHPBonus*100)))
	}
	return fmt.Sprintf("%s的SP +%d", guildName, GuildRules.HelpSP)
}

// gwQuestDifficultyName 原服 difficulty_name 按**当前时段**命名（样本夜场为
// 「公会战(夜)」），不是 normal/hard/very_hard 的本地化文案。
func gwQuestDifficultyName(now time.Time) string {
	fi := GuildActableFieldIndex(now)
	if fi < 0 {
		fi = GuildFieldIndex(now)
	}
	if fi < 0 || fi >= len(GuildRules.Fields) {
		return "公会战"
	}
	return "公会战(" + GuildRules.Fields[fi].Name + ")"
}

// gwTargetLabel quest.name 的「vs <会名>(N个公会)」两段：主目标会名取
// battle_condition.guilds 里同 id 的行，N 取本次选中目标数（atk.TargetGuildIDs）。
// 〔推断〕原服只有一个样本（「vs 没事溜溜圈(2个公会)」，该场 battle_condition
// 列了 4 个会而文案是 2）——按「本次选中目标数」解释，无法排除「攻守双方会数」
// 等其它口径；缺口：缺 1 目标/3 目标出击的录包样本。契约只校验 string 类型。
func gwTargetLabel(atk *GWAttack, gw *GWView) (name string, count int) {
	name = "目标公会"
	count = len(atk.TargetGuildIDs)
	if count == 0 {
		count = 1
	}
	if gw != nil {
		for _, p := range gw.Participants {
			if p.GuildID == atk.TargetGuildID && p.Name != "" {
				name = p.Name
				break
			}
		}
	}
	return name, count
}

// gwHelpPrayHandler /battles/help|/battles/pray。
//
// 原服回包（decrypted/1/battles_help.json、battles_pray.json）：
// `{animation_message, data_query:{user:{bp}}, data:{battle_condition}}`。
// data_query 是客户端缓存**增量刷新**通道：DataCache.UpdateDataByResponse→
// AddWithQuery（DataCache.cs:321-353、397-404）把 {data_key:{field:value}} 合并进
// 缓存并触发 RequireDocumentUpdated，应援/祈祷后 BP 变化即由此驱动 GuildTop 重画。
func gwHelpPrayHandler(dep Deps, action string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		gp, cost, err := dep.Guild.GWHelpPray(req.UserID, action,
			req.Query.Get("op_key"), guildWireNow(dep))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		gw, err := dep.Guild.GWState(req.UserID, guildWireNow(dep))
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "battles_" + action,
			"gp": gp, "bp_cost": cost,
		}
		guildName, _ := snap.User["guild_name"].(string)
		if gw != nil {
			for _, p := range gw.Participants {
				if p.Joined {
					guildName = p.Name
					break
				}
			}
		}
		msg := gwHelpAnimationMessage(action, guildName)
		return cn35wire.MarshalJSON(map[string]any{
			"status":            "success",
			"animation_message": msg,
			"data_query": map[string]any{
				"user": map[string]any{"bp": snap.User["bp"]},
			},
			"data": map[string]any{
				"user":             snap.User,
				"battle_condition": battleConditionDoc(gw),
				"guild_war": map[string]any{
					"action": action, "gp_delta": gp, "bp_cost": cost,
				},
			},
		})
	})
}

// gwExecuteHandler GET /battles/guild_quest_execute.json：mode=battle 的
// 会战出战建档。响应文档与剧情 execute 同形（QuestExecuteDocument 统一解析），
// 敌方怪组 = 目标公会防卫卡组快照。
func gwExecuteHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		q := req.Query
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guild_quest_execute",
			"mode": q.Get("mode"), "guild_id": q.Get("guild_id"),
			"difficulty_id": q.Get("difficulty_id"), "party_id": q.Get("party_id"),
		}
		if mode := q.Get("mode"); mode != "battle" {
			return cn35wire.MarshalJSON(guildModal("无法战斗", "不支持的战斗模式"))
		}
		diff := q.Get("difficulty_id")
		if diff == "" {
			diff = "normal"
		}
		// 目标集合：主攻 guild_id + guild_id_1..N 附加（服务端逐个验证同组）。
		targetGuildID := q.Get("guild_id")
		var extraTargets []string
		for i := 1; ; i++ {
			v := q.Get("guild_id_" + strconv.Itoa(i))
			if v == "" {
				break
			}
			extraTargets = append(extraTargets, v)
		}
		ordinal, err := ParsePartyOrdinal(q.Get("party_id"))
		if err != nil {
			return cn35wire.MarshalJSON(guildModal("无法战斗", "编队编号无效"))
		}
		if dep.Catalog == nil {
			return nil, fmt.Errorf("cn35: 公会战缺 Catalog")
		}
		atk, units, seeds, err := dep.Guild.GWAttackOpen(req.UserID, targetGuildID,
			extraTargets, diff, ordinal, 5, q.Get("op_key"), guildWireNow(dep))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		// battle_condition 组内公会（原服 quest.name 的 N 与本组会名口径〔推断〕）。
		gw, gwerr := dep.Guild.GWState(req.UserID, guildWireNow(dep))
		if gwerr != nil {
			return nil, gwerr
		}
		doc, err := buildGWExecuteDoc(dep, atk, units, seeds, snap, gw)
		if err != nil {
			return nil, err
		}
		c.Decoded["battle_id"] = atk.BattleID
		return cn35wire.MarshalJSON(doc)
	})
}

// gwDifficultyBattleBP 难度面板千分比（normal/hard/very_hard），
// 供防卫魔宠与小怪共用缩放。
func gwDifficultyBattleBP(difficulty string) (hpBP, atBP int) {
	hpBP = GuildRules.DifficultyHpBP[difficulty]
	atBP = GuildRules.DifficultyAtBP[difficulty]
	if hpBP == 0 {
		hpBP = 1000
	}
	if atBP == 0 {
		atBP = 1000
	}
	return hpBP, atBP
}

// gwBossAppearList 防卫快照 → boss_appear_table 波次 1 的大怪文档列。
// 客户端 Quest.cs WatchBattleBossAppearTable：boss 按 interval_sec
// 逐只出场；OnBattleBossDead 里 spawned boss 全灭且文档耗尽 → Clear()，
// 即「大魔宠击杀完即算完成战斗」的下发面。
//
// interval_sec 语义 = 该只出场后距下一只的间隔秒：组内第二只给 0
// 实现「每一波 1-2 只」，组间给难度间隔（普通 120/困难 80/极难 40s）。
func gwBossAppearList(atk *GWAttack, dep Deps) ([]any, error) {
	hpBP, atBP := gwDifficultyBattleBP(atk.Difficulty)
	interval := GuildRules.DifficultyWaveIntervalSec[atk.Difficulty]
	if interval <= 0 {
		interval = 120
	}
	total := GuildRules.BattleBossTotal
	if total <= 0 {
		total = len(atk.DefenseSnapshot)
	}
	// 多目标出击时按目标公会轮转取材：总量上限截断也保证每个
	// 已收费目标都有防卫怪出场（顺序截断会把后排目标整场吃掉，
	// 但其 GP 在结算时仍被掠夺）。
	byGuild := map[string][]GWDefenseMonster{}
	guildOrder := []string{}
	for _, d := range atk.DefenseSnapshot {
		if _, ok := byGuild[d.GuildID]; !ok {
			guildOrder = append(guildOrder, d.GuildID)
		}
		byGuild[d.GuildID] = append(byGuild[d.GuildID], d)
	}
	picked := []GWDefenseMonster{}
	for len(picked) < total && len(guildOrder) > 0 {
		next := guildOrder[:0]
		for _, g := range guildOrder {
			if len(byGuild[g]) == 0 {
				continue
			}
			picked = append(picked, byGuild[g][0])
			byGuild[g] = byGuild[g][1:]
			if len(byGuild[g]) > 0 {
				next = append(next, g)
			}
			if len(picked) >= total {
				break
			}
		}
		guildOrder = next
	}
	out := []any{}
	for i, d := range picked {
		m, err := dep.Catalog.Monster(d.MonsterID)
		if err != nil {
			return nil, fmt.Errorf("防卫魔宠模板缺失 %d: %w", d.MonsterID, err)
		}
		hp := int(float64(GuildRules.DefenseHPBase) *
			(1 + GuildRules.DefenseHPPerLevel*float64(d.Level-1)) *
			float64(hpBP) / 1000.0 * (1 + d.HPBonus))
		at := int(float64(GuildRules.DefenseATBase) *
			float64(atBP) / 1000.0 * (1 + d.ATBonus))
		gap := interval
		if i%2 == 0 {
			gap = 0 // 组内第二只紧随，组间才间隔
		}
		out = append(out, map[string]any{
			"id": d.MonsterID, "scale": 1.0, "powerup_rate": 2.5,
			"gauge": true, "interval_sec": gap,
			"params": storyMonsterParams(m, hp, at),
		})
	}
	return out, nil
}

// gwTrashAppearTable 小怪 appear_table（波次 1）：DifficultyTrashTotal
// 条数按难度间隔分批出场（每组 ≈ total/批数），与 boss 波次时间轴对齐。
// 单波时间轴：ms 键为该怪出场毫秒（客户端 WatchAppearTable 消费），
// 击杀不推进波次（wave_clear_pt 缺省 0），胜利只由 boss 表驱动。
func gwTrashAppearTable(atk *GWAttack, dep Deps, bossGroups int) (map[string]any, error) {
	total := GuildRules.DifficultyTrashTotal[atk.Difficulty]
	if total <= 0 || len(GuildRules.TrashMonsterIDs) == 0 {
		return map[string]any{}, nil
	}
	hpBP, atBP := gwDifficultyBattleBP(atk.Difficulty)
	interval := GuildRules.DifficultyWaveIntervalSec[atk.Difficulty]
	if interval <= 0 {
		interval = 120
	}
	if bossGroups <= 0 {
		bossGroups = 1
	}
	wm := map[string]any{}
	per := total / bossGroups
	rem := total % bossGroups
	idx := 0
	for g := 0; g < bossGroups; g++ {
		count := per
		if g == bossGroups-1 {
			count += rem
		}
		for j := 0; j < count; j++ {
			mid := GuildRules.TrashMonsterIDs[idx%len(GuildRules.TrashMonsterIDs)]
			idx++
			m, err := dep.Catalog.Monster(mid)
			if err != nil {
				return nil, fmt.Errorf("小怪模板缺失 %d: %w", mid, err)
			}
			params := storyMonsterParams(m, 0, 0)
			scaleParamKeys(params, []string{"hp", "life"}, hpBP)
			scaleParamKeys(params, []string{"at", "atk", "seed_at"}, atBP)
			ms := g*interval*1000 + 500 + j*200
			wm[strconv.Itoa(ms)] = map[string]any{
				"id": mid, "scale": 1.0, "powerup_rate": 2.5,
				"gauge": false, "params": params,
			}
		}
	}
	return wm, nil
}

// gwMonsterDocs 收集 boss + 小怪引用的魔宠模板文档（monsters 键）。
func gwMonsterDocs(ids []int, dep Deps) (map[string]any, error) {
	monsters := map[string]any{}
	for _, mid := range ids {
		key := strconv.Itoa(mid)
		if _, ok := monsters[key]; ok {
			continue
		}
		m, err := dep.Catalog.Monster(mid)
		if err != nil {
			return nil, fmt.Errorf("魔宠模板缺失 %d: %w", mid, err)
		}
		monsters[key] = BattleMonsterDoc(m)
	}
	return monsters, nil
}

// buildGWExecuteDoc 会战 execute 文档（剧情 execute 键集同形下发；原服
// /battles/guild_quest_execute.json 为**扁平根对象**，data 只含 user+guild）。
func buildGWExecuteDoc(dep Deps, atk *GWAttack, units []UnitRecord,
	seeds []MonsterRecord, snap *PlayerSnapshot, gw *GWView) (map[string]any, error) {
	cat, err := loadQuestCatalog(dep.AssetsDir)
	if err != nil {
		return nil, fmt.Errorf("会战关卡目录不可用: %w", err)
	}
	// 防卫魔宠 → boss_appear_table（大怪，击杀完即 Clear）；
	// 小怪 → appear_table 单波时间轴（ms 出场，无总时长限制）。
	bossList, err := gwBossAppearList(atk, dep)
	if err != nil {
		return nil, err
	}
	bossGroups := (len(bossList) + 1) / 2
	trashWave, err := gwTrashAppearTable(atk, dep, bossGroups)
	if err != nil {
		return nil, err
	}
	appearTable := map[string]any{"1": trashWave}
	bossAppearTable := map[string]any{"1": bossList}
	monsterIDs := append([]int{}, MonsterIDsInAppearTable(appearTable)...)
	for _, d := range atk.DefenseSnapshot {
		monsterIDs = append(monsterIDs, d.MonsterID)
	}
	monsters, err := gwMonsterDocs(monsterIDs, dep)
	if err != nil {
		return nil, err
	}
	// 参战单位文档。
	skillsByUnit, err := skillsByUnitFor(dep, atk.UserID)
	if err != nil {
		return nil, err
	}
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
		applyUnitSkills(udoc, cu.Rare, r.DuplicatedCount, skillsByUnit[r.UnitID])
		if atk.Effects.Version > 0 {
			udoc["hp"] = int(float64(udoc["hp"].(int)) * atk.Effects.UnitHP)
			udoc["at"] = int(float64(udoc["at"].(int)) * atk.Effects.UnitAT)
			if toughness, ok := udoc["taghness"].(float64); ok {
				udoc["taghness"] = toughness * atk.Effects.UnitToughness
			}
		}
		unitDocs = append(unitDocs, udoc)
	}
	// asset_names：公共段 + 单位三件套 + 敌怪三件套 + 参战魔宠贴图 + 背景。
	assets := append([]string{}, cat.Defaults.AssetNamesCommon...)
	for _, r := range units {
		assets = append(assets,
			fmt.Sprintf("unit/unit_%d", r.UnitID),
			fmt.Sprintf("unit/unit_btn_%d", r.UnitID),
			fmt.Sprintf("unit/unit_large_%d", r.UnitID))
	}
	declared := map[string]bool{}
	for _, a := range assets {
		declared[a] = true
	}
	for _, ud := range unitDocs {
		udoc := ud.(map[string]any)
		for _, k := range []string{"attack_effect", "target_effect"} {
			e, _ := udoc[k].(string)
			if e != "" && !declared[e] {
				declared[e] = true
				assets = append(assets, e)
			}
		}
	}
	declaredMonster := map[int]bool{}
	for _, mid := range monsterIDs {
		if declaredMonster[mid] {
			continue
		}
		declaredMonster[mid] = true
		assets = append(assets,
			fmt.Sprintf("monster/monster_%d", mid),
			fmt.Sprintf("monster/monster_btn_%d", mid),
			fmt.Sprintf("monster/monster_large_ns_%d", mid))
	}
	for _, m := range seeds {
		if !declaredMonster[m.MonsterID] {
			declaredMonster[m.MonsterID] = true
			assets = append(assets,
				fmt.Sprintf("monster/monster_btn_%d", m.MonsterID),
				fmt.Sprintf("monster/monster_large_ns_%d", m.MonsterID))
		}
	}
	// 背景/BGM：原服 guild battle 用 background_id=20、timezone=3、
	// `background/background20_3`、`sound/B-6`。我们资源清单里
	// `background/background20_3` **不存在**（assets/cn350/bundles.txt 与
	// client/decoded/assets/unity/background/ 只有 background1_1）→ 下发会让
	// 客户端 Unknown asset 黑幕，故保留 background1_1 + timezone 1〔有意差异〕；
	// `sound/B-6` 在清单内，按原服换成 B-6（原服同期同值）。
	assets = append(assets, "background/background1_1", gwBattleBGM)
	// 单波：length 足够大让终点旗不可及，战斗只由 boss 清空
	// （OnBattleBossDead→Clear）或我方全灭终结；无 time_limit =
	// 无战斗总时长限制（客户端 Has() 保护，缺失即不限时）。
	// 原服 help_table 是**空对象**（样本 {}）；客户端只有
	// WaveTable.ContainsKey(currentWaveId) 命中才取协力支援（Quest.cs:3167），
	// 空对象 = 本战无协力支援，安全。
	helpTable := map[string]any{}
	timezone := map[string]any{"1": 1}
	waveConf := map[string]any{"1": map[string]any{"length": 100000}}
	bid := atk.BattleID
	initialSP := 500
	if atk.Effects.Version > 0 {
		initialSP = atk.Effects.InitialSP
	}
	// quest.id = **目标公会 id**（原服样本 b9b76114-… 即 battle_condition.guilds
	// 里的第 4 个会「没事溜溜圈」，同时是 ap_use_url/result_url 的 guild_id 参数）；
	// quest.name = "vs <目标会名>(<本次目标数>个公会)"。
	// 客户端文档池去重比对 quest.id+difficulty_id 与 pageMessage.data["id"]
	// （Quest.cs:637-643），而会战页消息只带 guild_id 不带 id → 池恒为 null，
	// 同目标连续出击不会复用旧文档（含旧 URL）。
	questID := atk.TargetGuildID
	targetName, groupCount := gwTargetLabel(atk, gw)
	questDoc := map[string]any{
		"id": questID, "name": fmt.Sprintf("vs %s(%d个公会)", targetName, groupCount),
		"mode":                "battle",
		"use_ap":              atk.BPCost,
		"difficulty_id":       atk.Difficulty,
		"difficulty_name":     gwQuestDifficultyName(guildWireNow(dep)),
		"background_id":       1,
		"initial_power":       initialSP,
		"unit_count_limit":    5,
		"seed_atack_rate":     gwSeedAtackRate,
		"help_base_sec":       gwHelpBaseSec,
		"help_sec_level_rate": gwHelpSecLevelRate,
		"unit_help_rate":      1.0,
		"continuable":         false,
		"wave_conf":           waveConf,
		"background":          cat.Defaults.Background,
		"powerup_conf":        cat.Defaults.PowerupConf,
		"weapon_rate_hash":    gwWeaponRateHashDoc(),
	}
	gwQuestWeaponRateFields(questDoc)
	// data.guild：原服 execute 回包下发己方公会完整文档（战果/排名/设施刷新源）。
	// 复用 guildDoc 单一实现；dep.Guild 未装配（单元测试）时不下发该键。
	data := map[string]any{"user": snap.User}
	if dep.Guild != nil {
		gv, gerr := dep.Guild.MyGuild(atk.UserID)
		if gerr != nil {
			return nil, fmt.Errorf("会战出击读公会视图失败: %w", gerr)
		}
		if gdoc := gwBattleGuildDoc(gv, bid); gdoc != nil {
			data["guild"] = gdoc
		}
	}
	// ap_use_url/result_url：原服精确路径 /battles/bp_use、/battles/attack
	// （契约 url_path）。参数沿原服形状 guild_id=<目标会>&difficulty_id=<难度>，
	// 并带我们自有的不可猜 bid（客户端整串回发，服务端按 bid 定位会话）。
	// 原服 URL 还有 c=<9 位数字>（样本 47626325，疑为会话校验码，三个录包样本
	// 值不同）；缺请求体无法判定客户端是否原样回传 → 不下发〔推断〕。
	callQuery := "?guild_id=" + url.QueryEscape(atk.TargetGuildID) +
		"&difficulty_id=" + url.QueryEscape(atk.Difficulty) +
		"&bid=" + url.QueryEscape(bid)
	return map[string]any{
		"user":              snap.User,
		"quest":             questDoc,
		"units":             unitDocs,
		"monsters":          monsters,
		"main_character":    cat.Defaults.MainCharacter,
		"appear_table":      appearTable,
		"boss_appear_table": bossAppearTable,
		"help_table":        helpTable,
		"timezone_table":    timezone,
		"asset_names":       assets,
		"bgm_name":          gwBattleBGM,
		"exp":               0,
		"clear_count":       0,
		"flag_image_name":   gwFlagImageName,
		// rush_started/rush_ended 原服类型为 null（契约 want:"null"）：会战不开
		// fever/rush 模式（Quest.cs:2691/3067 只在非 null/true 时播 rush 演出），
		// 显式下发 null 而不是省略键（客户端 b(key) 对缺失与 null 同为 false）。
		"rush_started":                nil,
		"rush_ended":                  nil,
		"seed_attribute_rates":        gwSeedAttributeRatesDoc(),
		"guild_asset_help_level_base": gwGuildAssetHelpLevelBase,
		"battle_rank_help_level_rate": gwBattleRankHelpLevelRate,
		"battle_help_initial_level":   gwGuildAssetHelpLevelBase * gwBattleRankHelpLevelRate,
		"ap_use_url":                  "/battles/bp_use" + callQuery,
		"result_url":                  "/battles/attack" + callQuery,
		// lose_url：原服 guild battle 的 execute 不回该键（客户端 SyncResultToServer
		// 见缺键即不报败北，SWFQuestLose.cs:242-248）。我们保留它复用既有幂等
		// 败北上报（/battles/lose），属**有意差异**：不保留则负场只能等靠关场
		// 懒结算，个人贡献台账缺失。
		"lose_url":          "/battles/lose?bid=" + url.QueryEscape(bid),
		"next_page_message": "name=GuildTop&base=Common/Pages/GuildTop",
		"battle_log":        false,
		"data":              data,
	}, nil
}

// gwUseAPHandler /battles/use_ap 与 /battles/bp_use（原服 BP 付点）**同一处理器**：
// 都走 GWAttackPay 的 prepared→bp_paid 条件更新，BP 已在 GWAttackOpen 开战时扣，
// 重放/终态一律视为成功（幂等），不另造第二套扣点。原服 /battles/bp_use 回包只有
// {"status":"success"}（decrypted/1/battles_bp_use.json），这里保留 data.user 供
// 客户端缓存刷新（多余键客户端忽略）。
func gwUseAPHandler(dep Deps, endpoint string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": endpoint, "bid": bid,
		}
		if bid == "" {
			return cn35wire.MarshalJSON(statusDoc("error"))
		}
		atk, err := dep.Guild.GWAttackPay(req.UserID, bid)
		switch {
		case errors.Is(err, ErrGWAttackNotFound), errors.Is(err, ErrGWAttackState):
			return cn35wire.MarshalJSON(statusDoc("error"))
		case err != nil:
			return nil, err
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		c.Decoded["status"] = atk.Status
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   map[string]any{"user": snap.User},
		})
	})
}

// gwResultHandler /battles/result（重建路径）与 /battles/attack（原服胜利上报）
// **同一处理器**：都走 GWAttackSettle 结算事务与幂等重放，不另造第二套结算。
//
// 回包按原服 /battles/attack 骨架下发（decrypted/1/battles_attack.json）：
// `{status, total_gp, party_event:null, data:{messages,user,battle_condition}}`。
// 结构在所有路径（含 error）保持一致：客户端 SWFQuestClear 只认
// success/already_received，其余键仅用于缓存刷新与结算屏展示；错误路径回
// status=error + total_gp 0，不伪造成功。
func gwResultHandler(dep Deps, endpoint string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": endpoint,
			"bid": bid, "time": req.Query.Get("time"), "mc": req.Query.Get("mc"),
		}
		status := "error"
		var res *GWSettleResult
		if bid != "" {
			r, err := dep.Guild.GWAttackSettle(req.UserID, bid, true, 0)
			switch {
			case errors.Is(err, ErrGWAttackNotFound), errors.Is(err, ErrGWAttackState):
				// 伪造/跨账号 bid 或状态不允许：保持 status=error（客户端进入其
				// 重试循环即拒绝语义；合法客户端不会走到）。
			case err != nil:
				return nil, err
			default:
				res = r
				status = "success"
				if r.Already {
					status = "already_received"
				}
			}
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		gw, gwerr := dep.Guild.GWState(req.UserID, guildWireNow(dep))
		if gwerr != nil {
			return nil, gwerr
		}
		// 结算屏展示键（SWFQuestClear.cs:381-407）：total_gp = 固定胜场 GP +
		// 当场掠夺 GP，spend_time 回显客户端上行用时。
		var totalGP int64
		if res != nil {
			totalGP = res.GPDelta + res.Plundered
		}
		c.Decoded["status"] = status
		return cn35wire.MarshalJSON(map[string]any{
			"status": status,
			"data": map[string]any{
				"user":             snap.User,
				"messages":         gwBattleMessagesDoc(),
				"battle_condition": battleConditionDoc(gw),
			},
			"total_gp":          totalGP,
			"party_event":       nil,
			"next_page_message": "name=GuildTop&base=Common/Pages/GuildTop",
			"spend_time":        int(queryFloat(req.Query, "time")),
		})
	})
}

// gwLoseHandler /battles/lose：败北上报（幂等）。
func gwLoseHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "battles_lose", "bid": bid,
		}
		if bid == "" {
			return cn35wire.MarshalJSON(statusDoc("error"))
		}
		_, err := dep.Guild.GWAttackSettle(req.UserID, bid, false, 0)
		switch {
		case errors.Is(err, ErrGWAttackNotFound), errors.Is(err, ErrGWAttackState):
			return cn35wire.MarshalJSON(statusDoc("error"))
		case err != nil:
			return nil, err
		}
		return cn35wire.MarshalJSON(statusDoc("success"))
	})
}

// registerGuildBattleRoutes 会战战斗链路由（help/pray/execute/付点/结算/原路径别名）。
//
// 原路径兼容（GUILDCLOSE350/GC-05）：/battles/bp_use 与 /battles/attack 是原服
// 录包里的精确 POST 路径（contract.json 的 method=POST）；注册用 GET 走
// guildRegister 的 GET+POST 双注册约定（客户端整串回发 execute 下发的 URL）。
// 二者与 /battles/use_ap、/battles/result **共用同一处理器**，不另造第二套
// 扣点/结算或幂等逻辑。
func registerGuildBattleRoutes(reg guildRegister, dep Deps) {
	reg(http.MethodGet, "/battles/help", "会战应援", gwHelpPrayHandler(dep, "help"))
	reg(http.MethodGet, "/battles/pray", "会战祈祷", gwHelpPrayHandler(dep, "pray"))
	reg(http.MethodGet, "/battles/guild_quest_execute.json", "会战出战建档", gwExecuteHandler(dep))
	reg(http.MethodGet, "/battles/use_ap", "会战 BP 付点（幂等）", gwUseAPHandler(dep, "battles_use_ap"))
	reg(http.MethodGet, "/battles/bp_use", "原服 BP 付点（=/battles/use_ap 同事务）", gwUseAPHandler(dep, "battles_bp_use"))
	reg(http.MethodGet, "/battles/result", "会战胜利结算", gwResultHandler(dep, "battles_result"))
	reg(http.MethodGet, "/battles/attack", "原服胜利上报（=/battles/result 同事务）", gwResultHandler(dep, "battles_attack"))
	reg(http.MethodGet, "/battles/lose", "会战败北上报", gwLoseHandler(dep))
}
