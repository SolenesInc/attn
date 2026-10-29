#!/usr/bin/env python3
"""Compare process startup with 55 alternating runs per binary, using wait4 receipts."""
import argparse
import json
import os
from pathlib import Path
import platform
import statistics
import subprocess
import time

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--out', type=Path, required=True)
p.add_argument('binaries', nargs='+', help='label=/absolute/path')
args = p.parse_args()
args.out.mkdir(parents=True, exist_ok=True)
binaries = dict(item.split('=', 1) for item in args.binaries)
env = {k: v for k, v in os.environ.items() if not k.startswith('ATTN_') and k != 'GODEBUG'}
env |= {'ATTN_DATA_DIR': str(args.out.resolve() / 'unused-data'), 'ATTN_HEADLESS_TASKS': 'off', 'ATTN_GITHUB_POLLING': 'off'}
samples = {label: [] for label in binaries}
for i in range(55):
    for label in list(binaries)[::1 if i % 2 == 0 else -1]:
        start = time.monotonic()
        child = subprocess.Popen([binaries[label], '--version'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        _, status, usage = os.wait4(child.pid, 0)
        child.returncode = os.waitstatus_to_exitcode(status)
        samples[label].append({'cpu_ms': (usage.ru_utime + usage.ru_stime) * 1000, 'wall_ms': (time.monotonic() - start) * 1000,
                               'peak_rss_bytes': usage.ru_maxrss * (1 if platform.system() == 'Darwin' else 1024), 'exit': child.returncode})
summary = {}
for label, rows in samples.items():
    with (args.out / (label + '.inittrace')).open('w') as trace:
        subprocess.run([binaries[label], '--version'], env=env | {'GODEBUG': 'inittrace=1'}, stdout=subprocess.DEVNULL, stderr=trace, check=True)
    summary[label] = {'runs': len(rows), 'errors': sum(row['exit'] != 0 for row in rows),
                      'package_inits': sum(line.startswith('init ') for line in (args.out / (label + '.inittrace')).read_text().splitlines())}
    for metric in ('cpu_ms', 'wall_ms', 'peak_rss_bytes'):
        values = sorted(row[metric] for row in rows)
        summary[label][metric] = {'median': statistics.median(values), 'min': min(values), 'max': max(values), 'p05': values[2], 'p95': values[-3]}
receipt = {'platform': platform.platform(), 'command': '--version', 'binaries': binaries, 'summary': summary, 'samples': samples}
(args.out / 'startup.json').write_text(json.dumps(receipt, indent=2) + '\n')
print(json.dumps(summary, indent=2))
if any(row['errors'] for row in summary.values()):
    raise SystemExit('startup measurement had command failures')
