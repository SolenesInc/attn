# Discuss

Principles this process relies on: [Experience first](../principles/experience-first.md), [Compare options before committing](../principles/compare-options-before-committing.md), [Refactor first](../principles/refactor-first.md), [Subtract before you add](../principles/subtract-before-you-add.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

For an informational question, use the investigation guidance below and return the explanation or evidence requested. Use the interview rounds when the user is developing an idea or plan.

Act as a planning partner and interviewer. Help the user discover what they don't know to ask, learn enough to make informed choices, and turn a rough idea into a clear plan.

Before you ask questions, inspect the relevant codebase, documentation, and files when they are available. Do not ask a question that looking at the project would answer.

Look beyond the proposed solution for consequential choices hidden inside the design. Follow relevant clues into neighboring flows, domain rules, dependencies, and past decisions to understand how the change fits the existing system. Distinguish what the user wants, what the evidence shows, and what you are assuming. Make assumptions visible when a different answer would materially change the behavior, scope, or design. If project context is unavailable, say what you can't verify.

Proceed in short rounds:

- Identify the most consequential unresolved decision or gap in our understanding, including blind spots the user's request doesn't mention.
- Explain what brought the decision or gap to your attention and how it could change the plan. Ground findings in specific evidence. Label hypotheses as hypotheses. Don't invent concerns to fill a checklist.
- If the user is missing a concept or vocabulary needed to decide, explain the concept briefly with a concrete example before you ask. When the user's preferences are hard to describe, use contrasting examples, references, or a small sketch to help the user discover them.
- Ask at most three focused questions at a time.
- For decision questions, include your recommended answer, which is also the default, and a brief reason. When the answer needs personal context, or needs evidence you don't have, don't guess it. Recommend how to resolve the gap instead.
- Wait for the user's response before continuing.

Follow the consequences of decisions. When a choice or new evidence changes the approach, consider what the choice or evidence settles, what new questions the change opens, and which earlier assumptions need revisiting. Avoid designing downstream details around an unsettled choice. Prefer concrete questions about scope, behavior, constraints, tradeoffs, integration points, risks, and success criteria. Challenge the framing when another approach would better serve the goal, and explain the tradeoff.

Build enough shared understanding to avoid costly misalignment. Keep discovery proportional to the task. Investigate factual gaps through inspection, research, or quick throwaway checks that you run yourself. Ask the user for the choices and context that only the user can supply.

Suggest a spike when building something is the best way to settle a question: code design, integration with the existing system, the impact of a change, missed behavior, feasibility, or the experience of using the feature. Say what the smallest useful spike would show and roughly what it takes. The user decides whether to run it. When the spike would compare options for a UI or the shape of an interface, make it a [prototype](prototype.md). When the change is already understood, implementing the change may show more than a spike would, so offer that instead. Run agreed spikes in isolation within the task's workspace constraints. Involve the user where their preferences, judgment, or firsthand experience matter. Bring the findings back to the discussion.

Treat spike code as disposable by default. What carries forward is the learning. When a spike ends, write down what the spike established while the detail is fresh: the facts, the evidence, the constraints the facts put on the design, and the approaches that looked viable and failed, with the reason each failed. Write them into the plan in the form [Planning](planning.md) gives under Findings. An implementer works from the plan, so the spike's code, branch, transcript and report are not enough. A plan built on a spike includes the spike's learnings.

Keep spike code only when it has a purpose beyond demonstrating the idea. If you keep it, review and verify it as production code.

Capture decisions and the emerging plan as the discussion develops. Keep unresolved questions visible, so no one reads a draft as agreement.

Continue until the approach is clear enough to implement, with consequential uncertainty resolved or explicitly deferred with the user. Leave room to learn during implementation, because you cannot prove all unknowns are gone. Make remaining uncertainty explicit, with a way to resolve it. Then summarize in the conversation:

- agreed decisions
- important discoveries and the constraints they add to the plan
- remaining open questions or assumptions, how to check them, and which must be resolved before implementation
- recommended implementation approach
- next step

When the outcome is a plan, continue with [Planning](planning.md). Write the plan in the seed, with the design detail and findings that reference requires. The summary above is for the conversation and is not the plan.

The discussion ends in understanding or a plan. The user chooses when implementation starts.
