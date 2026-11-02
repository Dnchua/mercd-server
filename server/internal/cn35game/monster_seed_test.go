package cn35game

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
)

func TestRollMonsterDropScaleBounds(t *testing.T) {
	for i := 0; i < 2000; i++ {
		s := RollMonsterDropScale()
		parts := strings.Split(s, ".")
		if len(parts) != 2 || len(parts[1]) != 3 {
			t.Fatalf("掉落体型形态非法: %q", s)
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("掉落体型非十进制: %q", s)
		}
		if f < float64(MonsterDropScaleM.Min)/1000 || f > float64(MonsterDropScaleM.Max)/1000 {
			t.Fatalf("掉落体型越界: %q", s)
		}
	}
}

func TestMonsterSeedSkillParse(t *testing.T) {
	cases := []struct {
		skill   string
		elem    int
		wantID  int
		wantVal float64 // 0 表示不校验具体值
	}{
		{"暂无", 1, 0, 0},
		{"", 1, 0, 0},
		{"属性魂：(ファイアソウル) 出击中的X属性同伴的攻击力提高N%。", 2, 1, 0},
		{"属性破：(ダークブレイク) 出击中的同伴对X属性目标的效果提升N%。", 5, 15, 0},
		{"双色魂（水风）：(ウェイブソウル) 出击中的水属性和风属性同伴的攻击力提高N%。", 2, 28, 0},
		{"双色破（光暗）：(カオスブレイク) 出击同伴对A属性、B属性目标的效果提升N％。", 4, 43, 0},
		{"属性魂 出击中的特定属性同伴的攻击力提高。", 3, 3, 0},
		{"属性破 出击中的同伴对特定属性目标的效果提升。", 2, 12, 0},
		{"武器魂：弓斩魂：出击中的斩击属性和长弓属性同伴的攻击力提高31.1%。", 3, 21, 0.311},
		{"武器魂：打魂：出击中的打击属性和铳枪属性同伴的攻击力提高31.1%。", 3, 23, 0.311},
		{"武器魂 出击中的突击属性和魔法属性同伴的攻击力提高。", 5, 22, 0},
		{"幸运UP：30秒的时间内，SP掉落率增加42.6%", 3, 9, 0.426},
		{"士气UP：装备着的同伴士气提升3级。", 1, 10, 3},
		{"麻痹：使现身的怪物进入麻痹状态，持续5秒，不能行动。", 4, 8, 5},
		{"延长时间：探险的限制时间延长20秒。", 2, 51, 20},
		{"治愈：出击中的所有同伴的体力上限提高17.8%，体力恢复8473。", 5, 6, 8473},
		{"正领域：出现的所有同伴和魔宠的对xxx属性目标的效果降低nnn%。", 1, 16, 0},
		{"反领域：(ウィンドイクリーズ) 出现的所有同伴和魔宠的对风属性目标的效果增加N%。", 3, 46, 0},
		{"ダークソウル：出撃中の闇属性のユニットの攻撃力を上昇させる。", 5, 5, 0},
		{"パライズアサルト：出現中のモンスターを麻痺の状態にし。", 4, 50, 0},
		{"光暗魂：出撃中の光属性・闇属性のユニットの攻撃力を上昇させる。", 4, 33, 0},
		{"ダークウォール：出撃中の闇属性ユニットへのダメージを減少させる。", 5, 0, 0},
		{"双色魂（未知）：", 1, 0, 0},
		// 冒号后裸日文名（目录 1074/1075/1082 实形）。
		{"2属性ソウル：アシッドソウル（水闇）", 2, 30, 0},
		{"2属性ブレイク：ゲイルブレイク（風光）", 4, 41, 0},
		{"2属性ソウル：スチームソウル（炎水）", 1, 24, 0},
		// 全角百分号（目录 328/329/549/100022 实形）。
		{"双色破（火风）：爆炸破：出击同伴对火属性、风属性目标的效果提升18.5％。", 1, 35, 0.185},
		{"属性破：出撃中のユニットの対風属性補正を30.6％上昇させる。", 3, 13, 0.306},
	}
	for _, c := range cases {
		m := &cardcatalog.Monster{Skill: c.skill, Element: c.elem}
		id, v1, _ := MonsterSeedSkill(m)
		if id != c.wantID {
			t.Errorf("skill=%q → id=%d want %d", c.skill, id, c.wantID)
		}
		if c.wantVal != 0 && v1 != c.wantVal {
			t.Errorf("skill=%q → value=%v want %v", c.skill, v1, c.wantVal)
		}
	}
}

func TestMonsterSeedSkillSeedFieldPriority(t *testing.T) {
	// 目录结构化 seed 字段优先：485/710/848 同名 パライズアサルト
	// 但目录 seed=8（单次麻痹），日文名映射的 50（双次）不得胜出。
	eight := 8
	m := &cardcatalog.Monster{
		Skill:   "麻痹：(パライズアサルト) 使现身的怪物进入麻痹状态，持续N秒，不能行动。技能效果1Wave只能发动1次。",
		Element: 2,
		Seed:    &eight,
	}
	if id, _, _ := MonsterSeedSkill(m); id != 8 {
		t.Fatalf("seed=8 且文本麻痹 → id=%d want 8", id)
	}
}

func TestMonsterSeedSkillCatalogConsistency(t *testing.T) {
	cat, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Skipf("catalog 不可用: %v", err)
	}
	var wrong []int
	for _, mid := range cat.MonsterIDs() {
		m, err := cat.Monster(mid)
		if err != nil || m.Seed == nil || *m.Seed == 0 {
			continue
		}
		if id, _, _ := MonsterSeedSkill(m); id != *m.Seed {
			wrong = append(wrong, m.ID)
		}
	}
	if len(wrong) > 0 {
		t.Fatalf("seed 字段被覆盖错位的魔宠: %v", wrong)
	}
}

func TestMonsterSeedSkillDefaultValue(t *testing.T) {
	// 占位文本（nnn/N%）→ 默认表兜底而非 0 值。
	m := &cardcatalog.Monster{Skill: "属性魂 出击中的特定属性同伴的攻击力提高nnn%。", Element: 1}
	id, v1, _ := MonsterSeedSkill(m)
	if id != 1 || v1 != SeedSkillValueDefault[1][0] {
		t.Fatalf("id=%d v=%v want 1/%v", id, v1, SeedSkillValueDefault[1][0])
	}
}
