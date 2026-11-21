# CN3.5 M8 验收与验证规格（ACCEPTANCE.md）

## 1. 强门禁（任一不满足就不得宣称 M8 完成）

1. **客户端 DLL 零篡改**：不修改 `Assembly-CSharp.dll` 业务逻辑；原始 APK 按 `client/baseline.json` 校验 SHA-256。所有恢复只通过服务端协议、`consts` 和页面参数下发。
2. **历史迁移只读，状态全部持久化**：
   - `git diff server/internal/database/migrations/0001*..0055*` 必须为空，新增内容只能放在 `0056+`；
   - 重启 `mercd35` 后，活动推进边界、各难度通关次数、区域完成率奖励领取记录、周常 `clear_count`、礼物盒中的奖励都保持不变、不回退。
3. **回放字段同构**（设置 `MLK_CAPTURE_DIR` 时执行）：
   - `/events/`：行字段集合与录播一致，`release_event_select_option` 结构一致；
   - `/areas/event?event_id=44`：5 个顶层键的形态一致；区域行和关卡行的字段集合与录播完全相同；
   - `/quests/daily`（周六）：4 行字段集合与录播一致，`use_ap_map` 与 `difficulty_names` 的值一致；
   - `/quests/execute/80064.json`：`quest` 字段集合为录播超集，`use_ap_map/use_ap/difficulty_names/difficulty_name/difficulty_option_name/wave_conf/unit_help_rate` 与录播逐值一致；`appear_table` 的 38 项（时间、怪物 id、缩放）与录播逐项一致。
   - 以上「字段集合一致」均按「服务端字段 ⊇ 录播字段」判定（服务端额外下发 `quest_select_message`、`force_update` 等协约键）。
4. **结算幂等与事务**：
   - 同一场 `/quests/result` 重放、并发提交，奖励（包括完成率奖励和限定同伴）只发一次；
   - 推进边界、通关次数、奖励记录和物品入库在同一事务内完成；
   - 活动关和周常关的结算不改变主线推进指针。
5. **时间窗与日期门禁**：
   - 窗口外：活动 44 不出现在 `/events/` 中，`/areas/event?event_id=44` 返回空区域，直接调用开战接口会被拒绝且不扣 AP；
   - 非当天的周常关调用开战接口同样被拒绝且不扣 AP；
   - 窗口关闭再重开后，进度可以继续。
6. **范围门禁**：`trials_mode_released=false`；`/trials/*`、`/quests/event`、`/quests/reprint_events/` 仍为空态。
7. **仓库洁净**：`git status --short` 中不包含 decoded 树、APK、Unity 资源、原始抓包、运行库、签名或 `state/` 文件。

---

## 2. 用例矩阵（M801–M812，模拟器 Poco）

> 未连接模拟器执行 `inspect` 和 `calibrate` 之前，`m8_selectors.candidate.json` 中的所有节点都是 `unobserved-requires-device-tree` 状态，`m8_events_daily_plan.json` 标记为 `implementation_ready: false`。

| 用例 | 名称 | 客户端路径 | 端点 | UI / 网络 / SQLite 断言 |
|---|---|---|---|---|
| M801 | 历史活动页签开放 | 探险 → 右箭头 → 历史活动页签 | `/stages/`、`/events/` | 页签可以点击，不再弹「功能即将开放」；列表显示 42 行（窗口内为 43 行），倒计时为正数 |
| M802 | 历史活动行显示 | 滚动列表 | `/events/` | 记录行贴图的实际显示情况（预期背景空白）；行文字和倒计时正常 |
| M803 | 历史活动空区域（D1） | 点击任意历史活动 | `/areas/event?event_id=1` | 进入「选择区域」页且区域为空，不出现错误弹窗，可以正常返回；不请求 `/events/release`，徽章不变 |
| M804 | 活动 44 窗口内进入 | 配置窗口 → 历史活动第一行 → 区域页 → 关卡页 | `/events/`、`/areas/event?event_id=44`、`/quests/?area_id=4176` | 新账号只看到魔菇大街和第一关 1440；4 档 AP 与 doc17 一致；武器倍率显示突、魔强势 |
| M805 | 活动关战斗与推进 | 1440 普通难度出击 → 通关 → 返回 | `execute`、`use_ap`、`result` | 扣 5 AP；结算后 1441 解锁；`cn35_event_progress.quest_id=1441`；`cn35_quest_clears(1440, normal)=1` |
| M806 | 周常页 | 主线区域页周常横幅 → DailyQuestSelect | `/quests/daily` | 4 个按钮的 AP 为 10/20/30/40，`clear_count` 正确；记录横幅和按钮贴图的显示情况 |
| M807 | 周六特级锚点战斗 | 在周六（UTC+8）出击特级 | `/quests/execute/80064.json`、`result` | 3 波共 38 只；结算金币约等于 9,985 加上公会和星盘加成；`clear_count` +1；主线指针不变 |
| M808 | 素材日掉落 | 在任意素材日出击高级 | `execute`、`result`、`/user_items/` | 掉落当天属性的「花」，数量在配置区间内；背包数量增加 |
| M809 | 区域完成率奖励 | 魔菇大街累计通关 4 个难度 | `result` | 弹出完成率奖励提示；礼物盒中有钻石 ×1 和夏尔梅；`cn35_event_area_rewards(4176, 30)` 存在；重放结算不会重复发奖 |
| M810 | Boss 关替代模板 | 解锁并出击 1443 | `execute` | 出现 Boss 血条；替代模板的资源能正常加载；可以通关并结算 |
| M811 | 时间窗关闭 | 把配置窗口改为已过期，重启服务端 | `/events/`、`/areas/event?event_id=44` | 列表中没有活动 44；直接开战被拒绝；重开窗口后进度仍在 |
| M812 | 同库重启一致性 | 重启 `mercd35` 和客户端 → 重新登录 | 全部 M8 端点 | 推进边界、通关次数、奖励记录、礼物和周常次数重启前后完全一致 |

---

## 3. 执行命令

```sh
# 服务端门禁
make check
MLK_CAPTURE_DIR=<只读抓包目录>/decrypted go test ./server/internal/cn35game/ -run 'M8|Event|Daily' -v

# Poco 离线静态门禁
python3 -m unittest discover -s tools/cn350_poco -t . -p 'test_*.py' -v

# 实机（阶段 6，需要先确认可用设备）
python3 -m tools.cn350_poco inspect   --config tools/cn350_poco/config.m8.local.json --role A
python3 -m tools.cn350_poco calibrate --config tools/cn350_poco/config.m8.local.json --role A --selector m8.events.page
```

---

## 4. 当前状态与实机交接（2026-11-19）

### 4.1 已通过的自测

| 门禁 | 命令 | 结果 |
|---|---|---|
| 服务端格式、vet、全量测试 | `make check` | 通过 |
| M8 协议冒泡（含 mlk 回放比对） | `MLK_CAPTURE_DIR=<只读抓包目录>/decrypted go test ./server/internal/cn35game/ -run TestM8 -v` | 6 项全部通过（不设该变量时回放比对跳过） |
| 完成率台账幂等/并发/隔离/重启 | `go test ./server/internal/cn35store/ -run TestEventArea -v` | 通过 |
| 活动目录与生成脚本一致 | `python3 server/tools/build_event_catalog.py --check` | 通过 |
| Poco M8 离线门禁 | `python3 -m tools.cn350_poco.m8_events_daily_acceptance check`、`python3 -m unittest tools.cn350_poco.test_m8_events_daily_acceptance -v` | 通过（`implementation_ready=false`，选择器全部为 `candidate-not-device-verified`） |
| 真实二进制启动 | `make build` 后带 `MERCD35_EVENT_WINDOWS` 启动 | 无活动目录告警；格式错误时启动失败 |

**强门禁 1–7 中，第 1、2、4、5、6、7 条已由上述自测覆盖；第 3 条在设置 `MLK_CAPTURE_DIR` 时覆盖。M801–M812 全部未在实机执行。**

### 4.2 交给实机验收方的步骤

1. 按 `skills/cn35-apk-packaging/SKILL.md` 的模式 A（外置服务端 + APK）起服，并设置：
   - `MERCD35_EVENT_WINDOWS=44=<开始>/<结束>`（覆盖验收当天）；
   - `-remote-assets inputs/files`、请求日志开启。
2. 复制 `tools/cn350_poco/config.m7.local.json` 为 `config.m8.local.json`，修改 `selectors`、`db`、`requests`、`server_result`，并在 `server_hooks` 中增加：
   - `restart_same_state`：同库重启；
   - `restart_event_closed`：把 `MERCD35_EVENT_WINDOWS` 改为已过期窗口后重启（M811 用）。
3. 用 `inspect` / `calibrate` 校准 `m8_events_daily_selectors.candidate.json` 中的 30 个候选控件，写入 `selectors.m8.local.json`。
4. 产出 M8 服务端门禁 `server_result`（`module=M8`，`required_tests` 至少包含 `m8_events_daily_acceptance.M8_REQUIRED_TESTS`），把计划的 `implementation_ready` 改为 `true` 后执行 `run`。
5. 重点观察项：
   - M803 历史活动空区域页是否异常（PLAN §3.1，异常时需要用户决定是否改用占位区域）；
   - M802 / M806 贴图缺失时的显示（`event/event_<id>`、`common/daily-*`、`mode-*_daily`）；
   - 海吉（火）#8 的资源包不在 `inputs/files`，周二、周六、周日的周常会出场，需确认 APK 内是否内置；
   - 周常页依赖设备时区为 UTC+8。
