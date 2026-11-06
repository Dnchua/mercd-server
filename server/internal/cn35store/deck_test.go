package cn35store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/database"
)

func TestDeckDefaultsAreOwnedAndPersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mercd35.sqlite3")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	uidA := seedAccount(t, st, "deck-a")
	uidB := seedAccount(t, st, "deck-b")
	for _, id := range []int{336, 348} {
		if err := st.WithTx(func(tx *sql.Tx) error {
			return InsertUnitTx(tx, uidA, cn35game.UnitRecord{UnitID: id, Level: 1})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.WithTx(func(tx *sql.Tx) error {
		return InsertUnitTx(tx, uidB, cn35game.UnitRecord{UnitID: 152, Level: 1})
	}); err != nil {
		t.Fatal(err)
	}

	decksA, err := st.Decks(uidA)
	if err != nil {
		t.Fatal(err)
	}
	if len(decksA) != 2 || decksA[0].PartyID != "1" || decksA[1].PartyID != "2" {
		t.Fatalf("默认队伍 shape/order: %+v", decksA)
	}
	// A 持 336+348：起始五卡补发后首队 = 队长 + 库存升序。
	for i, id := range []string{"348", "152", "157", "161", "230"} {
		if decksA[0].Slots[i] != id {
			t.Fatalf("首队必须只由 A 当前持有单位生成且补空槽: %+v", decksA[0].Slots)
		}
	}
	for _, id := range decksA[1].Slots {
		if id != "" {
			t.Fatalf("次队默认空槽: %+v", decksA[1].Slots)
		}
	}
	decksB, err := st.Decks(uidB)
	if err != nil {
		t.Fatal(err)
	}
	// B 本只持 152：台账补发其余四卡后首队 = 队长 + 库存升序。
	for i, id := range []string{"348", "152", "157", "161", "230"} {
		if decksB[0].Slots[i] != id {
			t.Fatalf("B 首队包含非本账号数据: %+v", decksB[0].Slots)
		}
	}

	if err := st.SetDeckUnit(uidA, "2", 0, 336); err != nil {
		t.Fatalf("向本队空槽加入单位: %v", err)
	}
	// 同队已存在单位：1号位(slot 1)=152 与 2号位(slot 2)=157 对调
	if err := st.SetDeckUnit(uidA, "1", 2, 152); err != nil {
		t.Fatalf("同队槽位对调应成功: %v", err)
	}
	swappedA, err := st.Decks(uidA)
	if err != nil {
		t.Fatal(err)
	}
	if swappedA[0].Slots[1] != "157" || swappedA[0].Slots[2] != "152" {
		t.Fatalf("同队槽位对调结果错误: %+v", swappedA[0].Slots)
	}
	// 卸下 1 号位(slot 1, 现为 157) 变为空槽，且初始卡回填不得将其覆盖
	if err := st.ClearDeckUnit(uidA, "1", 1, 157); err != nil {
		t.Fatalf("卸下首队槽位应成功: %v", err)
	}
	afterClearA, err := st.Decks(uidA)
	if err != nil {
		t.Fatal(err)
	}
	if afterClearA[0].Slots[1] != "" {
		t.Fatalf("卸下首队初始卡后不得被自动回填覆盖: %+v", afterClearA[0].Slots)
	}
	// 空槽挪位：点空槽 1 号位(slot 1) 选择 2 号位(slot 2) 上的 152，应挪到 slot 1 且 slot 2 变空
	if err := st.SetDeckUnit(uidA, "1", 1, 152); err != nil {
		t.Fatalf("同队向空槽挪位应成功: %v", err)
	}
	afterMoveA, err := st.Decks(uidA)
	if err != nil {
		t.Fatal(err)
	}
	if afterMoveA[0].Slots[1] != "152" || afterMoveA[0].Slots[2] != "" {
		t.Fatalf("同队向空槽挪位结果错误: %+v", afterMoveA[0].Slots)
	}
	// 恢复首队满编供后续持久化校验
	if err := st.SetDeckUnit(uidA, "1", 2, 157); err != nil {
		t.Fatalf("恢复首队槽位失败: %v", err)
	}
	if err := st.SetDeckUnit(uidA, "1", 2, 396); !errors.Is(err, cn35game.ErrDeckUnitNotOwned) {
		t.Fatalf("跨账号模板拒绝: %v", err)
	}
	if err := st.SetDeckUnit(uidA, "99", 0, 336); !errors.Is(err, cn35game.ErrDeckNotFound) {
		t.Fatalf("伪造队伍拒绝: %v", err)
	}
	if err := st.SetDeckUnit(uidA, "1", 5, 336); !errors.Is(err, cn35game.ErrDeckSlotInvalid) {
		t.Fatalf("越界槽位拒绝: %v", err)
	}
	if err := st.ClearDeckUnit(uidA, "2", 0, 152); !errors.Is(err, cn35game.ErrDeckSlotConflict) {
		t.Fatalf("过期 off 请求拒绝: %v", err)
	}
	if err := st.ClearDeckUnit(uidA, "2", 0, 336); err != nil {
		t.Fatalf("清空精确槽位: %v", err)
	}
	if err := st.WithTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO cn35_player_deck_slots
			(user_id,party_id,slot_index,unit_id) VALUES (?,?,?,?)`, uidA, "2", 0, 152)
		return err
	}); err == nil {
		t.Fatal("复合 FK 接受了其他账号的模板")
	}
	var forbiddenMembers int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_player_deck_slots
		WHERE user_id=? AND party_id='2' AND slot_index=0 AND unit_id=152`, uidA).Scan(&forbiddenMembers); err != nil {
		t.Fatal(err)
	}
	if forbiddenMembers != 0 {
		t.Fatalf("FK 失败事务遗留非法槽位: count=%d", forbiddenMembers)
	}

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	persisted, err := st.Decks(uidA)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 2 || persisted[1].Slots[0] != "" || persisted[0].Slots[0] != "348" {
		t.Fatalf("重启后编队不一致: %+v", persisted)
	}
}

func TestDecksBackfillStarterPartyWhenEarlyReadEmptiedIt(t *testing.T) {
	st := openStore(t, t.TempDir())
	uid := seedAccount(t, st, "deck-late-grant")
	// 复现缺陷现场：编队结构已在库存为空时建立（首读早于单位入库），
	// 且该账号恰好未走到队长种子补发路径。
	if err := st.WithTx(func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for order := 0; order < cn35game.DefaultDeckCount; order++ {
			partyID := fmt.Sprint(order + 1)
			if _, err := tx.Exec(`INSERT INTO cn35_player_decks
				(user_id,party_id,display_order,created_at,updated_at) VALUES (?,?,?,?,?)`,
				uid, partyID, order, now, now); err != nil {
				return err
			}
			for slot := 0; slot < cn35game.DefaultDeckSlots; slot++ {
				if _, err := tx.Exec(`INSERT INTO cn35_player_deck_slots
					(user_id,party_id,slot_index,unit_id) VALUES (?,?,?,NULL)`,
					uid, partyID, slot); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{336, 348} {
		if err := st.WithTx(func(tx *sql.Tx) error {
			return InsertUnitTx(tx, uid, cn35game.UnitRecord{UnitID: id, Level: 1})
		}); err != nil {
			t.Fatal(err)
		}
	}
	decks, err := st.Decks(uid)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"348", "152", "157", "161", "230"} {
		if decks[0].Slots[i] != id {
			t.Fatalf("库存入库后应回填首队: %+v", decks[0].Slots)
		}
	}
	for _, id := range decks[1].Slots {
		if id != "" {
			t.Fatalf("回填不得触碰次队: %+v", decks[1].Slots)
		}
	}
	// 玩家已放置过单位的编队不得被回填改写。
	for slot := 0; slot < cn35game.DefaultDeckSlots; slot++ {
		if err := st.ClearDeckUnit(uid, "1", slot, []int{348, 152, 157, 161, 230}[slot]); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetDeckUnit(uid, "1", 0, 336); err != nil {
		t.Fatal(err)
	}
	decks, err = st.Decks(uid)
	if err != nil {
		t.Fatal(err)
	}
	// 含非起始卡（336）的编队视为已自定义，空槽位不得回填。
	wantTail := []string{"336", "", "", "", ""}
	for i, id := range wantTail {
		if decks[0].Slots[i] != id {
			t.Fatalf("已自定义编队必须保持: %+v", decks[0].Slots)
		}
	}
}

func TestDecksSeedLeaderForLegacyEmptyInventory(t *testing.T) {
	st := openStore(t, t.TempDir())
	uid := seedAccount(t, st, "deck-empty-inv")
	// 存量账号：建号早于五卡补发修复，库存与台账均为空——首读编队时
	// 须自愈为「持起始五卡 + 首队满编」，并留下台账防重发。
	decks, err := st.Decks(uid)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"348", "152", "157", "161", "230"}
	for i, id := range want {
		if decks[0].Slots[i] != id {
			t.Fatalf("空库存账号未补发起始编队: %+v", decks[0].Slots)
		}
	}
	var grants int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_inventory_grants
		WHERE user_id=? AND grant_key=?`, uid, cn35game.FiveStarGrantKey).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 5 {
		t.Fatalf("起始五卡台账缺失: %d", grants)
	}
	// 幂等：再次读取不得重复发放或改写玩家已见的编队。
	if _, err := st.Decks(uid); err != nil {
		t.Fatal(err)
	}
	var units int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_player_units WHERE user_id=?`, uid).Scan(&units); err != nil {
		t.Fatal(err)
	}
	if units != 5 {
		t.Fatalf("重复发放: units=%d", units)
	}
}

func TestDecksPreserveDisplayOrderBoundary(t *testing.T) {
	st := openStore(t, t.TempDir())
	uid := seedAccount(t, st, "deck-order")
	for _, id := range []int{336, 348} {
		if err := st.WithTx(func(tx *sql.Tx) error {
			return InsertUnitTx(tx, uid, cn35game.UnitRecord{UnitID: id, Level: 1})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.WithTx(func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		// Ordinary map encoding sorts these keys as 1,10,2; the persisted wheel
		// order is intentionally 2,10,1.
		for _, entry := range []struct {
			key   string
			order int
			unit  int
		}{{"2", 0, 336}, {"10", 1, 348}, {"1", 2, 336}} {
			if _, err := tx.Exec(`INSERT INTO cn35_player_decks
				(user_id,party_id,display_order,created_at,updated_at) VALUES (?,?,?,?,?)`,
				uid, entry.key, entry.order, now, now); err != nil {
				return err
			}
			for slot := 0; slot < cn35game.DefaultDeckSlots; slot++ {
				var unit any
				if slot == 0 {
					unit = entry.unit
				}
				if _, err := tx.Exec(`INSERT INTO cn35_player_deck_slots
					(user_id,party_id,slot_index,unit_id) VALUES (?,?,?,?)`,
					uid, entry.key, slot, unit); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	decks, err := st.Decks(uid)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{decks[0].PartyID, decks[1].PartyID, decks[2].PartyID}
	if fmt.Sprint(got) != "[2 10 1]" {
		t.Fatalf("party display order: %v", got)
	}
}

func TestDeckMigrationUpgradesRepresentative0007AccountAndInventory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-0007.sqlite3")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`CREATE TABLE schema_migrations
		(name TEXT PRIMARY KEY,sha256 TEXT NOT NULL,applied_at TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	names := []string{
		"0001_accounts.sql", "0002_player_core.sql", "0003_cn35.sql",
		"0004_cn35_guest_device.sql", "0005_cn35_inventory.sql",
		"0006_cn35_quest_progress.sql", "0007_cn35_quest_battle.sql",
	}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("..", "database", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(raw)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		sum := sha256.Sum256(raw)
		if _, err := tx.Exec(`INSERT INTO schema_migrations VALUES (?,?,?)`,
			name, fmt.Sprintf("%x", sum), "2026-09-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	userID, now := "legacy-deck-user", "2026-09-01T00:00:00Z"
	if _, err := tx.Exec(`INSERT INTO cn35_accounts
		(he_account_id,user_id,token,sk,is_new_user,tutorial_finish,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`, "he-legacy-deck", userID, "token", "sk", 0, 1, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO cn35_player_profiles
		(user_id,level,exp,max_exp,ap,max_ap,ap_updated_at,bp,max_bp,leader_unit_id,
		 login_bonus_receivable_sec,guild_name_snapshot,created_at,updated_at)
		VALUES (?,1,50,100,20,20,?,300,300,'348',0,'',?,?)`,
		userID, now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO cn35_player_units
		(user_id,unit_id,level,duplicated_count,protected,newcomer,monster_uuid,created_at,updated_at)
		VALUES (?,348,1,0,0,1,'',?,?)`, userID, now, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("0007 to latest Open: %v", err)
	}
	defer st.Close()
	var migrations, units, decks int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_player_units WHERE user_id=?`, userID).Scan(&units); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM cn35_player_decks WHERE user_id=?`, userID).Scan(&decks); err != nil {
		t.Fatal(err)
	}
	// 期望值取实际内嵌迁移数（0008/0009 及后续），避免每加一条迁移就改死数。
	ms, err := database.Status(context.Background(), st.DB())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != migrations || units != 1 || decks != 0 {
		t.Fatalf("升级应补齐全部待应用迁移且保留旧数据：applied=%d want=%d units=%d decks=%d",
			migrations, len(ms), units, decks)
	}
	// 0009 的 Size 列必须在旧库升级后存在（可空，旧行语义不变）。
	if _, err := st.DB().Exec(
		`SELECT scale,real_scale,scale_limit,is_rare FROM cn35_player_monsters LIMIT 0`); err != nil {
		t.Fatalf("0009 Size 列缺失: %v", err)
	}
	defaults, err := st.Decks(userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaults) != 2 || defaults[0].Slots[0] != "348" {
		t.Fatalf("旧账号首次读取的队伍只使用旧账号库存: %+v", defaults)
	}
}

func TestWaitBackRetryPreservesFirstDeckGap(t *testing.T) {
	st := openStore(t, t.TempDir())
	defer st.Close()
	uid := seedAccount(t, st, "deck-wait-gap")
	decks, err := st.Decks(uid)
	if err != nil {
		t.Fatal(err)
	}
	if decks[0].Slots[1] != "152" {
		t.Fatalf("unexpected starter deck %v", decks[0].Slots)
	}
	if err = st.WaitUnits(uid, []int{152}); err != nil {
		t.Fatal(err)
	}
	if err = st.WaitUnits(uid, []int{152}); err != nil {
		t.Fatalf("wait retry: %v", err)
	}
	decks, err = st.Decks(uid)
	if err != nil || decks[0].Slots[1] != "" {
		t.Fatalf("wait deck %+v: %v", decks, err)
	}
	if err = st.BackUnits(uid, []int{152}); err != nil {
		t.Fatal(err)
	}
	if err = st.BackUnits(uid, []int{152}); err != nil {
		t.Fatalf("back retry: %v", err)
	}
	decks, err = st.Decks(uid)
	if err != nil || decks[0].Slots[1] != "" {
		t.Fatalf("back deck %+v: %v", decks, err)
	}
}
