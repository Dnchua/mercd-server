package config

import (
	"flag"
	"fmt"
	"os"
)

// ApplyCN35Env 在 Parse 后、ValidateCN35 前调用。只加载已列明的运行键；
// 显式命令行参数优先，其次进程环境、环境文件，最后保持 BindCN35 默认值。
// LookupEnv 保留显式空值，允许关闭日志；不将文件内容注入进程环境。
func (c *CN35Config) ApplyCN35Env(fs *flag.FlagSet) error {
	file, err := LoadEnvFile(c.EnvFile)
	if err != nil {
		return err
	}
	explicit := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	for _, binding := range []struct{ flag, env string }{
		{"addr", "MERCD_ADDR"},
		{"server-host", "MERCD_SERVER_HOST"},
		{"assets", "MERCD_ASSETS"},
		{"cards", "MERCD_CARDS"},
		{"state", "MERCD_STATE"},
		{"bundles", "MERCD_BUNDLES"},
		{"remote-assets", "MERCD_REMOTE_ASSETS"},
		{"remote-assets-scan", "MERCD_REMOTE_ASSETS_SCAN"},
		{"reqlog", "MERCD_REQUEST_LOG"},
		{"event-log", "MERCD_EVENT_LOG"},
		{"v", "MERCD_VERBOSE"},
		{"hardness-profile", "MERCD_HARDNESS_PROFILE"},
		{"raid-boss-id", "MERCD35_RAID_BOSS_ID"},
		{"raid-trigger-rate", "MERCD35_RAID_TRIGGER_RATE"},
		{"raid-ttl-sec", "MERCD35_RAID_TTL_SEC"},
		{"raid-solo-assist", "MERCD35_RAID_SOLO_ASSIST"},
		{"event-windows", "MERCD35_EVENT_WINDOWS"},
	} {
		if explicit[binding.flag] {
			continue
		}
		value, ok := os.LookupEnv(binding.env)
		if !ok {
			value, ok = file[binding.env]
		}
		if ok {
			if err := fs.Set(binding.flag, value); err != nil {
				return fmt.Errorf("config: %s 格式无效: %w", binding.env, err)
			}
		}
	}
	return nil
}
