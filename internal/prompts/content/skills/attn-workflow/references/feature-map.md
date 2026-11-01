# Feature map

Principles this process relies on: [Prove it works](../principles/prove-it-works.md), [Build the lever](../principles/build-the-lever.md), [Derived things must earn their place](../principles/derived-things-must-earn-their-place.md), [Experience first](../principles/experience-first.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Use this process to build a feature map for a product: a skill in the repository that tells an agent how to start the product on an isolated instance, drive each feature through every way in, and keep the evidence. With the map, an agent checks a change the way the product is used, through every way in, without rediscovering how to drive it.

Name the skill `drive-<app>`, also when the user asks for a verification skill, and put the skill in the skill folder the repository already uses, such as `.claude/skills/` or `.agents/skills/`. Write the skill for an agent that reads it with no prior context, in the middle of a task, and has never seen the product.

## Learn how the product runs

Answer these from the codebase, and ask the user only what you cannot observe:

- **Audiences.** Who uses the product, and through which ways in. People use the product's interface, such as screens, keys, menus, or a CLI in their own shell. Agents are the agents that the product runs or serves, plus other programs that call the product. Agents use the product's CLI, the product's API, or the prompts the product sends them. Maintainers are whoever changes the product. Write the skill for them. A feature often serves several audiences.
- **Run.** How the product starts locally, from the repository's own documented commands. Note ports, environment variables, data folders, seed data and sign-in.
- **Isolate.** How to start an instance that shares nothing with the user's own instance. The new instance needs its own data folder, ports, and profile. If two instances cannot run side by side, write that in the skill.
- **Outside boundaries.** What the product reaches outside itself, such as agent binaries, external services and the network, and the fakes the repository already has for them. When a boundary has no fake, ask the user whether to build one or to leave the features behind the boundary unreachable, with the reason in the Gotchas of those features. Never point an instance at a real external service without the user's approval.
- **Drive.** How an agent reaches each audience's way in. Use the repository's own harnesses first, such as browser drivers, terminal harnesses, protocol clients and end-to-end tests. Fall back to a generic recipe only where none exists.
- **Observe.** What evidence each way in gives: screenshots, terminal output, response bodies, exit codes, logs, and a second view of stored state.

If the checkout does not build or start, fix it or report exactly why before you write the skill. A skill written against a broken base contains wrong steps.

## Ask the user what the features are

The user owns what each feature means. Before you write any description, ask them which features exist and what each is for, in their words. Give them a suggested list built from the source, so they can answer by correcting it: the ways in with their paths, candidate sub-features, and where the tests live. A subagent can build that list.

When the user asks you to draft instead, draft from their own existing words, such as product docs, the glossary or the README, and present it as a draft for their review. Match the style of the first map when you add later features. Follow the existing entries, and make each addition a small diff for the user to review.

## Write the skill

Write `SKILL.md` with frontmatter: `name: drive-<app>`, and a `description` that names the product, its ways in, and when to use the skill. Without frontmatter the skill never registers. Then write these sections from what you found, with real commands and no placeholders:

- **Launch.** The commands that start a fresh isolated instance with fakes at the outside boundaries, and how to tell it is ready to use, not only that a process answers. Ready includes migrations and seed data. For a short-lived CLI, build the CLI once in Launch and run each drive in its own isolated session.
- **Doctor.** One read-only command that reports whether the instance is worth driving. It confirms four things: the process is up, the process runs this checkout's build, the process owns its port and data folder, and the fakes are the ones answering. Run it first, and again whenever something looks wrong.
- **Drive.** For each audience's way in, the harness that drives it and what it reaches. Prefer stable handles, such as accessible names, test ids, command names and entity ids, over coordinates and tab order. When the product runs on the user's machine, drive it without taking their focus or covering their screen. A way in that the harness cannot drive without taking the user's focus or covering their screen is a gap in the harness. Fix the harness and never skip the step.
- **Evidence.** Drive the real path, never internal setters or test-only endpoints. Capture the action and the resulting state, and check side effects from a second view. Use fakes only where the product already meets the outside. When a safe mode, such as a dry run, stands in for the real path, observe what the safe mode skips instead of trusting its name. Name the folder where evidence goes.
- **Cleanup.** Stop what the run started, never by process name or pattern. Remove instances and scratch state, never the evidence.
- **The feature map.** Read `features/README.md`, then the files of the features a change touches, and drive every way in and every context listed there that applies. Update a feature's file in the same change that adds, removes or alters a way into it, and whenever you find a trap or a test the file lacks.

Make every helper script the skill ships executable, and show in the skill body how to run it.

## Write the map

Put the map in the skill folder, as `features/README.md` and one file per feature. The README has these parts:

- What the map is for, and its audiences.
- The baseline preconditions: the fresh isolated instance from Launch, with the doctor passing. If a feature needs realistic data, write that in its preconditions and copy real data out, read-only. Never drive the user's real instance.
- The driving conventions: which harness drives each audience's way in, the stable handles to prefer, and the real signals to wait on instead of sleeps.
- The proof rules: the evidence to capture for each way in, and how to report a way in you could not reach, with the command you tried and the precondition that failed. A proof through one convenient way in is incomplete while the file lists others.
- The feature entry contract below.
- The contexts: the conditions this product's features can behave differently under, such as restarts, other platforms, remote machines, tenants, or profiles.
- The index: one line per feature, with the audiences it serves and what it is for in the user's words.

Each feature file has these sections, in this order:

1. `# <Name>`, then one paragraph from the user's point of view: what the feature is and what it is for.
2. `## Sub-features`: short ids, one line each.
3. `## How to get to it`, with `### People` and `### Agents` always present. Write "None." when an audience has no way in, so the gap shows. Add `### Triggers` when something starts the feature without anyone invoking it, such as a schedule or an event. Name the source file for each way in.
4. `## Driving it`: the preconditions, then bullets that each pair one action with an exact command and what you observe. Cover every way in at least once, and end with a **Proof** bullet that names what to capture.
5. `## Contexts`: only the contexts that apply, each saying how the feature behaves under it.
6. `## Tests`: pointers to the committed tests, grouped by the repository's own kinds of test. Pointers only, never descriptions.
7. `## Gotchas`: traps that waste or invalidate a run, and what working-but-wrong looks like for the feature.

When you list the ways in for each audience, look for gaps: an action with no way to undo it, or with no view where the other audience can see it, such as an agent action that no person can see in the app. Report each gap to the user.

Use the terms in the repository's glossary or domain docs, and link to them instead of defining the terms again. Links to code are plain pointers. Do not add ownership globs, completeness checks, or links derived from test coverage. The user does not have the problems those additions solve.

When the baseline lacks a precondition that a recipe needs, the recipe fails, and you write the reason in the feature's Gotchas. Never weaken the recipe to make it pass.

## Prove the skill

Run the skill's own instructions once on an isolated instance: launch, run the doctor, drive one feature through each audience's way in that the skill can reach, capture the evidence, clean up, and confirm the evidence survived cleanup. Fix what fails, and run cleanup after each failed attempt so nothing stays running. A skill that was never run is a draft.

Two traps are common. When a trap applies, put a guard against it in the skill's Launch section:

- A build in the same checkout, by you or another agent, can replace the binary under a run. Keep one live driver per checkout.
- An agent that drives a product it runs inside can inherit that product's identity variables. The instance then treats the agent's shell as one of its own sessions. Check what the instance's environment sets, and clear what the shell inherits.

When you ran the skill in a stand-in, such as a local stack instead of a remote machine, list each step the stand-in did that a real run would not. Those steps stay unproven until a real run covers them.

## Keep it current

The map is a derived thing, so keep it current as [Derived things](derived-things.md) describes. Nothing generates the map or checks it mechanically, so agents check it at steps they already take. The skill's feature map section and [Implementation](implementation.md) tell agents to update a feature's file in the same change that adds, removes, or alters a way into the feature, and [Review](review.md) tells reviewers to confirm that the change did. Do not write a separate `verify-<thing>` skill for the map.

When the map and the product disagree, decide which one is wrong. Fix drift in the map. Report a regression in the product, and never edit the map to match the regression.

Some drift happens outside those steps, such as a commit made without them, or a dependency or an outside service that changes on its own. Suggest that the user set up an attn automation that drives every feature in the map on a schedule, fixes the drift it finds, and reports the regressions.

## Hand over the skill

Hand over the skill, the evidence from the run, the gaps you found, what the stand-in left unproven, and the automation you suggested.
