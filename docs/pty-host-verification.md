# Shared PTY host

The shared Rust PTY host is experimental and off by default. Enable it under
Settings → Terminal → PTY Backend. The setting applies to new and explicitly
reloaded sessions; running sessions stay where they are. If the host is
unavailable, new launches fall back to Go workers and Settings says so.
`ATTN_PTY_BACKEND` overrides the setting.

## Upgrade test

```sh
bash scripts/test-pty-upgrade.sh
```

It builds the last pre-Rust daemon, the current daemon, and the host, then
checks that sessions survive daemon upgrades, backend toggles, host changes, and
a missing host, all in temporary data directories. CI runs it on macOS and Linux.

## Resource measurement

```sh
make build-pty-host
bash scripts/measure-pty-host.sh /absolute/path/to/attn-pty-host attempt-name
```

It prints `PTY_RESOURCE` JSON records for 1, 8, and 32 terminals, idle and under
8 MiB output floods. On an Apple M5 Max, 32 empty terminals take about 8 MiB,
roughly 180 KiB per terminal, and idle CPU is near zero.
