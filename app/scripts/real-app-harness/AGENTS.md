# Real-app harness

[Profiles](../../../docs/profiles.md) covers installation, verification
requirements, preflight, and cleanup. Run the commands below from the repository root.

## Running scenarios

- Run packaged-app scenarios serially; they share one display. Use
  `pnpm --dir app run real-app:serial-matrix` for multiple scenarios.
- The harness uses `ATTN_PROFILE`, falling back to `dev`.
  `ATTN_HARNESS_PROFILE` overrides it. Production requires both
  `ATTN_HARNESS_PROFILE=` and `--run-against-prod`, plus the
  [production approval](../../../AGENTS.md#you-are-probably-running-inside-attn).
- A source fingerprint mismatch rejects a stale installed build.
- Remote scenarios target the local OrbStack VM (`attn-remote@orb`). Provision
  it with `pnpm --dir app run real-app:provision-remote`.

## Writing scenarios

- A scenario models what a user can do in the app, in the app's order. When
  the product flow changes, the scenario changes in the same PR. A scenario
  that passes while a user can reproduce the error is a test bug.
- Never pin `fable`. `launchFreshAppAndConnect` pins each agent's cheapest
  model with low effort and restores both prior values at exit;
  `ATTN_HARNESS_LAUNCH_MODEL_<AGENT>=inherit` is the expensive path and has to
  be asked for by name. A scenario that needs a stronger model pins it after
  the launch helper with a comment saying why.
- Give crew fixtures obviously synthetic names and pin them to
  `claude-haiku-4-5`. Use a stronger model only when the scenario needs it.
- Use `mockAgent.mjs` for every scenario whose result does not depend on a real
  provider. `writeMockAgentFixture` scripts its turns and `configureMockAgent`
  swaps the executable for the run. The daemon, worker PTY, hooks, transcript,
  and renderer stay real. A real agent belongs only in a scenario that tests
  that provider integration, and the scenario says why.
- A visible pane is a session pane. Resolve pane ids from daemon or app state.
  An empty workspace is invalid state; a scenario that creates one asserts it
  is removed. Shortcut scenarios use the app's shortcut registry ids.
- Every OS branch lives in `platform.mjs`: install-tree layout, how the app is
  launched, how a window is observed, how it is quit. Add a case there, never an
  `if (process.platform)` at a call site. A pid comes from the automation
  manifest or from the spawn, never from a command-line pattern, and a manifest
  pid is signalled only while `/proc` still shows it running the install tree's
  executable.

## Reading results
- Read the result from the last `ATTN_VERDICT ` line on stdout
  (`emitVerdict` in `common.mjs`). Scenarios with a hand-rolled `main()` print
  none.
- Inspect captured pane text and native screenshots before diagnosing a failure.
- A failure that looks like a product regression may be the display: the
  input driver refuses to post input to a dark or locked screen and names the
  condition; `pmset -g log | rg "Display is turned"` has the history.

## Recordings

Record the profile installed for verification:

```bash
./scripts/pr-evidence.sh record --profile <name> --seconds 20 --out clip.mp4
./scripts/pr-evidence.sh publish clip.mp4
```

`record` captures `attn-<name>.app`; `--app <owner>` selects a different window.
`publish` uploads an MP4 and GIF to `victorarias/attn-pr-evidence` and prints
Markdown for the PR description.

The evidence repository is public. Watch the clip for private session names,
transcripts, or work items before publishing; re-record if any appear. The GIF
renders inline and the MP4 provides full quality. GitHub's image limit is 10MB,
so keep clips around 20 seconds and heed the script's size warning.
[Rendering examples](https://github.com/victorarias/attn-pr-evidence/issues/1)
show the supported embed forms.

Set `ATTN_HARNESS_RECORD=1` to record every `createScenarioRunner` scenario,
including serial-matrix and soak legs. It writes `recording-NN.mp4` segments
in the artifacts directory; publish them with the same command.

Recorded runs use `~/Applications/attn-window-recorder.app`, installed with
`make install-window-recorder`. Its stable bundle id preserves the macOS Screen
Recording grant across worktrees. The harness rejects a stale recorder and
names the install command.
