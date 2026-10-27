#!/usr/bin/env python3
import argparse
import functools
import http.server
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import threading
import time

parser = argparse.ArgumentParser(description="Measure a Mac home's source and local-release pushes to a same-named systemd outpost, keeping a shell alive.")
parser.add_argument("--home", required=True, type=Path)
parser.add_argument("--wsctl", required=True, type=Path)
parser.add_argument("--host", default="ai-sandbox")
parser.add_argument("--instance", required=True)
parser.add_argument("--address", default="100.65.211.63:19439")
parser.add_argument("--initial-linux", required=True, help="remote Linux binary matching --home")
parser.add_argument("--old-linux", required=True, help="remote different-build Linux binary with update support")
parser.add_argument("--release-linux", type=Path, help="local release-channel Linux asset built from the home's commit")
parser.add_argument("--repository", required=True, type=Path)
parser.add_argument("--out", required=True, type=Path)
args = parser.parse_args()
args.home, args.wsctl, args.repository, args.out = (p.resolve() for p in (args.home, args.wsctl, args.repository, args.out))
args.out.mkdir(parents=True, exist_ok=True)
children = []
logs = []
receipts = []
home_process = None
remote_ready = False
remote_binary = None
unit = f"attn-{args.instance}.service"
base = {k: v for k, v in os.environ.items() if not k.startswith("ATTN_")}
env = base | {"ATTN_INSTANCE": args.instance, "ATTN_HEADLESS_TASKS": "off", "ATTN_GITHUB_POLLING": "off", "ATTN_HARNESS_SKILL_SYNC": "off", "ATTN_PTY_BACKEND": "worker", "ATTN_CACHED_SHELL_ENV": "[]"}
quote = shlex.quote


def say(kind, **values):
    receipt = {"kind": kind, **values}
    receipts.append(receipt)
    print(json.dumps(receipt), flush=True)
    (args.out / "receipts.json").write_text(json.dumps(receipts, indent=2) + "\n")


def run(command, child_env=None, check=True, input=None):
    result = subprocess.run([str(x) for x in command], env=child_env or env, text=True, capture_output=True, input=input)
    if check and result.returncode:
        raise RuntimeError(f"{command[0]}: {result.stderr.strip() or result.stdout.strip()}")
    return result


def remote(script, check=True):
    return run(["ssh", args.host, "bash", "-s"], base, check=check, input="set -euo pipefail\n" + script).stdout.strip()


def remote_cli(*parts):
    command = " ".join(quote(str(x)) for x in [remote_binary, *parts])
    return remote(f"ATTN_INSTANCE={quote(args.instance)} {command}\n")


def wire(*parts):
    return run([args.wsctl, *parts]).stdout.strip()


def start_home(binary):
    global home_process
    ready, signal_fd = os.pipe()
    log = (args.out / "home.stderr").open("a")
    logs.append(log)
    home_process = subprocess.Popen([str(binary), "daemon"], env=env | {"ATTN_DAEMON_READY_FD": str(signal_fd)}, stdout=log, stderr=log, pass_fds=(signal_fd,))
    children.append(home_process)
    os.close(signal_fd)
    with os.fdopen(ready) as stream:
        if stream.readline().strip() != "ready":
            raise RuntimeError("home did not signal readiness: " + (args.out / "home.stderr").read_text())


def stop_home():
    if home_process and home_process.poll() is None:
        home_process.terminate()
        home_process.wait()


def old_build():
    remote(f"install {quote(args.old_linux)} {quote(remote_binary)}\nsystemctl --user restart {quote(unit)}\n")
    row = json.loads(wire("outposts", "--until", "refused").splitlines()[-1])["outposts"][0]
    say("different_build", build=row["build"])


def push(label, expect_failure=False):
    start = time.monotonic()
    process = subprocess.Popen([str(args.wsctl), "outposts", "--update", outpost_id], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    children.append(process)
    phases = []
    last = None
    for line in process.stdout:
        message = json.loads(line)
        row = next((r for r in message["outposts"] if r["id"] == outpost_id), None)
        if not row or not row.get("update"):
            continue
        last = row
        phase = row["update"]["phase"]
        if not phases or phases[-1]["phase"] != phase:
            phases.append({"phase": phase, "seconds": time.monotonic() - start})
            say("phase", case=label, **phases[-1])
    code = process.wait()
    stderr = process.stderr.read()
    if expect_failure:
        assert code and last and "staged build" in last["update"].get("outcome", ""), (last, stderr)
    else:
        assert code == 0 and last and last["state"] == "connected" and last["update"]["phase"] == "done", (last, stderr)
    restarted = next((p["seconds"] for p in phases if p["phase"] == "restarting"), None)
    finished = phases[-1]["seconds"]
    say("push", case=label, seconds=finished, restart_to_connected_seconds=finished - restarted if restarted is not None else None, phases=phases, row=last)


def shell_answer(label, first=False):
    caller_env = env | {"ATTN_TERMINAL_ID": caller_terminal, "ATTN_INSIDE_APP": "1"}
    if first:
        result = json.loads(run([args.home, "terminal", "open", "--host", "sandbox", "--cwd", remote_work, "--json", "--", "printf 'shell-pid=%s\\n' $$"], caller_env).stdout)
        terminal, session = result["terminal_id"], result["session_id"]
        run([args.home, "terminal", "wait", terminal, "--until", "prompt", "--json"], caller_env)
        return terminal, session
    answer = run([active_home, "terminal", "run", terminal_id, f"printf 'shell-pid=%s {label}\\n' $$", "--wait", "--json"], caller_env).stdout
    result = json.loads(answer)
    output = result["block"]["output"]
    pid = int(re.search(r"shell-pid=(\d+)", output)[1])
    screen = wire("screen", "--id", terminal_id)
    assert label in screen
    listing = json.loads(wire("list"))
    state = next(s["state"] for s in listing["sessions"] if s["id"] == shell_session)
    assert state == "idle", state
    screen_path = args.out / f"shell-{label}.vt"
    screen_path.write_text(screen)
    say("shell", case=label, pid=pid, state=state, screen=str(screen_path))
    return pid


try:
    resources = json.loads(run([args.home, "instance", "resolve", "--instance", args.instance]).stdout)
    data_dir = Path(resources["dataDir"])
    if data_dir.exists():
        raise RuntimeError(f"test instance already has data: {data_dir}")
    env["ATTN_WS_URL"] = f"ws://127.0.0.1:{resources['wsPort']}/ws"
    env["SSH_AUTH_SOCK"] = str(args.out / "agent.sock")
    agent_log = (args.out / "agent.log").open("w+")
    logs.append(agent_log)
    agent = subprocess.Popen(["ssh-agent", "-D", "-a", env["SSH_AUTH_SOCK"]], env=env, stdout=subprocess.PIPE, stderr=agent_log, text=True)
    children.append(agent)
    if not agent.stdout.readline():
        raise RuntimeError("ssh-agent did not start")
    key = args.out / "key"
    run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key])
    run(["ssh-add", key])
    remote_home = remote("printf '%s' \"$HOME\"\n")
    remote_binary = f"{remote_home}/.local/bin/attn-{args.instance}"
    remote_root = f"{remote_home}/.attn-{args.instance}"
    remote_work = f"{remote_root}/verify-work"
    unit_path = f"{remote_home}/.config/systemd/user/{unit}"
    public_key = key.with_suffix(".pub").read_text()
    remote(f"test ! -e {quote(remote_root)}\ntest ! -e {quote(unit_path)}\nmkdir -p {quote(remote_work)} {quote(str(Path(remote_binary).parent))} {quote(str(Path(unit_path).parent))}\ninstall {quote(args.initial_linux)} {quote(remote_binary)}\ncat > {quote(remote_root + '/verify-key.pub')} <<'KEY'\n{public_key}KEY\n")
    remote_ready = True
    doc = (args.repository / "docs/using-outposts.md").read_text()
    body = re.search(r"```ini\n\s*# ~/.config/systemd/user/attn.service\n(.*?)```", doc, re.S)[1]
    body = "\n".join(line.strip() for line in body.strip().splitlines()) + "\n"
    body = body.replace("ExecStart=%h/.local/bin/attn daemon", f"ExecStart=/bin/bash -c 'exec {remote_binary} daemon 3>&1'\nEnvironment=ATTN_INSTANCE={args.instance}\nEnvironment=ATTN_AUTHORIZED_KEYS_PATH={remote_root}/verify-key.pub\nEnvironment=ATTN_DAEMON_READY_FD=3\nEnvironment=ATTN_HEADLESS_TASKS=off\nEnvironment=ATTN_GITHUB_POLLING=off\nEnvironment=ATTN_PTY_BACKEND=worker\nEnvironment=ATTN_HARNESS_SKILL_SYNC=off\nEnvironment=SHELL=/bin/bash")
    (args.out / "unit.service").write_text(body)
    remote(f"cat > {quote(unit_path)} <<'UNIT'\n{body}UNIT\nsystemctl --user daemon-reload\n")
    cursor = remote("journalctl --user -n 0 --show-cursor\n").split("-- cursor: ")[-1].strip()
    if not cursor.startswith("s="):
        raise RuntimeError("could not capture the user journal cursor: " + cursor)
    remote(f"systemctl --user start {quote(unit)}\n")
    remote(f"""coproc LOG {{ exec journalctl --user -u {quote(unit)} --after-cursor {quote(cursor)} -f -o cat; }}
watcher=$LOG_PID
trap 'kill -TERM "$watcher" 2>/dev/null || true; wait "$watcher" || true' EXIT
while read -r line <&"${{LOG[0]}}"; do
  if test "$line" = ready; then exit 0; fi
  case "$line" in *"daemon error:"*|*"Failed with result"*) printf '%s\\n' "$line" >&2; exit 1;; esac
done
exit 1
""")
    remote_cli("settings", "set", "outpost_listen", args.address)
    start_home(args.home)
    active_home = args.home
    run([args.home, "settings", "set", "outposts_enabled", "true"])
    row = json.loads(wire("outposts", "--add", "sandbox", "--address", args.address, "--until", "connected").splitlines()[-1])["outposts"][0]
    outpost_id = row["id"]
    say("paired", home=resources, row=row, unit=body)
    caller = "update-caller"
    local_work = args.out / "caller"
    local_work.mkdir()
    wire("add-session", "--agent", "shell", "--cwd", local_work, "--id", caller)
    listing = json.loads(wire("list"))
    caller_terminal = next(pane["runtime_id"] for desktop in listing["desktops"] for pane in desktop["panes"] if pane.get("session_id") == caller)
    terminal_id, shell_session = shell_answer("before", first=True)
    pid = shell_answer("before")
    for label in ["source-cold", "source-warm"]:
        old_build()
        push(label)
        assert shell_answer(label) == pid
    if args.release_linux:
        info = json.loads(run([args.home, "--build-info-json"]).stdout)
        asset_dir = args.out / "release" / f"v{info['version']}"
        asset_dir.mkdir(parents=True)
        shutil.copy2(args.release_linux.resolve(), asset_dir / "attn-linux-amd64")
        handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(asset_dir.parent))
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        serving = threading.Thread(target=server.serve_forever)
        serving.start()
        try:
            release_home = args.out / "attn-release-home"
            with (args.out / "release-home-build.txt").open("w") as build_log:
                subprocess.run(["make", "build", "CHANNEL=release", f"SOURCE_FINGERPRINT={info['sourceFingerprint']}", f"RELEASE_SOURCE=http://127.0.0.1:{server.server_port}", f"OUTPUT={release_home}"], cwd=args.repository, stdout=build_log, stderr=subprocess.STDOUT, check=True, env=base)
            old_build()
            stop_home()
            shutil.rmtree(data_dir / "outpost-builds")
            start_home(release_home)
            active_home = release_home
            wire("outposts", "--until", "refused")
            push("local-release")
            assert shell_answer("local-release") == pid
            old_build()
            stop_home()
            shutil.rmtree(data_dir / "outpost-builds")
            latest_home = args.out / "attn-github-release-home"
            with (args.out / "github-home-build.txt").open("w") as build_log:
                subprocess.run(["make", "build", "CHANNEL=release", f"SOURCE_FINGERPRINT={info['sourceFingerprint']}", f"OUTPUT={latest_home}"], cwd=args.repository, stdout=build_log, stderr=subprocess.STDOUT, check=True, env=base)
            start_home(latest_home)
            active_home = latest_home
            wire("outposts", "--until", "refused")
            push("latest-github-release-refused", expect_failure=True)
        finally:
            server.shutdown()
            serving.join()
            server.server_close()
    say("result", outcome="passed", shell_pid=pid)
finally:
    stop_home()
    if remote_ready:
        remote(f"systemctl --user stop {quote(unit)} || true\nATTN_INSTANCE={quote(args.instance)} {quote(remote_binary)} instance clean {quote(args.instance)}\nrm -f {quote(unit_path)} {quote(remote_binary)} {quote(remote_binary + '.previous')}\nsystemctl --user daemon-reload\n")
    if 'resources' in globals() and Path(resources["dataDir"]).exists():
        run([args.home, "instance", "clean", args.instance])
    for child in reversed(children):
        if child.poll() is None:
            child.terminate()
        child.wait()
    for log in logs:
        log.close()
