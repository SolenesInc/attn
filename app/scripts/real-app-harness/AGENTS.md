# Real-app harness

The root `AGENTS.md` ("Packaged-app harness") covers profiles, single tenancy,
the production guard, recordings, and the remote VM. Rules that live only here:

- A scenario models what a user can do in the app, in the app's order. When
  the product flow changes, the scenario changes in the same PR. A scenario
  that passes while a user can reproduce the error is a test bug.
- Never pin `fable`. `launchFreshAppAndConnect` pins each agent's cheapest
  model with low effort and restores both prior values at exit;
  `ATTN_HARNESS_LAUNCH_MODEL_<AGENT>=inherit` is the expensive path and has to
  be asked for by name. A scenario that needs a stronger model pins it after
  the launch helper with a comment saying why.
- Use `mockAgent.mjs` for every scenario whose result does not depend on a real
  provider. `writeMockAgentFixture` scripts its turns and `configureMockAgent`
  swaps the executable for the run. The daemon, worker PTY, hooks, transcript,
  and renderer stay real. A real agent belongs only in a scenario that tests
  that provider integration, and the scenario says why.
- A visible pane is a session pane. Resolve pane ids from daemon or app state.
  An empty workspace is invalid state; a scenario that creates one asserts it
  is removed. Shortcut scenarios use the app's shortcut registry ids.
- Read the result from the last `ATTN_VERDICT ` line on stdout
  (`emitVerdict` in `common.mjs`). Scenarios with a hand-rolled `main()` print
  none.
- A failure that looks like a product regression may be the display: the
  input driver refuses to post input to a dark or locked screen and names the
  condition; `pmset -g log | grep "Display is turned"` has the history.
- Recorded runs use `~/Applications/attn-window-recorder.app`, installed with
  `make install-window-recorder`. Its stable bundle id keeps the macOS Screen
  Recording grant across worktrees; the harness refuses a stale install and
  names that command instead of rebuilding a permission-sensitive binary in
  the checkout.
