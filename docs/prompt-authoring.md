# Edit product prompts

Use the checkout CLI from the repository root. It works without a daemon or
browser. `attn prompts` inspects the catalog embedded in the installed binary.

## Inspect before editing

```sh
go run ./cmd/prompt-editor list --json
go run ./cmd/prompt-editor inspect session/launch --json
go run ./cmd/prompt-editor inspect --scenario chief-with-handoff --json
go run ./cmd/prompt-editor uses content/crew/wake.md --json
```

`inspect` returns Markdown sources, revisions, inputs, composition and Go
locations. Supply `--scenario ID`, `--set name=value` or `--values FILE` to render.
`uses` follows producer links to indirect consumers. Use `help` for command syntax
and `--repo PATH` to select another checkout.

Trace the daemon or harness adapter when checking delivery. A preview shows what
the definition composes; `available_skill` and `reference` do not prove loading.
Charters, automation tasks, external skills and harness instructions remain inputs.

## Edit sources and definitions

- Edit prose in [`internal/prompts/content`](../internal/prompts/content).
- Edit recipients, events, inputs, conditions and ordering in
  [`internal/prompts/*.go`](../internal/prompts). Keep fragment IDs stable when
  moving files. Adapters retain serialization, iteration, byte limits and delivery.
- After Go changes, run `go run ./cmd/prompt-editor refresh`. It compiles the
  checkout's generator and reloads definitions. Stale definitions block inspection
  and apply. Existing drafts need `draft sync ID --revision N` after refresh.
- After Markdown changes, run `make generate-prompts`. Include changed generated
  files with the source changes:
  `internal/prompts/catalog.generated.json`,
  `plugins/attn-pi/automode/prompts.generated.json`, and
  `app/src/prompts/catalog.generated.json`.

Go embeds Markdown; Pi and the frontend bundle generated catalogs. Rebuild the
consuming artifacts to use saved changes at runtime. Increment
`prompts.ManifestVersion` when changing serialized DSL structure or rendering
semantics. Extend the TypeScript renderer and parity tests before exporting new
DSL constructs to Pi or the frontend. User overrides are not implemented.

## DSL rules

See [`model.go`](../internal/prompts/model.go) and neighboring definitions for
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

Save representative inputs in `internal/prompts/scenarios/ID.json`; include them
in Git. Start from [chief-with-handoff.json](../internal/prompts/scenarios/chief-with-handoff.json).
The schema has `version: 1`, `id`, `recipient`, `event`, `values`, optional
`description` and optional `inputs`.

`values` supplies literal strings. `inputs` maps a field to a named producer
scenario, for example `"crew_priming": "crew-with-handoff"`. Each binding must
match the field's `ProducedBy` declaration. A field cannot appear in both maps;
cycles are rejected. The id must match the filename.

```sh
go run ./cmd/prompt-editor check
go run ./cmd/prompt-editor compare --base next --json
```

Add `--scenario ID` to narrow either command. Comparison defaults to merge-base;
use `--mode tip` for the selected revision itself. It reads Git without fetching
or switching branches. Each side uses its own definitions and sources, including
producer output. Check `unavailable` before claiming composed parity: bases with
missing, unsupported or invalid manifests permit source comparison only.

## Share work with a maintainer

Edit checkout files directly for independent work. Use a shared draft when a
maintainer and agent need to edit together. Drafts and reviews persist in this
checkout's ignored `.prompt-editor/` directory.

```sh
go run ./cmd/prompt-editor draft create --title 'Clarify wake instructions' --json
go run ./cmd/prompt-editor inspect crew/wake --draft DRAFT_ID --json
go run ./cmd/prompt-editor draft put DRAFT_ID content/crew/wake.md \
  --file /tmp/wake.md --expect SOURCE_REVISION --author agent
go run ./cmd/prompt-editor draft focus DRAFT_ID --scenario crew-wake --base next
go run ./cmd/prompt-editor check --draft DRAFT_ID
go run ./cmd/prompt-editor compare --draft DRAFT_ID --base next --json
go run ./cmd/prompt-editor draft get DRAFT_ID --json
```

Use the source revision from `inspect` for `--expect`. Use the latest draft
revision from `draft get` for either operation:

```sh
go run ./cmd/prompt-editor draft apply DRAFT_ID --revision N
go run ./cmd/prompt-editor review create --draft DRAFT_ID --revision N --json
```

`apply` validates and writes all draft Markdown. A review freezes definitions,
sources, scenarios, selected inputs and the comparison commit. Creating a review
or adding feedback advances the parent draft revision; reread it before applying.

```sh
go run ./cmd/prompt-editor inspect --review REVIEW_ID --json
go run ./cmd/prompt-editor review get REVIEW_ID --json
go run ./cmd/prompt-editor watch --review REVIEW_ID --after 0 --timeout 30s --json
```

Read feedback with `review get`; add it with `review comment`. `watch --after N`
counts review comments or, with `--draft`, uses the draft revision. A timeout
returns `changed: false`.

Source/scenario writes use `--expect HASH`; draft apply, sync, archive and restore
use `--revision N`. Conflicts exit 3, invalid requests exit 2, failed scenario
checks exit 1. On conflict, inspect the current state and reconcile before
retrying. `draft reset` discards one edit; use it to adopt a changed checkout file
before reapplying the intended edit. `draft archive` is reversed by `draft restore`.

## Show the user in the browser

When presenting prompt changes, open the relevant draft or review and give the
user its exact URL with a sentence about what to inspect. Use a draft for live
editing or a review for feedback tied to fixed text.

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

## Verify before finishing

- Run `make check-prompts`, `go test ./internal/prompts` and tests for affected
  runtime adapters. Check representative scenarios and their base comparisons.
- For Pi/frontend prompts, run the affected prompt tests. Preserve
  [compatibility fixtures](../internal/prompttest/README.md) during refactors;
  explain and update specific expectations for intentional wording changes.
  Do not regenerate them from the new renderer just to make tests pass.
- For editor changes, run `make test-prompt-editor`. It needs app dependencies
  and Playwright Chromium. Set `PROMPT_EDITOR_ARTIFACTS` to retain recordings.
- For delivery changes, follow [profiles.md](profiles.md#verification-requirements)
  and run affected packaged-app scenarios, including `prompt-composition`.
  Rendering tests alone do not verify delivery or model behavior.
