-- 单机重建经济，默认关闭；不代表原服规则。见 2026-10-29 audit-remediation Note。
CREATE TABLE cn35_guild_economy_config (
 id INTEGER PRIMARY KEY CHECK(id=1),
 refill_enabled INTEGER NOT NULL DEFAULT 0 CHECK(refill_enabled IN(0,1)),
 season_enabled INTEGER NOT NULL DEFAULT 0 CHECK(season_enabled IN(0,1))
);
INSERT INTO cn35_guild_economy_config(id) VALUES(1);
CREATE TABLE cn35_guild_refills (
 guild_id TEXT NOT NULL, day TEXT NOT NULL, amount INTEGER NOT NULL CHECK(amount>=0),
 PRIMARY KEY(guild_id,day)
);
CREATE TABLE cn35_guild_seasons (month TEXT PRIMARY KEY, closed_at TEXT NOT NULL);
CREATE TABLE cn35_guild_season_ranks (
 month TEXT NOT NULL, guild_id TEXT NOT NULL, point INTEGER NOT NULL, rank INTEGER NOT NULL,
 PRIMARY KEY(month,guild_id)
);
-- guild_id 不设外键：解散后仍保留月结归属；user_id 保持真实账号约束。
CREATE TABLE cn35_guild_season_claims (
 month TEXT NOT NULL, user_id TEXT NOT NULL REFERENCES cn35_accounts(user_id),
 guild_id TEXT NOT NULL, rank INTEGER NOT NULL,
 gacha_point INTEGER NOT NULL, medal INTEGER NOT NULL, guild_asset INTEGER NOT NULL,
 skill_uuid TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(month,user_id)
);
