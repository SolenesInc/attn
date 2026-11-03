#!/usr/bin/env python3
"""Measure what verifying a change costs: lands, the full suites and CI.

usage: scripts/measure-verification-cost.py [--runs N] [--keep] [--ci-run ID]... [baseline...]

Baselines (all by default):
  land     one scripts/land.sh landing a one-file Go change
  lands3   three concurrent lands on one machine, each from its own worktree
  suites   scripts/test-go.sh -count=1, then pnpm --dir app exec vitest run
  ci       ci.yml wall time, its slowest chains and the longest job queue wait
           over the last 5 green main runs, or the runs --ci-run names

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

Lands never reach the real origin or the shared test history. They run in a
scratch clone of HEAD, beside this checkout, whose origin is a local bare
repository. Every worktree of the clone shares a scratch test-history spool
seeded with a copy of this checkout's mirror, so lands choose landing tests as a
real land would. Lands see an empty XDG_CONFIG_HOME, so test-history finds no
service token and sync stops before it connects. The suites record receipts
into the scratch spool, and nothing syncs it.
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
from contextlib import contextmanager
from pathlib import Path

BASELINES = ("land", "lands3", "suites", "ci")
LAND_PACKAGES = ("internal/daemon", "internal/store", "cmd/attn")
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
        self.land_env = {**self.env, "XDG_CONFIG_HOME": str(root / "config")}
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
        for i, path in enumerate(self.worktrees):
            git("reset", "--quiet", "--hard", self.base, cwd=path)
            package = path / LAND_PACKAGES[i % len(LAND_PACKAGES)]
            source_file = next(f for f in sorted(package.glob("*.go")) if not f.name.endswith("_test.go"))
            name = next(line.split()[1] for line in source_file.read_text().splitlines() if line.startswith("package "))
            change = package / f"verification_cost_{i + 1}.go"
            change.write_text(f"package {name}\n\nconst VerificationCostChange{i + 1} = {run}\n")
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


def fmt(rows, key):
    ok = [row[key] for row in rows if row["ok"]]
    if ok:
        return spread(ok)
    return f"no run succeeded; failed runs only {spread([row[key] for row in rows])}" if rows else "no runs"


def spread(values):
    return f"{statistics.median(values):.0f}s ({min(values):.0f}-{max(values):.0f})"


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
    path = scratch.worktrees[0]
    git("reset", "--quiet", "--hard", scratch.base, cwd=path)
    suites = (("go", ["scripts/test-go.sh", "-count=1"]), ("vitest", ["pnpm", "--dir", "app", "exec", "vitest", "run"]))
    for name, command in suites:
        rows = []
        for run in range(1, runs + 1):
            with measurement(f"{name} suite run {run}") as load:
                result = run_measured(command, path, scratch.env, scratch.logs / f"suite-{name}-{run}.log")
            result.update(load)
            result["ok"] = result["exit"] == 0
            print(f"  {name} suite run {run}: {describe(result)}, {fmt_load(result)}, log {result['log']}", flush=True)
            rows.append(result)
        print(f"full {name} suite, {outcome(rows)}: wall {fmt(rows, 'wall')}, cpu {fmt(rows, 'cpu')}", flush=True)


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
    macos = next((job for job in jobs if job["name"] == "App acceptance macOS"), None)
    chain = [*go_tests, *go_lint, build, macos, *shards]
    if not go_tests or not shards or any(job is None or job["conclusion"] != "success" for job in chain):
        return None
    step = lambda job, prefix: next(seconds(s["started_at"], s["completed_at"]) for s in job["steps"] if s["name"].startswith(prefix))
    job_wall = lambda job: seconds(job["started_at"], job["completed_at"])
    span = lambda group: seconds(min(job["started_at"] for job in group), max(job["completed_at"] for job in group))
    started = [job for job in jobs if job["started_at"] and job["conclusion"] != "skipped"]
    waits = sorted(started, key=lambda job: seconds(job["created_at"], job["started_at"]))
    return {
        "ok": True,
        "run": run["id"],
        "wall": seconds(run["created_at"], run["updated_at"]),
        "queue": seconds(run["created_at"], min(job["started_at"] for job in started)),
        "job_wait": seconds(waits[-1]["created_at"], waits[-1]["started_at"]),
        "job_wait_name": waits[-1]["name"],
        "go_tests": span(go_tests),
        "go_test_runners": len(go_tests),
        "go_test_step_max": max(step(job, "Test") for job in go_tests),
        "go_vet": step(go_lint[0], "Vet"),
        "go_lint": step(go_lint[0], "Lint"),
        "linux_acceptance": seconds(build["started_at"], max(job["completed_at"] for job in shards)),
        "linux_build": job_wall(build),
        "linux_shards": len(shards),
        "linux_shard_max": max(job_wall(job) for job in shards),
        "macos_acceptance": job_wall(macos),
        "macos_build": step(macos, "Build the App acceptance tree"),
        "macos_scenarios": step(macos, "Run native"),
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
            f" longest job queue wait {row['job_wait']:.0f}s ({row['job_wait_name']}),"
            f" go tests {row['go_tests']:.0f}s on {row['go_test_runners']} runners,"
            f" linux acceptance {row['linux_acceptance']:.0f}s with {row['linux_shards']} shards,"
            f" macos acceptance {row['macos_acceptance']:.0f}s",
            flush=True,
        )
    if not rows:
        return
    column = lambda key: fmt(rows, key)
    print(f"ci.yml, {label}: wall {column('wall')}, queue before the first job {column('queue')}, longest job queue wait {column('job_wait')}", flush=True)
    print(f"  go tests, first runner start to last runner end {column('go_tests')}: slowest Test step {column('go_test_step_max')}; vet {column('go_vet')}, lint {column('go_lint')}", flush=True)
    print(f"  linux acceptance, build start to last shard end {column('linux_acceptance')}: build job {column('linux_build')}, slowest shard {column('linux_shard_max')}", flush=True)
    print(f"  macos acceptance job {column('macos_acceptance')}: build {column('macos_build')}, scenarios {column('macos_scenarios')}", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--keep", action="store_true", help="keep the scratch folder and its logs")
    parser.add_argument("--ci-run", action="append", default=[], metavar="ID", help="measure these ci.yml runs, such as a pull request's, instead of the last green main runs")
    parser.add_argument("baselines", nargs="*", default=list(BASELINES))
    args = parser.parse_args()
    unknown = set(args.baselines) - set(BASELINES)
    if unknown:
        parser.error(f"no baseline {', '.join(sorted(unknown))}; baselines: {' '.join(BASELINES)}")
    source = Path(git("rev-parse", "--show-toplevel"))
    selected = [b for b in BASELINES if b in args.baselines]
    print(f"host {os.uname().nodename}, {os.cpu_count()} cores, {time.strftime('%Y-%m-%d %H:%M %Z')}", flush=True)

    local = [b for b in selected if b != "ci"]
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
        finally:
            if args.keep:
                log(f"kept {root}")
            else:
                shutil.rmtree(root, ignore_errors=True)
    if "ci" in selected:
        measure_ci(source, args.ci_run)


if __name__ == "__main__":
    main()
