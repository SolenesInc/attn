Sigma workers (`start_worker`, listed with `lineage.task`) are now adopted as visible Attn sessions of their own on the home machine instead of rolling up into their parent session.

- Each worker under an open Attn session becomes one session titled by `lineage.title` from any of three triggers (`status/watch`, `conversation/list` scans, or CLI/MCP caller authentication), opening a tile beside its parent without stealing focus.
- Worker sessions track their own state, files, pull requests, and spend from zero (including late adoption after a daemon restart and resume after close), while hosted subagents remain folded into the session holding their tree root.
- Trade-off: worker sessions open visible for now until a dedicated worker UI exists, and worker adoption runs on the home machine only until outpost adoption (`s-sgb9a9`) lands next.
