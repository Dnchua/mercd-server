-- cn35_login_bonus_claims: 登录奖励（不可思议之泉）领取台账（LOGIN350-01）。
--
-- 需求：每账号每个登录奖励周期（东八区 4:00 刷新，见 cn35game/login_bonus.go
-- LoginBonusRules.RefreshHour）可领取一次；领取记账与余额入账、
-- cn35_player_profiles.login_bonus_receivable_sec 倒计时推进同事务。
-- period_key 为按 (now-RefreshHour) 的 UTC+8 日期（YYYY-MM-DD），与
-- cn35_daily_shop_claims 同口径；(user_id,period_key) 主键兜底并发与重放。
-- reward_type/reward_count 为结构化奖励快照（本期单一货币行，
-- 不把货币/数量塞进非结构化 JSON）。next_receivable_sec 冗余记录领取后
-- 的下次可领时间，便于审计与恢复核对。
-- Affected routes: /users/show_login_bonus、/users/receive_login_bonus。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；仅新增表。

CREATE TABLE cn35_login_bonus_claims (
    user_id            TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    period_key         TEXT NOT NULL CHECK (length(period_key) = 10),
    claimed_at         INTEGER NOT NULL CHECK (claimed_at > 0),
    next_receivable_sec INTEGER NOT NULL CHECK (next_receivable_sec > claimed_at),
    reward_type        TEXT NOT NULL CHECK (length(reward_type) > 0),
    reward_count       INTEGER NOT NULL CHECK (reward_count > 0),
    PRIMARY KEY (user_id, period_key)
) STRICT;

CREATE INDEX idx_cn35_login_bonus_claims_user
    ON cn35_login_bonus_claims (user_id, claimed_at);
