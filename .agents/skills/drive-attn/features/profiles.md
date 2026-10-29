# Profiles

Separate worlds for the user's work; nothing in one profile exists in another.
A profile holds its own desktops, ledger, garden, crew and automations, and
every session belongs to the profile it started in, for life. The user
switches profiles from the window, creates, renames and deletes them, and
attn always keeps at least one. An install upgraded from before profiles sorts
its old arrangement into desktops of its first profile once, through a picker.

## Actions and ways in

### Switch, create, rename and delete

⌘⇧U (`profile.switch`, `app/src/shortcuts/registry.ts`) opens the switcher
(`app/src/components/ProfileSwitcher.tsx`): ↑ and ↓ move, Enter switches, N names a new profile
("Create and switch"), R renames, ⌫ asks to delete, Escape backs out or closes. Rows also offer
"Rename <name>" and "Delete <name>", and the list has "New profile". Palette `switch-profile` opens
it; `profile-<id>` switches directly ("Switch to <name>", `app/src/application/useAppCommands.tsx`).
The sidebar's `.sidebar-profile-pill`, titled "Switch profile (⌘⇧U)"
(`app/src/components/SidebarHeader.tsx`), and `[data-testid="queue-profile-pill"]`
(`app/src/components/QueueBar.tsx`) open it too. A new profile starts with one empty desktop and
becomes current. Names are unique case-insensitively among live profiles and become free after
deletion. Delete refuses while the profile holds anything, and at least one profile stays. After
restart the app returns to the profile it last showed.

### Choose a profile in the shell

Outside an attn session, `--profile <name|id>` selects the profile for scoped verbs such as `attn seed`, `attn automation`, `attn delegate`, `attn open`, `attn codex` and `attn agent attachment`;
one profile needs no flag. `attn session list --profile <id>` lists one profile's sessions, deleted
profiles included. An agent's commands act in its session's profile and cannot name another.

### Migrate an old arrangement

The first daemon start after an upgrade from before profiles prepares a migration; the app shows the
picker until it completes (`internal/daemon/profile_migration.go`,
`app/src/components/MigrationPicker/`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-profiles.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.

`<default>` and `<drive>` below are the profile ids `bridge get_state` prints under `arrangement.profiles`; `<seed>` is the id `seed plant` prints.

- **One profile at first.** Run `bridge get_state`. `arrangement.profiles` lists only `Default`, and `arrangement.selectedProfileId` is its id. `./attn seed ls` answers without `--profile`.
- **Open the switcher with the shortcut.** Press ⌘⇧U: `bridge native_key '{"key":"u","modifiers":["command","shift"]}'`, then `bridge dom_wait '{"selector":"[role=\"menu\"][aria-label=\"Switch profile\"]","focused":true,"timeoutMs":5000}'`. `bridge dom_text '{"selector":"[role=\"menu\"][aria-label=\"Switch profile\"]"}'` reads `Default` and `New profile`.
- **Create.** Press `n` (`bridge native_key '{"key":"n"}'`): `bridge dom_wait '{"selector":"#profile-switcher-name","focused":true,"timeoutMs":5000}'` matches. Run `bridge native_text '{"text":"Drive B"}'` and `bridge native_key '{"key":"Enter"}'`. The switcher closes, `bridge get_state` lists `Default` and `Drive B` with `selectedProfileId` set to `<drive>`, and `arrangement.desktops` holds one desktop, with no tiles on it. `bridge dom_text '{"selector":".sidebar-profile-pill"}'` names `Drive B`.
- **The shell sees two profiles.** Run `./attn seed ls`. It exits 1 and names both profiles after `choose --profile <name|id>; available profiles:`. Run `./attn seed plant "Profile drive" --profile "Drive B"`: it prints the new seed's id, slug and title, and `<seed>` is that id. Press ⌘⇧T and run `bridge garden_get_state`: `seeds` lists `<seed>`. Press Escape. `./attn session list --profile <drive>` exits 0 and lists no session, since `Drive B` has none.
- **Open the switcher from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"Switch profile"}'` and `bridge native_key '{"key":"Enter"}'`. `[role="menu"][aria-label="Switch profile"]` takes focus. Press Escape.
- **Switch from the palette.** Open the palette the same way, type `Switch to Default` with `bridge native_text` and press Enter. `bridge get_state` shows `selectedProfileId` `<default>`. Open the garden again: `bridge garden_get_state` no longer lists `<seed>`.
- **Switch from the sidebar pill.** Read `.sidebar-profile-pill` (or `[data-testid="queue-profile-pill"]` when the queue bar shows) with `bridge dom_bounds`, convert it as the README says, and click it with `bridge native_mouse`. The switcher opens on `Default`. Press ↓ (`bridge native_key '{"key":"ArrowDown"}'`) and Enter: `selectedProfileId` is `<drive>`.
- **Rename.** Press ⌘⇧U; the switcher starts on the current profile. Press `r`: `#profile-switcher-name` takes focus holding `Drive B`. Press ⌘A (`bridge native_key '{"key":"a","modifiers":["command"]}'`), type `Drive C` with `bridge native_text`, and press Enter. The switcher returns to its list, and `bridge get_state` names `<drive>` `Drive C`. Press Escape.
- **Delete is refused while the profile holds work.** Press ⌘⇧U, then Backspace (`bridge native_key '{"key":"Backspace"}'`): the switcher reads `Delete Drive C? Clean up its agents, crew, automations and tiles first.`. Press Enter. `bridge dom_text '{"selector":".profile-switcher-error"}'` names `profile "Drive C" is not empty:` and counts `1 open seeds`. Press Escape twice.
- **Empty it, then delete with the row's button.** Run `./attn seed wither <seed> -m "profile drive done" --profile "Drive C"`; its output starts with `<seed> is withered`. Press ⌘⇧U and click `[aria-label="Delete Drive C"]` natively: the switcher asks `Delete Drive C?`. Press Enter. The switcher returns to its list, `bridge get_state` lists only `Default` and selects it, and `./attn seed ls` answers without `--profile` again.
- **The last profile stays.** With the switcher still open, press Backspace. `bridge dom_text '{"selector":".profile-switcher-error"}'` reads `Default is the last profile, and attn always keeps one.`.
- **The list's buttons.** Click `[aria-label="Rename Default"]` natively: `#profile-switcher-name` takes focus holding `Default`. Press Escape to go back to the list. Click `.profile-switcher-actions button` ("New profile") natively: `#profile-switcher-name` takes focus, empty. Press Escape twice.
- **The migration picker.** On an install upgraded from before profiles, the app opens on the picker's welcome. Confirm, move or merge each desktop, then finish: the picker closes on "Your Default profile is ready." This needs copied data the baseline lacks, as Gotchas says.
- **Proof.** In `$P`, keep `bridge get_state` after each create, switch, rename and delete; each `./attn seed` command's output with its exit code; `bridge dom_text` of the switcher's refusals; and `bridge capture_screenshot '{"path":"<P>/profile-switcher.png"}'` with the switcher open on two profiles, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `profiles-palette-state.json`.

## Contexts

- **Durability across restarts**: profiles, their desktops and the selected profile are daemon records and come back after any restart, and the app comes back on the profile it showed last. Deleting the profile a window shows moves that window to the most recently used remaining profile.
- **Linux**: `profile.switch` is Ctrl+Alt+U where the app runs on Linux (`LINUX_SHORTCUTS`), and `--profile` works the same on a Linux CLI.
- **Outposts**: outposts serve every profile. A terminal on an outpost belongs to the profile that started it, and removing an outpost closes its terminals across all profiles.
- **Profiles**: desktops, the ledger, crew, automations and the garden belong to one profile, and so does every session for life. Settings, shortcuts, outposts, worktrees and the GitHub pull request list belong to the daemon and are the same in every profile. A seed, session or desktop of another profile is refused as if it did not exist, and resuming another profile's conversation opens a new session here.
- **Keyboard and native input**: ⌘⇧U opens the switcher with focus on its list, and N, R, ⌫, arrows, Enter and Escape reach every action without the pointer. No macOS menu item claims ⌘⇧U.
- **Compatibility across versions**: an install from before profiles migrates once, through the picker. `attn profile` and `attn profile-env` are not about profiles: they are the retired names of `attn instance` and `attn instance-env`, print the new command and exit 2.
- **Flow**: flow is one daemon-wide setting today, so switching profiles keeps the flow, though the glossary gives each profile its own.

## Tests

- Wire: `internal/daemon/profiles_protocol_test.go`, `internal/daemon/profile_delete_crew_wire_test.go`, `internal/daemon/garden_profile*_wire_test.go`, `internal/daemon/chief_profiles_wire_test.go`.
- Stack: `cmd/attn/automation_profile_stack_test.go`.
- Scenario: `profile-lifecycle`, `garden-profile-scope`, `desktop-migration`.
- Kernel: `internal/profilemigration/*_test.go`, `internal/store/profile*_test.go`.
- App: `app/src/App.migrationPicker.test.tsx`, `app/src/components/ProfileSwitcher.keyboard.test.tsx`, `app/src/components/MigrationPicker/MigrationPicker.flow.test.tsx`, `app/src/utils/selectedProfile.test.ts`, `app/src/hooks/useDaemonSocket.profiles.test.tsx`, `app/src/components/ledger/SessionsTab.profiles.test.tsx`.

## Gotchas

- A second profile changes every shell recipe: outside a session, `attn seed` and the other profile-scoped verbs refuse without `--profile` once two profiles exist. Delete the profiles a run creates, or later recipes fail for a reason that is not theirs.
- The switcher orders profiles by most recent use and starts on the current one, so the arrow presses to reach a profile depend on what was used last.
- Delete refuses while the profile holds live agents, crew members, automations, tiles, open seeds, pending delegations or running garden reviews. Harvest or wither its seeds first; a new profile's empty desktop does not count.
- `wsctl add-session` without `--profile` lands in the daemon's selected profile, not necessarily the one the window shows.
- The migration picker appears only on an install upgraded from before profiles. A fresh instance never shows it, and driving it needs data copied read-only from such an install. Scenario `desktop-migration` drives it on Linux only (macOS skips it); its app tests cover it otherwise.
- Working but wrong: switching profile never changes the flow. A ledger request that names no profile may list every profile's sessions (glossary, Changes to make).
