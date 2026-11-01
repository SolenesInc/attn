# Outposts

Agents and terminals on other machines, driven from the home's app. An
outpost is another computer running the same attn build, such as a Linux box,
that the home connects to over an SSH bridge. Shells, Claude Code and Codex on
an outpost appear in tiles beside local ones, and their state, turns, cost and
pull request links reach the home. The user adds, re-pairs, updates and
removes outposts in Settings. Agents open shells and delegate onto them with
`--host`. Outposts are experimental and off by default.

## Sub-features

- `outposts-switch`: Outposts on or off at the home. Off keeps the list and makes every outpost terminal offline.
- `bridge-socket`: every daemon accepts its user's bridge connections on its local bridge socket.
- `outpost-pair`: add an outpost by ssh destination, with an optional name. The home pins the outpost's host key and daemon id on the first connection.
- `bridge-states`: connecting, connected, stalled, lost and refused, with the last error.
- `outpost-connect-terminal`: answer ssh prompts in a focused home terminal. After ssh succeeds, the home reconnects automatically, and the user closes the tile.
- `outpost-repair`: Re-pair makes the home forget the pins and connect again.
- `outpost-remove`: Remove makes the home close the outpost's terminals in every profile, release the enrollment, stop the outpost's daemon over ssh, and then forget the outpost. The outpost's files stay. An outpost enrolled to another home keeps that enrollment and its daemon.
- `outpost-update`: use the banner or the row to install the home's Linux build over ssh. Updating works even when a failed first Add left no build.
- `enrollment`: an outpost's data folder belongs to the home it first accepted, until `attn enrollment leave`.
- `remote-terminals`: shells and agents on an outpost in tiles beside local ones. Their attn commands reach the home.
- `offline`: a terminal whose outpost is unreachable stays in place, marked, and sends no typing.
- `ledger-resume`: a closed outpost session's ledger row names the outpost, and resume brings the session back on that outpost with its conversation and cost. For the ledger itself, see [ledger.md](ledger.md).
- `launcher-reads`: `browse_directory`, `inspect_path`, `get_repo_info`, `create_worktree` and `delete_worktree` with `host` run on that outpost, and `agent_catalog` returns its harnesses. Before an outpost removes a worktree, whether by a delete or by the sweep, it asks the home. The home's open seeds keep the worktree's branch and get the removal note, and the home closes the sessions in that worktree. Without the home, the outpost refuses the removal. The sweep on an outpost also reads the home's open seeds before it decides, so it keeps a worktree a home seed owns, and without the home it decides nothing. Launcher 2.0 sends `host` for a recent on an outpost and for a typed address.
- `launcher-machines`: with an outpost attached, Launcher 2.0 lists recents from every machine with a machine badge. An outpost's recents show `~/…` for the home directory it reported when it last connected, also while it is lost. Enter, Tab's where list, ⌘Enter and ⌘⌫ act on the row's machine. The harness strip offers every harness installed on any machine. A start on an unreachable machine, or with a harness the target machine lacks, is refused in the dialog. A start on a stalled machine waits, and Esc leaves it to finish in the background.
- `launcher-addresses`: Launcher 2.0 reads `<machine>:<rest>` as an address. `ai-sandbox:~/` browses ai-sandbox, `ai-sandbox:attn` searches recents and aims them at ai-sandbox after it confirms the folder, and no prefix (or `home:`) means This Mac. An outpost's id also works, and a name two outposts share lists both, completing the picked one by its id. Typing a machine's name offers it, and Tab completes `<name>:~/`. While an outpost is browsed, a status line says it is asking, the folder is missing, or the machine is unreachable. A folder missing on the target lists the other machines that have it. Opening prefetches each reachable outpost's home folder and its recents' parent folders, for that opening only.
- `notebook-sync`: the home and its outposts keep one notebook in sync, and the outpost's row shows the sync. For the notebook itself, see [notebook.md](notebook.md).

## How to get to it

### People

- **Settings → Experimental**: the Outposts switch, `[role="switch"][aria-label="Outposts"]` (`app/src/components/SettingsModalSections.tsx`). Opening Settings is [settings.md](settings.md).
- **Settings → Outposts** (`app/src/components/OutpostsSettings.tsx`): the "Add an outpost" form with the required ssh destination, optional Name and "Add outpost". Each row shows its state, build, daemon id, host key, last error and notebook sync. Each row also offers "Connect in a terminal" for ssh authentication and host-key refusals, "Update to home build", "Re-pair" and "Remove". "Remove" shows a prompt reading "Remove and close N terminals" or "Remove and forget N terminals" with Cancel while the outpost has terminals.
- The banner "Update outposts", with its dismiss button (`app/src/components/OutpostUpdateBanner.tsx`). Dismissing hides the banner until the home build changes. After a successful update, the banner counts down from 5 seconds and closes itself. Hovering pauses the countdown. A later update shows the banner again.
- Where people see and steer what runs there: an outpost terminal's tile header shows the outpost's name after the title, and the tile's strip says "<name> not responding" while stalled, "Connecting to <name> · typing is not sent" while connecting, or "<name> unreachable · typing is not sent" while offline (`app/src/components/SessionTerminalDesktop/DesktopAgentIdentity.tsx`, `app/src/components/SessionTerminalDesktop/DesktopOfflineNotice.tsx`). ⌘D (`terminal.splitVertical`) in an outpost shell opens another shell on the same outpost. A closed outpost session's row in the Sessions ledger shows the outpost's name, and the row's "Reopen" resumes the session on that outpost (`app/src/components/ledger/SessionsTab.tsx`). For the ledger itself, see [ledger.md](ledger.md).
- Launcher 2.0 (`app/src/components/NewSessionLauncher.tsx`): each recent row carries `[data-testid="launcher-machine"]` with the machine's name and `data-state`, and a row on an unreachable machine has `data-reach="unreachable"` and is dimmed. A harness missing on the highlighted row's machine has the class `agent-option--missing` and is struck through. Typing a machine's name shows a row with `data-kind="machine"` under "Browse a machine"; a folder found on another machine shows `data-kind="elsewhere"` under "Found elsewhere". The address prefix in the input is `.path-input-accent`, and the status line under the input is `[data-testid="launcher-status"]`.
- Their own shell: on the home, run `attn settings set outposts_enabled true`. On the outpost, run `attn settings list --all` for `outpost_state`, `outpost_home` and `outpost_last_error`, and use `attn enrollment`, below. Setup is in `docs/guide/reference/using-outposts.md`.

### Agents

- `attn terminal open --host <outpost|home>` opens a shell on that machine beside the caller (`cmd/attn/terminal.go`). For the verb itself, see [terminals.md](terminals.md). An agent on an outpost opens on its own machine by default, and `--host home` reaches the home.
- `attn delegate --host <outpost|home>` starts a delegate on that machine, and that machine checks the folder, refs and worktree. For the verb itself, see [delegation.md](delegation.md).
- `attn session reopen <id>` resumes a closed outpost session on the outpost it ran on, and refuses naming the host while that outpost is unreachable or removed (`cmd/attn/session_reopen.go`). For the verb itself, see [ledger.md](ledger.md).
- `attn enrollment status` and `attn enrollment leave [--home <daemon-id>]` (`cmd/attn/enrollment.go`). Run them on an outpost to see or release the home that outpost is enrolled to. With `--home`, the outpost keeps an enrollment to any other home, and the status reads `kept`.
- An agent on an outpost runs every other attn verb through its outpost daemon, which reaches the home. When the home is unreachable, the command fails at once and names the home.

### Triggers

- The home dials each outpost, holds the bridge with heartbeats every 0.5 seconds, marks it stalled after 3 seconds of silence and lost after 60, and redials (`internal/daemon/outposts.go`, `internal/bridge`).
- After a new home build, the banner offers to update every outpost on a different build. Update builds or downloads, sends, verifies and reconnects on the home's build (`internal/daemon/outpost_update.go`).
- A message or ring to an agent on an unreachable outpost waits at the home and is delivered when the outpost reconnects. See [inbox.md](inbox.md) for delivery.

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers, then run `bridge dismiss_whats_new` to dismiss What's new.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-outposts.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-outposts-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, and save this fixture as `$A/.attn-mock-agent.json`:

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
- **Turn Outposts on in Settings.** Press ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`) and click `[data-testid="settings-nav-experimental"]` natively, then `[role="switch"][aria-label="Outposts"]`. The switch shows `aria-checked="true"` and reads `Disable`, `./attn settings get outposts_enabled` prints `true`, and `[data-testid="settings-nav-outposts"]` appears. `wsctl outposts` now prints an `outposts_updated` event with an empty `outposts` list.
- **Enrollment on the home.** Run `./attn enrollment status`. It prints `daemon:` with this daemon's id and `enrollment: home`. `./attn enrollment leave` prints `not enrolled to another daemon`.
- **Add an outpost in Settings.** Click `[data-testid="settings-nav-outposts"]` natively, click `#outpost-name` and type `drive-op` with `bridge native_text`, click `#outpost-destination` and type `unreachable.invalid`, then press Enter. `bridge dom_text '{"selector":".settings-outpost[aria-label=\"drive-op\"] .settings-pill"}'` reads `lost`, since ssh cannot resolve that destination. `connecting` may show for a moment first. Authentication and host-key failures read `refused` and show ssh's stderr. `wsctl outposts` prints the row with the same `state` and its `id`.
- **Answer ssh in a terminal.**
  - Add a destination whose host key is unknown or whose authentication needs your answer. That outpost's row reads `refused`, shows ssh's stderr verbatim and offers **Connect in a terminal**.
  - Tab to the button and press Enter: Settings closes and a focused home terminal opens beside the active tile.
  - Answer ssh's host-key, passphrase, password, 2FA or security-key prompt there. When ssh succeeds, `wsctl outposts` reads `connected` without another action.
  - The terminal stays open with its output. Close it with ⌘W. The terminal appears in no queue, home-screen agent list or live or closed ledger list.
  - If you answer `no` at the host-key prompt, ssh exits 255 and the tile stays with its output and a Resume button. Resume runs ssh again in the same `Connect to <name>` tile, and **Connect in a terminal** then focuses that tile instead of opening another. After a successful connection, Resume refuses with `outpost <name> has no ssh refusal to answer in a terminal`.
  - With a changed host key, the terminal shows ssh's refusal and the row stays refused. Follow ssh's message after checking the machine's identity, then try again.
  - Background master creation pauses while the prompt is open, including across a home daemon restart. Remove and Outposts off cancel an unfinished prompt before stopping the outpost's master.
- **The name home is reserved.** Click `#outpost-name` natively and type `home`, click `#outpost-destination` and type any destination, then press Enter. `bridge dom_text '{"selector":".settings-warning[role=\"alert\"]"}'` reads `outpost name "home" is reserved for the home`.
- **An agent reaches for the outpost.** Run `wsctl add-session --cwd $A --agent claude --label op-agent --id op-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "remote shell" --enter`. `./attn agent peek op-agent` shows the refusal `drive-op is unreachable; this needs it now`. `wsctl input --id <terminal> --text "missing host" --enter` shows `outpost "nowhere" does not exist`. `wsctl input --id <terminal> --text "remote delegate" --enter` shows `delegate: drive-op is unreachable; this needs it now`, refused before the delegation is accepted.
- **Re-pair.** Click `.settings-outpost[aria-label="drive-op"] .settings-outpost-actions button:first-child` ("Re-pair") natively. The pill reads `lost` again, after a moment of `connecting`. Nothing was pinned here, since the outpost never answered. `go test ./cmd/attn -run TestSSH -v` below checks pins.
- **Remove.** Click `.settings-outpost[aria-label="drive-op"] .settings-outpost-actions button:last-child` ("Remove") natively. With no terminals, the row leaves at once: `.settings-outpost[aria-label="drive-op"]` is absent and `wsctl outposts` lists no outposts.
- **Turn Outposts off.** Run `./attn settings set outposts_enabled false`. It prints `outposts_enabled = false`. `[data-testid="settings-nav-outposts"]` leaves the nav, and `wsctl outposts` refuses again.
- **Install from scratch, update and remove.** Run `node scripts/verify-ssh-outposts/install.mjs --help` for the named Mac and Linux check. It checks fresh Add, Notebook sync, shell survival through Update, remote daemon stop on Remove, retained files and unchanged default-instance files and processes. Keep its receipts, including upload time.
- **Remove a refused Add.** Run `./scripts/test-go.sh --packages -run TestOutpostsRemovingARefusedAddKeepsTheOtherHomesShell -v ./cmd/attn`. A first home pairs and runs a shell. A second home's Add is refused, and the second home's Remove leaves the first home's enrollment, bridge and shell PID in place.
- **Another home's Add on another build.** Run `./scripts/test-go.sh --packages -run TestOutpostsAnotherHomesAddOnAnotherBuildLeavesTheOutpostAlone -v ./cmd/attn`. Before it installs anything, attn refuses a second home whose build differs from the first home's, and the refusal says the outpost is paired to the first home. The outpost's binary is unchanged, and the first home's bridge, build and shell PID stay.
- **Finish Add after authentication.** Run `go test ./cmd/attn -run TestSSHUnpairedAddFinishesAfterAuthentication -v`. In the test, ssh first refuses a host key. The test then creates the master out of band, as the terminal does, and checks that installation and pairing finish without Update. Its restart case checks that the saved unpaired row resumes Add. `TestSSHDisablingOutpostsCancelsUnpairedAdd` holds an external request and checks that turning Outposts off cancels Add without a later install.
- **No repeat install after a refusal.** Run `go test ./internal/daemon -run TestAnUnpairedOutpostDoesNotRepeatARefusedInstall -v`. The test fakes the clock and the external SSH refusal. It checks that the home makes one install attempt across repeated retries and continued probes, and one new attempt after Update.
- **Pair through ssh.** Run `go test ./cmd/attn -run TestSSH -v`. The fake ssh models a Linux machine, and the real `_bridge` starts its daemon. The test opens a shell, exits its master, observes lost then connected, reattaches and types again. For a real route, run `scripts/verify-ssh-outposts/transport.mjs` with a fresh named instance. Add installs the matching binary.
- **Typing order and unanswered keys.** Run `./scripts/test-go.sh --packages -run 'TestRemote(Input|Reload|TerminalRefusals|TileActions|Wait)' -v ./cmd/attn`. It drives real paired daemons through the app protocol: later keys echo while an earlier answer is held, resizes precede later keys, reloads wait, and remote actions leave local typing responsive. Keys still unanswered at bridge loss produce probe and support-trace errors without a refusal flash. A refusal of keys sent while offline or connecting names the terminal, and those keys never reach the terminal. Use `scripts/verify-ssh-outposts/transport.mjs --mode idle` to measure typing echo on a real route.
- **Shells, agents, offline tiles and updates.**
  - Add the Linux box's ssh destination in Settings. Add installs the home's build for the same instance name.
  - Run `wsctl add-session --host <name> --agent shell --cwd <dir>`: it opens a shell there and its tile header's `.desktop-pane-host` reads the outpost's name.
  - With that shell's tile active, press ⌘D (`bridge native_key '{"key":"d","modifiers":["command"]}'`): a second shell opens beside it with the same `.desktop-pane-host`.
  - Make only the test outpost's connection silent: after 3 seconds, `.desktop-pane-offline-strip` reads `<name> not responding`. After 60 seconds, the strip reads `<name> unreachable · typing is not sent` and the row's pill reads `lost`.
  - Restore the connection: the strip goes and the tile shows the current screen.
  - Re-pair while that outpost cannot answer: its tiles read `Connecting to <name> · typing is not sent`. Restore it and check that this notice clears too.
  - With a different build, the "Update outposts" banner and the row's "Update to home build" install the home's build and reconnect. The shell keeps its PID.
  - The baseline cannot do this, as Gotchas says.
- **Launcher reads on an outpost.** Run `./scripts/test-go.sh --packages -run TestLauncherReadsRunOnTheNamedOutpost -v ./cmd/attn`. Through the app protocol, each read with `host` answers from the outpost's registry and harnesses, and with the outpost lost each fails at once with `<name> is unreachable; this needs it now`. On a Linux box, `wsctl request --event browse_directory_result --json '{"cmd":"browse_directory","host":"<name>","input_path":"~/","request_id":"b1"}'` lists that machine's home with its `home_path`, and `--json '{"cmd":"agent_catalog","host":"<name>","request_id":"c1"}' --event agent_catalog_result` lists the harnesses installed there.
- **Addresses in the launcher.** With an outpost added and Launcher 2.0 on, press ⌘N (`bridge native_key '{"key":"n","modifiers":["command"]}'`) and type `ai` with `bridge native_text`: the first row is `[data-kind="machine"]` reading `ai-sandbox:`. Press Tab natively: the input reads `ai-sandbox:~/` and the rows are that machine's folders under "Folders in ~/ on ai-sandbox". Type a folder that only another machine has: `[data-testid="launcher-status"]` reads `<dir> is not on <name>` and a `[data-kind="elsewhere"]` row names the machine that has it. ⌘T, an address and Enter open a shell there. The baseline cannot do this: it needs a reachable outpost.
- **Resume an outpost session from the ledger.** On the same Linux box, with Claude Code installed and signed in there:
  - Run `wsctl add-session --host <name> --agent claude --cwd <dir> --label op-claude --id op-claude`, send it `wsctl input --id <terminal> --text "hello" --enter`, and wait for `waiting_input` in `./attn agent list`.
  - Close it with `./attn agent close op-claude -m "outpost drive" --source-session op-claude`.
  - Press ⌘⇧L (`bridge native_key '{"key":"l","modifiers":["command","shift"]}'`) and set the Closed list for setup with `bridge sessions_set_filter '{"scope":"Closed"}'`: `bridge dom_text '{"selector":".ledger-row[data-row-key=\"op-claude\"]"}'` includes the outpost's name, and the row offers no Keep conversation or Show worktree.
  - Click `.ledger-row[data-row-key="op-claude"] .ledger-row-trailing .ledger-verb` ("Reopen") natively: `op-claude` comes back under the same id, its tile's `.desktop-pane-host` reads the outpost's name, and after the next turn `./attn session show op-claude`'s `usage` line counts the earlier tokens and the new ones.
  - Close it again and run `./attn session reopen op-claude`: it prints `op-claude reopened in <dir>` and returns on the same outpost.
  - Close it once more, cut the outpost's network, and once its row's pill reads `lost` run `./attn session reopen op-claude`: it exits 1 with `session reopen: <name> is unreachable; this needs it now`, and `./attn session show op-claude` still shows `state      closed`.
  - The baseline cannot do this either.
- **Proof.** In `$P`, keep these:
  - each `wsctl outposts` and `./attn` command's output with its exit code
  - `bridge dom_text` of the outpost row after each state change
  - `./attn agent peek op-agent` after each turn
  - `bridge dom_text` of the `op-claude` ledger row and `./attn session show op-claude` after each close, resume and refusal
  - the output of `go test ./cmd/attn -run TestSSH -v`
  - `bridge capture_screenshot '{"path":"<P>/outposts-settings.png"}'` with the Outposts section showing the row, where `<P>` is the folder's path written out

  Name each file for its way in, such as `outposts-settings-add.txt`.

## Contexts

- **Durability across restarts**: the outpost list, its pins and an outpost's enrollment are kept by each daemon, and the home redials after any restart. A shell on an outpost keeps running through a lost bridge and a restart of either daemon, and the shell's screen comes back on reconnect. If the shell's process is gone, the home marks the shell recoverable, and Resume starts a new shell in its place. A home restart resumes an accepted delegation onto an outpost on the same machine and path.
- **Linux**: the outpost daemon runs on Linux detached from ssh, built with `make build-linux-amd64` or `make build-linux-arm64`. `scripts/verify-ssh-outposts/transport.mjs` checks that its daemon and shell keep their PIDs after a master exit. The app stays on the Mac.
- **Profiles**: outposts belong to the home and serve every profile. A terminal on an outpost belongs to the profile that started it, and Remove closes the outpost's terminals in every profile.
- **Performance and idle cost**: each connected bridge exchanges a heartbeat every 0.5 seconds. `scripts/verify-ssh-outposts/transport.mjs --mode idle` measures typing latency against an echo through the same master. The `outposts-settings` scenario measures idle CPU with connected, stalled, connecting and unreachable tiles, and checks that the notices leave no running tile animation. It does not count heartbeat-related facts.
- **Harnesses**: Claude Code and Codex run on an outpost with that machine's own executable and credentials. Plugin harnesses, the chief and the crew run only at the home, and attn refuses a request to start them on an outpost and names that limit.
- **Keyboard and native input**: ⌘D in an outpost shell opens another shell on that outpost. Keys, resizes and pointer activity stream in order while connected or stalled. The tile flashes red when a key is refused before it reaches the bridge or refused by the outpost. The tile counts those keys as possibly unsent. An unanswered key at bridge loss does not flash and may still run. attn retries no key.
- **Compatibility across versions**: the home and the outpost must run the same build and the same instance name. Add continues until first pairing, including after ssh authentication or a home restart. After a failed install, attn starts no new install until the user chooses Update or Re-pair. A reconnect retries only the probe. Re-pair clears the pin and repeats Add. The home refuses a pinned reconnect to a different build or to an outpost with nothing installed. Choose Update in the row or banner to install the home's build.
- **Flow**: an offline agent keeps its queue row, marked, but Next, automatic advance, ⌘J and the waiting counts skip it, and settle, snooze, unsnooze and countdown cancel are hidden.

## Tests

- Wire: `internal/bridge/*_test.go`, `internal/daemon/outposts_wire_test.go`, `internal/daemon/outpost_forward_transcript_wire_test.go`.
- Stack: `cmd/attn/outposts_stack_test.go`, `cmd/attn/outpost_connect_terminal_stack_test.go`, `cmd/attn/outpost_*_stack_test.go`, `cmd/attn/remote_*_stack_test.go`, `cmd/attn/enrollment_stack_test.go`, `cmd/attn/settings_outpost_stack_test.go`, `cmd/attn/delegate_outpost_stack_test.go`, `cmd/attn/session_reopen_outpost_stack_test.go`, `cmd/attn/launcher_reads_stack_test.go`, `cmd/attn/remote_worktree_delete_stack_test.go`, `scripts/verify-outpost-ledger-resume.sh`, `app/e2e/outpost-terminal-links.spec.ts`.
- Scenario: `outposts-settings`, `outpost-update`, `notebook-sync`.
- Kernel: `internal/enrollment/*_test.go`, `internal/outpostbuild/*_test.go`.
- App: `app/src/App.outpostsSettings.test.tsx`, `app/src/App.outpostUpdates.test.tsx`, `app/src/App.remoteAgents.test.tsx`, `app/src/App.remoteShell.test.tsx`, `app/src/App.outpostPaths.test.tsx`, `app/src/App.outpostsNotebook.test.tsx`, `app/src/App.newSessionLauncher.outposts.test.tsx`, `app/src/components/ledger/SessionsTab.outposts.test.tsx`.

## Gotchas

- The baseline has nothing to connect to: an added outpost ends `lost`, or `refused` when ssh refuses authentication or a host key. `go test ./cmd/attn -run TestSSH -v` proves the bridge states only. Shells, agents, offline tiles, ledger resume, notebook sync and updates need a Linux box with the setup in `docs/guide/reference/using-outposts.md`. That outpost runs its machine's own Claude Code and Codex, never the mock agent.
- A named instance pairs only with an outpost of the same instance name, never with production or another name. `go test ./cmd/attn -run TestSSH -v` sidesteps this with two daemons in temporary data folders.
- Every outpost command refuses while Outposts is off: `outposts_enabled is off; run attn settings set outposts_enabled true before you add an outpost`.
- The bridge uses the user's OpenSSH configuration. Every background command uses BatchMode and shows ssh's stderr on failure. The master survives a lost bridge and a home restart. Remove, Outposts off and instance clean exit the master.
- `attn enrollment` reads the data folder of the attn it runs as. Run it on the outpost to see the outpost's side. On the home it always says `home`.
- Stalled and connecting notices appear only on tiles. Sidebar, queue and home screen offline marking still follows reachability. Connecting refuses typing, while stalled keeps sending typing.
- Working but wrong: the Experimental switch and the list of outposts share the accessible name "Outposts", so select the switch by `[role="switch"]`. The ledger's "Reopen" and `attn session reopen` say Reopen where the glossary says resume. Files on an outpost do not open on the Mac yet: Open in editor and ⌘-clicked paths in an outpost shell fail and name the outpost.
