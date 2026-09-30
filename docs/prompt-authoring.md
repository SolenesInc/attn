# Edit product prompts

We embed all prompts into the binary, and have an editor and workflow for making changes during development.
`attn prompts` inspects the catalog embedded in the installed binary.

## Assess the instructions before editing

Follow the workflow in `cmd/prompt-editor/authoring.md` for every
prompt change. `context` returns that workflow with the complete relevant
instructions. Start from an event, source, scenario or shared draft:

```sh
go run ./cmd/prompt-editor context crew/priming --include crew/wake --base next --json
go run ./cmd/prompt-editor context --scenario chief-with-handoff --json
go run ./cmd/prompt-editor context --draft DRAFT_ID --json
```

Work with the tool to understand the full scope of the change you intend to make.

Use `list --json` to discover events, `inspect RECIPIENT/EVENT --json` for one
definition and its Go declarations, and `uses FRAGMENT_OR_PATH --json` to explore
consumers. `authoring` prints the workflow alone. Use `help` for syntax and
`--repo PATH` for another checkout.

## DSL rules

See ../internal/prompts/model.go and neighboring definitions for
examples. Put branching in Go; Markdown accepts `{{name}}` substitutions.

| Construct | Behavior |
| --- | --- |
| `Use(id, path, bindings...)`, `Bind(name, node)` | Name a fragment and bind its placeholders. |
| `TextField`, `FlagField`, `Input` | Declare inputs and insert literal values once. Template-looking input stays literal. |
| `ProducedBy(field, "recipient/event")` | Identify an input's producer for inspection and scenario bindings. |
| `When`, `Choose`, `Present`, `Enabled` | Select branches. Blank text is absent; omitted flags are false. Selected inputs need a value, which may be empty. |
| `Compose` | Join nonempty results with a blank line. |
| `Join(separator, ...)` | Preserve empty positions and use the explicit separator. |
| `Trim`, `Trimmed`, `Quoted` | Trim a result, trim a field, or quote an input using Go string syntax. |
| `Exact`, `Document` | Ordinary sources lose one final newline. `Exact` keeps it; `Document` also disables substitution. |
| `Part(node, marker, index)` | Select a side of a required marker before substitution. |

Validation checks all branches for missing files, unused or undeclared bindings,
conflicting fragment IDs and invalid input types.

## Add scenarios and compare

Save representative inputs in `internal/prompts/scenarios/ID.json`. `values` supplies literal strings. `inputs` maps a field to a named producer
scenario.

```sh
go run ./cmd/prompt-editor compare --base next --json
```

Add `--scenario ID` to narrow `compare`. `check` validates the catalog and scenarios. Comparison defaults to merge-base;
use `--mode tip` for the selected revision itself.

Rerun `context` after edits and read the full results against the intended behavior.
It evaluates the selected dataset's scenario inputs against both revisions.
Shared context uses its pinned base unless `--base` selects another comparison.

## Share work with a maintainer

Edit checkout files directly for independent work. Use a shared draft when a
maintainer and agent need to edit together. Drafts and reviews persist in this
checkout's ignored `.prompt-editor/` directory.

```sh
go run ./cmd/prompt-editor draft create --title 'Clarify wake instructions' --json
go run ./cmd/prompt-editor context crew/wake --draft DRAFT_ID --json
go run ./cmd/prompt-editor draft put DRAFT_ID content/crew/wake.md \
  --file /tmp/wake.md --expect SOURCE_REVISION --author agent
go run ./cmd/prompt-editor draft focus DRAFT_ID --scenario crew-wake --base next
go run ./cmd/prompt-editor check --draft DRAFT_ID
go run ./cmd/prompt-editor compare --draft DRAFT_ID --base next --json
go run ./cmd/prompt-editor context --draft DRAFT_ID --base next --json
go run ./cmd/prompt-editor draft get DRAFT_ID --json
```

Use `sources[PATH].current.revision` from `context` for `--expect`. Use the latest
draft revision from `draft get` for either operation:

```sh
go run ./cmd/prompt-editor draft apply DRAFT_ID --revision N
go run ./cmd/prompt-editor review create --draft DRAFT_ID --revision N --json
```

`apply` validates and writes all draft Markdown. A review freezes definitions,
sources, scenarios, selected inputs and the comparison commit. Creating a review
or adding feedback advances the parent draft revision; reread it before applying.

```sh
go run ./cmd/prompt-editor context --review REVIEW_ID --json
go run ./cmd/prompt-editor review get REVIEW_ID --json
go run ./cmd/prompt-editor watch --review REVIEW_ID --after 0 --timeout 30s --json
```

Read feedback with `review get`; add it with `review comment`. `watch --after N`
counts review comments or, with `--draft`, uses the draft revision. A timeout
returns `changed: false`.

## Show the user in the browser

When presenting prompt changes, open the relevant draft or review and give the
user its exact URL with a sentence about what to inspect. Use a draft for live
editing or a review for feedback tied to fixed text.

Explain the intended behavior and how the instructions now fit together. Use
the full source and composed text beside the diff to review the overall result.

Reuse the editor for this checkout. If `show` reports that none is running,
start `make prompt-editor` in a persistent terminal, then retry. Keep it running
while the user reviews. Open the appropriate view:

```sh
go run ./cmd/prompt-editor show --draft DRAFT_ID \
  --scenario crew-wake --source content/crew/wake.md --base next --open
go run ./cmd/prompt-editor show --review REVIEW_ID --open
```

`show` prints the URL; `--open` opens the browser. For a review, set the draft's
scenario, source and base with `draft focus` before capturing it. The review
opens that frozen selection. Existing draft tabs follow navigation only when the
user enables **Follow shared focus**. Read comments with `review get` or `watch`.

## Verify

CI runs the catalog and scenario checks, prompt and editor tests, and the
`prompt-composition` delivery scenario. Locally, `compare` and `context` are
the review; `make check-prompts` and `make test-prompt-editor` run the CI checks
when you want them sooner. Update [compatibility fixtures](../internal/prompttest/testdata/)
only for intentional wording changes; never regenerate them to make tests pass.
