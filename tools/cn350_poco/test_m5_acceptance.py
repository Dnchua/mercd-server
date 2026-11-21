"""只测试候选 M5 门禁不会误报通过，不连接游戏或设备。"""
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from .m5_acceptance import BASE, bind_sql, check_plan, require_gate


class M5GateTests(unittest.TestCase):
    def setUp(self):
        self.plan = json.loads((BASE / 'm5_plan.json').read_text())
        self.selectors = json.loads((BASE / 'selectors.candidate.json').read_text())
        self.selectors.update(json.loads((BASE / 'm5_selectors.candidate.json').read_text()))

    def test_complete_candidate_has_three_way_gates(self):
        self.assertEqual(24, len(check_plan(self.plan, self.selectors)))
        self.assertFalse(self.plan['implementation_ready'])

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
        variables = {key: 'DELETE FROM cn35_player_units' for key in check_plan(self.plan, self.selectors)}
        with self.assertRaises(ValueError):
            bind_sql(self.plan, variables)

    def test_constant_sql_refused(self):
        variables = {key: 'SELECT 1' for key in check_plan(self.plan, self.selectors)}
        with self.assertRaisesRegex(ValueError, '真实 CN3.5'):
            bind_sql(self.plan, variables)

    def test_guild_empty_stale_or_incomplete_gate_refused(self):
        self.plan['implementation_ready'] = True
        good = {'module': 'M5', 'scope': 'protocol-store', 'passed': True,
                'source_fingerprint': 'current', 'required_tests': ['TestM5Contract'],
                'passed_tests': ['TestM5Contract'], 'missing_required_tests': [],
                'unresolved_fields': [], 'field_coverage': {'total': 10, 'verified': 10}}
        with tempfile.TemporaryDirectory() as d, patch('tools.cn350_poco.m5_acceptance.source_fingerprint', return_value='current'):
            path = Path(d) / 'gate.json'
            cfg = {'server_result': str(path)}
            for change in ({'module': 'guild'}, {'required_tests': []}, {'passed_tests': []},
                           {'source_fingerprint': 'old'}, {'unresolved_fields': ['quick']},
                           {'field_coverage': {'total': 10, 'verified': 9}}):
                bad = copy.deepcopy(good)
                bad.update(change)
                path.write_text(json.dumps(bad))
                with self.subTest(change=change), self.assertRaises(ValueError):
                    require_gate(self.plan, cfg)
            path.write_text(json.dumps(good))
            require_gate(self.plan, cfg)


if __name__ == '__main__':
    unittest.main()
