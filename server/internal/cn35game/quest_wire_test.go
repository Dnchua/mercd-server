// quest_wire_test.go STORY350-03 剧情目录读面的协议回放测试：
// 真实 cn35store(SQLite) × 真实 cn35wire 加密通道（httptest +
// cn35protocol 编解码），验证 /stages /areas /quests 的 {data:{key:…}}
// 信封、解锁过滤、通关标记下发、双账号隔离、重启保持与错误路径。
//
// 进度/通关的"写入"在测试里用 SQL 直改模拟 STORY350-07 结算事务的
// 结果——03 只交付读面与 schema，写面语义不在此断言。
package cn35game_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35protocol"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// questServer 装配真实 SQLite 账号/进度存量的测试服。
func questServer(t *testing.T, stateDir string) (*httptest.Server, *cn35store.Store) {
	t.Helper()
	assets := filepath.Join("..", "..", "assets", "cn350")
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:   st,
		Sessions:   st,
		Quests:     st,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  assets,
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

// setProgress / addClear 以 SQL 直写模拟 07 的结算落库结果。
func setProgress(t *testing.T, st *cn35store.Store, uid string, stage, area, quest, hard int) {
	t.Helper()
	if _, err := st.DB().Exec(`UPDATE cn35_quest_progress
		SET stage_id=?,area_id=?,quest_id=?,hard_mode_stage_id=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE user_id=?`, stage, area, quest, hard, uid); err != nil {
		t.Fatalf("setProgress: %v", err)
	}
}

func addClear(t *testing.T, st *cn35store.Store, uid string, questID int, diff string, count int) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO cn35_quest_clears
		(user_id,quest_id,difficulty,clear_count,updated_at) VALUES (?,?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(user_id,quest_id,difficulty) DO UPDATE SET clear_count=excluded.clear_count`,
		uid, questID, diff, count); err != nil {
		t.Fatalf("addClear: %v", err)
	}
}

// gameCallMaybeErr 同 gameCall 但非 200/未加密响应返回 nil（错误路径断言用）。
func gameCallMaybeErr(t *testing.T, base, path, uid, sk, s0, s1, query string) []byte {
	t.Helper()
	url := base + path
	if query != "" {
		e, err := cn35protocol.EncryptQuery(query, "1700000000", s0, s1)
		if err != nil {
			t.Fatal(err)
		}
		url += "?e=" + e
	}
	body, err := cn35protocol.EncryptFormBody("_method=GET", "1700000000", s0, s1)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	id1, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, uid)
	id4, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, sk)
	appT, _ := cn35protocol.EncryptAPP_T("1700000000", s0, s1)
	req.Header.Set("APP_ID_1", id1)
	req.Header.Set("APP_ID_4", id4)
	req.Header.Set("APP_T", appT)
	req.Header.Set("Encrypted", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("ENCRYPTED") != "true" {
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cn35protocol.DecodeGameResponse(raw, s0, s1)
	if err != nil {
		t.Fatalf("响应解密失败: %v", err)
	}
	return plain
}

func docList(t *testing.T, data map[string]any, key string) []any {
	t.Helper()
	v, ok := data[key]
	if !ok {
		t.Fatalf("data 缺键 %s: %v", key, data)
	}
	l, ok := v.([]any)
	if !ok {
		t.Fatalf("data.%s 不是 docList: %v", key, v)
	}
	return l
}

// TestStagesFreshAccount 新账号：只下发 stage_id=1 的 stage（锁定 stage2
// 不进包），五 tab 键齐全，行内注入 area_select_* 协约字段。
func TestStagesFreshAccount(t *testing.T) {
	ts, _ := questServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-q-stages")

	data := gameData(t, ts.URL, "/stages", uid, sk, s0, s1, "")
	stages := docList(t, data, "stages")
	if len(stages) != 1 {
		t.Fatalf("新账号 stages 应只含 stage1: %v", stages)
	}
	st := stages[0].(map[string]any)
	if st["id"].(float64) != 1 {
		t.Fatalf("stage id: %v", st["id"])
	}
	if st["area_select_message"] != "name=AreaSelect&method=Stack" {
		t.Fatalf("area_select_message: %v", st["area_select_message"])
	}
	opt, ok := st["area_select_option"].(map[string]any)
	if !ok {
		t.Fatalf("缺 area_select_option: %v", st)
	}
	parts := opt["parts"].(map[string]any)["QustAreaSelect"].(map[string]any)
	if parts["data_key"] != "areas_1" {
		t.Fatalf("data_key: %v", parts)
	}
	rd := opt["requireData"].(map[string]any)["areas_1"].(map[string]any)
	if rd["path"] != "/areas?stage_id=1" {
		t.Fatalf("requireData.path: %v", rd)
	}
	// 其余 tab 键恒发空表（缺键→客户端空 tab）；events 自 2026-11-23 起
	// 下发与 /events/ 同形的历史活动行（客户端 PastEvent tab 读此键）。
	for _, k := range []string{"hard_mode_stages", "extra_stages", "specials"} {
		if l := docList(t, data, k); len(l) != 0 {
			t.Fatalf("%s 应为空表: %v", k, l)
		}
	}
	for _, ev := range docList(t, data, "events") {
		row := ev.(map[string]any)
		for _, k := range []string{"id", "name", "release_limit", "release_event_select_option"} {
			if _, ok := row[k]; !ok {
				t.Fatalf("events 行缺键 %s: %v", k, row)
			}
		}
	}
}

// TestStagesProgressedAccount 推进后账号仍只见 stage1（story-01 目录
// 当前只配第一章；更深进度指针不会放出未配置 stage）。
func TestStagesProgressedAccount(t *testing.T) {
	ts, st := questServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-q-prog")
	setProgress(t, st, uid, 1, 5, 60, 0)

	stages := docList(t, gameData(t, ts.URL, "/stages", uid, sk, s0, s1, ""), "stages")
	if len(stages) != 1 || stages[0].(map[string]any)["id"].(float64) != 1 {
		t.Fatalf("story-01 目录只下发 stage1: %v", stages)
	}
}

// TestAreasAndQuestsEnvelope areas_<id> 与 quests_<areaId> 同包多键下发；
// 新账号只见 area1/quest1（锁定内容不出现）。
func TestAreasAndQuestsEnvelope(t *testing.T) {
	ts, _ := questServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-q-areas")

	data := gameData(t, ts.URL, "/areas", uid, sk, s0, s1, "stage_id=1")
	areas := docList(t, data, "areas_1")
	if len(areas) != 1 {
		t.Fatalf("新账号 areas_1 应只含 area1: %v", areas)
	}
	ad := areas[0].(map[string]any)
	if ad["id"].(float64) != 1 || ad["name"] == "" {
		t.Fatalf("area 行字段: %v", ad)
	}
	if _, ok := ad["area_image_id"]; !ok {
		t.Fatalf("area 缺 area_image_id: %v", ad)
	}
	if ad["percentage"].(float64) != 0 {
		t.Fatalf("未通关 percentage 应 0: %v", ad["percentage"])
	}

	quests := docList(t, data, "quests_1")
	if len(quests) != 1 {
		t.Fatalf("quests_1 应只含 quest1（前沿 quest_id=1）: %v", quests)
	}
	qd := quests[0].(map[string]any)
	if qd["id"].(float64) != 1 {
		t.Fatalf("quest id: %v", qd)
	}
	// *_clear 硬字段：int 且恒在（客户端 int.Parse 消费）。
	for _, k := range []string{"normal_clear", "hard_clear", "very_hard_clear"} {
		v, ok := qd[k].(float64)
		if !ok || v != 0 {
			t.Fatalf("%s 应为 int 0: %v", k, qd[k])
		}
	}
	// use_ap_map 三难度键（QuestSelect 难度钮）。
	ap := qd["use_ap_map"].(map[string]any)
	for _, d := range []string{"normal", "hard", "very_hard"} {
		if ap[d].(float64) <= 0 {
			t.Fatalf("use_ap_map.%s: %v", d, ap[d])
		}
	}
	// quest_select_option 注入（05 的 parts 键名/data_key/id 契约）。
	// 键名必须是 app_conf 实例键 DifficultySelect（prefab=QuestSelectParts）——
	// 实机取证：写 QuestSelectParts 时部件 option 拿不到 data_key/id。
	opt := qd["quest_select_option"].(map[string]any)
	qsParts := opt["parts"].(map[string]any)["DifficultySelect"].(map[string]any)
	if qsParts["data_key"] != "quests_1" || qsParts["id"] != "1" {
		t.Fatalf("quest_select_option.parts: %v", qsParts)
	}
	qrd := opt["requireData"].(map[string]any)["quests_1"].(map[string]any)
	if qrd["path"] != "/quests?area_id=1" {
		t.Fatalf("quest requireData.path: %v", qrd)
	}
	// 未解锁 area2 的 quests_2 键不下发（区域未出现）。
	if _, bad := data["quests_2"]; bad {
		t.Fatalf("锁定区域不应下发 quests_2: %v", data)
	}
}

// TestQuestsClearedAndPercentage 已通关账号：clear 计数下发到关卡文档，
// area percentage 按全量关卡（含未解锁）×3 难度计算。
func TestQuestsClearedAndPercentage(t *testing.T) {
	ts, st := questServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-q-clear")
	// 推进到 area2（子区域）后段：quest20 前沿；area1+2 解锁，
	// 两区各 4 关（quest1-8）全部在解锁范围内。
	setProgress(t, st, uid, 1, 2, 20, 0)
	addClear(t, st, uid, 1, "normal", 2)
	addClear(t, st, uid, 1, "hard", 1)
	addClear(t, st, uid, 3, "normal", 1)

	data := gameData(t, ts.URL, "/areas", uid, sk, s0, s1, "stage_id=1")
	areas := docList(t, data, "areas_1")
	if len(areas) != 2 {
		t.Fatalf("area_id=2 应见 area1+2: %v", areas)
	}
	// area1 全 4 关 ×3 难度分母 12；clears：q1 normal+hard, q3 normal
	// → 3/12 → 25。
	if pct := areas[0].(map[string]any)["percentage"].(float64); pct != 25 {
		t.Fatalf("area1 percentage=%v want 25", pct)
	}
	q1 := docList(t, data, "quests_1")
	if len(q1) != 4 {
		t.Fatalf("quests_1 应 4 关全解锁: %v", len(q1))
	}
	got := map[int]map[string]any{}
	for _, q := range q1 {
		m := q.(map[string]any)
		got[int(m["id"].(float64))] = m
	}
	if got[1]["normal_clear"].(float64) != 2 || got[1]["hard_clear"].(float64) != 1 || got[1]["very_hard_clear"].(float64) != 0 {
		t.Fatalf("quest1 clears: %v", got[1])
	}
	if got[3]["normal_clear"].(float64) != 1 {
		t.Fatalf("quest3 clears: %v", got[3])
	}
	// area2（埃诺丘陵）4 关 quest5-8 全部 ≤ 前沿 quest20 解锁。
	q2 := docList(t, data, "quests_2")
	if len(q2) != 4 || q2[0].(map[string]any)["id"].(float64) != 5 ||
		q2[3].(map[string]any)["id"].(float64) != 8 {
		t.Fatalf("quests_2 应含 quest5-8: %v", q2)
	}
	// /quests 独立端点同数据
	q1solo := docList(t, gameData(t, ts.URL, "/quests", uid, sk, s0, s1, "area_id=1"), "quests_1")
	if len(q1solo) != 4 {
		t.Fatalf("/quests?area_id=1 应同 4 关: %v", len(q1solo))
	}
}

// TestQuestsLockedAndError 锁定区域/未知 id 返空表；坏参返非 2xx。
func TestQuestsLockedAndError(t *testing.T) {
	ts, _ := questServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-q-lock")

	// 锁定 stage → areas_2 空表 200
	if l := docList(t, gameData(t, ts.URL, "/areas", uid, sk, s0, s1, "stage_id=2"), "areas_2"); len(l) != 0 {
		t.Fatalf("锁定 stage 应空: %v", l)
	}
	// 锁定 area → quests_2 空表
	if l := docList(t, gameData(t, ts.URL, "/quests", uid, sk, s0, s1, "area_id=2"), "quests_2"); len(l) != 0 {
		t.Fatalf("锁定 area 应空: %v", l)
	}
	// 未知 id → 空表（不是错误）
	if l := docList(t, gameData(t, ts.URL, "/quests", uid, sk, s0, s1, "area_id=99"), "quests_99"); len(l) != 0 {
		t.Fatalf("未知 area 应空: %v", l)
	}
	if l := docList(t, gameData(t, ts.URL, "/areas", uid, sk, s0, s1, "stage_id=99"), "areas_99"); len(l) != 0 {
		t.Fatalf("未知 stage 应空: %v", l)
	}
	// 坏参 → 非 2xx
	for _, tc := range []struct{ path, query string }{
		{"/quests", ""},
		{"/quests", "area_id=abc"},
		{"/areas", ""},
		{"/areas", "stage_id=xyz"},
	} {
		if plain := gameCallMaybeErr(t, ts.URL, tc.path, uid, sk, s0, s1, tc.query); plain != nil {
			t.Fatalf("%s?%s 应非 2xx，得到: %s", tc.path, tc.query, plain)
		}
	}
}

// TestQuestsTwoAccountsIsolation 双账号各自进度/通关互不污染。
func TestQuestsTwoAccountsIsolation(t *testing.T) {
	ts, st := questServer(t, t.TempDir())
	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "dev-q-iso-A")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "dev-q-iso-B")
	setProgress(t, st, uidA, 1, 2, 4, 0)
	addClear(t, st, uidA, 1, "normal", 3)

	qA := docList(t, gameData(t, ts.URL, "/quests", uidA, skA, s0A, s1A, "area_id=1"), "quests_1")
	if len(qA) != 4 {
		t.Fatalf("A 应见 4 关（前沿 quest_id=4）: %v", len(qA))
	}
	qB := docList(t, gameData(t, ts.URL, "/quests", uidB, skB, s0B, s1B, "area_id=1"), "quests_1")
	if len(qB) != 1 {
		t.Fatalf("B 初始只应见 quest1: %v", qB)
	}
	if qb := qB[0].(map[string]any); qb["normal_clear"].(float64) != 0 {
		t.Fatalf("B 的 clear 被 A 污染: %v", qb)
	}
}

// TestQuestsRestartPersistence 服务重启后进度/通关标记保持
// （SQLite 持久化，同一 state 目录重开）。
func TestQuestsRestartPersistence(t *testing.T) {
	dir := t.TempDir()
	ts, st := questServer(t, dir)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-q-restart")
	setProgress(t, st, uid, 1, 2, 4, 0)
	addClear(t, st, uid, 2, "normal", 1)
	ts.Close()
	st.Close()

	// “重启”：同一 state 目录新 Store + 新 httptest。
	ts2, st2 := questServer(t, dir)
	defer st2.Close()
	defer ts2.Close()
	// 会话持久化（cn35_sessions）→ 原 s0/s1/sk 仍有效，直接重放即可。
	data := gameData(t, ts2.URL, "/quests", uid, sk, s0, s1, "area_id=1")
	q := docList(t, data, "quests_1")
	if len(q) != 4 {
		t.Fatalf("重启后 quests_1 应 4 关: %v", len(q))
	}
	var q2 map[string]any
	for _, e := range q {
		if m := e.(map[string]any); m["id"].(float64) == 2 {
			q2 = m
		}
	}
	if q2 == nil || q2["normal_clear"].(float64) != 1 {
		t.Fatalf("重启后 quest2 normal_clear 丢失: %v", q2)
	}
}

// TestPresetDataQuestFields preset_data 的 data.user 带进度字段
// （账号隔离，经快照注入而非 fixture 静态值）。
func TestPresetDataQuestFields(t *testing.T) {
	ts, st := questServer(t, t.TempDir())
	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "dev-q-pd-A")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "dev-q-pd-B")
	setProgress(t, st, uidA, 2, 3, 6, 1)

	uA := gameData(t, ts.URL, "/users/preset_data.json", uidA, skA, s0A, s1A, "")["user"].(map[string]any)
	if uA["stage_id"].(float64) != 2 || uA["area_id"].(float64) != 3 ||
		uA["quest_id"].(float64) != 6 || uA["hard_mode_stage_id"].(float64) != 1 {
		t.Fatalf("A 进度字段: %v", uA)
	}
	uB := gameData(t, ts.URL, "/users/preset_data.json", uidB, skB, s0B, s1B, "")["user"].(map[string]any)
	if uB["stage_id"].(float64) != 1 || uB["quest_id"].(float64) != 1 || uB["hard_mode_stage_id"].(float64) != 0 {
		t.Fatalf("B 进度应为初始: %v", uB)
	}
}
