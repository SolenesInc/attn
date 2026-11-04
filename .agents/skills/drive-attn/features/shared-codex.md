# Shared Codex

Codex sessions share one app-server. With the experimental setting on, every
Codex terminal in a profile talks to one Codex app-server instead of running a
Codex of its own, and attn keeps that app-server updated and restarts it. A
conversation can then outlive the terminal that showed it. When a terminal
switches to another conversation, the first conversation keeps running as a
hidden session. A hidden session still queues and takes messages, and the user can still
answer its approvals. When a newer Codex is out, attn offers to install it,
moves the app-server to the new Codex once no turn runs, and offers to reload
the tiles still on the old Codex.
`docs/guide/reference/using-shared-codex.md` is the user's guide.

## Sub-features

- `codex-shared-switch`: the Settings switch, off by default. It decides how new Codex terminals and resumes from the ledger launch.
- `codex-status`: the Codex the app-server runs, the installed and latest ones, a pending restart and what it waits on, and the tiles on an older Codex.
- `codex-hidden`: a conversation no tile shows stays live and hidden, in the ledger and, optionally, the queue.
- `codex-approval`: an approval waits in the conversation until the user answers it, either in the conversation's tile or after opening the conversation in a tile.
- `codex-update`: attn checks daily for a newer Codex and offers it. Install runs Codex's own update in a terminal tile.
- `codex-restart`: the app-server moves to the installed Codex once no turn runs, or now on request. If the new Codex fails to start, the old one keeps running.
- `codex-reload-tiles`: restart the tiles still on an older Codex, in the same conversations.
- `codex-names`: a session's name and its Codex conversation's name follow each other.

## How to get to it

### People

- **Settings → Experimental** (`app/src/components/SettingsModalSections.tsx`): "Shared Codex", `[data-testid="settings-shared-codex-toggle"]` (setting `codex_shared_enabled`), and "Show hidden sessions in the queue" (`[data-testid="settings-queue-hidden-sessions-toggle"]`, setting `queue_show_hidden_sessions`, on by default).
- The banner at the top of the window, `[aria-label="Codex updates"]`, with Install, Restart now, Retry or Reload tiles as each applies, and a confirm prompt before a restart or a reload (`app/src/components/SharedCodexBanner.tsx`, `app/src/components/SharedCodexPrompts.tsx`). After the user closes the banner, the same offer stays under the bell in the sidebar.
- Palette, while each applies (`app/src/application/useSharedCodex.tsx`): `install-codex-update` ("Install Codex update"), `restart-shared-codex-now` ("Restart shared Codex now"), `retry-shared-codex-restart` ("Retry shared Codex restart") and `reload-old-codex-tiles` ("Reload old Codex tiles").
- In a shared Codex terminal, `/new`, `/resume <id>` and `/agents` switch the terminal's conversation.
- Where people see what agents do: a hidden session is marked `hidden` in the ledger ([ledger.md](ledger.md)) and queues without a desktop chip ([queue-flow.md](queue-flow.md)).
- Their own shell: the verbs below.

### Agents

- `attn codex status [--profile <name|id>] [--json]`, `attn codex update [--profile]` and `attn codex restart [--now] [--profile]` (`cmd/attn/codex.go`). Inside a session, these commands act on the session's profile.
- Prompts: `attn opened this conversation.`, typed into a conversation attn opens in the app-server (`internal/prompts/content/session/codex-opened.md`).

### Triggers

- When the first shared terminal opens, attn starts the app-server as a terminal of its own. attn stops the app-server when no open session and no shared terminal needs it (`internal/daemon/codex_shared.go`).
- Once a day attn asks `codex doctor` for the latest Codex, and attn reads the installed Codex when a shared terminal opens, when the app-server starts and when an update it ran finishes (`internal/daemon/codex_update.go`).
- When the installed Codex differs from the one the app-server runs, attn restarts the app-server once no turn runs and every shared terminal has finished starting (`internal/daemon/codex_restart.go`).
- attn answers Codex's folder trust question for a folder where an agent session already started a conversation, or whose repository Codex already trusts (`docs/guide/reference/using-shared-codex.md`).

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers, then run `bridge dismiss_whats_new` to dismiss What's new. At the repository root, run `R=$PWD && echo "export R=$R" >> $DRIVE_ENV`.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-codex.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `C=$(mktemp -d /tmp/drive-attn-codex-agent.XXXX) && echo "export C=$C" >> $DRIVE_ENV`, and save this fixture as `$C/.attn-mock-agent.json`. Under shared Codex the mock plays only `reply` and `delay`. The setting `"approval": true` makes the mock ask before it replies:

```json
{"version": 1, "turns": [
  {"includes": "hello", "actions": [{"type": "reply", "text": "hello back"}]},
  {"includes": "approve", "actions": [{"type": "reply", "text": "ran it", "approval": true}]},
  {"includes": "slow", "actions": [{"type": "delay", "ms": 600000}, {"type": "reply", "text": "slow done"}]}
]}
```

`<profile>` is the profile's name. `<terminal>` is the agent's terminal id from `wsctl list`. `<P>` is the proof folder's path written out.

- **Off by default.** Run `./attn codex status`. It prints `profile  <profile>`, `codex` with the mock's path, and `shared   off; turn it on in Settings → Experimental`.
- **Turn it on in Settings.** Open Settings with `bridge native_key '{"key":",","modifiers":["command"]}'` and click `[data-testid="settings-nav-experimental"]` natively. Click `[data-testid="settings-shared-codex-toggle"]` natively. `./attn settings get codex_shared_enabled` prints `true`, and `./attn codex status` prints `server  not running; the next shared Codex terminal starts it`.
- **Hidden sessions in the queue.** With Settings still open on Experimental, click `[data-testid="settings-queue-hidden-sessions-toggle"]` natively: `./attn settings get queue_show_hidden_sessions` prints `false`, since the switch starts on. Click it again and it prints `true`. Press Escape.
- **A shared terminal starts the app-server.** First run `./attn settings set auto_approve_enabled false`, before the terminal starts, so the user reviews the approval below. Run `wsctl add-session --cwd $C --agent codex --label shared-one --id shared-one --place`. `./attn codex status` prints `server  1.0.0`, `installed  1.0.0` and `restart  none`. Run `wsctl input --id <terminal> --text "hello" --enter`: `./attn agent list` shows `shared-one` as `waiting_input`.
- **An approval.** With Auto-approve disabled before launch, run `wsctl input --id <terminal> --text "approve" --enter`. `./attn agent list` shows `pending_approval`, and `./attn agent peek shared-one` shows `Allow the command to run?`. Run `wsctl input --id <terminal> --enter`: the turn runs, peek shows `• ran it`, and the state is `waiting_input`. Put the setting back with `./attn settings set auto_approve_enabled true` after the run.
- **A switch leaves a hidden session.** Run `wsctl input --id <terminal> --text "/new" --enter`. The tile shows a new session at once, and `./attn session show shared-one` prints `state      waiting_input (hidden)`. `./attn session list` says the same. In the ledger (⌘⇧L), the `shared-one` row is marked `hidden`.
- **Switch back by id.** The tile's screen names its conversation, `Conversation mock-<id>`. Note `shared-one`'s conversation id before `/new`. Run `wsctl input --id <terminal> --text "/resume mock-<id>" --enter`: `wsctl list` shows the terminal on `shared-one` again, `./attn session show shared-one` drops `(hidden)`, and the conversation `/new` made is listed `idle (hidden)`. Run `/new` once more, then `wsctl input --id <terminal> --text "/agents mock-<id>" --enter`: the terminal is back on `shared-one`.
- **Restart once idle, then now.** Run `./attn codex restart`. It prints `Shared Codex of profile <profile> restarts once no turn runs.`, and `./attn codex status` shows the restart until the line is back to `restart  none`. Run `./attn codex restart --now`: it prints `Restarting shared Codex of profile <profile> now.`, and once the terminal reconnects, peek shows its conversation again. The baseline mock cannot reconnect, so that post-restart assertion fails there. See Gotchas.
- **Status as JSON.** Run `./attn codex status --json`: it prints `executable`, `profile_id` and `profile_name`, and once shared Codex is on, `shared_codex` with `installed_version`, `latest_version`, `restart` and `running_version`.
- **Update without a known install.** Run `./attn codex update`. With the baseline's mock it prints `Could not detect how Codex was installed`, then the status, and exits 1.
- **Update, install and reload tiles.** Make a mock Codex install that reports 1.0.0 with 1.1.0 out, and restart the instance on it:

```bash
X=$(mktemp -d /tmp/drive-attn-codex-install.XXXX) && echo "export X=$X" >> $DRIVE_ENV
echo 1.0.0 > $X/version; echo 1.1.0 > $X/latest
printf '#!/bin/sh\nATTN_MOCK_CODEX_STATE_DIR=%s exec %s "$@"\n' "$X" "$R/app/scripts/real-app-harness/mockAgent.mjs" > $X/codex
chmod +x $X/codex
echo "export ATTN_CODEX_EXECUTABLE=$X/codex" >> $DRIVE_ENV
./attn instance stop-app
"$(./attn instance resolve --field appDaemon)" daemon stop
bridge --launch --wait-ready ping
```

  Start another shared terminal with a new id, `wsctl add-session --cwd $C --agent codex --label shared-update --id shared-update --place` (the earlier `shared-one` and its hidden conversation still exist after the restart). Send the new terminal `hello`, and wait for `waiting_input` so you can resume its conversation after closing it. `bridge dom_wait '{"selector":"[aria-label=\"Codex updates\"]","textIncludes":"Codex 1.1.0 is available.","timeoutMs":30000}'` matches. Open the palette with `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, wait for `[role="combobox"][aria-label="Commands"]` to take focus, and type `Install Codex update`: `bridge dom_text '{"selector":".palette-list"}'` lists it. Press Escape. Click `[aria-label="Codex updates"] .banner-button` natively: a new shell tile prints `Codex updated to 1.1.0`, and the banner reads `Codex 1.1.0 is running.` with a count of the tiles that still run an older Codex. `./attn codex status` prints `server  1.1.0` and an `old tiles` line naming them. Run the palette's "Reload old Codex tiles" (typed into the palette, then Enter): `.shared-codex-prompt` asks `Reload 1 Codex tile?`. Click `.shared-codex-prompt .mp-button.primary` natively. The banner closes, and `./attn codex status` has no `old tiles` line.
- **Update from the shell, reload from the banner.** Run `echo 1.2.0 > $X/latest` and `./attn codex update`: it prints `Codex updated to 1.2.0` and the status with `restart  restarting`. Once no turn runs, `./attn codex status` prints `server  1.2.0`, and the banner reads `Codex 1.2.0 is running.` with Reload tiles. Click `[aria-label="Codex updates"] .banner-button` natively, then the prompt's `.mp-button.primary`: the `old tiles` line disappears.
- **Restart now, from the palette and the banner.** Start a shared terminal (`--id shared-slow`), send it `slow`, and wait for `working`. Run `./attn codex restart`: `./attn codex status` prints `restart    pending, waiting on shared-slow (turn)`, and the banner reads `Restarts when agents are idle. Waiting on shared-slow (working).` with Restart now. Run the palette's "Restart shared Codex now": the prompt asks `Restart shared Codex now?` and names the running turn. Click its `.mp-button.primary` natively, and `restart` goes back to `none`. Repeat with a new `slow` terminal and click the banner's Restart now, then the prompt's primary button.
- **Retry a failed start.** Copy `$X/codex` aside and replace it with a script that exits 1 when its first argument is `app-server`. Run `./attn codex restart --now`: `./attn codex status` prints `restart    failed: <its stderr>`, and the banner reads `Codex 1.2.0 didn’t start: … nothing was interrupted.` with Retry. Run the palette's "Retry shared Codex restart": `./attn debug daemon-log --grep 'refused to start'` logs a second refusal. Put the working script back and click the banner's Retry natively: `restart` reads `none` and the banner closes.
- **Install from the palette.** Run `echo 1.0.5 > $X/version` and `echo 1.1.0 > $X/latest`, then start another shared terminal: `./attn codex status` prints `installed  1.0.5` and the banner offers 1.1.0. Run the palette's "Install Codex update": `./attn codex status` reaches `installed  1.1.0` and `server  1.1.0`. Dismiss the old-tiles banner with `[aria-label="Dismiss old Codex tiles notice"]`, click `[aria-label="Show Notifications"]` natively, and `.notifications-panel` lists `Codex 1.1.0 is available`. Close the popover with the palette's "Hide notifications".
- **Last tile closes into the ledger.** With the shared tile selected, press ⌘W (`bridge native_key '{"key":"w","modifiers":["command"]}'`). The session the tile showed closes, `./attn session list --closed` lists it, and the session's Codex conversation stays archived until someone resumes it from the ledger.
- **The app-server stops.** Close every other shared session, hidden ones included (`wsctl rm-session --id <session>` for each in `./attn session list`): `./attn codex status` prints `server     not running; the next shared Codex terminal starts it`.
- **Proof.** In `$P`, keep each `./attn codex` command with its output and exit code, `./attn session show shared-one` after the switch, `./attn agent peek shared-one` during the approval, `bridge dom_text '{"selector":"[aria-label=\"Codex updates\"]"}'` at each banner stage, and `bridge capture_screenshot '{"path":"<P>/codex-banner.png"}'` with the banner showing. Name each file for its way in, such as `codex-banner-install.txt`.

## Contexts

- **Durability across restarts**: the app-server runs as a terminal of its own, so it keeps running through app and daemon restarts, and the terminals reconnect and resume the conversations they show. If the app-server crashes, its running turns stop. attn starts the app-server again on the next connection, and hidden sessions whose turns stopped go idle.
- **Profiles**: one app-server per profile. Inside a session, `attn codex` acts on the session's profile. Elsewhere, `attn codex` needs `--profile` when there is more than one profile.
- **Performance and idle cost**: one app-server per profile replaces a Codex process per terminal and uses less memory. With no open shared session and no shared terminal left, the app-server stops.
- **Harnesses**: Codex only. Claude Code has no link, so a second tile on one of its conversations is a second session ([harnesses.md](harnesses.md)).
- **Keyboard and native input**: every banner action is also a palette command while that action applies.
- **Compatibility across versions**: a tile started before a restart keeps its older Codex and denies an approval of a kind that Codex does not know. Reload tiles moves the tile to the new Codex. A running session stays shared or plain, as it launched, after the switch changes.
- **Flow**: in queue flow a hidden session queues like any other, without a desktop chip, unless "Show hidden sessions in the queue" is off. When the user chooses a hidden session, attn opens it in a tile on the current desktop.

## Tests

- Wire: `internal/daemon/codex_*_wire_test.go`, `internal/daemon/slow_launch_wire_test.go`.
- Stack: `cmd/attn/codex_shared_stack_test.go`.
- Scenario: `codex-shared`, `codex-update`.
- App: `app/src/components/BannerStack.sharedCodex.test.tsx`, `app/src/components/SettingsModal.experimental.test.tsx`, `app/src/App.hiddenSessions.test.tsx`.

## Gotchas

- The shared mock switches only on `/resume <id>` and `/agents <id>`. A bare `/agents` prints nothing and leaves the terminal on its conversation.
- `latest` comes from the daily `codex doctor` check, so writing `$X/latest` mid-run does not change the latest version. `./attn codex update` installs the new version anyway. To make the banner and palette offer an update again, lower `$X/version` instead, as the palette install step does.
- The mock never asks Codex's folder trust question, so you cannot drive the trust answer on the baseline. It needs a real Codex.
- The shared mock app-server imports `ws` from `app/node_modules`. In a fresh worktree run `pnpm --dir app install` first: without it the app-server exits at once, and every shared launch fails with `exited before answering`.
- Under shared Codex the mock plays only `reply` and `delay` actions. A fixture turn with any other action fails, such as `the shared Codex mock does not play attn actions`. Drive an agent's attn verbs through a plain Codex or Claude mock. `attn codex` needs no session, so `./attn` stands in for the agent here.
- The switch decides how new terminals and ledger resumes launch. A Codex session already running stays plain until the session closes.
- attn captures Auto-approve when a terminal launches. Turning it off after launch leaves the running Codex on automatic review, and `pending_approval` can lag by about a minute. Disable it before starting the shared terminal, as the recipe and `codex-shared` scenario do.
- The baseline's `ATTN_CODEX_EXECUTABLE` points at the mock itself, which cannot update. An update needs the pinned install above, and the daemon and app restarted after the env file exports it. The variable wins over the executable set in Settings.
- The shared mock view does not reconnect after a server restart. On the baseline, an idle restart can leave no running server, so the following `restart --now` refuses. A fresh shared terminal lets that command run independently, but this mock still cannot verify the post-restart conversation check. The recipe keeps that assertion as a failed way in.
- CLI status pads its labels. Match each label and value with any spacing.
- If you switch a new tile before its first prompt, no hidden session is left behind. Send a prompt before `/new`.
- attn writes a new conversation to disk with `thread/inject_items` before its tile shows it, so a slow write delays the conversation's start. `attn debug daemon-log --grep 'shared Codex materialize'` gives each write's `elapsed`, its 60s `deadline` and `outcome` (`ok`, `local_deadline`, `server_error`, `disconnected`, `connection_ended` or `no_connection`). A write that fails or misses the deadline refuses the conversation: a Codex that is starting exits with `thread/start failed during TUI bootstrap`, and a running one reports the error and stays in its conversation. A quiet restart waits for a write and names the terminal as starting. A reload ends the write (`connection_ended`), and the terminal stays on the conversation that its replacement resumes.
- The app-server holds a conversation for about a minute after no terminal shows it. Until then, a plain Codex's `/resume` of that conversation fails with `already has an active writer`.
- `attn codex restart --now` stops turns waiting on an approval or a question at once. Use the flag only when the step proves that stop.
- Working but wrong: the banner's single button changes meaning with the stage (Install, Restart now, Retry, Reload tiles), so read the banner's text along with any selector on `.banner-button`.
