-- 0052_cn35_friends.sql：M3 好友系统与友情徽章礼物盒（FRIEND350-01）。
--
-- Affected routes: /friends/index、/friends/friend_apply_list、/friends/search_friend、
--   /friends/apply_friend、/friends/apply_friend_2、/friends/add_friend、
--   /friends/delete_friend、/friends/clear_apply_list、/friends/give_friend_medal、
--   /friends/give_all_friend_medal、/friends/show_friend_medal_list、
--   /friends/acquire_friend_medals、/user_follows/、/user_message_blocks/。
-- Recovery: 仅新增表；停服 → 一致性备份恢复到新 state 目录 → 配对旧二进制。

CREATE TABLE IF NOT EXISTS cn35_player_friends (
    user_id          TEXT    NOT NULL,
    friend_user_id   TEXT    NOT NULL,
    last_give_period TEXT    NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    PRIMARY KEY (user_id, friend_user_id)
);

CREATE INDEX IF NOT EXISTS idx_cn35_player_friends_user
    ON cn35_player_friends (user_id, created_at);

CREATE TABLE IF NOT EXISTS cn35_player_friend_applies (
    target_user_id    TEXT    NOT NULL,
    applicant_user_id TEXT    NOT NULL,
    created_at        INTEGER NOT NULL,
    PRIMARY KEY (target_user_id, applicant_user_id)
);

CREATE INDEX IF NOT EXISTS idx_cn35_player_friend_applies_target
    ON cn35_player_friend_applies (target_user_id, created_at);

CREATE TABLE IF NOT EXISTS cn35_player_friend_medal_gifts (
    uuid            TEXT    PRIMARY KEY,
    user_id         TEXT    NOT NULL,
    sender_user_id  TEXT    NOT NULL,
    sender_name     TEXT    NOT NULL,
    sender_unit_id  INTEGER NOT NULL,
    count           INTEGER NOT NULL DEFAULT 1 CHECK (count >= 1),
    created_at_sec  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_cn35_player_friend_medal_gifts_user
    ON cn35_player_friend_medal_gifts (user_id, created_at_sec);

-- 每日友情徽章赠送持久记录（独立于好友关系边存续，防止“赠送→解除→重加→再赠送”刷徽章与 CP）
CREATE TABLE IF NOT EXISTS cn35_player_friend_give_log (
    user_id          TEXT    NOT NULL,
    friend_user_id   TEXT    NOT NULL,
    last_give_period TEXT    NOT NULL,
    updated_at       INTEGER NOT NULL,
    PRIMARY KEY (user_id, friend_user_id)
);

CREATE INDEX IF NOT EXISTS idx_cn35_player_friend_give_log_user_period
    ON cn35_player_friend_give_log (user_id, last_give_period);


