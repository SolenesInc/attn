# Auto mode

Rules and a guardian model answer agents' permission prompts; agents propose
rules, only the user promotes them. Auto mode is Pi's approval policy. Prefix
rules decide whether a command runs, asks or is refused; a list of network
hosts decides what a sandboxed command may reach; an approval policy and a
sandbox mode set the rest. When a command needs approval, exactly one reviewer
answers it: the user, or, with auto mode on, the Guardian model. Agents and
people propose rules, hosts and policies from the CLI, and nothing they
propose takes effect until the user promotes it in Settings. The environment,
what the Guardian knows about this machine, is edited directly.

## Sub-features

- `automode-show`: the effective policy, the Guardian, the environment, the rules, the hosts and the pending proposals.
- `automode-rules`: propose a prefix rule, allow, prompt or forbidden, that inherits or bypasses the sandbox; or propose removing one.
- `automode-hosts`: propose allowing or denying a network host, or removing one.
- `automode-policy`: propose an approval policy, a sandbox mode, or whether the proxy may reach localhost.
- `automode-environment`: the slots the Guardian reads about this machine, such as trusted repositories, domains and registries, edited directly.
- `proposal-promote`: the user promotes or discards each proposal in Settings.
- `guardian`: the model and reasoning that answer prompts when auto mode is on.
- `automode-denials`: recent denials, newest first, with the session, the rule and the reason.
- `automode-launch`: whether new Pi sessions start in auto mode, the picker's switch, and `/auto` inside a session.
- `repository-rules`: rules a repository's checkout carries, merged in for sessions in it.

## How to get to it

### People

- Settings, Auto mode (section `autoMode`, `app/src/components/AutoModeSettings.tsx`): Global policy, with the new-sessions switch `[data-testid="automode-new-sessions"]`, "Approval policy" and "Sandbox mode"; This machine, the environment slots, each with its edit button `[data-testid="automode-slot-edit-<slot>"]`; Global rules; Network; Proposed rules, each with Promote `[data-testid="automode-promote-<id>"]` and Discard `[data-testid="automode-discard-<id>"]`; and Recent denials with a refresh button. The Guardian's "Guardian model" and "Guardian reasoning" are in `app/src/components/GuardianSettings.tsx`.
- The new-agent picker's auto mode switch, `[data-testid="location-picker-automode-toggle"]`, shown only for a harness whose plugin offers auto mode, which is Pi with `attn-pi` installed (`app/src/components/NewSessionDialog/SessionControls.tsx`). Launcher 2.0 adds ⌥A for it; the standard picker has no key.
- Inside a Pi session: `/auto on`, `/auto off` and `/auto status`, and `/permissions` for that session only (`plugins/attn-pi/docs/automode.md`).
- Where people see what agents do: a denial is announced naming the session, what was blocked, why and who decided, and it lands in Recent denials.
- Their own shell: `attn automode`, below.

### Agents

- `attn automode` (`cmd/attn/automode.go`): `show`; `env`, `env set <slot> <value>…`, `env clear <slot>`, `env notes` (from stdin); `rule add <token>…` and `rule remove <token>…` with `--decision allow|prompt|forbidden`, `--sandbox inherit|bypass` and `--justification`; `host add <host>` and `host remove <host>` with `--decision allow|deny`; `policy` with `--approval-policy`, `--sandbox-mode` and `--allow-local-binding`; `denials [--limit <n>]`. Every subcommand takes `--json`. `rule`, `host` and `policy` record a proposal; `env` writes directly.
- Prompts: what a Pi session is told about its sandbox, its credentials and its failures (`internal/prompts/content/pi/security/`), the Guardian's policy and answer format (`internal/prompts/content/pi/guardian/`), and the environment slots the Guardian reads (`internal/prompts/content/pi/environment/`).

### Triggers

- A Pi command that needs approval goes to the Guardian when auto mode is on, and to the user otherwise; a denial is recorded and announced (`internal/daemon/automode.go`).
- attn reads the repository a folder belongs to for the `trusted_repo` and `repo_visibility` slots, and the rules its checkout carries (`internal/daemon/automode_detect.go`).
- Only the user's promotion changes the rules, hosts or policy (`PromoteAutoModeProposal`, `internal/store/automode.go`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`). `R=$PWD && echo "export R=$R" >> $DRIVE_ENV` at the repository root.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-automode.XXXX) && echo "export P=$P" >> $DRIVE_ENV`, outside any git repository.
- An agent folder, `M=$(mktemp -d /tmp/drive-attn-automode-agent.XXXX) && echo "export M=$M" >> $DRIVE_ENV`, holding this fixture as `$M/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "propose", "actions": [
    {"type": "attn", "args": ["automode", "rule", "add", "--sandbox", "inherit", "go", "vet"]}]}
]}
```

`<n>` and `<m>` are the proposal numbers the propose steps print; `<terminal>` is the agent's terminal id from `wsctl list`; `<P>` is the proof folder's path written out.

- **Read the config.** Run `(cd $P && $R/attn automode show)`. It prints `enabled by default`, `approval policy`, `sandbox mode` and `guardian`, then `environment:` with `trusted_repo  (unset: …)` and the other slots, the rules, `network local binding:`, and `pending proposals: none`. `./attn automode denials` prints `no denials recorded`.
- **An agent proposes a rule.** Run `wsctl add-session --cwd $M --agent claude --label automode-agent --id automode-agent --place`, find its terminal, and run `wsctl input --id <terminal> --text "propose a rule" --enter`. `./attn agent peek automode-agent` shows `recorded proposal <n>: rule allow, inherit sandbox: go vet` and `This changed nothing yet. Promote it in the attn app to put it in force.` `./attn automode show` lists it under `pending proposals (promote them in the attn app):`, and its rules do not include `go vet`.
- **A person proposes from the shell.** Run `./attn automode host add example.com`: it prints `recorded proposal <m>: host allow example.com`. `./attn automode policy --approval-policy untrusted` records a policy proposal the same way.
- **Promote in Settings.** Open Settings with `bridge native_key '{"key":",","modifiers":["command"]}'` and click `[data-testid="settings-nav-autoMode"]` natively. `bridge dom_wait '{"selector":"[data-testid=\"automode-promote-<n>\"]","timeoutMs":5000}'` matches. Run `bridge dom_scroll_into_view '{"selector":"[data-testid=\"automode-promote-<n>\"]"}'` for setup, then click it natively. `./attn automode show` lists `allow  inherit  go vet` under its rules, and proposal `<n>` is no longer pending.
- **Discard in Settings.** Run `bridge dom_scroll_into_view '{"selector":"[data-testid=\"automode-discard-<m>\"]"}'` for setup, then click `[data-testid="automode-discard-<m>"]` natively. `./attn automode show` no longer lists `<m>`, and `network allowed` does not include `example.com`.
- **Edit the environment.** Run `./attn automode env set domains internal.example`. It prints `environment:` with `domains  internal.example`. With Settings still open on Auto mode, `bridge automode_get_state` shows `present: true` and the slot with that value; the reply is empty while the section is closed. `./attn automode env clear domains` puts it back to `(unset: …)`.
- **The picker's switch.** Press Escape to close Settings, then press ⌘N and wait for `[data-testid="location-picker-overlay"]`. With Claude or Codex checked, `[data-testid="location-picker-automode-toggle"]` is absent; it shows only for Pi, which needs `./attn plugin install-bundled attn-pi` and the real `pi` binary. With Pi checked, click the switch natively and read `aria-checked` flip. Press Escape.
- **The Guardian.** Reopen Settings on Auto mode. This needs Pi with `attn-pi` installed: the model list is read from Pi, and on the baseline `[aria-label="Guardian model"]` offers only "Follow session model" and the section shows a harness-unavailable error. With Pi, click the select natively, choose a model with `bridge native_key` ArrowDown and Enter, and `./attn automode show` prints that model on its `guardian` line.
- **Proof.** In `$P`, keep `./attn automode show` before the proposals, after the promote and after the discard; `./attn automode show --json`; the peek after the agent's proposal; each command's exit code; and `bridge capture_screenshot '{"path":"<P>/automode-proposals.png"}'` with the proposals showing. Put the way in in each file's name, such as `automode-settings-promote.txt`.

## Contexts

- **Durability across restarts**: rules, hosts, policy, environment, proposals and denials are the daemon's records and survive restarts. A respawned Pi session keeps its auto mode choice and its policy pair.
- **Linux**: Pi's sandbox wraps commands in bubblewrap on Linux and Seatbelt on macOS (`plugins/attn-pi/AGENTS.md`). The verbs work the same.
- **Performance and idle cost**: the denial log keeps a bounded number of denials and says what it dropped; pending proposals have a cap, and a proposal past it is refused naming the proposer, the limit and the ask.
- **Harnesses**: auto mode is Pi's, through the `attn-pi` plugin. Claude Code and Codex use their own permission modes, YOLO and auto-approve ([harnesses.md](harnesses.md)).
- **Keyboard and native input**: ⌥A toggles auto mode in Launcher 2.0 for a Pi agent; the standard picker's switch is clicked.
- **Compatibility across versions**: old patterns that could not be turned into rules show under `not converted (rewrite these as rules)`. A rule proposed without `--sandbox` keeps the old behavior: allow bypasses the sandbox, other decisions inherit it. Shipped rules stay ahead of the user's and cannot be removed.

## Tests

- Wire: `internal/daemon/automode_wire_test.go`, `internal/daemon/automode_driver_wire_test.go`, `internal/daemon/automode_launch_wire_test.go`, `internal/daemon/automode_repository_rules_wire_test.go`, `internal/daemon/plugin_driver_rules_wire_test.go`.
- Stack: `cmd/attn/automode_stack_test.go`.
- Scenario: `automode-environment`; outside the catalog, `app/scripts/real-app-harness/scenario-pi-automode.mjs` and `app/scripts/real-app-harness/scenario-pi-security.mjs`.
- Kernel: `internal/automode/*_test.go`, `internal/store/automode_test.go`, `internal/store/automode_guardian_test.go`.
- App: `app/src/App.autoMode.test.tsx`, `app/src/components/SettingsModal.autoMode.test.tsx`, `app/src/components/GuardianSettings.selection.test.tsx`.

## Gotchas

- `rule`, `host` and `policy` only propose, and nothing over the CLI promotes. A run that expects `automode show` to change after `rule add` has not promoted yet.
- `env` writes at once, unlike the rest; a shipped forbidden rule keeps a session out of it.
- `automode show` reads the folder it runs in. From the repository root it prints `trusted_repo  (detected here: …)` and may merge the checkout's rules; run it from a folder outside any repository for the plain view.
- On the baseline nothing asks for approval: the mock agent is not Pi, so the Guardian never answers and `denials` stays empty. Denials and the Guardian need a real Pi session with the `attn-pi` plugin, as `automode-environment` and `scenario-pi-automode.mjs` run.
- Proposed rules can be below the settings viewport. Scroll Promote and Discard into view before taking their bounds and clicking natively. Close Settings before driving the picker, then reopen Auto mode for the Guardian read.
- A proposal number is the daemon's counter; take it from the propose output or `--json`, never from an earlier run.
- `rule add` with no tokens exits 2 with `automode rule add: name the command tokens`.
- Working but wrong: the Settings section describes itself as "Manage attn's pi automode plugin" though it also holds the environment and the Guardian; and the CLI tells agents to "Promote it in the attn app" while nothing in the app points at a new proposal outside Settings.
