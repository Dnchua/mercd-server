"""M7 候选设备验收入口（主线剧情/泉水随机徽章/契约书9折/33池特殊招募/信券）；check 不连接设备，未定稿计划拒绝运行。"""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import re

from .provenance import source_fingerprint
from .runner import BASE, Runner, steps_for, validate

REQUIRED_CASES = {f'M7{i:02d}' for i in range(1, 7)}

DEFAULT_M7_SQL_QUERIES = {
    'm7_01_snapshot_sql': (
        "SELECT COALESCE((SELECT COUNT(*) FROM cn35_accounts WHERE user_id = ?1), 0)"
    ),
    'm7_01_assert_sql': (
        "SELECT CASE WHEN ?2 = 1 AND EXISTS ("
        "SELECT 1 FROM cn35_accounts WHERE user_id = ?1"
        ") THEN 1 ELSE 0 END"
    ),
    'm7_02_snapshot_sql': (
        "SELECT COALESCE((SELECT amount FROM cn35_player_balances "
        "WHERE user_id = ?1 AND currency = 'medal'), 0)"
    ),
    'm7_02_assert_sql': (
        "SELECT CASE WHEN EXISTS ("
        "SELECT 1 FROM cn35_spring_claims WHERE user_id = ?1 AND medal BETWEEN 5 AND 100"
        ") AND COALESCE((SELECT amount FROM cn35_player_balances "
        "WHERE user_id = ?1 AND currency = 'medal'), 0) > ?2 THEN 1 ELSE 0 END"
    ),
    'm7_03_snapshot_sql': (
        "SELECT COALESCE((SELECT amount FROM cn35_player_balances "
        "WHERE user_id = ?1 AND currency = 'scout_book_1'), 0)"
    ),
    'm7_03_assert_sql': (
        "SELECT CASE WHEN COALESCE((SELECT amount FROM cn35_player_balances "
        "WHERE user_id = ?1 AND currency = 'scout_book_1'), 0) > ?2 THEN 1 ELSE 0 END"
    ),
    'm7_04_snapshot_sql': (
        "SELECT COALESCE((SELECT COUNT(*) FROM cn35_gacha_operations "
        "WHERE user_id = ?1 AND currency = 'scout_book_1' AND draw_count = 10 AND total_cost = 9), 0)"
    ),
    'm7_04_assert_sql': (
        "SELECT CASE WHEN COALESCE((SELECT COUNT(*) FROM cn35_gacha_operations "
        "WHERE user_id = ?1 AND currency = 'scout_book_1' AND draw_count = 10 AND total_cost = 9), 0) > ?2 "
        "AND EXISTS (SELECT 1 FROM cn35_gacha_draws d JOIN cn35_gacha_operations o ON o.operation_id = d.operation_id "
        "WHERE o.user_id = ?1 AND o.draw_count = 10 GROUP BY d.operation_id HAVING COUNT(*) = 10) "
        "THEN 1 ELSE 0 END"
    ),
    'm7_05_snapshot_sql': (
        "SELECT COALESCE((SELECT COUNT(*) FROM cn35_gacha_operations "
        "WHERE user_id = ?1 AND CAST(pool_id AS INTEGER) BETWEEN 101 AND 133), 0)"
    ),
    'm7_05_assert_sql': (
        "SELECT CASE WHEN COALESCE((SELECT COUNT(*) FROM cn35_gacha_operations "
        "WHERE user_id = ?1 AND CAST(pool_id AS INTEGER) BETWEEN 101 AND 133), 0) > ?2 "
        "AND EXISTS (SELECT 1 FROM cn35_gacha_pity WHERE user_id = ?1 AND pity_group = 'cn350-limited-up') "
        "THEN 1 ELSE 0 END"
    ),
    'm7_06_snapshot_sql': (
        "SELECT COALESCE((SELECT quantity FROM cn35_player_items "
        "WHERE user_id = ?1 AND item_key = 'gacha_ticket_11'), 0) * 1000000 "
        "+ COALESCE((SELECT COUNT(*) FROM cn35_player_units WHERE user_id = ?1), 0)"
    ),
    'm7_06_assert_sql': (
        "SELECT CASE WHEN "
        "COALESCE((SELECT quantity FROM cn35_player_items WHERE user_id = ?1 AND item_key = 'gacha_ticket_11'), 0) <= (?2 / 1000000) - 5 "
        "AND EXISTS (SELECT 1 FROM cn35_player_items WHERE user_id = ?1 AND item_key = 'scout_book_4star' AND quantity >= 1) "
        "THEN 1 ELSE 0 END"
    ),
}


def resolve_sql_variables(variables=None):
    merged = dict(DEFAULT_M7_SQL_QUERIES)
    if variables:
        merged.update(variables)
    return merged


def check_plan(plan, selectors):
    validate(plan, selectors)
    missing = REQUIRED_CASES - {c['id'] for c in plan['cases']}
    if missing:
        raise ValueError('M7 必需用例缺失: ' + ', '.join(sorted(missing)))
    if plan.get('module') != 'M7' or plan.get('single_actor') is not True:
        raise ValueError('必须使用 M7 单账号计划')
    bindings = set()
    for case in plan['cases']:
        for step in steps_for(plan, case['steps']):
            if step['op'] in {'capture_sql', 'assert_sql'}:
                match = re.fullmatch(r'\$\{(m7_\w+_sql)\}', step['sql'])
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
            match = re.fullmatch(r'\$\{(m7_\w+_sql)\}', step['sql'])
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
        raise ValueError('M7 仍是候选计划；先完成控件校准并确认门禁后方可运行')
    gate = json.loads(Path(cfg['server_result']).read_text())
    if not (gate.get('passed') is True and gate.get('scope') == 'protocol-store'
            and gate.get('module') == 'M7'
            and gate.get('source_fingerprint') == source_fingerprint()):
        raise ValueError('需要当前源指纹的 M7 服务端门禁')
    required = gate.get('required_tests')
    passed = gate.get('passed_tests', [])
    if not isinstance(required, list) or not required or not set(required).issubset(passed):
        raise ValueError('M7 强制测试清单为空或有未执行/未通过项')
    if gate.get('missing_required_tests') != [] or gate.get('unresolved_fields') != []:
        raise ValueError('M7 仍有未闭合测试或字段，不能执行实机验收')
    coverage = gate.get('field_coverage', {})
    total = coverage.get('total', 0)
    if not isinstance(total, int) or total <= 0 or coverage.get('verified') != total:
        raise ValueError('M7 字段覆盖清单为空或尚未全量闭合')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['check', 'run'])
    parser.add_argument('--plan', default=str(BASE / 'm7_smoke_plan.json'))
    parser.add_argument('--config')
    parser.add_argument('--record', action='store_true')
    args = parser.parse_args(argv)
    plan = json.loads(Path(args.plan).read_text())
    if args.command == 'check':
        selectors = json.loads((BASE / 'selectors.candidate.json').read_text())
        selectors.update(json.loads((BASE / 'm7_smoke_selectors.candidate.json').read_text()))
        bindings = check_plan(plan, selectors)
        print(json.dumps({'status': 'static-valid', 'module': 'M7',
                          'cases': len(plan['cases']), 'device_verified': False,
                          'implementation_ready': plan.get('implementation_ready', False),
                          'required_sql_bindings': bindings,
                          'next': '待下一轮实机测试使用 inspect/calibrate 校准 M7 控件并产出 M7 服务端门禁后方可 run'},
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
    report = runner.output / 'm7-result.json'
    report.write_text(json.dumps({
        'module': 'M7', 'passed': bool(passed), 'device_validation_attempted': True,
        'device_verified': bool(passed),
        'source_fingerprint': before, 'runner_result': 'result.json',
        'scope': 'declared-m7-plan', 'note': 'M7 实机验收须联合 M7 服务端门禁与校准树审阅',
    }, ensure_ascii=False, indent=2) + '\n')
    manifest_path = runner.output / 'manifest.json'
    manifest = json.loads(manifest_path.read_text())
    manifest.append({'path': report.name, 'sha256': hashlib.sha256(report.read_bytes()).hexdigest()})
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
