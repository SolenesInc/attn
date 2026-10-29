# Ledger

Every session, live and closed, like a chat history: resume a closed one as
itself, keep or forget its conversation. Each profile has one ledger. A
session is attn's record of one conversation, and closing it keeps its row
with who closed it and why. People open the ledger to filter it, focus a live
session, or bring a closed one back in a tile under its own id; its
Conversations list keeps attn's own copies of Claude conversations. Agents
list and read sessions, rename their own, read transcripts and resume sessions
from the CLI. When a harness moves to another conversation, its terminal shows
that conversation's session and the one it left goes to the ledger.

## Actions and ways in

### Find and open sessions

⌘⇧L (`sessions.open`, `app/src/shortcuts/registry.ts`), palette `sessions` ("Open the sessions
list", `app/src/application/useAppCommands.tsx`) and sidebar "Open Ledger (⌘⇧L)"
(`app/src/application/useAppSidebarActions.tsx`) open "Sessions, worktrees and conversations" on its
last list. `]` moves between Sessions, Worktrees and Conversations
(`app/src/components/ledger/LedgerSurface.tsx`); [worktrees.md](worktrees.md) owns Worktrees.
Sessions offers Live, Closed or All, newest first, with a query field for tokens such as `profile:`
and `repo:`. Each row shows harness, outpost, place, usage, who closed it and why, and whether it
can return. Focus opens a live session; "Reopen" resumes a closed one under its own id, on its last
or current desktop. Rows offer Show worktree, the tended seed, and Keep conversation or Unkeep for
Claude; an outpost row names its host and offers neither Show worktree nor Keep
(`app/src/components/ledger/SessionsTab.tsx`). A stopped tile's Resume is in
[agent-launch.md](agent-launch.md). Recreating a worktree or fetching a branch runs only when its
action is named.

### List, inspect and resume from the CLI

People and agents run `attn session list` with `--closed`, `--all`, `--tree`, `--reopen`, `--limit`,
`--before`, `--repository`, `--last`, `--since`, `--until`, `--profile` and `--json`; `show <id>`;
`transcript <id>` with `--after`, `--follow` and `--json`; `instructions <id> --question <text>`
(Codex only, answered by a background model with prompts in
`internal/prompts/content/session-instructions/`); or `reopen <id> [--action <name>]`
(`cmd/attn/session_instructions.go`, `cmd/attn/session_ledger.go`, `cmd/attn/session_reopen.go`,
`cmd/attn/session_tree.go`). Transcripts contain timestamped, redacted events; the tree groups live
sessions under their dispatcher. `attn list` prints live sessions and profiles as JSON
(`cmd/attn/main.go`); outside attn, `--profile <id>` selects a profile. The skill names `session list --tree` and `attn list --json`
(`internal/prompts/content/skills/attn/references/converse-and-observe.md`). `priority`, `snooze`
and `unsnooze` belong to [queue-flow.md](queue-flow.md).

### Rename a session

Palette `rename-active-session` ("Rename this session…") and the sidebar row actions menu's "Rename
session" (`app/src/components/SessionActionsPopover.tsx`) open the dialog. `attn session rename <name> [--session <id>]` renames from the CLI; the garden contract tells agents to rename as work
drifts (`internal/prompts/content/garden.md`).

### Keep or forget conversations

The Conversations list takes Enter to keep or unkeep, and `2` to ask to Forget
(`app/src/components/ledger/ConversationsTab.tsx`). An open seed blocks forgetting; copies kept by
crew letters list `Crew letter: <Member> · <date>`, and confirmation says `These letters stay, but will no longer point to this transcript: <Member> · <date>.` "Forget now" unlinks letters and
deletes the copy. `attn conversation list [--all] [--json]`, `keep`, `unkeep` and `forget <session-or-conversation-id> [--unlink-crew-letters]` (`cmd/attn/conversation.go`) accept
`session:<id>` or `conversation:<id>` when ambiguous. Forget refuses copies kept by crew letters and
names them `<member> <filename>`; the flag consents to removing each letter's session record (the
`.json` beside the letter in the member home's `handoffs/`), leaves the letters, deletes the copy
and prints `letters that lost their transcript (letters are unchanged):` with each `<member> <filename>`. An open seed blocks either form until harvested or withered. A keep pass decides when
to copy Claude conversations and delete unpinned copies (`internal/daemon/conversation_keep.go`).

### Follow a conversation switch

/clear, /new, /resume or Codex /agents moves a terminal to the new conversation's session. A Claude
session left behind closes into the ledger; shared Codex leaves it open and hidden, live over a link
with no tile (`internal/daemon/monitor_switch.go`, `internal/daemon/agent_conversation.go`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`).
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-ledger.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `L=$(mktemp -d /tmp/drive-attn-ledger-agent.XXXX) && echo "export L=$L" >> $DRIVE_ENV`, holding this fixture as `$L/.attn-mock-agent.json`:

```json
{"version": 1, "resumable": true, "turns": [
  {"includes": "hello", "actions": [{"type": "reply", "text": "hello back"}]},
  {"includes": "rename", "actions": [{"type": "attn", "args": ["session", "rename", "ledger renamed"]}]}
]}
```

- For the crew-letter steps: a member home made before the daemon starts, as [crew.md](crew.md)'s preconditions describe, here `Q="$(./attn instance resolve --field dataDir)/crew/quill" && echo "export Q=$Q" >> $DRIVE_ENV` with `mkdir -p "$Q"` and `printf '# Quill\n\nFile a letter when asked.\n' > "$Q/CHARTER.md"`; crew v2 on, so a letter keeps its session's transcript: `./attn settings set crew_v2_enabled true`; and a member folder, `K=$(mktemp -d /tmp/drive-attn-ledger-crew.XXXX) && echo "export K=$K" >> $DRIVE_ENV`, holding this fixture as `$K/.attn-mock-agent.json`:

```json
{"version": 1, "resumable": true, "turns": [
  {"includes": "You have been woken", "actions": [{"type": "reply", "text": "Quill here."}]}
]}
```

`<terminal>` is the agent's terminal id from `wsctl list`, read again after a resume; `<P>` is the proof folder's path written out.

- **A live row.** Run `wsctl add-session --cwd $L --agent claude --label ledger-agent --id ledger-agent --place`, then `wsctl input --id <terminal> --text "hello" --enter` and wait for `waiting_input` in `./attn agent list`. `./attn session list` shows a row `ledger-agent  claude  waiting_input` labelled `ledger-agent`, and `./attn list` prints JSON with a session whose `id` is `ledger-agent`.
- **Open with ⌘⇧L.** Run `bridge native_key '{"key":"l","modifiers":["command","shift"]}'`. `bridge sessions_get_state` shows `open: true`, `scope: "Live"` and a row with `id: "ledger-agent"` and `state` `waiting_input`. Press Escape: `open` is false.
- **Open from the palette and the sidebar.** Run `bridge native_key '{"key":"k","modifiers":["command","shift"]}'`, `bridge dom_wait '{"selector":"[role=\"combobox\"][aria-label=\"Commands\"]","focused":true,"timeoutMs":5000}'`, `bridge native_text '{"text":"Open the sessions list"}'` and `bridge native_key '{"key":"Enter"}'`: `sessions_get_state` shows `open: true`. Press Escape, then click `[aria-label^="Open Ledger"]` natively, its box taken with `bridge dom_bounds`: the ledger opens again.
- **An agent renames its session.** Run `wsctl input --id <terminal> --text "rename" --enter`. `./attn agent peek ledger-agent` shows `ledger-agent renamed to "ledger renamed"`, and `./attn session show ledger-agent` starts with `ledger-agent  ledger renamed`.
- **A person renames it.** Select it for setup with `bridge select_session '{"sessionId":"ledger-agent"}'`. Open the palette, type `Rename this session`, press Enter, and wait for `[role="dialog"][aria-label="Rename session"]`. The current name is already selected, so type `renamed by hand` with `bridge native_text` and press Enter. `./attn session show ledger-agent` starts with `ledger-agent  renamed by hand`, and the sidebar row reads the same.
- **Transcript.** Run `./attn session transcript ledger-agent`. It prints the prompt `hello` and the reply `hello back` as timestamped events; `--json` prints one event per line.
- **Close and read it back.** Run `./attn agent close ledger-agent -m "ledger drive" --source-session ledger-agent`. It prints `closed session ledger-a (renamed by hand): ledger drive`. `./attn session list --closed` lists `ledger-agent` as `closed` with `ledger-agent closed because: ledger drive`. `./attn session show ledger-agent` shows `state      closed`, `because    ledger drive`, `reopen     yes` and `place      directory present`.
- **Keep, release and forget.** With the session closed, run `./attn conversation keep session:ledger-agent`: it prints `pinned conversation session:ledger-agent; attn keeps it forever`. A pass that starts after the pin makes the copy: re-read `./attn conversation list` with a deadline until its first line starts with `1 kept`. In the ledger, press `]` until Conversations shows: `bridge dom_text '{"selector":".ledger-panel"}'` includes `Forever · pinned`. `./attn conversation unkeep session:ledger-agent` prints `unpinned conversation session:ledger-agent; the next keep pass decides again`, and `./attn conversation forget session:ledger-agent` prints `deleted attn's copy of conversation session:ledger-agent; Claude's own files are untouched`.
- **A copy a crew letter keeps.** Run `./attn crew set quill --cwd $K --agent claude`, then `./attn crew wake quill --json`; its `session_id` is `<day>`. Re-read `./attn agent peek quill` until it shows `Quill here.` and its `state:` line stops reading `working`. Run `./attn handoff --session <day> -m "Dear successor, keep the reasons." --json`: it prints the letter's `path`, `<letter>`, and the day closes. `ls "$Q/handoffs"` lists the letter with `<letter>.json` beside it; copy the letter into `$P`. Re-read `./attn conversation list --json` until the row whose `session_ids` holds `<day>` has `kept` and `crew_letters` naming `quill` and the letter's file name; its `resume_id` is `<copy>`, and `./attn conversation list` shows it kept because of `crew letter quill <letter file>`. Wake, wait and hand off once more the same way for a second copy, `<copy2>` with `<letter2>`, for the app step.
- **Forget refuses a crew copy.** Run `./attn conversation forget conversation:<copy>`. It exits 1 with `error: conversation <copy> is kept by crew letters: quill <letter file>; to leave the letters and delete their transcript, rerun with --unlink-crew-letters`. The letter and `<letter>.json` are unchanged.
- **Forget with consent.** Run `./attn conversation forget conversation:<copy> --unlink-crew-letters`. It prints `deleted attn's copy of conversation conversation:<copy>; Claude's own files are untouched`, then `letters that lost their transcript (letters are unchanged):` and `quill <letter file>`. `cmp <letter> $P/<letter file>` exits 0, `<letter>.json` is gone, and `./attn conversation list --all --json` shows the row with `kept.deleted_at` set and no `crew_letters`.
- **Forget a crew copy in the app.** Open the ledger on Conversations (⌘⇧L, then `]` as above), click `.ledger-row[data-row-key="claude:<copy2>"] .ledger-row-title` natively and press `bridge native_key '{"key":"2"}'`. `bridge dom_text '{"selector":".ledger-inspector"}'` includes `These letters stay, but will no longer point to this transcript: Quill · <date>.` beside `Delete attn’s copy of`. Press Enter, which is Cancel: the confirmation goes and `<letter2>.json` is still there. Press `2` to ask again and `2` once more for "Forget now". Re-read `./attn conversation list --all --json` until the row for `<copy2>` has `kept.deleted_by` `"user"` and no `crew_letters`; `<letter2>` is unchanged and `<letter2>.json` is gone. Press Escape.
- **Resume from the ledger.** Press ⌘⇧L, set the Closed list for setup with `bridge sessions_set_filter '{"scope":"Closed"}'`, and click `.ledger-row[data-row-key="ledger-agent"] .ledger-row-trailing .ledger-verb` natively; it reads `Reopen`. `./attn agent list` lists `ledger-agent` again under the same id, and `./attn agent peek ledger-agent` shows `• hello back`, restored from its conversation.
- **Resume from the CLI.** Close it again as above, then run `./attn session reopen ledger-agent`. It prints `ledger-agent reopened in` with its folder and profile, and `./attn session list` shows it live.
- **A conversation switch.** Run `wsctl input --id <terminal> --text "/clear" --enter`. The same terminal now shows a new session: `./attn agent list` has a new row in `$L`, and `./attn session list --closed` lists `ledger-agent` as `closed`.
- **The tree.** Run `./attn session list --tree`. It lists the live sessions by profile; delegates nest under their dispatchers ([delegation.md](delegation.md)).
- **A question for a session.** Run `./attn session instructions <new session> --question "what did the user ask?"`. It exits 1 and prints `The target transcript is unavailable`, since the session is Claude Code and the verb reads Codex conversations; with `--json` the same failure prints `{"error":{"code":"transcript_unavailable",...}}` on stdout. `./attn session instructions nope --question "x"` exits 1 with `The target session was not found`. Start a Codex session the same way (`wsctl add-session --cwd $L --agent codex --label ledger-codex --id ledger-codex`), send it `hello`, and ask it: the baseline refuses the background model task, so it prints `The session-instructions model is unavailable` and exits 1.
- **Proof.** In `$P`, keep each `./attn` command with its output and exit code, `./attn session show ledger-agent` after the close and after each resume, `bridge sessions_get_state` after each way in, `ls "$Q/handoffs"` before and after each crew Forget, `bridge capture_screenshot '{"path":"<P>/ledger-crew-confirmation.png"}'` with the crew confirmation showing, and `bridge capture_screenshot '{"path":"<P>/ledger-closed.png"}'` with the Closed list showing the row. Put the way in in each file's name, such as `ledger-shortcut-state.json`.

## Contexts

- **Durability across restarts**: the ledger is the daemon's record. Closed rows, their reasons, names and kept conversations survive every restart. A session a machine restart stopped comes back `recoverable` and resumes on request.
- **Linux**: ⌘⇧L is Ctrl+Alt+L (`LINUX_SHORTCUTS`). The `attn session` and `attn conversation` verbs work the same where the daemon runs.
- **Outposts**: `attn session instructions` from an outpost is answered by the home, which holds the ledger. A live or closed row shows the outpost its session ran on by name, or by its host id once that outpost is removed, and offers neither Show worktree nor Keep conversation, since an outpost conversation has no kept copy at the home. "Reopen" or `attn session reopen <id>` resumes a closed outpost session on that same outpost, with its conversation and cost continuing; when its worktree was deleted, the `recreate_worktree_and_reopen` and `fetch_recreate_and_reopen` actions recreate it there, the second fetching the branch first. Resume refuses and the row stays closed when the outpost is unreachable or removed or Outposts is off (the refusal names the host), when the folder no longer exists there, and when the harness no longer has the conversation (`conversation <id> is no longer in codex's storage`).
- **Profiles**: each profile has its own ledger, and a session belongs to one profile for life. Resume lands in the session's own profile; a session whose profile was deleted cannot come back. Inside attn the session's profile scopes `session list --tree`; outside, `--profile` narrows it. The glossary still lists checking that a request with no profile does not list every profile.
- **Performance and idle cost**: the ledger reads in pages. The CLI ends a page with `showing N, M omitted, paginate with --before <id>`, and the dialog offers older rows on request.
- **Harnesses**: keeping a copy is for Claude Code conversations only, so Keep conversation shows on Claude rows that ran at the home. /clear moves Claude Code, /new moves Codex, /resume both, and Codex /agents under shared Codex. Under shared Codex the session left behind stays live and hidden instead of closing ([shared-codex.md](shared-codex.md)). Two Claude Code or Pi tiles on one conversation are two sessions, and both warn ([harnesses.md](harnesses.md)).
- **Keyboard and native input**: ⌘⇧L opens the ledger, `]` moves across its lists, arrows walk rows, Enter runs a row's first action, `2` asks to forget a kept conversation, and Escape closes. In the Forget confirmation, Enter is Cancel and `2` is "Forget now".
- **Compatibility across versions**: a later version reads today's sessions and kept conversations through the store's migrations. `attn conversation forget` against a daemon from before `--unlink-crew-letters` deletes the copy, then exits 1 with `daemon returned no conversation forget result`; restart the daemon on the CLI's build before driving `forget`. The CLI's stale-daemon warning shows only when the version numbers differ, so two builds of the same version give none.

## Tests

- Wire: `internal/daemon/session_ledger_wire_test.go`, `internal/daemon/session_ledger_ws_wire_test.go`, `internal/daemon/session_reopen_wire_test.go`, `internal/daemon/session_reopen_failure_wire_test.go`, `internal/daemon/session_close_wire_test.go`, `internal/daemon/session_close_metadata_wire_test.go`, `internal/daemon/session_recovery_wire_test.go`, `internal/daemon/rename_wire_test.go`, `internal/daemon/session_transcript_wire_test.go`, `internal/daemon/session_instructions_wire_test.go`, `internal/daemon/session_query_wire_test.go`, `internal/daemon/session_repository_wire_test.go`, `internal/daemon/sessions_filters_wire_test.go`, `internal/daemon/session_messages_wire_test.go`, `internal/daemon/conversation_wire_test.go`, `internal/daemon/conversation_keep_wire_test.go`, `internal/daemon/conversation_ledger_wire_test.go`, `internal/daemon/shared_conversation_wire_test.go`, `internal/daemon/spawn_resume_wire_test.go`, `internal/daemon/spawn_resume_picker_wire_test.go`, `scripts/verify-conversation-switches.sh`.
- Stack: `cmd/attn/session_ledger_stack_test.go`, `cmd/attn/session_recovery_stack_test.go`, `cmd/attn/session_recovered_evidence_stack_test.go`, `cmd/attn/session_transcript_stack_test.go`, `cmd/attn/session_tree_stack_test.go`, `cmd/attn/conversation_keep_stack_test.go`, `cmd/attn/crew_day_transcript_stack_test.go`, `cmd/attn/session_reopen_outpost_stack_test.go`, `internal/daemon/conversation_*_stack_test.go`, `app/e2e/session-restore-reconnect.spec.ts`, `scripts/verify-outpost-ledger-resume.sh`.
- Scenario: `sessions-surface`, `conversations-surface`, `session-reopen`, `session-close-ledger`, `clear-opens-session`.
- Kernel: `internal/store/session_ledger_test.go`, `internal/store/conversation_claim_test.go`.
- App: `app/src/App.sessionsLedger.test.tsx`, `app/src/App.sessionsLedgerTitles.test.tsx`, `app/src/App.sessionsDockButton.test.tsx`, `app/src/App.hiddenSessions.test.tsx`, `app/src/App.conversations.test.tsx`, `app/src/App.sessionSuccession.test.tsx`, `app/src/App.sessionProvenance.test.tsx`, `app/src/App.sessionSync.test.tsx`, `app/src/App.paletteRename.test.tsx`, `app/src/components/ledger/LedgerSurface.test.tsx`, `app/src/components/ledger/SessionsTab.*.test.tsx`, `app/src/components/ledger/ledgerQuery.test.ts`.

## Gotchas

- A fixture with `"resumable": true` writes the mock's conversation where the harness keeps its own: `.claude/projects` or `.codex/sessions` under `ATTN_TOOL_HOME`, which the baseline sets to a scratch folder. A daemon started without it writes into your real `~/.claude` or `~/.codex`; stop it and launch again in the instance shell. Without `resumable`, the conversation lives in the agent's folder and resume can find nothing to bring back.
- `attn list` takes no flags and never prints help: `attn list --help` lists every session as JSON. From a shell with no instance selected it reads production.
- `session rename` and `agent close` act as the calling session. From a plain shell they need `--session` or `--source-session`; drive the agent's door through the mock.
- `session show` and `session list` pad their labels to the widest row. Match `state      closed` as the label and its value, with any spacing.
- `session instructions` answers only for Codex sessions: any other target prints `The target transcript is unavailable`. For Codex it asks a background model, which the baseline refuses, so the verb cannot produce an answer there.
- `session instructions` reads like the launch instructions attn gives a session ([agent-guidance.md](agent-guidance.md)), but it asks a model about another session's conversation; it never prints what attn told that session.
- After `/clear`, wait for the new session in `./attn agent list` before reading the closed row.
- A live session's conversation is copied only after its transcript has been quiet for a day. `conversation keep` on a live session prints `0 kept` with `1 pending` and `forget` refuses with `attn has no live copy of conversation ... to forget`. Keep a closed session, or append `export ATTN_CONVERSATION_KEEP_QUIET=0s` to the env file before the daemon starts.
- `forget` also refuses while an open seed keeps the copy (`... is kept by open seeds: ...; harvest or wither them before forgetting`), with or without `--unlink-crew-letters`, and refuses a copy that crew letters keep until that flag is given. When removing a letter's session record or attn's copy fails partway, the error adds `unlinked crew letters: <letters, or none>; letters are unchanged`, and while attn's copy remains it ends with the exact `attn conversation forget conversation:<id> --unlink-crew-letters` to rerun. Working but wrong: after a consented Forget the row still shows `Crew letter:`, or the letter differs from before; the row drops its letters and the letter stays as it was.
- The baseline has no outpost to resume on ([outposts.md](outposts.md)), so resume on an outpost cannot be driven there; `scripts/verify-outpost-ledger-resume.sh` and `cmd/attn/session_reopen_outpost_stack_test.go` prove it.
- `sessions_set_filter` and `sessions_row_action` click through the DOM. Use them to set up; prove a person's way in with native clicks or keys.
- Working but wrong: the row action and `attn session reopen` say Reopen, and the palette describes the ledger "with reopen", where the glossary says resume. The palette says "Open the sessions list" and the sidebar "Open Ledger" for the same dialog.
