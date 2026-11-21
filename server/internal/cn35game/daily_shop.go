// daily_shop.go SHOP350-01：每日商店（面包店/RedDiamondShop）域定义。
//
// 需求形态：每日发放 100 钻石 = 商店内 0 元购商品，每账号每日限购 1 次，
// 库存每日东八区 4:00 刷新。客户端契约见 daily_shop_wire.go。
package cn35game

import "errors"

// ErrDailyShopClaimed 本周期已领取（客户端映射 result:false + 提示）。
var ErrDailyShopClaimed = errors.New("cn35game: 今日已领取")

// ErrDailyShopGoodsNotFound 商品 id 不在每日商店目录。
var ErrDailyShopGoodsNotFound = errors.New("cn35game: 商品不存在")

// ErrDailyShopInsufficientCoin 兑换契约书时钻石余额不足。
var ErrDailyShopInsufficientCoin = errors.New("cn35game: 钻石不足")

// DailyShopRules 每日免费商品占位常量（数值口径与 GuildRules 一致，
// 后期由数值 agent 统一调整）。
var DailyShopRules = struct {
	// GoodsID 商品行 id（/red_diamond_shop/buy?id=）。
	GoodsID int
	// GoodsKey 台账键。
	GoodsKey string
	// Name/Description 行展示文案。
	Name        string
	Description string
	// Coin 每次领取的钻石数量。
	Coin int64
	// DailyLimit 每周期限购次数。
	DailyLimit int
	// RefreshHour 库存每日刷新时刻（东八区，4=凌晨 4 点）。
	RefreshHour int
}{
	GoodsID:     1,
	GoodsKey:    "daily_free_diamond",
	Name:        "每日免费钻石",
	Description: "每日可免费领取 100 钻石，凌晨 4 点刷新",
	Coin:        100,
	DailyLimit:  1,
	RefreshHour: 4,
}

// ScoutBookShopRule 面包店常驻栏（red_diamond_shop_fixed）骑士契约书兑换规则。
var ScoutBookShopRule = struct {
	GoodsID     int
	Name        string
	Description string
	DiamondCost int64
	MaxBatch    int
}{
	GoodsID:     10000,
	Name:        "骑士契约书",
	Description: "必签HR(★3)以上同伴，十连契约享9折优惠（仅需9本）",
	DiamondCost: 5,
	MaxBatch:    999,
}

// DailyShopStore 每日商店读面 + 领取/兑换写面。
type DailyShopStore interface {
	// DailyShopClaims 该周期内各 goods_key 已领次数（无记录=空集）。
	DailyShopClaims(userID, periodKey string) (map[string]int, error)
	// ClaimDailyPack 单事务：领取计数+1（条件 UPDATE 限 limit/周期）→
	// coin 入账；超限 → ErrDailyShopClaimed。返回入账后 coin 余额。
	ClaimDailyPack(userID, goodsKey, periodKey string, coin int64, limit int) (int64, error)
	// BuyScoutBook 单事务：条件扣除 count * unitDiamondCost 钻石并入账
	// count 本骑士契约书（scout_book_1），返回更新后的 (coin, scoutBook1)。
	BuyScoutBook(userID string, count, unitDiamondCost int64) (coinAfter, bookAfter int64, err error)
}
