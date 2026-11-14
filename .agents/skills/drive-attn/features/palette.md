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
- `palette-pins`: commands about the agent on screen come first: settle, reload, rename, priority, delegation chain, seeds, usage, context cap, open in editor, and in queue flow snooze and unsnooze.

## How to get to it

### People

- ⌘K (`ui.actionMenu`) opens the agent palette and ⌘⇧K (`ui.commandPalette`) the command palette (`app/src/shortcuts/registry.ts`).
- The title bar's left group holds `button[aria-label="Commands"]`, which opens the command palette (`app/src/components/TitleBarControls.tsx`). On the queue bar, the waiting pill opens the agent palette (`app/src/components/QueueBar.tsx`). ⌘⇧A opens the agent palette wherever the queue sidebar is not showing ([sidebar.md](sidebar.md)).
- In the palette (`app/src/components/palette/UnifiedPalette.tsx`, `app/src/components/palette/Palette.tsx`): the input is `[role="combobox"][aria-label="Agents"]`, or `[aria-label="Commands"]` in command mode. ↑ and ↓ move, Enter runs, and Escape closes. Typing `>` switches to commands, and deleting it switches back. `.unified-palette-count` reads `<shown> of <all>`. Rows are `.unified-palette-option`, agents `[data-testid="palette-agent-<session>"]` and tiles `[data-testid="palette-tile-<tile>"]`. The footer names the settle and snooze keys (`[data-testid="palette-settle-hint"]`, `[data-testid="palette-snooze-hint"]`).
- The commands come from `app/src/application/useAppCommands.tsx` and `app/src/application/useSharedCodex.tsx`. Each command belongs to a feature, and that feature's file covers how to drive it.
- ⌘P opens other finders, not this palette: the markdown opener ([markdown.md](markdown.md)), or the note finder inside the notebook or an editor tile ([notebook.md](notebook.md)).
- Where people see what agents do: each agent row's status (`chief`, `waiting`, `approval`, `working`, `idle`, `snoozed`, or `between sessions` for a crew member), its turn age, its desktop's number, the priority flag, and ⌘J beside the head of the queue.

### Agents

None. The agent palette's rows show what agents do: their state, turn and priority. `attn callout` can point at the `commands` anchor, which is the Commands button, not ⌘K ([callouts.md](callouts.md)).

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers, then run `bridge dismiss_whats_new` to dismiss What's new.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-palette.XXXX) && echo "export P=$P" >> $DRIVE_ENV` and an empty agent folder with `A=$(mktemp -d /tmp/drive-attn-palette-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`.
- Start two agents with `wsctl add-session --cwd $A --agent claude --label pal-a --id pal-a`, then the same with `pal-b`. Both are owed a turn once they finish booting.
- Make a tile that is not an agent. Run `./attn seed plant "Palette tile"`: it prints a line of the seed's id, slug and title, and `<seed>` is that id. Run `./attn open <seed>`: it prints `opened <seed>` and puts the seed's tile on the current desktop ([garden.md](garden.md)).

- **Open the agent palette.** Run `bridge native_key '{"key":"k","modifiers":["command"]}'` and `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Agents\"]","focused":true,"timeoutMs":5000}'`. `bridge dom_text '{"selector":".unified-palette-count"}'` reads `3 of 3`, and `[data-testid="palette-agent-pal-a"]`, `[data-testid="palette-agent-pal-b"]` and `[data-testid^="palette-tile-"]` all match.
- **Search and go to an agent.** Run `bridge native_text '{"text":"pal-b"}'`: the count reads `1 of 3`. Run `bridge native_key '{"key":"Enter"}'`: `bridge dom_wait '{"selector":"[role=\"dialog\"][aria-label=\"Agents\"]","absent":true,"timeoutMs":5000}'` matches, and `bridge get_state` shows `activeLeaf.sessionId: "pal-b"`.
- **Go to a tile.** Press ⌘K, type `Palette tile` and press Enter. `bridge get_state` shows `activeLeaf.kind: "tile"`.
- **Commands by typing `>`.** Press ⌘K and run `bridge native_text '{"text":">keyboard shortcuts"}'`. `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'` matches, and `bridge dom_wait '{"selector":".unified-palette-option[aria-selected=\"true\"]","textIncludes":"Keyboard shortcuts","timeoutMs":5000}'` matches. Press Enter: `bridge dom_wait '{"selector":"[role=\"dialog\"][aria-labelledby=\"shortcuts-modal-title\"]","timeoutMs":5000}'` matches. Press Escape.
- **The command palette from its key and the button.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`: the `Commands` combobox has focus. Press Escape: `[role="dialog"][aria-label="Commands"]` is gone. Click `button[aria-label="Commands"]` natively, its point from `bridge dom_native_point`: the same combobox has focus. Press Escape.
- **Switch modes, then close with the same key.** Press ⌘⇧K and type `pal`. Press ⌘K: the `Agents` combobox has focus and the count reads `3 of 3`, since `pal` matches both agents and the tile. Press ⌘K again: `[role="dialog"][aria-label="Agents"]` is gone.
- **Move, and leave command mode.** Press ⌘K: `.unified-palette-option[aria-selected="true"]` is `pal-a`, and ↓ then ↑ move the highlight to `pal-b` and back. Type `>`: the `Commands` combobox has focus. Press Backspace: the `Agents` combobox has focus again and the count reads `3 of 3`. Press Escape.
- **The queue bar's pill and ⌘⇧A.** Run `./attn settings set queue_mode_enabled true` and press ⌘B: `[data-testid="queue-bar-pill"]` reads `1 waiting`. Click it natively: the `Agents` combobox has focus. Press Escape, then ⌘⇧A (`bridge native_key '{"key":"a","modifiers":["command","shift"]}'`): the agent palette opens again. Press Escape and ⌘B. With `queue_mode_enabled` false, ⌘⇧A opens the agent palette too.
- **What the palette learned.** Select `pal-b` for setup with `bridge select_session '{"sessionId":"pal-b"}'` and press ⌘⇧K. Wait for `.unified-palette-option` to match, since the list first reads "Loading command history…". `bridge dom_text '{"selector":".unified-palette-list"}'` shows `Reload this agent`, pinned for the agent on screen, first. It shows `Keyboard shortcuts`, which the earlier step picked, before `New agent`, which no step picked. Press Escape.
- **The order from a second view.** Relaunch the app with `bridge --fresh-launch --wait-ready ping`, select `pal-b` again and press ⌘⇧K. The list shows the same order: what the palette learned is the daemon's.
- **Act on an agent without leaving the palette.** Run `./attn settings set queue_mode_enabled true`: settle and snooze reach only agents in the queue, so in desktop flow ⌘⇧E and ⌘⇧S do nothing for these agents and the footer does not list them. Press ⌘K and type `pal-a`. Wait for the count to read `1 of 3`. `[data-testid="palette-settle-hint"]` reads `⌘⇧E settle` and `[data-testid="palette-snooze-hint"]` `⌘⇧S snooze`. Run `bridge native_key '{"key":"i","modifiers":["command","shift"]}'`: `wsctl list` shows `"priority": true` on `pal-a`, and the palette stays open. Press ⌘⇧I again to clear it. Run `bridge native_key '{"key":"e","modifiers":["command","shift"]}'`: `./attn agent list` shows `-` for `pal-a`. Run `bridge native_key '{"key":"s","modifiers":["command","shift"]}'`: the combobox is now `[aria-label="Snooze pal-a"]`. Press Enter: `./attn session list --tree` shows `pal-a` with `snoozed until <instant>` half an hour ahead, and the palette is back on agents with `pal-a` highlighted, its row reading `snoozed`. Press Escape and run `./attn settings set queue_mode_enabled false`.
- **Proof.** In `$P`, keep `bridge dom_text` of `.unified-palette-count` and `.unified-palette-list` after each search, `bridge get_state` after each jump, the list before and after the relaunch, and `wsctl list`, `./attn agent list` and `./attn session list --tree` after the palette's actions. Capture `bridge capture_screenshot '{"path":"<P>/agent-palette.png"}'` with the agent palette open and `<P>/command-palette.png` with the learned order, with `<P>` written out. Name each file for its way in, such as `palette-agents-key-count.json`.

### In the native app

The native app (`native/`) has the agent palette with its queue keys and snooze choices, the command palette with its order, pins and the daemon's history, `>` to switch, and the title bar's Commands button and the queue bar's pill. The commands a native feature lacks show only in the Tauri app: profile switching, drawers and the shared Codex commands among them. Crew members between sessions, the chief setup row and offline agents wait for the native crew and outpost units. `bridge` reaches the native app's own automation actions (`docs/internal/native-app.md`, Automation).

Preconditions:

- Install the instance with `make install INSTANCE=<name> APP=native`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`.
- Launch as [../SKILL.md](../SKILL.md) says for a native instance, then `bridge wait '{"target":{"key":"sidebar"}}'`.
- Make `P` and `A` and start `pal-a` and `pal-b` as above, and open the seed's tile with `./attn open <seed>`. A row's key is `palette.row.agent:<session>`, `palette.row.tile:<desktop>:<tile>` or `palette.row.command:<id>`. `bridge tree` marks the highlighted row `selected`, and `palette.dialog` is named `Agents`, `Commands` or `Snooze <name>`.
- The chords below are macOS's. On Linux, press the ones **Linux** under Contexts names.

Steps:

- **Open the agent palette.** Run `bridge key '{"chord":"mod+k"}'`: `bridge find '{"target":{"key":"palette.dialog"}}'` is named `Agents`, `palette.count` reads `3 of 3`, and the tree holds `palette.row.agent:pal-a`, `palette.row.agent:pal-b` and one `palette.row.tile:` row.
- **Search and go to an agent.** Run `bridge type '{"text":"pal-b"}'`: the count reads `1 of 3`. Press `enter`: `bridge wait '{"target":{"key":"palette"},"gone":true}'` answers and `bridge state '{"of":"window","includes":"pal-b"}'` shows `on_screen: "pal-b"`.
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
- **Proof.** In `$P`, keep `bridge tree` after each search, `bridge state '{"of":"window"}'` after each jump, the command order before and after the relaunch, and `wsctl list`, `./attn agent list` and `./attn session list --tree` after the palette's actions. Capture `bridge shot '{"path":"<P>/native-agent-palette.png"}'` with the agent palette open and `<P>/native-command-palette.png` with the learned order. The `native-palette-order` scenario runs the search, the learned order, `>`, priority and rename.

## Contexts

- **Durability across restarts**: what the palette learned is the daemon's, per profile, and survives app and daemon restarts. Each pick's weight halves every 14 days.
- **Linux**: `ui.actionMenu` is Ctrl+Shift+K, `ui.commandPalette` Ctrl+Alt+K, `sidebar.agentList` Ctrl+Alt+A, `session.toggleSidebar` Ctrl+Alt+B, `session.settle` Ctrl+Alt+E, `session.priority` Ctrl+Alt+I, `session.snooze` Ctrl+Alt+S and `session.goToDashboard` Ctrl+Alt+H (`LINUX_SHORTCUTS`).
- **Profiles**: the palette lists the selected profile's agents, crew and tiles only. Its history is the profile's own, and a profile switch picked from the palette counts in the profile it was picked in.
- **Performance and idle cost**: the palette exists only while open, and each opening asks the daemon once for the profile's history.
- **Harnesses**: the context-cap command shows only for Claude Code and Codex agents. Shared Codex adds commands of its own ([shared-codex.md](shared-codex.md)).
- **Keyboard and native input**: everything in the palette is a key. ↑ and ↓ step over the divider and headings, and Escape closes it and gives focus back to where it was. ⌘K and ⌘⇧K do nothing while Settings, the shortcut sheet, the location picker, What's new, the ledger, the fullscreen notebook, crew management, the fullscreen garden, another dialog waiting for an answer, or a snooze menu is open. The palette's settle, snooze and priority keys need a single-key binding. If one is bound to a chord, the footer says it needs a single-key shortcut.
- **Compatibility across versions**: history is kept by command id. A renamed id starts with no history.
- **Flow**: agent rows follow queue order in both flows, and the agent palette's ⌘⇧I works in both. ⌘⇧E settles an agent in the queue that is owed a turn, and settles an automation run that is owed one in either flow. ⌘⇧S snoozes only an agent in the queue, so in desktop flow it does nothing. The footer hints show only when a key applies. In queue flow the command palette adds snooze and unsnooze for the agent on screen, and settle for any agent that is owed a turn. In desktop flow the command palette shows settle only for an automation run ([queue-flow.md](queue-flow.md)).

## Tests

- Wire: `native/crates/attn-app/tests/wire/palette.rs` (the native app), `internal/daemon/command_usage_wire_test.go`.
- Stack: `cmd/attnd/command_usage_stack_test.go`.
- Scenario: `native-palette-order` (the native app), `palette-order`, `snooze-keyboard`.
- App: `app/src/App.actionMenu.test.tsx`, `app/src/App.commandUsage.test.tsx`, `app/src/App.paletteRename.test.tsx`, `app/src/components/palette/Palette.test.tsx`, `app/src/components/palette/UnifiedPalette.keyboard.test.tsx`, `app/src/components/palette/agentPaletteRows.ordering.test.tsx`.

## Gotchas

- `[aria-label="Commands"]` names the sidebar's button and the palette's input and dialog. Use `button[aria-label="Commands"]` for the button and `[role="combobox"][aria-label="Commands"]` for the input.
- The palette reads its history when it opens and keeps that order until the next time it opens. A pick shows its effect on the next open, not in the open list.
- The palette learns only picks in command mode. Going to an agent or a tile, and pressing a command's shortcut directly, teach it nothing.
- Every typed word must appear in a command's title, description or keywords. A word such as `queue` or `new` matches several commands, so type the whole title and check the highlighted row before Enter.
- While the history loads, the list reads "Loading command history…" and Enter does nothing. Wait for `.unified-palette-option`.
- A command missing from the list is usually conditional: the pinned ones need an agent on screen, snooze and unsnooze need queue flow, settle needs an agent that is owed a turn the user can settle, and others need their feature to be available.
- Snoozing from the agent palette opens choices inside the palette: Escape returns to the agents instead of closing.
- Settling or snoozing from the agent palette leaves the palette open. The change shows on the row.
- Working but wrong: `attn skill --reference callouts` has an example `callout show` on `commands` that says "Everything attn can do, searchable (⌘K)", but ⌘K opens the agent palette. The Commands button and ⌘⇧K open the command palette (`internal/prompts/content/skills/attn/references/callouts.md`).
