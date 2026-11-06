# Performance testing

One check here fails a land: the limit on the work attn does at start. Timing
can't gate a land, because other agents load the machine, so that check counts
allocations instead. For the costs a count can't see, the `verify-perf` skill
has the reader of a landing look for them. The other tools on this page record
trends and fail nothing.

## Start cost

Every CLI command and agent hook runs package init before doing anything else.
`TestEveryCommandStartsWithoutPayingForInitItNeverUses` runs on every Go land
and fails when `attn version` makes more than `maxPackageInitAllocs` init
allocations, naming the heaviest packages. `lint-go` also refuses jsonschema
and Prometheus, which the start work dropped, because the limit alone would let
Prometheus's 1,429 allocations back in.

| `GODEBUG=inittrace=1 attn version`, Linux unless named | Init allocations |
| --- | --- |
| Today, 5db0778fa | 10,373 |
| macOS arm64, 25c2331c | 10,537 |
| Today plus Prometheus | 11,802 |
| Today plus jsonschema | 32,725 |
| Before the start work, 6496f367b | 39,179 |

The count moves by a few allocations between runs, even at load 16, while init
time moves by a third. Raise the limit only after you can name what the extra
work buys, and update this table with the new measurement.

## Go benchmarks

```bash
go test -run '^$' -bench . -benchmem ./internal/daemon/ ./internal/transcript/
```

`allocs/op` is the stable signal. Compare `ns/op` only between runs on the same machine.

## Real-app memory

These hand-run [harness](../../app/scripts/real-app-harness/AGENTS.md) scenarios
measure the whole attn process tree and compare against the machine's own
baseline (`~/.attn-perf-registry/`, or `perf-baselines.json` for committed
reference machines). A regression sets `ok: false` in the verdict without
failing the exit code.

| Scenario | Measures | Command |
| --- | --- | --- |
| Baseline | RSS with N sessions | `pnpm --dir app run real-app:scenario-perf-baseline -- --sessions 8 --stream 2` |
| Cold and warm | RSS fresh and after a workload | `ATTN_HARNESS_INSTANCE=perf pnpm --dir app run real-app:scenario-perf-cold-warm -- --sessions 8` |
| Leak soak | Retained-RSS slope across session cycles | `ATTN_HARNESS_INSTANCE=perf pnpm --dir app run real-app:scenario-perf-leak-soak -- --cycles 12` |

The cold and warm scenario and the leak soak scenario wipe their data, so they
need a dedicated instance
(`make install INSTANCE=perf`). Add `--record-baseline` only after an intended
footprint change, never to silence a regression you don't understand.

## Memory by version

Each `process` record in the analytics `sample` stream
([Telemetry](../guide/reference/telemetry.md#process-samples)) holds memory by
process role and its workload, and is stored with the build that took it. One
sample is weak evidence: the same 4 pane workload on a Mac ranged 440-760 MB
depending on when JavaScriptCore collected. Compare medians per build at
comparable workload. Builds from source between releases share a version, so
group by commit as well. A new build needs a few hours of use before its median
means anything.

These queries read the home's samples. An outpost's arrive under a stream named
for it, such as `sample_m9c1e04b7aa21`: change the stream filter to include them.
A Mac app started from a terminal, as `bridge --launch` does, has no `webkit_*`
entries, so to sample WebKit on an instance, start the app through launchd:
`launchctl submit -l <label> -- <app executable>`, and `launchctl remove <label>`
before you quit it.

Across installs, query attn-ingest's ClickHouse, which keeps records past the
outbox's 7 days:

```sql
SELECT
  build_version, build_commit, build_os,
  JSONExtractString(process, 'role') AS role,
  JSONExtractInt(props, 'workload', 'panes_drawn') AS panes,
  intDiv(JSONExtractInt(props, 'workload', 'agents'), 5) * 5 AS agents_from,
  count() AS samples,
  round(median(if(JSONExtractUInt(process, 'footprint_bytes') > 0,
    JSONExtractUInt(process, 'footprint_bytes'),
    JSONExtractUInt(process, 'resident_bytes'))) / 1048576) AS median_mib
FROM events FINAL
ARRAY JOIN JSONExtractArrayRaw(props, 'processes') AS process
WHERE stream = 'sample' AND name = 'process'
GROUP BY build_version, build_commit, build_os, role, panes, agents_from
ORDER BY role, panes, agents_from, build_version, build_commit
```

On one machine, the same from the outbox of an instance, or of a copy of
production, which reaches back 7 days:

```bash
sqlite3 -readonly "$(attn instance resolve --field dataDir)/attn.db" <<'SQL'
WITH memory AS (
  SELECT
    json_extract(b.build, '$.version') AS version,
    substr(json_extract(b.build, '$.commit'), 1, 9) AS commit_id,
    json_extract(p.value, '$.role') AS role,
    json_extract(o.record, '$.props.workload.panes_drawn') AS panes,
    json_extract(o.record, '$.props.workload.agents') / 5 * 5 AS agents_from,
    coalesce(json_extract(p.value, '$.footprint_bytes'), json_extract(p.value, '$.resident_bytes')) / 1048576 AS mib
  FROM analytics_outbox o
  JOIN analytics_builds b ON b.id = o.build_id,
    json_each(o.record, '$.props.processes') p
  WHERE o.stream = 'sample'
), ranked AS (
  SELECT *,
    row_number() OVER (PARTITION BY version, commit_id, role, panes, agents_from ORDER BY mib) AS rank,
    count(*) OVER (PARTITION BY version, commit_id, role, panes, agents_from) AS samples
  FROM memory
)
SELECT version, commit_id, role, panes, agents_from, samples, mib AS median_mib
FROM ranked WHERE rank = (samples + 1) / 2
ORDER BY role, panes, agents_from, version, commit_id;
SQL
```

## Terminal streaming

`scenario-perf-pty-stream` measures what streaming terminal output costs while
three shells print `seq 1 400` every 20 ms, with the shared terminal host on
unless `--shared-host false` turns it off. With it on, the app streams only the
terminals it draws. It prints one JSON line per run and a summary with the
median, min and max of each number: CPU as a share of a core for the app,
WebContent, WebKit Networking, WebKit GPU, the daemon and the PTY host;
WebSocket messages and bytes a second; events a second per terminal from the
daemon and from the app; the terminals `daemon.log` shows streaming to the app;
key-to-echo p50 and p90 in a quiet shell under the load; and, for `hidden`, how
long the streaming panes take to show the daemon's screen once the user returns
to them, and when a command typed during that switch reaches its terminal.

```bash
ATTN_HARNESS_INSTANCE=<name> pnpm --dir app run real-app:scenario-perf-pty-stream -- \
  --workload hidden|drawn|unmounted --streams 3 --seconds 30 --runs 5 [--shared-host false]
```

`drawn` draws the streams as splits beside the quiet shell, `hidden` puts them
on the previous desktop, and `unmounted` on a desktop that is neither current
nor previous. `--profile-dir <dir>` adds `sample` profiles of the PTY host and
WebContent for each window, and `--pprof-port <port>` restarts the daemon with
`ATTN_PPROF` to add its CPU profile. Profiling costs CPU, so profiled runs are not baselines.

## A copy of production

The garden and session tools measure a copy of a real database in a named
instance. `scripts/gardenpushperf/copy-production.sh <name> [<source db>]`
copies the source read-only, production's `~/.attn/attn.db` by default, and
turns off what the copy would otherwise reach: the real Notebook root, crew
homes, conversations, worktree removal, outposts and every automation. A copied
crew member's home starts empty in the copy's data folder. Every copied session
moves to the outpost `copy source`, which does not exist, so the copy keeps its
records and tiles, shows them offline and never runs them, whether its daemon
starts alone or an app opens on it. No monitor watches a copied session, so a
copy's idle numbers leave monitors out. A session you start in the copy runs
normally. Pass a source to measure a copy made on another machine, such as a Mac
copy under `~/perf-copies/` on ai-sandbox.
`attn instance resolve --field dataDir` and `--field wsPort` give the copy's
data directory and port.

Run the tools, and the scripts that start them, in the instance's environment
(`eval "$(attn instance-env <name>)"`). They refuse to run without a
non-production instance. Given `-vars`, they refuse when that endpoint's daemon
is not the instance's, and `gardenpushperf` and `sessionfactperf` connect to
the WebSocket listener it reports.

## Garden push

`scripts/gardenpushperf` measures what one garden update costs on a real
garden. Start a copy's daemon and plant a seed to move.

The Go tool reports bytes and move-to-update latency for notes and for wither
and replant moves:

```bash
go run ./scripts/gardenpushperf -seed <id> -n 20
```

To measure the daemon's CPU per garden mutation instead, start the daemon with
`ATTN_PPROF=<port>`, plant the seed under a watched plot, and pass the
diagnostics endpoint and the garden's profile:

```bash
go run ./scripts/gardenpushperf -seed <id> -profile <profile id> \
  -vars http://127.0.0.1:<port>/debug/vars -n 10
```

Each run makes six mutations (a ringing note, a plain note, a check removed and
restored, a wither and a replant), waits for the daemon to return to idle, and
reports CPU milliseconds per mutation above the idle rate.

The app side needs a bundle built against that instance. `app-push.mjs` then
reports the socket handler, message-to-next-frame, and `JSON.parse` per update
in headless Chromium (`PROFILE=1` adds a CPU profile):

```bash
VITE_CLIENT_TOKEN=$(cat <instance data dir>/client-token) VITE_DAEMON_PORT=<instance ws port> \
  pnpm --dir app exec vite build --outDir <dir>
pnpm --dir app exec vite preview --outDir <dir> --port <port>
node scripts/gardenpushperf/app-push.mjs http://localhost:<port>/ <id> 20
```

## Garden reads

`scripts/gardenreadperf` measures what reading the garden costs on a copy. It
sends `seed_list` and `seed_ready` with `all` straight to the daemon's socket,
so process start and CLI decoding stay out of the number, and reports the
median, minimum and maximum time to first byte and to the whole reply, the
reply's bytes and its item counts. `-show <seed id>` and `-search <query>` add
`seed_show` and `seed_search`:

```bash
go run ./scripts/gardenreadperf -profile <profile id> -n 20 -show <seed id> -search render
```

Start the daemon with `ATTN_PPROF=<port>` and pass
`-vars http://127.0.0.1:<port>/debug/vars` to add the daemon's CPU per request
above its idle rate. The tool first waits for the daemon to settle below 20 CPU
milliseconds a second, since a daemon fresh from start uses hundreds. Each
`-cli <attn binary>` also runs `seed ls` and `seed ready --all` through that
binary and reports wall and CPU time. Several `-cli` binaries run in
interleaved rounds, so load on the machine reaches each one alike. CPU is the
number to compare when other agents load the machine.

To compare two builds, run each build's daemon on the same copy in turn. For
what limits a read, take a CPU profile while a loop of `seed ls` runs, and read
`handleSeedList` with `go tool pprof -top -cum`:

```bash
curl -s -o cpu-ls.pprof "http://127.0.0.1:<port>/debug/pprof/profile?seconds=20" &
end=$((SECONDS+20)); while [ $SECONDS -lt $end ]; do attn seed ls --profile <profile id> >/dev/null; done; wait
```

## Session facts

`scripts/sessionfactperf` measures what one session fact, such as a state or
cost change, costs the daemon and the app. It changes the
`session_cost.billed_as.codex-auto-review` setting to its built-in value, so
the daemon reprices every session with usage and sends one
`session_state_changed` for each, while the usage the app shows stays the same.

Make a copy and start its daemon with `ATTN_PPROF=<pprof port>`. Each run is one reprice
burst. The tool waits for the daemon to return to idle and reports CPU
milliseconds per fact above the idle rate, the gaps between frames and their
sizes:

```bash
go run ./scripts/sessionfactperf -vars http://127.0.0.1:<pprof port>/debug/vars -n 10
```

`-pace 10s -for 60s` sends one reprice every 10 seconds for a minute instead and
reports the daemon's share of a core. `-frames <file>` writes every
`session_state_changed` frame of the bursts to a file, one per line, to compare
the wire output of two builds on the same copy. Keep a backup of the copied
database (`sqlite3 <data dir>/attn.db ".backup <file>"`) and restore it before
each daemon start.

The worktree sweep's read-only pass still runs on the copy and moves the idle
rate by tens of milliseconds a second, so whole-daemon CPU per fact is noisy.
For a before-and-after comparison, take a profile during the bursts instead,
`/debug/pprof/profile` for CPU and two `/debug/pprof/allocs` snapshots for
allocations, and read `projectSessionStateChanged` with `go tool pprof -top
-cum`, using `-base <first snapshot>` for allocations.

`app-session.mjs` measures the app's main-thread time, React commits and paints
per fact in headless Chromium. It runs the Go tool for the bursts. Serve a
bundle built against the instance, as for the garden push. A dev server also
names the components each commit renders, and needs the instance's port:

```bash
VITE_DAEMON_PORT=<instance ws port> pnpm --dir app exec vite --port <port>
node scripts/sessionfactperf/app-session.mjs http://localhost:<port>/ \
  http://127.0.0.1:<pprof port>/debug/vars 10
```

## Daemon profiling

To get a loopback pprof server, start a non-production daemon with
`ATTN_PPROF=1` or with a port as the value:

```bash
curl -s http://127.0.0.1:6060/debug/vars | jq
go tool pprof -top http://127.0.0.1:6060/debug/pprof/heap
```

`/debug/vars` gives the daemon's WebSocket listener in `ws_addr` and lists
`worker_pids`. Per-session memory lives in those workers, not the daemon heap.
`/debug/vars` also reports `doc_subscriptions`, which counts
the live document queries that connected clients hold and drops when a client
disconnects.

## Verification cost

`scripts/measure-verification-cost.py` measures what checking a change costs:
one land, three concurrent lands on one machine, the full Go and Vitest suites,
and CI. Run it to show that a change to `scripts/land.sh`, the test runners or
`ci.yml` makes verification cheaper, on the same machine before and after. The
local baselines measure the checkout's committed `HEAD`:

```bash
scripts/measure-verification-cost.py                 # every baseline, 3 runs each
scripts/measure-verification-cost.py --runs 1 land   # one baseline
```

Each local baseline prints every run's outcome, wall seconds, CPU-seconds and
1-minute load average before and after it, then the median and range over the
runs that succeeded, or over the failed runs, labelled, when none did. Lands
also print how many times each land ran its checks. Runs on one machine take
turns through a lock on `/tmp/attn-verification-cost.lock`, so several agents
can measure at once without disturbing each other; a waiting run prints who
holds the lock.

The lands run in a scratch clone beside the checkout, whose `origin` is a local
bare repository, with a copy of this checkout's test-history mirror and no
service token, so they never push to the real `main` or upload receipts. `--keep` keeps the
scratch folder and each run's log. The `ci` baseline reads the last 5 green
`ci.yml` runs on `main` through `gh`, or the runs that `--ci-run <id>` names,
such as a pull request's. It prints the wall time, each chain's time, the
longest wait of a job for a runner, which grows when a run has more jobs than
the free plan runs at once, and the runner time all the run's jobs used.
