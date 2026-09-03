# Shared PTY host verification

New terminals use the Rust host. Existing Go workers keep their terminals until
those terminals exit or the user reloads them. A daemon restart recovers both
backends; a host binary change starts a new host generation without moving
sessions out of the old one.

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
3. Start an agent on Rust. Reload one old agent onto the same Rust host, checking
   that its native conversation ID resumes and the other processes stay put.
4. Restart the daemon and recover the mixed Go/Rust population.
5. Change the host executable identity and start another agent. Verify that it
   uses a different host PID while every earlier agent and worker keeps its PID.
6. Resize the old shell, exchange more input/output, and close every pane.

Readiness comes from filesystem events and the daemon's `initial_state` message.
Every survival assertion uses a unique input challenge, the registry's child and
worker PIDs, and the fixture agent's own PID. Fixed sleeps are not correctness
barriers. Deadlines only terminate stalled tests. A wrapper changes the host
executable hash for generation testing; it does not claim compatibility with an
arbitrary future host protocol.

On 2026-09-03, this passed five consecutive macOS runs with development-format
binaries, three macOS runs built from scratch with release-format stamps, and
three Linux ARM64 runs in disposable containers. Shared-host integration tests
also passed on macOS under the Go race detector and on Linux ARM64. The CI job
runs the upgrade and shared-host integration tests on macOS and Linux.

Profile cleanup authenticates each live host, verifies its PID, and asks it to
stop its children before deleting profile data. The live cleanup test covers two
generations with four PTYs, refuses forged tokens and mismatched PIDs, and keeps
an unreachable host's registry for a later attempt. It passed three repeats on
macOS with the race detector and three on Linux ARM64. Shell-close coverage
checks that termination begins with SIGHUP.

Two temporary Go build overlays checked the test's sensitivity:

| Deliberate break | Observed failure |
| --- | --- |
| Route new sessions to Go despite shared-host support | The new agent has no Rust session registry. |
| Attribute recovered Go sessions to the Rust backend | Attaching the surviving legacy shell returns `session not found`. |

Neither mutation is present in the working implementation. The packaged app's
`TERMINAL-INPUT` scenario passed using its bundled Rust host, with mock-agent
tripwires and headless model tasks disabled. It covered navigation and modifier
keys, application cursor mode, Kitty key events, Unicode, bracketed paste,
image paste, shortcuts, and zoomed terminal input.

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

Measurements below are from macOS 26.6.2 on an Apple M5 Max. Physical footprint,
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
