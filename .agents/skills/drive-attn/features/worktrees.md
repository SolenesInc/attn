# Worktrees

The git worktrees agents work in: created for delegations, kept or swept when
the work is done. Once attn creates a worktree in a repository, it tracks
every worktree of that repository. A background sweep reclaims a worktree
whose work has landed and that nothing holds, and says why it keeps each of
the rest. People and agents can pin a worktree so the sweep never takes it,
delete one now, and read what the sweep removed.

## Sub-features

- `worktree-create`: a worktree made by a delegation, the new session dialog or opening a pull request; that repository is tracked from then on.
- `worktree-list`: every worktree of every tracked repository, with its branch, state and why the sweep keeps it.
- `worktree-keep`: pin a worktree so the sweep never reclaims it, and unpin it.
- `worktree-sweep`: the background pass that reclaims merged, clean, idle worktrees nothing holds, on by default.
- `worktree-refresh`: run a pass now instead of on the hourly tick.
- `worktree-delete`: remove a worktree now; a dirty one needs force, and its sessions close with it.
- `worktree-log`: what was removed, by the sweep or by hand, and why; a removal also lands as a note on the seeds that worked there.
- `worktree-names`: the generated branch name the new session dialog offers for a new worktree.

## How to get to it

### People

- Palette (`app/src/application/useAppCommands.tsx`): `worktrees` ("Open worktrees") opens the Worktrees list.
- ⌘⇧L (`sessions.open`, `app/src/shortcuts/registry.ts`) opens the ledger surface on Sessions, and `]` switches to Worktrees (`app/src/components/ledger/LedgerSurface.tsx`). A worktree session's row there has "Show worktree" (`app/src/components/ledger/SessionsTab.tsx`), unless the session ran on an outpost. The surface itself is [ledger.md](ledger.md)'s.
- The Worktrees list (`app/src/components/ledger/WorktreesTab.tsx`): Active and Removed scopes, the status line's "refresh", and per row Keep or Unpin, "Sessions here", "Go to <session>" for each live session, and "Delete…", which asks for "Delete for real" or, when dirty, "Delete, losing changes".
- ⌘N (`session.new`): the new session dialog's new worktree form, with a generated branch name, "Pick another name", and a start from the default or the current branch (`app/src/components/NewSessionDialog/RepoOptions.tsx`, `app/src/components/NewSessionDialog/worktreeNames.ts`). The dialog is [agent-launch.md](agent-launch.md)'s.
- The home screen's "New worktree session" on a pull request card, driven in [pull-requests.md](pull-requests.md).
- Settings, Files and locations: "Reclaim merged worktrees in the background", whose button reads Disable or Enable (`[data-testid="settings-worktree-sweep-toggle"]`, `app/src/components/SettingsModalSections.tsx`), and the Projects Directory.
- Where people see what agents do: the list names the live sessions running in each worktree and the open seeds that point at it, and the seed's log gets a note when its worktree is removed.
- Their own shell: the `attn worktree` verbs below.

### Agents

- `attn worktree` (`cmd/attn/worktree.go`): `list` and `log` (with `--repo`, `--limit`, `--json`), `keep`, `unkeep`, `delete` (with `--force`) and `refresh`. None needs a session.
- `attn delegate --new-worktree` creates the worktree a delegate works in (`--branch` with `--from`, or `--existing-branch`, and `--worktree-path`); delegation owns it.
- What agents read: a removal adds the note "attn <removed|deleted> the worktree <path> (branch <branch> of <repository>): <reason>." to each seed that worked there, on the seed's log (`internal/daemon/worktree_sweep.go`).

### Triggers

- The sweep runs every hour on each tracked repository: it refreshes every worktree, keeps the ones a pin, a live session, an open seed, local changes, stashes, unpushed or unmerged commits or a recent change hold, and removes the rest once idle for 14 days (`internal/daemon/worktree_sweep.go`). `ATTN_WORKTREE_SWEEP_INTERVAL` and `ATTN_WORKTREE_SWEEP_IDLE_DAYS` in the daemon's environment change the hour and the 14 days.
- Foreground work in a repository, such as creating or deleting a worktree, preempts an automatic removal there (`internal/daemon/worktree_maintenance.go`).

## Driving it

Preconditions:

- The baseline instance. The steps that run `bridge` also need its app (`bridge --launch --wait-ready ping` answers) with What's new dismissed (`bridge dismiss_whats_new`).
- The daemon started with `export ATTN_WORKTREE_SWEEP_IDLE_DAYS=0` appended to the env file, so a merged, clean worktree is eligible at once. Without it, the removal steps see "too young for deep inspection".
- A repository on its real path, so the paths you type match the ones attn prints: `W=$(cd "$(mktemp -d /tmp/drive-attn-worktrees.XXXX)" && pwd -P)`, `git init -q -b main $W/repo` and `git -C $W/repo commit -q --allow-empty -m seed`.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-worktrees-proof.XXXX)`.
- An agent in it to delegate from, with this fixture as `$W/repo/.attn-mock-agent.json`. The fixture stays untracked in the main checkout, which the sweep never touches:

```json
{"version": 1, "turns": [
  {"includes": "make a worktree", "actions": [
    {"type": "attn", "args": ["delegate", "--agent", "shell", "--model", "none", "--new-worktree", "--branch", "drive-wt", "--from", "main", "--cwd", ".", "--name", "wt-drive", "--brief", "Own a worktree for the drive"]}]}
]}
```

- Start it with `wsctl add-session --cwd $W/repo --agent claude --label wt-owner --id wt-owner --place`; `<terminal>` is its terminal id from `wsctl list`.

`<delegated>` below is the delegated worktree's path, as `./attn worktree list --repo $W/repo --json` prints it; `<delegate>` is the delegate's session id and `<seed>` the delegation's seed id.

- **A delegation creates a worktree.** Run `wsctl input --id <terminal> --text "make a worktree" --enter`. `./attn agent peek wt-owner` shows the delegation's result, with `"branch": "drive-wt"`, `"worktree_created": true`, its `directory`, `seed_id` and `session_id`. `wsctl list` shows the new session in that directory. `./attn worktree list --repo $W/repo` prints `$W/repo` as the heading and a table whose BRANCH column holds `drive-wt`; the WORKTREE column shows folder names, not paths.
- **Refresh and read why it is kept.** Run `./attn worktree refresh`. It prints ``refreshing worktree state in the background; `attn worktree list` shows the result``. Re-read `./attn worktree list --repo $W/repo` until the `drive-wt` row's WHY names the delegate session as `... is running in it`, with SWEEP `kept_live_session`.
- **The sweep reclaims a merged worktree.** Run `git -C $W/repo worktree add -q -b drive-merged $W/wt-merged main`, then `./attn worktree refresh`. Re-read `./attn worktree log --repo $W/repo` until it lists `$W/wt-merged` with ACTION `removed` and WHY `merged (ancestor) and clean, idle 0 days`; the folder is gone.
- **Turn the sweep off and on.** Run `./attn settings set worktree_sweep_enabled false`; it prints `worktree_sweep_enabled = false`. Add `git -C $W/repo worktree add -q -b drive-held $W/wt-held main` and run `./attn worktree refresh`: the `drive-held` row's WHY reads `eligible now; the sweep is off (Settings › Files and locations › Worktree sweep)`. Press ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`) and click `[data-testid="settings-nav-desktop"]` natively: `bridge dom_text '{"selector":"[data-testid=\"settings-worktree-sweep-toggle\"]"}'` reads `Enable`. Click it natively: `./attn settings get worktree_sweep_enabled` prints `true`. Close Settings with Escape; the next `./attn worktree refresh` removes `$W/wt-held`.
- **Open the list from the palette.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"Open worktrees"}'` and `bridge native_key '{"key":"Enter"}'`. `bridge worktrees_get_state` shows `present: true`, `scope: "Active"`, and a row for `<delegated>` with branch `drive-wt` and its reason.
- **Open the list from the ledger.** Press Escape, then ⌘⇧L: `bridge native_key '{"key":"l","modifiers":["command","shift"]}'`. Run `bridge native_key '{"key":"]"}'`: `bridge worktrees_get_state` shows `present: true` again.
- **Show a session's worktree.** Press `[` to go back to Sessions. `bridge sessions_get_state` lists the `<delegate>` row with `actions` holding "Show worktree". Click `.ledger-row[data-row-key="<delegate>"]` natively and press the digit of "Show worktree" in `actions`, counting from 1. `bridge worktrees_get_state` shows `present: true` with the `<delegated>` row.
- **Keep from the app.** Click `.ledger-row[data-row-key="<delegated>"]` natively and press Enter (`bridge native_key '{"key":"Enter"}'`). `bridge worktrees_get_state` shows the row with `pinned: true` and `sweep: "kept"`, and `./attn worktree list --repo $W/repo --json` shows `"pinned": true` for it.
- **Unpin and pin from the shell.** Run `./attn worktree unkeep <delegated>`: it prints `unpinned <delegated>; the next sweep decides again`, and the app's row reads `pinned: false` with Keep as its first verb. `./attn worktree keep <delegated>` prints `pinned <delegated>; the sweep will never reclaim it`.
- **Refresh from the app.** Click `.ledger-status-left .ledger-status-link` ("refresh") natively. Re-read `bridge worktrees_get_state` until no row and no repository shows `refreshing: true`.
- **The removal log in the app.** Click `.ledger-panel .ledger-segmented button:nth-child(2)` ("Removed") natively. `bridge worktrees_get_state` shows `scope: "Removed"` and `log` entries for `$W/wt-merged` and `$W/wt-held` with `action: "removed"`.
- **Delete from the shell.** Run `echo wip > <delegated>/wip.txt`, then `./attn worktree delete <delegated>`. It exits 1 and the error names modified or untracked files and `--force`. `./attn worktree delete <delegated> --force` prints `deleted <delegated>`. The delegate session is gone from `wsctl list`, `./attn worktree log --repo $W/repo` lists the path with ACTION `deleted` and WHY `at your request`, and `./attn seed notes <seed>` ends with `attn deleted the worktree <delegated> (branch drive-wt of <W>/repo): at your request.`, where `<W>` is the folder's path written out.
- **Delete from the app.** Add `git -C $W/repo worktree add -q -b drive-gone $W/wt-gone main` and `echo wip > $W/wt-gone/wip.txt`, so the sweep keeps it as dirty, then run `./attn worktree refresh` until `./attn worktree list --repo $W/repo` shows a `drive-gone` row with SWEEP `kept_dirty`. In the Active list, click `.ledger-row[data-row-key="<W>/wt-gone"]` natively, where `<W>` is the folder's path written out, and press `3` (`bridge native_key '{"key":"3"}'`), its third verb, "Delete…". `bridge dom_text '{"selector":".ledger-row[data-row-key=\"<W>/wt-gone\"] .ledger-row-trailing .ledger-verb"}'` reads "Delete, losing changes". Press Enter: the row leaves `bridge worktrees_get_state`, the folder is gone, and `./attn worktree log --repo $W/repo` lists it with ACTION `deleted`.
- **A new worktree from the new session dialog.** Press ⌘N, type `$W/repo` with `bridge native_text`, and wait for `[data-testid="repo-new-worktree-form"]`. Its `[data-testid="repo-new-worktree-input"]` holds a generated adjective-animal name. Click the form natively and press Enter: a session starts in a new worktree on that branch, and `./attn worktree list --repo $W/repo` lists it. Driving the dialog itself is [agent-launch.md](agent-launch.md).
- **A new worktree from a pull request.** The home screen's "New worktree session" is driven in [pull-requests.md](pull-requests.md). On the baseline it fails before any worktree exists, because the mock GitHub serves no Git.
- **Proof.** In `$P`, keep each `./attn worktree` command's output with its exit code; `./attn agent peek wt-owner` after the delegation; `./attn seed notes <seed>` after the delete; `./attn worktree list --repo $W/repo --json` after each refresh, keep and delete; `./attn worktree log --repo $W/repo --json` at the end; `bridge worktrees_get_state` after each app action; and `bridge capture_screenshot '{"path":"<P>/worktrees.png"}'` with the list open, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `worktrees-palette-state.json`.

## Contexts

- **Durability across restarts**: tracked repositories, pins, verdicts and the removal log are daemon records and come back after any restart. A repository stays tracked after its last worktree is gone.
- **Linux**: `attn worktree` and the sweep work the same where the daemon runs on Linux. `sessions.open` is Ctrl+Alt+L there (`LINUX_SHORTCUTS`).
- **Outposts**: `worktree_sweep_enabled` is a machine setting, so each machine decides for itself. A delegation with `--host` creates its worktree on that outpost, which checks the folder, refs and worktree path. Resuming a closed outpost session whose worktree was deleted can recreate it on that outpost: Reopen offers "Recreate the worktree" (`recreate_worktree_and_reopen`), or "Fetch, recreate, reopen" (`fetch_recreate_and_reopen`) when the branch is only on a remote; resume is [ledger.md](ledger.md)'s. The Worktrees list covers only the home's repositories, so it never shows a worktree on an outpost, and an outpost session's ledger row has no "Show worktree".
- **Profiles**: worktrees belong to the machine, not to a profile. The list covers every tracked repository, and a live session in any profile keeps the worktree it runs in.
- **Performance and idle cost**: a pass reads git state for every worktree of every tracked repository; a worktree younger than the idle floor is not inspected deeply. Scenario `worktree-surface` times one refresh over a repository of tens of thousands of files.
- **Harnesses**: a plugin can create and remove worktrees in place of git; [harnesses.md](harnesses.md) covers plugins.
- **Keyboard and native input**: in the list, ↑ and ↓ (or j and k) move, Enter runs the first verb (Keep or Unpin), `.` or → opens the other verbs, 1 to 9 pick the nth verb, `/` filters, `y` copies the path, `[` and `]` switch lists, and Escape backs out. In the new session dialog's new worktree form, Ctrl+R picks another name and Tab switches the starting branch.

## Tests

- Wire: `internal/daemon/worktree_*_wire_test.go`, `internal/daemon/worktree_wire_test.go`, `internal/daemon/plugin_worktree_wire_test.go`, `internal/daemon/delegate_checkout_wire_test.go`.
- Stack: `cmd/attn/session_reopen_outpost_stack_test.go`.
- Scenario: `worktree-surface`.
- Kernel: `internal/git/worktree_test.go`.
- App: `app/src/App.worktrees.test.tsx`, `app/src/components/ledger/WorktreesTab.test.tsx`, `app/src/components/NewSessionDialog/worktreeNames.test.ts`.

## Gotchas

- `attn worktree refresh` and the list's "refresh" run a whole sweep pass. With the sweep on, they remove what is eligible, though the help says only that refresh updates state.
- Without `ATTN_WORKTREE_SWEEP_IDLE_DAYS=0`, nothing on a fresh instance is old enough: every row that nothing holds says `too young for deep inspection; age floor is 0 of 14 days`. With it, every merged, clean worktree that nothing holds is removed on the next refresh, so a worktree a step needs to keep must be held first: dirty, pinned once listed, or in use.
- `attn worktree keep` refuses a worktree attn has not listed yet (`worktree not found in registry`). Refresh after `git worktree add` before you pin.
- Only tracked repositories are listed. A `git worktree add` in a repository attn never created a worktree in stays invisible, however often you refresh.
- On macOS, `/tmp` is a link to `/private/tmp`, and attn prints and matches real paths. Build the folder with `pwd -P` as above, or the paths you pass and the paths it prints differ.
- Deleting a worktree closes every session running in it, the delegate included. Delete the delegated worktree last.
- A delegation inside Git needs a complete checkout form; `--new-worktree --branch` with `--from` is one. Run from a plain shell instead of an agent, it also needs `--source-session`. The delegated session is a shell: `--agent shell --model none` needs no model.
- The delegated worktree is held twice, by its live session and by the delegation's open seed. Closing the session alone leaves it kept as `open seed <id> points at it`.
- The verbs a row offers depend on its sessions: each live session adds a "Go to <session>" before "Delete…". Count the verbs, or read the row's menu, before pressing a digit.
- Working but wrong: with no tracked repository, `attn worktree list` says ``no worktrees tracked yet; run `attn worktree refresh` if a repository is missing``, but a refresh never tracks a repository.
