// monster_ops.go P0 写面（2026-09-28 Note）：魔宠装备/卸下/保护位持久化。
//
// 双向引用不变式（同一事务内维护，0005 已有两列）：
//
//	cn35_player_monsters.unit_id   ↔ cn35_player_units.monster_uuid
//
// 任何时刻一对一：装到目标同伴时，若该魔宠已挂在其他同伴上先清原宿主
// monster_uuid；若目标同伴已有其他魔宠先把那只魔宠 unit_id 置 NULL。
// 「换装即替换」是服务端重建语义——客户端确认文案（COMMON_INFO_OVERWITE_CONF）
// 仅提示覆盖，原服逐位行为未证。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// 编译期断言：Store 满足魔宠写面契约。
var _ cn35game.MonsterOpsStore = (*Store)(nil)

// monsterHostTx 取魔宠当前宿主（unit_id NULL=未装备）；不存在归一为 ErrMonsterNotOwned。
func monsterHostTx(tx *sql.Tx, userID, uuid string) (sql.NullInt64, error) {
	var cur sql.NullInt64
	err := tx.QueryRow(`SELECT unit_id FROM cn35_player_monsters
		WHERE user_id=? AND uuid=?`, userID, uuid).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return cur, cn35game.ErrMonsterNotOwned
	}
	if err != nil {
		return cur, fmt.Errorf("cn35store: 魔宠归属校验失败: %w", err)
	}
	return cur, nil
}

// EquipMonster /user_monsters/on：uuid 与 unit_id 都必须属本账号。
// 同事务内：清魔宠原宿主 → 卸目标同伴原魔宠 → 写两侧引用。
func (s *Store) EquipMonster(userID, uuid string, unitID int) error {
	return s.withTx(func(tx *sql.Tx) error {
		if err := ownUnitTx(tx, userID, unitID); err != nil {
			return err
		}
		cur, err := monsterHostTx(tx, userID, uuid)
		if err != nil {
			return err
		}
		// 已装在其他同伴 → 清原宿主指向（同 unit 上是幂等换装，不需要清）。
		if cur.Valid && int(cur.Int64) != unitID {
			res, err := tx.Exec(`UPDATE cn35_player_units
				SET monster_uuid='',updated_at=?
				WHERE user_id=? AND unit_id=? AND monster_uuid=?`,
				nowUTC(), userID, int(cur.Int64), uuid)
			if err != nil {
				return fmt.Errorf("cn35store: 清除原宿主装备引用失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return fmt.Errorf("cn35store: 魔宠 %s 声称装在 unit %d 但宿主引用不一致", uuid, cur.Int64)
			}
		}
		// 目标同伴已有其他魔宠 → 那只魔宠卸下（双向清）。
		var curUUID string
		if err := tx.QueryRow(`SELECT monster_uuid FROM cn35_player_units
			WHERE user_id=? AND unit_id=?`, userID, unitID).Scan(&curUUID); err != nil {
			return fmt.Errorf("cn35store: 读取目标同伴装备位失败: %w", err)
		}
		if curUUID != "" && curUUID != uuid {
			res, err := tx.Exec(`UPDATE cn35_player_monsters
				SET unit_id=NULL,updated_at=?
				WHERE user_id=? AND uuid=? AND unit_id=?`,
				nowUTC(), userID, curUUID, unitID)
			if err != nil {
				return fmt.Errorf("cn35store: 卸下目标同伴原魔宠失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return fmt.Errorf("cn35store: 同伴 %d 声称装着魔宠 %s 但魔宠引用不一致", unitID, curUUID)
			}
		}
		res, err := tx.Exec(`UPDATE cn35_player_monsters
			SET unit_id=?,updated_at=? WHERE user_id=? AND uuid=?`,
			unitID, nowUTC(), userID, uuid)
		if err != nil {
			return fmt.Errorf("cn35store: 写入魔宠宿主失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrMonsterNotOwned
		}
		res, err = tx.Exec(`UPDATE cn35_player_units
			SET monster_uuid=?,updated_at=? WHERE user_id=? AND unit_id=?`,
			uuid, nowUTC(), userID, unitID)
		if err != nil {
			return fmt.Errorf("cn35store: 写入同伴装备位失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrUnitNotOwned
		}
		return nil
	})
}

// UnequipMonster /user_monsters/off：unitID<0 表示客户端未指定宿主
// （MonsterButtonsListComponent.cs:382 形态），按当前宿主卸下；
// unitID>=0 时必须与实际宿主一致（错报宿主明确拒绝）。
func (s *Store) UnequipMonster(userID, uuid string, unitID int) error {
	return s.withTx(func(tx *sql.Tx) error {
		cur, err := monsterHostTx(tx, userID, uuid)
		if err != nil {
			return err
		}
		if !cur.Valid {
			return cn35game.ErrMonsterNotEquipped
		}
		if unitID >= 0 && int(cur.Int64) != unitID {
			return cn35game.ErrMonsterHostMismatch
		}
		host := int(cur.Int64)
		res, err := tx.Exec(`UPDATE cn35_player_monsters
			SET unit_id=NULL,updated_at=? WHERE user_id=? AND uuid=? AND unit_id=?`,
			nowUTC(), userID, uuid, host)
		if err != nil {
			return fmt.Errorf("cn35store: 卸下魔宠失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("cn35store: 魔宠 %s 宿主并发漂移", uuid)
		}
		res, err = tx.Exec(`UPDATE cn35_player_units
			SET monster_uuid='',updated_at=? WHERE user_id=? AND unit_id=? AND monster_uuid=?`,
			nowUTC(), userID, host, uuid)
		if err != nil {
			return fmt.Errorf("cn35store: 清除同伴装备位失败: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("cn35store: 魔宠 %s 宿主引用不一致（unit %d 未回指）", uuid, host)
		}
		return nil
	})
}

// SetMonsterProtected /user_monsters/protect：逐 uuid 写 protected，
// 任一 uuid 不属本账号即整体回滚（不报部分成功）。
func (s *Store) SetMonsterProtected(userID string, uuids []string, protected bool) error {
	return s.withTx(func(tx *sql.Tx) error {
		for _, uuid := range uuids {
			res, err := tx.Exec(`UPDATE cn35_player_monsters
				SET protected=?,updated_at=? WHERE user_id=? AND uuid=?`,
				boolInt(protected), nowUTC(), userID, uuid)
			if err != nil {
				return fmt.Errorf("cn35store: 写入魔宠保护位失败: %w", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrMonsterNotOwned
			}
		}
		return nil
	})
}

func normalizeUUIDs(uuids []string) ([]string, error) {
	if len(uuids) == 0 {
		return nil, cn35game.ErrInvalidRequest
	}
	seen := make(map[string]bool, len(uuids))
	out := make([]string, 0, len(uuids))
	for _, u := range uuids {
		u = strings.TrimSpace(u)
		if u == "" {
			return nil, cn35game.ErrInvalidRequest
		}
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out, nil
}

// isMonsterInGuildDefenseTx 检查魔宠是否处于公会防卫槽。
func isMonsterInGuildDefenseTx(tx *sql.Tx, userID, uuid string) (bool, error) {
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM cn35_guild_defense WHERE user_id=? AND monster_uuid=?`,
		userID, uuid).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// SellMonsters 批量出售魔宠（/user_monsters/sell）。
func (s *Store) SellMonsters(userID string, uuids []string, prices map[string]int64) (int64, error) {
	list, err := normalizeUUIDs(uuids)
	if err != nil {
		return 0, err
	}
	var totalGold int64
	err = s.withTx(func(tx *sql.Tx) error {
		for _, uuid := range list {
			var unitID sql.NullInt64
			var prot int
			err := tx.QueryRow(`SELECT unit_id, protected FROM cn35_player_monsters WHERE user_id=? AND uuid=?`,
				userID, uuid).Scan(&unitID, &prot)
			if errors.Is(err, sql.ErrNoRows) {
				return cn35game.ErrMonsterNotOwned
			}
			if err != nil {
				return fmt.Errorf("cn35store: 读取出售魔宠失败: %w", err)
			}
			if unitID.Valid || prot != 0 {
				return cn35game.ErrMonsterSellProtected
			}
			inDef, derr := isMonsterInGuildDefenseTx(tx, userID, uuid)
			if derr != nil {
				return derr
			}
			if inDef {
				return cn35game.ErrMonsterSellProtected
			}
			delRes, err := tx.Exec(`DELETE FROM cn35_player_monsters WHERE user_id=? AND uuid=?`,
				userID, uuid)
			if err != nil {
				return fmt.Errorf("cn35store: 删除出售魔宠失败: %w", err)
			}
			if n, _ := delRes.RowsAffected(); n != 1 {
				return cn35game.ErrMonsterNotOwned
			}
			if p := prices[uuid]; p > 0 {
				totalGold += p
			}
		}
		if totalGold > 0 {
			if err := creditBalanceTx(tx, userID, "gacha_point", totalGold); err != nil {
				return fmt.Errorf("cn35store: 出售魔宠入账金币失败: %w", err)
			}
		}
		return nil
	})
	return totalGold, err
}

// EvolveEgg 提炼进化之卵（/user_monsters/evolve_egg）。
func (s *Store) EvolveEgg(userID string, attrID, consumeMonsterCount int, eligibleMonsterIDs map[int]bool) (int, []string, error) {
	if attrID < 1 || attrID > 5 {
		return 0, nil, cn35game.ErrMonsterEvoInvalid
	}
	var crystalReq, tier int
	var goldReq int64
	switch consumeMonsterCount {
	case 1:
		crystalReq, goldReq, tier = 1, 5000, 1
	case 3:
		crystalReq, goldReq, tier = 2, 15000, 2
	case 5:
		crystalReq, goldReq, tier = 3, 25000, 3
	case 10:
		crystalReq, goldReq, tier = 5, 50000, 4
	default:
		return 0, nil, cn35game.ErrMonsterEvoInvalid
	}
	eggItemID := 100*attrID + tier
	var consumed []string
	err := s.withTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT uuid, monster_id, scale, real_scale
			FROM cn35_player_monsters
			WHERE user_id=? AND unit_id IS NULL AND protected=0
			  AND NOT EXISTS (SELECT 1 FROM cn35_guild_defense d WHERE d.user_id=cn35_player_monsters.user_id AND d.monster_uuid=cn35_player_monsters.uuid)
			ORDER BY uuid`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		var candidates []string
		for rows.Next() {
			var u string
			var mid int
			var sc, rsc sql.NullString
			if err := rows.Scan(&u, &mid, &sc, &rsc); err != nil {
				return err
			}
			if !eligibleMonsterIDs[mid] {
				continue
			}
			scaleF, serr := parseScale(rsc, sc)
			if serr != nil || scaleF < 1.6995 {
				continue
			}
			candidates = append(candidates, u)
			if len(candidates) == consumeMonsterCount {
				break
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(candidates) < consumeMonsterCount {
			return cn35game.ErrMonsterEvoInvalid
		}
		if err := debitItemTx(tx, userID, "evolve_item_1", crystalReq); err != nil {
			return err
		}
		res, berr := tx.Exec(`UPDATE cn35_player_balances
			SET amount=amount-?, updated_at=?
			WHERE user_id=? AND currency='gacha_point' AND amount>=?`,
			goldReq, nowUTC(), userID, goldReq)
		if berr != nil {
			return berr
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrCurrencyInsufficient
		}
		for _, cu := range candidates {
			delRes, derr := tx.Exec(`DELETE FROM cn35_player_monsters WHERE user_id=? AND uuid=?`, userID, cu)
			if derr != nil {
				return derr
			}
			if n, _ := delRes.RowsAffected(); n != 1 {
				return cn35game.ErrMonsterNotOwned
			}
		}
		eggKey := fmt.Sprintf("evolve_fancy_item_%d", eggItemID)
		if err := creditItemTx(tx, userID, eggKey, 1); err != nil {
			return err
		}
		consumed = candidates
		return nil
	})
	return eggItemID, consumed, err
}

// EvolveMonster 魔宠融合进化（/user_monsters/evolve）。
func (s *Store) EvolveMonster(userID, mainUUID string, subUUIDs []string, eggItemID, targetMonsterID int, targetScaleLimit string, consumeGold int64, addFusionDegrees int) (cn35game.MonsterRecord, error) {
	var created cn35game.MonsterRecord
	mainUUID = strings.TrimSpace(mainUUID)
	if mainUUID == "" || eggItemID <= 0 || targetMonsterID <= 0 || targetScaleLimit == "" {
		return created, cn35game.ErrMonsterEvoInvalid
	}
	err := s.withTx(func(tx *sql.Tx) error {
		var mainUnitID sql.NullInt64
		var mainProt, mainFusion int
		var mainScale, mainRealScale sql.NullString
		withFusion := s.monsterFusionColumn()
		q := `SELECT unit_id, protected, scale, real_scale`
		if withFusion {
			q += `, fusion_degrees`
		}
		q += ` FROM cn35_player_monsters WHERE user_id=? AND uuid=?`
		dest := []any{&mainUnitID, &mainProt, &mainScale, &mainRealScale}
		if withFusion {
			dest = append(dest, &mainFusion)
		}
		err := tx.QueryRow(q, userID, mainUUID).Scan(dest...)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrMonsterNotOwned
		}
		if err != nil {
			return err
		}
		if mainUnitID.Valid || mainProt != 0 {
			return cn35game.ErrMonsterSellProtected
		}
		if inDef, derr := isMonsterInGuildDefenseTx(tx, userID, mainUUID); derr != nil {
			return derr
		} else if inDef {
			return cn35game.ErrMonsterSellProtected
		}

		seenSub := map[string]bool{mainUUID: true}
		for _, su := range subUUIDs {
			su = strings.TrimSpace(su)
			if su == "" || seenSub[su] {
				return cn35game.ErrMonsterEvoInvalid
			}
			seenSub[su] = true
			var subUnitID sql.NullInt64
			var subProt int
			var subSc, subRsc sql.NullString
			err := tx.QueryRow(`SELECT unit_id, protected, scale, real_scale FROM cn35_player_monsters WHERE user_id=? AND uuid=?`,
				userID, su).Scan(&subUnitID, &subProt, &subSc, &subRsc)
			if errors.Is(err, sql.ErrNoRows) {
				return cn35game.ErrMonsterNotOwned
			}
			if err != nil {
				return err
			}
			if subUnitID.Valid || subProt != 0 {
				return cn35game.ErrMonsterSellProtected
			}
			if inDef, derr := isMonsterInGuildDefenseTx(tx, userID, su); derr != nil {
				return derr
			} else if inDef {
				return cn35game.ErrMonsterSellProtected
			}
			scF, serr := parseScale(subRsc, subSc)
			if serr != nil || scF < 1.6995 {
				return cn35game.ErrMonsterEvoInvalid
			}
		}

		eggKey := fmt.Sprintf("evolve_fancy_item_%d", eggItemID)
		if err := debitItemTx(tx, userID, eggKey, 1); err != nil {
			return err
		}
		if consumeGold > 0 {
			res, berr := tx.Exec(`UPDATE cn35_player_balances
				SET amount=amount-?, updated_at=?
				WHERE user_id=? AND currency='gacha_point' AND amount>=?`,
				consumeGold, nowUTC(), userID, consumeGold)
			if berr != nil {
				return berr
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrCurrencyInsufficient
			}
		}

		allConsumed := append([]string{mainUUID}, subUUIDs...)
		for _, cu := range allConsumed {
			delRes, derr := tx.Exec(`DELETE FROM cn35_player_monsters WHERE user_id=? AND uuid=?`,
				userID, strings.TrimSpace(cu))
			if derr != nil {
				return derr
			}
			if n, _ := delRes.RowsAffected(); n != 1 {
				return cn35game.ErrMonsterNotOwned
			}
		}

		mainScaleF, _ := parseScale(mainRealScale, mainScale)
		initScaleF := 1.720
		if mainScaleF > initScaleF {
			initScaleF = mainScaleF
		}
		initScaleStr := formatScale(initScaleF)
		limitStr := targetScaleLimit
		newFusion := mainFusion + addFusionDegrees + len(subUUIDs)*2
		if newFusion > 100 {
			newFusion = 100
		}
		isRareVal := true
		newUUID := "m-evo-" + randHex(16)
		now := nowUTC()
		if withFusion {
			if _, err := tx.Exec(`INSERT INTO cn35_player_monsters
				(user_id, uuid, monster_id, unit_id, level, protected, scale, real_scale, scale_limit, is_rare, fusion_degrees, created_at, updated_at)
				VALUES (?, ?, ?, NULL, 1, 0, ?, ?, ?, 1, ?, ?, ?)`,
				userID, newUUID, targetMonsterID, initScaleStr, initScaleStr, limitStr, newFusion, now, now); err != nil {
				return fmt.Errorf("cn35store: 插入融合魔宠失败: %w", err)
			}
		} else {
			if _, err := tx.Exec(`INSERT INTO cn35_player_monsters
				(user_id, uuid, monster_id, unit_id, level, protected, scale, real_scale, scale_limit, is_rare, created_at, updated_at)
				VALUES (?, ?, ?, NULL, 1, 0, ?, ?, ?, 1, ?, ?)`,
				userID, newUUID, targetMonsterID, initScaleStr, initScaleStr, limitStr, now, now); err != nil {
				return fmt.Errorf("cn35store: 插入融合魔宠失败: %w", err)
			}
		}
		created = cn35game.MonsterRecord{
			UUID:          newUUID,
			MonsterID:     targetMonsterID,
			Level:         1,
			Protected:     false,
			Scale:         &initScaleStr,
			RealScale:     &initScaleStr,
			ScaleLimit:    &limitStr,
			IsRare:        &isRareVal,
			FusionDegrees: newFusion,
		}
		return nil
	})
	return created, err
}
