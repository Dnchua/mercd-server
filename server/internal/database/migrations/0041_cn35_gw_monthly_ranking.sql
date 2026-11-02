-- 0041_cn35_gw_monthly_ranking
-- GC-06 会战月度排名读面（/guild_rankings、/guild_rankings/my_guild）：
-- 榜单由 cn35_gw_sessions(date_key 月度切片) × cn35_gw_participants(gp)
-- 聚合得出，本迁移只追加索引，不改表结构、不回填数据。
-- 说明：迁移编号 0038..0040 保留给并行工作包（T3 蓄水库/设施）；本号
-- 向 Lead 报备后使用（原约定 0041）。
-- 事务由 mercdb 管理；禁止在此添加 BEGIN/COMMIT/PRAGMA。

-- 月份切片与「最近一个场次」查询按 date_key 降序扫描。
CREATE INDEX IF NOT EXISTS cn35_gw_sessions_date_fi
  ON cn35_gw_sessions(date_key, field_index);

-- 单会历史成绩查询（/guild_rankings/my_guild）：按公会取参与场次。
CREATE INDEX IF NOT EXISTS cn35_gw_participants_guild
  ON cn35_gw_participants(guild_id, session_id);
