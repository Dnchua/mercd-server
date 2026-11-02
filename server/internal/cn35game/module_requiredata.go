// module_requiredata.go UI-WALKTHROUGH-20260927：主模块入口 requireData 补全。
//
// 背景：真实 app_conf（v201710121007）的页面 requireData 里仍有一批
// 未注册 path——客户端 PageBase.WaitForAllDataLoaded（PageBase.cs:782-800）
// 对任一依赖失败即 ShowErrorModal + CancelShowPage，整页不可进入。
// 2026-09-28 实机（Meizu 16th / 主栈 18091）证据：StageSelect 三件套
// `/events/`、`/trials/`、`/team_quest/get_team_areas` 501；UserUnits
// 依赖 `/albums/` 501；缺口与「12-CN3.5页面交互信息台账」§4 静态判定一致。
//
// 证据等级（同 requiredata.go 头部约定）：
//   - 「空集安全」依赖客户端显式空态分支：列表键经 cache.Get(key).docList
//     消费，空数组 → 零行渲染（DataCache.cs:35-42、Document.cs:180-187）。
//   - 内容语义：本期无生产数据来源的域（活动日程/试炼/协力/图鉴领取态/
//     道具/符文/章节）返回**结构完整空态**，不声明原服字段语义；账号
//     确有对应库存记录时（user_items/user_skills 无表无种子）空集即真实。
//   - /users/user_coin 为真实读面：复用 AccountStore.Snapshot 的同一
//     UserCoin 视图（与 preset_data.json 的 data.user_coin 同源）。
//   - /users/preset_data 为 preset_data.json 的无后缀别名（台账 §3 记录的
//     裸路径缺口：客户端 DataLoad 会用配置原样串请求）。
package cn35game

import (
	"fmt"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// userCoinHandler /users/user_coin：账号钻石余额独立读面。
//
// 消费证据：GachaIndexParts.cs:151/421 `cache.Get("user_coin").i("amount")`
// 读 diamond 余额；coin 行 ↔ data.user_coin.amount（gacha_rules.go 货币
// 口径注释）。响应与 preset_data 的 data.user_coin 同源，防止缓存过期
// （expire/force_update）时余额读取分叉。
func userCoinHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		snap, err := dep.Accounts.Snapshot(req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 /users/user_coin 快照失败: %w", err)
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "user_coin",
			"amount": snap.UserCoin["amount"],
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"user_coin": snap.UserCoin},
		})
	})
}

// emptyRequireDataHandler 生成「结构完整空态」读面：响应
// {"data":{<key>:[]}}（对象键给 {}）。cache 键名取客户端 requireData
// 声明键（响应 data 段逐键入缓存，DataCache.UpdateDataByResponse
// DataCache.cs:397-404）；数组/对象形态按消费方 docList/doc() 判定。
func emptyRequireDataHandler(dep Deps, endpoint string, arrayKeys, objectKeys []string) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		data := map[string]any{}
		for _, k := range arrayKeys {
			data[k] = []any{}
		}
		for _, k := range objectKeys {
			data[k] = map[string]any{}
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": endpoint,
			"mode": "empty_state", "schema": "unverified",
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// registerModuleRequireData 登记主模块入口缺口的读面端点（GET/POST 双形态）。
// 每条注释标注：页面（requireData 键 → 消费证据/空态理由）。
func registerModuleRequireData(register func(method, path, note string, handler httpapi.Handler), dep Deps) {
	reg := func(path, note, endpoint string, arrayKeys, objectKeys []string) {
		register("GET", path, note, emptyRequireDataHandler(dep, endpoint, arrayKeys, objectKeys))
		register("POST", path, note+" POST 形态", emptyRequireDataHandler(dep, endpoint, arrayKeys, objectKeys))
	}

	// ---- 探险（StageSelect）----
	// requireData：stages(/stages/ 已有)、events、trials、specials、
	// user(/users/messages 已有)。列表行消费经 docList，空数组=无在办活动/
	// 试炼/协力区域——三域均无生产数据，空集为合法语义。
	reg("/events/", "CN 3.5.0 历次活动列表（StageSelect/Events requireData.events；空态）",
		"events", []string{"events"}, nil)
	reg("/trials/", "CN 3.5.0 试炼列表（StageSelect requireData.trials；空态）",
		"trials", []string{"trials"}, nil)
	reg("/team_quest/get_team_areas", "CN 3.5.0 协力区域（StageSelect requireData.specials；空态）",
		"team_quest_get_team_areas", []string{"specials"}, nil)
	// 探险页内跳转：ReprintMiniEventsSelect（复刻活动选择）与 Trials 域子页。
	reg("/quests/reprint_events/", "CN 3.5.0 复刻迷你活动列表（ReprintMiniEventsSelect requireData.reprint_events；空态）",
		"quests_reprint_events", []string{"reprint_events"}, nil)
	reg("/trials/daily/", "CN 3.5.0 日替试炼关卡（TrialsQuestSelect requireData.trials_quests；空态）",
		"trials_daily", []string{"trials_quests"}, nil)
	reg("/trials/clear_count_bonus", "CN 3.5.0 试炼通关次数奖励（TrialsRewardPage requireData.trials_bonus；空态）",
		"trials_clear_count_bonus", []string{"trials_bonus"}, nil)

	// ---- 同伴/编队簇（Menu→UserUnits/UserMonsters/UserSkills/UserDecks/
	// UserAlbum/UserMonsterAlbum，Home.DeckBtn→UserDecks）----
	// albums/monster_albums 为图鉴领取态（FindByKey(id) 消费，AlbumUnitDetail.cs:136
	// /AlbumMonsterDetail.cs:42）；领取写面 /albums/wait|back 不在本批开放。
	// ALBUM350-01：装配 Inventory+Catalog 时 /albums/ 走真实图鉴读面
	// （cn35game.go 注册，album_wire.go：全目录 + hide=未持有标记 +
	// album_detail_message/album_unit 详情路径），此处不再占空态。
	if dep.Inventory == nil || dep.Catalog == nil {
		reg("/albums/", "CN 3.5.0 同伴图鉴态（UserUnits/UserAlbum requireData.albums；空态）",
			"albums", []string{"albums"}, nil)
		reg("/monster_albums/", "CN 3.5.0 魔宠图鉴态（UserMonsterAlbum requireData.monster_albums；空态）",
			"monster_albums", []string{"monster_albums"}, nil)
	}
	// 道具/章节：账号侧无对应库表与种子，空集=账号真值。
	// RUNE350-01：装配 Skills 时 /user_skills/ 走真实库存投影
	// （cn35game.go 注册），此处不再占空态。
	if dep.Skills == nil {
		reg("/user_skills/", "CN 3.5.0 符文列表（UserSkills/UserSkillsWithUnit requireData.user_skills；空态）",
			"user_skills", []string{"user_skills"}, nil)
	}
	// ITEM350-02：装配 Items 时 /user_items/ 走真实库存投影（cn35game.go
	// 注册），此处不再占空态（重复注册返回 false，必须以装配判定跳过）。
	if dep.Items == nil {
		reg("/user_items/", "CN 3.5.0 道具列表（UserItems.user_items / UserItemsV2.user_items_hash；空态）",
			"user_items", []string{"user_items", "user_items_hash"}, nil)
	}
	// /chapters/ 已由故事阅读实现真实读面（cn35game.go 故事块；设计
	// notes/proposed/2026-09-28-cn35-story-reader.md），不在此空态表。
	// /chapters/list 服务 ≥210 路径（game_server_version≥210 时 Menu 走
	// ChaptersAndStories；当前 preset 为 0 走旧路径 Chapters）——保持空态。
	reg("/chapters/list", "CN 3.5.0 章节+故事目录（ChaptersAndStories ≥210 路径 requireData.chapters/stories；空态）",
		"chapters_list", []string{"chapters", "stories"}, nil)

	// ---- 抽卡（FooterParts→Gacha、Menu 活动横幅 partial）----
	// country_pick_page 为对象键（choose_attr_page 先例同形下发）。
	reg("/gachas/choose_country_only_page/", "CN 3.5.0 属国单抽选页（Menu/PickupScoutPage requireData.country_pick_page；空态）",
		"gachas_choose_country_only_page", nil, []string{"country_pick_page"})
	// 票券列表（UserItemsV2 副键；本期无票券域 → 空）。
	reg("/gachas/ticket_available_list", "CN 3.5.0 可用票券（UserItemsV2 requireData.ticket_available_list；空态）",
		"gachas_ticket_available_list", []string{"ticket_available_list"}, nil)

	// ---- UI-WALKTHROUGH-20260927 §二批：剩余 requireData 缺口全覆盖 ----
	// 实机证据：2026-09-28 魔宠列表因 /user_monsters/evolve_select_info
	// 501 → ServerErrorModal。下表由 app_conf(v201710121007) 全页
	// requireData 声明自动归集（path→声明 data 键），逐条注册结构完整
	// 空态读面，解除「点击即报错」的入口级阻断。
	//
	// 边界：
	//   - 所有键一律按空态下发——列表键给 []、对象键给 {}（docList/doc
	//     消费形态对等空集）；语义为「无记录/无配置」，不声明原服字段。
	//   - /user_units/、/user_monsters/、/gachas/、/user_decks/ 等为
	//     既有真实读面（走持久库存），不在此表。
	//   - 写面（presents/receive、albums/wait、quests/result 等）不在
	//     requireData 面内，不开放。
	//   - /help.json.encode 为静态文件路径（static_file 加载通道），
	//     与游戏接口加密通道不同，未注册——帮助页进入仍为已知缺口。
	for _, e := range requireDataSweepTable {
		// ITEM350-07：装配 Medal+Items 时 /medal_items/ 走真实商品读面。
		if e.path == "/medal_items/" && dep.Medal != nil && dep.Items != nil {
			continue
		}
		// LOGIN350-01/02：装配 LoginBonus 时 /users/show_login_bonus 走真实读面
		// （data.user + 泉水待领量 show_login_bonus），不再下发空 login_bonus 对象。
		if dep.LoginBonus != nil && e.path == "/users/show_login_bonus" {
			continue
		}
		// LOGIN350-02：装配 Signin 时 /accu_login_activity/ 走真实读面
		// （data.acc_login_rewards 为 28 行数组）。必须在这里 skip：本 sweep 在
		// Register 早段执行，重复注册会被 HandleWithStatus 静默丢弃。
		if dep.Signin != nil && e.path == "/accu_login_activity/" {
			continue
		}
		// SHOP350-01：装配 DailyShop 时 /red_diamond_shop/ 走真实商品读面，
		// /users/function_portal 走主人公日常入口真实读面。
		if dep.DailyShop != nil &&
			(e.path == "/red_diamond_shop/" || e.path == "/users/function_portal") {
			continue
		}
		if e.path == "/user_monsters/evolve_select_info" {
			continue
		}
		// GUILD350：装配 Guild 时公会域/排名/waiting_users 走真实端点。
		// GC-08：/battles/comments 也归公会社交面（guild_social_wire.go），
		// 这里必须让位——本 sweep 早于 registerGuildRoutes 执行，重复注册会
		// 把真实处理器静默丢弃，只剩 battle_comments=[] 的错类型空态。
		if dep.Guild != nil {
			switch e.path {
			case "/guilds/", "/guilds/deck", "/guilds/monsters",
				"/guilds/search_index", "/guilds/subscriptions",
				"/guild_rankings",
				// GC-06：两张赛季奖励静态表由 guild_wire.go 的
				// guildRankingsItemRewardsHandler / guildRankingsSkillRewardsHandler
				// 从 assets/cn350 读真实配置下发。本 sweep 早于 registerGuildRoutes，
				// 不跳过就会把 8 档 / 250 行的真实读面静默覆盖成 [] 空态。
				"/guild_rankings/item_rewards", "/guild_rankings/skill_rewards",
				"/waiting_users", "/waiting_users/recruit",
				"/battles/comments":
				continue
			}
		}
		ak, ok := splitRequireDataKeys(e.keys)
		register("GET", e.path, "requireData 空态读面("+e.endpoint+")", emptyRequireDataHandler(dep, e.endpoint, ak, ok))
		register("POST", e.path, "requireData 空态读面("+e.endpoint+") POST 形态", emptyRequireDataHandler(dep, e.endpoint, ak, ok))
	}
}

// requireDataSweepEntry 一条待覆盖的 requireData 缺口。
type requireDataSweepEntry struct {
	path     string
	endpoint string
	keys     []string
}

// requireDataSweepTable 由 serve_app_conf_android.json 全页 requireData
// 归集（剔除已注册真实端点/条件注册端点/静态文件路径）。
var requireDataSweepTable = []requireDataSweepEntry{
	{"/accu_login_activity/", "accu_login_activity", []string{"acc_login_rewards"}},
	{"/activity/accu_login_activity_v30/", "activity_accu_login_activity_v30", []string{"accu_login_activity_v30_rewards"}},
	{"/activity/march_charge_activity/", "activity_march_charge_activity", []string{"march_charge_activity"}},
	{"/activity/march_charge_activity/show_chest", "activity_march_charge_activity_show_chest", []string{"march_charge_chest_info", "march_charge_chest_list1", "march_charge_chest_list2"}},
	{"/anniversary_gacha/blessing_box_info", "anniversary_gacha_blessing_box_info", []string{"anniversary_gacha_blessing_box_info"}},
	{"/anniversary_gacha/main_info", "anniversary_gacha_main_info", []string{"anniversary_gacha_main_info"}},
	{"/areas/event", "areas_event", []string{"areas_100021", "areas_1041", "user_event_condition_100020", "user_event_condition_100021", "user_event_condition_100022", "user_event_condition_41"}},
	{"/battles/comments", "battles_comments", []string{"battle_comments"}},
	{"/cakes/toto_paiqiu_log/", "cakes_toto_paiqiu_log", []string{"beachVolleyball"}},
	{"/charge_act2/index", "charge_act2_index", []string{"charge2_act", "charge2_act_list"}},
	{"/chest/show_chest_rules", "chest_show_chest_rules", []string{"chest_rules"}},
	{"/chest/show_page", "chest_show_page", []string{"chest_infos", "chest_paper_list"}},
	{"/chest/show_paper_list", "chest_show_paper_list", []string{"paper_meta_list"}},
	{"/coins/", "coins", []string{"coins"}},
	{"/collection/", "collection", []string{"collection_data"}},
	{"/collection/grab/", "collection_grab", []string{"collection_choice_data"}},
	{"/collection/monsters", "collection_monsters", []string{"collection_deck"}},
	{"/collection/rank", "collection_rank", []string{"collection_rank"}},
	{"/collection/rewards", "collection_rewards", []string{"collection_reward_data"}},
	{"/college/", "college", []string{"collegeSelectData"}},
	{"/college/get_apply_for_positional_titles_info", "college_get_apply_for_positional_titles_info", []string{"applyForMorePositionalData", "applyformore_positional_list"}},
	{"/college/get_apply_for_student_condition", "college_get_apply_for_student_condition", []string{"applyStudentConditionData"}},
	{"/college/get_apply_for_student_list", "college_get_apply_for_student_list", []string{"applyForStudentsList"}},
	{"/college/get_daily_tasks", "college_get_daily_tasks", []string{"college_get_daily_tasks_data", "college_get_daily_tasks_tasks"}},
	{"/college/get_student_detail", "college_get_student_detail", []string{"studentDetailInfo", "student_detail_task_list"}},
	{"/college/get_students_info", "college_get_students_info", []string{"classMates_list"}},
	{"/college/get_study_info", "college_get_study_info", []string{"studyInfoData", "studyInfo_list"}},
	{"/college/get_teacher_info", "college_get_teacher_info", []string{"teacherInfoData", "teachers_students"}},
	{"/college/get_week_sign_info", "college_get_week_sign_info", []string{"college_get_week_sign_info"}},
	{"/college/search_teacher", "college_search_teacher", []string{"search_teachers_list"}},
	{"/college/task_detail", "college_task_detail", []string{"taskDetailData", "taskDetailData_progress_rewards"}},
	{"/community_topics/", "community_topics", []string{"community_topics"}},
	{"/consume_act2/index", "consume_act2_index", []string{"consume2_act", "consume2_act_list"}},
	{"/consume_packages/", "consume_packages", []string{"consumer_package"}},
	{"/daily_payment", "daily_payment", []string{"diamond_on_sale"}},
	{"/dungeon_rank/show_rank", "dungeon_rank_show_rank", []string{"ranking_list"}},
	{"/events/ranking", "events_ranking", []string{"event_ranking_paging", "event_ranking_temp"}},
	{"/events/show", "events_show", []string{"event_1"}},
	{"/exchange_activity/show", "exchange_activity_show", []string{"exchange_activity_list"}},
	{"/extra_quest_rankings/past_quests_list", "extra_quest_rankings_past_quests_list", []string{"guild"}},
	{"/extra_quest_rankings/rewards", "extra_quest_rankings_rewards", []string{"extra_quest_rankings_reward_type", "extra_quest_rankings_rewards"}},
	{"/extra_quest_rankings/yyyymms", "extra_quest_rankings_yyyymms", []string{"extra_quest_rankings_yyyymms"}},
	{"/fate/fate_buff_summary", "fate_fate_buff_summary", []string{"fate_buff_summary"}},
	{"/fate/show", "fate_show", []string{"fate"}},
	{"/first_charge/info", "first_charge_info", []string{"first_charge"}},
	{"/first_store_activity/show", "first_store_activity_show", []string{"first_store_page"}},
	{"/friends/friend_apply_list", "friends_friend_apply_list", []string{"friends_apply"}},
	{"/friends/index", "friends_index", []string{"friends"}},
	{"/friends/show_friend_medal_list", "friends_show_friend_medal_list", []string{"friend_medal_gifts"}},
	{"/friends_activities/index", "friends_activities_index", []string{"friends_activities"}},
	{"/friends_activities/progress", "friends_activities_progress", []string{"friends_activities"}},
	{"/gachas/attr_gacha", "gachas_attr_gacha", []string{"pickup_gacha"}},
	{"/gachas/available_unit_list", "gachas_available_unit_list", []string{"available_5"}},
	{"/gachas/dia_only_gacha/", "gachas_dia_only_gacha", []string{"dia_only_gacha"}},
	{"/gachas/discount_gacha/", "gachas_discount_gacha", []string{"discount_gacha"}},
	{"/gachas/pickup_gacha/", "gachas_pickup_gacha", []string{"pickup_gacha"}},
	{"/gachas/show/", "gachas_show", []string{"box_gacha", "boys_gacha", "girls_gacha"}},
	{"/gachas/show_choice_list", "gachas_show_choice_list", []string{"choice_unit_list_data"}},
	{"/gachas/weapon_gacha/", "gachas_weapon_gacha", []string{"weapon_gacha"}},
	{"/global_rewards/index", "global_rewards_index", []string{"global_rewards"}},
	{"/global_rewards/reward_list", "global_rewards_reward_list", []string{"global_reward_list"}},
	{"/guild_rankings", "guild_rankings", []string{"guild_rankings"}},
	{"/guild_rankings/item_rewards", "guild_rankings_item_rewards", []string{"guild_rankings_item_rewards"}},
	{"/guild_rankings/skill_rewards", "guild_rankings_skill_rewards", []string{"guild_rankings_skill_rewards_level", "guild_rankings_skill_rewards_type"}},
	{"/guilds/", "guilds", []string{"battle_condition", "guild"}},
	{"/guilds/deck", "guilds_deck", []string{"guild_deck"}},
	{"/guilds/monsters", "guilds_monsters", []string{"guild_monsters"}},
	{"/guilds/search_index", "guilds_search_index", []string{"search_index"}},
	{"/guilds/subscriptions", "guilds_subscriptions", []string{"guild_subscriptions"}},
	{"/guilds/temporary_equipment_index", "guilds_temporary_equipment_index", []string{"temporary_equipments"}},
	{"/invite_user_to_boys/", "invite_user_to_boys", []string{"invite_user_to_boys"}},
	{"/ladders/get_candidates", "ladders_get_candidates", []string{"candidates"}},
	{"/ladders/monster_grow_index", "ladders_monster_grow_index", []string{"monster_grow"}},
	{"/ladders/monsters", "ladders_monsters", []string{"ladder_deck"}},
	{"/ladders/show_ladder_info", "ladders_show_ladder_info", []string{"ladder_main_page"}},
	{"/ladders/show_rank", "ladders_show_rank", []string{"ladder_ranking_list"}},
	{"/main_chara_buff/infos", "main_chara_buff_infos", []string{"chara_buff_infos"}},
	{"/main_chara_buff/upgrade_infos", "main_chara_buff_upgrade_infos", []string{"chara_buff_upgrade_infos"}},
	{"/medal_items/", "medal_items", []string{"medal_items"}},
	{"/medal_items/team_shop_index", "medal_items_team_shop_index", []string{"team_quest_shop"}},
	{"/month_card/month_card_daily_rewards", "month_card_month_card_daily_rewards", []string{"month_card_daily_rewards"}},
	{"/month_card/month_card_gacha_items", "month_card_month_card_gacha_items", []string{"month_card_gacha_items"}},
	{"/month_card/show", "month_card_show", []string{"month_card_content", "month_card_detail"}},
	{"/month_card/show_gacha_page", "month_card_show_gacha_page", []string{"month_card_gacha_ticket_cnt"}},
	{"/new_year2016_happy_gacha/", "new_year2016_happy_gacha", []string{"new_year_2016_casino"}},
	{"/new_year2016_happy_gacha/currency/", "new_year2016_happy_gacha_currency", []string{"new_year_2016_currency"}},
	{"/new_year2016_shop/", "new_year2016_shop", []string{"new_year2016_shop"}},
	{"/passive_skill/", "passive_skill", []string{"max_skill_count", "user_passive_skills"}},
	{"/passive_skill/available_passive_skill_list", "passive_skill_available_passive_skill_list", []string{"passive_skill_available_1", "passive_skill_available_2"}},
	{"/quests/daily", "quests_daily", []string{"daily_quests"}},
	{"/quests/event", "quests_event", []string{"mini_eventquests_20170810", "mini_eventquests_20170810_bonus_hard", "mini_eventquests_20170810_bonus_normal", "mini_eventquests_20170810_bonus_very_hard", "mini_eventquests_20170810_condition"}},
	{"/quests/event_available_unit", "quests_event_available_unit", []string{"mini_eventquests_20170810_unit_hard", "mini_eventquests_20170810_unit_normal", "mini_eventquests_20170810_unit_very_hard"}},
	{"/raid_monsters/", "raid_monsters", []string{"raid_condition", "raid_monsters"}},
	{"/raid_monsters/lv_bonus", "raid_monsters_lv_bonus", []string{"raid_lv_bonus"}},
	{"/raid_monsters/pt_bonus", "raid_monsters_pt_bonus", []string{"raid_pt_bonus"}},
	{"/recall_users/", "recall_users", []string{"recall_user_data"}},
	{"/red_diamond_shop/", "red_diamond_shop", []string{"red_diamond_shop"}},
	{"/rob_battles", "rob_battles", []string{"rob_battle_ready"}},
	{"/rob_battles/get_candidates", "rob_battles_get_candidates", []string{"rob_candidates"}},
	{"/rob_battles/show_rob_battles_info", "rob_battles_show_rob_battles_info", []string{"rob_battle_enter", "rob_battle_record"}},
	{"/team_quest/get_team_quests", "team_quest_get_team_quests", []string{"team_area_info", "team_quests"}},
	{"/team_quest/get_team_rooms", "team_quest_get_team_rooms", []string{"team_quest_teams"}},
	{"/team_quest/get_team_rooms_filters", "team_quest_get_team_rooms_filters", []string{"team_quest_team_filter"}},
	{"/team_quest/invite_team_member", "team_quest_invite_team_member", []string{"team_quest_invite_candidates", "team_quest_invite_info"}},
	{"/team_quest/ranks", "team_quest_ranks", []string{"ranks"}},
	{"/team_quest/rewards", "team_quest_rewards", []string{"teamquest_reward_data"}},
	{"/team_quest/user_tasks", "team_quest_user_tasks", []string{"user_special_tasks"}},
	{"/truelove/list", "truelove_list", []string{"truelove_list"}},
	{"/truelove/main_info", "truelove_main_info", []string{"truelove_main_info"}},
	{"/truelove/town_info", "truelove_town_info", []string{"truelove_pos_info", "truelove_town_info"}},
	{"/unit_ability_exchange/get_unit_ability_exchange_info", "unit_ability_exchange_get_unit_ability_exchange_info", []string{"changeAbilityInfo"}},
	{"/unit_ability_exchange/select_unit", "unit_ability_exchange_select_unit", []string{"changeAbility_units"}},
	{"/unit_exchange/", "unit_exchange", []string{"unit_exchange"}},
	{"/unit_exchange/show_page", "unit_exchange_show_page", []string{"unit_exchange_unit"}},
	{"/unit_friendship/show_description", "unit_friendship_show_description", []string{"unit_friendship_next_info", "unit_friendship_next_unit_list"}},
	{"/unit_friendship/show_friendship_list", "unit_friendship_show_friendship_list", []string{"unit_friendship_list"}},
	{"/unit_friendship/show_page", "unit_friendship_show_page", []string{"unit_friendship_unit"}},
	{"/unit_friendship/show_unit_list", "unit_friendship_show_unit_list", []string{"unit_friendship_friendly_list"}},
	{"/user_follows/", "user_follows", []string{"user_follows"}},
	{"/user_message_blocks/", "user_message_blocks", []string{"user_message_blocks"}},
	{"/user_monsters/evolve_select_info", "user_monsters_evolve_select_info", []string{"user_monsters_evolve_select_info"}},
	{"/user_rob_decks", "user_rob_decks", []string{"user_rob_atk_decks", "user_rob_def_decks"}},
	{"/users/function_portal", "users_function_portal", []string{"function_portal", "function_portal_list"}},
	{"/users/show_login_bonus", "users_show_login_bonus", []string{"login_bonus"}},
	{"/users/user_memo/", "users_user_memo", []string{"user_memo"}},
	// GC-08：原服 /waiting_users 的响应键是 waiting_users（录包
	// decrypted/1/waiting_users.json），不是 guild_subscriptions；
	// 装配 Guild 时上面 switch 已跳过本行（真实处理器在
	// guild_social_wire.go），这里只修「未装配 Guild」降级态的键名，
	// 避免留下一个错键的空态陷阱。
	{"/waiting_users", "waiting_users", []string{"waiting_users"}},
	{"/waiting_users/recruit", "waiting_users_recruit", []string{"temp_search_indexes"}},
}

// requireDataObjectKeys 对象键白名单：消费方按 doc()/i()/s() 读字段的
// requireData 键（实证见各页消费代码；其余键按列表 [] 下发）。
// *_condition 后缀统一按对象处理（splitRequireDataKeys 内规则）。
var requireDataObjectKeys = map[string]bool{
	"country_pick_page":                   true,
	"user_monsters_evolve_select_info":    true,
	"user_memo":                           true,
	"login_bonus":                         true,
	"guild":                               true,
	"battle_condition":                    true,
	"function_portal":                     true,
	"team_area_info":                      true,
	"team_quest_invite_info":              true,
	"truelove_main_info":                  true,
	"truelove_town_info":                  true,
	"ladder_main_page":                    true,
	"rob_battle_enter":                    true,
	"rob_battle_ready":                    true,
	"changeAbilityInfo":                   true,
	"collegeSelectData":                   true,
	"max_skill_count":                     true,
	"march_charge_activity":               true,
	"march_charge_chest_info":             true,
	"new_year_2016_casino":                true,
	"new_year_2016_currency":              true,
	"diamond_on_sale":                     true,
	"first_charge":                        true,
	"first_store_page":                    true,
	"month_card_content":                  true,
	"month_card_detail":                   true,
	"acc_login_rewards":                   true,
	"accu_login_activity_v30_rewards":     true,
	"global_rewards":                      true,
	"chest_rules":                         true,
	"chara_buff_infos":                    true,
	"chara_buff_upgrade_infos":            true,
	"fate":                                true,
	"fate_buff_summary":                   true,
	"anniversary_gacha_main_info":         true,
	"anniversary_gacha_blessing_box_info": true,
	"beachVolleyball":                     true,
	"college_get_week_sign_info":          true,
	"search_index":                        true,
	"temp_search_indexes":                 true,
	"applyStudentConditionData":           true,
	"applyForMorePositionalData":          true,
	"college_get_daily_tasks_data":        true,
	"college_get_daily_tasks_tasks":       true,
	"studentDetailInfo":                   true,
	"studyInfoData":                       true,
	"teacherInfoData":                     true,
	"taskDetailData":                      true,
	"unit_friendship_next_info":           true,
	"guild_deck":                          true,
	"guild_monsters":                      true,
	"guild_subscriptions":                 true,
	"raid_condition":                      true,
	"team_quest_team_filter":              true,
	"teamquest_reward_data":               true,
	"user_special_tasks":                  true,
	"event_1":                             true,
	"extra_quest_rankings_reward_type":    true,
	"guild_rankings_skill_rewards_type":   true,
	"chest_infos":                         true,
}

// splitRequireDataKeys 把声明键按消费形态拆分：白名单/_condition 后缀
// → 对象 {}；其余 → 列表 []。
func splitRequireDataKeys(keys []string) (arrayKeys, objectKeys []string) {
	for _, k := range keys {
		if requireDataObjectKeys[k] || strings.HasSuffix(k, "_condition") {
			objectKeys = append(objectKeys, k)
		} else {
			arrayKeys = append(arrayKeys, k)
		}
	}
	return arrayKeys, objectKeys
}
