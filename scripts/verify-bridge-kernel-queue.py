#!/usr/bin/env python3
"""Compare bridge Calls under real TCP backpressure and record loopback throughput."""
import argparse
import hashlib
import json
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
        if 'echo_max=' in line or 'MB_s_each_direction=' in line:
            print(line.strip(), flush=True)
    if expect_failure:
        if result.returncode == 0 or 'input round trip=' not in log.read_text():
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
base = overlay('base', {path: git('show', args.base + ':' + path) for path in base_files if path.endswith('.go') and not path.endswith('_test.go')})
try:
    run('base-latency', 'TestBulkKernelQueueLeavesCallsResponsive', 1, base, expect_failure=True)
    run('base-throughput', 'TestKernelBulkThroughputReceipt', 3, base)
    current = (root / 'internal/bridge/transport.go').read_text()
    for chunk in (8192, 16384, 32768):
        candidate = re.sub(r'const bulkChunkBytes = .*', 'const bulkChunkBytes = ' + str(chunk), current)
        path = overlay('chunk-' + str(chunk), {'internal/bridge/transport.go': candidate.encode()})
        run(str(chunk) + '-latency', 'TestBulkKernelQueueLeavesCallsResponsive', 3, path)
        run(str(chunk) + '-throughput', 'TestKernelBulkThroughputReceipt', 3, path)
finally:
    (out / 'commands.json').write_text(json.dumps(records, indent=2) + '\n')
