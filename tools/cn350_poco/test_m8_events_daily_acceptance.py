"""只测试候选 M8 Poco 静态契约、SQL 编译绑定与活动目录生成一致性，不连接游戏或设备。"""
import contextlib
import copy
import io
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from .m8_events_daily_acceptance import (
    BASE,
    DEFAULT_M8_SQL_QUERIES,
    M8_REQUIRED_TESTS,
    bind_sql,
    check_plan,
    main,
    require_gate,
    resolve_sql_variables,
)

REPO = BASE.parent.parent


class M8EventsDailyGateTests(unittest.TestCase):
    def setUp(self):
        self.plan = json.loads((BASE / 'm8_events_daily_plan.json').read_text())
        self.selectors = json.loads((BASE / 'selectors.candidate.json').read_text())
        self.m8_selectors = json.loads((BASE / 'm8_events_daily_selectors.candidate.json').read_text())
        self.selectors.update(self.m8_selectors)

    def test_complete_candidate_has_three_way_gates(self):
        bindings = check_plan(self.plan, self.selectors)
        self.assertEqual(24, len(bindings))
        self.assertFalse(self.plan['implementation_ready'])
        self.assertTrue(all(s['status'] == 'candidate-not-device-verified' for s in self.m8_selectors.values()))

    def test_cannot_remove_required_case(self):
        self.plan['cases'].pop()
        with self.assertRaisesRegex(ValueError, '必需用例缺失'):
            check_plan(self.plan, self.selectors)

    def test_candidate_refused_before_any_config_or_device_access(self):
        with self.assertRaisesRegex(ValueError, '候选计划'):
            require_gate(self.plan, {})

    def test_missing_write_or_constant_sql_refused(self):
        with self.assertRaises(ValueError):
            bind_sql(self.plan, {})
        keys = check_plan(self.plan, self.selectors)
        with self.assertRaises(ValueError):
            bind_sql(self.plan, {k: 'DELETE FROM cn35_event_area_rewards' for k in keys})
        with self.assertRaisesRegex(ValueError, '真实 CN3.5'):
            bind_sql(self.plan, {k: 'SELECT 1' for k in keys})

    def test_stale_or_incomplete_gate_refused(self):
        self.plan['implementation_ready'] = True
        good = {'module': 'M8', 'scope': 'protocol-store', 'passed': True, 'source_fingerprint': 'current',
                'required_tests': list(M8_REQUIRED_TESTS), 'passed_tests': list(M8_REQUIRED_TESTS),
                'missing_required_tests': [], 'unresolved_fields': []}
        with tempfile.TemporaryDirectory() as d, patch('tools.cn350_poco.m8_events_daily_acceptance.source_fingerprint', return_value='current'):
            path = Path(d) / 'gate.json'
            cfg = {'server_result': str(path)}
            for change in ({'module': 'M7'}, {'required_tests': M8_REQUIRED_TESTS[:-1]},
                           {'passed_tests': []}, {'source_fingerprint': 'old'},
                           {'unresolved_fields': ['event_row_texture']}):
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
        self.assertEqual(('static-valid', 'M8', 12, False, False),
                         (out['status'], out['module'], out['cases'], out['device_verified'], out['implementation_ready']))

    def test_default_sql_bindings_cover_plan_and_compile_against_schema(self):
        self.assertEqual(sorted(DEFAULT_M8_SQL_QUERIES.keys()), check_plan(self.plan, self.selectors))
        bound = bind_sql(self.plan, resolve_sql_variables())
        mig_dir = REPO / 'server' / 'internal' / 'database' / 'migrations'
        conn = sqlite3.connect(':memory:')
        conn.execute('PRAGMA foreign_keys=ON')
        try:
            for mig in sorted(mig_dir.glob('*.sql')):
                conn.executescript(mig.read_text())
            for case in bound['cases']:
                for step in case['steps']:
                    if step['op'] == 'capture_sql':
                        self.assertIsNotNone(conn.execute(step['sql'], ['u1']).fetchone())
                    elif step['op'] == 'assert_sql':
                        row = conn.execute(step['sql'], ['u1', 0]).fetchone()
                        self.assertIn(row[0], (0, 1))
        finally:
            conn.close()

    def test_event_catalog_fixture_matches_generator(self):
        proc = subprocess.run([sys.executable, str(REPO / 'server' / 'tools' / 'build_event_catalog.py'), '--check'],
                              capture_output=True, text=True)
        self.assertEqual(0, proc.returncode, proc.stderr)
        cat = json.loads((REPO / 'server' / 'assets' / 'cn350' / 'serve_event_catalog.json').read_text())
        self.assertEqual(42, len(cat['past_events']))
        self.assertEqual(12, len(cat['events'][0]['quests']))
        self.assertEqual(28, sum(len(d['quests']) for d in cat['daily']['days']))
        anchor = cat['daily']['days'][6]['quests'][3]
        self.assertEqual((80064, 104), (anchor['id'], anchor['x_origin']['origin_quest_id']))
        self.assertEqual(38, sum(len(w) for w in anchor['battle']['appear_table'].values()))


if __name__ == '__main__':
    unittest.main()
