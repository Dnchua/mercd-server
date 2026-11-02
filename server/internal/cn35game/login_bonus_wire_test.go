// login_bonus_wire_test.go LOGIN350-02：不可思议之泉（时间累积）读/领协议回放
// 与月签到读/领协议回放（真实 SQLite + 加密通道）。
//
// 覆盖：读面不发奖、按累积时长下发待领量、领取入账三资源与 BP 溢出、
// 重复/并发只发一次、伪造参数无效、双账号隔离、无效会话拒绝、签到 28 行
// 与解锁/重复领取、签到发奖与台账。
package cn35game_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// springServer 装配泉水/签到读写面所需最小依赖（账号/会话/进度/台账/签到）。
func springServer(t *testing.T, stateDir string) (*httptest.Server, *cn35store.Store) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:   st,
		Sessions:   st,
		Quests:     st,
		Catalog:    cat,
		LoginBonus: st,
		Signin:     st,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	return ts, st
}

// springUser 读响应里的 data.user。
func springUser(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	user, ok := data["user"].(map[string]any)
	if !ok {
		t.Fatalf("data.user 缺失: %v", data)
	}
	return user
}

// springShowDoc 读 data.show_login_bonus。
func springShowDoc(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	doc, ok := data["show_login_bonus"].(map[string]any)
	if !ok {
		t.Fatalf("data.show_login_bonus 缺失: %v", data)
	}
	return doc
}

// springContentsOf 读 data.temp_login_bonus.contents。
func springContentsOf(t *testing.T, data map[string]any) []any {
	t.Helper()
	doc, ok := data["temp_login_bonus"].(map[string]any)
	if !ok {
		t.Fatalf("data.temp_login_bonus 缺失: %v", data)
	}
	contents, ok := doc["contents"].([]any)
	if !ok {
		t.Fatalf("temp_login_bonus.contents 非数组: %v", doc)
	}
	return contents
}

// springSetAnchor 构造「距上次领取 N 小时」的档案状态。
func springSetAnchor(t *testing.T, st *cn35store.Store, uid string, anchorSec int64) {
	t.Helper()
	if _, err := st.DB().Exec(`UPDATE cn35_player_profiles
		SET login_bonus_receivable_sec=? WHERE user_id=?`,
		anchorSec+cn35game.SpringIntervalSec, uid); err != nil {
		t.Fatalf("设置锚点: %v", err)
	}
}

// springDBState 读档案可领时刻、BP 与余额。
func springDBState(t *testing.T, st *cn35store.Store, uid string) (receivable, bp, gold, guild int64) {
	t.Helper()
	if err := st.DB().QueryRow(`SELECT login_bonus_receivable_sec,bp
		FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&receivable, &bp); err != nil {
		t.Fatalf("读档案: %v", err)
	}
	read := func(cur string) int64 {
		var v int64
		err := st.DB().QueryRow(`SELECT amount FROM cn35_player_balances
			WHERE user_id=? AND currency=?`, uid, cur).Scan(&v)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("读余额 %s: %v", cur, err)
		}
		return v
	}
	return receivable, bp, read("gacha_point"), read("guild_asset")
}

// springClaimRows 泉水台账行数。
func springClaimRows(t *testing.T, st *cn35store.Store, uid string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_spring_claims
		WHERE user_id=?`, uid).Scan(&n); err != nil {
		t.Fatalf("统计泉水台账: %v", err)
	}
	return n
}

const springWireSeedPoint = 302

func TestSpringReadAccrualAndClaim(t *testing.T) {
	ts, st := springServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-spring-wire")
	now := time.Now().Unix()

	// 1) 新号（建号 <1 小时）：读面待领量全 0；不落台账；user 带未来可领时刻。
	show := gameData(t, ts.URL, "/users/show_login_bonus", uid, sk, s0, s1, "")
	doc := springShowDoc(t, show)
	for _, k := range []string{"progress_rate", "hard_mode_progress_rate",
		"gacha_point", "guild_asset", "battle_point"} {
		if _, ok := doc[k]; !ok {
			t.Fatalf("show_login_bonus 缺字段 %s: %v", k, doc)
		}
	}
	if doc["gacha_point"] != float64(0) || doc["guild_asset"] != float64(0) ||
		doc["battle_point"] != float64(0) {
		t.Fatalf("新号待领量应为 0: %v", doc)
	}
	user := springUser(t, show)
	if sec, _ := user["login_bonus_receivable_sec"].(float64); int64(sec) <= now {
		t.Fatalf("新号可领时刻应在未来: %v", user["login_bonus_receivable_sec"])
	}
	if user["gacha_point"] != float64(springWireSeedPoint) {
		t.Fatalf("读面不应发奖: %v", user["gacha_point"])
	}
	if n := springClaimRows(t, st, uid); n != 0 {
		t.Fatalf("读面落台账: %d", n)
	}

	// 2) 累积 2 小时后：读面下发待领量 2000/2/10。
	springSetAnchor(t, st, uid, now-2*3600)
	show = gameData(t, ts.URL, "/users/show_login_bonus", uid, sk, s0, s1, "")
	doc = springShowDoc(t, show)
	if doc["gacha_point"] != float64(2000) || doc["guild_asset"] != float64(2) ||
		doc["battle_point"] != float64(10) {
		t.Fatalf("待领量 = %v, want 2000/2/10", doc)
	}

	// 3) 领取：contents 三行；余额/BP/可领时刻同响应刷新。
	recv := gameData(t, ts.URL, "/users/receive_login_bonus", uid, sk, s0, s1, "")
	contents := springContentsOf(t, recv)
	if len(contents) != 3 {
		t.Fatalf("领取 contents = %v", contents)
	}
	seen := map[string]float64{}
	for _, c := range contents {
		row := c.(map[string]any)
		seen[row["type"].(string)] = row["count"].(float64)
	}
	if seen["gacha_point"] != 2000 || seen["guild_asset"] != 2 || seen["battle_point"] != 10 {
		t.Fatalf("奖励行 = %v", seen)
	}
	user = springUser(t, recv)
	if user["gacha_point"] != float64(springWireSeedPoint+2000) {
		t.Fatalf("领取后金币 = %v", user["gacha_point"])
	}
	if user["guild_asset_count"] != float64(2) {
		t.Fatalf("领取后公会建设素材 = %v", user["guild_asset_count"])
	}
	if user["bp"] != float64(310) {
		t.Fatalf("领取后 BP = %v（应允许溢出到 310）", user["bp"])
	}
	recvSec, _ := user["login_bonus_receivable_sec"].(float64)
	if int64(recvSec) <= now {
		t.Fatalf("领取后可领时刻应推进到未来: %v", user["login_bonus_receivable_sec"])
	}
	if n := springClaimRows(t, st, uid); n != 1 {
		t.Fatalf("领取台账 = %d, want 1", n)
	}

	// 4) 重放：空 contents、零重复入账、可领时刻不变。
	again := gameData(t, ts.URL, "/users/receive_login_bonus", uid, sk, s0, s1, "")
	if c := springContentsOf(t, again); len(c) != 0 {
		t.Fatalf("重放不应再发奖: %v", c)
	}
	if got := springUser(t, again)["gacha_point"]; got != float64(springWireSeedPoint+2000) {
		t.Fatalf("重放后金币 = %v", got)
	}
	if got := springUser(t, again)["login_bonus_receivable_sec"]; got != user["login_bonus_receivable_sec"] {
		t.Fatalf("重放改了可领时刻: %v → %v", user["login_bonus_receivable_sec"], got)
	}
	if n := springClaimRows(t, st, uid); n != 1 {
		t.Fatalf("重放后台账 = %d, want 1", n)
	}

	// 5) 读面/preset_data 与 DB 同源。
	recvDB, _, goldDB, guildDB := springDBState(t, st, uid)
	for name, data := range map[string]map[string]any{
		"show":   gameData(t, ts.URL, "/users/show_login_bonus", uid, sk, s0, s1, ""),
		"preset": gameData(t, ts.URL, "/users/preset_data.json", uid, sk, s0, s1, ""),
	} {
		u := springUser(t, data)
		if u["login_bonus_receivable_sec"] != float64(recvDB) {
			t.Fatalf("%s 可领时刻 = %v, DB = %d", name, u["login_bonus_receivable_sec"], recvDB)
		}
		if u["gacha_point"] != float64(goldDB) || u["guild_asset_count"] != float64(guildDB) {
			t.Fatalf("%s 余额与 DB 不一致", name)
		}
	}
}

func TestSpringConcurrentClaimOnlyOnce(t *testing.T) {
	ts, st := springServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-spring-conc")
	springSetAnchor(t, st, uid, time.Now().Unix()-3600)

	const n = 8
	var wg sync.WaitGroup
	granted := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			doc, err := gameJSONConcurrent(ts.URL, "/users/receive_login_bonus",
				uid, sk, s0, s1, "", "")
			if err != nil {
				return
			}
			data, _ := doc["data"].(map[string]any)
			if d, ok := data["temp_login_bonus"].(map[string]any); ok {
				if rows, ok := d["contents"].([]any); ok {
					granted[i] = len(rows)
				}
			}
		}(i)
	}
	wg.Wait()
	total := 0
	for _, g := range granted {
		total += g
	}
	if total != 3 {
		t.Fatalf("并发有效发奖行数合计 = %d, want 3（一次领取三行；%v）", total, granted)
	}
	if _, _, gold, guild := springDBState(t, st, uid); gold != springWireSeedPoint+1000 || guild != 1 {
		t.Fatalf("并发后余额 = 金币 %d/公会 %d", gold, guild)
	}
	if n := springClaimRows(t, st, uid); n != 1 {
		t.Fatalf("并发台账 = %d, want 1", n)
	}
}

func TestSpringParamInjectionIgnored(t *testing.T) {
	ts, st := springServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-spring-inject")
	springSetAnchor(t, st, uid, time.Now().Unix()-3600)
	recv := gameData(t, ts.URL, "/users/receive_login_bonus", uid, sk, s0, s1,
		"count=99999&type=coin&gold=999999")
	contents := springContentsOf(t, recv)
	if len(contents) != 3 {
		t.Fatalf("contents = %v", contents)
	}
	for _, c := range contents {
		row := c.(map[string]any)
		if row["type"] == "coin" || row["count"] == float64(99999) {
			t.Fatalf("伪造参数生效: %v", row)
		}
	}
	if _, _, gold, _ := springDBState(t, st, uid); gold != springWireSeedPoint+1000 {
		t.Fatalf("金币 = %d", gold)
	}
}

func TestSpringAccountIsolationAndInvalidSession(t *testing.T) {
	ts, st := springServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "dev-spring-iso-a")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "dev-spring-iso-b")
	now := time.Now().Unix()
	springSetAnchor(t, st, uidA, now-3600)
	springSetAnchor(t, st, uidB, now-3600)

	if c := springContentsOf(t, gameData(t, ts.URL, "/users/receive_login_bonus", uidA, skA, s0A, s1A, "")); len(c) != 3 {
		t.Fatalf("A 领取应成功: %v", c)
	}
	if c := springContentsOf(t, gameData(t, ts.URL, "/users/receive_login_bonus", uidB, skB, s0B, s1B, "")); len(c) != 3 {
		t.Fatalf("B 领取应成功: %v", c)
	}
	if _, _, goldA, _ := springDBState(t, st, uidA); goldA != springWireSeedPoint+1000 {
		t.Fatalf("A 金币 = %d", goldA)
	}
	if _, _, goldB, _ := springDBState(t, st, uidB); goldB != springWireSeedPoint+1000 {
		t.Fatalf("B 金币 = %d", goldB)
	}
	if plain := gameCallMaybeErr(t, ts.URL, "/users/receive_login_bonus",
		"no-such-user", "no-such-sk", s0A, s1A, ""); plain != nil {
		t.Fatalf("无效会话应 4xx: %.200s", plain)
	}
}

// ---- 月签到协议 ----

// gameDoc 读完整响应（含 data 之外的 modal 等顶层键）。
func gameDoc(t *testing.T, base, path, uid, sk, s0, s1, query string) map[string]any {
	t.Helper()
	plain := gameCall(t, base, path, uid, sk, s0, s1, query, "_method=GET")
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatalf("响应 JSON: %v: %.200s", err, plain)
	}
	return doc
}

// signinRowsOf 读 data.acc_login_rewards 行集。
func signinRowsOf(t *testing.T, data map[string]any) []any {
	t.Helper()
	rows, ok := data["acc_login_rewards"].([]any)
	if !ok {
		t.Fatalf("data.acc_login_rewards 非数组: %v", data)
	}
	return rows
}

func TestSigninListAndClaim(t *testing.T) {
	ts, st := springServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-signin-wire")
	now := time.Now().Unix()
	wantMonth, day := cn35game.SigninMonth(now)

	show := gameData(t, ts.URL, "/accu_login_activity/", uid, sk, s0, s1, "")
	rows := signinRowsOf(t, show)
	if len(rows) != cn35game.SigninNodeCount {
		t.Fatalf("签到行数 = %d, want 28", len(rows))
	}
	for i, r := range rows {
		row := r.(map[string]any)
		node := i + 1
		if row["day"] != "第"+strconv.Itoa(node)+"天" {
			t.Fatalf("第 %d 行 day = %v", node, row["day"])
		}
		rewards, ok := row["rewards"].([]any)
		if !ok || len(rewards) != 3 {
			t.Fatalf("第 %d 行 rewards 必须 3 项: %v", node, row["rewards"])
		}
		right, ok := row["right_content"].(map[string]any)
		if !ok {
			t.Fatalf("第 %d 行 right_content 缺失", node)
		}
		if node <= day {
			if right["type"] != "button" || right["text"] != "签到" {
				t.Fatalf("第 %d 行应可签到: %v", node, right)
			}
			if right["msg"] != "accu_login_activity/receive?node="+strconv.Itoa(node) {
				t.Fatalf("第 %d 行 msg = %v", node, right["msg"])
			}
		} else if right["type"] != "text" || right["text"] != "未开启" {
			t.Fatalf("第 %d 行应未开启: %v", node, right)
		}
	}
	if got := springUser(t, show)["login_bonus_receivable_sec"]; got == nil {
		t.Fatalf("签到读面也应回推 user 快照")
	}

	// 领取第 1 格（若当月 1 日尚未到则用可领节点）。
	node := 1
	if day < 1 {
		t.Fatalf("异常 day=%d", day)
	}
	full := gameDoc(t, ts.URL, "/accu_login_activity/receive", uid, sk, s0, s1,
		"node="+strconv.Itoa(node))
	if _, ok := full["modal"]; !ok {
		t.Fatalf("签到响应应带 modal: %v", full)
	}
	recv := full["data"].(map[string]any)
	if got := springUser(t, recv)["medal"]; got != float64(25) {
		t.Fatalf("签到后徽章 = %v, want 25", got)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_signin_claims
		WHERE user_id=? AND month_key=?`, uid, wantMonth).Scan(&n); err != nil {
		t.Fatalf("统计签到台账: %v", err)
	}
	if n != 1 {
		t.Fatalf("签到台账 = %d, want 1", n)
	}
	// 重复领取同一节点：modal 提示、不重复入账。
	recv2 := gameData(t, ts.URL, "/accu_login_activity/receive", uid, sk, s0, s1,
		"node="+strconv.Itoa(node))
	if got := springUser(t, recv2)["medal"]; got != float64(25) {
		t.Fatalf("重复签到后徽章 = %v, want 25", got)
	}
	rows2 := signinRowsOf(t, recv2)
	if right := rows2[0].(map[string]any)["right_content"].(map[string]any); right["type"] != "text" {
		t.Fatalf("已领取行应显示文案: %v", right)
	}
}
