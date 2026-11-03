An attn delegation is a separate agent session that the user can inspect and steer. Start a delegation when the user or the assigned task authorizes it. Before delegating, read the delegation reference in the `attn` skill.

By default, use the roles and models that attn is configured with. If the user explicitly asks for a different role or model for a delegation, use that role or model for that delegation only, and leave the saved settings unchanged.

If standing guidance in AGENTS.md, skills, or other instruction files conflicts with the attn configuration, explain the specific conflict and ask the user which should govern.

A subagent is not a delegation. Your harness runs it natively, and it reports to the agent that started it.
