"""M6 候选设备验收入口（公会设施战斗加成 + 暴走魔宠 Raid）；check 不连接设备，未定稿计划拒绝运行。"""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import re

from .provenance import source_fingerprint
from .runner import BASE, Runner, steps_for, validate

REQUIRED_CASES = {f'M6{i:02d}' for i in range(1, 11)}

DEFAULT_M6_SQL_QUERIES = {
    'm6_01_snapshot_sql': (
        "SELECT COALESCE((SELECT GROUP_CONCAT(type_id || ':' || level, ',') "
        "FROM (SELECT e.type_id, e.level FROM cn35_guild_equipment e "
        "JOIN cn35_guild_members m ON m.guild_id = e.guild_id "
        "WHERE m.user_id = ?1 ORDER BY e.type_id)), '')"
    ),
    'm6_01_assert_sql': (
        "SELECT CASE WHEN EXISTS ("
        "SELECT 1 FROM cn35_guild_members m "
        "JOIN cn35_guild_equipment e ON e.guild_id = m.guild_id "
        "WHERE m.user_id = ?1 AND e.level > 0"
        ") AND COALESCE((SELECT GROUP_CONCAT(type_id || ':' || level, ',') "
        "FROM (SELECT e.type_id, e.level FROM cn35_guild_equipment e "
        "JOIN cn35_guild_members m ON m.guild_id = e.guild_id "
        "WHERE m.user_id = ?1 ORDER BY e.type_id)), '') = ?2 "
        "THEN 1 ELSE 0 END"
    ),
    'm6_02_snapshot_sql': (
        "SELECT COALESCE((SELECT COUNT(*) FROM cn35_battle_sessions WHERE user_id = ?1), 0)"
    ),
    'm6_02_assert_sql': (
        "SELECT CASE WHEN (SELECT COUNT(*) FROM cn35_battle_sessions WHERE user_id = ?1) > ?2 "
        "AND EXISTS (SELECT 1 FROM cn35_player_profiles WHERE user_id = ?1) "
        "THEN 1 ELSE 0 END"
    ),
    'm6_03_snapshot_sql': (
        "SELECT COALESCE((SELECT discovered_count FROM cn35_raid_profiles WHERE user_id = ?1), 0)"
    ),
    'm6_03_assert_sql': (
        "SELECT CASE WHEN EXISTS ("
        "SELECT 1 FROM cn35_raid_instances WHERE owner_user_id = ?1 AND status = 'active'"
        ") AND COALESCE((SELECT discovered_count FROM cn35_raid_profiles WHERE user_id = ?1), 0) >= CASE WHEN ?2 = 0 THEN 1 ELSE ?2 END "
        "THEN 1 ELSE 0 END"
    ),
    'm6_04_snapshot_sql': (
        "SELECT COALESCE((SELECT raid_lv || ':' || raid_pt FROM cn35_raid_profiles WHERE user_id = ?1), '1:0')"
    ),
    'm6_04_assert_sql': (
        "SELECT CASE WHEN EXISTS ("
        "SELECT 1 FROM cn35_raid_instances WHERE owner_user_id = ?1 AND status = 'active' AND attacked = 0"
        ") AND COALESCE((SELECT raid_lv || ':' || raid_pt FROM cn35_raid_profiles WHERE user_id = ?1), '1:0') = ?2 "
        "THEN 1 ELSE 0 END"
    ),
    'm6_05_snapshot_sql': (
        "SELECT COALESCE((SELECT current_point FROM cn35_raid_instances "
        "WHERE owner_user_id = ?1 AND status = 'active' ORDER BY spawned_at DESC LIMIT 1), 0)"
    ),
    'm6_05_assert_sql': (
        "SELECT CASE WHEN ?2 > 0 AND COALESCE((SELECT current_point FROM cn35_raid_instances "
        "WHERE owner_user_id = ?1 AND status = 'active' ORDER BY spawned_at DESC LIMIT 1), 0) = ?2 "
        "THEN 1 ELSE 0 END"
    ),
    'm6_06_snapshot_sql': (
        "SELECT COALESCE((SELECT current_point FROM cn35_raid_instances "
        "WHERE owner_user_id = ?1 AND status = 'active' ORDER BY spawned_at DESC LIMIT 1), 0)"
    ),
    'm6_06_assert_sql': (
        "SELECT CASE WHEN EXISTS ("
        "SELECT 1 FROM cn35_raid_instances WHERE owner_user_id = ?1 AND attacked = 1 AND current_point < ?2"
        ") AND EXISTS ("
        "SELECT 1 FROM cn35_raid_sessions WHERE user_id = ?1 AND status = 'lost'"
        ") AND EXISTS ("
        "SELECT 1 FROM cn35_raid_participants WHERE user_id = ?1 AND hit_count >= 1"
        ") THEN 1 ELSE 0 END"
    ),
    'm6_07_snapshot_sql': (
        "SELECT COALESCE((SELECT help_guild + help_community FROM cn35_raid_instances "
        "WHERE owner_user_id = ?1 AND status = 'active' ORDER BY spawned_at DESC LIMIT 1), 0)"
    ),
    'm6_07_assert_sql': (
        "SELECT CASE WHEN EXISTS ("
        "SELECT 1 FROM cn35_raid_instances "
        "WHERE owner_user_id = ?1 AND status = 'active' AND help_guild = 1 AND help_community = 1 "
        "AND (help_guild + help_community) > ?2"
        ") THEN 1 ELSE 0 END"
    ),
    'm6_08_snapshot_sql': (
        "SELECT COALESCE((SELECT raid_lv FROM cn35_raid_profiles WHERE user_id = ?1), 1)"
    ),
    'm6_08_assert_sql': (
        "SELECT CASE WHEN EXISTS ("
        "SELECT 1 FROM cn35_raid_profiles WHERE user_id = ?1 AND raid_lv > ?2 AND killed_count >= 1 AND raid_pt > 0"
        ") AND EXISTS ("
        "SELECT 1 FROM cn35_raid_instances WHERE owner_user_id = ?1 AND status = 'defeated'"
        ") AND EXISTS ("
        "SELECT 1 FROM cn35_raid_lv_claims WHERE user_id = ?1"
        ") THEN 1 ELSE 0 END"
    ),
    'm6_09_snapshot_sql': (
        "SELECT COALESCE((SELECT COUNT(*) FROM cn35_raid_instances WHERE owner_user_id = ?1 AND status = 'giveup'), 0) * 1000000 "
        "+ COALESCE((SELECT SUM(exchanged_count) FROM cn35_raid_pt_exchanges WHERE user_id = ?1), 0)"
    ),
    'm6_09_assert_sql': (
        "SELECT CASE WHEN "
        "COALESCE((SELECT COUNT(*) FROM cn35_raid_instances WHERE owner_user_id = ?1 AND status = 'giveup'), 0) > (?2 / 1000000) "
        "AND COALESCE((SELECT SUM(exchanged_count) FROM cn35_raid_pt_exchanges WHERE user_id = ?1), 0) > (?2 % 1000000) "
        "THEN 1 ELSE 0 END"
    ),
    'm6_10_snapshot_sql': (
        "SELECT COALESCE((SELECT raid_lv || ':' || raid_pt || ':' || killed_count "
        "FROM cn35_raid_profiles WHERE user_id = ?1), '') || '|' || "
        "COALESCE((SELECT SUM(exchanged_count) FROM cn35_raid_pt_exchanges WHERE user_id = ?1), 0)"
    ),
    'm6_10_assert_sql': (
        "SELECT CASE WHEN ?2 <> '' AND ("
        "COALESCE((SELECT raid_lv || ':' || raid_pt || ':' || killed_count "
        "FROM cn35_raid_profiles WHERE user_id = ?1), '') || '|' || "
        "COALESCE((SELECT SUM(exchanged_count) FROM cn35_raid_pt_exchanges WHERE user_id = ?1), 0)"
        ") = ?2 THEN 1 ELSE 0 END"
    ),
}


def resolve_sql_variables(variables=None):
    merged = dict(DEFAULT_M6_SQL_QUERIES)
    if variables:
        merged.update(variables)
    return merged


def check_plan(plan, selectors):
    validate(plan, selectors)
    missing = REQUIRED_CASES - {c['id'] for c in plan['cases']}
    if missing:
        raise ValueError('M6 必需用例缺失: ' + ', '.join(sorted(missing)))
    if plan.get('module') != 'M6' or plan.get('single_actor') is not True:
        raise ValueError('必须使用 M6 单账号计划')
    bindings = set()
    for case in plan['cases']:
        for step in steps_for(plan, case['steps']):
            if step['op'] in {'capture_sql', 'assert_sql'}:
                match = re.fullmatch(r'\$\{(m6_\w+_sql)\}', step['sql'])
                if match:
                    bindings.add(match[1])
    return sorted(bindings)


def bind_sql(plan, variables):
    """未来 schema 的查询整段绑定；数据值仍由 params 参数化，禁止插值到 SQL。"""
    plan = copy.deepcopy(plan)
    for case in plan['cases']:
        case['steps'] = steps_for(plan, case['steps'])
        for step in case['steps']:
            if step['op'] not in {'capture_sql', 'assert_sql'}:
                continue
            match = re.fullmatch(r'\$\{(m6_\w+_sql)\}', step['sql'])
            if match:
                step['sql'] = variables.get(match[1], '')
            sql = step['sql']
            if not isinstance(sql, str) or not sql.lstrip().upper().startswith(('SELECT ', 'WITH ')):
                raise ValueError('缺少只读 SQL 绑定: ' + str(match.group(1) if match else sql))
            if '${' in sql or 'cn35_' not in sql.lower():
                raise ValueError('SQL 必须查询真实 CN3.5 状态，不能含未绑定变量或常量空断言')
    return plan


def require_gate(plan, cfg):
    if plan.get('implementation_ready') is not True:
        raise ValueError('M6 仍是候选计划；先完成服务端开发、控件校准并确认门禁后方可运行')
    gate = json.loads(Path(cfg['server_result']).read_text())
    if not (gate.get('passed') is True and gate.get('scope') == 'protocol-store'
            and gate.get('module') == 'M6'
            and gate.get('source_fingerprint') == source_fingerprint()):
        raise ValueError('需要当前源指纹的 M6 服务端门禁；公会或 M5 门禁不能替代')
    required = gate.get('required_tests')
    passed = gate.get('passed_tests', [])
    if not isinstance(required, list) or not required or not set(required).issubset(passed):
        raise ValueError('M6 强制测试清单为空或有未执行/未通过项')
    if gate.get('missing_required_tests') != [] or gate.get('unresolved_fields') != []:
        raise ValueError('M6 仍有未闭合测试或字段，不能执行最终验收')
    coverage = gate.get('field_coverage', {})
    total = coverage.get('total', 0)
    if not isinstance(total, int) or total <= 0 or coverage.get('verified') != total:
        raise ValueError('M6 字段覆盖清单为空或尚未全量闭合')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['check', 'run'])
    parser.add_argument('--plan', default=str(BASE / 'm6_raid_plan.json'))
    parser.add_argument('--config')
    parser.add_argument('--record', action='store_true')
    args = parser.parse_args(argv)
    plan = json.loads(Path(args.plan).read_text())
    if args.command == 'check':
        selectors = json.loads((BASE / 'selectors.candidate.json').read_text())
        selectors.update(json.loads((BASE / 'm6_raid_selectors.candidate.json').read_text()))
        bindings = check_plan(plan, selectors)
        print(json.dumps({'status': 'static-valid', 'module': 'M6',
                          'cases': len(plan['cases']), 'device_verified': False,
                          'implementation_ready': plan.get('implementation_ready', False),
                          'required_sql_bindings': bindings,
                          'next': '按 plans/cn35-m6-raid-2026-11-13/PLAN.md 完成审核与开发、inspect/calibrate 校准控件并生成 M6 服务端门禁后再 run'},
                         ensure_ascii=False, indent=2))
        return 0
    if not args.config:
        parser.error('run 必须指定 --config；仅支持专用测试副本')
    cfg = json.loads(Path(args.config).read_text())
    require_gate(plan, cfg)
    selectors = json.loads(Path(cfg['selectors']).read_text())
    check_plan(plan, selectors)
    bound = bind_sql(plan, resolve_sql_variables(cfg.get('variables')))
    if args.record:
        cfg.setdefault('record', {})['enabled'] = True
    before = source_fingerprint()
    runner = Runner(cfg, bound, selectors)
    code = runner.run()
    passed = (code == 0 and before == source_fingerprint()
              and len(runner.results) == len(bound['cases'])
              and all(r['status'] == 'passed' for r in runner.results))
    if cfg.get('record', {}).get('enabled'):
        passed = passed and all(r.get('video') and not r['video'].get('error')
                                and r['video'].get('video') for r in runner.results)
    report = runner.output / 'm6-result.json'
    report.write_text(json.dumps({
        'module': 'M6', 'passed': bool(passed), 'device_validation_attempted': True,
        'device_verified': bool(passed),
        'source_fingerprint': before, 'runner_result': 'result.json',
        'scope': 'declared-m6-plan', 'note': '公会设施战斗加成与暴走魔宠全链路验收须联合 M6 服务端门禁审阅',
    }, ensure_ascii=False, indent=2) + '\n')
    manifest_path = runner.output / 'manifest.json'
    manifest = json.loads(manifest_path.read_text())
    manifest.append({'path': report.name, 'sha256': hashlib.sha256(report.read_bytes()).hexdigest()})
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())

