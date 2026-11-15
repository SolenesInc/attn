# Delegation

An agent can start another agent to work on something. The delegate is a full
session you can open and steer, in any harness.

If you are used to subagents, think of a delegation as a subagent that you can
inspect and steer, because the delegate is a full agent.

## Delegating

Ask any agent to delegate work to another agent. The delegate opens at the end of
the row on your agent's desktop, without taking your focus, or on whichever
desktop you ask for.

Attn records your delegation chain. Cmd+Shift+Up (Ctrl+Alt+O on Linux)
takes you from a delegate back to the agent that started it. To go the other way, click the chain button on an agent's sidebar row. The button lists everyone in the chain, and clicking an agent in the list takes you to that agent.

### The `attn delegate` command

Agents delegate with `attn delegate`, and you can run it yourself from a shell
inside Attn. Every delegation works on a seed in [the garden](#the-garden):
either a new seed made from a brief, or an existing seed.

```sh
attn delegate --brief "Find why the export times out" --role pathfinder \
  --cwd ~/src/shop --reuse-checkout --branch main
attn delegate --seed s-7k3f9m --role builder \
  --cwd ~/src/shop --new-worktree --branch fix-export --from origin/main
```

The flags you'll reach for most:

- `--brief TEXT` or `--brief-file PATH` plants a new seed for the work. `--seed
  ID` uses an existing seed instead. Pass exactly one of the three.
- `--cwd PATH` is the folder the delegate works in. It's always required.
- Inside a Git repository, also pick a checkout: `--reuse-checkout --branch NAME`
  works in the checkout that's already on that branch, `--new-worktree --branch
  NAME --from REF` makes a new branch and worktree, and `--new-worktree
  --existing-branch NAME` makes a worktree for a local branch you already have.
- `--role ID` uses one of your [delegation roles](#roles), and `--choice ID` uses
  one of the role's alternatives. `--fallback` uses "Anything else". Without
  `--role`, `--choice`, or `--fallback`, `--model` is required (`--model default`
  takes the harness default).
- `--agent NAME`, `--model`, `--effort` and `--provider` pick the harness and
  model for this one delegation, on top of a role or without one.
- `--desktop` opens the delegate on another desktop of your profile. Give the
  desktop's shortcut digit, name, or id.
- `--name` names the session. The default is the title of the brief or seed.
- `--priority` puts the delegate's turns first in your queue.
- `--authorization TEXT` says how far you let the task go: the steps the
  delegate takes beyond what the project's guidance allows, and the steps it
  stops before, such as "Land on main once the checks pass. Ask me before
  releasing." See [Authorization](#authorization).

`attn delegate --help` lists every flag, and `attn delegate roles` prints the
roles agents choose from.

### Authorization

A project's guidance, such as `AGENTS.md`, sets what an agent does without
asking. When you tell an agent that its delegate may go further, or must stop
earlier, the agent writes that as instructions to the delegate and passes them
with `--authorization`. Attn saves the words on the seed, with the agent that
set them, and puts them in the delegate's opening message as yours. The opening
also tells the delegate to take each step you allowed without asking again. A
handover keeps the words: the next agent's opening carries the seed's current
words without anyone passing them again. `attn seed show` prints them under the header, with
who set them and when.

Only the seed's tender, the sessions above the tender in the delegation chain,
the chief or you can set or change them while the seed is claimed. Attn doesn't
check that the words are yours. The agent that sets them is responsible for them, and
the seed's log records who set what.

When you change your mind, change the words yourself on the seed's page in the
app, with Set, Change or Clear, or tell the agent, which sets them with `attn
seed authorization`. Attn types the change into the delegate's conversation as
your message, waiting while the delegate needs approval. Only the delegate's
own seed changes: each agent decides whether the change applies to the work it
delegated, and passes it on. The seed's page and `attn seed show` say whether
the last change was typed in and when.

## Steering a delegate

Open the delegate's tile and talk to the delegate, like any agent. When you're
done, close the delegate like any agent.

The agent that delegated work gets updates as the delegate works, so the agent
can follow along. The agent can stop following with `attn seed unwatch`.

### Agents messaging each other

Agents can message each other with `attn agent msg`. For example, you can ask
the agent that started a delegate to send the delegate a message. The target
can be a session, a crew member, or a seed id. A seed id reaches whoever tends
that seed now, or whoever tends it next if nobody does yet. The message waits in the recipient's inbox, and Attn
lets the recipient know it's there. When the seed is on standby and its tender's
session is closed, the message resumes the tender, as [Seeds on
standby](#seeds-on-standby) describes.

### Handing work to a fresh agent

A handover gives a seed, and the seed's claim, to a new agent. The agent handing
over the seed runs `attn delegate --seed <id> --handover -m "<what comes next>"`,
plus a role and a folder, as in any delegation. The note goes on the seed, and
the new agent's opening carries the seed's [authorization](#authorization). The
previous agent keeps running until you close it or it closes itself.

Handovers are how a Pathfinder passes an agreed plan to an Orchestrator or a
Builder: the plan stays in the seed, and the new agent reads it from there.

### Asks

Agents send you questions with `attn ask` instead of stopping to wait for you.
Asks are on by default. Turn them off in **Settings → Agents and models → Asks**
(`asks_enabled`); agents keep the setting they launched with.

Cmd+Shift+R (Ctrl+Alt+R on Linux) opens the Asks drawer, where you answer them
without opening the agent's terminal. Attn types your answer into the
agent's conversation as your message. When the ask is about a seed, the answer
goes to whoever tends that seed when you answer, and Attn adds the answer to the
seed as a note.

When an ask has recommended answers for two or more questions, **Accept all
recommended** (or `a`) selects every one of them in the draft, so you can review
and change them before you send. Questions with no recommendation stay
unanswered. The phone app has the same button.

**Snooze** (or `s`) in the drawer snoozes the agent that holds the ask, with the
queue's snooze choices, and **Wake** (or `s`) on a snoozed agent's ask wakes it.
[The queue page](queue.md) says what a snooze does to the agent's asks.

## Roles

When making a delegation, the agent picks:

- A coding harness, such as Claude Code or Codex.
- A model.
- A reasoning effort level.
- A way of working.

In **Settings → Delegation**, you define delegation roles, each a combination of
those choices. Your roles together make up the delegation table. For each role
you set:

- When to choose this role. The agent reads this to pick a role, so describe
  the work the role fits and what it's not for.
- What it runs on: harness, model and effort.
- Its instructions: how it works and when it stops.

A role can have alternatives: another model for a condition you describe, such
as frontend work, or consequential work that needs a stronger model. "Anything
else" covers delegations no role fits. The switch at the top of **Settings →
Delegation** turns the whole table off, and then agents pick harness and model on
their own.

Attn bundles its own delegation roles. Add them with **Add Attn roles** in
**Settings → Delegation**, or with `attn delegate roles install`. Attn adds each role
without a model, and agents can't pick a role until it has one, so pick a model
for each role you want agents to use. Adding the
roles also installs the attn-workflow skill for your harnesses. The
bundled roles need the skill. Any agent on your computer can use the skill,
although the skill is built for use inside Attn. To change how a bundled role works, use **Make
an editable copy**.

Agents can read and change the saved table for you with `attn delegate roles`
(`attn delegate roles --help` lists the commands), for example to add Attn's
roles or set a role's model.

### Attn's roles

- Pathfinder: investigates a question, explains how something works, or works
  with you toward a plan.
- Prototyper: builds options for a UI or the shape of an interface, so you can
  compare them and pick one.
- Orchestrator: executes an agreed plan. It starts Builders, reviews their work
  and verifies the result.
- Builder: implements an agreed change.
- Reviewer: reviews a change or a PR, only when you ask for one.

[Building with roles](../tutorials/building-with-roles.md) walks through a whole
change with them.

## Watching pull requests

An agent that opened or reviews a pull request runs `attn pr watch <url>` to
hear about it in its inbox. A watch sends:

- State changes: ready, changes requested, failed checks, merged and closed.
  These always reach the agent.
- Feedback: comments, review comments, review bodies and reopened review
  threads. By default, a watch skips the feedback your own account wrote.
- Monitoring outages.

Agents post to GitHub with your account, so a comment from your account usually
comes from an agent. So that an agent doesn't hear about its own `gh pr comment`,
a watch skips the comments, replies and review bodies that the account attn uses
for that GitHub host wrote, on any pull request. Everyone else's reach the agent,
including those from the author of a pull request you review. On your own pull
request, a watch also skips reopened review threads that you started. GitHub
doesn't say who reopened a thread, so on someone else's pull request every
reopened thread reaches the agent. If you steer an agent by commenting from your
own account, turn on **Ring on my own PR comments** in **Settings → Agents and
models**, or run `attn settings set pull_requests.own_comments ring`. The setting
belongs to your profile and defaults to `quiet`. Attn doesn't replay the comments
that a watch skipped before you switched to `ring`.

Watches survive restarts, and Attn doesn't notify the agent again about an
observation that hasn't changed. Feedback already on the PR at the first check
is the baseline. Changing the mode keeps that baseline. If you unwatch and then
watch again, the new watch starts a new baseline.

| Mode | Ready when |
|---|---|
| `green` (default) | The PR is open, not a draft, and GitHub reports it `CLEAN` or `HAS_HOOKS`. |
| `codex` | `green`, plus the Codex app's 👍 and no 👀 after a 30-second settling window. Reactions are best effort, since GitHub doesn't tie them to a commit. |
| `formal-review` | GitHub's overall approval. `--reviewer <login>` requires that reviewer's current verdict instead. |

Apart from the conditions in the table, comments, threads, requested changes
and failed checks are updates the agent hears about. None of them adds a
condition for ready. A watch never changes anything on GitHub and never grants
permission to merge.

`attn pr status` lists the watches, and `attn pr unwatch <url>` stops a watch and
clears the watch's unread notifications. Closing or merging the PR ends the
watch. For a script or a terminal that should block until the PR is ready, use
`attn pr wait-ready <pr> --repo <owner/repo> --mode <mode>`. The command's
`--help` explains reviewers, baselines, resuming, and exit codes.

### Watching a pull request on a seed

To watch a pull request for a seed instead of for the session, run
`attn pr watch <url> --seed <id>`. The watch belongs to the seed, so it outlives
the session that created it. Each update goes to whoever tends the seed when the
update arrives, the way [a message to a seed id](#agents-messaging-each-other)
does. A handover therefore moves the watch to the new tender, and nobody has to
watch again. `attn pr watch` refuses a seed that doesn't exist or is closed.

`attn pr unwatch`, `attn pr ls` and `attn pr status` take `--seed <id>` too, and
then act on the seed's watches. Without `--seed`, they act on the session's own
watch, including one it holds as a crew member or the Chief. Each watch reports
readiness and health in its own mode, so two seeds can watch one pull request in
different modes. `attn seed watch` is a different watch: it follows the seed's
own moves.

Harvesting or withering the seed stops its watches and clears their unread
updates. A seed whose work continues after the merge, such as following a
deploy, must not use `attn seed harvest --when-merged`. That command harvests
the seed at the merge, which stops its watches and ends its work.

## The garden

The garden is Attn's work tracker, and every delegation works on a seed in the
garden. A seed is a ticket, like one in Jira or Linear, kept inside Attn. If you
know [beads](https://github.com/steveyegge/beads), the garden works much like
beads.

Seeds can contain other seeds at any depth. You can delegate each seed to an
agent, or one agent can work across several seeds.

A seed with other seeds is called a plot. Agents tend a seed to take it,
harvest it to complete it, wither it to drop it, and park it to put it on hold.

Cmd+Shift+T (Ctrl+Alt+G on Linux) opens the garden, with every seed and where it
stands.

Agents work the garden with `attn seed`: `plant`, `tend`, `park`, `harvest`,
`wither`, `replant`, `note`, `show` and `ls` are the everyday verbs.
`attn seed --help` lists them all, and `attn seed guide` explains how agents
write seeds and plots.

### Reminders

An agent, or you from your shell, can set a reminder on a seed:

```sh
attn seed remind s-7k3f9m --in 20m -m "Check the rollout of the new build"
```

At that time Attn posts the message to the seed's mailbox, which delivers it
to whoever tends the seed then. When nobody tends the seed, the message waits
for the next tender. Attn checks once a minute, so a message can arrive up to
a minute late. To pick a time of day, use `--at` instead of `--in`, with a date
and time such as `2026-11-05T09:00:00+01:00`.

A seed holds one reminder, and a new one replaces it. To remove it, run
`attn seed remind s-7k3f9m --clear`. The reminder also ends when Attn posts
the message, and when the seed is harvested or withered. Until then,
`attn seed show` and `attn seed ls` print it.

### Seeds on standby

An agent that waits on someone else can put its seed on standby, instead of
keeping a session open for the wait:

```sh
attn seed standby s-7k3f9m -m "Review is pending on #123. When it is approved, merge (approved by the user up front), then follow the deploy."
attn seed standby s-7k3f9m --at 2026-11-13T09:00:00+01:00 -m "Pick this up Friday: rerun the backfill."
```

The note says what to do next, and standby closes the agent's session. `--stay`
keeps it open, and agents pass it only when you asked them to stay. `--in` and
`--at` also set the seed's reminder to that time, with the note as its message,
so the agent resumes then if no other mail arrives first.
`attn seed standby --clear` ends standby and leaves the reminder.

When mail reaches the seed, Attn resumes the closed session as
itself, or starts a session for the crew member that tends the seed, and hands over the mail
with the note. Mail for a seed is a reminder, an update from a pull request
watch that addresses the seed, a message to the seed's id, and your answer to an
ask about the seed. Only the seed's tender can put a seed on standby.

Standby ends when the agent has read all of the seed's mail. It also ends when
the seed is parked, harvested or withered, and when the seed gets a new tender.
If the session closes before the agent reads the mail, the seed stays on standby
and the mail resumes the agent right away. Attn tries each piece of mail at
most 3 times, and the limit below still applies. A session you closed
yourself resumes the same way while its seed is on standby. A closed session
whose seed isn't on standby stays closed, and its mail waits for the next
tender. To keep an agent closed, park or wither its seed.

`attn seed ls --standby` lists the seeds on standby. Each row shows the pull
requests the seed watches with their readiness, the seed's reminder, and why
Attn can't resume the agent when it is blocked.

A resumed agent ends its turn by going on standby again, by harvesting the seed,
by asking you with `attn ask`, or by reporting. Like any agent that stops, it is
owed a turn in your queue.

**Settings → Agents and models → Seeds on standby** picks which mail resumes a
closed agent: **Any mail**, **PRs and reminders**, or **Never**. The choice
belongs to your profile, and `attn settings set garden.tender_resume` changes it
with `any`, `watches` or `off`. Mail that doesn't resume an agent waits in its
inbox. A crew member that tends the seed gets a session under every choice.

Attn delivers a resumed Claude Code session's mail through the
[Claude mod](using-claude-mod.md) and never types it into the terminal. The
resumed session loads the mod, even if the session that went on standby ran
without it.

Standby shrinks the conversation before it closes the session when the session
can compact, as `attn agent close --self --compact` does. Attn waits for the
agent's turn to end, has the harness compact the conversation, then closes the
session, so the next run resumes the compacted conversation. Work the agent
leaves running in the background, such as a shell or a monitor, does not hold
the close. Mail the
agent reads before the compact ends the standby, and the session stays open.
Mail the agent hasn't read doesn't cancel the close: the session compacts and
closes, and the unread mail resumes it again. Mail that arrives during the compact
waits until the session has closed, then resumes the agent. Nothing you type or
send stops the close. Notes you send from the app or a phone while the compact
runs are not delivered, and Attn tells you that the session is closing. If the compact fails,
the session still closes once the turn ends, and its reason says the compact did
not run.
Restarting the daemon on the machine that runs the session drops a close that
still waits for its compact, and the session stays open. If the home is offline or
unreachable when the compact ends on an outpost, the session closes when the link
returns.

Claude Code sessions with the [Claude mod](using-claude-mod.md) connected and
Codex sessions on [shared Codex](using-shared-codex.md) compact on your home
machine and on an outpost. Plugin sessions compact on your home machine when
their driver supports native compaction. Standby closes a session without a
compaction path without compacting, and `attn agent close --self --compact`
refuses it, saying why. Compacting does not
help a crew member: mail starts a new session from its latest letter, and Attn
refuses to close a crew member's session, so standby leaves it open.

Attn resumes the tender of one seed at most 48 times in 24 hours, and the count
restarts when the daemon restarts. Sessions started for a crew member's seed mail count
against the seed's 48, and not against the member's `crew.mail_start_limit`. When Attn
can't resume the tender, it adds a note to the seed that names the reason and the
mail, and shows you a critical notification that opens the seed. The reasons
are a removed worktree, a conversation that is gone, a Claude session whose mod
doesn't connect after the resume, and the limit of 48. The mail stays unread.

### Why the garden uses a gardening metaphor

Every verb and noun in the garden maps to a term in a classic work tracker such
as Linear or Jira. Many of us have to work with such trackers, and we want to
keep agents from mistaking Attn seeds for Linear or Jira tickets. That is the
reason for the metaphor. If you tell an agent to harvest its seed, the agent
never moves your Linear ticket to done by mistake.

## Details

<details>
<summary>Does a delegated Claude ask me to trust its folder?</summary>

Only in a repository Attn doesn't know yet. When an agent session in the same profile has started a conversation in a repository with the same `origin`, on any machine, Attn records Claude's trust for that repository before the delegate launches, on the machine that runs the delegate, and notes it in the daemon log. It records the trust on the main repository, as answering the question in a worktree would, so new worktrees don't ask either. Remove the repository's entry from `.claude.json` to take the trust back.

In any other repository, Claude asks. The session waits for you, and the agent that delegated hears about it after 90 seconds. Answer the question once and the repository is known from then on. A launch that never got past the question doesn't count.
</details>

<details>
<summary>What happens to a delegate when the agent that started it closes?</summary>

The delegate keeps running with its seed, and you can still open, steer, and close the delegate. The delegate's chain shows the closed agent at the top as unavailable, and Cmd+Shift+Up (Ctrl+Alt+O on Linux) does nothing from the delegate. When a crew member started the delegate, the member's current session takes the delegator's place, so the chain and the jump lead to the member's new session.
</details>

<details>
<summary>How does the agent that started a delegate hear how it's going?</summary>

Through the garden. For each new delegation, Attn starts a watch on the delegate's seed. The watch covers every seed under that seed, including seeds planted later. The agent that delegated hears when any of those seeds moves, when a note on any of them is marked to ring, and when blocked work among them becomes ready. Plain notes do not notify the agent, and an agent never hears about its own moves. Attn merges several updates on one seed into one update until the agent reads it. `attn seed unwatch <id>` removes a watch. Restarting or resuming the delegation does not bring a removed watch back, but `attn seed watch <id>` does.
</details>

<details>
<summary>Does a delegate see my conversation with the agent that started it?</summary>

No. The delegate's first prompt names the delegate's seed and tells the delegate to read the seed with `attn seed show`. The prompt then adds the role's name and instructions, or your Anything else instructions if you wrote any. The delegating agent writes the brief in the seed, so the seed must hold anything the delegate needs from your conversation.
</details>

<details>
<summary>How does an agent pick a role, and what if no role fits?</summary>

The agent reads your table with `attn delegate roles`. The command lists only the roles that are on and have a default model. The agent picks the role whose "When to choose this role" text fits the work.

Within that role, the agent takes the alternative whose condition fits, and otherwise the default. An alternative without a condition is never picked.

When no role fits, the agent uses Anything else. When Anything else has no model, or the whole table is off, the agent picks the harness and model itself.

A role or model you ask for applies to that one delegation, because the table guides agents and does not bind them. Table changes apply from the next delegation. Running delegates keep the settings they started with.
</details>

<details>
<summary>What does "Add Attn roles" install, and what happens to the roles I already have?</summary>

It adds the Attn roles your table doesn't have yet, each without a model, as [Roles](#roles) describes. It also installs the attn-workflow skill for each harness you have, in `~/.claude/skills/attn-workflow` (Claude Code) and `~/.agents/skills/attn-workflow` (Codex). From then on Attn keeps those folders matching its own copy, so Attn replaces or removes anything you change or add inside them. Your own roles stay. When the name of one of your roles starts with an Attn role's name, Attn asks whether to add the Attn role beside yours or replace yours (replacing keeps your row's models and whether the role is on). `attn delegate roles install` doesn't ask. The command adds each missing Attn role beside yours, as `builder-2` when one of your roles already has the id `builder`.
</details>

<details>
<summary>Do Attn's roles change when Attn updates, and what does an editable copy change?</summary>

Attn maintains the text of its own roles and updates the text with releases, while the models and alternatives you set stay yours. **Make an editable copy** adds a custom role right below the original, named like the original with "(custom)" at the end, starting from the current text, models, alternatives, and whether the role is on. Attn never changes the copy after that. The original stays in your table, so turn off or delete the original if the copy should replace it.
</details>

<details>
<summary>What happens to a delegate's seed when the delegate closes?</summary>

The seed doesn't move. The seed keeps its state and still names the closed session as its tender, but the claim no longer blocks anyone, so any agent can tend or delegate the seed without `--force`. When an agent closes the delegate, Attn adds a note to the seed that says who closed it and why. Resuming the closed session makes the claim apply again. When the seed is on standby, the claim still applies, and mail for the seed resumes the session.
</details>

<details>
<summary>Can an agent close the delegates it started?</summary>

Yes. An agent can close itself and any agent below it in its delegation chain, delegates of delegates included. The Chief can close any agent in its profile. Attn refuses any other agent. When a crew member started the delegate, the member's later sessions can close it too. Each close by an agent needs a reason of up to 400 characters, and the ledger shows the reason on the closed session.
</details>

<details>
<summary>What do the seed states mean, and who can move a seed?</summary>

A planted seed is open and nobody tends it. A seed is growing while an agent or crew member tends it. A parked seed is on hold and has no tender. A harvested seed (done, with a summary of what got done) and a withered seed (dropped) stay closed until someone replants them. The garden board shows planted, growing, parked and closed seeds as Ready, In progress, Parked and Closed. Any agent can move a seed with `attn seed`. To take a seed that another agent tends, an agent needs `--force`, unless the agent taking it sits above the tender in the delegation chain or is the profile's Chief. In the garden, you can park, harvest, wither or replant any seed, even one an agent tends.
</details>

<details>
<summary>Can a delegate run on a different harness, or in another profile?</summary>

Any harness can delegate to any other, so a Claude Code agent can hand work to Codex, or to a harness a plugin adds, and back. Without a role or a named harness, the delegate runs on the same harness as the agent that started it, or Codex when that was a shell. A delegation stays in the profile of the agent that started it, so the delegate opens on one of that profile's desktops and a seed from another profile is refused. Your delegation roles are one table shared by every profile.
</details>
