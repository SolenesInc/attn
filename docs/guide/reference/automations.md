# Automations

An automation is a prompt Attn starts on its own. Each time its trigger
fires, Attn starts an agent with your prompt, the same kind of agent you
launch yourself: you can open it, read along and steer it. Use one for work
that recurs: a morning triage, a check after every new commit, a first review
of every pull request you are asked to look at.

## Triggers

An automation has one trigger:

- **Manual**. It runs when you click **Run now** or run
  `attn automation run <id>`.
- **Scheduled**. It runs on a cron schedule, such as `0 9 * * 1-5` for 9:00
  on weekdays, in your time zone.
- **PR review requested**. It runs when someone asks you to review an open,
  non-draft pull request on GitHub. Attn sees these through the accounts
  you are logged into with `gh`.

Each time a trigger fires is an occurrence, and each occurrence gets one run.

## Create an automation

Open the automations drawer with the Automations button at the top of the
sidebar, or "Show automations" in the command palette (Cmd+Shift+K,
Ctrl+Alt+K on Linux). Click **New automation** and fill in:

- **Name**, and the **Trigger** with its settings (below).
- **Runs as**. The harness (Claude Code or Codex), model and effort, and the
  launch desktop where its agents start. By default that is a new desktop
  named after the automation.
- **Prompt**. The instruction the agent receives. Details of the occurrence,
  such as the pull request, arrive separately as data and never rewrite the prompt.

The form ends with a sentence that says what the automation will do, in
plain words. **Save** creates the automation, enabled.

For a **Manual** or **Scheduled** trigger, **Directory** is the folder the
agent works in. A scheduled trigger also takes:

- **Schedule (cron)**. A standard five-field cron expression. The form reads
  it back in words.
- **Worker**. **Fresh** starts a new agent for every run. **Singleton** sends
  every run to the same agent, resuming the agent if it was closed. When you change
  the prompt, harness, model, effort or directory, the next run starts a new
  agent on the same seed, so the new agent picks up where the old one left
  off. Attn leaves the old agent running, and you can still open the old agent.
  Once a run on the new agent succeeds, the seed has changed hands: if the old
  agent tends the seed again, later runs fail and name its tender instead of
  taking the seed back.
- **Missed runs**. What happens to a time that passed while Attn was off or
  the automation was disabled. **Skip** runs the missed time only if Attn
  notices within 5 minutes. **Latest** runs once for the most recent missed time.
- **Guard (optional)**. A shell command, such as `git rev-parse HEAD`, that
  runs in the folder when the schedule is due. The agent starts only when the
  command succeeds and prints something different from what the last run
  saw, and the agent receives that output.

For **PR review requested**, list repositories to include or exclude as
`host/owner/repository` (for example `github.com/acme/api`). With no includes,
every repository you can reach counts. Each review runs in a fresh worktree
checked out at the pull request's head commit, so your own clone is never
touched. Each pull request has one agent: when you are asked to review the pull
request again, the same agent picks it up. When you change the prompt, harness, model
or effort, the next review starts a new agent on the pull request's seed.
Attn ignores requests that already exist when you enable the automation.

## Runs

A run's agent starts on the automation's launch desktop. The agent works on a seed
in the garden that Attn plants for the run, so the run's progress and outcome
appear there. For a review run, Attn tells the agent that the review is local
only. The agent reports on the seed and the session and does not post, approve,
comment, or push on GitHub.

In [queue flow](queue.md), runs never join the queue. The sidebar has an
Automations group under the queue that lists each automation with its runs.
A run that stopped and owes you a turn shows a count beside its automation,
and the group's header offers "N runs need you". Click it, or press
Cmd+Shift+J (Ctrl+Alt+J on Linux), to open the next run that needs you. Press
again to open the one after. Settle a run with ✓ on its row or Cmd+Shift+E
(Ctrl+Alt+E on Linux), as you would any agent. In desktop flow, runs are
tiles on their launch desktop like any other agent.

In the drawer, click an automation to see its runs: state (pending,
delivered, failed or cancelled), when it started, and a click through to its
agent. If a run fails or a guard fails, the drawer shows **Failed** on the
automation's row, with the error underneath.

## Managing automations

In the drawer, the checkbox on each row enables or disables the automation,
and **Edit** opens the form again. When you disable an automation, Attn cancels
runs that have not started yet. **Delete** in the form removes the
automation, and its seeds and run history stay.

The CLI does the same. Definitions are YAML. `attn automation show <id>`
prints a definition, and `--profile <name|id>` picks a profile other than the
current profile.

| Command | What it does |
|---|---|
| `attn automation list` | Lists the automations as JSON. |
| `attn automation show <id>` | Prints the definition, its profile, launch desktop, last run and guard check. |
| `attn automation apply --file <yaml>` | Creates an automation, or updates the one whose `id` the file names. `--launch-desktop` sets where runs start. |
| `attn automation validate --file <yaml>` | Checks a definition without saving it. |
| `attn automation run <id>` | Runs a manual automation now. |
| `attn automation runs <id>` | Lists its runs as JSON. |
| `attn automation enable <id>`, `disable <id>` | Turns it on or off. |
| `attn automation set <id> --launch-desktop <own\|digit\|name\|id>` | Moves where its runs start. |
| `attn automation delete <id>` | Deletes the automation. Seeds and run history stay. |
| `attn automation cleanup <id>` | Removes the worktrees of its finished runs, keeping any with uncommitted changes or a live agent. |

A scheduled definition looks like this:

```yaml
api_version: attn.dev/automations/v1alpha1
name: Morning triage
trigger:
  type: scheduled
  schedule:
    cron: "0 9 * * 1-5"
    time_zone: Europe/Lisbon
  continuity: fresh
  catch_up: skip
prompt: Read yesterday's CI failures and plant a seed for each new one.
launch:
  driver: claude
location:
  type: directory
  path: /Users/you/projects/api
```

## Details

<details>
<summary>Can I run a scheduled or review automation by hand?</summary>

No. Only manual automations run on demand. For a review on demand, write a manual definition with a `repository_worktree` location and run it with `attn automation run <id> --pr-url <url>`.
</details>

<details>
<summary>Do runs stop to ask me for permissions?</summary>

Runs start unattended, with an approval reviewer in your place, so a routine permission prompt does not wait for you. A run still stops and owes you a turn when it finishes or asks you something.
</details>

<details>
<summary>What does the agent receive?</summary>

Your prompt, the seed it works on, and the path to a JSON file with the occurrence's data: the scheduled time and the guard's output, or the pull request's title, body, author, branches and head commit. Attn tells the agent to treat that data as untrusted and never to follow instructions in it.
</details>

<details>
<summary>What happens to a review run when the request is withdrawn?</summary>

If the agent has not started yet, Attn cancels the run and notes why on its seed. An agent that already started keeps working.
</details>

<details>
<summary>Are old runs and worktrees cleaned up?</summary>

Yes. Each hour Attn removes runs older than 14 days beyond the newest 200 of each automation, with their worktrees. It keeps any run whose worktree has uncommitted changes or whose agent is still live. `attn automation cleanup <id>` removes finished worktrees right away.
</details>

<details>
<summary>Does an automation belong to a profile?</summary>

Yes. It runs, and its agents start, only in the profile it was created in. The drawer shows the current profile's automations.
</details>
