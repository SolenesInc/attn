# Building with roles

Take one change from idea to delivery with Attn's roles: plan it with a
Pathfinder, and let an Orchestrator and its Builders deliver it.

Attn comes bundled with its own opinionated workflow: attn-workflow. Think of
[superpowers](https://github.com/obra/superpowers) or
[Matt Pocock's skills](https://github.com/mattpocock/skills), but tightly
integrated into Attn.

attn-workflow is designed to be the best process to create software or
infrastructure with agents. Use it as is, or ask your agents to customize the
delegation roles to your own needs. Attn keeps the attn-workflow skill itself up
to date, so change the roles, not the skill's files.

You can also use attn-workflow without delegations: ask the agent you're talking
to to work as a Pathfinder, and it takes the role right there.

## Before you start

Open **Settings → Delegation** and click **Add Attn roles**, or run
`attn delegate roles install`. Either action adds the five roles and installs
the attn-workflow skill. An agent in any role reads the skill to learn how to work.
See [delegation](../reference/delegation.md#roles) for what each role is.

The roles arrive without a model, and
[agents can't pick a role until it has one](../reference/delegation.md#roles).
Pick a harness and model for each role you want to use, in the **Runs on** column. You can also ask any agent to set them for you.

## Plan with a Pathfinder

Ask the agent you're talking to to delegate a Pathfinder on your idea, or to
work as one itself. Delegating gives the Pathfinder its own session, next to
the agent that started it.

The Pathfinder reads the code first, and then interviews you about the idea. As
the idea takes shape, it works through the open decisions with you in short
rounds, at most three questions at a time, each with its recommendation.

When something is risky or not clearly known, such as how a library behaves or
what a refactor would cost, the Pathfinder suggests a spike: a quick throwaway
build to find out. When a choice is easier to make by comparing options, such as
a screen or the shape of an interface, it suggests a Prototyper to build them
side by side. When the choice is costly to reverse and the Prototyper role has
more than one model, the Pathfinder can instead suggest one independent candidate per model.

When the interview ends, the Pathfinder puts the plan in the garden: one seed
for a single task, or a plot with a child per piece of work, saying which pieces
become which pull requests (or landings, in a repository that pushes straight to
its main branch). Before building starts, the Pathfinder tells you how it
understood the work and what it assumed, so you can correct the Pathfinder. It
then waits for you to choose between adjusting the plan and dispatching it.

## Hand it off

Tell the Pathfinder to dispatch work on the plan. It recommends an Orchestrator
when the work needs coordinating or reviewing, or a single Builder when the work
doesn't, and hands the seed over.

The Orchestrator starts a Builder for each piece, each on its own seed. A
Builder often runs on a cheaper model, though not always, so check your
delegation roles. When a Builder meets something unexpected, it brings the
problem to the Orchestrator instead of working around it.

## Follow along

You don't need to watch every step. The Orchestrator answers Builders'
questions, reviews each Builder's work itself, and brings you anything that
changes what you agreed. You may need to adjust your project's autonomy
settings, and your local skills, so the Orchestrator can move on its own.

When the Orchestrator has questions, it waits for you like any agent. With Asks
turned on in **Settings → Experimental**, it sends you an ask instead and keeps
working on everything else. Cmd+Shift+R (Ctrl+Alt+R on Linux) opens the Asks
drawer to answer the ask. You can still open any Builder and talk to it at any
time. The chain button on an agent's sidebar row shows everyone in the chain.

## Ship it

Agents take only the delivery steps you authorized. They push, open pull
requests and merge as far as you said and no further. Set up your
project's AGENTS.md or CLAUDE.md, and your local skills, to allow as much
autonomy and verification as possible to ensure quality.
