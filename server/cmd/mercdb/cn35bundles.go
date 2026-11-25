// cn35bundles.go Unity bundle CAB 名治理子命令：
//
//	cn35-embedded-cabs <unity目录> [-out PATH]
//	    扫描 APK 内置 assets/unity，生成 {逻辑名: [cab...]} 表
//	    （默认 server/assets/cn350/embedded_bundle_cabs.json）。
//	cn35-normalize-bundles <unity目录> [-cabs embedded_bundle_cabs.json]
//	    模式 B 单 APK 合并目录原地规范化：文件 CAB 与内置表一致视为
//	    原版不改；其余按 PlanCABRenames 判定冲突并原地改名。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Dnchua/mercd-server/server/internal/unitybundle"
)

// cn35EmbeddedCabs 生成 APK 内置 bundle CAB 表（稳定排序输出）。
func cn35EmbeddedCabs(args []string) error {
	fs := flag.NewFlagSet("cn35-embedded-cabs", flag.ContinueOnError)
	out := fs.String("out", "assets/cn350/embedded_bundle_cabs.json", "输出路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: mercdb cn35-embedded-cabs [-out PATH] <unity目录>")
	}
	m, err := unitybundle.ScanDirCABs(fs.Arg(0))
	if err != nil {
		return err
	}
	for _, cabs := range m {
		sort.Strings(cabs)
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	tmp := *out + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, *out); err != nil {
		return err
	}
	fmt.Printf("%s: %d bundles\n", *out, len(m))
	return nil
}

// cn35NormalizeBundles 模式 B：合并目录（APK+远端）原地 CAB 规范化。
func cn35NormalizeBundles(args []string) error {
	fs := flag.NewFlagSet("cn35-normalize-bundles", flag.ContinueOnError)
	cabsFile := fs.String("cabs", "assets/cn350/embedded_bundle_cabs.json", "APK 内置 CAB 表")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: mercdb cn35-normalize-bundles [-cabs JSON] <unity目录>")
	}
	root := fs.Arg(0)
	found, err := unitybundle.ScanDirCABs(root)
	if err != nil {
		return err
	}
	embedded := make(map[string][]string)
	if raw, rerr := os.ReadFile(*cabsFile); rerr == nil {
		if jerr := json.Unmarshal(raw, &embedded); jerr != nil {
			return fmt.Errorf("内置 CAB 表解析失败 %s: %w", *cabsFile, jerr)
		}
	} else {
		fmt.Fprintf(os.Stderr, "mercdb: 内置 CAB 表缺失 %s，仅做目录内部判定\n", *cabsFile)
	}

	// 文件 CAB 与内置表一致 → 视为原版（受保护）；其余为待判定项。
	protected := make(map[string][]string)
	remote := make(map[string][]string)
	for logical, cabs := range found {
		if sameStringSet(cabs, embedded[logical]) {
			protected[logical] = cabs
		} else {
			remote[logical] = cabs
		}
	}
	plan := unitybundle.PlanCABRenames(protected, remote)
	renamed := 0
	for logical, renames := range plan {
		p := filepath.Join(root, filepath.FromSlash(logical+".Android.unity3d"))
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("%s: %w", logical, err)
		}
		out, err := unitybundle.ApplyCABPlan(data, renames)
		if err != nil {
			return fmt.Errorf("%s: %w", logical, err)
		}
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, out, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, p); err != nil {
			return err
		}
		renamed++
		for o, n := range renames {
			fmt.Printf("%s: %s -> %s\n", logical, o, n)
		}
	}
	fmt.Printf("scanned=%d renamed=%d\n", len(found), renamed)
	return nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa, bb := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}
