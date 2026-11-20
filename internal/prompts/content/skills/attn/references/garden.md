# The garden: seeds, plots, and reporting

The garden is where work lives, inside your session's profile. Seeds belong to their planting profile for life. Lists, relationships, claims and delegations stay in that profile. Outside attn, seed commands require `--profile <name|id>` when several profiles exist. Run `attn seed prime` for working rules.

Before you write a plot, read `attn seed guide` for body authoring, a complete plot example and completion checks. `attn seed --help` lists every command, and `attn seed <command> --help` gives one command's syntax and flags.

## Speaking the user's language

Garden words have Jira-style equivalents: seed = ticket, ready = todo, plot = epic, harvested = done. Use the Garden word by default. When the user uses one of those Jira words, mirror it for that concept for the rest of the exchange. Do not correct them or switch the other concepts unless they do.

## Speaking of seeds

Every command prints a seed as `id  slug  title`. `attn seed`, `attn delegate --seed` and `attn ask --seed` take either the id or the slug. `attn open` and `attn agent msg` take the id. Give other agents the id, because attn refuses a slug that two seeds share and the refusal lists each match. To the user, say the slug: `mermaid-rendered-grid` (`s-7k3f9m`) on the first mention, then the slug alone. Never make a person decode an id.

## Planting work

Track work that outlives this turn in seeds, not in markdown TODO lists or your harness's todo tool, because those end with the session. Examples are a bug you found, a follow-up you are not doing now, and a piece you split off. Search before you plant, so you find work that already exists instead of planting it twice. Plant work before you start it, so the claim and the log exist while you work. Under a plot, plant with `--part-of <plot>` so the new seed stays with the plot's plan. If you discover work while tending another seed, add `--discovered-from <seed>` so the origin is on record. Before your turn ends, plant what is still undone.

Every seed body, including the body of a plot child, is a work prompt for someone without this conversation. WRITING A BODY in `attn seed guide` says what the prompt contains.

    attn seed search <words> [--limit <n>] [--json]

Finds seeds by words across your profile's garden, harvested and withered included: titles, bodies and log entries. Words match as stemmed prefixes; identifier parts match too. Seeds carrying some words also appear. Ranks by title coverage, word coverage, relevance and recency, with a boost for open seeds. Prints status, last active and an original matching line. Search before you plant, so a duplicate is found instead of created.

    attn seed search retry uploader

    attn seed plant "<title>" [-m <body>] [--part-of <plot>] [--discovered-from <seed>] [--check "[C#=]<statement>"]... [--json]

Plants a seed and prints its id, slug and title. `-m` takes markdown; if the seed gains children, that body is the plot's plan. `--part-of` plants it under a plot. `--check` defines what the seed must prove; attn numbers each check unless C#= names the id, and prints each id. Lists up to 3 similar open seeds in your profile, leaving out that plot. Planting always proceeds. If one is the same work, wither the new seed.

    attn seed plant "Retry the uploader on 503" --part-of s-p4k2m1 --check "A 503 is retried three times"

## Planting a plot

Keep the shared plan in the parent. In each child, name the child's own work. Also name each parent section, sibling result, or artifact the child needs, and say why. The agent that tends the child reads those sources before it starts, because a delegation does not include related bodies automatically. The example in `attn seed guide` shows complete bodies and dependencies.

    attn seed plot [-f <path>] [--json]

Plants a whole plot in one move from a JSON payload, read from the `-f` file, or from stdin when the file is - or omitted: {"title": …, "body": …, "checks": [{"id": "C5", "text": …, "shared": true}], "part_of": "<plot>", "discovered_from": "<seed>", "children": [{"title": …, "body": …, "checks": […], "after": ["<sibling step slug>"]}]}.

Children are parallel by default; `after` names the siblings a child waits for, by step slug or full title. `part_of` plants the plot inside another plot, and `discovered_from` records the seed whose work turned up the plot; give each by id or slug. A shared check is proven by every child. The plot, its children and their edges commit together. Prints the plot's id and each child's id and step slug, then up to 3 similar open seeds across all planted titles, each naming the new seed it resembles. Planting always proceeds; wither a new seed if a suggestion is the same work.

    attn seed link <a> <blocks|part-of|discovered-from> <b> [--json]

Relates two seeds. "a blocks b" keeps b out of ready until a closes. "a part-of b" puts a under b; b is then a plot. "a discovered-from b" records where work came from without ordering it. A structural cycle is refused, naming both seeds and the edge to remove.

    attn seed link s-7k3f9m blocks s-p4k2m1

    attn seed unlink <a> <blocks|part-of|discovered-from> <b> [--json]

Removes the edge "a relation b". Every link has an unlink.

## Finding and reading work

Every change to a seed adds an entry to the seed's log: notes, handoffs, moves, links, edits, checks, attachments and answers. `attn seed show <id>` prints a bookmark under the seed's header, `bookmark  attn seed show <id> --since <N>`, then the newest entries. `attn seed notes <id>` lists the whole log and ends with its own bookmark line. Run a bookmark later to get only the entries saved after N, on the seed and every seed below it, each labeled with its seed. That read ends with a new bookmark. Keep the bookmark from your last read and read since that bookmark, instead of reading the whole seed again. When a bookmark is older than the entries the event log keeps, the read says so and gives the full seed instead.

    attn seed ready [--plot <plot> | --all] [--json]

Lists what you can tend right now, oldest first: nothing open blocks it, nobody is holding it, and it is not a plot, because a plot's work is its children. With neither `--plot` nor `--all` the scope is your profile's garden, unless this session was dispatched at a plot, and then that plot; `--all` steps back out to the garden.

    attn seed ls [--plot <plot> | --stale | --standby] [--window <d>] [--flat] [--json]

Lists the garden, newest first, with children nested under their plot, without bodies or checks; `attn seed show` reads one seed whole. `--plot` lists every child of one plot, oldest first, harvested and withered included. `--stale` narrows to open seeds whose log has not moved for `--window` (default 7d): a query for your judgment, never a reaper. `--standby` narrows to seeds on standby, each with the pull requests it watches and their readiness, its reminder, and why a resume is blocked when one is.

    attn seed show <id> [--since <bookmark> | --section <heading|id>] [--json]

Reads one seed, news first: its state, who tends it and a bookmark; the newest entries on its log, and where the freshest handoff is when it is older than those; every edge that touches it, with seeds discovered from it folded to a count and the open ones; the outline of the plot it is part of; a plot's open children as a board, each with what it waits for, its checks and its newest entry, closed children folded into one line; checks and artifacts; the body last.

`--since` returns only the log entries saved after a bookmark, on this seed and every seed below it, newest first and labeled by seed, then a new bookmark. `--section` returns one part of the body: the section under a heading, or every item that starts with an id like D3.

    attn seed show s-7k3f9m --since 3618073

    attn seed notes <id> [--since <bookmark>] [--kind <kind>] [--limit <n>] [--json]

Lists the whole log, newest first; `attn seed show` renders the newest few and says how many more are here. The last line is a bookmark. `--since` lists every entry saved after a bookmark, on this seed and every seed below it, labeled by seed. `--kind` keeps one kind of entry, such as handoff or answer.

    attn seed notes s-7k3f9m --kind handoff

    attn seed prime

Prints the standing garden contract and this session's live garden position.

    attn seed guide

Prints the craft: writing a body, deliverable types and what "done" is for each, a complete plot example, artifacts, and handoffs and steering.

## Tending a seed

The moves are tend, park, harvest, wither and replant. A move needs no `--force` when the actor is one of these:

- the claim's tender
- a session above the tender in the delegation chain
- the profile's chief

A move also needs no `--force` after the tender's session ends, because a session's claim lapses when the session ends, unless the seed is on standby. The claim holds again if the session resumes. A crew member's claim lasts across the member's sessions.

Everyone else needs `--force`, and the log records that they used it. A call without a live session has no standing. A move records who acted and whose claim ended, and prints the audit note and the sessions attn rang. A delegate hears the move through the delegate's delegation watch.

Repeating a move to the same state succeeds without rewriting the seed or ringing anyone. A different reason goes on the seed's log as a quiet note. To move between harvested and withered, run `attn seed replant <id>` first.

    attn seed tend <id> [-m "<why>"] [--for <name>] [--force] [--json]

Claims the seed and starts growing it. The freshest handoff prints on the claim, so picking a seed up primes you. Tending your own claim again changes nothing and rings nobody. `--for` assigns the seed to a crew member instead; the user never claims a seed, so a tend from outside an attn session needs it.

    attn seed note <id> -m "<what happened>" [--handoff] [--ring] [--json]

Appends to the seed's log: what happened and what you learned, for whoever tends it next. `--handoff` addresses it to your successor on this seed: `attn seed tend` prints the freshest one on the claim, so it is read before any work, and `attn seed show` points at it once newer entries bury it. `--ring` tells current and covering plot tenders and watchers to look; ordinary notes stay quiet.

    attn seed note s-7k3f9m -m "The parser change passes focused checks. Integration verification remains."

    attn seed park <id> [-m "<where you left it>"] [--force] [--json]

Pauses the seed deliberately: it is parked and lets go of its tender. An optional `-m` lands on the log in the same move. Tending it again picks it back up. Parking it again keeps the state and adds only the reason. Moves print who they rang.

    attn seed replant <id> [-m "<why>"] [--force] [--json]

Puts the seed back in the pool: planted, unclaimed, ready for whoever is free. Reopens a closed seed, takes a parked one off hold, and hands back one being grown. Replanting an already planted seed changes nothing and rings nobody. Moves print who they rang.

    attn seed wither <id> [-m "<why>"] [--force] [--json]

Closes the seed as abandoned: nobody is picking this up. Repeating it keeps the earlier reason and adds a different reason to the log. A harvested seed must be replanted first. A plot refuses while any direct child is open. Closing a plot's last child leaves a ringing note on the plot and names the plot in the output. Withering withdraws the seed's open asks from the user and names them in the output. Moves print who they rang.

    attn seed send-to-chief <id> [-m "<optional guidance>"] [--json]

Gives this seed to the Chief to decide its next working context. The saved folder, branch and placement problem are recorded automatically; `-m` adds an exception such as a special branch or directory.

## Closing a seed or a plot

Harvest and wither refuse on a plot while any direct child is planted, growing or parked, even with `--force` or `--when-merged`. The refusal names each open child. To close the plot, first close each open child or move its follow-ups out. To give up your claim on the plot without closing it, run `attn seed replant <plot>`. When the last open child closes, attn adds a ringing note to the plot and names the plot in the output. The plot keeps its state.

When the only thing left on a seed is a pull request merging, say so once with `attn seed harvest <id> --when-merged` and let attn close it. A seed you were growing is parked, and attn releases your claim. Every surface then shows `harvests when owner/repo#n merges`. When the merge lands, attn harvests the seed as the member `attn`, with your reason. If a pull request closes without merging, attn clears the arming and rings instead, and the seed stays where it was. If the seed has gained open children or an open ask, attn clears the arming at the merge and explains why. Use `--when-merged` only when the merge is genuinely the last step. Review notes to answer, docs to write, and evidence to attach are work, and work belongs to a tender.

An ask about the seed keeps it open, and its answer comes to whoever tends the seed. The user's own harvest and any wither withdraw the seed's open asks, and the command names them.

    attn seed harvest <id> [<pr-url>] [-m "<what got done>"] [--when-merged] [--clear] [--force] [--json]

Closes the seed as done, with a required `-m` unless `--when-merged` is set. Repeating a harvest succeeds with the earlier reason; a different reason goes on its log. A withered seed must be replanted first. A plot refuses while any direct child is open. A seed with an ask the user has not answered refuses and names the ask: end your turn, harvest after you act on the answer, or withdraw the ask with `attn ask close`. Closing a plot's last child leaves a ringing note on the plot and names the plot in the output. Moves print who they rang.

`--when-merged` releases your claim and harvests the seed with your `-m` when the pull request merges. Without a reason it uses "PR #N merged: <title>". This command merges nothing. With no `<pr-url>` it takes this session's single open pull request, and names the pull requests when there is more than one; `--when-merged` with `--clear` takes the arming back.

    attn seed harvest s-7k3f9m -m "Retries land; the wire test covers a 503" --when-merged
    attn seed harvest s-7k3f9m --when-merged --clear

## Checks

A check is a numbered statement (C1) that a seed must prove. attn stores the check on the seed as data, with its latest report and verdict. Cite the check by id in the body. Define checks with `checks` in plot JSON, with `--check "<statement>"` on plant, or later with `attn seed check add`. Each gives the check the next free id in the plot tree and prints that id. A plot check marked shared applies to every seed below the plot that has no children, and each of those seeds needs its own report for it. `attn seed show` lists a seed's checks, including inherited ones, as open, reported, confirmed or rejected.

Whoever does the work reports each check, as Report each check as you prove it (`attn skill --reference delegated-agent`) describes. Whoever judges the work confirms or rejects. Checks never block a move. The harvest command lists the checks that are still open or rejected. Add `--ring` to a check command when the other side must act now.

    attn seed check add <id> [<C#>] -m "<statement>" [--shared] [--ring] [--json]

Defines a statement the seed must prove and prints its id, which the body cites. attn picks the next free id in the plot tree; `<C#>` names one instead. On a plot, `--shared` makes every seed below it prove the check with its own report. Removed ids are never reused.

    attn seed check edit <id> <C#> -m "<statement>" [--ring] [--json]

Replaces the check's statement.

    attn seed check remove <id> <C#> [-m "<why>"] [--ring] [--json]

Removes a check. Its id stays reserved.

    attn seed check restore <id> <C#> [-m "<why>"] [--ring] [--json]

Brings a removed check back with its result.

    attn seed check report <id> <C#> -m "<summary line>[\n\n<detail>]" [--evidence <file|artifact|url>]... [--commit <rev>] [--ring] [--json]

Records the proof for a check in one call: the first line of `-m`, one line of at most 400 characters that `attn seed show` prints, and the rest of `-m`, kept in the report's check note. Together they fit in 32 KiB. `--evidence` is a local file, an artifact on the seed, or an http(s) URL, and can repeat. A local file is copied onto the seed, replacing its file of the same name, unless it already sits in the seed's artifact folder. A refused report lists every problem it found; nothing is recorded. A new report clears the verdict. `attn seed show` marks cited evidence replaced after the report. If a check doesn't apply, report that too: say why and cite what shows it. The verdict decides.

    attn seed check report s-7k3f9m C1 -m "Retries pass" --evidence retry.log

    attn seed check confirm <id> <C#> [-m "<how you verified it>"] [--ring] [--json]

Confirms the check, with or without a report.

    attn seed check reject <id> <C#> -m "<what would prove it>" [--ring] [--json]

Rejects the check. `-m` names what would prove it.

    attn seed check uncheck <id> <C#> [-m "<why>"] [--ring] [--json]

Clears the verdict and keeps the report.

    attn seed check reopen <id> <C#> [-m "<why>"] [--ring] [--json]

Clears the report and verdict.

## Rings and watches

A plot's tender hears updates from every seed under the plot, including grandchildren, without a watch. When the claim ends, that coverage ends and attn withdraws unread updates unless a watch still covers them. Lifecycle moves ring current and covering tenders and watching sessions, and print who they rang. Harvesting or withering a blocker also rings the parties of each seed it was blocking, unless another open blocker still holds that seed. Notes stay quiet unless you add `--ring`. A successful action never rings the session that caused it. Automation acceptance is quiet, and its bell waits until the work is ready or the attempt needs attention.

A bell carries the seed, what moved, and the read that shows it: `attn seed show <id> --since <N>`, where N is the bookmark just before the first change the bell covers. Later changes join the unread bell, and the same read returns them too. Any `show` or `notes` of the seed resets the bell for the next meaningful move. You cannot recall updates already read or handed to an agent.

    attn seed watch <id> [--json]

Subscribes this session to the seed and its descendants, including children planted later. Delegation starts the same watch on the seed it binds. Tending a plot already covers its descendants until the claim ends. A crew member's watch belongs to the member and lasts across the member's sessions. `attn seed show` names the subscriptions that still cover a seed. Restarting or resuming the same delegation does not restore a removed watch.

    attn seed unwatch <id> [--json]

Removes this seed's subscription. It also drops a queued update only when the session is not a current tender, not a covering tender, and not covered by another watch. Separate child watches remain, and an ancestor watch can still cover the seed. `attn seed show` and this command name those subscriptions and the commands that remove them.

## Reminders and standby

Standby closes your session for a wait that needs nobody in it: a time, a pull request, another seed or another agent. If you tend no seed for the work, plant one first. A wait on the user is different. Ask with `attn ask` and end your turn. Your session stays open, so the user can find you, talk to you and run commands in your terminal, and attn types the answer into this conversation.

Seed mail is a reminder, a message sent with `attn agent msg <seed-id>`, an answer to an ask about the seed, or an update from a PR watch on the seed. The setting `garden.tender_resume` chooses which mail resumes a session: `any` (the default), `watches` (PR watches and reminders) or `off`. attn resumes a tender at most 48 times per seed in 24 hours. When it cannot resume one, it notes the reason on the seed and notifies the user, and the mail stays unread.

The note is the first thing the resumed session reads, so write down anything the user approved up front, such as "merge once approved". The resumed session should act without waiting for the user: go on standby again, harvest, ask with `attn ask`, or report on the seed.

    attn seed remind <id> (--in <duration> | --at <time>) -m "<message>" | remind <id> --clear [--json]

Sets a reminder: at that time attn posts `-m` to the seed's mailbox, which delivers it to whoever tends the seed then. When nobody tends it, the message waits for the next tender, who reads it with `attn agent inbox`. `--in` counts from now; `--at` takes an RFC3339 time; one of them is required unless `--clear`. attn checks once a minute, so a message can arrive up to a minute late. A seed holds one reminder, and a new one replaces it. The reminder ends when attn posts the message, when `--clear` removes it, and when the seed is harvested or withered. `attn seed show` and `attn seed ls` print it until then. While someone tends the seed, only the tender, sessions above it in the delegation chain, the profile's chief and the user can set or clear its reminder; anyone can while nobody holds the claim, and nobody can force it.

    attn seed remind s-7k3f9m --in 20m -m "Check the rollout of the new build"
    attn seed remind s-7k3f9m --at 2026-11-05T09:00:00+01:00 -m "Ask for the review"
    attn seed remind s-7k3f9m --clear

    attn seed standby <id> -m "<what to do next>" [--in <duration> | --at <time>] [--stay] | standby <id> --clear [--json]

Puts a seed you tend on standby with `-m`, a note saying what to do next, and closes this session. When mail reaches the seed, attn resumes the session and delivers the note; for a crew member it starts a session. `--in` and `--at` also set the seed's reminder to that time, with the note as its message, as `attn seed remind` does. When the session can compact, attn compacts it before closing; end your turn right after the command. `--stay` keeps this session open.

attn refuses while the seed has mail you have not read; read it with `attn agent inbox` first. attn also refuses to close the session while it has an open ask, about this seed, another seed or none, and names each ask: a wait on the user keeps the session open, so ask with `attn ask` and end your turn. When the user tells you to close while an ask is open, put the seed on standby with `--stay`, then run `attn agent close --self`: the seed stays on standby, and the answer resumes you.

Only the seed's tender sets standby, from its own session, and the seed stays claimed while the session is closed. A new standby replaces the note. Standby ends once you have read all of the seed's mail, and when the seed is parked, harvested, withered or gets a new tender. If the session closes before reading the mail, the mail resumes it again. `--clear` ends standby and leaves the reminder.

    attn seed standby s-7k3f9m -m "The user asked to pick this up Friday: rerun the backfill, then report the counts." --at 2026-11-13T09:00:00+01:00
    attn seed standby s-7k3f9m -m "Review is pending on #123. When it is approved, merge (the user approved this up front), then follow the deploy."
    attn seed standby s-7k3f9m -m "Waiting for the user to try the build." --stay
    attn seed standby s-7k3f9m --clear

## Bodies and authorization

To change a body, export it, edit the file, and submit it with `attn seed edit`. EDITING A BODY in `attn seed guide` gives the steps and explains how to roll back an edit.

    attn seed export <id> [--out <path>] [--json]

Writes the raw body with a first-line stamp naming this seed and its body hash. Edit the file with any tool, keep its first line, then submit it with `attn seed edit <id> -m - < <id>.md`. `--out` - writes to stdout; the default is <id>.md in the current directory.

    attn seed edit <id> -m - [--note "<what changed>"] [--json]

Saves an exported body after editing the file with any tool. Keep the first stamp line: it names the body the edit started from. If the body changed since export, export again and reapply. Empty bodies are refused. Each edit saves a numbered version and a log entry; `--note` adds what changed to that entry.

    attn seed rollback <id> [<version>] [--json]

Restores the body before the newest version, or the numbered `<version>`. The restored body becomes a new version, so rollback can itself be rolled back. Edit entries in `attn seed notes` show the versions and size changes.

    attn seed authorization <id> ["<words>" | --clear] [--json]

Sets, clears or prints the task's authorization: the steps agents take beyond the project's guidance and the steps they stop before, written as instructions. Set only what the user allowed. Delegation openings deliver the words. When someone other than the seed's tender changes the words, attn types the change into the tender's conversation as the user's message, waiting while the tender needs approval. With neither `"<words>"` nor `--clear`, prints them and the state of the last typed update. Who may change them follows seed moves: the tender, sessions above it in the delegation chain, and the profile's chief; anyone in the profile while nobody holds the seed.

## Artifacts

To bring a local file into durable seed ownership, choose Move or Copy explicitly. Prefer Move. Tracked Git files must use Copy. Repository, Notebook and URL documents remain links where they already live.

When you name a seed's artifact to the next agent, say which copy is canonical. For a repository document, the Git file is canonical, so name its branch and introducing commit in the brief. For a Notebook document, the Notebook file is canonical. Notes on the seed record meaningful edits, renames and deletions, and tell you when to read the plan again.

The Garden labels old path associations as linked files. Bring a linked file into seed ownership explicitly with Move or Copy. A failed transfer leaves the link alone.

    attn seed attach <id> [--path <file> | --notebook <id> | --url <url>] [--move | --copy] [--repo <repository>] [-m "<note>"] [--json]

Puts a local regular file in durable seed-owned storage, or records a document that stays where it lives. For a local file, `--move` or `--copy` is required: move transfers the file and leaves one editable canonical file, which is the recommended choice; copy keeps an independent source snapshot. Git-tracked files must use copy. A file the seed already has by that name is replaced, and `attn seed show` marks check evidence replaced after its report; re-attaching an unchanged file changes nothing. A `--path` in a `--repo`, a `--notebook` document and a `--url` remain associations, and `-m` goes on the log with them.

    attn seed attach s-7k3f9m --path retry.log --move
    attn seed attach s-7k3f9m --path plan.md --copy
    attn seed attach s-7k3f9m --path docs/design.md --repo attn
    attn seed attach s-7k3f9m --url https://example.com/spec

    attn seed detach <id> [--path <filename> | --notebook <id> | --url <url>] [--to <destination>] [--reference] [--repo <repository>] [-m "<note>"] [--json]

Moves a managed artifact back out of the seed to `--to`, and never overwrites. `--reference` removes an old linked path association instead. `--repo`, `--notebook` and `--url` drop the association they name, and `-m` goes on the log with it.

    attn seed detach s-7k3f9m --path retry.log --to ./retry.log
    attn seed detach s-7k3f9m --path notes.md --reference

## Garden review

    attn seed review start [--json]

Starts an advisory Garden review. The review never changes a seed's state.

    attn seed review show [<review-id>] [--json]

Inspects the review and its progressive results. With no `<review-id>`, shows the latest review.

    attn seed review cancel <review-id> [--json]

Cancels unfinished classification.

    attn seed review retry <review-id> <seed-id> [--json]

Retries one failed or changed review item.

    attn seed review keep <review-id> <seed-id> [--json]

Keeps a seed growing and reviews it again after seven quiet days.

## Packets

A packet is a plot flagged as a template with declared variables. attn cannot yet create seeds from a packet. `attn seed show` says `packet yes`, and `attn seed ready` skips the packet's subtree.
