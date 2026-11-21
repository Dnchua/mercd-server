// gacha_engine_test.go GACHA350-04 概率保底引擎的确定性边界测试。
//
// 测试哲学（对应计划 §GACHA350-04）：用**注入固定序列**验证全部分支，
// 不要求随机样本"恰好命中 2%"；基础率与含保底综合率分开做统计校验，
// 用预设置信区间而非精确值。
package cn35game

import (
	"math"
	"reflect"
	"testing"
)

// testUnits 构造一个覆盖 N/R/HR/SR/UR 的角色表。
func testUnits() []gachaUnit {
	return []gachaUnit{
		{UnitID: "n1", RarityID: gachaRarityN},
		{UnitID: "n2", RarityID: gachaRarityN},
		{UnitID: "r1", RarityID: gachaRarityR},
		{UnitID: "h1", RarityID: gachaRarityHR},
		{UnitID: "h2", RarityID: gachaRarityHR},
		{UnitID: "s1", RarityID: gachaRaritySR},
		{UnitID: "s2", RarityID: gachaRaritySR},
		{UnitID: "u1", RarityID: gachaRarityUR},
		{UnitID: "u2", RarityID: gachaRarityUR},
	}
}

// newTestEngine 用显式序列构建引擎。
func newTestEngine(t *testing.T, poolID string, units []gachaUnit, seq ...int) *gachaEngine {
	t.Helper()
	rules := gachaDefaultRules()
	eng, err := newGachaEngine(rules, poolID, units, newSequenceGachaRand(seq...))
	if err != nil {
		t.Fatalf("构建引擎失败: %v", err)
	}
	return eng
}

// TestGachaRulesValidateDefault 默认规则必须自洽（四槽唯一、价格合法等）。
func TestGachaRulesValidateDefault(t *testing.T) {
	rules := gachaDefaultRules()
	if err := rules.validate(); err != nil {
		t.Fatalf("默认规则校验失败: %v", err)
	}
	// 四槽齐全且槽号唯一。
	slots := map[int]bool{}
	for _, p := range rules.Pools {
		if slots[p.Slot] {
			t.Fatalf("槽 %d 重复占用", p.Slot)
		}
		slots[p.Slot] = true
	}
	for _, want := range []int{1, 2, 3, 4} {
		if !slots[want] {
			t.Fatalf("槽 %d 未配置池（客户端 4 个标签槽不能有空洞）", want)
		}
	}
}

// TestGachaRulesRejectDuplicateSlot 同槽重复必须被拒绝——
// 客户端 gachaDocuments.Add 会对同键二次 Add 抛异常。
func TestGachaRulesRejectDuplicateSlot(t *testing.T) {
	rules := gachaDefaultRules()
	rules.Pools[1].Slot = rules.Pools[0].Slot
	rules.Pools[1].ListTypeID = rules.Pools[0].ListTypeID
	if err := rules.validate(); err == nil {
		t.Fatal("同槽重复配置未被拒绝")
	}
}

// TestGachaRulesRejectListTypeOutOfRange list_type_id>=4 会被客户端
// 全部映射到槽1，必须拒绝。
func TestGachaRulesRejectListTypeOutOfRange(t *testing.T) {
	rules := gachaDefaultRules()
	rules.Pools[2].ListTypeID = 4
	rules.Pools[2].Slot = 5
	if err := rules.validate(); err == nil {
		t.Fatal("list_type_id 越界未被拒绝")
	}
}

// TestGachaRulesRejectBonus 合法 9+1（十连 9 折）通过；负数或超出 10 次上限必须被拒绝。
func TestGachaRulesRejectBonus(t *testing.T) {
	rules := gachaDefaultRules()
	rules.Pools[1].MaxExecutableCount = 9
	rules.Pools[1].MaxExecuteBonus = 1
	if err := rules.validate(); err != nil {
		t.Fatalf("合法 9+1 配置被拒绝: %v", err)
	}
	rules.Pools[1].MaxExecuteBonus = 2 // 9+2=11 > 10
	if err := rules.validate(); err == nil {
		t.Fatal("max_executable_count+max_execute_bonus=11 未被拒绝")
	}
	rules.Pools[1].MaxExecuteBonus = -1
	if err := rules.validate(); err == nil {
		t.Fatal("负 max_execute_bonus 未被拒绝")
	}
}

// TestURRateCurve 锁定五星率曲线的四个关键点（设计 §3.1 明确要求）。
func TestURRateCurve(t *testing.T) {
	eng := newTestEngine(t, "2", testUnits())
	cases := []struct {
		k    int
		want int
	}{
		{1, 2}, {2, 2}, {49, 2},
		{50, 2}, // 第50次仍 2%
		{51, 4}, // 第51次 4%
		{52, 6},
		{78, 58},
		{79, 60},  // 设计文档明确：第79次 60%
		{80, 100}, // 第80次 100%
		{81, 100},
		{200, 100},
	}
	for _, c := range cases {
		if got := eng.urRatePercent(c.k); got != c.want {
			t.Fatalf("第%d次五星率=%d%% want %d%%", c.k, got, c.want)
		}
	}
}

// TestURHardPityForcesUR 第80抽必须必中五星，且理由是 hard_pity。
func TestURHardPityForcesUR(t *testing.T) {
	// n5=79 → k=80 → 100%。注意引擎仍会消费一次 Next(100) 判定
	// （实现保持统一抽样形状，不为 100% 分支走捷径），故序列首项是判定值，
	// 第二项才是 UR 集合 [u1,u2] 的 Next(2)=1 → u2。
	eng := newTestEngine(t, "2", testUnits(), 0, 1)
	res, err := eng.drawOnce(gachaPityState{N5: 79, N4: 79})
	if err != nil {
		t.Fatalf("硬保底抽取失败: %v", err)
	}
	if res.RarityID != gachaRarityUR {
		t.Fatalf("第80抽稀有度=%d want UR(%d)", res.RarityID, gachaRarityUR)
	}
	if res.Reason != gachaReasonURHardPity {
		t.Fatalf("理由=%q want %q", res.Reason, gachaReasonURHardPity)
	}
	if res.StateAfter.N5 != 0 || res.StateAfter.N4 != 0 {
		t.Fatalf("出五星后计数未清零: n5=%d n4=%d", res.StateAfter.N5, res.StateAfter.N4)
	}
}

// TestURSoftPityRateApplied 第51抽按 4% 判定：
// roll=3 命中（<4），roll=4 不命中（>=4）。
func TestURSoftPityRateApplied(t *testing.T) {
	// 命中：roll=3 → UR；随后抽角色 Next(2)=0。
	eng := newTestEngine(t, "2", testUnits(), 3, 0)
	res, err := eng.drawOnce(gachaPityState{N5: 50, N4: 1})
	if err != nil {
		t.Fatalf("软保底抽取失败: %v", err)
	}
	if res.RarityID != gachaRarityUR {
		t.Fatalf("roll=3 于 4%% 应命中五星，实际 %d", res.RarityID)
	}
	if res.Reason != gachaReasonURSoftPity {
		t.Fatalf("理由=%q want %q", res.Reason, gachaReasonURSoftPity)
	}

	// 不命中：roll=4 → 非五星。n4=8 → n4+1=9 <10，不触发四星保证，
	// 走 SR:HR 归一化（Next(98)）。
	eng2 := newTestEngine(t, "2", testUnits(), 4, 0, 0)
	res2, err := eng2.drawOnce(gachaPityState{N5: 50, N4: 8})
	if err != nil {
		t.Fatalf("软保底未命中分支失败: %v", err)
	}
	if res2.RarityID == gachaRarityUR {
		t.Fatal("roll=4 于 4% 不应命中五星")
	}
	if res2.StateAfter.N5 != 51 {
		t.Fatalf("未中五星后 n5=%d want 51", res2.StateAfter.N5)
	}
}

// TestSRPityForcesSR 第10次（n4=9）未中五星时强制四星。
func TestSRPityForcesSR(t *testing.T) {
	// n5=0 → k=1 → 2%；roll=50 不中五星 → n4=9+1=10 >= 10 强制 SR。
	eng := newTestEngine(t, "2", testUnits(), 50, 0)
	res, err := eng.drawOnce(gachaPityState{N5: 0, N4: 9})
	if err != nil {
		t.Fatalf("四星保证抽取失败: %v", err)
	}
	if res.RarityID != gachaRaritySR {
		t.Fatalf("n4=9 未中五星应强制四星，实际 %d", res.RarityID)
	}
	if res.Reason != gachaReasonSRPity {
		t.Fatalf("理由=%q want %q", res.Reason, gachaReasonSRPity)
	}
	if res.StateAfter.N4 != 0 {
		t.Fatalf("强制四星后 n4=%d want 0", res.StateAfter.N4)
	}
}

// TestSRPityDoesNotOverrideUR 四星保证不得覆盖五星：
// n4=9 且五星命中时，结果必须是五星，且 reason 不是 sr_pity。
func TestSRPityDoesNotOverrideUR(t *testing.T) {
	// k=1 → 2%；roll=1 命中五星 → 在 UR 集合抽 Next(2)=0。
	eng := newTestEngine(t, "2", testUnits(), 1, 0)
	res, err := eng.drawOnce(gachaPityState{N5: 0, N4: 9})
	if err != nil {
		t.Fatalf("双保底同触发抽取失败: %v", err)
	}
	if res.RarityID != gachaRarityUR {
		t.Fatalf("五星命中应优先于四星保证，实际稀有度 %d", res.RarityID)
	}
	if res.Reason == gachaReasonSRPity {
		t.Fatal("五星结果的理由不应是 sr_pity（四星保证不得覆盖五星）")
	}
	if res.StateAfter.N4 != 0 {
		t.Fatalf("出五星后 n4=%d want 0", res.StateAfter.N4)
	}
}

// TestSRHitResetsN4 未到保证点但自然出四星时 n4 归零。
func TestSRHitResetsN4(t *testing.T) {
	// k=1 → 2%；roll=50 不中五星 → n4=3+1=4 <10 → SR:HR Next(98)；
	// roll=0 <18 → SR。
	eng := newTestEngine(t, "2", testUnits(), 50, 0, 0)
	res, err := eng.drawOnce(gachaPityState{N5: 0, N4: 3})
	if err != nil {
		t.Fatalf("自然四星抽取失败: %v", err)
	}
	if res.RarityID != gachaRaritySR {
		t.Fatalf("roll=0 <18/98 应为四星，实际 %d", res.RarityID)
	}
	if res.StateAfter.N4 != 0 {
		t.Fatalf("出四星后 n4=%d want 0", res.StateAfter.N4)
	}
}

// TestHRIncrementsN4 未达四星及以上时 n4 递增。
func TestHRIncrementsN4(t *testing.T) {
	// roll=50 不中五星 → n4 4→5 → SR:HR Next(98)=20 >=18 → HR。
	eng := newTestEngine(t, "2", testUnits(), 50, 20, 0)
	res, err := eng.drawOnce(gachaPityState{N5: 0, N4: 4})
	if err != nil {
		t.Fatalf("HR 抽取失败: %v", err)
	}
	if res.RarityID != gachaRarityHR {
		t.Fatalf("roll=20 >=18/98 应为 HR，实际 %d", res.RarityID)
	}
	if res.StateAfter.N4 != 5 {
		t.Fatalf("未出四星及以上 n4=%d want 5", res.StateAfter.N4)
	}
}

// TestDrawTenSharesState 十连 = 十次单抽状态机，且逐抽立即更新计数。
// 构造：前 9 抽全部 HR（n4 从 0 累到 9），第 10 抽因 n4 到点强制 SR。
func TestDrawTenSharesState(t *testing.T) {
	seq := []int{}
	for i := 0; i < 9; i++ {
		// 每抽：五星判定 Next(100)=50 不中；SR:HR Next(98)=20 → HR；
		// HR 集合 Next(2)=0。
		seq = append(seq, 50, 20, 0)
	}
	// 第 10 抽：五星判定 50 不中 → n4=9+1=10 强制 SR → SR 集合 Next(2)=0。
	seq = append(seq, 50, 0)
	eng := newTestEngine(t, "2", testUnits(), seq...)
	res, end, err := eng.drawMany(gachaPityState{}, 10)
	if err != nil {
		t.Fatalf("十连失败: %v", err)
	}
	if len(res) != 10 {
		t.Fatalf("结果数=%d want 10", len(res))
	}
	for i := 0; i < 9; i++ {
		if res[i].RarityID != gachaRarityHR {
			t.Fatalf("第%d抽应为 HR，实际 %d", i+1, res[i].RarityID)
		}
	}
	if res[9].RarityID != gachaRaritySR {
		t.Fatalf("第10抽应因四星保证为 SR，实际 %d", res[9].RarityID)
	}
	if res[9].Reason != gachaReasonSRPity {
		t.Fatalf("第10抽理由=%q want %q", res[9].Reason, gachaReasonSRPity)
	}
	if end.N4 != 0 {
		t.Fatalf("十连结束 n4=%d want 0（末抽强制四星已重置）", end.N4)
	}
	if end.N5 != 10 {
		t.Fatalf("十连结束 n5=%d want 10", end.N5)
	}
}

// TestDrawManyEqualsSequentialSingle 十连结果必须与逐次单抽完全一致
// （同序列、同起始状态），这是"十连=连续单抽"的机器证明。
func TestDrawManyEqualsSequentialSingle(t *testing.T) {
	mkSeq := func() []int {
		s := []int{}
		// 起始 n4=0：第1—9抽不中五星→HR（n4 递增到 9），
		// 第10抽因 n4 到点强制 SR。
		for i := 0; i < 9; i++ {
			s = append(s, 50, 20, 0) // 不中五星 → HR
		}
		s = append(s, 50, 0) // 第10抽：不中五星 → 强制 SR → SR 集合 Next(2)=0
		return s
	}
	multi := newTestEngine(t, "2", testUnits(), mkSeq()...)
	mres, mend, err := multi.drawMany(gachaPityState{}, 10)
	if err != nil {
		t.Fatalf("十连失败: %v", err)
	}

	single := newTestEngine(t, "2", testUnits(), mkSeq()...)
	cur := gachaPityState{}
	var sres []gachaDrawResult
	for i := 0; i < 10; i++ {
		r, err := single.drawOnce(cur)
		if err != nil {
			t.Fatalf("单抽第%d次失败: %v", i+1, err)
		}
		cur = r.StateAfter
		sres = append(sres, r)
	}
	if len(mres) != len(sres) {
		t.Fatalf("结果数不一致: 十连 %d vs 单抽 %d", len(mres), len(sres))
	}
	for i := range mres {
		if mres[i].UnitID != sres[i].UnitID || mres[i].RarityID != sres[i].RarityID ||
			mres[i].Reason != sres[i].Reason {
			t.Fatalf("第%d抽不一致: 十连 %+v vs 单抽 %+v", i+1, mres[i], sres[i])
		}
	}
	if mend != cur {
		t.Fatalf("结束状态不一致: 十连 %+v vs 单抽 %+v", mend, cur)
	}
}

// TestDrawManyRejectsOverLimit count 超上限必须拒绝（不静默截断）。
func TestDrawManyRejectsOverLimit(t *testing.T) {
	eng := newTestEngine(t, "2", testUnits())
	if _, _, err := eng.drawMany(gachaPityState{}, 11); err == nil {
		t.Fatal("count=11 未被拒绝")
	}
	if _, _, err := eng.drawMany(gachaPityState{}, 0); err == nil {
		t.Fatal("count=0 未被拒绝")
	}
	if _, _, err := eng.drawMany(gachaPityState{}, -1); err == nil {
		t.Fatal("count=-1 未被拒绝")
	}
}

// TestUPGuaranteeFlow 验证 UP 偏斜与保证：
// 非保证态 50/50；命中常驻则设保证；保证态下一五星必中 UP 并清除保证。
func TestUPGuaranteeFlow(t *testing.T) {
	units := testUnits()
	rules := gachaDefaultRules()
	// 给限时池配一个 UP 五星。
	rules.Pools[0].FeaturedURIDs = []string{"u1"}
	// 限时池价格 Coin 已为 5，可执行。
	eng, err := newGachaEngine(rules, "1", units, newSequenceGachaRand(1, 1, 0))
	if err != nil {
		t.Fatalf("构建 UP 引擎失败: %v", err)
	}
	// 第1抽：五星判定 Next(100)=1 <2 命中；UP 50/50 Next(2)=1 → 常驻；
	// 常驻集合=[u2] Next(1)=0 → u2，并设 FeaturedGuaranteed。
	res, err := eng.drawOnce(gachaPityState{})
	if err != nil {
		t.Fatalf("UP 偏斜抽取失败: %v", err)
	}
	if res.UnitID != "u2" {
		t.Fatalf("50/50 命中常驻应为 u2，实际 %q", res.UnitID)
	}
	if !res.StateAfter.FeaturedGuaranteed {
		t.Fatal("命中常驻五星后未设置 UP 保证态")
	}

	// 第2抽：保证态必中 UP。
	eng2, err := newGachaEngine(rules, "1", units,
		newSequenceGachaRand(1, 0))
	if err != nil {
		t.Fatalf("构建 UP 引擎失败: %v", err)
	}
	res2, err := eng2.drawOnce(gachaPityState{FeaturedGuaranteed: true})
	if err != nil {
		t.Fatalf("UP 保证抽取失败: %v", err)
	}
	if res2.UnitID != "u1" {
		t.Fatalf("保证态应必中 UP u1，实际 %q", res2.UnitID)
	}
	if res2.Reason != gachaReasonFeaturedGuar {
		t.Fatalf("保证态理由=%q want %q", res2.Reason, gachaReasonFeaturedGuar)
	}
	if res2.StateAfter.FeaturedGuaranteed {
		t.Fatal("保证态命中后未清除 FeaturedGuaranteed")
	}
}

// TestNoPityPoolFixedDistribution 金币池无保底：计数不推进，
// 且稀有度落在池配置的星级分布档内。
func TestNoPityPoolFixedDistribution(t *testing.T) {
	// 池3 分布 N70/R24/HR5/SR1，总 100。
	// roll=0 → N 档；N 集合 [n1,n2] Next(2)=1 → n2。
	eng := newTestEngine(t, "3", testUnits(), 0, 1)
	res, err := eng.drawOnce(gachaPityState{N4: 7, N5: 33})
	if err != nil {
		t.Fatalf("金币池抽取失败: %v", err)
	}
	if res.RarityID != gachaRarityN {
		t.Fatalf("roll=0 应落入 N 档，实际 %d", res.RarityID)
	}
	if res.UnitID != "n2" {
		t.Fatalf("角色=%q want n2", res.UnitID)
	}
	// 无保底池不得推进计数。
	if res.StateAfter.N4 != 7 || res.StateAfter.N5 != 33 {
		t.Fatalf("无保底池改动了计数: n4=%d n5=%d", res.StateAfter.N4, res.StateAfter.N5)
	}
	if res.Reason != gachaReasonNoPityPool {
		t.Fatalf("理由=%q want %q", res.Reason, gachaReasonNoPityPool)
	}
}

// TestNoPityPoolBoundaries 验证池3/池4 的档位边界。
func TestNoPityPoolBoundaries(t *testing.T) {
	// 池3：N70/R24/HR5/SR1（ROSTER350 1-4★分布）。roll=0/69 → N；
	// 70/93 → R；94/98 → HR；99 → SR。
	cases := []struct {
		roll int
		want int
	}{
		{0, gachaRarityN}, {69, gachaRarityN},
		{70, gachaRarityR}, {93, gachaRarityR},
		{94, gachaRarityHR}, {98, gachaRarityHR},
		{99, gachaRaritySR},
	}
	for _, c := range cases {
		eng := newTestEngine(t, "3", testUnits(), c.roll, 0)
		res, err := eng.drawOnce(gachaPityState{})
		if err != nil {
			t.Fatalf("roll=%d 抽取失败: %v", c.roll, err)
		}
		if res.RarityID != c.want {
			t.Fatalf("池3 roll=%d 稀有度=%d want %d", c.roll, res.RarityID, c.want)
		}
	}
	// 池4：R80/HR18/SR2，总 100。roll=0/79→R；80/97→HR；98/99→SR。
	cases4 := []struct {
		roll int
		want int
	}{
		{0, gachaRarityR}, {79, gachaRarityR},
		{80, gachaRarityHR}, {97, gachaRarityHR},
		{98, gachaRaritySR}, {99, gachaRaritySR},
	}
	for _, c := range cases4 {
		eng := newTestEngine(t, "4", testUnits(), c.roll, 0)
		res, err := eng.drawOnce(gachaPityState{})
		if err != nil {
			t.Fatalf("池4 roll=%d 抽取失败: %v", c.roll, err)
		}
		if res.RarityID != c.want {
			t.Fatalf("池4 roll=%d 稀有度=%d want %d", c.roll, res.RarityID, c.want)
		}
	}
}

// TestEngineRejectsEmptyPool 空集合必须阻止启用（防空中奖）。
func TestEngineRejectsEmptyPool(t *testing.T) {
	if _, err := newGachaEngine(gachaDefaultRules(), "2", nil, newSequenceGachaRand()); err == nil {
		t.Fatal("空角色表未被拒绝")
	}
	// 只有 N 角色时，钻石池应当可以构建（N 档不在候选，但 HR/SR 抽不到会报错）；
	// 这里验证 UP 角色必须在名单内。
	rules := gachaDefaultRules()
	rules.Pools[0].FeaturedURIDs = []string{"不存在"}
	if _, err := newGachaEngine(rules, "1", testUnits(), newSequenceGachaRand()); err == nil {
		t.Fatal("UP 角色不在名单中未被拒绝")
	}
}

// TestEngineRejectsUnopenedPool 价格为零的池不得执行。
func TestEngineRejectsUnopenedPool(t *testing.T) {
	rules := gachaDefaultRules()
	rules.Pools[1].Price = gachaPrice{}
	if _, err := newGachaEngine(rules, "2", testUnits(), newSequenceGachaRand()); err == nil {
		t.Fatal("价格为零的池未被拒绝执行")
	}
}

// TestSequenceRandRejectsOutOfRange 注入序列越界必须报错而不是取模，
// 否则测试会静默漂移到未预期分支。
func TestSequenceRandRejectsOutOfRange(t *testing.T) {
	r := newSequenceGachaRand(5)
	if _, err := r.Next(3); err == nil {
		t.Fatal("越界序列项未被拒绝")
	}
	r2 := newSequenceGachaRand()
	if _, err := r2.Next(10); err == nil {
		t.Fatal("序列耗尽未被拒绝")
	}
}

// TestCryptoRandUnbiasedRange 生产随机源返回值必须落在 [0,n)。
func TestCryptoRandUnbiasedRange(t *testing.T) {
	for i := 0; i < 200; i++ {
		v, err := cryptoGachaRand{}.Next(7)
		if err != nil {
			t.Fatalf("crypto rand 失败: %v", err)
		}
		if v < 0 || v >= 7 {
			t.Fatalf("crypto rand 越界: %d", v)
		}
	}
	if _, err := (cryptoGachaRand{}).Next(0); err == nil {
		t.Fatal("n=0 未被拒绝")
	}
}

// TestLongRunHitRateWithinInterval 基础率统计校验：用 crypto/rand 抽
// 大量样本，五星实际命中率应落在基础率附近（宽松置信区间）。
// 注意：这里刻意**独立**于保底统计，避免把基础率与综合率混为一谈。
func TestLongRunHitRateWithinInterval(t *testing.T) {
	if testing.Short() {
		t.Skip("统计校验在 -short 下跳过")
	}
	eng := newTestEngine(t, "2", testUnits())
	const n = 20000
	hits := 0
	// 每抽都从 n5=0 开始（隔离基础率，不叠加保底）。
	for i := 0; i < n; i++ {
		// 需要真实随机源；用生产源替换注入源。
		realEng, err := newGachaEngine(gachaDefaultRules(), "2", testUnits(), cryptoGachaRand{})
		if err != nil {
			t.Fatalf("构建引擎失败: %v", err)
		}
		res, err := realEng.drawOnce(gachaPityState{})
		if err != nil {
			t.Fatalf("抽取失败: %v", err)
		}
		if res.RarityID == gachaRarityUR {
			hits++
		}
	}
	_ = eng
	p := float64(hits) / float64(n)
	// 基础 2%。二项分布 n=20000，p=0.02，σ≈0.00099；
	// 取 ±5σ ≈ ±0.005 的宽松区间。
	lo, hi := 0.02-0.006, 0.02+0.006
	if p < lo || p > hi {
		t.Fatalf("基础五星率 %.4f 超出区间 [%.3f,%.3f]（n=%d hits=%d）", p, lo, hi, n, hits)
	}
}

// TestPityRecurrenceMatchesSimulation 用精确状态递推求长期综合五星率，
// 再与模拟结果对照（设计 §3.6 要求"先递推、再模拟校验"）。
func TestPityRecurrenceMatchesSimulation(t *testing.T) {
	eng := newTestEngine(t, "2", testUnits())
	// 精确递推：以 n5 为状态的马尔可夫链，求吸收前的期望抽数。
	// E[k] = 1 + (1-p_k)*E[k+1]，E[hardPity]=hardPity 抽封顶。
	hard := eng.pity.URHardPity
	// E[k] 表示"当前已连续 k 次未中五星，还需要多少抽才能出五星"。
	E := make([]float64, hard+2)
	E[hard] = 1.0 // 第 hard 次必中，还需 1 抽
	for k := hard - 1; k >= 0; k-- {
		p := float64(eng.urRatePercent(k+1)) / 100.0
		E[k] = 1.0 + (1.0-p)*E[k+1]
	}
	// 【实测结论，非笔误】期望抽数 34.6 抽，平均 1/34.6 = 2.89%。
	//
	// 为什么"2% 基础率 + 第51抽起提升"会得到 2.89% 而不是接近 2%？
	// 因为**基础率本身已经是 1/50 = 2% 的几何分布**，其自然期望就是 50 抽；
	// 软保底（51 抽起递增）恰好作用在这条长尾上，把期望从 50 抽压到 34.6 抽。
	// 换句话说：50 次内命中本身就是 2% 基础率下的**自然结果**，
	// 而 51—79 的递增补偿的是"本该更久才出"的那部分尾部。
	// 这就是"基础率"与"含保底综合率"必须分开公示的原因——
	// 综合率 2.89% 比基础率 2% 高出 44%，玩家实际体感接近 1/35 抽。
	longRunRate := 1.0 / E[0]
	// 综合率必须显著高于基础率（软保底确实在起作用）。
	if longRunRate <= 0.02 {
		t.Fatalf("含保底综合五星率 %.4f 未高于基础率 2%%", longRunRate)
	}
	// 严格上界：第 80 抽必中 ⇒ 期望抽数 ≤ 80 ⇒ 综合率 ≥ 1.25%。
	// 严格下界：软保底只在第 51 抽起提供额外命中，故综合率不应超过
	// 基础率与硬保底上界的合理组合；这里用 1/30=3.33% 作为保守上界，
	// 超出即说明递推或曲线实现有误。
	if longRunRate < 1.0/float64(hard) {
		t.Fatalf("综合五星率 %.4f 低于硬保底率 %.4f，与硬保底矛盾",
			longRunRate, 1.0/float64(hard))
	}
	if longRunRate > 1.0/30.0 {
		t.Fatalf("综合五星率 %.4f 超过保守上界 3.33%%，曲线实现有误", longRunRate)
	}
	// 期望抽数必须**低于**硬保底次数：硬保底 80 抽是上限，
	// 期望 34.6 抽说明软保底确实提前兑现了大量五星（≤80 才是自洽的）。
	if E[0] > float64(hard) {
		t.Fatalf("期望五星抽数 %.2f 超过硬保底 %d，递推有误", E[0], hard)
	}
	if E[0] < 30 {
		t.Fatalf("期望五星抽数 %.2f 偏低，与 2%%+软保底 不符", E[0])
	}
	t.Logf("含保底综合五星率=%.4f%%（基础 2%%），期望 %.2f 抽", longRunRate*100, E[0])

	// 模拟校验（较少样本，只验证同量级，不做精确断言）。
	if testing.Short() {
		return
	}
	const trials = 3000
	state := gachaPityState{}
	draws, urs := 0, 0
	for i := 0; i < trials; i++ {
		realEng, err := newGachaEngine(gachaDefaultRules(), "2", testUnits(), cryptoGachaRand{})
		if err != nil {
			t.Fatalf("构建引擎失败: %v", err)
		}
		// 一直抽到出五星为止。
		for {
			res, err := realEng.drawOnce(state)
			if err != nil {
				t.Fatalf("抽取失败: %v", err)
			}
			draws++
			state = res.StateAfter
			if res.RarityID == gachaRarityUR {
				urs++
				break
			}
			if draws > trials*hard {
				t.Fatal("模拟未收敛（可能死循环）")
			}
		}
	}
	simRate := float64(urs) / float64(draws)
	// 与递推值同量级校验（宽松 ±30% 相对误差，样本量有限）。
	if math.Abs(simRate-longRunRate)/longRunRate > 0.3 {
		t.Fatalf("模拟综合率 %.4f%% 与递推值 %.4f%% 相差过大", simRate*100, longRunRate*100)
	}
	t.Logf("模拟综合五星率=%.4f%%（%d 抽 / %d 五星）", simRate*100, draws, urs)
}

// TestNonURWeightsSingleSource 非五星档权重的三处口径必须同源：
// 引擎抽样（effectiveNonURWeights）、读面可达集合（gachaPoolReachableRarities）、
// 发布校验（BuildGachaSnapshot reach）。缺省与显式配置各断言一次，
// 防止"发布认为某档不可达而运行期回退默认"的口径分叉回归。
func TestNonURWeightsSingleSource(t *testing.T) {
	rules := gachaDefaultRules()
	pool, ok := rules.poolByID("2")
	if !ok {
		t.Fatal("缺池2")
	}
	// 缺省：可达集合 = {UR,SR,HR}（引擎与读面共认的默认档）。
	reach := gachaPoolReachableRarities(pool, rules)
	want := map[int]bool{gachaRarityUR: true, gachaRaritySR: true, gachaRarityHR: true}
	if !reflect.DeepEqual(reach, want) {
		t.Fatalf("缺省权重可达集合 = %v want %v", reach, want)
	}
	if got := rules.Pity[gachaPityGroupDiamond].effectiveNonURWeights(); len(got) != 2 ||
		got[gachaRaritySR] != 18 || got[gachaRarityHR] != 80 {
		t.Fatalf("缺省非五星权重 = %v want {SR:18,HR:80}", got)
	}
	// 显式配置 N+R 档：三处口径同步收窄。
	rules.Pity[gachaPityGroupDiamond] = gachaPityRules{
		Enabled: true, URBasePercent: 2, URSoftStart: 50,
		URSoftStepPercent: 2, URHardPity: 80, SRPity: 10,
		NonURWeights: map[int]int{gachaRarityN: 50, gachaRarityR: 50},
	}
	reach = gachaPoolReachableRarities(pool, rules)
	want = map[int]bool{gachaRarityUR: true, gachaRarityN: true, gachaRarityR: true}
	if !reflect.DeepEqual(reach, want) {
		t.Fatalf("自定义权重可达集合 = %v want %v", reach, want)
	}
}

// TestPityValidateRejectsOutOfDomainKeys non_ur_weights 键域限定
// 10/20/30/40：50 会绕过保底通道偷出 5★，60+ 超出客户端契约。
func TestPityValidateRejectsOutOfDomainKeys(t *testing.T) {
	base := gachaPityRules{
		Enabled: true, URBasePercent: 2, URSoftStart: 50,
		URSoftStepPercent: 2, URHardPity: 80, SRPity: 10,
	}
	for _, bad := range []int{0, 5, 50, 60, 99} {
		p := base
		p.NonURWeights = map[int]int{bad: 1, gachaRarityHR: 80}
		if err := p.validate(); err == nil {
			t.Errorf("non_ur_weights 键 %d 应被拒绝", bad)
		}
	}
	p := base
	p.NonURWeights = map[int]int{gachaRarityN: 1, gachaRarityR: 1, gachaRarityHR: 1, gachaRaritySR: 1}
	if err := p.validate(); err != nil {
		t.Errorf("合法键域被拒: %v", err)
	}
}

// TestPityRulesVersionNormalizesWeights 保底指纹按**生效**权重归一化：
// NonURWeights 缺省与显式默认档语义相同，指纹必须一致——否则发布一个
// 语义等价的草稿会把全体玩家保底行锁死在"版本不一致→请联系客服"。
func TestPityRulesVersionNormalizesWeights(t *testing.T) {
	base := gachaPityRules{
		Enabled: true, URBasePercent: 2, URSoftStart: 50,
		URSoftStepPercent: 2, URHardPity: 80, SRPity: 10,
	}
	nilVersion := PityRulesVersion(base)
	base.NonURWeights = map[int]int{gachaRaritySR: 18, gachaRarityHR: 80}
	if got := PityRulesVersion(base); got != nilVersion {
		t.Fatalf("显式默认档指纹=%s 应等于缺省档 %s", got, nilVersion)
	}
	base.NonURWeights = map[int]int{gachaRaritySR: 20, gachaRarityHR: 80}
	if got := PityRulesVersion(base); got == nilVersion {
		t.Fatal("权重变化应产生不同指纹")
	}
	base.NonURWeights = map[int]int{gachaRarityN: 50, gachaRarityR: 50}
	if got := PityRulesVersion(base); got == nilVersion {
		t.Fatal("档位变化应产生不同指纹")
	}
}

// TestRulesValidateRejectsPityOnPointPool 保底组只能由钻石（coin 计价）
// 池绑定：金币池混入会把金币消耗写进共享保底进度（跨池污染）。
// 反向同样拒绝：钻石计价池必须有保底组。
func TestRulesValidateRejectsPityOnPointPool(t *testing.T) {
	rules := gachaDefaultRules()
	for i := range rules.Pools {
		if rules.Pools[i].PoolID == "3" {
			rules.Pools[i].PityGroup = gachaPityGroupDiamond
		}
	}
	if err := rules.validate(); err == nil {
		t.Fatal("金币池绑定保底组应被拒绝")
	}
	rules = gachaDefaultRules()
	for i := range rules.Pools {
		if rules.Pools[i].PoolID == "2" {
			rules.Pools[i].PityGroup = gachaPityGroupNone
		}
	}
	if err := rules.validate(); err == nil {
		t.Fatal("钻石计价池缺保底组应被拒绝")
	}
}

// TestRulesValidateRejectsLimitedNoWindow 启用限时池缺时间窗 =
// 永远未开放的占槽死池（gachaPoolOpen 对 begin/end<=0 恒判未开放）。
func TestRulesValidateRejectsLimitedNoWindow(t *testing.T) {
	rules := gachaDefaultRules()
	for i := range rules.Pools {
		if rules.Pools[i].PoolID == "1" {
			rules.Pools[i].BeginAt, rules.Pools[i].EndAt = 0, 0
		}
	}
	if err := rules.validate(); err == nil {
		t.Fatal("限时池缺时间窗应被拒绝")
	}
}

// TestEngineRejectsPityGroupOnPointPool 执行边界兜底：即使上游校验被
// 绕过，非钻石计价池绑定保底组也在建池期 fail-closed。
func TestEngineRejectsPityGroupOnPointPool(t *testing.T) {
	rules := gachaDefaultRules()
	for i := range rules.Pools {
		if rules.Pools[i].PoolID == "3" {
			rules.Pools[i].PityGroup = gachaPityGroupDiamond
		}
	}
	units := []gachaUnit{{UnitID: "100", RarityID: gachaRarityHR}}
	if _, err := newGachaEngine(rules, "3", units, newSequenceGachaRand(1)); err == nil {
		t.Fatal("金币池绑定保底组应在建池期报错")
	}
}

// TestRulesValidateRejectsBadDistribution 无保底池 distribution 键域
// 同样限定非五星档（快照解析期 fail-closed）。
func TestRulesValidateRejectsBadDistribution(t *testing.T) {
	for _, bad := range []int{50, 60, 0} {
		rules := gachaDefaultRules()
		for i := range rules.Pools {
			if rules.Pools[i].PoolID == "3" {
				rules.Pools[i].Distribution = map[int]int{bad: 100}
			}
		}
		if err := rules.validate(); err == nil {
			t.Errorf("distribution 键 %d 应被拒绝", bad)
		}
	}
}
