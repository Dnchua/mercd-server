# 接口路由表（mercd35 CN 3.5.0）

> 本文件由 `go run ./cmd/apidoc`（server/ 目录）从活路由表自动生成，勿手改。
> 生成时间 2026-11-17T14:46:23Z · 路由数 754 · 默认监听 `http://127.0.0.1:18091`。
> 交互版见同目录 `index.html`，机器可读版见 `openapi.json`，字段级契约见 `docs/协议对接/cn350/contracts/`。

## 账号帧（二进制信封） · `account`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| POST | `/account` | confirmed | CN 3.5.0 账号帧（type3/t300，AES-static+zlib+xor 帧） |
| POST | `/account/` | confirmed | CN 3.5.0 账号帧（type3/t300，AES-static+zlib+xor 帧） |

## accu_login_activity · `accu_login_activity`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/accu_login_activity/` | confirmed | requireData 空态读面(accu_login_activity) |
| POST | `/accu_login_activity/` | confirmed | requireData 空态读面(accu_login_activity) POST 形态 |

## 活动页配置 · `act_pages`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/act_pages` | confirmed | CN 3.5.0 活动页列表（驱动活动大图/横幅轮播与活动红点） |
| POST | `/act_pages` | confirmed | CN 3.5.0 活动页列表 POST 形态 |
| GET | `/act_pages/` | confirmed | CN 3.5.0 活动页列表（驱动活动大图/横幅轮播与活动红点） |
| POST | `/act_pages/` | confirmed | CN 3.5.0 活动页列表 POST 形态 |

## activity · `activity`（8 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/activity/accu_login_activity_v30/` | confirmed | requireData 空态读面(activity_accu_login_activity_v30) |
| POST | `/activity/accu_login_activity_v30/` | confirmed | requireData 空态读面(activity_accu_login_activity_v30) POST 形态 |
| GET | `/activity/march_charge_activity/` | confirmed | requireData 空态读面(activity_march_charge_activity) |
| POST | `/activity/march_charge_activity/` | confirmed | requireData 空态读面(activity_march_charge_activity) POST 形态 |
| GET | `/activity/march_charge_activity/show_chest` | confirmed | requireData 空态读面(activity_march_charge_activity_show_chest) |
| POST | `/activity/march_charge_activity/show_chest` | confirmed | requireData 空态读面(activity_march_charge_activity_show_chest) POST 形态 |
| GET | `/activity/march_charge_activity/show_chest/` | confirmed | requireData 空态读面(activity_march_charge_activity_show_chest) |
| POST | `/activity/march_charge_activity/show_chest/` | confirmed | requireData 空态读面(activity_march_charge_activity_show_chest) POST 形态 |

## adventure · `adventure`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/adventure/` | confirmed | CN 3.5.0 登录冒险读面+当日发放（LOGIN350-03，0036 轮次配置；未配置回空态） |
| POST | `/adventure/` | confirmed | CN 3.5.0 登录冒险 POST 形态 |

## 图鉴/休息室 · `albums`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/albums/` | confirmed | CN 3.5.0 同伴图鉴态（UserUnits/UserAlbum requireData.albums；空态） |
| POST | `/albums/` | confirmed | CN 3.5.0 同伴图鉴态（UserUnits/UserAlbum requireData.albums；空态） POST 形态 |

## anniversary_gacha · `anniversary_gacha`（8 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/anniversary_gacha/blessing_box_info` | confirmed | requireData 空态读面(anniversary_gacha_blessing_box_info) |
| POST | `/anniversary_gacha/blessing_box_info` | confirmed | requireData 空态读面(anniversary_gacha_blessing_box_info) POST 形态 |
| GET | `/anniversary_gacha/blessing_box_info/` | confirmed | requireData 空态读面(anniversary_gacha_blessing_box_info) |
| POST | `/anniversary_gacha/blessing_box_info/` | confirmed | requireData 空态读面(anniversary_gacha_blessing_box_info) POST 形态 |
| GET | `/anniversary_gacha/main_info` | confirmed | requireData 空态读面(anniversary_gacha_main_info) |
| POST | `/anniversary_gacha/main_info` | confirmed | requireData 空态读面(anniversary_gacha_main_info) POST 形态 |
| GET | `/anniversary_gacha/main_info/` | confirmed | requireData 空态读面(anniversary_gacha_main_info) |
| POST | `/anniversary_gacha/main_info/` | confirmed | requireData 空态读面(anniversary_gacha_main_info) POST 形态 |

## 启动配置 · `app_conf_android.json.encode`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/app_conf_android.json.encode` | confirmed | CN 3.5.0 页面配置（RenderWire 剥注解出包） |
| GET | `/app_conf_android.json.encode/` | confirmed | CN 3.5.0 页面配置（RenderWire 剥注解出包） |

## areas · `areas`（8 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/areas` | confirmed | CN 3.5.0 区域目录（?stage_id=N → areas_N + quests_<areaId> 多键） |
| POST | `/areas` | confirmed | CN 3.5.0 区域目录 POST 形态 |
| GET | `/areas/` | confirmed | CN 3.5.0 区域目录（?stage_id=N → areas_N + quests_<areaId> 多键） |
| POST | `/areas/` | confirmed | CN 3.5.0 区域目录 POST 形态 |
| GET | `/areas/event` | confirmed | requireData 空态读面(areas_event) |
| POST | `/areas/event` | confirmed | requireData 空态读面(areas_event) POST 形态 |
| GET | `/areas/event/` | confirmed | requireData 空态读面(areas_event) |
| POST | `/areas/event/` | confirmed | requireData 空态读面(areas_event) POST 形态 |

## asset_info_android4_v2_short.json.encode · `asset_info_android4_v2_short.json.encode`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/asset_info_android4_v2_short.json.encode` | confirmed | CN 3.5.0 资产清单（@文本格式；远端叠加后带 md5 行走 CDN） |
| GET | `/asset_info_android4_v2_short.json.encode/` | confirmed | CN 3.5.0 资产清单（@文本格式；远端叠加后带 md5 行走 CDN） |

## 公会战 · `battles`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/battles/comments` | confirmed | requireData 空态读面(battles_comments) |
| POST | `/battles/comments` | confirmed | requireData 空态读面(battles_comments) POST 形态 |
| GET | `/battles/comments/` | confirmed | requireData 空态读面(battles_comments) |
| POST | `/battles/comments/` | confirmed | requireData 空态读面(battles_comments) POST 形态 |

## cakes · `cakes`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/cakes/toto_paiqiu_log/` | confirmed | requireData 空态读面(cakes_toto_paiqiu_log) |
| POST | `/cakes/toto_paiqiu_log/` | confirmed | requireData 空态读面(cakes_toto_paiqiu_log) POST 形态 |

## chapters · `chapters`（6 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/chapters/` | confirmed | CN 3.5.0 章节目录（Chapters requireData.chapters；故事阅读入口） |
| POST | `/chapters/` | confirmed | CN 3.5.0 章节目录 POST 形态 |
| GET | `/chapters/list` | confirmed | CN 3.5.0 章节+故事目录（ChaptersAndStories >=210 路径 requireData.chapters/stories） |
| POST | `/chapters/list` | confirmed | CN 3.5.0 章节+故事目录 POST 形态 |
| GET | `/chapters/list/` | confirmed | CN 3.5.0 章节+故事目录（ChaptersAndStories >=210 路径 requireData.chapters/stories） |
| POST | `/chapters/list/` | confirmed | CN 3.5.0 章节+故事目录 POST 形态 |

## charge_act2 · `charge_act2`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/charge_act2/index` | confirmed | requireData 空态读面(charge_act2_index) |
| POST | `/charge_act2/index` | confirmed | requireData 空态读面(charge_act2_index) POST 形态 |
| GET | `/charge_act2/index/` | confirmed | requireData 空态读面(charge_act2_index) |
| POST | `/charge_act2/index/` | confirmed | requireData 空态读面(charge_act2_index) POST 形态 |

## chest · `chest`（12 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/chest/show_chest_rules` | confirmed | requireData 空态读面(chest_show_chest_rules) |
| POST | `/chest/show_chest_rules` | confirmed | requireData 空态读面(chest_show_chest_rules) POST 形态 |
| GET | `/chest/show_chest_rules/` | confirmed | requireData 空态读面(chest_show_chest_rules) |
| POST | `/chest/show_chest_rules/` | confirmed | requireData 空态读面(chest_show_chest_rules) POST 形态 |
| GET | `/chest/show_page` | confirmed | requireData 空态读面(chest_show_page) |
| POST | `/chest/show_page` | confirmed | requireData 空态读面(chest_show_page) POST 形态 |
| GET | `/chest/show_page/` | confirmed | requireData 空态读面(chest_show_page) |
| POST | `/chest/show_page/` | confirmed | requireData 空态读面(chest_show_page) POST 形态 |
| GET | `/chest/show_paper_list` | confirmed | requireData 空态读面(chest_show_paper_list) |
| POST | `/chest/show_paper_list` | confirmed | requireData 空态读面(chest_show_paper_list) POST 形态 |
| GET | `/chest/show_paper_list/` | confirmed | requireData 空态读面(chest_show_paper_list) |
| POST | `/chest/show_paper_list/` | confirmed | requireData 空态读面(chest_show_paper_list) POST 形态 |

## 编解码自检 · `cn35`（6 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/cn35/assets/remote` | confirmed | CN 3.5.0 远端资源叠加诊断（entries/pending/rejected/版本） |
| GET | `/cn35/assets/remote/` | confirmed | CN 3.5.0 远端资源叠加诊断（entries/pending/rejected/版本） |
| GET | `/cn35/catalog/info` | confirmed | CN 3.5.0 卡牌目录诊断（版本/哈希/计数/质量警告汇总） |
| GET | `/cn35/catalog/info/` | confirmed | CN 3.5.0 卡牌目录诊断（版本/哈希/计数/质量警告汇总） |
| POST | `/cn35/codec-echo` | confirmed | CN 3.5.0 编解码自检（verify 帧回显） |
| POST | `/cn35/codec-echo/` | confirmed | CN 3.5.0 编解码自检（verify 帧回显） |

## coins · `coins`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/coins/` | confirmed | requireData 空态读面(coins) |
| POST | `/coins/` | confirmed | requireData 空态读面(coins) POST 形态 |

## collection · `collection`（16 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/collection/` | confirmed | requireData 空态读面(collection) |
| POST | `/collection/` | confirmed | requireData 空态读面(collection) POST 形态 |
| GET | `/collection/grab/` | confirmed | requireData 空态读面(collection_grab) |
| POST | `/collection/grab/` | confirmed | requireData 空态读面(collection_grab) POST 形态 |
| GET | `/collection/monsters` | confirmed | requireData 空态读面(collection_monsters) |
| POST | `/collection/monsters` | confirmed | requireData 空态读面(collection_monsters) POST 形态 |
| GET | `/collection/monsters/` | confirmed | requireData 空态读面(collection_monsters) |
| POST | `/collection/monsters/` | confirmed | requireData 空态读面(collection_monsters) POST 形态 |
| GET | `/collection/rank` | confirmed | requireData 空态读面(collection_rank) |
| POST | `/collection/rank` | confirmed | requireData 空态读面(collection_rank) POST 形态 |
| GET | `/collection/rank/` | confirmed | requireData 空态读面(collection_rank) |
| POST | `/collection/rank/` | confirmed | requireData 空态读面(collection_rank) POST 形态 |
| GET | `/collection/rewards` | confirmed | requireData 空态读面(collection_rewards) |
| POST | `/collection/rewards` | confirmed | requireData 空态读面(collection_rewards) POST 形态 |
| GET | `/collection/rewards/` | confirmed | requireData 空态读面(collection_rewards) |
| POST | `/collection/rewards/` | confirmed | requireData 空态读面(collection_rewards) POST 形态 |

## college · `college`（46 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/college/` | confirmed | requireData 空态读面(college) |
| POST | `/college/` | confirmed | requireData 空态读面(college) POST 形态 |
| GET | `/college/get_apply_for_positional_titles_info` | confirmed | requireData 空态读面(college_get_apply_for_positional_titles_info) |
| POST | `/college/get_apply_for_positional_titles_info` | confirmed | requireData 空态读面(college_get_apply_for_positional_titles_info) POST 形态 |
| GET | `/college/get_apply_for_positional_titles_info/` | confirmed | requireData 空态读面(college_get_apply_for_positional_titles_info) |
| POST | `/college/get_apply_for_positional_titles_info/` | confirmed | requireData 空态读面(college_get_apply_for_positional_titles_info) POST 形态 |
| GET | `/college/get_apply_for_student_condition` | confirmed | requireData 空态读面(college_get_apply_for_student_condition) |
| POST | `/college/get_apply_for_student_condition` | confirmed | requireData 空态读面(college_get_apply_for_student_condition) POST 形态 |
| GET | `/college/get_apply_for_student_condition/` | confirmed | requireData 空态读面(college_get_apply_for_student_condition) |
| POST | `/college/get_apply_for_student_condition/` | confirmed | requireData 空态读面(college_get_apply_for_student_condition) POST 形态 |
| GET | `/college/get_apply_for_student_list` | confirmed | requireData 空态读面(college_get_apply_for_student_list) |
| POST | `/college/get_apply_for_student_list` | confirmed | requireData 空态读面(college_get_apply_for_student_list) POST 形态 |
| GET | `/college/get_apply_for_student_list/` | confirmed | requireData 空态读面(college_get_apply_for_student_list) |
| POST | `/college/get_apply_for_student_list/` | confirmed | requireData 空态读面(college_get_apply_for_student_list) POST 形态 |
| GET | `/college/get_daily_tasks` | confirmed | requireData 空态读面(college_get_daily_tasks) |
| POST | `/college/get_daily_tasks` | confirmed | requireData 空态读面(college_get_daily_tasks) POST 形态 |
| GET | `/college/get_daily_tasks/` | confirmed | requireData 空态读面(college_get_daily_tasks) |
| POST | `/college/get_daily_tasks/` | confirmed | requireData 空态读面(college_get_daily_tasks) POST 形态 |
| GET | `/college/get_student_detail` | confirmed | requireData 空态读面(college_get_student_detail) |
| POST | `/college/get_student_detail` | confirmed | requireData 空态读面(college_get_student_detail) POST 形态 |
| GET | `/college/get_student_detail/` | confirmed | requireData 空态读面(college_get_student_detail) |
| POST | `/college/get_student_detail/` | confirmed | requireData 空态读面(college_get_student_detail) POST 形态 |
| GET | `/college/get_students_info` | confirmed | requireData 空态读面(college_get_students_info) |
| POST | `/college/get_students_info` | confirmed | requireData 空态读面(college_get_students_info) POST 形态 |
| GET | `/college/get_students_info/` | confirmed | requireData 空态读面(college_get_students_info) |
| POST | `/college/get_students_info/` | confirmed | requireData 空态读面(college_get_students_info) POST 形态 |
| GET | `/college/get_study_info` | confirmed | requireData 空态读面(college_get_study_info) |
| POST | `/college/get_study_info` | confirmed | requireData 空态读面(college_get_study_info) POST 形态 |
| GET | `/college/get_study_info/` | confirmed | requireData 空态读面(college_get_study_info) |
| POST | `/college/get_study_info/` | confirmed | requireData 空态读面(college_get_study_info) POST 形态 |
| GET | `/college/get_teacher_info` | confirmed | requireData 空态读面(college_get_teacher_info) |
| POST | `/college/get_teacher_info` | confirmed | requireData 空态读面(college_get_teacher_info) POST 形态 |
| GET | `/college/get_teacher_info/` | confirmed | requireData 空态读面(college_get_teacher_info) |
| POST | `/college/get_teacher_info/` | confirmed | requireData 空态读面(college_get_teacher_info) POST 形态 |
| GET | `/college/get_week_sign_info` | confirmed | requireData 空态读面(college_get_week_sign_info) |
| POST | `/college/get_week_sign_info` | confirmed | requireData 空态读面(college_get_week_sign_info) POST 形态 |
| GET | `/college/get_week_sign_info/` | confirmed | requireData 空态读面(college_get_week_sign_info) |
| POST | `/college/get_week_sign_info/` | confirmed | requireData 空态读面(college_get_week_sign_info) POST 形态 |
| GET | `/college/search_teacher` | confirmed | requireData 空态读面(college_search_teacher) |
| POST | `/college/search_teacher` | confirmed | requireData 空态读面(college_search_teacher) POST 形态 |
| GET | `/college/search_teacher/` | confirmed | requireData 空态读面(college_search_teacher) |
| POST | `/college/search_teacher/` | confirmed | requireData 空态读面(college_search_teacher) POST 形态 |
| GET | `/college/task_detail` | confirmed | requireData 空态读面(college_task_detail) |
| POST | `/college/task_detail` | confirmed | requireData 空态读面(college_task_detail) POST 形态 |
| GET | `/college/task_detail/` | confirmed | requireData 空态读面(college_task_detail) |
| POST | `/college/task_detail/` | confirmed | requireData 空态读面(college_task_detail) POST 形态 |

## community_topics · `community_topics`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/community_topics/` | confirmed | requireData 空态读面(community_topics) |
| POST | `/community_topics/` | confirmed | requireData 空态读面(community_topics) POST 形态 |

## consume_act2 · `consume_act2`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/consume_act2/index` | confirmed | requireData 空态读面(consume_act2_index) |
| POST | `/consume_act2/index` | confirmed | requireData 空态读面(consume_act2_index) POST 形态 |
| GET | `/consume_act2/index/` | confirmed | requireData 空态读面(consume_act2_index) |
| POST | `/consume_act2/index/` | confirmed | requireData 空态读面(consume_act2_index) POST 形态 |

## consume_packages · `consume_packages`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/consume_packages/` | confirmed | requireData 空态读面(consume_packages) |
| POST | `/consume_packages/` | confirmed | requireData 空态读面(consume_packages) POST 形态 |

## daily_payment · `daily_payment`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/daily_payment` | confirmed | requireData 空态读面(daily_payment) |
| POST | `/daily_payment` | confirmed | requireData 空态读面(daily_payment) POST 形态 |
| GET | `/daily_payment/` | confirmed | requireData 空态读面(daily_payment) |
| POST | `/daily_payment/` | confirmed | requireData 空态读面(daily_payment) POST 形态 |

## dungeon_rank · `dungeon_rank`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/dungeon_rank/show_rank` | confirmed | requireData 空态读面(dungeon_rank_show_rank) |
| POST | `/dungeon_rank/show_rank` | confirmed | requireData 空态读面(dungeon_rank_show_rank) POST 形态 |
| GET | `/dungeon_rank/show_rank/` | confirmed | requireData 空态读面(dungeon_rank_show_rank) |
| POST | `/dungeon_rank/show_rank/` | confirmed | requireData 空态读面(dungeon_rank_show_rank) POST 形态 |

## 活动 · `events`（10 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/events/` | confirmed | CN 3.5.0 历次活动列表（StageSelect/Events requireData.events；空态） |
| POST | `/events/` | confirmed | CN 3.5.0 历次活动列表（StageSelect/Events requireData.events；空态） POST 形态 |
| GET | `/events/ranking` | confirmed | requireData 空态读面(events_ranking) |
| POST | `/events/ranking` | confirmed | requireData 空态读面(events_ranking) POST 形态 |
| GET | `/events/ranking/` | confirmed | requireData 空态读面(events_ranking) |
| POST | `/events/ranking/` | confirmed | requireData 空态读面(events_ranking) POST 形态 |
| GET | `/events/show` | confirmed | requireData 空态读面(events_show) |
| POST | `/events/show` | confirmed | requireData 空态读面(events_show) POST 形态 |
| GET | `/events/show/` | confirmed | requireData 空态读面(events_show) |
| POST | `/events/show/` | confirmed | requireData 空态读面(events_show) POST 形态 |

## exchange_activity · `exchange_activity`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/exchange_activity/show` | confirmed | requireData 空态读面(exchange_activity_show) |
| POST | `/exchange_activity/show` | confirmed | requireData 空态读面(exchange_activity_show) POST 形态 |
| GET | `/exchange_activity/show/` | confirmed | requireData 空态读面(exchange_activity_show) |
| POST | `/exchange_activity/show/` | confirmed | requireData 空态读面(exchange_activity_show) POST 形态 |

## extra_quest_rankings · `extra_quest_rankings`（12 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/extra_quest_rankings/past_quests_list` | confirmed | requireData 空态读面(extra_quest_rankings_past_quests_list) |
| POST | `/extra_quest_rankings/past_quests_list` | confirmed | requireData 空态读面(extra_quest_rankings_past_quests_list) POST 形态 |
| GET | `/extra_quest_rankings/past_quests_list/` | confirmed | requireData 空态读面(extra_quest_rankings_past_quests_list) |
| POST | `/extra_quest_rankings/past_quests_list/` | confirmed | requireData 空态读面(extra_quest_rankings_past_quests_list) POST 形态 |
| GET | `/extra_quest_rankings/rewards` | confirmed | requireData 空态读面(extra_quest_rankings_rewards) |
| POST | `/extra_quest_rankings/rewards` | confirmed | requireData 空态读面(extra_quest_rankings_rewards) POST 形态 |
| GET | `/extra_quest_rankings/rewards/` | confirmed | requireData 空态读面(extra_quest_rankings_rewards) |
| POST | `/extra_quest_rankings/rewards/` | confirmed | requireData 空态读面(extra_quest_rankings_rewards) POST 形态 |
| GET | `/extra_quest_rankings/yyyymms` | confirmed | requireData 空态读面(extra_quest_rankings_yyyymms) |
| POST | `/extra_quest_rankings/yyyymms` | confirmed | requireData 空态读面(extra_quest_rankings_yyyymms) POST 形态 |
| GET | `/extra_quest_rankings/yyyymms/` | confirmed | requireData 空态读面(extra_quest_rankings_yyyymms) |
| POST | `/extra_quest_rankings/yyyymms/` | confirmed | requireData 空态读面(extra_quest_rankings_yyyymms) POST 形态 |

## fate · `fate`（8 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/fate/fate_buff_summary` | confirmed | requireData 空态读面(fate_fate_buff_summary) |
| POST | `/fate/fate_buff_summary` | confirmed | requireData 空态读面(fate_fate_buff_summary) POST 形态 |
| GET | `/fate/fate_buff_summary/` | confirmed | requireData 空态读面(fate_fate_buff_summary) |
| POST | `/fate/fate_buff_summary/` | confirmed | requireData 空态读面(fate_fate_buff_summary) POST 形态 |
| GET | `/fate/show` | confirmed | requireData 空态读面(fate_show) |
| POST | `/fate/show` | confirmed | requireData 空态读面(fate_show) POST 形态 |
| GET | `/fate/show/` | confirmed | requireData 空态读面(fate_show) |
| POST | `/fate/show/` | confirmed | requireData 空态读面(fate_show) POST 形态 |

## first_charge · `first_charge`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/first_charge/info` | confirmed | requireData 空态读面(first_charge_info) |
| POST | `/first_charge/info` | confirmed | requireData 空态读面(first_charge_info) POST 形态 |
| GET | `/first_charge/info/` | confirmed | requireData 空态读面(first_charge_info) |
| POST | `/first_charge/info/` | confirmed | requireData 空态读面(first_charge_info) POST 形态 |

## first_store_activity · `first_store_activity`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/first_store_activity/show` | confirmed | requireData 空态读面(first_store_activity_show) |
| POST | `/first_store_activity/show` | confirmed | requireData 空态读面(first_store_activity_show) POST 形态 |
| GET | `/first_store_activity/show/` | confirmed | requireData 空态读面(first_store_activity_show) |
| POST | `/first_store_activity/show/` | confirmed | requireData 空态读面(first_store_activity_show) POST 形态 |

## friends · `friends`（12 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/friends/friend_apply_list` | confirmed | requireData 空态读面(friends_friend_apply_list) |
| POST | `/friends/friend_apply_list` | confirmed | requireData 空态读面(friends_friend_apply_list) POST 形态 |
| GET | `/friends/friend_apply_list/` | confirmed | requireData 空态读面(friends_friend_apply_list) |
| POST | `/friends/friend_apply_list/` | confirmed | requireData 空态读面(friends_friend_apply_list) POST 形态 |
| GET | `/friends/index` | confirmed | requireData 空态读面(friends_index) |
| POST | `/friends/index` | confirmed | requireData 空态读面(friends_index) POST 形态 |
| GET | `/friends/index/` | confirmed | requireData 空态读面(friends_index) |
| POST | `/friends/index/` | confirmed | requireData 空态读面(friends_index) POST 形态 |
| GET | `/friends/show_friend_medal_list` | confirmed | requireData 空态读面(friends_show_friend_medal_list) |
| POST | `/friends/show_friend_medal_list` | confirmed | requireData 空态读面(friends_show_friend_medal_list) POST 形态 |
| GET | `/friends/show_friend_medal_list/` | confirmed | requireData 空态读面(friends_show_friend_medal_list) |
| POST | `/friends/show_friend_medal_list/` | confirmed | requireData 空态读面(friends_show_friend_medal_list) POST 形态 |

## friends_activities · `friends_activities`（8 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/friends_activities/index` | confirmed | requireData 空态读面(friends_activities_index) |
| POST | `/friends_activities/index` | confirmed | requireData 空态读面(friends_activities_index) POST 形态 |
| GET | `/friends_activities/index/` | confirmed | requireData 空态读面(friends_activities_index) |
| POST | `/friends_activities/index/` | confirmed | requireData 空态读面(friends_activities_index) POST 形态 |
| GET | `/friends_activities/progress` | confirmed | requireData 空态读面(friends_activities_progress) |
| POST | `/friends_activities/progress` | confirmed | requireData 空态读面(friends_activities_progress) POST 形态 |
| GET | `/friends_activities/progress/` | confirmed | requireData 空态读面(friends_activities_progress) |
| POST | `/friends_activities/progress/` | confirmed | requireData 空态读面(friends_activities_progress) POST 形态 |

## 抽卡 · `gachas`（42 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/gachas` | confirmed | CN 3.5.0 卡池目录读面（app_conf requireData.gachas 门控页面就绪） |
| POST | `/gachas` | confirmed | CN 3.5.0 卡池目录 POST 形态 |
| GET | `/gachas/` | confirmed | CN 3.5.0 卡池目录读面（app_conf requireData.gachas 门控页面就绪） |
| POST | `/gachas/` | confirmed | CN 3.5.0 卡池目录 POST 形态 |
| GET | `/gachas/attr_gacha` | confirmed | CN 3.5.0 每日轮换特殊招募页面（pickup_gacha） |
| POST | `/gachas/attr_gacha` | confirmed | CN 3.5.0 每日轮换特殊招募页面 POST 形态（pickup_gacha） |
| GET | `/gachas/attr_gacha/` | confirmed | CN 3.5.0 每日轮换特殊招募页面（pickup_gacha） |
| POST | `/gachas/attr_gacha/` | confirmed | CN 3.5.0 每日轮换特殊招募页面 POST 形态（pickup_gacha） |
| GET | `/gachas/available_unit_list` | confirmed | CN 3.5.0 招募可获名单列表（含当期特殊招募与 5★ 展示） |
| POST | `/gachas/available_unit_list` | confirmed | CN 3.5.0 招募可获名单列表 POST 形态 |
| GET | `/gachas/available_unit_list/` | confirmed | CN 3.5.0 招募可获名单列表（含当期特殊招募与 5★ 展示） |
| POST | `/gachas/available_unit_list/` | confirmed | CN 3.5.0 招募可获名单列表 POST 形态 |
| GET | `/gachas/choose_attr_page/` | confirmed | CN 3.5.0 每日轮换特殊招募入口页（Home/country_pick_page） |
| POST | `/gachas/choose_attr_page/` | confirmed | CN 3.5.0 每日轮换特殊招募入口页 POST 形态 |
| GET | `/gachas/choose_country_only_page/` | confirmed | CN 3.5.0 每日轮换属国/主题特殊招募页（country_pick_page） |
| POST | `/gachas/choose_country_only_page/` | confirmed | CN 3.5.0 每日轮换属国/主题特殊招募页 POST 形态 |
| GET | `/gachas/dia_only_gacha/` | confirmed | CN 3.5.0 每日轮换特殊招募页面（dia_only_gacha） |
| POST | `/gachas/dia_only_gacha/` | confirmed | CN 3.5.0 每日轮换特殊招募页面 POST 形态（dia_only_gacha） |
| GET | `/gachas/discount_gacha/` | confirmed | requireData 空态读面(gachas_discount_gacha) |
| POST | `/gachas/discount_gacha/` | confirmed | requireData 空态读面(gachas_discount_gacha) POST 形态 |
| GET | `/gachas/exchange_scout_book` | confirmed | CN 3.5.0 星级契约书 6 合 1 兑换（?item_id=100015\|100011） |
| POST | `/gachas/exchange_scout_book` | confirmed | CN 3.5.0 星级契约书 6 合 1 兑换 POST 形态 |
| GET | `/gachas/exchange_scout_book/` | confirmed | CN 3.5.0 星级契约书 6 合 1 兑换（?item_id=100015\|100011） |
| POST | `/gachas/exchange_scout_book/` | confirmed | CN 3.5.0 星级契约书 6 合 1 兑换 POST 形态 |
| GET | `/gachas/execute` | confirmed | CN 3.5.0 抽卡执行（单/连抽；幂等未达严格标准时拒绝真实扣费） |
| POST | `/gachas/execute` | confirmed | CN 3.5.0 抽卡执行 POST 形态 |
| GET | `/gachas/execute/` | confirmed | CN 3.5.0 抽卡执行（单/连抽；幂等未达严格标准时拒绝真实扣费） |
| POST | `/gachas/execute/` | confirmed | CN 3.5.0 抽卡执行 POST 形态 |
| GET | `/gachas/pickup_gacha/` | confirmed | CN 3.5.0 每日轮换特殊招募页面（pickup_gacha） |
| POST | `/gachas/pickup_gacha/` | confirmed | CN 3.5.0 每日轮换特殊招募页面 POST 形态（pickup_gacha） |
| GET | `/gachas/show/` | confirmed | CN 3.5.0 特殊招募总览（box/boys/girls） |
| POST | `/gachas/show/` | confirmed | CN 3.5.0 特殊招募总览 POST 形态 |
| GET | `/gachas/show_choice_list` | confirmed | requireData 空态读面(gachas_show_choice_list) |
| POST | `/gachas/show_choice_list` | confirmed | requireData 空态读面(gachas_show_choice_list) POST 形态 |
| GET | `/gachas/show_choice_list/` | confirmed | requireData 空态读面(gachas_show_choice_list) |
| POST | `/gachas/show_choice_list/` | confirmed | requireData 空态读面(gachas_show_choice_list) POST 形态 |
| GET | `/gachas/ticket_available_list` | confirmed | CN 3.5.0 ★3/★4/★5 信可用列表读面 |
| POST | `/gachas/ticket_available_list` | confirmed | CN 3.5.0 ★3/★4/★5 信可用列表 POST 形态 |
| GET | `/gachas/ticket_available_list/` | confirmed | CN 3.5.0 ★3/★4/★5 信可用列表读面 |
| POST | `/gachas/ticket_available_list/` | confirmed | CN 3.5.0 ★3/★4/★5 信可用列表 POST 形态 |
| GET | `/gachas/weapon_gacha/` | confirmed | CN 3.5.0 每日轮换特殊招募页面（weapon_gacha） |
| POST | `/gachas/weapon_gacha/` | confirmed | CN 3.5.0 每日轮换特殊招募页面 POST 形态（weapon_gacha） |

## global_rewards · `global_rewards`（8 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/global_rewards/index` | confirmed | requireData 空态读面(global_rewards_index) |
| POST | `/global_rewards/index` | confirmed | requireData 空态读面(global_rewards_index) POST 形态 |
| GET | `/global_rewards/index/` | confirmed | requireData 空态读面(global_rewards_index) |
| POST | `/global_rewards/index/` | confirmed | requireData 空态读面(global_rewards_index) POST 形态 |
| GET | `/global_rewards/reward_list` | confirmed | requireData 空态读面(global_rewards_reward_list) |
| POST | `/global_rewards/reward_list` | confirmed | requireData 空态读面(global_rewards_reward_list) POST 形态 |
| GET | `/global_rewards/reward_list/` | confirmed | requireData 空态读面(global_rewards_reward_list) |
| POST | `/global_rewards/reward_list/` | confirmed | requireData 空态读面(global_rewards_reward_list) POST 形态 |

## guild_rankings · `guild_rankings`（12 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/guild_rankings` | confirmed | requireData 空态读面(guild_rankings) |
| POST | `/guild_rankings` | confirmed | requireData 空态读面(guild_rankings) POST 形态 |
| GET | `/guild_rankings/` | confirmed | requireData 空态读面(guild_rankings) |
| POST | `/guild_rankings/` | confirmed | requireData 空态读面(guild_rankings) POST 形态 |
| GET | `/guild_rankings/item_rewards` | confirmed | requireData 空态读面(guild_rankings_item_rewards) |
| POST | `/guild_rankings/item_rewards` | confirmed | requireData 空态读面(guild_rankings_item_rewards) POST 形态 |
| GET | `/guild_rankings/item_rewards/` | confirmed | requireData 空态读面(guild_rankings_item_rewards) |
| POST | `/guild_rankings/item_rewards/` | confirmed | requireData 空态读面(guild_rankings_item_rewards) POST 形态 |
| GET | `/guild_rankings/skill_rewards` | confirmed | requireData 空态读面(guild_rankings_skill_rewards) |
| POST | `/guild_rankings/skill_rewards` | confirmed | requireData 空态读面(guild_rankings_skill_rewards) POST 形态 |
| GET | `/guild_rankings/skill_rewards/` | confirmed | requireData 空态读面(guild_rankings_skill_rewards) |
| POST | `/guild_rankings/skill_rewards/` | confirmed | requireData 空态读面(guild_rankings_skill_rewards) POST 形态 |

## 公会 · `guilds`（22 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/guilds/` | confirmed | requireData 空态读面(guilds) |
| POST | `/guilds/` | confirmed | requireData 空态读面(guilds) POST 形态 |
| GET | `/guilds/deck` | confirmed | requireData 空态读面(guilds_deck) |
| POST | `/guilds/deck` | confirmed | requireData 空态读面(guilds_deck) POST 形态 |
| GET | `/guilds/deck/` | confirmed | requireData 空态读面(guilds_deck) |
| POST | `/guilds/deck/` | confirmed | requireData 空态读面(guilds_deck) POST 形态 |
| GET | `/guilds/monsters` | confirmed | requireData 空态读面(guilds_monsters) |
| POST | `/guilds/monsters` | confirmed | requireData 空态读面(guilds_monsters) POST 形态 |
| GET | `/guilds/monsters/` | confirmed | requireData 空态读面(guilds_monsters) |
| POST | `/guilds/monsters/` | confirmed | requireData 空态读面(guilds_monsters) POST 形态 |
| GET | `/guilds/search_index` | confirmed | requireData 空态读面(guilds_search_index) |
| POST | `/guilds/search_index` | confirmed | requireData 空态读面(guilds_search_index) POST 形态 |
| GET | `/guilds/search_index/` | confirmed | requireData 空态读面(guilds_search_index) |
| POST | `/guilds/search_index/` | confirmed | requireData 空态读面(guilds_search_index) POST 形态 |
| GET | `/guilds/subscriptions` | confirmed | requireData 空态读面(guilds_subscriptions) |
| POST | `/guilds/subscriptions` | confirmed | requireData 空态读面(guilds_subscriptions) POST 形态 |
| GET | `/guilds/subscriptions/` | confirmed | requireData 空态读面(guilds_subscriptions) |
| POST | `/guilds/subscriptions/` | confirmed | requireData 空态读面(guilds_subscriptions) POST 形态 |
| GET | `/guilds/temporary_equipment_index` | confirmed | requireData 空态读面(guilds_temporary_equipment_index) |
| POST | `/guilds/temporary_equipment_index` | confirmed | requireData 空态读面(guilds_temporary_equipment_index) POST 形态 |
| GET | `/guilds/temporary_equipment_index/` | confirmed | requireData 空态读面(guilds_temporary_equipment_index) |
| POST | `/guilds/temporary_equipment_index/` | confirmed | requireData 空态读面(guilds_temporary_equipment_index) POST 形态 |

## 公告 · `infos`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/infos/index` | confirmed | CN 3.5.0 公告面（Home requireData.infos） |
| POST | `/infos/index` | confirmed | CN 3.5.0 公告面 POST 形态 |
| GET | `/infos/index/` | confirmed | CN 3.5.0 公告面（Home requireData.infos） |
| POST | `/infos/index/` | confirmed | CN 3.5.0 公告面 POST 形态 |

## invite_user_to_boys · `invite_user_to_boys`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/invite_user_to_boys/` | confirmed | requireData 空态读面(invite_user_to_boys) |
| POST | `/invite_user_to_boys/` | confirmed | requireData 空态读面(invite_user_to_boys) POST 形态 |

## ladders · `ladders`（20 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/ladders/get_candidates` | confirmed | requireData 空态读面(ladders_get_candidates) |
| POST | `/ladders/get_candidates` | confirmed | requireData 空态读面(ladders_get_candidates) POST 形态 |
| GET | `/ladders/get_candidates/` | confirmed | requireData 空态读面(ladders_get_candidates) |
| POST | `/ladders/get_candidates/` | confirmed | requireData 空态读面(ladders_get_candidates) POST 形态 |
| GET | `/ladders/monster_grow_index` | confirmed | requireData 空态读面(ladders_monster_grow_index) |
| POST | `/ladders/monster_grow_index` | confirmed | requireData 空态读面(ladders_monster_grow_index) POST 形态 |
| GET | `/ladders/monster_grow_index/` | confirmed | requireData 空态读面(ladders_monster_grow_index) |
| POST | `/ladders/monster_grow_index/` | confirmed | requireData 空态读面(ladders_monster_grow_index) POST 形态 |
| GET | `/ladders/monsters` | confirmed | requireData 空态读面(ladders_monsters) |
| POST | `/ladders/monsters` | confirmed | requireData 空态读面(ladders_monsters) POST 形态 |
| GET | `/ladders/monsters/` | confirmed | requireData 空态读面(ladders_monsters) |
| POST | `/ladders/monsters/` | confirmed | requireData 空态读面(ladders_monsters) POST 形态 |
| GET | `/ladders/show_ladder_info` | confirmed | requireData 空态读面(ladders_show_ladder_info) |
| POST | `/ladders/show_ladder_info` | confirmed | requireData 空态读面(ladders_show_ladder_info) POST 形态 |
| GET | `/ladders/show_ladder_info/` | confirmed | requireData 空态读面(ladders_show_ladder_info) |
| POST | `/ladders/show_ladder_info/` | confirmed | requireData 空态读面(ladders_show_ladder_info) POST 形态 |
| GET | `/ladders/show_rank` | confirmed | requireData 空态读面(ladders_show_rank) |
| POST | `/ladders/show_rank` | confirmed | requireData 空态读面(ladders_show_rank) POST 形态 |
| GET | `/ladders/show_rank/` | confirmed | requireData 空态读面(ladders_show_rank) |
| POST | `/ladders/show_rank/` | confirmed | requireData 空态读面(ladders_show_rank) POST 形态 |

## main_chara_buff · `main_chara_buff`（8 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/main_chara_buff/infos` | confirmed | requireData 空态读面(main_chara_buff_infos) |
| POST | `/main_chara_buff/infos` | confirmed | requireData 空态读面(main_chara_buff_infos) POST 形态 |
| GET | `/main_chara_buff/infos/` | confirmed | requireData 空态读面(main_chara_buff_infos) |
| POST | `/main_chara_buff/infos/` | confirmed | requireData 空态读面(main_chara_buff_infos) POST 形态 |
| GET | `/main_chara_buff/upgrade_infos` | confirmed | requireData 空态读面(main_chara_buff_upgrade_infos) |
| POST | `/main_chara_buff/upgrade_infos` | confirmed | requireData 空态读面(main_chara_buff_upgrade_infos) POST 形态 |
| GET | `/main_chara_buff/upgrade_infos/` | confirmed | requireData 空态读面(main_chara_buff_upgrade_infos) |
| POST | `/main_chara_buff/upgrade_infos/` | confirmed | requireData 空态读面(main_chara_buff_upgrade_infos) POST 形态 |

## 勋章道具 · `medal_items`（6 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/medal_items/` | confirmed | requireData 空态读面(medal_items) |
| POST | `/medal_items/` | confirmed | requireData 空态读面(medal_items) POST 形态 |
| GET | `/medal_items/team_shop_index` | confirmed | requireData 空态读面(medal_items_team_shop_index) |
| POST | `/medal_items/team_shop_index` | confirmed | requireData 空态读面(medal_items_team_shop_index) POST 形态 |
| GET | `/medal_items/team_shop_index/` | confirmed | requireData 空态读面(medal_items_team_shop_index) |
| POST | `/medal_items/team_shop_index/` | confirmed | requireData 空态读面(medal_items_team_shop_index) POST 形态 |

## monster_albums · `monster_albums`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/monster_albums/` | confirmed | CN 3.5.0 魔宠图鉴态（UserMonsterAlbum requireData.monster_albums；空态） |
| POST | `/monster_albums/` | confirmed | CN 3.5.0 魔宠图鉴态（UserMonsterAlbum requireData.monster_albums；空态） POST 形态 |

## 月卡 · `month_card`（16 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/month_card/month_card_daily_rewards` | confirmed | requireData 空态读面(month_card_month_card_daily_rewards) |
| POST | `/month_card/month_card_daily_rewards` | confirmed | requireData 空态读面(month_card_month_card_daily_rewards) POST 形态 |
| GET | `/month_card/month_card_daily_rewards/` | confirmed | requireData 空态读面(month_card_month_card_daily_rewards) |
| POST | `/month_card/month_card_daily_rewards/` | confirmed | requireData 空态读面(month_card_month_card_daily_rewards) POST 形态 |
| GET | `/month_card/month_card_gacha_items` | confirmed | requireData 空态读面(month_card_month_card_gacha_items) |
| POST | `/month_card/month_card_gacha_items` | confirmed | requireData 空态读面(month_card_month_card_gacha_items) POST 形态 |
| GET | `/month_card/month_card_gacha_items/` | confirmed | requireData 空态读面(month_card_month_card_gacha_items) |
| POST | `/month_card/month_card_gacha_items/` | confirmed | requireData 空态读面(month_card_month_card_gacha_items) POST 形态 |
| GET | `/month_card/show` | confirmed | requireData 空态读面(month_card_show) |
| POST | `/month_card/show` | confirmed | requireData 空态读面(month_card_show) POST 形态 |
| GET | `/month_card/show/` | confirmed | requireData 空态读面(month_card_show) |
| POST | `/month_card/show/` | confirmed | requireData 空态读面(month_card_show) POST 形态 |
| GET | `/month_card/show_gacha_page` | confirmed | requireData 空态读面(month_card_show_gacha_page) |
| POST | `/month_card/show_gacha_page` | confirmed | requireData 空态读面(month_card_show_gacha_page) POST 形态 |
| GET | `/month_card/show_gacha_page/` | confirmed | requireData 空态读面(month_card_show_gacha_page) |
| POST | `/month_card/show_gacha_page/` | confirmed | requireData 空态读面(month_card_show_gacha_page) POST 形态 |

## new_year2016_happy_gacha · `new_year2016_happy_gacha`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/new_year2016_happy_gacha/` | confirmed | requireData 空态读面(new_year2016_happy_gacha) |
| POST | `/new_year2016_happy_gacha/` | confirmed | requireData 空态读面(new_year2016_happy_gacha) POST 形态 |
| GET | `/new_year2016_happy_gacha/currency/` | confirmed | requireData 空态读面(new_year2016_happy_gacha_currency) |
| POST | `/new_year2016_happy_gacha/currency/` | confirmed | requireData 空态读面(new_year2016_happy_gacha_currency) POST 形态 |

## new_year2016_shop · `new_year2016_shop`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/new_year2016_shop/` | confirmed | requireData 空态读面(new_year2016_shop) |
| POST | `/new_year2016_shop/` | confirmed | requireData 空态读面(new_year2016_shop) POST 形态 |

## passive_skill · `passive_skill`（6 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/passive_skill/` | confirmed | requireData 空态读面(passive_skill) |
| POST | `/passive_skill/` | confirmed | requireData 空态读面(passive_skill) POST 形态 |
| GET | `/passive_skill/available_passive_skill_list` | confirmed | requireData 空态读面(passive_skill_available_passive_skill_list) |
| POST | `/passive_skill/available_passive_skill_list` | confirmed | requireData 空态读面(passive_skill_available_passive_skill_list) POST 形态 |
| GET | `/passive_skill/available_passive_skill_list/` | confirmed | requireData 空态读面(passive_skill_available_passive_skill_list) |
| POST | `/passive_skill/available_passive_skill_list/` | confirmed | requireData 空态读面(passive_skill_available_passive_skill_list) POST 形态 |

## 关卡/战斗 · `quests`（44 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/quests` | confirmed | CN 3.5.0 关卡列表（?area_id=N → quests_N，含账号通关标记） |
| POST | `/quests` | confirmed | CN 3.5.0 关卡列表 POST 形态 |
| GET | `/quests/` | confirmed | CN 3.5.0 关卡列表（?area_id=N → quests_N，含账号通关标记） |
| POST | `/quests/` | confirmed | CN 3.5.0 关卡列表 POST 形态 |
| GET | `/quests/continue` | confirmed | CN 3.5.0 续战确认（扣 1 钻，data.user_coin） |
| POST | `/quests/continue` | confirmed | CN 3.5.0 续战确认 POST 形态 |
| GET | `/quests/continue/` | confirmed | CN 3.5.0 续战确认（扣 1 钻，data.user_coin） |
| POST | `/quests/continue/` | confirmed | CN 3.5.0 续战确认 POST 形态 |
| GET | `/quests/current_quest_additional_option` | confirmed | CN 3.5.0 任务附加选项（?quest_id=N → QuestSelect option 文档） |
| POST | `/quests/current_quest_additional_option` | confirmed | CN 3.5.0 任务附加选项 POST 形态 |
| GET | `/quests/current_quest_additional_option/` | confirmed | CN 3.5.0 任务附加选项（?quest_id=N → QuestSelect option 文档） |
| POST | `/quests/current_quest_additional_option/` | confirmed | CN 3.5.0 任务附加选项 POST 形态 |
| GET | `/quests/daily` | confirmed | requireData 空态读面(quests_daily) |
| POST | `/quests/daily` | confirmed | requireData 空态读面(quests_daily) POST 形态 |
| GET | `/quests/daily/` | confirmed | requireData 空态读面(quests_daily) |
| POST | `/quests/daily/` | confirmed | requireData 空态读面(quests_daily) POST 形态 |
| GET | `/quests/event` | confirmed | requireData 空态读面(quests_event) |
| POST | `/quests/event` | confirmed | requireData 空态读面(quests_event) POST 形态 |
| GET | `/quests/event/` | confirmed | requireData 空态读面(quests_event) |
| POST | `/quests/event/` | confirmed | requireData 空态读面(quests_event) POST 形态 |
| GET | `/quests/event_available_unit` | confirmed | requireData 空态读面(quests_event_available_unit) |
| POST | `/quests/event_available_unit` | confirmed | requireData 空态读面(quests_event_available_unit) POST 形态 |
| GET | `/quests/event_available_unit/` | confirmed | requireData 空态读面(quests_event_available_unit) |
| POST | `/quests/event_available_unit/` | confirmed | requireData 空态读面(quests_event_available_unit) POST 形态 |
| GET | `/quests/execute/{filename}` | confirmed | CN 3.5.0 剧情战斗 execute（动态 id 路径） |
| POST | `/quests/execute/{filename}` | confirmed | CN 3.5.0 剧情战斗 execute POST 形态 |
| GET | `/quests/log_battle` | confirmed | CN 3.5.0 战斗日志归档（battle_record） |
| POST | `/quests/log_battle` | confirmed | CN 3.5.0 战斗日志归档 POST 形态 |
| GET | `/quests/log_battle/` | confirmed | CN 3.5.0 战斗日志归档（battle_record） |
| POST | `/quests/log_battle/` | confirmed | CN 3.5.0 战斗日志归档 POST 形态 |
| GET | `/quests/lose` | confirmed | CN 3.5.0 败北上报（会话转 lost+首败标志） |
| POST | `/quests/lose` | confirmed | CN 3.5.0 败北上报 POST 形态 |
| GET | `/quests/lose/` | confirmed | CN 3.5.0 败北上报（会话转 lost+首败标志） |
| POST | `/quests/lose/` | confirmed | CN 3.5.0 败北上报 POST 形态 |
| GET | `/quests/reprint_events/` | confirmed | CN 3.5.0 复刻迷你活动列表（ReprintMiniEventsSelect requireData.reprint_events；空态） |
| POST | `/quests/reprint_events/` | confirmed | CN 3.5.0 复刻迷你活动列表（ReprintMiniEventsSelect requireData.reprint_events；空态） POST 形态 |
| GET | `/quests/result` | confirmed | CN 3.5.0 战斗结算（success/already_received 幂等） |
| POST | `/quests/result` | confirmed | CN 3.5.0 战斗结算 POST 形态 |
| GET | `/quests/result/` | confirmed | CN 3.5.0 战斗结算（success/already_received 幂等） |
| POST | `/quests/result/` | confirmed | CN 3.5.0 战斗结算 POST 形态 |
| GET | `/quests/use_ap` | confirmed | CN 3.5.0 AP 扣点（execute 下发 URL，会话幂等） |
| POST | `/quests/use_ap` | confirmed | CN 3.5.0 AP 扣点 POST 形态 |
| GET | `/quests/use_ap/` | confirmed | CN 3.5.0 AP 扣点（execute 下发 URL，会话幂等） |
| POST | `/quests/use_ap/` | confirmed | CN 3.5.0 AP 扣点 POST 形态 |

## raid_monsters · `raid_monsters`（46 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/raid_monsters/` | confirmed | 暴走魔宠主页读面（raid_condition/my_raid_monster/协力列表/日志） |
| POST | `/raid_monsters/` | confirmed | 暴走魔宠主页读面（raid_condition/my_raid_monster/协力列表/日志） POST 形态 |
| GET | `/raid_monsters/bp_use` | confirmed | 暴走魔宠开战 BP 扣费 |
| POST | `/raid_monsters/bp_use` | confirmed | 暴走魔宠开战 BP 扣费 POST 形态 |
| GET | `/raid_monsters/bp_use/` | confirmed | 暴走魔宠开战 BP 扣费 |
| POST | `/raid_monsters/bp_use/` | confirmed | 暴走魔宠开战 BP 扣费 POST 形态 |
| GET | `/raid_monsters/call_help` | confirmed | 暴走魔宠发送协力求援（guild/community_topic） |
| POST | `/raid_monsters/call_help` | confirmed | 暴走魔宠发送协力求援（guild/community_topic） POST 形态 |
| GET | `/raid_monsters/call_help/` | confirmed | 暴走魔宠发送协力求援（guild/community_topic） |
| POST | `/raid_monsters/call_help/` | confirmed | 暴走魔宠发送协力求援（guild/community_topic） POST 形态 |
| GET | `/raid_monsters/damage_list` | confirmed | 暴走魔宠协力者伤害排行 |
| POST | `/raid_monsters/damage_list` | confirmed | 暴走魔宠协力者伤害排行 POST 形态 |
| GET | `/raid_monsters/damage_list/` | confirmed | 暴走魔宠协力者伤害排行 |
| POST | `/raid_monsters/damage_list/` | confirmed | 暴走魔宠协力者伤害排行 POST 形态 |
| GET | `/raid_monsters/exchange` | confirmed | 暴走魔宠击退 pt 交换所兑换写面 |
| POST | `/raid_monsters/exchange` | confirmed | 暴走魔宠击退 pt 交换所兑换写面 POST 形态 |
| GET | `/raid_monsters/exchange/` | confirmed | 暴走魔宠击退 pt 交换所兑换写面 |
| POST | `/raid_monsters/exchange/` | confirmed | 暴走魔宠击退 pt 交换所兑换写面 POST 形态 |
| GET | `/raid_monsters/execute.json` | confirmed | 暴走魔宠出击开战（三档 BP/武器特攻/设施加成/单波 Boss） |
| POST | `/raid_monsters/execute.json` | confirmed | 暴走魔宠出击开战（三档 BP/武器特攻/设施加成/单波 Boss） POST 形态 |
| GET | `/raid_monsters/execute.json/` | confirmed | 暴走魔宠出击开战（三档 BP/武器特攻/设施加成/单波 Boss） |
| POST | `/raid_monsters/execute.json/` | confirmed | 暴走魔宠出击开战（三档 BP/武器特攻/设施加成/单波 Boss） POST 形态 |
| GET | `/raid_monsters/giveup` | confirmed | 暴走魔宠放走确认/执行 |
| POST | `/raid_monsters/giveup` | confirmed | 暴走魔宠放走确认/执行 POST 形态 |
| GET | `/raid_monsters/giveup/` | confirmed | 暴走魔宠放走确认/执行 |
| POST | `/raid_monsters/giveup/` | confirmed | 暴走魔宠放走确认/执行 POST 形态 |
| GET | `/raid_monsters/lose` | confirmed | 暴走魔宠限时/败北伤害结算（damage[raid]） |
| POST | `/raid_monsters/lose` | confirmed | 暴走魔宠限时/败北伤害结算（damage[raid]） POST 形态 |
| GET | `/raid_monsters/lose/` | confirmed | 暴走魔宠限时/败北伤害结算（damage[raid]） |
| POST | `/raid_monsters/lose/` | confirmed | 暴走魔宠限时/败北伤害结算（damage[raid]） POST 形态 |
| GET | `/raid_monsters/lv_bonus` | confirmed | 暴走魔宠击退 Lv 奖励表 |
| POST | `/raid_monsters/lv_bonus` | confirmed | 暴走魔宠击退 Lv 奖励表 POST 形态 |
| GET | `/raid_monsters/lv_bonus/` | confirmed | 暴走魔宠击退 Lv 奖励表 |
| POST | `/raid_monsters/lv_bonus/` | confirmed | 暴走魔宠击退 Lv 奖励表 POST 形态 |
| GET | `/raid_monsters/pt_bonus` | confirmed | 暴走魔宠击退 pt 交换所商品表 |
| POST | `/raid_monsters/pt_bonus` | confirmed | 暴走魔宠击退 pt 交换所商品表 POST 形态 |
| GET | `/raid_monsters/pt_bonus/` | confirmed | 暴走魔宠击退 pt 交换所商品表 |
| POST | `/raid_monsters/pt_bonus/` | confirmed | 暴走魔宠击退 pt 交换所商品表 POST 形态 |
| GET | `/raid_monsters/result` | confirmed | 暴走魔宠击退胜利结算 |
| POST | `/raid_monsters/result` | confirmed | 暴走魔宠击退胜利结算 POST 形态 |
| GET | `/raid_monsters/result/` | confirmed | 暴走魔宠击退胜利结算 |
| POST | `/raid_monsters/result/` | confirmed | 暴走魔宠击退胜利结算 POST 形态 |
| GET | `/raid_monsters/show` | confirmed | 暴走魔宠出击准备页（temp_raid_monster/battle_weapon_rate） |
| POST | `/raid_monsters/show` | confirmed | 暴走魔宠出击准备页（temp_raid_monster/battle_weapon_rate） POST 形态 |
| GET | `/raid_monsters/show/` | confirmed | 暴走魔宠出击准备页（temp_raid_monster/battle_weapon_rate） |
| POST | `/raid_monsters/show/` | confirmed | 暴走魔宠出击准备页（temp_raid_monster/battle_weapon_rate） POST 形态 |

## recall_users · `recall_users`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/recall_users/` | confirmed | requireData 空态读面(recall_users) |
| POST | `/recall_users/` | confirmed | requireData 空态读面(recall_users) POST 形态 |

## red_diamond_shop · `red_diamond_shop`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/red_diamond_shop/` | confirmed | requireData 空态读面(red_diamond_shop) |
| POST | `/red_diamond_shop/` | confirmed | requireData 空态读面(red_diamond_shop) POST 形态 |

## rob_battles · `rob_battles`（12 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/rob_battles` | confirmed | requireData 空态读面(rob_battles) |
| POST | `/rob_battles` | confirmed | requireData 空态读面(rob_battles) POST 形态 |
| GET | `/rob_battles/` | confirmed | requireData 空态读面(rob_battles) |
| POST | `/rob_battles/` | confirmed | requireData 空态读面(rob_battles) POST 形态 |
| GET | `/rob_battles/get_candidates` | confirmed | requireData 空态读面(rob_battles_get_candidates) |
| POST | `/rob_battles/get_candidates` | confirmed | requireData 空态读面(rob_battles_get_candidates) POST 形态 |
| GET | `/rob_battles/get_candidates/` | confirmed | requireData 空态读面(rob_battles_get_candidates) |
| POST | `/rob_battles/get_candidates/` | confirmed | requireData 空态读面(rob_battles_get_candidates) POST 形态 |
| GET | `/rob_battles/show_rob_battles_info` | confirmed | requireData 空态读面(rob_battles_show_rob_battles_info) |
| POST | `/rob_battles/show_rob_battles_info` | confirmed | requireData 空态读面(rob_battles_show_rob_battles_info) POST 形态 |
| GET | `/rob_battles/show_rob_battles_info/` | confirmed | requireData 空态读面(rob_battles_show_rob_battles_info) |
| POST | `/rob_battles/show_rob_battles_info/` | confirmed | requireData 空态读面(rob_battles_show_rob_battles_info) POST 形态 |

## stages · `stages`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/stages` | confirmed | CN 3.5.0 关卡目录（stages 五 tab 键，按账号 stage_id 过滤） |
| POST | `/stages` | confirmed | CN 3.5.0 关卡目录 POST 形态 |
| GET | `/stages/` | confirmed | CN 3.5.0 关卡目录（stages 五 tab 键，按账号 stage_id 过滤） |
| POST | `/stages/` | confirmed | CN 3.5.0 关卡目录 POST 形态 |

## 剧情 · `stories`（6 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/stories/list` | confirmed | CN 3.5.0 章节故事列表（?chapter_id=N → stories_N；Chapters option 注入） |
| POST | `/stories/list` | confirmed | CN 3.5.0 章节故事列表 POST 形态 |
| GET | `/stories/list/` | confirmed | CN 3.5.0 章节故事列表（?chapter_id=N → stories_N；Chapters option 注入） |
| POST | `/stories/list/` | confirmed | CN 3.5.0 章节故事列表 POST 形态 |
| GET | `/stories/read/{filename}` | confirmed | CN 3.5.0 剧本读取（/stories/read/<id>.json；原文下发无 data 信封） |
| POST | `/stories/read/{filename}` | confirmed | CN 3.5.0 剧本读取 POST 形态 |

## 任务 · `task`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/task/show_tutorial_tasks` | confirmed | CN 3.5.0 新手任务列表（TutorialTasks 未装配，空态） |
| POST | `/task/show_tutorial_tasks` | confirmed | CN 3.5.0 新手任务列表空态 POST 形态 |
| GET | `/task/show_tutorial_tasks/` | confirmed | CN 3.5.0 新手任务列表（TutorialTasks 未装配，空态） |
| POST | `/task/show_tutorial_tasks/` | confirmed | CN 3.5.0 新手任务列表空态 POST 形态 |

## team_quest · `team_quest`（32 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/team_quest/get_team_areas` | confirmed | CN 3.5.0 协力区域（StageSelect requireData.specials；空态） |
| POST | `/team_quest/get_team_areas` | confirmed | CN 3.5.0 协力区域（StageSelect requireData.specials；空态） POST 形态 |
| GET | `/team_quest/get_team_areas/` | confirmed | CN 3.5.0 协力区域（StageSelect requireData.specials；空态） |
| POST | `/team_quest/get_team_areas/` | confirmed | CN 3.5.0 协力区域（StageSelect requireData.specials；空态） POST 形态 |
| GET | `/team_quest/get_team_quests` | confirmed | requireData 空态读面(team_quest_get_team_quests) |
| POST | `/team_quest/get_team_quests` | confirmed | requireData 空态读面(team_quest_get_team_quests) POST 形态 |
| GET | `/team_quest/get_team_quests/` | confirmed | requireData 空态读面(team_quest_get_team_quests) |
| POST | `/team_quest/get_team_quests/` | confirmed | requireData 空态读面(team_quest_get_team_quests) POST 形态 |
| GET | `/team_quest/get_team_rooms` | confirmed | requireData 空态读面(team_quest_get_team_rooms) |
| POST | `/team_quest/get_team_rooms` | confirmed | requireData 空态读面(team_quest_get_team_rooms) POST 形态 |
| GET | `/team_quest/get_team_rooms/` | confirmed | requireData 空态读面(team_quest_get_team_rooms) |
| POST | `/team_quest/get_team_rooms/` | confirmed | requireData 空态读面(team_quest_get_team_rooms) POST 形态 |
| GET | `/team_quest/get_team_rooms_filters` | confirmed | requireData 空态读面(team_quest_get_team_rooms_filters) |
| POST | `/team_quest/get_team_rooms_filters` | confirmed | requireData 空态读面(team_quest_get_team_rooms_filters) POST 形态 |
| GET | `/team_quest/get_team_rooms_filters/` | confirmed | requireData 空态读面(team_quest_get_team_rooms_filters) |
| POST | `/team_quest/get_team_rooms_filters/` | confirmed | requireData 空态读面(team_quest_get_team_rooms_filters) POST 形态 |
| GET | `/team_quest/invite_team_member` | confirmed | requireData 空态读面(team_quest_invite_team_member) |
| POST | `/team_quest/invite_team_member` | confirmed | requireData 空态读面(team_quest_invite_team_member) POST 形态 |
| GET | `/team_quest/invite_team_member/` | confirmed | requireData 空态读面(team_quest_invite_team_member) |
| POST | `/team_quest/invite_team_member/` | confirmed | requireData 空态读面(team_quest_invite_team_member) POST 形态 |
| GET | `/team_quest/ranks` | confirmed | requireData 空态读面(team_quest_ranks) |
| POST | `/team_quest/ranks` | confirmed | requireData 空态读面(team_quest_ranks) POST 形态 |
| GET | `/team_quest/ranks/` | confirmed | requireData 空态读面(team_quest_ranks) |
| POST | `/team_quest/ranks/` | confirmed | requireData 空态读面(team_quest_ranks) POST 形态 |
| GET | `/team_quest/rewards` | confirmed | requireData 空态读面(team_quest_rewards) |
| POST | `/team_quest/rewards` | confirmed | requireData 空态读面(team_quest_rewards) POST 形态 |
| GET | `/team_quest/rewards/` | confirmed | requireData 空态读面(team_quest_rewards) |
| POST | `/team_quest/rewards/` | confirmed | requireData 空态读面(team_quest_rewards) POST 形态 |
| GET | `/team_quest/user_tasks` | confirmed | requireData 空态读面(team_quest_user_tasks) |
| POST | `/team_quest/user_tasks` | confirmed | requireData 空态读面(team_quest_user_tasks) POST 形态 |
| GET | `/team_quest/user_tasks/` | confirmed | requireData 空态读面(team_quest_user_tasks) |
| POST | `/team_quest/user_tasks/` | confirmed | requireData 空态读面(team_quest_user_tasks) POST 形态 |

## toto_image_4 · `toto_image_4`（1 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/toto_image_4/unity/{filename}` | confirmed | CN 3.5.0 unity3d CDN 兜底（内嵌缺失项） |

## trials · `trials`（8 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/trials/` | confirmed | CN 3.5.0 试炼列表（StageSelect requireData.trials；空态） |
| POST | `/trials/` | confirmed | CN 3.5.0 试炼列表（StageSelect requireData.trials；空态） POST 形态 |
| GET | `/trials/clear_count_bonus` | confirmed | CN 3.5.0 试炼通关次数奖励（TrialsRewardPage requireData.trials_bonus；空态） |
| POST | `/trials/clear_count_bonus` | confirmed | CN 3.5.0 试炼通关次数奖励（TrialsRewardPage requireData.trials_bonus；空态） POST 形态 |
| GET | `/trials/clear_count_bonus/` | confirmed | CN 3.5.0 试炼通关次数奖励（TrialsRewardPage requireData.trials_bonus；空态） |
| POST | `/trials/clear_count_bonus/` | confirmed | CN 3.5.0 试炼通关次数奖励（TrialsRewardPage requireData.trials_bonus；空态） POST 形态 |
| GET | `/trials/daily/` | confirmed | CN 3.5.0 日替试炼关卡（TrialsQuestSelect requireData.trials_quests；空态） |
| POST | `/trials/daily/` | confirmed | CN 3.5.0 日替试炼关卡（TrialsQuestSelect requireData.trials_quests；空态） POST 形态 |

## truelove · `truelove`（12 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/truelove/list` | confirmed | requireData 空态读面(truelove_list) |
| POST | `/truelove/list` | confirmed | requireData 空态读面(truelove_list) POST 形态 |
| GET | `/truelove/list/` | confirmed | requireData 空态读面(truelove_list) |
| POST | `/truelove/list/` | confirmed | requireData 空态读面(truelove_list) POST 形态 |
| GET | `/truelove/main_info` | confirmed | requireData 空态读面(truelove_main_info) |
| POST | `/truelove/main_info` | confirmed | requireData 空态读面(truelove_main_info) POST 形态 |
| GET | `/truelove/main_info/` | confirmed | requireData 空态读面(truelove_main_info) |
| POST | `/truelove/main_info/` | confirmed | requireData 空态读面(truelove_main_info) POST 形态 |
| GET | `/truelove/town_info` | confirmed | requireData 空态读面(truelove_town_info) |
| POST | `/truelove/town_info` | confirmed | requireData 空态读面(truelove_town_info) POST 形态 |
| GET | `/truelove/town_info/` | confirmed | requireData 空态读面(truelove_town_info) |
| POST | `/truelove/town_info/` | confirmed | requireData 空态读面(truelove_town_info) POST 形态 |

## unit_ability_exchange · `unit_ability_exchange`（16 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/unit_ability_exchange/exchange_unit_ability` | confirmed | CN 3.5.0 能力互换执行（ChangeAbilityConfirmModal.cs:143） |
| POST | `/unit_ability_exchange/exchange_unit_ability` | confirmed | CN 3.5.0 能力互换执行 POST 形态 |
| GET | `/unit_ability_exchange/exchange_unit_ability/` | confirmed | CN 3.5.0 能力互换执行（ChangeAbilityConfirmModal.cs:143） |
| POST | `/unit_ability_exchange/exchange_unit_ability/` | confirmed | CN 3.5.0 能力互换执行 POST 形态 |
| GET | `/unit_ability_exchange/get_unit_ability_exchange_info` | confirmed | CN 3.5.0 能力互换页信息（ChangeAbilityModal.cs:96） |
| POST | `/unit_ability_exchange/get_unit_ability_exchange_info` | confirmed | CN 3.5.0 能力互换页信息 POST 形态 |
| GET | `/unit_ability_exchange/get_unit_ability_exchange_info/` | confirmed | CN 3.5.0 能力互换页信息（ChangeAbilityModal.cs:96） |
| POST | `/unit_ability_exchange/get_unit_ability_exchange_info/` | confirmed | CN 3.5.0 能力互换页信息 POST 形态 |
| GET | `/unit_ability_exchange/get_unit_ability_exchange_result` | confirmed | CN 3.5.0 能力互换预览（ChangeAbilityListModal.cs:204） |
| POST | `/unit_ability_exchange/get_unit_ability_exchange_result` | confirmed | CN 3.5.0 能力互换预览 POST 形态 |
| GET | `/unit_ability_exchange/get_unit_ability_exchange_result/` | confirmed | CN 3.5.0 能力互换预览（ChangeAbilityListModal.cs:204） |
| POST | `/unit_ability_exchange/get_unit_ability_exchange_result/` | confirmed | CN 3.5.0 能力互换预览 POST 形态 |
| GET | `/unit_ability_exchange/select_unit` | confirmed | CN 3.5.0 能力互换候选伙伴列表（ChangeAbilityListModal.cs:92） |
| POST | `/unit_ability_exchange/select_unit` | confirmed | CN 3.5.0 能力互换候选伙伴列表 POST 形态 |
| GET | `/unit_ability_exchange/select_unit/` | confirmed | CN 3.5.0 能力互换候选伙伴列表（ChangeAbilityListModal.cs:92） |
| POST | `/unit_ability_exchange/select_unit/` | confirmed | CN 3.5.0 能力互换候选伙伴列表 POST 形态 |

## unit_exchange · `unit_exchange`（6 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/unit_exchange/` | confirmed | requireData 空态读面(unit_exchange) |
| POST | `/unit_exchange/` | confirmed | requireData 空态读面(unit_exchange) POST 形态 |
| GET | `/unit_exchange/show_page` | confirmed | requireData 空态读面(unit_exchange_show_page) |
| POST | `/unit_exchange/show_page` | confirmed | requireData 空态读面(unit_exchange_show_page) POST 形态 |
| GET | `/unit_exchange/show_page/` | confirmed | requireData 空态读面(unit_exchange_show_page) |
| POST | `/unit_exchange/show_page/` | confirmed | requireData 空态读面(unit_exchange_show_page) POST 形态 |

## unit_friendship · `unit_friendship`（16 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/unit_friendship/show_description` | confirmed | requireData 空态读面(unit_friendship_show_description) |
| POST | `/unit_friendship/show_description` | confirmed | requireData 空态读面(unit_friendship_show_description) POST 形态 |
| GET | `/unit_friendship/show_description/` | confirmed | requireData 空态读面(unit_friendship_show_description) |
| POST | `/unit_friendship/show_description/` | confirmed | requireData 空态读面(unit_friendship_show_description) POST 形态 |
| GET | `/unit_friendship/show_friendship_list` | confirmed | requireData 空态读面(unit_friendship_show_friendship_list) |
| POST | `/unit_friendship/show_friendship_list` | confirmed | requireData 空态读面(unit_friendship_show_friendship_list) POST 形态 |
| GET | `/unit_friendship/show_friendship_list/` | confirmed | requireData 空态读面(unit_friendship_show_friendship_list) |
| POST | `/unit_friendship/show_friendship_list/` | confirmed | requireData 空态读面(unit_friendship_show_friendship_list) POST 形态 |
| GET | `/unit_friendship/show_page` | confirmed | requireData 空态读面(unit_friendship_show_page) |
| POST | `/unit_friendship/show_page` | confirmed | requireData 空态读面(unit_friendship_show_page) POST 形态 |
| GET | `/unit_friendship/show_page/` | confirmed | requireData 空态读面(unit_friendship_show_page) |
| POST | `/unit_friendship/show_page/` | confirmed | requireData 空态读面(unit_friendship_show_page) POST 形态 |
| GET | `/unit_friendship/show_unit_list` | confirmed | requireData 空态读面(unit_friendship_show_unit_list) |
| POST | `/unit_friendship/show_unit_list` | confirmed | requireData 空态读面(unit_friendship_show_unit_list) POST 形态 |
| GET | `/unit_friendship/show_unit_list/` | confirmed | requireData 空态读面(unit_friendship_show_unit_list) |
| POST | `/unit_friendship/show_unit_list/` | confirmed | requireData 空态读面(unit_friendship_show_unit_list) POST 形态 |

## 编队 · `user_decks`（12 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/user_decks` | confirmed | CN 3.5.0 账号独立编队（parties 按 display_order 有序下发） |
| POST | `/user_decks` | confirmed | CN 3.5.0 账号编队读取 POST 形态 |
| GET | `/user_decks/` | confirmed | CN 3.5.0 账号独立编队（parties 按 display_order 有序下发） |
| POST | `/user_decks/` | confirmed | CN 3.5.0 账号编队读取 POST 形态 |
| GET | `/user_decks/off` | confirmed | CN 3.5.0 编队槽位清空（保留槽位并校验旧模板） |
| POST | `/user_decks/off` | confirmed | CN 3.5.0 编队槽位清空 POST 形态 |
| GET | `/user_decks/off/` | confirmed | CN 3.5.0 编队槽位清空（保留槽位并校验旧模板） |
| POST | `/user_decks/off/` | confirmed | CN 3.5.0 编队槽位清空 POST 形态 |
| GET | `/user_decks/on` | confirmed | CN 3.5.0 编队槽位设置（会话账号/模板库存/队内唯一） |
| POST | `/user_decks/on` | confirmed | CN 3.5.0 编队槽位设置 POST 形态 |
| GET | `/user_decks/on/` | confirmed | CN 3.5.0 编队槽位设置（会话账号/模板库存/队内唯一） |
| POST | `/user_decks/on/` | confirmed | CN 3.5.0 编队槽位设置 POST 形态 |

## 关注 · `user_follows`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/user_follows/` | confirmed | requireData 空态读面(user_follows) |
| POST | `/user_follows/` | confirmed | requireData 空态读面(user_follows) POST 形态 |

## 道具 · `user_items`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/user_items/` | confirmed | CN 3.5.0 道具列表（UserItems.user_items / UserItemsV2.user_items_hash；空态） |
| POST | `/user_items/` | confirmed | CN 3.5.0 道具列表（UserItems.user_items / UserItemsV2.user_items_hash；空态） POST 形态 |

## user_message_blocks · `user_message_blocks`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/user_message_blocks/` | confirmed | requireData 空态读面(user_message_blocks) |
| POST | `/user_message_blocks/` | confirmed | requireData 空态读面(user_message_blocks) POST 形态 |

## 怪兽 · `user_monsters`（48 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/user_monsters` | confirmed | CN 3.5.0 魔宠列表（账号库存×catalog，捆绑 user_units） |
| POST | `/user_monsters` | confirmed | CN 3.5.0 魔宠列表 POST 形态 |
| GET | `/user_monsters/{filename}` | confirmed | CN 3.5.0 魔宠写面兜底（未取证子项明确拒绝） |
| POST | `/user_monsters/{filename}` | confirmed | CN 3.5.0 魔宠写面兜底 POST 形态 |
| GET | `/user_monsters/evolve` | confirmed | CN 3.5.0 魔宠融合执行（MergeMonsteUpModal.cs:379） |
| POST | `/user_monsters/evolve` | confirmed | CN 3.5.0 魔宠融合执行 POST 形态 |
| GET | `/user_monsters/evolve/` | confirmed | CN 3.5.0 魔宠融合执行（MergeMonsteUpModal.cs:379） |
| POST | `/user_monsters/evolve/` | confirmed | CN 3.5.0 魔宠融合执行 POST 形态 |
| GET | `/user_monsters/evolve_egg` | confirmed | CN 3.5.0 进化之卵融合执行（MergeEggUpModal.cs:375） |
| POST | `/user_monsters/evolve_egg` | confirmed | CN 3.5.0 进化之卵融合执行 POST 形态 |
| GET | `/user_monsters/evolve_egg/` | confirmed | CN 3.5.0 进化之卵融合执行（MergeEggUpModal.cs:375） |
| POST | `/user_monsters/evolve_egg/` | confirmed | CN 3.5.0 进化之卵融合执行 POST 形态 |
| GET | `/user_monsters/evolve_monster_select_info` | confirmed | CN 3.5.0 魔宠融合候选详情（MergeMonsterModal.cs:76） |
| POST | `/user_monsters/evolve_monster_select_info` | confirmed | CN 3.5.0 魔宠融合候选详情 POST 形态 |
| GET | `/user_monsters/evolve_monster_select_info/` | confirmed | CN 3.5.0 魔宠融合候选详情（MergeMonsterModal.cs:76） |
| POST | `/user_monsters/evolve_monster_select_info/` | confirmed | CN 3.5.0 魔宠融合候选详情 POST 形态 |
| GET | `/user_monsters/evolve_select_info` | confirmed | CN 3.5.0 魔宠进化选择列表（MonsterEvolveModal requireData） |
| POST | `/user_monsters/evolve_select_info` | confirmed | CN 3.5.0 魔宠进化选择列表 POST 形态 |
| GET | `/user_monsters/evolve_select_info/` | confirmed | CN 3.5.0 魔宠进化选择列表（MonsterEvolveModal requireData） |
| POST | `/user_monsters/evolve_select_info/` | confirmed | CN 3.5.0 魔宠进化选择列表 POST 形态 |
| GET | `/user_monsters/off` | confirmed | CN 3.5.0 魔宠卸下（?uuid=&unit_id=，UserMonsterDetail.cs:366/414） |
| POST | `/user_monsters/off` | confirmed | CN 3.5.0 魔宠卸下 POST 形态 |
| GET | `/user_monsters/off/` | confirmed | CN 3.5.0 魔宠卸下（?uuid=&unit_id=，UserMonsterDetail.cs:366/414） |
| POST | `/user_monsters/off/` | confirmed | CN 3.5.0 魔宠卸下 POST 形态 |
| GET | `/user_monsters/on` | confirmed | CN 3.5.0 魔宠装备（?uuid=&unit_id=，UserMonsterDetail.cs:390） |
| POST | `/user_monsters/on` | confirmed | CN 3.5.0 魔宠装备 POST 形态 |
| GET | `/user_monsters/on/` | confirmed | CN 3.5.0 魔宠装备（?uuid=&unit_id=，UserMonsterDetail.cs:390） |
| POST | `/user_monsters/on/` | confirmed | CN 3.5.0 魔宠装备 POST 形态 |
| GET | `/user_monsters/powerup` | confirmed | CN 3.5.0 魔宠强化（?uuid=&powerup_item_id=&count=，MonsterPowerupModal.cs:184） |
| POST | `/user_monsters/powerup` | confirmed | CN 3.5.0 魔宠强化 POST 形态 |
| GET | `/user_monsters/powerup/` | confirmed | CN 3.5.0 魔宠强化（?uuid=&powerup_item_id=&count=，MonsterPowerupModal.cs:184） |
| POST | `/user_monsters/powerup/` | confirmed | CN 3.5.0 魔宠强化 POST 形态 |
| GET | `/user_monsters/protect` | confirmed | CN 3.5.0 魔宠保护位（?uuids[]=&protected=，UserMonsterDetail.cs:515-516） |
| POST | `/user_monsters/protect` | confirmed | CN 3.5.0 魔宠保护位 POST 形态 |
| GET | `/user_monsters/protect/` | confirmed | CN 3.5.0 魔宠保护位（?uuids[]=&protected=，UserMonsterDetail.cs:515-516） |
| POST | `/user_monsters/protect/` | confirmed | CN 3.5.0 魔宠保护位 POST 形态 |
| GET | `/user_monsters/rare_monster_category_config` | confirmed | CN 3.5.0 融合宠强化档位配置（ScaleCategory_New.cs:67） |
| POST | `/user_monsters/rare_monster_category_config` | confirmed | CN 3.5.0 融合宠强化档位配置 POST 形态 |
| GET | `/user_monsters/rare_monster_category_config/` | confirmed | CN 3.5.0 融合宠强化档位配置（ScaleCategory_New.cs:67） |
| POST | `/user_monsters/rare_monster_category_config/` | confirmed | CN 3.5.0 融合宠强化档位配置 POST 形态 |
| GET | `/user_monsters/sell` | confirmed | CN 3.5.0 魔宠出售（?uuids[]=，MonsterSaleConfirmModal.cs:37） |
| POST | `/user_monsters/sell` | confirmed | CN 3.5.0 魔宠出售 POST 形态 |
| GET | `/user_monsters/sell/` | confirmed | CN 3.5.0 魔宠出售（?uuids[]=，MonsterSaleConfirmModal.cs:37） |
| POST | `/user_monsters/sell/` | confirmed | CN 3.5.0 魔宠出售 POST 形态 |
| GET | `/user_monsters/show_evolve_board` | confirmed | CN 3.5.0 进化之卵融合面板配置（MergeEggModal.cs:370） |
| POST | `/user_monsters/show_evolve_board` | confirmed | CN 3.5.0 进化之卵融合面板配置 POST 形态 |
| GET | `/user_monsters/show_evolve_board/` | confirmed | CN 3.5.0 进化之卵融合面板配置（MergeEggModal.cs:370） |
| POST | `/user_monsters/show_evolve_board/` | confirmed | CN 3.5.0 进化之卵融合面板配置 POST 形态 |

## 礼物箱 · `user_presents`（6 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/user_presents/` | confirmed | CN 3.5.0 待领礼物列表（Home requireData.user_presents） |
| POST | `/user_presents/` | confirmed | CN 3.5.0 待领礼物列表 POST 形态 |
| GET | `/user_presents/receive` | confirmed | CN 3.5.0 礼物领取（去尾斜杠形） |
| POST | `/user_presents/receive` | confirmed | CN 3.5.0 礼物领取 POST 形态（去尾斜杠形） |
| GET | `/user_presents/receive/` | confirmed | CN 3.5.0 礼物领取（单领/全领，同事务发奖+标记） |
| POST | `/user_presents/receive/` | confirmed | CN 3.5.0 礼物领取 POST 形态 |

## user_rob_decks · `user_rob_decks`（4 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/user_rob_decks` | confirmed | requireData 空态读面(user_rob_decks) |
| POST | `/user_rob_decks` | confirmed | requireData 空态读面(user_rob_decks) POST 形态 |
| GET | `/user_rob_decks/` | confirmed | requireData 空态读面(user_rob_decks) |
| POST | `/user_rob_decks/` | confirmed | requireData 空态读面(user_rob_decks) POST 形态 |

## 技能 · `user_skills`（2 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/user_skills/` | confirmed | CN 3.5.0 符文列表（UserSkills/UserSkillsWithUnit requireData.user_skills；空态） |
| POST | `/user_skills/` | confirmed | CN 3.5.0 符文列表（UserSkills/UserSkillsWithUnit requireData.user_skills；空态） POST 形态 |

## 单位 · `user_units`（28 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/user_units` | confirmed | CN 3.5.0 同伴列表（账号库存×catalog，捆绑 user_monsters） |
| POST | `/user_units` | confirmed | CN 3.5.0 同伴列表 POST 形态 |
| GET | `/user_units/{filename}` | confirmed | CN 3.5.0 单位养成写面兜底（未启用的子项明确拒绝） |
| POST | `/user_units/{filename}` | confirmed | CN 3.5.0 单位养成写面兜底 POST 形态 |
| GET | `/user_units/evolution` | confirmed | CN 3.5.0 伙伴进化（?id=，UnitEvolutionModal.cs:145） |
| POST | `/user_units/evolution` | confirmed | CN 3.5.0 伙伴进化 POST 形态 |
| GET | `/user_units/evolution/` | confirmed | CN 3.5.0 伙伴进化（?id=，UnitEvolutionModal.cs:145） |
| POST | `/user_units/evolution/` | confirmed | CN 3.5.0 伙伴进化 POST 形态 |
| GET | `/user_units/protect` | confirmed | CN 3.5.0 同伴保护位（?unit_id=&protected=，UserUnitDetail.cs:683-685） |
| POST | `/user_units/protect` | confirmed | CN 3.5.0 同伴保护位 POST 形态 |
| GET | `/user_units/protect/` | confirmed | CN 3.5.0 同伴保护位（?unit_id=&protected=，UserUnitDetail.cs:683-685） |
| POST | `/user_units/protect/` | confirmed | CN 3.5.0 同伴保护位 POST 形态 |
| GET | `/user_units/sell` | confirmed | CN 3.5.0 伙伴出售（?ids[]=，UnitSaleConfirmModal.cs:37） |
| POST | `/user_units/sell` | confirmed | CN 3.5.0 伙伴出售 POST 形态 |
| GET | `/user_units/sell/` | confirmed | CN 3.5.0 伙伴出售（?ids[]=，UnitSaleConfirmModal.cs:37） |
| POST | `/user_units/sell/` | confirmed | CN 3.5.0 伙伴出售 POST 形态 |
| GET | `/user_units/set_helper` | confirmed | CN 3.5.0 设助战（?id=，UserUnitDetail.cs:460-461） |
| POST | `/user_units/set_helper` | confirmed | CN 3.5.0 设助战 POST 形态 |
| GET | `/user_units/set_helper/` | confirmed | CN 3.5.0 设助战（?id=，UserUnitDetail.cs:460-461） |
| POST | `/user_units/set_helper/` | confirmed | CN 3.5.0 设助战 POST 形态 |
| GET | `/user_units/set_leader` | confirmed | CN 3.5.0 设队长（?id=，UserUnitDetail.cs:441-442） |
| POST | `/user_units/set_leader` | confirmed | CN 3.5.0 设队长 POST 形态 |
| GET | `/user_units/set_leader/` | confirmed | CN 3.5.0 设队长（?id=，UserUnitDetail.cs:441-442） |
| POST | `/user_units/set_leader/` | confirmed | CN 3.5.0 设队长 POST 形态 |
| GET | `/user_units/training` | confirmed | CN 3.5.0 同伴训练（事务内等级边界/扣点/只改 level；STAT350-05A） |
| POST | `/user_units/training` | confirmed | CN 3.5.0 同伴训练 POST 形态 |
| GET | `/user_units/training/` | confirmed | CN 3.5.0 同伴训练（事务内等级边界/扣点/只改 level；STAT350-05A） |
| POST | `/user_units/training/` | confirmed | CN 3.5.0 同伴训练 POST 形态 |

## 用户 · `users`（54 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/users/check_ap` | confirmed | CN 3.5.0 AP 回复弹窗读面（Messages.cs ApHealButton → NoApModal） |
| POST | `/users/check_ap` | confirmed | CN 3.5.0 AP 回复弹窗读面 POST 形态 |
| GET | `/users/check_ap/` | confirmed | CN 3.5.0 AP 回复弹窗读面（Messages.cs ApHealButton → NoApModal） |
| POST | `/users/check_ap/` | confirmed | CN 3.5.0 AP 回复弹窗读面 POST 形态 |
| GET | `/users/function_portal` | confirmed | requireData 空态读面(users_function_portal) |
| POST | `/users/function_portal` | confirmed | requireData 空态读面(users_function_portal) POST 形态 |
| GET | `/users/function_portal/` | confirmed | requireData 空态读面(users_function_portal) |
| POST | `/users/function_portal/` | confirmed | requireData 空态读面(users_function_portal) POST 形态 |
| GET | `/users/help` | confirmed | CN 3.5.0 玩家救援写面 |
| POST | `/users/help` | confirmed | CN 3.5.0 玩家救援写面 POST 形态 |
| GET | `/users/help/` | confirmed | CN 3.5.0 玩家救援写面 |
| POST | `/users/help/` | confirmed | CN 3.5.0 玩家救援写面 POST 形态 |
| GET | `/users/messages` | confirmed | CN 3.5.0 消息面（Top 轮询空类别字典） |
| POST | `/users/messages` | confirmed | CN 3.5.0 消息面 POST 形态 |
| GET | `/users/messages/` | confirmed | CN 3.5.0 消息面（Top 轮询空类别字典） |
| POST | `/users/messages/` | confirmed | CN 3.5.0 消息面 POST 形态 |
| GET | `/users/preset_data` | confirmed | CN 3.5.0 玩家快照无后缀别名（FateAstrolabePage/RedDiamondShop requireData） |
| POST | `/users/preset_data` | confirmed | CN 3.5.0 玩家快照无后缀别名 POST 形态 |
| GET | `/users/preset_data.json` | confirmed | CN 3.5.0 玩家快照（fixture+会话注入，tutorial_session 支持） |
| POST | `/users/preset_data.json` | confirmed | CN 3.5.0 玩家快照 POST 形态 |
| GET | `/users/preset_data.json/` | confirmed | CN 3.5.0 玩家快照（fixture+会话注入，tutorial_session 支持） |
| POST | `/users/preset_data.json/` | confirmed | CN 3.5.0 玩家快照 POST 形态 |
| GET | `/users/preset_data/` | confirmed | CN 3.5.0 玩家快照无后缀别名（FateAstrolabePage/RedDiamondShop requireData） |
| POST | `/users/preset_data/` | confirmed | CN 3.5.0 玩家快照无后缀别名 POST 形态 |
| GET | `/users/reload_data` | confirmed | CN 3.5.0 缓存刷新（on=gachas_execute：user/user_coin/user_units/gachas） |
| POST | `/users/reload_data` | confirmed | CN 3.5.0 缓存刷新 POST 形态 |
| GET | `/users/reload_data/` | confirmed | CN 3.5.0 缓存刷新（on=gachas_execute：user/user_coin/user_units/gachas） |
| POST | `/users/reload_data/` | confirmed | CN 3.5.0 缓存刷新 POST 形态 |
| GET | `/users/show` | confirmed | CN 3.5.0 玩家主页读面（UserPage requireData.user_temp） |
| POST | `/users/show` | confirmed | CN 3.5.0 玩家主页读面 POST 形态 |
| GET | `/users/show/` | confirmed | CN 3.5.0 玩家主页读面（UserPage requireData.user_temp） |
| POST | `/users/show/` | confirmed | CN 3.5.0 玩家主页读面 POST 形态 |
| GET | `/users/show_login_bonus` | confirmed | requireData 空态读面(users_show_login_bonus) |
| POST | `/users/show_login_bonus` | confirmed | requireData 空态读面(users_show_login_bonus) POST 形态 |
| GET | `/users/show_login_bonus/` | confirmed | requireData 空态读面(users_show_login_bonus) |
| POST | `/users/show_login_bonus/` | confirmed | requireData 空态读面(users_show_login_bonus) POST 形态 |
| POST | `/users/update` | confirmed | CN 3.5.0 教程写入（user_name/tutorial_finish，status:success 契约） |
| POST | `/users/update/` | confirmed | CN 3.5.0 教程写入（user_name/tutorial_finish，status:success 契约） |
| GET | `/users/update_memo` | confirmed | CN 3.5.0 玩家签名更新（UserPage.OnMemoChange） |
| POST | `/users/update_memo` | confirmed | CN 3.5.0 玩家签名更新 POST 形态 |
| GET | `/users/update_memo/` | confirmed | CN 3.5.0 玩家签名更新（UserPage.OnMemoChange） |
| POST | `/users/update_memo/` | confirmed | CN 3.5.0 玩家签名更新 POST 形态 |
| GET | `/users/user_coin` | confirmed | CN 3.5.0 钻石余额读面（Gacha/CoinShop requireData.user_coin） |
| POST | `/users/user_coin` | confirmed | CN 3.5.0 钻石余额读面 POST 形态 |
| GET | `/users/user_coin/` | confirmed | CN 3.5.0 钻石余额读面（Gacha/CoinShop requireData.user_coin） |
| POST | `/users/user_coin/` | confirmed | CN 3.5.0 钻石余额读面 POST 形态 |
| GET | `/users/user_memo/` | confirmed | requireData 空态读面(users_user_memo) |
| POST | `/users/user_memo/` | confirmed | requireData 空态读面(users_user_memo) POST 形态 |
| POST | `/users/verify_nickname` | confirmed | CN 3.5.0 昵称查重（{result:bool}） |
| POST | `/users/verify_nickname/` | confirmed | CN 3.5.0 昵称查重（{result:bool}） |
| GET | `/users/version_info` | confirmed | CN 3.5.0 版本握手（asset_info_version/app_conf_version 非零） |
| POST | `/users/version_info` | confirmed | CN 3.5.0 版本握手 POST 形态 |
| GET | `/users/version_info/` | confirmed | CN 3.5.0 版本握手（asset_info_version/app_conf_version 非零） |
| POST | `/users/version_info/` | confirmed | CN 3.5.0 版本握手 POST 形态 |

## 公会候补 · `waiting_users`（8 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/waiting_users` | confirmed | requireData 空态读面(waiting_users) |
| POST | `/waiting_users` | confirmed | requireData 空态读面(waiting_users) POST 形态 |
| GET | `/waiting_users/` | confirmed | requireData 空态读面(waiting_users) |
| POST | `/waiting_users/` | confirmed | requireData 空态读面(waiting_users) POST 形态 |
| GET | `/waiting_users/recruit` | confirmed | requireData 空态读面(waiting_users_recruit) |
| POST | `/waiting_users/recruit` | confirmed | requireData 空态读面(waiting_users_recruit) POST 形态 |
| GET | `/waiting_users/recruit/` | confirmed | requireData 空态读面(waiting_users_recruit) |
| POST | `/waiting_users/recruit/` | confirmed | requireData 空态读面(waiting_users_recruit) POST 形态 |

## 运维端点 · `mercd`（5 条）

| 方法 | 路径 | 状态 | 说明 |
|---|---|---|---|
| GET | `/mercd/api` | confirmed | 重定向到 Huma 接口文档 |
| GET | `/mercd/api/{filename}` | confirmed | Huma OpenAPI 与内嵌 Swagger UI |
| GET | `/mercd/health` | confirmed | CN3.5 服务健康检查 |
| GET | `/mercd/routes` | confirmed | 导出 CN3.5 路由及响应状态 |
| GET | `/mercd/unknown-endpoints` | confirmed | 聚合 501 未实现请求 backlog（按命中次数降序） |

