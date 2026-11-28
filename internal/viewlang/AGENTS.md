# viewlang

- `catalog/catalog.go` lists the view language's parts with their arguments, slots, keys and reports, and its data sets, store types, store commands, formats and commands. The checker and `cmd/viewgen` read only this list. `make generate-types` runs `cmd/viewgen`, which writes from it the wire types (`internal/protocol/schema/view.tsp`), the reference agents read (`internal/viewlang/reference.md`) and the app's table of parts (`cli/protocol/src/view_parts.rs`).
- `catalog` imports nothing from attn, so `cmd/viewgen` builds while `internal/protocol` does not.
- Each part's entry names its example in the `views` folder of the current language's fixtures, `testdata/v<Language>/views`. The reference shows the example, and the corpus test checks it. When `Language` rises, copy the folder to the new `v<N>` and point the `viewgen` line of `generate-types` at it.
- The files under `testdata/v<N>` are views that a later attn reads with the same meaning. Reword a fixture's `.md` only when its meaning stays the same. A change of meaning raises `Language` and ships a rewriter from the old form.
- `go test ./internal/viewlang -update` rewrites each fixture's expected output. Read the diff before you commit it.
- The app's wire test `native/crates/attn-app/tests/wire/views.rs` reads `every-static-part.json`, `commands.json` and `question.json` and names parts by their index and line. Update it when you change those fixtures.
