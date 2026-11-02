// cn35.go 国服 3.5.0 mercd35 的运行配置。
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultCN35Addr cn35 监听地址。
	DefaultCN35Addr = "0.0.0.0:18091"
	// DefaultCN35ServerHost 回给客户端的 serverIP/assetInfoServerIp/staticHostIp。
	DefaultCN35ServerHost = "127.0.0.1:18091"
	// DefaultCN35AssetsDir cn350 fixture 目录（server/ 下相对路径；仓库根运行
	// 时经 resolveAssetsDir 兜底到 server/assets/cn350）。
	DefaultCN35AssetsDir = "assets/cn350"
	// DefaultCN35CardsDir 卡牌数值快照目录（版本无关共享目录；仓库根
	// 运行时兜底 server/assets/shared/cards）。缺失即启动失败，不回退固定值。
	DefaultCN35CardsDir = "assets/shared/cards"
	// DefaultCN35StateDir cn35 独立运行态目录（09 起存放 SQLite/会话）。
	DefaultCN35StateDir = "server-state-cn35"
)

// CN35Config mercd35 的运行配置。
type CN35Config struct {
	EnvFile    string // 环境文件；配置优先级为显式 flag、进程环境、文件、默认值。
	Addr       string
	ServerHost string
	AssetsDir  string
	CardsDir   string // 卡牌数值快照目录（cardcatalog.Load 加载）
	StateDir   string
	BundlesDir string // unity3d CDN 兜底目录；空=仅取 assets/bundles（不存在则 404）
	RequestLog string
	EventLog   string
	Verbose    bool
	ListRoutes bool
	MemMode    bool // 内存账号/会话桩（联调/测试；生产勿用）
	// HardnessProfile 非空时启用「重建测试 profile」的 hardness 表下发
	// （STAT350-03B）。默认空=不下发，保留客户端回退。取值为文件名（相对
	// AssetsDir）或绝对路径。表值一律为重建值，非原服数据。
	HardnessProfile string
	// RemoteAssetsDir 远端叠加资源目录（运营者直接解压 rar/文件进去，
	// 布局同 client/decoded/assets/unity 逻辑名）。空=<state>/remote_assets。
	RemoteAssetsDir string
	// RemoteAssetsScan 远端目录扫描周期；0=仅启动时扫描一次。
	RemoteAssetsScan time.Duration

	// GuildSolo 单机 NPC 公会域开关（GUILDLIFE350-04）：开启时搜索/入会
	// 会维护「19/20 候选 + 稳定对手」持久池；关闭后停止补池与候选推荐，
	// 既有 NPC 公会仍可读/可退出（不删档）。
	GuildSolo          bool
	GuildDailyRefill   bool
	GuildSeasonRewards bool
	// GuildSoloCandidates 期望「仍有空位」的候选公会数（0..12）。
	GuildSoloCandidates int
	// GuildSoloOpponents 期望的持久对手公会数（0..6，公会战敌会来源）。
	GuildSoloOpponents int
	// GuildSoloMaxGuilds NPC 公会池总量上限（有界补充，1..60）。
	GuildSoloMaxGuilds int
	// GuildClockOffset 公会战判定时钟偏移（验收/排障用测试手段，默认 0=真实时钟）。
	// 只影响「时窗/日场/结算判定」用到的 now，不改写任何落库时间戳，
	// 也不改系统时间；用于在非 12:00-13:00 / 20:00-21:00 时段做实机会战验收。
	GuildClockOffset time.Duration
}

// BindCN35 注册 mercd35 的 flag 集。
func (c *CN35Config) BindCN35(fs *flag.FlagSet) {
	fs.StringVar(&c.EnvFile, "env-file", os.Getenv("MERCD_ENV_FILE"), "环境文件（空=探测当前目录 mercd35.env/.env；不执行文件内容）")
	fs.StringVar(&c.Addr, "addr", DefaultCN35Addr, "CN3.5 API/账号监听地址")
	fs.StringVar(&c.ServerHost, "server-host", DefaultCN35ServerHost, "回给客户端的业务/asset/static 服务器地址（LAN 可达 host:port）")
	fs.StringVar(&c.AssetsDir, "assets", DefaultCN35AssetsDir, "CN 3.5.0 fixture 目录")
	fs.StringVar(&c.CardsDir, "cards", DefaultCN35CardsDir, "同伴/魔宠数值快照目录（SHA256SUMS 校验；缺失即失败）")
	fs.StringVar(&c.StateDir, "state", DefaultCN35StateDir, "CN3.5 运行态目录（会话/账号持久化，09 接管）")
	fs.StringVar(&c.BundlesDir, "bundles", "", "unity3d CDN 兜底目录（空=assets/cn350/bundles）")
	fs.StringVar(&c.RequestLog, "reqlog", filepath.Join(DefaultCN35StateDir, "requests.ndjson"), "请求取证日志（空字符串关闭）")
	fs.StringVar(&c.EventLog, "event-log", filepath.Join(DefaultCN35StateDir, "events.ndjson"), "请求到达事件日志（空字符串关闭）")
	fs.BoolVar(&c.Verbose, "v", false, "输出请求摘要")
	fs.BoolVar(&c.ListRoutes, "list-routes", false, "列出 CN3.5 路由后退出")
	fs.BoolVar(&c.MemMode, "mem", false, "内存账号/会话桩（联调/测试用；生产勿用）")
	fs.StringVar(&c.HardnessProfile, "hardness-profile", "",
		"启用重建测试 hardness 表（文件相对 assets 或绝对路径；留空=不下发，保留客户端回退；表值为重建值非原服）")
	fs.StringVar(&c.RemoteAssetsDir, "remote-assets", "",
		"远端叠加资源目录（清单带 md5 行走 CDN 下发；空=<state>/remote_assets）")
	fs.DurationVar(&c.RemoteAssetsScan, "remote-assets-scan", 30*time.Second,
		"远端资源目录扫描周期（0=仅启动时扫描一次）")

	fs.BoolVar(&c.GuildDailyRefill, "guild-solo-daily-refill", false, "单机重建：每日首次访问补满蓄水库（非原服规则）")
	fs.BoolVar(&c.GuildSeasonRewards, "guild-solo-season-rewards", false, "单机重建：月结按贡献归属发奖（非原服规则）")
	fs.BoolVar(&c.GuildSolo, "guild-solo", true,
		"单机 NPC 公会域（19/20 候选公会 + 持久对手，免审加入；关闭=停止补池与推荐，保留既有存档）")
	fs.IntVar(&c.GuildSoloCandidates, "guild-solo-candidates", 3,
		"单机候选公会数（仍有 1 个真人空位的 19/20 公会；0..12）")
	fs.IntVar(&c.GuildSoloOpponents, "guild-solo-opponents", 3,
		"单机持久对手公会数（公会战敌会，0..6）")
	fs.IntVar(&c.GuildSoloMaxGuilds, "guild-solo-max", 24,
		"单机 NPC 公会池总量上限（有界补充，1..60）")
	fs.DurationVar(&c.GuildClockOffset, "guild-clock-offset", 0,
		"公会战判定时钟偏移（验收/排障测试手段；只影响时窗判定，默认 0）")
}

// ValidateCN35 校验并归一化配置。
func (c *CN35Config) ValidateCN35() error {
	if c.Addr == "" {
		return fmt.Errorf("config: addr 不能为空")
	}
	if c.ServerHost == "" {
		return fmt.Errorf("config: server-host 不能为空")
	}
	if err := validateLANHost(c.ServerHost, listenOnLoopback(c.Addr)); err != nil {
		return err
	}
	if c.AssetsDir == "" {
		return fmt.Errorf("config: assets 目录不能为空")
	}
	c.AssetsDir = resolveCN35AssetsDir(c.AssetsDir)
	if fi, err := os.Stat(c.AssetsDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("config: CN3.5 assets 目录不可用 %q: %v", c.AssetsDir, err)
	}
	if c.CardsDir == "" {
		return fmt.Errorf("config: cards 目录不能为空")
	}
	c.CardsDir = resolveCN35Dir(c.CardsDir, DefaultCN35CardsDir)
	if fi, err := os.Stat(c.CardsDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("config: CN3.5 cards 目录不可用 %q: %v", c.CardsDir, err)
	}
	if c.BundlesDir == "" {
		c.BundlesDir = resolveCN35BundlesDir(c.AssetsDir)
	}
	if c.StateDir == "" {
		return fmt.Errorf("config: state 目录不能为空")
	}
	if err := os.MkdirAll(c.StateDir, 0o755); err != nil {
		return fmt.Errorf("config: 无法创建 state 目录 %q: %w", c.StateDir, err)
	}
	if c.RemoteAssetsDir == "" {
		c.RemoteAssetsDir = filepath.Join(c.StateDir, "remote_assets")
	}
	if err := os.MkdirAll(c.RemoteAssetsDir, 0o755); err != nil {
		return fmt.Errorf("config: 无法创建远端资源目录 %q: %w", c.RemoteAssetsDir, err)
	}
	if c.RemoteAssetsScan < 0 {
		return fmt.Errorf("config: remote-assets-scan 不能为负: %s", c.RemoteAssetsScan)
	}
	return nil

}

func resolveCN35AssetsDir(path string) string {
	return resolveCN35Dir(path, DefaultCN35AssetsDir)
}

func resolveCN35BundlesDir(assetsDir string) string {
	def := filepath.Join(assetsDir, "bundles")
	if dirExists(def) {
		return def
	}
	for _, cand := range []string{
		filepath.Join("client", "decoded", "assets", "unity"),
		filepath.Join("..", "client", "decoded", "assets", "unity"),
	} {
		if dirExists(cand) {
			return cand
		}
	}
	return def
}

// resolveCN35Dir 默认相对目录兜底：从仓库根启动时回退到 server/<默认>。
func resolveCN35Dir(path, def string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if path == def {
		if alt := filepath.Join("server", path); dirExists(alt) {
			return alt
		}
	}
	return path
}
