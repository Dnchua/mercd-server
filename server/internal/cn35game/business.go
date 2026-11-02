// business.go CN 3.5.0 启动三件套的 handler：version_info / preset_data /
// asset_info / app_conf / unity3d 兜底 + codec-echo 自检。
//
// 传输形态（contracts/00 §1 + 20-static-assets.md）：
//   - game 通道请求必须 DecodeGameRequest（APP_ID_1→userId→SessionStore→S0/S1）；
//     任何解码/会话失败直接错误返回，不进入业务。
//   - game 响应经 WriteGameResponse 加密下发（ENCRYPTED:true + type301）。
//   - static 文件明文下发（客户端 _isStaticFile 先试 zlib 失败按 text）。
//
// 静态/动态分离：fixture 提供静态骨架（consts/env/tutorial_session/app_conf/
// asset_info），data.user/user_coin/user_summary 由 AccountStore.Snapshot
// 注入；consts.time/stamp 为服务端时钟，每次下发重写（fixture 置 0 占位）。
package cn35game

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/appconf"
	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// fixture 文件名（assets/cn350/，由 server/tools/cn350_assets.py 生成）。
const (
	assetInfoFile  = "serve_asset_info_android4_v2_short.txt"
	appConfFile    = "serve_app_conf_android.json"
	presetDataFile = "serve_preset_data.json"
)

// gameHandler 包装 game 通道公共解码：身份头→会话→e=/表单。
// fn 返回的 payload 经 WriteGameResponse 加密（encrypted=true）下发。
func gameHandler(dep Deps, fn func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error)) httpapi.Handler {
	return func(c *httpapi.Ctx) error {
		req, err := cn35wire.DecodeGameRequest(c.R, c.Body, dep.Sessions)
		if err != nil {
			return fmt.Errorf("CN3.5 game 请求解码失败: %w", err)
		}
		payload, err := fn(c, req)
		if err != nil {
			return err
		}
		return cn35wire.WriteGameResponse(c.W, payload, req.Session, true)
	}
}

// versionInfoHandler GET /users/version_info：
// 版本号从 fixture 实况解析——asset_info 取 `version,<n>` 首行，
// app_conf 取顶层 "version"——保证与下发文件一致（客户端按版本做缓存失效）。
func versionInfoHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		assetInfoVer, appConfVer, err := loadVersions(dep.AssetsDir)
		if err != nil {
			return nil, err
		}
		// 远端叠加管理器已产出快照时，asset_info_version 取有效版本——
		// 必须与下发清单首行同值（客户端按版本做清单缓存失效）。
		if dep.RemoteAssets != nil && dep.RemoteAssets.Scanned() {
			assetInfoVer = dep.RemoteAssets.Version()
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID,
			"asset_info_version": assetInfoVer, "app_conf_version": appConfVer,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"asset_info_version": assetInfoVer,
			"app_conf_version":   appConfVer,
		})
	})
}

// loadVersions 从 cn350 fixture 解析两个版本号；坏配置在首请求即报错。
func loadVersions(assetsDir string) (assetInfoVer, appConfVer int64, err error) {
	raw, err := os.ReadFile(filepath.Join(assetsDir, assetInfoFile))
	if err != nil {
		return 0, 0, fmt.Errorf("cn35 asset_info fixture 读取失败: %w", err)
	}
	first, _, _ := strings.Cut(string(raw), "@")
	if !strings.HasPrefix(first, "version,") {
		return 0, 0, fmt.Errorf("cn35 asset_info fixture 首行缺 version,: %.32q", first)
	}
	assetInfoVer, err = strconv.ParseInt(strings.TrimSpace(first[len("version,"):]), 10, 64)
	if err != nil || assetInfoVer == 0 {
		return 0, 0, fmt.Errorf("cn35 asset_info version 非法: %q", first)
	}
	confRaw, err := os.ReadFile(filepath.Join(assetsDir, appConfFile))
	if err != nil {
		return 0, 0, fmt.Errorf("cn35 app_conf fixture 读取失败: %w", err)
	}
	var conf struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal(confRaw, &conf); err != nil || conf.Version == 0 {
		return 0, 0, fmt.Errorf("cn35 app_conf version 非法: %v", err)
	}
	return assetInfoVer, conf.Version, nil
}

// presetDataHandler GET /users/preset_data.json：
// fixture 骨架 + Snapshot 动态注入。会话未绑定/无快照 → 错误返回
// （不回放共享玩家，契约 11 的 game_status 双刃剑约束由快照实现负责）。
func presetDataHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		raw, err := os.ReadFile(filepath.Join(dep.AssetsDir, presetDataFile))
		if err != nil {
			return nil, fmt.Errorf("cn35 preset_data fixture 读取失败: %w", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("cn35 preset_data fixture 非法 JSON: %w", err)
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 preset_data 快照失败: %w", err)
		}
		data, ok := doc["data"].(map[string]any)
		if !ok {
			data = make(map[string]any)
			doc["data"] = data
		}
		data["user"] = snap.User
		data["user_coin"] = snap.UserCoin
		// TASK350：tutorial_task_config 与 doing 列表联动（QuestTaskManager
		// 要求两边任务数一致；客户端仅 task_v2 分支消费）。
		data["tutorial_task_config"] = tutorialTaskConfigDocs()
		if snap.UserSummary != nil {
			data["user_summary"] = snap.UserSummary
		}
		// PLAY350-07：leader/helper 引用单位实例——DB/内存桩存 TEXT，
		// 客户端 i() 比较需 JSON NUMBER（见 inventory_wire.go）。
		numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
		// PLAY350-07：与 /user_units 同一组装面注入库存键（空库存合法，
		// 下发空数组；Inventory 未装配时保持 fixture 原样）。
		if dep.Inventory != nil && dep.Catalog != nil {
			if invData, ierr := inventoryDataFor(dep, c, req.UserID); ierr == nil {
				for k, v := range invData {
					data[k] = v
				}
			} else {
				return nil, ierr
			}
		}
		if dep.Decks != nil {
			decks, derr := dep.Decks.Decks(req.UserID)
			if derr != nil {
				return nil, fmt.Errorf("cn35 preset_data 编队快照失败: %w", derr)
			}
			data["user_decks"] = DecksWire(decks)
		}
		// ACT-BANNER-01（2026-10-21）：活动位随登录快照下发，与原版同源。
		// 客户端只有一个 act_pages 消费点（DataCache.cs:265-267 → common.activityList），
		// 而真实 app_conf 无任何页面 requireData.act_pages —— 原服即靠 preset_data
		// 携带该键。这里由服务端白名单注入（resolveActivityPageIDs），避免 fixture
		// 里写死玩家可见活动、也避免空数组把缓存占住（UI-HOME-FIX 记录过的坑）。
		// act_has_new 取「签到可领」这一可推导事实，驱动首页活动图标红点。
		actIDs, actMissing := resolveActivityPageIDs(dep.AssetsDir)
		if len(actMissing) > 0 && dep.Log != nil {
			dep.Log.Warn("cn35 preset_data act_pages 白名单键在 app_conf banners 中缺失",
				"missing", actMissing)
		}
		actPages := make([]any, 0, len(actIDs))
		for _, id := range actIDs {
			actPages = append(actPages, id)
		}
		data["act_pages"] = actPages
		data["act_has_new"] = activityHasNew(dep, req.UserID, time.Now().Unix())
		// GACHA350-06/真机验收修正：抽卡读面**不**再随 preset_data 预置。
		// app_conf Gacha 页声明 requireData.gachas.path=/gachas，而
		// DataCache.DataLoad 在 cache 已有键时跳过请求——preset_data 预置
		// gachas 会让 /gachas 端点在真机上永不触发（首轮验收 requests
		// 日志为证）。现改为只经 requireData 拉取：/gachas 响应一次性下发
		// gachas + gacha_warnings + gacha_button_options + 名单数据，
		// 与概率规则同源，加载完毕才放行页面就绪（PageBase.cs:325）。
		// 已缓存后切页不重复请求，是客户端原生语义，不是缺口。
		// 服务端时钟字段：UpdateConst 用 time 校 clientServerTimeDifference。
		if consts, ok := data["consts"].(map[string]any); ok {
			// 服务端权威时钟：装配了 GuildClock（-guild-clock-offset 验收手段）
			// 时，下发的 time/stamp 与被偏移的会战判定时钟保持一致，否则客户端
			// 仍按本机时间选 fields 时窗，会与 battle_condition 打架（实机现象：
			// 已进可战场次但主页显示「今日高挂免战牌」）。
			now := time.Now().Unix()
			if dep.GuildClock != nil {
				now = dep.GuildClock().Unix()
			}
			consts["time"] = now
			consts["stamp"] = now
			// GUILD350：会战难度行说明（BattleQuestSelectParts 读
			// consts.battle_difficulty.<diff>.{interval,rate} 两个字符串
			// 拼 "\n{interval}\nGP {rate}%"）。展示面；实际波次间隔与
			// 掠夺率由 GuildRules 同名难度表在服务端落地。
			consts["battle_difficulty"] = map[string]any{
				"normal": map[string]any{
					"interval": GuildRules.DifficultyIntervalLabel["normal"],
					"rate":     GuildRules.DifficultyRateLabel["normal"],
				},
				"hard": map[string]any{
					"interval": GuildRules.DifficultyIntervalLabel["hard"],
					"rate":     GuildRules.DifficultyRateLabel["hard"],
				},
				"very_hard": map[string]any{
					"interval": GuildRules.DifficultyIntervalLabel["very_hard"],
					"rate":     GuildRules.DifficultyRateLabel["very_hard"],
				},
			}
		}
		// STAT350-03B：hardness 表默认不下发（保留客户端回退）；仅显式启用
		// 重建测试 profile 时注入。表存在会关掉客户端已知回退（C0.4）。
		if dep.HardnessProfile != "" {
			p := dep.HardnessProfile
			if !filepath.IsAbs(p) {
				p = filepath.Join(dep.AssetsDir, p)
			}
			rows, herr := LoadHardnessProfile(p)
			if herr != nil {
				return nil, herr
			}
			data["hardness"] = rows
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID,
			"tutorial_session": req.Query.Get("tutorial_session"),
			"method":           req.Method,
		}
		return cn35wire.MarshalJSON(doc)
	})
}

// staticFileHandler 静态文件端点（GET，明文下发）。renderWire=true 时
// 走 appconf.RenderWire 管线（剥离 "_" 注解键→紧凑序列化）。
func staticFileHandler(dep Deps, fileName string, renderWire bool) httpapi.Handler {
	full := filepath.Join(dep.AssetsDir, fileName)
	return func(c *httpapi.Ctx) error {
		body, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("cn35 静态配置 %s 读取失败: %w", full, err)
		}
		if renderWire {
			body, err = appconf.RenderWire(body)
			if err != nil {
				return fmt.Errorf("cn35 app_conf 映射出包失败: %w", err)
			}
		}
		c.Decoded = map[string]any{"cn35_static": fileName, "bytes": len(body)}
		c.W.Header().Set("Content-Type", "application/octet-stream")
		c.W.Header().Set("Content-Length", fmt.Sprint(len(body)))
		c.W.Header().Set("X-Mercd-CN35", "static")
		c.W.WriteHeader(http.StatusOK)
		_, _ = c.W.Write(body)
		return nil
	}
}

// assetInfoHandler GET /asset_info_android4_v2_short.json.encode：
// 由远端叠加管理器快照出 body（基线 + 远端 md5 行）。管理器尚未产出
// 快照（首次扫描失败）时回退 fixture 直读，保持与 staticFileHandler
// 相同的明文下发形态。
func assetInfoHandler(dep Deps) httpapi.Handler {
	full := filepath.Join(dep.AssetsDir, assetInfoFile)
	return func(c *httpapi.Ctx) error {
		body := dep.RemoteAssets.Body()
		if len(body) == 0 {
			var err error
			body, err = os.ReadFile(full)
			if err != nil {
				return fmt.Errorf("cn35 静态配置 %s 读取失败: %w", full, err)
			}
		}
		c.Decoded = map[string]any{"cn35_static": assetInfoFile, "bytes": len(body),
			"remote_overlay": dep.RemoteAssets.Scanned()}
		c.W.Header().Set("Content-Type", "application/octet-stream")
		c.W.Header().Set("Content-Length", fmt.Sprint(len(body)))
		c.W.Header().Set("X-Mercd-CN35", "static")
		c.W.WriteHeader(http.StatusOK)
		_, _ = c.W.Write(body)
		return nil
	}
}

// bundleHandler GET /toto_image_4/unity/{urlName}.Android.{ver}.unity3d：
// 先按 Asset.urlName 规则反查远端叠加快照（unit_v2→unit、monster_v2→
// monster、effect_ss_v2→effect_ss），命中稳定条目即 ServeFile；未命中
// 保持 BundlesDir 原始路径兜底（内嵌缺失项）。BundlesDir 缺省或文件
// 不存在时 404（httpapi 记取证），不伪造资源。
func bundleHandler(dep Deps) httpapi.Handler {
	return func(c *httpapi.Ctx) error {
		// 末段文件名白名单式清洗：只允许 [A-Za-z0-9_./-] 且拒绝 .. 穿越。
		name := strings.TrimPrefix(c.R.URL.Path, "/toto_image_4/unity/")
		if name == "" || strings.Contains(name, "..") {
			return fmt.Errorf("cn35 bundle 路径非法: %q", c.R.URL.Path)
		}
		if dep.RemoteAssets != nil {
			if logical, ok := bundleLogicalName(name); ok {
				if e, hit := dep.RemoteAssets.Entry(logical); hit {
					c.Decoded = map[string]any{"cn35_bundle": name,
						"remote_entry": logical, "bytes": e.Size}
					serveBundleFile(c, e.File)
					return nil
				}
			}
		}
		if dep.BundlesDir == "" {
			return fmt.Errorf("cn35 bundle %s 未配置 BundlesDir（内嵌缺失项: %s）", name, name)
		}
		full := filepath.Join(dep.BundlesDir, filepath.FromSlash(name))
		fi, err := os.Stat(full)
		if err != nil || fi.IsDir() {
			if logical, ok := bundleLogicalName(name); ok {
				alt := filepath.Join(dep.BundlesDir, filepath.FromSlash(logical+".Android.unity3d"))
				if afi, aerr := os.Stat(alt); aerr == nil && !afi.IsDir() {
					full, fi, err = alt, afi, nil
				}
			}
		}
		if err != nil || fi.IsDir() {
			return fmt.Errorf("cn35 bundle 缺失: %s（%w）", name, err)
		}
		c.Decoded = map[string]any{"cn35_bundle": name, "bytes": fi.Size()}
		serveBundleFile(c, full)
		return nil
	}
}

// serveBundleFile 统一 unity3d 响应头 + ServeFile。
func serveBundleFile(c *httpapi.Ctx, full string) {
	c.W.Header().Set("Content-Type", mime.TypeByExtension(".unity3d"))
	if ct := c.W.Header().Get("Content-Type"); ct == "" {
		c.W.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeFile(c.W, c.R, full)
}

// remoteAssetsInfoHandler GET /cn35/assets/remote：远端叠加只读诊断——
// 有效/基线版本、远端目录、稳定条目与 pending/rejected 列表。明文 JSON，
// 仅运维/测试用，不走游戏加密通道。
func remoteAssetsInfoHandler(dep Deps) httpapi.Handler {
	return func(c *httpapi.Ctx) error {
		rep := dep.RemoteAssets.Report()
		body, err := cn35wire.MarshalJSON(rep)
		if err != nil {
			return err
		}
		c.Decoded = map[string]any{"cn35_remote_assets": true,
			"entries": len(rep.Entries), "pending": len(rep.Pending),
			"rejected": len(rep.Rejected)}
		c.W.Header().Set("Content-Type", "application/json")
		c.W.WriteHeader(http.StatusOK)
		_, _ = c.W.Write(body)
		return nil
	}
}

// catalogInfoHandler GET /cn35/catalog/info：PLAY350-04 只读诊断——
// 下发 catalog Meta（版本/SHA256/计数）与质量警告汇总，证明 mercd35
// 实际装配了卡牌目录。明文 JSON，仅运维/测试用，不走游戏加密通道。
func catalogInfoHandler(dep Deps) httpapi.Handler {
	return func(c *httpapi.Ctx) error {
		meta := dep.Catalog.Meta()
		rep := dep.Catalog.Report()
		body, err := cn35wire.MarshalJSON(map[string]any{
			"meta":          meta,
			"warn_count":    len(rep.Warn),
			"warn_by_kind":  countKinds(rep.Warn),
			"sample_issues": rep.Warn[:min(len(rep.Warn), 10)],
		})
		if err != nil {
			return err
		}
		c.Decoded = map[string]any{"cn35_catalog_info": true,
			"units": meta.UnitCount, "monsters": meta.MonsterCount}
		c.W.Header().Set("Content-Type", "application/json")
		c.W.WriteHeader(http.StatusOK)
		_, _ = c.W.Write(body)
		return nil
	}
}

func countKinds(issues []cardcatalog.Issue) map[string]int {
	out := map[string]int{}
	for _, i := range issues {
		out[string(i.Kind)]++
	}
	return out
}

// codecEchoHandler POST /cn35/codec-echo：verify 帧自检——
// 解出 method 列表后回 results=[{method,retCode:200}]（不播种、无业务副作用）。
// 供 08 联调与运维探活：能解 = 协议栈装配正确。
func codecEchoHandler(dep Deps) httpapi.Handler {
	return func(c *httpapi.Ctx) error {
		req, err := cn35wire.DecodeVerifyRequest(c.Body)
		if err != nil {
			return err
		}
		results := make([]cn35wire.VerifyResult, 0, len(req.Methods))
		for _, m := range req.Methods {
			results = append(results, cn35wire.VerifyResult{"method": m, "retCode": 200, "echo": true})
		}
		c.Decoded = map[string]any{"cn35_codec_echo": true, "methods": req.Methods}
		return cn35wire.WriteVerifyResponse(c.W, results, nil, req.Methods, "", "")
	}
}
