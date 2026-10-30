#!/usr/bin/env python3
"""Summarize one outpost measurement.

Window: the driver's measurement window (bulk: sampling start to in_sync; idle: the keystroke run).
Direct per-call R: the daemon's input-write duration, grouped by runtime, receive time and duration.
Each input in one write batch shares those values. Trace ids join writes to the measurement window.
The legacy echo estimator keeps the minimum latency of each multikey batch arriving within 8 ms.
Control: each route's samples sent within the same epoch seconds.
MB/s: outpost Notebook bytes / (in_sync - sampling start), as A21's direct receipt computed 17.42 MB/s; also
from the first positive need_bytes event.
CPU: per process, delta cumulative CPU / delta wall time inside the window, mean and max one-second interval.
"""
import argparse, json
from pathlib import Path

p = argparse.ArgumentParser(); p.add_argument('case', type=Path)
p.add_argument('--until-s', type=float, help='cut the window this many seconds after it starts (to exclude a route outage); MB/s then comes from need_bytes')
a = p.parse_args()
s = json.loads((a.case / 'samples.json').read_text())
r = next(x for x in json.loads((a.case / 'receipts.json').read_text()) if x['what'] == 'result')
w = dict(r['window'])
if a.until_s is not None:
    w['cut_ms'] = w['from_ms'] + a.until_s * 1000; w['to_ms'] = min(w['to_ms'] or w['cut_ms'], w['cut_ms'])
pts = sorted((x['t_ms'] + x['ms'], x['t_ms'], x['ms']) for x in s['samples'] if x['t_ms'] >= w['from_ms'] and (w['to_ms'] is None or x['t_ms'] <= w['to_ms']))


def quant(v):
    v = sorted(v); n = len(v)
    return {'samples': n, 'p50_ms': v[n // 2], 'p90_ms': v[min(n - 1, int(.9 * n))], 'p99_ms': v[min(n - 1, int(.99 * n))], 'max_ms': v[-1]} if n else {'samples': 0}


batches = []
for point in pts:
    if not batches or point[0] - batches[-1][-1][0] > 8: batches.append([])
    batches[-1].append(point)
start = (s['t0_epoch_ms'] + w['from_ms']) / 1000
end = (s['t0_epoch_ms'] + (w['to_ms'] or max(x[1] for x in pts))) / 1000
c = json.loads((a.case / 'control-echo.json').read_text())
controls = {name: {**quant([x[1] for x in route['samples'] if start <= x[0] <= end]), 'unanswered': route['unanswered']} for name, route in c['routes'].items()}

direct_r = None
trace_path = a.case / 'input-traces.json'
if trace_path.exists():
    traces = json.loads(trace_path.read_text())
    probes = {x['probe_id']: x for x in traces['probes']}
    ids = {f"c12-input-{x['i']}" for x in s['samples'] if x['t_ms'] >= w['from_ms'] and (w['to_ms'] is None or x['t_ms'] <= w['to_ms'])}
    calls = {}
    for trace in traces['traces']:
        probe = probes[trace['trace_id']]
        if not probe['success'] or probe['write_duration_us'] != trace['write_duration_us']:
            raise ValueError(f"input duration mismatch for {trace['trace_id']}")
        if trace['trace_id'] in ids:
            key = (trace['runtime_id'], trace['received_at_unix_ms'], trace['write_duration_us'])
            calls[key] = trace['write_duration_us'] / 1000
    direct_r = quant(list(calls.values()))

files_bytes = r['notebook_files']['outpost_files_and_bytes'].split('\n')
nb_bytes = int(files_bytes[1]) if len(files_bytes) > 1 else None
sync = None
if a.until_s is not None:
    ev = [(e['t_ms'], e['nb'].get('need_bytes')) for e in r['notebook_events'] if e['nb'].get('need_bytes') is not None]
    last = max((x for x in ev if x[0] <= w['to_ms']), key=lambda x: x[0])
    sync = {'partial': True, 'bytes_moved': nb_bytes - last[1], 'between_ms': [w['from_ms'], last[0]], 'MBps_from_need_bytes': round((nb_bytes - last[1]) / (last[0] - w['from_ms']) / 1000, 3)}
elif w.get('synced_ms') and nb_bytes and nb_bytes > 10_000:
    first = next((e['t_ms'] for e in r['notebook_events'] if (e['nb'].get('need_bytes') or 0) > 0), None)
    sync = {'bytes': nb_bytes, 'files': int(files_bytes[0]), 'seconds_from_sampling': (w['synced_ms'] - w['from_ms']) / 1000,
            'MBps_from_sampling': round(nb_bytes / (w['synced_ms'] - w['from_ms']) / 1000, 3),
            'MBps_from_first_need': round(nb_bytes / (w['synced_ms'] - first) / 1000, 3) if first is not None else None}

cpu = {}
rows = [json.loads(l) for l in (a.case / 'cpu.jsonl').read_text().split('\n') if l.strip()] if (a.case / 'cpu.jsonl').exists() else []
rows = [x for x in rows if start - 1 <= x['t'] <= end + 1]
for prev, cur in zip(rows, rows[1:]):
    dt = cur['t'] - prev['t']
    for label, proc in cur['procs'].items():
        old = prev['procs'].get(label)
        if old and old['pid'] == proc['pid'] and dt > 0:
            cpu.setdefault(label, {'pid': proc['pid'], 'pct': []})['pct'].append(100 * (proc['cpu_s'] - old['cpu_s']) / dt)
cpu = {label: {'pid': v['pid'], 'mean_pct': round(sum(v['pct']) / len(v['pct']), 1), 'max_1s_pct': round(max(v['pct']), 1), 'intervals': len(v['pct'])} for label, v in cpu.items() if v['pct']}

result = {'case': a.case.name, 'mode': r['mode'], 'window': w, 'window_epoch_s': [start, end], 'raw_echo': quant([x[2] for x in pts]), 'bridge_unanswered': r['unanswered'],
          'batch_count': len(batches), 'multikey_batches': sum(len(b) > 1 for b in batches), 'per_call_R': quant([min(x[2] for x in b) for b in batches if len(b) > 1]),
          'per_call_R_direct': direct_r,
          'control_same_seconds': controls, 'notebook_sync': sync, 'cpu': cpu, 'heartbeat_gaps': r['heartbeat_gaps'], 'notebook_files': r['notebook_files'], 'connected_ms': r.get('connected_ms')}
print(json.dumps(result, indent=2))
