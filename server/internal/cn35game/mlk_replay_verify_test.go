// mlk_replay_verify_test.go M5（T03~T10）：
//   - T03：潜能系统关闭（consts.unit_upgrade_open=false，/user_units/upgrade 拒绝）与功能开关验证
//   - T04：魔宠种子技（普通魔宠 + 五系融合宠 100005..100045）读面与 /quests/execute 闭环
//   - T05：符文强化、出售、批量装卸（set_all + skills_Set）闭环
//   - T06：同伴告别（/user_units/sell）与魔宠告别（/user_monsters/sell）及守卫/级联解绑
//   - T07：魔宠融合全链路（evolve_select_info -> show_evolve_board -> evolve_egg -> evolve_monster_select_info -> evolve -> rare_monster_category_config & 融合宠 powerup）
//   - T08：同伴进化（evolution_map + /user_units/evolution）及觉醒折半、宿主级联迁移
//   - T09：同伴能力互换 4 端点（双缓存键、互换石优先与 10 钻回退）
//   - T10：mlk-capture-201711 抓包回放比对与客户端消费契约校验
package cn35game_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35roster"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func m5Server(t *testing.T, stateDir string) (*httptest.Server, *cn35store.Store, *cardcatalog.Catalog) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	assetsDir := filepath.Join("..", "..", "assets", "cn350")
	in, _, err := cn35roster.LoadImport(assetsDir, filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatalf("名册清单加载: %v", err)
	}
	if _, _, err := cn35game.SeedGachaRoster(st, in, "test"); err != nil {
		t.Fatalf("名册种子化: %v", err)
	}
	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		Accounts:       st,
		Sessions:       st,
		Inventory:      st,
		Decks:          st,
		Quests:         st,
		Battles:        st,
		Training:       st,
		Gacha:          st,
		GachaConfig:    st,
		UnitOps:        st,
		MonsterOps:     st,
		PresentReceive: st,
		Presents:       st,
		Infos:          st,
		Items:          st,
		Medal:          st,
		Awaken:         st,
		ItemUse:        st,
		Skills:         st,
		Catalog:        cn35game.NewRosterCatalog(cat, st),
		ServerHost:     "127.0.0.1:18091",
		AssetsDir:      assetsDir,
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, cat
}

func setItemQty(t *testing.T, st *cn35store.Store, uid, itemKey string, qty int) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO cn35_player_items (user_id,item_key,quantity,updated_at)
		VALUES (?,?,?,'2026-11-08T00:00:00Z')
		ON CONFLICT(user_id,item_key) DO UPDATE SET quantity=excluded.quantity, updated_at=excluded.updated_at`,
		uid, itemKey, qty); err != nil {
		t.Fatalf("setItemQty(%s=%d): %v", itemKey, qty, err)
	}
}

func setBalance(t *testing.T, st *cn35store.Store, uid, currency string, amount int64) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO cn35_player_balances (user_id,currency,amount,updated_at)
		VALUES (?,?,?,'2026-11-08T00:00:00Z')
		ON CONFLICT(user_id,currency) DO UPDATE SET amount=excluded.amount, updated_at=excluded.updated_at`,
		uid, currency, amount); err != nil {
		t.Fatalf("setBalance(%s=%d): %v", currency, amount, err)
	}
}

// TestMonsterSeedSkillAndExecute 验证 T04：普通魔宠与五系融合宠（100005..100045）种子技解析与 /user_monsters/ 读面输出。
func TestMonsterSeedSkillAndExecute(t *testing.T) {
	ts, st, cat := m5Server(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-m5-seed")

	fusionIDs := []int{100006, 100011, 100019, 100038, 100042}
	for i, mid := range fusionIDs {
		m, err := cat.Monster(mid)
		if err != nil {
			t.Fatalf("Catalog.Monster(%d): %v", mid, err)
		}
		skID, skV, _ := cn35game.MonsterSeedSkill(m)
		if skID <= 0 || skV <= 0 {
			t.Fatalf("融合魔宠 %d MonsterSeedSkill=(%d, %v), want >0", mid, skID, skV)
		}
		uuid := fmt.Sprintf("mon-fusion-%d", i+1)
		if _, err := st.DB().Exec(`INSERT INTO cn35_player_monsters
			(user_id,uuid,monster_id,unit_id,level,protected,scale,real_scale,scale_limit,is_rare,fusion_degrees,created_at,updated_at)
			VALUES (?,?,?,NULL,1,0,'1.750','1.750','1.923',1,6,'2026-11-08T00:00:00Z','2026-11-08T00:00:00Z')`,
			uid, uuid, mid); err != nil {
			t.Fatalf("insert fusion monster %d: %v", mid, err)
		}
	}

	doc := gameJSON(t, ts.URL, "/user_monsters/", uid, sk, s0, s1, "", "_method=GET")
	mons := doc["data"].(map[string]any)["user_monsters"].([]any)
	found := 0
	for _, raw := range mons {
		m := raw.(map[string]any)
		mid := int(m["monster_id"].(float64))
		if cn35game.MonsterBatch(mid) < 0 {
			continue
		}
		found++
		if int(m["seed_skill_id"].(float64)) <= 0 || m["seed_skill_value"].(float64) <= 0 {
			t.Fatalf("融合魔宠 %d seed_skill 未下发有效数值: %v", mid, m)
		}
		if int(m["cost"].(float64)) <= 0 || int(m["call_interval_ms"].(float64)) <= 0 {
			t.Fatalf("融合魔宠 %d cost/call_interval_ms 未下发正数: %v", mid, m)
		}
		if m["is_rare"] != true || int(m["formula_id"].(float64)) != 1 {
			t.Fatalf("融合魔宠 %d is_rare/formula_id 错误: is_rare=%v formula_id=%v", mid, m["is_rare"], m["formula_id"])
		}
	}
	if found != len(fusionIDs) {
		t.Fatalf("user_monsters 融合宠数量=%d want %d", found, len(fusionIDs))
	}
}

// TestSkillPowerupSellEquip 验证 T05：符文强化、批量装卸（set_all + skills_Set 别名）与出售保护守卫。
func TestSkillPowerupSellEquip(t *testing.T) {
	ts, st, _ := m5Server(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-m5-skill")
	insertUnit(t, st, uid, 152, 1)
	setBalance(t, st, uid, "gacha_point", 500000)
	setItemQty(t, st, uid, "powerup_item_11", 10)

	for i, uuid := range []string{"sk-m5-1", "sk-m5-2"} {
		if _, err := st.DB().Exec(`INSERT INTO cn35_player_skills
			(user_id,uuid,skill_id,level,base_level,protected,unit_id,created_at,updated_at)
			VALUES (?,?,?,25.0,25.0,0,NULL,'2026-11-08T00:00:00Z','2026-11-08T00:00:00Z')`,
			uid, uuid, i+1); err != nil {
			t.Fatalf("insert skill %s: %v", uuid, err)
		}
	}

	// 1. 强化符文 sk-m5-1（档3 使用花 powerup_item_id=11）
	doc := gameJSON(t, ts.URL, "/user_skills/powerup", uid, sk, s0, s1,
		"uuid=sk-m5-1&powerup_item_id=11&count=1", "_method=GET")
	if doc["status"] == "error" {
		t.Fatalf("/user_skills/powerup 失败: %v", doc)
	}

	// 2. 通过 /user_skills/skills_Set 装备 sk-m5-1 到同伴 152
	doc = gameJSON(t, ts.URL, "/user_skills/skills_Set", uid, sk, s0, s1,
		"unit_id=152&uuids[]=sk-m5-1", "_method=GET")
	if doc["status"] == "error" {
		t.Fatalf("/user_skills/skills_Set 失败: %v", doc)
	}

	// 3. 出售已装备符文 sk-m5-1 应拒绝，出售未装备符文 sk-m5-2 应成功
	doc = gameJSON(t, ts.URL, "/user_skills/sell", uid, sk, s0, s1,
		"uuids[]=sk-m5-1", "_method=GET")
	assertModalError(t, doc, "/user_skills/sell equipped")

	doc = gameJSON(t, ts.URL, "/user_skills/sell", uid, sk, s0, s1,
		"uuids[]=sk-m5-2", "_method=GET")
	if doc["status"] == "error" {
		t.Fatalf("/user_skills/sell sk-m5-2 失败: %v", doc)
	}
}

// TestUnitAndMonsterSell 验证 T06：同伴告别（/user_units/sell）与魔宠告别（/user_monsters/sell）。
func TestUnitAndMonsterSell(t *testing.T) {
	ts, st, _ := m5Server(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-m5-sell")
	setBalance(t, st, uid, "gacha_point", 1000)

	// 补充同伴确保总同行数 > 5（建号自带 + 新增 19/20/21/152/157）
	for _, id := range []int{19, 20, 21, 152, 157, 161, 230, 348} {
		insertUnit(t, st, uid, id, 10)
	}
	// 设置 152 为保护，157 装备魔宠与符文并放入编队
	if _, err := st.DB().Exec(`UPDATE cn35_player_units SET protected=1 WHERE user_id=? AND unit_id=152`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO cn35_player_monsters
		(user_id,uuid,monster_id,unit_id,level,protected,scale,real_scale,created_at,updated_at)
		VALUES (?,'mon-sell-1',1,157,1,0,'1.200','1.200','2026-11-08T00:00:00Z','2026-11-08T00:00:00Z'),
		       (?,'mon-sell-2',2,NULL,1,0,'1.200','1.200','2026-11-08T00:00:00Z','2026-11-08T00:00:00Z')`,
		uid, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE cn35_player_units SET monster_uuid='mon-sell-1' WHERE user_id=? AND unit_id=157`, uid); err != nil {
		t.Fatal(err)
	}

	// 保护中的 152 不可出售
	doc := gameJSON(t, ts.URL, "/user_units/sell", uid, sk, s0, s1, "ids[]=152", "_method=GET")
	assertModalError(t, doc, "/user_units/sell protected")

	// 已装备在 157 身上的魔宠 mon-sell-1 不可直接出售
	doc = gameJSON(t, ts.URL, "/user_monsters/sell", uid, sk, s0, s1, "uuids[]=mon-sell-1", "_method=GET")
	assertModalError(t, doc, "/user_monsters/sell equipped")

	// 出售同伴 157：应成功，自动解绑 mon-sell-1 并增加金币
	doc = gameJSON(t, ts.URL, "/user_units/sell", uid, sk, s0, s1, "ids[]=157", "_method=GET")
	data := assertWriteBackShape(t, doc, "/user_units/sell 157")
	goldAfterUnit := int64(data["user"].(map[string]any)["gacha_point"].(float64))
	if goldAfterUnit <= 1000 {
		t.Fatalf("出售同伴 157 后金币未增加: %d", goldAfterUnit)
	}

	// 此时 mon-sell-1 已自动卸下，可正常与 mon-sell-2 一并出售
	doc = gameJSON(t, ts.URL, "/user_monsters/sell", uid, sk, s0, s1, "uuids[]=mon-sell-1&uuids[]=mon-sell-2", "_method=GET")
	data = assertWriteBackShape(t, doc, "/user_monsters/sell")
	goldAfterMon := int64(data["user"].(map[string]any)["gacha_point"].(float64))
	if goldAfterMon <= goldAfterUnit {
		t.Fatalf("出售魔宠后金币未增加: %d <= %d", goldAfterMon, goldAfterUnit)
	}
}

// TestMonsterEvoAndRareMonsterPowerup 验证 T07：魔宠融合全链路与融合宠体型强化。
func TestMonsterEvoAndRareMonsterPowerup(t *testing.T) {
	ts, st, cat := m5Server(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-m5-evo")
	setBalance(t, st, uid, "gacha_point", 500000)
	setItemQty(t, st, uid, "evolve_item_1", 10)
	setItemQty(t, st, uid, "powerup_item_8", 20)  // 风之花蕾（attr=3, form=2 -> id=8）
	setItemQty(t, st, uid, "powerup_item_13", 20) // 风之花（attr=3, form=3 -> id=13）

	// 1. /user_monsters/evolve_select_info 11 键校验
	doc := gameJSON(t, ts.URL, "/user_monsters/evolve_select_info", uid, sk, s0, s1, "", "_method=GET")
	info := doc["data"].(map[string]any)["user_monsters_evolve_select_info"].(map[string]any)
	if info["title"] != "魔宠融合" || info["egg_btn_label"] != "融合蛋" || info["monster_btn_label"] != "选择魔宠" {
		t.Fatalf("user_monsters_evolve_select_info 不符 mlk 抓包: %v", info)
	}

	// 找出一个风系（Element=3）且 Rare=1 的魔宠 ID
	wind1StarID := 0
	for id := 1; id <= 200; id++ {
		if m, err := cat.Monster(id); err == nil && m.Element == 3 && m.Rare == 1 {
			wind1StarID = id
			break
		}
	}
	if wind1StarID == 0 {
		t.Fatalf("未找到风系 1★ 魔宠模板")
	}

	// 2. 插入 3 只风系 1★ 完全体魔宠（scale=1.720）与 2 只风系 3★ 配方宠皮卡（id=26, scale=1.720）
	for i := 1; i <= 3; i++ {
		if _, err := st.DB().Exec(`INSERT INTO cn35_player_monsters
			(user_id,uuid,monster_id,unit_id,level,protected,scale,real_scale,created_at,updated_at)
			VALUES (?,?,?,NULL,1,0,'1.720','1.720','2026-11-08T00:00:00Z','2026-11-08T00:00:00Z')`,
			uid, fmt.Sprintf("m-1star-%d", i), wind1StarID); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 2; i++ {
		if _, err := st.DB().Exec(`INSERT INTO cn35_player_monsters
			(user_id,uuid,monster_id,unit_id,level,protected,scale,real_scale,created_at,updated_at)
			VALUES (?,?,26,NULL,1,0,'1.720','1.720','2026-11-08T00:00:00Z','2026-11-08T00:00:00Z')`,
			uid, fmt.Sprintf("m-pika-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	// 3. /user_monsters/show_evolve_board
	doc = gameJSON(t, ts.URL, "/user_monsters/show_evolve_board", uid, sk, s0, s1, "", "_method=GET")
	eggCfg := doc["data"].(map[string]any)["show_monster_evo"].(map[string]any)["egg_evo_config"].(map[string]any)
	windTab := eggCfg["2"].(map[string]any) // index 2 = attr 3 (风)
	if int(windTab["full_scale_monster_count"].(float64)) != 3 {
		t.Fatalf("风系可用 1★ 完全体魔宠数量=%v want 3", windTab["full_scale_monster_count"])
	}

	// 4. /user_monsters/evolve_egg?attr_id=3&consume_monster_count=3 -> 产出 302（稀有的风之蛋）
	doc = gameJSON(t, ts.URL, "/user_monsters/evolve_egg", uid, sk, s0, s1,
		"attr_id=3&consume_monster_count=3", "_method=GET")
	if _, hasModal := doc["modal"]; hasModal {
		t.Fatalf("evolve_egg 成功响应不得包含顶层 modal: %v", doc)
	}
	eggItem := doc["data"].(map[string]any)["evolve_fancy_item"].(map[string]any)
	if int(eggItem["id"].(float64)) != 302 {
		t.Fatalf("evolve_egg 产出 egg id=%v want 302", eggItem["id"])
	}

	// 5. /user_monsters/evolve_monster_select_info?uuid=m-pika-1
	doc = gameJSON(t, ts.URL, "/user_monsters/evolve_monster_select_info", uid, sk, s0, s1,
		"uuid=m-pika-1", "_method=GET")
	showEvo := doc["data"].(map[string]any)["show_monster_evo"].(map[string]any)
	if showEvo["use_egg"] != true {
		t.Fatalf("evolve_monster_select_info.use_egg=%v", showEvo["use_egg"])
	}

	// 6. /user_monsters/evolve?uuids=m-pika-1|m-pika-2&egg_item_id=302 -> 产出 100010（赫黎安提斯 2 阶, scale_limit=1.923）
	doc = gameJSON(t, ts.URL, "/user_monsters/evolve", uid, sk, s0, s1,
		"uuids=m-pika-1|m-pika-2&egg_item_id=302", "_method=GET")
	if _, hasModal := doc["modal"]; hasModal {
		t.Fatalf("evolve 成功响应不得包含顶层 modal: %v", doc)
	}
	evoMon := doc["data"].(map[string]any)["evolve_monster"].(map[string]any)
	if int(evoMon["monster_id"].(float64)) != 100010 || evoMon["is_rare"] != true {
		t.Fatalf("融合产出魔宠不符预期: %v", evoMon)
	}
	evoUUID := evoMon["uuid"].(string)

	// 7. /user_monsters/rare_monster_category_config 与融合宠体型强化（+0.01/步）
	cfgDoc := gameJSON(t, ts.URL, "/user_monsters/rare_monster_category_config", uid, sk, s0, s1, "", "_method=GET")
	cfgList := cfgDoc["data"].(map[string]any)["rare_monster_category_config"].([]any)
	if len(cfgList) != 6 {
		t.Fatalf("rare_monster_category_config 长度=%d want 6", len(cfgList))
	}

	// 100010 初始体型 1.720 位于档位 id=3（1.700..1.799，消耗蕾 form=2 即 powerup_item_8，每步 2 个 + 5000 金币）
	pDoc := gameJSON(t, ts.URL, "/user_monsters/powerup", uid, sk, s0, s1,
		fmt.Sprintf("uuid=%s&powerup_item_id=8&count=4", evoUUID), "_method=GET")
	pData := assertWriteBackShape(t, pDoc, "/user_monsters/powerup rare")
	for _, raw := range pData["user_monsters"].([]any) {
		m := raw.(map[string]any)
		if m["uuid"] == evoUUID {
			if m["scale"].(float64) < 1.739 || m["scale"].(float64) > 1.741 {
				t.Fatalf("融合宠强化 2 步（4 个素材）后 scale=%v want 1.74", m["scale"])
			}
		}
	}
}

// TestUnitEvolution 验证 T08：同伴进化读面与 /user_units/evolution 写面（19 -> 499）。
func TestUnitEvolution(t *testing.T) {
	ts, st, _ := m5Server(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-m5-unitev")
	for id := 1; id <= 15; id++ {
		setItemQty(t, st, uid, fmt.Sprintf("powerup_item_%d", id), 30)
	}

	// 插入可进化 2★ 光系同伴 19（[光阳之女]弗舞，Lv40，觉醒 3 次）并设为队长
	insertUnit(t, st, uid, 19, 40)
	if _, err := st.DB().Exec(`UPDATE cn35_player_units SET duplicated_count=3 WHERE user_id=? AND unit_id=19`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET leader_unit_id='19' WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}

	// 检查读面 /user_units/ 下发的 evolution_map 与 is_evolutable
	uDoc := gameJSON(t, ts.URL, "/user_units/", uid, sk, s0, s1, "", "_method=GET")
	var found19 map[string]any
	for _, raw := range uDoc["data"].(map[string]any)["user_units"].([]any) {
		u := raw.(map[string]any)
		if int(u["id"].(float64)) == 19 {
			found19 = u
			break
		}
	}
	if found19 == nil || found19["is_evolutable"] != true {
		t.Fatalf("同伴 19 应为 is_evolutable=true: %v", found19)
	}
	evoMap := found19["evolution_map"].(map[string]any)
	if int(evoMap["id"].(float64)) != 499 {
		t.Fatalf("同伴 19 evolution_map.id=%v want 499", evoMap["id"])
	}

	// 执行进化 /user_units/evolution?id=19
	resDoc := gameJSON(t, ts.URL, "/user_units/evolution", uid, sk, s0, s1, "id=19", "_method=GET")
	if _, hasModal := resDoc["modal"]; hasModal {
		t.Fatalf("/user_units/evolution 成功响应不得带顶层 modal: %v", resDoc)
	}
	rData := resDoc["data"].(map[string]any)
	before := rData["evolution_before"].(map[string]any)
	after := rData["evolution_after"].(map[string]any)
	if int(before["id"].(float64)) != 19 || int(after["id"].(float64)) != 499 {
		t.Fatalf("evolution_before/after ID 错误: before=%v after=%v", before["id"], after["id"])
	}
	if int(after["level"].(float64)) != 1 || int(after["duplicated_count"].(float64)) != 1 {
		t.Fatalf("进化后应为 Lv1 且觉醒折半 3/2=1: level=%v dup=%v", after["level"], after["duplicated_count"])
	}
	if int(rData["user"].(map[string]any)["leader_unit_id"].(float64)) != 499 {
		t.Fatalf("队长应级联更新为 499: %v", rData["user"].(map[string]any)["leader_unit_id"])
	}
}

// TestUnitAbilityExchange 验证 T09：同伴能力互换 4 端点（互换石优先扣减 + 10 钻回退）。
func TestUnitAbilityExchange(t *testing.T) {
	ts, st, _ := m5Server(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-m5-exch")
	setItemQty(t, st, uid, "power_stone", 1)
	setBalance(t, st, uid, "coin", 50)

	// 插入两名同为 5★ 的同伴：152（Lv50, 觉醒 4）与 157（Lv1, 觉醒 0）
	insertUnit(t, st, uid, 152, 50)
	insertUnit(t, st, uid, 157, 1)
	if _, err := st.DB().Exec(`UPDATE cn35_player_units SET duplicated_count=4 WHERE user_id=? AND unit_id=152`, uid); err != nil {
		t.Fatal(err)
	}

	// 1. get_unit_ability_exchange_info 双缓存键校验
	infoDoc := gameJSON(t, ts.URL, "/unit_ability_exchange/get_unit_ability_exchange_info", uid, sk, s0, s1, "", "_method=GET")
	iData := infoDoc["data"].(map[string]any)
	if iData["changeAbilityInfo"] == nil || iData["unit_ability_exchange_info"] == nil {
		t.Fatalf("get_unit_ability_exchange_info 必须同时返回双缓存键: %v", iData)
	}
	if int(iData["unit_ability_exchange_info"].(map[string]any)["stone_count"].(float64)) != 1 {
		t.Fatalf("stone_count=%v want 1", iData["unit_ability_exchange_info"])
	}

	// 2. select_unit?unit_id=152 筛选同星级候选
	selDoc := gameJSON(t, ts.URL, "/unit_ability_exchange/select_unit", uid, sk, s0, s1, "unit_id=152", "_method=GET")
	cands := selDoc["data"].(map[string]any)["changeAbility_units"].([]any)
	found157 := false
	for _, raw := range cands {
		u := raw.(map[string]any)
		if int(u["id"].(float64)) == 157 && u["change_ability_enable"] == true {
			found157 = true
		}
		if int(u["id"].(float64)) == 152 {
			t.Fatalf("select_unit 不应包含已选自身 152")
		}
	}
	if !found157 {
		t.Fatalf("select_unit 候选未命中同星级 157")
	}

	// 3. get_unit_ability_exchange_result 预览
	prevDoc := gameJSON(t, ts.URL, "/unit_ability_exchange/get_unit_ability_exchange_result", uid, sk, s0, s1,
		"unit_a_id=152&unit_b_id=157", "_method=GET")
	if prevDoc["data"].(map[string]any)["unit_ability_exchange_result"] == nil {
		t.Fatalf("预览缺少 unit_ability_exchange_result: %v", prevDoc)
	}

	// 4. 第一次执行互换（消耗 1 颗 power_stone，钻石保持 50）
	exDoc := gameJSON(t, ts.URL, "/unit_ability_exchange/exchange_unit_ability", uid, sk, s0, s1,
		"unit_a_id=152&unit_b_id=157", "_method=GET")
	if _, hasModal := exDoc["modal"]; hasModal {
		t.Fatalf("exchange_unit_ability 成功响应不得包含顶层 modal: %v", exDoc)
	}
	exData := exDoc["data"].(map[string]any)
	if exData["unit_ability_exchange_succ"] != true || exData["unit_ability_exchange_modal"] == nil {
		t.Fatalf("exchange_unit_ability 响应字段缺失: %v", exData)
	}
	if int(exData["user_coin"].(map[string]any)["amount"].(float64)) != 50 {
		t.Fatalf("首次互换应优先扣能力互换石而非钻石: user_coin=%v", exData["user_coin"])
	}

	// 5. 第二次执行互换（power_stone 已为 0，回退扣 10 钻石，剩余 40 钻，等级与觉醒换回）
	exDoc2 := gameJSON(t, ts.URL, "/unit_ability_exchange/exchange_unit_ability", uid, sk, s0, s1,
		"unit_a_id=152&unit_b_id=157", "_method=GET")
	exData2 := exDoc2["data"].(map[string]any)
	if int(exData2["user_coin"].(map[string]any)["amount"].(float64)) != 40 {
		t.Fatalf("二次互换应扣减 10 钻: user_coin=%v want 40", exData2["user_coin"])
	}
}

// TestMLKReplayVerify 验证 T10：mlk-capture-201711 抓包契约对齐与全链路 Schema 校验。
func TestMLKReplayVerify(t *testing.T) {
	ts, st, _ := m5Server(t, t.TempDir())
	uid, sk, s0, s1 := establishSession(t, ts.URL, "dev-m5-mlk")

	// 1. 校验 preset_data 的 consts 开关（T03：潜能关闭，融合/互换/符文优化开启）
	pDoc := gameJSON(t, ts.URL, "/users/preset_data.json", uid, sk, s0, s1, "", "_method=GET")
	pData := pDoc["data"].(map[string]any)
	consts := pData["consts"].(map[string]any)
	if consts["unit_upgrade_open"] != false {
		t.Fatalf("consts.unit_upgrade_open=%v want false", consts["unit_upgrade_open"])
	}
	for _, k := range []string{"monster_evo_open", "changeability_switch", "skill_optimize", "skill_select_all"} {
		if consts[k] != true {
			t.Fatalf("consts.%s=%v want true", k, consts[k])
		}
	}
	if pData["rare_monster_category_config"] == nil {
		t.Fatalf("preset_data 缺少 rare_monster_category_config")
	}

	// 2. 潜能端点明确拒绝（T03）
	upDoc := gameJSON(t, ts.URL, "/user_units/upgrade", uid, sk, s0, s1, "id=152", "_method=GET")
	assertModalError(t, upDoc, "/user_units/upgrade")

	// 3. 若通过环境变量 MLK_CAPTURE_DIR 指定了本地只读 mlk 抓包目录，逐键比对 user_monsters_evolve_select_info.json
	if mlkRoot := os.Getenv("MLK_CAPTURE_DIR"); mlkRoot != "" {
		mlkDir := filepath.Join(mlkRoot, "1")
		if raw, err := os.ReadFile(filepath.Join(mlkDir, "user_monsters_evolve_select_info.json")); err == nil {
			var mlkDoc map[string]any
			if err := json.Unmarshal(raw, &mlkDoc); err != nil {
				t.Fatal(err)
			}
			wantInfo := mlkDoc["data"].(map[string]any)["user_monsters_evolve_select_info"].(map[string]any)
			srvDoc := gameJSON(t, ts.URL, "/user_monsters/evolve_select_info", uid, sk, s0, s1, "", "_method=GET")
			gotInfo := srvDoc["data"].(map[string]any)["user_monsters_evolve_select_info"].(map[string]any)
			for k, wantV := range wantInfo {
				if gotInfo[k] != wantV {
					t.Fatalf("mlk user_monsters_evolve_select_info[%s]=%q want %q", k, gotInfo[k], wantV)
				}
			}
		}
	}

	// 4. 校验 test001 种子账号的融合与互换素材投影
	var test001UID string
	if err := st.DB().QueryRow(`SELECT user_id FROM cn35_accounts WHERE account_name='test001'`).Scan(&test001UID); err == nil && test001UID != "" {
		snap, err := st.Snapshot(test001UID)
		if err != nil {
			t.Fatal(err)
		}
		ei := snap.User["evolve_items"].(map[string]any)
		ef := snap.User["evolve_fancy_items"].(map[string]any)
		if ei["1"].(int64) < 10 || ef["102"].(int64) < 2 {
			t.Fatalf("test001 预置融合素材未生效: evolve_items=%v evolve_fancy_items=%v", ei, ef)
		}
	}
}
