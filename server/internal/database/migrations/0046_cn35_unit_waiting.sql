-- 0046_cn35_unit_waiting.sql：同伴待机所分流状态列（/albums/wait 与 /albums/back）。
--
-- 契约依据（ilspy-cs 反编译 + 2017-11 国服抓包 albums/albums_wait/albums_back）：
--   - is_waiting=0：同行中（出现在 /user_units/ 列表，/albums/ 行标记 is_user_unit="true"、
--     is_waiting="false"）；
--   - is_waiting=1：待机中（不出现在 /user_units/ 列表，/albums/ 行标记 is_user_unit="false"、
--     is_waiting="true"，并下发 waiting_unit_level 与 waiting_unit_duplicated_count）。
--   - 抽卡/礼物重复获得处于待机所的同伴时，直接累加 duplicated_count（保留 ★2-5 15 觉、★1 5 觉上限）。
--
-- Affected routes: /albums/、/albums/album_unit、/albums/show_unit、/albums/wait、/albums/back、
--   /user_units、/users/preset_data.json。
-- Recovery: 停服 → 一致性备份恢复到新 state 目录 → 配对旧二进制；本迁移只追加列与索引，
--   默认 0 不回填，存量账号同行列表语义与迁移前完全一致。

ALTER TABLE cn35_player_units
    ADD COLUMN is_waiting INTEGER NOT NULL DEFAULT 0 CHECK (is_waiting IN (0, 1));

CREATE INDEX cn35_player_units_waiting_key
    ON cn35_player_units(user_id, is_waiting);

