"""只编译独立测试桥。匹配SDK由操作者构建/校验，不下载未知DLL。"""
import argparse
from pathlib import Path
import subprocess
import hashlib
import json

def main():
    a=argparse.ArgumentParser();a.add_argument('--compiler',default='mcs');a.add_argument('--sdk',required=True);a.add_argument('--out',required=True);o=a.parse_args()
    repo=Path(__file__).resolve().parents[2]
    engine=repo/'client/decoded/assets/bin/Data/Managed/UnityEngine.dll'
    sdk=Path(o.sdk).resolve();out=Path(o.out).resolve()
    if out==sdk or repo/'client' in out.parents: raise ValueError('测试产物必须在client之外，不能覆盖SDK')
    out.parent.mkdir(parents=True,exist_ok=True)
    subprocess.run([o.compiler,'-target:library','-out:'+str(out),'-r:'+str(engine),'-r:'+str(sdk),str(Path(__file__).parent/'bootstrap/PocoBootstrap.cs')],check=True)
    print(json.dumps({'sdk_sha256':hashlib.sha256(sdk.read_bytes()).hexdigest(),'unity_sha256':hashlib.sha256(engine.read_bytes()).hexdigest(),'bootstrap':str(out),'bootstrap_sha256':hashlib.sha256(out.read_bytes()).hexdigest(),'runtime_verified':False},indent=2))
if __name__=='__main__':main()
