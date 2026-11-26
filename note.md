Fixes the red `native-codex-update` on main. No product change.

- The app acceptance observer's `terminalOf` threw while no binding was known, so no `waitFor` around it ever waited. It now returns `null`, like the observer's other lookups. A session can reach the observer before its binding, because the daemon sends `session_state_changed` during a spawn.
- Also fixes a second, older flake in `native-codex-shared` (4 of 10 runs failed at `ac8c5c19d`): it could read the conversation from the screen as it was before a switch. The mock Codex now prints what it switched from, and the scenario waits for that line.
