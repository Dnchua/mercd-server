package apidoc

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cardcatalog"
	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/Dnchua/mercd-server/server/internal/ops"
)

// BuildRegistryCN35 装配完整依赖模式的 CN3.5 路由登记表（内存桩账号/会话，
// 不打开玩家数据库、不启动监听）。运维端点（admin35.RegisterOps 的
// /mercd/health|routes|unknown-endpoints）与内嵌后台 SPA（webui 的
// /mercd/admin/*）同样入册；mercd35 的治理端点为 admin35
// （/mercd/cn35/admin/*，Bearer 令牌 fail-closed）。
func BuildRegistryCN35() (*httpapi.Registry, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	assets, cards, err := docAssets(cwd)
	if err != nil {
		return nil, err
	}
	cat, err := cardcatalog.Load(cards)
	if err != nil {
		return nil, fmt.Errorf("文档卡牌目录: %w", err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := httpapi.NewRegistry()
	// 读取只读目录完成真实战斗引用门控；不打开玩家数据库。
	cn35game.Register(reg, cn35game.Deps{
		Log:        quiet,
		Accounts:   cn35game.NewMemoryAccounts(),
		Sessions:   cn35wire.NewMemoryStore(),
		Quests:     cn35game.NewMemoryQuests(),
		Battles:    cn35game.NewMemoryBattles(),
		Catalog:    cat,
		ServerHost: "apidoc.local:18091",
		AssetsDir:  assets,
		// RemoteAssets 仅用于路由登记（诊断路由/asset_info 叠加形态）；
		// 不 Scan、不读远端目录。
		RemoteAssets: cn35game.NewRemoteAssets(
			"server-state-cn35/remote_assets", assets, "server-state-cn35", quiet),
		// P0 写面（2026-09-28 批）登记桩：路由按 nil 检查门控，文档
		// 导出只需入册，处理器在导出期不会被调用。
		Decks:          docDecks{},
		Training:       docTraining{},
		Gacha:          docGacha{},
		UnitOps:        docUnitOps{},
		MonsterOps:     docMonsterOps{},
		PresentReceive: docPresentReceive{},
	})
	ops.RegisterOps(reg, ops.OpsDeps{Registry: reg, Started: time.Now(), AssetsDir: assets, StateDir: "server-state-cn35", ServerHost: "apidoc.local:18091", Log: quiet})

	Register(reg, "")
	return reg, nil
}

// errDocStub 登记桩方法返回值；处理器在文档导出期不会被调用。
var errDocStub = errors.New("apidoc: 登记桩不可调用")

type docUnitOps struct{}

func (docUnitOps) SetLeaderUnitID(string, int) error        { return errDocStub }
func (docUnitOps) SetHelperUnitID(string, int) error        { return errDocStub }
func (docUnitOps) SetUnitProtected(string, int, bool) error { return errDocStub }
func (docUnitOps) WaitUnits(string, []int) error            { return errDocStub }
func (docUnitOps) BackUnits(string, []int) error            { return errDocStub }

type docMonsterOps struct{}

func (docMonsterOps) EquipMonster(string, string, int) error           { return errDocStub }
func (docMonsterOps) UnequipMonster(string, string, int) error         { return errDocStub }
func (docMonsterOps) SetMonsterProtected(string, []string, bool) error { return errDocStub }
func (docMonsterOps) MonsterPowerup(string, string, int, int, int) (*cn35game.MonsterPowerupResult, error) {
	return nil, errDocStub
}

type docPresentReceive struct{}

func (docPresentReceive) ReceivePresent(string, string, int64, cn35game.UnitCatalog) (*cn35game.PresentReceiveResult, error) {
	return nil, errDocStub
}

func (docPresentReceive) ReceiveAllPresents(string, bool, int64, cn35game.UnitCatalog) (*cn35game.PresentReceiveResult, error) {
	return nil, errDocStub
}

// BuildRegistryFor 按服务名装配路由表；仅支持 cn35，其余返回 nil。
func BuildRegistryFor(service string) (*httpapi.Registry, error) {
	switch service {
	case "cn35", "":
		return BuildRegistryCN35()
	}
	return nil, fmt.Errorf("未知 service %q（仅支持 cn35）", service)
}

type docDecks struct{}

func (docDecks) Decks(string) ([]cn35game.Deck, error)        { return nil, errDocStub }
func (docDecks) SetDeckUnit(string, string, int, int) error   { return errDocStub }
func (docDecks) ClearDeckUnit(string, string, int, int) error { return errDocStub }

type docTraining struct{}

func (docTraining) TrainUnit(string, int, int, int, int) (int64, error) { return 0, errDocStub }

type docGacha struct{}

func (docGacha) GachaPity(string, string) (cn35game.GachaPity, bool, error) {
	return cn35game.GachaPity{}, false, errDocStub
}
func (docGacha) GachaOperationByKey(string, string) (*cn35game.GachaOperationRecord, error) {
	return nil, errDocStub
}
func (docGacha) GachaOperationByDigest(string, string) (*cn35game.GachaOperationRecord, error) {
	return nil, errDocStub
}
func (docGacha) GachaRecentOperations(string, int) ([]cn35game.GachaOperationRecord, error) {
	return nil, errDocStub
}
func (docGacha) GachaDraws(string, int64) ([]cn35game.GachaDraw, error) { return nil, errDocStub }
func (docGacha) SettleGacha(cn35game.GachaSettlement) (*cn35game.GachaOperationRecord, error) {
	return nil, errDocStub
}

// docAssets 仅定位固定的 server/assets，不扫描源码或协议说明。
// 支持仓库根、server 和包测试目录；缺失时明确失败，不输出残缺文档。
func docAssets(start string) (assets, cards string, err error) {
	root, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	for {
		for _, base := range []string{filepath.Join(root, "assets"), filepath.Join(root, "server", "assets")} {
			assets, cards = filepath.Join(base, "cn350"), filepath.Join(base, "shared", "cards")
			if _, err := os.Stat(filepath.Join(assets, "serve_app_conf_android.json")); err != nil {
				continue
			}
			if _, err := os.Stat(filepath.Join(cards, "SHA256SUMS")); err == nil {
				return assets, cards, nil
			}
		}
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	return "", "", fmt.Errorf("从 %s 未找到完整 CN3.5 静态目录；请在包含 server/assets 的源码仓库执行文档导出", start)
}
