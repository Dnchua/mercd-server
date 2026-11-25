# 开发约束

- 使用中文沟通。CN3.5 是当前范围；不重新引入已退役版本业务代码。
- 原始 APK 只读；解码前按 client/baseline.json 核对 SHA-256。
- 客户端编辑与构建以 client/decoded/ 为源。不得修改原游戏 DLL 业务逻辑；游戏恢复通过服务端协议与配置下发。
- 不提交 decoded 树、APK、Unity 资源、原始抓包、运行库、签名或 SDK 秘密。
- 玩家数据使用独立 state/SQLite；不得通过重启重置或共享 fixture 代替持久化。
- 历史 SQL 不改写、不删除；已有库升级前一致性备份，恢复配对二进制与新 state 目录。
- 文档区分原作观测、社区资料、重建值与未验证假设；没有实机证据不能宣称完整游戏验收。

## 强制工作流

- 凡是涉及 APK 打包、重打包、客户端构建、gomobile/AAR 集成、服务端运行、安装到模拟器/真机、或排查"数据解压中"等启动卡死的任务，必须先阅读 `skills/cn35-apk-packaging/SKILL.md` 再动手；该文件定义了"服务端+APK"与"单 APK（集成 AAR）"两种互斥模式，混用是卡解压的主因。

## 录播证据与 M10 验证

- mlk 原服录播解密目录丢失时，按 `docs/18-癒术士大赛与公会训练回放统计.md` §0.1 从 `mlk虚拟服务器/*.mlk` 只读重建到仓库外临时目录（S1 为客户端未播种 `lrand48` 首 32 次取模 62）；明文不入库。
- M10 验证：`cd server && go test ./internal/cn35game/ -run TestM10 && go test ./internal/cn35store/ -run TestPvP`；有解密目录时加 `MLK_CAPTURE_DIR=<目录>` 跑 `TestM10CaptureReadFaceReplay`；Poco 离线门禁 `python3 -m unittest discover -s tools/cn350_poco -t . -p 'test_m10*.py'`。

## 实机调试备忘（2026-11-21）

- 当前模拟器 `192.168.1.44:16416`（MuMu，x86_64+houdini），宿主机 LAN IP `192.168.1.69`；模式 A 启动：`MERCD_SERVER_HOST=192.168.1.69:18091 MERCD_STATE=<验收副本> ./output/rebuilt/mercd35`（`.env` 中的旧 IP 需用环境变量覆盖）。
- decoded 树已含 `MainActivity$PocoLoader`（启动 30s 后 `loadLibrary(cn350poco)`），只需把测试桥 `Cn350.Poco.Runtime.dll` 用 `run-as` 拷到 `files/`，Poco 即监听 5001，无需 Frida；首装停在权限页时该加载会崩，授权后重启即可。
- 服务端：store 事务回调内禁止调用 `dep.Catalog`/任何 Store 方法（单连接 SQLite 会死锁）；execute 文档的 `asset_names` 不得重复（客户端 `textures.Add` 重复键即加载失败）。
- 战斗下发：客户端 `Unit.OnWalk` 触旗即清波，不等 appear_table 剩余项——Boss/后续出怪不能离末组太远（多波 Boss 关标记 ≤ 末只普通怪 +500ms，`TestStoryMultiWaveBossFollowsLastGroup` 守护）；主线战表改动走 `../../数值维护/experiments/story_encounter_v2.py` → `server/tools/story_catalog_v2_build.py`。
- 战斗结算后的跳页不得用 `method=Stack`（当前页是已终止的 Quest 页，返回即黑屏）；`/quests/result` 遭遇暴走时不得下发顶层 `next_page_message`（会覆盖 RaidAppearModal 的选择）。
