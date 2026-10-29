# Notebook

The notebook is the user's notebook and daily journal, edited in a tile or
fullscreen; the chief keeps it too. It is a folder of plain markdown files on
disk, one per instance and shared by every profile: a journal of dated
entries and a knowledge base of notes. People read and edit it in attn's
editor; agents read and edit the files with their own tools and append to the
journal through attn, so concurrent writers never collide. The chief treats
it as its home.

## Sub-features

- `journal-append`: an entry appended to the day's journal file, created with journal frontmatter when needed; one writer for every agent.
- `notebook-fullscreen`: the notebook over the whole window: its files, the open document, an outline and backlinks.
- `editor-tile`: an editor docked beside the terminals, opened on the notebook or any other folder.
- `live-editor`: markdown edited in place and saved to disk, with frontmatter shown as a card and links to missing notes marked.
- `note-finder`: ⌘P inside the notebook or an editor tile finds a note by name.
- `notebook-links`: root-absolute links between notes, followed in place, and the backlinks that point at a note.
- `selection-to-chief`: a selection quoted into the notebook's [inbox.md](inbox.md) for the chief, which a ring tells; [crew.md](crew.md) owns what the chief does with it.
- `editor-undo`: native ⌘Z and ⇧⌘Z undo and redo in the editor.
- `notebook-root`: the folder the notebook lives in, a machine setting.
- `notebook-sync`: an outpost's notebook syncs with the home's.

## How to get to it

### People

- Keys (`app/src/shortcuts/registry.ts`): ⌥⌘N opens an editor tile (`notebook.openTile`), ⇧⌥⌘N opens the notebook fullscreen (`notebook.openFullscreen`).
- Palette (`app/src/application/useAppCommands.tsx`): `notebook` ("Open the notebook") and `notebook-tile` ("Open Editor tile").
- Sidebar: the "Open Notebook (⇧⌥⌘N)" button (`app/src/application/useAppSidebarActions.tsx`).
- Fullscreen and tile (`app/src/components/NotebookSurface.tsx`): the "Notebook files" list, the document with its save status, the Outline and Backlinks sections, "Send to chief" over a selection, the "chief: active" or "chief: idle" status, and "Close" (Esc) fullscreen.
- Editor tile (`app/src/components/notebook/NotebookTile.tsx`): its "Editor root" picker (`app/src/components/SessionTerminalDesktop/NotebookRootPicker.tsx`) and its note finder, "Find a note…" (`app/src/components/notebook/NotebookFinder.tsx`).
- Settings: the notebook folder, blank for the default (`app/src/components/SettingsModalSections.tsx`), the machine setting `notebook.root`.
- Where people see what agents do: journal entries and notes agents write appear in the open notebook as the files change, and the status shows whether the chief is active.
- Their own shell: the files themselves, and the verbs below.

### Agents

- `attn journal append (--entry <text> | --entry-file <path>) [--date YYYY-MM-DD] [--session <id>] [--json]` (`cmd/attn/main.go`). There is no notebook CLI: agents read and edit notes with their own file tools.
- `attn settings get notebook.root.effective` gives an outpost's notebook folder.
- Prompts: the chief's instructions name the notebook as its home (`internal/prompts/content/chief.md`); the attn skill's notebook reference covers the two layers, frontmatter, links and the journal command (`internal/prompts/content/skills/attn/references/notebook.md`); the chief is told when a selection lands in its inbox (`internal/prompts/content/chief/inbox.md`).

### Triggers

- An outpost's notebook syncs with the home's while Outposts is on (`internal/daemon/notebook_sync.go`).
- A selection sent to the chief rings the profile's chief when there is one.

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- `NB=$(./attn settings get notebook.root.effective) && printf 'export NB=%q\n' "$NB" >> "$DRIVE_ENV"` names the instance's notebook, `~/attn-notebook-<name>`.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-notebook-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-notebook.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "journal-entry", "actions": [
    {"type": "attn", "args": ["journal", "append", "--entry", "The mock agent wrote this."]}]}
]}
```

- The agent on the current desktop: `wsctl add-session --cwd $A --agent claude --label nb-agent --id nb-agent --place`. `<agent>` is its `runtime_id` in `wsctl list`.

- **Journal from the person's shell.** Run `./attn journal append --entry "drive-attn journal"`. It prints `appended to journal/<date>.md` and exits 0. `cat $NB/journal/<date>.md` starts with frontmatter holding `type: journal` and holds the entry. `./attn journal append --entry "dated" --date 2026-01-02 --json` prints `rel_path` `journal/2026-01-02.md` and a `hash`.
- **Journal from an agent.** Run `wsctl input --id <agent> --text "journal-entry" --enter`. `./attn agent peek nb-agent` shows `appended to journal/<date>.md`, and the day's file holds `The mock agent wrote this.` below the first entry.
- **Fullscreen with the shortcut.** Run `bridge native_key '{"key":"n","modifiers":["command","option","shift"]}'` and `bridge dom_wait '{"selector":".notebook-browser","timeoutMs":5000}'`. `bridge dom_text '{"selector":"#notebook-browser-title"}'` reads `Notebook`, and `bridge dom_text '{"selector":".notebook-browser-list"}'` lists `journal`. Press Escape: `bridge dom_wait '{"selector":".notebook-browser","absent":true,"timeoutMs":5000}'` matches.
- **Fullscreen from the palette and the sidebar.** Open the palette (⌘⇧K, wait for `[role="combobox"][aria-label="Commands"]`), type `Open the notebook`, press Enter: `.notebook-browser` appears. Close it, read the box of `[aria-label^="Open Notebook"]` with `bridge dom_bounds`, convert it as the README says, and click it with `bridge native_mouse`: it appears again.
- **Find a note and read it.** With the notebook open, run `bridge native_key '{"key":"p","modifiers":["command"]}'`, `bridge dom_wait '{"selector":"[placeholder=\"Find a note…\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"<date>"}'` and `bridge native_key '{"key":"Enter"}'`. `bridge dom_text '{"selector":".notebook-browser-document"}'` holds `drive-attn journal` and `The mock agent wrote this.`, and the markdown opener did not open (`bridge markdown_opener_get_state` shows `open: false`).
- **It follows the disk.** Run `./attn journal append --entry "appended while open"`. `bridge dom_wait '{"selector":".notebook-browser-document","textIncludes":"appended while open","timeoutMs":5000}'` matches.
- **Edit, save and undo.** Click inside `.notebook-browser-document .cm-content` natively, run `bridge native_key '{"key":"End","modifiers":["command"]}'` and `bridge native_text '{"text":"\nTyped in attn."}'`. `bridge dom_wait '{"selector":".notebook-browser-save-status","textIncludes":"Saved","timeoutMs":10000}'` matches, and `cat $NB/journal/<date>.md` ends with `Typed in attn.`. Run `bridge native_key '{"key":"z","modifiers":["command"]}'`: the line leaves the editor, and after `Saved` it leaves the file. `bridge native_key '{"key":"z","modifiers":["command","shift"]}'` brings it back.
- **Send to chief.** Select a line in the document by native drag (`bridge native_mouse '{"action":"drag",...}'` across it), then click `.notebook-browser-send-chief` natively. `bridge dom_wait '{"selector":".notebook-browser-chief-status","textIncludes":"Added to chief","timeoutMs":5000}'` matches, and `cat $NB/inbox.md` quotes the line under a heading naming the note. With a chief in the profile, the chief is rung; that part is driven in [crew.md](crew.md).
- **Editor tile.** Close the notebook and run `bridge native_key '{"key":"n","modifiers":["command","option"]}'`. `bridge dom_wait '{"selector":"[data-tile-kind=\"notebook\"]","timeoutMs":5000}'` matches, and `wsctl list` holds a tile whose `tile_kind` is `notebook` on the current desktop. The palette's "Open Editor tile" does the same. With an agent on screen the tile opens on that agent's folder, `$A`, not on the notebook; its "Editor root" picker switches it. Click inside the tile natively (box from `bridge dom_bounds '{"selector":"[data-tile-kind=\"notebook\"] .notebook-surface-tile"}'`, converted as the README says), then run ⌘P: `bridge dom_wait '{"selector":"[placeholder=\"Find a note…\"]","focused":true,"timeoutMs":5000}'` matches, and `bridge markdown_opener_get_state` shows `open: false`.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, the day's journal file after each append and after the edit and undo, `$NB/inbox.md` after the send, `./attn agent peek nb-agent` after the agent's append, and `wsctl list` with the editor tile open. Capture `bridge capture_screenshot '{"path":"<P>/notebook-fullscreen.png"}'` with the journal open and `<P>/notebook-tile.png` with the editor tile, `<P>` written out. Put the way in in each file's name, such as `notebook-agent-journal-peek.txt`.

## Contexts

- **Durability across restarts**: the notebook is files on disk and does not depend on attn running. An edit that the app had not saved yet is the only thing a crash can lose; the save status shows it. The open editor tile comes back with its desktop.
- **Linux**: `attn journal append` and the files work the same where the daemon and CLI run on Linux. The editor tile is Ctrl+Shift+Alt+N and fullscreen Ctrl+Shift+Alt+F there (`LINUX_SHORTCUTS`).
- **Outposts**: an outpost keeps its own copy at its `notebook.root.effective`, synced with the home's, and conflict copies appear on the outpost's row in Settings. Agents on an outpost append to the journal with `attn journal append`, which writes at the home. While the outpost is offline its copy waits for the next sync.
- **Profiles**: the notebook belongs to the instance, not a profile. Every profile's chief reads and writes the same files, and a selection sent to the chief rings the chief of the profile the app shows.
- **Performance and idle cost**: the open notebook and each editor tile watch their folder for changes. Nothing measures the cost of a large notebook or of an idle open editor; when a change touches them, check with `docs/perf-testing.md`.
- **Harnesses**: any harness reads and edits the files with its own tools and appends with the same `attn journal append`.
- **Keyboard and native input**: ⌥⌘N, ⇧⌥⌘N, ⌘P inside the notebook, Esc to close fullscreen. ⌘Z and ⇧⌘Z reach the editor because attn removes the Edit menu's Undo and Redo, which would take them first. ⌘E is left free for inline code.
- **Compatibility across versions**: frontmatter keys attn does not know are kept as written, and `index.md` and `log.md` carry none.

## Tests

- Wire: `internal/daemon/notebook_wire_test.go`, `internal/daemon/notebook_containment_wire_test.go`, `internal/daemon/journal_wire_test.go`, `internal/daemon/notebook_test.go`.
- Stack: `cmd/attn/notebook_sync_stack_test.go`, `cmd/attn/agent_commands_stack_test.go`, `app/e2e/notebook-tile.spec.ts`, `app/e2e/notebook-tile-finder.spec.ts`, `app/e2e/notebook-browser.spec.ts`, `app/e2e/live-markdown-editor.spec.ts`, `app/e2e/broken-links.spec.ts`, `app/e2e/frontmatter-card.spec.ts`.
- Scenario: `notebook-editor-undo`, `notebook-sync`; outside the catalog, `app/scripts/real-app-harness/scenario-notebook-link-nav.mjs` and `scenario-notebook-tile-close.mjs`.
- Kernel: `internal/notebook/*_test.go`.
- App: `app/src/App.notebook.test.tsx`, `app/src/App.notebookTile.test.tsx`, `app/src/App.outpostsNotebook.test.tsx`, `app/src/components/notebook/*.test.ts`, `app/src/application/useAppSessions.notebookChief.test.tsx`.

## Gotchas

- On a fresh instance the notebook folder does not exist until something writes to it; the first `attn journal append` creates it. `instance clean` leaves `~/attn-notebook-<name>` behind, so a later instance with the same name starts with old entries; read the day's file before counting entries.
- Entries are dated by the home's clock. A run across midnight writes two files.
- The notebook is one folder for every profile. A run in a second profile sees the first profile's notes; that is the design.
- ⌘P means the note finder while focus is inside the notebook or an editor tile, and the markdown opener everywhere else. A freshly docked editor tile is not focused until you click in it.
- Sidebar tile rows carry `data-tile-kind` too. Select inside the tile (`[data-tile-kind="notebook"] .notebook-surface-tile`) when you need the tile's own box.
- `bridge capture_screenshot` shows the editor, but a save is proven only by the file on disk.
- "Send to chief" works without a chief: the quote still lands in [inbox.md](inbox.md) and nobody is rung.
- Working but wrong: the palette calls the tile "Editor tile" while the tile kind in `tree_json` reads `notebook`; it is the same thing. Fullscreen and the tile share one editor, so a key that works in one and not the other is a bug.
