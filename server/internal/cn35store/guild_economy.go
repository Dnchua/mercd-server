package cn35store

// 单机重建策略，独立开关默认关闭；奖励额度来自录包展示资产，资格/时点非原服事实。
import (
	"database/sql"
	"sort"
	"time"
)

func (s *Store) ConfigureGuildEconomy(refill, season bool) error {
	return s.withTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE cn35_guild_economy_config SET refill_enabled=?,season_enabled=? WHERE id=1`, refill, season)
		return err
	})
}

func economyEnabledTx(tx *sql.Tx, column string) (bool, error) {
	var enabled bool
	// column 只能由本包常量调用，禁止请求参数进入 SQL。
	err := tx.QueryRow(`SELECT ` + column + ` FROM cn35_guild_economy_config WHERE id=1`).Scan(&enabled)
	if err != nil || !enabled {
		return false, err
	}
	solo, _, err := soloStateTx(tx)
	return solo.Enabled, err
}

func refillGuildTx(tx *sql.Tx, gid string, now time.Time) error {
	enabled, err := economyEnabledTx(tx, "refill_enabled")
	if err != nil || !enabled {
		return err
	}
	day := now.In(guildLoc).Format("2006-01-02")
	st, err := bpStorageStatusTx(tx, gid)
	if err != nil {
		return err
	}
	amount := st.Capacity - st.Amount
	if amount < 0 {
		amount = 0
	}
	r, err := tx.Exec(`INSERT INTO cn35_guild_refills(guild_id,day,amount) VALUES(?,?,?) ON CONFLICT DO NOTHING`, gid, day, amount)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	if amount == 0 {
		return nil
	}
	_, err = tx.Exec(`INSERT INTO cn35_guild_bp_storage(guild_id,amount,updated_at) VALUES(?,?,?) ON CONFLICT(guild_id) DO UPDATE SET amount=amount+excluded.amount,updated_at=excluded.updated_at`, gid, amount, nowUTC())
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO cn35_guild_bp_storage_log(guild_id,user_id,kind,amount,amount_after,op_key,reason,created_at) VALUES(?,'','deposit',?,?,?,?,?)`, gid, amount, st.Amount+amount, "daily-refill:"+gid+":"+day, "solo_daily_refill_v1", nowUTC())
	return err
}

type seasonMember struct {
	uid, gid string
	gp       int64
}

// settleGuildSeasonsTx 在关场后触发，整月冻结排名/真实贡献归属/发奖原子提交。
func settleGuildSeasonsTx(tx *sql.Tx, now time.Time) error {
	enabled, err := economyEnabledTx(tx, "season_enabled")
	if err != nil || !enabled {
		return err
	}
	cur := now.In(guildLoc).Format("2006-01")
	rows, err := tx.Query(`SELECT DISTINCT substr(date_key,1,7) FROM cn35_gw_sessions WHERE substr(date_key,1,7)<? AND status='settled' AND substr(date_key,1,7) NOT IN(SELECT month FROM cn35_guild_seasons) ORDER BY 1`, cur)
	if err != nil {
		return err
	}
	var months []string
	for rows.Next() {
		var m string
		if err = rows.Scan(&m); err != nil {
			rows.Close()
			return err
		}
		months = append(months, m)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, m := range months {
		if err = settleGuildMonthTx(tx, m); err != nil {
			return err
		}
	}
	return nil
}
func settleGuildMonthTx(tx *sql.Tx, m string) error {
	// 未支付完的跨关场战斗保留最终上报能力，月奖等它终结后再冻结。
	var pending int
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_gw_attacks a JOIN cn35_gw_sessions s ON s.session_id=a.session_id WHERE substr(s.date_key,1,7)=? AND a.status='bp_paid'`, m).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}
	rows, err := tx.Query(`SELECT p.guild_id,SUM(p.gp) FROM (SELECT session_id,guild_id,gp FROM cn35_gw_participants UNION ALL SELECT h.session_id,h.guild_id,h.gp FROM cn35_guild_season_history h WHERE NOT EXISTS(SELECT 1 FROM cn35_gw_participants p WHERE p.session_id=h.session_id AND p.guild_id=h.guild_id)) p JOIN cn35_gw_sessions s ON s.session_id=p.session_id WHERE substr(s.date_key,1,7)=? AND s.status='settled' GROUP BY p.guild_id`, m)
	if err != nil {
		return err
	}
	var points []gwMonthPoint
	for rows.Next() {
		var p gwMonthPoint
		if err = rows.Scan(&p.GuildID, &p.Point); err != nil {
			rows.Close()
			return err
		}
		points = append(points, p)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	ranks := map[string]int{}
	for _, r := range gwRankPoints(0, points) {
		ranks[r.GuildID] = r.Rank
		if _, err = tx.Exec(`INSERT INTO cn35_guild_season_ranks VALUES(?,?,?,?)`, m, r.GuildID, r.Point, r.Rank); err != nil {
			return err
		}
	}
	rows, err = tx.Query(`SELECT sc.user_id,sc.guild_id,SUM(sc.gp_earned) FROM cn35_gw_member_scores sc JOIN cn35_gw_sessions s ON s.session_id=sc.session_id JOIN cn35_accounts a ON a.user_id=sc.user_id WHERE substr(s.date_key,1,7)=? AND s.status='settled' GROUP BY sc.user_id,sc.guild_id`, m)
	if err != nil {
		return err
	}
	var members []seasonMember
	for rows.Next() {
		var x seasonMember
		if err = rows.Scan(&x.uid, &x.gid, &x.gp); err != nil {
			rows.Close()
			return err
		}
		members = append(members, x)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	sort.Slice(members, func(i, j int) bool {
		a, b := members[i], members[j]
		if a.uid != b.uid {
			return a.uid < b.uid
		}
		if a.gp != b.gp {
			return a.gp > b.gp
		}
		return a.gid < b.gid
	})
	seen := map[string]bool{}
	for _, x := range members {
		if seen[x.uid] {
			continue
		}
		seen[x.uid] = true
		rank := ranks[x.gid]
		if rank == 0 {
			continue
		}
		// 可发资产/符文按录包展示表投影；不解析未知 point 字段。
		reward, ok := guildSeasonReward(rank)
		if !ok {
			continue
		}
		uuid := ""
		if rank <= 250 {
			sk, e := grantSkillTx(tx, x.uid, 25, 30-float64(rank-1)/100)
			if e != nil {
				return e
			}
			uuid = sk.UUID
		}
		for _, b := range []struct {
			k string
			n int64
		}{{"gacha_point", reward.gold}, {"medal", reward.medal}, {"guild_asset", reward.asset}} {
			if err = creditBalanceTx(tx, x.uid, b.k, b.n); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`INSERT INTO cn35_guild_season_claims VALUES(?,?,?,?,?,?,?,?,?)`, m, x.uid, x.gid, rank, reward.gold, reward.medal, reward.asset, uuid, nowUTC()); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO cn35_guild_seasons VALUES(?,?)`, m, nowUTC())
	return err
}

type seasonReward struct{ gold, medal, asset int64 }

// 原服展示表8档的数值投影，来源 SHA 见资产；单机领取策略与展示分离。
func guildSeasonReward(rank int) (seasonReward, bool) {
	limits := []int{100, 500, 1000, 1500, 2000, 3000, 4000, 5500}
	gold := []int64{250000, 200000, 170000, 140000, 110000, 80000, 60000, 40000}
	if rank <= 0 {
		return seasonReward{}, false
	}
	for i, max := range limits {
		if rank <= max {
			return seasonReward{gold[i], gold[i] / 1000, gold[i]}, true
		}
	}
	return seasonReward{}, false
}
