// deck_wire.go MIN350-06：账号编队读写协议端点。
package cn35game

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

func userDecksHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		decks, err := dep.Decks.Decks(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 编队读取失败: %w", err)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_decks",
			"party_count": len(decks),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"user_decks": DecksWire(decks)},
		})
	})
}

func userDeckMutationHandler(dep Deps, clear bool) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		q := req.Query
		partyID := firstNonEmpty(q.Get("party_id"), req.Form.Get("party_id"))
		slotIndex, slotErr := strconv.Atoi(firstNonEmpty(
			q.Get("party_index"), q.Get("slot"),
			req.Form.Get("party_index"), req.Form.Get("slot")))
		unitID, unitErr := strconv.Atoi(firstNonEmpty(
			q.Get("unit_id"), q.Get("id"),
			req.Form.Get("unit_id"), req.Form.Get("id")))
		name := "user_decks_on"
		if clear {
			name = "user_decks_off"
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": name,
			"party_id": partyID, "party_index": slotIndex, "unit_id": unitID,
		}
		if slotErr != nil || unitErr != nil || partyID == "" || unitID <= 0 {
			return cn35wire.MarshalJSON(deckMutationError(ErrDeckSlotInvalid))
		}
		if len(partyID) == 3 && partyID[0] == '0' {
			if decks, derr := dep.Decks.Decks(req.UserID); derr == nil {
				if ord, oerr := ParsePartyOrdinal(partyID); oerr == nil && ord >= 1 && ord <= len(decks) {
					partyID = decks[ord-1].PartyID
				}
			}
		}
		if clear {
			if err := dep.Decks.ClearDeckUnit(req.UserID, partyID, slotIndex, unitID); err != nil {
				return cn35wire.MarshalJSON(deckMutationError(err))
			}
		} else if err := dep.Decks.SetDeckUnit(req.UserID, partyID, slotIndex, unitID); err != nil {
			return cn35wire.MarshalJSON(deckMutationError(err))
		}
		decks, err := dep.Decks.Decks(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 编队更新后读取失败: %w", err)
		}
		c.Decoded["updated"] = true
		return cn35wire.MarshalJSON(map[string]any{
			"status": "success",
			"data":   map[string]any{"user_decks": DecksWire(decks)},
		})
	})
}

func deckMutationError(err error) map[string]any {
	title, body := "编队更新失败", "请重新打开编队页后再试。"
	switch {
	case errors.Is(err, ErrDeckNotFound):
		body = "队伍不存在或请求的队伍编号无效。"
	case errors.Is(err, ErrDeckSlotInvalid):
		body = "槽位编号无效。"
	case errors.Is(err, ErrDeckUnitNotOwned):
		body = "队伍只能使用当前账号持有的同伴。"
	case errors.Is(err, ErrDeckDuplicate):
		body = "同一队伍不能重复放入相同同伴。"
	case errors.Is(err, ErrDeckSlotConflict):
		body = "队伍已在其他位置更新，请重新打开编队页。"
	}
	return map[string]any{"status": "error", "modal": map[string]any{
		"type": "ok", "title": title, "body": body,
	}}
}
