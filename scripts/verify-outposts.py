#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import time

parser = argparse.ArgumentParser(description="Verify two isolated bridge daemons through settings and wsctl.")
parser.add_argument("--attn", required=True, type=Path)
parser.add_argument("--wsctl", required=True, type=Path)
args = parser.parse_args()
args.attn = args.attn.resolve()
args.wsctl = args.wsctl.resolve()
children = []
logs = []
started = time.monotonic()


def receipt(label, value):
    print(f"{time.monotonic() - started:.3f}s {label}: {value}", flush=True)


def run(command, env):
    result = subprocess.run(command, env=env, text=True, capture_output=True, check=True)
    return result.stdout.strip()


def outpost_facts(env):
    status = json.loads(run([str(args.attn), "bus", "status", "--json"], env))
    return next((row["events"] for row in status["producers"] if row["name"] == "outpost.changed"), 0)


def listener():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    sock.listen()
    return sock


with tempfile.TemporaryDirectory(prefix="attn-bridge-", dir="/tmp") as temporary:
    root = Path(temporary)
    env = {k: v for k, v in os.environ.items() if not k.startswith("ATTN_")}
    env["SSH_AUTH_SOCK"] = str(root / "agent.sock")
    try:
        agent = subprocess.Popen(["/usr/bin/ssh-agent", "-D", "-a", env["SSH_AUTH_SOCK"]], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, env=env)
        children.append(agent)
        if not agent.stdout.readline():
            raise RuntimeError("ssh-agent exited before reporting its socket")
        key = root / "key"
        run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)], env)
        run(["ssh-add", str(key)], env)
        environments = {}
        daemons = {}
        for name in ["home", "outpost"]:
            data = root / name
            data.mkdir(mode=0o700)
            (data / "authorized_keys").write_bytes(key.with_suffix(".pub").read_bytes())
            daemon_env = env | {
                "ATTN_DATA_DIR": str(data),
                "ATTN_HARNESS_DATA_DIR": str(data),
                "ATTN_HARNESS_NOTEBOOK_ROOT": str(data / "notebook"),
                "ATTN_AUTHORIZED_KEYS_PATH": str(data / "authorized_keys"),
                "ATTN_HEADLESS_TASKS": "off",
                "ATTN_PTY_BACKEND": "embedded",
                "ATTN_GITHUB_POLLING": "off",
                "ATTN_HARNESS_SKILL_SYNC": "off",
                "ATTN_CACHED_SHELL_ENV": "[]",
            }
            web = listener()
            port = web.getsockname()[1]
            daemon_env["ATTN_WS_PORT"] = str(port)
            daemon_env["ATTN_WS_URL"] = f"ws://127.0.0.1:{port}/ws"
            daemon_env["ATTN_HARNESS_WS_LISTENER_FD"] = str(web.fileno())
            ready, signal_fd = os.pipe()
            daemon_env["ATTN_DAEMON_READY_FD"] = str(signal_fd)
            log = (root / f"{name}.stderr").open("w")
            logs.append(log)
            daemon = subprocess.Popen([str(args.attn), "daemon"], env=daemon_env, stdout=log, stderr=log, pass_fds=(web.fileno(), signal_fd))
            children.append(daemon)
            os.close(signal_fd)
            web.close()
            with os.fdopen(ready) as stream:
                if stream.readline().strip() != "ready":
                    raise RuntimeError(f"{name} was not ready: {(data / 'daemon.log').read_text()}")
            environments[name] = daemon_env
            daemons[name] = daemon
            receipt(name, f"ready pid={daemon.pid} data={data}")
        for name, daemon_env in environments.items():
            if outpost_facts(daemon_env) != 0 or (root / name / "bridge_host_key").exists():
                raise RuntimeError("disabled bridge created a host key or fact")
        receipt("disabled", "no outpost.changed facts or bridge host keys on either daemon")
        bridge_socket = listener()
        address = f"127.0.0.1:{bridge_socket.getsockname()[1]}"
        bridge_socket.close()
        home, outpost = environments["home"], environments["outpost"]
        for name, key, value in [("outpost", "outpost_listen", address), ("home", "outposts_enabled", "true")]:
            receipt("settings", run([str(args.attn), "settings", "set", key, value], environments[name]))
        paired = run([str(args.wsctl), "outposts", "--add", "proof", "--address", address, "--until", "connected"], home)
        for line in paired.splitlines():
            receipt("wsctl", line)
        watch = subprocess.Popen([str(args.wsctl), "outposts", "--watch"], env=home, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
        children.append(watch)
        before_freeze = outpost_facts(home)
        transitions = []
        frozen = False
        resumed = False
        for line in watch.stdout:
            event = json.loads(line)
            receipt("wsctl", json.dumps(event))
            row = next((r for r in event["outposts"] if r["name"] == "proof"), None)
            if not row:
                continue
            if frozen and (not transitions or transitions[-1] != row["state"]):
                transitions.append(row["state"])
            if row["state"] == "connected" and not frozen:
                daemons["outpost"].send_signal(signal.SIGSTOP)
                frozen = True
                receipt("signal", "SIGSTOP outpost")
            elif row["state"] == "lost" and not resumed:
                daemons["outpost"].send_signal(signal.SIGCONT)
                resumed = True
                receipt("signal", "SIGCONT outpost")
            elif row["state"] == "connected" and resumed:
                if row.get("last_error"):
                    raise RuntimeError("successful recovery retained its last error")
                receipt("result", "connected → stalled → lost → connected; pins retained; last error cleared")
                break
        else:
            raise RuntimeError("wsctl exited before recovery")
        after_recovery = outpost_facts(home)
        if after_recovery - before_freeze != len(transitions):
            raise RuntimeError(f"outpost.changed facts {before_freeze} → {after_recovery}, transitions {transitions}")
        receipt("bus", f"outpost.changed facts {before_freeze} → {after_recovery}, one per transition {transitions}; no heartbeat facts")
        receipt("outpost state", run([str(args.attn), "settings", "get", "outpost_state"], outpost))
        receipt("outpost home", run([str(args.attn), "settings", "get", "outpost_home"], outpost))
        receipt("outpost last error", run([str(args.attn), "settings", "get", "outpost_last_error"], outpost))
    finally:
        for daemon in locals().get("daemons", {}).values():
            if daemon.poll() is None:
                daemon.send_signal(signal.SIGCONT)
        for child in reversed(children):
            if child.poll() is None:
                child.terminate()
            child.wait()
        for log in logs:
            log.close()
