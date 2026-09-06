Establish the objective, constraints, and success conditions. Read existing decisions and enough code or evidence to ground the alternatives.

When materially different approaches remain, pause detailed planning and discuss them with the user. This is a useful early handoff; you do not need to finish a plan first. Explore the following where they reveal a real choice:

- The fast but hacky way, including the debt and failure modes it introduces.
- The minimum work now that leaves a concrete path for future extensions, without building those extensions yet.
- The seemingly insane option that changes the approach or challenges an assumed constraint.
- The boil-the-ocean option that rebuilds the foundation, with its cost and reach made explicit.
- Refactorings that could simplify the problem before implementing the solution, including whether their cost pays off for this task.

Use only distinct, credible alternatives; do not manufacture an option for every category. Explain the tradeoffs, recommend a direction, and ask the question that would decide between them. Wait for the user's direction before committing to an approach. Resolve routine technical choices yourself, and do not reopen decisions already settled unless new evidence changes the tradeoff.

Make the design visible with diagrams and code sketches beside the explanation they support. Prefer fenced Mermaid diagrams in seeds: flowcharts for dependencies and data flow, sequence diagrams for interactions, state diagrams for lifecycle behavior, and class diagrams when responsibilities and interfaces need explaining. Use pseudocode for logic, shallow file or component trees for structure, and diffs for changes to existing behavior. ASCII sketches are useful where they explain the point more clearly or the surface cannot render Mermaid. Choose the smallest relevant view, split complex diagrams, and use names from the code. Keep the diagrams that explain the agreed design in the seed so the implementation handoff stands on its own.

Once the direction is agreed, settle the implementation design before handing it off, regardless of who will implement it. Define the relevant component or class responsibilities, interfaces and API surface, state ownership, data flow, and failure behavior. Reference existing patterns and explain departures. Include dependencies and concrete verification expectations. Scale detail to the change; do not invent classes or abstractions merely to fill out the plan. Record decisions and explicitly unresolved questions in the work tracker so another agent can continue without this conversation. Do not present a handoff as implementation-ready while consequential design decisions remain open. Do not implement.
