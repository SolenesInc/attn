---
name: verify-perf
description: Use before landing or opening a pull request whose changes add a dependency or package-level work to attn's binaries, or touch the CLI or hook start, garden reads and pushes, event projections, PTY output and streaming, terminal painting, or what the daemon or app does while idle.
---

# Verify performance

Attn must stay fast and quiet while it runs all day, and a land measures very
little of that. Read the change for the costs below. Report findings. Do not
edit.

## Read

1. List the changes: `git diff --stat "$(git merge-base HEAD origin/main)"`.
2. For each changed function on a path this skill covers, read the whole
   function and find how often it runs: per process start, per paint, per
   output chunk, per event, per seed or per session, or on a timer.
3. Multiply how often it runs by what it does. A small cost on a hot path is
   the finding. A large cost on a path that runs once is usually not.

## Judge

| Do not | What it cost | What else guards it |
| --- | --- | --- |
| Do work in package `init` or package-level `var`s, or link a package whose `init` does | Every CLI command and agent hook paid for it: 39,179 init allocations against 10,373 today | `TestEveryCommandStartsWithoutPayingForInitItNeverUses`, `lint-go` |
| Allocate an object per cell or per row while painting | With the scan below, about 210-250 MB of held WebContent memory at 4 panes | This review |
| Scan every row or every seed on each paint | As above | This review |
| Send a whole list when one item changed | 3.92 MB per garden push against 11 KB, and 95.7 ms from message to frame against 8.7 ms | `TestGardenPushSendsOnlyWhatChanged` |
| Reread every session's or party's state on each garden read or change | Daemon CPU per garden change of 65 ms against 31 ms | This review |
| Send one message per output chunk instead of one per frame | About 1,450 WebSocket messages a second per terminal, against about 50 | `batches_keep_every_byte_and_go_out_at_most_once_per_tick` in `pty-host/src/output_batch.rs` |
| Stream output to a pane nothing draws | 50.4% of a core for hidden panes, against 7.4% | `app/src/App.drawnStreaming.test.tsx` |
| Add a timer, poll or repaint that runs while nothing happens | CPU burned all day. `AGENTS.md` calls this a defect | This review |
| Add a cache to cover a slow path | A second copy of state to keep correct, with the slow path still there | This review |

A guard in the last column covers one path, not the rule. Apply the rule to
the changed code anyway.

When the cost is unclear, measure it. `docs/internal/perf-testing.md` lists the
tools. Compare CPU, not wall time, when other agents load the machine.

## Report

For each finding, give:

- the `file:line`, the row of the table it breaks, and how often the code runs
- the cost, labelled measured or inferred
- the change: what to do instead, and why behaviour stays the same

If no change has a finding, say so in one line.
