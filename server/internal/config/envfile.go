// envfile.go mercd35 的 env 环境文件加载（AUTHKEY 等密钥类配置落盘入口）。
//
// 格式为常见 .env 子集：KEY=VALUE 每行一条，# 开头为注释，容忍
// `export ` 前缀与成对单/双引号；不做变量展开与转义处理。同名键
// 进程环境变量优先——文件只补缺口，绝不覆盖外部已注入的值。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultEnvFiles 未显式指定 -env-file / MERC35_ENV_FILE 时按序探测的
// 候选文件（相对当前工作目录）；全部不存在视为未配置，不算错误。
var DefaultEnvFiles = []string{"mercd35.env", ".env"}

// LoadEnvFile 解析 env 文件为键值 map。path 为空按 DefaultEnvFiles
// 探测第一个存在的文件；显式路径不存在返回错误。
func LoadEnvFile(path string) (map[string]string, error) {
	if path == "" {
		for _, cand := range DefaultEnvFiles {
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				path = cand
				break
			}
		}
		if path == "" {
			return nil, nil
		}
	}
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("读取 env 文件 %q 失败: %w", path, err)
	}
	out := map[string]string{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("env 文件 %s 第 %d 行不是 KEY=VALUE", path, i+1)
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if q := val[0]; (q == '"' || q == '\'') && val[len(val)-1] == q {
				val = val[1 : len(val)-1]
			}
		}
		out[key] = val
	}
	return out, nil
}

// envValue 按「flag 值 > 进程环境变量 > env 文件」解析密钥类配置。
// flagVal 为 flag 包已解析值（非空即视为显式指定）；envName 为对应
// 环境变量名；file 为 LoadEnvFile 的结果（可为 nil）。
func envValue(flagVal, envName string, file map[string]string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv(envName); v != "" {
		return v
	}
	return file[envName]
}
