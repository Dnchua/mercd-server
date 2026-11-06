---
name: cn35-apk-packaging
description: 引导式打包 CN3.5 梅露可物语重打包 APK。先让用户在两种模式中选择：(A) 服务端+APK——files/remote_assets 由桌面 mercd35 下发，APK 内不嵌服务；(B) 单 APK（集成 AAR）——gomobile 把 mercd35 编进 APK，BootProvider 进程内起服，inputs/files 并入 assets/unity 随包解压。当用户要求打包、重打包、构建 APK、单 APK、集成 AAR，或游戏卡在"数据解压中"时使用。
---

# CN3.5 APK 打包引导

本 skill 是一个**交互式 workflow**：不要一次跑完所有命令，按步骤推进，每步先验证前置条件再执行。

## 步骤 0：确认打包模式

**必须先问用户选哪种模式**（用 ask_user_question）：

- **模式 A · 服务端 + APK**：桌面/服务器跑 `mercd35`，`inputs/files`（remote_assets）由服务端通过网络下发。APK 较小（约 325 MB 级 unity），但需要外部服务可达。**默认走 LAN IP**：模拟器和真机都填同一个内网 IP（如 `192.168.x.y:18091`），不要用 `127.0.0.1`（模拟器默认走 LAN IP 目的地；`127.0.0.1` + `adb reverse` 仅作为备用兜底，见下方说明）。
- **模式 B · 单 APK（集成 AAR）**：`gomobile bind` 生成 `mercd35.aar`，`BootProvider` 在进程内起服（127.0.0.1:18091）；`inputs/files` 并入 `assets/unity`，`merge_asset_manifest.py` 生成 5 字段无 MD5 清单，客户端全部直接从 APK 内 `assets/unity/`（`StreamingAssets`）读取；构建期由 `mercdb cn35-seed-db` 合成已完成最新 SQL 迁移与静态发布的 `assets/backend/seed.sqlite3`（及记录截止 SQL 版本的 `seed_meta.json`），`BootProvider` 首启时直接复制 `seed.sqlite3` 就位并解压轻量 `assets/backend/{cn350,cards}` 配置、清理外部存储旧清单缓存（`asset_info_android4_v2_short.json.encode`），`BootActivity` 等待服务就绪后进入 Unity。无需首启解压 17,267 个文件，也避免在 32 位 `libhoudini.so` + `wazero` 下冷启动执行数十条 SQL 迁移与上万次 INSERT 导致卡黑屏闪退。

模式决定一切后续步骤，**不可混用**：模式 A 若残留 BootProvider/classes2.dex/assets/backend，包内服务会抢占 18091 并遮蔽外部服务端（这是"卡数据解压中"的常见根因）；模式 B 若缺 `inputs/files` 并入，客户端会永远等远程资源下载。

## 步骤 1：环境前置检查（两种模式共用）

逐项检查，缺哪个就停下来让用户装/配：

```sh
java -version                                   # apktool/d8 需要；d8 需 JDK 11+（javac 侧用 JDK8 + bootclasspath）
python3 --version                               # prepare.py / build.sh 读 baseline.json
apktool --version  # 或 APKTOOL_JAR 指向的 jar   # 必须恰好 3.0.3（client/build.sh 强校验）
```

- 原包：`inputs/apk/国服-3.5.0.apk` 存在，SHA-256 须等于 `client/baseline.json` 的 `originalApkSha256`（`d9412c15…9118a2`）。`prepare.py` 会自动校验。
- Android SDK：`ANDROID_SDK_ROOT`/`ANDROID_HOME` 下有 `build-tools`（zipalign；签名还要 apksigner）和 `platforms/android.jar`。
- 签名（`--sign` 时必需）四个环境变量：`ANDROID_KEYSTORE`、`ANDROID_KEY_ALIAS`、`ANDROID_KEYSTORE_PASS`、`ANDROID_KEY_PASS`。没有 keystore 可引导用户生成：
  ```sh
  keytool -genkeypair -v -keystore mercd.keystore -alias mercd -keyalg RSA -keysize 2048 -validity 10000
  ```
- Windows 注意：所有脚本都是 bash（Git Bash / MSYS2 / WSL 任一），`client/android-bootstrap/build.sh` 里 `/usr/libexec/java_home` 是 macOS 专有——Windows 上须显式设 `D8_JAVA_HOME`（JDK 11+）与 `JAVAC8`（JDK8 javac）；`cp -Rc` 的 clonefile 会自动 fallback 到普通 `cp -R`，无需处理。

### 步骤 1.5：确定 LAN IP（模式 A 必做，先于解码）

先在宿主机取内网 IP，后续 `prepare.py --server-host`、`.env` 的 `MERCD_ADDR`/`MERCD_SERVER_HOST`、gsp.txt **三处必须用同一个值**：

```sh
# Windows（Git Bash）
ipconfig | iconv -f GBK -t UTF-8 | grep -iE "IPv4|适配器"        # 取形如 192.168.x.y
# macOS / Linux
ipconfig getifaddr en0   # 或 ip route get 1 | awk '{print $7; exit}'
```

- 取**物理网卡**（以太网/WLAN）的 IPv4，例如 `192.168.50.92`；不要取 `172.x` 的 vEthernet/Hyper-V 虚拟网卡，也不要取 `127.0.0.1`。
- 全套示例把 `<LAN_IP>` 替换为该地址，端口沿用 18091：`<LAN_IP>:18091`。
- 服务端须绑定 `0.0.0.0:18091`（或该 LAN IP），仅绑 `127.0.0.1` 时局域网设备连不上（Windows 防火墙可能还需放行 18091）。
- 后续统一口径：`MERCD_ADDR=<LAN_IP>:18091`、`MERCD_SERVER_HOST=<LAN_IP>:18091`、gsp.txt 第 6 字段 `<LAN_IP>:18091`。**三处不一致是"请求连不上/卡登录"的常见根因。**

### 步骤 1.6：核对 MERCD_REMOTE_ASSETS 并 curl 验证资源可达（模式 A 必做）

**`MERCD_REMOTE_ASSETS` 必须指向真正含资源的目录。**

本仓库的 remote_assets 常见多一层 `files/`：资源实际在 `inputs/files/files/`（`area/`、`unit/`、`common/`… 直接在此层），
而 `.env` 很容易误写成上一层 `inputs/files`。**写错一层的后果**：服务端算出的逻辑名会多出 `files/` 前缀
（如 `files/area/area_10` 而非 `area/area_10`），AssetType 前缀匹配全失败 → 扫描结果
`entries=0 rejected=9937`，客户端拿到的清单为空 → 卡"数据解压中"。

```sh
# 1) 找真正的资源层：目录里应直接看到 area/ unit/ common/ 等同名子目录
ls inputs/files/            # 若这里只有一个 files/ 子目录 → 真正的资源层在它下一层
ls inputs/files/files/      # ✅ 正确层：直接含 area/ unit/ common/ background/...
# 2) 与 .env 对齐：MERCD_REMOTE_ASSETS 的值应等于上面看到的“真正资源层”路径
grep MERCD_REMOTE_ASSETS .env      # 应为 MERCD_REMOTE_ASSETS=inputs/files/files
```

启动后**必须用日志 + curl 双重核对**（不要只看进程起来了）：

```sh
# 日志：entries 应≈资源数、rejected=0；出现 entries=0 rejected>0 就是路径写错层级
grep "远端资源扫描完成" state/cn35/server-console.log
# 期望：dir=inputs/files/files entries=9937 pending=0 rejected=0

# curl：诊断路由应返回非空 entries，且 remote_dir 指向真正资源层
curl -s http://<LAN_IP>:18091/cn35/assets/remote | head -c 400
# 期望：{"asset_info_version":...,"remote_dir":"inputs/files/files","entries":[...9937 条...]}

# curl：抽查一个具体资源确实可下载（把名称换成 entries 里任意一条，或下列示例）
curl -s -o /dev/null -w "%{http_code} %{size_download}\n" \
  "http://<LAN_IP>:18091/asset_info_android4_v2_short.json.encode"
# 期望：200 且 size 非 0；从模拟器侧也 curl 一次，确认设备可达同一地址
adb shell "curl -s -o /dev/null -w '%{http_code}\n' http://<LAN_IP>:18091/mercd/health"
```

判定：`entries>0 且 rejected=0` + curl 返回 200 且体积非 0 = 资源层配对正确；否则回到本步第 1 项逐层核对。


## 步骤 2：解码客户端（两种模式共用，每棵树只做一次）

`client/decoded` 已存在时 `prepare.py` 拒绝覆盖。若已存在且模式未变可跳过；若要切换模式（A↔B），先确认 decoded 树归属，**切到 B 需要重新 prepare**（Manifest 要注入 provider）：

```sh
# 模式 A（默认走 LAN IP：<LAN_IP> 换成步骤 1.5 取到的内网 IP，模拟器与真机同一地址）
python3 client/prepare.py --mode server --server-host <LAN_IP>:18091

# 模式 A 备用：模拟器 + adb reverse（不改 LAN，走 127.0.0.1 隧道；仅当 LAN 不可达时用）
python3 client/prepare.py --mode server --server-host 127.0.0.1:18091

# 模式 B（server-host 固定 127.0.0.1:18091，脚本强制）
python3 client/prepare.py --mode standalone
```

prepare 只改两处：Manifest（改包名 `com.happyelements.miraclecn.uc.cn350dbg`、debuggable；standalone 额外注入 `com.mercd.backend.BootProvider`）和 `assets/gsp.txt`（第 6 字段服务地址 + 第 8 字段登录开关，见步骤 2.5）。**DLL 不做任何修改**——若用户声称要改 DLL，指出服务地址在 gsp.txt，不在 DLL。

### 步骤 2.5：核对 gsp.txt（打包前必做，Windows 重点）

gsp.txt 共 10 个分号分隔字段。除第 6 字段（服务地址）外，**第 8 字段是登录开关**，
且**行尾必须是 LF**——这两处错了都会表现为"卡数据解压中"/"登录按钮点了没反应"。

```
1: miracle_androidcn_prod
2: 7800107601
3: uc
4: uc
5: (空)
6: <LAN_IP>:18091        ← 服务地址
7: uc
8: false                 ← ⚠️ 登录开关：必须 false
9: true
10: true
```

- **第 8 字段必须 `false`**：`true` 会启用 UC SDK 账号登录流程。但按 3A-6 阉割掉 UC SDK 后
  该流程无法完成——点击"登录"按钮后 logcat 只打 `Click Login Button`，**不发出任何 HTTP 请求**，
  服务端只见 `methods:["metaInfo"]`，流程死在 `step 2 login`。
  设 `false` 后登录 UI 提供**"游客试玩"**入口，点击即走 `heQuickLogin → getServerList →
  chooseServer → version_info → asset_info → preset_data → app_conf`，
  最终 `Page Load Done : Top`。原包该字段为 `true`，`prepare.py` 现已默认改写为 `false`。
- **行尾必须 LF，不能 CRLF**：Windows 上 Python `write_text()` 会把 `\n` 翻译成 `\r\n`，
  使末字段变成 `true\r`，客户端解析异常 → 卡"数据解压中"。`prepare.py` 现已改为二进制读写强制 LF。
  校验（`$` = LF，`^M$` = CRLF，后者必须修）：
  ```sh
  cat -A client/decoded/assets/gsp.txt | head -1
  # 正确：...;uc;false;true;true$
  # 错误：...;uc;false;true;true^M$
  ```

## 步骤 3A：服务端 + APK 流程

1. **清掉单机残留**（若 decoded 树曾被 android-bootstrap/build.sh 碰过）：
   ```sh
   rm -f client/decoded/classes2.dex client/decoded/lib/armeabi-v7a/libgojni.so
   rm -rf client/decoded/assets/backend
   grep -n BootProvider client/decoded/AndroidManifest.xml   # 有残留则需重新 prepare（provider 是 prepare 写入的）
   ```
2. 打包：
   ```sh
   client/build.sh --sign    # 或去掉 --sign 仅产出 aligned-unsigned
   ```
   产物在 `output/rebuilt/mercstoria-cn350*.apk`。
3. 配服务端 `.env`（`make env` 生成）——**三处地址必须与步骤 1.5 的 `<LAN_IP>` 完全一致**：
   ```
   MERCD_ADDR=<LAN_IP>:18091                # 或 0.0.0.0:18091 监听全网卡；不要用 127.0.0.1（局域网设备连不上）
   MERCD_SERVER_HOST=<LAN_IP>:18091         # 必须是客户端可达地址；与 prepare.py --server-host、gsp.txt 第 6 字段一致
   MERCD_REMOTE_ASSETS=inputs/files/files   # ⚠️ 指向“真正含资源的层”，常比 inputs/files 多一层；见步骤 1.6
   ```
   `MERCD_REMOTE_ASSETS` 必须存在且有内容，**且层级正确**（写错层级 → `entries=0 rejected=9937` → 客户端清单为空）。启动后务必按**步骤 1.6** 用日志 `entries/rejected` + `curl /cn35/assets/remote` + 直接 curl 具体资源三重核对。
4. 起服 `make run`（服务端须绑 `0.0.0.0`/LAN IP，Windows 防火墙放行 18091），然后安装并连通：
   ```sh
   adb install -r output/rebuilt/mercstoria-cn350.apk
   # 默认走 LAN IP：模拟器直接访问 http://<LAN_IP>:18091，无需 adb reverse
   ```
   **仅当 LAN 不可达时的备用兜底**（用 127.0.0.1 隧道，此时 prepare 与 .env 都要同步改回 127.0.0.1）：
   ```sh
   adb reverse tcp:18091 tcp:18091     # 杀进程/重连后失效，每次调试用前重设
   ```
5. 验证：**先从设备侧验证 LAN 可达**再进游戏：
   ```sh
   adb shell curl -s http://<LAN_IP>:18091/mercd/health   # 宿主机也可 curl 同一地址
   ```
   卡"数据解压中"时抓 `adb logcat -s Unity:*` 与服务端请求日志（`MERCD_REQUEST_LOG`）对照——客户端首启会向服务端拉 remote_assets 清单并下载，服务端日志无请求即网络没通。
6. **UC SDK `errorCode=493` 绕过（方案 1 · 阉割）**：Android 12/API 32 上 UC SDK loader 会反射调用
   已被移除的 `AssetManager.addAssetPaths` → `NoSuchMethodException` → `加载模块dispatcher失败` →
   `强制回滚失败` / `errorCode=493`，卡在 `step 2 login`。`settings put global hidden_api_policy 1`
   **无效**（方法确实不存在，不是被策略拦）。已在实机验证有效的绕过法是**删掉 UC SDK 的 loader 外壳与模块，
   让它“安静降级”**（mac 打包的可用包即此法）：
   ```sh
   # 删除 loader 外壳 + 9 个模块 APK；保留 modules/list、modules/version.ini 与 native .so
   rm -f client/decoded/assets/ucgamesdk/lib/ucsdk.apk
   rm -f client/decoded/assets/ucgamesdk/modules/*.apk
   ```
   重打包安装后，logcat 应从「`addAssetPaths` 崩溃 + `errorCode=493` + `强制回滚`」变为**可降级的**
   「`释放assets插件失败` + `FileNotFoundException: ucgamesdk/lib/ucsdk.apk`（`UCGameSdk.java:113`）」，
   `493` 消失、主流程不再被拦。**注意**：这只解决“SDK 崩溃”，不解决“真正 login 方法未发出”
   （服务端仍只见 `methods:["metaInfo"]`）——后者要靠步骤 2.5 把 gsp 第 8 字段设为 `false` 解决。
7. **对比“别处打包的可用包”时，必须先确认设备上跑的到底是不是那个包**：
   设备上的包随时可能被人重装过，`lastUpdateTime` 与早期 pull 下来的副本很可能不是同一份。
   **不要拿旧副本下结论**，每次都重新从设备取：
   ```sh
   adb shell "pm path com.happyelements.miraclecn.uc.cn350dbg"      # 取真实路径
   adb shell "dumpsys package com.happyelements.miraclecn.uc.cn350dbg | grep lastUpdateTime"
   adb pull /data/app/~~xxx==/com.happyelements.miraclecn.uc.cn350dbg-yyy==/base.apk /tmp/live.apk
   ```
   然后与本地产物逐项对齐（`unzip -p <apk> assets/gsp.txt`、`unzip -l | grep classes2.dex`、
   `libgojni.so`、`assets/backend/`）。**判定内置服务端是否真的存在，只看三件套是否齐全**：
   `classes2.dex` + `lib/armeabi-v7a/libgojni.so` + `assets/backend/`。
   只带 `classes2.dex` 而缺后两者 = **引导器空壳，不算内置服务端**（它仍会去请求外部 gsp 地址）。


## 步骤 3B：单 APK（集成 AAR）流程

1. 确认 `inputs/files/` 存在且为用户自备的完整 remote_assets（缺失会导致卡"数据解压中"，这是该模式最常见失败）。
2. 生成 AAR（需要 Go ≥ go.mod 要求 + Android NDK；gomobile 需已 `gomobile init` 或新版直接可用）：
   ```sh
   cd server && gomobile bind -target=android/arm -androidapi 21 -o /tmp/mercd35.aar ./mobile
   ```
   只用 `android/arm`：客户端是 ARM32-only，多 ABI 会导致模拟器/64 位机闪退（build.sh 内注释有完整原因）。Windows 上输出路径如 `C:/temp/mercd35.aar`，记得同步改 `AAR=`。
3. 跑 bootstrap（编译 BootProvider→classes2.dex、拷 libgojni.so、拷 assets/backend、调用 `mercdb cn35-seed-db` 生成预置种子库 `seed.sqlite3` 与截止 SQL 版本元数据 `seed_meta.json`、生成 decoded-single 并并入 inputs/files + 重生成 5 字段无 MD5 资源清单）：
   ```sh
   AAR=/tmp/mercd35.aar client/android-bootstrap/build.sh
   ```
   成功标志：输出 `seed_db=... cutoff_migration=0049_...` 与 `5 字段无 MD5 模式（直读 APK 内 StreamingAssets）`。缺 inputs/files、清单引用资源不存在或资源非法时脚本直接失败，不得继续打包。脚本支持多套一层 files/ 的输入。升级时必须重建 AAR，BootProvider 仅在 `state/mercd35.sqlite3` 不存在时从 `seed.sqlite3` 初始化，已有存档原样保留并按 `schema_migrations` 增量执行 `cutoff_migration` 之后的新 SQL。
4. 打包整包 staging 树（不是 decoded）：
   ```sh
   CLIENT_SOURCE_DIR=client/decoded-single client/build.sh --sign
   ```
5. 安装验证：`adb install -r`；首次启动 logcat 过滤 `mercd-boot` 应见 `backend assets extracted` / `seeded initial database from backend/seed.sqlite3 (..., cutoff_migration=...)` / `Mobile.start returned`，等待页约 2 秒结束后直接进入 Unity（无需解压 1.45 GB `.unity3d` 文件）；资源加载直读 APK 内 `StreamingAssets`，无 bundle HTTP 请求；之后进游戏不再依赖任何外部服务，`adb reverse` 不需要也不要配（避免端口歧义）。

## 验收判据：怎样才算“真的进游戏”

启动后抓 `adb logcat` 过滤 Unity，**见到 `Page Load Done : Top` 才算进游戏**。中途的两条告警
不是失败，不要据此判定"包有问题"：

```sh
adb logcat -d | grep -aE "I Unity|W Unity" | grep -aviE "Track UserPerformance|Filename:"
```

正常成功链路（实测）：
```
<Start Info> → uc InitPlatform → step 1 NoBetaUser Skip → step 2 login
→ [点“游客试玩”] Request verify isNewUser
→ step 3 loadServerList → step 4 ChooseServer → step 5 AfterAwake
→ asset_info / preset_data / app_conf  全部 200
→ Load asset bundles for quest0 → Page Load Done : Top   ✅
→ [点“Game Start!”] Page Load Done : IntroduceText_1     ✅ 开场剧情
```

**可忽略的告警**（已知行为，不阻断流程）：
- `Request Decompress static file! e.msg:Bad state (unknown compression method (0x76/0x7B))`
- `Error... Page : Conf is Empty! Please Check app_conf!`

出现这两条后流程仍会继续走到 `Page Load Done : Top`，**不要把它们当成卡死证据**。
真正的失败是：停在 `step 2 login` 且点击登录无请求发出（→ gsp 第 8 字段问题），
或画面长时间停在"数据解压中"且服务端无新请求（→ gsp CRLF / 资源层 / 网络问题）。

## 常见坑速查

| 症状 | 根因 |
|---|---|
| 一直"数据解压中" | 模式混用：decoded 带 BootProvider 但 remote_assets 没并入；或模式 A 的 inputs/files 为空/服务端不可达 |
| 装不上/闪退 `Unable to find native library main` | APK 里出现 armeabi-v7a 以外的 ABI 目录；bootstrap 只应拷 armeabi-v7a |
| `apktool 3.0.3 required` | 版本强校验，换 `APKTOOL_JAR` 指向 3.0.3 |
| `client/decoded 已存在` | prepare 防覆盖；切换模式先备份/删除重建 |
| 起服冲突 18091 | BootProvider 残留（模式 A 误带包内服务），按 3A-1 清理 |
| 改过 APK 后又卡解压 | `adb reverse` 掉了；或 decoded-single 是旧 staging，重跑 android-bootstrap/build.sh |
| 请求连不上 / 卡登录 | 三处地址不一致：`MERCD_ADDR`、`MERCD_SERVER_HOST`、gsp.txt 第 6 字段没有用同一个 `<LAN_IP>`；或服务端只绑了 127.0.0.1 / 防火墙拦了 18091 |
| 日志 `entries=0 rejected>0`、清单为空、卡数据解压 | `MERCD_REMOTE_ASSETS` 层级写错（常见少了/多了 `files/` 一层）；按步骤 1.6 定位真正资源层并用 curl 核对 |
| 卡"数据解压中"且资源清单/网络都正常 | gsp.txt 行尾是 **CRLF**（Windows `write_text()` 引入），末字段变成 `true\r`；按步骤 2.5 改回 LF |
| 登录按钮点了无反应、服务端只见 `methods:["metaInfo"]` | gsp.txt **第 8 字段为 `true`**，启用了已被阉割的 UC SDK 登录；按步骤 2.5 改为 `false`，改用"游客试玩"入口 |
| 误判"某个包内置了服务端" | 只带 `classes2.dex`、缺 `libgojni.so`+`assets/backend/` = 引导器空壳；且设备上的包可能已被重装，按 3A-7 重新 pull 再判 |
| `zipalign not found` / `apksigner not found`（Windows） | `build.sh` 的 `find`+`test -x` 在 Windows 上匹配不到无扩展名文件；显式 `ZIPALIGN=<sdk>/build-tools/<ver>/zipalign.exe`，签名用 `cmd //c apksigner.bat`（`test -x` 不认 .bat） |
| 装不上 `INSTALL_FAILED_UPDATE_INCOMPATIBLE` | 设备上已装同包名但签名不同的包（如别处打的 DEBUG 签名）；先 `adb uninstall` 再 `install` |
| 卡 `step 2 login`（服务端只见 `methods:["metaInfo"]`） | 阉割 UC SDK 后真登录走不通；按步骤 2.5 把 gsp 第 8 字段设 `false`，走“游客试玩”。**不需要模式 B**，模式 A 亦可完全走通 |

| 卸载重装/全新安装后卡黑屏"正在启动单机服务…"并在 30~100s 后闪退（`libhoudini.so` SIGILL/SIGSEGV 或 `wazero` nil panic） | APK 缺少预置 `assets/backend/seed.sqlite3`，导致 32 位 ARM + `libhoudini.so` + `wazero` 从零跑数十条 SQL 迁移与近万次 `ImportRoster` INSERT 触发栈展开/解释器崩溃；重跑 `client/android-bootstrap/build.sh` 生成 `seed.sqlite3` + `seed_meta.json` 后重打包 |

## 关键文件索引

- `client/prepare.py`：校验原包 SHA-256 → apktool d → 改 Manifest/gsp.txt（唯一改客户端的入口）
- `client/build.sh`：apktool b → zipalign → 可选 apksigner；`CLIENT_SOURCE_DIR`/`CLIENT_OUTPUT_DIR` 可覆盖
- `client/android-bootstrap/build.sh`：AAR→classes2.dex+libgojni+assets/backend（含 `seed.sqlite3`/`seed_meta.json`）→decoded-single 整包 staging
- `server/cmd/mercdb/cn35seeddb.go`：`mercdb cn35-seed-db`，合成截止最新 SQL 迁移的单机预置库并记录 `cutoff_migration`
- `client/merge_asset_manifest.py`：并入 remote_assets 后重生成 unity 清单
- `client/baseline.json`：原包路径/SHA-256/apktool 版本/输出名
- `server/mobile/mobile.go`：gomobile 入口（`Mobile.start(state, cn350, cards, port)`）
- `server/run.sh` / `.env`：模式 A 服务端运行参数
- `docs/运行与材料.md`：权威说明，与本 skill 冲突时以它为准
