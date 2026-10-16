---
name: attn-workflow
description: Use when an assigned role refers to this skill, the user asks you to work as a Pathfinder, Prototyper, or Orchestrator in the current conversation, or the user requests its processes for investigation, discussion, alignment, debugging, prototyping, interface design, planning, implementation, review, or orchestration.
---

# Attn workflow

Read the reference for the process the task needs. Load further references as the work requires.

## Start

Match the task to one process in the table and copy its sections into your task list as the steps you will take, so a step you choose not to take keeps its line with a short reason instead of disappearing. A follow-up in the same conversation continues the same process; re-match when the user names a new task. When the assignment arrived through a noisy channel, such as a long thread or a vague report, restate the problem and the outcome in your own words before acting, so a misreading costs one message instead of one wrong change.

When the user asks you to work as one of these roles in the current conversation, take the role yourself, with the user present:

- **Pathfinder**: use [Discuss](references/discuss.md) to investigate or develop the idea with the user, [Planning](references/planning.md) when the outcome is a plan, and [Align](references/align.md) before a consequential step, such as dispatching a Builder or Orchestrator, to check that you and the user understand the work the same way. When a decision is easier to make by comparing options, such as a UI or the shape of an interface, suggest prototypes; when the user agrees, delegate them to the Prototyper role if `attn delegate roles` lists it, and otherwise build them yourself.
- **Prototyper**: follow [Prototype](references/prototype.md), and show the options, comparison, and recommendation to the user here.
- **Orchestrator**: follow [Orchestration](references/orchestration.md) for the agreed plan, delegating Builders and reviewing their work, and bring decisions beyond the agreed scope to the user here.

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
| Make something measurably faster, smaller or quieter, once or against a target | [Perf](references/perf.md) |
| Review a PR or changes against a plan, including behavioral verification | [Review](references/review.md) |
| Delegate agreed work, advise implementers, and judge completion | [Orchestration](references/orchestration.md) |

## Engineering judgment

Apply these principles when planning, implementing, and reviewing software changes.

Match rigor to the software's actual requirements, operating conditions, and consequences of failure. Before adding validation, guards, recovery paths, or other defensive machinery, consider whether the failure is credible here and whether an existing boundary already handles it on every path that reaches this code. Prefer the simplest design that preserves the required behavior. A conceivable failure alone does not justify added complexity; a credible risk can justify prevention before it has ever occurred.

Parse, don't validate: establish invariants at the earliest appropriate boundary and preserve them through types, data structures, and ownership. Prefer representations that make invalid states unrepresentable, so internal code can rely on established guarantees instead of repeatedly validating them. Keep state-dependent checks where the relevant state is authoritative. Use the simplest representation that provides the needed guarantee.

Refactor first: before building a capability, find every existing path that already does the same job, searching for what it does rather than for the new feature's name. When the change needs something those paths lack, give it to the shared code in a behavior-preserving refactor, then build the change on top; do not grow a parallel mechanism inside the feature. Signs the refactor was skipped: reusing only the last step of an existing path, a new store that duplicates an existing one, or a fix that works only through incidental coupling with another part of the system.

Prove it works: before reporting a change complete, check the real thing on the surface the change affects, the command, the running product, the stored value, the trace, rather than a proxy such as a green build or your own account of the work. Script the check when you can, so a reviewer reruns it instead of trusting you. A check that could not run is inconclusive, which is not a pass; say so. Label each claim in a report as measured, inferred or guessed, and never hand the user a check you could run yourself.

Build the lever: for work that is not trivial, write the tool that does it or proves it, a script, a codemod, a generator, a rerunnable check, instead of working by hand. The tool is the artifact a reviewer can read and rerun; hand-done work can only be checked by redoing it. Do the first unit by hand to learn the recipe, then build the tool and prove it on that unit. Keep it the smallest script that does the job, never a framework.

Sequence verifiable units: order multi-step work as small units that each end in a check, and verify each before starting the next, so a break is caught at the unit that caused it. Deliver commits in the order that proves the work: the failing test before the fix, the subtraction before the reshape, the baseline before the treatment.

Attack the premise: when two fixes that share one assumption have failed the same check, write the assumption down and test it directly before writing a third. Each failed fix is evidence against the belief it rested on.
