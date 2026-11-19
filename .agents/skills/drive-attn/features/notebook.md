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
- `editor-tile-drafts`: an editor tile that leaves the screen for the shelf saves its unsaved edit on the way out. If that save fails or conflicts, the tile brings back the edit and the message when it returns.
- `note-finder`: ⌘P inside the notebook or an editor tile finds a note by name.
- `notebook-links`: root-absolute links between notes, followed in place, and the backlinks that point at a note.
- `editor-undo`: native ⌘Z and ⇧⌘Z undo and redo in the editor.
- `profile-notebook`: a profile's Notebook folder, the profile setting `notebook.root`. An empty setting uses the default folder, `~/attn-notebook` (with the instance name on a non-default instance), or `<data folder>/notebook` on a daemon whose `ATTN_DATA_DIR` is not its instance's own folder.
- `notebook-sync`: an outpost keeps a synced copy of every Notebook folder the home's profiles use.

## How to get to it

### People

- Keys (`native/crates/attn-app/src/keymap.rs`): ⌥⌘N opens an editor tile (`notebook.openTile`), ⇧⌥⌘N opens the notebook fullscreen (`notebook.openFullscreen`).
- Palette (`native/crates/attn-app/src/palette/commands.rs`): `notebook` ("Open the notebook") and `notebook-tile` ("Open Editor tile").
- Title bar: the "Open Notebook (⇧⌥⌘N)" button, `titlebar.notebook` (`native/crates/attn-app/src/sidebar/chrome.rs`).
- Fullscreen and tile (`native/crates/attn-app/src/notebook/view.rs`): the "Notebook files" list, the document with its save status, the Outline and Backlinks sections, the "chief: active" or "chief: idle" status, and "Close" (Esc) fullscreen.
- Editor tile: its "Editor root" picker (`native/crates/attn-app/src/notebook/root_picker.rs`) and its note finder, "Find a note…" (`native/crates/attn-app/src/notebook/finder.rs`).
- **Settings → Notebook Folder** (`native/crates/attn-app/src/settings/desktop.rs`): "Notebook folder" sets `notebook.root` for the profile the window shows, and a blank value means the default folder. "This profile uses:" shows `notebook.root.effective`.
- Where people see what agents do: journal entries and notes agents write appear in the open notebook as the files change, and the status shows whether the chief is active.
- Their own shell: the files themselves, and the verbs below.

### Agents

- `attn journal append (--entry <text> | --entry-file <path>) [--date YYYY-MM-DD] [--session <id>] [--json]` (`cmd/attnd/main.go`). There is no notebook CLI: agents read and edit notes with their own file tools.
- `attn settings get notebook.root.effective [--profile <name|id>]` gives the folder the caller's profile uses (on an outpost, the copy of that folder, inside a session only). `attn settings set notebook.root <path> [--profile <name|id>]` sets a profile's folder. Inside a session, both commands use the session's profile.
- Prompts: the Chief's guidance names the notebook (`internal/prompts/content/crew/v2/chief.md`), and `attn skill --reference notebook` covers the two layers, frontmatter, links and the journal command (`internal/prompts/content/skills/attn/references/notebook.md`).

### Triggers

- An outpost's copies sync with the home's folders while Outposts is on (`internal/daemon/notebook_sync.go`). The home pushes the folder list on connect and when a profile's folder, or the set of profiles, changes (`internal/daemon/notebook_folders.go`). When the outpost rejects the list, the Outposts row shows the rejection and the home tries again on its own, every 2 s at first and backing off to every 10 s; the outpost's Notebook stream opens only after it accepts a list. When the stream closes while the bridge stays up, such as when one side sends an index for a folder the other just dropped, the Outposts row shows `Notebook stream: connection closed` and the home reopens the stream on the same bridge, every 2 s at first and backing off to every 10 s.

## Driving it

Preconditions:

- Use the baseline instance with its app, launched as [../SKILL.md](../SKILL.md) says. Then run `bridge wait '{"target":{"key":"home"}}'`. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`.
- The steps press the macOS chords. On Linux press `mod+alt+shift+n` for the editor tile (`mod+alt+n`), `mod+alt+shift+f` for fullscreen (`mod+alt+shift+n`) and `mod+shift+p` for the finder (`mod+p`).
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
- **Fullscreen with the shortcut.** Run `bridge key '{"chord":"mod+alt+shift+n"}'` and `bridge wait '{"target":{"key":"notebook.panel"}}'`. `bridge find '{"target":{"key":"notebook.title"}}'` is named `Notebook`, and `notebook.file.journal` is in `bridge tree`. Press Escape: `bridge wait '{"target":{"key":"notebook.panel"},"gone":true}'` answers.
- **Fullscreen from the palette and the title bar.** Run `bridge key '{"chord":"mod+shift+k"}'`, `bridge type '{"text":"Open the notebook"}'` and `bridge key '{"chord":"enter"}'`: `notebook.panel` answers. Close it and run `bridge press '{"target":{"key":"titlebar.notebook"}}'`: it answers again.
- **Find a note and read it.** With the notebook open, run `bridge run '{"name":"file.open"}'`, `bridge wait '{"target":{"key":"notebook.finder.field"}}'`, `bridge type '{"text":"<date>"}'` and `bridge key '{"chord":"enter"}'`. `notebook.document.title` is named `<date>`, and `bridge tree` holds `drive-attn journal` and `The mock agent wrote this.` as `notebook.editor.line.<n>` text. The markdown opener did not open: `opener` is not in `bridge tree`.
- **It follows the disk.** Run `./attn journal append --entry "appended while open"`. `bridge wait '{"target":{"role":"label","name":"appended while open"}}'` answers.
- **Edit, save and undo.** Run `bridge press '{"target":{"key":"notebook.editor"}}'`, `bridge key '{"chord":"mod+down"}'` and `bridge type '{"text":"\nTyped in attn."}'`. `notebook.status` reads `Saved` within a second, and `cat $NB/journal/<date>.md` ends with `Typed in attn.`. Run `bridge key '{"chord":"mod+z"}'`: the line leaves `bridge tree`, and once `notebook.status` reads `Saved` it leaves the file. `bridge key '{"chord":"mod+shift+z"}'` brings it back.
- **Editor tile.** A tile's keys under `notebook.tile.<tile>.` exist only while the tile is on screen, not shelved.
  - Close the notebook, go Home with `bridge press '{"target":{"key":"sidebar.home"}}'`, and run `bridge key '{"chord":"mod+alt+n"}'`: the window leaves Home for the current desktop with the tile on it. `bridge state '{"of":"desktops"}'` holds a tile whose `tile_kind` is `notebook`, and `wsctl list` shows it on the current desktop. With no agent on screen it opens on the notebook, and its finder opens: `notebook.finder.field` answers. Press Escape.
  - `bridge press '{"target":{"key":"notebook.tile.<tile>.root-trigger"}}'` lists `Notebook` and `Browse…` as options. Press Escape.
  - Close the tile: press `notebook.tile.<tile>.surface`, then run `bridge run '{"name":"session.close"}'`, which is what ⌘W's menu item runs. Open it again from the palette: type `Open Editor tile`.
  - Press nb-agent's sidebar row, `sidebar.session.nb-agent`, and press ⌥⌘N again: the tile opens on `$A`, `bridge state '{"of":"desktops"}'` shows its `tile_params` `{"root":"$A"}` written out, and `bridge state '{"of":"desktops"}'` has no `context` rail for it because it is not the notebook.
  - Press `notebook.tile.<tile>.surface`, then run `bridge run '{"name":"file.open"}'`: `notebook.finder.field` answers inside the tile, and the markdown opener does not.
- **Drafts leave the screen with the tile.** With `narrowest_tile_cols` at 150, so only one tile fits, type in the editor tile and press another tile's shelf chip within 700 ms: `cat` shows the edit on disk, and pressing the tile's chip brings it back. Append to the file from a shell between typing and pressing: the tile returns with the edit and `notebook.tile.<tile>.document.conflict`, and pressing `notebook.tile.<tile>.document.overwrite` saves it. Run `chmod 555` on the folder before typing: the tile returns with the edit and an alert naming the error, and after `chmod 755` `notebook.tile.<tile>.status` reads `Saved`.
- **Two edits of one tile.** With the folder at `chmod 555`, type `MAIN` in the tile, pop it out with `tile.<tile>.popout`, type `POPPED` in its window, bring it back with `bridge run '{"name":"tile.popOut"}'`, then pop it out and back once more. After `chmod 755`, `cat` shows `MAIN`, the tile then shows `POPPED` with `notebook.tile.<tile>.document.conflict`, and pressing `notebook.tile.<tile>.document.overwrite` saves it.
- **Change the editor root.** Press `notebook.tile.<tile>.root-trigger`, then the option `Notebook`, key `option0`: `wsctl list` shows the tile's params empty.
- **The Chief's status.**
  - Run `./attn crew set chief --agent claude --cwd $A`, then `./attn crew start chief --json`, and note its `session_id` as `<chief>`.
  - Open the notebook: `notebook.chief` reads `chief: idle`.
  - Run `wsctl input --id <chief> --text "journal-entry" --enter`: `bridge wait '{"target":{"key":"notebook.chief"}}'` reads `chief: active`, then `chief: idle`, and the open journal shows one more `The mock agent wrote this.` without a reload.
  - Press `notebook.close`: `notebook.panel` goes.
  - Run `./attn crew close chief`. An agent cannot close the Chief's session.
- **Notebook Folder in Settings.**
  - Open Settings as [settings.md](settings.md) says and press `settings.nav.desktop`.
  - Run `bridge type '{"target":{"key":"settings.input.notebook.root.field"},"text":"<folder>"}'` with a folder under the home folder, such as `$HOME/attn-nbprof-<name>` written out, and press Enter: `settings.notebook.effective` reads `This profile uses:` that folder, and `./attn settings get notebook.root` prints it.
  - Clear the field (`mod+a`, Backspace, Enter): the effective folder is `$NB` again and the setting is empty.
  - Set and clear the folder from the shell with `./attn settings set notebook.root <folder>` and `""`: the open field and `This profile uses:` follow without reopening Settings.
  - Press Escape.
- **Settings and an entry file from an agent.**
  - Make a folder for a second agent with `A2=$(mktemp -d /tmp/drive-attn-notebook2.XXXX)` and append it to the env file. Save `Entry from a file.` as `$A2/entry.md`.
  - Save a fixture in `$A2` whose turns run `["settings","get","notebook.root.effective"]` on `nb-root`, `["settings","set","notebook.root","<home>/attn-nbagent-<name>"]` on `nb-set`, `["settings","set","notebook.root",""]` on `nb-clear` and `["journal","append","--entry-file","<A2>/entry.md","--json"]` on `nb-file`, with the paths written out.
  - Start a second agent in `$A2`. Type each trigger through `wsctl input`. Peek then shows `$NB`, then `notebook.root = <folder>` (and `./attn settings get notebook.root.effective` from the shell prints that folder, since both are in the Default profile), then an empty `notebook.root`, then `rel_path` `journal/<date>.md`, and the day's file holds `Entry from a file.`.
- **The person's shell with a profile and a session.** `./attn settings get notebook.root.effective --profile Default` prints `$NB`. `./attn settings set notebook.root <folder> --profile Default` changes it and `""` clears it. `./attn journal append --entry "with session flag" --session <second agent's session id>` exits 0 and appends the entry.
- **What agents are told.** `./attn crew prime` in the Chief's session prints the Chief's guidance, `## You are the Chief` (`internal/prompts/content/crew/v2/chief.md`), and `./attn skill --reference notebook` prints the notebook reference with `attn journal append` and `attn settings get notebook.root.effective`.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, the day's journal file after each append and after the edit and undo, `./attn agent peek nb-agent` after the agent's append, `bridge tree` after each way in, and `wsctl list` with the editor tile open. Capture `bridge shot '{"path":"<P>/notebook-fullscreen.png"}'` with the journal open and `<P>/notebook-tile.png` with the editor tile, `<P>` written out. Name each file for its way in, such as `notebook-agent-journal-peek.txt`.

## Contexts

- **Durability across restarts**: the notebook is files on disk and does not depend on attn running. An edit that the app had not saved yet is the only thing a crash can lose, and the save status shows when an edit is unsaved. That includes an editor tile's edit whose save failed or conflicted while the tile was off screen, which the app holds in memory until the tile shows it again. An edit typed while its save is in flight saves again before the notebook closes, opens another note or lets a tile leave the screen. A tile's edit that fails or conflicts off screen also shows a notice in the window. Two unsaved edits of one tile, such as one handed back from its popped-out window while the tile holds another, both wait: the tile shows the other once its own reaches disk, and neither replaces the other. When the profile's Notebook folder changes with a note open, the edit saves into the folder it was read from, and the notebook then opens on the new folder. The open editor tile comes back with its desktop.
- **Linux**: `attn journal append` and the files work the same where the daemon and CLI run on Linux. The editor tile is Ctrl+Shift+Alt+N and fullscreen Ctrl+Shift+Alt+F there (`native/crates/attn-app/src/keymap.rs`).
- **Outposts**: an outpost keeps a copy of each Notebook folder at `~/attn-notebook-<name>-<key>`, synced with the home's, and conflict copies appear on the outpost's row in Settings. An agent there works in the copy of its profile's folder, its `notebook.root.effective`, and `attn seed attach`, `detach` and `check report --evidence` for that profile's seeds land in the profile's folder at the home. Agents on an outpost append to the journal with `attn journal append`, which writes at the home. While the outpost is offline its copies wait for the next sync.
- **Profiles**: each profile has its own `notebook.root`, and profiles that leave it blank use the default folder. The app's notebook, editor tiles, the chief's launch guidance, `attn journal append` and seed artifacts all follow the profile: the window's profile for the app, the session's profile for agents, the seed's profile for its files. After switching the window's profile, the notebook opens on that profile's folder. Repointing a profile never moves files. An editor tile rooted at another profile's folder keeps following the disk when that profile repoints or clears its folder from a shell (`attn settings set notebook.root "" --profile <name>`). To open the tile on that folder, place a terminal there first, since Browse opens a native dialog. Setting a profile's folder inside or around another profile's folder or the default folder is refused, paired or not, with an error naming both folders: `attn settings set notebook.root` exits 1, Settings shows it as an alert for a typed or browsed folder, and the Chief's setup shows it in the popover. A folder equal to another profile's is accepted, and they sync one copy.
- **Outposts and a folder change**: changing a profile's folder while paired syncs the new folder into its own copy and leaves the old copy unsynced. Pointing back moves the old copy to `<copy>.before-sync-<date>` and syncs it fresh.
- **Performance and idle cost**: the open notebook and each editor tile watch their folder for changes. Nothing measures the cost of a large notebook or of an idle open editor. When a change touches either, check with `docs/internal/perf-testing.md`.
- **Harnesses**: any harness reads and edits the files with its own tools and appends with the same `attn journal append`.
- **Keyboard and native input**: ⌥⌘N, ⇧⌥⌘N, ⌘P inside the notebook, Esc to close fullscreen. ⌘Z and ⇧⌘Z reach the editor. attn leaves ⌘E free for inline code.
- **Compatibility across versions**: attn keeps frontmatter keys it does not know as written, and `index.md` and `log.md` carry no frontmatter.
- **Accessibility**: the finder's field names the highlighted note as its active descendant (`notebook.finder.row.<path>`), so VoiceOver reads each note as ↑ and ↓ move. The editor (`notebook.editor`) is a multiline text input named "Note": its value is the note's markdown and its selection follows the caret, so VoiceOver reads the line or word the caret moves to and echoes typing. The caret's paragraph reads line by drawn line, as it wraps; the other paragraphs read whole. `bridge tree` shows the caret as `selection`, `[run + 1, character, run + 1, character]`, where a run is one line, or 255 characters of a longer one.

## Tests

- Wire: `native/crates/attn-app/tests/wire/notebook.rs`, `native/crates/attn-app/tests/wire/notebook/`, `internal/daemon/notebook_wire_test.go`, `internal/daemon/notebook_profiles_wire_test.go`, `internal/daemon/notebook_containment_wire_test.go`, `internal/daemon/journal_wire_test.go`, `internal/daemon/fs_watch_wire_test.go`.
- Stack: `cmd/attnd/notebook_sync_stack_test.go`, `cmd/attnd/agent_commands_stack_test.go`.
- Scenario: `native-notebook-editor-undo`, `native-notebook-link-nav`, `native-notebook-tile-close` and `native-notebook-sync`.
- Kernel: `internal/notebook/*_test.go`.
- Kernel: the tests beside `native/crates/attn-app/src/notebook/**`.

## Gotchas

- On a fresh instance the notebook folder does not exist until something writes to it. The first `attn journal append` creates the folder. `instance clean` leaves `~/attn-notebook-<name>` behind, so a later instance with the same name starts with old entries. Read the day's file before counting entries.
- Entries are dated by the home's clock. A run across midnight writes two files.
- Profiles with a blank `notebook.root` all use the default folder. A run in a second such profile sees the first profile's notes, by design.
- ⌘P means the note finder while focus is inside the notebook or an editor tile, and the markdown opener everywhere else. A freshly docked editor tile is not focused until you press it.
- `bridge shot` shows the editor, but only the file on disk proves a save.
- Working but wrong: the palette calls the tile "Editor tile" while the tile kind in `tree_json` reads `notebook`. They are the same thing. Fullscreen and the tile share one editor, so a key that works in one and not the other is a bug.
