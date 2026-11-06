// album_wire_test.go ALBUM350-01 图鉴读面契约测试：
// /albums/ 全目录 + hide 标记 + album_detail_message /
// /albums/album_unit 未持有详情 / 账号隔离 / 非法参数 modal /
// 列表零 DB 写入。真实 SQLite + 真实 cardcatalog + 加密 game 通道。
package cn35game_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

// albumCatalog 测试用真实 catalog（与 inventoryServer 同源）。
func albumCatalog(t *testing.T) *cardcatalog.Catalog {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	return cat
}

// TestAlbumListChain /albums/ 端到端：全目录下发、持有/未持有行形态、
// 计数字段（非 hide / 全量）、详情入口消息、枚举旁路、隔离、零写入。
func TestAlbumListChain(t *testing.T) {
	ts, st := inventoryServer(t)
	cat := albumCatalog(t)
	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "dev-album-a")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "dev-album-b")

	insertUnit(t, st, uidA, 152, 1)
	insertUnit(t, st, uidA, 348, 1)

	var unitsBefore int
	st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_player_units`).Scan(&unitsBefore)

	// 带尾斜杠形态（app_conf requireData 原样路径）。
	data := gameData(t, ts.URL, "/albums/", uidA, skA, s0A, s1A, "")
	albums, ok := data["albums"].([]any)
	if !ok {
		t.Fatalf("data.albums 缺失: %v", data)
	}
	total := len(cat.UnitIDs())
	if len(albums) != total {
		t.Fatalf("albums 行数=%d want 全目录 %d", len(albums), total)
	}
	// 图鉴由客户端本地筛选/排序，不能减少目录行数来降体积。
	// 预算覆盖整个数组，防止再次混入完整战斗文档/长篇档案。
	rowsJSON, err := json.Marshal(albums)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsJSON) > total*1100 {
		t.Fatalf("图鉴列表超出每行平均 1100 字节预算: %d bytes / %d rows", len(rowsJSON), total)
	}
	t.Logf("图鉴列表: %d rows, %d JSON bytes", total, len(rowsJSON))
	// 从当前 app_conf 提取真实筛选/排序键，避免白名单遗漏配置的读面。
	confJSON, err := os.ReadFile(filepath.Join("..", "..", "assets", "cn350", "serve_app_conf_android.json"))
	if err != nil {
		t.Fatal(err)
	}
	var conf map[string]any
	if err := json.Unmarshal(confJSON, &conf); err != nil {
		t.Fatal(err)
	}
	filter := conf["pages"].(map[string]any)["UserAlbum"].(map[string]any)["parts"].(map[string]any)["ListParts"].(map[string]any)["components"].(map[string]any)["filter"]
	filterKeys := map[string]bool{}
	var collectKeys func(any)
	collectKeys = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if key, ok := value["key"].(string); ok {
				filterKeys[key] = true
			}
			for _, child := range value {
				collectKeys(child)
			}
		case []any:
			for _, child := range value {
				collectKeys(child)
			}
		}
	}
	collectKeys(filter)

	hide, shown := 0, 0
	for _, r := range albums {
		row := r.(map[string]any)
		if len(row) > 32 { // MLK 的 34 项扣除未实现的 waiting_*。
			t.Fatalf("图鉴行 %v 字段膨胀: %d", row["id"], len(row))
		}
		for _, key := range []string{"uuid", "description", "skills", "passive_skills", "attack_range", "hit_frame", "fire_guard_rate"} {
			if _, has := row[key]; has {
				t.Fatalf("图鉴行 %v 泄漏详情/战斗字段 %s", row["id"], key)
			}
		}
		for key := range filterKeys {
			if _, ok := row[key].(float64); !ok {
				t.Fatalf("图鉴行 %v 缺少数值筛选/排序字段 %s: %v", row["id"], key, row[key])
			}
		}
		if row["hide"].(bool) {
			hide++
		} else {
			shown++
		}
	}
	if shown != 2 || hide != total-2 {
		t.Fatalf("持有 %d / 未持有 %d want 2 / %d", shown, hide, total-2)
	}

	// 持有行 152：hide=false + 模式字段 + 详情入口（无 data_path）+
	// 筛选排序字段（rare_type_id/attr_id/weapon_id/country_id/hp_max/at_max）。
	a152 := rowByID(t, albums, 152)
	if a152["hide"].(bool) || a152["is_user_unit"] != "true" ||
		a152["is_showable"] != "true" || a152["is_waiting"] != "false" {
		t.Fatalf("152 模式字段: %v", a152)
	}
	if a152["unit_alert"].(bool) || a152["wait_alert"].(bool) {
		t.Fatalf("152 alert 字段应为 false 初值: %v", a152)
	}
	if a152["max_duplicated_count"].(float64) != float64(statmodel.MaxDuplicatedCount(5)) {
		t.Fatalf("152 max_duplicated_count: %v", a152["max_duplicated_count"])
	}
	dm, _ := a152["album_detail_message"].(string)
	if !strings.Contains(dm, "name=AlbumUnitDetail") || !strings.Contains(dm, "id=152") ||
		!strings.Contains(dm, "isUserUnit=true") || strings.Contains(dm, "data_path") {
		t.Fatalf("152 album_detail_message: %q", dm)
	}
	if _, has := a152["unit_detail_message"]; has {
		t.Fatalf("152 不应残留 unit_detail_message")
	}
	for _, k := range []string{"rare_type_id", "attr_id", "weapon_id", "country_id",
		"hp_max", "at_max", "speed", "reach", "taghness", "main_name"} {
		if _, has := a152[k]; !has {
			t.Fatalf("152 缺筛选/排序字段 %s: %v", k, a152)
		}
	}

	// 未持有行 2（catalog 存在、账号未持有）：hide=true + data_path 入口。
	a2 := rowByID(t, albums, 2)
	if !a2["hide"].(bool) || a2["is_user_unit"] != "false" || a2["is_showable"] != "false" {
		t.Fatalf("2 未持有形态: %v", a2)
	}
	dm2, _ := a2["album_detail_message"].(string)
	if !strings.Contains(dm2, "isUserUnit=false") ||
		!strings.Contains(dm2, "data_path=%2Falbums%2Falbum_unit%3Fid%3D2") {
		t.Fatalf("2 data_path 转义形态: %q", dm2)
	}
	// 列表只留显示/筛选面；模板面板留给按需详情。
	for _, key := range []string{"hp", "hp_min", "at", "at_min", "level", "max_level"} {
		if _, has := a2[key]; has {
			t.Fatalf("2 列表不应携带详情面板 %s", key)
		}
	}
	// waiting_* 键省略（无待机态，不伪造；客户端缺省 "-"）。
	for _, k := range []string{"waiting_unit_level", "waiting_unit_duplicated_count"} {
		if _, has := a2[k]; has {
			t.Fatalf("2 不应下发 %s", k)
		}
	}

	// 枚举旁路：全目录覆盖（country/sex/weapon）。
	for _, k := range []string{"country", "sex", "weapon"} {
		if _, has := data[k]; !has {
			t.Fatalf("data 缺枚举键 %s", k)
		}
	}

	// 隔离：B 账号全目录均未持有（hide 全 true，含 152/348）。
	dataB := gameData(t, ts.URL, "/albums/", uidB, skB, s0B, s1B, "")
	albumsB := dataB["albums"].([]any)
	if len(albumsB) != total {
		t.Fatalf("B 行数=%d want %d", len(albumsB), total)
	}
	if !rowByID(t, albumsB, 152)["hide"].(bool) {
		t.Fatalf("B 应见 152 为未持有")
	}

	// 读面零 DB 写入。
	var unitsAfter int
	st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_player_units`).Scan(&unitsAfter)
	if unitsAfter != unitsBefore {
		t.Fatalf("读面产生 DB 写入: %d → %d", unitsBefore, unitsAfter)
	}

	// 重复刷新稳定（缓存过期重拉同形）。
	data2 := gameData(t, ts.URL, "/albums/", uidA, skA, s0A, s1A, "")
	if len(data2["albums"].([]any)) != total {
		t.Fatalf("重复刷新行数漂移")
	}
}

// TestAlbumUnitDetailWire /albums/album_unit?id=N：
// 未持有详情模板态数值 + data_path 还原闭环 + 非法/未知 id modal。
func TestAlbumUnitDetailWire(t *testing.T) {
	ts, st := inventoryServer(t)
	cat := albumCatalog(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-album-detail")

	insertUnit(t, st, uid, 152, 1)

	// 未持有单位 2：Lv1/dup0 模板态（与 gacha 名单同口径）。
	plain := gameCall(t, ts.URL, "/albums/album_unit", uid, sk, s0, s1,
		"id=2", "_method=GET")
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatalf("响应 JSON: %v", err)
	}
	unit, ok := doc["data"].(map[string]any)["album_unit"].(map[string]any)
	if !ok {
		t.Fatalf("data.album_unit 缺失: %.200s", plain)
	}
	u2, err := cat.Unit(2)
	if err != nil {
		t.Fatalf("catalog unit 2: %v", err)
	}
	if unit["id"].(float64) != 2 || unit["level"].(float64) != 1 {
		t.Fatalf("id/level: %v", unit)
	}
	panel, err := statmodel.ComputeUnitPanel(statmodel.UnitInput{
		Type: u2.Type, Rare: u2.Rare, HpMin: u2.Life, AtMin: u2.Atk,
		Level: 1, DuplicatedCount: 0,
	})
	if err != nil {
		t.Fatalf("statmodel: %v", err)
	}
	if unit["hp"].(float64) != float64(panel.HP) || unit["at"].(float64) != float64(panel.At) {
		t.Fatalf("Lv1 面板 hp/at=%v/%v want %d/%d", unit["hp"], unit["at"], panel.HP, panel.At)
	}
	if _, has := unit["unit_detail_message"]; has {
		t.Fatalf("album_unit 不应携带 unit_detail_message")
	}

	// 持有单位也可走该端点（模板态；持有详情客户端实际走 user_units）。
	gameCall(t, ts.URL, "/albums/album_unit", uid, sk, s0, s1,
		"id=152", "_method=GET")

	// 未知 id / 非法 id → status:error + modal（不伪造空文档）。
	for _, q := range []string{"id=9999", "id=abc", "id=0"} {
		raw := gameCall(t, ts.URL, "/albums/album_unit", uid, sk, s0, s1, q, "_method=GET")
		var d map[string]any
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatalf("%s 响应 JSON: %v", q, err)
		}
		assertModalError(t, d, "/albums/album_unit?"+q)
	}

	// /albums/show_unit 原服路径兼容（unit_id 参数）。
	plainShow := gameCall(t, ts.URL, "/albums/show_unit", uid, sk, s0, s1,
		"unit_id=152", "_method=POST")
	var showDoc map[string]any
	if err := json.Unmarshal(plainShow, &showDoc); err != nil {
		t.Fatalf("show_unit JSON: %v", err)
	}
	if showDoc["data"].(map[string]any)["album_unit"].(map[string]any)["id"].(float64) != 152 {
		t.Fatalf("show_unit id != 152")
	}
}

// TestMonsterAlbumWire /monster_albums/ 与 /monster_albums/show_monster 端到端契约验证。
func TestMonsterAlbumWire(t *testing.T) {
	ts, st := inventoryServer(t)
	cat := albumCatalog(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-monster-album")
	insertMonster(t, st, uid, "m-album-159", 159, 1)

	data := gameData(t, ts.URL, "/monster_albums/", uid, sk, s0, s1, "")
	rows, ok := data["monster_albums"].([]any)
	if !ok || len(rows) != len(cat.MonsterIDs()) {
		t.Fatalf("monster_albums 行数=%d want %d", len(rows), len(cat.MonsterIDs()))
	}
	m159 := rowByID(t, rows, 159)
	if m159["hide"].(bool) || m159["is_showable"] != "true" {
		t.Fatalf("持有魔宠 159 状态错误: %v", m159)
	}
	m1 := rowByID(t, rows, 1)
	if !m1["hide"].(bool) || m1["is_showable"] != "false" {
		t.Fatalf("未持有魔宠 1 状态错误: %v", m1)
	}

	plain := gameCall(t, ts.URL, "/monster_albums/show_monster", uid, sk, s0, s1,
		"monster_id=159", "_method=POST")
	var detail map[string]any
	if err := json.Unmarshal(plain, &detail); err != nil {
		t.Fatalf("show_monster JSON: %v", err)
	}
	am, ok := detail["data"].(map[string]any)["album_monster"].(map[string]any)
	if !ok || am["id"].(float64) != 159 {
		t.Fatalf("album_monster 错误: %v", detail)
	}
}

func albumOpsServer(t *testing.T) (*httptest.Server, *cn35store.Store, *cardcatalog.Catalog) {
	t.Helper()
	cat := albumCatalog(t)
	st, err := cn35store.Open(filepath.Join(t.TempDir(), "album-ops.sqlite3"))
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
		UnitOps:    st,
		MonsterOps: st,
		Skills:     st,
		Decks:      st,
		Training:   st,
		Gacha:      st,
		ServerHost: "127.0.0.1:18091",
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, cat
}

// TestAlbumWaitBackAndWaitingGachaStack /albums/wait 与 /albums/back 端到端契约：
//   - 待机移入：从 /user_units 分流、自动卸下符文/魔宠/编队/保护、回推 data.status=="success" 与 album_unit
//   - 队长/助战保护：拒绝进入待机所
//   - 待机状态下 /albums/album_unit 返回实际等级与觉醒数
//   - 待机状态下抽卡获得重复同伴：直接叠加觉醒层数（无需移回同伴列表）且保持待机状态
//   - 待机移回：恢复至 /user_units 活跃列表
func TestAlbumWaitBackAndWaitingGachaStack(t *testing.T) {
	ts, st, cat := albumOpsServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-album-wait-back")

	// 插入同伴 152（Lv30, dup2）与队长 348（Lv1, dup0），以及魔宠与符文。
	insertUnitDup(t, st, uid, 152, 30, 2)
	insertUnitDup(t, st, uid, 348, 1, 0)
	insertMonster(t, st, uid, "m-wait-159", 159, 1)
	skRec, err := st.SkillGrant(uid, 1, 15.0)
	if err != nil {
		t.Fatalf("发放符文: %v", err)
	}
	u152, err := cat.Unit(152)
	if err != nil {
		t.Fatal(err)
	}

	// 装备魔宠、符文、保护位、编队槽位到 152，并将 348 设为队长。
	if err := st.SetLeaderUnitID(uid, 348); err != nil {
		t.Fatalf("设队长 348: %v", err)
	}
	if err := st.SetUnitProtected(uid, 152, true); err != nil {
		t.Fatalf("保护 152: %v", err)
	}
	if err := st.EquipMonster(uid, "m-wait-159", 152); err != nil {
		t.Fatalf("装备魔宠: %v", err)
	}
	if err := st.SkillSetAll(uid, 152, u152.Weapon, 3, []string{skRec.UUID}); err != nil {
		t.Fatalf("装备符文: %v", err)
	}
	decks, err := st.Decks(uid)
	if err != nil || len(decks) < 2 {
		t.Fatalf("读取编队: %v", err)
	}
	if err := st.SetDeckUnit(uid, decks[1].PartyID, 0, 152); err != nil {
		t.Fatalf("编队上阵 152: %v", err)
	}

	// 1. 队长 348 不能进入待机所。
	rawLeaderWait := gameCall(t, ts.URL, "/albums/wait", uid, sk, s0, s1,
		"unit_ids[]=348", "_method=GET")
	var leaderWaitDoc map[string]any
	if err := json.Unmarshal(rawLeaderWait, &leaderWaitDoc); err != nil {
		t.Fatal(err)
	}
	assertModalError(t, leaderWaitDoc, "/albums/wait?unit_ids[]=348")

	// 2. 将 152 移入待机所。
	rawWait := gameCall(t, ts.URL, "/albums/wait", uid, sk, s0, s1,
		"unit_ids[]=152", "_method=GET")
	var waitDoc map[string]any
	if err := json.Unmarshal(rawWait, &waitDoc); err != nil {
		t.Fatalf("albums/wait JSON: %v", err)
	}
	if waitDoc["status"] != "success" {
		t.Fatalf("顶层 status=%v want success: %.200s", waitDoc["status"], rawWait)
	}
	waitData, ok := waitDoc["data"].(map[string]any)
	if !ok || waitData["status"] != "success" {
		t.Fatalf("data.status 缺失或非 success: %v", waitDoc)
	}

	// user_units 已分流，不再包含 152。
	uulist := waitData["user_units"].([]any)
	for _, item := range uulist {
		if item.(map[string]any)["id"].(float64) == 152 {
			t.Fatalf("待机后 user_units 不应包含 152")
		}
	}

	// albums 中 152 标记为 is_waiting="true", is_user_unit="false", hide=false，且带 waiting_unit_*。
	albums := waitData["albums"].([]any)
	a152 := rowByID(t, albums, 152)
	if a152["hide"].(bool) || a152["is_user_unit"] != "false" ||
		a152["is_waiting"] != "true" || a152["is_showable"] != "true" {
		t.Fatalf("待机后 albums[152] 状态错误: %v", a152)
	}
	if a152["waiting_unit_level"].(float64) != 30 || a152["waiting_unit_duplicated_count"].(float64) != 2 {
		t.Fatalf("待机后 waiting_unit_level/duplicated_count 错误: %v", a152)
	}
	dm152, _ := a152["album_detail_message"].(string)
	if !strings.Contains(dm152, "isUserUnit=false") || !strings.Contains(dm152, "isWaiting=true") ||
		!strings.Contains(dm152, "data_path=%2Falbums%2Falbum_unit%3Fid%3D152") {
		t.Fatalf("待机后 album_detail_message 错误: %q", dm152)
	}

	// data.album_unit 与 /albums/album_unit?id=152 均返回待机单位的真实等级（Lv30, dup2）。
	au, ok := waitData["album_unit"].(map[string]any)
	if !ok || au["id"].(float64) != 152 || au["level"].(float64) != 30 || au["duplicated_count"].(float64) != 2 {
		t.Fatalf("wait 回推 album_unit 错误: %v", waitData["album_unit"])
	}
	detailData := gameData(t, ts.URL, "/albums/album_unit", uid, sk, s0, s1, "id=152")
	auGet := detailData["album_unit"].(map[string]any)
	if auGet["level"].(float64) != 30 || auGet["duplicated_count"].(float64) != 2 {
		t.Fatalf("/albums/album_unit 待机详情等级/觉醒错误: %v", auGet)
	}

	// 魔宠、符文、编队槽位均已自动卸下。
	for _, item := range waitData["user_monsters"].([]any) {
		m := item.(map[string]any)
		if m["uuid"] == "m-wait-159" && m["unit_id"] != nil {
			t.Fatalf("待机后魔宠未卸下: %v", m)
		}
	}
	for _, item := range waitData["user_skills"].([]any) {
		skDoc := item.(map[string]any)
		if skDoc["uuid"] == skRec.UUID && skDoc["unit_id"] != nil {
			t.Fatalf("待机后符文未卸下: %v", skDoc)
		}
	}
	decksAfterWait, err := st.Decks(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range decksAfterWait {
		for _, s := range d.Slots {
			if s == "152" {
				t.Fatalf("待机后编队 %s 仍残留 152: %v", d.PartyID, d.Slots)
			}
		}
	}

	// 3. 待机状态下的同伴若在抽卡中抽到重复卡，直接叠加觉醒层数（2 -> 3），且仍处于待机所。
	_, err = st.SettleGacha(cn35game.GachaSettlement{
		UserID:        uid,
		OperationKey:  "wait-dup-stack-1",
		RequestDigest: "wait-dup-stack-1",
		PoolID:        "test_pool",
		PityGroup:     "test_pity",
		RulesVersion:  "v1",
		DrawCount:     1,
		Currency:      "gacha_point",
		UnitCost:      0,
		TotalCost:     0,
		ResultJSON:    `{}`,
		Pity:          cn35game.GachaPity{RulesVersion: "v1"},
		Draws: []cn35game.GachaDraw{{
			UnitID:          152,
			RarityID:        u152.Rare,
			Reason:          "normal",
			DuplicatedDelta: 1,
		}},
	})
	if err != nil {
		t.Fatalf("待机同伴抽卡重复结算: %v", err)
	}
	albumAfterGacha := gameData(t, ts.URL, "/albums/", uid, sk, s0, s1, "")
	a152AfterGacha := rowByID(t, albumAfterGacha["albums"].([]any), 152)
	if a152AfterGacha["is_waiting"] != "true" || a152AfterGacha["waiting_unit_duplicated_count"].(float64) != 3 {
		t.Fatalf("待机同伴抽卡叠加后状态错误: %v", a152AfterGacha)
	}

	// 4. 将 152 从待机所移回同伴列表（/albums/back）。
	rawBack := gameCall(t, ts.URL, "/albums/back", uid, sk, s0, s1,
		"unit_ids[]=152", "_method=GET")
	var backDoc map[string]any
	if err := json.Unmarshal(rawBack, &backDoc); err != nil {
		t.Fatalf("albums/back JSON: %v", err)
	}
	backData, ok := backDoc["data"].(map[string]any)
	if !ok || backData["status"] != "success" {
		t.Fatalf("albums/back data.status 错误: %v", backDoc)
	}
	uulistBack := backData["user_units"].([]any)
	if len(uulistBack) != len(uulist)+1 {
		t.Fatalf("移回后 user_units 数量应 +1: got %d want %d", len(uulistBack), len(uulist)+1)
	}
	u152Back := rowByID(t, uulistBack, 152)
	if u152Back["level"].(float64) != 30 || u152Back["duplicated_count"].(float64) != 3 || u152Back["protected"].(bool) {
		t.Fatalf("移回后 user_units[152] 字段错误: %v", u152Back)
	}
	a152Back := rowByID(t, backData["albums"].([]any), 152)
	if a152Back["is_user_unit"] != "true" || a152Back["is_waiting"] != "false" {
		t.Fatalf("移回后 albums[152] 状态错误: %v", a152Back)
	}
}
