# Notebook

The Notebook is an LLM Wiki, Andrej Karpathy's pattern: a folder of markdown
pages that agents build and keep current, so knowledge compounds instead of
being rediscovered. Edit the files directly with your file tools.
`attn settings get notebook.root` prints the folder. Paths below are relative to
it, `<root>`.

Its parts map onto the pattern:

- `journal/<date>.md` is the record of what happened, one file per day. It is
  the source that knowledge base pages cite.
- `knowledge/` is the knowledge base, the pattern's wiki: pages you own and keep
  current. It starts organized by PARA (`projects/`, `areas/`, `resources/`,
  `archive/`). The user may reshape it.
- `index.md` at the root describes the layout. Each folder's `index.md` says
  what belongs in that folder.
- Each page's `summary:` is its entry in the pattern's index. List them with
  `grep -r '^summary:' <root>/knowledge/`.
- This reference is the schema.

attn owns the names `journal/`, `seeds/`, `crew/` and `.attn/`. Don't move,
rename or delete them.

Each profile's Notebook syncs with outposts. On an outpost, use that machine's
`notebook.root.effective` for `<root>`. Its machine root is a parent folder;
your profile's files are under the stable subfolder for its home root. Profiles
that share a root share that folder. `attn seed show <seed>` prints the artifact
folder on the caller's machine. Change roots only while disconnected from sync
peers.

## Read first

Before you add anything, read `<root>/index.md`, list the summaries, and read the
pages that cover your subject.

## Ingest: fold in what you learn

When something worth keeping happens, such as a decision, a finished delegation
or a fact the user taught you:

1. Add it to the journal with `attn journal append --entry "…"`. It creates the
   day's file, and it doesn't lose an entry when another agent writes the
   journal at the same time. Add `--date YYYY-MM-DD` to backfill a past day.
   Use the command from outposts too, so appends share the home's writer.
   Without `--date`, it uses the home's date.
2. Update the knowledge base pages it touches instead of adding a page beside
   them. Add a page only for a subject no page covers.
3. Fix the links between the pages you changed. If the new fact contradicts a
   page, correct the page and say in the journal what changed.
4. Keep each changed page's `summary:` true to the page.

The knowledge base holds what is known, not what is to do. Tasks belong in the
Garden.

## Query: answer from the knowledge base

To answer a question, list the summaries, read the relevant pages, and cite the
pages you used. When an answer would help again, file it as a page with a
`summary:` and the `sources:` of the pages you used.

## Lint: keep it healthy

When the user asks, or when you notice drift, look for contradictions, stale
claims, missing links and pages without a `summary:` or `sources:`. Fix what you
can and report the rest.

## Projects and plots

When a `projects/<slug>/` folder tracks a plot in the Garden, add
`resource: attn:seed/<plot-id>` to its `index.md` frontmatter. When the plot is
harvested, move anything lasting into `areas/`, then move the folder under
`archive/`. attn doesn't move it for you.

## File format

A knowledge base page starts with:

    ---
    type: note
    title: Notebook is filesystem-canonical
    summary: The .md files on disk are the source of truth.
    sources:
      - /journal/2026-06-18.md
    ---

- `type:`, `summary:` and `sources:` are required, and you choose the `type:`
  value. `title`, `tags`, `created` and `updated` are
  recommended. Keep keys you don't recognize. `index.md` files have no
  frontmatter, except a project folder's `resource:` line.
- `sources:` lists where the page's facts came from, as journal paths such as
  `/journal/2026-06-18.md` or as URLs. A fact with nothing to cite belongs in
  the journal, not the knowledge base.
- Link with root-absolute markdown, `[label](/knowledge/areas/foo.md)`, not
  `[[wikilinks]]`. Say the kind of link, such as supersedes or relates to, in
  the sentence.
- Keep pages short. Future agents read them under a token budget, so prefer a
  tight summary and a source over a transcript.
