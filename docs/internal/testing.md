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
- **Performance**: idle attn stays quiet, and memory does not creep.
  Benchmarks and memory scenarios track it; see
  [Performance testing](perf-testing.md).
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
attn's own state.

Kernel tests build their function's input directly, since the function is
itself a promise.

## Kinds of test

A kind names where the test enters and what it may fake. Fake nothing else.

- **Kernel** enters and observes through a function with a written
  specification. Real: the function. Faked: nothing.
- **Wire** enters and observes through the protocol, from one side. Real:
  everything on the other side of the protocol. Faked: agent binaries,
  external services, clock, network; the browser and Tauri host for the app.
- **Stack** enters through the CLI, the protocol, or a browser page, across
  processes. Real: daemon, PTY workers and host, CLI, frontend. Faked: agent
  binaries, external services such as GitHub, clock; the PTY in the browser suite.
- **Scenario** enters through native input and observes the screen, the
  protocol, and the CLI. Real: everything attn ships, packaged as shipped.
  Faked: agent binaries, external services such as GitHub, clock.

### Time

Tests control time instead of waiting for it. A test that needs time to pass
moves a clock; a test that needs something to happen waits for a signal that
it did. No test sleeps or polls.

- Go daemon wire tests run timer behavior under `synctest` with `inBubble`. A
  world outside a bubble, which can run agents and watch folders, gives its
  daemon a stepped clock the test moves; see
  [Daemon wire tests](daemon-wire-tests.md#timers).
- App wire tests use Vitest fake timers; see
  [App wire tests](app-wire-tests.md#waiting-and-time).
- Stack tests move one stepped clock shared by the daemon and every PTY worker
  it starts; see [CLI stack tests](cli-stack-tests.md).
- Browser end-to-end specs wait for what the page shows (`expect`,
  `expect.poll`, `page.waitForFunction`), or install `page.clock` and run
  forward the timers the behavior depends on.
- Scenarios step each daemon with the harness's `stepClock`, as the
  [harness guide](../../app/scripts/real-app-harness/AGENTS.md) describes.
- A process signals that it is ready by opening a pipe the test reads.
  `procwait.Exited` waits for any process to exit, including one the test did
  not start.

A stepped timer the test never advances fails at the hang guard instead of
passing slowly. `make lint-go` fails a `time.Sleep`, `time.Tick`,
`time.NewTicker` or bare `<-time.After` in Go test code outside a
`synctest.Test` body, and `scripts/testtimelint` lists the few allowed ones
with their reasons. `pnpm --dir app run lint` fails a `waitForTimeout` in
`app/e2e`. Heartbeats, kernel socket deadlines, the operating system's file
events, syncthing's own timers and the shared PTY host keep real time.

### Wire

Wire tests are the default, and come in two forms:

- A daemon wire test runs a real daemon with a real store, bus, and PTY layer,
  and acts as a protocol client.
- An app wire test renders the real app with its real socket client and acts
  as the daemon. It sends protocol messages and asserts on what renders and
  what the app sends back. It runs in a simulated browser with the Tauri host
  stubbed; everything else attn ships in the frontend is real.

Both use the generated protocol types, so a protocol change breaks both at
once.

A fake agent binary behaves like the real harness from the outside: same
hooks, transcript files, and terminal output. To simulate a network failure,
wrap the client's `net.Conn` inside a `synctest` bubble.

[Daemon wire tests](daemon-wire-tests.md) and [App wire tests](app-wire-tests.md)
describe each form's harness.

### Stack

Stack tests cover what only exists between processes: restarts, reconnects,
signals, PTY ownership, CLI behavior, and Linux paths. The browser end-to-end
suite is also a stack test: a real daemon serves the frontend to a browser.
It mocks the PTY unless a test needs real terminals.

[CLI stack tests](cli-stack-tests.md) describes the CLI stack harness.

### Scenario

Scenarios run the packaged app in CI under Xvfb; see the
[verification requirements](instances.md#verification-requirements) and the
[harness guide](../../app/scripts/real-app-harness/AGENTS.md). They cover
rendering, focus, native keyboard and pointer input, menus, scrolling, and
whole-product behavior such as queue mode. Their verdicts come from the screen,
the protocol, or the CLI, never from the daemon's database.

### Kernel

Write a kernel test only when both hold:

- The function has a specification in domain terms, independent of how it is
  written. Examples: the state classifier, turn accounting, parsers, the
  terminal wire rewrite, migrations.
- Its input space is too large to cover through the protocol.

Kernel tests are tables, corpora, or `rapid` properties; a single
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
- Scenarios run in CI; run them locally only to reproduce a failure or
  develop a scenario.

A test over its budget belongs to a slower kind, or its harness needs work.

## When a test breaks

If a change keeps behavior and a test breaks, the test depended on
internals. Delete it, or replace it with a test of the right kind. Do not
repair it to match the new internals.

## Isolation

Every test that reaches config paths follows the
[test safety contract](../../AGENTS.md#test-safety).

Wire and CLI stack worlds see an unavailable Tailscale backend by default.
Tests of Tailscale provide their own CLI fake; the host's installed CLI is never
used.

## Running tests

Before landing, run targeted tests for the behavior you changed, such as
`./scripts/test-go.sh --packages -run TestName ./pkg` or one file with
`pnpm --dir app exec vitest run`. The `land-on-main` skill is the rest of
landing.

`--packages` passes the remaining arguments to `go test` without running the
full suite; leading `--unit` and `--receipt-out` options go to test history.
The runner and Makefile use `-trimpath` so unchanged packages can reuse build
artifacts across worktrees. The runner gives targeted and full test runs the
same isolated outer data directory; each package's `TestMain` still creates a
fresh directory when tests run. Go can reuse successful test results when
their flags, environment, and file inputs also match.
`scripts/test-go.sh`, `pnpm --dir app test` and `pnpm --dir app run e2e`
record a receipt of each run; `vitest` and `playwright` run directly do not.
See [Test history](test-history.md).

The runner and Makefile also share the pinned native VT artifact between
worktrees. Before using Go commands directly, run
`./scripts/build-libghostty-vt.sh` to prepare it for this checkout.
