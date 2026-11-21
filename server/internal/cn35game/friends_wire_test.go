// friends_wire_test.go FRIEND350-01..03：M3 好友系统与友情徽章礼物盒协议回放与 .mlk 双向契约测试。
package cn35game_test

import (
	"fmt"
	"testing"
	"time"
)

func TestFriendsWireContract(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	ts, st, _ := fateFriendServer(t, t.TempDir(), func() time.Time { return now })
	defer ts.Close()
	defer st.Close()

	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "dev-friend-a")
	uidB, skB, s0B, s1B := establishSession(t, ts.URL, "dev-friend-b")

	// 1. BV-FRD-01：初始 /friends/index 契约
	idx0 := gameData(t, ts.URL, "/friends/index", uidA, skA, s0A, s1A, "")
	if idx0["friend_count_limit"] != float64(30) || idx0["friend_count"] != float64(0) {
		t.Fatalf("初始好友数与上限契约不符: %v", idx0)
	}
	if len(idx0["friends"].([]any)) != 0 {
		t.Fatalf("初始 friends 应为空数组: %v", idx0["friends"])
	}
	if idx0["medal_package_count"] != float64(0) {
		t.Fatalf("初始 medal_package_count 应为 0: %v", idx0)
	}

	// 2. 搜索好友：可搜到真实玩家 B 与内置 NPC 冒险者，且 user_id 为 string，不含 can_give_friend_medal
	searchB := gameData(t, ts.URL, "/friends/search_friend", uidA, skA, s0A, s1A, "user_id="+uidB+"&name=")
	foundB := searchB["users"].([]any)
	if len(foundB) != 1 || foundB[0].(map[string]any)["user_id"] != uidB {
		t.Fatalf("搜索玩家 B 结果不符: %v", foundB)
	}
	if _, hasCanGive := foundB[0].(map[string]any)["can_give_medal"]; hasCanGive {
		t.Fatalf("搜索列表行不应包含 can_give_medal: %v", foundB[0])
	}

	searchNPC := gameData(t, ts.URL, "/friends/search_friend", uidA, skA, s0A, s1A, "user_id=&name=艾琳")
	foundNPC := searchNPC["users"].([]any)
	if len(foundNPC) == 0 || foundNPC[0].(map[string]any)["user_id"] != "npc_frd_0001" {
		t.Fatalf("搜索内置 NPC 失败: %v", foundNPC)
	}

	// 3. 发送好友申请与待批列表（A 申请 B，同时 A 申请 NPC npc_frd_0001）
	applyAB := gameJSON(t, ts.URL, "/friends/apply_friend", uidA, skA, s0A, s1A, "user_id="+uidB, "_method=POST")
	if applyAB["result"] != true {
		t.Fatalf("A 申请 B 响应不符: %v", applyAB)
	}
	// B 的申请列表应包含 A
	bApplies := gameData(t, ts.URL, "/friends/friend_apply_list", uidB, skB, s0B, s1B, "")
	bApplyRows := bApplies["friends_apply"].([]any)
	if len(bApplyRows) != 1 || bApplyRows[0].(map[string]any)["user_id"] != uidA {
		t.Fatalf("B 的待批列表应包含 A: %v", bApplies)
	}

	// B 批准 A 的申请 -> 双向成为好友
	addBA := gameJSON(t, ts.URL, "/friends/add_friend", uidB, skB, s0B, s1B, "user_id="+uidA, "_method=GET")
	if addBA["result"] != true {
		t.Fatalf("B 批准 A 失败: %v", addBA)
	}
	addData := addBA["data"].(map[string]any)
	if addData["friend_count"] != float64(1) || len(addData["friends_apply"].([]any)) != 0 {
		t.Fatalf("批准后 friend_count 应为 1 且待批清空: %v", addData)
	}

	// A 添加 NPC 好友 npc_frd_0001 与 npc_frd_0002（先验证未申请直接调用 add_friend 会被拦截）
	if r := gameJSON(t, ts.URL, "/friends/add_friend", uidA, skA, s0A, s1A, "user_id=npc_frd_0002", "_method=GET"); r["result"] != false {
		t.Fatalf("未收到好友申请时直接调用 add_friend 必须被拒绝: %v", r)
	}
	if r := gameJSON(t, ts.URL, "/friends/apply", uidA, skA, s0A, s1A, "id=npc_frd_0001", "_method=GET"); r["result"] != true {
		t.Fatalf("申请 NPC 0001 失败: %v", r)
	}
	if r := gameJSON(t, ts.URL, "/friends/add_friend", uidA, skA, s0A, s1A, "user_id=npc_frd_0001", "_method=GET"); r["result"] != true {
		t.Fatalf("批准 NPC 0001 失败: %v", r)
	}
	if r := gameJSON(t, ts.URL, "/friends/apply", uidA, skA, s0A, s1A, "id=npc_frd_0002", "_method=GET"); r["result"] != true {
		t.Fatalf("申请 NPC 0002 失败: %v", r)
	}
	if r := gameJSON(t, ts.URL, "/friends/add_friend", uidA, skA, s0A, s1A, "user_id=npc_frd_0002", "_method=GET"); r["result"] != true {
		t.Fatalf("批准 NPC 0002 失败: %v", r)
	}

	idxA := gameData(t, ts.URL, "/friends/index", uidA, skA, s0A, s1A, "")
	if idxA["friend_count"] != float64(3) {
		t.Fatalf("A 当前应有 3 名好友 (B + 2 NPC): %v", idxA["friend_count"])
	}
	row1 := idxA["friends"].([]any)[0].(map[string]any)
	if row1["can_give_medal"] != true {
		t.Fatalf("今日未赠送前 can_give_medal 应为 true: %v", row1)
	}

	// 4. BV-FRD-02：单人赠送友情徽章（给 NPC 0001）与一键赠送 CP 溢出二次确认弹窗
	// 先将 A 的 CP 设为 112（再送 1 人变 116 <= 120；再一键送剩余 2 人需 +8 -> 124 > 120 触发弹窗）
	if _, err := st.DB().Exec(`INSERT INTO cn35_player_balances (user_id,currency,amount,updated_at) VALUES (?,'cp',112,'2026-11-07T00:00:00Z') ON CONFLICT(user_id,currency) DO UPDATE SET amount=112`, uidA); err != nil {
		t.Fatalf("设置 CP=112 失败: %v", err)
	}
	give1 := gameJSON(t, ts.URL, "/friends/give_friend_medal", uidA, skA, s0A, s1A, "user_id=npc_frd_0001", "_method=GET")
	if give1["result"] != true {
		t.Fatalf("向 npc_frd_0001 赠送友情徽章失败: %v", give1)
	}
	g1Data := give1["data"].(map[string]any)
	if g1Data["user"].(map[string]any)["cp"] != float64(116) {
		t.Fatalf("赠送 1 人后 CP 应为 116: %v", g1Data["user"].(map[string]any)["cp"])
	}
	// NPC 0001 自动回赠了 1 份友情徽章礼盒，medal_package_count 应变为 1
	if g1Data["medal_package_count"] != float64(1) {
		t.Fatalf("NPC 好友应自动回赠 1 份友情徽章礼盒: %v", g1Data["medal_package_count"])
	}

	// 一键赠送剩余 2 名好友（116 + 8 = 124 > 120），不传 confirm 时应返回 .mlk 同款 yes_no 弹窗
	giveAllWarn := gameJSON(t, ts.URL, "/friends/give_all_friend_medal", uidA, skA, s0A, s1A, "", "_method=GET")
	modal := giveAllWarn["modal"].(map[string]any)
	if modal["type"] != "yes_no" || modal["url"] != "/friends/give_all_friend_medal?_method=GET&confirm=true" {
		t.Fatalf("CP 溢出弹窗契约不符: %v", modal)
	}

	// 带 confirm=true 再次调用一键赠送 -> 成功赠送剩余 2 人（B 与 NPC 0002），CP 封顶 120
	giveAllOK := gameJSON(t, ts.URL, "/friends/give_all_friend_medal", uidA, skA, s0A, s1A, "confirm=true", "_method=GET")
	gaData := giveAllOK["data"].(map[string]any)
	if gaData["user"].(map[string]any)["cp"] != float64(120) {
		t.Fatalf("一键赠送后 CP 应封顶 120: %v", gaData["user"].(map[string]any)["cp"])
	}
	// A 此时共收到 2 位 NPC 的回赠礼盒，B 收到来自 A 的 1 份礼盒
	if gaData["medal_package_count"] != float64(2) {
		t.Fatalf("A 应收到 2 份 NPC 回赠礼盒: %v", gaData["medal_package_count"])
	}

	// 5. BV-FRD-03：友情徽章礼物盒查看与领取（单领 + 全领）并入账 item_29
	listA := gameData(t, ts.URL, "/friends/show_friend_medal_list", uidA, skA, s0A, s1A, "")
	medalSlice := listA["friend_medal_gifts"].([]any)
	if len(medalSlice) != 2 {
		t.Fatalf("礼盒列表应有 2 条: %v", medalSlice)
	}
	firstUUID := medalSlice[0].(map[string]any)["uuid"].(string)

	// 单领第 1 条
	acq1 := gameJSON(t, ts.URL, "/friends/acquire_friend_medals", uidA, skA, s0A, s1A, "uuid="+firstUUID, "_method=GET")
	if acq1["result"] != true {
		t.Fatalf("单条领取友情徽章失败: %v", acq1)
	}
	acq1Data := acq1["data"].(map[string]any)
	if acq1Data["friend_medal_count"] != float64(1) || acq1Data["medal_package_count"] != float64(1) {
		t.Fatalf("单领后计数不符: %v", acq1Data)
	}

	// 全部领取剩余礼盒（uuid=""）
	acqAll := gameJSON(t, ts.URL, "/friends/acquire_friend_medals", uidA, skA, s0A, s1A, "uuid=", "_method=GET")
	if acqAll["result"] != true {
		t.Fatalf("全部领取友情徽章失败: %v", acqAll)
	}
	acqAllData := acqAll["data"].(map[string]any)
	if acqAllData["friend_medal_count"] != float64(2) || acqAllData["medal_package_count"] != float64(0) {
		t.Fatalf("全领后计数应为 friend_medal_count=2, medal_package_count=0: %v", acqAllData)
	}

	// 6. 30 人好友上限校验（返回 result:false, msg:"好友已满，无法添加"）
	for i := 1; i <= 27; i++ {
		fakeID := fmt.Sprintf("fake_frd_%02d", i)
		if _, err := st.DB().Exec(`INSERT INTO cn35_player_friends (user_id,friend_user_id,created_at) VALUES (?,?,?)`, uidA, fakeID, "2026-11-07T00:00:00Z"); err != nil {
			t.Fatalf("预置第 %d 个好友失败: %v", i, err)
		}
	}
	fullResp := gameJSON(t, ts.URL, "/friends/add_friend", uidA, skA, s0A, s1A, "user_id=npc_frd_0003", "_method=GET")
	if fullResp["result"] != false || fullResp["msg"] != "好友已满，无法添加" {
		t.Fatalf("满 30 好友时添加应返回 \"好友已满，无法添加\": %v", fullResp)
	}
	if _, err := st.DB().Exec(`DELETE FROM cn35_player_friends WHERE user_id=? AND friend_user_id LIKE 'fake_frd_%'`, uidA); err != nil {
		t.Fatalf("清理占位好友失败: %v", err)
	}

	// 7. 解除好友与清空申请列表（3 名真实/NPC 好友解除 2 名后剩 1 名 B）
	delResp := gameJSON(t, ts.URL, "/friends/delete_friend", uidA, skA, s0A, s1A, "user_ids[]=npc_frd_0001&user_ids[]=npc_frd_0002", "_method=GET")
	if delResp["result"] != true || delResp["data"].(map[string]any)["friend_count"] != float64(1) {
		t.Fatalf("解除 2 名 NPC 好友后应剩 1 名: %v", delResp)
	}

	// 8. 防刷校验：同日解除好友后再重新申请+添加 npc_frd_0001，can_give_medal 仍为 false，不可重复赠送刷礼盒
	if r := gameJSON(t, ts.URL, "/friends/apply", uidA, skA, s0A, s1A, "id=npc_frd_0001", "_method=GET"); r["result"] != true {
		t.Fatalf("重新申请 NPC 0001 失败: %v", r)
	}
	reAdd := gameJSON(t, ts.URL, "/friends/add_friend", uidA, skA, s0A, s1A, "user_id=npc_frd_0001", "_method=GET")
	if reAdd["result"] != true {
		t.Fatalf("重新批准 NPC 0001 失败: %v", reAdd)
	}
	for _, item := range reAdd["data"].(map[string]any)["friends"].([]any) {
		f := item.(map[string]any)
		if f["user_id"] == "npc_frd_0001" && f["can_give_medal"] != false {
			t.Fatalf("同日重加已赠送过的好友后 can_give_medal 必须保持为 false: %v", f)
		}
	}
	reGive := gameJSON(t, ts.URL, "/friends/give_friend_medal", uidA, skA, s0A, s1A, "user_id=npc_frd_0001", "_method=GET")
	if reGive["result"] != false {
		t.Fatalf("同日删好友重加后再次赠送必须被拒绝: %v", reGive)
	}

	if r := gameJSON(t, ts.URL, "/friends/apply", uidA, skA, s0A, s1A, "id=npc_frd_0004", "_method=GET"); r["result"] != true {
		t.Fatalf("申请 NPC 0004 失败: %v", r)
	}
	clrResp := gameJSON(t, ts.URL, "/friends/clear_apply_list", uidA, skA, s0A, s1A, "", "_method=GET")
	if clrResp["result"] != true || len(clrResp["data"].(map[string]any)["friends_apply"].([]any)) != 0 {
		t.Fatalf("清空申请列表后应为空数组: %v", clrResp)
	}
}
