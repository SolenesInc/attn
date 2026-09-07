# Collect evidence when a terminal stops accepting input

While the problem is happening, try a few keys in the affected terminal. Press
Cmd+K, search for **Create diagnostic report**, and press Enter. Review the
disclosure, choose whether to include recent visible output from any affected
panes, then save the report. attn writes a uniquely named `.attn-report.json`
file to Downloads by default. On Linux, use Ctrl+Shift+K.

You can also export the same evidence from another terminal:

```sh
attn debug input --tail 0 > attn-input-dump.jsonl
```

Send the report with the approximate time and anything you noticed just before
the problem. It includes app, daemon, native input, terminal, transport, and PTY
write evidence captured when you opened the action. It also includes saved
input evidence from before reopening the app. The report excludes terminal
output unless you select panes in the disclosure.

The CLI export is a smaller fallback. It reads local logs without contacting
the app or daemon. Collect it soon: input records share the rotating 8 MiB
terminal diagnostic log.

The records contain the app build, session/pane/runtime identifiers, actual
focus, composition start/end state, and key-handling decisions. A document key
event without a terminal decision points to focus or event routing; a
`composition_mismatch` records a normal browser key rejected by the terminal's
composition flag. These are observations, not automatic diagnoses.

The report correlates each observed input across the native window, document,
terminal handler, WebSocket transport, daemon, PTY write, and subsequent output.
Its conclusion for each journey identifies the last boundary with evidence;
successful PTY delivery still does not prove that the agent consumed the input.
Terminal write and paint times help distinguish input failure from output or
rendering failure. Records identify both the app launch and handler so a
remount is visible.

The always-on trace uses fixed-size memory rings and records key categories and
modifier flags without key values, composition text, clipboard contents, prompt
bodies, terminal text, raw logs, environment variables, or arbitrary error
messages. Collection is event-driven and schedules no polling timer. A report
records overwritten entries and other unavailable evidence instead of silently
omitting it.

Profiles select their own logs. Use the same `ATTN_PROFILE` as the affected app.
This command and the added evidence require a build containing input tracing;
older builds only provide `attn debug diagnostics` and `attn debug incidents`.
