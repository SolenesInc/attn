#!/usr/bin/env python3
import argparse
import ctypes
import json
import os
from pathlib import Path
import re
import select
import signal
import socket
import subprocess
import tempfile
import uuid

parser = argparse.ArgumentParser(description="On a Linux box, run an outpost under the systemd user unit from docs/using-outposts.md and check that its shells survive systemctl --user restart and attn daemon stop followed by systemctl --user start.")
parser.add_argument("--attn", required=True, type=Path, help="Linux attn binary, used for both daemons")
parser.add_argument("--wsctl", required=True, type=Path, help="Linux build of scripts/wsctl")
parser.add_argument("--doc", default=Path(__file__).resolve().parent.parent / "docs" / "using-outposts.md", type=Path)
args = parser.parse_args()
args.attn = args.attn.resolve()
args.wsctl = args.wsctl.resolve()
unit = "attn-verify-" + uuid.uuid4().hex[:8] + ".service"
unit_path = Path.home() / ".config" / "systemd" / "user" / unit
children = []
logs = []
failures = []


def say(label, value):
    print(f"{label}: {value}", flush=True)


def run(command, env=None, check=True):
    result = subprocess.run([str(part) for part in command], env=env, text=True, capture_output=True)
    if check and result.returncode:
        raise RuntimeError(f"{command[0]} {command[1:]}: {result.stderr.strip() or result.stdout.strip()}")
    return result.stdout.strip()


def systemctl(*verbs, check=True):
    return run(["systemctl", "--user", *verbs], check=check)


def documented_unit():
    match = re.search(r"```ini\n\s*# ~/.config/systemd/user/attn.service\n(.*?)```", args.doc.read_text(), re.S)
    if not match:
        raise RuntimeError(f"{args.doc} has no ini block for ~/.config/systemd/user/attn.service")
    return "\n".join(line.strip() for line in match.group(1).strip().splitlines()) + "\n"


def listener():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    sock.listen()
    return sock


def free_port():
    sock = listener()
    port = sock.getsockname()[1]
    sock.close()
    return port


def daemon_env(base, data, port):
    data.mkdir(mode=0o700)
    (data / "authorized_keys").write_bytes(key.with_suffix(".pub").read_bytes())
    return base | {
        "ATTN_DATA_DIR": str(data),
        "ATTN_HARNESS_DATA_DIR": str(data),
        "ATTN_HARNESS_NOTEBOOK_ROOT": str(data / "notebook"),
        "ATTN_AUTHORIZED_KEYS_PATH": str(data / "authorized_keys"),
        "ATTN_WS_PORT": str(port),
        "ATTN_WS_URL": f"ws://127.0.0.1:{port}/ws",
        "ATTN_HEADLESS_TASKS": "off",
        "ATTN_GITHUB_POLLING": "off",
        "ATTN_HARNESS_SKILL_SYNC": "off",
        "ATTN_CACHED_SHELL_ENV": "[]",
    }


def start_plain_daemon(name, env):
    ready, signal_fd = os.pipe()
    log = (root / f"{name}.stderr").open("w")
    logs.append(log)
    daemon = subprocess.Popen([str(args.attn), "daemon"], env=env | {"ATTN_DAEMON_READY_FD": str(signal_fd)}, stdout=log, stderr=log, pass_fds=(signal_fd,))
    children.append(daemon)
    os.close(signal_fd)
    with os.fdopen(ready) as stream:
        if stream.readline().strip() != "ready":
            raise RuntimeError(f"{name} was not ready: {(root / (name + '.stderr')).read_text()}")
    return daemon


def wsctl(*values):
    return run([args.wsctl, *values], env=home)


def sessions():
    return {row["id"]: row for row in json.loads(wsctl("list"))["sessions"]}


class Deadline:
    def __init__(self, seconds, what):
        self.seconds, self.what = seconds, what

    def __enter__(self):
        def expire(_signum, _frame):
            raise TimeoutError(f"{self.what} did not happen within {self.seconds}s")
        signal.signal(signal.SIGALRM, expire)
        signal.alarm(self.seconds)

    def __exit__(self, *_):
        signal.alarm(0)


def shell_pid(shell, label):
    fifo = root / f"{shell}-{label}.fifo"
    os.mkfifo(fifo)
    wsctl("input", "--id", terminals[shell], "--text", f"sh -c 'echo {label}-{shell} $PPID; echo $PPID > {fifo}'", "--enter")
    with Deadline(30, f"{shell} answering {label!r}"), fifo.open() as stream:
        return int(stream.read().strip())


def pidfd_open(pid):
    fd = ctypes.CDLL(None, use_errno=True).syscall(434, pid, 0)
    if fd < 0:
        raise OSError(ctypes.get_errno(), f"pidfd_open({pid})")
    return fd


def cgroup(pid):
    try:
        return Path(f"/proc/{pid}/cgroup").read_text().strip()
    except FileNotFoundError:
        return "gone"


def await_reconnect(restart):
    watch = subprocess.Popen([str(args.wsctl), "outposts", "--watch"], env=home, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    children.append(watch)
    seen = []
    restarted = False
    with Deadline(120, "the home reconnecting to the outpost"):
        for line in watch.stdout:
            state = next(row["state"] for row in json.loads(line)["outposts"] if row["name"] == "sandbox")
            if not seen or seen[-1] != state:
                seen.append(state)
            if not restarted and state == "connected":
                restart()
                restarted = True
            elif restarted and state == "connected" and len(seen) > 1:
                break
    watch.terminate()
    watch.wait()
    say("home saw outpost", " → ".join(seen))


def check(label, restart):
    say("restart", label)
    before = {shell: cgroup(pids[shell]) for shell in pids}
    await_reconnect(restart)
    rows = sessions()
    for shell, pid in pids.items():
        after = cgroup(pid)
        say(f"{shell} pid {pid}", f"{before[shell]} → {after}")
        state = rows[shell]["state"]
        say(f"{shell} state at the home", state)
        if after == "gone":
            failures.append(f"{label}: {shell} pid {pid} is gone")
            continue
        if state == "recoverable":
            failures.append(f"{label}: {shell} is recoverable at the home")
            continue
        screen = run([args.wsctl, "screen", "--id", terminals[shell]], env=home, check=False)
        marker = f"typed-{shell} {pid}"
        say(f"{shell} screen keeps {marker!r}", marker in screen)
        if marker not in screen:
            failures.append(f"{label}: {shell} screen lost {marker!r}:\n{screen}")
        answered = shell_pid(shell, "after-" + label.split()[0])
        say(f"{shell} answers as pid", answered)
        if answered != pid:
            failures.append(f"{label}: {shell} answered as pid {answered}, expected {pid}")


with tempfile.TemporaryDirectory(prefix="attn-sdcheck-", dir="/tmp") as temporary:
    root = Path(temporary)
    base = {k: v for k, v in os.environ.items() if not k.startswith("ATTN_")}
    base["SSH_AUTH_SOCK"] = str(root / "agent.sock")
    unit_written = False
    try:
        agent = subprocess.Popen(["ssh-agent", "-D", "-a", base["SSH_AUTH_SOCK"]], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, env=base)
        children.append(agent)
        if not agent.stdout.readline():
            raise RuntimeError("ssh-agent exited before reporting its socket")
        key = root / "key"
        run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key], base)
        run(["ssh-add", key], base)
        outpost = daemon_env(base, root / "outpost", free_port())
        bridge_address = f"127.0.0.1:{free_port()}"
        configure = start_plain_daemon("outpost-configure", outpost)
        say("outpost settings", run([args.attn, "settings", "set", "outpost_listen", bridge_address], outpost))
        configure.terminate()
        configure.wait()

        body = documented_unit()
        documented_exec = "ExecStart=%h/.local/bin/attn daemon\n"
        if documented_exec not in body:
            raise RuntimeError(f"documented unit no longer runs {documented_exec.strip()!r}:\n{body}")
        isolation = "".join(f"Environment={k}={v}\n" for k, v in outpost.items() if k.startswith("ATTN_"))
        tested = body.replace(documented_exec, f"ExecStart={args.attn} daemon\n" + isolation)
        unit_path.parent.mkdir(parents=True, exist_ok=True)
        unit_path.write_text(tested)
        unit_written = True
        say("documented unit", "\n" + body)
        say(f"tested as {unit}", f"the documented unit with ExecStart pointing at {args.attn} and isolation Environment= lines for ATTN_* only")
        systemctl("daemon-reload")
        systemctl("enable", "--now", unit)

        home_web = listener()
        home = daemon_env(base, root / "home", home_web.getsockname()[1]) | {"ATTN_HARNESS_WS_LISTENER_FD": str(home_web.fileno())}
        ready, signal_fd = os.pipe()
        log = (root / "home.stderr").open("w")
        logs.append(log)
        children.append(subprocess.Popen([str(args.attn), "daemon"], env=home | {"ATTN_DAEMON_READY_FD": str(signal_fd)}, stdout=log, stderr=log, pass_fds=(home_web.fileno(), signal_fd)))
        os.close(signal_fd)
        home_web.close()
        with os.fdopen(ready) as stream:
            if stream.readline().strip() != "ready":
                raise RuntimeError("home was not ready: " + (root / "home.stderr").read_text())
        home = {k: v for k, v in home.items() if k != "ATTN_HARNESS_WS_LISTENER_FD"}
        say("home settings", run([args.attn, "settings", "set", "outposts_enabled", "true"], home))
        say("pair", wsctl("outposts", "--add", "sandbox", "--address", bridge_address, "--until", "connected").splitlines()[-1])

        workdir = root / "work"
        workdir.mkdir()
        shells = ["shell-a", "shell-b"]
        for shell in shells:
            wsctl("add-session", "--agent", "shell", "--host", "sandbox", "--cwd", workdir, "--id", shell, "--label", shell)
        desktops = json.loads(wsctl("list"))["desktops"]
        terminals = {pane["session_id"]: pane["runtime_id"] for desktop in desktops for pane in desktop["panes"] if pane["session_id"] in shells}
        if sorted(terminals) != shells:
            raise RuntimeError(f"could not find both terminals in desktops: {json.dumps(desktops)}")
        pids = {shell: shell_pid(shell, "typed") for shell in shells}
        for shell, pid in pids.items():
            say(f"{shell} terminal {terminals[shell]} pid {pid}", cgroup(pid))

        check("systemctl --user restart", lambda: systemctl("restart", unit))
        check("attn daemon stop, then systemctl --user start", lambda: (
            say("attn daemon stop", run([args.attn, "daemon", "stop"], outpost)),
            say("unit after stop", systemctl("is-active", unit, check=False)),
            systemctl("start", unit),
        ))
        say("stop everything", f"systemctl --user kill {unit}")
        exits = {shell: pidfd_open(pid) for shell, pid in pids.items() if cgroup(pid) != "gone"}
        systemctl("kill", unit)
        with Deadline(30, "the shells exiting after systemctl --user kill"):
            for shell, pidfd in exits.items():
                select.select([pidfd], [], [])
                os.close(pidfd)
                say(f"{shell} pid {pids[shell]}", "exited")
    finally:
        cleanup = []
        if unit_written:
            for verbs in [("kill", "--signal=SIGKILL", unit), ("disable", "--now", unit)]:
                result = subprocess.run(["systemctl", "--user", *verbs], text=True, capture_output=True)
                if result.returncode and "not loaded" not in result.stderr:
                    cleanup.append(" ".join(verbs) + ": " + result.stderr.strip())
            unit_path.unlink()
            systemctl("daemon-reload", check=False)
            systemctl("reset-failed", unit, check=False)
        for child in reversed(children):
            if child.poll() is None:
                child.terminate()
            child.wait()
        for log in logs:
            log.close()
        say("cleanup", cleanup or f"{unit} stopped and removed, home and ssh-agent stopped, {root} removed")
if failures:
    print("FAIL\n" + "\n".join(failures))
    raise SystemExit(1)
print("PASS: both shells kept their pids, screens and state through both restarts, and systemctl --user kill ended them")
