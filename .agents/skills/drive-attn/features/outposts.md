# Outposts

Agents and terminals on other machines, driven from the home's app. An
outpost is another computer running the same attn build, such as a Linux box,
that the home connects to over an SSH bridge. Shells, Claude Code and Codex on
an outpost appear in tiles beside local ones, and their state, turns, cost and
pull request links reach the home. The user adds, re-pairs, updates and
removes outposts in Settings; agents open shells and delegate onto them with
`--host`. Outposts are experimental and off by default.

## Sub-features

- `outposts-switch`: Outposts on or off at the home; off keeps the list and makes every outpost terminal offline.
- `bridge-socket`: every daemon accepts its user's bridge connections on its local bridge socket.
- `outpost-pair`: add an outpost by ssh destination, with an optional name; the first connection pins its host key and daemon id.
- `bridge-states`: connecting, connected, stalled, lost and refused, with the last error.
- `outpost-connect-terminal`: answer ssh prompts in a focused home terminal; success reconnects automatically, and the user closes the tile.
- `outpost-repair`: Re-pair forgets the pins and connects again.
- `outpost-remove`: Remove closes the outpost's terminals in every profile, releases enrollment and stops its daemon over ssh, then forgets it. Its files stay. An outpost enrolled to another home keeps its enrollment and daemon.
- `outpost-update`: the banner and row install the home's Linux build over ssh, including when a failed first Add left no build.
- `enrollment`: an outpost's data folder belongs to the home it first accepted, until `attn enrollment leave`.
- `remote-terminals`: shells and agents on an outpost in tiles beside local ones; their attn commands reach the home.
- `offline`: a terminal whose outpost is unreachable stays in place, marked, and sends no typing.
- `ledger-resume`: a closed outpost session's ledger row names its outpost, and resume brings it back there with its conversation and cost. The ledger is [ledger.md](ledger.md)'s.
- `notebook-sync`: the home and its outposts keep one notebook in sync; the outpost's row shows the sync. The notebook is [notebook.md](notebook.md)'s.

## How to get to it

### People

- Settings, Experimental: the Outposts switch, `[role="switch"][aria-label="Outposts"]` (`app/src/components/SettingsModalSections.tsx`). Opening Settings is [settings.md](settings.md).
- Settings, Outposts (`app/src/components/OutpostsSettings.tsx`): the "Add an outpost" form with the required ssh destination, optional Name and "Add outpost"; each row's state, build, daemon id, host key, last error and notebook sync; and "Connect in a terminal" for ssh authentication and host-key refusals, "Update to home build", "Re-pair" and "Remove", which asks "Remove and close N terminals" or "Remove and forget N terminals" with Cancel while the outpost has terminals.
- The banner "Update outposts", with its dismiss button (`app/src/components/OutpostUpdateBanner.tsx`).
- Where people see and steer what runs there: an outpost terminal's tile header shows the outpost's name after the title, and its strip says "<name> not responding" while stalled, "Connecting to <name> · typing is not sent" while connecting, or "<name> unreachable · typing is not sent" while offline (`app/src/components/SessionTerminalDesktop/DesktopAgentIdentity.tsx`, `app/src/components/SessionTerminalDesktop/DesktopOfflineNotice.tsx`). ⌘D (`terminal.splitVertical`) in an outpost shell opens another shell on the same outpost. A closed outpost session's row in the Sessions ledger shows the outpost's name, and its "Reopen" resumes the session on that outpost (`app/src/components/ledger/SessionsTab.tsx`); [ledger.md](ledger.md) owns the ledger.
- Their own shell: on the home, `attn settings set outposts_enabled true`; on the outpost, `attn settings list --all` for `outpost_state`, `outpost_home` and `outpost_last_error`, and `attn enrollment`, below. Setup is in `docs/using-outposts.md`.

### Agents

- `attn terminal open --host <outpost|home>` opens a shell on that machine beside the caller (`cmd/attn/terminal.go`); [terminals.md](terminals.md) owns the verb. An agent on an outpost opens on its own machine by default, and `--host home` reaches the home.
- `attn delegate --host <outpost|home>` starts a delegate on that machine, where the folder, refs and worktree are checked; [delegation.md](delegation.md) owns it.
- `attn session reopen <id>` resumes a closed outpost session on the outpost it ran on, and refuses naming the host while that outpost is unreachable or removed (`cmd/attn/session_reopen.go`); [ledger.md](ledger.md) owns the verb.
- `attn enrollment status` and `attn enrollment leave [--home <daemon-id>]` (`cmd/attn/enrollment.go`), run on an outpost to see or release the home it is enrolled to; with `--home`, an enrollment to any other home is kept and the status reads `kept`.
- An agent on an outpost runs every other attn verb through its outpost daemon, which reaches the home; when the home is unreachable, the command fails at once and names it.

### Triggers

- The home dials each outpost, holds the bridge with heartbeats every 0.5 seconds, marks it stalled after 3 seconds of silence and lost after 60, and redials (`internal/daemon/outposts.go`, `internal/bridge`).
- After a new home build, the banner offers to update every outpost on a different build; Update builds or downloads, sends, verifies and reconnects on the home's build (`internal/daemon/outpost_update.go`).
- A message or ring to an agent on an unreachable outpost waits at the home and is delivered when it reconnects; [inbox.md](inbox.md) covers delivery.

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-outposts.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-outposts-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "remote shell", "actions": [
    {"type": "attn", "args": ["terminal", "open", "--host", "drive-op"]}]},
  {"includes": "missing host", "actions": [
    {"type": "attn", "args": ["terminal", "open", "--host", "nowhere"]}]},
  {"includes": "remote delegate", "actions": [
    {"type": "attn", "args": ["delegate", "--host", "drive-op", "--agent", "shell", "--model", "none", "--cwd", "/tmp", "--name", "op-delegate", "--brief", "Drive the outpost refusal"]}]}
]}
```

`<terminal>` below is the agent's terminal id from `wsctl list`.

- **Off by default.** Run `wsctl outposts`. It exits 1 with `error: outposts_enabled is off`. `./attn settings get outposts_enabled` prints an empty line.
- **Turn Outposts on in Settings.** Press ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`) and click `[data-testid="settings-nav-experimental"]` natively, then `[role="switch"][aria-label="Outposts"]`. It shows `aria-checked="true"` and reads `Disable`, `./attn settings get outposts_enabled` prints `true`, and `[data-testid="settings-nav-outposts"]` appears. `wsctl outposts` now prints an `outposts_updated` event with an empty `outposts` list.
- **Enrollment on the home.** Run `./attn enrollment status`. It prints `daemon:` with this daemon's id and `enrollment: home`. `./attn enrollment leave` prints `not enrolled to another daemon`.
- **Add an outpost in Settings.** Click `[data-testid="settings-nav-outposts"]` natively, click `#outpost-name` and type `drive-op` with `bridge native_text`, click `#outpost-destination` and type `unreachable.invalid`, then press Enter. `bridge dom_text '{"selector":".settings-outpost[aria-label=\"drive-op\"] .settings-pill"}'` reads `lost`, since ssh cannot resolve that destination; `connecting` may show for a moment first. Authentication and host-key failures read `refused` and show ssh's stderr. `wsctl outposts` prints the row with the same `state` and its `id`.
- **Answer ssh in a terminal.** Add a destination whose host key is unknown or whose authentication needs your answer. Its row reads `refused`, shows ssh's stderr verbatim and offers **Connect in a terminal**. Tab to the button and press Enter: Settings closes and a focused home terminal opens beside the active tile. Answer ssh's host-key, passphrase, password, 2FA or security-key prompt there. When ssh succeeds, `wsctl outposts` reads `connected` without another action. The terminal stays open with its output; close it with ⌘W. It appears in no queue, home-screen agent list or live or closed ledger list. If you answer `no` at the host-key prompt, ssh exits 255 and the tile stays with its output and a Resume button. Resume runs ssh again in the same `Connect to <name>` tile, and **Connect in a terminal** then focuses that tile instead of opening another. After a successful connection, Resume refuses with `outpost <name> has no ssh refusal to answer in a terminal`. With a changed host key, the terminal shows ssh's refusal and the row stays refused. Follow ssh's message after checking the machine's identity, then try again. Background master creation pauses while the prompt is open, including across a home daemon restart. Remove and Outposts off cancel an unfinished prompt before stopping its master.
- **The name home is reserved.** Click `#outpost-name` natively and type `home`, click `#outpost-destination` and type any destination, then press Enter. `bridge dom_text '{"selector":".settings-warning[role=\"alert\"]"}'` reads `outpost name "home" is reserved for the home`.
- **An agent reaches for the outpost.** Run `wsctl add-session --cwd $A --agent claude --label op-agent --id op-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "remote shell" --enter`. `./attn agent peek op-agent` shows the refusal `drive-op is unreachable; this needs it now`. `wsctl input --id <terminal> --text "missing host" --enter` shows `outpost "nowhere" does not exist`. `wsctl input --id <terminal> --text "remote delegate" --enter` shows `delegate: drive-op is unreachable; this needs it now`, refused before the delegation is accepted.
- **Re-pair.** Click `.settings-outpost[aria-label="drive-op"] .settings-outpost-actions button:first-child` ("Re-pair") natively. The pill reads `lost` again, after a moment of `connecting`. Nothing was pinned here, since the outpost never answered; `go test ./cmd/attn -run TestSSH -v` below proves pins.
- **Remove.** Click `.settings-outpost[aria-label="drive-op"] .settings-outpost-actions button:last-child` ("Remove") natively. With no terminals, the row leaves at once: `.settings-outpost[aria-label="drive-op"]` is absent and `wsctl outposts` lists no outposts.
- **Turn Outposts off.** Run `./attn settings set outposts_enabled false`; it prints `outposts_enabled = false`. `[data-testid="settings-nav-outposts"]` leaves the nav, and `wsctl outposts` refuses again.
- **Install from scratch, update and remove.** Run `node scripts/verify-ssh-outposts/install.mjs --help` for the named Mac and Linux check. It proves fresh Add, Notebook sync, shell survival through Update, remote daemon stop on Remove, retained files and unchanged default-instance files and processes. Keep its receipts, including upload time.
- **Remove a refused Add.** Run `./scripts/test-go.sh --packages -run TestOutpostsRemovingARefusedAddKeepsTheOtherHomesShell -v ./cmd/attn`. A first home pairs and runs a shell; a second home's Add is refused, and its Remove leaves the first home's enrollment, bridge and shell PID in place.
- **Finish Add after authentication.** Run `go test ./cmd/attn -run TestSSHUnpairedAddFinishesAfterAuthentication -v`. It first refuses a host key, creates the master out of band as the terminal does, and observes installation and pairing without Update. Its restart case proves the saved unpaired row resumes Add. `TestSSHDisablingOutpostsCancelsUnpairedAdd` holds an external request and proves Outposts off cancels Add without a later install.
- **A failed install waits for action.** Run `go test ./internal/daemon -run TestAnUnpairedOutpostDoesNotRepeatARefusedInstall -v`. With a fake clock and external SSH refusal, it proves one install attempt across repeated retries, continued probes, and one new attempt after Update.
- **Pair through ssh.** Run `go test ./cmd/attn -run TestSSH -v`. The fake ssh models a Linux machine, and the real `_bridge` starts its daemon. The test opens a shell, exits its master, observes lost then connected, reattaches and types again. For a real route, run `scripts/verify-ssh-outposts/transport.mjs` with a fresh named instance; Add installs the matching binary.
- **Typing order and unanswered keys.** Run `./scripts/test-go.sh --packages -run 'TestRemote(Input|Reload|TerminalRefusals|TileActions|Wait)' -v ./cmd/attn`. It drives real paired daemons through the app protocol: later keys echo while an earlier answer is held, resizes precede later keys, reloads wait, and remote actions leave local typing responsive. Keys still unanswered at bridge loss produce probe and support-trace errors without a refusal flash. Keys refused while offline or connecting name their terminal and never reach it. Use `scripts/verify-ssh-outposts/transport.mjs --mode idle` to measure typing echo on a real route.
- **Shells, agents, offline tiles and updates.** Add the Linux box's ssh destination in Settings. Add installs the home's build for the same instance name. `wsctl add-session --host <name> --agent shell --cwd <dir>` opens a shell there: its tile header's `.desktop-pane-host` reads the outpost's name. With that shell's tile active, press ⌘D (`bridge native_key '{"key":"d","modifiers":["command"]}'`): a second shell opens beside it with the same `.desktop-pane-host`. Make only the test outpost's connection silent: after 3 seconds, `.desktop-pane-offline-strip` reads `<name> not responding`; after 60 seconds, it reads `<name> unreachable · typing is not sent` and the row's pill reads `lost`. Restore the connection: the strip goes and the tile shows the current screen. Re-pair while that outpost cannot answer: its tiles read `Connecting to <name> · typing is not sent`. Restore it and check that this notice clears too. With a different build, the "Update outposts" banner and the row's "Update to home build" install the home's build and reconnect; the shell keeps its PID. The baseline cannot do this, as Gotchas says.
- **Resume an outpost session from the ledger.** On the same Linux box, with Claude Code installed and signed in there, run `wsctl add-session --host <name> --agent claude --cwd <dir> --label op-claude --id op-claude`, send it `wsctl input --id <terminal> --text "hello" --enter`, and wait for `waiting_input` in `./attn agent list`. Close it with `./attn agent close op-claude -m "outpost drive" --source-session op-claude`. Press ⌘⇧L (`bridge native_key '{"key":"l","modifiers":["command","shift"]}'`) and set the Closed list for setup with `bridge sessions_set_filter '{"scope":"Closed"}'`: `bridge dom_text '{"selector":".ledger-row[data-row-key=\"op-claude\"]"}'` includes the outpost's name, and the row offers no Keep conversation or Show worktree. Click `.ledger-row[data-row-key="op-claude"] .ledger-row-trailing .ledger-verb` ("Reopen") natively: `op-claude` comes back under the same id, its tile's `.desktop-pane-host` reads the outpost's name, and after the next turn `./attn session show op-claude`'s `usage` line counts the earlier tokens and the new ones. Close it again and run `./attn session reopen op-claude`: it prints `op-claude reopened in <dir>` and returns on the same outpost. Close it once more, cut the outpost's network, and once its row's pill reads `lost` run `./attn session reopen op-claude`: it exits 1 with `session reopen: <name> is unreachable; this needs it now`, and `./attn session show op-claude` still shows `state      closed`. The baseline cannot do this either.
- **Proof.** In `$P`, keep each `wsctl outposts` and `./attn` command's output with its exit code; `bridge dom_text` of the outpost row after each state change; `./attn agent peek op-agent` after each turn; `bridge dom_text` of the `op-claude` ledger row and `./attn session show op-claude` after each close, resume and refusal; the output of `go test ./cmd/attn -run TestSSH -v`; and `bridge capture_screenshot '{"path":"<P>/outposts-settings.png"}'` with the Outposts section showing the row, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `outposts-settings-add.txt`.

## Contexts

- **Durability across restarts**: the outpost list, its pins and an outpost's enrollment are kept by each daemon, and the home redials after any restart. A shell on an outpost keeps running through a lost bridge and a restart of either daemon, and its screen comes back on reconnect. If its process is gone, the home marks it recoverable, and Resume starts a new shell in its place. A home restart resumes an accepted delegation onto an outpost on the same machine and path.
- **Linux**: the outpost daemon runs on Linux detached from ssh, built with `make build-linux-amd64` or `make build-linux-arm64`. `scripts/verify-ssh-outposts/transport.mjs` checks that its daemon and shell keep their PIDs after a master exit. The app stays on the Mac.
- **Profiles**: outposts belong to the home and serve every profile. A terminal on an outpost belongs to the profile that started it, and Remove closes the outpost's terminals in every profile.
- **Performance and idle cost**: each connected bridge exchanges a heartbeat every 0.5 seconds. `scripts/verify-ssh-outposts/transport.mjs --mode idle` measures typing latency against an echo through the same master. The `outposts-settings` scenario measures idle CPU with connected, stalled, connecting and unreachable tiles, and checks that the notices leave no running tile animation. It does not count heartbeat-related facts.
- **Harnesses**: Claude Code and Codex run on an outpost with that machine's own executable and credentials. Pi and other plugin harnesses, the chief and the crew run only at the home, and a request to start them on an outpost is refused and names that limit.
- **Keyboard and native input**: ⌘D in an outpost shell opens another shell on that outpost. Keys, resizes and pointer activity stream in order while connected or stalled. A key refused before it reaches the bridge, or refused by the outpost, flashes its tile red; the tile counts those keys as possibly unsent. An unanswered key at bridge loss does not flash and may still run. No key is retried.
- **Compatibility across versions**: the home and the outpost must run the same build and the same instance name. Add continues until first pairing, including after ssh authentication or a home restart. A failed install waits for Update or Re-pair; reconnect retries only probe. Re-pair clears the pin and repeats Add. A pinned reconnect to a different build or nothing installed is refused. Choose Update in the row or banner to install the home's build.
- **Flow**: an offline agent keeps its queue row, marked, but Next, automatic advance, ⌘J and the waiting counts skip it, and settle, snooze, unsnooze and countdown cancel are hidden.

## Tests

- Wire: `internal/bridge/*_test.go`, `internal/daemon/outposts_wire_test.go`, `internal/daemon/outpost_forward_transcript_wire_test.go`.
- Stack: `cmd/attn/outposts_stack_test.go`, `cmd/attn/outpost_connect_terminal_stack_test.go`, `cmd/attn/outpost_*_stack_test.go`, `cmd/attn/remote_*_stack_test.go`, `cmd/attn/enrollment_stack_test.go`, `cmd/attn/settings_outpost_stack_test.go`, `cmd/attn/delegate_outpost_stack_test.go`, `cmd/attn/session_reopen_outpost_stack_test.go`, `scripts/verify-outpost-ledger-resume.sh`, `app/e2e/outpost-terminal-links.spec.ts`.
- Scenario: `outposts-settings`, `outpost-update`, `notebook-sync`.
- Kernel: `internal/enrollment/*_test.go`, `internal/outpostbuild/*_test.go`.
- App: `app/src/App.outpostsSettings.test.tsx`, `app/src/App.outpostUpdates.test.tsx`, `app/src/App.remoteAgents.test.tsx`, `app/src/App.remoteShell.test.tsx`, `app/src/App.outpostPaths.test.tsx`, `app/src/App.outpostsNotebook.test.tsx`, `app/src/components/ledger/SessionsTab.outposts.test.tsx`.

## Gotchas

- The baseline has nothing to connect to: an added outpost ends `lost`, or `refused` when ssh refuses authentication or a host key. `go test ./cmd/attn -run TestSSH -v` proves the bridge states only; shells, agents, offline tiles, ledger resume, notebook sync and updates need a Linux box with the setup in `docs/using-outposts.md`. That outpost runs its machine's own Claude Code and Codex, never the mock agent.
- A named instance pairs only with an outpost of the same instance name, never with production or another name. `go test ./cmd/attn -run TestSSH -v` sidesteps this with two daemons in temporary data folders.
- Every outpost command refuses while Outposts is off: `outposts_enabled is off; run attn settings set outposts_enabled true before you add an outpost`.
- The bridge uses the user's OpenSSH configuration. Every background command uses BatchMode and shows ssh's stderr on failure. The master survives a lost bridge and a home restart; Remove, Outposts off and instance clean exit it.
- `attn enrollment` reads the data folder of the attn it runs as. Run it on the outpost to see the outpost's side; on the home it always says `home`.
- Stalled and connecting notices appear only on tiles. Sidebar, queue and home screen offline marking still follows reachability; connecting refuses typing, while stalled keeps sending it.
- Working but wrong: the Experimental switch and the list of outposts share the accessible name "Outposts"; select the switch by `[role="switch"]`. The ledger's "Reopen" and `attn session reopen` say Reopen where the glossary says resume. Files on an outpost do not open on the Mac yet: Open in editor and ⌘-clicked paths in an outpost shell fail and name the outpost.
