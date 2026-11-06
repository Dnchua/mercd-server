// gacha_wire.go GACHA350-05：/gachas/execute 与 /users/reload_data 协议端点。
//
// 客户端契约（ILSpy 反编译实证，行号回指 artifacts/tasks/toto-cn-3.5.0-onboarding/run/ilspy-cs/）：
//
//	GachaIndexParts.cs:156  单抽 GET /gachas/execute?id=<i("id")>&count=1
//	GachaIndexParts.cs:238  连抽 GET /gachas/execute?id=<s("id")>&count=<multiExecuteCount>
//	GachaIndexParts.cs:97-104  可选附加 &use_scout_book=true（首期关闭）
//	GachaIndexParts.cs:325  错误分支 GET /users/reload_data?on=gachas_execute
//
// 请求由 modal.ShowYesNoModal(title, body, url, callback) 发出
// （GachaIndexParts.cs:158/240 → Modal.cs:567 JSONRequester.GetFromServer），
// 查询串整段加密为 ?e=<密文>（JSONRequester.cs:289），服务端解密后
// url.ParseQuery 解析（cn35wire.DecodeGameRequest）。参数名无白名单。
//
// 响应消费（Common.cs:2583-2595）：data.temp_gacha_result 的 docList
// **Count>0 才**进 GachaResult 页，否则弹 ServerErrorModal——空列表等同失败，
// 不能当成功响应。每项由 GachaResult.cs:91 读 id/rare_type_id/level/max_level，
// 并按 rare_type_id 升序排序展示（**不是**抽取顺序）。
//
// 结果行渲染契约（ilspy：GachaResult.cs UpdateDisplay → UnitListRow.cs
// isScoutResult 分支，实机截图印证）：
//   - 行部件是 UnitListRow（单抽 UserUnitLargeRow / 连抽 UserUnitIconRow），
//     字段口径与 user_units 行一致：main_name/prefix_name/rare_type_name
//     （星级）/level/max_level/duplicated_count（觉醒等级）/bonus_sp
//     （SP加成）/skill_slot_count（符文孔）/asset_name。复用 buildUnitDoc+
//     applyUnitSkills 组装，招募页与同伴列表同口径。
//   - newcomer → New 角标；重复卡 awake=true 且带 before_state 文档——
//     客户端把 before_state 与本文档并排渲染觉醒前后数值（已封顶时前后
//     同值，上限变化如实可见）。
//
// 扣费不由响应回传：客户端自己按 gacha.point（金币）/gacha.price（钻石）
// 计算并上报埋点（GachaIndexParts.cs:186-203/267-297）。服务端只需回推
// data.user（gacha_point）与 data.user_coin（amount）供 cache 更新。
package cn35game

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

// gachaMaxCount 是服务端独立钳制的次数上限（不信任客户端按钮状态）。
const gachaMaxCount = 10

// 重放判定窗口（有界幂等，见 gacha.go 头注释）：
//
//	gachaReplayWindowSec  同参请求距最近一次 committed 在该窗口内 →
//	                      判定传输层重发，回放快照（毫秒级自动重发必然落内；
//	                      玩家手动重抽需走完结果页→关闭→再点→确认，实测
//	                      不可能在该窗口内完成）。
//	gachaReplayMaxAgeSec  快照可回放的最大年龄；超出后即使 reload 信号
//	                      命中也按新抽处理，避免陈旧快照污染体验。
//
// reload_data 支（GachaIndexParts.cs:160-164 实证）：客户端**成功**路径
// 走 ShowUnitGetAnimation 不发请求；**失败**路径才 ReloadData()。因此
// "committed 之后出现过 on=gachas_execute 的 reload_data"可识别玩家
// 经错误 modal 重试——该支不设窗口，覆盖"响应丢失+慢速重试"。
// 内部时间戳一律纳秒（UnixNano）——客户端重试与结算常落在同一秒，
// 秒级精度会把"结算后到达的 reload"漏判为"之前"。
//
// 残余风险（如实标注）：lastReloadAt 为内存态，服务端重启即丢失；
// "结算成功 + 响应丢失 + 服务重启 + 玩家重试"组合会落为新抽（多扣
// 一次费）。该场景需要 adb 直连链路丢响应且恰好重启，概率极低；
// 文档（note §4 / contracts 22）如实记录此边界，不声称严格幂等。
var gachaReplayWindowSec int64 = 5
var gachaReplayMaxAgeSec int64 = 600

// GachaSetReplayWindowForTest 调整重放窗口参数，仅测试用（进程级生效，
// 调用方需自行恢复原值）。置于 var 而非 const 即为此目的。
func GachaSetReplayWindowForTest(window, maxAge int64) {
	gachaReplayWindowSec, gachaReplayMaxAgeSec = window, maxAge
}

// gachaClientState 是每个账号的客户端行为记忆（进程内）：
//
//	lastAttemptAt  最近一次**非重放** execute（新抽或被拒）的到达时间
//	               （UnixNano）。重放请求不计——结果未送达前玩家可
//	               无限次重试。
//	lastReloadAt   最近一次 on=gachas_execute 的 reload_data 到达时间
//	               （UnixNano，客户端错误路径信号）。
type gachaClientState struct {
	mu            sync.Mutex
	lastAttemptAt int64
	lastReloadAt  int64
}

// gachaClientStates 按账号隔离的行为记忆。重启丢失只影响 reload 支的
// 重放判定（见上方残余风险说明）。
var gachaClientStates sync.Map // userID → *gachaClientState

func gachaStateFor(userID string) *gachaClientState {
	v, _ := gachaClientStates.LoadOrStore(userID, &gachaClientState{})
	return v.(*gachaClientState)
}

// gachaExecuteHandler 处理单抽/连抽。
func gachaExecuteHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		poolID := firstNonEmpty(req.Query.Get("id"), req.Form.Get("id"))
		countRaw := firstNonEmpty(req.Query.Get("count"), req.Form.Get("count"))
		// 首期关闭票券：use_scout_book 只记录不生效。
		useScoutBook := firstNonEmpty(req.Query.Get("use_scout_book"), req.Form.Get("use_scout_book"))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "gachas_execute",
			"pool_id": poolID, "count": countRaw, "use_scout_book": useScoutBook,
		}

		if dep.Gacha == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(gachaError(
				"招募暂未开放", "服务端未装配招募写面。"))
		}

		// 同账号 execute 串行：重放判定与结算必须在同一临界区内，
		// 否则并发同参请求会双双落入"新抽"分支造成重复扣费。
		// （不同账号互不影响；同客户端不可能并发发起两个 execute——
		// modal 一次只有一个，但传输层重发可能并发到达。）
		st := gachaStateFor(req.UserID)
		st.mu.Lock()
		defer st.mu.Unlock()
		// arrived 必须在锁内取：并发重发在锁外等待期间，前一笔已提交，
		// 锁外取时间会让"到达时间"早于上次 committed → age<0 误判新抽。
		arrived := time.Now()
		body, attempted, err := gachaExecuteSettle(dep, c, req, st, arrived,
			poolID, countRaw, useScoutBook)
		if attempted {
			st.lastAttemptAt = arrived.UnixNano()
		}
		return body, err
	})
}

// gachaReject 组装拒绝响应并标记为"新尝试"（拒绝消耗 reload 信号：
// 玩家被明确拒绝后再来的同参请求应按新尝试对待，而非回放旧快照）。
func gachaReject(title, body string) ([]byte, bool, error) {
	b, _ := cn35wire.MarshalJSON(gachaError(title, body))
	return b, true, nil
}

// gachaExecuteSettle 执行校验、重放判定与结算。attempted=false 仅在
// 重放路径——该次请求不算"新尝试"，不消耗 reload 信号。
func gachaExecuteSettle(dep Deps, c *httpapi.Ctx, req *cn35wire.GameRequest,
	st *gachaClientState, arrived time.Time,
	poolID, countRaw, useScoutBook string) ([]byte, bool, error) {
	if useScoutBook != "" && useScoutBook != "false" {
		// 首期票券路径未建模（无票券库存与扣减事务），明确拒绝。
		return gachaReject("招募暂未开放", "招募券功能尚未开放。")
	}

	count, err := strconv.Atoi(countRaw)
	if err != nil || count < 1 || count > gachaMaxCount {
		return gachaReject("招募参数非法", "招募次数无效，请重新打开招募页面。")
	}

	// 规则真源 = 已发布版本快照（ROSTER350-03）：同一次请求内池定义、
	// 名单、权重、价格、保底全部锁定同一版本；未发布则执行面整体关闭。
	cfg, err := gachaActiveConfig(dep)
	if err != nil {
		return nil, true, err
	}
	if cfg == nil {
		return gachaReject("招募暂未开放", "招募配置尚未发布，请稍后再试。")
	}
	rules := cfg.Rules
	pool, ok := rules.poolByID(poolID)
	if !ok {
		// 旧槽位池（1 限时 / 4 高级金币）不在已发布版本内：明确拒绝，
		// 不可能进入扣费结算（ROSTER350-02/05 双池边界）。
		return gachaReject("招募暂未开放", "该招募已结束或不存在。")
	}
	if err := gachaPoolOpen(pool, arrived.Unix()); err != nil {
		return gachaReject("招募暂未开放", "该招募不在开放时间内。")
	}
	if count > pool.MaxExecutableCount {
		return gachaReject("招募次数超出上限",
			fmt.Sprintf("本次最多可招募 %d 次。", pool.MaxExecutableCount))
	}

	// 可中奖集合 = 已发布版本的成员名单（资源合格+启用，发布时已校验）；
	// 不再从 catalog 推导，杜绝"解析成功即可抽"。
	units := cfg.MembersByPool[pool.PoolID]
	if len(units) == 0 {
		return gachaReject("招募暂未开放", "该招募的同伴名单尚未准备好。")
	}

	// 读取当前保底进度（无记录即零值，不按持卡量反推）。
	// 版本口径：cn35_gacha_pity.rules_version 存的是保底参数指纹
	//（PityRulesVersion）——发布只调成员/价格不改保底参数时进度延续；
	// 保底参数变更才会触发"版本不一致→拒绝+人工处理"。
	group := rules.pityGroupFor(pool)
	state := gachaPityState{}
	if group != gachaPityGroupNone {
		row, ok, perr := dep.Gacha.GachaPity(req.UserID, group)
		if perr != nil {
			return nil, true, fmt.Errorf("cn35 抽卡保底读取失败: %w", perr)
		}
		if ok {
			if !gachaPityVersionCompatible(row.RulesVersion, group, rules.Pity[group]) {
				// 保底参数变更：不静默迁移计数，明确拒绝并让运营介入。
				return gachaReject("招募暂未开放", "招募规则已更新，请联系客服处理保底进度。")
			}
			state = gachaPityState{
				N4: row.N4, N5: row.N5,
				FeaturedGuaranteed: row.FeaturedGuaranteed,
			}
		}
	}

	engine, err := newGachaEngine(rules, poolID, units, cryptoGachaRand{})
	if err != nil {
		return gachaReject("招募暂未开放", "该招募的同伴名单尚未准备好。")
	}

	// 结算前先做一次**只读**余额预检，给玩家明确提示（真正防超扣
	// 由事务内的条件 UPDATE 保证，这里只是体验层短路）。
	if res, ok := gachaBalanceCheck(dep, req.UserID, engine, count); !ok {
		return res, true, nil
	}

	// 有界重放判定：同参指纹下最近一次 committed 是否应回放。
	digest := gachaRequestDigest(req.UserID, pool.PoolID, count,
		engine.primaryCurrency(), rules.Version)
	prev, err := dep.Gacha.GachaOperationByDigest(req.UserID, digest)
	if err != nil {
		return nil, true, fmt.Errorf("cn35 抽卡重放判定读取失败: %w", err)
	}
	if gachaShouldReplay(prev, st, arrived.UnixNano()) {
		body, rerr := gachaReplayBody(dep, c, req, prev)
		return body, false, rerr
	}

	results, endState, err := engine.drawMany(state, count)
	if err != nil {
		return gachaReject("招募失败", "招募过程中出现异常，请稍后再试。")
	}

	settlement, resultEntries, err := buildGachaSettlement(dep, req.UserID, pool, rules, group,
		engine, results, endState, count)
	if err != nil {
		return nil, true, err
	}
	rec, err := dep.Gacha.SettleGacha(*settlement)
	if err != nil {
		switch {
		case errors.Is(err, ErrGachaInsufficient):
			return gachaReject("余额不足", gachaInsufficientMessage(pool))
		case errors.Is(err, ErrGachaOperationConflict):
			// 并发同键：另一事务已提交，回放缓存结果（防御兜底，
			// 正常路径已由上方 digest 判定拦截）。
			if dup, derr := dep.Gacha.GachaOperationByDigest(req.UserID, digest); derr == nil && dup != nil {
				body, rerr := gachaReplayBody(dep, c, req, dup)
				return body, false, rerr
			}
			return gachaReject("招募失败", "请求与已记录的操作不符，请重新打开招募页面。")
		}
		return nil, true, fmt.Errorf("cn35 抽卡结算失败: %w", err)
	}

	// 回推结果与最新读面。
	data, err := inventoryDataFor(dep, c, req.UserID)
	if err != nil {
		return nil, true, err
	}
	snap, err := dep.Accounts.Snapshot(req.UserID)
	if err != nil {
		return nil, true, fmt.Errorf("cn35 抽卡后快照失败: %w", err)
	}
	numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
	data["user"] = snap.User
	data["user_coin"] = snap.UserCoin
	data["temp_gacha_result"] = resultEntries
	// temp_gacha_reward 留空：首期无额外奖励，客户端 GachaResult.cs
	// 在该键非空时弹结算 modal，空/缺键为正常路径。
	data["temp_gacha_reward"] = []any{}
	data["albums"] = nil
	c.Decoded["gacha_operation_id"] = rec.OperationID
	c.Decoded["gacha_count"] = count
	body, merr := cn35wire.MarshalJSON(map[string]any{
		"status": "success",
		"data":   data,
	})
	return body, true, merr
}

// gachaActiveConfig 读取当前已发布卡池配置；未发布返回 (nil, nil)。
// 这是执行面与文档读面共用的唯一入口——同一次请求锁定同一版本。
func gachaActiveConfig(dep Deps) (*ActiveGachaConfig, error) {
	if dep.GachaConfig == nil {
		return nil, nil
	}
	return dep.GachaConfig.ActiveGachaConfig()
}

// gachaPityVersionCompatible 判定已存保底行的版本标签是否兼容当前参数。
// 兼容 = 新口径指纹一致；或历史别名 "cn350-gacha-rules-v1" 且该组参数
// 与旧默认钻石池参数等价（旧标签是全局版本，参数未变则进度延续）。
func gachaPityVersionCompatible(stored, group string, params gachaPityRules) bool {
	want := PityRulesVersion(params)
	if stored == want {
		return true
	}
	if stored != gachaRulesVersion {
		return false
	}
	if group != gachaPityGroupDiamond {
		return false
	}
	var legacy gachaPityRules
	for _, p := range GachaSeedPools() {
		if p.Code == "diamond" {
			legacy = p.Pity
		}
	}
	return want == PityRulesVersion(legacy)
}

// gachaShouldReplay 判定同参请求是否应回放最近一次 committed 快照。
// st 已由调用方加锁。判定链：
//  1. 该次结算之后已发生过新尝试（含被拒）→ 本次也是新尝试；
//  2. 快照超出最大年龄 → 新抽；
//  3. 窗口内 → 传输层重发，回放；
//  4. 结算后出现过 gachas_execute 的 reload_data → 错误路径重试，回放。
func gachaShouldReplay(prev *GachaOperationRecord, st *gachaClientState, nowNano int64) bool {
	if prev == nil {
		return false
	}
	created, err := time.Parse(time.RFC3339Nano, prev.CreatedAt)
	if err != nil {
		return false
	}
	createdNano := created.UnixNano()
	if st.lastAttemptAt > createdNano {
		return false
	}
	age := nowNano - createdNano
	if age < 0 || age > gachaReplayMaxAgeSec*int64(time.Second) {
		return false
	}
	if age <= gachaReplayWindowSec*int64(time.Second) {
		return true
	}
	return st.lastReloadAt > createdNano
}

// gachaReplayBody 按已提交快照重建成功响应：temp_gacha_result 与首次
// 完全一致（客户端展示同一结果），读面（余额/库存）回推当前值。
func gachaReplayBody(dep Deps, c *httpapi.Ctx, req *cn35wire.GameRequest,
	prev *GachaOperationRecord) ([]byte, error) {
	results, entries, err := unmarshalGachaSnapshot(prev.ResultJSON)
	if err != nil {
		return nil, fmt.Errorf("cn35 抽卡快照重建失败: %w", err)
	}
	data, err := inventoryDataFor(dep, c, req.UserID)
	if err != nil {
		return nil, err
	}
	snap, err := dep.Accounts.Snapshot(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("cn35 抽卡回放快照失败: %w", err)
	}
	numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
	data["user"] = snap.User
	data["user_coin"] = snap.UserCoin
	data["temp_gacha_result"] = entries
	if entries == nil {
		// 旧格式快照（无 entry 字段）回放兜底：保持改动前的最小字段集。
		data["temp_gacha_result"] = gachaResultEntriesFallback(dep, results)
	}
	data["temp_gacha_reward"] = []any{}
	data["albums"] = nil
	c.Decoded["gacha_replay"] = true
	c.Decoded["gacha_operation_id"] = prev.OperationID
	return cn35wire.MarshalJSON(map[string]any{
		"status": "success",
		"data":   data,
	})
}

// gachaPoolsHandler 处理 /gachas：池目录读面。
//
// 为什么必须有这个端点：app_conf 的 Gacha 页声明
// requireData.gachas.path=/gachas，而 PageBase.cs:755-768 会对每个
// requireData 键调用 cache.DataLoad(key, path) 发一次 GET，并用
// requireDataLoaded 门控页面就绪（PageBase.cs:325）。缺该端点会让
// 抽卡页永远卡在等待数据。
//
// 同一份池表也随 preset_data 下发（preset_data.gachas），两条路径同源，
// 均出自 gachaPoolDocuments()。
func gachaPoolsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "gachas",
		}
		if dep.Gacha == nil || dep.GachaConfig == nil {
			// 未装配抽卡写面/配置面时不伪造池表：回空列表，客户端无池可显示，
			// 而不是显示一张点了没反应的池。
			return cn35wire.MarshalJSON(map[string]any{
				"status": "success",
				"data":   map[string]any{"gachas": []any{}},
			})
		}
		cfg, err := gachaActiveConfig(dep)
		if err != nil {
			return nil, err
		}
		data := map[string]any{
			"gachas":               []any{},
			"gacha_warnings":       []any{},
			"gacha_button_options": map[string]any{},
		}
		if cfg == nil {
			// 未发布任何版本：空池表（客户端无池可点，不会误扣费）。
			return cn35wire.MarshalJSON(map[string]any{
				"status": "success", "data": data,
			})
		}
		pools := gachaPoolDocuments(cfg)
		c.Decoded["gacha_pools"] = len(pools)
		c.Decoded["gacha_config_version"] = cfg.Version
		data["gachas"] = pools
		data["gacha_button_options"] = gachaButtonOptions(cfg)
		// 可获得名单：名单页 UnitListParts 走 cache.Get(data_key) 读数，
		// 数据必须随本响应预先入 cache（ListParts 不自行拉取）。
		avail, err := gachaAvailableUnitsDocs(dep, cfg)
		if err != nil {
			return nil, err
		}
		for k, v := range avail {
			data[k] = v
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

// gachaReloadDataHandler 处理 /users/reload_data?on=gachas_execute。
//
// 客户端在 ResponseFilter 判定失败（错误模态）时调用它刷新缓存
// （GachaIndexParts.cs:323-330）。回 data.{user,user_coin,user_units,gachas}
// 是客户端消费的键集合；gachas 供下次进入页面重算四槽映射。
func gachaReloadDataHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		on := firstNonEmpty(req.Query.Get("on"), req.Form.Get("on"))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "users_reload_data", "on": on,
		}
		if on == "gachas_execute" {
			// 客户端错误路径信号（GachaIndexParts.cs:160-164：成功走
			// ShowUnitGetAnimation 不发请求，失败才 ReloadData）。记录
			// 时间供 execute 重放判定：该次 committed 后出现 reload
			// 意味着玩家经错误 modal 重试 → 同参请求应回放快照。
			st := gachaStateFor(req.UserID)
			st.mu.Lock()
			st.lastReloadAt = time.Now().UnixNano()
			st.mu.Unlock()
		}
		data, err := inventoryDataFor(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 reload_data 快照失败: %w", err)
		}
		numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
		data["user"] = snap.User
		data["user_coin"] = snap.UserCoin
		// 池列表随刷新下发，保证槽位映射与文案同源；名单数据同源刷新。
		data["gachas"] = []any{}
		data["gacha_warnings"] = []any{}
		data["gacha_button_options"] = map[string]any{}
		cfg, cerr := gachaActiveConfig(dep)
		if cerr != nil {
			return nil, cerr
		}
		if dep.Gacha != nil && cfg != nil {
			data["gachas"] = gachaPoolDocuments(cfg)
			data["gacha_button_options"] = gachaButtonOptions(cfg)
			avail, aerr := gachaAvailableUnitsDocs(dep, cfg)
			if aerr != nil {
				return nil, aerr
			}
			for k, v := range avail {
				data[k] = v
			}
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   data,
		})
	})
}

// gachaPoolOpen 判断池在给定时刻是否可抽取。
// 常驻池恒开；限时池需 begin>0 且 begin<=now<=end。
// 未配置时间的限时池（首期槽1）视为**未开放**——客户端 IsValidTime
// 同样判为无效，两边口径一致。
func gachaPoolOpen(pool gachaPoolRules, now int64) error {
	if pool.Price.Point == 0 && pool.Price.Coin == 0 {
		return ErrGachaPoolClosed
	}
	if !pool.Limited {
		return nil
	}
	if pool.BeginAt <= 0 || pool.EndAt <= 0 {
		return ErrGachaPoolClosed
	}
	if now < pool.BeginAt || now > pool.EndAt {
		return ErrGachaPoolClosed
	}
	return nil
}

// gachaPoolUnits 已由 ROSTER350-03 移除：可中奖集合不再从 catalog 推导，
// 而是已发布版本快照内的成员名单（资源合格+启用由发布校验保证）。

// gachaResultEntryDoc 组装一条招募结果行（UnitListRow isScoutResult 契约，
// 见文件头注释）。字段与 user_units 行同口径（buildUnitDoc+applyUnitSkills），
// 另附招募页专有键：
//   - newcomer：New 角标。
//   - 重复卡 awake=true + before_state（觉醒前同构文档）：客户端并排
//     渲染 before→after（觉醒等级/等级上限/SP加成/符文孔）；满觉封顶时
//     delta=0，前后同值照实下发，不伪报增量。
//
// level 对重复卡取库存当前等级（抽卡不改等级）；dupBefore/dupAfter 为
// 本次操作内该卡的逐抽累计值（同一抽内多张同卡各算各的 before/after）。
// skills 是宿主单位已装备符文（读面同口径 skillsByUnitFor）；新卡传 nil。
func gachaResultEntryDoc(dep Deps, unitID, level, dupBefore, dupAfter int, newcomer bool, skills []PlayerSkill) (map[string]any, error) {
	if dep.Catalog == nil {
		// 未装配目录（-mem 桩）时回退最小字段集，不阻塞结算。
		return map[string]any{
			"id": unitID, "level": level,
			"duplicated_count": dupAfter, "newcomer": newcomer,
		}, nil
	}
	u, err := dep.Catalog.Unit(unitID)
	if err != nil || u == nil {
		return nil, fmt.Errorf("cn35 抽卡结果模板缺失 unit_id=%d: %w", unitID, err)
	}
	after, err := gachaUnitStatusDoc(u, unitID, level, dupAfter, newcomer, skills)
	if err != nil {
		return nil, err
	}
	if newcomer {
		after["awake"] = false
		return after, nil
	}
	after["awake"] = true
	before, err := gachaUnitStatusDoc(u, unitID, level, dupBefore, false, skills)
	if err != nil {
		return nil, err
	}
	after["before_state"] = before
	return after, nil
}

// gachaUnitStatusDoc 生成指定实例态（等级+觉醒数）下的完整单位行文档。
// 重复卡的 before_state 与 after 文档共用本函数，保证两栏字段完全同构。
func gachaUnitStatusDoc(u *cardcatalog.Unit, unitID, level, dup int, newcomer bool, skills []PlayerSkill) (map[string]any, error) {
	doc, err := buildUnitDoc(UnitRecord{
		UnitID: unitID, Level: level, DuplicatedCount: dup, Newcomer: newcomer,
	}, u)
	if err != nil {
		return nil, fmt.Errorf("cn35 抽卡结果行组装失败 unit_id=%d: %w", unitID, err)
	}
	// 符文孔位（skill_slot_count/rune_icon_name_*）按结算后觉醒数下发；
	// 重复卡沿用宿主已装备符文（与 user_units 读面同口径），新卡 skills=nil。
	applyUnitSkills(doc, u.Rare, dup, skills)
	return doc, nil
}

// gachaResultEntriesFallback 旧格式快照回放兜底：最小字段集
// （id/rare_type_id/level/max_level + 名称/图标），仅用于改动前提交的
// 历史操作被重放时。
func gachaResultEntriesFallback(dep Deps, results []gachaDrawResult) []any {
	out := make([]any, 0, len(results))
	for _, r := range results {
		id, err := strconv.Atoi(r.UnitID)
		if err != nil {
			continue
		}
		entry := map[string]any{
			"id":           id,
			"rare_type_id": r.RarityID,
			"level":        1,
			"max_level":    1,
		}
		if dep.Catalog != nil {
			if u, uerr := dep.Catalog.Unit(id); uerr == nil && u != nil {
				entry["name"] = u.Name
				entry["asset_name"] = fmt.Sprintf("unit/unit_%d", u.ID)
			}
		}
		out = append(out, entry)
	}
	return out
}

// gachaBalanceCheck 只读预检余额，不足时返回客户端可消费的错误响应。
// 真正的防超扣在事务内（条件 UPDATE + RowsAffected），这里仅优化体验。
func gachaBalanceCheck(dep Deps, userID string, engine *gachaEngine, count int) ([]byte, bool) {
	snap, err := dep.Accounts.Snapshot(userID)
	if err != nil {
		return nil, true // 读取失败交由事务路径报错，不在此处误判为余额不足。
	}
	cost := engine.totalCost(count)
	need := cost.Point
	if cost.Coin > 0 {
		need = cost.Coin
	}
	have := 0
	if cost.Coin > 0 {
		if v, ok := snap.UserCoin["amount"]; ok {
			have = toInt(v)
		}
	} else if v, ok := snap.User["gacha_point"]; ok {
		have = toInt(v)
	}
	if have >= need {
		return nil, true
	}
	body, merr := cn35wire.MarshalJSON(gachaError("余额不足", gachaInsufficientMessage(engine.pool)))
	if merr != nil {
		return nil, true // 编码失败不吞掉真实结算路径的错误语义。
	}
	return body, false
}

// gachaInsufficientMessage 生成与池币种一致的不足提示。
func gachaInsufficientMessage(pool gachaPoolRules) string {
	if pool.Price.Point > 0 {
		return "金币不足，无法进行本次招募。"
	}
	return "钻石不足，无法进行本次招募。"
}

// toInt 把快照中的数值（可能是 int/int64/float64/json.Number）转为 int。
func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

// gachaOperationKey 派生每次结算唯一的操作键（审计 id）。
//
// 语义已改版：键 = H(同参指纹, 结算 nonce)，nonce 每次随机——
// **同一玩家连续两次合法同参抽卡产生不同键**，不再被唯一约束误并。
// 重放定位不靠本键，靠 request_digest（见 gachaRequestDigest）+
// gachaShouldReplay 的窗口/reload 信号判定。
func gachaOperationKey(digest, nonce string) string {
	raw := strings.Join([]string{"v2", digest, nonce}, "\x1f")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// gachaNonce 生成结算 nonce（操作键的唯一量）。crypto/rand 不可用时
// 退化为纳秒时间戳——nonce 只要求同指纹下唯一，不要求不可预测。
func gachaNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// buildGachaSettlement 把引擎结果组装为存储层结算输入。
//
// 重复卡规则（设计 §5）：已有模板累加 duplicated_count，**不覆盖**等级、
// 保护状态、编队与魔宠绑定；超过觉醒上限时按星级补偿徽章并公示。
// 同一操作内同卡多次按顺序结算，正确区分首次 newcomer 与后续 duplicated_delta。
func buildGachaSettlement(
	dep Deps, userID string, pool gachaPoolRules, rules gachaRules, group string,
	engine *gachaEngine, results []gachaDrawResult, endState gachaPityState, count int,
) (*GachaSettlement, []any, error) {
	// 当前库存快照：判定"是否已有该模板"以及累加后的觉醒上限。
	owned := map[int]UnitRecord{}
	if dep.Inventory != nil {
		recs, err := dep.Inventory.Units(userID)
		if err != nil {
			return nil, nil, fmt.Errorf("cn35 抽卡读取库存失败: %w", err)
		}
		for _, r := range recs {
			owned[r.UnitID] = r
		}
	}

	// 本次操作内对各模板的累计增量（同卡多次必须逐次累加而不是各写各的）。
	// granted 记录本操作内已首次入库的模板，防止同卡第二次仍判 newcomer
	// 造成 (user_id,unit_id) 主键冲突。
	// 重复卡结果行沿用宿主已装备符文：同一 unit_id 的库存行与招募结果行
	// 符文图标/加成口径一致（skillsByUnitFor 读面同一来源）。
	skillsByUnit, skErr := skillsByUnitFor(dep, userID)
	if skErr != nil {
		return nil, nil, skErr
	}

	seen := map[int]int{}
	granted := map[int]bool{}
	draws := make([]GachaDraw, 0, len(results))
	entries := make([]any, 0, len(results))
	compensation := 0
	for i, r := range results {
		id, err := strconv.Atoi(r.UnitID)
		if err != nil {
			return nil, nil, fmt.Errorf("cn35 抽卡结果模板 id 非法 %q: %w", r.UnitID, err)
		}
		rec, has := owned[id]
		priorDelta := seen[id]
		newcomer := !has && !granted[id]

		// 觉醒上限取 min(规则面, statmodel 星级上限)：规则面 DuplicateMaxCount
		// 是全局值（首期 15），但 ★1 真上限为 5——按全局值放行会写入
		// dup>5 的非法实例态，读面 buildUnitDoc 即报错。
		dupCap := rules.DuplicateMaxCount
		if dep.Catalog != nil {
			if u, uerr := dep.Catalog.Unit(id); uerr == nil && u != nil {
				if mc := statmodel.MaxDuplicatedCount(u.Rare); mc < dupCap {
					dupCap = mc
				}
			}
		}

		delta := 0
		comp := 0
		if newcomer {
			granted[id] = true
		} else {
			// 已有卡：当前觉醒计数 = 库存值 + 本次已累加。
			current := rec.DuplicatedCount + priorDelta
			if current < dupCap {
				delta = 1
				seen[id] = priorDelta + 1
			} else {
				// 满觉醒：不吞卡，转为公示的徽章补偿。
				cr, ok := rules.DuplicateCompensation[r.RarityID]
				if !ok {
					return nil, nil, fmt.Errorf("cn35 抽卡缺少稀有度 %d 的重复补偿配置", r.RarityID)
				}
				comp = cr.Medal
				compensation += comp
			}
		}
		draws = append(draws, GachaDraw{
			UnitID: id, RarityID: r.RarityID, Reason: string(r.Reason),
			Newcomer: newcomer, DuplicatedDelta: delta,
			N4After: r.StateAfter.N4, N5After: r.StateAfter.N5,
			CompensatedPoint: comp,
		})
		// 结果行：dupBefore = 库存值 + 本操作内已累加；dupAfter = +delta
		//（满觉补偿时 delta=0，前后同值如实下发）。重复卡等级取库存
		// 当前值（抽卡不改等级），新卡恒 1。
		level := 1
		if has {
			level = rec.Level
		}
		var entrySkills []PlayerSkill
		if !newcomer {
			entrySkills = skillsByUnit[id]
		}
		entry, eerr := gachaResultEntryDoc(dep, id, level,
			rec.DuplicatedCount+priorDelta, rec.DuplicatedCount+priorDelta+delta,
			newcomer, entrySkills)
		if eerr != nil {
			return nil, nil, eerr
		}
		entries = append(entries, entry)
		_ = i
	}

	cost := engine.totalCost(count)
	currency := engine.primaryCurrency()
	unitCost := pool.Price.Point
	totalCost := cost.Point
	if currency == gachaCurrencyCoin {
		unitCost = pool.Price.Coin
		totalCost = cost.Coin
	}

	ledger := []GachaLedgerEntry{{
		Seq: 0, Currency: currency, Delta: -totalCost,
		Reason: "gacha_consume", BalanceAfter: 0,
	}}
	if compensation > 0 {
		ledger = append(ledger, GachaLedgerEntry{
			Seq: 1, Currency: gachaCurrencyMedal, Delta: compensation,
			Reason: "gacha_duplicate_compensation", BalanceAfter: 0,
		})
	}

	// 结果快照：审计字段 + 客户端结果行一并持久化，重放时原样返回
	//（结果行包含觉醒前快照，事后无法从库存重算——dup 可能已再变）。
	resultJSON, err := marshalGachaSnapshot(draws, entries)
	if err != nil {
		return nil, nil, err
	}

	digest := gachaRequestDigest(userID, pool.PoolID, count, currency, rules.Version)
	return &GachaSettlement{
		UserID:        userID,
		OperationKey:  gachaOperationKey(digest, gachaNonce()),
		RequestDigest: digest,
		PoolID:        pool.PoolID,
		PityGroup:     group,
		RulesVersion:  rules.Version,
		DrawCount:     count,
		Currency:      currency,
		UnitCost:      unitCost,
		TotalCost:     totalCost,
		ResultJSON:    resultJSON,
		Pity: GachaPity{
			N4: endState.N4, N5: endState.N5,
			FeaturedGuaranteed: endState.FeaturedGuaranteed,
			// 保底行版本标签 = 保底参数指纹（组级），与操作审计的
			// rules_version（发布版本标签 "pv<N>"）分开演进。
			RulesVersion: PityRulesVersion(rules.Pity[group]),
		},
		Draws:  draws,
		Ledger: ledger,
	}, entries, nil
}

// gachaRequestDigest 是同参指纹：账号+池+次数+币种+规则版本的哈希。
// 它替代旧操作键承担重放定位——同一请求的重复提交必然同指纹，
// 合法再抽也同指纹（这是不可区分性的事实面），由 gachaShouldReplay
// 的窗口/reload 信号在时间维度上切开两类语义。
func gachaRequestDigest(userID, poolID string, count int, currency, rulesVersion string) string {
	raw := strings.Join([]string{
		"digest", "v1", userID, poolID, strconv.Itoa(count), currency, rulesVersion,
	}, "\x1f")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// gachaSnapDraw 是 result_json 快照中一抽的持久形状（marshal/unmarshal 共用）。
// Entry 是客户端结果行原文（含 before_state），回放路径原样下发——
// 觉醒前快照事后无法从库存重算，必须随结算固化。
type gachaSnapDraw struct {
	UnitID          int            `json:"unit_id"`
	RarityID        int            `json:"rare_type_id"`
	Reason          string         `json:"reason"`
	Newcomer        bool           `json:"newcomer"`
	DuplicatedDelta int            `json:"duplicated_delta"`
	Compensated     int            `json:"compensated_point"`
	Entry           map[string]any `json:"entry,omitempty"`
}

// marshalGachaSnapshot 序列化结果快照。
func marshalGachaSnapshot(draws []GachaDraw, entries []any) (string, error) {
	snap := make([]gachaSnapDraw, 0, len(draws))
	for i, d := range draws {
		sd := gachaSnapDraw{
			UnitID: d.UnitID, RarityID: d.RarityID, Reason: d.Reason,
			Newcomer: d.Newcomer, DuplicatedDelta: d.DuplicatedDelta,
			Compensated: d.CompensatedPoint,
		}
		if i < len(entries) {
			if e, ok := entries[i].(map[string]any); ok {
				sd.Entry = e
			}
		}
		snap = append(snap, sd)
	}
	b, err := cn35wire.MarshalJSON(map[string]any{"draws": snap})
	if err != nil {
		return "", fmt.Errorf("cn35 抽卡结果快照序列化失败: %w", err)
	}
	return string(b), nil
}

// unmarshalGachaSnapshot 把 result_json 快照反解为回放输入：
// draws（审计形状）+ entries（客户端结果行原文）。entries 为 nil 表示
// 旧格式快照（无 entry 字段），调用方走兜底重建。
func unmarshalGachaSnapshot(resultJSON string) ([]gachaDrawResult, []any, error) {
	var doc struct {
		Draws []gachaSnapDraw `json:"draws"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &doc); err != nil {
		return nil, nil, fmt.Errorf("cn35 抽卡快照解析失败: %w", err)
	}
	out := make([]gachaDrawResult, 0, len(doc.Draws))
	entries := make([]any, 0, len(doc.Draws))
	hasEntries := true
	for _, d := range doc.Draws {
		out = append(out, gachaDrawResult{
			UnitID: strconv.Itoa(d.UnitID), RarityID: d.RarityID,
		})
		if d.Entry == nil {
			hasEntries = false
		}
		entries = append(entries, d.Entry)
	}
	if !hasEntries {
		entries = nil
	}
	return out, entries, nil
}

// gachaPoolDocuments 生成下发给客户端的 gachas 文档数组。
//
// 客户端 GachaIndexParts.cs:352-363 按 list_type_id 建立槽位映射
// （<4 → 槽号=list_type_id+1，否则落槽1），同槽重复会 Dictionary.Add
// 抛异常。因此**必须**由规则同源生成，且保持 list_type_id 唯一。
func gachaPoolDocuments(cfg *ActiveGachaConfig) []any {
	out := make([]any, 0, len(cfg.Rules.Pools))
	for _, p := range cfg.Rules.Pools {
		pid, err := strconv.Atoi(p.PoolID)
		if err != nil {
			return nil
		}
		doc := map[string]any{
			// id 必须下发数字：客户端单抽用 gacha.i("id")（int），连抽用
			// gacha.s("id")（string，number 自动转 str）。下发 string 时
			// 单抽读到 0（实机证据：execute?id=0&count=1）。
			"id":                   pid,
			"list_type_id":         p.ListTypeID,
			"name":                 p.Name,
			"description":          p.Description,
			"point":                p.Price.Point,
			"price":                p.Price.Coin,
			"max_executable_count": p.MaxExecutableCount,
			"max_execute_bonus":    p.MaxExecuteBonus,
			// ClickUnitList（GachaIndexParts.cs:332-334）：
			// ShowPage(PageMessage("name=GachaAvailableList&method=Stack"),
			//   gacha.doc("available_list_option"))——本 doc 整体作为
			// additionalOption 深并入 GachaAvailableList 页 option
			// （PageBase.cs:279-281 option.Merge）。因此 parts 段会变为
			// 页面真实部件：UnitList 经 data_key 读 cache 渲染名单。
			// 几何参数与 UserUnits 页 UnitList 实测配置一致（app_conf
			// serve_app_conf_android.json 页 UnitList，实机已证）。
			"available_list_option": map[string]any{
				"name":   "GachaAvailableList",
				"method": "Stack",
				"parts": map[string]any{
					"UnitList": map[string]any{
						"prefab":            "Common/PageParts/UnitListParts",
						"x":                 1,
						"y":                 33,
						"data_key":          gachaAvailableUnitsKey(p.PoolID),
						"row_prefab_name":   "Common/Piece/UserUnitListRow",
						"arrangement":       "horizon",
						"cell_width":        74,
						"clip_range_x":      0,
						"clip_range_y":      0,
						"clip_range_size_x": 460,
						"clip_range_size_y": 190,
					},
				},
			},
		}
		if p.Limited && p.BeginAt > 0 && p.EndAt > 0 {
			doc["began_at"] = p.BeginAt
			doc["ended_at"] = p.EndAt
		}
		out = append(out, doc)
	}
	return out
}

// GachaPoolDocumentsForTest 暴露池文档给同包外测试，锁死"展示=算法"同源。
// 生产路径仍只经 gachaPoolDocuments；本函数不改变行为。
func GachaPoolDocumentsForTest(cfg *ActiveGachaConfig) []map[string]any {
	out := make([]map[string]any, 0, len(cfg.Rules.Pools))
	for _, p := range cfg.Rules.Pools {
		out = append(out, map[string]any{
			"id":                   p.PoolID,
			"list_type_id":         p.ListTypeID,
			"name":                 p.Name,
			"point":                p.Price.Point,
			"price":                p.Price.Coin,
			"max_executable_count": p.MaxExecutableCount,
			"max_execute_bonus":    p.MaxExecuteBonus,
		})
	}
	return out
}

// gachaButtonOptions 生成 gacha_button_options：
// 客户端据此决定详情按钮是否可见（detail_onclick_option 存在即显示），
// 并把该 doc 交给 CreateMessageAndShowPage 按 method 动作动词分派
// （IPageComponentExtentions.cs CreateMessageAndShowPageCore 的 switch：
// ShowPage/ConfirmModal/ItemCountSelectModal/ShowModal/OpenUrl/ShowRule/
// CheckInstalled/RequestJson——**不是** PageMessage 的 Stack/Replace）。
// 详情按钮点击以原生 ShowRuleTipModal 展示本池规则与概率公示，文案与
// 概率/保底参数同源于 gachaDefaultRules（修改规则数值必须同步此处语义，
// 由测试锁定关键词）。
func gachaButtonOptions(cfg *ActiveGachaConfig) map[string]any {
	out := map[string]any{}
	for _, p := range cfg.Rules.Pools {
		out[p.PoolID] = map[string]any{
			"detail_onclick_option": map[string]any{
				"method":     "ShowRule",
				"title_text": p.Name + " 提供割合",
				"info_text":  gachaRuleDisclosure(p, cfg.Rules),
			},
		}
	}
	return out
}

// gachaAvailableUnitsKey 是某池名单数据在客户端 cache 中的键名。
// UnitListParts.LoadListRows 经 common.cache.Get(data_key).docList 读行
// （UnitListParts.cs:26-38），数据随 /gachas 与 reload_data 响应预置。
func gachaAvailableUnitsKey(poolID string) string {
	return "gacha_available_units_" + poolID
}

// gachaPoolReachableRarities 返回池可产出的稀有度集合——与引擎抽样同源：
// 保底池 = {UR} ∪ 该池保底参数的生效非五星档（effectiveNonURWeights，
// 缺省回退 {SR,HR}）；无保底池 = pool.Distribution 的正权重档。
func gachaPoolReachableRarities(p gachaPoolRules, rules gachaRules) map[int]bool {
	out := map[int]bool{}
	if g := rules.pityGroupFor(p); g != gachaPityGroupNone {
		out[gachaRarityUR] = true
		for r, w := range rules.Pity[g].effectiveNonURWeights() {
			if w > 0 {
				out[r] = true
			}
		}
		return out
	}
	for rarity, w := range p.Distribution {
		if w > 0 {
			out[rarity] = true
		}
	}
	return out
}

// gachaAvailableUnitsDocs 生成每池可获得名单数据（cache 键 → 单位行数组）。
//
// 名单集合与 gachaPoolUnits 同源（catalog 解析失败/未知稀有度不入列），
// 再按池可达稀有度过滤——名单与中奖集合严格一致（设计 §7：名单不得
// 出现池中抽不到或资源不合格的角色）。
//
// 行形态复用 buildUnitDoc（Lv1/dup0 模板态）：UserUnitListRow 渲染的
// level/max_level/hp/at/稀有度/属性/武器/立绘键全部同源。**不下发
// unit_detail_message**——该键驱动 UserUnitDetail 页按 user_units 反查
// （UserUnitDetail.cs:227），名单单位非持有、反查必空，置缺让行点击
// 无操作比下发必坏链安全（名单页验收目标为名单渲染与进出，非详情页）。
func gachaAvailableUnitsDocs(dep Deps, cfg *ActiveGachaConfig) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range cfg.Rules.Pools {
		key := gachaAvailableUnitsKey(p.PoolID)
		out[key] = []any{}
		if dep.Catalog == nil {
			continue
		}
		// 名单 = 已发布成员（发布时已校验资源合格+启用），
		// 再按池可达稀有度过滤——名单与中奖集合严格一致。
		// 例外（运营口径 2026-10-02）：钻石常驻池名单仅展示 5★——
		// 纯展示过滤，不影响中奖集合（3/4★ 仍可被抽中）。
		units := cfg.MembersByPool[p.PoolID]
		reach := gachaPoolReachableRarities(p, cfg.Rules)
		if cfg.Rules.pityGroupFor(p) == gachaPityGroupDiamond {
			reach = map[int]bool{gachaRarityUR: true}
		}
		docs := []any{}
		for _, gu := range units {
			if !reach[gu.RarityID] {
				continue
			}
			id, err := strconv.Atoi(gu.UnitID)
			if err != nil {
				continue
			}
			u, err := dep.Catalog.Unit(id)
			if err != nil || u == nil {
				continue
			}
			d, err := buildUnitDoc(UnitRecord{UnitID: id, Level: 1}, u)
			if err != nil {
				return nil, fmt.Errorf("cn35 名单单位 %d 组装失败: %w", id, err)
			}
			delete(d, "unit_detail_message")
			docs = append(docs, d)
		}
		out[key] = docs
	}
	return out, nil
}

// gachaRuleDisclosure 生成 detail 按钮 ShowRuleTipModal 的公示文案。
// 概率/保底数值全部从规则结构取值——不得出现与引擎不一致的手写数字。
func gachaRuleDisclosure(p gachaPoolRules, rules gachaRules) string {
	var b strings.Builder
	fmt.Fprintf(&b, "【%s】", p.Name)
	if p.Price.Coin > 0 {
		fmt.Fprintf(&b, "\n价格：单抽 %d 钻石，%d 连 %d 钻石。",
			p.Price.Coin, p.MaxExecutableCount, p.Price.Coin*p.MaxExecutableCount)
	} else {
		fmt.Fprintf(&b, "\n价格：单抽 %d 金币，%d 连 %d 金币。",
			p.Price.Point, p.MaxExecutableCount, p.Price.Point*p.MaxExecutableCount)
	}
	if p.Limited && (p.BeginAt <= 0 || p.EndAt <= 0) {
		b.WriteString("\n本期暂未开放，开放时间以后续公告为准。")
	}
	group := rules.pityGroupFor(p)
	if group == gachaPityGroupNone {
		dist := p.Distribution
		total := 0
		for _, w := range dist {
			total += w
		}
		b.WriteString("\n提供割合：")
		first := true
		for _, r := range []int{gachaRarityN, gachaRarityR, gachaRarityHR, gachaRaritySR, gachaRarityUR} {
			w := dist[r]
			if w <= 0 {
				continue
			}
			if !first {
				b.WriteString("、")
			}
			first = false
			fmt.Fprintf(&b, "%s %d%%", gachaRarityName(r), w*100/max(total, 1))
		}
		b.WriteString("。\n本池无保底，概率固定。")
	} else {
		pr := rules.Pity[group]
		// 非五星档按生效权重表逐档列名（缺省回退 {SR,HR} 默认 18:80）——
		// 文案与权重表同源，自定义档位不会退化成"4星与3星"的空文案。
		nonUR := pr.effectiveNonURWeights()
		rs := make([]int, 0, len(nonUR))
		for r, w := range nonUR {
			if w > 0 {
				rs = append(rs, r)
			}
		}
		sort.Sort(sort.Reverse(sort.IntSlice(rs)))
		names := make([]string, 0, len(rs))
		weights := make([]string, 0, len(rs))
		for _, r := range rs {
			names = append(names, gachaRarityName(r))
			weights = append(weights, strconv.Itoa(nonUR[r]))
		}
		fmt.Fprintf(&b, "\n提供割合：5星基础率 %d%%；未中5星时%s按 %s 分配。",
			pr.URBasePercent, strings.Join(names, "与"), strings.Join(weights, ":"))
		fmt.Fprintf(&b, "\n保底：连续未出5星第 %d 次起每抽 +%d%%，第 %d 次必得5星；",
			pr.URSoftStart+1, pr.URSoftStepPercent, pr.URHardPity)
		fmt.Fprintf(&b, "连续 %d 次未得4星及以上时下一次必得4星。", pr.SRPity)
		if pr.UPGuarantee && len(p.FeaturedURIDs) > 0 {
			b.WriteString("\nUP规则：五星 50% 为本期UP角色；未中UP时下一次五星必为UP。")
		}
	}
	b.WriteString("\n※本池为服务端重建规则，非原服数值。")
	return b.String()
}

// gachaRarityName 公示用稀有度中文名（rare_type_id = 星级×10）。
func gachaRarityName(rarity int) string {
	switch rarity {
	case gachaRarityN:
		return "1星"
	case gachaRarityR:
		return "2星"
	case gachaRarityHR:
		return "3星"
	case gachaRaritySR:
		return "4星"
	case gachaRarityUR:
		return "5星"
	}
	return fmt.Sprintf("稀有度%d", rarity)
}

// gachaError 构造客户端 ResponseFilter 能识别的错误响应。
// 与 trainingError 同形：HTTP 200 + {"status":"error","modal":{...}}。
func gachaError(title, body string) map[string]any {
	return map[string]any{
		"status": "error",
		"modal": map[string]any{
			"type":  "ok",
			"title": title,
			"body":  body,
		},
	}
}
