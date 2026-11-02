// guild_social_wire_test.go GUILDCLOSE350 GC-07/GC-08：社交面真实 HTTP 回归。
//
// 与 mlk_guild_contract_test.go 的分工：
//   - 契约测试只校验 20 条原服录包路径的字段类型（不含 waiting 系列动作）；
//   - 本测试覆盖**动作链**与写面事务：登记等待邀请 → 邀请 → 被邀者看到邀请 →
//     走原服 join_approve_url 接受 → 状态收敛；会战留言写入 → 计数键变化；
//     以及权限/满员/重复/未开放类型的显式拒绝（不得空成功）。
package cn35game_test

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// socialTestServer 建一个装配真实 cn35store 的 CN3.5 服务（单机域默认开启）。
func socialTestServer(t *testing.T) (*httptest.Server, *cn35store.Store) {
	t.Helper()
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := cn35store.Open(filepath.Join(t.TempDir(), "guild-social.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ConfigureGuildSolo(cn35store.DefaultGuildSoloSettings); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 45, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{
		Accounts: st, Sessions: st, Inventory: st, Decks: st, Quests: st,
		Battles: st, Guild: st, Catalog: cat,
		AssetsDir:  filepath.Join("..", "..", "assets", "cn350"),
		ServerHost: "127.0.0.1:18091",
		GuildClock: func() time.Time { return now },
	})
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); st.Close() })
	return ts, st
}

// socialData 取响应 data 段（失败直接失败，避免 nil 断言噪音）。
func socialData(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	data, ok := doc["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺 data 段: %v", doc)
	}
	return data
}

// socialModal 断言响应是业务弹窗（显式拒绝），返回弹窗正文。
func socialModal(t *testing.T, doc map[string]any) string {
	t.Helper()
	modal, ok := doc["modal"].(map[string]any)
	if !ok {
		t.Fatalf("期望业务弹窗（显式拒绝），实际回包: %v", doc)
	}
	body, _ := modal["body"].(string)
	return body
}

// socialRows 取数组键。
func socialRows(t *testing.T, data map[string]any, key string) []any {
	t.Helper()
	rows, ok := data[key].([]any)
	if !ok {
		t.Fatalf("data.%s 不是数组: %v", key, data[key])
	}
	return rows
}

func TestGuildUsersWire(t *testing.T) {
	ts, st := socialTestServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "gc-t4-users-master")
	other, osk, o0, o1 := establishSession(t, ts.URL, "gc-t4-users-other")
	g, err := st.CreateGuild(uid, "原路径会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	doc := gameJSON(t, ts.URL, "/guilds/users/", uid, sk, s0, s1,
		"id="+url.QueryEscape(g.ID), "_method=GET")
	data := socialData(t, doc)
	rows := socialRows(t, data, "guild_users")
	if len(rows) != 1 {
		t.Fatalf("成员行数=%d, 期望 1", len(rows))
	}
	// 原服同响应双键：guild_users + guild_users_<gid>（录包 guilds_users.json）。
	if dynRows := socialRows(t, data, "guild_users_"+g.ID); len(dynRows) != len(rows) {
		t.Fatalf("动态键行数不一致: %d vs %d", len(dynRows), len(rows))
	}
	row := rows[0].(map[string]any)
	for _, k := range []string{"name", "level", "leader_unit_id", "logged_at",
		"guild_joined_at", "square_icon_asset_name", "id", "user_status",
		"guild_role", "name_and_role", "user_detail_message"} {
		if _, ok := row[k]; !ok {
			t.Fatalf("成员行缺键 %s: %v", k, row)
		}
	}
	if row["id"] != uid {
		t.Fatalf("成员 id=%v, 期望 %v", row["id"], uid)
	}
	// 客户端只读这些键，禁发未经证实的 point/ranking 原始值（见 wire 注释）。
	if _, ok := row["point"]; ok {
		t.Fatalf("不应下发 point（客户端不消费）: %v", row)
	}
	role, _ := row["guild_role"].(map[string]any)
	if role["id"] != float64(3) || role["name"] != "公会长" {
		t.Fatalf("guild_role=%v", row["guild_role"])
	}
	// 只校验 name(role) 形状，不校验账号名（账号名由登录链生成）。
	if !strings.HasSuffix(row["name_and_role"].(string), "(公会长)") {
		t.Fatalf("name_and_role=%v", row["name_and_role"])
	}
	// 无贡献成员：user_status 三段、后两段是 "-"（原服 '268\n-\n-' 形状）。
	if row["user_status"] != "1\n-\n-" {
		t.Fatalf("user_status=%v", row["user_status"])
	}
	detail := row["user_detail_message"].(string)
	if !strings.HasPrefix(detail, "name=UserPage&base=Common/Pages/UserPage&method=Stack&additional_require_data_path=%2Fusers%2Fshow%3Fid%3D") {
		t.Fatalf("user_detail_message=%v", detail)
	}
	if !strings.HasSuffix(detail, uid) {
		t.Fatalf("user_detail_message 未带目标 id: %v", detail)
	}
	// 非本会成员访问：显式拒绝（不返回空列表冒充成功）。
	socialModal(t, gameJSON(t, ts.URL, "/guilds/users/", other, osk, o0, o1,
		"id="+url.QueryEscape(g.ID), "_method=GET"))
	// 未入会者不带参数：属于自己公会的入口，同样显式拒绝。
	socialModal(t, gameJSON(t, ts.URL, "/guilds/users/", other, osk, o0, o1, "", "_method=GET"))
}

func TestGuildWaitingLifecycleWire(t *testing.T) {
	ts, st := socialTestServer(t)
	masterID, mSK, ms0, ms1 := establishSession(t, ts.URL, "gc-t4-wait-master")
	waiterID, wSK, ws0, ws1 := establishSession(t, ts.URL, "gc-t4-wait-user")
	g, err := st.CreateGuild(masterID, "邀请链会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// 1) 本人登记等待邀请（客户端 GuildNotJoinedPageBase.cs:83）。
	doc := gameJSON(t, ts.URL, "/waiting_users/add.json", waiterID, wSK, ws0, ws1,
		"", "_method=POST&play_type_id=3")
	user := socialData(t, doc)["user"].(map[string]any)
	if user["is_waiting"] != true {
		t.Fatalf("登记后 user.is_waiting 应为 true: %v", user)
	}

	// 2) 会长打开邀请页：本人登记的行必须出现，字段形状对齐原服。
	doc = gameJSON(t, ts.URL, "/waiting_users", masterID, mSK, ms0, ms1, "", "_method=GET")
	data := socialData(t, doc)
	rows := socialRows(t, data, "waiting_users")
	if len(rows) == 0 {
		t.Fatal("等待邀请列表为空（应含真人登记与单机 NPC 候补）")
	}
	var target map[string]any
	for _, r := range rows {
		m := r.(map[string]any)
		if m["invite_url"] == "/waiting_users/invite/?user_id="+waiterID {
			target = m
			break
		}
	}
	if target == nil {
		t.Fatalf("列表里找不到已登记账号: %v", rows)
	}
	if target["play_type_id"] != "3" {
		t.Fatalf("play_type_id 应为字符串 \"3\": %v", target["play_type_id"])
	}
	if target["confirm_text"] != "邀请[eb8900]"+target["name"].(string)+"[-]吗？" {
		t.Fatalf("confirm_text=%v", target["confirm_text"])
	}
	if !strings.HasSuffix(target["user_status"].(string), "\n精益求精") {
		t.Fatalf("user_status=%v", target["user_status"])
	}
	for _, k := range []string{"name", "level", "leader_unit_id", "logged_at",
		"created_at", "square_icon_asset_name", "user_status", "user_detail_message"} {
		if _, ok := target[k]; !ok {
			t.Fatalf("候补行缺键 %s: %v", k, target)
		}
	}

	// 3) 邀请（原服 invite_url 形状）→ 幂等重复邀请不报错、不产生第二行。
	doc = gameJSON(t, ts.URL, "/waiting_users/invite/", masterID, mSK, ms0, ms1,
		"user_id="+url.QueryEscape(waiterID), "")
	if _, bad := doc["modal"]; bad {
		t.Fatalf("邀请失败: %v", doc)
	}
	doc = gameJSON(t, ts.URL, "/waiting_users/invite/", masterID, mSK, ms0, ms1,
		"user_id="+url.QueryEscape(waiterID), "")
	if _, bad := doc["modal"]; bad {
		t.Fatalf("重复邀请应幂等成功: %v", doc)
	}

	// 4) 被邀者 /waiting_users/recruit：temp_search_indexes 里是公会 search_index 行，
	//    join_approve_url 为原服形状（/guilds/join/?guild_id=）。
	doc = gameJSON(t, ts.URL, "/waiting_users/recruit", waiterID, wSK, ws0, ws1, "", "_method=GET")
	recruit := socialRows(t, socialData(t, doc), "temp_search_indexes")
	if len(recruit) != 1 {
		t.Fatalf("收到的邀请数=%d, 期望 1: %v", len(recruit), recruit)
	}
	inv := recruit[0].(map[string]any)
	if inv["guild_id"] != g.ID || inv["join_approve_url"] != "/guilds/join/?guild_id="+g.ID {
		t.Fatalf("邀请行=%v", inv)
	}
	if s, _ := inv["confirm_text"].(string); s == "" {
		t.Fatalf("邀请行缺 confirm_text: %v", inv)
	}

	// 5) 走原服 join_approve_url 接受 → 真实入会。
	doc = gameJSON(t, ts.URL, "/guilds/join/", waiterID, wSK, ws0, ws1,
		"guild_id="+url.QueryEscape(g.ID), "")
	if _, bad := doc["modal"]; bad {
		t.Fatalf("接受邀请失败: %v", doc)
	}
	view, err := st.MyGuild(waiterID)
	if err != nil || view.Guild == nil || view.Guild.ID != g.ID {
		t.Fatalf("接受邀请后未入会: %+v %v", view, err)
	}

	// 6) 入会后：邀请全部收敛（recruit 空），候选列表里不再有该账号，
	//    再邀请已是「已加入公会」的显式拒绝。
	doc = gameJSON(t, ts.URL, "/waiting_users/recruit", waiterID, wSK, ws0, ws1, "", "_method=GET")
	if left := socialRows(t, socialData(t, doc), "temp_search_indexes"); len(left) != 0 {
		t.Fatalf("入会后仍有邀请: %v", left)
	}
	doc = gameJSON(t, ts.URL, "/waiting_users", masterID, mSK, ms0, ms1, "", "_method=GET")
	for _, r := range socialRows(t, socialData(t, doc), "waiting_users") {
		if r.(map[string]any)["invite_url"] == "/waiting_users/invite/?user_id="+waiterID {
			t.Fatal("已入会账号不应还在候选列表")
		}
	}
	body := socialModal(t, gameJSON(t, ts.URL, "/waiting_users/invite/", masterID, mSK, ms0, ms1,
		"user_id="+url.QueryEscape(waiterID), ""))
	if !strings.Contains(body, "已加入公会") {
		t.Fatalf("已入会邀请弹窗正文=%q", body)
	}
	// 已入会者再登记等待邀请 → 显式拒绝（不是空成功）。
	socialModal(t, gameJSON(t, ts.URL, "/waiting_users/add.json", waiterID, wSK, ws0, ws1,
		"", "_method=POST&play_type_id=1"))
}

func TestGuildBattleCommentsWire(t *testing.T) {
	ts, st := socialTestServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "gc-t4-comments")
	other, osk, o0, o1 := establishSession(t, ts.URL, "gc-t4-comments-other")
	g, err := st.CreateGuild(uid, "留言会", "2", "1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// 房间 = 当前会战场次（guild.battle_id 同源）。测试服务时钟固定在
	// 2026-10-28 12:45（可战窗口内），GWState 会为本会建/取当前场次。
	now := time.Date(2026, 10, 28, 12, 45, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	gw, err := st.GWState(uid, now)
	if err != nil || gw == nil || gw.Session == nil {
		t.Fatalf("测试前置：当前场次缺失 %+v %v", gw, err)
	}
	room := gw.Session.SessionID

	// 读面：battle_comments 必须是 number（原服 unix 秒），动态子键按房间 id。
	doc := gameJSON(t, ts.URL, "/battles/comments", uid, sk, s0, s1, "", "_method=GET")
	data := socialData(t, doc)
	latest, ok := data["battle_comments"].(float64)
	if !ok {
		t.Fatalf("battle_comments 不是 number: %v", data["battle_comments"])
	}
	if latest != 0 {
		t.Fatalf("空房间 battle_comments=%v, 期望 0", latest)
	}
	if _, ok := data["battle_comments_"+room]; !ok {
		t.Fatalf("缺动态子键 battle_comments_%s: %v", room, data)
	}
	// 兼容别名：客户端房间键取 user.battle_id，本工程 user 文档缺该键时会读
	// "battle_comments_"（空后缀）。读面必须同时给出 user.battle_id 与别名。
	if _, ok := data["battle_comments_"]; !ok {
		t.Fatalf("缺兼容别名键 battle_comments_: %v", data)
	}
	if u, ok := data["user"].(map[string]any); !ok || u["battle_id"] != room {
		t.Fatalf("回包 user.battle_id 应为房间 id: %v", data["user"])
	}

	// ---- 回归（GC-10 实机缺陷）：参数只放加密 query（?e=），body 只有 _method，
	// 且客户端不下发 battle_id（user.battle_id 缺失）→ 必须成功写入并落库。
	doc = gameJSON(t, ts.URL, "/comments/create", uid, sk, s0, s1,
		"message_type_id=battle_comment&text="+url.QueryEscape("guildclose-gc08-test"),
		"")
	if doc["status"] != "success" {
		t.Fatalf("加密 query 形态写入失败: %v", doc)
	}
	var n int
	if err = st.DB().QueryRow(`SELECT count(*) FROM cn35_battle_comments
		WHERE battle_id=? AND body=?`, room, "guildclose-gc08-test").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("留言未落库（房间 %s 命中 %d 行）", room, n)
	}
	data = socialData(t, doc)
	rows, ok := data["battle_comments_"+room].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("写入回执应带刷新后的房间列表: %v", data["battle_comments_"+room])
	}
	row := rows[0].(map[string]any)
	if row["text"] != "guildclose-gc08-test" || row["user_id"] != uid {
		t.Fatalf("留言行=%v", row)
	}
	// 行键集对齐原服 Message 行：客户端 BattleMessages 行渲染依赖 type_id 与
	// unit_id（气泡形态/头像贴图），其余键由 Message 构造器读取。
	for _, k := range []string{"type_id", "unit_id", "user_name", "user_short_name",
		"help_id", "raid_id", "addition", "small_text", "stamp", "private",
		"team_id", "entry_url", "created_at"} {
		if _, ok := row[k]; !ok {
			t.Fatalf("留言行缺键 %s: %v", k, row)
		}
	}
	if row["type_id"] != "battle_comment" {
		t.Fatalf("type_id=%v", row["type_id"])
	}
	// 成功回执形状对齐原服录包（status + modal 完成/已发送 + data）。
	if m, ok := doc["modal"].(map[string]any); !ok || m["body"] != "已发送" {
		t.Fatalf("成功回执 modal 不符原服: %v", doc["modal"])
	}

	// 读面计数键随真实留言变化（不是恒定值）。
	doc = gameJSON(t, ts.URL, "/battles/comments", uid, sk, s0, s1, "", "_method=GET")
	data = socialData(t, doc)
	if latest = data["battle_comments"].(float64); latest <= 0 {
		t.Fatalf("写入后 battle_comments=%v, 期望 >0", latest)
	}

	// 显式带上正确 battle_id 也照常写入（客户端拿到 user.battle_id 之后的形态）。
	doc = gameJSON(t, ts.URL, "/comments/create", uid, sk, s0, s1,
		"message_type_id=battle_comment&battle_id="+url.QueryEscape(room)+
			"&text="+url.QueryEscape("第二条"), "")
	if doc["status"] != "success" {
		t.Fatalf("显式房间写入失败: %v", doc)
	}

	// 非本会账号不能在该房间发言（显式拒绝；他有自己的场次也不影响本次判定）。
	body := socialModal(t, gameJSON(t, ts.URL, "/comments/create", other, osk, o0, o1,
		"message_type_id=battle_comment&battle_id="+url.QueryEscape(room)+
			"&text="+url.QueryEscape("捣乱"), ""))
	if body == "" {
		t.Fatalf("越权留言应带弹窗正文")
	}
	// 未开放的留言类型：显式「功能未开放」，不得空成功。
	body = socialModal(t, gameJSON(t, ts.URL, "/comments/create", uid, sk, s0, s1,
		"message_type_id=user_comment&text="+url.QueryEscape("用户留言"), ""))
	if !strings.Contains(body, "仅开放会战留言") {
		t.Fatalf("未开放类型弹窗正文=%q", body)
	}
	// 内容长度上限不放宽：111 字必须仍被拒（BattleMessages.cs:59）。
	long := strings.Repeat("あ", cn35game.BattleCommentMaxRunes+1)
	body = socialModal(t, gameJSON(t, ts.URL, "/comments/create", uid, sk, s0, s1,
		"message_type_id=battle_comment&text="+url.QueryEscape(long), ""))
	if !strings.Contains(body, "1-110") {
		t.Fatalf("超长留言应报字数错误，实际=%q", body)
	}
	_ = g
}

// Top 轮询先给房间键；无需依赖打开聊天室时额外补缓存。
func TestGuildMessagesProvidesRoom(t *testing.T) {
	ts, st := socialTestServer(t)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "gc-room-poll")
	if _, err := st.CreateGuild(uid, "房间会", "1", "1", "", ""); err != nil {
		t.Fatal(err)
	}
	d := socialData(t, gameJSON(t, ts.URL, "/users/messages", uid, sk, s0, s1, "", "_method=GET"))
	user, ok := d["user"].(map[string]any)
	if !ok || user["battle_id"] == nil || user["battle_id"] == "" {
		t.Fatalf("缺房间: %v", d)
	}
}
