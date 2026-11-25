Ask another agent to run `agent_close` with {"self": true, …}, send with `agent_msg` with {"target": "<seed-id>", …}, press `terminal_send` with {"keys": ["Enter"], …}, read `terminal_read` with {"mode": "last", …}, then take it back with `seed_harvest` with {"when_merged": true, "clear": true, …}.

```text
agent_close {"self": true, …}
agent_close {"self": true, "reason": "done"}
```

A target machine takes `"host": "ai-sandbox", "cwd": "~/project"`:

```text
delegate {"seed": "s-1", "role": "builder", "cwd": "/repo"}
```

Settle it with `seed_check_uncheck` or `seed_check_reject` with {"seed": "<id>", "id": "<C#>", …}.
