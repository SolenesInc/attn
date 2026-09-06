# Delegated-Agent Guidance

Load this reference when you are a delegated leaf. Your initial task opens
with a line identifying you as a delegated attn session.

## You are a leaf, not a coordinator

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

Report an update when it helps the delegator or a future tender understand the
current state, steer the work, or continue it later. This includes meaningful
progress, material findings, changes in direction or scope, blockers, and
decisions needed. Combine closely related developments into one note, and avoid
command-by-command or test-by-test narration:

    attn seed note <seed-id> -m \
      "The parser is implemented and its focused tests pass. Error wording remains."

Add `--ring` only when the delegator needs to respond now, such as for a blocker
or decision:

    attn seed note <seed-id> --ring -m \
      "Blocked on the event contract: should this emit created or updated?"

Close it when its outcome and required verification are complete. Before your
final response, record the result on the seed. If the outcome, key evidence or
verification, artifact locations, and unresolved work fit in the 400-character
harvest reason, put them there:

    attn seed harvest <seed-id> -m "<what got done>"

Otherwise, write one result note with the necessary detail, then harvest with a
concise summary.

Put long findings in a durable artifact, attach or link it to the seed, and name
the artifact in the note or harvest reason. `attn seed guide` explains note
discipline, artifact handover, and when the evidence is strong enough to
harvest.

If the requested outcome cannot or should not be completed, record why:

    attn seed wither <seed-id> -m "The required API was removed; nobody should pick this up"

Then tell the user what happened in this session and any next step. Do not make
them inspect the seed to learn the outcome.

Writing a note does not stop or transfer your session. Continue unless the task
is blocked or complete. Untracked delegation, with no seed in the prompt, has
nowhere to report and needs none of this.

`attn ticket` retired: every write verb prints the garden command that replaced
it and exits nonzero. `show` and `list` read legacy records; `inbox` reads and
acknowledges unread activity on tickets that predate the garden. See
[garden.md](garden.md).
