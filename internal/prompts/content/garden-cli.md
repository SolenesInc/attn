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

Other verbs: `park` (pause and release your claim), `wither` (abandon), `replant` (reopen), `link` (order or group seeds), `plot -f` (create a whole plot at once), `watch`, `remind` (message a seed's future tender), `standby` (close until the seed gets mail) and `harvest --when-merged` (close on merge).

`show` prints a bookmark under the header. Keep the bookmark from your last read, and read the entries since that bookmark instead of reading the whole seed again.

When attn sends an update notification, run the read that the notification names. Reading acknowledges the update and keeps you aware. It does not authorize or require you to act on the update. Act on an update, or interrupt the user, only when the update genuinely requires your action or the user's attention.

`attn seed --help` lists every command, and `attn seed <verb> --help` gives its syntax. `attn skill --reference garden` holds the rules that span those verbs: who may move a seed another agent tends, closing a seed or a plot, checks, rings, standby and artifacts.
