# Unity 控件映射表（M5 扩展）

本表与 `selectors.candidate.json`、`m5_selectors.candidate.json` 的语义别名对应。现有公共/公会/核心操作映射仍在原候选文件中，本表不改变它们的验证状态。本次未连接设备，以下 42 项均未获得真实 Unity 控件 ID，不能作为已校准选择器使用。

## 新控件回填规范

Poco inspect 或运行失败采集发现新控件 ID/name/稳定父子路径时，必须在本表补齐对应行，并同步候选映射与本地 `selectors.m5.local.json`。不得只在临时脚本中硬编码。若发现表外控件，新增语义别名和行，并关联用例。

每项记录：页面、用途、实际 name/ID、稳定父子路径/属性、版本适用条件、已安装 APK SHA-256、树 SHA-256、本地证据索引、校准时间、关联用例、状态。实际路径采用 Runner 支持的 `attrs` 与 `offspring` 结构；同名控件必须加父级约束并验证唯一可见。动态 instanceID 仅留作观测，不保证跨启动稳定。

公开表只保存脱敏结构和哈希/相对证据标识；账号、设备号、完整树与截图留在 output 下。本地 `calibration` 记录由 calibrate 命令生成，不能手写伪造。稳定映射的更新与新增 ID 回填是 T11/T13 的完成条件。失效 ID 保留版本与替代说明，不静默覆盖。

## M5 待校准项

| 语义别名 | 页面/用途分组 | 实际 Unity ID/name 与父级路径 | APK/树哈希、证据与日期 | 状态 |
|---|---|---|---|---|
| `m5.ability.preview` | `ability` | 待实机观测 | 待采集 | unobserved |
| `m5.ability.submit` | `ability` | 待实机观测 | 待采集 | unobserved |
| `m5.ability.target` | `ability` | 待实机观测 | 待采集 | unobserved |
| `m5.battle.finish` | `battle` | 待实机观测 | 待采集 | unobserved |
| `m5.battle.guts` | `battle` | 待实机观测 | 待采集 | unobserved |
| `m5.battle.hud` | `battle` | 待实机观测 | 待采集 | unobserved |
| `m5.battle.pause` | `battle` | 待实机观测 | 待采集 | unobserved |
| `m5.battle.retreat` | `battle` | 待实机观测 | 待采集 | unobserved |
| `m5.battle.seed` | `battle` | 待实机观测 | 待采集 | unobserved |
| `m5.battle.sp` | `battle` | 待实机观测 | 待采集 | unobserved |
| `m5.battle.unit` | `battle` | 待实机观测 | 待采集 | unobserved |
| `m5.battle.win` | `battle` | 待实机观测 | 待采集 | unobserved |
| `m5.evolve.material` | `evolve` | 待实机观测 | 待采集 | unobserved |
| `m5.evolve.preview` | `evolve` | 待实机观测 | 待采集 | unobserved |
| `m5.evolve.submit` | `evolve` | 待实机观测 | 待采集 | unobserved |
| `m5.modal.error` | `modal` | 待实机观测 | 待采集 | unobserved |
| `m5.monster.evolve` | `monster` | 待实机观测 | 待采集 | unobserved |
| `m5.monster.list` | `monster` | 待实机观测 | 待采集 | unobserved |
| `m5.monster.panel` | `monster` | 待实机观测 | 待采集 | unobserved |
| `m5.monster.scale_limit` | `monster` | 待实机观测 | 待采集 | unobserved |
| `m5.monster.sell` | `monster` | 待实机观测 | 待采集 | unobserved |
| `m5.monster.sell_target` | `monster` | 待实机观测 | 待采集 | unobserved |
| `m5.quest.entry` | `quest` | 待实机观测 | 待采集 | unobserved |
| `m5.quest.result` | `quest` | 待实机观测 | 待采集 | unobserved |
| `m5.quest.start` | `quest` | 待实机观测 | 待采集 | unobserved |
| `m5.quest.target` | `quest` | 待实机观测 | 待采集 | unobserved |
| `m5.rune.off` | `rune` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.ability` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.ability_value` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.at` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.hp` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.list` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.panel` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.rune_slot` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.sell` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.sell_target` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.upgrade` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.unit.upgrade_level` | `unit` | 待实机观测 | 待采集 | unobserved |
| `m5.upgrade.cost` | `upgrade` | 待实机观测 | 待采集 | unobserved |
| `m5.upgrade.preview` | `upgrade` | 待实机观测 | 待采集 | unobserved |
| `m5.upgrade.submit` | `upgrade` | 待实机观测 | 待采集 | unobserved |
| `m5.upgrade.unaffordable_target` | `upgrade` | 待实机观测 | 待采集 | unobserved |

复用别名（例如 `nav.menu`、`unit.row.primary`、`modal.yes`）也必须针对本次 APK 和测试目标重新核验；`unit.row.primary` 应绑定选定测试同伴，不能简单取列表第一行。候选 JSON 中 `${..._name}` 是变量占位，不是控件 ID。首次校准时应替换为真实稳定属性并补上必要父级，避免把真实账号显示文本固化进公开文件。

关联计划：[M5 验收](../../plans/cn35-m5-2026-11-08/ACCEPTANCE.md)。

## M6 待校准项（公会设施与暴走魔宠 Raid）

以下 55 项对应 `m6_raid_selectors.candidate.json`，尚未连接设备采集真实 Unity 控件树，均处于 `unobserved` 状态，不能作为已校准选择器使用：

| 语义别名 | 页面/用途分组 | 已知客户端路径提示（待实机确认） | 状态 |
|---|---|---|---|
| `m6.battle.guts` | `battle` | 战斗界面救援/士气文本 | unobserved |
| `m6.battle.hud` | `battle` | 战斗 HUD 根节点 | unobserved |
| `m6.battle.sp` | `battle` | 战斗左下角 `PowerupButton` SP 数值 | unobserved |
| `m6.battle.win` | `battle` | 通关结算推进控件 | unobserved |
| `m6.guild.facility_at_value` | `guild` | `GuildEquipment` 训练所攻击加成文本 | unobserved |
| `m6.guild.facility_entry` | `guild` | 公会主页「公会设施」入口按钮 | unobserved |
| `m6.guild.facility_hp_value` | `guild` | `GuildEquipment` 医疗所体力加成文本 | unobserved |
| `m6.guild.facility_next_page` | `guild` | `GuildEquipment` 右侧翻页箭头 | unobserved |
| `m6.guild.facility_panel` | `guild` | `GuildEquipmentListRows` 容器 | unobserved |
| `m6.guild.facility_sp_value` | `guild` | `GuildEquipment` 祭坛初始 SP 文本 | unobserved |
| `m6.guild.facility_storage_value` | `guild` | `GuildEquipment` 蓄水库上限与比例文本 | unobserved |
| `m6.guild.facility_toughness_value` | `guild` | `GuildEquipment` 食堂体力与 Guts 文本 | unobserved |
| `m6.home.activity_btn` | `home` | 旅馆 `Home` 左上角 `IconActivityInfo` 或轮播区域 | unobserved |
| `m6.home.activity_pt_row` | `home` | 活动列表/轮播 `banner_raid_pt` 条目 | unobserved |
| `m6.home.activity_raid_row` | `home` | 活动列表/轮播 `banner_raid` 条目 | unobserved |
| `m6.quest.entry` | `quest` | 底部导航「探险」入口 | unobserved |
| `m6.quest.start` | `quest` | 日常关卡出击按钮 | unobserved |
| `m6.quest.target` | `quest` | 日常测试关卡目标行 | unobserved |
| `m6.raid.appear_attack_btn` | `raid_modal` | `HUD0/RaidAppearModal` 右键「前往击退」 | unobserved |
| `m6.raid.appear_giveup_btn` | `raid_modal` | `HUD0/RaidAppearModal` 左键「放走」 | unobserved |
| `m6.raid.appear_modal` | `raid_modal` | `HUD0/RaidAppearModal` 暴走强袭弹窗 | unobserved |
| `m6.raid.attack_btn` | `raid` | `L5/MyRaid/Has/ButtonAttack` 前往击退 | unobserved |
| `m6.raid.battle_pause` | `raid_battle` | 暴走战斗暂停按钮 | unobserved |
| `m6.raid.battle_retreat` | `raid_battle` | 暂停菜单撤退按钮（触发 `/raid_monsters/lose`） | unobserved |
| `m6.raid.boss_gauge` | `raid_battle` | 战斗顶栏 `QuestLimitBase/Gauge` Boss 血条 | unobserved |
| `m6.raid.diff_hard` | `raid_select` | `RaidQuestSelect` 强击（200s / 60BP） | unobserved |
| `m6.raid.diff_normal` | `raid_select` | `RaidQuestSelect` 急击（160s / 30BP） | unobserved |
| `m6.raid.diff_very_hard` | `raid_select` | `RaidQuestSelect` 猛击（240s / 100BP） | unobserved |
| `m6.raid.giveup_btn` | `raid` | `L5/MyRaid/Has/ButtonGiveUp` 放走 | unobserved |
| `m6.raid.go_battle_btn` | `raid_select` | `RaidQuestSelect` 右下角「前往击退！」 | unobserved |
| `m6.raid.help_call_btn` | `raid` | `L5/MyRaid/Has/ButtonHelpCall` 协力邀请 | unobserved |
| `m6.raid.help_community_btn` | `raid_modal` | `HUD0/RaidHelpModal/Panel/ButtonPanel/CommunityButton` 聊天室协力请求（双边求援完毕才自动关闭弹窗） | unobserved |
| `m6.raid.help_guild_btn` | `raid_modal` | `HUD0/RaidHelpModal/Panel/ButtonPanel/GuildButton` 公会协力请求（`RaidHelpModal.cs`） | unobserved |
| `m6.raid.help_modal` | `raid_modal` | `HUD0/RaidHelpModal` 协力邀请弹窗 | unobserved |
| `m6.raid.history_first_row` | `raid` | 击退日志 Tab 列表首行 `RaidRow` | unobserved |
| `m6.raid.initial_sp_label` | `raid_select` | `RaidQuestSelect` 保底 SP 文本 | unobserved |
| `m6.raid.lv_bonus_entry` | `raid_bonus` | `RaidEventPage` 「查看击退奖励」按钮 `lv_button`（跳转 `RaidLvBonusPage` 唯一入口） | unobserved |
| `m6.raid.lv_bonus_panel` | `raid_bonus` | `RaidLvBonusPage` 奖励列表容器 | unobserved |
| `m6.raid.lv_text` | `raid` | `L5/Condition/Lv` 击退 Lv | unobserved |
| `m6.raid.members_btn` | `raid` | `L5/MyRaid/Has/Buttons/ButtonMembers` 「协力者」排行入口按钮 | unobserved |
| `m6.raid.members_panel` | `raid_members` | `RaidMembers` 协力者伤害排行页面 | unobserved |
| `m6.raid.my_monster_hp` | `raid` | `L5/MyRaid/Has/Point` 当前魔宠剩余 HP | unobserved |
| `m6.raid.my_monster_panel` | `raid` | `L5/MyRaid/Has` 当前遭遇魔宠面板 | unobserved |
| `m6.raid.page` | `raid` | `Common/Pages/Raid` 暴走主页容器 | unobserved |
| `m6.raid.pt_exchange_buy_btn` | `raid_exchange` | `ShowRaidPtExchange2` 首个可兑换商品槽 | unobserved |
| `m6.raid.pt_exchange_panel` | `raid_exchange` | `ShowRaidPtExchange2` 击退 pt 交换所容器 | unobserved |
| `m6.raid.pt_text` | `raid` | `L5/Condition/Pt` 击退 pt | unobserved |
| `m6.activity.raid_info_entry` | `activity` | 活动页横幅 `banner_raid_info`（跳转 `RaidEventPage`，`placeholder`） | unobserved |
| `nav.back` | `nav` | 通用栈页返回按钮（`RaidMembers` 等，`placeholder`） | unobserved |
| `m6.raid.reload_btn` | `raid` | `L6/ButtonReload` 更新信息按钮 | unobserved |
| `m6.raid.select_page` | `raid_select` | `RaidQuestSelect` 出击准备页容器 | unobserved |
| `m6.raid.tab_community` | `raid` | `L6/Tab/TabButton2` 聊天室协力请求 | unobserved |
| `m6.raid.tab_guild` | `raid` | `L6/Tab/TabButton1` 公会协力请求 | unobserved |
| `m6.raid.tab_history` | `raid` | `L6/Tab/TabButton3` 魔宠击退日志 | unobserved |
| `m6.raid.weapon_rate_box` | `raid_select` | `RaidQuestSelect` 武器种类效果倍率区 | unobserved |

关联计划：[M6 验收](../../plans/cn35-m6-raid-2026-11-13/ACCEPTANCE.md)。
