# Present reviews

Present opens a guided code review inside attn. It is not a GitHub PR flow.
The agent writes a YAML manifest with a stable git frame, reading order, notes,
and optional inline annotations.

Use Present when the reviewer needs more than a raw diff. Skip it for small,
mechanical changes unless a short tour would expose a real risk.

## Workflow

1. Read the full diff. Read the head version of every file you plan to annotate
   and any plan or design doc that explains the change.
2. Decide the reviewer's job. For a normal branch review, the default is
   "decide whether this is safe to merge." State a different goal in the
   summary when needed.
3. Order files by concept, then behavior, then wiring. Adapt that order to the
   change.
4. Write `.present.yml`, validate it, and fix every error or ambiguous anchor.
5. Open Present with `--wait`. Continue from the returned feedback.

## Manifest

`.present.yml` is the default path. `--manifest <path>` selects another file.
The schema is version 1 and rejects unknown fields.

```yaml
version: 1
kind: changes
title: Reconcile slice classifier
frame:
  repo: /Users/user/projects/attn
  base: main
  head: feat/reconcile-slice-classifier

summary: >
  Separates transcript slicing from classification. Read the boundary first,
  then the classifier that trusts it.

files:
  - path: internal/reconcile/slice.go
    note: >
      This file owns the cutoff. Everything after it assumes future turns
      cannot enter the slice.
    annotations:
      - anchor: "func SliceTranscript"
        thread:
          - "Cuts at tool-result boundaries so a tool call stays intact."
          - "Timestamp ordering was rejected because resumed transcripts are not monotonic."

skip:
  - internal/reconcile/testdata/golden.json
```

Required fields:

- `version: 1`, `kind: changes`, and `title`.
- `frame.repo`, an absolute path.
- `frame.base` and `frame.head`, each a branch, tag, or SHA. Present pins both
  to SHAs when it opens.

Optional fields:

- `summary`, rendered as markdown before the file tour.
- `files`, in review order. Each entry has a relative `path`, optional markdown
  `note`, and optional `annotations`.
- `skip`, for changed files the reviewer can safely leave until last.

Changed files omitted from both lists appear under Other. `files` paths cannot
repeat, escape the repo, or also appear in `skip`.

Each annotation needs one location and one body:

- Location: `anchor`, `line`, or `start` plus `end`. Lines are 1-based and
  refer to the head file. An anchor is a substring of at least three characters.
- Body: `note` or `thread`. A thread is an ordered list of non-empty comments.

Prefer a unique `anchor` because it survives edits. Present uses the first
match, so make it specific. Static line ranges cannot overlap. Present has no
`view` field or jaunt-style `{author, body}` threads.

## Write for the reviewer

- Explain why a file or line matters. The diff already shows what changed.
- Keep the summary short. Name the change, the reading path, and any non-default
  review goal.
- Tour only files that carry the design. Put generated files, lock files, and
  large fixtures in `skip` when naming them prevents confusion.
- Use annotations for invariants, tradeoffs, and decisions tied to a specific
  line. Use `thread` when a rejected alternative needs its own comment.
- Use plain engineering prose. Assume the reviewer knows standard patterns.
  Backtick symbols and flags.

Markdown renders in summaries, notes, and annotation bodies. Use `>` for
wrapped prose and `|` when line breaks matter, such as a list or Mermaid block.
Add a diagram only when it makes relationships or ordering clearer. The
validator does not parse Mermaid syntax.

## Validate and open

```bash
attn present validate --manifest .present.yml
```

Validation checks the schema, refs, line ranges, and annotation anchors. Do not
open the review until it passes without ambiguous anchors.

```bash
attn present --manifest .present.yml --wait
```

Codex must use `--wait` and keep the command in the foreground. The command
returns when the reviewer submits or closes the round and prints the result.

Read feedback later with:

```bash
attn present feedback <presentation-id> [--round <n>] [--json]
```

After making requested changes, open another round on the same presentation:

```bash
attn present --manifest .present.yml --presentation <presentation-id> --wait
```

Write the new round as the delta from the last one. Do not make the reviewer
walk the original tour again.
