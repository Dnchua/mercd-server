// activity.go CN 3.5.0 活动页/横幅端点（CN35-UI-RESTORE）。
//
// 契约依据（客户端消费侧，ilspy-cs 反编译）：
//   - DataCache.cs:265-267：缓存键 "act_pages" → common.activityList = _value.docList
//   - Common.cs:713-714：appConfBanners =
//     app_conf.partials.CarouselBannerPartial.CarouselBanner.banners（docHash）
//   - BasicParts.cs:207-250（texture widget，top_activity=true）：
//     取 activityList[0]（字符串键）→ appConfBanners[key] → 读其 big_assetname
//     覆盖本 widget 的 asset_name，并把该 banner 的 OnClick 复制过来。
//     故服务端必须保证 activityList[0] 是 **banners 里存在的键**，
//     否则 docHash 取到 null（客户端有 try/catch，表现为大图静默不显示）。
//   - CarouselBannerParts：数据源同为 appConfBanners，按 banners 键序渲染轮播。
//
// 因此 /act_pages 返回的 id 必须与 app_conf partials 的 banners 键一一对应。
// 二者是同一份配置的两半：改一处必须改另一处（本文件与 serve_app_conf_android.json）。
package cn35game

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// activityPageWhitelist 当前下发到活动位的 banner 白名单（按展示优先级排序）。
//
// 2026-10-21（ACT-BANNER-01）：活动位从「app_conf 全部 55 个键」收窄为显式白名单。
// 原因：真实配置里 7 个 banner 的目标页在 app_conf 中不存在、22 个 banner 的图已
// 随 CDN 清退、10 个 banner 标了 isShow:false（但 ActivityInfoListRow 不过滤 isShow，
// 一起下发会出现在活动列表），全量下发会让首页轮播/活动列表出现空白与死链。
//
// 每个键都必须满足三条（activity_test.go 锁死）：
//  1. 存在于 app_conf partials.CarouselBannerPartial.CarouselBanner.banners；
//  2. 其 OnClick.message 的目标页存在于 app_conf pages；
//  3. 横幅图 asset_name 走 common/*（CN3.5 只有该通道可出图）。
//
// 现选四项与目标页：
//
//	banner6               每日签到得好礼 → AccLoginRewardsActivity（真实读/领面）
//	banner_gacha          钻石限定★5招募 → Gacha（/gachas 真实）
//	banner_medal_exchange 徽章交换所     → ExchangeShopScrollable
//	banner_gacha_sale     钻石限定优惠   → Gacha（同页第二入口）
var activityPageWhitelist = []string{
	"banner6",
	"banner_gacha",
	"banner_medal_exchange",
	"banner_gacha_sale",
}

// activityIDsCache 缓存从 app_conf 解析出的 banners 键序（按资产目录粒度）。
// app_conf 是静态 fixture，进程内缓存即可；测试与重启会重新读取。
var (
	activityIDsMu    sync.Mutex
	activityIDsCache = map[string][]string{}
)

// resolveActivityPageIDs 返回白名单与 app_conf banners 的交集（保持白名单顺序），
// 同时返回配置里缺失的白名单键供调用方告警（静默漂移会导致活动位少项）。
//
// 读不到 app_conf 时回退白名单本身：活动位宁可多给，也不让 activityList 为空
// （空列表会让首页轮播/活动大图/活动列表整体空白，见 CarouselBannerParts.cs:126）。
func resolveActivityPageIDs(assetsDir string) (ids []string, missing []string) {
	activityIDsMu.Lock()
	confKeys, cached := activityIDsCache[assetsDir]
	if !cached || len(confKeys) == 0 {
		raw, err := os.ReadFile(filepath.Join(assetsDir, appConfFile))
		if err == nil {
			confKeys = bannersKeyOrder(raw)
		}
		if len(confKeys) > 0 {
			activityIDsCache[assetsDir] = confKeys
		}
	}
	activityIDsMu.Unlock()
	if len(confKeys) == 0 {
		return append([]string(nil), activityPageWhitelist...), nil
	}
	inConf := make(map[string]bool, len(confKeys))
	for _, k := range confKeys {
		inConf[k] = true
	}
	ids = make([]string, 0, len(activityPageWhitelist))
	for _, k := range activityPageWhitelist {
		if inConf[k] {
			ids = append(ids, k)
		} else {
			missing = append(missing, k)
		}
	}
	if len(ids) == 0 {
		return append([]string(nil), activityPageWhitelist...), missing
	}
	return ids, missing
}

// loadActivityPageIDs 兼容旧测试/调用点的便捷形态。
func loadActivityPageIDs(assetsDir string) []string {
	ids, _ := resolveActivityPageIDs(assetsDir)
	return ids
}

// activityHasNew 活动图标红点：签到现在有可领节点即为 true。
//
// 原版 act_has_new 由运营按下发；重建规则取「签到可领」这一可推导事实，避免
// 恒 true 让红点常亮（旧实现固定 true）。未装配 Signin 时不点亮。
func activityHasNew(dep Deps, userID string, now int64) bool {
	if dep.Signin == nil {
		return false
	}
	st, err := dep.Signin.SigninState(userID, now)
	if err != nil || st == nil {
		return false
	}
	limit := st.Day
	if limit > SigninNodeCount {
		limit = SigninNodeCount
	}
	for n := 1; n <= limit; n++ {
		if !st.SigninClaimed(n) {
			return true
		}
	}
	return false
}

// bannersKeyOrder 从 app_conf 原文中提取 partials.CarouselBannerPartial.
// CarouselBanner.banners 的键序。
//
// 为什么要保序：客户端 BasicParts 的 top_activity 只消费 activityList[0]，
// 键序即展示优先级，map[string]any 会丢序，故用 json.Decoder 流式下降。
func bannersKeyOrder(raw []byte) []string {
	root, err := decodeOrdered(raw)
	if err != nil {
		return nil
	}
	partials := childObject(root, "partials")
	cbp := childObject(partials, "CarouselBannerPartial")
	cb := childObject(cbp, "CarouselBanner")
	bn := childObject(cb, "banners")
	if bn == nil {
		return nil
	}
	keys := make([]string, 0, len(bn.pairs))
	for _, p := range bn.pairs {
		keys = append(keys, p.key)
	}
	return keys
}

// orderedObject 是保序的 JSON 对象节点；仅需要键序，故值只区分「对象/非对象」。
type orderedObject struct {
	pairs []orderedPair
}

type orderedPair struct {
	key string
	val *orderedObject // 非对象为 nil
}

// decodeOrdered 解析 JSON 顶层对象并保留成员顺序。
func decodeOrdered(raw []byte) (*orderedObject, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("app_conf 顶层不是对象")
	}
	return readObject(dec)
}

// readObject 读取一个对象体（'{' 已消费），递归保留键序。
func readObject(dec *json.Decoder) (*orderedObject, error) {
	obj := &orderedObject{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := kt.(string)
		vt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if d, ok := vt.(json.Delim); ok && d == '{' {
			sub, err := readObject(dec)
			if err != nil {
				return nil, err
			}
			obj.pairs = append(obj.pairs, orderedPair{key: key, val: sub})
			continue
		}
		obj.pairs = append(obj.pairs, orderedPair{key: key})
		if err := skipRemainder(dec, vt); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil { // 消费 '}'
		return nil, err
	}
	return obj, nil
}

// skipRemainder 消费非对象值；若首 token 是 '[' 则跳过整个数组。
func skipRemainder(dec *json.Decoder, first json.Token) error {
	d, ok := first.(json.Delim)
	if !ok || d != '[' {
		return nil
	}
	for dec.More() {
		vt, err := dec.Token()
		if err != nil {
			return err
		}
		if inner, ok := vt.(json.Delim); ok && inner == '{' {
			if _, err := readObject(dec); err != nil {
				return err
			}
			continue
		}
		if err := skipRemainder(dec, vt); err != nil {
			return err
		}
	}
	_, err := dec.Token() // ']'
	return err
}

// childObject 按键取子对象，未命中返回 nil。
func childObject(o *orderedObject, key string) *orderedObject {
	if o == nil {
		return nil
	}
	for _, p := range o.pairs {
		if p.key == key {
			return p.val
		}
	}
	return nil
}

// actPagesHandler GET /act_pages：活动页 id 列表 + 红点标志。
//
// 为什么返回非空：BasicParts 的 top_activity 分支要求
// activityList.Count > 0 才会去 appConfBanners 取图；返回空数组会让
// 左侧活动大图恒为空白。act_has_new 驱动 Home 的活动图标红点。
func actPagesHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		ids, missing := resolveActivityPageIDs(dep.AssetsDir)
		if len(missing) > 0 && dep.Log != nil {
			dep.Log.Warn("cn35 act_pages 白名单键在 app_conf banners 中缺失（活动位将少项）",
				"missing", missing)
		}
		pages := make([]any, 0, len(ids))
		for _, id := range ids {
			pages = append(pages, id)
		}
		hasNew := activityHasNew(dep, req.UserID, time.Now().Unix())
		first := ""
		if len(ids) > 0 {
			first = ids[0]
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "act_pages",
			"act_page_count": len(pages), "act_has_new": hasNew,
			"first": first, "source": "activityPageWhitelist",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"act_pages":   pages,
				"act_has_new": hasNew,
			},
		})
	})
}
