# Testing

Tests guard the promises attn makes. To check your own work, run it: the
daemon over `wsctl`, the CLI, a non-production instance, a throwaway test. Keep
those scratch checks out of commits.

## Promises

A promise is behavior that someone outside the code relies on: the user, the
agents attn runs, the daemon's clients, or a later version of attn
reading today's data. attn's promises include:

- **Protocol**: the commands, responses, and events exchanged between the
  daemon and its clients, the app and the CLI.
- **Durability**: sessions, terminals, and the database survive app, daemon,
  and machine restarts, and upgrades between versions.
- **CLI**: commands, output, and exit codes that users and agents rely on.
- **Screen**: what the user sees, and what keyboard and pointer input does.
- **Performance**: commands start fast, idle attn stays quiet, and memory does
  not creep. A limit on start work fails a land, and benchmarks and memory
  scenarios track the rest. See [Performance testing](perf-testing.md).
- **Agent prompts**: the instructions attn sends to the agents it runs. Their
  compatibility fixtures change only for intentional wording edits, per
  [Prompt authoring](prompt-authoring.md#verify).
- **Specified logic**: rules with large input spaces that other promises
  depend on, such as state classification or the wire feed rewrite.

Most behavior is observable through the protocol, so daemon and app tests
both run against it.

## Depend only on promises

A committed test depends only on promises, never on internals. To check,
imagine rewriting the code from scratch with the same behavior. If the test
would break, it depends on internals. The `write-test` skill's Gotchas name
the shortcuts that most often break this rule, and what replaces each.

When a promise cannot be reached or observed through other promises, extend
the harness (`testworld`, `fakeagent`, the real-app harness) instead of
reaching inside from the test. A harness capability models something outside
attn, such as an agent, the user, the clock, or the network. It never sets
attn's own state. The one exception is a pause or crash point: a named step in
attn's code (`internal/pausepoint`). A test holds the process that reaches the
point, or kills the daemon there, to reach a moment between two steps of one
action that no outside event can reach. The point sets no state, and the test
still observes through promises. Stack tests and daemon wire tests arm the same
points and get the same `testworld.Pause`. [CLI stack tests](cli-stack-tests.md)
and [Daemon wire tests](daemon-wire-tests.md) say how each world arms them.

A point marks the moment it holds. `pausepoint.At(name)` marks a moment, such
as the instant before a step's first effect. `pausepoint.Done(name, err)` marks
a finished step. It holds only when the step succeeded. When the step failed, it
fails the waiting test at once with the step's error. To fault an operation,
hold the operation at its own point, arm the fault, then release the hold. A
hold on something the operation only might wait for, such as a fake agent's
boot, orders no steps unless the operation waits for it every time.
Arm only points that some daemon run or worker reaches at least once during the
test. If no run reaches an armed point, the test fails at cleanup and names the
point, because a hold that nothing reached cannot order any step.

Kernel tests build their function's input directly, since the function is
itself a promise.

## Kinds of test

A kind names where the test enters and what it may fake. Fake nothing else.

- **Kernel** enters and observes through a function with a written
  specification. Real: the function. Faked: nothing.
- **Wire** enters and observes through the protocol, from one side. Real:
  everything on the other side of the protocol. Faked: agent binaries,
  external services, clock, and network. An app wire test also runs without a
  window or GPU.
- **Stack** enters through the CLI or the protocol, across processes. Real:
  daemon, PTY workers and host, CLI, and the app without a window. Faked: agent
  binaries, external services such as GitHub, and clock.
- **Scenario** enters through native input and observes the screen, the
  protocol, and the CLI. Real: everything attn ships, packaged as shipped.
  Faked: agent binaries, external services such as GitHub, clock.

### Time

Tests control time instead of waiting for it. A test that needs time to pass
moves a clock. A test that needs something to happen waits for a signal that it
happened. No test sleeps or polls.

- Go daemon wire tests run timer behavior under `synctest` with `inBubble`. A
  world outside a bubble, which can run agents and watch folders, gives its
  daemon a stepped clock that the test moves. See
  [Daemon wire tests](daemon-wire-tests.md#timers).
- The app reads time through an injected clock, and an app wire test moves
  it. See [Native wire tests](native-wire-tests.md).
- Stack tests move one stepped clock shared by the daemon and every PTY worker
  it starts. See [CLI stack tests](cli-stack-tests.md).
- Scenarios step each daemon with the harness's `stepClock`, as the
  [harness guide](../../app/scripts/real-app-harness/AGENTS.md) describes.
- A process signals that it is ready by opening a pipe the test reads.
  `procwait.Exited` waits for any process to exit, including one the test did
  not start.

A stepped timer the test never advances fails at the hang guard instead of
passing slowly. `make lint-go` rejects these in Go test code outside a `synctest.Test` body:
`time.Sleep`, `time.Tick`, `time.NewTicker`, a bare `<-time.After`, and a
`time.After` deadline other than `fakeagent.HangGuard`. It also rejects running
`cargo` from test files and test harness packages. `scripts/testlint`
lists the few allowed exceptions with their reasons. Clippy rejects
`std::thread::sleep`, `Instant::now` and `SystemTime::now` anywhere in
`native/` (`native/clippy.toml`). In plugin and SDK tests, `make lint-js`
rejects a sleep on `setTimeout` or `Bun.sleep`; `.oxlintrc.json` lists the
files still allowed, with their reasons. Kernel socket deadlines, the
operating system's file events, syncthing's own timers, and the shared PTY
host keep real time.

### Wire

Wire tests are the default, and come in two forms:

- A daemon wire test runs a real daemon with a real store, bus, and PTY layer,
  and acts as a protocol client.
- An app wire test runs the app's real view and real daemon link on Rune's
  windowless `Runtime`, and acts as the daemon. It sends protocol messages and
  asserts on what the app shows and what it sends back.

Both use the generated protocol types, so a protocol change breaks both at
once.

A fake agent binary behaves like the real harness from the outside: same
hooks, transcript files, and terminal output. To simulate a network failure,
wrap the client's `net.Conn` inside a `synctest` bubble.

[Daemon wire tests](daemon-wire-tests.md) and
[Native wire tests](native-wire-tests.md) describe each form's harness.

### Stack

Stack tests cover what only exists between processes: restarts, reconnects,
signals, PTY ownership, CLI behavior, and Linux paths. A stack test that needs
the app starts it without a window with `Stack.StartNativeApp` and drives it
over its automation protocol, as [Native app](native-app.md#automation) says.

[CLI stack tests](cli-stack-tests.md) describes the CLI stack harness.

### Scenario

Scenarios run the packaged app in CI under Xvfb. See the
[verification requirements](instances.md#verification-requirements) and the
[harness guide](../../app/scripts/real-app-harness/AGENTS.md). Scenarios cover
rendering, focus, native keyboard and pointer input, menus, scrolling, and
whole-product behavior such as queue flow. Their verdicts come from the screen,
the protocol, or the CLI, never from the daemon's database.

### Kernel

Write a kernel test only when both hold:

- The function has a specification in domain terms, independent of how it is
  written. Examples: the state classifier, turn accounting, parsers, the
  terminal wire rewrite, migrations.
- The function's input space is too large to cover through the protocol.

Kernel tests are tables, corpora, or `rapid` properties. A single
hand-picked example does not count. A migration test's input is a database
with an older version's schema and data, built however the test needs.

## Choosing a kind

Use the first kind in this order that can observe the behavior:

1. Wire, by default.
2. Stack, when the behavior crosses a process boundary.
3. Scenario, when the behavior is pixels, focus, or native input.
4. Kernel, only under the conditions above.

A bug fix adds a regression test at the first kind that reproduces what the
user saw.

## Budgets

- A wire test finishes well under a second.
- A stack test finishes in a few seconds.
- Scenarios run in CI. Run one locally when it helps you verify a change,
  reproduce a failure or develop a scenario, not on every change.

A test over its budget belongs to a slower kind, or its harness needs work.

## When a test breaks

If a change keeps behavior and a test breaks, the test depended on
internals. Delete it, or replace it with a test of the right kind. Do not
repair it to match the new internals.

## Isolation

Every test that reaches config paths follows the
[test safety contract](../../AGENTS.md#test-safety).

## Running tests

Before landing, run targeted tests for the behavior you changed, such as
`./scripts/test-go.sh --packages -run TestName ./pkg`, or the app's wire tests
with `cargo test -p attn-app --test wire` from `native/`. The `land-on-main`
skill covers the remaining steps of landing.

`--packages` passes the remaining arguments to `go test` without running the
full suite. `scripts/test-go.sh` passes leading `--unit` and `--receipt-out` options to
`go tool test-history capture`.
A full run is eleven `go test` jobs run at once: `packages`,
`daemon-1` to `daemon-4` and `attn-1` to `attn-6`. Leading `--job <name>` options run only
the jobs they name, as CI does to spread the suite across runners:
`./scripts/test-go.sh --job daemon-3 --job attn-1`.
The runner, Makefile and CI use `-trimpath` so unchanged packages can reuse build
artifacts across worktrees. They and `scripts/land-gate.sh` also read `CGO_CFLAGS`
from `go-cgo-cflags`, which builds SQLite without memory statistics, so every daemon does less work
per allocation. attn calls nothing that needs them. A bare `go build` or
`go test` still works, but it compiles go-sqlite3 again and keeps the statistics.
The runner gives targeted and full test runs the
same isolated outer data directory, and each package's `TestMain` still creates
a fresh directory when tests run. Go can reuse successful test results when
their flags, environment, and file inputs also match.
`scripts/test-go.sh` records a receipt of each run, and so do CI's `Native`
jobs for `cargo test`. Running `go test` or `cargo test` directly records none.
See [Test history](test-history.md).

The runner and Makefile also share the pinned native VT artifact between
worktrees. Before using Go commands directly, run
`./scripts/build-libghostty-vt.sh` to prepare it for this checkout.

The runner builds the Rust binaries tests run before `go test` starts, and
tests read their paths from the environment. The CLI, the
PTY host (`ATTN_TEST_PTY_HOST`) and, when the run includes `./cmd/attnd` or
`./internal/ptybackend`, the PTY host variants listed in
`pty-host/test-variants` (`ATTN_TEST_PTY_HOST_VARIANTS`) come from the
machine-wide build store, `scripts/lib/rust-build-store.sh`, kept in
`~/.cache/attn/rust-builds` (`~/Library/Caches/attn/rust-builds` on macOS), so a
worktree whose sources match a build already made reuses that build. A test that
needs a binary the runner did not provide fails and names `scripts/test-go.sh`.
The store keys each recipe on the contents of its input roots and refuses to keep a build that read a file outside them or an
environment variable the recipe does not declare. The error names the file or
variable to add to the recipe. An entry no run has used for 7 days is removed,
and deleting the directory is always safe. A run that selects a native app test
gets `attn-app` from the store too, as [Native app](native-app.md) describes.
