-- cn35_event_area_rewards: M8 当期活动区域完成率奖励领取台账。
--
-- 设计（plans/cn35-m8-events-daily-2026-11-19/PLAN.md §2.4、§3.4）：
--   - 活动推进边界与完成率均由 cn35_quest_clears 推导（活动关 id 1440–1451、
--     周常关 id 80001–80064 与主线/资源本号段不冲突），不另建进度表；
--   - 本表只记「某账号某区域某档位（30/60/100）已发放」，主键即幂等闸门；
--     台账插入与礼物盒 cn35_user_presents 写入同事务（cn35store/events.go）。
--
-- Affected routes: /quests/result（活动关结算后对账）、/areas/event（读面对账）。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制。

CREATE TABLE cn35_event_area_rewards (
    user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    event_id INTEGER NOT NULL CHECK (event_id > 0),
    area_id INTEGER NOT NULL CHECK (area_id > 0),
    threshold INTEGER NOT NULL CHECK (threshold IN (30, 60, 100)),
    presents_json TEXT NOT NULL,
    granted_at INTEGER NOT NULL CHECK (granted_at >= 0),
    PRIMARY KEY (user_id, area_id, threshold)
) STRICT;
