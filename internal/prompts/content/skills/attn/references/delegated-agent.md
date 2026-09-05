# Delegated-Agent Guidance

Load this reference when you are a delegated leaf — your initial task opens
with a line identifying you as a delegated attn session.

## You Are A Leaf, Not A Coordinator

Do the assigned work in this session. A subagent is always a native runtime
subagent, including when the user says to delegate or dispatch subagents. An
explicit request from the user steering this session selects attn delegation;
otherwise, use native subagents.

An attn delegation creates a visible agent session the user can inspect,
converse with, and steer directly. Native subagents report to you.

## Start from your seed

If your launch prompt names a seed, you are its tender. Before working,
read its current body and log with `attn seed show <seed-id>`, then follow the
references it names for the assignment. Parent and sibling bodies require
separate reads; you do not inherit the delegator's conversation.

## Report on your seed

Write to the log for the session that delegated you and anyone watching when you:

- reach a meaningful milestone
- need input or are blocked
- finish the requested work

    attn seed note <seed-id> -m \
      "Implemented the parser and tests pass. Next: review the error wording."

    attn seed note <seed-id> -m \
      "Core implementation is ready locally; which event contract should be used?"

Close it when its outcome and required verification are complete:

    attn seed harvest <seed-id> -m "<what got done>"
    attn seed wither <seed-id> -m "The required API was removed; nobody should pick this up"

Harvest timing, note discipline, and artifact handover are craft:
`attn seed guide` prints it — when the evidence is strong enough to harvest,
what a concrete note carries, and how to associate a durable document with
your seed. Run it when one of those calls is yours to make.

Noting does not stop or transfer your session. Continue working unless the task
is blocked or complete. Untracked delegation — no seed in your prompt — has
nowhere to report and needs none of this.

`attn ticket` retired: every write verb prints the garden command that replaced
it and exits nonzero. `show` and `list` read legacy records; `inbox` reads and
acknowledges unread activity on tickets that predate the garden. See
[garden.md](garden.md).
