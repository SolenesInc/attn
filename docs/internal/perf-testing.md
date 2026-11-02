# Performance testing

Performance checks record trends. None fails a PR.

## Go benchmarks

```bash
go test -run '^$' -bench . -benchmem ./internal/daemon/ ./internal/transcript/
```

`allocs/op` is the stable signal. Compare `ns/op` only between runs on the same machine.
For PRs that touch those packages, the `Bench A/B` workflow runs base and head
on one runner and posts the `benchstat` delta in the workflow summary.

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

## Garden push

`scripts/gardenpushperf` measures what one garden update costs on a real
garden. Copy the database into a named instance with
`scripts/gardenpushperf/copy-production.sh <name>`, which reads production
read-only and turns off what the copy would otherwise reach: the real Notebook
root, worktree removal, outposts, Tailscale and every automation. Start the
instance's daemon and plant a seed to move.

`gardenpushperf` and `sessionfactperf` change the daemon they reach, so run
them, and the scripts that start them, in the instance's environment. They
refuse to run without a non-production instance. Given `-vars`, they connect
to the WebSocket listener that endpoint's daemon reports, and refuse before
any change when that listener is not on the instance's port.

The Go tool reports bytes and move-to-update latency for notes and for wither
and replant moves:

```bash
eval "$(attn instance-env <name>)"
go run ./scripts/gardenpushperf -seed <id> -n 20
```

`attn instance resolve --field dataDir` and `--field wsPort` give the data
directory and port.

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

## Session facts

`scripts/sessionfactperf` measures what one session fact, such as a state or
cost change, costs the daemon and the app. It changes the
`session_cost.billed_as.codex-auto-review` setting to its built-in value, so
the daemon reprices every session with usage and sends one
`session_state_changed` for each, while the usage the app shows stays the same.

Copy production into a named instance with
`scripts/gardenpushperf/copy-production.sh <name>`, as for the garden push, and
start its daemon with `ATTN_PPROF=<pprof port>`. Each run is one reprice
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
each daemon start. Even then, `state_since` and `state_updated_at` differ
between starts, because each start marks the copy's sessions recoverable.

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
