// gacha_migrate_0055.go 存量库 M7 卡池数据升级（migrate-0055）的纯函数部分：
// 只改激活版本快照里需要升级的字段，不读草稿面，避免把管理员未发布的
// 草稿编辑连带上线。存储层负责事务、版本行、指针与标记。
package cn35game

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// 旧种子池 "2"（0013～M6 口径）：钻石十连 10 抽 0 赠。仅当名称/描述仍是
// 旧种子原文时才替换为新文案；管理员改过的文案保留（只做换行转义归一）。
const (
	legacyPool2Name        = "钻石招募"
	legacyPool2Description = `使用钻石招募同伴。\n每连续 10 次至少获得 1 名 SR 以上。`

	// ScoutDiscountPool2Name / ScoutDiscountPool2Description 是 M7 池 "2" 新文案
	// （与 GachaSeedPools 同源；描述内换行为客户端可命中的 `\\n` 三字符序列）。
	ScoutDiscountPool2Name        = "骑士契约书（常驻招募）"
	ScoutDiscountPool2Description = `使用骑士契约书招募同伴（5钻兑换1本，十连享9折仅需9本）。\\n每连续 10 次至少获得 1 名 SR 以上。`
)

// NormalizeGachaDescriptionNewlines 把描述里的单反斜杠换行标记 `\n`（两字符）
// 归一为 `\\n`（三字符）；已是 `\\n` 的保持不变。
//
// 依据（CN3.5 客户端，ilspy GachaIndexParts.cs:419 / Document.cs:320-335）：
// JSONObject 保留原始转义，Document.s() 经 DecordReturn 后只把线上 `\\\\n`
// 还原为 `\\n`，GachaIndexParts 再 Replace(`\\n`→换行)。线上若是 `\\n`
// （即值为两字符 `\n`）最终显示为字面 "\n"——实机 M7 已复现。
// 注：mlk-capture-201711（2017 客户端）线上为 `\\n`，与 CN3.5 客户端不同代，
// 不能直接照抄。
func NormalizeGachaDescriptionNewlines(s string) string {
	if !strings.Contains(s, `\n`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == 'n' && (i == 0 || s[i-1] != '\\') {
			b.WriteString(`\\n`)
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// UpgradePool2ScoutDiscountDef 对草稿池定义做同口径升级：仅当池 "2" 仍是
// 旧口径（钻石计价、max_exec=10、max_bonus=0）时改为 9+1；所有池描述做
// 换行归一。返回是否有改动。
func UpgradePool2ScoutDiscountDef(p *GachaPoolDef) bool {
	changed := false
	if d := NormalizeGachaDescriptionNewlines(p.Description); d != p.Description {
		p.Description = d
		changed = true
	}
	if p.PoolID == "2" && p.PriceCoin > 0 && p.MaxExec == 10 && p.MaxBonus == 0 {
		p.MaxExec, p.MaxBonus = 9, 1
		if p.Name == legacyPool2Name {
			p.Name = ScoutDiscountPool2Name
		}
		if p.Description == NormalizeGachaDescriptionNewlines(legacyPool2Description) {
			p.Description = ScoutDiscountPool2Description
		}
		changed = true
	}
	return changed
}

// PatchGachaSnapshotScoutDiscount 在已发布快照上应用 migrate-0055 升级：
//   - 池 "2" 仅在仍为旧口径 10/0（钻石计价）时改为 9+1，管理员已自定义
//     的 max_exec/max_bonus 一律不动；
//   - 全部池描述做换行转义归一（修复存量库金币池等字面 "\n"）。
//
// 成员、权重、保底、价格保持原样。changed=false 时 raw/digest 为空。
// 产出快照经 ParseActiveGachaConfig 自洽回读，杜绝写入加载即失败的版本。
func PatchGachaSnapshotScoutDiscount(snapshot string) (raw []byte, digest string, changed bool, err error) {
	var snap gachaSnapshot
	if err := json.Unmarshal([]byte(snapshot), &snap); err != nil {
		return nil, "", false, fmt.Errorf("卡池快照解析失败: %w", err)
	}
	if snap.Schema != gachaSnapshotSchema {
		return nil, "", false, fmt.Errorf("卡池快照 schema %q 不支持", snap.Schema)
	}
	for i := range snap.Pools {
		sp := &snap.Pools[i]
		def := GachaPoolDef{
			PoolID: sp.PoolID, Name: sp.Name, Description: sp.Description,
			PriceCoin: sp.PriceCoin, MaxExec: sp.MaxExec, MaxBonus: sp.MaxBonus,
		}
		if UpgradePool2ScoutDiscountDef(&def) {
			sp.Name, sp.Description = def.Name, def.Description
			sp.MaxExec, sp.MaxBonus = def.MaxExec, def.MaxBonus
			changed = true
		}
	}
	if !changed {
		return nil, "", false, nil
	}
	raw, err = json.Marshal(snap)
	if err != nil {
		return nil, "", false, fmt.Errorf("卡池快照序列化失败: %w", err)
	}
	if _, perr := ParseActiveGachaConfig(0, string(raw)); perr != nil {
		return nil, "", false, fmt.Errorf("升级后快照自洽回读失败: %w", perr)
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), true, nil
}
