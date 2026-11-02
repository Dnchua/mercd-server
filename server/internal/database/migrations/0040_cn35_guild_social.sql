-- 0040_cn35_guild_social.sql：GUILDCLOSE350 GC-07/08 公会社交面持久化。
--
-- 需求（2026-10-28）：把「成员原路径 /guilds/users/」「等待邀请池
-- /waiting_users 系列」「会战留言 /battles/comments」从空态/错键升级为
-- 真实持久化读面。原服回包形状见
-- artifacts/mlk-capture-201711/decrypted/1/{guilds_users,waiting_users,battles_comments}.json
-- 与 server/testdata/mlk-live/guild/contract.json。
--
-- 表职责：
--   1) cn35_guild_waiting_users —— 本人「登记等待邀请」的真实队列
--      （客户端 GuildNotJoinedPageBase.cs:83 提交 /waiting_users/add.json，
--      参数 play_type_id=期望方针）。一行一账号，重复登记=更新方针。
--   2) cn35_guild_waiting_candidates —— **单机扩展**：NPC 待邀请候补池。
--      国服原服此列表是真人玩家登记的队列；本机单机域没有第二个真人，
--      以有界持久 NPC 候补填充同一列表（id 命名空间 npccNNNN，与
--      cn35_npc_identities 的 npcNNNNAA 不冲突）。开关取既有
--      cn35_guild_solo_state.enabled（单机域总开关），关闭后不再补池，
--      既有行仍可读。这是**有意差异**，不是原服事实（见证据文件）。
--   3) cn35_battle_comments —— 会战留言。BattleMessages.cs 通过
--      /comments/create?message_type_id=battle_comment&battle_id= 写入，
--      /battles/comments 读回 {"battle_comments":<最新Unix秒>,
--      "battle_comments_<battle_id>":[行]}；行键见 Message.cs（id/text/
--      user_id/user_name/created_at…）。
--
-- 邀请动作复用既有 cn35_guild_subscriptions（kind='invite'，0027/0028 已建
-- 部分唯一索引 pending），不新增邀请表：真人被邀请 → pending invite，
-- 被邀者在 /waiting_users/recruit 看到公会行并走原服 join_approve_url 接受；
-- NPC 候补无法自己应答 → 邀请即入会（cn35_guild_npc_members），单机扩展。
--
-- Affected routes: GET|POST /guilds/users/、/waiting_users、
--   /waiting_users/add.json、/waiting_users/invite/、/waiting_users/recruit、
--   /battles/comments、/comments/create（battle_comment 一类）。
-- Recovery: 停服 → 一致性备份恢复到新 state 目录 → 配对旧二进制；
--   本迁移只新增 3 表，旧二进制不读即忽略。等待队列行可清空重建，
--   会战留言为玩家生成内容，恢复后按表对账。
-- 本迁移只追加：不改 0039 及更早 SQL；不写 BEGIN/COMMIT/PRAGMA；
--   不改既有表结构；不写共享 fixture。

-- 1) 等待邀请真实队列（本人登记）。
CREATE TABLE IF NOT EXISTS cn35_guild_waiting_users (
  user_id      TEXT PRIMARY KEY REFERENCES cn35_accounts(user_id) ON DELETE CASCADE,
  -- play_type_id 取 play_type_select_option 的 value（0 未指定 / 1..4），
  -- 空串=未指定（客户端不传时）。列不进 CHECK：原服选项集合可能扩展，
  -- 取值域由 store 归一化，避免旧 state 升级时被 CHECK 卡死。
  play_type_id TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS cn35_guild_waiting_users_play
  ON cn35_guild_waiting_users(play_type_id, created_at);

-- 2) 单机扩展：NPC 待邀请候补池（有界持久，npc 命名空间 npccNNNN）。
CREATE TABLE IF NOT EXISTS cn35_guild_waiting_candidates (
  npc_id         TEXT PRIMARY KEY,
  name           TEXT NOT NULL,
  level          INTEGER NOT NULL DEFAULT 1,
  leader_unit_id TEXT NOT NULL DEFAULT '',
  play_type_id   TEXT NOT NULL DEFAULT '1',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS cn35_guild_waiting_candidates_play
  ON cn35_guild_waiting_candidates(play_type_id, created_at);

-- 3) 会战留言（battle_comment 一类）。
CREATE TABLE IF NOT EXISTS cn35_battle_comments (
  comment_id TEXT PRIMARY KEY,
  battle_id  TEXT NOT NULL,
  guild_id   TEXT NOT NULL DEFAULT '',
  user_id    TEXT NOT NULL,
  user_name  TEXT NOT NULL DEFAULT '',
  body       TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS cn35_battle_comments_battle
  ON cn35_battle_comments(battle_id, created_at);
CREATE INDEX IF NOT EXISTS cn35_battle_comments_user
  ON cn35_battle_comments(user_id, created_at);

-- 4) 单机扩展独立配置：NPC「待邀请候补池」。
--
-- 为什么单独建配置表（Lead 2026-10-28 拍板）：红线要求「语义有意不同的单机
-- 扩展必须**独立命名配置**并保留原服差异说明」。原服 /waiting_users 是真人
-- 登记队列；本机没有第二个真人，因此以有界持久 NPC 候补兜底，但这条兜底必须
-- 能单独关掉、且不能复用 cn35_guild_solo_state.enabled 当唯一开关：
--   - 生效条件 = solo 域开关(cn35_guild_solo_state.enabled) AND 本表 enabled；
--   - 任一为关 → **不注入** NPC 候选，只返回真实登记队列（可以为空）；
--   - waiting_pool_max 上限 12（防无限建数据），默认 6。
-- NPC 候补只在被邀请入会时才落到 cn35_npc_identities + cn35_guild_npc_members，
-- 不进入玩家资产链（不建账号、不写余额/库存/奖励）。
CREATE TABLE IF NOT EXISTS cn35_guild_social_config (
  id                   INTEGER PRIMARY KEY CHECK (id = 1),
  waiting_pool_enabled INTEGER NOT NULL DEFAULT 1,
  waiting_pool_max     INTEGER NOT NULL DEFAULT 6,
  updated_at           TEXT NOT NULL
);
INSERT OR IGNORE INTO cn35_guild_social_config
  (id, waiting_pool_enabled, waiting_pool_max, updated_at)
  VALUES (1, 1, 6, '');
