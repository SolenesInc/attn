References that agents read through attn's tools no longer misrender four inputs, and two tools take the argument names agents send.

- A code-block line that is not an attn command stays as written: `git push --force` keeps its flag.
- A command with flags only the CLI takes renders as the tool call and says what it leaves out: `terminal_run` with {…} without `--wait --timeout 600` (CLI only). Of the shipped texts only wrap-up's `presence` line gains this.
- A repeated flag is one list: `"evidence": ["a.txt","b.txt"]`.
- The seed guide's `seed_plot` example shows its payload as an object slot, and the wire test calls the tool with it. The old text was refused (bug 3506486).
- `seed_search` takes `query` (was `words`) and `journal_append` takes `text` (was `entry`), one name each. The CLI's spelling and text do not change; a new `cli_name` in the catalog keeps the word its usage refusal prints. A caller that still sends the old name is refused and reads the schema.

`seed_harvest`, `agent_close` and `skill` keep their names: no single name is guessed for the first, and rendering fixed the second.
