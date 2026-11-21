# CN3.5 M8 开发计划：限时活动与周常探险

> 文档状态：**阶段 1–5 已完成**（服务端协议、SQLite `0056`、加密协议冒泡与 mlk 回放比对、Poco 离线候选门禁全部通过）；**阶段 6 实机校准与验收待执行**（交由实机验收方，见 [ACCEPTANCE.md](ACCEPTANCE.md) §4）。实现与本计划的偏差见 §七。
> 证据底稿：[限时活动与周常探险回放统计](../../docs/17-限时活动与周常探险回放统计.md)（下称「doc17」）。本计划中的「原服观测 / 公告 / 推断 / 重建值」沿用 doc17 的标记口径。
> 硬约束：不修改客户端 DLL；历史迁移 `0001`–`0055` 不改不删；玩家状态全部落 SQLite；不得以重启重置代替持久化。

---

## 一、已确认的决策（2026-11-19）

| # | 问题 | 决策 | 计划中的落点 |
|---|---|---|---|
| D1 | 41 个历史活动 | **A：只开放列表，点进去是空区域** | §3.1、§3.2 |
| D2 | 当期活动 44 的开放时间 | **按配置时间窗开放** | §2.3、§3.3 |
| D3 | 活动 44 的波次与 Boss | **用现有 Boss 模板替代**（迫伦斯、斯内兰、波彭本地都没有模板） | §4.1 |
| D4 | 周常其余 6 天 | **按公告规则重建**：周一至周五五属性素材本，周末金币本；**以周六特级录播为数值基准** | §4.2 |
| D5 | 周常关卡 id | **独立号段** | §2.1 |
| D6 | 试炼页签 | **M8 保持关闭**（`trials_mode_released=false`） | §0 范围 |

---

## 0. 范围

**做：**
1. 历史活动页签：开放开关，下发 42 条活动列表，点击后进入空区域页。
2. 当期活动 44：3 区域 12 关的目录、推进边界、战斗、结算、区域完成率奖励（含限定同伴 1274、1275），按配置时间窗开放。
3. 周常探险：7 天 × 4 档共 28 关，按星期切换，复用现有战斗与结算链路。
4. 新增 SQLite 迁移 `0056`，以及 Go 端到端加密协议测试。
5. Poco 候选验收计划（`implementation_ready=false`，等待实机校准）。

**不做：**
- 试炼页签、`/trials/*`、协力作战（D6）。
- 迷你活动 `/quests/event`、复刻活动 `/quests/reprint_events/`、活动排行 `/events/ranking`：继续返回空态。
- 41 个历史活动的关卡内容（D1）。
- 公告中「限定探险」的 1.5 倍加成活动：只做无加成的基础值，不做加成时间窗。
- 首页轮播新增活动 44 横幅（录播和资源里都没有活动 44 横幅素材）。

---

## 二、数据设计

### 2.1 id 号段

| 对象 | id | 说明 |
|---|---|---|
| 历史活动 | 原服 event_id（1–40、100000+） | 只出现在列表里，不建关卡 |
| 活动 44 | event_id=44，stage 1044，area 4176–4178，quest 1440–1451 | **沿用原服 id**。与本地主线样例（quest 1–160、area 1–40）和资源本（area 9001+、quest 90011+）都不冲突，启动时做冲突校验 |
| 周常（D5） | `quest_id = 80000 + wday×10 + tier` | wday：周日 0 … 周六 6；tier：初级 1 … 特级 4。例如周日 80001–80004，周六 80061–80064。周六特级 **80064** 以原服 104 为数值锚点，fixture 中记录 `origin_quest_id: 104` |

周常 28 关只有关卡、没有区域，和原服一样 `area_id=null`。

### 2.2 静态目录 fixture：`server/assets/cn350/serve_event_catalog.json`

新增一个只读目录文件，与 `serve_quest_catalog.json` 平行。启动时加载并校验，校验失败就拒绝启动：

```text
{
  "_doc", "_provenance",
  "past_events": [ {id, name, area_key} × 42 ],            // doc17 §3.2，原服观测
  "events": [{
     "id": 44, "name": "雨水滴落的秋之壳与月之雫的翅膀",
     "stage_id": 1044, "area_key": "areas_1044",
     "default_window": {"open_at": null, "close_at": null}, // §2.3
     "areas":  [ {...} × 3 ],                               // doc17 §4.3，原服观测
     "quests": [ {...元数据, "battle": {...}} × 12 ],       // 元数据原服观测；battle 段为重建值（§4.1）
     "area_rewards": [ ... ]                                // §3.4，重建值
  }],
  "daily": {
     "switch_tz": "Asia/Shanghai", "switch_hour": 0,
     "days": [ {wday, name, theme, element, background_id, quests: [ ... × 4 ]} × 7 ]
  }
}
```

- 每个字段都带来源标注：`origin`（原服观测）、`announcement`（公告）、`reconstruction`（重建值）。
- 校验内容：
  - id 不冲突；
  - 所有 `monster_id` 能在 cardcatalog 与纪念版中解析；
  - 所有资源键能在叠加资源清单中找到（缺失项必须登记在 fixture 的 `asset_substitutions` 中）；
  - 周常每天恰好 4 档；
  - 活动关的 `use_ap_map` 中，炼狱 AP 等于极难 AP。

### 2.3 时间窗配置（D2）

- **生效优先级**（与项目现有规则一致）：命令行参数 > 环境变量 > 环境文件 > fixture 的 `default_window`。
- 新增环境变量 `MERCD35_EVENT_WINDOWS`，格式为 `44=2026-11-20T12:00:00+08:00/2026-12-04T11:00:00+08:00`，多个活动用分号分隔；对应命令行参数 `-event-windows`，在 `server/internal/config/cn35_env.go` 中注册。
- **默认值：关闭**。fixture 的 `open_at`/`close_at` 都是 null，表示不配置就不开放。`.env.example` 中给出示例。
- 时间窗只决定「能不能看到和进入」。**玩家的进度、通关记录、已领奖励跨时间窗永久保留**：窗口关闭再重开后，进度从上次继续。
- 窗口内判定：`open_at ≤ now < close_at`。关卡的 `retriable_ended_at` 下发为 `close_at`。

### 2.4 迁移 `0056_cn35_events_daily.sql`

| 表 | 主键 | 用途 |
|---|---|---|
| `cn35_event_progress` | `(user_id, event_id)` | 活动推进边界 `area_id`、`quest_id`，对应 `user_event_condition_<id>`。首次访问时惰性插入初始行（第一个区域、第一关） |
| `cn35_event_area_rewards` | `(user_id, area_id, threshold)` | 区域完成率奖励的领取记录（`threshold` 取 30/60/100），保证幂等；与结算、礼物入库在同一事务内写入 |

**复用的现有表（不改结构）：**
- `cn35_quest_clears`：活动关和周常关的通关次数都记在这张表。活动关的 id、周常关的 80000+ 号段都不会与主线冲突；周常只写 `difficulty='normal'`。
- 礼物盒、物品、金币、同伴发放：沿用现有 grant 与 present 事务。

迁移只建表，不回填数据。升级前按项目流程做一致性备份。

---

## 三、接口契约

所有端点都同时支持 GET 和 POST。注册后要在 `module_requiredata.go` 的空态表中跳过对应路径（在空态表中按装配条件 `continue`，否则真实 handler 会被静默丢弃）。

| ID | 端点 | 行为 |
|---|---|---|
| E1 | `/events/` | 返回 `{"data":{"events":[...]}}`。<br>1）窗口内的当期活动排在最前，`release_limit = close_at`；<br>2）其后是 42 条历史活动，`release_limit` 下发为「当前时间 + 365 天」的滚动值，**偏离原服**的固定值 2027-10-12，目的是让 `release_limit` 永不到期，避免客户端走到 `/events/release` 扣徽章的分支；<br>3）每行 `release_event_select_option` 按 doc17 §3.1 的结构生成 |
| E2 | `/areas/event?event_id=N` | **当期活动且在窗口内**：返回 `areas_1044`、`quests_4176..4178`、`user_event_condition_44`，区域行带服务端计算的 `percentage`，关卡行带 `normal_clear/hard_clear/very_hard_clear`（复用 `quest_wire.go` 的 `areaDoc` 和关卡行组装逻辑）。<br>**当期活动但在窗口外**：返回空区域数组，边界为 `{area_id:0, quest_id:0}`。<br>**历史活动（D1）**：返回 `{<area_key>: [], user_event_condition_N: {area_id:0, quest_id:0}}`。<br>**未知 id**：同历史活动 |
| E3 | `/quests/?area_id=4176..4178` | 与 E2 返回同一组关卡键（关卡选择页 `requireData` 会单独请求这个路径），同样受时间窗控制 |
| E4 | `/quests/daily`（含 `?wday=` 形态） | 返回 `{"data":{"daily_quests_<wday>":[4 行]}}`，wday 按 `Asia/Shanghai` 0 点切日计算；`?wday=` 参数仅用于读面，不能绕过开战门禁。每行字段与 doc17 §5.1 一致，`clear_count` 读 `cn35_quest_clears`，`retriable_ended_at` 为当天 23:59:59 |
| E5 | `/quests/execute/<id>.json` | 复用 `questExecuteHandler`，扩展关卡查找：<br>活动关要求在窗口内、`quest_id ≤ 推进边界`；<br>周常关要求 `quest_id` 对应的 wday 等于服务端当天的 wday。<br>不满足时返回错误弹窗，不扣 AP、不建会话。<br>开战包形状与 `quests_execute_104` 对齐：周常额外下发 `difficulty_name`（档位名）和 `difficulty_option_name`（周X）。公会设施加成沿用 `ApplyGuildFacilityBattleBuffs` |
| E6 | `/quests/use_ap`、`/quests/result`、`/quests/lose`、`/quests/continue` | 复用现有处理链。`/quests/result` 按关卡类型分支：<br>**主线**：推进主线指针（现状）。<br>**活动关**：首次通关时推进 `cn35_event_progress`（同区域下一关，最后一关后进入下一区域）；重新计算区域完成率；跨过 30/60/100 阈值时写入 `cn35_event_area_rewards`，奖励发到礼物盒，并下发 `progress_rate_reward_message`（格式见 doc17 §4.6）。<br>**周常关**：只累加 `clear_count`，不推进任何指针。<br>以上写入都与奖励入账在**同一事务**内完成，重放不会重复发奖 |
| E7 | `/events/release?id=` | 在正常流程下不会被调用（见 E1）。作为防御性兜底，直接返回 `status=success`，不扣徽章 |
| E8 | `consts` 下发（`business.go`） | `event_mode_released=true`；`trials_mode_released` 保持 `false` |

### 3.1 历史活动空区域的风险（D1）

`AreaSelectParts` 收到空数组时会走 `SetIndicator(0)` 和 `CenterRow(null)`，在源码层面看不出空引用风险，但没有实机证据。

- 实机验收 M803 专门覆盖这一点。
- 如果实机出现异常，备选方案是下发 1 个只有名称、没有关卡的占位区域。这个方案**需要你再次确认后才会切换**。

### 3.2 历史活动行贴图

行组件 `EventStageListRow` 加载 `event/event_<id>` 贴图，但 `inputs/files` 中没有 `event/` 目录。预期结果是行背景空白但可以点击，由 M802 实机确认。本期不补贴图。

### 3.3 活动 44 的入口

活动 44 不新增 app_conf 页面，直接作为 `/events/` 列表的第一行出现在历史活动页签。点击后客户端用该行的 `release_event_select_option` 打开 `ReleasedEventAreaSelect`（客户端已有页面）。窗口关闭后这一行从列表中移除。

### 3.4 区域完成率奖励（重建值，依据公告和 `bonus_label`）

完成率的口径与客户端一致：**区域内（关卡数 × 3 个难度）中已通关的难度数所占比例**（`AreaSelectParts.cs:109-121`）。以每区域 4 关计，30% 需要通关 4 个难度，60% 需要 8 个，100% 需要全部 12 个。

| 区域 | 30% | 60% | 100% | 依据 |
|---|---|---|---|---|
| 4176 魔菇大街 | 钻石 ×1 + 夏尔梅（1274） | 夏尔梅（1274） | 钻石 ×1 | 30% 来自 `bonus_label`；60% 来自公告；100% 为重建值 |
| 4177 寄宿学校 | 钻石 ×1 | 琪莉（1275） | 夏尔梅（1274） | 30% 来自 `bonus_label`；60%、100% 来自公告 |
| 4178 螺旋之间 | 钻石 ×1 | 钻石 ×1 | 琪莉（1275） | 30% 来自 `bonus_label`；100% 来自公告；60% 为重建值 |

奖励统一发到礼物盒，与原服结算文案「已经发送到礼盒中」一致。同伴通过现有礼物发放同伴的路径入库。

---

## 四、战斗内容重建规则

### 4.1 活动 44 的 12 关（D3）

**元数据**全部照搬 doc17 §4.4 和 §4.5：AP、属性、标签、波数、波长、初始 SP、背景、武器倍率、`powerup_conf`、`difficulty_names`、`unit_help_rate=0.6` 等。

**出怪表（重建值）：**

1. **普通关**（标签「琼卡、波彭 其他」）：
   - 每波 4–6 只，节奏沿用录播规律：每 6000 ms 一组，组内间隔 250 ms。
   - 怪物池：
     - 琼卡（纪念版中有多个属性版本，按关卡属性选取，资源必须齐全）；
     - 「波彭」用一种同级、资源齐全、属性与关卡一致的普通魔宠替代，并在 fixture 的 `asset_substitutions` 中登记；
     - 「其他」：品奇、海吉、特洛普的同属性版本。
2. **Boss 关**（1443 迫伦斯 / 火，1450 斯内兰 / 水）：
   - 单波，用一个现有 Boss 模板替代：从本地 Boss 级模板中挑选属性一致、资源齐全的一只；
   - 设置 `gauge=true`（显示 Boss 血条），伴随 2–4 只小怪。
3. **强度**：
   - **基准**：普通难度以周六特级录播的模板数值为参照，按 AP 比例缩放（活动关普通 AP 5–12，周六特级 40）；同时不得高于本地主线同 AP 档关卡的强度。
   - **难度倍率**：困难 ×1.6、极难 ×2.4，炼狱与极难相同。
4. **掉落**：每只怪奖励金币、建材、SP，各档数值参照周六录播并按 AP 比例缩放；无素材掉落。每关有小概率掉落魔宠种子（关卡怪物池内的模板）。

**评审节点**：阶段 1 产出 12 关的完整出怪表、两个 Boss 替代候选和「波彭」替代候选，写入 doc17 §10，由你确认后再进入阶段 2。

### 4.2 周常 28 关（D4）

**公共字段**：沿用 doc17 §5.1 的周六 4 档。
- AP 为 10/20/30/40，`use_ap_map` 为 2 倍 / 3 倍 / 3 倍；
- 3 波，每波长 640；`unit_help_rate=1.0`；
- 武器倍率全部为 1.0；`difficulty_names` 4 个值相同（初级 / 中级 / 高级 / 特级）；
- `current_quest_asset_name=common/icon-current_quest_daily`；
- 背景都用 5（只有周六是原服观测，其余天为重建值）。

**每日主题**（依据公告）：

| wday | 名称 | 主题 | 怪物属性 | 素材掉落 |
|---|---|---|---|---|
| 0 | 周日探险 | 金币本 | 光 / 暗 / 火（重建值） | 无 |
| 1 | 周一探险 | 暗属性素材本 | 暗 | 暗属性 |
| 2 | 周二探险 | 火属性素材本 | 火 | 火属性 |
| 3 | 周三探险 | 水属性素材本 | 水 | 水属性 |
| 4 | 周四探险 | 风属性素材本 | 风 | 风属性 |
| 5 | 周五探险 | 光属性素材本 | 光 | 光属性 |
| 6 | 周六探险 | 金币本 | 火 / 水 / 风（原服观测） | 无 |

素材按档位区分（依据公告）：初级掉种子，中级掉花蕾，高级和特级掉花。物品复用 `0017` 已有的 `powerup_item_1..15`。

**数值锚点**：周六特级（80064）**逐只复刻** doc17 §5.2.2 的 38 只出场表，包括时间、怪物、缩放和三项奖励；经验 800；额外掉落中，符文与魔宠种子各做一次概率判定（重建值：符文 50%、种子 30%）。

**其他 27 关的推导**（全部为重建值）：

| 档位 | 每波怪数（第 1/2/3 波） | HP / AT 倍率（相对录播模板） | 允许的稀有度 | 每只金币 / 建材 | 经验 |
|---|---|---|---|---|---|
| 初级 | 4 / 5 / 6 | 0.25 | 只有 ╂ | 66 / 33 | 200 |
| 中级 | 6 / 8 / 10 | 0.45 | ╂，第 3 波可出现 ╂╂ | 131 / 65 | 400 |
| 高级 | 8 / 10 / 13 | 0.70 | ╂ 与 ╂╂ | 197 / 98 | 600 |
| 特级 | 10 / 12 / 16 | 1.00 | 与录播相同 | 263 / 131 | 800 |

- 金币和建材按 AP 比例从特级锚点等比推出；SP 按怪物种类固定（品奇 140、特洛普 150、海吉 160、莫诺巴特 200、纳吉 210，原服观测）。
- **素材日**：每只怪的金币降为周末的 1/3；素材在开战时一次性判定，复用资源本的 `rollResourceRewards` 和会话奖励快照机制。每档数量参考 `0017` 资源本的初始值。
- **模板替换**：
  - 光、暗两天没有纳吉，用同属性的莫诺巴特替代；
  - 海吉（火）#8 的资源包缺失，如果资源门禁确认 APK 内也没有，就在周二和周日用品奇（火）#4 替代，并在 fixture 中登记。

**评审节点**：阶段 1 产出 28 关的出怪表摘要（每关的怪数、稀有度分布、HP 区间、金币和素材期望），写入 doc17 §10 供你确认。

---

## 五、任务拆解

| 阶段 | 任务 | 主要文件 | 完成条件 |
|---|---|---|---|
| **1 目录与重建表** | M8-01 编写 `serve_event_catalog.json`（42 条历史活动、活动 44、周常 28 关）及生成脚本 | `server/assets/cn350/serve_event_catalog.json`、`server/tools/build_event_catalog.py` | 脚本可重复生成同一份文件；doc17 §10 写入出怪表与替代候选；**用户评审通过** |
| | M8-02 目录加载与启动校验 | `server/internal/cn35game/event_catalog.go`（新增） | id 冲突、怪物模板、资源键、档位数校验全部有单测 |
| **2 数据层** | M8-03 迁移 `0056` 与存储层 | `server/internal/database/migrations/0056_cn35_events_daily.sql`、`server/internal/cn35store/events.go` | 惰性插入初始边界；完成率奖励的幂等写入；`git diff` 中 `0001`–`0055` 为空 |
| | M8-04 时间窗配置 | `server/internal/config/cn35_env.go`、`.env.example`、`server/cmd/mercd35` | 优先级与格式校验有单测；默认关闭 |
| **3 读面** | M8-05 E1、E2、E3、E7 活动读面 | `server/internal/cn35game/event_wire.go`（新增）、`module_requiredata.go`、`cn35game.go` | 窗口内外、历史活动、未知 id 四种情况都有加密协议测试；与 doc17 字段集合逐键比对 |
| | M8-06 E4 周常读面与 E8 consts | `daily_quest_wire.go`（新增）、`business.go` | 7 个 wday 各自返回正确的键和 4 行；跨日切换有测试 |
| **4 战斗与结算** | M8-07 E5 开战门禁与开战包 | `battle_wire.go`、`resource_quest.go`（合并目录） | 80064 开战包与 `quests_execute_104` 的顶层键和 `quest` 字段集合同构，38 只出场表逐项一致 |
| | M8-08 E6 结算分支：活动推进、完成率奖励、周常次数 | `battle_wire.go`、`cn35store` | 重放和并发下只发一次奖；奖励进入礼物盒；主线指针不受影响；重启后数据保持 |
| **5 门禁与文档** | M8-09 回放比对测试，读取 `MLK_CAPTURE_DIR` | `event_wire_test.go`、`daily_quest_wire_test.go` | 设置 `MLK_CAPTURE_DIR` 时逐键比对 `events`、`areas_event`、`quests_daily`、`quests_execute_104`；未设置时记日志并跳过 |
| | M8-10 Poco 候选计划与选择器 | `tools/cn350_poco/m8_events_daily_plan.json`、`m8_selectors.candidate.json` | 离线静态检查通过；`implementation_ready=false` |
| | M8-11 更新 README 功能现状与 TODO、doc17 实现状态 | `README.md`、`docs/17-*.md` | 只标「服务端已落地，待实机」 |
| **6 实机** | M8-12 模拟器校准与 M801–M812 验收 | 按 `skills/cn35-apk-packaging/SKILL.md` 模式 A 执行 | 见 ACCEPTANCE.md；**在你确认可用的模拟器上执行**，不使用其他任务占用的设备 |

**依赖关系**：阶段 1 评审通过后才开始阶段 2；阶段 3 和阶段 4 依赖阶段 2；阶段 6 依赖阶段 1 至 5 全部完成。

---

## 六、风险与待实机确认项

| 风险 | 影响 | 应对 |
|---|---|---|
| 历史活动的空区域页可能异常（§3.1） | D1 无法按原样落地 | M803 优先验证；异常时提出占位区域方案，经你确认后切换 |
| 历史活动行贴图缺失（§3.2） | 行背景空白 | 接受，在 M802 中记录 |
| 活动 44 的区域图（4176–4178）和背景（84/85）缺失 | 区域图空白；开战时背景加载失败 | 背景改用 `background_pool` 中已有的套件（重建值，登记替代）；区域图接受空白或复用已有区域图，阶段 1 列出候选 |
| 周常横幅和按钮贴图（`daily-*`、`mode-*_daily`）不在 `inputs/files` 中 | 周常页按钮和横幅可能空白 | 阶段 1 核对 APK 内置资源；缺失时在 M806 中记录，不阻塞协议验收 |
| 客户端用设备时区计算 wday（`Common.Now` 基于设备本地时间） | 设备时区与 UTC+8 不一致时，客户端会请求错误的星期键 | 服务端按 UTC+8 判定；验收设备使用 UTC+8；开战门禁会拒绝非当天的关卡 |
| 「初始 SP 1000 → 3200」的差值来源 | — | 已由 M6 证实是公会设施祭坛加成，开战时沿用注入器；列表中仍下发 1000 |

---

## 七、实现记录（2026-11-19）

### 7.1 交付物

| 任务 | 文件 | 状态 |
|---|---|---|
| M8-01 | `server/tools/build_event_catalog.py` → `server/assets/cn350/serve_event_catalog.json` | 完成；`--check` 校验生成一致 |
| M8-02 | `server/internal/cn35game/event_catalog.go` | 完成；启动期校验 id 号段、区域引用、周常 7×4、battle 形状与怪物模板引用，失败时活动读面保持空态、剧情目录不受影响 |
| M8-03 | `server/internal/database/migrations/0056_cn35_events_daily.sql`、`server/internal/cn35store/events.go` | 完成 |
| M8-04 | `server/internal/config/cn35*.go`、`server/internal/cn35app/app.go`、`.env.example` | 完成；`MERCD35_EVENT_WINDOWS` / `-event-windows`，格式错误启动即失败 |
| M8-05/06 | `server/internal/cn35game/event_wire.go`、`quest_wire.go`、`business.go` | 完成 |
| M8-07/08 | `server/internal/cn35game/battle_wire.go` | 完成 |
| M8-09 | `server/internal/cn35game/event_wire_test.go`、`server/internal/cn35store/events_test.go` | 完成；`MLK_CAPTURE_DIR` 设置时与录播比对通过 |
| M8-10 | `tools/cn350_poco/m8_events_daily_*`、`test_m8_events_daily_acceptance.py` | 完成；`implementation_ready=false` |
| M8-11 | `README.md`、`docs/17-*.md` §10 | 完成 |
| M8-12 | — | **待实机** |

### 7.2 与计划的偏差

1. **不建 `cn35_event_progress` 表**：推进边界改为由 `cn35_quest_clears` 推导（首个三难度均未通关的关卡）。结算、重启、窗口重开后天然一致，没有第二份可失步的状态。`0056` 只建 `cn35_event_area_rewards`。
2. **完成率奖励与结算不在同一事务**：结算事务提交后立即对账发放（台账主键幂等 + 礼物同事务）；`/areas/event` 读面也会对账补发，覆盖结算后进程中断的情况。
3. **活动关 `next_page_message`** 为 `name=StageSelect&base=Common/Pages/StageSelect&select=4`（回到历史活动页签）；原服活动关的值没有录播。
4. **炼狱难度**：`use_ap_map.impossible` 照录播下发，但开战一律拒绝（`cn35_quest_clears` 的难度约束不含 `impossible`）。
5. **周常符文掉落未实现**：录播中的符文 `{key:"skill", level}` 掉落形状尚未取证，只保留魔宠种子概率掉落。
6. **周常会话 `area_id`**：会话表约束 `area_id ≥ 1`，周常关记为当日号段占位 `80000 + wday×10`，不对应任何区域。
7. **`force_update`**：活动行与关卡行的 `requireData` 额外带 `force_update: true`（录播无此键），防止结算后推进边界缓存不刷新。
8. **资源替代**：区域图 4176/4177/4178 → 4164/4165/4162，背景 84/85 → 5；原值记录在 fixture `x_origin`。
