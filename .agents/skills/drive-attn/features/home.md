# Home screen

The home screen is the dashboard of what asks for the user's attention across
agents, with notifications. Its Sessions card lists the profile's sessions by
what they need: in queue flow the agents that are owed a turn come first, and in
desktop flow sessions are grouped by state. Snoozed agents wait in their own
group until they are due. Home also shows the open asks, the chief and the
pull requests waiting on the user. The notifications popover holds what attn
itself needs the user to know, such as a duty that failed for good. The app
opens on home.

## Sub-features

- `dashboard`: the Sessions card, grouped by state. In queue flow, "Your turn" comes first, then "Settled".
- `all-settled`: queue flow's "All settled" banner, with what still runs and "Take me to the next agent that needs you".
- `snoozed-group`: snoozed agents, soonest first, each with its due time and an unsnooze button.
- `home-cards`: the Asks group and the Pull Requests card. [asks.md](asks.md) and [pull-requests.md](pull-requests.md) own their content.
- `notifications`: the popover of attn's own notifications, newest first. Unread rows are marked, a row expands to its detail and actions, and "Mark all read" clears the marks.
- `critical-strip`: an unread critical notification shows as a strip at the top of the sidebar.
- `whats-new`: the intro that opens on a new release, and the home banner that replays it.
- `toasts`: short-lived errors and undo offers in a corner of the window. The user clicks a toast's button to run its action.

## How to get to it

### People

- ⌘⇧H (`session.goToDashboard`, `app/src/shortcuts/registry.ts`).
- Palette (`app/src/application/useAppCommands.tsx`): `home` ("Home"), `notifications` ("Show notifications" or "Hide notifications"), `whats-new` ("What's new").
- Sidebar: the Home row `[data-testid="sidebar-home"]` above everything (`app/src/components/Sidebar.tsx`, `app/src/components/QueueSidebar.tsx`), and `[aria-label="Home"]` on the rail (`app/src/components/SidebarChrome.tsx`). The bell, `[aria-label="Show Notifications"]`, with an `[aria-label="Unread notifications"]` dot when something is unread (`app/src/application/useAppSidebarActions.tsx`). The critical strip, `button.critical-strip` (`app/src/components/CriticalNotificationStrip.tsx`).
- On home (`app/src/components/Dashboard.tsx`): click a session row `[data-testid="session-<session>"]` to open the agent. Click the snoozed group's header `[data-testid="session-group-snoozed-header"]` to expand the group, and click `[data-testid="session-unsnooze-<session>"]` to end a snooze. The follow checkbox is in `[data-testid="follow-next-turn"]`. The intro banner has "Replay the intro" and `[aria-label="Dismiss the intro banner"]`. Home also hosts "+ New" ([agent-launch.md](agent-launch.md)), the settings gear `[data-testid="settings-button"]` ([settings.md](settings.md)) and the cards above.
- The notifications popover, `[role="dialog"][aria-label="Notifications"]` (`app/src/components/NotificationsPanel.tsx`): a click on a row expands the row and marks it read. Each row has action buttons, such as a retry, and the popover has "Mark all read".
- What's new, `.whats-new-modal` (`app/src/components/WhatsNewModal.tsx`): ← and → step through it, Enter advances, Escape or "Close what's new" closes it for this release.
- Toasts, `.toast` with role `alert` (`app/src/components/Toast.tsx`): a row's button runs its action, such as Undo.
- Where people see what agents do: each row's state, activity line, turn age and due time, and what still runs under "All settled".

### Agents

None. What agents do shows here. Home groups agents by their state ([agent-state.md](agent-state.md)), `attn ask` adds a row to Asks ([asks.md](asks.md)), and `attn turn open` puts the agent under "Your turn" ([queue-flow.md](queue-flow.md)). `attn callout` can point at the `home` and `settings-button` anchors ([callouts.md](callouts.md)).

### Triggers

- attn raises a notification when one of its duties needs the user. The cases are a background task that failed for good, with a retry (`internal/daemon/notifications.go`), a durable event-log consumer holding the log open (`internal/daemon/bus_pin_alarm.go`), a plugin that attn stopped restarting (`internal/daemon/plugin_runtime.go`), and a notebook conflict copy (`internal/daemon/notebook_sync.go`). The plugin case is critical.
- The app opens What's new by itself when it has not shown the current release's intro (`app/src/hooks/useWhatsNew.ts`).

## Driving it

Preconditions:

- Launch the baseline instance with its app fresh, and check that `bridge --launch --wait-ready ping` answers. Run the first two steps before the baseline's `bridge dismiss_whats_new`, which dismisses the intro and its banner. Afterwards, the command changes nothing.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-home.XXXX) && echo "export P=$P" >> $DRIVE_ENV`. Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-home-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`. Save this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "question", "actions": [{"type": "reply", "text": "Which option do you want?"}]}
]}
```

`<terminal>` is the agent's terminal id from `wsctl list`.

- **What's new on a fresh app.** `bridge dom_wait '{"selector":".whats-new-modal","timeoutMs":5000}'` matches, and `bridge dom_text '{"selector":".whats-new-count"}'` reads `1 of 7`. Run `bridge native_key '{"key":"ArrowRight"}'`: it reads `2 of 7`. Run `bridge native_key '{"key":"Enter"}'`: it reads `3 of 7`. Run `bridge native_key '{"key":"ArrowLeft"}'`: it reads `2 of 7`. Run `bridge native_key '{"key":"Escape"}'`: `.whats-new-modal` is gone, and `[data-testid="intro-banner"]` shows on home.
- **Replay and dismiss the banner.** Click `.intro-banner-replay` natively, its point from `bridge dom_native_point`: the intro opens again. Press Escape. Click `[aria-label="Dismiss the intro banner"]` natively: `bridge dom_wait '{"selector":"[data-testid=\"intro-banner\"]","absent":true,"timeoutMs":5000}'` matches.
- **What's new from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"whats new"}'` and `bridge native_key '{"key":"Enter"}'`. `.whats-new-modal` matches. Click `[aria-label="Close what's new"]` natively: `.whats-new-modal` is gone.
- **Home with the key.** Start the agent: `wsctl add-session --cwd $A --agent claude --label home-a --id home-a`. Select it for setup with `bridge select_session '{"sessionId":"home-a"}'`, then run `bridge native_key '{"key":"h","modifiers":["command","shift"]}'`. `bridge get_state` shows `view: "dashboard"`, and `bridge home_get_state` shows `onScreen: true`, `allSettled: false` and `home-a` in `stateGroupSessionIds`.
- **Open an agent from home.** Click `[data-testid="session-home-a"]` natively. `bridge get_state` shows `view: "session"` and `activeLeaf.sessionId: "home-a"`.
- **Home from the palette and the sidebar.** Open the palette as above, type `Home` and press Enter: `view` is `"dashboard"`. Click `[aria-label="Open home-a"]` in the sidebar to leave, then click `[data-testid="sidebar-home"]` natively: `view` is `"dashboard"` again. Open `home-a` the same way, press ⌘B, and click `[aria-label="Home"]` on the rail natively: `view` is `"dashboard"`. Press ⌘B to expand.
- **Snoozed agents.** Run `./attn session snooze home-a --until 1h`. It prints `home-a snoozed until <instant>`. `bridge home_get_state` shows `snoozed.present: true` and `expanded: false`. Click `[data-testid="session-group-snoozed-header"]` natively: `expanded` is true, and `rows` holds `home-a` with its due time in `snoozedUntil` and `canUnsnooze: true`. Click `[data-testid="session-unsnooze-home-a"]` natively: `./attn session list --tree` no longer shows `snoozed until` for `home-a`, and `snoozed.present` is false.
- **Your turn.** Run `./attn settings set queue_mode_enabled true`. `bridge dom_wait '{"selector":"[data-testid=\"session-group-turns\"] [data-testid=\"session-home-a\"]","timeoutMs":5000}'` matches: attn opened the agent's turn again when the snooze ended.
- **All settled, then the next turn.** Select `home-a` and press ⌘⇧E ([queue-flow.md](queue-flow.md)). The app goes home: `bridge home_get_state` shows `allSettled: true`, `detail: "Nothing is running."` and `followNextTurn: true`. Run `wsctl input --id <terminal> --text "question" --enter`. When the agent stops, the app takes the user to it: `bridge get_state` shows `activeLeaf.sessionId: "home-a"`.
- **Stay home instead.** Settle `home-a` again, then click `[data-testid="follow-next-turn"] input` natively: `followNextTurn` is false. Send `question` again. The user stays home, and the row returns under "Your turn".
- **The notifications popover.** Click `[aria-label="Show Notifications"]` natively. `bridge notifications_get_state` shows `present: true` and, on the baseline, `empty: "No notifications."`. Open the palette, type `Hide notifications` and press Enter: `present` is false. Type `Show notifications` and press Enter: `present` is true. Click `[aria-label="Hide Notifications"]` natively. This way out fails, because the open popover's backdrop covers the bell (see Gotchas). Close the popover with `Hide notifications` from the palette.
- **Read a notification.** Raise a critical notification with a plugin that attn gives up restarting.
  1. Run `C=$(mktemp -d /tmp/drive-attn-home-crash.XXXX) && echo "export C=$C" >> $DRIVE_ENV`.
  2. Write `$C/attn-plugin.toml` with `name = "home-crash"`, `version = "0.0.1"`, `attn_api_version = 7`, and a `[plugin]` table that holds `kind = "executable"` and `path = "crash.sh"`.
  3. Write `$C/crash.sh` as `#!/bin/sh` followed by `exit 1`, and make it executable.
  4. Write `$C/package.json` as `{"name":"home-crash","version":"0.0.1","private":true}`.
  5. Run `./attn plugin install --path $C`.

  About two minutes later `./attn plugin list` shows `home-crash` with `runtime_phase` `parked` and `restart_attempt` 10, and the bell shows `[aria-label="Unread notifications"]`. Open the popover: `rows` lists the notification with `unread: true`. Click its `.notification-row-head` natively: the row expands and `unread` turns false. For the critical notification, the app also shows `button.critical-strip`, reading `Plugin stopped: home-crash`, at the top of the sidebar. Click the strip natively: the popover opens with that row.
- **Mark all read.** Run `./attn plugin uninstall home-crash`, then install the plugin again. When the second row arrives unread, click `.notifications-panel-markall` natively: every row's `unread` is false and the dot is gone. Run `./attn plugin uninstall home-crash`, then close the popover with the palette's "Hide notifications".
- **A toast.** Open the palette, type `Sort desktops` and press Enter. `bridge dom_wait '{"selector":".toast","textIncludes":"Desktops sorted","timeoutMs":5000}'` matches. Click `.toast-row button` natively: `bridge dom_text '{"selector":".toast-row button small"}'` reads `✓ Done`. Undo puts the desktops back in the order they had before the sort ([desktops.md](desktops.md)). With one or two desktops, that order may already be the sorted one, so the order proves nothing there.
- **Settings from home.** Go home with ⌘⇧H and click `[data-testid="settings-button"]` natively: `bridge dom_wait '{"selector":"[data-testid=\"settings-nav-general\"]","timeoutMs":5000}'` matches. Press Escape. The "+ New" button starts a new agent, whose recipe is in [agent-launch.md](agent-launch.md).
- **Proof.** In `$P`, keep `bridge home_get_state` after each step on home, `bridge notifications_get_state` with the popover open, `bridge get_state` after each way home, and the `./attn` commands' output and exit codes. Capture `bridge capture_screenshot '{"path":"<P>/home-desktop-flow.png"}'` with the agent in a state group, `<P>/home-all-settled.png` with the banner, and `<P>/whats-new.png` with the intro open. Write `<P>` out as the folder's path. Name each file for its way in, such as `home-key-state.json`.

## Contexts

- **Durability across restarts**: home reads the daemon's sessions, turns and snoozes, so it shows the same after any restart. Notifications and what the user read are the daemon's records. The app remembers in its own storage that the user saw What's new and its banner. The follow checkbox is not kept. attn ticks the checkbox only when the user settles or snoozes the last turn, and clears it when the user goes anywhere.
- **Linux**: `session.goToDashboard` is Ctrl+Alt+H (`LINUX_SHORTCUTS`).
- **Outposts**: an agent whose outpost is offline is marked offline on home, never sits under "Your turn", and has no unsnooze button.
- **Profiles**: home lists the selected profile's sessions and asks. A notification about something a profile owns shows only in that profile: a failure for a session or a Garden review item shows in the profile that owns it, and a Notebook conflict in the profiles that use its folder. Notifications about the daemon itself, such as a stopped plugin, show in every profile. The unread count follows the selected profile.
- **Performance and idle cost**: home re-reads the clock for turn ages every 30 seconds, and the GitHub rate-limit banner ticks every second only while the banner shows. Nothing measures home's idle cost. When a change touches home's rendering, check it for repaints with `docs/internal/perf-testing.md`.
- **Keyboard and native input**: ⌘⇧H reaches home from anywhere, a text field included. The intro takes ←, →, Enter and Escape. Home's session rows take clicks only (see Gotchas).
- **Flow**: in desktop flow, the Sessions card groups by state. Queue flow puts "Your turn" first and the rest under "Settled", and shows "All settled" when no agent is owed a turn. The snoozed group shows in both.

## Tests

- Wire: `internal/daemon/notifications_wire_test.go`; a session's failed title reaching only its profile in `internal/daemon/session_title_wire_test.go`.
- Scenario: `focusless-native` (parked macOS native input), `home-dock`.
- Kernel: `internal/store/notifications_test.go`, `internal/daemon/notifications_render_test.go`.
- App: `app/src/App.dashboard.test.tsx`, `app/src/App.homeDock.test.tsx`, `app/src/App.notifications.test.tsx`, `app/src/App.criticalNotifications.test.tsx`, `app/src/App.whatsNew.test.tsx`, `app/src/components/Toast.lifecycle.test.tsx`.

## Gotchas

- On the reused `drive-2` instance at `8ed664831`, cleaning and reinstalling did not restore the fresh intro or its replay banner. The first two steps failed their freshness precondition. The palette still opened the intro, but opening it from the palette does not prove automatic opening or the banner controls. Keep those steps and report them as unreached when the release was already seen.
- Live on `drive-3` at `b292a5aa5`: clicking `[aria-label="Hide Notifications"]` leaves `bridge notifications_get_state` at `present: true`. The open popover's full-window backdrop (`.notifications-panel-backdrop`) covers the bell, so the native hide step fails by design. The palette's `Hide notifications` closes the popover. Close the popover before checking another control, since the popover also intercepts clicks on home.
- A fresh app opens What's new over home, and What's new holds the palette and most keys. The baseline's `bridge dismiss_whats_new` also dismisses the replay banner, so drive the intro first or not at all on that app.
- The baseline raises no notification, and the popover reads "No notifications.". The read steps raise one with a plugin that exits at once. The other sources need what the baseline lacks. A notebook conflict copy needs an outpost. A durable event-log consumer that stops reading ([diagnostics.md](diagnostics.md)) and a background task that fails for good need the failure to really happen. A row's action buttons, such as a task's retry, come only with a failed task, so they stay undriven on the baseline.
- No toast carries a session today, so a toast's button only runs its action.
- The queue bar has no bell. Open the popover from the palette. Only a critical notification shows on the bar, as the strip.
- `bridge home_get_state` returns `shortcutFooter`, which reads a footer that home no longer has. The value is always false.
- ⌘⇧H, the Home row and the palette's Home leave the follow checkbox clear. Only settling or snoozing the last turn ticks it, so a test of "the next turn takes me there" starts from a settle.
- A toast fades six seconds after it appears unless the pointer or focus is on it. Click it right away, or read it with `bridge dom_wait` first.
- The palette title "What's new" has an apostrophe, which breaks the single-quoted JSON as written. Typing `whats new` matches the command through its keywords.
- Working but wrong: home's session rows are not buttons, so Tab never reaches them. From the keyboard, the user reaches agents with ⌘J, ⌘K or the sidebar. The state groups say "Pending approval" for needs approval, an older name. The bell's label is "Show Notifications" while the palette says "Show notifications".
