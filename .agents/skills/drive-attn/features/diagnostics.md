# Diagnostics and repair

See why attn misbehaves and repair it: logs, incidents, the event bus,
database restore. A maintainer checks an instance with preflight, reads the
daemon log and the app's terminal logs, watches the event bus and the task
runner, and restores the database from a rotating backup. A user who hits a
stuck terminal saves a diagnostic report from the palette and hands it to
whoever maintains attn. `attn state explain` shows why one session has its
state ([agent-state.md](agent-state.md)).
`docs/internal/diagnosing-terminal-input.md` explains how to diagnose typing that stops working, using `attn debug input`.

## Sub-features

- `preflight`: one read-only check of tools, paths, routing, protocol and launch settings, naming the cause and the action for each failure.
- `daemon-log`: the instance's `daemon.log`, filtered by time and pattern.
- `frontend-logs`: the app's terminal incidents and input decisions. `attn debug ls` lists the files.
- `diagnostic-report`: a private report file saved from the palette, with optional recent terminal output.
- `event-bus`: the log's window, producers, consumers, lag and pin alarm. The CLI and **Settings → Event bus** can trim the log and disable and enable a consumer.
- `task-runner`: **Settings → Task runner** lists background tasks and offers Retry on a failed or dead one.
- `db-backup`: the daemon writes a rotating backup when it starts and every 6 hours, keeps the newest 12, and takes a snapshot before each migration.
- `db-restore`: swap a backup in while the daemon is stopped. The replaced database stays beside the restored `attn.db`.
- `migration-failure`: the app's screen after a data migration failed at startup, with the details to copy.
- `diag-endpoint`: the daemon opens a loopback pprof and `/debug/vars` endpoint when `ATTN_PPROF` is set, for `docs/internal/perf-testing.md`.

## How to get to it

### People

- Palette (`native/crates/attn-app/src/palette/commands.rs`): `create-diagnostic-report` ("Create diagnostic report") opens the report dialog (`native/crates/attn-app/src/diagnostic_report/`). In the dialog, Save report writes `attn-<time>.attn-report.json` into Downloads and the file manager opens on it.
- **Settings → Task runner** (`backgroundTasks`, `native/crates/attn-app/src/settings/tasks.rs`): background tasks, most recently updated first, each with its state, `kind:subject`, attempts and last error. A failed or dead task has Retry, and "Try again" shows when the list cannot load.
- **Settings → Event bus** (`eventBus`, `native/crates/attn-app/src/settings/bus.rs`): the log's size, producers, consumers with Disable or Enable (`settings.bus.consumer.<consumer>.switch`) and health. See [settings.md](settings.md) for opening the settings dialog.
- The migration failure screen (`native/crates/attn-app/src/migration_failure.rs`): "attn seems broken after migration", the failure's facts and "Copy details".
- A maintainer's shell: the verbs below, and `ATTN_PPROF=1` (or a port) in the environment of a daemon the run starts. The daemon then opens the loopback endpoint (`internal/config/config.go`, `internal/diag/diag.go`).

### Agents

- `attn preflight [--agent <name>] [--model <name>] [--effort <level>] [--app-path <path>] [--json]` (`cmd/attnd/preflight.go`).
- `attn debug ls|incidents|diagnostics|input|daemon-log` with `--tail`, `--grep` and, for the daemon log, `--since` (`cmd/attnd/debug.go`).
- `attn bus status [--json]`, `trim`, `disable <consumer>` and `enable <consumer>` (`cmd/attnd/bus.go`).
- `attn db restore [path|latest]` (`cmd/attnd/db.go`).
- No prompt attn sends names these verbs. `attn --help` lists them, and the repository's `CLAUDE.md` lists them for maintainers under Diagnostics.

### Triggers

- The daemon writes a backup when it starts and every 6 hours (`internal/daemon/backup.go`).
- The daemon raises a warning notification when an enabled consumer pins the log past `ATTN_BUS_PIN_ALARM_AGE`.
- The daemon raises a notification when a background task exhausts its retries.
- The app samples a terminal's input decisions to `terminal-diagnostics.jsonl`, which `attn debug input` reads: at once when the terminal refuses a key, has stopped or is gone, or the write finds no connection, once per terminal and reason every 30 s, and otherwise at most once per terminal every 30 s while the user types (`native/crates/attn-app/src/terminal/input_trace.rs`).
- The app logs a terminal input incident to `terminal-incidents.jsonl`, which `attn debug incidents` reads, when a key waits 250 ms or more between its native event and its handler, when a typed write's probed round trip to the daemon takes 250 ms or more, fails, or goes unanswered for 5 s; once per terminal and reason every 30 s (`native/crates/attn-app/src/terminal/latency.rs`).
- When a data migration fails at startup, the daemon leaves `migration-failure.json` in the data folder (`internal/daemon/migration_failure.go`) and the app opens on the migration failure screen (`native/crates/attn-app/src/migration_failure.rs`).

## Driving it

Preconditions:

- Use the baseline instance. The CLI steps need only the daemon. For the palette, settings and migration steps, also use the instance's app, launched as [../SKILL.md](../SKILL.md) says. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-diagnostics.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- For the report, launch the app with `RUNE_DIRS_ROOT=$P/rune` in its environment and make `$P/rune/downloads` first. Every app folder then lives under `$P`, Downloads included, so the report never reaches the real Downloads. Without the folder, the app asks where to save instead.

The report's `diagnostics.input` holds the app's input trace (`frontend`), each traced key's way to the daemon's write (`journeys`) and the input samples from `terminal-diagnostics.jsonl` (`historical`). The trace covers the keys typed in the main window and in every popped-out tile window, whichever window opens the report. The app collects no OS key events and no terminal or UI diagnostics, so the report names `diagnostics.input.native`, `diagnostics.terminal` and `diagnostics.ui` in `omissions` with the reason `not_collected_by_native_app`. The app writes no GPU surface records to the diagnostics stream.

- **Preflight.** With the daemon only, `./attn preflight --agent claude` prints the checks and `attn preflight: FAIL (2 failed, 0 warnings)`, then exits 1. The two `protocol.` checks fail because they need an installed app. With the app installed, `"$(./attn instance resolve --field appDaemon)" preflight --agent claude` reports `PASS`. `--json` prints the stable report.
- **Daemon log.** `./attn debug daemon-log --grep "daemon started"` prints `[<time>] INFO: daemon started`. `./attn debug daemon-log --since 10m --grep "database backup written"` names the backup taken at start. `./attn debug ls` prints `frontend debug dir:` with its files, then `daemon.log:` with its path and size.
- **App logs.** With the app open and a terminal used, `./attn debug diagnostics --tail 5` prints JSON lines, and `./attn debug input --tail 5` only the input decisions. Before the app writes a file, the reader exits 1 with `no such file:` and the path. `./attn debug incidents` does the same until the app records an incident.
- **Event bus from the shell.** `./attn bus status` prints `log: seq` with the window, the producers, then `CONSUMER  CURSOR  LAG  OLDEST UNREAD  ENABLED  FILTER` listing `garden-seed-bells`, the only consumer on the baseline (`analytics-mirror` also appears while usage analytics is on). `./attn bus disable garden-seed-bells` prints `consumer "garden-seed-bells" disabled`, and `./attn bus status --json` shows `garden-seed-bells` with `"enabled": false`. `./attn bus enable garden-seed-bells` prints `consumer "garden-seed-bells" enabled`. `./attn bus disable nope` exits 1 with `bus disable: no consumer named "nope"`. `./attn bus trim` prints `removed <n> event(s); log now holds` and the size.
- **Event bus in the app.** Press ⌘, (`ui.openSettings`) with `bridge key '{"chord":"mod+,"}'`, then `bridge press '{"target":{"key":"settings.nav.eventBus"}}'`. `bridge wait '{"target":{"key":"settings.bus.consumer.garden-seed-bells"}}'` answers. Press `settings.bus.consumer.garden-seed-bells.switch`: the row's name includes `Disabled`, and `./attn bus status --json` shows `"enabled": false`. Press it again to enable it.
- **Task runner.** Press `settings.nav.backgroundTasks`. `bridge tree` under `settings.tasks` lists the one-off tasks the daemon ran, most recently updated first, each as its state and `kind:subject`. Periodic duties such as the hourly vents prune are not listed, so a fresh instance shows `settings.tasks.empty`, `No background tasks.`, until a one-off task has run. A failed or dead task offers Retry.
- **Diagnostic report.** Close Settings with Escape. Add a shell agent placed on a desktop with `wsctl add-session --cwd "$P" --agent shell --label <label> --id <label> --place`, using a label no earlier run used. Wait for `sidebar.session.<label>`, press it, and wait for the tile, `{"role":"group","name":"<label>"}`. Type `echo REPORT-MARKER-$((40+2))` into the tile with `bridge type` and press Enter. Press ⌘⇧K (`ui.commandPalette`) with `bridge key '{"chord":"mod+shift+k"}'`, type `diagnostic report`, wait for `palette.row.command:create-diagnostic-report` and press Enter. `diagnostic-report-dialog` opens. The tile's pane is `diagnostic-report.pane.<paneId>`, a check box named for the tile and already checked, and `diagnostic-report.save`, "Save report", has focus. Press Enter: the dialog closes and `$P/rune/downloads` holds `attn-<YYYYMMDD-HHMMSS>Z.attn-report.json`. Its `consent.paneContent` is `selected_panes`, its `paneContent` entry for that pane has `REPORT-MARKER-42` in `text`, `daemons` holds the daemon's snapshot, and `build.instance` names the instance. Each typed key has a `diagnostics.input.journeys` entry with `terminalOutcome` `sent`, `transportReady` true, a `daemonWrite` whose `result` is `accepted` and the conclusion `pty_write_succeeded_no_agent_evidence`, and `./attn debug input --tail 0` prints the tile's sample, naming its pane. Outside `paneContent`, neither the report nor the sample holds the typed text. Run it again, press `diagnostic-report.clear` and save: `consent.paneContent` is `metadata_only` and `paneContent` is empty. Then run `bridge run '{"name":"tile.popOut"}'`, take the tile window's id from `bridge native_windows`, and type `echo POPPED-$((40+2))` and Return into it with `xdotool windowfocus --sync <id>`, `xdotool type --window <id> …` and `xdotool key --window <id> Return` (Linux; `bridge` input reaches only the main window). A report saved from the main window has a `sent` journey with an `accepted` `daemonWrite` for each of those keys, on the tile's runtime. Remove the agent with `wsctl rm-session --id <label>`.
- **Restore the database.** Once `./attn debug daemon-log --grep "database backup written"` names the backup taken at start, plant a seed: `./attn seed plant "Restore drive"` prints `<seed>`. `./attn db restore` exits 1 with `attn db restore: the attn daemon is running; stop it first`. Quit the app with `./attn instance stop-app` if it runs, then `./attn daemon stop` prints `stopped daemon (pid <n>)`. `./attn db restore` prints `restored attn.db from:` with a path under `<data-dir>/backups/`, `previous attn.db preserved as:` with `attn.db.pre-restore-<time>`, and how to start the daemon. `./attn daemon ensure` prints JSON with `"status": "started"`, and `./attn seed ls` no longer lists `Restore drive`.
- **A failed migration stops the app on its details.** Quit the app with `./attn instance stop-app` and stop the daemon with `./attn daemon stop`. Move `<data-dir>/attn.db` aside and make a folder named `attn.db` in its place. `./attn daemon ensure` now exits 1, and the daemon leaves `<data-dir>/migration-failure.json` with an error that reads `is a directory`. Launch the app: `bridge wait '{"target":{"key":"migration-failure"}}'` answers. `migration-failure.title` reads "attn seems broken after migration", and `migration-failure.facts` lists each fact as `<label>: <value>`, from "Daemon log" through "Failed at", then "Failure marker" with the marker's path. No What's new opens over it. The app makes no second try at starting the daemon.
- **Copy details.** Run `bridge press '{"target":{"key":"migration-failure.copy"}}'`: the button reads "Copied", and the clipboard holds "attn failed to migrate its data at startup." followed by one line per fact.
- **Repair.** Quit the app, remove the `attn.db` folder, move the database back, and delete `migration-failure.json`. Launch the app: it opens on home.
- **The diagnostics endpoint.** Quit the app if it runs (`./attn instance stop-app`), run `./attn daemon stop`, then start the daemon again with a port of your own that nothing else holds: `ATTN_PPROF=<port> ./attn daemon ensure` prints `"status": "started"`. `./attn debug daemon-log --grep "diagnostics endpoint listening"` shows `diagnostics endpoint listening on http://127.0.0.1:<port>/ (pprof + /debug/vars)`, `curl -s http://127.0.0.1:<port>/debug/vars` prints JSON that includes `worker_pids`, and `curl -s http://127.0.0.1:<port>/` lists the endpoints. Stop that daemon and run `./attn daemon ensure` without the variable to close the port.
- **Proof.** In `$P`, keep each `./attn` command with its stdout, stderr and exit code. Also keep `./attn bus status --json` from before and after each toggle, `bridge tree` with each screen open and after each switch press, `bridge shot '{"path":"<P>/event-bus.png"}'` with **Settings → Event bus** open, `migration-failure.png` and `diagnostic-report.png` the same way (where `<P>` is the folder's path written out), the clipboard's text, the report file, and `./attn seed ls` from before and after the restore. Name each file for its way in, such as `diagnostics-bus-cli-disable.txt`.

## Contexts

- **Durability across restarts**: the daemon takes a backup at every start and every 6 hours and keeps the newest 12 backups, and keeps the pre-migration snapshots as well. A restore keeps the database it replaces. A restore also pins again each conversation archive that the restored backup records. The daemon stores a consumer's cursor and enabled bit, so a disabled consumer stays disabled across restarts. `daemon.log` is kept across restarts, and the daemon trims it once it grows past 20 MiB.
- **Linux**: `attn preflight`, `attn debug`, `attn bus` and `attn db` work the same where the daemon and CLI run on Linux. The report and the settings sections are in the app. On Linux, `ui.commandPalette` is Ctrl+Alt+K and `ui.openSettings` is Ctrl+Shift+,.
- **Outposts**: `attn debug`, `attn bus` and `attn db` act on the machine they run on: an outpost's own log, event bus and database, never the home's.
- **Profiles**: logs, the event bus, tasks and backups belong to the daemon and cover every profile.
- **Performance and idle cost**: `attn bus status` names the loudest producers, the consumer at the retention floor and the consumer that pins the log, and **Settings → Event bus** shows the same facts. `ATTN_PPROF` and `docs/internal/perf-testing.md` cover memory and CPU.
- **Keyboard and native input**: the palette opens the report dialog, the "Save report" button has focus so Enter saves, and Escape closes the dialog. Tab moves from Save report to the dialog's Close, Clear, each pane's check box and Cancel.
- **Compatibility across versions**: `attn db restore <path>` accepts a pre-migration snapshot, `attn-premigration-<version>-<timestamp>.db`, to go back to the data before a migration.

## Tests

- Wire: `native/crates/attn-app/tests/wire/migration_failure.rs`, `native/crates/attn-app/tests/wire/diagnostic_report.rs`, `native/crates/attn-app/tests/wire/settings/bus.rs`, `native/crates/attn-app/tests/wire/settings/tasks.rs`, `internal/daemon/bus_status_wire_test.go`, `internal/daemon/bus_health_wire_test.go`, `internal/daemon/bus_pin_alarm_wire_test.go`, `internal/daemon/daemon_warnings_wire_test.go`, `internal/daemon/diag_wire_test.go`, `internal/daemon/support_snapshot_wire_test.go`, `internal/daemon/backup_wire_test.go`, `internal/daemon/backup_rotation_wire_test.go`, `internal/daemon/tasks_wire_test.go`, `internal/daemon/tasks_protocol_wire_test.go`, `internal/daemon/tasks_restart_wire_test.go`.
- Stack: `cmd/attnd/debug_stack_test.go`, `cmd/attnd/preflight_stack_test.go`, `cmd/attnd/db_stack_test.go`, `cmd/attnd/bus_stack_test.go`.
- Kernel: `cmd/attnd/bus_test.go`, `internal/daemon/bus_pin_alarm_test.go`, `internal/bus/*_test.go`, `internal/jobs/*_test.go`.

## Gotchas

- `attn bus` and `attn db` open the database file directly and `attn debug` reads files on disk, so they act on whatever instance the shell selects, daemon or not. Run them only in the instance shell. Without an instance they read and change production. Each prints the `[attn instance=<name> ...]` banner on stderr. If you see no banner, stop.
- `attn db restore` needs the daemon stopped, and an open app starts its daemon again. Quit the app first.
- A restore brings back the newest backup, so anything since that backup is gone. The replaced database stays beside it as `attn.db.pre-restore-<time>`.
- Disabling `garden-seed-bells` stops seed updates until it is enabled again. Enable every consumer you disabled before the run ends.
- `attn bus status` reads the database, not the daemon. It cannot show whether a consumer's delivery is running, and it says so.
- `ATTN_BUS_PIN_ALARM_AGE` and `ATTN_BUS_RETENTION` change the daemon and `attn bus` separately. Set them for both, or the table and the notification disagree. On a young instance a trim removes little, since the default window is 30 days.
- The diagnostic report goes to the real Downloads folder unless `RUNE_DIRS_ROOT` moves it, and the file manager opens on it. The report may contain terminal output the user ticked. Check it for private text before sharing.
- `attn debug incidents`, `diagnostics` and `input` read the app's files: on a daemon-only instance they exit 1 with `no such file:`.
- The migration failure screen needs a migration that really failed. The recipe makes one by putting a folder where `attn.db` was, which fails the start. You reach Retry and the failed-task notification only when a task has failed or died. If **Settings → Task runner** shows none, report them as not reached.
