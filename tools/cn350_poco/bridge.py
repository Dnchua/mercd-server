"""Unity Poco连接/主线程测试桥注入。仅运行时，不能写入客户端业务DLL。"""
import hashlib
import json
from pathlib import Path
import re
import shlex
import subprocess
import time


class UnityBridge:
    def __init__(self, cfg, output):
        self.cfg, self.output = cfg, Path(output)
        self.api = None
        self.pocos, self.sessions, self.scripts = {}, [], []
        self.forwards = {}

    def connect(self):
        from airtest.core import api
        self.api = api
        api.auto_setup(__file__, devices=[a['uri'] for a in self.cfg['actors'].values()], logdir=str(self.output))
        for index, (name, actor) in enumerate(self.cfg['actors'].items()):
            self.reconnect(name, index)

    def reconnect(self, name, index=None):
        from poco.drivers.unity3d import UnityPoco
        actor = self.cfg['actors'][name]
        index = list(self.cfg['actors']).index(name) if index is None else index
        self.api.set_current(index)
        package=self.cfg['package']
        if not re.fullmatch(r'[A-Za-z0-9_.]+',package):raise ValueError('非法包名')
        paths=self.api.shell('pm path '+package).splitlines()
        base=next((x[8:] for x in paths if x.startswith('package:') and x.endswith('/base.apk')),None)
        if not base:raise RuntimeError('无法定位安装包base.apk，不猜测客户端指纹')
        actual=self.api.shell('sha256sum '+shlex.quote(base)).split()[0]
        if actual!=self.cfg['client_fingerprint']:raise RuntimeError('安装客户端SHA-256与配置不符')
        (self.output/('client-'+name+'.json')).write_text(json.dumps({'package':package,'base_sha256':actual}))
        # 每次客户端重启后由执行器重新运行此步骤；失败绝不回落OCR。
        mode = actor['poco']['mode']
        serial = actor['uri'].split('///')[-1].split('?')[0]
        if mode not in {'jni-load', 'mono-inject', 'attached'}:
            raise ValueError('未知Poco模式: '+mode)
        if serial not in self.forwards:
            result = subprocess.run(
                ['adb', '-s', serial, 'forward', 'tcp:0', 'tcp:5001'],
                capture_output=True, text=True, timeout=20, check=True,
            )
            self.forwards[serial] = int(result.stdout.strip())
        port = self.forwards[serial]
        if mode == 'jni-load':
            from . import inject
            so_sha256 = actor['poco'].get('so_sha256')
            dll_sha256 = actor['poco'].get('dll_sha256')
            inject.verify_bridge(serial, package, so_sha256, dll_sha256)
            if not inject.wait_poco(port, timeout=1):
                result = inject.load_bridge(serial, package, so_sha256, dll_sha256)
                if not inject.wait_poco(port, timeout=actor['poco'].get('timeout', 120)):
                    raise RuntimeError('Poco 桥注入后未监听；查看 logcat -s CN350Poco')
                (self.output/('inject-'+name+'.json')).write_text(json.dumps(result, ensure_ascii=False))
        elif mode == 'mono-inject':
            self.inject(name, actor)
        # UnityPoco 对 localhost 会再做一次 ADB 转发，使用 IP 直连已分配的端口。
        poco = UnityPoco(('127.0.0.1', port), device=self.api.device(),
                         connect_default_device=False, screenshot_each_action=False)
        tree = poco.agent.hierarchy.dump()
        if not isinstance(tree, dict) or not tree.get('name') and not tree.get('children'):
            raise RuntimeError('Unity Poco未返回有效树')
        (self.output / ('tree-probe-'+name+'.json')).write_text(json.dumps(tree, ensure_ascii=False, indent=2))
        self.pocos[name] = poco
        self.activate(name)

    def activate(self, role):
        self.api.set_current(list(self.cfg['actors']).index(role))
        return self.pocos[role]

    def inject(self, name, actor):
        import frida
        spec = actor['poco']
        package = self.cfg['package']
        if not package.endswith('.cn350dbg'): raise ValueError('仅允许明确的国服本地调试包')
        if not re.fullmatch(r'[A-Za-z0-9_.]+', package): raise ValueError('非法包名')
        # 设备由配置精确选择，不调用默认-U，防注入另一设备。
        device = frida.get_device(spec['frida_device_id'], timeout=10)
        proc = device.get_process(package)
        session = device.attach(proc.pid)
        script = session.create_script((Path(__file__).parent/'bootstrap/inject_mono.js').read_text())
        try:
            script.load()
            probe = script.exports_sync.probe()
            (self.output/('frida-probe-'+name+'.json')).write_text(json.dumps(probe,indent=2))
            if probe['frida'] != frida.__version__:
                raise RuntimeError('Frida宿主/目标版本不一致')
            thread_id=spec.get('main_thread_id')
            if spec.get('main_thread_name'):
                matches=[t['id'] for t in probe['threads'] if t.get('name')==spec['main_thread_name']]
                if len(matches)!=1: raise ValueError('无法唯一识别指定Unity主线程名称，读取probe，不猜测')
                thread_id=matches[0]
            if not thread_id: raise ValueError('须指定已确认的Unity主线程名称或id')
            assemblies = []
            manifest=[]
            for item in spec['assemblies']:
                path=Path(item['path']).resolve(); raw=path.read_bytes()
                digest=hashlib.sha256(raw).hexdigest()
                if digest != item['sha256']: raise ValueError('测试程序集SHA-256不匹配: '+path.name)
                if path.name in ('Assembly-CSharp.dll','Assembly-CSharp-firstpass.dll','UnityEngine.dll'):
                    raise ValueError('不能把业务/引擎程序集当测试桥重新加载')
                assemblies.append({'name':path.name,'bytes':list(raw)})
                manifest.append({'path':str(path),'sha256':digest})
            (self.output/('inject-manifest-'+name+'.json')).write_text(json.dumps(manifest,indent=2))
            script.exports_sync.arm({'mono_module':spec['mono_module'], 'main_thread_id':thread_id, 'assemblies':assemblies})
            deadline=time.monotonic()+spec.get('timeout',20)
            while time.monotonic()<deadline:
                status=script.exports_sync.status()
                if status['state']=='loaded': break
                if status['state']=='failed': raise RuntimeError(status['error'])
                time.sleep(.1)
            else:
                script.exports_sync.cancel()
                raise TimeoutError('未命中已校准Unity主线程；停止，不转到其他线程强行调用')
            (self.output/('inject-result-'+name+'.json')).write_text(json.dumps(status,indent=2))
        except BaseException:
            session.detach()
            raise
        self.sessions.append(session); self.scripts.append(script)

    def dump(self, role, destination):
        poco=self.activate(role)
        Path(destination).write_text(json.dumps(poco.agent.hierarchy.dump(),ensure_ascii=False,indent=2))

    def close(self):
        for session in self.sessions:
            try: session.detach()
            except Exception: pass
        self.sessions=[]
        for serial, port in self.forwards.items():
            try:
                subprocess.run(['adb', '-s', serial, 'forward', '--remove', f'tcp:{port}'],
                               capture_output=True, timeout=5)
            except (OSError, subprocess.TimeoutExpired):
                pass
        self.forwards.clear()
