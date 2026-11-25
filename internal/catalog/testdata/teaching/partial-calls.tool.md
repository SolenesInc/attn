Ask another agent to run `agent_close({self: true, …})`, send with `agent_msg({target: "<seed-id>", …})`, press `terminal_send({keys: ["Enter"], …})`, read `terminal_read({mode: "last", …})`, then take it back with `seed_harvest({when_merged: true, clear: true, …})`.

```text
agent_close({self: true, …})
agent_close({self: true, reason: "done"})
```

A target machine takes `host: "ai-sandbox", cwd: "~/project"`:

```text
delegate({seed: "s-1", role: "builder", cwd: "/repo"})
```

Settle it with `seed_check_uncheck({seed: "<id>", id: "<C#>"})` or `seed_check_reject({seed: "<id>", id: "<C#>", …})`.
