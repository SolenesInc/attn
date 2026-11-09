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
someone without this conversation. WRITING A BODY in `attn seed guide` says
what the prompt contains.

## Editing a body

To change a body, export it, edit the file, and submit it with
`attn seed edit`. EDITING A BODY in `attn seed guide` gives the steps and
explains how to roll back an edit.

## Planting a plot

Keep the shared plan in the parent. In each child, name the child's own work.
Also name each parent section, sibling result, or artifact the child needs, and
say why. The agent that tends the child reads those sources before it starts,
because a delegation does not include related bodies automatically.

`attn seed plot -f <payload.json>` plants a plot and its children in one move.
The example in `attn seed guide` shows complete bodies and dependencies. In a
child, `after` names the siblings the child waits for, by slug or full title.
Leave independent children parallel. `part_of` plants the plot inside another
plot, and `discovered_from` records the seed whose work turned up the plot.
Give each by id or slug. Pass `-` as the payload file to read from stdin.

## Checks

A check is a numbered statement (C1) that a seed must prove. attn stores the
check on the seed as data, with its latest report and verdict. Cite the check
by id in the body. Define checks with `checks` in plot JSON, with
`--check "<statement>"` on plant, or later with
`attn seed check add <id> -m "<statement>"`. Both `attn seed plant` and
`attn seed check add` give the check the next free id in the plot tree and
print that id. A plot check marked shared applies to every seed below the plot
that has no children, and each of those seeds needs its own report for it.
`attn seed show` lists a seed's checks, including inherited ones, as open,
reported, confirmed or rejected.

    attn seed check confirm <id> <C#> [-m "<how you verified it>"]
    attn seed check reject <id> <C#> -m "<what would prove it>"

Whoever does the work reports each check, as Report each check as you prove it (`attn skill --reference delegated-agent`) describes. Whoever judges the work confirms or rejects. Checks never block a move. The harvest command lists the checks that are still open or rejected. Add `--ring` to a check command when the other side must act now.

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

The moves are tend, park, harvest, wither and replant. A move needs no `--force` when the actor is one of these:

- the claim's tender
- a session above the tender in the delegation chain
- the profile's chief

A move also needs no `--force` after the tender's session ends, because a session's claim lapses when the session ends, unless the seed is on standby. The claim holds again if the session resumes. A crew member's claim lasts across the member's sessions.

Everyone else needs `--force`, and the log records that they used it. A call without a live session has no standing. A move records who acted and whose claim ended, and prints the audit note and the sessions attn rang. A delegate hears the move through the delegate's delegation watch.

## Closing a plot

Harvest and wither refuse while any direct child is planted, growing or parked,
even with `--force` or `--when-merged`. The refusal names each open child. To
close the plot, first close each open child or move its follow-ups out. To give
up your claim on the plot without closing it, run `attn seed replant <plot>`.
When the last open child closes, attn adds a ringing note to the plot and names
the plot in the output. The plot keeps its state.

## Harvesting on a merge

When the only thing left on a seed is a pull request merging, say so once and
let attn close it:

    attn seed harvest <id> --when-merged [<pr-url>] -m "<outcome and verification>"
    attn seed harvest <id> --when-merged --clear

With no url, the command takes this session's single open pull request, and
names the pull requests when there is more than one. A seed you were growing
is parked, and attn releases your claim. Every surface then shows
`harvests when owner/repo#n merges`. The command merges nothing. When the merge
lands, attn harvests the seed as the member `attn`, with your reason. Without
`-m`, attn uses `PR #n merged: <title>`. If a pull request closes without
merging, attn clears the arming and rings instead, and the seed stays where it
was. If the seed has gained open children, attn clears the arming at the merge and
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
rang. Harvesting or withering a blocker also rings the parties of each seed it was blocking, unless another open blocker still holds that seed. Notes stay quiet unless you add `--ring`. A successful
action never rings the session that caused it. Automation acceptance is quiet,
and its bell waits until the work is ready or the attempt needs attention.
`attn seed watch <id>` subscribes this session to the seed and its descendants,
including children planted later. A crew member's watch belongs to the member
and lasts across the member's sessions. A new delegation starts the same watch
on the seed it binds.

`attn seed unwatch <id>` removes that subscription. The command also drops a queued update
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

## Reminders

A reminder is a time and a message on a seed. At that time attn posts the
message to the seed's mailbox, which delivers it to whoever tends the seed
then. The tender reads it in `attn agent inbox`, where it arrives as a notice
and not as a seed update. When nobody tends the seed, the message waits for the
next tender.

    attn seed remind s-7k3f9m --in 20m -m "Check the rollout of the new build"
    attn seed remind s-7k3f9m --at 2026-11-05T09:00:00+01:00 -m "Ask for the review"
    attn seed remind s-7k3f9m --clear

`--in` counts from now, and `--at` takes an RFC3339 time. attn checks once a
minute, so a message can arrive up to a minute late.

A seed holds one reminder, and a new one replaces it. The reminder ends when
attn posts the message, when `--clear` removes it, and when the seed is
harvested or withered. Until then, `attn seed show` and `attn seed ls` print
it. Setting or clearing a reminder adds a quiet note to the seed's log, and
posting the message adds no entry.

Setting or clearing a reminder needs the same standing as a move: the tender, a
session above the tender in the delegation chain, the chief, or the user. When
nobody holds the claim, anyone can. There is no `--force`. You can offer a
reminder to the user.

## Standby

A seed on standby waits for mail without a session held open for it. The seed's
tender puts it on standby with a note that says what to do next. When mail
reaches the seed, attn resumes the tender's session, or starts one for a crew member, and hands over the note.

    attn seed standby s-7k3f9m -m "Review is pending on #123. When it is approved, merge (the user approved this up front), then follow the deploy."
    attn seed standby s-7k3f9m -m "Check the rollout of the new build" --close
    attn seed standby s-7k3f9m --clear

Standby keeps your claim, so only the tender of a growing seed can put it on
standby, from its own session. Clearing it needs the same standing as a move.
Seed mail is an item addressed to the seed's mailbox: a reminder, a message sent
with `attn agent msg <seed-id>`, the answer to an ask about the seed, and an
update from a PR watch that addresses the seed.

When seed mail arrives for a seed on standby and the tender's session is closed,
attn resumes that session as itself, or starts a session for the crew member that tends the
seed. A session the user closed resumes the same way. A closed session whose seed
is not on standby stays closed, and its mail waits for the next tender.

The resumed session reads the mail and your standby note. The user may not be
watching, so it ends its turn one of four ways: it puts the seed on standby
again, harvests it, asks the user with `attn ask`, or reports.

Standby ends once the tender has read all of the seed's mail with
`attn agent inbox`, whether the session was open or resumed. Input that attn types
into the session, such as the user's answer to an ask, counts as read. Standby
also ends when the seed is parked, harvested or withered, and when the seed gets
a new tender. If the session closes before you read the mail, the seed stays on
standby and the mail resumes it again at once. A new standby replaces the note.
Write down anything the user approved up front, such as "merge once approved".
The session that resumes may have compacted its conversation, and the note is what
it reads first.

`attn seed ls --standby` lists the seeds on standby. Each row names the pull
requests the seed's mailbox watches with their readiness, the seed's reminder,
and why attn cannot resume the tender when it is blocked:

    s-7k3f9m  origin-check  growing  …  Enforce delivery origin  [standby: owner/repo#123 ready, reminder 2026-11-05 14:20]
    s-2b8n4q  zip-retry     growing  …  Retry zip unpack         [standby: owner/repo#118 open · wake blocked: worktree removed]

`--close` closes your session in the same move, as `attn agent close --self`
does. attn refuses it while the seed has unread mail and names
`attn agent inbox`. Read the mail, then run the command again.

`--close --compact` compacts your conversation before the close, as
`attn agent close --self --compact` does, so the next run resumes a
shorter conversation. Closing a session (`attn skill --reference converse-and-observe`)
says how it works and which sessions can compact. Seed mail you read before the
compact ends the standby, and you stay open. Seed mail you leave unread does not
cancel the close: the session compacts and closes, and the unread mail resumes it
again. Seed mail that arrives during the compact waits until the session has
closed, then resumes it as above. When attn refuses `--compact`, it leaves standby
unset, so run the command again without it.

attn delivers a resumed Claude session's mail through the Claude mod and never
types it.

The profile setting `garden.tender_resume` decides which mail resumes a closed
session. `any` (the default) resumes for all seed mail. `watches` resumes for PR
watch updates and reminders only. `off` resumes no session, and the mail waits in
the inbox. A crew member that tends the seed gets a session under every value.
`attn settings set garden.tender_resume watches` changes it, and the user can
change it in Settings.

attn allows 48 tender resumes per seed in 24 hours. When it cannot resume the tender, it
adds a note to the seed that names the reason and the item, and it sends the
user a critical notification that opens the seed. The reasons are a removed
worktree, a missing conversation, a Claude session whose mod does not poll
after the resume, and the limit of 48. The mail stays unread. You can offer
standby to the user when a seed waits on someone else.

## Artifacts

To bring a local file into durable seed ownership, choose Move or Copy
explicitly. Prefer Move. Tracked Git files must use Copy. Attaching a
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

When you name a seed's artifact to the next agent, say which copy is canonical. For a repository document, the Git file is canonical, so name its branch and introducing commit in the brief. For a Notebook document, the Notebook file is canonical. Notes on the seed record meaningful edits, renames and deletions, and tell you when to read the plan again.

The Garden labels old path associations as linked files. Bring a linked file into seed
ownership explicitly with Move or Copy. A failed transfer leaves the link alone.

## Packets

A packet is a plot flagged as a template with declared variables.
attn cannot yet create seeds from a packet.
`attn seed show` says `packet yes`, and `attn seed ready` skips the packet's subtree.
