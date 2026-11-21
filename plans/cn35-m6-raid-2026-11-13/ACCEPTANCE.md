# CN3.5 M6 验收与验证规格（ACCEPTANCE.md）

## 1. 强门禁（Hard Gates — 任一不满足即不得宣称 M6 完成）

1. **客户端 DLL 零篡改门禁**：
   - 严禁修改 `client/decoded/assets/bin/Data/Managed/Assembly-CSharp.dll` 业务逻辑；原始 APK 按 `client/baseline.json` 校验 SHA-256。
2. **历史迁移只读与真实 SQLite 持久化门禁**：
   - `git diff server/internal/database/migrations/0001*..0053*` 必须为空；
   - 暴走魔宠全部状态表仅允许通过新编号迁移（`0054+`）引入；
   - 重启 `mercd35` 后，公会设施等级与蓄水库数据、暴走 `raid_lv`/`raid_pt`、击退历史、Lv 奖励与 pt 商店兑换记录全部保持不回退。
3. **公会设施 100+ 级校验与战斗三端同源门禁**：
   - `GuildEquipMaxLevel = 120`；Lv.101+ 可继续投资并正确返回下级经验与加成；
   - 蓄水库双锚点真值：`Lv.79 -> 49000 BP / 65.60%`、`Lv.96 -> 57500 BP / 69.00%` 必须精确命中；
   - 日常探险（`/quests/execute/*.json`）、公会战（`/battles/guild_quest_execute.json`）与暴走魔宠（`/raid_monsters/execute.json`）三处共用同一套公会设施战斗注入逻辑，且通过 `mlk` 抓包 `default/quests_execute_104.json.2.json` 回放数值断言。
4. **暴走魔宠（Raid）全链路契约门禁**：
   - 首页「活动」及活动轮播 `banner_raid` / `banner_raid_pt` 可直接跳转至 `Raid` 主页与 `ShowRaidPtExchange2` 交换所；
   - `/quests/result` 遭遇强袭 → `RaidAppearModal` → `/raid_monsters/` → `/raid_monsters/show` → `/raid_monsters/execute.json` → `/raid_monsters/bp_use` → `/raid_monsters/lose`（上报 `damage[raid]` 扣血并解锁协力）→ `/raid_monsters/call_help` → `/raid_monsters/damage_list` → `/raid_monsters/result`（击退 `raid_lv+1` 与 `raid_pt` 发奖）→ `/raid_monsters/lv_bonus` → `/raid_monsters/pt_bonus` + `/raid_monsters/exchange` → `/raid_monsters/giveup` 全端点闭环可运行。
5. **仓库洁净门禁**：
   - `git status --short` 不得包含 decoded 树、APK、Unity 资源、原始抓包、运行库、签名或 `state/` 文件。

---

## 2. Poco 模拟器验收用例矩阵（M601 ~ M610）

> 注：未连接模拟器运行 `inspect` 与 `calibrate` 前，`tools/cn350_poco/m6_raid_selectors.candidate.json` 所有节点均处于 `unobserved-requires-device-tree` 状态，且 `m6_raid_plan.json` 标记 `implementation_ready: false`。

| 用例 ID | 名称 | 客户端入口与路径 | 服务端端点契约 | 核心三方断言（UI / 网络 / SQLite） |
|---|---|---|---|---|
| `M601` | 公会设施 100+ 级读面与蓄水库校准 | 公会主页 → 公会设施 → 翻页 | `GET\|POST /guilds/` | 校验祭坛 Lv.100 `+2500`、训练所 Lv.101 `+25.25%`、食堂与蓄水库双锚点数值显示及 DB 设施等级 |
| `M602` | 公会设施加成带入日常探险战斗 | 探险主页 → 日常关卡 → 进入战斗并通关 | `/quests/execute/`<br/>`/quests/result` | 校验战斗 HUD 初始 SP 含祭坛加成、救援 Guts 含食堂加成，请求记录捕获且 SQLite 完成体力扣减 |
| `M603` | 日常通关触发暴走魔宠强袭弹窗 | 探险结算 → `RaidAppearModal` → 点击前往击退 | `/quests/result`<br/>`/raid_monsters/` | 弹窗可见；转入 `Common/Pages/Raid` 主界面；`cn35_raid_instances` 写入 `status='active'` 记录 |
| `M604` | 首页「活动」快速跳转暴走主页 | 旅馆 `Home` → 活动入口 → `banner_raid` → 切换三 Tab | `/raid_monsters/` | 快速跳转直达暴走主页；核验击退 Lv / pt、三栏 Tab 切换及首刀前协力邀请按钮灰显 `disabled` |
| `M605` | 暴走魔宠出击准备页检查 | 暴走主页点击「前往击退」 → `RaidQuestSelect` | `/raid_monsters/show` | 核验急击(160s/30BP)、强击(200s/60BP)、猛击(240s/100BP)、武器倍率区与保底 SP 提示 |
| `M606` | 暴走首刀未击杀与 `damage[raid]` 上报 | 选择难度 → 出击战斗 → 造成伤害后退场/超时结算 | `/raid_monsters/execute.json`<br/>`/raid_monsters/bp_use`<br/>`/raid_monsters/lose` | 校验 Boss 血条可见与 BP 扣除；退场后实例 `current_point` 精准减少、`attacked=1`、协力邀请按钮点亮 |
| `M607` | 协力邀请双通道呼叫与伤害榜 | 点击协力邀请 → `RaidHelpModal` → 公会/聊天室请求 → 协力者排行 | `/raid_monsters/call_help`<br/>`/raid_monsters/damage_list` | 公会/聊天室求助均返回成功；`RaidMembers` 页面展示伤害排行榜；DB 写入协力标志与消息记录 |
| `M608` | 暴走最终击退、`raid_lv` 晋升与 Lv 奖励 | 再次出击击破残血 Boss → 查看击退日志与 Lv 奖励页 | `/raid_monsters/result`<br/>`/raid_monsters/lv_bonus` | `raid_lv` 加 1、`raid_pt` 按伤害万分之一入账、击退日志展示该条记录、`RaidLvBonusPage` 读取奖励列表 |
| `M609` | 击退 pt 交换所兑换与放走魔宠 | 首页活动 `banner_raid_pt` → `ShowRaidPtExchange2` 兑换；新宠点击放走 | `/raid_monsters/pt_bonus`<br/>`/raid_monsters/exchange`<br/>`/raid_monsters/giveup` | `raid_pt` 扣减、目标道具入库且限购计数递增；放走弹窗确认后当前实例标记归档 |
| `M610` | 同库重启持久化一致性验收 | 重启 `mercd35` 与重启客户端 → 登录 → 活动跳转暴走主页 | `/preset_data`<br/>`/raid_monsters/` | 公会设施、暴走 `raid_lv`、`raid_pt`、兑换库存与击退日志在重启前后 100% 一致 |

---

## 3. 前置准备与执行命令

### 前置测试存档条件
- 专用测试副本账号 `A` 需已加入包含测试用高等级设施（例如 Lv.100 祭坛/医疗所、Lv.101 训练所、Lv.90 食堂、Lv.96 蓄水库）的公会；
- 拥有满级测试同伴编队、充足 AP/BP 恢复道具（或足够体力上限）以完成连续出击与消耗验证；
- 启动外置服务端时启用 `-remote-assets <inputs/files 绝对路径>` 并开启请求日志。

### 静态检查与离线单测
```sh
python3 -m tools.cn350_poco.m6_raid_acceptance check
python3 -m unittest discover -s tools/cn350_poco -t . -p 'test_*.py' -v
```

### 实机/模拟器校准与最终运行（阶段 5 解锁）
```sh
python3 -m tools.cn350_poco inspect --config tools/cn350_poco/config.m6.local.json --role A
python3 -m tools.cn350_poco calibrate --config tools/cn350_poco/config.m6.local.json --role A --selector m6.raid.page
python3 -m tools.cn350_poco.m6_raid_acceptance run --config tools/cn350_poco/config.m6.local.json --record
```
