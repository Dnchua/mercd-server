// guild_rankings_test.go GC-06：月度会战排名读面（/guild_rankings、my_guild）。
//
// 覆盖：月份列表（含当月、降序、去重）、竞争排名（并列跳号）、分页 offset、
// 本会窗口、本会名次；数据源为 session×participant 的月度 GP 聚合。
package cn35store

import (
	"testing"
)

// seedGWMonth 直接落一场会战会话与参与者 GP（绕过时窗，专注聚合语义）。
func seedGWMonth(t *testing.T, s *Store, sessionID, dateKey string, fieldIndex int, points map[string]int64) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO cn35_gw_sessions
		(session_id,date_key,field_index,group_key,status,winner_guild_id,created_at)
		VALUES (?,?,?,?, 'settled','',?)`,
		sessionID, dateKey, fieldIndex, "g-"+sessionID, nowUTC()); err != nil {
		t.Fatal(err)
	}
	i := 0
	for guildID, gp := range points {
		i++
		if _, err := s.DB().Exec(`INSERT INTO cn35_gw_participants
			(session_id,guild_id,color,gp,rank_in_group,seed_powerup_rate)
			VALUES (?,?,?,?,?,0)`, sessionID, guildID, "ff4848", gp, i); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGWMonthlyRankingAggregatesAndRanks(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	u1, u2, u3 := guildUID(t, s, "r1"), guildUID(t, s, "r2"), guildUID(t, s, "r3")
	g1, err := s.CreateGuild(u1, "阿尔法", "1", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	g2, err := s.CreateGuild(u2, "贝塔", "1", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	g3, err := s.CreateGuild(u3, "伽马", "1", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-09：g1=100、g2=100（并列）、g3=50；2026-08：g1=10。
	seedGWMonth(t, s, "s-2609-a", "2026-09-02", 6, map[string]int64{g1.ID: 60, g2.ID: 100, g3.ID: 50})
	seedGWMonth(t, s, "s-2609-b", "2026-09-03", 6, map[string]int64{g1.ID: 40, g2.ID: 0, g3.ID: 0})
	seedGWMonth(t, s, "s-2608-a", "2026-08-05", 6, map[string]int64{g1.ID: 10})

	months, err := s.GWRankingMonths()
	if err != nil {
		t.Fatalf("GWRankingMonths: %v", err)
	}
	if len(months) < 2 || months[0] < months[1] {
		t.Fatalf("月份列表应降序且包含历史月: %v", months)
	}
	seen := map[int]bool{}
	for _, m := range months {
		if m == 202609 || m == 202608 {
			seen[m] = true
		}
	}
	if !seen[202609] || !seen[202608] {
		t.Fatalf("月份列表缺 202609/202608: %v", months)
	}

	rows, err := s.GWMonthlyRanking(202609, 0, 100)
	if err != nil {
		t.Fatalf("GWMonthlyRanking: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("202609 应 3 行: %+v", rows)
	}
	// 竞争排名：g1/g2 同为 100 → rank 1 并列，g3 50 → rank 3（跳号）。
	byID := map[string]int{}
	points := map[string]int64{}
	for _, r := range rows {
		byID[r.GuildID] = r.Rank
		points[r.GuildID] = r.Point
	}
	if points[g1.ID] != 100 || points[g2.ID] != 100 || points[g3.ID] != 50 {
		t.Fatalf("月度 GP 聚合错: %+v", points)
	}
	if byID[g1.ID] != 1 || byID[g2.ID] != 1 || byID[g3.ID] != 3 {
		t.Fatalf("竞争排名应为 1/1/3: %+v", byID)
	}
	if rows[0].MasterName == "" {
		t.Fatal("榜单行应带会长名")
	}
	if rows[0].YYYYMM != 202609 {
		t.Fatalf("行 yyyymm 错: %+v", rows[0])
	}

	// 分页：offset=1 取后两行。
	page, err := s.GWMonthlyRanking(202609, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 {
		t.Fatalf("offset 分页应 2 行: %+v", page)
	}

	// 本会窗口：以 g3（rank 3）为窗口锚点仍返回全部 3 行（不足 100）。
	self, err := s.GWRankingSelf(g3.ID, 202609, 100)
	if err != nil {
		t.Fatalf("GWRankingSelf: %v", err)
	}
	if len(self) == 0 {
		t.Fatal("本会窗口不应为空")
	}
	found := false
	for _, r := range self {
		if r.GuildID == g3.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("本会窗口应包含本会: %+v", self)
	}

	// 本会名次：跨月隔离。
	rank, point, err := s.GWRankingPosition(g1.ID, 202608)
	if err != nil {
		t.Fatalf("GWRankingPosition: %v", err)
	}
	if rank != 1 || point != 10 {
		t.Fatalf("202608 本会名次/GP 错: %d/%d", rank, point)
	}
	// 未上榜：0/0。
	if rank, point, err = s.GWRankingPosition(g2.ID, 202607); err != nil || rank != 0 || point != 0 {
		t.Fatalf("未上榜应为 0/0: %d/%d %v", rank, point, err)
	}
	// 空月份不返回行（不伪造）。
	if rows, err = s.GWMonthlyRanking(202607, 0, 100); err != nil || len(rows) != 0 {
		t.Fatalf("空月应 0 行: %+v %v", rows, err)
	}
}

func TestGWMonthlyRankingEmptyDatabase(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	months, err := s.GWRankingMonths()
	if err != nil {
		t.Fatalf("GWRankingMonths: %v", err)
	}
	if len(months) != 1 {
		t.Fatalf("无会战数据时月份列表应只含当前月: %v", months)
	}
	rows, err := s.GWMonthlyRanking(months[0], 0, 100)
	if err != nil || len(rows) != 0 {
		t.Fatalf("空库应 0 行: %+v %v", rows, err)
	}
}
