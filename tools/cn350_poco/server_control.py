"""专用验收服务：一致性复制旧库、同一目录重启、推进判定时间。不注入玩家资产。"""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import time
from urllib.request import ProxyHandler, build_opener


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('action', choices=['clone', 'restart', 'stop', 'open_window', 'next_window', 'next_day_open_window', 'next_month', 'off_window', 'after_window', 'morning', 'noon', 'evening', 'night'])
    ap.add_argument('--config', required=True)
    ap.add_argument('--source-db', help='clone 的只读来源，目标不得已存在')
    args = ap.parse_args()
    cfg = json.loads(Path(args.config).read_text())
    db = Path(cfg['db']).resolve()
    if db.name != 'mercd35.sqlite3':
        ap.error('db 文件名必须为 mercd35.sqlite3，服务端按 -state 目录定位数据库')
    state = db.parent
    marker = state / '.airtest-test-state'
    if args.action == 'clone':
        if db.exists() or state.exists(): raise SystemExit('拒绝覆盖已有目标state，请指定新目录')
        if not args.source_db:
            ap.error('clone 需要 --source-db')
        source = Path(args.source_db).resolve(strict=True)
        state.mkdir(parents=True)
        with sqlite3.connect(source.as_uri() + '?mode=ro', uri=True) as src, sqlite3.connect(db) as dest:
            src.backup(dest)
            if dest.execute('PRAGMA integrity_check').fetchone()[0] != 'ok': raise SystemExit('备份完整性失败')
        marker.write_text(json.dumps({'source_sha256': hashlib.sha256(source.read_bytes()).hexdigest(), 'backup_sha256': hashlib.sha256(db.read_bytes()).hexdigest(), 'note': 'source主文件哈希不代表WAL整体；backup哈希才是验收快照指纹'}))
        return
    if not marker.is_file(): raise SystemExit('缺少验收state标记，拒绝操作')
    pidfile = state / 'airtest-server.json'
    binary = Path(cfg['binary']).resolve()
    if pidfile.exists():
        saved = json.loads(pidfile.read_text())
        pid = saved['pid']
        live = subprocess.run(['ps', '-p', str(pid), '-o', 'command='], capture_output=True, text=True).stdout
        if live.strip():
            if str(binary) not in live or str(state) not in live: raise SystemExit('PID身份不匹配，拒绝停止')
            os.kill(pid, signal.SIGTERM)
            for _ in range(100):
                time.sleep(.1)
                status = subprocess.run(['ps', '-p', str(pid), '-o', 'stat='], capture_output=True, text=True).stdout.strip()
                if not status or status.startswith('Z'): break
            else: raise SystemExit('服务未退出，拒绝启动第二实例')
        pidfile.unlink()
    if args.action == 'stop': return
    tz = dt.timezone(dt.timedelta(hours=8))
    anchor = dt.datetime.fromisoformat(cfg['clock_start']).astimezone(tz)
    clockfile = state / 'airtest-clock.json'
    if clockfile.exists(): anchor = dt.datetime.fromisoformat(json.loads(clockfile.read_text())['clock'])
    if args.action in ('morning','noon','evening','night'):
        hour,minute={'morning':(8,15),'noon':(12,45),'evening':(18,45),'night':(22,15)}[args.action]
        candidate=anchor.replace(hour=hour,minute=minute)
        anchor=candidate if candidate>anchor else candidate+dt.timedelta(days=1)
    elif args.action in ('off_window','after_window'):
        candidate=anchor.replace(hour=23,minute=0)
        anchor=candidate if candidate>anchor else candidate+dt.timedelta(days=1)
    elif args.action == 'next_day_open_window': anchor = (anchor + dt.timedelta(days=1)).replace(hour=12, minute=45)
    elif args.action == 'next_month':
        anchor = (anchor.replace(day=28) + dt.timedelta(days=4)).replace(day=1, hour=12, minute=45)
    elif args.action == 'next_window':
        windows = [(8,15),(12,45),(18,45),(22,15)]
        future = [anchor.replace(hour=h, minute=m) for h,m in windows if (h,m) > (anchor.hour,anchor.minute)]
        anchor = future[0] if future else (anchor + dt.timedelta(days=1)).replace(hour=8, minute=15)
    elif args.action == 'open_window': anchor = anchor.replace(hour=12, minute=45)
    # restart 按已记录的偏移继续走钟；不回拨冻结到旧时刻。
    offset = (anchor - dt.datetime.now(tz)).total_seconds()
    if args.action == 'restart' and clockfile.exists(): offset = json.loads(clockfile.read_text())['offset']
    clockfile.write_text(json.dumps({'clock': anchor.isoformat(), 'offset': offset}))
    argv = [str(binary), *cfg.get('server_args', []), '-state', str(state), '-addr', cfg['addr'], '-server-host', cfg['server_host'],
            '-reqlog', cfg['requests'], '-event-log', str(state/'events.ndjson'),
            '-assets', cfg['assets'], '-cards', cfg['cards'], '-guild-clock-offset', str(int(offset))+'s',
            '-guild-solo-daily-refill', '-guild-solo-season-rewards']
    with (state/'server.log').open('ab') as log:
        proc = subprocess.Popen(argv, stdout=log, stderr=log, start_new_session=True)
    pidfile.write_text(json.dumps({'pid': proc.pid, 'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(), 'argv': argv}))
    host, port = cfg['addr'].rsplit(':', 1)
    if host in ('0.0.0.0', ''):
        host = '127.0.0.1'
    elif host == '[::]':
        host = '[::1]'
    health = f'http://{host}:{port}/mercd/health'
    opener = build_opener(ProxyHandler({}))
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            pidfile.unlink()
            raise SystemExit(f'服务启动失败，查看 {state / "server.log"}')
        try:
            with opener.open(health, timeout=1) as response:
                info = json.load(response)
                if info.get('service') == 'mercd35' and info.get('state_dir') == str(state):
                    return
        except (OSError, ValueError):
            pass
        time.sleep(.25)
    proc.terminate()
    raise SystemExit(f'服务在 90 秒内未就绪，查看 {state / "server.log"}')


if __name__ == '__main__': main()
