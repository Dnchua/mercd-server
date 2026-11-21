-- 0057_cn35_pvp_ladder_rob.sql：CN3.5 M10 癒术士大赛（/ladders/*）与公会训练（/rob_battles/*）。
--
-- 只增不改：不触碰 0001..0056 既有迁移与存量数据。
-- 设计：plans/cn35-m10-pvp-2026-11-20/PLAN.md §二；证据：docs/18-癒术士大赛与公会训练回放统计.md。
-- Affected routes: /users/function_portal、/users/preset_data（lc/rc/potions 投影）、
--   /ladders/*、/rob_battles/*、/user_rob_decks*。
-- Recovery: 停服 → 一致性备份恢复到新 state 目录 → 配对旧二进制；新增表对旧二进制无影响。

CREATE TABLE cn35_ladder_profiles (
    user_id TEXT PRIMARY KEY REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    season_key TEXT NOT NULL,
    week_key TEXT NOT NULL,
    score INTEGER NOT NULL DEFAULT 0 CHECK (score >= 0),
    best_win_score INTEGER NOT NULL DEFAULT 0 CHECK (best_win_score >= 0),
    atk_win INTEGER NOT NULL DEFAULT 0 CHECK (atk_win >= 0),
    atk_total INTEGER NOT NULL DEFAULT 0 CHECK (atk_total >= 0),
    def_win INTEGER NOT NULL DEFAULT 0 CHECK (def_win >= 0),
    def_total INTEGER NOT NULL DEFAULT 0 CHECK (def_total >= 0),
    week_battles INTEGER NOT NULL DEFAULT 0 CHECK (week_battles >= 0),
    day_key TEXT NOT NULL DEFAULT '',
    day_used INTEGER NOT NULL DEFAULT 0 CHECK (day_used >= 0),
    day_defended INTEGER NOT NULL DEFAULT 0 CHECK (day_defended >= 0),
    potion_1 INTEGER NOT NULL DEFAULT 0 CHECK (potion_1 >= 0),
    potion_2 INTEGER NOT NULL DEFAULT 0 CHECK (potion_2 >= 0),
    potion_3 INTEGER NOT NULL DEFAULT 0 CHECK (potion_3 >= 0),
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE cn35_ladder_decks (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    slot_index INTEGER NOT NULL CHECK (slot_index BETWEEN 0 AND 4),
    monster_uuid TEXT NOT NULL CHECK (monster_uuid <> ''),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, slot_index),
    UNIQUE (user_id, monster_uuid)
) STRICT;

CREATE TABLE cn35_ladder_grow (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    grow_type INTEGER NOT NULL CHECK (grow_type BETWEEN 1 AND 3),
    attr_id INTEGER NOT NULL CHECK (attr_id BETWEEN 1 AND 5),
    steps INTEGER NOT NULL DEFAULT 0 CHECK (steps >= 0),
    spent INTEGER NOT NULL DEFAULT 0 CHECK (spent >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, grow_type, attr_id)
) STRICT;

CREATE TABLE cn35_ladder_sessions (
    battle_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    opponent_id TEXT NOT NULL,
    opponent_kind TEXT NOT NULL CHECK (opponent_kind IN ('player', 'npc')),
    opponent_name TEXT NOT NULL,
    opponent_score INTEGER NOT NULL CHECK (opponent_score >= 0),
    difficulty_id TEXT NOT NULL CHECK (difficulty_id IN ('normal', 'hard', 'very_hard')),
    rewards_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL CHECK (status IN ('prepared', 'paid', 'won', 'lost')),
    score_delta INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    settled_at INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX cn35_ladder_sessions_user_idx ON cn35_ladder_sessions (user_id, status);

CREATE TABLE cn35_ladder_settlements (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    week_key TEXT NOT NULL,
    season_key TEXT NOT NULL,
    score INTEGER NOT NULL CHECK (score >= 0),
    tier TEXT NOT NULL CHECK (tier IN ('SS', 'S', 'A', 'B')),
    reward_json TEXT NOT NULL,
    claimed INTEGER NOT NULL DEFAULT 0 CHECK (claimed IN (0, 1)),
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, week_key)
) STRICT;

CREATE TABLE cn35_rob_profiles (
    user_id TEXT PRIMARY KEY REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    score INTEGER NOT NULL DEFAULT 1000 CHECK (score >= 0),
    day_key TEXT NOT NULL DEFAULT '',
    rc_left INTEGER NOT NULL DEFAULT 0 CHECK (rc_left >= 0),
    tickets_bought INTEGER NOT NULL DEFAULT 0 CHECK (tickets_bought BETWEEN 0 AND 3),
    atk_win INTEGER NOT NULL DEFAULT 0 CHECK (atk_win >= 0),
    atk_total INTEGER NOT NULL DEFAULT 0 CHECK (atk_total >= 0),
    def_win INTEGER NOT NULL DEFAULT 0 CHECK (def_win >= 0),
    def_total INTEGER NOT NULL DEFAULT 0 CHECK (def_total >= 0),
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE cn35_rob_decks (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    party_type TEXT NOT NULL CHECK (party_type IN ('atk', 'def')),
    party_id INTEGER NOT NULL CHECK (party_id BETWEEN 1 AND 2),
    slot_index INTEGER NOT NULL CHECK (slot_index BETWEEN 0 AND 4),
    unit_id INTEGER NOT NULL CHECK (unit_id > 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, party_type, party_id, slot_index),
    UNIQUE (user_id, party_type, unit_id)
) STRICT;

CREATE TABLE cn35_rob_sessions (
    battle_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    opponent_id TEXT NOT NULL,
    opponent_kind TEXT NOT NULL CHECK (opponent_kind IN ('player', 'npc')),
    opponent_name TEXT NOT NULL,
    opponent_score INTEGER NOT NULL CHECK (opponent_score >= 0),
    revenge_record_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('prepared', 'paid', 'won', 'lost')),
    score_delta INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    settled_at INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX cn35_rob_sessions_user_idx ON cn35_rob_sessions (user_id, status);

CREATE TABLE cn35_rob_records (
    record_id TEXT PRIMARY KEY,
    defender_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    attacker_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    attacker_win INTEGER NOT NULL CHECK (attacker_win IN (0, 1)),
    score_delta INTEGER NOT NULL DEFAULT 0,
    revenged INTEGER NOT NULL DEFAULT 0 CHECK (revenged IN (0, 1)),
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX cn35_rob_records_defender_idx ON cn35_rob_records (defender_id, created_at);
