# Terminals

A terminal is a real PTY in a tile: what the user types and what the program
prints, with scrollback, find, links and images. Every agent runs in one, and
so does every shell. People type, read, search, copy and click in a terminal.
Agents open shells on their desktop's row, run commands in the shells, read what
came back and close the shells. Terminals keep running when the app or the daemon restarts.

## Sub-features

- `terminal-input`: keys, text and paste reach the program as typed. ⌘ shortcuts stay with attn.
- `terminal-output`: what the program prints, rendered with colors, styles and the terminal's title.
- `size-owner`: when several clients show one terminal, such as the app and a phone, the one the user last typed in, pointed at or focused sets the terminal's size. A phone takes the size when it opens the terminal or types in it (the Home Gateway's `terminal_focus` and `terminal_input`). The app draws a terminal another client sized smaller than its tile at that size, scaled to fit and centered.
- `scrollback`: history above the screen, kept across relaunch.
- `find`: search the screen and scrollback, with match case.
- `blocks`: with shell integration, each command and its output form a block to copy, filter or scroll to.
- `context-menu`: right-click to copy, copy the command or output, paste, filter a block, find, scroll a block.
- `links`: ⌘-click a URL to open it in the system browser, a markdown path to dock it in a tile ([markdown.md](markdown.md)), any other path to open it.
- `images`: kitty graphics images drawn in the terminal.
- `shell-tile`: a shell in its own tile, started by the user or an agent.
- `shell-titles`: a shell's tile and sidebar name follow its program and folder.
- `focus-terminal`: one key returns typing to the active tile's terminal.
- `agent-terminals`: agents open, list, type into, run in, read, wait on and close shells. Opened shells take no focus.
- `pty-host`: terminals run in a dedicated PTY worker each, or in one shared PTY host, which is the default.

## How to get to it

### People

- Keys (`app/src/shortcuts/registry.ts`): ⌘T opens a new shell on this desktop (`session.newTerminal`) by opening the location picker with the shell chosen ([agent-launch.md](agent-launch.md)). ⌘` puts typing back in the active tile's terminal (`terminal.open`). ⌘F opens find (`terminal.find`). ⌘C copies a selection. ⇧⌘C copies a selected block's command. ⌘=, ⌘- and ⌘0 resize the app's text, terminals included. The recipe for them is in [settings.md](settings.md).
- In the terminal (`app/src/components/GhosttyTerminal.tsx`): typing and paste, the find bar ("Find", "Match case", "Previous match", "Next match", "Close find"), the block filter ("Filter block output"), ⌘-click on links, and the right-click menu (`app/src/components/TerminalContextMenu.tsx`) with Copy, Copy command, Copy output, Paste, Filter block output, Find, Scroll to top of block and Scroll to bottom of block.
- Tile header (`app/src/components/SessionTerminalDesktop/DesktopAgentHeader.tsx`): "Rename session <name>" on a shell's tile.
- Size: typing in a terminal (`pty_input`), moving the pointer over it (`terminal_pointer_activity`) or focusing it (`terminal_focus`, sent when the terminal takes focus by click or key) makes the app the terminal's size owner (`internal/daemon/pty_size_owner.go`). The app's `pty_resize` applies only while the app owns the size. A second client, such as `wsctl attach`, takes the size the same way.
- **Settings → Agents and models**, "Shared terminal host" (`app/src/components/SettingsModalSections.tsx`), the home setting `pty_shared_host_enabled`.
- Where people see what agents do: a shell an agent opens appears at the row's end with a pulse and takes no focus ([desktops.md](desktops.md)), and the shell's tile shows what the agent typed and what ran.
- Their own shell: nothing. `attn terminal`, below, refuses without a calling agent.
- Splitting, zooming, moving and closing terminal tiles belong to [desktops.md](desktops.md).

### Agents

- `attn terminal` (`cmd/attnd/terminal.go`): `ls [--desktop <ref> | --all] [--json]`, `open [--host <outpost|home>] [--down] [--desktop <ref>] [--cwd <dir>] [-- <command>]`, `run <terminal> "<command>" [--wait] [--timeout <seconds>] [--desktop <ref>]`, `send <terminal> (--text "<text>" | --keys C-c Escape Enter ...) [--desktop <ref>]`, `read <terminal> [--last | --scrollback [--lines <n>]]`, `wait <terminal> --until prompt|exit [--timeout <seconds>]`, `close <terminal> [--desktop <ref>]`. Typing and closing on another desktop need `--desktop`. `move` is in [desktops.md](desktops.md).
- `attn agent peek <session>` reads another agent's screen ([agent-state.md](agent-state.md)).
- Prompts: `attn skill --reference terminal`, on placement, reach, typing, reading and waiting (`internal/prompts/content/skills/attn/references/terminal.md`), and its capability index (`internal/prompts/content/skills/attn/SKILL.md`).

### Triggers

- New terminals start in the shared PTY host, which outlives the app and the daemon. With `pty_shared_host_enabled` stored `false`, or on a machine with no host, they start in a PTY worker that outlives them the same way (`internal/ptyworker`), and existing terminals keep their owner (`internal/daemon/pty_settings.go`). The same setting makes the app stream output only to terminals it draws, for every terminal at once.

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers, then run `bridge dismiss_whats_new` to dismiss What's new.
- Use a shell on your machine with OSC 133 shell integration (fish, zsh or bash). `run --wait`, `read --last` and `wait --until prompt` need that integration.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-terminals.XXXX) && echo "export P=$P" >> $DRIVE_ENV`, and a link fixture inside the checkout with `L=$PWD/README.md && echo "export L=$L" >> $DRIVE_ENV`.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-terminals-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, and save this fixture as `$A/.attn-mock-agent.json`:

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

- Start the agent on the current desktop with `wsctl add-session --cwd $A --agent claude --label term-agent --id term-agent --place`. `<agent>` is its `runtime_id` in `wsctl list`.

- **A shell from the keyboard.** Select the agent for setup with `bridge select_session '{"sessionId":"term-agent"}'`. Run `bridge native_key '{"key":"t","modifiers":["command"]}'` and `bridge dom_wait '{"selector":"[data-testid=\"location-picker-overlay\"]","timeoutMs":5000}'`: the location picker opens with `selectedAgent` reading the shell in `bridge location_picker_get_state`. Run `bridge native_text '{"text":"<A>/"}'`, `<A>` written out, and `bridge native_key '{"key":"Enter"}'`. `bridge dom_wait '{"selector":"[data-testid=\"location-picker-overlay\"]","absent":true,"timeoutMs":10000}'` matches. `wsctl list` shows a new entry with `"agent": "shell"` on the current desktop, and `bridge get_state` names its id in `activeLeaf.sessionId`. Call that id `<shell-id>`, and its terminal `<shell>`, the `runtime_id` in `wsctl list`.
- **Type and read.** Run `bridge native_text '{"text":"cd <P>\n"}'`, `<P>` written out, then `bridge native_text '{"text":"echo drive-attn-typed\n"}'`. `bridge read_pane_text '{"sessionId":"<shell-id>"}'` holds `drive-attn-typed` on the command line and again as output. `wsctl screen --id <shell>` shows the same screen from the protocol.
- **Find.** Run `bridge native_key '{"key":"f","modifiers":["command"]}'` and `bridge dom_wait '{"selector":"[data-testid=\"ghostty-find-input\"]","focused":true,"timeoutMs":5000}'`. Run `bridge native_text '{"text":"drive-attn-typed"}'`: the find bar rescans after a short pause, so wait for the count with `bridge dom_wait '{"selector":"[data-testid=\"ghostty-find-count\"]","textIncludes":"/2","timeoutMs":5000}'`. `bridge dom_text` on the same selector reads `1/2` or `2/2`. Click `[aria-label="Next match"]` and `[aria-label="Previous match"]` natively: the count steps through the matches. Click `[aria-label="Match case"]` natively, click the find input, press ⌘A and type `DRIVE-ATTN-TYPED`: the count reads `0/0`. Click Match case again and the find bar finds the matches again. Click `[aria-label="Close find"]` natively: `bridge dom_wait '{"selector":"[data-testid=\"ghostty-find-bar\"]","absent":true,"timeoutMs":5000}'` matches. Open find with ⌘F again and press Escape: it closes the same way.
- **Right-click and copy a block.** Take the row of the output line from `read_pane_text`, pass the `target` from `bridge get_pane_cell_rect '{"sessionId":"<shell-id>","cell":{"col":2,"row":<row>}}'` to `bridge dom_native_point`, and run `bridge native_mouse '{"action":"right_click","x":<x>,"y":<y>}'`. `bridge get_terminal_context_menu_state` shows `open: true` with the items `copy`, `copy-command`, `copy-output`, `paste`, `filter-block`, `find`, `scroll-block-top` and `scroll-block-bottom`, `copy-output` enabled. Click `filter-block` natively at `bridge dom_native_point '{"selector":"[data-testid=\"terminal-context-menu-filter-block\"]"}'`: `bridge dom_wait '{"selector":"[data-testid=\"ghostty-filter-input\"]","focused":true,"timeoutMs":5000}'` matches. Press Escape and the filter input goes. Open the menu again and click `find`: the find input takes focus. Press Escape. Click `scroll-block-top`, then `scroll-block-bottom`, from a fresh menu each: the menu closes after each. Only off a shared Mac, click `copy-output`: `pbpaste` prints `drive-attn-typed`. `copy`, `copy-command`, `paste`, ⌘C and ⇧⌘C use the same clipboard.
- **⌘-click a markdown path.** The terminal resolves a relative path against the folder the shell started in, `<A>`, not against where it has since moved to, so print an absolute path. Run `bridge native_text '{"text":"echo <L>\n"}'`, `<L>` written out. Find the last row that reads `<L>` in `read_pane_text`, resolve its cell's point as above, and run `bridge native_mouse '{"action":"click","x":<x>,"y":<y>,"modifiers":["command"]}'`. A markdown tile docks: `bridge dom_wait '{"selector":"[data-tile-kind=\"markdown\"]","timeoutMs":5000}'` matches and `wsctl list` holds a `tile_kind` of `markdown` whose `tile_params` is `<L>`. The docked tile can become the active tile, so run `bridge select_session '{"sessionId":"<shell-id>"}'` to put typing back in the shell before the steps below.
- **Focus the terminal.** Click `[aria-label="Collapse sidebar"]` natively: `bridge get_pane_state '{"sessionId":"<shell-id>"}'` shows `inputFocused: false`. Run ``bridge native_key '{"key":"`","modifiers":["command"]}'``: it shows `inputFocused: true`. Restore the sidebar with `bridge native_key '{"key":"b","modifiers":["command"]}'`. Clicking the current desktop's sidebar row does not blur the terminal. The click selects the desktop and returns typing to the desktop's active tile.
- **Shell titles.** Run `bridge native_text '{"text":"sleep 5\n"}'`. While the command runs, `bridge get_session_ui_state '{"sessionId":"<shell-id>"}'` shows `sleep` in `sidebarItem.text`. After the command ends, the folder's name returns.
- **Rename a shell.** Select the shell with `bridge select_session '{"sessionId":"<shell-id>"}'`, so its tile shows its header, then click `[aria-label="Rename session <name>"]` natively, where `<name>` is the tile's title. The rename field takes focus. Press ⌘A, type `build-shell` and press Enter. `wsctl list` shows the session's `label` as `build-shell`, and `bridge get_session_ui_state` shows it in `sidebarItem.text`.
- **An agent opens a shell.** Select the agent for setup. Run `wsctl input --id <agent> --text "term-open" --enter`. `./attn agent peek term-agent` shows a terminal id, `<t>`. A tile opens beside the agent, `wsctl list` shows a new `"agent": "shell"` entry on the desktop, and `bridge get_state` keeps `activeLeaf.sessionId` on `term-agent`. Right after the input, `bridge dom_wait '{"selector":".desktop-pane.leaf-arrival","timeoutMs":8000}'` matches: the new tile's pulse. `wsctl screen --id <t>` shows `drive-attn-open` printed below the command.
- **The agent lists, runs and reads.** Run `wsctl input --id <agent> --text "term-list" --enter`: peek shows the header `ID NAME AGENT STATE DESKTOP POSITION OPENED BY` and a row whose ID is the first 8 characters of `<t>` and whose OPENED BY is `term-agent`. Run `wsctl input --id <agent> --text "term-run <t>" --enter`: peek shows `drive-attn-run`. Run `wsctl input --id <agent> --text "term-read <t>" --enter`: peek shows `drive-attn-run` again, the last block's output.
- **Keys and a wait.** Run `wsctl input --id <agent> --text "term-keys <t>" --enter`. Peek shows `done` with no error. Re-read `wsctl screen --id <t>` until it shows `sleep 30` interrupted and a fresh prompt, with a deadline that fails the run.
- **The agent closes it.** Run `wsctl input --id <agent> --text "term-close <t>" --enter`: the tile goes, and `wsctl list` no longer holds `<t>`.
- **The other flags.** Make a second agent folder `B` and save a fixture in it whose turns run `terminal ls --all --json`, `terminal open --down --host home --desktop 1 --cwd <B>`, `terminal run {{t}} "echo t2-run-out" --wait --timeout 10 --desktop 1`, `terminal read {{t}} --scrollback --lines 5`, `terminal read {{t}}`, `terminal close {{t}} --desktop 1`, and `terminal send {{t}} --text exit --desktop 1`, `send --keys Enter --desktop 1` and `terminal wait {{t}} --until exit --timeout 10`, with `<B>` written out. Give each turn an `includes` that no other turn's prompt contains. Start it with `wsctl add-session --cwd $B --agent claude --label term-agent2 --id term-agent2 --place` and send each turn's prompt. `ls --all --json` lists every terminal with `terminal_id` and `opened_by`. `open` puts the shell below the agent, with the folder `<B>`. `run` shows `t2-run-out`. Both reads show the command and its output. `close --desktop 1` and the `exit` turn each remove the shell from `wsctl list`. Start a third agent whose fixture runs `terminal ls --desktop 1`: it lists the desktop's terminals.
- **What agents read.** `./attn skill --list` names `terminal`, `./attn skill --reference terminal` prints `# Terminals`, and `./attn skill` names `terminal` in the attn map.
- **The person's shell has no session.** Run `./attn terminal ls` from the instance shell. It exits 1 with `terminal: caller rule: attn terminal requires a live calling agent; run it inside an attn agent session`. `./attn terminal --help` prints the usage and exits 0.
- **Another client sets the size.** Select the shell with `bridge select_session '{"sessionId":"<shell-id>"}'`, type `bridge native_text '{"text":"stty size\n"}'`, and note the tile's size in `wsctl screen --id <shell>`. Start a second client in the background and keep its PID: `wsctl attach --id <shell> --cols 50 --rows 30 --text "stty size" --enter & PHONE=$! && echo "export PHONE=$PHONE" >> $DRIVE_ENV`. `wsctl screen --id <shell>` shows `30 50`, and `bridge dom_wait '{"selector":".ghostty-terminal > canvas[style*=\"scale(\"]","timeoutMs":5000}'` matches: the tile draws the 50x30 terminal scaled to fit. Click the terminal natively at `bridge dom_native_point '{"selector":"[aria-label=\"Terminal input\"]"}'`: the same `dom_wait` with `"absent":true` matches, and `stty size` typed in the tile shows the tile's size again. Stop the second client with `kill -INT $PHONE`, the PID you captured.
- **Restart keeps it.** Run `bridge --fresh-launch --wait-ready ping`. `bridge read_pane_text '{"sessionId":"<shell-id>"}'` still holds `drive-attn-typed`, and `wsctl list` shows the same `runtime_id` values. Then restart the daemon: `./attn instance stop-app`, `"$(./attn instance resolve --field appDaemon)" daemon stop` and `bridge --launch --wait-ready ping`. `attn.pid` names a new daemon, and `wsctl list` and `read_pane_text` show the same terminals and text.
- **Only drawn terminals stream.** With `pty_shared_host_enabled` on, open a shell on desktop 1 that prints `while :; do date; sleep 0.2; done`, then press ⌘2: `daemon.log` shows `pty stream forward stop` for it, and its tile on the previous desktop keeps its last frame. Press ⌘1 and type `echo back` at once: the tile shows the daemon's current screen, then `back`. The dashboard (⇧⌘H) does the same for the current desktop. Whatever the setting, a pane the layout collapses to a strip, or that ⌘⇧↩ hides behind a focused pane, holds no stream. `scenario-perf-pty-stream --workload hidden` measures this and checks the typed command.
- **Shared PTY host.** On a fresh instance press ⌘T and start a shell as above: the shell runs in the host and `./attn settings list --all` shows `pty_shared_host_active` true on a row marked read-only. `./attn settings set pty_shared_host_enabled false` turns the setting off for new terminals, and the shells opened before keep working. `./attn settings set pty_shared_host_enabled true` prints `pty_shared_host_enabled = true`, or refuses with `cannot enable shared PTY host:` and the reason. The Settings switch, Agents and models, "Shared terminal host" (`[data-testid="settings-feature-pty_shared_host_enabled"]`), does the same: click it natively and `./attn settings get pty_shared_host_enabled` follows. `docs/internal/pty-host-verification.md` holds the full check.
- **Images.** `app/scripts/real-app-harness/scenario-terminal-kitty-image.mjs` draws its own image, quits the instance's app, restarts its daemon and scrubs its data, so run it last on an instance nothing else drives (`pnpm --dir app run real-app:scenario-terminal-kitty-image`).
- **Proof.** In `$P`, keep each `./attn` and `wsctl` command's output and exit code, `bridge read_pane_text` after typing and after the relaunch, `bridge get_terminal_context_menu_state` with the menu open, `pbpaste` after the copy, and `./attn agent peek term-agent` after each agent step. Capture `bridge capture_screenshot '{"path":"<P>/terminals-find.png"}'` with the find bar open. Terminal text does not show in the screenshot, so the text proof is `read_pane_text` and `wsctl screen`. Name each file for its way in, such as `terminals-agent-run-peek.txt`.

### In the native app

The native app (`native/`) draws, types into and resizes terminals, scrolls their history, selects and copies, takes OSC 52 writes and image pastes, underlines and opens links, selects and copies command blocks, reports the mouse to programs that track it, answers terminal queries and reports pointer activity. Its right-click menu opens on macOS only, since Rune has no pop-up menu on Linux. Find, the block filter, images, the location picker and `attn terminal`'s tiles drive only in the Tauri app. `bridge` reaches the native app's own automation actions (`docs/internal/native-app.md`, Automation) such as `press`, `type`, `key`, `scroll`, `wait`, `state` and `shot`, not the Tauri reads and native input above.

Preconditions:

- Install the instance with `make install INSTANCE=<name> APP=native`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'` so the display outlives each step.
- Launch as [../SKILL.md](../SKILL.md) says for a native instance: `bridge --launch ping`, then `bridge wait '{"target":{"key":"sidebar"}}'`. `bridge state '{"of":"connection"}'` reads `"Connected"`.
- Make the proof folder `P` as above, and save this reader as `$P/mouse-reader.sh`. It turns on SGR mouse tracking and prints each byte it reads, ESC as `ESC`, until `q`:

```bash
stty raw -echo
printf '\033[?1000h\033[?1006hREADY\r\n'
while IFS= read -rs -n1 c; do
  case "$c" in $'\e') printf 'ESC' ;; q) break ;; *) printf '%s' "$c" ;; esac
done
printf '\033[?1000l\033[?1006l'
stty sane
printf '\r\nDONE\n'
```

Steps:

- **A shell.** Run `wsctl add-session --cwd $P --agent shell --label shop --id shop --place`. `bridge state '{"of":"desktops"}'` holds the shell's pane; its `runtime_id` is `<shell>`. The app opens on home, so run `bridge press '{"target":{"key":"sidebar.session.shop"}}'` to open the shell's desktop. `bridge wait '{"target":{"key":"terminal.<shell>"}}'` then answers with the terminal's box.
- **Type and read.** Run `bridge press '{"target":{"key":"terminal.<shell>"}}'`, `bridge type '{"text":"echo native-$((40 + 2))"}'` and `bridge key '{"chord":"Enter"}'`. `bridge state '{"of":"terminal:<shell>","includes":"native-42"}'` waits for the rows the window drew, and one row reads `native-42` alone. `wsctl screen --id <shell>` shows the same screen from the daemon.
- **Scroll, select and copy.** Type `for i in $(seq 1 200); do echo line-$i; done` and press Enter. Run `bridge scroll '{"target":{"key":"terminal.<shell>"},"dy":-400}'`: `bridge state '{"of":"terminal-view:<shell>"}'` reads a `scrolled_back` above 0. Run `bridge drag '{"target":{"key":"terminal.<shell>"},"at":[0.01,0.3],"by":[60,40]}'`: `terminal-view` lists `Wash` overlays on the dragged rows, and `copies` holds their `line-` text. Scroll back down with `dy` `4000`.
- **Blocks.** Press the terminal, type `echo block-one` and press Enter. Find the row that reads `block-one` alone in `bridge state '{"of":"terminal:<shell>"}'`, and run `bridge press` on the terminal with `at` `[0.01, (<row> + 0.5) / <rows>]`, `<rows>` the terminal's rows: `terminal-view` reads a `block` id and lists `Outline` overlays from the command's row through the output's. On Linux, `bridge key '{"chord":"ctrl+alt+c"}'` copies the block's command, and `ctrl+shift+v` pastes `echo block-one` on the prompt; press Ctrl-U to clear it. On macOS, ⇧⌘C and ⌘V do the same, and a right-click opens the menu with Copy, Copy command, Copy output, Paste, Scroll to top of block and Scroll to bottom of block.
- **A program writes the clipboard.** Press the terminal, type `printf "\033]52;c;$(printf osc52-live | base64)\a"` and press Enter. On Linux, `bridge key '{"chord":"ctrl+shift+v"}'` pastes, and the prompt row ends in `osc52-live`.
- **Links.** Put a stub `xdg-open` that appends its argument to `$P/opened.txt` first on the `PATH` the app launches with, so a click records the link instead of opening a browser; on macOS, click only paths. Make `$P/src/main.go`, press the terminal, type `clear; printf "see https://example.test/live and src/main.go:3\n"` and press Enter. `bridge hover` over the URL with `at` on its row: `terminal-view` lists an `Underline` overlay over the URL's columns, and none once the pointer moves off. Run `bridge hold '{"mods":"mod"}'`, `bridge press` on the URL, then `bridge hold '{"mods":""}'`: `opened.txt` holds `https://example.test/live`. Do the same on `src/main.go:3`: it holds `file://$P/src/main.go`. A plain press opens nothing.
- **A program that tracks the mouse.** Type `bash <P>/mouse-reader.sh`, `<P>` written out, press Enter, and wait for `READY` with `state` and `includes`. Press the terminal again: wait for `ESC[<3;`, and one row holds `ESC[<0;<col>;<row>M` followed by `ESC[<3;<col>;<row>m`, the press and its release. Run `bridge scroll '{"target":{"key":"terminal.<shell>"},"dy":60}'`: wait for `ESC[<65;`, the wheel turning down. Type `q` and wait for `DONE`.
- **Restart keeps it.** Run `./attn instance stop-app`, then launch as above. `bridge wait` finds `terminal.<shell>` again, and `state` with `includes` `native-42` shows the earlier output.
- **Proof.** Keep each `bridge state` answer, `wsctl screen` after typing, and `bridge shot '{"path":"<P>/native-terminal.png"}'` after the mouse step. Unlike the Tauri app's capture, the native shot shows the terminal's text. The `native-terminal-core` scenario runs the shell, type and read, and mouse steps and takes the same shot.

## Contexts

- **Durability across restarts**: an app or daemon restart leaves every terminal running in its worker, and the tile comes back with its screen and scrollback. After a machine restart, attn brings back the sessions it can resume and reaps the rest (`crash-recovery`).
- **Linux**: the PTY workers and `attn terminal` run the same where the daemon and CLI run on Linux. Plain Ctrl+letter belongs to the shell there, so app keys add Shift or Alt (`LINUX_SHORTCUTS`): Ctrl+Shift+T new shell, Ctrl+Shift+` focus the terminal, Ctrl+Shift+F find, Ctrl+Shift+V paste.
- **Outposts**: `attn terminal open --host <outpost>` starts a shell on an outpost, and an agent's terminals default to its own machine. The home decides who owns an outpost terminal's size, and the size travels to the outpost as any resize does. While the host is offline, the tile stays, attn sends no typing, and attn refuses ⌘-click on a path, because the path lives on the other machine.
- **Profiles**: every `attn terminal` target belongs to the caller's profile. `attn terminal` does not find a terminal in another profile.
- **Performance and idle cost**: a terminal that prints nothing costs nothing. Floods hit back pressure instead of growing memory (`cmd/attnd/pty_flood_stack_test.go`, `internal/daemon/pty_stream_backpressure_wire_test.go`). `internal/daemon/pty_forward_bench_test.go` and `pty_input_bench_test.go` measure the stream. Hidden terminals release their drawing surface, and with `pty_shared_host_enabled` on, the app streams output only to the terminals it draws. Measure rendering changes with `docs/internal/perf-testing.md`, and use `docs/internal/diagnosing-terminal-input.md` when typing stops working.
- **Harnesses**: Claude Code, Codex and plugin harnesses each run in a terminal the same way, and `attn terminal` works from any of them. Shared Codex can show one session in several tiles. Then a session id is ambiguous, and the agent passes a terminal id.
- **A program that is not reading**: input waits in that terminal's own queue, so a paste into a stuck program never slows other terminals. The queue holds 16 MiB, twice the largest frame the daemon accepts. Past it the write is refused: the tile flashes and shows that keys may not have been sent, and `attn terminal` and the daemon log name the limit. `internal/daemon/pty_input_blocked_wire_test.go` covers both backends. Keys typed after a stuck paste wait behind it, Ctrl-C included, so closing the tile is the way out. What attn delivers to an agent, such as annotations and inbox rings, is different from typing: it counts as delivered only when the program's terminal has taken all of it. A delivery the terminal does not take within 5 s is withdrawn and fails with a reason.
- **Keyboard and native input**: plain keys go to the program. ⌘ keys stay with attn, except ⌘C, which copies. ⌘` returns typing to the active tile after focus moves elsewhere. AppKit handles ⌘. before the web view. The countdown key is therefore a menu item, and the terminal never receives ⌘.
- **Compatibility across versions**: an upgrade keeps running terminals in their old workers (`cmd/attnd/pty_worker_upgrade_stack_test.go`), and a tile shows a notice when its terminal still runs an older build.

## Tests

- Wire: `native/crates/attn-app/tests/wire/terminal.rs` (the native app), `internal/daemon/pty_*_wire_test.go`, `internal/daemon/terminal_commands_wire_test.go`, `internal/daemon/terminal_identity_wire_test.go`, `internal/daemon/stopped_terminal_wire_test.go`, `internal/daemon/kitty_wire_test.go`, `internal/daemon/attach_policy_wire_test.go`, `internal/daemon/attach_revive_wire_test.go`, `internal/daemon/screen_snapshot_wire_test.go`, `internal/daemon/satellite_wire_test.go`, `internal/daemon/session_input_wire_test.go`, `internal/daemon/session_input_credit_wire_test.go`, `internal/daemon/shell_*_wire_test.go`.
- Stack: `cmd/attnd/terminal_stack_test.go`, `scripts/verify-terminal-move.sh`, `cmd/attnd/terminal_close_stack_test.go`, `cmd/attnd/terminal_identity_stack_test.go`, `cmd/attnd/terminal_recovery_stack_test.go`, `cmd/attnd/terminal_reload_stack_test.go`, `cmd/attnd/pty_*_stack_test.go`, `cmd/attnd/session_input_stack_test.go`, `cmd/attnd/shell_titles_stack_test.go`, `app/e2e/terminal-interactions.spec.ts`, `app/e2e/terminal-find.spec.ts`, `app/e2e/terminal-blocks.spec.ts`, `app/e2e/terminal-wake.spec.ts`, `app/e2e/binary-pty-output-realpty.spec.ts`, `app/e2e/outpost-terminal-links.spec.ts`.
- Scenario: `native-terminal-core` (the native app), `terminal-input`, `terminal-context-menu`, `terminal-block-copy`, `terminal-block-resize`, `terminal-md-link`, `ghostty-scroll`, `shell-titles`, `agent-terminals`, `pty-host-setting`, `crash-recovery`, `tr201-local-claude`, `tr301-local-claude`, `tr401-local-claude`, and outside the catalog, `app/scripts/real-app-harness/scenario-terminal-kitty-image.mjs` and `scenario-webgl-recovery.mjs`.
- Kernel: `internal/pty/*_test.go`, `internal/ptybackend/*_test.go`, `internal/ghosttyvt/*_test.go`.
- App: `app/src/App.terminalInput.test.tsx`, `app/src/App.terminalFocus.test.tsx`, `app/src/App.terminalScroll.test.tsx`, `app/src/App.terminalRuntime.test.tsx`, `app/src/App.terminalAttachment.test.tsx`, `app/src/App.paneAttach.test.tsx`, `app/src/App.switchInput.test.tsx`, `app/src/App.drawnStreaming.test.tsx`, `app/src/App.shellTitles.test.tsx`, `app/src/utils/terminal*.test.ts`, `app/src/utils/ghostty*.test.ts`, `app/src/utils/kitty*.test.ts`, `app/src/ghostty/*.test.ts`, `app/src/pty/*.test.ts`, `app/src/components/GhosttyWebGlRenderer.test.ts`, `app/src/components/terminalGlyphFont.test.ts`.

## Gotchas

- `attn terminal` needs a session. From the instance shell every verb but `--help` exits 1 with the caller rule. Drive `attn terminal` through the mock agent.
- ⌘T opens the location picker with the shell chosen. ⌘T does not start a shell until you confirm a folder with Enter. With `projects_directory` set, the path field starts with that folder, so typed text goes after the folder: press ⌘A before typing.
- The find count counts matches on screen rows, so a command line that wraps at the tile's width no longer matches its own text. Keep typed commands short.
- A mock turn matches the first turn whose `includes` the prompt contains, so a turn `t2-ls` also takes the prompt `t2-lsd`.
- `attn terminal ls` prints each terminal's first 8 characters as its ID. `open` prints the whole id. Either works as a target while the id names one terminal.
- `term-keys` sends Ctrl-C right after Enter. If the interrupt lands before the shell starts `sleep 30`, the command runs on and the screen never shows the interrupt. Run the turn again.
- `send --text` writes bytes and presses no Enter. Follow it with `send --keys Enter`, or use `run` for a shell command.
- `open -- <command>` and `run --wait` need OSC 133 marks. A shell without them, or `ATTN_NO_SHELL_INTEGRATION`, makes `open --` refuse before opening and leaves `run --wait` and `read --last` without blocks. `run` also needs the shell at its prompt.
- An agent may type into or move a shell freely. An agent may type into or move another agent only when one of these is true: the target is the caller, the target is below the caller in the delegation chain, or the caller is the chief. A refusal names the rule.
- `bridge capture_screenshot` leaves out terminal content. Prove text with `bridge read_pane_text`, `wsctl screen` or `./attn agent peek`.
- ⌘-click on a URL opens the system browser on your machine. Click markdown paths in a recipe, not URLs.
- On macOS, the packaged app can probe terminal file links under `$HOME`. A `/tmp` fixture cannot prove path-link detection, so use the checkout README named by `L`.
- The kitty image step is unverified and must not run. See Images under Driving it.
- `wsctl input` counts as the user typing, so rings to that terminal wait 30 seconds after the input.
- `wsctl input` never asks for a size, so it never takes the size from the app. Use `wsctl attach`, which asks for one and stays connected, to hold a second client's size. Moving the pointer over the tile takes the size back for the app, so keep the native pointer off the tile until the step clicks it.
- Working but wrong: ⌘` is labeled "Focus utility terminal", but there is no utility terminal, and ⌘` puts typing back in the active tile. `terminal.collapse`, "Collapse utility terminal" (⇧`), is in the shortcut editor and has no action. `pty_shared_host_enabled` moves only terminals started after the setting changes to the shared host, so a run that toggles the setting and checks an old terminal's backend proves nothing. Its drawn-only streaming applies to every terminal at once.
