#!/usr/bin/env python3
"""Compare bridge Calls under real TCP backpressure and record loopback throughput."""
import argparse
import hashlib
import json
import platform
from pathlib import Path
import re
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out', required=True, type=Path)
parser.add_argument('--base', default='origin/main')
args = parser.parse_args()
root = Path(__file__).resolve().parent.parent
out = args.out.resolve()
out.mkdir(parents=True, exist_ok=False)


def git(*values):
    return subprocess.check_output(['git', *values], cwd=root)


def overlay(name, replacements):
    directory = out / name
    directory.mkdir()
    paths = {}
    for relative, content in replacements.items():
        target = directory / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(content)
        paths[str(root / relative)] = str(target)
    path = directory / 'overlay.json'
    path.write_text(json.dumps({'Replace': paths}, indent=2) + '\n')
    return path


def run(name, test, count, replacement=None, expect_failure=False):
    command = ['go', 'test', '-tags', 'bridge_receipts']
    if replacement:
        command += ['-overlay', str(replacement)]
    command += ['./internal/bridge', '-run', '^' + test + '$', '-v', '-count', str(count)]
    log = out / (name + '.txt')
    with log.open('w') as stream:
        result = subprocess.run(command, cwd=root, stdout=stream, stderr=subprocess.STDOUT)
    records.append({'name': name, 'command': command, 'exit': result.returncode, 'log': log.name})
    print(name, 'exit=' + str(result.returncode), flush=True)
    for line in log.read_text().splitlines():
        if 'echo_max=' in line or 'MB_s_each_direction=' in line or 'peak_unsent=' in line:
            print(line.strip(), flush=True)
    if expect_failure:
        if result.returncode == 0 or 'max_unsent_bytes=' not in log.read_text():
            raise RuntimeError('baseline did not reproduce bounded-input failure: ' + str(log))
    elif result.returncode:
        raise RuntimeError('check failed: ' + str(log))


records = []
metadata = {
    'head': git('rev-parse', 'HEAD').decode().strip(),
    'base': git('rev-parse', args.base).decode().strip(),
    'diff_sha256': hashlib.sha256(git('diff', 'HEAD')).hexdigest(),
    'source_sha256': {str(path.relative_to(root)): hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted((root / 'internal/bridge').rglob('*.go'))},
}
(out / 'revision.json').write_text(json.dumps(metadata, indent=2) + '\n')
base_files = git('ls-tree', '-r', '--name-only', args.base, 'internal/bridge').decode().splitlines()
base_source = {path: git('show', args.base + ':' + path) for path in base_files if path.endswith('.go') and not path.endswith('_test.go')}
transport = base_source['internal/bridge/transport.go'].decode()
transport = transport.replace('"net"', '"net"\n\t"syscall"').replace('raw.(*net.TCPConn)', 'raw.(syscall.Conn)').replace('if !ok {', 'if !ok || raw.LocalAddr().Network() != "tcp" {')
base_source['internal/bridge/transport.go'] = transport.encode()
metadata['baseline_adapter'] = 'Use syscall.Conn for transparent TCP observation; retain baseline LOWAT and writes.'
(out / 'revision.json').write_text(json.dumps(metadata, indent=2) + '\n')
base = overlay('base', base_source)
try:
    run('base-queue', 'TestBulkKernelQueueIsBounded', 1, base, expect_failure=True)
    run('head-queue', 'TestBulkKernelQueueIsBounded', 3)
    run('base-latency', 'TestBulkKernelQueueLeavesCallsResponsive', 1, base)
    run('base-throughput', 'TestKernelBulkThroughputReceipt', 3, base)
    current = (root / 'internal/bridge/transport.go').read_text()
    if platform.system() == 'Darwin':
        start = current.index('func (p *bulkPacer) wait(')
        end = current.index('func boundUnsentBytes(', start)
        unpaced = current[:start] + 'func (p *bulkPacer) wait(ctx context.Context) error { return ctx.Err() }\n\n' + current[end:]
        unpaced = unpaced.replace('\n\t"time"', '')
        mutation = overlay('no-pacing', {'internal/bridge/transport.go': unpaced.encode()})
        run('no-pacing-queue', 'TestBulkKernelQueueIsBounded', 1, mutation, expect_failure=True)

    for chunk in (8192, 16384, 32768):
        candidate = re.sub(r'const bulkChunkBytes = .*', 'const bulkChunkBytes = ' + str(chunk), current)
        path = overlay('chunk-' + str(chunk), {'internal/bridge/transport.go': candidate.encode()})
        run(str(chunk) + '-latency', 'TestBulkKernelQueueLeavesCallsResponsive', 3, path)
        run(str(chunk) + '-throughput', 'TestKernelBulkThroughputReceipt', 3, path)
finally:
    (out / 'commands.json').write_text(json.dumps(records, indent=2) + '\n')
