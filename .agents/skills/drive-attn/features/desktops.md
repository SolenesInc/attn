# Desktops and tiles

Desktops are arrangements of tiles, like macOS Spaces: switch, split, sort,
and move tiles between desktops. Each profile has its own desktops, and every
window shows the profile's current desktop. A tile is one rectangle on a
desktop: a terminal tile, or a browser, markdown, seed or editor tile. People
arrange desktops from the keyboard, the sidebar and the overview; agents move
their own terminals and shells, and crew members and automations start their
sessions on a launch desktop.

## Sub-features

- `desktop-switch`: go to a desktop with ⌘1–⌘9, the sidebar, the overview or the palette; the current desktop's digit goes back to the previous one.
- `desktop-overview`: every desktop as a card, with keys to switch, send the active tile and create.
- `desktop-new`: create a desktop, which takes the lowest free number; ⌘<n> or a move to a free digit creates that numbered desktop.
- `desktop-rename`: name a desktop from the sidebar.
- `desktop-close`: close a desktop and everything on it, after a confirmation; ⌘W closes an empty one.
- `desktop-sort`: numbered desktops first, then names A–Z, with Undo; drag a desktop in the sidebar to reorder it.
- `empty-desktop-sweep`: an unnamed empty desktop goes away 30 seconds after the user leaves it.
- `tile-split`: open a shell beside or below the active terminal tile.
- `tile-focus`: move the active tile with arrows; the focus style marks it (dim, rail or spotlight).
- `tile-zoom`: zoom the active tile, or focus it alone on the desktop.
- `tile-move`: send the active tile to another desktop and follow or stay; drag it; agents move terminals; move an agent with its delegates.
- `tile-history`: walk back and forward through the tiles this window showed.
- `tile-close`: close the active tile.
- `launch-desktop`: the desktop where a crew member's or an automation's sessions start.

## How to get to it

### People

- Keys (`app/src/shortcuts/registry.ts`): ⌘1–⌘9 (`desktop.select1`–`desktop.select9`), ⌥⌘1–⌥⌘9 move the active tile and follow (`desktop.send1`–`desktop.send9`), ⇧⌥⌘1–⇧⌥⌘9 move it and stay (`desktop.sendStay1`–`desktop.sendStay9`), ⌘G (`desktop.overview`), ⌘D and ⌘⇧D split (`terminal.splitVertical`, `terminal.splitHorizontal`), ⌘⇧Z zoom (`terminal.toggleZoom`), ⌘⇧↩ focus (`terminal.toggleMaximize`), ⌥⌘←→↑↓ (`terminal.focusLeft|Right|Up|Down`), ⌘W (`terminal.close`, `session.close`), ⌘[ and ⌘] (`session.historyBack`, `session.historyForward`), and in desktop flow ⌘↑ and ⌘↓ (`session.prev`, `session.next`).
- Native menu (`app/src-tauri/core/src/lib.rs`): File → "Close Pane" carries ⌘W. It closes the focused markdown, seed, editor or browser tile; otherwise the terminal on screen, which ends its shell or agent; on an empty desktop, the desktop.
- Palette (`app/src/application/useAppCommands.tsx`): `desktop-overview`, `new-desktop`, `close-desktop`, `sort-desktops`, `desktop-<id>` ("Go to <desktop>"), `move-with-delegates-<id>` ("Move with delegates to <desktop>", offered while the agent on screen has a delegate on this desktop), and `tile-focus-dim`, `tile-focus-rail`, `tile-focus-spotlight`.
- Sidebar (`app/src/components/SidebarDesktops.tsx`): the "Desktop overview" row, each desktop's row ("Open <desktop>"), its "Rename <desktop>" and "Close <desktop>" buttons, dragging a desktop's row to reorder it, and dragging an agent's row onto another desktop; dropping it with ⌥ held brings the agent's delegates along. Each tile's row under its desktop has a "Close <title>" button, and a browser tile's row a "Reload <title>" button (`app/src/components/SidebarRows.tsx`). The row buttons take clicks only while the pointer is over the row. Closing a desktop asks first (`app/src/components/DesktopClosePrompt.tsx`).
- Overview (`app/src/components/DesktopOverview.tsx`): arrows move, ↵ switches, ⇧↵ sends the active tile, digits switch, Esc closes, "+ New desktop" creates.
- Tile headers (`app/src/components/SessionTerminalDesktop/DesktopDockTile.tsx`, `DesktopAgentHeader.tsx`): "Close tile", "Focus agent <name>", and dragging a header to another place or desktop (`app/src/application/useLeafDrag.ts`).
- Launch desktop field in the crew launch tab and the automation form (`app/src/components/LaunchDesktopSelect.tsx`).
- Where people see what agents do: terminals an agent opens or moves appear on their desktop in the sidebar; an empty desktop shows the launcher (`app/src/application/EmptyDesktopLauncher.tsx`).
- Their own shell: the `--launch-desktop` verbs below. `attn terminal` refuses without a session.

### Agents

- `attn terminal move [<terminal>] <desktop>` (`cmd/attn/terminal.go`): moves the caller's terminal, or a shell, or an agent below the caller in its delegation chain. `<desktop>` is a digit, a name or an id; a missing numbered desktop is created. `attn terminal open --desktop <ref>` is in [terminals.md](terminals.md).
- `attn crew set <member> --launch-desktop` (`cmd/attn/crew.go`) and `attn automation set <id> --launch-desktop`, `attn automation apply --file <yaml> --launch-desktop` (`cmd/attn/automation.go`).
- `attn delegate --desktop` places a delegate ([delegation.md](delegation.md)), and `attn callout show desktop:<which>` points at a desktop ([callouts.md](callouts.md)).
- Prompts: placement, reach and moves in the attn skill's terminal reference (`internal/prompts/content/skills/attn/references/terminal.md`).

### Triggers

- An unnamed desktop that is empty, not current and no item's launch desktop is removed 30 seconds after it empties (`internal/daemon/empty_desktops.go`).
- A crew wake or an automation run starts its session on its launch desktop (`internal/daemon/launch_desktops.go`); crew and automations own the wake and the run.

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-desktops.XXXX)`, holding `check.yaml`: `api_version: attn.dev/automations/v1alpha1`, `name: Local check`, `trigger: {type: manual}`, `prompt: Check locally.`, `launch: {driver: claude}`, `location: {type: directory, path: <P>}`, one key per line, with `<P>` written out.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-desktops-agent.XXXX)`, holding this fixture as `$A/.attn-mock-agent.json`, with `<A>` written out as the folder's path:

```json
{"version": 1, "turns": [
  {"includes": "move-shell", "actions": [
    {"type": "capture", "name": "shell", "pattern": "move-shell (\\S+)"},
    {"type": "capture", "name": "desk", "pattern": "move-shell \\S+ (\\S+)"},
    {"type": "attn", "args": ["terminal", "move", "{{shell}}", "{{desk}}"]}]},
  {"includes": "move-self", "actions": [
    {"type": "capture", "name": "desk", "pattern": "move-self (\\S+)"},
    {"type": "attn", "args": ["terminal", "move", "{{desk}}"]}]},
  {"includes": "delegate-one", "actions": [
    {"type": "attn", "args": ["delegate", "--agent", "codex", "--model", "mock-agent-1", "--brief", "Desktop drive delegate", "--cwd", "<A>", "--name", "desk-delegate"]}]}
]}
```

- A shell and an agent on the current desktop: `wsctl add-session --cwd $P --agent shell --label desk-shell --id desk-shell --place` and `wsctl add-session --cwd $A --agent claude --label desk-agent --id desk-agent --place`. In `wsctl list`, `<shell>` and `<agent>` are their `runtime_id` values. `<desktop>` is `arrangement.currentDesktopId` in `bridge get_state`, and `<n>` is that desktop's `slot` there (its `shortcut_slot` in `wsctl list`), 1 on a fresh instance; the steps below use slots 2 and 3 for the other desktops.

- **Split with the keyboard.** Select the shell for setup with `bridge select_session '{"sessionId":"desk-shell"}'`. Run `bridge native_key '{"key":"d","modifiers":["command"]}'`. `bridge get_desktop '{"sessionId":"desk-shell"}'` lists one more entry in `panes`, and `wsctl list` shows a new `"agent": "shell"` entry on `<desktop>`. Run `bridge native_key '{"key":"d","modifiers":["command","shift"]}'` with the new shell active: a third shell opens, and `bridge get_session_ui_state '{"sessionId":"desk-shell"}'` lists splits of both kinds in `desktop.splits`, one with `direction: "vertical"` and one with `direction: "horizontal"`. The ids of the two new shells are the `"agent": "shell"` entries in `wsctl list` other than `desk-shell`: call them `<split-1>` and `<split-2>`.
- **Move focus, zoom and focus.** Run `bridge native_key '{"key":"ArrowLeft","modifiers":["command","option"]}'`: `desktop.view.activeLeafId` in `get_session_ui_state` names another tile. Run `bridge native_key '{"key":"z","modifiers":["command","shift"]}'`: `desktop.view.zoomedPaneId` names the active tile; press it again and it is null. Run `bridge native_key '{"key":"Enter","modifiers":["command","shift"]}'`: `desktop.view.maximizedPaneId` names it; press it again and it is null.
- **Close a tile.** Select a split shell, never `desk-shell`, with `bridge select_session '{"sessionId":"<split-2>"}'`, and run `bridge native_key '{"key":"w","modifiers":["command"]}'`. The File menu takes ⌘W and closes that shell with its tile. `bridge get_desktop '{"sessionId":"desk-shell"}'` lists one entry fewer in `panes`, `wsctl list` no longer holds `<split-2>`, and `desk-shell` keeps its text (`bridge read_pane_text '{"sessionId":"desk-shell"}'`).
- **New desktop from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"New desktop"}'` and `bridge native_key '{"key":"Enter"}'`. `bridge get_state` shows one more entry in `arrangement.desktops` with the lowest free `slot`, `arrangement.currentDesktopId` is the new one, and `bridge dom_wait '{"selector":"[data-testid=\"empty-desktop-launcher\"]","timeoutMs":5000}'` matches.
- **Switch with digits and bounce.** Run `bridge native_key '{"key":"<n>","modifiers":["command"]}'`: `arrangement.currentDesktopId` is `<desktop>`. Press it again: the new desktop is current, and `previousDesktopId` is `<desktop>`. Press it once more to return.
- **Empty desktops go.** Re-read `bridge get_state` until the new, unnamed desktop is gone from `arrangement.desktops`, with a 45 second deadline that fails the run: the daemon removes it 30 seconds after you left it (`ATTN_EMPTY_DESKTOP_GRACE` shortens that, see Gotchas). `wsctl list` no longer holds it either.
- **Send and stay.** Select the shell for setup. Run `bridge native_key '{"key":"2","modifiers":["command","option","shift"]}'`. `wsctl list` shows `desk-shell` under the desktop whose `shortcut_slot` is 2, created if it was missing, and `arrangement.currentDesktopId` is still `<desktop>`.
- **Step through desktops.** Run `bridge native_key '{"key":"ArrowDown","modifiers":["command"]}'`: `arrangement.currentDesktopId` is the entry after `<desktop>` in `arrangement.desktops`, and wraps to the first entry after the last. Run it with `ArrowUp`: it is back on `<desktop>`. Open the palette, type `Go to Desktop 2`, press Enter: the slot 2 desktop is current. Press ⌘<n> to return to `<desktop>`.
- **The overview.** Run `bridge native_key '{"key":"g","modifiers":["command"]}'` and `bridge dom_wait '{"selector":"dialog.desktop-overview","timeoutMs":5000}'`. `bridge dom_text '{"selector":"dialog.desktop-overview h2"}'` names the profile and counts its desktops. Run `bridge native_key '{"key":"2"}'`: the overview closes and `currentDesktopId` is the slot 2 desktop. Open it again and press Escape: `bridge dom_wait '{"selector":"dialog.desktop-overview","absent":true,"timeoutMs":5000}'` matches. Open it from the palette, command `Desktop overview`, and close it with Escape the same way.
- **Send and follow.** On the slot 2 desktop, select the shell and run `bridge native_key '{"key":"<n>","modifiers":["command","option"]}'`. The shell is back on `<desktop>` and `currentDesktopId` follows it. The slot 2 desktop is now empty and unnamed, and goes 30 seconds later.
- **Tile history.** Select the agent, then the shell, for setup. Run `bridge native_key '{"key":"[","modifiers":["command"]}'`: `bridge get_state` shows `activeLeaf.sessionId` back on `desk-agent`. Run `bridge native_key '{"key":"]","modifiers":["command"]}'`: it is `desk-shell` again.
- **Sort from the palette.** Open the palette, type `Sort desktops`, press Enter. `bridge dom_wait '{"selector":".toast","textIncludes":"Desktops sorted","timeoutMs":5000}'` matches, and `wsctl list` orders numbered desktops first. Click the toast's Undo natively (box of `.toast-row--action button` from `bridge dom_bounds`, converted as the README says): `wsctl list` returns to the earlier order.
- **Focus style.** Open the palette, type `Tile focus`, and note which of the three commands is described as "Current tile focus style". Pick `Tile focus: spotlight` instead: type `Tile focus: spotlight` and press Enter. Open the palette and type `Tile focus` again: `bridge dom_wait '{"selector":".unified-palette-option","textIncludes":"Current tile focus style","timeoutMs":5000}'` matches the `Tile focus: spotlight` row. Restore the style you noted the same way.
- **An agent moves a shell and itself.** Run `wsctl input --id <agent> --text "move-shell <shell> 3" --enter`. `./attn agent peek desk-agent` shows `<shell> moved to desktop <d3>`, and `wsctl list` holds that desktop, with `shortcut_slot` 3, whose `panes` include `<shell>`. Run `wsctl input --id <agent> --text "move-self 3" --enter`: peek shows `<agent> moved to desktop <d3>`, and `bridge get_state` keeps `arrangement.currentDesktopId` on `<desktop>`.
- **Move with delegates.** Move the agent back with `wsctl input --id <agent> --text "move-self <n>" --enter`. Run `wsctl input --id <agent> --text "delegate-one" --enter` and re-read `./attn agent peek desk-agent` until it shows the delegation's result with the delegate's `"session_id"`; `wsctl list` then shows that delegate's tile on `<desktop>`. Select the agent for setup, open the palette and type `Move with delegates to Desktop 3`. `bridge dom_wait '{"selector":".unified-palette-option[aria-selected=\"true\"]","textIncludes":"Move with delegates to Desktop 3","timeoutMs":5000}'` matches. Press Enter: `wsctl list` shows the agent and its delegate both on `<d3>`.
- **Drag.** Boxes come from `bridge dom_bounds`, converted as the README says. Drag `[data-testid="sidebar-session-desk-agent"]` onto the `<desktop>` row, `[data-desktop-id="<desktop>"] .sidebar-row-select`, with `bridge native_mouse '{"action":"drag","x":<x1>,"y":<y1>,"toX":<x2>,"toY":<y2>,"modifiers":["option"]}'`: `wsctl list` shows the agent and its delegate back on `<desktop>`. Drag the `<desktop>` row onto the lower edge of the `<d3>` row, `[data-desktop-id="<d3>"] .sidebar-row-select`, the same way without a modifier: the order of `desktops` in `wsctl list` changes. Drag the agent's header, `[data-pane-session-id="desk-agent"] .desktop-pane-header`, onto the delegate tile's right edge: `tree_json` of `<desktop>` in `wsctl list` differs from before.
- **Sidebar rename and close.** The sidebar's rename and close buttons take clicks only while the pointer is over the desktop's row. Take the box of `[data-testid="rename-desktop-<d3>"]` from `bridge dom_bounds`, convert it as the README says, move there with `bridge native_mouse '{"action":"move","x":<x>,"y":<y>}'`, then click it with `bridge native_mouse`. The name field opens selected: run `bridge native_text '{"text":"Drive"}'` and `bridge native_key '{"key":"Enter"}'`. `wsctl list` shows `<d3>` with `"name": "Drive"`. Move to and click `[data-testid="close-desktop-<d3>"]` the same way: `bridge dom_wait '{"selector":"#desktop-close-title","textIncludes":"Close Drive?","timeoutMs":5000}'` matches. Click `.desktop-close-prompt .mp-button.primary` natively: `<d3>` and its tiles are gone from `wsctl list`, the shell with them.
- **Close from the palette.** Press ⌘3: a new empty desktop opens. Open the palette, type `Close desktop`, press Enter: the empty desktop closes at once, with no prompt, and leaves `arrangement.desktops`.
- **Launch desktop.** Run `./attn automation apply --file $P/check.yaml --launch-desktop own`. It prints the definition as JSON, its launch desktop labelled `Local check (no ⌘ number)`, and `wsctl list` holds a desktop named `Local check`. It stays after 30 seconds while empty, because it is a launch desktop. Crew members get theirs with `./attn crew set <member> --launch-desktop own` ([crew.md](crew.md)); a run landing there is driven in [automations.md](automations.md).
- **The shell has no session.** Run `./attn terminal move <agent> 4` from the instance shell. It exits 1 with `terminal: caller rule: attn terminal requires a live calling agent; run it inside an attn agent session`.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, `wsctl list` before and after each move, `bridge get_state` after each switch, `bridge get_session_ui_state '{"sessionId":"desk-shell"}'` after split, zoom and focus, and `./attn agent peek desk-agent` after each agent move. Capture `bridge capture_screenshot '{"path":"<P>/desktops-split.png"}'` with the splits and `<P>/desktops-overview.png` with the overview open, `<P>` written out. Put the way in in each file's name, such as `desktops-send-stay-state.json`.

## Contexts

- **Durability across restarts**: desktops, their names, slots, order, tiles and active tile are the daemon's records and come back after any restart, with every terminal in its tile. Tile history belongs to a window and starts empty in a new one.
- **Linux**: `attn terminal move` and launch desktops work the same where the daemon and CLI run on Linux. Bindings there (`LINUX_SHORTCUTS`): Ctrl+Shift+1–9 switch, Ctrl+Alt+1–9 send and follow, Ctrl+Shift+Alt+1–9 send and stay, Ctrl+Shift+G overview, Ctrl+Shift+D and Ctrl+Alt+D split, Ctrl+Alt+Z zoom, Ctrl+Alt+Enter focus, Ctrl+Shift+W close, Ctrl+Shift+arrows move focus, Ctrl+Shift+[ and ] history, Ctrl+Shift+PageUp and PageDown step desktops.
- **Outposts**: a terminal that runs on an outpost sits in a tile on a home desktop like any other, and moves the same way. While its host is offline the tile stays where it is and typing into it is not sent.
- **Profiles**: each profile has its own desktops and current desktop. Desktop refs in `attn terminal move`, `--launch-desktop` and callouts resolve only in the caller's profile, and terminals in another profile are not found.
- **Performance and idle cost**: the app keeps only the current desktop, the previous one and a drag's source desktop mounted, and a hidden terminal releases its drawing surface; `app/e2e/split-paint.spec.ts` checks repainting while splitting. When a change touches tile layout or switching, measure it with `docs/perf-testing.md`.
- **Keyboard and native input**: every desktop and tile action has a key, and the overview runs on arrows, ↵, ⇧↵, digits and Esc. ⌘W reaches attn through File → "Close Pane", so a rebind of `terminal.close` or `session.close` does not move it. After a switch, typing goes straight to the shown desktop's active tile.
- **Compatibility across versions**: a later version reads today's desktops through the store's migrations. An arrangement from before desktops opens the migration picker before any shell (scenario `desktop-migration`); [profiles.md](profiles.md) owns the profile migration.
- **Flow**: in desktop flow the sidebar lists each desktop with its tiles, and ⌘↑ and ⌘↓ step through desktops. In queue flow the sidebar shows desktop chips and ⌘↑ and ⌘↓ walk the queue instead ([queue-flow.md](queue-flow.md)).

## Tests

- Wire: `internal/daemon/desktop_*_wire_test.go`, `internal/daemon/launch_desktop_wire_test.go`, `internal/daemon/terminal_move_wire_test.go`, `internal/daemon/desktop_tiles_protocol_test.go`.
- Stack: `cmd/attn/launch_desktop_stack_test.go`, `cmd/attn/terminal_move_stack_test.go`, `scripts/verify-terminal-move.sh`, `app/e2e/desktop-sessions.spec.ts`, `app/e2e/tile-header.spec.ts`, `app/e2e/agent-header.spec.ts`, `app/e2e/pane-focus-ring.spec.ts`, `app/e2e/split-paint.spec.ts`.
- Scenario: `desktop-switching`, `desktop-order`, `desktop-splits`, `close-desktop`, `close-pane-nonblocking`, `desktop-close-one-session-keeps-selection`, `desktop-migration`, `launch-desktops`, `move-with-delegates`.
- Kernel: `internal/layouttree/*_test.go`.
- App: `app/src/App.desktop*.test.tsx`, `app/src/App.closeDesktop.test.tsx`, `app/src/App.activeLeaf.test.tsx`, `app/src/App.moveLeaf.test.tsx`, `app/src/App.moveDelegates.test.tsx`, `app/src/App.paneHeader.test.tsx`, `app/src/App.launchDesktops.test.tsx`, `app/src/App.launchDesktopKeyboard.test.tsx`, `app/src/components/SessionTerminalDesktop.test.tsx`, `app/src/components/SessionTerminalDesktop/*.test.*`, `app/src/components/DesktopOverview.keyboard.test.tsx`, `app/src/application/useLeafDrag.lifecycle.test.tsx`.

## Gotchas

- Pressing the current desktop's digit goes back to the previous desktop, once the window has left one. A run that presses ⌘1 twice expecting to stay is wrong, not attn.
- A desktop with no name and nothing on it disappears 30 seconds after you leave it, numbered or not. Name it, put a tile on it, or make it a launch desktop before a step that comes back to it. The daemon reads the wait from `ATTN_EMPTY_DESKTOP_GRACE` (such as `2s`) when it starts, which shortens the sweep step.
- ⌘W on a terminal tile ends what runs in it: a split shell closes, and an agent's session closes too. Press it on a shell in a recipe, never on the agent you still need.
- `attn delegate` without a role needs `--model` and `--cwd`; the mock agent answers to `--model mock-agent-1`, and `--agent` defaults to the caller's harness. Without `--model` the delegation step stops at `--model is required`.
- `attn terminal` needs a session: from the instance shell it exits 1 with the caller rule. Drive moves through the mock agent.
- What's new on a fresh app takes the keys; dismiss it first.
- ⌘D and ⌘⇧D split only from a terminal tile. With a markdown, browser, seed or editor tile active they do nothing.
- `move-with-delegates-*` appears only while the agent on screen has a delegate on the current desktop. On the baseline there is none until the delegation step runs.
- The overview's ⇧↵ and "Send focused pane here" do nothing for the current desktop's card.
- `bridge capture_screenshot` leaves out terminal content. Prove what a moved terminal still holds with `bridge read_pane_text` or `wsctl screen --id <terminal>`.
- Working but wrong: the shortcut labels say "Split pane down" for ⌘D and "Split pane sideways" for ⌘⇧D, while ⌘D's split has `direction: "vertical"`, which lays the tiles out side by side. Check which way the new tile went in `desktop.splits`, not the label. ⌘↑ and ⌘↓ are labeled as desktop moves with a queue aside, and do something else in queue flow.
