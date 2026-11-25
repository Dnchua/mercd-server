// battle_timezone.go 探险战斗时段（朝/夕/夜）与 Fever Time。
//
// 客户端消费（CN3.5 Quest.cs / SWFQuestCurtain.cs）：
//   - timezone_table.<wave> 决定该波背景 background<id>_<tz> 与幕布时钟动画；
//     时段从低到高切换到 3 时播 feverStart.swf（hasFeverMode = mode=="quest"）。
//   - rush_started=true 且当前时段 >=3 时挂 fever 粒子。
//   - 时段本身不改战斗数值；收益加成由服务端写进 appear_table 掉落。
//
// 原作观测（mlk 原服 execute 录播，每怪金币/资材均值）：
//
//	朝 tz=1 金币 36.8 / 资材 12.1（q1359、q1361）
//	夕 tz=2 金币 43.9 / 资材 14.0（q1360 第 2、3 波）→ ≈ ×1.19
//	夜 tz=3 金币 72.5 / 资材 23.0（q1357 rush_mode=mode_70）→ ≈ ×1.97
//	q80 极难：夕 3805 → 夜 6343（×1.667 = 2.0/1.2）
//
// 经验与 SP（power）不随时段变化。原服的时段推进与 rush_mode/rush_count
// 计数规则未还原；本实现按配置概率逐场随机（重建值）。
package cn35game

import (
	"math"
	mathrand "math/rand"
	"sort"
	"strconv"
)

// 时段取值（与 background<id>_<tz> 资源后缀一致）。
const (
	TimezoneDay   = 1
	TimezoneDusk  = 2
	TimezoneNight = 3
)

// timezoneRewardRates 金币/资材倍率（原作观测取整：朝 1.0、夕 1.2、夜 2.0）。
var timezoneRewardRates = map[int]float64{
	TimezoneDay:   1.0,
	TimezoneDusk:  1.2,
	TimezoneNight: 2.0,
}

// timezoneScaledKeys 随时段加成的奖励键。
var timezoneScaledKeys = map[string]bool{"gacha_point": true, "guild_asset": true}

// BattleTimezoneConfig 探险战斗时段随机参数（重建值）。零值 = 关闭（全程白天）。
type BattleTimezoneConfig struct {
	// DuskRate 开局为黄昏的概率。
	DuskRate float64
	// NightRate 开局即为夜晚（Fever Time）的概率。
	NightRate float64
	// AdvanceRate 每进入下一波时段推进一档（朝→夕→夜）的概率。
	AdvanceRate float64
}

// Enabled 是否启用时段随机。
func (c BattleTimezoneConfig) Enabled() bool {
	return c.DuskRate > 0 || c.NightRate > 0 || c.AdvanceRate > 0
}

// NormalizeBattleTimezoneConfig 各概率夹到 [0,1]，开局概率之和超过 1 时按比例缩放。
func NormalizeBattleTimezoneConfig(c BattleTimezoneConfig) BattleTimezoneConfig {
	clamp := func(v float64) float64 { return math.Max(0, math.Min(1, v)) }
	c.DuskRate, c.NightRate, c.AdvanceRate = clamp(c.DuskRate), clamp(c.NightRate), clamp(c.AdvanceRate)
	if s := c.DuskRate + c.NightRate; s > 1 {
		c.DuskRate, c.NightRate = c.DuskRate/s, c.NightRate/s
	}
	return c
}

// sortedWaveKeys appear_table 的数字波次键（升序）。
func sortedWaveKeys(appearTable map[string]any) []string {
	var waves []int
	for w := range appearTable {
		if n, err := strconv.Atoi(w); err == nil {
			waves = append(waves, n)
		}
	}
	sort.Ints(waves)
	out := make([]string, len(waves))
	for i, w := range waves {
		out[i] = strconv.Itoa(w)
	}
	return out
}

// rollBattleTimezones 逐波摇时段：开局按 Dusk/Night 概率定档，之后每波以
// AdvanceRate 推进一档（不回退，夜为上限）。未启用时返回 nil 且不消耗 rnd。
func rollBattleTimezones(cfg BattleTimezoneConfig, appearTable map[string]any, rnd *mathrand.Rand) map[string]int {
	if !cfg.Enabled() {
		return nil
	}
	tz := TimezoneDay
	switch r := rnd.Float64(); {
	case r < cfg.NightRate:
		tz = TimezoneNight
	case r < cfg.NightRate+cfg.DuskRate:
		tz = TimezoneDusk
	}
	out := map[string]int{}
	for i, w := range sortedWaveKeys(appearTable) {
		if i > 0 && tz < TimezoneNight && rnd.Float64() < cfg.AdvanceRate {
			tz++
		}
		out[w] = tz
	}
	return out
}

// timezoneOf 取波次时段（缺省白天）。
func timezoneOf(tzTable map[string]int, wave string) int {
	if tz, ok := tzTable[wave]; ok && tz >= TimezoneDay && tz <= TimezoneNight {
		return tz
	}
	return TimezoneDay
}

// hasNightWave 是否有夜晚波（Fever Time）。
func hasNightWave(tzTable map[string]int) bool {
	for _, tz := range tzTable {
		if tz >= TimezoneNight {
			return true
		}
	}
	return false
}

// scaleTimezoneCount 按时段倍率缩放单条奖励数量（四舍五入）。
func scaleTimezoneCount(count int, tz int) int {
	return int(math.Round(float64(count) * timezoneRewardRates[tz]))
}

// appearTableWithTimezoneRewards 返回各波怪位金币/资材按本波时段倍率缩放
// 后的副本；只拷贝改动节点，目录对象不被改写。
func appearTableWithTimezoneRewards(appearTable map[string]any, tzTable map[string]int) map[string]any {
	out := make(map[string]any, len(appearTable))
	for wkey, wv := range appearTable {
		out[wkey] = wv
		tz := timezoneOf(tzTable, wkey)
		wm, ok := wv.(map[string]any)
		if !ok || tz == TimezoneDay {
			continue
		}
		nw := make(map[string]any, len(wm))
		for mkey, ev := range wm {
			nw[mkey] = ev
			em, ok := ev.(map[string]any)
			if !ok {
				continue
			}
			rawRewards, _ := em["rewards"].([]any)
			if len(rawRewards) == 0 {
				continue
			}
			rewards := make([]any, len(rawRewards))
			for i, rv := range rawRewards {
				rewards[i] = rv
				rm, ok := rv.(map[string]any)
				key, _ := rm["key"].(string)
				if !ok || !timezoneScaledKeys[key] {
					continue
				}
				cp := make(map[string]any, len(rm))
				for k, v := range rm {
					cp[k] = v
				}
				cp["count"] = scaleTimezoneCount(int(numOf(rm["count"])), tz)
				rewards[i] = cp
			}
			cp := make(map[string]any, len(em))
			for k, v := range em {
				cp[k] = v
			}
			cp["rewards"] = rewards
			nw[mkey] = cp
		}
		out[wkey] = nw
	}
	return out
}

// timezoneScaledRewards 返回金币/资材按时段倍率缩放后的奖励副本。
func timezoneScaledRewards(rs []BattleReward, tz int) []BattleReward {
	out := append([]BattleReward(nil), rs...)
	if tz == TimezoneDay {
		return out
	}
	for i := range out {
		if timezoneScaledKeys[out[i].Key] {
			out[i].Count = scaleTimezoneCount(out[i].Count, tz)
		}
	}
	return out
}
