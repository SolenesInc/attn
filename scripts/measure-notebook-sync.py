#!/usr/bin/env python3
"""Measure native Notebook edits on a real isolated paired daemon stack."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import urllib.request


def run(command, env):
    return subprocess.run(command, env=env, text=True, capture_output=True, check=True).stdout.strip()


def free_port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        return listener.getsockname()[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--attn', required=True, type=Path)
    parser.add_argument('--wsctl', required=True, type=Path)
    parser.add_argument('--notebook', required=True, type=Path)
    parser.add_argument('--receipts', required=True, type=Path)
    parser.add_argument('--seconds', type=int, default=600, help='native-edit window; zero measures first sync only')
    parser.add_argument('--interval', type=float, default=1)
    args = parser.parse_args()
    args.attn, args.wsctl = args.attn.resolve(), args.wsctl.resolve()
    args.receipts = args.receipts.resolve()
    args.receipts.mkdir(parents=True, exist_ok=False)
    env = {k: v for k, v in os.environ.items() if not k.startswith('ATTN_') and k != 'CODEX_THREAD_ID'}
    children, logs = [], []
    events, errors = [], []
    changed = threading.Condition()
    latest = None
    reader = None
    profile = None
    with tempfile.TemporaryDirectory(prefix='nbs-perf-', dir='/tmp') as temporary:
        root = Path(temporary).resolve()
        env['SSH_AUTH_SOCK'] = str(root / 'agent.sock')
        env['ATTN_INSTANCE'] = 'nb-perf'
        env['ATTN_MOCK_GH_URL'] = 'http://127.0.0.1:1'
        try:
            agent = subprocess.Popen(['ssh-agent', '-D', '-a', env['SSH_AUTH_SOCK']], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            children.append(agent)
            if not agent.stdout.readline(): raise RuntimeError('ssh-agent did not signal readiness')
            key = root / 'key'
            run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(key)], env)
            run(['ssh-add', str(key)], env)
            environments, endpoints, daemons = {}, {}, {}
            for name in ('home', 'outpost'):
                data = root / name; data.mkdir(mode=0o700)
                if name == 'home':
                    run(['cp', '-cR', str(args.notebook.resolve()), str(data / 'notebook')], env)
                    journal = data / 'notebook' / 'journal' / 'sync-perf.md'
                    journal.parent.mkdir(exist_ok=True); journal.write_text('Notebook sync workload\n')
                diagnostic, websocket = free_port(), free_port()
                child_env = env | {'ATTN_DATA_DIR':str(data),'ATTN_HARNESS_DATA_DIR':str(data), 'ATTN_HARNESS_NOTEBOOK_ROOT':str(data/'notebook'), 'ATTN_WS_PORT':str(websocket), 'ATTN_WS_URL':f'ws://127.0.0.1:{websocket}/ws', 'ATTN_AUTHORIZED_KEYS_PATH':str(key)+'.pub', 'ATTN_HEADLESS_TASKS':'off','ATTN_GITHUB_POLLING':'off','ATTN_HARNESS_SKILL_SYNC':'off','ATTN_PTY_BACKEND':'embedded','ATTN_CACHED_SHELL_ENV':'[]','ATTN_PPROF':str(diagnostic)}
                ready, writer = os.pipe()
                log = (args.receipts / (name+'.stderr')).open('w'); logs.append(log)
                daemon = subprocess.Popen([str(args.attn),'daemon'],env=child_env|{'ATTN_DAEMON_READY_FD':str(writer)},stdout=log,stderr=log,pass_fds=(writer,))
                children.append(daemon); daemons[name]=daemon
                os.close(writer)
                with os.fdopen(ready) as stream:
                    if stream.readline().strip() != 'ready': raise RuntimeError(name+' did not signal readiness')
                environments[name]=child_env; endpoints[name]=f'http://127.0.0.1:{diagnostic}'
                print(json.dumps({'ready':name,'pid':daemon.pid}),flush=True)
            def sample(name):
                with urllib.request.urlopen(endpoints[name]+'/debug/vars') as response: return json.load(response)
            if 'notebook_engine_events' not in sample('home'): raise RuntimeError('requires the counter overlay from build-notebook-sync-perf.py')
            home, outpost = environments['home'], environments['outpost']
            address = '127.0.0.1:'+str(free_port())
            run([str(args.attn),'settings','set','outpost_listen',address],outpost)
            run([str(args.attn),'settings','set','outposts_enabled','true'],home)
            watch = subprocess.Popen([str(args.wsctl),'outposts','--watch'],env=home,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            children.append(watch)
            def consume():
                nonlocal latest
                try:
                    for line in watch.stdout:
                        event=json.loads(line)
                        with changed:
                            events.append({'seconds':time.monotonic(),'event':event})
                            if event['outposts']: latest=event['outposts'][0]
                            changed.notify_all()
                except Exception as error:
                    errors.append(str(error))
                finally:
                    with changed: errors.append('observer ended');changed.notify_all()
            reader=threading.Thread(target=consume);reader.start()
            def await_state(predicate):
                with changed:
                    while not predicate(latest):
                        if errors: raise RuntimeError(errors)
                        if latest and latest.get('notebook',{}).get('state')=='error': raise RuntimeError(latest['notebook'])
                        changed.wait()
            sync_before=sample('home'); started=time.monotonic()
            run([str(args.wsctl),'outposts','--add','perf','--address',address,'--until','connected'],home)
            await_state(lambda row: row and row.get('notebook',{}).get('state')=='in_sync')
            synced=time.monotonic();sync_after=sample('home')
            print(json.dumps({'first_sync_seconds':synced-started,'home':sync_after}),flush=True)
            def capture_profile():
                try:
                    with urllib.request.urlopen(endpoints['home']+'/debug/pprof/profile?seconds=30') as response:
                        (args.receipts/'home.cpu.pprof').write_bytes(response.read())
                except Exception as error: errors.append('profile: '+str(error))
            if args.seconds >= 30:
                profile=threading.Thread(target=capture_profile);profile.start()
            before={name:sample(name) for name in environments}; began=time.monotonic(); next_report=60; windows=[]
            writes=0; previous=before['home']; previous_at=began
            while time.monotonic()-began < args.seconds:
                with journal.open('a') as stream:
                    stream.write(f'Native append {writes}\n');stream.flush();os.fsync(stream.fileno())
                writes+=1
                elapsed=time.monotonic()-began
                if elapsed>=next_report:
                    current=sample('home');now=time.monotonic()
                    window={'seconds':now-began,'cpu_percent':(current['cpu_time_ms']-previous['cpu_time_ms'])/(now-previous_at)/10,'walks':current['notebook_conflict_walks']-previous['notebook_conflict_walks'],'engine_events':current['notebook_engine_events']-previous['notebook_engine_events'],'broadcasts':current['notebook_broadcasts']-previous['notebook_broadcasts']}
                    windows.append(window);print(json.dumps(window),flush=True);previous=current;previous_at=now;next_report+=60
                threading.Event().wait(max(0,began+writes*args.interval-time.monotonic()))
            elapsed=time.monotonic()-began;after={name:sample(name) for name in environments}
            expected=hashlib.sha256(journal.read_bytes()).hexdigest()
            def received(row):
                path=root/'outpost/notebook/journal/sync-perf.md'
                return row and row.get('notebook',{}).get('state')=='in_sync' and path.exists() and hashlib.sha256(path.read_bytes()).hexdigest()==expected
            await_state(received)
            first_events=[event for event in events if started<=event['seconds']<=synced]
            bins={}
            for event in first_events:
                second=int(event['seconds']-started);bins[second]=bins.get(second,0)+1
            result={'seconds':elapsed,'native_journal_appends':writes,'interval':args.interval,'writes_arrived':True,'first_sync':{'seconds':synced-started,'home_cpu_ms':sync_after['cpu_time_ms']-sync_before['cpu_time_ms'],'engine_events':sync_after['notebook_engine_events']-sync_before['notebook_engine_events'],'broadcasts':len(first_events),'peak_broadcasts_per_second':max(bins.values(),default=0)},'before':before,'after':after,'minute_windows':windows,'home_cpu_percent':(after['home']['cpu_time_ms']-before['home']['cpu_time_ms'])/elapsed/10}
            (args.receipts/'receipt.json').write_text(json.dumps(result,indent=2)+'\n')
            (args.receipts/'events.json').write_text(json.dumps(events,indent=2)+'\n')
            print(json.dumps(result),flush=True)
        finally:
            for child in reversed(children):
                if child.poll() is None: child.terminate()
                child.wait()
            if reader:reader.join()
            if profile:profile.join()
            for name in ('home','outpost'):
                log=root/name/'daemon.log'
                if log.exists():(args.receipts/(name+'-daemon.log')).write_bytes(log.read_bytes())
            for log in logs:log.close()


if __name__=='__main__':main()
