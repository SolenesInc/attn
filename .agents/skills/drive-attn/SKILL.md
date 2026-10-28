---
name: drive-attn
description: Drive attn's CLI, daemon and app on a non-production instance the way people and agents use them. Use to check a change to attn live before calling it done, or to find every way into a feature (keys, palette, CLI, the prompts agents read, triggers) with its driving recipe, contexts and tests.
---

# Drive attn

This skill is for maintainers: the agents and people changing attn. It starts
attn on a named instance, drives a feature through each way in, and keeps the
proof. `docs/instances.md` and `app/scripts/real-app-harness/AGENTS.md` hold
the detail; this file holds the commands you need in hand.

Never touch production: not `~/.attn`, and not the `attn` on `PATH` without an
instance selected. Kill only a PID you started or one the automation manifest
names, never by name or pattern.

## Launch

Run from the repository root in a shell of its own, so your own `attn` keeps
pointing at the attn you run inside.

```bash
make build                               # ./attn from this checkout
eval "$(./attn instance-env <name>)"     # select the instance; clears inherited routing
unset ATTN_INSIDE_APP ATTN_TERMINAL_ID ATTN_SESSION_ID ATTN_AGENT ATTN_WRAPPER_PATH \
  ATTN_CODEX_SHARED_PROFILE CODEX_THREAD_ID CODEX_HOME
export ATTN_CLAUDE_EXECUTABLE="$PWD/app/scripts/real-app-harness/mockAgent.mjs"
export ATTN_CODEX_EXECUTABLE="$ATTN_CLAUDE_EXECUTABLE" ATTN_HEADLESS_TASKS=off
export ATTN_TOOL_HOME="$(mktemp -d /tmp/drive-attn-toolhome.XXXX)" \
  ATTN_ANALYTICS_INGEST_URL=http://127.0.0.1:1
export ATTN_MOCK_GH_URL="http://127.0.0.1:$(./attn instance resolve --field mockGitHubPort)" \
  ATTN_MOCK_GH_TOKEN=test-token ATTN_MOCK_GH_HOST=mock.github.local
pnpm --dir app run real-app:mock-github ensure
wsctl() { go run ./scripts/wsctl "$@"; }
bridge() { pnpm --silent --dir app run real-app:bridge-cli -- "$@"; }
```

Pick a name of your own (`[a-z0-9][a-z0-9-]{0,15}`), not `dev`: `dev` is
shared with other work and installs the attn skill into your harness homes.
The `unset` line drops the identity of the session you run in, so the
instance does not mistake your shell for one of its terminals. The exports
make the daemon launch the mock agent for `claude` and `codex`, refuse
headless model tasks, and talk to the mock GitHub. `ATTN_TOOL_HOME` gives
the instance a scratch harness home, so a mock conversation a fixture makes
resumable lands there and not in your `~/.claude` or `~/.codex`.
`ATTN_ANALYTICS_INGEST_URL` points analytics uploads at an address that goes
nowhere, so turning sharing on cannot reach the real telemetry service.

Daemon only, for CLI and protocol work:

```bash
./attn daemon ensure                     # {"status": "started"}, "already_running" or "restarted"
```

Full app. `make install` starts the instance's daemon with this shell's
environment, so run the exports above first:

```bash
make install INSTANCE=<name>
bridge --launch --wait-ready ping        # {"frontendReady": true, "pong": true}
```

`--launch` starts the app with this shell's environment and parks its window
at the screen's edge. On macOS, `make run INSTANCE=<name>` opens the app
through Launch Services instead, which drops these variables. If the daemon
started without them (the doctor shows a real agent's executable), or a
recipe needs another variable in the daemon's environment, quit the app with
`./attn instance stop-app`, stop the daemon with
`"$(./attn instance resolve --field appDaemon)" daemon stop`, export what is
missing, then launch again.

## Doctor

One read-only check, through the instance's own binary:

```bash
"$(./attn instance resolve --field appDaemon)" preflight --agent claude
```

It must report `PASS` overall, with `routing.daemon instance=<name>`,
`protocol.cli_app` and `protocol.app_daemon` (the installed app, CLI and
daemon speak one protocol), `tool.mockAgent.mjs` (the daemon launches the
mock, not a real Claude) and `headless.tasks daemon headless tasks off`.
Most verbs, `seed`, `agent`, `daemon` and `preflight` among them, also print
`[attn instance=<name> ...]` on stderr; there, no banner means no instance
is selected. `./attn instance` shows the selection instead. For the build,
the same binary's `version --json` must show the `sourceFingerprint` that
`bash scripts/source-fingerprint.sh` prints for your tree, and
`bridge --wait-ready` refuses an app built from other source. Daemon only:
run `./attn preflight --agent claude`; the two `protocol.` checks fail
because no app is installed, so it prints `FAIL (2 failed, 0 warnings)`
and exits 1. Everything else must pass.

## Drive

| Harness | Door | Reaches | Cannot reach |
| --- | --- | --- | --- |
| `./attn <verb>` in the instance shell | A person's own shell; agents, for verbs that need no session | Every CLI verb, `--json` output, exit codes | The app; verbs that act as a session, such as `seed tend`, `seed watch` and `terminal`, refuse without one |
| Mock agent | Agents | The CLI and prompts as a session in the instance sees them | Model judgment: it does only what its fixture says |
| `wsctl` | The daemon protocol | Sessions, desktops and tiles (`wsctl list`), spawning, terminal input | What the app shows |
| `bridge` | People | Native keys, text and clicks inside the window; UI state; screenshots | The macOS menu bar |
| Scenarios | People, as committed proof | The packaged app, through `createWindowDriver({ appPath, client })`, menus included | A quick local loop: CI runs them |

**Mock agent.** The daemon launches `mockAgent.mjs` for `claude` and `codex`.
It reads `.attn-mock-agent.json` from the session's folder at launch; without
one it stays silent. Each turn matches a prompt by `includes` and runs its
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
wsctl add-session --cwd <dir> --agent claude --label <label> --id <session>
wsctl list                                         # terminal id: desktops[].panes[].runtime_id
wsctl input --id <terminal> --text "tend s-xxxxxx" --enter
./attn agent peek <session>                        # the agent's screen, as text
```

**bridge.** Reads: `get_state`, the feature reads such as `garden_get_state`,
`dom_text '{"selector":"..."}'`, and `dom_wait
'{"selector":"...","timeoutMs":5000}'`, which waits for a selector. Native
input: `native_key '{"key":"t","modifiers":["command","shift"]}'`,
`native_text '{"text":"..."}'`, `native_mouse
'{"action":"click","x":0.5,"y":0.5}'` with window-relative coordinates.
Capture: `capture_screenshot '{"path":"<file>"}'`. DOM actions (`dom_click`,
`dispatch_shortcut`, `garden_toggle_frame` and the like) skip native input:
use them to set up, never as proof of a person's way in. The actions are the
`case` labels in `app/src/hooks/useUiAutomationBridge.ts` and
`app/src/hooks/uiAutomationDomActions.ts`, the native ones in
`app/src-tauri/core/src/native_input.rs` and
`app/src-tauri/core/src/ui_automation.rs`, and quick capture's in
`app/src/quickCapture/automationBridge.ts`.

**Scenarios.** `pnpm --dir app run real-app:serial-matrix -- --scenario <id>`.
CI runs them; run one locally only to reproduce a failure or develop one,
after reading the harness guide. `driver.menu`, native window captures and
recordings need macOS Accessibility or Screen Recording permission for the
instance's app or your terminal.

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
  own session's shell, not the instance shell, run
  `attn seed attach <stake> --path <file> --move`, then cite it with
  `attn seed check report <stake> <C#> -m "<summary>" --evidence <file name>`.
- Recordings: `./scripts/pr-evidence.sh record --instance <name> --seconds 20
  --out <dir>/clip.mp4`, then `./scripts/pr-evidence.sh publish <dir>/clip.mp4`.
  Watch the clip for private data before publishing. macOS only.

## Cleanup

```bash
./attn instance clean <name>
pnpm --dir app run real-app:mock-github stop
eval "$(./attn instance-env --unset)"
```

`clean` stops the daemon, quits the app, reaps its workers, and removes the
app and the data folder. If it reports a live app, run
`./attn instance stop-app` and clean again; for an unconfirmed PID, follow
`docs/instances.md`. Never delete a data folder by hand. `clean` leaves the
instance's notebook folder, `~/attn-notebook-<name>`, with whatever the run
wrote to the notebook or attached to seeds, and a later instance with the
same name starts with it. The scratch tool home, `$ATTN_TOOL_HOME`, stays
under `/tmp` too. Proof in your own folder or on
your stake survives; check that it is still there after cleaning.

## The feature map

Read `features/README.md`, which holds the driving conventions, then
`features/<id>.md` for the feature you changed. Each file lists the feature's ways in, a recipe that drives them,
the contexts it answers and where its tests live. Drive every way in it
lists before calling a change done. When you find a way in, a trap or a test
the file lacks, fix the file in the same change.
