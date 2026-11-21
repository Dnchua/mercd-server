// ladder_wire.go M10 癒术士大赛读写面（契约见 docs/18 §2）。
package cn35game

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

var pvpAttrNames = []string{"火", "水", "风", "光", "暗"}

func ladderRuleInfo(per LadderPeriod) string {
	end := per.SeasonEnd.In(pvpLoc)
	firstWeekEnd := per.SeasonEnd.Add(-time.Duration(LadderRules.SeasonWeeks-1) * 7 * 24 * time.Hour).In(pvpLoc)
	lines := []string{
		fmt.Sprintf("第%d届癒术士大赛由王国主办，完成战斗教学且达到%d级以上癒术士均可参加", per.SeasonNo, LadderRules.MinLevel),
		fmt.Sprintf("本届癒术士大赛截至时间为：%d年%02d月%02d日", end.Year(), end.Month(), end.Day()),
		fmt.Sprintf("大赛第一周为排位赛（截止时间：%d年%02d月%02d日），其余时间为分段赛", firstWeekEnd.Year(), firstWeekEnd.Month(), firstWeekEnd.Day()),
		fmt.Sprintf("比赛期间，参赛癒术士可以挑战积分相近的其他参赛者，每日%d次免费挑战机会", LadderRules.DailyFree),
		"战斗方式为使用同伴队伍挑战对方防御魔宠，与公会战形式基本相同",
		"初次参赛需要先设定至少1只防御魔宠，防御魔宠变更实时生效",
		"挑战成功即可获得积分和掉落奖励，排行榜根据参赛者获得积分进行排名",
		"战胜积分根据双方分差进行计算，挑战积分越高的对手，战胜可获得积分越高",
		"被其他参赛者挑战并且防守失败，则有可能失去部分积分",
		"每周二21点根据参赛者所在分段结算上周奖励，结算后需要在一周以内领取，超过时间则无法再领取",
		"只有当周参加过挑战的癒术士才会获得结算奖励",
		fmt.Sprintf("分段按积分划分：SS级%d分以上、S级%d分以上、A级%d分以上，其余为B级", LadderRules.TierScore["SS"], LadderRules.TierScore["S"], LadderRules.TierScore["A"]),
		"赛季结束后排名和积分将全部清零",
	}
	for i := range lines {
		lines[i] = fmt.Sprintf("%d.%s", i+1, lines[i])
	}
	return strings.Join(lines, "\n\n")
}

func ladderRankOf(rows []LadderRankRow, userID string) int {
	for i, r := range rows {
		if r.UserID == userID {
			return i + 1
		}
	}
	return 0
}

func ladderMainData(dep Deps, userID string) (map[string]any, error) {
	view, err := dep.PvP.LadderView(userID)
	if err != nil {
		return nil, err
	}
	snap, err := dep.Accounts.Snapshot(userID)
	if err != nil {
		return nil, err
	}
	ranking, err := dep.PvP.LadderRanking(200)
	if err != nil {
		return nil, err
	}
	per := LadderPeriodAt(pvpNow(dep))
	p := view.Profile
	myTier := LadderTierOf(p.Score)
	rating := myTier
	if p.Score == 0 && p.AtkTotal == 0 {
		rating = "---"
	}
	myRank := ladderRankOf(ranking, userID)
	rankStr := "未上榜"
	if myRank > 0 {
		rankStr = strconv.Itoa(myRank)
	}
	// 前三名补足：客户端 LadderMainRankListPart 只填已有行、不隐藏缺行，
	// 真实上榜不足 3 人时露出 prefab 默认占位（设备实测）。以候选同源确定性
	// NPC 补足至 3 人：先取同分段 NPC（按积分降序、idx 稳定），仍不足再
	// 按积分降序补，保证每行恒 3 条。show_rank 排行榜仍只列真实账号。
	lvl := toInt(snap.User["level"])
	npcPool := make([]*PvPOpponent, 0, LadderRules.CandidateMax)
	for i := 0; i < LadderRules.CandidateMax; i++ {
		npcPool = append(npcPool, LadderNPC(dep.Catalog, pvpUnitPool(dep), per.SeasonKey, i, p.Score, lvl))
	}
	sort.SliceStable(npcPool, func(a, b int) bool { return npcPool[a].Score > npcPool[b].Score })
	top3 := func(tier string) []any {
		out := []any{}
		for _, r := range ranking {
			if tier != "" && LadderTierOf(r.Score) != tier {
				continue
			}
			out = append(out, map[string]any{"icon": unitSquare(r.LeaderUnitID), "name": r.Name})
			if len(out) == 3 {
				break
			}
		}
		if len(out) < 3 {
			used := map[string]bool{}
			pad := func(matchTier bool) {
				for _, n := range npcPool {
					if len(out) == 3 {
						return
					}
					if used[n.UserID] || (matchTier && tier != "" && LadderTierOf(n.Score) != tier) {
						continue
					}
					used[n.UserID] = true
					out = append(out, map[string]any{"icon": unitSquare(n.LeaderUnitID), "name": n.Name})
				}
			}
			pad(true)
			pad(false)
		}
		return out
	}
	info := []any{}
	rankIndex := 0
	if per.WeekInSeason == 0 {
		info = append(info, map[string]any{
			"bg": "common/ladder_bg", "ladder_name": "排位竞技场", "up_label": "掉落：随机增幅药水",
			"down_label":  fmt.Sprintf("当前排名：%s  当前积分: %d", rankStr, p.Score),
			"player_list": top3(""),
		})
	} else {
		for i, t := range LadderTiers {
			down := "当前排名：-  当前积分: -"
			if t == myTier {
				rankIndex = i
				down = fmt.Sprintf("当前排名：%s  当前积分: %d", rankStr, p.Score)
			}
			info = append(info, map[string]any{
				"bg": "common/ladder_bg", "ladder_name": t + "级竞技场", "up_label": "掉落：随机增幅药水",
				"down_label": down, "player_list": top3(t),
			})
		}
	}
	deckEmpty := view.Deck == ([5]*MonsterRecord{})
	hasRewards := "false"
	if len(view.Pending) > 0 {
		hasRewards = "true"
	}
	return map[string]any{
		"has_ladder_rewards":           hasRewards,
		"ladder_condition":             ladderConditionDoc(),
		"ladder_main_page_disable_key": map[string]any{"ladder_deck_empty": deckEmpty},
		"ladder_main_page": map[string]any{
			"player_name": snap.User["name"],
			"rank_index":  rankIndex,
			"player_info_left": fmt.Sprintf("当前积分评级：%s\n\n今日已挑战次数：%d\n\n今日被挑战次数：%d",
				rating, p.DayUsed, p.DayDefended),
			"player_info_right": fmt.Sprintf("战胜最高积分：%d\n\n挑战胜率：%d%%\n\n被挑战胜率：%d%%",
				p.BestWinScore, pctOf(p.AtkWin, p.AtkTotal), pctOf(p.DefWin, p.DefTotal)),
			"rule_info":  ladderRuleInfo(per),
			"rule_title": "规则说明",
			"btn_list": []any{
				map[string]any{"text": "开始挑战", "visible": true, "enable": true,
					"onClick": "name=LadderChallenger&method=Stack", "disable_key": "ladder_deck_empty",
					"disable_title": "提示", "disable_body": "至少要设置一只防御魔宠才可以发起战斗哟",
					"disable_page": "name=LadderDefenceConfig&method=Stack"},
				map[string]any{"text": "防御设定", "visible": true, "enable": true, "onClick": "name=LadderDefenceConfig&method=Stack"},
				map[string]any{"text": "规则说明", "visible": true, "enable": true, "onClick": "ShowRule"},
				map[string]any{"text": "排行榜", "visible": true, "enable": true,
					"onClick": "name=LadderRankingList&base=Common/Pages/RankingList&method=Stack&overlayModal=show&key=ladder_ranking_list"},
				map[string]any{"text": "魔宠增幅", "visible": true, "enable": true,
					"onClick": "name=LadderMonsterGrow&base=Common/Pages/MonsterGrow&method=Stack"},
				map[string]any{"text": "敬请期待", "visible": true, "enable": false},
			},
			"ladder_info": info,
		},
		"user": snap.User,
	}, nil
}

func ladderConditionDoc() map[string]any {
	out := map[string]any{}
	for _, w := range gwWeaponRateNames {
		out[w+"_rate"] = 1
	}
	return out
}

func ladderInfoHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "ladders_show_ladder_info"}
		data, err := ladderMainData(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

func pvpSnapLevel(dep Deps, userID string) (*PlayerSnapshot, int, error) {
	snap, err := dep.Accounts.Snapshot(userID)
	if err != nil {
		return nil, 0, err
	}
	return snap, toInt(snap.User["level"]), nil
}

// ladderResolveOpponent NPC 按 id 重算（只认本届）；真实账号读库。
func ladderResolveOpponent(dep Deps, userID, oppID string, myScore, myLevel int) (*PvPOpponent, error) {
	if kind, period, idx, ok := ParseNPCOpponentID(oppID); ok {
		per := LadderPeriodAt(pvpNow(dep))
		if kind != "ladder" || period != per.SeasonKey || idx >= LadderRules.CandidateMax {
			return nil, ErrPvPOpponent
		}
		return LadderNPC(dep.Catalog, pvpUnitPool(dep), per.SeasonKey, idx, myScore, myLevel), nil
	}
	if oppID == "" {
		return nil, ErrPvPOpponent
	}
	return dep.PvP.LadderPlayer(userID, oppID)
}

func ladderCandidatesHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "ladders_get_candidates"}
		view, err := dep.PvP.LadderView(req.UserID)
		if err != nil {
			return nil, err
		}
		_, lvl, err := pvpSnapLevel(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		opps, err := dep.PvP.LadderPlayers(req.UserID, LadderRules.CandidateMax)
		if err != nil {
			return nil, err
		}
		per := LadderPeriodAt(pvpNow(dep))
		for i := 0; len(opps) < LadderRules.CandidateMax; i++ {
			opps = append(opps, *LadderNPC(dep.Catalog, pvpUnitPool(dep), per.SeasonKey, i, view.Profile.Score, lvl))
		}
		sort.SliceStable(opps, func(a, b int) bool { return opps[a].Score > opps[b].Score })
		rows := make([]any, 0, len(opps))
		for _, o := range opps {
			guild := o.GuildName
			if guild == "" {
				guild = "无"
			}
			rows = append(rows, map[string]any{
				"icon": unitSquare(o.LeaderUnitID), "name": o.Name, "tip": "", "btn_visible": true,
				"info": fmt.Sprintf("%d\n%d\n%s", o.Score, o.Level, guild),
				"message": map[string]any{
					"addition": "user_id=" + url.QueryEscape(o.UserID),
					"partials": map[string]any{"DifficultyMainBar": map[string]any{"option": map[string]any{
						"MainBar": map[string]any{"widgets": map[string]any{"label": map[string]any{"text": o.Name}}}}}},
				},
			})
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{"candidates": rows}})
	})
}

func ladderDeckDoc(dep Deps, deck [5]*MonsterRecord) (map[string]any, error) {
	out := map[string]any{}
	for i, m := range deck {
		if m == nil {
			out[strconv.Itoa(i)] = map[string]any{}
			continue
		}
		cm, err := dep.Catalog.Monster(m.MonsterID)
		if err != nil {
			out[strconv.Itoa(i)] = map[string]any{}
			continue
		}
		doc, err := buildMonsterDoc(*m, cm)
		if err != nil {
			return nil, err
		}
		out[strconv.Itoa(i)] = doc
	}
	return out, nil
}

func ladderDeckHandler(dep Deps, op string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		q := req.Query
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "ladders_deck", "op": op,
			"party_index": q.Get("party_index"), "uuid": q.Get("uuid")}
		slot, _ := strconv.Atoi(q.Get("party_index"))
		var err error
		switch op {
		case "set":
			err = dep.PvP.LadderSetDeck(req.UserID, slot, q.Get("uuid"))
		case "remove":
			err = dep.PvP.LadderRemoveDeck(req.UserID, slot)
		}
		if m := pvpErrModal(err); m != nil {
			return cn35wire.MarshalJSON(m)
		} else if err != nil {
			return nil, err
		}
		view, err := dep.PvP.LadderView(req.UserID)
		if err != nil {
			return nil, err
		}
		deck, err := ladderDeckDoc(dep, view.Deck)
		if err != nil {
			return nil, err
		}
		rates := make([]any, len(LadderRules.SlotRates))
		for i, r := range LadderRules.SlotRates {
			rates[i] = r
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{
			"ladder_deck":      deck,
			"ladder_deck_data": map[string]any{"powerup_rate": rates},
			"ladder_main_page_disable_key": map[string]any{
				"ladder_deck_empty": view.Deck == ([5]*MonsterRecord{})},
		}})
	})
}

func monsterRealScale(m *MonsterRecord) float64 {
	for _, p := range []*string{m.RealScale, m.Scale} {
		if p != nil {
			if f, err := strconv.ParseFloat(*p, 64); err == nil && f > 0 {
				return f
			}
		}
	}
	return 1
}

// ladderBossList 对手防御魔宠 → boss_appear_table 波次 1（doc18 §2.2）。
func ladderBossList(dep Deps, opp *PvPOpponent, diff LadderDifficulty) ([]any, []int, error) {
	out := []any{}
	ids := []int{}
	for i, m := range opp.Deck {
		if m == nil {
			continue
		}
		cm, err := dep.Catalog.Monster(m.MonsterID)
		if err != nil || cm == nil {
			continue
		}
		scale := monsterRealScale(m)
		attr := cm.Element
		growRate := func(t int) float64 {
			if attr < 1 || attr > 5 {
				return 0
			}
			steps := opp.Grow[[2]int{t, attr}]
			return float64(steps*LadderGrowTabs[t-1].Step) / 100
		}
		out = append(out, map[string]any{
			"id": m.MonsterID, "scale": scale, "footY": 0,
			"rewards":                      []any{map[string]any{"key": "power", "count": LadderRules.BossPower}},
			"powerup_rate":                 math.Round(scale*diff.Factor*LadderRules.SlotRates[i]*10000) / 10000,
			"gauge":                        true,
			"interval_sec":                 diff.IntervalSec,
			"monster_grow_hp_powerup_rate": growRate(1),
			"monster_grow_at_powerup_rate": growRate(2),
		})
		ids = append(ids, m.MonsterID)
	}
	return out, ids, nil
}

// ladderTrashTable 杂兵：1000ms 起每 4000ms 一只，首只携带本场掉落（不含潜能叶子）。
func ladderTrashTable(dep Deps, drops LadderReward, battleID string) map[string]any {
	r := pvpSeed("ladder-trash", battleID)
	wave := map[string]any{}
	for i := 0; i < LadderRules.TrashCount; i++ {
		rewards := []any{map[string]any{"key": "power", "count": LadderRules.TrashPower}}
		if i == 0 {
			for k := 0; k < 3; k++ {
				for n := 0; n < drops.Potions[k]; n++ {
					rewards = append(rewards, map[string]any{"key": "other", "type_id": 10007, "id": k + 1,
						"count": 1, "icon": fmt.Sprintf("common_icon/potion_%d", k+1)})
				}
			}
			rewards = append(rewards,
				map[string]any{"key": "guild_asset", "count": drops.GuildAsset},
				map[string]any{"key": "gacha_point", "count": drops.GachaPoint})
		}
		id := LadderRules.TrashIDs[r.Intn(len(LadderRules.TrashIDs))]
		wave[strconv.Itoa(LadderRules.TrashStartMs+i*LadderRules.TrashGapMs)] = map[string]any{
			"id": id, "scale": math.Round((1+r.Float64()*0.17)*100) / 100, "rewards": rewards,
		}
	}
	return map[string]any{"1": wave}
}

func ladderExecuteHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		q := req.Query
		diffID := q.Get("difficulty_id")
		if diffID == "" {
			diffID = "normal"
		}
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "ladders_execute",
			"difficulty_id": diffID, "opponent": q.Get("user_id"), "party_id": q.Get("party_id")}
		diff, ok := LadderDifficulties[diffID]
		if !ok {
			return cn35wire.MarshalJSON(pvpErrModal(ErrPvPDifficulty))
		}
		ordinal, err := ParsePartyOrdinal(q.Get("party_id"))
		if err != nil {
			ordinal = 1
		}
		snap, lvl, err := pvpSnapLevel(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		if lvl < LadderRules.MinLevel {
			return cn35wire.MarshalJSON(pvpErrModal(ErrPvPLevel))
		}
		view, err := dep.PvP.LadderView(req.UserID)
		if err != nil {
			return nil, err
		}
		opp, err := ladderResolveOpponent(dep, req.UserID, q.Get("user_id"), view.Profile.Score, lvl)
		if m := pvpErrModal(err); m != nil {
			return cn35wire.MarshalJSON(m)
		} else if err != nil {
			return nil, err
		}
		if opp.Deck == ([5]*MonsterRecord{}) {
			return cn35wire.MarshalJSON(pvpErrModal(ErrPvPOpponent))
		}
		drops := LadderRollDrops(battleRand(dep))
		sess, units, seeds, err := dep.PvP.LadderOpen(req.UserID, opp, diffID, ordinal, drops)
		if m := pvpErrModal(err); m != nil {
			return cn35wire.MarshalJSON(m)
		} else if err != nil {
			return nil, err
		}
		doc, err := buildLadderExecuteDoc(dep, sess, opp, diff, units, seeds, snap)
		if err != nil {
			return nil, err
		}
		c.Decoded["battle_id"] = sess.BattleID
		return cn35wire.MarshalJSON(doc)
	})
}

func buildLadderExecuteDoc(dep Deps, sess *LadderSession, opp *PvPOpponent, diff LadderDifficulty,
	units []UnitRecord, seeds []MonsterRecord, snap *PlayerSnapshot) (map[string]any, error) {
	cat, err := loadQuestCatalog(dep.AssetsDir)
	if err != nil {
		return nil, fmt.Errorf("癒术士大赛关卡目录不可用: %w", err)
	}
	bosses, bossIDs, err := ladderBossList(dep, opp, diff)
	if err != nil {
		return nil, err
	}
	appear := ladderTrashTable(dep, sess.Rewards, sess.BattleID)
	monsterIDs := append(MonsterIDsInAppearTable(appear), bossIDs...)
	monsters, err := gwMonsterDocs(monsterIDs, dep)
	if err != nil {
		return nil, err
	}
	unitDocs, err := pvpUnitDocs(dep, sess.UserID, units)
	if err != nil {
		return nil, err
	}
	assets := pvpAssetNames(cat.Defaults.AssetNamesCommon, units, unitDocs, monsterIDs, seeds, nil,
		gwFlagImageName)
	quest := map[string]any{
		"id": opp.UserID, "name": "vs " + opp.Name, "mode": "ladder",
		"difficulty_id": sess.Difficulty, "difficulty_name": "挑战", "use_ap": 0,
		"background_id": 1, "background": cat.Defaults.Background, "powerup_conf": cat.Defaults.PowerupConf,
		"initial_power": LadderRules.InitialPower, "unit_count_limit": 5,
		"wave_conf": map[string]any{"1": map[string]any{
			"length": LadderRules.WaveLength, "time_limit": LadderRules.TimeLimitSec}},
		"help_base_sec": gwHelpBaseSec, "help_sec_level_rate": gwHelpSecLevelRate,
		"continuable": false, "seed_atack_rate": gwSeedAtackRate, "unit_help_rate": 1.0,
	}
	for _, w := range gwWeaponRateNames {
		quest[w+"_rate"] = 1
	}
	bid := "?difficulty_id=" + url.QueryEscape(sess.Difficulty) + "&bid=" + url.QueryEscape(sess.BattleID)
	return map[string]any{
		"user": snap.User, "asset_names": assets, "flag_image_name": gwFlagImageName,
		"quest": quest, "exp": LadderRules.Exp, "timezone_table": map[string]any{"1": 1},
		"boss_appear_table": map[string]any{"1": bosses}, "appear_table": appear,
		"help_table": map[string]any{}, "main_character": cat.Defaults.MainCharacter,
		"monsters": monsters, "units": unitDocs, "bgm_name": gwBattleBGM,
		"result_url": "/ladders/attack" + bid, "ap_use_url": "/ladders/lc_used" + bid,
		"lose_url":          "/ladders/lose" + bid,
		"next_page_message": "name=LadderMainPage&base=Common/Pages/LadderMainPage",
		"rush_started":      nil, "rush_ended": nil, "is_ladder": true, "clear_count": 0, "battle_log": false,
		"seed_attribute_rates": gwSeedAttributeRatesDoc(),
		"data":                 map[string]any{"user": snap.User},
	}, nil
}

// pvpAssetNames 公共段 + 我方/敌方单位三件套 + 特效 + 魔宠 + 背景/BGM + extra 追加。
// 客户端 Quest.OnPrepare 对 asset_names 逐项 textures.Add，任何重复即
// ArgumentException 整战加载失败，故公共段与 extra 同样走 add 去重。
func pvpAssetNames(common []string, units []UnitRecord, unitDocs []any, monsterIDs []int,
	seeds []MonsterRecord, extraUnits []UnitRecord, extra ...string) []string {
	assets := []string{}
	seen := map[string]bool{}
	add := func(a string) {
		if a != "" && !seen[a] {
			seen[a] = true
			assets = append(assets, a)
		}
	}
	for _, a := range common {
		add(a)
	}
	for _, r := range append(append([]UnitRecord{}, units...), extraUnits...) {
		add(fmt.Sprintf("unit/unit_%d", r.UnitID))
		add(fmt.Sprintf("unit/unit_btn_%d", r.UnitID))
		add(fmt.Sprintf("unit/unit_large_%d", r.UnitID))
	}
	for _, ud := range unitDocs {
		if d, ok := ud.(map[string]any); ok {
			for _, k := range []string{"attack_effect", "target_effect"} {
				if e, _ := d[k].(string); e != "" {
					add(e)
				}
			}
		}
	}
	for _, id := range monsterIDs {
		add(fmt.Sprintf("monster/monster_%d", id))
		add(fmt.Sprintf("monster/monster_btn_%d", id))
		add(fmt.Sprintf("monster/monster_large_ns_%d", id))
	}
	for _, m := range seeds {
		add(fmt.Sprintf("monster/monster_btn_%d", m.MonsterID))
		add(fmt.Sprintf("monster/monster_large_ns_%d", m.MonsterID))
	}
	add("background/background1_1")
	add(gwBattleBGM)
	for _, a := range extra {
		add(a)
	}
	return assets
}

func ladderPayHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "ladders_lc_used", "bid": bid}
		_, err := dep.PvP.LadderPay(req.UserID, bid)
		switch {
		case errors.Is(err, ErrPvPSession), errors.Is(err, ErrPvPState), errors.Is(err, ErrPvPNoCount):
			return cn35wire.MarshalJSON(statusDoc("error"))
		case err != nil:
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{"status": "success",
			"data": map[string]any{"user": snap.User, "user_coin": snap.UserCoin}})
	})
}

func ladderSettleHandler(dep Deps, win bool) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "ladders_settle", "bid": bid, "win": win}
		res, err := dep.PvP.LadderSettle(req.UserID, bid, win)
		status := "success"
		switch {
		case errors.Is(err, ErrPvPSession), errors.Is(err, ErrPvPState):
			status = "error"
		case err != nil:
			return nil, err
		case res.Already:
			status = "already_received"
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		doc := map[string]any{
			"status":            status,
			"next_page_message": "name=LadderMainPage&base=Common/Pages/LadderMainPage",
			"spend_time":        int(queryFloat(req.Query, "time")),
			"data":              map[string]any{"user": snap.User},
		}
		if res != nil && res.Win && res.Gain > 0 {
			doc["ladder_score"] = res.Gain
			doc["ladder_score_text"] = fmt.Sprintf("积分 +%d（当前 %d）", res.Gain, res.Score)
		}
		c.Decoded["status"] = status
		return cn35wire.MarshalJSON(doc)
	})
}

func ladderGrowDoc(view *LadderView) map[string]any {
	keys := []any{}
	tabs := map[string]any{}
	for _, t := range LadderGrowTabs {
		keys = append(keys, t.Key)
		list := []any{}
		for a := 1; a <= 5; a++ {
			steps := view.Grow[[2]int{t.Type, a}]
			list = append(list, map[string]any{
				"rate": steps * t.Step, "max_rate": t.MaxRate,
				"info":         fmt.Sprintf(t.InfoFmt, pvpAttrNames[a-1]),
				"url":          fmt.Sprintf("ladders/monster_grow_upgrade?grow_type=%d&attr_id=%d&count=", t.Type, a),
				"price":        LadderGrowPrice(view.Grow, t.Type, a),
				"step":         t.Step,
				"remain_count": t.MaxRate/t.Step - steps,
			})
		}
		tabs[t.Key] = map[string]any{
			"tab_name": t.TabName, "item_name": t.ItemName, "item_icon": t.Icon,
			"item_count": view.Profile.Potions[t.Type-1],
			"reset_url":  fmt.Sprintf("ladders/monster_grow_upgrade_reset?tab_id=%d", t.Type),
			"reset_text": "重置", "list": list,
		}
	}
	return map[string]any{
		"bg_texture": "common/monster_grow_bg", "detail_title": "规则说明",
		"detail_body": "1.魔宠增幅器集成多国最新技术研制而成，可以为个人全体防御魔宠提供属性加成\n" +
			"2.增幅器需要注入特定药水才能产生增幅效果，提升不同属性需要注入不同的药水\n" +
			"3.每种元素类型的魔宠增幅部件不同，需要分别进行升级\n" +
			"4.增幅部件存在魔阻，已升级一种元素类型后，升级另一种元素类型的药水消耗会增加\n" +
			"5.增幅器可以重置，重置后返还全部药水，可以重新进行升级\n" +
			"6.增幅效果在防御魔宠被挑战时生效；药水可在癒术士大赛战胜时获得",
		"tab_key_list": keys, "tab_data_list": tabs,
	}
}

func ladderGrowHandler(dep Deps, op string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		q := req.Query
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "ladders_grow", "op": op}
		var err error
		switch op {
		case "upgrade":
			t, _ := strconv.Atoi(q.Get("grow_type"))
			a, _ := strconv.Atoi(q.Get("attr_id"))
			n, _ := strconv.Atoi(q.Get("count"))
			err = dep.PvP.LadderGrowUpgrade(req.UserID, t, a, n)
		case "reset":
			t, _ := strconv.Atoi(q.Get("tab_id"))
			err = dep.PvP.LadderGrowReset(req.UserID, t)
		}
		if m := pvpErrModal(err); m != nil {
			return cn35wire.MarshalJSON(m)
		} else if err != nil {
			return nil, err
		}
		view, err := dep.PvP.LadderView(req.UserID)
		if err != nil {
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{
			"monster_grow": ladderGrowDoc(view), "user": snap.User}})
	})
}

func ladderRewardBody() string {
	lines := []string{"1.每周二21时按上周结束时的分段结算奖励（当周需参加过挑战）", "2.奖励需在结算一周以内领取", "3.各分段奖励设定如下："}
	for _, t := range LadderTiers {
		r := LadderRules.TierReward[t]
		lines = append(lines, fmt.Sprintf("%s级  金币x%d 徽章x%d", t, r.GachaPoint, r.Medal))
	}
	return strings.Join(lines, "\n")
}

func ladderRankDoc(dep Deps, userID string) (map[string]any, error) {
	view, err := dep.PvP.LadderView(userID)
	if err != nil {
		return nil, err
	}
	rows, err := dep.PvP.LadderRanking(100)
	if err != nil {
		return nil, err
	}
	per := LadderPeriodAt(pvpNow(dep))
	all := []any{}
	for i, r := range rows {
		all = append(all, map[string]any{"rank": i + 1, "head_icon": unitSquare(r.LeaderUnitID), "name": r.Name,
			"info": fmt.Sprintf("等级：%d\n积分：%d", r.Level, r.Score), "user_id": r.UserID})
	}
	end := per.WeekEnd.In(pvpLoc)
	rankName := LadderTierOf(view.Profile.Score) + "级排行"
	if per.WeekInSeason == 0 {
		rankName = "排位赛排行"
	}
	return map[string]any{
		"tab_key_list": []any{"ladder_rank"},
		"rank_list_data": map[string]any{"ladder_rank": map[string]any{
			"max_show_num": 99999, "reward_texture": "common/miracle_happy",
			"can_get_reward": len(view.Pending) > 0, "url": "ladders/fetch_rewards",
			"reward_btn_label": "领取排行奖励",
			"info":             fmt.Sprintf("结算时间：%d月%d日21时", end.Month(), end.Day()),
			"more":             "更多细节", "rank_name": rankName,
			"detail_title": "排行奖励", "detail_body": ladderRewardBody(),
			"all_list": all, "friend_list": []any{},
		}},
		"other": false,
	}, nil
}

func ladderRankHandler(dep Deps, claim bool) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "ladders_rank", "claim": claim}
		var modal map[string]any
		if claim {
			got, err := dep.PvP.LadderClaimRewards(req.UserID)
			if m := pvpErrModal(err); m != nil {
				return cn35wire.MarshalJSON(m)
			} else if err != nil {
				return nil, err
			}
			var gp, medal int
			for _, s := range got {
				gp += s.Reward.GachaPoint
				medal += s.Reward.Medal
			}
			modal = map[string]any{"title": "领取成功", "body": fmt.Sprintf("获得金币x%d、徽章x%d", gp, medal)}
		}
		doc, err := ladderRankDoc(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		data := map[string]any{"ladder_ranking_list": doc}
		out := map[string]any{"data": data}
		if claim {
			snap, err := dep.Accounts.Snapshot(req.UserID)
			if err != nil {
				return nil, err
			}
			data["user"] = snap.User
			data["has_ladder_rewards"] = "false"
			out["modal"] = modal
		}
		return cn35wire.MarshalJSON(out)
	})
}
