# Real-app harness

Packaged-app scenarios. Set up a profile per [profiles](../../../docs/profiles.md)
and run commands from the repository root.

## Running

- Scenarios share one display and run serially:
  `pnpm --dir app run real-app:serial-matrix`. A second run waits for the lock.
- The profile comes from `ATTN_HARNESS_PROFILE`, then `ATTN_PROFILE`, then `dev`.
  Production needs `--run-against-prod` and explicit approval.
- Install the current checkout first; a stale build fails its fingerprint check.
- Hunt CI flakes with
  `gh workflow run acceptance-soak.yml --ref next -f scenarios=<ids>`.
  `scripts/ci-flake-report.sh` ranks failing tests across CI history.
- Linux VM: `pnpm --dir app real-app:linux provision` ([linux-runner](../../../docs/linux-runner.md)).
  Remote scenarios need `ATTN_HARNESS_REMOTE_SSH_TARGET`.
- Platform skips go in the catalog entry as `skipOn` with a reason. A product
  failure on Linux is a finding, not a skip.

## Writing scenarios

- New scenario files get a `scenarioCatalog.mjs` entry. After a shape change,
  update its weight in `scenario-durations.json` from a green run.
- Drive the app like a user, through `createWindowDriver({ appPath, client })`.
  On macOS it sends input without taking focus or moving the pointer, except
  `menu` and scrolling; on Linux, `xdotool` focuses the window and moves the
  pointer. A scenario that needs real focus calls `driver.activateApp()` and
  says why.
- Scenarios run the mock agent, not real models. Script its turns with
  `writeMockAgentFixture` in the session cwd before launch; no fixture means a
  silent agent. Real providers need `allowRealAgents` and a reason.
- The agent tripwire fails a scenario that runs a real agent or headless model
  task. `allowRealAgents: ['pi']` exempts only the named agents; `true` exempts
  every agent and turns headless tasks back on.
- Non-production runs talk to the mock GitHub (`scripts/mock-github.mjs`), never
  github.com. Production runs keep the real github.com.
  Seed custom PRs through `/__control/seed`.
- Build child environments with `profileCliEnv`, never `{ ...process.env }`.
- Read the daemon DB through `queryDaemonDb`. Resolve pane ids from app state.
- Signal only PIDs from the automation manifest or spawned processes. Keep
  OS-specific behavior in `platform.mjs`.

## Reading results

- The verdict is the last `ATTN_VERDICT ` stdout line; `summary.json` has the rest.
- Check pane text and native screenshots before diagnosing. WebGL terminals need
  native window captures.
- Linux needs `xvfb-run`, `xdotool`, `xclip`, `sqlite3`, `fish`, `bash`, `zsh`,
  and `pi`, plus `attn plugin install-bundled attn-pi`.

## Recordings

Record a non-production profile and check clips for private data before publishing:

```bash
./scripts/pr-evidence.sh record --profile <name> --seconds 20 --out clip.mp4
./scripts/pr-evidence.sh publish clip.mp4
```

`ATTN_HARNESS_RECORD=1` records scenario segments. Recording is macOS-only.
