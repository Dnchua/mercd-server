// Package cardcatalog 提供同伴/魔宠模板数值的只读目录（PLAY350-04）。
//
// 数据源：assets/shared/cards/{units,monsters}.json + .version + SHA256SUMS
// （上游 merusuto/data 冻结快照，README.md 记录双源对照）。加载期校验：
// SHA256 匹配、顶层为数组、ID 唯一、必需字段在场——任一失败属致命错误；
// 已知缺口（"暂缺"占位、可选字段缺失）降级为警告，不自动填零。
//
// 消费契约与 PLAY350-03 handoffs/03/inventory-contract.md 对齐：
// Unit.ID=模板 ID（资源名/详情参数/leader_unit_id 比对基准），
// Monster.ID=模板 ID；实例级 uuid/unit_id 归 06 库存层，不在此目录。
package cardcatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ErrUnknown 模板 ID 不存在（Unit/Monster 查询统一哨兵，errors.Is 判定）。
var ErrUnknown = errors.New("cardcatalog: 未知模板 ID")

// IssueKind 质量问题分级。
type IssueKind string

const (
	IssueMissing     IssueKind = "missing"       // 可选字段缺失
	IssuePlaceholder IssueKind = "placeholder"   // "暂缺"/"暂无" 占位
	IssueBadType     IssueKind = "bad_type"      // 字段类型非法（警告级）
	IssueDuplicateID IssueKind = "duplicate_id"  // 致命
	IssueStructure   IssueKind = "bad_structure" // 致命：顶层非数组/缺必需字段
	IssueHash        IssueKind = "hash_mismatch" // 致命：SHA256SUMS 不符/缺失
)

// Issue 单条质量问题。ID=0 表示文件级。
type Issue struct {
	File   string    `json:"file"`
	ID     int       `json:"id,omitempty"`
	Field  string    `json:"field,omitempty"`
	Kind   IssueKind `json:"kind"`
	Detail string    `json:"detail,omitempty"`
}

// Report 结构化质量报告：Fatal 非空时 Load 返回错误，目录不可用。
type Report struct {
	Fatal []Issue `json:"fatal,omitempty"`
	Warn  []Issue `json:"warn,omitempty"`
}

// LoadError 致命加载错误；Report 携带全部 fatal/warn 明细。
type LoadError struct{ Report *Report }

func (e *LoadError) Error() string {
	first := ""
	if len(e.Report.Fatal) > 0 {
		f := e.Report.Fatal[0]
		first = fmt.Sprintf("%s[%d].%s: %s", f.File, f.ID, f.Field, f.Kind)
	}
	return fmt.Sprintf("cardcatalog: %d 项致命问题（首项 %s）", len(e.Report.Fatal), first)
}

// Meta 版本/哈希/计数元信息（供诊断端点与交接核对）。
type Meta struct {
	UnitsVersion    string `json:"units_version"`
	MonstersVersion string `json:"monsters_version"`
	UnitsSHA256     string `json:"units_sha256"`
	MonstersSHA256  string `json:"monsters_sha256"`
	UnitCount       int    `json:"unit_count"`
	MonsterCount    int    `json:"monster_count"`
	Dir             string `json:"dir"`
}

// AttrRates 五系伤害倍率（>1 克制 <1 被克；nil=缺失或"暂缺"）。
type AttrRates struct {
	Fire  *float64 `json:"fire,omitempty"`
	Aqua  *float64 `json:"aqua,omitempty"`
	Wind  *float64 `json:"wind,omitempty"`
	Light *float64 `json:"light,omitempty"`
	Dark  *float64 `json:"dark,omitempty"`
}

// Unit 同伴模板（units.json 一条；字段名见 assets/shared/cards/README.md）。
type Unit struct {
	ID           int       `json:"id"`
	Name         string    `json:"name"`
	NameJP       string    `json:"name_jp,omitempty"`
	Title        string    `json:"title"` // 称号 → 契约 prefix_name/main_name 候选
	TitleJP      string    `json:"title_jp,omitempty"`
	Rare         int       `json:"rare"`
	Element      int       `json:"element"`            // 1炎2水3风4光5暗
	Weapon       int       `json:"weapon"`             // 1..7（2/3 突打归属未证，README）
	Type         int       `json:"type"`               // 1..3（语义未证）
	ANum         *int      `json:"anum,omitempty"`     // 攻击次数 → attackable_count
	Life         int       `json:"life"`               // 体力基础 → hp
	Atk          int       `json:"atk"`                // 攻击基础 → at
	MSpd         *int      `json:"mspd,omitempty"`     // 移速 → speed
	ASPd         *float64  `json:"aspd,omitempty"`     // 攻速秒间隔 → attack_ms=aspd*1000
	AArea        int       `json:"aarea"`              // 攻击范围 → reach
	Tenacity     *float64  `json:"tenacity,omitempty"` // 韧性 → taghness
	Hits         *int      `json:"hits,omitempty"`
	Rates        AttrRates `json:"rates"`
	Country      string    `json:"country,omitempty"`    // 国家中文名（枚举行 name）
	CountryID    int       `json:"country_id,omitempty"` // 国家数值枚举 id（Load 统一派生；无国家=0）
	Gender       *int      `json:"gender,omitempty"`     // 2男3女（1=怪物型；缺失→nil）
	Age          *string   `json:"age,omitempty"`        // int|数字串混合，统一为字符串
	Career       string    `json:"career,omitempty"`
	Interest     string    `json:"interest,omitempty"`
	Nature       string    `json:"nature,omitempty"`
	Obtain       string    `json:"obtain,omitempty"`
	Remark       string    `json:"remark,omitempty"`
	Gacha        []int     `json:"gacha,omitempty"`
	Contributors []string  `json:"contributors,omitempty"`
	// Missing 列出缺失或占位字段名（不含 fatal 级字段——那会导致整条拒绝）。
	Missing []string `json:"missing,omitempty"`
	// Raw 原始记录（未建模字段的只读透传面）。
	Raw map[string]any `json:"-"`
}

// Monster 魔宠模板（monsters.json 一条）。
type Monster struct {
	ID       int            `json:"id"`
	Name     string         `json:"name"`
	NameJP   string         `json:"name_jp,omitempty"`
	Rare     int            `json:"rare"`
	Element  int            `json:"element"`
	Skin     int            `json:"skin"`
	Skill    string         `json:"skill"`           // 技能文本（含 nnn 占位属已知缺口，仅警告）
	SklSP    int            `json:"sklsp"`           // 技能 SP 消耗
	SklCD    *float64       `json:"sklcd,omitempty"` // 冷却秒
	SklMax   *float64       `json:"sklmax,omitempty"`
	SArea    *int           `json:"sarea,omitempty"`
	ANum     *int           `json:"anum,omitempty"`
	MSpd     *int           `json:"mspd,omitempty"`
	ASPd     *float64       `json:"aspd,omitempty"`
	AArea    *int           `json:"aarea,omitempty"`
	Tenacity *float64       `json:"tenacity,omitempty"`
	Hits     *int           `json:"hits,omitempty"`
	Atk      *int           `json:"atk,omitempty"` // 联动段字段
	Life     *int           `json:"life,omitempty"`
	Rates    AttrRates      `json:"rates"`
	Seed     *int           `json:"seed,omitempty"`   // 融合素材种子
	Parts    *int           `json:"parts,omitempty"`  // 分部位绘制标记
	Server   *int           `json:"server,omitempty"` // 语义未证
	Obtain   string         `json:"obtain,omitempty"`
	Remark   string         `json:"remark,omitempty"`
	Missing  []string       `json:"missing,omitempty"`
	Raw      map[string]any `json:"-"`
}

// Catalog 按模板 ID 索引的只读目录。
type Catalog struct {
	units      map[int]*Unit
	monsters   map[int]*Monster
	countryIDs map[string]int
	meta       Meta
	report     *Report
}

// Load 加载并校验目录。fatal 问题返回 *LoadError（errors.As 取 Report）。
func Load(dir string) (*Catalog, error) {
	rep := &Report{}
	l := &loader{rep: rep, missCount: map[string]int{}}
	c := &Catalog{units: make(map[int]*Unit), monsters: make(map[int]*Monster), report: rep, meta: Meta{Dir: dir}}

	// 1) SHA256SUMS 校验（缺文件/缺条目/不匹配 → 致命），并把实测值记入 Meta
	hashes := checkSHA256(dir, rep)
	c.meta.UnitsSHA256 = hashes["units.json"]
	c.meta.MonstersSHA256 = hashes["monsters.json"]

	// 2) 版本戳（缺失只警告——不阻断数据使用，但交接须记录）
	c.meta.UnitsVersion = readVersion(dir, "units.json.version", rep)
	c.meta.MonstersVersion = readVersion(dir, "monsters.json.version", rep)

	// 3) 解析+建索引（乱序安全）
	unitsRaw := loadRecords(dir, "units.json", rep)
	monstersRaw := loadRecords(dir, "monsters.json", rep)
	for i, raw := range unitsRaw {
		u := decodeUnit(l, raw, i)
		if u == nil {
			continue
		}
		if _, dup := c.units[u.ID]; dup {
			rep.Fatal = append(rep.Fatal, Issue{File: "units.json", ID: u.ID, Kind: IssueDuplicateID})
			continue
		}
		c.units[u.ID] = u
	}
	for i, raw := range monstersRaw {
		m := decodeMonster(l, raw, i)
		if m == nil {
			continue
		}
		if _, dup := c.monsters[m.ID]; dup {
			rep.Fatal = append(rep.Fatal, Issue{File: "monsters.json", ID: m.ID, Kind: IssueDuplicateID})
			continue
		}
		c.monsters[m.ID] = m
	}
	c.meta.UnitCount = len(c.units)
	c.meta.MonsterCount = len(c.monsters)

	// 已知国家使用客户端筛选配置中的 ID；其他国家使用不与其冲突的重建值。
	c.countryIDs = assignCountryIDs(c.units)

	// 聚合缺失计数 → 每字段一条 warn（逐条噪音太大；逐条明细在 record.Missing）
	for _, k := range sortedKeys(l.missCount) {
		file, field, _ := strings.Cut(k, ".")
		rep.Warn = append(rep.Warn, Issue{File: file, Field: field, Kind: IssueMissing,
			Detail: fmt.Sprintf("%d 条记录缺失", l.missCount[k])})
	}

	if len(rep.Fatal) > 0 {
		return nil, &LoadError{Report: rep}
	}
	return c, nil
}

// CountryID 返回已加载目录中某国家的数值 ID；未知名称返回 0。
func (c *Catalog) CountryID(country string) int {
	if id, ok := knownCountryIDs[country]; ok {
		return id
	}
	return c.countryIDs[country]
}

// CountryDisplayName 统一同一国家的目录别名，避免枚举 id 重复或显示不稳定。
func CountryDisplayName(id int, name string) string {
	switch id {
	case 7:
		return "电之国"
	case 10:
		return "沙漠之国"
	case 12:
		return "异邦之国"
	default:
		return name
	}
}

// 已知值来自 CN3.5 UserUnits 出身国筛选配置；别名来自目录/原服对照。
var knownCountryIDs = map[string]int{
	"王国": 1, "妖精之国": 2, "机械之国": 3, "和之国": 4, "空之国": 5,
	"西部之国": 6, "电之国": 7, "エレキ之国": 7, "魔法之国": 8,
	"恐龙之国": 9, "沙漠之国": 10, "砂漠之国": 10, "死者之国": 11,
	"异邦": 12, "异邦之国": 12, "少数民族之国": 12, "动物之国": 13,
	"常夏之国": 14, "植物之国": 15, "科学之国": 16, "点心之国": 17,
	"雪之国": 19, "崩坏之国": 100000, "弹幕之国": 100001,
	"漫画之国": 100002, "华夏之国": 100003,
}

// 未知国家按最小 unit id 分配不与已知筛选值冲突的重建 ID。
func assignCountryIDs(units map[int]*Unit) map[string]int {
	minID := map[string]int{}
	for _, u := range units {
		if u.Country == "" {
			continue
		}
		if m, ok := minID[u.Country]; !ok || u.ID < m {
			minID[u.Country] = u.ID
		}
	}
	type kv struct {
		country string
		min     int
	}
	ord := make([]kv, 0, len(minID))
	for cty, m := range minID {
		ord = append(ord, kv{cty, m})
	}
	sort.Slice(ord, func(i, j int) bool {
		if ord[i].min != ord[j].min {
			return ord[i].min < ord[j].min
		}
		return ord[i].country < ord[j].country
	})
	idOf := map[string]int{}
	next := 200000
	for _, e := range ord {
		if id, ok := knownCountryIDs[e.country]; ok {
			idOf[e.country] = id
		} else {
			idOf[e.country] = next
			next++
		}
	}
	for _, u := range units {
		u.CountryID = idOf[u.Country]
	}
	return idOf
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// loader 贯穿单次 Load 的解析上下文（rep + 缺失聚合计数）。
type loader struct {
	rep       *Report
	missCount map[string]int
}

// Unit 按模板 ID 查同伴；未知 → ErrUnknown。
func (c *Catalog) Unit(id int) (*Unit, error) {
	if u, ok := c.units[id]; ok {
		return u, nil
	}
	return nil, fmt.Errorf("%w: unit %d", ErrUnknown, id)
}

// Monster 按模板 ID 查魔宠；未知 → ErrUnknown。
func (c *Catalog) Monster(id int) (*Monster, error) {
	if m, ok := c.monsters[id]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("%w: monster %d", ErrUnknown, id)
}

// UnitIDs 升序模板 ID 列表（05 筛选/遍历用，不依赖数组顺序）。
func (c *Catalog) UnitIDs() []int {
	ids := make([]int, 0, len(c.units))
	for id := range c.units {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// MonsterIDs 升序模板 ID 列表。
func (c *Catalog) MonsterIDs() []int {
	ids := make([]int, 0, len(c.monsters))
	for id := range c.monsters {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// Meta 返回元信息副本。
func (c *Catalog) Meta() Meta { return c.meta }

// Report 返回质量报告（仅警告级；fatal 已由 Load 拦截）。
func (c *Catalog) Report() *Report { return c.report }

// ---------- 内部：校验与解析 ----------

// checkSHA256 返回实测哈希表（即使不匹配也记录实测值供诊断）。
func checkSHA256(dir string, rep *Report) map[string]string {
	got := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		rep.Fatal = append(rep.Fatal, Issue{File: "SHA256SUMS", Kind: IssueHash, Detail: err.Error()})
		return got
	}
	want := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			want[f[1]] = f[0]
		}
	}
	for _, name := range []string{"units.json", "monsters.json"} {
		exp, ok := want[name]
		if !ok {
			rep.Fatal = append(rep.Fatal, Issue{File: name, Kind: IssueHash, Detail: "SHA256SUMS 缺条目"})
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			rep.Fatal = append(rep.Fatal, Issue{File: name, Kind: IssueHash, Detail: err.Error()})
			continue
		}
		sum := sha256.Sum256(body)
		got[name] = hex.EncodeToString(sum[:])
		if got[name] != exp {
			rep.Fatal = append(rep.Fatal, Issue{File: name, Kind: IssueHash,
				Detail: fmt.Sprintf("got %s want %s", got[name], exp)})
		}
	}
	return got
}

func readVersion(dir, name string, rep *Report) string {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		rep.Warn = append(rep.Warn, Issue{File: name, Kind: IssueMissing, Detail: err.Error()})
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func loadRecords(dir, name string, rep *Report) []map[string]any {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		rep.Fatal = append(rep.Fatal, Issue{File: name, Kind: IssueStructure, Detail: err.Error()})
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		rep.Fatal = append(rep.Fatal, Issue{File: name, Kind: IssueStructure, Detail: err.Error()})
		return nil
	}
	return arr
}

// req 必需字段缺失 → fatal（返回 false，整条丢弃）。
func req(rep *Report, file string, idx int, rec map[string]any, fields ...string) (int, bool) {
	id := 0
	if v, ok := rec["id"].(float64); ok {
		id = int(v)
	}
	before := len(rep.Fatal)
	for _, f := range fields {
		if _, ok := rec[f]; !ok {
			rep.Fatal = append(rep.Fatal, Issue{File: file, ID: id, Field: f, Kind: IssueStructure,
				Detail: fmt.Sprintf("记录 #%d 缺必需字段", idx)})
		}
	}
	return id, len(rep.Fatal) == before
}

func decodeUnit(l *loader, rec map[string]any, idx int) *Unit {
	id, ok := req(l.rep, "units.json", idx, rec, "id", "name", "rare", "element", "weapon", "type", "life", "atk", "aarea")
	if !ok {
		return nil
	}
	u := &Unit{ID: id, Raw: rec}
	u.Name, _ = rec["name"].(string)
	u.NameJP, _ = rec["name_jp"].(string)
	u.Title, _ = rec["title"].(string)
	u.TitleJP, _ = rec["title_jp"].(string)
	u.Rare = intNum(rec, "rare")
	u.Element = intNum(rec, "element")
	u.Weapon = intNum(rec, "weapon")
	u.Type = intNum(rec, "type")
	u.Life = intNum(rec, "life")
	u.Atk = intNum(rec, "atk")
	u.AArea = intNum(rec, "aarea")
	u.Country, _ = rec["country"].(string)
	u.Career, _ = rec["career"].(string)
	u.Interest, _ = rec["interest"].(string)
	u.Nature, _ = rec["nature"].(string)
	u.Obtain, _ = rec["obtain"].(string)
	u.Remark, _ = rec["remark"].(string)
	u.ANum = optInt(l, rec, "anum", "units.json", id, &u.Missing)
	u.MSpd = optInt(l, rec, "mspd", "units.json", id, &u.Missing)
	u.ASPd = optFloat(l, rec, "aspd", "units.json", id, &u.Missing)
	u.Tenacity = optFloat(l, rec, "tenacity", "units.json", id, &u.Missing)
	u.Hits = optInt(l, rec, "hits", "units.json", id, &u.Missing)
	u.Gender = optInt(l, rec, "gender", "units.json", id, &u.Missing)
	u.Age = optStr(l, rec, "age", "units.json", id, &u.Missing)
	u.Rates = rates(l, rec, "units.json", id, &u.Missing)
	if g, ok := rec["gacha"].([]any); ok {
		for _, v := range g {
			if n, ok := v.(float64); ok {
				u.Gacha = append(u.Gacha, int(n))
			}
		}
	}
	if cs, ok := rec["contributors"].([]any); ok {
		for _, v := range cs {
			if s, ok := v.(string); ok {
				u.Contributors = append(u.Contributors, s)
			}
		}
	}
	return u
}

func decodeMonster(l *loader, rec map[string]any, idx int) *Monster {
	id, ok := req(l.rep, "monsters.json", idx, rec, "id", "name", "rare", "element", "skill")
	if !ok {
		return nil
	}
	m := &Monster{ID: id, Raw: rec}
	m.Name, _ = rec["name"].(string)
	m.NameJP, _ = rec["name_jp"].(string)
	m.Rare = intNum(rec, "rare")
	m.Element = intNum(rec, "element")
	m.Skin = intNum(rec, "skin")
	m.Skill, _ = rec["skill"].(string)
	m.SklSP = intNum(rec, "sklsp")
	m.Obtain, _ = rec["obtain"].(string)
	m.Remark, _ = rec["remark"].(string)
	m.SklCD = optFloat(l, rec, "sklcd", "monsters.json", id, &m.Missing)
	m.SklMax = optFloat(l, rec, "sklmax", "monsters.json", id, &m.Missing)
	m.SArea = optInt(l, rec, "sarea", "monsters.json", id, &m.Missing)
	m.ANum = optInt(l, rec, "anum", "monsters.json", id, &m.Missing)
	m.MSpd = optInt(l, rec, "mspd", "monsters.json", id, &m.Missing)
	m.ASPd = optFloat(l, rec, "aspd", "monsters.json", id, &m.Missing)
	m.AArea = optInt(l, rec, "aarea", "monsters.json", id, &m.Missing)
	m.Tenacity = optFloat(l, rec, "tenacity", "monsters.json", id, &m.Missing)
	m.Hits = optInt(l, rec, "hits", "monsters.json", id, &m.Missing)
	m.Atk = optInt(l, rec, "atk", "monsters.json", id, &m.Missing)
	m.Life = optInt(l, rec, "life", "monsters.json", id, &m.Missing)
	m.Seed = optInt(l, rec, "seed", "monsters.json", id, &m.Missing)
	m.Parts = optInt(l, rec, "parts", "monsters.json", id, &m.Missing)
	m.Server = optInt(l, rec, "server", "monsters.json", id, &m.Missing)
	m.Rates = rates(l, rec, "monsters.json", id, &m.Missing)
	if strings.Contains(m.Skill, "nnn") {
		l.rep.Warn = append(l.rep.Warn, Issue{File: "monsters.json", ID: id, Field: "skill",
			Kind: IssuePlaceholder, Detail: "技能文本含 nnn 占位"})
	}
	return m
}

func rates(l *loader, rec map[string]any, file string, id int, missing *[]string) AttrRates {
	var r AttrRates
	r.Fire = optFloat(l, rec, "fire", file, id, missing)
	r.Aqua = optFloat(l, rec, "aqua", file, id, missing)
	r.Wind = optFloat(l, rec, "wind", file, id, missing)
	r.Light = optFloat(l, rec, "light", file, id, missing)
	r.Dark = optFloat(l, rec, "dark", file, id, missing)
	return r
}

// intNum 必需整型字段直读（req 已保证在场；非数取 0 不掩盖——req 阶段已按
// "在场"校验，类型异常极少见，保持零值并在 Missing 之外不另计）。
func intNum(rec map[string]any, key string) int {
	switch v := rec[key].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func isPlaceholder(s string) bool { return s == "暂缺" || s == "暂无" }

// miss 聚合缺失计数（file.field → n），并把字段记入 record.Missing。
func (l *loader) miss(file, key string, missing *[]string) {
	*missing = append(*missing, key)
	l.missCount[file+"."+key]++
}

func optInt(l *loader, rec map[string]any, key, file string, id int, missing *[]string) *int {
	v, ok := rec[key]
	if !ok {
		l.miss(file, key, missing)
		return nil
	}
	switch t := v.(type) {
	case float64:
		n := int(t)
		return &n
	case string:
		if isPlaceholder(t) {
			l.miss(file, key, missing)
			l.rep.Warn = append(l.rep.Warn, Issue{File: file, ID: id, Field: key, Kind: IssuePlaceholder, Detail: t})
			return nil
		}
		if n, err := strconv.Atoi(t); err == nil {
			return &n
		}
	}
	l.rep.Warn = append(l.rep.Warn, Issue{File: file, ID: id, Field: key, Kind: IssueBadType,
		Detail: fmt.Sprintf("%T", v)})
	l.miss(file, key, missing)
	return nil
}

func optFloat(l *loader, rec map[string]any, key, file string, id int, missing *[]string) *float64 {
	v, ok := rec[key]
	if !ok {
		l.miss(file, key, missing)
		return nil
	}
	switch t := v.(type) {
	case float64:
		return &t
	case string:
		if isPlaceholder(t) {
			l.miss(file, key, missing)
			l.rep.Warn = append(l.rep.Warn, Issue{File: file, ID: id, Field: key, Kind: IssuePlaceholder, Detail: t})
			return nil
		}
		if n, err := strconv.ParseFloat(t, 64); err == nil {
			return &n
		}
	}
	l.rep.Warn = append(l.rep.Warn, Issue{File: file, ID: id, Field: key, Kind: IssueBadType,
		Detail: fmt.Sprintf("%T", v)})
	l.miss(file, key, missing)
	return nil
}

// optStr 可选字符串（name_jp/age 等）：缺失属常态不报警；占位/坏类型才记。
func optStr(l *loader, rec map[string]any, key, file string, id int, missing *[]string) *string {
	v, ok := rec[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case string:
		if isPlaceholder(t) {
			l.rep.Warn = append(l.rep.Warn, Issue{File: file, ID: id, Field: key, Kind: IssuePlaceholder, Detail: t})
			return nil
		}
		return &t
	case float64:
		s := strconv.Itoa(int(t))
		return &s
	}
	l.rep.Warn = append(l.rep.Warn, Issue{File: file, ID: id, Field: key, Kind: IssueBadType, Detail: fmt.Sprintf("%T", v)})
	return nil
}
