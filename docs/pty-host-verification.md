# Shared PTY host verification

The shared Rust PTY host is experimental and off by default. Enable it under
Settings → Terminal → PTY Backend. New and explicitly reloaded sessions use the
selected backend; changing the setting never moves or stops a running session.
Turning it off returns future launches to dedicated Go workers.

Enabling checks the host before saving the setting. A failed check or save
leaves the previous selection intact. Recovery always handles both backends,
even with the experiment off or the host executable missing. Explicit
`ATTN_PTY_BACKEND` overrides disable the Settings control.

## Host builds

A daemon pins the host build it found when it started. Replacing the app bundle
while the daemon runs changes nothing until the next daemon start. Checked
builds live under `<data-dir>/pty-hosts/<daemon-instance>/artifacts/`.

A build is checked once per environment with a throwaway terminal. The host
runs its built-in probe child, which has no shell, `PATH`, or user
configuration. The daemon resizes it and sends a nonce through the normal input
path; the child must answer with the nonce and the new size. The result is
recorded, so ordinary restarts do not repeat the check. A build that passes
becomes the last-known-good build.

At startup the daemon recovers existing sessions first, then checks a newly
installed build in the background. Until it passes, new
sessions use the last-known-good build, or Go when none has passed yet, as on
the first start after this change. A build that fails is not checked again
until it changes or the setting is turned on again, and one in-app
notification names it. A check cut short by its time limit or daemon shutdown
is not a failure;
the next start repeats it. A missing or failing bundle leaves new sessions on
the last-known-good build; with none, they use Go and Settings reports the
fallback. Validation terminals left behind by a daemon exit are removed at the
next recovery.

Every host process gets its own socket and control token. A host retires 45
seconds after its last terminal closes, and the next launch starts a fresh one.
A host from an older build keeps serving its sessions until they end.

## Reproduce the upgrade test

```sh
bash scripts/test-pty-upgrade.sh
```

The script builds the actual pre-Rust daemon at
`f68ab3f02f329f46d3b9b3d1e7603f545c27dd50`, the current daemon, and the Rust host.
Both daemon builds carry their derived snapshot-format tag. All processes use
temporary data directories. No installed daemon or real provider is involved.

`TestPTYUpgradeAcrossDaemonBinaries` exercises the app's WebSocket commands:

1. Start the old daemon with a shell and two fixture agents on Go workers.
2. Kill that daemon, start the new binary, and exchange fresh input/output with
   every surviving process.
3. Confirm the default is off and a new agent still uses Go. Enable the setting,
   start an agent on Rust, and reload one old agent onto that same Rust host.
   Check that its native conversation ID resumes and other processes stay put.
4. Delete the recorded host builds and restart with the opt-in saved, as on
   the first start after this update; the bundled build passes its check and
   becomes active. Recover the mixed population. Disable it,
   explicitly reload the promoted agent back onto Go, launch another Go agent,
   and restart again. Both populations retain their remaining PIDs and exchange
   fresh input/output; another new launch still uses Go.
5. Re-enable, change the host executable identity, and restart. Once the new
   build passes its check, start another agent. Verify that it uses a different
   host PID while every earlier agent and worker keeps its PID.
6. Resize the old shell. Restart with a build that fails its check: exactly one
   notification names it and a new agent uses the last-known-good host. Restart with
   the same build: it is not checked again and no second notification appears.
7. Restart with the host executable missing. A new agent still uses the
   last-known-good host; with the setting off, the next launch uses Go. Every
   earlier session keeps working. Close every pane.

Readiness comes from filesystem events and the daemon's `initial_state` message.
Every survival assertion uses a unique input challenge, the registry's child and
worker PIDs, and the fixture agent's own PID. Fixed sleeps are not correctness
barriers. Deadlines only terminate stalled tests. A wrapper changes the host
executable hash to test a new build; it does not claim compatibility with an
arbitrary future host protocol.

The CI job runs the upgrade and shared-host integration tests on macOS and
Linux. `scripts/build-retained-pty-host.sh` builds the oldest retained host (the
last build before host checks); with `ATTN_TEST_RETAINED_PTY_HOST` pointing at
it, a test verifies that the current daemon recovers, resizes, feeds, and
removes its sessions, and rejects that build as a new default without disturbing
them. Running it in CI is tracked in #315.

Profile cleanup authenticates each live host, verifies its PID, and asks it to
stop its children before deleting profile data. The live cleanup test covers two
host builds with four PTYs, refuses forged tokens and mismatched PIDs, and keeps
an unreachable host's registry for a later attempt. It passed three repeats on
macOS with the race detector and three on Linux ARM64. Shell-close coverage
checks that termination begins with SIGHUP.

All 22 Rust tests, clippy, the macOS backend suite with the race detector, and
the Linux shared-host and mixed-backend suites passed. Setting tests cover probe
failure, failed persistence, concurrent writes, read-only status, and overrides.
Router tests cover pending spawns retaining their owner and existing IO continuing
during a probe. A paused probe also leaves Settings snapshots and the client's
command loop available. The full frontend suite passed.

Nested-shell prompt markers retain their foreground process-group owner until
pre-exec or a foreground handoff. Deterministic Rust tests cover keepalives,
command end, and stale ownership. A live nested-shell test passes three repeats
under the race detector and fails against the pre-fix host when its next poll
overwrites the prompt with a busy claim.

Temporary Go build overlays checked the upgrade test's sensitivity:

| Deliberate break | Observed failure |
| --- | --- |
| Route new sessions to Go despite shared-host support | The new agent has no Rust session registry. |
| Attribute recovered Go sessions to the Rust backend | Attaching the surviving legacy shell returns `session not found`. |
| Ignore the saved opt-in and enable Rust on startup | The initial default-off assertion sees active Rust launches. |
| Skip Rust recovery while the experiment is off | The existing Rust agent returns `session not found` after restart. |
| Run the enable probe on the client command loop | The barrier-based responsiveness test stalls inside the probe and times out. |

None of these mutations is present in the working implementation. The packaged app's
`TERMINAL-INPUT` scenario passed using its bundled Rust host, with mock-agent
tripwires and headless model tasks disabled. It covered navigation and modifier
keys, application cursor mode, Kitty key events, Unicode, bracketed paste,
image paste, shortcuts, and zoomed terminal input.

The recorded `PTY-HOST-SETTING` scenario exercised the Settings toggle in the
packaged app. Off/on/off launched dedicated Go, shared Rust, and dedicated Go
terminals. All three retained their worker and child PIDs and returned fresh
challenge output after the toggles. Screenshots verify the effective-backend
text in each state. Headless tasks were off and the mock-agent ledger was empty.

The full concurrent Go suite passed, including all five daemon shards and the
store/docstore race checks. No test deadlines were extended or assertions removed.

## Resource experiment

```sh
make build-pty-host
bash scripts/measure-pty-host.sh /absolute/path/to/attn-pty-host attempt-name
```

The experiment logs JSON records prefixed with `PTY_RESOURCE`. It starts 32
empty 80×24 PTYs, samples the host at 1, 8, and 32 terminals, and measures two
seconds idle plus three 8 MiB plain-output transfers, detached and attached.
The fixture waits for a real terminal status reply before acknowledging a
transfer. Attached transfers additionally require the completion marker and
exactly 8,388,608 output bytes; a disconnect fails the experiment.

On Linux the probe reads proportional set size, resident size, and CPU clock
ticks from `/proc`; it reports no instruction counts, and CPU time has the
kernel's tick resolution. Measurements below are from macOS 26.6.2 on an Apple
M5 Max. Physical footprint,
resident size, CPU counters, instructions, and thread counts come from `libproc`.
CPU time is host user plus system time, converted from Mach ticks with
`mach_timebase_info`, consistent with the counters populated by
[XNU's task usage accounting](https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/kern/bsd_kern.c).
CPU values are medians of three transfers, not wall-clock latency. Instruction
counts help distinguish work reduction from frequency and scheduling noise.

These numbers include the Rust host and its terminal models, not the daemon,
app, shell/agent processes, test controller, or total machine/kernel PTY cost.
The attached phase follows the detached floods, so its memory includes output
history and must not be described as empty-terminal memory. The fixture is not
a shell; this does not measure foreground-process polling for 32 shell panes.

## Five optimization rounds

Each candidate was built, correctness-tested, measured, and either kept or
reverted before the next independent attempt. Later candidates include earlier
kept changes. All five passed ordinary unit/integration tests; the resource
experiment's exact-output checks rejected two of them.

| Round | Attempt | Measurement | Decision |
| --- | --- | --- | --- |
| 1 | Release optimization `z` → `3` | Detached CPU 227.52 → 50.04 ms/8 MiB; attached stream disconnected twice. | Revert. |
| 2 | Skip base64/JSON construction when nobody subscribes | Paired detached CPU 211.35 → 204.83 ms; instructions 3.899 → 3.731 billion. | Keep. |
| 3 | Borrow unchanged wire bytes instead of allocating a copy | Under 0.2% fewer instructions; no physical-memory reduction. | Revert; no meaningful gain. |
| 4 | Shrink each reader buffer from 16 KiB to 4 KiB | Empty 32-PTY footprint 8,733,104 → 8,290,736 bytes, 432 KiB saved. Instructions unchanged; CPU variation under 3%. | Keep. |
| 5 | Skip query scans without ESC and format cursor replies only on demand | Detached CPU 209.42 → 25.06 ms/8 MiB; attached stream disconnected. | Revert. |

The retained implementation measured 2.06–2.09 MiB with one empty terminal and
7.91–7.95 MiB with 32 across the round and final repeat. The incremental slope
from 8 to 32 terminals was about 176–177 KiB per PTY, versus 189 KiB before the
loop. Fixed startup allocations make the one-terminal total a different
quantity from this slope. Empty 32-terminal physical footprint fell about 4–5%
against the corresponding original baselines. The two-second idle samples
were around 0.001–0.002% of one CPU core, not zero.

The final sequential baseline/final repeat, after the build and test workload
ended, passed all twelve transfers. Detached CPU was 228.42 → 210.42 ms/8 MiB
and instructions were 3.919 → 3.739 billion (4.6% less work). Attached CPU was
338.45 → 347.95 ms, with essentially unchanged instructions (4.486 → 4.492
billion). There is no demonstrated attached-output CPU improvement.

The rejected fast producers exposed a throughput limit: the 256-event subscriber
queue disconnects on saturation. Increasing the queue would spend more memory
without defining slow-consumer behavior. A separate change must test bounded
backpressure, cross-session responsiveness, and disconnect/resnapshot recovery
before retaining those much larger CPU gains. The two retained changes passed
all six detached/attached transfers without losing output.

A repeat after the profile-cleanup and shell-close changes passed all six
transfers. Empty-host physical footprint was 2,146,664 bytes at one PTY,
3,965,312 at eight, and 8,405,400 at 32 (2.05 MiB and 8.02 MiB at the endpoints,
181 KiB per additional PTY from eight to 32). Detached and attached instruction
medians were 3.762 and 4.515 billion per 8 MiB; CPU medians were 281.77 and
450.95 ms. This was a separate run, not a paired CPU comparison with the baseline.

The 2026-09-04 lifecycle-cleanup comparison passed all twelve detached/attached
transfers. Empty 32-PTY footprint was 8,323,480 bytes before and 8,356,272 after
(about 7.94 and 7.97 MiB); the eight-to-32 slope was 176.7 and 178.0 KiB per PTY.
Detached/attached instruction medians changed by less than 1%. Idle samples used
0.0013–0.0025% of one core. CPU medians were 343.24/527.46 ms before and
337.91/471.29 ms after, but a single pair does not establish a speedup. These
checks support roughly unchanged resource use, not an optimization claim.

## Linux optimization round

On Linux amd64 with four CPUs, the current host used 7.2–7.7 MiB PSS with 32
empty PTYs and about 16 MiB after the floods, with 37 threads (103 with 32
subscribers). An 8 MiB flood cost 320 ms of host CPU detached and 390 ms
attached; two idle seconds cost less than one 10 ms tick. The last host before
this change measured the same within noise.

Kept after alternating measurements:

- Idle shells no longer repeat an unchanged prompt state every second. The
  daemon already ignored those repeats. With 16 idle shells, host CPU fell from
  3.2 to 1.1 ms and daemon CPU from 5.8 to 1.9 ms per 10 s.
- The daemon decodes each host frame once instead of three times. Decoding an
  8 MiB attached stream fell from 74.5 to 41.1 ms, with 5x fewer allocations;
  small frames decode 3.5x faster.
- Output bytes decode straight from JSON, cutting 27% of the allocated bytes
  per attached stream with an unchanged wire format.

Rejected: a single malloc arena saved 2 MiB PSS at 32 PTYs but cost 3–7% flood
CPU and wall time. Scanning for terminal queries only from escape bytes cut
detached flood CPU from 320 to 60 ms but again disconnected attached streams
when the 256-event subscriber queue filled. Bounded backpressure kept every
attached transfer but cost 10–16% flood CPU on its own; the pair has not been
measured together.
