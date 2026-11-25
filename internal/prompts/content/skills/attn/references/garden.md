# The garden: seeds, plots, and reporting

The garden is where work lives, inside your session's profile. Seeds belong to their planting profile for life. Lists, relationships, claims and delegations stay in that profile. Outside attn, seed commands require `--profile <name|id>` when several profiles exist.

Every command prints a seed as `id  slug  title`. The garden section of your launch prompt teaches ids and slugs, the user's words for garden things and when to plant. `attn seed prime` prints it again with the loop of commands. This reference holds the rules that span commands. Each section lists its commands; `attn seed <command> --help` gives a command's flags, its full behavior and examples. Before you write a plot, read `attn seed guide` for body authoring, a complete plot example and completion checks.

## Planting work

- `attn seed search <words> [--limit <n>] [--json]`: find seeds by words, closed ones included
- `attn seed plant "<title>" [-m <body>] [--part-of <plot>] [--discovered-from <seed>] [--bed <bed>] [--check "[C#=]<statement>"]... [--json]`: plant a seed and print its id, slug and title

## Planting a plot

Keep the shared plan in the parent, and give each child the work prompt your launch prompt describes. Children are parallel by default. To order them, name the siblings a child waits for in `after` in the plot payload, or run `attn seed link <a> blocks <b>`: a blocked seed stays out of `attn seed ready` until its blocker closes. The example in `attn seed guide` shows complete bodies and dependencies.

- `attn seed plot [-f <path>] [--json]`: plant a whole plot in one move from a JSON payload
- `attn seed link <a> <blocks|part-of|discovered-from> <b> [--json]`: relate two seeds
- `attn seed unlink <a> <blocks|part-of|discovered-from> <b> [--json]`: remove an edge between two seeds

## Finding and reading work

Every change to a seed adds an entry to the seed's log: notes, handoffs, moves, links, edits, checks, attachments and answers. `attn seed show <id>` prints a bookmark under the seed's header, `bookmark  attn seed show <id> --since <N>`, then the newest entries. `attn seed notes <id>` lists the whole log and ends with its own bookmark line. Run a bookmark later to get only the entries saved after N, on the seed and every seed below it, each labeled with its seed. That read ends with a new bookmark. Keep the bookmark from your last read and read since that bookmark, instead of reading the whole seed again. When a bookmark is older than the entries the event log keeps, the read says so and gives the full seed instead.

- `attn seed ready [--plot <plot> | --all] [--json]`: what you can tend right now
- `attn seed ls [--plot <plot> | --stale | --standby] [--window <d>] [--bed <bed|unassigned>] [--flat] [--json]`: the garden, newest first, with children nested under their plot
- `attn seed show <id> [--since <bookmark> | --section <heading|id>] [--json]`: one seed, news first
- `attn seed notes <id> [--since <bookmark>] [--kind <kind>] [--limit <n>] [--json]`: the whole log, newest first
- `attn seed prime`: the standing garden contract and this session's garden position
- `attn seed guide`: the craft of writing seeds

## Tending a seed

The moves are tend, park, harvest, wither and replant. A move needs no `--force` when the actor is one of these:

- the claim's tender
- a session above the tender in the delegation chain
- the profile's chief

A move also needs no `--force` after the tender's session ends, because a session's claim lapses when the session ends, unless the seed is on standby. The claim holds again if the session resumes. A crew member's claim lasts across the member's sessions.

Everyone else needs `--force`, and the log records that they used it. A call without a live session has no standing. A move records who acted and whose claim ended, and prints the audit note and the sessions attn rang. A delegate hears the move through the delegate's delegation watch.

Repeating a move to the same state succeeds without rewriting the seed or ringing anyone. A different reason goes on the seed's log as a quiet note. To move between harvested and withered, run `attn seed replant <id>` first.

- `attn seed tend <id> [-m "<why>"] [--for <name>] [--force] [--json]`: claim the seed and start growing it
- `attn seed note <id> -m "<what happened>" [--handoff] [--ring] [--json]`: append to the seed's log
- `attn seed park <id> [-m "<where you left it>"] [--force] [--json]`: pause the seed and let go of it
- `attn seed replant <id> [-m "<why>"] [--force] [--json]`: put the seed back in the pool
- `attn seed wither <id> [-m "<why>"] [--force] [--json]`: close the seed as abandoned
- `attn seed send-to-chief <id> [-m "<optional guidance>"] [--json]`: give the seed to the Chief to decide its next working context

## Closing a seed

Harvest a seed when the outcome and the verification its body asks for are complete. An ask about the seed keeps it open: an agent's harvest refuses while the user has not answered, and names each open ask. Read the ask with `attn ask show <id>`. If its answer still matters, end your turn, and harvest after you act on the answer, which comes to whoever tends the seed. If it no longer matters, withdraw the ask with `attn ask close <id>`, then harvest. The user's own harvest and any wither withdraw the seed's open asks, and the command names them.

- `attn seed harvest <id> [<pr-url>] [-m "<what got done>"] [--when-merged] [--clear] [--force] [--json]`: close the seed as done

## Closing a plot

Harvest and wither refuse on a plot while any direct child is planted, growing or parked, even with `--force` or `--when-merged`. The refusal names each open child. To close the plot, first close each open child or move its follow-ups out. To give up your claim on the plot without closing it, run `attn seed replant <plot>`. When the last open child closes, attn adds a ringing note to the plot and names the plot in the output. The plot keeps its state.

## Harvesting on a merge

When the only thing left on a seed is a pull request merging, say so once with `attn seed harvest <id> --when-merged -m "<outcome and verification>"` and let attn close it. With no pull request url, the command takes this session's single open pull request. A seed you were growing is parked, and attn releases your claim. Every surface then shows `harvests when owner/repo#n merges`. When the merge lands, attn harvests the seed as the member `attn`, with your reason. If a pull request closes without merging, attn clears the arming and rings instead, and the seed stays where it was. attn refuses to arm a seed with an open ask. If the seed has gained open children or an open ask since, attn clears the arming at the merge and explains why. `--when-merged --clear` takes the arming back.

Use `--when-merged` only when the merge is genuinely the last step. Review notes to answer, docs to write, and evidence to attach are work, and work belongs to a tender.

## Checks

A check is a numbered statement (C1) that a seed must prove. attn stores the check on the seed as data, with its latest report and verdict. Cite the check by id in the body. Define checks with `checks` in plot JSON, where you write each id, with `--check "<statement>"` on plant, or later with `attn seed check add`. `plant` and `add` give the check the next free id in the plot tree and print that id. A plot check marked shared applies to every seed below the plot that has no children, and each of those seeds needs its own report for it. `attn seed show` lists a seed's checks, including inherited ones, as open, reported, confirmed or rejected.

Whoever does the work reports each check, as Report each check as you prove it (`attn skill --reference delegated-agent`) describes. Whoever judges the work confirms or rejects. Checks never block a move. The harvest command lists the checks that are still open or rejected. Add `--ring` to a check command when the other side must act now.

- `attn seed check add <id> [<C#>] -m "<statement>" [--shared] [--ring] [--json]`: define a statement the seed must prove
- `attn seed check edit <id> <C#> -m "<statement>" [--ring] [--json]`: replace the check's statement
- `attn seed check remove <id> <C#> [-m "<why>"] [--ring] [--json]`: remove a check
- `attn seed check restore <id> <C#> [-m "<why>"] [--ring] [--json]`: bring a removed check back
- `attn seed check report <id> <C#> -m "<summary line>[\n\n<detail>]" [--evidence <file|artifact|url>]... [--commit <rev>] [--ring] [--json]`: record the proof for a check in one call
- `attn seed check confirm <id> <C#> [-m "<how you verified it>"] [--ring] [--json]`: confirm the check
- `attn seed check reject <id> <C#> -m "<what would prove it>" [--ring] [--json]`: reject the check
- `attn seed check uncheck <id> <C#> [-m "<why>"] [--ring] [--json]`: clear the verdict and keep the report
- `attn seed check reopen <id> <C#> [-m "<why>"] [--ring] [--json]`: clear the report and verdict

## Rings and watches

A plot's tender hears updates from every seed under the plot, including grandchildren, without a watch. When the claim ends, that coverage ends and attn withdraws unread updates unless a watch still covers them. Lifecycle moves ring current and covering tenders and watching sessions, and print who they rang. Harvesting or withering a blocker also rings the parties of each seed it was blocking, unless another open blocker still holds that seed. Notes stay quiet unless you add `--ring`. A successful action never rings the session that caused it. Automation acceptance is quiet, and its bell waits until the work is ready or the attempt needs attention.

A bell carries the seed, what moved, and the read that shows it: `attn seed show <id> --since <N>`, where N is the bookmark just before the first change the bell covers. Later changes join the unread bell, and the same read returns them too. Any `show` or `notes` of the seed resets the bell for the next meaningful move. You cannot recall updates already read or handed to an agent.

- `attn seed watch <id> [--json]`: watch this seed and its descendants
- `attn seed unwatch <id> [--json]`: remove this seed's subscription

## Standby

Standby closes your session for a wait that needs nobody in it: a time, a pull request, another seed or another agent. If you tend no seed for the work, plant one first. A wait on the user is different. Ask with `attn ask` and end your turn. Your session stays open, so the user can find you, talk to you and run commands in your terminal, and attn types the answer into this conversation. While you have an open ask, about this seed, another seed or none, attn refuses a standby that would close you, and names each ask. When the user tells you to close while an ask about the seed is open, run `attn seed standby <id> --stay -m "…"`, then `attn agent close --self`: the seed stays on standby, and the answer resumes you.

Only the seed's tender puts it on standby, from its own session, with a note that says what to do next. The seed stays claimed while the session is closed. `--in` and `--at` also set the seed's reminder, so the session resumes at that time if nothing else arrives first. A seed holds one reminder; `attn seed remind` sets one without closing the session.

Seed mail is a reminder, a message sent with `attn agent msg <seed-id>`, an answer to an ask about the seed, or an update from a PR watch on the seed. The setting `garden.tender_resume` chooses which mail resumes a session: `any` (the default), `watches` (PR watches and reminders) or `off`. attn resumes a tender at most 48 times per seed in 24 hours. When it cannot resume one, it notes the reason on the seed and notifies the user, and the mail stays unread.

The note is the first thing the resumed session reads, so write down anything the user approved up front, such as "merge once approved". The resumed session should act without waiting for the user: go on standby again, harvest, ask with `attn ask`, or report on the seed.

- `attn seed remind <id> (--in <duration> | --at <time>) -m "<message>" | remind <id> --clear [--json]`: set a reminder that posts a message to the seed's mailbox
- `attn seed standby <id> -m "<what to do next>" [--in <duration> | --at <time>] [--stay] | standby <id> --clear [--json]`: wait for seed mail with this session closed

## Bodies and authorization

To change a body, export it, edit the file, and submit it with `attn seed edit`. EDITING A BODY in `attn seed guide` gives the steps and explains how to roll back an edit.

- `attn seed export <id> [--out <path>] [--json]`: write the raw body to a file for editing
- `attn seed edit <id> -m - [--note "<what changed>"] [--json]`: save an edited body
- `attn seed rollback <id> [<version>] [--json]`: restore an earlier body
- `attn seed authorization <id> ["<words>" | --clear] [--json]`: set, clear or print the task's authorization

## Artifacts

To bring a local file into durable seed ownership, choose Move or Copy explicitly. Prefer Move. Tracked Git files must use Copy. Repository, Notebook and URL documents remain links where they already live.

When you name a seed's artifact to the next agent, say which copy is canonical. For a repository document, the Git file is canonical, so name its branch and introducing commit in the brief. For a Notebook document, the Notebook file is canonical. Notes on the seed record meaningful edits, renames and deletions, and tell you when to read the plan again.

The Garden labels old path associations as linked files. Bring a linked file into seed ownership explicitly with Move or Copy. A failed transfer leaves the link alone.

- `attn seed attach <id> [--path <file> | --notebook <id> | --url <url>] [--move | --copy] [--repo <repository>] [-m "<note>"] [--json]`: give the seed a file or a document to keep
- `attn seed detach <id> [--path <filename> | --notebook <id> | --url <url>] [--to <destination>] [--reference] [--repo <repository>] [-m "<note>"] [--json]`: move a managed artifact back out, or drop an association

## Beds and pulse

When `garden_v2_enabled` is on, a profile can hold curated beds that group root seeds and plots by project or initiative. Beds are user-curated: do not create, edit or remove beds unless the user asks. Root seeds auto-route into the bed bound to their repository (exact path match before bare repository name), or take an existing bed explicitly; child seeds inherit their root plot's bed.

A pulse is a seed's current one-line status (at most 160 characters), replaced in place without adding a log entry. Update it when starting a phase, reaching a milestone, or hitting a blocker. Closing or replanting a seed clears its pulse.

- `attn seed bed ls [--json]`: list the profile's curated beds, their repos and open seed counts
- `attn seed bed add "<name>" [-m "<one-line note>"] [--repo <path|name>]... [--json]`: create a curated bed
- `attn seed bed set <bed> [--name "<name>"] [-m "<one-line note>" | --clear-note] [--repo <path|name> | --clear-repos] [--json]`: update a bed's name, note or repos
- `attn seed bed rm <bed> [--json]`: remove a curated bed and leave its seeds unassigned
- `attn seed bed assign <id> [<bed> | --clear] [--json]`: assign a root seed or plot to an existing bed, or clear its bed
- `attn seed pulse <id> [-m "<one-line status>" | --clear] [--json]`: set or clear a seed's one-line status pulse

## Packets

A packet is a plot flagged as a template with declared variables. attn cannot yet create seeds from a packet. `attn seed show` says `packet yes`, and `attn seed ready` skips the packet's subtree.
