# Prototype

Prototype when a decision is easier to make by comparing options than by describing them: a screen, a flow, an interaction, or the shape of an interface, as [Design](design.md) defines it. A prototype is a spike whose product is a set of options, so the spike rules in [Discuss](discuss.md) apply: its code is disposable, and what carries forward is the choice and what the options revealed.

## Frame the decision

State the decision the prototypes inform, what is fixed, and what the options may vary. Read the code, existing design, and conventions the result must fit, so every option is realistic. When the change overlaps an existing mechanism, make one option reshape that mechanism to absorb the new case, so the comparison shows refactoring first beside building alongside it. If the brief leaves the decision unclear, ask before building; when you were delegated and nobody answers, state the interpretation you chose and build against it.

## Build the options

Build at least two options, adding another only when it is a genuinely different approach. Options differ in approach rather than in detail: a different structure, interaction model, or boundary, not a different color or parameter name. If only one approach is credible, say so and show the strongest alternative you rejected, with the reason. Give each option a short name and one sentence on the idea behind it.

Make each option concrete enough to judge the decision, and no more. Build them in isolation like any spike: write the options outside the tracked tree, or wherever the task's isolation allows, and do not commit them.

- **Visual.** Write each option as a self-contained HTML file with inline styles and scripts, realistic content, the states that matter (such as empty, typical, overflowing, and failed), and working versions of the interactions in question. Match the product's existing look unless the look is the question. Show the options with the harness's native artifact or preview feature when it has one. Otherwise give each file's path, and open the files in the default browser (`open` on macOS, `xdg-open` on Linux) only when you share the user's machine and display.
- **Interface design.** Write each option as the interface's declarations and the same two or three realistic call sites, as [Design](design.md) describes under Start from the caller, using names from the codebase. Put the options side by side in one document and judge them with Design's checks.

## Compare and recommend

For each option, state what it does well, what it costs, and where it breaks. Then recommend one option, or a combination, and give the reason, pointing to the call site, screen, or state that shows it. The user chooses.

Record where the options live, the comparison, and the recommendation in the assigned seed, if there is one. Whoever presents the options to the user carries the result into the plan, as [Planning](planning.md) describes: the user's choice and its reason become a decision, and what the options revealed, such as an approach that broke on a realistic case, becomes a finding, with its evidence quoted from the option because the option is disposable.
