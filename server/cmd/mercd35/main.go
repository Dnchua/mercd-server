// Command mercd35 服务国服 3.5.0 客户端（com.happyelements.miraclecn.uc
// versionCode 1316）的本地契约。协议常量、assets、state 全部独立；
// 另挂载无鉴权只读运维端点（/mercd/health 等）与内嵌管理后台 SPA
// （/mercd/admin/）。
//
// 本入口只做 flag 收集与进程生命周期；启动链本体在 internal/cn35app，
// 与 mobile（gomobile AAR 入口）共用。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Dnchua/mercd-server/server/internal/cn35app"
	"github.com/Dnchua/mercd-server/server/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mercd35:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("mercd35", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var cfg config.CN35Config
	cfg.BindCN35(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if err := cfg.ApplyCN35Env(fs); err != nil {
		return err
	}
	if err := cfg.ValidateCN35(); err != nil {
		return err
	}

	level := slog.LevelInfo
	if cfg.Verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return cn35app.Run(ctx, cfg, log)
}
