#!/usr/bin/env python3
"""Measure what verifying a change costs: lands, the full suites and CI.

usage: scripts/measure-verification-cost.py [--runs N] [--keep] [--ci-run ID]... [--workers N,...] [baseline...]

Baselines (all by default):
  land     one scripts/land.sh landing a one-file Go change
  lands3   three concurrent lands on one machine, each from its own worktree
  suites   scripts/test-go.sh -count=1, then pnpm --dir app exec vitest run
  daemon   scripts/test-go.sh --packages -count=1 on ./internal/daemon, then on
           ./cmd/attnd: the two packages whose tests start daemons
  ci       ci.yml wall time, its slowest chains and the longest job queue wait
           over the last 5 green main runs, or the runs --ci-run names
  acceptance  the Linux acceptance matrix with 1 worker, then each --workers count

Local baselines measure this checkout's HEAD commit. They print each run's
outcome, wall seconds, CPU-seconds and the 1-minute load average before and
after it, then the median and range over the runs that succeeded (default 3
runs). A failed run, such as a land that did not push or a suite with a failing
test, stays out of the median. When no run succeeds, the median covers the
failed runs and says so: a red main still shows what a suite costs.

One measurement runs at a time per machine: each run and each scratch setup
takes a lock on /tmp/attn-verification-cost.lock, waits while another process
holds it and prints the holder. The kernel releases the lock when its process
exits.

Concurrent lands start together and wait for each other in land.sh's land
queue. Their wall time runs from the start to the last land.

Every land commits a change no earlier land compiled, so Go builds the changed
package cold, as a real land does, instead of reusing the shared build cache.

Lands never reach the real origin or the shared test history. They run in a
scratch clone of HEAD, beside this checkout, whose origin is a local bare
repository. Every worktree of the clone shares a scratch test-history spool
seeded with a copy of this checkout's mirror, so lands choose landing tests as a
real land would. Lands see an empty XDG_CONFIG_HOME, so test-history finds no
service token and sync stops before it connects. The suites record receipts
into the scratch spool, and nothing syncs it.

The acceptance baseline runs on Linux in this checkout, not a scratch clone,
because it drives installed packaged apps: install instances acc1..accN from
this checkout first (`make install INSTANCE=acc1` and so on). It runs
app/scripts/real-app-harness/run-parallel-matrix.mjs, each worker on its own
Xvfb display, inside a systemd user scope, and reads the scope's CPU time, so
the apps, daemons and PTY workers the matrix starts count toward its CPU. One
worker is the serial matrix. A run is ok when every scenario passed.
"""

import argparse
import fcntl
import json
import os
import re
import shutil
import statistics
import subprocess
import sys
import tempfile
import threading
import time
import uuid
from contextlib import contextmanager
from pathlib import Path

BASELINES = ("land", "lands3", "suites", "daemon", "ci", "acceptance")
LAND_PACKAGES = ("internal/daemon", "internal/store", "cmd/attnd")
CI_GREEN_RUNS = 5
MEASUREMENT_LOCK = Path("/tmp/attn-verification-cost.lock")


def log(message):
    print(message, file=sys.stderr, flush=True)


def git(*args, cwd=None):
    return subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True, text=True).stdout.strip()


@contextmanager
def measurement(label):
    lock = os.open(MEASUREMENT_LOCK, os.O_RDWR | os.O_CREAT, 0o644)
    try:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            holder = os.pread(lock, 4096, 0).decode(errors="replace").strip()
            log(f"{label}: waiting for {MEASUREMENT_LOCK}, held by {holder}")
            fcntl.flock(lock, fcntl.LOCK_EX)
        os.ftruncate(lock, 0)
        os.pwrite(lock, f"pid {os.getpid()} in {os.getcwd()}, {label}, since {time.strftime('%H:%M:%S')}\n".encode(), 0)
        load = {"load_before": os.getloadavg()[0]}
        yield load
        load["load_after"] = os.getloadavg()[0]
    finally:
        os.close(lock)


def run_measured(command, cwd, env, log_path):
    with open(log_path, "w") as out:
        start = time.monotonic()
        process = subprocess.Popen(command, cwd=cwd, env=env, stdout=out, stderr=subprocess.STDOUT)
        _, status, usage = os.wait4(process.pid, 0)
        wall = time.monotonic() - start
    process.returncode = os.waitstatus_to_exitcode(status)
    return {"exit": process.returncode, "wall": wall, "cpu": usage.ru_utime + usage.ru_stime, "log": str(log_path)}


def run_concurrently(jobs):
    results = [None] * len(jobs)

    def run(i, job):
        results[i] = run_measured(*job)

    threads = [threading.Thread(target=run, args=(i, job)) for i, job in enumerate(jobs)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    return results


class Scratch:
    def __init__(self, source, root, worktrees):
        self.source = source
        self.root = root
        self.base = git("rev-parse", "HEAD", cwd=source)
        self.logs = root / "logs"
        self.logs.mkdir(parents=True, exist_ok=True)
        self.env = dict(os.environ)
        for name in ("ATTN_SESSION_ID", "ATTN_INSTANCE", "ATTN_DATA_DIR", "ATTN_DB_PATH", "ATTN_SOCKET_PATH", "ATTN_CONFIG_PATH", "ATTN_PLUGIN_DIR", "GIT_DIR", "GIT_WORK_TREE"):
            self.env.pop(name, None)
        self.env["ATTN_TEST_HISTORY_DIR"] = str(root / "spool")
        self.land_env = {**self.env, "XDG_CONFIG_HOME": str(root / "config"), "GIT_CONFIG_GLOBAL": os.devnull}
        (root / "config").mkdir(exist_ok=True)
        with measurement("scratch setup"):
            self.make_origin()
            self.make_spool()
            self.worktrees = [self.make_worktree(i) for i in range(worktrees)]

    def make_origin(self):
        origin = self.root / "origin.git"
        git("init", "--quiet", "--bare", str(origin))
        objects = Path(git("rev-parse", "--path-format=absolute", "--git-common-dir", cwd=self.source)) / "objects"
        (origin / "objects" / "info" / "alternates").write_text(f"{objects}\n")
        git("update-ref", "refs/heads/main", self.base, cwd=origin)
        git("clone", "--quiet", "--shared", str(origin), str(self.root / "clone"))
        git("config", "user.name", "verification cost", cwd=self.root / "clone")
        git("config", "user.email", "verification-cost@localhost", cwd=self.root / "clone")
        self.origin = origin

    def make_spool(self):
        real = Path(subprocess.run(["go", "tool", "test-history", "spool"], cwd=self.source, check=True, capture_output=True, text=True).stdout.strip())
        spool = self.root / "spool"
        spool.mkdir()
        if (real / "mirror").is_dir():
            shutil.copytree(real / "mirror", spool / "mirror")
            shutil.copy2(real / "mirror.json", spool / "mirror.json")
        else:
            log(f"no mirror in {real}; lands choose only the baseline groups")

    def make_worktree(self, i):
        path = self.root / f"wt{i + 1}"
        git("worktree", "add", "--quiet", "-b", f"change-{i + 1}", str(path), self.base, cwd=self.root / "clone")
        native_vt = subprocess.run(["make", "-s", "-f", "Makefile", "-f", "-", "native-vt-dep"], input="native-vt-dep:\n\t@echo $(NATIVE_VT_DEP)\n", cwd=path, env=self.env, check=True, capture_output=True, text=True).stdout.strip()
        if native_vt:
            subprocess.run(["make", "-s", native_vt], cwd=path, env=self.env, check=True, stdout=subprocess.DEVNULL)
        subprocess.run(["make", "-s", "app/node_modules/.attn-installed"], cwd=path, env=self.env, check=True, stdout=subprocess.DEVNULL)
        return path

    def reset(self, run):
        git("update-ref", "refs/heads/main", self.base, cwd=self.origin)
        change_id = uuid.uuid4().hex
        for i, path in enumerate(self.worktrees):
            git("reset", "--quiet", "--hard", self.base, cwd=path)
            package = path / LAND_PACKAGES[i % len(LAND_PACKAGES)]
            source_file = next(f for f in sorted(package.glob("*.go")) if not f.name.endswith("_test.go"))
            name = next(line.split()[1] for line in source_file.read_text().splitlines() if line.startswith("package "))
            change = package / f"verification_cost_{i + 1}.go"
            change.write_text(f"package {name}\n\nconst VerificationCostChange{i + 1} = \"{change_id}\"\n")
            git("add", str(change), cwd=path)
            git("commit", "--quiet", "--no-verify", "-m", f"Verification cost change {i + 1}, run {run}", cwd=path)

    def land(self, label, run, worktrees):
        jobs = [(["scripts/land.sh"], path, self.land_env, self.logs / f"{label}-{run}-wt{i + 1}.log") for i, path in enumerate(worktrees)]
        results = run_concurrently(jobs)
        for result in results:
            text = Path(result["log"]).read_text()
            result["ok"] = "land: main is at" in text
            result["checks"] = len(re.findall(r"^landing tests \(", text, re.MULTILINE))
        return results


def fmt(rows, key, unit="s"):
    ok = [row[key] for row in rows if row["ok"]]
    if ok:
        return spread(ok, unit)
    return f"no run succeeded; failed runs only {spread([row[key] for row in rows], unit)}" if rows else "no runs"


def spread(values, unit="s"):
    return f"{statistics.median(values):.0f}{unit} ({min(values):.0f}-{max(values):.0f})"


def outcome(rows):
    return f"{sum(row['ok'] for row in rows)} of {len(rows)} runs ok"


def describe(result):
    status = "ok" if result["ok"] else f"FAILED, exit {result['exit']}"
    return f"{status}, wall {result['wall']:.0f}s cpu {result['cpu']:.0f}s"


def fmt_load(run):
    return f"load before {run['load_before']:.1f} after {run['load_after']:.1f}"


def fmt_checks(result):
    if result["ok"] and result["checks"] == 0:
        return "checks unknown: no 'landing tests (' summary in its log"
    return f"checks {result['checks']}"


def measure_land(scratch, runs):
    rows = []
    for run in range(1, runs + 1):
        scratch.reset(run)
        with measurement(f"land run {run}") as load:
            [result] = scratch.land("land", run, scratch.worktrees[:1])
        result.update(load)
        print(f"  land run {run}: {describe(result)}, {fmt_checks(result)}, {fmt_load(result)}, log {result['log']}", flush=True)
        rows.append(result)
    print(f"one land, {outcome(rows)}: wall {fmt(rows, 'wall')}, cpu {fmt(rows, 'cpu')}", flush=True)


def measure_lands3(scratch, runs):
    rows = []
    for run in range(1, runs + 1):
        scratch.reset(run)
        with measurement(f"lands3 run {run}") as load:
            start = time.monotonic()
            results = scratch.land("lands3", run, scratch.worktrees[:3])
            wall = time.monotonic() - start
        row = {"ok": all(r["ok"] for r in results), "wall": wall, "cpu": sum(r["cpu"] for r in results), "lands": results, **load}
        lands = "; ".join(f"wt{i + 1} {describe(r)}, {fmt_checks(r)}" for i, r in enumerate(results))
        print(f"  lands3 run {run}: {'ok' if row['ok'] else 'FAILED'}, all done {wall:.0f}s, cpu {row['cpu']:.0f}s, {fmt_load(row)}; {lands}", flush=True)
        rows.append(row)
    lands = [land for row in rows if row["ok"] for land in row["lands"]]
    checks = " / ".join(",".join(str(land["checks"]) for land in row["lands"]) for row in rows if row["ok"])
    print(f"three concurrent lands, {outcome(rows)}: first start to last land {fmt(rows, 'wall')}, each land {fmt(lands, 'wall')}, cpu of all three {fmt(rows, 'cpu')}; checks run per land, by run: {checks or 'none'}", flush=True)


def measure_suites(scratch, runs):
    measure_commands(scratch, runs, (("go", ["scripts/test-go.sh", "-count=1"]), ("vitest", ["pnpm", "--dir", "app", "exec", "vitest", "run"])))


def measure_daemon_packages(scratch, runs):
    packages = ("./internal/daemon", "./cmd/attnd")
    measure_commands(scratch, runs, tuple((package, ["scripts/test-go.sh", "--packages", "-count=1", package]) for package in packages))


def measure_commands(scratch, runs, suites):
    path = scratch.worktrees[0]
    git("reset", "--quiet", "--hard", scratch.base, cwd=path)
    for name, command in suites:
        rows = []
        for run in range(1, runs + 1):
            with measurement(f"{name} suite run {run}") as load:
                result = run_measured(command, path, scratch.env, scratch.logs / f"suite-{name.replace('/', '_').strip('._')}-{run}.log")
            result.update(load)
            result["ok"] = result["exit"] == 0
            print(f"  {name} suite run {run}: {describe(result)}, {fmt_load(result)}, log {result['log']}", flush=True)
            rows.append(result)
        print(f"full {name} suite, {outcome(rows)}: wall {fmt(rows, 'wall')}, cpu {fmt(rows, 'cpu')}", flush=True)


def run_in_scope(command, cwd, env, log_path):
    reader = 'stat="$1"; shift; status=0; "$@" || status=$?; cat "/sys/fs/cgroup$(cut -d: -f3 /proc/self/cgroup)/cpu.stat" >"$stat"; exit $status'
    stat_path = Path(f"{log_path}.cpu")
    scoped = ["systemd-run", "--user", "--scope", "--quiet", "--collect", "sh", "-c", reader, "sh", str(stat_path), *command]
    with open(log_path, "w") as out:
        start = time.monotonic()
        process = subprocess.run(scoped, cwd=cwd, env=env, stdout=out, stderr=subprocess.STDOUT)
        wall = time.monotonic() - start
    usage = dict(line.split() for line in stat_path.read_text().splitlines())
    return {"exit": process.returncode, "wall": wall, "cpu": int(usage["usage_usec"]) / 1e6, "log": str(log_path)}


def measure_acceptance(source, runs, worker_counts, logs):
    env = {key: value for key, value in os.environ.items() if not key.startswith("ATTN_")}
    for workers in [1, *[count for count in worker_counts if count != 1]]:
        rows = []
        for run in range(1, runs + 1):
            artifacts = logs / f"acceptance-w{workers}-{run}"
            command = ["node", "app/scripts/real-app-harness/run-parallel-matrix.mjs", "--workers", str(workers)]
            with measurement(f"acceptance {workers} workers run {run}") as load:
                result = run_in_scope(command, source, {**env, "ATTN_REAL_APP_ARTIFACTS_DIR": str(artifacts)}, logs / f"acceptance-w{workers}-{run}.log")
            result.update(load)
            result["ok"] = result["exit"] == 0
            digest = artifacts / "matrix-digest.txt"
            failed = len(re.findall(r"^s\d+  FAIL", digest.read_text(), re.MULTILINE)) if digest.exists() else "unknown"
            print(f"  acceptance {workers} workers run {run}: {describe(result)}, {failed} scenarios failed, {fmt_load(result)}, log {result['log']}", flush=True)
            rows.append(result)
        print(f"acceptance matrix, {workers} workers, {outcome(rows)}: wall {fmt(rows, 'wall')}, cpu {fmt(rows, 'cpu')}", flush=True)


def gh(path, source):
    return json.loads(subprocess.run(["gh", "api", path], cwd=source, check=True, capture_output=True, text=True).stdout)


def seconds(start, end):
    parse = lambda stamp: time.mktime(time.strptime(stamp, "%Y-%m-%dT%H:%M:%SZ"))
    return parse(end) - parse(start)


def ci_chains(run, jobs):
    named = lambda prefix: [job for job in jobs if job["name"] == prefix or job["name"].startswith(prefix + " (")]
    go_tests = named("Daemon")
    go_lint = named("Go vet and lint") or go_tests
    shards = [job for job in jobs if job["name"].startswith("App acceptance shard ")]
    build = next((job for job in jobs if job["name"] == "App acceptance build"), None)
    macos = named("App acceptance macOS")
    chain = [*go_tests, *go_lint, build, *macos, *shards]
    if not go_tests or not shards or not macos or any(job is None or job["conclusion"] != "success" for job in chain):
        return None
    step = lambda job, prefix: next(seconds(s["started_at"], s["completed_at"]) for s in job["steps"] if s["name"].startswith(prefix))
    job_wall = lambda job: seconds(job["started_at"], job["completed_at"])
    span = lambda group: seconds(min(job["started_at"] for job in group), max(job["completed_at"] for job in group))
    started = [job for job in jobs if job["started_at"] and job["conclusion"] != "skipped"]
    wait = lambda job: seconds(job["created_at"], job["started_at"])
    waits = sorted(started, key=wait)
    return {
        "ok": True,
        "run": run["id"],
        "wall": seconds(run["created_at"], run["updated_at"]),
        "queue": seconds(run["created_at"], min(job["started_at"] for job in started)),
        "job_wait": wait(waits[-1]),
        "job_wait_name": waits[-1]["name"],
        "runner_minutes": sum(job_wall(job) for job in started if job["completed_at"]) / 60,
        "go_tests": span(go_tests),
        "go_test_runners": len(go_tests),
        "go_test_step_max": max(step(job, "Test") for job in go_tests),
        "go_vet": step(go_lint[0], "Vet"),
        "go_lint": step(go_lint[0], "Lint"),
        "linux_acceptance": seconds(build["started_at"], max(job["completed_at"] for job in shards)),
        "linux_build": job_wall(build),
        "linux_shards": len(shards),
        "linux_shard_max": max(job_wall(job) for job in shards),
        "macos_acceptance": span(macos),
        "macos_jobs": len(macos),
        "macos_wait": max(wait(job) for job in macos),
        "macos_build": max(step(job, "Build the App acceptance tree") for job in macos),
        "macos_scenarios": max(step(job, "Run native") for job in macos),
    }


def measure_ci(source, run_ids):
    jobs_of = lambda run: gh(f"repos/{{owner}}/{{repo}}/actions/runs/{run['id']}/attempts/{run['run_attempt']}/jobs?per_page=100", source)["jobs"]
    if run_ids:
        runs = [gh(f"repos/{{owner}}/{{repo}}/actions/runs/{run_id}", source) for run_id in run_ids]
        label = f"runs {' '.join(run_ids)}"
    else:
        runs = gh("repos/{owner}/{repo}/actions/workflows/ci.yml/runs?branch=main&event=push&status=success&per_page=50", source)["workflow_runs"]
        label = f"last {CI_GREEN_RUNS} green main runs with every chain"
    rows = []
    for run in runs:
        row = ci_chains(run, jobs_of(run))
        if row is None:
            if run_ids:
                log(f"run {run['id']} has a chain that did not succeed; left out")
            continue
        rows.append(row)
        if len(rows) == CI_GREEN_RUNS and not run_ids:
            break
    for row in rows:
        print(
            f"  ci run {row['run']}: wall {row['wall']:.0f}s, queue before the first job {row['queue']:.0f}s,"
            f" longest job queue wait {row['job_wait']:.0f}s ({row['job_wait_name']}), {row['runner_minutes']:.0f} runner-minutes,"
            f" go tests {row['go_tests']:.0f}s on {row['go_test_runners']} runners,"
            f" linux acceptance {row['linux_acceptance']:.0f}s with {row['linux_shards']} shards,"
            f" macos acceptance {row['macos_acceptance']:.0f}s on {row['macos_jobs']} runners, longest macos queue wait {row['macos_wait']:.0f}s",
            flush=True,
        )
    if not rows:
        return
    column = lambda key, unit="s": fmt(rows, key, unit)
    print(f"ci.yml, {label}: wall {column('wall')}, queue before the first job {column('queue')}, longest job queue wait {column('job_wait')}, runner time {column('runner_minutes', ' min')}", flush=True)
    print(f"  go tests, first runner start to last runner end {column('go_tests')}: slowest Test step {column('go_test_step_max')}; vet {column('go_vet')}, lint {column('go_lint')}", flush=True)
    print(f"  linux acceptance, build start to last shard end {column('linux_acceptance')}: build job {column('linux_build')}, slowest shard {column('linux_shard_max')}", flush=True)
    print(f"  macos acceptance, first job start to last job end {column('macos_acceptance')}: longest queue wait {column('macos_wait')}, slowest build {column('macos_build')}, slowest scenarios {column('macos_scenarios')}", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--keep", action="store_true", help="keep the scratch folder and its logs")
    parser.add_argument("--ci-run", action="append", default=[], metavar="ID", help="measure these ci.yml runs, such as a pull request's, instead of the last green main runs")
    parser.add_argument("--workers", default="4", help="acceptance: comma-separated worker counts to measure after 1 worker (default 4)")
    parser.add_argument("baselines", nargs="*", default=list(BASELINES))
    args = parser.parse_args()
    unknown = set(args.baselines) - set(BASELINES)
    if unknown:
        parser.error(f"no baseline {', '.join(sorted(unknown))}; baselines: {' '.join(BASELINES)}")
    source = Path(git("rev-parse", "--show-toplevel"))
    selected = [b for b in BASELINES if b in args.baselines]
    print(f"host {os.uname().nodename}, {os.cpu_count()} cores, {time.strftime('%Y-%m-%d %H:%M %Z')}", flush=True)

    local = [b for b in selected if b not in ("ci", "acceptance")]
    if local:
        if git("status", "--porcelain", cwd=source):
            log("the checkout has uncommitted changes; the local baselines measure its HEAD commit without them")
        root = Path(tempfile.mkdtemp(prefix=".attn-verification-cost.", dir=source.parent))
        try:
            scratch = Scratch(source, root, 3 if "lands3" in local else 1)
            print(f"measuring HEAD {scratch.base[:10]}, scratch {root}", flush=True)
            if "land" in local:
                measure_land(scratch, args.runs)
            if "lands3" in local:
                measure_lands3(scratch, args.runs)
            if "suites" in local:
                measure_suites(scratch, args.runs)
            if "daemon" in local:
                measure_daemon_packages(scratch, args.runs)
        finally:
            if args.keep:
                log(f"kept {root}")
            else:
                shutil.rmtree(root, ignore_errors=True)
    if "ci" in selected:
        measure_ci(source, args.ci_run)
    if "acceptance" in selected:
        logs = Path(tempfile.mkdtemp(prefix=".attn-acceptance-cost.", dir=source.parent))
        print(f"acceptance: measuring the installed acc instances from {source}, logs {logs}", flush=True)
        measure_acceptance(source, args.runs, [int(count) for count in args.workers.split(",")], logs)


if __name__ == "__main__":
    main()
