package cn35game

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// activityConfFixture 只解析本包需要的 app_conf 片段。
type activityConfFixture struct {
	Pages map[string]struct {
		RequireData map[string]struct {
			Path string `json:"path"`
		} `json:"requireData"`
	} `json:"pages"`
	Partials struct {
		CarouselBannerPartial struct {
			CarouselBanner struct {
				Banners map[string]struct {
					AssetName    string `json:"asset_name"`
					BigAssetName string `json:"big_assetname"`
					IsShow       *bool  `json:"isShow"`
					OnClick      struct {
						Method  string `json:"method"`
						Message string `json:"message"`
					} `json:"OnClick"`
				} `json:"banners"`
			} `json:"CarouselBanner"`
		} `json:"CarouselBannerPartial"`
	} `json:"partials"`
}

func loadActivityConfFixture(t *testing.T) (activityConfFixture, string) {
	t.Helper()
	assetsDir := filepath.Join("..", "..", "assets", "cn350")
	raw, err := os.ReadFile(filepath.Join(assetsDir, "serve_app_conf_android.json"))
	if err != nil {
		t.Skipf("跳过：读不到 app_conf（%v）", err)
	}
	var conf activityConfFixture
	if err := json.Unmarshal(raw, &conf); err != nil {
		t.Fatalf("解析 app_conf: %v", err)
	}
	return conf, assetsDir
}

// TestActivityWhitelistMatchesAppConf 断言活动位白名单与 app_conf 同源且可跳转。
//
// ACT-BANNER-01（2026-10-21）：/act_pages 与 preset_data.data.act_pages 改为按
// activityPageWhitelist 下发（不再把 banners 全部 55 键抛给客户端）。白名单是
// 客户端 common.activityList 的唯一来源，三项约束都必须锁死，否则表现为
// 轮播/活动列表出现空白条目或点击进入空壳页：
//  1. 键必须存在于 partials.CarouselBannerPartial.CarouselBanner.banners
//     （CarouselBannerParts 用它查 appConfBanners，查不到即无图）；
//  2. OnClick.message 的 name= 目标页必须存在于 pages
//     （否则 PageBase.SetOption 仅告警 "Conf is Empty"，打开空白页）；
//  3. 横幅图必须走 common/*（CN3.5 只有该通道可出图）；
//  4. 不得选 isShow:false 的键（轮播会过滤，但 ActivityInfoListRow 不过滤，
//     会以「活动列表里有、轮播里没有」的矛盾状态出现）。
func TestActivityWhitelistMatchesAppConf(t *testing.T) {
	conf, _ := loadActivityConfFixture(t)
	banners := conf.Partials.CarouselBannerPartial.CarouselBanner.Banners
	if len(banners) == 0 {
		t.Fatal("app_conf 未定义 banners")
	}
	if len(activityPageWhitelist) == 0 {
		t.Fatal("activityPageWhitelist 为空：activityList 空会让轮播/活动列表整体空白")
	}
	for _, id := range activityPageWhitelist {
		b, ok := banners[id]
		if !ok {
			t.Fatalf("白名单键 %q 不在 app_conf banners 中（轮播无图、活动列表也取不到文案）", id)
		}
		msg := b.OnClick.Message
		name := ""
		for _, part := range strings.Split(msg, "&") {
			if strings.HasPrefix(part, "name=") {
				name = strings.TrimPrefix(part, "name=")
			}
		}
		if name == "" {
			t.Fatalf("banners[%q].OnClick.message 未声明 name=（无法跳转）：%q", id, msg)
		}
		if _, ok := conf.Pages[name]; !ok {
			t.Fatalf("banners[%q] 目标页 %q 不在 app_conf pages 中：跳转会打开空白壳页", id, name)
		}
		if b.AssetName == "" {
			t.Fatalf("banners[%q] 缺 asset_name：轮播无图可取", id)
		}
		if !strings.HasPrefix(b.AssetName, "common/") {
			t.Fatalf("banners[%q] 横幅图 %q 非 common/*：CN3.5 客户端可能不出图", id, b.AssetName)
		}
		if b.BigAssetName != "" && !strings.HasPrefix(b.BigAssetName, "common/") {
			t.Fatalf("banners[%q] 大图 %q 非 common/*：CN3.5 客户端可能不出图", id, b.BigAssetName)
		}
		if b.IsShow != nil && !*b.IsShow {
			t.Fatalf("banners[%q] isShow:false：轮播会过滤而活动列表不过滤，不能进白名单", id)
		}
	}
}

// TestResolveActivityPageIDs 断言解析结果就是白名单（配置齐全时）。
func TestResolveActivityPageIDs(t *testing.T) {
	_, assetsDir := loadActivityConfFixture(t)
	ids, missing := resolveActivityPageIDs(assetsDir)
	if len(missing) != 0 {
		t.Fatalf("白名单键在 app_conf banners 中缺失：%v", missing)
	}
	if len(ids) != len(activityPageWhitelist) {
		t.Fatalf("解析结果 %v 与白名单 %v 数量不一致", ids, activityPageWhitelist)
	}
	for i := range ids {
		if ids[i] != activityPageWhitelist[i] {
			t.Fatalf("顺序不一致：%v vs %v", ids, activityPageWhitelist)
		}
	}
}

// TestActPagesHandlerShape：端点返回形状与客户端消费键一致。
func TestActPagesHandlerShape(t *testing.T) {
	ids := loadActivityPageIDs(filepath.Join("..", "..", "assets", "cn350"))
	if len(ids) == 0 {
		t.Fatal("activityPageIDs 为空：activityList 空会导致轮播/活动列表整块空白")
	}
	for _, id := range ids {
		if id == "" {
			t.Fatal("activityPageIDs 含空 id")
		}
	}
}
