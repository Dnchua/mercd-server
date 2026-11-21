"""只测试候选 M6 门禁不会误报通过，不连接游戏或设备。"""
import contextlib
import copy
import io
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

from .m6_raid_acceptance import (
    BASE,
    DEFAULT_M6_SQL_QUERIES,
    bind_sql,
    check_plan,
    main,
    require_gate,
    resolve_sql_variables,
)


class M6GateTests(unittest.TestCase):
    def setUp(self):
        self.plan = json.loads((BASE / 'm6_raid_plan.json').read_text())
        self.selectors = json.loads((BASE / 'selectors.candidate.json').read_text())
        self.m6_selectors = json.loads((BASE / 'm6_raid_selectors.candidate.json').read_text())
        self.selectors.update(self.m6_selectors)

    def test_complete_candidate_has_three_way_gates(self):
        bindings = check_plan(self.plan, self.selectors)
        self.assertEqual(20, len(bindings))
        self.assertFalse(self.plan['implementation_ready'])
        self.assertFalse(any('calibration' in s for s in self.m6_selectors.values()))

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
        variables = {key: 'DELETE FROM cn35_raid_instances' for key in check_plan(self.plan, self.selectors)}
        with self.assertRaises(ValueError):
            bind_sql(self.plan, variables)

    def test_constant_sql_refused(self):
        variables = {key: 'SELECT 1' for key in check_plan(self.plan, self.selectors)}
        with self.assertRaisesRegex(ValueError, '真实 CN3.5'):
            bind_sql(self.plan, variables)

    def test_non_m6_or_stale_or_incomplete_gate_refused(self):
        self.plan['implementation_ready'] = True
        good = {'module': 'M6', 'scope': 'protocol-store', 'passed': True,
                'source_fingerprint': 'current', 'required_tests': ['TestM6RaidContract'],
                'passed_tests': ['TestM6RaidContract'], 'missing_required_tests': [],
                'unresolved_fields': [], 'field_coverage': {'total': 12, 'verified': 12}}
        with tempfile.TemporaryDirectory() as d, patch('tools.cn350_poco.m6_raid_acceptance.source_fingerprint', return_value='current'):
            path = Path(d) / 'gate.json'
            cfg = {'server_result': str(path)}
            for change in ({'module': 'guild'}, {'module': 'M5'}, {'required_tests': []},
                           {'passed_tests': []}, {'source_fingerprint': 'old'},
                           {'unresolved_fields': ['raid_weapon_rate']},
                           {'field_coverage': {'total': 12, 'verified': 11}}):
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
        self.assertEqual('M6', out['module'])
        self.assertEqual(10, out['cases'])
        self.assertFalse(out['device_verified'])

    def test_default_sql_bindings_cover_plan_and_compile_against_schema(self):
        bindings = check_plan(self.plan, self.selectors)
        self.assertEqual(sorted(DEFAULT_M6_SQL_QUERIES.keys()), bindings)
        bound = bind_sql(self.plan, resolve_sql_variables())
        repo_root = BASE.parent.parent
        mig_dir = repo_root / 'server' / 'internal' / 'database' / 'migrations'
        conn = sqlite3.connect(':memory:')
        conn.executescript("""
            CREATE TABLE cn35_player_profiles (user_id TEXT PRIMARY KEY);
            CREATE TABLE cn35_guild_members (guild_id TEXT NOT NULL, user_id TEXT PRIMARY KEY);
            CREATE TABLE cn35_guild_equipment (guild_id TEXT NOT NULL, type_id TEXT NOT NULL, level INTEGER NOT NULL);
            CREATE TABLE cn35_battle_sessions (battle_id TEXT PRIMARY KEY, user_id TEXT NOT NULL, status TEXT NOT NULL);
            CREATE TABLE cn35_messages (id INTEGER PRIMARY KEY, category TEXT NOT NULL);
        """)
        conn.executescript((mig_dir / '0054_cn35_raid_monsters.sql').read_text())
        for case in bound['cases']:
            for step in case['steps']:
                if step['op'] == 'capture_sql':
                    row = conn.execute(step['sql'], ['u1']).fetchone()
                    self.assertIsNotNone(row)
                elif step['op'] == 'assert_sql':
                    before = 0 if case['id'] in {'M602', 'M603', 'M605', 'M606', 'M607', 'M608', 'M609'} else 'before'
                    row = conn.execute(step['sql'], ['u1', before]).fetchone()
                    self.assertIsNotNone(row)
                    self.assertIn(row[0], (0, 1))
        conn.close()


if __name__ == '__main__':
    unittest.main()

