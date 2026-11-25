// story_encounter.go 2026-10-01 第一篇剧情关战斗下发：
// execute 时的出怪 params 合成、按难度数值缩放、剧情魔宠概率掉落
// 与战斗背景随机化。
//
// 设计真源：notes/proposed/2026-10-01-cn35-story1-encounters.md；
// 数值来自数值组 story-01 草案（PROPOSED），经
// tools/story_encounters_build.py 编译进 serve_quest_catalog.json。
//
// 紧凑出怪项 {id,scale,powerup_rate,gauge,hp,at,rewards} 的 params 在
// 本文件由 cardcatalog+纪念版合成（与 resourceMonsterParams 同一管线），
// hp/at 为方案绝对值（已含 powerup_rate 折算口径），再按难度 hp_bp/at_bp
// 千分比缩放。内联 params 的旧式条目原样保留行为字段、只缩放面板键。
package cn35game

import (
	"math"
	mathrand "math/rand"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

// battleRand 战斗摇奖源：优先 Deps.BattleRand（测试注入；*Rand 非
// 并发安全，仅装配于回放测试），缺省每次开战新建加密种子源。
func battleRand(dep Deps) *mathrand.Rand {
	if dep.BattleRand != nil {
		return dep.BattleRand
	}
	return newDropRand()
}

// battleProfileFor 取指定难度 profile；缺键/缺表回退 normal，
// normal 也缺时给恒等倍率 + battle.exp。
func battleProfileFor(b *catalogBattle, diff string) battleDifficultyProfile {
	if b.Difficulty != nil {
		if p, ok := b.Difficulty[diff]; ok {
			return p
		}
		if p, ok := b.Difficulty["normal"]; ok {
			return p
		}
	}
	return battleDifficultyProfile{HpBP: 1000, AtBP: 1000, Exp: b.Exp}
}

// storyMonsterParams 由魔宠模板合成出怪 params（BattleMonsterDoc 全键 +
// rates 拍平成 fire_rate 等官方 execute 形状），随后把面板键覆盖为
// 方案绝对值 hp/at（seed_at/at 同值）。
func storyMonsterParams(m *cardcatalog.Monster, hp, at int) map[string]any {
	p := BattleMonsterDoc(m)
	if rates, ok := p["rates"].(map[string]any); ok {
		p["fire_rate"] = rates["fire"]
		p["water_rate"] = rates["aqua"]
		p["wind_rate"] = rates["wind"]
		p["light_rate"] = rates["light"]
		p["dark_rate"] = rates["dark"]
		delete(p, "rates")
	}
	if hp > 0 {
		p["hp"], p["life"] = hp, hp
	}
	if at > 0 {
		p["at"], p["atk"], p["seed_at"] = at, at, at
	}
	return p
}

// scaleParamKeys 对 params 内指定键按千分比缩放（存在且为正才动）。
func scaleParamKeys(params map[string]any, keys []string, bp int) {
	if bp == 1000 {
		return
	}
	for _, k := range keys {
		if v, ok := params[k]; ok {
			if n := numOf(v); n > 0 {
				params[k] = int(math.Round(n * float64(bp) / 1000.0))
			}
		}
	}
}

// materializeAppearTable 生成本次 execute 下发的 appear_table：
// 紧凑项合成 params；统一按难度 hp_bp/at_bp 缩放面板键。
// 返回全新结构，不改写目录对象。
func materializeAppearTable(b *catalogBattle, prof battleDifficultyProfile,
	cards UnitCatalog) (map[string]any, error) {
	out := make(map[string]any, len(b.AppearTable))
	for wkey, wv := range b.AppearTable {
		wm, ok := wv.(map[string]any)
		if !ok {
			continue
		}
		nw := make(map[string]any, len(wm))
		for mkey, ev := range wm {
			em, ok := ev.(map[string]any)
			if !ok {
				nw[mkey] = ev
				continue
			}
			cp := make(map[string]any, len(em)+1)
			for k, v := range em {
				cp[k] = v
			}
			if _, hasID := em["id"]; !hasID {
				// {"type":"boss_appear"} 标记项（story-v2 主线 Boss 关形状）：
				// 不是魔宠出怪，原样透传，不合成 params、不查 cardcatalog。
				nw[mkey] = cp
				continue
			}
			mid := int(numOf(em["id"]))
			params, hasParams := em["params"].(map[string]any)
			if !hasParams {
				m, err := cards.Monster(mid)
				if err != nil {
					return nil, err
				}
				params = storyMonsterParams(m,
					int(numOf(em["hp"])), int(numOf(em["at"])))
			} else {
				pc := make(map[string]any, len(params))
				for k, v := range params {
					pc[k] = v
				}
				params = pc
			}
			scaleParamKeys(params, []string{"hp", "life"}, prof.HpBP)
			scaleParamKeys(params, []string{"at", "atk", "seed_at"}, prof.AtBP)
			cp["params"] = params
			nw[mkey] = cp
		}
		out[wkey] = nw
	}
	return out, nil
}

// fixedBattleRewards 难度 profile 的固定入账奖励（金币/资材；0 跳过）。
func fixedBattleRewards(prof battleDifficultyProfile) []BattleReward {
	out := []BattleReward{}
	if prof.GachaPoint > 0 {
		out = append(out, BattleReward{Key: "gacha_point", Count: prof.GachaPoint})
	}
	if prof.GuildAsset > 0 {
		out = append(out, BattleReward{Key: "guild_asset", Count: prof.GuildAsset})
	}
	return out
}

// rollStorySeedDrop 剧情魔宠一次伯努利判定；nil 配置/未命中返回 nil。
func rollStorySeedDrop(sd *battleSeedDrop, diff string, rnd *mathrand.Rand) *BattleReward {
	if sd == nil || sd.MonsterID <= 0 {
		return nil
	}
	bp := sd.ChanceBP[diff]
	if bp <= 0 {
		return nil
	}
	if bp < 10000 && rnd.Intn(10000) >= bp {
		return nil
	}
	return &BattleReward{Key: "seed", ID: sd.MonsterID, Count: 1}
}

// appearTableWithSeedReward 把 seed 奖励注入该魔宠实际出现的末波怪位
// （客户端按 spawnEntry.rewards 展示掉落来源）；该 id 不在表中时退化为
// appearTableWithRewards 的末波轮转。不改写入参。
func appearTableWithSeedReward(table map[string]any, r BattleReward) map[string]any {
	// 找最后一个 entry.id==r.ID 的 (wave,ms)。
	maxWave, pickMS := -1, -1
	var waveMap map[string]any
	for wkey, wv := range table {
		n, err := strconv.Atoi(wkey)
		if err != nil {
			continue
		}
		wm, ok := wv.(map[string]any)
		if !ok {
			continue
		}
		for mkey, ev := range wm {
			em, ok := ev.(map[string]any)
			if !ok || int(numOf(em["id"])) != r.ID {
				continue
			}
			ms, err := strconv.Atoi(mkey)
			if err != nil {
				continue
			}
			if n > maxWave || (n == maxWave && ms > pickMS) {
				maxWave, pickMS, waveMap = n, ms, wm
			}
		}
	}
	if waveMap == nil {
		return appearTableWithRewards(table, []BattleReward{r})
	}
	out := map[string]any{}
	for k, v := range table {
		out[k] = v
	}
	nw := map[string]any{}
	for mkey, ev := range waveMap {
		em, ok := ev.(map[string]any)
		ms, _ := strconv.Atoi(mkey)
		if !ok || ms != pickMS {
			nw[mkey] = ev
			continue
		}
		cp := map[string]any{}
		for k, v := range em {
			cp[k] = v
		}
		existing, _ := cp["rewards"].([]any)
		cp["rewards"] = append([]any{map[string]any{
			"key": r.Key, "id": r.ID, "count": r.Count}}, existing...)
		nw[mkey] = cp
	}
	out[strconv.Itoa(maxWave)] = nw
	return out
}

// pickBattleBackground 每次开战从 background_pool 摇一个套件 id；
// 空池退回 quest.BackgroundID，仍为空退 1（background1_1 恒存在）。
// 2026-11-23：story-v2 主线已改回原服固定背景（background_id 逐关下发，
// 不再写 background_pool），随机池只剩资源本等仍配置的关卡使用。
func pickBattleBackground(q *catalogQuest, rnd *mathrand.Rand) int {
	if len(q.BackgroundPool) > 0 {
		return q.BackgroundPool[rnd.Intn(len(q.BackgroundPool))]
	}
	if q.BackgroundID > 0 {
		return q.BackgroundID
	}
	return 1
}
