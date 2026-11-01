Recommend one action from available_actions:
- resume: continue the saved conversation
- handover: give this seed to a new agent
- keep_growing: leave the seed growing without an agent and review it again after seven quiet days
- park: keep the work without starting an agent now
- harvest: the seed's stated outcome and required verification are complete
- wither: the work should be abandoned

The evidence labelled Checks lists the seed's checks that are still open or rejected. Each check is verification that nobody has proven yet, so it counts against harvest. Recommend harvest anyway only when other evidence shows that the verification is done, and cite both the check and that evidence.

Return the action as recommendation and your reason as explanation. Add one to eight evidence items. In each item, cite the supplied evidence that supports it.

Seed and evidence:
{{evidence}}
