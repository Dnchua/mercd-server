// deck.go MIN350-06：账号编队持久化与同账号槽位约束。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var _ cn35game.DeckStore = (*Store)(nil)
var _ cn35game.DeckBattleStore = (*Store)(nil)

// Decks returns account-owned parties in the exact order used by the client
// carousel. The first read creates two safe defaults if the account has none.
func (s *Store) Decks(userID string) ([]cn35game.Deck, error) {
	var out []cn35game.Deck
	err := s.withTx(func(tx *sql.Tx) error {
		if err := s.ensureDefaultDecksTx(tx, userID); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT d.party_id,d.display_order,s.slot_index,s.unit_id
			FROM cn35_player_decks d
			LEFT JOIN cn35_player_deck_slots s
				ON s.user_id=d.user_id AND s.party_id=d.party_id
			WHERE d.user_id=?
			ORDER BY d.display_order,d.party_id,s.slot_index`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		indexByParty := map[string]int{}
		for rows.Next() {
			var partyID string
			var displayOrder int
			var slotIndex sql.NullInt64
			var unitID sql.NullInt64
			if err := rows.Scan(&partyID, &displayOrder, &slotIndex, &unitID); err != nil {
				return err
			}
			index, ok := indexByParty[partyID]
			if !ok {
				index = len(out)
				indexByParty[partyID] = index
				out = append(out, cn35game.Deck{
					PartyID: partyID, DisplayOrder: displayOrder,
					Slots: make([]string, cn35game.DefaultDeckSlots),
				})
			}
			if slotIndex.Valid {
				i := int(slotIndex.Int64)
				if i < 0 || i >= cn35game.DefaultDeckSlots {
					return fmt.Errorf("cn35store: deck %s slot index %d outside current shape", partyID, i)
				}
				if unitID.Valid {
					out[index].Slots[i] = strconv.FormatInt(unitID.Int64, 10)
				}
			}
		}
		return rows.Err()
	})
	return out, err
}

// ensureDefaultDecksTx creates a local starter shape from this account's
// existing inventory only. Shared preset fixture members are never consulted.
func (s *Store) ensureDefaultDecksTx(tx *sql.Tx, userID string) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_decks WHERE user_id=?`, userID).Scan(&count); err != nil {
		return err
	}
	// 新手起始阵容保底：台账幂等补齐五卡礼包（建号早于本修复的存量
	// 账号在首次编队读取时自愈），否则零库存账号永远组不出可出战编队，
	// 教程首个战斗关被客户端「编队为空」阻断。
	if err := s.ensureStarterUnitsTx(tx, userID); err != nil {
		return err
	}
	if count > 0 {
		// 编队结构已建但首队留有空槽 → 建号后首读早于单位入库的场景
		// 需要补填空槽；玩家已放置的槽位保持原状。
		return s.fillStarterDeckTx(tx, userID)
	}
	selected, err := s.starterUnitsTx(tx, userID)
	if err != nil {
		return err
	}

	now := s.clockNowUTC().Format(timeStampLayout)
	for order := 0; order < cn35game.DefaultDeckCount; order++ {
		partyID := strconv.Itoa(order + 1)
		if _, err := tx.Exec(`INSERT INTO cn35_player_decks
			(user_id,party_id,display_order,created_at,updated_at) VALUES (?,?,?,?,?)`,
			userID, partyID, order, now, now); err != nil {
			return fmt.Errorf("cn35store: 创建默认编队 %s 失败: %w", partyID, err)
		}
		for slot := 0; slot < cn35game.DefaultDeckSlots; slot++ {
			var unit any
			if order == 0 && slot < len(selected) {
				unit = selected[slot]
			}
			if _, err := tx.Exec(`INSERT INTO cn35_player_deck_slots
				(user_id,party_id,slot_index,unit_id) VALUES (?,?,?,?)`,
				userID, partyID, slot, unit); err != nil {
				return fmt.Errorf("cn35store: 初始化编队 %s 槽位 %d 失败: %w", partyID, slot, err)
			}
		}
	}
	return nil
}

// starterUnitsTx 从账号实际库存计算首队候选：队长（若持有）优先，其余按
// unit_id 升序补足五槽；与默认编队初始化共用同一份选择规则。
// 前置：调用方须先经 ensureStarterUnitsTx 保证起始阵容已发放。
func (s *Store) starterUnitsTx(tx *sql.Tx, userID string) ([]int, error) {
	var leaderRaw string
	if err := tx.QueryRow(`SELECT leader_unit_id FROM cn35_player_profiles WHERE user_id=?`,
		userID).Scan(&leaderRaw); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT unit_id FROM cn35_player_units WHERE user_id=? AND is_waiting=0 ORDER BY unit_id`, userID)
	if err != nil {
		return nil, err
	}
	owned := make([]int, 0, cn35game.DefaultDeckSlots)
	ownedSet := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		owned = append(owned, id)
		ownedSet[id] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	selected := make([]int, 0, cn35game.DefaultDeckSlots)
	if leader, err := strconv.Atoi(leaderRaw); err == nil && ownedSet[leader] {
		selected = append(selected, leader)
	}
	for _, id := range owned {
		if len(selected) == cn35game.DefaultDeckSlots {
			break
		}
		alreadySelected := false
		for _, chosen := range selected {
			if chosen == id {
				alreadySelected = true
				break
			}
		}
		if !alreadySelected {
			selected = append(selected, id)
		}
	}
	return selected, nil
}

// fillStarterDeckTx 补存量账号首队的空槽位：仅当所有已占用槽位都属于
// 起始五卡集时视为「未经玩家自定义的新手编队」，为 NULL 槽位按
// starterUnitsTx 顺序补入尚未入编的起始卡。含非起始卡槽位的编队
// 认定已被玩家配置过，一律保持原状；五槽全满时不写。
func (s *Store) fillStarterDeckTx(tx *sql.Tx, userID string) error {
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_decks
		WHERE user_id=? AND party_id='1' AND starter_backfill_disabled=0`, userID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	rows, err := tx.Query(`SELECT slot_index,unit_id FROM cn35_player_deck_slots
		WHERE user_id=? AND party_id='1' ORDER BY slot_index`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	emptySlots := []int{}
	placed := map[int]bool{}
	customized := false
	starterSet := map[int]bool{}
	for _, id := range starterUnitIDs {
		starterSet[id] = true
	}
	for rows.Next() {
		var slot int
		var unit sql.NullInt64
		if err := rows.Scan(&slot, &unit); err != nil {
			return err
		}
		if unit.Valid {
			id := int(unit.Int64)
			placed[id] = true
			if !starterSet[id] {
				customized = true
			}
		} else {
			emptySlots = append(emptySlots, slot)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(emptySlots) == 0 || customized {
		return nil
	}
	selected, err := s.starterUnitsTx(tx, userID)
	if err != nil {
		return err
	}
	pending := []int{}
	for _, id := range selected {
		if !placed[id] {
			pending = append(pending, id)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	now := s.clockNowUTC().Format(timeStampLayout)
	for i, id := range pending {
		if i >= len(emptySlots) {
			break
		}
		if _, err := tx.Exec(`UPDATE cn35_player_deck_slots SET unit_id=?
			WHERE user_id=? AND party_id='1' AND slot_index=?`,
			id, userID, emptySlots[i]); err != nil {
			return fmt.Errorf("cn35store: 回填初始编队槽位 %d 失败: %w", emptySlots[i], err)
		}
	}
	return touchDeckTx(tx, userID, "1", now)
}

// starterUnitIDs 新手起始阵容 = PLAY350-08 五卡礼包锁定集
// （server/assets/cn350/five_star_grant.json，leader=348）。台账键复用
// cn35game.FiveStarGrantKey：后续管理员对同账号执行 cn35grant 时命中
// 幂等短路而非重复发放。
var starterUnitIDs = []int{152, 157, 161, 230, 348}

// ensureStarterUnitsTx 保证账号持有起始阵容：台账 (user_id,
// FiveStarGrantKey, unit_id) 已登记的卡跳过；库存已有该行（台账缺失的
// 历史数据）只补台账不重复插入；其余同事务发放 + 登记。
func (s *Store) ensureStarterUnitsTx(tx *sql.Tx, userID string) error {
	applied, err := GrantAppliedTx(tx, userID, cn35game.FiveStarGrantKey)
	if err != nil {
		return err
	}
	for _, id := range starterUnitIDs {
		if applied[id] {
			continue
		}
		var held int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_units
			WHERE user_id=? AND unit_id=?`, userID, id).Scan(&held); err != nil {
			return err
		}
		if held == 0 {
			if err := InsertUnitTx(tx, userID, cn35game.UnitRecord{
				UnitID: id, Level: seedUnitLevel, Newcomer: true}); err != nil {
				return err
			}
		}
		if err := RecordGrantTx(tx, userID, cn35game.FiveStarGrantKey, id); err != nil {
			return err
		}
	}
	return nil
}

const timeStampLayout = "2006-01-02T15:04:05.999999999Z07:00"

// SetDeckUnit replaces an existing slot after checking ownership. If the
// selected unit is already present in another slot of the same party, the two
// slots are atomically swapped (or moved if the target slot is empty).
func (s *Store) SetDeckUnit(userID, partyID string, slotIndex, unitID int) error {
	if slotIndex < 0 || slotIndex >= cn35game.DefaultDeckSlots || unitID <= 0 {
		return cn35game.ErrDeckSlotInvalid
	}
	return s.withTx(func(tx *sql.Tx) error {
		if err := requireDeckAndSlotTx(tx, userID, partyID, slotIndex); err != nil {
			return err
		}
		var owned int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM cn35_player_units WHERE user_id=? AND unit_id=? AND is_waiting=0`,
			userID, unitID).Scan(&owned); err != nil {
			return err
		}
		if owned != 1 {
			return cn35game.ErrDeckUnitNotOwned
		}
		var current sql.NullInt64
		if err := tx.QueryRow(`SELECT unit_id FROM cn35_player_deck_slots
			WHERE user_id=? AND party_id=? AND slot_index=?`,
			userID, partyID, slotIndex).Scan(&current); err != nil {
			return err
		}
		if current.Valid && int(current.Int64) == unitID {
			return markDeckMutatedTx(tx, userID, partyID, s.clockNowUTC().Format(timeStampLayout))
		}
		var otherSlot int
		err := tx.QueryRow(`SELECT slot_index FROM cn35_player_deck_slots
			WHERE user_id=? AND party_id=? AND unit_id=? AND slot_index<>?`,
			userID, partyID, unitID, slotIndex).Scan(&otherSlot)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if _, err := tx.Exec(`UPDATE cn35_player_deck_slots SET unit_id=NULL
				WHERE user_id=? AND party_id=? AND slot_index=?`,
				userID, partyID, otherSlot); err != nil {
				return fmt.Errorf("cn35store: 腾出原编队槽位失败: %w", err)
			}
			if _, err := tx.Exec(`UPDATE cn35_player_deck_slots SET unit_id=?
				WHERE user_id=? AND party_id=? AND slot_index=?`,
				unitID, userID, partyID, slotIndex); err != nil {
				return fmt.Errorf("cn35store: 设置目标编队槽位失败: %w", err)
			}
			if current.Valid {
				if _, err := tx.Exec(`UPDATE cn35_player_deck_slots SET unit_id=?
					WHERE user_id=? AND party_id=? AND slot_index=?`,
					current.Int64, userID, partyID, otherSlot); err != nil {
					return fmt.Errorf("cn35store: 对调原编队槽位失败: %w", err)
				}
			}
		} else {
			if _, err := tx.Exec(`UPDATE cn35_player_deck_slots SET unit_id=?
				WHERE user_id=? AND party_id=? AND slot_index=?`,
				unitID, userID, partyID, slotIndex); err != nil {
				return fmt.Errorf("cn35store: 设置编队槽位失败: %w", err)
			}
		}
		return markDeckMutatedTx(tx, userID, partyID, s.clockNowUTC().Format(timeStampLayout))
	})
}

// ClearDeckUnit clears only the exact current slot the client selected; stale
// edit pages cannot erase a different member after another request changed it.
func (s *Store) ClearDeckUnit(userID, partyID string, slotIndex, expectedUnitID int) error {
	if slotIndex < 0 || slotIndex >= cn35game.DefaultDeckSlots || expectedUnitID <= 0 {
		return cn35game.ErrDeckSlotInvalid
	}
	return s.withTx(func(tx *sql.Tx) error {
		if err := requireDeckAndSlotTx(tx, userID, partyID, slotIndex); err != nil {
			return err
		}
		var current sql.NullInt64
		if err := tx.QueryRow(`SELECT unit_id FROM cn35_player_deck_slots
			WHERE user_id=? AND party_id=? AND slot_index=?`,
			userID, partyID, slotIndex).Scan(&current); err != nil {
			return err
		}
		if !current.Valid || int(current.Int64) != expectedUnitID {
			return cn35game.ErrDeckSlotConflict
		}
		if _, err := tx.Exec(`UPDATE cn35_player_deck_slots SET unit_id=NULL
			WHERE user_id=? AND party_id=? AND slot_index=?`,
			userID, partyID, slotIndex); err != nil {
			return err
		}
		return markDeckMutatedTx(tx, userID, partyID, s.clockNowUTC().Format(timeStampLayout))
	})
}

func requireDeckAndSlotTx(tx *sql.Tx, userID, partyID string, slotIndex int) error {
	var exists int
	err := tx.QueryRow(`SELECT 1 FROM cn35_player_decks
		WHERE user_id=? AND party_id=?`, userID, partyID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return cn35game.ErrDeckNotFound
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(`SELECT 1 FROM cn35_player_deck_slots
		WHERE user_id=? AND party_id=? AND slot_index=?`,
		userID, partyID, slotIndex).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return cn35game.ErrDeckSlotInvalid
	}
	return err
}

func touchDeckTx(tx *sql.Tx, userID, partyID, at string) error {
	_, err := tx.Exec(`UPDATE cn35_player_decks SET updated_at=?
		WHERE user_id=? AND party_id=?`, at, userID, partyID)
	return err
}

func markDeckMutatedTx(tx *sql.Tx, userID, partyID, at string) error {
	_, err := tx.Exec(`UPDATE cn35_player_decks
		SET updated_at=?,
		    starter_backfill_disabled=CASE WHEN party_id='1' THEN 1 ELSE starter_backfill_disabled END
		WHERE user_id=? AND party_id=?`, at, userID, partyID)
	return err
}
