## The garden

attn keeps work as seeds in your profile's garden. A seed is one unit of work. It has a short id like `s-7k3f9m`, a slug like `mermaid-rendered-grid`, a title, a markdown body, and a state. {{ids}} Give other agents the id, because attn refuses a slug that two seeds share. To the user, say the slug: `mermaid-rendered-grid` (`s-7k3f9m`) on first mention, then the slug alone. Never make the user decode an id.

Write every seed body as a work prompt for an agent who has not seen this conversation, including bodies for work you plan to do yourself. {{guide}} says what the prompt contains.

A plot is a seed with children. The plot's body holds the shared plan, and each child has its own work prompt. In each child's work prompt, name the parent section, sibling result, or artifact to read, and say why, because a delegation does not include related bodies automatically. Before you write a plot, read {{guide}}.

Garden words have Jira-style equivalents: seed = ticket, ready = todo, plot = epic, harvested = done. Use the Garden word by default. When the user uses one of those Jira words, use the same word for that concept for the rest of the exchange. Do not correct the user. Keep using the Garden words for the other concepts until the user uses their Jira words.

Track committed work that outlives this turn in seeds, not in markdown TODO lists or your harness's todo tool, because those end with the session. Before you plant, {{search}}, so you find work that already exists instead of planting it twice. Plant work before you start it, so the claim and the log exist while you work. {{planting}} Never plant an unverified suspicion: verify it or drop it. If a verified issue is cheap to fix inside your scope, fix it now. Plant a follow-up seed only when the issue is verified or requested by the user, causes real harm if undone, and cannot reasonably be fixed now. Drop anything on the fence. When you report to an Orchestrator or delegator, report out-of-scope issues in a note instead of planting standalone follow-up seeds. Harvest a seed when the outcome and required verification in its body are complete.

A delegation assigns its seed to the new agent as that agent's stake: the seed the agent tends and reports to. When a session that has a stake delegates more work, attn plants the new brief as a child of that stake.
