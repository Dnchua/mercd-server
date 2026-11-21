// gacha_wire_test.go GACHA350-05 抽卡协议回放测试。
//
// 真实 SQLite + cardcatalog + **加密 game 通道**（复用 establishSession/
// gameJSON），覆盖：
//   - 真实结算：扣费/发卡/逐抽/流水/保底/操作记录同事务 + 响应契约字段
//   - 有界重放：传输层窗口重发回放、reload_data 信号重试回放、
//     合法再抽正常扣费、并发同参只结算一次
//   - 余额不足/非法参数（零/负数/超限/未知池/非数字/票券/未开放限时池）
//     一律无副作用
//   - 客户端不得通过任何参数指定中奖角色
//   - 页面读面池列表形状、四槽映射唯一性、价格与规则同源
//   - /users/reload_data 键集合
package cn35game_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35protocol"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

// idStr 把下发的池 id（JSON number，客户端 i()/s() 双读）转回规则层
// string 键用于断言比对。
func idStr(v any) string {
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return t
	default:
		return fmt.Sprint(v)
	}
}

// gachaTestServer 返回已注册抽卡路由的测试服务地址与库。
func gachaTestServer(t *testing.T) (string, *cn35store.Store) {
	t.Helper()
	ts, st := trainingServer(t, t.TempDir())
	return ts.URL, st
}

// TestGachaExecuteSettles 真实结算主断言：扣费、发卡/累觉、逐抽记录、
// 流水、保底推进、操作记录同事务落地，响应携带客户端契约字段。
func TestGachaExecuteSettles(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-settle")
	setCoin(t, st, uid, 100)

	resp := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=2&count=1", "_method=GET")
	if resp["status"] != "success" {
		t.Fatalf("结算应成功，实际 %v", resp)
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺 data: %v", resp)
	}
	// 客户端契约：temp_gacha_result Count>0 才进 GachaResult 页；
	// 每项 id/rare_type_id/level/max_level 是渲染必需字段。
	res, ok := data["temp_gacha_result"].([]any)
	if !ok || len(res) != 1 {
		t.Fatalf("temp_gacha_result 应有 1 项: %v", data["temp_gacha_result"])
	}
	entry := res[0].(map[string]any)
	for _, k := range []string{"id", "rare_type_id", "level", "max_level",
		"main_name", "rare_type_name", "duplicated_count",
		"bonus_sp", "skill_slot_count", "newcomer", "awake"} {
		if _, ok := entry[k]; !ok {
			t.Fatalf("结果项缺客户端消费字段 %s: %v", k, entry)
		}
	}
	// 新卡契约（UnitListRow isScoutResult）：newcomer=true→New 角标，
	// awake=false→不显示觉醒前后栏。
	if entry["newcomer"] != true || entry["awake"] != false {
		t.Fatalf("新卡角标字段错误: newcomer=%v awake=%v", entry["newcomer"], entry["awake"])
	}
	coin, ok := data["user_coin"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺 user_coin（客户端据此更新余额）: %v", data)
	}
	if got, _ := asFloat(coin["amount"]); got != 95 {
		t.Fatalf("user_coin.amount=%v want 95（扣 5 钻）", coin["amount"])
	}

	// 持久化断言：余额只扣一次，四表各有记录。
	if got := coinBalanceOf(t, st, uid); got != 95 {
		t.Fatalf("coin=%d want 95", got)
	}
	if n := gachaOpRows(t, st, uid); n != 1 {
		t.Fatalf("操作记录=%d want 1", n)
	}
	if n := gachaDrawRows(t, st, uid); n != 1 {
		t.Fatalf("逐抽记录=%d want 1", n)
	}
	if n := gachaLedgerRows(t, st, uid); n < 1 {
		t.Fatalf("货币流水=%d want >=1", n)
	}
	// 钻石池属 cn350-diamond-regular 保底组：结算后必须有进度行。
	if n := gachaPityRows(t, st, uid); n != 1 {
		t.Fatalf("保底进度行=%d want 1", n)
	}
}

// TestGachaExecuteDuplicateAwakeContract 重复卡招募结果契约：
// awake=true 且带 before_state（觉醒前文档），客户端据此并排渲染
// 觉醒等级/等级上限/SP加成/符文孔的 before→after（含封顶同值）。
// 方法：把全部 catalog 单位预置为 dup=1 库存，则每次抽取必为重复卡。
func TestGachaExecuteDuplicateAwakeContract(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-awake")
	setGachaPoint(t, st, uid, 100000)

	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("加载 catalog: %v", err)
	}
	if err := st.WithTx(func(tx *sql.Tx) error {
		for _, id := range cat.UnitIDs() {
			if err := cn35store.InsertUnitTx(tx, uid, cn35game.UnitRecord{
				UnitID: id, Level: 10, DuplicatedCount: 1,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("预置库存失败: %v", err)
	}

	resp := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=3&count=10", "_method=GET")
	if resp["status"] != "success" {
		t.Fatalf("结算应成功，实际 %v", resp)
	}
	res, ok := resp["data"].(map[string]any)["temp_gacha_result"].([]any)
	if !ok || len(res) != 10 {
		t.Fatalf("temp_gacha_result 应有 10 项: %v", resp["data"])
	}
	for i, raw := range res {
		e := raw.(map[string]any)
		if e["awake"] != true || e["newcomer"] != false {
			t.Fatalf("第 %d 项应为觉醒重复卡: %v", i, e)
		}
		before, ok := e["before_state"].(map[string]any)
		if !ok {
			t.Fatalf("第 %d 项缺 before_state: %v", i, e)
		}
		bDup, _ := asFloat(before["duplicated_count"])
		aDup, _ := asFloat(e["duplicated_count"])
		if bDup < 1 || aDup != bDup+1 {
			t.Fatalf("第 %d 项觉醒前后异常: before=%v after=%v", i, bDup, aDup)
		}
		// 等级上限同步 +5；SP加成 = 20*rare*(dup+5)。
		bMax, _ := asFloat(before["max_level"])
		aMax, _ := asFloat(e["max_level"])
		if aMax != bMax+5 {
			t.Fatalf("第 %d 项等级上限变化异常: before=%v after=%v", i, bMax, aMax)
		}
		rare, _ := asFloat(e["rare_type_count"])
		sp, _ := asFloat(e["bonus_sp"])
		if want := 20 * rare * (aDup + 5); sp != want {
			t.Fatalf("第 %d 项 SP加成=%v want %v", i, sp, want)
		}
		if _, ok := e["skill_slot_count"]; !ok {
			t.Fatalf("第 %d 项缺 skill_slot_count", i)
		}
	}
}

// TestGachaExecuteCappedDuplicateContract 满觉醒重复卡：delta=0、
// 补偿入账，结果行 awake=true 且 before/after 同值（封顶如实展示）。
func TestGachaExecuteCappedDuplicateContract(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-capped")
	setGachaPoint(t, st, uid, 100000)
	goldBefore := gachaBalanceOf(t, st, uid)
	medalBefore := medalBalanceOf(t, st, uid)

	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("加载 catalog: %v", err)
	}
	if err := st.WithTx(func(tx *sql.Tx) error {
		for _, id := range cat.UnitIDs() {
			u, uerr := cat.Unit(id)
			if uerr != nil {
				return uerr
			}
			if err := cn35store.InsertUnitTx(tx, uid, cn35game.UnitRecord{
				UnitID: id, Level: 10,
				DuplicatedCount: statmodel.MaxDuplicatedCount(u.Rare),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("预置库存失败: %v", err)
	}

	resp := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=3&count=10", "_method=GET")
	if resp["status"] != "success" {
		t.Fatalf("满觉结算应成功，实际 %v", resp)
	}
	res, ok := resp["data"].(map[string]any)["temp_gacha_result"].([]any)
	if !ok || len(res) != 10 {
		t.Fatalf("temp_gacha_result 应有 10 项: %v", resp["data"])
	}
	for i, raw := range res {
		e := raw.(map[string]any)
		before, ok := e["before_state"].(map[string]any)
		if !ok {
			t.Fatalf("第 %d 项缺 before_state: %v", i, e)
		}
		bDup, _ := asFloat(before["duplicated_count"])
		aDup, _ := asFloat(e["duplicated_count"])
		if bDup != aDup {
			t.Fatalf("第 %d 项满觉应前后同值: before=%v after=%v", i, bDup, aDup)
		}
	}
	// 满觉补偿须入账徽章 medal（不吞卡、不发金币）：金币只减扣费，
	// 徽章按稀有度补偿表合计增加。
	if got := gachaBalanceOf(t, st, uid); got != goldBefore-2000 {
		t.Fatalf("金币扣费异常: before=%d after=%d want %d", goldBefore, got, goldBefore-2000)
	}
	if got := medalBalanceOf(t, st, uid); got <= medalBefore {
		t.Fatalf("满觉徽章补偿未入账: before=%d after=%d", medalBefore, got)
	}
}

// 回放已提交快照：结果一致、不重复扣费、不新增操作记录。
func TestGachaExecuteReplayWithinWindow(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-replay")
	setCoin(t, st, uid, 100)

	r1 := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=2&count=1", "_method=GET")
	if r1["status"] != "success" {
		t.Fatalf("首次结算失败: %v", r1)
	}
	r2 := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=2&count=1", "_method=GET")
	if r2["status"] != "success" {
		t.Fatalf("重放应成功: %v", r2)
	}
	if !sameGachaResult(r1, r2) {
		t.Fatalf("重放结果应与首次一致:\n first=%v\nsecond=%v",
			r1["data"], r2["data"])
	}
	if got := coinBalanceOf(t, st, uid); got != 95 {
		t.Fatalf("重放重复扣费: coin=%d want 95", got)
	}
	if n := gachaOpRows(t, st, uid); n != 1 {
		t.Fatalf("重放新增操作记录: %d want 1", n)
	}
}

// TestGachaExecuteRetryAfterReloadReplays 玩家经错误路径重试
// （committed 后发过 on=gachas_execute 的 reload_data）→ 回放快照。
// 传输层窗口置负以隔离验证：本断言只靠 reload 信号支触发。
func TestGachaExecuteRetryAfterReloadReplays(t *testing.T) {
	cn35game.GachaSetReplayWindowForTest(-1, 600)
	t.Cleanup(func() { cn35game.GachaSetReplayWindowForTest(5, 600) })

	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-retry")
	setCoin(t, st, uid, 100)

	r1 := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=2&count=1", "_method=GET")
	if r1["status"] != "success" {
		t.Fatalf("首次结算失败: %v", r1)
	}
	// 模拟客户端"扣费后响应丢失"：走错误路径刷新缓存。
	rel := gameJSON(t, base, "/users/reload_data", uid, sk, s0, s1,
		"on=gachas_execute", "_method=GET")
	if rel["status"] != "success" {
		t.Fatalf("reload_data 失败: %v", rel)
	}
	r2 := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=2&count=1", "_method=GET")
	if r2["status"] != "success" {
		t.Fatalf("重试应回放成功: %v", r2)
	}
	if !sameGachaResult(r1, r2) {
		t.Fatalf("重试回放结果应与首次一致:\n first=%v\nsecond=%v",
			r1["data"], r2["data"])
	}
	if got := coinBalanceOf(t, st, uid); got != 95 {
		t.Fatalf("重试重复扣费: coin=%d want 95", got)
	}
	if n := gachaOpRows(t, st, uid); n != 1 {
		t.Fatalf("重试新增操作记录: %d want 1", n)
	}
}

// TestGachaExecuteIntentionalRedrawCharges 成功后玩家合法再抽同参
// （无 reload_data 信号）→ 新结算：再次扣费、新增操作记录。
// 传输层窗口置负隔离 reload/窗口两支——合法再抽**绝不**被误判回放。
func TestGachaExecuteIntentionalRedrawCharges(t *testing.T) {
	cn35game.GachaSetReplayWindowForTest(-1, 600)
	t.Cleanup(func() { cn35game.GachaSetReplayWindowForTest(5, 600) })

	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-redraw")
	setCoin(t, st, uid, 100)

	r1 := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=2&count=1", "_method=GET")
	if r1["status"] != "success" {
		t.Fatalf("首次结算失败: %v", r1)
	}
	r2 := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=2&count=1", "_method=GET")
	if r2["status"] != "success" {
		t.Fatalf("合法再抽应成功: %v", r2)
	}
	if got := coinBalanceOf(t, st, uid); got != 90 {
		t.Fatalf("合法再抽应再扣 5 钻: coin=%d want 90", got)
	}
	if n := gachaOpRows(t, st, uid); n != 2 {
		t.Fatalf("合法再抽应新增操作: %d want 2", n)
	}
}

// TestGachaExecuteConcurrentReplay 并发同参请求：per-账号串行保证
// 只有一个进入新结算，其余落窗口重放——扣费一次、操作一条。
func TestGachaExecuteConcurrentReplay(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-conc")
	setCoin(t, st, uid, 100)

	const n = 6
	results := make([]map[string]any, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// goroutine 内不得调用 t.Fatal 系方法（FailNow 限测试
			// goroutine）——用 error 返回变体，主测试统一断言。
			results[i], errs[i] = gameJSONConcurrent(base, "/gachas/execute",
				uid, sk, s0, s1, "id=2&count=1", "_method=GET")
		}(i)
	}
	wg.Wait()

	for i := range results {
		if errs[i] != nil {
			t.Fatalf("并发请求 %d 调用失败: %v", i, errs[i])
		}
		if results[i]["status"] != "success" {
			t.Fatalf("并发请求 %d 应成功（首抽或重放）: %v", i, results[i])
		}
	}
	for i := 1; i < n; i++ {
		if !sameGachaResult(results[0], results[i]) {
			t.Fatalf("并发同参应回放同一结果: req0=%v req%d=%v",
				results[0]["data"], i, results[i]["data"])
		}
	}
	if got := coinBalanceOf(t, st, uid); got != 95 {
		t.Fatalf("并发重复扣费: coin=%d want 95", got)
	}
	if ops := gachaOpRows(t, st, uid); ops != 1 {
		t.Fatalf("并发产生多个操作: %d want 1", ops)
	}
}

// TestGachaExecuteInsufficientBalance 余额不足：error modal + 零副作用。
func TestGachaExecuteInsufficientBalance(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-insufficient")
	setCoin(t, st, uid, 3) // < 单次 5 钻

	resp := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=2&count=1", "_method=GET")
	if resp["status"] != "error" {
		t.Fatalf("余额不足应拒绝，实际 %v", resp)
	}
	if _, ok := resp["modal"]; !ok {
		t.Fatalf("应返回 modal 供客户端 ResponseFilter 识别: %v", resp)
	}
	if got := coinBalanceOf(t, st, uid); got != 3 {
		t.Fatalf("余额不足请求改动了余额: %d want 3", got)
	}
	if n := gachaOpRows(t, st, uid); n != 0 {
		t.Fatalf("余额不足留下操作记录: %d", n)
	}
}

// TestGachaExecuteMultiDraw 连抽：count=10 一次扣 50 钻、出 10 条结果、
// 逐抽记录 10 条、保底按序推进。
func TestGachaExecuteMultiDraw(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-multi")
	setCoin(t, st, uid, 100)

	resp := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=2&count=10", "_method=GET")
	if resp["status"] != "success" {
		t.Fatalf("连抽应成功，实际 %v", resp)
	}
	data := resp["data"].(map[string]any)
	res, ok := data["temp_gacha_result"].([]any)
	if !ok || len(res) != 10 {
		t.Fatalf("temp_gacha_result 应有 10 项: %v", data["temp_gacha_result"])
	}
	if got := coinBalanceOf(t, st, uid); got != 50 {
		t.Fatalf("连抽应扣 50 钻: coin=%d want 50", got)
	}
	if n := gachaDrawRows(t, st, uid); n != 10 {
		t.Fatalf("连抽逐抽记录=%d want 10", n)
	}
	if n := gachaOpRows(t, st, uid); n != 1 {
		t.Fatalf("连抽应为单次操作: %d want 1", n)
	}
}

// TestGachaExecuteInvalidInputNoSideEffect 非法参数一律无副作用。
func TestGachaExecuteInvalidInputNoSideEffect(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-invalid")
	point := gachaBalanceOf(t, st, uid)

	cases := []struct {
		name  string
		query string
	}{
		{"次数为零", "id=3&count=0"},
		{"次数为负", "id=3&count=-1"},
		{"次数超上限", "id=3&count=11"},
		{"次数极大", "id=3&count=999"},
		{"未知池", "id=999&count=1"},
		{"次数非数字", "id=3&count=abc"},
		{"缺池id", "count=1"},
		{"票券未开放", "id=3&count=1&use_scout_book=true"},
		{"限时池未开放", "id=1&count=1"},
	}
	for _, c := range cases {
		resp := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1, c.query, "_method=GET")
		if resp["status"] != "error" {
			t.Fatalf("%s 应被拒绝，实际 %v", c.name, resp)
		}
	}
	if got := gachaBalanceOf(t, st, uid); got != point {
		t.Fatalf("非法请求改动了余额: %d want %d", got, point)
	}
	if n := gachaUnitRows(t, st, uid); n != 0 {
		t.Fatalf("非法请求发放了卡: %d 行", n)
	}
	if n := gachaOpRows(t, st, uid); n != 0 {
		t.Fatalf("非法请求留下操作记录: %d 条", n)
	}
}

// TestGachaExecuteIgnoresForcedWinParams 客户端不得通过任何参数指定中奖角色
// （设计 §3.7：生产不接受客户端随机种子或中奖角色）。执行面开放后合法
// 参数正常结算，但伪造的 unit_id/rare_type_id/seed 必须被忽略——
// 金币池（id=3）产不出 UR(152)，出现即证明参数被采纳。
func TestGachaExecuteIgnoresForcedWinParams(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-forced")
	setGachaPoint(t, st, uid, 1000)

	resp := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
		"id=3&count=1&unit_id=152&rare_type_id=50&seed=1&force=true", "_method=GET")
	if resp["status"] != "success" {
		t.Fatalf("合法参数应正常结算（伪造参数被忽略），实际 %v", resp)
	}
	data := resp["data"].(map[string]any)
	res, ok := data["temp_gacha_result"].([]any)
	if !ok || len(res) != 1 {
		t.Fatalf("temp_gacha_result 应有 1 项: %v", data["temp_gacha_result"])
	}
	got, _ := asFloat(res[0].(map[string]any)["id"])
	if int(got) == 152 {
		t.Fatal("伪造 unit_id=152 被采纳——客户端成功指定中奖角色")
	}
	gotRarity, _ := asFloat(res[0].(map[string]any)["rare_type_id"])
	if int(gotRarity) == 50 {
		t.Fatal("伪造 rare_type_id=50(UR) 被采纳")
	}
}

// TestGachaPoolsViaRequireData 抽卡读面只经 requireData /gachas 下发：
// preset_data 不预置 gachas（预置会让 DataLoad 命中缓存、/gachas 永不
// 触发——首轮真机验收实测），/gachas 一次性回池表+警告+按钮选项+名单。
func TestGachaPoolsViaRequireData(t *testing.T) {
	base, _ := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-preset")

	// preset_data 直接回 fixture 骨架文档（无 status 包装），data 在顶层。
	preset := gameJSON(t, base, "/users/preset_data.json", uid, sk, s0, s1, "", "_method=GET")
	pdata, ok := preset["data"].(map[string]any)
	if !ok {
		t.Fatalf("preset_data 缺 data: %v", preset)
	}
	for _, key := range []string{"gachas", "gacha_warnings", "gacha_button_options"} {
		if _, ok := pdata[key]; ok {
			t.Fatalf("preset_data 不应预置 %s（会抑制 requireData /gachas 请求）", key)
		}
	}

	resp := gameJSON(t, base, "/gachas", uid, sk, s0, s1, "", "_method=GET")
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("/gachas 缺 data: %v", resp)
	}
	pools, ok := data["gachas"].([]any)
	if !ok || len(pools) == 0 {
		t.Fatalf("/gachas 缺 gachas 池列表: %v", data["gachas"])
	}
	seen := map[int]bool{}
	for _, raw := range pools {
		p, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("池条目形状错误: %v", raw)
		}
		lt, ok := p["list_type_id"].(float64)
		if !ok {
			t.Fatalf("池缺 list_type_id: %v", p)
		}
		// id 必须是 JSON number：客户端单抽 i("id") 读 int、连抽
		// s("id") 读 string——下发 string 会让单抽退化为 0。
		if _, isNum := p["id"].(float64); !isNum {
			t.Fatalf("池 id 非数字（单抽将读到 0）: %v", p["id"])
		}
		// 复刻客户端映射：<4 → 槽号=list_type_id+1，否则落槽1。
		slot := 1
		if int(lt) < 4 {
			slot = int(lt) + 1
		}
		if seen[slot] {
			t.Fatalf("list_type_id=%v 导致槽 %d 重复（客户端会抛异常）", lt, slot)
		}
		seen[slot] = true
		// 客户端 GachaIndexParts.UpdateContents 消费的字段必须齐备。
		for _, key := range []string{"id", "name", "description", "point", "price",
			"max_executable_count", "max_execute_bonus"} {
			if _, ok := p[key]; !ok {
				t.Fatalf("池 %v 缺客户端消费字段 %s", p["id"], key)
			}
		}
	}
	// M7：槽1 为每日轮换特殊招募（101..133），槽2 为骑士契约书/钻石常驻池，槽3 为金币池；
	// 停用槽4 不下发。
	for _, slot := range []int{1, 2, 3} {
		if !seen[slot] {
			t.Fatalf("槽 %d 无池（每日特殊招募+常驻双池必须下发）", slot)
		}
	}
	if seen[4] {
		t.Fatalf("停用槽 4 不应下发池文档")
	}
	if _, ok := data["gacha_warnings"]; !ok {
		t.Fatal("/gachas 缺 gacha_warnings")
	}
	opts, ok := data["gacha_button_options"].(map[string]any)
	if !ok {
		t.Fatalf("/gachas 缺 gacha_button_options（驱动详情按钮可见性）: %v",
			data["gacha_button_options"])
	}
	for _, raw := range pools {
		id := idStr(raw.(map[string]any)["id"])
		entry, ok := opts[id].(map[string]any)
		if !ok {
			t.Fatalf("池 %s 缺按钮选项", id)
		}
		detail, ok := entry["detail_onclick_option"].(map[string]any)
		if !ok {
			t.Fatalf("池 %s 缺 detail_onclick_option（规则/名单入口）", id)
		}
		// 详情按钮经 CreateMessageAndShowPageCore 按 method 动词分派；
		// 规则公示走原生 ShowRuleTipModal（标题+正文）。
		if detail["method"] != "ShowRule" {
			t.Fatalf("池 %s detail_onclick_option.method=%v，应为 ShowRule", id, detail["method"])
		}
		if s, _ := detail["info_text"].(string); s == "" {
			t.Fatalf("池 %s detail_onclick_option 缺 info_text 公示文案", id)
		}
		// 名单入口：available_list_option.parts.UnitList 深并入名单页
		// option.parts（PageBase.Merge），data_key 指到响应内名单键。
		alo, ok := raw.(map[string]any)["available_list_option"].(map[string]any)
		if !ok {
			t.Fatalf("池 %s 缺 available_list_option", id)
		}
		ul, ok := alo["parts"].(map[string]any)["UnitList"].(map[string]any)
		if !ok {
			t.Fatalf("池 %s available_list_option 缺 parts.UnitList（名单页将无内容）", id)
		}
		dk, _ := ul["data_key"].(string)
		if dk == "" || ul["row_prefab_name"] != "Common/Piece/UserUnitListRow" {
			t.Fatalf("池 %s UnitList 配置不完整: %v", id, ul)
		}
		if _, ok := data[dk]; !ok {
			t.Fatalf("/gachas 缺名单数据键 %s（名单页读不到行）", dk)
		}
	}
}

// TestGachaAvailableUnitsDocs 名单数据键必须与池可达稀有度同源：
// ROSTER350 双池政策下金币池3 产 N/R/HR/SR（1-4★）、钻石池2 产 HR/SR/UR；
// 停用槽 1/4 不下发名单键。名单含任意池外稀有度 = 公示与算法不一致。
func TestGachaAvailableUnitsDocs(t *testing.T) {
	base, _ := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-avail")

	resp := gameJSON(t, base, "/gachas", uid, sk, s0, s1, "", "_method=GET")
	data := resp["data"].(map[string]any)
	wantRarities := map[string]map[int]bool{
		"2": {30: true, 40: true, 50: true},
		"3": {10: true, 20: true, 30: true, 40: true},
	}
	if gachas, ok := data["gachas"].([]any); ok {
		for _, raw := range gachas {
			p := raw.(map[string]any)
			pid := idStr(p["id"])
			if lt, _ := asFloat(p["list_type_id"]); int(lt) == 0 && pid != "2" && pid != "3" {
				wantRarities[pid] = map[int]bool{30: true, 40: true, 50: true}
			}
		}
	}
	for _, pid := range []string{"1", "4"} {
		if _, ok := data["gacha_available_units_"+pid]; ok {
			t.Fatalf("停用池 %s 不应有名单键", pid)
		}
	}
	for pid, allowed := range wantRarities {
		key := "gacha_available_units_" + pid
		rows, ok := data[key].([]any)
		if !ok || len(rows) == 0 {
			t.Fatalf("池 %s 名单为空（键 %s）", pid, key)
		}
		for _, raw := range rows {
			row := raw.(map[string]any)
			r, _ := asFloat(row["rare_type_id"])
			if !allowed[int(r)] {
				t.Fatalf("池 %s 名单含不可产出稀有度 %v 单位 %v", pid, row["rare_type_id"], row["id"])
			}
			if _, has := row["unit_detail_message"]; has {
				t.Fatalf("名单单位 %v 不应携带 unit_detail_message（非持有反查必空）", row["id"])
			}
			// 渲染必需键（UserUnitListRow 消费面）。
			for _, k := range []string{"id", "name", "level", "max_level",
				"hp", "at", "rare_type_id", "attr_id", "weapon_id", "asset_name"} {
				if _, ok := row[k]; !ok {
					t.Fatalf("池 %s 名单行缺键 %s", pid, k)
				}
			}
		}
	}
}

// TestGachaReloadDataKeySet /users/reload_data 必须回客户端消费的键集合。
func TestGachaReloadDataKeySet(t *testing.T) {
	base, _ := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-reload")

	resp := gameJSON(t, base, "/users/reload_data", uid, sk, s0, s1,
		"on=gachas_execute", "_method=GET")
	if resp["status"] != "success" {
		t.Fatalf("reload_data 失败: %v", resp)
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("reload_data 缺 data: %v", resp)
	}
	for _, key := range []string{"user", "user_coin", "user_units", "gachas"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("reload_data 缺键 %s（客户端 ResponseFilter 依赖）", key)
		}
	}
	user, ok := data["user"].(map[string]any)
	if !ok {
		t.Fatalf("reload_data 的 user 形状错误: %v", data["user"])
	}
	if _, ok := user["gacha_point"]; !ok {
		t.Fatal("user 缺 gacha_point（客户端按此显示金币余额）")
	}
	coin, ok := data["user_coin"].(map[string]any)
	if !ok {
		t.Fatalf("user_coin 形状错误: %v", data["user_coin"])
	}
	if _, ok := coin["amount"]; !ok {
		t.Fatal("user_coin 缺 amount（客户端按此显示钻石余额）")
	}
}

// TestGachaExecuteDisabledPoolsRejected ROSTER350-02/05：旧槽 1（限时）与
// 槽 4（高级金币）已停用且不入已发布版本——直接请求执行也必须被拒且
// 零副作用（不扣费、不产生操作/逐抽记录）。
func TestGachaExecuteDisabledPoolsRejected(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-disabled")
	setCoin(t, st, uid, 100)
	setGachaPoint(t, st, uid, 100000)

	for _, pid := range []string{"1", "4", "99"} {
		resp := gameJSON(t, base, "/gachas/execute", uid, sk, s0, s1,
			"id="+pid+"&count=1", "_method=GET")
		if resp["status"] == "success" {
			t.Fatalf("停用/未知池 %s 不应执行成功: %v", pid, resp)
		}
	}
	if got := coinBalanceOf(t, st, uid); got != 100 {
		t.Fatalf("停用池拒绝不应扣钻: coin=%d", got)
	}
	if n := gachaOpRows(t, st, uid); n != 0 {
		t.Fatalf("停用池拒绝不应产生操作记录: %d", n)
	}
}

// TestGachaPoolDocPriceMatchesRules 下发价格必须与规则同源——
// 否则出现"UI 显示 5 钻、实际扣 10 钻"的静默不一致。
func TestGachaPoolDocPriceMatchesRules(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-price")

	resp := gameJSON(t, base, "/gachas", uid, sk, s0, s1, "", "_method=GET")
	data := resp["data"].(map[string]any)
	pools := data["gachas"].([]any)

	// 已发布版本叠加当日特殊招募主题池是文档真源。
	cat, cerr := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if cerr != nil {
		t.Fatalf("加载 catalog: %v", cerr)
	}
	want, _, err := cn35game.GachaResolvedPoolsForTest(cn35game.Deps{
		Catalog:     cn35game.NewRosterCatalog(cat, st),
		GachaConfig: st,
	}, time.Now())
	if err != nil || len(want) == 0 {
		t.Fatalf("读运行期配置: %v len=%d", err, len(want))
	}
	if len(pools) != len(want) {
		t.Fatalf("池数=%d want %d", len(pools), len(want))
	}
	byID := map[string]map[string]any{}
	for _, raw := range pools {
		p := raw.(map[string]any)
		byID[idStr(p["id"])] = p
	}
	for _, w := range want {
		id := w["id"].(string)
		p, ok := byID[id]
		if !ok {
			t.Fatalf("池 %s 未下发", id)
		}
		for _, key := range []string{"point", "price", "max_executable_count",
			"max_execute_bonus", "list_type_id"} {
			// JSON 数字解为 float64，规则侧是 Go int；统一按 float64 比较。
			got, gok := asFloat(p[key])
			exp, eok := asFloat(w[key])
			if !gok || !eok {
				t.Fatalf("池 %s 字段 %s 非数值: 下发=%v 规则=%v", id, key, p[key], w[key])
			}
			if got != exp {
				t.Fatalf("池 %s 字段 %s 下发值=%v 规则值=%v", id, key, p[key], w[key])
			}
		}
	}
}

// --- 测试辅助 ---

// asFloat 把 JSON 数字或 Go 整数统一为 float64 以便比较。
func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	}
	return 0, false
}

func gachaBalanceOf(t *testing.T, st *cn35store.Store, uid string) int {
	t.Helper()
	var v int
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&v); err != nil {
		t.Fatalf("读取 gacha_point 失败: %v", err)
	}
	return v
}

func gachaUnitRows(t *testing.T, st *cn35store.Store, uid string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_player_units WHERE user_id=?`, uid).Scan(&n); err != nil {
		t.Fatalf("统计库存失败: %v", err)
	}
	return n
}

func gachaOpRows(t *testing.T, st *cn35store.Store, uid string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_operations WHERE user_id=?`, uid).Scan(&n); err != nil {
		t.Fatalf("统计抽卡操作失败: %v", err)
	}
	return n
}

// medalBalanceOf 读徽章余额（满觉重复卡的补偿币种）。
func medalBalanceOf(t *testing.T, st *cn35store.Store, uid string) int {
	t.Helper()
	var v int
	if err := st.DB().QueryRow(`SELECT COALESCE(
		(SELECT amount FROM cn35_player_balances
		 WHERE user_id=? AND currency='medal'),0)`, uid).Scan(&v); err != nil {
		t.Fatalf("读取 medal 失败: %v", err)
	}
	return v
}

// setGachaPoint 直接写金币余额（金币池 point 行）。
func setGachaPoint(t *testing.T, st *cn35store.Store, uid string, amount int) {
	t.Helper()
	if _, err := st.DB().Exec(`UPDATE cn35_player_balances
		SET amount=? WHERE user_id=? AND currency='gacha_point'`, amount, uid); err != nil {
		t.Fatalf("写 gacha_point 失败: %v", err)
	}
}

// coinBalanceOf 读钻石余额（user_coin.amount 对应 coin 行）。
func coinBalanceOf(t *testing.T, st *cn35store.Store, uid string) int {
	t.Helper()
	var v int
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency='coin'`, uid).Scan(&v); err != nil {
		t.Fatalf("读取 coin 失败: %v", err)
	}
	return v
}

func gachaDrawRows(t *testing.T, st *cn35store.Store, uid string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_draws d
		JOIN cn35_gacha_operations o ON o.operation_id=d.operation_id
		WHERE o.user_id=?`, uid).Scan(&n); err != nil {
		t.Fatalf("统计逐抽记录失败: %v", err)
	}
	return n
}

func gachaLedgerRows(t *testing.T, st *cn35store.Store, uid string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_ledger WHERE user_id=?`, uid).Scan(&n); err != nil {
		t.Fatalf("统计抽卡流水失败: %v", err)
	}
	return n
}

func gachaPityRows(t *testing.T, st *cn35store.Store, uid string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_gacha_pity WHERE user_id=?`, uid).Scan(&n); err != nil {
		t.Fatalf("统计保底进度失败: %v", err)
	}
	return n
}

// sameGachaResult 比较两次响应的 temp_gacha_result 是否逐字段一致
// （重放必须返回与首次相同的结果）。
func sameGachaResult(a, b map[string]any) bool {
	da, _ := a["data"].(map[string]any)
	db, _ := b["data"].(map[string]any)
	return reflect.DeepEqual(da["temp_gacha_result"], db["temp_gacha_result"])
}

// gameJSONConcurrent 与 gameJSON 同构但返回 error——供 goroutine 调用
// （t.Fatal/FailNow 只能由测试 goroutine 调用）。
func gameJSONConcurrent(base, path, uid, sk, s0, s1, query, form string) (map[string]any, error) {
	url := base + path
	if query != "" {
		e, err := cn35protocol.EncryptQuery(query, "1700000000", s0, s1)
		if err != nil {
			return nil, err
		}
		url += "?e=" + e
	}
	if form == "" {
		form = "_method=GET"
	}
	body, err := cn35protocol.EncryptFormBody(form, "1700000000", s0, s1)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	id1, err := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, uid)
	if err != nil {
		return nil, err
	}
	id4, err := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, sk)
	if err != nil {
		return nil, err
	}
	appT, err := cn35protocol.EncryptAPP_T("1700000000", s0, s1)
	if err != nil {
		return nil, err
	}
	req.Header.Set("APP_ID_1", id1)
	req.Header.Set("APP_ID_4", id4)
	req.Header.Set("APP_T", appT)
	req.Header.Set("Encrypted", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	plain, err := cn35protocol.DecodeGameResponse(raw, s0, s1)
	if err != nil {
		return nil, fmt.Errorf("game 响应解码: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		return nil, fmt.Errorf("响应 JSON: %w", err)
	}
	return doc, nil
}

// TestGachaPoolsEndpointServesRequireData /gachas 必须存在且回 gachas 键。
//
// app_conf 的 Gacha 页声明 requireData.gachas.path=/gachas；PageBase.cs:755-768
// 会对每个 requireData 键发 GET 并用 requireDataLoaded 门控页面就绪
// （PageBase.cs:325）。缺该端点会让抽卡页永远卡在等待数据。
func TestGachaPoolsEndpointServesRequireData(t *testing.T) {
	base, st := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-pools")

	resp := gameJSON(t, base, "/gachas", uid, sk, s0, s1, "", "_method=GET")
	if resp["status"] != "success" {
		t.Fatalf("/gachas 失败: %v", resp)
	}
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("/gachas 缺 data: %v", resp)
	}
	pools, ok := data["gachas"].([]any)
	if !ok || len(pools) == 0 {
		t.Fatalf("/gachas 未回池列表: %v", data["gachas"])
	}
	if _, ok := data["gacha_button_options"]; !ok {
		t.Fatal("/gachas 缺 gacha_button_options")
	}
	// 与运行期已发布版本+当日轮换特殊招募同源。
	cat, cerr := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if cerr != nil {
		t.Fatalf("加载 catalog: %v", cerr)
	}
	want, _, err := cn35game.GachaResolvedPoolsForTest(cn35game.Deps{
		Catalog:     cn35game.NewRosterCatalog(cat, st),
		GachaConfig: st,
	}, time.Now())
	if err != nil || len(want) == 0 {
		t.Fatalf("读运行期配置: %v len=%d", err, len(want))
	}
	if len(pools) != len(want) {
		t.Fatalf("/gachas 池数=%d 规则=%d", len(pools), len(want))
	}
}

// TestGachaDescriptionNewlineEscaping 锁定描述换行的转义口径。
//
// 客户端 GachaIndexParts.cs:419 做：
//
//	descriptionLabel.text = gacha.s("description","").Replace("\\\\n", "\n");
//
// C# 源码里的 "\\\\n" 在运行时是**四个字符** \\n（反斜杠、反斜杠、n 中的后两者
// —— 准确说是「反斜杠 + 反斜杠 + n」里的双反斜杠加 n 共 4 字符）。
// 因此 JSON 里必须携带「转义的反斜杠 + n」，Go 字面量写作 "\\\\n"。
// 若只写 "\n"，客户端 Replace 不命中，屏幕上会直接出现字面 "\n"。
//
// 实机证据：GACHA350-08 首轮 A1-02 截图显示「钻石招募」描述出现字面 "\n"。
func TestGachaDescriptionNewlineEscaping(t *testing.T) {
	base, _ := gachaTestServer(t)
	uid, sk, s0, s1 := establishSession(t, base, "gacha-desc")

	resp := gameJSON(t, base, "/gachas", uid, sk, s0, s1, "", "_method=GET")
	data := resp["data"].(map[string]any)
	pools := data["gachas"].([]any)
	if len(pools) == 0 {
		t.Fatal("无池可校验")
	}
	for _, raw := range pools {
		p := raw.(map[string]any)
		desc, _ := p["description"].(string)
		if desc == "" {
			t.Fatalf("池 %v 描述为空", p["id"])
		}
		// 解析后的 JSON 字符串里应含「双反斜杠+n」三字符序列，
		// 供客户端 GachaIndexParts.cs:419 的 Replace("\\\\n", "\n") 命中。
		if !strings.Contains(desc, `\\n`) {
			t.Fatalf("池 %v 描述缺少可被客户端 Replace 命中的 \\\\\\\\n 序列: %q", p["id"], desc)
		}
		// 不能含真实换行——客户端只认字面 \\n。
		if strings.ContainsAny(desc, "\r\n") {
			t.Fatalf("池 %v 描述含真实换行，客户端不会转换: %q", p["id"], desc)
		}
	}
}
