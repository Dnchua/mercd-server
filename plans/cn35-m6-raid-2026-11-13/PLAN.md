# CN3.5 M6 开发计划与待审核清单：公会设施日常战斗加成 & 暴走魔宠（Raid）恢复

> 文档状态：**已获批并完成阶段 1～4 服务端协议、SQLite `0054` 迁移与 Poco SQL 绑定开发；阶段 5 模拟器实机校准待在设备环境执行**。
> 编写依据：用户提供截图（公会设施 Lv.90~101、暴走主页 `莫鲁菲涅 Lv.233`、暴走准备页）、`client/decoded/assets/bin/Data/Managed/Assembly-CSharp.dll` 反编译源码、`../../cn-mercd-backend/artifacts/mlk-capture-201711/decrypted/` 原服抓包、`server/assets/cn350/serve_app_conf_android.json` 页面配置及梅露可物语 Wiki 暴走活动规则。

---

## 一、今晚与本周末开发排期（2026-11-13 周五深夜 ～ 2026-11-15 周日）

| 阶段 | 时间窗口 | 核心目标 | 主要产出与验证 |
|---|---|---|---|
| **阶段 0（已完成）** | 2026-11-13 周五深夜 | 进度更新、调研定界与 Poco 脚本基线 | 更新 `README.md` 与 `docs/README.md`；输出本待审开发清单与 `ACCEPTANCE.md`；落盘 `tools/cn350_poco/m6_raid_*` 验收套件并通过离线静态门禁 |
| **阶段 1（审核通过后立即启动）** | 今晚 ～ 周六凌晨（约 2～3h） | **Bug 修复 1：公会设施 100+ 级系数校准与日常战斗加成带入** | 统一 `GuildEquipMaxLevel = 120`；修正蓄水库双锚点公式；抽离 `ApplyGuildFacilityBattleBuffs` 统一接入日常探险、公会战、后续暴走战斗；通过 `mlk` 日常战斗样本逐字段回放断言 |
| **阶段 2** | 2026-11-14 周六上午 ～ 下午 | **暴走魔宠数据层 + 首页「活动」快速跳转 + 核心读面/遭遇钩子** | 新增只增 SQLite 迁移 `0054_cn35_raid_monsters.sql` 与 `cn35store` 事务；接入首页「活动」与轮播 `banner_raid` / `banner_raid_pt` 快速跳转；实现 `/quests/result` 强袭遭遇钩子、`/raid_monsters/` 主页与 `/raid_monsters/show` 出击准备页 |
| **阶段 3** | 2026-11-14 周六晚上 | **暴走战斗执行与双路径结算闭环** | 实现 `/raid_monsters/execute.json`（急击/强击/猛击与协力首刀 BP 减免、武器特攻倍率、保底 SP、公会设施加成复用）、`/raid_monsters/bp_use`、`/raid_monsters/lose`（`damage[raid]` 扣血与解锁协力）、`/raid_monsters/result`（击退 Lv+1、按 `总伤害/10000` 发 `raid_pt`、击退日志归档） |
| **阶段 4** | 2026-11-15 周日上午 ～ 下午 | **协力外围交互 + 奖励交换所 + 服务端门禁** | 实现 `/raid_monsters/giveup`、`/raid_monsters/call_help`（公会/聊天室双通道求援）、`/raid_monsters/damage_list`、`/raid_monsters/lv_bonus`、`/raid_monsters/pt_bonus`（双槽 `_0`/`_1`）与 `/raid_monsters/exchange`；补齐 Go 全链路单测与 M6 门禁校验 |
| **阶段 5** | 2026-11-15 周日晚上 | **模拟器 Poco 校准与端到端实机验收** | 按 [CN3.5 APK 打包技能](../../skills/cn35-apk-packaging/SKILL.md) 模式 A 启动外置 `mercd35 -remote-assets inputs/files` 与调试 APK，执行 `inspect` / `calibrate` 并跑通 `M601`～`M610` 端到端与同库重启持久化验收 |

---

## 二、Bug 修复 1 待审核清单：公会设施加成带入日常战斗 & 100+ 级校准

### 2.1 截图与 `mlk` 抓包双锚点实证汇总

| 设施 | 抓包观测（`mlk` `default/guilds.2.json`） | 用户截图观测 | 结论公式与数据来源 | 现状问题 |
|---|---|---|---|---|
| **等级上限** | 抓包最高 Lv.88 | 图1 训练所 `Lv.101` 仍显示 `到下一Lv.还差 21,633,623` | `GuildEquipMaxLevel = 120`（`original_observation` + `community`） | `guild_storage_wire.go:58` 硬编码上限 `100`；`guild.go:145` 仍残留 `EquipMaxLevel: 10` |
| **祭坛 (`sp`, ID=1)** | Lv.88 → `2200` | 图1 Lv.100 → `初始SP值+2500` | `value = 25 * Lv`（`original_observation`，零误差） | 日常探险 `/quests/execute` 未读取该值 |
| **医疗所 (`hp`, ID=2)** | Lv.79 → `19.75%` | 图1 Lv.100 → `体力+25%` | `value = 0.25% * Lv`（`original_observation`，零误差） | 日常探险未生效；公会战乘算基数有偏差（见下） |
| **训练所 (`at`, ID=3)** | Lv.84 → `21%` | 图1 Lv.101 → `攻击力+25.25%` | `value = 0.25% * Lv`（`original_observation`，零误差） | 日常探险未生效；公会战乘算基数有偏差（见下） |
| **食堂 (`taghness`, ID=4)** | Lv.70 → `+28000 / Guts 3` | 图2 Lv.90 → `+36000 / Guts 4` | `value = 400 * Lv`，`value2 = min(5, Lv / 20)`（`original_observation`） | 日常战斗与公会战均漏加主人公 HP；旧代码误将 `taghness` 乘在同伴韧性上 |
| **蓄水库 (`bp_storage`, ID=5)** | Lv.79 → `49000 BP / 65.60%` | 图2 Lv.96 → `蓄水上限 57500 BP / 蓄水库 69.00%` | **双锚点唯一线性真值**：<br/>`蓄水上限 value = 9500 + 500 * Lv`（即 `10000 + 500*(Lv-1)`）<br/>`注入转化率 value2 = 0.498 + 0.002 * Lv`（即 `50.00% + 0.20%*(Lv-1)`）（`original_observation`） | 旧代码 `guild_storage_wire.go:176,190` 单锚点误用 `round100(620*Lv)`（Lv.96 错算为 `59500`）；`guild_storage.go:116` 误用余额比覆盖 `value2` |

### 2.2 `mlk` 日常战斗下发实证（`default/quests_execute_104.json.2.json`）

日常探险响应中 `data` 不下发 `guild` 对象，全部设施加成由服务端在构建战斗文档时直接合入：
1. **初始 SP**：`quest.initial_power` 由模板基础 `1000` 提升为 `1000 + 2200 = 3200`（祭坛 Lv.88），客户端 `PowerupButton.cs:269` 再于此基础上加上编队 `bonus_sp`。
2. **主人公 HP**：`main_character.hp` 由基础 `1000` 提升为 `1000 + 28000 = 29000`（食堂 Lv.70）。
3. **参战同伴 `units[].hp` 与 `units[].at` 的叠加顺序**：对 `mlk` 该战 5 名出战同伴逐一验算确认——医疗所/训练所百分比**乘在同伴等级成长基础面板（不含符文与星盘加成前的 `base_hp` / `base_at`）上四舍五入后加算到终值面板**：
   - `exec_hp = final_hp + round(base_hp * 0.0025 * hpLv)`
   - `exec_at = final_at + round(base_at * 0.0025 * atLv)`（`mlk` 3 号位同伴 `base_at = 10260`，训练所 Lv.84=+21%，增量为 `round(10260 * 0.21) = 2155`，`16823 + 2155 = 18978` 精确命中抓包；若按旧公会战在符文之后直接乘算会偏差至 `20355`）。
4. **救援 Guts 基数**：下发顶层 `guild_asset_help_level_base = max(1, min(5, taghnessLv / 20))`（`PowerupButton.cs:175` 消费）。

### 2.3 Bug 修复 1 具体代码改动清单

- [x] **B1-1 设施上限与双锚点公式校准**：修改 `server/internal/cn35game/guild_storage_wire.go`、`guild.go` 与 `server/internal/cn35store/guild_storage.go`，统一 `GuildEquipMaxLevel = 120`，补全 Lv.89~120 升级资材阈值（利用截图 1/2 中 Lv.90~101 的余量差值校准平滑梯度，并标为 `reconstruction`），修正蓄水库 `value = 9500 + 500*Lv` 与 `value2 = 0.498 + 0.002*Lv`。
- [x] **B1-2 抽离统一设施加成注入器**：新增 `server/internal/cn35game/guild_facility_battle.go`，根据玩家所属公会读取当前常驻四项设施等级（`altar`/`medical`/`training`/`dining`；当前 CN3.5 `temporary` 为空占位；未入会则全 0），深拷贝 `main_character`（避免污染全局 `cat.Defaults.MainCharacter`），注入 `quest.initial_power`、`main_character.hp`、参战同伴 `hp`/`at` 基础面板增量与 `guild_asset_help_level_base`。
- [x] **B1-3 日常探险战斗接入**：修改 `server/internal/cn35game/battle_wire.go`（`buildExecutePayload`），使普通/曜日/历代活动探险全部接入该注入器。
- [x] **B1-4 公会战口径对齐与旧偏差修正**：修改 `server/internal/cn35game/guild_battle_wire.go` 与 `guild.go:1645-1665` 改用同一注入器，修复公会战漏加主人公食堂 HP、同伴百分比在符文后二次乘算偏高、以及误将食堂 `taghness` 乘到同伴韧性上的 3 处历史偏差。
- [x] **B1-5 `mlk` 抓包逐点回归断言**：在 `server/internal/cn35game/guild_facility_battle_test.go` 新增 `TestQuestExecuteGuildFacilityReplayContract` 端到端 `/quests/execute/1.json` 回归测试，校验 `initial_power=3200`、`main_character.hp=29000`、`guild_asset_help_level_base=3` 及同伴 `hp`/`at` 精确命中，并在本地抓包 `default/quests_execute_104.json.2.json` 存在时做同构结构比对。

---

## 三、暴走魔宠（Raid）功能开发待审核清单

### 3.1 客户端资源与入口快速跳转核验

1. **客户端 AssetBundle 就绪确认（`original_observation`）**：
   - `inputs/files/common/` 下已具备全套 12 个必需包：`bg_quest_extra`、`bg_event_170914`、`banner_event_170914`、`banner_event_s_170914`、`bnr_raidboss_exchange_170914`、`bnr-raidguide`、`btn-eventinfo`、`bg-basic_fs_128`、`mode-normal_raid_short`、`mode-hard_raid_short`、`mode-veryHard_raid_short`、`activity_icon`。
   - `inputs/files/monster/` 下已具备图 3 同款光王宠 **`688 莫鲁菲涅`** 与配置默认王宠 **`861 穆西穆`** 五件套（`monster_<id>`、`monster_btn_<id>`、`monster_large_ns_<id>`、`monster_list_<id>`、`monster_square_<id>`）。外置服务端模式下 `remote_assets.go` 会自动在基线清单上叠加下发，无需修改 APK 或 DLL（若后续构建单 APK 模式再同步登记到 `client/asset-types.json`）。
2. **首页「活动」快速跳转入口清单（`client_contract`）**：
   - 修改 `server/internal/cn35game/activity.go`：将 `serve_app_conf_android.json` 中已存在的 `"banner_raid"`（点击执行 `name=Raid&base=Common/Pages/Raid&method=Stack` 直跳暴走主页）和 `"banner_raid_pt"`（点击执行 `name=ShowRaidPtExchange2&method=Stack` 直跳击退 pt 交换所）加入 `activityPageWhitelist`。
   - 联动首页左上角「活动」红点（`act_has_new`）：当存在活跃暴走事件或未击退的暴走魔宠时高亮提示；同时同步检查 `Home.partials.EventBanner` 下的 `RaidEventBannerPartial`（是否放宽 2017 年硬编码时间窗以便直接点开 `Raid` 与 `RaidEventPage`）。

### 3.2 服务端接口与客户端消费契约清单（1 个遭遇钩子 + 11 个 `/raid_monsters/*` 端点）

| 序号 | 路由 | 客户端调用方（`Assembly-CSharp.dll`） | 核心契约与行为 |
|---|---|---|---|
| **R1** | `POST\|GET /quests/result`（钩子） | `Quest.cs:2434-2487`、`RaidAppearModal.cs` | 常规探险通关且当前无未完成自发暴走魔宠时，按概率生成实例并在 `data.raid_monster` 返回 `{id, message, giveup_confirm, next_page_message: "name=Raid&base=Common/Pages/Raid"}`，触发强袭弹窗与转场动画 |
| **R2** | `GET\|POST /raid_monsters/` | `Raid.cs`、`RaidRow.cs`、`BasicParts.cs` | 替代 `module_requiredata.go:323` 空态；返回 `raid_condition`（`lv`, `pt`, `has_raid: 1\|0`, `no_raid_label`, `raid_attack_message`）、`my_raid_monster`（含 `point`/`max_point`/`expired_at`/`attacked`/`monster`）、以及三栏列表 `guild_help_raid_monsters` / `community_help_raid_monsters` / `my_history_raid_monsters` |
| **R3** | `GET\|POST /raid_monsters/show?id=<id>` | `QuestSelectParts.cs:95253-95329` | 图 4 出击准备页；返回 `data.temp_raid_monster`（`header_str`、`user_name`、`point`/`max_point`、`expired_at`、`initial_power` 保底 SP、`difficulty_options`: `normal(160s/30BP)`, `hard(200s/60BP)`, `very_hard(240s/100BP)`，他人协力首刀 `0/30/60BP`）与 `data.battle_weapon_rate`（7 武器特攻倍率） |
| **R4** | `GET\|POST /raid_monsters/execute.json` | `QuestExecuteDocument`、`QuestSelectParts.cs:95403` | 参数 `difficulty_id`, `mode=raid`, `id`, `party_id`；校验存活与 BP，复用 `ApplyGuildFacilityBattleBuffs` 叠加公会设施+符文+星盘并合并等级保底 SP；下发单波 Boss 文档（`mode="raid"`, `current_hp=point`, `calc_damage_key="raid"`, `gauge=true`, `ap_use_url`, `result_url`, `lose_url`） |
| **R5** | `GET\|POST /raid_monsters/bp_use?bid=<bid>` | 战斗开场扣费 | 幂等状态流转 `prepared -> bp_paid` 并扣减对应 BP（若该刀命中协力首刀减免规则按减免后 BP 扣减） |
| **R6** | `GET\|POST /raid_monsters/lose?bid=<bid>&time=...&mc=...&damage[raid]=<val>` | `SWFQuestLose.cs:104808-104878` | 限时结束或全灭退场时上报本刀对 Boss 造成的伤害 `damage[raid]`；服务端扣减实例剩余 `point`、累计玩家伤害台账并置 `attacked=true`（解锁主界面「协力邀请」按钮）；若该刀伤害恰好打空剩余血量则衔接击退完成结算 |
| **R7** | `GET\|POST /raid_monsters/result?bid=<bid>&time=...&mc=0` | `SWFQuestClear.cs:101861-101893` | 限时内直接击破 Boss 胜利结算；将 `point` 置 `0`，发现者 `raid_lv + 1` 并触发 Lv 奖励入账，全体参战者按 `floor(总伤害 / 10000)` 结算 `raid_pt` 并写入击退日志 |
| **R8** | `GET\|POST /raid_monsters/giveup` | `RaidAppearModal.cs:32632`、`serve_app_conf:17520` | 未带 `confirmed=true` 返回 YesNo 确认弹窗，带 `confirmed=true` 将自发魔宠标记放走关闭并清理 |
| **R9** | `GET\|POST /raid_monsters/call_help?raid_help_type=<guild\|community_topic>` | `RaidHelpModal.cs:32725,32760` | 要求已出击至少一刀（`attacked=true`），标记魔宠对公会或公开聊天室可见，并向对应聊天流插入带 `raid_id` 的求援消息 |
| **R10** | `GET\|POST /raid_monsters/damage_list?id=<id>` | `RaidMembers` 页面 | 返回 `data.temp_raid_damage_list`（`{rank, name, damage_str, status, square_icon_asset_name, user_detail_message}`）供「协力者」排行展示 |
| **R11** | `GET\|POST /raid_monsters/lv_bonus`<br/>`GET\|POST /raid_monsters/pt_bonus`<br/>`GET\|POST /raid_monsters/exchange` | `RaidLvBonusPage`、`ShowRaidPtExchange2`（`serve_app_conf:20964`） | `lv_bonus` 返回 `data.raid_lv_bonus`；`pt_bonus` 按双商品槽模板每行返回 `_0` 与 `_1` 两组完整字段；`exchange` 校验限购与 `raid_pt` 后扣 pt 发奖入库 |

### 3.3 数据库迁移设计（只增 `0054_cn35_raid_monsters.sql`，绝不修改 `0001..0053`）

- `cn35_raid_profiles`：`(user_id PK, raid_lv, raid_pt, discovered_count, killed_count, updated_at)`
- `cn35_raid_instances`：`(raid_id PK, owner_user_id, monster_id, monster_name, attr_id, level, max_point, current_point, initial_power, attacked, help_guild, help_community, status, spawned_at, expired_at, finished_at)`
- `cn35_raid_participants`：`(raid_id, user_id, total_damage, hit_count, free_first_used, reward_claimed, last_hit_at, PRIMARY KEY(raid_id, user_id))`
- `cn35_raid_sessions`：`(battle_id PK, raid_id, user_id, difficulty_id, bp_cost, status, reported_damage, created_at, settled_at)`
- `cn35_raid_lv_claims`：`(user_id, target_lv, claimed_at, PRIMARY KEY(user_id, target_lv))`
- `cn35_raid_pt_exchanges`：`(user_id, slot_id, exchanged_count, updated_at, PRIMARY KEY(user_id, slot_id))`

### 3.4 需要用户审核确认的 5 项设计决策（请审阅批复）

1. **当期默认暴走王宠选择**：
   - 选项 A（推荐）：采用图 3 截图同款光属性 **`688 莫鲁菲涅`** 作为默认出战王宠（五件套资源齐备）；同时支持配置文件切换为配置默认 **`861 穆西穆`** 或按活动周期轮换。
2. **常规探险触发概率与等级门槛**：
   - 建议：通关 `AP > 0` 的常规/活动关卡且当前无存活自发魔宠时，默认遭遇概率设为 **50%**（存活时长固定为原版 **90 分钟**），同时允许通过服务配置覆盖为 **100%** 方便测试与单机速刷。
3. **高 Lv Boss 血量与保底 SP 分段曲线（`reconstruction`）**：
   - 已知实机锚点：图 3 `Lv.233` 莫鲁菲涅血量 `10,059,161,640`，图 4 `Lv.233` 保底初始 `SP: 312,500以上`；前期 Lv.1～50 按原版新手～中坚平缓成长（如 Lv.1 约 `30,000 HP`，Lv.50 约 `15,000,000 HP`），Lv.50 以上平滑指数爬升并精确经过 `Lv.233` 锚点。是否按此分段曲线重构？
4. **7 武器特攻倍率规则与奖励表范围**：
   - 武器倍率：建议按每 5 级轮换一组弱点特攻武器（如 `150%`/`200%`），整十/高段位（如如图 4 显示全武器 `100%`）或按原版规则轮换。
   - Lv 奖励 / pt 交换所商品：按 Wiki 标准档位重构钻石、徽章、金币、觉醒书、强化种子与当期王宠 `688 莫鲁菲涅` 本体，支持限购计数持久化。
5. **单机本地游玩的「模拟协力支援」可选开关**：
   - 默认关闭（严格按多玩家/同公会真实交互工作）；是否额外提供一个环境变量/配置开关，允许在单机独狼模式下点击「协力邀请」后由本地模拟队友在几分钟后协助削血、或在配置里允许降低 Lv 回退选项？

