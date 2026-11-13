# Worktrees

The git worktrees agents work in. attn creates them for delegations, then
keeps or sweeps them when the work is done. Once attn creates a worktree in a
repository, attn tracks every worktree of that repository. A background sweep
reclaims a worktree whose work has landed and that nothing holds. The list shows
why the sweep keeps each of the others. People and agents can pin a worktree so the
sweep never takes it, delete a worktree now, and read what the sweep removed.

## Sub-features

- `worktree-create`: a delegation, the new session dialog or opening a pull request makes a worktree, and attn tracks that repository from then on.
- `worktree-list`: every worktree of every tracked repository, with its branch, state and why the sweep keeps it.
- `worktree-keep`: pin a worktree so the sweep never reclaims it, and unpin it.
- `worktree-sweep`: the background pass that reclaims merged, clean, idle worktrees nothing holds, on by default.
- `worktree-refresh`: run a pass now instead of on the hourly tick.
- `worktree-delete`: remove a worktree now. A dirty worktree needs force, and its sessions close with it.
- `worktree-log`: what the sweep or a person removed, and why, and the repositories the sweep stopped tracking because their folder is gone. attn also adds a note about each removal to the seeds that worked there.
- `worktree-names`: the generated branch name the new session dialog offers for a new worktree.

## How to get to it

### People

- Palette (`app/src/application/useAppCommands.tsx`): `worktrees` ("Open worktrees") opens the Worktrees list.
- ⌘⇧L (`sessions.open`, `app/src/shortcuts/registry.ts`) opens the ledger surface on Sessions, and `]` switches to Worktrees (`app/src/components/ledger/LedgerSurface.tsx`). A worktree session's row there has "Show worktree" (`app/src/components/ledger/SessionsTab.tsx`), unless the session ran on an outpost. The surface itself is [ledger.md](ledger.md)'s.
- The Worktrees list (`app/src/components/ledger/WorktreesTab.tsx`): Active and Removed scopes, the status line's "refresh", and per row Keep or Unpin, "Sessions here", "Go to <session>" for each live session, and "Delete…", which asks for "Delete for real" or, when dirty, "Delete, losing changes".
- ⌘N (`session.new`): the new session dialog's new worktree form, with a generated branch name, "Pick another name", and a start from the default or the current branch (`app/src/components/NewSessionDialog/RepoOptions.tsx`, `app/src/components/NewSessionDialog/worktreeNames.ts`). The dialog is [agent-launch.md](agent-launch.md)'s.
- The home screen's "New worktree session" on a pull request card, driven in [pull-requests.md](pull-requests.md).
- **Settings → Files and locations**: "Reclaim merged worktrees in the background", whose button reads Disable or Enable (`[data-testid="settings-worktree-sweep-toggle"]`, `app/src/components/SettingsModalSections.tsx`), and the Projects Directory.
- Where people see what agents do: the list names the live sessions running in each worktree and the open seeds that point at the worktree, and attn adds a note to a seed's log when attn removes the seed's worktree.
- Their own shell: the `attn worktree` verbs below.

### Agents

- `attn worktree` (`cmd/attnd/worktree.go`): `list` and `log` (with `--repo`, `--limit`, `--json`), `keep`, `unkeep`, `delete` (with `--force`) and `refresh`. None needs a session.
- `attn delegate --new-worktree` creates the worktree a delegate works in (`--branch` with `--from`, or `--existing-branch`, and `--worktree-path`). [delegation.md](delegation.md) covers the verb.
- What agents read: when attn removes a worktree, the daemon adds the note "attn <removed|deleted> the worktree <path> (branch <branch> of <repository>): <reason>." to the log of each seed that worked there (`internal/daemon/worktree_sweep.go`).

### Triggers

- The sweep runs every hour on each tracked repository and refreshes every worktree. A worktree stays if it has any of these: a pin, a live session, an open seed, local changes, stashes, unpushed commits, unmerged commits, or a recent change. The sweep removes every other worktree once the worktree has been idle for 14 days (`internal/daemon/worktree_sweep.go`). To change the hour or the 14 days, set `ATTN_WORKTREE_SWEEP_INTERVAL` or `ATTN_WORKTREE_SWEEP_IDLE_DAYS` in the daemon's environment.
- When a tracked repository's folder is gone, the pass runs no git there. It stops tracking the repository, drops its worktree rows, and logs each row and the repository with ACTION `forgotten` (`internal/daemon/worktree_sweep.go`). Creating a worktree there with attn tracks it again, and the next pass rediscovers the worktrees git still lists with their pins.
- Foreground work in a repository, such as creating or deleting a worktree, preempts an automatic removal there (`internal/daemon/worktree_maintenance.go`).

## Driving it

Preconditions:

- Use the baseline instance. The steps that run `bridge` also need its app. Check that `bridge --launch --wait-ready ping` answers. Run `bridge dismiss_whats_new` to dismiss What's new.
- Start the daemon with `export ATTN_WORKTREE_SWEEP_IDLE_DAYS=0` appended to the env file, so a merged, clean worktree is eligible at once. Without it, the removal steps see "too young for deep inspection".
- Make a repository on its real path, so the paths you type match the ones attn prints: run `W=$(cd "$(mktemp -d /tmp/drive-attn-worktrees.XXXX)" && pwd -P) && echo "export W=$W" >> $DRIVE_ENV`, `git init -q -b main $W/repo` and `git -C $W/repo commit -q --allow-empty -m seed`.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-worktrees-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Save this fixture as `$W/repo/.attn-mock-agent.json` for an agent in that repository to delegate from. The fixture stays untracked in the main checkout, which the sweep never touches:

```json
{"version": 1, "turns": [
  {"includes": "make a worktree", "actions": [
    {"type": "attn", "args": ["delegate", "--agent", "codex", "--model", "default", "--new-worktree", "--branch", "drive-wt", "--from", "main", "--cwd", ".", "--name", "wt-drive", "--brief", "Own a worktree for the drive"]}]}
]}
```

- Start the agent with `wsctl add-session --cwd $W/repo --agent claude --label wt-owner --id wt-owner --place`. `<terminal>` is the agent's terminal id from `wsctl list`.

`<delegated>` below is the delegated worktree's path, as `./attn worktree list --repo $W/repo --json` prints it. `<delegate>` is the delegate's session id and `<seed>` the delegation's seed id.

- **A delegation creates a worktree.** Run `wsctl input --id <terminal> --text "make a worktree" --enter`. `./attn agent peek wt-owner` shows the delegation's result, with `"branch": "drive-wt"`, `"worktree_created": true`, its `directory`, `seed_id` and `session_id`. `wsctl list` shows the new session in that directory. `./attn worktree list --repo $W/repo` prints `$W/repo` as the heading and a table whose BRANCH column holds `drive-wt`. The WORKTREE column shows folder names, not paths.
- **Refresh and read why it is kept.** Run `./attn worktree refresh`. It prints ``refreshing worktree state in the background; `attn worktree list` shows the result``. Re-read `./attn worktree list --repo $W/repo` until the `drive-wt` row's WHY names the delegate session as `... is running in it`, with SWEEP `kept_live_session`.
- **The sweep reclaims a merged worktree.** Run `git -C $W/repo worktree add -q -b drive-merged $W/wt-merged main`, then `./attn worktree refresh`. Re-read `./attn worktree log --repo $W/repo` until it lists `$W/wt-merged` with ACTION `removed` and WHY `merged (ancestor) and clean, idle 0 days`. The folder is gone.
- **Turn the sweep off and on.** Run `./attn settings set worktree_sweep_enabled false`. It prints `worktree_sweep_enabled = false`. Add `git -C $W/repo worktree add -q -b drive-held $W/wt-held main` and run `./attn worktree refresh`: the `drive-held` row's WHY reads `eligible now; the sweep is off (Settings › Files and locations › Worktree sweep)`. Press ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`) and click `[data-testid="settings-nav-desktop"]` natively: `bridge dom_text '{"selector":"[data-testid=\"settings-worktree-sweep-toggle\"]"}'` reads `Enable`. Click it natively: `./attn settings get worktree_sweep_enabled` prints `true`. Close Settings with Escape. The next `./attn worktree refresh` removes `$W/wt-held`.
- **Open the list from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"Open worktrees"}'` and `bridge native_key '{"key":"Enter"}'`. `bridge worktrees_get_state` shows `present: true`, `scope: "Active"`, and a row for `<delegated>` with branch `drive-wt` and its reason.
- **Open the list from the ledger.** Press Escape, then ⌘⇧L: `bridge native_key '{"key":"l","modifiers":["command","shift"]}'`. Run `bridge native_key '{"key":"]"}'`: `bridge worktrees_get_state` shows `present: true` again.
- **Show a session's worktree.** Press `[` to go back to Sessions. `bridge sessions_get_state` lists the `<delegate>` row with `actions` holding "Show worktree". Click `.ledger-row[data-row-key="<delegate>"]` natively and press the digit of "Show worktree" in `actions`, counting from 1. `bridge worktrees_get_state` shows `present: true` with the `<delegated>` row.
- **Sessions here and Go to.** Press `]` for the Worktrees list and click `.ledger-row[data-row-key="<delegated>"] .ledger-row-title` natively. `bridge worktrees_get_state` gives the row's `verbs`, `["Keep","Sessions here","Go to wt-drive","Delete…"]` while the delegate runs. Press `2`: the ledger shows Sessions, and `bridge sessions_get_state` lists only `<delegate>`. Press `]`, click the row again and press `3`: the ledger closes and `bridge get_state`'s `activeLeaf.sessionId` is `<delegate>`. Reopen the list from the palette for the next step.
- **Keep from the app.** Click `.ledger-row[data-row-key="<delegated>"]` natively and press Enter (`bridge native_key '{"key":"Enter"}'`). `bridge worktrees_get_state` shows the row with `pinned: true` and `sweep: "kept"`, and `./attn worktree list --repo $W/repo --json` shows `"pinned": true` for it.
- **Unpin and pin from the shell.** Run `./attn worktree unkeep <delegated>`: it prints `unpinned <delegated>; the next sweep decides again`, and the app's row reads `pinned: false` with Keep as its first verb. `./attn worktree keep <delegated>` prints `pinned <delegated>; the sweep will never reclaim it`.
- **Refresh from the app.** Click `.ledger-status-left .ledger-status-link` ("refresh") natively. Re-read `bridge worktrees_get_state` until no row and no repository shows `refreshing: true`.
- **The removal log in the app.** Click `.ledger-panel .ledger-segmented button:nth-child(2)` ("Removed") natively. `bridge worktrees_get_state` shows `scope: "Removed"` and `log` entries for `$W/wt-merged` and `$W/wt-held` with `action: "removed"`.
- **What holds a worktree.** A pin overrides every other hold on a worktree, so unpin the worktree first. Click `.ledger-panel .ledger-segmented button:nth-child(1)` ("Active") natively to return to the Active list. Run `./attn worktree unkeep <delegated>` and `./attn worktree refresh`, and re-read `./attn worktree list --repo $W/repo` until the `drive-wt` row's SWEEP reads `kept_live_session`. While the delegate runs, the row's chips read `1 live session` and `kept live session`, and the inspector's Sessions lists `wt-drive`. Close the delegate with `wsctl rm-session --id <delegate>` and run `./attn worktree refresh`: the row's WHY reads `an open seed points at it` with SWEEP `kept_open_seed`, and the inspector's Sessions reads `none live here`. A held worktree keeps STATE `not refreshed`: the sweep reads git state deeply only for worktrees nothing holds.
- **Delete from the shell.** Run `echo wip > <delegated>/wip.txt`, then `./attn worktree delete <delegated>`. It exits 1 and the error names modified or untracked files and `--force`. `./attn worktree delete --force <delegated>` prints `deleted <delegated>`. The delegate session is gone from `wsctl list`, `./attn worktree log --repo $W/repo` lists the path with ACTION `deleted` and WHY `at your request`, and `./attn seed notes <seed>` ends with `attn deleted the worktree <delegated> (branch drive-wt of <W>/repo): at your request.`, where `<W>` is the folder's path written out.
- **Delete from the app.** Add `git -C $W/repo worktree add -q -b drive-gone $W/wt-gone main` and `echo wip > $W/wt-gone/wip.txt`, so the sweep keeps it as dirty, then run `./attn worktree refresh` until `./attn worktree list --repo $W/repo` shows a `drive-gone` row with SWEEP `kept_dirty`. In the Active list, click `.ledger-row[data-row-key="<W>/wt-gone"]` natively, where `<W>` is the folder's path written out, and press `3` (`bridge native_key '{"key":"3"}'`), its third verb, "Delete…". `bridge dom_text '{"selector":".ledger-row[data-row-key=\"<W>/wt-gone\"] .ledger-row-trailing .ledger-verb"}'` reads "Delete, losing changes". Press Enter: the row leaves `bridge worktrees_get_state`, the folder is gone, and `./attn worktree log --repo $W/repo` lists it with ACTION `deleted`.
- **A new worktree from the new session dialog.** Press ⌘N, wait for `[data-testid="location-picker-path-input"]` to take focus, press ⌘A (`bridge native_key '{"key":"a","modifiers":["command"]}'`), type `$W/repo` with `bridge native_text`, and press Enter to select the path. Wait for `[data-testid="repo-new-worktree-form"]`. Its `[data-testid="repo-new-worktree-input"]` holds a generated adjective-animal name. `bridge dom_value '{"selector":"[data-testid=\"repo-new-worktree-input\"]"}'` reads it. Click `[aria-label="Pick another name"]` natively, then press ⌃R (`bridge native_key '{"key":"r","modifiers":["control"]}'`): each gives a new name. Tab moves the selection between `[data-testid="repo-new-worktree-start-default"]` and `[data-testid="repo-new-worktree-start-current"]`. The selected one sits in a `label.selected`. Press Enter: a session starts in a new worktree on that branch, and `./attn worktree list --repo $W/repo` lists it. [agent-launch.md](agent-launch.md) drives the dialog itself.
- **Projects Directory.** In **Settings → Files and locations**, scroll `[data-testid="settings-projects-directory-input"]` into view, click it natively, press ⌘A, type `$W` written out and press Enter: `[data-testid="settings-projects-directory-saved"]` appears and `./attn settings get projects_directory` prints it. The field cannot be set back to empty, so set `projects_directory` back to its earlier value from the shell.
- **The hourly sweep.** Append `export ATTN_WORKTREE_SWEEP_INTERVAL=15s` to the env file and restart the daemon as the preconditions do. Add `git -C $W/repo worktree add -q -b drive-tick $W/wt-tick main` and run no refresh: within about 15 seconds `./attn worktree log --repo $W/repo` lists `$W/wt-tick` as `removed`, `merged (ancestor) and clean, idle 0 days`.
- **A tracked repository whose folder is gone.** Run `git init -q -b main $W/lost` and `git -C $W/lost commit -q --allow-empty -m seed`, then create a worktree there with `wsctl request --json '{"cmd":"create_worktree","main_repo":"<W>/lost","branch":"drive-lost","request_id":"c1"}' --event create_worktree_result`, where `<W>` is the folder's path written out, and keep it with `./attn worktree keep $W/lost--drive-lost`. Run `mv $W/lost $W/lost-away` and `./attn worktree refresh`. `./attn worktree list --repo $W/lost` lists nothing, and `./attn worktree log --repo $W/lost` lists `$W/lost` and `$W/lost--drive-lost` with ACTION `forgotten` and WHY `its repository folder is gone; attn tracks the repository again when you create a worktree there`. Another refresh adds no entries. Move the folder back: a refresh still lists nothing. Create a second worktree the same way on branch `drive-back`, refresh, and the list shows both worktrees, `$W/lost--drive-lost` still `pinned`.
- **An existing branch at a chosen path.** A second agent's fixture runs `delegate --agent codex --model default --new-worktree --existing-branch drive-wt --worktree-path <W>/wt-existing --cwd . --name wt-existing --brief ...` once `drive-wt` has no worktree: its result shows `"branch": "drive-wt"`, `"worktree_created": true` and that `directory`. Delete the new worktree with `--force` afterwards.
- **A new worktree from a pull request.** The recipe for the home screen's "New worktree session" is in [pull-requests.md](pull-requests.md). On the baseline it fails before any worktree exists, because the mock GitHub serves no Git.
- **Proof.** In `$P`, keep these:
  - each `./attn worktree` command's output with its exit code
  - `./attn agent peek wt-owner` after the delegation
  - `./attn seed notes <seed>` after the delete
  - `./attn worktree list --repo $W/repo --json` after each refresh, keep and delete
  - `./attn worktree log --repo $W/repo --json` at the end
  - `bridge worktrees_get_state` after each app action
  - `bridge capture_screenshot '{"path":"<P>/worktrees.png"}'` with the list open, where `<P>` is the folder's path written out

  Name each file for its way in, such as `worktrees-palette-state.json`.

### In the native app

The native app (`native/`) has the new session dialog's worktree chooser: the repository's worktrees, the new worktree form with its generated name, and deleting a worktree from the chooser. It has the ledger's Worktrees list with Active and Removed, the query (`repo:` and words), "refresh", and per row Keep or Unpin, "Sessions here", "Go to <session>" and "Delete…", and a session row's "Show worktree". Settings drives only in the Tauri app. `bridge` reaches the native app's own automation actions (`docs/internal/native-app.md`, Automation).

Preconditions:

- Install the instance with `make install INSTANCE=<name> APP=native`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`.
- Launch as [../SKILL.md](../SKILL.md) says for a native instance, then `bridge wait '{"target":{"key":"home"}}'`.
- Make `W` with its repository and `P`, and start the daemon with `ATTN_WORKTREE_SWEEP_IDLE_DAYS=0`, as the Preconditions above say. The chooser's keys start `new-session.repo.`: `row.<n>` for each destination, `form` for the new worktree form, `name.field` for its branch name, `reroll` for "Pick another name" and `start.default` and `start.current` for where it starts.
- The Worktrees list keys a row `ledger.row.<path>` with the path written out, its words `ledger.row.<path>.meta`, its first verb `ledger.row.<path>.verb`, its More button `ledger.row.<path>.more`, and the menu that `.` or More opens, `ledger.row.<path>-menu`, with its items `ledger.row.<path>.menu.<n>`, counting from 0. The open menu holds the keyboard: arrows and Enter or a digit run a verb, and Escape closes it and gives the keyboard back to the list. A repository's heading is `ledger.group.<repository>`, with the repository's path written out, and a removal's row is `ledger.row.<entry id>`.
- The steps press the macOS chords. On Linux press `mod+alt+l` for `mod+shift+l` and `mod+alt+k` for `mod+shift+k`.
- The chords below are macOS's. On Linux, press the ones **Linux** under Contexts names.

Steps:

- **A new worktree from the new session dialog.** Press `bridge key '{"chord":"mod+n"}'`, type `$W/repo` written out with `bridge type` and press `enter`. `bridge wait '{"target":{"key":"new-session.repo.form"}}'` answers, and `bridge tree` shows `new-session.repo.name.field` holding an adjective-animal name. Run `bridge press '{"target":{"key":"new-session.repo.reroll"}}'`, then press `ctrl+r`: each gives a new name. The tree marks `new-session.repo.start.default` `checked`. Press `enter`: after a few seconds a session starts in a new worktree on that branch, `./attn worktree list --repo $W/repo` lists it, and `wsctl list` shows the session in it.
- **Open an existing worktree.** Press `mod+n`, type `$W/repo` and press `enter`, then `down` past `new-session.repo.row.0`, the main checkout, until the tree marks the new worktree's `new-session.repo.row.<n>` `selected`, and press `enter`: a session starts in that worktree, and `./attn worktree list --repo $W/repo` lists no new one. Close that second session with `wsctl rm-session --id <second>`. `<new>` below is the worktree the first step made, as `./attn worktree list --repo $W/repo --json` prints its path, and `<session>` is the first step's session.
- **Open the list from the palette.** Press `mod+shift+k`, type `open worktrees` and press `enter`. `bridge wait '{"target":{"key":"ledger.row.<new>"}}'` answers, where `<new>` is the worktree the first step made. Its row is `selected` under `ledger.group.<W>/repo`, focus is on `ledger.list`, and `ledger.row.<new>.meta` holds its branch, `1 live session` and the sweep's word. Capture `bridge shot '{"path":"<P>/native-worktrees.png"}'`, with `<P>` written out.
- **Keep from the app.** Press `enter`: `bridge wait` until `ledger.row.<new>.verb` is named `Unpin`, the meta holds `kept`, and `./attn worktree list --repo $W/repo --json` shows `"pinned": true` for it. Press `enter` again: the verb is named `Keep`.
- **Sessions here and Go to.** Press `.`: `ledger.row.<new>.menu.1` is named `Sessions here` and `menu.2` `Go to <session>`. Press `2`: the ledger shows Sessions, `ledger.query.field` holds `dir:<new>`, and the only row is the session. Press `]`, then `3` on the worktree's row: the ledger closes and `bridge state '{"of":"window"}'` shows the session on screen.
- **Show a session's worktree.** Press `mod+shift+l`, then `.` on `<session>`'s row: one menu item is named `Show worktree`. Press its digit, counting from 1: the ledger shows Worktrees with `ledger.row.<new>` `selected`.
- **Refresh from the app.** Press `ledger.refresh`: it is named `refresh · asked now`.
- **Delete from the app.** Run `git -C $W/repo worktree add -q -b drive-gone $W/wt-gone main` and `echo wip > $W/wt-gone/wip.txt`, then `./attn worktree refresh` until `./attn worktree list --repo $W/repo` shows `drive-gone` with SWEEP `kept_dirty`. The row `ledger.row.<W>/wt-gone` appears on its own, its meta holding `dirty 1`. Press the row, then `3`, its third verb, `Delete…`: `ledger.row.<W>/wt-gone.verb` is named `Delete, losing changes`. Without the idle-days override the daemon never reads the folder as dirty: the verb is `Delete for real`, and Enter leaves the daemon's refusal, `... contains modified or untracked files, use --force to delete it`, on the row. Press `enter`: the row leaves the list, the folder is gone, and `./attn worktree log --repo $W/repo` lists it with ACTION `deleted`.
- **The removal log.** Press `ledger.scope.removed`: `ledger.row.<entry id>.meta` begins `drive-gone  ·  deleted  ·  at your request`, and `ledger.counts` is named `1 removed`. Press `ledger.scope.active` to go back.
- **Proof.** In `$P`, keep `bridge tree` with the form open and after each new name, and `./attn worktree list --repo $W/repo --json` after each start. Keep `bridge tree` after each step of the Worktrees list. Capture `bridge shot '{"path":"<P>/native-worktree-form.png"}'` with the form open, with `<P>` written out.

## Contexts

- **Durability across restarts**: tracked repositories, pins, verdicts and the removal log are daemon records and come back after any restart. A repository stays tracked after its last worktree is gone, and until its folder is gone.
- **Linux**: `attn worktree` and the sweep work the same where the daemon runs on Linux. `session.new` is Ctrl+Shift+N and `sessions.open` is Ctrl+Alt+L there (`LINUX_SHORTCUTS`).
- **Outposts**: `worktree_sweep_enabled` is a machine setting, so each machine decides for itself. A delegation with `--host` creates its worktree on that outpost, and the outpost checks the folder, refs and worktree path. When you resume a closed outpost session whose worktree was deleted, Reopen can recreate the worktree on that outpost, and offers "Recreate the worktree" (`recreate_worktree_and_reopen`), or "Fetch, recreate, reopen" (`fetch_recreate_and_reopen`) when the branch is only on a remote. For resume, see [ledger.md](ledger.md). The Worktrees list covers only the home's repositories, so it never shows a worktree on an outpost, and an outpost session's ledger row has no "Show worktree".
- **Profiles**: worktrees belong to the machine, not to a profile. The list covers every tracked repository, and a live session in any profile keeps the worktree it runs in.
- **Performance and idle cost**: a pass reads git state for every worktree of every tracked repository. The pass does not inspect a worktree younger than the idle floor deeply. Scenario `worktree-surface` times one refresh over a repository of tens of thousands of files.
- **Harnesses**: a plugin can create and remove worktrees in place of git. See [harnesses.md](harnesses.md) for plugins.
- **Keyboard and native input**: in the list, ↑ and ↓ (or j and k) move, Enter runs the first verb (Keep or Unpin), `.` or → opens the other verbs, 1 to 9 pick the nth verb, `/` filters, `y` copies the path, `[` and `]` switch lists, and Escape backs out. In the new session dialog's new worktree form, Ctrl+R picks another name and Tab switches the starting branch.

## Tests

- Wire: `internal/daemon/worktree_*_wire_test.go`, `internal/daemon/worktree_wire_test.go`, `internal/daemon/plugin_worktree_wire_test.go`, `internal/daemon/delegate_checkout_wire_test.go`, `native/crates/attn-app/tests/wire/ledger/worktrees.rs` (the native app).
- Stack: `cmd/attnd/session_reopen_outpost_stack_test.go`.
- Scenario: `worktree-surface`.
- Kernel: `internal/git/worktree_test.go`.
- App: `app/src/App.worktrees.test.tsx`, `app/src/components/ledger/WorktreesTab.test.tsx`, `app/src/components/NewSessionDialog/worktreeNames.test.ts`.

## Gotchas

- Put `--force` before the path: `attn worktree delete --force <path>`. The CLI help shows the flag after the path, but the command ignores a flag there and still refuses to delete a dirty tree.
- On macOS, `bridge native_key '{"key":"l","modifiers":["command","shift"]}'` can report `handledBy: ["menu"]` while the ledger stays closed ([usage.md](usage.md)). When it does, the ledger-shortcut step is unverified. Open the palette command `sessions` ("Open the sessions list") to continue the row checks, and do not count that as proof of the shortcut.
- `attn worktree refresh` and the list's "refresh" run a whole sweep pass. With the sweep on, they remove what is eligible, though the help says only that refresh updates state.
- Without `ATTN_WORKTREE_SWEEP_IDLE_DAYS=0`, nothing on a fresh instance is old enough: every row that nothing holds says `too young for deep inspection; age floor is 0 of 14 days`. With the variable set, the next refresh removes every merged, clean worktree that nothing holds. Hold any worktree a step needs to keep first: make the worktree dirty, pin it once listed, or keep a session running in it.
- `attn worktree keep` refuses a worktree attn has not listed yet (`worktree not found in registry`). Refresh after `git worktree add` before you pin.
- Only tracked repositories are listed. A `git worktree add` in a repository attn never created a worktree in stays invisible, however often you refresh.
- On macOS, `/tmp` is a link to `/private/tmp`, and attn prints and matches real paths. Build the folder with `pwd -P` as above, or the paths you pass and the paths it prints differ.
- Deleting a worktree closes every session running in it, the delegate included. Delete the delegated worktree last.
- A delegation inside Git needs a complete checkout form, and `--new-worktree --branch` with `--from` is one. From a plain shell instead of an agent, the delegation also needs `--source-session`. Use `--agent codex --model default` for the baseline mock. `--agent shell` is normalized to Codex, and `--model none` is a literal model name, not a request for no model.
- The delegated worktree is held twice, by its live session and by the delegation's open seed. Closing the session alone leaves it kept as `an open seed points at it`. The reason names no seed, because the Worktrees surface shows it to every profile.
- The verbs a row offers depend on its sessions: each live session adds a "Go to <session>" before "Delete…". Count the verbs, or read the row's menu, before pressing a digit.
- No step drives the preemption trigger. The trigger needs an automatic removal held part-way while foreground work starts in the same repository, and only the wire tests' pause point holds a removal there.
- Working but wrong: with no tracked repository, `attn worktree list` says ``no worktrees tracked yet; run `attn worktree refresh` if a repository is missing``, but a refresh never tracks a repository.
