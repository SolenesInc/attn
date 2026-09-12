
# Planning

Develop a concrete implementation approach that the user can review and another agent can execute. Store the plan directly in a garden seed or plot body. Do not create a standalone plan file.

Read the `attn` skill's garden guidance and run `attn seed guide` before planning. Use that guide for plan structure, technical diagrams, child seed briefs, and lifecycle rules. If the garden is unavailable, report what is missing; do not fall back to a file.

## Workflow

1. **Find the work.** Read the relevant implementation seed or plot, its notes, and any children. Reuse existing work and preserve its scope and decisions. If the work has no implementation seed, plant one to hold the plan.
2. **Investigate the approach.** Start from the user's request, relevant conversation, and any vision referenced by the work or supplied by the user. Read vision seeds with `attn seed show <id>`. Read enough code to identify the components, entry points, state, interfaces, and ownership involved. Trace production and test paths where they differ. Propose an approach from your findings. Ask about choices or assumptions that could change the plan.
3. **Write the plan.** Put the implementation approach in the seed or plot body, following the garden guide. If there is a vision seed, reference its ID; do not overwrite its body with the implementation plan. Make the intended changes, boundaries, and verification clear enough for an implementer who has none of this conversation. Keep the body focused on the current design.
4. **Organize execution.** Use a plot when the work has distinct pieces to scope and track separately, even within one PR. Use a single seed for one coherent task. Add or reuse child seeds for each unit of work, with an outcome, scope, and verification; refer to the parent plan without repeating it. Explain which changes belong in each PR. Add `blocks` links only for actual prerequisites; otherwise leave children independent.

## Explain the design

Explain how the proposed system works. Show the relevant components, their responsibilities, and how they interact. Describe the public API and key data types, including inputs, outputs, and persisted data. For stateful behavior, show who owns the state, what changes it, and its lifecycle through success, failure, and recovery. Use small diagrams and type or code sketches beside the explanations they support. Scale the detail to the change so the reader can assess the design without reconstructing it from the code.

## Tracking and handoff

Write the plan so another agent can carry it forward. Keep the implementation design in the seed. When execution is authorized, use the delegation process in the `attn` skill to select the agent and launch configuration.

Seed states and notes carry progress. Update the plan body when the implementation approach changes, and record the reason in a note. Plant deferred work as seeds. Do not keep a task checklist or activity log in the plan body.

Read back the saved plan, children, and dependency links to check that they cover the intended outcome and preserve existing work. Show the user the proposed plan and, when pull-request delivery applies, its proposed pull-request boundaries and ordering. Save the plan in the seed so another agent can continue from it, and name the seed or plot for review.

Recommend how to execute it. Recommend an Orchestrator when the plan requires coordinated or reviewed Builder work, or benefits from mixing harnesses or models between the coordinating agent and its Builders. Otherwise, recommend a single Builder. Explain the recommendation briefly.

Ask whether the user wants to review or adjust the plan, or dispatch, and wait for their answer. This checkpoint applies even when their earlier request included execution. Dispatch only when the user chooses dispatch after seeing the proposed plan and handoff. Give the next agent the plan seed, code location, agreed scope, verification expectations, and authorization. Agreement on the plan alone does not authorize execution. Keep execution seeds open when only the plan is complete.

For an authorized handover, keep the plan in its seed and record the next assignment and execution authorization in a handoff note. Use the delegation process in the `attn` skill to choose the explicit folder and checkout. The successor reads the plan and handoff from the seed.
