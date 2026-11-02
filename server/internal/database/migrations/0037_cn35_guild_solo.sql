-- 0037_cn35_guild_solo.sql：公会生命周期补齐 + 单机 NPC 公会域（GUILDLIFE350-03..07）。
--
-- 需求（用户 2026-10-27）：按 notes/proposed/2026-10-27-cn35-guild-lifecycle-solo.md
-- 完成服务端开发：真实公会组织 CRUD 缺口补齐 + 可关闭的「19/20 NPC 公会」池、
-- 免审加入、持久成员与防卫、单机公会战闭环。
--
-- 设计边界（红线：未验证的推断不得写成事实）：
--   - NPC 成员**不建** cn35_accounts 行、不进玩家资产/奖励链，独立身份表
--     cn35_npc_identities + 成员表 cn35_guild_npc_members；因此
--     cn35_guild_members 的 user_id（真实账号）语义保持不变。
--   - 候选公会池/对手公会全部是**重建运营初值**（19+1 席位、名称/等级/防卫
--     数值），不是原服取证数据。客户端契约只约束字段形状（
--     docs/协议对接/cn350/公会战-客户端契约取证.md §2/§3）。
--   - 只追加：不改 0027/0028/0029 等已合并 SQL；cn35_guilds 通过 ADD COLUMN
--     扩列（source/pool_role/pool_batch），旧行默认 'player'/''/0 保持现状。
--
-- Affected routes: /guilds/ /guilds/search.json /guilds/show /guilds/join
--   /guilds/dropout /guild_rankings /battles/*（会战匹配与结算）。
-- Recovery: 关闭开关（-guild-solo=false 或 solo_state.enabled=0）后既有 NPC
--   公会仍可读/可退出，不删除真人数据；回退程序前按 docs/后端数据库与Go工作流.md
--   用一致性备份恢复到新 state 目录。

-- 公会来源与池角色：player 为真人自建（历史行全部默认值），npc 为重建 NPC 域。
ALTER TABLE cn35_guilds ADD COLUMN source TEXT NOT NULL DEFAULT 'player';
ALTER TABLE cn35_guilds ADD COLUMN pool_role TEXT NOT NULL DEFAULT '';
-- 池批次：有界补充每批 +1，用于「耗尽后补充」审计与上限判定。
ALTER TABLE cn35_guilds ADD COLUMN pool_batch INTEGER NOT NULL DEFAULT 0;

-- 单机 NPC 域开关与配额的持久化状态（单行）。运行时以进程配置为准覆盖
-- 开关/配额，但状态落库使「关闭后不再补池」跨重启一致。
CREATE TABLE IF NOT EXISTS cn35_guild_solo_state (
  id              INTEGER PRIMARY KEY CHECK(id = 1),
  enabled         INTEGER NOT NULL DEFAULT 1,
  candidate_count INTEGER NOT NULL DEFAULT 3,   -- 期望的「有空位候选公会」数
  opponent_count  INTEGER NOT NULL DEFAULT 3,   -- 期望的持久对手公会数
  max_guilds      INTEGER NOT NULL DEFAULT 24,  -- 池总量上限（有界补充）
  seq             INTEGER NOT NULL DEFAULT 0,   -- 池批次序号（guild_id/npc_id 来源）
  updated_at      TEXT NOT NULL DEFAULT ''
);
INSERT INTO cn35_guild_solo_state (id, enabled, candidate_count, opponent_count,
  max_guilds, seq, updated_at) VALUES (1, 1, 3, 3, 24, 0, '');

-- NPC 独立身份：稳定 npc_id + 展示名/等级/头像单位，不映射到账号。
CREATE TABLE IF NOT EXISTS cn35_npc_identities (
  npc_id          TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  level           INTEGER NOT NULL DEFAULT 1,
  leader_unit_id  TEXT NOT NULL DEFAULT '',   -- square_icon_asset_name=unit/unit_<id>
  detail_message  TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL
);

-- NPC 成员：一个 npc_id 只属于一个公会（唯一索引），role 语义与真人成员一致
-- （1 成员 / 2 副长 / 3 会长）。
CREATE TABLE IF NOT EXISTS cn35_guild_npc_members (
  guild_id   TEXT NOT NULL REFERENCES cn35_guilds(guild_id) ON DELETE CASCADE,
  npc_id     TEXT NOT NULL REFERENCES cn35_npc_identities(npc_id),
  role       INTEGER NOT NULL CHECK(role IN (1,2,3)),
  joined_at  TEXT NOT NULL,
  PRIMARY KEY (guild_id, npc_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS cn35_guild_npc_members_identity
  ON cn35_guild_npc_members(npc_id);
CREATE INDEX IF NOT EXISTS cn35_guild_npc_members_guild
  ON cn35_guild_npc_members(guild_id);

-- NPC 防卫：不写 cn35_player_monsters（魔宠属于真人账号），独立存
-- (guild,npc,slot) → 模板 id + 等级；会战防卫快照按此表合成。
CREATE TABLE IF NOT EXISTS cn35_guild_npc_defense (
  guild_id   TEXT NOT NULL,
  npc_id     TEXT NOT NULL,
  slot       INTEGER NOT NULL CHECK(slot BETWEEN 0 AND 4),
  monster_id INTEGER NOT NULL,
  level      INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (guild_id, npc_id, slot),
  FOREIGN KEY (guild_id, npc_id)
    REFERENCES cn35_guild_npc_members(guild_id, npc_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS cn35_guild_npc_defense_guild
  ON cn35_guild_npc_defense(guild_id);

-- 会战奖励发放台账：同一场次同一玩家同一类别只发一次（跨公会去重，防换会刷奖）。
-- 结算发放（kind='settlement_win'）与出战胜利奖励同事务写台账。
CREATE TABLE IF NOT EXISTS cn35_gw_reward_claims (
  session_id TEXT NOT NULL,
  user_id    TEXT NOT NULL,
  kind       TEXT NOT NULL,
  amount     INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (session_id, user_id, kind)
);
