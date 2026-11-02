-- cn35_player_profiles.memo：玩家个人主页签名（/users/update_memo 写面）。
--
-- 契约依据（客户端 ilspy-cs 反编译）：
--   UserPage.cs:292 memoInput.text = userTemp.s("memo", string.Empty)；
--   UserPage.cs:629-639 OnMemoChange → POST /users/update_memo (memo=<text>)。
--   未自定义签名（空串 ''）时由 wire 层 ApplyDefaultUserWireFields 下发默认文案
--   "请多多指教！"；自定义后下发落库值。
--
-- Affected routes: /users/update_memo、/users/show、/users/preset_data.json。
-- Recovery: 停服 → 备份副本恢复到新 state 目录 → 配对旧二进制；本迁移只增列、
--   默认 '' 不回填，不触碰既有账号数据。
ALTER TABLE cn35_player_profiles
    ADD COLUMN memo TEXT NOT NULL DEFAULT '';
