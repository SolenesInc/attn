#!/usr/bin/env python3
"""Run the SSH bridge stack checks and record the host folders they must not change."""
import argparse
import json
import os
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out', required=True, type=Path)
args = parser.parse_args()
args.out.mkdir(parents=True, exist_ok=True)
paths = [Path.home() / '.local/bin', Path('/tmp') / f'attn-{os.getuid()}']

def listing():
    result = {}
    for root in paths:
        if not root.exists():
            result[str(root)] = None
            continue
        result[str(root)] = [{
            'name': item.name, 'mode': oct(item.lstat().st_mode),
            'size': item.lstat().st_size, 'mtime_ns': item.lstat().st_mtime_ns,
            'link': os.readlink(item) if item.is_symlink() else None,
        } for item in sorted(root.iterdir())]
    return result

before = listing()
(args.out / 'isolation-before.json').write_text(json.dumps(before, indent=2) + '\n')
command = ['go', 'test', './cmd/attn', '-run', 'TestSSH|TestBridgeExits|TestLegacyBridgeAddress|TestOutpostUpdateNames', '-count=1', '-v']
with (args.out / 'stack.log').open('w') as output:
    result = subprocess.run(command, stdout=output, stderr=subprocess.STDOUT)
after = listing()
(args.out / 'isolation-after.json').write_text(json.dumps(after, indent=2) + '\n')
receipt = {'command': command, 'exit': result.returncode, 'unchanged': before == after, 'before': before, 'after': after}
(args.out / 'stack-receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
print((args.out / 'stack.log').read_text(), end='')
print(json.dumps({'exit': result.returncode, 'host_folders_unchanged': before == after, 'receipt': str(args.out / 'stack-receipt.json')}))
raise SystemExit(result.returncode or (0 if before == after else 1))
