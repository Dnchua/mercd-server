// adventure_test.go LOGIN350-03：登录冒险轮次日程、当日一次性弹窗开关、
// 逐日发放幂等、错过不补、轮次滚动、时钟口径、故障回滚、账号隔离、
// 重启恢复与 0035→0036 迁移升级（真实 SQLite + 真实迁移文件）。
//
// 语义真源见 notes/proposed/2026-10-26-cn35-login-adventure.md 与
// cn35game/adventure.go 文件头；验收 ID 见 plans/.../README.md（LA-02—LA-12）。
package cn35store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// advLoc 东八区（与 cn35game 内部 springLoc 同口径，独立声明避免跨包耦合）。
var advLoc = time.FixedZone("UTC+8", 8*3600)

// advAt 返回 2026-11-<day> <h>:<m>:<s>（东八区）的 Unix 秒。
func advAt(day, h, m, s int) int64 {
	return time.Date(2026, 11, day, h, m, s, 0, advLoc).Unix()
}

// advPresents 该账号待领礼物行（content_type/content_id/amount/uuid）。
type advPresent struct {
	UUID        string
	ContentType string
	ContentID   int64
	Amount      int64
}

func advPresentsOf(t *testing.T, s *Store, uid string) []advPresent {
	t.Helper()
	// 只要冒险发放行：建号时 STARTER-GIFT 会写 starter-* 三条，不能算进来。
	rows, err := s.DB().Query(`SELECT uuid,content_type,content_id,amount
		FROM cn35_user_presents WHERE user_id=? AND uuid LIKE 'adv-%'
		ORDER BY created_at ASC, uuid ASC`, uid)
	if err != nil {
		t.Fatalf("读取礼物行: %v", err)
	}
	defer rows.Close()
	var out []advPresent
	for rows.Next() {
		var p advPresent
		if err := rows.Scan(&p.UUID, &p.ContentType, &p.ContentID, &p.Amount); err != nil {
			t.Fatalf("扫描礼物行: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// advCall 调一次读面（断言无错误）。
func advCall(t *testing.T, s *Store, uid string, nowSec int64) (*cn35game.AdventureState, *cn35game.AdventureConfig) {
	t.Helper()
	st, cfg, err := s.AdventureState(uid, nowSec)
	if err != nil {
		t.Fatalf("AdventureState(%s,%d): %v", uid, nowSec, err)
	}
	if st == nil || cfg == nil {
		t.Fatalf("预期真实轮次数据，实际空态（st=%v cfg=%v）", st, cfg)
	}
	return st, cfg
}

// TestAdventureFreshRoundGrant 首拉：建轮、当日发放第 1 天、写入礼物盒内容池、
// 台账与礼物行一一对应（LA-02/LA-05）。
func TestAdventureFreshRoundGrant(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "adv-dev-1")
	now := advAt(10, 10, 0, 0)

	st, cfg := advCall(t, s, uid, now)
	if st.RoundIndex != 0 {
		t.Fatalf("首拉 round_index=%d，预期 0", st.RoundIndex)
	}
	if st.RewardConfigID != 1 || cfg.RewardConfigID != 1 {
		t.Fatalf("首拉 reward_config_id=%d/%d，预期种子首套=1", st.RewardConfigID, cfg.RewardConfigID)
	}
	if st.BeginDate != "2026-11-10" {
		t.Fatalf("begin_date=%q，预期 2026-11-10", st.BeginDate)
	}
	if st.CurrentDay != 1 || st.GrantedDay != 1 {
		t.Fatalf("currentDay=%d grantedDay=%d，预期 1/1", st.CurrentDay, st.GrantedDay)
	}
	if !st.TodayShown {
		t.Fatal("首拉应标记当日首次（today_days 数字 ⇒ 弹窗）")
	}
	if len(cfg.Days) != cfg.DurationDays || cfg.DurationDays != 10 {
		t.Fatalf("轮长/日表不一致：duration=%d days=%d", cfg.DurationDays, len(cfg.Days))
	}
	if len(cfg.Path) != 20 {
		t.Fatalf("path 数值数=%d，预期 20（10 节点蛇形折线）", len(cfg.Path))
	}
	if st.PresentUUID == "" {
		t.Fatal("发放后应记录礼物 uuid")
	}

	// 台账 = 1 行且与礼物行同 uuid（跨域写入同事务的可核对证据）。
	var (
		grantType   string
		grantID     int64
		grantAmount int64
		presentUUID string
	)
	if err := s.DB().QueryRow(`SELECT grant_type,grant_content_id,grant_amount,present_uuid
		FROM cn35_adventure_claims WHERE user_id=? AND round_index=? AND day=?`,
		uid, 0, 1).Scan(&grantType, &grantID, &grantAmount, &presentUUID); err != nil {
		t.Fatalf("台账行缺失: %v", err)
	}
	day, ok := cfg.AdventureDay(1)
	if !ok {
		t.Fatal("配置缺第 1 天")
	}
	if grantType != day.GrantType || grantID != day.GrantContentID || grantAmount != day.GrantAmount {
		t.Fatalf("台账内容 %s/%d/%d 与配置 %s/%d/%d 不一致",
			grantType, grantID, grantAmount, day.GrantType, day.GrantContentID, day.GrantAmount)
	}
	presents := advPresentsOf(t, s, uid)
	if len(presents) != 1 {
		t.Fatalf("礼物行数=%d，预期 1（当日恰好一条）", len(presents))
	}
	if presents[0].UUID != presentUUID || presents[0].ContentType != grantType ||
		presents[0].ContentID != grantID || presents[0].Amount != grantAmount {
		t.Fatalf("礼物行 %+v 与台账 %s/%d/%d/%s 不一致",
			presents[0], grantType, grantID, grantAmount, presentUUID)
	}
}

// TestAdventureSameDaySecondPull 同日第二次拉取：不补发、不再弹窗（LA-04/LA-06）。
func TestAdventureSameDaySecondPull(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "adv-dev-2")
	now := advAt(10, 9, 0, 0)
	advCall(t, s, uid, now)

	st, _ := advCall(t, s, uid, now+30) // 30 秒后再拉（同东八区自然日）
	if st.TodayShown {
		t.Fatal("同日第二次拉取不得再标记当日首次（客户端应走 already shown, skipped）")
	}
	if st.GrantedDay != 0 {
		t.Fatalf("同日第二次拉取 grantedDay=%d，预期 0", st.GrantedDay)
	}
	if got := len(advPresentsOf(t, s, uid)); got != 1 {
		t.Fatalf("礼物行数=%d，预期仍为 1", got)
	}
	if n, err := adventureClaimRows(s.DB(), uid, 0); err != nil || n != 1 {
		t.Fatalf("台账行数=%d err=%v，预期 1", n, err)
	}
}

// TestAdventureNextDayAndMissedDay 跨天前进；跳过的一天不补发（LA-04/LA-07）。
func TestAdventureNextDayAndMissedDay(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "adv-dev-3")
	advCall(t, s, uid, advAt(10, 10, 0, 0))

	st, _ := advCall(t, s, uid, advAt(11, 10, 0, 0))
	if st.CurrentDay != 2 || st.GrantedDay != 2 || !st.TodayShown {
		t.Fatalf("次日 currentDay=%d grantedDay=%d shown=%v，预期 2/2/true",
			st.CurrentDay, st.GrantedDay, st.TodayShown)
	}

	// 11-12 不登录；11-13 直接登：currentDay=4，只发第 4 天，第 2/3 天灰 tag 不补。
	st, _ = advCall(t, s, uid, advAt(13, 10, 0, 0))
	if st.CurrentDay != 4 || st.GrantedDay != 4 {
		t.Fatalf("跳日后 currentDay=%d grantedDay=%d，预期 4/4", st.CurrentDay, st.GrantedDay)
	}
	if !st.AdventureClaimed(1) || !st.AdventureClaimed(2) || st.AdventureClaimed(3) || !st.AdventureClaimed(4) {
		t.Fatalf("位图不应补发跳过的第 3 天：claims=%v", st.ClaimedDays)
	}
	if got := len(advPresentsOf(t, s, uid)); got != 3 {
		t.Fatalf("礼物行数=%d，预期 3（第 1/2/4 天）", got)
	}
}

// TestAdventureRolloverNextConfig 走完一轮后次日开新轮，并轮到下一套配置（LA-08）。
func TestAdventureRolloverNextConfig(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "adv-dev-4")
	// 11-10 首登建轮（第 1 天），随后直接跳到轮末（第 10 天）：仍是本轮。
	advCall(t, s, uid, advAt(10, 10, 0, 0))
	st, _ := advCall(t, s, uid, advAt(19, 10, 0, 0))
	if st.RoundIndex != 0 || st.CurrentDay != 10 || st.GrantedDay != 10 {
		t.Fatalf("轮末 round=%d currentDay=%d granted=%d，预期 0/10/10",
			st.RoundIndex, st.CurrentDay, st.GrantedDay)
	}
	// 次日（11-20）：currentDay=11 > duration ⇒ 滚动开新轮，begin=today，config=2。
	st, cfg := advCall(t, s, uid, advAt(20, 10, 0, 0))
	if st.RoundIndex != 1 {
		t.Fatalf("滚动后 round_index=%d，预期 1", st.RoundIndex)
	}
	if st.BeginDate != "2026-11-20" || st.CurrentDay != 1 || st.GrantedDay != 1 {
		t.Fatalf("新轮 begin=%q currentDay=%d granted=%d，预期 2026-11-20/1/1",
			st.BeginDate, st.CurrentDay, st.GrantedDay)
	}
	if st.RewardConfigID != 2 || cfg.RewardConfigID != 2 {
		t.Fatalf("新轮 reward_config_id=%d/%d，预期滚到第二套配置 2",
			st.RewardConfigID, cfg.RewardConfigID)
	}
	// 新轮位图从零开始（旧轮台账不串轮次）。
	if len(st.ClaimedDays) != 1 || st.ClaimedDays[0] != 1 {
		t.Fatalf("新轮位图=%v，预期仅第 1 天", st.ClaimedDays)
	}
	// 旧轮台账仍在（审计/对账），总礼物 = 旧轮 10 + 新轮 1。
	if n, err := adventureClaimRows(s.DB(), uid, 0); err != nil || n != 2 {
		t.Fatalf("旧轮台账=%d err=%v，预期 2（第 1/10 天）", n, err)
	}
	if got := len(advPresentsOf(t, s, uid)); got != 3 {
		t.Fatalf("礼物行数=%d，预期 3（旧轮 2 + 新轮 1）", got)
	}
}

// TestAdventureEast8Boundary 东八区 23:59:59 → 00:00:01 正好跨天（LA-09）。
func TestAdventureEast8Boundary(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "adv-dev-5")
	st, _ := advCall(t, s, uid, advAt(10, 23, 59, 59))
	if st.CurrentDay != 1 || !st.TodayShown {
		t.Fatalf("跨天前 currentDay=%d shown=%v，预期 1/true", st.CurrentDay, st.TodayShown)
	}
	st, _ = advCall(t, s, uid, advAt(11, 0, 0, 1))
	if st.CurrentDay != 2 || st.GrantedDay != 2 || !st.TodayShown {
		t.Fatalf("跨天后 currentDay=%d granted=%d shown=%v，预期 2/2/true",
			st.CurrentDay, st.GrantedDay, st.TodayShown)
	}
	// 服务器日回到前一天（时钟回拨）：currentDay 钳到 1，不产生第 0 天，
	// 且第 1 天已发过 → 不重复发放。
	st, _ = advCall(t, s, uid, advAt(10, 12, 0, 0))
	if st.CurrentDay != 1 {
		t.Fatalf("时钟回拨后 currentDay=%d，预期钳到 1", st.CurrentDay)
	}
	if st.GrantedDay != 0 {
		t.Fatalf("时钟回拨后不得重复发放，grantedDay=%d", st.GrantedDay)
	}
}

// TestAdventureConcurrentFirstPull 并发首拉只发一次（LA-06）。
func TestAdventureConcurrentFirstPull(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "adv-dev-6")
	now := advAt(10, 10, 0, 0)

	const workers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted int
		shown   int
		fails   []string
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, _, err := s.AdventureState(uid, now)
			if err != nil {
				mu.Lock()
				fails = append(fails, err.Error())
				mu.Unlock()
				return
			}
			mu.Lock()
			if st.GrantedDay == 1 {
				granted++
			}
			if st.TodayShown {
				shown++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(fails) != 0 {
		t.Fatalf("并发请求失败: %v", fails)
	}
	if granted != 1 {
		t.Fatalf("并发首拉发放次数=%d，预期 1", granted)
	}
	// 连接层 MaxOpenConns(1) 串行化：恰好一个请求拿到数字 today_days。
	if shown != 1 {
		t.Fatalf("并发首拉当日首次次数=%d，预期 1", shown)
	}
	if n, err := adventureClaimRows(s.DB(), uid, 0); err != nil || n != 1 {
		t.Fatalf("台账行数=%d err=%v，预期 1", n, err)
	}
	if got := len(advPresentsOf(t, s, uid)); got != 1 {
		t.Fatalf("礼物行数=%d，预期 1", got)
	}
}

// TestAdventureAccountIsolation 账号 B 不继承 A 的轮次/位图/礼物（LA-06）。
func TestAdventureAccountIsolation(t *testing.T) {
	s := openStore(t, t.TempDir())
	uidA := seedAccount(t, s, "adv-dev-7a")
	uidB := seedAccount(t, s, "adv-dev-7b")
	advCall(t, s, uidA, advAt(10, 10, 0, 0))
	stA, _ := advCall(t, s, uidA, advAt(11, 10, 0, 0))
	if stA.CurrentDay != 2 || len(stA.ClaimedDays) != 2 {
		t.Fatalf("账号 A 进度异常: %+v", stA)
	}

	// B 在 11-11 首次登录：自己的轮次从今天开始，只发第 1 天。
	stB, _ := advCall(t, s, uidB, advAt(11, 10, 0, 0))
	if stB.BeginDate != "2026-11-11" || stB.CurrentDay != 1 || stB.GrantedDay != 1 {
		t.Fatalf("账号 B 首拉 begin=%q currentDay=%d granted=%d，预期 2026-11-11/1/1",
			stB.BeginDate, stB.CurrentDay, stB.GrantedDay)
	}
	if len(stB.ClaimedDays) != 1 {
		t.Fatalf("账号 B 位图=%v，预期仅 1 天", stB.ClaimedDays)
	}
	if gotA, gotB := len(advPresentsOf(t, s, uidA)), len(advPresentsOf(t, s, uidB)); gotA != 2 || gotB != 1 {
		t.Fatalf("礼物行数 A=%d B=%d，预期 2/1", gotA, gotB)
	}
}

// TestAdventureNoConfigEmptyState 未配置轮次 ⇒ 空态回落（不是错误）。
func TestAdventureNoConfigEmptyState(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "adv-dev-8")
	if _, err := s.DB().Exec(`DELETE FROM cn35_adventure_configs`); err != nil {
		t.Fatalf("清空轮次配置: %v", err)
	}
	st, cfg, err := s.AdventureState(uid, advAt(10, 10, 0, 0))
	if err != nil {
		t.Fatalf("未配置轮次应回空态而非错误: %v", err)
	}
	if st != nil || cfg != nil {
		t.Fatalf("未配置轮次应回 (nil,nil)，实际 st=%v cfg=%v", st, cfg)
	}
	if got := len(advPresentsOf(t, s, uid)); got != 0 {
		t.Fatalf("空态不应发放礼物，实际 %d 行", got)
	}
}

// TestAdventureGrantFailureRollsBack 发放中途失败整体回滚（LA-11）：
// 配置指向不存在的物品目录 ⇒ 事务内报错，台账/礼物/轮次行全无。
func TestAdventureGrantFailureRollsBack(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := seedAccount(t, s, "adv-dev-9")
	// 停用两套种子配置，换一套「第 1 天发不存在的物品」的坏配置。
	if _, err := s.DB().Exec(`UPDATE cn35_adventure_configs SET enabled=0`); err != nil {
		t.Fatalf("停用种子配置: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO cn35_adventure_configs
			(reward_config_id,duration_days,path_json,sort,enabled,created_at,updated_at)
			VALUES (99,1,'[-100,0,0,0,100,0]',9,1,'2026-10-26T00:00:00Z','2026-10-26T00:00:00Z')`,
		`INSERT INTO cn35_adventure_days
			(reward_config_id,day,title,pos,special,scale,display_type_id,display_type_name,
			 display_content_id,display_count,display_unit_rare,grant_type,grant_content_id,grant_amount)
			VALUES (99,1,'第1天',0,0,1,1,'coin',0,10,0,'item',999999,1)`,
	} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatalf("写入坏配置: %v", err)
		}
	}
	if _, _, err := s.AdventureState(uid, advAt(10, 10, 0, 0)); err == nil {
		t.Fatal("发放内容不存在时应报错（不得静默吞掉）")
	}
	// 三轮跨表断言：没有任何半发放态。
	var claims, presents, rounds int
	for _, q := range []struct {
		sql string
		dst *int
	}{
		{`SELECT count(*) FROM cn35_adventure_claims WHERE user_id=?`, &claims},
		{`SELECT count(*) FROM cn35_user_presents WHERE user_id=? AND uuid LIKE 'adv-%'`, &presents},
		{`SELECT count(*) FROM cn35_adventure_user_rounds WHERE user_id=?`, &rounds},
	} {
		if err := s.DB().QueryRow(q.sql, uid).Scan(q.dst); err != nil {
			t.Fatalf("计数失败: %v", err)
		}
	}
	if claims != 0 || presents != 0 || rounds != 0 {
		t.Fatalf("故障注入后应全回滚，实际 claims=%d presents=%d rounds=%d",
			claims, presents, rounds)
	}
	// 修复配置后同格可重试，且只成功一次。
	if _, err := s.DB().Exec(`UPDATE cn35_adventure_days SET grant_content_id=101
		WHERE reward_config_id=99 AND day=1`); err != nil {
		t.Fatalf("修复配置: %v", err)
	}
	st, _ := advCall(t, s, uid, advAt(10, 10, 0, 0))
	if st.GrantedDay != 1 {
		t.Fatalf("修复后应可发放第 1 天，实际 granted=%d", st.GrantedDay)
	}
	advCall(t, s, uid, advAt(10, 11, 0, 0))
	if got := len(advPresentsOf(t, s, uid)); got != 1 {
		t.Fatalf("修复后重试礼物行数=%d，预期 1", got)
	}
}

// TestAdventureRestartKeepsProgress 重启（关闭再打开库）后进度不丢、不重复发放。
func TestAdventureRestartKeepsProgress(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := seedAccount(t, s, "adv-dev-10")
	now := advAt(10, 10, 0, 0)
	advCall(t, s, uid, now)
	advCall(t, s, uid, advAt(11, 10, 0, 0))
	if err := s.Close(); err != nil {
		t.Fatalf("关闭运行库: %v", err)
	}

	s2 := openStore(t, dir)
	st, _ := advCall(t, s2, uid, advAt(11, 10, 5, 0))
	if st.CurrentDay != 2 || st.GrantedDay != 0 || st.TodayShown {
		t.Fatalf("重启后 currentDay=%d granted=%d shown=%v，预期 2/0/false",
			st.CurrentDay, st.GrantedDay, st.TodayShown)
	}
	if got := len(advPresentsOf(t, s2, uid)); got != 2 {
		t.Fatalf("重启后礼物行数=%d，预期 2", got)
	}
}

// TestAdventureUpgradeFrom0035 旧库（迁移到 0035）升级到 0036：既有账号数据保留、
// 新表可用、轮次可建（LA-12）。
func TestAdventureUpgradeFrom0035(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mercd35.sqlite3")
	buildLegacyDBThrough0035(t, path)

	// 旧库里已有账号 + 一条既有礼物（升级必须原样保留）。
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开旧库: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO cn35_accounts
		(he_account_id,user_id,token,sk,user_name,is_new_user,tutorial_finish,created_at,updated_at)
		VALUES ('he-legacy','legacyuid0001','tok','sk','旧玩家',0,1,
			'2026-10-01T00:00:00Z','2026-10-01T00:00:00Z')`); err != nil {
		t.Fatalf("写旧账号: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO cn35_user_presents
		(uuid,user_id,content_type,content_id,amount,expire_at,received_at,created_at)
		VALUES ('legacy-present','legacyuid0001','coin',1,500,0,0,1790000000)`); err != nil {
		t.Fatalf("写旧礼物: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭旧库: %v", err)
	}

	s := openStore(t, dir) // Open 内建 Migrate：0035 → 0036
	assertLatestSchema(t, s)
	var name string
	if err := s.DB().QueryRow(`SELECT user_name FROM cn35_accounts WHERE user_id='legacyuid0001'`).
		Scan(&name); err != nil || name != "旧玩家" {
		t.Fatalf("升级后旧账号丢失/变更: name=%q err=%v", name, err)
	}
	var presentN int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_user_presents WHERE user_id='legacyuid0001'`).
		Scan(&presentN); err != nil || presentN != 1 {
		t.Fatalf("升级后旧礼物行数=%d err=%v，预期 1", presentN, err)
	}
	// 新表就绪且旧账号可直接进入冒险（种子配置由 0036 提供）。
	st, cfg := advCall(t, s, "legacyuid0001", advAt(10, 10, 0, 0))
	if cfg.RewardConfigID != 1 || st.CurrentDay != 1 || st.GrantedDay != 1 {
		t.Fatalf("升级后首拉异常: cfg=%d currentDay=%d granted=%d",
			cfg.RewardConfigID, st.CurrentDay, st.GrantedDay)
	}
	// 旧礼物与冒险礼物共存（内容池不被清空/覆盖）。
	var total int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_user_presents WHERE user_id='legacyuid0001'`).
		Scan(&total); err != nil || total != 2 {
		t.Fatalf("升级后礼物行数=%d err=%v，预期 2（旧 1 + 冒险 1）", total, err)
	}
	if got := len(advPresentsOf(t, s, "legacyuid0001")); got != 1 {
		t.Fatalf("升级后冒险礼物行数=%d，预期 1", got)
	}
}

// legacyMigrationNum 从迁移文件名解析编号数值前缀（"0035_cn35_xxx.sql" → 35）。
// 不用字符串比较：将来若出现 5 位编号（如 "01000_*.sql"），字典序会误判。
func legacyMigrationNum(t *testing.T, name string) int {
	t.Helper()
	idx := strings.IndexByte(name, '_')
	if idx <= 0 {
		t.Fatalf("迁移文件名缺编号前缀: %s", name)
	}
	n, err := strconv.Atoi(name[:idx])
	if err != nil {
		t.Fatalf("迁移文件名编号非法 %s: %v", name, err)
	}
	return n
}

// buildLegacyDBThrough0035 用真实迁移文件把库建到 0035（不应用 0036），
// 模拟「有数据的旧库升级」。
func buildLegacyDBThrough0035(t *testing.T, path string) {
	t.Helper()
	dir := filepath.Join("..", "database", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取迁移目录: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// 只回放「到 0035 为止」的历史迁移：按编号**数值**过滤（不是字符串比较，
		// 也不逐个列举名字），这样将来再加 0042/0043 或出现 5 位编号时本旧库夹具
		// 仍然只回放 ≤0035。（2026-10-28 批次追加 0038–0041 后，原「列举 0036/0037」
		// 的写法把新迁移误算进 0035 旧库，已由本写法取代。）
		if legacyMigrationNum(t, e.Name()) >= 36 {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) != 35 {
		t.Fatalf("0035 之前应有 35 个迁移脚本，实际 %d", len(names))
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开旧库: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("开启旧库事务: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE schema_migrations
		(name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatalf("建台账表: %v", err)
	}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读取迁移 %s: %v", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("执行迁移 %s: %v", name, err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(raw))
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations VALUES (?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
			name, sum); err != nil {
			t.Fatalf("写台账 %s: %v", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("提交旧库: %v", err)
	}
}
