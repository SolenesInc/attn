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
- `frontend-logs`: the app's terminal incidents, lifecycle diagnostics and input decisions. `attn debug ls` lists the files. The lifecycle stream's `surface` records log each change to a pane's GPU surface (renderer created or destroyed, canvas resized, drawing buffer released or restored, glyph atlas grown).
- `diagnostic-report`: a private report file saved from the palette, with optional recent terminal output.
- `event-bus`: the log's window, producers, consumers, lag and pin alarm. The CLI and **Settings → Event bus** can trim the log and disable and enable a consumer.
- `task-runner`: **Settings → Task runner** lists background tasks and offers Retry on a failed or dead one.
- `db-backup`: the daemon writes a rotating backup when it starts and every 6 hours, keeps the newest 12, and takes a snapshot before each migration.
- `db-restore`: swap a backup in while the daemon is stopped. The replaced database stays beside the restored `attn.db`.
- `migration-failure`: the app's screen after a data migration failed at startup, with the details to copy.
- `diag-endpoint`: the daemon opens a loopback pprof and `/debug/vars` endpoint when `ATTN_PPROF` is set, for `docs/internal/perf-testing.md`.

## How to get to it

### People

- Palette (`app/src/application/useAppCommands.tsx`): `create-diagnostic-report` ("Create diagnostic report") opens the report dialog (`app/src/components/DiagnosticReportPrompt.tsx`). In the dialog, Save report writes `attn-<time>.attn-report.json` into Downloads and shows the file in Finder.
- **Settings → Task runner** (`backgroundTasks`, `app/src/components/BackgroundTasksSettings.tsx`): background tasks, most recently updated first, each with its state, `kind:subject`, attempts and last error. A failed or dead task has Retry, and "Try again" shows when the list cannot load.
- **Settings → Event bus** (`eventBus`, `app/src/components/EventBusSettings.tsx`): the log's size, producers, consumers with Disable or Enable (`[data-testid="bus-consumer-toggle-<consumer>"]`) and health. See [settings.md](settings.md) for opening the settings dialog.
- The migration failure screen (`app/src/components/MigrationFailureScreen.tsx`): "attn seems broken after migration", the failure's facts and "Copy details".
- A maintainer's shell: the verbs below, and `ATTN_PPROF=1` (or a port) in the environment of a daemon the run starts. The daemon then opens the loopback endpoint (`internal/config/config.go`, `internal/diag/diag.go`).

### Agents

- `attn preflight [--agent <name>] [--model <name>] [--effort <level>] [--app-path <path>] [--json]` (`cmd/attn/preflight.go`).
- `attn debug ls|incidents|diagnostics|input|daemon-log` with `--tail`, `--grep` and, for the daemon log, `--since` (`cmd/attn/debug.go`).
- `attn bus status [--json]`, `trim`, `disable <consumer>` and `enable <consumer>` (`cmd/attn/bus.go`).
- `attn db restore [path|latest]` (`cmd/attn/db.go`).
- No prompt attn sends names these verbs. `attn --help` lists them, and the repository's `CLAUDE.md` lists them for maintainers under Diagnostics.

### Triggers

- The daemon writes a backup when it starts and every 6 hours (`internal/daemon/backup.go`).
- The daemon raises a warning notification when an enabled consumer pins the log past `ATTN_BUS_PIN_ALARM_AGE`.
- The daemon raises a notification when a background task exhausts its retries.
- When a data migration fails at startup, the daemon leaves `migration-failure.json` in the data folder (`internal/daemon/migration_failure.go`) and the app opens on the migration failure screen (`app/src-tauri/core/src/migration_failure.rs`).

## Driving it

Preconditions:

- Use the baseline instance. The CLI steps need only the daemon. For the palette and settings steps, also use the instance's app. Check that `bridge --launch --wait-ready ping` answers. Run `bridge dismiss_whats_new` to dismiss What's new.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-diagnostics.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Before native Settings clicks, scroll the nav button or consumer toggle fully into view with `bridge dom_scroll_into_view '{"selector":"<selector>"}'`, then resolve its point. A partly clipped toggle can put the click on the backdrop and close Settings. Scrolling is setup. The click stays native.

- **Preflight.** With the daemon only, `./attn preflight --agent claude` prints the checks and `attn preflight: FAIL (2 failed, 0 warnings)`, then exits 1. The two `protocol.` checks fail because they need an installed app. With the app installed, `"$(./attn instance resolve --field appDaemon)" preflight --agent claude` reports `PASS`. `--json` prints the stable report.
- **Daemon log.** `./attn debug daemon-log --grep "daemon started"` prints `[<time>] INFO: daemon started`. `./attn debug daemon-log --since 10m --grep "database backup written"` names the backup taken at start. `./attn debug ls` prints `frontend debug dir:` with its files, then `daemon.log:` with its path and size.
- **App logs.** With the app open and a terminal used, `./attn debug diagnostics --tail 5` prints JSON lines, and `./attn debug input --tail 5` only the input decisions. Before the app writes a file, the reader exits 1 with `no such file:` and the path. `./attn debug incidents` does the same until the app records an incident.
- **GPU surfaces.** With the app open, `./attn debug diagnostics --grep '"kind":"surface"'` prints one record per change, each with `pane`, `session`, `event`, `canvasWidth`, `canvasHeight`, `bufferReleased` and `atlasSize`. Every mounted pane has `renderer_created` and a `canvas_resized` once it fits. Switching desktops adds `buffer_released` for the panes that leave and `buffer_restored` for the panes that return. `atlas_grown` appears once a pane's glyph atlas fills, for example after printing emoji and Nerd Font icons. `renderer_destroyed` appears when a pane closes or its renderer is rebuilt. `bridge capture_perf_snapshot '{"includeMemory":false}'` lists the current values per mounted pane under `terminals[].gpuSurface`.
- **Event bus from the shell.** `./attn bus status` prints `log: seq` with the window, the producers, then `CONSUMER  CURSOR  LAG  OLDEST UNREAD  ENABLED  FILTER` listing `garden-seed-bells`, the only consumer on the baseline (`analytics-mirror` also appears while usage analytics is on). `./attn bus disable garden-seed-bells` prints `consumer "garden-seed-bells" disabled`, and `./attn bus status --json` shows `garden-seed-bells` with `"enabled": false`. `./attn bus enable garden-seed-bells` prints `consumer "garden-seed-bells" enabled`. `./attn bus disable nope` exits 1 with `bus disable: no consumer named "nope"`. `./attn bus trim` prints `removed <n> event(s); log now holds` and the size.
- **Event bus in the app.** Press ⌘, (`ui.openSettings`) with `bridge native_key '{"key":",","modifiers":["command"]}'`, then click `[data-testid="settings-nav-eventBus"]` natively. `bridge dom_text '{"selector":"[data-testid=\"bus-consumers\"]"}'` lists `garden-seed-bells`. Click `[data-testid="bus-consumer-toggle-garden-seed-bells"]` natively: the row reads `Disabled`, and `./attn bus status --json` shows `"enabled": false`. Click it again to enable it.
- **Task runner.** Click `[data-testid="settings-nav-backgroundTasks"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-section-backgroundTasks\"]"}'` lists the one-off tasks the daemon ran, most recently updated first, each as its state and `kind:subject`. Periodic duties such as the hourly vents prune are not listed, so a fresh instance reads `No background tasks.` until a one-off task has run. A failed or dead task offers Retry.
- **Diagnostic report.** Close settings with Escape. Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'` and `bridge native_text '{"text":"Create diagnostic report"}'`, then `bridge native_key '{"key":"Enter"}'`. `bridge dom_wait '{"selector":"#diagnostic-report-title","timeoutMs":5000}'` matches. "Save report" has focus: run `bridge native_key '{"key":"Enter"}'`. The dialog closes, `ls -t ~/Downloads/attn-*.attn-report.json | head -1` names the new file, and Finder shows it. Move only that file into `$P`, and leave the other files in Downloads.
- **Restore the database.** Once `./attn debug daemon-log --grep "database backup written"` names the backup taken at start, plant a seed: `./attn seed plant "Restore drive"` prints `<seed>`. `./attn db restore` exits 1 with `attn db restore: the attn daemon is running; stop it first`. Quit the app with `./attn instance stop-app` if it runs, then `./attn daemon stop` prints `stopped daemon (pid <n>)`. `./attn db restore` prints `restored attn.db from:` with a path under `<data-dir>/backups/`, `previous attn.db preserved as:` with `attn.db.pre-restore-<time>`, and how to start the daemon. `./attn daemon ensure` prints JSON with `"status": "started"`, and `./attn seed ls` no longer lists `Restore drive`.
- **The diagnostics endpoint.** Quit the app if it runs (`./attn instance stop-app`), run `./attn daemon stop`, then start the daemon again with a port of your own that nothing else holds: `ATTN_PPROF=<port> ./attn daemon ensure` prints `"status": "started"`. `./attn debug daemon-log --grep "diagnostics endpoint listening"` shows `diagnostics endpoint listening on http://127.0.0.1:<port>/ (pprof + /debug/vars)`, `curl -s http://127.0.0.1:<port>/debug/vars` prints JSON that includes `worker_pids`, and `curl -s http://127.0.0.1:<port>/` lists the endpoints. Stop that daemon and run `./attn daemon ensure` without the variable to close the port.
- **Proof.** In `$P`, keep each `./attn` command with its stdout, stderr and exit code. Also keep `./attn bus status --json` from before and after each toggle, `bridge dom_text` of `[data-testid="bus-consumers"]` after each click, `bridge capture_screenshot '{"path":"<P>/event-bus.png"}'` with **Settings → Event bus** open (where `<P>` is the folder's path written out), the report file, and `./attn seed ls` from before and after the restore. Name each file for its way in, such as `diagnostics-bus-cli-disable.txt`.

## Contexts

- **Durability across restarts**: the daemon takes a backup at every start and every 6 hours and keeps the newest 12 backups, and keeps the pre-migration snapshots as well. A restore keeps the database it replaces. A restore also pins again each conversation archive that the restored backup records. The daemon stores a consumer's cursor and enabled bit, so a disabled consumer stays disabled across restarts. `daemon.log` is kept across restarts, and the daemon trims it once it grows past 20 MiB.
- **Linux**: `attn preflight`, `attn debug`, `attn bus` and `attn db` work the same where the daemon and CLI run on Linux. The report and the settings sections are in the app. On Linux, `ui.commandPalette` is Ctrl+Alt+K and `ui.openSettings` is Ctrl+Shift+,.
- **Outposts**: `attn debug`, `attn bus` and `attn db` act on the machine they run on: an outpost's own log, event bus and database, never the home's.
- **Profiles**: logs, the event bus, tasks and backups belong to the daemon and cover every profile.
- **Performance and idle cost**: `attn bus status` names the loudest producers, the consumer at the retention floor and the consumer that pins the log, and **Settings → Event bus** shows the same facts. `ATTN_PPROF` and `docs/internal/perf-testing.md` cover memory and CPU.
- **Keyboard and native input**: the palette opens the report dialog, the "Save report" button has focus so Enter saves, and Escape closes the dialog.
- **Compatibility across versions**: `attn db restore <path>` accepts a pre-migration snapshot, `attn-premigration-<version>-<timestamp>.db`, to go back to the data before a migration.

## Tests

- Wire: `internal/daemon/bus_status_wire_test.go`, `internal/daemon/bus_health_wire_test.go`, `internal/daemon/bus_lag_wire_test.go`, `internal/daemon/bus_pin_alarm_wire_test.go`, `internal/daemon/daemon_warnings_wire_test.go`, `internal/daemon/diag_wire_test.go`, `internal/daemon/support_snapshot_wire_test.go`, `internal/daemon/backup_wire_test.go`, `internal/daemon/backup_rotation_wire_test.go`, `internal/daemon/tasks_wire_test.go`, `internal/daemon/tasks_protocol_wire_test.go`, `internal/daemon/tasks_restart_wire_test.go`.
- Stack: `cmd/attn/debug_stack_test.go`, `cmd/attn/preflight_stack_test.go`, `cmd/attn/db_stack_test.go`, `cmd/attn/bus_stack_test.go`.
- Kernel: `cmd/attn/bus_test.go`, `internal/daemon/bus_pin_alarm_test.go`, `internal/store/backup_test.go`, `internal/bus/*_test.go`, `internal/jobs/*_test.go`.
- App: `app/src/App.diagnosticReport.test.tsx`, `app/src/App.eventBusSettings.test.tsx`, `app/src/App.backgroundTasks.test.tsx`, `app/src/application/useAppDiagnostics.test.tsx`, `app/src/components/MigrationFailureScreen.facts.test.tsx`.

## Gotchas

- `attn bus` and `attn db` open the database file directly and `attn debug` reads files on disk, so they act on whatever instance the shell selects, daemon or not. Run them only in the instance shell. Without an instance they read and change production. Each prints the `[attn instance=<name> ...]` banner on stderr. If you see no banner, stop.
- `attn db restore` needs the daemon stopped, and an open app starts its daemon again. Quit the app first.
- A restore brings back the newest backup, so anything since that backup is gone. The replaced database stays beside it as `attn.db.pre-restore-<time>`.
- Disabling `garden-seed-bells` stops seed updates until it is enabled again. Enable every consumer you disabled before the run ends.
- `attn bus status` reads the database, not the daemon. It cannot show whether a consumer's delivery is running, and it says so.
- `ATTN_BUS_PIN_ALARM_AGE` and `ATTN_BUS_RETENTION` change the daemon and `attn bus` separately. Set them for both, or the table and the notification disagree. On a young instance a trim removes little, since the default window is 30 days.
- The diagnostic report goes to the real Downloads folder and Finder opens on it. The report may contain terminal output the user ticked. Check it for private text before sharing.
- `attn debug incidents`, `diagnostics` and `input` read the app's files: on a daemon-only instance they exit 1 with `no such file:`.
- The migration failure screen cannot be driven on the baseline: it needs a migration that really failed. You reach Retry and the failed-task notification only when a task has failed or died. If **Settings → Task runner** shows none, report them as not reached.
