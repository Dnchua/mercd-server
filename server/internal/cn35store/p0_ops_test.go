// p0_ops_test.go 2026-09-28 P0 写面存储层验收：0016 升级、leader/helper/
// protect、魔宠双向装备、礼物领取事务与幂等。
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
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/database"
)

// testCatalog 见 grant_test.go（真目录；礼物入库需要 Unit/Monster 模板校验）。

// insertUnit 直插一条单位库存行（夹具）。
func insertUnit(t *testing.T, s *Store, uid string, unitID, level int) {
	t.Helper()
	if err := s.WithTx(func(tx *sql.Tx) error {
		return InsertUnitTx(tx, uid, cn35game.UnitRecord{UnitID: unitID, Level: level})
	}); err != nil {
		t.Fatalf("插入单位 %d: %v", unitID, err)
	}
}

// insertPresent 直插一条礼物行（夹具）。
func insertPresent(t *testing.T, s *Store, uid, uuid, contentType string, contentID, amount, expireAt int64) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO cn35_user_presents
		(uuid,user_id,content_type,content_id,amount,expire_at,received_at,created_at)
		VALUES (?,?,?,?,?,?,0,1000)`,
		uuid, uid, contentType, contentID, amount, expireAt); err != nil {
		t.Fatalf("插入礼物 %s: %v", uuid, err)
	}
}

// clearPresents 清空账号的建号预置礼物行，恢复「空礼物池」夹具前提
// （STARTER-GIFT 后新账号自带 3 条待领）。
func clearPresents(t *testing.T, s *Store, uids ...string) {
	t.Helper()
	for _, uid := range uids {
		if _, err := s.DB().Exec(`DELETE FROM cn35_user_presents WHERE user_id=?`, uid); err != nil {
			t.Fatalf("清空礼物: %v", err)
		}
	}
}

// applyMigrationsThrough 回放 0001~last 全部迁移文件（模拟 P0 前的真实库）。
func applyMigrationsThrough(t *testing.T, db *sql.DB, last string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations
		(name TEXT PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join("..", "database", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") && e.Name() <= last {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("..", "database", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(string(raw)); err != nil {
			tx.Rollback()
			t.Fatalf("exec %s: %v", name, err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(raw))
		if _, err = tx.Exec(`INSERT INTO schema_migrations VALUES (?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
			name, sum); err != nil {
			tx.Rollback()
			t.Fatalf("登记 %s: %v", name, err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestUpgradePre016KeepsProfileAndDefaultsHelper 0015 版真实库升级到 0016：
// 既有 profile 行保留，helper_unit_id 默认 ”。
func TestUpgradePre016KeepsProfileAndDefaultsHelper(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationsThrough(t, db, "0015_cn35_presents_infos.sql")
	// 代表性旧数据：一个 cn35 账号 + profile（leader 必填非空）。
	if _, err = db.Exec(`INSERT INTO cn35_accounts
		(he_account_id,user_id,token,sk,user_name,account_name,password_hash,
		 sdk_account_id,device_id,is_new_user,tutorial_finish,last_server_id,
		 created_at,updated_at)
		VALUES ('old-he','old-u','tok','sk-old','旧玩家','','','','dev-old',
		 0,1,'1','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO cn35_player_profiles
		(user_id,level,exp,max_exp,ap,max_ap,ap_updated_at,bp,max_bp,
		 leader_unit_id,login_bonus_receivable_sec,guild_name_snapshot,
		 created_at,updated_at)
		VALUES ('old-u',10,5,100,50,50,'2020-01-01T00:00:00Z',10,10,
		 '152',3600,'', '2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openStore(t, dir)
	defer s.Close()
	assertLatestSchema(t, s)
	var helper string
	var level int
	if err := s.DB().QueryRow(`SELECT helper_unit_id,level FROM cn35_player_profiles
		WHERE user_id='old-u'`).Scan(&helper, &level); err != nil {
		t.Fatal(err)
	}
	if helper != "" || level != 10 {
		t.Fatalf("升级后 helper=%q level=%d，want ''/10", helper, level)
	}
}

// TestSetLeaderHelperIndependentAndPersist 队长/助战独立写 + 重启持久化。
// 两字段无互斥（UserUnitDetail.cs:346-360 各自独立比较）：把当前队长
// 同单位设为助战后，leader 不变、两字段都等于该 id。
func TestSetLeaderHelperIndependentAndPersist(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := seedAccount(t, s, "dev-p0-unit")
	insertUnit(t, s, uid, 152, 1)
	insertUnit(t, s, uid, 157, 1)

	if err := s.SetLeaderUnitID(uid, 152); err != nil {
		t.Fatalf("SetLeaderUnitID: %v", err)
	}
	if err := s.SetHelperUnitID(uid, 157); err != nil {
		t.Fatalf("SetHelperUnitID: %v", err)
	}
	// 独立：把队长同一单位设成助战 → leader 保持 152，两字段同值。
	if err := s.SetHelperUnitID(uid, 152); err != nil {
		t.Fatalf("SetHelperUnitID 152: %v", err)
	}
	var leader, helper string
	if err := s.DB().QueryRow(`SELECT leader_unit_id,helper_unit_id
		FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&leader, &helper); err != nil {
		t.Fatal(err)
	}
	if leader != "152" || helper != "152" {
		t.Fatalf("独立写入后 leader=%q helper=%q，want 152/152", leader, helper)
	}
	// 独立：换队长为 157 → 助战位不动（仍 152）。
	if err := s.SetLeaderUnitID(uid, 157); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT leader_unit_id,helper_unit_id
		FROM cn35_player_profiles WHERE user_id=?`, uid).Scan(&leader, &helper); err != nil {
		t.Fatal(err)
	}
	if leader != "157" || helper != "152" {
		t.Fatalf("换队长后 leader=%q helper=%q，want 157/152", leader, helper)
	}
	// 恢复 helper=157 供重启持久化断言。
	if err := s.SetHelperUnitID(uid, 157); err != nil {
		t.Fatal(err)
	}
	// 未持有单位 → ErrUnitNotOwned。
	if err := s.SetLeaderUnitID(uid, 9999); !errors.Is(err, cn35game.ErrUnitNotOwned) {
		t.Fatalf("未持有单位 err=%v，want ErrUnitNotOwned", err)
	}
	s.Close()

	// 重启（重新打开同一文件）后 helper 仍在。
	s2 := openStore(t, dir)
	defer s2.Close()
	if err := s2.DB().QueryRow(`SELECT helper_unit_id FROM cn35_player_profiles
		WHERE user_id=?`, uid).Scan(&helper); err != nil {
		t.Fatal(err)
	}
	if helper != "157" {
		t.Fatalf("重启后 helper=%q，want 157", helper)
	}
}

// TestUnitOpsIsolation 跨账号拒绝 + protect 写位。
func TestUnitOpsIsolation(t *testing.T) {
	s := openStore(t, t.TempDir())
	uidA := seedAccount(t, s, "dev-p0-a")
	uidB := seedAccount(t, s, "dev-p0-b")
	insertUnit(t, s, uidA, 152, 1)

	// B 拿 A 的单位设队长/助战/保护 → 一律 ErrUnitNotOwned（不区分不存在/他人）。
	for _, fn := range []error{
		s.SetLeaderUnitID(uidB, 152),
		s.SetHelperUnitID(uidB, 152),
		s.SetUnitProtected(uidB, 152, true),
	} {
		if !errors.Is(fn, cn35game.ErrUnitNotOwned) {
			t.Fatalf("跨账号 err=%v，want ErrUnitNotOwned", fn)
		}
	}
	if err := s.SetUnitProtected(uidA, 152, true); err != nil {
		t.Fatal(err)
	}
	var prot int
	if err := s.DB().QueryRow(`SELECT protected FROM cn35_player_units
		WHERE user_id=? AND unit_id=152`, uidA).Scan(&prot); err != nil {
		t.Fatal(err)
	}
	if prot != 1 {
		t.Fatalf("protected=%d want 1", prot)
	}
}

// monsterHost 读魔宠宿主（nil=未装备）。
func monsterHost(t *testing.T, s *Store, uid, uuid string) *int {
	t.Helper()
	var v sql.NullInt64
	if err := s.DB().QueryRow(`SELECT unit_id FROM cn35_player_monsters
		WHERE user_id=? AND uuid=?`, uid, uuid).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

// unitMonster 读同伴装备位魔宠 uuid。
func unitMonster(t *testing.T, s *Store, uid string, unitID int) string {
	t.Helper()
	var v string
	if err := s.DB().QueryRow(`SELECT monster_uuid FROM cn35_player_units
		WHERE user_id=? AND unit_id=?`, uid, unitID).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestMonsterEquipBidirectional 换装双向一致 + 重启持久化。
func TestMonsterEquipBidirectional(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	uid := seedAccount(t, s, "dev-p0-mon")
	insertUnit(t, s, uid, 152, 1)
	insertUnit(t, s, uid, 157, 1)
	m1, m2 := "mon-p0-1", "mon-p0-2"
	if err := s.WithTx(func(tx *sql.Tx) error {
		if err := InsertMonsterTx(tx, uid, cn35game.MonsterRecord{UUID: m1, MonsterID: 1, Level: 1}); err != nil {
			return err
		}
		return InsertMonsterTx(tx, uid, cn35game.MonsterRecord{UUID: m2, MonsterID: 2, Level: 1})
	}); err != nil {
		t.Fatal(err)
	}

	// 装备 m1→152：两侧引用一致。
	if err := s.EquipMonster(uid, m1, 152); err != nil {
		t.Fatalf("EquipMonster: %v", err)
	}
	if h := monsterHost(t, s, uid, m1); h == nil || *h != 152 {
		t.Fatalf("m1 host=%v want 152", h)
	}
	if got := unitMonster(t, s, uid, 152); got != m1 {
		t.Fatalf("152 monster=%q want %q", got, m1)
	}

	// m1 换装到 157：原宿主 152 引用清空。
	if err := s.EquipMonster(uid, m1, 157); err != nil {
		t.Fatal(err)
	}
	if h := monsterHost(t, s, uid, m1); h == nil || *h != 157 {
		t.Fatalf("换装后 m1 host=%v want 157", h)
	}
	if got := unitMonster(t, s, uid, 152); got != "" {
		t.Fatalf("原宿主 152 残留 monster=%q", got)
	}

	// m2 装上 157（已占 m1）→ m1 被卸下，m2 占据。
	if err := s.EquipMonster(uid, m2, 157); err != nil {
		t.Fatal(err)
	}
	if h := monsterHost(t, s, uid, m1); h != nil {
		t.Fatalf("m1 应被卸下，host=%v", h)
	}
	if h := monsterHost(t, s, uid, m2); h == nil || *h != 157 {
		t.Fatalf("m2 host=%v want 157", h)
	}
	if got := unitMonster(t, s, uid, 157); got != m2 {
		t.Fatalf("157 monster=%q want %q", got, m2)
	}

	// off（带宿主校验）→ 双侧清空。
	if err := s.UnequipMonster(uid, m2, 157); err != nil {
		t.Fatal(err)
	}
	if h := monsterHost(t, s, uid, m2); h != nil {
		t.Fatalf("off 后 m2 host=%v want nil", h)
	}
	if got := unitMonster(t, s, uid, 157); got != "" {
		t.Fatalf("off 后 157 monster=%q", got)
	}

	// off 未装备的 → ErrMonsterNotEquipped；错宿主 → ErrMonsterHostMismatch。
	if err := s.UnequipMonster(uid, m2, 157); !errors.Is(err, cn35game.ErrMonsterNotEquipped) {
		t.Fatalf("未装备 off err=%v", err)
	}
	if err := s.EquipMonster(uid, m2, 152); err != nil {
		t.Fatal(err)
	}
	if err := s.UnequipMonster(uid, m2, 157); !errors.Is(err, cn35game.ErrMonsterHostMismatch) {
		t.Fatalf("错宿主 off err=%v", err)
	}
	// 不指定宿主（unitID<0）按实际宿主卸下。
	if err := s.UnequipMonster(uid, m2, -1); err != nil {
		t.Fatal(err)
	}
	if got := unitMonster(t, s, uid, 152); got != "" {
		t.Fatalf("无宿主 off 后 152 monster=%q", got)
	}

	// 跨账号：B 的 uuid/unit 一律拒绝。
	uidB := seedAccount(t, s, "dev-p0-mon-b")
	insertUnit(t, s, uidB, 152, 1)
	if err := s.EquipMonster(uidB, m1, 152); !errors.Is(err, cn35game.ErrMonsterNotOwned) {
		t.Fatalf("他人魔宠装备 err=%v", err)
	}
	if err := s.WithTx(func(tx *sql.Tx) error {
		return InsertMonsterTx(tx, uidB, cn35game.MonsterRecord{UUID: "mon-b", MonsterID: 1, Level: 1})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.EquipMonster(uidB, "mon-b", 157); !errors.Is(err, cn35game.ErrUnitNotOwned) {
		t.Fatalf("他人同伴装备 err=%v", err)
	}

	// protect：他人 uuid 混入 → 整体回滚，本账号魔宠保护位不被部分写入。
	if err := s.EquipMonster(uid, m1, 152); err != nil {
		t.Fatal(err)
	}
	err := s.SetMonsterProtected(uid, []string{m1, "mon-b"}, true)
	if !errors.Is(err, cn35game.ErrMonsterNotOwned) {
		t.Fatalf("混合 protect err=%v", err)
	}
	var prot int
	if err := s.DB().QueryRow(`SELECT protected FROM cn35_player_monsters
		WHERE user_id=? AND uuid=?`, uid, m1).Scan(&prot); err != nil {
		t.Fatal(err)
	}
	if prot != 0 {
		t.Fatalf("混合 protect 应整体回滚，m1 protected=%d", prot)
	}
	if err := s.SetMonsterProtected(uid, []string{m1}, true); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 重启后装备关系与保护位仍在。
	s2 := openStore(t, dir)
	defer s2.Close()
	if h := monsterHost(t, s2, uid, m1); h == nil || *h != 152 {
		t.Fatalf("重启后 m1 host=%v want 152", h)
	}
	if err := s2.DB().QueryRow(`SELECT protected FROM cn35_player_monsters
		WHERE user_id=? AND uuid=?`, uid, m1).Scan(&prot); err != nil {
		t.Fatal(err)
	}
	if prot != 1 {
		t.Fatalf("重启后 m1 protected=%d want 1", prot)
	}
}

// TestReceivePresentSingle 单领：单位/魔宠/货币三类内容同事务入库。
func TestReceivePresentSingle(t *testing.T) {
	s := openStore(t, t.TempDir())
	cat := testCatalog(t)
	uid := seedAccount(t, s, "dev-p0-recv")
	setBalance(t, s, uid, "coin", 0)
	setBalance(t, s, uid, "gacha_point", 0)
	insertPresent(t, s, uid, "p-unit", "unit", 152, 1, 0)
	insertPresent(t, s, uid, "p-mon", "monster", 1, 2, 0)
	insertPresent(t, s, uid, "p-coin", "coin", 1, 50, 0)
	insertPresent(t, s, uid, "p-point", "gacha_point", 1, 30, 0)

	// 同伴礼物：新实例入库（newcomer）。
	res, err := s.ReceivePresent(uid, "p-unit", 2000, cat)
	if err != nil || len(res.Granted) != 1 {
		t.Fatalf("领取同伴礼物: %v res=%v", err, res)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_player_units
		WHERE user_id=? AND unit_id=152`, uid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("同伴礼物入库 n=%d err=%v", n, err)
	}
	// 重复领取 → ErrPresentAlreadyReceived，且不重复入库。
	if _, err = s.ReceivePresent(uid, "p-unit", 2001, cat); !errors.Is(err, cn35game.ErrPresentAlreadyReceived) {
		t.Fatalf("重复领取 err=%v", err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_player_units
		WHERE user_id=? AND unit_id=152`, uid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("重复领取后单位数=%d", n)
	}

	// 魔宠礼物：amount=2 → 两个实例，uuid 已生成。
	res, err = s.ReceivePresent(uid, "p-mon", 2002, cat)
	if err != nil || len(res.Granted) != 1 || len(res.Granted[0].MonsterUUIDs) != 2 {
		t.Fatalf("领取魔宠礼物: %v res=%+v", err, res)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_player_monsters
		WHERE user_id=? AND monster_id=1`, uid).Scan(&n); err != nil || n != 2 {
		t.Fatalf("魔宠礼物入库 n=%d err=%v", n, err)
	}

	// 货币礼物：余额入账。
	if _, err = s.ReceivePresent(uid, "p-coin", 2003, cat); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReceivePresent(uid, "p-point", 2004, cat); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, s, uid, "coin"); b != 50 {
		t.Fatalf("coin=%d want 50", b)
	}
	if b := balance(t, s, uid, "gacha_point"); b != 30 {
		t.Fatalf("gacha_point=%d want 30", b)
	}
}

// TestReceivePresentRejections 过期/他人 uuid/不支持类型一律拒绝，
// received_at 保持 0（半领取态不得存在）。
func TestReceivePresentRejections(t *testing.T) {
	s := openStore(t, t.TempDir())
	cat := testCatalog(t)
	uidA := seedAccount(t, s, "dev-p0-ra")
	uidB := seedAccount(t, s, "dev-p0-rb")
	insertPresent(t, s, uidA, "p-expired", "coin", 1, 10, 1500) // expire_at<now
	insertPresent(t, s, uidB, "p-foreign", "coin", 1, 10, 0)
	insertPresent(t, s, uidA, "p-skill", "skill", 1, 1, 0)
	insertPresent(t, s, uidA, "p-badunit", "unit", 999999, 1, 0)

	if _, err := s.ReceivePresent(uidA, "p-expired", 2000, cat); !errors.Is(err, cn35game.ErrPresentExpired) {
		t.Fatalf("过期礼物 err=%v", err)
	}
	// 他人 uuid → NotFound（不泄露存在性）。
	if _, err := s.ReceivePresent(uidA, "p-foreign", 2000, cat); !errors.Is(err, cn35game.ErrPresentNotFound) {
		t.Fatalf("他人礼物 err=%v", err)
	}
	// 不支持类型 → ErrPresentUnsupported。
	if _, err := s.ReceivePresent(uidA, "p-skill", 2000, cat); !errors.Is(err, cn35game.ErrPresentUnsupported) {
		t.Fatalf("skill 礼物 err=%v", err)
	}
	// 目录未知 unit → ErrPresentUnsupported 且 received_at 未被标记（回滚）。
	if _, err := s.ReceivePresent(uidA, "p-badunit", 2000, cat); !errors.Is(err, cn35game.ErrPresentUnsupported) {
		t.Fatalf("未知 unit err=%v", err)
	}
	var cnt int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_user_presents
		WHERE user_id=? AND received_at!=0`, uidA).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("拒绝后仍有已领取行 %d", cnt)
	}
}

// TestReceiveAllSkipsUnsupported 全领：不支持类型与过期行跳过保持未领；
// 支持类型一个事务内全部入库；without_unit 排除同伴。
func TestReceiveAllSkipsUnsupported(t *testing.T) {
	s := openStore(t, t.TempDir())
	cat := testCatalog(t)
	uid := seedAccount(t, s, "dev-p0-all")
	clearPresents(t, s, uid)
	setBalance(t, s, uid, "coin", 0)
	insertPresent(t, s, uid, "a-unit", "unit", 152, 1, 0)
	insertPresent(t, s, uid, "a-coin", "coin", 1, 20, 0)
	insertPresent(t, s, uid, "a-skill", "skill", 1, 1, 0)    // 不支持 → 跳过
	insertPresent(t, s, uid, "a-expired", "coin", 1, 5, 100) // 过期 → 跳过

	// without_unit=true：unit 跳过，coin 领取，skill/过期跳过。
	res, err := s.ReceiveAllPresents(uid, true, 2000, cat)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Granted) != 1 || res.Granted[0].ContentType != "coin" {
		t.Fatalf("without_unit 全领结果=%+v", res.Granted)
	}
	if b := balance(t, s, uid, "coin"); b != 20 {
		t.Fatalf("coin=%d want 20", b)
	}
	// 再全领（不含 without_unit）：unit 领取；skill/过期仍跳过。
	res, err = s.ReceiveAllPresents(uid, false, 2001, cat)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Granted) != 1 || res.Granted[0].ContentType != "unit" {
		t.Fatalf("二次全领结果=%+v", res.Granted)
	}
	// 剩余未领行 = a-skill + a-expired。
	left, err := s.UnreadPresents(uid, 2002)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].UUID != "a-skill" {
		t.Fatalf("剩余待领=%+v，want 仅 a-skill", left)
	}
	var receivedCount int
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_user_presents
		WHERE user_id=? AND received_at!=0`, uid).Scan(&receivedCount); err != nil {
		t.Fatal(err)
	}
	if receivedCount != 2 {
		t.Fatalf("已领取行数=%d want 2", receivedCount)
	}
	// 三次全领：无新内容（幂等）。
	res, err = s.ReceiveAllPresents(uid, false, 2003, cat)
	if err != nil || len(res.Granted) != 0 {
		t.Fatalf("三次全领 granted=%d err=%v", len(res.Granted), err)
	}
}

// TestStarterPresentsSeeded STARTER-GIFT：建号同事务预置 3 条货币礼物
// （钻石 9999 / 徽章 5000 / 金币 1000000），领取即入账；同设备复登不重复。
func TestStarterPresentsSeeded(t *testing.T) {
	s := openStore(t, t.TempDir())
	cat := testCatalog(t)
	uid := seedAccount(t, s, "dev-p0-starter")

	got, err := s.UnreadPresents(uid, 99999999999)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("建号礼物=%d 条 want 3: %+v", len(got), got)
	}
	want := map[string]int64{"coin": 99999, "medal": 5000, "gacha_point": 1000000}
	byType := map[string]cn35game.PresentRecord{}
	for _, r := range got {
		byType[r.ContentType] = r
		if r.Amount != want[r.ContentType] {
			t.Fatalf("%s amount=%d want %d", r.ContentType, r.Amount, want[r.ContentType])
		}
		if r.ExpireAt != 0 || r.UUID == "" {
			t.Fatalf("礼物行字段异常: %+v", r)
		}
	}
	for ct := range want {
		if _, ok := byType[ct]; !ok {
			t.Fatalf("缺 %s 礼物: %+v", ct, got)
		}
	}
	// 领取钻石礼物 → coin 余额入账（建号种子 coin=0）。
	setBalance(t, s, uid, "coin", 0)
	if _, err = s.ReceivePresent(uid, byType["coin"].UUID, 2000, cat); err != nil {
		t.Fatalf("领取新手钻石: %v", err)
	}
	if b := balance(t, s, uid, "coin"); b != 99999 {
		t.Fatalf("coin=%d want 99999", b)
	}
	// 同设备复登是幂等登录不建新号 → 不重复预置。
	if _, err = s.QuickLogin("dev-p0-starter"); err != nil {
		t.Fatal(err)
	}
	var total int
	if err = s.DB().QueryRow(`SELECT count(*) FROM cn35_user_presents WHERE user_id=?`, uid).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("复登后礼物行=%d want 3（含已领）", total)
	}
}

func TestWaitingUnitPresentCapKeepsGiftAndBatchAtomic(t *testing.T) {
	s := openStore(t, t.TempDir())
	defer s.Close()
	cat := testCatalog(t)
	uid := seedAccount(t, s, "waiting-cap")
	clearPresents(t, s, uid)
	insertUnit(t, s, uid, 152, 1)
	if _, err := s.DB().Exec(`UPDATE cn35_player_units SET is_waiting=1,duplicated_count=15 WHERE user_id=? AND unit_id=152`, uid); err != nil {
		t.Fatal(err)
	}
	insertPresent(t, s, uid, "cap-unit", "unit", 152, 1, 0)
	insertPresent(t, s, uid, "cap-coin", "coin", 1, 50, 0)
	if _, err := s.ReceivePresent(uid, "cap-unit", 2000, cat); !errors.Is(err, cn35game.ErrPresentUnitCap) {
		t.Fatalf("single: %v", err)
	}
	if _, err := s.ReceiveAllPresents(uid, false, 2000, cat); !errors.Is(err, cn35game.ErrPresentUnitCap) {
		t.Fatalf("batch: %v", err)
	}
	var dup, received int
	if err := s.DB().QueryRow(`SELECT duplicated_count FROM cn35_player_units WHERE user_id=? AND unit_id=152`, uid).Scan(&dup); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM cn35_user_presents WHERE user_id=? AND received_at!=0`, uid).Scan(&received); err != nil {
		t.Fatal(err)
	}
	if dup != 15 || received != 0 {
		t.Fatalf("dup=%d received=%d", dup, received)
	}
}
