# Maintainer reference

Read the sections that apply to the code you are changing. Repository-wide
safety and review rules live in [AGENTS.md](../AGENTS.md).

## Commands

```bash
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

- Tests of elapsed time (backoff, debounce, recurrence) or of something never
  happening run under `synctest.Test`. Do not use sleeps or polling loops.
- A unit with a stated invariant and a large input space gets a
  `pgregory.net/rapid` property beside its example tests. Commit the
  `testdata/rapid/` seeds it writes on failure.
- Use the embedded Toxiproxy helper (`newToxiProxy(t, upstream)`) for network
  behavior such as backpressure, eviction, and reconnect. Use a fake or direct
  channel write when it can express the same test without real-time waits.

`make lint-go` runs the comment linter and staticcheck, pinned by the `tool`
directive in `go.mod` and configured by `staticcheck.conf`. CI also runs `go vet`.

## Architecture

- `cmd/attn`: CLI, agent launch, session registration, hooks/settings
- `internal/hooks`: Claude hooks and state/todo reporting
- `internal/daemon`: lifecycle, PTY orchestration, git/GitHub, WebSocket
- `internal/pty`: PTY, read loop, replay, terminal-query responses
- `internal/ptybackend`: `worker` (default) / `embedded` selector
- `internal/ptyworker`: per-session process; production PTYs run here through
  `internal/pty`, not inside the daemon
- `internal/store`: SQLite plus in-memory cache
- `internal/enrollment`: daemon identity (`daemon-id`) and home ownership
  (`enrollment.json`). Garden and crew handlers must call `Status.RequireHome`
  through `Daemon.requireHome`.
- `internal/crew`: member ids, registry records, and home discovery.
  `internal/daemon/crew.go` owns session bindings.
- `internal/bus`: durable event bus (domain facts, per-consumer cursors)
- `internal/docstore`: queries, SQL compilation, and physical names;
  `internal/store/documents.go` executes the SQL. Derive every SQL identifier
  here from an integer or a validated field name, never raw caller text. Each
  collection's registry id names its `doc_<id>` table.
- `internal/jobs`: durable job queue (retry/backoff, coalescing, commit fence,
  cron entries). Every background duty and every periodic tick runs on it
- `internal/apps`: app names and derived bus/document identities.
  `internal/store/apps.go` and `internal/daemon/apps.go` own registry storage
  and lifecycle handlers.
- `internal/supervise`: process supervision for long-lived daemon children
  (restart backoff, generation fencing, stability window, disconnect grace,
  give-up parking, per-child logs). The plugin runtime and app sidecar supply
  their own start functions.
- `internal/automode`: pi auto mode config and launch JSON, mirrored in
  `plugins/attn-pi/automode/config.ts`. Storage is in
  `internal/store/automode.go`; permission rules are [below](#auto-mode).
- `internal/classifier`: stop-time state classification
- `internal/transcript`: assistant-message extraction from JSONL
- `app`: Tauri frontend. Read [app/AGENTS.md](../app/AGENTS.md) for the frontend
  map, tests, rendering, and native shortcuts.

States: `launching`, `working`, `pending_approval`, `waiting_input`, `idle`,
`unknown`, `scheduled`, `recoverable`. [Turn semantics](sessions.md#turns-and-the-queue)
live in `internal/attention`. WebSocket clients buffer 256 messages; sustained
over-send may drop messages or disconnect slow clients. Each profile authenticates
clients with its own [client token](daemon-ownership.md#client-token).

## Cross-cutting contracts

### Protocol

For command/event/message-shape changes:

1. Edit `internal/protocol/schema/main.tsp`.
2. Run `make generate-types`.
3. Increment `ProtocolVersion` in `internal/protocol/constants.go` and
   `PROTOCOL_VERSION` in `app/src/hooks/useDaemonSocket.ts`.

Do not hand-edit generated `internal/protocol/generated.go` or
`app/src/types/generated.ts`.

### The app SDK

`sdk/attn-app/` supplies `@victorarias/attn-app` to the frontend as a pnpm
workspace package and to `attn app apply` as embedded declarations. Apply writes
a types-only package under `<data-dir>/apps/sdk/<hash>` and links it into the
app's `node_modules`.

After editing `sdk/attn-app/src`, run `make generate-sdk` and commit
`internal/appbuild/sdkdist/`: `//go:embed` reads from the Go tree, so the
generated `.d.ts` must be committed. `make check-sdk` rejects a stale copy in
CI's Frontend job. `appbuild.ReactTypesVersion` must match the React declarations
in the frontend lockfile.

### Event bus

Read the [event bus contracts](apps-and-events.md#event-bus) before changing
publishers, consumers, or projections.

- Publish a fact with the changed entity's id as its subject. A missing subject
  forces a whole-list push.
- A projection only writes to the wire. It must not change state or publish
  another fact; the bus holds its publish lock during fan-out, so a nested
  publish deadlocks.
- For bulk changes, publish one fact per entity inside `coalesceSnapshots`
  so the app gets one list push.
- A durable consumer's handler may see a fact twice; make it idempotent. A
  handler that keeps failing stalls that consumer alone.
- Everything that registers a durable consumer must also `Unregister` it on
  uninstall, or the abandoned cursor pins retention forever.
- Inspect consumers, cursors, and retention with `attn bus status`. Use
  `attn bus disable|enable <consumer>` to control delivery.
  `ATTN_BUS_RETENTION` and `ATTN_BUS_PIN_ALARM_AGE` let tests shorten the
  30-day retention window and pin alarm delay.

## Auto mode

**Auto mode** is pi's permission system. Static rules allow work in the session's
working directory, a classifier handles the remaining actions, and the agent
receives denials in the conversation. Implementation guidance lives in the
[pi plugin guide](../plugins/attn-pi/AGENTS.md).

Its daemon-owned, global **config** holds environment prose, allow and hard-deny
patterns, the ordered models used by both classifier passes, and whether attn
sessions start with auto mode enabled. A session receives a snapshot at launch;
changes apply to later sessions.

A **proposal** records a requested allow pattern, hard deny, or model change
without applying it. **Promotion** is the human applying that proposal in the
attn app. It is the only way those rules or models enter the config. Agent-facing
commands such as `attn automode allow ...` can only propose; there is no
promotion CLI.

Environment prose describes the machine to the classifier and can be edited
directly through `attn automode env` or the app. It supplies context without
creating a rule that skips classification. An **environment template** supplies
starting text, copied into the config with no continuing link to the template.

A **denial** records a refused call. attn keeps a bounded log, notifies the user
with the blocked action and reason, and lists it under `attn automode denials`.
Each denial identifies the decision source: a static rule, `classifier-2a`,
`classifier-2b`, or the circuit breaker. The agent receives the reason and
continues its run. A plain user reply approving the action lets it retry.

Route pattern and model writes through `PromoteAutoModeProposal` in
`internal/store/automode.go`.

## Diagnostics

- Daemon: `~/.attn/daemon.log` (profile data-dir equivalent for non-prod).
  The default WebSocket endpoint is `ws://localhost:9849`.
- Worker PTY: `<data-dir>/workers/<daemon-instance>/log/<session>.log`;
  `pty.Session` logs are here, not in `daemon.log`.
- Debug daemon: quit the app first (it respawns without `DEBUG`), then run
  `DEBUG=debug attn daemon ensure` for the selected profile.
- Daemon code: use `d.logf(...)` / injected `LogFunc`; background stderr drops
  `log.Printf()`.
- Frontend diagnostics are in [app/AGENTS.md](../app/AGENTS.md#diagnostics).

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
