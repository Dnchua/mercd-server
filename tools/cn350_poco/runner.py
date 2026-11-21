"""状态化公会验收；UI、该账号新增网络记录、只读DB三方门禁。"""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import re
import sqlite3
import subprocess
import time
import traceback
import uuid
from .ui import UI, expand, select
from .bridge import UnityBridge
from .recorder import CaseRecorder
from .provenance import source_fingerprint

BASE=Path(__file__).parent
UI_OPS={'click','long_click','set_text','visible','text_equals','disabled','swipe'}
OPS=UI_OPS|{'request','assert_sql','capture_sql','mark','server','restart_app','wait','absent'}


def scalar(db,sql,params):
    if not sql.lstrip().upper().startswith(('SELECT ','WITH ')): raise ValueError('仅允许只读查询')
    with sqlite3.connect(Path(db).resolve().as_uri()+'?mode=ro',uri=True,timeout=5) as conn:
        conn.execute('PRAGMA query_only=ON')
        row=conn.execute(sql,params).fetchone()
        if row is None: raise AssertionError('DB断言无结果: '+sql)
        return row[0]


def compare(actual,op,expected):
    tests={'eq':lambda:actual==expected,'gt':lambda:actual>expected,'ge':lambda:actual>=expected,'lt':lambda:actual<expected,'le':lambda:actual<=expected,'ne':lambda:actual!=expected}
    if op not in tests or not tests[op](): raise AssertionError(f'DB断言失败: {actual!r} {op} {expected!r}')


def steps_for(plan,steps):
    out=[]
    for step in steps:
        if 'flow' in step:
            if step['flow'] not in plan['flows']: raise ValueError('未知flow')
            # flows不允许递归，避免空步骤或循环隐藏验收动作。
            for item in plan['flows'][step['flow']]:
                if 'flow' in item: raise ValueError('flow不允许嵌套')
                item=copy.deepcopy(item)
                if 'role' in step: item['role']=step['role']
                out.append(item)
        else: out.append(step)
    return out


def validate(plan,selectors):
    ids=set()
    for case in plan['cases']:
        if case['id'] in ids: raise ValueError('重复case id')
        for dependency in case.get('depends_on',[]):
            if dependency not in ids: raise ValueError('依赖必须出现在前面: '+dependency)
        ids.add(case['id']); steps=steps_for(plan,case['steps'])
        kinds={s.get('op') for s in steps}
        if not kinds & {'visible','text_equals','disabled','absent'} or not {'assert_sql','request'}.issubset(kinds): raise ValueError('case缺UI/网络/DB门禁: '+case['id'])
        for s in steps:
            if s['op'] not in OPS: raise ValueError('未知动作: '+s['op'])
            if s['op'] in UI_OPS|{'absent'} and s['target'] not in selectors: raise ValueError('选择器缺失: '+s['target'])
    if not ids: raise ValueError('禁止空套件')


class RequestLog:
    def __init__(self,path):self.path=Path(path);self.offset=0;self.identity=None;self.pending=[]
    def mark(self):
        stat=self.path.stat();self.offset=stat.st_size;self.identity=(stat.st_dev,stat.st_ino);self.pending=[]
    def wait(self,path,uid,timeout=25,decoded=None,allow_missing_uid=False):
        # 部分端点（如 /guilds/set_shifts）decoded 不带 uid；只有显式 allow_missing_uid 才放宽。

        deadline=time.monotonic()+timeout
        while time.monotonic()<deadline:
            st=self.path.stat()
            if self.identity!=(st.st_dev,st.st_ino) or st.st_size<self.offset:
                raise RuntimeError('请求日志轮转/截断，拒绝复用旧证据；重新mark')
            with self.path.open('rb') as f:
                f.seek(self.offset)
                while True:
                    begin=f.tell();raw=f.readline()
                    if not raw:break
                    if not raw.endswith(b'\n'):f.seek(begin);break
                    row=json.loads(raw)
                    self.pending.append((begin,row))
                self.offset=f.tell()
            for i,(pos,row) in enumerate(self.pending):
                d=row.get('decoded') or {}
                uid_ok = d.get('uid')==uid or (allow_missing_uid and d.get('uid') is None)
                if row.get('path','').split('?')[0].rstrip('/')==path.rstrip('/') and uid_ok and row.get('status')==200 and all(d.get(k)==v for k,v in (decoded or {}).items()):
                    self.pending.pop(i)
                    return {'offset':pos,'record':row}
            time.sleep(.2)
        raise AssertionError('无本账号新请求: '+path)


class Runner:
    def __init__(self,cfg,plan,selectors,selected=None):
        validate(plan,selectors)
        self.cfg,self.plan,self.selectors=cfg,plan,selectors
        self.selected=set(selected) if selected else None
        if self.selected:
            by_id={c['id']:c for c in plan['cases']}
            todo=list(self.selected)
            while todo:
                for dep in by_id[todo.pop()].get('depends_on',[]):
                    if dep not in self.selected:self.selected.add(dep);todo.append(dep)
        self.variables=dict(cfg.get('variables',{}));self.variables['run_id']=uuid.uuid4().hex[:6]
        for role,a in cfg['actors'].items():self.variables[role+'_uid']=a['user_id']
        self.results=[];self.index=0
        self.record_cfg=dict(cfg.get('record') or {})

    def preflight(self):
        if not re.fullmatch(r'[0-9a-f]{64}',self.cfg.get('client_fingerprint','')):raise ValueError('必须提供已安装调试APK的实际SHA-256')
        db=Path(self.cfg['db']).resolve()
        if not (db.parent/'.airtest-test-state').is_file(): raise ValueError('必须使用专用验收副本')
        if self.plan.get('single_actor'):
            if 'A' not in self.cfg['actors']:raise ValueError('单账号验收必须配置A真实账号')
        else:
            if set(self.cfg['actors']) != {'A','B'}:raise ValueError('整套验收必须配置A/B两真实账号')
            two_device = len({a['uri'] for a in self.cfg['actors'].values()})==2
            if not two_device and not self.cfg.get('single_device_sequential'):
                raise ValueError('两个账号必须连接不同设备/模拟器；单设备顺序双账号需显式 single_device_sequential=true')
            if len({a['user_id'] for a in self.cfg['actors'].values()})!=2:raise ValueError('两个账号必须不同')
        for case in self.plan['cases']:
            for s in steps_for(self.plan,case['steps']):
                if s['op'] in UI_OPS|{'absent'}:
                    meta=self.selectors[s['target']].get('calibration',{})
                    if meta.get('client_fingerprint')!=self.cfg.get('client_fingerprint') or not meta.get('tree_sha256'):
                        raise ValueError('选择器未在本客户端校准: '+s['target'])
                    evidence=Path(meta.get('evidence',''))
                    if not evidence.is_file() or hashlib.sha256(evidence.read_bytes()).hexdigest()!=meta['tree_sha256']:raise ValueError('选择器校准树缺失或哈希变化: '+s['target'])
        for a in self.cfg['actors'].values():
            if scalar(db,'SELECT count(*) FROM cn35_accounts WHERE user_id=?',[a['user_id']])!=1: raise ValueError('测试账号不存在')

    def run(self):
        self.output=Path(self.cfg['output']).resolve()/('poco-'+time.strftime('%Y%m%d-%H%M%S')+'-'+self.variables['run_id'])
        self.output.mkdir(parents=True,exist_ok=False)
        self.bridge=UnityBridge(self.cfg,self.output)
        record_opts={k:v for k,v in self.record_cfg.items() if k in ('enabled','mode','fps','max_time','orientation','bit_rate')}
        self.recorder=CaseRecorder(lambda: self.bridge.api.device() if getattr(self.bridge,'api',None) else None,
                                   self.output/'videos', logger=print, **record_opts)
        self.requests=RequestLog(self.cfg['requests'])
        failure=None
        try:
            self.preflight();self.bridge.connect()
            self.ui=UI(self.bridge,self.selectors,self.variables)
            completed=set()
            for case in self.plan['cases']:
                if self.selected and case['id'] not in self.selected:continue
                if any(dep not in completed for dep in case.get('depends_on',[])):
                    self.results.append({'id':case['id'],'status':'blocked','reason':'前置用例未通过'});continue
                self.case=case['id'];self.role='A';self.variables['case_id']=self.case
                # 必须用设备时间，不能把宿主时钟直接作为logcat游标。
                log_since={}
                for role in self.cfg['actors']:
                    self.bridge.activate(role)
                    log_since[role]=self.bridge.api.shell('date +%m-%d\\ %H:%M:%S.000').strip()
                error=None
                self.recorder.start(self.case)
                try:
                    self.requests.mark()
                    for s in steps_for(self.plan,case['steps']):self.step(s)
                except Exception as e:
                    error=str(e);traceback.print_exc()
                    try:self.evidence('failure')
                    except Exception as capture:error+=' 失败证据采集异常:'+str(capture)
                finally:
                    video=self.recorder.stop()
                    for role in self.cfg['actors']:
                        try:
                            self.bridge.activate(role)
                            logs=self.bridge.api.shell('logcat -d -v time -T "'+log_since[role]+'"')
                            (self.output/(self.case+'-'+role+'.log')).write_text(logs)
                            if re.search(r'NullReferenceException|FATAL EXCEPTION|Fatal signal',logs):error=(error or '')+' 客户端异常，见logcat'
                        except Exception as e:error=(error or '')+' 日志采集失败:'+str(e)
                result={'id':self.case,'status':'failed' if error else 'passed','error':error,'criteria':case['criteria'],'video':video}
                if video and video.get('error'): result['video_error']=video['error']
                self.results.append(result)
                if not error:completed.add(self.case)
        except Exception as e:failure=str(e);traceback.print_exc()
        finally:
            try:self.recorder.stop()
            except Exception:pass
            self.bridge.close()
            full=len(self.results)==len(self.plan['cases']) and not self.selected
            passed=not failure and bool(self.results) and all(r['status']=='passed' for r in self.results)
            gate=None
            if self.cfg.get('server_result'):
                try:gate=json.loads(Path(self.cfg['server_result']).read_text())
                except (OSError,ValueError):gate=None
            gate_ok=bool(gate and gate.get('passed') is True and gate.get('scope')=='protocol-store' and gate.get('source_fingerprint')==source_fingerprint())
            two_device = len({a['uri'] for a in self.cfg['actors'].values()})==2
            summary={'schemaVersion':1,'two_device':two_device,'record':{'enabled':self.recorder.enabled,'videos':len([r for r in self.results if r.get('video')])},'single_device_sequential':bool(self.cfg.get('single_device_sequential')),'test_plan_sha256':hashlib.sha256(json.dumps(self.plan,sort_keys=True).encode()).hexdigest(),'selectors_sha256':hashlib.sha256(json.dumps(self.selectors,sort_keys=True).encode()).hexdigest(),'source_fingerprint':source_fingerprint(),'scope':'full' if full else 'partial','full_suite_passed':bool(full and passed and gate_ok and two_device),'single_device_passed':bool(full and passed and not two_device),'device_suite_passed':bool(full and passed),'server_gate_current':gate_ok,'selected_passed':passed,'preflight_error':failure,'results':self.results,'variables':self.variables}
            (self.output/'result.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2))
            files=[{'path':str(p.relative_to(self.output)),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()} for p in self.output.rglob('*') if p.is_file()]
            (self.output/'manifest.json').write_text(json.dumps(files,ensure_ascii=False,indent=2))
        return 0 if passed and (self.selected or gate_ok) else 1

    def evidence(self,label):
        self.index+=1
        prefix=f'{getattr(self,"case","preflight")}-{self.index:04d}-{label}'
        self.bridge.activate(getattr(self,'role','A'))
        self.bridge.api.snapshot(filename=str(self.output/(prefix+'.png')),msg=label)
        self.bridge.dump(getattr(self,'role','A'),self.output/(prefix+'.tree.json'))

    def step(self,step):
        role=step.get('role',self.role);self.role=role
        op=step['op']
        if op in UI_OPS:
            self.ui.act(role,op,step['target'],step.get('value'));self.evidence(op)
        elif op=='absent':
            node=select(self.bridge.activate(role),expand(self.selectors[step['target']]['selector'],self.variables))
            if len(node)>0 and any(n.attr('visible') is True for n in node):raise AssertionError('控件仍可见: '+step['target'])
            self.evidence('absent')
        elif op=='mark': self.requests.mark()
        elif op=='request':
            result=self.requests.wait(expand(step['path'],self.variables),self.cfg['actors'][role]['user_id'],step.get('timeout',25),expand(step.get('decoded',{}),self.variables),step.get('allow_missing_uid',False))
            with (self.output/(self.case+'.requests.jsonl')).open('a') as f:f.write(json.dumps(result,ensure_ascii=False)+'\n')
        elif op in {'assert_sql','capture_sql'}:
            params=[self.variables[p[2:-1]] if isinstance(p,str) and p.startswith('${') and p.endswith('}') and p[2:-1] in self.variables and isinstance(self.variables[p[2:-1]],(int,float)) else expand(p,self.variables) for p in step.get('params',[])]
            actual=scalar(self.cfg['db'],step['sql'],params)
            if op=='capture_sql':self.variables[step['as']]=actual
            else:
                expected=expand(step['expected'],self.variables)
                if step.get('expected_var'):expected=self.variables[step['expected_var']]+step.get('delta',0)
                compare(actual,step.get('compare','eq'),expected)
            with (self.output/(self.case+'.db.jsonl')).open('a') as f:f.write(json.dumps({'step':step,'actual':actual},ensure_ascii=False)+'\n')
        elif op=='wait':time.sleep(min(float(step['seconds']),30))
        elif op=='server':
            # 仅显式配置的服务编排argv；绝不回放客户端请求。
            argv=expand(self.cfg['server_hooks'][step['action']],self.variables)
            subprocess.run(argv,shell=False,check=True,timeout=120);self.requests.mark()
        elif op=='restart_app':
            self.bridge.activate(role);self.bridge.api.stop_app(self.cfg['package']);self.bridge.api.start_app(self.cfg['package'])
            time.sleep(self.cfg.get('startup_wait',8));self.bridge.reconnect(role)
        else:raise ValueError(op)


def main(argv=None):
    ap=argparse.ArgumentParser()
    ap.add_argument('command',choices=['validate','run','inspect','calibrate'])
    ap.add_argument('--config',required=True);ap.add_argument('--plan');ap.add_argument('--case',action='append');ap.add_argument('--role',default='A');ap.add_argument('--selector');ap.add_argument('--record',action='store_true')
    a=ap.parse_args(argv);config_path=Path(a.config).resolve();cfg=json.loads(config_path.read_text())
    if a.record:
        cfg.setdefault('record',{})['enabled']=True
    plan_path=Path(a.plan or cfg.get('plan') or (BASE/'guild_plan.json')).resolve()
    plan=json.loads(plan_path.read_text());selector_path=Path(cfg['selectors']).resolve();selectors=json.loads(selector_path.read_text())
    validate(plan,selectors)
    if a.case and not set(a.case).issubset({c['id'] for c in plan['cases']}):raise ValueError('未知case')
    if a.command=='validate':
        print(json.dumps({'cases':len(plan['cases']),'selectors':len(selectors),'status':'static-valid','device_verified':False}));return 0
    if a.command=='run':return Runner(cfg,plan,selectors,a.case).run()
    out=Path(cfg['output'])/('inspect-'+uuid.uuid4().hex[:10]);out.mkdir(parents=True)
    bridge=UnityBridge(cfg,out)
    try:
        bridge.connect();tree=out/(a.role+'.tree.json');bridge.dump(a.role,tree)
        if a.command=='calibrate':
            if not a.selector:raise ValueError('需要--selector')
            # 动态gid/marker在执行时实例化；校准时提供相同变量绑定。
            spec=expand(selectors[a.selector]['selector'],cfg.get('variables',{}));node=select(bridge.activate(a.role),spec)
            if len(node)!=1 or node.attr('visible') is not True:raise ValueError('校准要求唯一可见节点')
            selectors[a.selector]['calibration']={'client_fingerprint':cfg['client_fingerprint'],'tree_sha256':hashlib.sha256(tree.read_bytes()).hexdigest(),'role':a.role,'evidence':str(tree),'at':time.time()}
            selector_path.write_text(json.dumps(selectors,ensure_ascii=False,indent=2))
        print(str(tree))
    finally:bridge.close()
    return 0
