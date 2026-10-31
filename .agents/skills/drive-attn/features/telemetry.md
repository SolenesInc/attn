# Telemetry

What attn records about its own use, kept in an outbox and uploaded only when
the user turns it on. With usage analytics on, attn writes redacted records of
its event bus, app actions, `attn` command outcomes and resource samples to an
outbox on the machine and keeps them 7 days. Settings › Analytics shows every
record exactly as it is stored. Sharing uploads the outbox to attn's
maintainers; both are off by default. `docs/guide/reference/telemetry.md` says
what each record holds and what leaves the machine. Vents, which live in the
same section, are in [vents.md](vents.md).

## Sub-features

- `recording`: `analytics.enabled` turns recording on for this machine; experimental, off by default.
- `streams`: `bus`, `app`, `cli` and `sample` records, redacted before they are stored, and `vent` records the user sends.
- `outbox`: the records kept 7 days, listed newest first by stream in Settings › Analytics, exactly as stored.
- `sharing`: an upload every 5 minutes, and once when sharing turns on; a refusal shows with its limit.
- `forget-install`: delete what the server holds and start a new install.
- `clear-record`: delete the local outbox and dismissed vents.
- `ingest`: attn-ingest, the service that receives uploads (`cmd/attn-ingest`, `docs/internal/attn-ingest.md`).

## How to get to it

### People

- Settings › Experimental, the "Usage analytics" switch (`[data-testid="settings-analytics-toggle"]`, `app/src/components/SettingsModalSections.tsx`). Turning it on adds Settings › Analytics (`[data-testid="settings-nav-analytics"]`). Opening the settings dialog is in [settings.md](settings.md).
- Settings › Analytics (`app/src/components/Analytics/AnalyticsSettings.tsx`): the strip (Recording, Sharing, Vents, Kept here); the lists Vents, All records, `bus`, `app`, `cli` and `sample`; and the inspector, which shows a record's JSON under "Exactly as stored". Keys: `[` and `]` switch lists, `j`/`k` or the arrows move, Enter expands a burst of records from the same second, `c` copies a record's JSON, `r` refreshes, `m` opens Machine settings.
- Machine settings (`app/src/components/Analytics/AnalyticsMachine.tsx`): "Record on this machine" (Disable), "Share with attn's maintainers" (Enable or Disable, then "Delete from server" after sharing ran), "Send vents", "Tool vents", "Forget this install…" and "Clear the local record…".
- Their own shell: `attn settings set analytics.enabled|analytics.sharing_enabled|analytics.share_vents|analytics.tool_vents true|false` and `attn settings get` (`cmd/attn/settings.go`).

### Agents

None. Agents do not read or change telemetry: their `attn` commands are recorded (Triggers), and their vents are in [vents.md](vents.md).

### Triggers

- While recording: every event bus fact except `vent.*` is mirrored into the outbox (`internal/daemon/analytics_mirror.go`); app actions, `attn` command outcomes and socket command outcomes are recorded (`internal/daemon/analytics_records.go`); a resource sample of the daemon is taken every 5 minutes (`internal/daemon/analytics_samples.go`).
- While sharing: an upload every 5 minutes and once when sharing turns on (`internal/daemon/analytics_ship.go`), to `ATTN_ANALYTICS_INGEST_URL` from the daemon's environment, or `https://attn-telemetry.caketroll.cloud` without it.
- Records older than 7 days leave the outbox; records not yet sent wait up to 30 days while sharing is on.

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-telemetry.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- The baseline's `ATTN_ANALYTICS_INGEST_URL=http://127.0.0.1:1` in the daemon's environment, so a stray click on a sharing control cannot reach the real service. A daemon started without the env file lacks it: stop it and start it again in the instance shell.
- Sharing only: a local attn-ingest on `127.0.0.1:8080`, started as "Running locally" in `docs/internal/attn-ingest.md` says (`make ingest-clickhouse` needs Docker; `go run ./cmd/attn-ingest` needs `ATTN_INGEST_CLICKHOUSE_URL`, `ATTN_INGEST_IP_SECRET` and `ATTN_INGEST_LISTEN=127.0.0.1:8080`), and the daemon started with `export ATTN_ANALYTICS_INGEST_URL=http://127.0.0.1:8080` appended to the env file instead. Without both, skip the sharing step and report it skipped.

- **Turn recording on in the app.** Press ⌘, (`ui.openSettings`): run `bridge native_key '{"key":",","modifiers":["command"]}'`; `bridge settings_get_state` shows `open: true`. Click `[data-testid="settings-nav-experimental"]` natively, then `[data-testid="settings-analytics-toggle"]`. `bridge dom_wait '{"selector":"[data-testid=\"settings-nav-analytics\"]","timeoutMs":5000}'` matches, and `./attn settings get analytics.enabled` prints `true`.
- **Off and on from the shell.** `./attn settings set analytics.enabled false` prints `analytics.enabled = false`, and `bridge dom_wait '{"selector":"[data-testid=\"settings-nav-analytics\"]","absent":true,"timeoutMs":5000}'` matches. `./attn settings set analytics.enabled true` brings the section back.
- **Read the outbox.** Scroll the nav button into view with `bridge dom_scroll_into_view '{"selector":"[data-testid=\"settings-nav-analytics\"]"}'`, then click `[data-testid="settings-nav-analytics"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-analytics\"]"}'` reads `Recording`, `On`, `Sharing`, `Off`. Run `bridge native_key '{"key":"]"}'`: `bridge dom_wait '{"selector":"ol[aria-label=\"All records\"] li","timeoutMs":5000}'` matches. Run `bridge native_key '{"key":"j"}'`: `[data-testid="analytics-inspector"]` reads `Exactly as stored` and the record's JSON. Where the record has them, free text is replaced by its length, such as `{"s":24}`, and subjects by `h:` and 12 hex digits.
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
- The "Usage analytics" switch sits low in Settings › Experimental; `dom_native_point` scrolls it into view before returning its point.
- A click that removes the focused control, such as a "Clear…" or "Forget…" confirm, or `m` pressed on a Machine settings button, leaves the page without focus: its keys do nothing until a row or button inside it is clicked.
- The record lists load when a list opens. A record made while the list is open shows only after `r` or the Refresh button.
- Turning recording off and on again starts the mirror at the head of the bus: facts from the gap are never recorded.
- `vent.*` facts never reach the outbox. A vent becomes a record only when it is sent.
- Working but wrong: the section header's pills read "Recording" and "Local only" even while sharing is on, and the Experimental switch still says "Nothing is sent."
