## The garden

attn keeps work as seeds in your profile's garden. A seed is one unit of work. It has a short id like `s-7k3f9m`, a slug like `mermaid-rendered-grid`, a title, a markdown body, and a state. `attn seed`, `attn delegate --seed`, and `attn ask --seed` take the id or the slug. `attn open` and `attn agent msg` take the id. Give other agents the id, because attn refuses a slug that two seeds share. To the user, say the slug: `mermaid-rendered-grid` (`s-7k3f9m`) on first mention, then the slug alone. Never make the user decode an id.

Write every seed body as a work prompt for an agent who has not seen this conversation, including bodies for work you plan to do yourself. `attn seed guide` says what the prompt contains.

A plot is a seed with children. The plot's body holds the shared plan, and each child has its own work prompt. In each child's work prompt, name the parent section, sibling result, or artifact to read, and say why, because a delegation does not include related bodies automatically. Before you write a plot, read `attn seed guide`.

Garden words have Jira-style equivalents: seed = ticket, ready = todo, plot = epic, harvested = done. Use the Garden word by default. When the user uses one of those Jira words, use the same word for that concept for the rest of the exchange. Do not correct the user. Keep using the Garden words for the other concepts until the user uses their Jira words.

Track work that outlives this turn in seeds, such as a bug you found, a follow-up you are not doing now, or a piece you split off. Do not use markdown TODO lists or your harness's todo tool for it, because they end with the session. Before you plant, run `attn seed search <words>`, so you find work that already exists instead of planting it twice. Plant work before you start it, so the claim and the log exist while you work. To plant under a plot, use `--part-of <plot>`. If you discover work while tending another seed, add `--discovered-from <seed>`. Before your turn ends, plant the work that is still undone. Harvest a seed when the outcome and required verification in its body are complete.

A delegation assigns its seed to the new agent as that agent's stake: the seed the agent tends and reports to. When a session that has a stake delegates more work, attn plants the new brief as a child of that stake.

The loop:

    attn seed ready                  what you can pick up now. When you report to a plot, the list covers only that plot. --all covers the whole garden
    attn seed show <id>              news first: state, tender, a bookmark, the newest entries, the plot's outline, checks, then the body
    attn seed notes <id>             the whole log, newest first. `--since <N>` gives only the entries after a bookmark
    attn seed tend <id>              claim the seed. A seed has one tender at a time
    attn seed note <id> -m "…"       record what happened and what you learned. `--handoff` addresses the next tender. `--ring` tells watchers to look
    attn seed check report <id> <C#> -m "…" --evidence <file|artifact|url>    the proof for one of the seed's checks
    attn seed harvest <id> -m "…"    mark the seed done. The reason is required and at most 400 characters. Put the long version in a note
    attn seed attach <id> --path <file> (--move | --copy)    give the seed a file to keep. Prefer Move
    attn seed search <words>         find seeds across your profile's garden, closed ones included
    attn seed plant "<title>" -m "…" [--part-of <plot>] [--discovered-from <seed>]    create a seed and print its id

Other verbs: `park` (pause and release your claim), `wither` (abandon), `replant` (reopen), `link` (order or group seeds), `plot -f` (create a whole plot at once), `watch`, `remind` (message a seed's future tender), `standby` (wait for mail, optionally closing yourself) and `harvest --when-merged` (close on merge).

`show` prints a bookmark under the header. Keep the bookmark from your last read, and read the entries since that bookmark instead of reading the whole seed again.

When attn sends an update notification, run the read that the notification names. Reading acknowledges the update and keeps you aware. It does not authorize or require you to act on the update. Act on an update, or interrupt the user, only when the update genuinely requires your action or the user's attention.

`attn seed --help` lists every command, and `attn seed <verb> --help` gives its syntax. `attn skill --reference garden` covers those verbs in depth, moving a seed another agent tends, closing a plot, and artifacts.
