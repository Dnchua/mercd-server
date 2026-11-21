-- 0053_cn35_monster_fusion_and_unit_ops.sql
-- M5（CN3.5）：魔宠融合/进化之卵/融合宠强化、同伴进化与能力互换目录及字段补齐。
-- 严格追加迁移，不改写 0001..0052 历史 SQL。

-- 1. 为玩家魔宠表新增融合度列 fusion_degrees（0..100，供 MergeMonsteUpModal 展示与保底累加）。
ALTER TABLE cn35_player_monsters ADD COLUMN fusion_degrees INTEGER NOT NULL DEFAULT 0;

-- 2. 回填存量五系融合培育魔宠（100005..100045）的 is_rare 与默认 scale_limit。
UPDATE cn35_player_monsters
SET is_rare = 1
WHERE monster_id IN (
    100005, 100006, 100007, 100008,
    100009, 100010, 100011, 100012,
    100018, 100019, 100020, 100021,
    100038, 100039, 100040, 100041,
    100042, 100043, 100044, 100045
) AND is_rare IS NULL;

UPDATE cn35_player_monsters
SET scale_limit = CASE
    WHEN monster_id IN (100005, 100009, 100018, 100038, 100042) THEN '1.850'
    WHEN monster_id IN (100006, 100010, 100039, 100043) THEN '1.923'
    WHEN monster_id = 100019 THEN '1.913'
    WHEN monster_id IN (100007, 100011, 100020, 100040, 100044) THEN '2.023'
    WHEN monster_id IN (100008, 100012, 100021, 100041, 100045) THEN '2.300'
    ELSE scale_limit
END
WHERE monster_id IN (
    100005, 100006, 100007, 100008,
    100009, 100010, 100011, 100012,
    100018, 100019, 100020, 100021,
    100038, 100039, 100040, 100041,
    100042, 100043, 100044, 100045
) AND (scale_limit IS NULL OR scale_limit = '');

-- 3. 物品目录新增：
--    - 神秘水晶（evolve_item_1，wire content_type_id=107, id=1, item_type=3, bag_category='6'）
--    - 20 种进化之卵（evolve_fancy_item_101..504，wire content_type_id=106, id=101..504, item_type=4, bag_category='6'）
--    - 能力互换石（power_stone，wire content_type_id=2, id=200016, code='power_stone', item_type=0, bag_category='6'）
INSERT OR IGNORE INTO cn35_item_catalog (
    item_key, kind, content_id, name, icon_asset_name, element, form, rare, bag_category, enabled, created_at, updated_at
) VALUES
    ('evolve_item_1',         'consumable', 107001, '神秘水晶',       'evolve_item',       0, 0, 1, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_101', 'consumable', 106101, '普通的火之蛋',   'evolve_fancy_item', 1, 0, 1, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_102', 'consumable', 106102, '稀有的火之蛋',   'evolve_fancy_item', 1, 0, 2, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_103', 'consumable', 106103, '超稀有的火之蛋', 'evolve_fancy_item', 1, 0, 3, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_104', 'consumable', 106104, '究极的火之蛋',   'evolve_fancy_item', 1, 0, 4, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_201', 'consumable', 106201, '普通的水之蛋',   'evolve_fancy_item', 2, 0, 1, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_202', 'consumable', 106202, '稀有的水之蛋',   'evolve_fancy_item', 2, 0, 2, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_203', 'consumable', 106203, '超稀有的水之蛋', 'evolve_fancy_item', 2, 0, 3, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_204', 'consumable', 106204, '究极的水之蛋',   'evolve_fancy_item', 2, 0, 4, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_301', 'consumable', 106301, '普通的风之蛋',   'evolve_fancy_item', 3, 0, 1, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_302', 'consumable', 106302, '稀有的风之蛋',   'evolve_fancy_item', 3, 0, 2, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_303', 'consumable', 106303, '超稀有的风之蛋', 'evolve_fancy_item', 3, 0, 3, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_304', 'consumable', 106304, '究极的风之蛋',   'evolve_fancy_item', 3, 0, 4, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_401', 'consumable', 106401, '普通的光之蛋',   'evolve_fancy_item', 4, 0, 1, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_402', 'consumable', 106402, '稀有的光之蛋',   'evolve_fancy_item', 4, 0, 2, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_403', 'consumable', 106403, '超稀有的光之蛋', 'evolve_fancy_item', 4, 0, 3, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_404', 'consumable', 106404, '究极的光之蛋',   'evolve_fancy_item', 4, 0, 4, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_501', 'consumable', 106501, '普通的暗之蛋',   'evolve_fancy_item', 5, 0, 1, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_502', 'consumable', 106502, '稀有的暗之蛋',   'evolve_fancy_item', 5, 0, 2, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_503', 'consumable', 106503, '超稀有的暗之蛋', 'evolve_fancy_item', 5, 0, 3, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('evolve_fancy_item_504', 'consumable', 106504, '究极的暗之蛋',   'evolve_fancy_item', 5, 0, 4, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z'),
    ('power_stone',           'consumable', 200016, '能力互换石',     'power_stone',       0, 0, 1, '6', 1, '2026-11-08T00:00:00Z', '2026-11-08T00:00:00Z');

-- 4. 为内置测试账号 test001 预置融合与互换素材（仅当账号存在时插入）。
WITH v(item_key, qty) AS (
    VALUES
        ('evolve_item_1',         10),
        ('evolve_fancy_item_102',  2),
        ('evolve_fancy_item_203',  1),
        ('evolve_fancy_item_301',  2),
        ('evolve_fancy_item_502',  1),
        ('evolve_fancy_item_503',  1),
        ('power_stone',            5)
)
INSERT OR IGNORE INTO cn35_player_items (user_id, item_key, quantity, updated_at)
SELECT u.user_id, v.item_key, v.qty, '2026-11-08T00:00:00Z'
FROM cn35_accounts u
CROSS JOIN v
WHERE u.account_name = 'test001' OR u.user_id = '681abf0d7485';
