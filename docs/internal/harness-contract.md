# Harness contract

Every coding agent harness that attn hosts sits behind one Go contract in `internal/harness` and one pushed wire catalog (`harness_catalog` and `harness_logo`). The daemon, session monitor, and PTY packages ask the Go contract, and the native app reads the pushed catalog, instead of branching on a harness id. `pty-host` receives a session's title parser kind as spawn data. The CLI talks to the daemon over its socket, and the phone app talks to the Home Gateway.

Four adapters implement the contract under `internal/harness/internal/`:

- `internal/harness/internal/claude`: Claude Code (terminal process, hook config, transcript, Claude mod, kept conversations, warm spare).
- `internal/harness/internal/codex`: Codex (plain and shared Codex app-server, hook config, transcript, Codex updates, session instructions).
- `internal/harness/internal/sigma`: Sigma (`attn-app-server-v2`, built in behind `sigma_enabled`).
- `internal/harness/internal/plugin`: every harness a plugin registers through `driver.register`, with or without `attn-app-server-v2`.

Outside `internal/harness/internal/`, only `internal/harness/builtin` and `internal/harness/conformance` may import adapter packages.

## Contract parts

### Required parts

- `harness.Harness` (`internal/harness/harness.go`): the adapter interface. Generic code calls `Facts`, `Health`, `Models`, `Plan`, `Launch`, `Conversation`, `End`, `Terminal`, `Start`, `Recover`, `SettingChanged`, `Released`, `Stop`, and `Conversations`.
- `harness.Facts` (`internal/harness/harness.go`): static adapter data (`ID`, `DisplayName`, `Logo`, `Options`, `Executable`, `SkillDirs`, `CacheTTL`, `Tiers`, `CrewModel`, `Signals`, `Recovery`, `ModelDiscovery`, `Activity`, `ConversationIsSession`, `ServerTerminal`, `ReservesServer`, `Owner`, `Caller`). Every adapter states every field, and the zero value is a statement rather than a leak.
- `harness.Conversation` (`internal/harness/conversation.go`): a stateless handle built from `SessionFacts` on each call. `Deliver` and `Compact` return a `Custody` with one `Outcome` (`Taken`, `Held`, `Deferred`, `Rejected`, `Stopped`, or `NotMine`). `CanCompact` returns `nil` or a refusal error. `Input.Turn` asks for a steer or an interrupt of the running turn; an adapter that cannot place one answers `Rejected`, and attn queues the input for the turn's end. A `Taken` custody sets `TookAs` to the turn use it applied: `Steer` when the running turn took the input, `Interrupt` when the input started the next turn after the adapter stopped a running one, and `Queue` when no turn was running and the input started one.
- `harness.ConversationStore` (`internal/harness/harness.go`): checks resume readiness and admission (`Check`, `Prepare`, `Admit`), reads transcripts and harness versions (`Page`, `LastReply`, `Window`, `Activity`, `Version`), and maps transcript paths (`Locate`, `IDFromPath`).
- `harness.Events` (`internal/harness/events.go`): the single evidence sink from adapters into core (`Turn`, `Compacting`, `Prompt`, `Tool`, `Usage`, `Replied`, `Named`, `Moved`, `Compaction`, `Returned`, `Settled`, `Reach`, `Reloaded`).
- `harness.Host` (`internal/harness/host.go`): what attn gives an adapter, including `Life`, `Events`, `Setting`, `LoginEnv`, `ServerTools`, `TerminalTools`, `Sessions`, `Files`, `Terminals`, `Records`, `Clock`, `Executable`, `RegisterCron`, `Notifications`, `Profiles`, and command and snapshot registration.
- `internal/harness/appserver`: the shared `attn-app-server-v2` client used by Sigma and by server-backed plugins. Servers declare their abilities (`compact`, `watch`, `targets`, `steer`, `interrupt`, `question_safe`).
- `harness.DirectLauncher` (`internal/harness/direct_launch.go`): wrapper-side launch construction (`Facts`, `ResolveExecutable`, `Prepare`, `Build`, `FindTranscript`) used by `cmd/attnd` (`attn -s` with `ATTN_DAEMON_MANAGED=1`) when `direct_agent_launch_enabled` is off.

### Shared optional parts

Generic code asks for an optional interface with `harness.As[T](h)`. These shared parts serve more than one harness and need no allow-list entry:

- `harness.OneShot` (`internal/harness/oneshot.go`): answers one prompt without a session for background tasks and `attn model` (Claude Code, Codex, Sigma).
- `harness.Trust` (`internal/harness/harness.go`): pre-records folder trust before unattended launches with `TrustFolder` (Claude Code, Codex).
- `harness.Transcript` (`internal/harness/transcript.go`): provides transcript format, live `TranscriptWatcher`, usage source resolution, and `LastAssistant` reading (Claude Code, Codex).
- `harness.CallerResolver` (`internal/harness/caller.go`): authenticates and resolves server-backed tool and CLI callers (`OwnsCredential`, `Authenticate`, `IdentifyCall`, `ResolveCaller`). Codex implements it as an adapter, and `appserver.Client` implements it for Sigma and server-backed plugins.

### Wire catalog and logos

The daemon projects every registered harness into `HarnessCatalogMessage` (`harness_catalog` in `internal/protocol/schema/main.tsp`). It sends the catalog at connect, on `get_settings`, after login-environment capture, and on plugin or setting changes. Each `Harness` record carries `id`, `name`, `offered`, `installation` (`installed`, `missing`, `checking`), `logo` (content hash), `options` (`HarnessOptions`), and `activity_effort`.

The native app reads `Facts::harnesses` (`native/crates/attn-client/src/store.rs`, `native/crates/attn-app/src/harness/catalog.rs`). It fetches unknown SVG logos once by hash through `harness_logo` and caches them under `<data-dir>/cache/harness-logos/` (`native/crates/attn-app/src/harness/logo.rs`). Built-in adapters embed their SVG marks. Plugins supply theirs in `driver.register`.

## Approved leaks

A leak is a place where a harness's identity or a single-implementer contract member appears outside its adapter. `scripts/harnesslint` enforces the mechanical boundaries (harness literals, adapter imports, `harness.Host` names, and `harness.As[T]` caller files) against `scripts/harnesslint/allow.txt`. Adding a new leak or optional single-implementer part requires user approval.

### Leak 1: optional parts with one implementer

Generic code asks for these interfaces with `harness.As[T](h)` only in the caller files listed in `scripts/harnesslint/allow.txt`:

- `harness.Keeper` (`internal/daemon/conversation_keep.go`): copies, restores, and forgets conversations and auxiliary files (Claude Code).
- `harness.SpareStarter` (`internal/daemon/model_call_spare.go`): starts a warm spare process ahead of a one-shot call (Claude Code).
- `harness.Targets` (`internal/daemon/harness_targets.go`): lists and validates execution targets (server-backed plugins).
- `harness.ScreenReader` (`internal/sessionmonitor/folder_trust.go`, `internal/daemon/launch_watch.go`, `internal/daemon/agent_peek.go`): reads folder-trust prompts and composer suggestions off a terminal screen (Claude Code).
- `harness.Renamer` (`internal/daemon/monitor_leaves.go`): mirrors a session rename into the harness server (shared Codex).
- `harness.Channel` (`internal/daemon/harnesses.go`): checks whether a harness's direct delivery channel is up (Claude mod).
- `harness.Runs` (`internal/daemon/harnesses.go`): notifies a plugin's RPC driver when a run ends and resolves its route (plugin adapter).

Single-implementer members inside `Events`, `TurnEvent`, `Input`, `Custody`, `LaunchRequest`, and `TerminalEvent` approved under Leak 1:

- `TurnEvent.Stop` background work and yield fields (`Left`, `Yielded`): Claude Code stop hooks.
- `Events.Named`: session rename reported by shared Codex.
- `Outcome` values `Held` and `Stopped`, `Input.Ring`, `Events.Settled`, and `Events.Returned`: Claude mod held-ring lifecycle.
- `Custody.Uncertain`: plugin RPC direct delivery outcomes.
- `Events.Reach`: Claude mod channel reachability reports.
- `TerminalEvent` values `Handed`, `Working`, and `Restarting`: shared Codex terminal lifecycle.
- `LaunchRequest.Note`: plugin RPC launch metadata.

### Leak 2: harness-specific features with their own wire or UI

These features stay isolated in their adapter, dedicated command or UI folders, or pricing tables without branching in generic launch or monitor code:

- Shared Codex updates and restarts: `internal/harness/internal/codex/` (registered through `harness.Host`), `cli/attn/src/commands/codex.rs`, and `native/crates/attn-app/src/harness/codex/`.
- Session instructions (`attn session instructions`): `internal/harness/internal/codex/instructions*.go` (registered through `harness.Host`).
- Claude mod outpost reach routing (`claude_mod_reach`, `claude_mod_reach_report`): `internal/harness/internal/claude/mod.go`, wired in `internal/daemon/harnesses.go`, `internal/daemon/monitor_leaves.go`, and `internal/daemon/harness_events.go`.
- Codex auto-review billing alias (`codex-auto-review` in `session_cost.billed_as.<model>`): `internal/sessioncost/prices.go`, `internal/store/legacy_migration_policy.go`, and `native/crates/attn-app/src/settings/agents/pricing.rs`.

### CLI hook adapters

`cli/attn/src/hooks/` keeps one file per hook-based harness (`claude.rs`, `codex.rs`) for hook payload fields, tool names, and caller variables. Both send the same neutral evidence messages to the daemon.

### Remaining violations

`scripts/harnesslint/violations.txt` records only remaining unapproved leaks that must shrink, never new violations. Currently it holds three fallback lines: one in `classifierHarness` in `internal/daemon/monitor_home_calls.go` and two in `titleProviderAgent` in `internal/daemon/session_title.go`.

### What is not a leak

- `Facts` fields: every adapter states them.
- Terminal title parsers in `pty-host/`: the PTY host keeps parsers as code and receives `TitleKind` as spawn data.

## Build and conformance checks

- `make lint-harness` (`go run ./scripts/harnesslint`): fails on any unlisted built-in harness literal in non-test Go, `pty-host/`, `native/`, `cli/`, or `mobile/` files. It also fails on adapter imports outside `builtin` and `conformance`, references to symbols declared in an adapter package, `harness.Host` methods or types naming a harness, `harness.As[T]` calls for a Leak 1 part outside `allow.txt`, and stale entries in `allow.txt` or `violations.txt`.
- `internal/harness/conformance` (`TestEveryHarnessAnswersTheContractWithItsFake` in `internal/daemon/harness_conformance_wire_test.go`): exercises every registered adapter's facts, health, options, and no-channel conversation refusal against its fake harness.
- `make check-types`: runs `go run ./scripts/harnessdoc docs/guide/reference/harnesses.md` and fails if the generated capability table drifts from `internal/harness/builtin`.
- The `verify-harness` skill (`.agents/skills/verify-harness`): before a landing, runs `make lint-harness` and has an agent read the change for leaks the lint can't see, such as generic code that parses a value a harness owns, or depends on a fact only one harness sets.

## Add a harness

### Add a plugin harness

Use a plugin unless the harness must ship inside attn. Implement `driver.register` (and `attn-app-server-v2` if conversations live in a server) as [Writing a harness plugin](../guide/reference/writing-harness-plugins.md) describes. You need no Go, Rust, or app code changes: the plugin adapter builds `Facts`, pushes the harness name, options, and SVG logo into `harness_catalog`, and serves `harness_logo`.

### Add a built-in adapter

1. Create a package under `internal/harness/internal/<name>/` implementing `New(...)` (`harness.Harness`), `Facts()`, `Launcher()` (`harness.DirectLauncher`), an embedded SVG `Logo`, and any shared optional parts (`OneShot`, `Trust`, `Transcript`, `CallerResolver`). If the harness speaks `attn-app-server-v2`, bind it through `internal/harness/appserver`.
2. Register the adapter in `internal/harness/builtin/builtin.go` (`All`, `launchers`) and add `<name>` to `builtinHarnessIDs` in `scripts/harnesslint/main.go`.
3. If the harness reports through CLI hooks, add one hook file under `cli/attn/src/hooks/` that sends the existing neutral evidence messages.
4. Add a fake harness in `internal/fakeagent/` and a conformance row in `internal/daemon/harness_conformance_wire_test.go`.
5. Run `make generate-types` to update the capability table in `docs/guide/reference/harnesses.md`.
6. Record probed harness behavior in `docs/internal/harnesses/<name>.md` and link it from `docs/internal/harnesses.md`.
7. Run `make lint-harness`, targeted daemon conformance and wire tests, `make check-types`, and the `verify-harness` skill. Do not add an entry to `scripts/harnesslint/allow.txt` without user approval.
