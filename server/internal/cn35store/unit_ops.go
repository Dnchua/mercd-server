// unit_ops.go P0 写面（2026-09-28 Note）：同伴队长/助战/保护位持久化 +
// M5 同伴出售/进化/能力互换持久化。
//
// 事务约定（与 gacha.go 相同）：所有校验与写入在单个 withTx 内完成；
// 归属判定一律以 (user_id, unit_id) 库存行存在为准，跨账号请求与未持有
// 归一为 cn35game.ErrUnitNotOwned——不区分"单位不存在"与"他人持有"。
//
// leader/helper 两字段独立写入（客户端取证）：UserUnitDetail.cs:346-360
// 对 leader_unit_id 与 helper_unit_id 各自独立比较渲染，无互斥证据——
// 同一 unit_id 可同时占据两槽位。helper 空位编码为 ”（0016 允许、
// 空则不下发该键）；leader 空位不存在于正常流程，0003 的 CHECK 要求
// leader_unit_id 非空。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// 编译期断言：Store 满足同伴写面契约。
var _ cn35game.UnitOpsStore = (*Store)(nil)

// ownUnitTx 校验会话账号持有该 unit_id 模板行且处于同行状态（防跨账号或对
// 待机所单位设置队长/助战/装备魔宠）。
func ownUnitTx(tx *sql.Tx, userID string, unitID int) error {
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_player_units
		WHERE user_id=? AND unit_id=? AND is_waiting=0`, userID, unitID).Scan(&n); err != nil {
		return fmt.Errorf("cn35store: 单位归属校验失败: %w", err)
	}
	if n != 1 {
		return cn35game.ErrUnitNotOwned
	}
	return nil
}

// SetLeaderUnitID 写 cn35_player_profiles.leader_unit_id（与 helper 独立，
// 不清对面槽位）。
func (s *Store) SetLeaderUnitID(userID string, unitID int) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := ownUnitTx(tx, userID, unitID); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE cn35_player_profiles
			SET leader_unit_id=?,updated_at=?
			WHERE user_id=?`, strconv.Itoa(unitID), nowUTC(), userID)
		if err != nil {
			return fmt.Errorf("cn35store: 写入 leader_unit_id 失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("cn35store: leader_unit_id 写入未命中账号 user_id=%s", userID)
		}
		return nil
	})
}

// SetHelperUnitID 写 cn35_player_profiles.helper_unit_id（0016 增列，
// 与 leader 独立，不清对面槽位）。
func (s *Store) SetHelperUnitID(userID string, unitID int) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := ownUnitTx(tx, userID, unitID); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE cn35_player_profiles
			SET helper_unit_id=?,updated_at=?
			WHERE user_id=?`, strconv.Itoa(unitID), nowUTC(), userID)
		if err != nil {
			return fmt.Errorf("cn35store: 写入 helper_unit_id 失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("cn35store: helper_unit_id 写入未命中账号 user_id=%s", userID)
		}
		return nil
	})
}

// SetUnitProtected 写 cn35_player_units.protected（归属条件在 UPDATE 内：
// 未持有或处于待机所即 0 行命中 → ErrUnitNotOwned）。
func (s *Store) SetUnitProtected(userID string, unitID int, protected bool) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE cn35_player_units
			SET protected=?,updated_at=? WHERE user_id=? AND unit_id=? AND is_waiting=0`,
			boolInt(protected), nowUTC(), userID, unitID)
		if err != nil {
			return fmt.Errorf("cn35store: 写入单位保护位失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrUnitNotOwned
		}
		return nil
	})
}

// normalizeUnitIDs 去重并校验正整数 unitID 列表。
func normalizeUnitIDs(unitIDs []int) ([]int, error) {
	if len(unitIDs) == 0 {
		return nil, cn35game.ErrInvalidRequest
	}
	seen := make(map[int]bool, len(unitIDs))
	out := make([]int, 0, len(unitIDs))
	for _, id := range unitIDs {
		if id <= 0 {
			return nil, cn35game.ErrInvalidRequest
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// WaitUnits 批量将同行同伴移入待机所（/albums/wait）：
// 单事务校验非队长/助战、归属且当前同行，自动卸下符文/魔宠并移出编队。
func (s *Store) WaitUnits(userID string, unitIDs []int) error {
	ids, err := normalizeUnitIDs(unitIDs)
	if err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error {
		var leaderRaw, helperRaw string
		if err := tx.QueryRow(`SELECT leader_unit_id, helper_unit_id FROM cn35_player_profiles WHERE user_id=?`,
			userID).Scan(&leaderRaw, &helperRaw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return cn35game.ErrNoAccount
			}
			return fmt.Errorf("cn35store: 读取玩家队长/助战失败: %w", err)
		}
		leaderID, _ := strconv.Atoi(leaderRaw)
		helperID, _ := strconv.Atoi(helperRaw)
		now := nowUTC()
		deckTouched := false
		for _, id := range ids {
			var isWait int
			err := tx.QueryRow(`SELECT is_waiting FROM cn35_player_units
				WHERE user_id=? AND unit_id=?`, userID, id).Scan(&isWait)
			if errors.Is(err, sql.ErrNoRows) {
				return cn35game.ErrUnitNotOwned
			}
			if err != nil {
				return fmt.Errorf("cn35store: 读取单位状态失败: %w", err)
			}
			if isWait != 0 {
				continue
			}
			if (leaderID > 0 && id == leaderID) || (helperID > 0 && id == helperID) {
				return cn35game.ErrUnitRoleProtected
			}
			if _, err := tx.Exec(`UPDATE cn35_player_decks SET starter_backfill_disabled=1 WHERE user_id=? AND party_id='1' AND EXISTS (SELECT 1 FROM cn35_player_deck_slots WHERE user_id=? AND party_id='1' AND unit_id=?)`, userID, userID, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE cn35_player_skills
				SET unit_id=NULL, updated_at=? WHERE user_id=? AND unit_id=?`,
				now, userID, id); err != nil {
				return fmt.Errorf("cn35store: 待机卸下符文失败: %w", err)
			}
			if _, err := tx.Exec(`UPDATE cn35_player_monsters
				SET unit_id=NULL, updated_at=? WHERE user_id=? AND unit_id=?`,
				now, userID, id); err != nil {
				return fmt.Errorf("cn35store: 待机卸下魔宠失败: %w", err)
			}
			deckRes, err := tx.Exec(`UPDATE cn35_player_deck_slots
				SET unit_id=NULL WHERE user_id=? AND unit_id=?`,
				userID, id)
			if err != nil {
				return fmt.Errorf("cn35store: 待机清理编队槽位失败: %w", err)
			}
			if n, _ := deckRes.RowsAffected(); n > 0 {
				deckTouched = true
			}
			res, err := tx.Exec(`UPDATE cn35_player_units
				SET is_waiting=1, protected=0, newcomer=0, monster_uuid='', updated_at=?
				WHERE user_id=? AND unit_id=? AND is_waiting=0`,
				now, userID, id)
			if err != nil {
				return fmt.Errorf("cn35store: 写入单位待机状态失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrUnitAlreadyWaiting
			}
		}
		if deckTouched {
			if _, err := tx.Exec(`UPDATE cn35_player_decks SET updated_at=? WHERE user_id=?`,
				now, userID); err != nil {
				return fmt.Errorf("cn35store: 更新编队时间戳失败: %w", err)
			}
		}
		return nil
	})
}

// BackUnits 批量将待机所同伴召回同行列表（/albums/back）。
func (s *Store) BackUnits(userID string, unitIDs []int) error {
	ids, err := normalizeUnitIDs(unitIDs)
	if err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error {
		now := nowUTC()
		for _, id := range ids {
			var isWait int
			err := tx.QueryRow(`SELECT is_waiting FROM cn35_player_units
				WHERE user_id=? AND unit_id=?`, userID, id).Scan(&isWait)
			if errors.Is(err, sql.ErrNoRows) {
				return cn35game.ErrUnitNotOwned
			}
			if err != nil {
				return fmt.Errorf("cn35store: 读取单位状态失败: %w", err)
			}
			if isWait == 0 {
				continue
			}
			res, err := tx.Exec(`UPDATE cn35_player_units
				SET is_waiting=0, newcomer=0, updated_at=?
				WHERE user_id=? AND unit_id=? AND is_waiting=1`,
				now, userID, id)
			if err != nil {
				return fmt.Errorf("cn35store: 恢复单位同行状态失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrUnitNotWaiting
			}
		}
		return nil
	})
}

// SellUnits 批量出售同行同伴（/user_units/sell）。
// 同事务校验：
//  1. 归属且处于同行状态（is_waiting=0）；
//  2. 未锁定（protected=0）且非队长/助战（leader_unit_id / helper_unit_id）；
//  3. 出售后同行同伴数量 >= 5（UnitButtonsListComponent.cs:646 最低保留 5 名）；
//  4. 自动卸下符文与魔宠、清理编队槽位（并标记 starter_backfill_disabled）、
//     删除同伴行，按 prices 累加金币写入 cn35_player_balances (gacha_point)。
func (s *Store) SellUnits(userID string, unitIDs []int, prices map[int]int64) (int64, error) {
	ids, err := normalizeUnitIDs(unitIDs)
	if err != nil {
		return 0, err
	}
	var totalGold int64
	err = s.withTx(func(tx *sql.Tx) error {
		var leaderRaw, helperRaw string
		if err := tx.QueryRow(`SELECT leader_unit_id, helper_unit_id FROM cn35_player_profiles WHERE user_id=?`,
			userID).Scan(&leaderRaw, &helperRaw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return cn35game.ErrNoAccount
			}
			return fmt.Errorf("cn35store: 读取玩家队长/助战失败: %w", err)
		}
		leaderID, _ := strconv.Atoi(leaderRaw)
		helperID, _ := strconv.Atoi(helperRaw)

		var activeCount int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_player_units WHERE user_id=? AND is_waiting=0`,
			userID).Scan(&activeCount); err != nil {
			return fmt.Errorf("cn35store: 统计同行同伴数失败: %w", err)
		}
		if activeCount-len(ids) < 5 {
			return cn35game.ErrUnitSellMinKeep
		}

		now := nowUTC()
		deckTouched := false
		for _, id := range ids {
			var isWait, prot int
			err := tx.QueryRow(`SELECT is_waiting, protected FROM cn35_player_units
				WHERE user_id=? AND unit_id=?`, userID, id).Scan(&isWait, &prot)
			if errors.Is(err, sql.ErrNoRows) {
				return cn35game.ErrUnitNotOwned
			}
			if err != nil {
				return fmt.Errorf("cn35store: 读取单位状态失败: %w", err)
			}
			if isWait != 0 {
				return cn35game.ErrUnitNotOwned
			}
			if prot != 0 || (leaderID > 0 && id == leaderID) || (helperID > 0 && id == helperID) {
				return cn35game.ErrUnitSellProtected
			}
			if _, err := tx.Exec(`UPDATE cn35_player_decks SET starter_backfill_disabled=1 WHERE user_id=? AND party_id='1' AND EXISTS (SELECT 1 FROM cn35_player_deck_slots WHERE user_id=? AND party_id='1' AND unit_id=?)`,
				userID, userID, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE cn35_player_skills SET unit_id=NULL, updated_at=? WHERE user_id=? AND unit_id=?`,
				now, userID, id); err != nil {
				return fmt.Errorf("cn35store: 出售同伴卸下符文失败: %w", err)
			}
			if _, err := tx.Exec(`UPDATE cn35_player_monsters SET unit_id=NULL, updated_at=? WHERE user_id=? AND unit_id=?`,
				now, userID, id); err != nil {
				return fmt.Errorf("cn35store: 出售同伴卸下魔宠失败: %w", err)
			}
			deckRes, err := tx.Exec(`UPDATE cn35_player_deck_slots SET unit_id=NULL WHERE user_id=? AND unit_id=?`,
				userID, id)
			if err != nil {
				return fmt.Errorf("cn35store: 出售同伴清理编队槽位失败: %w", err)
			}
			if n, _ := deckRes.RowsAffected(); n > 0 {
				deckTouched = true
			}
			delRes, err := tx.Exec(`DELETE FROM cn35_player_units WHERE user_id=? AND unit_id=? AND is_waiting=0`,
				userID, id)
			if err != nil {
				return fmt.Errorf("cn35store: 删除出售同伴失败: %w", err)
			}
			if n, _ := delRes.RowsAffected(); n != 1 {
				return cn35game.ErrUnitNotOwned
			}
			if p := prices[id]; p > 0 {
				totalGold += p
			}
		}
		if deckTouched {
			if _, err := tx.Exec(`UPDATE cn35_player_decks SET updated_at=? WHERE user_id=?`,
				now, userID); err != nil {
				return fmt.Errorf("cn35store: 更新编队时间戳失败: %w", err)
			}
		}
		if totalGold > 0 {
			if err := creditBalanceTx(tx, userID, "gacha_point", totalGold); err != nil {
				return fmt.Errorf("cn35store: 出售同伴金币入账失败: %w", err)
			}
		}
		return nil
	})
	return totalGold, err
}

// EvolveUnit 同伴进化（/user_units/evolution）。
func (s *Store) EvolveUnit(userID string, fromUnitID, toUnitID, reqBaseMaxLv int, needItems map[string]int) (cn35game.UnitRecord, cn35game.UnitRecord, error) {
	var beforeRec, afterRec cn35game.UnitRecord
	if fromUnitID <= 0 || toUnitID <= 0 || fromUnitID == toUnitID || reqBaseMaxLv <= 0 {
		return beforeRec, afterRec, cn35game.ErrUnitEvolutionInvalid
	}
	err := s.withTx(func(tx *sql.Tx) error {
		var lv, dup, prot, newcom, isWait int
		var mUUID string
		err := tx.QueryRow(`SELECT level, duplicated_count, protected, newcomer, monster_uuid, is_waiting
			FROM cn35_player_units WHERE user_id=? AND unit_id=?`, userID, fromUnitID).
			Scan(&lv, &dup, &prot, &newcom, &mUUID, &isWait)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrUnitNotOwned
		}
		if err != nil {
			return fmt.Errorf("cn35store: 读取进化前同伴失败: %w", err)
		}
		if isWait != 0 {
			return cn35game.ErrUnitNotOwned
		}
		if lv < reqBaseMaxLv {
			return cn35game.ErrUnitEvolutionInvalid
		}
		var targetCount int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_player_units WHERE user_id=? AND unit_id=?`,
			userID, toUnitID).Scan(&targetCount); err != nil {
			return err
		}
		if targetCount > 0 {
			return cn35game.ErrUnitEvolutionOwned
		}
		for cidStr, reqQty := range needItems {
			if reqQty <= 0 {
				continue
			}
			cid, cerr := strconv.Atoi(cidStr)
			if cerr != nil || cid <= 0 {
				return cn35game.ErrInvalidRequest
			}
			itemKey, kerr := itemKeyByContentTx(tx, cid)
			if kerr != nil {
				return kerr
			}
			if derr := debitItemTx(tx, userID, itemKey, reqQty); derr != nil {
				return derr
			}
		}

		newDup := dup / 2
		now := nowUTC()
		beforeRec = cn35game.UnitRecord{
			UnitID:          fromUnitID,
			Level:           lv,
			DuplicatedCount: dup,
			Protected:       prot != 0,
			Newcomer:        newcom != 0,
			MonsterUUID:     mUUID,
			Waiting:         false,
		}
		afterRec = cn35game.UnitRecord{
			UnitID:          toUnitID,
			Level:           1,
			DuplicatedCount: newDup,
			Protected:       prot != 0,
			Newcomer:        false,
			MonsterUUID:     mUUID,
			Waiting:         false,
		}
		// 先插入进化后同伴行，再迁移编队/魔宠/符文/队长/助战引用，最后删除原同伴行，满足外键约束。
		if _, err := tx.Exec(`INSERT INTO cn35_player_units
			(user_id, unit_id, level, duplicated_count, protected, newcomer, monster_uuid, is_waiting, created_at, updated_at)
			VALUES (?, ?, 1, ?, ?, 0, ?, 0, ?, ?)`,
			userID, toUnitID, newDup, prot, mUUID, now, now); err != nil {
			return fmt.Errorf("cn35store: 插入进化后同伴失败: %w", err)
		}
		if _, err := tx.Exec(`UPDATE cn35_player_deck_slots SET unit_id=? WHERE user_id=? AND unit_id=?`,
			toUnitID, userID, fromUnitID); err != nil {
			return fmt.Errorf("cn35store: 进化迁移编队槽位失败: %w", err)
		}
		if _, err := tx.Exec(`UPDATE cn35_player_monsters SET unit_id=?, updated_at=? WHERE user_id=? AND unit_id=?`,
			toUnitID, now, userID, fromUnitID); err != nil {
			return fmt.Errorf("cn35store: 进化迁移魔宠宿主失败: %w", err)
		}
		if _, err := tx.Exec(`UPDATE cn35_player_skills SET unit_id=?, updated_at=? WHERE user_id=? AND unit_id=?`,
			toUnitID, now, userID, fromUnitID); err != nil {
			return fmt.Errorf("cn35store: 进化迁移符文宿主失败: %w", err)
		}
		fromStr := strconv.Itoa(fromUnitID)
		toStr := strconv.Itoa(toUnitID)
		if _, err := tx.Exec(`UPDATE cn35_player_profiles SET leader_unit_id=?, updated_at=? WHERE user_id=? AND leader_unit_id=?`,
			toStr, now, userID, fromStr); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE cn35_player_profiles SET helper_unit_id=?, updated_at=? WHERE user_id=? AND helper_unit_id=?`,
			toStr, now, userID, fromStr); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM cn35_player_units WHERE user_id=? AND unit_id=?`,
			userID, fromUnitID); err != nil {
			return fmt.Errorf("cn35store: 删除进化前同伴失败: %w", err)
		}
		_, _ = tx.Exec(`INSERT OR IGNORE INTO cn35_inventory_grants (user_id, grant_key, unit_id, created_at)
			VALUES (?, ?, ?, ?)`, userID, fmt.Sprintf("evo-%d-%d", fromUnitID, toUnitID), toUnitID, now)
		return nil
	})
	return beforeRec, afterRec, err
}

// ExchangeUnitAbility 同伴能力互换（/unit_ability_exchange/exchange_unit_ability）。
func (s *Store) ExchangeUnitAbility(userID string, unitAID, unitBID int) (cn35game.UnitRecord, cn35game.UnitRecord, error) {
	var recA, recB cn35game.UnitRecord
	if unitAID <= 0 || unitBID <= 0 || unitAID == unitBID {
		return recA, recB, cn35game.ErrUnitExchangeInvalid
	}
	err := s.withTx(func(tx *sql.Tx) error {
		loadActive := func(uid int) (cn35game.UnitRecord, error) {
			var r cn35game.UnitRecord
			var prot, newcom, isWait int
			err := tx.QueryRow(`SELECT unit_id, level, duplicated_count, protected, newcomer, monster_uuid, is_waiting
				FROM cn35_player_units WHERE user_id=? AND unit_id=?`, userID, uid).
				Scan(&r.UnitID, &r.Level, &r.DuplicatedCount, &prot, &newcom, &r.MonsterUUID, &isWait)
			if errors.Is(err, sql.ErrNoRows) {
				return r, cn35game.ErrUnitNotOwned
			}
			if err != nil {
				return r, err
			}
			if isWait != 0 {
				return r, cn35game.ErrUnitNotOwned
			}
			r.Protected = prot != 0
			r.Newcomer = newcom != 0
			return r, nil
		}
		var err error
		recA, err = loadActive(unitAID)
		if err != nil {
			return err
		}
		recB, err = loadActive(unitBID)
		if err != nil {
			return err
		}
		if recA.Level == recB.Level && recA.DuplicatedCount == recB.DuplicatedCount {
			return cn35game.ErrUnitExchangeInvalid
		}
		// 优先消耗 1 颗能力互换石（power_stone），若无则消耗 10 钻（coin）。
		if derr := debitItemTx(tx, userID, "power_stone", 1); derr != nil {
			if !errors.Is(derr, cn35game.ErrItemInsufficient) {
				return derr
			}
			res, berr := tx.Exec(`UPDATE cn35_player_balances
				SET amount=amount-10, updated_at=?
				WHERE user_id=? AND currency='coin' AND amount>=10`,
				nowUTC(), userID)
			if berr != nil {
				return berr
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrCurrencyInsufficient
			}
		}
		now := nowUTC()
		recA.Level, recB.Level = recB.Level, recA.Level
		recA.DuplicatedCount, recB.DuplicatedCount = recB.DuplicatedCount, recA.DuplicatedCount
		if _, err := tx.Exec(`UPDATE cn35_player_units SET level=?, duplicated_count=?, updated_at=? WHERE user_id=? AND unit_id=?`,
			recA.Level, recA.DuplicatedCount, now, userID, unitAID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE cn35_player_units SET level=?, duplicated_count=?, updated_at=? WHERE user_id=? AND unit_id=?`,
			recB.Level, recB.DuplicatedCount, now, userID, unitBID); err != nil {
			return err
		}
		return nil
	})
	return recA, recB, err
}
