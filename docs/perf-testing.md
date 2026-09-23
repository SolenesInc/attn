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

## Daemon profiling

Start a non-production daemon with `ATTN_PPROF=1` (or a port) for a loopback
pprof server:

```bash
curl -s http://127.0.0.1:6060/debug/vars | jq
go tool pprof -top http://127.0.0.1:6060/debug/pprof/heap
```

`/debug/vars` lists `worker_pids`; per-session memory lives in those workers,
not the daemon heap.
