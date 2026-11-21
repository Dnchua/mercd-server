// resource_quest.go ITEM350-05/06：把已发布的资源副本配置（素材本/金币本）
// 合并进关卡目录与战斗链路。
//
// 形态（设计 Note 2026-09-30-cn35-items-resources）：
//   - 24 关常驻于 extra_stages tab（stage_id 900=素材收集 / 901=金币本）；
//     `AlwaysOpen` 绕过剧情进度锁（areaUnlocked/questUnlocked/execute 门禁）。
//   - 波次表 → appear_table 出场项：params 以 cardcatalog 魔宠模板 +
//     memorial 行为层合成（与 BattleMonsterDoc 同源），hp/at/seed_at 按
//     波次千分比倍率缩放。
//   - 掉落在 execute 建档时按生效版本独立判定（chance_bp 万分比 +
//     [min,max] 闭区间），折算进会话奖励快照；结算沿用快照入账
//     （item→cn35_player_items；gacha_point→余额），战斗中途调参不影响
//     已开战会话。
package cn35game

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	mathrand "math/rand"
	"sort"
	"strconv"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

// resourceStageIDs 资源副本 stage 键（素材/金币/符文本三个常驻 tab 行）。
var resourceStageIDs = map[int]string{
	900: "素材收集",
	901: "金币本",
	902: "符文本",
}

// resourceAreaNames 资源副本区域名（按属性 + 金币 + 符文）。
var resourceAreaNames = map[int]string{
	9001: "火之洞窟", 9002: "水之洞窟", 9003: "风之洞窟",
	9004: "光之洞窟", 9005: "暗之洞窟", 9006: "黄金乡", 9007: "符文猎场",
}

// resourceAttrOfQuest 素材本属性：quest_id=90000+elem*10+tier → elem。
// 非素材关（gold_*，906xx）返回 0。
func resourceAttrOfQuest(questID int) int {
	if questID >= 90011 && questID <= 90054 && questID < 90600 {
		return (questID - 90000) / 10
	}
	return 0
}

// mergedCatalog 关卡目录 + 资源副本生效配置合并视图。
type mergedCatalog struct {
	*questCatalog
	// drops 按 quest_id 索引的掉落规则（execute 摇奖用）。
	drops map[int][]ResourceDrop
	// runeSpecs 按 quest_id 索引的符文本逐怪掉落规格（execute 摇奖用）。
	runeSpecs map[int]*RuneDropSpec
	// version 生效配置版本；0=无资源副本（未发布或依赖未装配）。
	version int64
	// itemContent 掉落 item_key → 客户端 content_id 解析表。
	itemContent map[string]int
	// events M8 活动/周常目录（nil=未装配 serve_event_catalog.json）。
	events *eventCatalog
	// eventDrops 周常素材掉落（不走资源本版本台账，ConfigVersion 恒 0）。
	eventDrops map[int][]ResourceDrop
}

// loadMergedCatalog 读静态关卡目录并按生效资源配置合并。Resources/Items
// 未装配或无发布版本时退回静态目录（不改变剧情行为）。
func loadMergedCatalog(dep Deps) (*mergedCatalog, error) {
	cat, err := loadQuestCatalog(dep.AssetsDir)
	if err != nil {
		return nil, err
	}
	out := &mergedCatalog{questCatalog: cat}
	// 活动目录故障不得拖垮剧情目录：降级为未装配（启动期已校验并告警）。
	ec, err := loadEventCatalog(dep.AssetsDir)
	if err != nil {
		dep.log().Warn("CN3.5 活动目录不可用，活动/周常降级关闭", "err", err)
		ec = nil
	}
	defer out.mergeEventCatalog(ec)
	if dep.Resources == nil || dep.Items == nil {
		if ec != nil && dep.Items != nil {
			if err := out.loadItemContent(dep); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	cfg, err := dep.Resources.ActiveResourceConfig()
	if err != nil {
		return nil, fmt.Errorf("cn35 资源副本配置读取失败: %w", err)
	}
	if err := out.loadItemContent(dep); err != nil {
		return nil, err
	}
	if cfg == nil {
		return out, nil
	}
	if err := out.mergeResourceConfig(cfg, dep.Catalog); err != nil {
		return nil, err
	}
	out.version = cfg.Version
	return out, nil
}

// loadItemContent 物品 item_key → content_id 解析表（资源本/周常掉落共用）。
func (m *mergedCatalog) loadItemContent(dep Deps) error {
	defs, err := dep.Items.ItemCatalog()
	if err != nil {
		return fmt.Errorf("cn35 物品目录读取失败: %w", err)
	}
	m.itemContent = map[string]int{}
	for _, d := range defs {
		if d.Enabled {
			m.itemContent[d.ItemKey] = d.ContentID
		}
	}
	return nil
}

// mergeResourceConfig 把生效快照并入目录：stage/area/quest 行 +
// appear_table（波次→魔宠模板）。引用未知怪物的关卡整体剔除（不下发
// 半残关卡），聚合诊断错误只在全部剔除时返回。
func (m *mergedCatalog) mergeResourceConfig(cfg *ResourceConfig, cards UnitCatalog) error {
	if len(cfg.Quests) == 0 {
		return nil
	}
	m.drops = map[int][]ResourceDrop{}
	m.runeSpecs = map[int]*RuneDropSpec{}
	stagesSeen := map[int]bool{}
	areasSeen := map[int]bool{}
	var skipped []string
	quests := append([]ResourceQuestDraft(nil), cfg.Quests...)
	sort.Slice(quests, func(i, j int) bool {
		return quests[i].QuestID < quests[j].QuestID
	})
	for _, q := range quests {
		if !q.Open {
			continue
		}
		appear, err := buildResourceAppearTable(q, cards)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s:%v", q.QuestKey, err))
			continue
		}
		if !stagesSeen[q.StageID] {
			name, ok := resourceStageIDs[q.StageID]
			if !ok {
				skipped = append(skipped, fmt.Sprintf("%s:未知 stage %d", q.QuestKey, q.StageID))
				continue
			}
			stagesSeen[q.StageID] = true
			m.ExtraStages = append(m.ExtraStages, catalogStage{
				ID: q.StageID, Name: name,
			})
		}
		if !areasSeen[q.AreaID] {
			name, ok := resourceAreaNames[q.AreaID]
			if !ok {
				name = fmt.Sprintf("资源区%d", q.AreaID)
			}
			areasSeen[q.AreaID] = true
			m.Areas = append(m.Areas, catalogArea{
				ID: q.AreaID, StageID: q.StageID, Name: name,
				AreaImageID: 1, NoDifficulty: true, AlwaysOpen: true,
			})
		}
		initialPower := q.InitialPower
		if initialPower <= 0 {
			initialPower = 500
		}
		questsArr := catalogQuest{
			ID: q.QuestID, AreaID: q.AreaID, Name: q.Name,
			NoDifficulty: true, AlwaysOpen: true,
			UseAPMap:       map[string]int{"normal": q.AP},
			UnitCountLimit: 5, InitialPower: initialPower,
			WeaponRateHash: resourceWeaponRateHash(q),
			SeedAtackRate:  1.0, UnitHelpRate: 1.0,
			Continuable: boolPtr(true),
			Mode:        "quest", BackgroundID: 1,
			DifficultyNames: map[string]string{"0": "挑战"},
			WaveCount:       resourceWaveCount(q.Waves),
			WaveConf:        resourceWaveConf(q),
			Battle: &catalogBattle{
				Exp:         q.Exp,
				AppearTable: appear,
			},
		}
		if attr := resourceAttrOfQuest(q.QuestID); attr > 0 {
			questsArr.AttrID = strconv.Itoa(attr)
		}
		m.Quests = append(m.Quests, questsArr)
		m.questMap[q.QuestID] = &m.Quests[len(m.Quests)-1]
		m.drops[q.QuestID] = append([]ResourceDrop(nil), q.Drops...)
		if q.RuneDrop != nil {
			m.runeSpecs[q.QuestID] = buildRuneDropSpec(q, cards)
		}
	}
	// 排序后再重建指针索引：先建索引再排序会让旧索引指向被交换后的槽位。
	sort.Slice(m.Areas, func(i, j int) bool { return m.Areas[i].ID < m.Areas[j].ID })
	m.areaMap = map[int]*catalogArea{}
	for i := range m.Areas {
		m.areaMap[m.Areas[i].ID] = &m.Areas[i]
	}
	sort.Slice(m.Quests, func(i, j int) bool { return m.Quests[i].ID < m.Quests[j].ID })
	m.questMap = map[int]*catalogQuest{}
	for i := range m.Quests {
		m.questMap[m.Quests[i].ID] = &m.Quests[i]
	}
	if len(skipped) > 0 && len(m.drops) == 0 {
		return fmt.Errorf("资源副本全部剔除: %v", skipped)
	}
	return nil
}

// resourceWaveCount 按不同 wave_no 计数；不能把“怪位行数”当波次数。
func resourceWaveCount(waves []ResourceWave) int {
	set := map[int]bool{}
	for _, w := range waves {
		set[w.WaveNo] = true
	}
	return len(set)
}

// resourceWeaponRateHash 武器优势倍率表（斩/铳可经草稿上调，其余 1.0）。
// 副本不区分难度：客户端按 difficulty_id 取二级键，normal 一份即可。
func resourceWeaponRateHash(q ResourceQuestDraft) map[string]map[string]float64 {
	slash, gun := 1.0, 1.0
	if q.SlashRateBP > 0 {
		slash = float64(q.SlashRateBP) / 1000.0
	}
	if q.GunRateBP > 0 {
		gun = float64(q.GunRateBP) / 1000.0
	}
	return map[string]map[string]float64{
		"normal": {
			"slash_rate": slash, "rush_rate": 1.0, "hit_rate": 1.0,
			"arrow_rate": 1.0, "magic_rate": 1.0, "gun_rate": gun,
			"heal_rate": 1.0,
		},
	}
}

// resourceWaveConf 由波次表生成 wave_conf（与剧情关形状一致 {wave:{length}}；
// 符文本含 time_limit 秒，超时判负）。
func resourceWaveConf(q ResourceQuestDraft) map[string]any {
	set := map[int]bool{}
	for _, w := range q.Waves {
		set[w.WaveNo] = true
	}
	out := map[string]any{}
	for w := range set {
		conf := map[string]any{"length": 640}
		if q.TimeLimitSec > 0 {
			conf["time_limit"] = q.TimeLimitSec
		}
		out[strconv.Itoa(w)] = conf
	}
	return out
}

// buildResourceAppearTable 波次表 → appear_table。params 以魔宠模板 +
// memorial 行为层合成（BattleMonsterDoc 同源键），hp/at/seed_at 按千分比
// 缩放（int 取整）。怪物模板缺失 → 该关剔除（调用方跳过）。
func buildResourceAppearTable(q ResourceQuestDraft, cards UnitCatalog) (map[string]any, error) {
	if len(q.Waves) == 0 {
		return nil, fmt.Errorf("无波次")
	}
	table := map[string]any{}
	for _, w := range q.Waves {
		mon, err := cards.Monster(w.MonsterID)
		if err != nil {
			return nil, fmt.Errorf("wave%d 怪物 %d 无模板", w.WaveNo, w.MonsterID)
		}
		wkey := strconv.Itoa(w.WaveNo)
		wm, _ := table[wkey].(map[string]any)
		if wm == nil {
			wm = map[string]any{}
			table[wkey] = wm
		}
		powerupRate := q.PowerupRateBP
		if powerupRate <= 0 {
			powerupRate = 2500
		}
		wm[strconv.Itoa(w.Slot)] = map[string]any{
			"id":           w.MonsterID,
			"scale":        float64(w.ScaleBP) / 1000.0,
			"powerup_rate": float64(powerupRate) / 1000.0,
			"gauge":        w.IsBoss,
			"params":       resourceMonsterParams(mon, w),
			"rewards":      []any{},
		}
	}
	return table, nil
}

// resourceMonsterParams 以 BattleMonsterDoc 输出为底合成出场 params：
// 行为字段（act_type_id/hit_frame/run 帧/hardness_id/cutin 等）已由
// memorial 层覆盖；hp/at/seed_at 按波次倍率缩放（基准=模板/纪念版基值）。
// rates 拍平成 fire_rate 等键（官方 execute params 形状）。
func resourceMonsterParams(m *cardcatalog.Monster, w ResourceWave) map[string]any {
	p := BattleMonsterDoc(m)
	if rates, ok := p["rates"].(map[string]any); ok {
		p["fire_rate"] = rates["fire"]
		p["water_rate"] = rates["aqua"]
		p["wind_rate"] = rates["wind"]
		p["light_rate"] = rates["light"]
		p["dark_rate"] = rates["dark"]
		delete(p, "rates")
	}
	if w.Hp > 0 {
		p["hp"], p["life"] = w.Hp, w.Hp
	} else {
		scale := func(key string, rateBP int) {
			if v, ok := p[key]; ok {
				p[key] = int(math.Round(numOf(v) * float64(rateBP) / 1000.0))
			}
		}
		scale("hp", w.HpRateBP)
		scale("life", w.HpRateBP)
	}
	if w.At > 0 {
		p["at"], p["atk"], p["seed_at"] = w.At, w.At, w.At
	} else {
		scale := func(key string, rateBP int) {
			if v, ok := p[key]; ok {
				p[key] = int(math.Round(numOf(v) * float64(rateBP) / 1000.0))
			}
		}
		scale("at", w.AtRateBP)
		scale("atk", w.AtRateBP)
		scale("seed_at", w.AtRateBP)
	}
	if w.KillSp > 0 {
		// 击杀 SP 走 cost 字段下发（战斗侧消费口径待实机核）。
		p["cost"] = w.KillSp
	}
	return p
}

// buildRuneDropSpec 符文本关 → 逐怪掉落规格（spawn 序=波次排序后序）。
// Seed 由 execute 在开战时注入。
func buildRuneDropSpec(q ResourceQuestDraft, cards UnitCatalog) *RuneDropSpec {
	spec := &RuneDropSpec{
		MobChanceBP:  q.RuneDrop.MobChanceBP,
		BossChanceBP: q.RuneDrop.BossChanceBP,
		MobQ:         q.RuneDrop.MobQBP,
		BossQ:        q.RuneDrop.BossQBP,
	}
	for _, w := range q.Waves {
		// appear_table 已按全量模板校验，这里取不到元素时按 0 段外处理。
		el := 0
		if mon, err := cards.Monster(w.MonsterID); err == nil {
			el = mon.Element
		}
		spec.Spawns = append(spec.Spawns, RuneSpawn{
			Wave: w.WaveNo, Ms: w.Slot, Element: el, Boss: w.IsBoss,
		})
	}
	sort.Slice(spec.Spawns, func(i, j int) bool {
		if spec.Spawns[i].Wave != spec.Spawns[j].Wave {
			return spec.Spawns[i].Wave < spec.Spawns[j].Wave
		}
		return spec.Spawns[i].Ms < spec.Spawns[j].Ms
	})
	return spec
}

// rollResourceRewards 按掉落规则独立判定生成会话奖励快照。
// item 掉落在白名单内（publish 校验：素材关只掉素材、不掉书/徽章）；
// 数量在 [Min,Max] 闭区间等概率。ChanceBP=0 的规则直接跳过。
func rollResourceRewards(drops []ResourceDrop, itemContent map[string]int,
	rnd *mathrand.Rand) []BattleReward {
	out := []BattleReward{}
	for _, d := range drops {
		if d.ChanceBP <= 0 {
			continue
		}
		if d.ChanceBP < 10000 && rnd.Intn(10000) >= d.ChanceBP {
			continue
		}
		count := d.MinCount
		if d.MaxCount > d.MinCount {
			count += rnd.Intn(d.MaxCount - d.MinCount + 1)
		}
		if count <= 0 {
			continue
		}
		switch d.RewardType {
		case "gacha_point":
			out = append(out, BattleReward{Key: "gacha_point", Count: count})
		case "item":
			cid, ok := itemContent[d.ItemKey]
			if !ok {
				continue // 停用/未登记物品不掉（发布校验已挡，双保险）
			}
			out = append(out, BattleReward{Key: "item", ID: cid, ItemKey: d.ItemKey, Count: count})
		}
	}
	return out
}

// newDropRand 加密种子初始化 math/rand（掉落判定非预测要求低，
// 但要避免跨请求共享全局锁）。
func newDropRand() *mathrand.Rand {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
	}
	return mathrand.New(mathrand.NewSource(int64(binary.LittleEndian.Uint64(b[:]))))
}

func boolPtr(v bool) *bool { return &v }
