# Review

Principles this process relies on: [Prove it works](../principles/prove-it-works.md), [Test behavior, not implementation](../principles/test-behavior-not-implementation.md), [Minimize reader load](../principles/minimize-reader-load.md), [Refactor first](../principles/refactor-first.md), [Match rigor to consequences](../principles/match-rigor-to-consequences.md), [Attack the premise](../principles/attack-the-premise.md), [Encode lessons in structure](../principles/encode-lessons-in-structure.md), [Smallest change](../principles/smallest-change.md), [Subtract before you add](../principles/subtract-before-you-add.md), [Parse, don't validate](../principles/parse-dont-validate.md), [Migrate callers, then delete the old way](../principles/migrate-callers-then-delete-the-old-way.md), [Derived things must earn their place](../principles/derived-things-must-earn-their-place.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Assess work against its intended outcome, and return findings that help decide whether the work is ready for its next step. What you review decides how you review it:

- **A change** is a diff, a pull request, landed commits, or the running product. Judge whether the change is ready to land, as [Review a change](#review-a-change) describes.
- **A plan** is a plan seed or plot whose work has not started. Judge whether the plan is ready to dispatch, as [Review a plan](#review-a-plan) describes.

Report either kind as [Return findings](#return-findings) describes.

## Review a change

Inspect the implementation and the evidence that it works.

### Establish the review context

For a pull request or landed commits, read the description or commit messages and the linked requirements, seed, or design context. Work out what the change is meant to do. If important intent is missing, name the gap and ask a focused question. Continue the checks that do not depend on the answer.

For changes against a plan, read the agreed plan, its decisions, and the seed notes that record how and why the plan changed. Check that the implementation covers the requirements. Examine departures from the plan, including departures from its findings, and explain their consequences. What you learned while implementing can justify a departure.

Identify the revision under review, and use Delivery (`attn skill --reference delivery`)'s timing and evidence rules for the repository's flow. Read the relevant repository guidance and inspect the surrounding code to understand how the changed paths fit the system.

### Inspect and exercise the change

Check correctness, failure and recovery behavior, compatibility, and interactions with affected callers or components. Follow dependencies when you need them to assess a concrete concern. Keep the review proportional to the change.

When the change adds or reshapes an interface, judge the interface from its callers with [Design](design.md). A parallel mechanism beside an existing one that does the same job, or a fix that relies on incidental coupling, is a finding about the design, not something to pin with a test.

Assess existing tests and verification evidence against the reviewed revision. Ask what real failure each relevant test would catch. When the repository has a `drive-*` skill, read the files of the features the change touches. Before you approve, check every applicable way in and context that the files list, either from the evidence or by driving the feature yourself. Confirm that the change includes updates to the files for every way in that the change adds, removes, or alters. Run focused checks to resolve doubts, missing coverage, or contradictory results. Reuse evidence that still applies. Repeat checks when changes or integration risks warrant it.

When the assignment asks you to exercise the running product, use realistic, isolated data and the repository's verification guidance. Cover the relevant user interactions and integration paths. Record the revision and environment, the scenario or command, expected and observed behavior, and supporting logs or recordings. Distinguish passed, failed, blocked, and untested behavior.

## Review a plan

Review a plan before its work is dispatched, while a correction still costs only an edit to the plan. A plan has no run that can fail, so a reviewer who lists what might go wrong invents risks. Report a finding only when you can point at what shows it: a place in the code, the evidence of a spike, two passages of the plan that disagree, or a whole alternative with its cost. Report a doubt that only building something would settle as a question, with the spike that answers it, as the questions below describe.

Read the request before the plan: the user's brief, any vision seed, and the notes that record what the user decided. Then read the plan seed or plot, its children with their checks, and the artifacts of its spikes. [Planning](planning.md) says what each part of a plan must contain. A choice that the notes record as the user's is settled. Report on a settled choice only when the code or a spike's evidence shows that the choice cannot work. When an earlier review of the plan exists, form your own findings before you read it.

Then answer these questions, in proportion to what the plan puts at stake:

- **Does the plan deliver the request?** Compare each thing the user asked for with what the plan does and with what the plan deliberately leaves out.
- **Are the plan's claims true?** Findings and Design state facts about the existing code and about what spikes showed. Open each citation that the design depends on, and check that the code or the spike's evidence says what the plan says. Run a throwaway check in a scratch directory when reading does not settle a claim. Then look for what the plan missed: an existing path that does the same job, another way into the behavior, another client or platform that the change reaches, or a way in with no way out.
- **What deserves a spike before dispatch?** Look for decisions that rest on reasoning alone: an integration that nobody ran, a claim about speed or feasibility with no measurement, or an experience that nobody used. Report one when a wrong answer would force a redesign and a spike costs less than that redesign. Say what the smallest spike would show and which decision waits on the result, as [Discuss](discuss.md) describes. Suggest a [prototype](prototype.md) instead when the doubt is between shapes of a UI or an interface.
- **What could the plan remove?** For each decision that shapes the system, [attack the premise](../principles/attack-the-premise.md) and [subtract before you add](../principles/subtract-before-you-add.md). Ask which requirement needs each piece of machinery, and whether a simpler whole design delivers the same outcome. Name a larger alternative too, with its cost, when you see one. Judge an interface that the plan adds or reshapes from its callers with [Design](design.md). Leave the tactics that an implementer can change.
- **Would the checks catch a broken result?** For each check, ask what broken result would still pass. Name each part of the outcome that no check proves.
- **Can a Builder start each child from its seed alone?** Read each child as an agent that has only that seed. Check that the child states its outcome, scope, and checks, that the parent and the children agree, that the order between children matches the real prerequisites, and that no work falls between two children.

Before you report a finding, trace the concrete sequence of events that produces the problem, and check each step against the plan, the code, or a spike's evidence. Drop a finding whose sequence breaks. Cite the plan's own numbers, such as D2, F4, or C3, so the author can find the passage.

Do not edit the plan or its children. The plan's author rewrites the plan with the changes that the user accepts.

## Return findings

Prioritize actionable findings by severity. Verify each finding against the code or a run before you report it, and leave out P2 polish unless the task asks for it. Drop guesses you have not verified against the code. For each finding, explain the affected behavior, the conditions that expose the defect, and the consequence. Include a file location or reproducible steps and supporting evidence. Label each finding measured, inferred, or guessed, as [Prove it works](../principles/prove-it-works.md) defines the labels. When you weigh another reviewer's findings, give the reason for each dismissed finding in one line. A finding that two independent reviews raise is strong evidence. If a finding repeats an earlier one, it is a class: say so, and recommend fixing it with [Correction](correction.md).

Sort the findings into three groups, and name the group of each finding:

- Blocking: a defect in behavior that you can show at the reviewed revision, with a run that fails or a line of documentation that the change made wrong. A defect that the base already has blocks only when the change copies it or newly reaches it. Record any other defect of the base in one line. In a plan, a blocking finding is one that the author must correct before dispatch: a claim that the code or a spike's evidence contradicts, passages that disagree, a requested outcome that the plan does not deliver, or a check that a broken result would pass.
- Scope question: a finding about the design, as Inspect and exercise the change describes. A scope question needs no run. In a plan, a scope question is a decision with a better alternative, or a doubt that needs a spike. The user decides both.
- Follow-up: a verified, high-impact defect outside the reviewed change's scope or reach, such as a pre-existing P0 or P1 in untouched code or work requiring another machine. Leave out unconfirmed suspicions, P2 nitpicks, and optional polish.

List the questions about intent that you asked under Establish the review context apart from the findings.

When you review a change and the reviewed seed carries checks, record a verdict on each check you judged. Use `attn seed check confirm <seed-id> <C#>` for a check the evidence proves, and `attn seed check reject <seed-id> <C#> -m "<what would prove it>"` for a check the evidence does not prove. A rejected check blocks the change. Leave the checks you did not judge as they are. A plan's checks have no reports yet, so give them no verdict.

Summarize what you checked, the reviewed revision, coverage gaps, and your recommendation. For a plan, the reviewed revision is the bookmark that `attn seed show` printed for the plan seed, and the recommendation is one of three: dispatch as written, dispatch after the named changes, or rework the named parts first. If no actionable findings remain, say so and state any material limits on the review.

Return the blocking findings to the Builder, or to the plan's author, with enough context to address them. In a follow-up review, check only the correction and the affected behavior, and reuse prior evidence where it remains valid. Do not start a fresh hunt for P2 polish: only verified P0 or P1 defects block. When two corrections that share an assumption have failed the same check, [Attack the premise](../principles/attack-the-premise.md) and report the assumption as the finding. Publish comments or other external responses only when the user or the assigned task authorizes it.

Report the review in the conversation, and record it in the assigned seed with links to the supporting artifacts.
