"""M10 候选设备验收入口（癒术士大赛与公会训练）；check 不连接设备，未定稿计划拒绝运行。"""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import re

from .provenance import source_fingerprint
from .runner import BASE, Runner, steps_for, validate

REQUIRED_CASES = {f'M10{i:02d}' for i in range(1, 13)}

DEFAULT_M10_SQL_QUERIES = {
    'm10_01_assert_sql': 'SELECT CASE WHEN (SELECT COUNT(*) FROM cn35_ladder_profiles WHERE user_id = ?1) >= ?2 THEN 1 ELSE 0 END',
    'm10_01_snapshot_sql': 'SELECT COUNT(*) FROM cn35_ladder_profiles WHERE user_id = ?1',
    'm10_02_assert_sql': 'SELECT CASE WHEN COALESCE((SELECT score FROM cn35_ladder_profiles WHERE user_id = ?1), 0) = ?2 THEN 1 ELSE 0 END',
    'm10_02_snapshot_sql': 'SELECT COALESCE((SELECT score FROM cn35_ladder_profiles WHERE user_id = ?1), 0)',
    'm10_03_assert_sql': 'SELECT CASE WHEN (SELECT COUNT(*) FROM cn35_ladder_decks WHERE user_id = ?1) >= 1 AND (SELECT COUNT(*) FROM cn35_ladder_decks WHERE user_id = ?1) >= ?2 THEN 1 ELSE 0 END',
    'm10_03_snapshot_sql': 'SELECT COUNT(*) FROM cn35_ladder_decks WHERE user_id = ?1',
    'm10_04_assert_sql': 'SELECT CASE WHEN COALESCE((SELECT day_used FROM cn35_ladder_profiles WHERE user_id = ?1), 0) = ?2 THEN 1 ELSE 0 END',
    'm10_04_snapshot_sql': 'SELECT COALESCE((SELECT day_used FROM cn35_ladder_profiles WHERE user_id = ?1), 0)',
    'm10_05_assert_sql': "SELECT CASE WHEN COALESCE((SELECT score FROM cn35_ladder_profiles WHERE user_id = ?1), 0) > ?2 / 1000 AND COALESCE((SELECT day_used FROM cn35_ladder_profiles WHERE user_id = ?1), 0) = (?2 % 1000) + 1 AND (SELECT status FROM cn35_ladder_sessions WHERE user_id = ?1 ORDER BY created_at DESC LIMIT 1) = 'won' THEN 1 ELSE 0 END",
    'm10_05_snapshot_sql': 'SELECT COALESCE((SELECT score FROM cn35_ladder_profiles WHERE user_id = ?1), 0) * 1000 + COALESCE((SELECT day_used FROM cn35_ladder_profiles WHERE user_id = ?1), 0)',
    'm10_06_assert_sql': 'SELECT CASE WHEN COALESCE((SELECT day_used FROM cn35_ladder_profiles WHERE user_id = ?1), 0) <= 3 AND COALESCE((SELECT day_used FROM cn35_ladder_profiles WHERE user_id = ?1), 0) = ?2 THEN 1 ELSE 0 END',
    'm10_06_snapshot_sql': 'SELECT COALESCE((SELECT day_used FROM cn35_ladder_profiles WHERE user_id = ?1), 0)',
    'm10_07_assert_sql': 'SELECT CASE WHEN COALESCE((SELECT potion_1 FROM cn35_ladder_profiles WHERE user_id = ?1), 0) = ?2 AND COALESCE((SELECT SUM(steps) FROM cn35_ladder_grow WHERE user_id = ?1 AND grow_type = 1), 0) = 0 THEN 1 ELSE 0 END',
    'm10_07_snapshot_sql': 'SELECT COALESCE((SELECT potion_1 FROM cn35_ladder_profiles WHERE user_id = ?1), 0)',
    'm10_08_assert_sql': 'SELECT CASE WHEN (SELECT COUNT(*) FROM cn35_ladder_settlements WHERE user_id = ?1 AND claimed = 1) = ?2 THEN 1 ELSE 0 END',
    'm10_08_snapshot_sql': 'SELECT COUNT(*) FROM cn35_ladder_settlements WHERE user_id = ?1 AND claimed = 1',
    'm10_09_assert_sql': 'SELECT CASE WHEN COALESCE((SELECT tickets_bought FROM cn35_rob_profiles WHERE user_id = ?1), 0) = ?2 + 1 AND COALESCE((SELECT rc_left FROM cn35_rob_profiles WHERE user_id = ?1), 0) = 5 THEN 1 ELSE 0 END',
    'm10_09_snapshot_sql': 'SELECT COALESCE((SELECT tickets_bought FROM cn35_rob_profiles WHERE user_id = ?1), 0)',
    'm10_10_assert_sql': "SELECT CASE WHEN (SELECT COUNT(*) FROM cn35_rob_decks WHERE user_id = ?1 AND party_type = 'atk') = 10 AND (SELECT COUNT(*) FROM cn35_rob_decks WHERE user_id = ?1 AND party_type = 'def') = 10 AND ?2 >= 0 THEN 1 ELSE 0 END",
    'm10_10_snapshot_sql': 'SELECT COUNT(*) FROM cn35_rob_decks WHERE user_id = ?1',
    'm10_11_assert_sql': 'SELECT CASE WHEN COALESCE((SELECT rc_left FROM cn35_rob_profiles WHERE user_id = ?1), 0) = ?2 / 100000 - 1 AND COALESCE((SELECT score FROM cn35_rob_profiles WHERE user_id = ?1), 0) >= ?2 % 100000 THEN 1 ELSE 0 END',
    'm10_11_snapshot_sql': 'SELECT COALESCE((SELECT rc_left FROM cn35_rob_profiles WHERE user_id = ?1), 0) * 100000 + COALESCE((SELECT score FROM cn35_rob_profiles WHERE user_id = ?1), 0)',
    'm10_12_assert_sql': 'SELECT CASE WHEN (COALESCE((SELECT score FROM cn35_ladder_profiles WHERE user_id = ?1), 0) * 1000000 + COALESCE((SELECT day_used FROM cn35_ladder_profiles WHERE user_id = ?1), 0) * 100000 + COALESCE((SELECT score FROM cn35_rob_profiles WHERE user_id = ?1), 0) * 10 + COALESCE((SELECT COUNT(*) FROM cn35_ladder_decks WHERE user_id = ?1), 0)) = ?2 THEN 1 ELSE 0 END',
    'm10_12_snapshot_sql': 'SELECT COALESCE((SELECT score FROM cn35_ladder_profiles WHERE user_id = ?1), 0) * 1000000 + COALESCE((SELECT day_used FROM cn35_ladder_profiles WHERE user_id = ?1), 0) * 100000 + COALESCE((SELECT score FROM cn35_rob_profiles WHERE user_id = ?1), 0) * 10 + COALESCE((SELECT COUNT(*) FROM cn35_ladder_decks WHERE user_id = ?1), 0)',
}

M10_REQUIRED_TESTS = [
    'TestM10EntryAndConsts', 'TestM10LadderFlow', 'TestM10RobFlow', 'TestM10SettleConcurrent',
    'TestM10CaptureReadFaceReplay', 'TestPvPMigration0057TablesExist', 'TestPvPLadderWeekAndSeasonRollover',
    'TestPvPRobTicketOrderAndGold',
]


def resolve_sql_variables(variables=None):
    merged = dict(DEFAULT_M10_SQL_QUERIES)
    if variables:
        merged.update(variables)
    return merged


def check_plan(plan, selectors):
    validate(plan, selectors)
    missing = REQUIRED_CASES - {c['id'] for c in plan['cases']}
    if missing:
        raise ValueError('M10 必需用例缺失: ' + ', '.join(sorted(missing)))
    if plan.get('module') != 'M10' or plan.get('single_actor') is not True:
        raise ValueError('必须使用 M10 单账号计划')
    bindings = set()
    for case in plan['cases']:
        for step in steps_for(plan, case['steps']):
            if step['op'] in {'capture_sql', 'assert_sql'}:
                match = re.fullmatch(r'\$\{(m10_\w+_sql)\}', step['sql'])
                if match:
                    bindings.add(match[1])
    return sorted(bindings)


def bind_sql(plan, variables):
    """查询整段绑定；数据值由 params 参数化，禁止字符串插值。"""
    plan = copy.deepcopy(plan)
    for case in plan['cases']:
        case['steps'] = steps_for(plan, case['steps'])
        for step in case['steps']:
            if step['op'] not in {'capture_sql', 'assert_sql'}:
                continue
            match = re.fullmatch(r'\$\{(m10_\w+_sql)\}', step['sql'])
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
        raise ValueError('M10 仍是候选计划；先完成控件校准并确认门禁后方可运行')
    gate = json.loads(Path(cfg['server_result']).read_text())
    if not (gate.get('passed') is True and gate.get('scope') == 'protocol-store'
            and gate.get('module') == 'M10'
            and gate.get('source_fingerprint') == source_fingerprint()):
        raise ValueError('需要当前源指纹的 M10 服务端门禁')
    required = gate.get('required_tests')
    passed = gate.get('passed_tests', [])
    if not isinstance(required, list) or not set(M10_REQUIRED_TESTS).issubset(required) or not set(required).issubset(passed):
        raise ValueError('M10 强制测试清单不完整或有未执行/未通过项')
    if gate.get('missing_required_tests') != [] or gate.get('unresolved_fields') != []:
        raise ValueError('M10 仍有未闭合测试或字段，不能执行实机验收')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['check', 'run'])
    parser.add_argument('--plan', default=str(BASE / 'm10_pvp_plan.json'))
    parser.add_argument('--config')
    parser.add_argument('--record', action='store_true')
    args = parser.parse_args(argv)
    plan = json.loads(Path(args.plan).read_text())
    if args.command == 'check':
        selectors = json.loads((BASE / 'selectors.candidate.json').read_text())
        selectors.update(json.loads((BASE / 'm10_pvp_selectors.candidate.json').read_text()))
        bindings = check_plan(plan, selectors)
        print(json.dumps({'status': 'static-valid', 'module': 'M10',
                          'cases': len(plan['cases']), 'device_verified': False,
                          'implementation_ready': plan.get('implementation_ready', False),
                          'required_sql_bindings': bindings,
                          'required_tests': M10_REQUIRED_TESTS,
                          'next': '实机阶段：inspect/calibrate 校准 M10 控件，产出 M10 服务端门禁后方可 run'},
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
    report = runner.output / 'm10-result.json'
    report.write_text(json.dumps({
        'module': 'M10', 'passed': bool(passed), 'device_validation_attempted': True,
        'device_verified': bool(passed), 'source_fingerprint': before,
        'runner_result': 'result.json', 'scope': 'declared-m10-plan',
        'note': 'M10 实机验收须联合 M10 服务端门禁与校准树审阅',
    }, ensure_ascii=False, indent=2) + '\n')
    manifest_path = runner.output / 'manifest.json'
    manifest = json.loads(manifest_path.read_text())
    manifest.append({'path': report.name, 'sha256': hashlib.sha256(report.read_bytes()).hexdigest()})
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
