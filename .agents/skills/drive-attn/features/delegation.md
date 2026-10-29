# Delegation

One agent starts another on a seed, from any harness to any harness, as a full
session the user can open and steer. Every delegation has a seed: a brief
plants one, or the dispatcher names an existing one. The delegate tends that
seed as its stake, reads it when it starts, and reports there. A role picks
the instructions, harness, model and effort for one type of work; the user
keeps the roles, and agents choose among them. A handover gives a seed and its
claim to a fresh agent while the previous one keeps running.

## Actions and ways in

### Delegate or hand over

An agent or a person's shell uses `attn delegate` (`cmd/attn/main.go`): `--brief`, `--brief-file` or `--seed` chooses new work or an existing seed nobody active tends; a brief becomes a child of the dispatcher's stake when it has one. `--handover` with `-m` transfers a seed and its claim while the prior agent keeps running. The command takes `--cwd`, `--reuse-checkout`, `--new-worktree`, `--branch`, `--existing-branch`, `--from`, `--worktree-path`, `--allow-worktree-reuse`, `--host`, `--role`, `--choice`, `--fallback`, `--agent`, `--model`, `--provider`, `--effort`, `--name`, `--desktop`, `--priority`, `--yolo`, `--request-id`, `--source-session` and `--profile`. The delegate opens beside the dispatcher or on a named desktop without changing what the user sees. `attn delegate status <request-or-operation-id>` checks a delegation whose answer was lost; `--request-id` is its stable retry key. A person's shell supplies `--source-session <id>`.

A seed's garden page has "Resume" and "Handover", with a form for "What should the new agent know?", working folder, Git checkout and agent (`app/src/components/GardenPanel.tsx`, `app/src/components/DelegationCheckoutFields.tsx`). `attn seed handover` is retired and names `attn delegate --seed <id> --handover --cwd <path>`; garden lists it. The delegate is a full session beside its dispatcher, with role on its tile header and stake on its seed chip; people can type into it.

### Choose roles

Settings, Delegation, has the roles table and switch, alternatives, "Anything else" fallback and "Add Attn roles", which also installs attn-workflow for each harness with a skills folder (`app/src/components/DelegationSettings.tsx`). `attn delegate roles` (`cmd/attn/delegation_roles.go`) lists the catalog; `show`, `history`, `add`, `set`, `copy`, `rm`, `enable`, `disable`, `apply` and `rollback` manage revisions and history.

### Follow the chain

⌘⇧↑ (`session.orchestrator`, `app/src/shortcuts/registry.ts`) shows the displayed agent's dispatcher. Palette `show-delegation-chain` ("Show delegation chain", `app/src/application/useAppCommands.tsx`) is offered while an agent is active. The sidebar row and tile-header chain triggers name the role and open the chain card; hovering the sidebar label also opens it (`app/src/components/DelegationChain.tsx`). ↑ ↓ Home End walk it, Enter opens an agent and Escape closes it. `attn session list --tree` nests live sessions under dispatchers (`cmd/attn/session_ledger.go`). Moving an agent with its delegates is owned by [desktops.md](desktops.md).

### Guidance

Every session gets the delegation boundary (`internal/prompts/content/delegation-boundary.md`); a delegate's opening names its seed (`internal/prompts/content/delegation/identity.md`, `opening.md`, `seed.md`). Role execution guidance and catalog text are in `internal/prompts/content/delegation-preferences/`; attn's references are `internal/prompts/content/skills/attn/references/delegation.md`, `delegated-agent.md` and `delegation-roles.md`. The attn-workflow skill covers Pathfinder plan sign-off before dispatch, handoff, prototypes and independent candidates, and Orchestrator delegation to Builders (`internal/prompts/content/skills/attn-workflow/SKILL.md`, `references/planning.md`, `references/prototype.md`, `references/orchestration.md`). Without a daemon, `attn prompts show attn-workflow-skill` lists it and `attn prompts render attn-workflow-skill planning` prints a reference ([agent-guidance.md](agent-guidance.md)).

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
- **Roles in Settings.** Press ⌘, (`ui.openSettings`) with `bridge native_key '{"key":",","modifiers":["command"]}'`, then click `[data-testid="settings-nav-delegation"]` natively. `bridge dom_text '{"selector":"[data-testid=\"settings-section-delegation\"]"}'` names Drive, and `[role="switch"][aria-label="Delegation preferences"]` has `aria-checked="true"`. Close Settings with Escape.
- **An agent delegates with a role.** Run `wsctl add-session --cwd $A --agent claude --label dispatcher --id dispatcher --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "delegate $D" --enter`. Re-read `./attn agent peek dispatcher` until it shows the result, with a deadline of two minutes (`attn delegate` returns once the delegate's first turn starts): `"seed_id"`, `"session_id"`, `"agent": "codex"` and a `"desktop_id"`. `wsctl list` shows the delegate's tile on the dispatcher's desktop, and `./attn agent peek <delegate>` shows `noted on <seed>`. `./attn seed show <seed>` shows a `status` line reading `growing` and a `tender` line naming the delegate, and `./attn seed notes <seed>` lists `read my assignment`.
- **Check the request.** Run `./attn delegate status drive-delegate-1`. It prints the operation as JSON with `"state": "completed"` and the same `session_id`.
- **The chain from the shell.** Run `./attn session list --tree`. Under the profile's header and `SESSION  AGENT  STATE  TURN  SEED`, each row starts with the session id cut to 8 characters. `drive-delegate` sits on a `└─` or `├─` row below `dispatcher`, with its seed's slug in the last column.
- **The role on the tile header.** Select the delegate for setup with `bridge select_session '{"sessionId":"<delegate>"}'`. `bridge dom_text '{"selector":".delegation-chain-trigger--header[data-delegation-session=\"<delegate>\"]"}'` reads `Drive`. Click it natively: `bridge dom_wait '{"selector":"[data-testid=\"delegation-chain-popover\"] [data-chain-session=\"<delegate>\"]:focus","timeoutMs":5000}'` matches. Escape closes the card.
- **Jump to the dispatcher.** With the delegate shown, press ⌘⇧↑: `bridge native_key '{"key":"ArrowUp","modifiers":["command","shift"]}'`. `bridge get_state` shows `view: "session"` and `activeLeaf.sessionId: "dispatcher"`.
- **The chain from the palette.** Open the palette as the README says, type `delegation chain` and press Enter on "Show delegation chain". `bridge dom_wait '{"selector":"[data-testid=\"delegation-chain-popover\"] [data-chain-session=\"dispatcher\"]:focus","timeoutMs":5000}'` matches. Run `bridge native_key '{"key":"ArrowDown"}'` and `bridge native_key '{"key":"Enter"}'`: the card closes and `bridge get_state` shows `activeLeaf.sessionId: "<delegate>"`.
- **Place a delegate on another desktop.** Select the dispatcher for setup with `bridge select_session '{"sessionId":"dispatcher"}'`, then run `wsctl input --id <terminal> --text "place $D" --enter`. Re-read `./attn agent peek dispatcher` until its newest result shows a `"desktop_id"` that ends in `desktop_7`. `bridge get_state` still shows `activeLeaf.sessionId: "dispatcher"` on the same current desktop, and `wsctl list` shows desktop 7 holding `<placed>`.
- **Hand over from an agent.** Run `wsctl input --id <terminal> --text "handover <seed> $D" --enter`. Re-read peek until it shows a new `"session_id"` (`<successor>`) and `"predecessor_session_id": "<delegate>"`. `./attn seed show <seed>` names `<successor>` as tender, and `./attn seed notes <seed>` lists the handover note. `./attn agent list` still lists `<delegate>`: a handover leaves the previous agent running.
- **Resume from the seed's page.** Press ⌘⇧T to open the garden, then click `[data-seed-row="<seed>"]` natively. With `<successor>` live, the page shows `[data-testid="seed-resume-<seed>"]`; click it natively. `bridge get_state` shows `activeLeaf.sessionId: "<successor>"`.
- **Hand over from the seed's page.** On the same page, click `[data-testid="seed-handover-<seed>"]` natively. The note field takes focus, and the working folder and agent come filled in from the current tender: `codex`, and `$D` as its real path (`/private/tmp/...` on macOS). Type a note with `bridge native_text '{"text":"Pick this up from the page."}'` and click `.garden-handover button[type="submit"]` natively. The form shows "Handing over…" and closes, and the app selects the new agent. `./attn seed show <seed>` names it as tender, and `wsctl list` shows its tile.
- **Retired flags.** Run `./attn delegate --ticket x --source-session dispatcher --model mock-agent-1 --cwd $D`. It exits 2 with `--ticket retired:` and names `attn seed plant` and `attn delegate --seed <seed-id> --cwd <path>`.
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

- Without `--role` or `--fallback`, `attn delegate` exits 2 with `--model is required`. Every delegation needs `--cwd`; a relative one starts at the caller's directory, which for the mock agent is its session folder.
- From a plain shell there is no source session. Pass `--source-session <id>`, or `--agent` with `--model`, or the daemon refuses with `source inheritance is unavailable`.
- A seed that an active session tends is refused without `--handover`, naming the tender. Delegating the dispatcher's own stake also needs `--handover`.
- `attn delegate` returns once the delegate's first turn starts, or after 90 seconds with a `first_turn_unconfirmed` line on stderr. A delegate whose fixture matches nothing in its opening still starts, but writes no note; match on `Your assignment`, which every delegate's opening carries.
- Inside Git, a delegation needs one complete checkout form, and an active session in the same checkout refuses it until `--allow-worktree-reuse`. Keep the delegate folder outside Git to drive the rest.
- `--desktop 7` creates a missing numbered desktop. Rerunning the recipe on the same instance finds it already there; that is not a bug.
- The seed page fills the handover's folder and agent from a live tender. With the tender gone or its folder removed, the form asks for a working folder and an agent ("required without a source session"), and Resume shows only when the closed session's conversation can come back. The mock writes a resumable conversation only with `"resumable": true` in its fixture, and then under the harness home, which is your real `~/.claude` or `~/.codex` unless `ATTN_TOOL_HOME` named a scratch folder when the daemon started. Drive Resume of a closed session only with that folder set, never against the real home.
- A reused `--request-id` returns the first operation with that id. Pick a new one per run, or expect the old result.
- `attn delegate roles add <role> --builtin ...` needs "Add Attn roles" in Settings once; custom roles need `--name`. On a named instance "Add Attn roles" is refused with `attn-workflow installation is disabled for instance "<name>"; saved preferences were not changed`, because a named instance never writes skills into your harness homes ([agent-guidance.md](agent-guidance.md)). Drive roles there with `--name`, as above.
- One `attn delegate` runs one choice of a role. For a design choice that is costly to reverse, the agent presenting the options may propose independent candidates: one Prototyper delegation per distinct model among the role's choices that fit, each `attn delegate --role prototyper --choice <id>`, with the default counted among them. Several Prototyper delegations from one dispatcher on one task are that, not a bug. When the fitting choices share one model it starts one and says so. Working but wrong: candidates started before the user agreed to them, or a delegated Prototyper starting candidates of its own.
- A Pathfinder presents the plan and waits for the user's choice even when the request included execution. It dispatches without waiting only when the user said, before the plan existed, to dispatch without reviewing it, and then records its understanding and the user's instruction as notes on the plan seed and names that seed in its reply. Working but wrong: dispatching after a request that merely included execution, such as "just do it", or skipping the review with no note on the plan seed.
- Working but wrong: `attn delegate roles` lists nothing while the table is turned off, even with roles saved; `show` still lists them. The role's name shows on the delegate's tile header, but a role deleted later stays on delegates that launched with it.
