"""在已启动的国服调试客户端内，用 JNI 加载独立只读 Poco 桥（不修改磁盘业务DLL/APK）。

沿用 cn-mercd-backend 的设备内独立测试桥接口
  files/libcn350poco.so        (ARM32，只提供 socket 原语)
  files/Cn350.Poco.Runtime.dll (托管 LegacyPocoManager，只读 Dump/GetScreenSize/GetSDKVersion)
通过 Runtime.load0(appClass, soPath) 让 ART 用**应用 ClassLoader 命名空间** dlopen，
原生桥加载托管程序集后在 Unity 主线程日志回调里启动 5001 监听。
必须使用应用 ClassLoader；用 BootClassLoader/裸 dlopen 会因 ABI（EM_ARM vs EM_386）被拒。
"""
import json
import re
import socket
import struct
import subprocess
import time

PKG_DEFAULT = 'com.happyelements.miraclecn.uc.cn350dbg'

JNI_LOAD_JS = r'''
function ti(t,i){return t.add(i*Process.pointerSize).readPointer();}
rpc.exports={
  load(soPath){
    const out={};
    const art=Process.getModuleByName('libart.so');
    if(!art) throw new Error('libart.so 未加载');
    const getVMs=new NativeFunction(art.getExportByName('JNI_GetCreatedJavaVMs'),'int',['pointer','int','pointer']);
    const vmBuf=Memory.alloc(Process.pointerSize), n=Memory.alloc(4);
    const r=getVMs(vmBuf,1,n); if(r!==0) throw new Error('JNI_GetCreatedJavaVMs='+r);
    const vm=vmBuf.readPointer(), invoke=vm.readPointer();
    const attach=new NativeFunction(ti(invoke,4),'int',['pointer','pointer','pointer']);
    const envBox=Memory.alloc(Process.pointerSize);
    const ar=attach(vm,envBox,ptr(0)); if(ar!==0) throw new Error('AttachCurrentThread='+ar);
    const env=envBox.readPointer(), tbl=env.readPointer();
    const FindClass=new NativeFunction(ti(tbl,6),'pointer',['pointer','pointer']);
    const GetStaticMethodID=new NativeFunction(ti(tbl,113),'pointer',['pointer','pointer','pointer','pointer']);
    const GetMethodID=new NativeFunction(ti(tbl,33),'pointer',['pointer','pointer','pointer','pointer']);
    const NewStringUTF=new NativeFunction(ti(tbl,167),'pointer',['pointer','pointer']);
    const CallStaticObjectMethod=new NativeFunction(ti(tbl,114),'pointer',['pointer','pointer','pointer']);
    const CallObjectMethod0=new NativeFunction(ti(tbl,34),'pointer',['pointer','pointer','pointer']);
    const CallObjectMethod1=new NativeFunction(ti(tbl,34),'pointer',['pointer','pointer','pointer','pointer']);
    const CallVoidMethod2=new NativeFunction(ti(tbl,61),'void',['pointer','pointer','pointer','pointer','pointer']);
    const ExOcc=new NativeFunction(ti(tbl,15),'pointer',['pointer']);
    const ExDesc=new NativeFunction(ti(tbl,16),'void',['pointer']);
    const ExClear=new NativeFunction(ti(tbl,17),'void',['pointer']);
    const check=(w)=>{const e=ExOcc(env); if(!e.isNull()){ExDesc(env);ExClear(env);throw new Error('JNI 异常: '+w);}};
    const at=FindClass(env,Memory.allocUtf8String('android/app/ActivityThread'));
    const cur=GetStaticMethodID(env,at,Memory.allocUtf8String('currentApplication'),Memory.allocUtf8String('()Landroid/app/Application;'));
    const app=CallStaticObjectMethod(env,at,cur);
    if(app.isNull()) throw new Error('currentApplication 为空');
    const ctx=FindClass(env,Memory.allocUtf8String('android/content/Context'));
    const gcl=GetMethodID(env,ctx,Memory.allocUtf8String('getClassLoader'),Memory.allocUtf8String('()Ljava/lang/ClassLoader;'));
    const loader=CallObjectMethod0(env,app,gcl);
    if(loader.isNull()) throw new Error('应用 ClassLoader 为空（拒绝退化为 BootClassLoader）');
    const cl=FindClass(env,Memory.allocUtf8String('java/lang/ClassLoader'));
    const loadClass=GetMethodID(env,cl,Memory.allocUtf8String('loadClass'),Memory.allocUtf8String('(Ljava/lang/String;)Ljava/lang/Class;'));
    const target=CallObjectMethod1(env,loader,loadClass,NewStringUTF(env,Memory.allocUtf8String('com.happyelements.miraclecn.MainActivity')));
    if(target.isNull()) throw new Error('应用类加载失败');
    const rt=FindClass(env,Memory.allocUtf8String('java/lang/Runtime'));
    const getRuntime=GetStaticMethodID(env,rt,Memory.allocUtf8String('getRuntime'),Memory.allocUtf8String('()Ljava/lang/Runtime;'));
    const runtime=CallStaticObjectMethod(env,rt,getRuntime);
    const load0=GetMethodID(env,rt,Memory.allocUtf8String('load0'),Memory.allocUtf8String('(Ljava/lang/Class;Ljava/lang/String;)V'));
    CallVoidMethod2(env,runtime,load0,target,NewStringUTF(env,Memory.allocUtf8String(soPath)));
    check('Runtime.load0');
    out.ok=true; return out;
  }
};
'''


def _adb(serial, *args, timeout=30):
    return subprocess.run(['adb', '-s', serial] + list(args), capture_output=True, text=True, timeout=timeout)


def client_pid(serial, package):
    out = _adb(serial, 'shell', 'pidof ' + package).stdout.strip()
    return int(out.split()[0]) if out else None


def process_age_sec(serial, pid):
    """进程启动至今秒数（/proc/<pid>/stat 第22字段 + /proc/uptime，USER_HZ=100）。"""
    out = _adb(serial, 'shell',
               "awk '{print $22}' /proc/%d/stat 2>/dev/null; cut -d' ' -f1 /proc/uptime" % pid).stdout.split()
    if len(out) < 2:
        return None
    try:
        start_ticks = int(out[0]); uptime = float(out[1])
    except ValueError:
        return None
    return uptime - start_ticks / 100.0


def unity_threads(serial, pid):
    out = _adb(serial, 'shell', 'grep -l UnityMain /proc/%d/task/*/comm 2>/dev/null | wc -l' % pid).stdout.strip()
    try:
        return int(out or 0)
    except ValueError:
        return 0


def wait_client_ready(serial, package, timeout=90, min_uptime=12):
    """等客户端真正可注入：进程存在、UnityMain 线程已出现、且启动至少 min_uptime 秒。

    早期进程（尤其模拟器 x86 兼容层刚起 Unity）直接 attach 会 SIGSEGV 在 frida-agent，
    这是实测过的失败模式；宁可多等十几秒，也不要在启动瞬间抢注入。
    """
    deadline = time.time() + timeout
    while time.time() < deadline:
        pid = client_pid(serial, package)
        if pid:
            age = process_age_sec(serial, pid)
            if unity_threads(serial, pid) >= 1 and (age is None or age >= min_uptime):
                return pid
        time.sleep(1)
    raise RuntimeError('等待客户端可注入超时（UnityMain/启动时长不满足）；拒绝在启动瞬间抢注入')


def verify_bridge(serial, package, so_sha256, dll_sha256):
    if not re.fullmatch(r'[A-Za-z0-9_.]+', package) or not package.endswith('.cn350dbg'):
        raise ValueError('仅允许明确的国服本地调试包')
    hashes = (so_sha256, dll_sha256)
    if not all(re.fullmatch(r'[0-9a-f]{64}', digest or '') for digest in hashes):
        raise ValueError('必须提供两个测试桥 SHA-256')
    files = [f'/data/data/{package}/files/libcn350poco.so',
             f'/data/data/{package}/files/Cn350.Poco.Runtime.dll']
    for path, digest in zip(files, hashes):
        result = _adb(serial, 'shell', 'run-as', package, 'sha256sum', path)
        if result.returncode != 0 or not result.stdout.split():
            raise RuntimeError(f'无法读取设备测试桥哈希 {path}: {result.stderr.strip()}')
        actual = result.stdout.split()[0]
        if actual != digest:
            raise RuntimeError(f'{path} SHA-256 不符: {actual} != {digest}')
    return files


def load_bridge(serial, package, so_sha256, dll_sha256,
                timeout=20, wait_ready_timeout=90, min_uptime=12):
    """检查桥指纹后注入。"""
    files = verify_bridge(serial, package, so_sha256, dll_sha256)
    pid = wait_client_ready(serial, package, timeout=wait_ready_timeout, min_uptime=min_uptime)
    import frida
    try:
        dev = frida.get_device(serial, timeout=10)
    except Exception as e:
        raise RuntimeError('Frida 设备不可用（frida-server 是否在运行？）: %s' % e)
    session = dev.attach(pid)
    try:
        script = session.create_script(JNI_LOAD_JS)
        script.load()
        script.exports_sync.load(files[0])
    finally:
        session.detach()
    return {'so_sha256': so_sha256, 'dll_sha256': dll_sha256, 'pid': pid}


def wait_poco(port=5001, timeout=120):
    deadline = time.time() + timeout
    msg = json.dumps({'jsonrpc': '2.0', 'id': 1, 'method': 'GetScreenSize', 'params': {}}).encode()
    while time.time() < deadline:
        try:
            s = socket.create_connection(('127.0.0.1', port), timeout=1)
            s.sendall(struct.pack('<I', len(msg)) + msg)
            data = s.recv(4096)
            s.close()
            if data:
                return True
        except OSError:
            pass
        time.sleep(1)
    return False


def main():
    import argparse
    ap = argparse.ArgumentParser(description='向已运行客户端注入只读 Poco 桥（JNI/AppClassLoader）')
    ap.add_argument('--serial', required=True)
    ap.add_argument('--package', default=PKG_DEFAULT)
    ap.add_argument('--so-sha256', required=True)
    ap.add_argument('--dll-sha256', required=True)
    ap.add_argument('--timeout', type=int, default=120)
    a = ap.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9_.]+', a.package) or not a.package.endswith('.cn350dbg'):
        raise SystemExit('仅允许明确的国服本地调试包')
    result = _adb(a.serial, 'forward', 'tcp:0', 'tcp:5001')
    result.check_returncode()
    port = int(result.stdout.strip())
    try:
        print(json.dumps(load_bridge(a.serial, a.package, a.so_sha256, a.dll_sha256), ensure_ascii=False))
        if not wait_poco(port, timeout=a.timeout):
            raise SystemExit('注入后 Poco 未在 5001 监听；查看 logcat -s CN350Poco')
        print('poco listening 5001')
    finally:
        _adb(a.serial, 'forward', '--remove', f'tcp:{port}')



if __name__ == '__main__':
    main()
