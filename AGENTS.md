# attn

Always read [docs/glossary.md](docs/glossary.md) to understand and change attn
internals. It is attn's domain model.

attn stands for Attention: an interface friendly to both human and agent
brains, built as a harness augmenter. What it does today, and what each part
asks of you as a maintainer:

- Durability. App, daemon, and machine restarts bring every session and
  terminal back.
- Bring your own harness. attn wraps Claude Code, Codex, Copilot, and Pi as
  they are; the user gets each harness's native experience and can use the
  bare CLI at any time. Users can add new harnesses with plugins.
- The queue mode sorts agents into "waiting for you" or "busy" and moves the
  user to the next one after every prompt. State classification and turn
  accounting exist to serve this.
- Remote hosts. Everything the daemon does also works over SSH on a Linux
  box, which is why `cmd/attn` and `internal/**` build on Linux.
- The Garden is the work tracker: seeds planted, tended, harvested. It is
  the unit agents hand off, work upon, and help them stay organized.
- Visible orchestration. Delegations are full sessions the user can open and
  steer, from any harness to any harness, and agents can message each other.
- Crew members are permanent agents with charters; the Chief is one.
- Automations start a steerable agent on a schedule or an event.
- Annotations. The user selects text in a live terminal or in a natively
  rendered markdown file, comments on it, and sends the batch to a session as
  one message.

The app (the Tauri UI) is mac only for now; Linux support is being worked upon.
The daemon runs on Linux, so daemon code stays portable.

## What makes attn special?

attn is Victor's most loved and most used piece of software. It is not widely
used, by design (Victor doesn't want to carry a large user base) but the few
people who run it matter to him. Maintain and iterate on it like something loved.

The things we can never compromise on: frictionless experience, keyboard
friendliness, and performance. They go hand in hand. And attn runs all day,
every day: memory that creeps or CPU burned while idle is a defect.

## Note from Victor

I love ambitious ideas and strive for simple, elegant systems. Refactoring
first so a new feature weaves in gracefully is the norm here, not an exception
to argue for. I work iteratively, crafting boutique software, not IKEA
software. Nothing wrong with IKEA; it just doesn't spark passion in me.

## You are probably running inside attn

- Never kill by name, pattern, or worktree path. Kill only a PID captured at
  spawn, or a port/socket owner confirmed by working directory.
- Production ~/.attn is read-only. Copy data out; never run a test daemon
  against it, open it read-write, or clean it.
- Using attn CLI to interact with ~/.attn is allowed.
- Non-production builds, installs, launches, and restarts are pre-authorized.
  Production `make`, `make install`, and `make install-daemon` need Victor's
  explicit approval.
- Never redirect `HOME` or resolve test config paths to production `~/.attn`.
  Use the [test isolation rules](#test-safety) when adding or changing tests
  that reach config paths.

## Working rules

Before introducing a wrapper, interface, configuration option, fallback, cache,
or coordination layer, name the current requirement it serves. Search existing
code and platform capabilities first. If the justification is hypothetical future
flexibility, leave it out. An abstraction earns its place by removing complexity
from today's code; moving complexity behind a new name does not count. Preserve
the full requested behavior.

During design and review, ask: what could we remove from this design and still
satisfy the full requirement?

- Make protocol bumps and DB migrations as needed by the changes.
- Diagnose before fixing. If the cause is unknown, propose instrumentation.
- Do not commit spikes.
- Riskier changes, such as a new harness integration or PTY runtime, land first
  behind an experimental setting, off by default, like the shared PTY host.
- Do not add explanatory code comments. Express intent through function and
  variable names and code structure. Rewrite code that needs a comment to be understood.
- Align with the user before adding a bus event or changing an existing event's name, subject, payload, semantics, or compatibility behavior.
- Remote outposts are temporarily incomplete: Garden and crew remain home-only
  until the generic uplink exists, and other cross-daemon flows may be
  unsupported. Do not absorb that remote debt into unrelated work; preserve
  existing fences and fail loudly when an operation depends on an unsupported
  remote path.
- Product prompts address the user, never "Victor". Distinguish the agent
  changing attn from the agents it runs.

## PR posture

### Reviewer

- Review for unnecessary machinery as well as correctness. For each new layer,
  option, fallback, or duplicated state, check what current requirement it serves.
  When proposing simplification, name what can be removed, what replaces it, and
  why the full behavior is preserved. Fewer lines alone are not evidence of a
  better design.
- Ask for rigor only where a current requirement or a promise in
  [Testing](docs/testing.md) needs it, and name which. Do not request tests
  that guard no promise, unit tests for behavior a wire test covers, or
  validation, fallbacks, and edge-case handling for hypothetical inputs.
- Apply a rule for its purpose. If a change does not touch what a rule
  protects, skip the rule's steps, such as a protocol bump for a schema edit
  that leaves the wire unchanged.

### Author

- Codex reviews every PR as `chatgpt-codex-connector[bot]`. It reviews each push on
  its own. A 👍 reaction on the PR means it was approved with no feedback.
  👀 reaction means Codex is reviewing.
- Read reviews, inline comments, review threads with `isResolved`, and PR reactions
  through the API (GraphQL for threads). `gh pr view` misses reactions and thread state.
- Reply on the thread with what changed, and resolve it. When no change is needed,
  reply with the reason.
- Use `attn pr watch <url> --mode codex` after pushing a PR to wait for CI/review. Do not loop for updates.
- When addressing comments, avoid patchwork fixes. Understand the root cause of the issue captured
  by the reviewer, and address it holistically, if necessary by refactoring the system.

## Commands and verification

- `make test`: Go tests (`make test-v` verbose, `make test-watch` on file changes)
- `make test-frontend` (`pnpm --dir app test:ui` for the Vitest UI)
- `make test-e2e`: Browser tests (`pnpm --dir app e2e:headed` to watch them)
- `make test-scripts`: Shell script tests
- `pnpm --dir app run dev`: Runs the frontend/app dev server
- `make lint`: Overall linter

`make test` skips the Go suite when only `docs/`, root Markdown and `app/src` changed since `origin/next`; `FORCE=1` runs it and `DIFF_BASE=<ref>` compares against another branch.

Choose checks for affected CLI, daemon, app, protocol, and Linux paths using
[verification requirements](docs/instances.md#verification-requirements).
Rendering changes must avoid continuous repainting.

## Writing tests

Follow [Testing](docs/testing.md). In short:

- Commit tests that guard a promise attn makes: behavior that users, the
  agents attn runs, clients, or later versions rely on. Check
  your own work by running it; keep scratch tests out of commits.
- A committed test depends only on promises, never on internals. When a
  promise cannot be reached that way, extend the harness.
- Kinds of test, by where they enter:
  - Wire: one side of the protocol. A real daemon driven as a protocol
    client, or the real app driven as the daemon. The default.
  - Stack: real processes together, entered through the CLI, the protocol,
    or a browser page. For restarts, reconnects, signals, and Linux paths.
  - Scenario: the packaged app driven by native input, judged by the screen.
    For pixels, focus, and keyboard or pointer behavior.
  - Kernel: one function with a written specification. Only for specified
    logic with large input spaces, as tables, corpora, or properties.
- When a behavior-preserving change breaks a test, delete or replace the test;
  do not repair it.
- Do not test script helpers or test helpers, copy production code into tests,
  or test compile-time guarantees.

## Test safety

- Never resolve test config paths to production `~/.attn`; never redirect `HOME`.
- Packages reaching config paths need `TestMain`: create a temp dir and call
  `config.ScopeTestEnvironment(dir)` before `m.Run()`. It sets `ATTN_DATA_DIR`
  and clears inherited DB/socket/config/plugin overrides. Raw `os.Setenv`
  is insufficient. Missing `ATTN_DATA_DIR` intentionally panics under `go test`.
  `testworld.Main(m)` does this for packages that run wire or stack worlds.
- Per-test isolation may use `t.Setenv("ATTN_DATA_DIR", t.TempDir())`.
- Use `synctest.Test` for elapsed-time or never-happens assertions; no sleeps/polls.
- Use `pgregory.net/rapid` for invariants over large inputs; commit failure seeds.
- Simulate network failures by wrapping the client's `net.Conn` inside a `synctest` bubble.

## Ownership

- Production PTYs run in `internal/ptyworker`, using `internal/pty`.
- `internal/store` owns SQLite/cache; `internal/attention` owns turn predicates.
  Derive `turn_owed` from persisted opened/settled timestamps.
- `internal/jobs` owns background duties and periodic ticks;
  `internal/supervise` owns long-lived daemon children.
- Goroutines and timers in `internal/daemon` start through `d.life` (`lifetime.go`);
  `Daemon.stop` waits for them without cancelling. `goTransport` is only for peer I/O.
- Stop drains running jobs and lifetime work before closing Git or shutting down PTYs.
  Handler and Git deadlines still apply; shutdown itself does not fence or cancel their results.
- Garden/crew handlers call `Daemon.requireHome` (`internal/enrollment`).
  Outposts own sessions; Garden/crew belong to their home.
- Everything belongs to one profile for life, and anything in another profile is treated as
  if it doesn't exist (e.g. /resume of its conversation opens a new session here).
- Crew files are authoritative; the registry records paths. One active session
  binding per member (`internal/daemon/crew.go`).
- `internal/docstore` compiles SQL; `internal/store/documents.go` executes it.
  SQL identifiers come from integers or validated field names, never caller text.
- Auto-mode pattern/model writes go only through `PromoteAutoModeProposal`
  in `internal/store/automode.go`. Agents propose; only the user promotes.

## Protocol

For command/event/message-shape changes:

1. Edit `internal/protocol/schema/main.tsp`; run `make generate-types`.
2. Increment `ProtocolVersion` in `internal/protocol/constants.go` and
   `PROTOCOL_VERSION` in `app/src/hooks/useDaemonSocket.ts`.

Never hand-edit `internal/protocol/generated.go` or `app/src/types/generated.ts`.

## Event bus

- Publish entity ids as fact subjects; omit byte streams.
- Only projections send wire traffic. The exceptions are the remote relay
  (already published on the remote bus), per-watcher filesystem change bursts,
  and tile content sent to its subscribers.
- Projections only write to the wire. A state change or nested publish inside
  one can deadlock.
- Bulk changes publish one fact per entity inside `coalesceSnapshots`.
- Durable handlers must be idempotent; unregister consumers when their owner is removed.
- Enabled durable consumers pin retention. Disabled consumers release it.
  Pin alarms never discard unread facts.
- Inspect with `attn bus status`; control delivery with `attn bus disable|enable`.
  Tests can shorten `ATTN_BUS_RETENTION` and `ATTN_BUS_PIN_ALARM_AGE`.

## Native VT library

- Keep `internal/ghosttyvt` build tags, cgo tuples, `scripts/lib/libghostty-vt.sh`,
  and Makefile platforms aligned: darwin/arm64, linux/amd64+arm64.
- `ghostty-vt.pin` must match upstream's rolling `tip` build (the wasm source).
  Run `make publish-ghostty-vt-wasm`, then `make publish-native-vt`; commit both locks.
- On a pin bump, verify `abi.layout.test.ts`, rerun
  `go test ./internal/pty -run TestKittyWireRewriteCorpus -update`, and check
  measured limits in `internal/pty/wirefeed.go`.

## Documentation

Docs define product vocabulary, explain intended behavior and tell people how
to use, run and test attn. Keep the glossary to short definitions.

Do not write implementation notes anywhere. The code must explain how it works.
If it needs a prose explanation of its wiring or control flow, make the code
clearer.

## Task-specific guidance

Read the relevant entry when the task touches its subject. When changing or working on:

- Tests, before writing, changing, or deleting them => docs/testing.md
- How a harness behaves, before relying on it => docs/harnesses.md; check new
  claims in its source or with a probe and record them there
- Agent-facing content in `internal/prompts/content/**`, its Go definitions, or CLI help => docs/prompt-authoring.md
- Branches, PRs, merges, or waiting on reviews => docs/working-with-next.md
- Changelog fragments, releases, hotfixes, or syncing `main` into `next` => docs/making-a-release.md
- Installing, launching, or verifying instances => docs/instances.md
- Frontend code or shortcuts => app/AGENTS.md
- Packaged-app scenarios or recording/publishing evidence => app/scripts/real-app-harness/AGENTS.md
- Pi driver or auto-mode permissions  => plugins/attn-pi/AGENTS.md
- CPU, memory, or benchmarks => docs/perf-testing.md
- Terminal input that stops working => docs/diagnosing-terminal-input.md
- Shared Rust PTY host => docs/pty-host-verification.md

## Diagnostics

- Daemon: `<data-dir>/daemon.log`, or `attn debug daemon-log --since 10m --grep PATTERN`.
- Dedicated PTY worker: `<data-dir>/workers/<daemon-instance>/log/<terminal>.log`, the pane's `runtime_id`.
- Shared PTY host: `<data-dir>/pty-hosts/<daemon-instance>/log/host.log`.
- `attn debug incidents|diagnostics|input`: frontend terminal logs; `attn debug ls` lists them.
- `attn state explain <session>`: why a session has its state, claim by claim.
- `attn bus status`: event-log consumers, lag, and retention.
- `go run ./scripts/wsctl`: drives a non-production daemon over WebSocket (sessions, input, screen).
- `attn db restore`: restores the database from a rotating backup while the daemon is stopped.
- Daemon code uses `d.logf(...)` or injected `LogFunc`; background stderr is lost.
- To debug an isolated daemon, quit its app, then `DEBUG=debug attn daemon ensure`.
- Restarting the app or daemon has no impact on the underlying agents, unless
  Settings → Terminal reports the embedded backend: then a daemon restart stops them.
