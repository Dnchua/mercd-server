// cn35load CN3.5 多玩家并发压测：以客户端同一条加密管线模拟 N 名玩家
// 登录（heQuickLogin→getServerList→chooseServer→version_info→preset_data）
// 后按权重执行常见玩法（Top 轮询、列表浏览、关卡战斗链、抽卡、礼物领取等）。
//
// 只做黑盒 HTTP 压测：会建新游客账号并产生真实写入，务必指向独立 state 的
// 压测实例（tools/cn35_loadtest.sh 会自动起一个），不要对玩家正式库运行。
//
//	go run ./cmd/cn35load -base http://127.0.0.1:18190 -players 200 -duration 3m
//	go run ./cmd/cn35load -base ... -players 200 -profile stress   # 无思考时间，测饱和吞吐
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35protocol"
)

// ---------------------------------------------------------------- 指标

type sample struct {
	route string
	ms    float64
	kind  int // 0=ok 1=业务失败(200 但 status=error 等) 2=错误(网络/非200/解密失败)
	at    time.Time
}

type recorder struct {
	mu      sync.Mutex
	samples []sample
	errs    map[string]int // 错误类别示例计数
	start   time.Time
}

func newRecorder() *recorder { return &recorder{errs: map[string]int{}, start: time.Now()} }

func (r *recorder) add(route string, d time.Duration, kind int, errKey string) {
	r.mu.Lock()
	r.samples = append(r.samples, sample{route, float64(d.Microseconds()) / 1000, kind, time.Now()})
	if errKey != "" {
		if len(errKey) > 160 {
			errKey = errKey[:160]
		}
		r.errs[route+" | "+errKey]++
	}
	r.mu.Unlock()
}

type dist struct {
	N        int     `json:"n"`
	OK       int     `json:"ok"`
	BizFail  int     `json:"biz_fail"`
	Errors   int     `json:"errors"`
	P50      float64 `json:"p50_ms"`
	P90      float64 `json:"p90_ms"`
	P95      float64 `json:"p95_ms"`
	P99      float64 `json:"p99_ms"`
	Max      float64 `json:"max_ms"`
	Mean     float64 `json:"mean_ms"`
	RPS      float64 `json:"rps"`
	ErrorPct float64 `json:"error_pct"`
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(float64(len(sorted))*p)) - 1
	if i < 0 {
		i = 0
	}
	return sorted[i]
}

func summarize(ss []sample, seconds float64) dist {
	var d dist
	lat := make([]float64, 0, len(ss))
	sum := 0.0
	for _, s := range ss {
		d.N++
		switch s.kind {
		case 0:
			d.OK++
		case 1:
			d.BizFail++
		default:
			d.Errors++
		}
		lat = append(lat, s.ms)
		sum += s.ms
	}
	if d.N == 0 {
		return d
	}
	sort.Float64s(lat)
	d.P50, d.P90, d.P95, d.P99, d.Max = pct(lat, .5), pct(lat, .9), pct(lat, .95), pct(lat, .99), lat[len(lat)-1]
	d.Mean = sum / float64(d.N)
	if seconds > 0 {
		d.RPS = float64(d.N) / seconds
	}
	d.ErrorPct = 100 * float64(d.Errors) / float64(d.N)
	return d
}

// ---------------------------------------------------------------- 协议客户端

type player struct {
	idx      int
	deviceID string
	uid, sk  string
	s0, s1   string
	rng      *mrand.Rand
	hasUnits bool
}

type client struct {
	base string
	hc   *http.Client
	rec  *recorder
}

func randStr(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// verify 发送 /account type3 请求；s1 非空时按 chooseServer 信封包装。
func (c *client) verify(ctx context.Context, route, biz, s1 string) (map[string]any, string, error) {
	st := "0"
	if s1 != "" {
		st = "1"
	}
	env := `[{"v":1316,"mv":"1","ct":2,"sk":"gip-eurhieur8387583979385798327958udfhdjf","aid":"11","lang":"zh_CN","pf":"uc","notify":true,"st":` + st + `},[` + biz + `]]`
	plain := env
	if s1 != "" {
		plain = `{"r":"` + s1 + `","m":"` + randStr(8) + `","data":` + env + `}`
	}
	wire, err := cn35protocol.EncodeAccountRequest([]byte(plain))
	if err != nil {
		return nil, "", err
	}
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/account?uid=666666", bytes.NewReader(wire))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.hc.Do(req)
	if err != nil {
		c.rec.add(route, time.Since(start), 2, "net: "+errShort(err))
		return nil, "", err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	elapsed := time.Since(start)
	if err != nil || resp.StatusCode != 200 {
		c.rec.add(route, elapsed, 2, fmt.Sprintf("http %d %v", resp.StatusCode, err))
		return nil, "", fmt.Errorf("%s http %d", route, resp.StatusCode)
	}
	payload, seeded, err := cn35protocol.DecodeAccountResponse(raw)
	if err != nil {
		c.rec.add(route, elapsed, 2, "decode: "+err.Error())
		return nil, "", err
	}
	var doc struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil || len(doc.Results) == 0 {
		c.rec.add(route, elapsed, 2, "results 缺失")
		return nil, "", errors.New(route + " results 缺失")
	}
	r := doc.Results[0]
	if ok, has := r["success"].(bool); has && !ok {
		c.rec.add(route, elapsed, 1, fmt.Sprint("success=false ", r["msg"]))
		return r, seeded, fmt.Errorf("%s success=false", route)
	}
	c.rec.add(route, elapsed, 0, "")
	return r, seeded, nil
}

// game 发送游戏通道请求（_method 表单 + ?e= 加密 query + 身份头），返回解密 JSON。
func (c *client) game(ctx context.Context, p *player, route, path, query, form string) (map[string]any, error) {
	if form == "" {
		form = "_method=GET"
	}
	t := strconv.FormatInt(time.Now().Unix(), 10)
	target := c.base + path
	if query != "" {
		e, err := cn35protocol.EncryptQuery(query, t, p.s0, p.s1)
		if err != nil {
			return nil, err
		}
		target += "?e=" + e
	}
	body, err := cn35protocol.EncryptFormBody(form, t, p.s0, p.s1)
	if err != nil {
		return nil, err
	}
	id1, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, p.uid)
	id4, _ := cn35protocol.EncryptIdentity(cn35protocol.IdentityDesKey, p.sk)
	appT, _ := cn35protocol.EncryptAPP_T(t, p.s0, p.s1)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("APP_ID_1", id1)
	req.Header.Set("APP_ID_4", id4)
	req.Header.Set("APP_T", appT)
	req.Header.Set("Encrypted", "true")
	req.Header.Set("Content-Type", "application/octet-stream")

	start := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err() // 压测结束时的取消不计入错误
		}
		c.rec.add(route, time.Since(start), 2, "net: "+errShort(err))
		return nil, err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	elapsed := time.Since(start)
	if err != nil || resp.StatusCode != 200 {
		snip := string(raw)
		if len(snip) > 80 {
			snip = snip[:80]
		}
		c.rec.add(route, elapsed, 2, fmt.Sprintf("http %d %s", resp.StatusCode, snip))
		return nil, fmt.Errorf("%s http %d", route, resp.StatusCode)
	}
	plain := raw
	if strings.EqualFold(resp.Header.Get("ENCRYPTED"), "true") {
		if plain, err = cn35protocol.DecodeGameResponse(raw, p.s0, p.s1); err != nil {
			c.rec.add(route, elapsed, 2, "decode: "+err.Error())
			return nil, err
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		c.rec.add(route, elapsed, 2, "json: "+err.Error())
		return nil, err
	}
	if s, _ := doc["status"].(string); s == "error" || s == "failed" || s == "fail" {
		c.rec.add(route, elapsed, 1, fmt.Sprint("status=", s, " ", firstNonNil(doc["message"], doc["msg"], doc["error"], doc["reason"])))
		return doc, nil
	}
	c.rec.add(route, elapsed, 0, "")
	return doc, nil
}

func firstNonNil(v ...any) any {
	for _, x := range v {
		if x != nil {
			return x
		}
	}
	return ""
}

func errShort(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i < 80 {
		return s[i+2:]
	}
	return s
}

// ---------------------------------------------------------------- 玩家行为

func (c *client) login(ctx context.Context, p *player) error {
	if _, _, err := c.verify(ctx, "account:metaInfo", `{"method":"metaInfo","metaInfo":"device=load"}`, ""); err != nil {
		return err
	}
	r, _, err := c.verify(ctx, "account:heQuickLogin", `{"method":"heQuickLogin","randomStr":"`+randStr(8)+`","deviceId":"`+p.deviceID+`"}`, "")
	if err != nil {
		return err
	}
	heID, _ := r["heAccountId"].(string)
	token, _ := r["token"].(string)
	if _, _, err = c.verify(ctx, "account:getServerList", `{"method":"getServerList","token":"`+token+`","heAccountId":"`+heID+`","deviceId":"`+p.deviceID+`","pf":"uc","clientVersion":"1316","b":"1","metaInfo2":"x"}`, ""); err != nil {
		return err
	}
	p.s1 = randStr(32)
	r, seeded, err := c.verify(ctx, "account:chooseServer", `{"method":"chooseServer","heAccountId":"`+heID+`","token":"`+token+`","deviceId":"`+p.deviceID+`","deviceName":"load","deviceBootTime":"1","serverId":"1"}`, p.s1)
	if err != nil {
		return err
	}
	p.uid, _ = r["userId"].(string)
	p.sk, _ = r["sk"].(string)
	p.s0 = seeded
	if p.uid == "" || len(p.s0) != 32 {
		return errors.New("chooseServer 未返回 userId/S0")
	}
	if _, err = c.game(ctx, p, "users/version_info", "/users/version_info", "v=1316", ""); err != nil {
		return err
	}
	_, err = c.game(ctx, p, "users/preset_data", "/users/preset_data.json", "", "")
	return err
}

// onboard 新号一次性准备：完成教程 + 全领新手礼物（获得同伴后才能开战）。
func (c *client) onboard(ctx context.Context, p *player) {
	name := url.QueryEscape(fmt.Sprintf("压测%04d%s", p.idx, randStr(4)))
	c.game(ctx, p, "users/update", "/users/update", "", "_method=POST&tutorial_finish=true&user_name="+name)
	c.game(ctx, p, "user_presents", "/user_presents/", "", "")
	c.game(ctx, p, "user_presents/receive_all", "/user_presents/receive/", "", "")
	c.game(ctx, p, "users/preset_data", "/users/preset_data.json", "", "")
}

type action struct {
	name   string
	weight int
	run    func(ctx context.Context, c *client, p *player)
}

func simple(route, path, query string) func(context.Context, *client, *player) {
	return func(ctx context.Context, c *client, p *player) { c.game(ctx, p, route, path, query, "") }
}

var actions = []action{
	{"battle", 25, func(ctx context.Context, c *client, p *player) {
		doc, err := c.game(ctx, p, "quests/execute", "/quests/execute/1.json", "mode=quest&id=1&difficulty_id=normal&party_id=001", "")
		if err != nil || doc == nil {
			return
		}
		apURL, _ := doc["ap_use_url"].(string)
		_, bid, ok := strings.Cut(apURL, "bid=")
		if !ok {
			// AP 不足等：按客户端行为用元气之水后放弃本轮
			c.game(ctx, p, "user_items/use", "/user_items/use/", "id=201&count=1", "")
			return
		}
		if i := strings.IndexByte(bid, '&'); i >= 0 {
			bid = bid[:i]
		}
		c.game(ctx, p, "quests/use_ap", "/quests/use_ap", "bid="+bid, "")
		sleepCtx(ctx, think(p, 300*time.Millisecond, 800*time.Millisecond)) // 模拟战斗中的停顿（缩短）
		c.game(ctx, p, "quests/log_battle", "/quests/log_battle", "",
			"_method=POST&battle_record="+url.QueryEscape(`{"battleId":1,"waves":[{"1":[]}]}`))
		c.game(ctx, p, "quests/result", "/quests/result", "bid="+bid+"&time=45.5&mc=0", "")
	}},
	{"map", 10, func(ctx context.Context, c *client, p *player) {
		c.game(ctx, p, "stages", "/stages/", "", "")
		c.game(ctx, p, "areas", "/areas", "stage_id=1", "")
		c.game(ctx, p, "quests", "/quests", "area_id=1", "")
	}},
	{"units", 8, simple("user_units", "/user_units/", "")},
	{"monsters", 4, simple("user_monsters", "/user_monsters/", "")},
	{"albums", 4, simple("albums", "/albums/", "")},
	{"presents", 6, func(ctx context.Context, c *client, p *player) {
		c.game(ctx, p, "user_presents", "/user_presents/", "", "")
		c.game(ctx, p, "user_presents/receive_all", "/user_presents/receive/", "", "")
	}},
	{"gacha", 6, func(ctx context.Context, c *client, p *player) {
		c.game(ctx, p, "gachas", "/gachas/", "", "")
		c.game(ctx, p, "gachas/execute", "/gachas/execute", "id=2&count=1", "")
	}},
	{"items", 5, simple("user_items", "/user_items/", "")},
	{"decks", 5, simple("user_decks", "/user_decks", "")},
	{"skills", 3, simple("user_skills", "/user_skills/", "")},
	{"adventure", 4, simple("adventure", "/adventure/", "")},
	{"infos", 3, simple("infos/index", "/infos/index", "")},
	{"guilds", 4, simple("guilds", "/guilds/", "")},
	{"reload", 3, simple("users/preset_data", "/users/preset_data.json", "")},
}

var totalWeight = func() int {
	n := 0
	for _, a := range actions {
		n += a.weight
	}
	return n
}()

func pick(p *player) action {
	n := p.rng.IntN(totalWeight)
	for _, a := range actions {
		if n < a.weight {
			return a
		}
		n -= a.weight
	}
	return actions[0]
}

func think(p *player, lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(p.rng.Int64N(int64(hi-lo)))
}

func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// ---------------------------------------------------------------- 主流程

type report struct {
	Config   map[string]any   `json:"config"`
	Login    map[string]any   `json:"login_phase"`
	Steady   dist             `json:"steady_overall"`
	Routes   map[string]dist  `json:"steady_routes"`
	Timeline []map[string]any `json:"timeline"`
	TopErrs  map[string]int   `json:"error_samples"`
	Verdict  map[string]any   `json:"verdict"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cn35load:", err)
		os.Exit(1)
	}
}

func run() error {
	base := flag.String("base", "http://127.0.0.1:18190", "压测目标（必须是独立 state 的压测实例）")
	players := flag.Int("players", 200, "并发玩家数")
	loginConc := flag.Int("login-concurrency", 20, "登录阶段并发度（模拟开服/维护后集中上线）")
	rampUp := flag.Duration("ramp", 20*time.Second, "稳态阶段玩家逐步进入的时长")
	duration := flag.Duration("duration", 3*time.Minute, "稳态阶段时长")
	profile := flag.String("profile", "normal", "normal=真实思考时间；stress=无思考时间（饱和吞吐）")
	thinkMin := flag.Duration("think-min", 2*time.Second, "normal 模式两次操作之间的最小思考时间")
	thinkMax := flag.Duration("think-max", 8*time.Second, "normal 模式最大思考时间")
	poll := flag.Duration("poll", 60*time.Second, "Top 页 /users/messages 轮询周期（实机日志约 60s）")
	devicePrefix := flag.String("device-prefix", "", "设备号前缀；留空每次随机（=全部新建号）。固定前缀可复用账号")
	out := flag.String("out", "", "JSON 报告输出路径")
	sloP95 := flag.Float64("slo-p95", 300, "通过线：稳态整体 p95 (ms)")
	sloP99 := flag.Float64("slo-p99", 1000, "通过线：稳态整体 p99 (ms)")
	sloErr := flag.Float64("slo-err", 1, "通过线：错误率 (%)")
	flag.Parse()
	if *players < 1 || *loginConc < 1 {
		return errors.New("players/login-concurrency 必须 ≥1")
	}
	*base = strings.TrimRight(*base, "/")
	if *devicePrefix == "" {
		*devicePrefix = "load-" + randStr(6)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	tr := &http.Transport{MaxIdleConns: *players * 2, MaxIdleConnsPerHost: *players * 2, IdleConnTimeout: 90 * time.Second}
	hc := &http.Client{Transport: tr, Timeout: 30 * time.Second}

	if resp, err := hc.Get(*base + "/mercd/health"); err != nil {
		return fmt.Errorf("目标不可达 %s: %w", *base, err)
	} else {
		resp.Body.Close()
	}

	// ---------- 阶段 1：集中登录 + 新号准备
	loginRec := newRecorder()
	lc := &client{base: *base, hc: hc, rec: loginRec}
	ps := make([]*player, *players)
	for i := range ps {
		ps[i] = &player{idx: i, deviceID: fmt.Sprintf("%s-%04d", *devicePrefix, i), rng: mrand.New(mrand.NewPCG(uint64(i)+1, uint64(time.Now().UnixNano())))}
	}
	fmt.Printf("== 阶段1 登录风暴：%d 名玩家，并发 %d → %s\n", *players, *loginConc, *base)
	loginStart := time.Now()
	var loginFail atomic.Int32
	sem := make(chan struct{}, *loginConc)
	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Add(1)
		sem <- struct{}{}
		go func(p *player) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := lc.login(ctx, p); err != nil {
				loginFail.Add(1)
				return
			}
			lc.onboard(ctx, p)
		}(p)
	}
	wg.Wait()
	loginSecs := time.Since(loginStart).Seconds()
	alive := ps[:0:0]
	for _, p := range ps {
		if p.uid != "" {
			alive = append(alive, p)
		}
	}
	loginAll := summarize(loginRec.samples, loginSecs)
	loginRoutes := groupBy(loginRec.samples, loginSecs)
	fmt.Printf("   完成 %.1fs，成功 %d / 失败 %d，请求 %d，p95 %.1fms，p99 %.1fms\n",
		loginSecs, len(alive), loginFail.Load(), loginAll.N, loginAll.P95, loginAll.P99)
	printTable(loginRoutes)
	if len(alive) == 0 {
		return errors.New("没有玩家登录成功，终止")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// ---------- 阶段 2：稳态游玩
	rec := newRecorder()
	gc := &client{base: *base, hc: hc, rec: rec}
	runCtx, stop := context.WithTimeout(ctx, *rampUp+*duration)
	defer stop()
	fmt.Printf("\n== 阶段2 稳态游玩：%d 名在线，profile=%s，ramp %s + %s\n", len(alive), *profile, *rampUp, *duration)
	stress := *profile == "stress"
	var inflight atomic.Int64
	steadyStart := time.Now()
	for i, p := range alive {
		wg.Add(1)
		go func(i int, p *player) {
			defer wg.Done()
			sleepCtx(runCtx, time.Duration(int64(*rampUp)*int64(i)/int64(len(alive))))
			nextPoll := time.Now().Add(think(p, 0, *poll))
			for runCtx.Err() == nil {
				if time.Now().After(nextPoll) {
					inflight.Add(1)
					gc.game(runCtx, p, "users/messages", "/users/messages", "last_read_at=0", "")
					inflight.Add(-1)
					nextPoll = time.Now().Add(*poll)
				}
				a := pick(p)
				inflight.Add(1)
				a.run(runCtx, gc, p)
				inflight.Add(-1)
				if !stress {
					sleepCtx(runCtx, think(p, *thinkMin, *thinkMax))
				}
			}
		}(i, p)
	}

	// 每 10s 打印一次窗口指标
	var timeline []map[string]any
	done := make(chan struct{})
	go func() {
		tk := time.NewTicker(10 * time.Second)
		defer tk.Stop()
		last := 0
		for {
			select {
			case <-done:
				return
			case <-tk.C:
				rec.mu.Lock()
				win := append([]sample(nil), rec.samples[last:]...)
				last = len(rec.samples)
				rec.mu.Unlock()
				d := summarize(win, 10)
				el := time.Since(steadyStart).Seconds()
				fmt.Printf("   t=%4.0fs  rps=%6.1f  p50=%7.1f  p95=%7.1f  p99=%7.1f  max=%7.1f  err=%d biz=%d inflight=%d\n",
					el, d.RPS, d.P50, d.P95, d.P99, d.Max, d.Errors, d.BizFail, inflight.Load())
				timeline = append(timeline, map[string]any{"t": el, "rps": d.RPS, "p50": d.P50, "p95": d.P95, "p99": d.P99, "max": d.Max, "errors": d.Errors})
			}
		}
	}()
	wg.Wait()
	close(done)

	// 只统计 ramp 之后的稳态窗口
	cut := steadyStart.Add(*rampUp)
	var steady []sample
	for _, s := range rec.samples {
		if s.at.After(cut) {
			steady = append(steady, s)
		}
	}
	secs := time.Since(cut).Seconds()
	overall := summarize(steady, secs)
	routes := groupBy(steady, secs)
	fmt.Printf("\n== 稳态结果（ramp 后 %.0fs）：请求 %d，%.1f req/s，p50 %.1fms p95 %.1fms p99 %.1fms max %.1fms，错误 %.2f%%，业务失败 %d\n",
		secs, overall.N, overall.RPS, overall.P50, overall.P95, overall.P99, overall.Max, overall.ErrorPct, overall.BizFail)
	printTable(routes)

	errsAll := map[string]int{}
	for k, v := range loginRec.errs {
		errsAll["[login] "+k] += v
	}
	for k, v := range rec.errs {
		errsAll[k] += v
	}
	if len(errsAll) > 0 {
		fmt.Println("\n错误/业务失败样本（前 15）：")
		type kv struct {
			k string
			v int
		}
		var l []kv
		for k, v := range errsAll {
			l = append(l, kv{k, v})
		}
		sort.Slice(l, func(i, j int) bool { return l[i].v > l[j].v })
		for i, e := range l {
			if i >= 15 {
				break
			}
			fmt.Printf("  %6d  %s\n", e.v, e.k)
		}
	}

	pass := overall.P95 <= *sloP95 && overall.P99 <= *sloP99 && overall.ErrorPct <= *sloErr && int(loginFail.Load()) == 0
	verdict := map[string]any{
		"target_players": *players, "online_players": len(alive), "profile": *profile,
		"slo":  map[string]float64{"p95_ms": *sloP95, "p99_ms": *sloP99, "error_pct": *sloErr},
		"pass": pass,
	}
	fmt.Printf("\n== 结论：%d 人在线（%s）→ ", len(alive), *profile)
	if pass {
		fmt.Printf("通过（p95≤%.0fms, p99≤%.0fms, 错误≤%.1f%%）\n", *sloP95, *sloP99, *sloErr)
	} else {
		fmt.Printf("未通过（p95=%.1f/%.0f, p99=%.1f/%.0f, err=%.2f%%/%.1f%%, 登录失败=%d）\n",
			overall.P95, *sloP95, overall.P99, *sloP99, overall.ErrorPct, *sloErr, loginFail.Load())
	}

	if *out != "" {
		rep := report{
			Config: map[string]any{"base": *base, "players": *players, "login_concurrency": *loginConc, "ramp": rampUp.String(),
				"duration": duration.String(), "profile": *profile, "think": []string{thinkMin.String(), thinkMax.String()}, "poll": poll.String()},
			Login:    map[string]any{"seconds": loginSecs, "ok": len(alive), "failed": loginFail.Load(), "overall": loginAll, "routes": loginRoutes},
			Steady:   overall,
			Routes:   routes,
			Timeline: timeline,
			TopErrs:  errsAll,
			Verdict:  verdict,
		}
		raw, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*out, raw, 0644); err != nil {
			return err
		}
		fmt.Println("报告:", *out)
	}
	if !pass {
		os.Exit(2)
	}
	return nil
}

func groupBy(ss []sample, secs float64) map[string]dist {
	m := map[string][]sample{}
	for _, s := range ss {
		m[s.route] = append(m[s.route], s)
	}
	out := map[string]dist{}
	for k, v := range m {
		out[k] = summarize(v, secs)
	}
	return out
}

func printTable(m map[string]dist) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]].P95 > m[keys[j]].P95 })
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "route\tn\trps\tp50\tp95\tp99\tmax\terr\tbiz\t")
	for _, k := range keys {
		d := m[k]
		fmt.Fprintf(w, "%s\t%d\t%.2f\t%.1f\t%.1f\t%.1f\t%.1f\t%d\t%d\t\n", k, d.N, d.RPS, d.P50, d.P95, d.P99, d.Max, d.Errors, d.BizFail)
	}
	w.Flush()
}
