# Home screen

The home screen is the dashboard of what asks for the user's attention across
agents, with notifications. Its Sessions card lists the profile's sessions by
what they need: in queue flow the agents that are owed a turn come first, and in
desktop flow sessions are grouped by state. Snoozed agents wait in their own
group until they are due. Home also shows the open asks, the chief and the
pull requests waiting on the user. The notifications popover holds what attn
itself needs the user to know, such as a duty that failed for good. The app
opens on home. With the experimental setting `views_enabled` on, home is the
Home desktop instead (`home-desktop`).

## Sub-features

- `dashboard`: the Sessions card, grouped by state. In queue flow, "Your turn" comes first, then "Settled".
- `all-settled`: queue flow's "All settled" banner, with what still runs and "Take me to the next agent that needs you".
- `snoozed-group`: snoozed agents, soonest first, each with its due time and an unsnooze button.
- `home-cards`: the Asks group and the Pull Requests card. [asks.md](asks.md) and [pull-requests.md](pull-requests.md) own their content.
- `notifications`: the popover of attn's own notifications, newest first. Unread rows are marked, a row expands to its detail and actions, and "Mark all read" clears the marks.
- `critical-strip`: an unread critical notification shows as a strip at the top of the sidebar.
- `whats-new`: the intro that opens on a new release, and the home banner that replays it.
- `toasts`: short-lived errors and undo offers in a corner of the window. The user clicks a toast's button to run its action.
- `view-tiles`: with the experimental setting `views_enabled`, a markdown tile whose params are `{"view": "attn"}`, `{"view": "sessions"}` or `{"view": "pull-requests"}` shows that part of home on any desktop, drawn by home's code: the brand line with Settings and the what's-new banner, the Sessions card under "All settled", or the Pull Requests card under the rate-limit notice. Its controls keep their `home.*` keys inside `tile.<tile id>`. Its sidebar row reads `VIEW` and the tile's title is the view's. On a desktop that shows Sessions, `home.follow` takes the user to the next turn from there, as on home. A view tile does not pop out: its header has no pop-out button, and ⇧⌘O leaves it in place. Behind the same setting, a markdown file written in the view language opens as a view drawn part by part ([markdown.md](markdown.md) `view-document`).
- `home-desktop`: with `views_enabled` on, each profile has one desktop of kind `home` (`<profile>/home`), at the top of the sidebar's desktops by default. It holds `tile-view-attn` above `tile-view-sessions`, with `tile-view-pull-requests` in the next column, and takes the place of the Home row and the old home screen. Going home is `desktop_set_current` to it, so every window and the next launch show it. It has no digit, no close and no rename, and stays when empty. It moves like any desktop, and `desktop_reset_home` puts it back at the top with its three views, keeping other tiles after them. Its tiles always take the softer look: rounded, on a darker backdrop, with no line under the header. Other desktops take it from the palette's `soft-tiles` (app pref `attn.workspace.softTiles`). With the setting off, the Home desktop is hidden and the work on it moves to the next desktop.

## How to get to it

### People

- ⌘⇧H (`session.goToDashboard`, `native/crates/attn-app/src/keymap.rs`).
- Palette (`native/crates/attn-app/src/palette/commands.rs`): `home` ("Home"), `notifications` ("Show notifications" or "Hide notifications"), `whats-new` ("What's new"). With `views_enabled` on and a current desktop, `view-attn`, `view-sessions` and `view-pull-requests` ("Show attn in a tile", "Show Sessions in a tile", "Show Pull requests in a tile") dock the view as `tile-view-<name>` on the current desktop, or go to the tile that already shows it.
- With `views_enabled` on, the same key, palette `home`, Home row and rail button go to the Home desktop. Its sidebar line is `sidebar.desktop.<profile>/home`, named "Open Home", and replaces `sidebar.home`. The palette adds `put-home-back` ("Put Home back to its default") and leaves out `close-desktop` on Home. `soft-tiles` toggles the softer look on other desktops.
- Sidebar: the Home row `sidebar.home` above everything (`native/crates/attn-app/src/sidebar/mod.rs`), and `rail.home` on the collapsed rail (`native/crates/attn-app/src/sidebar/chips.rs`). The bell in the title bar, `titlebar.notifications`, named "Show Notifications", with the unread count as its value. The critical strip, `sidebar.critical`.
- On home (`native/crates/attn-app/src/home/mod.rs`): press a session row `home.session.<session>` to open the agent. Press the snoozed group's header `home.group.snoozed.toggle` to expand the group, and `home.session.<session>.unsnooze` to end a snooze. The follow checkbox is `home.follow`. The intro banner has `home.intro-banner.replay` and `home.intro-banner.dismiss`. Home also hosts "New" (`home.new`, named "New session") ([agent-launch.md](agent-launch.md)), the settings gear `home.settings` ([settings.md](settings.md)) and the cards above.
- The notifications panel, `notifications.panel` (`native/crates/attn-app/src/notifications/view.rs`): a press on a row's head expands the row and marks it read. Each row has action buttons, such as a retry, and the panel has "Mark all read", `notifications.mark-all`.
- What's new, `whats-new-dialog` (`native/crates/attn-app/src/whats_new/mod.rs`): ← and → step through it, Enter advances, and Escape closes it for this release.
- Toasts at the window's bottom right, `notice.text` and `notice.offer` (`native/crates/attn-app/src/shell.rs`): an offer's button runs its action, such as Undo.
- Where people see what agents do: each row's state, activity line, turn age and due time, and what still runs under "All settled".

### Agents

None. What agents do shows here. Home groups agents by their state ([agent-state.md](agent-state.md)), `attn ask` adds a row to Asks ([asks.md](asks.md)), and `attn turn open` puts the agent under "Your turn" ([queue-flow.md](queue-flow.md)). `attn callout` can point at the `home` and `settings-button` anchors ([callouts.md](callouts.md)).

### Triggers

- attn raises a notification when one of its duties needs the user. The cases are a background task that failed for good, with a retry (`internal/daemon/notifications.go`), a durable event-log consumer holding the log open (`internal/daemon/bus_pin_alarm.go`), a plugin that attn stopped restarting (`internal/daemon/plugin_runtime.go`), a notebook conflict copy (`internal/daemon/notebook_sync.go`), a tender that could not be woken (`internal/daemon/tender_resume.go`), a failed login-environment capture (`internal/daemon/login_env.go`), a rejected PTY host (`internal/daemon/pty_settings.go`), a requested update (`internal/daemon/update_run.go`), a Codex update (`internal/harness/internal/codex/update.go`) and a conversation whose references are incomplete (`internal/daemon/conversation_keep.go`). The plugin case and the unwakeable tender are critical.
- The app opens What's new by itself when it has not shown the current release's intro (`native/crates/attn-app/src/whats_new/mod.rs`).

## Driving it

Preconditions:

- Use the baseline instance with its app, launched fresh as [../SKILL.md](../SKILL.md) says. Then run `bridge wait '{"target":{"key":"home"}}'`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`.
- The steps press the macOS chords. On Linux press `mod+alt+k` for `mod+shift+k`, `mod+alt+h` for `mod+shift+h`, `mod+alt+e` for `mod+shift+e` and `mod+alt+b` for `mod+b`.
- The app keeps What's new and its banner in its own prefs, `~/.config/<bundle id>/` on Linux, apart from the instance's data. A freshly installed instance greets with What's new. One that already showed this release does not, and the first step is unreached there.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-home.XXXX) && echo "export P=$P" >> $DRIVE_ENV`. Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-home-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`. Save this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "question", "actions": [{"type": "reply", "text": "Which option do you want?"}]}
]}
```

- Start the agent after the What's new steps: `wsctl add-session --cwd $A --agent claude --label home-a --id home-a --place`.

`<terminal>` is the agent's terminal id from `wsctl list`.

- **What's new on a fresh app.** Before anything else, `bridge wait '{"target":{"key":"whats-new-dialog"}}'` answers, a dialog named "What's new". `whats-new.count` reads "1 of 2" and `whats-new.title` "A new look". `whats-new.scene` is an image named for the step. Press `Right`: "2 of 2", titled "More colors", with the button "Got it". Press `Left`: "1 of 2". Press `Escape`: the dialog goes and `home.intro-banner` shows on home.
- **Replay and dismiss the banner.** Run `bridge press '{"target":{"key":"home.intro-banner.replay"}}'`: the tour opens on "1 of 2". Press `Escape`, then press `home.intro-banner.dismiss`: `home.intro-banner` goes. Quit and launch the app again: neither the tour nor the banner shows.
- **What's new from the palette.** Press `mod+shift+k`, run `bridge type '{"text":"whats new"}'` and press `Enter`: `whats-new-dialog` shows on "1 of 2". Press `whats-new.shortcuts`: the tour closes and the keyboard shortcuts dialog shows. Press `Escape`.
- **Home groups agents by state.** `bridge wait '{"target":{"key":"home.session.home-a"}}'` answers inside `home.group.waiting`, or the group for the state `wsctl list` shows.
- **Open an agent from home, and home with the key.** Run `bridge press '{"target":{"key":"home.session.home-a"}}'`: `bridge state '{"of":"window"}'` shows `view: "Desktop"` and `on_screen: "home-a"`. Press `bridge key '{"chord":"mod+shift+h"}'`: `view` is `Home`.
- **Home from the palette, the sidebar and the rail.** Open `home-a`, press `mod+shift+k`, type `Home` and press `Enter`: `view` is `Home`. Open `home-a` again and run `bridge press '{"target":{"key":"sidebar.home"}}'`: `view` is `Home`. Open it again, press `mod+b` and run `bridge press '{"target":{"key":"rail.home"}}'`: `view` is `Home`. Press `mod+b` to expand.
- **Snoozed agents.** Run `./attn session snooze home-a --until 1h`. It prints `home-a snoozed until <instant>`. `bridge wait '{"target":{"key":"home.group.snoozed.toggle"}}'` answers, and `bridge tree` shows it collapsed. Press it: the tree shows it expanded, and `home.session.home-a` shows with its due time in `home.session.home-a.when`. Run `bridge press '{"target":{"key":"home.session.home-a.unsnooze"}}'`: `./attn session list --tree` no longer shows `snoozed until` for `home-a`, and the snoozed group goes.
- **Your turn.** Run `./attn settings set queue_mode_enabled true`: `home.session.home-a` shows inside `home.group.turns`. attn opened the agent's turn again when the snooze ended.
- **All settled, then the next turn.** Open `home-a` and press `mod+shift+e` ([queue-flow.md](queue-flow.md)). The app goes home: `bridge wait '{"target":{"key":"home.all-settled"}}'` answers and `bridge tree` shows `home.follow` checked. Run `wsctl input --id <terminal> --text "question" --enter`. When the agent stops, `bridge state '{"of":"window","expect":{"on_screen":"home-a"}}'` answers.
- **Stay home instead.** Settle `home-a` again, then run `bridge press '{"target":{"key":"home.follow"}}'`: the tree shows it unchecked. Send `question` again. The user stays home, and the row returns under `home.group.turns`.
- **The notifications panel.** `bridge find '{"target":{"key":"titlebar.notifications"}}'` is the bell, named "Show Notifications", its value the unread count. Press `mod+shift+k`, run `bridge type '{"text":"show notifications"}'` and press `Enter`: `bridge wait '{"target":{"key":"notifications.panel"}}'` answers, and on the baseline `notifications.state` reads "No notifications.". Press `Escape`: the panel goes and the bell has the keyboard. While the panel is open the window is inert behind it, so the bell is not in `bridge tree`. A click outside the panel closes it.
- **Read a notification.** Raise a critical notification with a plugin that attn gives up restarting.
  1. Run `C=$(mktemp -d /tmp/drive-attn-home-crash.XXXX) && echo "export C=$C" >> $DRIVE_ENV`.
  2. Write `$C/attn-plugin.toml` with `name = "home-crash"`, `version = "0.0.1"`, `attn_api_version = 7`, and a `[plugin]` table that holds `kind = "executable"` and `path = "crash.sh"`.
  3. Write `$C/crash.sh` as `#!/bin/sh` followed by `exit 1`, and make it executable.
  4. Write `$C/package.json` as `{"name":"home-crash","version":"0.0.1","private":true}`.
  5. Run `./attn plugin install --path $C`.

  About two minutes later `./attn plugin list` shows `home-crash` with `runtime_phase` `parked` and `restart_attempt` 10, and `bridge wait '{"target":{"key":"sidebar.critical"}}'` answers above `sidebar.home`, named "1 unread critical notification: Plugin stopped: home-crash. Open notifications.". A wait gives up after 30 s, so repeat it. Press the strip: the panel opens, and its row `notifications.row.<id>` has the value "critical, unread". Press `notifications.row.<id>.head`: `notifications.row.<id>.detail` shows, the row's value turns "critical", and the strip goes.
- **Mark all read.** Run `./attn plugin uninstall home-crash`, then install the plugin again. When the strip comes back, press the bell, then `notifications.mark-all`: every row loses "unread" and "Mark all read" is disabled. Press `Escape`: the bell's value is 0. Run `./attn plugin uninstall home-crash`.
- **A toast.** Press `mod+shift+k`, type `Sort desktops` and press `Enter`. `bridge wait '{"target":{"key":"notice.offer"}}'` answers, named `Desktops sorted Undo`. Press it: its name ends in `✓ Done`. Undo puts the desktops back in the order they had before the sort ([desktops.md](desktops.md)). With one or two desktops, that order may already be the sorted one, so the order proves nothing there.
- **Settings and a new agent from home.** Run `bridge press '{"target":{"key":"home.settings"}}'` and `bridge press '{"target":{"key":"home.new"}}'`: each opens its feature, whose recipe is in its own file ([settings.md](settings.md), [agent-launch.md](agent-launch.md)).
- **Views in tiles.** This step needs the mock GitHub's pull requests ([pull-requests.md](pull-requests.md)). Run `./attn settings set views_enabled true` and `./attn settings set queue_mode_enabled true`, settle `home-a`, and open its desktop. Four tiles need a wide window: start Xvfb with `-screen 0 2560x1300x24` and run `bridge set_window_bounds '{"logicalBounds":{"x":0,"y":0,"width":2400,"height":1200}}'`. Press `mod+shift+k`, type `Show attn in a tile` and press `Enter`. Do the same for `Show Sessions in a tile` and `Show Pull requests in a tile`. Each docks at the right of the active tile. `bridge state '{"of":"desktops"}'` shows three `markdown` tiles with `{"view":…}` params beside the terminal. Press `mod+alt+=` until every tile is on screen. `bridge tree` holds `home.settings` inside `tile.tile-view-attn`, `home.all-settled` and `home.follow` inside `tile.tile-view-sessions`, and `home.pr.<id>.approve` inside `tile.tile-view-pull-requests`. Tab to each and press `space`: follow toggles, approve sends `approve_pr` (the daemon log names the PR), and Settings opens its dialog. With follow checked, send `question` to `home-a`: when the agent stops, `on_screen` is `home-a`, as from home. Run `Show Sessions in a tile` again: the window's `keyboard.pane` is `tile-view-sessions`, and no second tile docks. With `views_enabled` off, the palette offers none of the three, and the daemon refuses a view tile, naming the setting.
- **Proof.** In `$P`, keep `bridge state '{"of":"window"}'` after each way home, the `./attn` commands' output and exit codes, and `bridge tree` after each step on home. Capture `bridge shot '{"path":"<P>/home.png"}'` with the agent in a state group, `<P>/home-all-settled.png` with "All settled", `<P>/critical-strip.png` with the strip, `<P>/notifications.png` with the panel open and `<P>/whats-new.png` with the tour open, with `<P>` written out. Name each file for its way in, such as `home-key-window.json`.

## Contexts

- **Durability across restarts**: home reads the daemon's sessions, turns and snoozes, so it shows the same after any restart. Notifications and what the user read are the daemon's records. The app remembers in its own storage that the user saw What's new and its banner. The follow checkbox is not kept. attn ticks the checkbox only when the user settles or snoozes the last turn, and clears it when the user goes anywhere.
- **Linux**: `session.goToDashboard` is Ctrl+Alt+H (`native/crates/attn-app/src/keymap.rs`).
- **Outposts**: an agent whose outpost is offline is marked offline on home, never sits under "Your turn", and has no unsnooze button.
- **Profiles**: home lists the selected profile's sessions and asks. A notification about something a profile owns shows only in that profile: a failure for a session shows in the profile that owns it, and a Notebook conflict in the profiles that use its folder. Notifications about the daemon itself, such as a stopped plugin, show in every profile. The unread count follows the selected profile.
- **Performance and idle cost**: home re-reads the clock for turn ages every 30 seconds, and the GitHub rate-limit banner ticks every second only while one shows, on home or in a Pull Requests tile. Nothing measures home's idle cost. When a change touches home's rendering, check it for repaints with `docs/internal/perf-testing.md`.
- **Keyboard and native input**: ⌘⇧H reaches home from anywhere, a text field included. The intro takes ←, →, Enter and Escape. → stops on the last step, and only Enter or Got it there closes the tour. The tour draws each step's scene as it rests after the change, without motion.
- **Flow**: in desktop flow, the Sessions card groups by state. Queue flow puts "Your turn" first and the rest under "Settled", and shows "All settled" when no agent is owed a turn. The snoozed group shows in both.

## Tests

- Wire: `native/crates/attn-app/tests/wire/home_desktop.rs`, `internal/daemon/home_desktop_wire_test.go`, `native/crates/attn-app/tests/wire/home.rs`, `native/crates/attn-app/tests/wire/notifications.rs`, `native/crates/attn-app/tests/wire/whats_new.rs`, `native/crates/attn-app/tests/wire/view_tiles.rs`, `internal/daemon/notifications_wire_test.go`, `internal/daemon/view_tiles_wire_test.go`.
- Scenario: `native-agent-queue` (All settled and wake), `native-home-desktop` (the Home key, a digit, moving Home, putting it back, and a restart onto Home).
- Migration: `internal/store/home_desktop_migration_test.go`.
- Kernel: `internal/store/notifications_test.go`, `internal/daemon/notifications_render_test.go`.

## Gotchas

- On the reused `drive-2` instance at `8ed664831`, cleaning and reinstalling did not restore the fresh intro or its replay banner. The first two steps failed their freshness precondition. The palette still opened the intro, but opening it from the palette does not prove automatic opening or the banner controls. Keep those steps and report them as unreached when the release was already seen.
- The baseline raises no notification, and the panel reads "No notifications.". The read steps raise one with a plugin that exits at once. The other sources need what the baseline lacks. A notebook conflict copy needs an outpost. A durable event-log consumer that stops reading ([diagnostics.md](diagnostics.md)) and a background task that fails for good need the failure to really happen. A row's action buttons, such as a task's retry, come only with a failed task, so they stay undriven on the baseline.
- No toast carries a session today, so a toast's button only runs its action.
- The queue bar has no bell. Open the panel from the palette. Only a critical notification shows on the bar, as the strip.
- ⌘⇧H, the Home row and the palette's Home leave the follow checkbox clear. Only settling or snoozing the last turn ticks it, so a test of "the next turn takes me there" starts from a settle.
- A toast fades six seconds after it appears unless the pointer rests on it. Press it right away, or read it with `bridge wait` first.
- The palette title "What's new" has an apostrophe, which breaks the single-quoted JSON as written. Typing `whats new` matches the command through its keywords.
- The mock GitHub has no rate-limit mode, so the rate-limit notice in a Pull Requests view tile shows only in `view_tiles.rs`.
- Working but wrong: the bell's label is "Show Notifications" while the palette says "Show notifications".
