---
name: attn-workflow
description: Use when an assigned role refers to this skill, the user asks you to work as a Pathfinder, Prototyper, or Orchestrator in the current conversation, or the user requests its processes for investigation, discussion, alignment, debugging, prototyping, interface design, planning, implementation, refactoring, performance work, review, orchestration, or correcting a kind of mistake that keeps coming back.
---

# Attn workflow

## Start

Match the task to one process in the table, read its reference and the principles it names, and load further references as the work requires. When you leave out a part of the process, say which part and why, in the conversation and on the seed when there is one, so the omission is visible instead of silent. A follow-up in the same conversation continues the same process; re-match when the user names a new task. When the assignment arrived through a noisy channel, such as a long discussion or a vague report, restate the problem and the outcome in your own words before acting, so a misreading costs one message instead of one wrong change.

| Task | Reference |
|---|---|
| Investigate a question or develop an idea through grounded discussion | [Discuss](references/discuss.md) |
| Test shared understanding, assumptions, and boundaries | [Align](references/align.md) |
| Investigate unexpected behavior through evidence and experiments | [Debugging](references/debugging.md) |
| Build options for a UI or the shape of an interface, compare them, and recommend one | [Prototype](references/prototype.md) |
| Shape, compare, or review an interface, from a function to a protocol | [Design](references/design.md) |
| Write a substantial implementation plan in the garden and arrange its handoff | [Planning](references/planning.md) |
| Implement an agreed brief or plan and address review findings | [Implementation](references/implementation.md) |
| Change structure or shape while preserving behavior | [Refactoring](references/refactoring.md) |
| Find what makes something slow, large or busy when it should be idle, and make it measurably better, once or against a target | [Perf](references/perf.md) |
| Review a PR or changes against a plan, including behavioral verification | [Review](references/review.md) |
| Decide whether a derived thing, such as a doc, a comment or a generated file, should exist, and build what keeps it current | [Derived things](references/derived-things.md) |
| Make a kind of mistake that keeps coming back impossible to repeat in the repository | [Correction](references/correction.md) |
| Delegate agreed work, advise implementers, and judge completion | [Orchestration](references/orchestration.md) |

When the user asks you to work as one of these roles in the current conversation, take the role yourself, with the user present:

- **Pathfinder**: use [Discuss](references/discuss.md) to investigate or develop the idea with the user, [Planning](references/planning.md) when the outcome is a plan, and [Align](references/align.md) before a consequential step, such as dispatching a Builder or Orchestrator, to check that you and the user understand the work the same way. When a decision is easier to make by comparing options, such as a UI or the shape of an interface, suggest prototypes; when the user agrees, delegate them to the Prototyper role if `attn delegate roles` lists it, one delegation per model when the decision is costly to reverse and the role offers more than one, as [Prototype](references/prototype.md) describes under Independent candidates, and otherwise build them yourself.
- **Prototyper**: follow [Prototype](references/prototype.md), and show the options, comparison, and recommendation to the user here.
- **Orchestrator**: follow [Orchestration](references/orchestration.md) for the agreed plan, delegating Builders and reviewing their work, and bring decisions beyond the agreed scope to the user here.

Independent judgment, such as an Orchestrator reviewing a Builder's change, options compared side by side, or a second review, is worth more when the two sides run on different models, because each model misses different things. That difference comes from the user's delegation roles: choose the role and its choices as the `attn` skill's delegation reference describes, including when one task runs several choices of a role, and do not override a model the user did not ask to change. When the roles put both sides on one model, say so where you report the judgment. A second review by a separate Reviewer delegation runs only when the user asks for one, as that reference says. When a change is contested or costly to reverse, you may suggest one as its own question; a plan or handoff the user approves is not that request, even when it mentions a Reviewer.

## Principles

Apply these principles when planning, implementing, and reviewing software changes. Each lives in its own file under `principles/`. A line here says when a principle applies and what it asks in one clause; it is a trigger, not the rule. Before you apply a principle, read its file in full. Each process reference names the principles it relies on: read those when you load the process, and any other whose trigger fits the work. When a principle changes a decision, name it in your report or seed note with the choice it changed, and cite only principles whose file you read in this session.

Shape the change:

- [Match rigor to consequences](principles/match-rigor-to-consequences.md): before adding validation, guards, or recovery paths. Guard only credible failures that no existing boundary already handles.
- [Smallest change](principles/smallest-change.md): sizing a diff, or tempted to add a layer, option, or abstraction. The smallest change that delivers the full requirement, with each choice made once.
- [Subtract before you add](principles/subtract-before-you-add.md): sequencing an addition, refactor, or rewrite. Remove dead weight first, then build on the simpler base.
- [Derived things must earn their place](principles/derived-things-must-earn-their-place.md): before adding a doc, comment, summary, generated file, or copy of state. Prefer a better option; otherwise add it with a way to keep it current.
- [Refactor first](principles/refactor-first.md): building a capability something may already do. Give the shared path what it lacks instead of growing a parallel mechanism.
- [Redesign around the new requirement](principles/redesign-around-the-new-requirement.md): integrating a requirement the design did not anticipate. Build what you would have built had it been known from the start.
- [Model the domain first](principles/model-the-domain-first.md): before writing logic, or when code branches a lot. Name the data shape and encode the domain in a structure, not scattered conditionals.
- [Experience first](principles/experience-first.md): product, interface, or scope tradeoffs. Choose the experience of whoever consumes the work over implementation convenience.
- [Compare options before committing](principles/compare-options-before-committing.md): a decision with no precedent, or one that depends on feel. Build two or three different options and compare them side by side.

Structure:

- [Parse, don't validate](principles/parse-dont-validate.md): validation, error handling, adapters, types and signatures. Check at the boundary, trust the types inside, make invalid states unrepresentable.
- [Minimize reader load](principles/minimize-reader-load.md): code that is hard to trace. Cut layers to trace and state to hold in mind.
- [Make operations idempotent](principles/make-operations-idempotent.md): a step that changes state and can be interrupted or retried. Converge on the same end state from any partial run.
- [Separate before serializing shared state](principles/separate-before-serializing-shared-state.md): concurrent actors, processes or agents, that might write the same thing. Remove the sharing before adding a lock.
- [Migrate callers, then delete the old way](principles/migrate-callers-then-delete-the-old-way.md): a new interface replacing an old one. Move every caller you own and delete the old one in the same piece of work; name the callers you cannot change.
- [Converge on the outcome](principles/converge-on-the-outcome.md): a planned rewrite or migration. Aim at the verified end state, not smooth midpoints held up by throwaway compatibility code.

Verification:

- [Prove it works](principles/prove-it-works.md): before reporting any change complete. Check the real thing on the affected surface, and label each claim measured, inferred, or guessed.
- [Fix root causes](principles/fix-root-causes.md): debugging, or about to guard a state that broke something. Fix the first wrong decision, not where it surfaced.
- [Test behavior, not implementation](principles/test-behavior-not-implementation.md): writing, keeping, or reviewing a test. It fails when behavior breaks and passes when only internals change.
- [Explain the number](principles/explain-the-number.md): before trusting or reporting a measured number. Name its limiter and rule out what else it measures.
- [Build the lever](principles/build-the-lever.md): any work that is not trivial, including work fanned out to other agents. Write the script that does or proves it.
- [Sequence verifiable units](principles/sequence-verifiable-units.md): multi-step work, and the order of commits and pull requests. Small units, each checked before the next, proof before the change.
- [Attack the premise](principles/attack-the-premise.md): two fixes sharing one assumption failed the same check, or fixes keep arriving in one shape. Test the assumption before a third fix.

Working with the user and other agents:

- [Never block on the user](principles/never-block-on-the-user.md): tempted to stop and ask during execution. Decide reversible calls, settle facts by running them, and bring the rest to your coordinator or the user.
- [Guard the context window](principles/guard-the-context-window.md): large outputs, files or searches, or work that may outlast your context. Delegate the bulk and keep what must survive in the seed.
- [Update what a decision overturns](principles/update-what-a-decision-overturns.md): the user's decision contradicts docs, code, or agent guidance. Change them in the task, or plant a seed.
- [Encode lessons in structure](principles/encode-lessons-in-structure.md): the same kind of mistake corrected, or the same instruction written, a second time. Fix the class with structure, not another instruction.

## Writing

Text another reader acts on, such as a plan, a brief, a question for the user, a review, or a report, follows [Technical writing](references/technical-writing.md). Read it before writing one.
