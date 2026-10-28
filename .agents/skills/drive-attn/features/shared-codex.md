# Shared Codex

Codex sessions share one app-server that attn keeps updated and restarts. With
the experimental setting on, every Codex terminal in a profile talks to one
Codex app-server instead of running a Codex of its own. That lets a
conversation outlive the terminal that showed it: a terminal that switches to
another conversation leaves the first one running as a hidden session, which
still queues, takes messages and answers approvals. When a newer Codex is out,
attn offers to install it, moves the app-server to it once no turn runs, and
offers to reload the tiles still on the old one. `docs/using-shared-codex.md`
is the user's guide.

## Sub-features

- `codex-shared-switch`: the Settings switch, off by default; it decides how new Codex terminals and resumes from the ledger launch.
- `codex-status`: the Codex the app-server runs, the installed and latest ones, a pending restart and what it waits on, and the tiles on an older Codex.
- `codex-hidden`: a conversation no tile shows stays live and hidden, in the ledger and, optionally, the queue.
- `codex-approval`: an approval waits in the conversation, answered in its tile or after it opens in one.
- `codex-update`: a daily check offers a newer Codex; Install runs Codex's own update in a terminal tile.
- `codex-restart`: the app-server moves to the installed Codex once no turn runs, or now on request; a failed start keeps the old one running.
- `codex-reload-tiles`: restart the tiles still on an older Codex, in the same conversations.
- `codex-names`: a session's name and its Codex conversation's name follow each other.

## How to get to it

### People

- Settings, Experimental (`app/src/components/SettingsModalSections.tsx`): "Shared Codex", `[data-testid="settings-shared-codex-toggle"]` (setting `codex_shared_enabled`), and "Show hidden sessions in the queue" (`[data-testid="settings-queue-hidden-sessions-toggle"]`, setting `queue_show_hidden_sessions`, on by default).
- The banner at the top of the window, `[aria-label="Codex updates"]`, with Install, Restart now, Retry or Reload tiles as each applies, and a confirm prompt before a restart or a reload (`app/src/components/SharedCodexBanner.tsx`, `app/src/components/SharedCodexPrompts.tsx`). Closed, the same offer stays under the bell in the sidebar.
- Palette, while each applies (`app/src/application/useSharedCodex.tsx`): `install-codex-update` ("Install Codex update"), `restart-shared-codex-now` ("Restart shared Codex now"), `retry-shared-codex-restart` ("Retry shared Codex restart") and `reload-old-codex-tiles` ("Reload old Codex tiles").
- In a shared Codex terminal: `/new`, `/resume <id>` and `/agents` switch its conversation.
- Where people see what agents do: a hidden session is marked `hidden` in the ledger ([ledger.md](ledger.md)) and queues without a desktop chip ([queue-flow.md](queue-flow.md)).
- Their own shell: the verbs below.

### Agents

- `attn codex status [--profile <name|id>] [--json]`, `attn codex update [--profile]` and `attn codex restart [--now] [--profile]` (`cmd/attn/codex.go`). Inside a session they act on the session's profile.
- Prompts: `attn opened this conversation.`, typed into a conversation attn opens in the app-server (`internal/prompts/content/session/codex-opened.md`).

### Triggers

- The first shared terminal starts the app-server as a terminal of its own; it stops when no open session and no shared terminal needs it (`internal/daemon/codex_shared.go`).
- Once a day attn asks `codex doctor` for the latest Codex, and it reads the installed Codex when a shared terminal opens, when the app-server starts and when an update it ran finishes (`internal/daemon/codex_update.go`).
- When the installed Codex differs from the one the app-server runs, attn restarts it once no turn runs and every shared terminal has finished starting (`internal/daemon/codex_restart.go`).
- attn answers Codex's folder trust question for a folder where an agent session already started a conversation, and for every automation (`docs/using-shared-codex.md`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`). `R=$PWD` at the repository root.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-codex.XXXX)`.
- An agent folder, `C=$(mktemp -d /tmp/drive-attn-codex-agent.XXXX)`, holding this fixture as `$C/.attn-mock-agent.json`. Under shared Codex the mock plays only `reply` and `delay`; `"approval": true` makes it ask before it replies:

```json
{"version": 1, "turns": [
  {"includes": "hello", "actions": [{"type": "reply", "text": "hello back"}]},
  {"includes": "approve", "actions": [{"type": "reply", "text": "ran it", "approval": true}]}
]}
```

`<profile>` is the profile's name; `<terminal>` is the agent's terminal id from `wsctl list`; `<P>` is the proof folder's path written out.

- **Off by default.** Run `./attn codex status`. It prints `profile  <profile>`, `codex` with the mock's path, and `shared   off; turn it on in Settings → Experimental`.
- **Turn it on in Settings.** Open Settings with `bridge native_key '{"key":",","modifiers":["command"]}'` and click `[data-testid="settings-nav-experimental"]` natively. Click `[data-testid="settings-shared-codex-toggle"]` natively. `./attn settings get codex_shared_enabled` prints `true`, and `./attn codex status` prints `server  not running; the next shared Codex terminal starts it`.
- **Hidden sessions in the queue.** With Settings still open on Experimental, click `[data-testid="settings-queue-hidden-sessions-toggle"]` natively: `./attn settings get queue_show_hidden_sessions` prints `false`, since the switch starts on. Click it again and it prints `true`. Press Escape.
- **A shared terminal starts the app-server.** Run `wsctl add-session --cwd $C --agent codex --label shared-one --id shared-one --place`. `./attn codex status` prints `server  1.0.0`, `installed  1.0.0` and `restart  none`. Run `wsctl input --id <terminal> --text "hello" --enter`: `./attn agent list` shows `shared-one` as `waiting_input`.
- **An approval.** Turn Auto-approve off first with `./attn settings set auto_approve_enabled false`; with it on, an approval goes to an automatic reviewer and shows as pending only after a delay. Run `wsctl input --id <terminal> --text "approve" --enter`. `./attn agent list` shows `pending_approval`, and `./attn agent peek shared-one` shows `Allow the command to run?`. Run `wsctl input --id <terminal> --enter`: the turn runs, peek shows `• ran it`, and the state is `waiting_input`. Put the setting back with `./attn settings set auto_approve_enabled true` after the run.
- **A switch leaves a hidden session.** Run `wsctl input --id <terminal> --text "/new" --enter`. The tile shows a new session at once, and `./attn session show shared-one` prints `state      waiting_input (hidden)`; `./attn session list` says the same. In the ledger (⌘⇧L), the `shared-one` row is marked `hidden`.
- **Restart once idle, then now.** Run `./attn codex restart`. It prints `Shared Codex of profile <profile> restarts once no turn runs.`, and `./attn codex status` shows the restart until it is back to `restart  none`. Run `./attn codex restart --now`: it prints `Restarting shared Codex of profile <profile> now.`, and once the terminal reconnects, peek shows its conversation again.
- **Update without a known install.** Run `./attn codex update`. With the baseline's mock it prints `Could not detect how Codex was installed`, then the status, and exits 1.
- **Update, install and reload tiles.** Make a mock Codex install that reports 1.0.0 with 1.1.0 out, and restart the instance on it:

```bash
X=$(mktemp -d /tmp/drive-attn-codex-install.XXXX)
echo 1.0.0 > $X/version; echo 1.1.0 > $X/latest
printf '#!/bin/sh\nATTN_MOCK_CODEX_STATE_DIR=%s exec %s "$@"\n' "$X" "$R/app/scripts/real-app-harness/mockAgent.mjs" > $X/codex
chmod +x $X/codex
export ATTN_CODEX_EXECUTABLE=$X/codex
./attn instance stop-app
"$(./attn instance resolve --field appDaemon)" daemon stop
bridge --launch --wait-ready ping
```

  Start a shared terminal in `$C` as above. `bridge dom_wait '{"selector":"[aria-label=\"Codex updates\"]","textIncludes":"Codex 1.1.0 is available.","timeoutMs":30000}'` matches. Open the palette with `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, wait for `[role="combobox"][aria-label="Commands"]` to take focus, and type `Install Codex update`: `bridge dom_text '{"selector":".palette-list"}'` lists it. Press Escape. Click `[aria-label="Codex updates"] .banner-button` natively: a new shell tile prints `Codex updated to 1.1.0`, and the banner reads `Codex 1.1.0 is running.` with a count of the tiles that still run an older Codex. `./attn codex status` prints `server  1.1.0` and an `old tiles` line naming them. Click the banner's button again, then `.shared-codex-prompt .mp-button.primary`: the banner goes, and `./attn codex status` has no `old tiles` line.
- **Last tile closes into the ledger.** With the shared tile selected, press ⌘W (`bridge native_key '{"key":"w","modifiers":["command"]}'`). The session it showed closes, `./attn session list --closed` lists it, and its Codex conversation is archived until the ledger resumes it.
- **Proof.** In `$P`, keep each `./attn codex` command with its output and exit code, `./attn session show shared-one` after the switch, `./attn agent peek shared-one` during the approval, `bridge dom_text '{"selector":"[aria-label=\"Codex updates\"]"}'` at each banner stage, and `bridge capture_screenshot '{"path":"<P>/codex-banner.png"}'` with the banner showing. Put the way in in each file's name, such as `codex-banner-install.txt`.

## Contexts

- **Durability across restarts**: the app-server runs as a terminal of its own, so it keeps running through app and daemon restarts, and the terminals reconnect and resume the conversations they show. If the app-server crashes, its running turns stop; attn starts it again on the next connection, and hidden sessions whose turns stopped go idle.
- **Profiles**: one app-server per profile. Inside a session `attn codex` acts on the session's profile; elsewhere it needs `--profile` when there is more than one.
- **Performance and idle cost**: one app-server per profile replaces a Codex process per terminal and uses less memory. With no open shared session and no shared terminal left, it stops.
- **Harnesses**: Codex only. Claude Code and Pi have no link, so a second tile on one of their conversations is a second session ([harnesses.md](harnesses.md)).
- **Keyboard and native input**: every banner action is also a palette command while it applies.
- **Compatibility across versions**: a tile started before a restart keeps its older Codex and denies an approval of a kind it does not know; Reload tiles moves it to the new one. A running session stays shared or plain, as it launched, after the switch changes.
- **Flow**: in queue flow a hidden session queues like any other, without a desktop chip, unless "Show hidden sessions in the queue" is off; choosing it opens it in a tile on the current desktop.

## Tests

- Wire: `internal/daemon/codex_*_wire_test.go`, `internal/daemon/slow_launch_wire_test.go`.
- Stack: `cmd/attn/codex_shared_stack_test.go`.
- Scenario: `codex-shared`, `codex-update`.
- App: `app/src/components/BannerStack.sharedCodex.test.tsx`, `app/src/components/SettingsModal.experimental.test.tsx`, `app/src/App.hiddenSessions.test.tsx`.

## Gotchas

- Under shared Codex the mock plays only `reply` and `delay` actions; a fixture turn with any other action fails, such as `the shared Codex mock does not play attn actions`. An agent's attn verbs are driven through a plain Codex or Claude mock; `attn codex` needs no session, so `./attn` stands in for the agent here.
- The switch decides how new terminals and ledger resumes launch. A Codex session already running stays plain until it closes.
- Auto-approve is on by default for Claude Code and Codex. Under it an approval is handled by an automatic reviewer, and `pending_approval` can lag by about a minute; the `codex-shared` scenario turns it off for the approval steps, and the approval step here does the same.
- The baseline's `ATTN_CODEX_EXECUTABLE` points at the mock itself, which cannot update. An update needs the pinned install above, and the daemon and app restarted from a shell that exports it; the variable wins over the executable set in Settings.
- A new tile switched before its first prompt leaves no hidden session behind. Send a prompt before `/new`.
- The app-server holds a conversation for about a minute after no terminal shows it; a plain Codex's `/resume` of it fails with `already has an active writer` until then.
- `attn codex restart --now` stops turns waiting on an approval or a question at once. Use it only when that is what the step proves.
- Working but wrong: the banner's single button changes meaning with the stage (Install, Restart now, Retry, Reload tiles), so a selector on `.banner-button` must be read with the banner's text.
