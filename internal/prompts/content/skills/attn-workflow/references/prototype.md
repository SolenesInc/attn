# Prototype

Principles this process relies on: [Compare options before committing](../principles/compare-options-before-committing.md), [Experience first](../principles/experience-first.md), [Refactor first](../principles/refactor-first.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Prototype when comparing options settles a decision more easily than describing the options does. The decisions are a screen, a flow, an interaction, or the shape of an interface, as [Design](design.md) defines it. A prototype is a spike whose product is a set of options, so the spike rules in [Discuss](discuss.md) apply. The prototype's code is disposable. What carries forward is the choice and what the options revealed.

## Frame the decision

State the decision the prototypes inform, what is fixed, and what the options may vary. Read the code, the existing design, and the conventions that the result must fit, so every option is realistic. When the change overlaps an existing mechanism, make one option reshape that mechanism to absorb the new case, so the comparison shows refactoring first beside building alongside. If the brief leaves the decision unclear, ask before you build. If you were delegated and nobody answers, state the interpretation you chose and build against it.

## Build the options

Build at least two options. Add another only when it is a genuinely different approach. When your brief makes you one of several independent candidates, build one option, as Independent candidates describes. Make the options differ in approach, not in detail: a different structure, interaction model, or boundary, not a different color or parameter name. If only one approach is credible, or you build one candidate, say so and show the strongest alternative you rejected, with the reason. Give each option a short name and one sentence on its idea.

Make each option concrete enough to judge the decision, and no more concrete. Build the options in isolation, like any spike. Write them outside the tracked tree, or wherever the task's isolation allows, and do not commit them.

- **Visual.** Write each option as a self-contained HTML file with inline styles and scripts, realistic content, the states that matter (such as empty, typical, overflowing, and failed), and working versions of the interactions in question. Match the product's existing look unless the look is the question. If the harness has a native artifact or preview feature, show the options with it. Otherwise, give each file's path. Open the files in the default browser (`open` on macOS, `xdg-open` on Linux) only when you share the user's machine and display.
- **Interface design.** Write each option as the interface's declarations and the same two or three realistic call sites, using names from the codebase, as [Design](design.md) describes under Start from the caller. Put the options side by side in one document and judge them with Design's checks.

## Independent candidates

Options built by one agent share that agent's assumptions. When the decision is costly to reverse and the Prototyper role's choices that fit the decision offer more than one model, the agent that presents the options to the user can propose independent candidates instead. Delegate one Prototyper per model, each building one candidate, as the `attn` skill's delegation reference describes. Start the delegations only after the user agrees to candidates. A delegated Prototyper does not start candidates of its own. When the fitting choices offer one model, one Prototyper builds every option, as above.

Give every delegation the same brief. The brief states the decision, what is fixed, the call sites or states each candidate must show, and what will decide between the candidates. For an interface, the deciding factor is the [Design](design.md) checks that apply. For a UI, it is the states and interactions the decision turns on. Tell each Prototyper that other candidates exist, without showing them. Give each candidate its own output location, so no candidate can read another's work: its own worktree when it needs the code, its own folder otherwise.

When you build one candidate, build the best design you can, not a safe middle. The comparison depends on the differences between candidates.

The agent that started the candidates combines them. Read every candidate in full. Judge the candidates point by point against what the brief said would decide, not on overall feel. Candidates that converge agree, and need no grafts. Candidates that address different questions show that the brief left the decision open. In that case, sharpen the brief and, with the user's agreement, run the candidates again instead of averaging them. Otherwise, pick a base, name what each graft takes from which candidate, and fold each graft in so the result reads as one design. Then recommend the design as below.

## Compare and recommend

For each option, state what it does well, what it costs, and where it breaks. Then recommend one option or a combination, and give the reason, pointing to the call site, screen, or state that shows the reason. For a combination, name the option you start from and what you take from each of the others. The user chooses.

If there is an assigned seed, record in it where the options live, the comparison, and the recommendation. Whoever presents the options to the user carries the result into the plan, as [Planning](planning.md) describes. Record the user's choice and its reason as a decision. Record what the options revealed, such as an approach that broke on a realistic case, as a finding. Quote the finding's evidence from the option, because the option is disposable.
