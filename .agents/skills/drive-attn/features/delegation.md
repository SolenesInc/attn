# Delegation

One agent starts another on a seed, from any harness to any harness, as a full
session the user can open and steer. Every delegation has a seed: a brief
plants one, or the dispatcher names an existing one. The delegate tends that
seed as its stake, reads it when it starts, and reports there. A role picks
the instructions, harness, model and effort for one type of work; the user
keeps the roles, and agents choose among them. A handover gives a seed and its
claim to a fresh agent while the previous one keeps running.

## Sub-features

- `delegate-brief`: start a delegate on new work; the brief becomes its seed, part of the dispatcher's stake when it has one.
- `delegate-seed`: start a delegate on an existing seed nobody active tends.
- `handover`: a new session takes over a seed and its claim, with a note; the previous agent keeps running.
- `roles`: the user's roles, their alternatives and the fallback, kept in revisions with history and rollback.
- `workflow-skill`: "Add Attn roles" or `attn delegate roles install` adds attn's own roles and installs the attn-workflow skill they use beside the attn skill. While workflow skills are enabled, daemon startup, executable-setting saves, delegation-preference saves and role launches refresh them. Preference saves include the app, CLI commits and rollback; settings reads leave skill files alone.
- `delegate-placement`: the delegate opens beside the dispatcher, or on a named desktop, without changing what the user sees.
- `delegate-checkout`: the machine, folder and Git checkout the delegate works in.
- `delegation-status`: a stable request id to check or retry a delegation whose answer was lost.
- `delegation-chain`: the dispatcher, peers and delegates of a session, as a card the keyboard walks.
- `jump-to-dispatcher`: go from a delegate to the session that started it.
- `seed-continue`: Resume and Handover on a seed's page.
- `delegation-guidance`: what attn tells dispatchers and delegates, including the plan sign-off before dispatch and independent design candidates.

## How to get to it

### People

- ⌘⇧↑ (`session.orchestrator`, `app/src/shortcuts/registry.ts`) shows the shown agent's dispatcher.
- Palette (`app/src/application/useAppCommands.tsx`): `show-delegation-chain` ("Show delegation chain"), offered while an agent is active.
- Moving an agent with its delegates, from the palette or by dragging its sidebar row with ⌥ held, is [desktops.md](desktops.md)'s.
- The chain trigger on the agent's tile header, which names the role, opens the delegation chain card. On a sidebar row the trigger shows at rest, and pointing at the row opens the card; while the pointer is over the row, the row's actions cover the trigger (`app/src/components/DelegationChain.tsx`). In the card, ↑ ↓ Home End walk the chain, Enter opens an agent, Escape closes it.
- A seed's page in the garden: "Resume" and "Handover", with the handover form that asks "What should the new agent know?", the working folder, the Git checkout and the agent (`app/src/components/GardenPanel.tsx`, `app/src/components/DelegationCheckoutFields.tsx`).
- Settings, Delegation: the roles table, its switch, "Add Attn roles", which also installs the attn-workflow skill for each harness that has a skills folder, alternatives and "Anything else", the fallback (`app/src/components/DelegationSettings.tsx`).
- Where people see and steer what agents do: the delegate is a full session in a tile beside its dispatcher, with its role on the tile header and the seed it tends on its seed chip. People type into it like any agent.
- Their own shell: `attn delegate --source-session <id>` and `attn delegate roles`, below.

### Agents

- `attn delegate` (`cmd/attn/main.go`): `--brief`, `--brief-file` or `--seed`; `--handover` with `-m`; `--cwd` with `--reuse-checkout`, `--new-worktree`, `--branch`, `--existing-branch`, `--from`, `--worktree-path` and `--allow-worktree-reuse`; `--host`; `--role`, `--choice`, `--fallback`, `--agent`, `--model`, `--provider`, `--effort`; `--name`, `--desktop`, `--priority`, `--yolo`, `--request-id`, `--source-session`, `--profile`.
- `attn delegate status <request-or-operation-id>` (`cmd/attn/main.go`).
- `attn delegate roles` (`cmd/attn/delegation_roles.go`): with no command, the catalog agents choose from; `show`, `history`, `install`, `add`, `set`, `copy`, `rm`, `enable`, `disable`, `apply`, `rollback`.
- `attn session list --tree` nests live sessions under their dispatchers (`cmd/attn/session_ledger.go`).
- `attn seed handover` is gone: it answers `seed: unknown command "handover"`. `attn delegate --seed <id> --handover --cwd <path>` replaces it.
- Prompts: the delegation boundary in every session's instructions (`internal/prompts/content/delegation-boundary.md`); the delegate's opening, which names its seed and that seed's checks, its plot's shared ones included, or says it has none (`internal/prompts/content/delegation/identity.md`, `opening.md`, `seed.md`, `seed-checks.md`, `seed-no-checks.md`); the role's execution guidance and the catalog text (`internal/prompts/content/delegation-preferences/`); the attn skill's delegation references (`internal/prompts/content/skills/attn/references/delegation.md`, `delegated-agent.md`, `delegation-roles.md`); the attn-workflow skill, which tells a Pathfinder how to hand off a plan and when to run prototypes or independent candidates, and an Orchestrator how to delegate Builders. Its Delivery reference covers both repository delivery flows (`internal/prompts/content/skills/attn-workflow/SKILL.md`, `references/planning.md`, `references/prototype.md`, `references/orchestration.md`, `references/delivery.md`). Without a daemon, `attn prompts show attn-workflow-skill` lists the skill and its references and `attn prompts render attn-workflow-skill planning` prints one ([agent-guidance.md](agent-guidance.md)).

## Driving it

Preconditions:

- The baseline instance. The steps that run `bridge` also need its app (`bridge --launch --wait-ready ping` answers) with What's new dismissed (`bridge dismiss_whats_new`); the rest need only the daemon.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-delegation.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- A delegate folder, `D=$(mktemp -d /tmp/drive-attn-delegate.XXXX) && echo "export D=$D" >> $DRIVE_ENV`, outside Git, holding this fixture as `$D/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "Your assignment", "actions": [
    {"type": "capture", "name": "seed", "pattern": "seed `(s-[a-z0-9]+)`"},
    {"type": "attn", "args": ["seed", "note", "{{seed}}", "-m", "read my assignment"]}]}
]}
```

- A dispatcher folder, `A=$(mktemp -d /tmp/drive-attn-dispatcher.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`. Each turn takes the delegate folder from the prompt:

```json
{"version": 1, "turns": [
  {"includes": "handover", "actions": [
    {"type": "capture", "name": "seed", "pattern": "(s-[a-z0-9]+)"},
    {"type": "capture", "name": "cwd", "pattern": "handover s-[a-z0-9]+ (\\S+)"},
    {"type": "attn", "args": ["delegate", "--seed", "{{seed}}", "--handover", "-m", "Carry on from the notes.", "--agent", "codex", "--model", "mock-agent-1", "--cwd", "{{cwd}}"]}]},
  {"includes": "place", "actions": [
    {"type": "capture", "name": "cwd", "pattern": "place (\\S+)"},
    {"type": "attn", "args": ["delegate", "--agent", "codex", "--model", "mock-agent-1", "--brief", "Placed drive: note the seed and wait.", "--cwd", "{{cwd}}", "--name", "drive-placed", "--desktop", "7"]}]},
  {"includes": "delegate", "actions": [
    {"type": "capture", "name": "cwd", "pattern": "delegate (\\S+)"},
    {"type": "attn", "args": ["delegate", "--role", "drive", "--brief", "Delegation drive: note the seed and wait.", "--cwd", "{{cwd}}", "--name", "drive-delegate", "--request-id", "drive-delegate-1"]}]}
]}
```

`<terminal>` is the dispatcher's terminal id from `wsctl list`. `<delegate>`, `<seed>`, `<placed>` and `<successor>` are the `session_id` and `seed_id` values the dispatcher's replies print.

- **Roles from the shell.** Run `./attn delegate roles`. On the baseline it prints `No delegation roles available.` and points at Settings > Delegation. Run `./attn delegate roles add drive --name "Drive" --agent codex --model mock-agent-1 -m "drive-attn"`, then `./attn delegate roles enable`. Each prints `revision <n>`, the change, and `undo: attn delegate roles rollback`. `./attn delegate roles` now prints `## Drive [drive]` with its `Default: Default [default]` choice, and `./attn delegate roles history` lists both revisions, newest first.
- **The other role commands.** Run `./attn delegate roles copy drive drive2 --name "Drive two"`, `./attn delegate roles set drive2 --description "second"`, `./attn delegate roles add drive2/fast --when "speed matters" --model mock-agent-1`, then `./attn delegate roles show drive2`: it prints `Drive two (drive2)`, the description and `alternative drive2/fast "fast"` with `when: speed matters`. `./attn delegate roles disable drive2` makes `./attn delegate roles show` mark the row `off` and count `1 of 2 roles offered to agents`; `enable drive2` turns it back on. `./attn delegate roles rm drive2/fast` and `rm drive2` remove them, and `./attn delegate roles rollback` prints `revision <n> restores revision <n-2>` and brings `drive2` back; remove it again. Export with `./attn delegate roles show --json > $P/roles.json` and run `./attn delegate roles apply $P/roles.json`: it prints a revision with `no changes`. An export older than the live table is refused with `the table changed after revision <n>; export it again`. Finish with `./attn delegate roles set --fallback --agent codex --model mock-agent-1`: `fallback: no model chosen → codex mock-agent-1`.
- **Roles in Settings.** Press ⌘, (`ui.openSettings`) with `bridge native_key '{"key":",","modifiers":["command"]}'`, then click `[data-testid="settings-nav-delegation"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-section-delegation\"]"}'` names Drive, and `[role="switch"][aria-label="Delegation preferences"]` has `aria-checked="true"`. Bring each control below into view with `bridge dom_scroll_into_view` before reading its bounds, then click it natively:
  - `[aria-label="Drive details"]` opens the row; `.delegation-alt.add button` ("+ Alternative model") adds a row labeled "Alternative", and `./attn delegate roles show` lists it under `drive`. Remove it with `./attn delegate roles rm drive/<choice-id>`.
  - The switch: `aria-checked="false"`, `./attn delegate roles` prints `No delegation roles available.` and `show` reads `delegation roles off`. Click it again to turn it back on.
  - `[aria-label="Anything else details"]` opens the fallback's row with its `#fb-ins` instructions field.
  - "Add Attn roles" (`.delegation-addrow .settings-action:nth-child(2)`): on a named instance the section shows `attn-workflow installation is disabled for instance "<name>"; saved preferences were not changed`.
  Close Settings with Escape.
- **An agent delegates with a role.** Run `wsctl add-session --cwd $A --agent claude --label dispatcher --id dispatcher --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "delegate $D" --enter`. Re-read `./attn agent peek dispatcher` until it shows the result, with a deadline of two minutes (`attn delegate` returns once the delegate's first turn starts): `"seed_id"`, `"session_id"`, `"agent": "codex"` and a `"desktop_id"`. `wsctl list` shows the delegate's tile on the dispatcher's desktop, and `./attn agent peek <delegate>` shows `noted on <seed>`. `./attn seed show <seed>` shows a `status` line reading `growing` and a `tender` line naming the delegate, and `./attn seed notes <seed>` lists `read my assignment`.
- **Check the request.** Run `./attn delegate status drive-delegate-1`. It prints the operation as JSON with `"state": "completed"` and the same `session_id`.
- **The chain from the shell.** Run `./attn session list --tree`. Under the profile's header and `SESSION  AGENT  STATE  TURN  SEED`, each row starts with the session id cut to 8 characters. `drive-delegate` sits on a `└─` or `├─` row below `dispatcher`, with its seed's slug in the last column.
- **The role on the tile header.** Select the delegate for setup with `bridge select_session '{"sessionId":"<delegate>"}'`. `bridge dom_text '{"selector":".delegation-chain-trigger--header[data-delegation-session=\"<delegate>\"]"}'` reads `DDrive`: the role's initial, then its name. Click it natively: `bridge dom_wait '{"selector":"[data-testid=\"delegation-chain-popover\"] [data-chain-session=\"<delegate>\"]","focused":true,"timeoutMs":5000}'` matches. Home moves focus to `dispatcher`'s entry, End back to `<delegate>`, ↑ to `dispatcher` again. Escape closes the card.
- **The chain from a sidebar row.** Move the pointer natively over `[data-testid="sidebar-session-dispatcher"]` (`native_mouse` `move`). `bridge dom_wait '{"selector":"[data-testid=\"delegation-chain-popover\"] [data-chain-session=\"dispatcher\"]","timeoutMs":5000}'` matches and `dom_text` on the popover names both agents. Move the pointer onto a tile to close it.
- **Jump to the dispatcher.** With the delegate shown, press ⌘⇧↑: `bridge native_key '{"key":"ArrowUp","modifiers":["command","shift"]}'`. `bridge get_state` shows `view: "session"` and `activeLeaf.sessionId: "dispatcher"`.
- **The chain from the palette.** Open the palette as the README says, type `delegation chain` and press Enter on "Show delegation chain". `bridge dom_wait '{"selector":"[data-testid=\"delegation-chain-popover\"] [data-chain-session=\"dispatcher\"]","focused":true,"timeoutMs":5000}'` matches. Run `bridge native_key '{"key":"ArrowDown"}'` and `bridge native_key '{"key":"Enter"}'`: the card closes and `bridge get_state` shows `activeLeaf.sessionId: "<delegate>"`.
- **Place a delegate on another desktop.** Select the dispatcher for setup with `bridge select_session '{"sessionId":"dispatcher"}'`, then run `wsctl input --id <terminal> --text "place $D" --enter`. Re-read `./attn agent peek dispatcher` until its newest result shows a `"desktop_id"` that ends in `desktop_7`. `bridge get_state` still shows `activeLeaf.sessionId: "dispatcher"` on the same current desktop, and `wsctl list` shows desktop 7 holding `<placed>`.
- **Hand over from an agent.** Run `wsctl input --id <terminal> --text "handover <seed> $D" --enter`. Re-read peek until it shows a new `"session_id"` (`<successor>`) and `"predecessor_session_id": "<delegate>"`. `./attn seed show <seed>` names `<successor>` as tender, and `./attn seed notes <seed>` lists the handover note. `./attn agent list` still lists `<delegate>`: a handover leaves the previous agent running.
- **Resume from the seed's page.** Press ⌘⇧T to open the garden, then click `[data-seed-row="<seed>"]` natively. With `<successor>` live, the page shows `[data-testid="seed-resume-<seed>"]`; click it natively. `bridge get_state` shows `activeLeaf.sessionId: "<successor>"`.
- **Hand over from the seed's page.** On the same page, click `[data-testid="seed-handover-<seed>"]` natively. The note field takes focus, and the working folder and agent come filled in from the current tender: `codex`, and `$D` as its real path (`/private/tmp/...` on macOS). Type a note with `bridge native_text '{"text":"Pick this up from the page."}'` and click `.garden-handover button[type="submit"]` natively. The form shows "Handing over…" and closes, and the app selects the new agent. `./attn seed show <seed>` names it as tender, and `wsctl list` shows its tile.
- **A person delegates from their shell.** Run `./attn delegate --source-session dispatcher --role drive --brief "From the person's shell." --cwd $D --name f-shell`. It prints the request and acceptance lines, then the result with `"role": "Drive"`, and `./attn session list --tree` shows `f-shell` under `dispatcher`.
- **Every flag from an agent.** Make a Git folder `G=$(mktemp -d /tmp/drive-attn-flagrepo.XXXX)` with `git -C $G init -q -b main`, one empty commit and a branch `audit-b2`, append `G` to the env file, write a brief to `$G.brief.md`, and plant a seed with one check from the shell: `./attn seed plant "flag seed" -m "..."`, then `./attn seed check add <flag-seed> -m "The delegate notes the seed."`. Give `$G` a fixture with one turn per word, each running one `attn delegate` (a refused action ends the turn), and start `wsctl add-session --cwd $G --agent claude --label flagger --id flagger`. Type each word with `wsctl input` and re-read `./attn agent peek flagger` until its state leaves `working`:
  - `--brief-file <file> --fallback --cwd <D>`: `"role": ""`, a new seed.
  - `--role drive --choice default --priority --yolo --profile Default --brief ... --cwd <D>`: `"role": "Drive"`.
  - `--seed <flag-seed> --agent codex --model mock-agent-1 --cwd <D>`: that `seed_id`; the delegate's opening says `Report C1 with \`attn seed check report\``, where a seed with no checks says `The seed has no checks`. Read the opening in the newest transcript under `<D>/.attn-mock-agent/`.
  - `--effort high`: `"effort": "high"`. `--provider x` without a role: refused, `--provider requires --role or --fallback`.
  - In `<G>`: `--new-worktree --branch audit-b1 --from HEAD --worktree-path <G>-wt1` gives `"checkout": "created"` at that path; `--reuse-checkout --branch main` is refused naming the active session `flagger` and `--allow-worktree-reuse`, and passes with it; `--new-worktree --existing-branch audit-b2` gives `"worktree_created": true`.
  - `--host nowhere`: refused, `outpost "nowhere" does not exist`. `--host home` starts the delegate here.
- **What a delegate is told.** `./attn prompts render session launch` includes `Attn delegation starts a separate agent session`. `./attn skill --list` names `delegated-agent`, `delegation-roles` and `delegation`, and `./attn skill --reference delegation` prints `# Delegation`. `./attn prompts render attn-workflow-skill planning` prints `# Planning`; the `delivery` reference covers pull requests and trunk delivery.
- **Retired flags.** Run `./attn delegate --ticket x --source-session dispatcher --model mock-agent-1 --cwd $D`. It exits 2 with `--ticket retired:` and names `attn seed plant` and `attn delegate --seed <seed-id> --cwd <path>`. `--plot x`, `--worktree x`, `--repo x`, `--preferences-revision 1`, `--confirm` and `--no-worktree` each answer `<flag> retired:` with their replacement. `./attn seed handover <seed>` answers `seed: unknown command "handover"`.
- **Proof.** In `$P`, keep each `./attn` command's output with its exit code; `./attn agent peek dispatcher` after each delegation; `./attn seed show <seed> --json` after the first delegation and after each handover; `./attn session list --tree`; `wsctl list` before and after the placement and the move; `bridge get_state` after the jump and after Enter in the card; `bridge capture_screenshot '{"path":"<P>/delegation-chain.png"}'` with the card open, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `delegation-palette-chain.png`.

## Contexts

- **Durability across restarts**: a delegation operation, its request id and its seed are the daemon's records; `attn delegate status` reads them after a restart, and a retry with the same `--request-id` returns the accepted operation instead of starting another. A delegation that a restart interrupts finishes on the same session once the daemon is back. Delegates come back like any session; an embedded-backend delegate stops with the daemon.
- **Linux**: `attn delegate` works the same where the daemon and CLI run on Linux. `session.orchestrator` is Ctrl+Alt+O there (`LINUX_SHORTCUTS`).
- **Outposts**: a delegation starts on the dispatcher's machine unless `--host` names an outpost, or `home`. Paths are checked on the target machine. An unknown or unreachable host refuses before acceptance; after a home restart, an accepted operation waits for its outpost, and its status names the host. Checkout sharing is judged per machine.
- **Profiles**: the delegate, its seed and its desktop belong to the dispatcher's profile. `--desktop` takes only desktops of that profile. Outside attn, `--profile` picks the profile when there are several.
- **Performance and idle cost**: an open delegation chain card must not repaint while idle; the `delegation-chain` scenario samples the app with it open.
- **Harnesses**: any harness delegates to any harness. `--agent` picks the delegate's harness, `--provider` a plugin harness's model provider, and a role's choice pins harness, model and effort. Without a role, `--model` is required; `default` asks for the harness's own.
- **Keyboard and native input**: ⌘⇧↑, the palette and the card's arrows, Home, End, Enter and Escape reach the chain. A placed delegate does not take the keyboard: the dispatcher keeps focus.
- **Compatibility across versions**: retired flags answer with their replacement: `--ticket`, `--confirm`, `--plot`, `--worktree`, `--repo`, `--no-worktree`, `--preferences-revision` and the two flags that once chose a desktop. A delegate keeps the role it launched with when the role is later changed or deleted.
- **Flow**: `--priority` puts the delegate's owed turns first in queue flow. The chain trigger shows on queue sidebar rows and desktop sidebar rows alike.

## Tests

- Wire: `internal/daemon/delegate*_wire_test.go`, `internal/daemon/delegation*_wire_test.go`, `internal/daemon/session_delegation_roles_wire_test.go`, `internal/daemon/seed_handover_wire_test.go`, `internal/daemon/garden_profile_delegation_delete_wire_test.go`.
- Stack: `cmd/attn/delegate_stack_test.go`, `cmd/attn/delegate_restart_stack_test.go`, `cmd/attn/delegate_outpost_stack_test.go`, `cmd/attn/session_tree_stack_test.go`, `app/e2e/delegation-chain.spec.ts`, `scripts/verify-delegation-placement.sh`.
- Scenario: `delegation-chain`, `delegation-preferences`.
- Kernel: `internal/prompts/delegation_preferences_test.go`.
- App: `app/src/App.delegationChain.test.tsx`, `app/src/App.delegationSettings.test.tsx`, `app/src/components/DelegationSettings.autosave.test.tsx`, `app/src/components/DelegationSettings.editing.test.tsx`.

## Gotchas

- `bridge capture_screenshot` draws the page without the chain card: read the card with `dom_text` on `[data-testid="delegation-chain-popover"]`.
- A native click below the Settings dialog's visible area lands outside the dialog and closes Settings. Scroll the control into view first.
- `attn delegate status` with an id no request or operation has, such as one the daemon refused before accepting it, answers `sql: no rows in result set` (seed `name-missing-request-attn-delegate-status`, s-9fw162).
- The bridge leaves the app in the background. A card can own `document.activeElement` while CSS `:focus` matches nothing. Use `dom_wait` with `"focused":true` to check the keyboard target.
- A live macOS drive found that a native click reached `[data-testid="settings-nav-delegation"]` (the trusted pointer receipt matched) but left `settings_get_state.activeSection` on `connectivity`. The native click passed after an app relaunch. Keep the failed attempt in the proof; setup-only `settings_select_section` does not verify the native path.
- Without `--role` or `--fallback`, `attn delegate` exits 2 with `--model is required`. Every delegation needs `--cwd`; a relative one starts at the caller's directory, which for the mock agent is its session folder.
- From a plain shell there is no source session. Pass `--source-session <id>`, or `--agent` with `--model`, or the daemon refuses with `source inheritance is unavailable`.
- A seed that an active session tends is refused without `--handover`, naming the tender. Delegating the dispatcher's own stake also needs `--handover`.
- `attn delegate` returns once the delegate's first turn starts, or after 90 seconds with a `first_turn_unconfirmed` line on stderr. A delegate whose fixture matches nothing in its opening still starts, but writes no note; match on `Your assignment`, which every delegate's opening carries.
- Inside Git, a delegation needs one complete checkout form, and an active session in the same checkout refuses it until `--allow-worktree-reuse`. Keep the delegate folder outside Git to drive the rest.
- `--desktop 7` creates a missing numbered desktop. Rerunning the recipe on the same instance finds it already there; that is not a bug.
- The seed page fills the handover's folder and agent from a live tender. With the tender gone or its folder removed, the form asks for a working folder and an agent ("required without a source session"), and Resume shows only when the closed session's conversation can come back. On a seed a crew member claims, or one whose last session was a member's, Resume wakes or focuses the member instead of reopening that session, and says the member is retired when it is. That holds for work assigned with `attn seed tend <id> --for <name>` from a session or by the user: the page offers Resume before the member has a session, and after the member tends and parks the seed, Resume goes back to the member, never to the session that assigned it. Without the app, `wsctl seed-continuation --seed <seed>` prints what the page offers and `wsctl seed-resume --seed <seed>` presses Resume. The mock writes a resumable conversation only with `"resumable": true` in its fixture, and then under the harness home, which is your real `~/.claude` or `~/.codex` unless `ATTN_TOOL_HOME` named a scratch folder when the daemon started. Drive Resume of a closed session only with that folder set, never against the real home.
- A reused `--request-id` returns the first operation with that id. Pick a new one per run, or expect the old result.
- `attn delegate roles add <role> --builtin ...` needs "Add Attn roles" or `attn delegate roles install` once; custom roles need `--name`. On a named instance both are refused with `attn-workflow installation is disabled for instance "<name>"; saved preferences were not changed`, because a named instance never writes skills into your harness homes ([agent-guidance.md](agent-guidance.md)). Drive roles there with `--name`, as above.
- One `attn delegate` runs one choice of a role. For a design choice that is costly to reverse, the agent presenting the options may propose independent candidates: one Prototyper delegation per distinct model among the role's choices that fit, each `attn delegate --role prototyper --choice <id>`, with the default counted among them. Several Prototyper delegations from one dispatcher on one task are that, not a bug. When the fitting choices share one model it starts one and says so. Working but wrong: candidates started before the user agreed to them, or a delegated Prototyper starting candidates of its own.
- A Pathfinder presents the plan and waits for the user's choice even when the request included execution. It dispatches without waiting only when the user said, before the plan existed, to dispatch without reviewing it, and then records its understanding and the user's instruction as notes on the plan seed and names that seed in its reply. Working but wrong: dispatching after a request that merely included execution, such as "just do it", or skipping the review with no note on the plan seed.
- Working but wrong: `attn delegate roles` lists nothing while the table is turned off, even with roles saved; `show` still lists them. The role's name shows on the delegate's tile header, but a role deleted later stays on delegates that launched with it.
