# Writing

People and agents act on attn's docs, skills, product prompts and CLI help.
Write each sentence so the reader can act on it without first working out what
it means. These rules apply to all of that text.

## Name who acts

Make the subject of each sentence whoever really does the action: "you" in
instructions, or the person, agent or program that acts. Don't give an object
or an event a verb that only a person can do.

| Instead of | Write |
| --- | --- |
| A change keeps the files it touches current. | Whoever changes a feature updates its map file. |
| A pass audits one pinned revision of `main`. | In a pass, you pin one revision of `main` and drive every entrypoint on it. |
| Each blocked row names the seed that carries its cause. | For each blocked row, link the seed that explains why it was blocked. |

Verbs for what a program literally does are fine: "the parser rejects a bad
date", "`scripts/land.sh` rebases onto `origin/main`".

## Repeat the noun

Don't chain "it", "its" and "one" across clauses. Repeat the noun when a
pronoun could point at more than one thing, or when its noun is more than a
clause back.

| Instead of | Write |
| --- | --- |
| Its report has one row per entrypoint, `audit.py check` accepts it, and it is attached to your stake. | The report has one row per entrypoint, passes `audit.py check`, and is attached to your stake. |
| Put it on a seed, reusing one that already covers it. | Put the bug on a seed. If a seed for that bug already exists, add a note to it instead of planting a new one. |

## Say what to do

When the reader has to act, tell them what to do, in order. A list of end
states, such as "is attached" or "are removed", leaves the reader to work out
the steps. A list of conditions for being done is fine when the reader can
check each one.

| Instead of | Write |
| --- | --- |
| Every instance it started is cleaned and its checkouts removed. | Clean every instance you started and delete the worktrees you created. |
