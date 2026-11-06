// present_receive.go P0 写面（2026-09-28 Note）：/user_presents/receive/ 的领域契约。
//
// 客户端请求形态（ilspy-cs 取证）：
//   - 单领 `UserPresentsListRow.cs:86`：`/user_presents/receive/?uuid=<uuid>`；
//   - 全领 `UserPresentReceiveAllImageButton.cs:24-27`：`/user_presents/receive/`，
//     变体 `?without_unit=true`（排除同伴类礼物）；
//   - 回调读顶层 `rewards`/`modal_title`/`modal_body`（ShowContentsDialog，
//     Modal.cs:685-705：每条 reward 需 type/id/message/rare_type_id；
//     icon_asset_name 空时客户端按 type/id 经 UIUtil.IconAssetName 推导，
//     服务端不下发），并按 `type`/`id`/`count` 做埋点（unit/monster/
//     coin/gacha_point/skill/medal 分支在 ListRow.cs:96-192）。
//
// 持久化支持面：unit（同伴）、monster（魔宠）、coin（钻石余额）、
// gacha_point（金币余额）。skill/medal 等类型无库表与入库规则——不领取、
// 不伪造：单领明确拒绝，全领跳过并保持未领。
package cn35game

import "errors"

var (
	// ErrPresentNotFound uuid 不存在或不属本账号（不区分两者，防探测）。
	ErrPresentNotFound = errors.New("cn35game: 礼物不存在")
	// ErrPresentAlreadyReceived 礼物已领取（含并发二次领取）。
	ErrPresentAlreadyReceived = errors.New("cn35game: 礼物已领取")
	// ErrPresentExpired 礼物已过期（expire_at<=now 且非 0）。
	ErrPresentExpired = errors.New("cn35game: 礼物已过期")
	// ErrPresentUnsupported content_type 无持久化实现或内容模板缺失。
	ErrPresentUnsupported = errors.New("cn35game: 礼物类型未支持")
	ErrPresentUnitCap     = errors.New("cn35game: 礼物同伴觉醒超过上限")
)

// PresentGrant 一条实际发放的礼物内容（响应 rewards 行的数据源）。
type PresentGrant struct {
	ContentType  string
	ContentID    int64
	Amount       int64
	MonsterUUIDs []string // monster 类型新建的实例 uuid（发放顺序）
}

// PresentReceiveResult 一次领取请求的发放集合（顺序与处理顺序一致）。
type PresentReceiveResult struct {
	Granted []PresentGrant
}

// PresentReceiveStore 礼物领取写面。
//
// 实现约定（2026-09-28 Note §DB impact）：发奖与 received_at 更新在同一
// 事务；received_at 以条件更新（received_at=0 → 影响行数必须 1）保证
// 幂等；任一失败整体回滚，不产生"已领取未发奖"或"发奖未标记"。
// cat 用于校验 unit/monster 内容模板存在（未知模板按 ErrPresentUnsupported）。
type PresentReceiveStore interface {
	// ReceivePresent 单领：uuid 必须属本账号、未领、未过期、类型受支持。
	ReceivePresent(userID, uuid string, now int64, cat UnitCatalog) (*PresentReceiveResult, error)
	// ReceiveAllPresents 全领：一个事务内领取全部未领未过期且类型受支持的
	// 礼物；不支持类型跳过保持未领；withoutUnit=true 时排除 unit 类型。
	ReceiveAllPresents(userID string, withoutUnit bool, now int64, cat UnitCatalog) (*PresentReceiveResult, error)
}
