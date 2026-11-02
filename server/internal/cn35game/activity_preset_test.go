// activity_preset_test.go ACT-BANNER-01 外部契约测试：
// 活动位随登录快照下发，且每个 id 都能落到 app_conf 的 banner 定义与目标页。
//
// 客户端 act_pages 只有一个消费点（DataCache.cs:265-267 → common.activityList），
// 真实 app_conf 也没有页面 requireData.act_pages，故必须由 preset_data 携带，
// 否则首页轮播/活动列表整块空白（UI-HOME-FIX 实机取证）。本测试走真实加密通道，
// 断言注入结果形状与「键存在、目标页存在」两条硬约束（白名单内容由内部测试
// TestActivityWhitelistMatchesAppConf 锁死）。
package cn35game_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresetDataInjectsActivityPages(t *testing.T) {
	assetsDir := filepath.Join("..", "..", "assets", "cn350")
	raw, err := os.ReadFile(filepath.Join(assetsDir, "serve_app_conf_android.json"))
	if err != nil {
		t.Skipf("跳过：读不到 app_conf（%v）", err)
	}
	var conf struct {
		Pages    map[string]json.RawMessage `json:"pages"`
		Partials struct {
			CarouselBannerPartial struct {
				CarouselBanner struct {
					Banners map[string]struct {
						OnClick struct {
							Message string `json:"message"`
						} `json:"OnClick"`
					} `json:"banners"`
				} `json:"CarouselBanner"`
			} `json:"CarouselBannerPartial"`
		} `json:"partials"`
	}
	if err := json.Unmarshal(raw, &conf); err != nil {
		t.Fatalf("解析 app_conf: %v", err)
	}
	banners := conf.Partials.CarouselBannerPartial.CarouselBanner.Banners
	if len(banners) == 0 {
		t.Fatal("app_conf 未定义 banners")
	}

	ts, _ := testServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "act-pages-preset")

	preset := gameJSON(t, ts.URL, "/users/preset_data.json", uid, sk, s0, s1, "", "_method=GET")
	data, ok := preset["data"].(map[string]any)
	if !ok {
		t.Fatalf("preset_data 缺 data: %v", preset)
	}
	list, ok := data["act_pages"].([]any)
	if !ok {
		t.Fatalf("preset_data.data.act_pages 缺失或非数组: %v", data["act_pages"])
	}
	if len(list) == 0 {
		t.Fatal("act_pages 为空：common.activityList 空会让轮播/活动列表整块空白")
	}
	seen := map[string]bool{}
	for i, item := range list {
		id, _ := item.(string)
		if id == "" {
			t.Fatalf("act_pages[%d] 非字符串或为空: %v", i, item)
		}
		if seen[id] {
			t.Fatalf("act_pages 含重复 id %q（轮播会出现重复条目）", id)
		}
		seen[id] = true
		b, ok := banners[id]
		if !ok {
			t.Fatalf("act_pages 含 %q，但 app_conf banners 无此键（轮播无图）", id)
		}
		name := ""
		for _, part := range strings.Split(b.OnClick.Message, "&") {
			if strings.HasPrefix(part, "name=") {
				name = strings.TrimPrefix(part, "name=")
			}
		}
		if name == "" {
			t.Fatalf("banners[%q].OnClick.message 无 name=（无法跳转）", id)
		}
		if _, ok := conf.Pages[name]; !ok {
			t.Fatalf("banners[%q] 目标页 %q 不在 app_conf pages 中（会打开空白壳页）", id, name)
		}
	}
	if _, ok := data["act_has_new"].(bool); !ok {
		t.Fatalf("act_has_new 缺失或非布尔: %v", data["act_has_new"])
	}
}
