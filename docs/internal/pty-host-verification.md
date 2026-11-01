# Shared PTY host

The shared Rust PTY host is experimental and off by default. Enable it under
**Settings → Experimental → Shared terminal host**. The setting applies to new and
explicitly reloaded sessions. Running sessions keep their current backend. If the
host is unavailable, new launches fall back to Go workers.
`ATTN_PTY_BACKEND` overrides the setting.

## Host builds

A daemon pins the host build it found when it started. Replacing the app bundle
changes nothing until the next daemon start. A build becomes last-known-good
after one throwaway terminal round trip passes in the current environment. The
daemon records the result, so ordinary restarts do not repeat the check.

At startup the daemon recovers sessions first, then checks a newly installed
build in the background. Until the new build passes, new sessions use the
last-known-good build, or Go workers when no build has passed yet. When a build
fails, the daemon shows one in-app notification and does not check that build
again until the build changes or the user turns the setting on again. The daemon
does not count a check as a failure when its time limit or a daemon shutdown
cuts the check short.

Every host process has its own socket and control token. A host retires 45
seconds after its last terminal closes, or after it starts without a terminal.
The next launch starts a fresh host.

## Moving running terminals to a new build

When an update changes the terminal snapshot format, a running host's
terminals can no longer hand the app a screen the app can read. As part of
startup recovery, the daemon moves each such host onto the newest build that
passed its check. The host stops reading at a chunk boundary, writes each
terminal's screen as plain VT, and re-execs itself as the new build. The host
keeps its pid, sockets, PTYs, agent processes, screens, scrollback, and command
blocks. It loses kitty images. The app gets its sessions only after every move
has finished, so the app reattaches to the new build and sees the same screen.

Before the daemon touches a live host, it rehearses the move on a throwaway
host running the same old build with a probe terminal. If the rehearsal fails,
the daemon leaves the live host alone and the host's terminals show the reload
notice. If the new build cannot take the terminals over, the new build hands
them back to the build it replaced. A host started by a build from before hosts
could move cannot move. Its terminals show the reload notice until the user reloads or closes
them.

## Upgrade test

```sh
bash scripts/test-pty-upgrade.sh
```

The script builds the last pre-Rust daemon, the current daemon, and the host,
then checks that sessions survive daemon upgrades, backend toggles, host changes,
failing host builds, and a missing host, all in temporary data directories. CI
runs it on macOS and Linux.

`scripts/build-retained-pty-host.sh OUTPUT` builds the retained host, the last
host build from before hosts could move terminals to a new build. With `ATTN_TEST_RETAINED_PTY_HOST=OUTPUT`, the shared-host integration
tests verify that the current daemon keeps operating that host's sessions and
never adopts it for new ones.

## Resource measurement

On macOS or Linux:

```sh
make build-pty-host
bash scripts/measure-pty-host.sh /absolute/path/to/attn-pty-host attempt-name
```

The script prints `PTY_RESOURCE` JSON records for 1, 8, and 32 terminals, idle and under
8 MiB output floods. On an Apple M5, 32 empty terminals take about 4.4 MiB of
physical memory (9.9 MiB resident), which is about 72 KiB for each terminal after
the first, and idle CPU is near zero. On Linux amd64, 32
empty terminals take 7.2–7.7 MiB PSS, and an 8 MiB flood costs about 320 ms of
host CPU detached and 390 ms attached.
