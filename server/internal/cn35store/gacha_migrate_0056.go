// gacha_migrate_0056.go 存量库一次性迁移（migrate-0056）：8 个官方样本
// 单位（unit_battle_behavior.json unit_overrides）的战斗行为数值在比
// unit_memorial 更高的优先级层下发，但修复前的准入谓词
// MemorialUnitConfigured 只查 unit_memorial，把它们误挡在卡池外。
// 本迁移把合格者置 enabled 并按稀有度归属补入草稿成员后重发版本。
package cn35store

import (
	"database/sql"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// AddGachaOfficialSampleUnits 执行 migrate-0056 存量库卡池补入（单事务）：
//
//	a. 对每个 id 读名册与模板（发布校验同一来源取 rare）；无模板、
//	   resource_ok=0、wiki=0、blocked=1 的跳过。
//	b. 合格者 roster enabled 置 1（updated_by=actor，notes=note；
//	   沿用 SetRosterFlags 的写法/审计口径）。
//	c. 对 poolsForRare(rare) 中、且在 cn35_gacha_pools 存在的池，若草稿
//	   尚无此 unit 则追加 weight=1 并写入草稿表。
//	d. 有任何新增成员：用草稿池定义 + 新草稿成员发布并移动指针；
//	   否则不发布（新库种子已含样本单位时安全成为 no-op）。
//	e. 无论是否改动都写 actor 标记（cn35_admin_audit action=migration.mark），
//	   GachaMigrationMarkers 据此保证只执行一次。
//
// 返回新发布版本（无新增成员时为 nil）与 published 标志。
func (s *Store) AddGachaOfficialSampleUnits(actor, note string, ids []int, poolsForRare func(rare int) []string) (*cn35game.GachaConfigVersion, bool, error) {
	var out *cn35game.GachaConfigVersion
	var added []int
	err := s.withTx(func(tx *sql.Tx) error {
		pools, err := s.gachaPoolsTx(tx)
		if err != nil {
			return err
		}
		poolExists := map[string]bool{}
		for _, p := range pools {
			poolExists[p.PoolID] = true
		}

		// a. 发布校验同口径筛选：名册证据位 + 模板 rare。
		var qualified []int
		rareByID := map[int]int{}
		for _, id := range ids {
			if id <= 0 {
				continue
			}
			ru, ok, err := scanRosterUnitOK(tx, id)
			if err != nil {
				return err
			}
			if !ok || !ru.ResourceOK || !ru.Wiki || ru.Blocked {
				continue
			}
			tpl, err := scanTemplate(tx.QueryRow(
				`SELECT `+templateCols+` FROM cn35_unit_templates WHERE unit_id=?`, id))
			if err != nil || tpl == nil {
				continue // 无模板：跳过
			}
			qualified = append(qualified, id)
			rareByID[id] = tpl.Rare
		}

		// b. 合格者置 enabled（同事务，发布校验可读）。
		if len(qualified) > 0 {
			yes := true
			if err := setRosterFlagsTx(tx, qualified, &yes, nil, note, actor); err != nil {
				return err
			}
		}

		// c. 草稿成员补入（池存在 + 尚无此 unit 才追加）。
		members, err := gachaDraftMembersTx(tx)
		if err != nil {
			return err
		}
		inDraft := map[string]map[int]bool{}
		for pid, ms := range members {
			inDraft[pid] = map[int]bool{}
			for _, m := range ms {
				inDraft[pid][m.UnitID] = true
			}
		}
		for _, id := range qualified {
			for _, pid := range poolsForRare(rareByID[id]) {
				if !poolExists[pid] || inDraft[pid][id] {
					continue
				}
				if inDraft[pid] == nil {
					inDraft[pid] = map[int]bool{}
				}
				inDraft[pid][id] = true
				members[pid] = append(members[pid],
					cn35game.GachaDraftMember{PoolID: pid, UnitID: id, Weight: 1})
				added = append(added, id)
				if _, err := tx.Exec(`INSERT INTO cn35_gacha_pool_draft_members
					(pool_id,unit_id,weight,updated_by,updated_at) VALUES (?,?,?,?,?)`,
					pid, id, 1, actor, nowUTC()); err != nil {
					return err
				}
			}
		}

		// d. 有新增才发布（校验+版本+指针+审计同事务，失败整包回滚）。
		if len(added) > 0 {
			v, err := s.publishGachaCommitTx(tx, pools, members, actor, note)
			if err != nil {
				return err
			}
			out = v
		}

		// e. 迁移标记：无论是否改动都落，保证只执行一次。
		return appendAuditTx(tx, actor, gachaMigrationMarkAction, "gacha", map[string]any{
			"note": note, "added": added, "published": out != nil,
		})
	})
	if err != nil {
		return nil, false, err
	}
	return out, out != nil, nil
}
