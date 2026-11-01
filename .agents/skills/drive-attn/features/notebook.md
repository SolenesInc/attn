# Notebook

The notebook is the user's notebook and daily journal. The user edits it in a
tile or fullscreen, and the chief keeps it too. The notebook is a folder of
plain markdown files on disk: a journal of dated entries and a knowledge base of
notes. Each profile has its own folder setting, and profiles that leave it blank use the default folder.
People read and edit the notebook in attn's editor. Agents read and edit the
files with their own tools and append to the journal through attn, so concurrent
writers never collide. The chief treats the notebook as its home.

## Sub-features

- `journal-append`: an entry appended to the day's journal file, which attn creates with journal frontmatter when needed. One writer serves every agent.
- `notebook-fullscreen`: the notebook over the whole window: its files, the open document, an outline and backlinks.
- `editor-tile`: an editor docked beside the terminals, opened on the notebook or any other folder.
- `live-editor`: markdown edited in place and saved to disk, with frontmatter shown as a card and links to missing notes marked.
- `note-finder`: ⌘P inside the notebook or an editor tile finds a note by name.
- `notebook-links`: root-absolute links between notes, followed in place, and the backlinks that point at a note.
- `editor-undo`: native ⌘Z and ⇧⌘Z undo and redo in the editor.
- `profile-notebook`: a profile's Notebook folder, the profile setting `notebook.root`. An empty setting uses the default folder, `~/attn-notebook` (with the instance name on a non-default instance).
- `notebook-sync`: an outpost keeps a synced copy of every Notebook folder the home's profiles use.

## How to get to it

### People

- Keys (`app/src/shortcuts/registry.ts`): ⌥⌘N opens an editor tile (`notebook.openTile`), ⇧⌥⌘N opens the notebook fullscreen (`notebook.openFullscreen`).
- Palette (`app/src/application/useAppCommands.tsx`): `notebook` ("Open the notebook") and `notebook-tile` ("Open Editor tile").
- Sidebar: the "Open Notebook (⇧⌥⌘N)" button (`app/src/application/useAppSidebarActions.tsx`).
- Fullscreen and tile (`app/src/components/NotebookSurface.tsx`): the "Notebook files" list, the document with its save status, the Outline and Backlinks sections, the "chief: active" or "chief: idle" status, and "Close" (Esc) fullscreen.
- Editor tile (`app/src/components/notebook/NotebookTile.tsx`): its "Editor root" picker (`app/src/components/SessionTerminalDesktop/NotebookRootPicker.tsx`) and its note finder, "Find a note…" (`app/src/components/notebook/NotebookFinder.tsx`).
- **Settings → Notebook Folder** (`app/src/components/SettingsModalSections.tsx`): "Notebook folder" sets `notebook.root` for the profile the window shows, and a blank value means the default folder. "This profile uses:" shows `notebook.root.effective`.
- Where people see what agents do: journal entries and notes agents write appear in the open notebook as the files change, and the status shows whether the chief is active.
- Their own shell: the files themselves, and the verbs below.

### Agents

- `attn journal append (--entry <text> | --entry-file <path>) [--date YYYY-MM-DD] [--session <id>] [--json]` (`cmd/attn/main.go`). There is no notebook CLI: agents read and edit notes with their own file tools.
- `attn settings get notebook.root.effective [--profile <name|id>]` gives the folder the caller's profile uses (on an outpost, the copy of that folder, inside a session only). `attn settings set notebook.root <path> [--profile <name|id>]` sets a profile's folder. Inside a session, both commands use the session's profile.
- Prompts: the chief's instructions name the notebook as its home (`internal/prompts/content/chief.md`), and the attn skill's notebook reference covers the two layers, frontmatter, links and the journal command (`internal/prompts/content/skills/attn/references/notebook.md`).

### Triggers

- An outpost's copies sync with the home's folders while Outposts is on (`internal/daemon/notebook_sync.go`). The home pushes the folder list on connect and when a profile's folder, or the set of profiles, changes (`internal/daemon/notebook_folders.go`). When the outpost rejects the list, the Outposts row shows the rejection and the home tries again on its own, every 2 s at first and backing off to every 10 s; the outpost's Notebook stream opens only after it accepts a list.

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers, then run `bridge dismiss_whats_new` to dismiss What's new.
- Name the instance's notebook, `~/attn-notebook-<name>`, with `NB=$(./attn settings get notebook.root.effective) && printf 'export NB=%q\n' "$NB" >> "$DRIVE_ENV"`.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-notebook-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-notebook.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, and save this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "journal-entry", "actions": [
    {"type": "attn", "args": ["journal", "append", "--entry", "The mock agent wrote this."]}]}
]}
```

- Start the agent on the current desktop with `wsctl add-session --cwd $A --agent claude --label nb-agent --id nb-agent --place`. `<agent>` is its `runtime_id` in `wsctl list`.

- **Journal from the person's shell.** Run `./attn journal append --entry "drive-attn journal"`. It prints `appended to journal/<date>.md` and exits 0. `cat $NB/journal/<date>.md` starts with frontmatter holding `type: journal` and holds the entry. `./attn journal append --entry "dated" --date 2026-01-02 --json` prints `rel_path` `journal/2026-01-02.md` and a `hash`.
- **Journal from an agent.** Run `wsctl input --id <agent> --text "journal-entry" --enter`. `./attn agent peek nb-agent` shows `appended to journal/<date>.md`, and the day's file holds `The mock agent wrote this.` below the first entry.
- **Fullscreen with the shortcut.** Run `bridge native_key '{"key":"n","modifiers":["command","option","shift"]}'` and `bridge dom_wait '{"selector":".notebook-browser","timeoutMs":5000}'`. `bridge dom_text '{"selector":"#notebook-browser-title"}'` reads `Notebook`, and `bridge dom_text '{"selector":".notebook-browser-list"}'` lists `journal`. Press Escape: `bridge dom_wait '{"selector":".notebook-browser","absent":true,"timeoutMs":5000}'` matches.
- **Fullscreen from the palette and the sidebar.** Open the palette (⌘⇧K, wait for `[role="combobox"][aria-label="Commands"]`), type `Open the notebook`, press Enter: `.notebook-browser` appears. Close it, resolve the point of `[aria-label^="Open Notebook"]` with `bridge dom_native_point` and click it with `bridge native_mouse`: it appears again.
- **Find a note and read it.** With the notebook open, run `bridge native_key '{"key":"p","modifiers":["command"]}'`, `bridge dom_wait '{"selector":"[placeholder=\"Find a note…\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"<date>"}'` and `bridge native_key '{"key":"Enter"}'`. `bridge dom_text '{"selector":".notebook-browser-document"}'` holds `drive-attn journal` and `The mock agent wrote this.`, and the markdown opener did not open (`bridge markdown_opener_get_state` shows `open: false`).
- **It follows the disk.** Run `./attn journal append --entry "appended while open"`. `bridge dom_wait '{"selector":".notebook-browser-document","textIncludes":"appended while open","timeoutMs":5000}'` matches.
- **Edit, save and undo.** Click inside `.notebook-browser-document .cm-content` natively, run `bridge native_key '{"key":"End","modifiers":["command"]}'` and `bridge native_text '{"text":"\nTyped in attn."}'`. `bridge dom_wait '{"selector":".notebook-browser-save-status","textIncludes":"Saved","timeoutMs":10000}'` matches, and `cat $NB/journal/<date>.md` ends with `Typed in attn.`. Run `bridge native_key '{"key":"z","modifiers":["command"]}'`: the line leaves the editor, and once the status reads `Saved`, the line leaves the file. `bridge native_key '{"key":"z","modifiers":["command","shift"]}'` brings it back.
- **Editor tile.**
  - Close the notebook and run `bridge native_key '{"key":"n","modifiers":["command","option"]}'`. `bridge dom_wait '{"selector":"[data-tile-kind=\"notebook\"]","timeoutMs":5000}'` matches, and `wsctl list` holds a tile whose `tile_kind` is `notebook` on the current desktop. The tile opens on the notebook: after fullscreen closes no session is in context, and `bridge dom_value '{"selector":"[aria-label=\"Editor root\"]"}'` reads an empty value.
  - Click inside the tile and press ⌘W to close the tile.
  - Open the palette, type `Open Editor tile` and press Enter: the tile opens again.
  - Close it the same way.
  - Click nb-agent's sidebar row, `[data-session-id="nb-agent"]`, natively and press ⌥⌘N again: with the agent on screen the tile opens on its folder, and the "Editor root" value reads `$A` written out.
  - Click inside the tile natively (point from `bridge dom_native_point '{"selector":"[data-tile-kind=\"notebook\"] .notebook-surface-tile"}'`), then run ⌘P: `bridge dom_wait '{"selector":"[placeholder=\"Find a note…\"]","focused":true,"timeoutMs":5000}'` matches, and `bridge markdown_opener_get_state` shows `open: false`.
- **Change the editor root.** Click `[aria-label="Editor root"]` natively (its point comes from `bridge dom_native_point`). The click focuses the select without opening its menu. Then run `bridge native_text '{"text":"Notebook"}'` to select Notebook. Press Home and Enter: the value stays unchanged. `bridge dom_value '{"selector":"[aria-label=\"Editor root\"]"}'` reads an empty value, and `wsctl list` shows the editor tile updated from its agent-folder root.
- **The chief's status.**
  - Click `[data-testid="session-actions-nb-agent"]` natively, then `[data-testid="chief-of-staff-session-action"]` ("Make chief of staff").
  - Open the notebook (⇧⌥⌘N): `bridge dom_text '{"selector":".notebook-browser-chief-pulse"}'` reads `chief: idle`.
  - Run `wsctl input --id <agent> --text "journal-entry" --enter`: `bridge dom_wait '{"selector":".notebook-browser-chief-pulse","textIncludes":"chief: active","timeoutMs":10000}'` matches, then `chief: idle` again, and the open journal shows one more `The mock agent wrote this.` without a reload.
  - Click `.notebook-browser-close` natively: `.notebook-browser` goes away.
  - Remove the chief role from the same menu ("Remove chief role") before closing the agent. Closing the agent is refused while it is chief.
- **Notebook Folder in Settings.**
  - Press ⌘, and click `[data-testid="settings-nav-desktop"]` natively.
  - Click `[data-testid="settings-notebook-root-input"]` natively, type a folder under the home folder, such as `$HOME/attn-nbprof-<name>` written out, and press Enter: `[data-testid="settings-notebook-root-effective"]` reads `This profile uses:` that folder, and `./attn settings get notebook.root` prints it.
  - Clear the field (⌘A, Backspace, Enter): the effective folder is `$NB` again and the setting is empty.
  - Set and clear the folder from the shell with `./attn settings set notebook.root <folder>` and `""`: the open field and `This profile uses:` follow without reopening Settings.
  - Press Escape.
- **Settings and an entry file from an agent.**
  - Make a folder for a second agent with `A2=$(mktemp -d /tmp/drive-attn-notebook2.XXXX)` and append it to the env file. Save `Entry from a file.` as `$A2/entry.md`.
  - Save a fixture in `$A2` whose turns run `["settings","get","notebook.root.effective"]` on `nb-root`, `["settings","set","notebook.root","<home>/attn-nbagent-<name>"]` on `nb-set`, `["settings","set","notebook.root",""]` on `nb-clear` and `["journal","append","--entry-file","<A2>/entry.md","--json"]` on `nb-file`, with the paths written out.
  - Start a second agent in `$A2`. Type each trigger through `wsctl input`. Peek then shows `$NB`, then `notebook.root = <folder>` (and `./attn settings get notebook.root.effective` from the shell prints that folder, since both are in the Default profile), then an empty `notebook.root`, then `rel_path` `journal/<date>.md`, and the day's file holds `Entry from a file.`.
- **The person's shell with a profile and a session.** `./attn settings get notebook.root.effective --profile Default` prints `$NB`. `./attn settings set notebook.root <folder> --profile Default` changes it and `""` clears it. `./attn journal append --entry "with session flag" --session <second agent's session id>` exits 0 and appends the entry.
- **What agents are told.** `./attn prompts render session launch --set notebook_root=$NB` opens with `You are the chief of staff of your profile. The attn Notebook at <NB> is your durable home`, and `./attn skill --reference notebook` prints the notebook reference with `attn journal append` and `attn settings get notebook.root.effective`.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, the day's journal file after each append and after the edit and undo, `./attn agent peek nb-agent` after the agent's append, and `wsctl list` with the editor tile open. Capture `bridge capture_screenshot '{"path":"<P>/notebook-fullscreen.png"}'` with the journal open and `<P>/notebook-tile.png` with the editor tile, `<P>` written out. Name each file for its way in, such as `notebook-agent-journal-peek.txt`.

## Contexts

- **Durability across restarts**: the notebook is files on disk and does not depend on attn running. An edit that the app had not saved yet is the only thing a crash can lose, and the save status shows when an edit is unsaved. The open editor tile comes back with its desktop.
- **Linux**: `attn journal append` and the files work the same where the daemon and CLI run on Linux. The editor tile is Ctrl+Shift+Alt+N and fullscreen Ctrl+Shift+Alt+F there (`LINUX_SHORTCUTS`).
- **Outposts**: an outpost keeps a copy of each Notebook folder at `~/attn-notebook-<name>-<key>`, synced with the home's, and conflict copies appear on the outpost's row in Settings. An agent there works in the copy of its profile's folder, its `notebook.root.effective`, and `attn seed attach`, `detach` and `check report --evidence` for that profile's seeds land in the profile's folder at the home. Agents on an outpost append to the journal with `attn journal append`, which writes at the home. While the outpost is offline its copies wait for the next sync.
- **Profiles**: each profile has its own `notebook.root`, and profiles that leave it blank use the default folder. The app's notebook, editor tiles, the chief's launch guidance, `attn journal append` and seed artifacts all follow the profile: the window's profile for the app, the session's profile for agents, the seed's profile for its files. After switching the window's profile, the notebook opens on that profile's folder. Repointing a profile never moves files. An editor tile rooted at another profile's folder keeps following the disk when that profile repoints or clears its folder from a shell (`attn settings set notebook.root "" --profile <name>`). To open the tile on that folder, place a terminal there first, since Browse opens a native dialog.
- **Outposts and a folder change**: changing a profile's folder while paired syncs the new folder into its own copy and leaves the old copy unsynced. Pointing back moves the old copy to `<copy>.before-sync-<date>` and syncs it fresh. While an outpost is paired, setting a profile's folder inside or around another profile's folder is refused. Nesting that arises otherwise (pairing with nested folders, a new profile on the default folder around another's) leaves only the inner folder out of sync: the Outposts row shows its error naming both profiles and paths, agents of that profile are refused on the outpost, and the other folders keep syncing. Once the nesting resolves, a file deleted from the inner folder at the home while it was left out stays deleted on both sides.
- **Performance and idle cost**: the open notebook and each editor tile watch their folder for changes. Nothing measures the cost of a large notebook or of an idle open editor. When a change touches either, check with `docs/internal/perf-testing.md`.
- **Harnesses**: any harness reads and edits the files with its own tools and appends with the same `attn journal append`.
- **Keyboard and native input**: ⌥⌘N, ⇧⌥⌘N, ⌘P inside the notebook, Esc to close fullscreen. ⌘Z and ⇧⌘Z reach the editor because attn removes the Edit menu's Undo and Redo, which would take them first. attn leaves ⌘E free for inline code.
- **Compatibility across versions**: attn keeps frontmatter keys it does not know as written, and `index.md` and `log.md` carry no frontmatter.

## Tests

- Wire: `internal/daemon/notebook_wire_test.go`, `internal/daemon/notebook_profiles_wire_test.go`, `internal/daemon/notebook_containment_wire_test.go`, `internal/daemon/journal_wire_test.go`, `internal/daemon/fs_watch_wire_test.go`.
- Stack: `cmd/attn/notebook_sync_stack_test.go`, `cmd/attn/agent_commands_stack_test.go`, `app/e2e/notebook-tile.spec.ts`, `app/e2e/notebook-tile-finder.spec.ts`, `app/e2e/notebook-browser.spec.ts`, `app/e2e/live-markdown-editor.spec.ts`, `app/e2e/broken-links.spec.ts`, `app/e2e/frontmatter-card.spec.ts`.
- Scenario: `notebook-editor-undo`, `notebook-sync`, and outside the catalog, `app/scripts/real-app-harness/scenario-notebook-link-nav.mjs` and `scenario-notebook-tile-close.mjs`.
- Kernel: `internal/notebook/*_test.go`.
- App: `app/src/App.notebook.test.tsx`, `app/src/App.notebookTile.test.tsx`, `app/src/App.outpostsNotebook.test.tsx`, `app/src/components/notebook/*.test.ts`, `app/src/application/useAppSessions.notebookChief.test.tsx`.

## Gotchas

- On a fresh instance the notebook folder does not exist until something writes to it. The first `attn journal append` creates the folder. `instance clean` leaves `~/attn-notebook-<name>` behind, so a later instance with the same name starts with old entries. Read the day's file before counting entries.
- Entries are dated by the home's clock. A run across midnight writes two files.
- Profiles with a blank `notebook.root` all use the default folder. A run in a second such profile sees the first profile's notes, by design.
- ⌘P means the note finder while focus is inside the notebook or an editor tile, and the markdown opener everywhere else. A freshly docked editor tile is not focused until you click in it.
- Sidebar tile rows carry `data-tile-kind` too. Select inside the tile (`[data-tile-kind="notebook"] .notebook-surface-tile`) when you need the tile's own box.
- `bridge capture_screenshot` shows the editor, but only the file on disk proves a save.
- Working but wrong: the palette calls the tile "Editor tile" while the tile kind in `tree_json` reads `notebook`. They are the same thing. Fullscreen and the tile share one editor, so a key that works in one and not the other is a bug.
- Product finding from the native drive: clicking inside an editor tile, including its `.cm-content` with a note open, leaves focus on the outer `.desktop-dock-tile-body--notebook`. ⌘P then opens the global markdown opener (`markdown_opener_get_state.open: true`) instead of the tile finder. The editor-tile finder step fails. Fullscreen finder proof does not cover it.
