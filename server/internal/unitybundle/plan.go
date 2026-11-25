// plan.go CAB 名冲突判定（模式 A 远端叠加 / 模式 B 单 APK 合并目录共用）。
//
// Unity 4 不允许同时加载两个内部归档文件名（CAB 名）相同的 bundle；
// 用户自备资源存在大量不同逻辑名共用同一 CAB 名的情况（贴图不同、
// 归档名未改），需在交付前改名。
//
// 规则：
//   - 远端逻辑名覆盖同名 APK 内置条目（远端以 remote 表为准）。
//   - 某远端 bundle 的 CAB c 被另一个不同逻辑名的 bundle 也使用 → 需改名。
//   - 目标名 TargetCABName(logical)；已被其他逻辑名占用则回退
//     "CAB-"+md5hex(logical)。已等于目标名或与其他名字不冲突则不动。
//   - 判定按逻辑名排序依次进行，先定的改名占住新名，保证结果稳定。
package unitybundle

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ScanDirCABs 遍历 unity 资源目录（client/decoded/assets/unity 布局：
// <subdir>/<name>.Android.unity3d）下全部 Android bundle，返回
// {逻辑名: [cab...]}。单个文件解析失败即返回错误。
func ScanDirCABs(root string) (map[string][]string, error) {
	out := make(map[string][]string)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".Android.unity3d") {
			return nil
		}
		f, oerr := os.Open(p)
		if oerr != nil {
			return oerr
		}
		cabs, cerr := CABNames(f)
		f.Close()
		if cerr != nil {
			return fmt.Errorf("%s: %w", rel, cerr)
		}
		out[strings.TrimSuffix(rel, ".Android.unity3d")] = cabs
		return nil
	})
	return out, err
}

// TargetCABName 逻辑名（unit/unit_list_1197）的默认目标 CAB 名
// （CAB-<文件 basename 去 .unity3d>，即 CAB-unit_list_1197.Android）。
func TargetCABName(logical string) string {
	return "CAB-" + path.Base(logical) + ".Android"
}

func fallbackCABName(logical string) string {
	sum := md5.Sum([]byte(logical))
	return "CAB-" + hex.EncodeToString(sum[:])
}

// PlanCABRenames 判定需要改名的远端 bundle。
// embedded = APK 内置 {逻辑名: [cab...]}（可为 nil），remote = 远端
// {逻辑名: [cab...]}（远端同名覆盖 embedded 同名）。
// 返回 {远端逻辑名: {原cab: 新cab}}，只含需要改名的项。
func PlanCABRenames(embedded, remote map[string][]string) map[string]map[string]string {
	// cab → 使用者逻辑名集合（remote 覆盖 embedded 同名后）。
	use := make(map[string]map[string]bool)
	add := func(logical, cab string) {
		if use[cab] == nil {
			use[cab] = map[string]bool{}
		}
		use[cab][logical] = true
	}
	for logical, cabs := range embedded {
		if _, overridden := remote[logical]; overridden {
			continue
		}
		for _, c := range cabs {
			add(logical, c)
		}
	}
	for logical, cabs := range remote {
		for _, c := range cabs {
			add(logical, c)
		}
	}

	names := make([]string, 0, len(remote))
	for n := range remote {
		names = append(names, n)
	}
	sort.Strings(names)

	plan := make(map[string]map[string]string)
	claimed := make(map[string]bool) // 已有逻辑名按"自然目标名"保留的 cab
	for _, logical := range names {
		for _, cab := range remote[logical] {
			conflict := false
			for other := range use[cab] {
				if other != logical {
					conflict = true
					break
				}
			}
			if !conflict {
				continue
			}
			target := TargetCABName(logical)
			if target == cab && !claimed[cab] {
				// CAB 已等于自然目标名：保留——但同名自然目标被
				// 排序更前的逻辑名占用过时（如 unit/ 与 unit_hd/
				// 的 unit_large_1239）须落入下面的改名路径。
				claimed[cab] = true
				continue
			}
			if target == cab {
				target = fallbackCABName(logical)
			} else if users := use[target]; len(users) > 0 && !users[logical] {
				target = fallbackCABName(logical)
			}
			if plan[logical] == nil {
				plan[logical] = map[string]string{}
			}
			plan[logical][cab] = target
			// 改名后占用新名、释放旧名。
			delete(use[cab], logical)
			add(logical, target)
		}
	}
	return plan
}

// ApplyCABPlan 对 data 执行 plan 中的全部改名（逐项 RenameCAB）。
func ApplyCABPlan(data []byte, renames map[string]string) ([]byte, error) {
	// 稳定顺序执行，便于测试与排错。
	old := make([]string, 0, len(renames))
	for o := range renames {
		old = append(old, o)
	}
	sort.Strings(old)
	var err error
	for _, o := range old {
		data, err = RenameCAB(data, o, renames[o])
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}
