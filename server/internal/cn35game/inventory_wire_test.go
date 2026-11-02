// inventory_wire_test.go PLAY350-07 库存读面契约测试：
// 真实 SQLite 库存（cn35store）× 真实 cardcatalog × 加密 game 通道。
//
// 覆盖卡面要求：加密收发 / 双账号隔离 / 空库存 / 详情所需字段 /
// 跨账号不可见（且不接受 user_id 参数）/ preset_data 一致性 /
// 重复刷新不重复 / 未绑定会话拒绝 / 未知模板明确数据错误 /
// 错误响应形态 / 列表查询零 DB 写入。
package cn35game_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35protocol"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

// inventoryServer 装配带真实 SQLite + 真实 catalog 的测试服务。
func inventoryServer(t *testing.T) (*httptest.Server, *cn35store.Store) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		Accounts:   st,
		Sessions:   st,
		Inventory:  st,
		Catalog:    cat,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

func insertUnit(t *testing.T, st *cn35store.Store, uid string, unitID, level int) {
	t.Helper()
	insertUnitDup(t, st, uid, unitID, level, 0)
}

// insertUnitDup 直插实例态（含觉醒次数）；绕过领域校验以构造边界/脏数据向量。
func insertUnitDup(t *testing.T, st *cn35store.Store, uid string, unitID, level, dup int) {
	t.Helper()
	if err := st.WithTx(func(tx *sql.Tx) error {
		return cn35store.InsertUnitTx(tx, uid, cn35game.UnitRecord{
			UnitID: unitID, Level: level, DuplicatedCount: dup,
		})
	}); err != nil {
		t.Fatalf("插入单位 %d: %v", unitID, err)
	}
}

func insertMonster(t *testing.T, st *cn35store.Store, uid, uuid string, monsterID, level int) {
	t.Helper()
	if err := st.WithTx(func(tx *sql.Tx) error {
		return cn35store.InsertMonsterTx(tx, uid, cn35game.MonsterRecord{
			UUID: uuid, MonsterID: monsterID, Level: level,
		})
	}); err != nil {
		t.Fatalf("插入魔宠 %s: %v", uuid, err)
	}
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func insertMonsterFull(t *testing.T, st *cn35store.Store, uid string, r cn35game.MonsterRecord) {
	t.Helper()
	if err := st.WithTx(func(tx *sql.Tx) error {
		return cn35store.InsertMonsterTx(tx, uid, r)
	}); err != nil {
		t.Fatalf("插入魔宠 %s: %v", r.UUID, err)
	}
}

func gameData(t *testing.T, base, path, uid, sk, s0, s1, query string) map[string]any {
	t.Helper()
	plain := gameCall(t, base, path, uid, sk, s0, s1, query, "_method=GET")
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatalf("响应 JSON: %v: %.200s", err, plain)
	}
	data, ok := doc["data"].(map[string]any)
	if !ok {
		t.Fatalf("data 封套缺失: %.200s", plain)
	}
	return data
}

func rowByID(t *testing.T, rows []any, id float64) map[string]any {
	t.Helper()
	for _, r := range rows {
		if m := r.(map[string]any); m["id"] == id {
			return m
		}
	}
	t.Fatalf("id=%v 行缺失: %v", id, rows)
	return nil
}

// TestInventoryListChain 双账号端到端：加密收发/隔离/详情字段/
// preset_data 一致/重复刷新/枚举旁路/零写入。
func TestInventoryListChain(t *testing.T) {
	ts, st := inventoryServer(t)
	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "dev-inv-a")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "dev-inv-b")

	insertUnit(t, st, uidA, 152, 1)
	insertUnit(t, st, uidA, 348, 1)
	insertMonster(t, st, uidA, "m-uuid-0001", 1, 1)

	countRows := func() (units, mons, grants int) {
		st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_player_units`).Scan(&units)
		st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_player_monsters`).Scan(&mons)
		st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_inventory_grants`).Scan(&grants)
		return
	}
	u0, m0, g0 := countRows()

	// A 列表：两行单位 + 一条魔宠（双向捆绑键都在）。
	dataA := gameData(t, ts.URL, "/user_units", uidA, skA, s0A, s1A, "")
	units := dataA["user_units"].([]any)
	monsters := dataA["user_monsters"].([]any)
	if len(units) != 2 || len(monsters) != 1 {
		t.Fatalf("A 库存: units=%d monsters=%d", len(units), len(monsters))
	}

	// 152 行字段（catalog 实测值：title 自带[]、weapon=3 大剑、aspd2.4）。
	u152 := rowByID(t, units, 152)
	// uuid 为 unitMap 键（客户端 Quest.AddUnit/Unit.cs:267）：
	// 派生规则 "unit-<unit_id>"，同账号内唯一且跨请求稳定。
	if u152["uuid"] != "unit-152" {
		t.Fatalf("uuid=%v want unit-152", u152["uuid"])
	}
	if u152["name"] != "[火焰剑豪]摩尔沙" {
		t.Fatalf("name=%v", u152["name"])
	}
	if u152["prefix_name"] != "火焰剑豪" || u152["main_name"] != "摩尔沙" {
		t.Fatalf("prefix/main: %v", u152)
	}
	if u152["rare_type_id"].(float64) != 50 || u152["rare_type_name"] != "╂╂╂╂╂" {
		t.Fatalf("rare 字段: %v", u152)
	}
	if u152["attack_ms"].(float64) != 2400 || u152["reach"].(float64) != 30 {
		t.Fatalf("attack_ms/reach: %v", u152)
	}
	if u152["hp"].(float64) != 3680 || u152["at"].(float64) != 3900 {
		t.Fatalf("hp/at: %v", u152)
	}
	// 成长区间与满觉端点（statmodel；152 rare5/type3 → base_max_level 70）。
	if u152["growth_type_id"].(float64) != 3 || u152["base_max_level"].(float64) != 70 {
		t.Fatalf("growth_type_id/base_max_level: %v", u152)
	}
	if u152["hp_min"].(float64) != 3680 || u152["hp_max"].(float64) != 7728 {
		t.Fatalf("hp_min/hp_max: %v", u152)
	}
	if u152["at_min"].(float64) != 3900 || u152["at_max"].(float64) != 8190 {
		t.Fatalf("at_min/at_max: %v", u152)
	}
	if u152["max_duplicated_count"].(float64) != 15 ||
		u152["hp_15_max"].(float64) != 12078 || u152["at_15_max"].(float64) != 12840 {
		t.Fatalf("满觉端点字段: %v", u152)
	}
	if u152["attr_id"].(float64) != 1 || u152["attr_name"] != "炎" {
		t.Fatalf("attr: %v", u152)
	}
	// country_id 为 NUMBER（客户端按 i() 统计同国加成，STRING 恒 0）：
	// >0 且 country 枚举行存在 {id:同值,name:沙漠之国}（见下枚举旁路断言）。
	cid, cidOK := u152["country_id"].(float64)
	if !cidOK || cid <= 0 || u152["sex_id"].(float64) != 1 {
		t.Fatalf("country/sex 枚举键: %v", u152)
	}
	// SP/召唤经济字段【重建策略】：★5 → cost=bonus_sp=100*5=500。
	if u152["cost"].(float64) != 500 || u152["bonus_sp"].(float64) != 500 {
		t.Fatalf("cost/bonus_sp: %v", u152)
	}
	dm, _ := u152["unit_detail_message"].(string)
	if !strings.Contains(dm, "UserUnitDetail") || !strings.Contains(dm, "id=152") {
		t.Fatalf("unit_detail_message: %v", dm)
	}
	if u152["hp_growth_type_name"] != "晩成" {
		t.Fatalf("growth type: %v", u152["hp_growth_type_name"])
	}
	// C0.2：max_level = current_max_level（dup=0 → 70），不与 base_max_level 混用。
	if u152["max_level"].(float64) != 70 {
		t.Fatalf("dup=0 max_level=%v want 70", u152["max_level"])
	}
	// 潜能基线字段（对齐真实国服录包契约：★5 max_upgrade_level=145）。
	if u152["upgrade_level"].(float64) != 1 || u152["max_upgrade_level"].(float64) != 145 {
		t.Fatalf("潜能字段 upgrade_level/max_upgrade_level=%v/%v want 1/145", u152["upgrade_level"], u152["max_upgrade_level"])
	}

	// 魔宠行：uuid 实例键 + 模板数值。
	m1 := monsters[0].(map[string]any)
	if m1["uuid"] != "m-uuid-0001" || m1["id"].(float64) != 1 {
		t.Fatalf("monster 行: %v", m1)
	}
	if _, has := m1["unit_id"]; has {
		t.Fatalf("未装备魔宠不应带 unit_id: %v", m1)
	}
	// 种子技经济字段：魔宠 1 sklsp=250 → cost=250；sklcd=21.9 →
	// call_interval_ms=int(21.9*1000)=21899（浮点截断，与生产样本一致）。
	if m1["cost"].(float64) != 250 || m1["call_interval_ms"].(float64) != 21899 {
		t.Fatalf("魔宠 cost/call_interval_ms: %v", m1)
	}

	// 枚举旁路：country/sex 文档使 Enum() 命中显示名。
	// country 行形态 {id:数值CountryID,name:中文名}，id 须与单位行 country_id 同值。
	country := dataA["country"].([]any)
	foundCountry := false
	for _, c := range country {
		if d := c.(map[string]any); d["id"] == cid && d["name"] == "沙漠之国" {
			foundCountry = true
		}
	}
	if !foundCountry {
		t.Fatalf("country 枚举行缺 {id:%v,name:沙漠之国}: %v", cid, country)
	}
	sex := dataA["sex"].([]any)
	foundSex := false
	for _, s := range sex {
		if d := s.(map[string]any); d["id"].(float64) == 1 && d["name"] == "男" {
			foundSex = true
		}
	}
	if !foundSex {
		t.Fatalf("sex 枚举行缺男: %v", sex)
	}

	// B 隔离：库存为空；即使伪造 ?user_id=A 也只看自己。
	dataB := gameData(t, ts.URL, "/user_units", uidB, skB, s0B, s1B, "user_id="+uidA)
	if len(dataB["user_units"].([]any)) != 0 || len(dataB["user_monsters"].([]any)) != 0 {
		t.Fatalf("B 跨账号泄漏: %v", dataB)
	}

	// 反向接口同形（魔宠键为主但同样捆绑双键）。
	dataM := gameData(t, ts.URL, "/user_monsters", uidA, skA, s0A, s1A, "")
	if len(dataM["user_monsters"].([]any)) != 1 || len(dataM["user_units"].([]any)) != 2 {
		t.Fatalf("user_monsters 捆绑: %v", dataM)
	}

	// preset_data 一致性：同一组装面注入。
	pd := gameData(t, ts.URL, "/users/preset_data.json", uidA, skA, s0A, s1A, "")
	pdUnits := pd["user_units"].([]any)
	if len(pdUnits) != 2 {
		t.Fatalf("preset_data.user_units=%d", len(pdUnits))
	}
	rowByID(t, pdUnits, 152)
	if len(pd["user_monsters"].([]any)) != 1 {
		t.Fatalf("preset_data.user_monsters: %v", pd["user_monsters"])
	}
	user := pd["user"].(map[string]any)
	if v, ok := user["leader_unit_id"].(float64); !ok || v != 348 {
		t.Fatalf("preset leader_unit_id 应为 NUMBER 348: %v", user["leader_unit_id"])
	}

	// 重复刷新不复制。
	dataA2 := gameData(t, ts.URL, "/user_units", uidA, skA, s0A, s1A, "")
	if len(dataA2["user_units"].([]any)) != 2 {
		t.Fatal("重复刷新产生重复记录")
	}

	// 读面零 DB 写入。
	u1, m1c, g1 := countRows()
	if u1 != u0 || m1c != m0 || g1 != g0 {
		t.Fatalf("读操作写库: units %d→%d monsters %d→%d grants %d→%d", u0, u1, m0, m1c, g0, g1)
	}
}

// TestUnitCostBonusSPByRare SP/召唤经济字段重建策略：cost=bonus_sp=100*rare
// （锚点生产样本 152 ★5=500；★1 单位 id=1 → 100）。
func TestUnitCostBonusSPByRare(t *testing.T) {
	ts, st := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-inv-cost")
	insertUnit(t, st, uid, 152, 1) // ★5
	insertUnit(t, st, uid, 1, 1)   // ★1
	units := gameData(t, ts.URL, "/user_units", uid, sk, s0, s1, "")["user_units"].([]any)
	u5 := rowByID(t, units, 152)
	if u5["cost"].(float64) != 500 || u5["bonus_sp"].(float64) != 500 {
		t.Fatalf("★5 cost/bonus_sp: %v", u5)
	}
	u1 := rowByID(t, units, 1)
	if u1["cost"].(float64) != 100 || u1["bonus_sp"].(float64) != 100 {
		t.Fatalf("★1 cost/bonus_sp: %v", u1)
	}
}

// TestInventoryEmptyAccount 空库存合法：键在、值为空数组。
func TestInventoryEmptyAccount(t *testing.T) {
	ts, _ := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-inv-empty")
	data := gameData(t, ts.URL, "/user_units", uid, sk, s0, s1, "")
	if got := data["user_units"].([]any); len(got) != 0 {
		t.Fatalf("空账号 user_units=%v", got)
	}
	if _, ok := data["user_monsters"].([]any); !ok {
		t.Fatal("user_monsters 键缺失")
	}
}

// TestInventoryUnknownTemplate 库存行指向不存在模板 → 明确数据错误
// （500 handler_error 诊断体，note 含模板 ID），不静默伪造。
func TestInventoryUnknownTemplate(t *testing.T) {
	ts, st := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-inv-bad")
	// 绕过 ValidateUnitTemplates 直插（模拟脏数据；生产写入路径会拦）。
	if err := st.WithTx(func(tx *sql.Tx) error {
		return cn35store.InsertUnitTx(tx, uid, cn35game.UnitRecord{UnitID: 999999, Level: 1})
	}); err != nil {
		t.Fatalf("插入脏行: %v", err)
	}

	// gameCall 会校验 ENCRYPTED 头，错误响应是明文诊断——直接发请求。
	body, _ := cn35protocol.EncryptFormBody("_method=GET", "1700000000", s0, s1)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/user_units", strings.NewReader(string(body)))
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
	raw := readAll(t, resp)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("未知模板应 500: %d %.200s", resp.StatusCode, raw)
	}
	var diag map[string]any
	if err := json.Unmarshal(raw, &diag); err != nil {
		t.Fatalf("错误响应非 JSON 诊断体: %.200s", raw)
	}
	if diag["mercd_status"] != "handler_error" {
		t.Fatalf("诊断体 status: %v", diag)
	}
	note, _ := diag["mercd_note"].(string)
	if !strings.Contains(note, "999999") {
		t.Fatalf("note 未含模板 ID: %v", diag)
	}
}

// TestInventoryInvalidSession 未绑定会话的列表请求受控拒绝（非 200）。
func TestInventoryInvalidSession(t *testing.T) {
	ts, _ := inventoryServer(t)
	s0, s1 := strings.Repeat("0", 32), strings.Repeat("1", 32)
	body, _ := cn35protocol.EncryptFormBody("_method=GET", "1700000000", s0, s1)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/user_units", strings.NewReader(string(body)))
	id1, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, "ghost")
	req.Header.Set("APP_ID_1", id1)
	req.Header.Set("Encrypted", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("未绑定会话不得 200")
	}
}

// TestInventoryMonsterEquip 装备魔宠行带宿主 unit_id（装备关系可见性）。
func TestInventoryMonsterEquip(t *testing.T) {
	ts, st := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-inv-eq")
	insertUnit(t, st, uid, 157, 1)
	if err := st.WithTx(func(tx *sql.Tx) error {
		host := 157
		return cn35store.InsertMonsterTx(tx, uid, cn35game.MonsterRecord{
			UUID: "m-eq-1", MonsterID: 1, Level: 1, UnitID: &host,
		})
	}); err != nil {
		t.Fatalf("插入装备魔宠: %v", err)
	}
	data := gameData(t, ts.URL, "/user_monsters", uid, sk, s0, s1, "")
	m := data["user_monsters"].([]any)[0].(map[string]any)
	if m["unit_id"].(float64) != 157 {
		t.Fatalf("装备宿主 unit_id: %v", m)
	}
	u := data["user_units"].([]any)[0].(map[string]any)
	if u["monster_uuid"] != "" {
		t.Fatalf("单位 monster_uuid 应空（库存行未登记装备关系）: %v", u)
	}
}

// TestUnitPanelScalesWithLevel 面板随等级变化（不再是 Lv1 基础值）：
// 152 Lv70 → 未觉醒端点 hp 7728 / at 8190（Lv1 为 3680/3900，含 atk_override）。
func TestUnitPanelScalesWithLevel(t *testing.T) {
	ts, st := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-inv-panel")
	insertUnit(t, st, uid, 152, 70)
	u := rowByID(t, gameData(t, ts.URL, "/user_units", uid, sk, s0, s1, "")["user_units"].([]any), 152)
	if u["hp"].(float64) != 7728 || u["at"].(float64) != 8190 {
		t.Fatalf("Lv70 面板应到端点: hp=%v at=%v", u["hp"], u["at"])
	}
	if u["max_level"].(float64) != 70 || u["base_max_level"].(float64) != 70 {
		t.Fatalf("max_level/base_max_level: %v/%v", u["max_level"], u["base_max_level"])
	}
}

// TestMonsterBasePanel 魔宠 Size=1.0 下 base_hp/base_seed_at 与 hp/at/seed_at
// 同值（catalog 中 500：atk 7968 / life 78987）。
func TestMonsterBasePanel(t *testing.T) {
	ts, st := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-inv-mpanel")
	insertMonster(t, st, uid, "m-panel-1", 500, 1)
	m := gameData(t, ts.URL, "/user_monsters", uid, sk, s0, s1, "")["user_monsters"].([]any)[0].(map[string]any)
	if m["base_hp"].(float64) != 78987 || m["hp"].(float64) != 78987 {
		t.Fatalf("魔宠 base_hp/hp: %v/%v", m["base_hp"], m["hp"])
	}
	if m["base_seed_at"].(float64) != 7968 || m["at"].(float64) != 7968 || m["seed_at"].(float64) != 7968 {
		t.Fatalf("魔宠 base_seed_at/at/seed_at: %v/%v/%v", m["base_seed_at"], m["at"], m["seed_at"])
	}
}

// TestUnitPanelBoundaryVectors 用真实 ★5 模板逐条校验 (level,dup) → 面板/max_level。
func TestUnitPanelBoundaryVectors(t *testing.T) {
	// catalog 中存在且为 ★5 的模板：152（life 3680 / atk_override 3900 / type3）。
	const uid152 = 152
	type vec struct {
		level, dup, hp, at, maxLevel int
	}
	vectors := []vec{
		{1, 0, 3680, 3900, 70},
		{70, 0, 7728, 8190, 70},
		{145, 15, 12078, 12840, 145},
	}
	for _, v := range vectors {
		t.Run(fmt.Sprintf("lv%d_dup%d", v.level, v.dup), func(t *testing.T) {
			ts, st := inventoryServer(t)
			uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-inv-vec")
			insertUnitDup(t, st, uid, uid152, v.level, v.dup)
			u := rowByID(t, gameData(t, ts.URL, "/user_units", uid, sk, s0, s1, "")["user_units"].([]any), uid152)
			if u["hp"].(float64) != float64(v.hp) || u["at"].(float64) != float64(v.at) {
				t.Fatalf("面板 hp/at=%v/%v want %d/%d", u["hp"], u["at"], v.hp, v.at)
			}
			if u["max_level"].(float64) != float64(v.maxLevel) {
				t.Fatalf("max_level=%v want %d", u["max_level"], v.maxLevel)
			}
			if u["base_max_level"].(float64) != 70 {
				t.Fatalf("base_max_level=%v want 70（未觉醒上限，不随觉醒变）", u["base_max_level"])
			}
			if u["max_duplicated_count"].(float64) != 15 {
				t.Fatalf("max_duplicated_count=%v want 15", u["max_duplicated_count"])
			}
		})
	}
}

// TestInventoryOutOfRangeDupRejected 实例态越界（dup > 上限）不得静默截断：
// 读面返回明确数据错误（500 handler_error），而不是伪造面板。
func TestInventoryOutOfRangeDupRejected(t *testing.T) {
	ts, st := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-inv-dupbad")
	// ★5 觉醒上限 15；插 16 模拟脏数据。
	insertUnitDup(t, st, uid, 152, 1, 16)

	body, _ := cn35protocol.EncryptFormBody("_method=GET", "1700000000", s0, s1)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/user_units", strings.NewReader(string(body)))
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
	raw := readAll(t, resp)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("越界 dup 应 500: %d %.200s", resp.StatusCode, raw)
	}
	var diag map[string]any
	if err := json.Unmarshal(raw, &diag); err != nil {
		t.Fatalf("错误响应非 JSON: %.200s", raw)
	}
	if note, _ := diag["mercd_note"].(string); !strings.Contains(note, "152") {
		t.Fatalf("note 未含 unit_id: %v", diag)
	}
}

// TestStatChainConsistency 四条链路单位 hp/at 同源逐值一致（协议一致性，
// 不是伤害一致性）：/user_units、/users/preset_data.json、/quests/execute/*。
func TestStatChainConsistency(t *testing.T) {
	dir := t.TempDir()
	ts, st := battleServer(t, dir)
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-stat-chain")
	// 152 Lv70/dup0；另插两个单位供默认编队解析。
	insertUnitDup(t, st, uid, 152, 70, 0)
	insertUnit(t, st, uid, 336, 10)
	insertUnit(t, st, uid, 348, 10)

	list152 := rowByID(t, gameData(t, ts.URL, "/user_units", uid, sk, s0, s1, "")["user_units"].([]any), 152)
	wantHp := list152["hp"].(float64)
	wantAt := list152["at"].(float64)
	if wantHp != 7728 || wantAt != 8190 {
		t.Fatalf("/user_units 152 Lv70 面板: hp=%v at=%v", wantHp, wantAt)
	}

	// preset_data 同一组装面。
	pd := gameData(t, ts.URL, "/users/preset_data.json", uid, sk, s0, s1, "")
	pd152 := rowByID(t, pd["user_units"].([]any), 152)
	if pd152["hp"].(float64) != wantHp || pd152["at"].(float64) != wantAt {
		t.Fatalf("preset_data 与 user_units 不一致: %v/%v vs %v/%v",
			pd152["hp"], pd152["at"], wantHp, wantAt)
	}
	if pd152["max_level"].(float64) != list152["max_level"].(float64) {
		t.Fatalf("preset_data max_level 不一致: %v vs %v", pd152["max_level"], list152["max_level"])
	}

	// execute 战斗文档（同一 buildUnitDoc）。
	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001", "_method=GET")
	exe152 := rowByID(t, doc["units"].([]any), 152)
	if exe152["hp"].(float64) != wantHp || exe152["at"].(float64) != wantAt {
		t.Fatalf("execute 与 user_units 不一致: %v/%v vs %v/%v",
			exe152["hp"], exe152["at"], wantHp, wantAt)
	}
	if exe152["max_level"].(float64) != list152["max_level"].(float64) ||
		exe152["base_max_level"].(float64) != list152["base_max_level"].(float64) {
		t.Fatalf("execute 等级上限字段与 user_units 不一致: %v/%v vs %v/%v",
			exe152["max_level"], exe152["base_max_level"],
			list152["max_level"], list152["base_max_level"])
	}
}

// TestMonsterSizePersistedPanel STAT350-04：库存 Size（十进制字符串）驱动面板，
// 并原样下发 scale/real_scale；未知的 scale_limit/is_rare 省略键（不填伪值）。
func TestMonsterSizePersistedPanel(t *testing.T) {
	ts, st := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-size-panel")
	insertMonsterFull(t, st, uid, cn35game.MonsterRecord{
		UUID: "m-size-1", MonsterID: 500, Level: 1,
		Scale: strPtr("1.300"), RealScale: strPtr("1.300"),
	})
	m := gameData(t, ts.URL, "/user_monsters", uid, sk, s0, s1, "")["user_monsters"].([]any)[0].(map[string]any)

	// 客户端 Document.dec/f 按数值解析：wire 必须发 JSON 数值而非字符串
	// （2026-09-30 实机：字符串态 FormatException 致强化按钮无响应）。
	if m["scale"].(float64) != 1.3 || m["real_scale"].(float64) != 1.3 {
		t.Fatalf("Size 下发值: scale=%v real_scale=%v", m["scale"], m["real_scale"])
	}
	// catalog 500：life 78987 / atk 7968。
	wantHp := statmodel.MonsterHP(78987, 1.3)
	wantAt := statmodel.MonsterAt(7968, 1.3)
	if m["hp"].(float64) != float64(wantHp) || m["hp"].(float64) == 78987 {
		t.Fatalf("Size=1.3 hp=%v want %d（且不等于基值 78987）", m["hp"], wantHp)
	}
	if m["at"].(float64) != float64(wantAt) || m["seed_at"].(float64) != float64(wantAt) {
		t.Fatalf("Size=1.3 at/seed_at=%v/%v want %d", m["at"], m["seed_at"], wantAt)
	}
	if m["base_hp"].(float64) != 78987 || m["base_seed_at"].(float64) != 7968 {
		t.Fatalf("基值不应被 Size 改写: %v/%v", m["base_hp"], m["base_seed_at"])
	}
	// 未证的 scale_limit/is_rare 必须省略键。
	for _, k := range []string{"scale_limit", "is_rare"} {
		if _, has := m[k]; has {
			t.Fatalf("未证字段 %s 不得下发: %v", k, m[k])
		}
	}
}

// TestMonsterSizeLimitAndRareEmitted 有值时下发 scale_limit/is_rare（数量与布尔）。
func TestMonsterSizeLimitAndRareEmitted(t *testing.T) {
	ts, st := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-size-rare")
	insertMonsterFull(t, st, uid, cn35game.MonsterRecord{
		UUID: "m-size-2", MonsterID: 500, Level: 1,
		Scale: strPtr("1.549"), RealScale: strPtr("1.549"),
		ScaleLimit: strPtr("2.300"), IsRare: boolPtr(true),
	})
	m := gameData(t, ts.URL, "/user_monsters", uid, sk, s0, s1, "")["user_monsters"].([]any)[0].(map[string]any)
	if m["scale_limit"].(float64) != 2.3 {
		t.Fatalf("scale_limit=%v want 2.3", m["scale_limit"])
	}
	if v, ok := m["is_rare"].(bool); !ok || !v {
		t.Fatalf("is_rare=%v want true", m["is_rare"])
	}
}

// TestMonsterSizeDegradedExplicitOrAbsent 无 Size 数据 → 显式 1.000 降级
// （面板等于基值），但不得伪造 scale_limit/is_rare。
func TestMonsterSizeDegradedExplicitOrAbsent(t *testing.T) {
	ts, st := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-size-degrade")
	insertMonster(t, st, uid, "m-degrade-1", 500, 1)
	m := gameData(t, ts.URL, "/user_monsters", uid, sk, s0, s1, "")["user_monsters"].([]any)[0].(map[string]any)
	if m["scale"].(float64) != 1.0 || m["real_scale"].(float64) != 1.0 {
		t.Fatalf("降级 Scale 应显式为 %s（数值形态）: %v/%v",
			cn35game.MonsterSizeDegraded, m["scale"], m["real_scale"])
	}
	if m["hp"].(float64) != m["base_hp"].(float64) || m["at"].(float64) != m["base_seed_at"].(float64) {
		t.Fatalf("Size=1.000 降级面板应等于基值: hp=%v base=%v at=%v base=%v",
			m["hp"], m["base_hp"], m["at"], m["base_seed_at"])
	}
	for _, k := range []string{"scale_limit", "is_rare"} {
		if _, has := m[k]; has {
			t.Fatalf("降级行不得伪造 %s: %v", k, m[k])
		}
	}
}

// TestMonsterSizePrecisionRoundTrip Size 以十进制字符串精确往返（无二进制浮点漂移），
// 且账号隔离：另一账号看不到该 Size。
func TestMonsterSizePrecisionRoundTrip(t *testing.T) {
	ts, st := inventoryServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-size-prec")
	other, osk, os0, os1 := establishSession(t, ts.URL, "dev-size-other")
	insertMonsterFull(t, st, uid, cn35game.MonsterRecord{
		UUID: "m-prec-1", MonsterID: 500, Level: 1,
		Scale: strPtr("1.005"), RealScale: strPtr("1.005"),
	})
	m := gameData(t, ts.URL, "/user_monsters", uid, sk, s0, s1, "")["user_monsters"].([]any)[0].(map[string]any)
	if m["scale"].(float64) != 1.005 || m["real_scale"].(float64) != 1.005 {
		t.Fatalf("十进制往返失真: %v/%v", m["scale"], m["real_scale"])
	}
	// 重启（重开同一库文件）后 Size 保持。
	st2, err := cn35store.Open(filepath.Join(filepath.Dir(dbPathOf(t, st)), "test.sqlite3"))
	if err != nil {
		t.Fatalf("重开库: %v", err)
	}
	defer st2.Close()
	recs, err := st2.Monsters(uid)
	if err != nil || len(recs) != 1 || recs[0].RealScale == nil || *recs[0].RealScale != "1.005" {
		t.Fatalf("重启后 Size 未保持: %v %v", recs, err)
	}
	// 账号隔离。
	if got := gameData(t, ts.URL, "/user_monsters", other, osk, os0, os1, "")["user_monsters"].([]any); len(got) != 0 {
		t.Fatalf("跨账号泄漏魔宠: %v", got)
	}
}

// dbPathOf 从 Store 反查底层库文件路径（重启持久化断言用）。
func dbPathOf(t *testing.T, st *cn35store.Store) string {
	t.Helper()
	var seq int
	var name, path string
	if err := st.DB().QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatalf("database_list: %v", err)
	}
	return path
}
