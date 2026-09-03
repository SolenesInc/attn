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

The app (the Tauri UI) runs on macOS and Linux; Linux CI exercises its packaged
application tree under Xvfb. The daemon stays portable across both platforms.

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
- Production `~/.attn` is read-only. Copy data out; never run a test daemon
  against it, open it read-write, or clean it.
- Non-production builds, installs, launches, and restarts are pre-authorized.
  Production `make`, `make install`, and `make install-daemon` need Victor's
  explicit approval. Check the `[attn profile=…]` banner first.
- Never restart the daemon hosting this session.

## Working rules

- The daemon owns application state; the app owns rendering.
- Diagnose before fixing. If the cause is unknown, propose instrumentation.
- Prefer fast integration tests. Do not copy production code into tests or
  test compile-time guarantees.
- Avoid continuous repainting. Check idle CPU and memory.
- New actions need reversal and inspection: snooze/unsnooze, create/clean.
- Check affected CLI, daemon, app, protocol, and Linux paths before finishing.
  `cmd/attn` and `internal/**` must cross-compile and run on Linux.
- Plant a Garden plot for non-trivial work; put the plan in its body, pieces
  in children, and ordering in `blocks` edges. Offer parallel delegations
  for independent pieces. Small changes can go straight to a PR.
- Do not commit spikes; Victor decides what follows.
- Protocol bumps and DB migrations are routine. Make and verify them.
- Comments explain directives, measured limits, or hidden traps. Maximum two
  lines per block, enforced by `make lint`. Delete unclear compressed comments.
- Product prompts address the user, never "Victor". Distinguish the agent
  changing attn from the agents it runs.

## Commands

| Task | Command |
| --- | --- |
| Go tests | `make test` |
| Frontend tests | `make test-frontend` |
| Browser tests | `make test-e2e` |
| Go + frontend | `make test-all` |
| Go + frontend + browser | `make test-harness` |
| Frontend dev server | `pnpm --dir app run dev` |
| Lint | `make lint` |

## Test safety

- Never resolve test config paths to production `~/.attn`; never redirect `HOME`.
- Packages reaching config paths need `TestMain`: create a temp dir and call
  `config.ScopeTestEnvironment(dir)` before `m.Run()`. It sets `ATTN_DATA_DIR`
  and clears inherited DB/socket/config/plugin overrides. Raw `os.Setenv`
  is insufficient. Missing `ATTN_DATA_DIR` intentionally panics under `go test`.
- Per-test isolation may use `t.Setenv("ATTN_DATA_DIR", t.TempDir())`.
- Use `synctest.Test` for elapsed-time or never-happens assertions; no sleeps/polls.
- Use `pgregory.net/rapid` for invariants over large inputs; commit failure seeds.
- Use `newToxiProxy(t, upstream)` for network failures a fake cannot express.

## Ownership

- New production PTYs run in the shared Rust `pty-host`; `internal/ptyworker`
  and `internal/pty` retain live legacy sessions during migration.
- `internal/store` owns SQLite/cache; `internal/attention` owns turn predicates.
  Derive `turn_owed` from persisted opened/settled timestamps.
- `internal/jobs` owns background duties and periodic ticks;
  `internal/supervise` owns long-lived daemon children.
- Garden/crew handlers call `Daemon.requireHome` (`internal/enrollment`).
  Outposts own sessions; Garden/crew belong to their home.
- Crew files are authoritative; the registry records paths. One active session
  binding per member (`internal/daemon/crew.go`).
- `internal/docstore` compiles SQL; `internal/store/documents.go` executes it.
  SQL identifiers come from integers or validated field names, never caller text.
- App consumer/namespace names derive from `internal/apps`; enabled state is
  the consumer's enabled bit.
- Auto-mode pattern/model writes go only through `PromoteAutoModeProposal`
  in `internal/store/automode.go`. Agents propose; only the user promotes.

## Protocol

For command/event/message-shape changes:

1. Edit `internal/protocol/schema/main.tsp`; run `make generate-types`.
2. Increment `ProtocolVersion` in `internal/protocol/constants.go` and
   `PROTOCOL_VERSION` in `app/src/hooks/useDaemonSocket.ts`.

Never hand-edit `internal/protocol/generated.go` or `app/src/types/generated.ts`.

## The app SDK

After editing `sdk/attn-app/src`, run `make generate-sdk` and commit
`internal/appbuild/sdkdist/`; `make check-sdk` checks freshness.
Keep `appbuild.ReactTypesVersion` aligned with the frontend lockfile.
Views import React through `@victorarias/attn-app` to share attn's instance.

## Event bus

- Publish entity ids as fact subjects; omit byte streams.
- Projections only write to the wire. State changes or nested publishes can deadlock.
- Bulk changes publish one fact per entity inside `coalesceSnapshots`.
- Durable handlers must be idempotent; unregister consumers on uninstall.
- Enabled durable consumers and all installed apps pin retention. Disabled
  ordinary consumers release it. Pin alarms never discard unread facts.
- Inspect with `attn bus status`; control delivery with `attn bus disable|enable`.
  Tests can shorten `ATTN_BUS_RETENTION` and `ATTN_BUS_PIN_ALARM_AGE`.

## Diagnostics

- Daemon: `<data-dir>/daemon.log`.
- Dedicated PTY worker: `<data-dir>/workers/<daemon-instance>/log/<session>.log`.
- Shared PTY host: `<data-dir>/pty-hosts/<daemon-instance>/log/host.log`.
- Daemon code uses `d.logf(...)` or injected `LogFunc`; background stderr is lost.
- To debug an isolated daemon, quit its app, then `DEBUG=debug attn daemon ensure`.

## Native VT library

- Keep `internal/ghosttyvt` build tags, cgo tuples, `scripts/lib/libghostty-vt.sh`,
  and Makefile platforms aligned: darwin/arm64, linux/amd64+arm64.
- `ghostty-vt.pin` must match upstream's rolling `tip` build (the wasm source).
  Run `make publish-ghostty-vt-wasm`, then `make publish-native-vt`; commit both locks.
- On a pin bump, verify `abi.layout.test.ts`, rerun
  `go test ./internal/pty -run TestKittyWireRewriteCorpus -update`, and check
  measured limits in `internal/pty/wirefeed.go`.

## Verification

Choose each PR's verification and any exemption from
[profiles.md](docs/profiles.md#verification-requirements). App-observable changes
need the running app; visible changes need a recording. If required verification
is unavailable, ask before merging.

### Experience testing

Test feel with Victor early in spikes and at the end of substantial PR arcs.
Prepare a running profile from the branch, realistic data, and a short list
covering changed behavior, latency, and keyboard flow.

## Guidance

- Read [glossary.md](docs/glossary.md) before naming domain concepts; update
  definitions and implementation together.
- Read [working-with-next.md](docs/working-with-next.md) before creating
  branches, opening or merging PRs, or waiting on reviews.
- Read [making-a-release.md](docs/making-a-release.md) before adding changelog
  fragments, preparing releases or hotfixes, or syncing `main` into `next`.
- Read [profiles.md](docs/profiles.md) before installing, launching, or
  verifying a profile, and when choosing a PR's verification requirements.
- Read [app/AGENTS.md](app/AGENTS.md) before changing frontend code or shortcuts.
- Read [harness guidance](app/scripts/real-app-harness/AGENTS.md) before
  writing/running packaged-app scenarios or recording/publishing evidence.
- Read [pi guidance](plugins/attn-pi/AGENTS.md) before changing pi/nisse drivers
  or auto-mode permissions.
