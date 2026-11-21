"""M8 候选设备验收入口（限时活动与周常探险）；check 不连接设备，未定稿计划拒绝运行。"""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import re

from .provenance import source_fingerprint
from .runner import BASE, Runner, steps_for, validate

REQUIRED_CASES = {f'M8{i:02d}' for i in range(1, 13)}

_CLEARS = "COALESCE((SELECT SUM(clear_count) FROM cn35_quest_clears WHERE user_id = ?1 AND quest_id {}), 0)"
_EVT_PRESENTS = "COALESCE((SELECT COUNT(*) FROM cn35_user_presents WHERE user_id = ?1 AND uuid LIKE 'evt-%'), 0)"

DEFAULT_M8_SQL_QUERIES = {
    # M801/M802：页签开放与列表为读面，DB 侧断言账号与主线指针未被改写。
    'm8_01_snapshot_sql': "SELECT COALESCE((SELECT quest_id FROM cn35_quest_progress WHERE user_id = ?1), 0)",
    'm8_01_assert_sql': "SELECT CASE WHEN COALESCE((SELECT quest_id FROM cn35_quest_progress WHERE user_id = ?1), 0) = ?2 THEN 1 ELSE 0 END",
    'm8_02_snapshot_sql': "SELECT COALESCE((SELECT quest_id FROM cn35_quest_progress WHERE user_id = ?1), 0)",
    'm8_02_assert_sql': "SELECT CASE WHEN COALESCE((SELECT quest_id FROM cn35_quest_progress WHERE user_id = ?1), 0) = ?2 THEN 1 ELSE 0 END",
    # M803：进入历史活动空区域不扣徽章。
    'm8_03_snapshot_sql': "SELECT COALESCE((SELECT amount FROM cn35_player_balances WHERE user_id = ?1 AND currency = 'medal'), 0)",
    'm8_03_assert_sql': "SELECT CASE WHEN COALESCE((SELECT amount FROM cn35_player_balances WHERE user_id = ?1 AND currency = 'medal'), 0) = ?2 THEN 1 ELSE 0 END",
    # M804：读面不产生活动通关记录。
    'm8_04_snapshot_sql': "SELECT " + _CLEARS.format("BETWEEN 1440 AND 1451"),
    'm8_04_assert_sql': "SELECT CASE WHEN " + _CLEARS.format("BETWEEN 1440 AND 1451") + " = ?2 THEN 1 ELSE 0 END",
    # M805：1440 普通通关 +1。
    'm8_05_snapshot_sql': "SELECT COALESCE((SELECT clear_count FROM cn35_quest_clears WHERE user_id = ?1 AND quest_id = 1440 AND difficulty = 'normal'), 0)",
    'm8_05_assert_sql': "SELECT CASE WHEN COALESCE((SELECT clear_count FROM cn35_quest_clears WHERE user_id = ?1 AND quest_id = 1440 AND difficulty = 'normal'), 0) > ?2 THEN 1 ELSE 0 END",
    # M806：周常读面不写库。
    'm8_06_snapshot_sql': "SELECT " + _CLEARS.format("BETWEEN 80001 AND 80064"),
    'm8_06_assert_sql': "SELECT CASE WHEN " + _CLEARS.format("BETWEEN 80001 AND 80064") + " = ?2 THEN 1 ELSE 0 END",
    # M807：80064 clear_count +1 且金币增加 ≥ 9985。
    'm8_07_snapshot_sql': (
        "SELECT COALESCE((SELECT clear_count FROM cn35_quest_clears WHERE user_id = ?1 AND quest_id = 80064), 0) * 1000000000"
        " + COALESCE((SELECT amount FROM cn35_player_balances WHERE user_id = ?1 AND currency = 'gacha_point'), 0)"
    ),
    'm8_07_assert_sql': (
        "SELECT CASE WHEN COALESCE((SELECT clear_count FROM cn35_quest_clears WHERE user_id = ?1 AND quest_id = 80064), 0) > ?2 / 1000000000"
        " AND COALESCE((SELECT amount FROM cn35_player_balances WHERE user_id = ?1 AND currency = 'gacha_point'), 0) >= (?2 % 1000000000) + 9985"
        " THEN 1 ELSE 0 END"
    ),
    # M808：素材日任一进化素材数量增加。
    'm8_08_snapshot_sql': "SELECT COALESCE((SELECT SUM(quantity) FROM cn35_player_items WHERE user_id = ?1 AND item_key LIKE 'powerup_item_%'), 0)",
    'm8_08_assert_sql': "SELECT CASE WHEN COALESCE((SELECT SUM(quantity) FROM cn35_player_items WHERE user_id = ?1 AND item_key LIKE 'powerup_item_%'), 0) > ?2 THEN 1 ELSE 0 END",
    # M809：完成率台账 4176/30 存在、礼物盒增加且同伴 1274 在列。
    'm8_09_snapshot_sql': "SELECT " + _EVT_PRESENTS,
    'm8_09_assert_sql': (
        "SELECT CASE WHEN EXISTS (SELECT 1 FROM cn35_event_area_rewards WHERE user_id = ?1 AND area_id = 4176 AND threshold = 30)"
        " AND " + _EVT_PRESENTS + " > ?2"
        " AND EXISTS (SELECT 1 FROM cn35_user_presents WHERE user_id = ?1 AND content_type = 'unit' AND content_id = 1274)"
        " THEN 1 ELSE 0 END"
    ),
    # M810：Boss 关 1443 通关记录增加。
    'm8_10_snapshot_sql': "SELECT " + _CLEARS.format("= 1443"),
    'm8_10_assert_sql': "SELECT CASE WHEN " + _CLEARS.format("= 1443") + " > ?2 THEN 1 ELSE 0 END",
    # M811：窗口关闭后活动通关记录不变（直接开战被拒绝不写库）。
    'm8_11_snapshot_sql': "SELECT " + _CLEARS.format("BETWEEN 1440 AND 1451"),
    'm8_11_assert_sql': "SELECT CASE WHEN " + _CLEARS.format("BETWEEN 1440 AND 1451") + " = ?2 THEN 1 ELSE 0 END",
    # M812：重启前后活动/周常通关、台账、礼物总量一致。
    'm8_12_snapshot_sql': (
        "SELECT " + _CLEARS.format("BETWEEN 1440 AND 80064") + " * 1000000"
        " + COALESCE((SELECT COUNT(*) FROM cn35_event_area_rewards WHERE user_id = ?1), 0) * 1000"
        " + " + _EVT_PRESENTS
    ),
    'm8_12_assert_sql': (
        "SELECT CASE WHEN " + _CLEARS.format("BETWEEN 1440 AND 80064") + " * 1000000"
        " + COALESCE((SELECT COUNT(*) FROM cn35_event_area_rewards WHERE user_id = ?1), 0) * 1000"
        " + " + _EVT_PRESENTS + " = ?2 THEN 1 ELSE 0 END"
    ),
}

M8_REQUIRED_TESTS = [
    'TestM8ParseEventWindows', 'TestM8EventsListAndConsts', 'TestM8AreasEventReadFace',
    'TestM8EventBattleProgressAndAreaRewards', 'TestM8DailyQuests', 'TestM8CaptureReadFaceReplay',
    'TestEventAreaRewardsIdempotentConcurrentIsolated',
]


def resolve_sql_variables(variables=None):
    merged = dict(DEFAULT_M8_SQL_QUERIES)
    if variables:
        merged.update(variables)
    return merged


def check_plan(plan, selectors):
    validate(plan, selectors)
    missing = REQUIRED_CASES - {c['id'] for c in plan['cases']}
    if missing:
        raise ValueError('M8 必需用例缺失: ' + ', '.join(sorted(missing)))
    if plan.get('module') != 'M8' or plan.get('single_actor') is not True:
        raise ValueError('必须使用 M8 单账号计划')
    bindings = set()
    for case in plan['cases']:
        for step in steps_for(plan, case['steps']):
            if step['op'] in {'capture_sql', 'assert_sql'}:
                match = re.fullmatch(r'\$\{(m8_\w+_sql)\}', step['sql'])
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
            match = re.fullmatch(r'\$\{(m8_\w+_sql)\}', step['sql'])
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
        raise ValueError('M8 仍是候选计划；先完成控件校准并确认门禁后方可运行')
    gate = json.loads(Path(cfg['server_result']).read_text())
    if not (gate.get('passed') is True and gate.get('scope') == 'protocol-store'
            and gate.get('module') == 'M8'
            and gate.get('source_fingerprint') == source_fingerprint()):
        raise ValueError('需要当前源指纹的 M8 服务端门禁')
    required = gate.get('required_tests')
    passed = gate.get('passed_tests', [])
    if not isinstance(required, list) or not set(M8_REQUIRED_TESTS).issubset(required) or not set(required).issubset(passed):
        raise ValueError('M8 强制测试清单不完整或有未执行/未通过项')
    if gate.get('missing_required_tests') != [] or gate.get('unresolved_fields') != []:
        raise ValueError('M8 仍有未闭合测试或字段，不能执行实机验收')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['check', 'run'])
    parser.add_argument('--plan', default=str(BASE / 'm8_events_daily_plan.json'))
    parser.add_argument('--config')
    parser.add_argument('--record', action='store_true')
    args = parser.parse_args(argv)
    plan = json.loads(Path(args.plan).read_text())
    if args.command == 'check':
        selectors = json.loads((BASE / 'selectors.candidate.json').read_text())
        selectors.update(json.loads((BASE / 'm8_events_daily_selectors.candidate.json').read_text()))
        bindings = check_plan(plan, selectors)
        print(json.dumps({'status': 'static-valid', 'module': 'M8',
                          'cases': len(plan['cases']), 'device_verified': False,
                          'implementation_ready': plan.get('implementation_ready', False),
                          'required_sql_bindings': bindings,
                          'required_tests': M8_REQUIRED_TESTS,
                          'next': '实机阶段：inspect/calibrate 校准 M8 控件，产出 M8 服务端门禁后方可 run'},
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
    report = runner.output / 'm8-result.json'
    report.write_text(json.dumps({
        'module': 'M8', 'passed': bool(passed), 'device_validation_attempted': True,
        'device_verified': bool(passed), 'source_fingerprint': before,
        'runner_result': 'result.json', 'scope': 'declared-m8-plan',
        'note': 'M8 实机验收须联合 M8 服务端门禁与校准树审阅',
    }, ensure_ascii=False, indent=2) + '\n')
    manifest_path = runner.output / 'manifest.json'
    manifest = json.loads(manifest_path.read_text())
    manifest.append({'path': report.name, 'sha256': hashlib.sha256(report.read_bytes()).hexdigest()})
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
