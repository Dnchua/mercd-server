// items_wire_test.go ITEM350-02/05/06/07/09 协议回放：真实 SQLite 存量 +
// 真实加密通道，验证资源副本目录下发、execute→result 素材/金币入账、
// /user_items 背包投影、徽章商品列表/兑换与觉醒消费。
package cn35game_test

import (
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// itemServer 装配全链依赖（物品/资源配置/徽章/觉醒 + 战斗链）。
func itemServer(t *testing.T, stateDir string) (*httptest.Server, *cn35store.Store, *cardcatalog.Catalog) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	if _, _, err := st.PublishResourceConfig("test", "wire 测试发布",
		func(monsterID int) error { _, err := cat.Monster(monsterID); return err }); err != nil {
		t.Fatalf("发布资源配置: %v", err)
	}
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:   st,
		Sessions:   st,
		Inventory:  st,
		Decks:      st,
		Quests:     st,
		Battles:    st,
		Catalog:    cat,
		Items:      st,
		Resources:  st,
		Medal:      st,
		Awaken:     st,
		DailyShop:  st,
		Skills:     st,
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
	return ts, st, cat
}

// TestResourceStagesCatalog 生效配置并入 extra_stages：stage 900/901/902、
// 素材区按属性常驻开放（新账号即见，不受剧情进度锁）。
func TestResourceStagesCatalog(t *testing.T) {
	ts, st, _ := itemServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-res-stages")

	data := gameData(t, ts.URL, "/stages", uid, sk, s0, s1, "")
	extras := docList(t, data, "extra_stages")
	if len(extras) != 3 {
		t.Fatalf("extra_stages 应为 3（素材+金币+符文）: %v", extras)
	}
	areas := docList(t, gameData(t, ts.URL, "/areas?stage_id=900", uid, sk, s0, s1, ""), "areas_900")
	if len(areas) != 5 {
		t.Fatalf("areas_900 = %v", areas)
	}
	quests := docList(t, gameData(t, ts.URL, "/quests?area_id=9001", uid, sk, s0, s1, ""), "quests_9001")
	if len(quests) != 4 {
		t.Fatalf("quests_9001 = %v", quests)
	}
	// 金币区四档。
	gquests := docList(t, gameData(t, ts.URL, "/quests?area_id=9006", uid, sk, s0, s1, ""), "quests_9006")
	if len(gquests) != 4 {
		t.Fatalf("quests_9006 = %v", gquests)
	}
	// RUNE350-03 符文本：area 9007 三档。
	rquests := docList(t, gameData(t, ts.URL, "/quests?area_id=9007", uid, sk, s0, s1, ""), "quests_9007")
	if len(rquests) != 3 {
		t.Fatalf("quests_9007 = %v", rquests)
	}
}

// TestResourceBattleChain 素材本 execute→use_ap→result 全链：掉落进库存、
// 结算台账落库、/user_items 下发分类投影。
func TestResourceBattleChain(t *testing.T) {
	ts, st, _ := itemServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-res-battle")
	seedBattleUnits(t, st, uid)

	hard := gameJSON(t, ts.URL, "/quests/execute/90011.json", uid, sk, s0, s1,
		"mode=quest&id=90011&difficulty_id=hard&party_id=001&unit_ids=336,348", "_method=GET")
	if _, ok := hard["modal"]; !ok {
		t.Fatalf("资源副本不应接受 hard 难度: %v", hard)
	}

	doc := gameJSON(t, ts.URL, "/quests/execute/90011.json", uid, sk, s0, s1,
		"mode=quest&id=90011&party_id=001&unit_ids=336,348", "_method=GET")
	if doc["status"] == "fail" {
		t.Fatalf("execute 90011 失败: %v", doc)
	}
	// execute 下发的 appear_table 必须含摇中掉落（客户端胜利结算画面
	// 只渲染 spawnEntry.rewards，空则战斗后看不到获得资源）。
	wave := doc["appear_table"].(map[string]any)["1"].(map[string]any)
	foundReward := false
	for _, ev := range wave {
		for _, rw := range ev.(map[string]any)["rewards"].([]any) {
			rwd := rw.(map[string]any)
			if rwd["key"] == "item" && rwd["count"].(float64) > 0 {
				foundReward = true
			}
		}
	}
	if !foundReward {
		t.Fatalf("appear_table 未注入素材掉落: %v", wave)
	}
	bid := strings.SplitN(doc["ap_use_url"].(string), "bid=", 2)[1]
	gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+bid, "_method=GET")
	res := gameJSON(t, ts.URL, "/quests/result", uid, sk, s0, s1,
		"bid="+bid+"&time=30&mc=0", "_method=GET")
	if res["status"] != "success" {
		t.Fatalf("result: %v", res)
	}
	// 素材入账：mat_fire_1 必掉种子（chance=10000）。
	items, err := st.PlayerItems(uid)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, pi := range items {
		if pi.ItemKey == "powerup_item_1" {
			got = pi.Quantity
		}
	}
	if got < 2 {
		t.Fatalf("火之种子入账 = %d, want >=2", got)
	}
	var ev int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_resource_reward_events
		WHERE user_id=? AND quest_key='mat_fire_1' AND config_version=1`, uid).Scan(&ev); err != nil || ev != 1 {
		t.Fatalf("结算台账 = %d err=%v", ev, err)
	}
	// /user_items/ 投影：flat + hash 分类 "2"（进化素材页签）。
	bag := gameData(t, ts.URL, "/user_items/", uid, sk, s0, s1, "")
	flat := docList(t, bag, "user_items")
	if len(flat) == 0 {
		t.Fatalf("user_items 应含素材: %v", bag)
	}
	hash := bag["user_items_hash"].(map[string]any)
	if len(hash["2"].([]any)) == 0 {
		t.Fatalf("user_items_hash.2 应含素材: %v", hash)
	}

	// 符文本（area 9007 最高档）全链：掉落符文结算回 user_skills=null（对齐原作抓包失效缓存，不覆写全量列表），
	// 且首包丢失重试（already_received）同样补回 user_skills=null。
	if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET max_ap=200,ap=200 WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	rquests := docList(t, gameData(t, ts.URL, "/quests?area_id=9007", uid, sk, s0, s1, ""), "quests_9007")
	runeQID := itoaInt(int(rquests[len(rquests)-1].(map[string]any)["id"].(float64)))
	rdoc := gameJSON(t, ts.URL, "/quests/execute/"+runeQID+".json", uid, sk, s0, s1,
		"mode=quest&id="+runeQID+"&party_id=001&unit_ids=336,348", "_method=GET")
	if rdoc["status"] == "fail" || rdoc["ap_use_url"] == nil {
		t.Fatalf("execute %s 失败: %v", runeQID, rdoc)
	}
	rbid := strings.SplitN(rdoc["ap_use_url"].(string), "bid=", 2)[1]
	gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+rbid, "_method=GET")
	rres := gameJSON(t, ts.URL, "/quests/result", uid, sk, s0, s1,
		"bid="+rbid+"&time=30&mc=0", "_method=GET")
	if rres["status"] != "success" {
		t.Fatalf("rune result: %v", rres)
	}
	rdata := rres["data"].(map[string]any)
	if v, ok := rdata["user_skills"]; !ok || v != nil {
		t.Fatalf("符文本掉落结算应下发 user_skills=null 使客户端缓存过期重拉: ok=%v v=%v", ok, v)
	}
	rretry := gameJSON(t, ts.URL, "/quests/result", uid, sk, s0, s1,
		"bid="+rbid+"&time=30&mc=0", "_method=GET")
	if rretry["status"] != "already_received" {
		t.Fatalf("符文本重试应回 already_received: %v", rretry)
	}
	rretryData := rretry["data"].(map[string]any)
	if v, ok := rretryData["user_skills"]; !ok || v != nil {
		t.Fatalf("符文本 already_received 重试应补回 user_skills=null: ok=%v v=%v", ok, v)
	}
}

// TestResourceQuestsDoNotAdvanceStory 回归：资源关只挂在 extra_stages，
// 不参与剧情 next_quest 链；剧情末关结算不得把账号推进 900xx 资源 ID 空间。
func TestResourceQuestsDoNotAdvanceStory(t *testing.T) {
	ts, st, _ := itemServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-res-progress")
	seedBattleUnits(t, st, uid)
	// 剧情末关为 quest160（stage10/area40，地方=stage、子区域=area）；
	// 结算后无下一关，前沿保持。
	if _, err := st.DB().Exec(`UPDATE cn35_quest_progress
		SET stage_id=10,area_id=40,quest_id=160,updated_at=? WHERE user_id=?`,
		"2026-09-30T00:00:00Z", uid); err != nil {
		t.Fatal(err)
	}

	doc := gameJSON(t, ts.URL, "/quests/execute/160.json", uid, sk, s0, s1,
		"mode=quest&id=160&difficulty_id=normal&party_id=001&unit_ids=336,348", "_method=GET")
	if doc["status"] == "fail" {
		t.Fatalf("剧情末关 execute 失败: %v", doc)
	}
	bid := strings.SplitN(doc["ap_use_url"].(string), "bid=", 2)[1]
	gameJSON(t, ts.URL, "/quests/use_ap", uid, sk, s0, s1, "bid="+bid, "_method=GET")
	res := gameJSON(t, ts.URL, "/quests/result", uid, sk, s0, s1,
		"bid="+bid+"&time=30&mc=0", "_method=GET")
	if res["status"] != "success" {
		t.Fatalf("剧情末关 result: %v", res)
	}
	var stageID, areaID, questID int
	if err := st.DB().QueryRow(`SELECT stage_id,area_id,quest_id
		FROM cn35_quest_progress WHERE user_id=?`, uid).
		Scan(&stageID, &areaID, &questID); err != nil {
		t.Fatal(err)
	}
	if questID != 160 || areaID != 40 || stageID != 10 {
		t.Fatalf("剧情进度被资源关污染: stage=%d area=%d quest=%d", stageID, areaID, questID)
	}
	var ledger int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_resource_reward_events
		WHERE user_id=? AND request_key=?`, uid, bid).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if ledger != 0 {
		t.Fatal("剧情战斗不应写资源副本奖励台账")
	}
	// 已污染的旧进度指针必须 fail-closed，不能因 900xx 大于全部剧情
	// quest_id 而把剧情链全部视为已解锁。
	if _, err := st.DB().Exec(`UPDATE cn35_quest_progress
		SET quest_id=90011,updated_at=? WHERE user_id=?`,
		"2026-09-30T00:00:01Z", uid); err != nil {
		t.Fatal(err)
	}
	poisoned := gameJSON(t, ts.URL, "/quests/execute/1.json", uid, sk, s0, s1,
		"mode=quest&id=1&difficulty_id=normal&party_id=001&unit_ids=336,348", "_method=GET")
	if _, ok := poisoned["modal"]; !ok {
		t.Fatalf("污染剧情进度仍放行 execute: %v", poisoned)
	}
}

// TestMedalItemsExchangeWire 商品列表 + 兑换链：余额不足拒绝 → admin 发放
// → 兑换成功扣 medal 入书 → 幂等重放。
func TestMedalItemsExchangeWire(t *testing.T) {
	ts, st, _ := itemServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-medal-x")

	list := gameData(t, ts.URL, "/medal_items/", uid, sk, s0, s1, "")
	if _, ok := list["medal_unit_list"].([]any); !ok {
		t.Fatalf("缺 medal_unit_list 键")
	}
	mi := list["medal_right_hash"].(map[string]any)
	goods := mi["item_list"].([]any)
	if len(goods) != 31 {
		t.Fatalf("商品列表 = %v", goods)
	}
	powerups := mi["powerup_item_list"].([]any)
	if len(powerups) != 15 {
		t.Fatalf("素材商品列表 = %v", powerups)
	}
	// 药水投放稀有徽章页（非空即让客户端切四按钮模式）；下发
	// price=1 是 0 价商品的客户端下限，实扣仍按快照 price=0。
	// BP 药水已按需求下架，稀有徽章页仅剩 AP 药水。
	rare := mi["rare_medal_item_list"].([]any)
	if len(rare) != 1 {
		t.Fatalf("稀有徽章页列表 = %v", rare)
	}
	potion := rare[0].(map[string]any)
	if potion["price"] != float64(1) || potion["price_str"] != "免费" {
		t.Fatalf("免费商品行 = %v", potion)
	}
	// 无徽章：兑换 5★ 书应失败回执。
	bad := gameJSON(t, ts.URL, "/medal_items/exchange", uid, sk, s0, s1,
		"id=4&count=1", "_method=GET")
	if bad["status"] == "success" {
		t.Fatalf("无徽章兑换应失败: %v", bad)
	}
	if err := st.GrantMedal(uid, 600, "test", "测试发放", "wire-grant-1"); err != nil {
		t.Fatal(err)
	}
	// count/id 参数严格解析：非法值不得静默改成 1 或放行到持久层。
	for _, form := range []string{"id=4&count=abc", "id=4&count=0", "id=bad&count=1"} {
		badReq := gameJSON(t, ts.URL, "/medal_items/exchange", uid, sk, s0, s1, form, "_method=GET")
		if badReq["status"] != "error" {
			t.Fatalf("非法兑换参数未被拒绝 %q: %v", form, badReq)
		}
	}
	ok := gameJSON(t, ts.URL, "/medal_items/exchange", uid, sk, s0, s1,
		"id=4&count=1&request_key=wire-ex-1", "_method=GET")
	if ok["status"] != "success" {
		t.Fatalf("兑换失败: %v", ok)
	}
	u := ok["data"].(map[string]any)["user"].(map[string]any)
	if u["medal"].(float64) != 100 {
		t.Fatalf("medal = %v, want 100", u["medal"])
	}
	items, _ := st.PlayerItems(uid)
	found := false
	for _, pi := range items {
		if pi.ItemKey == "awakening_book_5" && pi.Quantity == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("书未入账: %v", items)
	}
	// 幂等重放不再扣。
	replay := gameJSON(t, ts.URL, "/medal_items/exchange", uid, sk, s0, s1,
		"id=4&count=1&request_key=wire-ex-1", "_method=GET")
	if replay["status"] != "success" {
		t.Fatalf("重放: %v", replay)
	}
	u2 := replay["data"].(map[string]any)["user"].(map[string]any)
	if u2["medal"].(float64) != 100 {
		t.Fatalf("重放重复扣款: %v", u2["medal"])
	}
	conflict := gameJSON(t, ts.URL, "/medal_items/exchange", uid, sk, s0, s1,
		"id=3&count=1&request_key=wire-ex-1", "_method=GET")
	if conflict["status"] != "error" {
		t.Fatalf("同幂等键不同载荷应拒绝: %v", conflict)
	}
	// 素材商品（goods_id=4+content_id，medal_mat_15 价 150）同样可兑换。
	if err := st.GrantMedal(uid, 200, "test", "素材兑换", "wire-grant-2"); err != nil {
		t.Fatal(err)
	}
	mat := gameJSON(t, ts.URL, "/medal_items/exchange", uid, sk, s0, s1,
		"id=19&count=1&request_key=wire-ex-2", "_method=GET")
	if mat["status"] != "success" {
		t.Fatalf("素材兑换失败: %v", mat)
	}
	// 免费药水（goods_id=20，price=0）：徽章不扣减、可重复兑换。
	before := gameJSON(t, ts.URL, "/medal_items/exchange", uid, sk, s0, s1,
		"id=19&count=1&request_key=wire-ex-3", "_method=GET")
	medalBefore := before["data"].(map[string]any)["user"].(map[string]any)["medal"].(float64)
	for _, rk := range []string{"wire-free-1", "wire-free-2"} {
		free := gameJSON(t, ts.URL, "/medal_items/exchange", uid, sk, s0, s1,
			"id=20&count=1&request_key="+rk, "_method=GET")
		if free["status"] != "success" {
			t.Fatalf("免费兑换失败 %q: %v", rk, free)
		}
		m := free["data"].(map[string]any)["user"].(map[string]any)["medal"].(float64)
		if m != medalBefore {
			t.Fatalf("免费兑换扣款: medal %v -> %v", medalBefore, m)
		}
	}
	items, _ = st.PlayerItems(uid)
	qty := map[string]int{}
	for _, pi := range items {
		qty[pi.ItemKey] = pi.Quantity
	}
	if qty["powerup_item_15"] != 2 || qty["ap_potion"] != 2 {
		t.Fatalf("兑换入账数量错误: %v", items)
	}
}

// TestAwakenWire /user_units/awake 暂定契约：扣书加 dup，回推面板。
func TestAwakenWire(t *testing.T) {
	ts, st, cat := itemServer(t, t.TempDir())
	defer ts.Close()
	defer st.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-awake")
	seedBattleUnits(t, st, uid)

	// 无书 → 失败回执。
	bad := gameJSON(t, ts.URL, "/user_units/awake", uid, sk, s0, s1,
		"id=336", "_method=GET")
	if bad["status"] == "success" {
		t.Fatalf("无书觉醒应失败: %v", bad)
	}
	rare := catUnitRare(t, cat, 336)
	if _, err := st.DB().Exec(`INSERT INTO cn35_player_items
		(user_id,item_key,quantity,updated_at) VALUES (?,?,1,?)`,
		uid, "awakening_book_"+itoa(rare), "2026-09-30T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	ok := gameJSON(t, ts.URL, "/user_units/awake", uid, sk, s0, s1,
		"id=336", "_method=GET")
	if ok["status"] != "success" {
		t.Fatalf("觉醒失败: %v", ok)
	}
	var dup int
	if err := st.DB().QueryRow(`SELECT duplicated_count FROM cn35_player_units
		WHERE user_id=? AND unit_id=336`, uid).Scan(&dup); err != nil || dup != 1 {
		t.Fatalf("dup = %d err=%v", dup, err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COALESCE(quantity,0) FROM cn35_player_items
		WHERE user_id=? AND item_key=?`, uid, "awakening_book_"+itoa(rare)).Scan(&n); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("书未扣减: %d", n)
	}
}

func catUnitRare(t *testing.T, cat *cardcatalog.Catalog, id int) int {
	t.Helper()
	u, err := cat.Unit(id)
	if err != nil {
		t.Fatalf("unit %d: %v", id, err)
	}
	return u.Rare
}

func itoa(i int) string {
	return string(rune('0' + i))
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
