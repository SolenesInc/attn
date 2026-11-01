# Collect evidence when a terminal stops accepting input

While the problem is happening, try a few keys in the affected terminal, then
run **Create diagnostic report** from the command palette (Cmd+Shift+K, or
Ctrl+Alt+K on Linux). Choose whether to include recent output from affected
panes, then save the report. attn saves the report in Downloads as
`.attn-report.json`.

Without the app, export the input log from another terminal, with
`ATTN_INSTANCE` set to the affected app's instance:

```sh
attn debug input --tail 0 > attn-input-dump.jsonl
```

Collect the log soon, because it rotates. Send either file with the approximate
time and anything you noticed just before the problem. Neither file contains key values,
clipboard contents, or terminal text unless you selected panes in the report.
