# Shared PTY host

## Upgrade and compatibility tests

```sh
bash scripts/test-pty-upgrade.sh
```

Downloads the latest release and builds the last pre-Rust daemon, the current
daemon and the host to verify session survival across upgrades, backend toggles,
failing builds and mid-move daemon crashes. Requires `lsof` and `fish`. CI runs
it on macOS and Linux.

To test compatibility with a pre-move retained host:

```sh
scripts/build-retained-pty-host.sh /tmp/retained-pty-host
ATTN_TEST_RETAINED_PTY_HOST=/tmp/retained-pty-host ./scripts/test-go.sh --packages -run TestSharedHost ./cmd/attnd
```

## Resource measurement

```sh
make build-pty-host
bash scripts/measure-pty-host.sh /absolute/path/to/attn-pty-host <attempt-name>
```

Prints `PTY_RESOURCE` JSON for 1, 8 and 32 terminals (idle and 8 MiB floods).
Reference measurements: Apple M5, 32 empty terminals ~4.4 MiB physical (9.9 MiB
RSS, ~72 KiB per extra terminal, ~0 idle CPU); Linux amd64, 32 empty terminals
7.2–7.7 MiB PSS, 8 MiB flood ~320 ms host CPU detached / ~390 ms attached.

## Traps

- Released Linux hosts (`make build-pty-host-linux-{amd64,arm64}`) link with zig
  against an older glibc so they run wherever `attn` runs; release preflight
  fails if the host needs a newer glibc than `attn`.
- `sh -c 'ps -o comm= -p $(ps -o ppid= -p $PPID)'` inside a terminal shows
  whether it runs under `attn-pty-host` or a Go worker (`attn`).
