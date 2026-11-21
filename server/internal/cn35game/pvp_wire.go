// pvp_wire.go M10 公共：路由注册、日常入口、单位文档、错误弹窗、素材齐全同伴池。
package cn35game

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func pvpNow(dep Deps) time.Time {
	if dep.GuildClock != nil {
		return dep.GuildClock()
	}
	return time.Now()
}

func pvpModal(title, body string) map[string]any { return questModal(title, body) }

func pvpErrModal(err error) map[string]any {
	switch {
	case errors.Is(err, ErrPvPLevel):
		return pvpModal("无法参加", fmt.Sprintf("达到%d级以上的癒术士才可以参加癒术士大赛哟", LadderRules.MinLevel))
	case errors.Is(err, ErrPvPNoCount):
		return pvpModal("无法挑战", "今日挑战次数已用完，请明天再来")
	case errors.Is(err, ErrPvPNoDefense):
		return pvpModal("提示", "请先完成防御设定")
	case errors.Is(err, ErrPvPAttackDeck):
		return pvpModal("无法挑战", "请先编满两支攻击队伍")
	case errors.Is(err, ErrPvPOpponent), errors.Is(err, ErrPvPSelf):
		return pvpModal("无法挑战", "对手信息已失效，请刷新列表")
	case errors.Is(err, ErrPvPDifficulty):
		return pvpModal("无法挑战", "难度无效")
	case errors.Is(err, ErrPvPMonster):
		return pvpModal("无法设置", "魔宠不存在")
	case errors.Is(err, ErrPvPUnit):
		return pvpModal("无法编队", "同伴不存在")
	case errors.Is(err, ErrPvPCareerLimit):
		return pvpModal("无法编队", "该职业的同伴编入人数已达上限")
	case errors.Is(err, ErrPvPGrowFull):
		return pvpModal("无法增幅", "该增幅已达上限")
	case errors.Is(err, ErrPvPPotion):
		return pvpModal("无法增幅", "药水不足")
	case errors.Is(err, ErrPvPTicket):
		return pvpModal("无法购买", "当前不能购买该入场券")
	case errors.Is(err, ErrPvPGold):
		return pvpModal("无法购买", "金币不足")
	case errors.Is(err, ErrPvPNoReward):
		return pvpModal("提示", "没有可领取的排行奖励")
	case errors.Is(err, ErrPvPInvalidArg):
		return pvpModal("无法操作", "请求参数无效")
	case errors.Is(err, ErrDeckEmpty), errors.Is(err, ErrDeckNotFound):
		return pvpModal("无法战斗", "编队为空，无法开始战斗")
	default:
		return nil
	}
}

// pvpUnitDocs 我方/敌方参战同伴文档：符文、星盘、公会设施加成与剧情战斗同源。
func pvpUnitDocs(dep Deps, userID string, units []UnitRecord) ([]any, error) {
	fac := GuildFacilityLevelsForUser(dep, userID)
	skillsByUnit, err := skillsByUnitFor(dep, userID)
	if err != nil {
		return nil, err
	}
	fate := fateEntriesFor(dep, userID)
	out := make([]any, 0, len(units))
	for _, r := range units {
		cu, err := dep.Catalog.Unit(r.UnitID)
		if err != nil {
			return nil, err
		}
		doc, err := buildUnitDoc(r, cu)
		if err != nil {
			return nil, err
		}
		baseHP, _ := doc["hp"].(int)
		baseAT, _ := doc["at"].(int)
		applyUnitSkills(doc, cu.Rare, r.DuplicatedCount, skillsByUnit[r.UnitID])
		ApplyUnitFateBuffs(doc, cu.Element, fate)
		ApplyUnitFacilityBuff(doc, baseHP, baseAT, fac.HP, fac.AT)
		out = append(out, doc)
	}
	return out, nil
}

// npcUnitDoc NPC 同伴文档：等级不合法时逐级回落（避免 statmodel 越界）。
func npcUnitDoc(dep Deps, r UnitRecord) (map[string]any, error) {
	cu, err := dep.Catalog.Unit(r.UnitID)
	if err != nil {
		return nil, err
	}
	for lvl := r.Level; ; lvl /= 2 {
		if lvl < 1 {
			lvl = 1
		}
		r.Level = lvl
		doc, derr := buildUnitDoc(r, cu)
		if derr == nil || lvl == 1 {
			return doc, derr
		}
	}
}

var (
	pvpPoolMu    sync.Mutex
	pvpPoolCache = map[string][]int{}
)

// pvpUnitPool 名册清单中素材齐全（qualified）且 catalog 可解析的 2–5★ 同伴，用作 NPC 阵容与头像。
// 清单缺失时回退到原服录播出现过的头像 id。
func pvpUnitPool(dep Deps) []int {
	pvpPoolMu.Lock()
	defer pvpPoolMu.Unlock()
	if p, ok := pvpPoolCache[dep.AssetsDir]; ok {
		return p
	}
	pool := []int{}
	if raw, err := os.ReadFile(filepath.Join(dep.AssetsDir, "unit_roster_manifest.json")); err == nil {
		if m, err := ParseRosterManifest(raw); err == nil {
			for k, u := range m.Units {
				id, err := strconv.Atoi(k)
				if err != nil || !u.Qualified || u.Rare < 2 || u.Rare > 5 {
					continue
				}
				if dep.Catalog != nil {
					if cu, err := dep.Catalog.Unit(id); err != nil || cu == nil {
						continue
					}
				}
				pool = append(pool, id)
			}
		}
	}
	if len(pool) == 0 {
		pool = []int{721, 150093, 513, 680, 150002, 1074, 1277, 933, 1152, 110015, 1265}
	}
	sort.Ints(pool)
	pvpPoolCache[dep.AssetsDir] = pool
	return pool
}

func unitSquare(id int) string {
	if id <= 0 {
		id = 721
	}
	return fmt.Sprintf("unit/unit_square_%d", id)
}

func pctOf(a, b int) int {
	if b <= 0 {
		return 0
	}
	return a * 100 / b
}

// pvpPortalEntries 主人公的日常右列追加项（原服 function_portal_list 第 8/9 项原样）。
func pvpPortalEntries() []map[string]any {
	return []map[string]any{
		{
			"id": 8, "name": "rob_battle", "texture_asset_name": "common/bnr_rob_battle",
			"show_page_message": "name=RobBattleMainPage&method=Stack",
		},
		{
			"id": 9, "name": "ladder", "texture_asset_name": "common/bnr_ladder",
			"show_page_message": "name=LadderMainPage&base=Common/Pages/LadderMainPage&method=Stack",
		},
	}
}

// pvpInjectConsts preset_data.consts：难度文案与职业限制（原服观测原值）。
func pvpInjectConsts(consts map[string]any) {
	consts["ladder_difficulty"] = LadderDifficultyConsts()
	consts["rob_battle_career_limit"] = RobCareerLimitConsts()
}

func registerPvPRoutes(register func(method, path, note string, handler httpapi.Handler), dep Deps) {
	both := func(path, note string, h httpapi.Handler) {
		register(http.MethodGet, path, note, h)
		register(http.MethodPost, path, note+" POST 形态", h)
	}
	both("/ladders/show_ladder_info", "M10 癒术士大赛主页", ladderInfoHandler(dep))
	both("/ladders/get_candidates", "M10 癒术士大赛候选对手", ladderCandidatesHandler(dep))
	both("/ladders/monsters", "M10 癒术士大赛防御魔宠", ladderDeckHandler(dep, ""))
	both("/ladders/set_deck", "M10 癒术士大赛设置防御魔宠", ladderDeckHandler(dep, "set"))
	both("/ladders/remove_from_deck", "M10 癒术士大赛移除防御魔宠", ladderDeckHandler(dep, "remove"))
	both("/ladders/ladder_quest_execute.json", "M10 癒术士大赛开战", ladderExecuteHandler(dep))
	both("/ladders/lc_used", "M10 癒术士大赛扣挑战次数", ladderPayHandler(dep))
	both("/ladders/attack", "M10 癒术士大赛胜利结算", ladderSettleHandler(dep, true))
	both("/ladders/lose", "M10 癒术士大赛败北上报（重建端点）", ladderSettleHandler(dep, false))
	both("/ladders/monster_grow_index", "M10 魔宠增幅读面", ladderGrowHandler(dep, ""))
	both("/ladders/monster_grow_upgrade", "M10 魔宠增幅升级", ladderGrowHandler(dep, "upgrade"))
	both("/ladders/monster_grow_upgrade_reset", "M10 魔宠增幅重置", ladderGrowHandler(dep, "reset"))
	both("/ladders/show_rank", "M10 癒术士大赛排行", ladderRankHandler(dep, false))
	both("/ladders/fetch_rewards", "M10 癒术士大赛领取排行奖励", ladderRankHandler(dep, true))

	both("/rob_battles/show_rob_battles_info", "M10 公会训练主页", robInfoHandler(dep, false))
	both("/rob_battles/buy_rob_ticket", "M10 公会训练购买入场券", robInfoHandler(dep, true))
	both("/user_rob_decks", "M10 公会训练编队读面", robDeckHandler(dep, ""))
	both("/user_rob_decks/on", "M10 公会训练编入同伴", robDeckHandler(dep, "on"))
	both("/user_rob_decks/off", "M10 公会训练移出同伴", robDeckHandler(dep, "off"))
	both("/rob_battles/get_candidates", "M10 公会训练候选对手", robCandidatesHandler(dep))
	both("/rob_battles/battle_ready", "M10 公会训练战前准备", robReadyHandler(dep))
	both("/rob_battles", "M10 公会训练战前准备（app_conf 缺省路径）", robReadyHandler(dep))
	both("/rob_battles/rob_battle_quest_execute.json", "M10 公会训练开战（AutoQuest）", robExecuteHandler(dep))
	both("/rob_battles/rc_used", "M10 公会训练扣次数（重建端点）", robPayHandler(dep))
	both("/rob_battles/attack", "M10 公会训练胜利结算", robSettleHandler(dep, true))
	both("/rob_battles/lose", "M10 公会训练败北上报（重建端点）", robSettleHandler(dep, false))
}
