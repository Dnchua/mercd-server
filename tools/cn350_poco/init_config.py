"""生成本机配置，不写入真实凭据；定位候选不是已验证控件名。"""
import argparse
import json
from pathlib import Path
import shutil
import sys

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--out',default='tools/cn350_poco/config.local.json');a=ap.parse_args()
    repo=Path(__file__).resolve().parents[2];target=Path(a.out).resolve()
    if target.exists():raise ValueError('拒绝覆盖现有配置')
    selectors=target.parent/'selectors.local.json'
    if selectors.exists():raise ValueError('拒绝覆盖已校准选择器')
    baseline=json.loads((repo/'client/baseline.json').read_text())
    state=str(repo/'state/poco/acceptance')
    actors={}
    for role in ('A','B'):
        actors[role]={'uri':'Android:///REPLACE_'+role+'_SERIAL','user_id':'REPLACE_'+role+'_UID','poco':{
          'mode':'jni-load','so_sha256':'REPLACE_SO_SHA256','dll_sha256':'REPLACE_DLL_SHA256'}}
    cfg={'package':baseline['applicationId'],'client_fingerprint':'REPLACE_INSTALLED_BASE_APK_SHA256',
      'selectors':str(selectors),'actors':actors,'db':state+'/mercd35.sqlite3','requests':state+'/requests.ndjson',
      'output':str(repo/'output/poco'),'server_result':'REPLACE_SERVER_GATE_RESULT_JSON','binary':str(repo/'output/rebuilt/mercd35'),
      'addr':'0.0.0.0:18092','server_host':'REPLACE_LAN_IP:18092','assets':str(repo/'server/assets/cn350'),'cards':str(repo/'server/assets/shared/cards'),
      'clock_start':'2026-10-29T12:45:00+08:00','server_args':[],
      'variables':{'guild_name':'验收${run_id}','A_name':'REPLACE_A_NAME','B_name':'REPLACE_B_NAME','memo':'验收${run_id}',
        'marker':'gc${run_id}','long_message':'x'*111,'truncated_message':'x'*110,'closed_month':'2026-10','previous_month_label':'10月',
        'enemy_guild_name':'REPLACE_OPPONENT_1','enemy_guild_name2':'REPLACE_OPPONENT_2','enemy_guild_name3':'REPLACE_OPPONENT_3',
        'weak_party_name':'REPLACE_WEAK_PARTY_NAME','strong_party_name':'REPLACE_STRONG_PARTY_NAME','difficulty_name':'普通'},'server_hooks':{},
      'record':{'enabled':False,'mode':'yosemite','fps':10,'orientation':2,'max_time':900}}
    controller=Path(__file__).with_name('server_control.py').resolve()
    for action in ('off_window','after_window','morning','noon','evening','night','next_day_open_window','next_month','next_window'):
        cfg['server_hooks'][action]=[sys.executable,str(controller),action,'--config',str(target)]
    cfg['server_hooks']['restart_same_state']=[sys.executable,str(controller),'restart','--config',str(target)]
    target.parent.mkdir(parents=True,exist_ok=True)
    target.write_text(json.dumps(cfg,ensure_ascii=False,indent=2)+'\n');shutil.copyfile(Path(__file__).parent/'selectors.candidate.json',selectors)
    print(str(target));print(str(selectors))
if __name__=='__main__':main()
