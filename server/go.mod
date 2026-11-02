// Module mercd：《梅露可物语》国服 CN3.5 本地复原服务。
//
// 设计要点：
//   - SQLite 使用锁定版本的纯 Go 驱动，CGO_ENABLED=0 仍可交叉编译单二进制。
//     离线构建须先准备 module cache 或 vendor，见 docs/后端数据库与Go工作流.md。
//   - 协议实现保持可独立验证；客户端未确认的业务路由回 501 并记录脱敏请求，
//     不把猜测响应冒充成已验证协议。
module github.com/Dnchua/mercd-server/server

go 1.25.0

require (
	github.com/danielgtaylor/huma/v2 v2.39.1
	github.com/ncruces/go-sqlite3 v0.24.0
	github.com/swaggest/swgui v1.8.9
	modernc.org/sqlite v1.54.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.23 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/tetratelabs/wazero v1.9.0 // indirect
	github.com/vearutop/statigz v1.4.0 // indirect
	golang.org/x/mobile v0.0.0-20260813181013-1960c775504c // indirect
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	modernc.org/libc v1.74.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

tool golang.org/x/mobile/cmd/gobind
