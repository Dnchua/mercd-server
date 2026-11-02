// album_wire_test.go ALBUM350-01 图鉴读面契约测试：
// /albums/ 全目录 + hide 标记 + album_detail_message /
// /albums/album_unit 未持有详情 / 账号隔离 / 非法参数 modal /
// 列表零 DB 写入。真实 SQLite + 真实 cardcatalog + 加密 game 通道。
package cn35game_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
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

	hide, shown := 0, 0
	for _, r := range albums {
		if r.(map[string]any)["hide"].(bool) {
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
	// 未持有行数值面 = Lv1/dup0 模板态（hp==hp_min）。
	if a2["hp"].(float64) != a2["hp_min"].(float64) || a2["level"].(float64) != 1 {
		t.Fatalf("2 模板态数值: %v", a2)
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
