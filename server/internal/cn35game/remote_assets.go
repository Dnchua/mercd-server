// remote_assets.go CN 3.5.0 远端资源叠加管理器。
//
// 资源交付策略（notes/proposed/2026-09-24-cn35-remote-asset-overlay.md）：
// 大版本发整包 APK（资源随包、清单行无 md5）；版本之间的小更新由游戏服务端
// 下发（清单行带 md5 → 客户端走 GetBundleServerUrl CDN 下载 + 持久缓存校验）。
//
// 运营者把 rar/文件直接解压进 RemoteAssetsDir（目录布局与
// client/decoded/assets/unity/ 一致的逻辑名，如 unit/unit_414.Android.unity3d）。
// 管理器周期扫描该目录，在基线清单（AssetsDir/serve_asset_info…txt）上叠加：
//   - 同名行替换为 <后缀>,<idx>,<基线version>,<基线preload>,1,<md5>
//   - 新名字追加 <后缀>,<idx>,1,0,1,<md5>（按逻辑名排序）
//   - 有效版本 = max(基线version, 持久version)；digest 变化时 +1 并落盘
//     （StateDir/remote_assets_state.json，tmp+rename 原子写），保证客户端
//     清单缓存在资源集合变化时单调失效。
//
// 防护：文件需 (size,mtime) 两次扫描一致或 mtime 早于 now-10s 才下发
// （防解压一半）；文件头必须 UnityWeb\x00 / UnityRaw\x00；逻辑名必须能映射
// AssetType。pending/rejected 只进诊断路由，不进清单。
package cn35game

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/unitybundle"
)

// assetType CN3.5 客户端 AssetType 表项（server/tools/cn350_assets.py
// ASSET_TYPES 原样移植；顺序即 typeIdx，与客户端 InitAssetTypeDic 一致）。
type assetType struct {
	mark   string // AssetType mark（诊断用）
	prefix string // 资产名前缀（逻辑名含目录）
	typ    string // 客户端 Asset.type
}

// assetTypes 顺序即清单行 typeIdx。勿重排。
var assetTypes = []assetType{
	{"area", "area/area_", "area"},                                            // 0
	{"background", "background/background", "background"},                     // 1
	{"common", "common/", "common"},                                           // 2
	{"common_icon", "common_icon/", "common"},                                 // 3
	{"common_text", "common_text/", "common_text"},                            // 4
	{"effect_pd", "effect_pd/", "effect"},                                     // 5
	{"effect_ss", "effect_ss/", "effect"},                                     // 6
	{"equipment", "equipment/equipment_", "equipment"},                        // 7
	{"event", "event/event_", "event"},                                        // 8
	{"monster", "monster/monster_", "monster"},                                // 9
	{"monster_btn", "monster/monster_btn_", "monster_btn"},                    // 10
	{"monster_gray_btn", "monster/monster_gray_btn_", "monster_gray_btn"},     // 11
	{"monster_large_ns", "monster/monster_large_ns_", "monster_large_ns"},     // 12
	{"monster_list", "monster/monster_list_", "monster_list"},                 // 13
	{"monster_rectangle", "monster/monster_rectangle_", "monster_rectangle"},  // 14
	{"monster_square", "monster/monster_square_", "monster_square"},           // 15
	{"skill", "skill/skill_", "skill"},                                        // 16
	{"sound", "sound/", "sound"},                                              // 17
	{"storyactress", "storyactress/storyactress", "storyactress"},             // 18
	{"storyactress_hd", "storyactress_hd/storyactress", "storyactress_hd"},    // 19
	{"storybackground", "storybackground/storybackground", "storybackground"}, // 20
	{"swf", "swf/", "swf"},                                                    // 21
	{"task", "task/", "task"},                                                 // 22
	{"unit", "unit/unit_", "unit"},                                            // 23
	{"unit_btn_recruit", "unit/unit_btn_", "unit_btn_recruit"},                // 24
	{"unit_btn", "unit/unit_btn_", "unit_btn"},                                // 25
	{"unit_gray_btn", "unit/unit_gray_btn_", "unit_gray_btn"},                 // 26
	{"unit_large", "unit/unit_large_", "unit_large"},                          // 27
	{"unit_large_ns", "unit/unit_large_ns_", "unit_large_ns"},                 // 28
	{"unit_list", "unit/unit_list_", "unit_list"},                             // 29
	{"unit_rectangle", "unit/unit_rectangle_", "unit_rectangle"},              // 30
	{"unit_silhouette", "unit/unit_silhouette_", "unit_silhouette"},           // 31
	{"unit_square", "unit/unit_square_", "unit_square"},                       // 32
	{"unit_large_hd", "unit_hd/unit_large_", "unit_large_hd"},                 // 33
	{"unit_large_ns_hd", "unit_hd/unit_large_ns_", "unit_large_ns_hd"},        // 34
	{"unit_square_hd", "unit_hd/unit_square_", "unit_square_hd"},              // 35
	{"stamp", "stamp/stamp_", "stamp"},                                        // 36
}

// typeIndex 逻辑名 → (typeIdx, name后缀)：最长前缀匹配；同前缀歧义
// （unit/unit_btn_ 同时命中 unit_btn_recruit 与 unit_btn）缺省落非 recruit
// 项——与 cn350_assets.py type_index 缺省口径一致。
func typeIndex(name string) (int, string, bool) {
	idx, maxLen := -1, 0
	for _, t := range assetTypes {
		if strings.HasPrefix(name, t.prefix) && len(t.prefix) > maxLen {
			maxLen = len(t.prefix)
		}
	}
	if maxLen == 0 {
		return 0, "", false
	}
	for i, t := range assetTypes {
		if len(t.prefix) == maxLen && strings.HasPrefix(name, t.prefix) &&
			t.typ != "unit_btn_recruit" {
			idx = i
			break
		}
	}
	if idx < 0 { // 候选全是 recruit（实际不会发生）
		for i, t := range assetTypes {
			if len(t.prefix) == maxLen && strings.HasPrefix(name, t.prefix) {
				idx = i
				break
			}
		}
	}
	return idx, name[len(assetTypes[idx].prefix):], true
}

var (
	remoteFileRe = regexp.MustCompile(`^(.+)\.Android\.unity3d$`)
	// bundleURLRe 客户端 GetBundleServerUrl 文件名形态：
	// <urlName>.Android.<asset.version>.unity3d。
	bundleURLRe = regexp.MustCompile(`^(.+)\.Android\.[0-9]+\.unity3d$`)
)

// unity 魔数白名单（client/decoded/assets/unity 全量统计：3732 个 Android
// bundle 全为 UnityWeb\x00；UnityRaw\x00 为未压缩变体，一并放行）。
var bundleMagics = [][]byte{[]byte("UnityWeb\x00"), []byte("UnityRaw\x00")}

// fileObs 一次扫描观察到的文件形态（稳定性判定 + 哈希缓存键）。
type fileObs struct {
	size      int64
	mtimeUnix int64 // ModTime UnixNano（== 可比较，不带时区/单调钟歧义）
}

type fileHash struct {
	md5    string // md5 hex 前 8 位小写（清单 rand 参数/客户端校验口径）
	sha256 string
}

// remoteCand 一次扫描收集到的候选文件。
type remoteCand struct {
	rel  string // 相对远端目录，/ 分隔
	name string // 逻辑名（去 .Android.unity3d）
	obs  fileObs
}

// RemoteEntry 一条稳定且通过校验、参与清单叠加的远端资源。
type RemoteEntry struct {
	Name             string `json:"name"` // 逻辑名（unit/unit_414）
	File             string `json:"file"` // 磁盘路径
	Size             int64  `json:"size"`
	MD5              string `json:"md5"`
	SHA256           string `json:"sha256"`
	TypeIdx          int    `json:"type_idx"`
	ReplacesEmbedded bool   `json:"replaces_embedded"`  // 覆盖了基线清单同名行
	OrigCAB          string `json:"orig_cab,omitempty"` // 原内部归档名（CAB 冲突改名时记录）
	CAB              string `json:"cab,omitempty"`      // 下发文件的内部归档名
}

// RemotePending 观察中尚未稳定（可能仍在解压）的候选文件。
type RemotePending struct {
	File string `json:"file"`
}

// RemoteReject 校验失败、永不下发的候选文件。
type RemoteReject struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// RemoteReport /cn35/assets/remote 诊断路由输出。
type RemoteReport struct {
	AssetInfoVersion int64           `json:"asset_info_version"`
	BaseVersion      int64           `json:"base_version"`
	RemoteDir        string          `json:"remote_dir"`
	Entries          []RemoteEntry   `json:"entries"`
	Pending          []RemotePending `json:"pending"`
	Rejected         []RemoteReject  `json:"rejected"`
}

// remoteSnapshot 一次扫描产出的只读视图（RWMutex 保护，整份替换）。
type remoteSnapshot struct {
	body        []byte // 渲染好的 asset_info 清单
	version     int64  // 有效 asset_info_version（= body 首行）
	baseVersion int64
	entries     map[string]RemoteEntry // 逻辑名 → 条目
	pending     []RemotePending
	rejected    []RemoteReject
}

// remoteStateFile StateDir/remote_assets_state.json 的持久结构。
type remoteStateFile struct {
	AssetInfoVersion int64  `json:"asset_info_version"`
	Digest           string `json:"digest"`
}

// RemoteAssets 远端资源叠加管理器：扫描 → 校验 → 渲染 → 版本持久化。
// 快照经 RWMutex 并发读出；扫描可在后台 goroutine 周期执行。
type RemoteAssets struct {
	log       *slog.Logger
	dir       string // 远端资源目录（运营者投放点）
	baseFile  string // 基线清单（AssetsDir/serve_asset_info_android4_v2_short.txt）
	cabsFile  string // APK 内置 bundle CAB 表（AssetsDir/embedded_bundle_cabs.json）
	normDir   string // StateDir/remote_assets_normalized（改名后交付文件）
	stateFile string // StateDir/remote_assets_state.json

	now       func() time.Time // 可注入时钟（测试）
	stableAge time.Duration    // mtime 早于此即视为稳定（默认 10s）

	mu       sync.RWMutex
	snap     remoteSnapshot
	scanned  bool
	observed map[string]fileObs  // rel 路径 → 上次扫描观察（稳定性判定）
	hashes   map[string]fileHash // rel|size|mtime → 哈希缓存
	cabs     map[string][]string // rel|size|mtime → 内部归档名缓存
}

// NewRemoteAssets 创建管理器（不做首次扫描；调用方 Scan 一次后再服务）。
// stateDir 为空时状态文件落在远端目录同级不可达——必须传实际 StateDir。
func NewRemoteAssets(dir, assetsDir, stateDir string, log *slog.Logger) *RemoteAssets {
	if log == nil {
		log = slog.Default()
	}
	return &RemoteAssets{
		log:       log,
		dir:       dir,
		baseFile:  filepath.Join(assetsDir, assetInfoFile),
		cabsFile:  filepath.Join(assetsDir, "embedded_bundle_cabs.json"),
		normDir:   filepath.Join(stateDir, "remote_assets_normalized"),
		stateFile: filepath.Join(stateDir, "remote_assets_state.json"),
		now:       time.Now,
		stableAge: 10 * time.Second,
		observed:  make(map[string]fileObs),
		hashes:    make(map[string]fileHash),
		cabs:      make(map[string][]string),
	}
}

// Dir 远端资源目录（诊断展示）。
func (m *RemoteAssets) Dir() string { return m.dir }

// Body 当前渲染好的清单字节；未扫描过时为 nil。
func (m *RemoteAssets) Body() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snap.body
}

// Version 有效 asset_info_version（与 Body 首行一致）；未扫描过为 0。
func (m *RemoteAssets) Version() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snap.version
}

// Entry 按逻辑名查稳定条目（bundleHandler 反查后的命中面）。
func (m *RemoteAssets) Entry(name string) (RemoteEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.snap.entries[name]
	return e, ok
}

// Report 诊断快照（/cn35/assets/remote）。
func (m *RemoteAssets) Report() RemoteReport {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rep := RemoteReport{
		AssetInfoVersion: m.snap.version,
		BaseVersion:      m.snap.baseVersion,
		RemoteDir:        m.dir,
		Entries:          make([]RemoteEntry, 0, len(m.snap.entries)),
		Pending:          append([]RemotePending(nil), m.snap.pending...),
		Rejected:         append([]RemoteReject(nil), m.snap.rejected...),
	}
	for _, e := range m.snap.entries {
		rep.Entries = append(rep.Entries, e)
	}
	sort.Slice(rep.Entries, func(i, j int) bool { return rep.Entries[i].Name < rep.Entries[j].Name })
	return rep
}

// Scanned 是否已成功产出过快照。
func (m *RemoteAssets) Scanned() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scanned
}

// Scan 执行一轮 扫描→校验→渲染→版本持久化，成功后整份替换快照。
// 任何一步失败保留旧快照并返回错误（调用方记日志）。
func (m *RemoteAssets) Scan() error {
	scanStart := time.Now()
	now := m.now()
	// 1) 遍历远端目录收集候选（非 *.Android.unity3d 静默忽略）。
	var cands []remoteCand
	seen := make(map[string]bool)
	walkErr := filepath.Walk(m.dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		if strings.HasPrefix(fi.Name(), ".") {
			return nil // 隐藏文件/._*/.DS_Store 静默忽略
		}
		rel, err := filepath.Rel(m.dir, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		mm := remoteFileRe.FindStringSubmatch(rel)
		if mm == nil {
			return nil // .iPhone 等其他文件静默忽略
		}
		obs := fileObs{size: fi.Size(), mtimeUnix: fi.ModTime().UnixNano()}
		seen[rel] = true
		cands = append(cands, remoteCand{rel: rel, name: mm[1], obs: obs})
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		return fmt.Errorf("远端资源目录遍历失败 %s: %w", m.dir, walkErr)
	}

	// 2) 稳定性 + 校验。
	entries := make(map[string]RemoteEntry)
	candByName := make(map[string]remoteCand) // 逻辑名 → 候选（CAB 规范化用）
	var pending []RemotePending
	var rejected []RemoteReject
	for _, cd := range cands {
		prev, seenBefore := m.observed[cd.rel]
		stable := (seenBefore && prev == cd.obs) ||
			now.UnixNano()-cd.obs.mtimeUnix > m.stableAge.Nanoseconds()
		m.observed[cd.rel] = cd.obs
		if !stable {
			pending = append(pending, RemotePending{File: cd.rel})
			continue
		}
		full := filepath.Join(m.dir, filepath.FromSlash(cd.rel))
		h, reason := m.hashAndCheck(full, cd.rel, cd.obs)
		if reason != "" {
			rejected = append(rejected, RemoteReject{File: cd.rel, Reason: reason})
			m.log.Warn("远端资源校验失败，不下发", "file", cd.rel, "reason", reason)
			continue
		}
		idx, _, ok := typeIndex(cd.name)
		if !ok {
			rejected = append(rejected, RemoteReject{File: cd.rel, Reason: "无匹配 AssetType: " + cd.name})
			m.log.Warn("远端资源无法映射 AssetType，不下发", "file", cd.rel, "name", cd.name)
			continue
		}
		entries[cd.name] = RemoteEntry{
			Name: cd.name, File: full, Size: cd.obs.size,
			MD5: h.md5, SHA256: h.sha256, TypeIdx: idx,
		}
		candByName[cd.name] = cd
	}
	// 2.5) CAB 名冲突规范化：不同逻辑名共用同一内部归档名的远端 bundle
	// 改名后写入 StateDir/remote_assets_normalized，条目改指规范化文件。
	cabRenamed, cabDur, cabRejects := m.normalizeCABs(entries, candByName)
	rejected = append(rejected, cabRejects...)

	// 清理已消失文件的观察记录，避免 map 无限增长。
	for rel := range m.observed {
		if !seen[rel] {
			delete(m.observed, rel)
		}
	}

	// 3) 重读基线清单并解析（行 = '@' 分隔；末行尾随 \n）。
	baseRaw, err := os.ReadFile(m.baseFile)
	if err != nil {
		return fmt.Errorf("基线清单读取失败 %s: %w", m.baseFile, err)
	}
	baseBody := strings.TrimSuffix(string(baseRaw), "\n")
	baseRows := strings.Split(baseBody, "@")
	if len(baseRows) == 0 || !strings.HasPrefix(baseRows[0], "version,") {
		return fmt.Errorf("基线清单首行缺 version,: %.32q", baseRows[0])
	}
	baseVer, err := strconv.ParseInt(strings.TrimSpace(baseRows[0][len("version,"):]), 10, 64)
	if err != nil || baseVer == 0 {
		return fmt.Errorf("基线清单 version 非法: %q", baseRows[0])
	}
	baseSHA := sha256.Sum256(baseRaw)

	// 基线资产行：行名 = assetTypes[idx].prefix + 后缀。
	baseRowName := make(map[string]int) // 逻辑名 → baseRows 下标
	for i := 1; i < len(baseRows); i++ {
		f := strings.Split(baseRows[i], ",")
		if len(f) < 5 {
			continue // 头行（ios_version/android_version/maintenance 等）
		}
		idx, ierr := strconv.Atoi(f[1])
		if ierr != nil || idx < 0 || idx >= len(assetTypes) {
			continue
		}
		baseRowName[assetTypes[idx].prefix+f[0]] = i
	}

	// 4) digest = sha256(排序 name|md5 列表 + 基线 sha256)。
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	dh := sha256.New()
	for _, n := range names {
		fmt.Fprintf(dh, "%s|%s\n", n, entries[n].MD5)
	}
	dh.Write(baseSHA[:])
	digest := hex.EncodeToString(dh.Sum(nil))

	// 5) 有效版本 = max(基线, 持久 N)；digest 变化时 +1 并落盘。
	stored := remoteStateFile{}
	if raw, rerr := os.ReadFile(m.stateFile); rerr == nil {
		_ = json.Unmarshal(raw, &stored)
	}
	effVer := max(baseVer, stored.AssetInfoVersion)
	if stored.Digest != digest && !(len(entries) == 0 && stored.Digest == "") {
		// digest 变化 → 单调递增并落盘；无有效条目且无持久状态时
		// 输出即基线（版本不递增、不写文件——rejected/pending/忽略
		// 文件不产生清单变化）。
		effVer = max(baseVer, stored.AssetInfoVersion) + 1
		if werr := writeRemoteState(m.stateFile, remoteStateFile{
			AssetInfoVersion: effVer, Digest: digest,
		}); werr != nil {
			return fmt.Errorf("远端资源状态文件写入失败: %w", werr)
		}
	}

	// 6) 渲染：头行 version 替换为有效版本；同名行替换；新名追加。
	out := make([]string, 0, len(baseRows)+len(entries))
	out = append(out, "version,"+strconv.FormatInt(effVer, 10))
	overridden := make(map[string]bool)
	for i := 1; i < len(baseRows); i++ {
		row := baseRows[i]
		f := strings.Split(row, ",")
		if len(f) < 5 {
			out = append(out, row)
			continue
		}
		idx, ierr := strconv.Atoi(f[1])
		if ierr != nil || idx < 0 || idx >= len(assetTypes) {
			out = append(out, row)
			continue
		}
		name := assetTypes[idx].prefix + f[0]
		if e, ok := entries[name]; ok {
			out = append(out, fmt.Sprintf("%s,%d,%s,%s,1,%s",
				f[0], idx, f[2], f[3], e.MD5))
			overridden[name] = true
			ent := e
			ent.ReplacesEmbedded = true
			ent.TypeIdx = idx // 与基线行 idx 对齐（歧义前缀时以基线为准）
			entries[name] = ent
		} else {
			out = append(out, row)
		}
	}
	for _, n := range names {
		if overridden[n] {
			continue
		}
		e := entries[n]
		idx, suffix, _ := typeIndex(n)
		out = append(out, fmt.Sprintf("%s,%d,1,0,1,%s", suffix, idx, e.MD5))
	}
	body := []byte(strings.Join(out, "@") + "\n")

	m.mu.Lock()
	m.snap = remoteSnapshot{
		body: body, version: effVer, baseVersion: baseVer,
		entries: entries, pending: pending, rejected: rejected,
	}
	m.scanned = true
	m.mu.Unlock()
	m.log.Info("远端资源扫描完成", "dir", m.dir,
		"entries", len(entries), "pending", len(pending),
		"rejected", len(rejected), "asset_info_version", effVer,
		"base_version", baseVer,
		"cab_renamed", cabRenamed, "cab_ms", cabDur.Milliseconds(),
		"scan_ms", time.Since(scanStart).Milliseconds())
	return nil
}

// normSidecar 规范化文件旁的缓存凭据：源 sha256 + 改名表命中即复用。
type normSidecar struct {
	SrcSHA256 string            `json:"src_sha256"`
	Renames   map[string]string `json:"renames"`
}

// normalizeCABs 对 entries 做 CAB 名冲突判定并改写需改名条目的交付文件。
// 返回（改名数, 耗时, 追加的 rejected）。
func (m *RemoteAssets) normalizeCABs(entries map[string]RemoteEntry, candByName map[string]remoteCand) (int, time.Duration, []RemoteReject) {
	start := time.Now()
	embedded := make(map[string][]string)
	if raw, err := os.ReadFile(m.cabsFile); err == nil {
		if jerr := json.Unmarshal(raw, &embedded); jerr != nil {
			m.log.Warn("内置 CAB 表解析失败，仅做远端内部判定", "file", m.cabsFile, "err", jerr)
			embedded = nil
		}
	} else {
		m.log.Warn("内置 CAB 表缺失，仅做远端内部判定", "file", m.cabsFile)
	}

	remote := make(map[string][]string, len(entries))
	for name, e := range entries {
		cd := candByName[name]
		cabs, reason := m.cabNamesFor(e.File, cd.rel, cd.obs)
		if reason != "" {
			// 目录解析失败的条目不参与判定，保留原文件下发
			// （魔数校验已通过；与历史行为一致）。
			m.log.Warn("远端资源 CAB 目录解析失败，按原名下发",
				"file", cd.rel, "reason", reason)
			continue
		}
		remote[name] = cabs
	}
	plan := unitybundle.PlanCABRenames(embedded, remote)
	if len(plan) == 0 {
		return 0, time.Since(start), nil
	}

	// 并行改名：worker 只产出规范化文件，主 goroutine 串行改 entries。
	type job struct {
		name    string
		rel     string
		src     string
		srcSHA  string
		renames map[string]string
	}
	type result struct {
		name   string
		file   string
		reject RemoteReject
	}
	names := make([]string, 0, len(plan))
	for n := range plan {
		names = append(names, n)
	}
	sort.Strings(names)
	jobs := make(chan job)
	results := make(chan result, len(names))
	workers := runtime.NumCPU()
	if workers > len(names) {
		workers = len(names)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				normPath := filepath.Join(m.normDir, filepath.FromSlash(j.rel))
				res := result{name: j.name, file: normPath}
				if !m.normCacheHit(normPath, j.srcSHA, j.renames) {
					if err := m.writeNormalized(normPath, j.src, j.srcSHA, j.renames); err != nil {
						res.file = ""
						res.reject = RemoteReject{File: j.rel,
							Reason: "CAB 改名失败: " + err.Error()}
					}
				}
				results <- res
			}
		}()
	}
	for _, n := range names {
		cd := candByName[n]
		jobs <- job{name: n, rel: cd.rel, src: entries[n].File,
			srcSHA: entries[n].SHA256, renames: plan[n]}
	}
	close(jobs)
	wg.Wait()
	close(results)

	var rejected []RemoteReject
	renamed := 0
	for res := range results {
		if res.file == "" {
			delete(entries, res.name)
			rejected = append(rejected, res.reject)
			m.log.Warn("远端资源 CAB 改名失败，不下发", "file", res.reject.File,
				"reason", res.reject.Reason)
			continue
		}
		fi, err := os.Stat(res.file)
		if err != nil {
			delete(entries, res.name)
			rejected = append(rejected, RemoteReject{File: candByName[res.name].rel,
				Reason: "CAB 规范化文件缺失: " + err.Error()})
			continue
		}
		e := entries[res.name]
		h, reason := m.hashAndCheck(res.file, "norm/"+candByName[res.name].rel,
			fileObs{size: fi.Size(), mtimeUnix: fi.ModTime().UnixNano()})
		if reason != "" {
			delete(entries, res.name)
			rejected = append(rejected, RemoteReject{File: candByName[res.name].rel,
				Reason: "CAB 规范化文件校验失败: " + reason})
			continue
		}
		e.File, e.Size = res.file, fi.Size()
		e.MD5, e.SHA256 = h.md5, h.sha256
		e.OrigCAB = strings.Join(remote[res.name], ",")
		newCabs := make([]string, 0, len(remote[res.name]))
		for _, c := range remote[res.name] {
			if n, ok := plan[res.name][c]; ok {
				newCabs = append(newCabs, n)
			} else {
				newCabs = append(newCabs, c)
			}
		}
		e.CAB = strings.Join(newCabs, ",")
		entries[res.name] = e
		renamed++
	}
	return renamed, time.Since(start), rejected
}

// cabNamesFor 读 bundle 内部归档名（按 rel|size|mtime 缓存）。
func (m *RemoteAssets) cabNamesFor(full, rel string, obs fileObs) ([]string, string) {
	key := fmt.Sprintf("%s|%d|%d", rel, obs.size, obs.mtimeUnix)
	if c, ok := m.cabs[key]; ok {
		return c, ""
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, "读取失败: " + err.Error()
	}
	defer f.Close()
	cabs, err := unitybundle.CABNames(f)
	if err != nil {
		return nil, "CAB 目录解析失败: " + err.Error()
	}
	m.cabs[key] = cabs
	return cabs, ""
}

// normCacheHit 规范化缓存是否命中（sidecar 的源 sha+改名表一致且文件在）。
func (m *RemoteAssets) normCacheHit(normPath, srcSHA string, renames map[string]string) bool {
	raw, err := os.ReadFile(normPath + ".json")
	if err != nil {
		return false
	}
	var sc normSidecar
	if json.Unmarshal(raw, &sc) != nil || sc.SrcSHA256 != srcSHA ||
		len(sc.Renames) != len(renames) {
		return false
	}
	for o, n := range renames {
		if sc.Renames[o] != n {
			return false
		}
	}
	fi, err := os.Stat(normPath)
	return err == nil && !fi.IsDir()
}

// writeNormalized 读源 bundle 改名后 tmp+rename 落盘 + sidecar。
func (m *RemoteAssets) writeNormalized(normPath, src, srcSHA string, renames map[string]string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	out, err := unitybundle.ApplyCABPlan(data, renames)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(normPath), 0o755); err != nil {
		return err
	}
	tmp := normPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, normPath); err != nil {
		return err
	}
	sc, _ := json.Marshal(normSidecar{SrcSHA256: srcSHA, Renames: renames})
	if err := os.WriteFile(normPath+".json.tmp", sc, 0o644); err != nil {
		return err
	}
	return os.Rename(normPath+".json.tmp", normPath+".json")
}

// hashAndCheck 魔数校验 + md5/sha256（按 rel|size|mtime 缓存）。
func (m *RemoteAssets) hashAndCheck(full, rel string, obs fileObs) (fileHash, string) {
	key := fmt.Sprintf("%s|%d|%d", rel, obs.size, obs.mtimeUnix)
	if h, ok := m.hashes[key]; ok {
		return h, ""
	}
	f, err := os.Open(full)
	if err != nil {
		return fileHash{}, "读取失败: " + err.Error()
	}
	defer f.Close()
	head := make([]byte, 9)
	if _, err := io.ReadFull(f, head); err != nil {
		return fileHash{}, "文件头不足 9 字节或非 unity3d"
	}
	okMagic := false
	for _, mg := range bundleMagics {
		if string(head[:len(mg)]) == string(mg) {
			okMagic = true
			break
		}
	}
	if !okMagic {
		return fileHash{}, fmt.Sprintf("魔数非法 %q（非 UnityWeb/UnityRaw）", head)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fileHash{}, "seek 失败: " + err.Error()
	}
	mh := md5.New()
	sh := sha256.New()
	if _, err := io.Copy(io.MultiWriter(mh, sh), f); err != nil {
		return fileHash{}, "哈希失败: " + err.Error()
	}
	h := fileHash{
		md5:    hex.EncodeToString(mh.Sum(nil))[:8],
		sha256: hex.EncodeToString(sh.Sum(nil)),
	}
	m.hashes[key] = h
	return h, ""
}

// Run 后台周期扫描（interval<=0 直接返回——由调用方保证已 Scan 一次）。
// ctx 取消即退出（随 server 生命周期）。
func (m *RemoteAssets) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.Scan(); err != nil {
				m.log.Warn("远端资源周期扫描失败，保留上一快照", "err", err)
			}
		}
	}
}

// writeRemoteState tmp+rename 原子写状态文件。
func writeRemoteState(path string, st remoteStateFile) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// bundleLogicalName 把客户端请求的 <urlName>.Android.<ver>.unity3d 反查回
// 远端目录逻辑名：urlName 的 unit_v2/monster_v2/effect_ss_v2 前缀对应
// Asset.urlName 改写（unit/monster/effect_ss），其余类型路径不变。
func bundleLogicalName(name string) (string, bool) {
	mm := bundleURLRe.FindStringSubmatch(name)
	if mm == nil {
		return "", false
	}
	p := mm[1]
	for _, rw := range [][2]string{
		{"unit_v2/", "unit/"},
		{"monster_v2/", "monster/"},
		{"effect_ss_v2/", "effect_ss/"},
	} {
		if strings.HasPrefix(p, rw[0]) {
			return rw[1] + p[len(rw[0]):], true
		}
	}
	return p, true
}
