// events_test.go M8 区域完成率奖励台账：首发、重放零新增、并发单笔生效、
// 账号隔离、重启保持（0056 主键幂等 + 礼物盒同事务）。
package cn35store

import (
	"sync"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func TestEventAreaRewardsIdempotentConcurrentIsolated(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	a := seedAccount(t, s, "evt-a")
	b := seedAccount(t, s, "evt-b")
	claim := []cn35game.EventAreaClaim{{
		EventID: 44, AreaID: 4176, Threshold: 30,
		Presents: []cn35game.EventPresent{
			{ContentType: "coin", ContentID: 1, Amount: 1},
			{ContentType: "unit", ContentID: 1274, Amount: 1},
		},
	}}
	presents := func(uid string) int {
		var n int
		if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_user_presents WHERE user_id=? AND uuid LIKE 'evt-%'`, uid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	got, err := s.ClaimEventAreaRewards(a, claim, 100)
	if err != nil || len(got) != 1 {
		t.Fatalf("首发: %v %v", got, err)
	}
	if again, err := s.ClaimEventAreaRewards(a, claim, 101); err != nil || len(again) != 0 {
		t.Fatalf("重放应零新增: %v %v", again, err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := s.ClaimEventAreaRewards(b, claim, 200)
			if err != nil {
				t.Errorf("并发领取: %v", err)
				return
			}
			mu.Lock()
			total += len(g)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 1 || presents(b) != 2 || presents(a) != 2 {
		t.Fatalf("并发仅一笔生效且账号隔离: total=%d a=%d b=%d", total, presents(a), presents(b))
	}
	claims, err := s.EventAreaRewardClaims(a)
	if err != nil || !claims[[2]int{4176, 30}] || len(claims) != 1 {
		t.Fatalf("台账读面: %v %v", claims, err)
	}
	s.Close()
	s = openStore(t, dir)
	if claims, err := s.EventAreaRewardClaims(b); err != nil || !claims[[2]int{4176, 30}] {
		t.Fatalf("重启后台账: %v %v", claims, err)
	}
	if again, _ := s.ClaimEventAreaRewards(b, claim, 300); len(again) != 0 {
		t.Fatal("重启后重放不应再发")
	}
}
