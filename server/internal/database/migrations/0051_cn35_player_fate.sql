-- 0051_cn35_player_fate.sql：M1 命运星盘（艾琳的小屋）玩家状态与转动扣费台账（FATE350-01）。
--
-- Affected routes: /fate/show、/fate/fate_buff_summary、/fate/roll、/users/function_portal。
-- Recovery: 仅新增表；停服 → 一致性备份恢复到新 state 目录 → 配对旧二进制。

CREATE TABLE IF NOT EXISTS cn35_player_fate (
    user_id          TEXT    NOT NULL PRIMARY KEY,
    period_key       TEXT    NOT NULL,
    daily_luck_desc  TEXT    NOT NULL DEFAULT '',
    today_roll_times INTEGER NOT NULL DEFAULT 0 CHECK (today_roll_times >= 0),
    fate_list_json   TEXT    NOT NULL DEFAULT '[]',
    updated_at       TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS cn35_player_fate_ledger (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          TEXT    NOT NULL,
    period_key       TEXT    NOT NULL,
    roll_type        TEXT    NOT NULL,
    roll_seq         INTEGER NOT NULL CHECK (roll_seq >= 1),
    cost_coin        INTEGER NOT NULL CHECK (cost_coin >= 0),
    fate_list_json   TEXT    NOT NULL,
    created_at       TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_cn35_player_fate_ledger_user_period
    ON cn35_player_fate_ledger (user_id, period_key);

