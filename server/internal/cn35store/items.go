// items.go ITEM350-02/07/09：物品目录/账号库存读面，徽章兑换/发放与
// 觉醒书消费的 SQLite 事务实现。
//
// 事务约定与 battle.go/present_receive.go 一致：方法即事务边界，账号隔离
// 全靠 WHERE user_id 绑定；条件扣减（quantity>=n / amount>=price）兜底并发。
package cn35store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// 编译期断言：Store 满足物品/徽章/觉醒契约。
var (
	_ cn35game.ItemStore      = (*Store)(nil)
	_ cn35game.MedalStore     = (*Store)(nil)
	_ cn35game.AwakenStore    = (*Store)(nil)
	_ cn35game.ItemGrantStore = (*Store)(nil)
	_ cn35game.ItemUseStore   = (*Store)(nil)
)

type sqlQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// itemCatalog 可由 *sql.DB 或 *sql.Tx 调用；发布快照必须在同一事务内
// 读目录，不能把事务外读到的物品状态与草稿混用。
func itemCatalog(q sqlQueryer) ([]cn35game.ItemDef, error) {
	rows, err := q.Query(`SELECT item_key,kind,content_id,name,icon_asset_name,
		element,form,rare,bag_category,enabled FROM cn35_item_catalog ORDER BY content_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.ItemDef{}
	for rows.Next() {
		var d cn35game.ItemDef
		var enabled int
		if err = rows.Scan(&d.ItemKey, &d.Kind, &d.ContentID, &d.Name,
			&d.IconAssetName, &d.Element, &d.Form, &d.Rare, &d.BagCategory,
			&enabled); err != nil {
			return nil, err
		}
		d.Enabled = enabled == 1
		out = append(out, d)
	}
	return out, rows.Err()
}

// ItemCatalog 见 cn35game.ItemStore。
func (s *Store) ItemCatalog() ([]cn35game.ItemDef, error) {
	return itemCatalog(s.db)
}

// PlayerItems 见 cn35game.ItemStore。
func (s *Store) PlayerItems(userID string) ([]cn35game.PlayerItem, error) {
	rows, err := s.db.Query(`SELECT p.item_key,p.quantity,
		c.kind,c.content_id,c.name,c.icon_asset_name,c.element,c.form,c.rare,
		c.bag_category,c.enabled
		FROM cn35_player_items p JOIN cn35_item_catalog c ON c.item_key=p.item_key
		WHERE p.user_id=? AND p.quantity>0 ORDER BY c.content_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cn35game.PlayerItem{}
	for rows.Next() {
		var pi cn35game.PlayerItem
		var enabled int
		if err = rows.Scan(&pi.ItemKey, &pi.Quantity, &pi.Def.Kind, &pi.Def.ContentID,
			&pi.Def.Name, &pi.Def.IconAssetName, &pi.Def.Element, &pi.Def.Form,
			&pi.Def.Rare, &pi.Def.BagCategory, &enabled); err != nil {
			return nil, err
		}
		pi.Def.ItemKey = pi.ItemKey
		pi.Def.Enabled = enabled == 1
		out = append(out, pi)
	}
	return out, rows.Err()
}

// itemKeyByContentTx 事务内 content_id→item_key 解析；停用/未登记 →
// ErrItemNotFound。
func itemKeyByContentTx(tx *sql.Tx, contentID int) (string, error) {
	var key string
	err := tx.QueryRow(`SELECT item_key FROM cn35_item_catalog
		WHERE content_id=? AND enabled=1`, contentID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", cn35game.ErrItemNotFound
	}
	return key, err
}

// creditItemTx 物品入账（行不存在则建）；delta<=0 为空操作。
func creditItemTx(tx *sql.Tx, userID, itemKey string, delta int) error {
	if delta <= 0 {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO cn35_player_items (user_id,item_key,quantity,updated_at)
		VALUES (?,?,?,?)
		ON CONFLICT(user_id,item_key) DO UPDATE
		SET quantity=quantity+excluded.quantity,updated_at=excluded.updated_at`,
		userID, itemKey, delta, nowUTC())
	return err
}

// debitItemTx 物品条件扣减：quantity>=delta 未命中 → ErrItemInsufficient。
func debitItemTx(tx *sql.Tx, userID, itemKey string, delta int) error {
	if delta <= 0 {
		return nil
	}
	res, err := tx.Exec(`UPDATE cn35_player_items
		SET quantity=quantity-?,updated_at=?
		WHERE user_id=? AND item_key=? AND quantity>=?`,
		delta, nowUTC(), userID, itemKey, delta)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return cn35game.ErrItemInsufficient
	}
	return nil
}

// MedalGoods 见 cn35game.MedalStore：只读生效版本商品；未发布为空集，
// 绝不把未发布草稿暴露给玩家。草稿预览走 admin。
func (s *Store) MedalGoods() ([]cn35game.MedalGoodDraft, error) {
	cfg, err := s.ActiveResourceConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return []cn35game.MedalGoodDraft{}, nil
	}
	return cfg.Goods, nil
}

// medalExchangeRequestJSON 是幂等摘要的规范化形态；同一 request_key 只有
// operation 与摘要完全一致才允许走 duplicate 快路径。
func medalExchangeRequestJSON(goodsID, count int) string {
	raw, _ := json.Marshal(map[string]int{"count": count, "goods_id": goodsID})
	return string(raw)
}

// ExchangeMedal 见 cn35game.MedalStore。商品、价格、限购与物品键全部来自
// 开战时玩家看到的同一类不可变来源——当前发布快照；草稿改动不影响兑换。
//
// 去重两层：可选 request_key 走持久台账幂等；并发同参数请求（双击/重传）
// 走 dedupe 在途去重，后到者复用首个结果（Duplicate=true）不再二次入账，
// 首个完成后同参数再次购买正常执行。
func (s *Store) ExchangeMedal(userID string, goodsID, count int, requestKey string) (*cn35game.MedalExchangeResult, error) {
	key := fmt.Sprintf("%s|medal_exchange|%s|%d|%d", userID, requestKey, goodsID, count)
	v, dup, err := s.dedupe(key, func() (any, error) {
		return s.exchangeMedal(userID, goodsID, count, requestKey)
	})
	if err != nil {
		return nil, err
	}
	res := *v.(*cn35game.MedalExchangeResult)
	if dup {
		res.Duplicate = true
	}
	return &res, nil
}

func (s *Store) exchangeMedal(userID string, goodsID, count int, requestKey string) (*cn35game.MedalExchangeResult, error) {
	if goodsID <= 0 || count <= 0 || count > 999 || len(requestKey) > 128 {
		return nil, fmt.Errorf("%w: goods_id=%d count=%d request_key_len=%d",
			cn35game.ErrInvalidRequest, goodsID, count, len(requestKey))
	}
	res := &cn35game.MedalExchangeResult{GoodsID: goodsID}
	wantReq := medalExchangeRequestJSON(goodsID, count)
	err := s.withTx(func(tx *sql.Tx) error {
		if requestKey != "" {
			var operation, reqJSON, rewardsJSON string
			err := tx.QueryRow(`SELECT operation,request_json,rewards_json
				FROM cn35_resource_reward_events
				WHERE user_id=? AND request_key=?`, userID, requestKey).
				Scan(&operation, &reqJSON, &rewardsJSON)
			switch {
			case err == nil && (operation != "medal_exchange" || reqJSON != wantReq):
				return cn35game.ErrDuplicateRequestConflict
			case err == nil:
				res.Duplicate = true
				var rewards []struct {
					ItemKey string `json:"item_key"`
					Count   int    `json:"count"`
				}
				if json.Unmarshal([]byte(rewardsJSON), &rewards) == nil && len(rewards) > 0 {
					res.ItemKey, res.Count = rewards[0].ItemKey, rewards[0].Count
				}
				res.Medal = balanceOfTx(tx, userID, "medal")
				return nil
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
		}
		cfg, err := activeResourceConfigTx(tx)
		if err != nil {
			return err
		}
		if cfg == nil {
			return cn35game.ErrNoResourceConfig
		}
		var goods *cn35game.MedalGoodDraft
		for i := range cfg.Goods {
			if cfg.Goods[i].GoodsID == goodsID {
				goods = &cfg.Goods[i]
				break
			}
		}
		if goods == nil || !goods.Enabled {
			return cn35game.ErrGoodsNotFound
		}
		var itemKind string
		var enabled int
		err = tx.QueryRow(`SELECT kind,enabled FROM cn35_item_catalog
			WHERE item_key=? AND kind IN ('book','material','consumable')`, goods.ItemKey).Scan(&itemKind, &enabled)
		if errors.Is(err, sql.ErrNoRows) || enabled != 1 {
			return cn35game.ErrGoodsNotFound
		}
		if err != nil {
			return err
		}
		// 限购按 goods_key 累计；先建 0 行，再用条件 UPDATE 兜底并发。
		if _, err := tx.Exec(`INSERT OR IGNORE INTO cn35_medal_purchase_totals
			(user_id,goods_key,total_count,updated_at) VALUES (?,?,0,?)`,
			userID, goods.GoodsKey, nowUTC()); err != nil {
			return err
		}
		if goods.PurchaseLimit != nil {
			up, err := tx.Exec(`UPDATE cn35_medal_purchase_totals
				SET total_count=total_count+?,updated_at=?
				WHERE user_id=? AND goods_key=? AND total_count+?<=?`,
				count, nowUTC(), userID, goods.GoodsKey, count, *goods.PurchaseLimit)
			if err != nil {
				return err
			}
			if n, _ := up.RowsAffected(); n != 1 {
				return cn35game.ErrPurchaseLimit
			}
		} else if _, err := tx.Exec(`UPDATE cn35_medal_purchase_totals
			SET total_count=total_count+?,updated_at=? WHERE user_id=? AND goods_key=?`,
			count, nowUTC(), userID, goods.GoodsKey); err != nil {
			return err
		}
		res.ItemKey = goods.ItemKey
		res.Count = count
		total := int64(goods.Price) * int64(count)
		up, err := tx.Exec(`UPDATE cn35_player_balances
			SET amount=amount-?,updated_at=?
			WHERE user_id=? AND currency='medal' AND amount>=?`,
			total, nowUTC(), userID, total)
		if err != nil {
			return err
		}
		if n, _ := up.RowsAffected(); n != 1 {
			return cn35game.ErrMedalInsufficient
		}
		// RUNE350-01：skill_rune_<id> 锚点物品的符文商品入账符文库存
		// （固定魔力 SkillShopLevel 基础品质），count>1 逐枚发放；
		// 其余照旧物品入账。
		if skillID := itemSkillID(goods.ItemKey); skillID > 0 {
			for i := 0; i < count; i++ {
				if _, err := grantSkillTx(tx, userID, skillID,
					cn35game.SkillShopLevel); err != nil {
					return err
				}
			}
		} else if err := creditItemTx(tx, userID, goods.ItemKey, count); err != nil {
			return err
		}
		if requestKey != "" {
			rewards, _ := json.Marshal([]map[string]any{{
				"item_key": goods.ItemKey, "count": count, "goods_id": goodsID,
			}})
			if _, err := tx.Exec(`INSERT INTO cn35_resource_reward_events
				(user_id,request_key,operation,request_json,quest_key,config_version,
				 result,rewards_json,created_at)
				VALUES (?,?,'medal_exchange',?,?,?,'medal_exchange',?,?)`,
				userID, requestKey, wantReq, goods.GoodsKey, cfg.Version,
				string(rewards), nowUTC()); err != nil {
				return err
			}
		}
		res.Medal = balanceOfTx(tx, userID, "medal")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// GrantMedal 有审计的徽章发放：request_key 幂等台账 + cn35_admin_audit 同事务。
// 相同 request_key 且请求载荷一致 → ErrDuplicateRequest；载荷不同 →
// ErrDuplicateRequestConflict，杜绝把新的发放误报成旧请求成功。
func (s *Store) GrantMedal(userID string, amount int64, actor, reason, requestKey string) error {
	if amount <= 0 {
		return fmt.Errorf("cn35store: 发放数量必须为正 %d", amount)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("%w: 徽章发放缺 reason", cn35game.ErrInvalidRequest)
	}
	if requestKey == "" || len(requestKey) > 128 {
		return fmt.Errorf("%w: 徽章发放 request_key 非法", cn35game.ErrInvalidRequest)
	}
	return s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		var existingUser, existingReason string
		var existingAmount int64
		err := tx.QueryRowContext(ctx, `SELECT user_id,amount,reason
			FROM cn35_medal_grants WHERE request_key=?`, requestKey).
			Scan(&existingUser, &existingAmount, &existingReason)
		switch {
		case err == nil:
			if existingUser != userID || existingAmount != amount || existingReason != reason {
				return cn35game.ErrDuplicateRequestConflict
			}
			return cn35game.ErrDuplicateRequest
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM cn35_accounts WHERE user_id=?`, userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return cn35game.ErrNoAccount
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cn35_medal_grants
			(request_key,user_id,amount,actor,reason,created_at) VALUES (?,?,?,?,?,?)`,
			requestKey, userID, amount, actor, reason, nowUTC()); err != nil {
			return err
		}
		if err := creditBalanceTx(tx, userID, "medal", amount); err != nil {
			return err
		}
		detail := fmt.Sprintf(`{"user_id":%q,"amount":%d,"reason":%q,"request_key":%q}`,
			userID, amount, reason, requestKey)
		_, err = tx.ExecContext(ctx, `INSERT INTO cn35_admin_audit
			(actor,action,target,detail,created_at) VALUES (?,?,?,?,?)`,
			actor, "medal.grant", userID, detail, nowUTC())
		return err
	})
}

// GrantItem 有审计的物品发放：request_key 幂等台账 + cn35_admin_audit 同事务。
// 相同 request_key 且载荷一致 → ErrDuplicateRequest；载荷不同 →
// ErrDuplicateRequestConflict（语义同 GrantMedal）。目标物品须为已启用目录行。
func (s *Store) GrantItem(userID, itemKey string, count int, actor, reason, requestKey string) error {
	if count <= 0 {
		return fmt.Errorf("cn35store: 发放数量必须为正 %d", count)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("%w: 物品发放缺 reason", cn35game.ErrInvalidRequest)
	}
	if requestKey == "" || len(requestKey) > 128 {
		return fmt.Errorf("%w: 物品发放 request_key 非法", cn35game.ErrInvalidRequest)
	}
	return s.withTx(func(tx *sql.Tx) error {
		var existingUser, existingItem, existingReason string
		var existingCount int
		err := tx.QueryRow(`SELECT user_id,item_key,count,reason
			FROM cn35_item_grants WHERE request_key=?`, requestKey).
			Scan(&existingUser, &existingItem, &existingCount, &existingReason)
		switch {
		case err == nil:
			if existingUser != userID || existingItem != itemKey ||
				existingCount != count || existingReason != reason {
				return cn35game.ErrDuplicateRequestConflict
			}
			return cn35game.ErrDuplicateRequest
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var n int
		if err := tx.QueryRow(
			`SELECT count(*) FROM cn35_accounts WHERE user_id=?`, userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return cn35game.ErrNoAccount
		}
		var enabled int
		err = tx.QueryRow(`SELECT enabled FROM cn35_item_catalog
			WHERE item_key=?`, itemKey).Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) || enabled != 1 {
			return cn35game.ErrItemNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO cn35_item_grants
			(request_key,user_id,item_key,count,actor,reason,created_at)
			VALUES (?,?,?,?,?,?,?)`,
			requestKey, userID, itemKey, count, actor, reason, nowUTC()); err != nil {
			return err
		}
		// 符文锚点（skill_rune_<id>，kind='consumable'）的真实库存是
		// cn35_player_skills——进普通物品栏会产生不可用行。
		if skillID := itemSkillID(itemKey); skillID > 0 {
			for i := 0; i < count; i++ {
				if _, err := grantSkillTx(tx, userID, skillID, cn35game.SkillShopLevel); err != nil {
					return err
				}
			}
		} else if err := creditItemTx(tx, userID, itemKey, count); err != nil {
			return err
		}
		detail := fmt.Sprintf(`{"user_id":%q,"item_key":%q,"count":%d,"reason":%q,"request_key":%q}`,
			userID, itemKey, count, reason, requestKey)
		_, err = tx.Exec(`INSERT INTO cn35_admin_audit
			(actor,action,target,detail,created_at) VALUES (?,?,?,?,?)`,
			actor, "item.grant", userID, detail, nowUTC())
		return err
	})
}

// AwakenUnit 见 cn35game.AwakenStore（暂定契约：书键 awakening_book_<rare>）。
// 上行无稳定请求标识：并发同单位觉醒经 dedupe 在途去重，后到者复用首个
// 结果（Duplicate=true），首个完成后二次觉醒在有书且未达上限时仍执行。
func (s *Store) AwakenUnit(userID string, unitID, rare, maxDup, count int) (*cn35game.AwakenResult, error) {
	key := fmt.Sprintf("%s|awaken|%d|%d", userID, unitID, count)
	v, dup, err := s.dedupe(key, func() (any, error) {
		return s.awakenUnit(userID, unitID, rare, maxDup, count)
	})
	if err != nil {
		return nil, err
	}
	res := *v.(*cn35game.AwakenResult)
	if dup {
		res.Duplicate = true
	}
	return &res, nil
}

func (s *Store) awakenUnit(userID string, unitID, rare, maxDup, count int) (*cn35game.AwakenResult, error) {
	if count <= 0 {
		return nil, cn35game.ErrInvalidRequest
	}
	res := &cn35game.AwakenResult{UnitID: unitID}
	err := s.withTx(func(tx *sql.Tx) error {
		var dup int
		err := tx.QueryRow(`SELECT duplicated_count FROM cn35_player_units
			WHERE user_id=? AND unit_id=? AND is_waiting=0`, userID, unitID).Scan(&dup)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrAwakeUnitNotFound
		}
		if err != nil {
			return err
		}
		if dup+count > maxDup {
			return cn35game.ErrAwakeCap
		}
		bookKey := fmt.Sprintf("awakening_book_%d", rare)
		var contentID int
		err = tx.QueryRow(`SELECT content_id FROM cn35_item_catalog
			WHERE item_key=? AND kind='book' AND enabled=1`, bookKey).Scan(&contentID)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrItemNotFound
		}
		if err != nil {
			return err
		}
		if err := debitItemTx(tx, userID, bookKey, count); err != nil {
			return err
		}
		up, err := tx.Exec(`UPDATE cn35_player_units
			SET duplicated_count=duplicated_count+?,updated_at=?
			WHERE user_id=? AND unit_id=? AND duplicated_count+?<=?`,
			count, nowUTC(), userID, unitID, count, maxDup)
		if err != nil {
			return err
		}
		if n, _ := up.RowsAffected(); n != 1 {
			return cn35game.ErrAwakeCap
		}
		res.DuplicatedCount = dup + count
		res.BookContentID = contentID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// UseItem 见 cn35game.ItemUseStore：/user_items/use/ 写面。按 content_id
// 解析启用目录行，按 item_key 分派效果，同事务条件扣减数量。
//
// 效果表（重建策略，证据见 items_wire.go）：ap_potion→AP 回满并重置恢复
// 锚点；bp_potion→BP 回满（无 bp 恢复计时列）。并发同物品使用经 dedupe
// 在途去重，后到者复用首个结果（Duplicate=true）。
func (s *Store) UseItem(userID string, contentID, count int) (*cn35game.UseItemResult, error) {
	key := fmt.Sprintf("%s|use_item|%d|%d", userID, contentID, count)
	v, dup, err := s.dedupe(key, func() (any, error) {
		return s.useItem(userID, contentID, count)
	})
	if err != nil {
		return nil, err
	}
	res := *v.(*cn35game.UseItemResult)
	if dup {
		res.Duplicate = true
	}
	return &res, nil
}

func (s *Store) useItem(userID string, contentID, count int) (*cn35game.UseItemResult, error) {
	if count <= 0 || count > 999 {
		return nil, cn35game.ErrInvalidRequest
	}
	res := &cn35game.UseItemResult{Count: count}
	err := s.withTx(func(tx *sql.Tx) error {
		now := nowUTC()
		if contentID == 203 {
			// NoApModal 钻石恢复 AP：扣除 count 枚钻石并将 AP 恢复至上限。
			up, err := tx.Exec(`UPDATE cn35_player_balances
				SET amount=amount-?,updated_at=?
				WHERE user_id=? AND currency='coin' AND amount>=?`,
				count, now, userID, count)
			if err != nil {
				return err
			}
			if n, _ := up.RowsAffected(); n != 1 {
				return cn35game.ErrItemInsufficient
			}
			if _, err := tx.Exec(`UPDATE cn35_player_profiles
				SET ap=MAX(ap, max_ap),ap_updated_at=?,updated_at=? WHERE user_id=?`,
				now, now, userID); err != nil {
				return err
			}
			res.ItemKey = "coin_ap"
			return nil
		}
		var itemKey, kind string
		err := tx.QueryRow(`SELECT item_key,kind FROM cn35_item_catalog
			WHERE content_id=? AND enabled=1`, contentID).Scan(&itemKey, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrItemNotFound
		}
		if err != nil {
			return err
		}
		if kind != "consumable" {
			return cn35game.ErrItemNotUsable
		}
		switch itemKey {
		case "ap_potion":
			// AP 回满（若已溢出则保持原值）：锚点重置为 now，恢复计时从满 AP 重新开始。
			if _, err := tx.Exec(`UPDATE cn35_player_profiles
				SET ap=MAX(ap, max_ap),ap_updated_at=?,updated_at=? WHERE user_id=?`,
				now, now, userID); err != nil {
				return err
			}
		case "bp_potion":
			if _, err := tx.Exec(`UPDATE cn35_player_profiles
				SET bp=max_bp,updated_at=? WHERE user_id=?`,
				now, userID); err != nil {
				return err
			}
		default:
			return cn35game.ErrItemNotUsable
		}
		if err := debitItemTx(tx, userID, itemKey, count); err != nil {
			return err
		}
		res.ItemKey = itemKey
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// balanceOfTx 事务内读单币种余额（无行=0）。
func balanceOfTx(tx *sql.Tx, userID, currency string) int64 {
	var amt int64
	_ = tx.QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency=?`, userID, currency).Scan(&amt)
	return amt
}
