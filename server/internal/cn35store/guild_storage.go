// guild_storage.go 公会 BP 蓄水库与六类设施持久化（GUILDCLOSE350 GC-03/GC-04）。
//
// 迁移：0038_cn35_guild_bp_storage.sql（余额+流水）、
// 0039_cn35_guild_equip_invest.sql（投资台账扩列）。
//
// 事务纪律（docs/后端数据库与Go工作流.md）：
//   - 每条写面在 withTx 内完成「幂等判重 + 前置校验 + 跨域写入」；
//   - 蓄水库余额与玩家 BP 都用**条件 UPDATE**（RowsAffected==0 → 并发拒绝），
//     两者与幂等记录、流水同一事务提交；
//   - 读面（guildEquipmentsTx）把设施等级由 point 现算，不信任可能落后的
//     level 列（0039 之前的老库 level 按占位规则写入，升级后由本函数纠正）。
//
// 数值口径（重建规则，非原服公式）见 cn35game/guild_storage_wire.go 头注释与
// artifacts/tasks/cn350-guild-live-closure-20261028/run/evidence-guild-storage.md。
package cn35store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var _ cn35game.GuildStorageStore = (*Store)(nil)

// bpStorageRatio 蓄水填充比例：3 位小数（原服 wire value2=0.656 口径）。
func bpStorageRatio(amount, capacity int64) float64 {
	if capacity <= 0 {
		return 0
	}
	return math.Round(float64(amount)/float64(capacity)*1000) / 1000
}

// guildEquipPointTx 读某类设施的累计 point（无行=0）。
func guildEquipPointTx(tx *sql.Tx, guildID, typeID string) (int64, error) {
	var point int64
	err := tx.QueryRow(`SELECT point FROM cn35_guild_equipment
		WHERE guild_id=? AND type_id=?`, guildID, typeID).Scan(&point)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return point, err
}

// bpStorageStatusTx 读公会蓄水库状态（等级/上限/余额/比例），在事务内一致读。
func bpStorageStatusTx(tx *sql.Tx, guildID string) (*cn35game.GuildBPStorage, error) {
	point, err := guildEquipPointTx(tx, guildID, "bp_storage")
	if err != nil {
		return nil, err
	}
	level := cn35game.GuildEquipLevel(point)
	capacity := cn35game.GuildBPStorageCapacity(level)
	var amount int64
	err = tx.QueryRow(`SELECT amount FROM cn35_guild_bp_storage WHERE guild_id=?`,
		guildID).Scan(&amount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return &cn35game.GuildBPStorage{
		GuildID:  guildID,
		Amount:   amount,
		Capacity: capacity,
		Level:    level,
		Ratio:    bpStorageRatio(amount, capacity),
	}, nil
}

// guildEquipmentsTx 组装公会六类设施读面投影（/guilds/ 与 /guilds/show 共用）。
//
// Level 由 point 反解（GuildEquipLevel），UntilPoint/Value/Value2 由重建曲线
// 现算；bp_storage 的 value=蓄水上限、value2=当前填充比例。
// temporary 不参与投资，值为零值（wire 层渲染为全 null）。
func guildEquipmentsTx(tx *sql.Tx, guildID string) (map[string]*cn35game.GuildEquipment, error) {
	rows, err := tx.Query(`SELECT type_id,point FROM cn35_guild_equipment
		WHERE guild_id=?`, guildID)
	if err != nil {
		return nil, err
	}
	points := map[string]int64{}
	for rows.Next() {
		var typeID string
		var point int64
		if err = rows.Scan(&typeID, &point); err != nil {
			rows.Close()
			return nil, err
		}
		points[typeID] = point
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	storage, err := bpStorageStatusTx(tx, guildID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*cn35game.GuildEquipment, len(cn35game.GuildEquipmentTypeIDs))
	for _, typeID := range cn35game.GuildEquipmentTypeIDs {
		point := points[typeID]
		level := cn35game.GuildEquipLevel(point)
		e := &cn35game.GuildEquipment{TypeID: typeID, Point: point, Level: level}
		if typeID == "temporary" {
			e.Level = 0
			out[typeID] = e
			continue
		}
		e.UntilPoint = cn35game.GuildEquipUntilPoint(level)
		e.End = level >= cn35game.GuildEquipMaxLevel
		value, value2, hasValue2 := cn35game.GuildEquipValue(typeID, level)
		e.Value, e.Value2, e.HasValue2 = value, value2, hasValue2
		if typeID == "bp_storage" {
			// 蓄水上限随设施等级，填充比例随公会共享余额。
			e.Value = float64(storage.Capacity)
			e.Value2 = storage.Ratio
			e.HasValue2 = true
		}
		out[typeID] = e
	}
	return out, nil
}

// profileBPTx 读玩家 BP 与上限。
func profileBPTx(tx *sql.Tx, userID string) (bp, maxBP int64, err error) {
	err = tx.QueryRow(`SELECT bp,max_bp FROM cn35_player_profiles WHERE user_id=?`,
		userID).Scan(&bp, &maxBP)
	return bp, maxBP, err
}

// BPStorageStatus 蓄水库确认读面：/guilds/bp_storage。
//
// 返回 (状态, 请求方当前是否可取)。可取判据：bp<max_bp 且余额>0——原服拒绝态
// 出现在余额 16,761/49,000 时，故门禁必然包含请求方 BP 状态（证据 §2）。
func (s *Store) BPStorageStatus(userID string) (*cn35game.GuildBPStorage, bool, error) {
	var rec *cn35game.GuildBPStorage
	var canTake bool
	err := s.withTx(func(tx *sql.Tx) error {
		guildID, _, err := memberRoleTx(tx, userID)
		if err != nil {
			return err
		}
		if guildID == "" {
			return cn35game.ErrGuildNoMember
		}
		if err := refillGuildTx(tx, guildID, s.clockNowUTC()); err != nil {
			return err
		}
		rec, err = bpStorageStatusTx(tx, guildID)
		if err != nil {
			return err
		}
		bp, maxBP, err := profileBPTx(tx, userID)
		if err != nil {
			return err
		}
		canTake = rec.CanTake(bp, maxBP)
		return nil
	})
	return rec, canTake, err
}

// takeReplayAmountTx 幂等重放时回放原取出量（流水按 user_id+op_key 命中）。
func takeReplayAmountTx(tx *sql.Tx, userID, opKey string) (int64, error) {
	var amount int64
	err := tx.QueryRow(`SELECT amount FROM cn35_guild_bp_storage_log
		WHERE user_id=? AND op_key=? AND kind='take' ORDER BY log_id DESC LIMIT 1`,
		userID, opKey).Scan(&amount)
	if errors.Is(err, sql.ErrNoRows) {
		// 幂等键命中但流水缺失（极端不一致）：回 0 而不是编造数量。
		return 0, nil
	}
	return amount, err
}

// TakeoverBPStorage 蓄水库取出事务：/guilds/takeover_bp_storage。
//
// 规则（候选规则 A，已证一部分；见证据 §2）：取出量 = min(蓄水库余额,
// max_bp - bp)，即「把请求方 BP 补到上限，最多取空蓄水库」。
// 门禁：bp >= max_bp 或余额 <= 0 → ErrGuildBPStorageEmpty（原服渲染拒绝态）。
//
// 幂等：无客户端 op_key，缺省用「60 秒时间桶」语义键（与 Invest 同策略）；
// 显式键则按 op/payload 严格判重（同键异参 → ErrGuildOp）。
// 同键重放不重复扣、不重复回 BP，回放原取出量。
func (s *Store) TakeoverBPStorage(userID, opKey string) (*cn35game.GuildBPStorageTakeover, error) {
	bucket := s.clockNowUTC().Unix() / 60
	var out *cn35game.GuildBPStorageTakeover
	err := s.withTx(func(tx *sql.Tx) error {
		guildID, _, err := memberRoleTx(tx, userID)
		if err != nil {
			return err
		}
		if guildID == "" {
			return cn35game.ErrGuildNoMember
		}
		key := opKeyOr(opKey, fmt.Sprintf("bp_takeover:%d", bucket))
		if err = recordGuildOpTx(tx, userID, key, "bp_takeover", guildID); err != nil {
			if !errors.Is(err, errOpReplay) {
				return err
			}
			amount, rerr := takeReplayAmountTx(tx, userID, key)
			if rerr != nil {
				return rerr
			}
			rec, rerr := bpStorageStatusTx(tx, guildID)
			if rerr != nil {
				return rerr
			}
			out = &cn35game.GuildBPStorageTakeover{
				Amount: amount, Storage: rec, Replay: true,
			}
			return nil
		}
		rec, err := bpStorageStatusTx(tx, guildID)
		if err != nil {
			return err
		}
		bp, maxBP, err := profileBPTx(tx, userID)
		if err != nil {
			return err
		}
		if !rec.CanTake(bp, maxBP) {
			return cn35game.ErrGuildBPStorageEmpty
		}
		amount := maxBP - bp
		if amount > rec.Amount {
			amount = rec.Amount
		}
		if amount <= 0 {
			return cn35game.ErrGuildBPStorageEmpty
		}
		// 蓄水库余额条件扣减（共享池：并发/第三方同时取出时 RowsAffected=0）。
		res, err := tx.Exec(`UPDATE cn35_guild_bp_storage
			SET amount=amount-?,updated_at=?
			WHERE guild_id=? AND amount>=?`,
			amount, nowUTC(), guildID, amount)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrGuildBPStorageBusy
		}
		// 玩家 BP 条件入账（同事务；并发改动 bp 则整笔回滚）。
		res, err = tx.Exec(`UPDATE cn35_player_profiles
			SET bp=bp+?,updated_at=?
			WHERE user_id=? AND bp=?`,
			amount, nowUTC(), userID, bp)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return cn35game.ErrGuildBPStorageBusy
		}
		after := rec.Amount - amount
		if _, err = tx.Exec(`INSERT INTO cn35_guild_bp_storage_log
			(guild_id,user_id,kind,amount,amount_after,op_key,reason,created_at)
			VALUES (?,?,?,?,?,?,?,?)`,
			guildID, userID, "take", amount, after, key, "", nowUTC()); err != nil {
			return err
		}
		rec.Amount = after
		rec.Ratio = bpStorageRatio(after, rec.Capacity)
		out = &cn35game.GuildBPStorageTakeover{Amount: amount, Storage: rec}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DepositBPStorageTx 事务内蓄水库存入原语（供**其他域在自身事务里接线**）。
//
// 语义与 Store.DepositBPStorage 完全一致，只是不开新事务：调用方把「来源事件
// （BP 溢出/道具/奖励）+ 入池 + 流水 + 幂等键」放在同一个 *sql.Tx 里，满足
// 「跨域写入与幂等记录必须同事务」（docs/后端数据库与Go工作流.md）。
//
// 边界（Lead 2026-10-28 批准）：当前**不**在 login_bonus.go / items.go 等他人文件里
// 接线；本函数只提供接线点。补给来源仍是取证缺口 G2，见
// artifacts/.../run/evidence-guild-storage.md §5.2 / §9。
//
// 返回 (存入后余额, 实际存入量)；容量封顶、溢出部分丢弃；已满回 (当时余额, 0)
// 且不写流水（流水表 amount>0 的 CHECK 也不允许 0 行）。
func DepositBPStorageTx(tx *sql.Tx, guildID, userID string, amount int64, reason, opKey string) (
	after, deposited int64, err error) {
	if guildID == "" || amount <= 0 {
		return 0, 0, cn35game.ErrGuildInvest
	}
	// 缺省幂等键：调用方没给时按「公会+数量+60 秒时间桶」；跨域调用**必须**显式给键
	// （用来源事件自己的 id，例如溢出/发放流水号），否则同分钟同额存入会被判重放。
	key := opKeyOr(opKey, fmt.Sprintf("bp_deposit:%s:%d:%d",
		guildID, amount, time.Now().Unix()/60))
	if err = recordGuildOpTx(tx, userID, key, "bp_deposit",
		fmt.Sprintf("%s:%d", guildID, amount)); err != nil {
		if !errors.Is(err, errOpReplay) {
			return 0, 0, err
		}
		// 同键重放：回放原存入量，不重复入池。
		var prev int64
		rerr := tx.QueryRow(`SELECT amount FROM cn35_guild_bp_storage_log
			WHERE user_id=? AND op_key=? AND kind='deposit'
			ORDER BY log_id DESC LIMIT 1`, userID, key).Scan(&prev)
		if rerr != nil && !errors.Is(rerr, sql.ErrNoRows) {
			return 0, 0, rerr
		}
		st, rerr2 := bpStorageStatusTx(tx, guildID)
		if rerr2 != nil {
			return 0, 0, rerr2
		}
		return st.Amount, prev, nil
	}
	st, err := bpStorageStatusTx(tx, guildID)
	if err != nil {
		return 0, 0, err
	}
	room := st.Capacity - st.Amount
	if room <= 0 {
		return st.Amount, 0, nil
	}
	if amount > room {
		amount = room
	}
	res, err := tx.Exec(`UPDATE cn35_guild_bp_storage
		SET amount=amount+?,updated_at=?
		WHERE guild_id=? AND amount=?`,
		amount, nowUTC(), guildID, st.Amount)
	if err != nil {
		return 0, 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if st.Amount != 0 {
			return 0, 0, cn35game.ErrGuildBPStorageBusy
		}
		// 无行：首次存入建行（amount=0 时上面的条件 UPDATE 不会命中）。
		if _, err = tx.Exec(`INSERT INTO cn35_guild_bp_storage
			(guild_id,amount,updated_at) VALUES (?,?,?)
			ON CONFLICT(guild_id) DO UPDATE SET
			amount=excluded.amount,updated_at=excluded.updated_at`,
			guildID, amount, nowUTC()); err != nil {
			return 0, 0, err
		}
	}
	after = st.Amount + amount
	if _, err = tx.Exec(`INSERT INTO cn35_guild_bp_storage_log
		(guild_id,user_id,kind,amount,amount_after,op_key,reason,created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		guildID, userID, "deposit", amount, after, key, reason, nowUTC()); err != nil {
		return 0, 0, err
	}
	return after, amount, nil
}

// DepositBPStorage 蓄水库存入原语（自开事务版本，测试/运维工具用）。
//
// 容量封顶、溢出部分丢弃（原服补给来源与溢出处理未证，见证据 §5）；
// 返回存入后状态与实际存入量。当前**不接入任何发放链**：需要跨域接线时，
// 调用方在自己的事务里用 DepositBPStorageTx（同一语义，不加事务层次）。
func (s *Store) DepositBPStorage(guildID, userID string, amount int64, reason, opKey string) (
	*cn35game.GuildBPStorage, int64, error) {
	if guildID == "" || amount <= 0 {
		return nil, 0, cn35game.ErrGuildInvest
	}
	var rec *cn35game.GuildBPStorage
	var deposited int64
	err := s.withTx(func(tx *sql.Tx) error {
		if _, dep, derr := DepositBPStorageTx(tx, guildID, userID, amount, reason, opKey); derr != nil {
			return derr
		} else {
			deposited = dep
		}
		st, serr := bpStorageStatusTx(tx, guildID)
		if serr != nil {
			return serr
		}
		rec = st
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return rec, deposited, nil
}
