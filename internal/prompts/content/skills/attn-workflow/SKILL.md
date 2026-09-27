---
name: attn-workflow
description: Use when an assigned role refers to this skill, the user asks for the Pathfinder approach in the current conversation, or the user requests its processes for investigation, discussion, alignment, debugging, planning, implementation, review, or orchestration.
---

# Attn workflow

Read the reference for the process the task needs. Load further references as the work requires.

For the Pathfinder approach, use [Discuss](references/discuss.md) to investigate or develop the idea with the user, drawing on [Align](references/align.md) to test shared understanding. When the outcome is a plan, use [Planning](references/planning.md) to capture it. Work in the current conversation; delegate only when authorized.

| Task | Reference |
|---|---|
| Investigate a question or develop an idea through grounded discussion | [Discuss](references/discuss.md) |
| Test shared understanding, assumptions, and boundaries | [Align](references/align.md) |
| Investigate unexpected behavior through evidence and experiments | [Debugging](references/debugging.md) |
| Write a substantial implementation plan in the garden and arrange its handoff | [Planning](references/planning.md) |
| Implement an agreed brief or plan and address review findings | [Implementation](references/implementation.md) |
| Review a PR or changes against a plan, including behavioral verification | [Review](references/review.md) |
| Delegate agreed work, advise implementers, and judge completion | [Orchestration](references/orchestration.md) |

## Engineering judgment

Apply these principles when planning, implementing, and reviewing software changes.

Match rigor to the software's actual requirements, operating conditions, and consequences of failure. Before adding validation, guards, recovery paths, or other defensive machinery, consider whether the failure is credible here and whether existing boundaries already handle it. Prefer the simplest design that preserves the required behavior. A conceivable failure alone does not justify added complexity; a credible risk can justify prevention before it has ever occurred.

Parse, don't validate: establish invariants at the earliest appropriate boundary and preserve them through types, data structures, and ownership. Prefer representations that make invalid states unrepresentable, so internal code can rely on established guarantees instead of repeatedly validating them. Keep state-dependent checks where the relevant state is authoritative. Use the simplest representation that provides the needed guarantee.
