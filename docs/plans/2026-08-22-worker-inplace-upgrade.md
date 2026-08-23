# Plan: the pty-worker upgrades itself in place

## Goal

A worker whose terminal library no longer matches the app replaces its own
binary while keeping the PTY, the agent child, and the screen. The user sees
nothing. This is the promise the reload notice shipped in #989 already makes:
"a one-off from a terminal-engine upgrade, future updates handle it
automatically."

Today a bump strands every live worker: the app refuses the foreign snapshot,
the pane comes up **empty**, and the only way out is a reload that kills the
agent. That is the whole cost being removed.

## What the spikes proved

Five standalone programs, outside attn, in the session scratchpad
(`spikes/`). Every number below is measured, not assumed.

| # | Question | Answer |
|---|---|---|
| 1 | Does `execve` keep the PTY and the child? | Yes. Pid unchanged, so the child stays **our** child: `os.FindProcess(pid).Wait()` returned its real exit status (42). No SIGHUP, `$PPID` never moved, 300/300 output lines arrived with no gap and no duplicate. |
| 2 | What survives a plain-VT dump and replay? | Within one version: text, scrollback, styles, truecolor, hyperlinks, wide chars and emoji, soft wrap, modes, scrolling region, tabstops, cursor. Three gaps found — see below. |
| 3 | Does the dump cross a real version boundary? | Yes. Dumped by `d760ee96` (pre-bump), replayed by `da5ddcb0` (post-bump), the two libraries #989 actually moved between: 4/4 realistic screens matched exactly, including the primary screen underneath an alt-screen app. |
| 4 | End to end, with a live child? | Yes. A pre-bump miniature worker handed 7.5KB of VT to a post-bump binary: the new model reproduced the old one **exactly**, then kept counting to 160 with no gap, no duplicate, no overwrite. Blackout 11ms warm, ~300ms for a freshly installed (cold) binary. |
| 5 | Does the daemon's socket survive? | Inheriting the listener fd: **0 failed connects out of 2483** across the swap. Rebinding instead: 5 failures, unreachable for 12ms. Inherit. |
| 6 | Can the read loop stop without losing bytes? | Only if the PTY master is pollable. See below. |

### The three gaps spike 2 found

**A dumped screen replays one row short**, whenever the grid's bottom rows are
blank — which is every ordinary "shell printed N lines, cursor waits below
them" screen. The cursor lands *on* the last line of output instead of below
it, and everything the child prints next overwrites a row it believes is still
there. The negative control in spike 4 shows it exactly: without the fix, tick
52 (the last line before the swap) is gone and the grid is one row short
forever after.

The shortfall does not follow the cursor, so it cannot be predicted from it —
a cursor moved up-screen hides it entirely. `HandoffVT` **measures** it:
replay the dump into a scratch terminal, compare `TOTAL_ROWS`, append that
many line feeds. 13 of 14 grid shapes then round-trip exactly.

The fourteenth: a screen with a scrolling region set *and* a blank bottom row
keeps its shortfall, because the line feeds land outside the region and scroll
nothing. Programs that set a region keep the screen full, so a real session
does not reach the combination. Documented at the call site, not worked
around.

**A dump taken on the alternate screen loses the primary underneath.** The
formatter serializes the active screen only. The fix uses the fact that the
dumping terminal is about to be thrown away: dump the alt screen, write
`ESC [ ? 1049 l` to leave it, dump the primary, and emit
`primary + 1049h + alt`. That makes `HandoffVT` destructive, which its doc
line says outright.

**Kitty images do not cross.** The dump carries no image data; a placement
present before the swap is gone after. Re-transmitting is not a small fix —
an image scrolled into history has no cell to be placed at any more, so a
faithful restore is not reachable anyway. Not carried in this change. See
Follow-ups.

### The quiesce spike 6 forced

Spike 1 swapped while the reader goroutine sat blocked in `read(2)`, and
called that fine. It is not: a read that RETURNS between the last applied
chunk and the `execve` takes those bytes into an image about to disappear.
attn widens the window on purpose — its reader parks up to four coalesced
chunks in a channel. Measured with a counter the child prints as fast as it
can: swapping with the loop still running lost 128 and 256 numbers (~1KB) in
two runs out of three.

The stop has to end a read that is blocked in the kernel *without consuming
anything*, which is `SetReadDeadline` — and that fails outright on the master
creackpty hands back:

```text
SET DEADLINE FAILED: file type does not support deadline
```

Go never registers a blocking file with its poller. So a session's master is
now held **pollable**: `dup`, `O_NONBLOCK`, `os.NewFile`. Then a deadline in
the past ends the pending read, everything the reader already pulled comes
back with the error and is applied, and the rest stays in the kernel for the
next image. Quiesce measured at **38µs** under a 15MB/s stream; eight swaps
crossed with no gap and no repeat.

Two consequences worth knowing. `File.Fd()` is entitled to drop the poller
registration, so the two ioctls on the master (`TIOCSWINSZ`, `TIOCGPGRP`) go
through `SyscallConn` instead. And a pollable master no longer parks an OS
thread per live session in a blocking read — a side benefit, unmeasured.

## Architecture map

```text
Current — a bump strands the worker:
  daemon starts (new binary)
    ptybackend.connectWithIdentity
      worker RPC hello -> snapshot_format=<old>     mismatch
        decorateSessionWithTerminalBuild            terminal_build_stale=true
          app: TerminalStaleBuildNotice             "reload the session"
            reload -> the agent process dies

Target — the worker replaces itself:
  daemon starts (new binary)
    ptybackend.connectWithIdentity
      worker RPC hello -> snapshot_format=<old>     mismatch
        RPC upgrade{executable}                     NEW
          worker: quiesce read loop
                  HandoffVT + block table -> handoff file
                  dup ptmx fd, dup listener fd (CLOEXEC off)
                  execve(executable, argv + --adopt-handoff)
          worker (new image): adopt fds + child pid
                  replay the dump into a fresh model
                  resume the read loop at last_seq
      daemon reconnects, hello -> snapshot_format=<new>   match
        stale flag never set; nothing reaches the app
```

The session's process tree does not move: same worker pid, same agent child,
same PTY. Only the executable behind the pid changes.

```text
pty.Session today            pty.Session after
  creackpty.StartWithSize      creackpty.StartWithSize   (spawn path)
    s.cmd.Wait()          ->     s.waitChild()           (adopt path too)
                                   cmd.Wait()      when spawned
                                   os.FindProcess(pid).Wait()  when adopted
```

## Data model

The handoff file is attn's own state, so it is JSON, not a library format.
Nothing in it comes from libghostty-vt except the VT bytes, which are plain
escape sequences.

```go
type Handoff struct {
    SessionID   string
    ChildPID    int      // still our child across the exec
    PtmxFD      int      // dup'd, CLOEXEC cleared
    ListenerFD  int      // same, so no dial ever fails
    VTDumpPath  string   // written beside it, not inlined
    LastSeq     uint32   // the attach stream continues, not restarts
    Blocks      []AttachBlockData  // OSC 133; a VT replay rebuilds none
    Theme       TerminalTheme
    Exited      *ExitState         // nil while the child runs
    HandedOver  time.Time          // the blackout receipt in the log
}
```

Lost on purpose, and named in the log line so a reader can tell an upgrade
from a bug: kitty images and their placements.

## Boundaries

- `ghosttyvt` owns what a terminal can say about itself. `HandoffVT` is the
  only destructive method on it and says so.
- `ptyworker` owns the swap: quiesce, dump, dup, exec, adopt. It decides
  nothing about *when*.
- The daemon decides when, and names the binary. A worker never resolves its
  own replacement — after an install, `os.Executable()` can point at a path
  that was replaced underneath it, and the daemon already knows which binary
  it is running.
- The app learns nothing. A successful upgrade produces no wire traffic beyond
  the snapshot re-push that already happens on reconnect.

## Implementation steps

- [x] `ghosttyvt.HandoffVT` + `TotalRows`, with the measured row-deficit
      correction and the two-screen dump
- [x] Tests for it: round-trip fidelity per grid shape, the deficit as a
      pinned bug (a test that fails when the correction is removed), the
      alt-screen primary underneath, the documented scrolling-region limit
- [x] `pty.Manager.Handoff` / `Adopt`: a pollable master, a read loop that
      stops at a chunk boundary, and a session rebuilt around an inherited
      ptmx + child pid
- [x] `ptyworker` handoff: quiesce the read loop at a parse boundary, write
      the handoff, dup both fds, exec
- [x] `ptyworker` adopt: `--adopt-handoff`, replay, resume at `LastSeq`,
      rewrite the registry entry
- [x] `MethodUpgrade` on the worker RPC (additive, like `MethodSnapshot`: an
      older worker answers `bad_request` and the daemon falls back to the
      notice)
- [x] Daemon: on a `snapshot_format` mismatch, upgrade instead of flagging;
      flag only if the upgrade fails
- [x] The notice's copy now describes the fallback it became, not a one-off
- [x] Live verification across a real bump, and a changelog fragment
      (`real-app:scenario-terminal-build-upgrade`, profile `vtswap`: worker
      pid and child pid unchanged across the swap, the pre-swap marker still
      on the pane, the shell answering afterwards, no stale flag)

## Decisions

- **Re-exec, not a new process.** A fresh process would have to adopt the
  child by pid, and nothing on Unix lets a non-parent reap one: `pidfd` and
  `kqueue` report the exit, neither gives the status. Keeping the pid keeps
  the parent relationship, which keeps `Wait`. Spike 1.
- **Plain VT, not the binary snapshot.** The snapshot format is exactly what
  cannot cross a bump. VT is version-neutral by construction, and spike 3
  crossed the real boundary with it.
- **Measure the row deficit, do not predict it.** The cursor-based rule I
  tried first was wrong in three of fourteen grid shapes, and wrong in both
  directions. A replay into a scratch terminal costs a few milliseconds, once
  per upgrade.
- **Inherit the listener fd.** Rebinding leaves a 12ms hole where a daemon
  dial fails; inheriting leaves none. Three lines of difference.
- **The daemon names the binary.** See Boundaries.
- **The PTY master is pollable for every session, not only during an
  upgrade.** A blocking master cannot be quiesced at all, and the flag has to
  be set before the reader blocks. Spike 6.
- **Blocks travel as rows and are re-pinned.** `ghosttyvt.TrackPoint` pins a
  screen row the way `TrackCursor` pins the cursor; the old image's native
  refs die with it, and a VT replay rebuilds no OSC 133 state.
- **The adopted session mints a NEW kitty epoch.** Images do not cross, so a
  client holding pixels from before the upgrade must not draw them.
- **Accepting stops before the session is captured, not after.** Between the
  capture and the exec the worker no longer owns the session, so a dial
  landing there is answered "session not found" — which is exactly what the
  first real execve run produced. Closing the listener (after dup'ing its fd)
  parks new connections in the kernel backlog for the image that takes over,
  and a capture that fails hands the same socket back. The integration test's
  `Snapshot` immediately after the upgrade is the pin.
- **One upgrade per session, not one per hello.** A daemon start greets the
  same worker more than once (the recovery probe and the lifecycle watch),
  and each greeting reports a format nothing has recorded yet, so both
  started a swap. The loser was told an upgrade was already running, read
  that as a failure, and published the stale flag. The daemon now claims a
  session before starting and holds the claim across the upgrade's own
  re-handshake. Found live, not by a test.
- **`UpgradeWorker` ends with a re-handshake.** It is both the proof the new
  image is answering and what re-records the terminal build, so the stale flag
  clears through the same path that sets it rather than a second source of
  truth.

## Open questions

- **Merge target.** The swap only runs on a format mismatch, which cannot
  happen without a bump, so it is inert in a same-version world — that argues
  for main. It also rewires worker lifecycle, which argues for an `epic/*`
  branch. My call: two PRs to main (`ghosttyvt` first, then the swap), because
  the second is gated behind a condition no unbumped build can reach. Yours.
- **Same release as #989?** The changelog fragment from #989 says the swap
  must ship in the same release; the PR body called it the next change. It
  only has to be the same release if the notice's promise is to be true on
  first sight, which it is. Worth settling before the next release.

## Follow-ups

- **Kitty images across the swap.** Placements in the viewport could be
  re-transmitted as APC sequences (also plain VT); ones scrolled into history
  cannot be placed at all. Worth doing only if a real session loses something
  the user notices.
- **Upstream: the formatter drops trailing blank rows.** `trim: false`
  arguably should not. Worked around locally; not reported.
