// present_receive.go P0 写面：/user_presents/receive/ 的 SQLite 实现。
//
// 事务不变式（与 gacha.go 同一套）：读礼物行 → 校验 → 条件更新
// received_at → 发奖（单位/魔宠/余额经 award_tx.go 共享原语）→ 提交。
// 任一失败整体回滚，received_at 不留半领取态。
//
// **事务内禁止调用 cat**：mercd35 装配的 UnitCatalog 是 rosterCatalog
// 覆盖层，Unit/Monster 会走 Store.UnitTemplate→s.db 查询——SQLite 单连接
// 下事务内再取连接必死锁（与 SettleGacha 的"模板校验在上游"约定一致，
// 校验全部在 withTx 之外完成；received_at 条件更新兜底并发）。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/statmodel"
)

// 编译期断言：Store 满足礼物领取写面契约。
var _ cn35game.PresentReceiveStore = (*Store)(nil)

// presentRowFor 读一条礼物行（含 received_at 判定位）；不存在/他人 uuid
// 归一 ErrPresentNotFound。
func (s *Store) presentRowFor(userID, uuid string) (cn35game.PresentRecord, int64, error) {
	var r cn35game.PresentRecord
	var receivedAt int64
	err := s.db.QueryRow(`SELECT content_type,content_id,amount,expire_at,received_at,created_at
		FROM cn35_user_presents WHERE uuid=? AND user_id=?`, uuid, userID).
		Scan(&r.ContentType, &r.ContentID, &r.Amount, &r.ExpireAt, &receivedAt, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, 0, cn35game.ErrPresentNotFound
	}
	if err != nil {
		return r, 0, fmt.Errorf("cn35store: 礼物读取失败: %w", err)
	}
	r.UUID = uuid
	return r, receivedAt, nil
}

// presentGrantSupported 类型/模板/数量校验（事务外执行）。
func presentGrantSupported(r cn35game.PresentRecord, cat cn35game.UnitCatalog) bool {
	if r.Amount < 1 {
		return false
	}
	switch r.ContentType {
	case "unit":
		_, err := cat.Unit(int(r.ContentID))
		return err == nil
	case "monster":
		_, err := cat.Monster(int(r.ContentID))
		return err == nil
	case "coin", "gacha_point", "medal":
		return true
	case "item":
		// ITEM350-07：道具礼物；content_id→item_key 归属在 grantPresentTx
		// 事务内经 itemKeyByContentTx 核对（不存在 → ErrPresentUnsupported）。
		return r.ContentID > 0
	}
	return false
}

// ReceivePresent 单领：uuid 必须属本账号、未领、未过期、类型受支持。
func (s *Store) ReceivePresent(userID, uuid string, now int64, cat cn35game.UnitCatalog) (*cn35game.PresentReceiveResult, error) {
	// 阶段 1（事务外）：读行 + 状态/类型校验。事务内不得调 cat（见文件头）。
	r, receivedAt, err := s.presentRowFor(userID, uuid)
	if err != nil {
		return nil, err
	}
	if receivedAt != 0 {
		return nil, cn35game.ErrPresentAlreadyReceived
	}
	if r.ExpireAt != 0 && r.ExpireAt <= now {
		return nil, cn35game.ErrPresentExpired
	}
	if !presentGrantSupported(r, cat) {
		return nil, cn35game.ErrPresentUnsupported
	}
	unitCaps := presentUnitCaps([]cn35game.PresentRecord{r}, cat)
	// 阶段 2（事务内）：条件标记 + 发奖。并发二次领取由条件更新兜底。
	res := &cn35game.PresentReceiveResult{Granted: []cn35game.PresentGrant{}}
	err = s.withTx(func(tx *sql.Tx) error {
		if err := markPresentReceivedTx(tx, userID, uuid, now); err != nil {
			return err
		}
		g, err := grantPresentTx(tx, userID, r, unitCaps[int(r.ContentID)])
		if err != nil {
			return err
		}
		res.Granted = append(res.Granted, *g)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// ReceiveAllPresents 全领：一个事务内领取全部未领未过期且类型受支持的
// 礼物；不支持/异常类型跳过并保持未领；withoutUnit 排除 unit 类型。
func (s *Store) ReceiveAllPresents(userID string, withoutUnit bool, now int64, cat cn35game.UnitCatalog) (*cn35game.PresentReceiveResult, error) {
	// 阶段 1（事务外）：取候选集 + 逐行可领取性判定（cat 查询不得进 withTx）。
	rows, err := s.db.Query(`SELECT uuid,content_type,content_id,amount,expire_at,created_at
		FROM cn35_user_presents WHERE user_id=? AND received_at=0
		ORDER BY created_at ASC, uuid ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 礼物列表读取失败: %w", err)
	}
	var pending []cn35game.PresentRecord
	for rows.Next() {
		var r cn35game.PresentRecord
		if err := rows.Scan(&r.UUID, &r.ContentType, &r.ContentID,
			&r.Amount, &r.ExpireAt, &r.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	claimable := make([]cn35game.PresentRecord, 0, len(pending))
	for _, r := range pending {
		if r.ExpireAt != 0 && r.ExpireAt <= now {
			continue // 过期保持未领（与 UnreadPresents 口径一致）
		}
		if withoutUnit && r.ContentType == "unit" {
			continue
		}
		if !presentGrantSupported(r, cat) {
			continue // 不支持类型保持未领，不伪造成功
		}
		claimable = append(claimable, r)
	}
	unitCaps := presentUnitCaps(claimable, cat)
	// 阶段 2（事务内）：逐行条件标记 + 发奖；任一失败整批回滚。
	res := &cn35game.PresentReceiveResult{Granted: []cn35game.PresentGrant{}}
	err = s.withTx(func(tx *sql.Tx) error {
		for _, r := range claimable {
			if err := markPresentReceivedTx(tx, userID, r.UUID, now); err != nil {
				return err
			}
			g, gerr := grantPresentTx(tx, userID, r, unitCaps[int(r.ContentID)])
			if gerr != nil {
				return gerr
			}
			res.Granted = append(res.Granted, *g)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// markPresentReceivedTx 条件更新：仅未领取行可置 received_at；
// 影响行数必须恰为 1（并发/重放的第二道防线）。
func markPresentReceivedTx(tx *sql.Tx, userID, uuid string, now int64) error {
	res, err := tx.Exec(`UPDATE cn35_user_presents
		SET received_at=? WHERE uuid=? AND user_id=? AND received_at=0`, now, uuid, userID)
	if err != nil {
		return fmt.Errorf("cn35store: 礼物领取标记失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return cn35game.ErrPresentAlreadyReceived
	}
	return nil
}

// grantPresentTx 发奖原语：单位/魔宠经 award_tx.go 共享语义入库。
// 调用前必须已过 presentGrantSupported（模板校验在事务外）。
func grantPresentTx(tx *sql.Tx, userID string, r cn35game.PresentRecord, maxDup int) (*cn35game.PresentGrant, error) {
	g := &cn35game.PresentGrant{ContentType: r.ContentType, ContentID: r.ContentID, Amount: r.Amount}
	switch r.ContentType {
	case "unit":
		// amount=拷贝数：未持有时第 1 个为实体行，其余进 duplicated_count；
		// 已持有时全部堆叠（服务端重建语义，与抽卡入库同原语）。
		var owned int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_player_units
			WHERE user_id=? AND unit_id=?`, userID, r.ContentID).Scan(&owned); err != nil {
			return nil, fmt.Errorf("cn35store: 礼物单位归属读取失败: %w", err)
		}
		var dup int
		if owned != 0 {
			if err := tx.QueryRow(`SELECT duplicated_count FROM cn35_player_units WHERE user_id=? AND unit_id=?`, userID, r.ContentID).Scan(&dup); err != nil {
				return nil, err
			}
		}
		copies := r.Amount
		if owned == 0 {
			copies--
		}
		if copies > int64(maxDup-dup) {
			return nil, cn35game.ErrPresentUnitCap
		}
		if owned == 0 {
			if err := settleUnitCopyTx(tx, userID, int(r.ContentID), 0, true); err != nil {
				return nil, err
			}
			if r.Amount > 1 {
				if err := settleUnitCopyTx(tx, userID, int(r.ContentID), int(r.Amount)-1, false); err != nil {
					return nil, err
				}
			}
		} else if err := settleUnitCopyTx(tx, userID, int(r.ContentID), int(r.Amount), false); err != nil {
			return nil, err
		}
	case "monster":
		for i := int64(0); i < r.Amount; i++ {
			uuid := "mon" + randHex(16)
			if err := InsertMonsterTx(tx, userID, cn35game.MonsterRecord{
				UUID: uuid, MonsterID: int(r.ContentID), Level: 1,
			}); err != nil {
				return nil, fmt.Errorf("cn35store: 礼物魔宠 %d 入库失败: %w", r.ContentID, err)
			}
			g.MonsterUUIDs = append(g.MonsterUUIDs, uuid)
		}
	case "coin", "gacha_point", "medal":
		if err := creditBalanceTx(tx, userID, r.ContentType, r.Amount); err != nil {
			return nil, err
		}
	case "item":
		key, err := itemKeyByContentTx(tx, int(r.ContentID))
		if err != nil {
			return nil, cn35game.ErrPresentUnsupported
		}
		if err := creditItemTx(tx, userID, key, int(r.Amount)); err != nil {
			return nil, err
		}
	default:
		return nil, cn35game.ErrPresentUnsupported
	}
	return g, nil
}

// presentUnitCaps 在事务外读取模板，避免目录实现嵌套获取数据库连接。
func presentUnitCaps(records []cn35game.PresentRecord, cat cn35game.UnitCatalog) map[int]int {
	caps := map[int]int{}
	for _, r := range records {
		if r.ContentType == "unit" {
			if u, err := cat.Unit(int(r.ContentID)); err == nil && u != nil {
				caps[int(r.ContentID)] = statmodel.MaxDuplicatedCount(u.Rare)
			}
		}
	}
	return caps
}
