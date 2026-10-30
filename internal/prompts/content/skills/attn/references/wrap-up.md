# Wrap up

The user asks you to wrap up, to close yourself, or whether they can close you
when they believe your work is done, or don't know what is left. Find what is
left, settle what needs no decision, raise what does, and close yourself when
nothing remains. Delegates still running are no reason to stay. They keep
working after you close, and their seeds keep their reports.

Check, don't recall. Before you decide nothing is left, read the seeds you
tend (`attn presence --seed-ids` lists them), the sessions you dispatched
(`attn session list --tree`), and the files you wrote outside a seed folder.

## Hand over

Give each live delegate the context it lacks and will need after you close.
Write it on its seed, and add `attn agent msg <seed-id> "…"` when it needs it
now.

## Clean up without asking

- Attach the files a reader needs to the seed they support. Scratch
  directories do not outlive you.
- Bring seed bodies and handoff notes up to date with what you learned.
- Harvest seeds whose outcome and verification are complete.
- Close your asks that no longer need an answer.

## Raise what needs the user, and stay

Put these in one `attn ask`, and do not close while it is unanswered:

- decisions still open
- decisions agreed but not yet acted on or dispatched
- work for another agent, including a review or merge a delegate waits on
  you for
- follow-ups you found. Raise them instead of planting them, so the user
  sees them.

When the answer arrives, act on it, then close.

## Close yourself

When nothing is left to raise:

    attn agent close --self -m "<what you settled>"

The close is immediate, so write everything first. The reason lands as a note
on each seed you still tend, and the session stays in the ledger.
