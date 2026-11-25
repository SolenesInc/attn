Agent bench measurements, made valid before the attn tools grid runs. `attn session transcript` now shows the arguments of a Codex `function_call`, which it dropped.

The rest is the bench driver in `scripts/agent-bench`, which a person runs. A tool is matched by its catalog command. Tool calls, CLI calls and their refusals come from the daemon's log and real shell lines. A run whose model, tools setting or code mode is not its cell's is a fault. Probes judge the end state. Sigma is judged paired by task, not per task. Each run has its own Notebook.
