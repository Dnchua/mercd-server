// story.go 故事目录 fixture（AssetsDir/stories/）的加载与校验。
//
// 设计真源：notes/proposed/2026-09-28-cn35-story-reader.md。
// fixture 由 server/tools/cn350_assets.py stories 从 APK 内嵌
// Config/story* TextAsset 提取（story1-10，chapter_id=1 首期 10 话）；
// 未纳入的 TextAsset（教程变体/系统讲解/备份）在 index.json excluded 留痕。
//
// 校验失败的故事不下发、只进 diagnostics；fixture 缺失/全部失败时
// catalog 为空，/chapters/ 回落空列表——不 panic、不阻止启动（记 warn）。
// 校验规则即 Note §1 的三层：结构（story 对象/id 对齐/场景数/文本长）、
// 资源闭包（asset_names/立绘/背景 ⊆ 生效清单）、目录键唯一。
package cn35game

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// storyIndexFile Stories fixture 索引（AssetsDir 下相对路径）。
const storyIndexFile = "stories/index.json"

// ---- index.json 模型（与 cn350_assets.py stories 产物同构）----

type storyIndexChapter struct {
	ID                  int    `json:"id"`
	Name                string `json:"name"`
	BackgroundAssetName string `json:"background_asset_name"`
	Provenance          string `json:"provenance"`
}

type storyIndexEntry struct {
	ID           int    `json:"id"`
	TextAsset    string `json:"textasset"`
	SourceFile   string `json:"source_file"`
	SourceSHA256 string `json:"source_sha256"`
	ChapterID    int    `json:"chapter_id"`
	OrderNum     int    `json:"order_num"`
	Title        string `json:"title"`
	SubTitle     string `json:"sub_title"`
	AreaID       int    `json:"area_id"`
	QuestID      int    `json:"quest_id"`
	ReadTimingID int    `json:"read_timing_id"`
	StoryTypeID  int    `json:"story_type_id"`
	SceneCount   int    `json:"scene_count"`
	Provenance   string `json:"provenance"`
}

type storyIndexDoc struct {
	Chapters []storyIndexChapter `json:"chapters"`
	Stories  []storyIndexEntry   `json:"stories"`
	Excluded []storyIndexExcl    `json:"excluded"`
}

type storyIndexExcl struct {
	TextAsset  string `json:"textasset"`
	SourceFile string `json:"source_file"`
	InnerID    any    `json:"inner_id"`
	Reason     string `json:"reason"`
}

// storyDiagnostic 一条剔除/失败诊断（不进下发面）。
type storyDiagnostic struct {
	TextAsset string `json:"textasset,omitempty"`
	File      string `json:"file,omitempty"`
	Reason    string `json:"reason"`
}

// catalogStory 一话通过校验的故事：元数据 + 原始响应体。
// payload 即 story_<id>.json 文件原文（顶层 story/asset_names/actress_xys，
// 客户端 StoryReadDocument.ParseCore 直读顶层，不加 data 信封）。
type catalogStory struct {
	meta    storyIndexEntry
	payload json.RawMessage
}

// storyCatalog 启动期加载的故事目录（只读，加载后不再变）。
type storyCatalog struct {
	chapters    []storyIndexChapter // 仅含 ≥1 话通过校验的章
	stories     map[int]*catalogStory
	byChapter   map[int][]*catalogStory // 按 order_num 升序
	diagnostics []storyDiagnostic
}

// loadStoryCatalog 读 AssetsDir/stories/ 并逐话校验。
// accepted 为生效清单名集（基线清单 ∪ 远端叠加快照 entries）；
// fixture 缺失/索引损坏时返回空 catalog（diagnostics 记因），绝不返回 nil。
func loadStoryCatalog(assetsDir string, accepted map[string]bool) *storyCatalog {
	cat := &storyCatalog{
		stories:   map[int]*catalogStory{},
		byChapter: map[int][]*catalogStory{},
	}
	dir := filepath.Join(assetsDir, "stories")
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			cat.diagnostics = append(cat.diagnostics, storyDiagnostic{
				File: "index.json", Reason: "读取失败: " + err.Error()})
		}
		return cat
	}
	var idx storyIndexDoc
	if err := json.Unmarshal(raw, &idx); err != nil {
		cat.diagnostics = append(cat.diagnostics, storyDiagnostic{
			File: "index.json", Reason: "非法 JSON: " + err.Error()})
		return cat
	}
	seenIDs := map[int]bool{}
	seenKeys := map[[2]int]bool{}
	for _, e := range idx.Stories {
		label := e.TextAsset
		if label == "" {
			label = fmt.Sprintf("story_%d", e.ID)
		}
		key := [2]int{e.ChapterID, e.OrderNum}
		if seenIDs[e.ID] {
			cat.diagnostics = append(cat.diagnostics, storyDiagnostic{
				TextAsset: label, Reason: fmt.Sprintf("story.id %d 与已收冲突", e.ID)})
			continue
		}
		if seenKeys[key] {
			cat.diagnostics = append(cat.diagnostics, storyDiagnostic{
				TextAsset: label,
				Reason:    fmt.Sprintf("目录键 (chapter_id=%d,order_num=%d) 重复", e.ChapterID, e.OrderNum)})
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(dir, fmt.Sprintf("story_%d.json", e.ID)))
		if rerr != nil {
			cat.diagnostics = append(cat.diagnostics, storyDiagnostic{
				TextAsset: label, File: fmt.Sprintf("story_%d.json", e.ID),
				Reason: "文件读取失败: " + rerr.Error()})
			continue
		}
		if reason := validateStoryDoc(e.ID, body, accepted); reason != "" {
			cat.diagnostics = append(cat.diagnostics, storyDiagnostic{
				TextAsset: label, File: fmt.Sprintf("story_%d.json", e.ID),
				Reason: reason})
			continue
		}
		// 下发前必须压实：客户端 JSONObject.cs 手写 tokenizer 按固定偏移
		// 取 key（Substring(num+2,i-num-3)，假设 "key" 紧跟分隔符），
		// 缩进/换行会污染 key 提取 → ParseCore 找不到 "story" →
		// StoryViewer 静默 cancel（errorType=None，ShowErrorModal 无操作）。
		// 实机证据 2026-09-28：带 indent 的响应使页 Page Story→cancel 同帧。
		var compact bytes.Buffer
		if cerr := json.Compact(&compact, body); cerr != nil {
			cat.diagnostics = append(cat.diagnostics, storyDiagnostic{
				TextAsset: label, File: fmt.Sprintf("story_%d.json", e.ID),
				Reason: "JSON 压实失败: " + cerr.Error()})
			continue
		}
		seenIDs[e.ID] = true
		seenKeys[key] = true
		cs := &catalogStory{meta: e, payload: json.RawMessage(compact.Bytes())}
		cat.stories[e.ID] = cs
		cat.byChapter[e.ChapterID] = append(cat.byChapter[e.ChapterID], cs)
	}
	for _, list := range cat.byChapter {
		sort.Slice(list, func(i, j int) bool {
			return list[i].meta.OrderNum < list[j].meta.OrderNum
		})
	}
	for _, ch := range idx.Chapters {
		if len(cat.byChapter[ch.ID]) > 0 {
			cat.chapters = append(cat.chapters, ch)
		}
	}
	return cat
}

// validateStoryDoc 校验单话响应体。返回非空串 = 剔除原因。
//   - story 为对象、story.id 为数字且等于文件 id；
//   - scenes_map_array ≥1，每场景 text 为串且 ≥2 个 rune
//     （客户端 SceneDocument 对 text 做 Substring(1,len-2)，短于 2 会越界）；
//   - asset_names 全在生效清单；
//   - 场景 left/center/right 对象带 id 时：actress_xys 含 actress_xy_<id>
//     且立绘包在生效清单并列入 asset_names（StoryViewer 只预载 asset_names，
//     未预载的包在 UpdateActresses 里取 bundleObjects 抛异常、角色静默不显示）：
//     type=scoutactress 读 unit/unit_large_ns_<id>，其余读 storyactress/storyactress<id>；
//   - 场景 background 非空时：storybackground/storybackground<bg>_<tz>
//     在生效清单；
//   - 场景 bgm 非空时：bgm 值（sound/<名>）在生效清单并列入 asset_names
//     （AudioManager.PlayBGM 只从已加载包取 AudioClip，未加载则静默不播）。
func validateStoryDoc(fileID int, raw []byte, accepted map[string]bool) string {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "JSON 非法: " + err.Error()
	}
	story, ok := doc["story"].(map[string]any)
	if !ok {
		return "story 对象缺失"
	}
	sid, ok := story["id"].(float64)
	if !ok || int(sid) != fileID {
		return fmt.Sprintf("story.id %v 与文件 id %d 不符", story["id"], fileID)
	}
	scenes, ok := story["scenes_map_array"].([]any)
	if !ok || len(scenes) < 1 {
		return "scenes_map_array 缺失或为空"
	}
	names, _ := doc["asset_names"].([]any)
	preload := make(map[string]bool, len(names))
	for _, a := range names {
		s, ok := a.(string)
		if !ok {
			return "asset_names 含非字符串项"
		}
		if !accepted[s] {
			return "asset_names 引用未下发资源: " + s
		}
		preload[s] = true
	}
	actressXYs, _ := doc["actress_xys"].(map[string]any)
	for i, sv := range scenes {
		sc, ok := sv.(map[string]any)
		if !ok {
			return fmt.Sprintf("scenes_map_array[%d] 非对象", i)
		}
		text, ok := sc["text"].(string)
		if !ok || len([]rune(text)) < 2 {
			return fmt.Sprintf("scenes_map_array[%d].text 非字符串或长度<2", i)
		}
		for _, pos := range []string{"left", "center", "right"} {
			ch, ok := sc[pos].(map[string]any)
			if !ok {
				continue
			}
			if storyStringField(ch["type"]) != "scoutactress" && storyStringField(ch["expression"]) == "" {
				return fmt.Sprintf("scenes_map_array[%d].%s 立绘 expression 为空（客户端会绘制图集首个表情块），应为 tujo", i, pos)
			}
			aid, hasID := storyActorID(ch["id"])
			if !hasID {
				continue
			}
			xyKey := fmt.Sprintf("actress_xy_%d", aid)
			if _, ok := actressXYs[xyKey]; !ok {
				return fmt.Sprintf("scenes_map_array[%d].%s id=%d 缺 %s", i, pos, aid, xyKey)
			}
			bundle := fmt.Sprintf("storyactress/storyactress%d", aid)
			if storyStringField(ch["type"]) == "scoutactress" {
				bundle = fmt.Sprintf("unit/unit_large_ns_%d", aid)
			}
			if !accepted[bundle] {
				return fmt.Sprintf("scenes_map_array[%d].%s id=%d 缺资源 %s", i, pos, aid, bundle)
			}
			if !preload[bundle] {
				return fmt.Sprintf("scenes_map_array[%d].%s id=%d 的 %s 未列入 asset_names", i, pos, aid, bundle)
			}
		}
		if bgm := storyStringField(sc["bgm"]); bgm != "" {
			if !accepted[bgm] {
				return fmt.Sprintf("scenes_map_array[%d] 缺 BGM 资源 %s", i, bgm)
			}
			if !preload[bgm] {
				return fmt.Sprintf("scenes_map_array[%d] BGM %s 未列入 asset_names", i, bgm)
			}
		}
		bg, tz := storyStringField(sc["background"]), storyStringField(sc["timezone"])
		if bg != "" {
			bundle := "storybackground/storybackground" + bg + "_" + tz
			if !accepted[bundle] {
				return fmt.Sprintf("scenes_map_array[%d] 缺背景资源 %s", i, bundle)
			}
		}
	}
	return ""
}

// storyActorID 容错读角色 id（JSON number 或数字串）；>0 才视为有效。
func storyActorID(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n > 0 && n == float64(int(n)) {
			return int(n), true
		}
	case string:
		if i, err := strconv.Atoi(n); err == nil && i > 0 {
			return i, true
		}
	}
	return 0, false
}

// storyStringField 容错读字符串字段（原数据为串；数字兜底转十进制）。
func storyStringField(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		if s == float64(int(s)) {
			return strconv.Itoa(int(s))
		}
		return strconv.FormatFloat(s, 'f', -1, 64)
	}
	return ""
}

// acceptedAssetNames 生效清单名集 = 基线清单
// （serve_asset_info_android4_v2_short.txt）∪ RemoteAssets 已扫描快照
// entries。RemoteAssets 为 nil 或未 Scanned 时只用基线。
// 基线行：@分隔、跳过 version/ios_version/android_version/maintenance
// 头行，资产行 `后缀,typeIdx,...` → 名 = assetTypes[typeIdx].prefix + 后缀。
func acceptedAssetNames(dep Deps) map[string]bool {
	names := map[string]bool{}
	if raw, err := os.ReadFile(filepath.Join(dep.AssetsDir, assetInfoFile)); err == nil {
		for _, row := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "@") {
			f := strings.Split(row, ",")
			if len(f) < 5 {
				continue // 头行/残缺行
			}
			idx, ierr := strconv.Atoi(f[1])
			if ierr != nil || idx < 0 || idx >= len(assetTypes) {
				continue
			}
			names[assetTypes[idx].prefix+f[0]] = true
		}
	}
	if dep.RemoteAssets != nil && dep.RemoteAssets.Scanned() {
		for _, e := range dep.RemoteAssets.Report().Entries {
			names[e.Name] = true
		}
	}
	return names
}
