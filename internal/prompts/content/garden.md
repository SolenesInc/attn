attn keeps work as seeds in your profile's garden. A seed belongs to the profile that planted it, for life. Lists, links, children, claims, and delegations stay in that profile, and attn refuses another profile's seed by name. Inside attn, your session chooses the profile. Outside attn, pass `--profile <name|id>` when several profiles exist. When only one profile exists, attn selects it automatically.

A seed is one unit of work. It has a short id like `s-7k3f9m`, a slug like `mermaid-rendered-grid` (the first key words of the title), a title, a markdown body, and a state. `attn seed`, `attn delegate --seed`, and `attn ask --seed` take the id or the slug, and every verb prints the id beside the slug. `attn open` and `attn agent msg` take the id. Give other agents the id, because attn refuses a slug that two seeds share and lists each match in the refusal. To the user, say the slug: `mermaid-rendered-grid` (`s-7k3f9m`) on first mention, then the slug alone. Never make the user decode an id.

Write every seed body as a work prompt for an agent starting without this conversation, including work you plan to do yourself. State the task and outcome, starting context and constraints, and how to verify completion.

A plot is a seed with children. The plot's body holds the shared plan, and each child has its own work prompt. Keep shared decisions in the parent. In each child's work prompt, name the parent section, sibling result, or artifact to read, and say why. A delegation does not include related bodies automatically. Children run in parallel unless one waits for a sibling. To make a child wait, use `after` in a plot payload or a `blocks` link. Before you write a plot, read `attn seed guide`. Any seed can be a plot. Seed packets are templates for plots, and the attn skill explains them.

Garden words have Jira-style equivalents: seed = ticket, ready = todo, plot = epic, harvested = done. Use the Garden word by default. When the user uses one of those Jira words, use the same word for that concept for the rest of the exchange. Do not correct the user. Do not switch the other concepts unless the user does.

Track work that outlives this turn in seeds, such as a bug you found, a follow-up you are not doing now, or a piece you split off. Do not use markdown TODO lists or your harness's todo tool for it, because they end with the session. Before you plant, run `attn seed search <words>`. It ranks stemmed word-prefix matches across titles, bodies, and logs, including identifier parts and closed seeds, so you find work that already exists instead of planting it twice. Plant work before you start it, so the claim and the log exist while you work. To plant under a plot, use `--part-of <plot>` so the seed stays with its plan. If you discover work while tending another seed, add `--discovered-from <seed>` so the origin is on record. Before your turn ends, plant the work that is still undone. Harvest a seed when the outcome and required verification in its body are complete.

A delegation assigns its seed to the new agent as that agent's stake: the seed the agent tends and reports to. A new delegation stores its brief as the seed body. A delegation at an existing seed uses the assignment in that seed's body. The opening names the seed, and the agent reads it with `attn seed show`. A handover saves the next-step note, transfers ownership, and then starts a new agent. The previous session keeps running. `attn seed ready` without flags shows the plot of your stake. When a session delegates more work, attn makes a new brief a child only when the resolved source has a stake. Every other garden verb acts on the seed you name, by id or slug.

The loop:

    attn seed ready                  what you can pick up now: seeds that are open, not parked, not blocked, and held by nobody.
                                     When you report to a plot, the list covers only that plot. A plot itself is never ready, and only its children can be
    attn seed ready --all            the same list across your profile's whole garden. Use it to look past your plot
    attn seed show <id>              news first: state, tender and a bookmark, the newest entries, edges, the outline of
                                     the plot the seed is part of, a plot's open children as a board, checks, then the body
    attn seed tend <id>              claim the seed. A seed has one tender at a time, and when someone else tends it, attn refuses you and names the tender
    attn seed note <id> -m "…"       what happened and what you learned, whether or not you tend the seed. `--handoff` addresses the next tender
                                     `--ring` tells watchers to look
    attn seed check report <id> <C#> -m "…" --evidence <file|artifact|url>    the proof for one of the seed's checks. attn copies a local file onto the seed. `attn seed check --help` lists the other check moves
    attn seed harvest <id> -m "…"    done. The reason is required and at most 400 characters. Put the long version in a note
    attn seed harvest <id> --when-merged [pr-url] [-m "<outcome and verification>"]    use only when a merge is all that is left: attn releases the claim and harvests the seed when that pull request merges
                                     with no url, attn uses this session's single open pull request. `-m` keeps your reason for the harvest. This command merges nothing
                                     `--when-merged --clear` takes the arming back
    attn seed wither <id> [-m "…"]   abandoned: nobody will pick it up
    attn seed park <id>              stop work and release your claim. Tend the seed again to resume
    attn seed replant <id>           move a harvested or withered seed back to planted
    attn seed search <words>         find seeds by keyword across your profile's garden, closed ones included. The line that
                                     matched prints under each hit. Run it before you plant
    attn seed plant "<title>" -m "…" [--part-of <plot>] [--discovered-from <seed>]    a new seed. The command prints the id

Each of `attn seed tend`, `attn seed park`, `attn seed harvest`, `attn seed wither`, and `attn seed replant` checks who tends the seed. The tender, the sessions above the tender in the delegation chain, and the profile's chief can act on the claim. When a session above the tender, or the chief, acts, attn records who moved the seed and whose claim ended, and the delegate hears about it through the delegate's delegation watch. Everyone else needs `--force`, which also records the move. A call without a live session cannot act as the tender, as a session above the tender, or as the chief. A crew member's claim lasts across the member's sessions. A session's claim lapses when the session ends and holds again when the session resumes. `attn seed tend <id> --for <name>` assigns a seed to a crew member.

If you repeat a move to the same state, the command succeeds without rewriting the seed or ringing anyone. If you give a different reason, attn keeps it as a quiet note. To harvest a withered seed or wither a harvested seed, run `attn seed replant <id>` first. Every move prints who attn rang. Harvest and wither also print the seeds that the close unblocks, and attn rings whoever tends one. Harvest and wither refuse while any direct child is planted, growing, or dormant, even with `--force` or `--when-merged`. When your part of a plot is done, replant the plot to hand it back. Otherwise, close the plot's children or move follow-ups out first. When the last child closes, attn leaves a ringing note on the plot and names it in the output. The plot keeps its state.

Plans:

    attn seed plot -f <file.json>    a whole plot in one move. `-` reads stdin. The file is
                                     {"title": …, "body": …, "part_of": "<plot>", "discovered_from": "<seed>",
                                      "children": [{"title": …, "body": …, "after": ["<sibling-slug>"]}]}
                                     `part_of` and `discovered_from` are optional. `after` names the siblings a child waits for. A slug is the sibling's title
                                     without its stop words, lowercased and joined with dashes. The sibling's title also works. `attn seed guide` has a full example
    attn seed link <a> blocks <b>    b waits until a closes. `unlink` removes the edge
    attn seed link <a> part-of <b>   a joins b's plot. A seed can be in one plot at a time
    attn seed link <a> discovered-from <b>    a was discovered while working on b. The link records that origin and never orders or blocks anything
    attn seed ls [--flat]            everything planted and who holds it, with children nested under their plot. `--flat` gives one list
    attn seed ls --plot <plot>       every child of one plot, harvested and withered included

Export the body, edit the file with any tool, then submit it:

    attn seed export s-7k3f9m
    # edit s-7k3f9m.md and keep its first line
    attn seed edit s-7k3f9m -m - --note "what changed" < s-7k3f9m.md

The first line of the file is a stamp that names the seed and its body hash. If another edit changed the body, export again and reapply your change. attn refuses an empty body. Each edit saves an undoable body version and one log entry, and `--note` adds your explanation to that entry. Read the entries with `attn seed notes s-7k3f9m`. To restore the previous version, run `attn seed rollback s-7k3f9m`. To restore a named version, run `attn seed rollback s-7k3f9m 2`. Rollback saves a new version too.

Keeping up:

attn writes an entry in the seed's log for every change: notes, handoffs, moves, links, edits, checks, attachments, and answers. `show` prints a bookmark under the header, and a `notes` or `--since` read ends with a bookmark. Keep the bookmark from your last read, and read the entries since that bookmark instead of reading the whole seed again.

When attn sends an update notification, run the read that the notification names. Reading acknowledges the update and keeps you aware. It does not authorize or require you to act on the update. Act or interrupt the user only when attention is genuinely needed.

    attn seed show <id> --since <N>  only the entries saved after bookmark N, on the seed and every seed below it
    attn seed notes <id>             the whole log, newest first. It takes `--since <N>`, and `--kind <kind>` for one kind
                                     of entry, such as handoff or answer
    attn seed watch <id>             watch this seed and its descendants. `unwatch` removes the subscription
    attn seed attach <id> --path <file> (--move | --copy)    bring a local file into durable seed ownership, replacing a file of the same name. Move is recommended
    attn seed detach <id> --path <filename> --to <destination>    move an owned file back out without overwriting
    attn seed attach <id> --path <file> --repo <repo> | --notebook <doc-id> | --url <url>    keep a link to a document elsewhere
    attn seed export <id> [--out <path>]    the raw body with its first-line stamp

When you tend a plot, you receive updates from every seed under it, without a watch. When you end the claim, that coverage stops, and attn withdraws unread updates unless a watch still covers them. A new delegation starts an ordinary watch on its seed. `unwatch` removes that subscription, but separate child or ancestor watches remain. `show` and `unwatch` name the subscriptions that still cover a seed and say how to remove them. `watch` restores a subscription. A new delegation starts the watch again, but a restart or recovery does not. A crew member's watch belongs to the member and lasts across the member's sessions.

Use `attn agent msg` for attn-managed sessions. Use Claude's SendMessage only for follow-ups to the native subagents this session spawned.

Run `attn session rename "<a few words>" [--session <id>]` to rename a session, yours by default, when its main work no longer matches the name it started with.

`attn seed --help` lists the commands. `attn seed <verb> --help` gives the syntax and flags of that verb. `attn seed guide` explains how to write a body worth handing to someone else.
