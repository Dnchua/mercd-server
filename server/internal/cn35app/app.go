// Package cn35app 承载 CN3.5 mercd35 的服务装配与运行链：
// cmd/mercd35（桌面/服务器入口）与 mobile（gomobile AAR 入口）共用同一套
// 启动逻辑，只在外层做参数收集与生命周期控制。
//
// 启动链覆盖（contracts/implementation-map §最小必经面）：
//
//	verify  /account（metaInfo/登录组/getServerList/chooseServer，S0 播种）
//	game    /users/version_info、/users/preset_data.json（会话密钥通道）
//	static  asset_info/app_conf fixture + /toto_image_4/unity/ 兜底
//
// 账号与会话当前为进程内受控桩（MemoryAccounts/MemoryStore）：重启丢号
// 属预期；持久化由 CN350-09 的 SQLite 实现接管。
package cn35app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/apidoc"
	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35roster"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/config"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/ops"
)

// Run 装配全部 CN3.5 路由并开始监听，直到 ctx 取消或监听失败。
// cfg 须已通过 ValidateCN35。
func Run(ctx context.Context, cfg config.CN35Config, log *slog.Logger) error {
	// PLAY350-04 卡牌目录：启动即加载+SHA256 校验；失败清楚报错，不静默回退。
	catalog, err := cardcatalog.Load(cfg.CardsDir)
	if err != nil {
		return fmt.Errorf("卡牌目录加载失败（-cards %s）: %w", cfg.CardsDir, err)
	}
	cmeta := catalog.Meta()
	log.Info("CN3.5 卡牌目录已加载", "dir", cmeta.Dir,
		"units", cmeta.UnitCount, "monsters", cmeta.MonsterCount,
		"units_version", cmeta.UnitsVersion, "monsters_version", cmeta.MonstersVersion,
		"warn", len(catalog.Report().Warn))

	// 远端资源叠加：启动先扫描一次（版本/digest 落盘在 StateDir）；
	// 周期扫描 goroutine 在 signal ctx 建立后启动。
	remoteAssets := cn35game.NewRemoteAssets(cfg.RemoteAssetsDir, cfg.AssetsDir, cfg.StateDir, log)
	if err := remoteAssets.Scan(); err != nil {
		log.Warn("远端资源首次扫描失败，asset_info 暂回退基线", "err", err)
	}

	reg := httpapi.NewRegistry()
	var accounts cn35game.AccountStore
	var sessions cn35wire.SessionStore
	var inventory cn35game.InventoryStore
	var decks cn35game.DeckStore
	var quests cn35game.QuestStore
	var battles cn35game.BattleStore
	var training cn35game.UnitTrainingStore
	var messages cn35game.MessageStore             // CN35-UI-RESTORE：底部消息条内容池（只读）
	var gacha cn35game.GachaStore                  // GACHA350-03/05：抽卡保底/操作/流水（0012）
	var gachaCfg cn35game.GachaConfigStore         // ROSTER350-03：已发布卡池配置读面
	var presents cn35game.PresentStore             // LEGACY-ASSETS-02：待领礼物池（0015）
	var infos cn35game.InfoStore                   // LEGACY-ASSETS-02：公告池（0015）
	var unitOps cn35game.UnitOpsStore              // P0 批：同伴 leader/helper/protect（0016）
	var monsterOps cn35game.MonsterOpsStore        // P0 批：魔宠 on/off/protect
	var presentRecv cn35game.PresentReceiveStore   // P0 批：礼物领取写面
	var items cn35game.ItemStore                   // ITEM350-02：物品目录/库存（0017）
	var resources cn35game.ResourceConfigReadStore // ITEM350-03：资源副本生效配置
	var medal cn35game.MedalStore                  // ITEM350-07：徽章商品/兑换
	var awaken cn35game.AwakenStore                // ITEM350-09：觉醒书消费
	var itemUse cn35game.ItemUseStore              // 物品使用写面（药水回体力）
	var skills cn35game.SkillStore                 // RUNE350-01：符文系统
	var tutorialTasks cn35game.TutorialTaskStore   // TASK350：新手任务读/写面（0019）
	var guild cn35game.GuildStore                  // GUILD350-02..08：公会/公会战（0027）
	var guildClock func() time.Time                // 会战判定时钟（默认 nil=time.Now）
	var dailyShop cn35game.DailyShopStore          // SHOP350-01：每日商店 0 元购（0031）
	var loginBonus cn35game.SpringStore            // LOGIN350-02：不可思议之泉读/领面（0035）
	var signin cn35game.SigninStore                // LOGIN350-02：月签到读/领面（0035）
	var adventure cn35game.AdventureStore          // LOGIN350-03：登录冒险读/发面（0036）
	var store *cn35store.Store                     // ROSTER350-03：名册/卡池治理同库
	if cfg.ListRoutes || cfg.MemMode {
		accounts, sessions = cn35game.NewMemoryAccounts(), cn35wire.NewMemoryStore()
		quests = cn35game.NewMemoryQuests()
		battles = cn35game.NewMemoryBattles()
	} else {
		st, err := cn35store.Open(filepath.Join(cfg.StateDir, "mercd35.sqlite3"))
		if err != nil {
			return fmt.Errorf("打开 CN3.5 运行库失败: %w", err)
		}
		defer st.Close()
		store = st
		gachaCfg = st
		accounts, sessions, inventory, decks, quests = st, st, st, st, st
		battles = st       // STORY350-07：战斗会话/结算写面同库（0007）
		training = st      // STAT350-05A：同伴训练写面（只改 level）
		messages = st      // CN35-UI-RESTORE：cn35_messages 内容池随机抽样
		gacha = st         // GACHA350-03：抽卡持久化与原子结算（0012）
		presents = st      // LEGACY-ASSETS-02：/user_presents/ 内容池（0015）
		infos = st         // LEGACY-ASSETS-02：/infos/index 公告池（0015）
		unitOps = st       // P0 批：同伴写面（2026-09-28 Note）
		monsterOps = st    // P0 批：魔宠写面
		presentRecv = st   // P0 批：礼物领取
		items = st         // ITEM350-02：物品目录/库存
		resources = st     // ITEM350-03：资源副本生效配置
		medal = st         // ITEM350-07：徽章商品/兑换
		awaken = st        // ITEM350-09：觉醒书消费
		itemUse = st       // 物品使用写面（药水回体力）
		skills = st        // RUNE350-01：符文库存/装备/强化/出售（0025）
		tutorialTasks = st // TASK350：新手任务（进度推导+领取同事务）
		guild = st         // GUILD350：公会组织/会战（0027 表族）
		dailyShop = st     // SHOP350-01：每日商店领取台账（0031）
		loginBonus = st    // LOGIN350-02：泉水累积/领取台账（0035）
		signin = st        // LOGIN350-02：月签到台账（0035）
		adventure = st     // LOGIN350-03：登录冒险轮次/位图/发放台账（0036）

		// GUILDLIFE350-13：验收时钟偏移（默认 0）——只影响会战判定 now。
		if cfg.GuildClockOffset != 0 {
			offset := cfg.GuildClockOffset
			st.SetClock(func() time.Time { return time.Now().Add(offset) })
			guildClock = func() time.Time { return time.Now().Add(offset) }
		}

		// GUILDLIFE350-04：单机 NPC 公会域。启动时把进程配置（开关/配额）
		// 落库并做一次有界补池；关闭开关只停止补池/推荐，不删既有数据。
		if err := st.ConfigureGuildEconomy(cfg.GuildDailyRefill, cfg.GuildSeasonRewards); err != nil {
			return err
		}
		if err := st.ConfigureGuildSolo(cn35store.GuildSoloSettings{
			Enabled:        cfg.GuildSolo,
			CandidateCount: cfg.GuildSoloCandidates,
			OpponentCount:  cfg.GuildSoloOpponents,
			MaxGuilds:      cfg.GuildSoloMaxGuilds,
		}); err != nil {
			return fmt.Errorf("单机 NPC 公会池初始化失败: %w", err)
		}

		// ROSTER350-02/03：首次启动种子化名册+双池配置。无清单时明确告警，
		// 运行期抽卡返回"未发布配置"而非静默使用静态表。
		if err := seedGachaRoster(cfg, st, log); err != nil {
			return fmt.Errorf("名册/卡池种子化失败: %w", err)
		}
		// ITEM350-03：首次启动把 0017 草稿发布为生效版本（无发布才做；
		// 管理员之后经 admin 改草稿/发布/回滚，种子不再介入）。
		if active, err := st.ActiveResourceConfig(); err != nil {
			return fmt.Errorf("资源配置读取失败: %w", err)
		} else if active == nil {
			v, digest, err := st.PublishResourceConfig("seed", "user-design-2026-09-30 首版发布",
				func(monsterID int) error { _, err := catalog.Monster(monsterID); return err })
			if err != nil {
				return fmt.Errorf("资源配置首发失败: %w", err)
			}
			log.Info("资源副本/徽章商品首版已发布", "version", v, "digest", digest)
		} else {
			// 0025/0030 迁移前的存量库：激活版本先于新域草稿发布，草稿只
			// 快照挂接不自动回灌——缺对应域时发布一次最新草稿。
			// 防回滚失效：补发由 migrate-* 作者行标记，已补发过就不再发
			// （管理员回滚/删域发布的旧版不受重启覆盖）。
			migrated, err := st.ResourceMigrationMarkers()
			if err != nil {
				return fmt.Errorf("补发标记读取失败: %w", err)
			}
			hasRuneGoods := false
			for _, g := range active.Goods {
				if strings.HasPrefix(g.GoodsKey, "medal_skill_") {
					hasRuneGoods = true
					break
				}
			}
			if !hasRuneGoods && !migrated["migrate-0025"] {
				v, digest, err := st.PublishResourceConfig("migrate-0025",
					"补发符文商品（0025 迁移前的存量配置）",
					func(monsterID int) error { _, err := catalog.Monster(monsterID); return err })
				if err != nil {
					return fmt.Errorf("符文商品补发失败: %w", err)
				}
				log.Info("符文商品已补发至激活配置", "version", v, "digest", digest)
			}
			// 0030 迁移前的存量库：激活版本缺符文本关时重发一版草稿
			// （rune_* 关卡键入快照才下发 902 tab）。
			hasRuneQuest := false
			for _, q := range active.Quests {
				if strings.HasPrefix(q.QuestKey, "rune_") {
					hasRuneQuest = true
					break
				}
			}
			if !hasRuneQuest && !migrated["migrate-0030"] {
				v, digest, err := st.PublishResourceConfig("migrate-0030",
					"补发符文本关卡（0030 迁移前的存量配置）",
					func(monsterID int) error { _, err := catalog.Monster(monsterID); return err })
				if err != nil {
					return fmt.Errorf("符文本补发失败: %w", err)
				}
				log.Info("符文本已补发至激活配置", "version", v, "digest", digest)
			}
			// 0033 迁移前的存量库：激活版本仍含 BP 药水时重发一版
			// （草稿行已被 0033 删除，重发即下架）。同样走 migrate-*
			// 标记防回滚覆盖。
			hasBPPotion := false
			for _, g := range active.Goods {
				if g.GoodsKey == "medal_potion_bp" {
					hasBPPotion = true
					break
				}
			}
			if hasBPPotion && !migrated["migrate-0033"] {
				// 只改已发布快照：不依赖管理员草稿，避免未完成草稿
				// 阻断升级启动。
				v, digest, found, err := st.DropActiveConfigGoods("migrate-0033",
					"下架 BP 药水（0033 迁移后的存量配置重发）",
					"medal_potion_bp")
				if err != nil {
					return fmt.Errorf("BP 药水下架重发失败: %w", err)
				}
				if found {
					log.Info("BP 药水已下架并重发配置", "version", v, "digest", digest)
				}
			}
		}
	}

	// ROSTER350-04 读面覆盖：DB 已发布模板优先于静态卡牌目录
	// （inventory/unit详情/战斗数值同源）；模板缺失行回退静态目录。
	unitCatalog := cn35game.UnitCatalog(catalog)
	if store != nil {
		unitCatalog = cn35game.NewRosterCatalog(catalog, store)
	}
	routes := cn35game.Register(reg, cn35game.Deps{
		Log:             log,
		Accounts:        accounts,
		Sessions:        sessions,
		ServerHost:      cfg.ServerHost,
		AssetsDir:       cfg.AssetsDir,
		BundlesDir:      cfg.BundlesDir,
		Catalog:         unitCatalog,
		Inventory:       inventory,
		Decks:           decks,
		Quests:          quests,
		Battles:         battles,
		HardnessProfile: cfg.HardnessProfile,
		Training:        training,
		Messages:        messages,
		Presents:        presents,
		Infos:           infos,
		UnitOps:         unitOps,
		MonsterOps:      monsterOps,
		PresentReceive:  presentRecv,
		Items:           items,
		Resources:       resources,
		Medal:           medal,
		Awaken:          awaken,
		ItemUse:         itemUse,
		Skills:          skills,
		TutorialTasks:   tutorialTasks,
		Gacha:           gacha,
		Guild:           guild,
		GuildClock:      guildClock,
		DailyShop:       dailyShop,
		LoginBonus:      loginBonus,
		Signin:          signin,
		Adventure:       adventure,
		GachaConfig:     gachaCfg,
		RemoteAssets:    remoteAssets,
	})
	opsDep := ops.OpsDeps{Registry: reg, Started: time.Now(), AssetsDir: cfg.AssetsDir, StateDir: cfg.StateDir, ServerHost: cfg.ServerHost, Log: log}
	if store != nil {
		opsDep.Accounts = store.AccountCount
	}
	ops.RegisterOps(reg, opsDep)

	apidoc.Register(reg, "")
	log.Info("CN3.5 路由表构建完成", "routes", routes, "registered", reg.Len(),
		"server_host", cfg.ServerHost, "assets_dir", cfg.AssetsDir)
	if _, mem := accounts.(*cn35game.MemoryAccounts); mem {
		log.Warn("CN3.5 账号/会话为内存桩：重启丢号（-mem 模式）")
	} else {
		log.Info("CN3.5 账号/会话走 SQLite 持久化", "state", cfg.StateDir)
	}
	if cfg.HardnessProfile != "" {
		log.Warn("CN3.5 启用重建测试 hardness 表：表值为重建值，非原服数据；客户端外皮回退被关闭",
			"profile", cfg.HardnessProfile)
	}

	if cfg.ListRoutes {
		printRoutes(reg, cfg)
		return nil
	}

	srv, err := httpapi.NewServer(httpapi.Options{
		Addr:       cfg.Addr,
		RequestLog: cfg.RequestLog,
		EventLog:   cfg.EventLog,
		Verbose:    cfg.Verbose,
		// JP PC 客户端把 API 前缀编进 Host（http://host:port/api1），
		// 归一化 /apiNNN/ 前缀后按 CN 标准路由表查找。
		StripAPIPrefix: true,
	}, reg, log)
	if err != nil {
		return err
	}
	defer srv.Close()

	// 远端资源周期扫描随服务生命周期退出（0=仅启动时一次）。
	if cfg.RemoteAssetsScan > 0 {
		go remoteAssets.Run(ctx, cfg.RemoteAssetsScan)
	}

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", cfg.Addr, err)
	}
	server := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		log.Info("CN3.5 服务已启动", "addr", listener.Addr().String(), "routes", reg.Len())
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

// seedGachaRoster 首次启动时把名册清单+模板+双池配置种子化并发布
// （ROSTER350-02/03）。只在 cn35_gacha_pools 为空时执行；清单缺失仅告警，
// 抽卡执行面会明确返回"未发布配置"而非静默回退静态表。
func seedGachaRoster(cfg config.CN35Config, st *cn35store.Store, log *slog.Logger) error {
	pools, err := st.GachaPools()
	if err != nil {
		return err
	}
	if len(pools) > 0 {
		// 存量库：memorial 数值配置要件（2026-10-05 口径）生效前已种子化
		// 的库需要一次性重发成员名单。migrate-* 作者标记保证只补一次，
		// 管理员后续的草稿/回滚不被重启覆盖。
		return republishGachaMemorialFilter(st, log)
	}
	in, _, err := cn35roster.LoadImport(cfg.AssetsDir, cfg.CardsDir)
	if err != nil {
		log.Warn("名册清单不可用，跳过种子化；抽卡执行将返回未发布配置", "err", err)
		return nil
	}
	v, seeded, err := cn35game.SeedGachaRoster(st, in, "seed")
	if err != nil {
		return err
	}
	if seeded {
		log.Info("名册/卡池种子化完成", "version", v.Version, "digest", v.Digest)
	}
	return nil
}

// gachaMemorialMigrationActor 是 memorial 数值配置要件重发版本的作者标记。
// 出现在版本史中即视为已补发；编号取下一可用迁移序号（数据级补发，不占
// schema 迁移位，但与既有 migrate-XXXX 命名口径一致）。
const gachaMemorialMigrationActor = "migrate-0032"

// republishGachaMemorialFilter 对存量库执行一次性成员名单重发：单事务内
// 把所有池的现有成员按 memorial 数值配置要件过滤后重发新版本——只剔除
// 不合格单位，管理员自定义成员/权重/池配置全部保留（不用种子名单覆盖）。
// 跳过条件（任一）：作者标记已在版本史中（补发过），或激活版本成员已
// 全部合格——后者不写标记也不动管理员草稿。
func republishGachaMemorialFilter(st *cn35store.Store, log *slog.Logger) error {
	migrated, err := st.GachaMigrationMarkers()
	if err != nil {
		return fmt.Errorf("卡池补发标记读取失败: %w", err)
	}
	if migrated[gachaMemorialMigrationActor] {
		return nil
	}
	v, done, err := st.RepublishGachaFiltered(gachaMemorialMigrationActor,
		"memorial 数值配置要件重发（六族素材+unit_memorial 双要件）",
		cn35game.MemorialUnitConfigured)
	if err != nil {
		return fmt.Errorf("memorial 卡池重发失败: %w", err)
	}
	if done {
		log.Info("卡池已按 memorial 数值配置要件重发", "version", v.Version, "digest", v.Digest)
	}
	return nil
}

func printRoutes(reg *httpapi.Registry, cfg config.CN35Config) {
	all := reg.Routes()
	sort.Slice(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Method < all[j].Method
	})
	fmt.Printf("CN3.5 本地服务\nAPI/账号: %s\n业务地址: %s\n配置目录: %s\n状态目录: %s\n\n路由 (%d):\n",
		cfg.Addr, cfg.ServerHost, cfg.AssetsDir, cfg.StateDir, len(all))
	for _, route := range all {
		fmt.Printf("%-6s %-52s %-10s %s\n", route.Method, route.Path, route.Status.String(), route.Note)
	}
}
