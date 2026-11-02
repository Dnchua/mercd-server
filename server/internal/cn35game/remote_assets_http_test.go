// remote_assets_http_test.go 远端叠加的 HTTP 面测试：
// /toto_image_4/unity/ 反查下发、/cn35/assets/remote 诊断、
// version_info 与清单首行版本一致（走真实加密通道）。
package cn35game_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// testServerRemote 起带远端叠加管理器的测试服。remoteDir 由调用方备好。
func testServerRemote(t *testing.T, remoteDir, bundlesDir string) (*httptest.Server, *cn35wire.MemoryStore, *cn35game.RemoteAssets) {
	t.Helper()
	assets := filepath.Join("..", "..", "assets", "cn350")
	if _, err := os.Stat(filepath.Join(assets, "serve_app_conf_android.json")); err != nil {
		t.Skipf("cn350 fixture 目录不可用: %v", err)
	}
	m := cn35game.NewRemoteAssets(remoteDir, assets, t.TempDir(), nil)
	if err := m.Scan(); err != nil {
		t.Fatalf("远端扫描: %v", err)
	}
	sessions := cn35wire.NewMemoryStore()
	reg := httpapi.NewRegistry()
	n := cn35game.Register(reg, cn35game.Deps{
		Accounts:     cn35game.NewMemoryAccounts(),
		Sessions:     sessions,
		ServerHost:   "127.0.0.1:18091",
		AssetsDir:    assets,
		BundlesDir:   bundlesDir,
		RemoteAssets: m,
	})
	if n == 0 {
		t.Fatal("未注册任何路由")
	}
	srv, err := httpapi.NewServer(httpapi.Options{Addr: "127.0.0.1:0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, sessions, m
}

func writeRemoteHTTP(t *testing.T, remoteDir, rel string, content []byte) {
	t.Helper()
	full := filepath.Join(remoteDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(full, old, old); err != nil {
		t.Fatal(err)
	}
}

func fakeUnity(payload string) []byte {
	return append([]byte("UnityWeb\x00"), payload...)
}

// 7) bundle 反查：unit_v2→unit、unit_list 原路径、monster_v2→monster、
// effect_ss_v2→effect_ss；未命中走 BundlesDir；.. 拒绝。
func TestRemoteBundleOverlayHTTP(t *testing.T) {
	remote := t.TempDir()
	bundles := t.TempDir()
	f414 := fakeUnity("unit414-bytes")
	writeRemoteHTTP(t, remote, "unit/unit_414.Android.unity3d", f414)
	listBytes := fakeUnity("list414")
	writeRemoteHTTP(t, remote, "unit/unit_list_414.Android.unity3d", listBytes)
	monBytes := fakeUnity("mon39")
	writeRemoteHTTP(t, remote, "monster/monster_39.Android.unity3d", monBytes)
	effBytes := fakeUnity("eff999")
	writeRemoteHTTP(t, remote, "effect_ss/effect_999.Android.unity3d", effBytes)
	// BundlesDir 兜底文件（不在远端目录）。
	fallback := fakeUnity("fallback")
	if err := os.MkdirAll(filepath.Join(bundles, "sound"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundles, "sound", "B-99.Android.9.unity3d"), fallback, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundles, "common"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundles, "common", "bg-house_2.Android.unity3d"), fallback, 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _, _ := testServerRemote(t, remote, bundles)

	get := func(path string) (int, []byte) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, readAll(t, resp)
	}
	cases := []struct {
		path string
		want []byte
	}{
		{"/toto_image_4/unity/unit_v2/unit_414.Android.1.unity3d?nocache=1&rand=x", f414},
		{"/toto_image_4/unity/unit/unit_list_414.Android.1.unity3d", listBytes},
		{"/toto_image_4/unity/monster_v2/monster_39.Android.1.unity3d", monBytes},
		{"/toto_image_4/unity/effect_ss_v2/effect_999.Android.1.unity3d", effBytes},
		{"/toto_image_4/unity/sound/B-99.Android.9.unity3d", fallback},        // BundlesDir 精确文件名兜底
		{"/toto_image_4/unity/common/bg-house_2.Android.1.unity3d", fallback}, // BundlesDir 逻辑名无版本号兜底
	}
	for _, c := range cases {
		code, body := get(c.path)
		if code != http.StatusOK {
			t.Fatalf("%s → %d (%s)", c.path, code, body[:min(len(body), 120)])
		}
		if string(body) != string(c.want) {
			t.Fatalf("%s 字节不等 (%d vs %d)", c.path, len(body), len(c.want))
		}
	}
	// 未命中且 BundlesDir 无此文件 → 非 200。
	if code, _ := get("/toto_image_4/unity/unit_v2/unit_999.Android.1.unity3d"); code == http.StatusOK {
		t.Fatal("未命中不应 200")
	}
	// .. 拒绝。
	if code, _ := get("/toto_image_4/unity/../unit/unit_414.Android.1.unity3d"); code == http.StatusOK {
		t.Fatal(".. 不应 200")
	}
}

// 8) version_info 的 asset_info_version == asset_info body 首行版本
// （远端非空时 = 基线+1）。走真实 verify/game 加密通道。
func TestRemoteVersionInfoMatchesManifest(t *testing.T) {
	remote := t.TempDir()
	writeRemoteHTTP(t, remote, "unit/unit_414.Android.unity3d", fakeUnity("x"))
	ts, _, m := testServerRemote(t, remote, "")

	// asset_info body 首行版本。
	resp, err := http.Get(ts.URL + "/asset_info_android4_v2_short.json.encode")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	resp.Body.Close()
	first, _, _ := strings.Cut(string(body), "@")
	if !strings.HasPrefix(first, "version,") {
		t.Fatalf("清单首行: %q", first)
	}
	bodyVer, err := strconv.ParseInt(strings.TrimPrefix(first, "version,"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if bodyVer != m.Version() {
		t.Fatalf("body 版本 %d != 管理器 %d", bodyVer, m.Version())
	}
	if !strings.Contains(string(body), "@414,23,1,0,1,") {
		t.Fatal("清单缺 414 md5 行")
	}

	// 加密通道播种 → version_info。
	doc := verifyCall(t, ts.URL, `{"method":"heQuickLogin","randomStr":"rs-r","deviceId":"dev-r"}`)
	r := results0(t, doc)
	heID, token := r["heAccountId"].(string), r["token"].(string)
	s1 := "S1saltS1saltS1saltS1saltS1salt12"
	doc, seeded := verifyCallSeeded(t, ts.URL,
		`{"method":"chooseServer","heAccountId":"`+heID+`","token":"`+token+
			`","deviceId":"dev-r","deviceName":"meizu","deviceBootTime":"1","serverId":"1"}`, s1)
	r = results0(t, doc)
	uid, sk := r["userId"].(string), r["sk"].(string)
	plain := gameCall(t, ts.URL, "/users/version_info", uid, sk, seeded, s1, "v=1316", "_method=GET")
	var vi map[string]any
	if err := json.Unmarshal(plain, &vi); err != nil {
		t.Fatal(err)
	}
	if int64(vi["asset_info_version"].(float64)) != bodyVer {
		t.Fatalf("version_info %v != 清单首行 %d", vi, bodyVer)
	}
}

// 诊断路由：明文 JSON 暴露版本/条目/pending/rejected。
func TestRemoteDiagRoute(t *testing.T) {
	remote := t.TempDir()
	writeRemoteHTTP(t, remote, "unit/unit_414.Android.unity3d", fakeUnity("x"))
	writeRemoteHTTP(t, remote, "bad/none.Android.unity3d", []byte("BADMAGIC!!"))
	ts, _, m := testServerRemote(t, remote, "")

	resp, err := http.Get(ts.URL + "/cn35/assets/remote")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("诊断路由 %d", resp.StatusCode)
	}
	var rep struct {
		AssetInfoVersion int64  `json:"asset_info_version"`
		BaseVersion      int64  `json:"base_version"`
		RemoteDir        string `json:"remote_dir"`
		Entries          []struct {
			Name string `json:"name"`
			MD5  string `json:"md5"`
		} `json:"entries"`
		Rejected []struct {
			File   string `json:"file"`
			Reason string `json:"reason"`
		} `json:"rejected"`
	}
	if err := json.Unmarshal(readAll(t, resp), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.AssetInfoVersion != m.Version() || rep.BaseVersion <= 0 || rep.RemoteDir != remote {
		t.Fatalf("诊断: %+v (mgr ver %d)", rep, m.Version())
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Name != "unit/unit_414" {
		t.Fatalf("entries: %+v", rep.Entries)
	}
	if len(rep.Rejected) != 1 || rep.Rejected[0].File != "bad/none.Android.unity3d" {
		t.Fatalf("rejected: %+v", rep.Rejected)
	}
}
