# Sidebar

The sidebar is the window's list of what the user works with, and its shape
follows the profile's flow. In desktop flow it is the desktop sidebar: each
desktop with its agents and tiles, which collapses to a thin column of desktop
chips. In queue flow it is the queue sidebar: Home, the crew, the turns the
user owes, the rest of the agents, automation runs and desktop chips, which
collapses into the queue bar across the top. Its header holds the profile,
Commands, the flow switch and the buttons for the garden, the notebook, the
ledger, automations, notifications and asks.

## Sub-features

- `desktop-sidebar`: desktop flow; each desktop with its agents and tiles, in layout order.
- `collapsed-rail`: the desktop sidebar collapsed to a column with Home, Commands, the flow switch, the header buttons and desktop chips.
- `queue-sidebar`: queue flow; Home, crew, the waiting card with the oldest turns, automation runs and desktop chips.
- `queue-bar`: the queue sidebar collapsed into a strip: the waiting pill and its peek, asks, runs and desktop chips.
- `agent-list`: every agent of the profile under the waiting card, with a filter.
- `collapse`: hide the sidebar down to the rail or the bar, and bring it back.
- `header`: the profile pill, New, Commands, the flow switch, collapse, and the buttons for the garden, notebook, ledger, automations, notifications and asks.
- `session-rows`: one row per agent with its state, harness mark and actions menu; a clipped name shows in full on hover.

## How to get to it

### People

- Keys (`app/src/shortcuts/registry.ts`): ⌘B (`session.toggleSidebar`) collapses and expands; ⌘⇧A (`sidebar.agentList`) opens and closes the agent list. Where the queue sidebar is not showing, ⌘⇧A opens the agent palette instead (`app/src/application/useAppController.tsx`).
- Palette (`app/src/application/useAppCommands.tsx`): `toggle-sidebar` ("Hide the sidebar" or "Show the sidebar").
- Header (`app/src/components/SidebarHeader.tsx`): `[aria-label="Collapse sidebar"]`, `[aria-label="New agent"]`, `button[aria-label="Commands"]`, the flow button, and the profile pill. The header buttons are named by what they do, such as "Show the garden" and "Show Notifications" (`app/src/application/useAppSidebarActions.tsx`); what each opens belongs to its feature.
- Desktop sidebar and rail (`app/src/components/Sidebar.tsx`, `app/src/components/SidebarChrome.tsx`, `app/src/components/SidebarDesktops.tsx`, `app/src/components/SidebarRows.tsx`): rows `[data-testid="sidebar-session-<session>"]` with `[aria-label="Open <name>"]` and the actions button `[data-testid="session-actions-<session>"]`, which queue rows carry too; desktop groups `[data-testid="sidebar-desktop-<desktop>"]`; the rail's `button[title="Expand sidebar"]`.
- Queue sidebar and bar (`app/src/components/QueueSidebar.tsx`, `app/src/components/QueueBar.tsx`, `app/src/components/QueueRows.tsx`): `[data-testid="queue-agents-toggle"]` and the filter `[aria-label="Filter agents"]`; ↑ and ↓ walk the rows, a letter typed with the list open filters it, and Escape clears the filter, then closes the list, then returns to the agent on screen. On the bar, `[aria-label="Show sidebar"]` expands it, and the waiting pill `[data-testid="queue-bar-pill"]` opens the agent palette; hovering it shows a peek of the agents.
- Every row's actions menu: "Mark priority", "Rename session", "Make chief of staff", "Reload session" and "Close session" (`app/src/components/SessionActionsPopover.tsx`). [queue-flow.md](queue-flow.md), [ledger.md](ledger.md), [crew.md](crew.md) and [agent-launch.md](agent-launch.md) own what each does.
- Driven in other files: ⌘↑ and ⌘↓ step through what the sidebar lists, desktops in desktop flow ([desktops.md](desktops.md)) and the queue in queue flow ([queue-flow.md](queue-flow.md)); desktops are switched, renamed, closed and dragged between here ([desktops.md](desktops.md)); the flow button changes the shape ([queue-flow.md](queue-flow.md)); the dock of shortcut chips sits at the foot of the desktop sidebar ([shortcuts.md](shortcuts.md)); the harness logos on each row are [harnesses.md](harnesses.md)'s, and the instance marker in the header is [instances.md](instances.md)'s.
- Where people see what agents do: each row's state dot, harness mark, priority flag and turn age, the waiting count, and the waiting dot on desktop chips.

### Agents

None. Agents change what the sidebar shows without driving it: `attn session rename` sets the name a row shows ([ledger.md](ledger.md)), and `attn callout` can point at its `sidebar`, `home`, `commands`, `garden`, `desktops`, `queue` and `crew` anchors ([callouts.md](callouts.md)).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`). The baseline starts in desktop flow.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-sidebar.XXXX) && echo "export P=$P" >> $DRIVE_ENV`, and an empty agent folder, `A=$(mktemp -d /tmp/drive-attn-sidebar-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`.
- Two agents: `wsctl add-session --cwd $A --agent claude --label side-a --id side-a`, then the same with `side-b`. With no session the sidebar starts collapsed; the first agent expands it.

- **Open an agent from its row.** Run `bridge dom_wait '{"selector":"[data-testid=\"sidebar-session-side-a\"]","timeoutMs":5000}'`, then click `[aria-label="Open side-a"]` natively, converting its `bridge dom_bounds` box as the README says. `bridge get_state` shows `activeLeaf.sessionId: "side-a"`.
- **Collapse with the key.** Run `bridge native_key '{"key":"b","modifiers":["command"]}'`. `bridge dom_wait '{"selector":".sidebar.collapsed","timeoutMs":5000}'` matches, and the rail still has `[aria-label="Home"]` and `button[aria-label="Commands"]`. Press ⌘B again: `[data-testid="sidebar-home"]` is back.
- **Collapse from the palette and the header.** Open the palette with ⌘⇧K, type `Hide the sidebar` and press Enter: `.sidebar.collapsed` matches. Click `button[title="Expand sidebar"]` natively: it expands. Click `[aria-label="Collapse sidebar"]` natively: it collapses again, and ⌘B brings it back.
- **The header buttons.** Click `button[aria-label="Commands"]` natively: `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'` matches; press Escape. Click `[aria-label="Show Notifications"]` natively: `bridge notifications_get_state` shows `present: true`. Click `[aria-label="Hide Notifications"]` natively: `present` is false. `[aria-label="New agent"]`, the garden, notebook, ledger, automations and asks buttons and the profile pill open features of their own; [agent-launch.md](agent-launch.md), [garden.md](garden.md), [notebook.md](notebook.md), [ledger.md](ledger.md), [automations.md](automations.md), [asks.md](asks.md) and [profiles.md](profiles.md) drive them.
- **The queue sidebar.** Run `./attn settings set queue_mode_enabled true`. `bridge dom_wait '{"selector":"[data-testid=\"sidebar-queue\"]","timeoutMs":5000}'` matches, and `bridge queue_get_state` shows `present: true` and `turns` naming `side-a` and `side-b`, which owe a turn since they booted. Settle `side-b` for setup: select it with `bridge select_session '{"sessionId":"side-b"}'` and press ⌘⇧E ([queue-flow.md](queue-flow.md)); `turns` now names only `side-a`.
- **The agent list.** Run `bridge native_key '{"key":"a","modifiers":["command","shift"]}'`. `bridge queue_get_state` shows `agentList.expanded: true` and `settled` naming `side-b`, and `bridge dom_wait '{"selector":"[data-testid=\"queue-agent-filter\"]","focused":true,"timeoutMs":5000}'` matches. Run `bridge native_text '{"text":"zzz"}'`: `settled` is empty and the list reads `Nobody else.`. Press Escape: the filter clears. Press Escape again: `agentList.expanded` is false.
- **The queue bar.** Press ⌘B. `bridge queue_get_state` shows `present: false` and `bar.present: true`, with `bar.waiting: 1` and `bar.crumbs` naming `side-a`. Click `[data-testid="queue-bar-pill"]` natively: `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Agents\"]","focused":true,"timeoutMs":5000}'` matches; press Escape. Press ⌘⇧A: the agent palette opens again, since the queue sidebar is not showing; press Escape. Click `[aria-label="Show sidebar"]` natively: `present` is true again.
- **A row's actions.** Click `[data-testid="session-actions-side-a"]` natively. `bridge dom_text '{"selector":"[role=\"menu\"][aria-label=\"Actions for side-a\"]"}'` lists `Mark priority`, `Rename session`, `Make chief of staff`, `Reload session` and `Close session`. Press Escape: the menu closes.
- **Proof.** In `$P`, keep `bridge get_state` after each step and `bridge queue_get_state` after each shape change, and capture each shape: `bridge capture_screenshot '{"path":"<P>/desktop-sidebar.png"}'`, then `rail.png`, `queue-sidebar.png` and `queue-bar.png`, with `<P>` written out. Put the way in in each file's name, such as `sidebar-collapse-key-state.json`.

## Contexts

- **Durability across restarts**: the shape follows the daemon's flow setting and comes back with it. Collapsed or expanded is not kept: a relaunch starts expanded when there are sessions and collapsed when there are none, with the agent list closed. Harness logos are a daemon setting.
- **Linux**: `session.toggleSidebar` is Ctrl+Alt+B and `sidebar.agentList` is Ctrl+Alt+A (`LINUX_SHORTCUTS`).
- **Outposts**: an agent whose outpost is offline keeps its row, marked offline, and the waiting count leaves it out.
- **Profiles**: the sidebar shows the selected profile's desktops, agents and crew only; the profile pill switches profiles ([profiles.md](profiles.md)). Flow is one setting for every profile today, so switching profiles keeps the shape.
- **Performance and idle cost**: turn ages re-read the clock every 30 seconds, and a row's settling bar animates only while a countdown runs. Nothing measures the sidebar's idle cost; when a change touches its rendering, check it for repaints with `docs/perf-testing.md`.
- **Harnesses**: each row carries its harness's mark. With logos off, the queue sidebar still names the harness in the row's hover text.
- **Keyboard and native input**: ⌘B and ⌘⇧A reach the sidebar from a terminal or the window. In an input, a textarea or an editor outside a terminal, ⌘B stays with that field, while ⌘⇧A still fires. In the queue sidebar, ↑ and ↓ walk rows and Escape steps back.
- **Flow**: queue flow shows the queue sidebar and the queue bar, desktop flow the desktop sidebar and the rail. ⌘⇧A opens the agent list only where the queue sidebar shows.

## Tests

- Stack: `app/e2e/sidebar-rail.spec.ts`, `app/e2e/sidebar-tree.spec.ts`, `app/e2e/queue-bar.spec.ts`, `app/e2e/queue-sidebar-fit.spec.ts`, `app/e2e/queue-sidebar-rows.spec.ts`.
- Scenario: `agent-queue`.
- App: `app/src/App.sidebar.test.tsx`, `app/src/App.sidebarHeader.test.tsx`, `app/src/App.sidebarLabels.test.tsx`, `app/src/App.desktopSidebar.test.tsx`, `app/src/App.agentNavigation.test.tsx`, `app/src/App.queueRows.test.tsx`, `app/src/components/Sidebar.test.tsx`, `app/src/components/QueueSidebar.sections.test.tsx`, `app/src/components/QueueBar.sections.test.tsx`.

## Gotchas

- On a fresh instance with no session, the sidebar is collapsed: the rail, or the queue bar in queue flow. It expands by itself when the first session appears, and collapses again when the last one goes. Launch an agent before judging the expanded shapes.
- The queue bar has no Home row, no Commands button and no header buttons. Go home with ⌘⇧H and open drawers from the palette or their keys while it shows.
- `[aria-label="Commands"]` also names the palette's input and dialog. Click `button[aria-label="Commands"]`.
- The agent list's filter takes focus when the list opens, so ⌘↑, ⌘↓ and ⌘B do nothing until Escape or a click moves focus out of it.
- ⌘⇧A opens the agent palette in desktop flow, on the queue bar and while a tile fills the window. That is by design, not the agent list failing.
- The waiting card shows only the turns that fit; the rest move into the agent list as "Also waiting". A short window hides turns from the card, not from the queue.
- A row's buttons (settle, snooze, unsnooze, actions) take clicks only while the pointer is on the row. After a native click on one, keyboard focus stays on that row, and ⌘⇧E, ⌘⇧S and ⌘⇧I act on that row, not on the agent on screen.
- `bridge queue_get_state` reads `settled` and `snoozed` rows only while the agent list is open.
- Working but wrong: the bell's label is "Show Notifications" while the palette says "Show notifications".
