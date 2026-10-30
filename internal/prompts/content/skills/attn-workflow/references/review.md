# Review

Principles this process relies on: [Prove it works](../principles/prove-it-works.md), [Test behavior, not implementation](../principles/test-behavior-not-implementation.md), [Minimize reader load](../principles/minimize-reader-load.md), [Refactor first](../principles/refactor-first.md), [Match rigor to consequences](../principles/match-rigor-to-consequences.md), [Attack the premise](../principles/attack-the-premise.md), [Encode lessons in structure](../principles/encode-lessons-in-structure.md), [Smallest change](../principles/smallest-change.md), [Subtract before you add](../principles/subtract-before-you-add.md), [Parse, don't validate](../principles/parse-dont-validate.md), [Migrate callers, then delete the old way](../principles/migrate-callers-then-delete-the-old-way.md), [Derived things must earn their place](../principles/derived-things-must-earn-their-place.md). Read them before you start; [the skill's index](../SKILL.md#principles) lists the rest.

Assess a change against its intended outcome and return findings that help decide whether it is ready. Inspect the implementation and the evidence that it works. Review a diff, a pull request, landed commits, or the running product: a plan with no code behind it draws invented risks, so settle its open questions with a spike or prototype instead, as [Discuss](discuss.md) describes.

## Establish the review context

For a pull request or landed commits, read the description or commit messages and the linked requirements, seed, or design context. Establish what the change is intended to do. If important intent is missing, identify the gap and ask a focused question while continuing checks that do not depend on it.

For changes against a plan, read the agreed plan, its decisions, and the seed notes that record how and why the plan changed. Check that the implementation covers the requirements. Examine departures from the plan, including its findings, and explain their consequences; a departure may be justified by what implementation revealed.

Identify the revision under review and use [Delivery](delivery.md)'s timing and evidence rules for the repository's flow. Read relevant repository guidance and inspect the surrounding code to understand how the changed paths fit the system.

## Inspect and exercise the change

Check correctness, failure and recovery behavior, compatibility, and interactions with affected callers or components. Follow dependencies when they are needed to assess a concrete concern. Keep the review proportional to the change.

When the change adds or reshapes an interface, judge it from its callers with [Design](design.md). A parallel mechanism beside an existing one that does the same job, or a fix that relies on incidental coupling, is a finding about the design, not something to pin with a test.

Assess existing tests and verification evidence against the reviewed revision. Ask what real failure each relevant test would catch. When the repository has a `drive-*` skill, read the files of the features the change touches, and before you approve, check every way in and context they list that applies, from the evidence or by driving it yourself, and confirm the change updates the files for every way in it adds, removes or alters. Run focused checks to resolve doubts, missing coverage, or contradictory results. Reuse evidence that still applies; repeat checks when changes or integration risks warrant it.

When the assignment calls for exercising the running product, use realistic, isolated data and the repository's verification guidance. Cover the relevant user interactions and integration paths. Record the revision and environment, the scenario or command, expected and observed behavior, and supporting logs or recordings. Distinguish passed, failed, blocked, and untested behavior.

## Return findings

Prioritize actionable findings by severity. For each finding, explain the affected behavior, the conditions that expose it, and its consequence. Include a file location or reproducible steps and supporting evidence, and label each finding measured, inferred or guessed, as [Prove it works](../principles/prove-it-works.md) defines them. Separate confirmed defects from unresolved questions and optional improvements. When you weigh another reviewer's findings, give each one you dismiss its reason in one line; a finding two independent reviews raise is strong evidence. A finding that repeats an earlier one is a class: say so, and recommend fixing it with [Correction](correction.md).

When the reviewed seed carries checks, record a verdict on each check you judged: `attn seed check confirm <seed-id> <C#>` for one the evidence proves, and `attn seed check reject <seed-id> <C#> -m "<what would prove it>"` for one it does not. Leave the checks you did not judge as they are.

Summarize what you checked, the reviewed revision, coverage gaps, and your recommendation. If no actionable findings remain, say so and state any material limits on the review.

Return implementation fixes to Builder with enough context to address them. For follow-up review, check the correction and affected behavior, reusing prior evidence where it remains valid. When two corrections that shared an assumption have failed the same check, [Attack the premise](../principles/attack-the-premise.md) and report the assumption as the finding. Publish comments or other external responses only when authorized.

Make the review readable in the conversation and durable in the assigned seed, with links to supporting artifacts.

