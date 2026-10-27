Recommend one action from available_actions:
- resume: continue the saved conversation
- handover: give this seed to a new agent
- keep_growing: leave the seed growing without an agent and review it again after seven quiet days
- park: keep the work without starting an agent now
- harvest: the seed's stated outcome and required verification are complete
- wither: the work should be abandoned

Evidence labelled Checks names the seed's checks still open or rejected. Each is verification the seed has not proven, so it weighs against harvest: recommend harvest anyway only when other evidence shows that verification done, and cite both.

Return the action as recommendation, why you chose it as explanation, and one to eight evidence items, each citing the supplied evidence it rests on.

Seed and evidence:
{{evidence}}
