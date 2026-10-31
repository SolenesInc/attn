# The garden: seeds, plots, and reporting

The garden is where work lives, inside your session's profile. Seeds belong to their planting profile for life. Lists, relationships, claims and delegations stay in that profile. Outside attn, seed verbs require `--profile <name|id>` when several profiles exist. Run `attn seed prime` for working rules.
Before you write a plot, read `attn seed guide` for body authoring, a complete
plot example and completion checks. Find syntax in `attn seed <verb> --help`. `attn seed --help` lists all commands.

## Speaking the user's language

Garden words have Jira-style equivalents: seed = ticket, ready = todo,
plot = epic, harvested = done. Use the Garden word by default. When the user
uses one of those Jira words, mirror it for that concept for the rest of the
exchange. Do not correct them or switch the other concepts unless they do.

## Planting work

Track work that outlives this turn in seeds, not in markdown TODO lists or your harness's todo tool, because those end with the session. Examples are a bug you found, a follow-up you are not doing now, and a piece you split off. Search before you plant. `attn seed search <words>` ranks stemmed word-prefix matches across titles, bodies and logs, including identifier parts and closed seeds, so you find work that already exists instead of planting it twice. Plant work before you start it, so the claim and the log exist while you work. Under a plot, plant with `--part-of <plot>` so the new seed stays with the plot's plan. If you discover work while tending another seed, add `--discovered-from <seed>` so the origin is on record. Before your turn ends, plant what is still undone.

Every seed body, including the body of a plot child, is a work prompt for
someone without this conversation. In the body, state the task and outcome,
the starting context, the constraints and the completion check. A short body
is enough when it gives that direction.

## Editing a body

Export the body, edit the file with any tool, then submit it:

    attn seed export s-7k3f9m
    # edit s-7k3f9m.md; keep its first line
    attn seed edit s-7k3f9m -m - --note "what changed" < s-7k3f9m.md

The stamp names the seed and its body hash. If another edit changed the body,
export again and reapply your change. attn refuses an empty body. Each edit saves
an undoable body version and one log entry. `--note` adds your explanation to
the log entry. Read the entries with `attn seed notes s-7k3f9m`. Restore the
previous version with `attn seed rollback s-7k3f9m`, or a named version with
`attn seed rollback s-7k3f9m 2`. A rollback saves a new version too.

## Planting a plot

Keep the shared plan in the parent. In each child, name the child's own work
and tell the reader which parent section, sibling result or artifact the child
needs, and why. Read those references before you start. A delegation does not
include related bodies automatically.

`attn seed plot -f <payload.json>` plants a plot and its children in one move.
The example in `attn seed guide` shows complete bodies and dependencies. In a
child, `after` names the siblings the child waits for, by slug or full title.
Leave independent children parallel. `part_of` plants the plot inside another
plot, and `discovered_from` records the seed whose work turned up the plot.
Give each by id or slug. Pass `-` to read from stdin.

## Checks

A check is a numbered statement (C1) that a seed must prove. attn keeps the
check on the seed as data, with its latest report and verdict. The body cites
the check by id. Define checks with `checks` in plot JSON, with
`--check "<statement>"` on plant, or later with
`attn seed check add <id> -m "<statement>"`. Plant and add number each
check with the next free id in the plot tree and print the id. Every seed below
the plot that has no children must prove a plot check marked shared, each with
its own report. `attn seed show` lists a seed's checks, including inherited
ones, as open, reported, confirmed or rejected.

    attn seed check report <id> <C#> -m "<summary line>" --evidence <file|artifact|url> --commit <rev>
    attn seed check confirm <id> <C#> [-m "<how you verified it>"]
    attn seed check reject <id> <C#> -m "<what would prove it>"

Whoever does the work reports each check in one call. The first line of `-m` is
the summary line, at most 400 characters. The lines after it are detail, which
attn keeps in the report's check note. attn copies a local `--evidence` file
onto the seed and replaces a file of the same name. A refused report lists
every problem at once. A new report replaces the last report and clears the
verdict. Whoever judges the work confirms or rejects. Checks never block a
move. Harvest lists the checks still open or rejected. Add `--ring` when the
other side must act now.

## Speaking of seeds

Every verb prints a seed as `id  slug  title`. `attn seed`, `attn delegate
--seed` and `attn ask --seed` take either the id or the slug. `attn open` and
`attn agent msg` take the id. Give other agents the id, because attn refuses a
slug that two seeds share and the refusal lists each match. To the user, say the
slug: `mermaid-rendered-grid` (`s-7k3f9m`) on the first mention, then the slug
alone. Never make a person decode an id.

## Reading what changed

Every change to a seed adds an entry to the seed's log: notes, handoffs, moves,
links, edits, checks, attachments and answers. `attn seed show <id>` prints a
bookmark under the seed's header, `bookmark  attn seed show <id> --since <N>`,
then the newest entries. `attn seed notes <id>` lists the whole log and ends
with its own bookmark line. `--kind <kind>` keeps one kind, such as `handoff`
or `answer`. Run a bookmark later to get only the entries saved after N, on the
seed and every seed below it, each labeled with its seed. That read ends with a
new bookmark. Keep the bookmark from your last read and read since that
bookmark, instead of reading the whole seed again. When a bookmark is older than
the entries the event log keeps, the read says so and gives the full seed
instead.

On a plot, `show` lists the open children as a board and folds the closed ones
into one line. `attn seed ls --plot <plot>` lists every child, closed ones
included.

## Moving a seed another agent tends

Tend, park, harvest, wither and replant need no `--force` when the actor is the
claim's tender, a session above the tender in the delegation chain, or the
profile's chief. The move records who acted and whose claim ended, and prints
the audit note and who the move rang. A delegate hears the move through the
delegate's delegation watch. Everyone else needs `--force`, which the log also
records. A call without a live session has no standing. A crew member's claim
lasts across the member's sessions. A session's claim lapses when the session
ends, and a move then needs no override. The claim holds again if the session
resumes.

## Closing a plot

Harvest and wither refuse while any direct child is planted, growing or dormant,
even with `--force` or `--when-merged`. The refusal names each open child.
If your part is done, replant hands the plot back. Otherwise close the plot's
children or move follow-ups out first. Closing the last open child leaves a ringing note
on the plot and names it in the output. The plot keeps its state.

## Harvesting on a merge

When the only thing left on a seed is a pull request merging, say so once and
let attn close it:

    attn seed harvest <id> --when-merged [<pr-url>] -m "<outcome and verification>"
    attn seed harvest <id> --when-merged --clear

With no url, the command takes this session's single open pull request, and
names the pull requests when there is more than one. A seed you were growing
goes dormant and releases your claim. Every surface then reads
`harvests when owner/repo#n merges`. The command merges nothing. When the merge
lands, attn harvests the seed as the member `attn`, with your reason. Without
`-m`, attn uses `PR #n merged: <title>`. If a pull request closes without
merging, attn clears the arming and rings instead, and the seed stays where it
was. If the seed has gained open children, a merge also clears the arming and
explains why.

Use `--when-merged` only when the merge is genuinely the last step. Review notes to
answer, docs to write, and evidence to attach are work, and work belongs to a
tender.

## Rings and watches

Repeating a move to the same state succeeds without rewriting the seed or
ringing anyone. A different reason goes on the seed's log as a quiet note. To move between
harvested and withered, run `attn seed replant <id>` first.

A plot's tender hears updates from every seed under the plot, including
grandchildren, without a watch. When the claim ends, that coverage ends and
attn withdraws unread updates unless a watch still covers them. Lifecycle moves
ring current and covering tenders and watching sessions, and print who they
rang. Harvesting or withering a blocker also rings the parties of each seed
that it sets free. Notes stay quiet unless you add `--ring`. A successful
action never rings the session that caused it. Automation acceptance is quiet,
and its bell waits until the work is ready or the attempt needs attention.
`attn seed watch <id>` subscribes this session to the seed and its descendants,
including children planted later. A crew member's watch belongs to the member
and lasts across the member's sessions. A new delegation starts the same watch
on the seed it binds.

`attn seed unwatch <id>` removes that subscription. It drops a queued update
only when the session is not a current tender, not a covering tender, and not
covered by another watch. Separate child watches remain. An ancestor watch can
still cover the seed. `attn seed show <id>` and `unwatch` name those
subscriptions and the commands that remove them. `watch` restores a
subscription, and a new delegation starts it again. Restarting or resuming the
same delegation does not restore a removed watch.

A bell carries the seed, what moved, and the read that shows it:
`attn seed show <id> --since <N>`, where N is the bookmark just before the
first change the bell covers. Later changes join the unread bell, and the same
read returns them too. Any `show` or `notes` of the seed resets the bell for
the next meaningful move. You cannot recall updates already read or handed to
an agent.

## Artifacts

To bring a local file into durable seed ownership, choose Move or Copy
explicitly. Move is recommended. Tracked Git files must use Copy. Attaching a
file replaces any file of the same name on the seed, and `attn seed show` marks
check evidence replaced after its report. Detaching moves the owned file back
out and never overwrites:

    attn seed attach <id> --path <file> --move
    attn seed attach <id> --path <file> --copy
    attn seed detach <id> --path <filename> --to <destination>

Repository, Notebook and URL documents remain links where they already live:

    attn seed attach <id> --path <file.md> --repo <repository>
    attn seed attach <id> --notebook <document-id>
    attn seed attach <id> --url <url>
    attn seed detach <id> --path <file.md> --reference

The Garden labels old path associations as linked files. Bring a linked file into seed
ownership explicitly with Move or Copy. A failed transfer leaves the link alone.

## Packets

A packet is a plot flagged as a template with declared variables.
Nothing sows a packet yet.
`attn seed show` says `packet yes`, and `attn seed ready` skips the packet's subtree.
