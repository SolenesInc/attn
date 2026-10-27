#!/usr/bin/env python3
"""Measure Notebook sync on an isolated Mac home and same-named Linux outpost."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import time
import uuid


def run(command, **kwargs):
    result = subprocess.run(command, text=True, capture_output=True, **kwargs)
    if result.returncode:
        raise RuntimeError(f"{command[0]}: {result.stdout}{result.stderr}")
    return result.stdout.strip()


def manifest(root):
    result = {}
    for directory, dirs, files in os.walk(root):
        if Path(directory) == root:
            dirs[:] = [name for name in dirs if name != '.attn']
        for name in files:
            if name == '.DS_Store' or name.startswith('.seed-transfer-') or (name.startswith('.') and '.tmp.' in name):
                continue
            path = Path(directory) / name
            if path.is_symlink():
                digest = 'link:' + os.readlink(path)
            else:
                with path.open('rb') as stream:
                    digest = hashlib.file_digest(stream, 'sha256').hexdigest()
            result[str(path.relative_to(root))] = [path.lstat().st_size, digest]
    return result


def cpu_seconds(value):
    return sum(float(part) * 60 ** index for index, part in enumerate(reversed(value.strip().split(':'))))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--attn', required=True, type=Path)
    parser.add_argument('--linux-attn', required=True, type=Path)
    parser.add_argument('--probe', required=True, type=Path, help='go build ./scripts/verify-remote-shell')
    parser.add_argument('--notebook', required=True, type=Path, help='read-only source; copied with cp -cR')
    parser.add_argument('--remote', default='ai-sandbox')
    parser.add_argument('--address', required=True, help='dedicated non-production listener, never production')
    parser.add_argument('--receipts', required=True, type=Path)
    parser.add_argument('--max-bytes', type=int, help='select whole files from the copy up to this size for a bounded relay run')
    parser.add_argument('--idle-seconds', type=int, default=30)
    args = parser.parse_args()
    for field in ('attn', 'linux_attn', 'probe', 'notebook', 'receipts'):
        setattr(args, field, getattr(args, field).resolve())
    args.receipts.mkdir(parents=True, exist_ok=False)
    instance = 'nbs-' + uuid.uuid4().hex[:8]
    env = {key: value for key, value in os.environ.items() if not key.startswith('ATTN_')}
    env |= {'ATTN_INSTANCE': instance, 'ATTN_HEADLESS_TASKS': 'off', 'ATTN_GITHUB_POLLING': 'off', 'ATTN_HARNESS_SKILL_SYNC': 'off'}
    source_root = args.receipts / 'notebook-copy'
    run(['cp', '-cR', str(args.notebook), str(source_root)])
    if args.max_bytes is not None:
        total = 0
        for directory, dirs, files in os.walk(source_root):
            if Path(directory) == source_root:
                dirs[:] = [name for name in dirs if name != '.attn']
            for name in sorted(files):
                path = Path(directory) / name
                size = path.lstat().st_size
                if total + size > args.max_bytes:
                    path.unlink()
                else:
                    total += size
    expected = manifest(source_root)
    remote_dir = ''
    remote_started = False
    home = None
    data = None
    log = None
    record = None

    def ssh(script):
        return run(['ssh', args.remote, 'bash', '-s'], input='set -eu\n' + script + '\n')

    def remote_cli(*values):
        return ssh('env ATTN_INSTANCE=' + shlex.quote(instance) + ' ' + ' '.join(shlex.quote(str(value)) for value in [record['binary'], *values]))

    def sample(pid, remote=False):
        command = ['ps', '-p', str(pid), '-o', 'time=,rss=']
        value = ssh(' '.join(shlex.quote(value) for value in command)) if remote else run(command)
        elapsed, rss = value.split()
        return {'cpu_seconds': cpu_seconds(elapsed), 'rss_bytes': int(rss) * 1024}

    try:
        remote_dir = ssh('mktemp -d /tmp/attn-notebook-proof.XXXXXX')
        helper = Path(__file__).with_name('outpost-instance.py')
        run(['scp', str(args.linux_attn), str(helper), args.remote + ':' + remote_dir + '/'])
        remote_build = json.loads(ssh(shlex.quote(remote_dir + '/' + args.linux_attn.name) + ' --build-info-json'))
        home_build = json.loads(run([str(args.attn), '--build-info-json'], env=env))
        if home_build['sourceFingerprint'] != remote_build['sourceFingerprint']:
            raise RuntimeError(f'build mismatch: {home_build} {remote_build}')
        (args.receipts / 'build.json').write_text(json.dumps(home_build, indent=2) + '\n')
        record = json.loads(ssh('python3 ' + shlex.quote(remote_dir + '/outpost-instance.py') + ' start --instance ' + instance + ' --binary ' + shlex.quote(remote_dir + '/' + args.linux_attn.name) + ' --listen ' + shlex.quote(args.address)))
        remote_started = True
        data = run([str(args.attn), 'instance', 'resolve', '--field', 'dataDir'], env=env)
        if Path(data).exists():
            raise RuntimeError('refusing existing home data: ' + data)
        ready, writer = os.pipe()
        log = (args.receipts / 'home.stderr').open('w')
        home = subprocess.Popen([str(args.attn), 'daemon'], env=env | {'ATTN_DAEMON_READY_FD': str(writer)}, stdout=log, stderr=log, pass_fds=(writer,))
        os.close(writer)
        with os.fdopen(ready) as stream:
            if stream.readline().strip() != 'ready':
                raise RuntimeError('home did not signal readiness')
        run([str(args.attn), 'settings', 'set', 'notebook.root', str(source_root)], env=env)
        run([str(args.attn), 'settings', 'set', 'outposts_enabled', 'true'], env=env)
        port = run([str(args.attn), 'instance', 'resolve', '--field', 'wsPort'], env=env)
        route = ssh('tailscale ping --until-direct=false -c 5 100.127.148.75')
        (args.receipts / 'route.txt').write_text(route + '\n')
        command = [str(args.probe), '--url', 'ws://127.0.0.1:' + port + '/ws', '--data', data, '--address', args.address, '--cwd', remote_dir, '--name', args.remote, '--mode', 'notebook', '--samples', '100', '--notebook-events', str(args.receipts / 'daemon-events.json')]
        with (args.receipts / 'sync.log').open('w') as output:
            result = subprocess.run(command, env=env, stdout=output, stderr=subprocess.STDOUT)
        if result.returncode:
            raise RuntimeError((args.receipts / 'sync.log').read_text())
        manifest_source = Path(__file__).read_text().split('def manifest(root):\n', 1)[1].split('\n\ndef cpu_seconds', 1)[0]
        remote_manifest = 'import os,hashlib,json\nfrom pathlib import Path\ndef manifest(root):\n' + manifest_source + '\nprint(json.dumps(manifest(Path(' + repr(record['notebookRoot']) + '))))\n'
        actual = json.loads(ssh("python3 - <<'MANIFEST'\n" + remote_manifest + '\nMANIFEST'))
        if actual != expected:
            raise RuntimeError(f'Notebook manifest differs: missing={sorted(expected.keys()-actual.keys())}; extra={sorted(actual.keys()-expected.keys())}; changed={[name for name in expected.keys() & actual.keys() if expected[name] != actual[name]]}')
        remote_pid = int(ssh('cat ' + shlex.quote(record['dataDir'] + '/attn.pid')))
        before = [sample(home.pid), sample(remote_pid, True)]
        began = time.monotonic()
        time.sleep(args.idle_seconds)
        elapsed = time.monotonic() - began
        after = [sample(home.pid), sample(remote_pid, True)]
        sync = next(json.loads(line) for line in (args.receipts / 'sync.log').read_text().splitlines() if line.startswith('{"bulk_window_bytes"'))
        receipt = {'instance': instance, 'listener': args.address, **sync, 'files': len(expected), 'bytes': sum(value[0] for value in expected.values()), 'manifest_equal': True, 'selected_max_bytes': args.max_bytes, 'manifest_sha256': hashlib.sha256(json.dumps(expected, sort_keys=True).encode()).hexdigest(), 'idle_interval_seconds': elapsed,
                   'idle': {name: {'cpu_percent_one_core': (end['cpu_seconds']-start['cpu_seconds']) * 100 / elapsed, 'rss_bytes': end['rss_bytes']} for name, start, end in zip(('home', 'outpost'), before, after)}}
        (args.receipts / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
        print(json.dumps(receipt, indent=2))
    finally:
        failures = []
        if home is not None:
            home.terminate(); home.wait(); log.close()
            shutil.copy2(Path(data) / 'daemon.log', args.receipts / 'home-daemon.log')
            run([str(args.attn), 'instance', 'clean', instance], env=env)
        if remote_started:
            try:
                remote_cli('enrollment', 'leave')
                remote_log = ssh('journalctl --user -u ' + shlex.quote('attn-outpost-' + instance + '.service') + ' --no-pager')
                (args.receipts / 'outpost-daemon.log').write_text(remote_log + '\n')
                ssh('python3 ' + shlex.quote(remote_dir + '/outpost-instance.py') + ' remove --instance ' + instance)
            except Exception as error:
                failures.append(str(error))
        if remote_dir and not failures:
            ssh('rm ' + shlex.quote(remote_dir + '/' + args.linux_attn.name) + ' ' + shlex.quote(remote_dir + '/outpost-instance.py') + '\nrmdir ' + shlex.quote(remote_dir))
        shutil.rmtree(source_root)
        print('cleanup:', instance, failures or 'both instances removed')
        if failures:
            raise RuntimeError('; '.join(failures))


if __name__ == '__main__':
    main()
