// roster.go CN3.5 名册资源证据 + 模板发布值 + 双卡池版本化治理的
// SQLite 持久实现（ROSTER350-03/05/06，迁移 0013）。
//
// 事务不变式：
//   - 名册导入：sources/templates/revisions/assets/roster 全部在同一事务
//     落库；commit=false 时整包回滚（预览不留痕迹）。
//   - 发布：草稿读出 → BuildGachaSnapshot 校验 → 版本+成员+生效指针+
//     审计同事务；校验失败不产生任何写入。
//   - 回滚/激活：只动 cn35_gacha_release 指针 + 审计；版本行不改写。
//   - 名册状态位：resource_ok/missing_required/wiki 由导入写；
//     enabled/blocked 由管理端写；两者互不越权。
package cn35store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
)

var (
	_ cn35game.GachaConfigStore       = (*Store)(nil)
	_ cn35game.RosterAdminStore       = (*Store)(nil)
	_ cn35game.UnitTemplateReader     = (*Store)(nil)
	_ cn35game.UnitTemplateBulkReader = (*Store)(nil)
)

// ---------------------------------------------------------------------------
// 已发布配置读面（运行时唯一真源）
// ---------------------------------------------------------------------------

// ActiveGachaConfig 读生效指针指向的版本并反解为运行时规则+名单。
// 未发布返回 (nil, nil)。
func (s *Store) ActiveGachaConfig() (*cn35game.ActiveGachaConfig, error) {
	var version int64
	var snapshot string
	err := s.db.QueryRow(`SELECT v.version, v.snapshot
		FROM cn35_gacha_release r JOIN cn35_gacha_pool_versions v ON v.version=r.version
		WHERE r.release_id=1`).Scan(&version, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cn35store: 读已发布卡池版本失败: %w", err)
	}
	cfg, err := cn35game.ParseActiveGachaConfig(version, snapshot)
	if err != nil {
		return nil, err
	}
	sum := snapshotDigest(snapshot)
	cfg.Digest = sum
	return cfg, nil
}

func snapshotDigest(snapshot string) string {
	sum := sha256.Sum256([]byte(snapshot))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// 名册读面
// ---------------------------------------------------------------------------

func scanRosterUnit(row interface{ Scan(...any) error }) (cn35game.RosterUnit, error) {
	var r cn35game.RosterUnit
	var enabled, okRes, wiki, blocked int
	var missing string
	err := row.Scan(&r.UnitID, &enabled, &okRes, &missing, &wiki, &blocked,
		&r.Notes, &r.UpdatedBy, &r.UpdatedAt)
	if err != nil {
		return r, err
	}
	r.Enabled, r.ResourceOK, r.Wiki, r.Blocked = enabled == 1, okRes == 1, wiki == 1, blocked == 1
	_ = json.Unmarshal([]byte(missing), &r.MissingRequired)
	return r, nil
}

const rosterCols = `unit_id,enabled,resource_ok,missing_required,wiki,blocked,notes,updated_by,updated_at`

// rosterState 读单条名册行；无行返回 (zero,false,nil)。
func (s *Store) rosterState(unitID int) (cn35game.RosterUnit, bool, error) {
	r, err := scanRosterUnit(s.db.QueryRow(
		`SELECT `+rosterCols+` FROM cn35_unit_roster WHERE unit_id=?`, unitID))
	if errors.Is(err, sql.ErrNoRows) {
		return cn35game.RosterUnit{}, false, nil
	}
	return r, true, err
}

// RosterOverview 管理端总览（名册计数 + 当前版本 + 草稿成员数）。
func (s *Store) RosterOverview() (*cn35game.RosterOverview, error) {
	o := &cn35game.RosterOverview{
		ResourcePerRare:  map[string]int{},
		DraftMemberCount: map[string]int{},
	}
	if err := s.db.QueryRow(`SELECT count(*), coalesce(sum(wiki),0),
		coalesce(sum(resource_ok),0), coalesce(sum(enabled),0), coalesce(sum(blocked),0)
		FROM cn35_unit_roster`).Scan(&o.UnitsTotal, &o.UnitsWiki,
		&o.UnitsResourceOK, &o.UnitsEnabled, &o.UnitsBlocked); err != nil {
		return nil, fmt.Errorf("cn35store: 名册总览失败: %w", err)
	}
	rows, err := s.db.Query(`SELECT t.rare, count(*) FROM cn35_unit_roster r
		JOIN cn35_unit_templates t ON t.unit_id=r.unit_id
		WHERE r.resource_ok=1 GROUP BY t.rare`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rare, n int
		if err := rows.Scan(&rare, &n); err != nil {
			rows.Close()
			return nil, err
		}
		o.ResourcePerRare[strconv.Itoa(rare)] = n
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT pool_id, count(*) FROM cn35_gacha_pool_draft_members GROUP BY pool_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid string
		var n int
		if err := rows.Scan(&pid, &n); err != nil {
			rows.Close()
			return nil, err
		}
		o.DraftMemberCount[pid] = n
	}
	rows.Close()
	if err := s.db.QueryRow(`SELECT r.version, v.rules_digest FROM cn35_gacha_release r
		JOIN cn35_gacha_pool_versions v ON v.version=r.version WHERE r.release_id=1`).
		Scan(&o.ActiveVersion, &o.ActiveDigest); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return o, nil
}

// RosterUnits 列表查询（名册×模板×草稿归属）。返回 (行集, 过滤后总数)。
func (s *Store) RosterUnits(f cn35game.RosterFilter) ([]cn35game.RosterUnitView, int, error) {
	var where []string
	var args []any
	if f.Rare > 0 {
		where = append(where, `t.rare=?`)
		args = append(args, f.Rare)
	}
	switch f.Resource {
	case "ok":
		where = append(where, `r.resource_ok=1`)
	case "missing":
		where = append(where, `r.resource_ok=0 AND r.wiki=1`)
	case "none":
		where = append(where, `r.resource_ok=0`)
	}
	switch f.Complete {
	case "full":
		// 'null' 与 '[]' 同为"无缺失字段"（修复前行可能写为 'null'，
		// 无模板行的 t.missing 为 SQL NULL，两边都被排除）。
		where = append(where, `t.missing IN ('[]','null')`)
	case "partial":
		where = append(where, `t.missing NOT IN ('[]','null')`)
	}
	if f.Enabled != nil {
		where = append(where, `r.enabled=?`)
		args = append(args, boolInt(*f.Enabled))
	}
	if f.Query != "" {
		if n, err := strconv.Atoi(f.Query); err == nil {
			where = append(where, `r.unit_id=?`)
			args = append(args, n)
		} else {
			where = append(where, `t.name LIKE ?`)
			args = append(args, "%"+f.Query+"%")
		}
	}
	if f.Pool == "none" {
		where = append(where, `r.unit_id NOT IN (
			SELECT m.unit_id FROM cn35_gacha_pool_draft_members m
			JOIN cn35_gacha_pools p ON p.pool_id=m.pool_id AND p.enabled=1)`)
	} else if f.Pool != "" {
		where = append(where, `r.unit_id IN (
			SELECT unit_id FROM cn35_gacha_pool_draft_members WHERE pool_id=?)`)
		args = append(args, f.Pool)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	base := `FROM cn35_unit_roster r LEFT JOIN cn35_unit_templates t ON t.unit_id=r.unit_id` + cond
	var total int
	if err := s.db.QueryRow(`SELECT count(*) `+base, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("cn35store: 名册计数失败: %w", err)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT r.` + strings.ReplaceAll(rosterCols, ",", ",r.") +
		`,t.name,t.rare,t.obtain,t.missing ` + base +
		` ORDER BY r.unit_id LIMIT ? OFFSET ?`
	rows, err := s.db.Query(q, append(args, limit, f.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("cn35store: 名册查询失败: %w", err)
	}
	defer rows.Close()
	var out []cn35game.RosterUnitView
	var idList []int
	for rows.Next() {
		var v cn35game.RosterUnitView
		var name, obtain, missing sql.NullString
		var rare sql.NullInt64
		var enabled, okRes, wiki, blocked int
		var missReq, updBy, updAt string
		if err := rows.Scan(&v.UnitID, &enabled, &okRes, &missReq, &wiki, &blocked,
			&v.Notes, &updBy, &updAt, &name, &rare, &obtain, &missing); err != nil {
			return nil, 0, err
		}
		v.Enabled, v.ResourceOK, v.Wiki, v.Blocked = enabled == 1, okRes == 1, wiki == 1, blocked == 1
		v.UpdatedBy, v.UpdatedAt = updBy, updAt
		_ = json.Unmarshal([]byte(missReq), &v.MissingRequired)
		if name.Valid {
			v.Name = name.String
		}
		if rare.Valid {
			v.Rare = int(rare.Int64)
		}
		if obtain.Valid {
			v.Obtain = obtain.String
		}
		if missing.Valid {
			_ = json.Unmarshal([]byte(missing.String), &v.Missing)
		}
		out = append(out, v)
		idList = append(idList, v.UnitID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// 草稿归属（池 id 列表），按本页 id 集查询。
	poolMap, err := s.draftPoolsOfUnits(idList)
	if err != nil {
		return nil, 0, err
	}
	for i := range out {
		out[i].Pools = poolMap[out[i].UnitID]
	}
	return out, total, nil
}

func (s *Store) draftPoolsOfUnits(ids []int) (map[int][]string, error) {
	out := map[int][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT unit_id, pool_id FROM cn35_gacha_pool_draft_members
		WHERE unit_id IN (`+ph+`) ORDER BY pool_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid int
		var pid string
		if err := rows.Scan(&uid, &pid); err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], pid)
	}
	return out, rows.Err()
}

// RosterUnitDetail unit 详情：模板 + 资源证据 + 名册状态 + 草稿/发布归属。
func (s *Store) RosterUnitDetail(unitID int) (*cn35game.RosterUnitDetail, error) {
	d := &cn35game.RosterUnitDetail{PublishedIn: map[string]int64{}}
	r, ok, err := s.rosterState(unitID)
	if err != nil {
		return nil, err
	}
	if !ok {
		r = cn35game.RosterUnit{UnitID: unitID}
	}
	d.Roster = r
	t, has, err := s.UnitTemplate(unitID)
	if err != nil {
		return nil, err
	}
	if has {
		d.Template = t
		d.WikiRaw = t.RawJSON
	}
	rows, err := s.db.Query(`SELECT family,bundle,size,crc32,sha256,served,index_sha256,index_match
		FROM cn35_unit_assets WHERE unit_id=? ORDER BY family`, unitID)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 资源证据读取失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a cn35game.UnitAsset
		var served int
		var im sql.NullInt64
		if err := rows.Scan(&a.Family, &a.Bundle, &a.Size, &a.CRC32, &a.SHA256,
			&served, &a.IndexSHA256, &im); err != nil {
			return nil, err
		}
		a.Served = served == 1
		if im.Valid {
			b := im.Int64 == 1
			a.IndexMatch = &b
		}
		d.Assets = append(d.Assets, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pm, err := s.draftPoolsOfUnits([]int{unitID})
	if err != nil {
		return nil, err
	}
	d.DraftPools = pm[unitID]
	rows2, err := s.db.Query(`SELECT pool_id, max(version) FROM cn35_gacha_pool_version_members
		WHERE unit_id=? GROUP BY pool_id`, unitID)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var pid string
		var v int64
		if err := rows2.Scan(&pid, &v); err != nil {
			return nil, err
		}
		d.PublishedIn[pid] = v
	}
	return d, rows2.Err()
}

// ---------------------------------------------------------------------------
// 模板读写
// ---------------------------------------------------------------------------

const templateCols = `unit_id,name,name_jp,title,title_jp,rare,element,weapon,unit_type,
	life,atk,aarea,anum,mspd,aspd,hits,tenacity,fire,aqua,wind,light,dark,gender,age,
	country,career,interest,nature,obtain,remark,wiki_gacha,contributors,missing,raw_json,source_version`

func scanTemplate(row interface{ Scan(...any) error }) (*cn35game.UnitTemplate, error) {
	var t cn35game.UnitTemplate
	var anum, mspd, hits, gender sql.NullInt64
	var aspd, tenacity, fire, aqua, wind, light, dark sql.NullFloat64
	var age sql.NullString
	var gacha, contrib, missing string
	err := row.Scan(&t.UnitID, &t.Name, &t.NameJP, &t.Title, &t.TitleJP,
		&t.Rare, &t.Element, &t.Weapon, &t.Type, &t.Life, &t.Atk, &t.AArea,
		&anum, &mspd, &aspd, &hits, &tenacity, &fire, &aqua, &wind, &light, &dark,
		&gender, &age, &t.Country, &t.Career, &t.Interest, &t.Nature,
		&t.Obtain, &t.Remark, &gacha, &contrib, &missing, &t.RawJSON, &t.SourceVersion)
	if err != nil {
		return nil, err
	}
	setInt := func(p **int, v sql.NullInt64) {
		if v.Valid {
			n := int(v.Int64)
			*p = &n
		}
	}
	setF := func(p **float64, v sql.NullFloat64) {
		if v.Valid {
			f := v.Float64
			*p = &f
		}
	}
	setInt(&t.ANum, anum)
	setInt(&t.MSpd, mspd)
	setInt(&t.Hits, hits)
	setInt(&t.Gender, gender)
	setF(&t.ASPd, aspd)
	setF(&t.Tenacity, tenacity)
	setF(&t.Fire, fire)
	setF(&t.Aqua, aqua)
	setF(&t.Wind, wind)
	setF(&t.Light, light)
	setF(&t.Dark, dark)
	if age.Valid {
		t.Age = &age.String
	}
	_ = json.Unmarshal([]byte(gacha), &t.WikiGacha)
	_ = json.Unmarshal([]byte(contrib), &t.Contributors)
	_ = json.Unmarshal([]byte(missing), &t.Missing)
	return &t, nil
}

// UnitTemplate 按 id 读模板行；(nil,false,nil) = 无模板（回退静态 catalog）。
func (s *Store) UnitTemplate(unitID int) (*cn35game.UnitTemplate, bool, error) {
	t, err := scanTemplate(s.db.QueryRow(
		`SELECT `+templateCols+` FROM cn35_unit_templates WHERE unit_id=?`, unitID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cn35store: 读模板 %d 失败: %w", unitID, err)
	}
	return t, true, nil
}

// AllUnitTemplates 一条 SELECT 读出全部模板行（rosterCatalog 快照重建用）。
func (s *Store) AllUnitTemplates() ([]*cn35game.UnitTemplate, error) {
	rows, err := s.db.Query(`SELECT ` + templateCols + ` FROM cn35_unit_templates ORDER BY unit_id`)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 读全部模板失败: %w", err)
	}
	defer rows.Close()
	var out []*cn35game.UnitTemplate
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("cn35store: 读全部模板失败: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cn35store: 读全部模板失败: %w", err)
	}
	return out, nil
}

// UnitTemplateGeneration 本进程模板写入代际号（成功提交后递增）。
// 其他进程对同一库的写入不可见：mercdb cn35-roster import -commit 后须重启服务。
func (s *Store) UnitTemplateGeneration() uint64 { return s.tplGen.Load() }

// templateSnapshot 模板行全字段 JSON（修订历史快照）。
// nil 切片按 [] 归一化：'null' 与 '[]' 语义同为"无缺失"，但 marshal 文本
// 不等——字段级差异比较必须统一口径，否则"库内 '[]'、输入 nil"会被误判为
// 变化而反复 upsert（破坏导入幂等）。
func templateSnapshot(t *cn35game.UnitTemplate) string {
	c := *t
	if c.WikiGacha == nil {
		c.WikiGacha = []int{}
	}
	if c.Contributors == nil {
		c.Contributors = []string{}
	}
	if c.Missing == nil {
		c.Missing = []string{}
	}
	b, _ := json.Marshal(&c)
	return string(b)
}

// SaveUnitTemplate 更新模板行 + 追加修订史 + 审计（同事务）。
func (s *Store) SaveUnitTemplate(t *cn35game.UnitTemplate, actor, note string) error {
	if t == nil || t.UnitID <= 0 || t.Name == "" || t.Rare < 1 {
		return fmt.Errorf("cn35store: 模板字段不全（id/name/rare 必填）")
	}
	defer s.tplGen.Add(1) // 失败/无变化时多递增一次只会多重建一次快照，无正确性影响
	return s.withTx(func(tx *sql.Tx) error {
		old, err := scanTemplate(tx.QueryRow(
			`SELECT `+templateCols+` FROM cn35_unit_templates WHERE unit_id=?`, t.UnitID))
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if exists && templateSnapshot(old) == templateSnapshot(t) {
			return nil // 幂等：无变化不审计
		}
		srcID := int64(0)
		if !exists {
			var serr error
			srcID, serr = upsertSourceTx(tx, "admin", "manual-edit", "", "", map[string]any{})
			if serr != nil {
				return serr
			}
		}
		if err := upsertTemplateTx(tx, t, srcID); err != nil {
			return err
		}
		var rev int
		_ = tx.QueryRow(`SELECT coalesce(max(revision),0)+1 FROM cn35_unit_template_revisions
			WHERE unit_id=?`, t.UnitID).Scan(&rev)
		if _, err := tx.Exec(`INSERT INTO cn35_unit_template_revisions
			(unit_id,revision,snapshot,actor,note,created_at) VALUES (?,?,?,?,?,?)`,
			t.UnitID, rev, templateSnapshot(t), actor, note, nowUTC()); err != nil {
			return err
		}
		return appendAuditTx(tx, actor, "unit.template.save", strconv.Itoa(t.UnitID),
			map[string]any{"revision": rev, "note": note})
	})
}

// upsertTemplateTx 模板行 upsert。srcID 仅用于新建行；既有行的 source_id
// 保持不变（管理修订不改原始溯源）。
func upsertTemplateTx(tx *sql.Tx, t *cn35game.UnitTemplate, srcID int64) error {
	gacha, _ := json.Marshal(t.WikiGacha)
	contrib, _ := json.Marshal(t.Contributors)
	missing, _ := json.Marshal(t.Missing)
	// nil 切片归一化为 []——'null' 与 '[]' 语义同为"无缺失"，但
	// missing 列是 complete=full/partial 过滤的文本依据，必须单一形态。
	if t.WikiGacha == nil {
		gacha = []byte("[]")
	}
	if t.Contributors == nil {
		contrib = []byte("[]")
	}
	if t.Missing == nil {
		missing = []byte("[]")
	}
	raw := t.RawJSON
	if raw == "" {
		raw = "{}"
	}
	_, err := tx.Exec(`INSERT INTO cn35_unit_templates (`+templateCols+`,source_id,updated_at)
		VALUES (`+strings.Repeat("?,", 36)+`?)
		ON CONFLICT(unit_id) DO UPDATE SET
		name=excluded.name,name_jp=excluded.name_jp,title=excluded.title,title_jp=excluded.title_jp,
		rare=excluded.rare,element=excluded.element,weapon=excluded.weapon,unit_type=excluded.unit_type,
		life=excluded.life,atk=excluded.atk,aarea=excluded.aarea,anum=excluded.anum,
		mspd=excluded.mspd,aspd=excluded.aspd,hits=excluded.hits,tenacity=excluded.tenacity,
		fire=excluded.fire,aqua=excluded.aqua,wind=excluded.wind,light=excluded.light,dark=excluded.dark,
		gender=excluded.gender,age=excluded.age,country=excluded.country,career=excluded.career,
		interest=excluded.interest,nature=excluded.nature,obtain=excluded.obtain,remark=excluded.remark,
		wiki_gacha=excluded.wiki_gacha,contributors=excluded.contributors,missing=excluded.missing,
		raw_json=excluded.raw_json,source_version=excluded.source_version,updated_at=excluded.updated_at`,
		t.UnitID, t.Name, t.NameJP, t.Title, t.TitleJP, t.Rare, t.Element, t.Weapon, t.Type,
		t.Life, t.Atk, t.AArea, t.ANum, t.MSpd, t.ASPd, t.Hits, t.Tenacity,
		t.Fire, t.Aqua, t.Wind, t.Light, t.Dark, t.Gender, t.Age,
		t.Country, t.Career, t.Interest, t.Nature, t.Obtain, t.Remark,
		string(gacha), string(contrib), string(missing), raw, t.SourceVersion,
		srcID, nowUTC())
	return err
}

// ---------------------------------------------------------------------------
// 名册导入（mercdb cn35-roster-import / admin 触发共用）
// ---------------------------------------------------------------------------

// ImportRoster 在**单事务**内执行清单+模板导入。commit=false 时整包回滚
// （差异照常统计——预览路径与写入路径同一份代码，杜绝"预览一套写一套"）。
// 幂等：同一份清单重复导入不产生新写；enabled/blocked 位导入不触碰。
func (s *Store) ImportRoster(in *cn35game.RosterImport, commit bool, actor string) (*cn35game.RosterImportResult, error) {
	if in == nil || in.Manifest == nil {
		return nil, fmt.Errorf("cn35store: 导入输入缺少清单")
	}
	res := &cn35game.RosterImportResult{Committed: commit}
	err := s.withTx(func(tx *sql.Tx) error {
		// 1) 来源登记（kind+sha256 幂等去重）。
		manifestSrc, err := upsertSourceTx(tx, "apk_manifest", "unit_roster_manifest",
			"", in.ManifestSHA, map[string]any{
				"apk_sha256": in.Manifest.APK.SHA256,
				"apk_path":   in.Manifest.APK.Path,
				"path":       in.ManifestPath,
				"required":   in.Manifest.Families.Required,
				"optional":   in.Manifest.Families.Optional,
			})
		if err != nil {
			return err
		}
		wikiSrc, err := upsertSourceTx(tx, "wiki", "units.json",
			in.UnitsVersion, in.UnitsSHA, map[string]any{"path": in.UnitsPath})
		if err != nil {
			return err
		}

		// 2) 资源证据 upsert + 名册证据位重算。
		//    cn35_unit_assets 是清单的物化视图：同事务内先清后插——
		//    清单重生成后撤收的族/单位不得残留陈旧证据行（此表仅此一处写）。
		if _, err := tx.Exec(`DELETE FROM cn35_unit_assets`); err != nil {
			return fmt.Errorf("资源证据清表失败: %w", err)
		}
		assetStmt, err := tx.Prepare(`INSERT INTO cn35_unit_assets
			(unit_id,family,bundle,size,crc32,sha256,served,index_sha256,index_match,source_id,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(unit_id,family) DO UPDATE SET
			bundle=excluded.bundle,size=excluded.size,crc32=excluded.crc32,
			sha256=excluded.sha256,served=excluded.served,
			index_sha256=excluded.index_sha256,index_match=excluded.index_match,
			source_id=excluded.source_id,updated_at=excluded.updated_at`)
		if err != nil {
			return fmt.Errorf("准备资源证据语句失败: %w", err)
		}
		defer assetStmt.Close()
		rosterStmt, err := tx.Prepare(`INSERT INTO cn35_unit_roster
			(unit_id,enabled,resource_ok,missing_required,wiki,blocked,notes,updated_by,updated_at)
			VALUES (?,0,?,?,?,0,'',?,?)
			ON CONFLICT(unit_id) DO UPDATE SET
			resource_ok=excluded.resource_ok,missing_required=excluded.missing_required,
			wiki=excluded.wiki,updated_at=excluded.updated_at`)
		if err != nil {
			return fmt.Errorf("准备名册行语句失败: %w", err)
		}
		defer rosterStmt.Close()
		now := nowUTC()
		assetRows, rosterRows, nonWiki := 0, 0, 0
		for idStr, u := range in.Manifest.Units {
			id, aerr := strconv.Atoi(idStr)
			if aerr != nil || id < 0 {
				continue
			}
			if !u.Wiki {
				nonWiki++
			}
			for fam, ev := range u.Families {
				if ev == nil {
					continue
				}
				// 与 served 索引键同源：unit/unit_<fam>_<id>（fam=unit 时
				// 退化为 unit/unit_<id>）。写错名字会让证据行无法回对索引。
				bundle := "unit/unit_" + idStr
				if fam != "unit" {
					bundle = "unit/unit_" + fam + "_" + idStr
				}
				var im *int64
				if ev.IndexMatch != nil {
					v := int64(0)
					if *ev.IndexMatch {
						v = 1
					}
					im = &v
				}
				if _, err := assetStmt.Exec(
					id, fam, bundle, ev.Size, ev.CRC32, ev.SHA256, boolInt(ev.Served),
					ev.IndexSHA256, im, manifestSrc, now); err != nil {
					return fmt.Errorf("资源证据 %s/%s 写入失败: %w", idStr, fam, err)
				}
				assetRows++
			}
			miss, _ := json.Marshal(u.MissingRequired)
			if u.MissingRequired == nil {
				miss = []byte("[]")
			}
			if _, err := rosterStmt.Exec(
				id, boolInt(u.Qualified), string(miss), boolInt(u.Wiki),
				actor, now); err != nil {
				return fmt.Errorf("名册行 %s 写入失败: %w", idStr, err)
			}
			rosterRows++
			if u.Wiki && !u.Qualified {
				res.Disqualified = append(res.Disqualified,
					fmt.Sprintf("%s(rare%d 缺:%s)", idStr, u.Rare, strings.Join(u.MissingRequired, ",")))
			}
			if u.Qualified {
				res.ResourceOKCount++
			}
		}
		res.AssetRows = assetRows
		res.RosterRows = rosterRows
		if nonWiki > 0 {
			res.Notes = append(res.Notes,
				fmt.Sprintf("清单中非 wiki 单位 %d 个（只留证据，不进 wiki 名册）", nonWiki))
		}

		// 2.5) 清单未覆盖的既有名册行：资源证据归零。清单是资源证据的唯一
		//      真源——重生成后撤收的单位、以及管理端手工建的无证据行，
		//      都不得残留旧的 qualified/missing_required 位。
		//      assets 表此时即清单全集，故 NOT IN 子查询即"清单未覆盖"。
		missingAll, _ := json.Marshal(cn35game.RosterRequiredFamilies)
		reset, err := tx.Exec(`UPDATE cn35_unit_roster
			SET resource_ok=0, missing_required=?, updated_at=?
			WHERE unit_id NOT IN (SELECT unit_id FROM cn35_unit_assets)
			AND (resource_ok<>0 OR missing_required<>?)`,
			string(missingAll), nowUTC(), string(missingAll))
		if err != nil {
			return fmt.Errorf("陈旧名册行证据归零失败: %w", err)
		}
		if n, _ := reset.RowsAffected(); n > 0 {
			res.Notes = append(res.Notes,
				fmt.Sprintf("清单未覆盖的 %d 条名册行已重置资源证据", n))
		}

		// 3) 模板 upsert（字段级差异才计 upsert；逐字段比较而不是按行覆盖）。
		for i := range in.Templates {
			t := &in.Templates[i]
			if t.UnitID <= 0 {
				continue
			}
			t.SourceVersion = in.UnitsVersion
			old, err := scanTemplate(tx.QueryRow(
				`SELECT `+templateCols+` FROM cn35_unit_templates WHERE unit_id=?`, t.UnitID))
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				old.SourceVersion = t.SourceVersion
				if templateSnapshot(old) == templateSnapshot(t) {
					res.TemplateUnchanged++
					continue
				}
			}
			// 新建行以 wiki 来源落 source_id；既有行保留原溯源。
			if err := upsertTemplateTx(tx, t, wikiSrc); err != nil {
				return fmt.Errorf("模板 %d 写入失败: %w", t.UnitID, err)
			}
			var rev int
			_ = tx.QueryRow(`SELECT coalesce(max(revision),0)+1 FROM cn35_unit_template_revisions
				WHERE unit_id=?`, t.UnitID).Scan(&rev)
			if _, err := tx.Exec(`INSERT INTO cn35_unit_template_revisions
				(unit_id,revision,snapshot,actor,note,created_at) VALUES (?,?,?,?,?,?)`,
				t.UnitID, rev, templateSnapshot(t), actor, "roster.import", nowUTC()); err != nil {
				return err
			}
			res.TemplateUpserts++
		}

		// 4) wiki 在册但清单未覆盖的单位补名册行（resource_ok=0）。
		//    只补 wiki 单位：清单外单位（APK 独有 id）已由步骤2覆盖。
		//    missing_required 落全部必需族——没有任何资源证据 = 八族全缺，
		//    写 '[]' 会与"证据齐全"的管理读面口径矛盾。
		wikiAbsent := 0
		for i := range in.Templates {
			id := in.Templates[i].UnitID
			key := strconv.Itoa(id)
			if _, inManifest := in.Manifest.Units[key]; inManifest {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO cn35_unit_roster
				(unit_id,enabled,resource_ok,missing_required,wiki,blocked,notes,updated_by,updated_at)
				VALUES (?,0,0,?,1,0,'wiki 在册但 APK 清单未覆盖',?,?)
				ON CONFLICT(unit_id) DO UPDATE SET wiki=1, updated_at=excluded.updated_at`,
				id, string(missingAll), actor, nowUTC()); err != nil {
				return err
			}
			wikiAbsent++
		}
		if wikiAbsent > 0 {
			res.Notes = append(res.Notes,
				fmt.Sprintf("wiki 在册但清单未覆盖 %d 个（保持 resource_ok=0）", wikiAbsent))
		}

		if err := tx.QueryRow(`SELECT count(*) FROM cn35_unit_roster WHERE enabled=1`).
			Scan(&res.RosterEnabledNow); err != nil {
			return err
		}
		if err := appendAuditTx(tx, actor, "roster.import", in.ManifestPath, map[string]any{
			"commit": commit, "manifest_sha256": in.ManifestSHA,
			"units_sha256": in.UnitsSHA, "units_version": in.UnitsVersion,
			"asset_rows": res.AssetRows, "roster_rows": res.RosterRows,
			"template_upserts": res.TemplateUpserts,
		}); err != nil {
			return err
		}
		if !commit {
			return errRosterPreview // 预览：同一路径写入后整包回滚
		}
		return nil
	})
	if errors.Is(err, errRosterPreview) {
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	s.tplGen.Add(1) // 已提交：令进程内模板快照重建
	return res, nil
}

var errRosterPreview = errors.New("cn35store: 预览导入回滚")

// upsertSourceTx 登记来源行；同 (kind,sha256) 复用旧行（幂等锚点）。
func upsertSourceTx(tx *sql.Tx, kind, name, version, sha string, meta map[string]any) (int64, error) {
	mb, _ := json.Marshal(meta)
	var id int64
	err := tx.QueryRow(`SELECT source_id FROM cn35_roster_sources
		WHERE kind=? AND sha256=?`, kind, sha).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO cn35_roster_sources (kind,name,version,sha256,meta,created_at)
		VALUES (?,?,?,?,?,?)`, kind, name, version, sha, string(mb), nowUTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func appendAuditTx(tx *sql.Tx, actor, action, target string, detail any) error {
	db, _ := json.Marshal(detail)
	_, err := tx.Exec(`INSERT INTO cn35_admin_audit (actor,action,target,detail,created_at)
		VALUES (?,?,?,?,?)`, actor, action, target, string(db), nowUTC())
	return err
}

// ---------------------------------------------------------------------------
// 名册管理位写面
// ---------------------------------------------------------------------------

// SetRosterFlags 批量设置 enabled/blocked（管理位），附审计。
// 不动证据位；缺行自动建行（wiki 未在册的单位也允许显式标记）。
func (s *Store) SetRosterFlags(unitIDs []int, enabled *bool, blocked *bool, note, actor string) error {
	if len(unitIDs) == 0 {
		return fmt.Errorf("cn35store: 名册操作未给出单位")
	}
	if enabled == nil && blocked == nil {
		return fmt.Errorf("cn35store: 名册操作未给出变更")
	}
	return s.withTx(func(tx *sql.Tx) error {
		for _, id := range unitIDs {
			if id <= 0 {
				return fmt.Errorf("cn35store: 非法 unit_id %d", id)
			}
			sets := []string{"updated_by=?", "updated_at=?"}
			args := []any{actor, nowUTC()}
			if enabled != nil {
				sets = append(sets, "enabled=?")
				args = append(args, boolInt(*enabled))
			}
			if blocked != nil {
				sets = append(sets, "blocked=?")
				args = append(args, boolInt(*blocked))
			}
			if note != "" {
				sets = append(sets, "notes=?")
				args = append(args, note)
			}
			args = append(args, id)
			res, err := tx.Exec(`UPDATE cn35_unit_roster SET `+
				strings.Join(sets, ",")+` WHERE unit_id=?`, args...)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				// 名册行不存在（未导入过）：建行并置证据位为 0。
				en, bl := int64(0), int64(0)
				if enabled != nil {
					en = boolInt64(*enabled)
				}
				if blocked != nil {
					bl = boolInt64(*blocked)
				}
				if _, err := tx.Exec(`INSERT INTO cn35_unit_roster
					(unit_id,enabled,resource_ok,missing_required,wiki,blocked,notes,updated_by,updated_at)
					VALUES (?,?,0,'[]',0,?,?,?,?)`,
					id, en, bl, note, actor, nowUTC()); err != nil {
					return err
				}
			}
		}
		act := "unit.flags"
		if enabled != nil {
			act = map[bool]string{true: "unit.enable", false: "unit.disable"}[*enabled]
		}
		return appendAuditTx(tx, actor, act, joinInts(unitIDs), map[string]any{
			"enabled": enabled, "blocked": blocked, "note": note, "count": len(unitIDs),
		})
	})
}

// ---------------------------------------------------------------------------
// 池草稿读面
// ---------------------------------------------------------------------------

func scanPoolDef(row interface{ Scan(...any) error }) (cn35game.GachaPoolDef, error) {
	var p cn35game.GachaPoolDef
	var enabled, limited int
	var featured, dist, pity string
	err := row.Scan(&p.PoolID, &p.Slot, &p.ListTypeID, &p.Code, &p.Name, &p.Description,
		&enabled, &p.PricePoint, &p.PriceCoin, &p.MaxExec, &p.MaxBonus,
		&limited, &p.BeginAt, &p.EndAt, &featured, &dist, &pity, &p.PityGroup, &p.Notes)
	if err != nil {
		return p, err
	}
	p.Enabled, p.Limited = enabled == 1, limited == 1
	if featured != "" && featured != "null" && featured != "[]" {
		if err := json.Unmarshal([]byte(featured), &p.FeaturedIDs); err != nil {
			return p, fmt.Errorf("池 %s featured_ids 列解析失败: %w", p.PoolID, err)
		}
	}
	if dist != "" && dist != "{}" && dist != "null" {
		var m map[string]int
		if err := json.Unmarshal([]byte(dist), &m); err != nil {
			return p, fmt.Errorf("池 %s distribution 列解析失败: %w", p.PoolID, err)
		}
		p.Distribution = map[int]int{}
		for k, v := range m {
			r, err := strconv.Atoi(k)
			if err != nil {
				return p, fmt.Errorf("池 %s distribution 稀有度键 %q 非法", p.PoolID, k)
			}
			p.Distribution[r] = v
		}
	}
	if pity != "" && pity != "{}" && pity != "null" {
		pv, perr := cn35game.ParsePityJSON([]byte(pity))
		if perr != nil {
			return p, fmt.Errorf("池 %s pity 列解析失败: %w", p.PoolID, perr)
		}
		p.Pity = pv
	}
	return p, nil
}

// GachaPools 读全部草稿池定义（含停用行）。
func (s *Store) GachaPools() ([]cn35game.GachaPoolDef, error) {
	rows, err := s.db.Query(`SELECT pool_id,slot,list_type_id,code,name,description,enabled,
		price_point,price_coin,max_exec,max_bonus,limited,begin_at,end_at,
		featured_ids,distribution,pity,pity_group,notes
		FROM cn35_gacha_pools ORDER BY slot`)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 读卡池草稿失败: %w", err)
	}
	defer rows.Close()
	var out []cn35game.GachaPoolDef
	for rows.Next() {
		p, err := scanPoolDef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GachaDraftMembers 读全部草稿成员（pool→列表）。
func (s *Store) GachaDraftMembers() (map[string][]cn35game.GachaDraftMember, error) {
	rows, err := s.db.Query(`SELECT pool_id,unit_id,weight FROM cn35_gacha_pool_draft_members
		ORDER BY pool_id,unit_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]cn35game.GachaDraftMember{}
	for rows.Next() {
		var m cn35game.GachaDraftMember
		if err := rows.Scan(&m.PoolID, &m.UnitID, &m.Weight); err != nil {
			return nil, err
		}
		out[m.PoolID] = append(out[m.PoolID], m)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// 池草稿写面
// ---------------------------------------------------------------------------

// SaveGachaPoolDef upsert 草稿池定义（审计）。不改成员。
func (s *Store) SaveGachaPoolDef(def cn35game.GachaPoolDef, actor string) error {
	if def.PoolID == "" {
		return fmt.Errorf("cn35store: 池定义缺 pool_id")
	}
	featured, _ := json.Marshal(def.FeaturedIDs)
	dist := map[string]int{}
	for r, w := range def.Distribution {
		dist[strconv.Itoa(r)] = w
	}
	distJSON, _ := json.Marshal(dist)
	pityJSON := cn35game.MarshalPityJSON(def.Pity)
	return s.withTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO cn35_gacha_pools
			(pool_id,slot,list_type_id,code,name,description,enabled,price_point,price_coin,
			 max_exec,max_bonus,limited,begin_at,end_at,featured_ids,distribution,pity,pity_group,notes,updated_by,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(pool_id) DO UPDATE SET
			slot=excluded.slot,list_type_id=excluded.list_type_id,code=excluded.code,
			name=excluded.name,description=excluded.description,enabled=excluded.enabled,
			price_point=excluded.price_point,price_coin=excluded.price_coin,
			max_exec=excluded.max_exec,max_bonus=excluded.max_bonus,limited=excluded.limited,
			begin_at=excluded.begin_at,end_at=excluded.end_at,featured_ids=excluded.featured_ids,
			distribution=excluded.distribution,pity=excluded.pity,pity_group=excluded.pity_group,
			notes=excluded.notes,updated_by=excluded.updated_by,updated_at=excluded.updated_at`,
			def.PoolID, def.Slot, def.ListTypeID, def.Code, def.Name, def.Description,
			boolInt(def.Enabled), def.PricePoint, def.PriceCoin, def.MaxExec, def.MaxBonus,
			boolInt(def.Limited), def.BeginAt, def.EndAt, string(featured), string(distJSON),
			string(pityJSON), def.PityGroup, def.Notes, actor, nowUTC())
		if err != nil {
			return err
		}
		return appendAuditTx(tx, actor, "pool.def.save", def.PoolID, map[string]any{
			"enabled": def.Enabled, "price_point": def.PricePoint, "price_coin": def.PriceCoin,
		})
	})
}

// ReplaceDraftMembers 整体替换某池的草稿成员（审计记录成员数与哈希）。
func (s *Store) ReplaceDraftMembers(poolID string, members []cn35game.GachaDraftMember, actor string) error {
	return s.withTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_gacha_pools WHERE pool_id=?`,
			poolID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("%w: 草稿池 %s", cn35game.ErrRosterNotFound, poolID)
		}
		seen := map[int]bool{}
		for _, m := range members {
			if m.UnitID <= 0 || m.Weight <= 0 {
				return fmt.Errorf("cn35store: 草稿成员非法 (unit=%d weight=%d)", m.UnitID, m.Weight)
			}
			if seen[m.UnitID] {
				return fmt.Errorf("cn35store: 草稿成员 %d 重复", m.UnitID)
			}
			seen[m.UnitID] = true
		}
		if _, err := tx.Exec(`DELETE FROM cn35_gacha_pool_draft_members WHERE pool_id=?`,
			poolID); err != nil {
			return err
		}
		ids := make([]int, 0, len(members))
		for _, m := range members {
			if _, err := tx.Exec(`INSERT INTO cn35_gacha_pool_draft_members
				(pool_id,unit_id,weight,updated_by,updated_at) VALUES (?,?,?,?,?)`,
				poolID, m.UnitID, m.Weight, actor, nowUTC()); err != nil {
				return err
			}
			ids = append(ids, m.UnitID)
		}
		return appendAuditTx(tx, actor, "pool.members.replace", poolID, map[string]any{
			"count": len(ids),
		})
	})
}

// ---------------------------------------------------------------------------
// 发布 / 版本 / 审计
// ---------------------------------------------------------------------------

// PublishGachaDraft 单事务：读草稿 → 校验 → 版本+成员+生效指针+审计。
// 同内容重复发布按 digest 去重复用旧版本行（仍落审计）。
func (s *Store) PublishGachaDraft(actor, note string) (*cn35game.GachaConfigVersion, error) {
	var out cn35game.GachaConfigVersion
	err := s.withTx(func(tx *sql.Tx) error {
		pools, err := s.gachaPoolsTx(tx)
		if err != nil {
			return err
		}
		members, err := gachaDraftMembersTx(tx)
		if err != nil {
			return err
		}
		v, err := s.publishGachaCommitTx(tx, pools, members, actor, note)
		if err != nil {
			return err
		}
		out = *v
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// gachaDraftMembersTx 事务内读全部草稿成员。
func gachaDraftMembersTx(tx *sql.Tx) (map[string][]cn35game.GachaDraftMember, error) {
	members := map[string][]cn35game.GachaDraftMember{}
	rows, err := tx.Query(`SELECT pool_id,unit_id,weight FROM cn35_gacha_pool_draft_members
		ORDER BY pool_id,unit_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var m cn35game.GachaDraftMember
		if err := rows.Scan(&m.PoolID, &m.UnitID, &m.Weight); err != nil {
			rows.Close()
			return nil, err
		}
		members[m.PoolID] = append(members[m.PoolID], m)
	}
	rows.Close()
	return members, rows.Err()
}

// RepublishGachaFiltered 存量库一次性重发：单事务内读全部池草稿成员，
// 剔除 configured 判定为无配置的单位后重发新版本（同事务写草稿+校验+
// 版本+生效指针+审计，失败整包回滚）。与种子名单无关——保留管理员自定义
// 成员与权重，只移除不合格单位；覆盖所有启用池而非仅种子池。
//
// 返回 (version, republished, err)：republished=false 表示激活版本成员已
// 全部合格（或无任何激活版本且草稿为空），未做写入。
func (s *Store) RepublishGachaFiltered(actor, note string, configured func(unitID int) bool) (*cn35game.GachaConfigVersion, bool, error) {
	var out *cn35game.GachaConfigVersion
	err := s.withTx(func(tx *sql.Tx) error {
		// 运行期真源是激活版本：仅当激活成员含不合格单位时才需要重发。
		// 逐版本检查覆盖所有启用池（含种子外的自定义池）。
		var activeVersion int64
		err := tx.QueryRow(`SELECT version FROM cn35_gacha_release WHERE release_id=1`).Scan(&activeVersion)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		dirty := activeVersion == 0 // 无激活版本按需发布一次（草稿可能待发布）
		if !dirty {
			rows, err := tx.Query(`SELECT unit_id FROM cn35_gacha_pool_version_members WHERE version=?`, activeVersion)
			if err != nil {
				return err
			}
			for rows.Next() {
				var uid int
				if err := rows.Scan(&uid); err != nil {
					rows.Close()
					return err
				}
				if !configured(uid) {
					dirty = true
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		if !dirty {
			return nil // 激活名单已全部合格，不动草稿
		}
		pools, err := s.gachaPoolsTx(tx)
		if err != nil {
			return err
		}
		members, err := gachaDraftMembersTx(tx)
		if err != nil {
			return err
		}
		candidate := map[string][]cn35game.GachaDraftMember{}
		for pid, ms := range members {
			for _, m := range ms {
				if configured(m.UnitID) {
					candidate[pid] = append(candidate[pid], m)
				}
			}
		}
		// 同事务先写回过滤后的草稿成员（池存在性校验同 ReplaceDraftMembers）。
		// 注意按原草稿池集合写回：整池成员都不合格的池过滤后为空，也必须
		// 清空其草稿行，否则发布校验会读到残留成员。
		for pid, ms := range members {
			ms = candidate[pid]
			var exists int
			if err := tx.QueryRow(`SELECT count(*) FROM cn35_gacha_pools WHERE pool_id=?`, pid).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM cn35_gacha_pool_draft_members WHERE pool_id=?`, pid); err != nil {
				return err
			}
			for _, m := range ms {
				if _, err := tx.Exec(`INSERT INTO cn35_gacha_pool_draft_members
					(pool_id,unit_id,weight,updated_by,updated_at) VALUES (?,?,?,?,?)`,
					pid, m.UnitID, m.Weight, actor, nowUTC()); err != nil {
					return err
				}
			}
		}
		// 校验+版本+指针+审计同事务：发布被拒则上面草稿写回一并回滚。
		v, err := s.publishGachaCommitTx(tx, pools, candidate, actor, note)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, out != nil, nil
}

// publishGachaCommitTx 发布事务的校验+落库段：快照校验 → 版本行（digest
// 去重）→ 生效指针 → 审计。调用方须已在外层事务内。
func (s *Store) publishGachaCommitTx(tx *sql.Tx, pools []cn35game.GachaPoolDef,
	members map[string][]cn35game.GachaDraftMember, actor, note string) (*cn35game.GachaConfigVersion, error) {

	snapRaw, digest, flat, err := cn35game.BuildGachaSnapshot(pools, members,
		func(unitID int) (cn35game.RosterUnit, bool) {
			r, ok, rerr := scanRosterUnitOK(tx, unitID)
			if rerr != nil {
				return cn35game.RosterUnit{}, false
			}
			return r, ok
		},
		func(unitID int) (*cn35game.UnitTemplate, bool) {
			t, err := scanTemplate(tx.QueryRow(
				`SELECT `+templateCols+` FROM cn35_unit_templates WHERE unit_id=?`, unitID))
			return t, err == nil
		})
	if err != nil {
		return nil, err
	}

	var version int64
	err = tx.QueryRow(`SELECT version FROM cn35_gacha_pool_versions WHERE rules_digest=?`,
		digest).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := tx.Exec(`INSERT INTO cn35_gacha_pool_versions
				(rules_digest,snapshot,actor,note,created_at) VALUES (?,?,?,?,?)`,
			digest, string(snapRaw), actor, note, nowUTC())
		if err != nil {
			return nil, err
		}
		version, err = res.LastInsertId()
		if err != nil {
			return nil, err
		}
		for _, m := range flat {
			if _, err := tx.Exec(`INSERT INTO cn35_gacha_pool_version_members
					(version,pool_id,unit_id,weight,rare,resource_ok) VALUES (?,?,?,?,?,?)`,
				version, m.PoolID, m.UnitID, m.Weight, m.Rare, boolInt(m.ResourceOK)); err != nil {
				return nil, err
			}
		}
	} else if err != nil {
		return nil, err
	}
	// 生效指针同事务移动。
	if _, err := tx.Exec(`INSERT INTO cn35_gacha_release (release_id,version,actor,note,updated_at)
			VALUES (1,?,?,?,?)
			ON CONFLICT(release_id) DO UPDATE SET version=excluded.version,
			actor=excluded.actor,note=excluded.note,updated_at=excluded.updated_at`,
		version, actor, note, nowUTC()); err != nil {
		return nil, err
	}
	if err := appendAuditTx(tx, actor, "pool.publish", strconv.FormatInt(version, 10), map[string]any{
		"digest": digest, "note": note, "pools": len(pools),
	}); err != nil {
		return nil, err
	}
	return &cn35game.GachaConfigVersion{
		Version: version, Digest: digest, Snapshot: string(snapRaw),
		Actor: actor, Note: note, CreatedAt: nowUTC(),
	}, nil
}

// ActivateGachaVersion 回滚/切换到既有版本（只动指针+审计，不改版本行）。
func (s *Store) ActivateGachaVersion(version int64, actor, note string) error {
	return s.withTx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT count(*) FROM cn35_gacha_pool_versions WHERE version=?`,
			version).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return cn35game.ErrGachaVersionUnknown
		}
		if _, err := tx.Exec(`INSERT INTO cn35_gacha_release (release_id,version,actor,note,updated_at)
			VALUES (1,?,?,?,?)
			ON CONFLICT(release_id) DO UPDATE SET version=excluded.version,
			actor=excluded.actor,note=excluded.note,updated_at=excluded.updated_at`,
			version, actor, note, nowUTC()); err != nil {
			return err
		}
		return appendAuditTx(tx, actor, "pool.activate", strconv.FormatInt(version, 10),
			map[string]any{"note": note})
	})
}

// GachaVersions 列出版本历史与当前生效版本号。
func (s *Store) GachaVersions(limit int) ([]cn35game.GachaConfigVersion, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var active int64
	_ = s.db.QueryRow(`SELECT version FROM cn35_gacha_release WHERE release_id=1`).Scan(&active)
	rows, err := s.db.Query(`SELECT version,rules_digest,actor,note,created_at,length(snapshot)
		FROM cn35_gacha_pool_versions ORDER BY version DESC LIMIT ?`, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []cn35game.GachaConfigVersion
	var snapLen int
	for rows.Next() {
		var v cn35game.GachaConfigVersion
		if err := rows.Scan(&v.Version, &v.Digest, &v.Actor, &v.Note, &v.CreatedAt, &snapLen); err != nil {
			return nil, 0, err
		}
		v.Snapshot = strconv.Itoa(snapLen) + " bytes" // 列表不返回大快照体
		out = append(out, v)
	}
	return out, active, rows.Err()
}

// GachaMigrationMarkers 返回卡池版本史中出现过的 migrate-* 作者标记
// （DISTINCT 扫描无 LIMIT，版本数不受清单分页影响）。用于存量库一次性
// 重发判定：补发过就不再发，管理员回滚/旧版不被重启覆盖。
func (s *Store) GachaMigrationMarkers() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT DISTINCT actor FROM cn35_gacha_pool_versions
		WHERE actor LIKE 'migrate-%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var actor string
		if err := rows.Scan(&actor); err != nil {
			return nil, err
		}
		out[actor] = true
	}
	return out, rows.Err()
}

// GachaVersionMembers 读某版本成员明细。
func (s *Store) GachaVersionMembers(version int64) ([]cn35game.GachaVersionMember, error) {
	rows, err := s.db.Query(`SELECT pool_id,unit_id,weight,rare,resource_ok
		FROM cn35_gacha_pool_version_members WHERE version=? ORDER BY pool_id,unit_id`, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cn35game.GachaVersionMember
	for rows.Next() {
		var m cn35game.GachaVersionMember
		var ok int
		if err := rows.Scan(&m.PoolID, &m.UnitID, &m.Weight, &m.Rare, &ok); err != nil {
			return nil, err
		}
		m.ResourceOK = ok == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// AuditLog 最近审计记录。
func (s *Store) AuditLog(limit int) ([]cn35game.AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT audit_id,actor,action,target,detail,created_at
		FROM cn35_admin_audit ORDER BY audit_id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cn35game.AuditEntry
	for rows.Next() {
		var e cn35game.AuditEntry
		if err := rows.Scan(&e.AuditID, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (s *Store) gachaPoolsTx(tx *sql.Tx) ([]cn35game.GachaPoolDef, error) {
	rows, err := tx.Query(`SELECT pool_id,slot,list_type_id,code,name,description,enabled,
		price_point,price_coin,max_exec,max_bonus,limited,begin_at,end_at,
		featured_ids,distribution,pity,pity_group,notes
		FROM cn35_gacha_pools ORDER BY slot`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cn35game.GachaPoolDef
	for rows.Next() {
		p, err := scanPoolDef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanRosterUnitOK(tx *sql.Tx, unitID int) (cn35game.RosterUnit, bool, error) {
	r, err := scanRosterUnit(tx.QueryRow(
		`SELECT `+rosterCols+` FROM cn35_unit_roster WHERE unit_id=?`, unitID))
	if errors.Is(err, sql.ErrNoRows) {
		return cn35game.RosterUnit{}, false, nil
	}
	return r, err == nil, err
}

func boolInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func joinInts(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}
