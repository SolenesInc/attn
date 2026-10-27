# Prototype

Principles this process relies on: [Compare options before committing](../principles/compare-options-before-committing.md), [Experience first](../principles/experience-first.md), [Refactor first](../principles/refactor-first.md). Read them before you start; [the skill's index](../SKILL.md#principles) lists the rest.

Prototype when a decision is easier to make by comparing options than by describing them: a screen, a flow, an interaction, or the shape of an interface, as [Design](design.md) defines it. A prototype is a spike whose product is a set of options, so the spike rules in [Discuss](discuss.md) apply: its code is disposable, and what carries forward is the choice and what the options revealed.

## Frame the decision

State the decision the prototypes inform, what is fixed, and what the options may vary. Read the code, existing design, and conventions the result must fit, so every option is realistic. When the change overlaps an existing mechanism, make one option reshape that mechanism to absorb the new case, so the comparison shows refactoring first beside building alongside it. If the brief leaves the decision unclear, ask before building; when you were delegated and nobody answers, state the interpretation you chose and build against it.

## Build the options

Build at least two options, adding another only when it is a genuinely different approach. Options differ in approach rather than in detail: a different structure, interaction model, or boundary, not a different color or parameter name. If only one approach is credible, say so and show the strongest alternative you rejected, with the reason. Give each option a short name and one sentence on the idea behind it.

Make each option concrete enough to judge the decision, and no more. Build them in isolation like any spike: write the options outside the tracked tree, or wherever the task's isolation allows, and do not commit them.

- **Visual.** Write each option as a self-contained HTML file with inline styles and scripts, realistic content, the states that matter (such as empty, typical, overflowing, and failed), and working versions of the interactions in question. Match the product's existing look unless the look is the question. Show the options with the harness's native artifact or preview feature when it has one. Otherwise give each file's path, and open the files in the default browser (`open` on macOS, `xdg-open` on Linux) only when you share the user's machine and display.
- **Interface design.** Write each option as the interface's declarations and the same two or three realistic call sites, as [Design](design.md) describes under Start from the caller, using names from the codebase. Put the options side by side in one document and judge them with Design's checks.

## Independent candidates

Options built by one agent share its assumptions. When the decision is costly to reverse and the Prototyper role offers more than one model, the agent that frames the decision can start one Prototyper delegation per model instead, as the `attn` skill's delegation reference describes, each building one candidate. Give every delegation the same brief: the decision, what is fixed, the call sites or states each candidate must show, and the [Design](design.md) checks that will decide between them. Say that other candidates exist, without showing them. When the roles offer one model, one Prototyper builds every option, as above.

When you build one candidate, build the best design you can rather than a safe middle: the differences between candidates are what the comparison learns from. Name the strongest alternative you rejected, with the reason, in place of a second option.

The agent that started the candidates combines them. Read every candidate in full and judge them check by check against the brief's checks, not on overall feel. Candidates that converge are agreement and need no grafts. Candidates that diverge wildly mean the brief left the decision open: sharpen it and run them again instead of averaging. Otherwise pick a base, name what each graft takes from which candidate, and fold each one in so the result reads as one design. Then recommend it as below.

## Compare and recommend

For each option, state what it does well, what it costs, and where it breaks. Then recommend one option, or a combination, and give the reason, pointing to the call site, screen, or state that shows it. For a combination, name the option it starts from and what it takes from each of the others. The user chooses.

Record where the options live, the comparison, and the recommendation in the assigned seed, if there is one. Whoever presents the options to the user carries the result into the plan, as [Planning](planning.md) describes: the user's choice and its reason become a decision, and what the options revealed, such as an approach that broke on a realistic case, becomes a finding, with its evidence quoted from the option because the option is disposable.
