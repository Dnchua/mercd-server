// friends.go FRIEND350-01：M3 好友系统与友情徽章礼物盒领域定义。
//
// 协议与客户端证据（mlk-capture-201711 + Assembly-CSharp.dll 反编译）：
//   - AddFriendInfos（/friends/index）：
//     data.{friend_count_limit, friend_count, friend_medal_count, medal_package_count, send_all_friend_preset_open, friends}
//     FriendButtonListComponent 读 send_all_friend_preset_open 控制「一键赠送」按钮显隐，
//     读 medal_package_count > 0 控制「领取徽章」红点角标。
//   - AcceptFriend（/friends/friend_apply_list）：
//     data.{friends_apply, friend_count, friend_count_limit}
//   - ApplyFriend -> ApplyFriendInfos（/friends/search_friend?user_id=&name=）：
//     data.{users}
//   - 写面：
//   - /friends/apply_friend（及 /friends/apply_friend_2） -> {result, msg, data?}
//   - /friends/add_friend?user_id= -> {result, msg, data?}（满员返回 result:false, msg:"好友已满，无法添加"）
//   - /friends/delete_friend?user_ids[]= -> {result:true, data:{friend_count, friends}}
//   - /friends/clear_apply_list -> {result:true, msg, data:{friends_apply:[]}}
//   - /friends/give_friend_medal?user_id= -> {result:true, modal:{title:"成功", body:...}, data:{...}}
//   - /friends/give_all_friend_medal[?confirm=true] ->
//     当 cp + 4*N > max_cp 且 !confirm 时返回 yes_no 确认弹窗（url="/friends/give_all_friend_medal?_method=GET&confirm=true"）；
//     确认后返回 modal:{title:"成功", body:"赠送成功N个友情徽章，获得M点CP。"} + data.{user, friend_count_limit, friend_count, friend_medal_count, medal_package_count, friends}
//   - /friends/show_friend_medal_list -> data.{friend_medal_gifts}
//   - /friends/acquire_friend_medals[?uuid=] -> {result:true, msg, data:{user, friend_medal_count, medal_package_count, friend_medal_gifts}}
package cn35game

import "errors"

// 好友域业务错误定义。
var (
	// ErrFriendLimitReached 好友人数已达上限（30 人）。
	ErrFriendLimitReached = errors.New("cn35game: 好友已满，无法添加")
	// ErrFriendAlreadyExists 双方已经是好友。
	ErrFriendAlreadyExists = errors.New("cn35game: 对方已经是你的好友")
	// ErrFriendSelfOperation 不能对自己发起好友操作。
	ErrFriendSelfOperation = errors.New("cn35game: 不能添加自己为好友")
	// ErrFriendTargetNotFound 目标玩家不存在。
	ErrFriendTargetNotFound = errors.New("cn35game: 未找到该玩家")
	// ErrFriendApplyNotFound 对方尚未向当前玩家发起好友申请，无法直接批准。
	ErrFriendApplyNotFound = errors.New("cn35game: 未找到该好友申请")
	// ErrFriendGiveCPOverflow 一键赠送时 CP 将超过上限且未带 confirm=true。
	ErrFriendGiveCPOverflow = errors.New("cn35game: CP将超过上限，需二次确认")
	// ErrFriendNothingToGive 当前没有可赠送徽章的好友。
	ErrFriendNothingToGive = errors.New("cn35game: 今日已向全部好友赠送过友情徽章")
	// ErrFriendMedalGiftEmpty 当前没有可领取的友情徽章礼物。
	ErrFriendMedalGiftEmpty = errors.New("cn35game: 没有可领取的友情徽章")
)

// FriendRules 好友系统常量（对齐 .mlk 抓包真值）。
var FriendRules = struct {
	FriendCountLimit int
	CPPerGive        int64
	MaxCP            int64
}{
	FriendCountLimit: 30,
	CPPerGive:        4,
	MaxCP:            120,
}

// FriendSummary 好友/申请/搜索行对象（JSON 键严格对齐 FriendInfoListRow.cs 与 .mlk）。
type FriendSummary struct {
	UserID       string
	Name         string
	UnitID       int
	Level        int
	GuildName    string
	LoggedAt     int64
	CanGiveMedal bool
}

// FriendMedalGift 友情徽章礼物盒条目（对齐 FriendPresentInfoListRow.cs 与 .mlk）。
type FriendMedalGift struct {
	UUID         string
	CreatedAtSec int64
	SenderUserID string
	SenderName   string
	SenderUnitID int
	Count        int
}

// FriendIndexState /friends/index 所需聚合状态。
type FriendIndexState struct {
	FriendCountLimit int
	FriendCount      int
	FriendMedalCount int64
	MedalPackageCnt  int
	Friends          []FriendSummary
}

// FriendGiveResult 赠送友情徽章结果。
type FriendGiveResult struct {
	GivenCount int
	GainedCP   int64
	Index      *FriendIndexState
}

// FriendAcquireResult 领取友情徽章礼物结果。
type FriendAcquireResult struct {
	AcquiredCount    int64
	FriendMedalCount int64
	MedalPackageCnt  int
	RemainingGifts   []FriendMedalGift
}

// SeedNPCFriend 单机环境内置 NPC 冒险者定义（支持搜索、互加好友与每日回赠友情徽章）。
type SeedNPCFriend struct {
	UserID    string
	Name      string
	UnitID    int
	Level     int
	GuildName string
}

// DefaultNPCFriends 内置 8 名单机活跃 NPC 冒险者，确保单人离线服也能完整体验搜索/加友/赠礼/收礼闭环。
var DefaultNPCFriends = []SeedNPCFriend{
	{UserID: "npc_frd_0001", Name: "艾琳的学徒", UnitID: 348, Level: 65, GuildName: "星盘观测所"},
	{UserID: "npc_frd_0002", Name: "微风旅人", UnitID: 375, Level: 78, GuildName: "妖精之森"},
	{UserID: "npc_frd_0003", Name: "晨曦骑士", UnitID: 152, Level: 92, GuildName: "王国近卫团"},
	{UserID: "npc_frd_0004", Name: "星光占卜师", UnitID: 491, Level: 84, GuildName: "星盘观测所"},
	{UserID: "npc_frd_0005", Name: "苍蓝剑士", UnitID: 620, Level: 105, GuildName: "苍穹旅团"},
	{UserID: "npc_frd_0006", Name: "绯红魔导", UnitID: 1207, Level: 88, GuildName: "红莲学派"},
	{UserID: "npc_frd_0007", Name: "白羽祭司", UnitID: 1249, Level: 96, GuildName: "圣域教会"},
	{UserID: "npc_frd_0008", Name: "茶会甜点师", UnitID: 364, Level: 72, GuildName: "甜点同盟"},
}

// LookupNPCFriend 按 userID 查找内置 NPC 好友。
func LookupNPCFriend(userID string) (SeedNPCFriend, bool) {
	for _, n := range DefaultNPCFriends {
		if n.UserID == userID {
			return n, true
		}
	}
	return SeedNPCFriend{}, false
}

// FriendStore 好友系统持久化接口（由 cn35store.Store 实现）。
type FriendStore interface {
	// FriendIndex 读取玩家好友列表与徽章/礼盒计数。
	FriendIndex(userID, periodKey string) (*FriendIndexState, error)
	// FriendApplyList 读取玩家收到的待处理好友申请列表。
	FriendApplyList(userID string) ([]FriendSummary, int, error)
	// SearchFriends 按 user_id 或 name 搜索可添加玩家（含真实账号与内置 NPC）。
	SearchFriends(userID, queryUserID, queryName string) ([]FriendSummary, error)
	// ApplyFriend 发送好友申请（若目标为内置 NPC 好友，同时在待批列表或直接可批产生互动）。
	ApplyFriend(userID, targetUserID string) error
	// AddFriend 批准/添加好友（双向建边，校验双方好友数 < 30）。
	AddFriend(userID, applicantUserID, periodKey string) (*FriendIndexState, error)
	// DeleteFriends 批量解除好友关系。
	DeleteFriends(userID string, targetUserIDs []string, periodKey string) (*FriendIndexState, error)
	// ClearFriendApplyList 清空收到的好友申请列表。
	ClearFriendApplyList(userID string) error
	// GiveFriendMedal 向单个好友赠送友情徽章（单日每好友限 1 次，发起方 +4 CP，接收方收 1 礼盒；若对方为 NPC 则自动回赠 1 礼盒）。
	GiveFriendMedal(userID, friendUserID, periodKey string) (*FriendGiveResult, error)
	// GiveAllFriendMedals 一键向全部可赠好友赠送友情徽章（当 cp + 4*N > max_cp 且 !confirm 时返回 ErrFriendGiveCPOverflow）。
	GiveAllFriendMedals(userID, periodKey string, confirm bool) (*FriendGiveResult, error)
	// FriendMedalGifts 读取待领取的友情徽章礼物盒列表。
	FriendMedalGifts(userID string) ([]FriendMedalGift, error)
	// AcquireFriendMedals 领取友情徽章礼物（uuid 为空表示全部领取，非空表示单条领取）。
	AcquireFriendMedals(userID, uuid string) (*FriendAcquireResult, error)
}

// FriendSummaryWire 序列化好友/申请/搜索行对象（includeCanGive=true 用于 /friends/index，false 用于申请/搜索列表）。
func FriendSummaryWire(f FriendSummary, includeCanGive bool) map[string]any {
	guildName := f.GuildName
	if guildName == "" {
		guildName = "无所属"
	}
	m := map[string]any{
		"user_id":    f.UserID,
		"name":       f.Name,
		"unit_id":    f.UnitID,
		"level":      f.Level,
		"guild_name": guildName,
		"logged_at":  f.LoggedAt,
	}
	if includeCanGive {
		m["can_give_medal"] = f.CanGiveMedal
	}
	return m
}

// FriendIndexWire 构造 /friends/index 及写面回推的 data 字段集（严格对齐 .mlk）。
func FriendIndexWire(st *FriendIndexState) map[string]any {
	rows := make([]any, 0, len(st.Friends))
	for _, f := range st.Friends {
		rows = append(rows, FriendSummaryWire(f, true))
	}
	return map[string]any{
		"friend_count_limit":          st.FriendCountLimit,
		"friend_count":                st.FriendCount,
		"friend_medal_count":          st.FriendMedalCount,
		"medal_package_count":         st.MedalPackageCnt,
		"send_all_friend_preset_open": true,
		"friends":                     rows,
	}
}

// FriendMedalGiftWire 构造 /friends/show_friend_medal_list 中的单条礼物文档（严格对齐 .mlk）。
func FriendMedalGiftWire(g FriendMedalGift) map[string]any {
	cnt := g.Count
	if cnt <= 0 {
		cnt = 1
	}
	return map[string]any{
		"uuid":           g.UUID,
		"created_at_sec": g.CreatedAtSec,
		"content": map[string]any{
			"user_id":       g.SenderUserID,
			"name":          g.SenderName,
			"unit_id":       g.SenderUnitID,
			"content_count": cnt,
		},
		"message":           "赠送你1个友情徽章。",
		"content_type_name": "friendmedal",
	}
}
