// login_bonus_test.go LOGIN350-02：不可思议之泉时间累积的读/领事务、
// 上限、BP 溢出、并发/重放、零写入与账号隔离（真实 SQLite）。
package cn35store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/database"
)

// springSeedPoint 建号种子金币（quest0 教程上下文 gacha_point=302）。
const springSeedPoint = 302

// springNewAccount 建号并返回 user_id（走 QuickLogin→ChooseServer 真实路径）。
func springNewAccount(t *testing.T, s *Store, device string) string {
	t.Helper()
	r, err := s.QuickLogin(device)
	if err != nil {
		t.Fatalf("QuickLogin(%s): %v", device, err)
	}
	uid, _, err := s.ChooseServer(r.HeAccountID, r.Token, "1")
	if err != nil {
		t.Fatalf("ChooseServer(%s): %v", device, err)
	}
	return uid
}

// springSetAnchor 把「可领时刻」写成 anchor+3600，构造指定累积时长。
func springSetAnchor(t *testing.T, s *Store, uid string, anchor int64) {
	t.Helper()
	if _, err := s.DB().Exec(`UPDATE cn35_player_profiles
		SET login_bonus_receivable_sec=? WHERE user_id=?`,
		anchor+cn35game.SpringIntervalSec, uid); err != nil {
		t.Fatalf("设置累积锚点: %v", err)
	}
}

// springProfile 读可领时刻与 BP。
func springProfile(t *testing.T, s *Store, uid string) (receivable, bp int64) {
	t.Helper()
	if err := s.DB().QueryRow(`SELECT login_bonus_receivable_sec,bp
		FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&receivable, &bp); err != nil {
		t.Fatalf("读取档案: %v", err)
	}
	return receivable, bp
}

// springBalance 读余额（无行=0）。
func springBalance(t *testing.T, s *Store, uid, currency string) int64 {
	t.Helper()
	var v int64
	err := s.DB().QueryRow(`SELECT amount FROM cn35_player_balances
		WHERE user_id=? AND currency=?`, uid, currency).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("读取余额: %v", err)
	}
	return v
}

// springClaimRows 该账号泉水台账行数。
func springClaimRows(t *testing.T, s *Store, uid string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_spring_claims
		WHERE user_id=?`, uid).Scan(&n); err != nil {
		t.Fatalf("读取泉水台账: %v", err)
	}
	return n
}

func TestSpringStateAndClaim(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-spring-claim")
	now := time.Now().Unix()

	// 累积 2 小时：金币 2000 / 公会素材 2 / BP 10。
	springSetAnchor(t, s, uid, now-2*3600)
	st, err := s.SpringState(uid, now)
	if err != nil {
		t.Fatalf("SpringState: %v", err)
	}
	if st.Gold != 2000 || st.GuildAsset != 2 || st.BattlePoint != 10 {
		t.Fatalf("待领量 = %+v, want 2000/2/10", st)
	}
	if st.ReceivableSec != now-2*3600+3600 {
		t.Fatalf("可领时刻 = %d", st.ReceivableSec)
	}

	res, err := s.ClaimSpring(uid, now)
	if err != nil || !res.Granted {
		t.Fatalf("ClaimSpring = %+v, %v", res, err)
	}
	if res.Gold != 2000 || res.GuildAsset != 2 || res.BattlePoint != 10 {
		t.Fatalf("发放量 = %+v", res)
	}
	if got := springBalance(t, s, uid, "gacha_point"); got != springSeedPoint+2000 {
		t.Fatalf("金币 = %d, want %d", got, springSeedPoint+2000)
	}
	if got := springBalance(t, s, uid, "guild_asset"); got != 2 {
		t.Fatalf("公会建设素材 = %d, want 2", got)
	}
	receivable, bp := springProfile(t, s, uid)
	if bp != 300+10 {
		t.Fatalf("BP = %d, want 310", bp)
	}
	if receivable != now+3600 {
		t.Fatalf("领取后可领时刻 = %d, want %d", receivable, now+3600)
	}

	// 立即再领：无可领量，零写入。
	if _, err := s.ClaimSpring(uid, now); !errors.Is(err, cn35game.ErrLoginBonusNotReady) {
		t.Fatalf("重复领取应 ErrLoginBonusNotReady, got %v", err)
	}
	if n := springClaimRows(t, s, uid); n != 1 {
		t.Fatalf("重复领取落台账: %d", n)
	}
	if got := springBalance(t, s, uid, "gacha_point"); got != springSeedPoint+2000 {
		t.Fatalf("重复领取后金币 = %d", got)
	}
}

func TestSpringCaps(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-spring-caps")
	now := time.Now().Unix()

	// 50 小时：金币/公会素材达 48h 上限，BP 达 24h 上限。
	springSetAnchor(t, s, uid, now-50*3600)
	res, err := s.ClaimSpring(uid, now)
	if err != nil || !res.Granted {
		t.Fatalf("ClaimSpring = %+v, %v", res, err)
	}
	if res.Gold != 48000 || res.GuildAsset != 48 || res.BattlePoint != 120 {
		t.Fatalf("上限发放量 = %+v, want 48000/48/120", res)
	}
}

func TestSpringBPOverflowAllowed(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-spring-bp")
	now := time.Now().Unix()
	// 建号 BP=300=max_bp；0035 起允许溢出持有。
	springSetAnchor(t, s, uid, now-3600)
	res, err := s.ClaimSpring(uid, now)
	if err != nil || !res.Granted {
		t.Fatalf("ClaimSpring = %+v, %v", res, err)
	}
	if res.BattlePoint != 5 {
		t.Fatalf("BP 发放 = %d, want 5", res.BattlePoint)
	}
	if _, bp := springProfile(t, s, uid); bp != 305 {
		t.Fatalf("BP 溢出持有 = %d, want 305（不再受 max_bp=300 限制）", bp)
	}
}

func TestSpringZeroPendingNoWrite(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-spring-zero")
	now := time.Now().Unix()
	// 新号 created_at=now，累积不足 1 小时。
	if _, err := s.ClaimSpring(uid, now); !errors.Is(err, cn35game.ErrLoginBonusNotReady) {
		t.Fatalf("无累积量应 ErrLoginBonusNotReady, got %v", err)
	}
	if n := springClaimRows(t, s, uid); n != 0 {
		t.Fatalf("零写入失败: 台账 %d 行", n)
	}
	if got := springBalance(t, s, uid, "gacha_point"); got != springSeedPoint {
		t.Fatalf("零写入失败: 金币 = %d", got)
	}
}

func TestSpringConcurrentClaimOnlyOnce(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-spring-race")
	now := time.Now().Unix()
	springSetAnchor(t, s, uid, now-3600)

	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.ClaimSpring(uid, now)
			if err == nil && res.Granted {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != 1 {
		t.Fatalf("并发领取成功 %d 次, want 1", granted)
	}
	if n := springClaimRows(t, s, uid); n != 1 {
		t.Fatalf("并发台账 %d 行, want 1", n)
	}
	if got := springBalance(t, s, uid, "gacha_point"); got != springSeedPoint+1000 {
		t.Fatalf("并发后金币 = %d, want %d", got, springSeedPoint+1000)
	}
}

func TestSpringAccountIsolation(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	a := springNewAccount(t, s, "dev-spring-a")
	b := springNewAccount(t, s, "dev-spring-b")
	now := time.Now().Unix()
	springSetAnchor(t, s, a, now-3600)
	if _, err := s.ClaimSpring(a, now); err != nil {
		t.Fatalf("A 领取: %v", err)
	}
	if got := springBalance(t, s, a, "gacha_point"); got != springSeedPoint+1000 {
		t.Fatalf("A 金币 = %d", got)
	}
	if got := springBalance(t, s, b, "gacha_point"); got != springSeedPoint {
		t.Fatalf("B 不应继承 A 的领取: %d", got)
	}
	if n := springClaimRows(t, s, b); n != 0 {
		t.Fatalf("B 台账 %d 行", n)
	}
}

func TestSpringInvalidParams(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-spring-invalid")
	if _, err := s.ClaimSpring("", 1); !errors.Is(err, cn35game.ErrInvalidRequest) {
		t.Fatalf("空 userID 应 ErrInvalidRequest, got %v", err)
	}
	if _, err := s.ClaimSpring(uid, 0); !errors.Is(err, cn35game.ErrInvalidRequest) {
		t.Fatalf("now=0 应 ErrInvalidRequest, got %v", err)
	}
	if _, err := s.SpringState("no-such-user", 1); !errors.Is(err, cn35game.ErrNoAccount) {
		t.Fatalf("未知账号应 ErrNoAccount, got %v", err)
	}
}

// TestSpringUpgradeFrom0034 真实上一 schema（0034）旧库升级到 0035：
// 旧「每日 4:00」可领时刻被重置为 0（锚点回落 created_at），账号资产保持，
// bp<=max_bp 约束移除后 BP 可溢出，泉水台账立即可用。
func TestSpringUpgradeFrom0034(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationsThrough(t, db, "0034_cn35_login_bonus.sql")
	if _, err = db.Exec(`INSERT INTO cn35_accounts
		(he_account_id,user_id,token,sk,user_name,account_name,password_hash,
		 sdk_account_id,device_id,is_new_user,tutorial_finish,last_server_id,
		 created_at,updated_at)
		VALUES ('spring-old-he','spring-old-u','tok','sk-old','旧玩家','','','','dev-spring-old',
		 0,1,'1','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cn35_player_profiles
		(user_id,level,exp,max_exp,ap,max_ap,ap_updated_at,bp,max_bp,
		 leader_unit_id,login_bonus_receivable_sec,guild_name_snapshot,
		 created_at,updated_at)
		VALUES ('spring-old-u',10,5,100,50,50,'2020-01-01T00:00:00Z',10,10,
		 '152',1600000000,'', '2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cn35_player_balances
		(user_id,currency,amount,updated_at) VALUES ('spring-old-u','gacha_point',777,'2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openStore(t, dir)
	defer s.Close()
	assertLatestSchema(t, s)
	var level, receivable int64
	if err := s.DB().QueryRow(`SELECT level,login_bonus_receivable_sec
		FROM cn35_player_profiles WHERE user_id='spring-old-u'`).Scan(&level, &receivable); err != nil {
		t.Fatal(err)
	}
	if level != 10 || receivable != 0 {
		t.Fatalf("升级后 profile = level %d receivable %d（旧口径应重置 0）", level, receivable)
	}
	// created_at=2020 → 长期未登录，首次即达上限；既有金币 777 保留。
	now := time.Now().Unix()
	st, err := s.SpringState("spring-old-u", now)
	if err != nil {
		t.Fatalf("SpringState: %v", err)
	}
	if st.Gold != 48000 || st.GuildAsset != 48 || st.BattlePoint != 120 {
		t.Fatalf("老账号首读待领量 = %+v, want 上限 48000/48/120", st)
	}
	res, err := s.ClaimSpring("spring-old-u", now)
	if err != nil || !res.Granted {
		t.Fatalf("升级库领取 = %+v, %v", res, err)
	}
	if got := springBalance(t, s, "spring-old-u", "gacha_point"); got != 777+48000 {
		t.Fatalf("升级库金币 = %d, want %d", got, 777+48000)
	}
	// BP 溢出：旧库 bp=10 max_bp=10，领取 120 后应为 130（约束已移除）。
	if _, bp := springProfile(t, s, "spring-old-u"); bp != 130 {
		t.Fatalf("升级库 BP = %d, want 130（溢出持有）", bp)
	}
}

// TestSpringClaimRollbackOnLedgerFailure 台账写入失败时整笔回滚：
// 可领时刻推进、金币/公会素材余额、BP 全部回到领取前，不留半领取态。
func TestSpringClaimRollbackOnLedgerFailure(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	uid := springNewAccount(t, s, "dev-spring-rollback")
	now := time.Now().Unix()
	springSetAnchor(t, s, uid, now-2*3600)
	beforeReceivable, beforeBP := springProfile(t, s, uid)
	beforeGold := springBalance(t, s, uid, "gacha_point")

	if _, err := s.DB().Exec(`CREATE TRIGGER spring_claim_fail
		BEFORE INSERT ON cn35_spring_claims
		BEGIN SELECT RAISE(ABORT,'inject'); END`); err != nil {
		t.Fatalf("注入失败触发器: %v", err)
	}
	if _, err := s.ClaimSpring(uid, now); err == nil {
		t.Fatal("台账注入失败时领取应报错")
	}
	if r, bp := springProfile(t, s, uid); r != beforeReceivable || bp != beforeBP {
		t.Fatalf("回滚失败：可领时刻 %d→%d BP %d→%d", beforeReceivable, r, beforeBP, bp)
	}
	if got := springBalance(t, s, uid, "gacha_point"); got != beforeGold {
		t.Fatalf("回滚失败：金币 %d→%d", beforeGold, got)
	}
	if got := springBalance(t, s, uid, "guild_asset"); got != 0 {
		t.Fatalf("回滚失败：公会素材 = %d", got)
	}
	if n := springClaimRows(t, s, uid); n != 0 {
		t.Fatalf("回滚失败：台账 %d 行", n)
	}

	// 移除注入后同一 now 可正常领取一次。
	if _, err := s.DB().Exec(`DROP TRIGGER spring_claim_fail`); err != nil {
		t.Fatalf("移除触发器: %v", err)
	}
	if res, err := s.ClaimSpring(uid, now); err != nil || !res.Granted {
		t.Fatalf("修复后领取 = %+v, %v", res, err)
	}
	if n := springClaimRows(t, s, uid); n != 1 {
		t.Fatalf("修复后台账 = %d, want 1", n)
	}
}
