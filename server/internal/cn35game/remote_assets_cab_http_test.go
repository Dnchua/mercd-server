// remote_assets_cab_http_test.go CAB 名冲突规范化的下发面测试：
// 两个共用内部归档名的远端 bundle → 一个被改名、MD5 变化、
// HTTP 返回改名后字节；以及 MERCD_REAL_ASSETS_DIR 门控的真实目录扫描。
package cn35game_test

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/unitybundle"
)

// 两个远端 bundle 共用 CAB-shared.Android：排序靠前者改名为
// CAB-<basename>，HTTP 下发的是规范化后的字节。
func TestRemoteBundleCABRenameHTTP(t *testing.T) {
	remote := t.TempDir()
	bundles := t.TempDir()
	payloadA := bytes.Repeat([]byte{0xAA}, 4096)
	payloadB := bytes.Repeat([]byte{0xBB}, 4096)
	a, err := unitybundle.Pack("CAB-shared.Android", payloadA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := unitybundle.Pack("CAB-shared.Android", payloadB)
	if err != nil {
		t.Fatal(err)
	}
	writeRemoteHTTP(t, remote, "unit/unit_list_8001.Android.unity3d", a)
	writeRemoteHTTP(t, remote, "unit/unit_list_8002.Android.unity3d", b)
	ts, _, m := testServerRemote(t, remote, bundles)

	e1, ok := m.Entry("unit/unit_list_8001")
	if !ok || e1.CAB != "CAB-unit_list_8001.Android" ||
		e1.OrigCAB != "CAB-shared.Android" {
		t.Fatalf("8001 entry: %+v ok=%v", e1, ok)
	}
	e2, ok := m.Entry("unit/unit_list_8002")
	if !ok || e2.CAB != "" {
		t.Fatalf("8002 应保留原名下发: %+v", e2)
	}
	if e1.MD5 == e2.MD5 || e1.MD5 == "" {
		t.Fatalf("改名后 md5 应变化且不同: %q vs %q", e1.MD5, e2.MD5)
	}

	get := func(path string) []byte {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s -> %d", path, resp.StatusCode)
		}
		return readAll(t, resp)
	}
	gotA := get("/toto_image_4/unity/unit/unit_list_8001.Android.1.unity3d")
	names, err := unitybundle.CABNames(bytes.NewReader(gotA))
	if err != nil || len(names) != 1 || names[0] != "CAB-unit_list_8001.Android" {
		t.Fatalf("下发包 CAB 名: %v err=%v", names, err)
	}
	if bytes.Equal(gotA, a) {
		t.Fatal("下发字节应与原文件不同（CAB 已改名）")
	}
	gotB := get("/toto_image_4/unity/unit/unit_list_8002.Android.1.unity3d")
	if !bytes.Equal(gotB, b) {
		t.Fatal("保留者下发字节应与原文件一致")
	}
}

// 真实远端目录扫描：MERCD_REAL_ASSETS_DIR 门控；断言交付后不同逻辑名
// 之间（含与 APK 内置表之间）CAB 零重复。
func TestRemoteAssetsRealDirNoCABConflict(t *testing.T) {
	dir := os.Getenv("MERCD_REAL_ASSETS_DIR")
	if dir == "" {
		t.Skip("MERCD_REAL_ASSETS_DIR 未设置")
	}
	assets := filepath.Join("..", "..", "assets", "cn350")
	state := os.Getenv("MERCD_STATE_DIR")
	if state == "" {
		state = t.TempDir()
	} else if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	m := cn35game.NewRemoteAssets(dir, assets, state, nil)
	t0 := time.Now()
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	first := time.Since(t0)
	t0 = time.Now()
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	t.Logf("首次扫描 %s，二次扫描 %s", first, time.Since(t0))
	rep := m.Report()
	seen := make(map[string]string) // cab → 逻辑名
	dups := 0
	renamed := 0
	for _, e := range rep.Entries {
		if e.CAB != "" {
			renamed++
		}
		f, err := os.Open(e.File)
		if err != nil {
			t.Fatal(err)
		}
		cabs, err := unitybundle.CABNames(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s CAB 解析: %v", e.Name, err)
		}
		for _, c := range cabs {
			if prev, ok := seen[c]; ok && prev != e.Name {
				dups++
				t.Logf("冲突残留: %s 被 %s 与 %s 共用", c, prev, e.Name)
			}
			seen[c] = e.Name
		}
	}
	if dups != 0 {
		t.Fatalf("仍存在 %d 处跨逻辑名 CAB 冲突", dups)
	}
	t.Logf("entries=%d renamed=%d rejected=%d", len(rep.Entries), renamed, len(rep.Rejected))
}
