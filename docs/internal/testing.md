# Testing

Committed tests guard **promises**: behavior relied on by users, the agents
attn runs, clients, or a later version reading today's data (protocol,
durability, CLI, screen, start-cost performance, prompt compatibility fixtures,
and specified logic). Check throwaway work by running it; keep scratch tests
out of commits.

See the `write-test` skill (`.agents/skills/write-test`) for harness entry
points and gotchas, and `AGENTS.md` ("Test safety") for config isolation.

## Depend only on promises

A committed test depends only on promises, never internals. If rewriting the
code with the same behavior would break the test, delete or replace the test
rather than repairing it.

When a promise cannot be reached from outside, extend the harness (`testworld`,
`fakeagent`, `attn-testkit`, the real-app harness) with something that models
the outside world (agent, user, clock, network), never something that sets
attn's internal state.

The sole exception is `internal/pausepoint` (`pausepoint.At(name)` for a moment,
`pausepoint.Done(name, err)` for a completed step) and `ATTN_CRASH_AT` to reach
a point between two steps of one operation:

- Arm only points that at least one run reaches (unreached points fail cleanup).
- To fault an operation, hold the operation at its own point, arm the fault,
  then release—never rely on holding an unrelated step like a fake agent boot.

## Kinds of test

Pick the **first** kind in this order that can observe the promise. Fake nothing
beyond what the kind allows.

| Order | Kind | Enters / observes | Faked | Budget |
| --- | --- | --- | --- | --- |
| 1 | **Wire** (default) | Protocol from one side (real daemon + test client, or real app on windowless Rune `Runtime` + scripted daemon). | Agent binaries, external services, clock, network. | Well under 1 s |
| 2 | **Stack** | CLI or protocol across real processes (daemon, PTY host/workers, CLI, headless app via `Stack.StartNativeApp`). | Agent binaries, external services, clock. | A few seconds |
| 3 | **Scenario** | Native input on the packaged app; observes screen, protocol or CLI (never the DB). | Agent binaries, external services, clock. | CI (run locally to develop/repro) |
| 4 | **Kernel** | One function with a domain spec whose input space is too large for the protocol. Must be a table, corpus, or `rapid` property (never one hand-picked example). Migration tests build pre-migration DBs directly. | Nothing. | Milliseconds |

A bug fix adds a regression test at the first kind that reproduces what the
user saw.

### Scenario

Packaged-app scenarios live in `app/scripts/real-app-harness/` (see
[its AGENTS.md](../../app/scripts/real-app-harness/AGENTS.md)) and run under
Xvfb in CI. Verdicts come from the screen, the protocol, or the CLI, never from
the daemon's database.

## Time

No test sleeps or polls. Move a clock (`inBubble` + `synctest`, stepped clocks,
injected Rust clock, `stepClock` in scenarios) or await a real signal.

Enforced automatically:

- `make lint-go` (`scripts/testlint`): rejects `time.Sleep`, `time.Tick`,
  `time.NewTicker`, bare `<-time.After`, non-`fakeagent.HangGuard` deadlines
  outside `synctest.Test`, and running `cargo` from Go tests.
- `native/clippy.toml`: rejects `std::thread::sleep`, `Instant::now`,
  `SystemTime::now`.
- `make lint-js` (`.oxlintrc.json`): rejects `setTimeout` / `Bun.sleep` waits.

Kernel socket deadlines, OS file events, syncthing timers and the Rust shared
PTY host keep real time.

## Running Go tests

Always use `./scripts/test-go.sh --packages -run TestName ./pkg` rather than
bare `go test`: it builds or reuses `libghostty-vt` and the Rust test binaries
(`attn`, `attn-pty-host`, host variants, `attn-app`) from the machine-wide
build store (`~/.cache/attn/rust-builds` or `~/Library/Caches/attn/rust-builds`,
7-day TTL, safe to delete), sets `CGO_CFLAGS` from `go-cgo-cflags`, isolates
the outer data dir, and records receipts ([Test history](test-history.md)).

- Before running bare `go` commands in a fresh checkout, run
  `./scripts/build-libghostty-vt.sh`.
- The build store refuses builds that read undeclared files or env vars and
  names what to add to the recipe in `scripts/lib/rust-build-store.sh`.
- Sharded runs: `./scripts/test-go.sh --job daemon-3 --job attn-1`.
