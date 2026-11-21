// rob_wire.go M10 公会训练（/rob_battles/*、/user_rob_decks*）读写面（契约见 docs/18 §3）。
package cn35game

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

const robMainPage = "name=RobBattleMainPage&method=Stack"

func robEnterDoc(p RobProfile) map[string]any {
	enters := []any{}
	for i, price := range RobRules.TicketPrices {
		k := i + 1
		text := "免费"
		rPrice := int64(100)
		if price > 0 {
			text = fmt.Sprintf("%d金币", price)
			rPrice = price
		}
		visible := k == 1 || (p.TicketsBought >= k-1 && (p.TicketsBought >= k || p.RCLeft == 0))
		enters = append(enters, map[string]any{
			"id": k, "text": text, "price": price, "r_price": rPrice, "visible": visible,
			"url":       fmt.Sprintf("/rob_battles/buy_rob_ticket?ticket_id=%d", k),
			"can_enter": p.TicketsBought == k && p.RCLeft > 0,
			"can_buy":   k == p.TicketsBought+1 && p.RCLeft == 0,
			"dia_price": 0,
		})
	}
	return map[string]any{"enters": enters, "rob_score": fmt.Sprintf("当前积分:%d", p.Score)}
}

func robReadyMessage(oppID, name, revengeID string) map[string]any {
	path := "/rob_battles/battle_ready?user_id=" + url.QueryEscape(oppID)
	if revengeID != "" {
		path += "&revenge_id=" + url.QueryEscape(revengeID)
	}
	return map[string]any{
		"requireData": map[string]any{"rob_battle_ready": map[string]any{"path": path, "force_update": true}},
		"partials": map[string]any{"DifficultyMainBar": map[string]any{"option": map[string]any{
			"MainBar": map[string]any{"widgets": map[string]any{"label": map[string]any{"text": name}}}}}},
	}
}

func guildOrNone(g string) string {
	if g == "" {
		return "无"
	}
	return g
}

func robRecordRows(v *RobView) []any {
	rows := []any{}
	for _, r := range v.Records {
		tip := "防守成功"
		if r.AttackerWin {
			tip = "防守失败"
		}
		rows = append(rows, map[string]any{
			"icon": unitSquare(r.AttackerLead), "name": r.AttackerName, "tip": tip,
			"info":        fmt.Sprintf("%d\n%s", r.AttackerLvl, guildOrNone(r.AttackerGuild)),
			"btn_visible": r.AttackerWin && !r.Revenged,
			"message":     robReadyMessage(r.AttackerID, r.AttackerName, r.RecordID),
		})
	}
	return rows
}

func robInfoHandler(dep Deps, buy bool) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "rob_battles_info", "buy": buy}
		if buy {
			id, _ := strconv.Atoi(req.Query.Get("ticket_id"))
			err := dep.PvP.RobBuyTicket(req.UserID, id)
			if m := pvpErrModal(err); m != nil {
				return cn35wire.MarshalJSON(m)
			} else if err != nil {
				return nil, err
			}
		}
		v, err := dep.PvP.RobView(req.UserID)
		if err != nil {
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{
			"rob_battle_enter": robEnterDoc(v.Profile), "rob_battle_record": robRecordRows(v), "user": snap.User,
		}})
	})
}

func robDeckDoc(partyType string, d [2][5]int) map[string]any {
	parties := map[string]any{}
	for p := 0; p < 2; p++ {
		ids := make([]any, 5)
		for i, u := range d[p] {
			ids[i] = ""
			if u > 0 {
				ids[i] = strconv.Itoa(u)
			}
		}
		parties[strconv.Itoa(p+1)] = ids
	}
	return map[string]any{"parties": parties, "party_type": partyType}
}

func robDeckHandler(dep Deps, op string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		q := req.Query
		pt := q.Get("party_type")
		if pt != "def" {
			pt = "atk"
		}
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "user_rob_decks", "op": op, "party_type": pt}
		if op != "" {
			pid, _ := strconv.Atoi(q.Get("party_id"))
			slot, _ := strconv.Atoi(q.Get("party_index"))
			unitID, _ := strconv.Atoi(q.Get("unit_id"))
			var weaponOf func(int) int
			if dep.Catalog != nil {
				// RobSetDeck 的回调在 store tx 内同步执行，绝不能触达
				// dep.Catalog（rosterCatalog 冷快照会回源 Store 查询，与
				// 持锁 tx 争同一 DB 连接 → 死锁）。先在 tx 外把涉及单位
				// 的 weapon 全部解出，闭包只读 map。
				pre, err := dep.PvP.RobView(req.UserID)
				if err != nil {
					return nil, err
				}
				weapons := map[int]int{}
				resolve := func(id int) {
					if id == 0 {
						return
					}
					if _, ok := weapons[id]; ok {
						return
					}
					w := 0
					if u, err := dep.Catalog.Unit(id); err == nil && u != nil {
						w = u.Weapon
					}
					weapons[id] = w
				}
				resolve(unitID)
				for _, decks := range [2][2][5]int{pre.Atk, pre.Def} {
					for _, row := range decks {
						for _, u := range row {
							resolve(u)
						}
					}
				}
				weaponOf = func(id int) int { return weapons[id] }
			}
			err := dep.PvP.RobSetDeck(req.UserID, pt, pid, slot, unitID, op == "on", weaponOf)
			if m := pvpErrModal(err); m != nil {
				return cn35wire.MarshalJSON(m)
			} else if err != nil {
				return nil, err
			}
		}
		v, err := dep.PvP.RobView(req.UserID)
		if err != nil {
			return nil, err
		}
		d := v.Atk
		if pt == "def" {
			d = v.Def
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{
			"user_rob_" + pt + "_decks": robDeckDoc(pt, d)}})
	})
}

func robResolveOpponent(dep Deps, userID, oppID string, myScore, myLevel int) (*PvPOpponent, error) {
	if kind, period, idx, ok := ParseNPCOpponentID(oppID); ok {
		day := RobNPCPeriod(pvpNow(dep))
		if kind != "rob" || period != day || idx >= RobRules.CandidateMax {
			return nil, ErrPvPOpponent
		}
		o := RobNPC(dep.Catalog, pvpUnitPool(dep), day, idx, myScore, myLevel)
		if len(o.DefUnits) == 0 {
			return nil, ErrPvPOpponent
		}
		return o, nil
	}
	if oppID == "" {
		return nil, ErrPvPOpponent
	}
	return dep.PvP.RobPlayer(userID, oppID)
}

func robCandidatesHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "rob_battles_get_candidates"}
		v, err := dep.PvP.RobView(req.UserID)
		if err != nil {
			return nil, err
		}
		_, lvl, err := pvpSnapLevel(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		opps, err := dep.PvP.RobPlayers(req.UserID, RobRules.CandidateMax)
		if err != nil {
			return nil, err
		}
		day := RobNPCPeriod(pvpNow(dep))
		for i := 0; len(opps) < RobRules.CandidateMax && i < RobRules.CandidateMax; i++ {
			if o := RobNPC(dep.Catalog, pvpUnitPool(dep), day, i, v.Profile.Score, lvl); len(o.DefUnits) > 0 {
				opps = append(opps, *o)
			}
		}
		sort.SliceStable(opps, func(a, b int) bool { return opps[a].Score > opps[b].Score })
		rows := []any{}
		for _, o := range opps {
			rows = append(rows, map[string]any{
				"icon": unitSquare(o.LeaderUnitID), "name": o.Name, "tip": "", "btn_visible": true,
				"info":    fmt.Sprintf("%d\n%s", o.Level, guildOrNone(o.GuildName)),
				"message": robReadyMessage(o.UserID, o.Name, ""),
			})
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{"rob_candidates": rows}})
	})
}

// robEnemyDocs 敌方同伴文档：真实账号按其符文/星盘/设施同源计算；NPC 用模板面板。
// uuid 加 enemy- 前缀、清空 monster_uuid，避免与我方同伴/魔宠缓存撞键。
func robEnemyDocs(dep Deps, opp *PvPOpponent) ([]any, error) {
	var docs []any
	if opp.NPC {
		for _, r := range opp.DefUnits {
			d, err := npcUnitDoc(dep, r)
			if err != nil {
				return nil, err
			}
			docs = append(docs, d)
		}
	} else {
		var err error
		if docs, err = pvpUnitDocs(dep, opp.UserID, opp.DefUnits); err != nil {
			return nil, err
		}
	}
	for i, d := range docs {
		if m, ok := d.(map[string]any); ok {
			m["uuid"] = fmt.Sprintf("enemy-%d-%v", i, m["id"])
			m["monster_uuid"] = ""
			delete(m, "monster_id")
		}
	}
	return docs, nil
}

func robReadyHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		q := req.Query
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "rob_battles_battle_ready", "opponent": q.Get("user_id")}
		v, err := dep.PvP.RobView(req.UserID)
		if err != nil {
			return nil, err
		}
		_, lvl, err := pvpSnapLevel(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		opp, err := robResolveOpponent(dep, req.UserID, q.Get("user_id"), v.Profile.Score, lvl)
		if m := pvpErrModal(err); m != nil {
			return cn35wire.MarshalJSON(m)
		} else if err != nil {
			return nil, err
		}
		enemy, err := robEnemyDocs(dep, opp)
		if err != nil {
			return nil, err
		}
		addition := "&user_id=" + url.QueryEscape(opp.UserID)
		if rid := q.Get("revenge_id"); rid != "" {
			addition += "&revenge_id=" + url.QueryEscape(rid)
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{
			"rob_battle_condition": ladderConditionDoc(),
			"rob_battle_ready": map[string]any{
				"enemy":      enemy,
				"reward":     fmt.Sprintf("胜利可获得%d积分", RobScoreGain(v.Profile.Score, opp.Score)),
				"addition":   addition,
				"score_mode": true,
			},
		}})
	})
}

func robExecuteHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		q := req.Query
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "rob_battles_execute", "opponent": q.Get("user_id")}
		v, err := dep.PvP.RobView(req.UserID)
		if err != nil {
			return nil, err
		}
		snap, lvl, err := pvpSnapLevel(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		opp, err := robResolveOpponent(dep, req.UserID, q.Get("user_id"), v.Profile.Score, lvl)
		if m := pvpErrModal(err); m != nil {
			return cn35wire.MarshalJSON(m)
		} else if err != nil {
			return nil, err
		}
		sess, units, seeds, err := dep.PvP.RobOpen(req.UserID, opp, q.Get("revenge_id"))
		if m := pvpErrModal(err); m != nil {
			return cn35wire.MarshalJSON(m)
		} else if err != nil {
			return nil, err
		}
		cat, err := loadQuestCatalog(dep.AssetsDir)
		if err != nil {
			return nil, err
		}
		unitDocs, err := pvpUnitDocs(dep, req.UserID, units)
		if err != nil {
			return nil, err
		}
		enemy, err := robEnemyDocs(dep, opp)
		if err != nil {
			return nil, err
		}
		seedIDs := make([]int, 0, len(seeds))
		for _, m := range seeds {
			seedIDs = append(seedIDs, m.MonsterID)
		}
		monsters, err := gwMonsterDocs(seedIDs, dep)
		if err != nil {
			return nil, err
		}
		assets := pvpAssetNames(cat.Defaults.AssetNamesCommon, units, append(append([]any{}, unitDocs...), enemy...),
			nil, seeds, opp.DefUnits, "common/player_flag", "common/enemy_flag")
		quest := map[string]any{
			"id": opp.UserID, "name": "vs " + opp.Name, "mode": "rob_battle",
			"difficulty_id": "normal", "difficulty_name": "训练", "use_ap": 0,
			"background_id": 1, "background": cat.Defaults.Background, "powerup_conf": cat.Defaults.PowerupConf,
			"initial_power": LadderRules.InitialPower, "unit_count_limit": 10, "continuable": false,
			"wave_conf": map[string]any{"1": map[string]any{
				"length": RobRules.WaveLength, "time_limit": RobRules.TimeLimitSec}},
			"seed_atack_rate": gwSeedAtackRate, "unit_help_rate": 1.0,
		}
		for _, w := range gwWeaponRateNames {
			quest[w+"_rate"] = 1
		}
		bid := "?bid=" + url.QueryEscape(sess.BattleID)
		c.Decoded["battle_id"] = sess.BattleID
		return cn35wire.MarshalJSON(map[string]any{
			"user": snap.User, "quest": quest, "units": unitDocs, "enemy": enemy,
			"monsters": monsters, "main_character": cat.Defaults.MainCharacter,
			"appear_table": map[string]any{"1": map[string]any{}}, "boss_appear_table": map[string]any{"1": []any{}},
			"help_table": map[string]any{}, "timezone_table": map[string]any{"1": 1},
			"asset_names": assets, "bgm_name": gwBattleBGM,
			"flag_image_name": "common/player_flag", "enemy_flag_image_name": "common/enemy_flag",
			"ap_use_url": "/rob_battles/rc_used" + bid, "result_url": "/rob_battles/attack" + bid,
			"lose_url": "/rob_battles/lose" + bid, "next_page_message": robMainPage,
			"exp": 0, "clear_count": 0, "battle_log": false, "rush_started": nil, "rush_ended": nil,
			"seed_attribute_rates": gwSeedAttributeRatesDoc(),
			"data":                 map[string]any{"user": snap.User},
		})
	})
}

func robPayHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "rob_battles_rc_used", "bid": bid}
		_, err := dep.PvP.RobPay(req.UserID, bid)
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
		return cn35wire.MarshalJSON(map[string]any{"status": "success", "data": map[string]any{"user": snap.User}})
	})
}

func robSettleHandler(dep Deps, win bool) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		bid := req.Query.Get("bid")
		c.Decoded = map[string]any{"cn35": true, "uid": req.UserID, "endpoint": "rob_battles_settle", "bid": bid, "win": win}
		res, err := dep.PvP.RobSettle(req.UserID, bid, win)
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
		doc := map[string]any{"status": status, "next_page_message": robMainPage,
			"data": map[string]any{"user": snap.User}}
		if res != nil && res.Win && res.Gain > 0 {
			doc["rob_battle_score"] = res.Gain
			doc["rob_battle_score_text"] = fmt.Sprintf("训练积分 +%d（当前 %d）", res.Gain, res.Score)
		}
		c.Decoded["status"] = status
		return cn35wire.MarshalJSON(doc)
	})
}
