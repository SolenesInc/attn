attn keeps work as seeds in the garden. A seed is one unit of work: a short id like `s-7k3f9m`, a title, a markdown body, a state. A plot is a seed with children: its body is the execution plan, and the children are parallel unless a `blocks` edge orders them. Any seed can be a plot. Seed packets are templates for plots; if you are told to use packets, or the task calls for one, the attn skill says how they work.

Track work in seeds, not in markdown TODO lists or your own todo tool. Plant a seed for any work that outlives this turn: a bug you found, a follow-up you are not doing now, a piece you split off. Plant it before you start on it, so the claim and the log exist while you work. Under a plot, plant with `--part-of <plot>` so it stays with its plan. When it fell out of a seed you were working on, add `--discovered-from <seed>` so its origin is on record. Before your turn ends, plant what is still undone.

A delegated session reports to one seed: the seed planted for its brief, or the seed it was dispatched at with `--plot`. Flag-free `attn seed ready` answers from that seed's plot, a delegation started from it is planted under it, and its report lands on its log. Every other verb takes the id you give it.

The loop:

    attn seed ready                  what you can pick up now: open, not parked, not blocked, nobody holding it; inside your plot when you report to one. A plot is never ready itself, its children are
    attn seed ready --all            the same across the whole garden; use it to look past your plot
    attn seed show <id>              body, state, tender, edges, children, freshest handoff
    attn seed tend <id>              claim it; one tender at a time, a held seed refuses you by name
    attn seed note <id> -m "..."       what happened and what you learned, tending it or not; --handoff addresses the next tender; --ring tells watchers to look
    attn seed harvest <id> -m "..."    done; the reason is required and fits in 400 characters, the long version goes in a note
    attn seed wither <id> [-m "..."]   abandoned, nobody will pick it up
    attn seed park <id>              put down, claim released; tend it again to resume
    attn seed replant <id>           a harvested or withered seed back to planted
    attn seed plant "<title>" -m "..." [--part-of <plot>] [--discovered-from <seed>]    a new seed; prints the id

Tend, park, harvest, wither and replant all check who holds the seed. `--force` acts anyway, with the log recording who forced it. A seed whose session ended is not held. `--member <name>` acts as a crew member instead of this session, and a member's claim never expires.

Plans:

    attn seed plot -f <file.json>    plant a whole plot in one move
    attn seed link <a> blocks <b>    b waits until a closes
    attn seed link <a> part-of <b>   a joins b's plot
    attn seed link <a> discovered-from <b>    record where work came from without ordering it
    attn seed ls [--flat]            everything planted and who holds it
    attn seed edit <id> -m "..."       replace the body; say what changed in a note

Keeping up:

    attn seed notes <id>             the whole log, newest first
    attn seed watch <id>             ring this session when the seed or anything in its plot moves
    attn seed attach <id> --path <file.md> | --notebook <doc-id> | --url <url>
    attn seed export <id> [--out <path>]
    attn seed set-resume <id> --resume-session-id <id> --cwd <path> --agent <name>

Delegating:

    attn delegate --brief "..." --model <m>   starts a visible agent session and plants a seed bound to it
        --plot <seed>                       dispatch at an existing seed
        --brief-file <path>                 read the brief from a file
        --new-workspace | --workspace <id> | --cwd <path>
    attn agent msg <seed-id> "..."            reaches whoever tends it now
    attn seed show <id>                     reads the delegate's report

`attn seed --help` has every flag. `attn seed guide` has how to write a body worth handing to somebody else.
