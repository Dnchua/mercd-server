// guild_social_wire.go 公会社交读面（GUILDCLOSE350 GC-07/GC-08）。
//
// 端点与字段形状的原服证据（只读，勿改写）：
//   - artifacts/mlk-capture-201711/decrypted/1/guilds_users.json
//     → {"data":{"guild_users":[…], "guild_users_<gid>":[…]}}（两键同内容，20 行/键）
//   - artifacts/mlk-capture-201711/decrypted/1/waiting_users.json
//     → {"data":{"waiting_users":[{name,level,leader_unit_id,logged_at,created_at,
//     play_type_id, square_icon_asset_name, confirm_text, user_status,
//     invite_url, user_detail_message}]}}
//   - artifacts/mlk-capture-201711/decrypted/1/battles_comments.json
//     → {"data":{"battle_comments":1511966405,"battle_comments_<battle_id>":[]}}
//   - server/testdata/mlk-live/guild/contract.json（go: type 校验真源）
//
// 客户端消费证据（client/decoded，反编译树在
// artifacts/tasks/toto-cn-3.5.0-onboarding/run/ilspy-cs/）：
//   - GuildMemberListRow.cs:64/74/84/94 —— 行内按钮读 document.s("id") 与
//     cache user.guild_id；行文本键见 app_conf pages.GuildMembers.parts.ListParts.row_config
//     （square_icon_asset_name / name_and_role / user_status / logged_at）。
//   - app_conf pages.GuildMembers 的 ListParts.data_key 被 /guilds/ 下发的
//     guild_member_option.parts.ListParts.data_key（=guild_users）覆盖，requireData
//     指向 /guilds/users/?id=<gid>；录包同响应里另有 guild_users_<gid> 动态键，
//     因此本处理器**两键同发**（原服行为，不是冗余）。
//   - GuildNotJoinedPageBase.cs:83 —— POST /waiting_users/add.json（play_type_id）。
//   - app_conf pages.GuildWaitingList（data_key=waiting_users，行含 confirm_text +
//     invite_url 的 ConfirmModal）与 pages.GuildWaitingListRecruit
//     （requireData temp_search_indexes → /waiting_users/recruit，
//     行由 GuildSearchIndexListPartial 渲染 = 公会 search_index 文档）。
//   - BattleMessages.cs:144 —— cache["battle_comments_"+battleId].docList；
//     DataCache.cs:299 —— cache_updated_info.battle_comments → 已读时间戳（Unix 秒）。
//   - Message.cs —— 留言行键 id/text/user_id/user_name/created_at。
//
// 原服未下发的动作路径一律不造：邀请接受/拒绝走原服 join_approve_url
// （/guilds/join/?guild_id=，search_index 行内下发），不新增
// /waiting_users/accept|reject。
package cn35game

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// 社交面常量。原服未取证项按〔推断〕标注并给出依据；不得写成原服事实。
const (
	// GuildWaitingTTL 等待邀请登记/邀请的有效期〔推断〕：原服无过期字段，
	// 只有页文案「如果登记邀请时间过的太久了，请再次登记邀请信息」
	// （app_conf GuildWaitingListRecruit.no_record_modal），说明存在过期语义但
	// 没给数值；取 7 天并在证据文件登记为待取证缺口。
	GuildWaitingTTL = 7 * 24 * time.Hour
	// GuildWaitingPoolMaxLimit NPC 待邀请候补池上限（配置写入时收敛，防无限建数据）。
	GuildWaitingPoolMaxLimit = 12
	// BattleCommentMaxRunes 单条留言字符上限：客户端
	// input.defaultText = 110（BattleMessages.cs:59/OnPrepare 第 59 行）。
	BattleCommentMaxRunes = 110
	// BattleCommentListLimit 读面单次下发条数上限（客户端 GetMessagesQueue
	// 只保留最后 50 条，Messages.cs:50<queue.Count 出队）。
	BattleCommentListLimit = 100
)

// 社交面受控错误（handler 映射为业务 modal；不吞错成「成功」）。
var (
	// ErrGuildSocialUser 目标玩家/NPC 不存在。
	ErrGuildSocialUser = errors.New("cn35game: 社交目标不存在")
	// ErrGuildSocialSelf 不能邀请自己。
	ErrGuildSocialSelf = errors.New("cn35game: 不能邀请自己")
	// ErrGuildSocialPlayType 公会方针取值非法。
	ErrGuildSocialPlayType = errors.New("cn35game: 公会方针非法")
	// ErrGuildSocialComment 留言内容非法（空/超长/类型未开放）。
	ErrGuildSocialComment = errors.New("cn35game: 留言内容非法")
	// ErrGuildSocialUnsupported 未装配社交存储（读面不能假装空成功）。
	ErrGuildSocialUnsupported = errors.New("cn35game: 未装配公会社交存储")
)

// GuildWaitingPoolSettings 单机扩展「NPC 待邀请候补池」**独立配置**。
//
// 红线要求（Lead 2026-10-28 拍板）：语义有意不同的单机扩展必须独立命名配置并
// 保留原服差异说明。原服 /waiting_users 是**真人登记队列**；本机没有第二个
// 真人，因此以有界持久 NPC 候补兜底，但：
//   - 生效条件 = 单机域开关(cn35_guild_solo_state.enabled) AND 本配置 Enabled；
//   - 任一为关 → 不注入任何 NPC 候选，只返回真实登记队列（可以为空）；
//   - Max 上限 GuildWaitingPoolMaxLimit。
//
// 落库在 cn35_guild_social_config（迁移 0040），不复用 solo 总开关。
type GuildWaitingPoolSettings struct {
	Enabled bool
	Max     int
}

// DefaultGuildWaitingPoolSettings 重建运营初值（非原服数据）：开启，保持 6 个候补。
var DefaultGuildWaitingPoolSettings = GuildWaitingPoolSettings{Enabled: true, Max: 6}

// NormalizeGuildWaitingPoolSettings 收敛到安全区间（防负值/超上限）。
func NormalizeGuildWaitingPoolSettings(s GuildWaitingPoolSettings) GuildWaitingPoolSettings {
	if s.Max < 0 {
		s.Max = 0
	}
	if s.Max > GuildWaitingPoolMaxLimit {
		s.Max = GuildWaitingPoolMaxLimit
	}
	return s
}

// WaitingUser /waiting_users 行（待邀请候补）。
//
// IsNPC 标记单机扩展候补行（id 命名空间 npccNNNN），用于排障与邀请分支；
// 客户端未声明该键，下发行里不带（保持原服键集合）。
type WaitingUser struct {
	UserID       string
	Name         string
	Level        int64
	LeaderUnitID string
	PlayTypeID   string
	LoggedAt     int64 // 最后活动时间（Unix 秒）
	CreatedAt    int64 // 登记/建池时间（Unix 秒）
	IsNPC        bool
}

// BattleComment 会战留言行（/battles/comments 的 battle_comments_<id> 元素）。
type BattleComment struct {
	ID        string
	BattleID  string
	UserID    string
	UserName  string
	Body      string
	CreatedAt int64
	// UnitID 作者头像单位（原服消息行的 unit_id；客户端 Message.unitId 决定
	// 气泡形态与头像贴图，BattleMessages 行渲染必需）。
	UnitID int64
}

// GuildSocialStore 社交面持久化（GC-07/08）。
//
// 独立于 GuildStore 接口：成员原路径/等待邀请/会战留言是社交域新增能力，
// 不进 GuildStore（guild.go 属其他 taker）。生产装配为 *cn35store.Store。
type GuildSocialStore interface {
	// GuildMemberExtras /guilds/users/ 行补充数据：
	// loggedAt = 成员最后活动时间（Unix 秒），point = 本会贡献点数。
	GuildMemberExtras(guildID string) (loggedAt map[string]int64, point map[string]int64, err error)
	// WaitingAdd 本人登记/更新「等待邀请」（play_type_id 取选项 value）。
	WaitingAdd(userID, playTypeID, opKey string) error
	// WaitingState 本人是否在等待邀请队列（user.is_waiting）。
	WaitingState(userID string) (bool, error)
	// WaitingList 待邀请候补列表（真人登记 ∪ 单机 NPC 候补；后者受独立配置门控）。
	//
	// includeReal=false 时**不下发任何真人登记行**：用于未入会 viewer 的
	// 隐私收敛（见 handler 注释）；NPC 候选是重建身份域数据，不受此开关影响。
	WaitingList(viewerID, playTypeFilter string, ttl time.Duration, now time.Time,
		includeReal bool) ([]WaitingUser, error)
	// WaitingPoolSettings 读单机 NPC 候补池配置（诊断/测试/运营面）。
	WaitingPoolSettings() (GuildWaitingPoolSettings, error)
	// ConfigureWaitingPool 落库 NPC 候补池配置（独立命名，不影响 solo 总开关）。
	ConfigureWaitingPool(cfg GuildWaitingPoolSettings) error
	// InviteUser 邀请候补入会：真人 → pending invite；NPC → 直接入会〔单机扩展〕。
	InviteUser(actorID, targetUserID, opKey string, now time.Time) (subID string, err error)
	// InvitedGuildIDs 本人收到的、未过期的邀请所属公会（/waiting_users/recruit）。
	InvitedGuildIDs(userID string, ttl time.Duration, now time.Time) ([]string, error)
	// BattleCommentFeed 会战留言房间读取：roomID = 当前会战场次 id（原服
	// guild.battle_id 同源，见 guildDocExtra.BattleID），返回最新留言 Unix 秒与行。
	BattleCommentFeed(roomID string, limit int) (latest int64, rows []BattleComment, err error)
	// AddBattleComment 写入一条会战留言（校验参战归属、长度、短时去重）。
	// roomID 必须是本会参战的场次 id；调用者必须是该场次参战公会成员。
	AddBattleComment(userID, roomID, body string, now time.Time) (BattleComment, error)
}

// guildSocial 取社交存储；未装配时读面必须显式失败，不能空态冒充。
func guildSocial(dep Deps) (GuildSocialStore, bool) {
	if dep.Guild == nil {
		return nil, false
	}
	s, ok := dep.Guild.(GuildSocialStore)
	return s, ok
}

// ---- 文档组装 ----

// guildRoleDocName 职位显示名（原服录包 guild_role.name：1 成员/2 副会长/3 公会长）。
func guildRoleDocName(role int) string {
	switch role {
	case GuildRoleMaster:
		return "公会长"
	case GuildRoleSubMaster:
		return "副会长"
	default:
		return "成员"
	}
}

// socialPlayTypeName 方针显示名。
//
// 文案真源归 T1（guild_wire.go 的 guildPlayTypeNames，覆盖 ""/0=未指定、
// 1..4 四种方针）；本文件只读不复制第二份枚举，未知取值原样回显（不猜语义）。
func socialPlayTypeName(id string) string {
	if n, ok := guildPlayTypeNames[id]; ok {
		return n
	}
	return id
}

// socialEscapeID 严格百分号编码（保留 unreserved 字符）。
//
// 依据：原服 user_detail_message 里 uid 的 '*'→%2A、'$'→%24、'/'→%2F、
// '?'→%3F，字母数字与 '-' 原样（guilds_users.json / waiting_users.json 样本）。
// net/url.QueryEscape 对 '*' 不编码，故不能直接用。
func socialEscapeID(s string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'A' && ch <= 'Z', ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9',
			ch == '-', ch == '_', ch == '.', ch == '~':
			b.WriteByte(ch)
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[ch>>4])
			b.WriteByte(upperhex[ch&0x0f])
		}
	}
	return b.String()
}

// socialUserDetailMessage 用户详情页跳转串。
//
// 原服两种键序都存在：成员行 method=Stack 在前（guilds_users.json），
// 候补行 method=Stack 在末尾（waiting_users.json）。按端点各自的样本原样复现。
func socialUserDetailMessage(uid string, methodFirst bool) string {
	const base = "name=UserPage&base=Common/Pages/UserPage"
	path := "%2Fusers%2Fshow%3Fid%3D" + socialEscapeID(uid)
	if methodFirst {
		return base + "&method=Stack&additional_require_data_path=" + path
	}
	return base + "&additional_require_data_path=" + path + "&method=Stack"
}

// socialSquareIcon 头像 asset 名（原服 unit/unit_square_<leader_unit_id>）。
func socialSquareIcon(leaderUnitID int64) string {
	return fmt.Sprintf("unit/unit_square_%d", leaderUnitID)
}

// socialLeaderUnitID 把 profiles.leader_unit_id（TEXT）转行内 int；
// 解析失败按 0（客户端只做贴图名拼接，不参与业务）。
func socialLeaderUnitID(raw string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// socialThousands 千分位格式化（原服 user_status 第二行 "19,460"）。
func socialThousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, ch := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}

// guildUserStatus 成员行 user_status = "等级\n攻击公会点数\n公会内贡献排行"。
// 原服无点数/名次的成员该行是 "-"（guilds_users.json 行样本），不是 0。
func guildUserStatus(level, point, ranking int64) string {
	p, r := "-", "-"
	if point > 0 {
		p = socialThousands(point)
	}
	if ranking > 0 {
		r = strconv.FormatInt(ranking, 10)
	}
	return fmt.Sprintf("%d\n%s\n%s", level, p, r)
}

// guildContributionRanks 公会内贡献名次（point 降序；同分按入会时间、user_id 定序）。
// 〔推断〕原服 ranking 是「公会内贡献排行」（app_conf 列标题），具体统计周期
// 未取证；这里用 cn35_gw_member_scores 的累计 gp_earned（同公会全部场次）。
func guildContributionRanks(view *GuildView, point map[string]int64) map[string]int64 {
	type entry struct {
		uid  string
		pt   int64
		join int64
	}
	list := []entry{}
	for _, m := range view.Members {
		if p := point[m.UserID]; p > 0 {
			list = append(list, entry{m.UserID, p, joinedAtUnix(m.JoinedAt)})
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].pt != list[j].pt {
			return list[i].pt > list[j].pt
		}
		if list[i].join != list[j].join {
			return list[i].join < list[j].join
		}
		return list[i].uid < list[j].uid
	})
	out := map[string]int64{}
	for i, e := range list {
		out[e.uid] = int64(i + 1)
	}
	return out
}

// guildUserRows /guilds/users/ 成员行。
//
// 下发键 = 客户端**实际消费**的 11 键：
//   - app_conf pages.GuildMembers.parts.ListParts.row_config.widgets：
//     icon←square_icon_asset_name、name←name_and_role、status_label←user_status、
//     last_login←logged_at(niceTime)、OnClick←user_detail_message；
//   - GuildMemberListRow.cs:25/64/74/84/94 ← guild_role.id 与 id。
//
// 原服录包另有 point/ranking（仅对有贡献的成员下发），但客户端行内没有任何
// 读点：GuildMembers 行的 row_prefab 是 GuildMemberListRow，其 UpdateContents
// 只走 base.UpdateContents()（row_config widgets）+ 按钮 URL 拼装；两个键
// 只在服务端拼 user_status 时用到。按「未验证的推断不得写成事实」原则
// 不下发这两个未经证实统计口径的原始数值，只保留 user_status 文本
// （原服三种取值样本见 guilds_users.json：'263\n19,460\n2' / '268\n-\n-' /
// '222\n24,880 (蓄水库消费 19,200)\n1'；括号后缀是蓄水库消费口径，未取证，
// 本实现不下发该后缀）。
func guildUserRows(view *GuildView, loggedAt, point map[string]int64) []any {
	ranks := guildContributionRanks(view, point)
	rows := []any{}
	for _, m := range view.Members {
		leader := socialLeaderUnitID(m.LeaderUnitID)
		roleName := guildRoleDocName(m.Role)
		pt := point[m.UserID]
		rank := ranks[m.UserID]
		rows = append(rows, map[string]any{
			"name":                   m.Name,
			"level":                  m.Level,
			"leader_unit_id":         leader,
			"logged_at":              loggedAt[m.UserID],
			"guild_joined_at":        joinedAtUnix(m.JoinedAt),
			"square_icon_asset_name": socialSquareIcon(leader),
			"id":                     m.UserID,
			"user_status":            guildUserStatus(m.Level, pt, rank),
			"guild_role":             map[string]any{"id": m.Role, "name": roleName},
			"name_and_role":          m.Name + "(" + roleName + ")",
			"user_detail_message":    socialUserDetailMessage(m.UserID, true),
		})
	}
	return rows
}

// waitingUserRows /waiting_users 行（键集合 = 原服录包 11 键）。
func waitingUserRows(users []WaitingUser) []any {
	rows := []any{}
	for _, u := range users {
		leader := socialLeaderUnitID(u.LeaderUnitID)
		rows = append(rows, map[string]any{
			"name":                   u.Name,
			"level":                  u.Level,
			"leader_unit_id":         leader,
			"logged_at":              u.LoggedAt,
			"created_at":             u.CreatedAt,
			"play_type_id":           u.PlayTypeID, // 原服为**字符串**（录包 "1"）
			"square_icon_asset_name": socialSquareIcon(leader),
			"confirm_text":           "邀请[eb8900]" + u.Name + "[-]吗？",
			"user_status":            fmt.Sprintf("%d\n%s", u.Level, socialPlayTypeName(u.PlayTypeID)),
			// 原服 invite_url 里 user_id 原样未编码（录包 uid 含 $/*）。
			"invite_url":          "/waiting_users/invite/?user_id=" + u.UserID,
			"user_detail_message": socialUserDetailMessage(u.UserID, false),
		})
	}
	return rows
}

// battleCommentRows 留言行（Message.cs 消费键）。
// battleCommentRows 留言行（客户端 Message.cs 构造器读取的全部键）。
//
// 键集对齐原服消息行（decrypted/1/users_messages.2.json::data.messages.*）：
// id/text/type_id/user_id/user_name/unit_id/created_at + 可选 help/raid/addition/
// small_text/stamp/private/team_id/entry_url。client Message 对缺键有默认值，
// 但 BattleMessages 行渲染（气泡形态、头像、可复制文本）依赖 type_id 与 unit_id，
// 缺失会走空贴图/空单位分支。
func battleCommentRows(rows []BattleComment) []any {
	out := []any{}
	for _, r := range rows {
		out = append(out, map[string]any{
			"id":              r.ID,
			"text":            r.Body,
			"type_id":         "battle_comment",
			"user_id":         r.UserID,
			"user_name":       r.UserName,
			"user_short_name": r.UserName,
			"unit_id":         r.UnitID,
			"created_at":      r.CreatedAt,
			"helped_at":       0,
			"help_id":         "",
			"raid_id":         "",
			"addition":        "",
			"small_text":      "",
			"stamp":           "",
			"private":         false,
			"team_id":         "",
			"entry_url":       "",
		})
	}
	return out
}

// socialErrModal 社交面错误 → 弹窗（未命中则回落公会域通用映射）。
func socialErrModal(err error) map[string]any {
	switch {
	case errors.Is(err, ErrGuildSocialUser):
		return guildModal("无法操作", "该玩家不存在")
	case errors.Is(err, ErrGuildSocialSelf):
		return guildModal("无法操作", "不能邀请自己")
	case errors.Is(err, ErrGuildSocialPlayType):
		return guildModal("无法操作", "公会方针取值非法")
	case errors.Is(err, ErrGuildSocialComment):
		return guildModal("无法发言", fmt.Sprintf("留言须为 1-%d 个字符", BattleCommentMaxRunes))
	case errors.Is(err, ErrGuildJoined):
		return guildModal("无法邀请", "该玩家已加入公会")
	default:
		return guildErrModal(err)
	}
}

// socialParam 取参数：query 与表单都查（GET 走 query、POST 走加密体）。
func socialParam(req *cn35wire.GameRequest, key string) string {
	if v := req.Query.Get(key); v != "" {
		return v
	}
	return req.Form.Get(key)
}

// socialCopyUser 复制 user 文档并补 is_waiting（等待邀请状态）：
// user 快照来自 AccountStore，社交状态不进账号快照，由本域在回包里补键。
func socialCopyUser(user map[string]any, isWaiting bool) map[string]any {
	out := map[string]any{}
	for k, v := range user {
		out[k] = v
	}
	out["is_waiting"] = isWaiting
	return out
}

// ---- handlers ----

// guildUsersHandler GET|POST /guilds/users/?id=<guild_id>
//
// 原服回包：{"data":{"guild_users":[…],"guild_users_<gid>":[…]}}。
// 不传 id 时取本人公会（客户端 guild_member_option.requireData 恒带 id，
// 契约回放不带参数，必须两条路都成立）。
func guildUsersHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		social, ok := guildSocial(dep)
		if !ok {
			return nil, ErrGuildSocialUnsupported
		}
		view, err := dep.Guild.MyGuild(req.UserID)
		if err != nil {
			return nil, err
		}
		gid := socialParam(req, "id")
		if gid == "" {
			gid = socialParam(req, "guild_id")
		}
		if gid == "" && view.Guild != nil {
			gid = view.Guild.ID
		}
		if gid == "" {
			return cn35wire.MarshalJSON(guildModal("无法操作", "你未加入公会"))
		}
		// 成员列表仅本会成员可见〔推断〕：客户端唯一入口是公会主页的
		// guild_member_option（id 恒为本会），非成员无此页面。
		if view.Guild == nil || view.Guild.ID != gid {
			return cn35wire.MarshalJSON(guildModal("无法操作", "你未加入公会"))
		}
		loggedAt, point, err := social.GuildMemberExtras(gid)
		if err != nil {
			return nil, err
		}
		rows := guildUserRows(view, loggedAt, point)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "guilds_users",
			"guild_id": gid, "rows": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"guild_users":        rows,
				"guild_users_" + gid: rows,
			},
		})
	})
}

// waitingUsersHandler GET|POST /waiting_users
//
// 原服回包 {"data":{"waiting_users":[…]}}（不是 guild_subscriptions）。
// 排序：created_at 升序（页面 reverse=true 展示最新在最上）。
func waitingUsersHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		social, ok := guildSocial(dep)
		if !ok {
			return nil, ErrGuildSocialUnsupported
		}
		// 按 viewer 维度过滤（Lead 2026-10-28 复核要求）：
		//   - 已入会 viewer：可见真人登记队列 ∪ NPC 候补（他确实有邀请入口，
		//     客户端 GuildTop.UpdateDisplayForGuildRole 只对 Role.User 禁用按钮）；
		//   - 未入会 viewer：**只**下发 NPC 候补（重建身份域数据），真人登记行
		//     一律不下发 —— 读面放宽到「已登录可读」不等于泄露其他真实玩家的行。
		// 行字段本身只有原服 11 键（name/level/leader_unit_id/…/invite_url），
		// 不含账号名、邮箱、余额等隐私字段。
		// 写面（/waiting_users/invite/）仍严格校验「已入会且副长以上」。
		view, err := dep.Guild.MyGuild(req.UserID)
		if err != nil {
			return nil, err
		}
		users, err := social.WaitingList(req.UserID, socialParam(req, "play_type_id"),
			GuildWaitingTTL, guildWireNow(dep), view.Guild != nil)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "waiting_users",
			"rows": len(users),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"waiting_users": waitingUserRows(users)},
		})
	})
}

// waitingAddHandler POST|GET /waiting_users/add.json
//
// 客户端 GuildNotJoinedPageBase.cs:83：url=/waiting_users/add.json，
// 参数 play_type_id=期望方针。入会校验：已入会 → ErrGuildJoined（弹窗），
// 不写任何状态。
func waitingAddHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		social, ok := guildSocial(dep)
		if !ok {
			return nil, ErrGuildSocialUnsupported
		}
		if err := social.WaitingAdd(req.UserID, socialParam(req, "play_type_id"),
			socialParam(req, "op_key")); err != nil {
			return cn35wire.MarshalJSON(socialErrModal(err))
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "waiting_users_add",
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"user": socialCopyUser(snap.User, true)},
		})
	})
}

// waitingInviteHandler GET|POST /waiting_users/invite/?user_id=<uid>
//
// 路径与参数取自原服 /waiting_users 行内 invite_url（录包 waiting_users.json），
// 客户端行按钮走 ConfirmModal{confirm_key:confirm_text,url_key:invite_url}
// （app_conf pages.GuildWaitingList.row_config.invite_button）。
//
// 权限/容量/重复/过期规则全部在 store 事务内判定，见 cn35store/guild_social.go。
func waitingInviteHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		social, ok := guildSocial(dep)
		if !ok {
			return nil, ErrGuildSocialUnsupported
		}
		target := socialParam(req, "user_id")
		if target == "" {
			target = socialParam(req, "id")
		}
		subID, err := social.InviteUser(req.UserID, target,
			socialParam(req, "op_key"), guildWireNow(dep))
		if err != nil {
			return cn35wire.MarshalJSON(socialErrModal(err))
		}
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, err
		}
		data := map[string]any{"user": snap.User}
		// 邀请成功后列表即时刷新（客户端行按钮没有额外 reload 钩子）。
		if users, lerr := social.WaitingList(req.UserID, "",
			GuildWaitingTTL, guildWireNow(dep), true); lerr == nil {
			data["waiting_users"] = waitingUserRows(users)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "waiting_users_invite",
			"target": target, "sub_id": subID,
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// waitingRecruitHandler GET|POST /waiting_users/recruit
//
// 原服 key = temp_search_indexes，行是**公会 search_index 文档**
// （app_conf pages.GuildWaitingListRecruit requireData: temp_search_indexes →
// /waiting_users/recruit；行渲染器 GuildSearchIndexListPartial）。
// 语义 = 「我收到的公会邀请」（页文案「没有邀请信息」）。
// 行文档复用 T1 的 guildTempDoc（同一组装逻辑，不另写第二套），
// 只把 join_approve_url 覆盖为原服形状（录包 guilds_search_index.json /
// brief §6：/guilds/join/?guild_id=<gid>）。
func waitingRecruitHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		social, ok := guildSocial(dep)
		if !ok {
			return nil, ErrGuildSocialUnsupported
		}
		ids, err := social.InvitedGuildIDs(req.UserID, GuildWaitingTTL, guildWireNow(dep))
		if err != nil {
			return nil, err
		}
		rows := []any{}
		for _, gid := range ids {
			view, verr := dep.Guild.GuildShow(gid)
			if verr != nil || view == nil || view.Guild == nil {
				continue
			}
			row := guildTempDoc(view)
			row["join_approve_url"] = "/guilds/join/?guild_id=" + gid
			rows = append(rows, row)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "waiting_users_recruit",
			"rows": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"temp_search_indexes": rows},
		})
	})
}

// socialGuildBattleRoom 解析「当前会战留言房间」= 当前会战场次 id。
//
// 原服 `guild.battle_id` 与会战场次同源（guildDocExtra.BattleID =
// gw.Session.SessionID，见 guild_wire.go）；客户端 BattleMessages.cs:72 用
// `user.battle_id` 作为房间键（`battle_comments_<battle_id>`）。
// 无会籍 / 无场次时返回 ""（调用方决定空读还是报「当前不在会战场次」）。
func socialGuildBattleRoom(dep Deps, userID string, now time.Time) (string, error) {
	gw, err := dep.Guild.GWState(userID, now)
	if err != nil {
		return "", err
	}
	if gw == nil || gw.Session == nil {
		return "", nil
	}
	return gw.Session.SessionID, nil
}

// battleCommentReadDoc 组会战留言读面 data 段（读面与写入回执共用）。
//
// 键：
//   - battle_comments：number（最新留言 Unix 秒；已读时间戳，DataCache.cs:299）
//   - battle_comments_<room>：该房间留言行（BattleMessages.cs:144 按 room 读）
//   - battle_comments_：**兼容别名**——客户端房间键取 `user.battle_id`，本工程
//     user 文档当前没有该键（战斗域未下发，见证据缺口），此时客户端会读
//     `"battle_comments_"`（空后缀）。下发别名让留言在两种情况下都能显示。
//   - user：补 battle_id=room，令客户端后续写入/再读用同一房间键。
func battleCommentReadDoc(dep Deps, userID, roomID string, latest int64,
	rows []BattleComment) (map[string]any, error) {
	data := map[string]any{"battle_comments": latest}
	if roomID != "" {
		rowsDoc := battleCommentRows(rows)
		data["battle_comments_"+roomID] = rowsDoc
		data["battle_comments_"] = rowsDoc
	}
	snap, err := dep.Accounts.Snapshot(userID)
	if err != nil {
		return nil, err
	}
	// 注意：这里不能复用 socialCopyUser（它会写 is_waiting），否则会把
	// 「等待邀请」状态误置为 false。只补 battle_id。
	user := map[string]any{}
	for k, v := range snap.User {
		user[k] = v
	}
	if roomID != "" {
		user["battle_id"] = roomID
	}
	data["user"] = user
	return data, nil
}

// battleCommentsHandler GET|POST /battles/comments
//
// 原服回包：battle_comments = **number**（最新留言 Unix 秒，客户端作已读时间戳），
// battle_comments_<battle_id> = 该场次留言行列表（动态子键，不是固定键）。
func battleCommentsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		social, ok := guildSocial(dep)
		if !ok {
			return nil, ErrGuildSocialUnsupported
		}
		now := guildWireNow(dep)
		room, err := socialGuildBattleRoom(dep, req.UserID, now)
		if err != nil {
			return nil, err
		}
		latest := int64(0)
		rows := []BattleComment{}
		if room != "" {
			latest, rows, err = social.BattleCommentFeed(room, BattleCommentListLimit)
			if err != nil {
				return nil, err
			}
		}
		data, err := battleCommentReadDoc(dep, req.UserID, room, latest, rows)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "battles_comments",
			"battle_id": room, "latest": latest, "rows": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// battleCommentCreateHandler POST /comments/create
//
// 客户端 BattleMessages.cs:49 写入路径
// `/comments/create?message_type_id=battle_comment&text=&battle_id=`。
//
// **实机缺陷修正（GC-10 2026-10-28，requests.ndjson 16091 行证据）**：客户端把
// 参数放在**加密 query（`?e=`）**里（body 只有 `_method`）；且 `battle_id` 取
// `user.battle_id`，而本工程 user 文档没有该键（战斗域未下发）→ 实际下发空串。
// 旧实现把「battle_id 为空」当成内容非法，回落「留言须为 1-110 个字符」，
// 真实原因被掩盖且不落库。现在：
//   - 参数只从 query/表单读（socialParam 两处都查），不依赖明文 query；
//   - battle_id 为空或不是本会参战场次 → **回落到当前会战场次**；
//   - 仍然没有场次才报「当前不在会战场次」；
//   - 1–110 字内容校验保持不变（BattleMessages.cs:59 事实，不放宽）。
func battleCommentCreateHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		social, ok := guildSocial(dep)
		if !ok {
			return nil, ErrGuildSocialUnsupported
		}
		if mt := socialParam(req, "message_type_id"); mt != "battle_comment" {
			return cn35wire.MarshalJSON(guildModal("功能未开放",
				"当前仅开放会战留言"))
		}
		now := guildWireNow(dep)
		current, err := socialGuildBattleRoom(dep, req.UserID, now)
		if err != nil {
			return nil, err
		}
		room := socialParam(req, "battle_id")
		if room == "" {
			room = current
		}
		if room == "" {
			return cn35wire.MarshalJSON(socialErrModal(ErrGWNoSession))
		}
		comment, werr := social.AddBattleComment(req.UserID, room,
			socialParam(req, "text"), now)
		if werr != nil && errors.Is(werr, ErrGWNoSession) &&
			current != "" && current != room {
			// 客户端带来的场次 id 不属于本会（或已过期）→ 回落当前场次。
			comment, werr = social.AddBattleComment(req.UserID, current,
				socialParam(req, "text"), now)
		}
		if werr != nil {
			return cn35wire.MarshalJSON(socialErrModal(werr))
		}
		_, rows, ferr := social.BattleCommentFeed(comment.BattleID, BattleCommentListLimit)
		if ferr != nil {
			return nil, ferr
		}
		// 回包形状**严格对齐原服** decrypted/{1,default}/comments_create*.json：
		//   {"status":"success","modal":{"title":"完成","body":"已发送"},
		//    "data":{"messages":{7 类目}}}
		// 实机（GC-10）教训，逐条对应：
		//  1) modal 只给 title/body —— 原服没有 type / current_page_close_and_back。
		//     多给的 current_page_close_and_back 会让客户端在点「确定」时
		//     CurrentPageCloseAndBack()（改变页面栈语义），不是原服行为。
		//  2) data.messages 必须存在：客户端 ResponseFilter→cache.Add("messages")
		//     → MessageParts.UpdateMessageStack() 依赖它刷新消息栈；缺这个键等于
		//     回包「只成功了一半」。类目取 AllMessageCategories + 原服多出的
		//     community_raid_call。
		//  3) **不下发** data.user 与 data.battle_comments(number)：原服都没有。
		//     battle_comments 恰好是 BattleMessages 页 requireData 的键名，在
		//     ResponseFilter 写缓存阶段会触发页面 OnRequireDocumentUpdated →
		//     提前 LoadRows()，此时房间行还没写进缓存，是页面刷新时序噪声。
		//     房间行的刷新由回调里的 UpdateDisplay(_addition:true) 读缓存完成。
		messages := map[string]any{}
		for _, cat := range AllMessageCategories() {
			messages[cat] = []any{}
		}
		messages["community_raid_call"] = []any{}
		rowsDoc := battleCommentRows(rows)
		data := map[string]any{
			"messages":                            messages,
			"battle_comments_" + comment.BattleID: rowsDoc,
			// 兼容别名：客户端 user.battle_id 可能为空（见 §3.1 说明）。
			"battle_comments_": rowsDoc,
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "comments_create",
			"battle_id": comment.BattleID, "comment_id": comment.ID,
			"rows": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"modal":  map[string]any{"title": "完成", "body": "已发送"},
			"data":   data,
		})
	})
}

// registerGuildMemberRoutes 社交/成员面路由（GC-07/GC-08）。
//
// 注册顺序契约：guild_routes.go 在通用读面**之前**调用本函数，
// /waiting_users 与 /waiting_users/recruit 由本文件拥有（guild_wire.go 的
// 旧实现被 Registry 重复注册丢弃，不生效）。
func registerGuildMemberRoutes(reg guildRegister, dep Deps) {
	// GC-07 成员原路径（原服精确路径带尾斜杠，不能当前缀用）。
	reg(http.MethodGet, "/guilds/users/", "成员列表原路径（GC-07 原服 /guilds/users/?id=）",
		guildUsersHandler(dep))

	// GC-08 等待邀请 / 邀请动作。
	reg(http.MethodGet, "/waiting_users", "待邀请候补列表（GC-08 原服 waiting_users）",
		waitingUsersHandler(dep))
	reg(http.MethodGet, "/waiting_users/add.json", "登记等待邀请（GC-08 客户端 /waiting_users/add.json）",
		waitingAddHandler(dep))
	reg(http.MethodGet, "/waiting_users/invite/", "邀请候补入会（GC-08 原服 invite_url）",
		waitingInviteHandler(dep))
	reg(http.MethodGet, "/waiting_users/invite", "邀请候补入会（无尾斜杠兜底）",
		waitingInviteHandler(dep))
	reg(http.MethodGet, "/waiting_users/recruit", "收到的公会邀请（GC-08 temp_search_indexes）",
		waitingRecruitHandler(dep))

	// GC-08 会战留言：读面 + 客户端写入路径（只开放 battle_comment 一类）。
	reg(http.MethodGet, "/battles/comments", "会战留言计数与列表（GC-08 原服 battle_comments 计数键）",
		battleCommentsHandler(dep))
	reg(http.MethodPost, "/comments/create", "会战留言写入（GC-08 客户端 /comments/create）",
		battleCommentCreateHandler(dep))
}
