# Markdown tiles

Open a markdown file in a tile, rendered and live as the file changes. An
agent shows the user a plan, a report or a design this way, beside itself. The
user opens files with ⌘P, from a terminal link or from their own shell. The
tile follows edits on disk, so a document the agent keeps writing stays
current while the user reads it. For review notes on a markdown tile, see
[annotations.md](annotations.md).

## Sub-features

- `file-open`: `attn open <file.md>` docks a markdown tile beside the caller, or beside the active tile when no session calls. A seed id opens a seed tile instead ([garden.md](garden.md)).
- `file-opener`: ⌘P lists recent files on an empty query, fuzzy-searches the active agent's folder, and browses folders for a query starting with `/`, `~`, `./` or `../`.
- `recent-files`: files opened before, the most used and most recent first. Picking one returns to the file's tile when that tile is still open.
- `markdown-render`: headings, tables, alerts, code with highlighting, images, Mermaid diagrams and frontmatter.
- `live-reload`: the tile re-reads its file when the file changes.
- `terminal-link`: ⌘-click a markdown path in a terminal to dock it, bound to the session that terminal shows ([terminals.md](terminals.md)).
- `document-focus`: show the document alone on the desktop, and return with Escape.
- `open-in-editor`: open the active agent's folder in the user's editor.

## How to get to it

### People

- ⌘P (`file.open`, `app/src/shortcuts/registry.ts`) opens the opener (`app/src/components/palette/MarkdownOpener.tsx`). Inside a focused editor tile, ⌘P opens that tile's note finder instead ([notebook.md](notebook.md)).
- Palette (`app/src/application/useAppCommands.tsx`): `open-markdown-file` ("Open a markdown file"), and `open-in-editor` ("Open in editor"), offered while an agent with a local folder is on screen (`app/src/application/useOpenInEditor.ts`).
- In a terminal, ⌘-click on a `.md` path (`app/src/components/GhosttyTerminal.tsx`).
- Tile header (`app/src/components/SessionTerminalDesktop/DesktopDockTile.tsx`): "Focus document" (also ⌘⇧↩, `terminal.toggleMaximize`), and "Close tile". The tile's row in the sidebar (`app/src/components/SidebarRows.tsx`) has a "Close <title>" button that takes clicks while the pointer is over the row.
- In the document (`app/src/components/MarkdownReader/index.tsx`): a fragment link scrolls the tile. Images open in a lightbox.
- Settings: the editor command (`editor_executable`) that "Open in editor" runs. Without it, "Open in editor" runs `$EDITOR` or `$VISUAL`.
- Where people see what agents do: the tile an agent opens docks beside the agent's tile and follows the file as the agent writes it.
- Their own shell: `attn open <file.md> [--session <id>]`.

### Agents

- `attn open <file.md|seed-id> [--session <id>]` (`cmd/attn/main.go`): a relative path resolves against the folder `attn` runs in. The tile docks beside the caller, or beside the tile of `--session`.
- Prompts: the attn skill's markdown reference (`internal/prompts/content/skills/attn/references/markdown.md`), named in its capability index as "Show the user a markdown document" (`internal/prompts/content/skills/attn/SKILL.md`), and `attn open <plot>` in the seed craft (`internal/prompts/content/resources/seed-guide.md`).

### Triggers

- An open markdown tile re-reads its file when the file changes on disk, and shows an error when the file goes away or grows past 1 MiB (`internal/daemon/tilecontent.go`).

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers, then run `bridge dismiss_whats_new` to dismiss What's new.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-markdown-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-markdown.XXXX) && echo "export A=$A" >> $DRIVE_ENV`. In it, save `notes.md` (`# Drive notes` and a line of text), `second.md` (`# Second file`), and `rich.md` (frontmatter with `type: note`, a `# Rich drive` heading, a `> [!NOTE]` alert and a fenced `mermaid` block holding `graph LR; A-->B`). Save this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "show-notes", "actions": [
    {"type": "attn", "args": ["open", "notes.md"]}]}
]}
```

- Start the agent on the current desktop with `wsctl add-session --cwd $A --agent claude --label md-agent --id md-agent --place`. `<agent>` is its `runtime_id` in `wsctl list`.

- **An agent shows a file.** Run `wsctl input --id <agent> --text "show-notes" --enter`. `./attn agent peek md-agent` shows `opened ` and an absolute path ending in `/notes.md`. In `wsctl list`, the current desktop's `tree_json` holds a tile whose `tile_kind` is `markdown` and whose `tile_params` is that path, beside the agent. `bridge dom_wait '{"selector":"[data-tile-kind=\"markdown\"] .md-reader-doc","textIncludes":"Drive notes","timeoutMs":5000}'` matches.
- **The tile follows the file.** Run `printf '\nA new line.\n' >> $A/notes.md`. `bridge dom_wait '{"selector":"[data-tile-kind=\"markdown\"] .md-reader-doc","textIncludes":"A new line","timeoutMs":5000}'` matches.
- **From the person's shell.** Run `./attn open $A/rich.md`. It prints `opened <A>/rich.md` and exits 0. A second markdown tile docks beside the active tile. `bridge dom_wait '{"selector":".md-frontmatter","timeoutMs":5000}'` and `bridge dom_wait '{"selector":".markdown-mermaid","timeoutMs":10000}'` match, and `bridge dom_text '{"selector":".md-frontmatter"}'` reads `type:` and `note`. `./attn open $A/second.md --session md-agent` docks `second.md` beside the agent instead.
- **The opener with ⌘P.** Select the agent for setup with `bridge select_session '{"sessionId":"md-agent"}'`. Run `bridge native_key '{"key":"p","modifiers":["command"]}'`. `bridge markdown_opener_get_state` shows `open: true` and, on the empty query, a row for each file opened above in `rows`, ranked by use and recency, not by the order you opened them. Run `bridge native_text '{"text":"secnd"}'`: the rows include `second.md`, found by fuzzy search in the agent's folder. Press Enter: the opener closes, no third copy of `second.md` docks, and `desktop.view.activeLeafId` in `bridge get_session_ui_state '{"sessionId":"md-agent"}'` is the `tile_id` of the `second.md` tile in `tree_json`.
- **Path mode.** Press ⌘P and type `~/`: `markdown_opener_get_state` lists your home folder's entries. Press Escape: `open` is false.
- **From the palette.** Open the palette (⌘⇧K, wait for `[role="combobox"][aria-label="Commands"]`), type `Open a markdown file`, press Enter: `markdown_opener_get_state` shows `open: true`. Press Escape.
- **From a terminal link.** ⌘-click a `.md` path printed in a shell, as the recipe in [terminals.md](terminals.md) does: the tile that docks carries the clicked terminal's session id as its `tile_session_id` in `tree_json`.
- **Focus the document.** Read the `notes.md` tile's `tile_id` from `tree_json` as `<notes-tile>`. If the tile is a collapsed strip, click its `Expand notes.md` button natively and wait for `[data-pane-id="<notes-tile>"] .md-reader-doc`. Click `[data-pane-id="<notes-tile>"] .md-reader-doc` natively to make it active, then run `bridge native_key '{"key":"Enter","modifiers":["command","shift"]}'`. `desktop.view.maximizedPaneId` in `bridge get_session_ui_state '{"sessionId":"md-agent"}'` names the tile. Press Escape: `maximizedPaneId` is null again. The same tile's `[aria-label="Focus document"]` button, clicked natively, does the same.
- **In the document.** Images render only in a document under the home folder, as Gotchas records, so put this document there: `printf '# Image doc\n\n[Down](#bottom)\n\n![pic](drive-pic.png)\n\n<eighty lines of filler>\n## Bottom\n' > ~/attn-notebook-<name>/drive-imgdoc.md`. Put a 200×100 PNG named `drive-pic.png` beside it. Run `./attn open ~/attn-notebook-<name>/drive-imgdoc.md` and read the new tile's `tile_id` as `<img-tile>`. Click `[data-pane-id="<img-tile>"] img[alt="pic"]` natively: `bridge dom_wait '{"selector":".md-lightbox[role=\"dialog\"]","timeoutMs":5000}'` matches. Escape closes the lightbox. Read `bridge dom_bounds '{"selector":"[data-pane-id=\"<img-tile>\"] #bottom"}'`, click `[data-pane-id="<img-tile>"] a[href="#bottom"]` natively, and read the bounds again: the heading's `y` moves up into the tile.
- **Close the tile.** Click `[data-pane-id="<notes-tile>"] [aria-label="Close tile"]` natively (point from `bridge dom_native_point`): that tile leaves `tree_json`, and the file stays on disk. Open it again with `./attn open $A/notes.md` and read its `tile_id` again as `<notes-tile>`. `<desktop>` is `arrangement.currentDesktopId` in `bridge get_state`. Move the pointer over `[data-testid="sidebar-tile-<desktop>-<notes-tile>"]` with `bridge native_mouse` and `"action":"move"`, then click `[data-testid="close-tile-<desktop>-<notes-tile>"]` natively: that tile leaves `tree_json` too.
- **The file goes away or grows too large.** Read the `second.md` tile's `tile_id` from `tree_json` as `<second-tile>`. If the tile is a strip, expand it (`[aria-label="Expand second.md"]`, natively). Then run `mv $A/second.md $A/second.md.bak`: `bridge dom_text '{"selector":"[data-pane-id=\"<second-tile>\"] .desktop-dock-tile-body"}'` reads `file not found: <A>/second.md`. Move the file back: the tile reads `Second file` again. Append more than 1 MiB to the file: the tile reads `file is too large to preview (<bytes> bytes, max 1048576)`. Write `# Second file` back: the document returns.
- **Open in editor.** Run `./attn settings set editor_executable "touch <A>/editor-opened && echo"`, `<A>` written out. Select the agent, open the palette, type `Open in editor`, press Enter: re-run `test -f $A/editor-opened` until it succeeds, with a deadline that fails the run. Run `./attn settings set editor_executable ""` to restore the setting.
- **The editor setting in Settings, and its fallback.**
  - Press ⌘, and click `[data-testid="settings-nav-desktop"]` natively, click `#settings-editor-exec` natively, type `touch <A>/editor-opened2 && echo` and press Enter: `./attn settings get editor_executable` prints it, and "Open in editor" from the palette creates `$A/editor-opened2`. Set `editor_executable` back to empty.
  - For the fallback, append `export EDITOR='touch <A>/editor-env-opened && echo'` to the env file, quit the app with `./attn instance stop-app` and launch it again with `bridge --launch --wait-ready ping`: with `editor_executable` empty, "Open in editor" creates `$A/editor-env-opened`. Append `unset EDITOR` afterwards.
- **Refusals.** `./attn open $A/missing.md` exits 1 with `open:` and the missing file's error. `./attn open` exits 1 with `usage: attn open <file.md|seed-id> [--session <id>]`.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, `wsctl list` after each open, `bridge markdown_opener_get_state` with the opener open on the empty query and on `secnd`, `./attn agent peek md-agent` after the agent opens a file, and `bridge dom_text '{"selector":"[data-tile-kind=\"markdown\"] .md-reader-doc"}'` before and after the edit. Capture `bridge capture_screenshot '{"path":"<P>/markdown-tiles.png"}'` with both tiles docked and `<P>/markdown-opener.png` with the opener open, `<P>` written out. Name each file for its way in, such as `markdown-agent-open-list.json`.

## Contexts

- **Durability across restarts**: a markdown tile is part of its desktop and comes back after any restart, re-reading the file. Recent files are kept too. The file itself is the source, so a tile shows whatever is on disk now.
- **Linux**: `attn open` works the same where the daemon and CLI run on Linux. The opener is Ctrl+Shift+P there (`LINUX_SHORTCUTS`).
- **Outposts**: markdown tiles read files on the home's machine. attn refuses ⌘-click on a path in a terminal on an outpost, and "Open in editor" for an agent on an outpost, with `<path> is on <outpost>; opening files on an outpost isn't supported yet`.
- **Profiles**: tiles open on a desktop of the caller's profile. Without a session, a tile opens on the current desktop of the profile used most recently.
- **Performance and idle cost**: each open markdown tile keeps checking its file while it is open, so tiles nobody reads still cost a little. Close them. attn refuses files over 1 MiB and names the size and the limit. Large Mermaid diagrams scroll instead of shrinking (`app/e2e/mermaid-diagram.spec.ts`).
- **Harnesses**: every harness opens files with the same `attn open`.
- **Keyboard and native input**: ⌘P opens the opener from anywhere except a focused editor tile. In the opener, use the arrow keys, Enter and Escape. ⌘⇧↩ focuses the active document and Escape returns.

## Tests

- Wire: `internal/daemon/markdown_tiles_wire_test.go`, `internal/daemon/fs_wire_test.go`, `internal/daemon/fs_watch_wire_test.go`, `internal/daemon/fs_watch_darwin_wire_test.go`, `internal/daemon/fs_index_wire_test.go`, `internal/daemon/fs_containment_wire_test.go`, `internal/daemon/recent_files_wire_test.go`, `internal/daemon/agent_tiles_test.go`.
- Stack: `cmd/attn/agent_commands_stack_test.go`, `app/e2e/mermaid-diagram.spec.ts`.
- Scenario: `markdown-opener`, `terminal-md-link`.
- App: `app/src/App.markdownOpener.test.tsx`, `app/src/App.markdownRendering.test.tsx`, `app/src/components/MarkdownReader/*.test.*`, `app/src/components/palette/openerRank.test.ts`, `app/src/components/palette/pathMode.test.ts`, `app/src/utils/terminalMarkdown.test.ts`, `app/src/components/SessionTerminalDesktop/DesktopDockTile.test.tsx`.

## Gotchas

- With several documents docked, inactive tiles can collapse into strips, and their document bodies are absent from the DOM. Expand the target tile before clicking its body or header controls. A collapsed strip's `[data-pane-id]` holds only the file name, so reading the tile's document text or error shows only `second.md`. Scope focus and close controls to the intended `tile_id`, because a broad `[data-tile-kind="markdown"]` selector can pick a collapsed tile whose controls are not visible. Sidebar tile test ids include both the desktop id and the tile id.
- `attn open` reads a bare name starting with `s-` as a seed id, so `attn open s-notes.md` is refused as `"s-notes.md" is not a seed id`. Pass `./s-notes.md`.
- Opening the same file twice returns to the file's tile instead of docking a copy. A recipe that counts tiles after a second open sees one. Opening the same file through `/tmp` and `/private/tmp` still creates two tiles, because path-alias reuse is not fixed yet. Keep the path spelling consistent when checking reuse.
- A path the agent resolves from a relative name comes from the folder it runs in. On macOS `/tmp` is `/private/tmp`, so compare file names, or resolve both sides with `realpath`.
- On an empty query the opener lists only files opened before. A fresh instance shows none until something is opened.
- Without the app, `attn open` still places the tile in `tree_json`. Only the app renders it. Prove rendering with `bridge`, not the CLI.
- `bridge capture_screenshot` shows the rendered document, but Mermaid renders after the text. Wait for `.markdown-mermaid` before capturing.
- The app checks a path and reads a local image through a file scope limited to the home folder. A `.md` path under `/tmp` printed in a terminal is plain text to ⌘-click, and an image beside a document under `/tmp` renders as an empty 1×1 box. `attn open` itself opens a document anywhere. Drive the terminal link and images with files under the home folder, such as the instance's `~/attn-notebook-<name>`.
- Working but wrong: a file that grows past 1 MiB, or is deleted, turns the open tile into an error, and the tile recovers on its own once the file is back under the limit. "Open in editor" opens the agent's folder, not the document in the active tile.
