-- 0049_cn35_guild_defense_batch_and_test001_waiting.sql
-- 1. 修正公会防卫槽 cn35_guild_defense.batch：
--    原服抓包（guilds_monsters.json / guilds_deck.json）中普通魔宠 batch 均为 -1，
--    仅五系融合培育魔宠（100005..100045）具有非负同批排重标记（100/2/3/1/4）。
--    此前存量防卫槽固定写 batch=0，导致客户端 checkSelectMonsterLimit 将所有普通魔宠判为同批冲突。
-- 2. 将内置测试账号 test001（user_id='681abf0d7485'）的 1,251 名同伴分流：
--    保留当前编队、队长/助战、已装魔宠/符文/养成及全部五星等关键角色共 150 名在同行列表（is_waiting=0），
--    其余 1,101 名转入待机所（is_waiting=1），避免同伴一览一次性加载上千角色导致客户端卡死。

UPDATE cn35_guild_defense
SET batch = COALESCE((
    SELECT CASE
        WHEN m.monster_id BETWEEN 100005 AND 100008 THEN 100
        WHEN m.monster_id BETWEEN 100009 AND 100012 THEN 2
        WHEN m.monster_id BETWEEN 100018 AND 100021 THEN 3
        WHEN m.monster_id BETWEEN 100038 AND 100041 THEN 1
        WHEN m.monster_id BETWEEN 100042 AND 100045 THEN 4
        ELSE -1
    END
    FROM cn35_player_monsters m
    WHERE m.user_id = cn35_guild_defense.user_id
      AND m.uuid = cn35_guild_defense.monster_uuid
), -1);

UPDATE cn35_player_units
SET is_waiting = 1
WHERE user_id IN (
    SELECT user_id FROM cn35_accounts
    WHERE account_name = 'test001' OR user_id = '681abf0d7485'
);

WITH test001_users AS (
    SELECT user_id FROM cn35_accounts
    WHERE account_name = 'test001' OR user_id = '681abf0d7485'
),
key_units(unit_id) AS (
    VALUES
        -- 全部 133 名 ★5 同伴（含默认编队 348/336/156/296/375 与 20 名 stage 基准角色）
        (152), (153), (154), (155), (156), (157), (158), (159), (160), (161),
        (230), (280), (290), (296), (331), (336), (348), (368), (375), (396),
        (414), (426), (434), (457), (466), (490), (513), (524), (534), (542),
        (558), (566), (583), (592), (605), (619), (628), (642), (653), (656),
        (662), (677), (681), (690), (697), (701), (715), (722), (736), (742),
        (757), (765), (775), (786), (787), (797), (801), (819), (825), (833),
        (849), (854), (862), (872), (881), (891), (896), (910), (918), (923),
        (938), (943), (948), (961), (967), (978), (983), (984), (995), (1004),
        (1012), (1023), (1032), (1033), (1046), (1047), (1052), (1059), (1074), (1078),
        (1093), (1098), (1107), (1112), (1123), (1131), (1139), (1159), (1160), (1191),
        (1192), (1197), (1198), (1206), (1210), (1214), (1229), (1233), (1234), (1235),
        (1236), (1240), (1242), (1252), (1256), (1261), (1265), (1267), (1272), (1277),
        (110015), (110036), (110047), (110060), (110070), (110075),
        (150002), (150021), (150022), (150036), (150062), (150066), (150098),
        -- 17 名核心 ★4 同伴（凑齐 150 名同行角色）
        (137), (138), (139), (140), (141), (142), (143), (144), (145), (146),
        (147), (148), (149), (150), (151), (229), (231)
),
active_150 AS (
    SELECT pu.user_id, pu.unit_id
    FROM cn35_player_units pu
    JOIN test001_users tu ON tu.user_id = pu.user_id
    ORDER BY
        CASE
            -- P0：当前编队槽位中的角色、队长或助战角色（必须留在同行列表）
            WHEN EXISTS (
                SELECT 1 FROM cn35_player_deck_slots ds
                WHERE ds.user_id = pu.user_id AND ds.unit_id = pu.unit_id
            ) THEN 0
            WHEN EXISTS (
                SELECT 1 FROM cn35_player_profiles pp
                WHERE pp.user_id = pu.user_id
                  AND (pp.leader_unit_id = CAST(pu.unit_id AS TEXT)
                    OR pp.helper_unit_id = CAST(pu.unit_id AS TEXT))
            ) THEN 0
            -- P1：已装备魔宠、符文或已有养成进度/保护的角色
            WHEN pu.monster_uuid <> ''
              OR pu.protected <> 0
              OR pu.level > 1
              OR pu.duplicated_count > 0
              OR EXISTS (
                  SELECT 1 FROM cn35_player_skills ps
                  WHERE ps.user_id = pu.user_id AND ps.unit_id = pu.unit_id
              ) THEN 1
            -- P2：关键角色列表（133 名 ★5 + 17 名 ★4）
            WHEN EXISTS (
                SELECT 1 FROM key_units ku WHERE ku.unit_id = pu.unit_id
            ) THEN 2
            ELSE 3
        END ASC,
        pu.unit_id ASC
    LIMIT 150
)
UPDATE cn35_player_units
SET is_waiting = 0
WHERE (user_id, unit_id) IN (SELECT user_id, unit_id FROM active_150);

