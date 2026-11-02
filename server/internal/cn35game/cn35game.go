// Package cn35game 把国服 3.5.0 账号/启动契约装配进 httpapi 路由表。
// 进程形态：独立 mercd35 二进制（cmd/mercd35），不存在按请求内容自动分流。
//
// 注册面（contracts/00~20 + implementation-map §最小必经面）：
//
//	POST /account                                    verify 通道（type3/type300）
//	GET|POST /users/version_info                     版本握手（非零版本）
//	GET|POST /users/preset_data.json                 玩家快照（fixture+动态注入）
//	GET|POST /user_units /user_monsters              库存读面（PLAY350-07）
//	GET|POST /stages /areas /quests                  剧情关卡目录读面（STORY350-03）
//	GET  /asset_info_android4_v2_short.json.encode   资产清单（@文本格式）
//	GET  /app_conf_android.json.encode               页面配置（RenderWire 出包）
//	GET  /toto_image_4/unity/…unity3d                CDN 兜底静态（-bundles）
//	POST /cn35/codec-echo                            编解码自检（开发用）
package cn35game

import (
	mathrand "math/rand"
	"time"

	"log/slog"
	"net/http"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// Deps 装配依赖。Accounts/Sessions 为接口注入：
// 07 交付 MemoryAccounts/MemoryStore 受控桩；09 换 SQLite 持久实现。
type Deps struct {
	Log        *slog.Logger
	Accounts   AccountStore
	Sessions   cn35wire.SessionStore
	ServerHost string // 回给客户端的 serverIP/assetInfoServerIp/staticHostIp
	AssetsDir  string // assets/cn350 fixture 目录
	// BundlesDir unity3d CDN 兜底目录（仅服务于内嵌缺失项，如 sound/B-10）。
	// 空或目录不存在时该前缀返回 404 并记取证——客户端对内嵌命中资源
	// 永不请求此路径。
	BundlesDir string
	// Catalog 同伴/魔宠模板只读目录契约（PLAY350-04）。生产路径装配
	// rosterCatalog 覆盖层（ROSTER350-04）：DB 已发布模板优先，缺失行回退
	// 静态 cardcatalog（非抽取旧单位的明示回退）。nil 时仅跳过诊断路由，
	// 记 warn；mercd35 正常启动路径在 main 里强制加载。
	Catalog UnitCatalog
	// Inventory 账号库存读取面（PLAY350-06/07）。nil 时列表接口与
	// preset_data 库存键下发空数组（内存桩形态）；mercd35 正常路径装配
	// cn35store.Store。
	Inventory InventoryStore
	// Decks 账号独立编队（MIN350-06）；nil 时不注册编辑/读取接口，
	// preset_data 中 user_decks.parties 保持空对象。
	Decks DeckStore
	// Quests 剧情关卡账号进度读取面（STORY350-03：/stages /areas /quests
	// 与 preset_data 的 stage_id/area_id/quest_id/hard_mode_stage_id）。
	// nil 时按初始进度 (1,1,1,0)+无通关处理；mercd35 正常路径装配
	// cn35store.Store（0006 cn35_quest_progress/cn35_quest_clears）。
	// 写面归 STORY350-07，本接口只读。
	Quests QuestStore
	// Battles 剧情战斗会话/结算写面（STORY350-07：/quests/execute/*、
	// use_ap/result/lose/continue/log_battle）。nil 时不注册战斗端点
	// 并记 warn；mercd35 正常路径装配 cn35store.Store（0007 表）。
	Battles BattleStore
	// HardnessProfile 非空时启用重建测试 hardness 表下发（STAT350-03B）。
	// 空=不下发（保留客户端回退）。值为文件名（相对 AssetsDir）或绝对路径。
	// 表值一律为重建值，非原服数据（见 assets/cn350/serve_hardness_rebuild_test.json）。
	HardnessProfile string
	// Training 正常等级训练写面（STAT350-05A：/user_units/training）。
	// nil 时不注册该端点（响应为「功能未启用」modal）。05B（潜能）/05C（觉醒）/
	// 05D（魔宠强化）闸门未过，本 Deps 不提供对应写面，避免伪造成功。
	Training UnitTrainingStore
	// Gacha 抽卡持久化与结算面（GACHA350-03/05）。nil 时不注册
	// /gachas/execute 与 /users/reload_data，抽卡执行明确不可用。
	// 执行面采用有界重放模型（gacha_wire.go：窗口 + reload_data 信号），
	// 非严格幂等——边界与残余风险见该文件头注释。
	Gacha GachaStore
	// GachaConfig 已发布卡池配置读面（ROSTER350-03）。nil 或返回 nil 配置
	// 时：/gachas 下发空池表、execute 明确拒绝——绝不回退静态默认规则。
	GachaConfig GachaConfigStore
	// Messages 底部消息条内容池（CN35-UI-RESTORE，0010/0011 cn35_messages）。
	// nil 时 /users/messages 所有类别回空数组，footer 退回空框
	// （与装配内容池之前行为一致，不阻断页面）。
	Messages MessageStore
	// Presents /infos 内容池（LEGACY-ASSETS-02，0015 cn35_user_presents/cn35_infos）。
	// 真实 app_conf（v201710121006）Home.requireData 声明了 /user_presents/ 与
	// /infos/index 两条依赖：未注册时返回 501 → 客户端 ProtocolError →
	// ShowErrorModal + CancelShowPage，Home 整体打不开。
	// nil 时读面回空列表（IconPresentParts/IconInfoParts 都有显式空态分支，
	// 表现为图标/红点隐藏），不阻断页面。
	Presents PresentStore
	Infos    InfoStore
	// UnitOps 同伴写面（2026-09-28 P0 批：set_leader/set_helper/protect，
	// 0016 helper_unit_id）。nil 时对应端点不注册。
	UnitOps UnitOpsStore
	// MonsterOps 魔宠写面（2026-09-28 P0 批：on/off/protect，双向引用
	// 同事务）。nil 时对应端点不注册。
	MonsterOps MonsterOpsStore
	// PresentReceive 礼物领取写面（2026-09-28 P0 批：/user_presents/receive/
	// 单领+全领，发奖与标记同事务）。nil 时该端点不注册。
	PresentReceive PresentReceiveStore
	// Items 物品目录/账号库存读面（ITEM350-02）。nil 时 /user_items/
	// 保持空态、快照不下发 powerup_items 以外的物品数据。
	Items ItemStore
	// TutorialTasks 新手任务读/写面（TASK350：empty_state 软锁修复）。
	// nil 时 /task/show_tutorial_tasks 退回空态、doing 列表不下发。
	TutorialTasks TutorialTaskStore
	// Resources 资源副本生效配置读面（ITEM350-03/05）。nil 或无发布时
	// 关卡目录不含资源副本。
	Resources ResourceConfigReadStore
	// Medal 徽章商品读面+兑换写面（ITEM350-07）。nil 时 /medal_items/
	// 保持空态、exchange 不注册。
	Medal MedalStore
	// Skills 符文库存读面+写面（RUNE350-01：/user_skills/*）。nil 时
	// /user_skills/ 保持空态、写面不注册；装配后 buildUnitDoc 顺带
	// 合入装备符文加成（战斗/列表同源）。
	Skills SkillStore
	// Awaken 觉醒书消费写面（ITEM350-09，暂定契约 /user_units/awake）。
	// nil 时觉醒请求落到 /user_units/ 前缀兜底（明确拒绝）。
	Awaken AwakenStore
	// ItemUse 物品使用写面（/user_items/use/?id=&count=）。nil 时请求
	// 回“服务端未装配”弹窗而不静默吞掉。
	ItemUse ItemUseStore
	// RemoteAssets 远端资源叠加管理器（remote_assets.go）。非 nil 时
	// asset_info 由管理器快照出 body、version_info 的 asset_info_version
	// 取管理器有效版本、bundleHandler 先查叠加快照再回退 BundlesDir，
	// 并注册 /cn35/assets/remote 诊断路由。nil 时维持 fixture 直读
	// （apidoc/旧测试路径）。
	RemoteAssets *RemoteAssets
	// BattleRand 战斗摇奖源（剧情 seed 掉落/随机背景/资源副本掉落共用）。
	// nil 时每次开战新建加密种子 mathrand（newDropRand）；测试可注入
	// 固定 seed 的 *mathrand.Rand 使掉落判定确定。
	BattleRand *mathrand.Rand
	// Guild 公会/公会战域读写面（GUILD350-02..08，迁移 0027）。
	// nil 时公会相关 requireData 键走 sweep 空态。
	Guild GuildStore
	// GuildClock 会战时段判定时钟（测试注入）；nil 用 time.Now。
	GuildClock func() time.Time
	// DailyShop 每日商店读面+领取写面（SHOP350-01，迁移 0031：
	// 面包店 /red_diamond_shop/* 每日 0 元购钻石包）。nil 时
	// /red_diamond_shop/ 走 sweep 空态、buy/refresh 不注册。
	DailyShop DailyShopStore
	// LoginBonus 不可思议之泉读/领面（LOGIN350-02，迁移 0035：按时间累积金币/
	// 公会建设素材/BP，BP 允许溢出）。nil 时 /users/show_login_bonus 保持
	// requireData 空态、/users/receive_login_bonus 不注册。
	LoginBonus SpringStore
	// Signin 每月 28 格签到读/领面（LOGIN350-02，迁移 0035）。nil 时
	// /accu_login_activity/ 走 requireData 空态、receive 不注册。
	Signin SigninStore
	// Adventure 登录冒险读/发面（LOGIN350-03，迁移 0036：轮次配置 + 逐日
	// 位图 + 当日奖励写入 /user_presents/ 内容池 + 轮次滚动）。nil 时
	// /adventure/ 保持历史空态（结构完整、客户端跳过分支，不报错）；
	// 库里未配置轮次时同样回该空态，故运营未排期即不弹窗。
	Adventure AdventureStore
	// AdventureClock 登录冒险的判定时钟（LA-09 跨天/时钟对抗向量注入）。
	// nil 用 time.Now；生产路径不设置。
	AdventureClock func() time.Time
}

// Register 注册 CN 3.5.0 启动必经路由，返回登记数。
func Register(reg *httpapi.Registry, dep Deps) int {
	if dep.Log == nil {
		dep.Log = slog.Default()
	}
	if dep.Accounts == nil {
		dep.Accounts = NewMemoryAccounts()
	}
	if dep.Sessions == nil {
		dep.Sessions = cn35wire.NewMemoryStore()
	}
	count := 0
	// prefixPaths 记录已登记的**前缀路由**（如 `/user_units/`、`/quests/execute/`）。
	// 尾斜杠补登记必须避让它们：前缀路由依赖 Lookup 的「精确优先、其次前缀」
	// 顺序兜住未启用子项（registry.go:134-146）。若把 `/user_units/` 也注册成
	// **精确**路由，`/user_units/upgrade` 会先命中该精确项，不再落到
	// unitGrowthUnsupportedHandler，未启用写面被误当已实现
	// （TestGrowthUnsupportedEndpointsRejected 锁此不变量）。
	prefixPaths := map[string]bool{}
	register := func(method, path, note string, handler httpapi.Handler) {
		if reg.HandleWithStatus(method, path, note, httpapi.ModelConfirmed, handler) {
			count++
		}
		// 尾斜杠形态补登记（LEGACY-ASSETS-02）。
		//
		// 根因：真实 app_conf（v201710121006）的 requireData.path 大量使用
		// **带尾斜杠**的生产写法（`/stages/`、`/events/`、`/trials/`、
		// `/user_units/`、`/albums/` 等），而本服务端历史实现按**无尾斜杠**
		// 注册（`/stages`）。Registry.Lookup 是精确匹配（registry.go:134），
		// 故 `/stages/` 落到「未注册」→ 501 → 客户端 ProtocolError →
		// ShowErrorModal + CancelShowPage（PageBase.cs:782-800）。
		// 实机 2026-09-27 证据：Home 四条依赖补齐后，客户端请求 `/stages/`
		// `/events/` `/trials/` `/team_quest/get_team_areas` 仍 501。
		//
		// 两形态是同一逻辑端点，故在注册层统一补齐；避让前缀路由（见上）。
		if path != "/" && !strings.HasSuffix(path, "/") &&
			!prefixPaths[strings.ToUpper(method)+" "+path+"/"] {
			if reg.HandleWithStatus(method, path+"/", note, httpapi.ModelConfirmed, handler) {
				count++
			}
		}
	}
	// 预声明本文件内所有**前缀路由**（下方 registerPrefix 会实际登记）。
	// 必须在任何 register 调用之前填好：`/user_units` 的普通注册发生在
	// `/user_units/` 前缀登记之前，若依赖登记顺序则避让判断会漏掉。
	for _, pre := range []struct{ m, p string }{
		{http.MethodGet, "/user_units/"},
		{http.MethodPost, "/user_units/"},
		// P0 批：/user_monsters/ 前缀兜底（未取证魔宠写面 modal）——
		// 须预声明，否则 /user_monsters 读面注册会自动补登尾斜杠精确
		// 路由抢占前缀键。
		{http.MethodGet, "/user_monsters/"},
		{http.MethodPost, "/user_monsters/"},
		{http.MethodGet, "/quests/execute/"},
		{http.MethodPost, "/quests/execute/"},
		// 故事阅读（2026-09-28 Note）：/stories/read/<id>.json 前缀路由。
		{http.MethodGet, "/stories/read/"},
		{http.MethodPost, "/stories/read/"},
		{http.MethodGet, "/toto_image_4/unity/"},
	} {
		prefixPaths[pre.m+" "+pre.p] = true
	}
	// registerPrefix 登记前缀路由并记录，供 register 避让。
	registerPrefix := func(method, prefix, note string, handler httpapi.Handler) bool {
		prefixPaths[strings.ToUpper(method)+" "+prefix] = true
		if reg.HandlePrefixWithStatus(method, prefix, note, httpapi.ModelConfirmed, handler) {
			count++
			return true
		}
		return false
	}

	register(http.MethodPost, "/account",
		"CN 3.5.0 账号帧（type3/t300，AES-static+zlib+xor 帧）",
		verifyHandler(dep))

	// game 通道：契约 10/11。客户端 GetFromServer 以 WWWForm POST 发送
	// （_method=GET 在加密表单内），GET/POST 双形态都注册。
	register(http.MethodGet, "/users/version_info",
		"CN 3.5.0 版本握手（asset_info_version/app_conf_version 非零）",
		versionInfoHandler(dep))
	register(http.MethodPost, "/users/version_info",
		"CN 3.5.0 版本握手 POST 形态", versionInfoHandler(dep))
	register(http.MethodGet, "/users/preset_data.json",
		"CN 3.5.0 玩家快照（fixture+会话注入，tutorial_session 支持）",
		presetDataHandler(dep))
	register(http.MethodPost, "/users/preset_data.json",
		"CN 3.5.0 玩家快照 POST 形态", presetDataHandler(dep))
	register(http.MethodPost, "/users/update",
		"CN 3.5.0 教程写入（user_name/tutorial_finish，status:success 契约）",
		usersUpdateHandler(dep))
	register(http.MethodPost, "/users/verify_nickname",
		"CN 3.5.0 昵称查重（{result:bool}）", verifyNicknameHandler(dep))
	register(http.MethodGet, "/users/messages",
		"CN 3.5.0 消息面（Top 轮询空类别字典）", usersMessagesHandler(dep))
	register(http.MethodPost, "/users/messages",
		"CN 3.5.0 消息面 POST 形态", usersMessagesHandler(dep))
	register(http.MethodGet, "/users/show",
		"CN 3.5.0 玩家主页读面（UserPage requireData.user_temp）", usersShowHandler(dep))
	register(http.MethodPost, "/users/show",
		"CN 3.5.0 玩家主页读面 POST 形态", usersShowHandler(dep))
	register(http.MethodGet, "/users/update_memo",
		"CN 3.5.0 玩家签名更新（UserPage.OnMemoChange）", usersUpdateMemoHandler(dep))
	register(http.MethodPost, "/users/update_memo",
		"CN 3.5.0 玩家签名更新 POST 形态", usersUpdateMemoHandler(dep))
	register(http.MethodGet, "/users/help",
		"CN 3.5.0 玩家救援写面", usersHelpHandler(dep))
	register(http.MethodPost, "/users/help",
		"CN 3.5.0 玩家救援写面 POST 形态", usersHelpHandler(dep))
	// CN35-UI-RESTORE：活动页 id 列表。客户端 BasicParts(top_activity) 与
	// CarouselBannerParts 都经 common.activityList 取键，再查 app_conf
	// partials.CarouselBannerPartial.CarouselBanner.banners 取图。
	register(http.MethodGet, "/act_pages",
		"CN 3.5.0 活动页列表（驱动活动大图/横幅轮播与活动红点）", actPagesHandler(dep))
	register(http.MethodPost, "/act_pages",
		"CN 3.5.0 活动页列表 POST 形态", actPagesHandler(dep))

	// LEGACY-ASSETS-02：真实 app_conf（v201710121006）Home.requireData 的四条依赖。
	// 此前手写 v44 未声明它们，故从未注册；缺任一条客户端即
	// ShowErrorModal + CancelShowPage（PageBase.cs:782-800）。
	// user_presents/infos 为运营内容（0015 入库）；choose_attr_page 无生产数据
	// 来源，回结构完整空态（契约卡标注语义未证）；adventure 自 LOGIN350-03
	// （0036）起为真实读面，未配置轮次时回同一空态（见 requiredata.go）。
	register(http.MethodGet, "/user_presents/",
		"CN 3.5.0 待领礼物列表（Home requireData.user_presents）",
		userPresentsHandler(dep))
	register(http.MethodPost, "/user_presents/",
		"CN 3.5.0 待领礼物列表 POST 形态", userPresentsHandler(dep))
	register(http.MethodGet, "/infos/index",
		"CN 3.5.0 公告面（Home requireData.infos）", infosHandler(dep))
	register(http.MethodPost, "/infos/index",
		"CN 3.5.0 公告面 POST 形态", infosHandler(dep))
	register(http.MethodGet, "/adventure/",
		"CN 3.5.0 登录冒险读面+当日发放（LOGIN350-03，0036 轮次配置；未配置回空态）",
		adventureHandler(dep))
	register(http.MethodPost, "/adventure/",
		"CN 3.5.0 登录冒险 POST 形态", adventureHandler(dep))
	register(http.MethodGet, "/gachas/choose_attr_page/",
		"CN 3.5.0 属国抽选页（Home requireData.country_pick_page；空态）",
		chooseAttrPageHandler(dep))
	register(http.MethodPost, "/gachas/choose_attr_page/",
		"CN 3.5.0 属国抽选页 POST 形态", chooseAttrPageHandler(dep))

	// UI-WALKTHROUGH-20260927：主模块入口 requireData 缺口（台账 §4 与
	// 2026-09-28 实机 501 取证一致）。读面空态集中在 module_requiredata.go。
	register(http.MethodGet, "/users/user_coin",
		"CN 3.5.0 钻石余额读面（Gacha/CoinShop requireData.user_coin）",
		userCoinHandler(dep))
	register(http.MethodPost, "/users/user_coin",
		"CN 3.5.0 钻石余额读面 POST 形态", userCoinHandler(dep))
	// 台账 §3：配置裸写 /users/preset_data（无 .json）——CoinShop/
	// RedDiamondShop 的 requireData path；与 .json 形态同一 handler。
	register(http.MethodGet, "/users/preset_data",
		"CN 3.5.0 玩家快照（裸路径别名，同 preset_data.json）",
		presetDataHandler(dep))
	register(http.MethodPost, "/users/preset_data",
		"CN 3.5.0 玩家快照裸路径 POST 形态", presetDataHandler(dep))
	registerModuleRequireData(register, dep)

	// GUILD350：公会/公会战域。装配 dep.Guild 时注册真实读/写面；
	// 未装配时相关 requireData 缺口仍由 sweep 表空态兜底。
	if dep.Guild != nil {
		registerGuildRoutes(register, dep)
	} else {
		dep.log().Warn("CN3.5 未装配 Guild（公会域走 requireData 空态）")
	}

	// PLAY350-07 库存读面：会话账号 → InventoryStore 行 × cardcatalog 模板
	// 组装 user_units/user_monsters。两个接口互相捆绑对方键——详情页
	// WaitForData 要求双数据同时就位（handoffs/03 §消费链）。
	register(http.MethodGet, "/user_units",
		"CN 3.5.0 同伴列表（账号库存×catalog，捆绑 user_monsters）",
		userUnitsHandler(dep))
	register(http.MethodPost, "/user_units",
		"CN 3.5.0 同伴列表 POST 形态", userUnitsHandler(dep))
	register(http.MethodGet, "/user_monsters",
		"CN 3.5.0 魔宠列表（账号库存×catalog，捆绑 user_units）",
		userMonstersHandler(dep))
	register(http.MethodPost, "/user_monsters",
		"CN 3.5.0 魔宠列表 POST 形态", userMonstersHandler(dep))

	// ALBUM350-01 图鉴读面（cn35-album-ui-restore）：/albums/ 全目录
	// （未持有行 hide=true，计数 N/M 不再 0/0）+ /albums/album_unit
	// 未持有详情（AlbumUnitDetail data_path 目标）。未装配时
	// module_requiredata.go 空态桩兜底（见该文件 ALBUM350-01 注释）。
	if dep.Inventory != nil && dep.Catalog != nil {
		register(http.MethodGet, "/albums/",
			"CN 3.5.0 同伴图鉴全目录（持有/未持有 hide 标记+详情入口）",
			albumsHandler(dep))
		register(http.MethodPost, "/albums/",
			"CN 3.5.0 同伴图鉴全目录 POST 形态", albumsHandler(dep))
		register(http.MethodGet, "/albums/album_unit",
			"CN 3.5.0 图鉴未持有单位详情（Lv1 模板态数值）",
			albumUnitDetailHandler(dep))
		register(http.MethodPost, "/albums/album_unit",
			"CN 3.5.0 图鉴未持有单位详情 POST 形态", albumUnitDetailHandler(dep))
		register(http.MethodGet, "/albums/show_unit",
			"CN 3.5.0 原服图鉴单位详情（兼容 unit_id/id）",
			albumUnitDetailHandler(dep))
		register(http.MethodPost, "/albums/show_unit",
			"CN 3.5.0 原服图鉴单位详情 POST 形态", albumUnitDetailHandler(dep))
		register(http.MethodGet, "/monster_albums/",
			"CN 3.5.0 魔宠图鉴全目录（持有/未持有 hide 标记+详情入口）",
			monsterAlbumsHandler(dep))
		register(http.MethodPost, "/monster_albums/",
			"CN 3.5.0 魔宠图鉴全目录 POST 形态", monsterAlbumsHandler(dep))
		register(http.MethodGet, "/monster_albums/show_monster",
			"CN 3.5.0 魔宠图鉴详情（Lv1 模板态数值）",
			monsterAlbumDetailHandler(dep))
		register(http.MethodPost, "/monster_albums/show_monster",
			"CN 3.5.0 魔宠图鉴详情 POST 形态", monsterAlbumDetailHandler(dep))
	}

	// STAT350-05A 正常等级训练写面：只改 level，扣 gacha_point，回推读面。
	// 05B/05C/05D 闸门未过 → 不注册对应端点（不伪造写面）。
	if dep.Training != nil {
		register(http.MethodGet, "/user_units/training",
			"CN 3.5.0 同伴训练（事务内等级边界/扣点/只改 level；STAT350-05A）",
			unitTrainingHandler(dep))
		register(http.MethodPost, "/user_units/training",
			"CN 3.5.0 同伴训练 POST 形态", unitTrainingHandler(dep))
	} else {
		dep.log().Warn("CN3.5 未装配 Training（跳过 /user_units/training；05A 写面未启用）")
	}

	// GACHA350-05 抽卡执行链。/gachas/execute 为复数路径（客户端
	// GachaIndexParts.cs:156/238 硬编码拼接），/users/reload_data 供错误分支
	// 刷新缓存（GachaIndexParts.cs:325）。执行面按有界重放模型开放真实
	// 扣费：同参重发回放快照不重复扣费，合法再抽正常结算（gacha_wire.go）。
	if dep.Gacha != nil {
		register(http.MethodGet, "/gachas",
			"CN 3.5.0 卡池目录读面（app_conf requireData.gachas 门控页面就绪）",
			gachaPoolsHandler(dep))
		register(http.MethodPost, "/gachas",
			"CN 3.5.0 卡池目录 POST 形态", gachaPoolsHandler(dep))
		register(http.MethodGet, "/gachas/execute",
			"CN 3.5.0 抽卡执行（单/连抽；幂等未达严格标准时拒绝真实扣费）",
			gachaExecuteHandler(dep))
		register(http.MethodPost, "/gachas/execute",
			"CN 3.5.0 抽卡执行 POST 形态", gachaExecuteHandler(dep))
		register(http.MethodGet, "/users/reload_data",
			"CN 3.5.0 缓存刷新（on=gachas_execute：user/user_coin/user_units/gachas）",
			gachaReloadDataHandler(dep))
		register(http.MethodPost, "/users/reload_data",
			"CN 3.5.0 缓存刷新 POST 形态", gachaReloadDataHandler(dep))
	} else {
		dep.log().Warn("CN3.5 未装配 Gacha（跳过 /gachas/execute 与 /users/reload_data）")
	}

	if registerPrefix(http.MethodGet, "/user_units/",
		"CN 3.5.0 单位养成写面兜底（未启用的子项明确拒绝）",
		unitGrowthUnsupportedHandler(dep)) {
	}
	if registerPrefix(http.MethodPost, "/user_units/",
		"CN 3.5.0 单位养成写面兜底 POST 形态",
		unitGrowthUnsupportedHandler(dep)) {
	}

	// ---- 2026-09-28 P0 写面批（设计 Note
	// notes/proposed/2026-09-28-cn35-p0-write-endpoints.md）----
	// 规则：有客户端/存量证据 → 实装；无证据 → 保留明确拒绝弹窗。
	// 精确路由优先于上方 /user_units/ 与下方 /user_monsters/ 前缀兜底
	// （registry 精确→前缀匹配序）。
	if dep.UnitOps != nil {
		register(http.MethodGet, "/user_units/set_leader",
			"CN 3.5.0 设队长（?id=，UserUnitDetail.cs:441-442）",
			unitSetRoleHandler(dep, "leader"))
		register(http.MethodPost, "/user_units/set_leader",
			"CN 3.5.0 设队长 POST 形态", unitSetRoleHandler(dep, "leader"))
		register(http.MethodGet, "/user_units/set_helper",
			"CN 3.5.0 设助战（?id=，UserUnitDetail.cs:460-461）",
			unitSetRoleHandler(dep, "helper"))
		register(http.MethodPost, "/user_units/set_helper",
			"CN 3.5.0 设助战 POST 形态", unitSetRoleHandler(dep, "helper"))
		register(http.MethodGet, "/user_units/protect",
			"CN 3.5.0 同伴保护位（?unit_id=&protected=，UserUnitDetail.cs:683-685）",
			unitProtectHandler(dep))
		register(http.MethodPost, "/user_units/protect",
			"CN 3.5.0 同伴保护位 POST 形态", unitProtectHandler(dep))
	} else {
		dep.log().Warn("CN3.5 未装配 UnitOps（跳过 set_leader/set_helper/protect）")
	}
	if dep.MonsterOps != nil {
		register(http.MethodGet, "/user_monsters/on",
			"CN 3.5.0 魔宠装备（?uuid=&unit_id=，UserMonsterDetail.cs:390）",
			monsterOnHandler(dep))
		register(http.MethodPost, "/user_monsters/on",
			"CN 3.5.0 魔宠装备 POST 形态", monsterOnHandler(dep))
		register(http.MethodGet, "/user_monsters/off",
			"CN 3.5.0 魔宠卸下（?uuid=&unit_id=，UserMonsterDetail.cs:366/414）",
			monsterOffHandler(dep))
		register(http.MethodPost, "/user_monsters/off",
			"CN 3.5.0 魔宠卸下 POST 形态", monsterOffHandler(dep))
		register(http.MethodGet, "/user_monsters/protect",
			"CN 3.5.0 魔宠保护位（?uuids[]=&protected=，UserMonsterDetail.cs:515-516）",
			monsterProtectHandler(dep))
		register(http.MethodPost, "/user_monsters/protect",
			"CN 3.5.0 魔宠保护位 POST 形态", monsterProtectHandler(dep))
		register(http.MethodGet, "/user_monsters/powerup",
			"CN 3.5.0 魔宠强化（?uuid=&powerup_item_id=&count=，MonsterPowerupModal.cs:184）",
			monsterPowerupHandler(dep))
		register(http.MethodPost, "/user_monsters/powerup",
			"CN 3.5.0 魔宠强化 POST 形态", monsterPowerupHandler(dep))
		register(http.MethodGet, "/user_monsters/evolve_select_info",
			"CN 3.5.0 魔宠进化选择列表（MonsterEvolveModal requireData）",
			monsterEvolveSelectInfoHandler(dep))
		register(http.MethodPost, "/user_monsters/evolve_select_info",
			"CN 3.5.0 魔宠进化选择列表 POST 形态", monsterEvolveSelectInfoHandler(dep))
	} else {
		dep.log().Warn("CN3.5 未装配 MonsterOps（跳过 on/off/protect/powerup）")
	}
	// /user_monsters/ 前缀兜底：未取证的魔宠写面（sell/evolve 族/
	// share 等）与未注册子路径一律业务级 modal 拒绝，不再 501。
	if registerPrefix(http.MethodGet, "/user_monsters/",
		"CN 3.5.0 魔宠写面兜底（未取证子项明确拒绝）",
		monsterUnsupportedHandler(dep)) {
	}
	if registerPrefix(http.MethodPost, "/user_monsters/",
		"CN 3.5.0 魔宠写面兜底 POST 形态",
		monsterUnsupportedHandler(dep)) {
	}
	// 礼物领取（单领 ?uuid=/全领/?without_unit=，UserPresentsListRow.cs:86、
	// UserPresentReceiveAllImageButton.cs:24-27）。/user_presents/receive
	// 与尾斜杠形都注册——精准匹配优先，客户端只发尾斜杠形。
	if dep.PresentReceive != nil {
		register(http.MethodGet, "/user_presents/receive/",
			"CN 3.5.0 礼物领取（单领/全领，同事务发奖+标记）",
			presentReceiveHandler(dep))
		register(http.MethodPost, "/user_presents/receive/",
			"CN 3.5.0 礼物领取 POST 形态", presentReceiveHandler(dep))
		register(http.MethodGet, "/user_presents/receive",
			"CN 3.5.0 礼物领取（去尾斜杠形）", presentReceiveHandler(dep))
		register(http.MethodPost, "/user_presents/receive",
			"CN 3.5.0 礼物领取 POST 形态（去尾斜杠形）", presentReceiveHandler(dep))
	} else {
		dep.log().Warn("CN3.5 未装配 PresentReceive（跳过 /user_presents/receive/）")
	}

	// ITEM350-02 物品背包真实读面（装配 Items 时取代 requiredata 空态）。
	if dep.Items != nil {
		register(http.MethodGet, "/user_items/",
			"CN 3.5.0 道具列表（cn35_player_items 投影：user_items+user_items_hash）",
			userItemsHandler(dep))
		register(http.MethodPost, "/user_items/",
			"CN 3.5.0 道具列表 POST 形态", userItemsHandler(dep))
		register(http.MethodGet, "/user_units/compose_upgrade_item",
			"CN 3.5.0 进化素材合成（UserItemsListRow count_modal_hash）",
			unitComposeUpgradeItemHandler(dep, false))
		register(http.MethodPost, "/user_units/compose_upgrade_item",
			"CN 3.5.0 进化素材合成 POST 形态",
			unitComposeUpgradeItemHandler(dep, false))
		register(http.MethodGet, "/user_units/decompose_upgrade_item",
			"CN 3.5.0 进化素材分解（UserItemsListRow count_modal_hash）",
			unitComposeUpgradeItemHandler(dep, true))
		register(http.MethodPost, "/user_units/decompose_upgrade_item",
			"CN 3.5.0 进化素材分解 POST 形态",
			unitComposeUpgradeItemHandler(dep, true))
	}
	// TASK350：新手任务读面+领取写面（替代 requiredata 空态——空文档
	// 使新手指引页 ListParts 走早退路径，真机实测导航失效需 force-stop）。
	if dep.TutorialTasks != nil {
		register(http.MethodGet, "/task/show_tutorial_tasks",
			"CN 3.5.0 新手任务列表（自研目录，台账推导进度）",
			showTutorialTasksHandler(dep))
		register(http.MethodPost, "/task/show_tutorial_tasks",
			"CN 3.5.0 新手任务列表 POST 形态", showTutorialTasksHandler(dep))
		register(http.MethodGet, "/task/receive_tutorial_reward",
			"CN 3.5.0 新手任务领奖（同事务发奖+标记）",
			receiveTutorialRewardHandler(dep))
		register(http.MethodPost, "/task/receive_tutorial_reward",
			"CN 3.5.0 新手任务领奖 POST 形态", receiveTutorialRewardHandler(dep))
		register(http.MethodGet, "/task/update_task_status",
			"CN 3.5.0 任务进度回推（进度服务端推导，仅回成功）",
			updateTaskStatusHandler(dep))
		register(http.MethodPost, "/task/update_task_status",
			"CN 3.5.0 任务进度回推 POST 形态", updateTaskStatusHandler(dep))
	} else {
		register(http.MethodGet, "/task/show_tutorial_tasks",
			"CN 3.5.0 新手任务列表（TutorialTasks 未装配，空态）",
			emptyRequireDataHandler(dep, "task_show_tutorial_tasks",
				[]string{"tutorial_task_list"}, nil))
		register(http.MethodPost, "/task/show_tutorial_tasks",
			"CN 3.5.0 新手任务列表空态 POST 形态",
			emptyRequireDataHandler(dep, "task_show_tutorial_tasks",
				[]string{"tutorial_task_list"}, nil))
	}
	// ITEM350-07 徽章交易所读面与兑换写面。
	if dep.Medal != nil && dep.Items != nil {
		register(http.MethodGet, "/medal_items/",
			"CN 3.5.0 徽章商品列表（生效版本 item_list 分组）",
			medalItemsHandler(dep))
		register(http.MethodPost, "/medal_items/",
			"CN 3.5.0 徽章商品列表 POST 形态", medalItemsHandler(dep))
		register(http.MethodGet, "/medal_items/exchange",
			"CN 3.5.0 徽章兑换（?id=&count=&request_key=，同事务扣发）",
			medalExchangeHandler(dep))
		register(http.MethodPost, "/medal_items/exchange",
			"CN 3.5.0 徽章兑换 POST 形态", medalExchangeHandler(dep))
	}
	// SHOP350-01 每日商店（面包店）读面与领取写面。
	if dep.DailyShop != nil {
		register(http.MethodGet, "/red_diamond_shop/",
			"CN 3.5.0 每日商品列表（red_diamond_shop+info：0 元购钻石包，4 点刷新）",
			redDiamondShopHandler(dep))
		register(http.MethodPost, "/red_diamond_shop/",
			"CN 3.5.0 每日商品列表 POST 形态", redDiamondShopHandler(dep))
		register(http.MethodGet, "/red_diamond_shop/buy",
			"CN 3.5.0 每日商品购买（?id=，同事务领取标记+发钻）",
			redDiamondShopBuyHandler(dep))
		register(http.MethodPost, "/red_diamond_shop/buy",
			"CN 3.5.0 每日商品购买 POST 形态", redDiamondShopBuyHandler(dep))
		register(http.MethodGet, "/red_diamond_shop/refresh",
			"CN 3.5.0 每日商品手动刷新（免费，回推行集）",
			redDiamondShopRefreshHandler(dep))
		register(http.MethodPost, "/red_diamond_shop/refresh",
			"CN 3.5.0 每日商品手动刷新 POST 形态", redDiamondShopRefreshHandler(dep))
		register(http.MethodGet, "/users/function_portal",
			"CN 3.5.0 主人公日常入口列表（含面包店 banner 跳转）",
			functionPortalHandler(dep))
		register(http.MethodPost, "/users/function_portal",
			"CN 3.5.0 主人公日常入口列表 POST 形态", functionPortalHandler(dep))
	}
	// LOGIN350-02 不可思议之泉（时间累积）读面与领取写面。
	if dep.LoginBonus != nil {
		register(http.MethodGet, "/users/show_login_bonus",
			"CN 3.5.0 泉水读面（show_login_bonus 待领量+user；不发奖）",
			loginBonusShowHandler(dep))
		register(http.MethodPost, "/users/show_login_bonus",
			"CN 3.5.0 泉水读面 POST 形态", loginBonusShowHandler(dep))
		register(http.MethodGet, "/users/receive_login_bonus",
			"CN 3.5.0 泉水领取（同事务 CAS 推进+三资源入账+台账）",
			loginBonusReceiveHandler(dep))
		register(http.MethodPost, "/users/receive_login_bonus",
			"CN 3.5.0 泉水领取 POST 形态", loginBonusReceiveHandler(dep))
	}
	// LOGIN350-02 每月 28 格签到读面与领取写面。
	if dep.Signin != nil {
		register(http.MethodGet, "/accu_login_activity/",
			"CN 3.5.0 月签到列表（acc_login_rewards 28 行）", signinShowHandler(dep))
		register(http.MethodPost, "/accu_login_activity/",
			"CN 3.5.0 月签到列表 POST 形态", signinShowHandler(dep))
		register(http.MethodGet, "/accu_login_activity/receive",
			"CN 3.5.0 月签到领取（?node=1..28，台账+入账同事务）",
			signinClaimHandler(dep))
		register(http.MethodPost, "/accu_login_activity/receive",
			"CN 3.5.0 月签到领取 POST 形态", signinClaimHandler(dep))
	}
	// RUNE350-01 符文读面与写面（装配 Skills 时取代 requiredata 空态）。
	if dep.Skills != nil {
		register(http.MethodGet, "/user_skills/",
			"CN 3.5.0 符文列表（cn35_player_skills 投影）",
			userSkillsHandler(dep))
		register(http.MethodPost, "/user_skills/",
			"CN 3.5.0 符文列表 POST 形态", userSkillsHandler(dep))
		register(http.MethodGet, "/user_skills/set_all",
			"CN 3.5.0 符文装备（?unit_id=&uuids[]= 整体替换槽位）",
			skillSetAllHandler(dep))
		register(http.MethodPost, "/user_skills/set_all",
			"CN 3.5.0 符文装备 POST 形态", skillSetAllHandler(dep))
		register(http.MethodGet, "/user_skills/off",
			"CN 3.5.0 符文卸下（?uuid= 单枚 / ?all=true&unit_id= 全卸下）",
			skillOffHandler(dep))
		register(http.MethodPost, "/user_skills/off",
			"CN 3.5.0 符文卸下 POST 形态", skillOffHandler(dep))
		register(http.MethodGet, "/user_skills/sell",
			"CN 3.5.0 符文出售（?uuids[]= 批量，入账 gacha_point）",
			skillSellHandler(dep))
		register(http.MethodPost, "/user_skills/sell",
			"CN 3.5.0 符文出售 POST 形态", skillSellHandler(dep))
		register(http.MethodGet, "/user_skills/protect",
			"CN 3.5.0 符文保护（?uuids[]=&protected=）",
			skillProtectHandler(dep))
		register(http.MethodPost, "/user_skills/protect",
			"CN 3.5.0 符文保护 POST 形态", skillProtectHandler(dep))
		register(http.MethodGet, "/user_skills/powerup",
			"CN 3.5.0 符文强化（?uuid=&powerup_item_id=&count=）",
			skillPowerupHandler(dep))
		register(http.MethodPost, "/user_skills/powerup",
			"CN 3.5.0 符文强化 POST 形态", skillPowerupHandler(dep))
		register(http.MethodGet, "/user_skills/composite",
			"CN 3.5.0 符文合成（?uuid=&seed_uuid=）",
			skillCompositeHandler(dep))
		register(http.MethodPost, "/user_skills/composite",
			"CN 3.5.0 符文合成 POST 形态", skillCompositeHandler(dep))
	}
	// ITEM350-09 觉醒写面（暂定契约 /user_units/awake?id=）。精确路由
	// 优先于下方 /user_units/ 前缀兜底；客户端实际路径若不同仍明确拒绝。
	if dep.Awaken != nil && dep.Items != nil {
		register(http.MethodGet, "/user_units/awake",
			"CN 3.5.0 觉醒（消耗 awakening_book_<rare>，dup+1；暂定契约）",
			unitAwakeHandler(dep))
		register(http.MethodPost, "/user_units/awake",
			"CN 3.5.0 觉醒 POST 形态", unitAwakeHandler(dep))
	}
	// 物品使用写面（/user_items/use/）：药水回体力等消耗品效果；
	// 依赖 Items 读面做响应内库存回推。
	if dep.ItemUse != nil && dep.Items != nil {
		register(http.MethodGet, "/user_items/use/",
			"CN 3.5.0 物品使用（?id=<content_id>&count=<n>）",
			useItemHandler(dep))
		register(http.MethodPost, "/user_items/use/",
			"CN 3.5.0 物品使用 POST 形态", useItemHandler(dep))
	}

	// MIN350-06 账号编队读写面。party_id 是 parties 对象原 key；
	// execute 的 001/002 是按相同有序列表解析出的轮盘 ordinal。
	if dep.Decks != nil {
		register(http.MethodGet, "/user_decks",
			"CN 3.5.0 账号独立编队（parties 按 display_order 有序下发）",
			userDecksHandler(dep))
		register(http.MethodPost, "/user_decks",
			"CN 3.5.0 账号编队读取 POST 形态", userDecksHandler(dep))
		register(http.MethodGet, "/user_decks/on",
			"CN 3.5.0 编队槽位设置（会话账号/模板库存/队内唯一）",
			userDeckMutationHandler(dep, false))
		register(http.MethodPost, "/user_decks/on",
			"CN 3.5.0 编队槽位设置 POST 形态", userDeckMutationHandler(dep, false))
		register(http.MethodGet, "/user_decks/off",
			"CN 3.5.0 编队槽位清空（保留槽位并校验旧模板）",
			userDeckMutationHandler(dep, true))
		register(http.MethodPost, "/user_decks/off",
			"CN 3.5.0 编队槽位清空 POST 形态", userDeckMutationHandler(dep, true))
	} else {
		dep.log().Warn("CN3.5 未装配 Decks（跳过账号编队端点注册）")
	}

	// STORY350-03 剧情关卡目录读面：StageSelect/AreaSelect/QuestSelect
	// 消费的 stages/areas_<stageId>/quests_<areaId> 缓存键。
	// 路径服务端自定（客户端不硬编码；同族先例 /areas/event?event_id=N），
	// 04 在 requireData.path 写同形串。
	register(http.MethodGet, "/stages",
		"CN 3.5.0 关卡目录（stages 五 tab 键，按账号 stage_id 过滤）",
		stagesHandler(dep))
	register(http.MethodPost, "/stages",
		"CN 3.5.0 关卡目录 POST 形态", stagesHandler(dep))
	register(http.MethodGet, "/areas",
		"CN 3.5.0 区域目录（?stage_id=N → areas_N + quests_<areaId> 多键）",
		areasHandler(dep))
	register(http.MethodPost, "/areas",
		"CN 3.5.0 区域目录 POST 形态", areasHandler(dep))
	register(http.MethodGet, "/quests",
		"CN 3.5.0 关卡列表（?area_id=N → quests_N，含账号通关标记）",
		questsHandler(dep))
	register(http.MethodPost, "/quests",
		"CN 3.5.0 关卡列表 POST 形态", questsHandler(dep))
	// P0 批（2026-09-28）：任务页加建按钮读面。客户端
	// CurrentQuestButtonParts.cs:56-73 发 ?quest_id=，回调按
	// data.current_quest_additional_option.parts 建 UI——与
	// quest_select_option 同源下发（questSelectOptionDoc）。
	register(http.MethodGet, "/quests/current_quest_additional_option",
		"CN 3.5.0 任务附加选项（?quest_id=N → QuestSelect option 文档）",
		currentQuestAdditionalOptionHandler(dep))
	register(http.MethodPost, "/quests/current_quest_additional_option",
		"CN 3.5.0 任务附加选项 POST 形态",
		currentQuestAdditionalOptionHandler(dep))

	// 2026-09-28 故事阅读（系统→故事→阅读故事；设计真源
	// notes/proposed/2026-09-28-cn35-story-reader.md）。
	// /chapters/ 取代 module_requiredata.go 原空态；章节行
	// chapter_select_option 经深合并下发 Stories 页 requireData/data_key；
	// /stories/read/ 前缀路由原样下发 APK 内置剧本原文（无 data 信封）。
	// catalog 启动时加载校验：失败话只进 diagnostics 不下发；
	// fixture 缺失/全剔时 /chapters/ 回落空列表，不阻止启动。
	storyCat := loadStoryCatalog(dep.AssetsDir, acceptedAssetNames(dep))
	if len(storyCat.diagnostics) > 0 {
		dep.log().Warn("CN3.5 故事目录校验剔除",
			"loaded", len(storyCat.stories),
			"rejected", len(storyCat.diagnostics),
			"first_reason", storyCat.diagnostics[0].Reason)
	}
	register(http.MethodGet, "/chapters/",
		"CN 3.5.0 章节目录（Chapters requireData.chapters；故事阅读入口）",
		chaptersHandler(dep, storyCat))
	register(http.MethodPost, "/chapters/",
		"CN 3.5.0 章节目录 POST 形态", chaptersHandler(dep, storyCat))
	register(http.MethodGet, "/stories/list",
		"CN 3.5.0 章节故事列表（?chapter_id=N → stories_N；Chapters option 注入）",
		storiesListHandler(dep, storyCat))
	register(http.MethodPost, "/stories/list",
		"CN 3.5.0 章节故事列表 POST 形态", storiesListHandler(dep, storyCat))
	registerPrefix(http.MethodGet, "/stories/read/",
		"CN 3.5.0 剧本读取（/stories/read/<id>.json；原文下发无 data 信封）",
		storyReadHandler(dep, storyCat))
	registerPrefix(http.MethodPost, "/stories/read/",
		"CN 3.5.0 剧本读取 POST 形态", storyReadHandler(dep, storyCat))

	// STORY350-07 正式剧情战斗写链：execute→ap_use→result/lose→continue
	// + log_battle 归档。会话态/扣点/结算全在 cn35store 事务里（0007）。
	if dep.Battles != nil {
		if dep.Catalog == nil {
			dep.log().Warn("CN3.5 Battles 装配但 Catalog 缺失：战斗端点跳过注册")
		} else if cat, err := loadQuestCatalog(dep.AssetsDir); err != nil {
			dep.log().Warn("CN3.5 关卡目录不可用，战斗端点跳过注册", "err", err)
		} else if err := cat.validateBattleRefs(dep.Catalog); err != nil {
			dep.log().Warn("CN3.5 关卡 battle 段引用校验失败，战斗端点跳过注册", "err", err)
		} else {
			register(http.MethodGet, "/quests/use_ap",
				"CN 3.5.0 AP 扣点（execute 下发 URL，会话幂等）", questUseAPHandler(dep))
			register(http.MethodPost, "/quests/use_ap",
				"CN 3.5.0 AP 扣点 POST 形态", questUseAPHandler(dep))
			register(http.MethodGet, "/quests/result",
				"CN 3.5.0 战斗结算（success/already_received 幂等）", questResultHandler(dep))
			register(http.MethodPost, "/quests/result",
				"CN 3.5.0 战斗结算 POST 形态", questResultHandler(dep))
			register(http.MethodGet, "/quests/lose",
				"CN 3.5.0 败北上报（会话转 lost+首败标志）", questLoseHandler(dep))
			register(http.MethodPost, "/quests/lose",
				"CN 3.5.0 败北上报 POST 形态", questLoseHandler(dep))
			register(http.MethodGet, "/quests/continue",
				"CN 3.5.0 续战确认（扣 1 钻，data.user_coin）", questContinueHandler(dep))
			register(http.MethodPost, "/quests/continue",
				"CN 3.5.0 续战确认 POST 形态", questContinueHandler(dep))
			register(http.MethodGet, "/quests/log_battle",
				"CN 3.5.0 战斗日志归档（battle_record）", questLogBattleHandler(dep))
			register(http.MethodPost, "/quests/log_battle",
				"CN 3.5.0 战斗日志归档 POST 形态", questLogBattleHandler(dep))
			registerPrefix(http.MethodGet, "/quests/execute/",
				"CN 3.5.0 剧情战斗 execute（动态 id 路径）",
				questExecuteHandler(dep))
			registerPrefix(http.MethodPost, "/quests/execute/",
				"CN 3.5.0 剧情战斗 execute POST 形态",
				questExecuteHandler(dep))
		}
	} else {
		dep.log().Warn("CN3.5 未装配 Battles（跳过战斗端点注册）")
	}

	// static 通道：契约 20。客户端 _isStaticFile 先试 zlib 再按明文 ——
	// 本实现下发明文（asset_info 为 @ 文本格式，app_conf 经 RenderWire）。
	// 装配远端叠加管理器时清单由快照出 body（基线 + 远端 md5 行叠加）；
	// 管理器存在但尚未产出快照时回退 fixture 直读，保证接口可用。
	assetInfoH := staticFileHandler(dep, assetInfoFile, false)
	if dep.RemoteAssets != nil {
		assetInfoH = assetInfoHandler(dep)
	}
	register(http.MethodGet, "/asset_info_android4_v2_short.json.encode",
		"CN 3.5.0 资产清单（@文本格式；远端叠加后带 md5 行走 CDN）",
		assetInfoH)
	register(http.MethodGet, "/app_conf_android.json.encode",
		"CN 3.5.0 页面配置（RenderWire 剥注解出包）",
		staticFileHandler(dep, "serve_app_conf_android.json", true))

	registerPrefix(http.MethodGet, "/toto_image_4/unity/",
		"CN 3.5.0 unity3d CDN 兜底（内嵌缺失项）",
		bundleHandler(dep))

	register(http.MethodPost, "/cn35/codec-echo",
		"CN 3.5.0 编解码自检（verify 帧回显）", codecEchoHandler(dep))

	// PLAY350-04 诊断入口：证明 mercd35 实际装配了卡牌目录。
	if dep.Catalog != nil {
		register(http.MethodGet, "/cn35/catalog/info",
			"CN 3.5.0 卡牌目录诊断（版本/哈希/计数/质量警告汇总）",
			catalogInfoHandler(dep))
	} else {
		dep.log().Warn("CN3.5 未装配 cardcatalog（跳过 /cn35/catalog/info）")
	}

	// 远端资源叠加诊断：版本/条目/pending/rejected 只读快照（明文 JSON）。
	if dep.RemoteAssets != nil {
		register(http.MethodGet, "/cn35/assets/remote",
			"CN 3.5.0 远端资源叠加诊断（entries/pending/rejected/版本）",
			remoteAssetsInfoHandler(dep))
	}

	dep.log().Info("CN3.5 端点已注册", "routes", count,
		"server_host", dep.ServerHost, "assets_dir", dep.AssetsDir, "bundles_dir", dep.BundlesDir)
	return count
}
