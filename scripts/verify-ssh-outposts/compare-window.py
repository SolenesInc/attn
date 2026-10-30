#!/usr/bin/env python3
import argparse
import json
import re
import shutil
import statistics
import subprocess
from pathlib import Path
from shlex import quote

p = argparse.ArgumentParser(description="Compare bridge output control over one ssh route, idle then three alternating pairs.")
for name in ("before-mac", "before-linux", "after-mac", "after-linux", "notebook", "out"):
    p.add_argument("--" + name, type=Path, required=True)
p.add_argument("--ssh", default="ai-sandbox")
p.add_argument("--instance", required=True)
a = p.parse_args()
if not re.fullmatch(r"[a-z0-9][a-z0-9-]{0,15}", a.instance) or a.instance == "default":
    p.error("--instance must be a named instance: [a-z0-9][a-z0-9-]{0,15}, excluding default")
a.out.mkdir(parents=True, exist_ok=False)
root = Path(__file__).resolve().parent


def remote(script):
    return subprocess.run(["ssh", "-o", "BatchMode=yes", a.ssh, "bash", "-s"],
                          input="set -euo pipefail\n" + script + "\n", text=True, capture_output=True, check=True).stdout.strip()


remote_home = remote('printf "%s" "$HOME"')
binary = f"{remote_home}/.local/share/attn-outpost-instances/{a.instance}/attn"
analyses = {}
cases = [("idle", "after", "idle")]
for pair in range(1, 4):
    order = ("before", "after") if pair % 2 else ("after", "before")
    cases.extend((f"pair-{pair}-{version}", version, "bulk") for version in order)

for label, version, mode in cases:
    mac = getattr(a, version + "_mac").resolve()
    linux = getattr(a, version + "_linux").resolve()
    notebook = a.out / (label + "-notebook")
    if mode == "bulk":
        shutil.copytree(a.notebook, notebook)
    else:
        notebook.mkdir()
    remote(f"test ! -e {quote(binary)}; mkdir -p {quote(str(Path(binary).parent))}")
    subprocess.run(["scp", "-o", "BatchMode=yes", str(linux), f"{a.ssh}:{binary}"], check=True)
    command = ["node", str(root / "transport.mjs"), "--mode", mode, "--macBin", str(mac),
               "--instance", a.instance, "--ssh", a.ssh, "--scratch", f"/tmp/{a.instance}",
               "--out", str(a.out / label), "--notebook", str(notebook), "--samples", "1001"]
    with (a.out / (label + "-driver.log")).open("w") as log:
        subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
    result = subprocess.run(["python3", str(root / "analyze.py"), str(a.out / label)], capture_output=True, text=True, check=True)
    analyses[label] = json.loads(result.stdout)
    (a.out / label / "analysis.json").write_text(result.stdout)
    print(label, result.stdout, flush=True)
    shutil.rmtree(notebook)

idle = analyses["idle"]
rows = []
ratios = []
failures = []
for pair in range(1, 4):
    before, after = (analyses[f"pair-{pair}-{version}"] for version in ("before", "after"))
    ratio = after["notebook_sync"]["MBps_from_sampling"] / before["notebook_sync"]["MBps_from_sampling"]
    ratios.append(ratio)
    excess = {
        "mux": after["control_same_seconds"]["mux"]["p50_ms"] - idle["control_same_seconds"]["mux"]["p50_ms"],
        "separate": after["control_same_seconds"]["separate"]["p50_ms"] - idle["control_same_seconds"]["separate"]["p50_ms"],
        "per_call_R": after["per_call_R_direct"]["p50_ms"] - idle["per_call_R_direct"]["p50_ms"],
        "raw_echo": after["raw_echo"]["p50_ms"] - idle["raw_echo"]["p50_ms"],
    }
    for name, bound in {"mux": 10, "separate": 10, "per_call_R": 25, "raw_echo": 45}.items():
        if excess[name] > bound:
            failures.append(f"pair {pair} {name} p50 excess {excess[name]:.3f}ms exceeds {bound}ms")
    if after["bridge_unanswered"] or any(x["unanswered"] for x in after["control_same_seconds"].values()):
        failures.append(f"pair {pair} has unanswered typing or control samples")
    rows.append({"pair": pair, "after_p50_excess_ms": excess, "after_before_throughput": ratio})
median = statistics.median(ratios)
if median < .85:
    failures.append(f"median throughput ratio {median:.4f} is below 0.85")
report = {"pairs": rows, "median_throughput_ratio": median, "failures": failures}
(a.out / "comparison.json").write_text(json.dumps(report, indent=2) + "\n")
print(json.dumps(report, indent=2))
raise SystemExit(bool(failures))
