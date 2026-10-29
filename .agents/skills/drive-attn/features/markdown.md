# Markdown tiles

Open a markdown file in a tile, rendered and live as the file changes. An
agent shows the user a plan, a report or a design this way, beside itself; the
user opens files with ⌘P, from a terminal link or from their own shell. The
tile follows edits on disk, so a document the agent keeps writing stays
current while the user reads it. Review notes on a markdown tile belong to
[annotations.md](annotations.md).

## Actions and ways in

### Open a document

⌘P (`file.open`, `app/src/shortcuts/registry.ts`) opens the markdown opener
(`app/src/components/palette/MarkdownOpener.tsx`); the palette's `open-markdown-file` ("Open a
markdown file", `app/src/application/useAppCommands.tsx`) does too. An empty query lists recent
files, most used and recent first; a query fuzzy-searches the active agent's folder, or browses
folders when it begins with `/`, `~`, `./` or `../`. Picking a recent file returns to its open tile.
Inside a focused editor tile, ⌘P opens the note finder instead ([notebook.md](notebook.md)). ⌘-click
a `.md` path in a terminal to dock it bound to that session
(`app/src/components/GhosttyTerminal.tsx`, [terminals.md](terminals.md)). People and agents can run
`attn open <file.md|seed-id> [--session <id>]` (`cmd/attn/main.go`): relative paths resolve from the
command's folder; the tile docks beside the caller, the named session, or the active tile without a
session. A seed id opens a seed tile ([garden.md](garden.md)).

### Read and focus

The tile renders headings, tables, alerts, highlighted code, images, Mermaid and frontmatter.
Fragment links scroll and images open in a lightbox (`app/src/components/MarkdownReader/index.tsx`).
The header's "Focus document" or ⌘⇧↩ (`terminal.toggleMaximize`) shows it alone; Escape returns.
"Close tile" closes it (`app/src/components/SessionTerminalDesktop/DesktopDockTile.tsx`); its
sidebar row's "Close <title>" button takes clicks while hovered
(`app/src/components/SidebarRows.tsx`). The tile re-reads disk changes and shows an error if its
file disappears or exceeds 1 MiB (`internal/daemon/tilecontent.go`).

### Open the agent's folder in an editor

Palette `open-in-editor` ("Open in editor") appears for an agent with a local folder
(`app/src/application/useAppCommands.tsx`, `app/src/application/useOpenInEditor.ts`). Settings'
`editor_executable` selects the command, falling back to `$EDITOR` or `$VISUAL`.

### Guide agents

The attn skill's markdown reference (`internal/prompts/content/skills/attn/references/markdown.md`)
is named "Show the user a markdown document" in its capability index
(`internal/prompts/content/skills/attn/SKILL.md`); the seed craft names `attn open <plot>`
(`internal/prompts/content/resources/seed-guide.md`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-markdown-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-markdown.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding `notes.md` (`# Drive notes` and a line of text), `second.md` (`# Second file`), `rich.md` (frontmatter with `type: note`, a `# Rich drive` heading, a `> [!NOTE]` alert and a fenced `mermaid` block holding `graph LR; A-->B`), and this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "show-notes", "actions": [
    {"type": "attn", "args": ["open", "notes.md"]}]}
]}
```

- The agent on the current desktop: `wsctl add-session --cwd $A --agent claude --label md-agent --id md-agent --place`. `<agent>` is its `runtime_id` in `wsctl list`.

- **An agent shows a file.** Run `wsctl input --id <agent> --text "show-notes" --enter`. `./attn agent peek md-agent` shows `opened ` and an absolute path ending in `/notes.md`. In `wsctl list`, the current desktop's `tree_json` holds a tile whose `tile_kind` is `markdown` and whose `tile_params` is that path, beside the agent. `bridge dom_wait '{"selector":"[data-tile-kind=\"markdown\"] .md-reader-doc","textIncludes":"Drive notes","timeoutMs":5000}'` matches.
- **It follows the file.** Run `printf '\nA new line.\n' >> $A/notes.md`. `bridge dom_wait '{"selector":"[data-tile-kind=\"markdown\"] .md-reader-doc","textIncludes":"A new line","timeoutMs":5000}'` matches.
- **From the person's shell.** Run `./attn open $A/rich.md`. It prints `opened <A>/rich.md` and exits 0; a second markdown tile docks beside the active tile. `bridge dom_wait '{"selector":".md-frontmatter","timeoutMs":5000}'` and `bridge dom_wait '{"selector":".markdown-mermaid","timeoutMs":10000}'` match, and `bridge dom_text '{"selector":".md-frontmatter"}'` reads `type:` and `note`. `./attn open $A/second.md --session md-agent` docks `second.md` beside the agent instead.
- **The opener with ⌘P.** Select the agent for setup with `bridge select_session '{"sessionId":"md-agent"}'`. Run `bridge native_key '{"key":"p","modifiers":["command"]}'`. `bridge markdown_opener_get_state` shows `open: true` and, on the empty query, a row for each file opened above in `rows`, ranked by use and recency, not by the order you opened them. Run `bridge native_text '{"text":"secnd"}'`: the rows include `second.md`, found by fuzzy search in the agent's folder. Press Enter: the opener closes, no third copy of `second.md` docks, and `desktop.view.activeLeafId` in `bridge get_session_ui_state '{"sessionId":"md-agent"}'` is the `tile_id` of the `second.md` tile in `tree_json`.
- **Path mode.** Press ⌘P and type `~/`: `markdown_opener_get_state` lists your home folder's entries. Press Escape: `open` is false.
- **From the palette.** Open the palette (⌘⇧K, wait for `[role="combobox"][aria-label="Commands"]`), type `Open a markdown file`, press Enter: `markdown_opener_get_state` shows `open: true`. Press Escape.
- **From a terminal link.** ⌘-click a `.md` path printed in a shell, as [terminals.md](terminals.md) drives it: the tile that docks carries the clicked terminal's session id as its `tile_session_id` in `tree_json`.
- **Focus the document.** Click the `notes.md` tile's body natively to make it active, then run `bridge native_key '{"key":"Enter","modifiers":["command","shift"]}'`. `desktop.view.maximizedPaneId` in `bridge get_session_ui_state '{"sessionId":"md-agent"}'` names the tile. Press Escape: it is null again. The header's "Focus document" button, clicked natively, does the same.
- **Close the tile.** Click `[data-tile-kind="markdown"] [aria-label="Close tile"]` natively (box from `bridge dom_bounds`, converted as the README says): the tile leaves `tree_json`, and the file stays on disk. Open it again with `./attn open $A/notes.md`, move the pointer over its sidebar row, `[data-testid^="sidebar-tile-"]` (`bridge native_mouse` with `"action":"move"`), and click `[data-testid^="close-tile-"]` there: it leaves `tree_json` the same way.
- **Open in editor.** Run `./attn settings set editor_executable "touch <A>/editor-opened && echo"`, `<A>` written out. Select the agent, open the palette, type `Open in editor`, press Enter: re-run `test -f $A/editor-opened` until it succeeds, with a deadline that fails the run. Run `./attn settings set editor_executable ""` to restore it.
- **Refusals.** `./attn open $A/missing.md` exits 1 with `open:` and the missing file's error. `./attn open` exits 1 with `usage: attn open <file.md|seed-id> [--session <id>]`.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, `wsctl list` after each open, `bridge markdown_opener_get_state` with the opener open on the empty query and on `secnd`, `./attn agent peek md-agent` after the agent opens a file, and `bridge dom_text '{"selector":"[data-tile-kind=\"markdown\"] .md-reader-doc"}'` before and after the edit. Capture `bridge capture_screenshot '{"path":"<P>/markdown-tiles.png"}'` with both tiles docked and `<P>/markdown-opener.png` with the opener open, `<P>` written out. Put the way in in each file's name, such as `markdown-agent-open-list.json`.

## Contexts

- **Durability across restarts**: a markdown tile is part of its desktop and comes back after any restart, re-reading the file; recent files are kept too. The file itself is the source, so a tile shows whatever is on disk now.
- **Linux**: `attn open` works the same where the daemon and CLI run on Linux. The opener is Ctrl+Shift+P there (`LINUX_SHORTCUTS`).
- **Outposts**: markdown tiles read files on the home's machine. ⌘-click on a path in a terminal on an outpost, and "Open in editor" for an agent on an outpost, are refused with `<path> is on <outpost>; opening files on an outpost isn't supported yet`.
- **Profiles**: tiles open on a desktop of the caller's profile; without a session, on the current desktop of the profile used most recently.
- **Performance and idle cost**: each open markdown tile keeps checking its file while it is open, so tiles nobody reads still cost a little; close them. Files over 1 MiB are refused with the size and the limit. Large Mermaid diagrams scroll instead of shrinking (`app/e2e/mermaid-diagram.spec.ts`).
- **Harnesses**: every harness opens files with the same `attn open`.
- **Keyboard and native input**: ⌘P opens the opener from anywhere except a focused editor tile; arrows, Enter and Escape drive it. ⌘⇧↩ focuses the active document and Escape returns.

## Tests

- Wire: `internal/daemon/markdown_tiles_wire_test.go`, `internal/daemon/fs_wire_test.go`, `internal/daemon/fs_watch_wire_test.go`, `internal/daemon/fs_watch_darwin_wire_test.go`, `internal/daemon/fs_index_wire_test.go`, `internal/daemon/fs_containment_wire_test.go`, `internal/daemon/recent_files_wire_test.go`, `internal/daemon/agent_tiles_test.go`.
- Stack: `cmd/attn/agent_commands_stack_test.go`, `app/e2e/mermaid-diagram.spec.ts`.
- Scenario: `markdown-opener`, `terminal-md-link`.
- App: `app/src/App.markdownOpener.test.tsx`, `app/src/App.markdownRendering.test.tsx`, `app/src/components/MarkdownReader/*.test.*`, `app/src/components/palette/openerRank.test.ts`, `app/src/components/palette/pathMode.test.ts`, `app/src/utils/terminalMarkdown.test.ts`, `app/src/components/SessionTerminalDesktop/DesktopDockTile.test.tsx`.

## Gotchas

- A bare name starting with `s-` is read as a seed id: `attn open s-notes.md` is refused as `"s-notes.md" is not a seed id`. Pass `./s-notes.md`.
- Opening the same file twice returns to its tile instead of docking a copy; a recipe that counts tiles after a second open sees one.
- A path the agent resolves from a relative name comes from the folder it runs in. On macOS `/tmp` is `/private/tmp`, so compare file names, or resolve both sides with `realpath`.
- On an empty query the opener lists only files opened before; a fresh instance shows none until something is opened. Fuzzy search covers the active agent's folder, so select the agent whose folder holds the file.
- Without the app, `attn open` still places the tile in `tree_json`; only the app renders it. Prove rendering with `bridge`, not the CLI.
- `bridge capture_screenshot` shows the rendered document, but Mermaid renders after the text; wait for `.markdown-mermaid` before capturing.
- "Open in editor" starts a real program. Point `editor_executable` at a harmless command for a recipe, and restore it after.
- Working but wrong: a file that grows past 1 MiB, or is deleted, turns the open tile into an error, and it recovers on its own once the file is back under the limit. "Open in editor" opens the agent's folder, not the document in the active tile.
