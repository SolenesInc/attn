#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import uuid

parser = argparse.ArgumentParser(description="Measure duplex bulk and real PTY echo on an isolated outpost bridge.")
parser.add_argument("--probes", required=True, type=Path, help="Mac probe-262144, probe-524288 and probe-1048576 binaries")
parser.add_argument("--remote-dir", required=True, help="owned ai-sandbox directory containing the same Linux probes and attn-current")
parser.add_argument("--remote", default="ai-sandbox")
parser.add_argument("--address", required=True)
parser.add_argument("--bytes", type=int, default=1_000_000_000)
parser.add_argument("--route", choices=["direct", "derp"], default="direct")
parser.add_argument("--windows", nargs="+", choices=["ssh-only", "1048576", "524288", "262144"], default=["ssh-only", "1048576", "524288", "262144"])
parser.add_argument("--receipts", required=True, type=Path)
args = parser.parse_args()
args.receipts.mkdir(parents=True, exist_ok=True)
args.probes = args.probes.resolve()


def run(command, **kwargs):
    return subprocess.run(command, text=True, capture_output=True, check=True, **kwargs).stdout.strip()


def ssh(script):
    return run(["ssh", "-o", "ControlPath=none", args.remote, "bash", "-s"], input="set -eu\n" + script + "\n")


for label in args.windows:
    raw = label == "ssh-only"
    window = 1048576 if raw else int(label)
    instance = "bbulk-" + uuid.uuid4().hex[:8]
    remote_env = "env -u ATTN_DATA_DIR -u ATTN_DB_PATH -u ATTN_SOCKET_PATH -u ATTN_CONFIG_PATH -u ATTN_PLUGIN_DIR -u ATTN_WS_PORT -u ATTN_HOST ATTN_INSTANCE=" + instance + " ATTN_HEADLESS_TASKS=off ATTN_GITHUB_POLLING=off ATTN_HARNESS_SKILL_SYNC=off"
    remote_attn = args.remote_dir + "/attn-current"

    def remote_cli(*values):
        command = " ".join(shlex.quote(value) for value in [remote_attn, *values])
        return ssh(remote_env + " " + command)

    remote_data = remote_cli("instance", "resolve", "--field", "dataDir")
    remote_probe = args.remote_dir + "/probe-" + str(window)
    script = "printf '%s\\n' \"$BASHPID\"\nexec " + remote_env + " " + shlex.quote(remote_probe) + " --serve --address " + shlex.quote(args.address) + " --data " + shlex.quote(remote_data) + " --keys \"$HOME/.ssh/authorized_keys\"\n"
    remote_pid = None
    with (args.receipts / (label + "-server.log")).open("w") as server_log:
        remote = subprocess.Popen(["ssh", "-o", "ControlPath=none", args.remote, "bash", "-s"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=server_log, text=True)
        try:
            remote.stdin.write("set -eu\n" + script)
            remote.stdin.close()
            remote_pid = int(remote.stdout.readline().strip())
            (args.receipts / (label + "-instance.json")).write_text(json.dumps({"instance": instance, "remote_pid": remote_pid, "address": args.address, "remote_data": remote_data}) + "\n")
            ready = remote.stdout.readline().strip()
            if not ready.startswith("ready "):
                raise RuntimeError("probe failed to start: " + ready)
            route = ssh("tailscale ping " + ("--until-direct=false " if args.route == "derp" else "") + "-c 5 100.127.148.75")
            if ("via DERP" in route.splitlines()[-1]) != (args.route == "derp"):
                raise RuntimeError("unexpected route: " + route)
            (args.receipts / (label + "-route.txt")).write_text(route + "\n")
            print(label, instance, ready, route, flush=True)
            env = {k: v for k, v in os.environ.items() if not k.startswith("ATTN_")}
            env |= {"ATTN_INSTANCE": instance, "ATTN_HEADLESS_TASKS": "off"}
            with tempfile.TemporaryDirectory(prefix="bridge-bulk-") as home:
                env["ATTN_DATA_DIR"] = home
                command = [str(args.probes / ("probe-" + str(window))), "--address", args.address, "--data", home, "--bytes", str(args.bytes)]
                if raw:
                    command += ["--ssh-only"]
                client_log = args.receipts / (label + ".log")
                with client_log.open("w") as output:
                    result = subprocess.run(command, env=env, text=True, stdout=output, stderr=subprocess.STDOUT)
                print(client_log.read_text(), flush=True)
                result.check_returncode()
        finally:
            if remote_pid is not None and remote.poll() is None:
                ssh("kill -TERM " + str(remote_pid))
            remote.wait()
            print(remote_cli("instance", "clean", instance), flush=True)
