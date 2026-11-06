-- 0050_cn35_ap_potion_icon.sql：修正元气之水（ap_potion）/勇气之水（bp_potion）图标与名称，并为 test001 补齐元气之水库存。
--
-- 原服抓包证据（mlk-capture-201711/decrypted/1/user_items.json）与客户端内嵌纹理核验：
--   - 元气之水（id=67）：icon_asset_name = 'common_icon/item_67'（粉红色棱柱水晶瓶）；
--   - 勇气之水（id=139）：icon_asset_name = 'common_icon/item_139'（蓝色棱柱水晶瓶）；
--   - 此前 0024 使用的 common_icon/potion_2、potion_1 实为圆肚软木塞药水瓶（暴走/好感度药水素材族），与元气之水原图不符。
--
-- Affected routes: /user_items/、/medal_items/、/quests/execute/<id>.json（no_ap 弹窗）。
-- Recovery: 仅 UPDATE/INSERT 存量行；停服 → 一致性备份恢复到新 state 目录 → 配对旧二进制。

UPDATE cn35_item_catalog
SET name = '元气之水',
    icon_asset_name = 'common_icon/item_67',
    updated_at = '2026-11-06T00:00:00Z'
WHERE item_key = 'ap_potion';

UPDATE cn35_item_catalog
SET name = '勇气之水',
    icon_asset_name = 'common_icon/item_139',
    updated_at = '2026-11-06T00:00:00Z'
WHERE item_key = 'bp_potion';

UPDATE cn35_medal_goods_drafts
SET name = '元气之水',
    updated_at = '2026-11-06T00:00:00Z'
WHERE goods_key = 'medal_potion_ap';

UPDATE cn35_medal_goods_drafts
SET name = '勇气之水',
    updated_at = '2026-11-06T00:00:00Z'
WHERE goods_key = 'medal_potion_bp';

-- 为内置测试账号 test001 保底配备 10 瓶元气之水，便于实机与回归测试直接触发 NoApModal 恢复体力。
INSERT INTO cn35_player_items (user_id, item_key, quantity, updated_at)
SELECT u.user_id, 'ap_potion', 10, '2026-11-06T00:00:00Z'
FROM cn35_accounts u
WHERE u.account_name = 'test001' OR u.user_id = '681abf0d7485'
ON CONFLICT(user_id, item_key) DO UPDATE SET
    quantity = MAX(cn35_player_items.quantity, 10),
    updated_at = '2026-11-06T00:00:00Z';

