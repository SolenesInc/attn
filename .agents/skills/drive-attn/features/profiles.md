# Profiles

Separate worlds for the user's work. Nothing in one profile exists in another.
A profile holds its own desktops, ledger, garden, crew and automations, and
every session belongs to the profile it started in, for life. The user
switches profiles from the window, creates, renames and deletes them, and
attn always keeps at least one. An install upgraded from before profiles
opens on its first profile with each old workspace on its own desktop.

## Sub-features

- `profile-switch`: show another profile in the window. After a restart the app comes back on the profile it showed last.
- `profile-create`: create a profile with one empty desktop, and switch to it.
- `profile-rename`: rename a profile. Names are unique among live profiles, ignoring case, and free again once deleted.
- `profile-delete`: delete an empty profile. attn refuses while the profile holds anything, and always keeps one.
- `profile-scope`: what belongs to a profile, and how another profile's things are refused as if they did not exist.
- `profile-from-shell`: `--profile <name|id>` for verbs run outside attn when several profiles exist.
- `profile-migration`: the one-time move of an install from before profiles onto desktops of its first profile.

## How to get to it

### People

- ⌘⇧U (`profile.switch`, `native/crates/attn-app/src/keymap.rs`) opens the profile switcher `profile-switcher` (`native/crates/attn-app/src/profile_switcher.rs`): ↑ and ↓ move, Enter switches, N names a new profile, R renames, ⌫ asks to delete, and Escape backs out or closes. Each row is `profile-switcher.choose.<id>`, and the list has "New profile".
- Palette (`native/crates/attn-app/src/palette/commands.rs`): `switch-profile` ("Switch profile") opens the switcher, and `profile-<id>` ("Switch to <name>") switches to each other profile directly.
- The title bar's profile pill, `titlebar.profile`, showing the profile's name (`native/crates/attn-app/src/profile_switcher.rs`).
- Their own shell: outside an attn session, `--profile <name|id>` picks the profile on the verbs that act in one, such as `attn seed`, `attn automation`, `attn delegate`, `attn open <seed-id>`, `attn codex` and `attn agent attachment`. `attn open <file>` takes no profile and opens the file on the window's current desktop. `attn session list` takes `--profile <id>` to list one profile's sessions, deleted profiles included. With one profile, they need no flag.

### Agents

None. An agent's attn commands act in its session's profile and cannot name another.

### Triggers

- The first daemon start after an upgrade from before profiles finishes the migration before any app connects (`FinishPendingProfileMigration`, `internal/store/profile_migration.go`). Each old workspace keeps its own desktop, the first nine on ⌘1–⌘9 by rank and the rest without a number, and each crew member and automation launches on a desktop of its own. A home an earlier version left waiting in its migration picker finishes the same way, keeping any arrangement the user had already made there.

## Driving it

Preconditions:

- Use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says. Then run `bridge wait '{"target":{"key":"home"}}'`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`. The chords below are Linux's. On macOS, the switcher is `mod+shift+u` and the palette `mod+shift+k`.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-profiles.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- `bridge state '{"of":"profiles"}'` prints `selected` and `all`, each profile with its `id`, `name` and `revision`. `<default>` and `<drive>` below are profile ids from it. `<seed>` is the id `seed plant` prints.

- **One profile at first.** `bridge state '{"of":"profiles"}'` lists only `Default` in `all`, and `selected` is its id. `./attn seed ls` answers without `--profile`.
- **Create.** Press `mod+alt+u`: `bridge wait '{"target":{"key":"profile-switcher"}}'` answers. Press `n`, wait for `profile-switcher.name`, type `Drive B` and press `Enter`. The switcher goes, `state` of `profiles` lists `Drive B` as `selected`, and `bridge find '{"target":{"key":"titlebar.profile"}}'` names it. Call its id `<drive>`. The new profile's desktop holds no tiles and shows the new agent picker. Send the picker no Enter.
- **An agent keeps its profile.** Run `wsctl add-session --cwd $P --agent shell --label drive-b-shell --id drive-b-shell --profile <drive> --place`: `sidebar.session.drive-b-shell` answers, and `bridge state '{"of":"sessions"}'` gives it `profile_id` `<drive>`.
- **The shell sees two profiles.** Run `./attn seed ls`. It exits 1 and names both profiles after `choose --profile <name|id>; available profiles:`. Run `./attn seed plant "Profile drive" --profile "Drive B"`: it prints the new seed's id, slug and title, and `<seed>` is that id. Run `bridge run '{"name":"board.open"}'`: `garden.row.<seed>` answers. Press Escape. `./attn session list --profile <drive>` exits 0 and lists `drive-b-shell`.
- **Every profile-scoped verb from the shell.** With `Drive B` still there, run each without `--profile`, then with `--profile "Drive B"`: `./attn automation list`, `./attn delegate status nonexist`, `./attn codex status` and `./attn agent attachment cap-x att-y --out $P/att.bin`. Each first run exits 1 with `choose --profile <name|id>; available profiles:`. With the flag, `automation list` prints `null`, `codex status` prints `profile  Drive B`, and `delegate status` and `agent attachment` get past the profile to their own refusal (`sql: no rows in result set`, `must be canonical UUIDs`). `./attn open <seed>` exits 1 the same way. `./attn open <seed> --profile Default` is refused with `seed <seed> belongs to profile "Drive B"`, and `--profile "Drive B"` prints `opened <seed>`. Close the seed tile that command opens with its `tile.<tile>.close` before deleting the profile.
- **Switch from the pill and the palette.** Press `titlebar.profile`, then `profile-switcher.choose.<default>`: `selected` is `<default>` and the shell's sidebar row goes. Press `mod+alt+k`, type `Switch to Drive B`, wait for `palette.row.command:profile-<drive>` and press `Enter`: `selected` is `<drive>` again. Run `bridge run '{"name":"board.open"}'` while `<default>` is selected: `garden.row.<seed>` does not answer.
- **Rename.** Press `mod+alt+u`, then `r`: `profile-switcher.name` is named `Rename Drive B` and holds the name. Press `End` and `Backspace` once, type `C` and press `Enter`: `state` of `profiles` names `<drive>` `Drive C`, and `profile-switcher.choose.<drive>` reads it. Press `Escape`.
- **Delete is refused while the profile holds work.** Press `mod+alt+u`, then `Backspace`: `profile-switcher.delete.ask` reads `Delete Drive C? Clean up its agents, crew, automations and tiles first.`. Press `Enter`: `profile-switcher.refusal` starts with `profile "Drive C" is not empty:`. Press `Escape` twice.
- **Delete the empty profile.** Run `./attn seed wither <seed> -m "profile drive done" --profile "Drive C"`: the output starts with `<seed> (profile-drive) is withered`. Run `wsctl rm-session --id drive-b-shell`. Open the switcher, press `Backspace`, then `Enter`: `state` of `profiles` no longer lists `<drive>` and selects the profile used before it, and `./attn seed ls` answers without `--profile` again.
- **The last profile stays.** With one profile left and the switcher still open, press `Backspace`: `profile-switcher.refusal` reads `Default is the last profile, and attn always keeps one.`.
- **An unfinished migration finishes at start.** Quit the app with `./attn instance stop-app` and stop the daemon with `./attn daemon stop`. Run `sqlite3 <data-dir>/attn.db "update profile_migration set phase='launch_required', launch_review_complete=0"`, then `./attn daemon ensure`. `sqlite3 <data-dir>/attn.db "select phase, launch_review_complete from profile_migration"` prints `complete|1`, and `sqlite3 <data-dir>/attn.db "select kind, item_id, desktop_id from launch_desktops"` gives `crew|chief` a desktop. Launch: `home` answers.
- **Proof.** In `$P`, keep `bridge state '{"of":"profiles"}'` after each create, switch, rename and delete, each `./attn` command's output with its exit code, `bridge find` of each refusal, and `bridge shot '{"path":"<P>/profile-switcher.png"}'` with the switcher open on two profiles, `<P>` written out. The `native-profile-lifecycle` scenario runs the switcher steps, except the unfinished migration. For it, keep each `sqlite3` output and `bridge find '{"target":{"key":"home"}}'`. Name each file for its way in, such as `profiles-palette-state.json`.

## Contexts

- **Durability across restarts**: profiles, their desktops and the selected profile are daemon records and come back after any restart, and the app comes back on the profile it showed last. Deleting the profile a window shows moves that window to the most recently used remaining profile.
- **Linux**: `profile.switch` is Ctrl+Alt+U where the app runs on Linux (`native/crates/attn-app/src/keymap.rs`), and `--profile` works the same on a Linux CLI.
- **Outposts**: outposts serve every profile. A terminal on an outpost belongs to the profile that started it, and removing an outpost closes its terminals across all profiles.
- **Profiles**: desktops, the ledger, crew, automations and the garden belong to one profile, and so does every session for life. Settings, shortcuts, outposts, worktrees and the GitHub pull request list belong to the daemon and are the same in every profile. A session or desktop of another profile is refused as if it did not exist, a seed of another profile is refused by name (`seed <id> belongs to profile "<name>"`), and resuming another profile's conversation opens a new session here.
- **Keyboard and native input**: ⌘⇧U opens the switcher with focus on its list, and N, R, ⌫, arrows, Enter and Escape reach every action without the pointer. No macOS menu item claims ⌘⇧U.
- **Compatibility across versions**: an install from before profiles migrates once, at its first daemon start, with no step for the user. `attn profile` and `attn profile-env` are not about profiles: they are the retired names of `attn instance` and `attn instance-env`, print the new command and exit 2.
- **Flow**: flow is one daemon-wide setting for all profiles, so switching profiles keeps the flow.
- **Accessibility**: the profile switcher's menu keeps focus while ↑ and ↓ move its highlight, and names the highlighted profile as its active descendant, so VoiceOver reads each profile as the arrows reach it.

## Tests

- Wire: `native/crates/attn-app/tests/wire/profile.rs` (the switcher), `internal/daemon/profiles_protocol_test.go`, `internal/daemon/profile_delete_crew_wire_test.go`, `internal/daemon/garden_profile*_wire_test.go`, `internal/daemon/profile_migration_wire_test.go` and `internal/daemon/launch_desktop_wire_test.go` (an upgraded home opens finished).
- Stack: `cmd/attnd/automation_profile_stack_test.go`.
- Scenario: `native-profile-lifecycle`.
- Kernel: `internal/profilemigration/*_test.go`, `internal/store/profile*_test.go`.

## Gotchas

- A new profile's empty desktop shows the new agent picker with its path field focused, and Escape does not close it. An Enter sent there, such as one left over from a failed step, launches the highlighted Recent folder as an agent in that profile, and the profile then refuses to delete.
- The profile pill is always in the title bar, and the profile list opens under it at the right end.
- A second profile changes every shell recipe: outside a session, `attn seed` and the other profile-scoped verbs refuse without `--profile` once two profiles exist. Delete the profiles a run creates, or later recipes fail for a reason that is not theirs.
- The switcher orders profiles by most recent use and starts on the current one, so the arrow presses to reach a profile depend on what was used last.
- Delete refuses while the profile holds live agents, crew members, automations, tiles, open seeds, pending delegations or running garden reviews. Harvest or wither the profile's seeds first. A new profile's empty desktop does not count.
- `wsctl add-session` without `--profile` lands in the daemon's selected profile, not necessarily the one the window shows.
- Placing the old workspaces needs a database from before profiles, which a hand-made home lacks. The daemon wire tests build one at schema 153; the live step above covers only the launch half.
- Working but wrong: switching profile never changes the flow. A ledger request that names no profile may list every profile's sessions (glossary, Changes to make).
