-- 公会解散级联删除参战行时保留月奖分数；不能追溯修复迁移前已删除历史。
CREATE TABLE cn35_guild_season_history (
 session_id TEXT NOT NULL, guild_id TEXT NOT NULL, gp INTEGER NOT NULL,
 PRIMARY KEY(session_id,guild_id)
);
CREATE TRIGGER cn35_guild_season_preserve BEFORE DELETE ON cn35_gw_participants
BEGIN
 INSERT INTO cn35_guild_season_history(session_id,guild_id,gp) VALUES(OLD.session_id,OLD.guild_id,OLD.gp)
 ON CONFLICT(session_id,guild_id) DO UPDATE SET gp=excluded.gp;
END;
