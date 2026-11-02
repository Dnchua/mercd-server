// adminview_test.go 管理后台数据读面验收：玩家列表/详情/持有同伴/
// 资产礼物合并视图/公告状态推导全部来自真实 cn35_* 表。
package cn35store

import (
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func TestAdminViewPlayersAndDetail(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	baseOverview, err := s.AdminOverview()
	if err != nil {
		t.Fatal(err)
	}
	uid := seedAccount(t, s, "admin-view-a")
	uidB := seedAccount(t, s, "admin-view-b")
	clearPresents(t, s, uid, uidB)
	grantFive(t, s, uid)
	// Decks 为懒创建：真实客户端登录后即拉 parties；详情页同口径先物化。
	if _, err := s.Decks(uid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE cn35_accounts SET user_name='测试玩家A' WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO cn35_user_presents
		(uuid,user_id,content_type,content_id,amount,expire_at,received_at,created_at)
		VALUES ('pv-1',?,'unit',152,1,0,0,1700000000)`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO cn35_infos
		(id,category,title,body,begin_at,end_at,enabled,weight) VALUES
		(1,'news','测试公告','正文',0,0,1,0),
		(2,'event','停用公告','正文',0,0,0,0)`); err != nil {
		t.Fatal(err)
	}

	// 列表：总数 + uid 过滤 + nickname 过滤（含 0045 内置 test001 账号）
	wantPlayers := int(baseOverview.Accounts) + 2
	rows, total, err := s.AdminPlayers(cn35game.AdminPlayerFilter{Page: 1, PageSize: 10})
	if err != nil || total != wantPlayers || len(rows) != wantPlayers {
		t.Fatalf("AdminPlayers: total=%d rows=%d want=%d err=%v", total, len(rows), wantPlayers, err)
	}
	rows, _, err = s.AdminPlayers(cn35game.AdminPlayerFilter{Nickname: "测试玩家", Page: 1, PageSize: 10})
	if err != nil || len(rows) != 1 || rows[0].UserID != uid {
		t.Fatalf("nickname 过滤: %+v err=%v", rows, err)
	}
	// CN3.5 建号种子：level=1、coin=0、gacha_point=302（0003 注释口径）。
	if rows[0].AccountKind != "guest" || rows[0].Level != 1 || rows[0].GachaPoint != 302 {
		t.Fatalf("玩家行字段异常: %+v", rows[0])
	}

	// 详情：单位 5 + 魔宠 1 + 余额行 + 编队 + 进度 + 脱敏 raw
	d, err := s.AdminPlayerDetail(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Units) != 5 || len(d.Monsters) != 1 {
		t.Fatalf("detail 库存: units=%d monsters=%d", len(d.Units), len(d.Monsters))
	}
	if len(d.Balances) == 0 || len(d.Decks) == 0 {
		t.Fatalf("detail balances=%v decks=%v", d.Balances, d.Decks)
	}
	if d.Quest == nil || d.Quest.QuestID != 1 {
		t.Fatalf("detail quest: %+v", d.Quest)
	}
	raw, _ := d.Raw["account"].(map[string]any)
	for _, secret := range []string{"token", "sk", "password_hash"} {
		if _, ok := raw[secret]; ok {
			t.Fatalf("raw 泄露敏感列 %s", secret)
		}
	}
	if _, err := s.AdminPlayerDetail("no-such-user"); err != cn35game.ErrNoAccount {
		t.Fatalf("未知账号应 ErrNoAccount: %v", err)
	}

	// 同伴：user_id 过滤隔离
	units, total, err := s.AdminCharacters(cn35game.AdminCharFilter{UserID: uid, Page: 1, PageSize: 10})
	if err != nil || total != 5 {
		t.Fatalf("AdminCharacters uidA: total=%d err=%v", total, err)
	}
	for _, u := range units {
		if u.OwnerUID != uid || u.Level < 1 {
			t.Fatalf("同伴行: %+v", u)
		}
	}
	units, _, _ = s.AdminCharacters(cn35game.AdminCharFilter{UserID: uidB, Page: 1, PageSize: 10})
	if len(units) != 0 {
		t.Fatalf("uidB 应零库存: %v", units)
	}
	units, _, err = s.AdminCharacters(cn35game.AdminCharFilter{UserID: uid, UnitID: 152, Page: 1, PageSize: 10})
	if err != nil || len(units) != 1 || units[0].UnitID != 152 {
		t.Fatalf("unit_id 精确过滤: %v %v", units, err)
	}

	// 资产合并视图：currency + present
	items, total, err := s.AdminItems(cn35game.AdminItemFilter{UserID: uid, Page: 1, PageSize: 50})
	if err != nil || total < 2 {
		t.Fatalf("AdminItems: total=%d err=%v", total, err)
	}
	var cur, pre bool
	for _, it := range items {
		if it.Kind == "currency" {
			cur = true
		}
		if it.Kind == "present" && it.ID == "present:pv-1" {
			pre = true
		}
	}
	if !cur || !pre {
		t.Fatalf("合并视图缺源: %+v", items)
	}
	items, _, _ = s.AdminItems(cn35game.AdminItemFilter{Kind: "present", Page: 1, PageSize: 10})
	for _, it := range items {
		if it.Kind != "present" {
			t.Fatalf("kind 过滤失效: %+v", it)
		}
	}

	// 公告状态推导：enabled=1 无窗口→active；enabled=0→disabled
	acts, err := s.AdminActivities()
	if err != nil || len(acts) != 2 {
		t.Fatalf("AdminActivities: %v %v", acts, err)
	}
	st := map[int64]string{}
	for _, a := range acts {
		st[a.ID] = a.Status
	}
	if st[1] != "active" || st[2] != "disabled" {
		t.Fatalf("status 推导: %v", st)
	}

	// 总览计数（相对迁移基线增量）
	o, err := s.AdminOverview()
	if err != nil ||
		o.Accounts != baseOverview.Accounts+2 ||
		o.Units != baseOverview.Units+5 ||
		o.Monsters != baseOverview.Monsters+1 ||
		o.Presents != baseOverview.Presents+1 ||
		o.Infos != baseOverview.Infos+2 {
		t.Fatalf("overview: %+v base=%+v err=%v", o, baseOverview, err)
	}
}

// TestAdminViewPagination 分页边界：第 2 页、超界页返回空列表不报错。
func TestAdminViewPagination(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	baseOverview, err := s.AdminOverview()
	if err != nil {
		t.Fatal(err)
	}
	for _, dev := range []string{"pg-a", "pg-b", "pg-c"} {
		seedAccount(t, s, dev)
	}
	wantTotal := int(baseOverview.Accounts) + 3
	rows, total, err := s.AdminPlayers(cn35game.AdminPlayerFilter{Page: 2, PageSize: 2})
	if err != nil || total != wantTotal || len(rows) != 2 {
		t.Fatalf("page2: total=%d rows=%d wantTotal=%d err=%v", total, len(rows), wantTotal, err)
	}
	rows, _, err = s.AdminPlayers(cn35game.AdminPlayerFilter{Page: 9, PageSize: 2})
	if err != nil || len(rows) != 0 {
		t.Fatalf("超界页: %v %v", rows, err)
	}
	if _, _, err = s.AdminPlayers(cn35game.AdminPlayerFilter{Page: 1, PageSize: 100000}); err != nil {
		t.Fatal("pageSize 超上限截断不应报错:", err)
	}
}
