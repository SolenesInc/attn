# Delegation

An agent can start another agent to work on something. The delegate is a full
session you can open and steer, in any harness.

If you are used to subagents, think of delegations as a subagent that you can
actually inspect and steer (it's just a first class agent after all).

## Delegating

Ask the Chief, or any agent, to delegate work to another agent. The delegate
opens next to the agent that started it, without taking your focus, or on
whichever desktop you ask for.

Attn keeps track of your delegation chain. Cmd+Shift+Up (Ctrl+Alt+O on Linux)
jumps from a delegate back to the agent that started it. To go the other way,
the chain button on an agent's sidebar row lists everyone in its chain; pick one
to go there.

## Steering a delegate

Open its tile and talk to it, like any agent. Agents can also message each
other, so you can ask the agent that started it to pass something along. Close
it like any agent when you're done.

The agent that delegated work will get updates as the delegate works, so it can
keep track of how work is going, or completely detach itself from it.

## Roles

When making a delegation, the agent picks:

- A coding harness, e.g. Claude Code or Codex.
- A model.
- A reasoning effort level.
- A way of working.

Attn lets you define the combination of all those via delegation roles,
configured in Settings > Delegation. For each role you set:

- When to use it. The agent reads this to pick a role, so describe the work it
  fits and what it's not for.
- What it runs on: harness, model and effort.
- Its instructions: how an agent in this role works.

A role can have alternatives: another model for a condition you describe, say
frontend work, or consequential work that needs a stronger model. "Anything
else" covers delegations no role fits.

Attn comes bundled with its own delegation roles you can opt into with "Add Attn
roles" in Settings > Delegation, or ask an agent to run
`attn delegate roles install`. Doing that also installs the attn-workflow skill for your harnesses. The
skill is necessary for the bundled roles, and any agent on your computer can use
it (although it's designed to be used inside Attn). To change how a bundled role
works, make an editable copy of it. Named instances refuse the user-global
skill install; you can still add custom roles there.

### Attn's roles

- Pathfinder: investigates a question, or works with you toward a plan.
- Prototyper: builds two or more options for a UI or a code interface, so you
  can compare them and pick one.
- Orchestrator: executes an agreed plan: starts Builders, reviews their work and
  verifies the result.
- Builder: implements an agreed change.
- Reviewer: reviews a change or a PR, only when you ask for one.

[building-with-roles](../tutorials/building-with-roles.md) walks through a whole
change with them.

## The garden

The garden powers the entire delegation and agentic planning system within Attn.
Every delegation is backed by a seed, which can be understood as a "ticket" as
in "a ticket in JIRA" or "a ticket in Linear" but within Attn. If you know
[beads](https://github.com/steveyegge/beads), the garden is beads embedded in
Attn.

Seeds can contain other seeds recursively, and each seed can be delegated to an
agent to work on, or a single agent can work across several seeds.

A seed with other seeds is called a plot. Seeds can be taken by agents (tended),
completed (harvested), dropped (withered), or put on hold (parked).

Cmd+Shift+T (Ctrl+Alt+G on Linux) opens the garden, with every seed and where it
stands.

### What's up with the gardening metaphor

Every verb and noun in the garden can be mapped to a classic work tracker like
Linear or JIRA. We understand many of us will have to work with such systems,
and we want to avoid the chance of agents mistaking Attn tickets with
Linear/JIRA tickets. Hence the metaphor: if you tell an agent to harvest its
seed, it would never mistakenly move your Linear ticket to done.

## Details

<details>
<summary>What happens to a delegate when the agent that started it closes?</summary>

It keeps running with its seed, and you can still open, steer and close it. Its delegation chain shows the closed agent at the top as unavailable, and Cmd+Shift+Up (Ctrl+Alt+O on Linux) does nothing from it. When a crew member such as the Chief started it, the member's current session takes the delegator's place, so after the Chief naps the chain and the jump lead to its new session.
</details>

<details>
<summary>How does the agent that started a delegate hear how it's going?</summary>

Through the garden. When an agent delegates, it watches the delegate's seed, and a watch covers every seed under it, including ones planted later. When a crew member such as the Chief delegates, the member holds the watch, so the updates still reach it after it naps. It hears when a note or a move on one of those seeds is marked to ring, and when blocked work there becomes ready; plain notes stay quiet, and nobody hears about their own moves. Several updates on one seed collapse into one until the agent reads it, and Attn rings the agent once it's at its prompt. `attn seed unwatch <id>` removes the watch, and restarts and resumes don't bring it back, while a new delegation starts a fresh one. A watch on a seed above it can still cover it.
</details>

<details>
<summary>Does a delegate see my conversation with the agent that started it?</summary>

No. Its first prompt names its seed and tells it to read it with `attn seed show`, then adds its role's name, instructions and stopping point (or your Anything else instructions, if you wrote any). The brief the delegating agent writes lives in the seed, so anything the delegate needs from your conversation has to be written there.
</details>

<details>
<summary>How does an agent pick a role, and what if no role fits?</summary>

It reads your table with `attn delegate roles`, which lists only the roles that are on and have a model set for their default, and picks the role whose "When to choose this role" text fits the work. Within that role it takes the alternative whose condition fits, or else the default, so an alternative without a condition is never picked. When no role fits it uses Anything else, and when Anything else has no model, or the whole table is off, the agent picks the harness and model itself. The table guides agents rather than locking them in, so a role or model you ask for applies to that one delegation, and table changes apply from the next delegation while running delegates keep what they started with.
</details>

<details>
<summary>What does "Add Attn roles" install, and what happens to the roles I already have?</summary>

It adds the Attn roles your table doesn't have yet, each without a model, so agents can't pick one until you set its model. It also installs the attn-workflow skill for each harness you have, in `~/.claude/skills/attn-workflow` (Claude Code), `~/.agents/skills/attn-workflow` (Codex and Pi). From then on Attn keeps those folders matching its own copy, so anything you change or add inside them gets replaced or removed. Your own roles stay, and when one's name starts with an Attn role's name, Attn asks whether to add the Attn role beside it or replace yours (replacing keeps your row's models and on/off state).
</details>

<details>
<summary>Do Attn's roles change when Attn updates, and what does an editable copy change?</summary>

Attn maintains the text of its own roles and updates it with releases, while the models and alternatives you set stay yours. "Make an editable copy" adds a custom role right below the original, named like it with "(custom)" at the end, starting from the current text, models, alternatives and on/off state. Attn never changes the copy after that. The original stays in your table, so turn it off or delete it if the copy should replace it.
</details>

<details>
<summary>What happens to a delegate's seed when the delegate closes?</summary>

The seed doesn't move. It keeps its state and still names the closed session as its tender, but the claim stops counting, so any agent can tend or delegate it without `--force`. When an agent did the closing, Attn adds a note to each seed the delegate was tending, with who closed it and why. Resuming the closed session makes its claim count again, and a delegate whose agent exited without being closed is treated the same way, with its seed updates waiting until you resume it.
</details>

<details>
<summary>Can an agent close the delegates it started?</summary>

Yes. An agent can close itself and the delegates it started, and the Chief can close any agent in its profile, while any other agent is refused. When a crew member started the delegate, the member's later sessions can close it too. Each agent close needs a reason of up to 400 characters, and the ledger shows it on the closed session.
</details>

<details>
<summary>What do the seed states mean, and who can move a seed?</summary>

Planted is open with nobody on it, tended (its state reads growing) means an agent or crew member has claimed it, and parked (its state reads dormant) means it's on hold with no claim. Harvested (done, with a summary of what got done) and withered (dropped) are closed until someone replants them. Any agent can move a seed with `attn seed`, but it needs `--force` to take one another agent or crew member is tending. In the garden you can park, harvest, wither or replant any seed, even one an agent is tending.
</details>

<details>
<summary>Can a delegate run on a different harness, or in another profile?</summary>

Any harness can delegate to any other, so a Claude Code agent can hand work to Codex or Pi (through its plugin), and back. Without a role or a named harness, the delegate runs on the same harness as the agent that started it, or Codex when that was a shell. A delegation stays in the profile of the agent that started it, so the delegate opens on one of that profile's desktops and a seed from another profile is refused. Your delegation roles are one table shared by every profile.
</details>
