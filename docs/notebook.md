# Notebook and workspace context

## Workspace context

The workspace's agents and user keep their current account of the work in
`context.md`: Area, Current Picture, Decisions, and Constraints. Agents update
it with information that should survive their own session. Its claims can be
stale or unverified.

`internal/store/workspace_context.go` owns this SQLite-backed record. The
keeper compacts it when it grows past a size threshold, and removing the
workspace deletes it. The Notebook holds the lasting record.

## The Notebook

attn's durable, profile-wide markdown store. Files on disk are authoritative,
and survive sessions, workspaces, and PRs. Both daemon WebSocket writes and
native file edits update the same tree.

The Notebook contains the journal, the knowledge base, and the machine-internal
raw tier that supplies the keeper's inputs.

## The journal

The curated account of work across workspaces, stored as `journal/<date>.md`
in the Notebook. It records decisions, shipped work, fixes, dead ends, and
lessons for later recall or performance review. An important event belongs
even if it happens only once.

| Writer | Contribution |
| --- | --- |
| The keeper | Each workspace's account of its work |
| The chief of staff | Decisions, delegations, and progress across workspaces |
| The user | Direct edits, corrections, and curation |

Other agents may write journal entries, but attn does not ask or nudge them to
do so. Automated capture is the keeper's job. Raw machine inputs stay in the
[raw tier](#the-raw-tier); the keeper turns them into journal prose.

## Knowledge base

The Notebook's `knowledge/` tree holds decisions, gotchas, and domain knowledge
worth keeping beyond a PR. Its PARA directories are `projects/`, `areas/`,
`resources/`, and `archive/`. `knowledge/index.md` and an `index.md` in each
PARA directory provide navigation.

A note carries frontmatter such as `type: note`. The type vocabulary is open;
the store does not validate a closed set. The chief and user maintain notes
through daemon writes or native file edits.

Knowledge notes describe what is known. Dated events belong in the journal and
unfinished work belongs in the Garden. Chief-authored notes cite resolvable
`sources:`, such as journal anchors or URLs. The user may organize their own
notes as they wish.

## Note title

A note's title is its first `# H1` heading. `Document.Title()` finds the first
level-1 ATX heading outside fenced code, falling back to the filename when
none exists. The filename remains the stable address used by links.

attn ignores frontmatter `title:`. Frontmatter properties such as `type`,
`summary`, `tags`, `sources`, and dates appear in the editor's properties card.
Journals use `# <date>` and the chief inbox uses `# Chief inbox` as their body
titles, with no frontmatter title.

## The keeper

The automated entity that maintains each workspace's context and narrates its
work into the journal. It preserves the account before pruning or compacting
`context.md`.

On final workspace removal, the keeper also moves the workspace's linked
`knowledge/projects/<slug>/` folder into `knowledge/archive/`. The folder's
`index.md` identifies the link with `resource: attn:workspace/<id>`. The chief
decides which knowledge should move into `areas/`.

The durable runner (`internal/tasks`) implements these duties with
`compact_context`, `summarize_session`, and `narrate_workspace`. A cheaper
summarize step produces session digests for a strong-tier narration agent.
These task kinds all belong to the keeper; they do not introduce separate
janitor, narrator, or summarizer roles.

## The chief of staff

The operator responsible for work across workspaces. Its durable home is the
Notebook. It journals decisions, delegated work, and changes across workspaces;
the keeper supplies each workspace's own narrative.

## The raw tier

Machine capture inside the Notebook's `.attn/raw/` directory supplies the
keeper's inputs:

- `sessions/<wsID>/<sessionID>.md` holds session digests. Sessions without a
  workspace use `sessions/_solo/<sessionID>.md`.
- `context-snapshots/<wsID>.md` holds the workspace-context snapshot taken
  synchronously when the workspace is removed.

The user-facing Notebook APIs cannot reach this tier because `CleanPath`
rejects dot-directory segments. Capture runs deterministically even if the
keeper never completes narration; the raw record remains available.
