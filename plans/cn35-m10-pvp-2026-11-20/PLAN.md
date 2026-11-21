# CN3.5 M10 开发计划：癒术士大赛与公会训练

> 文档状态：**服务端协议、SQLite `0057`、加密协议冒泡测试与 Poco 离线候选门禁已完成**；**实机验收 2026-11-21 已执行**（M1001–M1012 中 11 项通过、M1011 胜利分支未在实机触发，见 ACCEPTANCE §4）；选择器尚未校准入库。
> 证据底稿：[癒术士大赛与公会训练回放统计](../../docs/18-癒术士大赛与公会训练回放统计.md)（下称「doc18」），标记口径同 doc17。
> 硬约束：不修改客户端 DLL；历史迁移 `0001`–`0056` 不改不删；玩家状态全部落 SQLite；不以重启重置代替持久化。

---

## 一、已确认的决策（2026-11-20）

| # | 问题 | 决策 |
|---|---|---|
| D1 | 主角光环 | **不复原**，与同伴潜能、天赋同列永久范围外（README / docs04 / docs15 已更新） |
| D2 | M10 范围 | 癒术士大赛（`/ladders/*`）+ 公会训练（`/rob_battles/*`、`/user_rob_decks*`），两者共用「本地真实账号 + 确定性 NPC」对手池 |
| D3 | 叶子奖励 | 叶子只服务潜能/天赋（不复原），一律折算为徽章，战斗掉落去掉叶子 |
| D4 | 分段 | 本地无全服人数，按积分档位（doc18 §4） |
| D5 | 付费挑战 | 不做钻石加购挑战；每日免费 3 次用完即弹窗 |
| D6 | 实机 | 本轮只交付 Poco 候选计划（`implementation_ready=false`），不连接设备 |

---

## 0. 范围

**做：**
1. 入口：`/users/function_portal` 追加原服第 8 项「公会训练」与第 9 项「癒术士大赛」横幅（原服 `show_page_message` 原样）。
2. 癒术士大赛：主页、候选对手、防御魔宠编成、开战（普通/困难/极难）、扣次数、胜/败结算、积分与对手扣分、魔宠增幅（升级/重置）、排行榜与周结算领取。
3. 公会训练：主页（入场券/积分/战绩）、攻守编队、候选对手、战前准备、自动战斗开战文档（`AutoQuest`）、扣次数、胜/败结算、防守战绩与复仇。
4. `preset_data.consts` 注入 `ladder_difficulty`、`rob_battle_career_limit`；`data.user` 的 `lc/max_lc/rc/max_rc/potions` 改为真实投影。
5. SQLite 迁移 `0057_cn35_pvp_ladder_rob.sql`，Go 加密协议冒泡测试，Poco M10 候选计划。

**不做：**
- 主角光环、潜能、天赋（D1）。
- 钻石加购挑战（D5）、元素伤害增幅的战斗消费（客户端无消费方，只展示与扣药水）。
- 增幅器「重置后旧加成暂时维持」的保护机制（重置即时生效，有意差异）。
- 跨服排名、SS/S/A 按人数比例晋降级。

---

## 二、数据设计（迁移 0057）

| 表 | 主键 | 用途 |
|---|---|---|
| `cn35_ladder_profiles` | `user_id` | 届/周键、积分、最高战胜积分、攻守胜负计数、当日已用次数、三种药水 |
| `cn35_ladder_decks` | `(user_id, slot_index)` | 5 格防御魔宠 uuid |
| `cn35_ladder_grow` | `(user_id, grow_type, attr_id)` | 增幅等级（步数）与已花药水 |
| `cn35_ladder_sessions` | `battle_id` | 开战会话：对手、难度、对手积分、预掷掉落、状态 `prepared→paid→won/lost`、积分增量 |
| `cn35_ladder_settlements` | `(user_id, week_key)` | 周结算台账（分段、奖励 JSON、是否领取、过期时刻），主键即幂等 |
| `cn35_rob_profiles` | `user_id` | 积分、当日键、剩余 `rc`、当日已购券数、攻守胜负计数 |
| `cn35_rob_decks` | `(user_id, party_type, party_id, slot_index)` | 攻/守两套 2 队 × 5 格 unit_id |
| `cn35_rob_sessions` | `battle_id` | 开战会话：对手、预计增益、状态 |
| `cn35_rob_records` | `record_id` | 被挑战记录（防守方视角），复仇标记 |

所有写入（扣次数、发奖、积分、对手扣分、战绩）在同一事务；会话状态条件更新保证重放/并发只结算一次。

---

## 三、接口与重建规则

### 3.1 癒术士大赛

| 端点 | 行为 |
|---|---|
| `/ladders/show_ladder_info` | 结算上周（若本周首次访问）→ 回 `ladder_main_page`（玩家信息、规则 19 条、6 个按钮、`ladder_info` 排位赛 1 行 / 分段赛 4 行、`rank_index`）、`ladder_condition`、`ladder_main_page_disable_key`、`has_ladder_rewards`；前三名不足 3 人时以候选同源确定性 NPC 补足（重建值；排行榜仍只列真实账号） |
| `/ladders/get_candidates` | 真实账号优先、NPC 补足 20 行；行结构同原服（`icon/name/tip/btn_visible/info/message.addition`） |
| `/ladders/monsters`、`/ladders/set_deck`、`/ladders/remove_from_deck` | 5 格防御魔宠；同一魔宠换格时自动移出原格；回 `ladder_deck` + `ladder_deck_data` |
| `/ladders/ladder_quest_execute.json` | 校验 15 级、当日次数、己方编队、对手防御格；按 doc18 生成 boss/杂兵表，预掷掉落写会话 |
| `/ladders/lc_used` | `prepared→paid` 并扣 1 次（幂等）；回 `status/data.user/user_coin` |
| `/ladders/attack` | 胜利结算：积分、掉落入账、真实对手扣分；回 `status/ladder_score/ladder_score_text/data.user` |
| `/ladders/lose` | 败北（有意差异，见 doc18 §4） |
| `/ladders/monster_grow_index`、`monster_grow_upgrade`、`monster_grow_upgrade_reset` | 三页 × 5 属性；价格 50 + 25×魔阻；重置返还药水 |
| `/ladders/show_rank`、`/ladders/fetch_rewards` | 本地真实账号按积分排名；领取未过期的周结算奖励（金币 + 徽章） |

### 3.2 公会训练

| 端点 | 行为 |
|---|---|
| `/rob_battles/show_rob_battles_info` | `rob_battle_enter`（三档券 + 积分）+ `rob_battle_record`（最近 20 条被挑战记录） |
| `/rob_battles/buy_rob_ticket` | 按序购券（免费 / 50000 / 100000 金币），`rc` 补满 5 |
| `/user_rob_decks`、`/user_rob_decks/on`、`/user_rob_decks/off` | 攻守两套编队；同套内同伴不重复；治疗职业最多 2 人 |
| `/rob_battles/get_candidates` | 防守 2 队满员的真实账号优先、NPC 补足 10 行 |
| `/rob_battles/battle_ready` | 敌方 10 名同伴文档、`reward`、`addition`、`score_mode=true` |
| `/rob_battles/rob_battle_quest_execute.json` | 校验 `rc>0`、攻击 2 队满员；回 `AutoQuestExecuteDocument` 形状 |
| `/rob_battles/rc_used`、`/rob_battles/attack`、`/rob_battles/lose` | 扣次数 / 胜利结算 / 败北（重建端点） |

---

## 四、阶段

| 阶段 | 内容 | 状态 |
|---|---|---|
| 1 | 证据重建（mlk 只读解密）与 doc18 | 完成 |
| 2 | 迁移 0057 + `cn35store/pvp.go` 事务与幂等 | 完成 |
| 3 | `cn35game` 规则、读写面与路由 | 完成 |
| 4 | Go 冒泡测试（`TestM10*`）与 `make check` | 完成 |
| 5 | Poco 候选计划（M1001–M1012）与离线门禁 | 完成 |
| 6 | 实机校准与验收 | 手动实机验收完成（ACCEPTANCE §4）；Poco 选择器校准未做 |
