# CN3.5 M5 任务清单（TASKS.md — 对抗性审查修订版）

每个任务卡均包含：`id`、`title`、`goal`、`depends_on`、`inputs`、`deliverables`、`out_of_scope`、`client_contract`、`verification`、`evidence_paths`、`rollback`。

---

## T01：mlk 抓包离线提取与回放基准建立

- **id**: `T01`
- **title**: `mlk` 抓包离线提取、客户端契约映射与回放校验基准
- **goal**: 在不把原始 `mlk` 抓包入库的前提下，从 `../../cn-mercd-backend/artifacts/mlk-capture-201711/decrypted/` 提取 M5 涉及路由的只读 Schema 与字段契约基准，并核对客户端反编译代码（`Assembly-CSharp.dll`）中所有 M5 养成与战斗接口的请求参数、缓存键与响应消费逻辑。
- **depends_on**: `[]`
- **inputs**:
  - `../../cn-mercd-backend/artifacts/mlk-capture-201711/decrypted/`（只读外部抓包）
  - `client/decoded/assets/bin/Data/Managed/Assembly-CSharp.dll`
  - `docs/03-页面结构与交互全集.md`、`docs/04-成长面板与战斗数值.md`、`docs/05-战斗文档字段消费.md`、`docs/10-业务端点与持久化.md`
- **deliverables**:
  - M5 路由与 `mlk` 抓包字段契约对照表（重点覆盖：`user_monsters_evolve_select_info`、`user_monsters` 中 `formula_id`/`batch`/`is_rare`/`scale_limit`、`user_units` 中 `evolution_map`/`upgrade_level`、`user_items` 与 `user` 中 `evolve_items`/`evolve_fancy_items`/`200016 能力互换石`）。
  - 确认 `git status` 未引入任何原始抓包文件。
- **out_of_scope**:
  - 不修改任何 `.sql` 迁移历史文件；不拷贝原始 `.body` / `.pcap` / 含玩家隐私的原始 JSON 入库。
- **client_contract**:
  - `DataCache.cs:379`（`consts.monster_evo_open`）、`UserUnitDetail.cs:235`（`consts.unit_upgrade_open`）、`UnitButtonsListComponent.cs:488`（`consts.changeability_switch`）。
- **verification**:
  ```bash
  git status --short
  ```
- **evidence_paths**:
  - `plans/cn35-m5-2026-11-08/PLAN.md`
- **rollback**:
  - 纯只读取证与计划文档修订，无代码状态变更。

---

## T02：M1 ~ M4 回归门禁验证

- **id**: `T02`
- **title**: M1 ~ M4 服务端单元回归与启动门禁
- **goal**: 在动手修改 M5 业务代码前，确保当前仓库 Go 单元测试全绿，确认基线健康。
- **depends_on**: `["T01"]`
- **inputs**:
  - `server/internal/cn35game/`、`server/internal/cn35store/`、`server/internal/statmodel/`
- **deliverables**:
  - Go 单元测试全绿执行记录。
- **out_of_scope**:
  - 不修改既有通过测试的断言来掩盖失败。
- **client_contract**:
  - 既有 M1~M4 协议契约保持不变。
- **verification**:
  ```bash
  cd server && go test ./internal/cn35game/... ./internal/cn35store/... ./internal/statmodel/... -count=1
  ```
- **evidence_paths**:
  - 测试终端输出日志。
- **rollback**:
  - 若基线失败先定位原因，不带病进入 Phase B/C。

---

## T03：潜能系统关闭与数值来源标签化

- **id**: `T03`
- **title**: 潜能系统写面关闭、读面中性兼容与四分类来源标签建立
- **goal**:
  1. 落实「国服不恢复潜能系统」：运行态 `consts.unit_upgrade_open` 设为 `false`，保持 `/user_units/upgrade`、`/user_units/dismantle_confirm`、`/passive_skill/*` 写面显式拒绝；
  2. 保持 `user_units` 读面下发中性基线字段（`upgrade_level: 1`、`max_upgrade_level`、`passive_skill_slot_count: 0`、`passive_skills: []`），确保 `mlk` 回放比对与客户端 `UserUnitDetail` / `ChangeAbilityParts` 不报空指针；
  3. 为 M5 涉及的融合宠、同伴进化、能力互换、种子技、符文数据建立来源标签（`original_observation` / `client_contract` / `community` / `reconstruction`）。
- **depends_on**: `["T01", "T02"]`
- **inputs**:
  - `server/assets/cn350/serve_preset_data.json`
  - `server/internal/cn35game/inventory_wire.go`
  - `server/internal/cn35game/unit_ops_wire.go`
- **deliverables**:
  - `consts` 门控配置与单测断言（`unit_upgrade_open: false`、`monster_evo_open: true`、`changeability_switch: true`）；
  - `docs/04-成长面板与战斗数值.md` 与 `docs/10-业务端点与持久化.md` 中的潜能策略说明与来源标签。
- **out_of_scope**:
  - 不实现 `/user_units/upgrade` 潜能升级写操作，不恢复天赋树叶激活潜能或被动技抽取。
- **client_contract**:
  - `UserUnitDetail.cs:235`：`if (!SingletonAppMonoBehaviour<Common>.instance.consts.b("unit_upgrade_open"))` 隐藏右下潜能面板；
  - `ChangeAbilityParts.cs:270-285`：读取 `upgrade_level` 显示在能力互换面板（恒为 `1`）。
- **verification**:
  ```bash
  cd server && go test ./internal/cn35game/... -run "Test.*Preset|Test.*Upgrade" -v
  ```
- **evidence_paths**:
  - `server/internal/cn35game/inventory_wire.go`
  - `docs/04-成长面板与战斗数值.md`
- **rollback**:
  - `git checkout -- server/internal/cn35game/ docs/`

---

## T04：魔宠种子技（Seed Skill）真实字段下发与战斗消费闭环

- **id**: `T04`
- **title**: 魔宠种子技（Seed Skill）读面、关卡下发与战斗发动闭环
- **goal**: 校核并完善 `user_monsters` 与 `/quests/execute` 中魔宠（含普通魔宠与五系融合宠 `100005..100045`）的 `seed_skill_id`、`seed_skill_value`、`seed_skill_value_20`、`seed_skill_value_30`、`sklsp`/`cost`、`call_interval_ms` 等字段，使其与 `mlk` 抓包及客户端 `SeedControllerPanel.cs`、`QuestInitializer.cs` 完全对齐。
- **depends_on**: `["T03"]`
- **inputs**:
  - `server/internal/cn35game/inventory_wire.go`
  - `server/assets/shared/cards/monsters.json`
  - `server/internal/cn35game/memorial_masterdata.json`
- **deliverables**:
  - 普通宠与融合宠种子技参数解析与下发逻辑（特别注意 `monsters.json` 中融合宠部分档位 `sklsp: 0` 或 `nnn%` 占位文本的补齐，对齐 `mlk` 抓包实证值，如 `100006 摩布拉`、`100011 赫黎安提斯`、`100019 洛菲阿斯`）；
  - 种子技字段单测与 `/quests/execute` 契约测试。
- **out_of_scope**:
  - 不修改客户端战斗 DLL。
- **client_contract**:
  - `SeedControllerPanel.cs` 读取 `cost`（SP 消耗）、`call_interval_ms`（冷却毫秒）、`seed_skill_id`、`seed_skill_value`；
  - `UserMonsterDetail.cs:205` 读取 `sklcd`、`sklsp`、`skill_name`、`skill_description`。
- **verification**:
  ```bash
  cd server && go test ./internal/cn35game/... -run "Test.*Monster.*Seed|Test.*QuestExecute" -v
  ```
- **evidence_paths**:
  - `server/internal/cn35game/inventory_wire.go`
- **rollback**:
  - `git checkout -- server/internal/cn35game/inventory_wire.go`

---

## T05：符文（Skill Rune）强化/出售/装卸与关卡掉落全链路验证

- **id**: `T05`
- **title**: 符文（Skill Rune）强化、出售、装卸与关卡掉落 SQLite 持久化闭环
- **goal**: 校核并补强 `/skills/powerup`、`/skills/sell`、`/user_units/skills_Set` 及 `/quests/execute` + `/quests/result` 符文掉落与自动出售（`auto_sell_skill_categories`）全链路，确保所有字段与 `mlk` 抓包 `skills` 结构及客户端 `Skills.cs`、`SkillPowerupModal.cs`、`EquipmentSkills.cs` 完全对齐。
- **depends_on**: `["T03"]`
- **inputs**:
  - `server/internal/cn35game/skills.go`、`server/internal/cn35game/skills_wire.go`
  - `server/internal/cn35store/skills.go`
- **deliverables**:
  - 符文强化、出售、装卸、掉落及重启持久化的完整单测与边界校验（含已装备于同伴身上的符文禁止出售、同伴告别时符文自动卸下归还背包）。
- **out_of_scope**:
  - 不涉及潜能被动技（`passive_skills`）。
- **client_contract**:
  - `SkillPowerupModal.cs`：`/skills/powerup?uuid=<uuid>&powerup_item_id=<id>&count=<n>`；
  - `Skills.cs`：`/skills/sell?uuids[]=<uuid>`；
  - `EquipmentSkills.cs`：`/user_units/skills_Set?unit_id=<id>&skill_uuids[]=<uuid>`。
- **verification**:
  ```bash
  cd server && go test ./internal/cn35game/... ./internal/cn35store/... -run "Test.*Skill" -v
  ```
- **evidence_paths**:
  - `server/internal/cn35game/skills_wire.go`
  - `server/internal/cn35store/skills.go`
- **rollback**:
  - `git checkout -- server/internal/cn35game/skills* server/internal/cn35store/skills*`

---

## T06：同伴告别（`/user_units/sell`）与魔宠告别（`/user_monsters/sell`）写面实装

- **id**: `T06`
- **title**: 同伴告别与魔宠告别 SQLite 事务实装（严格对齐客户端参数与约束）
- **goal**: 将 `/user_units/sell` 与 `/user_monsters/sell` 从只读拒絕（`unsupported_write`）升级为完整 SQLite 事务写面，严格对齐客户端 `UnitButtonsListComponent.cs`、`MonsterButtonsListComponent.cs`、`UserMonsterDetail.cs` 的参数格式、前置守卫与联动清理。
- **depends_on**: `["T02", "T05"]`
- **inputs**:
  - `server/internal/cn35game/unit_ops_wire.go`、`server/internal/cn35game/monster_ops_wire.go`
  - `server/internal/cn35store/unit_ops.go`、`server/internal/cn35store/monster_ops.go`
- **deliverables**:
  - **同伴告别（`GET|POST /user_units/sell`）**：
    - 解析参数 `ids[]=<unit_id>`（兼容多选批量告别，去重且顺序无关）；
    - **前置守卫**：
      1. 必须归属当前玩家且处于活动同伴列表（非待机所）；
      2. 禁止出售当前队长（`leader_unit_id`）、助战同伴（`helper_unit_id`）及受保护同伴（`protected == true`）；
      3. **剩余活动同伴数守卫**：出售后玩家活动同伴总数必须 `>= 5`（对齐 `UnitButtonsListComponent.cs:820` `if (num3 < 5)`）；
    - **联动清理与结算（单事务 `withTx`）**：
      1. 若被告别同伴在编队（`cn35_player_decks`）中，自动从编队槽位移除；
      2. 若被告别同伴装备了符文（`cn35_player_skills.unit_id`）或魔宠（`cn35_player_monsters.unit_id`），自动卸下归还背包（置 `unit_id = NULL`）；
      3. 计算返还金币（`price = (2 * level - 1) * rare * 100`）与徽章（如有覚醒/星级返还规则，按客户端弹窗预览一致结算），写入玩家余额；
      4. 删除 `cn35_player_units` 行；
      5. 响应下发更新后的 `user`、`user_units`、`user_decks`、`user_monsters`、`skills`。
  - **魔宠告别（`GET|POST /user_monsters/sell`）**：
    - 解析参数 `uuids[]=<uuid>`（支持 `UserMonsterDetail.cs:453` 单只告别与 `MonsterButtonsListComponent.cs:625` 列表批量告别）；
    - **前置守卫**：
      1. 必须归属当前玩家；
      2. 禁止出售已装备在同伴身上（`unit_id != NULL`）、受保护（`protected == true`）或配置在公会防卫槽（`cn35_guild_defense_monsters`）中的魔宠；
    - **结算与持久化（单事务 `withTx`）**：
      1. 按 `price = rare * 100` 增加玩家金币（`gacha_point`）；
      2. 删除 `cn35_player_monsters` 对应 `uuid` 行；
      3. 响应下发更新后的 `user`、`user_monsters`。
- **out_of_scope**:
  - 不修改客户端确认弹窗逻辑。
- **client_contract**:
  - `UnitButtonsListComponent.cs:694-714`：点击 `sellButton` 先弹三键模态（`yes` = 待机所 `mode="wait"`，`no` = 告别 `mode="sell"`）；
  - `UnitButtonsListComponent.cs:767-872`：`/user_units/sell?ids[]=<id>&...`，`ResponseFilter` 后刷新列表并切回 `mode="detail"`；
  - `UserMonsterDetail.cs:441-471` & `MonsterButtonsListComponent.cs:586-646`：`/user_monsters/sell?uuids[]=<uuid>&...`。
- **verification**:
  ```bash
  cd server && go test ./internal/cn35game/... ./internal/cn35store/... -run "Test.*Sell" -v
  ```
- **evidence_paths**:
  - `server/internal/cn35game/unit_ops_wire.go`
  - `server/internal/cn35game/monster_ops_wire.go`
  - `server/internal/cn35store/unit_ops.go`
  - `server/internal/cn35store/monster_ops.go`
- **rollback**:
  - `git checkout -- server/internal/cn35game/ server/internal/cn35store/`

---

## T07：融合宠（魔宠融合 / 进化之卵 / 融合宠强化）全链路恢复

- **id**: `T07`
- **title**: CN3.5 融合宠系统（进化之卵培育 + 五系魔宠融合 + 融合宠体型强化）全链路恢复
- **goal**: 完整恢复国服 CN3.5 魔宠融合系统，使玩家能在魔宠列表点击「融合（`mergeButton`）」，完成 Step1「融合蛋培育（`/user_monsters/evolve_egg`）」、Step2「魔宠融合与融合度/体型上限突破（`/user_monsters/evolve`）」以及融合宠后续的「体型强化（`/user_monsters/powerup` + `rare_monster_category_config`）」。
- **depends_on**: `["T03", "T04", "T06"]`
- **inputs**:
  - 客户端反编译源码：`MonsterButtonsListComponent.cs`、`MergeSelectCaseModal.cs`、`MergeEggUpModal.cs`、`MergeMonsterSelectMaterial.cs`、`MergeMonsteUpModal.cs`、`MonsterPowerupModal.cs`、`ScaleCategory.cs`
  - `mlk` 抓包实证：`user_monsters_evolve_select_info.json`、`user_monsters.json`（`id=26 皮卡` `formula_id: 1`，`100006 摩布拉`、`100011 赫黎安提斯`、`100019 洛菲阿斯`）、`user_items.json`（`id=1 神秘水晶`、`id=102 稀有的火之蛋`）、`users_preset_data.json`（`evolve_items`、`evolve_fancy_items`）
  - 数据库迁移新增文件：`server/internal/database/migrations/0051_cn35_monster_fusion_and_unit_ops.sql`（仅新增，不改历史 SQL）
- **deliverables**:
  1. **功能开关与基础道具目录（SQLite 新迁移 `0051` + `inventory_wire.go` + `accounts.go`）**：
     - `consts.monster_evo_open = true`；
     - `cn35_item_catalog` 补齐：
       - `神秘水晶`：`content_id = 1, content_type_id = 107, item_type = 3, asset_name = "common_icon/evolve_item_1"`；
       - 五系四档进化之卵（共 20 种）：`content_id = 100*attr_id + tier`（`attr_id = 1..5` 火/水/风/光/暗，`tier = 1..4` 普通/稀有/超稀有/传说，如 `101..104`、`201..204`、`301..304`、`401..404`、`501..504`），`content_type_id = 106, item_type = 4, asset_name = "common_icon/evolve_fancy_item_<id>"`；
     - `user` 对象实时同步 `evolve_items: {"1": <count>}` 与 `evolve_fancy_items: {"101": <count>, ...}`；
     - **修复 `buildMonsterDoc` (`inventory_wire.go:516`)**：五只基础配方魔宠（`14` 诺滋风、`23` 飞拉、`26` 皮卡、`22` 针蜂、`24` 司坎德）以及五系融合宠（`100005..100045`）均下发 `formula_id: 1`（对齐 `mlk` 抓包 `id=26 皮卡` 的 `formula_id: 1`），且融合宠根据 `is_rare=1` 下发 `scale_limit`（数值型）与 `category_str`（按 `ScaleCategory` 档位名称：幼体/青年体/完全体/巨大型/超巨大型）。
  2. **入口与分流模态（`GET|POST /user_monsters/evolve_select_info`）**：
     - 100% 原样返回 `mlk` 抓包 `user_monsters_evolve_select_info.json` 载荷（`title: "魔宠融合"`, `egg_btn_label: "融合蛋"`, `monster_btn_label: "选择魔宠"`, `step1_*`, `step2_*`, `help_*`）。
  3. **Step 1：融合蛋看板与合成（`MergeEggUpModal.cs`）**：
     - `GET|POST /user_monsters/show_evolve_board`：返回 `data.show_monster_evo.egg_evo_config`，包含 `gold`（玩家当前金币）及 `"0".."4"` 五系属性页签（对应 `attr_id = 1..5`），每页签含 `name`、`full_scale_monster_count`（该属性未装备、未保护、未上防卫的 1★ 完全体 `scale >= 1.72` 魔宠数量）、`desc1..desc4`、`evo_config`（4 个按钮档位，分别消耗不同数量的 1★ 完全体魔宠 + `神秘水晶` + 金币，对应产出 1★~4★ 进化之卵）；
     - `GET|POST /user_monsters/evolve_egg?attr_id=<1..5>&consume_monster_count=<n>`：
       - 单事务校验并扣减玩家对应属性未装备未保护的 1★ 完全体魔宠 `n` 只、神秘水晶与金币；
       - 产出对应属性的进化之卵（写入玩家道具库存）；
       - 返回 `data.show_monster_evo`（含更新后的 `egg_evo_config` 及 `evo_egg: {is_success: true, egg_asset_name: "common_icon/evolve_fancy_item_<egg_id>", title, desc}`）以及更新后的 `user`、`user_monsters`、`user_items`。
  4. **Step 2：魔宠融合材料选择与执行（`MergeMonsterSelectMaterial.cs` + `MergeMonsteUpModal.cs`）**：
     - 五系融合配方表（对齐 `inventory_wire.go:488-497` 与 `memorial_masterdata.json`）：
       - 火系：`诺滋风 (14)` + `火之蛋 (101..104)` → `黛尼斯托 (100038..100041, batch=1)`
       - 水系：`飞拉 (23)` + `水之蛋 (201..204)` → `摩布拉 (100005..100008, batch=100)`
       - 风系：`皮卡 (26)` + `风之蛋 (301..304)` → `赫黎安提斯 (100009..100012, batch=2)`
       - 光系：`针蜂 (22)` + `光之蛋 (401..404)` → `梅赫莲恩 (100042..100045, batch=4)`
       - 暗系：`司坎德 (24)` + `暗之蛋 (501..504)` → `洛菲阿斯 (100018..100021, batch=3)`
       - 以及同类融合宠互融（`uuids=u1|u2`）提升融合度（`fusion_degrees`，最高 `+15`，每点提升体型上限并在突破档位时晋升阶位 ID）；
     - `GET|POST /user_monsters/evolve_monster_select_info?uuid=<uuid>`：
       - 根据所选魔宠 `uuid` 返回 `data.show_monster_evo`，包含 `use_egg`、`gold`、`require_egg_arr`、`user_evo_egg`、`user_evo_item`、`formulas_hash`（对齐 `MergeMonsterSelectMaterial.cs:96-250` 与 `MergeMonsteUpModal.cs:88-200` 所有字段读取）；
     - `GET|POST /user_monsters/evolve?uuids=<uuid1|uuid2|...>&egg_item_id=<egg_id>`：
       - **注意**：解析竖线 `|` 分隔的 `uuids` 参数（`MergeMonsteUpModal.cs:590`）；
       - 校验主魔宠/副魔宠未装备、未保护且满足完全体（基础宠需完全体 `scale >= 1.72`），扣减进化之卵（若 `egg_item_id > 0`）或副融合宠、神秘水晶与金币；
       - 生成或升级目标融合宠（写入 `is_rare = 1`、对应档位 `monster_id`、`scale = 1.550` 或保留当前体型、计算的 `scale_limit`、融合度）；
       - 返回 `data.show_monster_evo.evo_monster`（`is_success`, `monster_id`, `scale_limit`, `is_up`, `fusion_degrees`, `title`, `desc`）及更新后的 `user`、`user_monsters`、`user_items`。
  5. **Step 3：融合宠体型强化（`MonsterPowerupModal.cs` + `monster_powerup.go`）**：
     - `GET|POST /user_monsters/rare_monster_category_config`：下发 `data.rare_monster_category_config` 数组（涵盖 `ScaleCategory.cs` 新魔宠档位 `id = 6..10` 的 `id`、`powerup_item_cnt`、`gacha_points`）；
     - 升级 `server/internal/cn35store/monster_powerup.go:92`：当 `is_rare == 1` 时不再返回 `ErrMonsterRarePowerup`，而是按当前体型所在新魔宠档位（`1.549~1.678` 幼体 `id=6, form=1`、`1.679~1.808` 青年体 `id=7, form=2`、`1.809~1.938` 完全体 `id=8, form=3`、`1.939~2.068` 巨大型 `id=9, form=3`、`2.069~2.300` 超巨大型 `id=10, form=3`）校验对应属性种/蕾/花、扣减 `steps * powerup_item_cnt` 素材与 `steps * gacha_points` 金币，按 `+0.01/步` 推进 `scale`/`real_scale` 直至 `scale_limit`。
- **out_of_scope**:
  - 不修改客户端 SWF 动画文件与 C# 脚本。
- **client_contract**:
  - `MonsterButtonsListComponent.cs:302, 410-445, 574`
  - `MergeSelectCaseModal.cs:47-122`
  - `MergeEggUpModal.cs:81-460`
  - `MergeMonsterSelectMaterial.cs:90-370`
  - `MergeMonsteUpModal.cs:85-635`
  - `MonsterPowerupModal.cs:45-190` & `ScaleCategory.cs:1-220`
- **verification**:
  ```bash
  cd server && go test ./internal/cn35game/... ./internal/cn35store/... -run "Test.*Monster.*Evo|Test.*RareMonsterPowerup" -v
  ```
- **evidence_paths**:
  - `server/internal/cn35game/monster_ops_wire.go`
  - `server/internal/cn35store/monster_ops.go`
  - `server/internal/cn35store/monster_powerup.go`
  - `server/internal/database/migrations/0051_cn35_monster_fusion_and_unit_ops.sql`
- **rollback**:
  - 恢复迁移前备份的 `state/` DB 并回滚对应 Go 文件。

---

## T08：同伴进化（`/user_units/evolution`）恢复

- **id**: `T08`
- **title**: 同伴进化读面 `evolution_map` 与写面 `/user_units/evolution` 实装
- **goal**: 对齐 `mlk` 抓包 `user_units.json`（44 名可进化同伴携带非空 `evolution_map`）与客户端 `UnitEvolutionModal.cs:143`，恢复同伴进化读面与写面闭环。
- **depends_on**: `["T03", "T06"]`
- **inputs**:
  - `UnitEvolutionModal.cs`、`UnitButtonsListComponent.cs:544-560`
  - `mlk-capture-201711/decrypted/1/user_units.json` 中 `evolution_map` 样本
  - `server/assets/shared/cards/units.json` / `server/assets/cn350/unit_roster_manifest.json`
- **deliverables**:
  - `buildUnitDoc` (`inventory_wire.go:293`)：根据同伴进化映射表下发真实的 `evolution_map: {"id": <next_unit_id>, "need": {"<item_id>": <count>}}` 与 `is_evolutable`（布尔）；
  - `GET|POST /user_units/evolution?id=<unit_id>`：
    - 校验玩家持有该同伴、同伴存在进化目标（`evolution_map.id > 0`）、等级已达当前基础 `max_level`（或满足进化等级条件）、进化所需种/蕾/花素材充足；
    - 单事务扣减素材，将同伴进化为目标 `unit_id`（按原作规则：进化后初始等级 `level = 1`，若进化前有觉醒次数 `duplicated_count > 0`，折算继承一半觉醒次数并赠送图鉴奖励）；
    - 若该同伴在编队、队长、助战或装备了符文/魔宠，同步更新关联的 `unit_id` 引用；
    - 返回 `data.evolution_before`（进化前同伴 doc）、`data.evolution_after`（进化后同伴 doc）以及更新后的 `user_units`、`user_items`、`user`、`user_decks`、`user_monsters`、`skills`。
- **out_of_scope**:
  - 不涉及主线剧情自动进化外的特殊剧情脚本改动。
- **client_contract**:
  - `UnitButtonsListComponent.cs:548`：点击进化模式下拥有 `evolution_map.id != 0` 的同伴行打开 `UnitEvolutionModal`；
  - `UnitEvolutionModal.cs:143-173`：请求 `/user_units/evolution?id=<id>`，从响应 `data` 读取 `evolution_before` 和 `evolution_after` 播放 `evolution.swf` 演出。
- **verification**:
  ```bash
  cd server && go test ./internal/cn35game/... ./internal/cn35store/... -run "Test.*UnitEvolution" -v
  ```
- **evidence_paths**:
  - `server/internal/cn35game/unit_ops_wire.go`
  - `server/internal/cn35store/unit_ops.go`
- **rollback**:
  - `git checkout -- server/internal/cn35game/ server/internal/cn35store/`

---

## T09：同伴能力互换（`/unit_ability_exchange/*`）恢复

- **id**: `T09`
- **title**: 同伴能力互换 4 端点全链路实装（不含潜能、严格对齐客户端双缓存键）
- **goal**: 恢复 `UnitButtonsListComponent` → `ChangeAbility` 页面 → `ChangeAbilityUserUnits` 选人 → `ChangeAbilityConfirmModal` 确认互换的 4 个端点，互换两名同伴的 `level` 与 `duplicated_count`（`upgrade_level` 保持为 `1`）。
- **depends_on**: `["T03", "T06"]`
- **inputs**:
  - `ChangeAbilityParts.cs`、`ChangeAbilityConfirmModal.cs`、`UnitButtonsListComponent.cs:488, 728`
  - `mlk` 抓包 `user_items.json` 中 `id=200016`（`能力互换石`, `code="power_stone"`, `content_type_id=2`）
- **deliverables**:
  1. **道具目录补齐**：在 `cn35_item_catalog` 中注册 `200016` `能力互换石`（`code="power_stone"`, `asset_name="common_icon/item_200016"`）；
  2. **`GET|POST /unit_ability_exchange/get_unit_ability_exchange_info`**：
     - **双键下发**：在 `data` 下同时返回 `changeAbilityInfo`（供 `app_conf` 页面 `requireData` 检查）与 `unit_ability_exchange_info`（供 `ChangeAbilityParts.cs:153, 293, 448` 读取），包含 `stone_count`、`diamond`、`rule_title`、`rule_desc`、`desc`；
  3. **`GET|POST /unit_ability_exchange/select_unit?unit_id=<other_id>`**：
     - 返回 `data.changeAbility_units`：筛选可与 `other_id` 互换的候选同伴列表（排除 `other_id` 自身；若 `other_id > 0`，仅返回同稀有度 `rare` 且 `level > 1 || duplicated_count > 0` 的另一方有效组合），并为每行设置 `change_ability_enable: true`；
  4. **`GET|POST /unit_ability_exchange/get_unit_ability_exchange_result?unit_a_id=<a_id>&unit_b_id=<b_id>`**：
     - 校验两名同伴同星级（`rare` 相同）且至少有一方 `level > 1` 或 `duplicated_count > 0`；
     - 返回 `data.unit_ability_exchange_result: {consume_desc, desc}` 供 `ChangeAbilityConfirmModal.cs:76` 展示；
  5. **`GET|POST /unit_ability_exchange/exchange_unit_ability?unit_a_id=<a_id>&unit_b_id=<b_id>`**：
     - 单事务扣减 `能力互换石`（优先）或钻石，原子互换同伴 A 与同伴 B 的 `level` 与 `duplicated_count`（`upgrade_level` 维持 `1`）；
     - 返回 `data.unit_ability_exchange_succ: true`、`data.unit_ability_exchange_modal: {title, body}`、`data.unit_ability_exchange_units: [unitADoc, unitBDoc]` 以及更新后的 `user_units`、`user`、`user_coin`、`user_items`、`changeAbilityInfo`、`unit_ability_exchange_info`。
- **out_of_scope**:
  - 不互换潜能等级（国服不恢复潜能系统，`upgrade_level` 恒为 `1`）。
- **client_contract**:
  - `ChangeAbilityParts.cs:151-470`
  - `ChangeAbilityConfirmModal.cs:63-204`
- **verification**:
  ```bash
  cd server && go test ./internal/cn35game/... ./internal/cn35store/... -run "Test.*AbilityExchange" -v
  ```
- **evidence_paths**:
  - `server/internal/cn35game/unit_ops_wire.go`
  - `server/internal/cn35store/unit_ops.go`
- **rollback**:
  - `git checkout -- server/internal/cn35game/ server/internal/cn35store/`

---

## T10：`mlk` 请求回放比对校验闭环

- **id**: `T10`
- **title**: `mlk-capture-201711` 请求回放比对与客户端消费契约自动化校验
- **goal**: 在 T04~T09 全部开发完成后，执行针对 `mlk-capture-201711` 的自动化回放比对校验，确保服务端所有读面、战斗面与写面增量响应同 `mlk` 抓包 Schema、字段类型及客户端 `ResponseFilter`/`DataCache` 消费逻辑完全对齐。
- **depends_on**: `["T04", "T05", "T06", "T07", "T08", "T09"]`
- **inputs**:
  - `../../cn-mercd-backend/artifacts/mlk-capture-201711/decrypted/`（通过测试环境变量或相对路径只读读取，不入库）
  - `server/internal/cn35game/` 全部 M5 handler
- **deliverables**:
  - `server/internal/cn35game/mlk_replay_verify_test.go`：
    1. **直接回放比对（Read & Battle Replay Diff）**：回放 `/users/preset_data`、`/user_units`、`/user_monsters`、`/user_items`、`/user_monsters/evolve_select_info`、`/quests/execute`、`/quests/result`，与 `mlk` 解密样本逐键比对：
       - `user_monsters_evolve_select_info` 11 个键值逐字一致；
       - `user_monsters` 行结构（含基础配方宠 `formula_id: 1`、融合宠 `is_rare: true` / `batch` / `scale_limit` / `seed_skill_*`）与 `mlk` 样本字段集和 JSON 类型一致；
       - `user_units` 行结构（含 `evolution_map`、`upgrade_level: 1`、`max_upgrade_level`、`passive_skills: []`）与 `mlk` 样本字段集和 JSON 类型一致；
       - `user` 与 `user_items` 中 `evolve_items`、`evolve_fancy_items`、`power_stone (200016)` 结构与 `mlk` 样本一致；
    2. **写面回放与客户端消费模拟器（Write Contract & Schema Replay）**：
       - 对 `/user_units/sell`、`/user_monsters/sell`、`/user_monsters/show_evolve_board`、`/user_monsters/evolve_egg`、`/user_monsters/evolve_monster_select_info`、`/user_monsters/evolve`、`/user_monsters/rare_monster_category_config`、`/user_monsters/powerup`（稀有宠）、`/user_units/evolution`、`/unit_ability_exchange/*` 执行请求回放；
       - 校验返回的 `user`、`user_units`、`user_monsters`、`user_items` 实体 100% 通过 `mlk` 实体 Schema 校验器，且模拟客户端 `DataCache.UpdateDataByResponse` 与各 Modal 字段提取无缺失、无类型转换异常。
- **out_of_scope**:
  - 不提交 `mlk` 抓包原文入库。
- **client_contract**:
  - `Common.ResponseFilter` + `DataCache.UpdateDataByResponse` + M5 各 Modal/Page 组件字段提取契约。
- **verification**:
  ```bash
  cd server && go test ./internal/cn35game/... -run "TestMLKReplayVerify" -v
  ```
- **evidence_paths**:
  - `server/internal/cn35game/mlk_replay_verify_test.go`
- **rollback**:
  - `git checkout -- server/internal/cn35game/mlk_replay_verify_test.go`

---

## T11：Poco 真机验收 — 告别、融合宠、同伴进化与能力互换

- **id**: `T11`
- **title**: 养成写面与融合宠 Poco 真机端到端验收（M501, M504 ~ M508）
- **goal**: 在先读 `skills/cn35-apk-packaging/SKILL.md` 并确认模式 A（外置服务端 + 普通 APK）的前提下，通过 Poco 真机脚本完成同伴强化/觉醒（M501）、同伴告别（M504）、魔宠告别（M505）、融合宠全链路（M506：融合蛋+魔宠融合+融合宠强化）、同伴能力互换（M507）、同伴进化（M508）及重启持久化（M510）实机验证。
- **depends_on**: `["T10"]`
- **inputs**:
  - `skills/cn35-apk-packaging/SKILL.md`
  - `tools/cn350_poco/m5_acceptance.py`、`tools/cn350_poco/m5_plan.json`、`tools/cn350_poco/m5_selectors.candidate.json`
- **deliverables**:
  - 修正后的 `tools/cn350_poco/m5_plan.json` 与 `m5_selectors.candidate.json`（严格对齐客户端真实入口：列表页 `sellButton` + 「待机所/告别」模态 `noButton`、魔宠列表 `mergeButton`、同伴列表 `changeAblityButton` 与 `evolutionButton`）；
  - 真机截图、Poco 报告与 SQLite 前后持久化快照。
- **out_of_scope**:
  - 不执行已剔除的潜能用例（原 M502/M503）。
- **client_contract**:
  - `UnitButtonsListComponent`、`MonsterButtonsListComponent`、`MergeSelectCaseModal`、`MergeEggUpModal`、`MergeMonsterSelectMaterial`、`MergeMonsteUpModal`、`MonsterPowerupModal`、`UnitEvolutionModal`、`ChangeAbilityParts`、`ChangeAbilityConfirmModal`。
- **verification**:
  ```bash
  python3 -m py_compile tools/cn350_poco/m5_acceptance.py
  ```
- **evidence_paths**:
  - `tools/cn350_poco/m5_plan.json`
  - `tools/cn350_poco/m5_selectors.candidate.json`
  - `/tmp/cn35-m5-acceptance/report.json`
- **rollback**:
  - 恢复升级前备份的 `state/` SQLite 数据库。

---

## T12：Poco 真机验收 — 符文与关卡结算综合闭环

- **id**: `T12`
- **title**: 符文强化/出售/装卸与关卡种子技战斗结算 Poco 真机验收（M509, M510）
- **goal**: 通过 Poco 真机脚本完成符文强化/装卸/出售、携带融合宠/种子技与符文的同伴进入关卡释放技能并完成结算的端到端验收，确认重启服务端后所有状态在 SQLite 中持久化不回滚。
- **depends_on**: `["T11"]`
- **inputs**:
  - `tools/cn350_poco/m5_acceptance.py`
  - `docs/04-成长面板与战斗数值.md`、`docs/10-业务端点与持久化.md`
- **deliverables**:
  - M5 全量真机验收报告与文档同步更新。
- **out_of_scope**:
  - 无。
- **client_contract**:
  - `Skills.cs`、`SkillPowerupModal.cs`、`EquipmentSkills.cs`、`SeedControllerPanel.cs`。
- **verification**:
  ```bash
  cd server && go test ./... -count=1
  git status --short
  ```
- **evidence_paths**:
  - `docs/04-成长面板与战斗数值.md`
  - `docs/10-业务端点与持久化.md`
- **rollback**:
  - 恢复升级前备份的 `state/` SQLite 数据库。
