// guild_rankings.go GC-06：会战月度排名读面（原服 /guild_rankings、
// /guild_rankings/my_guild）。
//
// 数据源：cn35_gw_sessions（按 date_key="YYYY-MM-DD" 做月度切片）×
// cn35_gw_participants（公会当场 GP）× cn35_guilds（名称/会长/会长领队图标）。
// point = 该月全部场次公会 GP 之和；rank 用竞争排名（1,2,2,4）。
//
// 证据：原服 2017-11 录包 artifacts/mlk-capture-201711/decrypted/1/guild_rankings.json
// 出现 rank 9/9/11 的并列序列（并列同名次、后续跳号），据此选竞争排名；
// 该月榜只保留前 5500 名档（同 item_rewards 8 档上限）。
package cn35store

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// gwRankingPageLimit 单次下发行数上限：原服客户端一次拉 100 行
// （GuildRankingListParts.cs 的 limit/offsetMax=100）。
const gwRankingPageLimit = 100

// gwRankingWindowBefore 本会历史页在本人名次之前的上下文行数〔推断〕：
// 原服样本窗口从名次 298 起、本会名次 307（前 9 名上下文），保留同屏可见。
const gwRankingWindowBefore = 9

// gwRankingMonthsCap 月份列表上限：原服样本 26 个月（2015-10…2017-11）。
const gwRankingMonthsCap = 26

// gwMonthPoint 月度榜原始聚合行（未排名）。
type gwMonthPoint struct {
	GuildID      string
	GuildName    string
	MasterName   string
	LeaderUnitID string
	Point        int64
}

// gwMonthPointRows 取某月全部参与公会的 GP 聚合（未排名）。
func (s *Store) gwMonthPointRows(yyyymm int) ([]gwMonthPoint, error) {
	prefix := yyyymmPrefix(yyyymm)
	if prefix == "" {
		return nil, nil
	}
	// 已月结时用冻结分数；公会解散也保留历史行，名称缺省为历史guild id。
	frozen, err := s.db.Query(`SELECT r.guild_id,COALESCE(g.name,r.guild_id),r.point FROM cn35_guild_season_ranks r LEFT JOIN cn35_guilds g ON g.guild_id=r.guild_id WHERE r.month=?`, prefix)
	if err != nil {
		return nil, err
	}
	var saved []gwMonthPoint
	for frozen.Next() {
		var row gwMonthPoint
		if err = frozen.Scan(&row.GuildID, &row.GuildName, &row.Point); err != nil {
			frozen.Close()
			return nil, err
		}
		saved = append(saved, row)
	}
	if err = frozen.Err(); err != nil {
		frozen.Close()
		return nil, err
	}
	if err = frozen.Close(); err != nil {
		return nil, err
	}
	if len(saved) > 0 {
		return saved, nil
	}
	rows, err := s.db.Query(`SELECT p.guild_id, g.name,
		COALESCE(
			(SELECT COALESCE(NULLIF(a.user_name,''),NULLIF(a.account_name,''),a.user_id)
			 FROM cn35_guild_members m JOIN cn35_accounts a ON a.user_id=m.user_id
			 WHERE m.guild_id=g.guild_id AND m.role=3),
			(SELECT i.name FROM cn35_guild_npc_members n
			 JOIN cn35_npc_identities i ON i.npc_id=n.npc_id
			 WHERE n.guild_id=g.guild_id AND n.role=3), '') AS master_name,
		COALESCE(
			(SELECT pf.leader_unit_id FROM cn35_guild_members m
			 JOIN cn35_player_profiles pf ON pf.user_id=m.user_id
			 WHERE m.guild_id=g.guild_id AND m.role=3),
			(SELECT i.leader_unit_id FROM cn35_guild_npc_members n
			 JOIN cn35_npc_identities i ON i.npc_id=n.npc_id
			 WHERE n.guild_id=g.guild_id AND n.role=3), '') AS leader_unit_id,
		SUM(p.gp) AS point
		FROM cn35_gw_participants p
		JOIN cn35_gw_sessions s ON s.session_id=p.session_id
		JOIN cn35_guilds g ON g.guild_id=p.guild_id
		WHERE substr(s.date_key,1,7)=?
		GROUP BY p.guild_id, g.name`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gwMonthPoint{}
	for rows.Next() {
		var r gwMonthPoint
		if err = rows.Scan(&r.GuildID, &r.GuildName, &r.MasterName, &r.LeaderUnitID, &r.Point); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// gwRankPoints 对月度聚合做竞争排名（并列同名次、后续跳号）。
func gwRankPoints(yyyymm int, raw []gwMonthPoint) []cn35game.GWRankingRow {
	sorted := make([]gwMonthPoint, len(raw))
	copy(sorted, raw)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Point != sorted[j].Point {
			return sorted[i].Point > sorted[j].Point
		}
		return sorted[i].GuildID < sorted[j].GuildID
	})
	out := make([]cn35game.GWRankingRow, 0, len(sorted))
	rank := 0
	for i := range sorted {
		if i == 0 || sorted[i].Point != sorted[i-1].Point {
			rank = i + 1
		}
		out = append(out, cn35game.GWRankingRow{
			YYYYMM:              yyyymm,
			GuildID:             sorted[i].GuildID,
			GuildName:           sorted[i].GuildName,
			MasterName:          sorted[i].MasterName,
			SquareIconAssetName: gwRankingSquareIcon(sorted[i].LeaderUnitID),
			Point:               sorted[i].Point,
			Rank:                rank,
		})
	}
	return out
}

// gwRankingSquareIcon 榜单行图标：会长领队方图标（原服 unit/unit_square_<id>）。
func gwRankingSquareIcon(leaderUnitID string) string {
	id := strings.TrimSpace(leaderUnitID)
	if id == "" {
		return ""
	}
	if _, err := strconv.Atoi(id); err != nil {
		return ""
	}
	return "unit/unit_square_" + id
}

// yyyymmPrefix yyyymm 整数 → 会话 date_key 前缀 "YYYY-MM"；非法返回空串。
func yyyymmPrefix(yyyymm int) string {
	if yyyymm < 100001 || yyyymm > 999912 {
		return ""
	}
	year, month := yyyymm/100, yyyymm%100
	if month < 1 || month > 12 {
		return ""
	}
	return strconv.Itoa(year) + "-" + pad2(month)
}

func pad2(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}

// GWMonthlyRanking 某月榜单分页（offset/limit）。limit<=0 用原服 100 行。
func (s *Store) GWMonthlyRanking(yyyymm, offset, limit int) ([]cn35game.GWRankingRow, error) {
	raw, err := s.gwMonthPointRows(yyyymm)
	if err != nil {
		return nil, err
	}
	ranked := gwRankPoints(yyyymm, raw)
	return gwRankingSlice(ranked, offset, limit), nil
}

// GWRankingMonths 有会战数据的月份列表（yyyymm 降序，最新在前）；
// 始终包含当前月（无数据时也给出单月入口，避免客户端月份按钮全空）。
func (s *Store) GWRankingMonths() ([]int, error) {
	rows, err := s.db.Query(`SELECT DISTINCT substr(date_key,1,7) FROM cn35_gw_sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[int]bool{}
	months := []int{}
	for rows.Next() {
		var prefix string
		if err = rows.Scan(&prefix); err != nil {
			return nil, err
		}
		ym := parseYYYYMMPrefix(prefix)
		if ym == 0 || seen[ym] {
			continue
		}
		seen[ym] = true
		months = append(months, ym)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// 当前月（原服月份列表总含当月；榜单页默认展示当月）。
	now := time.Now().In(guildLoc)
	cur := now.Year()*100 + int(now.Month())
	if !seen[cur] {
		months = append(months, cur)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(months)))
	if len(months) > gwRankingMonthsCap {
		months = months[:gwRankingMonthsCap]
	}
	return months, nil
}

// parseYYYYMMPrefix "YYYY-MM" → yyyymm；非法 0。
func parseYYYYMMPrefix(prefix string) int {
	trimmed := strings.TrimSpace(prefix)
	if len(trimmed) < 7 {
		return 0
	}
	year, err := strconv.Atoi(trimmed[:4])
	if err != nil {
		return 0
	}
	month, err := strconv.Atoi(trimmed[5:7])
	if err != nil || month < 1 || month > 12 {
		return 0
	}
	return year*100 + month
}

// GWRankingSelf 本会所在月榜窗口：从本会名次前 gwRankingWindowBefore 行起
// 取 limit 行；本会未上榜时退回该月榜首（页面仍有行可显示）。
func (s *Store) GWRankingSelf(guildID string, yyyymm, limit int) ([]cn35game.GWRankingRow, error) {
	raw, err := s.gwMonthPointRows(yyyymm)
	if err != nil {
		return nil, err
	}
	ranked := gwRankPoints(yyyymm, raw)
	if limit <= 0 {
		limit = gwRankingPageLimit
	}
	offset := 0
	for i := range ranked {
		if ranked[i].GuildID == guildID {
			offset = i - gwRankingWindowBefore
			if offset < 0 {
				offset = 0
			}
			break
		}
	}
	return gwRankingSlice(ranked, offset, limit), nil
}

// GWRankingPosition 本会在某月的名次与 GP；未上榜返回 (0,0,nil)。
func (s *Store) GWRankingPosition(guildID string, yyyymm int) (int, int64, error) {
	if guildID == "" {
		return 0, 0, nil
	}
	raw, err := s.gwMonthPointRows(yyyymm)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range gwRankPoints(yyyymm, raw) {
		if r.GuildID == guildID {
			return r.Rank, r.Point, nil
		}
	}
	return 0, 0, nil
}

// gwRankingSlice 安全切片。
func gwRankingSlice(rows []cn35game.GWRankingRow, offset, limit int) []cn35game.GWRankingRow {
	if limit <= 0 {
		limit = gwRankingPageLimit
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(rows) {
		return []cn35game.GWRankingRow{}
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end]
}
