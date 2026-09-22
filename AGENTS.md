# attn

attn (Attention) wraps Claude Code, Codex, Copilot, and Pi with durable sessions,
terminals, and a keyboard-driven queue for agents waiting for the user. Harnesses
keep their native experience and remain usable as bare CLIs; plugins add more.
The Garden tracks work, visible delegations let users steer agents, crew members
have permanent charters, and automations start sessions on schedules or events.
Users can annotate terminal text and rendered markdown and send comments to a session.
Queue mode separates busy agents from those waiting for the user and advances
after each prompt. State classification and turn accounting serve that flow.

Sessions and terminals must survive app, daemon, and machine restarts. The Tauri
app runs on macOS and Linux; the daemon also serves remote hosts over SSH.
`cmd/attn` and `internal/**` must build and run on Linux. The daemon owns
application state; the app owns rendering.
Linux CI exercises the packaged application under Xvfb.

attn is Victor's most loved and most used piece of software. Its small user base
is intentional. Frictionless interaction, keyboard access, and performance matter
throughout: it runs all day, so idle CPU use and creeping memory are defects.

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
  Use the [test isolation rules](docs/maintainer-contracts.md#test-safety)
  when adding or changing tests that reach config paths.

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
- `scripts/pre-commit.sh`: formats staged Go and `app/src-tauri` Rust; usable as a git pre-commit hook

`make test` skips the Go suite when only `docs/`, root Markdown and `app/src` changed since `origin/next`; `FORCE=1` runs it and `DIFF_BASE=<ref>` compares against another branch.

## Writing tests

- Only write high value unit tests, and for critical parts of the codebase. Low value unit tests are not necessary. Do not write tests for script helpers or test helpers.
- Prefer fast integration tests.
- Do not copy production code into tests or test compile-time guarantees.
- Use the [test contracts](docs/maintainer-contracts.md#test-safety)
when choosing time, property, or network-failure test helpers.

Choose checks for affected CLI, daemon, app, protocol, and Linux paths using
[verification requirements](docs/profiles.md#verification-requirements).
Rendering changes must avoid continuous repainting.

## Documentation

Docs define product vocabulary, explain intended behavior and tell people how
to use, run and test attn. Keep the glossary to short definitions. Put product
rules in the relevant feature docs.

Do not write implementation notes anywhere. The code must explain how it works.
If it needs a prose explanation of its wiring or control flow, make the code
clearer.

## Task-specific guidance

Read the relevant entry when the task touches its subject. When changing or working on:

- Command, event, or message shapes => docs/maintainer-contracts.md#protocol
- `sdk/attn-app/src` or SDK consumers => docs/maintainer-contracts.md#the-app-sdk
- Event publishing, projections, consumers, or retention => docs/maintainer-contracts.md#event-bus
- Native VT builds, ABI, or pin updates => docs/maintainer-contracts.md#native-vt-library
- Agent-facing content in `internal/prompts/content/**`, its Go definitions, or CLI help => docs/prompt-authoring.md
- Product vocabulary => [Glossary](docs/glossary.md)
- Branches, PRs, merges, or waiting on reviews => docs/working-with-next.md
- Changelog fragments, releases, hotfixes, or syncing `main` into `next` => docs/making-a-release.md
- Installing, launching, or verifying profiles => docs/profiles.md
- Frontend code or shortcuts => app/AGENTS.md
- Packaged-app scenarios or recording/publishing evidence => app/scripts/real-app-harness/AGENTS.md
- Pi driver or auto-mode permissions  => plugins/attn-pi/AGENTS.md
- CPU, memory, or benchmarks => docs/perf-testing.md
- Terminal input that stops working => docs/diagnosing-terminal-input.md
- Shared Rust PTY host => docs/pty-host-verification.md

## Diagnostics

- Daemon: `<data-dir>/daemon.log`, or `attn debug daemon-log --since 10m --grep PATTERN`.
- Dedicated PTY worker: `<data-dir>/workers/<daemon-instance>/log/<session>.log`.
- Shared PTY host: `<data-dir>/pty-hosts/<daemon-instance>/log/host.log`.
- `attn debug incidents|diagnostics|input`: frontend terminal logs; `attn debug ls` lists them.
- `attn state explain <session>`: why a session has its state, claim by claim.
- `attn bus status`: event-log consumers, lag, and retention.
- `go run ./scripts/wsctl`: drives a non-production daemon over WebSocket (sessions, input, screen).
- `attn db restore`: restores the database from a rotating backup while the daemon is stopped.
- Daemon code uses `d.logf(...)` or injected `LogFunc`; background stderr is lost.
- To debug an isolated daemon, quit its app, then `DEBUG=debug attn daemon ensure`.
- Restarting the app or daemon has no impact on the underlying agents.
