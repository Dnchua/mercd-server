// present_receive_wire.go P0 写面（2026-09-28 Note）：/user_presents/receive/
// 单领与全领协议端点。
//
// 客户端契约（ilspy-cs 取证）：
//
//	单领 /user_presents/receive/?uuid=<uuid>        （UserPresentsListRow.cs:86）
//	全领 /user_presents/receive/                   （UserPresentReceiveAllImageButton.cs:24）
//	全领变体 ?without_unit=true                     （同 :27，排除同伴礼物）
//
// 回调消费（UserPresentsListRow.cs:90-192 / ReceiveAllImageButton.cs:29-120）：
//
//	ResponseFilter(_json) → 无 modal 继续；
//	document.docs("rewards") 顶层数组 → ShowContentsDialog(modal_title,
//	modal_body, rewards)；每条读 type/id/count（埋点）与
//	message/rare_type_id（渲染，Modal.cs:685-705）；icon_asset_name
//	不下发——Modal.cs:694-698 为空时客户端按 type/id 经
//	UIUtil.IconAssetName 自行推导，服务端拼值无证据且会覆盖正确默认。
//	cache.Get("user_presents") 判空关页 → data.user_presents 必须回推。
//
// 响应：{status:"success", rewards:[...], modal_title, modal_body,
// data:{user_presents,user_units,user_monsters,user,user_coin}}。
// rewards 顶层下发（客户端从响应根读，不在 data 内）。
package cn35game

import (
	"errors"
	"fmt"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func presentReceiveHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		uuid := firstNonEmpty(req.Query.Get("uuid"), req.Form.Get("uuid"))
		withoutUnit := firstNonEmpty(req.Query.Get("without_unit"), req.Form.Get("without_unit")) == "true"
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_presents_receive",
			"uuid": uuid, "without_unit": withoutUnit,
		}
		if dep.PresentReceive == nil || dep.Inventory == nil || dep.Catalog == nil {
			return cn35wire.MarshalJSON(trainingError(
				"功能未启用", "服务端未装配礼物领取写面。"))
		}
		now := time.Now().Unix()
		var res *PresentReceiveResult
		var rerr error
		if uuid != "" {
			res, rerr = dep.PresentReceive.ReceivePresent(req.UserID, uuid, now, dep.Catalog)
		} else {
			res, rerr = dep.PresentReceive.ReceiveAllPresents(req.UserID, withoutUnit, now, dep.Catalog)
		}
		if rerr != nil {
			switch {
			case errors.Is(rerr, ErrPresentNotFound):
				return cn35wire.MarshalJSON(trainingError(
					"无法领取", "该礼物不存在或已被领取。"))
			case errors.Is(rerr, ErrPresentAlreadyReceived):
				return cn35wire.MarshalJSON(trainingError(
					"无法领取", "该礼物已被领取。"))
			case errors.Is(rerr, ErrPresentExpired):
				return cn35wire.MarshalJSON(trainingError(
					"无法领取", "该礼物已过领取期限。"))
			case errors.Is(rerr, ErrPresentUnsupported):
				return cn35wire.MarshalJSON(trainingError(
					"无法领取", "该类型礼物暂未开放领取。"))
			}
			return nil, rerr
		}

		// 回推读面：剩余未领列表（客户端判空关页）+ 库存 + 快照。
		data, err := writeBackData(dep, c, req.UserID)
		if err != nil {
			return nil, err
		}
		remaining, err := loadUserPresents(dep, req.UserID, now)
		if err != nil {
			return nil, err
		}
		data["user_presents"] = presentListDocs(dep, remaining)
		data["messages"] = presentReceiveMessagesDoc(now)

		rewards := make([]any, 0, len(res.Granted))
		for _, g := range res.Granted {
			rewards = append(rewards, presentRewardDoc(dep, g))
		}
		c.Decoded["granted"] = len(rewards)
		return cn35wire.MarshalJSON(map[string]any{
			"status":      "success",
			"rewards":     rewards,
			"modal_title": "获得礼物",
			"modal_body":  "已领取以下礼物。",
			"data":        data,
		})
	})
}

// presentListDocs 待领列表的 wire 形状（与 requiredata.go /user_presents/ 一致）。
// 客户端在领取回调后用 data.user_presents 判空关页，形状必须同源。
func presentListDocs(dep Deps, records []PresentRecord) []any {
	items := make([]any, 0, len(records))
	for _, r := range records {
		items = append(items, presentRowDoc(dep, r))
	}
	return items
}

func presentReceiveMessagesDoc(now int64) map[string]any {
	createdAt := now - 5
	return map[string]any{
		"battle_activity": []any{
			map[string]any{
				"addition": "", "created_at": createdAt, "id": "ba-1",
				"text": "祈祷了！", "type_id": "battle_activity", "unit_id": int64(1), "user_id": "npc-1",
			},
		},
		"community_comment": []any{
			map[string]any{
				"created_at": createdAt, "help_id": "", "helped_at": int64(0), "id": "cc-1",
				"stamp": "", "text": "大家好！", "type_id": "community_comment",
				"unit_id": int64(1), "user_id": "npc-1", "user_name": "旅人",
			},
		},
		"community_help": []any{
			map[string]any{
				"created_at": createdAt, "help_id": "", "helped_at": int64(0), "id": "ch-1",
				"text": "请求救援！", "type_id": "community_help",
				"unit_id": int64(1), "user_id": "npc-1", "user_name": "旅人",
			},
		},
		"community_raid_call": []any{},
		"guild_comment": []any{
			map[string]any{
				"created_at": createdAt, "help_id": "", "helped_at": int64(0), "id": "gc-1",
				"stamp": "", "text": "请多多关照！", "type_id": "guild_comment",
				"unit_id": int64(1), "user_id": "npc-1", "user_name": "旅人",
			},
		},
		"guild_help": []any{
			map[string]any{
				"created_at": createdAt, "help_id": "", "helped_at": int64(0), "id": "gh-1",
				"text": "请求救援！", "type_id": "guild_help",
				"unit_id": int64(1), "user_id": "npc-1", "user_name": "旅人",
			},
		},
		"user_comment": []any{
			map[string]any{
				"created_at": createdAt, "id": "uc-1", "private": false,
				"text": "欢迎来到梅露可物语！", "type_id": "user_comment", "user_name": "梅露可",
			},
		},
	}
}

// presentRowDoc 单行礼物文档：行级字段 + content 嵌套文档。
// 客户端列表行实际消费字段（UserPresentsListRow.cs:39-72）：
//   - document.doc("content").s("name"|"main_name") → 名称；
//   - content.i("contents_count") → “×N” 计数；
//   - document.s("message") 或 content.s("description") → 说明；
//   - document.i("created_at_sec") → 时间标签；
//   - content.s("icon_asset_name") → document.s("icon_asset_name")
//     → UIUtil.IconAssetName(document.s("content_type_name"), content.i("id"))
//     三级回退推导图标。故行级须带 content_type_name、created_at_sec，
//     content 须带 id 与 contents_count。
//
// name 查不到时留空，客户端走 main_name/图标回退，不下发无证据字段。
func presentRowDoc(dep Deps, r PresentRecord) map[string]any {
	content := map[string]any{
		"id":             r.ContentID,
		"contents_count": r.Amount,
	}
	if name := presentDisplayName(dep, r.ContentType, r.ContentID); name != "" {
		content["name"] = name
	}
	return map[string]any{
		"uuid":              r.UUID,
		"type_name":         r.ContentType,
		"content_type":      r.ContentType,
		"content_type_name": r.ContentType,
		"content_id":        r.ContentID,
		"amount":            r.Amount,
		"expire_at":         r.ExpireAt,
		"created_at":        r.CreatedAt,
		"created_at_sec":    r.CreatedAt,
		"content":           content,
	}
}

// presentDisplayName 供列表行 content.name 使用；货币类无主目录，
// 名称与 presentRewardDoc 的 message 口径一致。
func presentDisplayName(dep Deps, contentType string, contentID int64) string {
	switch contentType {
	case "coin":
		return "钻石"
	case "gacha_point":
		return "金币"
	case "medal":
		return "徽章"
	case "unit":
		if dep.Catalog == nil {
			return ""
		}
		if u, err := dep.Catalog.Unit(int(contentID)); err == nil {
			if u.Title != "" {
				return u.Title + u.Name
			}
			return u.Name
		}
	case "monster":
		if dep.Catalog == nil {
			return ""
		}
		if m, err := dep.Catalog.Monster(int(contentID)); err == nil {
			return m.Name
		}
	case "item":
		if dep.Items != nil {
			if defs, err := dep.Items.ItemCatalog(); err == nil {
				for _, def := range defs {
					if def.ContentID == int(contentID) {
						return def.Name
					}
				}
			}
		}
	}
	return ""
}

// presentRewardDoc 组装一条 rewards 行：type/id/count 为客户端埋点必需，
// message/rare_type_id 为 ShowContentsDialog 渲染消费。icon_asset_name
// 不下发：Modal.cs:694-698 空值时客户端以 type/id 经 UIUtil.IconAssetName
// 推导——服务端拼的 "unit/unit_%d" 无证据且会覆盖正确默认。
func presentRewardDoc(dep Deps, g PresentGrant) map[string]any {
	doc := map[string]any{
		"type":  g.ContentType,
		"id":    g.ContentID,
		"count": g.Amount,
	}
	switch g.ContentType {
	case "unit":
		if u, err := dep.Catalog.Unit(int(g.ContentID)); err == nil {
			name := u.Name
			if u.Title != "" {
				name = u.Title + u.Name
			}
			doc["message"] = fmt.Sprintf("%s ×%d", name, g.Amount)
			doc["rare_type_id"] = u.Rare * 10
		}
	case "monster":
		if m, err := dep.Catalog.Monster(int(g.ContentID)); err == nil {
			doc["message"] = fmt.Sprintf("%s ×%d", m.Name, g.Amount)
			doc["rare_type_id"] = m.Rare * 10
		}
	case "coin":
		// tracking 分支 currency=diamond（UserPresentsListRow.cs:113-127）。
		doc["message"] = fmt.Sprintf("钻石 ×%d", g.Amount)
		doc["rare_type_id"] = 0
	case "gacha_point":
		// tracking 分支 currency=gold（同 :129-144）。
		doc["message"] = fmt.Sprintf("金币 ×%d", g.Amount)
		doc["rare_type_id"] = 0
	case "medal":
		doc["message"] = fmt.Sprintf("徽章 ×%d", g.Amount)
		doc["rare_type_id"] = 0
	case "item":
		if dep.Items != nil {
			if defs, err := dep.Items.ItemCatalog(); err == nil {
				for _, def := range defs {
					if def.ContentID == int(g.ContentID) {
						doc["message"] = fmt.Sprintf("%s ×%d", def.Name, g.Amount)
						doc["rare_type_id"] = def.Rare * 10
						break
					}
				}
			}
		}
	}
	return doc
}
