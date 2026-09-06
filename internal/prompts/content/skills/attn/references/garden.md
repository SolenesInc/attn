# The garden: seeds, plots, and reporting

The garden is where work lives. Run `attn seed prime` for working rules.
Before writing a plot, read `attn seed guide` for body authoring, a complete
plot example and completion checks. Syntax lives in `attn seed --help`.

## Speaking the user's language

Garden words have Jira-style equivalents: seed = ticket, ready = todo,
plot = epic, harvested = done. Use the Garden word by default. When the user
uses one of those Jira words, mirror it for that concept for the rest of the
exchange. Do not correct them or switch the other concepts unless they do.

## Planting work

Track work in seeds, not in markdown TODO lists or your own todo tool. Plant a seed for any work that outlives this turn: a bug you found, a follow-up you are not doing now, a piece you split off. Search before you plant: `attn seed search <words>` reads every title, body and log in the garden, harvested and withered seeds included, so you find work that already exists instead of planting it twice. Plant work before you start it, so the claim and the log exist while you work. Under a plot, plant with `--part-of <plot>` so it stays with its plan. If you discover work while tending another seed, add `--discovered-from <seed>` so its origin is on record. Before your turn ends, plant what is still undone.

Every seed body, including a plot child, is a work prompt for someone without
this conversation. State its task and outcome, starting context, constraints
and completion check. A short body is enough when it supplies that direction.

## Planting a plot

Keep the shared plan in the parent. Each child names its own work and tells the
reader which parent section, sibling result or artifact it needs and why.
Read those references before starting; related bodies are not included
automatically in a delegation.

`attn seed plot -f <payload.json>` plants a plot and its children in one move.
The example in `attn seed guide` shows complete bodies and dependencies. Each
child's `blocks` array names siblings that must wait for it, by slug or full
title. Leave independent children parallel. Pass `-` to read from stdin.

## Speaking of seeds

Every verb prints a seed as `id  slug  title`. The id is for commands and for
messages to other agents. To the user, say the slug: `mermaid-rendered-grid`
(`s-7k3f9m`) on first mention, then the slug alone. A person should never have
to decode an id.

## Harvesting on a merge

When the only thing left on a seed is a pull request merging, say so once and
let attn close it:

    attn seed harvest <id> --when-merged [<pr-url>]
    attn seed harvest <id> --when-merged --clear

With no url it takes this session's single open pull request, and names them
when there is more than one. A seed you were growing goes dormant and lets go
of your claim; every surface then reads `harvests when owner/repo#n merges`.
attn harvests it as the member `attn`, with the reason `PR #n merged: <title>`,
when the merge lands. A pull request closed without merging clears the arming
and rings instead, leaving the seed where it was.

Reach for it only when the merge is genuinely the last step. Review notes to
answer, docs to write or evidence to attach are work, and work belongs to a
tender.

## Rings and watches

Lifecycle moves ring the sessions with a stake in the seed. Notes stay quiet
unless you add `--ring`. `attn seed watch <id>` gives this session a stake;
watching a plot covers every seed in it. `attn seed unwatch <id>` is the way
out.

A bell carries only the seed and what moved, so read it with `attn seed show`;
`show` or `notes` resets the bell for the next meaningful move.

## Artifacts

Bring a local file into durable seed ownership with an explicit Move or Copy.
Move is recommended; tracked Git files must use Copy. Detaching moves the owned
file back out and never overwrites:

    attn seed attach <id> --path <file> --move
    attn seed attach <id> --path <file> --copy
    attn seed detach <id> --path <filename> --to <destination>

Repository, Notebook and URL documents remain links where they already live:

    attn seed attach <id> --path <file.md> --repo <repository>
    attn seed attach <id> --notebook <document-id>
    attn seed attach <id> --url <url>
    attn seed detach <id> --path <file.md> --reference

The Garden labels old path associations as linked files. Bring one into seed
ownership explicitly with Move or Copy; a failed transfer leaves the link alone.

## Packets

A packet is a plot flagged as a template with declared variables.
Nothing sows one yet.
`attn seed show` says `packet yes`, and `attn seed ready` skips its subtree.
