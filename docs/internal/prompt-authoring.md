# Edit product prompts

Follow `cmd/prompt-editor/authoring.md` for every prompt edit (run
`go run ./cmd/prompt-editor help` for full CLI flags, and inspect
`internal/prompts/model.go` for the Go composition DSL).

## Workflow

1. **Read full context before editing:**

   ```sh
   go run ./cmd/prompt-editor context crew/priming --include crew/new-session --base main --json
   go run ./cmd/prompt-editor context --situation crew-member-start --json
   ```

   Use `list`, `inspect RECIPIENT/EVENT`, `uses FRAGMENT_OR_PATH` and
   `situations` to find every consumer.
2. **Edit & compare:** keep branching in Go (`internal/prompts/`) and
   `{{name}}` substitutions in Markdown (`internal/prompts/content/`). Add
   representative scenarios in `internal/prompts/scenarios/<id>.json` and
   multi-event delivery sequences in `internal/prompts/situations/<id>.json`.
   Review the composed diff with:

   ```sh
   go run ./cmd/prompt-editor compare --base main --json
   ```

3. **Show the user in the browser:** when presenting prompt changes, open the
   editor at the changed situation or event and give the user the URL:

   ```sh
   go run ./cmd/prompt-editor show --situation crew-member-start --open
   ```

   If no editor is running for this checkout, start `make prompt-editor` in a
   persistent terminal first and keep it running while the user reviews.

## Verify

- Run `make check-prompts` and `make test-prompt-editor`.
- Update compatibility fixtures in `internal/prompttest/testdata/` only for
  intentional wording changes—never regenerate them just to make tests pass.
- **Capability discovery probes** (spends tokens; not in CI):
  `bun scripts/agent-bench/instance/bench.ts --instance <name> --tasks probes`
  (`scripts/agent-bench/README.md`). Add a probe when adding a capability to
  the attn map.
- **Real-session workflow scorecard** (reads local ledger/transcripts/garden,
  no model calls): `node scripts/workflow-scorecard.mjs collect`, then
  `node scripts/workflow-scorecard.mjs --since <date>`. Driven every two weeks
  by `.agents/skills/improve-workflow/SKILL.md`.
