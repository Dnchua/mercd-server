-- 0036_cn35_login_adventure.sql：登录冒险「图图历险记」真实活动（LOGIN350-03）。
--
-- 需求（用户 2026-10-26）：把 CN3.5 `/adventure/`（Home requireData
-- `LoginAdventureData`，页面 `LoginAdventure`）从「结构完整空态跳过弹窗」
-- 升级为真实活动：轮次日程 + 按日前进位图 + 当日奖励经 `/user_presents/`
-- 内容池入账 + `today_days` 当日一次性弹窗开关 + 走完一轮次日滚动开新轮。
-- 设计见 notes/proposed/2026-10-26-cn35-login-adventure.md；验收 LA-01—15 见
-- plans/cn350-login-adventure-2026-10-26/README.md。
--
-- 口径边界（红线：未验证的推断不得写成事实）：
--   - 客户端消费链是**静态源码取证**（LoginAdventurePage.cs 行号可回指，
--     见 artifacts/tasks/login350-03-login-adventure/evidence/loginadventure-client-contract.md）。
--   - 原服的轮长（days.Count）、奖励数值、special 日分布、path 自定义与否、
--     reward_config_id 取值域**均未取证**。本迁移的种子行全部是**重建运营初值**
--     （对应客户端 defaultPath 的 10 节点蛇形地图 + 常规/特别日交替），
--     不是原服数据；契约卡已如实标注「重建，非原服取证」。
--   - `cn35_adventure_days` 把「展示形状」与「实际发放」拆成两组列：
--     客户端页面只渲染 rewards[0]（LoginAdventurePage.cs:881-887），而发放
--     走礼物盒内容池，两者类型域不同（如 unit/-1 只是 scout_book 图标特例，
--     不是可发放的同伴）。拆列后发放口径可审计、不被展示口径污染。
--
-- Affected routes: GET|POST /adventure/（读面 + 当日首次发放）、
--   GET|POST /user_presents/（读到当日礼物）、/user_presents/receive/（领取）。
-- Recovery: 停服 → 一致性备份恢复到新的 state 目录 → 配对旧二进制。
--   新增 4 表对旧二进制不可见（旧二进制不读）；已发放资产以
--   cn35_adventure_claims 与 cn35_user_presents 对账。进度类数据可清空重建。
-- 本迁移只追加：不改 0035 及更早 SQL；不新增档案列；不写共享 fixture。

-- 1) 轮次配置（运营排期模板）：一轮 = 轮长 + 蛇形地图折线 + 每日奖励表。
--    reward_config_id 直接下发给客户端（LoginAdventurePage.cs:234 ParseInt 必填）；
--    原服取值域未证，重建口径为显式正整数（1 起）。
CREATE TABLE cn35_adventure_configs (
    reward_config_id     INTEGER PRIMARY KEY CHECK (reward_config_id > 0),
    duration_days        INTEGER NOT NULL CHECK (duration_days BETWEEN 1 AND 60),
    -- 地图折线坐标：x0,y0,x1,y1…（客户端 GeneratePath/GetPosByRatio 消费；
    -- JSON 数组文本，解析在 cn35game.AdventureConfig 装配处）。
    path_json            TEXT NOT NULL CHECK (length(path_json) > 1),
    background_override  TEXT NOT NULL DEFAULT '',
    stamper_override     TEXT NOT NULL DEFAULT '',
    right_title_overrdie TEXT NOT NULL DEFAULT '',
    path_indicator       TEXT NOT NULL DEFAULT '',
    -- 轮次滚动顺序（小在前）；同序按 reward_config_id 升序。
    sort                 INTEGER NOT NULL DEFAULT 0,
    enabled              INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
) STRICT;

CREATE INDEX idx_cn35_adventure_configs_enabled
    ON cn35_adventure_configs (enabled, sort, reward_config_id);

-- 2) 每日节点：第 day 天的展示形状（title/pos/special/scale/rewards[0]）
--    与发放内容（礼物盒 content_type/content_id/amount）。
--    pos 为**地图折线累计长度的归一化比例**（GetPosByRatio 口径 0..1），
--    不是格子序号——重建种子按 defaultPath 的 10 个节点累计比例填。
CREATE TABLE cn35_adventure_days (
    reward_config_id    INTEGER NOT NULL REFERENCES cn35_adventure_configs(reward_config_id) ON DELETE CASCADE,
    day                 INTEGER NOT NULL CHECK (day BETWEEN 1 AND 60),
    title               TEXT NOT NULL DEFAULT '',
    pos                 REAL NOT NULL CHECK (pos >= 0 AND pos <= 1),
    special             INTEGER NOT NULL DEFAULT 0 CHECK (special IN (0, 1)),
    scale               REAL NOT NULL DEFAULT 1 CHECK (scale > 0),
    -- 展示形状（对应客户端 SingleReward：type_id/type_name/content_id/count/extend.unit_rare）
    display_type_id     INTEGER NOT NULL DEFAULT 0 CHECK (display_type_id >= 0),
    display_type_name   TEXT NOT NULL CHECK (length(display_type_name) > 0),
    display_content_id  INTEGER NOT NULL DEFAULT 0,
    display_count       INTEGER NOT NULL CHECK (display_count > 0),
    display_unit_rare   INTEGER NOT NULL DEFAULT 0 CHECK (display_unit_rare >= 0),
    -- 实际发放（cn35_user_presents 行内容；content_id 须 >0，见 0015 CHECK）
    grant_type          TEXT NOT NULL CHECK (grant_type IN ('coin', 'medal', 'gacha_point', 'item', 'unit', 'monster')),
    grant_content_id    INTEGER NOT NULL CHECK (grant_content_id > 0),
    grant_amount        INTEGER NOT NULL CHECK (grant_amount > 0),
    PRIMARY KEY (reward_config_id, day)
) STRICT;

-- 3) 账号当前轮次（进度位图由 cn35_adventure_claims 派生，不冗余存位图）。
--    begin_date 为**东八区自然日**（YYYY-MM-DD）：客户端 CalculateDays 以
--    begin_date_i（本日 0 点 Unix 秒）到 server_date_i 的自然日差算 currentDay。
--    last_shown_date 承载 `today_days` 当日一次性口径（同日只弹一次）。
CREATE TABLE cn35_adventure_user_rounds (
    user_id          TEXT PRIMARY KEY REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    round_index      INTEGER NOT NULL CHECK (round_index >= 0),
    reward_config_id INTEGER NOT NULL,
    begin_date       TEXT NOT NULL CHECK (length(begin_date) = 10),
    duration_days    INTEGER NOT NULL CHECK (duration_days BETWEEN 1 AND 60),
    last_shown_date  TEXT NOT NULL DEFAULT '',
    updated_at       TEXT NOT NULL
) STRICT;

-- 4) 发放台账：(user_id, round_index, day) 主键兜底并发与重放——
--    同轮同日只有一次 INSERT 成功，发放内容池行与之同事务（红线：
--    跨域写入与幂等记录必须同事务）。present_uuid 供资产对账回指。
CREATE TABLE cn35_adventure_claims (
    user_id          TEXT NOT NULL REFERENCES cn35_accounts(user_id) ON DELETE RESTRICT,
    round_index      INTEGER NOT NULL CHECK (round_index >= 0),
    day              INTEGER NOT NULL CHECK (day BETWEEN 1 AND 60),
    reward_config_id INTEGER NOT NULL,
    granted_at       INTEGER NOT NULL CHECK (granted_at > 0),
    grant_type       TEXT NOT NULL,
    grant_content_id INTEGER NOT NULL CHECK (grant_content_id > 0),
    grant_amount     INTEGER NOT NULL CHECK (grant_amount > 0),
    present_uuid     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, round_index, day)
) STRICT;

CREATE INDEX idx_cn35_adventure_claims_user_round
    ON cn35_adventure_claims (user_id, round_index);

-- 5) 种子：两套轮次配置（滚动时轮到下一套 ⇒ 「新世界」有新奖励表）。
--    数值一律为**重建运营初值**（非原服取证），与既有经济面同量级：
--    月签到钻石 2000/月、面包店 100/日、金币 25000～300000 量级。
--    展示图标只用资产清单里确实存在的 key（common_icon/diamond|medal|gold|
--    item_1..4|common/scout_book），避免页面出现空白贴图。
INSERT INTO cn35_adventure_configs
    (reward_config_id,duration_days,path_json,background_override,
     stamper_override,right_title_overrdie,path_indicator,sort,enabled,created_at,updated_at)
VALUES
    (1,10,'[-186,-27,-186,87,-93,87,-93,-27,0,-27,0,87,93,87,93,-27,186,-27,186,87]',
     '','','','',1,1,'2026-10-26T00:00:00Z','2026-10-26T00:00:00Z'),
    (2,10,'[-186,-27,-186,87,-93,87,-93,-27,0,-27,0,87,93,87,93,-27,186,-27,186,87]',
     '','','','',2,1,'2026-10-26T00:00:00Z','2026-10-26T00:00:00Z');

-- 第 1 套：常规日 钻石/徽章/金币 交替，特别日（第 5/10 天）加倍，
-- 第 9 天起给书（item 1..4 图标 = 觉醒书 101..104 的既有贴图）。
INSERT INTO cn35_adventure_days
    (reward_config_id,day,title,pos,special,scale,display_type_id,display_type_name,
     display_content_id,display_count,display_unit_rare,grant_type,grant_content_id,grant_amount)
VALUES
    (1,1, '第1天', 0.0,      0,1.0, 1,'coin',        0,30,  0, 'coin',        1,30),
    (1,2, '第2天', 0.121019, 0,1.0, 2,'medal',       0,20,  0, 'medal',       1,20),
    (1,3, '第3天', 0.219745, 0,1.0, 3,'gacha_point', 0,3000,0, 'gacha_point', 1,3000),
    (1,4, '第4天', 0.340764, 0,1.0, 4,'item',        1,1,   0, 'item',        101,1),
    (1,5, '第5天', 0.439490, 1,1.15,5,'coin',        0,100, 0, 'coin',        1,100),
    (1,6, '第6天', 0.560510, 0,1.0, 6,'item',        2,1,   0, 'item',        102,1),
    (1,7, '第7天', 0.659236, 0,1.0, 7,'gacha_point', 0,8000,0, 'gacha_point', 1,8000),
    (1,8, '第8天', 0.780255, 0,1.0, 8,'item',        3,1,   0, 'item',        103,1),
    (1,9, '第9天', 0.878981, 0,1.0, 9,'unit',       -1,1,   3, 'item',        102,1),
    (1,10,'第10天',1.0,      1,1.15,10,'unit',      -1,1,   4, 'item',        103,1);

-- 第 2 套（滚动后的「新世界」）：同一张地图、更高的奖励表。
INSERT INTO cn35_adventure_days
    (reward_config_id,day,title,pos,special,scale,display_type_id,display_type_name,
     display_content_id,display_count,display_unit_rare,grant_type,grant_content_id,grant_amount)
VALUES
    (2,1, '第1天', 0.0,      0,1.0, 1,'gacha_point', 0,5000, 0, 'gacha_point', 1,5000),
    (2,2, '第2天', 0.121019, 0,1.0, 2,'coin',        0,50,   0, 'coin',        1,50),
    (2,3, '第3天', 0.219745, 0,1.0, 3,'item',        3,1,    0, 'item',        103,1),
    (2,4, '第4天', 0.340764, 0,1.0, 4,'medal',       0,50,   0, 'medal',       1,50),
    (2,5, '第5天', 0.439490, 1,1.15,5,'coin',        0,150,  0, 'coin',        1,150),
    (2,6, '第6天', 0.560510, 0,1.0, 6,'item',        4,1,    0, 'item',        104,1),
    (2,7, '第7天', 0.659236, 0,1.0, 7,'gacha_point', 0,12000,0, 'gacha_point', 1,12000),
    (2,8, '第8天', 0.780255, 0,1.0, 8,'medal',       0,100,  0, 'medal',       1,100),
    (2,9, '第9天', 0.878981, 0,1.0, 9,'item',        4,1,    0, 'item',        104,1),
    (2,10,'第10天',1.0,      1,1.15,10,'unit',      -1,1,    5, 'item',        104,1);
