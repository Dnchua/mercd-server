// tutorial_tasks.go CN3.5 新手任务读面与领取写面（empty_state 软锁修复）。
//
// 背景：/task/show_tutorial_tasks 此前走 requiredata 空态（tutorial_task_list=[]）。
// 真机实测（2026-09-30 验收）：教程完成账号进入新手指引页后导航失效，
// 只能 force-stop —— 空文档触发 ListParts 早退路径，页面交互面被禁用。
// 修复按「恢复功能」实现而非继续隐藏入口：
//
//   - 任务目录为自研内容（原服目录未取证，row schema 以 TaskListRow.cs 为准）：
//     title/desc/icon/rewards[0]{count,type_name,content_id}/status{type,text,action,msg}；
//   - 进度不依赖客户端 /task/update_task_status 推进（该端点仅回成功），
//     由 cn35store 按既有台账推导（通关/魔宠/等级/扭蛋/素材计数，单调不减）；
//   - 领取走 /task/receive_tutorial_reward?index=N，发奖+标记同事务（0019）；
//   - user.tutorial_task_doing_list + consts 侧 tutorial_task_config 联动
//     TutorialTaskIconParts 入口（全部领完后键省略、图标隐藏）。
package cn35game

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// ErrTutorialTaskClaimed 该任务奖励已领取（幂等命中，由 store 实现方返回）。
var ErrTutorialTaskClaimed = errors.New("cn35: tutorial task reward already claimed")

// TutorialTaskStore 新手任务读/写面契约（cn35store.Store 实现）。
type TutorialTaskStore interface {
	// TutorialTaskProgress 进度值（与 tutorialTaskCatalog 索引对齐）与已领取集。
	TutorialTaskProgress(userID string) (progress []int64, claimed map[int]bool, err error)
	// ClaimTutorialTaskReward 同事务发奖+标记；已领取 → cn35store.ErrTutorialTaskClaimed。
	ClaimTutorialTaskReward(userID string, taskIndex int, rewardKind, rewardKey string, rewardCount int64) error
}

// tutorialTaskDef 目录行。rewardKind：balance（发货币 currency=rewardKey）
// / item（发道具 item_key=rewardKey）。
type tutorialTaskDef struct {
	Title           string
	Desc            string
	Action          string
	Amount          int64
	Icon            string
	RewardKind      string
	RewardKey       string
	RewardTypeName  string // TaskListRow 图标 type_name（UIUtil.IconAssetName）
	RewardContentID int64
	RewardCount     int64
}

// tutorialTaskCatalog 自研新手任务目录（6 项；进度信号与
// cn35store.TutorialTaskProgress 的索引一一对应）。
var tutorialTaskCatalog = []tutorialTaskDef{
	{Title: "初次探险", Desc: "完成任意 1 次探险战斗", Action: "win_quest", Amount: 1,
		Icon: "100", RewardKind: "balance", RewardKey: "gacha_point",
		RewardTypeName: "gacha_point", RewardCount: 3000},
	{Title: "魔宠伙伴", Desc: "获得任意 1 只魔宠", Action: "has_monster", Amount: 1,
		Icon: "101", RewardKind: "balance", RewardKey: "medal",
		RewardTypeName: "medal", RewardCount: 5},
	{Title: "旅人成长", Desc: "玩家等级达到 5 级", Action: "master_level_up", Amount: 5,
		Icon: "100", RewardKind: "balance", RewardKey: "gacha_point",
		RewardTypeName: "gacha_point", RewardCount: 5000},
	{Title: "身经百战", Desc: "累计完成 5 次探险战斗", Action: "finish_quest", Amount: 5,
		Icon: "101", RewardKind: "item", RewardKey: "powerup_item_1",
		RewardTypeName: "powerup_item", RewardContentID: 1, RewardCount: 3},
	{Title: "初次扭蛋", Desc: "进行 1 次扭蛋", Action: "first_gacha", Amount: 1,
		Icon: "100", RewardKind: "balance", RewardKey: "medal",
		RewardTypeName: "medal", RewardCount: 3},
	{Title: "素材收集", Desc: "获得 1 个培养素材", Action: "op_power_up", Amount: 1,
		Icon: "101", RewardKind: "balance", RewardKey: "gacha_point",
		RewardTypeName: "gacha_point", RewardCount: 2000},
}

// tutorialTaskConfigDocs preset_data 的 data.tutorial_task_config：
// QuestTaskManager.Init 要求 doing 任务数与本配置长度一致（task_v2 分支）。
func tutorialTaskConfigDocs() []any {
	out := make([]any, 0, len(tutorialTaskCatalog))
	for _, d := range tutorialTaskCatalog {
		out = append(out, map[string]any{
			"action": d.Action, "amount": d.Amount,
			"target_id": "", "extends": map[string]any{},
		})
	}
	return out
}

// tutorialTaskListDocs 任务行（TaskListRow.cs 消费 schema）。
func tutorialTaskListDocs(progress []int64, claimed map[int]bool) []any {
	rows := make([]any, 0, len(tutorialTaskCatalog))
	for i, d := range tutorialTaskCatalog {
		var v int64
		if i < len(progress) {
			v = progress[i]
		}
		status := map[string]any{}
		switch {
		case claimed[i]:
			status = map[string]any{"type": "text", "text": "已领取"}
		case v >= d.Amount:
			// TaskListRow.OnClickConfirm：type=btn + action=click →
			// GET <msg> 后 ResponseFilter+RefreshPage。
			status = map[string]any{
				"type": "btn", "text": "领取", "action": "click",
				"msg": fmt.Sprintf("task/receive_tutorial_reward?index=%d", i),
			}
		default:
			status = map[string]any{
				"type": "text", "text": fmt.Sprintf("进度 %d/%d", v, d.Amount),
			}
		}
		rows = append(rows, map[string]any{
			"title": d.Title, "desc": d.Desc, "icon": d.Icon,
			"rewards": []any{map[string]any{
				"count": d.RewardCount, "type_name": d.RewardTypeName,
				"content_id": d.RewardContentID,
			}},
			"status": status,
		})
	}
	return rows
}

// TutorialTaskDoingListDoc user.tutorial_task_doing_list（task_v2 分节形：
// 每节 {tasks:[{status:[<value>],fetched:<bool>}]}；全部领完后省略该键
// 使 TutorialTaskIconParts 隐藏入口）。
func TutorialTaskDoingListDoc(progress []int64, claimed map[int]bool) map[string]any {
	allDone := true
	tasks := make([]any, 0, len(tutorialTaskCatalog))
	for i := range tutorialTaskCatalog {
		var v int64
		if i < len(progress) {
			v = progress[i]
		}
		fetched := claimed[i]
		if !fetched {
			allDone = false
		}
		tasks = append(tasks, map[string]any{
			"status":  []any{v},
			"fetched": fetched,
		})
	}
	if allDone {
		return nil
	}
	return map[string]any{
		"newbie": map[string]any{"tasks": tasks},
	}
}

// showTutorialTasksHandler /task/show_tutorial_tasks（GET/POST）：
// 下发真实任务列表（替代 empty_state 空数组）。
func showTutorialTasksHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		progress, claimed, err := dep.TutorialTasks.TutorialTaskProgress(req.UserID)
		if err != nil {
			return nil, err
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "task_show_tutorial_tasks",
			"tasks": len(tutorialTaskCatalog),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"tutorial_task_list": tutorialTaskListDocs(progress, claimed),
			},
		})
	})
}

// receiveTutorialRewardHandler /task/receive_tutorial_reward?index=N：
// 未达成 → status:error；已领取 → 幂等成功（重试/并发安全）；
// 达成未领 → 同事务发奖+标记，回推刷新后的列表与快照。
func receiveTutorialRewardHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		idx, err := strconv.Atoi(req.Query.Get("index"))
		if err != nil || idx < 0 || idx >= len(tutorialTaskCatalog) {
			return cn35wire.MarshalJSON(map[string]any{
				"status": "error", "msg": "无效的任务索引。"})
		}
		def := tutorialTaskCatalog[idx]
		progress, claimed, err := dep.TutorialTasks.TutorialTaskProgress(req.UserID)
		if err != nil {
			return nil, err
		}
		if !claimed[idx] {
			var v int64
			if idx < len(progress) {
				v = progress[idx]
			}
			if v < def.Amount {
				return cn35wire.MarshalJSON(map[string]any{
					"status": "error", "msg": "任务尚未达成。"})
			}
		}
		cerr := dep.TutorialTasks.ClaimTutorialTaskReward(
			req.UserID, idx, def.RewardKind, def.RewardKey, def.RewardCount)
		claimedHit := errors.Is(cerr, ErrTutorialTaskClaimed)
		if cerr != nil && !claimedHit {
			return nil, cerr
		}
		progress, claimed, err = dep.TutorialTasks.TutorialTaskProgress(req.UserID)
		if err != nil {
			return nil, err
		}
		data := map[string]any{
			"tutorial_task_list": tutorialTaskListDocs(progress, claimed),
		}
		if def.RewardKind == "item" {
			data["user_items"] = nil
			data["user_items_hash"] = nil
		}
		if snap, serr := dep.Accounts.Snapshot(req.UserID); serr == nil {
			data["user"] = snap.User
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "task_receive_tutorial_reward",
			"index": idx, "already_claimed": claimedHit,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"status":      "success",
			"modal_title": "领取成功",
			"modal_body":  fmt.Sprintf("已获得「%s」任务奖励。", def.Title),
			"data":        data,
		})
	})
}

// updateTaskStatusHandler /task/update_task_status（客户端进度回推）：
// 进度由服务端台账推导，客户端上报仅回成功，不落库。
func updateTaskStatusHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "task_update_task_status",
			"action": req.Query.Get("task_action"), "mode": "server_derived",
		}
		return cn35wire.MarshalJSON(map[string]any{"status": "success"})
	})
}
