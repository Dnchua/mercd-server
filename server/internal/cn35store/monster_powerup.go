// monster_powerup.go 魔宠强化写面（/user_monsters/powerup）。
//
// 客户端契约（MonsterPowerupModal.cs:184）：
//
//	/user_monsters/powerup?uuid=<uuid>&powerup_item_id=<素材 content_id>&count=<n>
//
// 语义（数值体系 §6）：
//   - 每次强化消耗 powerup_item_cnt 个素材——老魔宠成本恒 0 点/1 件
//     （MonsterPowerupModal.cs:54-75）；新魔宠（is_rare=1）的成本由
//     rare_monster_category_config（powerup_item_cnt + gacha_points）定义，
//     该表未取证/未下发前拒绝稀有魔宠强化（ErrMonsterRarePowerup），
//     不得按老魔宠 1 件/次的口径错价成交。
//   - Size 推进步长为客户端常量：普通魔宠 +0.02/次、新魔宠 +0.01/次
//     （ScaleCategory.cs），与 statmodel.ScaleAfterPowerup 同口径。
//   - 素材必须与魔宠属性（element=attr_id）和当前体型档位匹配：
//     ≤1.35 种子（form=1）/ ≤1.55 花蕾（form=2）/ ≤1.70 花（form=3）
//     （ScaleCategory 构造器），不符拒绝（ErrMonsterPowerupItem）。
//   - 普通魔宠上限边界为客户端常量 scaleTable[2]=1.70：cur>1.70
//     （id==4，含战斗掉落超限个体）拒绝（ErrMonsterScaleCap）；
//     边界内允许最后一步越界（客户端 maxItemForNextCategory
//     按 while scale<=boundary 计数），只执行/只扣可执行步数的素材。
//     scale_limit 列仅对新魔宠生效（isMaxScale 仅 newMonster 读它），
//     普通魔宠忽略该列。
//
// 幂等：上行无稳定请求标识，并发同参数请求经 dedupe 在途去重
// （与 AwakenUnit 同款），后到者复用首个结果 Duplicate=true。
//
// 事务约定与 monster_ops.go 一致：校验/扣物品/写 Size 全在单 withTx 内。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// MonsterPowerup 见 cn35game.MonsterOpsStore。
func (s *Store) MonsterPowerup(userID, uuid string, attrID, powerupItemID, count int) (*cn35game.MonsterPowerupResult, error) {
	key := fmt.Sprintf("%s|monster_powerup|%s|%d|%d", userID, uuid, powerupItemID, count)
	v, dup, err := s.dedupe(key, func() (any, error) {
		return s.monsterPowerup(userID, uuid, attrID, powerupItemID, count)
	})
	if err != nil {
		return nil, err
	}
	res := *v.(*cn35game.MonsterPowerupResult)
	if dup {
		res.Duplicate = true
	}
	return &res, nil
}

// normalScaleBoundM 普通魔宠体型档位的客户端常量边界
// （ScaleCategory.scaleTable[2]=1.70，千分比整型）。
const normalScaleBoundM = 1700

func (s *Store) monsterPowerup(userID, uuid string, attrID, powerupItemID, count int) (*cn35game.MonsterPowerupResult, error) {
	res := &cn35game.MonsterPowerupResult{UUID: uuid}
	err := s.withTx(func(tx *sql.Tx) error {
		var monsterID int
		var scale, realScale, scaleLimit sql.NullString
		var isRare sql.NullInt64
		err := tx.QueryRow(`SELECT monster_id,scale,real_scale,scale_limit,is_rare
			FROM cn35_player_monsters WHERE user_id=? AND uuid=?`,
			userID, uuid).Scan(&monsterID, &scale, &realScale, &scaleLimit, &isRare)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrMonsterNotOwned
		}
		if err != nil {
			return err
		}
		// powerup_item_id 是素材 content_id（PowerupItem.cs:52-66，1..15）；
		// element/form 用于档位校验。
		var itemKey string
		var elem, form int
		err = tx.QueryRow(`SELECT item_key,element,form FROM cn35_item_catalog
			WHERE content_id=? AND kind='material' AND enabled=1`,
			powerupItemID).Scan(&itemKey, &elem, &form)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrItemNotFound
		}
		if err != nil {
			return err
		}
		cur, err := parseScale(realScale, scale)
		if err != nil {
			return fmt.Errorf("cn35store: 魔宠 %s real_scale 非法: %w", uuid, err)
		}
		curM := int(math.Round(cur * 1000))
		if isRare.Valid && isRare.Int64 == 1 {
			if !scaleLimit.Valid && cn35game.MonsterBatch(monsterID) < 0 {
				return cn35game.ErrMonsterRarePowerup
			}
			limitF := cn35game.DefaultFusionMonsterScaleLimit(monsterID)
			if scaleLimit.Valid && scaleLimit.String != "" {
				if lf, lerr := strconv.ParseFloat(scaleLimit.String, 64); lerr == nil && lf > 0 {
					limitF = lf
				}
			}
			limitM := int(math.Round(limitF * 1000))
			if curM >= limitM || curM >= 2300 {
				return cn35game.ErrMonsterScaleCap
			}
			var wantForm, boundM, itemPerStep int
			var goldPerStep int64
			switch {
			case curM <= 1549:
				wantForm, boundM, itemPerStep, goldPerStep = 1, 1549, 2, 1000
			case curM <= 1699:
				wantForm, boundM, itemPerStep, goldPerStep = 2, 1699, 2, 2000
			case curM <= 1799:
				wantForm, boundM, itemPerStep, goldPerStep = 2, 1799, 2, 5000
			case curM <= 1899:
				wantForm, boundM, itemPerStep, goldPerStep = 3, 1899, 3, 10000
			case curM <= 2099:
				wantForm, boundM, itemPerStep, goldPerStep = 3, 2099, 4, 20000
			case curM <= 2299:
				wantForm, boundM, itemPerStep, goldPerStep = 3, 2299, 5, 50000
			default:
				return cn35game.ErrMonsterScaleCap
			}
			if elem != attrID || form != wantForm {
				return cn35game.ErrMonsterPowerupItem
			}
			const rareStepM = 10 // 融合宠 +0.01/次（ScaleCategory.SCALE_INCREASE_AMOUNT_New）
			maxCatSteps := (boundM - curM + rareStepM - 1) / rareStepM
			if maxCatSteps < 1 {
				maxCatSteps = 1
			}
			maxLimitSteps := (limitM - curM + rareStepM - 1) / rareStepM
			if maxLimitSteps < 1 {
				maxLimitSteps = 1
			}
			maxSteps := maxCatSteps
			if maxLimitSteps < maxSteps {
				maxSteps = maxLimitSteps
			}
			// MonsterPowerupModal.cs:32,65,184 将 count 作为消耗素材总件数（count = steps * itemCostSingle）上报；
			// 若调用方传入的是总素材件数（count >= itemPerStep），按 count / itemPerStep 换算步数。
			reqSteps := count / itemPerStep
			if reqSteps < 1 {
				reqSteps = 1
			}
			exec := reqSteps
			if exec > maxSteps {
				exec = maxSteps
			}
			totalItems := exec * itemPerStep
			totalGold := int64(exec) * goldPerStep
			if err := debitItemTx(tx, userID, itemKey, totalItems); err != nil {
				return err
			}
			if totalGold > 0 {
				gRes, gerr := tx.Exec(`UPDATE cn35_player_balances
					SET amount=amount-?, updated_at=?
					WHERE user_id=? AND currency='gacha_point' AND amount>=?`,
					totalGold, nowUTC(), userID, totalGold)
				if gerr != nil {
					return gerr
				}
				if n, _ := gRes.RowsAffected(); n != 1 {
					return cn35game.ErrCurrencyInsufficient
				}
			}
			next := cur + 0.01*float64(exec)
			str := formatScale(next)
			up, err := tx.Exec(`UPDATE cn35_player_monsters
				SET scale=?,real_scale=?,updated_at=? WHERE user_id=? AND uuid=?`,
				str, str, nowUTC(), userID, uuid)
			if err != nil {
				return err
			}
			if n, _ := up.RowsAffected(); n != 1 {
				return cn35game.ErrMonsterNotOwned
			}
			res.Scale, res.RealScale = str, str
			res.ChargedCount = totalItems
			return nil
		}
		// 千分比整型算术，避免十进制→二进制浮点误差吃掉可执行步数。
		const stepM = 20 // 普通魔宠 0.02/次
		// 档位→所需素材档位（ScaleCategory 构造器同口径）：
		// ≤1.35 种子 / ≤1.55 花蕾 / ≤1.70 花；>1.70 id==4 封顶
		// （战斗掉落超限个体天然不可强化）。
		var wantForm int
		switch {
		case curM <= 1350:
			wantForm = 1
		case curM <= 1550:
			wantForm = 2
		case curM <= normalScaleBoundM:
			wantForm = 3
		default:
			return cn35game.ErrMonsterScaleCap
		}
		if elem != attrID || form != wantForm {
			return cn35game.ErrMonsterPowerupItem
		}
		// 客户端 maxItemForNextCategory 按 while scale<=boundary 计数，
		// 允许最后一步越过边界（如 1.70→1.72 后 id=4 封顶）。
		maxSteps := (normalScaleBoundM-curM)/stepM + 1
		exec := count
		if exec > maxSteps {
			exec = maxSteps
		}
		if err := debitItemTx(tx, userID, itemKey, exec); err != nil {
			return err
		}
		next := cur + 0.02*float64(exec)
		str := formatScale(next)
		up, err := tx.Exec(`UPDATE cn35_player_monsters
			SET scale=?,real_scale=?,updated_at=? WHERE user_id=? AND uuid=?`,
			str, str, nowUTC(), userID, uuid)
		if err != nil {
			return err
		}
		if n, _ := up.RowsAffected(); n != 1 {
			return cn35game.ErrMonsterNotOwned
		}
		res.Scale, res.RealScale = str, str
		res.ChargedCount = exec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// parseScale 取有效推进 Size：real_scale → scale → 显式降级 1.000
// （与 inventory_wire effectiveScale 同口径）。
func parseScale(realScale, scale sql.NullString) (float64, error) {
	for _, s := range []sql.NullString{realScale, scale} {
		if s.Valid && s.String != "" {
			v, err := strconv.ParseFloat(s.String, 64)
			if err != nil {
				return 0, err
			}
			return v, nil
		}
	}
	return 1.0, nil
}

// formatScale 与读面同形态：三位小数十进制字符串（如 "1.000"/"1.020"）。
func formatScale(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}
