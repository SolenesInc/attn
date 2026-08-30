# attn

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
- Restart the dev daemon, never the one hosting you. Non-production builds,
  installs, and restarts are pre-authorized precisely so you never need to touch
  production; confirm the `[attn profile=…]` banner before any lifecycle command.

## Language

`docs/glossary.md` is the source of truth for attn's domain vocabulary:
workspace context, the Notebook, tickets, delegations, turns, and friends. Read it
before naming anything new, and when an implementation has drifted from a
definition, fix one or the other in the same change.

Several terms collide in this repo. Keep them apart:

- **you**: the agent reading this file and changing attn.
- **we**, **Victor**: who you are talking to; attn's maintainer.
- **user**: the person using attn to direct agents. Usually Victor too, but the
  distinction decides product questions. Don't write "Victor" in prompts within
  the codebase.
- **agent**: depending on context: you, the agent attn launches into a session
  (Claude Code, Codex), or a delegated agent on the board. Say which whenever the
  sentence does not make it obvious.
- **session**: one attn-managed agent process with a PTY. Not a delegation, not
  a workspace.

## Commands

```bash
# isolated non-production install (pre-authorized)
make dev                    # build/install/open attn-dev.app; ensure dev daemon
make install-daemon-dev     # replace/re-sign daemon sidecar only
make install PROFILE=<name> # build/install named isolated profile

# production (Victor's explicit approval required)
make                        # build/install/open ~/Applications/attn.app
make install
make install-daemon

# build and test
make build-app
make test
make test-frontend
make test-e2e
make test-harness           # Go + frontend + e2e
make test-all               # Go + frontend
go test ./internal/store -run TestList

# frontend-only loop
pnpm --dir app run dev
pnpm --dir app test
pnpm --dir app run e2e
```

Run full app builds/installs outside the sandbox: code signing needs the macOS
keychain; sandboxed identity lookup can cause ad-hoc signing and lose persistent
permissions.

## Profiles and live verification

- Non-production builds, installs, launches, and restarts are pre-authorized.
- Production `make`, `make install`, and `make install-daemon` require Victor's
  explicit approval.
- Install the cheapest tier that covers the change:
  - Go-only change (`cmd/attn`, `internal/**`) → `make install-daemon-dev`, or
    `make install-daemon PROFILE=<name>`. Replaces and re-signs the sidecar and
    restarts the daemon; no Tauri/Rust/frontend build.
  - Anything under `app/` (frontend, `src-tauri`, plugins), a protocol change
    (`generated.ts` moves with `generated.go`), or bundle metadata → `make dev`,
    or `make install PROFILE=<name>`.
    Escalate to the full build when unsure, or when a daemon-only install does not
    show the change.
- Named profile: select it with `eval "$(./attn profile-env <name>)"`, then run
  `make install PROFILE=<name>`. The shell's `ATTN_PROFILE` must match.
- `profile-env` clears inherited routing overrides (`ATTN_DATA_DIR` included).
  Verify the emitted `[attn profile=…]` banner before acting.
- `ATTN_PROFILE` set beside a data dir, socket, DB, config, plugin dir, or WS
  port belonging to another profile is refused outright. The error names both
  sides and the `env -u …` that fixes it. Your session inherits production
  routing, so scrub it (or `profile-env`) before any profile command.
- Inspect with `attn profile`, `attn profile list`, or
  `attn profile resolve --json`; remove with `attn profile clean <name>`.
- **Clean up the profile you created.** Nothing reaps it for you: its daemon
  (~40MB) and every pty-worker (~15MB) keep running until someone notices them
  days later. `attn profile clean <name>` reaps the workers, conversation
  hosts, and plugin runtime processes, stops the daemon, quits the app, and
  removes the bundle and data dir. `make install
PROFILE=<name>` records the worktree it ran from, so `attn profile list` tells
  you which profiles are yours, and a PostToolUse hook reminds you after you
  create or merge a PR.
- Full model and per-agent recipe: [docs/profiles.md](docs/profiles.md).

Every non-trivial PR needs live verification from the branch in a running
non-production app/daemon. Exempt only:

- trivial docs/comments/renames/log strings; or
- a pure isolated change fully covered by unit tests, with no daemon lifecycle,
  protocol, PTY, background-runner, timing, or UI surface. State the reason.

Match the verification tier to the behavior the change exposes. The cheapest
tier applies only when the change has no app-observable behavior at all: a
self-contained CLI command that never talks to the app or daemon can be
verified by running the built binary directly (plus its unit tests). Example:
`attn pr wait-ready` shells out to `gh` and touches no daemon or app surface,
so exercising the binary against a real PR is sufficient.

A daemon change is not automatically daemon-tier. Most daemon work reaches the
app via protocol, persisted state and its broadcasts, WebSocket events, PTY,
PR/git flows, and the app's reaction is part of the behavior, so it needs
integrated verification in the running app even though the code lives under
`internal/`. Reserve daemon-only verification for daemon internals with no path
to the app.

Daemon lifecycle, protocol, PTY, background-runner, and UI changes always need
live verification. If the environment cannot run the tier the change requires,
stop and ask; do not merge on automated tests alone.

Before live verification, run the selected profile's bundled preflight:

```bash
profile_app="$(./attn profile resolve --field appPath)"
"$profile_app/Contents/MacOS/attn" preflight
# mirror pinned launch settings when applicable:
"$profile_app/Contents/MacOS/attn" preflight \
  --agent codex --model <model> --effort high --json
```

Use the bundled CLI, not an unrelated `attn` on `PATH`. `attn preflight` is
diagnostic; fix reported tool/path/routing/daemon/protocol failures before
treating scenario output as product evidence.

### Packaged-app harness

- Single-tenant: never run packaged-app scenarios in parallel.
- Crew fixtures in harness and verification profiles use obviously synthetic
  names. Pin synthetic members explicitly to `claude-haiku-4-5`; use a stronger
  model only when the scenario is testing work that needs its intelligence.
- Multiple scenarios: `pnpm --dir app run real-app:serial-matrix`.
- The harness refuses a build whose source fingerprint differs from the
  checkout, so a stale install fails by name.
- Harness uses active `ATTN_PROFILE`, otherwise `dev`; `ATTN_HARNESS_PROFILE`
  overrides it.
- Production requires both `ATTN_HARNESS_PROFILE=` and `--run-against-prod`.
- On failure, inspect captured pane text and native screenshots before
  diagnosis.
- Remote scenarios target the local OrbStack VM (`attn-remote@orb`); provision
  with `pnpm --dir app run real-app:provision-remote`.

### Evidence recordings

A PR with a visible change carries a recording of the live verification in its
description. Record the run, publish, paste the emitted markdown:

```bash
./scripts/pr-evidence.sh record --profile <name> --seconds 20 --out clip.mp4
./scripts/pr-evidence.sh publish clip.mp4   # pushes mp4+gif to victorarias/attn-pr-evidence, prints the markdown
```

`record` captures the window of the named profile's app (`attn-<name>.app`).
Pass the profile you installed for this verification, the same name you will
`profile clean` later. `--app <owner>` overrides for a non-attn window.

Harness runs record themselves: `ATTN_HARNESS_RECORD=1` makes every
`createScenarioRunner` scenario (and each serial-matrix or soak leg) write
`recording-NN.mp4` segments into its artifacts dir, publishable with the same
`publish` command. Details in `app/scripts/real-app-harness/CLAUDE.md`.

The evidence repo is public and the clip shows whatever the window shows:
session names, transcripts, tickets. Watch the recording before publishing;
re-record if it shows something that should stay private.

The GIF renders inline in the PR; the mp4 beside it is the full-quality
master. GitHub never inline-plays a repo-hosted mp4, only the GIF embeds,
and images render only under 10MB, so keep clips around 20s (the script warns
past the limit). Rendering receipts, one section per embed form:
[attn-pr-evidence#1](https://github.com/victorarias/attn-pr-evidence/issues/1).

## Hit every surface

The most common defect is a change that works on the path you tested and is
missing everywhere else. The verification tier above decides how hard to check;
this decides what you forgot. Before calling work done, walk the list and say
which entries applied:

- **CLI.** `cmd/attn`. Behavior reachable from the app is usually also expected
  from the command line.
- **Daemon and app.** Most `internal/**` work reaches the app: protocol,
  persisted state and its broadcasts, WebSocket events, PTY, PR/git flows. The
  app's reaction is part of the behavior.
- **Protocol.** `generated.ts` moves with `generated.go` and
  `ProtocolVersion`/`PROTOCOL_VERSION` increments.
- **Linux.** `cmd/attn` and `internal/**` cross-compile and run on Linux
  remotes.
- **Docs.** New vocabulary in `docs/glossary.md`, changelog in a `changelog.d/`
  fragment.

If you added a way in, add the way out and the way to see it. Snooze needs
unsnooze, an opened turn needs a way to settle, `bus disable` needs `bus enable`,
a created profile needs `profile clean`. A one-way door is a bug.

## Test safety

Tests must never resolve `config.DataDir()` or derived paths to production
`~/.attn`.

- Scope with `ATTN_DATA_DIR`; never redirect `HOME`.
- Any package reaching config paths must define `TestMain`, create one temp dir,
  and call `config.ScopeTestEnvironment(dir)` before `m.Run()`.
- Do not replace that call with raw `os.Setenv`: the helper also clears inherited
  `ATTN_DB_PATH`, `ATTN_SOCKET_PATH`, `ATTN_CONFIG_PATH`, and `ATTN_PLUGIN_DIR`.
- Individual tests may add `t.Setenv("ATTN_DATA_DIR", t.TempDir())`.
- Under `go test`, missing `ATTN_DATA_DIR` intentionally panics.

## Testing tools

- A test that asserts elapsed time (backoff, debounce, recurrence) or that
  something **never** happens runs under `synctest.Test`: no sleeps, no poll
  loops.
- A unit with a stated invariant and a large input space gets a
  `pgregory.net/rapid` property beside its example tests. Rapid explores, it
  does not document. Commit the `testdata/rapid/` seeds it writes on failure.
- Behavior that _is_ the network being bad (backpressure, eviction, reconnect)
  goes through the embedded Toxiproxy helper (`newToxiProxy(t, upstream)`).
  Anything a fake or direct channel write can express does not; it costs real
  seconds.

## How changes ship

How much process a change gets is a judgment call. When in
doubt, ask the maintainer.

- **Plans live in the garden.** Start non-trivial work by planting a plot. Write
  the execution plan in its body and add one child for each piece of work. Add
  `blocks` edges where order matters. `attn seed guide` shows the format. A
  small change can go straight to a PR.
- **Normal work targets `next`.** The repository default stays `main`, so start
  feature and fix branches from `origin/next` and pass `--base next` when opening
  their PRs. A completed `epic/*` branch also targets `next`; PRs within the arc
  target its `epic/*` branch. Squash these ordinary PRs. The routine workflow is
  in [docs/working-with-next.md](docs/working-with-next.md).
- **`main` is the accepted release line.** Only frozen `release/vX.Y.Z`
  candidates and urgent `hotfix/*` branches target `main`. The
  `epic/release-train` PR is the one bootstrap exception.
- **Prepare candidates from accepted `next`.** Run `make release
  VERSION_TAG=vX.Y.Z` from a clean, current `next`. It opens a draft candidate;
  it never merges, tags, or starts the release. CI revalidates the exact source
  Acceptance, baseline, versions, and release-only diff before merge.
- **Prepare post-release hotfixes from `main`.** Commit the fix and changelog
  fragment on a clean `hotfix/*` branch, then run `make release-hotfix
  VERSION_TAG=vX.Y.Z`. It adds fresh release metadata and opens the draft PR.
- **Sync after every main change.** Run `./scripts/sync-main-to-next.sh`, then
  merge its PR with a merge commit. Never squash or rebase a sync PR: `main`
  must remain an ancestor of `next`. Routine cherry-pick sync is forbidden.
- **A spike answers a question.** The maintainer decides what happens after
  a spike: merge it, discard it, or build on what it showed. Do not commit
  spikes.

### Experience testing

The maintainer tests how attn feels to use; the harness and figgyster cover
correctness. That testing happens at two moments: very early on spikes (is the
idea right?) and at the end of a substantial arc of PRs (does the whole thing
feel right?).

When an arc gets there, prepare the test: a running profile installed from the
branch, realistic data, and a short list of things to try, focused on what
changed and what you could not judge yourself: feel, latency, keyboard flow. The
maintainer should spend those minutes trying things.

### Protocol bumps and migrations

Protocol version bumps and DB migrations are day-to-day work. Do them, verify
them, and do not present them as risks, blockers, or reasons to pause.

## Pull requests

Open PRs ready for review, not as drafts. An ordinary PR to `next` does not
need rebasing solely because `next` advanced. Rebase when the PR conflicts or
when the change depends on newer work from `next`; otherwise, merge once the
exact head is green, approved, and GitHub reports it mergeable. Release,
hotfix, and main-to-next sync PRs follow
[docs/making-a-release.md](docs/making-a-release.md).

To wait on a GitHub PR, run `attn pr wait-ready <pr> --repo <owner/repo>
--reviewer <login>` once; do not poll checks, reviews, and comments separately.

Its `--help` explains exit codes, baselining, and resume; the output carries
every comment body, verdict, and failing check URL, so act on it.

## Misc expectations

- Before fixing a bug, first diagnose root cause. If you can't, resist blind
  changes. Instead, propose adding instrumentation so next time you can
  diagnose.
- Do not copy production code into tests or test compile-time guarantees. Minimize
  unit tests, prefer fast integration tests instead.
- Every PR adds a changelog fragment under `changelog.d/`. CI enforces it.
  Do not edit `CHANGELOG.md` directly; it is compiled from fragments at
  release time. Format and release process: docs/making-a-release.md.
- The daemon is the source of truth and authoritative. Do not make the app the
  source of truth for anything besides rendering.
- Avoid continuously repainting animations. They tend to have high CPU usage and
  drain battery.
- Conventional commit titles with a scope, in plain language:
  `fix(queue): hand over the next agent however a turn closes`.
- `make lint-go` runs the comment linter and staticcheck (pinned by the `tool`
  directive in `go.mod`, configured by `staticcheck.conf`); CI runs both beside
  `go vet`.

## Comments

The code says what it does; a comment earns its place only by saying what
the code cannot: a tool directive, a measured receipt behind a number, or a
trap that is not visible from the file. No block is longer than two lines;
`make lint` enforces that in Go (`internal/lint/commentblock`) and in
TypeScript (`app/lint/attn`), and CI runs it. If a compressed comment does
not make sense on its own, delete it.

## Architecture

- `cmd/attn`: CLI, agent launch, session registration, hooks/settings
- `internal/hooks`: Claude hooks and state/todo reporting
- `internal/daemon`: lifecycle, PTY orchestration, git/GitHub, WebSocket
- `internal/pty`: PTY, read loop, replay, terminal-query responses
- `internal/ptybackend`: `worker` (default) / `embedded` selector
- `internal/ptyworker`: per-session process; production PTYs run here through
  `internal/pty`, not inside the daemon
- `internal/store`: SQLite plus in-memory cache
- `internal/enrollment`: who this daemon is (`daemon-id`) and whose it is
  (`enrollment.json`), the two files that decide whether home-level state may
  live here. `Status.RequireHome` is the fence every garden/crew surface calls;
  reach it from the daemon through `Daemon.requireHome`, never by reading the
  record yourself
- `internal/crew`: what a crew member IS: the id rule, the stored registry
  record, and how a home directory under `~/.attn/crew/` becomes one. Files stay
  canonical: the registry records where a home lives, never what it says. The
  daemon half (`internal/daemon/crew.go`) owns the binding a session launches
  with and the one-active-binding-per-member rule over it
- `internal/bus`: durable event bus (domain facts, per-consumer cursors)
- `internal/docstore`: document-store query semantics, SQL compilation, and the
  physical naming (no DB handle; `internal/store/documents.go` executes what it
  compiles). A collection is its own table `doc_<id>`, minted from its registry
  row; a declared field is an indexed generated column over the body. Every
  identifier the store executes is derived here from an integer or a validated
  field name, never from caller text
- `internal/jobs`: durable job queue (retry/backoff, coalescing, commit fence,
  cron entries). Every background duty and every periodic tick runs on it
- `internal/apps`: an app's identity: the name rule, and the bus consumer
  (`app:<name>`) and document namespace (`app/<name>`) derived from it. An app's
  enabled state IS its consumer's enabled bit; there is no registry column for
  it, and nothing stores the derived names. Registry tables and the lifecycle
  handlers live in `internal/store/apps.go` and `internal/daemon/apps.go`; see
  `docs/glossary.md` for app vs plugin
- `internal/supervise`: process supervision for long-lived daemon children
  (restart backoff, generation fencing, stability window, disconnect grace,
  give-up parking, per-child log capture). Consumers name a child and hand over
  a start function; the package knows nothing about what it supervises. The
  plugin runtime is one consumer, the app runtime's sidecar is the other
- `internal/automode`: pi auto mode's config value and the rules about what may
  be written into it, the Go mirror of `plugins/attn-pi/automode/config.ts`,
  and the JSON handed to a driver at launch. Storage is
  `internal/store/automode.go`, whose `PromoteAutoModeProposal` is the ONLY way
  a pattern or a model reaches the config; every CLI-reachable verb writes a
  proposal instead
- `internal/classifier`: stop-time state classification
- `internal/transcript`: assistant-message extraction from JSONL
- `app`: Tauri frontend; WebSocket `ws://localhost:9849`

Frontend map (`app/src`). `app/AGENTS.md` covers components and test patterns;
this is where daemon traffic lands:

- `hooks/useDaemonSocket.ts`: the socket. Connection, reconnect/circuit breaker,
  the event switch, and every `send*` command. Its return value is the frontend's
  entire daemon API. `App` is its only caller and publishes it through
  `contexts/DaemonApiContext.tsx`; everything below reads it with `useDaemonApi()`.
- `hooks/daemonPendingRequests.ts`: request/result correlation. A fallible
  command parks its promise under a key until the matching `*_result` event
  lands. `settlePendingRequest` is the typed way in. `sendRequest` (fresh request
  id per call) and `sendKeyedRequest` (caller's key, for the deliberately
  last-writer-wins commands) are the two ways out; both reject when the socket is
  down and time out on a daemon that never answers.
- `hooks/daemon<Domain>Events.ts` (`Fs`, `Notebook`, `MarkdownAnnotation`):
  per-domain event bodies lifted out of the switch, reached from its `default`.
  Grep a wire name (`fs_write_result`) to find the module that owns it. Adding a
  domain means a new module plus one line in that `default` chain.
- Markdown annotations use `daemonPendingRequests.ts`'s keyed correlation with
  `<op>:<documentUri>` keys, last-writer-wins superseding, and `request_id`
  checks. File and seed messages also carry typed source fields; the daemon
  validates those fields and never parses authority from the URI.
  `daemonMarkdownAnnotationEvents.ts` only decodes results.
- `store/daemonSessions.ts`: Zustand store for session/PR state.
- `pty/`: transport, attach planning, binary frame decode, runtime lifecycle.
- Tests are topic-suffixed: `Source.concern.test.tsx`. Keep that: the suffix
  names the behavior, and the set of suffixes maps a large file's seams.

States: `launching`, `working`, `pending_approval`, `waiting_input`, `idle`,
`unknown`, `scheduled`, `recoverable`. A turn opens when a session reaches a
state that wants the user (`internal/attention`) and closes only when the user
settles it; `turn_owed` is derived at broadcast from the persisted
`turn_opened_at`/`turn_settled_at` stamps.

IPC: `~/.attn/attn.sock`. WebSocket clients buffer 256 messages; sustained
over-send may drop messages or disconnect slow clients.

We avoid cross-profile app<>daemon contamination via the profile's client token.

## Cross-cutting contracts

### Protocol

For command/event/message-shape changes:

1. edit `internal/protocol/schema/main.tsp`;
2. run `make generate-types`;
3. Increment `ProtocolVersion` in `internal/protocol/constants.go` and
   `PROTOCOL_VERSION` in app/src/hooks/useDaemonSocket.ts

Do not hand-edit generated `internal/protocol/generated.go` or
`app/src/types/generated.ts`.

### The app SDK

`sdk/attn-app/` is the one TypeScript source apps import
(`@victorarias/attn-app`), and it has two consumers that must not disagree. The
frontend depends on it as a pnpm workspace package, so a view's React is attn's
React by construction. The binary embeds its declarations and `attn app apply`
materializes a types-only package under `<data-dir>/apps/sdk/<hash>`, linked
into the app's `node_modules`.

After editing `sdk/attn-app/src`, run `make generate-sdk` and commit
`internal/appbuild/sdkdist/`: `//go:embed` reads from the Go tree, so the
emitted `.d.ts` is generated _and_ committed like `generated.go`. `make
check-sdk` fails on a stale copy and runs in CI's Frontend job. React's own
declarations are pinned in `appbuild.ReactTypesVersion` and must match what the
frontend's lockfile resolves.

### Event bus

`internal/bus` is an append-only log of things that happened in the daemon. When
changes happen, the code that changed it publishes a **fact** describing the
change, and everyone who cares reads facts off the log in order. This is the
foundation of event driven behavior that allows attn to grow without a linear
increase in complexity.

- A **fact** is one row on the log: a name (`session.state.changed`,
  `garden.harvested`), a **subject** (the id of the thing that changed), and a
  small JSON payload. Facts describe what happened; they never carry byte
  streams (terminal output, attach data, file contents).
- A **consumer** reads facts. A _durable_ consumer has a **cursor**, the seq of
  the last fact it handled, stored in the DB, so it resumes where it left off
  after a restart and never misses one. An _ephemeral_ consumer (the WebSocket
  hub) starts at the head and has none.
- A **projection** is the hub's rule for turning one fact into WebSocket
  traffic for the app. The table is `wireProjections` in
  `internal/daemon/bus.go`. Most projections re-read the entity and push it;
  a **snapshot** projection pushes a whole list (all sessions, all seeds)
  because that is what the app renders.
- **Retention** trims old facts, but never past the cursor of an *enabled*
  durable consumer or of an installed app, enabled or not. Such a consumer
  that stops reading **pins** the log and it grows. A disabled ordinary
  consumer holds nothing and can lose facts.

What that means when you change things:

- To broadcast a change, publish a fact with a subject. If you do not know
  the entity's id at that point, fix the code so you do; a subject-less fact
  forces a whole-list re-push.
- A projection only writes to the wire. It must not change state or publish
  another fact; the bus holds its publish lock during fan-out, so a nested
  publish deadlocks.
- Changing many entities at once: one fact each, inside `coalesceSnapshots`,
  so the app gets one list push instead of N.
- A durable consumer's handler may see a fact twice; make it idempotent. A
  handler that keeps failing stalls that consumer alone.
- Everything that registers a durable consumer must also `Unregister` it on
  uninstall, or the abandoned cursor pins retention forever.
- `attn bus status` shows consumers, cursors, and who is pinning; Use `attn bus
disable|enable <consumer>` to disable/enable consumers. `ATTN_BUS_RETENTION`
  and `ATTN_BUS_PIN_ALARM_AGE` shorten the 30-day window and the pin alarm so
  you can watch either happen.

### macOS shortcuts

Packaged-app default menu accelerators can consume shortcuts before DOM keydown.

- Cmd+C: handle the DOM `copy` event (`GhosttyTerminal`), not keydown alone;
  verify with `real-app:scenario-terminal-block-copy`.
- Check every new shortcut against `Menu::default` accelerators.
- In `app/src-tauri/src/lib.rs`, remove a conflicting predefined menu item so
  the WebView resolver handles rebindings.
- Use `dispatch_native_shortcut` only when a visible/relabeled native menu item
  is required; it hardcodes the action.

## Diagnostics

- Daemon: `~/.attn/daemon.log` (profile data-dir equivalent for non-prod).
- Worker PTY: `<data-dir>/workers/<daemon-instance>/log/<session>.log`;
  `pty.Session` logs are here, not in `daemon.log`.
- Debug daemon: quit the app first (it respawns without `DEBUG`), then run
  `DEBUG=debug attn daemon ensure` for the selected profile.
- Daemon code: use `d.logf(...)` / injected `LogFunc`; background stderr drops
  `log.Printf()`.
- Frontend: use prefixed console logging and Tauri DevTools.
- Hard-to-reproduce UI bugs: prefer disk JSONL under
  `$APPLOCALDATA/debug/<name>.jsonl`; follow `terminalDiagnosticsLog.ts` or
  `terminalLinkHitTestLog.ts`; remove temporary instrumentation after the fix.

## Native VT library

- `internal/ghosttyvt` links `libghostty-vt` via cgo on darwin/arm64 and
  linux/amd64+arm64; everything else gets a pure-Go stub. `//go:build`, the
  `#cgo` tuples, `scripts/lib/libghostty-vt.sh`, and the Makefile list the same
  platforms. Change all four together.
- The static archive is per platform, gitignored under
  `third_party/ghostty-vt/<goos>_<goarch>/`, and fetched (sha-verified against
  `ghostty-vt-native.lock`) on the first `make build`/`dev`/`install*`. Source
  builds (zig 0.16.x) happen only on a fresh pin, a failed download, or
  `ATTN_VT_FROM_SOURCE=1`.
- Bumping: `ghostty-vt.pin` must be the commit upstream's rolling `tip` release
  was built from: only `tip` ships `ghostty-vt.wasm`, and its assets are
  overwritten on every commit. Then run
  `make publish-ghostty-vt-wasm`, then `make publish-native-vt` (zig + `gh`),
  and commit both regenerated locks.
- A bump moves terminal behavior and can move the wasm ABI: re-take receipts
  (`abi.layout.test.ts`, `go test ./internal/pty -run TestKittyWireRewriteCorpus
-update`, tripwire comments in `internal/pty/wirefeed.go`).
