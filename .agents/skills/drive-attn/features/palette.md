# Palette

The palette searches and runs commands, agents and tiles from the keyboard,
like the VS Code command palette; it learns what the user picks. ⌘K opens it
on agents: the chief and crew, the turns in queue order, the other agents and
the tiles of the profile, and Enter goes there. ⌘⇧K, or `>` typed in the
search, switches it to commands: everything attn can do, with its shortcut,
and the commands about the agent on screen pinned on top. Commands the user
picks often rise in the list.

## Sub-features

- `agent-palette`: ⌘K; the chief and crew, then turns in queue order, the other agents, snoozed agents and tiles. Enter opens one, or wakes a sleeping crew member.
- `command-palette`: ⌘⇧K, or `>` in the agent palette; every command attn offers, with its shortcut.
- `palette-switch`: ⌘K and ⌘⇧K switch between the two and keep the typed text; the same key again closes the palette.
- `palette-actions`: in the agent palette, ⌘⇧E settles, ⌘⇧I marks priority and ⌘⇧S snoozes the highlighted agent, without closing.
- `palette-order`: commands rank by how well they match, then pinned, then how often and how lately the user picked them in this profile.
- `palette-pins`: commands about the agent on screen come first: settle, reload, rename, priority, delegation chain, seeds, usage, context cap, open in editor, and in queue flow snooze and unsnooze.

## How to get to it

### People

- ⌘K (`ui.actionMenu`) opens the agent palette and ⌘⇧K (`ui.commandPalette`) the command palette (`app/src/shortcuts/registry.ts`).
- The sidebar's `button[aria-label="Commands"]` opens the command palette (`app/src/components/SidebarHeader.tsx`). On the queue bar, the waiting pill opens the agent palette (`app/src/components/QueueBar.tsx`). ⌘⇧A opens the agent palette wherever the queue sidebar is not showing ([sidebar.md](sidebar.md)).
- In the palette (`app/src/components/palette/UnifiedPalette.tsx`, `app/src/components/palette/Palette.tsx`): the input `[role="combobox"][aria-label="Agents"]`, or `[aria-label="Commands"]` in command mode; ↑ and ↓ move, Enter runs, Escape closes; typing `>` switches to commands and deleting it switches back. `.unified-palette-count` reads `<shown> of <all>`. Rows are `.unified-palette-option`, agents `[data-testid="palette-agent-<session>"]` and tiles `[data-testid="palette-tile-<tile>"]`. The footer names the settle and snooze keys (`[data-testid="palette-settle-hint"]`, `[data-testid="palette-snooze-hint"]`).
- The commands come from `app/src/application/useAppCommands.tsx` and `app/src/application/useSharedCodex.tsx`. Each command belongs to its feature, whose file drives it.
- ⌘P opens other finders, not this palette: the markdown opener ([markdown.md](markdown.md)), or the note finder inside the notebook or an editor tile ([notebook.md](notebook.md)).
- Where people see what agents do: each agent row's status (`chief`, `waiting`, `approval`, `working`, `idle`, `snoozed`, or `asleep` for a crew member), its turn age, its desktop's number, the priority flag, and ⌘J beside the head of the queue.

### Agents

None. What agents do shows in the agent palette's rows: their state, turn and priority. `attn callout` can point at the `commands` anchor, which is the Commands button, not ⌘K ([callouts.md](callouts.md)).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-palette.XXXX) && echo "export P=$P" >> $DRIVE_ENV`, and an empty agent folder, `A=$(mktemp -d /tmp/drive-attn-palette-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`.
- Two agents: `wsctl add-session --cwd $A --agent claude --label pal-a --id pal-a`, then the same with `pal-b`. Both owe a turn once they finish booting.
- A tile that is not an agent: `./attn seed plant "Palette tile"` prints a line of the seed's id, slug and title, and `<seed>` is that id. `./attn open <seed>` prints `opened <seed>` and puts its seed tile on the current desktop ([garden.md](garden.md)).

- **Open the agent palette.** Run `bridge native_key '{"key":"k","modifiers":["command"]}'` and `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Agents\"]","focused":true,"timeoutMs":5000}'`. `bridge dom_text '{"selector":".unified-palette-count"}'` reads `3 of 3`, and `[data-testid="palette-agent-pal-a"]`, `[data-testid="palette-agent-pal-b"]` and `[data-testid^="palette-tile-"]` all match.
- **Search and go to an agent.** Run `bridge native_text '{"text":"pal-b"}'`: the count reads `1 of 3`. Run `bridge native_key '{"key":"Enter"}'`: `bridge dom_wait '{"selector":"[role=\"dialog\"][aria-label=\"Agents\"]","absent":true,"timeoutMs":5000}'` matches, and `bridge get_state` shows `activeLeaf.sessionId: "pal-b"`.
- **Go to a tile.** Press ⌘K, type `Palette tile` and press Enter. `bridge get_state` shows `activeLeaf.kind: "tile"`.
- **Commands by typing `>`.** Press ⌘K and run `bridge native_text '{"text":">keyboard shortcuts"}'`. `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'` matches, and `bridge dom_wait '{"selector":".unified-palette-option[aria-selected=\"true\"]","textIncludes":"Keyboard shortcuts","timeoutMs":5000}'` matches. Press Enter: `bridge dom_wait '{"selector":"[role=\"dialog\"][aria-labelledby=\"shortcuts-modal-title\"]","timeoutMs":5000}'` matches. Press Escape.
- **The command palette from its key and the button.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`: the `Commands` combobox has focus. Press Escape: `[role="dialog"][aria-label="Commands"]` is gone. Click `button[aria-label="Commands"]` natively, its point from `bridge dom_native_point`: the same combobox has focus. Press Escape.
- **Switch modes, then close with the same key.** Press ⌘⇧K and type `pal`. Press ⌘K: the `Agents` combobox has focus and the count reads `3 of 3`, since `pal` matches both agents and the tile. Press ⌘K again: `[role="dialog"][aria-label="Agents"]` is gone.
- **What the palette learned.** Select `pal-b` for setup with `bridge select_session '{"sessionId":"pal-b"}'` and press ⌘⇧K. Wait for `.unified-palette-option` to match, since the list first reads "Loading command history…". `bridge dom_text '{"selector":".unified-palette-list"}'` shows `Reload this agent`, pinned for the agent on screen, first. It shows `Keyboard shortcuts`, which the earlier step picked, before `New agent`, which no step picked. Press Escape.
- **The order from a second view.** Relaunch the app with `bridge --fresh-launch --wait-ready ping`, select `pal-b` again and press ⌘⇧K. The list shows the same order: what the palette learned is the daemon's.
- **Act on an agent without leaving the palette.** Press ⌘K and type `pal-a`; wait for the count to read `1 of 3`. Run `bridge native_key '{"key":"i","modifiers":["command","shift"]}'`: `wsctl list` shows `"priority": true` on `pal-a`, and the palette stays open; press ⌘⇧I again to clear it. Run `bridge native_key '{"key":"e","modifiers":["command","shift"]}'`: `./attn agent list` shows `-` for `pal-a`. Run `bridge native_key '{"key":"s","modifiers":["command","shift"]}'`: the combobox is now `[aria-label="Snooze pal-a"]`. Press Enter: `./attn session list --tree` shows `pal-a` with `snoozed until <instant>` half an hour ahead, and the palette is back on agents with `pal-a` highlighted. Press Escape.
- **Proof.** In `$P`, keep `bridge dom_text` of `.unified-palette-count` and `.unified-palette-list` after each search, `bridge get_state` after each jump, the list before and after the relaunch, and `wsctl list`, `./attn agent list` and `./attn session list --tree` after the palette's actions. Capture `bridge capture_screenshot '{"path":"<P>/agent-palette.png"}'` with the agent palette open and `<P>/command-palette.png` with the learned order, with `<P>` written out. Put the way in in each file's name, such as `palette-agents-key-count.json`.

## Contexts

- **Durability across restarts**: what the palette learned is the daemon's, per profile, and survives app and daemon restarts. Each pick's weight halves every 14 days.
- **Linux**: `ui.actionMenu` is Ctrl+Shift+K and `ui.commandPalette` Ctrl+Alt+K (`LINUX_SHORTCUTS`).
- **Profiles**: the palette lists the selected profile's agents, crew and tiles only. Its history is the profile's own, and a profile switch picked from the palette counts in the profile it was picked in.
- **Performance and idle cost**: the palette exists only while open, and each opening asks the daemon once for the profile's history.
- **Harnesses**: the context-cap command shows only for Claude Code and Codex agents. Shared Codex adds commands of its own ([shared-codex.md](shared-codex.md)).
- **Keyboard and native input**: everything in the palette is a key. ↑ and ↓ step over the divider and headings, and Escape closes it and gives focus back to where it was. ⌘K and ⌘⇧K do nothing while Settings, the shortcut sheet, the location picker, What's new, the ledger, the fullscreen notebook, crew management, the fullscreen garden, another dialog waiting for an answer, or a snooze menu is open. The palette's settle, snooze and priority keys need a single-key binding; bound to a chord, the footer says it needs a single-key shortcut.
- **Compatibility across versions**: history is kept by command id. A renamed id starts with no history.
- **Flow**: agent rows follow queue order in both flows. In queue flow the command palette adds snooze and unsnooze for the agent on screen, and settle for any agent that owes a turn; in desktop flow settle shows only for an automation run ([queue-flow.md](queue-flow.md)). The agent palette's ⌘⇧E, ⌘⇧I and ⌘⇧S work in both flows.

## Tests

- Wire: `internal/daemon/command_usage_wire_test.go`.
- Stack: `cmd/attn/command_usage_stack_test.go`.
- Scenario: `palette-order`, `snooze-keyboard`.
- App: `app/src/App.actionMenu.test.tsx`, `app/src/App.commandUsage.test.tsx`, `app/src/App.paletteRename.test.tsx`, `app/src/components/palette/Palette.test.tsx`, `app/src/components/palette/UnifiedPalette.keyboard.test.tsx`, `app/src/components/palette/agentPaletteRows.ordering.test.tsx`.

## Gotchas

- `[aria-label="Commands"]` names the sidebar's button and the palette's input and dialog. Use `button[aria-label="Commands"]` for the button and `[role="combobox"][aria-label="Commands"]` for the input.
- The palette reads its history when it opens and keeps that order until it is opened again. A pick shows its effect on the next open, not in the open list.
- Only picks in command mode are learned. Going to an agent or a tile, and pressing a command's shortcut directly, teach it nothing.
- Every typed word must appear in a command's title, description or keywords. A word such as `queue` or `new` matches several commands, so type the whole title and check the highlighted row before Enter.
- While the history loads, the list reads "Loading command history…" and Enter does nothing. Wait for `.unified-palette-option`.
- A command missing from the list is usually conditional: the pinned ones need an agent on screen, snooze and unsnooze need queue flow, settle needs an agent that owes a turn the user can settle, and others need their feature to be available.
- Snoozing from the agent palette opens choices inside the palette: Escape returns to the agents instead of closing.
- Settling or snoozing from the agent palette leaves it open; the change shows on the row.
- Working but wrong: the attn skill's callouts reference shows a callout on `commands` that says "Everything attn can do, searchable (⌘K)", but ⌘K opens the agent palette; the Commands button and ⌘⇧K open the command palette (`internal/prompts/content/skills/attn/references/callouts.md`).
