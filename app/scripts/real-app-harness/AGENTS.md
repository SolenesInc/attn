# Real-app harness

Read [profiles.md](../../../docs/profiles.md) before installation or verification.
Run commands from the repository root.

## Running scenarios

- Scenarios share one display; run serially. Batch with
  `pnpm --dir app run real-app:serial-matrix`.
- Profile: `ATTN_HARNESS_PROFILE` overrides `ATTN_PROFILE`, which defaults to `dev`.
  Production needs `ATTN_HARNESS_PROFILE=`, `--run-against-prod`, and explicit approval.
- Install the current checkout; source fingerprint mismatches fail.
- Remote target: `attn-remote@orb`; provision with
  `pnpm --dir app run real-app:provision-remote`.

## Writing scenarios

- Exercise actual app actions/order; update scenarios when product flows change.
- Use `mockAgent.mjs`, `writeMockAgentFixture`, and `configureMockAgent`
  unless testing a provider integration; state why a real provider is needed.
- The mock ends every turn with the real Stop hook and a `<!-- attn:state=… -->`
  marker; an action's `state` sets it (default: `waiting_input` after a reply,
  `idle` when the turn was silent). `configureMockAgent` turns headless tasks
  off, which is what makes the daemon read that marker instead of a model. It
  registers its rollback before the first write and restores the *stored*
  values, and refuses by name when `ATTN_HEADLESS_TASKS` forces them back on.
- `launchFreshAppAndConnect` pins cheap models/low effort and restores settings.
  Never pin `fable`. `ATTN_HARNESS_LAUNCH_MODEL_<AGENT>=inherit` needs explicit request.
  Stronger scenario-specific models need an explanatory comment.
- Crew fixtures use synthetic names and `claude-haiku-4-5` unless stronger reasoning is required.
- Resolve pane ids from app/daemon state. Assert empty workspaces are removed.
  Shortcuts use registry ids.
- Keep OS-specific install paths, launch, observation, and quit behavior in
  `platform.mjs`. Use automation-manifest or spawned PIDs; verify manifest PIDs
  still run the installed executable before signalling them.

## Agent tripwire

`agentTripwire.mjs` shims `claude`, `codex`, `copilot` and `pi` so a scenario
that must call no model fails when a real agent binary is exec'd. A shim appends
`<scenario>\t<argv>` to `<run-dir>/agent-tripwire.ledger` and exits 97; the runner
fails the scenario on a non-empty ledger and prints the lines.

- The shims reach the app, the daemon it spawns, and the harness's own `attn`
  calls two ways: the shim dir first on `PATH`, and `ATTN_<AGENT>_EXECUTABLE`
  pins. Sessions need the pins — the login shell rebuilds `PATH` (see
  `internal/pty/manager.go`), so only the pins survive that hop.
- A command a scenario types by hand into a shell pane resolves on the login
  `PATH`, where a real agent binary can sit ahead of the shim dir. The tripwire
  covers every agent attn itself launches, not that.
- The daemon outlives a scenario, so the shim dir is stable and a `current-run`
  pointer file attributes execs to the scenario running now.
  `ensureDaemonCarriesTripwire` stops a daemon that predates the tripwire
  (never on a production target) so the app relaunch brings up an armed one.
- Every `createScenarioRunner` caller declares what it may run: a
  `scenarioCatalog.mjs` entry carrying its `runnerId`, or `allowRealAgents` in
  the runner options, which wins over the catalog. `false` arms everything,
  `true` allows all four, an array names the ones the scenario needs. A runner
  id neither covers fails at construction rather than defaulting to permissive.
  Every arming logs what it allowed. The pi scenarios carry `['pi']` — they exec
  the real `pi` binary against a stub provider or a recording, and keep
  claude/codex/copilot armed.
- Arming also sets `ATTN_HEADLESS_TASKS=off`, so the daemon refuses narration,
  classification, titling and every other headless LLM task (`internal/headless`)
  instead of enqueueing one. Without it the ledger check races the daemon:
  `narrate_workspace` debounces two minutes and retries, so its `claude --print`
  lands after `summary.json`, and the single `current-run` pointer stamps it with
  whichever scenario is armed by then. A scenario that allows real agents keeps
  headless tasks on.
- `summary.json` carries `headlessTasks`, read from the environment of the
  daemon the scenario ran against, so a green run states the switch was in force
  rather than leaving it assumed. Counting `headless task refused` lines instead
  does not work: the daemon logs them as a scenario tears its sessions down, in
  the same second `summary.json` is written.
- An armed scenario fails closed on both ends. At arm time, a running daemon
  whose environment cannot be read, or one this harness may not stop (a
  production target is never restarted), fails the scenario before it starts,
  naming the pid, what was read and what was expected. At the end, `ok: true`
  requires the daemon's environment to carry both this run's
  `ATTN_AGENT_TRIPWIRE` marker and `ATTN_HEADLESS_TASKS=off`; a switch reading
  `on`, `no daemon` or `unreadable`, or a marker from another run, fails the
  scenario with the value in the digest. A scenario allowing real agents keeps
  the old warn-and-continue: it has nothing to prove.

## Reading results

- Read the last `ATTN_VERDICT ` stdout line; hand-rolled `main()` scenarios omit it.
- Inspect captured pane text and native screenshots before diagnosing failures.
- Dark/locked screens block input. Check `pmset -g log | rg "Display is turned"`.

## Recordings

Record the installed verification profile; watch for private data before publishing
to the public evidence repository:

```bash
./scripts/pr-evidence.sh record --profile <name> --seconds 20 --out clip.mp4
./scripts/pr-evidence.sh publish clip.mp4
```

`publish` uploads MP4/GIF and prints PR Markdown. Re-record private content;
keep clips around 20 seconds and heed the 10MB GIF warning.
`ATTN_HARNESS_RECORD=1` writes `recording-NN.mp4` segments to scenario artifacts.
Install/update the recorder with `make install-window-recorder`; its stable
bundle preserves macOS Screen Recording permission.
