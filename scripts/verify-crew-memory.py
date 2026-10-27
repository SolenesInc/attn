#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import tempfile


parser = argparse.ArgumentParser(description="Exercise crew memory through the real CLI and daemon, with a copied crew home and a local harness stub.")
parser.add_argument("--attn", required=True, type=Path)
parser.add_argument("--crew-home", required=True, type=Path)
parser.add_argument("--receipts", required=True, type=Path)
args = parser.parse_args()
binary = args.attn.resolve()
source = args.crew_home.resolve()
receipts = args.receipts.resolve()
receipts.mkdir(parents=True, exist_ok=True)

with tempfile.TemporaryDirectory(prefix="attn-memory-", dir="/private/tmp") as temporary:
    root = Path(temporary)
    data = root / "data"
    member = source.name
    home = data / "crew" / member
    shutil.copytree(source, home)
    if (home / "memory").exists():
        raise RuntimeError("Use a pre-memory crew home copy so this proof starts with letters and no entries.")
    events = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
    event_path = root / "events.sock"
    events.bind(str(event_path))
    stub = root / "claude"
    stub.write_text("""#!/usr/bin/env python3
import os, signal, socket, sys
if '--version' in sys.argv:
    print('2.1.310 (crew-memory verification stub)')
    sys.exit(0)
if '--print' in sys.argv:
    sys.exit(0)
s = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
s.sendto(b'x', os.environ['ATTN_MEMORY_PROBE_EVENTS'])
print('Crew memory verification harness', flush=True)
while True:
    signal.pause()
""")
    stub.chmod(0o755)
    env = {k: v for k, v in os.environ.items() if not k.startswith("ATTN_") and k != "CODEX_THREAD_ID"}
    env.update({
        "ATTN_DATA_DIR": str(data),
        "ATTN_TOOL_HOME": str(root / "tools"),
        "ATTN_HEADLESS_TASKS": "off",
        "ATTN_GITHUB_POLLING": "off",
        "ATTN_HARNESS_SKILL_SYNC": "off",
        "ATTN_PTY_BACKEND": "worker",
        "ATTN_CLAUDE_EXECUTABLE": str(stub),
        "ATTN_MEMORY_PROBE_EVENTS": str(event_path),
        "ATTN_WS_PORT": "0",
        "PATH": str(root) + os.pathsep + env["PATH"],
    })
    read_fd, ready_fd = os.pipe()
    daemon_env = env | {"ATTN_DAEMON_READY_FD": str(ready_fd)}
    transcript = []

    def cli(*values, stdin=None, structured=False):
        command = [str(binary), *values]
        if structured:
            command.append("--json")
        result = subprocess.run(command, env=env, input=stdin, text=True, capture_output=True)
        transcript.append("$ attn " + shlex.join(values) + (" --json" if structured else ""))
        transcript.append(result.stdout.rstrip())
        if result.returncode:
            raise RuntimeError(result.stderr)
        return json.loads(result.stdout) if structured else result.stdout

    def prime(session):
        with socket.socket(socket.AF_UNIX) as connection:
            connection.connect(str(data / "attn.sock"))
            connection.sendall(json.dumps({"cmd": "crew_prime", "session_id": session}).encode() + b"\n")
            result = json.loads(connection.makefile("rb").readline())
        if not result["ok"]:
            raise RuntimeError(result)
        guidance = result["crew_prime_result"]["guidance"]
        return guidance.split("## Your memory\n", 1)[1].split("## Closure", 1)[0].strip()

    with (receipts / "daemon-stderr.log").open("w") as log:
        daemon = subprocess.Popen([str(binary), "daemon"], env=daemon_env, pass_fds=[ready_fd], stdout=log, stderr=log)
        os.close(ready_fd)
        live = ""
        try:
            with os.fdopen(read_fd, "rb") as ready:
                if not ready.read(1):
                    raise RuntimeError("daemon exited before its ready signal; see daemon.log")
            cli("settings", "set", "crew_memory_enabled", "true")
            cli("settings", "set", "crew.heartbeat_enabled", "false")
            cli("settings", "set", "crew.autosleep_enabled", "false")
            day = cli("crew", "wake", member, structured=True)
            live = day["session_id"]
            events.recv(1)
            first = prime(live)
            assert "Before your first `attn memory add`" in first
            oldest = sorted((home / "handoffs").glob("*.md"))[0]
            cli("memory", "import", "-", "--session", live, stdin=oldest.name + " A historical letter imported for the verification.\n")
            cli("memory", "add", "The user wants decisions to keep their reasons.", "--session", live)
            cli("memory", "recall", "reasons", "--session", live)
            cli("memory", "zoom", "0-1", "--session", live)
            handoff = cli("handoff", "--nap", "--day", "Verified memory through the CLI.", "-m", "The verification exercised a copied home; production files were only read.", "--session", live, structured=True)
            retried = cli("handoff", "--retry", "--session", live, structured=True)
            assert handoff["path"] == retried["path"] and handoff["day_id"] == retried["day_id"]
            cli("memory", "zoom", str(handoff["day_id"]), "--session", live)
            while True:
                pending = cli("memory", "summarize", "--session", live, structured=True)
                if pending.get("handoff"):
                    completed = pending
                    break
                answers = "".join(b["range"] + " The verification retained decisions and their reasons.\n" for b in pending["blocks"])
                if not answers:
                    raise RuntimeError("pending summaries offered no source blocks")
                completed = cli("memory", "summarize", "-", "--session", live, stdin=answers, structured=True)
                if completed.get("handoff"):
                    break
            successor = completed["handoff"]["session_id"]
            assert successor != live
            live = successor
            events.recv(1)
            view = prime(live)
            assert "The user wants decisions to keep their reasons." in view
            transcript.extend(["$ crew_prime successor: wake view", view])
            cli("memory", "rewrite", "0-1", "--session", live)
            cli("memory", "forget", "1", "--session", live)
            cli("memory", "recall", "reasons", "--session", live)
            transcript.append("PASS: add, recall, zoom, import, handoff --day, retry, summarize, successor wake view, rewrite and forget.")
        finally:
            try:
                if live and daemon.poll() is None:
                    cli("settings", "set", "crew_memory_enabled", "false")
                    cli("handoff", "--sleep", "-m", "Crew memory verification complete.", "--session", live)
            finally:
                daemon.terminate()
                daemon.wait()
                events.close()
                if (data / "daemon.log").exists():
                    shutil.copy2(data / "daemon.log", receipts / "daemon.log")
                (receipts / "commands.txt").write_text("\n".join(transcript) + "\n")

print((receipts / "commands.txt").read_text())
