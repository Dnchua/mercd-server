"""只测试候选 M7 Poco 静态契约与 SQL 编译绑定，不连接游戏或设备。"""
import contextlib
import copy
import io
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

from .m7_smoke_acceptance import (
    BASE,
    DEFAULT_M7_SQL_QUERIES,
    bind_sql,
    check_plan,
    main,
    require_gate,
    resolve_sql_variables,
)


class M7SmokeGateTests(unittest.TestCase):
    def setUp(self):
        self.plan = json.loads((BASE / 'm7_smoke_plan.json').read_text())
        self.selectors = json.loads((BASE / 'selectors.candidate.json').read_text())
        self.m7_selectors = json.loads((BASE / 'm7_smoke_selectors.candidate.json').read_text())
        self.selectors.update(self.m7_selectors)

    def test_complete_candidate_has_three_way_gates(self):
        bindings = check_plan(self.plan, self.selectors)
        self.assertEqual(12, len(bindings))
        self.assertFalse(self.plan['implementation_ready'])
        self.assertFalse(any('calibration' in s for s in self.m7_selectors.values()))

    def test_cannot_remove_required_case(self):
        self.plan['cases'].pop()
        with self.assertRaisesRegex(ValueError, '必需用例缺失'):
            check_plan(self.plan, self.selectors)

    def test_candidate_refused_before_any_config_or_device_access(self):
        with self.assertRaisesRegex(ValueError, '候选计划'):
            require_gate(self.plan, {})

    def test_missing_or_write_sql_refused(self):
        with self.assertRaises(ValueError):
            bind_sql(self.plan, {})
        variables = {key: 'DELETE FROM cn35_spring_claims' for key in check_plan(self.plan, self.selectors)}
        with self.assertRaises(ValueError):
            bind_sql(self.plan, variables)

    def test_constant_sql_refused(self):
        variables = {key: 'SELECT 1' for key in check_plan(self.plan, self.selectors)}
        with self.assertRaisesRegex(ValueError, '真实 CN3.5'):
            bind_sql(self.plan, variables)

    def test_non_m7_or_stale_or_incomplete_gate_refused(self):
        self.plan['implementation_ready'] = True
        good = {'module': 'M7', 'scope': 'protocol-store', 'passed': True,
                'source_fingerprint': 'current', 'required_tests': ['TestM7SmokeSpecialScout33DayRotationAndFilters'],
                'passed_tests': ['TestM7SmokeSpecialScout33DayRotationAndFilters'], 'missing_required_tests': [],
                'unresolved_fields': [], 'field_coverage': {'total': 8, 'verified': 8}}
        with tempfile.TemporaryDirectory() as d, patch('tools.cn350_poco.m7_smoke_acceptance.source_fingerprint', return_value='current'):
            path = Path(d) / 'gate.json'
            cfg = {'server_result': str(path)}
            for change in ({'module': 'guild'}, {'module': 'M6'}, {'required_tests': []},
                           {'passed_tests': []}, {'source_fingerprint': 'old'},
                           {'unresolved_fields': ['special_scout_banner']},
                           {'field_coverage': {'total': 8, 'verified': 7}}):
                bad = copy.deepcopy(good)
                bad.update(change)
                path.write_text(json.dumps(bad))
                with self.subTest(change=change), self.assertRaises(ValueError):
                    require_gate(self.plan, cfg)
            path.write_text(json.dumps(good))
            require_gate(self.plan, cfg)

    def test_cli_check_command_succeeds(self):
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            code = main(['check'])
        self.assertEqual(0, code)
        out = json.loads(buf.getvalue())
        self.assertEqual('static-valid', out['status'])
        self.assertEqual('M7', out['module'])
        self.assertEqual(6, out['cases'])
        self.assertFalse(out['device_verified'])
        self.assertFalse(out['implementation_ready'])

    def test_default_sql_bindings_cover_plan_and_compile_against_schema(self):
        bindings = check_plan(self.plan, self.selectors)
        self.assertEqual(sorted(DEFAULT_M7_SQL_QUERIES.keys()), bindings)
        bound = bind_sql(self.plan, resolve_sql_variables())
        repo_root = BASE.parent.parent
        mig_dir = repo_root / 'server' / 'internal' / 'database' / 'migrations'
        conn = sqlite3.connect(':memory:')
        conn.execute('PRAGMA foreign_keys=ON')
        try:
            for mig in sorted(mig_dir.glob('*.sql')):
                conn.executescript(mig.read_text())
            for case in bound['cases']:
                for step in case['steps']:
                    if step['op'] == 'capture_sql':
                        row = conn.execute(step['sql'], ['u1']).fetchone()
                        self.assertIsNotNone(row)
                    elif step['op'] == 'assert_sql':
                        before = 1 if case['id'] == 'M701' else 0
                        row = conn.execute(step['sql'], ['u1', before]).fetchone()
                        self.assertIsNotNone(row)
                        self.assertIn(row[0], (0, 1))
        finally:
            conn.close()


if __name__ == '__main__':
    unittest.main()
