"""只读预检，获取真实PID/ABI/Mono/线程，不启动注入，不安装/清数据。"""
import argparse
import json
from pathlib import Path
import subprocess
import re

def main():
    a=argparse.ArgumentParser();a.add_argument('--frida-device',required=True);a.add_argument('--adb-serial',required=True);a.add_argument('--package',required=True);a.add_argument('--output',required=True);o=a.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9_.]+',o.package) or not o.package.endswith('.cn350dbg'):raise ValueError('仅允许明确国服调试包')
    out=Path(o.output);out.mkdir(parents=True,exist_ok=True)
    checks={}
    for name,args in {'devices':['devices','-l'],'hardware':['-s',o.adb_serial,'shell','getprop','ro.hardware'],'qemu':['-s',o.adb_serial,'shell','getprop','ro.kernel.qemu'],'flavor':['-s',o.adb_serial,'shell','getprop','ro.build.flavor'],'package':['-s',o.adb_serial,'shell','dumpsys','package',o.package],'pid':['-s',o.adb_serial,'shell','pidof',o.package]}.items():
        r=subprocess.run(['adb']+args,capture_output=True,text=True,timeout=20)
        checks[name]={'code':r.returncode,'stdout':r.stdout,'stderr':r.stderr}
    import frida
    dev=frida.get_device(o.frida_device,timeout=10)
    # 客户端进程在部分模拟器上的显示名不是包名，按 pid 附加，禁止按名字猜。
    pid=subprocess.run(['adb','-s',o.adb_serial,'shell','pidof',o.package],capture_output=True,text=True,timeout=20).stdout.strip()
    if not pid: raise RuntimeError('客户端未运行: '+o.package)
    session=dev.attach(int(pid.split()[0]))
    try:
        script=session.create_script((Path(__file__).parent/'bootstrap/inject_mono.js').read_text());script.load()
        checks['runtime']=script.exports_sync.probe();checks['host_frida']=frida.__version__
    finally:session.detach()
    (out/'poco-preflight.json').write_text(json.dumps(checks,ensure_ascii=False,indent=2))
    if checks['runtime']['frida']!=frida.__version__:raise RuntimeError('Frida版本不匹配')
    if not any('mono' in m['name'].lower() for m in checks['runtime']['modules']):raise RuntimeError('未发现Mono，不能使用本注入器')
    print(str(out/'poco-preflight.json'))
if __name__=='__main__':main()
