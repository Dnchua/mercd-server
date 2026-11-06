package cn35game_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
)

func seedOrderedParties(t *testing.T, st *cn35store.Store, uid string) {
	t.Helper()
	seedBattleUnits(t, st, uid)
	if err := st.WithTx(func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, row := range []struct {
			key   string
			order int
			unit  int
		}{{"2", 0, 336}, {"10", 1, 348}, {"1", 2, 336}} {
			if _, err := tx.Exec(`INSERT INTO cn35_player_decks
				(user_id,party_id,display_order,created_at,updated_at) VALUES (?,?,?,?,?)`,
				uid, row.key, row.order, now, now); err != nil {
				return err
			}
			for slot := 0; slot < cn35game.DefaultDeckSlots; slot++ {
				var unit any
				if slot == 0 {
					unit = row.unit
				}
				if _, err := tx.Exec(`INSERT INTO cn35_player_deck_slots
					(user_id,party_id,slot_index,unit_id) VALUES (?,?,?,?)`,
					uid, row.key, slot, unit); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeckWireOrderMatchesExecuteOrdinalAndSavedPartyKey(t *testing.T) {
	ts, st := battleServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "deck-wire-order")
	seedOrderedParties(t, st, uid)

	raw := gameCall(t, ts.URL, "/user_decks", uid, sk, s0, s1, "", "_method=GET")
	if err := json.Unmarshal(raw, new(map[string]any)); err != nil {
		t.Fatalf("编队读取 JSON: %s: %v", raw, err)
	}
	partiesStart := strings.Index(string(raw), "\"parties\":{")
	if partiesStart < 0 {
		t.Fatalf("user_decks.parties 缺失: %s", raw)
	}
	partyJSON := string(raw[partiesStart:])
	positions := []int{
		strings.Index(partyJSON, "\"2\":["),
		strings.Index(partyJSON, "\"10\":["),
		strings.Index(partyJSON, "\"1\":["),
	}
	if positions[0] < 0 || positions[1] <= positions[0] || positions[2] <= positions[1] {
		t.Fatalf("最终 wire 顺序必须按 display_order 为 2,10,1，got positions=%v body=%s", positions, raw)
	}

	for _, tc := range []struct {
		ordinal string
		party   string
		unit    float64
	}{{"001", "2", 336}, {"002", "10", 348}, {"003", "1", 336}} {
		doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
			"mode=quest&id=1&difficulty_id=normal&party_id="+tc.ordinal+"&unit_ids=999999", "_method=GET")
		if doc["quest"] == nil {
			t.Fatalf("ordinal %s execute rejected: %v", tc.ordinal, doc)
		}
		units := doc["units"].([]any)
		if len(units) != 1 || units[0].(map[string]any)["id"].(float64) != tc.unit {
			t.Fatalf("ordinal %s must select party %s member %v, got units=%v", tc.ordinal, tc.party, tc.unit, units)
		}
		user := doc["data"].(map[string]any)["user"].(map[string]any)
		if user["ap"].(float64) != 20 {
			t.Fatalf("execute response must keep pre-use AP for ordinal %s: %v", tc.ordinal, user)
		}
		bid := strings.SplitN(doc["ap_use_url"].(string), "bid=", 2)[1]
		var stored string
		if err := st.DB().QueryRow(`SELECT party_id FROM cn35_battle_sessions WHERE battle_id=?`, bid).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != tc.party {
			t.Fatalf("ordinal %s mapped to session party_id=%s, want actual map key %s", tc.ordinal, stored, tc.party)
		}
	}
}

func TestDeckMutationAndInvalidPartyDoNotChargeAP(t *testing.T) {
	ts, st := battleServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uidA, skA, s0A, s1A := establishSession(t, ts.URL, "deck-wire-A")
	seedOrderedParties(t, st, uidA)
	uidB, _, _, _ := establishSession(t, ts.URL, "deck-wire-B")
	// 起始五卡保底后 152/157/161/230/348 人人持有，「他人单位」用例必须
	// 换用非起始卡（uidB 独有 396）。
	if err := st.WithTx(func(tx *sql.Tx) error {
		return cn35store.InsertUnitTx(tx, uidB, cn35game.UnitRecord{UnitID: 396, Level: 1})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Decks(uidB); err != nil {
		t.Fatal(err)
	}

	beforeAP := func(uid string) int {
		t.Helper()
		var ap int
		if err := st.DB().QueryRow(`SELECT ap FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&ap); err != nil {
			t.Fatal(err)
		}
		return ap
	}
	requireModal := func(doc map[string]any, name string) {
		t.Helper()
		if doc["modal"] == nil {
			t.Fatalf("%s 应被拒绝并回 modal: %v", name, doc)
		}
	}

	on := gameJSON(t, ts.URL, "/user_decks/on", uidA, skA, s0A, s1A,
		"party_id=2&party_index=1&unit_id=348", "_method=GET")
	if on["status"] != "success" || on["data"].(map[string]any)["user_decks"] == nil {
		t.Fatalf("有效 on 响应: %v", on)
	}
	// 同队已存在单位挪位到空槽（party 2 slot 1 -> slot 2），随后对调回 slot 1
	moveOn := gameJSON(t, ts.URL, "/user_decks/on", uidA, skA, s0A, s1A,
		"party_id=2&party_index=2&unit_id=348", "_method=GET")
	if moveOn["status"] != "success" {
		t.Fatalf("同队挪位 on 响应应成功: %v", moveOn)
	}
	swapBack := gameJSON(t, ts.URL, "/user_decks/on", uidA, skA, s0A, s1A,
		"party_id=2&party_index=1&unit_id=348", "_method=GET")
	if swapBack["status"] != "success" {
		t.Fatalf("同队挪回 slot 1 应成功: %v", swapBack)
	}
	if beforeAP(uidA) != 20 {
		t.Fatal("编队编辑改动了 AP")
	}
	for _, tc := range []struct {
		name, path, query string
	}{
		{"越界槽位", "/user_decks/on", "party_id=2&party_index=5&unit_id=336"},
		{"伪造队伍 key", "/user_decks/on", "party_id=999&party_index=0&unit_id=336"},
		{"他人单位", "/user_decks/on", "party_id=2&party_index=2&unit_id=396"},
		{"stale off", "/user_decks/off", "party_id=2&party_index=1&unit_id=336"},
	} {
		doc := gameJSON(t, ts.URL, tc.path, uidA, skA, s0A, s1A, tc.query, "_method=GET")
		requireModal(doc, tc.name)
		if beforeAP(uidA) != 20 {
			t.Fatalf("%s 修改了 AP", tc.name)
		}
	}
	bDecks, err := st.Decks(uidB)
	if err != nil {
		t.Fatal(err)
	}
	// uidB 首次编队读取触发起始五卡保底，首队 = 队长348 在前的起始阵。
	if bDecks[0].Slots[0] != "348" {
		t.Fatalf("A 的编辑泄漏到 B: %+v", bDecks)
	}

	off := gameJSON(t, ts.URL, "/user_decks/off", uidA, skA, s0A, s1A,
		"party_id=1&party_index=0&unit_id=336", "_method=GET")
	if off["status"] != "success" {
		t.Fatalf("精确 off 应成功并保留空槽: %v", off)
	}
	var beforeCount int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_battle_sessions WHERE user_id=?`, uidA).Scan(&beforeCount); err != nil {
		t.Fatal(err)
	}
	// 玩家已手动卸空 1 号队（ordinal 003）→ 禁用自动回填，保持空编队，
	// 尝试用空编队 003 或不存在编队 004 出战均须回 modal 且不建会话、不扣 AP。
	doc := gameJSON(t, ts.URL, "/quests/execute/1.json", uidA, skA, s0A, s1A,
		"mode=quest&id=1&difficulty_id=normal&party_id=003&unit_ids=336,348", "_method=GET")
	requireModal(doc, "ordinal 003 emptied party")
	doc = gameJSON(t, ts.URL, "/quests/execute/1.json", uidA, skA, s0A, s1A,
		"mode=quest&id=1&difficulty_id=normal&party_id=004&unit_ids=336,348", "_method=GET")
	requireModal(doc, "ordinal 004 unknown party")
	if beforeAP(uidA) != 20 {
		t.Fatal("invalid ordinal 004 修改 AP")
	}
	var afterCount int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_battle_sessions WHERE user_id=?`, uidA).Scan(&afterCount); err != nil {
		t.Fatal(err)
	}
	if afterCount != beforeCount {
		t.Fatalf("无效编队建立了会话: before=%d after=%d", beforeCount, afterCount)
	}
}
