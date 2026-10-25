#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import shlex
import socket
import subprocess
import uuid

parser = argparse.ArgumentParser(description="Run the remote-shell proof on an isolated Mac home and Linux outpost.")
parser.add_argument("--attn", required=True, type=Path)
parser.add_argument("--linux-attn", required=True, type=Path)
parser.add_argument("--probe", required=True, type=Path)
parser.add_argument("--remote", default="ai-sandbox")
parser.add_argument("--address", default="100.65.211.63:19428")
parser.add_argument("--route-cut", action="store_true")
parser.add_argument("--bench", action="store_true")
parser.add_argument("--force-derp", action="store_true")
parser.add_argument("--samples", default=1000, type=int)
parser.add_argument("--receipts", required=True, type=Path)
args = parser.parse_args()
for field in ["attn", "linux_attn", "probe", "receipts"]:
    setattr(args, field, getattr(args, field).resolve())
args.receipts.mkdir(parents=True, exist_ok=True)
instance = "rshell-" + uuid.uuid4().hex[:8]
env = {k: v for k, v in os.environ.items() if not k.startswith("ATTN_")}
env |= {"ATTN_INSTANCE": instance, "ATTN_HEADLESS_TASKS": "off", "ATTN_GITHUB_POLLING": "off", "ATTN_HARNESS_SKILL_SYNC": "off"}
remote_dir = ""
home = None
remote = None
remote_started = False
remote_ready = False
forced_derp = False
runtime_file = "/run/systemd/system/tailscaled.service.d/attn-remote-shell-" + instance + ".conf"
restore_unit = "attn-remote-shell-restore-" + instance
remote_env = "-u ATTN_DATA_DIR -u ATTN_DB_PATH -u ATTN_SOCKET_PATH -u ATTN_CONFIG_PATH -u ATTN_PLUGIN_DIR -u ATTN_WS_PORT -u ATTN_HOST ATTN_INSTANCE=" + shlex.quote(instance) + " ATTN_HEADLESS_TASKS=off ATTN_GITHUB_POLLING=off ATTN_HARNESS_SKILL_SYNC=off"


def run(command, **kwargs):
    result = subprocess.run(command, text=True, capture_output=True, **kwargs)
    if result.returncode:
        raise RuntimeError(str(command[0]) + ": " + result.stderr.strip())
    return result.stdout.strip()


def ssh(script):
    return run(["ssh", args.remote, "bash", "-s"], input="set -eu\n" + script + "\n")


def remote_cli(*values):
    command = " ".join(shlex.quote(str(value)) for value in [remote_dir + "/attn", *values])
    return ssh("env " + remote_env + " " + command)


def wait_for_peer():
    script = """python3 - <<'READY'
import json, subprocess
process = subprocess.Popen(['tailscale', 'debug', 'watch-ipn', '--initial-status'], stdout=subprocess.PIPE, text=True)
peer = '100.127.148.75'
def contains(value):
    if isinstance(value, str): return value == peer or value == peer + '/32'
    if isinstance(value, dict): return any(contains(v) for v in value.values())
    if isinstance(value, list): return any(contains(v) for v in value)
    return False
try:
    decoder = json.JSONDecoder()
    pending = ''
    for line in process.stdout:
        pending += line
        try: value, used = decoder.raw_decode(pending.lstrip())
        except json.JSONDecodeError: continue
        pending = pending.lstrip()[used:]
        if contains(value):
            print('Mac peer map ready')
            break
finally:
    process.terminate()
    process.wait()
READY"""
    print(ssh(script), flush=True)


def measure(mode):
    command = [str(args.probe), "--url", url, "--data", data, "--address", args.address, "--cwd", remote_dir, "--name", args.remote, "--mode", mode, "--samples", str(args.samples)]
    if mode == "scenario" and args.route_cut:
        command += ["--cut-command", str(cut_script)]
    with (args.receipts / (mode + ".log")).open("w") as output:
        result = subprocess.run(command, env=env, text=True, stdout=output, stderr=subprocess.STDOUT)
    print((args.receipts / (mode + ".log")).read_text(), flush=True)
    if result.returncode:
        raise RuntimeError(f"{mode} proof failed with exit {result.returncode}")


try:
    remote_dir = ssh("mktemp -d /tmp/attn-remote-shell.XXXXXX")
    run(["scp", str(args.linux_attn), args.remote + ":" + remote_dir + "/attn"])
    remote_build = json.loads(remote_cli("--build-info-json"))
    home_build = json.loads(run([str(args.attn), "--build-info-json"], env=env))
    if home_build["sourceFingerprint"] != remote_build["sourceFingerprint"]:
        raise RuntimeError(f"builds differ: {home_build} {remote_build}")
    (args.receipts / "build.json").write_text(json.dumps(home_build, indent=2) + "\n")
    data = run([str(args.attn), "instance", "resolve", "--field", "dataDir"], env=env)
    if Path(data).exists():
        raise RuntimeError("isolated instance already exists: " + data)
    port = run([str(args.attn), "instance", "resolve", "--field", "wsPort"], env=env)
    url = "ws://127.0.0.1:" + port + "/ws"
    ready, signal_fd = os.pipe()
    log = (args.receipts / "home.stderr").open("w")
    home = subprocess.Popen([str(args.attn), "daemon"], env=env | {"ATTN_DAEMON_READY_FD": str(signal_fd)}, stdout=log, stderr=log, pass_fds=(signal_fd,))
    os.close(signal_fd)
    with os.fdopen(ready) as stream:
        if stream.readline().strip() != "ready":
            raise RuntimeError("home did not signal ready")
    script = "set -eu\nenv " + remote_env + " ATTN_DAEMON_READY_FD=3 " + shlex.quote(remote_dir + "/attn") + " daemon 3>&1 2>" + shlex.quote(remote_dir + "/stderr") + "\n"
    remote = subprocess.Popen(["ssh", args.remote, "bash", "-s"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    remote_started = True
    remote.stdin.write(script)
    remote.stdin.close()
    if remote.stdout.readline().strip() != "ready":
        raise RuntimeError("outpost did not signal ready: " + remote.stderr.read())
    remote_ready = True
    print("ready:", instance, data, remote_dir, flush=True)
    print(remote_cli("settings", "set", "outpost_listen", args.address), flush=True)
    print(run([str(args.attn), "settings", "set", "outposts_enabled", "true"], env=env), flush=True)
    cut_script = args.receipts / "cut-route.sh"
    cut_script.write_text("#!/bin/sh\nexec ssh " + shlex.quote(args.remote) + " bash -s <<'CUT'\nset -eu\nsudo systemd-run --collect --unit=" + restore_unit + " --on-active=30s /usr/bin/systemctl start tailscaled\nsudo systemctl stop tailscaled\nCUT\n")
    cut_script.chmod(0o700)
    measure("scenario")
    if args.bench:
        direct = ssh("tailscale ping -c 5 100.127.148.75")
        (args.receipts / "direct-route.txt").write_text(direct + "\n")
        if "via DERP" in direct:
            raise RuntimeError("direct route was not established: " + direct)
        measure("direct")
    if args.force_derp:
        ssh("test ! -e " + runtime_file)
        forced_derp = True
        ssh("sudo mkdir -p /run/systemd/system/tailscaled.service.d\nprintf '[Service]\\nEnvironment=TS_DEBUG_ALWAYS_USE_DERP=true\\n' | sudo tee " + runtime_file + " >/dev/null\nsudo systemctl daemon-reload\nsudo systemctl restart tailscaled\ntailscale wait")
        wait_for_peer()
        relay = ssh("tailscale ping --until-direct=false -c 5 100.127.148.75")
        (args.receipts / "derp-route.txt").write_text(relay + "\n")
        if "via DERP" not in relay:
            raise RuntimeError("DERP was not observed: " + relay)
        measure("derp")
finally:
    cleanup_errors = []
    if forced_derp:
        try:
            ssh("sudo rm " + runtime_file + "\nsudo systemctl daemon-reload\nsudo systemctl restart tailscaled\ntailscale wait")
        except Exception as error:
            cleanup_errors.append("DERP restore: " + str(error))
    if home is not None:
        if home.poll() is None:
            home.terminate()
        home.wait()
        try:
            run([str(args.attn), "instance", "clean", instance], env=env)
        except Exception as error:
            cleanup_errors.append("home clean: " + str(error))
    if remote_dir:
        try:
            if remote_ready:
                remote_cli("enrollment", "leave")
            if remote_started:
                remote_cli("daemon", "stop")
                remote_cli("instance", "clean", instance)
            ssh("rm -f " + shlex.quote(remote_dir + "/attn") + " " + shlex.quote(remote_dir + "/stderr") + "\nrmdir " + shlex.quote(remote_dir))
        except Exception as error:
            cleanup_errors.append("outpost clean (retained " + remote_dir + "): " + str(error))
    if remote is not None:
        remote.wait()
    print("cleanup:", instance, cleanup_errors or "both instances removed; runtime DERP override restored", flush=True)
    if cleanup_errors:
        raise RuntimeError("; ".join(cleanup_errors))
