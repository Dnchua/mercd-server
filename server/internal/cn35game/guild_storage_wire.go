// guild_storage_wire.go BP 蓄水库与六类设施体系读/写面（GUILDCLOSE350 GC-03/GC-04）。
//
// 本文件属于 T3（guild-storage），承载三块：
//
//  1. 蓄水库领域模型 + 设施成长曲线（纯函数，可单测、可被 store 复用）；
//  2. /guilds/bp_storage（确认读面）与 /guilds/takeover_bp_storage（取出事务）
//     两个 handler 及其路由注册；
//  3. equipments 六类设施的 wire 文档组装（GuildEquipmentsDoc），供 T1 在
//     guild_wire.go 的 guildDoc/guildTempDoc 里一次调用，避免两套口径。
//
// 全部数值口径与证据分层见
// artifacts/tasks/cn350-guild-live-closure-20261028/run/evidence-guild-storage.md。
// 一句话口径：能证的是「形状 + 单点锚 + 门禁方向」，曲线与容量是〔推断〕重建规则。
package cn35game

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// ---- 错误（本文件新增，不改 guild.go 既有错误语义） ----

var (
	// ErrGuildBPStorageEmpty 蓄水库「无处可取」：请求方 BP 已满（bp>=max_bp）
	// 或蓄水库余额为 0。原服映射为 title=确认 的 OK 弹窗（无 type/url），
	// 不是错误响应；store 层用该错误表达「本次不可取」。
	ErrGuildBPStorageEmpty = errors.New("cn35game: 蓄水库无可取出BP")
	// ErrGuildBPStorageBusy 事务内条件 UPDATE 未命中（并发改了 BP 或余额）。
	ErrGuildBPStorageBusy = errors.New("cn35game: 蓄水库状态已变化，请重试")
	// ErrGuildStorageUnavailable 未装配蓄水库写面（dep.Guild 空或未实现扩展接口）。
	ErrGuildStorageUnavailable = errors.New("cn35game: 公会蓄水库未启用")
)

// ---- 设施成长曲线（GC-04，重建规则） ----
//
// 硬证据（同一份 guild 快照 decrypted/1/guilds.json）：
//
//	hp(Lv.79) until=29,928,300 与 bp_storage(Lv.79) until=29,928,300 同值
//	→ 等级阈值**只与等级有关、与设施类型无关**。
//	另有 sp(Lv.88)→70,547,000、at(Lv.84)→48,190,100、taghness(Lv.70)→12,700,000。
//	四点按 ×1.1 拟合残差 ≤0.03%（见 evidence §3）。
//
// 实现不是「原服公式」，而是**过锚点的分段几何插值**：
//   - 锚点处精确等于原服观测值（消除拟合误差）；
//   - 锚点之间按几何（对数线性）插值，保证单调；
//   - 最后锚点之上按末段比率外推，到 GuildEquipMaxLevel 封顶；
//   - 第一个锚点之下（1..71）按拟合比率 1.1 向下外推——该段**没有任何观测**，
//     属纯外推，标〔推断〕。
const (
	// GuildEquipMaxLevel 设施等级上限〔推断〕：观测最高 88 级（sp），
	// 取整百 100 作为重建上限；≥89 的取值不影响任何已观测数据。
	GuildEquipMaxLevel = 100
	// guildEquipRoundUnit 阈值/容量按百位取整（原服 29,928,300 / 70,547,000
	// 等锚点明显按有效位数取整，故重建曲线沿用百位取整）。
	guildEquipRoundUnit = 100
	// guildEquipLowRatio 1..71 段向下外推比率（拟合值 1.1）。
	guildEquipLowRatio = 1.1
)

type guildEquipAnchor struct {
	Level int
	Point int64
}

// guildEquipThresholdAnchors 原服观测锚点（累计 point 达到该级所需值）。
// 第一个锚点为「1 级 = 0 点」，其余四个全部来自录包 until 字段。
var guildEquipThresholdAnchors = []guildEquipAnchor{
	{Level: 1, Point: 0},
	{Level: 71, Point: 12700000},
	{Level: 80, Point: 29928300},
	{Level: 85, Point: 48190100},
	{Level: 89, Point: 70547000},
}

func guildEquipRound100(v float64) int64 {
	if v <= 0 {
		return 0
	}
	return int64(math.Round(v/guildEquipRoundUnit)) * guildEquipRoundUnit
}

func guildEquipSegmentRatio(a, b guildEquipAnchor) float64 {
	levels := float64(b.Level - a.Level)
	if levels <= 0 || a.Point <= 0 {
		return 1
	}
	return math.Pow(float64(b.Point)/float64(a.Point), 1/levels)
}

// GuildEquipThreshold 达到 level 级所需的累计 point（T(1)=0）。
//
// 单调递增、过四个原服锚点、在 GuildEquipMaxLevel 封顶。
func GuildEquipThreshold(level int) int64 {
	if level <= 1 {
		return 0
	}
	if level > GuildEquipMaxLevel {
		level = GuildEquipMaxLevel
	}
	anchors := guildEquipThresholdAnchors
	last := anchors[len(anchors)-1]
	// 锚点先精确命中（含首锚点之上的每个观测级），再做分段插值/外推。
	for _, a := range anchors {
		if a.Level == level {
			return a.Point
		}
	}
	if level > last.Level {
		r := guildEquipSegmentRatio(anchors[len(anchors)-2], last)
		return guildEquipRound100(float64(last.Point) * math.Pow(r, float64(level-last.Level)))
	}
	if level < anchors[1].Level {
		// 1..71 段：向下外推（无观测，〔推断〕）。
		return guildEquipRound100(float64(anchors[1].Point) /
			math.Pow(guildEquipLowRatio, float64(anchors[1].Level-level)))
	}
	for i := 1; i+1 < len(anchors); i++ {
		a, b := anchors[i], anchors[i+1]
		if level > a.Level && level < b.Level {
			r := guildEquipSegmentRatio(a, b)
			return guildEquipRound100(float64(a.Point) * math.Pow(r, float64(level-a.Level)))
		}
	}
	return last.Point
}

// GuildEquipLevel 由累计 point 反解等级（阈值表反函数；1..GuildEquipMaxLevel）。
func GuildEquipLevel(point int64) int {
	if point <= 0 {
		return 1
	}
	lo, hi := 1, GuildEquipMaxLevel
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if GuildEquipThreshold(mid) <= point {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// GuildEquipUntilPoint 下一级所需累计 point；已满级为 0（客户端 end=true 时
// 不读 until，原服样本满级无观测）。
func GuildEquipUntilPoint(level int) int64 {
	if level >= GuildEquipMaxLevel {
		return 0
	}
	if level < 1 {
		level = 1
	}
	return GuildEquipThreshold(level + 1)
}

// ---- 六类设施效果值（GC-04，逐条锚点见 evidence §4） ----
//
// 锚点回代（全部精确命中，无拟合）：
//
//	sp         value = 25×L   → L88 = 2200       （原服 effect 初始SP值+2200）
//	hp/at      value = 0.25×L → L79 = 19.75、L84 = 21
//	taghness   value = 400×L  → L70 = 28000；value2 = Guts = min(5, L/20) → L70 = 3
//	bp_storage value = capacity(L) = round100(620×L) → L79 = 49000
const (
	guildSPPerLevel        = 25.0
	guildHpAtPercentPerLvl = 0.25
	guildTaghnessHPPerLvl  = 400.0
	guildTaghnessGutsEvery = 20
	guildTaghnessGutsMax   = 5
	guildBPStoragePerLevel = 620.0
)

// GuildBPStorageCapacity 蓄水库上限〔推断〕：round100(620×level)。
// 唯一锚点 Lv.79 → 620×79 = 48,980 → 百位取整 49,000（原服 wire 与 modal 同值）。
// 备选模型（620.25×level，Lv.79 → 49,000 亦成立）在百位精度下与主模型几乎
// 不可区分；区分向量见 evidence §4「容量」。
func GuildBPStorageCapacity(level int) int64 {
	if level < 1 {
		level = 1
	}
	if level > GuildEquipMaxLevel {
		level = GuildEquipMaxLevel
	}
	return guildEquipRound100(guildBPStoragePerLevel * float64(level))
}

// GuildEquipValue 一类设施在当前等级的效果主值/次值。
// bp_storage 的 value2（填充比例）依赖公会余额，由 store 侧填充，这里返回 0。
func GuildEquipValue(typeID string, level int) (value, value2 float64, hasValue2 bool) {
	if level < 1 {
		level = 1
	}
	switch typeID {
	case "sp":
		return guildSPPerLevel * float64(level), 0, false
	case "hp", "at":
		return guildHpAtPercentPerLvl * float64(level), 0, false
	case "taghness":
		guts := level / guildTaghnessGutsEvery
		if guts > guildTaghnessGutsMax {
			guts = guildTaghnessGutsMax
		}
		return guildTaghnessHPPerLvl * float64(level), float64(guts), true
	case "bp_storage":
		return float64(GuildBPStorageCapacity(level)), 0, true
	}
	return 0, 0, false
}

// GuildEquipIsInvestable 该类型是否可投资（原服 temporary 为全 null 占位）。
func GuildEquipIsInvestable(typeID string) bool {
	t, ok := GuildEquipmentTypes[typeID]
	return ok && t.Investable
}

// ---- 蓄水库领域类型 ----

// GuildBPStorage 公会蓄水库状态（共享池，一行一公会）。
type GuildBPStorage struct {
	GuildID  string
	Amount   int64   // 当前蓄水余额（BP）
	Capacity int64   // 当前上限（由 bp_storage 设施等级推导）
	Level    int     // 蓄水库设施等级 1..GuildEquipMaxLevel
	Ratio    float64 // Amount/Capacity，按 3 位小数（原服 value2=0.656 口径）
}

// CanTake 请求方当前是否「可取」：自身 BP 未满且蓄水库非空。
// 原服拒绝态出现在 16,761/49,000，蓄水库远未空，故门禁必然含请求方 BP 状态；
// 多个拒绝原因（bp 满 / 余额 0）在原服是同一句文案，优先级不可区分，
// 统一按「不可取」处理（证据 §2「拒绝原因优先级」）。
func (s GuildBPStorage) CanTake(bp, maxBP int64) bool {
	return maxBP > bp && s.Amount > 0
}

// GuildBPStorageTakeover 取出回执。
type GuildBPStorageTakeover struct {
	Amount  int64 // 本次实际取出量（重放时为原量）
	Storage *GuildBPStorage
	Replay  bool // 命中幂等键，未产生新的余额变动
}

// GuildStorageStore 蓄水库读写面（cn35store.Store 实现）。
//
// 刻意不并入共享 GuildStore 接口（guild.go 由 T1 维护）：handler 侧用类型断言
// 探测，未装配时回明确弹窗而不是 500。
type GuildStorageStore interface {
	// BPStorageStatus 读确认态数据；canTake 由 store 在同一事务内按
	// bp/max_bp 与余额判定，wire 只据此选确认/拒绝文案。
	BPStorageStatus(userID string) (st *GuildBPStorage, canTake bool, err error)
	// TakeoverBPStorage 取出事务：幂等键 + BP 与蓄水库余额条件 UPDATE 同事务。
	TakeoverBPStorage(userID, opKey string) (*GuildBPStorageTakeover, error)
	// DepositBPStorage 存入原语（重建）：容量封顶后**溢出部分丢弃**，
	// 返回存入后的状态与实际存入量。原始服务器的补给来源未证，
	// 当前不接入任何发放链（见 evidence §5 取证缺口）。
	DepositBPStorage(guildID, userID string, amount int64, reason, opKey string) (
		st *GuildBPStorage, deposited int64, err error)
}

// guildStorageStore 取蓄水库写面；未装配返回 ErrGuildStorageUnavailable。
func guildStorageStore(dep Deps) (GuildStorageStore, error) {
	if dep.Guild == nil {
		return nil, ErrGuildStorageUnavailable
	}
	st, ok := dep.Guild.(GuildStorageStore)
	if !ok || st == nil {
		return nil, ErrGuildStorageUnavailable
	}
	return st, nil
}

// ---- 文案与格式（逐字对齐原服样本） ----

// guildComma 千分位整数（原服 confirm body 用 "47,345/49,000"）。
func guildComma(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// guildBPStorageBody 确认/拒绝态共用 body（原服逐字）：
//
//	蓄水库 (Lv.79)\n47,345/49,000\n\n要取出BP吗？
//	蓄水库 (Lv.79)\n16,761/49,000\n\n现在，无法从蓄水库中取出BP。
func guildBPStorageBody(st *GuildBPStorage, canTake bool) string {
	tail := "要取出BP吗？"
	if !canTake {
		tail = "现在，无法从蓄水库中取出BP。"
	}
	return fmt.Sprintf("蓄水库 (Lv.%d)\n%s/%s\n\n%s",
		st.Level, guildComma(st.Amount), guildComma(st.Capacity), tail)
}

// ---- handlers ----

// guildBPStorageHandler GET/POST /guilds/bp_storage：确认读面（无任何写副作用）。
func guildBPStorageHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		st, err := guildStorageStore(dep)
		if err != nil {
			return cn35wire.MarshalJSON(guildModal("无法取出", "公会蓄水库未启用"))
		}
		rec, canTake, err := st.BPStorageStatus(req.UserID)
		if err != nil {
			return cn35wire.MarshalJSON(guildStorageErrModal(err))
		}
		// 确认态带 type=yes_no + url（客户端 Modal.ShowByDocument 走 yes_no 分支，
		// 点「是」POST 下发 url）；拒绝态**不得**带 type/url——原服样本只有
		// title+body，客户端走 default 分支渲染 OK 弹窗（Common.cs:2100+）。
		modal := map[string]any{"title": "确认", "body": guildBPStorageBody(rec, canTake)}
		if canTake {
			modal["type"] = "yes_no"
			modal["url"] = "/guilds/takeover_bp_storage"
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_bp_storage",
			"level": rec.Level, "amount": rec.Amount, "capacity": rec.Capacity,
			"can_take": canTake,
		}
		return cn35wire.MarshalJSON(map[string]any{"modal": modal})
	})
}

// guildTakeoverBPStorageHandler GET/POST /guilds/takeover_bp_storage：取出事务。
//
// 回包形状与原服一致：{"data":{"user":<完整 user 快照>},"modal":{"title":"完成",
// "body":"从蓄水库中取出了600BP。"}}；modal 无 type → 客户端 OK 弹窗。
//
// **无论取出成功还是被拒都下发 data.user**：原服该端点回包顶层恒有 data/user，
// 客户端用它刷新顶栏与公会页（bp/guild_asset_count 等）；只在成功时给 user
// 会让拒绝态回包形状与原服不一致（契约 type:/data/user 也会缺）。
// user 一律走 dep.Accounts.Snapshot（preset_data 同一条组装链），不手写残缺对象。
func guildTakeoverBPStorageHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		st, err := guildStorageStore(dep)
		if err != nil {
			return cn35wire.MarshalJSON(guildModal("无法取出", "公会蓄水库未启用"))
		}
		res, err := st.TakeoverBPStorage(req.UserID, req.Query.Get("op_key"))
		// 先取快照（成功/拒绝都要下发），再决定 modal 形态。
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		data := map[string]any{"user": snap.User}
		if err != nil {
			// 无可取/状态已变等业务拒绝：原服拒绝态是 OK 弹窗（无 type/url），
			// 但 data.user 仍在。
			collateral := map[string]any{
				"cn35": true, "uid": req.UserID,
				"endpoint": "guilds_takeover_bp_storage",
				"amount":   0, "refused": err.Error(),
			}
			c.Decoded = collateral
			return cn35wire.MarshalJSON(map[string]any{
				"data": data, "modal": guildStorageErrModal(err)["modal"],
			})
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_takeover_bp_storage",
			"amount": res.Amount, "replay": res.Replay,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": data,
			"modal": map[string]any{
				"title": "完成",
				"body":  fmt.Sprintf("从蓄水库中取出了%sBP。", guildComma(res.Amount)),
			},
		})
	})
}

// guildStorageErrModal 蓄水库错误 → 200+modal（客户端不裸 4xx）。
func guildStorageErrModal(err error) map[string]any {
	switch {
	case errors.Is(err, ErrGuildBPStorageEmpty):
		return guildModal("无法取出", "现在，无法从蓄水库中取出BP。")
	case errors.Is(err, ErrGuildBPStorageBusy):
		return guildModal("无法取出", "蓄水库状态已变化，请重新打开页面再试")
	case errors.Is(err, ErrGuildStorageUnavailable):
		return guildModal("无法取出", "公会蓄水库未启用")
	case errors.Is(err, ErrGuildNoMember):
		return guildModal("无法操作", "你未加入公会")
	case errors.Is(err, ErrGuildInvest):
		return guildModal("无法投资", "投资额非法")
	case errors.Is(err, ErrGuildEquipMax):
		return guildModal("无法投资", "设施已满级")
	case errors.Is(err, ErrGuildAsset):
		return guildModal("无法投资", "公会资材不足")
	case errors.Is(err, ErrGuildOp):
		return guildModal("无法操作", "操作重复或参数不一致，请重新打开页面再试")
	default:
		return guildModal("无法操作", err.Error())
	}
}

// ---- equipments 六类设施 wire 文档（T1 guild_wire.go 调用） ----

// GuildEquipmentsDoc 六类设施文档（原服键序，GuildEquipmentTypeIDs）。
//
// view.Equipments 由 store 填好 Level/Point/UntilPoint/Value/Value2/HasValue2/End；
// 本函数只做投影：store 没填的字段按重建曲线补安全默认值（不重算已填好的值，
// 避免 wire 与 store 两套口径）。原服键序：
// temporary, sp, hp, at, taghness, bp_storage。
func GuildEquipmentsDoc(view *GuildView) map[string]any {
	out := make(map[string]any, len(GuildEquipmentTypeIDs))
	for _, tid := range GuildEquipmentTypeIDs {
		var e *GuildEquipment
		if view != nil {
			e = view.Equipments[tid]
		}
		out[tid] = GuildEquipmentDoc(tid, e)
	}
	return out
}

// GuildEquipmentDoc 单类设施文档：
//
//	{name,description,effect,asset_name,point,current:{level,until,value,value2,end}}
//
// temporary 为全 null 占位（level/until/value/value2 均为 null，end=false，
// point=0，effect=""）——原服样本逐字如此；客户端该行走 GuildEquipmentListRows
// 的 temporary 分支（不读 description/effect）。
func GuildEquipmentDoc(typeID string, e *GuildEquipment) map[string]any {
	t := GuildEquipmentTypes[typeID]
	if typeID == "temporary" {
		return map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"effect":      "",
			"asset_name":  GuildEquipmentAssetName(typeID, 0),
			"point":       int64(0),
			"current": map[string]any{
				"level": nil, "until": nil, "value": nil, "value2": nil, "end": false,
			},
		}
	}
	level, point := 1, int64(0)
	until := int64(0)
	end := false
	value, value2, hasValue2 := 0.0, 0.0, false
	if e != nil {
		level, point = e.Level, e.Point
		if level < 1 {
			level = 1
		}
		until, end = e.UntilPoint, e.End
		value, value2, hasValue2 = e.Value, e.Value2, e.HasValue2
	}
	if !hasValue2 {
		// store 未填（或该类型本就不下发 value2）：按重建曲线补算。
		value, value2, hasValue2 = GuildEquipValue(typeID, level)
	}
	if until <= 0 {
		until = GuildEquipUntilPoint(level)
	}
	if !end {
		end = level >= GuildEquipMaxLevel
	}
	current := map[string]any{
		"level": level,
		"until": until,
		"value": value,
		"end":   end,
	}
	if hasValue2 {
		current["value2"] = value2
	} else {
		// 原服 sp/hp/at 的 value2 为 null（键在、值为 null）。
		current["value2"] = nil
	}
	return map[string]any{
		"name":        t.Name,
		"description": t.Description,
		"effect":      GuildEquipEffect(typeID, level, value, value2),
		"asset_name":  GuildEquipmentAssetName(typeID, level),
		"point":       point,
		"current":     current,
	}
}

// GuildEquipEffect 设施效果文案（原服逐字格式）：
//
//	sp         初始SP值+2200
//	hp         体力+19.75%
//	at         攻击力+21%
//	taghness   体力 +28000 Guts 3
//	bp_storage 蓄水上限 49000 BP\n蓄水库 65.60%
//
// 数值一律走自然格式（不加千分位、整数值不带小数），仅蓄水比例固定 2 位小数
// ——这是原服样本里两种不同的格式，不要统一。
func GuildEquipEffect(typeID string, level int, value, value2 float64) string {
	switch typeID {
	case "sp":
		return "初始SP值+" + strconv.FormatFloat(value, 'f', -1, 64)
	case "hp":
		return "体力+" + strconv.FormatFloat(value, 'f', -1, 64) + "%"
	case "at":
		return "攻击力+" + strconv.FormatFloat(value, 'f', -1, 64) + "%"
	case "taghness":
		return "体力 +" + strconv.FormatFloat(value, 'f', -1, 64) +
			" Guts " + strconv.FormatFloat(value2, 'f', -1, 64)
	case "bp_storage":
		return "蓄水上限 " + strconv.FormatFloat(value, 'f', -1, 64) + " BP\n蓄水库 " +
			strconv.FormatFloat(value2*100, 'f', 2, 64) + "%"
	}
	return ""
}

// registerGuildStorageRoutes 蓄水库路由（GC-03）。
//
// 两条都是原服精确 POST 路径；reg 包装对 GET 自动补 POST 双注册，
// 与既有读面约定一致（客户端 requireData 以 POST 加密体访问读面）。
func registerGuildStorageRoutes(reg guildRegister, dep Deps) {
	reg(http.MethodGet, "/guilds/bp_storage", "蓄水库确认读面（GC-03）", guildBPStorageHandler(dep))
	reg(http.MethodGet, "/guilds/takeover_bp_storage", "蓄水库取出（GC-03）", guildTakeoverBPStorageHandler(dep))
}
