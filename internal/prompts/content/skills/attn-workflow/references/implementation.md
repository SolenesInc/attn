# Implementation

Read the agreed brief or plan and inspect the relevant code before editing. Make the change fit the codebase and resolve routine implementation choices yourself.

The plan's findings record what spikes and investigation already established. Build on them rather than rediscovering them. When the code appears to contradict a finding, or a simpler route appears than a finding allows, treat it as an unforeseen issue below instead of working around the finding.

A change that turns out to need a parallel version of an existing mechanism, which the plan did not account for, is an unforeseen issue. When an unforeseen issue requires a consequential decision, bring the evidence, what you tried, and your recommendation to the coordinating agent identified in the brief, or to the user if none is identified. Continue independent work while waiting; wait before proceeding with work that depends on that decision.

Work in small units that each end in a check, and verify each before starting the next.

Verify the affected behavior on the surface the change touches, using repository guidance: run the real command, walk the changed flow in the running product, read back the stored value. A passing build or test suite is not that proof. Prefer a scripted check that a reviewer can rerun. A check that could not run is inconclusive; report it as such rather than as a pass. Reuse evidence that still applies and run further checks where changes or gaps warrant them. Address review findings, then verify the corrections.

Return a reviewable change with the revision, checks performed, results, and remaining limits, each claim labeled measured, inferred or guessed. Make the result readable in the conversation and durable in the seed. Follow the task's authorization for delivery steps.
