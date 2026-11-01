# Delegation

An agent can start another agent to work on something. The delegate is a full
session you can open and steer, in any harness.

If you are used to subagents, think of a delegation as a subagent that you can
inspect and steer, because the delegate is a first-class agent.

## Delegating

Ask any agent to delegate work to another agent. The delegate opens next to the
agent that started it, without taking your focus, or on whichever desktop you
ask for.

Attn keeps track of your delegation chain. Cmd+Shift+Up (Ctrl+Alt+O on Linux)
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

`attn delegate --help` lists every flag, and `attn delegate roles` prints the
roles agents choose from.

## Steering a delegate

Open the delegate's tile and talk to the delegate, like any agent. When you're
done, close the delegate like any agent.

The agent that delegated work gets updates as the delegate works, so the agent
can follow along. The agent can stop following with `attn seed unwatch`.

### Agents messaging each other

Agents can message each other with `attn agent msg`, so you can ask the agent
that started a delegate to pass something along. The target can be a session,
a crew member, or a seed id. A seed id reaches whoever tends that seed now, or
next if nobody does yet. The message waits in the recipient's inbox, and Attn
lets the recipient know it's there.

### Handing work to a fresh agent

A handover gives a seed, and the seed's claim, to a new agent. The agent handing
over the seed runs `attn delegate --seed <id> --handover -m "<what comes next>"`,
with a role and folder like any delegation. The note goes on the seed. The
previous agent keeps running until you close it or it closes itself.

Handovers are how a Pathfinder passes an agreed plan to an Orchestrator or a
Builder: the plan stays in the seed, and the new agent reads it from there.

### Asks (experimental)

Asks are off by default. Turn them on in **Settings → Experimental → Asks**
(`asks_enabled`). Agents launched after that can send you questions with
`attn ask` instead of stopping to wait for you.

Cmd+Shift+R (Ctrl+Alt+R on Linux) opens the Asks drawer, where you answer them
without opening the agent's terminal. Attn types your answer into the
agent's conversation as your message. When the ask is about a seed, the answer
goes to whoever tends that seed by then, and Attn adds the answer to the seed as
a note.

## Roles

When making a delegation, the agent picks:

- A coding harness, such as Claude Code or Codex.
- A model.
- A reasoning effort level.
- A way of working.

In **Settings → Delegation**, you define delegation roles, each a combination of
those choices. For each role you set:

- When to choose this role. The agent reads this to pick a role, so describe
  the work the role fits and what it's not for.
- What it runs on: harness, model and effort.
- Its instructions: how it works and when it stops.

A role can have alternatives: another model for a condition you describe, such
as frontend work, or consequential work that needs a stronger model. "Anything
else" covers delegations no role fits. The switch at the top of **Settings →
Delegation** turns the whole table off, and then agents pick harness and model on
their own.

Attn bundles its own delegation roles. Add them with "Add Attn roles" in
**Settings → Delegation**, or with `attn delegate roles install`. Each role arrives
without a model, and agents can't pick a role until it has one, so pick a model
for each role you want agents to use. Adding the
roles also installs the attn-workflow skill for your harnesses. The
bundled roles need the skill. Any agent on your computer can use the skill,
although the skill is built for use inside Attn. To change how a bundled role works, use "Make
an editable copy".

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
hear about it in its inbox: readiness, failed checks, review findings, human
comments and replies, reopened review threads, and monitoring outages. Watches
survive restarts, and Attn doesn't notify the agent again about an observation
that hasn't changed. Feedback already on the PR at the first check is the
baseline. Changing modes keeps that history. If you unwatch and then watch
again, the watch starts a new baseline.

| Mode | Ready when |
|---|---|
| `green` (default) | The PR is open, not a draft, and GitHub reports it `CLEAN` or `HAS_HOOKS`. |
| `codex` | `green`, plus the Codex app's 👍 and no 👀 after a 30-second settling window. Reactions are best effort, since GitHub doesn't tie them to a commit. |
| `formal-review` | GitHub's overall approval. `--reviewer <login>` requires that reviewer's current verdict instead. |

Comments, threads, requested changes and failed checks are feedback the agent
hears about, not extra conditions for ready. A watch never changes anything on
GitHub and never grants permission to merge.

`attn pr status` lists the watches, and `attn pr unwatch <url>` stops a watch and
clears the watch's unread notifications. Closing or merging the PR ends the
watch. For a script or a terminal that should block until the PR is ready, use
`attn pr wait-ready <pr> --repo <owner/repo> --mode <mode>`. The command's
`--help` explains reviewers, baselines, resuming, and exit codes.

## The garden

The garden is Attn's work tracker, and every delegation works on a seed in the
garden. A seed is a ticket, like one in Jira or Linear, kept inside Attn. If you
know [beads](https://github.com/steveyegge/beads), the garden works much like
beads.

Seeds can contain other seeds recursively, and each seed can be delegated to an
agent to work on, or a single agent can work across several seeds.

A seed with other seeds is called a plot. Seeds can be taken by agents (tended),
completed (harvested), dropped (withered), or put on hold (parked).

Cmd+Shift+T (Ctrl+Alt+G on Linux) opens the garden, with every seed and where it
stands.

Agents work the garden with `attn seed`: `plant`, `tend`, `park`, `harvest`,
`wither`, `replant`, `note`, `show` and `ls` are the everyday verbs.
`attn seed --help` lists them all, and `attn seed guide` explains how agents
write seeds and plots.

### Why the garden uses a gardening metaphor

Every verb and noun in the garden maps to a term in a classic work tracker such
as Linear or Jira. Many of us have to work with such trackers, and we want to
keep agents from mistaking Attn seeds for Linear or Jira tickets. That is the
reason for the metaphor. If you tell an agent to harvest its seed, the agent
never moves your Linear ticket to done by mistake.

## Details

<details>
<summary>What happens to a delegate when the agent that started it closes?</summary>

The delegate keeps running with its seed, and you can still open, steer, and close the delegate. The delegate's chain shows the closed agent at the top as unavailable, and Cmd+Shift+Up (Ctrl+Alt+O on Linux) does nothing from the delegate. When a crew member started the delegate, the member's current session takes the delegator's place, so the chain and the jump lead to the member's new session.
</details>

<details>
<summary>How does the agent that started a delegate hear how it's going?</summary>

Through the garden. Each new delegation starts a watch on the delegate's seed, and a watch covers every seed under the delegate's seed, including seeds planted later. The agent that delegated hears when a seed there moves, when a note there is marked to ring, and when blocked work there becomes ready. Plain notes stay quiet, and nobody hears about their own moves. Attn merges several updates on one seed into one update until the agent reads it. `attn seed unwatch <id>` removes a watch. Restarting or resuming the delegation does not bring the watch back, but `attn seed watch <id>` does.
</details>

<details>
<summary>Does a delegate see my conversation with the agent that started it?</summary>

No. The delegate's first prompt names the delegate's seed and tells the delegate to read the seed with `attn seed show`. The prompt then adds the role's name and instructions, or your Anything else instructions if you wrote any. The delegating agent writes the brief in the seed, so the seed must hold anything the delegate needs from your conversation.
</details>

<details>
<summary>How does an agent pick a role, and what if no role fits?</summary>

The agent reads your table with `attn delegate roles`, which lists only the roles that are on and have a model set for their default, and picks the role whose "When to choose this role" text fits the work. Within that role the agent takes the alternative whose condition fits, or else the default, so an alternative without a condition is never picked. When no role fits it uses Anything else, and when Anything else has no model, or the whole table is off, the agent picks the harness and model itself. The table guides agents rather than locking them in, so a role or model you ask for applies to that one delegation. Table changes apply from the next delegation, while running delegates keep what they started with.
</details>

<details>
<summary>What does "Add Attn roles" install, and what happens to the roles I already have?</summary>

It adds the Attn roles your table doesn't have yet, each without a model, so agents can't pick a role until you set its model. It also installs the attn-workflow skill for each harness you have, in `~/.claude/skills/attn-workflow` (Claude Code) and `~/.agents/skills/attn-workflow` (Codex and Pi). From then on Attn keeps those folders matching its own copy, so Attn replaces or removes anything you change or add inside them. Your own roles stay. When the name of one of your roles starts with an Attn role's name, Attn asks whether to add the Attn role beside yours or replace yours (replacing keeps your row's models and on/off state). `attn delegate roles install` doesn't ask. The command adds each missing Attn role beside yours, as `builder-2` when one of your roles already has the id `builder`.
</details>

<details>
<summary>Do Attn's roles change when Attn updates, and what does an editable copy change?</summary>

Attn maintains the text of its own roles and updates the text with releases, while the models and alternatives you set stay yours. "Make an editable copy" adds a custom role right below the original, named like the original with "(custom)" at the end, starting from the current text, models, alternatives, and on/off state. Attn never changes the copy after that. The original stays in your table, so turn off or delete the original if the copy should replace it.
</details>

<details>
<summary>What happens to a delegate's seed when the delegate closes?</summary>

The seed doesn't move. The seed keeps its state and still names the closed session as its tender, but the claim stops counting, so any agent can tend or delegate the seed without `--force`. When an agent closed the delegate, Attn adds a note to the seed with who closed it and why. Resuming the closed session makes the claim count again.
</details>

<details>
<summary>Can an agent close the delegates it started?</summary>

Yes. An agent can close itself and any agent below it in its delegation chain, delegates of delegates included, and the Chief can close any agent in its profile, while any other agent is refused. When a crew member started the delegate, the member's later sessions can close it too. Each agent close needs a reason of up to 400 characters, and the ledger shows the reason on the closed session.
</details>

<details>
<summary>What do the seed states mean, and who can move a seed?</summary>

Planted is open with nobody on it, and growing means an agent or crew member has claimed (tended) the seed. Parked means the seed is on hold with no claim. Some places label it dormant. Harvested (done, with a summary of what got done) and withered (dropped) are closed until someone replants them. The garden board shows these as Ready, In progress, Parked, and Closed. Any agent can move a seed with `attn seed`, but an agent needs `--force` to take a seed that another agent is tending, unless the agent sits above that agent in the delegation chain or is the profile's Chief. In the garden you can park, harvest, wither or replant any seed, even one an agent is tending.
</details>

<details>
<summary>Can a delegate run on a different harness, or in another profile?</summary>

Any harness can delegate to any other, so a Claude Code agent can hand work to Codex or Pi (through Pi's plugin), and back. Without a role or a named harness, the delegate runs on the same harness as the agent that started it, or Codex when that was a shell. A delegation stays in the profile of the agent that started it, so the delegate opens on one of that profile's desktops and a seed from another profile is refused. Your delegation roles are one table shared by every profile.
</details>
