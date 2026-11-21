// gacha_rules.go CN3.5 抽卡规则配置真源（GACHA350-02）。
//
// 事实等级：本文件是**服务端重建提案**，不是原服逐位事实。原服真实概率、
// 保底与重复卡补偿均无证据；参考机制来源见
// notes/proposed/2026-09-25-cn35-gacha-restoration.md §3（只作机制参考，
// 不宣称复制任何游戏）。
//
// 为什么要有这个文件：客户端 GachaIndexParts.cs 把四个页签映射到
// `gachas` 文档数组（list_type_id<4 → 槽号=list_type_id+1；其余落槽1），
// 且 UI 只从服务端下发的 gacha doc 读 name/description/point/price/
// max_executable_count/max_execute_bonus/available_list_option。因此
// **池的展示与价格必须与本文件的规则同源生成**，不能两处各写一份
// （否则出现"UI 显示的价格与实际扣费不一致"）。本文件同时是概率计算的
// 唯一真源，rules 版本号进入抽卡操作记录用于审计。
package cn35game

import (
	"fmt"
)

// gachaRulesVersion 是规则内容版本。任何影响结果分布的改动都必须提升它；
// 已提交的历史操作按当时版本审计，禁止跨规则版本静默迁移保底计数。
//
// ROSTER350-03 起该常量仅作**历史别名**保留：cn35_gacha_pity.rules_version
// 里已存在的旧行以此值标记；新写入一律用 PityRulesVersion（保底参数指纹），
// 已发布版本的规则版本标签为 "pv<version>"。
const gachaRulesVersion = "cn350-gacha-rules-v1"

// 稀有度常量。客户端 SWFGacha.cs 把 rare_type_id 映射为演出档位
// （10/20/30/40/50 → N/R/HR/SR/UR），GachaResult.cs 按 rare_type_id
// 升序排序，因此这里的数值就是客户端契约的一部分，不可随意改。
const (
	gachaRarityN  = 10
	gachaRarityR  = 20
	gachaRarityHR = 30
	gachaRaritySR = 40
	gachaRarityUR = 50
)

// gachaCurrencyPoint / gachaCurrencyCoin / gachaCurrencyMedal 对应余额表的
// currency 列。金币=gacha_point 行（客户端 data.user.gacha_point），钻石=coin
// 行（客户端 data.user_coin.amount），徽章=medal 行（客户端 data.user.medal）。
// 三者同表不同行，见 0003 迁移。
const (
	gachaCurrencyPoint = "gacha_point"
	gachaCurrencyCoin  = "coin"
	gachaCurrencyMedal = "medal"
)

// gachaPrice 是一次抽取的价格。point 与 coin 互斥（point>0 走金币分支）。
type gachaPrice struct {
	Point int // >0 表示金币池（CN3.5 单抽/连抽都按 point 结算）
	Coin  int // >0 表示钻石池
}

// gachaPoolRules 是单个池的完整规则。
type gachaPoolRules struct {
	// PoolID 是下发给客户端的 gacha.id，也是 /gachas/execute?id= 的取值。
	// 必须与槽位映射一致（见 gachaRules.slotFor）。
	PoolID string
	// Slot 是该池占据的原生标签槽（1—4）。客户端按 list_type_id 排序后
	// 用 (list_type_id+1) 作槽号，槽1 为限时位（需要 began_at/ended_at）。
	Slot int
	// ListTypeID 是下发给客户端的 list_type_id 原值。等于 Slot-1；
	// 槽1 用 0，其余按 1/2/3。注意：客户端对任意 list_type_id>=4 都
	// 落到槽1，所以新增池必须显式给出 0—3，不能让默认值漂移。
	ListTypeID int
	// Limited 为真时下发 began_at/ended_at，客户端按时间启用槽1 并轮询。
	Limited bool
	// BeginAt/EndAt 是 Unix 秒。仅 Limited 池使用；end<=begin 视为未开放。
	BeginAt int64
	EndAt   int64
	// Name/Description 直接进客户端 nameLabel/descriptionLabel。
	Name        string
	Description string
	// Price 见 gachaPrice。
	Price gachaPrice
	// MaxExecutableCount 是连抽按钮的基准扣费次数（下发 max_executable_count；
	// 十连 9 折时为 9，实际抽取数为 MaxExecutableCount + MaxExecuteBonus = 10）。
	MaxExecutableCount int
	// MaxExecuteBonus 十连赠送抽数（十连 9 折时为 1，无折扣时为 0），下发 max_execute_bonus。
	MaxExecuteBonus int
	// FeaturedURIDs 是当期 UP 的五星集合。空集合=非 UP 池。
	// 首期只允许一个 UP 五星（设计 §3.4）。
	FeaturedURIDs []string
	// RarityWeights 是同星集合内的整数权重（unitID→权重）。缺省权重 1。
	// 非空时必须覆盖该池全部可获得角色，否则拒绝启用（防"空中奖"）。
	// ROSTER350 起由已发布版本成员表填充（成员级权重）。
	RarityWeights map[string]int
	// Distribution 是无保底池的星级分布（rare_type_id→整数权重）。
	// ROSTER350 起取代硬编码 gachaNoPityDistribution，随版本快照固化。
	Distribution map[int]int
	// PityGroup 是该池归属的保底组；""=无保底池（金币池）。
	// ROSTER350 起由快照显式给出，不再按币种/限时推断。
	PityGroup string
}

// gachaPityRules 是保底参数（仅钻石常驻与限时 UP 使用）。
type gachaPityRules struct {
	// Enabled 为假时该池明确公示"无保底"，引擎不推进任何计数。
	Enabled bool `json:"enabled"`
	// URBasePercent 基础五星率（百分点）。
	URBasePercent int `json:"ur_base"`
	// URSoftStart 是软保底起始序号：k<=URSoftStart 用基础率。
	URSoftStart int `json:"soft_start"`
	// URSoftStepPercent 是软保底每步增量（百分点）。
	URSoftStepPercent int `json:"soft_step"`
	// URHardPity 是硬保底序号：k>=URHardPity 时 100%。
	URHardPity int `json:"hard"`
	// SRPity 是"连续未得四星及以上"的强制四星次数（n4 达到该值即强制）。
	SRPity int `json:"sr_pity"`
	// UPGuarantee 为真时启用 UP 保证：无保证态下五星 50% 选 UP 集合。
	UPGuarantee bool `json:"up_guarantee"`
	// NonURWeights 是"未中五星"时非五星星级的归一化权重
	//（rare_type_id→权重；空时按 SR:HR=18:80 的既定默认值）。
	// 可达非五星星级 = 该表正权重键集合。
	NonURWeights map[int]int `json:"non_ur_weights"`
}

// gachaPityGroup 把池归并到保底继承组。同组共享 n4/n5/UP 保证；
// 不同组完全独立（金币池不参与任何保底）。设计 §3.5 要求
// pity_group 稳定且不随 banner 更换而变。
const (
	gachaPityGroupNone      = "" // 无保底池
	gachaPityGroupDiamond   = "cn350-diamond-regular"
	gachaPityGroupLimitedUP = "cn350-limited-up"
)

// gachaRules 是全部规则的根结构。
type gachaRules struct {
	Version string
	Pools   []gachaPoolRules
	// Pity 按 pity_group 索引保底参数。金币池不在表中。
	Pity map[string]gachaPityRules
	// DuplicateCompensation 是满觉醒溢出的星级补偿表（见 gachaPity 无关的
	// 重复卡结算）。首期只允许溢出转 medal（徽章），值按星级配置。
	DuplicateCompensation map[int]gachaDuplicateReward
	// DuplicateMaxCount 是 duplicated_count 的上限（觉醒上限）。
	// 现有读面 inventory_wire.go 以 max_level = base + dup*5 计算上限。
	DuplicateMaxCount int
}

// gachaDuplicateReward 描述重复卡超过觉醒上限后的补偿。
type gachaDuplicateReward struct {
	// Medal 是补偿的徽章数量（写入 medal 余额）。
	Medal int
}

// gachaUnit 是中奖角色的静态定义（来自 cardcatalog，运行时注入）。
type gachaUnit struct {
	UnitID   string
	RarityID int
}

// gachaDefaultRules 已由 ROSTER350-03 移除：规则真源改为已发布版本快照
//（ParseActiveGachaConfig）。测试构造规则请用 GachaSeedPools + 名册桩
// 走 BuildGachaSnapshot→ParseActiveGachaConfig 链路，与生产同路径。

// validNonURRarity 判定稀有度键是否属于"非五星档"（N/R/HR/SR）。
// UR(50) 只能经保底通道产出：non_ur_weights/distribution 混入 50 会绕过
// n5 重置、UP 判定与公示口径；60+ 是超出客户端契约的稀有度。
// 发布校验与快照解析共用同一键域。
func validNonURRarity(r int) bool {
	return r == gachaRarityN || r == gachaRarityR || r == gachaRarityHR || r == gachaRaritySR
}

// effectiveNonURWeights 返回生效的非五星星级权重表：未显式配置时回退既定
// 默认值 {SR:18, HR:80}。引擎抽样、发布可达性校验与公示文案必须共用同一
// 口径——否则"发布期认为 SR/HR 不可达、运行期回退默认却抽不到"会造成
// 校验通过的池在运行时稳定报错（死池）。
func (p gachaPityRules) effectiveNonURWeights() map[int]int {
	if len(p.NonURWeights) > 0 {
		return p.NonURWeights
	}
	return map[int]int{gachaRaritySR: 18, gachaRarityHR: 80}
}

// validate 校验保底参数自洽（发布校验与规则校验共用）。
func (p gachaPityRules) validate() error {
	if !p.Enabled {
		return nil
	}
	if p.URBasePercent <= 0 || p.URBasePercent > 100 {
		return fmt.Errorf("基础五星率 %d 越界", p.URBasePercent)
	}
	if p.URSoftStart < 1 {
		return fmt.Errorf("软保底起点 %d 越界", p.URSoftStart)
	}
	if p.URHardPity <= p.URSoftStart {
		return fmt.Errorf("硬保底 %d 必须大于软保底起点 %d", p.URHardPity, p.URSoftStart)
	}
	if p.SRPity < 1 {
		return fmt.Errorf("四星保证次数 %d 越界", p.SRPity)
	}
	for r, w := range p.NonURWeights {
		if !validNonURRarity(r) {
			return fmt.Errorf("非五星权重稀有度 %d 越界（合法档 10/20/30/40；5星只能走保底通道）", r)
		}
		if w <= 0 {
			return fmt.Errorf("非五星权重 rare=%d w=%d 非正", r, w)
		}
	}
	return nil
}

// pityGroupFor 返回池所属保底组（快照显式给出）；无保底池返回
// gachaPityGroupNone。ROSTER350 起不再按币种/限时推断。
func (r gachaRules) pityGroupFor(pool gachaPoolRules) string {
	return pool.PityGroup
}

// poolByID 按 id 查池。
func (r gachaRules) poolByID(id string) (gachaPoolRules, bool) {
	for _, p := range r.Pools {
		if p.PoolID == id {
			return p, true
		}
	}
	return gachaPoolRules{}, false
}

// slotFor 返回池占据的槽位，用于校验四槽映射不冲突。
func (r gachaRules) slotFor(id string) (int, bool) {
	p, ok := r.poolByID(id)
	if !ok {
		return 0, false
	}
	return p.Slot, true
}

// validate 校验规则自洽。任何一项失败都必须阻止池启用——
// 宁可少一个池，不可下发一张会让客户端 Dictionary.Add 失败
// （同槽重复）或点击越界的池表。
func (r gachaRules) validate() error {
	if r.Version == "" {
		return fmt.Errorf("gacha rules: version 为空")
	}
	slots := map[int]string{}
	listTypes := map[int]string{}
	ids := map[string]bool{}
	for _, p := range r.Pools {
		if p.PoolID == "" {
			return fmt.Errorf("gacha rules: 存在空 PoolID")
		}
		if ids[p.PoolID] {
			return fmt.Errorf("gacha rules: PoolID %q 重复", p.PoolID)
		}
		ids[p.PoolID] = true
		// 客户端 gachaDocuments.Add 会对同槽键二次 Add 抛异常，
		// 因此槽位与 list_type_id 都必须唯一。
		if prev, dup := slots[p.Slot]; dup {
			return fmt.Errorf("gacha rules: 槽%d 被池 %q 与 %q 同时占用", p.Slot, prev, p.PoolID)
		}
		slots[p.Slot] = p.PoolID
		if p.ListTypeID < 0 || p.ListTypeID > 3 {
			return fmt.Errorf("gacha rules: 池 %q list_type_id=%d 越界（客户端 >=4 会全部落到槽1）",
				p.PoolID, p.ListTypeID)
		}
		if p.ListTypeID+1 != p.Slot {
			return fmt.Errorf("gacha rules: 池 %q list_type_id=%d 与槽%d 不一致",
				p.PoolID, p.ListTypeID, p.Slot)
		}
		if prev, dup := listTypes[p.ListTypeID]; dup {
			return fmt.Errorf("gacha rules: list_type_id=%d 被池 %q 与 %q 同时使用",
				p.ListTypeID, prev, p.PoolID)
		}
		listTypes[p.ListTypeID] = p.PoolID
		if p.Slot < 1 || p.Slot > 4 {
			return fmt.Errorf("gacha rules: 池 %q 槽位 %d 越界（原生只有 4 个标签槽）",
				p.PoolID, p.Slot)
		}
		if p.Price.Point < 0 || p.Price.Coin < 0 {
			return fmt.Errorf("gacha rules: 池 %q 价格为负", p.PoolID)
		}
		if p.Price.Point > 0 && p.Price.Coin > 0 {
			return fmt.Errorf("gacha rules: 池 %q 同时配置金币与钻石价格，客户端会只走金币分支",
				p.PoolID)
		}
		if p.Price.Point == 0 && p.Price.Coin == 0 {
			return fmt.Errorf("gacha rules: 池 %q 价格全为 0，客户端会直接禁用按钮（未开放池应如此，但不能与可抽池混淆）",
				p.PoolID)
		}
		if p.PityGroup != gachaPityGroupNone && p.Price.Coin <= 0 {
			// 保底计数器按组共享且由计价币种定义通道：金币（point 计价）
			// 池绑定保底组会把金币消耗写进钻石保底进度——跨池污染。
			return fmt.Errorf("gacha rules: 池 %q 绑定保底组 %q 但非钻石计价（保底计数器只能由钻石池推进）",
				p.PoolID, p.PityGroup)
		}
		if p.PityGroup == gachaPityGroupNone && p.Price.Coin > 0 {
			return fmt.Errorf("gacha rules: 钻石计价池 %q 未配置保底组（钻石池必须有保底）",
				p.PoolID)
		}
		if p.MaxExecutableCount < 1 || p.MaxExecutableCount > 10 {
			return fmt.Errorf("gacha rules: 池 %q max_executable_count=%d 越界（客户端连抽上限 10）",
				p.PoolID, p.MaxExecutableCount)
		}
		if p.MaxExecuteBonus < 0 || p.MaxExecuteBonus > 2 || p.MaxExecutableCount+p.MaxExecuteBonus > 10 {
			return fmt.Errorf("gacha rules: 池 %q max_executable_count=%d max_execute_bonus=%d 越界（总连抽上限 10）",
				p.PoolID, p.MaxExecutableCount, p.MaxExecuteBonus)
		}
		if p.MaxExecuteBonus > 0 && p.MaxExecutableCount < 2 {
			return fmt.Errorf("gacha rules: 池 %q 启用赠送抽(%d)时基准连抽次数须 >=2",
				p.PoolID, p.MaxExecuteBonus)
		}
		if p.Limited {
			if p.BeginAt <= 0 || p.EndAt <= 0 {
				return fmt.Errorf("gacha rules: 限时池 %q 缺时间窗（began_at/ended_at 未配置则客户端与服务端同判未开放，等于占槽死池）",
					p.PoolID)
			}
			if p.EndAt <= p.BeginAt {
				return fmt.Errorf("gacha rules: 限时池 %q ended_at<=began_at", p.PoolID)
			}
		} else if p.BeginAt != 0 || p.EndAt != 0 {
			return fmt.Errorf("gacha rules: 常驻池 %q 不应携带限时时间", p.PoolID)
		}
		if len(p.FeaturedURIDs) > 1 {
			return fmt.Errorf("gacha rules: 池 %q 配置了 %d 个 UP 五星，首期只允许 1 个",
				p.PoolID, len(p.FeaturedURIDs))
		}
		for id, w := range p.RarityWeights {
			if w <= 0 {
				return fmt.Errorf("gacha rules: 池 %q 角色 %q 权重 %d 非正", p.PoolID, id, w)
			}
		}
	}
	for group, pr := range r.Pity {
		if err := pr.validate(); err != nil {
			return fmt.Errorf("gacha rules: 保底组 %q %v", group, err)
		}
	}
	// 每个已启用（价格非零）的池都必须有可解析的保底归属或显式无保底分布。
	for _, p := range r.Pools {
		if p.Price.Point == 0 && p.Price.Coin == 0 {
			continue
		}
		if g := r.pityGroupFor(p); g != gachaPityGroupNone {
			if _, ok := r.Pity[g]; !ok {
				return fmt.Errorf("gacha rules: 池 %q 归属保底组 %q 但未定义参数", p.PoolID, g)
			}
		} else {
			if len(p.Distribution) == 0 {
				return fmt.Errorf("gacha rules: 池 %q 无保底但未配置星级分布", p.PoolID)
			}
			for r, w := range p.Distribution {
				if !validNonURRarity(r) {
					return fmt.Errorf("gacha rules: 池 %q 分布稀有度 %d 越界（合法档 10/20/30/40）",
						p.PoolID, r)
				}
				if w <= 0 {
					return fmt.Errorf("gacha rules: 池 %q 分布权重 rare=%d w=%d 非正", p.PoolID, r, w)
				}
			}
		}
	}
	if r.DuplicateMaxCount < 1 {
		return fmt.Errorf("gacha rules: DuplicateMaxCount=%d 必须为正", r.DuplicateMaxCount)
	}
	return nil
}

func joinNonEmpty(items []string, sep string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}
