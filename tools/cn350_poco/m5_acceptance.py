"""M5 候选设备验收入口；check 不连接设备，未定稿的计划拒绝运行。"""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import re

from .provenance import source_fingerprint
from .runner import BASE, Runner, steps_for, validate

REQUIRED_CASES = {f'M5{i:02d}' for i in range(1, 13)}


def check_plan(plan, selectors):
    validate(plan, selectors)
    missing = REQUIRED_CASES - {c['id'] for c in plan['cases']}
    if missing:
        raise ValueError('M5 必需用例缺失: ' + ', '.join(sorted(missing)))
    if plan.get('module') != 'M5' or plan.get('single_actor') is not True:
        raise ValueError('必须使用 M5 单账号计划')
    bindings = set()
    for case in plan['cases']:
        for step in steps_for(plan, case['steps']):
            if step['op'] in {'capture_sql', 'assert_sql'}:
                match = re.fullmatch(r'\$\{(m5_\w+_sql)\}', step['sql'])
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
            match = re.fullmatch(r'\$\{(m5_\w+_sql)\}', step['sql'])
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
        raise ValueError('M5 仍是候选计划；先完成 T10–T12 的实现、断言和控件校准')
    gate = json.loads(Path(cfg['server_result']).read_text())
    if not (gate.get('passed') is True and gate.get('scope') == 'protocol-store'
            and gate.get('module') == 'M5'
            and gate.get('source_fingerprint') == source_fingerprint()):
        raise ValueError('需要当前源指纹的 M5 服务端门禁；公会门禁不能替代')
    required = gate.get('required_tests')
    passed = gate.get('passed_tests', [])
    if not isinstance(required, list) or not required or not set(required).issubset(passed):
        raise ValueError('M5 强制测试清单为空或有未执行/未通过项')
    if gate.get('missing_required_tests') != [] or gate.get('unresolved_fields') != []:
        raise ValueError('M5 仍有未闭合测试或字段，不能执行最终验收')
    coverage = gate.get('field_coverage', {})
    total = coverage.get('total', 0)
    if not isinstance(total, int) or total <= 0 or coverage.get('verified') != total:
        raise ValueError('M5 字段覆盖清单为空或尚未全量闭合')


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['check', 'run'])
    parser.add_argument('--plan', default=str(BASE / 'm5_plan.json'))
    parser.add_argument('--config')
    parser.add_argument('--record', action='store_true')
    args = parser.parse_args(argv)
    plan = json.loads(Path(args.plan).read_text())
    if args.command == 'check':
        selectors = json.loads((BASE / 'selectors.candidate.json').read_text())
        selectors.update(json.loads((BASE / 'm5_selectors.candidate.json').read_text()))
        bindings = check_plan(plan, selectors)
        print(json.dumps({'status': 'static-valid', 'module': 'M5',
                          'cases': len(plan['cases']), 'device_verified': False,
                          'implementation_ready': plan.get('implementation_ready', False),
                          'required_sql_bindings': bindings,
                          'next': '按 ACCEPTANCE.md 完成 T10–T12；本命令不表示可运行或验收通过'},
                         ensure_ascii=False, indent=2))
        return 0
    if not args.config:
        parser.error('run 必须指定 --config；仅支持专用测试副本')
    cfg = json.loads(Path(args.config).read_text())
    require_gate(plan, cfg)
    selectors = json.loads(Path(cfg['selectors']).read_text())
    check_plan(plan, selectors)
    bound = bind_sql(plan, cfg.get('variables', {}))
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
    # 原 Runner 的 full_suite_passed 属于双设备公会口径，M5 使用独立报告。
    report = runner.output / 'm5-result.json'
    report.write_text(json.dumps({
        'module': 'M5', 'passed': bool(passed), 'device_validation_attempted': True,
        'device_verified': bool(passed),
        'source_fingerprint': before, 'runner_result': 'result.json',
        'scope': 'declared-m5-plan', 'note': '全字段覆盖与原服一致性仍须联合 M5 服务端门禁审阅',
    }, ensure_ascii=False, indent=2) + '\n')
    manifest_path = runner.output / 'manifest.json'
    manifest = json.loads(manifest_path.read_text())
    manifest.append({'path': report.name, 'sha256': hashlib.sha256(report.read_bytes()).hexdigest()})
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
