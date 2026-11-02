// signin_test.go LOGIN350-02：每月 28 格签到的台账、解锁、补签、幂等、
// 回滚与账号隔离（真实 SQLite）。
package cn35store

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// signinLoc 东八区。
var signinLoc = time.FixedZone("UTC+8", 8*3600)

// signinNow 返回 2026-10-<day> 10:00（东八区）的 Unix 秒。
func signinNow(day int) int64 {
	return time.Date(2026, 10, day, 10, 0, 0, 0, signinLoc).Unix()
}

// signinClaimRows 该账号某月台账行数。
func signinClaimRows(t *testing.T, s *Store, uid, month string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_signin_claims
		WHERE user_id=? AND month_key=?`, uid, month).Scan(&n); err != nil {
		t.Fatalf("读取签到台账: %v", err)
	}
	return n
}

// signinItemQty 读某 content_id 对应素材数量。
func signinItemQty(t *testing.T, s *Store, uid string, contentID int64) int64 {
	t.Helper()
	var qty int64
	err := s.DB().QueryRow(`SELECT p.quantity FROM cn35_player_items p
		JOIN cn35_item_catalog c ON c.item_key=p.item_key
		WHERE p.user_id=? AND c.content_id=?`, uid, contentID).Scan(&qty)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("读取素材: %v", err)
	}
	return qty
}

func TestSigninStateAndClaim(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-signin-claim")
	now := signinNow(5)

	st, err := s.SigninState(uid, now)
	if err != nil {
		t.Fatalf("SigninState: %v", err)
	}
	if st.MonthKey != "2026-10" || st.Day != 5 || len(st.ClaimedNodes) != 0 {
		t.Fatalf("初始签到态 = %+v", st)
	}

	// 第 1 格：种子 ×5（属性按轮换）。
	want := cn35game.SigninNodeReward(1, now)
	res, err := s.ClaimSignin(uid, now, 1)
	if err != nil || !res.Granted {
		t.Fatalf("ClaimSignin(node=1) = %+v, %v", res, err)
	}
	if res.Reward.Coin != 50 || res.Reward.Medal != 25 || res.Reward.ItemCount != 5 {
		t.Fatalf("第 1 格奖励 = %+v", res.Reward)
	}
	if got := springBalance(t, s, uid, "coin"); got != 50 {
		t.Fatalf("钻石 = %d, want 50", got)
	}
	if got := springBalance(t, s, uid, "medal"); got != 25 {
		t.Fatalf("徽章 = %d, want 25", got)
	}
	if got := signinItemQty(t, s, uid, want.ItemContentID); got != 5 {
		t.Fatalf("素材 content_id=%d 数量 = %d, want 5", want.ItemContentID, got)
	}

	// 第 2 格：金币 25,000。
	if _, err := s.ClaimSignin(uid, now, 2); err != nil {
		t.Fatalf("ClaimSignin(node=2): %v", err)
	}
	if got := springBalance(t, s, uid, "gacha_point"); got != springSeedPoint+25000 {
		t.Fatalf("金币 = %d, want %d", got, springSeedPoint+25000)
	}

	// 未解锁节点（day=5 时的第 6 格）与重复领取。
	if _, err := s.ClaimSignin(uid, now, 6); !errors.Is(err, cn35game.ErrSigninNotReady) {
		t.Fatalf("未解锁节点应 ErrSigninNotReady, got %v", err)
	}
	if _, err := s.ClaimSignin(uid, now, 1); !errors.Is(err, cn35game.ErrSigninNotReady) {
		t.Fatalf("重复领取应 ErrSigninNotReady, got %v", err)
	}
	if _, err := s.ClaimSignin(uid, now, 0); !errors.Is(err, cn35game.ErrSigninInvalidNode) {
		t.Fatalf("非法节点应 ErrSigninInvalidNode, got %v", err)
	}
	if n := signinClaimRows(t, s, uid, "2026-10"); n != 2 {
		t.Fatalf("台账 = %d 行, want 2", n)
	}
}

func TestSigninCatchUpAndWeeklyNodes(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-signin-catchup")
	now := signinNow(10)

	// 前 10 天一次没领，第 10 天可补领 1..10 任意节点。
	for node := 1; node <= 10; node++ {
		if _, err := s.ClaimSignin(uid, now, node); err != nil {
			t.Fatalf("补领 node=%d: %v", node, err)
		}
	}
	if _, err := s.ClaimSignin(uid, now, 11); !errors.Is(err, cn35game.ErrSigninNotReady) {
		t.Fatalf("第 11 格未解锁: %v", err)
	}
	// 周节点 7 已在前 10 内领取，校验其奖励：花 ×4。
	var coin, medal, count int64
	if err := s.DB().QueryRow(`SELECT coin,medal,item_count FROM cn35_signin_claims
		WHERE user_id=? AND month_key='2026-10' AND node=7`, uid).
		Scan(&coin, &medal, &count); err != nil {
		t.Fatalf("读取周节点台账: %v", err)
	}
	if coin != 200 || medal != 200 || count != 4 {
		t.Fatalf("周节点奖励 = coin %d medal %d count %d, want 200/200/4", coin, medal, count)
	}
}

func TestSigninDay28HighTier(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-signin-28")
	now := signinNow(28)
	res, err := s.ClaimSignin(uid, now, 28)
	if err != nil || !res.Granted {
		t.Fatalf("ClaimSignin(28) = %+v, %v", res, err)
	}
	if res.Reward.Coin != 200 || res.Reward.Medal != 300 || res.Reward.ItemCount != 6 {
		t.Fatalf("第 28 格奖励 = %+v", res.Reward)
	}
}

func TestSigninConcurrentIdempotent(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-signin-race")
	now := signinNow(3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.ClaimSignin(uid, now, 1)
			if err == nil && res.Granted {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != 1 {
		t.Fatalf("并发签到成功 %d 次, want 1", granted)
	}
	if got := springBalance(t, s, uid, "coin"); got != 50 {
		t.Fatalf("并发后钻石 = %d, want 50", got)
	}
}

// TestSigninRollbackWhenItemMissing 素材目录缺失时整笔回滚：台账、余额、素材都不写。
func TestSigninRollbackWhenItemMissing(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-signin-rollback")
	now := signinNow(5)
	reward := cn35game.SigninNodeReward(1, now)
	if _, err := s.DB().Exec(`UPDATE cn35_item_catalog SET enabled=0 WHERE content_id=?`,
		reward.ItemContentID); err != nil {
		t.Fatalf("停用目录行: %v", err)
	}
	if _, err := s.ClaimSignin(uid, now, 1); err == nil {
		t.Fatal("素材目录缺失时领取应失败")
	}
	if n := signinClaimRows(t, s, uid, "2026-10"); n != 0 {
		t.Fatalf("回滚失败：台账 %d 行", n)
	}
	if got := springBalance(t, s, uid, "coin"); got != 0 {
		t.Fatalf("回滚失败：钻石 = %d", got)
	}
}

func TestSigninAccountIsolationAndMonthRollover(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	a := springNewAccount(t, s, "dev-signin-a")
	b := springNewAccount(t, s, "dev-signin-b")
	oct := signinNow(5)
	nov := time.Date(2026, 11, 2, 10, 0, 0, 0, signinLoc).Unix()

	if _, err := s.ClaimSignin(a, oct, 1); err != nil {
		t.Fatalf("A 十月签到: %v", err)
	}
	if got := springBalance(t, s, b, "coin"); got != 0 {
		t.Fatalf("B 不应继承 A 的签到: %d", got)
	}
	// 跨月独立：A 十一月可再领第 1 格。
	st, err := s.SigninState(a, nov)
	if err != nil {
		t.Fatalf("SigninState(nov): %v", err)
	}
	if st.MonthKey != "2026-11" || len(st.ClaimedNodes) != 0 {
		t.Fatalf("十一月初始态 = %+v", st)
	}
	if _, err := s.ClaimSignin(a, nov, 1); err != nil {
		t.Fatalf("A 十一月签到: %v", err)
	}
	if got := springBalance(t, s, a, "coin"); got != 100 {
		t.Fatalf("两月合计钻石 = %d, want 100", got)
	}
}
