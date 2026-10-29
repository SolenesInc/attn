# Telemetry

Usage analytics records redacted attn activity in a local outbox. Sharing can
upload it to the maintainers. Both are off by default. `docs/telemetry.md`
describes the records and uploads; [vents.md](vents.md) covers vents.

## Actions and ways in

### Record and inspect

Enable "Usage analytics" in Settings › Experimental (`[data-testid="settings-analytics-toggle"]`, `app/src/components/SettingsModalSections.tsx`). It adds Settings › Analytics (`[data-testid="settings-nav-analytics"]`; [settings.md](settings.md)). The section (`app/src/components/Analytics/AnalyticsSettings.tsx`) shows Recording, Sharing, Vents and Kept here, with lists for Vents, All records, `bus`, `app`, `cli` and `sample`. The inspector shows a record's JSON "Exactly as stored". `[` and `]` switch lists, `j`/`k` or arrows move, Enter expands a same-second burst, `c` copies JSON, `r` refreshes and `m` opens Machine settings.

While recording, attn mirrors every bus fact except `vent.*`, records app actions and `attn` and socket command outcomes, and samples the daemon every 5 minutes (`internal/daemon/analytics_mirror.go`, `internal/daemon/analytics_records.go`, `internal/daemon/analytics_samples.go`). Records leave the local outbox after 7 days; unsent records wait up to 30 days while sharing is on. Agents' `attn` commands are recorded, but agents do not use the telemetry controls.

### Share, clear or forget

Machine settings (`app/src/components/Analytics/AnalyticsMachine.tsx`) has "Record on this machine", "Share with attn's maintainers", "Delete from server" after sharing ran, "Send vents", "Tool vents", "Forget this install…" and "Clear the local record…". Clearing deletes the outbox and dismissed vents; forgetting deletes what the server holds and starts a new install. People can also run `attn settings set analytics.enabled|analytics.sharing_enabled|analytics.share_vents|analytics.tool_vents true|false` and `attn settings get` (`cmd/attn/settings.go`).

Sharing uploads when enabled and every 5 minutes (`internal/daemon/analytics_ship.go`) to `ATTN_ANALYTICS_INGEST_URL`, or `https://attn-telemetry.caketroll.cloud` by default. A refusal shows its limit. The receiving service is `cmd/attn-ingest` (`docs/attn-ingest.md`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-telemetry.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- The baseline's `ATTN_ANALYTICS_INGEST_URL=http://127.0.0.1:1` in the daemon's environment, so a stray click on a sharing control cannot reach the real service. A daemon started without the env file lacks it: stop it and start it again in the instance shell.
- Sharing only: a local attn-ingest on `127.0.0.1:8080`, started as "Running locally" in `docs/attn-ingest.md` says (`make ingest-clickhouse` needs Docker; `go run ./cmd/attn-ingest` needs `ATTN_INGEST_CLICKHOUSE_URL`, `ATTN_INGEST_IP_SECRET` and `ATTN_INGEST_LISTEN=127.0.0.1:8080`), and the daemon started with `export ATTN_ANALYTICS_INGEST_URL=http://127.0.0.1:8080` appended to the env file instead. Without both, skip the sharing step and report it skipped.

- **Turn recording on in the app.** Press ⌘, (`ui.openSettings`): run `bridge native_key '{"key":",","modifiers":["command"]}'`; `bridge settings_get_state` shows `open: true`. Click `[data-testid="settings-nav-experimental"]` natively, then `[data-testid="settings-analytics-toggle"]`. `bridge dom_wait '{"selector":"[data-testid=\"settings-nav-analytics\"]","timeoutMs":5000}'` matches, and `./attn settings get analytics.enabled` prints `true`.
- **Off and on from the shell.** `./attn settings set analytics.enabled false` prints `analytics.enabled = false`, and `bridge dom_wait '{"selector":"[data-testid=\"settings-nav-analytics\"]","absent":true,"timeoutMs":5000}'` matches. `./attn settings set analytics.enabled true` brings the section back.
- **Read the outbox.** Click `[data-testid="settings-nav-analytics"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-analytics\"]"}'` reads `Recording`, `On`, `Sharing`, `Off`. Run `bridge native_key '{"key":"]"}'`: `bridge dom_wait '{"selector":"ol[aria-label=\"All records\"] li","timeoutMs":5000}'` matches. Run `bridge native_key '{"key":"j"}'`: `[data-testid="analytics-inspector"]` reads `Exactly as stored` and the record's JSON. Where the record has them, free text is replaced by its length, such as `{"s":24}`, and subjects by `h:` and 12 hex digits.
- **A command outcome reaches the `cli` list.** From the shell, `./attn doc nope` exits 2 with `doc: unknown command "nope"`. In the app, press `]` until `ol[aria-label="cli"]` shows (three presses from All records), then `r`: `bridge dom_text '{"selector":"ol[aria-label=\"cli\"]"}'` includes `doc`.
- **Machine settings and clearing.** Run `bridge native_key '{"key":"m"}'`: `[data-testid="analytics-machine"]` reads `Record on this machine`, `Share with attn's maintainers` and `Tool vents`. Run `./attn settings set analytics.share_vents true`: `bridge dom_wait '{"selector":"[data-testid=\"analytics-machine\"]","textIncludes":"Send vents","timeoutMs":5000}'` matches, with sharing still off. Run `./attn settings set analytics.share_vents false` and the row goes. Click `[data-testid="analytics-machine"] .settings-row-card:last-child button.danger` natively ("Clear…"), then `.analytics-confirm button.danger` ("Clear"): `bridge dom_wait '{"selector":"[role=\"status\"]","textIncludes":"Cleared","timeoutMs":5000}'` matches.
- **Share with a local attn-ingest.** Only with the sharing preconditions. Run `./attn settings set analytics.sharing_enabled true`: it prints `analytics.sharing_enabled = true`, and an upload starts at once. The confirm buttons took focus out of the page, so click the list's Refresh button natively (`.analytics-footer-actions button:last-child`) instead of pressing `r`: the Sharing stat reads `On ·` and when it last sent. `curl -s -H "Authorization: Bearer $(jq -r .token "$(./attn instance resolve --field dataDir)/analytics-token")" http://127.0.0.1:8080/v1/installs/me` shows the install with `events` above 0. `./attn settings set analytics.sharing_enabled false` stops the uploads.
- **Proof.** In `$P`, keep each `./attn settings` command with its output and exit code, `bridge dom_text` of `[data-testid="settings-analytics"]` and `[data-testid="analytics-inspector"]` after each step, and `bridge capture_screenshot '{"path":"<P>/analytics-records.png"}'` with a record open, where `<P>` is the folder's path written out. With sharing, keep the `curl` reply. Put the way in in each file's name, such as `telemetry-shell-toggle.txt`.

## Contexts

- **Durability across restarts**: the outbox, the install token (`<data-dir>/analytics-token`) and the subject secret (`<data-dir>/analytics-secret`) live in the data folder and survive restarts. A fact delivered again after a crash is stored once.
- **Linux**: the daemon records and uploads the same where it runs on Linux. Settings › Analytics is in the app, and `ui.openSettings` is Ctrl+Shift+, there.
- **Outposts**: `analytics.enabled` is a machine setting, so each machine decides whether it records and keeps its own outbox. `analytics.sharing_enabled`, `analytics.share_vents` and `analytics.tool_vents` are home settings that outposts copy read-only. An `attn` usage error, such as an unknown command, is recorded on the machine that ran the command.
- **Profiles**: records are the daemon's, across every profile. A vent record is listed only in the profile its vent belongs to.
- **Performance and idle cost**: one resource sample every 5 minutes, and an upload carries at most 5,000 records. The open Analytics page does not animate or change while idle (scenario `analytics-settings`). The outbox mirror is an event bus consumer, so a stalled mirror pins the log; `attn bus status` shows it ([diagnostics.md](diagnostics.md)).
- **Harnesses**: each `cli` record carries the harness of the agent that ran the command, and session facts carry the harness and model.
- **Keyboard and native input**: every list action is one key, listed at the foot of the page, and the section takes focus when it opens. Keys typed into a text field do nothing to the lists.
- **Compatibility across versions**: attn-ingest accepts event names and streams it has never seen, so a newer build loses nothing to an older service. A retried batch counts its records as duplicates and adds no rows.

## Tests

- Wire: `internal/daemon/analytics_wire_test.go`, `internal/daemon/analytics_enrich_wire_test.go`, `internal/daemon/analytics_mirror_wire_test.go`, `internal/daemon/analytics_ship_wire_test.go`, `internal/daemon/analytics_streams_wire_test.go`.
- Stack: `cmd/attn/analytics_stack_test.go`, `internal/daemon/analytics_mirror_stack_test.go`, `cmd/attn-ingest/ingest_stack_test.go`.
- Scenario: `analytics-settings`.
- Kernel: `internal/analytics/redact_test.go`.
- App: `app/src/App.analytics.test.tsx`, `app/src/components/Analytics/AnalyticsSettings.records.test.tsx`, `app/src/components/Analytics/AnalyticsSettings.sharing.test.tsx`.

## Gotchas

- A named instance has no guard of its own against the real service. Turning sharing on, "Forget this install…" and "Delete from server" all reach `https://attn-telemetry.caketroll.cloud` unless the daemon was started with `ATTN_ANALYTICS_INGEST_URL`, which the baseline exports. `make install` and `./attn daemon ensure` start the daemon with the instance shell's environment, so a daemon started without the env file lacks it.
- With `analytics.enabled` off nothing is recorded or sent, whatever the other settings say, and Settings › Analytics is absent.
- The "Usage analytics" switch sits low in Settings › Experimental. A box below the window makes a native click miss: run `bridge dom_scroll_into_view '{"selector":"[data-testid=\"settings-analytics-toggle\"]"}'` before reading its box. Scrolling is setup, not the person's way in.
- A click that removes the focused control, such as a "Clear…" or "Forget…" confirm, or `m` pressed on a Machine settings button, leaves the page without focus: its keys do nothing until a row or button inside it is clicked.
- The record lists load when a list opens. A record made while the list is open shows only after `r` or the Refresh button.
- Turning recording off and on again starts the mirror at the head of the bus: facts from the gap are never recorded.
- `vent.*` facts never reach the outbox. A vent becomes a record only when it is sent.
- Working but wrong: the section header's pills read "Recording" and "Local only" even while sharing is on, and the Experimental switch still says "Nothing is sent."
