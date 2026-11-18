# Real-app harness

Packaged-app scenarios. Set up an instance per
[instances](../../../docs/internal/instances.md) and run commands from the
repository root.

## Running

- On one display, scenarios run serially:
  `pnpm --dir app run real-app:serial-matrix`. A second run waits for the lock.
  On a shared Mac, invoke a focus-free scenario directly.
- On Linux, `pnpm --dir app run real-app:parallel-matrix -- --workers N` runs
  N serial matrices at once, each on its own Xvfb display against its own
  instance (`acc1`..`accN` by default; `--instance-prefix` changes it). Install
  each instance from the checkout first: `make install INSTANCE=acc1`, and so
  on. A run holds its instances until it ends, so a second run that needs one
  waits. Each run writes its workers' output to
  `<artifacts>/parallel-<time>/w<k>/`, so earlier runs' evidence stays. Each
  run replaces the combined `<artifacts>/last-matrix.json` and
  `matrix-digest.txt`.
- The instance comes from `ATTN_HARNESS_INSTANCE`, then `ATTN_INSTANCE`, then `dev`.
  An `--app-path` that names another instance is refused. Production needs an
  empty `ATTN_HARNESS_INSTANCE=`, `--run-against-prod`, and explicit approval.
- Install the current checkout first; a stale build fails its fingerprint check.
- `scripts/land.sh` runs each native app scenario a landing adds or changes on
  CI's Linux before the push, through the Acceptance soak workflow, or on the clone's
  [landing runner](../../../docs/internal/linux-runner.md#landing-runner). When the landing also
  changes product code, it runs the scenarios listed by each feature file the
  landing changes in `.agents/skills/drive-attn/features/`. Neither the landing
  nor CI runs the Tauri app's scenarios.
- Hunt CI flakes with
  `gh workflow run acceptance-soak.yml --ref next -f scenarios=<ids>`.
  `scripts/ci-flake-report.sh` ranks failing tests across CI history.
- Linux VM: `pnpm --dir app real-app:linux provision`
  ([linux-runner](../../../docs/internal/linux-runner.md)). Run repeat runs,
  soaks and reproductions on `attn-linux` with `--provider ssh --target attn-linux`,
  never on the landing runner, which landings queue for.
- Platform skips go in the catalog entry as `skipOn` with a reason. A product
  failure on Linux is a finding, not a skip.

## Writing scenarios

- New scenario files get a `scenarioCatalog.mjs` entry. `scenario-durations.json`
  weighs each scenario for the shard plan. Give a new scenario a weight by hand
  until CI has run it, then regenerate the file from CI's receipts:
  `go tool test-history sync && node app/scripts/real-app-harness/measure-scenario-durations.mjs`.
  CI's shards run on Linux, so a scenario Linux skips keeps its hand weight.
- A scenario shares its instance with the scenarios before it in the shard, and
  which ones those are changes with the recorded durations. Leave no tile behind,
  and do not drive onto a desktop the scenario did not make. Before changing a
  setting, directly or through a key such as `shelf.fewerTiles`, queue its
  restore with `queueDaemonSettingRestore`. After each scenario the serial
  matrix puts back every stored setting the scenario changed, and fails the
  scenario when that changes a value the daemon shows.
- `runShellCommandInPane` requires a shell. For a mock agent, submit a prompt
  matched by its fixture and wait for the resulting reply.
- `profileCommand` awaits `profile_action_result`; CLI commands such as
  `terminal_move` use their CLI response contract instead.
- Drive the app like a user, through `createWindowDriver({ appPath, client })`.
  For the native app it returns a `NativeAppDriver`, whose input goes through
  the app's automation server: it takes no focus and leaves the pointer alone
  on both platforms. The driver finds, parks, places and captures the window
  with the platform driver. Shared-Mac runs keep the parked launch and never
  use activation or physical input; see the
  [drive-attn launch rules](../../../.agents/skills/drive-attn/SKILL.md#launch).
  Scenarios requiring real focus or system input run on a dedicated display.
- Point every pointer action at a target, an element's key or its role and
  name: `driver.click`, `hover`, `drag` and `typeInto` take one, and `at` aims
  at a fraction of the box. The app waits for the element to show and stop
  moving and scrolls it into view, so a scenario never measures geometry or
  waits for an animation before acting. A press replies with a receipt, and the
  driver fails the step when the press landed elsewhere; pass
  `{ allowMiss: true }` when that is the point. Every reply comes after the app
  handled the input, so the next read sees its effect.
  [Native app](../../../docs/internal/native-app.md#automation) lists the
  actions behind the driver: `waitFor`, `state`, `tree`, `atRest`, `pressKey`,
  `run` and the rest.
- New scenarios are native ones: give the catalog entry `app: 'native'`.
  `scenario-native-sidebar-session.mjs` is the smallest example. Entries
  without it are the Tauri app's and run only against a rollback build
  installed with `APP=tauri`.

### The Tauri app's driver

For the Tauri app, the rollback, `createWindowDriver` returns a driver that
aims at DOM selectors. On macOS it sends input without taking focus or moving
the pointer, except `menu`; on Linux, `xdotool` focuses the window the app
reports through the client and moves the pointer.

- Point every native pointer action at an element: `driver.click`,
  `rightClick`, `hover`, `drag` and `typeInto` take a selector or
  `{ selector, at, offset }` (`at` is a fraction of the box, default its
  center; `offset` adds CSS pixels). Before measuring, the app waits for the
  element to appear and for it and its ancestors to stop moving, and scrolls
  it into view when its point is offscreen or covered; `typeInto` also waits
  for the element to take focus before typing. The drivers send no input to
  raw coordinates, so a scenario never measures geometry or waits for an
  animation before acting. `get_pane_cell_rect` returns a terminal cell's
  `target`; a `drag` can end `{ by: { x, y } }` CSS pixels from its start;
  `holdDrag` presses and drags with the button still down, returns `dragTo` and
  `release`, and lets a scenario press keys or read the screen mid-drag;
  Quick Capture targets add `webview: 'capture'`, and its driver takes
  `systemInput: true` to keep system-level input.
- Native input waits for delivery. `click`, `rightClick`, `drag`, `hover`,
  `typeInto`, `pressKey`, `pressKeyCode`, `pressEnter` and `typeText` return
  only after the webview that should receive the input sees the trusted
  mouseup, the trusted mousemove for `hover`, or one keyup per stroke, so the
  next read sees the input's effect. Pointer input arms its target's webview;
  keys arm the main webview. Pointer actions return the `receipt`;
  `receipt.matches` says whether the event landed on the aimed selector. Input
  that lands in a browser tile page, starts a native window drag, or replaces
  the page receiving it has no receipt, and neither does a system-input click
  on a macOS window that is not key: that click only makes the window key. Nor
  do keys from a `systemInput: true` driver, which reach whichever window is
  key and paste text as one keystroke; the driver refuses them unreceipted.
  Pass `{ withoutReceipt: '<why>' }` in the last argument, after `modifiers`
  for `pressKey` and `pressKeyCode`.
- On macOS, the in-app `pressKey` hands a chord straight to the focused
  webview and the menu, so it skips AppKit's event queue and the app's local
  key monitor, which is where a browser tile's page keys are taken. Pass
  `{ queued: true }` to post the key into the queue instead, as a real key
  arrives. A queued key has no receipt, so wait for its effect.

### Agents, clocks and verdicts

- Scenarios built on `createScenarioRunner` launch the mock agent for `claude`
  and `codex`. Script its turns with `writeMockAgentFixture` in the session cwd
  before launch; no fixture means a silent agent. Real providers need
  `allowRealAgents` and a reason.
- In those scenarios the agent tripwire fails any real agent or headless model
  task. `allowRealAgents: ['codex']` exempts only the named agents; `true`
  exempts every agent and turns headless tasks back on.
- Those scenarios talk to the mock GitHub (`scripts/mock-github.mjs`) on
  non-production instances; production keeps the real github.com. Seed custom
  PRs through `/__control/seed`.
- Hand-run scripts outside `createScenarioRunner` have none of these guards and
  may launch real providers; read one before running it.
- Build child environments with `instanceCliEnv`, never `{ ...process.env }`.
- A scenario that waits on a daemon timer, such as an outpost's liveness
  timers or an automation schedule, steps that daemon with `stepClock` instead
  of waiting. Supply each clock's `env` through the daemon's child environment
  or the app's `launchEnv`; quit the old app before stopping its daemon so
  relaunch picks up that environment. Stop the daemons before closing their
  clocks. A stepped daemon's transcript watchers poll only when its clock
  advances.
  - Outpost scenarios advance around observed bridge states. Await a fresh
    timer registration by its name, such as `bridge-retry`, before advancing an
    asynchronous retry or a dial whose answer is deliberately held. To check
    a connecting notice, hold new requests at the fake SSH boundary and release
    them once the check is done; a paused daemon's health check rejects new
    connections.
  - Heartbeats follow the stepped clock: a daemon sends one when its clock
    passes the heartbeat interval, and answers each one it receives at once.
    Advancing the home past `bridge-stall` while the outpost is stopped
    stalls the bridge. The outpost answers that step's heartbeat when it
    continues, which clears the stall.
  - Schedule ticks follow the Go wire tests: `awaitIdle`, `advance('1m')`,
    `awaitIdle`, then read the result. `awaitIdle` returns once the daemon's
    job runner has finished what the tick started.
- Take verdicts from the screen, the protocol, or the CLI, not the daemon DB;
  see [Testing](../../../docs/internal/testing.md#scenario). Read the DB, when a
  scenario must, through `queryDaemonDb`. Resolve pane ids from app state.
- Signal only PIDs from the automation manifest or spawned processes. Keep
  OS-specific behavior in `platform.mjs`.

## Reading results

- The verdict is the last `ATTN_VERDICT ` stdout line (hand-run scripts may not
  print one); `summary.json` has the rest.
- A scenario that calls `registerNativeFailureEvidence` saves what the open app
  showed when a step failed, before cleanup closes it: `failure-native.png` and
  `failure-evidence.json` (window and target geometry, the focused element,
  native focus and pointer, the UI diagnostics ring).
- Check pane text and window screenshots before diagnosing. The Tauri app's
  WebGL terminals need native window captures.
- Linux needs `xvfb-run`, `xdotool`, `xclip`, `sqlite3`, `fish`, `bash`, `zsh`
  and `nvim`.

## Recordings

Record a non-production instance and check clips for private data before publishing:

```bash
./scripts/pr-evidence.sh record --instance <name> --seconds 20 --out clip.mp4
./scripts/pr-evidence.sh publish clip.mp4
```

`ATTN_HARNESS_RECORD=1` records scenario segments. Recording is macOS-only.
