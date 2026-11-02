// login_bonus_wire.go LOGIN350-02：不可思议之泉（时间累积）读/领接口。
//
// 端点（客户端契约见 login_bonus.go 文件头）：
//
//	GET|POST /users/show_login_bonus    页面 requireData 读面
//	                                      → data.user + data.show_login_bonus
//	GET|POST /users/receive_login_bonus 领取写面
//	                                      → data.user + data.show_login_bonus
//	                                        + data.temp_login_bonus.contents
//
// 读面只发数据：不写泉台账、不发奖、不推进可领时刻（只读 SpringState）。
// 领面由 SpringStore 单事务完成「条件推进可领时刻 + 三资源入账 + 台账」。
// 没有可领量时按客户端既有分支回**空 contents**（LoginBonus.cs:162-165
// 弹倒计时框），不显示假奖励、不报错误弹窗。
package cn35game

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// loginBonusShowLoginBonusDoc show_login_bonus 文档（LoginBonus.cs:213-224
// 逐字段消费）：progress_rate/hard_mode_progress_rate 为 0..1 比率
// （客户端 ×100 显示），gacha_point/guild_asset/battle_point 为**泉水待领量**
// （不是账号余额；LOGIN350-02 口径）。
func loginBonusShowLoginBonusDoc(dep Deps, req *cn35wire.GameRequest, snap *PlayerSnapshot,
	spring *SpringState) map[string]any {
	normal, hard := loginBonusProgressRates(dep, req.UserID)
	doc := map[string]any{
		"progress_rate":           normal,
		"hard_mode_progress_rate": hard,
		"gacha_point":             int64(0),
		"guild_asset":             int64(0),
		"battle_point":            int64(0),
	}
	if spring != nil {
		doc["gacha_point"] = spring.Gold
		doc["guild_asset"] = spring.GuildAsset
		doc["battle_point"] = spring.BattlePoint
	}
	return doc
}

// loginBonusProgressRates 剧情推进率（0..1）：normal=非 hard 区域已通关
// 关卡数/总关卡数；hard=hard 区域已通关数/总数。目录/进度不可用时 0
// （客户端仅展示，不阻断页面）。
func loginBonusProgressRates(dep Deps, userID string) (normal, hard float64) {
	if dep.Quests == nil || dep.AssetsDir == "" {
		return 0, 0
	}
	cat, err := loadQuestCatalog(dep.AssetsDir)
	if err != nil {
		return 0, 0
	}
	prog, err := dep.Quests.QuestProgress(userID)
	if err != nil {
		return 0, 0
	}
	hardAreas := map[int]bool{}
	for _, a := range cat.Areas {
		if a.IsHardMode {
			hardAreas[a.ID] = true
		}
	}
	var totalN, clearN, totalH, clearH int
	for _, q := range cat.Quests {
		diff := "normal"
		if hardAreas[q.AreaID] {
			totalH++
			diff = "hard"
			if clears := prog.Clears[q.ID]; clears != nil && clears[diff] > 0 {
				clearH++
			}
			continue
		}
		totalN++
		if clears := prog.Clears[q.ID]; clears != nil && clears[diff] > 0 {
			clearN++
		}
	}
	if totalN > 0 {
		normal = float64(clearN) / float64(totalN)
	}
	if totalH > 0 {
		hard = float64(clearH) / float64(totalH)
	}
	return normal, hard
}

// loginBonusSnapshot 读一次完整玩家快照（DataCache.Add 整键覆盖，部分字段
// 会清掉 ap/level 等；必须用 AccountStore.Snapshot 全量视图）。
func loginBonusSnapshot(dep Deps, req *cn35wire.GameRequest) (*PlayerSnapshot, error) {
	snap, err := dep.Accounts.Snapshot(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("cn35 泉水 user 快照失败: %w", err)
	}
	numericFieldsToNumber(snap.User, "leader_unit_id", "helper_unit_id")
	return snap, nil
}

// loginBonusContents rows：客户端 foreach cache["temp_login_bonus"].docs("contents")。
// type→UIUtil.IconAssetName 图标、count→掉落图标枚数（每 10 个一枚）、
// message→清单行文案、rare_type_id→清单行稀有度。三种资源各一行。
func loginBonusContents(res *SpringClaimResult) []any {
	rows := make([]any, 0, 3)
	add := func(typ, name string, count int64) {
		if count <= 0 {
			return
		}
		rows = append(rows, map[string]any{
			"type":         typ,
			"count":        count,
			"message":      name + " ×" + formatThousands(count),
			"rare_type_id": 0,
		})
	}
	add("gacha_point", "金币", res.Gold)
	add("guild_asset", "公会建设素材", res.GuildAsset)
	add("battle_point", "BP", res.BattlePoint)
	return rows
}

// formatThousands 千分位（"1000"→"1,000"）。
func formatThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	if n < 0 {
		return s
	}
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

// loginBonusShowHandler GET|POST /users/show_login_bonus：泉水读面。
// 只读：不写领取台账、不发奖、不推进可领时刻。
func loginBonusShowHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		nowSec := time.Now().Unix()
		spring, err := dep.LoginBonus.SpringState(req.UserID, nowSec)
		if err != nil {
			return nil, err
		}
		snap, err := loginBonusSnapshot(dep, req)
		if err != nil {
			return nil, err
		}
		user := snap.User
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "show_login_bonus",
			"receivable_sec": user["login_bonus_receivable_sec"],
			"pending_gold":   spring.Gold, "pending_guild": spring.GuildAsset,
			"pending_bp": spring.BattlePoint,
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"user":             user,
				"show_login_bonus": loginBonusShowLoginBonusDoc(dep, req, snap, spring),
			},
		})
	})
}

// loginBonusReceiveHandler GET|POST /users/receive_login_bonus：领取写面。
//
// 成功：单事务（CAS 推进可领时刻 + 三资源入账 + 台账）→ 回推 contents；
// 没有可领量：不写任何行，回空 contents（客户端弹倒计时）。
// 响应始终回推完整 user，保证红点/余额/可领时刻即时刷新。
func loginBonusReceiveHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		nowSec := time.Now().Unix()
		res, err := dep.LoginBonus.ClaimSpring(req.UserID, nowSec)
		switch {
		case err == nil:
		case errors.Is(err, ErrLoginBonusNotReady):
			// 累积不足 1 小时/并发重放：客户端空态分支（倒计时弹窗），不写入。
			res = &SpringClaimResult{}
		default:
			return nil, fmt.Errorf("cn35 泉水领取失败: %w", err)
		}
		snap, err := loginBonusSnapshot(dep, req)
		if err != nil {
			return nil, err
		}
		user := snap.User
		contents := []any{}
		if res.Granted {
			contents = loginBonusContents(res)
		}
		spring, serr := dep.LoginBonus.SpringState(req.UserID, nowSec)
		if serr != nil {
			return nil, serr
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "receive_login_bonus",
			"granted": res.Granted, "gold": res.Gold,
			"guild": res.GuildAsset, "bp": res.BattlePoint,
			"receivable_sec": user["login_bonus_receivable_sec"],
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"user":             user,
				"show_login_bonus": loginBonusShowLoginBonusDoc(dep, req, snap, spring),
				"temp_login_bonus": map[string]any{"contents": contents},
			},
		})
	})
}
