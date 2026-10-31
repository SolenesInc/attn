# Inbox

Messages, seed updates and quick captures addressed to an agent or a mailbox; a
ring prompts the agent to read them, like a push notification. A sender saves an
item for an address: a session, or a mailbox that whichever session holds it
reads (a crew member, the chief, a seed's tender). attn then decides when to
ring. A ring types what the user wrote into the conversation as the user's
message, and ends with a 📬 line for everything else, which the agent reads with
`attn agent inbox`. Items are durable and stay unread until read, typed,
withdrawn or replaced. `docs/internal/inbox.md` holds the rules; this file
drives them. Reading another agent's screen with `attn agent peek` is
[agent-state.md](agent-state.md)'s, and what a ring's run does to the queue is
[queue-flow.md](queue-flow.md)'s.

## Sub-features

- `peer-message`: one agent sends another a message; the sender gets a receipt at once.
- `msg-status`: the sender checks whether its message is queued, notified or read.
- `inbox-read`: read up to 20 unread items, oldest first, or one rung peer message again by id.
- `mailbox`: address a crew member, the chief or a seed, so the item follows whoever holds that job.
- `ring`: one prompt per address with unread items; at most 3 attempts per item, 5 minutes apart.
- `inbox-guidance`: what attn tells agents about messages and rings.

## How to get to it

### People

- No control of its own opens the inbox. People see a ring as the 📬 line in the agent's terminal, and the typed text of their own answers and quick captures there.
- Settings, Experimental: "Quiet notifications in the queue" keeps a ring's run out of the queue; [queue-flow.md](queue-flow.md) drives it.
- Items people put in come through other features: an answer to an ask ([asks.md](asks.md)) and a quick capture ([quick-capture.md](quick-capture.md)).
- Their own shell: `attn agent msg <target> "text" --source-session <id>` sends as that session, and `attn agent inbox --session <id>` reads as it.

### Agents

- `attn agent` (`cmd/attn/agent.go`): `msg <session-or-member-or-seed> "text"` with `--source-session`, `msg-status <message-id>` with `--session`, and `inbox [message-id]` with `--limit` and `--session`; `--json` on each.
- Prompts: the ring's 📬 line (`internal/prompts/content/session/inbox-notification.md`); how `attn agent inbox` prints items (`session/inbox-item.md`, `inbox-more.md`, `inbox-empty.md`, `peer-message.md`); the quiet-run note (`session/inbox-quiet.md`); the rules for reaching another agent in the attn skill (`internal/prompts/content/skills/attn/references/converse-and-observe.md`).

### Triggers

- An item lands, an outstanding ring ends, or the agent reaches a safe prompt: attn rings the address, retrying every 5 minutes up to 3 attempts per item (`internal/daemon/inbox_delivery.go`, `internal/daemon/inbox.go`).
- An item for an asleep crew member wakes the member first; [crew.md](crew.md) drives it.
- Seed updates ([garden.md](garden.md)), PR watch updates ([pull-requests.md](pull-requests.md)), answers ([asks.md](asks.md)), quick captures ([quick-capture.md](quick-capture.md)) and crew notices ([crew.md](crew.md)) arrive through this inbox; their features drive the events that cause them.

## Driving it

Preconditions:

- The baseline instance, daemon only.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-inbox.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-inbox-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`. Both agents below run in it:

```json
{"version": 1, "turns": [
  {"includes": "unread items", "actions": [
    {"type": "attn", "args": ["agent", "inbox"]}]},
  {"includes": "tell", "actions": [
    {"type": "capture", "name": "to", "pattern": "tell (\\S+)"},
    {"type": "attn", "args": ["agent", "msg", "{{to}}", "hello from the sender"]}]},
  {"includes": "tend", "actions": [
    {"type": "capture", "name": "seed", "pattern": "(s-[a-z0-9]+)"},
    {"type": "attn", "args": ["seed", "tend", "{{seed}}"]}]}
]}
```

- Two agents: `wsctl add-session --cwd $A --agent claude --label inbox-a --id inbox-a` and the same with `inbox-b`. `<a>` and `<b>` are their terminal ids from `wsctl list`; `<message>` is the id a receipt prints.
- inbox-b has settled once: run `wsctl input --id <b> --text "ready" --enter`, wait until the `state:` line of `./attn agent peek inbox-b` stops reading `working`, and start the first step 30 seconds after the input. Wait for that again before each later send to inbox-b: a turn still running holds the ring back.

- **Message an agent.** Run `wsctl input --id <a> --text "tell inbox-b" --enter`. `./attn agent peek inbox-a` shows the receipt `notified: notified inbox-b (id <message>)`. The ring types the 📬 line into inbox-b, which reads its inbox: `./attn agent peek inbox-b` shows `📨 from session inbox-a: hello from the sender` and the note that the message is from another agent, not the user.
- **The sender checks.** Re-read `./attn agent msg-status <message> --session inbox-a` until it prints `read: message <message> to session inbox-b`; it reads `notified` until inbox-b has run `attn agent inbox`.
- **Read again and read empty.** `./attn agent inbox <message> --session inbox-b` prints the same 📨 message again. `./attn agent inbox --session inbox-b` prints `inbox empty`.
- **From the person's shell.** Run `./attn agent msg inbox-b "from the shell"`. It exits 2 with `no sender: this shell is not an attn session, so pass --source-session <id>`. Run `./attn agent msg inbox-b "from the shell" --source-session inbox-a`: the receipt says `notified`, and peek on inbox-b shows the 📨 line with that text.
- **Refusals.** Run `./attn agent msg inbox-b "from the shell" --source-session inbox-a` again within 10 seconds: it prints `refused: you already sent that exact text to this session within the last 10s; say something new, or wait for a reply` and exits 1. `./attn agent msg inbox-a "me" --source-session inbox-a` prints `refused: that is this session; a message to yourself is not a conversation` and exits 1.
- **A seed's mailbox.** Plant a seed: `./attn seed plant "Inbox drive"` prints `<seed>  <slug>  Inbox drive`. Run `wsctl input --id <a> --text "tell <seed>" --enter`: peek on inbox-a shows `queued: queued (seed has no reachable tender; waits for its next tender)`. Run `wsctl input --id <b> --text "tend <seed>" --enter`. Thirty seconds after that input, peek on inbox-b shows the 📨 line from inbox-a: the item followed the seed to its new tender.
- **Typing holds the ring.** Run `wsctl input --id <b> --text "hold on" --enter`, then at once `./attn agent msg inbox-b "after typing" --source-session inbox-a`. The receipt is `queued (the user typed in the target moments ago; lands once the composer has been quiet for a while)`. Thirty seconds after the input, peek on inbox-b shows the 📨 line.
- **Proof.** In `$P`, keep each `./attn` command's output and exit code, `./attn agent peek inbox-a` after each send and `./attn agent peek inbox-b` after each ring, and `./attn agent msg-status <message> --session inbox-a --json`. Put the way in in each file's name, such as `inbox-seed-mailbox-peek.txt`.

## Contexts

- **Durability across restarts**: items, attempts and outstanding rings are the daemon's records. After a daemon restart, delivery resumes for every address with unread items once recovery finishes; a restart gives no item new attempts. A message sent during recovery is `queued until daemon recovery completes`.
- **Linux**: `attn agent msg`, `msg-status` and `inbox` work the same where the daemon and CLI run on Linux.
- **Outposts**: an item for an agent on an outpost whose host is unreachable waits, and the receipt's detail reads `queued: <agent> is on <host>, which is unreachable; it rings when <host> is back`.
- **Profiles**: a message reaches only sessions, members and seeds of the sender's profile. A session or member in another profile answers as if it did not exist: `no session or crew member matches`. A seed of another profile is refused by name.
- **Performance and idle cost**: limits a sender can hit, each named in its refusal: 32 KiB per message, the same text to one address once per 10 seconds, 8 messages from one sender to one address per 30 seconds, and 50 unread peer messages per address. `attn agent inbox` returns 20 items by default and 50 with `--limit`; the rest wait for the next read.
- **Harnesses**: every harness gets the same rings and reads with the same verbs; plugin harnesses get them through their driver. A shell never gets a ring: the receipt says it waits for `attn agent inbox` there.
- **Compatibility across versions**: items written by an earlier version stay readable; the store's inbox and mailbox migrations carry them forward.
- **Flow**: with queue flow and quiet notifications on, a run a ring started opens no turn when it stops, unless the agent needs approval or attn cannot tell its state ([queue-flow.md](queue-flow.md)). With queue flow off, quiet notifications change nothing.

## Tests

- Wire: `internal/daemon/agent_msg_wire_test.go`, `internal/daemon/agent_mailbox_wire_test.go`, `internal/daemon/inbox_attempts_wire_test.go`, `internal/daemon/garden_nudges_wire_test.go`, `internal/daemon/garden_bells_wire_test.go`, `internal/daemon/automation_bells_wire_test.go`, `internal/daemon/chief_nudge_quiet_wire_test.go`, `internal/daemon/plugin_inbox_wire_test.go`.
- Stack: `cmd/attn/agent_stack_test.go`, `cmd/attn/mailbox_restart_stack_test.go`, `internal/daemon/conversation_drop_delivery_stack_test.go`.
- Kernel: `internal/store/agent_mailbox_test.go`.

## Gotchas

- Text typed through `wsctl input` counts as the user typing: attn types nothing into that terminal for 30 seconds after it. Type into the sender, not the recipient, and wait 30 seconds after any input to the recipient before expecting its ring.
- The mock reads its inbox only because its fixture matches `unread items` in the 📬 line. A fixture without that turn leaves the item unread, and the next ring comes 5 minutes later.
- A ring lands only at a safe prompt. An agent that has not settled after launch gets `queued (target is not taking input right now ...)`; the ring follows when it settles, at no cost in attempts.
- After 3 unanswered rings attn stops ringing for an item and only the daemon log says so. A new item brings fresh attempts and its ring covers the old ones.
- `attn agent inbox` never returns answers or quick captures: a ring types them. Reading an empty inbox after a ring is not a lost item.
- Working but wrong: a receipt repeats its status, as in `queued: queued (seed has no reachable tender; waits for its next tender)` and `notified: notified inbox-b`. A `notified` receipt says the ring was placed, not that the agent read it; the proof is the 📬 line in that agent's terminal and `msg-status` reading `read`.
