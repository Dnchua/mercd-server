-- 0035_cn35_spring_signin.sql：不可思议之泉改为时间累积 + 每月 28 格签到（LOGIN350-02）。
--
-- 需求（用户 2026-10-20）：
--   1. 泉水按时间累积金币/公会建设素材/BP（1000/h 上限 48000、1/h 上限 48、
--      5/h 上限 120），只要有量就能领、可长期攒到上限；BP 允许溢出持有。
--      设计见 notes/proposed/2026-10-20-cn35-spring-signin.md。
--   2. 新增每月 28 格签到（普通 24 + 第 7/14/21/28 格），按月台账防重。
--   3. 旧 LOGIN350-01 的「东八区 4:00 每日一次、固定 1000 金币」口径作废：
--      login_bonus_receivable_sec 语义改为「上次领取时刻 + 3600s」的可领时刻，
--      0 表示由 cn35_player_profiles.created_at 起算（长期未登录账号即接近上限）。
--
-- 本迁移只追加：不改 0034 及更早 SQL；不新增档案列；不写共享 fixture。
-- Affected routes: /users/show_login_bonus、/users/receive_login_bonus、
--   /accu_login_activity/、/accu_login_activity/receive
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；新增表对旧二进制
--   不可见（旧二进制不读），login_bonus_receivable_sec=0 对旧二进制等价「立即可领」；
--   被重建的 cn35_player_profiles 保持全部列与行，仅移除 bp<=max_bp 约束。

-- 1) BP 溢出持有：SQLite 不能删除列约束，按官方表重建流程重建档案表。
--    本表无外键引用方、无索引/触发器，重建只搬数据。
CREATE TABLE cn35_player_profiles_new (
    user_id TEXT PRIMARY KEY REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    level INTEGER NOT NULL CHECK (level >= 1),
    exp INTEGER NOT NULL CHECK (exp >= 0),
    max_exp INTEGER NOT NULL CHECK (max_exp > 0),
    ap INTEGER NOT NULL CHECK (ap >= 0),
    max_ap INTEGER NOT NULL CHECK (max_ap >= 0),
    ap_updated_at TEXT NOT NULL,
    bp INTEGER NOT NULL CHECK (bp >= 0),
    max_bp INTEGER NOT NULL CHECK (max_bp >= 0),
    leader_unit_id TEXT NOT NULL CHECK (length(leader_unit_id) > 0),
    login_bonus_receivable_sec INTEGER NOT NULL CHECK (login_bonus_receivable_sec >= 0),
    guild_name_snapshot TEXT NOT NULL,
    helper_unit_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (ap <= max_ap)
) STRICT;

INSERT INTO cn35_player_profiles_new
    (user_id,level,exp,max_exp,ap,max_ap,ap_updated_at,bp,max_bp,leader_unit_id,
     login_bonus_receivable_sec,guild_name_snapshot,helper_unit_id,created_at,updated_at)
SELECT user_id,level,exp,max_exp,ap,max_ap,ap_updated_at,bp,max_bp,leader_unit_id,
     login_bonus_receivable_sec,guild_name_snapshot,helper_unit_id,created_at,updated_at
FROM cn35_player_profiles;

DROP TABLE cn35_player_profiles;
ALTER TABLE cn35_player_profiles_new RENAME TO cn35_player_profiles;

-- 2) 旧「每日 4:00」可领时刻作废，锚点回落到 created_at。
--    该功能未在生产对玩家开放，重置不涉及任何资产回退。
UPDATE cn35_player_profiles SET login_bonus_receivable_sec = 0;

-- 3) 泉水领取台账：审计与证据。并发/重放由 wire 侧条件 UPDATE 兜底，
--    本表再以 (user_id,claimed_at) 主键防同秒重复。
CREATE TABLE cn35_spring_claims (
    user_id            TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    claimed_at         INTEGER NOT NULL CHECK (claimed_at > 0),
    anchor_before_sec  INTEGER NOT NULL CHECK (anchor_before_sec >= 0),
    gold               INTEGER NOT NULL CHECK (gold >= 0),
    guild_asset        INTEGER NOT NULL CHECK (guild_asset >= 0),
    battle_point       INTEGER NOT NULL CHECK (battle_point >= 0),
    next_receivable_sec INTEGER NOT NULL CHECK (next_receivable_sec > claimed_at),
    PRIMARY KEY (user_id, claimed_at)
) STRICT;

CREATE INDEX idx_cn35_spring_claims_user
    ON cn35_spring_claims (user_id, claimed_at);

-- 4) 月签到台账：(user_id,month_key,node) 主键兜底并发/重放；
--    month_key 为东八区 (now-4h) 的 YYYY-MM，node 为 1..28。
CREATE TABLE cn35_signin_claims (
    user_id        TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    month_key      TEXT NOT NULL CHECK (length(month_key) = 7),
    node           INTEGER NOT NULL CHECK (node BETWEEN 1 AND 28),
    claimed_at     INTEGER NOT NULL CHECK (claimed_at > 0),
    coin           INTEGER NOT NULL CHECK (coin >= 0),
    medal          INTEGER NOT NULL CHECK (medal >= 0),
    gold           INTEGER NOT NULL CHECK (gold >= 0),
    item_content_id INTEGER NOT NULL CHECK (item_content_id >= 0),
    item_count     INTEGER NOT NULL CHECK (item_count >= 0),
    PRIMARY KEY (user_id, month_key, node)
) STRICT;

CREATE INDEX idx_cn35_signin_claims_user_month
    ON cn35_signin_claims (user_id, month_key);
