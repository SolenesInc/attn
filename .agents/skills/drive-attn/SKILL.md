---
name: drive-attn
description: Drive attn's CLI, daemon and app on a non-production instance the way people and agents use them. Use to check a change to attn live before calling it done, or to find every way into a feature (keys, palette, CLI, the prompts agents read, triggers) with its driving recipe, contexts and tests.
---

# Drive attn

This skill is for maintainers: the agents and people changing attn. Use it to
start attn on a named instance, drive a feature through each way in, and keep
the proof. `docs/internal/instances.md` and
`app/scripts/real-app-harness/AGENTS.md` hold the detail. This file holds the
commands you need at hand.

Keep production out of every run. Never point a daemon, app, recipe, harness
or test at `~/.attn` or the production app. For a feature that needs
realistic data, copy the data out read-only. Use the `attn` on `PATH` only
for your own garden work on your stake, from your own session, as
[Evidence](#evidence) shows and `AGENTS.md` allows. Never stop a process
yourself, by PID or by name. `attn instance clean` stops everything an
instance runs, and you end every run with `attn instance clean`, as
[Cleanup](#cleanup) says.

## Launch

Run from the repository root:

```bash
make build                                              # ./attn from this checkout
.agents/skills/drive-attn/scripts/write-env.sh <name>   # writes /tmp/drive-attn-<name>.env
. /tmp/drive-attn-<name>.env && pnpm --dir app run real-app:mock-github ensure
```

Every command after `write-env.sh` runs in the instance shell, which means
the command starts with `. /tmp/drive-attn-<name>.env &&`. Agents' command
tools start a fresh shell for each command (Claude Code keeps only the
working directory, and Codex starts a new process), so a selection, variable
or function from one command is gone by the next. Without the prefix,
`./attn` acts on production as your own session, `wsctl` and `bridge` are
undefined, and the mock GitHub reaches the `dev` instance. A missing file
stops the command, and the file ends with a guard that fails unless
`ATTN_INSTANCE` is `<name>`. In a shell that keeps its state, such as a
person's terminal, source the file once in a terminal of its own instead, so
your own `attn` keeps pointing at the attn you run inside. Code blocks here
show the prefix. Inline commands, here and in the feature map, leave the
prefix out.

Pick a name of your own (`[a-z0-9][a-z0-9-]{0,15}`), not `dev`, because `dev`
is shared with other work and installs the attn skill into your harness
homes. Keep a list of every name you start. Clean each name as
[Cleanup](#cleanup) says, including when you abandon the run.

Sourcing the env file selects the instance with `attn instance-env <name>`,
which clears inherited routing, and moves to this checkout. The file's
`unset` line drops the identity of the session you run in, so the instance
does not mistake your shell for one of its terminals. With the exports in
the file, the daemon launches the mock agent for `claude` and `codex`,
refuses headless model tasks, and talks to the mock GitHub. `ATTN_TOOL_HOME`
gives the instance a scratch harness home, so a mock conversation that a
fixture makes resumable lands there and not in your `~/.claude` or
`~/.codex`. `ATTN_ANALYTICS_INGEST_URL` points
analytics uploads at an address that goes nowhere, so turning sharing on
cannot reach the real telemetry service. The file also defines `wsctl`,
`bridge` and `DRIVE_ENV`, the path of the file itself, which recipes append
their variables to ([features/README.md](features/README.md)). Running
`write-env.sh` again starts the file over.

Daemon only, for CLI and protocol work:

```bash
. /tmp/drive-attn-<name>.env && ./attn daemon ensure   # {"status": "started"}, "already_running" or "restarted"
```

Full app. `make install` starts the instance's daemon with the instance
shell's environment:

```bash
. /tmp/drive-attn-<name>.env && make install INSTANCE=<name>
. /tmp/drive-attn-<name>.env && bridge --launch --wait-ready ping   # {"frontendReady": true, "pong": true}
```

`--launch` starts the app with the instance shell's environment and parks
the app window at the screen's edge. Keep that default on a shared Mac. Never
activate or unpark the window, set the parking or always-on-top variables
yourself, or send physical input. That includes `open`, `make run`, `activateApp`,
`osascript ... activate`, `cliclick` and the driver's `menu`.
Use `native_key` for menu shortcuts and DOM scrolling for setup. A native
input failure is a bridge or harness bug to diagnose, not permission to take
focus. A scenario that requires activation or system input needs a dedicated
display. Such a scenario cannot prove a shared-Mac recipe. On macOS,
`make run INSTANCE=<name>` opens the app through Launch Services instead,
which drops these variables. If the
daemon started without them (the doctor shows a real agent's executable), or
a recipe needs another variable in the daemon's environment, quit the app
with `./attn instance stop-app`, stop the daemon with
`"$(./attn instance resolve --field appDaemon)" daemon stop`, append the
missing `export` line to the env file, then launch again.

## Doctor

One read-only check. With the app installed, through the instance's own
binary (`appDaemon` is a file inside the installed app, so this fails with
`no such file or directory` on a daemon-only instance):

```bash
. /tmp/drive-attn-<name>.env && "$(./attn instance resolve --field appDaemon)" preflight --agent claude
```

The check must report `PASS` overall, with `routing.daemon instance=<name>`,
`protocol.cli_app` and `protocol.app_daemon` (the installed app, CLI and
daemon speak one protocol), `tool.mockAgent.mjs` (the daemon launches the
mock, not a real Claude) and `headless.tasks daemon headless tasks off`.
Most verbs, `seed`, `agent`, `daemon` and `preflight` among them, also print
`[attn instance=<name> ...]` on stderr. On those verbs, no banner means no
instance is selected. `./attn instance` shows the selection instead. To check
the build, the `version --json` of the same binary must show the
`sourceFingerprint` that `bash scripts/source-fingerprint.sh` prints for your
tree, and `bridge --wait-ready` refuses an app built from other source. For a
daemon-only instance, run the check through `./attn`:

```bash
. /tmp/drive-attn-<name>.env && ./attn preflight --agent claude
```

The two `protocol.` checks fail because no app is installed, so the check
prints `FAIL (2 failed, 0 warnings)` and exits 1. Everything else must pass.

## Drive

| Harness | Entrypoint | Reaches | Cannot reach |
| --- | --- | --- | --- |
| `./attn <verb>` in the instance shell | A person's own shell. Agents, for verbs that need no session | Every CLI verb, `--json` output, exit codes | The app. Verbs that act as a session, such as `seed tend`, `seed watch` and `terminal`, refuse without one |
| Mock agent | Agents | The CLI and prompts as a session in the instance sees them | Model judgment: it does only what its fixture says |
| `wsctl` | The daemon protocol | Sessions, desktops and tiles (`wsctl list`), spawning, terminal input, a seed page's Resume (`seed-continuation`, `seed-resume`) | What the app shows |
| `bridge` | People | Native keys, text and clicks inside the window, UI state, screenshots | The macOS menu bar |
| Scenarios | People, as committed proof | The packaged app, through `createWindowDriver({ appPath, client })`, menus included | A quick local loop: CI runs them |

**Mock agent.** The daemon launches `mockAgent.mjs` for `claude` and `codex`.
The mock agent reads `.attn-mock-agent.json` from the session's folder at
launch. Without the file, the mock agent stays silent. Each turn matches a prompt by `includes` and runs its
actions: `attn` runs an attn verb as that session, `reply` prints text,
`capture` keeps part of the prompt for `{{name}}`.

```json
{"version": 1, "turns": [
  {"includes": "tend", "actions": [
    {"type": "capture", "name": "seed", "pattern": "(s-[a-z0-9]+)"},
    {"type": "attn", "args": ["seed", "tend", "{{seed}}"]}
  ]}
]}
```

```bash
. /tmp/drive-attn-<name>.env && wsctl add-session --cwd <dir> --agent claude --label <label> --id <session>
. /tmp/drive-attn-<name>.env && wsctl list                    # terminal id: desktops[].panes[].runtime_id
. /tmp/drive-attn-<name>.env && wsctl input --id <terminal> --text "tend s-xxxxxx" --enter
. /tmp/drive-attn-<name>.env && ./attn agent peek <session>   # the agent's screen, as text
```

**bridge.** Reads: `get_state`, the feature reads such as `garden_get_state`,
`dom_text '{"selector":"..."}'`, and `dom_wait
'{"selector":"...","timeoutMs":5000}'`, which waits for a selector. Native
input: `native_key '{"key":"t","modifiers":["command","shift"]}'`,
`native_text '{"text":"..."}'`, `native_mouse
'{"action":"click","x":0.5,"y":0.5}'` with window-relative coordinates from
`dom_native_point '{"selector":"..."}'`.
Capture: `capture_screenshot '{"path":"<file>"}'`. DOM actions (`dom_click`,
`dispatch_shortcut`, `garden_toggle_frame` and the like) skip native input:
use them to set up, never as proof of a person's way in. The actions are the
`case` labels in `app/src/hooks/useUiAutomationBridge.ts` and
`app/src/hooks/uiAutomationDomActions.ts`, the native ones in
`app/src-tauri/core/src/native_input.rs` and
`app/src-tauri/core/src/ui_automation.rs`, and quick capture's in
`app/src/quickCapture/automationBridge.ts`.

**Scenarios.** Read the scenario and the harness guide before running one.
On a shared Mac, use a focus-free scenario directly, such as
`node app/scripts/real-app-harness/scenario-focusless-native.mjs`, or the
serial matrix with `--no-fresh-world --scenario <id>`. The default matrix
calls `freshWorld.mjs`, whose pattern-based worker cleanup violates the
repository's PID rule. Inspect other scenarios for activation, physical input
and fresh-world calls too. Native captures and recordings need Screen
Recording permission for the instance's app or your terminal.

## Evidence

- Take verdicts from the screen (`bridge` reads and captures), the protocol
  (`wsctl list`) or the CLI (`./attn ... --json`), never from the daemon
  database.
- Drive the real path: native input for people, the mock agent's `attn`
  action for agents, the real event for a trigger. Mocks stand in only
  where attn already meets the outside: agent binaries, GitHub, headless
  model tasks.
- Capture the action and the resulting state, not only the final screen:
  the command or input, its output and exit code, then a read of the state.
- Check side effects from a second view: read a change made in the app back
  through the CLI, and the reverse.
- Keep proof in a folder you name in your report
  (`mktemp -d /tmp/drive-attn-<feature>.XXXX`), or on your stake. From your
  own session, in a command without the instance shell's prefix, run
  `attn seed check report <stake> <C#> -m "<summary>" --evidence <file>`,
  which copies the file onto the stake and cites it.
- Recordings: `./scripts/pr-evidence.sh record --instance <name> --seconds 20
  --out <dir>/clip.mp4`, then `./scripts/pr-evidence.sh publish <dir>/clip.mp4`.
  Watch the clip for private data before publishing. macOS only.

## Cleanup

Every run ends here, finished or abandoned, before you report, hand off or
move on. For each instance you started:

```bash
.agents/skills/drive-attn/scripts/clean.sh <name>
```

The script sources the env file, runs `./attn instance clean <name>`, stops
the instance's mock GitHub, and removes the scratch tool home,
`$ATTN_TOOL_HOME`, and then the env file. The script stops at the first
command that fails and keeps the env file, so you can run the script again.
Close a terminal that sourced the env file.

`clean` quits the app, stops the daemon, reaps the daemon's workers, shared
PTY hosts and plugin drivers, and removes the app and the data folder.
`clean` refuses the default (production) instance. Never pass `--force` to
`clean`. If `clean` reports a live app, run `./attn instance stop-app` in the
instance shell and run `clean.sh` again. If `clean` reports anything else it
could not stop, leave the process alone and bring `clean`'s output to the
user. Never stop the process by PID or name, and never delete a data folder
by hand.

`clean` leaves the instance's notebook folder, `~/attn-notebook-<name>`, in
place, with whatever the run wrote to the notebook or attached to seeds. A
later instance with the same name starts with that folder. Proof in your own
folder or on your stake survives cleaning. Check that the proof is still
there after cleaning.

## The feature map

Read `features/README.md`, which holds the driving conventions, then
`features/<id>.md` for the feature you changed. Each feature file lists the
feature's ways in, a recipe that drives the ways in, the contexts that the
feature answers, and where the feature's tests live. Drive every way in that
the file lists before you call a change done. When you find a way in, a trap
or a test that the file lacks, fix the file in the same change.
