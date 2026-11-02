// guild_storage_test.go BP 蓄水库与六类设施持久化测试（GUILDCLOSE350 GC-03/GC-04）。
//
// 覆盖（docs/后端数据库与Go工作流.md 第 4 条）：
//   - 空库建到最新版（本包 TestEmptyDBCreatesLatestSchema 已含计数断言）；
//   - 真实上一版 schema（迁移到 0037）→ 升级到含 0038/0039 的最新版；
//   - 重启恢复（关库重开，余额/BP/流水保留）；
//   - 重复请求不重复扣（幂等键重放回原量）、同键异参拒绝（ErrGuildOp）；
//   - 约束失败/注入失败全回滚（BP 与蓄水库余额同事务）；
//   - 两账号隔离（不同公会互不影响）与并发（共享池不超扣、不为负）；
//   - 原服锚点回代：录包 guilds.json 的五类设施 point 直接喂进读面投影，
//     断言 level/until/value/value2 与原服逐字一致（曲线过锚点的机器证明）。
package cn35store

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

// seedBPStorage 直接写入公会蓄水库余额（等价于历史存量；无任何重置语义）。
func seedBPStorage(t *testing.T, s *Store, guildID string, amount int64) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO cn35_guild_bp_storage
		(guild_id,amount,updated_at) VALUES (?,?,?)
		ON CONFLICT(guild_id) DO UPDATE SET amount=excluded.amount`,
		guildID, amount, nowUTC()); err != nil {
		t.Fatal(err)
	}
}

// seedEquipPoint 直接写入设施累计 point（模拟历史投资存量，绕过投资流程）。
func seedEquipPoint(t *testing.T, s *Store, guildID, typeID string, point int64) {
	t.Helper()
	level := cn35game.GuildEquipLevel(point)
	if _, err := s.DB().Exec(`INSERT INTO cn35_guild_equipment
		(guild_id,type_id,level,point,updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(guild_id,type_id) DO UPDATE SET point=excluded.point`,
		guildID, typeID, level, point, nowUTC()); err != nil {
		t.Fatal(err)
	}
}

// setBP 设置玩家 BP/上限（跨域状态，测试直写）。
func setBP(t *testing.T, s *Store, userID string, bp, maxBP int64) {
	t.Helper()
	if _, err := s.DB().Exec(`UPDATE cn35_player_profiles SET bp=?,max_bp=?
		WHERE user_id=?`, bp, maxBP, userID); err != nil {
		t.Fatal(err)
	}
}

// bpOf 读玩家 BP。
func bpOf(t *testing.T, s *Store, userID string) int64 {
	t.Helper()
	var bp int64
	if err := s.DB().QueryRow(`SELECT bp FROM cn35_player_profiles WHERE user_id=?`,
		userID).Scan(&bp); err != nil {
		t.Fatal(err)
	}
	return bp
}

// storageAmountOf 读公会蓄水库余额。
func storageAmountOf(t *testing.T, s *Store, guildID string) int64 {
	t.Helper()
	var amount int64
	err := s.DB().QueryRow(`SELECT amount FROM cn35_guild_bp_storage WHERE guild_id=?`,
		guildID).Scan(&amount)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return amount
}

// bpStorageFixture 建会 + 蓄水库容量 + 请求方 BP 状态。
func bpStorageFixture(t *testing.T, s *Store, capacityPoint int64, pool, bp, maxBP int64) (string, string) {
	t.Helper()
	uid := guildUID(t, s, "bp")
	g, err := s.CreateGuild(uid, "蓄水会", "2", "1", "", "")
	if err != nil {
		t.Fatalf("CreateGuild: %v", err)
	}
	if capacityPoint > 0 {
		seedEquipPoint(t, s, g.ID, "bp_storage", capacityPoint)
	}
	seedBPStorage(t, s, g.ID, pool)
	setBP(t, s, uid, bp, maxBP)
	return uid, g.ID
}

func TestGuildBPStorageStatusAndTakeover(t *testing.T) {
	s := openStore(t, t.TempDir())
	// Lv.79 蓄水库：point 28,562,863（原服样本），上限 round100(620×79)=49,000。
	uid, gid := bpStorageFixture(t, s, 28562863, 47345, 0, 600)

	st, canTake, err := s.BPStorageStatus(uid)
	if err != nil || !canTake {
		t.Fatalf("status: %+v canTake=%v err=%v", st, canTake, err)
	}
	if st.Level != 79 || st.Capacity != 49000 || st.Amount != 47345 {
		t.Fatalf("蓄水库状态与原服样本不符: %+v", st)
	}
	// 原服 confirm 体：「蓄水库 (Lv.79)\n47,345/49,000」——比例 0.966。
	if st.Ratio != 0.966 {
		t.Fatalf("ratio=%v want 0.966", st.Ratio)
	}
	// 确认读面无副作用。
	if got := storageAmountOf(t, s, gid); got != 47345 {
		t.Fatalf("读面改了余额: %d", got)
	}

	// 取出：补满自身缺口 600（候选规则 A），蓄水库 47,345 → 46,745。
	res, err := s.TakeoverBPStorage(uid, "take-1")
	if err != nil {
		t.Fatalf("Takeover: %v", err)
	}
	if res.Amount != 600 || res.Replay {
		t.Fatalf("取出量应为 600（原服 32 条样本一致）: %+v", res)
	}
	if got := bpOf(t, s, uid); got != 600 {
		t.Fatalf("BP 应补到上限 600, got %d", got)
	}
	if got := storageAmountOf(t, s, gid); got != 46745 {
		t.Fatalf("蓄水库应 46,745, got %d", got)
	}
	// 原服样本里这正好是下一条 confirm 的余额（47,345→46,745 逐字一致）。
	if st2, ok, _ := s.BPStorageStatus(uid); ok || st2.Amount != 46745 {
		t.Fatalf("取满后应不可再取: %+v canTake=%v", st2, ok)
	}
}

func TestGuildBPStorageRefusals(t *testing.T) {
	s := openStore(t, t.TempDir())
	t.Run("BP已满", func(t *testing.T) {
		uid, gid := bpStorageFixture(t, s, 28562863, 16761, 600, 600)
		// 原服拒绝态就出现在 16,761/49,000：池子远未空，门禁是请求方 BP 满。
		st, canTake, err := s.BPStorageStatus(uid)
		if err != nil || canTake {
			t.Fatalf("BP 满时不可取: %+v canTake=%v err=%v", st, canTake, err)
		}
		if _, err = s.TakeoverBPStorage(uid, "take-full"); !errors.Is(err, cn35game.ErrGuildBPStorageEmpty) {
			t.Fatalf("应 ErrGuildBPStorageEmpty: %v", err)
		}
		if got := storageAmountOf(t, s, gid); got != 16761 {
			t.Fatalf("拒绝不应改余额: %d", got)
		}
		if got := bpOf(t, s, uid); got != 600 {
			t.Fatalf("拒绝不应改 BP: %d", got)
		}
	})
	t.Run("蓄水库空", func(t *testing.T) {
		uid, gid := bpStorageFixture(t, s, 28562863, 0, 0, 600)
		_, canTake, err := s.BPStorageStatus(uid)
		if err != nil || canTake {
			t.Fatalf("空池不可取: canTake=%v err=%v", canTake, err)
		}
		if _, err = s.TakeoverBPStorage(uid, "take-empty"); !errors.Is(err, cn35game.ErrGuildBPStorageEmpty) {
			t.Fatalf("应 ErrGuildBPStorageEmpty: %v", err)
		}
		if got := storageAmountOf(t, s, gid); got != 0 {
			t.Fatalf("拒绝不应建行/改余额: %d", got)
		}
	})
	t.Run("未入会", func(t *testing.T) {
		stranger := guildUID(t, s, "stranger")
		if _, _, err := s.BPStorageStatus(stranger); !errors.Is(err, cn35game.ErrGuildNoMember) {
			t.Fatalf("未入会应 ErrGuildNoMember: %v", err)
		}
		if _, err := s.TakeoverBPStorage(stranger, "take-x"); !errors.Is(err, cn35game.ErrGuildNoMember) {
			t.Fatalf("未入会应 ErrGuildNoMember: %v", err)
		}
	})
}

func TestGuildBPStorageTakeCappedByPool(t *testing.T) {
	s := openStore(t, t.TempDir())
	// 缺口 600 但池子只有 200：取出 200（取空），BP 只到 200。
	uid, gid := bpStorageFixture(t, s, 28562863, 200, 0, 600)
	res, err := s.TakeoverBPStorage(uid, "take-capped")
	if err != nil || res.Amount != 200 {
		t.Fatalf("应取出 200: %+v %v", res, err)
	}
	if got := storageAmountOf(t, s, gid); got != 0 {
		t.Fatalf("池子应取空: %d", got)
	}
	if got := bpOf(t, s, uid); got != 200 {
		t.Fatalf("BP 应为 200: %d", got)
	}
	// 已取空后再取 → 拒绝。
	if _, err = s.TakeoverBPStorage(uid, "take-capped-2"); !errors.Is(err, cn35game.ErrGuildBPStorageEmpty) {
		t.Fatalf("空池应拒绝: %v", err)
	}
}

func TestGuildBPStorageIdempotentReplay(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid, gid := bpStorageFixture(t, s, 28562863, 47345, 0, 600)
	first, err := s.TakeoverBPStorage(uid, "same-key")
	if err != nil {
		t.Fatal(err)
	}
	// 同键重放：不重复扣池、不重复回 BP，回放原量。
	second, err := s.TakeoverBPStorage(uid, "same-key")
	if err != nil {
		t.Fatalf("重放不应报错: %v", err)
	}
	if !second.Replay || second.Amount != first.Amount {
		t.Fatalf("重放应回放原量: %+v", second)
	}
	if got := storageAmountOf(t, s, gid); got != 47345-600 {
		t.Fatalf("重放重复扣池: %d", got)
	}
	if got := bpOf(t, s, uid); got != 600 {
		t.Fatalf("重放重复回 BP: %d", got)
	}
	var logs int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_bp_storage_log
		WHERE user_id=? AND op_key='same-key'`, uid).Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if logs != 1 {
		t.Fatalf("同键只应一条流水: %d", logs)
	}
}

func TestGuildBPStorageSameKeyDifferentParamRejected(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid, gid := bpStorageFixture(t, s, 28562863, 47345, 0, 600)
	if _, err := s.TakeoverBPStorage(uid, "shared-key"); err != nil {
		t.Fatal(err)
	}
	// 同键换个 op → 显式键冲突拒绝，不得静默成功。
	if _, err := s.Invest(uid, "hp", 1, "shared-key"); !errors.Is(err, cn35game.ErrGuildOp) {
		t.Fatalf("同键异 op 应 ErrGuildOp: %v", err)
	}
	// 同键换成存入（同 op 家族不同 payload 也不算同参）。
	creditBalance(t, s, uid, "guild_asset", 100)
	if _, _, err := s.DepositBPStorage(gid, uid, 100, "", "shared-key"); !errors.Is(err, cn35game.ErrGuildOp) {
		t.Fatalf("同键异参应 ErrGuildOp: %v", err)
	}
}

func TestGuildBPStorageRollbackOnConstraintFailure(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid, gid := bpStorageFixture(t, s, 28562863, 47345, 0, 600)
	// 用触发器在流水写入处注入失败：BP 与蓄水库余额必须整笔回滚。
	if _, err := s.DB().Exec(`CREATE TRIGGER t3_fail_bp_log
		BEFORE INSERT ON cn35_guild_bp_storage_log
		BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TakeoverBPStorage(uid, "take-boom"); err == nil {
		t.Fatal("注入失败后不应成功")
	}
	if got := storageAmountOf(t, s, gid); got != 47345 {
		t.Fatalf("失败后蓄水库应回滚到 47345, got %d", got)
	}
	if got := bpOf(t, s, uid); got != 0 {
		t.Fatalf("失败后 BP 应回滚到 0, got %d", got)
	}
	var ops int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_ops WHERE op_key='take-boom'`).Scan(&ops); err != nil {
		t.Fatal(err)
	}
	if ops != 0 {
		t.Fatalf("失败后幂等键也应回滚: %d", ops)
	}
	if _, err := s.DB().Exec(`DROP TRIGGER t3_fail_bp_log`); err != nil {
		t.Fatal(err)
	}
	// 去掉故障后同键可重新执行（上次没留痕）。
	if res, err := s.TakeoverBPStorage(uid, "take-boom"); err != nil || res.Amount != 600 {
		t.Fatalf("恢复后应可取出: %+v %v", res, err)
	}
}

func TestGuildBPStorageRestartPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	uid := guildUID(t, s, "restart")
	g, err := s.CreateGuild(uid, "重启会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	seedEquipPoint(t, s, g.ID, "bp_storage", 28562863)
	seedBPStorage(t, s, g.ID, 47345)
	setBP(t, s, uid, 0, 600)
	if _, err = s.TakeoverBPStorage(uid, "take-restart"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	// 重启：同目录重开，余额/BP/流水保留。
	s2 := openStore(t, dir)
	if got := storageAmountOf(t, s2, g.ID); got != 46745 {
		t.Fatalf("重启后蓄水库 = %d, want 46745", got)
	}
	if got := bpOf(t, s2, uid); got != 600 {
		t.Fatalf("重启后 BP = %d, want 600", got)
	}
	if _, _, err = s2.BPStorageStatus(uid); err != nil {
		t.Fatalf("重启后读面失败: %v", err)
	}
}

func TestGuildBPStorageTwoGuildsIsolated(t *testing.T) {
	s := openStore(t, t.TempDir())
	uidA, gidA := bpStorageFixture(t, s, 28562863, 1000, 0, 600)
	uidB := guildUID(t, s, "bp-b")
	gB, err := s.CreateGuild(uidB, "另一会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	seedEquipPoint(t, s, gB.ID, "bp_storage", 28562863)
	seedBPStorage(t, s, gB.ID, 2000)
	setBP(t, s, uidB, 0, 600)

	if _, err = s.TakeoverBPStorage(uidA, "take-a"); err != nil {
		t.Fatal(err)
	}
	// 两账号完全隔离：B 的池子/BP 不受影响。
	if got := storageAmountOf(t, s, gidA); got != 400 {
		t.Fatalf("A 池 = %d, want 400", got)
	}
	if got := storageAmountOf(t, s, gB.ID); got != 2000 {
		t.Fatalf("B 池被 A 影响: %d", got)
	}
	if got := bpOf(t, s, uidB); got != 0 {
		t.Fatalf("B 的 BP 被 A 影响: %d", got)
	}
	// 玩家不能取别的公会的池子：B 取自己会 → 600，池 1400。
	if res, err := s.TakeoverBPStorage(uidB, "take-b"); err != nil || res.Amount != 600 {
		t.Fatalf("B 取出: %+v %v", res, err)
	}
	if got := storageAmountOf(t, s, gB.ID); got != 1400 {
		t.Fatalf("B 池 = %d, want 1400", got)
	}
}

func TestGuildBPStorageConcurrentSharedPool(t *testing.T) {
	s := openStore(t, t.TempDir())
	// 同一公会两名成员共享池 700：缺口各 600，合计不得超过池子且不得为负。
	master := guildUID(t, s, "c-master")
	g, err := s.CreateGuild(master, "并发会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	seedEquipPoint(t, s, g.ID, "bp_storage", 28562863)
	seedBPStorage(t, s, g.ID, 700)
	setBP(t, s, master, 0, 600)
	member := guildUID(t, s, "c-member")
	if _, err = s.JoinGuild(member, g.ID, "join-member"); err != nil {
		t.Fatal(err)
	}
	setBP(t, s, member, 0, 600)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var amounts []int64
	var errs []error
	for i, uid := range []string{master, member} {
		wg.Add(1)
		go func(i int, uid string) {
			defer wg.Done()
			res, err := s.TakeoverBPStorage(uid, fmt.Sprintf("conc-%d", i))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			amounts = append(amounts, res.Amount)
		}(i, uid)
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("并发取出现意外错误: %v", errs)
	}
	var sum int64
	for _, a := range amounts {
		sum += a
	}
	if sum != 700 {
		t.Fatalf("并发取出合计 = %d, want 700（不得超扣）", sum)
	}
	// 每人各自拿到的量 = 请求时的缺口与池子余量的较小者；合计守恒即可。
	if got := storageAmountOf(t, s, g.ID); got != 0 {
		t.Fatalf("池子应取空, got %d", got)
	}
	for _, uid := range []string{master, member} {
		if got := bpOf(t, s, uid); got < 0 || got > 600 {
			t.Fatalf("BP 越界: %d", got)
		}
	}
}

// TestGuildBPStorageFreshGuildInitialState 新建公会的蓄水库初始状态。
//
// 原服补给来源未证（证据 §5.2 / 缺口 G2），因此重建规则明确为：
//   - 蓄水库**无行即 0**，新建公会不注入任何 BP；
//   - 容量由设施等级推导：Lv.1 → round100(620×1) = 600；
//   - 取出门禁因此为「不可取」，直到有来源（测试/运维用 deposit 原语注入）。
//
// 这条测试把「没有编造的初始补给」变成可执行断言：谁要改初始量，必须先过这里。
func TestGuildBPStorageFreshGuildInitialState(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := guildUID(t, s, "fresh")
	g, err := s.CreateGuild(uid, "新会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	setBP(t, s, uid, 0, 600)
	st, canTake, err := s.BPStorageStatus(uid)
	if err != nil {
		t.Fatal(err)
	}
	if st.Level != 1 || st.Capacity != 600 || st.Amount != 0 || st.Ratio != 0 {
		t.Fatalf("新建公会蓄水库初值不符: %+v", st)
	}
	if canTake {
		t.Fatal("新建公会蓄水库为空，不应可取出（不得编造初始补给）")
	}
	if _, err = s.TakeoverBPStorage(uid, "fresh-take"); !errors.Is(err, cn35game.ErrGuildBPStorageEmpty) {
		t.Fatalf("空池应拒绝: %v", err)
	}
	// 无行即 0：确认没有在磁盘上留下金额行（避免「隐式初始化」）。
	var rows int
	if err = s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_bp_storage WHERE guild_id=?`,
		g.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("新建公会不应建蓄水库行: %d", rows)
	}
}

// TestDepositBPStorageTxCrossDomain 跨域接线点：调用方在**自己的事务**里入池，
// 与来源事件同事务提交/回滚（Lead 2026-10-28 批准的实施边界）。
func TestDepositBPStorageTxCrossDomain(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid, gid := bpStorageFixture(t, s, 28562863, 47000, 0, 600)

	// 正常接线：同事务内两笔存入（不同显式键），一次提交。
	err := s.WithTx(func(tx *sql.Tx) error {
		if _, _, err := DepositBPStorageTx(tx, gid, uid, 600, "overflow-1", "evt-1"); err != nil {
			return err
		}
		_, _, err := DepositBPStorageTx(tx, gid, uid, 600, "overflow-2", "evt-2")
		return err
	})
	if err != nil {
		t.Fatalf("跨域接线失败: %v", err)
	}
	// 容量 49,000、原 47,000 → 只吃得下 2,000，两笔合计 1,200 全部吃下。
	if got := storageAmountOf(t, s, gid); got != 48200 {
		t.Fatalf("入池后余额 = %d, want 48200", got)
	}

	// 来源事务回滚：入池必须一起回滚（不得出现「来源失败但池子涨了」）。
	rollbackErr := errors.New("source rollback")
	err = s.WithTx(func(tx *sql.Tx) error {
		if _, _, err := DepositBPStorageTx(tx, gid, uid, 500, "overflow-3", "evt-3"); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("应返回来源错误: %v", err)
	}
	if got := storageAmountOf(t, s, gid); got != 48200 {
		t.Fatalf("来源回滚后余额应不变, got %d", got)
	}
	// 幂等：同键重放不再入池。
	err = s.WithTx(func(tx *sql.Tx) error {
		_, _, err := DepositBPStorageTx(tx, gid, uid, 600, "overflow-1", "evt-1")
		return err
	})
	if err != nil {
		t.Fatalf("同键重放不应报错: %v", err)
	}
	if got := storageAmountOf(t, s, gid); got != 48200 {
		t.Fatalf("同键重放重复入池: %d", got)
	}
}

func TestGuildBPStorageDeposit(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid, gid := bpStorageFixture(t, s, 28562863, 48000, 0, 600)
	// 容量 49,000：可存 1,000，超出部分丢弃（重建规则）。
	st, deposited, err := s.DepositBPStorage(gid, uid, 1500, "test-overflow", "dep-1")
	if err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	if deposited != 1000 || st.Amount != 49000 {
		t.Fatalf("应存满到 49,000 且丢弃溢出: deposited=%d st=%+v", deposited, st)
	}
	// 已满再存：回 0，不写流水（amount>0 的 CHECK 约束也不允许 0 流水）。
	_, deposited, err = s.DepositBPStorage(gid, uid, 500, "test-full", "dep-2")
	if err != nil || deposited != 0 {
		t.Fatalf("满库应存入 0: deposited=%d err=%v", deposited, err)
	}
	var logs int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_bp_storage_log
		WHERE guild_id=? AND kind='deposit'`, gid).Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if logs != 1 {
		t.Fatalf("只应有 1 条存入流水: %d", logs)
	}
	// 幂等重放：同键同参回放原量，不重复存。
	_, deposited, err = s.DepositBPStorage(gid, uid, 1500, "test-overflow", "dep-1")
	if err != nil || deposited != 1000 {
		t.Fatalf("重放应回放 1000: deposited=%d err=%v", deposited, err)
	}
	if got := storageAmountOf(t, s, gid); got != 49000 {
		t.Fatalf("重放不应改变余额: %d", got)
	}
}

// TestGuildEquipAnchorProjection 原服锚点回代：把录包 guilds.json 里五类设施
// 的 point 直接写进库，断言读面投影的 level/until/value/value2/effect 与原服
// 逐字一致。这是「曲线过锚点」的机器证明，避免曲线改动悄悄偏离证据。
//
// 证据：artifacts/mlk-capture-201711/decrypted/1/guilds.json
//
//	sha256 c8941ed5fbc059c93e23957af711f4a65e916351feb915be15cc1c594b1b6d61
//
//	data.guild.equipments.
func TestGuildEquipAnchorProjection(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := guildUID(t, s, "anchor")
	g, err := s.CreateGuild(uid, "锚点会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	anchors := []struct {
		typeID string
		point  int64
		level  int
		until  int64
		value  float64
		value2 float64
		effect string
	}{
		{"sp", 64779050, 88, 70547000, 2200, -1, "初始SP值+2200"},
		{"hp", 27766631, 79, 29928300, 19.75, -1, "体力+19.75%"},
		{"at", 44388282, 84, 48190100, 21, -1, "攻击力+21%"},
		{"taghness", 11548526, 70, 12700000, 28000, 3, "体力 +28000 Guts 3"},
	}
	for _, a := range anchors {
		seedEquipPoint(t, s, g.ID, a.typeID, a.point)
	}
	// 蓄水库 Lv.79：上限 49,000；余额 32,144 时比例 0.656（原服 value2）。
	seedEquipPoint(t, s, g.ID, "bp_storage", 28562863)
	seedBPStorage(t, s, g.ID, 32144)

	view, err := s.MyGuild(uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range anchors {
		e := view.Equipments[a.typeID]
		if e == nil {
			t.Fatalf("%s 缺失", a.typeID)
		}
		if e.Level != a.level || e.UntilPoint != a.until || e.Point != a.point {
			t.Fatalf("%s level/until/point = %d/%d/%d, want %d/%d/%d",
				a.typeID, e.Level, e.UntilPoint, e.Point, a.level, a.until, a.point)
		}
		if e.Value != a.value {
			t.Fatalf("%s value = %v, want %v", a.typeID, e.Value, a.value)
		}
		if a.value2 >= 0 && e.Value2 != a.value2 {
			t.Fatalf("%s value2 = %v, want %v", a.typeID, e.Value2, a.value2)
		}
		if got := cn35game.GuildEquipEffect(a.typeID, e.Level, e.Value, e.Value2); got != a.effect {
			t.Fatalf("%s effect = %q, want %q", a.typeID, got, a.effect)
		}
	}
	// 蓄水库：value=上限、value2=填充比例（3 位小数）。
	bp := view.Equipments["bp_storage"]
	if bp.Level != 79 || bp.UntilPoint != 29928300 || bp.Value != 49000 {
		t.Fatalf("bp_storage: %+v", bp)
	}
	if bp.Value2 != 0.656 || !bp.HasValue2 {
		t.Fatalf("bp_storage value2 = %v want 0.656", bp.Value2)
	}
	// effect 文案同样逐字。
	if got := cn35game.GuildEquipEffect("bp_storage", bp.Level, bp.Value, bp.Value2); got != "蓄水上限 49000 BP\n蓄水库 65.60%" {
		t.Fatalf("bp_storage effect = %q", got)
	}
	// temporary：全 null 占位、零值，可投资性为 false。
	tmp := view.Equipments["temporary"]
	if tmp == nil || tmp.Level != 0 || tmp.Point != 0 {
		t.Fatalf("temporary: %+v", tmp)
	}
	if cn35game.GuildEquipIsInvestable("temporary") {
		t.Fatal("temporary 不应可投资")
	}
	// 六类键齐全（原服 equipments 六个键）。
	for _, tid := range cn35game.GuildEquipmentTypeIDs {
		if _, ok := view.Equipments[tid]; !ok {
			t.Fatalf("缺 %s", tid)
		}
	}
}

func TestGuildEquipInvestGrowthAndErrors(t *testing.T) {
	s := openStore(t, t.TempDir())
	uid := guildUID(t, s, "invest")
	g, err := s.CreateGuild(uid, "投资会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// 资材不足：语义保持 ErrGuildAsset。
	if _, err = s.Invest(uid, "hp", 100, ""); !errors.Is(err, cn35game.ErrGuildAsset) {
		t.Fatalf("资材不足应 ErrGuildAsset: %v", err)
	}
	// temporary 不可投资 / 未知类型非法：都是参数非法，不是 500。
	if _, err = s.Invest(uid, "temporary", 100, ""); !errors.Is(err, cn35game.ErrGuildInvest) {
		t.Fatalf("temporary 应 ErrGuildInvest: %v", err)
	}
	if _, err = s.Invest(uid, "nope", 100, ""); !errors.Is(err, cn35game.ErrGuildInvest) {
		t.Fatalf("未知类型应 ErrGuildInvest: %v", err)
	}
	// 投资达标升级：T(2)=17,700 → 投 17,700 恰好 2 级。
	creditBalance(t, s, uid, "guild_asset", 20000)
	if _, err = s.Invest(uid, "hp", 17700, "inv-1"); err != nil {
		t.Fatalf("Invest: %v", err)
	}
	view, err := s.MyGuild(uid)
	if err != nil {
		t.Fatal(err)
	}
	hp := view.Equipments["hp"]
	if hp.Point != 17700 || hp.Level != 2 || hp.UntilPoint != cn35game.GuildEquipThreshold(3) {
		t.Fatalf("17,700 point 应为 2 级: %+v", hp)
	}
	// 台账 before/after 落盘（0039 扩列）。
	var pb, pa, lb, la int64
	var opKey string
	if err = s.DB().QueryRow(`SELECT point_before,point_after,level_before,level_after,op_key
		FROM cn35_guild_invest_log WHERE user_id=? AND type_id='hp' ORDER BY rowid DESC LIMIT 1`,
		uid).Scan(&pb, &pa, &lb, &la, &opKey); err != nil {
		t.Fatal(err)
	}
	if pb != 0 || pa != 17700 || lb != 1 || la != 2 || opKey != "inv-1" {
		t.Fatalf("台账快照 = %d→%d / %d→%d key=%s", pb, pa, lb, la, opKey)
	}
	// 满级拒绝：把 point 顶到 T(MaxLevel) 后投资 → ErrGuildEquipMax。
	maxPoint := cn35game.GuildEquipThreshold(cn35game.GuildEquipMaxLevel)
	seedEquipPoint(t, s, g.ID, "at", maxPoint)
	if _, err = s.Invest(uid, "at", 100, "inv-max"); !errors.Is(err, cn35game.ErrGuildEquipMax) {
		t.Fatalf("满级应 ErrGuildEquipMax: %v", err)
	}
	if got := view.Equipments["at"]; got != nil && got.Level >= cn35game.GuildEquipMaxLevel {
		// 只是断言曲线满级判定生效（下面重新读一次视图确认 end）。
		view2, _ := s.MyGuild(uid)
		if view2.Equipments["at"].Level != cn35game.GuildEquipMaxLevel || !view2.Equipments["at"].End {
			t.Fatalf("满级投影错误: %+v", view2.Equipments["at"])
		}
	}
}

func TestGuildEquipInvestTwoAccountsIsolated(t *testing.T) {
	s := openStore(t, t.TempDir())
	u1 := guildUID(t, s, "iso-1")
	u2 := guildUID(t, s, "iso-2")
	g1, err := s.CreateGuild(u1, "甲会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	g2, err := s.CreateGuild(u2, "乙会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	creditBalance(t, s, u1, "guild_asset", 5000)
	creditBalance(t, s, u2, "guild_asset", 5000)
	if _, err = s.Invest(u1, "sp", 1000, "iso-inv-1"); err != nil {
		t.Fatal(err)
	}
	v1, _ := s.MyGuild(u1)
	v2, _ := s.MyGuild(u2)
	if v1.Equipments["sp"].Point != 1000 {
		t.Fatalf("甲会 sp point = %d", v1.Equipments["sp"].Point)
	}
	if v2.Equipments["sp"].Point != 0 {
		t.Fatalf("乙会被甲会投资影响: %d", v2.Equipments["sp"].Point)
	}
	var bal1, bal2 int64
	_ = s.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='guild_asset'`, u1).Scan(&bal1)
	_ = s.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='guild_asset'`, u2).Scan(&bal2)
	if bal1 != 4000 || bal2 != 5000 {
		t.Fatalf("资材隔离失败: %d / %d", bal1, bal2)
	}
	_ = g1
	_ = g2
}

// buildLegacyDBThrough0037 用真实迁移文件建到 0037（含 0038/0039 之前的全部表），
// 模拟「有数据的上一版库」。新迁移只追加后，旧库升级不得丢数据。
func buildLegacyDBThrough0037(t *testing.T, path string) {
	buildLegacyDBThrough(t, path, 37)
}

func buildLegacyDBThrough(t *testing.T, path string, version int) {
	t.Helper()
	dir := filepath.Join("..", "database", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取迁移目录: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= fmt.Sprintf("%04d", version+1) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) != version {
		t.Fatalf("迁移数量 want=%d got=%d", version, len(names))
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开旧库: %v", err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE schema_migrations
		(name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读取迁移 %s: %v", name, err)
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			t.Fatalf("执行迁移 %s: %v", name, err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(raw))
		if _, err = tx.Exec(`INSERT INTO schema_migrations VALUES
			(?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, name, sum); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestGuildStorageUpgradeFrom0037(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mercd35.sqlite3")
	buildLegacyDBThrough0037(t, path)

	// 旧库里放代表性数据：公会、旧口径写下的设施行与投资流水。
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO cn35_guilds (guild_id,name,play_type_id,approval_type_id,recruit_text,
			memo,shifts,battle_setting_text,rating,rank_id,gp,created_at,updated_at)
		 VALUES ('g-old','旧会','2','1','','',127,'',0,'1',0,'old','old')`,
		// 旧占位规则：point 1500 → level 15（新曲线下应为 1 级）。
		`INSERT INTO cn35_guild_equipment (guild_id,type_id,level,point,updated_at)
		 VALUES ('g-old','hp',15,1500,'old')`,
		`INSERT INTO cn35_guild_equipment (guild_id,type_id,level,point,updated_at)
		 VALUES ('g-old','bp_storage',10,1000,'old')`,
		`INSERT INTO cn35_guild_invest_log (guild_id,user_id,type_id,amount,created_at)
		 VALUES ('g-old','u-old','hp',1500,'old')`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatalf("旧库数据: %v", err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	// 升级到最新（含 0038/0039）：不丢旧行，新表可用。
	s := openStore(t, dir)
	assertLatestSchema(t, s)
	var amount int64
	if err = s.DB().QueryRow(`SELECT count(*) FROM cn35_guild_bp_storage`).Scan(&amount); err != nil {
		t.Fatalf("0038 表未建: %v", err)
	}
	var pb, pa int64
	var opKey string
	if err = s.DB().QueryRow(`SELECT point_before,point_after,op_key
		FROM cn35_guild_invest_log WHERE user_id='u-old'`).Scan(&pb, &pa, &opKey); err != nil {
		t.Fatalf("旧流水丢失: %v", err)
	}
	if pb != 0 || pa != 0 || opKey != "" {
		t.Fatalf("旧行默认值不符: %d/%d/%q", pb, pa, opKey)
	}
	// 升级后读面按新曲线纠正等级（旧 level=15/10 不可信，point 才是真源）。
	var point int64
	if err = s.DB().QueryRow(`SELECT point FROM cn35_guild_equipment
		WHERE guild_id='g-old' AND type_id='hp'`).Scan(&point); err != nil {
		t.Fatal(err)
	}
	if got := cn35game.GuildEquipLevel(point); got != 1 {
		t.Fatalf("升级后等级应由 point 反解为 1, got %d", got)
	}
	// 新表可写：旧库升级后蓄水库可用（0 余额 → 不可取）。
	if _, err = s.DB().Exec(`INSERT INTO cn35_guild_bp_storage
		(guild_id,amount,updated_at) VALUES ('g-old',0,'new')`); err != nil {
		t.Fatalf("0038 新表写入失败: %v", err)
	}
	// 升级后设施投资仍可执行（新曲线）：用真实账号入会再投。
	uid := guildUID(t, s, "upgrade")
	if _, err = s.DB().Exec(`INSERT INTO cn35_guild_members
		(guild_id,user_id,role,joined_at) VALUES ('g-old',?,3,'new')`, uid); err != nil {
		t.Fatal(err)
	}
	creditBalance(t, s, uid, "guild_asset", 5000)
	if _, err = s.Invest(uid, "sp", 500, "upgrade-inv"); err != nil {
		t.Fatalf("升级后投资失败: %v", err)
	}
	view, err := s.MyGuild(uid)
	if err != nil {
		t.Fatal(err)
	}
	if view.Equipments["sp"].Point != 500 || view.Equipments["sp"].Level != 1 {
		t.Fatalf("升级后投资投影错误: %+v", view.Equipments["sp"])
	}
	// 旧 hp 行只剩 point，等级由曲线反解（不是旧 level=15）。
	if view.Equipments["hp"].Level != 1 {
		t.Fatalf("旧行等级应由 point 反解为 1, got %+v", view.Equipments["hp"])
	}
}
