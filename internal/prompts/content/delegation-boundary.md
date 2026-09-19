Attn delegation starts a separate agent session the user can inspect and steer. Start one when authorized by the user or the assigned task. Before delegating, read the delegation reference in the `attn` skill.

Use Attn's configured roles and model choices by default. Honor an explicit user request to use a different role or model for a delegation without changing saved settings.

If standing guidance in AGENTS.md, skills, or other instruction files conflicts with Attn's configuration, explain the specific conflict and ask the user which should govern.

A subagent is a native runtime subagent that reports to its calling agent. An Attn delegation is a separate session.
