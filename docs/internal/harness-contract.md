# Harness contract

Every coding agent harness sits behind the Go contract in `internal/harness/`
(`Harness`, `Facts`, `Conversation`, `ConversationStore`, `Events`, `Host`,
shared optional parts
`OneShot`/`Trust`/`Transcript`/`FolderTrustReader`/`CallerResolver`, and
`internal/harness/appserver` for `attn-app-server-v2`) and the pushed wire
catalog (`harness_catalog` / `harness_logo`). Adapters live under
`internal/harness/internal/{claude,codex,sigma,plugin}`; only `builtin` and
`conformance` may import them.

## Leak policy

Generic daemon, session monitor, PTY, CLI and app code must not branch on a
harness id or assume single-harness behavior.

- **Approved leaks:**
  - **Leak 1:** single-implementer optional Go interfaces asserted via
    `harness.As[T]`, restricted to the caller files listed in
    `scripts/harnesslint/allow.txt`.
  - **Leak 2:** harness-specific surfaces isolated in their own adapter,
    command, UI, or pricing paths (shared Codex updates/restarts, session
    instructions, Claude mod outpost reach routing, `codex-auto-review` billing
    alias).
- **Mechanical checks (`make lint-harness`):** `scripts/harnesslint` enforces
  harness literals, adapter imports, `harness.Host` names and Leak 1
  `harness.As[T]` callers against `scripts/harnesslint/allow.txt` and
  `scripts/harnesslint/violations.txt` (shrinking debt only, never add).
- **User approval required:** never add a new entry to `allow.txt`, a new
  single-implementer optional interface, or a new single-implementer field on
  shared contract structs without explicit user approval.
- **Approved single-implementer struct fields** (not enforced by `allow.txt`,
  checked by `.agents/skills/verify-harness`):
  - `TurnEvent.Stop.{Left,Yielded}` (Claude stop hooks)
  - `Events.Named` (shared Codex rename)
  - `Outcome.{Held,Stopped}`, `Input.Ring`, `Events.{Settled,Returned,Reach}`
    (Claude mod)
  - `Custody.Uncertain`, `LaunchRequest.Note` (plugin RPC)
  - `TerminalEvent.{Handed,Working,Restarting}` (shared Codex)
- **Not leaks:** `Facts` fields (every adapter states all of them), CLI hook
  adapters in `cli/attn/src/hooks/{claude,codex}.rs`, and `pty-host` title
  parsers selected via spawn `TitleKind`.

## Adding a harness

Prefer a plugin (`driver.register`, see
[Writing a harness plugin](../guide/reference/writing-harness-plugins.md))
unless the harness must ship built-in. For a built-in adapter:

1. Implement the contract under `internal/harness/internal/<name>/` (with
   embedded SVG `Logo`; bind `attn-app-server-v2` via `internal/harness/appserver`
   if server-backed).
2. Register in `internal/harness/builtin/builtin.go` (`All`, `launchers`) and
   add `<name>` to `builtinHarnessIDs` in `scripts/harnesslint/main.go`.
3. If hook-based, add `cli/attn/src/hooks/<name>.rs` emitting existing neutral
   evidence messages.
4. Add a fake in `internal/fakeagent/` and a conformance row in
   `internal/daemon/harness_conformance_wire_test.go`.
5. Run `make generate-types` (updates `docs/guide/reference/harnesses.md` via
   `scripts/harnessdoc`).
6. Record probed external behavior in `docs/internal/harnesses/<name>.md` and
   link it from `docs/internal/harnesses.md`.
7. Run `make lint-harness`, `make check-types`, conformance wire tests and the
   `verify-harness` skill.
