package cn35store

import (
	"context"
	"encoding/json"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/database"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func econSetup(t *testing.T) (*Store, string, string) {
	t.Helper()
	s := openStore(t, t.TempDir())
	t.Cleanup(func() { s.Close() })
	uid := guildUID(t, s, "economy")
	g, e := s.CreateGuild(uid, "经济会", "1", "1", "", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ConfigureGuildSolo(DefaultGuildSoloSettings); e != nil {
		t.Fatal(e)
	}
	return s, uid, g.ID
}
func econTime(day int) time.Time { return time.Date(2026, 11, day, 12, 45, 0, 0, guildLoc) }
func TestGuildEconomyDailyRefill(t *testing.T) {
	s, u, g := econSetup(t)
	now := econTime(1)
	s.SetClock(func() time.Time { return now })
	if _, _, e := s.BPStorageStatus(u); e != nil {
		t.Fatal(e)
	}
	if n := storageAmountOf(t, s, g); n != 0 {
		t.Fatal("默认开关必须关闭", n)
	}
	if e := s.ConfigureGuildEconomy(true, false); e != nil {
		t.Fatal(e)
	}
	setBP(t, s, u, 0, 300)
	if _, _, e := s.BPStorageStatus(u); e != nil {
		t.Fatal(e)
	}
	capacity := cn35game.GuildBPStorageCapacity(1)
	if n := storageAmountOf(t, s, g); n != capacity {
		t.Fatal(n, capacity)
	}
	if _, e := s.TakeoverBPStorage(u, "first"); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.BPStorageStatus(u); e != nil {
		t.Fatal(e)
	}
	if n := storageAmountOf(t, s, g); n != capacity-300 {
		t.Fatal("同日重复补水", n)
	}
	now = now.AddDate(0, 0, 1)
	if _, _, e := s.BPStorageStatus(u); e != nil {
		t.Fatal(e)
	}
	if n := storageAmountOf(t, s, g); n != capacity {
		t.Fatal(n)
	}
	if e := s.ConfigureGuildSolo(GuildSoloSettings{}); e != nil {
		t.Fatal(e)
	}
	now = now.AddDate(0, 0, 1)
	seedBPStorage(t, s, g, 0)
	if _, _, e := s.BPStorageStatus(u); e != nil {
		t.Fatal(e)
	}
	if n := storageAmountOf(t, s, g); n != 0 {
		t.Fatal("solo关闭仍补水", n)
	}
}
func econSeedMonth(t *testing.T, s *Store, u, g string) {
	t.Helper()
	seedGWMonth(t, s, "econ-oct", "2026-10-29", 6, map[string]int64{g: 100})
	_, e := s.DB().Exec(`INSERT INTO cn35_gw_member_scores(session_id,user_id,guild_id,gp_earned,updated_at) VALUES('econ-oct',?,?,100,'test')`, u, g)
	if e != nil {
		t.Fatal(e)
	}
}
func TestGuildEconomySeasonAtomicReplayAndRestart(t *testing.T) {
	s, u, g := econSetup(t)
	econSeedMonth(t, s, u, g)
	if e := s.ConfigureGuildEconomy(false, true); e != nil {
		t.Fatal(e)
	}
	// 换会/解散不依赖当前成员表；已发生贡献是资格真源。
	if _, e := s.DB().Exec(`DELETE FROM cn35_guild_members WHERE user_id=?`, u); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB().Exec(`DELETE FROM cn35_guilds WHERE guild_id=?`, g); e != nil {
		t.Fatal(e)
	}
	now := econTime(1)
	if _, e := s.GWState(u, now); e != nil {
		t.Fatal(e)
	}
	history, e := s.GWMonthlyRanking(202610, 0, 100)
	if e != nil || len(history) != 1 || history[0].GuildID != g || history[0].Point != 100 {
		t.Fatal(history, e)
	}
	var rank int
	var uuid string
	if e := s.DB().QueryRow(`SELECT rank,skill_uuid FROM cn35_guild_season_claims WHERE month='2026-10' AND user_id=?`, u).Scan(&rank, &uuid); e != nil {
		t.Fatal(e)
	}
	if rank != 1 || uuid == "" {
		t.Fatal(rank, uuid)
	}
	var bal int64
	if e := s.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='guild_asset'`, u).Scan(&bal); e != nil || bal != 250000 {
		t.Fatal(bal, e)
	}
	if _, e := s.GWState(u, now); e != nil {
		t.Fatal(e)
	}
	// 新连接验证持久化，重放不得增加余额/符文。
	var dbPath string
	var seq int
	var name string
	if e := s.DB().QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &dbPath); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	other, e := Open(dbPath)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	if _, e = other.GWState(u, now); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = other.DB().QueryRow(`SELECT count(*) FROM cn35_player_skills WHERE user_id=?`, u).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if e = other.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='guild_asset'`, u).Scan(&bal); e != nil || bal != 250000 {
		t.Fatal(bal, e)
	}
}
func TestGuildEconomyRollback(t *testing.T) {
	s, u, g := econSetup(t)
	econSeedMonth(t, s, u, g)
	if e := s.ConfigureGuildEconomy(true, true); e != nil {
		t.Fatal(e)
	}
	_, e := s.DB().Exec(`CREATE TRIGGER fail_month BEFORE INSERT ON cn35_guild_season_claims BEGIN SELECT RAISE(ABORT,'test'); END`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.GWState(u, econTime(1)); e == nil {
		t.Fatal("应失败")
	}
	for _, table := range []string{"cn35_guild_season_claims", "cn35_guild_season_ranks", "cn35_guild_seasons", "cn35_player_skills"} {
		var n int
		if e = s.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
	var n int64
	if e = s.DB().QueryRow(`SELECT COALESCE((SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='guild_asset'),0)`, u).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	_, e = s.DB().Exec(`DROP TRIGGER fail_month`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.GWState(u, econTime(1)); e != nil {
		t.Fatal(e)
	}
}
func TestGuildEconomyRewardSource(t *testing.T) {
	raw, e := os.ReadFile(filepath.Join("..", "..", "assets", "cn350", "guild_rankings_item_rewards.json"))
	if e != nil {
		t.Fatal(e)
	}
	var doc struct {
		Rewards []map[string]string `json:"rewards"`
	}
	if e = json.Unmarshal(raw, &doc); e != nil {
		t.Fatal(e)
	}
	for _, row := range doc.Rewards {
		rank, _ := strconv.Atoi(strings.TrimSuffix(row["max_rank"], "位"))
		r, ok := guildSeasonReward(rank)
		if !ok {
			t.Fatal(rank)
		}
		for k, n := range map[string]int64{"gacha_point": r.gold, "medal": r.medal, "guild_asset": r.asset} {
			v, e := strconv.ParseInt(strings.ReplaceAll(row[k], ",", ""), 10, 64)
			if e != nil || v != n {
				t.Fatal(k, v, n, e)
			}
		}
	}
}
func TestGuildEconomyRefillRollback(t *testing.T) {
	s, u, g := econSetup(t)
	s.ConfigureGuildEconomy(true, false)
	if _, e := s.DB().Exec(`CREATE TRIGGER fail_refill BEFORE INSERT ON cn35_guild_bp_storage_log BEGIN SELECT RAISE(ABORT,'test'); END`); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.BPStorageStatus(u); e == nil {
		t.Fatal("应失败")
	}
	if n := storageAmountOf(t, s, g); n != 0 {
		t.Fatal(n)
	}
	var n int
	if e := s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_refills`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}

func TestGuildEconomySwitchGuildAndCrossMonth(t *testing.T) {
	s, u, g := econSetup(t)
	econSeedMonth(t, s, u, g)
	other := guildUID(t, s, "economy-other")
	g2, e := s.CreateGuild(other, "另一会", "1", "1", "", "")
	if e != nil {
		t.Fatal(e)
	}
	seedGWMonth(t, s, "econ-oct-2", "2026-10-30", 6, map[string]int64{g2.ID: 200})
	if _, e = s.DB().Exec(`INSERT INTO cn35_gw_member_scores(session_id,user_id,guild_id,gp_earned,updated_at) VALUES('econ-oct-2',?,?,50,'test')`, u, g2.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.ConfigureGuildEconomy(false, true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GWState(u, time.Date(2026, 10, 31, 23, 0, 0, 0, guildLoc)); e != nil {
		t.Fatal(e)
	}
	var n int
	s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_season_claims`).Scan(&n)
	if n != 0 {
		t.Fatal("当月不得发奖")
	}
	if _, e = s.GWState(u, econTime(1)); e != nil {
		t.Fatal(e)
	}
	var got string
	if e = s.DB().QueryRow(`SELECT guild_id FROM cn35_guild_season_claims WHERE user_id=? AND month='2026-10'`, u).Scan(&got); e != nil || got != g {
		t.Fatal(got, g, e)
	}
	s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_season_claims WHERE user_id=?`, u).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}

func TestGuildEconomyUpgradeAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.sqlite3")
	buildLegacyDBThrough(t, path, 41)
	old, e := database.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = old.Exec(`INSERT INTO cn35_guilds(guild_id,name,play_type_id,approval_type_id,recruit_text,memo,shifts,battle_setting_text,rating,rank_id,gp,created_at,updated_at) VALUES('legacy','旧会','1','1','','',127,'',0,'1',123,'old','old')`)
	if e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(dir, "backup.sqlite3")
	if e = database.Backup(context.Background(), old, backup); e != nil {
		t.Fatal(e)
	}
	old.Close()
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var gp int64
	if e = s.DB().QueryRow(`SELECT gp FROM cn35_guilds WHERE guild_id='legacy'`).Scan(&gp); e != nil || gp != 123 {
		t.Fatal(gp, e)
	}
	var enabled int
	if e = s.DB().QueryRow(`SELECT refill_enabled+season_enabled FROM cn35_guild_economy_config`).Scan(&enabled); e != nil || enabled != 0 {
		t.Fatal(enabled, e)
	}
	restored, e := database.Open(backup)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	var n int
	if e = restored.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); e != nil || n != 41 {
		t.Fatal(n, e)
	}
	if e = restored.QueryRow(`SELECT gp FROM cn35_guilds WHERE guild_id='legacy'`).Scan(&gp); e != nil || gp != 123 {
		t.Fatal(gp, e)
	}
}
