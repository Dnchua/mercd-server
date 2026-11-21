"""协议/存储验收与设备验收分开；只跑临时存档的现有Go测试，不回放到设备账号。"""
import argparse
import json
from pathlib import Path
import subprocess
import time
import hashlib
from .provenance import source_fingerprint

COMMANDS=[
 ['go','test','./internal/cn35game','./internal/cn35store','./internal/database','-run','Test(Guild|GW|DepositBPStorage|Migration|Backup)','-count=1','-json'],
]

def main():
 a=argparse.ArgumentParser();a.add_argument('--output',required=True);a.add_argument('--race',action='store_true');o=a.parse_args()
 repo=Path(__file__).resolve().parents[2];out=Path(o.output).resolve()/('server-gate-'+time.strftime('%Y%m%d-%H%M%S'));out.mkdir(parents=True,exist_ok=False)
 before=source_fingerprint()
 commands=list(COMMANDS)
 if o.race:commands.append(['go','test','-race','-timeout','30m','./internal/cn35game','./internal/cn35store','-run','Test(Guild|GW|DepositBPStorage)','-count=1','-json'])
 results=[]
 passed_tests=set()
 for i,cmd in enumerate(commands):
  log=out/(str(i)+'.log')
  with log.open('wb') as f:r=subprocess.run(cmd,cwd=repo/'server',stdout=f,stderr=subprocess.STDOUT,shell=False)
  for line in log.read_text(errors='replace').splitlines():
   try:event=json.loads(line)
   except ValueError:continue
   if event.get('Action')=='pass' and event.get('Test'):passed_tests.add(event['Test'])
  results.append({'argv':cmd,'returncode':r.returncode,'log':str(log),'sha256':hashlib.sha256(log.read_bytes()).hexdigest()})
 required={'TestGuildEconomyRollback','TestGuildEconomyUpgradeAndBackup','TestGuildBPStorageConcurrentSharedPool','TestGWOpenReplayMultiTargetAndRestart','TestGuildRolesAndDropout','TestGuildBattleComments','TestGuildWaitingViewerPrivacy','TestGuildSoloConcurrentLastSlot','TestGWAttackAliasSuccessAndIdempotent','TestGuildBPStorageRollbackOnConstraintFailure','TestGuildOpSameKeyDifferentPayloadRejected','TestGWLateResultDoesNotRewriteSettled'}
 missing=sorted(required-passed_tests)
 (out/'result.json').write_text(json.dumps({'scope':'protocol-store','missing_required_tests':missing,'passed_tests':sorted(passed_tests),'device_validation':False,'source_fingerprint':before,'passed':not missing and before==source_fingerprint() and all(r['returncode']==0 for r in results),'results':results},indent=2))
 return 0 if not missing and before==source_fingerprint() and all(r['returncode']==0 for r in results) else 1
if __name__=='__main__':raise SystemExit(main())
