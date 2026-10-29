# Home screen

The home screen is the dashboard of what asks for the user's attention across
agents, with notifications. Its Sessions card lists the profile's sessions by
what they need: in queue flow the turns the user owes come first, and in
desktop flow sessions are grouped by state. Snoozed agents wait in their own
group until they are due. Beside them sit the open asks, the chief and the
pull requests waiting on the user. The notifications popover holds what attn
itself needs the user to know, such as a duty that failed for good. The app
opens on home.

## Sub-features

- `dashboard`: the Sessions card, grouped by state; in queue flow "Your turn" first, then "Settled".
- `all-settled`: queue flow's "All settled" banner, with what still runs and "Take me to the next agent that needs you".
- `snoozed-group`: snoozed agents, soonest first, each with its due time and an unsnooze button.
- `home-cards`: the Asks group, the Chief of Staff card and the Pull Requests card; [asks.md](asks.md), [crew.md](crew.md) and [pull-requests.md](pull-requests.md) own their content.
- `notifications`: the popover of attn's own notifications, newest first: unread marked, a row expands to its detail and actions, and "Mark all read".
- `critical-strip`: an unread critical notification shows as a strip at the top of the sidebar.
- `whats-new`: the intro that opens on a new release, and the home banner that replays it.
- `toasts`: short-lived errors and undo offers in a corner of the window; a click goes to the session or runs the action.

## How to get to it

### People

- ⌘⇧H (`session.goToDashboard`, `app/src/shortcuts/registry.ts`).
- Palette (`app/src/application/useAppCommands.tsx`): `home` ("Home"), `notifications` ("Show notifications" or "Hide notifications"), `whats-new` ("What's new").
- Sidebar: the Home row `[data-testid="sidebar-home"]` above everything (`app/src/components/Sidebar.tsx`, `app/src/components/QueueSidebar.tsx`), and `[aria-label="Home"]` on the rail (`app/src/components/SidebarChrome.tsx`). The bell, `[aria-label="Show Notifications"]`, with an `[aria-label="Unread notifications"]` dot when something is unread (`app/src/application/useAppSidebarActions.tsx`). The critical strip, `button.critical-strip` (`app/src/components/CriticalNotificationStrip.tsx`).
- On home (`app/src/components/Dashboard.tsx`): a session row `[data-testid="session-<session>"]` opens the agent; the snoozed group's header `[data-testid="session-group-snoozed-header"]` expands it, and `[data-testid="session-wake-<session>"]` ends a snooze; the follow checkbox in `[data-testid="follow-next-turn"]`; "Replay the intro" and `[aria-label="Dismiss the intro banner"]` on the intro banner. Home also hosts "+ New" ([agent-launch.md](agent-launch.md)), the settings gear `[data-testid="settings-button"]` ([settings.md](settings.md)) and the cards above.
- The notifications popover, `[role="dialog"][aria-label="Notifications"]` (`app/src/components/NotificationsPanel.tsx`): rows that expand on click and mark themselves read, each row's action buttons such as a retry, and "Mark all read".
- What's new, `.whats-new-modal` (`app/src/components/WhatsNewModal.tsx`): ← and → step through it, Enter advances, Escape or "Close what's new" closes it for this release.
- Toasts, `.toast` with role `alert` (`app/src/components/Toast.tsx`): a row's button goes to its session or runs its action, such as Undo.
- Where people see what agents do: each row's state, activity line, turn age and due time, and what still runs under "All settled".

### Agents

None. What agents do shows here: their state groups them ([agent-state.md](agent-state.md)), `attn ask` adds a row to Asks ([asks.md](asks.md)), and `attn turn open` puts them under "Your turn" ([queue-flow.md](queue-flow.md)). `attn callout` can point at the `home` and `settings-button` anchors ([callouts.md](callouts.md)).

### Triggers

- attn raises a notification when one of its duties needs the user: a background task that failed for good, with a retry (`internal/daemon/notifications.go`); a call auto mode blocked (`internal/daemon/automode.go`); a durable event-log consumer holding the log open (`internal/daemon/bus_pin_alarm.go`); a plugin it stopped restarting, which is critical (`internal/daemon/plugin_runtime.go`); and a notebook conflict copy (`internal/daemon/notebook_sync.go`).
- What's new opens by itself when the app has not shown the current release's intro (`app/src/hooks/useWhatsNew.ts`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers), launched fresh. Run the first two steps before the baseline's `bridge dismiss_whats_new`, which dismisses the intro and its banner; afterwards it changes nothing.
- On macOS, with the app already launched, run `open -a "$(./attn instance resolve --field appPath)"` before native input. This activates only the selected instance; launching it first preserves the mock environment.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-home.XXXX) && echo "export P=$P" >> $DRIVE_ENV`, and an agent folder, `A=$(mktemp -d /tmp/drive-attn-home-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "question", "actions": [{"type": "reply", "text": "Which option do you want?"}]}
]}
```

`<terminal>` is the agent's terminal id from `wsctl list`.

- **What's new on a fresh app.** `bridge dom_wait '{"selector":".whats-new-modal","timeoutMs":5000}'` matches, and `bridge dom_text '{"selector":".whats-new-count"}'` reads `1 of 7`. Run `bridge native_key '{"key":"ArrowRight"}'`: it reads `2 of 7`. Run `bridge native_key '{"key":"Escape"}'`: `.whats-new-modal` is gone, and `[data-testid="intro-banner"]` shows on home.
- **Replay and dismiss the banner.** Click `.intro-banner-replay` natively, converting its `bridge dom_bounds` box as the README says: the intro opens again; press Escape. Click `[aria-label="Dismiss the intro banner"]` natively: `bridge dom_wait '{"selector":"[data-testid=\"intro-banner\"]","absent":true,"timeoutMs":5000}'` matches.
- **What's new from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"whats new"}'` and `bridge native_key '{"key":"Enter"}'`. `.whats-new-modal` matches; press Escape.
- **Home with the key.** Start the agent: `wsctl add-session --cwd $A --agent claude --label home-a --id home-a`. Select it for setup with `bridge select_session '{"sessionId":"home-a"}'`, then run `bridge native_key '{"key":"h","modifiers":["command","shift"]}'`. `bridge get_state` shows `view: "dashboard"`, and `bridge home_get_state` shows `onScreen: true`, `allSettled: false` and `home-a` in `stateGroupSessionIds`.
- **Open an agent from home.** Click `[data-testid="session-home-a"]` natively. `bridge get_state` shows `view: "session"` and `activeLeaf.sessionId: "home-a"`.
- **Home from the palette and the sidebar.** Open the palette as above, type `Home` and press Enter: `view` is `"dashboard"`. Click `[aria-label="Open home-a"]` in the sidebar to leave, then click `[data-testid="sidebar-home"]` natively: `view` is `"dashboard"` again. Open `home-a` the same way, press ⌘B, and click `[aria-label="Home"]` on the rail natively: `view` is `"dashboard"`. Press ⌘B to expand.
- **Snoozed agents.** Run `./attn session snooze home-a --until 1h`; it prints `home-a snoozed until <instant>`. `bridge home_get_state` shows `snoozed.present: true` and `expanded: false`. Click `[data-testid="session-group-snoozed-header"]` natively: `expanded` is true, and `rows` holds `home-a` with its due time in `wake` and `canWake: true`. Click `[data-testid="session-wake-home-a"]` natively: `./attn session list --tree` no longer shows `snoozed until` for `home-a`, and `snoozed.present` is false.
- **Your turn.** Run `./attn settings set queue_mode_enabled true`. `bridge dom_wait '{"selector":"[data-testid=\"session-group-turns\"] [data-testid=\"session-home-a\"]","timeoutMs":5000}'` matches: the unsnooze opened the agent's turn again.
- **All settled, then the next turn.** Select `home-a` and press ⌘⇧E ([queue-flow.md](queue-flow.md)). The app lands home: `bridge home_get_state` shows `allSettled: true`, `detail: "Nothing is running."` and `followNextTurn: true`. Run `wsctl input --id <terminal> --text "question" --enter`. When the agent stops, the app takes the user to it: `bridge get_state` shows `activeLeaf.sessionId: "home-a"`.
- **Stay home instead.** Settle `home-a` again, then click `[data-testid="follow-next-turn"] input` natively: `followNextTurn` is false. Send `question` again. The user stays home, and the row returns under "Your turn".
- **The notifications popover.** Click `[aria-label="Show Notifications"]` natively. `bridge notifications_get_state` shows `present: true` and, on the baseline, `empty: "No notifications."`. Click `[aria-label="Hide Notifications"]` natively: `present` is false. Open the palette, type `Show notifications` and press Enter: `present` is true; run `Hide notifications` the same way.
- **Read a notification.** This needs a notification from another feature (see Gotchas). The bell carries `[aria-label="Unread notifications"]`. Open the popover: `rows` lists it with `unread: true`. Click its `.notification-row-head` natively: it expands and `unread` turns false. "Mark all read" marks the rest. For a critical notification, `button.critical-strip` sits at the top of the sidebar, and clicking it natively opens the popover.
- **A toast.** Open the palette, type `Sort desktops` and press Enter. `bridge dom_wait '{"selector":".toast","textIncludes":"Desktops sorted","timeoutMs":5000}'` matches. Click `.toast-row button` natively: `bridge dom_text '{"selector":".toast-row button small"}'` reads `✓ Done`. The Undo put the desktops back in the order they had before the sort ([desktops.md](desktops.md)); with one or two desktops that order may be the sorted one already, so the order proves nothing there.
- **Settings from home.** Go home with ⌘⇧H and click `[data-testid="settings-button"]` natively: `bridge dom_wait '{"selector":"[data-testid=\"settings-nav-general\"]","timeoutMs":5000}'` matches. Press Escape. The "+ New" button starts a new agent, which [agent-launch.md](agent-launch.md) drives.
- **Proof.** In `$P`, keep `bridge home_get_state` after each step on home, `bridge notifications_get_state` with the popover open, `bridge get_state` after each way home, and the `./attn` commands' output and exit codes. Capture `bridge capture_screenshot '{"path":"<P>/home-desktop-flow.png"}'` with the agent in a state group, `<P>/home-all-settled.png` with the banner, and `<P>/whats-new.png` with the intro open, with `<P>` written out. Put the way in in each file's name, such as `home-key-state.json`.

## Contexts

- **Durability across restarts**: home reads the daemon's sessions, turns and snoozes, so it shows the same after any restart. Notifications and what the user read are the daemon's records. What's new and its banner remember they were seen in the app's own storage. The follow checkbox is not kept: it is checked only by settling or snoozing the last turn, and cleared when the user goes anywhere.
- **Linux**: `session.goToDashboard` is Ctrl+Alt+H (`LINUX_SHORTCUTS`).
- **Outposts**: an agent whose outpost is offline is marked offline on home, never sits under "Your turn", and has no unsnooze button.
- **Profiles**: home lists the selected profile's sessions and asks. Notifications are the daemon's, not a profile's: every profile shows the same list.
- **Performance and idle cost**: turn ages re-read the clock every 30 seconds, and the GitHub rate-limit banner ticks every second only while it shows. Nothing measures home's idle cost; when a change touches its rendering, check it for repaints with `docs/perf-testing.md`.
- **Keyboard and native input**: ⌘⇧H reaches home from anywhere, a text field included. The intro takes ←, →, Enter and Escape. Home's session rows take clicks only (see Gotchas).
- **Flow**: in desktop flow, the Sessions card groups by state. Queue flow puts "Your turn" first and the rest under "Settled", and shows "All settled" when nothing is owed. The snoozed group shows in both.

## Tests

- Wire: `internal/daemon/notifications_wire_test.go`.
- Scenario: `home-dock`.
- Kernel: `internal/store/notifications_test.go`, `internal/daemon/notifications_render_test.go`.
- App: `app/src/App.dashboard.test.tsx`, `app/src/App.homeDock.test.tsx`, `app/src/App.notifications.test.tsx`, `app/src/App.criticalNotifications.test.tsx`, `app/src/App.whatsNew.test.tsx`, `app/src/components/Toast.lifecycle.test.tsx`.

## Gotchas

- On the reused `drive-2` instance at `8ed664831`, cleaning and reinstalling did not restore the fresh intro or its replay banner. The first two steps failed their freshness precondition. The palette still opened the intro, but that does not prove automatic opening or the banner controls. Keep those steps and report them as unreached when the release was already seen.
- A Home gear click on the parked, inactive app twice reported input delivered while Settings stayed closed. After activating the selected instance with `open -a`, the same native click opened Settings. Retain the failed receipts and activate the app before replaying this path.

- A fresh app opens What's new over home, and it holds the palette and most keys. The baseline's `bridge dismiss_whats_new` also dismisses the replay banner, so drive the intro first or not at all on that app.
- The baseline raises no notification; the popover reads "No notifications.". A row needs another feature's failure: a call auto mode blocks ([auto-mode.md](auto-mode.md)), a durable event-log consumer that stops reading ([diagnostics.md](diagnostics.md)), a plugin attn gives up restarting, which is the critical one ([harnesses.md](harnesses.md)), or a background task that fails for good. The read steps fail on the baseline for that reason.
- The queue bar has no bell. Open the popover from the palette; only a critical notification shows on the bar, as the strip.
- `bridge home_get_state` returns `shortcutFooter`, which reads a footer home no longer has; it is always false.
- ⌘⇧H, the Home row and the palette's Home leave the follow checkbox clear. Only settling or snoozing the last turn checks it, so a test of "the next turn takes me there" starts from a settle.
- A toast fades six seconds after it appears unless the pointer or focus is on it. Click it right away, or read it with `bridge dom_wait` first.
- The palette title "What's new" has an apostrophe, which breaks the single-quoted JSON as written; typing `whats new` matches the command through its keywords.
- Working but wrong: home's session rows are not buttons, so Tab never reaches them; from the keyboard the user reaches agents with ⌘J, ⌘K or the sidebar. The state groups say "Pending approval" for needs approval, and the snoozed row's button says "Wake" for unsnooze, both older names. The bell's label is "Show Notifications" while the palette says "Show notifications".
