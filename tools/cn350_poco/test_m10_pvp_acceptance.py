"""只测试候选 M10 Poco 静态契约与 SQL 编译绑定，不连接游戏或设备。"""
import contextlib
import copy
import io
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

from .m10_pvp_acceptance import (
    BASE,
    DEFAULT_M10_SQL_QUERIES,
    M10_REQUIRED_TESTS,
    bind_sql,
    check_plan,
    main,
    require_gate,
    resolve_sql_variables,
)

REPO = BASE.parent.parent


class M10PvPGateTests(unittest.TestCase):
    def setUp(self):
        self.plan = json.loads((BASE / 'm10_pvp_plan.json').read_text())
        self.selectors = json.loads((BASE / 'selectors.candidate.json').read_text())
        self.m10_selectors = json.loads((BASE / 'm10_pvp_selectors.candidate.json').read_text())
        self.selectors.update(self.m10_selectors)

    def test_complete_candidate_has_three_way_gates(self):
        bindings = check_plan(self.plan, self.selectors)
        self.assertEqual(24, len(bindings))
        self.assertFalse(self.plan['implementation_ready'])
        self.assertTrue(all(s['status'] == 'candidate-not-device-verified' for s in self.m10_selectors.values()))

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
            bind_sql(self.plan, {k: 'DELETE FROM cn35_ladder_profiles' for k in keys})
        with self.assertRaisesRegex(ValueError, '真实 CN3.5'):
            bind_sql(self.plan, {k: 'SELECT 1' for k in keys})

    def test_stale_or_incomplete_gate_refused(self):
        self.plan['implementation_ready'] = True
        good = {'module': 'M10', 'scope': 'protocol-store', 'passed': True, 'source_fingerprint': 'current',
                'required_tests': list(M10_REQUIRED_TESTS), 'passed_tests': list(M10_REQUIRED_TESTS),
                'missing_required_tests': [], 'unresolved_fields': []}
        with tempfile.TemporaryDirectory() as d, patch('tools.cn350_poco.m10_pvp_acceptance.source_fingerprint', return_value='current'):
            path = Path(d) / 'gate.json'
            cfg = {'server_result': str(path)}
            for change in ({'module': 'M8'}, {'required_tests': M10_REQUIRED_TESTS[:-1]},
                           {'passed_tests': []}, {'source_fingerprint': 'old'},
                           {'unresolved_fields': ['rob_battle_quest_execute']}):
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
        self.assertEqual(('static-valid', 'M10', 12, False, False),
                         (out['status'], out['module'], out['cases'], out['device_verified'], out['implementation_ready']))

    def test_default_sql_bindings_cover_plan_and_compile_against_schema(self):
        self.assertEqual(sorted(DEFAULT_M10_SQL_QUERIES.keys()), check_plan(self.plan, self.selectors))
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

    def test_plan_covers_both_modes_and_keeps_aura_disabled(self):
        paths = {s.get('path') for c in self.plan['cases'] for s in c['steps']}
        paths |= {s.get('path') for f in self.plan['flows'].values() for s in f}
        for p in ('/ladders/ladder_quest_execute.json', '/ladders/lc_used', '/ladders/attack',
                  '/rob_battles/rob_battle_quest_execute.json', '/rob_battles/rc_used', '/rob_battles/attack'):
            self.assertIn(p, paths)
        targets = {s.get('target') for c in self.plan['cases'] for s in c['steps']}
        self.assertIn('m10.daily.chara_buff_disabled', targets)


if __name__ == '__main__':
    unittest.main()
