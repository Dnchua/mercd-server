// items_test.go ITEM350-02/03/06/07/09：物品目录/库存、资源配置发布回滚、
// 徽章发放与兑换、觉醒书消费的持久化验收。
package cn35store

import (
	"errors"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// newAccountUID 建一个游客账号并回读 user_id（复用 questUserID 辅助）。
func newAccountUID(t *testing.T, s *Store) string {
	t.Helper()
	return questUserID(t, s, "dev-items-"+randHex(6))
}

func creditItem(t *testing.T, s *Store, uid, key string, n int) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO cn35_player_items
		(user_id,item_key,quantity,updated_at) VALUES (?,?,?,?)
		ON CONFLICT(user_id,item_key) DO UPDATE SET quantity=quantity+excluded.quantity`,
		uid, key, n, nowUTC()); err != nil {
		t.Fatal(err)
	}
}

func resourceTestMonsterValidator(monsterID int) error {
	if monsterID <= 0 {
		return cn35game.ErrResourceDraft
	}
	return nil
}

func TestItemCatalogSeeded(t *testing.T) {
	s := openStore(t, t.TempDir())
	defs, err := s.ItemCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 51 {
		t.Fatalf("目录种子 = %d, want 51", len(defs))
	}
	byKey := map[string]cn35game.ItemDef{}
	for _, d := range defs {
		byKey[d.ItemKey] = d
	}
	if d := byKey["powerup_item_1"]; d.ContentID != 1 || d.Element != 1 || d.Form != 1 {
		t.Fatalf("powerup_item_1 种子错误: %+v", d)
	}
	if d := byKey["awakening_book_5"]; d.Kind != "book" || d.Rare != 5 ||
		d.IconAssetName != "common_icon/item_4" {
		t.Fatalf("awakening_book_5 种子错误: %+v", d)
	}
	if d := byKey["ap_potion"]; d.Kind != "consumable" || d.ContentID != 201 ||
		d.IconAssetName != "common_icon/item_67" || d.Name != "元气之水" {
		t.Fatalf("ap_potion 种子错误: %+v", d)
	}
	if d := byKey["bp_potion"]; d.Kind != "consumable" || d.ContentID != 202 ||
		d.IconAssetName != "common_icon/item_139" || d.Name != "勇气之水" {
		t.Fatalf("bp_potion 种子错误: %+v", d)
	}
}

func TestPlayerItemsProjection(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := newAccountUID(t, s)
	creditItem(t, s, uid, "powerup_item_1", 3)
	creditItem(t, s, uid, "awakening_book_5", 1)
	items, err := s.PlayerItems(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("库存行 = %d, want 2", len(items))
	}
	// 快照投影：user.powerup_items 只含 material；medal=0 行已在建号种子。
	snap, err := s.Snapshot(uid)
	if err != nil {
		t.Fatal(err)
	}
	pi, _ := snap.User["powerup_items"].(map[string]int64)
	if pi["1"] != 3 {
		t.Fatalf("powerup_items[1] = %v, want 3", pi["1"])
	}
	if _, ok := pi["101"]; ok {
		t.Fatalf("powerup_items 不应含书籍 content_id 101")
	}
	if snap.User["medal"] != int64(0) {
		t.Fatalf("medal = %v, want 0", snap.User["medal"])
	}
}

func TestResourcePublishAndActivate(t *testing.T) {
	s := openStore(t, t.TempDir())
	drafts, err := s.ResourceDrafts()
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts.Quests) != 27 {
		t.Fatalf("草稿关卡 = %d, want 27", len(drafts.Quests))
	}
	if len(drafts.Goods) != 47 {
		t.Fatalf("草稿商品 = %d, want 47", len(drafts.Goods))
	}
	var potion *cn35game.MedalGoodDraft
	for i := range drafts.Goods {
		if drafts.Goods[i].GoodsKey == "medal_potion_ap" {
			potion = &drafts.Goods[i]
		}
	}
	if potion == nil || potion.Price != 0 || potion.Group != "rare_medal" {
		t.Fatalf("AP 药水草稿 = %+v", potion)
	}
	v1, d1, err := s.PublishResourceConfig("tester", "首发", resourceTestMonsterValidator)
	if err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	if v1 != 1 || len(d1) != 64 {
		t.Fatalf("version=%d digest=%q", v1, d1)
	}
	active, err := s.ActiveResourceConfig()
	if err != nil || active == nil {
		t.Fatalf("生效配置缺失: %v", err)
	}
	if active.Version != 1 || len(active.Quests) != 27 {
		t.Fatalf("生效版本 = %+v", active)
	}
	// 改草稿再发布 v2，回滚 v1 后读面恢复。
	q := drafts.Quests[0]
	q.AP = 99
	if err := s.SaveResourceQuest(q, "tester"); err != nil {
		t.Fatal(err)
	}
	v2, _, err := s.PublishResourceConfig("tester", "调 AP", resourceTestMonsterValidator)
	if err != nil {
		t.Fatal(err)
	}
	if v2 != 2 {
		t.Fatalf("v2 = %d", v2)
	}
	if err := s.ActivateResourceVersion(1, "tester", "回滚"); err != nil {
		t.Fatal(err)
	}
	active, _ = s.ActiveResourceConfig()
	if active.Version != 1 {
		t.Fatalf("回滚后版本 = %d", active.Version)
	}
	for _, qq := range active.Quests {
		if qq.QuestKey == q.QuestKey && qq.AP != 5 {
			t.Fatalf("回滚后 %s AP=%d, want 5", qq.QuestKey, qq.AP)
		}
	}
	if err := s.ActivateResourceVersion(999, "tester", ""); !errors.Is(err, cn35game.ErrResourceVersionUnknown) {
		t.Fatalf("未知版本错误 = %v", err)
	}
	// 审计存在。
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_admin_audit
		WHERE action LIKE 'resource.%'`).Scan(&n); err != nil || n < 4 {
		t.Fatalf("resource 审计行 = %d", n)
	}
}

func TestPublishValidationRejectsBookDrop(t *testing.T) {
	s := openStore(t, t.TempDir())
	bad := cn35game.ResourceQuestDraft{
		QuestKey: "mat_fire_1", QuestID: 90011, AreaID: 9001, StageID: 900,
		Name: "x", AP: 5, Sort: 1, Open: true,
		Drops: []cn35game.ResourceDrop{
			{Seq: 1, RewardType: "item", ItemKey: "awakening_book_5",
				ChanceBP: 10000, MinCount: 1, MaxCount: 1},
		},
		Waves: []cn35game.ResourceWave{
			{WaveNo: 1, Slot: 200, MonsterID: 1, ScaleBP: 1000, HpRateBP: 1000, AtRateBP: 1000},
		},
	}
	if err := s.SaveResourceQuest(bad, "t"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishResourceConfig("t", "bad", resourceTestMonsterValidator); !errors.Is(err, cn35game.ErrResourceDraft) {
		t.Fatalf("非法草稿发布未被拦: %v", err)
	}
}

func TestPublishValidationNamespaceAttributeAndItems(t *testing.T) {
	s := openStore(t, t.TempDir())
	drafts, err := s.ResourceDrafts()
	if err != nil {
		t.Fatal(err)
	}
	// 删除任一资源关后不得静默发布不完整配置。
	if err := s.DeleteResourceQuest("mat_dark_4", "t"); err != nil {
		t.Fatal(err)
	}
	errs, err := s.ValidateResourceDrafts(resourceTestMonsterValidator)
	if err != nil || len(errs) == 0 {
		t.Fatalf("缺关未报校验错误: %v errors=%v", err, errs)
	}
	// 恢复缺失关，再改火素材本为水素材掉落：跨属性必须失败。
	for _, q := range drafts.Quests {
		if q.QuestKey == "mat_dark_4" {
			if err := s.SaveResourceQuest(q, "t"); err != nil {
				t.Fatal(err)
			}
		}
		if q.QuestKey == "mat_fire_1" {
			q.Drops[0].ItemKey = "powerup_item_4" // 水之种子
			if err := s.SaveResourceQuest(q, "t"); err != nil {
				t.Fatal(err)
			}
		}
	}
	errs, err = s.ValidateResourceDrafts(resourceTestMonsterValidator)
	if err != nil || len(errs) == 0 {
		t.Fatalf("跨属性掉落未报校验错误: %v errors=%v", err, errs)
	}
	// 停用被商品引用的觉醒书也必须挡住发布。
	if _, err := s.DB().Exec(`UPDATE cn35_item_catalog SET enabled=0
		WHERE item_key='awakening_book_5'`); err != nil {
		t.Fatal(err)
	}
	errs, err = s.ValidateResourceDrafts(resourceTestMonsterValidator)
	if err != nil || len(errs) == 0 {
		t.Fatalf("停用物品未报校验错误: %v errors=%v", err, errs)
	}
}

func TestMedalGrantAndExchange(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := newAccountUID(t, s)
	if _, _, err := s.PublishResourceConfig("t", "首发", resourceTestMonsterValidator); err != nil {
		t.Fatal(err)
	}
	// 余额不足拒绝。
	if _, err := s.ExchangeMedal(uid, 4, 1, ""); !errors.Is(err, cn35game.ErrMedalInsufficient) {
		t.Fatalf("余额不足 = %v", err)
	}
	// admin 发放幂等；reason 是审计必填字段。
	if err := s.GrantMedal(uid, 600, "ops", "  ", "rk-bad"); !errors.Is(err, cn35game.ErrInvalidRequest) {
		t.Fatalf("空 reason 发放未被拒绝: %v", err)
	}
	if err := s.GrantMedal(uid, 600, "ops", "开服补偿", "rk-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantMedal(uid, 600, "ops", "开服补偿", "rk-1"); !errors.Is(err, cn35game.ErrDuplicateRequest) {
		t.Fatalf("重复发放 = %v", err)
	}
	if err := s.GrantMedal(uid, 600, "ops", "不同理由", "rk-1"); !errors.Is(err, cn35game.ErrDuplicateRequestConflict) {
		t.Fatalf("幂等键载荷冲突 = %v", err)
	}
	snap, _ := s.Snapshot(uid)
	if snap.User["medal"] != int64(600) {
		t.Fatalf("medal = %v, want 600", snap.User["medal"])
	}
	// 兑换 5★书（500）：成功一次，重放 request_key 不再扣。
	res, err := s.ExchangeMedal(uid, 4, 1, "ex-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Medal != 100 || res.ItemKey != "awakening_book_5" {
		t.Fatalf("兑换结果 = %+v", res)
	}
	res2, err := s.ExchangeMedal(uid, 4, 1, "ex-1")
	if err != nil || !res2.Duplicate || res2.Medal != 100 {
		t.Fatalf("重放 = %+v err=%v", res2, err)
	}
	if _, err := s.ExchangeMedal(uid, 4, 2, "ex-1"); !errors.Is(err, cn35game.ErrDuplicateRequestConflict) {
		t.Fatalf("兑换幂等键冲突 = %v", err)
	}
	// 未发布草稿不得改变玩家正在兑换的已发布价格/限购。
	drafts, err := s.ResourceDrafts()
	if err != nil {
		t.Fatal(err)
	}
	var good cn35game.MedalGoodDraft
	for _, g := range drafts.Goods {
		if g.GoodsID == 4 {
			good = g
		}
	}
	one := 1
	good.Price = 1
	good.PurchaseLimit = &one
	if err := s.SaveMedalGood(good, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExchangeMedal(uid, 4, 1, "draft-price"); !errors.Is(err, cn35game.ErrMedalInsufficient) {
		t.Fatalf("未发布价格影响兑换: %v", err)
	}
	if _, _, err := s.PublishResourceConfig("tester", "限购", resourceTestMonsterValidator); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExchangeMedal(uid, 4, 1, "ex-limit"); !errors.Is(err, cn35game.ErrPurchaseLimit) {
		t.Fatalf("限购未生效 = %v", err)
	}
	// 库存与再兑换余额校验。
	items, _ := s.PlayerItems(uid)
	got := 0
	for _, pi := range items {
		if pi.ItemKey == "awakening_book_5" {
			got = pi.Quantity
		}
	}
	if got != 1 {
		t.Fatalf("书库存 = %d, want 1", got)
	}
	if _, err := s.ExchangeMedal(uid, 4, 1, "ex-2"); !errors.Is(err, cn35game.ErrPurchaseLimit) {
		t.Fatalf("二次兑换未命中限购 = %v", err)
	}
}

// TestMedalExchangeFreePotion price=0 商品：任意余额下可兑换、实扣 0、
// 不限购可重复兑换（0023 AP 药水；BP 药水已于 0033 下架）。
func TestMedalExchangeFreePotion(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := newAccountUID(t, s)
	if _, _, err := s.PublishResourceConfig("t", "首发", resourceTestMonsterValidator); err != nil {
		t.Fatal(err)
	}
	// 徽章余额为 0 也能兑换（免费商品不做扣减）。
	for i := 0; i < 3; i++ {
		res, err := s.ExchangeMedal(uid, 20, 1, "")
		if err != nil {
			t.Fatalf("第 %d 次免费兑换失败: %v", i, err)
		}
		if res.Medal != 0 {
			t.Fatalf("免费兑换后 medal = %d, want 0", res.Medal)
		}
	}
	// BP 药水（goods_id=21）已于 0033 下架，兑换应被拒。
	if _, err := s.ExchangeMedal(uid, 21, 5, ""); err == nil {
		t.Fatal("已下架的 BP 药水仍可兑换")
	}
	items, _ := s.PlayerItems(uid)
	got := map[string]int{}
	for _, pi := range items {
		got[pi.ItemKey] = pi.Quantity
	}
	if got["ap_potion"] != 3 || got["bp_potion"] != 0 {
		t.Fatalf("药水库存 = %v", got)
	}
}

func TestAwakenUnit(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := newAccountUID(t, s)
	// 建一只 2★ 单位库存行（模板表不校验，直接插库）。
	if _, err := s.DB().Exec(`INSERT INTO cn35_player_units
		(user_id,unit_id,level,duplicated_count,protected,newcomer,created_at,updated_at)
		VALUES (?,1,1,0,0,1,?,?)`, uid, nowUTC(), nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AwakenUnit(uid, 1, 2, 15, 1); !errors.Is(err, cn35game.ErrItemInsufficient) {
		t.Fatalf("无书觉醒 = %v", err)
	}
	creditItem(t, s, uid, "awakening_book_2", 1)
	res, err := s.AwakenUnit(uid, 1, 2, 15, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.DuplicatedCount != 1 {
		t.Fatalf("dup = %d", res.DuplicatedCount)
	}
	items, _ := s.PlayerItems(uid)
	for _, pi := range items {
		if pi.ItemKey == "awakening_book_2" {
			t.Fatalf("书未扣减")
		}
	}
}

func TestSettleBattleItemReward(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := newAccountUID(t, s)
	if _, _, err := s.PublishResourceConfig("t", "首发", resourceTestMonsterValidator); err != nil {
		t.Fatal(err)
	}
	sess := &cn35game.BattleSession{
		BattleID: "bqtestitem", UserID: uid, QuestID: 90011, AreaID: 9001,
		Difficulty: "normal", Status: cn35game.BattlePrepared, APCost: 1,
		Rewards: []cn35game.BattleReward{
			{Key: "item", ID: 1, Count: 3},
			{Key: "gacha_point", Count: 500},
		},
		NextStageID: 1, NextAreaID: 1, NextQuestID: 1,
		ConfigVersion: 1,
	}
	if err := s.OpenBattle(sess, 86400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PayBattleAP(uid, sess.BattleID); err != nil {
		t.Fatal(err)
	}
	res, err := s.SettleBattle(uid, sess.BattleID, cn35game.BattleReport{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Already {
		t.Fatal("首结被判重放")
	}
	items, _ := s.PlayerItems(uid)
	if len(items) != 1 || items[0].ItemKey != "powerup_item_1" || items[0].Quantity != 3 {
		t.Fatalf("素材入账 = %+v", items)
	}
	snap, _ := s.Snapshot(uid)
	if snap.User["gacha_point"] != int64(seedGachaPoint+500) {
		t.Fatalf("gacha_point = %v", snap.User["gacha_point"])
	}
	var ev int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_resource_reward_events
		WHERE user_id=? AND request_key=?`, uid, sess.BattleID).Scan(&ev); err != nil || ev != 1 {
		t.Fatalf("奖励台账 = %d err=%v", ev, err)
	}
	// 重放幂等。
	res2, _ := s.SettleBattle(uid, sess.BattleID, cn35game.BattleReport{})
	if !res2.Already {
		t.Fatal("重放未幂等")
	}
	items, _ = s.PlayerItems(uid)
	if items[0].Quantity != 3 {
		t.Fatalf("重放重复入账 quantity=%d", items[0].Quantity)
	}
}

// TestUseItemPotion AP/BP 药水：回满对应体力 + 条件扣减；素材/未知物品拒用。
func TestUseItemPotion(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := newAccountUID(t, s)
	// 压低 AP/BP 便于观察回满。
	if _, err := s.DB().Exec(`UPDATE cn35_player_profiles SET ap=3,bp=10 WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	creditItem(t, s, uid, "ap_potion", 2)
	creditItem(t, s, uid, "bp_potion", 1)
	res, err := s.UseItem(uid, 201, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.ItemKey != "ap_potion" {
		t.Fatalf("ItemKey = %q", res.ItemKey)
	}
	var ap, bp int
	if err := s.DB().QueryRow(`SELECT ap,bp FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&ap, &bp); err != nil {
		t.Fatal(err)
	}
	if ap != 20 {
		t.Fatalf("ap = %d, want 20（回满 max_ap）", ap)
	}
	if bp != 10 {
		t.Fatalf("bp 不应被 AP 药水改动: %d", bp)
	}
	if _, err := s.UseItem(uid, 202, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT bp FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&bp); err != nil {
		t.Fatal(err)
	}
	if bp != 300 {
		t.Fatalf("bp = %d, want 300（回满 max_bp）", bp)
	}
	// 库存扣减验证：ap_potion 剩 1。
	items, _ := s.PlayerItems(uid)
	got := map[string]int{}
	for _, pi := range items {
		got[pi.ItemKey] = pi.Quantity
	}
	if got["ap_potion"] != 1 || got["bp_potion"] != 0 {
		t.Fatalf("药水库存 = %v", got)
	}
	// 库存不足 / 非消耗品 / 未知物品。
	if _, err := s.UseItem(uid, 202, 1); !errors.Is(err, cn35game.ErrItemInsufficient) {
		t.Fatalf("空库存使用 = %v", err)
	}
	if _, err := s.UseItem(uid, 1, 1); !errors.Is(err, cn35game.ErrItemNotUsable) {
		t.Fatalf("素材使用 = %v", err)
	}
	if _, err := s.UseItem(uid, 999, 1); !errors.Is(err, cn35game.ErrItemNotFound) {
		t.Fatalf("未知 content_id = %v", err)
	}
}

// TestAwakenUnitCount 批量觉醒：count 本书 → dup+count，超上限拒绝。
func TestAwakenUnitCount(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := newAccountUID(t, s)
	if _, err := s.DB().Exec(`INSERT INTO cn35_player_units
		(user_id,unit_id,level,duplicated_count,protected,newcomer,created_at,updated_at)
		VALUES (?,1,1,13,0,1,?,?)`, uid, nowUTC(), nowUTC()); err != nil {
		t.Fatal(err)
	}
	creditItem(t, s, uid, "awakening_book_2", 3)
	// dup 13 + count 3 > maxDup 15 → ErrAwakeCap。
	if _, err := s.AwakenUnit(uid, 1, 2, 15, 3); !errors.Is(err, cn35game.ErrAwakeCap) {
		t.Fatalf("超上限批量 = %v", err)
	}
	res, err := s.AwakenUnit(uid, 1, 2, 15, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.DuplicatedCount != 15 {
		t.Fatalf("dup = %d, want 15", res.DuplicatedCount)
	}
	items, _ := s.PlayerItems(uid)
	got := map[string]int{}
	for _, pi := range items {
		got[pi.ItemKey] = pi.Quantity
	}
	if got["awakening_book_2"] != 1 {
		t.Fatalf("书库存 = %v, want 1", got)
	}
}
