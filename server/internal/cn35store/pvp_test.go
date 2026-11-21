package cn35store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func TestPvPMigration0057TablesExist(t *testing.T) {
	st := openRaidTestStore(t, filepath.Join(t.TempDir(), "pvp0057.sqlite3"))
	defer st.Close()
	for _, tbl := range []string{"cn35_ladder_profiles", "cn35_ladder_decks", "cn35_ladder_grow", "cn35_ladder_sessions",
		"cn35_ladder_settlements", "cn35_rob_profiles", "cn35_rob_decks", "cn35_rob_sessions", "cn35_rob_records"} {
		var n int
		if err := st.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s: n=%d err=%v", tbl, n, err)
		}
	}
}

// TestPvPLadderWeekAndSeasonRollover 跨周写结算台账（仅当周有出战）、跨届清零积分、跨日清零次数。
func TestPvPLadderWeekAndSeasonRollover(t *testing.T) {
	st := openRaidTestStore(t, filepath.Join(t.TempDir(), "pvp-roll.sqlite3"))
	defer st.Close()
	uid := seedAccount(t, st, "pvp-roll")
	per := cn35game.LadderPeriodAt(time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC))
	now := per.WeekEnd.Add(-time.Hour)
	st.SetClock(func() time.Time { return now })
	if _, err := st.LadderView(uid); err != nil {
		t.Fatal(err)
	}
	st.DB().Exec(`UPDATE cn35_ladder_profiles SET score=700, week_battles=1, day_used=2 WHERE user_id=?`, uid)
	now = per.WeekEnd.Add(time.Hour)
	v, err := st.LadderView(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Pending) != 1 || v.Pending[0].Tier != "S" || v.Pending[0].Reward.Medal != cn35game.LadderRules.TierReward["S"].Medal {
		t.Fatalf("结算台账: %+v", v.Pending)
	}
	if v.Profile.DayUsed != 2 {
		t.Fatalf("周二 21 点前后同一游戏日，次数不应清零: %d", v.Profile.DayUsed)
	}
	if got, err := st.LadderClaimRewards(uid); err != nil || len(got) != 1 {
		t.Fatalf("领取: %v %v", got, err)
	}
	if _, err := st.LadderClaimRewards(uid); !errors.Is(err, cn35game.ErrPvPNoReward) {
		t.Fatalf("重复领取应拒绝: %v", err)
	}
	// 无出战的一周不产生台账；跨届积分清零。
	now = per.SeasonEnd.Add(time.Hour)
	v, err = st.LadderView(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Pending) != 0 || v.Profile.Score != 0 || v.Profile.DayUsed != 0 {
		t.Fatalf("跨届: pending=%d score=%d day_used=%d", len(v.Pending), v.Profile.Score, v.Profile.DayUsed)
	}
}

// TestPvPRobTicketOrderAndGold 入场券按序购买、rc>0 不可再购、付费券扣金币。
func TestPvPRobTicketOrderAndGold(t *testing.T) {
	st := openRaidTestStore(t, filepath.Join(t.TempDir(), "pvp-rob.sqlite3"))
	defer st.Close()
	uid := seedAccount(t, st, "pvp-rob")
	if err := st.RobBuyTicket(uid, 2); !errors.Is(err, cn35game.ErrPvPTicket) {
		t.Fatalf("跳档购买应拒绝: %v", err)
	}
	if err := st.RobBuyTicket(uid, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.RobBuyTicket(uid, 2); !errors.Is(err, cn35game.ErrPvPTicket) {
		t.Fatalf("rc>0 应拒绝: %v", err)
	}
	st.DB().Exec(`UPDATE cn35_rob_profiles SET rc_left=0 WHERE user_id=?`, uid)
	st.DB().Exec(`UPDATE cn35_player_balances SET amount=10 WHERE user_id=? AND currency='gacha_point'`, uid)
	if err := st.RobBuyTicket(uid, 2); !errors.Is(err, cn35game.ErrPvPGold) {
		t.Fatalf("金币不足应拒绝: %v", err)
	}
	st.DB().Exec(`INSERT INTO cn35_player_balances (user_id,currency,amount,updated_at) VALUES (?,?,?,?)
		ON CONFLICT(user_id,currency) DO UPDATE SET amount=excluded.amount`, uid, "gacha_point", 60000, nowUTC())
	if err := st.RobBuyTicket(uid, 2); err != nil {
		t.Fatal(err)
	}
	var gp, rc int
	st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, uid).Scan(&gp)
	st.DB().QueryRow(`SELECT rc_left FROM cn35_rob_profiles WHERE user_id=?`, uid).Scan(&rc)
	if gp != 10000 || rc != cn35game.RobRules.MaxRC {
		t.Fatalf("gp=%d rc=%d", gp, rc)
	}
}
