// event_wire_test.go M8 限时活动与周常探险协议冒泡：真实 cn35store(SQLite) ×
// cardcatalog × cn35wire 加密通道。覆盖 E1–E8：历史活动列表、活动区域读面
// 与时间窗、活动关推进与完成率奖励幂等、周常切日与开战门禁、周六特级锚点
// 出场表、素材日掉落入库、重启持久化；MLK_CAPTURE_DIR 存在时与原服录播比对。
package cn35game_test

import (
	"encoding/json"
	mathrand "math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

var m8Loc = time.FixedZone("UTC+8", 8*3600)

// m8Clock 可调测试时钟。
type m8Clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *m8Clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *m8Clock) Set(t time.Time) { c.mu.Lock(); c.now = t; c.mu.Unlock() }

// 2017-11-25 周六 12:00（录播当天）；活动 44 窗口 11-23 12:00 ～ 11-30 11:00。
var (
	m8Saturday = time.Date(2017, 11, 25, 12, 0, 0, 0, m8Loc)
	m8Window   = cn35game.EventWindow{
		OpenAt:  time.Date(2017, 11, 23, 12, 0, 0, 0, m8Loc),
		CloseAt: time.Date(2017, 11, 30, 11, 0, 0, 0, m8Loc),
	}
)

func m8Server(t *testing.T, dbPath string, clock *m8Clock, windows map[int]cn35game.EventWindow) (*httptest.Server, *cn35store.Store) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(dbPath)
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts: st, Sessions: st, Inventory: st, Decks: st, Quests: st, Battles: st,
		Items: st, ItemUse: st, Presents: st, PresentReceive: st, Events: st,
		Catalog: cat, ServerHost: "127.0.0.1:18091",
		AssetsDir:    filepath.Join("..", "..", "assets", "cn350"),
		BattleRand:   mathrand.New(mathrand.NewSource(7)),
		EventClock:   clock.Now,
		EventWindows: windows,
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(srv.Handler()), st
}

type m8Session struct {
	t               *testing.T
	base            string
	uid, sk, s0, s1 string
}

func (s m8Session) get(path, query string) map[string]any {
	return gameJSON(s.t, s.base, path, s.uid, s.sk, s.s0, s.s1, query, "_method=GET")
}

// fight execute→use_ap→result，返回 (execute 文档, result 文档)。
func (s m8Session) fight(questID int, diff string) (map[string]any, map[string]any) {
	s.t.Helper()
	q := "mode=quest&id=" + strconv.Itoa(questID) + "&party_id=001"
	if diff != "" {
		q += "&difficulty_id=" + diff
	}
	ex := s.get("/quests/execute/"+strconv.Itoa(questID)+".json", q)
	u, ok := ex["ap_use_url"].(string)
	if !ok {
		return ex, nil
	}
	bid := strings.SplitN(u, "bid=", 2)[1]
	s.get("/quests/use_ap", "bid="+bid)
	return ex, s.get("/quests/result", "bid="+bid+"&time=60&mc=0")
}

func m8Login(t *testing.T, ts *httptest.Server, st *cn35store.Store, device string) m8Session {
	uid, sk, s0, s1 := establishSession(t, ts.URL, device)
	seedBattleUnits(t, st, uid)
	setAPNow(t, st, uid, 9999)
	return m8Session{t: t, base: ts.URL, uid: uid, sk: sk, s0: s0, s1: s1}
}

func m8Data(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	d, ok := doc["data"].(map[string]any)
	if !ok {
		t.Fatalf("缺 data 信封: %v", doc)
	}
	return d
}

func TestM8ParseEventWindows(t *testing.T) {
	w, err := cn35game.ParseEventWindows("44=2026-11-20T12:00:00+08:00/2026-12-04T11:00:00+08:00; 45=2026-12-01T00:00:00Z/2026-12-02T00:00:00Z")
	if err != nil || len(w) != 2 || !w[44].Configured() {
		t.Fatalf("解析失败: %v %v", w, err)
	}
	if !w[44].Open(time.Date(2026, 11, 25, 0, 0, 0, 0, m8Loc)) || w[44].Open(time.Date(2026, 12, 4, 11, 0, 0, 0, m8Loc)) {
		t.Fatal("窗口应为 [开始, 结束)")
	}
	for _, bad := range []string{"44", "x=a/b", "44=2026-11-20T12:00:00+08:00", "44=2026-12-04T11:00:00+08:00/2026-11-20T12:00:00+08:00",
		"44=2026-11-20T12:00:00+08:00/2026-12-04T11:00:00+08:00;44=2026-11-20T12:00:00+08:00/2026-12-04T11:00:00+08:00"} {
		if _, err := cn35game.ParseEventWindows(bad); err == nil {
			t.Fatalf("应拒绝 %q", bad)
		}
	}
	if w, err := cn35game.ParseEventWindows(""); err != nil || len(w) != 0 {
		t.Fatal("空串=无覆盖")
	}
}

// M801/M802：列表、滚动 release_limit、当期活动置顶、consts 开关。
func TestM8EventsListAndConsts(t *testing.T) {
	clock := &m8Clock{now: m8Saturday}
	dir := t.TempDir()
	// 窗口外（无配置=默认关闭）。
	ts, st := m8Server(t, filepath.Join(dir, "a.sqlite3"), clock, nil)
	s := m8Login(t, ts, st, "m8-list-a")
	rows := m8Data(t, s.get("/events/", ""))["events"].([]any)
	if len(rows) != 42 {
		t.Fatalf("窗口外应仅 42 条历史活动: %d", len(rows))
	}
	first := rows[0].(map[string]any)
	if first["id"].(float64) != 1 || first["name"] != "远古女王和石头守卫" {
		t.Fatalf("首行应为活动 1: %v", first)
	}
	if rl := int64(first["release_limit"].(float64)); rl < m8Saturday.Add(364*24*time.Hour).Unix() {
		t.Fatalf("历史行 release_limit 应为滚动 +365 天: %d", rl)
	}
	opt := first["release_event_select_option"].(map[string]any)
	if opt["parts"].(map[string]any)["QustAreaSelect"].(map[string]any)["data_key"] != "areas_1001" {
		t.Fatalf("data_key: %v", opt)
	}
	if _, ok := opt["requireData"].(map[string]any)["user_event_condition_1"]; !ok {
		t.Fatalf("requireData 缺推进边界键: %v", opt)
	}
	// 100004 → areas_100002（不能按公式推导）。
	found := false
	for _, r := range rows {
		m := r.(map[string]any)
		if m["id"].(float64) == 100004 {
			dk := m["release_event_select_option"].(map[string]any)["parts"].(map[string]any)["QustAreaSelect"].(map[string]any)["data_key"]
			found = dk == "areas_100002"
		}
	}
	if !found {
		t.Fatal("100004 应映射 areas_100002")
	}
	preset := s.get("/users/preset_data.json", "")
	consts := m8Data(t, preset)["consts"].(map[string]any)
	if consts["event_mode_released"] != true || consts["trials_mode_released"] != false {
		t.Fatalf("consts 开关: event=%v trials=%v", consts["event_mode_released"], consts["trials_mode_released"])
	}
	if doc := s.get("/events/release", "id=1"); doc["status"] != "success" {
		t.Fatalf("/events/release 兜底: %v", doc)
	}
	ts.Close()
	st.Close()

	// 窗口内：活动 44 置顶，release_limit=close_at。
	ts, st = m8Server(t, filepath.Join(dir, "b.sqlite3"), clock, map[int]cn35game.EventWindow{44: m8Window})
	defer ts.Close()
	defer st.Close()
	s = m8Login(t, ts, st, "m8-list-b")
	rows = m8Data(t, s.get("/events/", ""))["events"].([]any)
	if len(rows) != 43 {
		t.Fatalf("窗口内应 43 条: %d", len(rows))
	}
	top := rows[0].(map[string]any)
	if top["id"].(float64) != 44 || int64(top["release_limit"].(float64)) != m8Window.CloseAt.Unix() {
		t.Fatalf("当期活动应置顶且 release_limit=close_at: %v", top)
	}
}

// 录播字段集合（docs/17 §4.2、§5.1）：服务端行必须是其超集。
var (
	m8CaptureAreaKeys  = []string{"id", "name", "description", "stage_id", "monster_attr_id", "achievement_key", "percentage", "no_difficulty", "bonus_label", "area_image_id"}
	m8CaptureQuestKeys = []string{"id", "name", "use_ap_map", "use_ap", "use_cp", "background_id", "initial_power", "background", "powerup_conf",
		"wave_conf", "weather", "slash_rate", "rush_rate", "hit_rate", "arrow_rate", "magic_rate", "gun_rate", "heal_rate",
		"help_base_sec", "help_sec_level_rate", "seed_atack_rate", "continuable", "unit_count_limit", "unit_help_rate",
		"attr_id", "monster_label", "boss_label", "weapon_rate_hash", "area_id", "save_current_quest", "current_quest_asset_name",
		"difficulty_names", "retriable_ended_at", "wave_count"}
	m8EventOnlyKeys = []string{"normal_clear", "hard_clear", "very_hard_clear", "quest_select_option"}
)

func m8RequireKeys(t *testing.T, what string, doc map[string]any, keys []string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := doc[k]; !ok {
			t.Fatalf("%s 缺录播字段 %s: %v", what, k, doc)
		}
	}
}

// M803/M804/M811：活动区域读面、历史空区域、窗口外。
func TestM8AreasEventReadFace(t *testing.T) {
	clock := &m8Clock{now: m8Saturday}
	ts, st := m8Server(t, filepath.Join(t.TempDir(), "m8.sqlite3"), clock, map[int]cn35game.EventWindow{44: m8Window})
	defer ts.Close()
	defer st.Close()
	s := m8Login(t, ts, st, "m8-areas")

	d := m8Data(t, s.get("/areas/event", "event_id=44"))
	areas := d["areas_1044"].([]any)
	if len(areas) != 3 {
		t.Fatalf("应 3 个区域: %v", areas)
	}
	for _, a := range areas {
		m8RequireKeys(t, "区域行", a.(map[string]any), m8CaptureAreaKeys)
	}
	for _, aid := range []string{"4176", "4177", "4178"} {
		qs := d["quests_"+aid].([]any)
		if len(qs) != 4 {
			t.Fatalf("quests_%s 应 4 关: %d", aid, len(qs))
		}
		for _, q := range qs {
			qm := q.(map[string]any)
			m8RequireKeys(t, "关卡行", qm, append(append([]string{}, m8CaptureQuestKeys...), m8EventOnlyKeys...))
			if qm["unit_help_rate"].(float64) != 0.6 || qm["current_quest_asset_name"] != "common/icon-current_quest_countryevent" {
				t.Fatalf("活动关公共字段: %v", qm)
			}
		}
	}
	q0 := d["quests_4176"].([]any)[0].(map[string]any)
	if q0["id"].(float64) != 1440 || q0["use_ap"].(float64) != 5 || q0["use_ap_map"].(map[string]any)["impossible"].(float64) != 15 {
		t.Fatalf("1440 AP: %v", q0)
	}
	if q0["retriable_ended_at"].(float64) != float64(m8Window.CloseAt.Unix()) {
		t.Fatalf("retriable_ended_at 应为窗口结束: %v", q0["retriable_ended_at"])
	}
	boss := d["quests_4176"].([]any)[3].(map[string]any)
	if boss["boss_label"] != "迫伦斯" || boss["monster_label"] != nil || boss["initial_power"].(float64) != 2000 {
		t.Fatalf("1443 Boss 关: %v", boss)
	}
	cond := d["user_event_condition_44"].(map[string]any)
	if cond["area_id"].(float64) != 4176 || cond["quest_id"].(float64) != 1440 {
		t.Fatalf("新账号推进边界: %v", cond)
	}
	// /quests/?area_id= 与 E2 同键同形。
	ql := m8Data(t, s.get("/quests/", "area_id=4177"))["quests_4177"].([]any)
	if len(ql) != 4 || ql[0].(map[string]any)["id"].(float64) != 1444 {
		t.Fatalf("/quests/?area_id=4177: %v", ql)
	}
	// 历史活动：空区域 + 边界 0（D1）。
	pd := m8Data(t, s.get("/areas/event", "event_id=1"))
	if a, ok := pd["areas_1001"].([]any); !ok || len(a) != 0 {
		t.Fatalf("历史活动应空区域: %v", pd)
	}
	if c := pd["user_event_condition_1"].(map[string]any); c["area_id"].(float64) != 0 || c["quest_id"].(float64) != 0 {
		t.Fatalf("历史活动边界: %v", c)
	}
	// 窗口外：空区域、开战拒绝且不扣 AP。
	clock.Set(m8Window.CloseAt)
	cd := m8Data(t, s.get("/areas/event", "event_id=44"))
	if len(cd["areas_1044"].([]any)) != 0 {
		t.Fatalf("窗口外应空区域: %v", cd)
	}
	if ql := m8Data(t, s.get("/quests/", "area_id=4176"))["quests_4176"].([]any); len(ql) != 0 {
		t.Fatalf("窗口外 /quests 应空: %v", ql)
	}
	ex := s.get("/quests/execute/1440.json", "mode=quest&id=1440&difficulty_id=normal&party_id=001")
	if _, ok := ex["modal"]; !ok {
		t.Fatalf("窗口外开战应拒绝: %v", ex)
	}
	if ap := m8AP(t, st, s.uid); ap != 9999 {
		t.Fatalf("拒绝开战不得扣 AP: %d", ap)
	}
}

func m8AP(t *testing.T, st *cn35store.Store, uid string) int {
	var ap int
	if err := st.DB().QueryRow(`SELECT ap FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&ap); err != nil {
		t.Fatal(err)
	}
	return ap
}

func m8Count(t *testing.T, st *cn35store.Store, q string, args ...any) int {
	var n int
	if err := st.DB().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// M805/M809/M810/M812：活动推进、炼狱拒绝、完成率奖励幂等、重启保持、主线指针不变。
func TestM8EventBattleProgressAndAreaRewards(t *testing.T) {
	clock := &m8Clock{now: m8Saturday}
	dbPath := filepath.Join(t.TempDir(), "m8.sqlite3")
	windows := map[int]cn35game.EventWindow{44: m8Window}
	ts, st := m8Server(t, dbPath, clock, windows)
	s := m8Login(t, ts, st, "m8-battle")
	storyBefore := m8Count(t, st, `SELECT quest_id FROM cn35_quest_progress WHERE user_id=?`, s.uid)

	// 未解锁关卡拒绝；炼狱拒绝。
	if ex := s.get("/quests/execute/1441.json", "mode=quest&id=1441&difficulty_id=normal&party_id=001"); ex["modal"] == nil {
		t.Fatalf("1441 未解锁应拒绝: %v", ex)
	}
	if ex := s.get("/quests/execute/1440.json", "mode=quest&id=1440&difficulty_id=impossible&party_id=001"); ex["modal"] == nil {
		t.Fatalf("炼狱应拒绝: %v", ex)
	}
	ex, res := s.fight(1440, "normal")
	if res == nil || res["status"] != "success" {
		t.Fatalf("1440 结算失败: ex=%.300v res=%v", ex, res)
	}
	q := ex["quest"].(map[string]any)
	if q["use_ap"].(float64) != 5 || q["unit_help_rate"].(float64) != 0.6 || q["background_id"].(float64) != 5 {
		t.Fatalf("execute quest 字段: %v", q)
	}
	m8RequireKeys(t, "execute.quest", q, append([]string{"difficulty_id", "difficulty_name", "difficulty_option_name", "is_hard_mode"}, m8CaptureQuestKeys[:len(m8CaptureQuestKeys)-1]...))
	if _, ok := m8Data(t, res)["quests_4176"]; !ok {
		t.Fatalf("结算应回推 quests_4176: %v", res)
	}
	if res["progress_rate_reward_message"] != nil {
		t.Fatal("1/12 不应触发完成率奖励")
	}
	cond := m8Data(t, s.get("/areas/event", "event_id=44"))["user_event_condition_44"].(map[string]any)
	if cond["quest_id"].(float64) != 1441 {
		t.Fatalf("通关 1440 后边界应为 1441: %v", cond)
	}
	// 再打 1440 困难/极难 + 1441 普通 → 4/12 = 33% ≥ 30%。
	s.fight(1440, "hard")
	s.fight(1440, "very_hard")
	_, res = s.fight(1441, "normal")
	msg, ok := res["progress_rate_reward_message"].(map[string]any)
	if !ok || !strings.Contains(msg["body"].(string), "钻石 1个") || !strings.Contains(msg["body"].(string), "夏尔梅") {
		t.Fatalf("应发放 30%% 档奖励: %v", res)
	}
	if n := m8Count(t, st, `SELECT count(*) FROM cn35_event_area_rewards WHERE user_id=? AND area_id=4176 AND threshold=30`, s.uid); n != 1 {
		t.Fatalf("台账应 1 行: %d", n)
	}
	if n := m8Count(t, st, `SELECT count(*) FROM cn35_user_presents WHERE user_id=? AND uuid LIKE 'evt-%'`, s.uid); n != 2 {
		t.Fatalf("礼物盒应 2 件（钻石+夏尔梅）: %d", n)
	}
	if n := m8Count(t, st, `SELECT count(*) FROM cn35_user_presents WHERE user_id=? AND content_type='unit' AND content_id=1274`, s.uid); n != 1 {
		t.Fatalf("夏尔梅礼物: %d", n)
	}
	// 读面对账与并发重放不重复发奖。
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.get("/areas/event", "event_id=44") }()
	}
	wg.Wait()
	if n := m8Count(t, st, `SELECT count(*) FROM cn35_user_presents WHERE user_id=? AND uuid LIKE 'evt-%'`, s.uid); n != 2 {
		t.Fatalf("重放后礼物不应增加: %d", n)
	}
	if after := m8Count(t, st, `SELECT quest_id FROM cn35_quest_progress WHERE user_id=?`, s.uid); after != storyBefore {
		t.Fatalf("活动关不得改主线指针: %d → %d", storyBefore, after)
	}
	pct := m8Data(t, s.get("/areas/event", "event_id=44"))["areas_1044"].([]any)[0].(map[string]any)["percentage"].(float64)
	if pct != 33 {
		t.Fatalf("魔菇大街完成率应 33: %v", pct)
	}
	ts.Close()
	st.Close()

	// 重启：边界、通关、台账保持；窗口关闭再重开进度继续。
	clock.Set(m8Window.CloseAt.Add(time.Hour))
	ts, st = m8Server(t, dbPath, clock, windows)
	defer ts.Close()
	defer st.Close()
	s2 := m8Session{t: t, base: ts.URL}
	s2.uid, s2.sk, s2.s0, s2.s1 = establishSession(t, ts.URL, "m8-battle")
	if s2.uid != s.uid {
		t.Fatalf("同设备应同账号: %s vs %s", s2.uid, s.uid)
	}
	if ex := s2.get("/quests/execute/1442.json", "mode=quest&id=1442&difficulty_id=normal&party_id=001"); ex["modal"] == nil {
		t.Fatal("窗口外开战应拒绝")
	}
	clock.Set(m8Saturday)
	cond = m8Data(t, s2.get("/areas/event", "event_id=44"))["user_event_condition_44"].(map[string]any)
	if cond["quest_id"].(float64) != 1442 {
		t.Fatalf("重启/重开后边界应保持 1442: %v", cond)
	}
	if n := m8Count(t, st, `SELECT count(*) FROM cn35_event_area_rewards WHERE user_id=?`, s.uid); n != 1 {
		t.Fatalf("重启后台账: %d", n)
	}
	// Boss 关替代模板可开战（gauge=true + 替代模板 36）。
	for _, qid := range []int{1442} {
		if _, r := s2.fight(qid, "normal"); r == nil || r["status"] != "success" {
			t.Fatalf("%d 结算: %v", qid, r)
		}
	}
	ex, res = s2.fight(1443, "normal")
	if res == nil || res["status"] != "success" {
		t.Fatalf("Boss 关结算: %v", ex)
	}
	if !m8HasGaugeMonster(ex, 36) {
		t.Fatalf("1443 应出现替代 Boss 36（gauge）: %v", ex["appear_table"])
	}
}

func m8HasGaugeMonster(ex map[string]any, id int) bool {
	for _, w := range ex["appear_table"].(map[string]any) {
		for _, e := range w.(map[string]any) {
			em := e.(map[string]any)
			if em["gauge"] == true && int(em["id"].(float64)) == id {
				return true
			}
		}
	}
	return false
}

// m8Spawn 出场项摘要（波次/时间/怪物/缩放）。
type m8Spawn struct {
	wave, ms, id int
	scale        float64
}

func m8Spawns(table map[string]any) []m8Spawn {
	var out []m8Spawn
	for w, wv := range table {
		wn, _ := strconv.Atoi(w)
		for ms, ev := range wv.(map[string]any) {
			mn, _ := strconv.Atoi(ms)
			em := ev.(map[string]any)
			out = append(out, m8Spawn{wn, mn, int(em["id"].(float64)), em["scale"].(float64)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].wave != out[j].wave {
			return out[i].wave < out[j].wave
		}
		return out[i].ms < out[j].ms
	})
	return out
}

// M806/M807/M808：周常读面、切日、开战门禁、周六特级锚点、素材日入库。
func TestM8DailyQuests(t *testing.T) {
	clock := &m8Clock{now: m8Saturday}
	ts, st := m8Server(t, filepath.Join(t.TempDir(), "m8.sqlite3"), clock, nil)
	defer ts.Close()
	defer st.Close()
	s := m8Login(t, ts, st, "m8-daily")

	d := m8Data(t, s.get("/quests/daily/", ""))
	rows, ok := d["daily_quests_6"].([]any)
	if !ok || len(rows) != 4 {
		t.Fatalf("周六应下发 daily_quests_6 四档: %v", d)
	}
	wantAP := []float64{10, 20, 30, 40}
	wantName := []string{"初级", "中级", "高级", "特级"}
	for i, r := range rows {
		m := r.(map[string]any)
		m8RequireKeys(t, "周常行", m, append(append([]string{}, m8CaptureQuestKeys...), "clear_count"))
		if m["id"].(float64) != float64(80061+i) || m["use_ap"].(float64) != wantAP[i] || m["name"] != "周六探险" ||
			m["difficulty_names"].(map[string]any)["0"] != wantName[i] || m["clear_count"].(float64) != 0 {
			t.Fatalf("周六第 %d 档: %v", i+1, m)
		}
		if m["retriable_ended_at"].(float64) != float64(time.Date(2017, 11, 25, 23, 59, 59, 0, m8Loc).Unix()) {
			t.Fatalf("retriable_ended_at 应为当日 23:59:59: %v", m["retriable_ended_at"])
		}
	}
	// ?wday= 兼容读面：额外下发指定星期键。
	if _, ok := m8Data(t, s.get("/quests/daily", "wday=1"))["daily_quests_1"]; !ok {
		t.Fatal("?wday=1 应额外下发 daily_quests_1")
	}
	// 非当天关卡拒绝且不扣 AP；非 normal 难度拒绝。
	if ex := s.get("/quests/execute/80014.json", "mode=quest&id=80014&party_id=001"); ex["modal"] == nil {
		t.Fatalf("周六开战周一关卡应拒绝: %v", ex)
	}
	if ex := s.get("/quests/execute/80064.json", "mode=quest&id=80064&difficulty_id=hard&party_id=001"); ex["modal"] == nil {
		t.Fatalf("周常非 normal 难度应拒绝: %v", ex)
	}
	if ap := m8AP(t, st, s.uid); ap != 9999 {
		t.Fatalf("拒绝开战不得扣 AP: %d", ap)
	}
	storyBefore := m8Count(t, st, `SELECT quest_id FROM cn35_quest_progress WHERE user_id=?`, s.uid)
	goldBefore := m8Count(t, st, `SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, s.uid)
	ex, res := s.fight(80064, "")
	if res == nil || res["status"] != "success" {
		t.Fatalf("80064 结算: ex=%.400v res=%v", ex, res)
	}
	q := ex["quest"].(map[string]any)
	if q["difficulty_name"] != "特级" || q["difficulty_option_name"] != "周六" || q["use_ap"].(float64) != 40 || q["area_id"] != nil {
		t.Fatalf("80064 quest 回显: %v", q)
	}
	if ex["next_page_message"] != "name=StageSelect&base=Common/Pages/StageSelect" || ex["exp"].(float64) < 800 {
		t.Fatalf("80064 next_page/exp: %v %v", ex["next_page_message"], ex["exp"])
	}
	spawns := m8Spawns(ex["appear_table"].(map[string]any))
	if len(spawns) != 38 {
		t.Fatalf("周六特级应 38 只: %d", len(spawns))
	}
	if sp := spawns[0]; sp != (m8Spawn{1, 2000, 69, 1.25652}) {
		t.Fatalf("首只: %+v", sp)
	}
	if sp := spawns[37]; sp != (m8Spawn{3, 26750, 82, 1.767}) {
		t.Fatalf("末只: %+v", sp)
	}
	m8CompareCaptureExecute(t, ex, spawns)
	goldAfter := m8Count(t, st, `SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, s.uid)
	if gain := goldAfter - goldBefore; gain < 9985 {
		t.Fatalf("周六特级金币入账应 ≥ 9985: %d", gain)
	}
	dq := m8Data(t, res)["daily_quests_6"].([]any)
	if dq[3].(map[string]any)["clear_count"].(float64) != 1 {
		t.Fatalf("结算回推 clear_count: %v", dq[3])
	}
	if after := m8Count(t, st, `SELECT quest_id FROM cn35_quest_progress WHERE user_id=?`, s.uid); after != storyBefore {
		t.Fatalf("周常不得改主线指针: %d → %d", storyBefore, after)
	}

	// 切日：周六 23:59:59 仍为 6，周日 00:00 变 0。
	clock.Set(time.Date(2017, 11, 25, 23, 59, 59, 0, m8Loc))
	if _, ok := m8Data(t, s.get("/quests/daily", ""))["daily_quests_6"]; !ok {
		t.Fatal("23:59:59 仍应为周六")
	}
	clock.Set(time.Date(2017, 11, 26, 0, 0, 0, 0, m8Loc))
	sun := m8Data(t, s.get("/quests/daily", ""))
	if _, ok := sun["daily_quests_0"]; !ok {
		t.Fatalf("00:00 应切到周日: %v", sun)
	}
	if ex := s.get("/quests/execute/80064.json", "mode=quest&id=80064&party_id=001"); ex["modal"] == nil {
		t.Fatal("周日不应再能开战周六关卡")
	}

	// 周一（暗素材日）高级：掉暗之花入背包。
	clock.Set(time.Date(2017, 11, 27, 12, 0, 0, 0, m8Loc))
	itemsBefore := m8Count(t, st, `SELECT COALESCE(SUM(quantity),0) FROM cn35_player_items WHERE user_id=? AND item_key='powerup_item_15'`, s.uid)
	ex, res = s.fight(80013, "")
	if res == nil || res["status"] != "success" {
		t.Fatalf("80013 结算: %v", ex)
	}
	itemsAfter := m8Count(t, st, `SELECT COALESCE(SUM(quantity),0) FROM cn35_player_items WHERE user_id=? AND item_key='powerup_item_15'`, s.uid)
	if gain := itemsAfter - itemsBefore; gain < 1 || gain > 2 {
		t.Fatalf("周一高级应掉 1–2 个暗之花: %d", gain)
	}
	for _, sp := range m8Spawns(ex["appear_table"].(map[string]any)) {
		if sp.id != 56 && sp.id != 72 && sp.id != 80 && sp.id != 84 {
			t.Fatalf("周一应只出暗属性怪: %+v", sp)
		}
	}
}

// m8CompareCaptureExecute MLK_CAPTURE_DIR 存在时与原服 quests_execute_104 逐项比对。
func m8CompareCaptureExecute(t *testing.T, ex map[string]any, spawns []m8Spawn) {
	t.Helper()
	root := os.Getenv("MLK_CAPTURE_DIR")
	if root == "" {
		t.Log("skip mlk 录播比对：MLK_CAPTURE_DIR 未设置")
		return
	}
	raw, err := os.ReadFile(filepath.Join(root, "default", "quests_execute_104.json.2.json"))
	if err != nil {
		t.Fatalf("读取录播: %v", err)
	}
	var cap map[string]any
	if err := json.Unmarshal(raw, &cap); err != nil {
		t.Fatal(err)
	}
	want := m8Spawns(cap["appear_table"].(map[string]any))
	if len(want) != len(spawns) {
		t.Fatalf("出场数 %d != 录播 %d", len(spawns), len(want))
	}
	for i := range want {
		if want[i].wave != spawns[i].wave || want[i].ms != spawns[i].ms || want[i].id != spawns[i].id ||
			abs(want[i].scale-spawns[i].scale) > 1e-6 {
			t.Fatalf("第 %d 只与录播不符: got %+v want %+v", i, spawns[i], want[i])
		}
	}
	capQuest := cap["quest"].(map[string]any)
	got := ex["quest"].(map[string]any)
	for k := range capQuest {
		if _, ok := got[k]; !ok {
			t.Fatalf("execute.quest 缺录播字段 %s", k)
		}
	}
	for _, k := range []string{"use_ap_map", "use_ap", "difficulty_names", "difficulty_name", "difficulty_option_name", "wave_conf", "unit_help_rate"} {
		a, _ := json.Marshal(capQuest[k])
		b, _ := json.Marshal(got[k])
		if string(a) != string(b) {
			t.Fatalf("execute.quest.%s 与录播不符: got %s want %s", k, b, a)
		}
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// TestM8CaptureReadFaceReplay MLK_CAPTURE_DIR 存在时与原服 events/areas_event/quests_daily 字段集合比对。
func TestM8CaptureReadFaceReplay(t *testing.T) {
	root := os.Getenv("MLK_CAPTURE_DIR")
	if root == "" {
		t.Skip("MLK_CAPTURE_DIR 未设置，跳过 mlk 录播读面比对")
	}
	load := func(name string) map[string]any {
		raw, err := os.ReadFile(filepath.Join(root, "default", name))
		if err != nil {
			t.Fatalf("读取录播 %s: %v", name, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		return doc["data"].(map[string]any)
	}
	clock := &m8Clock{now: m8Saturday}
	ts, st := m8Server(t, filepath.Join(t.TempDir(), "m8.sqlite3"), clock, map[int]cn35game.EventWindow{44: m8Window})
	defer ts.Close()
	defer st.Close()
	s := m8Login(t, ts, st, "m8-replay")

	capEvents := load("events.json")["events"].([]any)
	gotEvents := m8Data(t, s.get("/events/", ""))["events"].([]any)[1:]
	if len(capEvents) != len(gotEvents) {
		t.Fatalf("历史活动数 %d != 录播 %d", len(gotEvents), len(capEvents))
	}
	for i := range capEvents {
		c, g := capEvents[i].(map[string]any), gotEvents[i].(map[string]any)
		if c["id"] != g["id"] || c["name"] != g["name"] {
			t.Fatalf("第 %d 行与录播不符: %v vs %v", i, g, c)
		}
		cdk := c["release_event_select_option"].(map[string]any)["parts"].(map[string]any)["QustAreaSelect"].(map[string]any)["data_key"]
		gdk := g["release_event_select_option"].(map[string]any)["parts"].(map[string]any)["QustAreaSelect"].(map[string]any)["data_key"]
		if cdk != gdk {
			t.Fatalf("活动 %v data_key %v != 录播 %v", c["id"], gdk, cdk)
		}
	}
	capArea := load("areas_event.json")
	gotArea := m8Data(t, s.get("/areas/event", "event_id=44"))
	for k := range capArea {
		if _, ok := gotArea[k]; !ok {
			t.Fatalf("/areas/event 缺录播顶层键 %s", k)
		}
	}
	for i, ca := range capArea["areas_1044"].([]any) {
		cm, gm := ca.(map[string]any), gotArea["areas_1044"].([]any)[i].(map[string]any)
		for k := range cm {
			if _, ok := gm[k]; !ok {
				t.Fatalf("区域行缺录播字段 %s", k)
			}
		}
		for _, k := range []string{"id", "name", "description", "bonus_label", "stage_id"} {
			if cm[k] != gm[k] {
				t.Fatalf("区域 %v.%s: %v != %v", cm["id"], k, gm[k], cm[k])
			}
		}
	}
	for _, aid := range []string{"quests_4176", "quests_4177", "quests_4178"} {
		for i, cq := range capArea[aid].([]any) {
			cm, gm := cq.(map[string]any), gotArea[aid].([]any)[i].(map[string]any)
			for k := range cm {
				if _, ok := gm[k]; !ok {
					t.Fatalf("%s 关卡行缺录播字段 %s", aid, k)
				}
			}
			for _, k := range []string{"id", "name", "use_ap_map", "attr_id", "monster_label", "boss_label", "wave_count", "wave_conf", "weapon_rate_hash", "difficulty_names", "unit_help_rate", "initial_power"} {
				a, _ := json.Marshal(cm[k])
				b, _ := json.Marshal(gm[k])
				if string(a) != string(b) {
					t.Fatalf("%s #%v.%s: %s != %s", aid, cm["id"], k, b, a)
				}
			}
		}
	}
	capDaily := load("quests_daily.json")["daily_quests_6"].([]any)
	gotDaily := m8Data(t, s.get("/quests/daily", ""))["daily_quests_6"].([]any)
	for i, cq := range capDaily {
		cm, gm := cq.(map[string]any), gotDaily[i].(map[string]any)
		for k := range cm {
			if _, ok := gm[k]; !ok {
				t.Fatalf("周常行缺录播字段 %s", k)
			}
		}
		for _, k := range []string{"name", "use_ap_map", "use_ap", "difficulty_names", "wave_conf", "wave_count", "unit_help_rate", "weapon_rate_hash", "background_id"} {
			a, _ := json.Marshal(cm[k])
			b, _ := json.Marshal(gm[k])
			if string(a) != string(b) {
				t.Fatalf("周常第 %d 档.%s: %s != %s", i+1, k, b, a)
			}
		}
	}
}
