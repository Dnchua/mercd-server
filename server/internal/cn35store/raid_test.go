package cn35store

import (
	"path/filepath"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

func openRaidTestStore(t *testing.T, dbPath string) *Store {
	t.Helper()
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%s): %v", dbPath, err)
	}
	return st
}

func TestRaidMigration0054TablesExist(t *testing.T) {
	st := openRaidTestStore(t, filepath.Join(t.TempDir(), "raid0054.sqlite3"))
	defer st.Close()

	for _, tbl := range []string{
		"cn35_raid_profiles",
		"cn35_raid_instances",
		"cn35_raid_participants",
		"cn35_raid_sessions",
		"cn35_raid_lv_claims",
		"cn35_raid_pt_exchanges",
	} {
		var cnt int
		if err := st.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&cnt); err != nil {
			t.Fatalf("query table %s: %v", tbl, err)
		}
		if cnt != 1 {
			t.Fatalf("table %s count = %d, want 1", tbl, cnt)
		}
	}
}

func TestRaidStoreLifecycleAndPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "raid-life.sqlite3")
	st := openRaidTestStore(t, dbPath)

	uidA := seedAccount(t, st, "raid-owner-a")
	uidB := seedAccount(t, st, "raid-helper-b")
	grantFive(t, st, uidA)
	grantFive(t, st, uidB)

	// 提高 BP 上限到 600 供连续出击测试。
	for _, u := range []string{uidA, uidB} {
		if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET max_bp=600, bp=600 WHERE user_id=?`, u); err != nil {
			t.Fatal(err)
		}
	}

	g, err := st.CreateGuild(uidA, "暴走测试团", "2", "1", "", "")
	if err != nil {
		t.Fatalf("CreateGuild: %v", err)
	}
	// 让 B 加入同公会。
	if _, err := st.DB().Exec(`INSERT INTO cn35_guild_members (guild_id,user_id,role,joined_at) VALUES (?,?,1,'2026-11-14T00:00:00Z')`,
		g.ID, uidB); err != nil {
		t.Fatal(err)
	}

	t0 := int64(1_794_600_000)
	meta := cn35game.RaidMonsterMeta{MonsterID: 688, Name: "莫鲁菲涅", AttrID: 4}
	cfg := cn35game.RaidConfig{BossMonsterID: 688, TriggerRate: 0.5, TTLSeconds: 5400}

	// 1. roll >= TriggerRate 不触发；roll < TriggerRate 触发；已有活跃时互斥不重复触发。
	if _, spawned, err := st.MaybeSpawnRaidOnQuestClear(uidA, meta, cfg, 0.8, t0); err != nil || spawned {
		t.Fatalf("roll>=rate should not spawn: spawned=%v err=%v", spawned, err)
	}
	inst1, spawned, err := st.MaybeSpawnRaidOnQuestClear(uidA, meta, cfg, 0.2, t0)
	if err != nil || !spawned || inst1 == nil {
		t.Fatalf("expected spawn: inst=%v spawned=%v err=%v", inst1, spawned, err)
	}
	if inst1.Level != 1 || inst1.MaxPoint != 30_000 || inst1.CurrentPoint != 30_000 {
		t.Fatalf("unexpected Lv.1 raid: %+v", inst1)
	}
	if _, dup, err := st.MaybeSpawnRaidOnQuestClear(uidA, meta, cfg, 0.0, t0+10); err != nil || dup {
		t.Fatalf("singleton active raid must block second spawn: dup=%v err=%v", dup, err)
	}

	// 2. 超时 90 分钟后懒过期并允许再次遭遇；验证后再用活跃实例走完后续流程。
	has, err := st.RaidHasActive(uidA, t0+5401)
	if err != nil || has {
		t.Fatalf("expired raid should not be active: has=%v err=%v", has, err)
	}
	inst2, spawned, err := st.MaybeSpawnRaidOnQuestClear(uidA, meta, cfg, 0.1, t0+5401)
	if err != nil || !spawned || inst2 == nil {
		t.Fatalf("respawn after expire: err=%v spawned=%v", err, spawned)
	}

	// 3. 未自击前不可求援（ErrRaidNotAttacked）。
	if _, err := st.RaidCallHelp(uidA, "guild", false, meta, t0+5410); err != cn35game.ErrRaidNotAttacked {
		t.Fatalf("call_help before attack = %v, want ErrRaidNotAttacked", err)
	}

	// 4. 发现者首刀出击 + BP 幂等 + lose 扣血 12000 解锁求援。
	sessA1, _, _, _, err := st.RaidOpenBattle(uidA, inst2.RaidID, "normal", 1, 5, t0+5420)
	if err != nil || sessA1.BPCost != 30 || sessA1.FreeFirst {
		t.Fatalf("owner normal open: sess=%+v err=%v", sessA1, err)
	}
	if _, err := st.RaidPayBP(uidA, sessA1.BattleID, t0+5421); err != nil {
		t.Fatalf("RaidPayBP A1: %v", err)
	}
	if _, err := st.RaidPayBP(uidA, sessA1.BattleID, t0+5422); err != nil {
		t.Fatalf("RaidPayBP replay A1: %v", err)
	}
	var bpAfterPay int
	if err := st.DB().QueryRow(`SELECT bp FROM cn35_player_profiles WHERE user_id=?`, uidA).Scan(&bpAfterPay); err != nil {
		t.Fatal(err)
	}
	if bpAfterPay != 570 {
		t.Fatalf("BP after idempotent pay = %d, want 570", bpAfterPay)
	}
	loseA1, err := st.RaidReportLose(uidA, sessA1.BattleID, 12_000, meta, t0+5450)
	if err != nil || loseA1.Defeated || loseA1.Instance.CurrentPoint != 18_000 || !loseA1.Instance.Attacked {
		t.Fatalf("loseA1 = %+v err=%v", loseA1, err)
	}

	// 5. 求援（公会 + 聊天室），不再污染 cn35_messages 随机池，改由 RaidHelpMessages 按公会/公开作用域动态生成。
	if _, err := st.RaidCallHelp(uidA, "guild", false, meta, t0+5460); err != nil {
		t.Fatalf("RaidCallHelp guild: %v", err)
	}
	if _, err := st.RaidCallHelp(uidA, "community_topic", false, meta, t0+5461); err != nil {
		t.Fatalf("RaidCallHelp community: %v", err)
	}
	var msgCnt int
	if err := st.DB().QueryRow(`SELECT count(*) FROM cn35_messages WHERE category IN ('guild_comment','community_comment') AND created_at>=?`, t0+5460).Scan(&msgCnt); err != nil {
		t.Fatal(err)
	}
	if msgCnt != 0 {
		t.Fatalf("cn35_messages should not be polluted by raid call help, got count=%d", msgCnt)
	}
	helpMsgsB, err := st.RaidHelpMessages(uidB, t0+5462)
	if err != nil {
		t.Fatalf("RaidHelpMessages B: %v", err)
	}
	var sawGuildB, sawCommB bool
	for _, m := range helpMsgsB {
		if m.RaidID != inst2.RaidID {
			continue
		}
		if m.Category == "guild_comment" {
			sawGuildB = true
		}
		if m.Category == "community_comment" {
			sawCommB = true
		}
	}
	if !sawGuildB || !sawCommB {
		t.Fatalf("guild member B should see both guild_comment and community_comment, got %+v", helpMsgsB)
	}
	uidOutsider := seedAccount(t, st, "raid-outsider")
	helpMsgsOut, err := st.RaidHelpMessages(uidOutsider, t0+5462)
	if err != nil {
		t.Fatalf("RaidHelpMessages outsider: %v", err)
	}
	var sawGuildOut, sawCommOut bool
	for _, m := range helpMsgsOut {
		if m.RaidID != inst2.RaidID {
			continue
		}
		if m.Category == "guild_comment" {
			sawGuildOut = true
		}
		if m.Category == "community_comment" {
			sawCommOut = true
		}
	}
	if sawGuildOut || !sawCommOut {
		t.Fatalf("outsider must only see community_comment (guild=%v comm=%v): %+v", sawGuildOut, sawCommOut, helpMsgsOut)
	}
	ovB, err := st.RaidOverview(uidB, t0+5462)
	if err != nil || len(ovB.GuildHelps) != 1 || len(ovB.CommunityHelp) != 1 {
		t.Fatalf("ovB helps = guild:%d comm:%d err=%v", len(ovB.GuildHelps), len(ovB.CommunityHelp), err)
	}

	// 6. B 协力首刀享 BP 减免（normal=0, hard=30, very_hard=60），出刀后第二刀恢复原价。
	detB1, err := st.RaidShow(uidB, inst2.RaidID, t0+5463)
	if err != nil || !detB1.AssistFirstHit {
		t.Fatalf("detB1 = %+v err=%v", detB1, err)
	}
	sessB1, _, _, _, err := st.RaidOpenBattle(uidB, inst2.RaidID, "hard", 1, 5, t0+5464)
	if err != nil || sessB1.BPCost != 30 || !sessB1.FreeFirst {
		t.Fatalf("assist first hit hard: sess=%+v err=%v", sessB1, err)
	}
	if _, err := st.RaidPayBP(uidB, sessB1.BattleID, t0+5465); err != nil {
		t.Fatalf("RaidPayBP B1: %v", err)
	}
	loseB1, err := st.RaidReportLose(uidB, sessB1.BattleID, 5_000, meta, t0+5470)
	if err != nil || loseB1.Instance.CurrentPoint != 13_000 {
		t.Fatalf("loseB1 = %+v err=%v", loseB1, err)
	}
	detB2, err := st.RaidShow(uidB, inst2.RaidID, t0+5471)
	if err != nil || detB2.AssistFirstHit {
		t.Fatalf("second hit must lose AssistFirstHit: %+v err=%v", detB2, err)
	}
	sessB2, _, _, _, err := st.RaidOpenBattle(uidB, inst2.RaidID, "hard", 1, 5, t0+5472)
	if err != nil || sessB2.BPCost != 60 || sessB2.FreeFirst {
		t.Fatalf("assist second hit hard: sess=%+v err=%v", sessB2, err)
	}

	// 7. A 再出一刀并用 lose 上报剩余血量（致死击杀）→ 验证 raid_lv=2、Lv.1 奖励 30000 金币、双方 pt 入账。
	var gpBefore int64
	_ = st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, uidA).Scan(&gpBefore)
	sessA2, _, _, _, err := st.RaidOpenBattle(uidA, inst2.RaidID, "normal", 1, 5, t0+5480)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidPayBP(uidA, sessA2.BattleID, t0+5481); err != nil {
		t.Fatal(err)
	}
	killRes, err := st.RaidReportLose(uidA, sessA2.BattleID, 50_000, meta, t0+5490)
	if err != nil || !killRes.Defeated || killRes.ClearedLv != 1 || killRes.Instance.CurrentPoint != 0 {
		t.Fatalf("killRes = %+v err=%v", killRes, err)
	}
	if killRes.GrantedLvBonus == nil || killRes.GrantedLvBonus.TargetLv != 1 || killRes.GrantedLvBonus.Amount != 30000 {
		t.Fatalf("GrantedLvBonus = %+v", killRes.GrantedLvBonus)
	}
	var gpAfter int64
	_ = st.DB().QueryRow(`SELECT amount FROM cn35_player_balances WHERE user_id=? AND currency='gacha_point'`, uidA).Scan(&gpAfter)
	if gpAfter != gpBefore+30000 {
		t.Fatalf("Lv.1 gacha_point reward: %d -> %d, want +30000", gpBefore, gpAfter)
	}
	ovA, _ := st.RaidOverview(uidA, t0+5495)
	ovBAfter, _ := st.RaidOverview(uidB, t0+5495)
	// A: 12000+13000=25000 伤 -> 10pt; B: 5000 伤 -> 保底 10pt。
	if ovA.Profile.RaidLv != 2 || ovA.Profile.RaidPt != 10 || ovBAfter.Profile.RaidPt != 10 {
		t.Fatalf("post-kill profiles A=%+v B=%+v", ovA.Profile, ovBAfter.Profile)
	}

	// 8. RaidReportWin 与 Lv.2 晋升、NPC soloAssist、Giveup（权限+确认）与 pt 商店限购。
	inst3, spawned, err := st.MaybeSpawnRaidOnQuestClear(uidA, meta, cfg, 0.1, t0+5500)
	if err != nil || !spawned || inst3.Level != 2 {
		t.Fatalf("inst3 = %+v spawned=%v err=%v", inst3, spawned, err)
	}
	// 非发现者放走被拒；发现者放走成功。
	if err := st.RaidGiveup(uidB, inst3.RaidID, t0+5505); err != cn35game.ErrRaidNotOwner {
		t.Fatalf("non-owner giveup = %v, want ErrRaidNotOwner", err)
	}
	if err := st.RaidGiveup(uidA, inst3.RaidID, t0+5506); err != nil {
		t.Fatalf("owner giveup: %v", err)
	}

	// soloAssist 测试：重新遭遇，首刀扣血，call_help 触发 NPC 助战 30% 削血。
	inst4, spawned, err := st.MaybeSpawnRaidOnQuestClear(uidA, meta, cfg, 0.1, t0+5510)
	if err != nil || !spawned {
		t.Fatalf("inst4 spawn: %v", err)
	}
	sessA4, _, _, _, err := st.RaidOpenBattle(uidA, inst4.RaidID, "normal", 1, 5, t0+5511)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidPayBP(uidA, sessA4.BattleID, t0+5512); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidReportLose(uidA, sessA4.BattleID, 1_000, meta, t0+5515); err != nil {
		t.Fatal(err)
	}
	helped, err := st.RaidCallHelp(uidA, "guild", true, meta, t0+5520)
	if err != nil || helped.CurrentPoint >= inst4.MaxPoint-1_000 {
		t.Fatalf("soloAssist should deal NPC damage: helped=%+v err=%v", helped, err)
	}
	sessWin, _, _, _, err := st.RaidOpenBattle(uidA, inst4.RaidID, "normal", 1, 5, t0+5525)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidPayBP(uidA, sessWin.BattleID, t0+5526); err != nil {
		t.Fatal(err)
	}
	winRes, err := st.RaidReportWin(uidA, sessWin.BattleID, meta, t0+5530)
	if err != nil || !winRes.Defeated || winRes.ClearedLv != 2 {
		t.Fatalf("winRes = %+v err=%v", winRes, err)
	}

	// pt 商店兑换校验：注入 pt=600，兑换 slot_1（500pt 限量 5）成功 1 次后余 100pt，再兑报 ErrRaidNoPt；
	// 注入 pt=1000，兑换限量 1 的 slot_6? 改为 slot_2(ap_potion 100pt) 或直接测限购上限。
	if _, err := st.DB().Exec(`UPDATE cn35_raid_profiles SET raid_pt=600 WHERE user_id=?`, uidA); err != nil {
		t.Fatal(err)
	}
	goods := cn35game.RaidPtShopCatalog(688, "莫鲁菲涅")
	newPt, usedCnt, err := st.RaidExchangePt(uidA, goods[0].SlotID, 1, goods[0], t0+5540)
	if err != nil || newPt != 100 || usedCnt != 1 {
		t.Fatalf("exchange slot_1 = pt:%d used:%d err=%v", newPt, usedCnt, err)
	}
	if _, _, err := st.RaidExchangePt(uidA, goods[0].SlotID, 1, goods[0], t0+5541); err != cn35game.ErrRaidNoPt {
		t.Fatalf("insufficient pt = %v, want ErrRaidNoPt", err)
	}
	if _, err := st.DB().Exec(`UPDATE cn35_raid_profiles SET raid_pt=9000 WHERE user_id=?`, uidA); err != nil {
		t.Fatal(err)
	}
	// slot_6 限购 1，先买 1 次再买第 2 次报 ErrRaidSoldOut。
	if _, _, err := st.RaidExchangePt(uidA, goods[5].SlotID, 1, goods[5], t0+5542); err != nil {
		t.Fatalf("exchange slot_6 #1: %v", err)
	}
	if _, _, err := st.RaidExchangePt(uidA, goods[5].SlotID, 1, goods[5], t0+5543); err != cn35game.ErrRaidSoldOut {
		t.Fatalf("exchange slot_6 #2 = %v, want ErrRaidSoldOut", err)
	}

	// 无公会玩家 call_help guild 报错 ErrRaidNoGuild。
	uidSolo := seedAccount(t, st, "raid-solo-no-guild")
	grantFive(t, st, uidSolo)
	if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET max_bp=600, bp=600 WHERE user_id=?`, uidSolo); err != nil {
		t.Fatal(err)
	}
	instS, _, err := st.MaybeSpawnRaidOnQuestClear(uidSolo, meta, cfg, 0.1, t0+5550)
	if err != nil {
		t.Fatal(err)
	}
	sessS, _, _, _, _ := st.RaidOpenBattle(uidSolo, instS.RaidID, "normal", 1, 5, t0+5551)
	_, _ = st.RaidPayBP(uidSolo, sessS.BattleID, t0+5552)
	_, _ = st.RaidReportLose(uidSolo, sessS.BattleID, 100, meta, t0+5553)
	if _, err := st.RaidCallHelp(uidSolo, "guild", false, meta, t0+5554); err != cn35game.ErrRaidNoGuild {
		t.Fatalf("no guild call_help = %v, want ErrRaidNoGuild", err)
	}

	// 9. 关闭重开同库，确认全部暴走状态持久化保留。
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2 := openRaidTestStore(t, dbPath)
	defer st2.Close()

	ovPersist, err := st2.RaidOverview(uidA, t0+5600)
	if err != nil {
		t.Fatalf("reopen RaidOverview: %v", err)
	}
	if ovPersist.Profile.RaidLv != 3 || ovPersist.Profile.RaidPt != 4000 || ovPersist.Profile.KilledCount != 2 {
		t.Fatalf("persisted profile = %+v, want lv=3 pt=4000 killed=2", ovPersist.Profile)
	}
	if len(ovPersist.MyHistory) < 3 {
		t.Fatalf("persisted history len = %d, want >= 3", len(ovPersist.MyHistory))
	}
	lv2, claimed, err := st2.RaidLvClaims(uidA)
	if err != nil || lv2 != 3 || !claimed[1] {
		t.Fatalf("persisted lv claims: lv=%d claimed=%v err=%v", lv2, claimed, err)
	}
	ptShop, usedMap, err := st2.RaidPtShopState(uidA)
	if err != nil || ptShop != 4000 || usedMap[goods[0].SlotID] != 1 || usedMap[goods[5].SlotID] != 1 {
		t.Fatalf("persisted pt shop: pt=%d used=%v err=%v", ptShop, usedMap, err)
	}
}

func TestRaidHighHPBossNotOneShotByResult(t *testing.T) {
	st := openRaidTestStore(t, filepath.Join(t.TempDir(), "raid_high_hp.sqlite3"))
	defer st.Close()

	uid := seedAccount(t, st, "raid-lv233-owner")
	grantFive(t, st, uid)
	if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET bp=600, max_bp=600 WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	t0 := int64(1_794_700_000)
	maxHP := cn35game.RaidMaxPoint(233) // 10_059_161_640
	if _, err := st.DB().Exec(`INSERT INTO cn35_raid_instances
		(raid_id,owner_user_id,guild_id,monster_id,monster_name,attr_id,level,max_point,current_point,initial_power,attacked,help_guild,help_community,status,spawned_at,expired_at,finished_at)
		VALUES ('raid_store_lv233',?,'',688,'莫鲁菲涅',4,233,?,?,?,0,0,0,'active',?,?,0)`,
		uid, maxHP, maxHP, cn35game.RaidInitialPower(233), t0, t0+5400); err != nil {
		t.Fatal(err)
	}

	sess, _, _, _, err := st.RaidOpenBattle(uid, "raid_store_lv233", "very_hard", 1, 5, t0+10)
	if err != nil {
		t.Fatalf("RaidOpenBattle: %v", err)
	}
	if sess.BattleHP != cn35game.RaidSingleBattleHPCap {
		t.Fatalf("sess.BattleHP = %d, want %d", sess.BattleHP, cn35game.RaidSingleBattleHPCap)
	}
	if _, err := st.RaidPayBP(uid, sess.BattleID, t0+11); err != nil {
		t.Fatalf("RaidPayBP: %v", err)
	}
	meta := cn35game.RaidMonsterMeta{MonsterID: 688, Name: "莫鲁菲涅", AttrID: 4}
	res, err := st.RaidReportWin(uid, sess.BattleID, meta, t0+20)
	if err != nil {
		t.Fatalf("RaidReportWin: %v", err)
	}
	if res.Defeated {
		t.Fatalf("Lv.233 single win must NOT defeat 10B boss immediately")
	}
	if res.DamageDealt != cn35game.RaidSingleBattleHPCap {
		t.Fatalf("DamageDealt = %d, want %d", res.DamageDealt, cn35game.RaidSingleBattleHPCap)
	}
	wantRemain := maxHP - cn35game.RaidSingleBattleHPCap // 8_059_161_640
	if res.Instance.CurrentPoint != wantRemain || !res.Instance.Attacked || res.Instance.Status != "active" {
		t.Fatalf("after capped win instance = %+v, want CurrentPoint=%d attacked=true status=active", res.Instance, wantRemain)
	}
	ov, err := st.RaidOverview(uid, t0+21)
	if err != nil {
		t.Fatalf("RaidOverview: %v", err)
	}
	if ov.Profile.RaidPt != 0 || ov.Profile.RaidLv != 1 {
		t.Fatalf("non-defeat result must not grant pt or promote lv: %+v", ov.Profile)
	}
}

func TestRaidAccessControl(t *testing.T) {
	st := openRaidTestStore(t, filepath.Join(t.TempDir(), "raid_acl.sqlite3"))
	defer st.Close()

	uidA := seedAccount(t, st, "raid-acl-a")
	uidB := seedAccount(t, st, "raid-acl-b")
	uidC := seedAccount(t, st, "raid-acl-c")
	for _, u := range []string{uidA, uidB, uidC} {
		grantFive(t, st, u)
		if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET bp=600, max_bp=600 WHERE user_id=?`, u); err != nil {
			t.Fatal(err)
		}
	}
	gA, err := st.CreateGuild(uidA, "甲公会", "2", "1", "", "")
	if err != nil {
		t.Fatalf("CreateGuild A: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO cn35_guild_members (guild_id,user_id,role,joined_at) VALUES (?,?,1,'2026-11-14T00:00:00Z')`,
		gA.ID, uidB); err != nil {
		t.Fatal(err)
	}
	gC, err := st.CreateGuild(uidC, "乙公会", "2", "1", "", "")
	if err != nil {
		t.Fatalf("CreateGuild C: %v", err)
	}
	_ = gC

	t0 := int64(1_794_710_000)
	meta := cn35game.RaidMonsterMeta{MonsterID: 688, Name: "莫鲁菲涅", AttrID: 4}
	cfg := cn35game.RaidConfig{BossMonsterID: 688, TriggerRate: 1.0, TTLSeconds: 5400}
	inst, spawned, err := st.MaybeSpawnRaidOnQuestClear(uidA, meta, cfg, 0.0, t0)
	if err != nil || !spawned {
		t.Fatalf("spawn err=%v spawned=%v", err, spawned)
	}

	// 未求援前，同会 B 与异会 C 均被 ErrRaidForbidden 拦截。
	for _, viewer := range []string{uidB, uidC} {
		if _, err := st.RaidShow(viewer, inst.RaidID, t0+1); err != cn35game.ErrRaidForbidden {
			t.Fatalf("unshared RaidShow(%s) = %v, want ErrRaidForbidden", viewer, err)
		}
		if _, _, _, _, err := st.RaidOpenBattle(viewer, inst.RaidID, "normal", 1, 5, t0+1); err != cn35game.ErrRaidForbidden {
			t.Fatalf("unshared RaidOpenBattle(%s) = %v, want ErrRaidForbidden", viewer, err)
		}
		if _, err := st.RaidDamageList(viewer, inst.RaidID, t0+1); err != cn35game.ErrRaidForbidden {
			t.Fatalf("unshared RaidDamageList(%s) = %v, want ErrRaidForbidden", viewer, err)
		}
	}

	// A 攻击并仅发送公会求援后，同会 B 放行，异会 C 仍被拒绝。
	sessA, _, _, _, err := st.RaidOpenBattle(uidA, inst.RaidID, "normal", 1, 5, t0+2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidPayBP(uidA, sessA.BattleID, t0+3); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidReportLose(uidA, sessA.BattleID, 5_000, meta, t0+4); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidCallHelp(uidA, "guild", false, meta, t0+5); err != nil {
		t.Fatal(err)
	}

	if _, err := st.RaidShow(uidB, inst.RaidID, t0+6); err != nil {
		t.Fatalf("guild RaidShow(B) = %v", err)
	}
	if _, _, _, _, err := st.RaidOpenBattle(uidB, inst.RaidID, "normal", 1, 5, t0+6); err != nil {
		t.Fatalf("guild RaidOpenBattle(B) = %v", err)
	}
	if _, err := st.RaidDamageList(uidB, inst.RaidID, t0+6); err != nil {
		t.Fatalf("guild RaidDamageList(B) = %v", err)
	}
	if _, err := st.RaidShow(uidC, inst.RaidID, t0+6); err != cn35game.ErrRaidForbidden {
		t.Fatalf("guild-only RaidShow(C) = %v, want ErrRaidForbidden", err)
	}
	if _, _, _, _, err := st.RaidOpenBattle(uidC, inst.RaidID, "normal", 1, 5, t0+6); err != cn35game.ErrRaidForbidden {
		t.Fatalf("guild-only RaidOpenBattle(C) = %v, want ErrRaidForbidden", err)
	}
	if _, err := st.RaidDamageList(uidC, inst.RaidID, t0+6); err != cn35game.ErrRaidForbidden {
		t.Fatalf("guild-only RaidDamageList(C) = %v, want ErrRaidForbidden", err)
	}

	// 发送聊天室求援后，异会 C 亦可正常访问。
	if _, err := st.RaidCallHelp(uidA, "community_topic", false, meta, t0+7); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidShow(uidC, inst.RaidID, t0+8); err != nil {
		t.Fatalf("community RaidShow(C) = %v", err)
	}
	if _, _, _, _, err := st.RaidOpenBattle(uidC, inst.RaidID, "normal", 1, 5, t0+8); err != nil {
		t.Fatalf("community RaidOpenBattle(C) = %v", err)
	}
	if _, err := st.RaidDamageList(uidC, inst.RaidID, t0+8); err != nil {
		t.Fatalf("community RaidDamageList(C) = %v", err)
	}
}

func TestRaidAssistFirstHitConcurrencyAndPaidGuard(t *testing.T) {
	st := openRaidTestStore(t, filepath.Join(t.TempDir(), "raid_paid_guard.sqlite3"))
	defer st.Close()

	uidA := seedAccount(t, st, "raid-guard-a")
	uidB := seedAccount(t, st, "raid-guard-b")
	for _, u := range []string{uidA, uidB} {
		grantFive(t, st, u)
		if _, err := st.DB().Exec(`UPDATE cn35_player_profiles SET bp=600, max_bp=600 WHERE user_id=?`, u); err != nil {
			t.Fatal(err)
		}
	}
	g, err := st.CreateGuild(uidA, "守卫测试团", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO cn35_guild_members (guild_id,user_id,role,joined_at) VALUES (?,?,1,'2026-11-14T00:00:00Z')`,
		g.ID, uidB); err != nil {
		t.Fatal(err)
	}

	t0 := int64(1_794_720_000)
	meta := cn35game.RaidMonsterMeta{MonsterID: 688, Name: "莫鲁菲涅", AttrID: 4}
	cfg := cn35game.RaidConfig{BossMonsterID: 688, TriggerRate: 1.0, TTLSeconds: 5400}
	inst, _, err := st.MaybeSpawnRaidOnQuestClear(uidA, meta, cfg, 0.0, t0)
	if err != nil {
		t.Fatal(err)
	}
	sessA, _, _, _, err := st.RaidOpenBattle(uidA, inst.RaidID, "normal", 1, 5, t0+1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidPayBP(uidA, sessA.BattleID, t0+2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidReportLose(uidA, sessA.BattleID, 5_000, meta, t0+3); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RaidCallHelp(uidA, "guild", false, meta, t0+4); err != nil {
		t.Fatal(err)
	}

	// 1. 协力者 B 连续两次开战（未调 RaidPayBP）不会吞掉首刀免费资格。
	sessB1, _, _, _, err := st.RaidOpenBattle(uidB, inst.RaidID, "normal", 1, 5, t0+5)
	if err != nil || !sessB1.FreeFirst || sessB1.BPCost != 0 {
		t.Fatalf("first prepared session = %+v err=%v", sessB1, err)
	}
	sessB2, _, _, _, err := st.RaidOpenBattle(uidB, inst.RaidID, "normal", 1, 5, t0+6)
	if err != nil || !sessB2.FreeFirst || sessB2.BPCost != 0 {
		t.Fatalf("reopened prepared session must preserve free first hit: %+v err=%v", sessB2, err)
	}

	// 2. 即使 BPCost==0，未经过 RaidPayBP（bp_paid）时直接结算 lose/result 也必须被 ErrRaidBPPaidRequired 拒绝。
	if _, err := st.RaidReportLose(uidB, sessB2.BattleID, 1_000, meta, t0+7); err != cn35game.ErrRaidBPPaidRequired {
		t.Fatalf("unpaid RaidReportLose = %v, want ErrRaidBPPaidRequired", err)
	}
	if _, err := st.RaidReportWin(uidB, sessB2.BattleID, meta, t0+7); err != cn35game.ErrRaidBPPaidRequired {
		t.Fatalf("unpaid RaidReportWin = %v, want ErrRaidBPPaidRequired", err)
	}

	// 3. 调用 RaidPayBP 锁定首刀已付后，再次并发开战恢复 normal 原价（30 BP）。
	if _, err := st.RaidPayBP(uidB, sessB2.BattleID, t0+8); err != nil {
		t.Fatalf("RaidPayBP free first hit: %v", err)
	}
	_, wantNormalCost, _ := cn35game.RaidDifficultySpec("normal", false)
	sessB3, _, _, _, err := st.RaidOpenBattle(uidB, inst.RaidID, "normal", 1, 5, t0+9)
	if err != nil || sessB3.FreeFirst || sessB3.BPCost != wantNormalCost {
		t.Fatalf("concurrent open after bp_paid = %+v err=%v, want FreeFirst=false BPCost=%d", sessB3, err, wantNormalCost)
	}
}
