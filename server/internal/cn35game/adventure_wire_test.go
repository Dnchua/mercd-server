// adventure_wire_test.go LOGIN350-03：登录冒险读面/当日发放的协议回放
// （真实 SQLite + 加密通道，注入可控时钟）。
//
// 覆盖：LoginAdventureData 逐字段结构（客户端 Load 必填项）、today_days
// 当日一次性、位图与配置一致性、礼物盒内容池到账与领取、跨天推进、
// 未配置轮回空态、无效会话不产生发放。
package cn35game_test

import (
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// advTestClock 可注入的冒险判定时钟（LA-09 跨天向量）。
type advTestClock struct {
	mu  sync.Mutex
	sec int64
}

func (c *advTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Unix(c.sec, 0)
}

func (c *advTestClock) Set(sec int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sec = sec
}

// adventureServer 装配登录冒险读/发面（账号/会话/礼物池/领取/冒险）。
func adventureServer(t *testing.T, stateDir string) (*httptest.Server, *cn35store.Store, *advTestClock) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	clock := &advTestClock{sec: time.Date(2026, 11, 20, 10, 0, 0, 0, advWireLoc).Unix()}
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:       st,
		Sessions:       st,
		Quests:         st,
		Catalog:        cat,
		Presents:       st,
		Infos:          st,
		PresentReceive: st,
		Inventory:      st,
		Items:          st,
		Adventure:      st,
		AdventureClock: clock.Now,
		ServerHost:     "127.0.0.1:18091",
		AssetsDir:      filepath.Join("..", "..", "assets", "cn350"),
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	return ts, st, clock
}

// advWireLoc 东八区。
var advWireLoc = time.FixedZone("UTC+8", 8*3600)

// advData 读 data.LoginAdventureData。
func advData(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	lad, ok := data["LoginAdventureData"].(map[string]any)
	if !ok {
		t.Fatalf("data.LoginAdventureData 缺失: %v", data)
	}
	return lad
}

// advAdventure 读三层嵌套的 adventure_info.adventure。
func advAdventure(t *testing.T, lad map[string]any) map[string]any {
	t.Helper()
	outer, ok := lad["adventure_info"].(map[string]any)
	if !ok {
		t.Fatalf("LoginAdventureData.adventure_info 缺失（客户端 Load 会失败）: %v", lad)
	}
	adv, ok := outer["adventure"].(map[string]any)
	if !ok {
		t.Fatalf("adventure_info.adventure 缺失（客户端 Load 会失败）: %v", outer)
	}
	return adv
}

// advSetting 读 data.LoginAdventureData.setting。
func advSetting(t *testing.T, lad map[string]any) map[string]any {
	t.Helper()
	setting, ok := lad["setting"].(map[string]any)
	if !ok {
		t.Fatalf("LoginAdventureData.setting 缺失（客户端 Load 会失败）: %v", lad)
	}
	return setting
}

// TestAdventureWireStructure 首次拉取：逐字段结构 + 当日数字 today_days（LA-01/02）。
func TestAdventureWireStructure(t *testing.T) {
	ts, st, clock := adventureServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-adv-wire-1")
	now := clock.Now().Unix()

	lad := advData(t, gameData(t, ts.URL, "/adventure/", uid, sk, s0, s1, ""))
	adv := advAdventure(t, lad)
	bitmap, ok := adv["adventure_info"].(map[string]any)
	if !ok {
		t.Fatalf("位图 adventure_info 非对象: %v", adv)
	}
	if len(bitmap) != 1 || bitmap["1"] != true {
		t.Fatalf("首拉位图=%v，预期 位图含键 1=true", bitmap)
	}
	if adv["reward_config_id"].(float64) != 1 {
		t.Fatalf("reward_config_id=%v，预期 1", adv["reward_config_id"])
	}
	if _, ok := adv["user_id"].(float64); !ok {
		t.Fatalf("user_id 必须是数值（客户端 ParseInt）: %v", adv["user_id"])
	}
	if _, ok := adv["begin_date_i"].(float64); !ok {
		t.Fatalf("begin_date_i 必须是数值: %v", adv["begin_date_i"])
	}
	// begin_date_i 应为东八区当日 0 点。
	wantBegin := time.Unix(now, 0).In(advWireLoc)
	wantBeginUnix := time.Date(wantBegin.Year(), wantBegin.Month(), wantBegin.Day(), 0, 0, 0, 0, advWireLoc).Unix()
	if int64(adv["begin_date_i"].(float64)) != wantBeginUnix {
		t.Fatalf("begin_date_i=%v，预期东八区当日 0 点 %d",
			adv["begin_date_i"], wantBeginUnix)
	}
	if lad["today_days"] != "1" {
		t.Fatalf("today_days=%v，预期 1（当日首次 ⇒ 客户端弹窗）", lad["today_days"])
	}
	if delta := int64(lad["server_date_i"].(float64)) - now; delta < -5 || delta > 5 {
		t.Fatalf("server_date_i 与注入时钟差 %ds，预期 0", delta)
	}

	setting := advSetting(t, lad)
	days, ok := setting["days"].([]any)
	if !ok || len(days) != 10 {
		t.Fatalf("setting.days 长度=%v，预期 10", len(days))
	}
	path, ok := setting["path"].([]any)
	if !ok || len(path) != 20 {
		t.Fatalf("setting.path 长度=%v，预期 20（10 节点蛇形折线）", len(path))
	}
	// 每日格：title/pos/rewards 必填；rewards[0] 字段齐全。
	var prevPos float64 = -1
	for i, raw := range days {
		day := raw.(map[string]any)
		if _, has := day["title"].(string); !has {
			t.Fatalf("第 %d 天缺 title: %v", i+1, day)
		}
		pos, ok := day["pos"].(float64)
		if !ok || pos < 0 || pos > 1 || pos < prevPos {
			t.Fatalf("第 %d 天 pos=%v 越界或乱序", i+1, day["pos"])
		}
		prevPos = pos
		if _, has := day["special"].(bool); !has {
			t.Fatalf("第 %d 天缺 special: %v", i+1, day)
		}
		rewards, ok := day["rewards"].([]any)
		if !ok || len(rewards) == 0 {
			t.Fatalf("第 %d 天 rewards 缺失/空: %v", i+1, day)
		}
		r := rewards[0].(map[string]any)
		for _, k := range []string{"type_id", "type_name", "content_id", "count"} {
			if _, has := r[k]; !has {
				t.Fatalf("第 %d 天 rewards[0] 缺 %s: %v", i+1, k, r)
			}
		}
	}
	// 第 5/10 天为 special（重建种子口径）。
	if days[4].(map[string]any)["special"] != true || days[9].(map[string]any)["special"] != true {
		t.Fatalf("第 5/10 天应为 special：%v / %v",
			days[4].(map[string]any)["special"], days[9].(map[string]any)["special"])
	}
	// unit/-1 特例：第 9 天展示 scout_book 图标 + unit_rare=3。
	r9 := days[8].(map[string]any)["rewards"].([]any)[0].(map[string]any)
	if r9["type_name"] != "unit" || r9["content_id"].(float64) != -1 {
		t.Fatalf("第 9 天应为 unit/-1 展示特例: %v", r9)
	}
	ext, ok := r9["extend"].(map[string]any)
	if !ok || ext["unit_rare"].(float64) != 3 {
		t.Fatalf("第 9 天 extend.unit_rare 缺失/非 3: %v", r9)
	}

	// 同日第二次拉取：today_days 空串 ⇒ 客户端 "already shown, skipped"。
	lad2 := advData(t, gameData(t, ts.URL, "/adventure/", uid, sk, s0, s1, ""))
	if lad2["today_days"] != "" {
		t.Fatalf("同日第二次 today_days=%v，预期空串", lad2["today_days"])
	}
	// 位图/begin_date_i/user_id 保持稳定。
	adv2 := advAdventure(t, lad2)
	if adv2["begin_date_i"].(float64) != adv["begin_date_i"].(float64) ||
		adv2["user_id"].(float64) != adv["user_id"].(float64) {
		t.Fatalf("同日两次 begin_date_i/user_id 不一致: %v vs %v", adv, adv2)
	}
}

// TestAdventureWirePresentBox 当日奖励经礼物盒到账、可领取一次（LA-05）。
func TestAdventureWirePresentBox(t *testing.T) {
	ts, st, _ := adventureServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-adv-wire-2")

	// 先拉 /adventure/（当日首拉即发放），再拉礼物盒：种子第 1 天 = coin ×30。
	advData(t, gameData(t, ts.URL, "/adventure/", uid, sk, s0, s1, ""))
	pres := gameData(t, ts.URL, "/user_presents/", uid, sk, s0, s1, "")
	list := advAdventurePresents(t, pres)
	if len(list) != 1 {
		t.Fatalf("待领礼物=%d 条，预期 1（当日）+ 建号 STARTER 不计入: %v", len(list), list)
	}
	row := list[0]
	if row["type_name"] != "coin" || row["content_id"].(float64) != 1 ||
		row["amount"].(float64) != 30 {
		t.Fatalf("礼物行内容异常: %v", row)
	}

	before := advCoin(t, st, uid)
	uuid := row["uuid"].(string)
	doc := gameJSON(t, ts.URL, "/user_presents/receive/", uid, sk, s0, s1, "uuid="+uuid, "")
	if doc["status"] != "success" {
		t.Fatalf("领取失败: %v", doc)
	}
	after := advCoin(t, st, uid)
	if after-before != 30 {
		t.Fatalf("领取后钻石增量=%v，预期 30", after-before)
	}
	// 重复领取：明确拒绝且不再加钻。
	doc = gameJSON(t, ts.URL, "/user_presents/receive/", uid, sk, s0, s1, "uuid="+uuid, "")
	if doc["status"] == "success" {
		t.Fatalf("重复领取应被拒绝: %v", doc)
	}
	if again := advCoin(t, st, uid); again != after {
		t.Fatalf("重复领取后钻石变化 %v → %v", after, again)
	}
}

// advAdventurePresents 过滤出冒险发放的礼物行（uuid 前缀 adv-）。
func advAdventurePresents(t *testing.T, data map[string]any) []map[string]any {
	t.Helper()
	rows, ok := data["user_presents"].([]any)
	if !ok {
		t.Fatalf("data.user_presents 非数组: %v", data)
	}
	var out []map[string]any
	for _, raw := range rows {
		row := raw.(map[string]any)
		if uuid, _ := row["uuid"].(string); len(uuid) > 4 && uuid[:4] == "adv-" {
			out = append(out, row)
		}
	}
	return out
}

// advCoin 读账号钻石余额。
func advCoin(t *testing.T, st *cn35store.Store, uid string) float64 {
	t.Helper()
	var v int64
	if err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency='coin'`, uid).Scan(&v); err != nil {
		t.Fatalf("读钻石余额: %v", err)
	}
	return float64(v)
}

// TestAdventureWireNextDayAndEmptyState 跨天推进 + 未配置轮回空态（LA-04/LA-10）。
func TestAdventureWireNextDayAndEmptyState(t *testing.T) {
	ts, st, clock := adventureServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-adv-wire-3")

	lad := advData(t, gameData(t, ts.URL, "/adventure/", uid, sk, s0, s1, ""))
	if lad["today_days"] != "1" {
		t.Fatalf("首拉 today_days=%v", lad["today_days"])
	}
	// 服务端时钟推进一天：重新给数字且前进一格。
	clock.Set(clock.Now().Add(24 * time.Hour).Unix())
	lad = advData(t, gameData(t, ts.URL, "/adventure/", uid, sk, s0, s1, ""))
	if lad["today_days"] != "2" {
		t.Fatalf("次日 today_days=%v，预期 2", lad["today_days"])
	}
	bitmap := advAdventure(t, lad)["adventure_info"].(map[string]any)
	if bitmap["1"] != true || bitmap["2"] != true {
		t.Fatalf("次日位图=%v，预期 1/2 置位", bitmap)
	}

	// 未配置轮次（删配置）⇒ 结构完整空态，不是错误页。
	if _, err := st.DB().Exec(`DELETE FROM cn35_adventure_configs`); err != nil {
		t.Fatalf("清空轮次配置: %v", err)
	}
	clock.Set(clock.Now().Add(24 * time.Hour).Unix())
	empty := advData(t, gameData(t, ts.URL, "/adventure/", uid, sk, s0, s1, ""))
	if _, has := empty["adventure_info"]; !has {
		t.Fatalf("空态缺 adventure_info: %v", empty)
	}
	if _, has := empty["setting"]; !has {
		t.Fatalf("空态缺 setting: %v", empty)
	}
	if empty["today_days"] != "" {
		t.Fatalf("空态 today_days=%v，预期空串（客户端跳过弹窗）", empty["today_days"])
	}
}

// TestAdventureWireBadSessionNoGrant 坏会话/伪造账号不产生发放（LA-10）。
//
// 口径边界（如实记录，不改既有传输层）：`cn35wire.DecodeGameRequest` 只把
// APP_ID_4 解出 sk 放进 Session.SK，**鉴权比较由业务层做**（cn35wire.go:235），
// 而当前 CN3.5 各业务 handler 普遍未比较 sk。故「sk 不一致」在本平台不会
// 被拒——这是既有口径，不是本任务引入；本测试只断言它**不产生额外发放**。
// 真正会被拒的是「uid 无会话」与「加密参数解不开」两类。
func TestAdventureWireBadSessionNoGrant(t *testing.T) {
	ts, st, _ := adventureServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-adv-wire-4")

	counts := func() (claims, presents int) {
		if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_adventure_claims`).Scan(&claims); err != nil {
			t.Fatalf("统计台账: %v", err)
		}
		if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_user_presents
			WHERE uuid LIKE 'adv-%'`).Scan(&presents); err != nil {
			t.Fatalf("统计礼物: %v", err)
		}
		return claims, presents
	}

	// 正确会话一次，建立基线。
	advData(t, gameData(t, ts.URL, "/adventure/", uid, sk, s0, s1, ""))
	if claims, presents := counts(); claims != 1 || presents != 1 {
		t.Fatalf("基线应为 1/1，实际 %d/%d", claims, presents)
	}

	// 1) 未知 uid（无会话）⇒ 传输层拒绝，零副作用。
	if _, err := gameJSONConcurrent(ts.URL, "/adventure/", "ffffffffffff", sk, s0, s1, "", ""); err == nil {
		t.Fatal("未知 uid（无会话）应被拒绝")
	}
	// 2) 会话存在但加密参数用错 s0/s1 ⇒ 解不开 ⇒ 拒绝，零副作用。
	if _, err := gameJSONConcurrent(ts.URL, "/adventure/", uid, sk,
		"00000000000000000000000000000000", s1, "", ""); err == nil {
		t.Fatal("加密参数错误应被拒绝")
	}
	if claims, presents := counts(); claims != 1 || presents != 1 {
		t.Fatalf("坏会话请求产生了副作用: claims=%d presents=%d", claims, presents)
	}

	// 3) sk 不一致：平台不校验（既有口径），不得新增发放（同日已发过）。
	doc, err := gameJSONConcurrent(ts.URL, "/adventure/", uid, badSK(), s0, s1, "", "")
	if err != nil {
		t.Fatalf("sk 不一致的请求在既有平台口径下应可解码，实际报错: %v", err)
	}
	if _, has := doc["data"]; !has {
		t.Fatalf("响应缺 data 封套: %v", doc)
	}
	if claims, presents := counts(); claims != 1 || presents != 1 {
		t.Fatalf("sk 不一致请求新增了发放: claims=%d presents=%d", claims, presents)
	}
}

// badSK 与真实 sk 不可能相等的占位签名。
func badSK() string { return "forged-sk-0000000000000000" }
