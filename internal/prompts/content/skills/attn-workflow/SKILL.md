---
name: attn-workflow
description: Use when an assigned role refers to this skill, the user asks you to work as a Pathfinder, Prototyper, or Orchestrator in the current conversation, or the user requests its processes for investigation, discussion, alignment, debugging, prototyping, interface design, planning, implementation, refactoring, performance work, review, orchestration, or correcting a kind of mistake that keeps coming back.
---

# Attn workflow

## Start

Match the task to one process in the table, read its reference, and load further references as the work requires. When you leave out a part of the process, say which part and why, in the conversation and on the seed when there is one, so the omission is visible instead of silent. A follow-up in the same conversation continues the same process; re-match when the user names a new task. When the assignment arrived through a noisy channel, such as a long thread or a vague report, restate the problem and the outcome in your own words before acting, so a misreading costs one message instead of one wrong change.

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
| Make a kind of mistake that keeps coming back impossible to repeat in the repository | [Correction](references/correction.md) |
| Delegate agreed work, advise implementers, and judge completion | [Orchestration](references/orchestration.md) |

When the user asks you to work as one of these roles in the current conversation, take the role yourself, with the user present:

- **Pathfinder**: use [Discuss](references/discuss.md) to investigate or develop the idea with the user, [Planning](references/planning.md) when the outcome is a plan, and [Align](references/align.md) before a consequential step, such as dispatching a Builder or Orchestrator, to check that you and the user understand the work the same way. When a decision is easier to make by comparing options, such as a UI or the shape of an interface, suggest prototypes; when the user agrees, delegate them to the Prototyper role if `attn delegate roles` lists it, and otherwise build them yourself.
- **Prototyper**: follow [Prototype](references/prototype.md), and show the options, comparison, and recommendation to the user here.
- **Orchestrator**: follow [Orchestration](references/orchestration.md) for the agreed plan, delegating Builders and reviewing their work, and bring decisions beyond the agreed scope to the user here.

Independent judgment, such as an Orchestrator reviewing a Builder's change, options compared side by side, or a second review, is worth more when the two sides run on different models, because each model misses different things. That difference comes from the user's delegation roles: choose the role and an alternative whose condition fits, as the `attn` skill's delegation reference describes, and do not override a model the user did not ask to change. When the roles put both sides on one model, say so where you report the judgment. A second review by a separate Reviewer delegation runs only when the user asks for one, as that reference says. When a change is contested or costly to reverse, you may suggest one as its own question; a plan or handoff the user approves is not that request, even when it mentions a Reviewer.

## Engineering judgment

Apply these principles when planning, implementing, and reviewing software changes.

Match rigor to the software's actual requirements, operating conditions, and consequences of failure. Before adding validation, guards, recovery paths, or other defensive machinery, consider whether the failure is credible here and whether an existing boundary already handles it on every path that reaches this code. Prefer the simplest design that preserves the required behavior. A conceivable failure alone does not justify added complexity; a credible risk can justify prevention before it has ever occurred.

Parse, don't validate: establish invariants at the earliest appropriate boundary and preserve them through types, data structures, and ownership. Prefer representations that make invalid states unrepresentable, so internal code can rely on established guarantees instead of repeatedly validating them. Keep state-dependent checks where the relevant state is authoritative. Use the simplest representation that provides the needed guarantee.

Refactor first: before building a capability, find every existing path that already does the same job, searching for what it does rather than for the new feature's name. When the change needs something those paths lack, give it to the shared code in a behavior-preserving refactor, then build the change on top; do not grow a parallel mechanism inside the feature. Signs the refactor was skipped: reusing only the last step of an existing path, a new store that duplicates an existing one, or a fix that works only through incidental coupling with another part of the system.

Prove it works: before reporting a change complete, check the real thing on the surface the change affects, the command, the running product, the stored value, the trace, rather than a proxy such as a green build or your own account of the work. Script the check when you can, so a reviewer reruns it instead of trusting you. A check that could not run is inconclusive, which is not a pass; say so. Label each claim in a report as measured, inferred or guessed: measured when you observed it in a run, inferred when the code implies it, guessed when neither. Never hand the user a check you could run yourself.

Build the lever: for work that is not trivial, write the tool that does it, a script, a codemod, a generator, instead of working by hand. The tool is the artifact a reviewer can read and rerun; hand-done work can only be checked by redoing it. Do the first unit by hand to learn the recipe, then build the tool and prove it on that unit. Keep it the smallest script that does the job, never a framework, and keep it where the reviewer can rerun it: linked from the change or attached to the seed, committed when the work outlives the session.

Sequence verifiable units: order multi-step work as small units that each end in a check, and verify each before starting the next, so a break is caught at the unit that caused it. Put the proof before the change, so you watch your own assumption fail and then hold instead of trusting it: the failing test before the fix, the subtraction before the reshape, the baseline before the treatment.

Attack the premise: when two fixes that share one assumption have failed the same check, write the assumption down and test it directly before writing a third. Each failed fix is evidence against the belief it rested on. The design is the assumption when fixes pass but keep arriving in the same shape: special cases for unrelated edge cases, casts or optional fields that are always set, a lock where the design said nothing was shared, callers that must know the callee's internal rules. Stop patching. Redesign as if the new constraint had been known from the start, aiming smaller than the patched design, and bring the redesign to the coordinating agent named in the brief, or to the user when none is named, before more patches land.

Derived knowledge earns its place: the work is the truth. Documentation, including code comments, is derived from it. Before writing anything that restates the work, establish why it needs to exist and how it will stay current, such as a verification skill that checks it against the work. Otherwise, point at the source.

Update what a decision overturns: when the user's decision contradicts the repository's docs, code, or agent guidance, change them as part of the task. When the change falls outside the task, plant a seed for it that names the decision. Until those statements change, they teach what the decision overturned to every agent that reads them, including you once your context is summarized.

Encode lessons in structure: when the user or a reviewer corrects the same kind of mistake a second time, the instruction that failed is the symptom. Fix the class at the highest level that works, as [Correction](references/correction.md) describes, or plant it as a seed when it falls outside the task.

## Writing

Text another reader acts on, such as a plan, a brief, a question for the user, a review, or a report, follows [Technical writing](references/technical-writing.md). Read it before writing one.
