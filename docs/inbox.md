# Inbox

## What the inbox is

The inbox is how attn puts something in front of an agent: messages from other
agents, seed updates, the user's answers and Quick Captures, and attn's own
notices. A sender saves an item for an address. attn then decides when to ring
the agent, when to wake an asleep crew member first, and when to stop. A ring
types what the user wrote into the conversation as the user's message; the
agent reads everything else with `attn agent inbox`.

Items are durable. They survive app, daemon and machine restarts. An item stays
unread until an agent reads it or a ring types it, or until attn withdraws or
replaces it. The
sections below say when attn does that. Time and unanswered rings never remove
an item.

## Who an item is for

An address names who an item is for: a session, or a mailbox that whichever
session holds it reads.

| Address | Who reads it | When nobody holds it |
|---|---|---|
| session | that session | The item waits. attn never resumes a closed session to deliver it. Seed updates are the exception (see [Seed updates](#seed-updates)). |
| crew member | the member's current session | attn wakes the member (see [Wakes](#wakes)). |
| chief | the profile's current chief session | The item waits for the next chief. attn never wakes a chief. |
| seed | the seed's current tender: a session, or a crew member | The item waits for the next tender. A member tender is woken like a member address. |

An item follows its mailbox. When a member's session ends, their next session
gets the unread items. When a seed gets a new tender, the new tender gets the
unread items addressed to the seed. Seed updates are addressed to the tender
and the watchers themselves, so they do not move to a new tender.

The sender picks the address:

- `attn agent msg <member>` addresses the member.
- `attn agent msg <session>` addresses the member or the chief when that
  session holds one of those mailboxes. So the message follows the role into
  the next session. Otherwise it addresses the session.
- `attn agent msg <seed-id>` addresses the seed.
- A seed update addresses a crew member tender as the member. It addresses a
  session tender and every watcher as their session.
- Crew sleep and auto-sleep requests address the session. They are about that
  one session, and a wake to deliver them makes no sense.

A session reads all its addresses together: its own, its member's, the chief's
when it is the chief, and those of the seeds it tends.

## What puts items in

There are five kinds of item: peer messages, seed updates, user messages, PR
watch updates and notices.

### Peer messages

An agent sends a peer message with `attn agent msg`. attn refuses the message,
with the reason, when:

- it is empty, or longer than 32 KiB;
- the sender sent the same text to the same address in the last 10 seconds;
- the sender sent 8 messages to the same address in the last 30 seconds;
- the address has 50 unread peer messages;
- the message would reach the sender: their own session, their member, the
  chief when they are the chief, or a seed they tend.

### Seed updates

Some Garden events put a seed update in the inbox of the seed's tender and of
every session that watches the seed or a seed it is part of. Two sessions get
no update, at any of their addresses: the session that caused the event, and
the session that attn told about it directly, such as the new tender of a
delegation, a handover or a seed sent to the chief.

| Event | Seed update |
|---|---|
| tend, park, harvest, wither, replant | yes, unless the move asks to stay quiet (automation acceptance does) |
| note | only with `--ring`, or when the note is an automation run's outcome or a delegate that exited before its first turn |
| check move (add, edit, remove, restore, report, confirm, reject, uncheck, reopen) | only with `--ring` |
| harvest-when set or cleared | yes |
| unblocked: the last blocker closed | yes |
| automation work ready | yes |
| plant, body edit, artifact change, edge link or unlink, resume identity | no |

A seed update names the seed and the kind of event. For a check move it names
the move and who made it instead: "s-x moved: check C4 rejected by c9b177e1".
The agent reads the seed for the details.

A ringing note or check move replies with who gets its seed update: the tender
and the watchers, without the session that caused it. With nobody left, the
reply says so, so a ring on a closed seed with no watcher is not silent. A
recipient who already has an unread update on that seed gets the move merged
into it, and no new ring (see [Coalescing](#coalescing)). The reply names the
audience when the move commits; the update goes to the audience at delivery,
so the two differ only when a tender or watch changes in between.

`attn seed show` and `attn seed notes` mark the reader's unread update for that
seed as read. attn withdraws a recipient's unread update for a seed when they
stop being its tender or a watcher, or when the session it is addressed to
closes. A new tender does not get updates from before they tended the seed.

### User messages

A user message is text the user wrote or picked in attn: an answer to an ask,
or a Quick Capture. It stands for the user typing into the agent, so a ring
types it into the agent's conversation as the user's message (see
[Rings](#rings)), and harness auto-mode reviewers count an approval in it as
the user's. It does not wait for the agent to finish its turn, for the 5
minutes between rings or for a crew member's wake limit, and `attn agent
inbox` never returns it. Each one is its own item.

- An answer goes to the ask's address: the asking session or its mailbox, or
  the seed when the ask is about one, so it reaches whoever tends the seed by
  then. An answer to an ask about a seed is also a note on the seed, and that
  note does not ring.
- A Quick Capture goes to the chief or the crew member the user picked.

### PR watch updates

A PR watch update tells the watching agent what happened on a pull request:
ready, checks failed, changes requested, merged, closed, new review feedback, a
reopened thread, or that monitoring is delayed. attn withdraws the pull
request's unread updates when the watch stops. It withdraws an unread status
update when the pull request leaves that status, and an unread delay notice
when monitoring recovers.

### Notices

Notices are attn's own messages to an agent:

- a new selection in the chief's Notebook inbox;
- a seed sent to the chief;
- crew sleep and auto-sleep requests.

## Coalescing

An item can have a key. What a second item with the same address and key does
depends on its kind.

Seed updates merge. They use the seed as the key, so an address has at most
one unread update per seed. A later event on that seed leaves the unread update
as it is: same attempts, no new ring. The update keeps the first event's kind,
or its check move, unless the later event is `unblocked`. So once a seed update
has used its 3 attempts, later events on that seed stay silent until the agent
reads it; the seed's log still has every one of them.

Other keyed items are replaced. The new item takes the old one's place and gets
fresh attempts.

- A PR watch status update (ready, checks failed, changes requested, merged or
  closed) replaces the older unread status update for the same pull request. A
  delay notice replaces the older unread delay notice. New review feedback and
  reopened threads are separate items.
- An auto-sleep request replaces the older unread auto-sleep request.

Rings coalesce too. One ring covers every unread item at an address; see
[Rings](#rings).

## Rings

A ring is one prompt that covers every unread item at the address. It types
the text of each unread user message, oldest first. When other unread items
remain, it ends with "📬 You have unread items in your attn inbox. Run attn
agent inbox to read them.", and their text stays in the inbox. A session that
holds several addresses with unread items gets one ring for each.

Once attn has placed the ring in the session, its user messages are read by
that session. attn never checks that the agent acted on them; a ring lost to
an open menu in the harness stays lost.

- attn types user messages while the agent works or is at a safe prompt, and
  ends a ring with 📬 only at a safe prompt. A ring placed while the agent
  works types its user messages alone; the 📬 for the other items waits for
  the prompt.
- attn goes by the agent's state and never reads its screen. Nothing is typed
  while the agent needs approval, launches or is in a state attn cannot tell,
  or within 30 seconds of the user's last keystroke in its terminal. Waiting
  costs no attempt.
- A shell never gets a ring.
- After a ring that ends with 📬, the address has an outstanding ring. It ends
  when the agent reads, or 5 minutes after the ring. New items do not ring while it lasts.
  They wait for the read or the next ring. A user message does not wait: it
  is typed right away, without the 📬 until the outstanding ring ends.
- After 5 minutes with unread items, attn rings again.
- Each ring that ends with 📬 is one attempt for every unread item it covers.
  An item gets at most 3 attempts. Then attn stops ringing for it. The item
  stays unread, and the daemon log records the stop. Typing a user message
  uses no attempt.
- A new item has 3 fresh attempts. Its ring also covers the older items.

## Notification runs

A notification run is a run that attn's own input started: a ring, or a crew
member's heartbeat. If the user types into the agent during the run, the run
becomes the user's.

With quiet notifications on (`queue_quiet_notifications`) and the queue flow
on, a notification run does not bring the agent to the user:

- When it stops, it opens no turn, unless the agent needs approval or attn
  cannot tell its state.
- When it starts, it settles a turn that opened on its own. The agent made
  that change, so the app leaves the user where they are.
- During it, `attn agent inbox` ends with a note that says so and points to
  `attn turn open`.

An agent that needs the user opens its own turn with `attn turn open`. Called
while the agent works, it opens the turn when the agent stops; while the user
has the agent snoozed, once the snooze ends and the agent has stopped. The
request survives a daemon restart. Notification
runs do not settle that turn: the user does, or the agent with
`attn turn settle`, which refuses a turn that opened on its own. With the queue
flow off, both commands change nothing.

## Wakes

When a crew member's address has unread items and the member is asleep, attn
wakes the member before it rings.

- The wake is one attempt for every unread item. The new session gets the ring
  when it first reaches a safe prompt, and its user messages as soon as it
  works. That ring is part of the same attempt.
  Items that arrive during the wake share it.
- A member has at most one inbox wake at a time.
- An inbox wake counts against the member's wake limit: 8 wakes per 12 hours,
  set by `crew.wake_limit` and `crew.wake_limit_window_seconds`. A wake from
  the sidebar or `attn crew wake` does not count, and neither does a wake for
  an unread user message: like those, it is the user's.
- A wake that fails still uses an attempt, including one the wake limit
  refuses. attn tries again after 5 minutes. After 3 attempts the items wait
  for a read or a new item.
- A member that goes back to sleep with unread items that have attempts left
  is woken again, 5 minutes after the last attempt, or at once for a user
  message.
- attn never wakes the chief.
- A manual wake does not ring for items that have no attempts left. The agent
  still finds them with `attn agent inbox`. User messages are typed whatever
  their attempts.

## Reading and receipts

`attn agent inbox` returns the unread items of every address the session holds,
oldest first, except user messages, which only a ring delivers. It returns 20
items by default; `--limit` allows up to 50. It marks them read, records which
session read them, and says how many unread items remain. It ends the
outstanding ring of every address it read, even when `--limit` left items
unread. Those items ring again at the next safe prompt.

`attn agent inbox <message-id>` reads one peer message once it has been rung,
whether or not it was read before.

`attn agent msg` replies at once with what happens next: the agent was rung,
the message waits for a safe prompt or for an outstanding ring to be read, a
member is being woken, the message waits for a tender or a chief, or the wake
limit is used up (with the setting to change). `attn agent msg-status <id>`
shows the sender whether their message is queued, notified or read.

## Limits

| Limit | Value | Where | Receipt |
|---|---|---|---|
| Time between attempts (user messages do not wait) | 5 minutes | `inbox.AttemptDelay` | Healthy agents read within 60 s of a ring at p99.5. |
| Attempts per item | 3 | `inbox.MaxAttempts` | One ring was enough for over 99% of items; the repeats were harness outages. |
| Inbox wakes per member | 8 per 12 hours | `crew.wake_limit`, `crew.wake_limit_window_seconds` | |
| Peer message size | 32 KiB | `protocol.AgentMessageMaxChars` | |
| Same text to one address | once per 10 s | `agentMessageDedupeWindow` | |
| Peer messages from one sender to one address | 8 per 30 s | `agentMessageRateLimit` | |
| Unread peer messages per address | 50 | `agentMessageQueueCap` | |
| Items per `attn agent inbox` | 20 by default, 50 at most | `inbox.DefaultInboxLimit`, `inbox.MaxInboxLimit` | |

## Restarts

The database keeps items, attempts and outstanding rings. After a daemon
restart, attn resumes delivery for every address with unread items when
recovery finishes. A restart does not give items new attempts. A wake that a
restart interrupts still counts as an attempt. The woken session's ring then
waits 5 minutes and is a second attempt.

## Scenarios

**A burst to an asleep member.** Alder tends four seeds and is asleep. In two
minutes, 15 events happen on those seeds. The first update wakes Alder: one
attempt, one wake against the limit. Later events on the same seed merge into
its update. The first event on each other seed adds one update, and it shares
the wake. Alder's new session gets one ring and finds four updates. If Alder
does not read them, the next ring comes 5 minutes later.

**An agent ignores its ring.** A message arrives at 10:00. attn rings at 10:00,
10:05 and 10:10, then stops. A new message at 10:40 has 3 fresh attempts. attn
rings at 10:40, 10:45 and 10:50, and each ring covers both messages.

**A busy agent.** An agent works for an hour and gets 30 messages. Nothing
rings during the work. When the agent reaches a safe prompt, one ring covers
all 30, and `attn agent inbox` returns the oldest 20. The other 10 ring again
at the next safe prompt.

**An answer reaches a busy agent.** An agent asks the user, keeps working, and
gets two peer messages. The user answers. attn types the answer into the agent
at once, without the 📬. When the agent reaches a safe prompt, one ring covers
the two messages.

**The tender changes.** A seed has an unread update for its tender, and then
another session tends it. attn withdraws the old tender's update, unless the
old tender also watches the seed. The new tender gets no update for the earlier
event. A message sent to the seed's id goes to the new tender.

**The wake limit is used up.** A message for an asleep member arrives after the
member's 8th wake in 12 hours. The sender's receipt names `crew.wake_limit`.
attn tries at 0, 5 and 10 minutes, then stops. The message waits for the
member's next session.
