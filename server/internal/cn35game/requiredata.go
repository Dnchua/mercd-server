// requiredata.go 真实 app_conf Home.requireData 所需的四个端点（LEGACY-ASSETS-02）。
//
// 背景：关服前真实 app_conf（v201710121006）的 Home.requireData 声明了
// user_presents / infos / LoginAdventureData / country_pick_page 四条依赖，
// 它们在 CN3.5 服务端从未注册。客户端 PageBase.WaitForAllDataLoaded
// （ilspy-cs/PageBase.cs:782-800）对任一 requireData 失败即
// ShowErrorModal + CancelShowPage；未注册路由的 501 被判为 ProtocolError
// （JSONRequester.cs:432）→ Home 被整体取消（实机 2026-09-27 复现「连接出错」）。
//
// 证据等级（见 docs/协议对接/cn350/contracts/22-game-recovery-endpoints.md §22.6）：
//   - 消费链（客户端读取的键与空值分支）：客户端取证，行号可回指。
//   - 响应语义「空集 = 无内容」：服务端重建；原服在无公告/无礼物时的确切
//     响应字节未证，故只声明「客户端有显式空态分支、空集安全」。
//   - user_presents / infos 的**内容**为运营数据，入库（0015），非逻辑常量。
//   - choose_attr_page 为玩法内容，无生产数据来源，返回结构完整空态并在
//     契约卡标注「原服语义未证」。
//   - adventure 自 LOGIN350-03（迁移 0036）起为**真实读面**（轮次配置 + 逐日
//     位图 + 当日奖励经 /user_presents/ 内容池发放），实现见 adventure_wire.go；
//     本文件的 adventureEmptyStateData 只作为「未装配/未配置轮次」的合法回落。
package cn35game

import (
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// userPresentsHandler /user_presents/：账号待领礼物列表。
//
// 消费证据：IconPresentParts.OnLoadOption（ilspy-cs/IconPresentParts.cs:10-27）
//
//	Document document = common.cache.Get("user_presents");
//	int count = document.docList.Count;
//	if (count > 0) { countLabel.text = $"{count}"; noticeGo.SetActive(true); }
//	else { noticeGo.SetActive(false); }
//
// 故响应 `data.user_presents` 必须是**数组**（docList 语义）：空数组 → 图标隐藏，
// 非空 → 显示数量与红点。元素字段按 UserPresentsListRow 消费
// （uuid/type_name/content_id/amount）构造；uuid 用于
// `/user_presents/receive/?uuid=` 领取路径（UserPresentsListRow.cs:86）。
func userPresentsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uid := req.UserID
		now := time.Now().Unix()

		records, err := loadUserPresents(dep, uid, now)
		if err != nil {
			return nil, err
		}
		items := make([]any, 0, len(records))
		for _, r := range records {
			items = append(items, presentRowDoc(dep, r))
		}

		c.Decoded = map[string]any{
			"cn35": true, "uid": uid, "endpoint": "user_presents",
			"count": len(items),
		}
		// 客户端 cache.Get("user_presents") 取 docList，故用数组承载。
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"user_presents": items},
		})
	})
}

// infosHandler /infos/index：公告面。
//
// 消费证据：IconInfoParts.OnLoadOption（ilspy-cs/IconInfoParts.cs:10-30）
//
//	Document document = common.cache.Get("infos");
//	if (document.Count <= 0) { noticeGo.SetActive(false); }
//	else if (common.cache.Has("most_recently_info")) { 比较 id 与 info_last_read_id }
//
// `just_header=true` 是客户端请求参数（Home 只要头部摘要）。原服的
// header/detail 二次形态差异**未取证**，故不臆造第二形态：本实现按
// 同源过滤后返回全量条目，title/body 都给出。
func infosHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := time.Now().Unix()
		records, err := loadInfos(dep, now)
		if err != nil {
			return nil, err
		}
		listed := make([]any, 0, len(records))
		for _, r := range records {
			listed = append(listed, map[string]any{
				"id":         r.ID,
				"category":   r.Category,
				"title":      r.Title,
				"body":       r.Body,
				"begin_at":   r.BeginAt,
				"end_at":     r.EndAt,
				"created_at": r.BeginAt,
			})
		}

		data := map[string]any{"infos": listed}
		// most_recently_info：IconInfoParts 用它比较已读 id 决定是否显示红点。
		// 仅在有公告时下发；无公告时不下发该键（客户端 `Has` 为 false → 红点隐藏）。
		if len(listed) > 0 {
			if first, ok := listed[0].(map[string]any); ok {
				data["most_recently_info"] = first
			}
		}

		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "infos",
			"count": len(listed), "just_header": req.Query.Get("just_header"),
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// adventureEmptyStateData /adventure/ 的**结构完整空态**。
//
// LOGIN350-03 起这只是「未装配轮次配置时的合法回落」，不再是唯一实现：
// 真实读面/发放见 adventure_wire.go + 0036 迁移。
//
// 消费证据：LoginAdventurePage.CheckLoginAdventure（ilspy-cs/LoginAdventurePage.cs:702-753）
// 请求 `/adventure/` 后 UpdateDataByResponse；若缓存有 LoginAdventureData 则调
// NewAdventureDataArrived（:654-671）：
//
//	LoginAdventureData lad = new(); if (!lad.Load(_json)) { LogError; return; }
//	... if (!int.TryParse(today_days, out _)) { Log("already shown, skipped"); return; }
//	windowToBeShown = true; TryShowCachedWindow();
//
// `Load` 分两步：先 `ParseObj(GetField("adventure_info"), "adventure", …)`（:304）
// 取记录层，再逐个 ParseInt 必填字段（:233-235）；任一失败即 Load=false →
// **不弹冒险窗但打一条 `E Unity: Error when parsing LoginAdventureData`**。
//
// 2026-10-26 实机取证（LOGIN350-03 基线轮，见
// artifacts/tasks/login350-03-login-adventure/evidence/device-baseline.md）：
// 历史空态（`adventure_info:{}` 缺 `adventure` 子键）**走的正是 Load 失败分支**，
// 每次冷启动都留一条 Unity error。故空态改为「Load 可解析 + today_days 非数字」，
// 落到客户端显式的 `LoginAdventure: already shown, skipped` 分支（:664-668）——
// 静默跳过、无错误日志（LA-02 要求「空态不报错」）。
func adventureEmptyStateData(uid string) map[string]any {
	return map[string]any{
		"LoginAdventureData": map[string]any{
			"adventure_info": map[string]any{
				// 记录层必填：缺 adventure 子键 ⇒ 客户端 Load 失败（上面取证）。
				"adventure": map[string]any{
					"adventure_info":   map[string]any{},
					"begin_date_i":     0,
					"reward_config_id": 0,
					"user_id":          AdventureUserIDNum(uid),
				},
			},
			// today_days 空串 → int.TryParse 失败 → 客户端"already shown, skipped"。
			"today_days":    "",
			"server_date_i": 0,
			"setting": map[string]any{
				"path": []any{},
				"days": []any{},
			},
		},
	}
}

// chooseAttrPageHandler /gachas/choose_attr_page/：属国（属性）抽选页数据。
//
// requireData 键为 country_pick_page（真实 app_conf Home.requireData）。
// 全量 .cs 中对 `country_pick_page` / `choose_attr_page` 的消费点**零命中**
// （静态 grep），故其消费 schema 未取证。按红线「未验证的推断不得写成事实」，
// 本实现只保证**请求成功**（使 Home 的 requireData 就绪），
// 下发结构完整的空态，不声明任何原服字段语义。
func chooseAttrPageHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "gachas_choose_attr_page",
			"mode": "empty_state", "schema": "unverified",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"country_pick_page": map[string]any{},
			},
		})
	})
}

// ---- 读面装配辅助 ----

// PresentRecord 一条待领礼物（读面行）。
type PresentRecord struct {
	UUID        string
	ContentType string
	ContentID   int64
	Amount      int64
	ExpireAt    int64
	CreatedAt   int64
}

// InfoRecord 一条公告（读面行）。
type InfoRecord struct {
	ID       int64
	Category string
	Title    string
	Body     string
	BeginAt  int64
	EndAt    int64
}

// PresentStore /user_presents 的内容池读取面。
// nil 时读面回空列表（图标隐藏），不报错。
type PresentStore interface {
	// UnreadPresents 返回某账号未领取且未过期的礼物（按 created_at 升序）。
	UnreadPresents(userID string, now int64) ([]PresentRecord, error)
}

// InfoStore /infos/index 的公告读取面。
// nil 时读面回空列表（红点隐藏），不报错。
type InfoStore interface {
	// ActiveInfos 返回时间窗内启用的公告（weight DESC, id DESC）。
	ActiveInfos(now int64) ([]InfoRecord, error)
}

// loadUserPresents 读礼物：store 未装配时回空列表（空态合法，见文件头说明）。
func loadUserPresents(dep Deps, uid string, now int64) ([]PresentRecord, error) {
	if dep.Presents == nil {
		return nil, nil
	}
	return dep.Presents.UnreadPresents(uid, now)
}

// loadInfos 读公告：store 未装配时回空列表。
func loadInfos(dep Deps, now int64) ([]InfoRecord, error) {
	if dep.Infos == nil {
		return nil, nil
	}
	return dep.Infos.ActiveInfos(now)
}
