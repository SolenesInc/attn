# Palette

The palette searches and runs commands, agents and tiles from the keyboard,
like the VS Code command palette, and it learns what the user picks. ⌘K opens
the palette on agents: the chief and crew, the turns in queue order, the other
agents, and the tiles of the profile. Press Enter to go to the highlighted one. ⌘⇧K,
or `>` typed in the search, switches the palette to commands: everything attn
can do, with its shortcut, and the commands about the agent on screen pinned on
top. Commands the user picks often rise in the list.

## Sub-features

- `agent-palette`: ⌘K lists the chief and crew, then turns in queue order, the other agents, snoozed agents and tiles. Enter opens one, or starts a session for a crew member between sessions. An off-screen tile on the current desktop comes beside the active tile ([desktops.md](desktops.md)).
- `command-palette`: ⌘⇧K, or `>` in the agent palette, lists every command attn offers, with its shortcut.
- `palette-switch`: ⌘K and ⌘⇧K switch between the two and keep the typed text. The same key again closes the palette.
- `palette-actions`: in the agent palette, ⌘⇧E settles, ⌘⇧I marks priority and ⌘⇧S snoozes the highlighted agent, without closing.
- `palette-order`: commands rank first by how well they match, then by whether they are pinned, then by how often and how lately the user picked them in this profile.
- `palette-pins`: commands about the agent on screen come first: settle, reload, rename, priority, delegation chain, pull requests, seeds, usage, context cap, open in editor, and in queue flow snooze and unsnooze.

## How to get to it

### People

- ⌘K (`ui.actionMenu`) opens the agent palette and ⌘⇧K (`ui.commandPalette`) the command palette (`native/crates/attn-app/src/keymap.rs`).
- The title bar's left group holds the Commands button, `titlebar.commands`, which opens the command palette. On the queue bar, the waiting pill `queue-bar.pill` opens the agent palette (`titlebar.commands` in `native/crates/attn-app/src/title_bar.rs`, `queue-bar.pill` in `native/crates/attn-app/src/sidebar/chips.rs`). ⌘⇧A opens the agent palette wherever the queue sidebar is not showing ([sidebar.md](sidebar.md)).
- In the palette (`native/crates/attn-app/src/palette/`): the dialog `palette.dialog` is named `Agents`, or `Commands` in command mode. ↑ and ↓ move, Enter runs, and Escape closes. Typing `>` switches to commands, and deleting it switches back. `palette.count` reads `<shown> of <all>`. Rows are `palette.row.agent:<session>`, `palette.row.tile:<desktop>:<tile>` and `palette.row.command:<id>`. The footer hints `palette.hint.<n>` name the settle and snooze keys.
- The commands come from `native/crates/attn-app/src/palette/commands.rs` and `native/crates/attn-app/src/harness/codex/mod.rs`. Each command belongs to a feature, and that feature's file covers how to drive it.
- ⌘P opens other finders, not this palette: the markdown opener ([markdown.md](markdown.md)), or the note finder inside the notebook or an editor tile ([notebook.md](notebook.md)).
- Where people see what agents do: each agent row's status (`chief`, `waiting`, `approval`, `working`, `idle`, `snoozed`, or `between sessions` for a crew member), its turn age, its desktop's number, the priority flag, and ⌘J beside the head of the queue.

### Agents

None. The agent palette's rows show what agents do: their state, turn and priority. `attn callout` can point at the `commands` anchor, which is the Commands button, not ⌘K ([callouts.md](callouts.md)).

## Driving it

A crew member between sessions lists with `· crew` and the desktop it starts on, in `palette.row.member:<member>.crew`.

Preconditions:

- Use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says. Then run `bridge wait '{"target":{"key":"sidebar"}}'`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`.
- The chords below are macOS's. On Linux, press the ones **Linux** under Contexts names.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-palette.XXXX) && echo "export P=$P" >> $DRIVE_ENV` and an empty agent folder with `A=$(mktemp -d /tmp/drive-attn-palette-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`.
- Start two agents with `wsctl add-session --cwd $A --agent claude --label pal-a --id pal-a`, then the same with `pal-b`. Both are owed a turn once they finish booting.
- Make a tile that is not an agent. Run `./attn seed plant "Palette tile"`: it prints a line of the seed's id, slug and title, and `<seed>` is that id. Run `./attn open <seed>`: it prints `opened <seed>` and puts the seed's tile on the current desktop ([garden.md](garden.md)).
- A row's key is `palette.row.agent:<session>`, `palette.row.tile:<desktop>:<tile>` or `palette.row.command:<id>`. `bridge tree` marks the highlighted row `selected`, and `palette.dialog` is named `Agents`, `Commands` or `Snooze <name>`.

- **Open the agent palette.** Run `bridge key '{"chord":"mod+k"}'`: `bridge find '{"target":{"key":"palette.dialog"}}'` is named `Agents`, `palette.count` reads `3 of 3`, and the tree holds `palette.row.agent:pal-a`, `palette.row.agent:pal-b` and one `palette.row.tile:` row.
- **Search and go to an agent.** Run `bridge type '{"text":"pal-b"}'`: the count reads `1 of 3`. Press `enter`: `bridge wait '{"target":{"key":"palette"},"gone":true}'` answers and `bridge state '{"of":"window","expect":{"on_screen":"pal-b"}}'` answers.
- **Go to a tile.** Press `mod+k`, type `<seed>` and press `enter`; the native seed tile is named by the seed's id, not its title. `wsctl list` shows the tile as its desktop's `active_pane_id`.
- **Commands by typing `>`.** Press `mod+k` and type `>keyboard shortcuts`: `palette.dialog` is named `Commands` and the tree marks `palette.row.command:keyboard-shortcuts` `selected`. Press `enter`: `bridge wait '{"target":{"key":"shortcuts-dialog"}}'` answers. Press `escape`.
- **The command palette from its key and the button.** Press `mod+shift+k`: `palette.dialog` is named `Commands`. Press `escape`. Run `bridge press '{"target":{"key":"titlebar.commands"}}'`: the same palette opens. Press `escape`.
- **Switch faces, then close with the same key.** Press `mod+shift+k` and type `pal`. Press `mod+k`: `palette.dialog` is named `Agents` and the count reads `2 of 3`, since `pal` matches the two agents and not the seed tile, which is named by its id. Press `mod+k` again: `palette` is gone.
- **Move, and leave command mode.** Press `mod+k`: the tree marks the first agent `selected`, and `down` then `up` move the mark and back. Type `>`: the palette is named `Commands`. Press `backspace`: it is named `Agents` again. Press `escape`.
- **The queue bar's pill and the agent list key.** Run `./attn settings set queue_mode_enabled true` and press `mod+b`: `bridge find '{"target":{"key":"queue-bar.pill"}}'` is named `<n> waiting · …`, counting the agents owed a turn, then their names in queue order. Run `bridge press '{"target":{"key":"queue-bar.pill"}}'`: the agent palette opens. Press `escape`, then `mod+shift+a`: the agent palette opens again. Press `escape` and `mod+b`.
- **What the palette learned.** Open `pal-b` from the palette, then press `mod+shift+k` and wait for `palette.row.command:new-agent`, since the list first reads "Loading command history…". The tree lists the commands pinned for the agent on screen first, `palette.row.command:reload-active-session` among them, then `palette.row.command:keyboard-shortcuts`, which an earlier step picked, before `palette.row.command:new-agent`. Press `escape`. Quit the app by its captured PID, launch it again and repeat: the order is the same, since the history is the daemon's.
- **Rename, priority, reload and unsnooze for the agent on screen.** With `pal-b` on screen, press `mod+k` and type `>rename`, then `enter`: the tree shows `session.rename.field` holding `pal-b`. Type 49 `x` and press `enter`: `bridge find '{"target":{"key":"session.rename.refused"}}'` names the 48-character limit. Press `mod+a`, type `pal-b2` and press `enter`: `./attn agent list` shows `pal-b2`. Run `>priority` the same way: `wsctl list` shows `"priority": true` on it, and again clears it. `>reload` restarts it in place, as [agent-launch.md](agent-launch.md) checks.
- **Act on an agent without leaving the palette.** With `queue_mode_enabled` true, press `mod+k` and type `pal-a`. `bridge tree` lists footer hints `palette.hint.<n>` that include ` settle` and ` snooze` and end with `esc closes`. Press `mod+shift+i`: `wsctl list` shows `"priority": true` on `pal-a`, and the palette stays open; press it again to clear it. Press `mod+shift+e`: `./attn agent list` shows `-` for `pal-a`. Get `pal-a` owed again, then press `mod+shift+s`: the palette is named `Snooze pal-a`. Press `enter`: `./attn session list --tree` shows `pal-a` snoozed half an hour ahead, and the palette is back on agents with `pal-a` marked `selected` and its `.status` reading `snoozed`. Open `pal-a`, press `mod+shift+k`, type `unsnooze` and press `enter`: the snooze clears. Press `escape` and run `./attn settings set queue_mode_enabled false`.
- **Setting toggles.** Press `mod+shift+k` and type `auto-settle`, then `enter`: `./attn settings get auto_settle_enabled` flips; run it again to put it back. An experimental feature's toggle shows only for a search that names it.
- **Proof.** In `$P`, keep `bridge tree` after each search, `bridge state '{"of":"window"}'` after each jump, the command order before and after the relaunch, and `wsctl list`, `./attn agent list` and `./attn session list --tree` after the palette's actions. Capture `bridge shot '{"path":"<P>/agent-palette.png"}'` with the agent palette open and `<P>/command-palette.png` with the learned order, with `<P>` written out. Name each file for its way in, such as `palette-agents-key-tree.json`. The `native-palette-order` scenario runs the search, the learned order, `>`, priority and rename.

## Contexts

- **Durability across restarts**: what the palette learned is the daemon's, per profile, and survives app and daemon restarts. Each pick's weight halves every 14 days.
- **Linux**: `ui.actionMenu` is Ctrl+Shift+K, `ui.commandPalette` Ctrl+Alt+K, `sidebar.agentList` Ctrl+Alt+A, `session.toggleSidebar` Ctrl+Alt+B, `session.settle` Ctrl+Alt+E, `session.priority` Ctrl+Alt+I, `session.snooze` Ctrl+Alt+S and `session.goToDashboard` Ctrl+Alt+H (`native/crates/attn-app/src/keymap.rs`).
- **Profiles**: the palette lists the selected profile's agents, crew and tiles only. Its history is the profile's own, and a profile switch picked from the palette counts in the profile it was picked in.
- **Performance and idle cost**: the palette exists only while open, and each opening asks the daemon once for the profile's history.
- **Harnesses**: the context-cap command shows only for Claude Code and Codex agents. Shared Codex adds commands of its own ([shared-codex.md](shared-codex.md)).
- **Keyboard and native input**: everything in the palette is a key. ↑ and ↓ step over the divider and headings, and Escape closes it and gives focus back to where it was. ⌘K and ⌘⇧K do nothing while Settings, the shortcut sheet, the location picker, What's new, the ledger, the fullscreen notebook, crew management, the fullscreen garden, another dialog waiting for an answer, or a snooze menu is open. The palette's settle, snooze and priority keys need a single-key binding. If one is bound to a chord, the footer says it needs a single-key shortcut.
- **Compatibility across versions**: history is kept by command id. A renamed id starts with no history.
- **Flow**: agent rows follow queue order in both flows, and the agent palette's ⌘⇧I works in both. ⌘⇧E settles an agent in the queue that is owed a turn, and settles an automation run that is owed one in either flow. ⌘⇧S snoozes only an agent in the queue, so in desktop flow it does nothing. The footer hints show only when a key applies. In queue flow the command palette adds snooze and unsnooze for the agent on screen, and settle for any agent that is owed a turn. In desktop flow the command palette shows settle only for an automation run ([queue-flow.md](queue-flow.md)).
- **Accessibility**: the field keeps the keyboard while ↑ and ↓ move the highlight, and the field names the highlighted row as its active descendant, so VoiceOver reads the row with its position, such as "Set up Chief (2 of 19)". `bridge tree` shows it as the field's `activeDescendant`.

## Tests

- Wire: `native/crates/attn-app/tests/wire/palette.rs`, `internal/daemon/command_usage_wire_test.go`.
- Scenario: `native-palette-order`.

## Gotchas

- The palette reads its history when it opens and keeps that order until the next time it opens. A pick shows its effect on the next open, not in the open list.
- The palette learns only picks in command mode. Going to an agent or a tile, and pressing a command's shortcut directly, teach it nothing.
- Every typed word must appear in a command's title, description or keywords. A word such as `queue` or `new` matches several commands, so type the whole title and check the highlighted row before Enter.
- While the history loads, the list reads "Loading command history…". Wait for a command row such as `palette.row.command:new-agent`.
- A command missing from the list is usually conditional: the pinned ones need an agent on screen, snooze and unsnooze need queue flow, settle needs an agent that is owed a turn the user can settle, and others need their feature to be available.
- Snoozing from the agent palette opens choices inside the palette: Escape returns to the agents instead of closing.
- Settling or snoozing from the agent palette leaves the palette open. The change shows on the row.
- Working but wrong: `attn skill --reference callouts` has an example `callout show` on `commands` that says "Everything attn can do, searchable (⌘K)", but ⌘K opens the agent palette. The Commands button and ⌘⇧K open the command palette (`internal/prompts/content/skills/attn/references/callouts.md`).
