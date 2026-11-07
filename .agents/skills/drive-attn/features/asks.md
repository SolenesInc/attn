# Asks

An agent asks the user a question and keeps working. The user answers in
their own time, and the answer reaches the agent's conversation. An ask is
markdown with question blocks: pick one, pick any, or write an answer. People
answer every agent's asks from one drawer, one after another, without opening
the agents' terminals. attn types the answer into the asking agent's
conversation as the user's message. An ask about a seed goes to whoever tends
the seed by the time the user answers, and attn saves the answer as a note on
the seed. Asks are an experimental setting, off by default.

## Sub-features

- `ask-open`: an agent opens an ask from markdown on stdin or a file and gets its id at once.
- `ask-images`: the local images an ask shows, copied to the home when the ask opens and shown in the drawer from that copy.
- `ask-questions`: `:::question`, `:::question-multi` and `:::question-text` blocks, with choices, reasons and a recommended choice.
- `ask-drawer`: the drawer where people read and answer asks one after another.
- `ask-answer`: the user's answer, typed into the addressee's conversation as the user's message.
- `ask-replied`: asks answered in the last day, shown again with their answers.
- `ask-on-seed`: an ask about a seed the agent tends, answered to the seed's tender and kept as a note.
- `ask-list`: an agent lists its open asks and those about seeds it tends.
- `ask-close`: an agent withdraws an ask it no longer needs.
- `ask-markers`: the open-ask chip on the agent's tile header and the marker on its sidebar row.
- `ask-guidance`: what attn tells agents about asking.

## How to get to it

### People

- ⌘⇧R (`dock.asks`, `app/src/shortcuts/registry.ts`) opens and closes the asks drawer. The shortcut exists only while asks are on or any ask exists.
- Palette (`app/src/application/useAppCommands.tsx`): `asks` ("Open asks").
- Sidebar: the asks icon, labelled "Asks" or "N open asks", each followed by the shortcut (`app/src/application/useAppSidebarActions.tsx`).
- In the drawer (`app/src/components/Asks/AsksDrawer.tsx`): the ask list, each ask's questions with radio, checkbox, "Other…" and text answers (`app/src/components/Asks/AskQuestionCard.tsx`), the note "Anything else for <agent>?", "Send", "Replied" and "Close asks". Keys: ⌘↩ sends, j and k step through asks, r shows replied asks, o opens the asking agent, g opens the seed in the garden, Escape closes.
- On the asking agent's tile header, the chip "Open ask: <subject>" opens the drawer on that ask (`app/src/components/Asks/PaneAskChip.tsx`). A narrow tile cuts the subject, then shows only the `?` mark; the chip never leaves the header. The sidebar row carries a marker (`app/src/components/Asks/AskMarker.tsx`).
- **Settings → Experimental**: the "Asks" switch (`[data-testid="settings-feature-asks_enabled"]`, `app/src/components/SettingsModalSections.tsx`).
- Where people see what agents do: the title bar's asks button counts open asks, and an ask row says "Waiting on you" when its agent is also owed a turn.
- Their own shell: `attn ask list --session <id>` and `attn ask close <id> --session <id>`.

### Agents

- `attn ask` (`cmd/attn/ask.go`): with no subcommand it opens an ask from stdin or `--file`, and takes `--subject` and `--seed`. It reads each local image the markdown shows (resolved against the working directory, or beside `--file`) and sends the bytes with the ask; an unreadable image or more than 5 MiB of images refuses the ask. The subcommands are `list` and `close <id>`. Every form takes `--session` and `--json`.
- Prompts: the asks guidance in the session instructions of agents launched with asks on (`internal/prompts/content/ask/guidance.md`), and the answer as attn types it (`internal/prompts/content/ask/answer.md`, `entry.md`, `entry-picked.md`, `entry-recommended.md`, `entry-other.md`, `entry-unanswered.md`, `note.md`).

### Triggers

- The user's answer is user input in the addressee's inbox. A ring types the answer into the conversation without waiting for the agent's turn to end (`internal/daemon/asks.go`). The rules are in `docs/internal/inbox.md`.

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers, then run `bridge dismiss_whats_new` to dismiss What's new.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-asks.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Make an agent folder with `A=$(mktemp -d /tmp/drive-attn-asks-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`. Save `ask.md` (`:::question-text`, `Which day ships it?`, `:::`, one per line) in it, and this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "answered the ask", "actions": [
    {"type": "reply", "text": "got the answer"}]},
  {"includes": "withdraw", "actions": [
    {"type": "capture", "name": "ask", "pattern": "(a-[a-z0-9]+)"},
    {"type": "attn", "args": ["ask", "close", "{{ask}}"]}]},
  {"includes": "file ask", "actions": [
    {"type": "attn", "args": ["ask", "--file", "ask.md", "--subject", "Ship day", "--json"]}]},
  {"includes": "json list", "actions": [
    {"type": "attn", "args": ["ask", "list", "--json"]}]},
  {"includes": "list asks", "actions": [
    {"type": "attn", "args": ["ask", "list"]}]},
  {"includes": "ask about", "actions": [
    {"type": "capture", "name": "seed", "pattern": "(s-[a-z0-9]+)"},
    {"type": "attn", "args": ["seed", "tend", "{{seed}}"]},
    {"type": "attn", "args": ["ask", "--seed", "{{seed}}", "--subject", "Release note"], "input": ":::question-text\nWhat should the release note say?\n:::\n"}]},
  {"includes": "open an ask", "actions": [
    {"type": "attn", "args": ["ask", "--subject", "Cache store"], "input": "Deploys empty the cache.\n\n:::question\nWhich store should keep the cache?\n- [ ] Postgres — already deployed\n- [ ] Redis — faster, one more service\nRecommended: Postgres\n:::\n"}]}
]}
```

- While asks are still off, launch an agent with `wsctl add-session --cwd $A --agent claude --label ask-early --id ask-early`.

`<early>` and `<terminal>` are the terminal ids of ask-early and ask-agent from `wsctl list`. `<ask>` is the id an `Asked` reply prints.

- **Turn asks on.**
  - Press ⌘, (`ui.openSettings`) and click `[data-testid="settings-nav-experimental"]` natively.
  - At the baseline window size the Asks switch is below the viewport. Click `[aria-label="Search settings"]` natively to anchor keyboard focus, then repeat `bridge native_key '{"key":"Tab"}'` until `bridge dom_focus_state` names `activeElement.testId: "settings-feature-asks_enabled"`. Tab scrolls the switch into view.
  - Click `[data-testid="settings-feature-asks_enabled"]` natively, resolving its point after the scroll. Its `aria-checked` turns `true`, and `./attn settings get asks_enabled` prints `true`. Close Settings with Escape.
  - From a shell, `./attn settings set asks_enabled true` does the same and prints `asks_enabled = true`.
- **An agent launched before is refused.** Run `wsctl input --id <early> --text "open an ask" --enter`. `./attn agent peek ask-early` shows `mock agent error:` with `asks are off for this session because it launched without them; ask in the conversation instead`.
- **An agent asks.** Run `wsctl add-session --cwd $A --agent claude --label ask-agent --id ask-agent --place`, then `wsctl input --id <terminal> --text "open an ask" --enter`. Peek shows `Asked <ask>: "Cache store". attn types the answer into this conversation as the user's message.` The sidebar icon's label starts with `1 open ask`: `bridge dom_wait '{"selector":"[aria-label^=\"1 open ask\"]","timeoutMs":5000}'` matches.
- **Open the drawer with the shortcut.** Run `bridge native_key '{"key":"r","modifiers":["command","shift"]}'`. `bridge dom_wait '{"selector":"[role=\"region\"][aria-label=\"Asks\"]","timeoutMs":5000}'` matches, and `bridge dom_text '{"selector":"[data-testid=\"ask-<ask>\"]"}'` reads `Cache store`, the question, `Postgres`, `recommended` and `Redis`.
- **Answer from the keyboard.** `bridge dom_wait '{"selector":"[data-testid=\"ask-<ask>\"] input","focused":true,"timeoutMs":5000}'` matches: the first choice has focus. Run `bridge native_key '{"key":"space"}'`, then `bridge native_key '{"key":"Enter","modifiers":["command"]}'`. The drawer shows its empty state, `.asks-empty`. Thirty seconds after the last `wsctl input` to ask-agent, re-read `./attn agent peek ask-agent` until the agent's screen shows the typed answer and `got the answer` after it. The `last assistant message:` line shows the mock's hidden `<!-- attn:state=waiting_input -->` instead. The typed answer contains `🙋 The user answered the ask "Cache store" (<ask>).`
- **Replied asks.** With the drawer open, run `bridge native_key '{"key":"r"}'`. `bridge dom_text '{"selector":".asks-list"}'` reads `Replied in the last day` and `Cache store` with `replied`. Press Escape to close the drawer.
- **Step through asks with the drawer's keys.**
  - Plant a second seed and run `wsctl input --id <terminal> --text "open an ask" --enter`, then `wsctl input --id <terminal> --text "ask about <seed2>" --enter`, so two asks are open.
  - Select ask-early for setup with `bridge select_session '{"sessionId":"ask-early"}'`.
  - Press ⌘⇧R: the first ask, `Cache store`, has focus, and `bridge dom_text '{"selector":".asks-ask .asks-ask-subject"}'` reads it.
  - Press `j`: it reads `Release note`, and focus is in its text field.
  - Click `.asks-ask .asks-ask-subject` natively to move focus to the drawer, then press `k`: it reads `Cache store`.
  - Press `j`, click the subject again and press `g`: `bridge garden_get_state` shows `here` naming the seed.
  - Press Escape, open the drawer again, click the subject and press `o`: the drawer closes and `bridge get_state` shows `activeLeaf.sessionId` `ask-agent`.
  - Run `wsctl input --id <terminal> --text "list asks" --enter` to read the id of the `Release note` ask. Withdraw that ask with `wsctl input --id <terminal> --text "withdraw <ask>" --enter`, so only `Cache store` stays open for the queue bar.
- **The queue bar.**
  - With an ask open, run `./attn settings set queue_mode_enabled true` and press ⌘B to collapse the sidebar. `bridge dom_text '{"selector":"[data-testid=\"title-bar-right\"] .title-bar-count-badge"}'` reads `1`.
  - Click the asks button in `[data-testid="title-bar-right"]` natively: the drawer opens on the ask.
  - Press Escape, run `./attn settings set queue_mode_enabled false`, and click `button[title="Expand sidebar"]` natively. Withdraw the `Cache store` ask the same way.
- **Open from the palette.** Open the palette as the README says, type `Open asks` and press Enter. The `[role="region"][aria-label="Asks"]` drawer opens. Press Escape.
- **An ask about a seed, from the tile chip.**
  - Plant a seed: `./attn seed plant "Asks drive"` prints `<seed>  <slug>  Asks drive`. Run `wsctl input --id <terminal> --text "ask about <seed>" --enter`. Peek shows `Asked <ask> about asks-drive: "Release note".`
  - Select ask-agent for setup with `bridge select_session '{"sessionId":"ask-agent"}'`. `[data-testid="ask-chip-ask-agent"]` carries `aria-label="Open ask: Release note"`. Click it natively: the drawer opens on that ask, and `bridge dom_text '{"selector":".asks-ask-seed"}'` names `asks-drive`. The text field has focus.
  - Run `bridge native_text '{"text":"Ship it on Friday."}'` and `bridge native_key '{"key":"Enter","modifiers":["command"]}'`. `./attn seed notes <seed>` shows the answer as a note.
- **List and withdraw from the agent.** Run `wsctl input --id <terminal> --text "open an ask" --enter`, then `wsctl input --id <terminal> --text "list asks" --enter`: peek shows `<ask>  Cache store`. Run `wsctl input --id <terminal> --text "withdraw <ask>" --enter`: peek shows `Withdrew <ask>.`, and the drawer shows `.asks-empty`. The drawer reopens in the Replied list when you last left it there, so when `.asks-list` reads `Replied in the last day`, press `r` once with focus off any text field.
- **Ask from a file, as JSON.** Run `wsctl input --id <terminal> --text "file ask" --enter`: peek shows the ask as JSON with `"subject":"Ship day"` and its `"id"`. Run `wsctl input --id <terminal> --text "json list" --enter`: peek shows `{"asks":[...]` naming that id and `"enabled":true`. Withdraw it as above.
- **Images travel with the ask.** Save a png as `$A/shots/shot.png` and run `./attn ask --session ask-agent` from `$A` with `![shot](shots/shot.png)` and a `:::question-text` block on stdin. Delete `$A/shots`, open the drawer with ⌘⇧R: `bridge dom_wait '{"selector":".asks-markdown img[src^=\"data:image/png\"]","timeoutMs":5000}'` matches. From an outpost session, the same ask shows its image at the home. `ls <data-dir>/asks/<ask>` lists the copy; after `./attn ask close <ask> --session ask-agent` the folder is gone. An image over the limit refuses with `max_ask_image_bytes=5242880`.
- **From the person's shell.** `./attn ask list --session ask-agent` prints `No open asks.` and `./attn ask list --session ask-early` prints `Asks are off for this session.`
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, `./attn agent peek ask-agent` after each ask and after each answer lands, `./attn seed notes <seed>`, `bridge dom_text` of the ask before answering and of the replied list, and `bridge capture_screenshot '{"path":"<P>/asks-drawer.png"}'` with an open ask on screen, where `<P>` is the folder's path written out. Name each file for its way in, such as `asks-chip-seed-notes.txt`.

## Contexts

- **Durability across restarts**: open asks, answers and the answer's inbox item are the daemon's records and survive app, daemon and machine restarts. The home keeps an ask's images under `<data-dir>/asks/<ask>/` until the ask's record goes. Each time the daemon starts, it copies any image an ask shows that has no copy yet, from its file on the home; this covers asks opened before attn copied images.
- **Outposts**: `attn ask` on an outpost reads the images there and the home keeps the copy, so the drawer shows them. Relative links to other files still resolve on the asker's machine and do not open from an outpost ask. The drawer shows answered asks for a day.
- **Linux**: `attn ask` works the same where the daemon and CLI run on Linux. `dock.asks` is Ctrl+Alt+R there, and Ctrl+Enter sends from the drawer.
- **Performance and idle cost**: an ask's markdown is at most 64 KiB, and its images at most 5 MiB together. The drawer loads each image once per ask it shows. Nothing measures the drawer's idle cost. When a change touches the drawer's rendering, check it for repaints with `docs/internal/perf-testing.md`.
- **Harnesses**: the guidance tells every harness to ask with `attn ask` when the decision can wait, and to keep the harness's own question tool for when it can do nothing until the user answers. The answer arrives as the user's message in every harness, so harness auto-mode reviewers count an approval in it as the user's.
- **Keyboard and native input**: ⌘⇧R opens the drawer and puts focus on the first answer field. The keys j, k, r, o, g and Escape work while focus is not in a text field, and ⌘↩ sends from anywhere in the drawer.
- **Compatibility across versions**: an agent keeps the asks setting it launched with. When you turn asks on, only agents launched afterward can ask. An agent launched earlier refuses `attn ask` until the agent is relaunched, so relaunch the agent instead of toggling the setting again.
- **Flow**: in queue flow, the title bar's asks button counts open asks. Clicking it or pressing ⌘⇧R opens the drawer. An ask row whose agent is owed a turn says "Waiting on you".

## Tests

- Wire: `internal/daemon/asks_wire_test.go`.
- Stack: `cmd/attn/ask_stack_test.go`, `cmd/attn/ask_images_stack_test.go`, `app/e2e/asks.spec.ts`.
- Kernel: `internal/ask/ask_test.go`.
- App: `app/src/App.asks.test.tsx`.

## Gotchas

- The Asks switch is below the visible Settings viewport. `dom_native_point` scrolls the switch into view before returning its point.
- The bridge keeps the app in the background, where CSS `:focus` can match nothing even when `document.activeElement` is the answer input. Use `dom_wait` with `"focused":true`.
- An answer is user input, so attn types it only after 30 seconds of quiet in the agent's terminal. `wsctl input` to the asking agent restarts that wait.
- `attn ask --seed <id>` needs the asker to tend the seed. attn refuses it from an agent that has not claimed the seed, with `nobody is tending <seed> ... tend it first`.
- attn types a multi-line answer as one bracketed paste, so the mock sees one prompt. Keep `answered the ask` as the fixture's first turn: the mock tries turns in order, and the rest of the answer carries the user's own words, which can match a later turn.
- The drawer's letter keys act only while focus is on the drawer itself. ⌘⇧R and `j` put focus in an answer field, and a text answer's field takes the letter as text, so click the ask's subject natively first. With the drawer closed, the letter reaches the agent's terminal and restarts the 30-second quiet wait.
- The `dock.asks` shortcut, the palette command and the sidebar icon exist only while asks are on or some ask exists. A fresh instance with asks off shows none of them. Their absence there is not a missing way in.
- Working but wrong: after sending, the drawer moves to the next open ask, or to its empty state, with no confirmation that the answer reached the agent. The proof is the agent's screen, not the drawer.
