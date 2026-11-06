-- 0048_cn35_ap_overflow.sql：升级 AP 叠加新等级完整上限，允许 AP 溢出持有。
--
-- 需求：玩家升级时 AP 按新等级完整上限叠加（ap = ap + new_max_ap），允许超出 max_ap。
-- 本迁移只追加：不改历史 SQL，按 SQLite 官方表重建流程移除 cn35_player_profiles 的
-- CHECK (ap <= max_ap) 表级约束，完整保留全部 16 列与存量账号数据。
--
-- Affected routes: /quests/result、/users/preset_data.json、/quests/execute/<id>.json、/quests/use_ap。
-- Recovery: 停服 → 一致性备份恢复到新 state 目录 → 配对旧二进制。

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
    memo TEXT NOT NULL DEFAULT ''
) STRICT;

INSERT INTO cn35_player_profiles_new (
    user_id, level, exp, max_exp, ap, max_ap, ap_updated_at,
    bp, max_bp, leader_unit_id, login_bonus_receivable_sec,
    guild_name_snapshot, helper_unit_id, created_at, updated_at, memo
)
SELECT
    user_id, level, exp, max_exp, ap, max_ap, ap_updated_at,
    bp, max_bp, leader_unit_id, login_bonus_receivable_sec,
    guild_name_snapshot, helper_unit_id, created_at, updated_at, memo
FROM cn35_player_profiles;

DROP TABLE cn35_player_profiles;
ALTER TABLE cn35_player_profiles_new RENAME TO cn35_player_profiles;
