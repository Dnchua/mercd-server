// battle.go STORY350-07：正式剧情战斗生命周期的领域模型与写口契约。
//
// 链路（06 契约，消费点全部在 ilspy-cs 反编译树）：
//
//	execute 建会话(prepared) → ap_use 扣 AP(ap_paid) → result 结算(cleared)
//	  / lose(lost) → continue 扣钻续战(continued) → 再 result/lose
//	滞留会话(prepared/ap_paid 超时 或同关同难度被新 execute 顶替) → abandoned
//
// 事务/幂等设计：
//   - 三个下发 URL 内嵌 execute 签发的不可猜 battle_id（bid），客户端原样
//     拼接 time/mc 等参数回打；bid 绑定 user_id，跨账号一律 ErrBattleNotFound。
//   - AP 扣点在 ap_use 内做条件 UPDATE（ap>=cost）+ 会话 prepared→ap_paid
//     同事务；会话已离开 prepared 态的重放一律成功返回（不再扣点）。
//   - result 结算单事务：会话状态迁移（条件 UPDATE 抢首次）→ clear_count+1
//     → 进度指针推进 → 奖励入账（余额/魔宠掉落）→ exp/level → 台账字段回填。
//     已终态会话（cleared/lost/abandoned）重放一律 already_received。
//   - continue 单事务：取最近 lost+continuable 会话 → coin 条件扣 1 →
//     continued + continue_count+1；同会话可重复续战（客户端每点一次付一钻）。
//   - lose 幂等覆盖：ap_paid|continued→lost 记录战报；escaped 未置位
//     视为客户端 Escape() 主动放弃 → 本会话不再可续（Lose() 路径才带
//     escaped=true，字段方向存疑见 evidence/failure-modes.md，实现按
//     "非 escaped 的 lose 不可续"处理）。
package cn35game

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

// 战斗会话状态机取值（与 0007 CHECK 白名单一致）。
const (
	BattlePrepared  = "prepared"
	BattleAPPaid    = "ap_paid"
	BattleCleared   = "cleared"
	BattleLost      = "lost"
	BattleContinued = "continued"
	BattleAbandoned = "abandoned"
)

// 战斗域哨兵错误。handlers 把业务失败映射为非 success 状态/modal；
// ErrBattleNotFound 覆盖"bid 未知/非本账号"（伪造与跨账号同形拒绝）。
var (
	ErrBattleNotFound    = errors.New("cn35game: 战斗会话不存在或归属其他账号")
	ErrBattleState       = errors.New("cn35game: 战斗会话状态不允许该操作")
	ErrBattleAP          = errors.New("cn35game: AP 不足")
	ErrBattleCoin        = errors.New("cn35game: 钻石不足")
	ErrNoContinuable     = errors.New("cn35game: 无可续战会话")
	ErrBattleQuest       = errors.New("cn35game: 关卡不存在或未解锁")
	ErrBattleDifficulty  = errors.New("cn35game: 难度非法")
	ErrBattleParty       = errors.New("cn35game: 编队含未持有单位")
	ErrBattleUnsupported = errors.New("cn35game: 非剧情模式请求")
)

// BattleReward 结算入账单元（execute 时从 appear_table 聚合进快照）。
// Key 取值与 AppearTableDocument.SetReward 的消费枚举一致：
// gacha_point/guild_asset（count 累计）、seed（id=魔宠模板，count 累计）、
// skill/item/element/power/other（本实现只回显不落账，见 07 report）。
type BattleReward struct {
	Key   string `json:"key"`
	ID    int    `json:"id,omitempty"`
	Count int    `json:"count"`
	// ItemKey item 奖励开战时固化的物品键（按发布版本绑定）；空表示旧
	// 会话快照，结算退回 content_id 实时解析。
	ItemKey string `json:"item_key,omitempty"`
	// Level 符文（key=skill）开战冻结的魔力品质值；结算 grantSkillTx 用。
	Level float64 `json:"level,omitempty"`
}

// BattleSession 一次 execute 签发的战斗会话（cn35_battle_sessions 行投影）。
type BattleSession struct {
	BattleID    string         `json:"battle_id"`
	UserID      string         `json:"user_id"`
	QuestID     int            `json:"quest_id"`
	AreaID      int            `json:"area_id"`
	Difficulty  string         `json:"difficulty"`
	PartyID     string         `json:"party_id"`
	Status      string         `json:"status"`
	APCost      int            `json:"ap_cost"`
	Rewards     []BattleReward `json:"rewards"`
	Exp         int            `json:"exp"`
	NextStageID int            `json:"next_stage_id"`
	NextAreaID  int            `json:"next_area_id"`
	NextQuestID int            `json:"next_quest_id"`
	Continuable bool           `json:"continuable"`
	// ConfigVersion 开战绑定的资源配置版本（ITEM350-03/06；0=剧情关）。
	ConfigVersion int64 `json:"config_version,omitempty"`
	// RuneSpec/RuneDrops 符文本开战摇奖输入与结果：仅内存携带（不入库），
	// 判定在 OpenBattleForParty 同事务内完成，skill 奖励落 Rewards 快照。
	RuneSpec      *RuneDropSpec    `json:"-"`
	RuneDrops     []RuneDropResult `json:"-"`
	ContinueCount int              `json:"continue_count"`
	Escaped       bool             `json:"escaped"`
	ReportTime    float64          `json:"report_time,omitempty"`
	HasReportTime bool             `json:"has_report_time,omitempty"`
	RestMC        int              `json:"rest_mc,omitempty"`
	HasRestMC     bool             `json:"has_rest_mc,omitempty"`
	DamageJSON    string           `json:"damage_json,omitempty"`
	CreatedAt     string           `json:"created_at"`
	UpdatedAt     string           `json:"updated_at"`
	SettledAt     string           `json:"settled_at,omitempty"`
}

// BattleReport result/lose 上行参数（time/mc/damage/escaped）。
type BattleReport struct {
	Time    float64
	RestMC  int
	Escaped bool
	Damage  map[string]int64
}

// BattleSettleResult 一次结算的读回结果（响应组装用）。
type BattleSettleResult struct {
	Already      bool           `json:"already"`       // true=幂等重放，零新增写入
	FirstClear   bool           `json:"first_clear"`   // 该关该难度首次通关
	ClearCount   int            `json:"clear_count"`   // 结算后该关该难度累计次数
	Rewards      []BattleReward `json:"rewards"`       // 入账明细（含未持久化的展示类）
	MonsterUUIDs []string       `json:"monster_uuids"` // seed 掉落实例
	Session      *BattleSession `json:"-"`             // 结算后会话终态
}

// BattleStore 战斗会话写口（cn35store 实现 SQLite 版；mem 模式用 MemoryBattles）。
// 每个方法自带一个事务边界；跨表写（会话/余额/库存/进度/通关/标志）在单次
// 调用内原子提交。
type BattleStore interface {
	// OpenBattle 建 prepared 会话；同事务顶替同关同难度的滞留会话并扫 TTL。
	// 返回 ErrBattleAP 表示建档时 AP 已不足（execute 前置校验的并发兜底）。
	OpenBattle(sess *BattleSession, apTTLSeconds int64) error
	// BattleSession 按 bid 读会话（账号归属校验内做）。
	BattleSession(userID, battleID string) (*BattleSession, error)
	// PayBattleAP ap_use：prepared→ap_paid 且条件扣 AP；非 prepared 态幂等
	// 返回（已付/已终态均 success）。ErrBattleAP = 扣点失败（并发消耗后不足）。
	PayBattleAP(userID, battleID string) (*BattleSession, error)
	// SettleBattle result：ap_paid|continued→cleared 全量结算；
	// 终态会话返回 {Already:true}；prepared 兜底先补扣 AP 再结算。
	SettleBattle(userID, battleID string, rep BattleReport) (*BattleSettleResult, error)
	// LoseBattle lose：ap_paid|continued|prepared→lost 记战报+首败标志；
	// 终态幂等返回。escaped=false 时本会话 continuable 强制置否。
	LoseBattle(userID, battleID string, rep BattleReport) (*BattleSession, error)
	// ContinueBattle 取账号最近一条 lost+continuable 会话，扣 1 钻转 continued。
	// ErrNoContinuable / ErrBattleCoin 分别映射续战入口/余额失败。
	ContinueBattle(userID string) (*BattleSession, int64, error)
	// LogBattle 归档 battle_record（fire-and-forget；不校验 JSON 语义）。
	LogBattle(userID, battleID string, questID int, record string) error
}

// DeckBattleStore atomically resolves/validates the account's saved party
// together with its equipped seed monsters, and creates the prepared battle
// session. The production SQLite store implements this so a concurrent deck
// or equip edit cannot split resolution from session creation.
// 第二返回值是参战单位装备的魔宠实例（unit.monster_uuid →
// cn35_player_monsters 行）：execute 的 asset_names 须按它们的模板 id
// 声明 monster_btn/monster_large_ns——客户端种子面板/召唤 cutin 经
// user_monsters 缓存取 id 后直查 quest.textures，appear_table 只覆盖敌方。
type DeckBattleStore interface {
	OpenBattleForParty(sess *BattleSession, apTTLSeconds int64, ordinal, unitLimit int) ([]UnitRecord, []MonsterRecord, error)
}

// ---------------------------------------------------------------------------
// 纯函数（无状态，供 handler/测试共用）
// ---------------------------------------------------------------------------

// AggregateRewards 遍历 appear_table（{waveId:{ms:spawnEntry}}）聚合成入账
// 快照：gacha_point/guild_asset 按 count 累加；seed/skill/item 按 id 分组累加
// count（缺省按 1）；其余 key 原样透传 count。聚合顺序确定（波次升序、ms 升序）。
func AggregateRewards(appearTable map[string]any) []BattleReward {
	var waves []int
	for w := range appearTable {
		if n, err := strconv.Atoi(w); err == nil {
			waves = append(waves, n)
		}
	}
	sort.Ints(waves)
	order := []BattleReward{}
	index := map[string]int{}
	add := func(key string, id, count int) {
		k := key
		if id != 0 {
			k = fmt.Sprintf("%s#%d", key, id)
		}
		if i, ok := index[k]; ok {
			order[i].Count += count
			return
		}
		index[k] = len(order)
		order = append(order, BattleReward{Key: key, ID: id, Count: count})
	}
	for _, w := range waves {
		wm, ok := appearTable[strconv.Itoa(w)].(map[string]any)
		if !ok {
			continue
		}
		var mss []int
		for ms := range wm {
			if n, err := strconv.Atoi(ms); err == nil {
				mss = append(mss, n)
			}
		}
		sort.Ints(mss)
		for _, ms := range mss {
			entry, ok := wm[strconv.Itoa(ms)].(map[string]any)
			if !ok {
				continue
			}
			rawRewards, _ := entry["rewards"].([]any)
			for _, rv := range rawRewards {
				rm, ok := rv.(map[string]any)
				if !ok {
					continue
				}
				key, _ := rm["key"].(string)
				if key == "" {
					continue
				}
				add(key, int(numOf(rm["id"])), rewardCount(rm))
			}
		}
	}
	return order
}

// numOf 容错读 JSON 数字（float64/int/json.Number）。
func numOf(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

// rewardCount seed/skill/item 缺省 count=1（客户端按出现次数计）；
// 其余缺省 0（gacha_point/guild_asset 显式带 count）。
func rewardCount(rm map[string]any) int {
	c := int(numOf(rm["count"]))
	if c == 0 {
		switch rm["key"] {
		case "seed", "skill", "item", "element":
			c = 1
		}
	}
	return c
}

// MonsterIDsInAppearTable 收集 appear_table 引用的魔宠模板 id 集。
func MonsterIDsInAppearTable(appearTable map[string]any) []int {
	set := map[int]bool{}
	for _, wv := range appearTable {
		wm, ok := wv.(map[string]any)
		if !ok {
			continue
		}
		for _, ev := range wm {
			entry, ok := ev.(map[string]any)
			if !ok {
				continue
			}
			if id := int(numOf(entry["id"])); id > 0 {
				set[id] = true
			}
		}
	}
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// BattleMonsterDoc 由 cardcatalog 魔宠模板拼 execute 响应的 monsters map 项。
// 字段集对齐 quest0 / serve_quest_execute_1 内嵌样本（Config oracle）：
// 行为字段（act_type_id/act_target_id/hit_frame/attack_range/reach/
// multi_hit/attack_se/特效/offset/run 帧/hardness_id/call_interval_ms）
// 由 applyMemorialMonsterBattle 按 memorial_masterdata.json 下发——
// 15 个官方魔宠文档逐值覆盖（含 knockback），其余 902 怪走纪念版真值。
// hp/at/seed_at 发 Size=1 基值（官方为实例缩放值，wave 缩放未还原前
// 的已知口径差）；wiki 保留键继续下发以兼容现有消费。
func BattleMonsterDoc(m *cardcatalog.Monster) map[string]any {
	rates := map[string]any{
		"fire": rateOr(m.Rates.Fire), "aqua": rateOr(m.Rates.Aqua),
		"wind": rateOr(m.Rates.Wind), "light": rateOr(m.Rates.Light),
		"dark": rateOr(m.Rates.Dark),
	}
	doc := map[string]any{
		"id": m.ID, "name": m.Name, "rare": m.Rare, "element": m.Element,
		"skin": m.Skin, "sklsp": m.SklSP, "sklcd": numOfNil(m.SklCD),
		"sarea": numOfNilI(m.SArea), "skill": m.Skill,
		"rates": rates, "obtain": m.Obtain,
		"asset_name": fmt.Sprintf("monster/monster_%d", m.ID),
		// doc 键（官方 execute 形状）
		"attr_id":         m.Element,
		"attr_name":       attrName[m.Element],
		"rare_type_id":    m.Rare * 10,
		"rare_type_count": m.Rare,
		"rare_type_name":  strings.Repeat("╂", m.Rare),
		"seed_skill_id":   0,
		"batch":           -1,
		"cutin_x":         -90,
		"cutin_y":         -100,
		"cutin_scale":     1.3,
	}
	// 文本：国服真实录包中文优先，其次 CN catalog，缺则纪念版补缺。
	mt := memorialMonsterText(m.ID)
	if n, ok := mt["name"].(string); ok && n != "" {
		doc["name"] = n
	}
	if d, ok := mt["description"].(string); ok && d != "" {
		doc["description"] = d
	} else if m.Remark != "" {
		doc["description"] = m.Remark
	}
	if o, ok := mt["obtain"].(string); ok && o != "" {
		doc["appear_stage_names"] = o
	} else if m.Obtain != "" {
		doc["appear_stage_names"] = m.Obtain
	}
	// 行为层：monster_defaults 铺底（含 knockback 重建值）→
	// monster_official（15 怪逐值）/ monster_memorial（902 怪真值）。
	// official/memorial 命中时 speed/attack_ms/attackable_count/cost/reach/
	// call_interval_ms 已是纪念版/官方真值，仅 defaults 层（CN 独占怪）回退 wiki。
	layer := applyMemorialMonsterBattle(doc, m.ID)

	fill := memorialMonsterFill(m.ID)
	if layer == "defaults" {
		if m.MSpd != nil && *m.MSpd > 0 {
			doc["speed"] = *m.MSpd
		} else if v, ok := fill["speed"]; ok {
			doc["speed"] = v
		}
		if m.ASPd != nil && *m.ASPd > 0 && *m.ASPd <= 10 {
			doc["attack_ms"] = int(*m.ASPd * 1000)
		} else if v, ok := fill["attack_ms"]; ok {
			doc["attack_ms"] = v
		}
		if m.ANum != nil && *m.ANum >= 1 {
			doc["attackable_count"] = *m.ANum
		} else if v, ok := fill["attackable_count"]; ok {
			doc["attackable_count"] = v
		}
		if m.SklSP > 0 {
			doc["cost"] = m.SklSP
		} else if v, ok := fill["cost"]; ok {
			doc["cost"], doc["sklsp"] = v, v
		}
		if m.SklCD != nil && *m.SklCD > 0 {
			doc["call_interval_ms"] = int(float64(float32(*m.SklCD)) * 1000)
		}
	}
	// wiki 形键（兼容旧消费）：wiki 记录值原样下发（含占位 0），
	// 缺键时取 catalog_fill。
	if m.MSpd != nil {
		doc["mspd"] = *m.MSpd
	}
	if m.ASPd != nil {
		doc["aspd"] = *m.ASPd
	}
	if m.ANum != nil {
		doc["anum"] = *m.ANum
	}
	if m.AArea != nil {
		doc["aarea"] = *m.AArea
	}
	mentry, mcovered := memorialMonsterEntry(m.ID)
	if mcovered && mentry["taghness"] != nil {
		doc["taghness"] = mentry["taghness"]
		doc["tenacity"] = mentry["taghness"]
	} else if m.Tenacity != nil && *m.Tenacity > 0 {
		doc["taghness"] = *m.Tenacity
		doc["tenacity"] = *m.Tenacity
	} else if v, ok := fill["taghness"]; ok {
		doc["taghness"] = v
		doc["tenacity"] = v
	} else {
		doc["taghness"] = 50.0
	}
	setRate(doc, "fire_rate", m.Rates.Fire)
	setRate(doc, "water_rate", m.Rates.Aqua)
	setRate(doc, "wind_rate", m.Rates.Wind)
	setRate(doc, "light_rate", m.Rates.Light)
	setRate(doc, "dark_rate", m.Rates.Dark)
	for _, rk := range []string{"fire_rate", "water_rate", "wind_rate", "light_rate", "dark_rate"} {
		if mcovered {
			if v, ok := mentry[rk].(float64); ok && v > 0 {
				doc[rk] = v
			}
		}
		if _, has := doc[rk]; !has {
			doc[rk] = 1.0
		}
	}
	if m.SklCD != nil {
		doc["sklcd"] = *m.SklCD
	} else if v, ok := fill["sklcd"]; ok {
		doc["sklcd"] = v
	}
	// hp/at/seed_at：纪念版基值优先，缺口回退 wiki。
	if v, ok := mentry["base_hp"].(float64); ok && v > 0 {
		doc["hp"], doc["life"] = int(v), int(v)
	} else if m.Life != nil && *m.Life > 0 {
		doc["hp"], doc["life"] = *m.Life, *m.Life
	} else if v, ok := mentry["hp"].(float64); ok && v > 0 {
		doc["hp"] = v
	}
	if v, ok := mentry["base_at"].(float64); ok && v > 0 {
		doc["at"], doc["atk"] = int(v), int(v)
	} else if m.Atk != nil && *m.Atk > 0 {
		doc["at"], doc["atk"], doc["seed_at"] = *m.Atk, *m.Atk, *m.Atk
	} else if v, ok := mentry["at"].(float64); ok && v > 0 {
		doc["at"] = v
	}
	if v, ok := mentry["base_seed_at"].(float64); ok && v > 0 {
		doc["seed_at"] = int(v)
	} else if _, ok := doc["seed_at"]; !ok {
		if a, ok := doc["at"]; ok {
			doc["seed_at"] = a
		}
	}
	return doc
}

func rateOr(v *float64) any {
	if v == nil {
		return 1.0
	}
	return *v
}

func numOfNil(v *float64) any {
	if v == nil {
		return 0.0
	}
	return *v
}

func numOfNilI(v *int) any {
	if v == nil {
		return 0
	}
	return *v
}

// ---------------------------------------------------------------------------
// MemoryBattles：-mem 联调桩与 handler 单测用进程内实现（与 SQLite 版同语义）。
// ---------------------------------------------------------------------------

// MemoryBattles 进程内战斗会话表。AP/钻走自带小账（默认 ap=20/coin=0，
// 与 MemoryAccounts 快照默认值一致）；进程死即清，仅供 -mem/单测。
type MemoryBattles struct {
	mu       sync.Mutex
	sessions map[string]*BattleSession
	byUser   map[string][]string // user → 创建序 bid
	ap       map[string]int
	coin     map[string]int64
	clears   map[string]int // user|quest|diff → count
	failed   map[string]bool
	progress map[string][3]int // user → stage/area/quest
	exp      map[string][2]int // user → level/exp
	monsters map[string][]string
	logs     []BattleLogEntry
	clock    int64 // 单调时间戳（测试可注入）
}

// BattleLogEntry 一条归档战报（Memory/SQLite 共用行投影）。
type BattleLogEntry struct {
	UserID   string `json:"user_id"`
	BattleID string `json:"battle_id"`
	QuestID  int    `json:"quest_id"`
	Record   string `json:"record"`
}

// NewMemoryBattles 空表。
func NewMemoryBattles() *MemoryBattles {
	return &MemoryBattles{
		sessions: map[string]*BattleSession{},
		byUser:   map[string][]string{},
		ap:       map[string]int{},
		coin:     map[string]int64{},
		clears:   map[string]int{},
		failed:   map[string]bool{},
		progress: map[string][3]int{},
		exp:      map[string][2]int{},
		monsters: map[string][]string{},
	}
}

func (m *MemoryBattles) apOf(userID string) int {
	if v, ok := m.ap[userID]; ok {
		return v
	}
	return 20 // 与 MemoryAccounts stubAP 一致
}

func (m *MemoryBattles) coinOf(userID string) int64 {
	return m.coin[userID]
}

func (m *MemoryBattles) progressOf(userID string) [3]int {
	if v, ok := m.progress[userID]; ok {
		return v
	}
	return [3]int{1, 1, 1}
}

func (m *MemoryBattles) tick() string {
	m.clock++
	return fmt.Sprintf("mem-%012d", m.clock)
}

// clone 避免外部改动内部表。
func cloneSession(s *BattleSession) *BattleSession {
	cp := *s
	cp.Rewards = append([]BattleReward(nil), s.Rewards...)
	return &cp
}

// OpenBattle 见 BattleStore。
func (m *MemoryBattles) OpenBattle(sess *BattleSession, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apOf(sess.UserID) < sess.APCost {
		return ErrBattleAP
	}
	// 顶替同关同难度非终态会话。
	for _, bid := range m.byUser[sess.UserID] {
		old := m.sessions[bid]
		if old.QuestID == sess.QuestID && old.Difficulty == sess.Difficulty {
			switch old.Status {
			case BattlePrepared, BattleAPPaid, BattleLost, BattleContinued:
				old.Status = BattleAbandoned
				old.UpdatedAt = m.tick()
			}
		}
	}
	m.sessions[sess.BattleID] = cloneSession(sess)
	m.byUser[sess.UserID] = append(m.byUser[sess.UserID], sess.BattleID)
	return nil
}

// BattleSession 见 BattleStore。
func (m *MemoryBattles) BattleSession(userID, battleID string) (*BattleSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[battleID]
	if !ok || s.UserID != userID {
		return nil, ErrBattleNotFound
	}
	return cloneSession(s), nil
}

// PayBattleAP 见 BattleStore（prepared 才扣点，其余态幂等返回）。
func (m *MemoryBattles) PayBattleAP(userID, battleID string) (*BattleSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[battleID]
	if !ok || s.UserID != userID {
		return nil, ErrBattleNotFound
	}
	if s.Status == BattlePrepared {
		if m.apOf(userID) < s.APCost {
			return nil, ErrBattleAP
		}
		m.ap[userID] = m.apOf(userID) - s.APCost
		s.Status = BattleAPPaid
		s.UpdatedAt = m.tick()
	}
	return cloneSession(s), nil
}

// SettleBattle 见 BattleStore。
func (m *MemoryBattles) SettleBattle(userID, battleID string, rep BattleReport) (*BattleSettleResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[battleID]
	if !ok || s.UserID != userID {
		return nil, ErrBattleNotFound
	}
	s.ReportTime, s.HasReportTime = rep.Time, true
	s.RestMC, s.HasRestMC = rep.RestMC, true
	switch s.Status {
	case BattleCleared, BattleLost, BattleAbandoned:
		return &BattleSettleResult{Already: true, Session: cloneSession(s)}, nil
	case BattlePrepared:
		if m.apOf(userID) < s.APCost {
			return nil, ErrBattleAP
		}
		m.ap[userID] = m.apOf(userID) - s.APCost
		s.Status = BattleAPPaid
	}
	if s.Status != BattleAPPaid && s.Status != BattleContinued {
		return nil, ErrBattleState
	}
	s.Status = BattleCleared
	s.SettledAt = m.tick()
	s.UpdatedAt = s.SettledAt
	key := fmt.Sprintf("%s|%d|%s", userID, s.QuestID, s.Difficulty)
	m.clears[key]++
	res := &BattleSettleResult{
		ClearCount: m.clears[key], FirstClear: m.clears[key] == 1,
		Rewards: append([]BattleReward(nil), s.Rewards...), Session: cloneSession(s),
	}
	// 进度推进：仅当结算的是当前前沿关。
	if p := m.progressOf(userID); p[2] == s.QuestID {
		m.progress[userID] = [3]int{s.NextStageID, s.NextAreaID, s.NextQuestID}
	}
	// 奖励入账：gacha_point/guild_asset 记余额账；seed 记掉落 uuid。
	for _, r := range s.Rewards {
		switch r.Key {
		case "gacha_point":
			m.coin[userID+"#gp"] += int64(r.Count)
		case "guild_asset":
			m.coin[userID+"#ga"] += int64(r.Count)
		case "seed":
			for i := 0; i < r.Count; i++ {
				u := fmt.Sprintf("m-%d", len(m.monsters[userID])+1)
				m.monsters[userID] = append(m.monsters[userID], u)
				res.MonsterUUIDs = append(res.MonsterUUIDs, u)
			}
		}
	}
	// exp/level（mem 简版：exp+=，level=max(1,exp/100+1)）。
	lv, ex := m.expOf(userID)
	ex += s.Exp
	for ex >= 100 {
		lv++
		ex -= 100
	}
	m.exp[userID] = [2]int{lv, ex}
	return res, nil
}

func (m *MemoryBattles) expOf(userID string) (int, int) {
	if v, ok := m.exp[userID]; ok {
		return v[0], v[1]
	}
	return 1, 50 // 与 MemoryAccounts stub 初始值一致
}

// LoseBattle 见 BattleStore。
func (m *MemoryBattles) LoseBattle(userID, battleID string, rep BattleReport) (*BattleSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[battleID]
	if !ok || s.UserID != userID {
		return nil, ErrBattleNotFound
	}
	switch s.Status {
	case BattlePrepared, BattleAPPaid, BattleContinued:
		s.Status = BattleLost
		s.Escaped = rep.Escaped
		s.ReportTime, s.HasReportTime = rep.Time, true
		s.RestMC, s.HasRestMC = rep.RestMC, true
		if !rep.Escaped {
			s.Continuable = false // Escape() 主动放弃：本会话不可续
		}
		s.UpdatedAt = m.tick()
		m.failed[userID] = true
	}
	return cloneSession(s), nil
}

// ContinueBattle 见 BattleStore（最近一条 lost+continuable）。
func (m *MemoryBattles) ContinueBattle(userID string) (*BattleSession, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *BattleSession
	for _, bid := range m.byUser[userID] {
		s := m.sessions[bid]
		if s.Status == BattleLost && s.Continuable {
			latest = s
		}
	}
	if latest == nil {
		return nil, m.coinOf(userID), ErrNoContinuable
	}
	if m.coinOf(userID) < 1 {
		return nil, m.coinOf(userID), ErrBattleCoin
	}
	m.coin[userID]--
	latest.Status = BattleContinued
	latest.ContinueCount++
	latest.UpdatedAt = m.tick()
	return cloneSession(latest), m.coinOf(userID), nil
}

// LogBattle 见 BattleStore。
func (m *MemoryBattles) LogBattle(userID, battleID string, questID int, record string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, BattleLogEntry{UserID: userID, BattleID: battleID, QuestID: questID, Record: record})
	return nil
}

// SeedMem 测试/联调用：预置 ap/coin/进度/exp。
func (m *MemoryBattles) SeedMem(userID string, ap int, coin int64, stage, area, quest int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ap[userID] = ap
	m.coin[userID] = coin
	m.progress[userID] = [3]int{stage, area, quest}
}
