# Notebook

Load this reference when you read or maintain the attn Notebook — your
profile's durable markdown store — especially when your session is the chief of
staff. The Notebook outlives any single agent.

The Notebook is plain markdown on disk. Read it and edit knowledge notes with
native tools (Read/Write/Edit and `ls`/`rg`). Append journal entries through
`attn journal append`, which serializes writes at the home.
There is no `attn notebook` CLI. The notebook root is given to you in your
operating guidance (the chief-of-staff launch injection), and
`attn settings get notebook.root.effective` prints it. Profiles share one folder,
`~/attn-notebook` by default (per-instance variants append the instance name),
unless a profile sets its own with `notebook.profile_root`. Paths below are
written relative to that `<root>`. On an outpost, `notebook.root.effective` names
that machine's copy of the shared Notebook, which syncs with the home; a folder
a profile set for itself does not sync. `attn seed show <seed>` prints that
machine's artifact folder.

`<root>/crew/` holds crew members' homes: a folder per member with its charter
and letters. attn owns that folder and records where each home is, so don't
move, rename or delete anything in it.

## Orient First

Load what is already known before adding anything — read the index files:

    <root>/index.md             # the bundle guide
    <root>/knowledge/index.md   # the knowledge-base nav

Then browse the relevant subtree (`ls`/`grep` under `<root>/knowledge/...` or
`<root>/journal/`).

## Two Layers

- **The journal** — `<root>/journal/<date>.md`, dated entries: the durable,
  curated log of what was done across attn, kept for the user's
  recall and reviews, not a raw dump. Entries carry `type: journal`.
- **The knowledge base** — `<root>/knowledge/`, distilled, timeless notes worth
  keeping: decisions, gotchas, domain knowledge that outlived a single change. It is
  organized **PARA-style**: `projects/` (bounded efforts, roughly one per
  plot or epic), `areas/` (ongoing responsibilities and subsystems),
  `resources/` (reference material), `archive/` (inactive items). As a project
  finishes, promote its durable knowledge up into `areas/`.

When a `projects/<slug>/` folder corresponds to a plot in the garden, stamp its
`index.md` frontmatter with `resource: attn:seed/<plot-id>`. Move the folder under `archive/`
yourself when the work ends, and promote anything durable into `areas/` first,
since archived notes drop out of the active view.

The knowledge base is not a task tracker. Capture what is *known*, not what is
*to do*.

## Append A Journal Entry

Append a short prose entry through the home:

```sh
attn journal append --entry 'What changed and why it matters.'
attn journal append --date 2026-06-18 --entry 'An entry for a specific day.'
```

The command creates the day's file with journal frontmatter when needed. Without
`--date`, it uses the home's date. Use the command from outposts too, so concurrent
appends share the same writer. Read the resulting file through your local copy.

## Write Or Edit A Knowledge Note

Create a file under the right PARA directory, e.g.
`<root>/knowledge/areas/notebook.md`, with OKF frontmatter and a tight body:

    ---
    type: note
    title: Notebook is filesystem-canonical
    summary: The .md files on disk are the source of truth.
    sources:
      - /journal/2026-06-18.md
    ---

    The Notebook's markdown files are canonical; edits are plain file writes.

To edit, just Read then Edit the file — ordinary on-disk edits. The daemon's
filesystem watcher notices your change and refreshes any open in-app browser.

## Rules That Make The Knowledge Base Trustworthy

- **Grounding is required.** Every durable knowledge note must carry resolvable
  `sources:` in its frontmatter — journal anchors or URLs. Do not author a note
  from paraphrase alone.
- **Frontmatter is OKF.** `type:` is the one required field — an open,
  author-chosen string (e.g. `note`), not a fixed enum. `title`, `summary`,
  `tags`, `created`, `updated`, and `sources` are recommended. Unknown keys are
  preserved untouched. The reserved files `index.md` and `log.md` carry no
  frontmatter.
- **Links are root-absolute markdown:** `[label](/knowledge/areas/foo.md)`, not
  `[[wikilinks]]`. Keep the relationship kind (supersedes, relates-to) in prose.
- **Be concise.** Notes are read by future agents under a token budget; prefer a
  tight summary plus a `sources:` pointer over a transcript.

## As Chief Of Staff

The Notebook is your home. When you are promoted to the role mid-session, attn
points you at `<root>/index.md` — read it to orient. Read `<root>/knowledge/index.md`,
record durable decisions in the knowledge base as you make them, and keep the
day's journal current with your view of your profile. Write at a chief-of-staff
altitude — what moved across your profile's agents, what you delegated and
decided — not a per-agent play-by-play. Each profile has its own chief; chiefs
of profiles that share a Notebook folder write to the same files.
