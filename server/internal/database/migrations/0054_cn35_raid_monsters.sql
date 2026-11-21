-- 0054_cn35_raid_monsters.sql：CN3.5 暴走魔宠（Raid）活动全链路表结构。
--
-- 只增不改：绝不触碰 0001..0053 既有迁移与存量玩家数据。
-- Affected routes:
--   /quests/result（强袭遭遇钩子）、/users/preset_data（活动红点）、
--   /raid_monsters/{,show,execute.json,bp_use,lose,result,call_help,giveup,damage_list,lv_bonus,pt_bonus,exchange}。
-- Recovery: 停服 → 一致性备份恢复 state 目录 → 配对旧二进制；新增表对旧二进制无影响。

CREATE TABLE cn35_raid_profiles (
    user_id TEXT PRIMARY KEY,
    raid_lv INTEGER NOT NULL DEFAULT 1 CHECK (raid_lv >= 1),
    raid_pt INTEGER NOT NULL DEFAULT 0 CHECK (raid_pt >= 0),
    discovered_count INTEGER NOT NULL DEFAULT 0 CHECK (discovered_count >= 0),
    killed_count INTEGER NOT NULL DEFAULT 0 CHECK (killed_count >= 0),
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE cn35_raid_instances (
    raid_id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL,
    guild_id TEXT NOT NULL DEFAULT '',
    monster_id INTEGER NOT NULL CHECK (monster_id > 0),
    monster_name TEXT NOT NULL,
    attr_id INTEGER NOT NULL CHECK (attr_id BETWEEN 1 AND 5),
    level INTEGER NOT NULL CHECK (level >= 1),
    max_point INTEGER NOT NULL CHECK (max_point >= 1),
    current_point INTEGER NOT NULL CHECK (current_point >= 0),
    initial_power INTEGER NOT NULL CHECK (initial_power >= 0),
    attacked INTEGER NOT NULL DEFAULT 0 CHECK (attacked IN (0, 1)),
    help_guild INTEGER NOT NULL DEFAULT 0 CHECK (help_guild IN (0, 1)),
    help_community INTEGER NOT NULL DEFAULT 0 CHECK (help_community IN (0, 1)),
    status TEXT NOT NULL CHECK (status IN ('active', 'defeated', 'giveup', 'expired')),
    spawned_at INTEGER NOT NULL,
    expired_at INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX cn35_raid_instances_owner_status_idx
    ON cn35_raid_instances (owner_user_id, status);

CREATE INDEX cn35_raid_instances_help_idx
    ON cn35_raid_instances (status, help_guild, help_community, expired_at);

CREATE TABLE cn35_raid_participants (
    raid_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    total_damage INTEGER NOT NULL DEFAULT 0 CHECK (total_damage >= 0),
    hit_count INTEGER NOT NULL DEFAULT 0 CHECK (hit_count >= 0),
    free_first_used INTEGER NOT NULL DEFAULT 0 CHECK (free_first_used IN (0, 1)),
    reward_claimed INTEGER NOT NULL DEFAULT 0 CHECK (reward_claimed IN (0, 1)),
    pt_earned INTEGER NOT NULL DEFAULT 0 CHECK (pt_earned >= 0),
    last_hit_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (raid_id, user_id)
) STRICT;

CREATE TABLE cn35_raid_sessions (
    battle_id TEXT PRIMARY KEY,
    raid_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    difficulty_id TEXT NOT NULL CHECK (difficulty_id IN ('normal', 'hard', 'very_hard')),
    time_limit INTEGER NOT NULL CHECK (time_limit > 0),
    bp_cost INTEGER NOT NULL CHECK (bp_cost >= 0),
    free_first INTEGER NOT NULL DEFAULT 0 CHECK (free_first IN (0, 1)),
    party_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL CHECK (status IN ('prepared', 'bp_paid', 'won', 'lost')),
    reported_damage INTEGER NOT NULL DEFAULT 0 CHECK (reported_damage >= 0),
    created_at INTEGER NOT NULL,
    settled_at INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX cn35_raid_sessions_user_raid_idx
    ON cn35_raid_sessions (user_id, raid_id);

CREATE TABLE cn35_raid_lv_claims (
    user_id TEXT NOT NULL,
    target_lv INTEGER NOT NULL CHECK (target_lv >= 1),
    claimed_at TEXT NOT NULL,
    PRIMARY KEY (user_id, target_lv)
) STRICT;

CREATE TABLE cn35_raid_pt_exchanges (
    user_id TEXT NOT NULL,
    slot_id TEXT NOT NULL,
    exchanged_count INTEGER NOT NULL DEFAULT 0 CHECK (exchanged_count >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, slot_id)
) STRICT;

