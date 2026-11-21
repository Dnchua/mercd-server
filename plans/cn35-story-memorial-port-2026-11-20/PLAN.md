# CN3.5 剧情移植计划：纪念版剧本结构化移植与素材补齐

> 文档状态：**阶段 1–5 已完成**（解码、转换、素材构建、部署、服务端校验与 Go 测试全部通过，见 §八）；**阶段 6 实机抽检 2026-11-21 已执行**（见 §8.5）。范围决策 D1–D8 已全部确认。
> 证据底稿：[主线剧情移植与特殊招募说明](../../docs/16-主线剧情移植与特殊招募说明.md)（下称「doc16」）；纪念版提取工程 `~/Desktop/个人工作区/梅露可物语工程资源提取/`（下称「提取工程」，仓库外）。
> 来源标记沿用 doc16：「原作观测」= 纪念版/CN3.5 文件实测，「重建值」= 服务端补出的值，「推断」= 未经实机验证的判断。
> 硬约束：不修改客户端 DLL；剧情正文、APK、`.unity3d`、纪念版提取素材都不入库；新资源只放运行时叠加目录 `remote_assets/`，不进 `client/decoded/`。

---

## 一、背景与问题

- 国服于 2017 年停服；日服运营到 2025 年，期间客户端从 Unity 4 升级到 Unity 2022，纪念版（Memorial CHS）是 Unity 6。纪念版的很多素材与演出在 CN3.5（Unity 4.6）里根本不存在。
- 玩家提供的剧情版 APK 把纪念版 4008 篇剧本转成了 CN3.5 JSON。doc16 中「4008/4008 资源 100% 闭合」**成立的原因是剧本被削到了 CN3.5 已有素材的范围内**（原作观测）：
  - 场景字段只剩 `text/name/center/background/timezone`，丢掉了 left/right 站位、`bgm`、`effect`、`window_type`、`text_size`；`actress_xys` 全部为 0；
  - CN 没有的立绘直接不显示，CN 没有的背景换成其他背景（如 #1100 把 `169_3` 换成 `3_2`）；
  - 说话人保留了日文 key（例如「ラインハート」），需要另配假名词典才能转回中文。
- **纪念版剧本本身就是简体中文**：正文、标题、角色 `DisplayName` 都是中文（例如 key `ラインハート` → DisplayName `莱茵哈特`）。直接从纪念版原文移植，比从剧情版二次加工更准确。

## 二、已确认的决策（2026-11-20）

| # | 问题 | 决策 |
|---|---|---|
| D1 | 移植目标 | 剧情与缺失素材做到「客户端能支持的最大程度」，不强求还原纪念版独有演出 |
| D2 | 时代边界 | 以国服停服时的内容为界。国服最后一位同伴是 **1277（フィーユ）**（`unit_roster_manifest.json`：ID<10000 共 1233 条，最大 1277；资源包同样止于 1277；1278 获取途径为「10月新ユニット追加フェス2017」）。国服最后一期活动为 **event 44**（与 M8 当期活动一致） |
| D3 | 剧本来源 | 以纪念版 `StoryYamlData` 原文为准，不再以剧情版 APK 的削减版 JSON 为准 |
| D4 | 阶段 1 先行 | 先做结构化解码与精确缺口清单，后续阶段全部以此为输入 |

## 三、补充决策（2026-11-20 用户确认，原 Q1–Q4）

| # | 问题 | 决策 | 计划中的落点 |
|---|---|---|---|
| D5（Q1） | 范围外剧本（2137 篇，另有 94 条无场景数据）如何处理 | **在列表中隐藏**，不下发剧情版的纯文本降级版 | §4.1、阶段 2 |
| D6（Q2） | 4 个 `event_id=0` 的边界章节（19 篇） | **纳入范围** | §4.2、阶段 2 |
| D7（Q3） | 主线 533 话的标题 | **使用国服官方译名**（`cn350_main_story_catalog.json`）；正文使用纪念版 | 阶段 2 |
| D8（Q4） | Still/FadeStill 剧情 CG（302 个场景） | **先不做**，保留为阶段 6 可选项 | §4.4、阶段 6 |


---

## 四、范围（阶段 1 精确结果）

数据由提取工程的 `tools/story_gap_report.py` 生成，报告位于 `output/analysis/story/gap_report.json`。比对基线为 CN3.5 内置资源加 `remote_assets/`：storyactress 730、storybackground 709、sound 96、unit_large_ns 1228、名册 1398。

### 4.1 剧本范围（纪念版目录共 4091 条，其中 4008 个场景文件）

| 范围 | 判定规则 | 篇数 |
|---|---|---:|
| **做** · 主线第一部原版 | 国服已开放的 533 话（id 与 `cn350_main_story_catalog.json` 完全一致，0 偏差） | 533 |
| **做** · 同伴故事 | `unit_id` 在 CN3.5 名册内 | 878 |
| **做** · 活动故事 | 章节 `event_id` 为 1–44 | 430 |
| **做** · 边界章节（D6） | `event_id=0`，且后国服角色出场占比 ≤15% | 19 |
| 不做 | 主线第一部原版 11 章后 10 话与最终章 19 话（国服未开放） | 29 |
| 不做 | 新第一部（重制版，87）、第二部（428）、纪念故事（48） | 563 |
| 不做 | 后国服同伴的同伴故事 | 695 |
| 不做 | `event_id≥45` 的活动及其他后国服章节 | 850 |
| 不做 | 无场景数据（系统说明类，CN 客户端有本地版本） | 94 |

event 44 与 45 之间的分界非常清楚（原作观测）：event 1–44 的后国服角色出场占比几乎为 0（event 44 为 0.02），从 event 45 起跳到 0.5–0.96。

### 4.2 边界章节（D6：纳入）

| chapter_id | 名称 | 后国服占比 | 篇数 |
|---|---|---:|---:|
| 30 | 激突！斯尼诺地区！ | 0.000 | 2 |
| 50 | 宣传少女与漆黑的运营 | 0.000 | 2 |
| 55 | 邂逅＊君咲少女与瓶中的少女 | 0.079 | 5 |
| 78 | 星空乐谱与一夜限定的演奏会 | 0.119 | 10 |

### 4.3 范围内（含边界）剧本分类与素材缺口

- 共 1860 篇（含边界 19 篇）：**A 类 1491 篇**（引用的素材 CN3.5 全都有，只需补回字段）；**B 类 369 篇**（需要补素材）；**无法补齐的 0 篇**。
- 共 94,788 个场景、169,362 次角色出场。其中 134,671 次 CN 已有立绘；17,882 次为 Scout 类型，CN 已有 `unit_large_ns`；16,703 次需要补立绘；106 次需要补 `unit_large_ns`。
- 范围内剧本中出现的角色一律按「有素材就补」处理，因为这些剧本本身就是国服期内容。

| 素材 | 需要补的数量 | 纪念版来源 | 目标格式（原作观测，取自 CN 原包） |
|---|---:|---|---|
| storyactress | **215** | `output/assets/Story/<id>.png`，1024×1024，与 CN 764 号逐像素比对仅差 RGBA4444 量化误差 | `Texture2D(1024², RGBA4444)` + `AssetBundle`，container=`"<id>"` |
| unit_large_ns | **1**（1273） | `output/assets/Unit/<id>__*.png` 中 800×900 的本体图 | 沿用 unit 7 件套管线 |
| storybackground | **261** | `output/assets/Story/<bg>_<tz>.png`，1136×640 | `Texture2D(1136×640, RGB565/RGBA4444)` + `AssetBundle`，container=`"<bg>_<tz>"` |
| BGM | **22** | `output/assets/Sound/B-*.wav` | `AudioClip(MPEG)` + `AssetBundle`，container=`"b-xx"` |

需要补的 BGM：B-1002、B-107、B-108、B-111、B-158–161、B-163、B-168–171、B-173、B-175–179、B-205、B-210、B-260。

### 4.4 客户端不支持、只能降级的演出（范围内场景数）

| 演出 | 场景数 | 降级方式 |
|---|---:|---|
| `asset_parameters`（SpriteStudio/Prefab 叠加演出） | 2928 | 丢弃 |
| `face_texture_id`（差分表情） | 1038 | 丢弃；CN `ActressDocument` 的 `face_xy` 能力待查 |
| effect `Fade` | 279 | 映射为 `Wipe`，或丢弃（阶段 2 实测后定） |
| effect `Still` / `FadeStill` | 121 / 181 | 丢弃（D8） |
| effect `Scroll` | 13 | 丢弃 |
| `sound_effects`（逐场景音效） | 1 | 丢弃。客户端 `StoryViewer` 不支持场景音效；音效基本只出现在后国服内容里（全库 8303 个场景，范围内只有 1 个） |
| `background_effect` / `text_animation` | 0 | — |

CN 客户端原生支持的演出直接下发（原作观测，`StoryViewer.Effect*`）：Attack、Earthquake、Heal、Shake、Summon、Wipe、InputName、BecameFriend、ShowCursor，以及 `window_type=shout` 和 `text_size=big/middle`。

---

## 五、阶段计划

### 阶段 1：结构化解码与缺口清单 ✅

均为提取工程中的工具，仓库外、只读输入：

| 工具 | 产物 | 结果 |
|---|---|---|
| `tools/parse_story_yaml.py` | `output/analysis/story/scenes/story_<key>.json` | 4008/4008 解码，逐字节读到文件末尾，0 失败 |
| `tools/parse_story_master.py` | `output/analysis/story/story_catalog.json` | 章节 166 条、剧情目录 4091 条 |
| `tools/story_gap_report.py` | `output/analysis/story/gap_report.json` | 即 §4 的数据 |

格式要点（原作观测）：

- 类型取自 Memorial IL2CPP dump（metadata v31），编码为标准 MemoryPack。
- `.mp` 缺失明文首个 16 字节块：`decrypt_textassets.py` 把 blob[16:32] 当 IV 用，并从 blob[32:] 起解密。`.mp` 末尾还保留了 PKCS#7 填充。
- 分段剧情文件名为 `StoryYamlData_<id>_<n>`，例如 2111_1..3。

### 阶段 2：剧本转换器（服务端工具） ✅

- 新增或改造 `server/tools/extract_cn350_stories.py`，增加输入模式 `--memorial-scenes <dir> --gap-report <json>`。
  - 范围由 gap_report 的 `scope` 决定：`in` 与 `boundary` 都输出（D6）；`out` 不输出、不进目录（D5）。
- 场景映射规则：

  | 纪念版字段 | CN3.5 字段 |
  |---|---|
  | `left/center/right` | 对应站位。`texture_id`→`id`、`key`、`display_name`、`active`；`Navi`→`type:"navi"`；`Scout`→`type:"scoutactress"`（读取 `unit/unit_large_ns_<id>`） |
  | 说话人 | 取同场景角色的 `DisplayName`，其次取全库 key→DisplayName 众数，最后取现有 `speaker_map`。范围内 77,186 个说话人中 99.75% 能由纪念版自带的中文名解析，其余 189 个落到 `speaker_map` |
  | `bgm.name` | `bgm:"sound/B-xx"`，并把 `sound/B-xx` 加入 `asset_names` |
  | `effect` | 按 §4.4 映射；`ShowCursor`、`ShowModal` 的 `effect_parameter` 需单独核对格式 |
  | `window_type` / `text_size` | `shout` / `big`、`middle` |
  | 正文 | `\r\n`→`\n`；保留 NGUI 色标 `[xxxxxx]`/`[-]` 和 `<name>` |
  | `actress_xy` | 由 `offset` 与 `ActressPositionYamlData` 换算，换算公式在阶段 2 实测后确定；首版允许为 0（推断：与剧情版现状一致） |

- 主线 533 话保留官方 `id`、`chapter_id` 和国服官方标题（D7）；同伴故事和活动故事的 `chapter_id` 与目录形态另行设计，需对照 doc16 的 `/chapters/` 契约和 `ChaptersAndStories` 页签。
- 服务端 `story.go` 的资源闭合校验要把 `sound/` 算进去，并新增 `scoutactress` 的 `unit/unit_large_ns_<id>` 闭合检查。

### 阶段 3：纹理打包（215 个立绘、261 组背景） ✅

二选一，以产物与 CN 原包的格式、容器名一致为准：

- **A：Unity 4 无头模式**（`~/Desktop/ss4bridge`，Windows VM 中的 Unity 4.3.4f1，参数 `-batchmode -nographics`）。在 `SS4Builder.Icons` 的基础上新增 `story` 任务类型，TextureImporter 输出 RGBA4444 / RGB565。
- **B：UnityPy 模板克隆**（已做 PoC：`/tmp/storyaudit/poc/`，读回结构正确、像素差为 0）。需要自写 16 位像素打包；不打包的话 UnityPy 会降为 RGBA32，显存占用变成 4 倍。

### 阶段 4：BGM 打包（22 首） ✅

只走 Unity 4 无头模式：WAV 导入后压成 Android 用的 MPEG `AudioClip`，容器名小写 `b-xx`，与 `sound/B-10` 原包对拍。

### 阶段 5：部署与服务端校验 ✅

- 产物放入 `server-state-cn35/remote_assets/{storyactress,storybackground,sound,unit}/`，服务端每 30 秒扫描一次并自动追加清单。
- 启动日志的 `pending=0 rejected=0`，加上故事目录的校验诊断，作为门禁。
- Go 冒泡测试：抽样剧本的 `asset_names` 全部闭合，`left`、`right`、`bgm`、`scoutactress` 字段形态与客户端消费点一致。

### 阶段 6：实机抽检与可选项

- 抽检清单：主线第 1 话；一篇 B 类主线；一篇 B 类活动；一篇含 Scout 角色的同伴故事；一篇含 `shout`、`Earthquake` 和 BGM 切换的场景。
- 可选项：D8 剧情 CG 伪背景（暂不做）；`Fade` 映射效果；差分表情。

---

## 六、风险与未决

- **新包的运行时兼容性（推断）**：Unity 4.3.4 构建的包能否被 4.6.x 运行时加载，unit_414 已有先例；UnityPy 克隆包还没上过机。两种产物都必须实机抽检。
- **NPC 时代阈值（推断）**：ID 10353、20884 取自 CN 实际上限，假定 ID 单调递增；只影响边界章节的判入，不影响范围内补素材。
- **`actress_xy` 换算**：纪念版 `offset/scale` 与 CN `xy` 的坐标系还没对齐，首版可能有站位偏差。
- **同伴/活动故事的目录入口**：CN 旧 `Chapters` 路径只支持主线章节列表；同伴故事和活动故事的入口需按 UI 根准则另行设计，可能需要 `ChaptersAndStories`（`game_server_version≥210`）。
- **体积**：新增资源按 CN 包均值估算约 0.15 GB（立绘约 210 KB×216 + 背景约 330 KB×261 + BGM 约 880 KB×22）。

## 七、复现

```bash
cd ~/Desktop/个人工作区/梅露可物语工程资源提取
python3 tools/parse_story_yaml.py          # → output/analysis/story/scenes/
python3 tools/parse_story_master.py        # → output/analysis/story/story_catalog.json
python3 tools/story_gap_report.py          # → output/analysis/story/gap_report.json（含逐篇 scope/class/gaps 与素材清单）
```

## 八、执行记录（2026-11-20）

### 8.1 素材构建（阶段 3/4）

- 选用 Unity 4 无头模式（方案 A）。`~/Desktop/ss4bridge` 新增 `SS4Builder.Story` 入口；`build.ps1` 检测到 `story.json` 时自动调用它。
- 任务由提取工程的 `tools/story_asset_job.py` 根据 gap_report 生成。
- 校验：`tools/verify_story_bundles.py` 检查对象构成、container、格式/过滤/包裹、像素差与 AudioClip 设置，结果 **498/498 通过**。

| 类别 | 数量 | 实测格式（Unity 4.3.4f1 构建） | 体积 |
|---|---:|---|---:|
| storyactress | 215 | 1024² RGBA4444，Trilinear/Repeat，与源图像素差 ≤0.6 | 26 MB |
| storybackground | 261 | 1136×640，RGB565（近似不透明）或 RGBA4444，Bilinear/Clamp | 88 MB |
| sound（BGM） | 22 | AudioClip MPEG(13)，3D，CompressedInMemory，160 kbps | 31 MB |

降级与近似项（重建值）：

- 5 张纪念版长图背景（145_2、328_4、342_4、373_1、645_4）：缩放到宽 1136 后垂直居中裁成 640 高。
- **1273 的 unit_large_ns 不构建**：纪念版只有带背景特效的整绘，没有本体图。转换器把这 106 次 Scout 出场降级为不显示，台词保留。

### 8.2 部署与转换（阶段 2/5）

- 498 个包放入 `inputs/files/{storyactress,storybackground,sound}/`（即 `MERCD_REMOTE_ASSETS`），全部为新增文件，与内置包、已有包无重名。远端扫描结果 `entries=14037 rejected=0`。
- 转换器：`server/tools/convert_memorial_stories.py`。输出到 `server/assets/cn350/stories/`（gitignored），共 **1860 话、50 章**（主线 11 章、同伴故事、38 个活动章），资源闭合错误 0，加载时诊断 0。
- 统计（`convert_report.json`）：

  | 项目 | 数量 |
  |---|---:|
  | 场景 | 94,788 |
  | 普通立绘出场 | 132,601 |
  | navi 出场 | 18,773 |
  | scoutactress 出场 | 17,882 |
  | Fade→Wipe | 279 |
  | 丢弃 Still / FadeStill / Scroll / BecameFriend | 121 / 181 / 13 / 4 |

- 残留日文说话人 90 处，其中主线 4 处，如「索伊・ヤッサー・莱伊吉」：纪念版自带中文名和 speaker_map 都没覆盖到，需要可靠译名后补进 `cn350_main_story_catalog.json` 的 speaker_map。
- 旧 fixture 会移到仓库 `tmp/stories-backup-<时间>/`（gitignored）留档，不删除。**本轮第二次运行时，剧情版 533 话的旧 fixture 已被覆盖丢失**（当时备份逻辑会删除上一份 `.prev`，现已改正）。如需恢复，可用 `extract_cn350_stories.py --apk <剧情版APK>` 重新生成。

### 8.3 服务端改动

- `story.go` 的 `validateStoryDoc` 新增三项检查：
  - `scoutactress` 角色检查 `unit/unit_large_ns_<id>`；
  - 立绘包必须列入 `asset_names`（StoryViewer 只预载 asset_names）；
  - `bgm` 必须已下发，且列入 asset_names。
- 测试：
  - `story_internal_test.go` 新增纪念版字段形态的正例，以及 4 个负例；
  - `m7_smoke_wire_test.go` 改为“前 11 章是主线、主线合计 533 话”的断言；假名判定不再包含「・」「ー」。
- `go test ./...` 全部通过。

### 8.4 生效方式与待办

- 故事目录在服务启动时加载，运行中的服务需要**重启**才会读到新 fixture；新资源包由 30 秒扫描自动下发。
- 模式 B（单 APK）需要重新打包，才能把 `inputs/files` 的新包并入 APK（按 `skills/cn35-apk-packaging/SKILL.md`）。
- 阶段 6 实机抽检已执行，见 §8.5。

### 8.5 实机抽检（2026-11-21）

- 缺陷：立绘左上方多出一块裁切的表情图。根因（客户端源码）：`Navi/Actress.SetFaceSprite` 仅在 `expression=="tujo"` 时隐藏表情层，转换器输出 `expression:""` 时 NGUI 空 spriteName 回退到图集首个 sprite。修复：转换器与 `extract_cn350_stories.py` 默认 `tujo`；`validateStoryDoc` 拒收非 scoutactress 的空 expression；重新生成 1860 话（stats 与 §8.2 一致）。
- 修复后抽检（模拟器 192.168.50.92，模式 A）：主线第 1 话（navi 梅露可、主人公）无多余贴片；活动「歌乐坛之王与鸟笼的歌姬」第 11 话：新背景 219_3、新立绘 893/895 与新 BGM B-108 均 200 下载并显示，非说话人变暗正常，无异常日志（BGM 仅确认音频轨活跃，未人耳确认曲目）；同伴故事「[花冠弓手]琦珂」scoutactress 立绘正常。
- 主线第 1 话首场景含 `Earthquake`，播放正常；`shout`、`text_size` 等其余演出未逐项抽检；D8 可选项未做。
