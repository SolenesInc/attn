#!/usr/bin/env python3
"""Measure an isolated daemon and successful hook deliveries without an outpost."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import statistics
import subprocess
import time
import urllib.request

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--out', type=Path, required=True)
p.add_argument('--fixture', type=Path, required=True)
p.add_argument('binaries', nargs='+', help='label=/absolute/path')
args = p.parse_args()
args.out = args.out.resolve()
args.out.mkdir(parents=True, exist_ok=True)
binaries = dict(item.split('=', 1) for item in args.binaries)
base = {k: v for k, v in os.environ.items() if not k.startswith('ATTN_') and k != 'GODEBUG'}
rows = {label: [] for label in binaries}
for repeat in range(3):
    for label, binary in binaries.items():
        root = args.out / (label + '-' + str(repeat))
        root.mkdir(mode=0o700)
        shutil.copytree(args.fixture, root / 'notebook')
        env = base | {'ATTN_DATA_DIR': str(root), 'ATTN_HARNESS_DATA_DIR': str(root), 'ATTN_HARNESS_NOTEBOOK_ROOT': str(root / 'notebook'),
                      'ATTN_WS_PORT': '19429', 'ATTN_PPROF': '19430', 'ATTN_HEADLESS_TASKS': 'off', 'ATTN_GITHUB_POLLING': 'off',
                      'ATTN_HARNESS_SKILL_SYNC': 'off', 'ATTN_PTY_BACKEND': 'embedded', 'ATTN_CACHED_SHELL_ENV': '[]'}
        read, write = os.pipe()
        lifeline, holder = os.pipe()
        log = (root / 'stderr.log').open('w')
        child = subprocess.Popen([binary, 'daemon'], env=env | {'ATTN_DAEMON_READY_FD': str(write), 'ATTN_HARNESS_LIFELINE_FD': str(lifeline)}, pass_fds=(write, lifeline), stdout=log, stderr=log)
        os.close(write)
        os.close(lifeline)
        try:
            with os.fdopen(read) as ready:
                if ready.readline().strip() != 'ready':
                    raise RuntimeError('daemon did not signal ready: ' + (root / 'stderr.log').read_text())
            hook = []
            for i in range(55):
                start = time.monotonic()
                call = subprocess.Popen([binary, '_hook-state', 'startup-measurement', 'working'], env=env, stdin=subprocess.DEVNULL,
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                _, status, usage = os.wait4(call.pid, 0)
                call.returncode = os.waitstatus_to_exitcode(status)
                hook.append({'cpu_ms': (usage.ru_utime + usage.ru_stime) * 1000,
                             'peak_rss_bytes': usage.ru_maxrss * (1 if platform.system() == 'Darwin' else 1024), 'exit': call.returncode})
            with urllib.request.urlopen('http://127.0.0.1:19430/debug/vars') as response:
                stats = json.load(response)
            with urllib.request.urlopen('http://127.0.0.1:19430/debug/pprof/goroutine?debug=2') as response:
                (root / 'goroutines.txt').write_bytes(response.read())
            if platform.system() == 'Linux':
                (root / 'smaps_rollup.txt').write_text(Path('/proc/' + str(child.pid) + '/smaps_rollup').read_text())
            state = subprocess.check_output([binary, 'settings', 'get', 'notebook_sync_state'], env=env, text=True).strip()
            if state != 'off' or (root / 'notebook-sync').exists() or any(row['exit'] for row in hook):
                raise RuntimeError(f'invalid no-outpost measurement: {state=}, hooks={hook}')
            rows[label].append({'daemon': stats, 'hooks': hook, 'notebook_sync_state': state, 'sync_index_exists': False})
        finally:
            child.terminate()
            status = child.wait()
            os.close(holder)
            if status not in (0, -15):
                raise RuntimeError('daemon failed during shutdown: ' + (root / 'stderr.log').read_text())
            log.close()
summary = {}
for label, samples in rows.items():
    summary[label] = {'daemon_runs': len(samples), 'hook_runs': sum(len(row['hooks']) for row in samples), 'errors': 0}
    metrics = {'daemon_rss_bytes': [row['daemon']['resident_bytes'] for row in samples], 'goroutines': [row['daemon']['goroutines'] for row in samples]}
    metrics |= {metric: [hook[metric] for row in samples for hook in row['hooks']] for metric in ('cpu_ms', 'peak_rss_bytes')}
    for metric, values in metrics.items():
        summary[label][metric] = {'median': statistics.median(values), 'min': min(values), 'max': max(values)}
fixture = {str(p.relative_to(args.fixture)): hashlib.sha256(p.read_bytes()).hexdigest() for p in args.fixture.rglob('*') if p.is_file()}
receipt = {'platform': platform.platform(), 'summary': summary, 'samples': rows, 'fixture_files': len(fixture),
           'fixture_manifest_sha256': hashlib.sha256(json.dumps(fixture, sort_keys=True).encode()).hexdigest()}
(args.out / 'idle.json').write_text(json.dumps(receipt, indent=2) + '\n')
print(json.dumps(summary, indent=2))
