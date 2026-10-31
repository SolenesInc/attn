# Asks

An agent asks the user a question and keeps working; the user answers in
their own time, and the answer lands in the agent's conversation. An ask is
markdown with question blocks: pick one, pick any, or write an answer. People
answer every agent's asks from one drawer, one after another, without opening
the agents' terminals. attn types the answer into the asking agent's
conversation as the user's message. An ask about a seed goes to whoever tends
the seed by the time the user answers, and the seed keeps the answer as a
note. Asks are an experimental setting, off by default.

## Sub-features

- `ask-open`: an agent opens an ask from markdown on stdin or a file and gets its id at once.
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

- ⌘⇧R (`dock.asks`, `app/src/shortcuts/registry.ts`) opens and closes the asks drawer, offered while asks are on or any ask exists.
- Palette (`app/src/application/useAppCommands.tsx`): `asks` ("Open asks").
- Sidebar: the asks icon, labelled "Asks" or "N open asks", each followed by the shortcut (`app/src/application/useAppSidebarActions.tsx`).
- In the drawer (`app/src/components/Asks/AsksDrawer.tsx`): the ask list, each ask's questions with radio, checkbox, "Other…" and text answers (`app/src/components/Asks/AskQuestionCard.tsx`), the note "Anything else for <agent>?", "Send", "Replied" and "Close asks". Keys: ⌘↩ sends, j and k step through asks, r shows replied asks, o opens the asking agent, g opens the seed in the garden, Escape closes.
- On the asking agent's tile header, the chip "Open ask: <subject>" opens the drawer on that ask (`app/src/components/Asks/PaneAskChip.tsx`); the sidebar row carries a marker (`app/src/components/Asks/AskMarker.tsx`).
- Settings, Experimental: the "Asks" switch (`[data-testid="settings-asks-toggle"]`, `app/src/components/SettingsModalSections.tsx`).
- Where people see what agents do: the queue bar counts open asks and shows them on hover, and an ask row says "Waiting on you" when its agent also owes a turn.
- Their own shell: `attn ask list --session <id>` and `attn ask close <id> --session <id>`.

### Agents

- `attn ask` (`cmd/attn/ask.go`): with no command, opens an ask from stdin or `--file`, with `--subject` and `--seed`; `list`; `close <id>`; `--session` and `--json`.
- Prompts: the asks guidance in the session instructions of agents launched with asks on (`internal/prompts/content/ask/guidance.md`); the answer as attn types it (`internal/prompts/content/ask/answer.md`, `entry.md`, `entry-picked.md`, `entry-recommended.md`, `entry-other.md`, `entry-unanswered.md`, `note.md`).

### Triggers

- The user's answer is a user message in the addressee's inbox; a ring types it into the conversation without waiting for the agent's turn to end (`internal/daemon/asks.go`; the rules are in `docs/internal/inbox.md`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-asks.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-asks-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "answered the ask", "actions": [
    {"type": "reply", "text": "got the answer"}]},
  {"includes": "withdraw", "actions": [
    {"type": "capture", "name": "ask", "pattern": "(a-[a-z0-9]+)"},
    {"type": "attn", "args": ["ask", "close", "{{ask}}"]}]},
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

- An agent launched while asks are still off: `wsctl add-session --cwd $A --agent claude --label ask-early --id ask-early`.

`<early>` and `<terminal>` are the terminal ids of ask-early and ask-agent from `wsctl list`; `<ask>` is the id an `Asked` reply prints.

- **Turn asks on.** Press ⌘, (`ui.openSettings`) and click `[data-testid="settings-nav-experimental"]` natively. At the baseline window size the Asks switch is below the viewport. Click `[aria-label="Search settings"]` natively to anchor keyboard focus, then repeat `bridge native_key '{"key":"Tab"}'` until `bridge dom_focus_state` names `activeElement.testId: "settings-asks-toggle"`. Tab scrolls the switch into view. Click `[data-testid="settings-asks-toggle"]` natively, resolving its point after the scroll. Its `aria-checked` turns `true`, and `./attn settings get asks_enabled` prints `true`. Close Settings with Escape. From a shell, `./attn settings set asks_enabled true` does the same and prints `asks_enabled = true`.
- **An agent launched before is refused.** Run `wsctl input --id <early> --text "open an ask" --enter`. `./attn agent peek ask-early` shows `mock agent error:` with `asks are off for this session because it launched without them; ask in the conversation instead`.
- **An agent asks.** Run `wsctl add-session --cwd $A --agent claude --label ask-agent --id ask-agent --place`, then `wsctl input --id <terminal> --text "open an ask" --enter`. Peek shows `Asked <ask>: "Cache store". attn types the answer into this conversation as the user's message.` The sidebar icon's label starts with `1 open ask`: `bridge dom_wait '{"selector":"[aria-label^=\"1 open ask\"]","timeoutMs":5000}'` matches.
- **Open the drawer with the shortcut.** Run `bridge native_key '{"key":"r","modifiers":["command","shift"]}'`. `bridge dom_wait '{"selector":"[role=\"region\"][aria-label=\"Asks\"]","timeoutMs":5000}'` matches, and `bridge dom_text '{"selector":"[data-testid=\"ask-<ask>\"]"}'` reads `Cache store`, the question, `Postgres`, `recommended` and `Redis`.
- **Answer from the keyboard.** `bridge dom_wait '{"selector":"[data-testid=\"ask-<ask>\"] input","focused":true,"timeoutMs":5000}'` matches: the first choice holds the keyboard. Run `bridge native_key '{"key":"space"}'`, then `bridge native_key '{"key":"Enter","modifiers":["command"]}'`. The drawer shows its empty state, `.asks-empty`. Thirty seconds after the last `wsctl input` to ask-agent, re-read `./attn agent peek ask-agent` until it shows `got the answer` as its last assistant message and the typed answer on its screen, containing `🙋 The user answered the ask "Cache store" (<ask>).`
- **Replied asks.** With the drawer open, run `bridge native_key '{"key":"r"}'`. `bridge dom_text '{"selector":".asks-list"}'` reads `Replied in the last day` and `Cache store` with `replied`. Press Escape to close the drawer.
- **Open from the palette.** Open the palette as the README says, type `Open asks` and press Enter. The `[role="region"][aria-label="Asks"]` drawer opens. Press Escape.
- **An ask about a seed, from the tile chip.** Plant a seed: `./attn seed plant "Asks drive"` prints `<seed>  <slug>  Asks drive`. Run `wsctl input --id <terminal> --text "ask about <seed>" --enter`. Peek shows `Asked <ask> about asks-drive: "Release note".` Select ask-agent for setup with `bridge select_session '{"sessionId":"ask-agent"}'`; `[data-testid="ask-chip-ask-agent"]` carries `aria-label="Open ask: Release note"`. Click it natively: the drawer opens on that ask, and `bridge dom_text '{"selector":".asks-ask-seed"}'` names `asks-drive`. The text field holds the keyboard; run `bridge native_text '{"text":"Ship it on Friday."}'` and `bridge native_key '{"key":"Enter","modifiers":["command"]}'`. `./attn seed notes <seed>` shows the answer as a note.
- **List and withdraw from the agent.** Run `wsctl input --id <terminal> --text "open an ask" --enter`, then `wsctl input --id <terminal> --text "list asks" --enter`: peek shows `<ask>  Cache store`. Run `wsctl input --id <terminal> --text "withdraw <ask>" --enter`: peek shows `Withdrew <ask>.`, and the drawer shows `.asks-empty`.
- **From the person's shell.** `./attn ask list --session ask-agent` prints `No open asks.` and `./attn ask list --session ask-early` prints `Asks are off for this session.`
- **Proof.** In `$P`, keep each `./attn` command's output and exit code; `./attn agent peek ask-agent` after each ask and after each answer lands; `./attn seed notes <seed>`; `bridge dom_text` of the ask before answering and of the replied list; and `bridge capture_screenshot '{"path":"<P>/asks-drawer.png"}'` with an open ask on screen, where `<P>` is the folder's path written out. Put the way in in each file's name, such as `asks-chip-seed-notes.txt`.

## Contexts

- **Durability across restarts**: open asks, answers and the answer's inbox item are the daemon's records and survive app, daemon and machine restarts. The drawer shows answered asks for a day.
- **Linux**: `attn ask` works the same where the daemon and CLI run on Linux. `dock.asks` is Ctrl+Alt+R there, and Ctrl+Enter sends from the drawer.
- **Performance and idle cost**: an ask's markdown is at most 64 KiB. Nothing measures the drawer's idle cost; when a change touches its rendering, check it for repaints with `docs/internal/perf-testing.md`.
- **Harnesses**: the guidance tells every harness to ask with `attn ask` when the decision can wait, and to keep its own question tool for when it can do nothing until the user answers. The answer arrives as the user's message in every harness, so harness auto-mode reviewers count an approval in it as the user's.
- **Keyboard and native input**: ⌘⇧R opens the drawer and puts the keyboard on the first answer field; j, k, r, o, g and Escape work while focus is not in a text field, and ⌘↩ sends from anywhere in the drawer.
- **Compatibility across versions**: an agent keeps the asks setting it launched with. Turning asks on reaches only agents launched after it; earlier ones refuse `attn ask` until they relaunch.
- **Flow**: in queue flow, the queue bar counts open asks and shows them on hover; clicking it or ⌘⇧R opens the drawer. An ask row whose agent owes a turn says "Waiting on you".

## Tests

- Wire: `internal/daemon/asks_wire_test.go`.
- Stack: `cmd/attn/ask_stack_test.go`, `app/e2e/asks.spec.ts`.
- Kernel: `internal/ask/ask_test.go`.
- App: `app/src/App.asks.test.tsx`.

## Gotchas

- The Asks switch lies below the visible Settings viewport; `dom_native_point` scrolls it into view before returning its point.
- The bridge keeps the app in the background, where CSS `:focus` can match nothing even when the answer input owns `document.activeElement`. Use `dom_wait` with `"focused":true`.
- Asks apply to agents launched after the setting turns on. An agent launched before refuses `attn ask` for its whole life; relaunch it rather than toggling the setting again.
- An answer is a user message, so attn types it only after 30 seconds of quiet in the agent's terminal. `wsctl input` to the asking agent restarts that wait.
- `attn ask --seed <id>` needs the asker to tend the seed. Without a claim it is refused with `nobody is tending <seed> ... tend it first`.
- attn types a multi-line answer as one bracketed paste, so the mock sees one prompt. Keep `answered the ask` as the fixture's first turn: the mock tries turns in order, and the rest of the answer carries the user's own words, which can match a later turn.
- The `dock.asks` shortcut, the palette command and the sidebar icon exist only while asks are on or some ask exists. A fresh instance with asks off shows none of them; that is not a missing way in.
- Working but wrong: after sending, the drawer moves to the next open ask, or to its empty state, with no confirmation that the answer reached the agent. The proof is the agent's screen, not the drawer.
