-- 0038_cn35_guild_bp_storage.sql：公会 BP 蓄水库余额与流水（GUILDCLOSE350 GC-03）。
--
-- 证据真源（只读、不改写）：artifacts/tasks/cn350-guild-live-closure-20261028/run/evidence-guild-storage.md
--   decrypted/1/guilds.json                    data.guild.equipments.bp_storage
--                                             （value=49000 上限 @ Lv.79、value2=0.656 填充比例）
--   decrypted/1/guilds_bp_storage.json         确认态/拒绝态 modal（34 条确认、2 条拒绝）
--   decrypted/1/guilds_takeover_bp_storage.json 取出成功 32 条（每次「取出了600BP」）
--
-- 设计边界（未验证的推断不得写成事实）：
--   1. 上限 capacity **不落库**：由 cn35_guild_equipment 中 bp_storage 的 point
--      经重建曲线推导（value=number 是效果主值）。原服只给了单点
--      (Lv.79 → 49000)，故 capacity = round100(620×level) 是〔推断〕重建规则，
--      不是原服常量表；更换曲线只需改代码，不需要再迁移。
--   2. 余额是**公会共享**状态（原服 confirm 余额在会话内被第三方成员同时扣减：
--      出现 -1189/-1195 这类非整 600 残差），因此以 guild_id 为主键一行一会，
--      不得按玩家复制。
--   3. 取出/存入的幂等由既有 cn35_guild_ops(user_id, op_key) 在同一事务内承担；
--      本表流水只做审计与「同键重放回放原量」，不承担幂等。
--   4. **不引入任何重置/补货列**：原服补给来源与重置周期未证（见证据 §取证缺口），
--      不把未证规则写成数据库事实。
--
-- Affected routes: /guilds/bp_storage、/guilds/takeover_bp_storage。
-- Recovery: 本迁移只新增表，不触碰既有行；回退程序前按
--   docs/后端数据库与Go工作流.md 做一致性备份并恢复到新 state 目录。

CREATE TABLE IF NOT EXISTS cn35_guild_bp_storage (
  guild_id   TEXT PRIMARY KEY REFERENCES cn35_guilds(guild_id) ON DELETE CASCADE,
  amount     INTEGER NOT NULL DEFAULT 0 CHECK(amount >= 0),
  updated_at TEXT NOT NULL DEFAULT ''
);

-- 蓄水库流水：kind=take（玩家取出）/deposit（存入，重建原语）。
-- op_key 仅用于「同键重放回放原量」的审计读取；唯一性仍由 cn35_guild_ops 保证。
CREATE TABLE IF NOT EXISTS cn35_guild_bp_storage_log (
  log_id       INTEGER PRIMARY KEY AUTOINCREMENT,
  guild_id     TEXT NOT NULL,
  user_id      TEXT NOT NULL DEFAULT '',
  kind         TEXT NOT NULL CHECK(kind IN ('take','deposit')),
  amount       INTEGER NOT NULL CHECK(amount > 0),
  amount_after INTEGER NOT NULL CHECK(amount_after >= 0),
  op_key       TEXT NOT NULL DEFAULT '',
  reason       TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS cn35_guild_bp_storage_log_guild
  ON cn35_guild_bp_storage_log(guild_id, log_id);
CREATE INDEX IF NOT EXISTS cn35_guild_bp_storage_log_user_op
  ON cn35_guild_bp_storage_log(user_id, op_key);
