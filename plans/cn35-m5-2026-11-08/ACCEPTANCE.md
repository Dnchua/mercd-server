# CN3.5 M5 验收与验证规格（ACCEPTANCE.md — 对抗性审查修订版）

## 1. 强门禁（Hard Gates — 任一不满足即不得宣称 M5 完成）

1. **客户端 DLL 零篡改门禁**：
   - 严禁修改 `Assembly-CSharp.dll` 业务逻辑；原始 APK 按 `client/baseline.json` 校验 SHA-256。
2. **潜能系统不恢复门禁**：
   - 运行态 `consts.unit_upgrade_open` 必须为 `false`，客户端 `UserUnitDetail` 不展示右下潜能面板；
   - 写面 `/user_units/upgrade`、`/user_units/dismantle_confirm`、`/passive_skill/*` 必须保持显式拒绝；
   - 读面 `user_units` 保留中性基线字段（`upgrade_level: 1`、`max_upgrade_level`、`passive_skill_slot_count: 0`、`passive_skills: []`），不产生客户端空引用。
3. **融合宠（魔宠融合）全链路门禁**：
   - `consts.monster_evo_open == true`；
   - 基础配方魔宠（`14` 诺滋风、`23` 飞拉、`26` 皮卡、`22` 针蜂、`24` 司坎德）及五系融合宠（`100005..100045`）读面 `formula_id == 1`；
   - Step1 融合蛋培育（`/user_monsters/show_evolve_board` + `/user_monsters/evolve_egg?attr_id=&consume_monster_count=`）、Step2 魔宠融合（`/user_monsters/evolve_monster_select_info?uuid=` + `/user_monsters/evolve?uuids=u1|u2&egg_item_id=`）、Step3 融合宠体型强化（`/user_monsters/rare_monster_category_config` + `/user_monsters/powerup` `is_rare=1` 分支）全链路 SQLite 事务闭环。
4. **`mlk` 请求回放比对门禁（`TestMLKReplayVerify`）**：
   - 所有 M5 涉及路由必须通过与 `../../cn-mercd-backend/artifacts/mlk-capture-201711/decrypted/` 的回放比对校验：
     - 已捕获读面/战斗面（`preset_data`、`user_units`、`user_monsters`、`user_items`、`user_monsters_evolve_select_info`、`quests/execute`、`quests/result`）顶层键、实体字段集合与 JSON 类型 100% 匹配；
     - 写面响应（`sell`、`evolve_egg`、`evolve`、`evolution`、`unit_ability_exchange`）下发的增量实体 100% 通过 `mlk` 实体 Schema 校验器与客户端 `ResponseFilter` / `DataCache.UpdateDataByResponse` 消费契约断言。
5. **SQLite 真实持久化与历史迁移只读门禁**：
   - `git diff server/internal/database/migrations/0001*..0050*` 必须为空；新增表/种子仅允许通过新编号迁移（`0051+`）引入；
   - 重启 `mercd` 进程后，告别、融合蛋、融合宠、融合宠强化、同伴进化、能力互换、符文强化的结果全部保持，不回退。
6. **仓库洁净门禁**：
   - `git status --short` 不得包含 decoded 树、APK、Unity 资源、原始 `mlk` 抓包、运行库、签名或 SQLite `state/` 文件。

---

## 2. 用例矩阵（M501 ~ M510）

> 注：原 `M502`（潜能开启检查）与 `M503`（潜能升级执行）已按「国服不恢复潜能系统」重构为**潜能关闭与写面拒绝验证**。

| 用例 ID | 名称 | 客户端入口与真实交互路径（对齐反编译代码） | 服务端端点与参数契约 | 核心断言 |
|---|---|---|---|---|
| `M501` | 同伴强化与觉醒回归 | `UserUnits` → 点击同伴行 → `UserUnitDetail` → `powerupButton` / `wakeupButton` | `/user_units/powerup?id=<id>&count=<n>`<br/>`/user_units/wakeup?id=<id>&count=<n>` | 等级/觉醒次数提升，扣除金币或钻/书，面板 `hp/at` 与战斗一致 |
| `M502` | 潜能面板关闭与写面拒绝验证 | `UserUnitDetail` 检查 `unit_upgrade_open == false` 下潜能面板隐藏 | `/user_units/upgrade`、`/user_units/dismantle_confirm`、`/passive_skill/*` | 客户端不显示潜能激活按钮；直调写接口返回显式拒绝，DB 无脏写；读面 `upgrade_level == 1` |
| `M503` | 魔宠种子技与融合宠战斗发动 | 编队装备含种子技魔宠（含融合宠）→ 进入关卡 → 点击魔宠头像释放种子技 | `/quests/execute`<br/>`/quests/result` | `cost == sklsp`、`seed_skill_id`、`seed_skill_value` 生效，SP 正常扣除并触发技能效果 |
| `M504` | 同伴告别（`/user_units/sell`） | `UserUnits` 列表 → 点击 `sellButton` → **在「待机所/告别」三键模态点击 `noButton` 进入 `mode="sell"`** → 选择同伴行 → 点击 `decidedButton` → 确认模态（★3+ 二次确认） | `GET\|POST /user_units/sell?ids[]=<id>` | 拒绝队长/助战/受保护同伴；拒绝告别后活动同伴 `< 5`；成功告别后从 DB 删除、卸下已装符文/魔宠、移出编队并增加金币 |
| `M505` | 魔宠告别（`/user_monsters/sell`） | 路径 A：`UserMonsterDetail` → `sellButton` → 确认模态<br/>路径 B：`UserMonsters` 列表 → `sellButton` (`mode="sell"`) → 选行 → `decidedButton` → 确认模态 | `GET\|POST /user_monsters/sell?uuids[]=<uuid>` | 拒绝已装备/受保护/公会防卫魔宠；成功告别后删除对应 `uuid` 行并增加 `rare * 100` 金币 |
| `M506` | 融合宠全链路（进化之卵 + 魔宠融合 + 融合宠强化） | `UserMonsters` 列表 → 点击 `mergeButton` → `MergeSelectCaseModal`<br/>Step1：点击 `mergeEggButton` → `MergeEggUpModal` 合成进化之卵<br/>Step2：点击 `mergeMonsterButton` → 点击 `formula_id > 0` 魔宠行 → `MergeMonsterSelectMaterial` 选蛋 → `MergeMonsteUpModal` 融合<br/>Step3：对融合宠（`is_rare=1`）打开 `MonsterPowerupModal` 强化体型 | `/user_monsters/evolve_select_info`<br/>`/user_monsters/show_evolve_board`<br/>`/user_monsters/evolve_egg?attr_id=&consume_monster_count=`<br/>`/user_monsters/evolve_monster_select_info?uuid=`<br/>`/user_monsters/evolve?uuids=u1\|u2&egg_item_id=`<br/>`/user_monsters/rare_monster_category_config`<br/>`/user_monsters/powerup` | 1★完全体魔宠+神秘水晶+金币合成进化之卵（`101..504`）；基础宠（`14/22/23/24/26`）+蛋融合产出五系融合宠（`100005..100045, is_rare=1, scale_limit`）；融合宠强化按 `+0.01/步` 扣素材与金币推进至 `scale_limit` |
| `M507` | 同伴能力互换（`/unit_ability_exchange/*`） | `UserUnits` 列表 → 点击 `changeAblityButton` → `ChangeAbility` 页 → 点击左/右槽进 `ChangeAbilityUserUnits` 选两名同星级同伴 → 点击确认 → `ChangeAbilityConfirmModal` 确认互换 | `/unit_ability_exchange/get_unit_ability_exchange_info`<br/>`/unit_ability_exchange/select_unit?unit_id=`<br/>`/unit_ability_exchange/get_unit_ability_exchange_result?unit_a_id=&unit_b_id=`<br/>`/unit_ability_exchange/exchange_unit_ability?unit_a_id=&unit_b_id=` | 双键下发 `changeAbilityInfo` 与 `unit_ability_exchange_info`；扣减 `能力互换石 (200016)` 或钻石；原子互换两同伴 `level` 与 `duplicated_count`，`upgrade_level` 保持 `1` |
| `M508` | 同伴进化（`/user_units/evolution`） | `UserUnits` 列表 → 点击 `evolutionButton` (`mode="evolution"`) → 点击 `evolution_map.id > 0` 的同伴行 → `UnitEvolutionModal` → 点击进化 | `GET\|POST /user_units/evolution?id=<id>` | 校验满级与种/蕾/花素材；扣减素材并生成进化后同伴（继承折算觉醒），更新编队/队长/装备引用，返回 `evolution_before` 与 `evolution_after` |
| `M509` | 符文强化/出售/装卸与关卡掉落 | 符文列表强化（`/skills/powerup`）、出售（`/skills/sell`）、同伴详情装卸（`/user_units/skills_Set`）及关卡掉落自动出售 | `/skills/powerup`<br/>`/skills/sell`<br/>`/user_units/skills_Set`<br/>`/quests/result` | 符文 `ratio` 与等级按步进增长，已装备符文禁止出售，装卸与掉落入库正常 |
| `M510` | 重启持久化与 `mlk` 回放一致性总验收 | 重启 `mercd` 后复查 `preset_data`、`user_units`、`user_monsters`、`user_items`、`skills` 并运行 `TestMLKReplayVerify` | `/users/preset_data`<br/>`/user_units`<br/>`/user_monsters`<br/>`/user_items` | 重启前后 SQLite 状态 100% 一致；`TestMLKReplayVerify` 全绿 |
