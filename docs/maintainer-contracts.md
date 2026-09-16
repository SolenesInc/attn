# Maintainer contracts

Read the section relevant to the code you are changing. Repository-wide safety
and workflow guidance lives in [AGENTS.md](../AGENTS.md).

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

- PTYs use dedicated Go workers by default. The experimental shared Rust
  `pty-host` setting changes future launches only; recovery always handles both.
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
- Auto-mode rule, host and policy writes go through `PromoteAutoModeProposal`
  in `internal/store/automode.go` or the pi relay's `PromoteReportedAmendment`.
  The CLI proposes; the app promotes.

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

## Native VT library

- Keep `internal/ghosttyvt` build tags, cgo tuples, `scripts/lib/libghostty-vt.sh`,
  and Makefile platforms aligned: darwin/arm64, linux/amd64+arm64.
- A fresh checkout carries no archive. `make lint`, `make test`, and `make build`
  fetch it; outside make, run `./scripts/build-libghostty-vt.sh` yourself.
- `ghostty-vt.pin` must match upstream's rolling `tip` build (the wasm source).
  Run `make publish-ghostty-vt-wasm`, then `make publish-native-vt`; commit both locks.
- On a pin bump, verify `abi.layout.test.ts`, rerun
  `go test ./internal/pty -run TestKittyWireRewriteCorpus -update`, and check
  measured limits in `internal/pty/wirefeed.go`.
