# Performance testing

Wall-clock timing cannot gate a landing on a loaded machine. The only landing
gate is `TestEveryCommandStartsWithoutPayingForInitItNeverUses`
(`cmd/attnd/cli_start_stack_test.go`), which caps `GODEBUG=inittrace=1 attn version`
allocations at `maxPackageInitAllocs` (current baseline ~4,240 Linux / ~4,341
macOS arm64; `make lint-go` also bans heavy init dependencies by name). Raise
the limit only when you can name what the extra init work buys, and record the
measurement on the constant. For other performance checks, see the
`verify-perf` skill.

## Tool index

Pass `--help` to any script for flags and output fields.

| Surface | Command | Notes |
| --- | --- | --- |
| Go microbenchmarks | `go test -run '^$' -bench . -benchmem ./internal/daemon/ ./internal/transcript/` | Compare `allocs/op`; compare `ns/op` only on the same machine. |
| App gates (macOS) | `ATTN_HARNESS_INSTANCE=<name> node app/scripts/real-app-harness/scenario-perf-gates.mjs` | Uses `proc_pid_rusage`; compare retired instructions when CPU core placement differs. |
| Terminal streaming | `ATTN_HARNESS_INSTANCE=<name> pnpm --dir app run real-app:scenario-perf-pty-stream -- --workload drawn\|hidden\|unmounted` | Add `--profile-dir` / `--pprof-port` for profiles (not for baselines). |
| Real-app memory | `pnpm --dir app run real-app:scenario-perf-{baseline,cold-warm,leak-soak}` | `cold-warm` and `leak-soak` wipe their instance; use `INSTANCE=perf`. Pass `--record-baseline` only for intentional footprint changes. |
| Garden push / read | `go run ./scripts/gardenpushperf` / `go run ./scripts/gardenreadperf` | Run in a production-copy instance's env with `-vars http://127.0.0.1:<port>/debug/vars`. |
| Session facts | `go run ./scripts/sessionfactperf -vars http://127.0.0.1:<port>/debug/vars -n 10` | For before/after, profile `/debug/pprof/profile` and `/debug/pprof/allocs` (`projectSessionStateChanged`); worktree sweeps add idle noise. |
| Shared PTY host | `bash scripts/measure-pty-host.sh <host-bin> <name>` | See `pty-host/AGENTS.md`. |
| App over a real link to a remote home | `node app/scripts/real-app-harness/remote-home-traffic.mjs` | Bytes from `scripts/wirecount` as the daemon's ssh ProxyCommand; connect, idle, reconnect, echo under flood, Cmd+P, upload. |
| Land / CI cost | `scripts/measure-verification-cost.py [--runs 1 land]` | Uses `/tmp/attn-verification-cost.lock`; runs in an isolated scratch clone. |

## Measuring on a copy of production

```sh
scripts/gardenpushperf/copy-production.sh <name> [<source db>]
eval "$(attn instance-env <name>)"
ATTN_PPROF=6060 attn daemon ensure
```

- `copy-production.sh` copies `~/.attn/attn.db` read-only and neutralizes
  notebooks, crew homes, outposts and automations. Copied sessions move to a
  non-existent `copy source` outpost so they stay offline without monitors
  (a copy's idle numbers therefore exclude monitors).
- `ATTN_PPROF=<port>` serves `/debug/vars` (`ws_addr`, `worker_pids`,
  `doc_subscriptions`) and `/debug/pprof/{heap,allocs,profile}` on loopback.
  Per-session memory lives in the PTY host, not the daemon heap.
- Before repeating a daemon run on a copy, back up and restore the copied DB
  with `sqlite3 <dataDir>/attn.db ".backup <file>"`.

## App frames and version memory

- On a named instance (or `ATTN_AUTOMATION=1`), quitting the app writes frame
  stats (`rune-perf`) to `<appLocalDataDir>/debug/perf/perf.json` (and slow
  frames >16.7 ms to `perf.log` / `perf-incident-NN.json`). An idle window
  draws nothing, so frames recorded during a burst equal the burst's frames.
- For memory across versions, query `process_samples` in attn-ingest's
  ClickHouse (or `analytics_outbox` where `stream = 'sample'` in a local DB
  copy) grouped by `build_version, build_commit, role, panes_drawn`.
- **macOS WebKit trap:** an app launched from a terminal (including
  `bridge --launch`) reports no `webkit_*` process entries. To sample browser
  tile WebKit memory on an instance, launch through launchd:
  `launchctl submit -l <label> -- <app executable>`, and `launchctl remove <label>`
  before quitting.
