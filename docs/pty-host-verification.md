# Shared PTY host

The shared Rust PTY host is experimental and off by default. Enable it under
Settings → Terminal → PTY Backend. The setting applies to new and explicitly
reloaded sessions; running sessions stay where they are. If the host is
unavailable, new launches fall back to Go workers and Settings says so.
`ATTN_PTY_BACKEND` overrides the setting.

## Host builds

A daemon pins the host build it found when it started; replacing the app bundle
changes nothing until the next daemon start. A build becomes last-known-good
after one throwaway terminal round trip passes in the current environment. The
result is recorded, so ordinary restarts do not repeat the check.

At startup the daemon recovers sessions first, then checks a newly installed
build in the background. Until it passes, new sessions use the last-known-good
build, or Go when none has passed yet. A failing build produces one in-app
notification and is not checked again until it changes or the setting is turned
on again. A check cut short by its time limit or daemon shutdown is not a
failure.

Every host process gets its own socket and control token. A host retires 45
seconds after its last terminal closes, or after starting without one; the next
launch starts a fresh host. A host from an older build keeps serving its
sessions until they end.

## Upgrade test

```sh
bash scripts/test-pty-upgrade.sh
```

It builds the last pre-Rust daemon, the current daemon, and the host, then
checks that sessions survive daemon upgrades, backend toggles, host changes,
failing host builds, and a missing host, all in temporary data directories. CI
runs it on macOS and Linux.

`scripts/build-retained-pty-host.sh OUTPUT` builds the last host before host
checks. With `ATTN_TEST_RETAINED_PTY_HOST=OUTPUT`, the shared-host integration
tests verify that the current daemon keeps operating that host's sessions and
never adopts it for new ones.

## Resource measurement

On macOS or Linux:

```sh
make build-pty-host
bash scripts/measure-pty-host.sh /absolute/path/to/attn-pty-host attempt-name
```

It prints `PTY_RESOURCE` JSON records for 1, 8, and 32 terminals, idle and under
8 MiB output floods. On an Apple M5 Max, 32 empty terminals take about 8 MiB,
roughly 180 KiB per terminal, and idle CPU is near zero. On Linux amd64, 32
empty terminals take 7.2–7.7 MiB PSS, and an 8 MiB flood costs about 320 ms of
host CPU detached and 390 ms attached.
