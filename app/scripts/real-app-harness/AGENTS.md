# Real-app harness

Read [profiles.md](../../../docs/profiles.md) before installation or verification.
Run commands from the repository root.

## Running scenarios

- Scenarios share one display; run serially. Batch with
  `pnpm --dir app run real-app:serial-matrix`.
- A scenario that cannot run on a platform says so in its catalog entry:
  `skipOn: { linux: '<reason>' }`, or `{ reason, unlessEnv }` when an
  environment variable proves the runner has what it needs (the remote `tr*`
  probes run on Linux once `ATTN_HARNESS_REMOTE_SSH_TARGET` names a target).
  The matrix digest lists every skip with its reason. A scenario that fails
  on Linux for a product reason is a finding, not a skip.
- A second run waits for the active one's lock, naming the holder. It waits as
  long as the holder is alive and heartbeating (a matrix can hold for hours) and
  gives up on a wedged holder (5 min without a heartbeat);
  `ATTN_REAL_APP_SCENARIO_LOCK_WAIT_MS` caps the total wait (0 fails fast).
- Profile: `ATTN_HARNESS_PROFILE` overrides `ATTN_PROFILE`, which defaults to `dev`.
  Production needs `ATTN_HARNESS_PROFILE=`, `--run-against-prod`, and explicit approval.
- `profileCliEnv` drops the routing overrides the shell inherited, so run the
  harness from inside an attn session without unsetting anything. Build every
  child's environment with it, never `{ ...process.env }`; only an override you
  pass it deliberately survives.
- Install the current checkout; source fingerprint mismatches fail.
- Remote target: `attn-remote@orb`; provision with
  `pnpm --dir app run real-app:provision-remote`.
  Provisioning installs the mock-agent command and four tripwire shims; it does
  not need provider credentials. The `tr*` probes are remote by definition; a
  scenario whose remote leg is optional runs it only once
  `ATTN_HARNESS_REMOTE_SSH_TARGET` names a target, so it never probes by default.

## Writing scenarios

- Exercise actual app actions/order; update scenarios when product flows change.
- Pressing native keys (`driver.press*`, `driver.typeText`, `pressShortcutKeys`)
  needs `process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0'` before the launch: macOS
  makes the always-on-top window non-focusable, so a keystroke reaches nothing.
  The macOS driver fails the press when it does not, and
  `alwaysOnTopSweep.test.mjs` fails a scenario that neither opts out nor states
  why it need not. Clicks, drags and `driver.menu` reach the window either way.
- Every screenshot is evidence: take it with `captureEvidenceScreenshot`, never
  a bare `.catch(() => {})`. A capture that hits its cap fails the run; one the
  app refused names the missing artifact in the trace and lets the run stand.
- The mock agent is the default agent. An armed scenario launches `mockAgent.mjs`
  for `claude` and `codex`: the tripwire pins both `ATTN_<AGENT>_EXECUTABLE` at it
  and `launchFreshAppAndConnect` writes the matching `<agent>_executable` setting,
  restoring what it found. Sessions need both halves — the env reaches the daemon,
  the setting reaches each spawn.
- A scenario needing a real provider says so with `allowRealAgents` and states why.
  That list shrinks; adding to it needs a reason in the catalog entry.
- Give the mock a turn with `writeMockAgentFixture` in the session cwd before the
  session starts. No fixture is a silent agent, not a broken one: the pane paints
  the splash and every prompt closes its turn with no reply.
- A turn with `submitHook: false` models injected input that no user submitted.
  It still records the turn and runs the stop hook, but emits no prompt-submit hook.
- A brief delivered on argv (`-- <prompt>`, how every delegation, crew wake and
  automation launch starts an agent) is the mock's first turn, matched against the
  same fixture. Its resume flags land in the transcript's `session_meta`.
- A fixture marked `resumable` places the transcript where the daemon's finders
  walk — codex at launch under the codex sessions tree, claude on its first turn
  under the tool home's project folder — so a resume launch finds it, replays the
  earlier turns into the pane and appends to that same file. Codex `/new` binds a
  successor rollout.
- Actions beyond `reply`/`delay`/`touch`/`wait_for_file`/`attn`: `capture` lifts a
  value out of the prompt (`pattern`, `name`) for `{{name}}` in a later `attn` or
  `exec` argument, and `exec` runs a command into the pane and the transcript,
  failing the turn on a non-zero exit unless `allowFailure`.
- The mock ends every turn with the real Stop hook and a `<!-- attn:state=… -->`
  marker; an action's `state` sets it (default: `waiting_input` after a reply,
  `idle` when the turn was silent). Arming turns headless tasks off, which is what
  makes the daemon read that marker instead of a model.
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
- `claude` and `codex` pin at the mock agent rather than at their shim, so an
  armed scenario gets a working agent instead of a dead session. Their shims stay
  on `PATH`, so a name-resolved exec still lands in the ledger. `copilot` and `pi`
  have no mock and pin at their shims.
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
  Every arming logs what it allowed. The pi and nisse scenarios carry `['pi']`
  because the attn-pi plugin execs `pi --version` as its health probe; none of
  them reaches a real model. `pi-automode` runs `pi` against the loopback stub,
  `nisse-markdown-stream` replays a recording, and the other nisse scenarios
  point the `attn-nisse` host at the stub (`startStubWorld` and `scriptedAgent`
  in `piStubProvider.mjs`).
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
- Remote probe scenarios arm a second tripwire on the fixture VM. Their launch
  environment puts the provisioned shim directory first, pins all four agent
  executables, and sets `ATTN_HEADLESS_TASKS=off`. Each scenario saves the
  remote daemon's environment receipt and copies the remote ledger into its
  local artifacts before it can pass. TR-502 and TR-504 launch the provisioned
  mock through the same command name on macOS and Linux.

## Mock GitHub

`scripts/mock-github.mjs` is the GitHub every harness run talks to. The daemon's
`refreshGitHubHosts` returns the moment `ATTN_MOCK_GH_URL` is set: it registers
that one host, drops every other, and never runs `gh` discovery, so the switch
alone keeps a run off github.com.

- `createScenarioRunner` ensures the server and puts `ATTN_MOCK_GH_URL`,
  `ATTN_MOCK_GH_TOKEN` and `ATTN_MOCK_GH_HOST` in the environment the app launch
  carries into the daemon. `open` drops env, so naming them forces the
  spawn-style launch.
- The receipt is the live daemon, not the harness's own intent: `finishSuccess`
  reads `ATTN_MOCK_GH_URL` out of the running daemon's environment, fails the
  scenario unless it is exactly the URL this run started, and records what it
  read in `summary.json`. `missing`, `no daemon` and another run's URL each fail
  by name.
- The port is `attn profile resolve --field mockGitHubPort`, so one server serves
  a whole matrix and a daemon between scenarios keeps working. A daemon carrying
  another URL is stopped the way a daemon predating the tripwire is.
- `--ensure` identifies a running server by a hash of the server source and its
  fixture. A match is reset to fixture state, so no scenario inherits the last
  one's `/__control` mutations; a mismatch — an interrupted run's server from
  another checkout — is stopped and replaced.
- The serial matrix starts it before the first scenario and stops it after the
  last. By hand: `pnpm --dir app run real-app:mock-github status|ensure|stop`.
- A production target is skipped: a mock there would empty the user's live PRs.
- `fixtures/github-snapshot.json` is the default seed — 14 synthetic PRs, sized
  to keep an app-launch detail fetch (2 requests each, plus 3 searches) inside
  the client's 60-request burst. A scenario wanting its own set posts to
  `/__control/seed`; `/__control/requested` and `/__control/head` drive the
  automation scenarios' review request and head SHA.

## Reading results

- Read the last `ATTN_VERDICT ` stdout line; hand-rolled `main()` scenarios omit it.
- Inspect captured pane text and native screenshots before diagnosing failures.
- Dark/locked screens block input. Check `pmset -g log | rg "Display is turned"`.
- Linux input needs `DISPLAY` and `xdotool`; run scenarios through `xvfb-run` in CI.
  A Linux runner also needs `sqlite3`, `fish`, `bash`, `zsh`, `pi`, `xclip` on PATH, and
  `attn plugin install-bundled attn-pi` run once in the profile.
- Use `capture_screenshot_data` for DOM pixels. WebGL terminal evidence needs a
  native window capture (`import -window` on Linux).

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
Recording is unsupported on Linux; the harness names that and continues.
