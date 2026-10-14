# Building with roles

Take one change from idea to pull requests with Attn's roles: plan it with a
Pathfinder, and let an Orchestrator and its Builders deliver it.

Attn comes bundled with its own opinionated workflow: attn-workflow. Think of
[superpowers](https://github.com/obra/superpowers) or
[Matt Pocock's skills](https://github.com/mattpocock/skills), but tightly
integrated into Attn.

It is designed to be the best process to create software or infrastructure with
agents. Use it as is, or ask your agents to customize the delegation roles to
your own needs. Attn keeps the attn-workflow skill itself up to date, so change
the roles, not the skill's files.

You can also use attn-workflow without delegations: ask the agent you're talking
to to work as a Pathfinder, and it takes the role right there.

## Before you start

Add Attn's roles in Settings > Delegation with "Add Attn roles", or let the
Chief do it for you. This also installs the attn-workflow skill, which is how
each role knows how to work. See [delegation](../reference/delegation.md#roles)
for what each role is.

## Plan with a Pathfinder

Ask the Chief to start a Pathfinder on your idea, or ask the agent you're
talking to to work as one.

The Pathfinder reads the code first, and then interviews you about the idea. As
the idea takes shape, it works through the open decisions with you in short
rounds, a few questions at a time, each with its recommendation.

When something is risky or not clearly known, like how a library behaves or what
a refactor would cost, it suggests a spike: a quick throwaway build to find out.
When a choice is easier to make by comparing options, like a screen or the shape
of an interface, it suggests a Prototyper to build them side by side (or
multiple to create parallel solutions!).

When you're done, the plan lands in the garden: one seed for a single task, or a
plot with a child per piece of work, saying which pieces become which pull
requests. Before anything gets built, the Pathfinder tells you how it understood
the work and what it assumed, so you can correct it.

## Hand it off

Tell the Pathfinder to dispatch work on the plan. It recommends an Orchestrator
when the work needs coordinating or reviewing, or a single Builder when it
doesn't, and hands the seed over.

The Orchestrator starts a Builder for each piece, each on its own seed, often on
a cheaper model (but not necessarily, check your delegation roles!). Builders
bring surprises to the Orchestrator instead of working around them.

## Follow along

You don't need to watch every step. The Orchestrator answers Builders'
questions, reviews each Builder's work itself, and brings you anything that
changes what you agreed. You may need to adjust your project's autonomy
settings, and your local skills, so the Orchestrator can move on its own. When
it has questions or is unsure, it asks you like any agent waiting for you. You
can still open any Builder and talk to it at any time.

## Ship it

Agents only take the delivery steps you authorized, so pushing, opening pull
requests and merging happen as far as you said and no further. Set up your
project's AGENTS.md or CLAUDE.md, and your local skills, to allow as much
autonomy and verification as possible to ensure quality.
