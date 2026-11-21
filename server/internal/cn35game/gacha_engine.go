// gacha_engine.go CN3.5 抽卡概率与保底引擎（GACHA350-04）。
//
// 设计原则（对应 notes/proposed/2026-09-25-cn35-gacha-restoration.md §3）：
//
//  1. 十连不是一次抽样，而是**十次连续单抽**，每次立即更新计数。
//     单抽与十连共享同一份保底进度。
//  2. 先判五星，未中再在非五星里按 SR:HR 重新归一化判四星；
//     四星保证**不覆盖**五星（n4 到点但先中了五星，则五星优先且 n4 重置）。
//  3. 随机源由调用方注入（gachaRand）。生产用 crypto/rand 的无偏整数抽样；
//     测试注入固定序列。生产**绝不接受客户端随机种子或中奖角色**。
//  4. 本文件不碰数据库、不碰 HTTP——纯函数式状态推进，便于确定性测试。
package cn35game

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sort"
)

// gachaRand 是无偏整数抽样接口。Next(n) 返回 [0,n) 的整数，n<=0 视为错误。
type gachaRand interface {
	Next(n int) (int, error)
}

// cryptoGachaRand 生产随机源：crypto/rand 无偏抽样。
// 不用 math/rand：抽卡是经济行为，需要不可预测且无模偏差。
type cryptoGachaRand struct{}

func (cryptoGachaRand) Next(n int) (int, error) {
	if n <= 0 {
		return 0, fmt.Errorf("gacha rand: n=%d 必须为正", n)
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, fmt.Errorf("gacha rand: %w", err)
	}
	return int(v.Int64()), nil
}

// sequenceGachaRand 测试用固定序列源。序列耗尽即报错（不循环，
// 避免测试因为静默复用序列而误判）。
type sequenceGachaRand struct {
	seq []int
	pos int
}

func newSequenceGachaRand(seq ...int) *sequenceGachaRand {
	return &sequenceGachaRand{seq: seq}
}

func (s *sequenceGachaRand) Next(n int) (int, error) {
	if n <= 0 {
		return 0, fmt.Errorf("gacha rand: n=%d 必须为正", n)
	}
	if s.pos >= len(s.seq) {
		return 0, fmt.Errorf("gacha rand: 固定序列耗尽（已用 %d 项）", s.pos)
	}
	v := s.seq[s.pos]
	s.pos++
	if v < 0 || v >= n {
		// 越界值会让"注入序列"失去可控性，直接失败而不是取模。
		return 0, fmt.Errorf("gacha rand: 固定序列第%d项 %d 越出 [0,%d)", s.pos, v, n)
	}
	return v, nil
}

// gachaPityState 是某个保底组的持久化进度。
// 字段名与 cn35_gacha_pity 表列一一对应。
type gachaPityState struct {
	// N4 是连续未获得四星及以上（SR/UR）的次数。
	N4 int
	// N5 是连续未获得五星的次数。
	N5 int
	// FeaturedGuaranteed 为真表示下一个五星必定是 UP（"大保底"）。
	FeaturedGuaranteed bool
}

// gachaDrawReason 记录本次抽取是否由保底触发，用于审计与公示核对。
type gachaDrawReason string

const (
	gachaReasonNormal       gachaDrawReason = "normal"
	gachaReasonURSoftPity   gachaDrawReason = "ur_soft_pity"
	gachaReasonURHardPity   gachaDrawReason = "ur_hard_pity"
	gachaReasonSRPity       gachaDrawReason = "sr_pity"
	gachaReasonFeaturedUP   gachaDrawReason = "featured_up"
	gachaReasonFeaturedGuar gachaDrawReason = "featured_guaranteed"
	gachaReasonNoPityPool   gachaDrawReason = "no_pity_pool"
)

// gachaPoolPools 是按稀有度分桶的可中奖角色集合。
// 由 cardcatalog 解析而来；空桶表示该池不下发该稀有度。
type gachaPoolPools struct {
	byRarity map[int][]string
	// featuredUR 是 UP 五星集合（可空）。
	featuredUR []string
}

// newGachaPoolPools 从池规则与角色表构建分桶。
func newGachaPoolPools(pool gachaPoolRules, units []gachaUnit) (gachaPoolPools, error) {
	byRarity := map[int][]string{}
	rarityOf := map[string]int{}
	for _, u := range units {
		if u.UnitID == "" {
			return gachaPoolPools{}, fmt.Errorf("gacha engine: 角色表含空 UnitID")
		}
		if _, dup := rarityOf[u.UnitID]; dup {
			return gachaPoolPools{}, fmt.Errorf("gacha engine: 角色 %q 在池 %s 中重复", u.UnitID, pool.PoolID)
		}
		rarityOf[u.UnitID] = u.RarityID
		byRarity[u.RarityID] = append(byRarity[u.RarityID], u.UnitID)
	}
	// 确定性排序：随机抽样必须在稳定序列上做，否则同一种子不同进程
	// 会得到不同结果，测试与审计失去意义。
	for k := range byRarity {
		sort.Strings(byRarity[k])
	}
	featured := make([]string, 0, len(pool.FeaturedURIDs))
	for _, id := range pool.FeaturedURIDs {
		r, ok := rarityOf[id]
		if !ok {
			return gachaPoolPools{}, fmt.Errorf("gacha engine: 池 %s 的 UP 角色 %q 不在可获得名单中", pool.PoolID, id)
		}
		if r != gachaRarityUR {
			return gachaPoolPools{}, fmt.Errorf("gacha engine: 池 %s 的 UP 角色 %q 稀有度 %d 不是五星", pool.PoolID, id, r)
		}
		featured = append(featured, id)
	}
	// 权重校验：配了权重就必须覆盖该池全部角色（防空中奖/漏配）。
	if len(pool.RarityWeights) > 0 {
		for _, ids := range byRarity {
			for _, id := range ids {
				if _, ok := pool.RarityWeights[id]; !ok {
					return gachaPoolPools{}, fmt.Errorf("gacha engine: 池 %s 角色 %q 缺少权重配置", pool.PoolID, id)
				}
			}
		}
	}
	return gachaPoolPools{byRarity: byRarity, featuredUR: featured}, nil
}

// ids 返回某稀有度的候选集合（可能为空）。
func (p gachaPoolPools) ids(rarity int) []string {
	return p.byRarity[rarity]
}

// isEmpty 表示该池没有任何可获得角色——必须阻止启用，否则玩家抽到空结果。
func (p gachaPoolPools) isEmpty() bool {
	for _, ids := range p.byRarity {
		if len(ids) > 0 {
			return false
		}
	}
	return true
}

// gachaDrawResult 是单次抽取的结果，携带审计所需的全部信息。
type gachaDrawResult struct {
	UnitID   string
	RarityID int
	Reason   gachaDrawReason
	// StateAfter 是推进后的保底状态。
	StateAfter gachaPityState
}

type gachaEngine struct {
	rules   gachaRules
	pool    gachaPoolRules
	pools   gachaPoolPools
	pity    gachaPityRules
	group   string
	weights map[string]int
	rand    gachaRand
}

func newGachaEngine(rules gachaRules, poolID string, units []gachaUnit, rng gachaRand) (*gachaEngine, error) {
	pool, ok := rules.poolByID(poolID)
	if !ok {
		return nil, fmt.Errorf("gacha engine: 未知池 %q", poolID)
	}
	if pool.Price.Point == 0 && pool.Price.Coin == 0 {
		return nil, fmt.Errorf("gacha engine: 池 %q 未开放（价格为零）", poolID)
	}
	pools, err := newGachaPoolPools(pool, units)
	if err != nil {
		return nil, err
	}
	if pools.isEmpty() {
		return nil, fmt.Errorf("gacha engine: 池 %q 没有任何可获得角色，禁止执行", poolID)
	}
	group := rules.pityGroupFor(pool)
	if group != gachaPityGroupNone && pool.Price.Coin <= 0 {
		// 执行边界兜底：非钻石计价池绑定保底组会把其他币种的消耗
		// 写进共享保底计数——即使上游校验被绕过也在此 fail-closed。
		return nil, fmt.Errorf("gacha engine: 非钻石计价池 %q 不得绑定保底组 %q", poolID, group)
	}
	if group == gachaPityGroupNone && pool.Price.Coin > 0 {
		return nil, fmt.Errorf("gacha engine: 钻石计价池 %q 未配置保底组", poolID)
	}
	var pity gachaPityRules
	if group != gachaPityGroupNone {
		p, ok := rules.Pity[group]
		if !ok {
			return nil, fmt.Errorf("gacha engine: 池 %q 的保底组 %q 未定义", poolID, group)
		}
		pity = p
	}
	if rng == nil {
		rng = cryptoGachaRand{}
	}
	return &gachaEngine{
		rules: rules, pool: pool, pools: pools, pity: pity, group: group,
		weights: pool.RarityWeights, rand: rng,
	}, nil
}

// pityGroup 返回该引擎的保底组（金币池为 gachaPityGroupNone）。
func (e *gachaEngine) pityGroup() string { return e.group }

// urRatePercent 返回第 k 次（k=n5+1）抽取的五星率（百分点）。
//
//	k<=softStart        → base
//	softStart<k<hardPity → base + step*(k-softStart)，上限 100
//	k>=hardPity         → 100
//
// 以默认参数即：第50次 2%、第51次 4%、第79次 60%、第80次 100%。
func (e *gachaEngine) urRatePercent(k int) int {
	if !e.pity.Enabled {
		return 0
	}
	if k >= e.pity.URHardPity {
		return 100
	}
	if k <= e.pity.URSoftStart {
		return e.pity.URBasePercent
	}
	rate := e.pity.URBasePercent + e.pity.URSoftStepPercent*(k-e.pity.URSoftStart)
	if rate > 100 {
		rate = 100
	}
	return rate
}

// pickWeighted 在候选集合内按权重抽样（无权重配置时等权）。
func (e *gachaEngine) pickWeighted(candidates []string) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("gacha engine: 空候选集合")
	}
	if len(e.weights) == 0 {
		idx, err := e.rand.Next(len(candidates))
		if err != nil {
			return "", err
		}
		return candidates[idx], nil
	}
	total := 0
	for _, id := range candidates {
		total += e.weights[id]
	}
	if total <= 0 {
		return "", fmt.Errorf("gacha engine: 候选集合权重总和为 0")
	}
	roll, err := e.rand.Next(total)
	if err != nil {
		return "", err
	}
	acc := 0
	for _, id := range candidates {
		acc += e.weights[id]
		if roll < acc {
			return id, nil
		}
	}
	// 理论不可达（roll<total）；保守返回最后一个而非报错掩盖问题。
	return candidates[len(candidates)-1], nil
}

// drawOnce 在给定保底状态上执行一次抽取，返回结果与新状态。
// 这是单抽与十连共用的唯一推进路径。
func (e *gachaEngine) drawOnce(state gachaPityState) (gachaDrawResult, error) {
	if !e.pity.Enabled {
		// 无保底池：固定比例直接抽样，不推进任何计数。
		res, err := e.drawWithoutPity()
		if err != nil {
			return gachaDrawResult{}, err
		}
		res.StateAfter = state // 保持不变
		return res, nil
	}

	next := state
	// 先判五星。
	k := state.N5 + 1
	urRate := e.urRatePercent(k)
	roll, err := e.rand.Next(100)
	if err != nil {
		return gachaDrawResult{}, err
	}
	if roll < urRate {
		reason := gachaReasonNormal
		switch {
		case k >= e.pity.URHardPity:
			reason = gachaReasonURHardPity
		case k > e.pity.URSoftStart:
			reason = gachaReasonURSoftPity
		}
		unitID, upReason, guaranteedAfter, err := e.pickUR(next)
		if err != nil {
			return gachaDrawResult{}, err
		}
		if upReason != "" {
			reason = upReason
		}
		// 出五星：n5 与 n4 都重置（五星属"四星及以上"，故重置 n4）。
		next.N5 = 0
		next.N4 = 0
		// UP 保证态随之更新：吃到"歪"就设保证，吃到 UP 就清除。
		next.FeaturedGuaranteed = guaranteedAfter
		return gachaDrawResult{
			UnitID: unitID, RarityID: gachaRarityUR,
			Reason: reason, StateAfter: next,
		}, nil
	}

	// 未中五星：n5 递增。
	next.N5 = state.N5 + 1

	// 再看四星保证：n4 到点则强制"四星及以上"（注意不覆盖五星，此处已确认
	// 未中五星）。候选 = NonURWeights 中稀有度>=SR 的档，按权重归一化。
	if next.N4+1 >= e.pity.SRPity {
		unitID, rarity, err := e.pickNonUR(true)
		if err != nil {
			return gachaDrawResult{}, err
		}
		next.N4 = 0
		return gachaDrawResult{
			UnitID: unitID, RarityID: rarity,
			Reason: gachaReasonSRPity, StateAfter: next,
		}, nil
	}

	// 普通非五星：按 NonURWeights（默认 SR:HR=18:80）归一化抽样。
	unitID, rarity, err := e.pickNonUR(false)
	if err != nil {
		return gachaDrawResult{}, err
	}
	if rarity >= gachaRaritySR {
		next.N4 = 0 // 四星及以上重置 n4
	} else {
		next.N4 = state.N4 + 1 // 未达四星及以上，n4 递增
	}
	return gachaDrawResult{
		UnitID: unitID, RarityID: rarity,
		Reason: gachaReasonNormal, StateAfter: next,
	}, nil
}

// pickNonUR 在非五星星级里按权重抽样后在档内按成员权重取角色。
// onlySRPlus=true 时只在稀有度>=SR 的档中抽（四星保证路径）。
// 权重表与发布校验/公示共用 gachaPityRules.effectiveNonURWeights 口径。
func (e *gachaEngine) pickNonUR(onlySRPlus bool) (string, int, error) {
	weights := e.pity.effectiveNonURWeights()
	rarities := make([]int, 0, len(weights))
	for r, w := range weights {
		if w <= 0 {
			continue
		}
		if onlySRPlus && r < gachaRaritySR {
			continue
		}
		if len(e.pools.ids(r)) == 0 {
			continue // 该档池内无候选角色，跳过（不吞权重报错）
		}
		rarities = append(rarities, r)
	}
	if len(rarities) == 0 {
		return "", 0, fmt.Errorf("gacha engine: 池 %q 非五星候选为空（onlySRPlus=%v）",
			e.pool.PoolID, onlySRPlus)
	}
	if len(rarities) == 1 {
		// 单档时不消耗随机序列（强制四星路径与旧口径的掷点序列一致）。
		unitID, err := e.pickWeighted(e.pools.ids(rarities[0]))
		if err != nil {
			return "", 0, err
		}
		return unitID, rarities[0], nil
	}
	// 高稀有度先累计：roll 落在低段命中高档（与旧口径 SR 先累计一致）。
	sort.Sort(sort.Reverse(sort.IntSlice(rarities)))
	total := 0
	for _, r := range rarities {
		total += weights[r]
	}
	roll, err := e.rand.Next(total)
	if err != nil {
		return "", 0, err
	}
	acc := 0
	rarity := rarities[len(rarities)-1]
	for _, r := range rarities {
		acc += weights[r]
		if roll < acc {
			rarity = r
			break
		}
	}
	unitID, err := e.pickWeighted(e.pools.ids(rarity))
	if err != nil {
		return "", 0, err
	}
	return unitID, rarity, nil
}

// drawWithoutPity 处理无保底池：固定星级分布抽样，不读也不写计数。
func (e *gachaEngine) drawWithoutPity() (gachaDrawResult, error) {
	// ROSTER350 起星级分布随版本快照下发（池规则 Distribution 字段），
	// 不再硬编码 per-pool。缺档自动按剩余档归一化由调用方发布校验保证。
	weights := e.pool.Distribution
	total := 0
	for _, w := range weights {
		total += w
	}
	if total <= 0 {
		return gachaDrawResult{}, fmt.Errorf("gacha engine: 池 %q 无保底分布为空", e.pool.PoolID)
	}
	roll, err := e.rand.Next(total)
	if err != nil {
		return gachaDrawResult{}, err
	}
	acc := 0
	rarity := 0
	// 按稀有度升序稳定遍历，保证序列注入可预测。
	for _, r := range []int{gachaRarityN, gachaRarityR, gachaRarityHR, gachaRaritySR, gachaRarityUR} {
		w := weights[r]
		if w == 0 {
			continue
		}
		acc += w
		if roll < acc {
			rarity = r
			break
		}
	}
	if rarity == 0 {
		return gachaDrawResult{}, fmt.Errorf("gacha engine: 池 %q 稀有度抽样失败", e.pool.PoolID)
	}
	pool := e.pools.ids(rarity)
	if len(pool) == 0 {
		return gachaDrawResult{}, fmt.Errorf("gacha engine: 池 %q 稀有度 %d 无候选角色", e.pool.PoolID, rarity)
	}
	unitID, err := e.pickWeighted(pool)
	if err != nil {
		return gachaDrawResult{}, err
	}
	return gachaDrawResult{
		UnitID: unitID, RarityID: rarity, Reason: gachaReasonNoPityPool,
	}, nil
}

// pickSR / pickHR 已由 pickNonUR 归一化取代（ROSTER350-03 起星级权重随
// 版本快照下发，不再硬编码档位）。

// pickUR 抽取五星。返回选中的角色、UP 相关理由，以及**本次抽取之后**的
// UP 保证态（调用方必须把它写回保底状态）。
//
// UP 规则（设计 §3.4）：先抽稀有度再抽集合。
//   - 无 UP 集合：直接从全部五星中抽，理由为空，保证态不变。
//   - 有 UP 集合且处于保证态：必选 UP，并**清除**保证。
//   - 有 UP 集合且非保证态：50% 选 UP 集合，50% 选排除 UP 后的常驻五星；
//     命中常驻则**设置** FeaturedGuaranteed=true。
func (e *gachaEngine) pickUR(state gachaPityState) (string, gachaDrawReason, bool, error) {
	all := e.pools.ids(gachaRarityUR)
	if len(all) == 0 {
		return "", "", state.FeaturedGuaranteed, fmt.Errorf("gacha engine: 池 %q 无五星角色", e.pool.PoolID)
	}
	if len(e.pools.featuredUR) == 0 || !e.pity.UPGuarantee {
		// 非 UP 池：保证态不适用，保持原值（应为 false）。
		id, err := e.pickWeighted(all)
		return id, "", state.FeaturedGuaranteed, err
	}
	if state.FeaturedGuaranteed {
		// 保证态：必中 UP，清除保证。
		id, err := e.pickWeighted(e.pools.featuredUR)
		if err != nil {
			return "", "", state.FeaturedGuaranteed, err
		}
		return id, gachaReasonFeaturedGuar, false, nil
	}
	// 非保证态：50/50。为保证可注入，用 Next(2)。
	roll, err := e.rand.Next(2)
	if err != nil {
		return "", "", state.FeaturedGuaranteed, err
	}
	if roll == 0 {
		id, err := e.pickWeighted(e.pools.featuredUR)
		return id, gachaReasonFeaturedUP, false, err
	}
	// 常驻五星 = 全部五星排除 UP 集合。
	nonFeatured := make([]string, 0, len(all))
	featuredSet := map[string]bool{}
	for _, id := range e.pools.featuredUR {
		featuredSet[id] = true
	}
	for _, id := range all {
		if !featuredSet[id] {
			nonFeatured = append(nonFeatured, id)
		}
	}
	if len(nonFeatured) == 0 {
		// UP 集合覆盖了全部五星：无法"歪"，直接给 UP 且不设保证。
		id, err := e.pickWeighted(e.pools.featuredUR)
		return id, gachaReasonFeaturedUP, false, err
	}
	id, err := e.pickWeighted(nonFeatured)
	// 吃到"歪"：下一次五星必中 UP（大保底）。
	return id, "", true, err
}

// drawMany 连续执行 count 次抽取（十连 = 十次单抽状态机）。
// 返回按**服务端原始顺序**排列的结果，客户端结果页会按稀有度重排，
// 保底计算必须依赖本顺序（设计 §1 已指出结果页排序非抽取顺序）。
func (e *gachaEngine) drawMany(state gachaPityState, count int) ([]gachaDrawResult, gachaPityState, error) {
	if count < 1 {
		return nil, state, fmt.Errorf("gacha engine: count=%d 必须为正", count)
	}
	maxDraws := e.pool.MaxExecutableCount + e.pool.MaxExecuteBonus
	if count > maxDraws {
		return nil, state, fmt.Errorf("gacha engine: count=%d 超出池 %q 上限 %d",
			count, e.pool.PoolID, maxDraws)
	}
	out := make([]gachaDrawResult, 0, count)
	cur := state
	for i := 0; i < count; i++ {
		res, err := e.drawOnce(cur)
		if err != nil {
			return nil, state, err
		}
		cur = res.StateAfter
		out = append(out, res)
	}
	return out, cur, nil
}

// totalCost 返回 count 次抽取的总价（客户端连抽按 单价×count 显示）。
func (e *gachaEngine) totalCost(count int) gachaPrice {
	if e.pool.Price.Point > 0 {
		return gachaPrice{Point: e.pool.Price.Point * count}
	}
	return gachaPrice{Coin: e.pool.Price.Coin * count}
}

// primaryCurrency 返回该池的结算币种（余额表 currency 列值）。
func (e *gachaEngine) primaryCurrency() string {
	if e.pool.Price.Point > 0 {
		return gachaCurrencyPoint
	}
	return gachaCurrencyCoin
}
