// guild_wire.go GUILD350-02..09：公会/公会战 wire 面。
//
// 字段真源：docs/协议对接/cn350/公会战-客户端契约取证.md。
// 数值统一读 cn35game.GuildRules；无国服证据的键一律带〔推断〕注释。
package cn35game

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// guildRoleNames 公会职位（client Role 枚举：1 User/2 SubMaster/3 Master）。
const (
	GuildRoleMember    = 1
	GuildRoleSubMaster = 2
	GuildRoleMaster    = 3
)

// guildModal 校验失败响应（复用剧情弹窗协议）。
func guildModal(title, body string) map[string]any { return questModal(title, body) }

// ---- 文档组装 ----

// guildMemberDoc 成员行文档（GuildMemberListRow.cs 消费键）。
func guildMemberDoc(m GuildMember) map[string]any {
	return map[string]any{
		"id":                     m.UserID,
		"name":                   m.Name,
		"guild_id":               "", // 行内不携带本公会 id（客户端用 user.guild_id）
		"guild_role":             map[string]any{"id": m.Role},
		"level":                  m.Level,
		"name_and_role":          fmt.Sprintf("%s Lv.%d", m.Name, m.Level),
		"user_status":            fmt.Sprintf("Lv.%d\n0\n-", m.Level),
		"square_icon_asset_name": fmt.Sprintf("unit/unit_%s", orEmpty(m.LeaderUnitID, "0")),
		"user_detail_message":    "",
		"updated_at_i":           0,
		// GuildMembers 行 last_login 位（niceTime）：客户端
		// ListRowBase.cs:120-124 对该键做 int.Parse（Unix 秒），不能下发
		// RFC3339 字符串，否则整行渲染抛 FormatException。暂无登录表，
		// 以入会时间代。
		"logged_at": joinedAtUnix(m.JoinedAt),
		// 单机 NPC 成员标记（GUILDLIFE350-06）：NPC 身份不在 cn35_accounts，
		// 不可被踢/升职/转让；客户端未知键忽略，仅取证与排障用。
		"is_npc": m.IsNPC,
	}
}

// joinedAtUnix 把成员入会时间（RFC3339Nano）转成 Unix 秒字符串；
// 客户端 niceTime 位按整数解析（ListRowBase.cs:120-124）。
func joinedAtUnix(joinedAt string) int64 {
	if joinedAt == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, joinedAt); err == nil {
		return t.Unix()
	}
	if t, err := time.Parse("2006-01-02 15:04:05", joinedAt); err == nil {
		return t.Unix()
	}
	return 0
}

// guildMembersDoc 成员行列表（与 guild.users_map 同集合同序，含 NPC 成员）。
func guildMembersDoc(view *GuildView) []any {
	out := []any{}
	for _, m := range view.Members {
		out = append(out, guildMemberDoc(m))
	}
	return out
}

func orEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// guildEquipmentTypeNames 六类设施元数据来源 GuildEquipmentTypes（guild.go）；
// 文档组装统一走 T3 的 GuildEquipmentsDoc（guild_storage_wire.go），本文件
// 不再自行按等级公式推算 until/value（旧线性 10 级模型已废弃）。

// guildRankIDNum rank_id 转原服数字档位（guild_rank 枚举 id 0..4）。
// cn35_guilds.rank_id 为 TEXT；无法解析时按 1（排名A，建会默认档）。
func guildRankIDNum(raw string) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v < 0 || v > 4 {
		return 1
	}
	return v
}

// guildRankEnumName 职位名（原服 guild_role = {id, name}）。
func guildRoleName(role int) string {
	switch role {
	case GuildRoleMaster:
		return "会长"
	case GuildRoleSubMaster:
		return "副会长"
	default:
		return "成员"
	}
}

// guildDoc data.guild 文档（原服 decrypted/1/guilds.json::data.guild）。
//
// users_map 原服是**数组**（20 个元素）；客户端 GuildDescription.cs:68 与
// GuildTop.cs:437 只取其 Count（Document.Count：isList→docList.Count，
// object→json.keys.Count，两种形状都能人数正确）。契约 want:"array"，
// 故下发成员行数组。
func guildDoc(view *GuildView) map[string]any {
	return guildDocWith(view, guildDocExtra{})
}

// guildDocExtra 读面上下文（原服 guild 文档内的会话/名次键）。
type guildDocExtra struct {
	// BattleID 当前会战场次 id（原服 guild.battle_id 为 string；无场次为 ""）。
	BattleID string
	// RankingRank 当月会战名次（原服 guild.guild_ranking_rank 为 number；
	// 未上榜为 0）。
	RankingRank int
}

func guildDocWith(view *GuildView, extra guildDocExtra) map[string]any {
	g := view.Guild
	usersMap := []any{}
	for _, m := range view.Members {
		usersMap = append(usersMap, guildMemberDoc(m))
	}
	return map[string]any{
		"id":                  g.ID,
		"name":                g.Name,
		"master_name":         g.MasterName,
		"users_map":           usersMap,
		"rating":              g.Rating,
		"rank_id":             guildRankIDNum(g.RankID),
		"guild_ranking_point": g.Point,
		"guild_ranking_rank":  extra.RankingRank,
		"equipments":          GuildEquipmentsDoc(view),
		"battle_id":           extra.BattleID,
		// 蓄水库按钮开关：原服 4 份快照恒 1（T3 核实）；未入会时 data.guild
		// 为空对象，按钮不渲染，故无需 0 分支。
		"bp_storage_button": 1,
	}
}

// guildDocExtraFor 组装读面上下文：当前会战场次 id（guild.battle_id）与
// 当月榜名次（guild.guild_ranking_rank）。
func guildDocExtraFor(dep Deps, view *GuildView, gw *GWView, now time.Time) (guildDocExtra, error) {
	extra := guildDocExtra{}
	if gw != nil && gw.Session != nil {
		extra.BattleID = gw.Session.SessionID
	}
	if view != nil && view.Guild != nil {
		rank, _, err := dep.Guild.GWRankingPosition(view.Guild.ID, guildCurrentYYYYMM(now))
		if err != nil {
			return extra, err
		}
		extra.RankingRank = rank
	}
	return extra, nil
}

// guildPlayTypeNames 公会方针（原服 play_type_select_option 1..4；0/空=未指定）。
var guildPlayTypeNames = map[string]string{
	"": "未指定", "0": "未指定", "1": "与世无争", "2": "聚沙成塔",
	"3": "精益求精", "4": "锲而不舍",
}

// guildApprovalTypeNames 批准设定（原服 approval_type_select_option）。
var guildApprovalTypeNames = map[string]string{
	"": "未指定", "0": "未指定", "1": "自动加入", "2": "手动加入",
}

// guildOptionID 选项数值 id：空串按 0（未指定）。
func guildOptionID(raw string) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// guildShiftsDoc shifts 原服为 7 元素 bool 数组（周一..周日）。
func guildShiftsDoc(g *Guild) []any {
	out := make([]any, 7)
	for i := 0; i < 7; i++ {
		out[i] = g.Shifts[i]
	}
	return out
}

// guildPlayTypeDoc 方针对象 {id:number,name:string}。
func guildPlayTypeDoc(g *Guild) map[string]any {
	id := guildOptionID(g.PlayTypeID)
	name := guildPlayTypeNames[strconv.Itoa(id)]
	return map[string]any{"id": id, "name": name}
}

// guildApprovalTypeDoc 批准设定对象 {id:number,name:string}。
func guildApprovalTypeDoc(g *Guild) map[string]any {
	id := guildOptionID(g.ApprovalTypeID)
	name := guildApprovalTypeNames[strconv.Itoa(id)]
	return map[string]any{"id": id, "name": name}
}

// guildAutoApproval 免审直进：原服 approval_type=1（自动加入）→ true。
func guildAutoApproval(g *Guild) bool {
	return guildOptionID(g.ApprovalTypeID) == 1
}

// guildMasterUserID 会长 user_id（用户详情串需要；找不到返回空串）。
func guildMasterUserID(view *GuildView) string {
	for _, m := range view.Members {
		if m.Role == GuildRoleMaster {
			return m.UserID
		}
	}
	return ""
}

// guildSpaceIcon 会长领队方图标名（原服 unit/unit_square_<leader_unit_id>）。
func guildSpaceIcon(view *GuildView) string {
	for _, m := range view.Members {
		if m.Role != GuildRoleMaster {
			continue
		}
		id := strings.TrimSpace(m.LeaderUnitID)
		if _, err := strconv.Atoi(id); err != nil {
			return ""
		}
		return "unit/unit_square_" + id
	}
	return ""
}

// guildBattleSettingText 参战曜日文案（battle_setting_text 为空时按 shifts 生成）。
//
// 原服样本："参加　: 周一、周二、…、周日\n不参加: "（参加后用全角空格+冒号）。
func guildBattleSettingText(g *Guild) string {
	if g.BattleSettingText != "" {
		return g.BattleSettingText
	}
	names := [7]string{"周一", "周二", "周三", "周四", "周五", "周六", "周日"}
	in, out := []string{}, []string{}
	for i := 0; i < 7; i++ {
		if g.Shifts[i] {
			in = append(in, names[i])
		} else {
			out = append(out, names[i])
		}
	}
	return "参加　: " + strings.Join(in, "、") + "\n不参加: " + strings.Join(out, "、")
}

// guildUserDetailMessage 用户详情页跳转串（原服 name=UserPage&base=…&method=Stack）。
func guildUserDetailMessage(userID string) string {
	if userID == "" {
		return ""
	}
	return "name=UserPage&base=Common/Pages/UserPage&additional_require_data_path=" +
		url.QueryEscape("/users/show?id="+userID) + "&method=Stack"
}

// guildDescriptionConf 搜索行「详细」按钮的 additionalOption 配置
// （原服 data_key=guild_temp，按 /guilds/show 拉完整公文档）。
func guildDescriptionConf(guildID string) map[string]any {
	return map[string]any{
		"data_key": "guild_temp",
		"requireData": map[string]any{
			"guild_temp": map[string]any{
				"force_update": true,
				"path":         "/guilds/show?id=" + guildID,
			},
		},
	}
}

// searchIndexDoc /guilds/search_index 与 show 的 search_index_temp。
//
// 完整字段集来自原服 decrypted/1/guilds_search_index.json::data.search_index
// （客户端 GuildSettings.cs:105-215 读 play_type/approval_type 子对象的 name、
// battle_setting_text、recruit_text、bools("shifts")）。
func searchIndexDoc(view *GuildView) map[string]any {
	g := view.Guild
	if g == nil {
		return map[string]any{}
	}
	pt, at := guildPlayTypeDoc(g), guildApprovalTypeDoc(g)
	return map[string]any{
		"guild_id":     g.ID,
		"name":         g.Name,
		"play_type_id": pt["id"],
		"rating":       g.Rating,
		"recruit_text": g.RecruitText,
		// recruiting：原服样本 users_count=20（满员）时仍为 true，说明它不等于
		// 「有空位」，而与「公会仍接收加入请求」同义〔单样本推断〕。本服任何
		// 公会都接受申请（满员在 JoinGuild 内拒绝），故恒 true。
		"recruiting":             true,
		"users_count":            g.MemberCount,
		"play_type":              pt,
		"play_type_name":         pt["name"],
		"approval_type":          at,
		"approval_type_name":     at["name"],
		"master_user_name":       g.MasterName,
		"auto_approval":          guildAutoApproval(g),
		"join_approve_url":       "/guilds/join/?guild_id=" + g.ID,
		"confirm_text":           "要加入" + g.Name + "吗？",
		"square_icon_asset_name": guildSpaceIcon(view),
		"shifts":                 guildShiftsDoc(g),
		"battle_setting_text":    guildBattleSettingText(g),
		"user_detail_message":    guildUserDetailMessage(guildMasterUserID(view)),
		"description_conf":       guildDescriptionConf(g.ID),
	}
}

// guildTempDoc 出边信息页用公会简档（guild_temp / 搜索结果行）。
//
// GUILDLIFE350-04：人数与设施来自同一读面聚合（不能只填 member_count），
// 客户端搜索行读 users_count；公会详情弹窗 GuildDescription 的
// 成员数/会长/设施等级读 `guild.doc("users_map").Count` 与
// `guild.Traverse("equipments",<type>,"current","level")`（GuildDescription.cs:58-70），
// 因此简档必须是 guild 文档的完整投影。
func guildTempDoc(view *GuildView) map[string]any {
	g := view.Guild
	pt, at := guildPlayTypeDoc(g), guildApprovalTypeDoc(g)
	usersMap := []any{}
	for _, m := range view.Members {
		usersMap = append(usersMap, guildMemberDoc(m))
	}
	equip := GuildEquipmentsDoc(view)
	confirm := "要加入" + g.Name + "吗？"
	// description_conf 是**行内** additionalOption 配置（原服 data_key=guild_temp +
	// requireData /guilds/show），不是完整公文档；详情页据此另拉一份 guild_temp。
	detail := guildDescriptionConf(g.ID)
	return map[string]any{
		"id": g.ID, "name": g.Name, "master_name": g.MasterName,
		"rating": g.Rating, "rank_id": guildRankIDNum(g.RankID),
		"guild_ranking_point": g.Point, "guild_ranking_rank": 0,
		"users_map":    usersMap,
		"member_count": g.MemberCount,
		"equipments":   equip,
		// 搜索行键（GuildSearchIndexListPartial / GuildDescription.cs:43 按
		// guild_id 在 temp_search_indexes 里找回 search_index）。
		"guild_id":               g.ID,
		"master_user_name":       g.MasterName,
		"users_count":            g.MemberCount,
		"play_type_name":         pt["name"],
		"approval_type_name":     at["name"],
		"auto_approval":          guildAutoApproval(g),
		"confirm_text":           confirm,
		"join_approve_url":       "/guilds/join/?guild_id=" + g.ID,
		"square_icon_asset_name": guildSpaceIcon(view),
		"user_detail_message":    guildUserDetailMessage(guildMasterUserID(view)),
		"description_conf":       detail,
		"recruit_text":           g.RecruitText,
		"battle_setting_text":    guildBattleSettingText(g),
		"shifts":                 guildShiftsDoc(g),
		"is_npc":                 g.IsNPC,
	}
}

// guildBattleConditionType 原服 data.battle_condition.type（"Battle"）。
//
// 证据：artifacts/mlk-capture-201711/decrypted/{1,default}/battles_help*、
// battles_pray* 共 1605 份样本 + guilds.json 1 份，type 恒为 "Battle"，
// 无变体。客户端 GuildTop.cs:147 `isRegularBattle` 按 != "BattleCooperate"
// 判定——协力战未恢复，恒下发常规战。
const guildBattleConditionType = "Battle"

// guildBattleConditionI 原服 data.battle_condition.condition.i（字符串）。
//
// 证据：同上 1605 份样本 i 恒为 "2"，无变体；语义未取证（阶段序号/场次
// 序号都是候选），按原样透传，**不当业务规则**，也不参与结算。
const guildBattleConditionI = "2"

// battleConditionDoc data.battle_condition（原服 guilds.json / battles_help.json）。
//
// 形状：{type, condition{i,power,combo,seed_powerup_at,seed_powerup_rate},
// guilds[], weapon_rate{}}。T2 的 /battles/help、/battles/pray 复用本函数。
func battleConditionDoc(v *GWView) map[string]any {
	if v == nil || v.Session == nil {
		return map[string]any{}
	}
	guilds := []any{}
	for _, p := range v.Participants {
		guilds = append(guilds, map[string]any{
			"id": p.GuildID, "name": p.Name, "master_name": "",
			"rank_id": guildRankIDNum(p.RankID), "ranking": p.RankInGroup,
			"point": p.GP, "color": p.Color, "joined": p.Joined,
		})
	}
	return map[string]any{
		"type": guildBattleConditionType,
		"condition": map[string]any{
			"i":     guildBattleConditionI,
			"power": v.Power,
			"combo": v.Combo,
			// seed_powerup_at 原服为 Unix 秒字符串；重建无种子投放时刻，空串
			// 是原服在「无种子强化」时的合法取值（样本中大量出现 ""）。
			"seed_powerup_at":   "",
			"seed_powerup_rate": v.SeedPowerup,
		},
		"guilds":      guilds,
		"weapon_rate": battleWeaponRateDoc(),
	}
}

// fieldsDocs fields 文档列表（每行挂当日状态：actable/ranking_enable）。
// pray_num/help_num = 本场次全公会祈祷/应援累计次数（当前时窗行下发，
// 服务端台账 cn35_gw_participants.pray_count/help_count）。
// bg_name 仅当前可战时段下发：客户端 CurrentFieldDocument 非时窗回退
// fields[0]，isBattleOpened=bg_name非空+battle_condition非空——全行下发
// 会让休战幕布与战场地图同显（实机已现重叠）。
func fieldsDocs(now time.Time, actable bool, prayNum, helpNum int) []any {
	// 原服 fields 为**静态**时段表：actable/bg_name/pray_num/help_num/
	// ranking_enable 都是配置值，不随当前时刻或当场次数变化
	// （artifacts/mlk-capture-201711/decrypted/1/guilds.json::data.fields）。
	_ = now
	_ = actable
	_ = prayNum
	_ = helpNum
	out := []any{}
	for _, f := range GuildRules.Fields {
		out = append(out, map[string]any{
			"id": f.ID, "name": f.Name,
			"begin_hm": f.BeginHM, "end_hm": f.EndHM, "bg_name": f.BgName,
			"actable":        f.Actable,
			"ranking_enable": f.RankingEnable,
			"pray_bp":        f.PrayBP, "pray_gp": f.PrayGP,
			"help_bp": f.HelpBP, "help_gp": f.HelpGP,
			"attack_bp1": f.AttackBP1, "attack_bp2": f.AttackBP2,
			"attack_bp3": f.AttackBP3,
			"pray_num":   f.PrayNum, "help_num": f.HelpNum,
			"attack_bp": f.AttackBP1,
		})
	}
	return out
}

// guildWireLoc 会战判定时区（与 store guildLoc 同值；wire 独立持有避免跨包）。
func guildWireLoc() *time.Location { return time.FixedZone("Asia/Shanghai", 8*3600) }

// battleWeaponRateDoc 武器相性表（原服配置常量）。
//
// 证据：artifacts/mlk-capture-201711/decrypted/{1,2,3,default}/battles_help*、
// battles_pray* 共 1605 份样本 weapon_rate 完全一致：
// slash/rush/magic/gun/heal=1、hit/arrow=1.3；同一对象也出现在
// guilds.json::data.battle_weapon_rate。属配置常量，可复现规则；
// 对内伤害公式仍由战斗域自持（本函数只下发展示/协议值）。
func battleWeaponRateDoc() map[string]any {
	return map[string]any{
		"slash_rate": 1.0, "rush_rate": 1.0, "hit_rate": 1.3,
		"arrow_rate": 1.3, "magic_rate": 1.0, "gun_rate": 1.0,
		"heal_rate": 1.0,
	}
}

// guildRankDoc 公会 rank 枚举（原服 guilds.json::data.guild_rank）。
//
// 原服 5 档：id 0..4 = 排名B/排名A/排名S/等级S+/排名SS（short B/A/S/S+/SS）。
// 客户端 cache.Enum("guild_rank", guild.s("rank_id")) 按 **字符串化 id**
// 匹配（DataCache.cs:440-455：doc.s("id") 对 number 会 ToString()），因此
// 数字 id 与 guild.rank_id 数字档位一一对应。
func guildRankDoc() []any {
	rows := []struct {
		id          int
		name, short string
	}{
		{0, "排名B", "B"}, {1, "排名A", "A"}, {2, "排名S", "S"},
		{3, "等级S+", "S+"}, {4, "排名SS", "SS"},
	}
	out := []any{}
	for _, r := range rows {
		out = append(out, map[string]any{"id": r.id, "name": r.name, "short": r.short})
	}
	return out
}

// guildOptionDoc 选项表对象 {title, default_index, options:[{title,value}]}。
//
// 客户端 SelectModal 读 title 作弹窗标题、遍历 options 取 title/value
// （GuildSettings.cs:135-190）。value 为 number（回调拼 /guilds/update?…=<num>）。
func guildOptionDoc(title string, defaultIndex int, options [][2]any) map[string]any {
	rows := make([]any, 0, len(options))
	for _, o := range options {
		rows = append(rows, map[string]any{"title": o[0], "value": o[1]})
	}
	return map[string]any{"title": title, "default_index": defaultIndex, "options": rows}
}

// playTypeSelectOptionDoc 变更公会方针（原服 play_type_select_option）。
func playTypeSelectOptionDoc() map[string]any {
	return guildOptionDoc("变更公会方针", 0, [][2]any{
		{"未指定", 0}, {"与世无争", 1}, {"聚沙成塔", 2}, {"精益求精", 3}, {"锲而不舍", 4},
	})
}

// approvalTypeSelectOptionDoc 变更批准设定（原服 approval_type_select_option）。
func approvalTypeSelectOptionDoc() map[string]any {
	return guildOptionDoc("变更批准设定", 0, [][2]any{
		{"未指定", 0}, {"自动加入", 1}, {"手动加入", 2},
	})
}

// equipmentSelectOptionDoc 设施投资额度选项（原服 equipment_select_option）。
// title 是**带千分位**的展示串，value 是投资额数字。
func equipmentSelectOptionDoc() map[string]any {
	return guildOptionDoc("请选择投资额度", 0, [][2]any{
		{"100", 100}, {"500", 500}, {"1,000", 1000}, {"5,000", 5000},
		{"10,000", 10000}, {"50,000", 50000}, {"100,000", 100000},
	})
}

// guildMemberOptionDoc GuildMembers 页入口配置（原服 guild_member_option）。
//
// 客户端 GuildTop.MemberButtonClick（GuildTop.cs:582-590）把该文档交给
// ShowPage，ListParts 用 data_key=guild_users 取行，requireData 触发
// /guilds/users/?id=<gid>（T4 的成员读面，同时下发 guild_users 与
// guild_users_<gid> 两个键）。
func guildMemberOptionDoc(guildID string) map[string]any {
	return map[string]any{
		"parts": map[string]any{
			"ListParts": map[string]any{"data_key": "guild_users"},
		},
		"requireData": map[string]any{
			"guild_users": map[string]any{
				"path":         "/guilds/users/?id=" + guildID,
				"force_update": true,
			},
		},
	}
}

// guildEquipmentTypesDoc guild_equipment_types（原服仅 {id}，顺序六类）。
func guildEquipmentTypesDoc() []any {
	out := make([]any, 0, len(GuildEquipmentTypeIDs))
	for _, tid := range GuildEquipmentTypeIDs {
		out = append(out, map[string]any{"id": tid})
	}
	return out
}

// guildRoleDoc 当前查询者职位（原服 guild_role = {id,name}）。
func guildRoleDoc(role int) map[string]any {
	return map[string]any{"id": role, "name": guildRoleName(role)}
}

// guildRefreshData 写面回包公共文档：成员页/主页在写操作成功后只从 cache
// 重载（GuildMemberListRow.cs:64-100 → list.LoadListRows()），不回包就等于
// 列表不刷新；被踢/退会者也需要 user 快照立即变化。
func guildRefreshData(dep Deps, userID string) (map[string]any, error) {
	snap, err := dep.Accounts.Snapshot(userID)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"user": snap.User}
	view, verr := dep.Guild.MyGuild(userID)
	if verr != nil {
		return nil, verr
	}
	if view.Guild != nil {
		data["guild"] = guildDoc(view)
		data["guild_role"] = guildRoleDoc(view.MyRole)
		data["guild_members"] = guildMembersDoc(view)
		data["search_index"] = searchIndexDoc(view)
		data["my_guild_memo"] = view.Guild.Memo
	} else {
		data["guild"] = map[string]any{}
		data["guild_role"] = guildRoleDoc(0)
		data["guild_members"] = []any{}
		data["my_guild_memo"] = ""
	}
	return data, nil
}

// ---- 读面 handlers ----

// guildsIndexHandler GET /guilds/：主读面（data.guild + battle_condition +
// 全套辅助键，见取证 §7）。
func guildsIndexHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := guildWireNow(dep)
		gw, err := dep.Guild.GWState(req.UserID, now)
		if err != nil {
			return nil, err
		}
		view, err := dep.Guild.MyGuild(req.UserID)
		if err != nil {
			return nil, err
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		data := map[string]any{
			"user": snap.User,
		}
		extra, xerr := guildDocExtraFor(dep, view, gw, now)
		if xerr != nil {
			return nil, xerr
		}
		if view.Guild != nil {
			data["guild"] = guildDocWith(view, extra)
			data["guild_role"] = guildRoleDoc(view.MyRole)
			data["search_index"] = searchIndexDoc(view)
			data["my_guild_memo"] = view.Guild.Memo
		} else {
			data["guild"] = map[string]any{}
			data["guild_role"] = guildRoleDoc(0)
			data["my_guild_memo"] = ""
		}
		// GuildMembers 页 ListParts.data_key=guild_members（app_conf 10829 行）：
		// 成员列表行经独立顶层键下发，与 guild.users_map 同源同序。
		data["guild_members"] = guildMembersDoc(view)
		data["battle_condition"] = battleConditionDoc(gw)
		prayNum, helpNum := 0, 0
		if gw != nil {
			prayNum, helpNum = gw.PrayCount, gw.HelpCount
		}
		data["fields"] = fieldsDocs(now, gw != nil && gw.Actable, prayNum, helpNum)
		data["battle_weapon_rate"] = battleWeaponRateDoc()
		// guild_rank：详情 RankLabel / 主页名次读 cache.Enum("guild_rank",rank_id,
		// "short"/"name")，缺该键时全部回空串（GUILDLIFE350-04 显示缺口）。
		data["guild_rank"] = guildRankDoc()
		// waiting_users：GuildWaitingListRecruit 读顶层列表；T4 已接管
		// /waiting_users 真实读面，本页只保证键存在（空数组=无待处理）。
		data["waiting_users"] = []any{}
		data["my_shared_monsters"] = map[string]any{} // 协力战排除（取证 §9）
		guildID := ""
		if view.Guild != nil {
			guildID = view.Guild.ID
		}
		data["guild_member_option"] = guildMemberOptionDoc(guildID)
		data["equipment_select_option"] = equipmentSelectOptionDoc()
		data["play_type_select_option"] = playTypeSelectOptionDoc()
		data["approval_type_select_option"] = approvalTypeSelectOptionDoc()
		data["guild_equipment_types"] = guildEquipmentTypesDoc()
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_index",
			"guild": view.Guild != nil,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildDeckHandler GET /guilds/deck：防卫卡组读面。
func guildDeckHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		slots, err := dep.Guild.DefenseOf(req.UserID)
		if err != nil {
			return nil, err
		}
		deck, err := guildDefenseDeckDoc(dep, slots)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_deck",
			"slots": len(slots),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"guild_deck":      deck,
				"guild_deck_data": guildDeckDataDoc(),
				// my_shared_monsters：协力战共用魔宠槽（原服 /guilds/deck 有该键，
				// 协力战未恢复，下空对象而非缺键——契约 want:"object"）。
				"my_shared_monsters": map[string]any{},
			},
		})
	})
}

// guildDeckDataDoc guild_deck_data 旁路文档：powerup_rate[5] 槽位倍率与
// last_edit_name 为客户端 GuildDeckListParts 必读键，缺失抛
// ArgumentOutOfRangeException 致编队页空白。倍率与槽位加成待数值下发，
// 占位全 1.0。
func guildDeckDataDoc() map[string]any {
	return map[string]any{
		"powerup_rate":   []any{1.0, 1.0, 1.0, 1.0, 1.0},
		"last_edit_name": "",
	}
}

// guildDefenseSlotDoc 防卫槽位行：展示字段同 guild_monsters 魔宠行（名称/
// 属性/数值/贴图），否则已设防槽显示属性 0、无图标。
func guildDefenseSlotDoc(dep Deps, s GWDefenseSlot) (map[string]any, error) {
	m, err := dep.Catalog.Monster(s.MonsterID)
	if err != nil {
		return nil, fmt.Errorf("防卫魔宠模板缺失 %d: %w", s.MonsterID, err)
	}
	doc, err := buildMonsterDoc(MonsterRecord{
		UUID: s.MonsterUUID, MonsterID: s.MonsterID, Level: s.Level,
	}, m)
	if err != nil {
		return nil, err
	}
	if s.Batch != 0 {
		doc["batch"] = s.Batch
	} else {
		doc["batch"] = MonsterBatch(s.MonsterID)
	}
	return doc, nil
}

// guildDefenseDeckDoc 防卫卡组文档（5 槽 map）。
func guildDefenseDeckDoc(dep Deps, slots []GWDefenseSlot) (map[string]any, error) {
	deck := map[string]any{}
	for _, s := range slots {
		doc, err := guildDefenseSlotDoc(dep, s)
		if err != nil {
			return nil, err
		}
		deck[strconv.Itoa(s.Slot)] = doc
	}
	return deck, nil
}

// guildMonstersHandler GET /guilds/monsters：可选防卫魔宠列表（账号魔宠
// 全量，客户端按 guild_deck 已选 uuid 及非负 batch 排除；普通魔宠 batch=-1）。
func guildMonstersHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		records, err := dep.Inventory.Monsters(req.UserID)
		if err != nil {
			return nil, err
		}
		list := []any{}
		for _, r := range records {
			m, merr := dep.Catalog.Monster(r.MonsterID)
			if merr != nil {
				return nil, merr
			}
			doc, derr := buildMonsterDoc(r, m)
			if derr != nil {
				return nil, derr
			}
			list = append(list, doc)
		}
		// /guilds/monsters 原服同时下发已设防卡组（客户端编队页据此排除已选、
		// 展示槽位）：guild_deck / guild_deck_data 与 /guilds/deck 同源同形。
		slots, serr := dep.Guild.DefenseOf(req.UserID)
		if serr != nil {
			return nil, serr
		}
		deck, derr := guildDefenseDeckDoc(dep, slots)
		if derr != nil {
			return nil, derr
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_monsters",
			"count": len(list), "slots": len(slots),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"guild_monsters":  list,
				"guild_deck":      deck,
				"guild_deck_data": guildDeckDataDoc(),
			},
		})
	})
}

// guildSearchIndexHandler GET /guilds/search_index。
func guildSearchIndexHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		view, err := dep.Guild.MyGuild(req.UserID)
		if err != nil {
			return nil, err
		}
		// 原服 /guilds/search_index 的 data 键全集（decrypted/1/guilds_search_index.json）：
		// guild / search_index / my_guild_memo / equipment_select_option /
		// guild_member_option / play_type_select_option / approval_type_select_option。
		data := map[string]any{
			"equipment_select_option":     equipmentSelectOptionDoc(),
			"play_type_select_option":     playTypeSelectOptionDoc(),
			"approval_type_select_option": approvalTypeSelectOptionDoc(),
			"guild_member_option":         guildMemberOptionDoc(""),
			"my_guild_memo":               "",
			"guild":                       map[string]any{},
			"search_index":                map[string]any{},
		}
		if view.Guild != nil {
			extra, xerr := guildDocExtraFor(dep, view, nil, guildWireNow(dep))
			if xerr != nil {
				return nil, xerr
			}
			data["guild"] = guildDocWith(view, extra)
			data["search_index"] = searchIndexDoc(view)
			data["my_guild_memo"] = view.Guild.Memo
			data["guild_member_option"] = guildMemberOptionDoc(view.Guild.ID)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_search_index",
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildSubscriptionsHandler GET /guilds/subscriptions：待审批申请行
// （行文档带 join_approve_url〔推断 approve_subscription〕+ reject_url）。
func guildSubscriptionsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		view, err := dep.Guild.MyGuild(req.UserID)
		if err != nil {
			return nil, err
		}
		rows := []any{}
		for _, sub := range view.Subscriptions {
			rows = append(rows, map[string]any{
				"id":               sub.UserID,
				"sub_id":           sub.SubID,
				"name":             sub.UserName,
				"level":            sub.Level,
				"guild_id":         sub.GuildID,
				"guild_role":       map[string]any{"id": GuildRoleMember},
				"join_approve_url": "/guilds/approve_subscription?sub_id=" + sub.SubID,
				"reject_url":       "/guilds/reject_subscription?sub_id=" + sub.SubID,
			})
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_subscriptions",
			"pending": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_subscriptions": rows},
		})
	})
}

// /waiting_users、/waiting_users/recruit 已移交 T4（guild_social_wire.go 的
// registerGuildMemberRoutes），此处不再保留旧空态实现。

// ---- 会战月度排名（GC-06）----
//
// 原服回包形状（artifacts/mlk-capture-201711/decrypted/1/）：
//   /guild_rankings              → data{guild_ranking_yyyymms:[{yyyymm}],guild_rankings:[行]}
//   /guild_rankings/my_guild     → data{guild_rankings_self<ym>:[行]}（无 yyyymm 时 guild_rankings_self）
//   /guild_rankings/item_rewards → data{guild_rankings_item_rewards:[8 档]}
//   /guild_rankings/skill_rewards→ data{guild_rankings_skill_rewards_level:[250 行],
//                                    guild_rankings_skill_rewards_type:{…}}
//
// 客户端（GuildRankingListParts.cs:74-110）把 `cache_key` 查询参数当作回包
// 数据键名：cache_key=guild_rankings / guild_rankings_101 / guild_rankings_self201710。
// 服务端必须把行数组挂在**请求指定**的键上，否则 LoadRows 取不到行。

// guildRankingsPageLimit 原服客户端一次 100 行（GuildRankingListParts limit=100）。
const guildRankingsPageLimit = 100

// guildCacheKey 取并校验客户端要求的回包数据键（缺省用 fallback）。
// cache_key 会直接成为响应 JSON 键，只接受 [A-Za-z0-9_]，最长 64。
func guildCacheKey(raw, fallback string) string {
	if raw == "" || len(raw) > 64 {
		return fallback
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		return fallback
	}
	return raw
}

// guildParseYYYYMM 解析 yyyymm（"201711"）；非法/缺省返回 0。
func guildParseYYYYMM(raw string) int {
	v, err := strconv.Atoi(raw)
	if err != nil || v < 100001 || v > 999912 {
		return 0
	}
	if m := v % 100; m < 1 || m > 12 {
		return 0
	}
	return v
}

// guildRankingMonthsDoc guild_ranking_yyyymms：[{yyyymm:number},…]。
func guildRankingMonthsDoc(months []int) []any {
	out := []any{}
	for _, ym := range months {
		out = append(out, map[string]any{"yyyymm": ym})
	}
	return out
}

// guildRankingRowsDoc 榜单行：point 为**字符串**（原服契约 want:"string"）。
func guildRankingRowsDoc(rows []GWRankingRow) []any {
	out := []any{}
	for _, r := range rows {
		out = append(out, map[string]any{
			"yyyymm":                 r.YYYYMM,
			"guild_id":               r.GuildID,
			"guild_name":             r.GuildName,
			"master_name":            r.MasterName,
			"point":                  strconv.FormatInt(r.Point, 10),
			"rank":                   r.Rank,
			"square_icon_asset_name": r.SquareIconAssetName,
		})
	}
	return out
}

// guildCurrentYYYYMM 当前月（Asia/Shanghai）。
func guildCurrentYYYYMM(now time.Time) int {
	t := now.In(guildWireLoc())
	return t.Year()*100 + int(t.Month())
}

// guildRankingMonth 选月：请求月合法则用之，否则本月。
func guildRankingMonth(now time.Time, raw string) int {
	if ym := guildParseYYYYMM(raw); ym != 0 {
		return ym
	}
	return guildCurrentYYYYMM(now)
}

// guildRankingsHandler GET/POST /guild_rankings：月度榜单（100 行/页）。
func guildRankingsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := guildWireNow(dep)
		months, err := dep.Guild.GWRankingMonths()
		if err != nil {
			return nil, err
		}
		ym := guildRankingMonth(now, req.Query.Get("yyyymm"))
		offset, _ := strconv.Atoi(req.Query.Get("offset"))
		rows, err := dep.Guild.GWMonthlyRanking(ym, offset, guildRankingsPageLimit)
		if err != nil {
			return nil, err
		}
		key := guildCacheKey(req.Query.Get("cache_key"), "guild_rankings")
		data := map[string]any{
			"guild_ranking_yyyymms": guildRankingMonthsDoc(months),
			key:                     guildRankingRowsDoc(rows),
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guild_rankings",
			"yyyymm": ym, "offset": offset, "count": len(rows), "cache_key": key,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildRankingsMyGuildHandler /guild_rankings/my_guild：本会所在名次窗口。
func guildRankingsMyGuildHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		now := guildWireNow(dep)
		ym := guildRankingMonth(now, req.Query.Get("yyyymm"))
		key := guildCacheKey(req.Query.Get("cache_key"), "guild_rankings_self")
		rows := []GWRankingRow{}
		view, err := dep.Guild.MyGuild(req.UserID)
		if err != nil {
			return nil, err
		}
		if view.Guild != nil {
			rows, err = dep.Guild.GWRankingSelf(view.Guild.ID, ym, guildRankingsPageLimit)
			if err != nil {
				return nil, err
			}
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guild_rankings_my_guild",
			"yyyymm": ym, "count": len(rows), "cache_key": key,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{key: guildRankingRowsDoc(rows)},
		})
	})
}

// 静态奖励表文件名（AssetsDir 相对路径）。
const (
	guildRankingItemRewardsFile  = "guild_rankings_item_rewards.json"
	guildRankingSkillRewardsFile = "guild_rankings_skill_rewards.json"
)

// guildRankingItemRewardsData 静态物品奖励表（8 档，字符串化数值）。
type guildRankingItemRewardsData struct {
	Rewards []map[string]string `json:"rewards"`
}

// guildRankingSkillRewardsData 静态符文奖励表（250 行等级 + 类型对象）。
type guildRankingSkillRewardsData struct {
	Level           []map[string]any `json:"level"`
	Type            map[string]any   `json:"type"`
	TypeTitleFormat string           `json:"type_title_format"`
}

// guildStaticJSONCache 静态配置按路径缓存（进程内只解析一次）。
var guildStaticJSONCache sync.Map // path → []byte

// loadGuildStaticJSON 读取 AssetsDir 下的静态配置 JSON（带缓存）。
// 静态大表不在 Go 源码里硬编码：来源文件与 SHA-256 记录在
// artifacts/tasks/cn350-guild-live-closure-20261028/run/evidence-guild-read.md。
func loadGuildStaticJSON(path string, target any) error {
	if path == "" {
		return fmt.Errorf("cn35 公会静态配置路径为空（Deps.AssetsDir 未装配）")
	}
	cached, ok := guildStaticJSONCache.Load(path)
	if !ok {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cn35 公会静态配置读取失败 %s: %w", path, err)
		}
		guildStaticJSONCache.Store(path, raw)
		cached = raw
	}
	if err := json.Unmarshal(cached.([]byte), target); err != nil {
		return fmt.Errorf("cn35 公会静态配置非法 JSON %s: %w", path, err)
	}
	return nil
}

// guildAssetsPath 拼 AssetsDir 下的配置文件名。
func guildAssetsPath(dep Deps, name string) string {
	if dep.AssetsDir == "" {
		return ""
	}
	return filepath.Join(dep.AssetsDir, name)
}

// guildRankingsItemRewardsHandler /guild_rankings/item_rewards：8 档静态表。
func guildRankingsItemRewardsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		var cfg guildRankingItemRewardsData
		if err := loadGuildStaticJSON(guildAssetsPath(dep, guildRankingItemRewardsFile), &cfg); err != nil {
			return nil, err
		}
		if len(cfg.Rewards) == 0 {
			return nil, fmt.Errorf("cn35 公会物品奖励表为空: %s", guildRankingItemRewardsFile)
		}
		rows := []any{}
		for _, r := range cfg.Rewards {
			// 原服六键均为已格式化字符串（含千分位）；逐键复制保持固定结构。
			rows = append(rows, map[string]any{
				"min_rank":    r["min_rank"],
				"max_rank":    r["max_rank"],
				"point":       r["point"],
				"gacha_point": r["gacha_point"],
				"medal":       r["medal"],
				"guild_asset": r["guild_asset"],
			})
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guild_rankings_item_rewards",
			"tiers": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_rankings_item_rewards": rows},
		})
	})
}

// guildRankingsSkillRewardsHandler /guild_rankings/skill_rewards：250 行等级表。
func guildRankingsSkillRewardsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		var cfg guildRankingSkillRewardsData
		if err := loadGuildStaticJSON(guildAssetsPath(dep, guildRankingSkillRewardsFile), &cfg); err != nil {
			return nil, err
		}
		if len(cfg.Level) == 0 || len(cfg.Type) == 0 {
			return nil, fmt.Errorf("cn35 公会符文奖励表为空: %s", guildRankingSkillRewardsFile)
		}
		// 类型标题随月份变化（原服 201711 样本为「11月的符文奖励」）；单样本
		// 无法证明跨月格式，按格式化模板渲染并标〔推断〕。
		ym := guildRankingMonth(guildWireNow(dep), req.Query.Get("yyyymm"))
		typ := make(map[string]any, len(cfg.Type)+1)
		for k, v := range cfg.Type {
			typ[k] = v
		}
		if cfg.TypeTitleFormat != "" {
			typ["title"] = fmt.Sprintf(cfg.TypeTitleFormat, ym%100)
		}
		levels := make([]any, 0, len(cfg.Level))
		for _, row := range cfg.Level {
			levels = append(levels, row)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guild_rankings_skill_rewards",
			"levels": len(levels), "yyyymm": ym,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"guild_rankings_skill_rewards_level": levels,
				"guild_rankings_skill_rewards_type":  typ,
			},
		})
	})
}

// ---- 写面 handlers ----

// guildCreateHandler POST /guilds/create.json。
func guildCreateHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		f := req.Form
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_create",
		}
		g, err := dep.Guild.CreateGuild(req.UserID,
			f.Get("name"), f.Get("play_type_id"), f.Get("approval_type_id"),
			f.Get("recruit_text"), f.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		data := map[string]any{"user": snap.User}
		if view, verr := dep.Guild.MyGuild(req.UserID); verr == nil && view.Guild != nil {
			data["guild"] = guildDoc(view)
			data["guild_role"] = guildRoleDoc(view.MyRole)
		}
		data["guild_id"] = g.ID
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildSearchHandler POST /guilds/search.json（?recommend=true 同口）。
//
// 回包同时下发三种形状（客户端事实，见取证 §1/§7 与 GUILDLIFE350-04）：
//   - temp_search_indexes：GuildSearchIndexListPartial 的行数据源
//     （name/master_user_name/users_count/play_type_name/approval_type_name/
//     auto_approval/confirm_text/join_approve_url/description_conf）；
//   - guilds：旧读面键（保持兼容）；
//   - guild_temp：公会详情弹窗（GuildDescription.cs:42）在搜索行入口只读
//     单一 cache 键，服务端下发首行的完整公文档，使首行详情与列表同源
//     （逐行详情受客户端单键限制，另见 /guilds/show）。
func guildSearchHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		f := req.Form
		views, err := dep.Guild.SearchGuilds(req.UserID,
			f.Get("keyword"), f.Get("play_type_id"), f.Get("approval_type_id"))
		if err != nil {
			return nil, err
		}
		rows := []any{}
		for i := range views {
			rows = append(rows, guildTempDoc(&views[i]))
		}
		data := map[string]any{
			"search_index_temp":   map[string]any{},
			"guilds":              rows,
			"temp_search_indexes": rows,
		}
		if len(views) > 0 {
			// 详情弹窗 guild_temp：首行完整公文档（含 users_map/equipments）。
			data["guild_temp"] = guildTempDoc(&views[0])
			data["search_index_temp"] = searchIndexDoc(&views[0])
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_search",
			"count": len(rows), "recommend": req.Query.Get("recommend"),
			"npc": countNPCViews(views),
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// countNPCViews 搜索结果中 NPC 候选行数（取证/排障）。
func countNPCViews(views []GuildView) int {
	n := 0
	for i := range views {
		if views[i].Guild != nil && views[i].Guild.IsNPC {
			n++
		}
	}
	return n
}

// guildShowHandler GET /guilds/show?with_search_index=true&id=
// → search_index_temp + guild_temp（UserPage/GuildDescription 消费）。
func guildShowHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		gid := req.Query.Get("id")
		view, err := dep.Guild.GuildShow(gid)
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		// guild_temp 详情页入会按钮读 join_approve_url —— 用同一读面聚合
		// （成员/会长/设施与 /guilds/ 主页同源，GUILDLIFE350-04）。
		data := map[string]any{
			"guild_temp":        guildTempDoc(view),
			"guild":             guildDoc(view),
			"guild_members":     guildMembersDoc(view),
			"search_index_temp": searchIndexDoc(view),
		}
		// 出边页 useTemp 行：本人对该公会的入会/受邀按钮 URL。
		if _, _, merr := guildMemberRole(dep, req.UserID); merr == nil {
			// noop: memberRole 仅内部
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_show", "guild_id": gid,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildMemberRole 内部小助手（占位：wire 不做跨账号成员查询）。
func guildMemberRole(_ Deps, _ string) (string, int, error) { return "", 0, nil }

// guildJoinHandler GET /guilds/join?id=（出边页入会/申请）。
func guildJoinHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		// 原服下发的入会 URL 是 /guilds/join/?guild_id=<gid>（guilds.json::
		// data.search_index.join_approve_url）；成员页历史路径用 ?id=。两个都收。
		gid := req.Query.Get("guild_id")
		if gid == "" {
			gid = req.Query.Get("id")
		}
		auto, err := dep.Guild.JoinGuild(req.UserID, gid, req.Query.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		data, derr := guildRefreshData(dep, req.UserID)
		if derr != nil {
			return nil, derr
		}
		data["joined"] = auto
		data["guild_id"] = gid
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_join",
			"guild_id": gid, "auto": auto,
		}
		_ = snap
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildDecideSubHandler 审批/拒绝单条申请。
func guildDecideSubHandler(dep Deps, approve bool) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		subID := req.Query.Get("sub_id")
		err := dep.Guild.DecideSubscription(req.UserID, subID, approve, req.Query.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "sub_id": subID, "approve": approve,
		}
		data, derr := guildRefreshData(dep, req.UserID)
		if derr != nil {
			return nil, derr
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildRejectAllHandler POST /guilds/reject_all_subscription。
func guildRejectAllHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		if err := dep.Guild.RejectAllSubscriptions(req.UserID, req.Form.Get("op_key")); err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		return cn35wire.MarshalJSON(statusDoc("ok"))
	})
}

// guildDropoutHandler GET /guilds/dropout（自退/踢人同口）。
func guildDropoutHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		target := req.Query.Get("target_user_id")
		err := dep.Guild.Dropout(req.UserID, target, req.Query.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		data, derr := guildRefreshData(dep, req.UserID)
		if derr != nil {
			return nil, derr
		}
		if target != "" && target != req.UserID {
			// 踢人后操作者的成员列表也要刷新（同一读面聚合）。
			if view, verr := dep.Guild.MyGuild(req.UserID); verr == nil && view.Guild != nil {
				data["guild_members"] = guildMembersDoc(view)
			}
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_dropout",
			"target": target,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildChangeRoleHandler 会长/副长任免。
func guildChangeRoleHandler(dep Deps, role int) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		target := req.Query.Get("target_user_id")
		err := dep.Guild.ChangeRole(req.UserID, target, role, req.Query.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		data, derr := guildRefreshData(dep, req.UserID)
		if derr != nil {
			return nil, derr
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildUpdateHandler /guilds/update（GET ?play_type_id= / POST body）。
func guildUpdateHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		var in GuildUpdateInput
		if v := req.Query.Get("play_type_id"); v != "" {
			in.PlayTypeID = &v
		}
		if v := req.Query.Get("approval_type_id"); v != "" {
			in.ApprovalTypeID = &v
		}
		// 键存在即视为提交——空串是清空文案/留言的合法新值。
		if _, ok := req.Form["recruit_text"]; ok {
			v := req.Form.Get("recruit_text")
			in.RecruitText = &v
		}
		if _, ok := req.Form["memo"]; ok {
			v := req.Form.Get("memo")
			in.Memo = &v
		}
		if err := dep.Guild.UpdateGuild(req.UserID, in, req.Form.Get("op_key")); err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_update",
		}
		return cn35wire.MarshalJSON(statusDoc("ok"))
	})
}

// guildSetShiftsHandler POST /guilds/set_shifts {0..6: bool}。
func guildSetShiftsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		var shifts [7]bool
		for i := 0; i < 7; i++ {
			v := req.Form.Get(strconv.Itoa(i))
			shifts[i] = v == "true" || v == "True" || v == "1"
		}
		if err := dep.Guild.SetShifts(req.UserID, shifts, req.Form.Get("op_key")); err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		return cn35wire.MarshalJSON(statusDoc("ok"))
	})
}

// guildInvestHandler GET /guilds/invest?equipment_type_id=&value=。
func guildInvestHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		typeID := req.Query.Get("equipment_type_id")
		value, _ := strconv.ParseInt(req.Query.Get("value"), 10, 64)
		view, err := dep.Guild.Invest(req.UserID, typeID, value, req.Query.Get("op_key"))
		if err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		snap, serr := dep.Accounts.Snapshot(req.UserID)
		if serr != nil {
			return nil, serr
		}
		data := map[string]any{"user": snap.User}
		if view != nil && view.Guild != nil {
			data["guild"] = guildDoc(view)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_invest",
			"type_id": typeID, "value": value,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// guildSetDefenseHandler GET /guilds/set_deck?party_index=&uuid=。
func guildSetDefenseHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		slot, _ := strconv.Atoi(req.Query.Get("party_index"))
		uuid := req.Query.Get("uuid")
		if err := dep.Guild.SetDefense(req.UserID, slot, uuid, req.Query.Get("op_key")); err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		slots, err := dep.Guild.DefenseOf(req.UserID)
		if err != nil {
			return nil, err
		}
		deck, err := guildDefenseDeckDoc(dep, slots)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_deck": deck, "guild_deck_data": guildDeckDataDoc()},
		})
	})
}

// guildRemoveDefenseHandler GET /guilds/remove_from_deck?uuid=。
func guildRemoveDefenseHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuid := req.Query.Get("uuid")
		if err := dep.Guild.RemoveDefense(req.UserID, uuid, req.Query.Get("op_key")); err != nil {
			return cn35wire.MarshalJSON(guildErrModal(err))
		}
		slots, err := dep.Guild.DefenseOf(req.UserID)
		if err != nil {
			return nil, err
		}
		deck, err := guildDefenseDeckDoc(dep, slots)
		if err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"guild_deck": deck, "guild_deck_data": guildDeckDataDoc()},
		})
	})
}

// guildPushNotifyHandler /guilds/send_push_notify：推送通知占位（副长以上，
// 无推送通道时回成功——客户端仅做 ResponseFilter 确认）。
func guildPushNotifyHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_send_push_notify",
		}
		return cn35wire.MarshalJSON(statusDoc("ok"))
	})
}

// guildErrModal 错误 → 客户端弹窗映射（200+modal，不裸 4xx）。
func guildErrModal(err error) map[string]any {
	switch {
	case errors.Is(err, ErrGuildName):
		return guildModal("无法创建", "公会名须为 1-8 个字符")
	case errors.Is(err, ErrGuildJoined):
		return guildModal("无法入会", "你已加入公会")
	case errors.Is(err, ErrGuildFull):
		return guildModal("无法入会", "公会已满员")
	case errors.Is(err, ErrGuildNotFound):
		return guildModal("无法操作", "公会不存在")
	case errors.Is(err, ErrGuildNoMember):
		return guildModal("无法操作", "你未加入公会")
	case errors.Is(err, ErrGuildNoPerm):
		return guildModal("无法操作", "公会权限不足")
	case errors.Is(err, ErrGuildOp):
		return guildModal("无法操作", "操作重复或参数不一致，请重新打开页面再试")
	case errors.Is(err, ErrGuildSubNotFound):
		return guildModal("无法操作", "申请记录不存在")
	case errors.Is(err, ErrGuildSubDecided):
		return guildModal("无法操作", "该申请已处理")
	case errors.Is(err, ErrGuildInvest):
		return guildModal("无法投资", "投资额非法")
	case errors.Is(err, ErrGuildAsset):
		return guildModal("无法投资", "公会资材不足")
	case errors.Is(err, ErrGuildDeckSlot):
		return guildModal("无法设防", "槽位非法")
	case errors.Is(err, ErrGuildMonster):
		return guildModal("无法设防", "魔宠未持有或已在防卫卡组")
	case errors.Is(err, ErrGWNoSession):
		return guildModal("无法操作", "当前不在会战场次")
	case errors.Is(err, ErrGWBP):
		return guildModal("无法出战", "BP 不足")
	case errors.Is(err, ErrGWAttackNotFound):
		return guildModal("无法战斗", "战斗会话不存在")
	case errors.Is(err, ErrGWAttackState):
		return guildModal("无法战斗", "战斗会话状态不允许")
	case errors.Is(err, ErrGWDifficulty):
		return guildModal("无法战斗", "难度非法")
	case errors.Is(err, ErrGWTarget):
		return guildModal("无法战斗", "目标非法（不在同组或为己方）")
	default:
		return guildModal("无法操作", err.Error())
	}
}

// guildWireNow 注入时钟占位：wire 统一取 dep.GuildClock（未装配→time.Now）。
func guildWireNow(dep Deps) time.Time {
	if dep.GuildClock != nil {
		return dep.GuildClock()
	}
	return time.Now()
}

// registerGuildReadRoutes 公会只读面（GE-* 契约面）。
func registerGuildReadRoutes(reg guildRegister, dep Deps) {
	reg(http.MethodGet, "/guilds/", "公会主读面（GUILD350-02）", guildsIndexHandler(dep))
	reg(http.MethodGet, "/guilds/deck", "防卫卡组读面", guildDeckHandler(dep))
	reg(http.MethodGet, "/guilds/monsters", "可选防卫魔宠", guildMonstersHandler(dep))
	reg(http.MethodGet, "/guilds/search_index", "公会检索配置", guildSearchIndexHandler(dep))
	reg(http.MethodGet, "/guilds/subscriptions", "入会申请列表", guildSubscriptionsHandler(dep))
	reg(http.MethodGet, "/guilds/show", "公会信息出边", guildShowHandler(dep))
	reg(http.MethodGet, "/guilds/search.json", "公会搜索", guildSearchHandler(dep))
	// GC-06 赛季/排名读面（routes 20/20 缺口之一：/guild_rankings/my_guild）。
	reg(http.MethodGet, "/guild_rankings", "会战月度排名", guildRankingsHandler(dep))
	reg(http.MethodGet, "/guild_rankings/my_guild", "本会历史排名", guildRankingsMyGuildHandler(dep))
	reg(http.MethodGet, "/guild_rankings/item_rewards", "会战物品奖励表", guildRankingsItemRewardsHandler(dep))
	reg(http.MethodGet, "/guild_rankings/skill_rewards", "会战符文奖励表", guildRankingsSkillRewardsHandler(dep))
	// /waiting_users、/waiting_users/recruit 由 T4（社交面）在
	// registerGuildMemberRoutes 接管；本注册表不再登记，避免抢占。
}

// registerGuildOrgWriteRoutes 组织写面（建会/入会/审批/退出/职位/设置/设施/防卫）。
func registerGuildOrgWriteRoutes(reg guildRegister, dep Deps) {
	reg(http.MethodPost, "/guilds/create.json", "创建公会", guildCreateHandler(dep))
	reg(http.MethodGet, "/guilds/join", "入会/申请", guildJoinHandler(dep))
	reg(http.MethodGet, "/guilds/approve_subscription", "批准申请", guildDecideSubHandler(dep, true))
	reg(http.MethodGet, "/guilds/reject_subscription", "拒绝申请", guildDecideSubHandler(dep, false))
	reg(http.MethodPost, "/guilds/reject_all_subscription", "拒全部申请", guildRejectAllHandler(dep))
	reg(http.MethodGet, "/guilds/dropout", "退会/踢人", guildDropoutHandler(dep))
	reg(http.MethodGet, "/guilds/change_master", "禅让会长", guildChangeRoleHandler(dep, GuildRoleMaster))
	reg(http.MethodGet, "/guilds/change_sub_master", "任命副长", guildChangeRoleHandler(dep, GuildRoleSubMaster))
	reg(http.MethodGet, "/guilds/fire_sub_master", "解除副长", guildChangeRoleHandler(dep, GuildRoleMember))
	reg(http.MethodGet, "/guilds/update", "公会设置变更", guildUpdateHandler(dep))
	reg(http.MethodPost, "/guilds/update", "公会设置变更 POST", guildUpdateHandler(dep))
	reg(http.MethodPost, "/guilds/set_shifts", "曜日参战设定", guildSetShiftsHandler(dep))
	reg(http.MethodGet, "/guilds/invest", "设施投资", guildInvestHandler(dep))
	reg(http.MethodGet, "/guilds/set_deck", "防卫卡组设置", guildSetDefenseHandler(dep))
	reg(http.MethodGet, "/guilds/remove_from_deck", "防卫卡组移除", guildRemoveDefenseHandler(dep))
	reg(http.MethodGet, "/guilds/send_push_notify", "推送通知占位", guildPushNotifyHandler(dep))
	// 尾斜杠形态：成员管理按钮的客户端 URL 带尾斜杠
	// （GuildMemberListRow.cs:64/74/84/94：/guilds/change_master/?id=&target_user_id= 等）。
	// Registry.Lookup 是精确匹配 + 前缀兜底，`/guilds/` 是精确路由而非前缀，
	// 不补登记就会 501（客户端直接报协议错误）。与既有尾斜杠约定一致。
	reg(http.MethodGet, "/guilds/dropout/", "退会/踢人（尾斜杠）", guildDropoutHandler(dep))
	reg(http.MethodGet, "/guilds/change_master/", "禅让会长（尾斜杠）", guildChangeRoleHandler(dep, GuildRoleMaster))
	reg(http.MethodGet, "/guilds/change_sub_master/", "任命副长（尾斜杠）", guildChangeRoleHandler(dep, GuildRoleSubMaster))
	reg(http.MethodGet, "/guilds/fire_sub_master/", "解除副长（尾斜杠）", guildChangeRoleHandler(dep, GuildRoleMember))
	reg(http.MethodGet, "/guilds/join/", "入会/申请（尾斜杠）", guildJoinHandler(dep))
}
