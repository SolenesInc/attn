# Notebook

Load this reference when you read or maintain the attn Notebook, your
profile's durable markdown store, especially when your session is the chief of
staff. The Notebook outlives any single agent.

The Notebook is plain markdown on disk. Read the files, and edit knowledge
notes, with your native tools (Read, Write, Edit, `ls`, `rg`). To append a
journal entry, run `attn journal append`, which serializes writes at the home.
There is no `attn notebook` CLI.

Your operating guidance gives you the Notebook root, and the chief of staff
gets that guidance at launch. `attn settings get notebook.root.effective`
prints the root. All profiles share
one folder, `~/attn-notebook` by default. A profile can set its own folder with
`notebook.profile_root`, and a non-default instance adds its name to the folder
name. Paths below are relative to `<root>`.

On an outpost, `notebook.root.effective` is that machine's copy of your
profile's Notebook folder, and the copy syncs with the home. `attn seed show
<seed>` prints that machine's artifact folder.

`<root>/crew/` holds crew members' homes: a folder per member with the member's
charter and letters. attn owns that folder and records where each home is, so
do not move, rename or delete anything in it.

## Orient first

Before you add anything, load what is already known by reading the index files:

    <root>/index.md             # the bundle guide
    <root>/knowledge/index.md   # the knowledge-base nav

Then browse the relevant subtree with `ls` or `grep` under
`<root>/knowledge/...` or `<root>/journal/`.

## Two layers

- **The journal** is `<root>/journal/<date>.md`: dated entries that form the
  durable, curated log of what was done across attn. It is kept for the user's
  recall and reviews, not as a raw dump. Entries have `type: journal`.
- **The knowledge base** is `<root>/knowledge/`: distilled, timeless notes worth
  keeping, such as decisions, gotchas, and domain knowledge that outlived a
  single change. It is organized **PARA-style**. `projects/` holds bounded
  efforts, roughly one per plot or epic. `areas/` holds ongoing
  responsibilities and subsystems. `resources/` holds reference material.
  `archive/` holds inactive items.

When a `projects/<slug>/` folder corresponds to a plot in the garden, stamp the
`index.md` frontmatter of that folder with `resource: attn:seed/<plot-id>`.
When the work ends, first promote anything durable into `areas/`, because
archived notes drop out of the active view. Then move the folder under
`archive/` yourself.

The knowledge base is not a task tracker. Capture what is *known*, not what is
*to do*.

## Append a journal entry

Append a short prose entry through the home:

```sh
attn journal append --entry 'What changed and why it matters.'
attn journal append --date 2026-06-18 --entry 'An entry for a specific day.'
```

The command creates the day's file with journal frontmatter when needed. Without
`--date`, it uses the home's date. Use the command from outposts too, so concurrent
appends share the same writer. Read the resulting file through your local copy.

## Write or edit a knowledge note

Create a file under the right PARA directory, such as
`<root>/knowledge/areas/notebook.md`, with OKF frontmatter and a tight body:

    ---
    type: note
    title: Notebook is filesystem-canonical
    summary: The .md files on disk are the source of truth.
    sources:
      - /journal/2026-06-18.md
    ---

    The Notebook's markdown files are canonical. Edits are plain file writes.

To edit, Read the file, then Edit it. These are ordinary on-disk edits. The
daemon watches the Notebook folder and refreshes any open Notebook view in the
app when a file changes.

## Rules that keep the knowledge base trustworthy

- **Grounding is required.** Give every durable knowledge note resolvable
  `sources:` in its frontmatter, as journal anchors or URLs. Do not write a note
  from paraphrase alone.
- **Frontmatter is OKF.** `type:` is the one required field. Its value is an
  open string you choose, such as `note`, not a fixed list. `title`, `summary`,
  `tags`, `created`, `updated`, and `sources` are recommended. attn leaves
  unknown keys untouched. The `index.md` and `log.md` files that attn scaffolds
  have no frontmatter. A project's `index.md` is the exception: it carries
  `resource:`, as above.
- **Links are root-absolute markdown:** `[label](/knowledge/areas/foo.md)`, not
  `[[wikilinks]]`. Keep the relationship kind, such as supersedes or
  relates-to, in prose.
- **Be concise.** Future agents read notes under a token budget, so prefer a
  tight summary plus a `sources:` pointer over a transcript.

## As chief of staff

The Notebook holds your member home and the knowledge you maintain. When attn
promotes you to the role mid-session, attn points you at `<root>/index.md`.
Read that file to orient. Then read
`<root>/knowledge/index.md`, record durable decisions in the knowledge base as
you make them, and keep the day's journal current with your view of your
profile. Write at a chief-of-staff level of detail: what moved across your
profile's agents, what you delegated, and what you decided. Do not narrate each
agent's work. Each profile has its own chief. Chiefs of profiles that share a
Notebook folder write to the same files.
