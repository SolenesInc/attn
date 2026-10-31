# Delegation

An agent can start another agent to work on something. The delegate is a full
session you can open and steer, in any harness.

If you are used to subagents, think of delegations as a subagent that you can
actually inspect and steer (it's just a first class agent after all).

## Delegating

Ask any agent to delegate work to another agent. The delegate opens next to the
agent that started it, without taking your focus, or on whichever desktop you
ask for.

Attn keeps track of your delegation chain. Cmd+Shift+Up (Ctrl+Alt+O on Linux)
jumps from a delegate back to the agent that started it. To go the other way,
the chain button on an agent's sidebar row lists everyone in its chain; pick one
to go there.

### The `attn delegate` command

Agents delegate with `attn delegate`, and you can run it yourself from a shell
inside Attn. Every delegation works on a seed in [the garden](#the-garden):
either a new one made from a brief, or one that already exists.

```sh
attn delegate --brief "Find why the export times out" --role pathfinder \
  --cwd ~/src/shop --reuse-checkout --branch main
attn delegate --seed s-7k3f9m --role builder \
  --cwd ~/src/shop --new-worktree --branch fix-export --from origin/main
```

The flags you'll reach for most:

- `--brief TEXT` or `--brief-file PATH` plants a new seed for the work. `--seed
  ID` uses an existing one instead. Pass exactly one of the three.
- `--cwd PATH` is the folder the delegate works in. It's always required.
- Inside a Git repository, also pick a checkout: `--reuse-checkout --branch NAME`
  works in the checkout that's already on that branch, `--new-worktree --branch
  NAME --from REF` makes a new branch and worktree, and `--new-worktree
  --existing-branch NAME` makes a worktree for a local branch you already have.
- `--role ID` uses one of your [delegation roles](#roles), and `--choice ID` one
  of its alternatives. `--fallback` uses "Anything else". Without any of them,
  `--model` is required (`--model default` takes the harness default).
- `--agent NAME`, `--model`, `--effort` and `--provider` pick the harness and
  model for this one delegation, on top of a role or without one.
- `--desktop` opens the delegate on another desktop of your profile: its
  shortcut digit, its name or its id.
- `--name` names the session; it defaults to the brief's or seed's title.
- `--priority` puts the delegate's turns first in your queue.

`attn delegate --help` lists every flag, and `attn delegate roles` prints the
roles agents choose from.

## Steering a delegate

Open its tile and talk to it, like any agent. Close it like any agent when
you're done.

The agent that delegated work gets updates as the delegate works, so it can
follow along, or stop following with `attn seed unwatch`.

### Agents messaging each other

Agents can message each other with `attn agent msg`, so you can ask the agent
that started a delegate to pass something along. The target can be a session,
a crew member, or a seed id, which reaches whoever tends that seed now (or
next, if nobody does yet). The message waits in the recipient's inbox, and Attn
lets the recipient know it's there.

### Handing work to a fresh agent

A handover gives a seed, and its claim, to a new agent. The agent passing it on
runs `attn delegate --seed <id> --handover -m "<what comes next>"`, with a
role and folder like any delegation. The note goes on the seed, and the
previous agent keeps running until you or it closes it.

Handovers are how a Pathfinder passes an agreed plan to an Orchestrator or a
Builder: the plan stays in the seed, and the new agent reads it from there.

### Asks (experimental)

Asks are off by default. Turn them on in Settings > Experimental > Asks
(`asks_enabled`); agents launched after that can send you questions with
`attn ask` instead of stopping to wait for you.

Cmd+Shift+R (Ctrl+Alt+R on Linux) opens the Asks drawer, where you answer them
without opening the agent's terminal. Your answer is typed into the agent's
conversation as your message. When the ask is about a seed, the answer goes to
whoever tends that seed by then, and the seed keeps it as a note.

## Roles

When making a delegation, the agent picks:

- A coding harness, e.g. Claude Code or Codex.
- A model.
- A reasoning effort level.
- A way of working.

Attn lets you define the combination of all those via delegation roles,
configured in Settings > Delegation. For each role you set:

- When to choose this role. The agent reads this to pick a role, so describe
  the work it fits and what it's not for.
- What it runs on: harness, model and effort.
- Its instructions, and when it stops.

A role can have alternatives: another model for a condition you describe, say
frontend work, or consequential work that needs a stronger model. "Anything
else" covers delegations no role fits. The switch at the top of Settings >
Delegation turns the whole table off, and then agents pick harness and model on
their own.

Attn comes bundled with its own delegation roles you can opt into with "Add Attn
roles" in Settings > Delegation, or with `attn delegate roles install`. Each one
arrives without a model, so pick one
for each role you want agents to use. Adding them also installs the attn-workflow
skill for your harnesses. The skill is necessary for the bundled roles, and any
agent on your computer can use it (although it's designed to be used inside
Attn). To change how a bundled role works, use "Make an editable copy".

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
survive restarts, and an unchanged observation doesn't ping the agent again.
Feedback already on the PR at the first check is the baseline; changing modes
keeps that history, and unwatching then watching again starts a new baseline.

| Mode | Ready when |
|---|---|
| `green` (default) | The PR is open, not a draft, and GitHub reports it `CLEAN` or `HAS_HOOKS`. |
| `codex` | `green`, plus the Codex app's 👍 and no 👀 after a 30-second settling window. Reactions are best effort, since GitHub doesn't tie them to a commit. |
| `formal-review` | GitHub's overall approval. `--reviewer <login>` requires that reviewer's current verdict instead. |

Comments, threads, requested changes and failed checks are feedback the agent
hears about, not extra conditions for ready. A watch never changes anything on
GitHub and never grants permission to merge.

`attn pr status` lists the watches, and `attn pr unwatch <url>` stops one and
clears its unread notifications. Closing or merging the PR ends its watch. For
a script or a terminal that should block until the PR is ready, use
`attn pr wait-ready <pr> --repo <owner/repo> --mode <mode>`; its `--help`
explains reviewers, baselines, resuming and exit codes.

## The garden

The garden is Attn's work tracker, and every delegation works on a seed in it.
A seed is a ticket, like one in Jira or Linear, kept inside Attn. If you know
[beads](https://github.com/steveyegge/beads), the garden works much like it.

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

### What's up with the gardening metaphor

Every verb and noun in the garden can be mapped to a classic work tracker like
Linear or Jira. We understand many of us will have to work with such systems,
and we want to avoid the chance of agents mistaking Attn tickets with
Linear or Jira tickets. Hence the metaphor: if you tell an agent to harvest its
seed, it would never mistakenly move your Linear ticket to done.

## Details

<details>
<summary>What happens to a delegate when the agent that started it closes?</summary>

It keeps running with its seed, and you can still open, steer and close it. Its delegation chain shows the closed agent at the top as unavailable, and Cmd+Shift+Up (Ctrl+Alt+O on Linux) does nothing from it. When a crew member started it, the member's current session takes the delegator's place, so the chain and the jump lead to the member's new session.
</details>

<details>
<summary>How does the agent that started a delegate hear how it's going?</summary>

Through the garden. Each new delegation starts a watch on the delegate's seed, and a watch covers every seed under it, including ones planted later. The agent that delegated hears when a seed there moves, when a note there is marked to ring, and when blocked work there becomes ready; plain notes stay quiet, and nobody hears about their own moves. Several updates on one seed collapse into one until the agent reads it. `attn seed unwatch <id>` removes a watch, and restarting or resuming the delegation doesn't bring it back; `attn seed watch <id>` does.
</details>

<details>
<summary>Does a delegate see my conversation with the agent that started it?</summary>

No. Its first prompt names its seed and tells it to read it with `attn seed show`, then adds its role's name, instructions and stopping point (or your Anything else instructions, if you wrote any). The brief the delegating agent writes lives in the seed, so anything the delegate needs from your conversation has to be written there.
</details>

<details>
<summary>How does an agent pick a role, and what if no role fits?</summary>

It reads your table with `attn delegate roles`, which lists only the roles that are on and have a model set for their default, and picks the role whose "When to choose this role" text fits the work. Within that role it takes the alternative whose condition fits, or else the default, so an alternative without a condition is never picked. When no role fits it uses Anything else, and when Anything else has no model, or the whole table is off, the agent picks the harness and model itself. The table guides agents rather than locking them in, so a role or model you ask for applies to that one delegation. Table changes apply from the next delegation, while running delegates keep what they started with.
</details>

<details>
<summary>What does "Add Attn roles" install, and what happens to the roles I already have?</summary>

It adds the Attn roles your table doesn't have yet, each without a model, so agents can't pick one until you set its model. It also installs the attn-workflow skill for each harness you have, in `~/.claude/skills/attn-workflow` (Claude Code) and `~/.agents/skills/attn-workflow` (Codex and Pi). From then on Attn keeps those folders matching its own copy, so anything you change or add inside them gets replaced or removed. Your own roles stay, and when one's name starts with an Attn role's name, Attn asks whether to add the Attn role beside it or replace yours (replacing keeps your row's models and on/off state). `attn delegate roles install` doesn't ask. It adds each missing Attn role beside yours, as `builder-2` when one of yours already has the id `builder`.
</details>

<details>
<summary>Do Attn's roles change when Attn updates, and what does an editable copy change?</summary>

Attn maintains the text of its own roles and updates it with releases, while the models and alternatives you set stay yours. "Make an editable copy" adds a custom role right below the original, named like it with "(custom)" at the end, starting from the current text, models, alternatives and on/off state. Attn never changes the copy after that. The original stays in your table, so turn it off or delete it if the copy should replace it.
</details>

<details>
<summary>What happens to a delegate's seed when the delegate closes?</summary>

The seed doesn't move. It keeps its state and still names the closed session as its tender, but the claim stops counting, so any agent can tend or delegate it without `--force`. When an agent did the closing, the seed gets a note with who closed it and why. Resuming the closed session makes its claim count again.
</details>

<details>
<summary>Can an agent close the delegates it started?</summary>

Yes. An agent can close itself and any agent below it in its delegation chain, delegates of delegates included, and the Chief can close any agent in its profile, while any other agent is refused. When a crew member started the delegate, the member's later sessions can close it too. Each agent close needs a reason of up to 400 characters, and the ledger shows it on the closed session.
</details>

<details>
<summary>What do the seed states mean, and who can move a seed?</summary>

Planted is open with nobody on it, and growing means an agent or crew member has claimed (tended) it. Parked means it's on hold with no claim; some places label it dormant. Harvested (done, with a summary of what got done) and withered (dropped) are closed until someone replants them. The garden board shows these as Ready, In progress, Parked and Closed. Any agent can move a seed with `attn seed`, but it needs `--force` to take one another agent is tending, unless it sits above that agent in the delegation chain or is the profile's Chief. In the garden you can park, harvest, wither or replant any seed, even one an agent is tending.
</details>

<details>
<summary>Can a delegate run on a different harness, or in another profile?</summary>

Any harness can delegate to any other, so a Claude Code agent can hand work to Codex or Pi (through its plugin), and back. Without a role or a named harness, the delegate runs on the same harness as the agent that started it, or Codex when that was a shell. A delegation stays in the profile of the agent that started it, so the delegate opens on one of that profile's desktops and a seed from another profile is refused. Your delegation roles are one table shared by every profile.
</details>
