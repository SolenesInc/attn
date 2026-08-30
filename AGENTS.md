# attn

attn stands for Attention. It helps people work with several agents while
keeping each agent's native CLI available.

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
- The Garden tracks work in seeds that agents plant, tend, and hand off.
- Visible orchestration. Delegations are full sessions the user can open and
  steer, from any harness to any harness, and agents can message each other.
- Crew members are permanent agents with charters; the Chief is one.
- Automations start a steerable agent on a schedule or an event.
- Annotations. The user selects text in a live terminal or in a natively
  rendered markdown file, comments on it, and sends the batch to a session as
  one message.

The Tauri app currently runs on macOS; Linux app support is in progress.

## What makes attn special?

attn is Victor's most loved and most used piece of software. It is not widely
used, by design (Victor doesn't want to carry a large user base) but the few
people who run it matter to him. Maintain and iterate on it like something loved.

Keep the experience frictionless, keyboard friendly, and fast. attn runs all
day, every day. Growing memory use and CPU burned while idle are defects.

## Note from Victor

I love ambitious ideas and strive for simple, elegant systems. Refactoring
first so a new feature weaves in gracefully is the norm here, not an exception
to argue for. I work iteratively, crafting boutique software, not IKEA
software. Nothing wrong with IKEA; it just doesn't spark passion in me.

## You are probably running inside attn

Most attn work is done from attn itself, often driven remotely. The session you
are working in is an attn session: its PTY lives in `internal/ptyworker`, the
daemon that owns it listens on `~/.attn/attn.sock`, and its state is in the
production `~/.attn` database. A careless command ends your own session, or one of
Victor's others.

- Never kill by pattern. No `pkill -f attn`, no `pgrep | kill`, no killing a PID
  you matched on a name, path, or worktree string: your own process and every
  other live session carry those strings in their argv. Kill only a PID you
  captured at spawn, or a port/socket owner you confirmed by its working
  directory.
- Production `~/.attn` is read-only to you. Copy out of it for realistic data,
  but never point a daemon at it, never open it read-write, never clean it up.
  See "Test safety".
- Non-production builds, installs, launches, and restarts are pre-authorized.
  Production lifecycle commands (`make`, `make install`, `make install-daemon`)
  require Victor's explicit approval. Confirm the `[attn profile=…]` banner
  before acting; never restart the daemon hosting your session.

## Language

Read the relevant definitions in [docs/glossary.md](docs/glossary.md) before
naming a domain concept.
Keep its definitions and the implementation consistent in the same change.

Several terms collide in this repo. Keep them apart:

- **you**: the agent reading this file and changing attn.
- **we**, **Victor**: who you are talking to; attn's maintainer.
- **user**: the person using attn to direct agents. Usually Victor too, but the
  distinction decides product questions. Don't write "Victor" in prompts within
  the codebase.
- **agent**: depending on context: you, the agent attn launches into a session
  (Claude Code, Codex), or a delegated agent on the board. Say which whenever the
  sentence does not make it obvious.
- **session**: one attn-managed agent process, using a PTY or a conversation host.

## Profiles and live verification

Every PR must meet the [verification requirements](docs/profiles.md#verification-requirements).
Read [docs/profiles.md](docs/profiles.md) before installing or running a
verification profile.

### Packaged-app harness

Before running packaged-app scenarios, read the
[harness guide](app/scripts/real-app-harness/AGENTS.md).

### Evidence recordings

A PR with a visible change carries a recording of its live verification.
Follow the [recording recipe](app/scripts/real-app-harness/AGENTS.md#recordings)
before recording or publishing.

## Before finishing

Before calling work done, check each applicable entry:

- **CLI.** `cmd/attn`. Behavior reachable from the app is usually also expected
  from the command line.
- **Daemon and app.** Check the app's response to daemon changes.
- **Protocol.** Follow the [generation and version rules](docs/maintaining.md#protocol).
- **Linux.** `cmd/attn` and `internal/**` cross-compile and run on Linux
  remotes.
- **Docs.** Update domain definitions and follow the
  [changelog fragment rules](docs/making-a-release.md#changelog-fragments).

If you added a way in, add the way out and the way to see it. Snooze needs
unsnooze, an opened turn needs a way to settle, `bus disable` needs `bus enable`,
a created profile needs `profile clean`. A one-way door is a bug.

## Test safety

Tests must never resolve config paths to production `~/.attn`. Scope them with
`ATTN_DATA_DIR`; never redirect `HOME`. Read the
[test setup](docs/maintaining.md#test-safety) before adding or running backend tests.

## How changes ship

Let the size and risk of the change decide how much process it needs. Ask the
maintainer when the answer is unclear.

- Plant a plot in the Garden for non-trivial work. Put the plan in its body,
  add one child for each piece, and use `blocks` edges where order matters.
  When parts can run independently, offer to dispatch parallel attn
  delegations. A small change can go straight to a PR.
- Before branching or opening a PR, read
  [docs/working-with-next.md](docs/working-with-next.md).
  It covers epic branches, review, merge readiness, and waiting on a PR.
- For release candidates, urgent hotfixes, and syncing `main` back to `next`,
  follow [docs/making-a-release.md](docs/making-a-release.md).
- A spike answers a question. The maintainer decides whether to merge it,
  discard it, or build on what it showed. Do not commit spikes.

### Experience testing

The maintainer tests how attn feels to use; the harness and figgyster cover
correctness. Experience testing happens early in a spike, when the idea is
still cheap to change, and at the end of a substantial arc of PRs.

At the end of an arc, install a running profile from the branch and give it
realistic data. Give the maintainer a short list of things to try. Focus on what
changed and anything you could not judge yourself, such as feel, latency, or
keyboard flow.

## Misc expectations

- Diagnose a bug before fixing it. If the cause remains unknown, propose
  instrumentation to capture it next time.
- Prefer fast integration tests. Do not copy production code into tests or
  test compile-time guarantees.
- The daemon owns application state; the app owns rendering.
- Avoid continuously repainting animations. They tend to have high CPU usage and
  drain battery.
- Protocol version bumps and DB migrations are normal work. Make the change,
  verify it, and keep going.

## Comments

The code says what it does; a comment earns its place only by saying what
the code cannot: a tool directive, a measured receipt behind a number, or a
trap that is not visible from the file. No block is longer than two lines;
`make lint` enforces that in Go (`internal/lint/commentblock`) and in
TypeScript (`app/lint/attn`), and CI runs it. If a compressed comment does
not make sense on its own, delete it.

## Read when relevant

| Work | Guidance |
| --- | --- |
| CLI, daemon, storage, or backend tests | [Code map and test setup](docs/maintaining.md) |
| Session lifecycle, input, turns, queue, or presence | [Sessions](docs/sessions.md) |
| Seeds, crew, or legacy ticket recovery | [Garden and crew](docs/garden-and-crew.md) |
| Notebook, journal, or workspace context | [Notebook and workspace context](docs/notebook.md) |
| App lifecycle, views, documents, or event retention | [Apps and events](docs/apps-and-events.md) |
| Home/outpost ownership or client authentication | [Daemon ownership](docs/daemon-ownership.md) |
| Auto-mode permissions or configuration | [Auto mode](docs/maintaining.md#auto-mode) |
| Protocol messages or generated types | [Protocol](docs/maintaining.md#protocol) |
| App SDK declarations | [The app SDK](docs/maintaining.md#the-app-sdk) |
| Event publishers, consumers, or projections | [Event bus](docs/maintaining.md#event-bus) |
| Frontend components, rendering, or shortcuts | [app/AGENTS.md](app/AGENTS.md) |
| Daemon or PTY diagnosis | [Diagnostics](docs/maintaining.md#diagnostics) |
| Native VT library or pin updates | [Native VT library](docs/maintaining.md#native-vt-library) |
