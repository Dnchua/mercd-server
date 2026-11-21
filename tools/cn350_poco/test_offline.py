"""验收框架自身回归，不连接设备，不算游戏验收通过。"""
import copy
import contextlib
import io
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from .runner import RequestLog, Runner, compare, scalar, validate, steps_for
from .ui import UI, expand
from .recorder import CaseRecorder

BASE=Path(__file__).parent
class FakeNode:
    def __init__(self,n=1,visible=True,enabled=True,text='内容'):
        self.n,self.visible,self.enabled,self.text=n,visible,enabled,text;self.clicked=False
    def __len__(self):return self.n
    def attr(self,key):return {'visible':self.visible,'enabled':self.enabled,'text':self.text}.get(key)
    def click(self):self.clicked=True
    def set_text(self,value):self.text=value

class FakeApi:
    def __init__(self,node):self.node=node;self.texts=[]
    def text(self,value):self.texts.append(value);self.node.text=value
    def shell(self,*a,**k):return ''

class FakeBridge:
    def __init__(self,node):self.node=node;self.api=FakeApi(node)
    def activate(self,role):return lambda **attrs:self.node

class Offline(unittest.TestCase):
    def setUp(self):
        self.plan=json.loads((BASE/'guild_plan.json').read_text());self.core_plan=json.loads((BASE/'core_ops_plan.json').read_text());self.selectors=json.loads((BASE/'selectors.candidate.json').read_text())
    def test_all_cases_have_three_way_gates(self):
        validate(self.plan,self.selectors);self.assertEqual(40,len(self.plan['cases']))
        validate(self.core_plan,self.selectors);self.assertEqual(12,len(self.core_plan['cases']))
    def test_core_ops_dependencies_expanded(self):
        r=Runner({'actors':{}},self.core_plan,self.selectors,['U07','U09'])
        self.assertIn('U01',r.selected);self.assertIn('U06',r.selected);self.assertIn('U07',r.selected);self.assertIn('U09',r.selected);self.assertNotIn('U12',r.selected)
    def test_no_empty_green(self):
        self.plan['cases']=[]
        with self.assertRaises(ValueError):validate(self.plan,self.selectors)
    def test_missing_visual_gate_rejected(self):
        self.plan['cases'][0]['steps']=[{'op':'click','target':'nav.guild'},{'op':'assert_sql','sql':'SELECT 1','expected':1},{'op':'request','path':'/guilds/'}]
        with self.assertRaises(ValueError):validate(self.plan,self.selectors)
    def test_no_nested_flow(self):
        self.plan['flows']['guild']=[{'flow':'guild'}]
        with self.assertRaises(ValueError):steps_for(self.plan,[{'flow':'guild'}])
    def test_dependencies_expanded(self):
        r=Runner({'actors':{}},self.plan,self.selectors,['G19'])
        self.assertIn('G01',r.selected);self.assertIn('G19',r.selected);self.assertNotIn('G35',r.selected)
    def test_bind_variables_and_missing_failure(self):
        self.assertEqual({'text':'abc'},expand({'text':'${name}'},{'name':'abc'}))
        self.assertEqual(8,len(expand('${guild_name}',{'guild_name':'验收${run_id}','run_id':'123456'})))
        with self.assertRaises(ValueError):expand('${unknown}',{})
    def test_ambiguous_node_not_clicked(self):
        node=FakeNode(n=2);ui=UI(FakeBridge(node),{'k':{'selector':{'attrs':{'name':'x'}}}},{})
        with self.assertRaises(AssertionError):ui.act('A','click','k')
        self.assertFalse(node.clicked)
    def test_disabled_node_not_clicked(self):
        node=FakeNode(enabled=False);ui=UI(FakeBridge(node),{'k':{'selector':{'attrs':{'name':'x'}}}},{})
        with self.assertRaises(AssertionError):ui.act('A','click','k')
        self.assertFalse(node.clicked)
    def test_input_and_text_assertion(self):
        node=FakeNode();ui=UI(FakeBridge(node),{'k':{'selector':{'attrs':{'name':'x'}}}},{'v':'消息'})
        ui.act('A','set_text','k','${v}');ui.act('A','text_equals','k','消息')
        with self.assertRaises(AssertionError):ui.act('A','text_equals','k','错误')
    def test_sql_readonly(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'db';sqlite3.connect(p).close()
            self.assertEqual(1,scalar(p,'SELECT 1',[]))
            with self.assertRaises(ValueError):scalar(p,'DELETE FROM x',[])
            with self.assertRaises(sqlite3.OperationalError):scalar(p,'WITH x AS (SELECT 1) DELETE FROM sqlite_master',[])
    def test_comparison_negative(self):
        compare(1,'eq',1)
        with self.assertRaises(AssertionError):compare(1,'eq',2)
    def test_request_uid_cursor_partial_and_pending(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'requests';p.write_bytes(b'')
            r=RequestLog(p);r.mark()
            rows=[{'path':'/b','status':200,'decoded':{'uid':'A'}},{'path':'/a','status':200,'decoded':{'uid':'B'}},{'path':'/a','status':200,'decoded':{'uid':'A','state':'ok'}}]
            with p.open('a') as f:
                for row in rows:f.write(json.dumps(row)+'\n')
                f.write('{')
            self.assertEqual('ok',r.wait('/a','A',.1)['record']['decoded']['state'])
            self.assertEqual('/b',r.wait('/b','A',.1)['record']['path'])
            with self.assertRaises(AssertionError):r.wait('/b','B',.01)
            with p.open('a') as f:f.write('"path":"/c","status":200,"decoded":{"uid":"A"}}\n')
            self.assertEqual('/c',r.wait('/c','A',.1)['record']['path'])
    def test_log_rotation_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'requests';p.write_text('old\n');r=RequestLog(p);r.mark();p.write_text('')
            with self.assertRaises(RuntimeError):r.wait('/a','A',.1)
    def test_candidate_is_not_calibrated(self):
        self.assertFalse(any('calibration' in s for s in self.selectors.values()))
    def test_preflight_failure_has_report_and_nonzero(self):
        with tempfile.TemporaryDirectory() as d:
            cfg={'actors':{},'output':d,'requests':str(Path(d)/'missing'),'client_fingerprint':'unknown','server_result':str(Path(d)/'missing.json')}
            r=Runner(cfg,self.plan,self.selectors)
            with contextlib.redirect_stderr(io.StringIO()):self.assertEqual(1,r.run())
            report=json.loads((r.output/'result.json').read_text())
            self.assertFalse(report['full_suite_passed']);self.assertFalse(report['device_suite_passed'])
            self.assertIn('SHA-256',report['preflight_error'])
    def test_recorder_disabled_is_noop(self):
        with tempfile.TemporaryDirectory() as d:
            rec=CaseRecorder(lambda: None,d,enabled=False)
            self.assertIsNone(rec.start('G01'));self.assertIsNone(rec.stop())
            self.assertFalse((Path(d)/'manifest.json').exists())

    def test_recorder_writes_video_and_manifest(self):
        class Dev:
            def __init__(self):self.path=None
            def start_recording(self,**kw):self.path=kw['output'];return self.path
            def stop_recording(self):
                Path(self.path).write_bytes(b'mp4-bytes')
        with tempfile.TemporaryDirectory() as d:
            dev=Dev();rec=CaseRecorder(lambda: dev,d,enabled=True,fps=5)
            p=rec.start('G02');self.assertIsNotNone(p)
            entry=rec.stop()
            self.assertEqual(b'mp4-bytes',Path(entry['video']).read_bytes())
            self.assertEqual(9,entry['bytes']);self.assertEqual('G02',entry['case'])
            self.assertEqual(entry['sha256'],__import__('hashlib').sha256(b'mp4-bytes').hexdigest())
            manifest=json.loads((Path(d)/'manifest.json').read_text())
            self.assertEqual(1,len(manifest));self.assertEqual('G02',manifest[0]['case'])

    def test_recorder_failure_does_not_raise(self):
        class Dev:
            def start_recording(self,**kw):raise RuntimeError('boom')
            def stop_recording(self):pass
        with tempfile.TemporaryDirectory() as d:
            rec=CaseRecorder(lambda: Dev(),d,enabled=True)
            self.assertIsNone(rec.start('G03'));self.assertIn('boom',rec.last_error)

    def test_recorder_empty_file_is_removed(self):
        class Dev:
            def start_recording(self,**kw):
                Path(kw['output']).write_bytes(b'');return kw['output']
            def stop_recording(self):pass
        with tempfile.TemporaryDirectory() as d:
            rec=CaseRecorder(lambda: Dev(),d,enabled=True)
            rec.poll_attempts=2
            rec.start('G04');entry=rec.stop()
            self.assertIn('error',entry);self.assertEqual(0,entry['bytes'])
            self.assertEqual([],list(Path(d).glob('*.mp4')))

    def test_recorder_case_context_manager(self):
        class Dev:
            def start_recording(self,**kw):self.path=kw['output'];return self.path
            def stop_recording(self):Path(self.path).write_bytes(b'x')
        with tempfile.TemporaryDirectory() as d:
            rec=CaseRecorder(lambda: Dev(),d,enabled=True)
            with rec.case('G05') as box:
                self.assertIsNotNone(rec.active)
            self.assertEqual('G05',box['video']['case']);self.assertTrue(Path(box['video']['video']).is_file())

    def test_named_window_cases(self):
        actions={s.get('action') for c in self.plan['cases'] for s in steps_for(self.plan,c['steps']) if s['op']=='server'}
        self.assertTrue({'morning','noon','evening','night'}.issubset(actions))

if __name__=='__main__':unittest.main()
