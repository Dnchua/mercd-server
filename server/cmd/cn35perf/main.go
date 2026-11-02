// cn35perf 对现有 CN3.5 会话执行有界、只读的列表性能检测。
// 不登录、不迁移、不监听端口；本地处理器使用 mode=ro/query_only 数据库。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35protocol"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// 固定白名单，不能用此工具重放发奖、训练等写请求。
var paths = []string{"/user_units/", "/user_monsters/", "/albums/", "/monster_albums/"}

type distribution struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	Max float64 `json:"max_ms"`
}

func stats(a []float64) distribution {
	if len(a) == 0 {
		return distribution{}
	}
	a = append([]float64(nil), a...)
	sort.Float64s(a)
	return distribution{len(a), a[int(math.Ceil(float64(len(a))*.5))-1], a[int(math.Ceil(float64(len(a))*.95))-1], a[len(a)-1]}
}

type measurement struct {
	HTTP      distribution   `json:"http"`
	Decode    distribution   `json:"host_decode"`
	JSON      distribution   `json:"host_json_parse"`
	WireBytes int            `json:"wire_bytes"`
	JSONBytes int            `json:"json_bytes"`
	Rows      map[string]int `json:"rows"`
	TopFields []fieldSize    `json:"largest_fields"`
}
type fieldSize struct {
	Field string `json:"field"`
	Bytes int    `json:"value_bytes"`
}
type localMeasurement struct {
	measurement
	TemplateCalls  float64 `json:"template_queries_per_request"`
	TemplateMS     float64 `json:"template_queries_ms_per_request"`
	AllocatedBytes float64 `json:"allocated_bytes_per_request"`
}
type routeReport struct {
	Path   string             `json:"path"`
	Local  localMeasurement   `json:"local_handler"`
	Live   *measurement       `json:"live_http,omitempty"`
	Device *deviceMeasurement `json:"device_curl,omitempty"`
}

type deviceMeasurement struct {
	Total     distribution `json:"total"`
	TTFB      distribution `json:"ttfb"`
	WireBytes int          `json:"wire_bytes"`
}

// 在设备上用 curl 收完同一加密响应，排除 Unity 解析/渲染耗时。
// 请求凭据仅经过临时私有文件，完成后清理，不写入报告或进程参数。
func deviceMeasure(device, base, path string, s cn35wire.Session, n int) (deviceMeasurement, error) {
	var m deviceMeasurement
	dir, err := os.MkdirTemp("", "cn35perf-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(dir)
	remote := "/data/local/tmp/" + filepath.Base(dir)
	adb := func(args ...string) ([]byte, error) {
		return exec.Command("adb", append([]string{"-s", device}, args...)...).Output()
	}
	if _, err = adb("shell", "mkdir -m 700 "+remote); err != nil {
		return m, fmt.Errorf("设备临时目录失败: %w", err)
	}
	defer adb("shell", "rm -rf "+remote)
	r, err := request(base, path, s)
	if err != nil {
		return m, err
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return m, err
	}
	r.Body.Close()
	if err = os.WriteFile(filepath.Join(dir, "body"), body, 0600); err != nil {
		return m, err
	}
	var cfg strings.Builder
	fmt.Fprintf(&cfg, "url = %s\nrequest = \"POST\"\nmax-time = 15\nsilent\nshow-error\noutput = \"/dev/null\"\n", strconv.Quote(r.URL.String()))
	fmt.Fprintf(&cfg, "data-binary = %s\n", strconv.Quote("@"+remote+"/body"))
	for k, vs := range r.Header {
		for _, v := range vs {
			fmt.Fprintf(&cfg, "header = %s\n", strconv.Quote(k+": "+v))
		}
	}
	cfg.WriteString("write-out = \"%{http_code} %{time_starttransfer} %{time_total} %{size_download}\\n\"\n")
	if err = os.WriteFile(filepath.Join(dir, "request.cfg"), []byte(cfg.String()), 0600); err != nil {
		return m, err
	}
	for _, name := range []string{"body", "request.cfg"} {
		if _, err = adb("push", filepath.Join(dir, name), remote+"/"+name); err != nil {
			return m, fmt.Errorf("设备临时请求传输失败: %w", err)
		}
	}
	var total, ttfb []float64
	for i := -1; i < n; i++ {
		raw, err := adb("shell", "curl --config "+remote+"/request.cfg")
		if err != nil {
			return m, fmt.Errorf("设备 curl 失败: %w", err)
		}
		var status, size int
		var first, elapsed float64
		if _, err = fmt.Sscanf(string(raw), "%d %f %f %d", &status, &first, &elapsed, &size); err != nil || status != 200 {
			return m, fmt.Errorf("设备 curl 响应异常 HTTP %d: %v", status, err)
		}
		m.WireBytes = size
		if i >= 0 {
			total = append(total, elapsed*1000)
			ttfb = append(ttfb, first*1000)
		}
	}
	m.Total, m.TTFB = stats(total), stats(ttfb)
	return m, nil
}

type templateMeter struct {
	cn35game.UnitTemplateReader
	calls   int
	elapsed time.Duration
}

func (m *templateMeter) UnitTemplate(id int) (*cn35game.UnitTemplate, bool, error) {
	start := time.Now()
	t, ok, err := m.UnitTemplateReader.UnitTemplate(id)
	m.calls++
	m.elapsed += time.Since(start)
	return t, ok, err
}

func request(base, path string, s cn35wire.Session) (*http.Request, error) {
	t := fmt.Sprint(time.Now().Unix())
	body, err := cn35protocol.EncryptFormBody("_method=GET", t, s.S0, s.S1)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	id, err := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, s.UserID)
	if err != nil {
		return nil, err
	}
	sk, err := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, s.SK)
	if err != nil {
		return nil, err
	}
	appT, err := cn35protocol.EncryptAPP_T(t, s.S0, s.S1)
	if err != nil {
		return nil, err
	}
	r.Header.Set("APP_ID_1", id)
	r.Header.Set("APP_ID_4", sk)
	r.Header.Set("APP_T", appT)
	r.Header.Set("Encrypted", "true")
	r.Header.Set("Content-Type", "application/octet-stream")
	return r, nil
}

func inspect(raw []byte, s cn35wire.Session, m *measurement) (float64, float64, error) {
	start := time.Now()
	plain, err := cn35protocol.DecodeGameResponse(raw, s.S0, s.S1)
	decode := float64(time.Since(start).Nanoseconds()) / 1e6
	if err != nil {
		return 0, 0, fmt.Errorf("响应解密失败（会话可能已轮换）: %w", err)
	}
	var doc struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	start = time.Now()
	err = json.Unmarshal(plain, &doc)
	parse := float64(time.Since(start).Nanoseconds()) / 1e6
	if err != nil || doc.Data == nil {
		return 0, 0, fmt.Errorf("响应缺少 data 或 JSON 非法: %v", err)
	}
	m.WireBytes, m.JSONBytes = len(raw), len(plain)
	if m.Rows == nil {
		m.Rows = map[string]int{}
		fields := map[string]int{}
		for key, val := range doc.Data {
			if len(val) == 0 || val[0] != '[' {
				continue
			}
			var rows []json.RawMessage
			if err := json.Unmarshal(val, &rows); err != nil {
				return 0, 0, err
			}
			m.Rows[key] = len(rows)
			for _, row := range rows {
				var f map[string]json.RawMessage
				if json.Unmarshal(row, &f) == nil {
					for k, v := range f {
						fields[key+"."+k] += len(v)
					}
				}
			}
		}
		for k, n := range fields {
			m.TopFields = append(m.TopFields, fieldSize{k, n})
		}
		sort.Slice(m.TopFields, func(i, j int) bool {
			if m.TopFields[i].Bytes == m.TopFields[j].Bytes {
				return m.TopFields[i].Field < m.TopFields[j].Field
			}
			return m.TopFields[i].Bytes > m.TopFields[j].Bytes
		})
		if len(m.TopFields) > 10 {
			m.TopFields = m.TopFields[:10]
		}
	}
	return decode, parse, nil
}

func measure(n int, s cn35wire.Session, call func() ([]byte, error)) (measurement, error) {
	var m measurement
	var httpMS, decodeMS, jsonMS []float64
	for i := -1; i < n; i++ { // 一次预热，单独排除
		start := time.Now()
		raw, err := call()
		elapsed := float64(time.Since(start).Nanoseconds()) / 1e6
		if err != nil {
			return m, err
		}
		d, j, err := inspect(raw, s, &m)
		if err != nil {
			return m, err
		}
		if i >= 0 {
			httpMS = append(httpMS, elapsed)
			decodeMS = append(decodeMS, d)
			jsonMS = append(jsonMS, j)
		}
	}
	m.HTTP, m.Decode, m.JSON = stats(httpMS), stats(decodeMS), stats(jsonMS)
	return m, nil
}

func run() error {
	dbPath := flag.String("db", "../state/cn35/mercd35.sqlite3", "现有 SQLite（只读，不迁移）")
	base := flag.String("base", "", "运行中服务地址；留空只测本地处理器")
	uid := flag.String("user", "", "多会话时显式选择 user_id（不会写入报告）")
	cards := flag.String("cards", "assets/shared/cards", "卡牌目录")
	assets := flag.String("assets", "assets/cn350", "配置目录")
	n := flag.Int("n", 6, "每个接口采样数（1..30，另有一次预热）")
	out := flag.String("out", "", "不含凭据与响应原文的 JSON 报告")
	profile := flag.String("cpuprofile", "", "仅此诊断进程的 CPU profile")
	device := flag.String("device", "", "可选 ADB serial：设备 curl 重放相同只读请求")
	deviceBase := flag.String("device-base", "", "设备可达服务地址（使用 -device 时必填）")
	flag.Parse()
	if *n < 1 || *n > 30 {
		return fmt.Errorf("n 必须为 1..30")
	}
	if *device != "" && *deviceBase == "" {
		return fmt.Errorf("-device 必须同时指定 -device-base")
	}
	st, err := cn35store.OpenReadOnly(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if *uid == "" {
		var count int
		if err := st.DB().QueryRow("SELECT count(*) FROM cn35_sessions").Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("现有会话数 %d，请用 -user 选择；本工具不建号", count)
		}
		if err := st.DB().QueryRow("SELECT user_id FROM cn35_sessions LIMIT 1").Scan(uid); err != nil {
			return err
		}
	}
	s, err := st.Load(*uid)
	if err != nil {
		return err
	}
	cat, err := cardcatalog.Load(*cards)
	if err != nil {
		return err
	}
	meter := &templateMeter{UnitTemplateReader: st}
	reg := httpapi.NewRegistry()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cn35game.Register(reg, cn35game.Deps{Log: log, Accounts: st, Sessions: st, Inventory: st,
		Catalog: cn35game.NewRosterCatalog(cat, meter), Decks: st, Skills: st,
		AssetsDir: *assets, ServerHost: "127.0.0.1:18091"})
	srv, err := httpapi.NewServer(httpapi.Options{}, reg, log)
	if err != nil {
		return err
	}
	defer srv.Close()
	if *profile != "" {
		f, err := os.Create(*profile)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	report := struct {
		Time    string        `json:"time"`
		Runtime string        `json:"runtime"`
		Notes   []string      `json:"notes"`
		Routes  []routeReport `json:"routes"`
	}{Time: time.Now().Format(time.RFC3339), Runtime: runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
		Notes: []string{"只读现有会话；无迁移、登录、监听和业务写入", "local_handler 为当前源码进程内处理器；live_http 为运行中二进制端到端；二者可能版本不同", "host_decode/host_json_parse 在宿主机测量，不能代表 Unity 解析速度；CPU profile 属于诊断进程，包含诊断解析开销", "单请求串行，预热不计入分位数；模板查询与分配统计包含预热；n 较小时 p95 接近 max", "device_curl 为设备网络+服务器+下载耗时，不包含 Unity 数据处理；报告不保存会话凭据或响应原文"}}
	for _, path := range paths {
		calls0, dur0 := meter.calls, meter.elapsed
		var allocated uint64
		m, err := measure(*n, s, func() ([]byte, error) {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			r, err := request("http://local.invalid", path, s)
			if err != nil {
				return nil, err
			}
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
			runtime.ReadMemStats(&after)
			allocated += after.TotalAlloc - before.TotalAlloc
			if w.Code != 200 || w.Header().Get("ENCRYPTED") != "true" {
				return nil, fmt.Errorf("%s 本地响应异常 HTTP %d", path, w.Code)
			}
			return w.Body.Bytes(), nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		rr := routeReport{Path: path, Local: localMeasurement{measurement: m,
			TemplateCalls:  float64(meter.calls-calls0) / float64(*n+1),
			TemplateMS:     float64((meter.elapsed - dur0).Nanoseconds()) / 1e6 / float64(*n+1),
			AllocatedBytes: float64(allocated) / float64(*n+1)}}
		if *base != "" {
			live, err := measure(*n, s, func() ([]byte, error) {
				r, err := request(*base, path, s)
				if err != nil {
					return nil, err
				}
				resp, err := client.Do(r)
				if err != nil {
					return nil, err
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 || resp.Header.Get("ENCRYPTED") != "true" {
					return nil, fmt.Errorf("%s 线上响应异常 HTTP %d", path, resp.StatusCode)
				}
				return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
			})
			if err != nil {
				return err
			}
			rr.Live = &live
		}
		if *device != "" {
			m, err := deviceMeasure(*device, *deviceBase, path, s, *n)
			if err != nil {
				return err
			}
			rr.Device = &m
		}
		report.Routes = append(report.Routes, rr)
		fmt.Fprintf(os.Stderr, "%s local p50=%.1fms p95=%.1fms wire=%d JSON=%d templates=%.0f\n", path, m.HTTP.P50, m.HTTP.P95, m.WireBytes, m.JSONBytes, rr.Local.TemplateCalls)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if *out != "" {
		if err := os.MkdirAll(filepath.Dir(*out), 0700); err != nil {
			return err
		}
		return os.WriteFile(*out, append(data, '\n'), 0600)
	}
	_, err = os.Stdout.Write(append(data, '\n'))
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
