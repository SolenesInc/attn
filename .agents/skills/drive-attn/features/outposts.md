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
- `outpost-listen`: an outpost listens for its home on the address in `outpost_listen`.
- `outpost-pair`: add an outpost by name and address; the first connection pins its host key and daemon id.
- `bridge-states`: connecting, connected, stalled, lost and refused, with the last error.
- `outpost-repair`: Re-pair forgets the pins and connects again.
- `outpost-remove`: Remove closes the outpost's terminals in every profile, then forgets it.
- `outpost-update`: push the home's build to an outpost, from a banner or its row.
- `enrollment`: an outpost's data folder belongs to the home it first accepted, until `attn enrollment leave`.
- `remote-terminals`: shells and agents on an outpost in tiles beside local ones; their attn commands reach the home.
- `offline`: a terminal whose outpost is unreachable stays in place, marked, and sends no typing.
- `ledger-resume`: a closed outpost session's ledger row names its outpost, and resume brings it back there with its conversation and cost. The ledger is [ledger.md](ledger.md)'s.
- `notebook-sync`: the home and its outposts keep one notebook in sync; the outpost's row shows the sync. The notebook is [notebook.md](notebook.md)'s.

## How to get to it

### People

- Settings, Experimental: the Outposts switch, `[role="switch"][aria-label="Outposts"]` (`app/src/components/SettingsModalSections.tsx`). Opening Settings is [settings.md](settings.md).
- Settings, Outposts (`app/src/components/OutpostsSettings.tsx`): the "Add an outpost" form with Name, Address and "Add outpost"; each row's state, build, daemon id, host key, last error and notebook sync; and "Update to home build", "Re-pair" and "Remove", which asks "Remove and close N terminals" or "Remove and forget N terminals" with Cancel while the outpost has terminals.
- The banner "Update outposts", with its dismiss button (`app/src/components/OutpostUpdateBanner.tsx`).
- Where people see and steer what runs there: an outpost terminal's tile header shows the outpost's name after the title, and an offline one shows the strip "<name> unreachable · typing is not sent" (`app/src/components/SessionTerminalDesktop/DesktopAgentIdentity.tsx`, `app/src/components/SessionTerminalDesktop/DesktopOfflineNotice.tsx`). ⌘D (`terminal.splitVertical`) in an outpost shell opens another shell on the same outpost. A closed outpost session's row in the Sessions ledger shows the outpost's name, and its "Reopen" resumes the session on that outpost (`app/src/components/ledger/SessionsTab.tsx`); [ledger.md](ledger.md) owns the ledger.
- Their own shell: on the home, `attn settings set outposts_enabled true`; on the outpost, `attn settings set outpost_listen <address:port>`, `attn settings list --all` for `outpost_state`, `outpost_home` and `outpost_last_error`, and `attn enrollment`, below. Setup is in `docs/using-outposts.md`.

### Agents

- `attn terminal open --host <outpost|home>` opens a shell on that machine beside the caller (`cmd/attn/terminal.go`); [terminals.md](terminals.md) owns the verb. An agent on an outpost opens on its own machine by default, and `--host home` reaches the home.
- `attn delegate --host <outpost|home>` starts a delegate on that machine, where the folder, refs and worktree are checked; [delegation.md](delegation.md) owns it.
- `attn session reopen <id>` resumes a closed outpost session on the outpost it ran on, and refuses naming the host while that outpost is unreachable or removed (`cmd/attn/session_reopen.go`); [ledger.md](ledger.md) owns the verb.
- `attn enrollment status` and `attn enrollment leave` (`cmd/attn/enrollment.go`), run on an outpost to see or release the home it is enrolled to.
- An agent on an outpost runs every other attn verb through its outpost daemon, which reaches the home; when the home is unreachable, the command fails at once and names it.

### Triggers

- The home dials each outpost, holds the bridge with heartbeats every 0.5 seconds, marks it stalled after 3 seconds of silence and lost after 10, and redials (`internal/daemon/outposts.go`, `internal/bridge`).
- After a new home build, the banner offers to update every outpost on a different build; the update builds or downloads the Linux binary, sends it, verifies it and restarts the outpost (`internal/daemon/outpost_update.go`).
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
- **Add an outpost in Settings.** Click `[data-testid="settings-nav-outposts"]` natively, click `#outpost-name` and type `drive-op` with `bridge native_text`, click `#outpost-address` and type `127.0.0.1:9`, then press Enter. `bridge dom_text '{"selector":".settings-outpost[aria-label=\"drive-op\"] .settings-pill"}'` reads `lost`, since nothing listens there; `connecting` may show for a moment first. When the daemon has no ssh-agent key, it reads `refused` and the row's warning starts with `bridge authentication:`. `wsctl outposts` prints the row with the same `state` and its `id`.
- **The name home is reserved.** Click `#outpost-name` natively and type `home`, click `#outpost-address` and type any address, then press Enter. `bridge dom_text '{"selector":".settings-warning[role=\"alert\"]"}'` reads `outpost name "home" is reserved for the home`.
- **An agent reaches for the outpost.** Run `wsctl add-session --cwd $A --agent claude --label op-agent --id op-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "remote shell" --enter`. `./attn agent peek op-agent` shows the refusal `drive-op is unreachable; this needs it now`. `wsctl input --id <terminal> --text "missing host" --enter` shows `outpost "nowhere" does not exist`. `wsctl input --id <terminal> --text "remote delegate" --enter` shows `delegate: drive-op is unreachable; this needs it now`, refused before the delegation is accepted.
- **Re-pair.** Click `.settings-outpost[aria-label="drive-op"] .settings-outpost-actions button:first-child` ("Re-pair") natively. The pill reads `lost` again, after a moment of `connecting`. Nothing was pinned here, since the outpost never answered; `scripts/verify-outposts.py` below proves pins.
- **Remove.** Click `.settings-outpost[aria-label="drive-op"] .settings-outpost-actions button:last-child` ("Remove") natively. With no terminals, the row leaves at once: `.settings-outpost[aria-label="drive-op"]` is absent and `wsctl outposts` lists no outposts.
- **Turn Outposts off.** Run `./attn settings set outposts_enabled false`; it prints `outposts_enabled = false`. `[data-testid="settings-nav-outposts"]` leaves the nav, and `wsctl outposts` refuses again.
- **Pair two daemons.** Build a protocol client with `go build -o $P/wsctl ./scripts/wsctl`, then run `python3 scripts/verify-outposts.py --attn ./attn --wsctl $P/wsctl`. It starts two daemons in temporary data folders with their own ssh-agent, pairs them, freezes the outpost and thaws it, and prints a line ending in `result: connected → stalled → lost → connected; pins retained; last error cleared`, then a `bus:` line counting one `outpost.changed` fact per transition.
- **Shells, agents, offline tiles and updates.** On a Linux box with the home's build and instance name, start an outpost with `scripts/outpost-instance.py` as `docs/using-outposts.md` says, and add it in Settings. `wsctl add-session --host <name> --agent shell --cwd <dir>` opens a shell there: its tile header's `.desktop-pane-host` reads the outpost's name. With that shell's tile active, press ⌘D (`bridge native_key '{"key":"d","modifiers":["command"]}'`): a second shell opens beside it with the same `.desktop-pane-host`. Cut the outpost's network: `.desktop-pane-offline-strip` reads `<name> unreachable · typing is not sent`, and its row's pill reads `lost`. Restore it: the strip goes and the tile shows the current screen. With a different build, the "Update outposts" banner and the row's "Update to home build" walk the row through its steps to the outcome. The baseline cannot do this, as Gotchas says.
- **Resume an outpost session from the ledger.** On the same Linux box, with Claude Code installed and signed in there, run `wsctl add-session --host <name> --agent claude --cwd <dir> --label op-claude --id op-claude`, send it `wsctl input --id <terminal> --text "hello" --enter`, and wait for `waiting_input` in `./attn agent list`. Close it with `./attn agent close op-claude -m "outpost drive" --source-session op-claude`. Press ⌘⇧L (`bridge native_key '{"key":"l","modifiers":["command","shift"]}'`) and set the Closed list for setup with `bridge sessions_set_filter '{"scope":"Closed"}'`: `bridge dom_text '{"selector":".ledger-row[data-row-key=\"op-claude\"]"}'` includes the outpost's name, and the row offers no Keep conversation or Show worktree. Click `.ledger-row[data-row-key="op-claude"] .ledger-row-trailing .ledger-verb` ("Reopen") natively: `op-claude` comes back under the same id, its tile's `.desktop-pane-host` reads the outpost's name, and after the next turn `./attn session show op-claude`'s `usage` line counts the earlier tokens and the new ones. Close it again and run `./attn session reopen op-claude`: it prints `op-claude reopened in <dir>` and returns on the same outpost. Close it once more, cut the outpost's network, and once its row's pill reads `lost` run `./attn session reopen op-claude`: it exits 1 with `session reopen: <name> is unreachable; this needs it now`, and `./attn session show op-claude` still shows `state      closed`. The baseline cannot do this either.
- **Proof.** In `$P`, keep each `wsctl outposts` and `./attn` command's output with its exit code; `bridge dom_text` of the outpost row after each state change; `./attn agent peek op-agent` after each turn; `bridge dom_text` of the `op-claude` ledger row and `./attn session show op-claude` after each close, resume and refusal; the output of `scripts/verify-outposts.py`; and `bridge capture_screenshot '{"path":"<P>/outposts-settings.png"}'` with the Outposts section showing the row, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `outposts-settings-add.txt`.

## Contexts

- **Durability across restarts**: the outpost list, its pins and an outpost's enrollment are kept by each daemon, and the home redials after any restart. A shell on an outpost keeps running through a lost bridge and a restart of either daemon, and its screen comes back on reconnect. If its process is gone, the home marks it recoverable, and Resume starts a new shell in its place. A home restart resumes an accepted delegation onto an outpost on the same machine and path.
- **Linux**: the outpost daemon runs on Linux as a systemd user service, built with `make build-linux-amd64` or `make build-linux-arm64`. `scripts/verify-outpost-systemd.py` checks that its shells survive service restarts, crashes and updates. The app stays on the Mac.
- **Profiles**: outposts belong to the home and serve every profile. A terminal on an outpost belongs to the profile that started it, and Remove closes the outpost's terminals in every profile.
- **Performance and idle cost**: each connected bridge exchanges a heartbeat every 0.5 seconds. Only state changes are published as facts, never heartbeats, which `scripts/verify-outposts.py` checks through `attn bus status`. Nothing else measures a bridge's idle cost.
- **Harnesses**: Claude Code and Codex run on an outpost with that machine's own executable and credentials. Pi and other plugin harnesses, the chief and the crew run only at the home, and a request to start them on an outpost is refused and names that limit.
- **Keyboard and native input**: ⌘D in an outpost shell opens another shell on that outpost. A key typed into an offline terminal flashes its tile red and is never retried; after a lost bridge the tile counts the keys that may not have been sent.
- **Compatibility across versions**: the home and the outpost must run the same build and the same instance name. A different build is refused until "Update to home build"; outposts installed before build updates existed need one manual install.
- **Flow**: an offline agent keeps its queue row, marked, but Next, automatic advance, ⌘J and the waiting counts skip it, and settle, snooze, unsnooze and countdown cancel are hidden.

## Tests

- Wire: `internal/daemon/outposts_wire_test.go`, `internal/daemon/outpost_forward_transcript_wire_test.go`.
- Stack: `cmd/attn/outposts_stack_test.go`, `cmd/attn/outpost_*_stack_test.go`, `cmd/attn/remote_*_stack_test.go`, `cmd/attn/enrollment_stack_test.go`, `cmd/attn/settings_outpost_stack_test.go`, `cmd/attn/delegate_outpost_stack_test.go`, `cmd/attn/session_reopen_outpost_stack_test.go`, `scripts/verify-outpost-ledger-resume.sh`, `app/e2e/outpost-terminal-links.spec.ts`.
- Scenario: `outposts-settings`, `outpost-update`, `notebook-sync`.
- Kernel: `internal/bridge/*_test.go`, `internal/enrollment/*_test.go`, `internal/outpostbuild/*_test.go`.
- App: `app/src/App.outpostsSettings.test.tsx`, `app/src/App.outpostUpdates.test.tsx`, `app/src/App.remoteAgents.test.tsx`, `app/src/App.remoteShell.test.tsx`, `app/src/App.outpostPaths.test.tsx`, `app/src/App.outpostsNotebook.test.tsx`, `app/src/components/ledger/SessionsTab.outposts.test.tsx`.

## Gotchas

- The baseline has nothing to connect to: an added outpost ends `lost`, or `refused` without an ssh-agent key. `scripts/verify-outposts.py` proves the bridge states only; shells, agents, offline tiles, ledger resume, notebook sync and updates need a Linux box with `scripts/outpost-instance.py`. That outpost runs its machine's own Claude Code and Codex, never the mock agent.
- A named instance pairs only with an outpost of the same instance name, never with production or another name. `scripts/verify-outposts.py` sidesteps this with two daemons in temporary data folders.
- Every outpost command refuses while Outposts is off: `outposts_enabled is off; run attn settings set outposts_enabled true before you add an outpost`.
- The bridge signs in with the home daemon's ssh-agent. A daemon started from a shell with no `SSH_AUTH_SOCK`, or an agent with no key, refuses every outpost with `bridge authentication:` and the reason.
- `attn enrollment` reads the data folder of the attn it runs as. Run it on the outpost to see the outpost's side; on the home it always says `home`.
- While the home is still connecting, an outpost terminal does not look offline, yet typing is not sent.
- An outpost with the shared terminal host on refuses an update.
- Working but wrong: the Experimental switch and the list of outposts share the accessible name "Outposts"; select the switch by `[role="switch"]`. The ledger's "Reopen" and `attn session reopen` say Reopen where the glossary says resume. Files on an outpost do not open on the Mac yet: Open in editor and ⌘-clicked paths in an outpost shell fail and name the outpost.
