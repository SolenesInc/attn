# Maintainer contracts

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

## Protocol

For command/event/message-shape changes:

1. Edit `internal/protocol/schema/main.tsp`; run `make generate-types`.
2. Increment `ProtocolVersion` in `internal/protocol/constants.go` and
   `PROTOCOL_VERSION` in `app/src/hooks/useDaemonSocket.ts`.

Never hand-edit `internal/protocol/generated.go` or `app/src/types/generated.ts`.

## The app SDK

After editing `sdk/attn-app/src`, run `make generate-sdk` and commit
`internal/appbuild/sdkdist/`. CI checks freshness, as does `make check-sdk`.
Keep `appbuild.ReactTypesVersion` aligned with the frontend lockfile.
Views import React through `@victorarias/attn-app` to share attn's instance.

## Event bus

- Publish entity ids as fact subjects; omit byte streams.
- Projections only write to the wire. State changes or nested publishes can deadlock.
- Bulk changes publish one fact per entity inside `coalesceSnapshots`.
- Durable handlers must be idempotent; unregister consumers on uninstall.
- Inspect with `attn bus status`; control delivery with `attn bus disable|enable`.
  Tests can shorten `ATTN_BUS_RETENTION` and `ATTN_BUS_PIN_ALARM_AGE`.

## Native VT library

- Keep `internal/ghosttyvt` build tags, cgo tuples, `scripts/lib/libghostty-vt.sh`,
  and Makefile platforms aligned: darwin/arm64, linux/amd64+arm64.
- `ghostty-vt.pin` must match upstream's rolling `tip` build (the wasm source).
  Run `make publish-ghostty-vt-wasm`, then `make publish-native-vt`; commit both locks.
- On a pin bump, verify `abi.layout.test.ts`, rerun
  `go test ./internal/pty -run TestKittyWireRewriteCorpus -update`.
