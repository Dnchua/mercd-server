# mercd-server

《梅露可物语》国服 **CN3.5 本地复原服务端**。提供 Go 服务、SQLite 玩家存档、协议实现、gomobile 入口与客户端准备脚本。当前仅维护 CN3.5，不重新引入已退役版本业务代码。

原游戏 APK、Unity 资源与剧情正文需要使用者自行准备。部分数值和规则属于重建值；服务端实现或测试通过，不代表完整游戏已通过实机验收。

## 目录

- [功能现状](#功能现状)
- [TODO list](#todo-list)
- [环境与版本要求](#环境与版本要求)
- [快速启动](#快速启动)
- [环境配置](#环境配置)
- [客户端构建模式](#客户端构建模式)
- [开发与验证](#开发与验证)
- [性能诊断与评测](#性能诊断与评测)
- [文档与数据](#文档与数据)
- [提交与许可](#提交与许可)

## 功能现状

以下为当前代码已有的主要实现范围，不表示模块所有分支均已完成：

| 模块 | 已有实现 |
|---|---|
| 登录与存档 | 登录、选服、启动配置、独立账号 SQLite 持久化 |
| 探险与战斗 | 剧情关卡目录、编队、战斗会话、AP 消耗（含升级 AP 溢出累加）、胜负结算与资源副本；魔宠种子技字段下发、收益系符文掉落与经验结算展示对齐；战场与关卡救援（`/users/help`）；公会设施加成（祭坛 SP／医疗所 HP／训练所 AT／食堂主人公体力与救援 Guts）同步带入日常探险、公会战与暴走战斗 |
| 同伴与魔宠 | 库存、队长／助战、保护、同伴训练、同伴进化（274 条进化链，`/user_units/evolution`）、同伴与魔宠告别（`/user_units/sell`、`/user_monsters/sell`）、同伴能力互换（`/unit_ability_exchange/*`）、魔宠装备与强化、融合宠全链路（进化之卵培育 `/user_monsters/evolve_egg`、魔宠融合 `/user_monsters/evolve`、稀有宠体型强化）、符文强化／出售／装卸、同伴与魔宠图鉴（`/albums/`、`/monster_albums/`）；觉醒已有暂定实现，国服按原版规则关闭潜能系统（`unit_upgrade_open=false`） |
| 抽卡与经济 | 每日轮换 33 池特殊招募（UTC+8 `04:00` 切日）、常驻/金币池、单抽／9 折十连（骑士契约书 9 本抽 10 次）、面包店 5 钻兑骑士契约书、星级契约书使用及 6 合 1、★3/★4/★5 信券单抽、扣费发卡、礼物领取、物品背包与徽章兑换（新增机制已完成服务端冒泡验证，待实机测试） |
| 公会与公会战 | 建会、搜索、成员管理与成员原路径列表（`/guilds/users/?id=`，含活跃时间与贡献点）、防卫编队、基础设施/装备投资与强化、公会 BP 仓库（存储／取出／捐赠）、等待邀请池（`/waiting_users`）、时窗、祈祷／应援、赛季历史与月榜奖励结算（下发 item/skill 奖励表）、单／多目标战斗结算及独行公会适配 |
| 日常与活动 | 不可思议之泉（LoginBonus 按时间累积金币／建设素材／BP 并每周期随机产出 5–100 徽章，支持上限存储与溢出领取，Home 泉水倒计时与红点对齐）、登录冒险（`/adventure/`）真实读面与持久化、多日/春季签到活动（`/users/show_login_bonus`、`/users/receive_login_bonus`）读面与防重领取；暴走魔宠活动（`/raid_monsters/*` 全链路、探险遭遇强袭、首页活动快速跳转与 pt 交换所，服务端协议已落地）；限时活动与周常探险（M8：历史活动列表、当期活动 44 按时间窗开放与区域完成率奖励、周常 7 天×4 档按 UTC+8 切日，服务端协议已落地，待实机验收） |
| 癒术士大赛与公会训练 | 主人公的日常追加两项入口；癒术士大赛（`/ladders/*`）：防御魔宠 5 格、20 名候选对手（本地真实账号优先、确定性 NPC 补足）、普通/困难/极难开战、每日 3 次免费挑战、积分与对手扣分、增幅药水掉落与魔宠增幅、按分段的周结算领取；公会训练（`/rob_battles/*`、`/user_rob_decks*`）：攻守两套 2×5 编队（治疗限 2 人）、入场券三档、自动对战（`AutoQuest`）、积分、战绩与复仇；SQLite `0057`（M10 服务端协议已落地，待实机验收） |
| 个人与社交 | 个人主页展示（`/users/show`）、玩家个性签名设置与修改（`/users/update_memo`）、好友申请／批准／解除、好友搜索与助战、友情徽章礼物盒（`/friends/*`）、救援请求 |
| 主角成长与天赋 | 主角命运星盘（`/fates/*`）读面、祈愿与战斗／探索属性加成持久化；主角光环（`/main_chara_buff/*`）与同伴天赋（`/passive_skill/*`）同潜能一样列入**不复原**板块，入口置灰、读面保持空态 |
| 教程与剧情阅读 | 新手任务与领取、章节／故事读取（支持 `/chapters/` 与 `/chapters/list`）；内置 `server/tools/extract_cn350_stories.py` 提取器可从自备剧情版 APK 提取完整 CN3.5 第一部 11 章 533 话（叠加资源后 100% 闭合，待实机阅读验收） |

## TODO list

按大模块汇总，依据当前路由、空态处理和明确拒绝的写操作整理。完成前还需验证实际操作、奖励入账与重启存档。

- [x] **不可思议之泉与登录冒险**：不可思议之泉（LoginBonus）完整实现按时累积（金币、公会建设素材、BP 支持溢出持有）并在每周期随机产出 5–100 徽章（`0055` 落库与冒泡测试已通过，徽章弹窗表现待下轮实机核验），主页红点与倒计时对齐；登录冒险（`/adventure/`）实现为真实读面与持久化；多日/春季签到活动已落地；未验证的“每日转盘”独立玩法仍保留核对。
- [x] **公会设施与仓库**：基础设施与装备投资升级已完成 120 级上限、蓄水库双锚点校准（`9500+500*Lv` / `0.498+0.002*Lv`）、公会 BP 仓库（存取／捐赠与上限）持久化，以及三端战斗（日常探险／公会战／暴走）设施加成同源注入；临时设施到期与清理流程按需维护。
- [x] **公会外围功能补全**：成员原路径读面（`/guilds/users/?id=` 包含活跃时间与贡献点补充）、等待邀请池（`/waiting_users`）、赛季历史记录以及月度排行榜奖励（item/skill 奖励表下发与展示）已补齐。
- [x] **个人主页、好友与友情徽章**：已补齐个人主页展示（`/users/show`）、个性签名编辑更新（`/users/update_memo`）、救援请求（`/users/help`）、好友申请／批准／解除、好友搜索与友情徽章礼物盒（`/friends/*`）；社区话题细化仍按需推进。
- [x] **同伴进阶、融合宠养成与图鉴（M5 服务端协议）**：已支持同伴与魔宠图鉴（`/albums/`、`/monster_albums/`）、同伴进化（`/user_units/evolution`）、同伴与魔宠告别（`/user_units/sell`、`/user_monsters/sell`）、同伴能力互换（`/unit_ability_exchange/*`）、融合宠培育与进化（`/user_monsters/evolve_egg`、`/user_monsters/evolve`、稀有宠体型强化）、符文强化与出售；国服关闭潜能系统；魔宠分享仍关闭，待补齐实机 Poco 全量校准。
- [x] **限时活动与周常探险（M8 服务端协议）**：历史活动页签开放并下发 42 条历史活动（点入为空区域）；当期活动 44「雨水滴落的秋之壳与月之雫的翅膀」3 区域 12 关按 `MERCD35_EVENT_WINDOWS` 时间窗开放，推进边界、战斗结算与区域完成率奖励（含限定同伴 1274/1275，`0056` 幂等台账）已落地；周常 7 天×4 档（`quest_id=80000+wday*10+档位`，周六特级逐只复刻原服录播，周一至周五五属性素材本、周末金币本）按 UTC+8 0 点切日。加密协议冒泡与 mlk 回放比对已通过，待实机校准验收（见 [M8 计划](plans/cn35-m8-events-daily-2026-11-19/PLAN.md)）。
- [ ] **试炼、复刻与迷你活动**：日替试炼（依附协力入口，M8 保持关闭）、复刻迷你活动、活动排行及其余 41 个历史活动的关卡内容仍为空态。
- [ ] **协力作战**：区域、房间、组队邀请、任务、排行及奖励仍为空态，尚未形成完整多人流程。
- [ ] **暴走魔宠与收集活动**：暴走魔宠服务端协议与 SQLite `0054` 持久化已落地（`/raid_monsters/*`、首页活动 `banner_raid`／`banner_raid_pt` 快速跳转、探险遭遇强袭与 Poco SQL 绑定，见 [M6 计划](plans/cn35-m6-raid-2026-11-13/PLAN.md)），待模拟器实机校准验收；收集与争夺、其他活动排行仍为空态。
- [x] **癒术士大赛与公会训练（M10 服务端协议）**：即原 TODO「天梯与掠夺战」。对手列表、防御/攻守专用编队、开战与扣次数、胜负结算与对手扣分、战绩复仇、排行与周结算领取、魔宠增幅已落地（`0057`）；叶子奖励因潜能/天赋不复原而折算为徽章，困难难度系数、积分公式等为重建值（见 [M10 计划](plans/cn35-m10-pvp-2026-11-20/PLAN.md) 与 [doc18](docs/18-癒术士大赛与公会训练回放统计.md)）。Poco 候选计划已就绪，**尚未实机校准与验收**。
- [ ] **癒术士学园**：学习、执教、师生申请、出勤和任务奖励仍为空态。
- [ ] **伙伴羁绊与守护者约定**：羁绊列表／进度、真爱小镇与互动等仍为空态。
- [x] **主角成长与天赋**：命运星盘（`/fates/*`）读面、祈愿与战斗／探索属性加成已落地。主角光环（`/main_chara_buff/*`）与同伴天赋（`/passive_skill/*`）属于原国服后期为调整数值追加的养成线，与同伴潜能一样**列入不复原板块**（决策 2026-11-20）：「真·男主光环」按钮保持置灰（`chara_buff_button_disable=true`），星盘汇总中光环分量固定为 `+0`，相关读面保持空态、写面不注册。
- [ ] **特殊招募与票券**：面包店 5 钻兑骑士契约书、十连 9 折（9 本抽 10 次）、国服 Wiki 33 池每日轮换特殊招募、3★/4★/5★ 契约书（含 6 合 1）及 ★3/★4/★5 信（5 枚单抽）的服务端协议、持久化与冒泡用例已落地（见 [主线剧情移植与特殊招募说明](docs/16-主线剧情移植与特殊招募说明.md) 与 [M7 Poco 候选计划](tools/cn350_poco/m7_smoke_plan.json)）；部分节庆/自选池（`discount_gacha`/`show_choice_list`）仍保留空态兜底，待下轮模拟器/真机校准验收。
- [ ] **月卡与运营活动**：月卡、累计签到、召回、节庆奖池、充值／消费奖励等仍为空态；先核实 CN3.5 规则与恢复范围，不代表承诺接入真实支付。
- [ ] **帮助、剧情与资源验收**：帮助静态资源仍有缺口；已提供自备剧情版 APK 主线提取与规范化工具（可还原 CN3.5 第一部 11 章 533 话，叠加资源 100% 闭合，完整服务端冒泡已过），待下一轮结合实机逐章阅读、界面控件校准及核心玩法验收。

核对入口：[页面结构与交互全集](docs/03-页面结构与交互全集.md)、[日常入口与页面跳转](docs/12-日常入口与页面跳转.md)、[公会与公会战契约](docs/11-公会与公会战契约.md)。页面表中的 `ok` 仅表示当时的数据依赖有着落，`empty` 表示空态；历史标记不能直接作为当前功能完成率。

实现依据：[业务路由](server/internal/cn35game/cn35game.go)、[模块空态读面](server/internal/cn35game/module_requiredata.go)、[登录冒险空态](server/internal/cn35game/requiredata.go)。应同时检查精确路由与条件注册，不能只凭空态表中的路径判断功能未实现。

## 环境与版本要求

### 按用途准备工具

| 用途 | 工具与版本要求 | 约束来源 |
|---|---|---|
| 服务端开发与构建 | Go **1.25.0 起**；实际工具链还需满足锁定依赖要求 | [server/go.mod](server/go.mod)、[server/go.sum](server/go.sum) |
| 仓库开发命令 | Git、Make、Bash 与常用 Unix 工具；仓库未固定其版本 | [server/Makefile](server/Makefile)、[server/run.sh](server/run.sh) |
| APK 准备与重打包 | Python 3、Java、apktool **恰好 3.0.3**；Python／通用 Java 运行时未在仓库统一锁版 | [client/baseline.json](client/baseline.json)、[client/build.sh](client/build.sh) |
| APK 对齐、签名、安装 | Android SDK Build Tools（`zipalign`、`apksigner`）；设备安装另需 Platform Tools（`adb`） | Build Tools 未锁定具体版本，脚本默认选择目录排序后的最新版 |
| 单 APK 集成 AAR | 另需 Android NDK、gomobile／gobind、SDK platform 的 `android.jar` | NDK 未锁版；`golang.org/x/mobile` 依赖版本见 `server/go.mod` |
| 单 APK Java 引导器 | 编译侧 **JDK 8 javac**，d8 运行侧 **JDK 11+**（所选 Build Tools 若有更高要求，以其要求为准） | [android-bootstrap/build.sh](client/android-bootstrap/build.sh)；分别用 `JAVAC8`、`D8_JAVA_HOME` 指定 |

只构建服务端不需要 Android、Java 或 Python 工具链。AAR 当前目标为 `android/arm`、API 21，与原客户端 ARM32 ABI 配套；这不是所有 Android 设备均可运行的兼容性承诺。

### macOS / Windows 差异

| 项目 | macOS | Windows |
|---|---|---|
| 服务端命令 | 在具备 Make、Bash 和 Go 的终端执行 | 在具备 Make、Go 和 Unix 工具的 Git Bash／MSYS2，或 WSL 环境执行；PowerShell／CMD 不能直接照搬 Bash 示例 |
| SDK 路径 | 脚本默认 `$HOME/Library/Android/sdk`，其他位置需显式配置 | 显式设置 `ANDROID_SDK_ROOT`；WSL 应使用其内部可运行的 Linux 工具链 |
| Java 选择 | 引导脚本尝试通过 `java_home` 查找 d8 所需 JDK；仍需确保编译侧为 JDK 8 | 不依赖 macOS 的 `java_home`，显式设置 `JAVAC8` 和 `D8_JAVA_HOME` |
| 原生 APK 工具 | 脚本按无扩展名的 `zipalign`、`apksigner`、`d8` 查找 | `.exe/.bat` 查找与执行尚未完整适配；`ZIPALIGN` 可指定 `.exe`，签名需可执行包装脚本，bootstrap 还需处理 `d8.bat` 和 Java classpath 差异 |
| 换行 | 脚本与配置保留 LF | 同样保留 LF，尤其是 `gsp.txt`；历史 SQL 禁止自动换行转换 |

仓库尚未建立 macOS／Windows 最低系统版本与完整客户端兼容矩阵。以上描述当前脚本要求，不宣称所有系统版本均已验收。使用 WSL 时，Go、Java、SDK 与 NDK 应来自同一环境，避免混用 Windows 可执行文件与 Linux 路径。

## 快速启动

以下命令在仓库根目录、具备上述工具的 Bash 环境执行。Go 模块位于 `server/`，根 Makefile 转发开发命令。

```sh
make env       # 首次创建 .env，不覆盖已有文件
# 按需编辑 .env，再继续
make deps      # 下载并校验锁定依赖
make check     # 格式、静态检查和测试
make build     # 输出到 output/rebuilt/
make run       # 加载根目录 .env，前台运行；Ctrl-C 停止
```

默认监听 `127.0.0.1:18091`，玩家存档写入 `state/cn35`，请求与事件记录默认关闭。首次克隆需要执行 `make env`；构建成功并不意味着自备资源已经齐全。

## 环境配置

[.env.example](.env.example) 是公开模板，实际 `.env` 不提交。配置优先级：**显式命令行参数 > 系统环境变量 > 环境文件 > 程序默认值**。环境文件支持 `KEY=VALUE`、`export` 前缀与成对引号，不执行 shell，也不展开变量。

| 变量 | 用途与默认值 |
|---|---|
| `MERCD_ADDR` | 监听地址，默认 `127.0.0.1:18091` |
| `MERCD_SERVER_HOST` | 下发给客户端的地址，默认 `127.0.0.1:18091`；局域网使用时改为客户端可达的具体 IP:端口 |
| `MERCD_STATE` | 独立玩家存档目录，默认 `state/cn35` |
| `MERCD_ASSETS` / `MERCD_CARDS` | CN3.5 配置与共享卡牌数据，默认分别为 `server/assets/cn350`、`server/assets/shared/cards` |
| `MERCD_REMOTE_ASSETS` | 自备远端资源，模板为 `inputs/files`；应指向直接包含 `area/`、`unit/`、`common/` 等资源子目录的那一层 |
| `MERCD_REQUEST_LOG` / `MERCD_EVENT_LOG` | 请求／事件记录，默认留空关闭；启用时使用本地路径 |
| `MERCD35_RAID_BOSS_ID` / `MERCD35_RAID_TRIGGER_RATE` / `MERCD35_RAID_TTL_SEC` / `MERCD35_RAID_SOLO_ASSIST` | 暴走魔宠参数：王宠 ID（默认 `688` 莫鲁菲涅）、探险触发率（默认 `0.5`）、存活秒数（默认 `5400`）、单机模拟协力助战开关（默认 `false`） |
| `MERCD35_BATTLE_DUSK_RATE` / `MERCD35_BATTLE_FEVER_RATE` / `MERCD35_BATTLE_TIME_ADVANCE_RATE` | 探险战斗时段随机（重建值）：开局黄昏概率（默认 `0.25`，金币/资材 ×1.2）、开局夜晚 Fever Time 概率（默认 `0.10`，×2）、每进入下一波时段推进一档的概率（默认 `0.20`）；三项全为 `0` 时恢复全程白天 |
| `MERCD35_EVENT_WINDOWS` | M8 当期活动开放时间窗，格式 `44=<RFC3339 开始>/<RFC3339 结束>`，多个活动用 `;` 分隔；默认留空表示关闭，格式错误时启动失败 |

`make run` 与 `server/run.sh` 总是从仓库根运行，相对路径据此解析。直接执行二进制时，相对路径按当前工作目录解析。使用 `-env-file` 或 `MERCD_ENV_FILE` 可选择其他环境文件；未指定时依次探测 `mercd35.env`、`.env`，显式指定的文件不存在则报错。

`server/run.sh` 支持快捷参数与进程清理：
```sh
# 快速前台启动（若遇端口占用，加 --kill 自动终止旧 mercd35 进程释放端口）
server/run.sh --kill

# 跳过重新构建、启用请求摘要日志并以 LAN 直连模式运行
server/run.sh --no-build -v --addr 0.0.0.0:18091 --server-host 192.168.x.y:18091
```

局域网运行时，监听地址可为 `0.0.0.0:18091` 或本机局域网地址；`MERCD_SERVER_HOST` 与客户端配置的目标地址应一致，不能填 `0.0.0.0`。若自备资源多套了一层 `files/`，同步调整资源路径，例如 `inputs/files/files`。

## 客户端构建模式

| 模式 | 服务端位置 | 资源提供方式 |
|---|---|---|
| 服务端 + APK | 桌面／服务器独立运行 `mercd35` | APK 连接外部服务，远端资源通过网络下发 |
| 单 APK（集成 AAR） | APK 内通过 gomobile 引导服务 | 自备远端资源并入包内；使用 ARM32 AAR 与整包 staging 树 |

两种模式不能混用。开始打包、起服或安装排查前，先阅读 [CN3.5 APK 打包工作流](skills/cn35-apk-packaging/SKILL.md)，操作入口见[运行与材料](docs/运行与材料.md)。原包只读，解码前按 `client/baseline.json` 校验 SHA-256；客户端编辑以 `client/decoded/` 为源，不修改原游戏 DLL 业务逻辑。

## 开发与验证

| 命令 | 用途 |
|---|---|
| `make fmt` | 格式化 Go 代码 |
| `make check` | 检查格式，执行 `go vet` 与测试 |
| `make race` | 竞态检查，需要相应平台的 CGO／C 编译器环境 |
| `make build` | 构建当前平台的 `mercd35`、`mercdb`（含种子存档工具），关闭 CGO |
| `make cross` | 构建 Windows amd64、Linux amd64／arm64 版本 |
| `make api` | 根据实际路由生成 API 文档 |

仓库另内置 `mlk` 原服抓包回放契约校验（`server/internal/cn35game/mlk_replay_verify_test.go`）、协议压测工具（`server/cmd/cn35load`）以及 Unity Poco 模拟器／真机自动化验收套件（[tools/cn350_poco/README.md](tools/cn350_poco/README.md)）。

缺少真实故事或 bundle 时，相关测试会明确 skip。服务端单元测试、`mlk` 回放校验或 Poco 静态检查通过，均不代表完整游戏已通过实机验收；客户端仍需单独验证冷启动、教程、页面交互、资源加载、核心玩法及重启存档。

已有数据库升级前必须做一致性备份；恢复时使用配对二进制与新的 state 目录。不得通过重启重置存档或共享 fixture 代替玩家持久化。

## 文档与数据

| 文档 | 内容 |
|---|---|
| [完整技术文档索引](docs/README.md) | 页面全集、协议、数值、事务、公会与资源接入 |
| [运行与材料](docs/运行与材料.md) | 服务端与客户端入口、自备材料和验证边界 |
| [数据来源](docs/数据来源.md) | 原作观测、社区资料、重建数据与权利说明 |
| [数据来源与运行依赖](docs/01-数据来源与运行依赖.md) | 运行所需配置、模板与共享数据 |
| [自备输入目录](inputs/README.md) | APK、Unity 素材等本地输入的放置约定 |

`server/assets/shared/cards` 是当前运行依赖，不能因离线工具也读取就删除。纪念版补缺数据与 CN3.5 配置包含混合来源，保留来源声明；未验证假设与原作观测应明确区分。

## 提交与许可

提交源码、文档、`go.mod`、`go.sum` 和公开配置模板。不提交 `.env`、客户端解码树、APK、Unity 资源、剧情正文、原始抓包、玩家库、WAL、备份、日志、运行库、签名或 SDK 秘密。

Go 代码使用 `gofmt`，Makefile 使用 tab，脚本使用 LF。历史 SQL 按原字节保留，`.gitattributes` 禁止换行转换，不改写或删除既有迁移。

[MIT LICENSE](LICENSE) 适用于有权许可的代码和文档；第三方数据的来源与权利说明见[数据来源](docs/数据来源.md)。
