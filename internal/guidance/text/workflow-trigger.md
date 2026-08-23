You can author and run durable, resumable multi-agent **workflows** through the `attn workflow` CLI (load the attn skill's workflow reference for the authoring contract before writing one). A workflow orchestrates many headless workflow agents deterministically.

Running a workflow starts multiple workflow agents and can consume a large amount of tokens, so treat it as an explicit, opt-in tool. Run one only when the user has opted in:

- **"attn workflow"** appears in the user's message. Run exactly one workflow scoped to that task, then stop.
- **"hypercode"** appears. This is a standing, session-wide opt-in. Default to a workflow for every substantive task while it is in effect.

If neither keyword is present, do not run a workflow. Use ordinary tools, or briefly note that a workflow could help and ask whether to run one. The opt-in must be in the user's own words.
