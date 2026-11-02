// roster.go CN3.5 名册资源证据 + 模板发布值 + 双卡池版本化治理的领域契约
// （ROSTER350-01—06）。
//
// 事实等级与边界：
//   - 可抽资格四要件（缺一不入池）：必需六族证据齐全（APK 或远端叠加
//     资源目录均可作证）→ resource_ok；wiki 目录在册；管理员显式 enabled；
//     未被 blocked。
//   - wiki/units.json 是历史参考快照（版本/SHA256 入库溯源），不是当前
//     可抽证明；导入写模板与证据位，绝不自动启用。
//   - 运行时抽卡只读"已发布版本快照"（cn35_gacha_release 指向的
//     cn35_gacha_pool_versions.snapshot）；草稿面（cn35_gacha_pools +
//     cn35_gacha_pool_draft_members）在发布前不影响游戏端。
//   - 旧槽位池（pool_id 1 限时 / 4 高级金币）以 enabled=0 的草稿行显式
//     停用：发布校验拒绝把停用池写进快照，execute 因池不在版本内而拒绝。
package cn35game

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

// ---- 名册：资源证据 / 模板 / 状态 ----

// RosterRequiredFamilies 是必需资源族。原为 ROSTER350-01 冻结的八族；
// 2026-10-02 起降为六族：silhouette（未获得剪影）与 gray_btn（未持有灰按钮）
// 移出必需（抽中即持有的场景不消费这两族；可抽资源远多于八族全集，
// 原口径把 90% 的同伴挡在卡池外）。unit_large_ns 本就不在列。
var RosterRequiredFamilies = []string{
	"unit", "btn", "list", "square", "large", "rectangle",
}

// UnitAsset 是一族资源证据（与 cn35_unit_assets 行对应）。
type UnitAsset struct {
	Family      string `json:"family"`
	Bundle      string `json:"bundle"`
	Size        int64  `json:"size"`
	CRC32       string `json:"crc32"`
	SHA256      string `json:"sha256"`
	Served      bool   `json:"served"`
	IndexSHA256 string `json:"index_sha256"`
	IndexMatch  *bool  `json:"index_match"` // nil=索引缺键未核对
}

// UnitTemplate 是 units.json 单条的持久化形态（与 cn35_unit_templates 对应）。
// 可选字段缺失保持 nil/NULL 并记入 Missing；绝不伪造成 0。
type UnitTemplate struct {
	UnitID        int      `json:"unit_id"`
	Name          string   `json:"name"`
	NameJP        string   `json:"name_jp"`
	Title         string   `json:"title"`
	TitleJP       string   `json:"title_jp"`
	Rare          int      `json:"rare"`
	Element       int      `json:"element"`
	Weapon        int      `json:"weapon"`
	Type          int      `json:"unit_type"`
	Life          int      `json:"life"`
	Atk           int      `json:"atk"`
	AArea         int      `json:"aarea"`
	ANum          *int     `json:"anum"`
	MSpd          *int     `json:"mspd"`
	ASPd          *float64 `json:"aspd"`
	Hits          *int     `json:"hits"`
	Tenacity      *float64 `json:"tenacity"`
	Fire          *float64 `json:"fire"`
	Aqua          *float64 `json:"aqua"`
	Wind          *float64 `json:"wind"`
	Light         *float64 `json:"light"`
	Dark          *float64 `json:"dark"`
	Gender        *int     `json:"gender"`
	Age           *string  `json:"age"`
	Country       string   `json:"country"`
	Career        string   `json:"career"`
	Interest      string   `json:"interest"`
	Nature        string   `json:"nature"`
	Obtain        string   `json:"obtain"`
	Remark        string   `json:"remark"`
	WikiGacha     []int    `json:"wiki_gacha"`
	Contributors  []string `json:"contributors"`
	Missing       []string `json:"missing"`
	RawJSON       string   `json:"raw_json"`
	SourceVersion string   `json:"source_version"`
}

// RosterUnit 是某 unit_id 的名册状态（证据位 + 管理位）。
type RosterUnit struct {
	UnitID          int      `json:"unit_id"`
	Enabled         bool     `json:"enabled"`
	ResourceOK      bool     `json:"resource_ok"`
	MissingRequired []string `json:"missing_required"`
	Wiki            bool     `json:"wiki"`
	Blocked         bool     `json:"blocked"`
	Notes           string   `json:"notes"`
	UpdatedBy       string   `json:"updated_by"`
	UpdatedAt       string   `json:"updated_at"`
}

// Drawable 判定该名册行当前是否可被抽中（发布校验用同口径）。
func (r RosterUnit) Drawable() bool {
	return r.Enabled && r.ResourceOK && !r.Blocked
}

// ---- 名册清单（ROSTER350-01 生成产物的解析形态） ----

// RosterManifest 对应 server/assets/cn350/unit_roster_manifest.json。
// 由 tools/cn35roster 生成；导入只信 manifest 内嵌的 APK/索引指纹与逐族哈希。
type RosterManifest struct {
	ManifestVersion string `json:"manifest_version"`
	APK             struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"apk"`
	UnitsSource struct {
		Path    string `json:"path"`
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	} `json:"units_source"`
	Families struct {
		Required []string `json:"required"`
		Optional []string `json:"optional"`
	} `json:"families"`
	Units map[string]struct {
		Rare            int                           `json:"rare"`
		Name            string                        `json:"name"`
		Obtain          string                        `json:"obtain"`
		Wiki            bool                          `json:"wiki"`
		WikiGacha       []int                         `json:"wiki_gacha"`
		Families        map[string]*RosterManifestFam `json:"families"`
		MissingRequired []string                      `json:"missing_required"`
		Qualified       bool                          `json:"qualified"`
	} `json:"units"`
}

// RosterManifestFam 是清单中一族资源的证据。
type RosterManifestFam struct {
	Size        int64  `json:"size"`
	CRC32       string `json:"crc32"`
	SHA256      string `json:"sha256"`
	Served      bool   `json:"served"`
	IndexSHA256 string `json:"index_sha256,omitempty"`
	IndexMatch  *bool  `json:"index_match,omitempty"`
}

// ParseRosterManifest 解析清单并做结构校验（版本标签与必需族口径一致）。
func ParseRosterManifest(raw []byte) (*RosterManifest, error) {
	var m RosterManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("roster manifest 解析失败: %w", err)
	}
	if m.ManifestVersion != "roster350-01-v1" {
		return nil, fmt.Errorf("roster manifest 版本 %q 不支持", m.ManifestVersion)
	}
	if len(m.Units) == 0 {
		return nil, fmt.Errorf("roster manifest 没有任何 unit 记录")
	}
	got := map[string]bool{}
	for _, f := range m.Families.Required {
		got[f] = true
	}
	for _, f := range RosterRequiredFamilies {
		if !got[f] {
			return nil, fmt.Errorf("roster manifest 必需族 %q 缺失", f)
		}
	}
	return &m, nil
}

// ---- 卡池草稿与已发布版本 ----

// PityRules 是保底参数的导出别名（cn35store/admin35 需要构造该类型）。
type PityRules = gachaPityRules

// MarshalPityJSON 序列化保底参数（草稿池 pity 列存储形态）。
func MarshalPityJSON(p PityRules) []byte {
	b, _ := json.Marshal(pityToSnapshot(p))
	return b
}

// ParsePityJSON 反序列化草稿池 pity 列。
func ParsePityJSON(raw []byte) (PityRules, error) {
	var sp gachaSnapshotPity
	if err := json.Unmarshal(raw, &sp); err != nil {
		return PityRules{}, err
	}
	return pityFromSnapshot(&sp)
}

// GachaPoolDef 是草稿面池定义（cn35_gacha_pools 行的领域形态）。
type GachaPoolDef struct {
	PoolID      string   `json:"pool_id"`
	Slot        int      `json:"slot"`
	ListTypeID  int      `json:"list_type_id"`
	Code        string   `json:"code"` // diamond / gold / legacy_limited / legacy_gold_adv
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Enabled     bool     `json:"enabled"`
	PricePoint  int      `json:"price_point"`
	PriceCoin   int      `json:"price_coin"`
	MaxExec     int      `json:"max_exec"`
	MaxBonus    int      `json:"max_bonus"`
	Limited     bool     `json:"limited"`
	BeginAt     int64    `json:"begin_at"`
	EndAt       int64    `json:"end_at"`
	FeaturedIDs []string `json:"featured_ids"`
	// Distribution 无保底池的星级分布（rare_type_id→整数权重）。
	Distribution map[int]int `json:"distribution"`
	// Pity 保底参数；Enabled=false 时引擎不推进计数。
	Pity gachaPityRules `json:"pity"`
	// PityGroup 保底归属组（""=无保底池）。
	// 注意必须有显式 json 标签：管理端 JSON 一律 snake_case，缺标签时
	// "pity_group" 在反序列化中会被静默丢弃（EqualFold 不匹配下划线），
	// 管理员改保底归属会得到一个没有报错但字段丢失的草稿。
	PityGroup string `json:"pity_group"`
	Notes     string `json:"notes"`
}

// GachaDraftMember 是草稿面池成员。
type GachaDraftMember struct {
	PoolID string `json:"pool_id"`
	UnitID int    `json:"unit_id"`
	Weight int    `json:"weight"`
}

// GachaVersionMember 是已发布版本的成员明细。
type GachaVersionMember struct {
	PoolID     string `json:"pool_id"`
	UnitID     int    `json:"unit_id"`
	Weight     int    `json:"weight"`
	Rare       int    `json:"rare"`
	ResourceOK bool   `json:"resource_ok"`
}

// GachaConfigVersion 是一条已发布配置版本记录。
type GachaConfigVersion struct {
	Version   int64  `json:"version"`
	Digest    string `json:"digest"`
	Snapshot  string `json:"snapshot,omitempty"`
	Actor     string `json:"actor"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

// ActiveGachaConfig 是运行时消费的已发布配置（版本号 + 反解出的规则与名单）。
type ActiveGachaConfig struct {
	Version int64
	Digest  string
	Rules   gachaRules
	// MembersByPool 池→可中奖角色集合（发布校验过的名单+权重）。
	MembersByPool map[string][]gachaUnit
}

// AuditEntry 是一条管理操作审计。
type AuditEntry struct {
	AuditID   int64  `json:"audit_id"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

// ---- 发布快照（已发布版本的序列化形态；runtime 唯一真源） ----

const gachaSnapshotSchema = "cn350-gacha-config-v1"

type gachaSnapshotMember struct {
	UnitID int `json:"unit_id"`
	Weight int `json:"weight"`
	Rare   int `json:"rare"`
}

type gachaSnapshotPity struct {
	Enabled      bool           `json:"enabled"`
	URBase       int            `json:"ur_base"`
	URSoftStart  int            `json:"soft_start"`
	URSoftStep   int            `json:"soft_step"`
	URHard       int            `json:"hard"`
	SRPity       int            `json:"sr_pity"`
	UPGuarantee  bool           `json:"up_guarantee"`
	NonURWeights map[string]int `json:"non_ur_weights,omitempty"` // rare_type_id→权重
}

type gachaSnapshotPool struct {
	PoolID       string                `json:"pool_id"`
	Slot         int                   `json:"slot"`
	ListTypeID   int                   `json:"list_type_id"`
	Code         string                `json:"code"`
	Name         string                `json:"name"`
	Description  string                `json:"description"`
	PricePoint   int                   `json:"price_point"`
	PriceCoin    int                   `json:"price_coin"`
	MaxExec      int                   `json:"max_exec"`
	MaxBonus     int                   `json:"max_bonus"`
	Limited      bool                  `json:"limited"`
	BeginAt      int64                 `json:"begin_at,omitempty"`
	EndAt        int64                 `json:"end_at,omitempty"`
	FeaturedIDs  []string              `json:"featured_ids,omitempty"`
	Distribution map[string]int        `json:"distribution,omitempty"`
	Pity         *gachaSnapshotPity    `json:"pity,omitempty"`
	PityGroup    string                `json:"pity_group,omitempty"`
	Members      []gachaSnapshotMember `json:"members"`
}

type gachaSnapshot struct {
	Schema            string              `json:"schema"`
	Pools             []gachaSnapshotPool `json:"pools"`
	DuplicateComp     map[string]int      `json:"duplicate_comp"`
	DuplicateMaxCount int                 `json:"duplicate_max_count"`
}

// gachaGlobalPolicy 是全局结算政策（重复补偿与觉醒上限）。它是领域政策常量
// 而非逐池配置：发布时写入快照随版本固化，运行期从快照还原。
// comp 的值是满觉溢出后补偿的**徽章**数量（medal 余额，非金币）。
func gachaGlobalPolicy() (dupMax int, comp map[int]int) {
	return 15, map[int]int{
		gachaRarityN: 10, gachaRarityR: 20, gachaRarityHR: 50,
		gachaRaritySR: 200, gachaRarityUR: 1000,
	}
}

// ---- 发布校验与快照构建 ----

// RosterLookup 发布校验时查询名册状态的接口（由存储层注入）。
type RosterLookup func(unitID int) (RosterUnit, bool)

// TemplateLookup 发布校验时查询模板的接口。
type TemplateLookup func(unitID int) (*UnitTemplate, bool)

// GachaPublishError 汇总发布校验的全部失败项（批量报错便于一次修完）。
type GachaPublishError struct{ Problems []string }

func (e *GachaPublishError) Error() string {
	return fmt.Sprintf("卡池发布校验失败（%d 项）：%s", len(e.Problems), joinNonEmpty(e.Problems, "；"))
}

// BuildGachaSnapshot 把草稿面（池定义+成员）与名册/模板状态核对后构建
// 不可变快照。任一项违规返回 *GachaPublishError，绝不产出半成品。
//
// 冻结规则（ROSTER350-02/05）：
//   - 只有 enabled 池进入快照；停用池（1/4）绝不下发也不入快照。
//   - 金币池（price_point>0）禁含 5 星；成员稀有度必须在池可达稀有度内
//     （可达集合由 pity/non_ur_weights 或 distribution 决定）。
//   - 每个成员必须 resource_ok 且 enabled 且未 blocked 且模板存在。
//   - 每个启用池成员非空、价格单币种为正、权重为正。
func BuildGachaSnapshot(pools []GachaPoolDef, members map[string][]GachaDraftMember,
	roster RosterLookup, tpl TemplateLookup) ([]byte, string, []GachaVersionMember, error) {

	dupMax, compMap := gachaGlobalPolicy()
	snap := gachaSnapshot{
		Schema:            gachaSnapshotSchema,
		DuplicateComp:     map[string]int{},
		DuplicateMaxCount: dupMax,
	}
	for r, p := range compMap {
		snap.DuplicateComp[strconv.Itoa(r)] = p
	}
	var problems []string
	var flat []GachaVersionMember
	// pitySeen 记录每个保底组已见参数：同组共享计数器，跨池参数
	// 不一致会在快照解析期被"后者覆盖前者"静默吞掉，必须显式拒绝。
	pitySeen := map[string]gachaPityRules{}

	for _, p := range pools {
		if !p.Enabled {
			if len(members[p.PoolID]) > 0 {
				problems = append(problems,
					fmt.Sprintf("池 %s 已停用但仍有 %d 个草稿成员（先清空再发布）", p.PoolID, len(members[p.PoolID])))
			}
			continue
		}
		sp := gachaSnapshotPool{
			PoolID: p.PoolID, Slot: p.Slot, ListTypeID: p.ListTypeID,
			Code: p.Code, Name: p.Name, Description: p.Description,
			PricePoint: p.PricePoint, PriceCoin: p.PriceCoin,
			MaxExec: p.MaxExec, MaxBonus: p.MaxBonus,
			Limited: p.Limited, BeginAt: p.BeginAt, EndAt: p.EndAt,
			FeaturedIDs: append([]string(nil), p.FeaturedIDs...),
			PityGroup:   p.PityGroup,
		}
		// 结构校验（与 gachaRules.validate 同口径的执行前检查）。
		if p.PricePoint > 0 && p.PriceCoin > 0 {
			problems = append(problems, fmt.Sprintf("池 %s 同时配置金币与钻石价格", p.PoolID))
		}
		if p.PricePoint == 0 && p.PriceCoin == 0 {
			problems = append(problems, fmt.Sprintf("池 %s 价格为 0（启用池必须定价）", p.PoolID))
		}
		if p.MaxExec < 1 || p.MaxExec > 10 {
			problems = append(problems, fmt.Sprintf("池 %s max_exec=%d 越界", p.PoolID, p.MaxExec))
		}
		if p.Slot < 1 || p.Slot > 4 || p.ListTypeID < 0 || p.ListTypeID > 3 || p.Slot != p.ListTypeID+1 {
			problems = append(problems, fmt.Sprintf("池 %s 槽位/list_type_id 非法 (%d/%d)", p.PoolID, p.Slot, p.ListTypeID))
		}
		if p.Limited && (p.BeginAt <= 0 || p.EndAt <= p.BeginAt) {
			// 运行期 gachaPoolOpen 对 begin<=0/end<=begin 恒判未开放——
			// 启用池缺时间窗 = 发布一个永远点不开的池（占槽死池）。
			problems = append(problems, fmt.Sprintf("池 %s 为限时池但时间窗非法（begin=%d end=%d；启用池必须有可开放时间）",
				p.PoolID, p.BeginAt, p.EndAt))
		}
		// 保底面：coin 池必须有 pity 组且参数合法；point 池必须有 distribution。
		reach := map[int]bool{}
		if p.PriceCoin > 0 {
			if p.PityGroup == "" {
				problems = append(problems, fmt.Sprintf("池 %s 为钻石池但未配置保底组", p.PoolID))
			} else if prev, dup := pitySeen[p.PityGroup]; dup && PityRulesVersion(prev) != PityRulesVersion(p.Pity) {
				problems = append(problems, fmt.Sprintf("池 %s 保底组 %q 参数与其他池不一致（同组共享计数器，参数必须相同）",
					p.PoolID, p.PityGroup))
			} else {
				pitySeen[p.PityGroup] = p.Pity
			}
			if err := p.Pity.validate(); err != nil {
				problems = append(problems, fmt.Sprintf("池 %s 保底参数非法: %v", p.PoolID, err))
			}
			if !p.Pity.Enabled {
				problems = append(problems, fmt.Sprintf("池 %s 为钻石池但保底未启用（钻石池必须有保底）", p.PoolID))
			}
			sp.Pity = pityToSnapshot(p.Pity)
			reach[gachaRarityUR] = true
			// 可达集合必须与引擎抽样同口径：non_ur_weights 缺省时运行期
			// 回退 {SR,HR} 默认档，发布期若只认显式键会把"SR/HR 有成员"
			// 误判为不可达、又会把"仅 UR 成员"的错误配置放行成死池。
			for r := range p.Pity.effectiveNonURWeights() {
				reach[r] = true
			}
		} else {
			// 金币（point 计价）池不得携带保底配置：保底组是钻石计价通道的
			// 共享计数器，金币消耗混入会跨池推进保底进度（经济漏洞）；
			// pity 参数在此处也根本不会被写入快照，存留只会误导。
			if p.PityGroup != "" {
				problems = append(problems, fmt.Sprintf("池 %s 为金币池但配置了保底组 %q（保底计数器只能由钻石计价池推进）",
					p.PoolID, p.PityGroup))
			}
			if p.Pity.Enabled {
				problems = append(problems, fmt.Sprintf("池 %s 为金币池但配置了保底参数（金币池无保底，请先清空 pity）", p.PoolID))
			}
			if len(p.Distribution) == 0 {
				problems = append(problems, fmt.Sprintf("池 %s 为金币池但未配置星级分布", p.PoolID))
			}
			for r, w := range p.Distribution {
				if !validNonURRarity(r) {
					problems = append(problems, fmt.Sprintf("池 %s 分布稀有度 %d 越界（合法档 10/20/30/40）",
						p.PoolID, r))
					continue
				}
				if w <= 0 {
					problems = append(problems, fmt.Sprintf("池 %s 分布权重 rare=%d w=%d 非正", p.PoolID, r, w))
					continue
				}
				reach[r] = true
			}
			sp.Distribution = map[string]int{}
			for r, w := range p.Distribution {
				sp.Distribution[strconv.Itoa(r)] = w
			}
		}

		// 成员校验。
		dm := members[p.PoolID]
		if len(dm) == 0 {
			problems = append(problems, fmt.Sprintf("池 %s 已启用但没有成员", p.PoolID))
		}
		seen := map[int]bool{}
		memberRare := map[int]int{} // UP 校验用：featured_ids 必须是池内 5 星成员
		for _, m := range dm {
			if m.Weight <= 0 {
				problems = append(problems, fmt.Sprintf("池 %s 成员 %d 权重 %d 非正", p.PoolID, m.UnitID, m.Weight))
				continue
			}
			if seen[m.UnitID] {
				problems = append(problems, fmt.Sprintf("池 %s 成员 %d 重复", p.PoolID, m.UnitID))
				continue
			}
			seen[m.UnitID] = true
			ru, ok := roster(m.UnitID)
			if !ok {
				problems = append(problems, fmt.Sprintf("池 %s 成员 %d 不在名册中", p.PoolID, m.UnitID))
				continue
			}
			t, hasTpl := tpl(m.UnitID)
			if !hasTpl {
				problems = append(problems, fmt.Sprintf("池 %s 成员 %d 缺少模板记录", p.PoolID, m.UnitID))
				continue
			}
			rareID := t.Rare * 10
			if !ru.ResourceOK {
				problems = append(problems, fmt.Sprintf("池 %s 成员 %d 资源证据不齐（缺 %v）",
					p.PoolID, m.UnitID, ru.MissingRequired))
			}
			if !MemorialUnitConfigured(m.UnitID) {
				problems = append(problems, fmt.Sprintf("池 %s 成员 %d 缺 memorial 数值配置（unit_memorial 无行为条目）",
					p.PoolID, m.UnitID))
			}
			if !ru.Enabled {
				problems = append(problems, fmt.Sprintf("池 %s 成员 %d 未经管理员启用", p.PoolID, m.UnitID))
			}
			if ru.Blocked {
				problems = append(problems, fmt.Sprintf("池 %s 成员 %d 被标记阻断（%s）", p.PoolID, m.UnitID, ru.Notes))
			}
			if !reach[rareID] {
				problems = append(problems, fmt.Sprintf("池 %s 成员 %d 稀有度 %d 不可达（池分布/保底决定可达集合）",
					p.PoolID, m.UnitID, rareID))
			}
			if p.PricePoint > 0 && rareID == gachaRarityUR {
				problems = append(problems, fmt.Sprintf("池 %s 为金币池但含 5 星成员 %d（硬规则拒绝）", p.PoolID, m.UnitID))
			}
			memberRare[m.UnitID] = rareID
			sp.Members = append(sp.Members, gachaSnapshotMember{
				UnitID: m.UnitID, Weight: m.Weight, Rare: rareID,
			})
			flat = append(flat, GachaVersionMember{
				PoolID: p.PoolID, UnitID: m.UnitID, Weight: m.Weight,
				Rare: rareID, ResourceOK: ru.ResourceOK,
			})
		}
		// 反向校验：每个可达稀有度必须至少有一名成员，否则运行期会出现
		// "抽中稀有度却无候选角色"的悬空错误。
		byRare := map[int]int{}
		for _, m := range sp.Members {
			byRare[m.Rare]++
		}
		for r := range reach {
			if byRare[r] == 0 {
				problems = append(problems,
					fmt.Sprintf("池 %s 可达稀有度 %d 没有任何成员（分布/保底声明了但名单为空）", p.PoolID, r))
			}
		}
		// UP 校验：featured_ids 只能指向池内 5 星成员——否则引擎
		// 建池即报错（newGachaPoolPools），发布出去又是一个死池。
		for _, fid := range p.FeaturedIDs {
			id, ferr := strconv.Atoi(fid)
			if ferr != nil {
				problems = append(problems, fmt.Sprintf("池 %s UP 角色 id %q 非法", p.PoolID, fid))
				continue
			}
			if memberRare[id] != gachaRarityUR {
				problems = append(problems, fmt.Sprintf("池 %s UP 对象 %d 必须是池内 5 星成员", p.PoolID, id))
			}
		}
		sort.Slice(sp.Members, func(i, j int) bool { return sp.Members[i].UnitID < sp.Members[j].UnitID })
		snap.Pools = append(snap.Pools, sp)
	}
	if len(problems) > 0 {
		return nil, "", nil, &GachaPublishError{Problems: problems}
	}
	sort.Slice(snap.Pools, func(i, j int) bool { return snap.Pools[i].PoolID < snap.Pools[j].PoolID })
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil, "", nil, fmt.Errorf("快照序列化失败: %w", err)
	}
	// 自洽回读：产出的快照必须能被运行期解析层完整接收——max_bonus、
	// UP 数量、常驻池携带限时字段等"校验未逐条复制"的约束由
	// ParseActiveGachaConfig→rules.validate() 兜底，杜绝发布出
	// "写库成功、加载即失败"的半成品版本。
	if _, perr := ParseActiveGachaConfig(0, string(raw)); perr != nil {
		return nil, "", nil, &GachaPublishError{Problems: []string{
			fmt.Sprintf("快照自洽回读失败: %v", perr)}}
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), flat, nil
}

// pityToSnapshot / pityFromSnapshot 在领域与序列化形态间转换保底参数。
func pityToSnapshot(p gachaPityRules) *gachaSnapshotPity {
	sp := &gachaSnapshotPity{
		Enabled: p.Enabled, URBase: p.URBasePercent, URSoftStart: p.URSoftStart,
		URSoftStep: p.URSoftStepPercent, URHard: p.URHardPity, SRPity: p.SRPity,
		UPGuarantee: p.UPGuarantee,
	}
	if len(p.NonURWeights) > 0 {
		sp.NonURWeights = map[string]int{}
		for r, w := range p.NonURWeights {
			sp.NonURWeights[strconv.Itoa(r)] = w
		}
	}
	return sp
}

func pityFromSnapshot(sp *gachaSnapshotPity) (gachaPityRules, error) {
	p := gachaPityRules{
		Enabled: sp.Enabled, URBasePercent: sp.URBase, URSoftStart: sp.URSoftStart,
		URSoftStepPercent: sp.URSoftStep, URHardPity: sp.URHard, SRPity: sp.SRPity,
		UPGuarantee: sp.UPGuarantee,
	}
	if len(sp.NonURWeights) > 0 {
		p.NonURWeights = map[int]int{}
	}
	for k, v := range sp.NonURWeights {
		r, err := strconv.Atoi(k)
		if err != nil {
			// 非数字键不能静默丢弃：漏掉一个键会让 NonURWeights 变空，
			// 引擎回退默认档后分布口径与快照本意分叉。
			return p, fmt.Errorf("非五星权重键 %q 非法", k)
		}
		p.NonURWeights[r] = v
	}
	return p, nil
}

// ParseActiveGachaConfig 把版本行反解为运行时规则与名单。
// 这是游戏端唯一读口：rules_version 标签为 "pv<version>"，摘要随版本固化。
func ParseActiveGachaConfig(version int64, snapshot string) (*ActiveGachaConfig, error) {
	var snap gachaSnapshot
	if err := json.Unmarshal([]byte(snapshot), &snap); err != nil {
		return nil, fmt.Errorf("卡池版本 %d 快照解析失败: %w", version, err)
	}
	if snap.Schema != gachaSnapshotSchema {
		return nil, fmt.Errorf("卡池版本 %d 快照 schema %q 不支持", version, snap.Schema)
	}
	cfg := &ActiveGachaConfig{
		Version:       version,
		MembersByPool: map[string][]gachaUnit{},
		Rules: gachaRules{
			Version:               fmt.Sprintf("pv%d", version),
			Pity:                  map[string]gachaPityRules{},
			DuplicateCompensation: map[int]gachaDuplicateReward{},
			DuplicateMaxCount:     snap.DuplicateMaxCount,
		},
	}
	for k, v := range snap.DuplicateComp {
		r, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("卡池版本 %d 补偿表稀有度 %q 非法", version, k)
		}
		cfg.Rules.DuplicateCompensation[r] = gachaDuplicateReward{Medal: v}
	}
	if cfg.Rules.DuplicateMaxCount < 1 {
		cfg.Rules.DuplicateMaxCount = 15
	}
	for _, sp := range snap.Pools {
		pr := gachaPoolRules{
			PoolID: sp.PoolID, Slot: sp.Slot, ListTypeID: sp.ListTypeID,
			Name: sp.Name, Description: sp.Description,
			Price:              gachaPrice{Point: sp.PricePoint, Coin: sp.PriceCoin},
			MaxExecutableCount: sp.MaxExec, MaxExecuteBonus: sp.MaxBonus,
			Limited: sp.Limited, BeginAt: sp.BeginAt, EndAt: sp.EndAt,
			FeaturedURIDs: append([]string(nil), sp.FeaturedIDs...),
			PityGroup:     sp.PityGroup,
			RarityWeights: map[string]int{},
		}
		if len(sp.Distribution) > 0 {
			pr.Distribution = map[int]int{}
			for k, v := range sp.Distribution {
				r, err := strconv.Atoi(k)
				if err != nil {
					return nil, fmt.Errorf("卡池版本 %d 池 %s 分布键 %q 非法", version, sp.PoolID, k)
				}
				pr.Distribution[r] = v
			}
		}
		if sp.Pity != nil && sp.PityGroup != "" {
			pv, err := pityFromSnapshot(sp.Pity)
			if err != nil {
				return nil, fmt.Errorf("卡池版本 %d 池 %s 保底参数非法: %w", version, sp.PoolID, err)
			}
			cfg.Rules.Pity[sp.PityGroup] = pv
		}
		var units []gachaUnit
		for _, m := range sp.Members {
			units = append(units, gachaUnit{
				UnitID: strconv.Itoa(m.UnitID), RarityID: m.Rare,
			})
			pr.RarityWeights[strconv.Itoa(m.UnitID)] = m.Weight
		}
		cfg.MembersByPool[sp.PoolID] = units
		cfg.Rules.Pools = append(cfg.Rules.Pools, pr)
	}
	if err := cfg.Rules.validate(); err != nil {
		return nil, fmt.Errorf("卡池版本 %d 快照自洽校验失败: %w", version, err)
	}
	return cfg, nil
}

// PityRulesVersion 是保底组级版本标签：只含保底参数指纹，与名单/价格无关。
// 这样发布只调整成员的新版本不会把玩家保底计数锁死；保底参数变更才会
// 触发"版本不一致→拒绝+人工处理"路径（与原语义一致）。
func PityRulesVersion(p gachaPityRules) string {
	// 归一化：NonURWeights 缺省与显式默认档语义相同（引擎三处口径
	// 都走 effectiveNonURWeights），指纹必须一致——否则发布一个语义
	// 等价的草稿会把全体玩家的保底行锁死在"版本不一致→请联系客服"。
	p.NonURWeights = p.effectiveNonURWeights()
	sp := pityToSnapshot(p)
	raw, _ := json.Marshal(sp)
	sum := sha256.Sum256(raw)
	return "pity-" + hex.EncodeToString(sum[:8])
}

// ---- 存储接口（cn35store 实现；admin35 与游戏端共用） ----

// GachaConfigStore 是运行时消费的已发布配置读面。
// ActiveGachaConfig 返回 (nil, nil) 表示尚未发布任何版本。
type GachaConfigStore interface {
	ActiveGachaConfig() (*ActiveGachaConfig, error)
}

// RosterFilter 名册列表过滤条件（管理端筛选）。
type RosterFilter struct {
	Rare     int    // 1..5；0=全部
	Pool     string // ""=全部；"2"/"3"=草稿成员；"none"=不在任何启用草稿池
	Resource string // ""=全部；"ok"=证据齐；"missing"=缺必需族；"none"=无证据
	Complete string // ""=全部；"full"=模板无缺失字段；"partial"=有缺失字段
	Enabled  *bool  // nil=全部
	Query    string // 名称/ID 模糊
	Limit    int
	Offset   int
}

// RosterUnitView 是名册列表的一行（名册状态 + 模板摘要 + 草稿归属）。
type RosterUnitView struct {
	RosterUnit
	Name    string   `json:"name"`
	Rare    int      `json:"rare"`
	Obtain  string   `json:"obtain"`
	Pools   []string `json:"pools"`   // 所属草稿池
	Missing []string `json:"missing"` // 模板缺失字段
}

// RosterUnitDetail 是 unit 详情（模板 + 证据 + 名册状态）。
type RosterUnitDetail struct {
	Roster   RosterUnit    `json:"roster"`
	Template *UnitTemplate `json:"template"`
	Assets   []UnitAsset   `json:"assets"`
	// WikiRaw 原始 wiki 记录 JSON（与模板字段并排对照）。
	WikiRaw string `json:"wiki_raw"`
	// PoolMemberships 已发布/草稿归属。
	DraftPools  []string         `json:"draft_pools"`
	PublishedIn map[string]int64 `json:"published_in"` // pool_id → 最新含该成员的版本
}

// RosterOverview 管理端总览统计。
type RosterOverview struct {
	UnitsTotal       int            `json:"units_total"`
	UnitsWiki        int            `json:"units_wiki"`
	UnitsResourceOK  int            `json:"units_resource_ok"`
	UnitsEnabled     int            `json:"units_enabled"`
	UnitsBlocked     int            `json:"units_blocked"`
	ResourcePerRare  map[string]int `json:"resource_per_rare"`
	ActiveVersion    int64          `json:"active_version"`
	ActiveDigest     string         `json:"active_digest"`
	DraftMemberCount map[string]int `json:"draft_member_count"`
}

// RosterImport 是名册导入输入（mercdb cn35-roster-import 与种子发布共用）。
type RosterImport struct {
	Manifest     *RosterManifest
	ManifestPath string
	ManifestSHA  string
	Templates    []UnitTemplate // units.json 全量记录
	UnitsPath    string
	UnitsSHA     string
	UnitsVersion string
}

// RosterImportResult 导入结果/预览差异。
type RosterImportResult struct {
	Committed         bool     `json:"committed"`
	TemplateUpserts   int      `json:"template_upserts"` // 新增或内容变化
	TemplateUnchanged int      `json:"template_unchanged"`
	AssetRows         int      `json:"asset_rows"`
	RosterRows        int      `json:"roster_rows"`
	RosterEnabledNow  int      `json:"roster_enabled_now"` // 导入后仍启用（导入不动 enabled 位）
	ResourceOKCount   int      `json:"resource_ok_count"`
	Disqualified      []string `json:"disqualified"` // wiki 在册但资源不齐
	Notes             []string `json:"notes"`
}

// RosterAdminStore 名册+池治理的存储契约（admin35 与 mercdb 共用）。
type RosterAdminStore interface {
	GachaConfigStore // pools 视图需要当前生效版本
	// 名册读面
	RosterOverview() (*RosterOverview, error)
	RosterUnits(f RosterFilter) ([]RosterUnitView, int, error)
	RosterUnitDetail(unitID int) (*RosterUnitDetail, error)
	UnitTemplate(unitID int) (*UnitTemplate, bool, error)
	// 名册写面（全部落审计）
	SetRosterFlags(unitIDs []int, enabled *bool, blocked *bool, note, actor string) error
	SaveUnitTemplate(t *UnitTemplate, actor, note string) error
	ImportRoster(in *RosterImport, commit bool, actor string) (*RosterImportResult, error)
	// 池草稿读面
	GachaPools() ([]GachaPoolDef, error)
	GachaDraftMembers() (map[string][]GachaDraftMember, error)
	// 池草稿写面
	SaveGachaPoolDef(def GachaPoolDef, actor string) error
	ReplaceDraftMembers(poolID string, members []GachaDraftMember, actor string) error
	// 发布/版本
	PublishGachaDraft(actor, note string) (*GachaConfigVersion, error)
	ActivateGachaVersion(version int64, actor, note string) error
	GachaVersions(limit int) ([]GachaConfigVersion, int64, error)
	GachaVersionMembers(version int64) ([]GachaVersionMember, error)
	// 审计
	AuditLog(limit int) ([]AuditEntry, error)
}

// ErrRosterNotFound 名册行不存在。
var ErrRosterNotFound = errors.New("cn35game: 名册行不存在")

// ---- 模板发布值覆盖目录（ROSTER350-04） ----

// UnitCatalog 是同伴/魔宠模板只读目录契约。
// *cardcatalog.Catalog 与 rosterCatalog 都实现它；调用方经此接口取模板，
// 从而"DB 已发布模板值"与"静态目录回退"对所有读面一致生效。
type UnitCatalog interface {
	Unit(id int) (*cardcatalog.Unit, error)
	Monster(id int) (*cardcatalog.Monster, error)
	UnitIDs() []int
	MonsterIDs() []int
	Meta() cardcatalog.Meta
	Report() *cardcatalog.Report
}

// UnitTemplateReader 名册模板的按 id 读面（cn35store.Store 实现）。
type UnitTemplateReader interface {
	UnitTemplate(unitID int) (*UnitTemplate, bool, error)
}

// rosterCatalog 把 DB 名册模板叠加到静态 cardcatalog 之上：
//   - Unit(id)：模板行存在 → 由模板重建（管理员发布值即读面值）；
//     不存在 → 回退静态 catalog（非抽取旧单位/未导入单位的明示回退策略）。
//   - 魔宠与元信息/质量报告仍走静态目录（名册治理只管同伴模板）。
type rosterCatalog struct {
	base *cardcatalog.Catalog
	tpl  UnitTemplateReader
}

// NewRosterCatalog 构建覆盖层；tpl 为 nil 时等价于 base 直读。
func NewRosterCatalog(base *cardcatalog.Catalog, tpl UnitTemplateReader) UnitCatalog {
	if base == nil || tpl == nil {
		return base
	}
	return &rosterCatalog{base: base, tpl: tpl}
}

func (c *rosterCatalog) Unit(id int) (*cardcatalog.Unit, error) {
	if t, ok, err := c.tpl.UnitTemplate(id); err == nil && ok {
		return UnitFromTemplate(t), nil
	}
	return c.base.Unit(id)
}
func (c *rosterCatalog) Monster(id int) (*cardcatalog.Monster, error) { return c.base.Monster(id) }
func (c *rosterCatalog) UnitIDs() []int                               { return c.base.UnitIDs() }
func (c *rosterCatalog) MonsterIDs() []int                            { return c.base.MonsterIDs() }
func (c *rosterCatalog) Meta() cardcatalog.Meta                       { return c.base.Meta() }
func (c *rosterCatalog) Report() *cardcatalog.Report                  { return c.base.Report() }

// UnitFromTemplate 把名册模板行重建为 cardcatalog.Unit。
// 字段一一对应；Missing 语义保持（可选字段 nil + Missing 列表），
// 读面据此"缺键而非填零"。
func UnitFromTemplate(t *UnitTemplate) *cardcatalog.Unit {
	u := &cardcatalog.Unit{
		ID: t.UnitID, Name: t.Name, NameJP: t.NameJP, Title: t.Title,
		TitleJP: t.TitleJP, Rare: t.Rare, Element: t.Element, Weapon: t.Weapon,
		Type: t.Type, ANum: t.ANum, Life: t.Life, Atk: t.Atk, MSpd: t.MSpd,
		ASPd: t.ASPd, AArea: t.AArea, Tenacity: t.Tenacity, Hits: t.Hits,
		Rates: cardcatalog.AttrRates{
			Fire: t.Fire, Aqua: t.Aqua, Wind: t.Wind, Light: t.Light, Dark: t.Dark,
		},
		Country: t.Country, Gender: t.Gender, Age: t.Age, Career: t.Career,
		Interest: t.Interest, Nature: t.Nature, Obtain: t.Obtain,
		Remark: t.Remark, Gacha: t.WikiGacha, Contributors: t.Contributors,
		Missing: t.Missing,
	}
	if t.RawJSON != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(t.RawJSON), &raw); err == nil {
			u.Raw = raw
		}
	}
	return u
}

// ErrGachaNoPublishedConfig 没有任何已发布版本（未初始化抽卡配置）。
var ErrGachaNoPublishedConfig = errors.New("cn35game: 尚未发布任何卡池配置")

// ErrGachaVersionUnknown 指定版本不存在。
var ErrGachaVersionUnknown = errors.New("cn35game: 卡池配置版本不存在")

// ---- 种子双池政策（ROSTER350-02 冻结值） ----

// GachaSeedPools 返回四个草稿池定义：2=钻石、3=金币为启用双池；
// 1=限时、4=高级金币为停用占位（enabled=0，槽位显式占用）。
// 数值即现行服务端提案值（价格/保底/分布与公示文案同源，不得两处各写）。
func GachaSeedPools() []GachaPoolDef {
	return []GachaPoolDef{
		{
			PoolID: "1", Slot: 1, ListTypeID: 0, Code: "legacy_limited",
			Name: "限时招募", Enabled: false, Limited: true,
			Description: "本期推荐角色出现率提升。\\n本池为服务端重建规则，非原服数值。",
			PriceCoin:   5, MaxExec: 10,
			PityGroup: gachaPityGroupLimitedUP,
			Pity: gachaPityRules{
				Enabled: true, URBasePercent: 2, URSoftStart: 50,
				URSoftStepPercent: 2, URHardPity: 80, SRPity: 10,
				UPGuarantee: true, NonURWeights: map[int]int{gachaRaritySR: 18, gachaRarityHR: 80},
			},
			Notes: "ROSTER350：槽1 限时位停用占位（enabled=0），不进入已发布版本。",
		},
		{
			PoolID: "2", Slot: 2, ListTypeID: 1, Code: "diamond",
			Name: "钻石招募", Enabled: true,
			Description: "使用钻石招募同伴。\\n每连续 10 次至少获得 1 名 SR 以上。",
			PriceCoin:   5, MaxExec: 10,
			PityGroup: gachaPityGroupDiamond,
			Pity: gachaPityRules{
				Enabled: true, URBasePercent: 2, URSoftStart: 50,
				URSoftStepPercent: 2, URHardPity: 80, SRPity: 10,
				NonURWeights: map[int]int{gachaRaritySR: 18, gachaRarityHR: 80},
			},
		},
		{
			PoolID: "3", Slot: 3, ListTypeID: 2, Code: "gold",
			Name: "金币招募", Enabled: true,
			Description: "使用金币招募同伴。\\n本池无保底，招募概率固定。",
			PricePoint:  200, MaxExec: 10,
			// 1-4★分布（服务端重建提案值，非原服数值）：四星按 1% 低率进入。
			Distribution: map[int]int{gachaRarityN: 70, gachaRarityR: 24, gachaRarityHR: 5, gachaRaritySR: 1},
		},
		{
			PoolID: "4", Slot: 4, ListTypeID: 3, Code: "legacy_gold_adv",
			Name: "高级金币招募", Enabled: false,
			Description: "使用金币招募更高稀有度的同伴。\\n本池无保底，招募概率固定。",
			PricePoint:  2000, MaxExec: 10,
			Distribution: map[int]int{gachaRarityR: 80, gachaRarityHR: 18, gachaRaritySR: 2},
			Notes:        "ROSTER350：槽4 高级金币池停用占位（enabled=0），不进入已发布版本。",
		},
	}
}

// GachaSeedMembers 从名册清单推导种子成员（资源合格 + wiki 在册 +
// obtain 文本来源复核 + 星级归属规则 + memorial 数值配置）。推导口径冻结：
//   - 钻石池(2)：rare∈{3,4,5} 且 obtain 含"钻石"（骑士契约书）。
//   - 金币池(3)：rare∈{1,2,3,4} 且 obtain 含"金币"（勇者契约书2W等）。
//   - 停用池(1/4)与不可达稀有度不分配成员。
//   - unit_memorial 无行为数值配置的单位一律不入池（六族素材 + 数值
//     配置双要件，发布校验同口径拒绝）。
//
// 遵循 open-all 全量开放口径（2026-10-05 数值配置门槛）：所有满足
// Qualified（六族素材完整）&& Wiki（模板在册）&& MemorialUnitConfigured
// （unit_memorial 数值在册）的角色按星级开放入池：
//   - 池 "2"（钻石）：3★–5★（914 名）
//   - 池 "3"（金币）：1★–4★（1,096 名）
//
// 返回的成员需经 BuildGachaSnapshot 校验后才可发布。
func GachaSeedMembers(m *RosterManifest) map[string][]GachaDraftMember {
	out := map[string][]GachaDraftMember{"2": nil, "3": nil}
	for idStr, u := range m.Units {
		if !u.Qualified || !u.Wiki {
			continue
		}
		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			continue
		}
		// 数值配置要件（2026-10-05 口径）：unit_memorial 无对应行为数值
		// 配置的单位不允许抽取——素材六族齐全只是准入的一半，缺数值
		// 配置的角色战斗中会退回缺省行为。
		if !MemorialUnitConfigured(id) {
			continue
		}
		if u.Rare >= 3 && u.Rare <= 5 {
			out["2"] = append(out["2"], GachaDraftMember{PoolID: "2", UnitID: id, Weight: 1})
		}
		if u.Rare >= 1 && u.Rare <= 4 {
			out["3"] = append(out["3"], GachaDraftMember{PoolID: "3", UnitID: id, Weight: 1})
		}
	}
	for _, list := range out {
		sort.Slice(list, func(i, j int) bool { return list[i].UnitID < list[j].UnitID })
	}
	return out
}

// SeedGachaRoster 首次种子化：名册导入 + 双池定义 + 成员启用 + 发布，
// 一条龙在同一入口完成（mercd35 启动与 wire 测试共用，避免两处各写）。
// 已存在池定义时幂等跳过并返回 didSeed=false——注意此时不会自动补发
// 发布：草稿面的后续变更必须由管理员显式 publish/activate（草稿不可见
// 于运行期的红线不能借"补发"绕过）。若种子流程中途失败，残留的是
// 一致的草稿态（导入/池定义/成员各自事务完整），修复路径是显式调用
// publish 而非隐式重跑种子。
func SeedGachaRoster(st RosterAdminStore, in *RosterImport, actor string) (*GachaConfigVersion, bool, error) {
	pools, err := st.GachaPools()
	if err != nil {
		return nil, false, err
	}
	if len(pools) > 0 {
		return nil, false, nil
	}
	if _, err := st.ImportRoster(in, true, actor); err != nil {
		return nil, false, fmt.Errorf("种子导入失败: %w", err)
	}
	for _, def := range GachaSeedPools() {
		if err := st.SaveGachaPoolDef(def, actor); err != nil {
			return nil, false, fmt.Errorf("种子池定义 %s: %w", def.PoolID, err)
		}
	}
	members := GachaSeedMembers(in.Manifest)
	// 同一单位可同时进钻石/金币池：去重后只置一次 enabled，避免审计计数虚高。
	var ids []int
	seen := map[int]bool{}
	for _, ms := range members {
		for _, m := range ms {
			if seen[m.UnitID] {
				continue
			}
			seen[m.UnitID] = true
			ids = append(ids, m.UnitID)
		}
	}
	yes := true
	if err := st.SetRosterFlags(ids, &yes, nil, "seed：初始可抽名单", actor); err != nil {
		return nil, false, fmt.Errorf("种子名册启用: %w", err)
	}
	for pid, ms := range members {
		if err := st.ReplaceDraftMembers(pid, ms, actor); err != nil {
			return nil, false, fmt.Errorf("种子池 %s 成员: %w", pid, err)
		}
	}
	v, err := st.PublishGachaDraft(actor, "ROSTER350-02 种子双池初始发布（钻石3-5★/金币1-4★）")
	if err != nil {
		return nil, false, fmt.Errorf("种子发布: %w", err)
	}
	return v, true, nil
}
