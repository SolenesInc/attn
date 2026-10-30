# Automations

attn starts a steerable agent on a schedule or an event. An automation is a
prompt with a trigger: by hand, a schedule, or a pull request review request on
GitHub. Each time the trigger fires is an occurrence, and attn starts a run for
it: a full session on the automation's launch desktop that the user can open,
answer and steer like any agent. Continuity decides which session takes the
next occurrence: a new one each time, one per pull request, or one only. The
user follows runs from a drawer, and jumps to the next run that stopped with a
question.

## Sub-features

- `automation-define`: a definition in YAML (`attn.dev/automations/v1alpha1`), validated, applied and shown again; or the same in the app's form.
- `automation-manual`: a `manual` trigger, run from the CLI or the drawer's run button.
- `automation-schedule`: a `scheduled` trigger with a cron and a time zone, and `catch_up` of `skip` or `latest` after downtime.
- `automation-guard`: a scheduled trigger's optional shell command. A run starts when its trimmed output is nonempty and new.
- `automation-review-request`: a `github_review_requested` trigger that starts a run in a worktree at the pull request's head, one per pull request.
- `automation-continuity`: `fresh` or `singleton` for schedules; review requests are always one per subject. A singleton reuses its session after name, schedule, guard and catch-up edits, and replants its seed after a harvest or wither. Changing the prompt, launch contract or location starts a replacement on the next occurrence. Closing the session ends it for good: the next occurrence starts a fresh session and seed.
- `automation-runs`: each occurrence's run, its state (`pending`, `delivered`, `failed`, `cancelled`) and its session.
- `next-run`: go to the next run that stopped and owes the user a turn.
- `automation-enable`: turn an automation off and on; turning it off cancels its pending runs.
- `automation-cleanup`: remove finished runs' worktrees, keeping dirty and active ones; delete an automation, keeping its seeds and run history.
- `automation-guidance`: the opening attn sends a run's agent.

## How to get to it

### People

- ⌘⇧J (`session.nextRun`, `app/src/shortcuts/registry.ts`) shows the next automation run that needs the user.
- Palette (`app/src/application/useAppCommands.tsx`): `automations` ("Show automations" or "Hide automations"), and `next-run-needing-you` ("Next run needing you").
- Sidebar: the automations icon, labelled "Show Automations" or "Hide Automations" (`app/src/application/useAppSidebarActions.tsx`).
- The automations drawer (`app/src/components/AutomationsPanel.tsx`): "New automation", "Close automations", and per automation its row, the on and off switch, "Run now" for manual triggers, "Edit", a failure badge, and its runs, each of which opens its session. A guarded automation shows its latest guard outcome, when it was checked, and the checks since the last run. A failed guard shows the Failed badge and its reason.
- The form (`app/src/components/automations/AutomationForm.tsx`): name, folder, trigger (manual, scheduled or GitHub review request), cron with a plain-words reading, an optional Guard command for schedules, continuity, catch-up, repositories, harness and model, executable, prompt, "Save", "Cancel" and "Delete" with "Confirm delete". With a guard, the plain-words sentence names the command and says the agent works only when it prints something new.
- Where people see and steer runs: each run is an agent tile on the automation's launch desktop, marked ⚡ in the sidebar. In queue flow the queue sidebar groups runs under their automation and the queue bar counts them.
- Settling a run's turn, in either flow: ⌘⇧E, the palette's "Settle this agent" and the ✓ on the run's sidebar row. [queue-flow.md](queue-flow.md) owns and drives them.
- Their own shell: `attn automation`, below.

### Agents

- `attn automation` (`cmd/attn/automation.go`): `validate --file`, `apply --file` with `--launch-desktop` and `--desktop-name`, `list`, `show <id>`, `run <id>` with `--input-file`, `--pr-url` or `--request-id`, `runs <id>`, `enable <id>`, `disable <id>`, `delete <id>`, `cleanup <id>`, and `set <id> --launch-desktop`; `--profile` on each. The launch desktop is in [desktops.md](desktops.md).
- Prompts: a run's opening, which carries the delegation identity, the automation's prompt, the seed it reports to, and for a pull request its identity and the untrusted-data rules (`internal/prompts/content/delegation/identity.md`, `internal/prompts/content/automation/target.md`, `data.md`, `local-review.md`).

### Triggers

- A schedule comes due: the scheduler checks every minute. With `trigger.guard`, it runs the command in the automation's folder first and starts a run only for new output (`internal/daemon/automations_schedule.go`).
- A pull request asks the user for review on GitHub: the next pull request refresh starts a run, and a changed head or a withdrawn request settles it (`internal/daemon/automations_github.go`).
- A run's outcome and its work becoming ready are seed updates for whoever watches its seed ([garden.md](garden.md), [inbox.md](inbox.md)).

## Driving it

Preconditions:

- The baseline instance. The steps that run `bridge` also need its app (`bridge --launch --wait-ready ping` answers) with What's new dismissed (`bridge dismiss_whats_new`); the rest need only the daemon.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-automations.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- A run folder, `R=$(mktemp -d /tmp/drive-attn-automation-run.XXXX) && echo "export R=$R" >> $DRIVE_ENV`, holding this fixture as `$R/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "Drive automation", "actions": [
    {"type": "reply", "text": "Which change should I review next?"}]}
]}
```

- An agent folder, `A=$(mktemp -d /tmp/drive-attn-automation-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "run-automation", "actions": [
    {"type": "capture", "name": "id", "pattern": "run-automation (\\d+)"},
    {"type": "attn", "args": ["automation", "run", "{{id}}"]}]}
]}
```

- A manual definition, written with the folder's paths filled in. The `executable` line points the run at the mock agent the instance shell already exports:

```bash
cat > $P/manual.yaml <<EOF
api_version: attn.dev/automations/v1alpha1
name: Drive automation
trigger:
  type: manual
prompt: |
  Drive automation: ask which change to review next.
launch:
  driver: codex
  model: mock-agent-1
  executable: $ATTN_CODEX_EXECUTABLE
location:
  type: directory
  path: $R
EOF
```

`<id>` is the number `apply` prints as `id`; `<run>` and `<session>` come from `./attn automation runs <id>`. Wherever a step waits for a run, re-read until it shows, with a deadline that fails the run.

- **Validate and apply.** Run `./attn automation validate --file $P/manual.yaml`: it prints `{"valid": true}`. Run `./attn automation apply --file $P/manual.yaml --launch-desktop own --desktop-name "Drive automations"`: it prints the definition as JSON with its `id`, `"enabled": true` and `"trigger_type": "manual"`. `./attn automation list` includes it, and `./attn automation show <id>` prints `# Profile: <name>`, `# Launch desktop: Drive automations` and the YAML.
- **Run from the shell.** Run `./attn automation run <id>`. It prints the run as JSON. Re-read `./attn automation runs <id>` until the run shows `"state": "delivered"` and a `session_id`. Then `./attn agent peek <session>` shows `Which change should I review next?` and `turn: owed to this session`, and `wsctl list` shows the run's tile on a desktop named `Drive automations`.
- **Retry and structured input.** Write `{"topic": "drive"}` to `$P/input.json`. Run `./attn automation run <id> --request-id drive-1 --input-file $P/input.json` twice: both print the same run `id`, and `./attn automation runs <id>` lists it once. A run with a new request id adds a run.
- **An agent runs it.** Run `wsctl add-session --cwd $A --agent claude --label auto-agent --id auto-agent --place`, find its terminal in `wsctl list`, and run `wsctl input --id <terminal> --text "run-automation <id>" --enter`. `./attn agent peek auto-agent` shows the run as JSON, and `./attn automation runs <id>` lists one run more than before.
- **Open the drawer from the palette.** Open the palette as the README says, type `Show automations` and press Enter. `bridge automations_get_state` shows `present: true` and a definition named `Drive automation` with `trigger: "Manual"`, `enabled: true` and `canRunNow: true`.
- **Run from the drawer.** Click `[data-testid="automation-definition-select-<id>"]` natively, then `[data-testid="automation-run-now-<id>"]` natively. `bridge automations_get_state` lists the earlier runs and the new one, which is `delivered` once its session starts; `./attn automation runs <id>` agrees.
- **Next run needing you.** Press ⌘⇧J: `bridge native_key '{"key":"j","modifiers":["command","shift"]}'`. `bridge get_state` shows `activeLeaf.sessionId` set to the session of one run that owes a turn. Press it again: it moves to the next run, and the order repeats once every run has been visited. Open the palette, type `Next run`, and press Enter on "Next run needing you": it moves on in the same order.
- **Open from the sidebar.** Close the drawer by clicking `[aria-label="Close automations"]` natively, then click `[aria-label="Show Automations"]` natively. `bridge dom_wait '{"selector":"[data-testid=\"automations-panel\"]","timeoutMs":5000}'` matches.
- **Turn it off and on.** Run `./attn automation disable <id>`: the definition prints with `"enabled": false`, and `./attn automation run <id>` exits 1 with `automation <id> is disabled`. In the drawer, click `[data-testid="automation-toggle-<id>"]` natively: `bridge automations_get_state` shows `enabled: true`, and so does `./attn automation list`. Click it again for `enabled: false`, then run `./attn automation enable <id>`: the definition prints with `"enabled": true`, and the drawer follows.
- **Edit in the form.** Click `[data-testid="automation-edit-<id>"]` natively. `bridge automation_form_get_state` shows `present: true` and `status: "ready"`. Click `[data-testid="automation-form-name"]` natively, run `bridge native_key '{"key":"a","modifiers":["command"]}'` and `bridge native_text '{"text":"Drive automation edited"}'`, then click `[data-testid="automation-form-save"]` natively. `./attn automation show <id>` prints `name: Drive automation edited`, with the same `launch` model.
- **Set or remove a guard in the form.** Write `exit 0` to `$R/guard.sh`. Create a scheduled automation with cron `* * * * *` and folder `$R`. The Guard input (`[data-testid="automation-form-guard"]`) appears below the cron. Enter `sh guard.sh` natively: the sentence says `only when sh guard.sh prints something new`. Save, then reopen: the command is still there. Clear it and save to remove the guard. Manual and GitHub triggers hide the input and omit the guard when saved.
- **Move the launch desktop.** Run `./attn automation set <id> --launch-desktop 8`. It prints the launch desktop item as JSON, and `./attn automation show <id>` prints a `# Launch desktop:` line that no longer reads `Drive automations`.
- **A schedule.** Write the definition and apply it:

```bash
cat > $P/scheduled.yaml <<EOF
api_version: attn.dev/automations/v1alpha1
name: Drive schedule
trigger:
  type: scheduled
  schedule:
    cron: "* * * * *"
    time_zone: UTC
  continuity: singleton
  catch_up: skip
prompt: |
  Drive automation: report the time.
launch:
  driver: codex
  model: mock-agent-1
  executable: $ATTN_CODEX_EXECUTABLE
location:
  type: directory
  path: $R
EOF
./attn automation apply --file $P/scheduled.yaml
```

  `<schedule-id>` is the `id` it prints. The scheduler wakes every minute, so re-read `./attn automation runs <schedule-id>` for up to two minutes until it lists a `delivered` run, and for up to two more minutes until a second run appears with the same `session_id`. `bridge automations_get_state` shows the definition with `canRunNow: false`. Disable it when done.
- **Edit a singleton with previous sessions.** On an enabled schedule, rename it and change the guard or catch-up setting, keeping the same `id`. Its next run keeps the session. Have that agent harvest its seed; the next occurrence replants the seed and reuses the session. Change the prompt and apply again. `./attn automation show <schedule-id>` labels the last run's session as superseded until the next occurrence starts its replacement. `runs <schedule-id>` keeps all occurrences and marks the old session's `automation.superseded` as `true`. The drawer's run history labels it "Superseded session" and still opens it. In queue flow the automation group and queue bar count only the current session, including when the old sessions are recoverable. An old session that still owes a turn remains reachable through the next-run button, palette and ⌘⇧J. Editing does not stop its agent. Repeat the prompt edit to check that two previous sessions still leave one current run, then disable the schedule.
- **Close a singleton's session.** On the enabled schedule, after a second run reuses the first run's session, disable it and close that session from its tile or with `wsctl rm-session --id <session>`. `./attn automation show <schedule-id>` labels the last run's session as superseded. Enable it again: the next occurrence delivers on a new session and seed, and `runs <schedule-id>` marks the closed session's runs as superseded. Disable it when done.
- **A guarded schedule.** Use `scheduled.yaml` with `schedule: {cron: "@every 3s", time_zone: UTC}`, `continuity: singleton` and `guard: cat state.txt`. Before starting the daemon, set `ATTN_AUTOMATION_SCHEDULE_INTERVAL=1s` in the instance shell. Write `red` to `$R/state.txt`, validate and apply the definition, and capture `./attn automation show <guard-id>`, `list` and `runs <guard-id>` at each step. Wait on an `automations_changed` broadcast, then read `list` for the expected `guard_check`.
  - The first check reads `launched`. The run's opening names an occurrence input file containing `"guard_output":"red"`. Read it from the run's agent to prove the input arrived.
  - The next check reads `unchanged`, increases `checks_since_run`, and adds no run.
  - Empty `state.txt`. The next check reads `empty` and adds no run. Write `red` again. It launches again on the same singleton session.
  - Write `blue`. The next check launches with `guard_output` of `blue`.
  - Change the guard to `echo broken >&2; exit 17` and apply it with the same `id`. The next check reads `failed` with `exit 17: broken` and adds no run.
  - Change the guard to `sleep 120`. After its check, `show` reads `guard timed out after 60s`, and `list` agrees. Disable the automation when done.
- **Read the guard in the drawer.** Open the drawer from the sidebar or palette. After each check, `[data-testid="automation-guard-check-<guard-id>"]` shows `Guard: launched`, `unchanged`, `empty`, or `failed`, the check's relative time, and its count since the last run. A failed check also shows `[data-testid="automation-failure-badge-<guard-id>"]` and its reason. The time updates when the drawer refreshes, with no ticking timer. The packaged `automation-form` scenario creates a guard through native input, reloads it, and observes empty and failed checks without any run.
- **A review request.** Make a local repository that the mock GitHub's `owner/repo` points at, holding the fixture so the review run's agent has something to say:

```bash
G=$(mktemp -d /tmp/drive-attn-review.XXXX)
echo "export G=$G" >> $DRIVE_ENV
git -C $G init -q
git -C $G remote add origin https://mock.github.local/owner/repo.git
cp $R/.attn-mock-agent.json $G/
echo fixture > $G/README.md
git -C $G add README.md .attn-mock-agent.json
git -C $G -c user.name=drive -c user.email=drive@example.invalid commit -q -m fixture
curl -s -X POST "$ATTN_MOCK_GH_URL/__control/head" -H 'content-type: application/json' -d "{\"sha\":\"$(git -C $G rev-parse HEAD)\"}"
cat > $P/review.yaml <<EOF
api_version: attn.dev/automations/v1alpha1
name: Drive review
trigger:
  type: github_review_requested
  repositories:
    mode: all_accessible
    include: [mock.github.local/owner/repo]
prompt: |
  Drive automation: review the pull request.
launch:
  driver: codex
  model: mock-agent-1
  executable: $ATTN_CODEX_EXECUTABLE
location:
  type: repository_worktree
  repository_sources:
    default: {type: managed_cache}
    overrides:
      mock.github.local/owner/repo:
        type: local_clone
        path: $G
EOF
./attn automation apply --file $P/review.yaml
```

  `<review-id>` is the `id` it prints. Run `wsctl refresh-prs`: it prints `PR refresh completed` and establishes the automation's initial observation. Existing review requests do not start runs. Withdraw the mock request with `curl -s -X POST "$ATTN_MOCK_GH_URL/__control/requested" -H 'content-type: application/json' -d '{"active":false}'`, then run `wsctl refresh-prs`. Request review again with the same command carrying `'{"active":true}'`, then run `wsctl refresh-prs`. Re-read `./attn automation runs <review-id>` for up to 45 seconds until a run is `delivered`. `./attn agent list --json` gives its session's `directory`, and `git -C <directory> rev-parse HEAD` equals `git -C $G rev-parse HEAD`.
- **Run one pull request by hand.** A manual automation with a repository location takes a pull request URL. Write `$P/pull-request.yaml` like `review.yaml` with `name: Drive pull request` and `trigger: {type: manual}` in place of the review trigger, apply it, and run `./attn automation run <pull-request-id> --pr-url https://mock.github.local/owner/repo/pull/42`. It prints the run as JSON, and `./attn automation runs <pull-request-id>` shows it `delivered`.
- **Clean up and delete.** Run `./attn automation cleanup <id>`: it prints `cleaned`, `kept_dirty` and `kept_active` lists. Click `[data-testid="automation-edit-<id>"]` natively, then `[data-testid="automation-form-delete"]` natively: it reads `Confirm delete`. Click it again: `./attn automation list` no longer lists `<id>`. Run `./attn automation delete <schedule-id>`: it prints `{"deleted": <schedule-id>}`. Do the same for `<review-id>` and `<pull-request-id>`.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code; `./attn automation runs <id>` after each run; `./attn agent peek <session>`; `bridge automations_get_state` after each drawer action; `bridge get_state` after each ⌘⇧J; and `bridge capture_screenshot '{"path":"<P>/automations-drawer.png"}'` with the drawer open, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `automations-drawer-run-now-state.json`.

## Contexts

- **Durability across restarts**: definitions, occurrences and runs are the daemon's records, and runs' sessions come back like any session. After downtime, a schedule with `catch_up: latest` starts the latest missed occurrence and one with `skip` waits for the next.
- **Linux**: `attn automation` works the same where the daemon and CLI run on Linux. `session.nextRun` is Ctrl+Alt+J there (`LINUX_SHORTCUTS`).
- **Outposts**: a definition has no host; its runs start on the home's machine.
- **Profiles**: an automation belongs to the profile that applied it, and its runs, seeds and launch desktop stay there. Outside attn, `--profile` picks the profile on every verb.
- **Performance and idle cost**: the scheduler wakes once a minute whether or not anything is due. A cleanup removes finished runs' worktrees; until then they stay on disk.
- **Harnesses**: `launch.driver` picks claude, codex or a plugin driver, with `model`, `effort` and `executable`. The run's agent gets the same opening in every harness.
- **Keyboard and native input**: ⌘⇧J and the palette reach runs; the drawer and the form are pointer-first, and their controls take Tab and Enter like any form.
- **Compatibility across versions**: a definition carrying `enabled` is refused with `enabled is managed outside the spec; use 'attn automation enable' or 'attn automation disable'`, and one carrying `policy` with `policy has been removed; scheduled triggers take continuity and catch_up directly`. Unknown fields are refused by name.
- **Flow**: in queue flow, runs stay out of the user's queue; the queue sidebar groups current sessions under their automation, and the queue bar counts them and outstanding turns. Superseded singleton sessions stay in run history, and their outstanding turns still join next-run navigation. In desktop flow sessions keep their tiles on the launch desktop, marked ⚡.

## Tests

- Wire: `internal/daemon/automation_*_wire_test.go`, `internal/daemon/automation_wire_test.go`, `internal/daemon/automations_test.go`, `internal/daemon/automation_retention_test.go`, `internal/daemon/garden_profile_automations_wire_test.go`.
- Stack: `cmd/attn/automation_profile_stack_test.go`.
- Scenario: `automation-lifecycle`, `automation-form`, `automation-surface`, `automation-scheduled-cleanup`.
- Kernel: `internal/automation/automation_test.go`, `internal/automation/schedule_test.go`, `internal/store/automations_test.go`.
- App: `app/src/App.automations.test.tsx`, `app/src/App.automationActions.test.tsx`, `app/src/App.automationForm.test.tsx`, `app/src/App.automationSelection.test.tsx`, `app/src/components/automations/automationFormModel.test.ts`, `app/src/utils/automationRuns.ordering.test.ts`.

## Gotchas

- `attn automation` has no help: `--help` exits 1 with `unknown automation command "--help"`, after resolving the current session. Read `cmd/attn/automation.go` or the form for the fields.
- `location.path` must be an absolute folder that exists. Write the definition with the path filled in, as the heredoc above does; `~` and relative paths are refused.
- The run's agent reads its fixture from `location.path`. For a review request the worktree is the folder, so a fixture has to be committed in the repository for the mock to say anything; without one the run still delivers, silently.
- A run needs the user only while it owes a turn: its agent has stopped and the turn is not snoozed. ⌘⇧J skips a run that is still working and one the user snoozed.
- Schedules fire on the minute tick; a run within seconds is not expected. `ATTN_AUTOMATION_SCHEDULE_INTERVAL`, set before the daemon starts, shortens the tick.
- Allow for the tick and launch when waiting for another occurrence. A live baseline created consecutive occurrences 66 seconds apart; a one-minute deadline expired just before the second appeared. The recipe allows two minutes for each occurrence.
- A review run fires on the next pull request refresh (`wsctl refresh-prs`), not when the mock GitHub changes. The mock lists `owner/repo#42` only after `/__control/head` names a commit.
- The first GitHub observation after applying or enabling an automation records existing review requests without starting runs. Drive a new request after that observation, as the withdraw-and-request sequence above does.
- Turning an automation off cancels its pending runs. A run that was already delivered keeps its session.
- A continuation whose session still exists but cannot resume fails every occurrence until the user closes that session. The error, in `runs`, the drawer's run history and `show`'s `# Last run failed:` line, reads `automation continuity cannot resume the stopped session without a recorded transcript; close session <session> to start a fresh one on the next run`, or names the transcript as unavailable. The wire test `TestClosingASingletonsUnresumableSessionStartsAFreshOneOnTheNextRun` drives it by deleting a mock transcript.
- Guards run through `/bin/sh -c` with the login-shell environment. A pipeline reports the last command's exit status. Capture first, as in `refs=$(git ls-remote origin refs/heads/main) && printf '%s\n' "$refs" | awk '…'`, so a failed first command reads `failed` instead of `empty`.
- A guard fails at 60 seconds or more than 65536 stdout bytes. `show` and `list` name the limit. Failure detail keeps the last 2048 stderr bytes. These tripwires allow commands measured at 1.09–2.01 seconds and outputs of 15–41 bytes.
- Empty guard output forgets the handled output. A failed check keeps it. A failed or cancelled run, or a changed guard command, lets the same output launch again. Disable and enable keep the latest check.
- Skipped checks have no run. `checks_since_run` counts checks since the last launch and resets when a new run is claimed.
- The drawer's "Run now" and `attn automation run` work only on manual triggers. A scheduled or review-request automation answers `automation <id> is provider-driven and cannot be run manually yet`. `--pr-url` also needs a `repository_worktree` location, and refuses with `--pr-url requires a repository_worktree automation` otherwise.
- Working but wrong: a run reads `delivered` once its session starts, whatever the agent does next. A fixture that matches nothing in the opening leaves a delivered run sitting silent; prove a run by its agent's screen, not by its state.
