-- 0039_cn35_guild_equip_invest.sql：公会设施投资台账扩列（GUILDCLOSE350 GC-04）。
--
-- 背景：0027 的 cn35_guild_invest_log 只记 (guild_id,user_id,type_id,amount,created_at)，
-- 无法回放「这次投资把 point/level 从多少推到多少」。GC-04 要求
-- 「余额条件扣减 + point 增加 + level 重算 + 投资日志」同事务且可审计，
-- 故追加 before/after 快照与幂等键三列（只追加，不改历史 SQL）。
--
-- 只追加纪律：既有 INSERT（4 列）因新列均有 DEFAULT 仍可执行；历史行保持
-- point_before=point_after=0、level_before=level_after=0、op_key=''（表示未采集）。
--
-- 曲线依据（与 0038 同源）：artifacts/tasks/cn350-guild-live-closure-20261028/run/evidence-guild-storage.md
--   等级阈值只与等级有关、与设施类型无关（hp79 与 bp_storage79 的 until 同为 29,928,300）；
--   四个锚点 70,547,000 / 48,190,100 / 29,928,300 / 12,700,000 为重建曲线的过点约束。
--
-- Affected routes: /guilds/invest（以及所有读 equipments 的读面投影）。
-- Recovery: 只新增列与索引，旧程序可在本迁移后继续运行（新列走默认值）。

ALTER TABLE cn35_guild_invest_log ADD COLUMN point_before INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cn35_guild_invest_log ADD COLUMN point_after  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cn35_guild_invest_log ADD COLUMN level_before INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cn35_guild_invest_log ADD COLUMN level_after  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cn35_guild_invest_log ADD COLUMN op_key       TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS cn35_guild_invest_guild_type
  ON cn35_guild_invest_log(guild_id, type_id, created_at);
