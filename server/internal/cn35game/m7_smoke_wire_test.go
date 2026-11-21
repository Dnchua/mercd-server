// m7_smoke_wire_test.go M7 全链路协议与 SQLite 持久化冒泡用例：
//  1. CN3.5 剧情包提取产物 11 章 / 533 话主线完整性与解密读面（无假名说话人）
//  2. 不可思议之泉每周期随机 5..100 徽章发放、持久化与重开不丢
//  3. 面包店兑换骑士契约书（5 钻=1 本）+ 契约书单抽 + 十连 9 折（9 本抽 10 次）
//  4. 国服 Wiki 33 池特殊招募每日 04:00(UTC+8) 轮换、四维主题筛选与共享保底
//  5. 3★/4★/5★ 契约书使用与 6 合 1 合成 + ★3/★4/★5 信（5 枚单抽，不传 count）
package cn35game_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35roster"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func m7FullServer(
	t *testing.T, stateDir string, seed bool,
	clockFn func() time.Time, ra *cn35game.RemoteAssets,
) (*httptest.Server, *cn35store.Store, cn35game.UnitCatalog) {
	t.Helper()
	cardsDir := filepath.Join("..", "..", "assets", "shared", "cards")
	cat, err := cardcatalog.Load(cardsDir)
	if err != nil {
		t.Fatalf("cardcatalog 加载: %v", err)
	}
	st, err := cn35store.Open(filepath.Join(stateDir, "mercd35.sqlite3"))
	if err != nil {
		t.Fatalf("cn35store.Open: %v", err)
	}
	assetsDir := filepath.Join("..", "..", "assets", "cn350")
	if seed {
		if _, _, err := st.PublishResourceConfig("m7-test", "M7 smoke publish",
			func(monsterID int) error { _, err := cat.Monster(monsterID); return err }); err != nil {
			t.Fatalf("发布资源配置: %v", err)
		}
		in, _, err := cn35roster.LoadImport(assetsDir, cardsDir)
		if err != nil {
			t.Fatalf("名册清单加载: %v", err)
		}
		if _, _, err := cn35game.SeedGachaRoster(st, in, "m7-test"); err != nil {
			t.Fatalf("名册种子化: %v", err)
		}
	}
	rcat := cn35game.NewRosterCatalog(cat, st)
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:          st,
		Sessions:          st,
		Inventory:         st,
		Decks:             st,
		Quests:            st,
		Battles:           st,
		Items:             st,
		Resources:         st,
		Medal:             st,
		Awaken:            st,
		ItemUse:           st,
		ItemExchange:      st,
		DailyShop:         st,
		LoginBonus:        st,
		Signin:            st,
		Gacha:             st,
		GachaConfig:       st,
		Catalog:           rcat,
		RemoteAssets:      ra,
		SpecialScoutClock: clockFn,
		ServerHost:        "127.0.0.1:18091",
		AssetsDir:         assetsDir,
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		st.Close()
	})
	return ts, st, rcat
}

func TestM7SmokeMainStoryCompletenessAndRead(t *testing.T) {
	assetsDir := filepath.Join("..", "..", "assets", "cn350")
	storiesDir := filepath.Join(assetsDir, "stories")
	if info, err := os.Stat(storiesDir); err != nil || !info.IsDir() {
		t.Skipf("本地尚未提取故事目录（跳过主线 533 话全量校验）: %v", err)
	}
	entries, err := os.ReadDir(storiesDir)
	if err != nil {
		t.Fatalf("读取 stories 目录: %v", err)
	}
	if len(entries) < 533 {
		t.Skipf("当前 stories 仅含 %d 文件，非完整 533 话提取态", len(entries))
	}
	remoteDir := filepath.Join("..", "..", "server-state-cn35", "remote_assets")
	if _, err := os.Stat(remoteDir); err != nil {
		remoteDir = filepath.Join("..", "..", "..", "inputs", "files")
		if _, err2 := os.Stat(remoteDir); err2 != nil {
			t.Skipf("remote_assets 与 inputs/files 均不存在: %v", err)
		}
	}
	ra := cn35game.NewRemoteAssets(remoteDir, assetsDir, t.TempDir(), nil)
	if err := ra.Scan(); err != nil {
		t.Fatalf("RemoteAssets 扫描失败: %v", err)
	}

	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		Accounts:     cn35game.NewMemoryAccounts(),
		Sessions:     cn35wire.NewMemoryStore(),
		ServerHost:   "127.0.0.1:18091",
		AssetsDir:    assetsDir,
		RemoteAssets: ra,
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	uid, sk, s0, s1 := establishSession(t, ts.URL, "m7-story")

	// 纪念版移植后（plans/cn35-story-memorial-port-2026-11-20）目录 = 主线 11 章 + 同伴故事 + 国服期活动章；
	// 本测试只对主线 11 章 533 话做完整性断言，其余章节要求非空即可。
	mainChapterIDs := map[int]bool{1: true, 2: true, 5: true, 11: true, 16: true, 21: true, 31: true, 35: true, 40: true, 46: true, 56: true}
	chData := gameData(t, ts.URL, "/chapters/", uid, sk, s0, s1, "")
	chapters := docList(t, chData, "chapters")
	if len(chapters) < 11 {
		t.Fatalf("章节数 = %d, want >= 11", len(chapters))
	}
	for i := 0; i < 11; i++ {
		cid := int(chapters[i].(map[string]any)["id"].(float64))
		if !mainChapterIDs[cid] {
			t.Fatalf("前 11 章应为主线章，第 %d 项 id=%d", i, cid)
		}
	}

	chListData := gameData(t, ts.URL, "/chapters/list", uid, sk, s0, s1, "")
	chObj, ok := chListData["chapters"].(map[string]any)
	if !ok {
		t.Fatalf("/chapters/list chapters 非对象: %v", chListData["chapters"])
	}
	listChapters, _ := chObj["quest"].([]any)
	if len(listChapters) != len(chapters) {
		t.Fatalf("/chapters/list quest 章节数 = %d, want %d", len(listChapters), len(chapters))
	}
	var listMain int
	if smap, ok := chListData["stories"].(map[string]any); ok {
		for k, rawList := range smap {
			cid, _ := strconv.Atoi(k)
			if arr, ok := rawList.([]any); ok && mainChapterIDs[cid] {
				listMain += len(arr)
			}
		}
	}
	if listMain != 533 {
		t.Fatalf("/chapters/list 主线话数 = %d, want 533", listMain)
	}

	// 平假名 + 片假名本体；不含中点「・」(U+30FB) 与长音「ー」(U+30FC)——纪念版中文译名常用「・」分隔。
	kanaRe := regexp.MustCompile(`[\x{3041}-\x{3096}\x{30A1}-\x{30FA}]`)
	mainStories := 0
	var sampleIDs []int
	for idx, chRaw := range chapters {
		ch := chRaw.(map[string]any)
		cid := int(ch["id"].(float64))
		sData := gameData(t, ts.URL, "/stories/list", uid, sk, s0, s1, "chapter_id="+strconv.Itoa(cid))
		rows := docList(t, sData, "stories_"+strconv.Itoa(cid))
		if len(rows) == 0 {
			t.Fatalf("第 %d 章故事为空", cid)
		}
		if !mainChapterIDs[cid] {
			continue
		}
		mainStories += len(rows)
		if idx == 0 || idx == 5 || idx == 10 {
			firstRow := rows[0].(map[string]any)
			lastRow := rows[len(rows)-1].(map[string]any)
			sampleIDs = append(sampleIDs, int(firstRow["id"].(float64)), int(lastRow["id"].(float64)))
		}
	}
	if mainStories != 533 {
		t.Fatalf("11 章主线话数合计 = %d, want 533", mainStories)
	}

	for _, sid := range sampleIDs {
		plain := gameCall(t, ts.URL, "/stories/read/"+strconv.Itoa(sid)+".json", uid, sk, s0, s1, "", "_method=GET")
		var doc map[string]any
		if err := json.Unmarshal(plain, &doc); err != nil {
			t.Fatalf("story %d 解密后非 JSON: %v", sid, err)
		}
		stObj, _ := doc["story"].(map[string]any)
		scenes, _ := stObj["scenes_map_array"].([]any)
		if len(scenes) == 0 {
			t.Fatalf("story %d scenes_map_array 为空", sid)
		}
		for _, item := range scenes {
			m, _ := item.(map[string]any)
			for _, k := range []string{"speaker", "name", "display_name"} {
				if sp, _ := m[k].(string); sp != "" && kanaRe.MatchString(sp) {
					t.Fatalf("story %d 含未转换假名说话人(%s): %q", sid, k, sp)
				}
			}
		}
	}
}

func TestM7SmokeMysteriousFountainRandomMedals(t *testing.T) {
	stateDir := t.TempDir()
	ts, st := springServer(t, stateDir)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "m7-spring-medal")

	var expectedMedalSum int64
	distinctMedals := map[int64]bool{}
	const cycles = 6
	for i := 0; i < cycles; i++ {
		if i > 0 {
			// 将既有台账时刻回拨 1 小时以模拟经历下一个 1 小时可领周期（避开同秒 claimed_at 唯一键）。
			if _, err := st.DB().Exec(`UPDATE cn35_spring_claims SET claimed_at=claimed_at-3600 WHERE user_id=?`, uid); err != nil {
				t.Fatal(err)
			}
		}
		springSetAnchor(t, st, uid, time.Now().Unix()-cn35game.SpringIntervalSec)
		data := gameData(t, ts.URL, "/users/receive_login_bonus", uid, sk, s0, s1, "")
		contents := springContentsOf(t, data)
		var cycleMedal int64
		foundMedalRow := false
		for _, raw := range contents {
			row := raw.(map[string]any)
			if row["type"] == "medal" {
				foundMedalRow = true
				cycleMedal = int64(row["count"].(float64))
				break
			}
		}
		if !foundMedalRow {
			t.Fatalf("第 %d 轮泉水领取缺 medal 行: %v", i+1, contents)
		}
		if cycleMedal < cn35game.SpringRules.MedalMin || cycleMedal > cn35game.SpringRules.MedalMax {
			t.Fatalf("第 %d 轮泉水徽章 %d 超出 [%d, %d]",
				i+1, cycleMedal, cn35game.SpringRules.MedalMin, cn35game.SpringRules.MedalMax)
		}
		expectedMedalSum += cycleMedal
		distinctMedals[cycleMedal] = true

		uMedal := int64(springUser(t, data)["medal"].(float64))
		if uMedal != expectedMedalSum {
			t.Fatalf("第 %d 轮 user.medal=%d want %d", i+1, uMedal, expectedMedalSum)
		}
	}
	if len(distinctMedals) < 2 {
		t.Fatalf("6 次随机抽取徽章值全部相同（疑似固定值未随机）: %v", distinctMedals)
	}

	var claimsMedal int64
	if err := st.DB().QueryRow(`SELECT COALESCE(SUM(medal),0) FROM cn35_spring_claims WHERE user_id=?`, uid).Scan(&claimsMedal); err != nil {
		t.Fatal(err)
	}
	if claimsMedal != expectedMedalSum {
		t.Fatalf("cn35_spring_claims.medal 累计=%d want %d", claimsMedal, expectedMedalSum)
	}
	if got := medalBalanceOf(t, st, uid); got != int(expectedMedalSum) {
		t.Fatalf("cn35_player_balances medal=%d want %d", got, expectedMedalSum)
	}

	// 关闭服务与数据库后重开同状态库，通过网络接口校验重启持久化不丢。
	ts.Close()
	st.Close()
	ts2, _ := springServer(t, stateDir)
	defer ts2.Close()
	uid2, sk2, s02, s12 := establishSession(t, ts2.URL, "m7-spring-medal")
	if uid2 != uid {
		t.Fatalf("重启重连 user_id 漂移: %s vs %s", uid2, uid)
	}
	show2 := gameData(t, ts2.URL, "/users/show_login_bonus", uid2, sk2, s02, s12, "")
	if got := int64(springUser(t, show2)["medal"].(float64)); got != expectedMedalSum {
		t.Fatalf("重开 SQLite 后 user.medal=%d want %d", got, expectedMedalSum)
	}
}

func TestM7SmokeBreadShopScoutBookAnd9Discount(t *testing.T) {
	ts, st, _ := m7FullServer(t, t.TempDir(), true, nil, nil)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "m7-scout-discount")
	setCoin(t, st, uid, 50)

	shop := gameData(t, ts.URL, "/red_diamond_shop/", uid, sk, s0, s1, "")
	fixedRows, ok := shop["red_diamond_shop_fixed"].([]any)
	if !ok || len(fixedRows) == 0 {
		t.Fatalf("面包店 red_diamond_shop_fixed 缺失: %v", shop)
	}
	var scoutRow map[string]any
	for _, r := range fixedRows {
		m := r.(map[string]any)
		if int(m["id"].(float64)) == 10000 {
			scoutRow = m
			break
		}
	}
	if scoutRow == nil {
		t.Fatalf("red_diamond_shop_fixed 缺 id=10000 骑士契约书: %v", fixedRows)
	}
	if int(scoutRow["cost"].(float64)) != 5 || scoutRow["type_name"] != "scout_book" || scoutRow["unlimited"] != true {
		t.Fatalf("骑士契约书商品规格不符: %v", scoutRow)
	}

	// 50 钻买 10 本骑士契约书
	buy := gameJSON(t, ts.URL, "/red_diamond_shop/buy", uid, sk, s0, s1, "id=10000&count=10", "_method=GET")
	if buy["result"] != true {
		t.Fatalf("购买骑士契约书失败: %v", buy)
	}
	if got := coinBalanceOf(t, st, uid); got != 0 {
		t.Fatalf("买 10 本后剩余钻石=%d want 0", got)
	}

	preset := gameData(t, ts.URL, "/users/preset_data.json", uid, sk, s0, s1, "")
	uDoc := preset["user"].(map[string]any)
	if int(uDoc["scout_book_1"].(float64)) != 10 {
		t.Fatalf("preset_data user.scout_book_1=%v want 10", uDoc["scout_book_1"])
	}
	sbMap := uDoc["scout_books"].(map[string]any)
	if int(sbMap["1"].(float64)) != 10 {
		t.Fatalf("preset_data user.scout_books['1']=%v want 10", sbMap["1"])
	}

	// 单抽消耗 1 本得 1 名同伴（剩 9 本）
	single := gameJSON(t, ts.URL, "/gachas/execute", uid, sk, s0, s1, "id=2&count=1&use_scout_book=true", "_method=GET")
	if single["status"] != "success" {
		t.Fatalf("契约书单抽失败: %v", single)
	}
	sData := single["data"].(map[string]any)
	if res := sData["temp_gacha_result"].([]any); len(res) != 1 {
		t.Fatalf("单抽应返回 1 人, 实际=%d", len(res))
	}
	if int(sData["user"].(map[string]any)["scout_book_1"].(float64)) != 9 {
		t.Fatalf("单抽后契约书=%v want 9", sData["user"].(map[string]any)["scout_book_1"])
	}

	// 9 折十连：count=9 消耗 9 本抽 10 次（剩 0 本，得 10 人）
	ten := gameJSON(t, ts.URL, "/gachas/execute", uid, sk, s0, s1, "id=2&count=9&use_scout_book=true", "_method=GET")
	if ten["status"] != "success" {
		t.Fatalf("契约书 9 折十连失败: %v", ten)
	}
	tData := ten["data"].(map[string]any)
	if res := tData["temp_gacha_result"].([]any); len(res) != 10 {
		t.Fatalf("9 折十连应抽出 10 名同伴, 实际=%d", len(res))
	}
	if int(tData["user"].(map[string]any)["scout_book_1"].(float64)) != 0 {
		t.Fatalf("十连后契约书应归零: %v", tData["user"].(map[string]any)["scout_book_1"])
	}
	if got := coinBalanceOf(t, st, uid); got != 0 {
		t.Fatalf("契约书抽卡不应改动钻石余额: %d", got)
	}

	var drawCount, totalCost int
	var currency string
	if err := st.DB().QueryRow(`SELECT draw_count, currency, total_cost FROM cn35_gacha_operations
		WHERE user_id=? ORDER BY operation_id DESC LIMIT 1`, uid).Scan(&drawCount, &currency, &totalCost); err != nil {
		t.Fatal(err)
	}
	if drawCount != 10 || currency != "scout_book_1" || totalCost != 9 {
		t.Fatalf("审计流水异常: draw_count=%d currency=%q total_cost=%d want 10/scout_book_1/9",
			drawCount, currency, totalCost)
	}

	// 余额归零后再抽：明确拒绝且无副作用
	opsBefore := gachaOpRows(t, st, uid)
	reject := gameJSON(t, ts.URL, "/gachas/execute", uid, sk, s0, s1, "id=2&count=1&use_scout_book=true", "_method=GET")
	if reject["status"] != "error" {
		t.Fatalf("契约书不足应被拒绝: %v", reject)
	}
	if got := gachaOpRows(t, st, uid); got != opsBefore {
		t.Fatalf("被拒请求不应增加操作记录: %d -> %d", opsBefore, got)
	}
}

func TestM7SmokeSpecialScout33DayRotationAndFilters(t *testing.T) {
	utc8 := time.FixedZone("UTC+8", 8*3600)
	var mu sync.Mutex
	curNow := time.Date(2026, 11, 16, 4, 0, 0, 0, utc8)
	setNow := func(tm time.Time) {
		mu.Lock()
		curNow = tm
		mu.Unlock()
	}
	getNow := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return curNow
	}

	ts, st, cat := m7FullServer(t, t.TempDir(), true, getNow, nil)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "m7-special-33")

	slot1PoolID := func() string {
		data := gameData(t, ts.URL, "/gachas", uid, sk, s0, s1, "")
		for _, raw := range data["gachas"].([]any) {
			p := raw.(map[string]any)
			if int(p["list_type_id"].(float64)) == 0 {
				return idStr(p["id"])
			}
		}
		t.Fatalf("/gachas 缺少槽 1（list_type_id=0）特殊招募池: %v", data["gachas"])
		return ""
	}

	// 验证东八区 03:59:59 vs 04:00:00 日切边界
	baseDay := time.Date(2026, 11, 16, 4, 0, 0, 0, utc8)
	setNow(baseDay.Add(-1 * time.Second))
	pidBeforeCut := slot1PoolID()
	setNow(baseDay)
	pidAtCut := slot1PoolID()
	if pidBeforeCut == pidAtCut {
		t.Fatalf("UTC+8 04:00 切日前后未切换卡池: before=%s at=%s", pidBeforeCut, pidAtCut)
	}

	// 连续遍历 33 天，收集完整 101..133 轮换周期（不重不漏）
	seenPools := map[string]cn35game.SpecialScoutTheme{}
	themeDayByID := map[string]time.Time{}
	for d := 0; d < cn35game.SpecialScoutRotationCountForTest(); d++ {
		dayTime := baseDay.Add(time.Duration(d) * 24 * time.Hour)
		setNow(dayTime)
		pid := slot1PoolID()
		var matched *cn35game.SpecialScoutTheme
		for i := range cn35game.SpecialScoutRotation {
			if cn35game.SpecialScoutRotation[i].PoolID == pid {
				th := cn35game.SpecialScoutRotation[i]
				matched = &th
				break
			}
		}
		if matched == nil {
			t.Fatalf("第 %d 天槽 1 池 %q 不在 33 池轮换表内", d, pid)
		}
		if _, dup := seenPools[pid]; dup {
			t.Fatalf("33 天轮换第 %d 天出现重复池 %s", d, pid)
		}
		seenPools[pid] = *matched
		themeDayByID[pid] = dayTime
	}
	if len(seenPools) != 33 {
		t.Fatalf("33 天轮换仅覆盖 %d 池, want 33", len(seenPools))
	}

	// 对四大系列代表池（101 炎属性 / 106 斩击 / 113 沙漠 / 132 男性）执行端到端页签校验 + 9 折十连筛选校验 + 跨日保底累计
	targets := []string{"101", "106", "113", "132"}
	for idx, pid := range targets {
		setNow(themeDayByID[pid])
		th := seenPools[pid]

		// 校验各特殊招募端点返回有效结构
		pickup := gameData(t, ts.URL, "/gachas/pickup_gacha/", uid, sk, s0, s1, "")
		if pArr, _ := pickup["pickup_gacha"].([]any); len(pArr) != 1 || idStr(pArr[0].(map[string]any)["id"]) != pid {
			t.Fatalf("池 %s /gachas/pickup_gacha/ 异常: %v", pid, pickup["pickup_gacha"])
		}
		chooseAttr := gameData(t, ts.URL, "/gachas/choose_attr_page/", uid, sk, s0, s1, "")
		if cpp, _ := chooseAttr["country_pick_page"].(map[string]any); cpp["pool_id"] != pid {
			t.Fatalf("池 %s /gachas/choose_attr_page/ country_pick_page=%v", pid, cpp)
		}
		chooseCountry := gameData(t, ts.URL, "/gachas/choose_country_only_page/", uid, sk, s0, s1, "")
		if cpp, _ := chooseCountry["country_pick_page"].(map[string]any); cpp["pool_id"] != pid {
			t.Fatalf("池 %s /gachas/choose_country_only_page/ country_pick_page=%v", pid, cpp)
		}
		weaponDoc := gameData(t, ts.URL, "/gachas/weapon_gacha/", uid, sk, s0, s1, "")
		if wArr, _ := weaponDoc["weapon_gacha"].([]any); len(wArr) != 1 {
			t.Fatalf("池 %s /gachas/weapon_gacha/ 异常: %v", pid, weaponDoc["weapon_gacha"])
		}
		showDoc := gameData(t, ts.URL, "/gachas/show/", uid, sk, s0, s1, "")
		if bArr, _ := showDoc["boys_gacha"].([]any); len(bArr) != 1 {
			t.Fatalf("/gachas/show/ 缺 boys_gacha: %v", showDoc["boys_gacha"])
		}
		if gArr, _ := showDoc["girls_gacha"].([]any); len(gArr) != 1 {
			t.Fatalf("/gachas/show/ 缺 girls_gacha: %v", showDoc["girls_gacha"])
		}

		// 给 45 钻买 9 本契约书（9 折十连）
		setCoin(t, st, uid, 45)
		buy := gameJSON(t, ts.URL, "/red_diamond_shop/buy", uid, sk, s0, s1, "id=10000&count=9", "_method=GET")
		if buy["result"] != true {
			t.Fatalf("池 %s 购买契约书失败: %v", pid, buy)
		}

		exec := gameJSON(t, ts.URL, "/gachas/execute", uid, sk, s0, s1,
			"id="+pid+"&count=9&use_scout_book=true", "_method=GET")
		if exec["status"] != "success" {
			t.Fatalf("池 %s 9 折十连执行失败: %v", pid, exec)
		}
		results := exec["data"].(map[string]any)["temp_gacha_result"].([]any)
		if len(results) != 10 {
			t.Fatalf("池 %s 十连产出 %d 人, want 10", pid, len(results))
		}
		for _, rRaw := range results {
			entry := rRaw.(map[string]any)
			unitID := int(entry["id"].(float64))
			u, uerr := cat.Unit(unitID)
			if uerr != nil || !th.Match(u) {
				t.Fatalf("池 %s（%s）抽出池外同伴 unit_id=%d (%+v)", pid, th.Name, unitID, u)
			}
			rare := int(entry["rare_type_id"].(float64))
			if rare != 30 && rare != 40 && rare != 50 {
				t.Fatalf("池 %s 抽出非法星级 %d (unit_id=%d)", pid, rare, unitID)
			}
		}

		// 验证 cn350-limited-up 保底跨日共用递增（10 抽或重置后再加）
		pityRow, ok, err := st.GachaPity(uid, "cn350-limited-up")
		if err != nil || !ok {
			t.Fatalf("第 %d 池 %s 缺 cn350-limited-up 保底行: ok=%v err=%v", idx, pid, ok, err)
		}
		if pityRow.N5 < 0 || pityRow.N5 > 80 {
			t.Fatalf("cn350-limited-up n5=%d 越界", pityRow.N5)
		}
	}
}

func TestM7SmokeStarScoutBooksAndTicketGacha(t *testing.T) {
	ts, st, _ := m7FullServer(t, t.TempDir(), true, nil, nil)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "m7-books-tickets")

	// 1) 发放 7 本 3★ 契约书，验证背包投影 + 使用 1 本 + 6 合 1 得 4★ 契约书 + 使用 4★ 契约书
	if err := st.GrantItem(uid, "scout_book_3star", 7, "m7-test", "M7星级契约书冒泡", "m7-sb3-1"); err != nil {
		t.Fatalf("发放 3★ 契约书: %v", err)
	}
	itemsData := gameData(t, ts.URL, "/user_items/", uid, sk, s0, s1, "")
	cat6 := itemsData["user_items_hash"].(map[string]any)["6"].([]any)
	foundBook3 := false
	for _, raw := range cat6 {
		m := raw.(map[string]any)
		if int(m["id"].(float64)) == 100015 {
			foundBook3 = true
			if m["code"] != "scout_book" || int(m["stock_count"].(float64)) != 7 {
				t.Fatalf("3★ 契约书行字段异常: %v", m)
			}
		}
	}
	if !foundBook3 {
		t.Fatalf("user_items_hash['6'] 未投影 100015: %v", cat6)
	}

	use3 := gameJSON(t, ts.URL, "/user_items/use/", uid, sk, s0, s1, "id=100015&count=1", "_method=GET")
	if use3["status"] != "success" {
		t.Fatalf("使用 3★ 契约书失败: %v", use3)
	}
	res3 := use3["data"].(map[string]any)["temp_gacha_result"].([]any)
	if len(res3) != 1 || int(res3[0].(map[string]any)["rare_type_id"].(float64)) != 30 {
		t.Fatalf("3★ 契约书产出稀有度不符: %v", res3)
	}

	// 剩余 6 本 6 合 1 合成 4★ 契约书（100011）
	ex := gameJSON(t, ts.URL, "/gachas/exchange_scout_book", uid, sk, s0, s1, "item_id=100015", "_method=GET")
	if ex["status"] != "success" {
		t.Fatalf("6 合 1 合成失败: %v", ex)
	}
	cat6After := ex["data"].(map[string]any)["user_items_hash"].(map[string]any)["6"].([]any)
	var cnt3, cnt4 int
	for _, raw := range cat6After {
		m := raw.(map[string]any)
		switch int(m["id"].(float64)) {
		case 100015:
			cnt3 = int(m["stock_count"].(float64))
		case 100011:
			cnt4 = int(m["stock_count"].(float64))
		}
	}
	if cnt3 != 0 || cnt4 != 1 {
		t.Fatalf("6 合 1 后库存异常: 3★=%d 4★=%d", cnt3, cnt4)
	}

	use4 := gameJSON(t, ts.URL, "/user_items/use/", uid, sk, s0, s1, "id=100011&count=1", "_method=GET")
	if use4["status"] != "success" {
		t.Fatalf("使用 4★ 契约书失败: %v", use4)
	}
	res4 := use4["data"].(map[string]any)["temp_gacha_result"].([]any)
	if len(res4) != 1 || int(res4[0].(map[string]any)["rare_type_id"].(float64)) != 40 {
		t.Fatalf("4★ 契约书产出稀有度不符: %v", res4)
	}

	// 2) 发放各 5 枚 ★3/★4/★5 信，核验 category 7 映射与 ticket_available_list，再不带 count 执行 /gachas/execute?id=11|12|13
	for _, tc := range []struct {
		key      string
		ticketID int
		wantRare int
	}{
		{"gacha_ticket_11", 11, 30},
		{"gacha_ticket_12", 12, 40},
		{"gacha_ticket_13", 13, 50},
	} {
		if err := st.GrantItem(uid, tc.key, 5, "m7-test", "M7星级信冒泡", "m7-tk-"+tc.key); err != nil {
			t.Fatalf("发放 %s: %v", tc.key, err)
		}
	}

	tItems := gameData(t, ts.URL, "/user_items/", uid, sk, s0, s1, "")
	cat7 := tItems["user_items_hash"].(map[string]any)["7"].([]any)
	seenCat7 := map[int]bool{}
	for _, raw := range cat7 {
		m := raw.(map[string]any)
		id := int(m["id"].(float64))
		if m["code"] == "ticket" && int(m["stock_count"].(float64)) == 5 {
			seenCat7[id] = true
		}
	}
	for _, tid := range []int{11, 12, 13} {
		if !seenCat7[tid] {
			t.Fatalf("user_items_hash['7'] 未把 %s 映射为 id=%d code=ticket: %v",
				"gacha_ticket_"+strconv.Itoa(tid), tid, cat7)
		}
	}

	// 契约对齐原服录播 mlk-capture-201711：data 下平铺 ticket_gacha_11/12/13 对象
	// （UserItemsV2 按钮 data 键直读），available_list_option 指向 available_<id>。
	tal := gameData(t, ts.URL, "/gachas/ticket_available_list", uid, sk, s0, s1, "")
	if _, legacy := tal["ticket_available_list"]; legacy {
		t.Fatalf("ticket_available_list 不应下发自造数组键（原服为 ticket_gacha_1x 平铺）: %v", tal)
	}
	for _, tc := range []struct {
		pid      string
		name     string
		ball     []float64
		wantRare int
	}{
		{"11", "★3 信", []float64{1, 3}, 30},
		{"12", "★4 信", []float64{1, 3}, 40},
		{"13", "★5 信", []float64{1, 2}, 50},
	} {
		m, ok := tal["ticket_gacha_"+tc.pid].(map[string]any)
		if !ok {
			t.Fatalf("缺 ticket_gacha_%s: %v", tc.pid, tal)
		}
		if idStr(m["id"]) != tc.pid || idStr(m["list_type_id"]) != tc.pid || m["name"] != tc.name ||
			m["type"] != "Gacha::TicketOnly" || int(m["ticket_count"].(float64)) != 5 ||
			int(m["max_executable_count"].(float64)) != 1 || int(m["max_execute_bonus"].(float64)) != 0 {
			t.Fatalf("ticket_gacha_%s 字段与原服录播不符: %v", tc.pid, m)
		}
		ball := m["ball_type_id_array"].([]any)
		if len(ball) != 2 || ball[0].(float64) != tc.ball[0] || ball[1].(float64) != tc.ball[1] {
			t.Fatalf("ticket_gacha_%s ball_type_id_array=%v want %v", tc.pid, ball, tc.ball)
		}
		opt := m["available_list_option"].(map[string]any)
		key := "available_" + tc.pid
		if opt["parts"].(map[string]any)["ListParts"].(map[string]any)["data_key"] != key {
			t.Fatalf("ticket_gacha_%s ListParts.data_key 应为 %s: %v", tc.pid, key, opt)
		}
		path := opt["requireData"].(map[string]any)[key].(map[string]any)["path"]
		if path != "/gachas/available_unit_list?id="+tc.pid {
			t.Fatalf("ticket_gacha_%s requireData path=%v", tc.pid, path)
		}
		// 名单页读 available_<id>：只含目标星级，且非空。
		avail := gameData(t, ts.URL, "/gachas/available_unit_list", uid, sk, s0, s1, "id="+tc.pid)
		rows, _ := avail[key].([]any)
		if len(rows) == 0 {
			t.Fatalf("%s 名单为空", key)
		}
		for _, raw := range rows {
			if r := int(raw.(map[string]any)["rare_type_id"].(float64)); r != tc.wantRare {
				t.Fatalf("%s 含非目标星级单位 rare=%d want %d", key, r, tc.wantRare)
			}
		}
	}

	for _, tc := range []struct {
		tid      int
		wantRare int
	}{
		{11, 30},
		{12, 40},
		{13, 50},
	} {
		exec := gameJSON(t, ts.URL, "/gachas/execute", uid, sk, s0, s1,
			"id="+strconv.Itoa(tc.tid), "_method=GET")
		if exec["status"] != "success" {
			t.Fatalf("★信 id=%d 执行失败: %v", tc.tid, exec)
		}
		res := exec["data"].(map[string]any)["temp_gacha_result"].([]any)
		if len(res) != 1 || int(res[0].(map[string]any)["rare_type_id"].(float64)) != tc.wantRare {
			t.Fatalf("★信 id=%d 产出稀有度=%v want %d", tc.tid, res, tc.wantRare)
		}
		// 扣完 5 枚后再次抽应被拒
		re := gameJSON(t, ts.URL, "/gachas/execute", uid, sk, s0, s1,
			"id="+strconv.Itoa(tc.tid), "_method=GET")
		if re["status"] != "error" {
			t.Fatalf("★信 id=%d 耗尽后应拒绝: %v", tc.tid, re)
		}
	}
}
