-- 0055_cn35_spring_medal_and_special_scout.sql：不可思议之泉徽章产出与契约书/特殊招募票券目录。
--
-- 本迁移只追加，不改写/删除 0001..0054 任何历史 SQL：
--   1. cn35_spring_claims 追加 medal 列（重建值：每个可领周期随机 5..100 徽章）。
--   2. 为存量账号回填 scout_book_1（骑士契约书）初始余额 0。
--   3. cn35_item_catalog 追加 3★/4★/5★ 契约书（content_id 100015/100011/100012）
--      与 ★3/★4/★5 信（content_id 100021/100022/100023，避开 0017 已占用的
--      powerup_item 11..13；wire 层按 ticket code 映射回 id=11/12/13）。
--
-- Affected routes: /users/receive_login_bonus、/red_diamond_shop/*、
--   /gachas/*、/user_items/*
-- Recovery: 停服 → 一致性备份恢复到新 state 目录 → 配对旧二进制；新增列带默认值 0，
--   新增目录行与 balances 行对旧二进制无破坏性。

ALTER TABLE cn35_spring_claims
    ADD COLUMN medal INTEGER NOT NULL DEFAULT 0 CHECK (medal >= 0);

INSERT OR IGNORE INTO cn35_player_balances (user_id, currency, amount, updated_at)
SELECT user_id, 'scout_book_1', 0, updated_at FROM cn35_accounts;

INSERT OR IGNORE INTO cn35_item_catalog
    (item_key, kind, content_id, name, icon_asset_name, element, form, rare, bag_category, enabled, created_at, updated_at)
VALUES
    ('scout_book_3star', 'consumable', 100015, '3星契约书（★★★）', 'common_icon/item_100015', 0, 0, 3, '6', 1, '2026-11-16T00:00:00Z', '2026-11-16T00:00:00Z'),
    ('scout_book_4star', 'consumable', 100011, '4星契约书（★★★★）', 'common_icon/item_100011', 0, 0, 4, '6', 1, '2026-11-16T00:00:00Z', '2026-11-16T00:00:00Z'),
    ('scout_book_5star', 'consumable', 100012, '5星契约书（★★★★★）', 'common_icon/item_100012', 0, 0, 5, '6', 1, '2026-11-16T00:00:00Z', '2026-11-16T00:00:00Z'),
    ('gacha_ticket_11',  'consumable', 100021, '★3 信',               'common_icon/gacha_ticket_11', 0, 0, 3, '7', 1, '2026-11-16T00:00:00Z', '2026-11-16T00:00:00Z'),
    ('gacha_ticket_12',  'consumable', 100022, '★4 信',               'common_icon/gacha_ticket_12', 0, 0, 4, '7', 1, '2026-11-16T00:00:00Z', '2026-11-16T00:00:00Z'),
    ('gacha_ticket_13',  'consumable', 100023, '★5 信',               'common_icon/gacha_ticket_13', 0, 0, 5, '7', 1, '2026-11-16T00:00:00Z', '2026-11-16T00:00:00Z');

-- 放宽 cn35_gacha_pools.max_bonus（0013 原为 = 0），支持骑士契约书十连 9 折（max_exec=9, max_bonus=1）。
-- 在 foreign_keys=ON 下连同唯一外键子表 cn35_gacha_pool_draft_members 同事务重建。
CREATE TABLE _cn35_gacha_draft_members_bak AS
SELECT pool_id, unit_id, weight, updated_by, updated_at
FROM cn35_gacha_pool_draft_members;

DROP TABLE cn35_gacha_pool_draft_members;

CREATE TABLE cn35_gacha_pools_new (
    pool_id TEXT PRIMARY KEY CHECK (length(pool_id) > 0),
    slot INTEGER NOT NULL CHECK (slot BETWEEN 1 AND 4),
    list_type_id INTEGER NOT NULL CHECK (list_type_id BETWEEN 0 AND 3),
    code TEXT NOT NULL UNIQUE CHECK (length(code) > 0),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    price_point INTEGER NOT NULL DEFAULT 0 CHECK (price_point >= 0),
    price_coin INTEGER NOT NULL DEFAULT 0 CHECK (price_coin >= 0),
    max_exec INTEGER NOT NULL DEFAULT 10 CHECK (max_exec BETWEEN 1 AND 10),
    max_bonus INTEGER NOT NULL DEFAULT 0 CHECK (max_bonus BETWEEN 0 AND 2),
    limited INTEGER NOT NULL DEFAULT 0 CHECK (limited IN (0, 1)),
    begin_at INTEGER NOT NULL DEFAULT 0,
    end_at INTEGER NOT NULL DEFAULT 0,
    featured_ids TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(featured_ids)),
    distribution TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(distribution)),
    pity TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(pity)),
    pity_group TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '',
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    UNIQUE (slot),
    UNIQUE (list_type_id),
    CHECK (max_exec + max_bonus <= 10)
) STRICT;

INSERT INTO cn35_gacha_pools_new (
    pool_id, slot, list_type_id, code, name, description, enabled,
    price_point, price_coin, max_exec, max_bonus, limited, begin_at, end_at,
    featured_ids, distribution, pity, pity_group, notes, updated_by, updated_at
)
SELECT
    pool_id, slot, list_type_id, code, name, description, enabled,
    price_point, price_coin, max_exec, max_bonus, limited, begin_at, end_at,
    featured_ids, distribution, pity, pity_group, notes, updated_by, updated_at
FROM cn35_gacha_pools;

DROP TABLE cn35_gacha_pools;

ALTER TABLE cn35_gacha_pools_new RENAME TO cn35_gacha_pools;

CREATE TABLE cn35_gacha_pool_draft_members (
    pool_id TEXT NOT NULL REFERENCES cn35_gacha_pools(pool_id) ON DELETE CASCADE,
    unit_id INTEGER NOT NULL CHECK (unit_id > 0),
    weight INTEGER NOT NULL DEFAULT 1 CHECK (weight > 0),
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (pool_id, unit_id)
) STRICT;

INSERT INTO cn35_gacha_pool_draft_members (pool_id, unit_id, weight, updated_by, updated_at)
SELECT pool_id, unit_id, weight, updated_by, updated_at
FROM _cn35_gacha_draft_members_bak;

DROP TABLE _cn35_gacha_draft_members_bak;
