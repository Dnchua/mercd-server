package cn35game_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35store"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

type countryTemplates map[int]*cn35game.UnitTemplate

func (m countryTemplates) UnitTemplate(id int) (*cn35game.UnitTemplate, bool, error) {
	t, ok := m[id]
	return t, ok, nil
}

func TestRosterCountryWire(t *testing.T) {
	base, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	templates := countryTemplates{}
	for _, id := range []int{152, 348} {
		u, err := base.Unit(id)
		if err != nil {
			t.Fatal(err)
		}
		country := "王国"
		if id == 152 {
			country = "沙漠之国"
		}
		templates[id] = &cn35game.UnitTemplate{UnitID: id, Name: u.Name, Rare: u.Rare, Element: u.Element, Weapon: u.Weapon, Type: u.Type, Life: u.Life, Atk: u.Atk, AArea: u.AArea, Country: country}
	}
	st, err := cn35store.Open(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reg := httpapi.NewRegistry()
	cn35game.Register(reg, cn35game.Deps{Accounts: st, Sessions: st, Inventory: st, Catalog: cn35game.NewRosterCatalog(base, templates), ServerHost: "127.0.0.1:18091", AssetsDir: filepath.Join("..", "..", "assets", "cn350")})
	srv, err := httpapi.NewServer(httpapi.Options{}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	uid, sk, s0, s1 := establishSession(t, ts.URL, "country-roster-test")
	insertUnit(t, st, uid, 152, 1)
	insertUnit(t, st, uid, 348, 1)
	for _, spec := range []struct{ path, key string }{{"/user_units/", "user_units"}, {"/albums/", "albums"}} {
		data := gameData(t, ts.URL, spec.path, uid, sk, s0, s1, "")
		for id, want := range map[int]float64{152: 10, 348: 1} {
			row := rowByID(t, data[spec.key].([]any), float64(id))
			if row["country_id"] != want {
				t.Errorf("%s unit %d: country=%v want %v", spec.path, id, row["country_id"], want)
			}
		}
	}
}

func TestRosterCountryUsesClientFilterIDs(t *testing.T) {
	base, err := cardcatalog.Load(filepath.Join("..", "..", "assets", "shared", "cards"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "assets", "cn350", "serve_app_conf_android.json"))
	if err != nil {
		t.Fatal(err)
	}
	var conf map[string]any
	if err := json.Unmarshal(raw, &conf); err != nil {
		t.Fatal(err)
	}
	page := conf["pages"].(map[string]any)["UserUnits"].(map[string]any)
	parts := page["parts"].(map[string]any)["ListParts"].(map[string]any)
	components := parts["components"].(map[string]any)["filter_and_sort"].(map[string]any)
	foundCountryFilter := false
	for _, item := range components["options"].([]any) {
		filter := item.(map[string]any)
		if filter["title"] != "出身国" {
			continue
		}
		foundCountryFilter = true
		for _, option := range filter["SelectModal"].(map[string]any)["options"].([]any) {
			v := option.(map[string]any)
			if v["key"] != "country_id" || v["title"] == "其他" {
				continue
			}
			id, err := strconv.Atoi(v["value"].(string))
			if err != nil {
				t.Fatal(err)
			}
			name := v["title"].(string)
			if got := base.CountryID(name); got != id {
				t.Errorf("%s: got %d want %d", name, got, id)
			}
		}
	}
	if !foundCountryFilter {
		t.Fatal("缺少出身国筛选配置")
	}
	cat := cn35game.NewRosterCatalog(base, countryTemplates{
		152: {UnitID: 152, Country: "沙漠之国"},
		348: {UnitID: 348, Country: "王国"},
		349: {UnitID: 349, Country: "目录外的未知国家"},
	})
	for id, want := range map[int]int{152: 10, 348: 1, 349: 0} {
		u, err := cat.Unit(id)
		if err != nil || u.CountryID != want {
			t.Errorf("unit %d: country id=%v want %d err=%v", id, u.CountryID, want, err)
		}
	}
	// 没有模板覆盖时，静态目录仍保持相同国家编号。
	u, err := cn35game.NewRosterCatalog(base, countryTemplates{}).Unit(152)
	if err != nil || u.CountryID != 10 {
		t.Errorf("fallback unit 152: %+v err=%v", u, err)
	}
}
