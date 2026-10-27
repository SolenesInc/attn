#!/usr/bin/env python3
"""Start or remove an isolated named outpost on a Linux systemd host."""

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess


def run(command, env, check=True):
    result = subprocess.run(command, env=env, text=True, capture_output=True)
    if check and result.returncode:
        raise RuntimeError(f"{' '.join(command)}: {result.stdout}{result.stderr}")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("start", "remove"))
    parser.add_argument("--instance", required=True, help="same non-production instance name as the Mac home")
    parser.add_argument("--binary", type=Path, help="Linux build to install (start only)")
    parser.add_argument("--listen", help="explicit outpost address:port (start only)")
    parser.add_argument("--authorized-keys", type=Path, default=Path.home() / ".ssh/authorized_keys")
    args = parser.parse_args()
    if not re.fullmatch(r"[a-z0-9][a-z0-9-]{0,15}", args.instance) or args.instance == "default":
        parser.error("--instance must name a non-production instance")
    env = {key: value for key, value in os.environ.items() if not key.startswith("ATTN_")}
    env |= {"ATTN_INSTANCE": args.instance, "ATTN_HEADLESS_TASKS": "off", "ATTN_GITHUB_POLLING": "off"}
    root = Path.home() / ".local/share/attn-outpost-instances" / args.instance
    binary = root / "attn"
    marker = root / "instance.json"
    unit = f"attn-outpost-{args.instance}.service"
    unit_path = Path(os.environ["XDG_RUNTIME_DIR"]) / "systemd/user" / unit
    if args.action == "remove":
        record = json.loads(marker.read_text())
        if record["instance"] != args.instance or record["unit"] != str(unit_path):
            raise RuntimeError(f"ownership record disagrees with {args.instance}")
        if unit_path.exists():
            run(["systemctl", "--user", "stop", unit], env)
        print(run([str(binary), "instance", "clean", args.instance], env).stdout, end="")
        if unit_path.exists():
            unit_path.unlink()
            run(["systemctl", "--user", "daemon-reload"], env)
        shutil.rmtree(root)
        print(json.dumps({"removed": args.instance, "unit": unit}))
        return
    if args.binary is None or args.listen is None:
        parser.error("start needs --binary and --listen")
    if root.exists() or unit_path.exists():
        raise RuntimeError(f"{args.instance} already exists; remove it first")
    resolved = json.loads(run([str(args.binary.resolve()), "instance", "resolve", "--instance", args.instance, "--json"], env).stdout)
    data = Path(resolved["dataDir"])
    if data.exists():
        raise RuntimeError(f"refusing existing instance data: {data}")
    root.mkdir(parents=True)
    data.mkdir(mode=0o700, parents=True)
    shutil.copy2(args.binary, binary)
    shutil.copy2(args.authorized_keys, data / "authorized_keys")
    env["ATTN_AUTHORIZED_KEYS_PATH"] = str(data / "authorized_keys")
    record = {"instance": args.instance, "unit": str(unit_path), "dataDir": str(data), "notebookRoot": str(root / "notebook"), "binary": str(binary), "listen": args.listen}
    marker.write_text(json.dumps(record) + "\n")
    reader, writer = os.pipe()
    log = (root / "configure.log").open("a")
    daemon = subprocess.Popen([str(binary), "daemon"], env=env | {"ATTN_DAEMON_READY_FD": str(writer)}, stdout=log, stderr=log, pass_fds=(writer,))
    os.close(writer)
    try:
        with os.fdopen(reader) as ready:
            if ready.readline().strip() != "ready":
                raise RuntimeError(f"daemon failed to start: {(root / 'configure.log').read_text()}")
        run([str(binary), "settings", "set", "notebook.root", record["notebookRoot"]], env)
        run([str(binary), "settings", "set", "outpost_listen", args.listen], env)
    finally:
        daemon.terminate()
        daemon.wait()
        log.close()
    unit_path.parent.mkdir(parents=True, exist_ok=True)
    unit_path.write_text(
        "[Unit]\nDescription=attn verification outpost " + args.instance + "\n\n"
        "[Service]\nExecStart=" + str(binary) + " daemon\n"
        "Environment=ATTN_INSTANCE=" + args.instance + "\n"
        "Environment=ATTN_AUTHORIZED_KEYS_PATH=" + str(data / "authorized_keys") + "\n"
        "Environment=ATTN_GITHUB_POLLING=off\nEnvironment=ATTN_HEADLESS_TASKS=off\n"
        "Restart=on-failure\nKillMode=process\n"
    )
    run(["systemctl", "--user", "daemon-reload"], env)
    run(["systemctl", "--user", "start", unit], env)
    print(json.dumps(record))


if __name__ == "__main__":
    main()
