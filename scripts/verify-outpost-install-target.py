#!/usr/bin/env python3
import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import socket
import subprocess
import tempfile
import uuid

parser = argparse.ArgumentParser(description="Prove a named outpost refuses the default binary path inside an isolated Linux mount namespace.")
parser.add_argument("--attn", required=True, type=Path)
parser.add_argument("--probe", required=True, type=Path)
args = parser.parse_args()
children = []


def run(command, env):
    result = subprocess.run([str(x) for x in command], env=env, text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError(result.stderr or result.stdout)
    return result.stdout.strip()


with tempfile.TemporaryDirectory(prefix="attn-install-target-", dir="/tmp") as temporary:
    root = Path(temporary)
    actual_home = Path.home()
    masked_home = root / "home"
    target = masked_home / ".local/bin/attn"
    target.parent.mkdir(parents=True)
    target.write_bytes(args.attn.resolve().read_bytes())
    target.chmod(0o755)
    before = hashlib.sha256(target.read_bytes()).hexdigest()
    env = {k: v for k, v in os.environ.items() if not k.startswith("ATTN_")}
    env["SSH_AUTH_SOCK"] = str(root / "agent.sock")
    listener = socket.socket()
    listener.bind(("127.0.0.1", 0))
    address = f"127.0.0.1:{listener.getsockname()[1]}"
    listener.close()
    try:
        agent = subprocess.Popen(["ssh-agent", "-D", "-a", env["SSH_AUTH_SOCK"]], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        children.append(agent)
        if not agent.stdout.readline():
            raise RuntimeError("ssh-agent did not start")
        key = root / "key"
        run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key], env)
        run(["ssh-add", key], env)
        child_env = env | {
            "ATTN_DAEMON_READY_FD": "3", "ATTN_INSTANCE": "guard-wcd4g4", "INVOCATION_ID": "isolated-test-supervisor",
            "ATTN_AUTHORIZED_KEYS_PATH": str(key) + ".pub", "ATTN_PTY_BACKEND": "worker",
            "ATTN_PTY_SKIP_STARTUP_PROBE": "1", "ATTN_HEADLESS_TASKS": "off",
            "ATTN_GITHUB_POLLING": "off", "ATTN_HARNESS_SKILL_SYNC": "off",
        }
        visible = str(actual_home / ".local/bin/attn")
        ready = root / "ready"
        os.mkfifo(ready)
        quote = shlex.quote
        script = f"""set -eu
mount --bind {quote(str(masked_home))} {quote(str(actual_home))}
{quote(visible)} daemon 3>{quote(str(ready))} >{quote(str(root / 'daemon.stderr'))} 2>&1 &
daemon=$!
trap 'kill -TERM "$daemon" 2>/dev/null || true; wait "$daemon" || true' EXIT
read -r state <{quote(str(ready))}
test "$state" = ready
{quote(visible)} settings set outpost_listen {quote(address)}
echo listener-ready
read -r finish
"""
        daemon = subprocess.Popen(["unshare", "-Ur", "-m", "bash", "-c", script], env=child_env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        children.append(daemon)
        for line in daemon.stdout:
            if line.strip() == "listener-ready":
                break
        else:
            raise RuntimeError("isolated outpost did not start: " + daemon.stderr.read() + (root / "daemon.stderr").read_text())
        info = json.loads(run([args.attn.resolve(), "--build-info-json"], env))
        identity = root / "identity.json"
        identity.write_text(json.dumps({"daemon_id": "d-" + uuid.uuid4().hex, "instance": "guard-wcd4g4", "build": {"version": "different-home", "source_fingerprint": info["sourceFingerprint"], "protocol_version": run([args.attn.resolve(), "--protocol-version"], env)}}))
        result = subprocess.run([str(args.probe.resolve()), "--address", address, "--identity", str(identity), "--binary", str(args.attn.resolve())], env=env, text=True, capture_output=True)
        print(result.stdout, end="")
        print(result.stderr, end="")
        assert result.returncode != 0 and "guard-wcd4g4" in result.stderr and visible in result.stderr, result
        assert hashlib.sha256(target.read_bytes()).hexdigest() == before
        print(json.dumps({"result": "passed", "instance": "guard-wcd4g4", "target": visible, "binary_sha256_unchanged": before, "production_home_masked_by_mount_namespace": True}))
        daemon.stdin.write("finish\n")
        daemon.stdin.flush()
        daemon.wait()
    finally:
        for child in reversed(children):
            if child.poll() is None:
                child.terminate()
            child.wait()
