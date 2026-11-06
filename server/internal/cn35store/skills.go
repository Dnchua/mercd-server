// skills.go RUNE350-01：符文库存读面与写面（装备/卸下/出售/保护/
// 物品强化/符文合成）的 SQLite 事务实现。
//
// 事务约定与 items.go/monster_powerup.go 一致：方法即事务边界，账号
// 隔离全靠 WHERE user_id 绑定；出售入账走 cn35_player_balances
// gacha_point 条件/无条件的同事务写入（客户端 sell 确认弹窗与入账
// 均按 gacha_point——SkillButtonsListComponent.cs:666-683）。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var _ cn35game.SkillStore = (*Store)(nil)

// skillCols 库存行投影列（与 scanSkillRow/PlayerSkill 对齐）。
const skillCols = `uuid,skill_id,level,base_level,protected,unit_id`

func scanSkillRow(row *sql.Rows) (cn35game.PlayerSkill, error) {
	var sk cn35game.PlayerSkill
	var unitID sql.NullInt64
	var prot int
	if err := row.Scan(&sk.UUID, &sk.SkillID, &sk.Level, &sk.BaseLevel,
		&prot, &unitID); err != nil {
		return sk, err
	}
	sk.Protected = prot == 1
	if unitID.Valid {
		id := int(unitID.Int64)
		sk.UnitID = &id
	}
	return sk, nil
}

func playerSkillsTx(tx *sql.Tx, userID string) ([]cn35game.PlayerSkill, error) {
	rows, err := tx.Query(`SELECT `+skillCols+`
		FROM cn35_player_skills WHERE user_id=? ORDER BY skill_id,uuid`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.PlayerSkill{}
	for rows.Next() {
		sk, err := scanSkillRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

func loadSkillTx(tx *sql.Tx, userID, uuid string) (*cn35game.PlayerSkill, error) {
	var sk cn35game.PlayerSkill
	var unitID sql.NullInt64
	var prot int
	err := tx.QueryRow(`SELECT `+skillCols+`
		FROM cn35_player_skills WHERE user_id=? AND uuid=?`, userID, uuid).
		Scan(&sk.UUID, &sk.SkillID, &sk.Level, &sk.BaseLevel, &prot, &unitID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, cn35game.ErrSkillNotOwned
	}
	if err != nil {
		return nil, err
	}
	sk.Protected = prot == 1
	if unitID.Valid {
		id := int(unitID.Int64)
		sk.UnitID = &id
	}
	return &sk, nil
}

// PlayerSkills 见 cn35game.SkillStore。
func (s *Store) PlayerSkills(userID string) ([]cn35game.PlayerSkill, error) {
	var out []cn35game.PlayerSkill
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		out, err = playerSkillsTx(tx, userID)
		return err
	})
	return out, err
}

// grantSkillTx 事务内符文发放（商店兑换/SkillGrant 共用同一写入路径）。
func grantSkillTx(tx *sql.Tx, userID string, skillID int, level float64) (*cn35game.PlayerSkill, error) {
	if _, ok := cn35game.SkillCatalog[skillID]; !ok {
		return nil, fmt.Errorf("%w: skill_id=%d 不在符文目录",
			cn35game.ErrInvalidRequest, skillID)
	}
	if level < 5 || level > 36 {
		return nil, fmt.Errorf("%w: level=%v 越界", cn35game.ErrInvalidRequest, level)
	}
	sk := &cn35game.PlayerSkill{
		UUID: "sk-" + randHex(12), SkillID: skillID, Level: level, BaseLevel: level,
	}
	_, err := tx.Exec(`INSERT INTO cn35_player_skills
		(user_id,uuid,skill_id,level,base_level,protected,unit_id,created_at,updated_at)
		VALUES (?,?,?,?,?,0,NULL,?,?)`,
		userID, sk.UUID, sk.SkillID, sk.Level, sk.BaseLevel, nowUTC(), nowUTC())
	if err != nil {
		return nil, err
	}
	return sk, nil
}

// SkillGrant 见 cn35game.SkillStore。uuid 账号内唯一（"sk-"+随机）。
func (s *Store) SkillGrant(userID string, skillID int, level float64) (*cn35game.PlayerSkill, error) {
	var sk *cn35game.PlayerSkill
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		for i := 0; i < 3; i++ { // uuid 极小概率撞主键时重试
			sk, err = grantSkillTx(tx, userID, skillID, level)
			if err == nil {
				return nil
			}
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return sk, nil
}

// SkillSetAll 见 cn35game.SkillStore：同事务内校验+整体替换。
func (s *Store) SkillSetAll(userID string, unitID, weaponID, slotCap int, uuids []string) error {
	if len(uuids) == 0 {
		// 空集合法清空（客户端可能以空 uuids 表达全卸下）。
		return s.SkillOffAll(userID, unitID)
	}
	return s.withTx(func(tx *sql.Tx) error {
		if n := unitOwnedTx(tx, userID, unitID); !n {
			return cn35game.ErrUnitNotOwned
		}
		if len(uuids) > slotCap {
			return fmt.Errorf("%w: %d>%d", cn35game.ErrSkillSlotFull, len(uuids), slotCap)
		}
		seen := map[int]string{}
		picked := map[string]bool{}
		for _, uuid := range uuids {
			if picked[uuid] {
				return fmt.Errorf("%w: 重复 uuid %s", cn35game.ErrInvalidRequest, uuid)
			}
			picked[uuid] = true
			sk, err := loadSkillTx(tx, userID, uuid)
			if err != nil {
				return err
			}
			def, ok := cn35game.SkillCatalog[sk.SkillID]
			if !ok || !def.Equipable {
				return fmt.Errorf("%w: skill_id=%d", cn35game.ErrSkillNotEquipable, sk.SkillID)
			}
			if def.WeaponID != 0 && def.WeaponID != weaponID {
				return fmt.Errorf("%w: skill_id=%d weapon=%d/%d",
					cn35game.ErrSkillWeaponMismatch, sk.SkillID, def.WeaponID, weaponID)
			}
			if prev, dup := seen[sk.SkillID]; dup {
				return fmt.Errorf("%w: skill_id=%d (%s,%s)",
					cn35game.ErrSkillDuplicateKind, sk.SkillID, prev, uuid)
			}
			seen[sk.SkillID] = uuid
		}
		if _, err := tx.Exec(`UPDATE cn35_player_skills SET unit_id=NULL,updated_at=?
			WHERE user_id=? AND unit_id=?`, nowUTC(), userID, unitID); err != nil {
			return err
		}
		for _, uuid := range uuids {
			res, err := tx.Exec(`UPDATE cn35_player_skills SET unit_id=?,updated_at=?
				WHERE user_id=? AND uuid=?`, unitID, nowUTC(), userID, uuid)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrSkillNotOwned
			}
		}
		return nil
	})
}

// itemSkillID 解析 skill_rune_<id> 物品键 → skill_id（无效返回 0）。
func itemSkillID(itemKey string) int {
	var id int
	if n, err := fmt.Sscanf(itemKey, "skill_rune_%d", &id); err == nil && n == 1 {
		return id
	}
	return 0
}

// unitOwnedTx 账号单位存在性校验（单位行属 cn35_player_units 且处于同行状态）。
func unitOwnedTx(tx *sql.Tx, userID string, unitID int) bool {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM cn35_player_units
		WHERE user_id=? AND unit_id=? AND is_waiting=0`, userID, unitID).Scan(&n); err != nil {
		return false
	}
	return n == 1
}

// SkillOff 见 cn35game.SkillStore。
func (s *Store) SkillOff(userID, uuid string) error {
	return s.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE cn35_player_skills SET unit_id=NULL,updated_at=?
			WHERE user_id=? AND uuid=? AND unit_id IS NOT NULL`,
			nowUTC(), userID, uuid)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			var cnt int
			if err := tx.QueryRow(`SELECT COUNT(1) FROM cn35_player_skills
				WHERE user_id=? AND uuid=?`, userID, uuid).Scan(&cnt); err != nil {
				return err
			}
			if cnt == 0 {
				return cn35game.ErrSkillNotOwned
			}
			return cn35game.ErrSkillNotEquipable // 未装备：无可卸下
		}
		return nil
	})
}

// SkillOffAll 见 cn35game.SkillStore。
func (s *Store) SkillOffAll(userID string, unitID int) error {
	return s.withTx(func(tx *sql.Tx) error {
		if !unitOwnedTx(tx, userID, unitID) {
			return cn35game.ErrUnitNotOwned
		}
		_, err := tx.Exec(`UPDATE cn35_player_skills SET unit_id=NULL,updated_at=?
			WHERE user_id=? AND unit_id=?`, nowUTC(), userID, unitID)
		return err
	})
}

// SkillSell 见 cn35game.SkillStore：批量出售入账 gacha_point。
func (s *Store) SkillSell(userID string, uuids []string) (int64, int64, error) {
	var total int64
	var balance int64
	err := s.withTx(func(tx *sql.Tx) error {
		for _, uuid := range uuids {
			sk, err := loadSkillTx(tx, userID, uuid)
			if err != nil {
				return err
			}
			if sk.Protected {
				return fmt.Errorf("%w: %s", cn35game.ErrSkillProtected, uuid)
			}
			if sk.UnitID != nil {
				return fmt.Errorf("%w: %s", cn35game.ErrSkillEquipped, uuid)
			}
			total += cn35game.SkillSellPrice(sk.Level)
		}
		for _, uuid := range uuids {
			res, err := tx.Exec(`DELETE FROM cn35_player_skills
				WHERE user_id=? AND uuid=?`, userID, uuid)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrSkillNotOwned
			}
		}
		if err := creditBalanceTx(tx, userID, "gacha_point", total); err != nil {
			return err
		}
		balance = balanceOfTx(tx, userID, "gacha_point")
		return nil
	})
	return total, balance, err
}

// SkillProtect 见 cn35game.SkillStore。
func (s *Store) SkillProtect(userID string, uuids []string, protected bool) error {
	return s.withTx(func(tx *sql.Tx) error {
		for _, uuid := range uuids {
			res, err := tx.Exec(`UPDATE cn35_player_skills SET protected=?,updated_at=?
				WHERE user_id=? AND uuid=?`, boolInt(protected), nowUTC(), userID, uuid)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return cn35game.ErrSkillNotOwned
			}
		}
		return nil
	})
}

// SkillPowerup 见 cn35game.SkillStore：消耗强化素材物品推进 level。
// 素材即 cn35_item_catalog kind='material' content_id 1-15（powerup_item_<id>）。
func (s *Store) SkillPowerup(userID, uuid string, powerupItemID, count int) (float64, error) {
	var newLevel float64
	err := s.withTx(func(tx *sql.Tx) error {
		sk, err := loadSkillTx(tx, userID, uuid)
		if err != nil {
			return err
		}
		// 逐步按当前魔力重核档位：一次性批量不得用起始档步长跨过
		// 15/22.5/27 的档位边界（档4+ 之后只能走符文合成）。
		itemKey := fmt.Sprintf("powerup_item_%d", powerupItemID)
		if _, ierr := itemKeyByContentTx(tx, powerupItemID); ierr != nil {
			return ierr // ErrItemNotFound
		}
		used, level := 0, sk.Level
		var firstErr error
		for used < count {
			gain, ok := cn35game.SkillPowerupItemGain(sk.SkillID, level, powerupItemID)
			if !ok {
				if cn35game.SkillLevelCategory(level) > 3 {
					firstErr = cn35game.ErrSkillLevelCap // 档4+ 走符文合成
				} else {
					firstErr = cn35game.ErrPowerupItemMismatch
				}
				break
			}
			if level+gain > cn35game.SkillMaxLevel+1e-9 {
				firstErr = cn35game.ErrSkillLevelCap
				break
			}
			level += gain
			used++
		}
		if used == 0 {
			return firstErr
		}
		if err := debitItemTx(tx, userID, itemKey, used); err != nil {
			return err
		}
		newLevel = math.Round(level*1000) / 1000
		res, err := tx.Exec(`UPDATE cn35_player_skills SET level=?,updated_at=?
			WHERE user_id=? AND uuid=?`, newLevel, nowUTC(), userID, uuid)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrSkillNotOwned
		}
		return nil
	})
	return newLevel, err
}

// SkillComposite 见 cn35game.SkillStore：符文作素材合成。
func (s *Store) SkillComposite(userID, uuid, seedUUID string) (float64, error) {
	var newLevel float64
	err := s.withTx(func(tx *sql.Tx) error {
		base, err := loadSkillTx(tx, userID, uuid)
		if err != nil {
			return err
		}
		if cn35game.SkillLevelCategory(base.Level) < 4 {
			return fmt.Errorf("%w: level=%.3f", cn35game.ErrSkillCompositeBase, base.Level)
		}
		if base.Level >= cn35game.SkillMaxLevel {
			return cn35game.ErrSkillLevelCap
		}
		seed, err := loadSkillTx(tx, userID, seedUUID)
		if err != nil {
			return err
		}
		if seed.Protected {
			return fmt.Errorf("%w: %s", cn35game.ErrSkillProtected, seedUUID)
		}
		if seed.UnitID != nil {
			return fmt.Errorf("%w: %s", cn35game.ErrSkillEquipped, seedUUID)
		}
		gain := cn35game.SkillCompositeGain(base.Level, seed.Level, base.SkillID, seed.SkillID)
		if gain <= 0 {
			return fmt.Errorf("%w: seed level=%.3f", cn35game.ErrSkillCompositeSeed, seed.Level)
		}
		newLevel = math.Min(cn35game.SkillMaxLevel,
			math.Round((base.Level+gain)*1000)/1000)
		res, err := tx.Exec(`DELETE FROM cn35_player_skills
			WHERE user_id=? AND uuid=?`, userID, seedUUID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrSkillNotOwned
		}
		if _, err := tx.Exec(`UPDATE cn35_player_skills SET level=?,updated_at=?
			WHERE user_id=? AND uuid=?`, newLevel, nowUTC(), userID, uuid); err != nil {
			return err
		}
		return nil
	})
	return newLevel, err
}
