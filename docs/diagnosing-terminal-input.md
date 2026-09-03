# Collect evidence when a terminal stops accepting input

While the problem is happening, try a few keys in the affected terminal. Press
Cmd+K, search for **Copy terminal input diagnostics**, and press Enter. Paste
the copied dump into a file or your bug report. On Linux, use Ctrl+Shift+K.

You can also export the same evidence from another terminal:

```sh
attn debug input --tail 0 > attn-input-dump.jsonl
```

Send the dump with the approximate time, affected agent, and whether paste and
new agent output still worked. Both exports include saved evidence from before
reopening the app. The CLI reads local logs without contacting the app or daemon.
Collect it soon: input records share the rotating 8 MiB terminal diagnostic log.

The records contain the app build, session/pane/runtime identifiers, actual
focus, composition start/end state, and key-handling decisions. A document key
event without a terminal decision points to focus or event routing; a
`composition_mismatch` records a normal browser key rejected by the terminal's
composition flag. These are observations, not automatic diagnoses.

`lastSend` records transport readiness. `lastProbe` and `lastReceipt` correlate
sampled PTY write acknowledgements; an acknowledgement proves a PTY write, not
that the agent consumed it. The terminal's last write and paint times help
distinguish input failure from output or rendering failure. Counters cover an
input handler's lifetime; `recent` retains its last 32 events. Records identify
both the app launch and handler so a remount is visible.

The trace records key categories and modifier flags, without key values,
composition text, clipboard contents, or terminal text. Routine activity is
sampled at most once every 30 seconds per handler. Focus changes and unusual
decisions trigger a record, with repeated reasons limited to once every 30
seconds. Collection uses events and schedules no polling timer.

Profiles select their own logs. Use the same `ATTN_PROFILE` as the affected app.
This command and the added evidence require a build containing input tracing;
older builds only provide `attn debug diagnostics` and `attn debug incidents`.
