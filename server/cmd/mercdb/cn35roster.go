// cn35roster.go ROSTER350-06 名册/卡池治理开发管理入口。
//
//	mercdb cn35-roster import   [-db PATH] [-assets DIR] [-cards DIR] [-commit]
//	mercdb cn35-roster seed     [-db PATH] [-assets DIR] [-cards DIR]
//	mercdb cn35-roster publish  [-db PATH] [-note TXT]
//	mercdb cn35-roster activate -version N [-db PATH] [-note TXT]
//	mercdb cn35-roster status   [-db PATH]
//	mercdb cn35-roster audit    [-db PATH] [-limit N]
//
// 语义：import 默认预览（整包回滚），-commit 才落库；seed 只在无任何池定义时
// 执行（名册导入 + 双池定义 + 成员启用 + 成员表 + 发布一条龙）；publish/
// activate 等价于管理面同名操作。全部写操作落 cn35_admin_audit。
//
// 模板缓存：mercd35 把 cn35_unit_templates 缓存在进程内（只感知本进程写入）。
// 服务运行中用 import -commit / seed 改了同一库的模板后，须重启 mercd35 生效。
package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35roster"
)

const rosterActor = "mercdb"

func cn35Roster(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mercdb cn35-roster import|seed|publish|activate|status|audit|open-all [-flags]")
	}
	fs := flag.NewFlagSet("cn35-roster", flag.ContinueOnError)
	path := fs.String("db", "server-state-cn35/mercd35.sqlite3", "SQLite file")
	assets := fs.String("assets", "assets/cn350", "CN3.5 fixture 目录（清单在 <assets>/unit_roster_manifest.json）")
	cards := fs.String("cards", "assets/shared/cards", "cardcatalog 目录")
	commit := fs.Bool("commit", false, "import: 真正落库（默认预览回滚）")
	note := fs.String("note", "", "publish/activate 备注")
	version := fs.Int64("version", 0, "activate: 目标版本号")
	limit := fs.Int("limit", 50, "audit/status 输出条数")
	action := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch action {
	case "import", "seed":
		in, _, err := cn35roster.LoadImport(resolveAsset(*assets), resolveAsset(*cards))
		if err != nil {
			return err
		}
		st, err := openStore(*path)
		if err != nil {
			return err
		}
		defer st.Close()

		if action == "seed" {
			// seed 与 mercd35 启动共用同一入口：仅在没有任何池定义时初始化；
			// 已有池定义时整体跳过（包括导入），不落地任何写操作。
			v, seeded, err := cn35game.SeedGachaRoster(st, in, rosterActor)
			if err != nil {
				return err
			}
			if !seeded {
				fmt.Println("已有池定义，seed 跳过（如需重发用 publish）")
				return nil
			}
			fmt.Printf("已发布版本 v%d digest=%s\n", v.Version, v.Digest)
			return nil
		}

		res, err := st.ImportRoster(in, *commit, rosterActor)
		if err != nil {
			return err
		}
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		if !*commit {
			fmt.Println("（预览模式：以上差异未落库；加 -commit 写入）")
		} else if res.TemplateUpserts > 0 {
			fmt.Println("注意：mercd35 把同伴模板缓存在进程内，运行中的服务须重启后才读到本次模板变更")
		}
		return nil

	case "publish":
		st, err := openStore(*path)
		if err != nil {
			return err
		}
		defer st.Close()
		v, err := st.PublishGachaDraft(rosterActor, *note)
		if err != nil {
			return err
		}
		fmt.Printf("published version=%d digest=%s\n", v.Version, v.Digest)
		return nil

	case "activate":
		if *version <= 0 {
			return fmt.Errorf("activate 需要 -version N")
		}
		st, err := openStore(*path)
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.ActivateGachaVersion(*version, rosterActor, *note); err != nil {
			return err
		}
		fmt.Printf("active version=%d\n", *version)
		return nil

	case "status":
		st, err := openStore(*path)
		if err != nil {
			return err
		}
		defer st.Close()
		o, err := st.RosterOverview()
		if err != nil {
			return err
		}
		out, _ := json.MarshalIndent(o, "", "  ")
		fmt.Println(string(out))
		vs, active, err := st.GachaVersions(*limit)
		if err != nil {
			return err
		}
		fmt.Printf("active_version=%d\n", active)
		for _, v := range vs {
			fmt.Printf("  v%d %s actor=%s at=%s note=%s\n",
				v.Version, v.Digest[:16], v.Actor, v.CreatedAt, v.Note)
		}
		return nil

	case "open-all":
		return cn35RosterOpenAll(*path, *note)

	case "audit":
		st, err := openStore(*path)
		if err != nil {
			return err
		}
		defer st.Close()
		es, err := st.AuditLog(*limit)
		if err != nil {
			return err
		}
		for _, e := range es {
			fmt.Printf("#%d %s %s %s actor=%s\n  %s\n",
				e.AuditID, e.CreatedAt, e.Action, e.Target, e.Actor, e.Detail)
		}
		return nil
	}
	return fmt.Errorf("usage: mercdb cn35-roster import|seed|publish|activate|status|audit|open-all [-flags]")
}

// cn35RosterOpenAll 把名册中全部资源合格且未阻断的单位置为可抽
// （enabled=1），并按各启用草稿池的可达稀有度重写草稿成员后发布。
// 可达口径与 BuildGachaSnapshot 一致：钻石计价池 = {5星} ∪ 生效非五星
// 权重档（缺省回退 4星18/3星80），金币计价池 = distribution 正权重档；
// 最终名单仍由 PublishGachaDraft 的全量校验兜底（缺模板/稀有度不可达/
// 金币池含 5 星等都会整体拒绝，不落半成品版本）。
func cn35RosterOpenAll(path, note string) error {
	st, err := openStore(path)
	if err != nil {
		return err
	}
	defer st.Close()

	pools, err := st.GachaPools()
	if err != nil {
		return err
	}
	reach := map[string]map[int]bool{}
	for _, p := range pools {
		if !p.Enabled {
			continue
		}
		r := map[int]bool{}
		if p.PriceCoin > 0 {
			r[50] = true
			w := p.Pity.NonURWeights
			if len(w) == 0 {
				w = map[int]int{40: 18, 30: 80}
			}
			for k := range w {
				r[k] = true
			}
		} else {
			for k, v := range p.Distribution {
				if v > 0 {
					r[k] = true
				}
			}
		}
		reach[p.PoolID] = r
	}
	if len(reach) == 0 {
		return fmt.Errorf("没有已启用的草稿池")
	}

	var enableIDs []int
	members := map[string][]cn35game.GachaDraftMember{}
	for off := 0; ; off += 500 {
		rows, _, err := st.RosterUnits(cn35game.RosterFilter{
			Resource: "ok", Limit: 500, Offset: off,
		})
		if err != nil {
			return err
		}
		for _, v := range rows {
			if v.Blocked || v.Rare < 1 || v.Rare > 5 {
				continue
			}
			// 与发布校验同口径：无 memorial 数值配置的单位不入池。
			if !cn35game.MemorialUnitConfigured(v.UnitID) {
				continue
			}
			enableIDs = append(enableIDs, v.UnitID)
			for pid, r := range reach {
				if r[v.Rare*10] {
					members[pid] = append(members[pid],
						cn35game.GachaDraftMember{PoolID: pid, UnitID: v.UnitID, Weight: 1})
				}
			}
		}
		if len(rows) < 500 {
			break
		}
	}
	if len(enableIDs) == 0 {
		return fmt.Errorf("没有资源合格的可启用单位")
	}

	yes := true
	if err := st.SetRosterFlags(enableIDs, &yes, nil,
		"open-all：全量资源合格单位启用", rosterActor); err != nil {
		return fmt.Errorf("名册启用失败: %w", err)
	}
	for pid, ms := range members {
		if err := st.ReplaceDraftMembers(pid, ms, rosterActor); err != nil {
			return fmt.Errorf("池 %s 草稿成员写入失败: %w", pid, err)
		}
	}
	if note == "" {
		note = "open-all：放开全部资源合格同伴进卡池"
	}
	v, err := st.PublishGachaDraft(rosterActor, note)
	if err != nil {
		return err
	}
	fmt.Printf("enabled=%d\n", len(enableIDs))
	for _, p := range pools {
		if ms, ok := members[p.PoolID]; ok {
			fmt.Printf("  pool %s (%s): %d 名成员\n", p.PoolID, p.Code, len(ms))
		}
	}
	fmt.Printf("published version=%d digest=%s\n", v.Version, v.Digest)
	return nil
}
