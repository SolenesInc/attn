# Edit product prompts

attn embeds all prompts in the binary. During development, change prompts with the prompt editor and the workflow below.
`attn prompts` inspects the catalog embedded in the installed binary.

## Assess the instructions before editing

Follow the workflow in `cmd/prompt-editor/authoring.md` for every
prompt change. `context` returns that workflow with the complete relevant
instructions. Start `context` from an event, a source, a scenario or a
situation:

```sh
go run ./cmd/prompt-editor context crew/priming --include crew/new-session --base main --json
go run ./cmd/prompt-editor context --scenario crew-v2-chief-first-day --json
go run ./cmd/prompt-editor context --situation crew-member-start --json
```

Use `context` to understand the full scope of a change before you make it.

Use `list --json` to discover events, `inspect RECIPIENT/EVENT --json` for one
definition and its Go declarations, and `uses FRAGMENT_OR_PATH --json` to explore
consumers. `authoring` prints the workflow alone. Use `help` for syntax and
`--repo PATH` for another checkout.

## DSL rules

See ../../internal/prompts/model.go and neighboring definitions for
examples. Put branching in Go. Markdown accepts `{{name}}` substitutions.

| Construct | Behavior |
| --- | --- |
| `Use(id, path, bindings...)`, `Bind(name, node)` | `Use` names a fragment, and `Bind` binds its placeholders. |
| `TextField`, `FlagField`, `Input` | `TextField` and `FlagField` declare inputs, and `Input` inserts a literal value once. Template-looking input stays literal. |
| `ProducedBy(field, "recipient/event")` | Identify an input's producer for inspection and scenario bindings. |
| `When`, `Choose`, `Present`, `Enabled` | Select branches. Blank text is absent. Omitted flags are false. Selected inputs need a value, which may be empty. |
| `Compose` | Joins nonempty results with a blank line. |
| `Join(separator, ...)` | Preserves empty positions and uses the explicit separator. |
| `Trim`, `Trimmed`, `Quoted` | `Trim` trims a result, `Trimmed` trims a field, and `Quoted` quotes an input using Go string syntax. |
| `Exact`, `Document` | Ordinary sources lose one final newline. `Exact` keeps it. `Document` also disables substitution. |
| `Part(node, marker, index)` | Selects a side of a required marker before substitution. |

Validation checks all branches for missing files, unused or undeclared bindings,
conflicting fragment IDs, and invalid input types.

## Add scenarios and compare

Save representative inputs in `internal/prompts/scenarios/ID.json`. `values`
supplies literal strings. `inputs` maps a field to a named producer scenario.

A situation is what one agent receives, in delivery order, such as a crew
member's start. Save it in `internal/prompts/situations/ID.json` as the list of
scenarios whose events arrive one after another. Text that reaches the agent
inside another event, such as `crew/priming` inside `session/launch`, is not a
scenario of the situation: bind it through the outer scenario's `inputs`.

```json
{
  "version": 1,
  "id": "crew-member-start",
  "description": "What a crew member with a charter and a predecessor letter receives when the user starts its session.",
  "scenarios": ["crew-launch", "garden-ready-many", "crew-v2-new-session"]
}
```

`situations` lists them.

```sh
go run ./cmd/prompt-editor compare --base main --json
```

Add `--scenario ID` to narrow `compare`. `check` validates the catalog, the
scenarios and the situations: each situation's id matches its filename, and each
scenario it names exists. `compare` defaults to the merge-base. Use `--mode tip`
to compare the selected revision itself.

After you edit, rerun `context` and read the full results against the intended
behavior. `context` renders the scenarios in its scope from the checkout and,
with `--base`, from that revision too.

## Show the user in the browser

When you present prompt changes, open the editor at the situation or event you
changed and give the user the exact URL with a sentence about what to inspect.
Explain the intended behavior and how the instructions now fit together.

```sh
go run ./cmd/prompt-editor show --situation crew-member-start --open
go run ./cmd/prompt-editor show crew/new-session --open
```

`show` prints the URL of a situation or an event, and `--open` opens the
browser. Prefer a situation: the page then shows everything the agent receives,
in delivery order. A checkout has one editor: starting another prints the
running editor's URL and exits. If `show` reports that no editor is running,
start `make prompt-editor` in a persistent terminal, then retry. Keep the
editor running while the user reviews.

The page shows the composed prompt as one document. Each fragment is the text a
definition takes from a source file, usually the whole file and sometimes one
side of a marker. The file's name is in the left gutter, and placeholders show
as chips with their values. A fragment that fills a placeholder, or a
producer's output such as `crew/priming`, sits in a block under its parent. A
branch the scenario does not deliver is a stub that opens to its fragments, and
a flag's stub flips the flag for the page without writing anything. Ctrl or Cmd
K opens another situation, scenario or event, or a source file's raw text, and
unsaved edits stay.

You and the user edit the same checkout files. Typing in a fragment edits its
file, Ctrl or Cmd S saves every unsaved file, and the browser warns before it
leaves a page with unsaved edits. When a file changes on disk, the page takes
the new text. If the page holds an unsaved edit of that file, it keeps both
edits, including edits on adjacent lines. When both change the same lines, the
fragment shows the text on disk with Keep mine and Use disk, and saving skips
the file until the user chooses. Keep mine keeps the user's text on those lines
and takes the disk's changes elsewhere in the file. Use disk takes the text on
disk. A change to the catalog, a scenario or a situation redraws the page and
keeps unsaved edits.

A change to the Go definitions redraws the page too, but the catalog still
holds the old composition. The page shows that composition under a warning, and
saving is refused. Refresh Go definitions in the warning regenerates the catalog
and redraws the page, keeping unsaved edits.

When a source file holds text the catalog refuses, such as an unbound
placeholder, no page opens, because the catalog validates every event together.
Each page shows the catalog's error, which names the event and the fragment but
not the file. Find the file in Ctrl or Cmd K by its path, fix its raw text and
save, and pages open again. When the Go definitions changed too, the warning
shows over the error, and its refresh may be what the page needs. When a Go
change needs a source changed with it, such as a renamed placeholder, edit that
source outside the page, then refresh: the page refuses to save it against the
old catalog.

The Diff button, or Ctrl or Cmd Shift D, shows each fragment's change against
the merge-base with the branch chosen beside it in the header. That is `main`
unless `serve --base REF` sets another. Unchanged fragments fold to their file
name; open one by clicking it or pressing Enter on it.

## Verify

CI runs the catalog and scenario checks, and the prompt and editor tests.
Locally, review the change with `compare` and `context`. To run the CI checks sooner, run `make check-prompts` and
`make test-prompt-editor`. Update
[compatibility fixtures](../../internal/prompttest/testdata/) only for
intentional wording changes. Never regenerate them to make tests pass.

When a change moves or cuts what agents are told, check that they still find
each capability. The [agent bench](../../scripts/agent-bench/README.md) starts
real Claude Code, Codex and Sigma sessions on a non-production instance, gives
each a plain request, and judges the end state: its probes pass when the agent
reaches the right command through the CLI or attn's tools, and its tasks pass on
the garden's end state. `bun scripts/agent-bench/instance/bench.ts --instance
<name> --tasks probes` runs the probes. It spends model tokens, so CI doesn't
run it. Add a probe when you add a capability to the attn map.

The agent bench tells you what agents find before a change lands. To
see what delegated agents did in real work after it landed, run
`node scripts/workflow-scorecard.mjs collect`, then
`node scripts/workflow-scorecard.mjs --since <date>`; its usage text lists the
other flags. For Builders and Orchestrators on every host and both harnesses,
the scorecard prints how many loaded the attn-workflow skill and opened a
principle file, how many Orchestrator confirmations followed a look at the
work, how many checks were rejected, how many seeds had a check reported twice,
and how many seeds review on main routed findings to. It reads only the session
ledger, transcripts and the garden, and calls no model. `collect` appends one
line per closed session to a history file in the Notebook, because harnesses
delete old transcripts, so run it at least every two weeks. To judge a prompt
change, compare a window before it with a window after it. The
[improve-workflow](../../.agents/skills/improve-workflow/SKILL.md) skill does
that every two weeks as a cycle: it judges the last change, reads the escapes
that `node scripts/workflow-scorecard.mjs escapes` lists, and proposes the next
change.
