# Review

Principles this process relies on: [Prove it works](../principles/prove-it-works.md), [Test behavior, not implementation](../principles/test-behavior-not-implementation.md), [Minimize reader load](../principles/minimize-reader-load.md), [Refactor first](../principles/refactor-first.md), [Match rigor to consequences](../principles/match-rigor-to-consequences.md), [Attack the premise](../principles/attack-the-premise.md), [Encode lessons in structure](../principles/encode-lessons-in-structure.md), [Smallest change](../principles/smallest-change.md), [Subtract before you add](../principles/subtract-before-you-add.md), [Parse, don't validate](../principles/parse-dont-validate.md), [Migrate callers, then delete the old way](../principles/migrate-callers-then-delete-the-old-way.md), [Derived things must earn their place](../principles/derived-things-must-earn-their-place.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Assess a change against its intended outcome, and return findings that help decide whether the change is ready. Inspect the implementation and the evidence that it works. Review a diff, a pull request, landed commits, or the running product. A plan with no code behind it gives a reviewer nothing to check, so the reviewer invents risks. Settle the plan's open questions with a spike or prototype instead, as [Discuss](discuss.md) describes.

## Establish the review context

For a pull request or landed commits, read the description or commit messages and the linked requirements, seed, or design context. Work out what the change is meant to do. If important intent is missing, name the gap and ask a focused question. Continue the checks that do not depend on the answer.

For changes against a plan, read the agreed plan, its decisions, and the seed notes that record how and why the plan changed. Check that the implementation covers the requirements. Examine departures from the plan, including departures from its findings, and explain their consequences. What you learned while implementing can justify a departure.

Identify the revision under review, and use [Delivery](../../attn/references/delivery.md)'s timing and evidence rules for the repository's flow. Read the relevant repository guidance and inspect the surrounding code to understand how the changed paths fit the system.

## Inspect and exercise the change

Check correctness, failure and recovery behavior, compatibility, and interactions with affected callers or components. Follow dependencies when you need them to assess a concrete concern. Keep the review proportional to the change.

When the change adds or reshapes an interface, judge the interface from its callers with [Design](design.md). A parallel mechanism beside an existing one that does the same job, or a fix that relies on incidental coupling, is a finding about the design, not something to pin with a test.

Assess existing tests and verification evidence against the reviewed revision. Ask what real failure each relevant test would catch. When the repository has a `drive-*` skill, read the files of the features the change touches. Before you approve, check every applicable way in and context that the files list, either from the evidence or by driving the feature yourself. Confirm that the change includes updates to the files for every way in that the change adds, removes, or alters. Run focused checks to resolve doubts, missing coverage, or contradictory results. Reuse evidence that still applies. Repeat checks when changes or integration risks warrant it.

When the assignment asks you to exercise the running product, use realistic, isolated data and the repository's verification guidance. Cover the relevant user interactions and integration paths. Record the revision and environment, the scenario or command, expected and observed behavior, and supporting logs or recordings. Distinguish passed, failed, blocked, and untested behavior.

## Return findings

Prioritize actionable findings by severity. For each finding, explain the affected behavior, the conditions that expose the defect, and the consequence. Include a file location or reproducible steps and supporting evidence. Label each finding measured, inferred, or guessed, as [Prove it works](../principles/prove-it-works.md) defines the labels. Separate confirmed defects from unresolved questions and optional improvements. When you weigh another reviewer's findings, give the reason for each dismissed finding in one line. A finding that two independent reviews raise is strong evidence. If a finding repeats an earlier one, it is a class: say so, and recommend fixing it with [Correction](correction.md).

When the reviewed seed carries checks, record a verdict on each check you judged. Use `attn seed check confirm <seed-id> <C#>` for a check the evidence proves, and `attn seed check reject <seed-id> <C#> -m "<what would prove it>"` for a check the evidence does not prove. Leave the checks you did not judge as they are.

Summarize what you checked, the reviewed revision, coverage gaps, and your recommendation. If no actionable findings remain, say so and state any material limits on the review.

Return implementation fixes to the Builder with enough context to address them. In a follow-up review, check the correction and the affected behavior, and reuse prior evidence where it remains valid. When two corrections that share an assumption have failed the same check, [Attack the premise](../principles/attack-the-premise.md) and report the assumption as the finding. Publish comments or other external responses only when the user or the assigned task authorizes it.

Report the review in the conversation, and record it in the assigned seed with links to the supporting artifacts.
