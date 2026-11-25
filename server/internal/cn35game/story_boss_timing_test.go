package cn35game_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestStoryMultiWaveBossFollowsLastGroup 多波 Boss 关的 boss_appear 须紧跟末波
// 最后一只普通怪：客户端 Unit.OnWalk 触旗即 Quest.Clear()，不等待 appear_table
// 剩余项，留长空窗时清完末组即走到终点、Boss 未出场。
func TestStoryMultiWaveBossFollowsLastGroup(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "assets", "cn350", "serve_quest_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Quests []struct {
			ID        int    `json:"id"`
			BossLabel string `json:"boss_label"`
			WaveCount int    `json:"wave_count"`
			Battle    struct {
				AppearTable map[string]map[string]map[string]any `json:"appear_table"`
			} `json:"battle"`
		} `json:"quests"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, q := range cat.Quests {
		if q.BossLabel == "" || q.WaveCount <= 1 {
			continue
		}
		last := q.Battle.AppearTable[strconv.Itoa(q.WaveCount)]
		marker, regular := -1, -1
		for k, e := range last {
			ms, _ := strconv.Atoi(k)
			if _, ok := e["id"]; !ok {
				if marker < 0 || ms < marker {
					marker = ms
				}
			} else if g, _ := e["gauge"].(bool); !g && ms > regular {
				regular = ms
			}
		}
		if marker < 0 || regular < 0 || marker-regular > 500 {
			t.Errorf("quest %d 末波 boss_appear=%d 末只普通怪=%d：间隔须 ≤500ms", q.ID, marker, regular)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("未找到多波 Boss 关")
	}
}
