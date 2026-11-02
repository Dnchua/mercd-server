// Package mobile 是 CN3.5 后端的 gomobile 入口：
//
//	gomobile bind -target=android -o mercd35.aar ./mobile
//
// 只暴露进程级生命周期接口（Start/Stop/Status/Version）；250+ 业务 API
// 仍走 127.0.0.1 HTTP，不逐一 bind。Android 侧调用形态：
//
//	Mobile.start(filesDir + "/state", filesDir + "/assets/cn350",
//	             filesDir + "/assets/shared/cards", 17666);
//
// assets/cards 目录由 Java/Kotlin 侧在首启时从 APK assets 解压到应用
// 私有目录后传入；SQLite 落在 <stateDir>/mercd35.sqlite3。
package mobile

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35app"
	"github.com/Dnchua/mercd-server/server/internal/config"
	"github.com/Dnchua/mercd-server/server/internal/database"
)

var (
	mu      sync.Mutex
	cancel  context.CancelFunc
	stopped chan struct{}
	state   = "stopped"
	lastErr string
)

// Start 启动 CN3.5 本地服务（127.0.0.1:<port>）。幂等：已运行时直接返回。
// stateDir/assetsDir/cardsDir 须为应用私有目录下的可读路径。
func Start(stateDir, assetsDir, cardsDir string, port int) error {
	mu.Lock()
	defer mu.Unlock()
	if cancel != nil {
		return nil // 已在运行
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("mobile: 非法端口 %d", port)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cfg := config.CN35Config{
		Addr:       addr,
		ServerHost: addr,
		AssetsDir:  assetsDir,
		CardsDir:   cardsDir,
		StateDir:   stateDir,
		// 移动端默认关取证落盘与周期扫描、不开管理面（无令牌即 503）。
		RequestLog:       "",
		EventLog:         "",
		RemoteAssetsScan: 0,
	}
	if err := cfg.ValidateCN35(); err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(logWriter{}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, c := context.WithCancel(context.Background())
	cancel = c
	stopped = make(chan struct{})
	state = "starting"
	go func() {
		err := cn35app.Run(ctx, cfg, log)
		mu.Lock()
		cancel = nil
		state = "stopped"
		if err != nil {
			lastErr = err.Error()
		}
		s := stopped
		mu.Unlock()
		close(s)
	}()
	go markReadyWhenListening(ctx, addr)
	lastErr = ""
	return nil
}

// markReadyWhenListening 轮询拨号，端口可连接后才把状态从 starting 抬到 running；
// 种子化/资源扫描期间端口未监听，调用方据此等待就绪。
func markReadyWhenListening(ctx context.Context, addr string) {
	for {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			mu.Lock()
			if state == "starting" {
				state = "running"
			}
			mu.Unlock()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// Stop 请求优雅停止；返回前等服务退出（上限 ~10s）。并发调用安全。
func Stop() {
	mu.Lock()
	c := cancel
	s := stopped
	mu.Unlock()
	if c == nil {
		return
	}
	c()
	select {
	case <-s:
	case <-time.After(10 * time.Second):
	}
}

// Status 返回 "stopped" / "starting" / "running"。
func Status() string {
	mu.Lock()
	defer mu.Unlock()
	return state
}

// LastError 返回最近一次 Run 退出的错误（空串=无）。
func LastError() string {
	mu.Lock()
	defer mu.Unlock()
	return lastErr
}

// Version 返回嵌入的构建标识（当前为模块语义版本占位）。
func Version() string {
	return "cn35-mobile-poc"
}

// DbSelfTest 在 stateDir 下做最小 SQLite 开库/写读自检，返回 "ok" 或错误串。
// 仅 PoC 诊断用。
func DbSelfTest(stateDir string) string {
	dbPath := stateDir + "/selftest.sqlite3"
	db, err := database.Open(dbPath)
	if err != nil {
		return "open: " + err.Error()
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS t(a INTEGER PRIMARY KEY, b TEXT)`); err != nil {
		return "create: " + err.Error()
	}
	if _, err := db.Exec(`INSERT INTO t(b) VALUES('x')`); err != nil {
		return "insert: " + err.Error()
	}
	var s string
	if err := db.QueryRow(`SELECT b FROM t LIMIT 1`).Scan(&s); err != nil {
		return "select: " + err.Error()
	}
	return "ok:" + s
}

// logWriter 把 slog 输出转到 Go 标准输出（Android 上落入 stdout，
// 经 logcat 可见；后续可换成 __android_log_write）。
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	return fmt.Print(string(p))
}
