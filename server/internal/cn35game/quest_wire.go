// quest_wire.go STORY350-03：剧情关卡目录读面——静态 catalog
// （serve_quest_catalog.json）× 账号进度（QuestStore）组装成
// stages / areas_<stageId> / quests_<areaId> 缓存键信封。
//
// 契约锚点（artifacts/tasks/story350-01/run/field-matrix.md）：
//   - 响应必须是 {"data":{<key>:…}} 信封才会进客户端缓存
//     （ResponseFilter→DataCache.UpdateDataByResponse，DataCache.cs:397-404）。
//   - quests_<areaId> 每关必须恒下发 normal_clear/hard_clear/very_hard_clear
//     ——客户端 int.Parse(s()) 硬消费，缺席即 FormatException 崩行构建
//     （AreaSelectParts.cs:109-111、AreaListButton.cs:152-170）。
//   - stage 行导航字段 area_select_message/area_select_option 与关卡行
//     quest_select_message/quest_select_option 是本端点注入的协约字段名
//     （对称命名提案经 01 交接；CurrentQuestButtonParts.cs:72 已证
//     quest_select_option 键名）。子文档形状 = UnitFriendshipListParts.cs:248
//     先例 {"parts":{...},"requireData":{...}}。
//   - 端点路径服务端自定（客户端不硬编码；唯一先例形态 /areas/event?
//     event_id=N）：本实现用 /stages、/areas?stage_id=N、/quests?area_id=N。
//   - 不在本族响应下推 data.user——DataCache.Add 整键覆盖
//     （DataCache.cs:184-），部分 user 文档会清掉 ap/level 等字段；
//     进度字段由 preset_data 快照下发，执行/结算回推归 06/07。
package cn35game

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// questCatalogFile 静态关卡目录 fixture（AssetsDir 下）。
const questCatalogFile = "serve_quest_catalog.json"

// ---- catalog 静态模型（fixture 字段 → 校验后结构）----

type catalogStage struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	AssetName   string `json:"asset_name"`
	Description string `json:"description"`
}

type catalogArea struct {
	ID           int    `json:"id"`
	StageID      int    `json:"stage_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	AreaImageID  int    `json:"area_image_id"`
	AreaImage    string `json:"area_image"`
	BonusLabel   string `json:"bonus_label"`
	IsHardMode   bool   `json:"is_hard_mode"`
	NoDifficulty bool   `json:"no_difficulty"`
	// AlwaysOpen 常驻开放（资源副本；由 DB 配置合并层置位，fixture 不配）。
	AlwaysOpen bool `json:"always_open,omitempty"`
	// EventID M8 当期活动区域（serve_event_catalog.json 合并层置位）；
	// 非 0 时区域不属于剧情链，解锁走活动推进边界与时间窗。
	EventID int `json:"-"`
}

type catalogQuest struct {
	ID                    int                           `json:"id"`
	AreaID                int                           `json:"area_id"`
	Name                  string                        `json:"name"`
	AttrID                string                        `json:"attr_id"`
	BossLabel             string                        `json:"boss_label"`
	MonsterLabel          string                        `json:"monster_label"`
	WaveLabel             string                        `json:"wave_label"`
	WaveCount             int                           `json:"wave_count"`
	NoDifficulty          bool                          `json:"no_difficulty"`
	UseAPMap              map[string]int                `json:"use_ap_map"`
	WeaponRateHash        map[string]map[string]float64 `json:"weapon_rate_hash"`
	WaveConf              map[string]any                `json:"wave_conf"`
	UnitCountLimit        int                           `json:"unit_count_limit"`
	InitialPower          int                           `json:"initial_power"`
	SeedAtackRate         float64                       `json:"seed_atack_rate"`
	UnitHelpRate          float64                       `json:"unit_help_rate"`
	Continuable           *bool                         `json:"continuable"`
	SaveCurrentQuest      bool                          `json:"save_current_quest"`
	Mode                  string                        `json:"mode"`
	BackgroundID          int                           `json:"background_id"`
	DifficultyNames       map[string]string             `json:"difficulty_names"`
	CurrentQuestAssetName string                        `json:"current_quest_asset_name"`
	RetriableEndedAt      string                        `json:"retriable_ended_at"`
	QuestSelectMessage    string                        `json:"quest_select_message"`
	// BackgroundPool 战斗背景随机池（story_encounters_build 生成；仅列
	// 客户端实际存在的 background<N>_1 套件）。execute 每次开战摇一个；
	// 空池退回 BackgroundID。
	BackgroundPool []int `json:"background_pool,omitempty"`
	// AlwaysOpen 常驻开放（资源副本合并层置位）：不受剧情进度指针门禁。
	AlwaysOpen bool `json:"always_open,omitempty"`
	// DifficultyOptionName execute 回显 quest.difficulty_option_name（周常「周六」等）。
	DifficultyOptionName string `json:"difficulty_option_name,omitempty"`
	// Kind M8 合并层置位：""=剧情/资源本，"event"=当期活动，"daily"=周常。
	Kind string `json:"-"`
	// EventID 活动关所属活动；Wday 周常关所属星期（周日=0）。
	EventID int `json:"-"`
	Wday    int `json:"-"`
	// Battle 是 07 引入的 execute/结算样本段；nil 表示该关未配战斗数据
	// （fixture 校验层要求正式剧情关必须带 battle 块，见 validateBattle）。
	Battle *catalogBattle `json:"battle"`
}

// catalogBattle 是 execute/结算所需的每关战斗段（07 样本字段）。
// appear_table 支持两种出怪项：旧式内联 params（quest0 样本）与
// story-01 紧凑式 {id,scale,powerup_rate,gauge,hp,at,rewards}——后者
// 的 params 由 execute 侧按 cardcatalog+纪念版合成（见 story_encounter.go）。
type catalogBattle struct {
	Exp             int            `json:"exp"`
	BgmName         string         `json:"bgm_name,omitempty"`
	NextPageMessage string         `json:"next_page_message,omitempty"`
	AppearTable     map[string]any `json:"appear_table"`
	AssetNamesExtra []string       `json:"asset_names_extra,omitempty"`
	// Difficulty 按难度键（normal/hard/very_hard）的数值 profile：
	// hp_bp/at_bp 千分比缩放 params.hp/at，exp/gacha_point/guild_asset
	// 覆盖会话奖励与经验。缺键按 normal/identity 处理。
	Difficulty map[string]battleDifficultyProfile `json:"difficulty,omitempty"`
	// SeedDrop 剧情魔宠掉落：每次通关一次伯努利判定，monster_id 必须
	// 出现在本关敌人列表（生成器与启动校验双门）。
	SeedDrop *battleSeedDrop `json:"seed_drop,omitempty"`
}

// battleDifficultyProfile 单难度数值 profile（story-01 草案口径）。
type battleDifficultyProfile struct {
	HpBP       int `json:"hp_bp"`       // params.hp/life 千分比
	AtBP       int `json:"at_bp"`       // params.at/atk/seed_at 千分比
	Exp        int `json:"exp"`         // 覆盖 battle.exp
	GachaPoint int `json:"gacha_point"` // 金币（结算固定下发）
	GuildAsset int `json:"guild_asset"` // 资材（结算固定下发）
}

// battleSeedDrop 剧情魔宠概率掉落配置。
type battleSeedDrop struct {
	MonsterID int            `json:"monster_id"`
	ChanceBP  map[string]int `json:"chance_bp"` // 难度键 → 万分比
}

// executeDefaults 是 execute 响应的目录级默认段（quest0 采样常量，
// provenance 见 serve_quest_catalog.json 的 _provenance）。
type executeDefaults struct {
	NextPageMessage  string         `json:"next_page_message"`
	BgmName          string         `json:"bgm_name"`
	BattleLog        *bool          `json:"battle_log"`
	FlagImageName    string         `json:"flag_image_name"`
	MainCharacter    map[string]any `json:"main_character"`
	Background       map[string]any `json:"background"`
	PowerupConf      map[string]any `json:"powerup_conf"`
	AssetNamesCommon []string       `json:"asset_names_common"`
}

type questCatalog struct {
	Stages         []catalogStage  `json:"stages"`
	HardModeStages []catalogStage  `json:"hard_mode_stages"`
	ExtraStages    []catalogStage  `json:"extra_stages"`
	Specials       []catalogStage  `json:"specials"`
	Events         []catalogStage  `json:"events"`
	Areas          []catalogArea   `json:"areas"`
	Quests         []catalogQuest  `json:"quests"`
	Defaults       executeDefaults `json:"execute_defaults"`

	areaMap  map[int]*catalogArea
	questMap map[int]*catalogQuest
}

// loadQuestCatalog 读 fixture 并做结构校验：id 唯一正数、引用完整、
// use_ap_map 三难度键齐（客户端按 diff 取 cost，缺键=0 可点——校验拒绝
// 以免静默放出免费关）。每请求重新读文件：与 presetDataHandler 同先例，
// fixture 小文件，改动即生效不需重启。
func loadQuestCatalog(assetsDir string) (*questCatalog, error) {
	raw, err := os.ReadFile(filepath.Join(assetsDir, questCatalogFile))
	if err != nil {
		return nil, fmt.Errorf("cn35 关卡目录 fixture 读取失败: %w", err)
	}
	var cat questCatalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("cn35 关卡目录 fixture 非法 JSON: %w", err)
	}
	if err := cat.validate(); err != nil {
		return nil, fmt.Errorf("cn35 关卡目录校验失败: %w", err)
	}
	return &cat, nil
}

func (c *questCatalog) validate() error {
	stageIDs := map[int]bool{}
	for _, s := range c.Stages {
		if s.ID <= 0 {
			return fmt.Errorf("stage id 非法: %d", s.ID)
		}
		if stageIDs[s.ID] {
			return fmt.Errorf("stage id 重复: %d", s.ID)
		}
		stageIDs[s.ID] = true
	}
	areaIDs := map[int]bool{}
	for _, a := range c.Areas {
		if a.ID <= 0 {
			return fmt.Errorf("area id 非法: %d", a.ID)
		}
		if areaIDs[a.ID] {
			return fmt.Errorf("area id 重复: %d", a.ID)
		}
		if !stageIDs[a.StageID] {
			return fmt.Errorf("area %d 引用未知 stage %d", a.ID, a.StageID)
		}
		areaIDs[a.ID] = true
	}
	questIDs := map[int]bool{}
	for _, q := range c.Quests {
		if q.ID <= 0 {
			return fmt.Errorf("quest id 非法: %d", q.ID)
		}
		if questIDs[q.ID] {
			return fmt.Errorf("quest id 重复: %d", q.ID)
		}
		if !areaIDs[q.AreaID] {
			return fmt.Errorf("quest %d 引用未知 area %d", q.ID, q.AreaID)
		}
		if len(q.UseAPMap) == 0 {
			return fmt.Errorf("quest %d 缺 use_ap_map（难度钮 AP 硬消费）", q.ID)
		}
		for _, d := range questDifficulties {
			if _, ok := q.UseAPMap[d]; !ok {
				return fmt.Errorf("quest %d use_ap_map 缺难度键 %s", q.ID, d)
			}
		}
		questIDs[q.ID] = true
	}
	c.areaMap = map[int]*catalogArea{}
	for i := range c.Areas {
		c.areaMap[c.Areas[i].ID] = &c.Areas[i]
	}
	c.questMap = map[int]*catalogQuest{}
	for i := range c.Quests {
		c.questMap[c.Quests[i].ID] = &c.Quests[i]
	}
	if err := c.validateBattleShape(); err != nil {
		return err
	}
	for _, ss := range [][]catalogStage{c.Stages, c.HardModeStages, c.ExtraStages, c.Specials, c.Events} {
		seen := map[int]bool{}
		for _, s := range ss {
			if s.ID <= 0 || seen[s.ID] {
				return fmt.Errorf("tab 列表 stage id 非法/重复: %d", s.ID)
			}
			seen[s.ID] = true
		}
	}
	sort.Slice(c.Stages, func(i, j int) bool { return c.Stages[i].ID < c.Stages[j].ID })
	sort.Slice(c.HardModeStages, func(i, j int) bool { return c.HardModeStages[i].ID < c.HardModeStages[j].ID })
	sort.Slice(c.ExtraStages, func(i, j int) bool { return c.ExtraStages[i].ID < c.ExtraStages[j].ID })
	sort.Slice(c.Specials, func(i, j int) bool { return c.Specials[i].ID < c.Specials[j].ID })
	sort.Slice(c.Events, func(i, j int) bool { return c.Events[i].ID < c.Events[j].ID })
	sort.Slice(c.Areas, func(i, j int) bool { return c.Areas[i].ID < c.Areas[j].ID })
	sort.Slice(c.Quests, func(i, j int) bool { return c.Quests[i].ID < c.Quests[j].ID })
	return nil
}

func (c *questCatalog) stageByID(id int) *catalogStage {
	for _, ss := range [][]catalogStage{c.Stages, c.HardModeStages,
		c.ExtraStages, c.Specials, c.Events} {
		for i := range ss {
			if ss[i].ID == id {
				return &ss[i]
			}
		}
	}
	return nil
}

// questByID 按 id 查关卡（execute/结算用；quests 已按 id 升序排序）。
func (c *questCatalog) questByID(id int) *catalogQuest {
	return c.questMap[id]
}

// areaByID 按 id 查区域。
func (c *questCatalog) areaByID(id int) *catalogArea {
	return c.areaMap[id]
}

// nextQuest 返回剧情推进意义上的下一关；AlwaysOpen 资源副本只挂在
// extra_stages/areas，不属于剧情进度链，必须跳过，避免剧情结算把进度推进
// 到 900xx 资源 ID 空间。
func (c *questCatalog) nextQuest(id int) *catalogQuest {
	for i := range c.Quests {
		if c.Quests[i].ID > id && !c.Quests[i].AlwaysOpen && c.Quests[i].Kind == "" {
			return &c.Quests[i]
		}
	}
	return nil
}

// validateBattleShape 校验 battle 块结构（仅形状，不含模板存在性——
// 模板核对在 Register 时经 validateBattleRefs 对 cardcatalog 做）。
func (c *questCatalog) validateBattleShape() error {
	for _, q := range c.Quests {
		if q.Battle == nil {
			return fmt.Errorf("quest %d 缺 battle 块（execute 无可下发内容）", q.ID)
		}
		if q.Battle.Exp < 0 {
			return fmt.Errorf("quest %d battle.exp 非法: %d", q.ID, q.Battle.Exp)
		}
		if len(q.Battle.AppearTable) == 0 {
			return fmt.Errorf("quest %d battle.appear_table 为空", q.ID)
		}
		for wkey, wv := range q.Battle.AppearTable {
			if _, err := strconv.Atoi(wkey); err != nil {
				return fmt.Errorf("quest %d appear_table 波次键非数字: %q", q.ID, wkey)
			}
			wm, ok := wv.(map[string]any)
			if !ok {
				return fmt.Errorf("quest %d appear_table.%s 非对象", q.ID, wkey)
			}
			for mkey, ev := range wm {
				if _, err := strconv.Atoi(mkey); err != nil {
					return fmt.Errorf("quest %d appear_table.%s 的 ms 键非数字: %q", q.ID, wkey, mkey)
				}
				em, ok := ev.(map[string]any)
				if !ok {
					return fmt.Errorf("quest %d appear_table.%s.%s 非对象", q.ID, wkey, mkey)
				}
				if _, hasID := em["id"]; hasID {
					if numOf(em["id"]) <= 0 {
						return fmt.Errorf("quest %d appear_table.%s.%s id 非法", q.ID, wkey, mkey)
					}
				}
				// 紧凑出怪项（无 params）必须带正数 hp/at——execute 侧
				// 以此覆盖模板基值，缺省会让 0 面板怪进战斗。
				if _, hasParams := em["params"]; !hasParams {
					if numOf(em["hp"]) <= 0 || numOf(em["at"]) <= 0 {
						return fmt.Errorf("quest %d appear_table.%s.%s 紧凑项缺 hp/at", q.ID, wkey, mkey)
					}
				}
				if rws, ok := em["rewards"]; ok {
					if _, ok := rws.([]any); !ok {
						return fmt.Errorf("quest %d appear_table.%s.%s rewards 非数组", q.ID, wkey, mkey)
					}
				}
			}
		}
	}
	return nil
}

// validateBattleRefs 启动期一致性核对：appear_table 引用的魔宠模板与
// seed 掉落 id 必须都在 cardcatalog（缺模板=配置事故，启动即失败）。
func (c *questCatalog) validateBattleRefs(cards UnitCatalog) error {
	for _, q := range c.Quests {
		if q.Battle == nil {
			continue
		}
		for _, mid := range MonsterIDsInAppearTable(q.Battle.AppearTable) {
			if _, err := cards.Monster(mid); err != nil {
				return fmt.Errorf("quest %d appear_table 引用未知魔宠 %d: %w", q.ID, mid, err)
			}
		}
		for _, r := range AggregateRewards(q.Battle.AppearTable) {
			if r.Key != "seed" {
				continue
			}
			if _, err := cards.Monster(r.ID); err != nil {
				return fmt.Errorf("quest %d seed 奖励引用未知魔宠 %d: %w", q.ID, r.ID, err)
			}
		}
		if sd := q.Battle.SeedDrop; sd != nil {
			if _, err := cards.Monster(sd.MonsterID); err != nil {
				return fmt.Errorf("quest %d seed_drop 引用未知魔宠 %d: %w", q.ID, sd.MonsterID, err)
			}
			inList := false
			for _, mid := range MonsterIDsInAppearTable(q.Battle.AppearTable) {
				if mid == sd.MonsterID {
					inList = true
					break
				}
			}
			if !inList {
				return fmt.Errorf("quest %d seed_drop 魔宠 %d 不在本关敌人列表", q.ID, sd.MonsterID)
			}
		}
	}
	return nil
}

// questsOfArea 返回某 area 的全部关卡（升序，含锁定——过滤在组装层做，
// 因为 percentage 分母要全量）。
func (c *questCatalog) questsOfArea(areaID int) []catalogQuest {
	out := []catalogQuest{}
	for _, q := range c.Quests {
		if q.AreaID == areaID {
			out = append(out, q)
		}
	}
	return out
}

// questProgressFor 读账号进度；Quests 未装配（-mem 桩场景）时按
// 初始进度 (1,1,1,0) 返回——等价于新账号，不回退共享他人进度。
func questProgressFor(dep Deps, userID string) (*QuestProgress, error) {
	if dep.Quests == nil {
		return &QuestProgress{StageID: 1, AreaID: 1, QuestID: 1, Clears: map[int]map[string]int{}}, nil
	}
	return dep.Quests.QuestProgress(userID)
}

// ---- 文档组装（静态字段 + 账号覆盖 + 导航子文档注入）----

// stageDoc 组装一条 stages 行：目录字段 + RowClick 协约字段。
// area_select_option 注入 data_key=areas_<id> 与 requireData 拉取路径——
// 进 AreaSelect 时 additionalOption.Merge 进页配置（PageBase.SetOption）。
func stageDoc(s catalogStage) map[string]any {
	doc := map[string]any{
		"id":                  s.ID,
		"name":                s.Name,
		"area_select_message": "name=AreaSelect&method=Stack",
		"area_select_option": map[string]any{
			"parts": map[string]any{
				"QustAreaSelect": map[string]any{"data_key": fmt.Sprintf("areas_%d", s.ID)},
			},
			"requireData": map[string]any{
				fmt.Sprintf("areas_%d", s.ID): map[string]any{
					"path": fmt.Sprintf("/areas?stage_id=%d", s.ID),
					// 剧情前沿推进会改变已解锁区域集合；缺 force_update 时
					// 客户端复用首次缓存的旧表，新区永远不出现（实机缺陷：
					// stage1 只显 1 区、quest 前沿卡死在新手锁内）。
					"force_update": true,
				},
			},
		},
	}
	if s.AssetName != "" {
		doc["asset_name"] = s.AssetName
	}
	if s.Description != "" {
		doc["description"] = s.Description
	}
	return doc
}

// areaDoc 组装一条 areas_<stageId> 行。percentage 服务端按全量关卡
// （含未解锁）×三难度已通关计数算出——客户端缺省推算的分母只数得下发的
// 已解锁关卡，会把达成率算高，故服务端显式下发真值。
func areaDoc(a catalogArea, quests []catalogQuest, prog *QuestProgress) map[string]any {
	doc := map[string]any{
		"id":       a.ID,
		"stage_id": a.StageID,
		"name":     a.Name,
	}
	if a.Description != "" {
		doc["description"] = a.Description
	}
	if a.AreaImage != "" {
		doc["area_image"] = a.AreaImage
	} else if a.AreaImageID > 0 {
		doc["area_image_id"] = a.AreaImageID
	}
	if a.BonusLabel != "" {
		doc["bonus_label"] = a.BonusLabel
	}
	if a.IsHardMode {
		doc["is_hard_mode"] = true
	}
	if a.NoDifficulty {
		doc["no_difficulty"] = true
	}
	if len(quests) > 0 {
		cleared := 0
		diffs := questDifficulties
		if a.NoDifficulty {
			diffs = []string{"normal"}
		}
		for _, q := range quests {
			for _, d := range diffs {
				if prog.Clears[q.ID][d] > 0 {
					cleared++
				}
			}
		}
		doc["percentage"] = cleared * 100 / (len(quests) * len(diffs))
	} else {
		doc["percentage"] = 0
	}
	return doc
}

// questDoc 组装一条 quests_<areaId> 关卡文档：静态字段直发 +
// 账号 clear 计数覆盖 + quest_select_option 导航注入。
// {diff}_clear 恒发 int（0 也要发）——客户端 int.Parse 硬消费。
func questDoc(q catalogQuest, areaID int, prog *QuestProgress) map[string]any {
	doc := map[string]any{
		"id":         q.ID,
		"area_id":    areaID,
		"name":       q.Name,
		"use_ap_map": q.UseAPMap,
	}
	for _, d := range questDifficulties {
		doc[d+"_clear"] = prog.Clears[q.ID][d]
	}
	if q.AttrID != "" {
		doc["attr_id"] = q.AttrID
	}
	if q.BossLabel != "" {
		doc["boss_label"] = q.BossLabel
	}
	if q.MonsterLabel != "" {
		doc["monster_label"] = q.MonsterLabel
	}
	if q.WaveLabel != "" {
		doc["wave_label"] = q.WaveLabel
	}
	if q.WaveCount > 0 {
		doc["wave_count"] = q.WaveCount
	}
	if q.NoDifficulty {
		doc["no_difficulty"] = true
	}
	if len(q.WeaponRateHash) > 0 {
		doc["weapon_rate_hash"] = q.WeaponRateHash
	}
	if len(q.WaveConf) > 0 {
		doc["wave_conf"] = q.WaveConf
	}
	if q.UnitCountLimit > 0 {
		doc["unit_count_limit"] = q.UnitCountLimit
	}
	doc["initial_power"] = q.InitialPower
	if q.SeedAtackRate != 0 {
		doc["seed_atack_rate"] = q.SeedAtackRate
	}
	if q.UnitHelpRate != 0 {
		doc["unit_help_rate"] = q.UnitHelpRate
	}
	if q.Continuable != nil {
		doc["continuable"] = *q.Continuable
	}
	if q.SaveCurrentQuest {
		doc["save_current_quest"] = true
	}
	if q.Mode != "" {
		doc["mode"] = q.Mode
	}
	if q.BackgroundID > 0 {
		doc["background_id"] = q.BackgroundID
	}
	if len(q.DifficultyNames) > 0 {
		doc["difficulty_names"] = q.DifficultyNames
	}
	if q.CurrentQuestAssetName != "" {
		doc["current_quest_asset_name"] = q.CurrentQuestAssetName
	}
	if q.RetriableEndedAt != "" {
		doc["retriable_ended_at"] = q.RetriableEndedAt
	}
	msg := q.QuestSelectMessage
	if msg == "" {
		msg = "name=QuestSelect&method=Stack"
	}
	doc["quest_select_message"] = msg
	doc["quest_select_option"] = questSelectOptionDoc(q, areaID)
	return doc
}

// questSelectOptionDoc 单个关卡的 QuestSelect 导航子文档（P0 写面复用）：
// /quests?area_id= 列表注入与 /quests/current_quest_additional_option 同源——
// 两处的 parts.DifficultySelect.data_key/id 与 requireData 形状必须一致，
// 否则同一关在两入口下的 execute 检索行为不同。
func questSelectOptionDoc(q catalogQuest, areaID int) map[string]any {
	return map[string]any{
		"parts": map[string]any{
			// 键必须是 app_conf 页配置的**实例键**而非 prefab 类名：
			// QuestSelect.parts.DifficultySelect → prefab QuestSelectParts
			// （实机 2026-09-28：写成 QuestSelectParts 时部件 option 拿不到
			// data_key/id，questDoc 检索失败 → /quests/execute/.json 404）。
			"DifficultySelect": map[string]any{
				"data_key": fmt.Sprintf("quests_%d", areaID),
				"id":       strconv.Itoa(q.ID),
			},
		},
		"requireData": map[string]any{
			fmt.Sprintf("quests_%d", areaID): map[string]any{
				"path": fmt.Sprintf("/quests?area_id=%d", areaID),
				// 同上：通关计数/新解锁关卡随结算变化，缓存必须每次进入
				// 刷新，否则旧 quests_<areaId> 让刚解锁的关卡不可见。
				"force_update": true,
			},
		},
	}
}

// ---- 解锁判定（见 quest.go 包内注释的规则定义）----

func areaUnlocked(a catalogArea, prog *QuestProgress) bool {
	if a.AlwaysOpen {
		return true
	}
	if a.StageID < prog.StageID {
		return true
	}
	return a.StageID == prog.StageID && a.ID <= prog.AreaID
}

// storyQuestID 判定进度指针是否仍指向剧情链内的关卡。若旧库曾被污染到
// 900xx 资源 ID，后续剧情访问/execute 应 fail-closed，而不是把所有 id
// 小于 900xx 的剧情关一次性视为已解锁。
func (c *questCatalog) storyQuestID(id int) bool {
	q := c.questMap[id]
	return q != nil && !q.AlwaysOpen && q.Kind == ""
}

func (c *questCatalog) questUnlocked(q catalogQuest, prog *QuestProgress) bool {
	return q.AlwaysOpen || (c.storyQuestID(prog.QuestID) && q.ID <= prog.QuestID)
}

// reqParam 取业务参数：requireData 拉取的参数在 e= 加密 query 里
// （req.Query），POST 表单内也可能带——先 query 后 form。
func reqParam(req *cn35wire.GameRequest, key string) string {
	if v := req.Query.Get(key); v != "" {
		return v
	}
	return req.Form.Get(key)
}

// ---- handlers ----

// stagesHandler GET|POST /stages：StageSelect 页 requireData.stages 的
// 拉取端点。一次下发全部五个 tab 键（缺 tab 键=空表安全）；stages 已按
// 账号进度过滤（客户端 ListFilterComponent 同式再过滤，等价双门）。
func stagesHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		cat, err := loadMergedCatalog(dep)
		if err != nil {
			return nil, err
		}
		prog, err := questProgressFor(dep, req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 剧情进度读取失败: %w", err)
		}
		stages := []any{}
		for _, s := range cat.Stages {
			if s.ID <= prog.StageID {
				stages = append(stages, stageDoc(s))
			}
		}
		// 其余 tab 同样按对应进度指针过滤（catalog 当前未配 hard/extra/
		// special/event 目录 → 恒空表；补充条目后过滤即生效）。
		// 注意：hard_mode 行引入时需换 data_key 前缀（如 hard_areas_<id>）
		// 以免与普通区域键串缓存——本批目录无 hard 条目，仅留过滤钩子。
		hard := []any{}
		for _, s := range cat.HardModeStages {
			if s.ID <= prog.HardModeStageID {
				hard = append(hard, stageDoc(s))
			}
		}
		mkAll := func(ss []catalogStage) []any {
			out := []any{}
			for _, s := range ss {
				out = append(out, stageDoc(s))
			}
			return out
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "stages",
			"stage_id": prog.StageID, "stages": len(stages),
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{
			"stages":           stages,
			"hard_mode_stages": hard,
			"extra_stages":     mkAll(cat.ExtraStages),
			"specials":         mkAll(cat.Specials),
			"events":           mkAll(cat.Events),
		}})
	})
}

// areasHandler GET|POST /areas?stage_id=N：AreaSelect 的
// areas_<stageId> 键 + 同页每个已解锁区域的 quests_<areaId> 键
// 一并发下（S2 信封：一次响应填多键，DataCache 逐键 Add）。
// 锁定/未知 stage → 200 空列表（客户端渲染空区域页）；
// stage_id 缺失/非数字 → 错误（协议级参数错误，客户端错误模态回退）。
func areasHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		stageID, err := strconv.Atoi(reqParam(req, "stage_id"))
		if err != nil {
			return nil, fmt.Errorf("cn35 /areas 缺/坏 stage_id: %q", reqParam(req, "stage_id"))
		}
		cat, err := loadMergedCatalog(dep)
		if err != nil {
			return nil, err
		}
		prog, err := questProgressFor(dep, req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 剧情进度读取失败: %w", err)
		}
		key := fmt.Sprintf("areas_%d", stageID)
		areas := []any{}
		data := map[string]any{}
		if cat.stageByID(stageID) != nil {
			for _, a := range cat.Areas {
				if a.StageID != stageID || !areaUnlocked(a, prog) {
					continue
				}
				quests := cat.questsOfArea(a.ID)
				areas = append(areas, areaDoc(a, quests, prog))
				unlocked := []any{}
				for _, q := range quests {
					if cat.questUnlocked(q, prog) {
						unlocked = append(unlocked, questDoc(q, a.ID, prog))
					}
				}
				data[fmt.Sprintf("quests_%d", a.ID)] = unlocked
			}
		}
		data[key] = areas
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "areas",
			"stage_id": stageID, "areas": len(areas),
		}
		return cn35wire.MarshalJSON(map[string]any{"data": data})
	})
}

// questsHandler GET|POST /quests?area_id=N：单区域 quests_<areaId>
// 键的独立拉取（QuestSelect 进入时经 quest_select_option.requireData
// 刷新通关标记；已缓存则客户端跳过本请求——DataLoad 无 force 不重拉）。
// 锁定/未知 area → 空列表。
func questsHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		areaID, err := strconv.Atoi(reqParam(req, "area_id"))
		if err != nil {
			return nil, fmt.Errorf("cn35 /quests 缺/坏 area_id: %q", reqParam(req, "area_id"))
		}
		cat, err := loadMergedCatalog(dep)
		if err != nil {
			return nil, err
		}
		prog, err := questProgressFor(dep, req.UserID)
		if err != nil {
			return nil, fmt.Errorf("cn35 剧情进度读取失败: %w", err)
		}
		list := []any{}
		for _, a := range cat.Areas {
			if a.ID != areaID {
				continue
			}
			// M8 活动区域：解锁走活动时间窗 + 推进边界（客户端按
			// user_event_condition 过滤），不受剧情指针约束。
			if a.EventID != 0 && cat.events != nil {
				if ev := cat.events.byID[a.EventID]; ev != nil {
					list = eventAreaQuestDocs(dep, cat, ev, areaID, prog)
				}
				break
			}
			if areaUnlocked(a, prog) {
				for _, q := range cat.questsOfArea(areaID) {
					if cat.questUnlocked(q, prog) {
						list = append(list, questDoc(q, areaID, prog))
					}
				}
			}
			break // area_id 唯一
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "quests",
			"area_id": areaID, "quests": len(list),
		}
		return cn35wire.MarshalJSON(map[string]any{"data": map[string]any{
			fmt.Sprintf("quests_%d", areaID): list,
		}})
	})
}

// currentQuestAdditionalOptionHandler
// /quests/current_quest_additional_option?quest_id=<questId>（P0 写面）：
// 返回 {status:"success", current_quest_additional_option:{parts,requireData},
// data:{...}}——CurrentQuestButtonParts.cs:56-73 发 quest_id，回调内
// `new Document(_json).has("current_quest_additional_option")` 直接读
// **响应顶层**（Document 不解包 data），顶层缺键则 ShowPage 不触发；
// 同名子文档一并进 data，经 UpdateDataByResponse 进缓存。parts 键集与
// quest_select_option 的 DifficultySelect 必须一致，故与 questDoc 共用
// questSelectOptionDoc（一处实现两处下发）。
// quest 属锁定区域也照发：按钮渲染只需要 option 文档（服务端重建语义）。
func currentQuestAdditionalOptionHandler(dep Deps) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		rawID := reqParam(req, "quest_id")
		questID, err := strconv.Atoi(rawID)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "current_quest_additional_option",
			"quest_id": rawID,
		}
		if err != nil || questID <= 0 {
			return cn35wire.MarshalJSON(trainingError(
				"参数非法", "任务参数无效，请重新打开剧情页面。"))
		}
		cat, err := loadMergedCatalog(dep)
		if err != nil {
			return nil, err
		}
		for _, a := range cat.Areas {
			for _, q := range cat.questsOfArea(a.ID) {
				if q.ID == questID {
					opt := questSelectOptionDoc(q, a.ID)
					return cn35wire.MarshalJSON(map[string]any{
						"status":                          "success",
						"current_quest_additional_option": opt,
						"data": map[string]any{
							"current_quest_additional_option": opt,
						},
					})
				}
			}
		}
		return cn35wire.MarshalJSON(trainingError(
			"剧情未开放", "该剧情关卡当前未开放。"))
	})
}
