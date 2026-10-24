# Performance testing

Performance checks record trends; none fails a PR.

## Go benchmarks

```bash
go test -run '^$' -bench . -benchmem ./internal/daemon/ ./internal/transcript/
```

`allocs/op` is the stable signal; `ns/op` only compares on the same machine.
The `Bench A/B` workflow runs base and head on one runner for PRs touching
those packages and posts the `benchstat` delta in its summary.

## Real-app memory

These hand-run [harness](../app/scripts/real-app-harness/AGENTS.md) scenarios
measure the whole attn process tree and compare against the machine's own
baseline (`~/.attn-perf-registry/`, or `perf-baselines.json` for committed
reference machines). A regression sets `ok: false` in the verdict without
failing the exit code.

| Scenario | Measures | Command |
| --- | --- | --- |
| Baseline | RSS with N sessions | `pnpm --dir app run real-app:scenario-perf-baseline -- --sessions 8 --stream 2` |
| Cold/warm | RSS fresh and after a workload | `ATTN_HARNESS_INSTANCE=perf pnpm --dir app run real-app:scenario-perf-cold-warm -- --sessions 8` |
| Leak soak | Retained-RSS slope across session cycles | `ATTN_HARNESS_INSTANCE=perf pnpm --dir app run real-app:scenario-perf-leak-soak -- --cycles 12` |

Cold/warm and leak soak wipe their data, so they need a dedicated instance
(`make install INSTANCE=perf`). Add `--record-baseline` only after an intended
footprint change, never to silence a regression you don't understand.

## Garden push

`scripts/gardenpushperf` measures what one garden update costs on a real
garden. Copy the database into a named instance (`sqlite3
"file:$HOME/.attn/attn.db?mode=ro" ".backup <instance data dir>/attn.db"`),
start its daemon, and plant a seed to move. The Go tool reports bytes and
move-to-update latency for notes and for wither/replant moves:

```bash
ATTN_INSTANCE=<name> go run ./scripts/gardenpushperf -seed <id> -n 20
```

`attn instance resolve --field dataDir` and `--field wsPort` give the data
directory and port. The app side needs a bundle built against that instance,
then reports the socket handler, message-to-next-frame and `JSON.parse` per
update in headless Chromium (`PROFILE=1` adds a CPU profile):

```bash
VITE_CLIENT_TOKEN=$(cat <instance data dir>/client-token) VITE_DAEMON_PORT=<instance ws port> \
  pnpm --dir app exec vite build --outDir <dir>
pnpm --dir app exec vite preview --outDir <dir> --port <port>
ATTN_INSTANCE=<name> node scripts/gardenpushperf/app-push.mjs http://localhost:<port>/ <id> 20
```

## Daemon profiling

Start a non-production daemon with `ATTN_PPROF=1` (or a port) for a loopback
pprof server:

```bash
curl -s http://127.0.0.1:6060/debug/vars | jq
go tool pprof -top http://127.0.0.1:6060/debug/pprof/heap
```

`/debug/vars` lists `worker_pids`; per-session memory lives in those workers,
not the daemon heap. Its `doc_subscriptions` counts the live document queries
connected clients hold, and falls back when a client disconnects.
