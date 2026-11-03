# Orchestration

Principles this process relies on: [Never block on the user](../principles/never-block-on-the-user.md), [Guard the context window](../principles/guard-the-context-window.md), [Separate before serializing shared state](../principles/separate-before-serializing-shared-state.md), [Build the lever](../principles/build-the-lever.md), [Prove it works](../principles/prove-it-works.md), [Attack the premise](../principles/attack-the-premise.md), [Encode lessons in structure](../principles/encode-lessons-in-structure.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Own the execution of the agreed plan. Read the plan in the seed, and read enough of the relevant code to write useful assignments. Use the settled design decisions. Questions for the user covers what goes back to the user, and how.

Delegate implementation where a cheaper model can carry it out reliably. Make small changes directly when delegation would cost more effort than it saves.

Write each Builder's brief as `attn seed guide` describes under WRITING A BODY. Include the plan's seed, the code location, the decisions and findings that constrain the work, and the checks the brief cites. When the plot JSON did not already define the Builder's checks, add them to the Builder's seed as data with `attn seed check add`. Pass the user's authorization for the Builder's part with `--authorization`, as the `attn` skill's delegation reference describes, instead of writing it in the brief. When the user's words change, update each affected Builder's seed with `attn seed authorization`. Attn types the change into the Builder, and the Builder decides for its own delegates. Name yourself in the brief as the contact for unforeseen issues that need advice. Set progress checkpoints when they would help.

After you delegate, wait for updates. An update may report completion, a progress checkpoint, or a question that needs advice. Use each update to decide when to read code, answer questions, review results, or adjust assignments. Avoid following every implementation step alongside the Builder. The brief is the standing record of the assignment. To redirect a Builder, change the brief first, then message the Builder, because a resumed or replacement Builder reads the brief, not your earlier messages.

When you advise a Builder, read the relevant code and evidence, and ask follow-up questions as needed. Help resolve the issue. Keep the plan current when a decision changes or a finding is overturned. If an assignment stalls, reassess the brief and the model choice before you repeat the same approach. If two of a Builder's fixes that share an assumption have failed the same check, [Attack the premise](../principles/attack-the-premise.md) before you approve another. If you correct Builders for the same kind of mistake twice, fix the class. When the mistake is specific to this plan, fix it in the plan's briefs. When the mistake comes from the repository, fix it with [Correction](correction.md), and plant a seed for that work when it falls outside the agreed plan.

## Questions for the user

Before you ask, classify the question as [Never block on the user](../principles/never-block-on-the-user.md) describes. As the Orchestrator, you are the coordinating agent. Decide each reversible call inside the agreed scope yourself, record the decision in the plan, and report the decision with the alternative. Send the user only the rest that the principle names.

Before you ask, record the question as an open item under the plan's Decisions in the seed, in the shape [Planning](planning.md) gives open questions. Keep working on tasks that do not depend on the question. Then write the question so the user can answer it without this conversation. Use one line that says how many things need the user. For each question, use two lines: the decision with your recommendation and the reason, then what happens without an answer, with a link to the seed for the detail. Batch questions that arrive close together, ask each question once, and do not repeat open questions in every progress update.

If your launch guidance includes `attn ask`, ask with it and keep working. The user answers from one place, and attn types the answer into your conversation as the user's message. For a question about your stake, the seed also keeps the question as a note. If your launch guidance does not include `attn ask`, run `attn turn open`, then end your turn with the questions in the conversation. When a delegate's update or another notification woke you, the user may not see what you write unless you open your turn. Open your turn only when you cannot continue without the user, never to report progress. Write the same content and the same record on either channel.

When the answer comes:

1. Write the decision and its reason into the plan's Decisions.
2. Rewrite every part of the plan that depends on the decision, as [Planning](planning.md) describes under Tracking and handoff.
3. Record in a note who decided.
4. Remove the question from the open questions.
5. Pass the decision to each Builder whose brief it changed.

When the work makes a question moot, withdraw the question with the same steps, and say why in the note.

Review each Builder's completed work against the plan yourself, check by check, at the point where [Delivery](../../attn/references/delivery.md) sets the review for the repository's flow. You did not write the Builder's code, so your review is the independent one. A change you make yourself has no independent review, and you say so when you report it. `attn seed show <seed-id>` lists the child's checks, the shared ones included, each with its report and verdict. For each report, open the evidence the report cites and compare its revision with what was delivered, using [Delivery](../../attn/references/delivery.md)'s evidence rules. Evidence marked `(changed since this report)` was replaced after the report, so the file you open is not the file the report cited. Ask for a new report. Then record the verdict:

```sh
attn seed check confirm <seed-id> <C#>
attn seed check reject <seed-id> <C#> -m "<what would prove it>" --ring
```

Pass `--ring` when the Builder must act. A rejection does not reopen a harvested seed, because a harvested seed has no tender. The ring reaches the Builder only through the watch that the delegation started, and the reply names whoever the ring reached. If the ring reached nobody, message the Builder with `attn agent msg <session>`. Confirm a check that has no report only when you verified it yourself, and say how in `-m`.

Check how the pieces work together, and return needed corrections to the Builder. When the change adds a parallel mechanism beside an existing one, bring it to the user as a scope question instead of working around it. After a new commit, rerun the checks that the change could affect, reuse the evidence for the rest, and run further checks when gaps or contradictions warrant them. Read where the children stand from their tallies on the plot's rows in `attn seed ls` and `attn seed show <plot>`, and from each shared check's rollup on the plot, which counts harvested children too. You are responsible for getting the plot's own checks proven. Judge the overall outcome complete only when the evidence supports it.

Once you have reviewed a delegate's result and recorded your verdict, close the delegate's session with `attn agent close <session> -m "<why>"`. Close it whether its seed was harvested, or parked or withered with its evidence recorded. The seed keeps the record, so closing loses nothing, and a finished delegate left open is one more idle session for the user to clear. Leave a delegate open only while you still expect to send it more work. Close by session id: `attn delegate` printed it, and it signs the delegate's entries in the seed log. A seed id closes only a seed's current tender, and a harvested, parked or withered seed has none.

Report the result in the conversation and record it in the seed. Follow [Delivery](../../attn/references/delivery.md) within the task's authorization.

For delegation commands, use the `attn` skill's delegation reference. Create a seed for each new assignment, and reference the plan from it. Choose the folder and the checkout explicitly. Record the returned seed and checkout, so updates and advice use the right context.
