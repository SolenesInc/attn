# The garden: seeds, plots, and reporting

The garden is where work lives. Run `attn seed prime` for the rules an agent
follows while working, and `attn seed guide` for the craft of writing a body,
planning a plot, deciding what done means, and handing work over. Syntax lives
in `attn seed --help`.

## Planting work

Track work in seeds, not in markdown TODO lists or your own todo tool. Plant a seed for any work that outlives this turn: a bug you found, a follow-up you are not doing now, a piece you split off. Plant it before you start on it, so the claim and the log exist while you work. Under a plot, plant with `--part-of <plot>` so it stays with its plan. When it fell out of a seed you were working on, add `--discovered-from <seed>` so its origin is on record. Before your turn ends, plant what is still undone.

## Planting a plot

`attn seed plot -f <payload.json>` plants a plot and its children in one move.
The payload names sibling blockers by slug:

```json
{
  "title": "Search moves to the daemon",
  "body": "Outcome: the app asks the daemon for search results...",
  "children": [
    {"title": "Daemon search endpoint", "body": "..."},
    {"title": "App calls the endpoint", "body": "...", "blocks": ["remove-the-client-index"]},
    {"title": "Remove the client index", "body": "..."}
  ]
}
```

The slug is the sibling title lowercased. Each run of characters that are not
ASCII letters or digits becomes one dash. Pass `-` to read the payload from stdin.

## Rings and watches

Lifecycle moves ring the sessions with a stake in the seed. Notes stay quiet
unless you add `--ring`. `attn seed watch <id>` gives this session a stake;
watching a plot covers every seed in it. `attn seed unwatch <id>` is the way
out.

A bell carries only the seed and what moved, so read it with `attn seed show`;
`show` or `notes` resets the bell for the next meaningful move.

## Artifacts

Attach a document where it already lives, and detach the pointer when it stops
being current:

    attn seed attach <id> --path <file.md> [--repo <repository>]
    attn seed attach <id> --notebook <document-id>
    attn seed attach <id> --url <url>
    attn seed detach <id> --path <file.md>

## Packets

A packet is a plot flagged as a template with declared variables.
Nothing sows one yet.
`attn seed show` says `packet yes`, and `attn seed ready` skips its subtree.
