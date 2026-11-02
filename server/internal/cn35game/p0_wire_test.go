// p0_wire_test.go 2026-09-28 P0 写面协议回放验收：同伴 leader/helper/
// protect、魔宠 on/off/protect、礼物领取、任务附加选项、未取证写面的
// 明确 modal 拒绝（替代 501）。真实 SQLite + 加密 game 通道。
package cn35game_test

import (
	"database/sql"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35store"
)

// insertPresent 直插礼物行（wire 夹具；content_id>0 受 CHECK 约束）。
func insertPresent(t *testing.T, st *cn35store.Store, uid, uuid, ctype string, contentID, amount, expireAt int64) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO cn35_user_presents
		(uuid,user_id,content_type,content_id,amount,expire_at,received_at,created_at)
		VALUES (?,?,?,?,?,?,0,1000)`,
		uuid, uid, ctype, contentID, amount, expireAt); err != nil {
		t.Fatalf("插入礼物 %s: %v", uuid, err)
	}
}

// clearPresents 清空账号的建号预置礼物行，恢复「空礼物池」夹具前提
// （STARTER-GIFT 后新账号自带 3 条待领）。
func clearPresents(t *testing.T, st *cn35store.Store, uids ...string) {
	t.Helper()
	for _, uid := range uids {
		if _, err := st.DB().Exec(`DELETE FROM cn35_user_presents WHERE user_id=?`, uid); err != nil {
			t.Fatalf("清空礼物: %v", err)
		}
	}
}

// assertModalError 断言响应为 status:error + modal（type/title/body）。
func assertModalError(t *testing.T, doc map[string]any, path string) {
	t.Helper()
	if doc["status"] != "error" {
		t.Fatalf("%s status=%v want error: %v", path, doc["status"], doc)
	}
	modal, ok := doc["modal"].(map[string]any)
	if !ok || modal["type"] != "ok" || modal["title"] == "" {
		t.Fatalf("%s 缺 modal 或形状错误: %v", path, doc["modal"])
	}
}

// assertWriteBackShape 写面成功响应的公共形状：data 含
// user_units/user_monsters/user/user_coin。
func assertWriteBackShape(t *testing.T, doc map[string]any, path string) map[string]any {
	t.Helper()
	if doc["status"] != "success" {
		t.Fatalf("%s status=%v want success: %v", path, doc["status"], doc)
	}
	if _, has := doc["modal"]; has {
		t.Fatalf("%s 成功响应不得带 modal: %v", path, doc)
	}
	data, ok := doc["data"].(map[string]any)
	if !ok {
		t.Fatalf("%s 缺 data: %v", path, doc)
	}
	for _, k := range []string{"user_units", "user_monsters", "user", "user_coin"} {
		if _, has := data[k]; !has {
			t.Fatalf("%s data 缺键 %s: %v", path, k, data)
		}
	}
	return data
}

// TestUnitSetLeaderHelperProtectWire 同伴写面协议端到端：
// 成功形状 + leader/helper 独立 + 归属拒绝 + 参数非法 + 重启持久化。
func TestUnitSetLeaderHelperProtectWire(t *testing.T) {
	dir := t.TempDir()
	ts, st := trainingServer(t, dir)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-p0-leader")
	insertUnit(t, st, uid, 152, 1)
	insertUnit(t, st, uid, 157, 1)

	// set_leader：成功回推 data.user.leader_unit_id=152（数字形态）。
	doc := gameJSON(t, ts.URL, "/user_units/set_leader", uid, sk, s0, s1,
		"id=152", "_method=GET")
	data := assertWriteBackShape(t, doc, "/user_units/set_leader")
	if data["user"].(map[string]any)["leader_unit_id"].(float64) != 152 {
		t.Fatalf("leader_unit_id=%v want 152", data["user"])
	}

	// set_helper：helper_unit_id 下发；与 leader 同 id 时两字段独立，
	// 同单位可同时为队长与助战（UserUnitDetail.cs:346-360 独立比较）。
	doc = gameJSON(t, ts.URL, "/user_units/set_helper", uid, sk, s0, s1,
		"id=152", "_method=GET")
	data = assertWriteBackShape(t, doc, "/user_units/set_helper")
	user := data["user"].(map[string]any)
	if user["helper_unit_id"].(float64) != 152 {
		t.Fatalf("helper_unit_id=%v want 152", user["helper_unit_id"])
	}
	if user["leader_unit_id"].(float64) != 152 {
		t.Fatalf("独立写入后 leader_unit_id=%v want 152", user["leader_unit_id"])
	}

	// protect：true 写位 + 回推。
	doc = gameJSON(t, ts.URL, "/user_units/protect", uid, sk, s0, s1,
		"unit_id=152&protected=true", "_method=GET")
	assertWriteBackShape(t, doc, "/user_units/protect")
	var prot int
	if err := st.DB().QueryRow(`SELECT protected FROM cn35_player_units
		WHERE user_id=? AND unit_id=152`, uid).Scan(&prot); err != nil || prot != 1 {
		t.Fatalf("protected=%v err=%v", prot, err)
	}

	// 归属拒绝：未持有单位 → modal。
	doc = gameJSON(t, ts.URL, "/user_units/set_leader", uid, sk, s0, s1,
		"id=9999", "_method=GET")
	assertModalError(t, doc, "set_leader foreign")
	// 参数非法：非数字 id / 非法 protected → modal。
	doc = gameJSON(t, ts.URL, "/user_units/set_leader", uid, sk, s0, s1,
		"id=abc", "_method=GET")
	assertModalError(t, doc, "set_leader bad id")
	doc = gameJSON(t, ts.URL, "/user_units/protect", uid, sk, s0, s1,
		"unit_id=152&protected=yes", "_method=GET")
	assertModalError(t, doc, "protect bad bool")

	// 重启持久化：重开库后 helper 仍在（wire 层已验证快照含键）。
	st.Close()
	st2, err := cn35store.Open(dir + "/mercd35.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	var helper string
	if err := st2.DB().QueryRow(`SELECT helper_unit_id FROM cn35_player_profiles
		WHERE user_id=?`, uid).Scan(&helper); err != nil {
		t.Fatal(err)
	}
	if helper != "152" {
		t.Fatalf("重启后 helper=%q want 152", helper)
	}
}

// TestMonsterEquipWire 魔宠装备/卸下/保护位协议端到端 + 双向一致。
func TestMonsterEquipWire(t *testing.T) {
	ts, st := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-p0-moneq")
	insertUnit(t, st, uid, 152, 1)
	insertUnit(t, st, uid, 157, 1)
	insertMonster(t, st, uid, "mon-w-1", 1, 1)
	insertMonster(t, st, uid, "mon-w-2", 2, 1)

	// on：成功响应 data.user_units[152] 含魔宠；库内双侧一致。
	doc := gameJSON(t, ts.URL, "/user_monsters/on", uid, sk, s0, s1,
		"uuid=mon-w-1&unit_id=152", "_method=GET")
	assertWriteBackShape(t, doc, "/user_monsters/on")
	var host sql.NullInt64
	var unitMon string
	if err := st.DB().QueryRow(`SELECT m.unit_id,u.monster_uuid
		FROM cn35_player_monsters m, cn35_player_units u
		WHERE m.user_id=? AND m.uuid='mon-w-1' AND u.user_id=? AND u.unit_id=152`,
		uid, uid).Scan(&host, &unitMon); err != nil {
		t.Fatal(err)
	}
	if !host.Valid || int(host.Int64) != 152 || unitMon != "mon-w-1" {
		t.Fatalf("on 后引用不一致 host=%v unit=%q", host, unitMon)
	}

	// 换装：mon-w-2 上 152 → mon-w-1 被卸，152 指向 mon-w-2。
	doc = gameJSON(t, ts.URL, "/user_monsters/on", uid, sk, s0, s1,
		"uuid=mon-w-2&unit_id=152", "_method=GET")
	assertWriteBackShape(t, doc, "/user_monsters/on")
	if err := st.DB().QueryRow(`SELECT unit_id FROM cn35_player_monsters
		WHERE user_id=? AND uuid='mon-w-1'`, uid).Scan(&host); err != nil {
		t.Fatal(err)
	}
	if host.Valid {
		t.Fatalf("mon-w-1 应被卸下 host=%v", host.Int64)
	}
	if err := st.DB().QueryRow(`SELECT monster_uuid FROM cn35_player_units
		WHERE user_id=? AND unit_id=152`, uid).Scan(&unitMon); err != nil || unitMon != "mon-w-2" {
		t.Fatalf("152 monster=%q err=%v", unitMon, err)
	}

	// off（无宿主形）：卸下 mon-w-2。
	doc = gameJSON(t, ts.URL, "/user_monsters/off", uid, sk, s0, s1,
		"uuid=mon-w-2", "_method=GET")
	assertWriteBackShape(t, doc, "/user_monsters/off")
	if err := st.DB().QueryRow(`SELECT unit_id FROM cn35_player_monsters
		WHERE user_id=? AND uuid='mon-w-2'`, uid).Scan(&host); err != nil || host.Valid {
		t.Fatalf("off 后 host=%v err=%v", host, err)
	}

	// 归属/状态拒绝：他人 uuid、未装备 off、host 不一致 → modal。
	doc = gameJSON(t, ts.URL, "/user_monsters/on", uid, sk, s0, s1,
		"uuid=mon-x&unit_id=152", "_method=GET")
	assertModalError(t, doc, "on foreign uuid")
	doc = gameJSON(t, ts.URL, "/user_monsters/off", uid, sk, s0, s1,
		"uuid=mon-w-1&unit_id=152", "_method=GET")
	assertModalError(t, doc, "off not equipped")
	doc = gameJSON(t, ts.URL, "/user_monsters/on", uid, sk, s0, s1,
		"uuid=mon-w-1&unit_id=157", "_method=GET")
	assertWriteBackShape(t, doc, "on 157")
	doc = gameJSON(t, ts.URL, "/user_monsters/off", uid, sk, s0, s1,
		"uuid=mon-w-1&unit_id=152", "_method=GET") // 实际宿主 157 ≠ 152
	assertModalError(t, doc, "off host mismatch")

	// protect：uuids[] 数组形态 + 客户端拼写 "fasle"（先 true 后 fasle）。
	doc = gameJSON(t, ts.URL, "/user_monsters/protect", uid, sk, s0, s1,
		"uuids[]=mon-w-1&uuids[]=mon-w-2&protected=true", "_method=GET")
	assertWriteBackShape(t, doc, "/user_monsters/protect")
	var p1, p2 int
	if err := st.DB().QueryRow(`SELECT protected FROM cn35_player_monsters
		WHERE user_id=? AND uuid='mon-w-1'`, uid).Scan(&p1); err != nil || p1 != 1 {
		t.Fatalf("mon-w-1 protected=%v", p1)
	}
	doc = gameJSON(t, ts.URL, "/user_monsters/protect", uid, sk, s0, s1,
		"uuids[]=mon-w-2&protected=fasle", "_method=GET") // 客户端拼写
	assertWriteBackShape(t, doc, "protect fasle")
	if err := st.DB().QueryRow(`SELECT protected FROM cn35_player_monsters
		WHERE user_id=? AND uuid='mon-w-2'`, uid).Scan(&p2); err != nil || p2 != 0 {
		t.Fatalf("fasle 解保护后 mon-w-2 protected=%v", p2)
	}
}

// TestUnsupportedWriteModal 未取证写面返回业务级 modal（不再是 501）。
func TestUnsupportedWriteModal(t *testing.T) {
	ts, _ := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-p0-unsup")
	for _, p := range []string{
		"/user_units/sell", "/user_units/powerup", "/user_units/evolve",
		"/user_units/evolve_egg", "/user_units/evolve_monster_select_info",
		"/user_units/show_evolve_board", "/user_units/share",
		"/user_units/dismantle_confirm",
		"/user_units/compose/decompose_upgrade_item",
		"/user_monsters/sell", "/user_monsters/powerup", "/user_monsters/evolve",
		"/user_monsters/evolve_egg", "/user_monsters/evolve_monster_select_info",
		"/user_monsters/show_evolve_board", "/user_monsters/share",
	} {
		doc := gameJSON(t, ts.URL, p, uid, sk, s0, s1, "", "_method=GET")
		assertModalError(t, doc, p)
		if doc["status"] == "error" && doc["modal"] == nil {
			t.Fatalf("%s 应为 modal 而非裸 501", p)
		}
	}
}

// TestPresentReceiveWire 礼物领取协议端到端：单领/全领/幂等/拒绝语义。
func TestPresentReceiveWire(t *testing.T) {
	ts, st := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-p0-recv")
	uidB, _, _, _ := establishSession(t, ts.URL, "dev-p0-recv-b")
	clearPresents(t, st, uid, uidB)
	insertPresent(t, st, uid, "w-unit", "unit", 152, 1, 0)
	insertPresent(t, st, uid, "w-coin", "coin", 1, 30, 0)
	insertPresent(t, st, uid, "w-skill", "skill", 1, 1, 0)
	insertPresent(t, st, uidB, "w-foreign", "coin", 1, 10, 0)
	insertPresent(t, st, uid, "w-expired", "coin", 1, 5, 100)

	// 单领 unit：status success + rewards + modal_title + data.user_presents 收缩。
	doc := gameJSON(t, ts.URL, "/user_presents/receive/", uid, sk, s0, s1,
		"uuid=w-unit", "_method=GET")
	data := assertWriteBackShape(t, doc, "/user_presents/receive/")
	rewards, ok := doc["rewards"].([]any)
	if !ok || len(rewards) != 1 {
		t.Fatalf("rewards=%v", doc["rewards"])
	}
	rw := rewards[0].(map[string]any)
	if rw["type"] != "unit" || rw["id"].(float64) != 152 {
		t.Fatalf("reward 行=%v", rw)
	}
	// icon_asset_name 不下发：空值时客户端按 type/id 自行推导图标
	// （Modal.cs:694-698），服务端拼值会覆盖正确默认。
	if _, has := rw["icon_asset_name"]; has {
		t.Fatalf("icon_asset_name 不应下发: %v", rw)
	}
	if doc["modal_title"] == nil || doc["modal_title"] == "" {
		t.Fatalf("缺 modal_title: %v", doc)
	}
	remaining := data["user_presents"].([]any)
	if len(remaining) != 2 { // w-coin + w-skill（expired 不在列表）
		t.Fatalf("剩余待领=%d want 2: %v", len(remaining), remaining)
	}
	var owned int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_player_units
		WHERE user_id=? AND unit_id=152`, uid).Scan(&owned); err != nil || owned != 1 {
		t.Fatalf("同伴礼物入库=%d err=%v", owned, err)
	}

	// 重复领取 → modal，不重复入库。
	doc = gameJSON(t, ts.URL, "/user_presents/receive/", uid, sk, s0, s1,
		"uuid=w-unit", "_method=GET")
	assertModalError(t, doc, "repeat receive")
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_player_units
		WHERE user_id=? AND unit_id=152`, uid).Scan(&owned); err != nil || owned != 1 {
		t.Fatalf("重复后单位数=%d", owned)
	}
	// 过期/他人 uuid/不支持类型 → modal。
	for _, q := range []string{"uuid=w-expired", "uuid=w-foreign", "uuid=w-skill"} {
		doc = gameJSON(t, ts.URL, "/user_presents/receive/", uid, sk, s0, s1,
			q, "_method=GET")
		assertModalError(t, doc, q)
	}

	// 全领：coin 领取，skill 跳过保持未领。
	doc = gameJSON(t, ts.URL, "/user_presents/receive/", uid, sk, s0, s1,
		"", "_method=GET")
	data = assertWriteBackShape(t, doc, "/user_presents/receive/ all")
	rewards, _ = doc["rewards"].([]any)
	if len(rewards) != 1 {
		t.Fatalf("全领 rewards=%v want 1 条", rewards)
	}
	remaining = data["user_presents"].([]any)
	if len(remaining) != 1 {
		t.Fatalf("全领后剩余=%d want 1（skill）: %v", len(remaining), remaining)
	}
	if remaining[0].(map[string]any)["uuid"] != "w-skill" {
		t.Fatalf("剩余行=%v want w-skill", remaining[0])
	}
}

// TestQuestAdditionalOptionWire 任务附加选项：quest_id → option 文档；
// 未知/非法 id → modal。
func TestQuestAdditionalOptionWire(t *testing.T) {
	ts, _ := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-p0-quest")

	doc := gameJSON(t, ts.URL, "/quests/current_quest_additional_option", uid, sk, s0, s1,
		"quest_id=1", "_method=GET")
	if doc["status"] != "success" {
		t.Fatalf("status=%v: %v", doc["status"], doc)
	}
	// 客户端 CurrentQuestButtonParts.cs:63 在响应顶层 has() 判键——顶层
	// 与 data 两份都必须下发。
	opt, ok := doc["current_quest_additional_option"].(map[string]any)
	if !ok {
		t.Fatalf("top-level current_quest_additional_option missing: %v", doc)
	}
	if _, ok := doc["data"].(map[string]any)["current_quest_additional_option"]; !ok {
		t.Fatalf("data.current_quest_additional_option missing: %v", doc["data"])
	}
	parts := opt["parts"].(map[string]any)["DifficultySelect"].(map[string]any)
	if parts["id"] != "1" || parts["data_key"] != "quests_1" {
		t.Fatalf("parts=%v want id=1/data_key=quests_1", parts)
	}
	rd := opt["requireData"].(map[string]any)["quests_1"].(map[string]any)
	if rd["path"] != "/quests?area_id=1" {
		t.Fatalf("requireData=%v", rd)
	}

	// 非法/不存在 quest_id → modal（不 500 不 501）。
	for _, q := range []string{"quest_id=abc", "quest_id=999999"} {
		doc = gameJSON(t, ts.URL, "/quests/current_quest_additional_option", uid, sk, s0, s1,
			q, "_method=GET")
		assertModalError(t, doc, q)
	}
}

func TestUserAndGrowthOpsWire(t *testing.T) {
	ts, st := trainingServer(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-p0-user-ops")
	insertMonster(t, st, uid, "mon-evolve-1", 159, 1)

	showDoc := gameJSON(t, ts.URL, "/users/show", uid, sk, s0, s1, "id="+uid, "_method=GET")
	showData, ok := showDoc["data"].(map[string]any)
	if !ok || showData["user"] == nil || showData["user_temp"] == nil {
		t.Fatalf("/users/show data=%v", showDoc)
	}
	userTemp := showData["user_temp"].(map[string]any)
	if userTemp["id"] != uid || userTemp["add_friend_url"] == nil {
		t.Fatalf("user_temp=%v", userTemp)
	}

	memoDoc := gameJSON(t, ts.URL, "/users/update_memo", uid, sk, s0, s1, "memo=测试签名", "_method=GET")
	memoUser := memoDoc["data"].(map[string]any)["user"].(map[string]any)
	if memoUser["memo"] != "测试签名" || memoDoc["modal"] == nil {
		t.Fatalf("/users/update_memo=%v", memoDoc)
	}

	helpDoc := gameJSON(t, ts.URL, "/users/help", uid, sk, s0, s1, "", "_method=GET")
	if helpDoc["status"] != "success" || helpDoc["modal"] == nil || helpDoc["data"].(map[string]any)["user"] == nil {
		t.Fatalf("/users/help=%v", helpDoc)
	}

	evDoc := gameJSON(t, ts.URL, "/user_monsters/evolve_select_info", uid, sk, s0, s1, "", "_method=GET")
	evData, ok := evDoc["data"].(map[string]any)
	if !ok || evData["user_monsters_evolve_select_info"] == nil {
		t.Fatalf("/user_monsters/evolve_select_info=%v", evDoc)
	}
}
