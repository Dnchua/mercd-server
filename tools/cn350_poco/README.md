# CN3.5 Unity Poco 调试工具

从 cn-mercd-backend 迁入。Airtest 负责设备连接、输入、截图和录像，UnityPoco 负责读取 Unity 控件树，Frida 负责加载独立测试桥。测试桥只用于调试进程。

## 安装与控件树

在仓库根目录运行。验收服务控制器使用 POSIX 进程管理，以下命令适用于 macOS 和 Linux：

```sh
python3 -m venv output/poco/venv
source output/poco/venv/bin/activate
python -m pip install -r tools/cn350_poco/requirements.txt
python -m tools.cn350_poco.init_config
```

编辑 `tools/cn350_poco/config.local.json`，填写设备序列号（`Android:///<ADB 序列号>`）、测试账号及设备实际安装的 `base.apk` SHA-256。只查看 A 的控件树时，配置中可只保留 A。通过 `adb -s <序列号> shell pm path com.happyelements.miraclecn.uc.cn350dbg` 找到已安装包路径，再用设备端 `sha256sum` 计算哈希。

默认 `jni-load` 需要设备上已有版本匹配的 Frida 服务，以及调试包私有 `files/` 下的可信 ARM32 测试桥 `libcn350poco.so` 和 `Cn350.Poco.Runtime.dll`。填写对应 `so_sha256`、`dll_sha256`。如设备已有可用 Unity Poco 服务，可选 `attached`。另支持 `mono-inject`，需提供兼容的 SDK 与桥接程序集及其 SHA-256，以及经预检确认的 Frida 设备 ID、Mono 模块、唯一 Unity 主线程名称或本次线程 ID。可用 `python -m tools.cn350_poco.build_bootstrap --help` 查看独立入口的编译参数。SDK 和运行桥由操作者提供。

```sh
# 客户端启动后，可先检查 PID、ABI、Mono 模块和线程
python -m tools.cn350_poco.probe --frida-device <Frida设备ID> --adb-serial <ADB序列号> \
  --package com.happyelements.miraclecn.uc.cn350dbg --output output/poco/preflight

python -m tools.cn350_poco inspect --config tools/cn350_poco/config.local.json --role A
python -m tools.cn350_poco calibrate --config tools/cn350_poco/config.local.json --role A --selector nav.guild
```

`inspect` 导出真实树。按目标页面逐项调整并校准 `selectors.local.json`，校准要求控件唯一且可见，并绑定 APK 指纹和树文件。连接失败时按错误排查设备、ABI 和测试桥。脚本为每台设备分配独立的 ADB 转发端口，客户端重启后会重新检查并按需注入。

## 独立服务端验收

先按 [CN3.5 APK 打包技能](../../skills/cn35-apk-packaging/SKILL.md) 准备「服务端 + APK」模式的调试包。验收服务默认监听 `0.0.0.0:18092`。将配置中的 `server_host` 和调试 APK 中的 gsp 地址设为同一个设备可达的 `<LAN_IP>:18092`。外部资源需要在配置的 `server_args` 中添加 `["-remote-assets", "<直接包含 area、unit 等目录的绝对路径>"]`。

准备专用测试账号。默认公会计划需要两个设备及两个独立账号，初始均未入会，并按计划准备资材、防卫魔宠和测试队伍。核心操作候选计划可通过 `--plan tools/cn350_poco/core_ops_plan.json` 选择，配置中只保留 A 账号即可。检查候选计划中的页面路径、选择器、协议、SQL 断言和变量是否匹配当前包及存档。

```sh
make build
python -m tools.cn350_poco.server_control clone --config tools/cn350_poco/config.local.json --source-db <专用测试库>
python -m tools.cn350_poco.server_control restart --config tools/cn350_poco/config.local.json
python -m tools.cn350_poco validate --config tools/cn350_poco/config.local.json
python -m tools.cn350_poco.server_gate --output output/poco
# 将 Go 门禁生成的 result.json 路径填到 config.local.json 的 server_result
python -m tools.cn350_poco run --config tools/cn350_poco/config.local.json --case G01 --record
python -m tools.cn350_poco.server_control stop --config tools/cn350_poco/config.local.json
```

`clone` 通过 SQLite 一致性备份创建新副本，默认目录为 `state/poco/acceptance`，已有目标会拒绝覆盖。`restart` 沿用副本。每轮需要新快照时，给 `db` 和 `requests` 配置新的专用目录，库名固定为 `mercd35.sqlite3`。公会测试涉及建会、退会等真实写入，仅对测试副本执行。

`validate` 只做结构检查。`server_gate` 执行本仓库的公会和存储回归。`--case` 包含前置用例，报告为部分验收。省略它可执行整套已校准计划。运行证据保存在 `output/poco/`，包括树、截图、请求、SQL 断言和可选录像。配置、测试桥二进制、APK、存档和原始运行材料均留在本地。

离线回归入口为 `python3 -m tools.cn350_poco.test_offline`。公会覆盖范围见 [候选验收矩阵](ACCEPTANCE.md)。

## M5 养成与战斗字段候选验收

计划与任务见 [M5 计划](../../plans/cn35-m5-2026-11-08/PLAN.md) 和 [任务清单](../../plans/cn35-m5-2026-11-08/TASKS.md)。`m5_plan.json` 提供 12 个基线场景，当前尚未实现/校准；新状态的只读查询需在实现后绑定，不猜测未来 schema。

`python3 -m tools.cn350_poco.m5_acceptance check` 仅做静态检查。最终入口为 `python3 -m tools.cn350_poco.m5_acceptance run --config tools/cn350_poco/config.m5.local.json --record`，要求已定稿计划及当前 M5 专用服务端门禁，不能沿用公会门禁结果。完整配置和扩展场景见 [M5 验收说明](../../plans/cn35-m5-2026-11-08/ACCEPTANCE.md)。

## M6 公会设施战斗加成与暴走魔宠（Raid）候选验收

计划与验收矩阵见 [M6 计划与待审清单](../../plans/cn35-m6-raid-2026-11-13/PLAN.md) 与 [M6 验收规格](../../plans/cn35-m6-raid-2026-11-13/ACCEPTANCE.md)。`m6_raid_plan.json` 覆盖 `M601`~`M610` 共 10 个候选场景（公会设施 Lv.100+ 读面、日常探险战斗加成、暴走强袭弹窗、首页「活动」快速跳转、出击准备、退场伤害上报、双通道协力呼叫、最终击退与 Lv 奖励、pt 交换所与放走、同库重启一致性）；新 SQLite 表与选择器须在清单审核通过、服务端实现及模拟器控件校准后绑定。

`python3 -m tools.cn350_poco.m6_raid_acceptance check` 仅做离线三方门禁静态检查。实机/模拟器最终入口为 `python3 -m tools.cn350_poco.m6_raid_acceptance run --config tools/cn350_poco/config.m6.local.json --record`，要求 `implementation_ready=true`、校准后的本地选择器树及 M6 专用服务端门禁。

Poco 中发现新的 Unity 控件 ID/name/稳定路径时，必须补全 [控件映射表](UNITY_CONTROLS.md) 和对应选择器映射，记录已安装 APK 指纹与真实树校准证据。未观测的候选别名不能写成已验证 ID。
