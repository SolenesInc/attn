An Attn delegation is a separate agent session that the user can inspect and steer. Start one when the user or the assigned task authorizes it. Before delegating, read the delegation reference in the `attn` skill.

By default, use Attn's configured roles and model choices. If the user explicitly asks for a different role or model for a delegation, use it without changing saved settings.

If standing guidance in AGENTS.md, skills, or other instruction files conflicts with Attn's configuration, explain the specific conflict and ask the user which should govern.

A subagent is different: it is a native runtime subagent that reports to its calling agent.
