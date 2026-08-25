# Plan: harden the terminal lifecycle

## Goal

Keep a live terminal usable when a Ghostty history page is malformed, and stop
doing terminal work that carries no new information. The user-visible contract
does not change: a pane still restores before live output resumes, a resize
still reaches the PTY and every attached client, and grid tiles still reflect
terminal output and state promptly.

The work is local containment and lifecycle cleanup. Reporting the Ghostty
`RefCountedSet` defect upstream is deliberately separate and is not part of
this plan.

## What the investigation found

The recovered-pane diagnostic contains a valid snapshot prefix followed by a
malformed history page. Ghostty declares 107 style entries for that page but encodes
39. Its `RefCountedSet.addWithIdContext` path has overcounted `living`, so the
decoder correctly rejects the inconsistent page. attn currently turns that
late history-tail error into a model-fault recovery even though it has already
adopted a usable screen and may have restored earlier history pages.

Three independent lifecycle paths also do unnecessary work:

- the worker's five-second health poll serializes the whole terminal snapshot;
- attach always serializes a snapshot, including `fresh_spawn`, which discards
  it;
- resize always enters Ghostty, performs the ioctl, and publishes a resize even
  when grid and pixel geometry are unchanged.

The grid has a fourth idle cost: while it is visible, `TerminalGrid` schedules
another animation frame unconditionally and calls `model.update()` for every
visible tile on every display frame. Waiting and scheduled tiles also pulse
forever. A quiet grid therefore never becomes quiet.

## Boundaries

- `internal/pty` owns subscription, terminal snapshots, and the authoritative
  applied geometry. It decides whether a resize changed anything.
- `internal/ptybackend` and `internal/ptyworker` carry those choices across the
  process boundary. New fields remain additive so a daemon and an already-live
  worker can straddle an update safely.
- `internal/daemon` publishes resize facts only for applied changes and asks
  for a snapshot only when the attach policy can consume one.
- `GhosttyTerminal` owns app-side snapshot adoption. A valid adopted prefix is
  kept if only the lazy history tail fails.
- `TerminalGrid` owns when grid models are polled and drawn. Input, geometry,
  layout, state, and finite transitions wake it; quiet state does not.

## Implementation

### 1. Keep the valid snapshot prefix

Catch a failure from lazy history iteration at the history-drain boundary. Log
one bounded `snapshot_history_decode_rejected` diagnostic with the declared and
restored row counts, close the decoder, flush the successfully restored prefix,
and continue accepting live output. Do not send this case through generic
model-fault recovery.

Pin the behavior with a component test whose snapshot adopts successfully,
restores one history batch, then fails on the next page. It must keep one model,
emit one diagnostic, and accept subsequent output.

### 2. Make metadata and subscription cheap

Split three operations that currently share `Attach`:

- `SessionInfo` reads only session metadata and the current stream sequence;
- `Subscribe` registers a stream consumer without serializing terminal state;
- `Attach` registers first, then captures a full replay snapshot for policies
  that need one.

Worker health, startup metadata, debug capture, and activity probes use the
first two paths. Add an `omit_replay` attach option so `fresh_spawn` can
subscribe without creating bytes it throws away. Omission defaults to the old
full-snapshot behavior, so old callers and mixed daemon/worker versions stay
safe.

Count serialization in tests: health, metadata reads, and subscription must
perform zero serializations; a replaying attach must perform exactly one.

### 3. Drop authoritative no-op resizes

Track the last applied cell and total pixel geometry beside rows and columns.
Have `internal/pty` return whether a resize changed that geometry. A pure no-op
returns before Ghostty, image-placement fan-out, `TIOCSWINSZ`, and the daemon's
`session.pty_resized` fact. Same-grid calls with changed explicit pixel geometry
remain real changes.

Carry the result through the embedded and worker backends. A missing `changed`
field from an older worker means `true`, preserving the old safe behavior during
an in-place upgrade. Preserve pixel geometry in worker handoff state.

### 4. Let a quiet grid stop

Replace the permanent animation loop with a coalescing wake-up. Mark models
dirty when bytes or geometry arrive, and mark presentation dirty when tile
membership, layout, visibility, or state changes. Schedule another frame only
while a finite zoom, reflow, or attention transition is still moving.

Waiting, scheduled, and settled attention indicators become static once their
transition finishes; they must not depend on wall-clock pulses. Tests drive the
animation-frame queue directly and prove that it drains at rest, wakes once for
new output, and drains again.

## Verification

- Focused Go tests for `internal/pty`, `internal/ptybackend`,
  `internal/ptyworker`, and daemon resize publication.
- Focused frontend tests for snapshot containment, grid scheduling, and static
  tile state rendering.
- Full Go, frontend, and harness test gates, plus a Linux build of daemon-side
  code.
- A packaged isolated profile, bundled preflight, and live scenarios covering
  restore/continued input, `fresh_spawn`, resize/attach behavior, and a quiet
  visible grid. Compare the idle-grid receipt before and after this change.
