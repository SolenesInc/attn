# Terminals

A terminal is a real PTY in a tile: what the user types and what the program
prints, with scrollback, find, links and images. Every agent runs in one, and
so does every shell. People type, read, search, copy and click in it; agents
open shells beside themselves, run commands in them, read what came back and
close them. Terminals keep running when the app or the daemon restarts.

## Sub-features

- `terminal-input`: keys, text and paste reach the program as typed; ⌘ shortcuts stay with attn.
- `terminal-output`: what the program prints, rendered with colors, styles and the terminal's title.
- `scrollback`: history above the screen, kept across relaunch.
- `find`: search the screen and scrollback, with match case.
- `blocks`: with shell integration, each command and its output form a block to copy, filter or scroll to.
- `context-menu`: right-click to copy, copy the command or output, paste, filter a block, find, scroll a block.
- `links`: ⌘-click a URL to open it in the system browser, a markdown path to dock it in a tile ([markdown.md](markdown.md)), any other path to open it.
- `images`: kitty graphics images drawn in the terminal.
- `shell-tile`: a shell in its own tile, started by the user or an agent.
- `shell-titles`: a shell's tile and sidebar name follow its program and folder.
- `focus-terminal`: one key returns typing to the active tile's terminal.
- `agent-terminals`: agents open, list, type into, run in, read, wait on and close shells; opened shells take no focus.
- `pty-host`: terminals run in a dedicated PTY worker each, or in one shared PTY host behind an experimental setting.

## How to get to it

### People

- Keys (`app/src/shortcuts/registry.ts`): ⌘T new shell on this desktop (`session.newTerminal`), which opens the location picker with the shell chosen ([agent-launch.md](agent-launch.md)), ⌘` puts typing back in the active tile's terminal (`terminal.open`), ⌘F find (`terminal.find`). ⌘C copies a selection; ⇧⌘C copies a selected block's command. ⌘=, ⌘- and ⌘0 resize the app's text, terminals included; [settings.md](settings.md) drives them.
- In the terminal (`app/src/components/GhosttyTerminal.tsx`): typing and paste, the find bar ("Find", "Match case", "Previous match", "Next match", "Close find"), the block filter ("Filter block output"), ⌘-click on links, and the right-click menu (`app/src/components/TerminalContextMenu.tsx`) with Copy, Copy command, Copy output, Paste, Filter block output, Find, Scroll to top of block and Scroll to bottom of block.
- Tile header (`app/src/components/SessionTerminalDesktop/DesktopAgentHeader.tsx`): "Rename session <name>" on a shell's tile.
- Settings, Experimental, "Shared terminal host" (`app/src/components/SettingsModalSections.tsx`), the machine setting `pty_shared_host_enabled`.
- Where people see what agents do: a shell an agent opens appears beside it with a pulse, without taking focus, and its tile shows what the agent typed and what ran.
- Their own shell: nothing; `attn terminal`, below, refuses without a calling agent.
- Splitting, zooming, moving and closing terminal tiles belong to [desktops.md](desktops.md).

### Agents

- `attn terminal` (`cmd/attn/terminal.go`): `ls [--desktop <ref> | --all] [--json]`, `open [--host <outpost|home>] [--down] [--desktop <ref>] [--cwd <dir>] [-- <command>]`, `run <terminal> "<command>" [--wait] [--timeout <seconds>] [--desktop <ref>]`, `send <terminal> (--text "<text>" | --keys C-c Escape Enter ...) [--desktop <ref>]`, `read <terminal> [--last | --scrollback [--lines <n>]]`, `wait <terminal> --until prompt|exit [--timeout <seconds>]`, `close <terminal> [--desktop <ref>]`. Typing and closing on another desktop need `--desktop`. `move` is in [desktops.md](desktops.md).
- `attn agent peek <session>` reads another agent's screen ([agent-state.md](agent-state.md)).
- Prompts: the attn skill's terminal reference, on placement, reach, typing, reading and waiting (`internal/prompts/content/skills/attn/references/terminal.md`), and its capability index (`internal/prompts/content/skills/attn/SKILL.md`).

### Triggers

- Every terminal starts in a PTY worker that outlives the app and the daemon (`internal/ptyworker`). With `pty_shared_host_enabled` on, new terminals start in the shared PTY host instead, and existing ones keep their worker (`internal/daemon/pty_settings.go`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A shell on your machine with OSC 133 shell integration (fish, zsh or bash); `run --wait`, `read --last` and `wait --until prompt` need it.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-terminals.XXXX) && echo "export P=$P" >> $DRIVE_ENV`, and a link fixture inside the checkout: `L=$PWD/README.md && echo "export L=$L" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-terminals-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "term-open", "actions": [
    {"type": "attn", "args": ["terminal", "open", "--", "echo", "drive-attn-open"]}]},
  {"includes": "term-list", "actions": [
    {"type": "attn", "args": ["terminal", "ls"]}]},
  {"includes": "term-run", "actions": [
    {"type": "capture", "name": "t", "pattern": "term-run (\\S+)"},
    {"type": "attn", "args": ["terminal", "run", "{{t}}", "echo drive-attn-run", "--wait"]}]},
  {"includes": "term-read", "actions": [
    {"type": "capture", "name": "t", "pattern": "term-read (\\S+)"},
    {"type": "attn", "args": ["terminal", "read", "{{t}}", "--last"]}]},
  {"includes": "term-keys", "actions": [
    {"type": "capture", "name": "t", "pattern": "term-keys (\\S+)"},
    {"type": "attn", "args": ["terminal", "send", "{{t}}", "--text", "sleep 30"]},
    {"type": "attn", "args": ["terminal", "send", "{{t}}", "--keys", "Enter"]},
    {"type": "attn", "args": ["terminal", "send", "{{t}}", "--keys", "C-c"]},
    {"type": "attn", "args": ["terminal", "wait", "{{t}}", "--until", "prompt", "--timeout", "10"]}]},
  {"includes": "term-close", "actions": [
    {"type": "capture", "name": "t", "pattern": "term-close (\\S+)"},
    {"type": "attn", "args": ["terminal", "close", "{{t}}"]}]}
]}
```

- The agent on the current desktop: `wsctl add-session --cwd $A --agent claude --label term-agent --id term-agent --place`. `<agent>` is its `runtime_id` in `wsctl list`.

- **A shell from the keyboard.** Select the agent for setup with `bridge select_session '{"sessionId":"term-agent"}'`. Run `bridge native_key '{"key":"t","modifiers":["command"]}'` and `bridge dom_wait '{"selector":"[data-testid=\"location-picker-overlay\"]","timeoutMs":5000}'`: the location picker opens with `selectedAgent` reading the shell in `bridge location_picker_get_state`. Run `bridge native_text '{"text":"<A>/"}'`, `<A>` written out, and `bridge native_key '{"key":"Enter"}'`; `bridge dom_wait '{"selector":"[data-testid=\"location-picker-overlay\"]","absent":true,"timeoutMs":10000}'` matches. `wsctl list` shows a new entry with `"agent": "shell"` on the current desktop, and `bridge get_state` names its id in `activeLeaf.sessionId`; call that id `<shell-id>`, and its terminal `<shell>`, the `runtime_id` in `wsctl list`.
- **Type and read.** Run `bridge native_text '{"text":"cd <P> && echo drive-attn-typed\n"}'`, `<P>` written out. `bridge read_pane_text '{"sessionId":"<shell-id>"}'` holds `drive-attn-typed` on the command line and again as output. `wsctl screen --id <shell>` shows the same screen from the protocol.
- **Find.** Run `bridge native_key '{"key":"f","modifiers":["command"]}'` and `bridge dom_wait '{"selector":"[data-testid=\"ghostty-find-input\"]","focused":true,"timeoutMs":5000}'`. Run `bridge native_text '{"text":"drive-attn-typed"}'`: the find bar rescans after a short pause, so wait for it with `bridge dom_wait '{"selector":"[data-testid=\"ghostty-find-count\"]","textIncludes":"/2","timeoutMs":5000}'`; `bridge dom_text` on the same selector reads `1/2` or `2/2`. Press Escape: `bridge dom_wait '{"selector":"[data-testid=\"ghostty-find-bar\"]","absent":true,"timeoutMs":5000}'` matches.
- **Right-click and copy a block.** Take the row of the output line from `read_pane_text`, pass the `target` from `bridge get_pane_cell_rect '{"sessionId":"<shell-id>","cell":{"col":2,"row":<row>}}'` to `bridge dom_native_point`, and run `bridge native_mouse '{"action":"right_click","x":<x>,"y":<y>}'`. `bridge get_terminal_context_menu_state` shows `open: true` with the items `copy`, `copy-command`, `copy-output`, `paste`, `filter-block`, `find`, `scroll-block-top` and `scroll-block-bottom`, `copy-output` enabled. Click `copy-output` natively at `bridge dom_native_point '{"selector":"[data-testid=\"terminal-context-menu-copy-output\"]"}'`: `pbpaste` prints `drive-attn-typed`.
- **⌘-click a markdown path.** The terminal resolves a relative path against the folder the shell started in, `<A>`, not against where it has since moved to, so print an absolute path. Run `bridge native_text '{"text":"echo <L>\n"}'`, `<L>` written out. Find the last row that reads `<L>` in `read_pane_text`, resolve its cell's point as above, and run `bridge native_mouse '{"action":"click","x":<x>,"y":<y>,"modifiers":["command"]}'`. A markdown tile docks: `bridge dom_wait '{"selector":"[data-tile-kind=\"markdown\"]","timeoutMs":5000}'` matches and `wsctl list` holds a `tile_kind` of `markdown` whose `tile_params` is `<L>`. The docked tile can take the active tile, so run `bridge select_session '{"sessionId":"<shell-id>"}'` to put typing back in the shell before the steps below.
- **Focus the terminal.** Click `[aria-label="Collapse sidebar"]` natively: `bridge get_pane_state '{"sessionId":"<shell-id>"}'` shows `inputFocused: false`. Run ``bridge native_key '{"key":"`","modifiers":["command"]}'``: it shows `inputFocused: true`. Restore the sidebar with `bridge native_key '{"key":"b","modifiers":["command"]}'`. Clicking the current desktop's sidebar row does not blur the terminal: it selects the desktop and returns typing to its active tile.
- **Shell titles.** Run `bridge native_text '{"text":"sleep 5\n"}'`. While it runs, `bridge get_session_ui_state '{"sessionId":"<shell-id>"}'` shows `sleep` in `sidebarItem.text`; after it ends, the folder's name returns.
- **An agent opens a shell.** Select the agent for setup. Run `wsctl input --id <agent> --text "term-open" --enter`. `./attn agent peek term-agent` shows a terminal id, `<t>`. A tile opens beside the agent, `wsctl list` shows a new `"agent": "shell"` entry on the desktop, and `bridge get_state` keeps `activeLeaf.sessionId` on `term-agent`. `wsctl screen --id <t>` shows `drive-attn-open` printed below the command.
- **The agent lists, runs and reads.** Run `wsctl input --id <agent> --text "term-list" --enter`: peek shows the header `ID NAME AGENT STATE DESKTOP POSITION OPENED BY` and a row whose ID is the first 8 characters of `<t>` and whose OPENED BY is `term-agent`. Run `wsctl input --id <agent> --text "term-run <t>" --enter`: peek shows `drive-attn-run`. Run `wsctl input --id <agent> --text "term-read <t>" --enter`: peek shows `drive-attn-run` again, the last block's output.
- **Keys and a wait.** Run `wsctl input --id <agent> --text "term-keys <t>" --enter`. Peek shows `done` with no error. Re-read `wsctl screen --id <t>` until it shows `sleep 30` interrupted and a fresh prompt, with a deadline that fails the run.
- **The agent closes it.** Run `wsctl input --id <agent> --text "term-close <t>" --enter`: the tile goes, and `wsctl list` no longer holds `<t>`.
- **The person's shell has no session.** Run `./attn terminal ls` from the instance shell. It exits 1 with `terminal: caller rule: attn terminal requires a live calling agent; run it inside an attn agent session`. `./attn terminal --help` prints the usage and exits 0.
- **Restart keeps it.** Run `bridge --fresh-launch --wait-ready ping`. `bridge read_pane_text '{"sessionId":"<shell-id>"}'` still holds `drive-attn-typed`, and `wsctl list` shows the same `runtime_id` values.
- **Shared PTY host.** Run `./attn settings set pty_shared_host_enabled true`: it prints `pty_shared_host_enabled = true`, or refuses with `cannot enable shared PTY host:` and the reason. Press ⌘T and start a shell as above: it runs, `./attn settings list --all` shows `pty_shared_host_active` true on a row marked read-only, and the shells opened before keep working. `./attn settings set pty_shared_host_enabled false` turns it off for new terminals. The Settings switch, Experimental, "Shared terminal host", does the same. `docs/internal/pty-host-verification.md` holds the full check.
- **Images.** The kitty image scenario draws its own image, restarts the instance's daemon and starts a fresh world, so it runs last. Read `app/scripts/real-app-harness/scenario-terminal-kitty-image.mjs` and its imported `freshWorld.mjs` before running `pnpm --dir app run real-app:scenario-terminal-kitty-image`. This step currently fails its safety precondition: the helper discovers workers by matching the app path with `pgrep -f`, then signals those PIDs, which the repository's no-pattern-killing rule forbids. Keep it unverified until the helper uses registered or captured PIDs.
- **Proof.** In `$P`, keep each `./attn` and `wsctl` command's output and exit code, `bridge read_pane_text` after typing and after the relaunch, `bridge get_terminal_context_menu_state` with the menu open, `pbpaste` after the copy, and `./attn agent peek term-agent` after each agent step. Capture `bridge capture_screenshot '{"path":"<P>/terminals-find.png"}'` with the find bar open; terminal text does not show in it, so the text proof is `read_pane_text` and `wsctl screen`. Put the way in in each file's name, such as `terminals-agent-run-peek.txt`.

## Contexts

- **Durability across restarts**: an app or daemon restart leaves every terminal running in its worker, and the tile comes back with its screen and scrollback. A machine restart brings back the sessions attn can resume and reaps the rest (`crash-recovery`). A terminal on the embedded backend stops when the daemon restarts.
- **Linux**: the PTY workers and `attn terminal` run the same where the daemon and CLI run on Linux. Plain Ctrl+letter belongs to the shell there, so app keys add Shift or Alt (`LINUX_SHORTCUTS`): Ctrl+Shift+T new shell, Ctrl+Shift+` focus the terminal, Ctrl+Shift+F find, Ctrl+Shift+V paste.
- **Outposts**: `attn terminal open --host <outpost>` starts a shell on an outpost, and an agent's terminals default to its own machine. While the host is offline the tile stays, typing is not sent, and ⌘-click on a path is refused, since the path lives on the other machine.
- **Profiles**: every `attn terminal` target belongs to the caller's profile; a terminal in another profile is not found.
- **Performance and idle cost**: a terminal that prints nothing costs nothing; floods hit back pressure instead of growing memory (`cmd/attn/pty_flood_stack_test.go`, `internal/daemon/pty_stream_backpressure_wire_test.go`). `internal/daemon/pty_forward_bench_test.go` and `pty_input_bench_test.go` measure the stream. Hidden terminals release their drawing surface. Measure rendering changes with `docs/internal/perf-testing.md`, and use `docs/internal/diagnosing-terminal-input.md` when typing stops working.
- **Harnesses**: Claude Code, Codex and Pi each run in a terminal the same way, and `attn terminal` works from any of them. Shared Codex can show one session in several tiles; then a session id is ambiguous and the agent passes a terminal id.
- **Keyboard and native input**: plain keys go to the program; ⌘ keys stay with attn, except ⌘C, which copies. ⌘` returns typing to the active tile after focus wandered. AppKit takes ⌘. before the web view, which is why the countdown key is a menu item; the terminal never sees it.
- **Compatibility across versions**: an upgrade keeps running terminals in their old workers (`cmd/attn/pty_worker_upgrade_stack_test.go`, `scenario-terminal-build-upgrade.mjs`), and a tile tells the user when its terminal still runs an older build.

## Tests

- Wire: `internal/daemon/pty_*_wire_test.go`, `internal/daemon/terminal_commands_wire_test.go`, `internal/daemon/terminal_identity_wire_test.go`, `internal/daemon/stopped_terminal_wire_test.go`, `internal/daemon/kitty_wire_test.go`, `internal/daemon/attach_policy_wire_test.go`, `internal/daemon/attach_revive_wire_test.go`, `internal/daemon/screen_snapshot_wire_test.go`, `internal/daemon/satellite_wire_test.go`, `internal/daemon/session_input_wire_test.go`, `internal/daemon/session_input_credit_wire_test.go`, `internal/daemon/shell_*_wire_test.go`.
- Stack: `cmd/attn/terminal_stack_test.go`, `scripts/verify-terminal-move.sh`, `cmd/attn/terminal_close_stack_test.go`, `cmd/attn/terminal_identity_stack_test.go`, `cmd/attn/terminal_recovery_stack_test.go`, `cmd/attn/terminal_reload_stack_test.go`, `cmd/attn/pty_*_stack_test.go`, `cmd/attn/session_input_stack_test.go`, `cmd/attn/shell_titles_stack_test.go`, `app/e2e/terminal-interactions.spec.ts`, `app/e2e/terminal-find.spec.ts`, `app/e2e/terminal-blocks.spec.ts`, `app/e2e/terminal-wake.spec.ts`, `app/e2e/binary-pty-output-realpty.spec.ts`, `app/e2e/outpost-terminal-links.spec.ts`.
- Scenario: `terminal-input`, `terminal-context-menu`, `terminal-block-copy`, `terminal-block-resize`, `terminal-md-link`, `ghostty-scroll`, `shell-titles`, `agent-terminals`, `pty-host-setting`, `focus-probe`, `crash-recovery`, `tr201-local-claude`, `tr301-local-claude`, `tr401-local-claude`; outside the catalog, `app/scripts/real-app-harness/scenario-terminal-kitty-image.mjs`, `scenario-terminal-build-upgrade.mjs` and `scenario-webgl-recovery.mjs`.
- Kernel: `internal/pty/*_test.go`, `internal/ptybackend/*_test.go`, `internal/ghosttyvt/*_test.go`.
- App: `app/src/App.terminalInput.test.tsx`, `app/src/App.terminalFocus.test.tsx`, `app/src/App.terminalScroll.test.tsx`, `app/src/App.terminalRuntime.test.tsx`, `app/src/App.terminalAttachment.test.tsx`, `app/src/App.paneAttach.test.tsx`, `app/src/App.switchInput.test.tsx`, `app/src/App.shellTitles.test.tsx`, `app/src/utils/terminal*.test.ts`, `app/src/utils/ghostty*.test.ts`, `app/src/utils/kitty*.test.ts`, `app/src/ghostty/*.test.ts`, `app/src/pty/*.test.ts`, `app/src/components/GhosttyWebGlRenderer.test.ts`, `app/src/components/terminalGlyphFont.test.ts`.

## Gotchas

- `attn terminal` needs a session. From the instance shell every verb but `--help` exits 1 with the caller rule; drive it through the mock agent.
- ⌘T opens the location picker with the shell chosen; it does not start a shell until a folder is confirmed with Enter.
- `attn terminal ls` prints each terminal's first 8 characters as its ID; `open` prints the whole id. Either works as a target while it names one terminal.
- `term-keys` sends Ctrl-C right after Enter. If the interrupt lands before the shell starts `sleep 30`, the command runs on and the screen never shows the interrupt; run the turn again.
- `send --text` writes bytes and presses no Enter. Follow it with `send --keys Enter`, or use `run` for a shell command.
- `open -- <command>` and `run --wait` need OSC 133 marks. A shell without them, or `ATTN_NO_SHELL_INTEGRATION`, makes `open --` refuse before opening and leaves `run --wait` and `read --last` without blocks. `run` also needs the shell at its prompt.
- An agent may type into or move a shell freely, but into another agent only when that agent is itself, below it in its delegation chain, or the chief calls. A refusal names the rule.
- `bridge capture_screenshot` leaves out terminal content. Prove text with `bridge read_pane_text`, `wsctl screen` or `./attn agent peek`.
- ⌘-click on a URL opens the system browser on your machine. Click markdown paths in a recipe, not URLs.
- On macOS, the packaged app can probe terminal file links under `$HOME`. A `/tmp` fixture cannot prove path-link detection; use the checkout README named by `L`.
- The kitty image scenario quits the instance's app, restarts its daemon and scrubs its data. Run it last, on an instance nothing else drives.
- `wsctl input` counts as the user typing, so rings to that terminal wait 30 seconds after it.
- Working but wrong: ⌘` is labeled "Focus utility terminal", but there is no utility terminal; it puts typing back in the active tile. `terminal.collapse`, "Collapse utility terminal" (⇧`), is in the shortcut editor and has no action. `pty_shared_host_enabled` changes only terminals started after it; a run that toggles it and checks an old terminal proves nothing.
- The kitty image recipe imports `freshWorld.mjs`, whose worker cleanup finds PIDs with `pgrep -f` before signalling them. Do not run that helper under the repository's no-pattern-killing rule. The image recipe remains unverified.
