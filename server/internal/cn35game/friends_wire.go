// friends_wire.go FRIEND350-02：M3 好友系统与友情徽章礼物盒 HTTP 协议处理层。
//
// 端点：
//   - GET|POST /friends/index                   好友列表主页（AddFriendInfos）
//   - GET|POST /friends/friend_apply_list       待批准申请列表（AcceptFriend）
//   - GET|POST /friends/search_friend           按 ID / 昵称搜索玩家（ApplyFriend -> ApplyFriendInfos）
//   - GET|POST /friends/apply_friend            发送好友申请（含别名 /friends/apply_friend_2、/friends/apply）
//   - GET|POST /friends/add_friend              批准添加好友
//   - GET|POST /friends/delete_friend           批量解除好友（?user_ids[]=...）
//   - GET|POST /friends/clear_apply_list        清空待批准申请列表
//   - GET|POST /friends/give_friend_medal       向单个好友赠送友情徽章（?user_id=）
//   - GET|POST /friends/give_all_friend_medal   一键赠送全部好友友情徽章（含 CP 溢出二次确认）
//   - GET|POST /friends/show_friend_medal_list  友情徽章礼物盒列表（AcceptMedalPresent）
//   - GET|POST /friends/acquire_friend_medals   单领（?uuid=）或一键全领友情徽章礼物
package cn35game

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func collectFriendUserIDs(req *cn35wire.GameRequest) []string {
	var raw []string
	for _, k := range []string{"user_ids[]", "user_ids", "user_id", "ids", "id"} {
		if vs := req.Query[k]; len(vs) > 0 {
			raw = append(raw, vs...)
		}
		if vs := req.Form[k]; len(vs) > 0 {
			raw = append(raw, vs...)
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		for _, part := range strings.Split(v, ",") {
			u := strings.TrimSpace(part)
			if u != "" && !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}

func friendUserSnapshot(dep Deps, userID string) (map[string]any, error) {
	snap, err := dep.Accounts.Snapshot(userID)
	if err != nil {
		return nil, err
	}
	numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
	return snap.User, nil
}

// friendsIndexHandler GET|POST /friends/index：好友列表读面。
func friendsIndexHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		periodKey := FatePeriodKey(fateNow(dep))
		idx, err := dep.Friends.FriendIndex(req.UserID, periodKey)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_index",
			"friend_count": idx.FriendCount, "medal_package_count": idx.MedalPackageCnt,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": FriendIndexWire(idx),
		})
	})
}

// friendsApplyListHandler GET|POST /friends/friend_apply_list：待批准申请列表读面。
func friendsApplyListHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		applies, friendCnt, err := dep.Friends.FriendApplyList(req.UserID)
		if err != nil {
			return nil, err
		}
		rows := make([]any, 0, len(applies))
		for _, a := range applies {
			rows = append(rows, FriendSummaryWire(a, false))
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_friend_apply_list",
			"apply_count": len(rows), "friend_count": friendCnt,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"friends_apply":      rows,
				"friend_count":       friendCnt,
				"friend_count_limit": FriendRules.FriendCountLimit,
			},
		})
	})
}

// friendsSearchHandler GET|POST /friends/search_friend?user_id=<uid>&name=<name>：搜索好友。
func friendsSearchHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		qUID := strings.TrimSpace(firstNonEmpty(req.Query.Get("user_id"), req.Form.Get("user_id")))
		qName := strings.TrimSpace(firstNonEmpty(req.Query.Get("name"), req.Form.Get("name")))
		users, err := dep.Friends.SearchFriends(req.UserID, qUID, qName)
		if err != nil {
			return nil, err
		}
		rows := make([]any, 0, len(users))
		for _, u := range users {
			rows = append(rows, FriendSummaryWire(u, false))
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_search_friend",
			"query_uid": qUID, "query_name": qName, "found": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"users": rows,
			},
		})
	})
}

// friendsApplyHandler GET|POST /friends/apply_friend（及 /friends/apply_friend_2、/friends/apply）：发送好友申请。
func friendsApplyHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		targetUID := strings.TrimSpace(firstNonEmpty(
			req.Query.Get("user_id"), req.Form.Get("user_id"),
			req.Query.Get("id"), req.Form.Get("id"),
		))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_apply_friend",
			"target_uid": targetUID,
		}
		err := dep.Friends.ApplyFriend(req.UserID, targetUID)
		if err != nil {
			switch {
			case errors.Is(err, ErrFriendLimitReached):
				return cn35wire.MarshalJSON(map[string]any{"result": false, "msg": "好友已满，无法添加"})
			case errors.Is(err, ErrFriendAlreadyExists):
				return cn35wire.MarshalJSON(map[string]any{"result": false, "msg": "对方已经是你的好友。"})
			case errors.Is(err, ErrFriendSelfOperation):
				return cn35wire.MarshalJSON(map[string]any{"result": false, "msg": "不能向自己发送好友申请。"})
			case errors.Is(err, ErrFriendTargetNotFound), errors.Is(err, ErrInvalidRequest):
				return cn35wire.MarshalJSON(map[string]any{"result": false, "msg": "未找到该玩家。"})
			}
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{
			"result": true,
			"msg":    "已发送好友申请。",
		})
	})
}

// friendsAddHandler GET|POST /friends/add_friend?user_id=<uid>：批准好友申请。
func friendsAddHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		applicantUID := strings.TrimSpace(firstNonEmpty(
			req.Query.Get("user_id"), req.Form.Get("user_id"),
			req.Query.Get("id"), req.Form.Get("id"),
		))
		periodKey := FatePeriodKey(fateNow(dep))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_add_friend",
			"applicant_uid": applicantUID,
		}
		idx, err := dep.Friends.AddFriend(req.UserID, applicantUID, periodKey)
		if err != nil {
			switch {
			case errors.Is(err, ErrFriendLimitReached):
				return cn35wire.MarshalJSON(map[string]any{"result": false, "msg": "好友已满，无法添加"})
			case errors.Is(err, ErrFriendAlreadyExists):
				return cn35wire.MarshalJSON(map[string]any{"result": false, "msg": "对方已经是你的好友。"})
			case errors.Is(err, ErrFriendSelfOperation):
				return cn35wire.MarshalJSON(map[string]any{"result": false, "msg": "不能添加自己为好友。"})
			case errors.Is(err, ErrFriendApplyNotFound):
				return cn35wire.MarshalJSON(map[string]any{"result": false, "msg": "未找到该好友申请。"})
			case errors.Is(err, ErrFriendTargetNotFound), errors.Is(err, ErrInvalidRequest):
				return cn35wire.MarshalJSON(map[string]any{"result": false, "msg": "未找到该玩家。"})
			}
			return nil, err
		}
		applies, _, aerr := dep.Friends.FriendApplyList(req.UserID)
		if aerr != nil {
			return nil, aerr
		}
		applyRows := make([]any, 0, len(applies))
		for _, a := range applies {
			applyRows = append(applyRows, FriendSummaryWire(a, false))
		}
		data := FriendIndexWire(idx)
		data["friends_apply"] = applyRows
		return cn35wire.MarshalJSON(map[string]any{
			"result": true,
			"msg":    "已添加为好友。",
			"data":   data,
		})
	})
}

// friendsDeleteHandler GET|POST /friends/delete_friend?user_ids[]=<uid>...：批量解除好友。
func friendsDeleteHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		targets := collectFriendUserIDs(req)
		periodKey := FatePeriodKey(fateNow(dep))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_delete_friend",
			"targets": targets,
		}
		idx, err := dep.Friends.DeleteFriends(req.UserID, targets, periodKey)
		if err != nil {
			return nil, err
		}
		friendRows := make([]any, 0, len(idx.Friends))
		for _, f := range idx.Friends {
			friendRows = append(friendRows, FriendSummaryWire(f, true))
		}
		return cn35wire.MarshalJSON(map[string]any{
			"result": true,
			"data": map[string]any{
				"friend_count": idx.FriendCount,
				"friends":      friendRows,
			},
		})
	})
}

// friendsClearApplyListHandler GET|POST /friends/clear_apply_list：清空好友申请列表。
func friendsClearApplyListHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_clear_apply_list",
		}
		if err := dep.Friends.ClearFriendApplyList(req.UserID); err != nil {
			return nil, err
		}
		return cn35wire.MarshalJSON(map[string]any{
			"result": true,
			"msg":    "已清空好友申请列表。",
			"data": map[string]any{
				"friends_apply": []any{},
			},
		})
	})
}

// friendsGiveMedalHandler GET|POST /friends/give_friend_medal?user_id=<uid>：单人赠送友情徽章。
func friendsGiveMedalHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		friendUID := strings.TrimSpace(firstNonEmpty(
			req.Query.Get("user_id"), req.Form.Get("user_id"),
			req.Query.Get("id"), req.Form.Get("id"),
		))
		periodKey := FatePeriodKey(fateNow(dep))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_give_friend_medal",
			"friend_uid": friendUID, "period": periodKey,
		}
		res, err := dep.Friends.GiveFriendMedal(req.UserID, friendUID, periodKey)
		if err != nil {
			switch {
			case errors.Is(err, ErrFriendNothingToGive):
				return cn35wire.MarshalJSON(map[string]any{
					"result": false,
					"modal": map[string]any{
						"title": "提示",
						"body":  "今日已向该好友赠送过友情徽章。",
					},
				})
			case errors.Is(err, ErrFriendTargetNotFound), errors.Is(err, ErrInvalidRequest):
				return cn35wire.MarshalJSON(map[string]any{
					"result": false,
					"modal": map[string]any{
						"title": "提示",
						"body":  "未找到该好友。",
					},
				})
			}
			return nil, err
		}
		userDoc, err := friendUserSnapshot(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		data := FriendIndexWire(res.Index)
		data["user"] = userDoc
		return cn35wire.MarshalJSON(map[string]any{
			"result": true,
			"modal": map[string]any{
				"title": "成功",
				"body":  fmt.Sprintf("赠送成功%d个友情徽章，获得%d点CP。", res.GivenCount, res.GainedCP),
			},
			"data": data,
		})
	})
}

// friendsGiveAllMedalsHandler GET|POST /friends/give_all_friend_medal[?confirm=true]：一键赠送全部好友友情徽章。
func friendsGiveAllMedalsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		confirmStr := strings.TrimSpace(firstNonEmpty(req.Query.Get("confirm"), req.Form.Get("confirm")))
		confirm := strings.EqualFold(confirmStr, "true") || confirmStr == "1"
		periodKey := FatePeriodKey(fateNow(dep))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_give_all_friend_medal",
			"confirm": confirm, "period": periodKey,
		}
		res, err := dep.Friends.GiveAllFriendMedals(req.UserID, periodKey, confirm)
		if err != nil {
			switch {
			case errors.Is(err, ErrFriendGiveCPOverflow):
				userDoc, uerr := friendUserSnapshot(dep, req.UserID)
				if uerr != nil {
					return nil, uerr
				}
				return cn35wire.MarshalJSON(map[string]any{
					"modal": map[string]any{
						"title": "确认",
						"type":  "yes_no",
						"body":  "CP将超过上限，超过部分无法获得，是否确定赠送全部好友友情徽章",
						"url":   "/friends/give_all_friend_medal?_method=GET&confirm=true",
					},
					"data": map[string]any{
						"user":      userDoc,
						"user_memo": 0,
					},
				})
			case errors.Is(err, ErrFriendNothingToGive):
				idx, ierr := dep.Friends.FriendIndex(req.UserID, periodKey)
				if ierr != nil {
					return nil, ierr
				}
				return cn35wire.MarshalJSON(map[string]any{
					"modal": map[string]any{
						"title": "提示",
						"body":  "当前没有可赠送友情徽章的好友。",
					},
					"data": FriendIndexWire(idx),
				})
			}
			return nil, err
		}
		userDoc, err := friendUserSnapshot(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		data := FriendIndexWire(res.Index)
		data["user"] = userDoc
		return cn35wire.MarshalJSON(map[string]any{
			"modal": map[string]any{
				"title": "成功",
				"body":  fmt.Sprintf("赠送成功%d个友情徽章，获得%d点CP。", res.GivenCount, res.GainedCP),
			},
			"data": data,
		})
	})
}

// friendsShowMedalListHandler GET|POST /friends/show_friend_medal_list：友情徽章礼物盒读面。
func friendsShowMedalListHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		gifts, err := dep.Friends.FriendMedalGifts(req.UserID)
		if err != nil {
			return nil, err
		}
		rows := make([]any, 0, len(gifts))
		for _, g := range gifts {
			rows = append(rows, FriendMedalGiftWire(g))
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_show_friend_medal_list",
			"gifts": len(rows),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"friend_medal_gifts": rows,
			},
		})
	})
}

// friendsAcquireMedalsHandler GET|POST /friends/acquire_friend_medals[?uuid=]：领取友情徽章礼物。
func friendsAcquireMedalsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuid := strings.TrimSpace(firstNonEmpty(req.Query.Get("uuid"), req.Form.Get("uuid")))
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "friends_acquire_friend_medals",
			"uuid": uuid,
		}
		res, err := dep.Friends.AcquireFriendMedals(req.UserID, uuid)
		if err != nil {
			if errors.Is(err, ErrFriendMedalGiftEmpty) {
				return cn35wire.MarshalJSON(map[string]any{
					"result": false,
					"msg":    "没有可领取的友情徽章。",
				})
			}
			return nil, err
		}
		userDoc, err := friendUserSnapshot(dep, req.UserID)
		if err != nil {
			return nil, err
		}
		rows := make([]any, 0, len(res.RemainingGifts))
		for _, g := range res.RemainingGifts {
			rows = append(rows, FriendMedalGiftWire(g))
		}
		return cn35wire.MarshalJSON(map[string]any{
			"result": true,
			"msg":    fmt.Sprintf("成功领取%d个友情徽章。", res.AcquiredCount),
			"data": map[string]any{
				"user":                userDoc,
				"friend_medal_count":  res.FriendMedalCount,
				"medal_package_count": res.MedalPackageCnt,
				"friend_medal_gifts":  rows,
			},
		})
	})
}
