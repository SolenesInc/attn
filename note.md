A tool's structured result from attn's MCP server now also arrives as JSON text. Codex code-mode scripts that print `r.content` used to get `[]` and ask again.

References now show the plain `seed harvest` and `crew create` calls, and `seed_plot`'s schema requires each check's id. The agent bench records API requests per run and the first request's tokens, beside turns.

In Codex code mode, a script that prints a whole result prints both copies. No Codex run was made: the Codex side is shown by its source and a wire test.
