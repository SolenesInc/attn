# Implementation

Read the agreed brief or plan and inspect the relevant code before editing. Make the change fit the codebase and resolve routine implementation choices yourself.

The plan's findings record what spikes and investigation already established. Build on them rather than rediscovering them. When the code appears to contradict a finding, or a simpler route appears than a finding allows, treat it as an unforeseen issue below instead of working around the finding.

A change that turns out to need a parallel version of an existing mechanism, which the plan did not account for, is an unforeseen issue. When an unforeseen issue requires a consequential decision, bring the evidence, what you tried, and your recommendation to the coordinating agent identified in the brief, or to the user if none is identified. Continue independent work while waiting; wait before proceeding with work that depends on that decision.

Verify the affected behavior as the skill's engineering judgment describes under prove it works and sequence verifiable units: each unit ends in a check you ran on the surface the change touches, following the repository's verification guidance. Reuse evidence that still applies and run further checks where changes or gaps warrant them. Address review findings, then verify the corrections. When two corrections that shared an assumption have failed the same check, attack the premise as the skill's engineering judgment describes before writing a third.

Return a reviewable change with the revision, the checks performed and how to rerun them, the results, and remaining limits. Make the result readable in the conversation and durable in the seed. Follow the task's authorization for delivery steps.
