# Inbox

## What the inbox is


The inbox is the system attn uses to keeo track and notify agents about messages from other agents.

The inbox is how attn puts something in front of an agent. A sender saves an
item for an address. attn then decides when to ring the agent, when to wake an
asleep crew member first, and when to stop. The agent reads its items with
`attn agent inbox`.

Items are durable. They survive app, daemon and machine restarts. An item stays
unread until an agent reads it, or until attn withdraws it (see
[Seed updates](#seed-updates) and [Coalescing](#coalescing)). Time and
unanswered rings never remove an item.

## Who an item is for

An address names who an item is for. A session reads it, or a mailbox that the
session currently holds.

| Address | Who reads it | When nobody holds it |
|---|---|---|
| session | that session | The item waits. attn never resumes a closed session to deliver it. |
| crew member | the member's current session | attn wakes the member (see [Wakes](#wakes)). |
| chief | the profile's current chief session | The item waits for the next chief. attn never wakes a chief. |
| seed | the seed's current tender: a session, or a crew member | The item waits for the next tender. A member tender is woken like a member address. |

An item follows its mailbox. When a member's session ends, their next session
gets the unread items. When a seed gets a new tender, the new tender gets the
seed's unread items.

The sender picks the address:

- `attn agent msg <member>` addresses the member.
- `attn agent msg <session>` addresses the member or the chief when that
  session holds one of those mailboxes. So the message follows the role into
  the next session. Otherwise it addresses the session.
- `attn agent msg --seed <id>` addresses the seed.
- A seed update addresses a crew member tender as the member. It addresses a
  session tender and every watcher as their session.
- Crew sleep and auto-sleep requests address the session. They are about that
  one session, and a wake to deliver them makes no sense.

A session reads all its addresses together: its own, its member's, the chief's
when it is the chief, and those of the seeds it tends.

## What puts items in

There are five items: peer messages, seed updates, user messages, PR watch
updates and notices.

### Peer messages

An agent sends a peer message with `attn agent msg`. attn refuses the message,
with the reason, when:

- it is empty, or longer than 32 KiB;
- the sender sent the same text to the same address in the last 10 seconds;
- the sender sent 8 messages to the same address in the last 30 seconds;
- the address has 50 unread peer messages;
- the address is the sender's own session.

### Seed updates

Some Garden events put a seed update in the inbox of the seed's tender and of
every session that watches the seed or one of its parents. The session that
caused the event gets no update.

| Event | Seed update |
|---|---|
| tend, park, harvest, wither, replant | yes, unless the move asks to stay quiet (automation acceptance does) |
| note | only with `--ring`, or when the note is the user's answer to an ask |
| harvest-when set or cleared | yes |
| unblocked: the last blocker closed | yes |
| automation work ready | yes |
| plant, body edit, artifact change, edge link or unlink, resume identity | no |

A seed update says which seed changed. The agent reads the seed for what
changed.

`attn seed show` and `attn seed notes` mark the reader's unread update for that
seed as read. When a recipient stops being the tender or a watcher, attn
withdraws their unread update for that seed. A new tender does not get updates
from before they tended the seed.

### User messages

A user message is the user's answer to an ask that has no seed. It goes to the
address the ask came from. An answer to an ask with a seed is a note on that
seed instead (see [Seed updates](#seed-updates)).

### PR watch updates

A PR watch update tells the watching agent what happened on a pull request:
ready, checks failed, changes requested, new review feedback, or a reopened
thread. See [Coalescing](#coalescing) for which updates replace older ones.

### Notices

Notices are attn's own messages to an agent:

- a new selection in the chief's Notebook inbox;
- a seed sent to the chief;
- crew sleep and auto-sleep requests.

## Coalescing

An item can have a key. A new item with the same address, kind and key
replaces the unread one.

- Seed updates use the seed as the key. An address has at most one unread
  update per seed, however many events happen.
- A PR watch status update (ready, checks failed, changes requested) replaces
  the older unread status update for the same pull request. New
  review feedback and reopened threads are separate items.
- An auto-sleep request replaces the older unread auto-sleep request.

Rings coalesce too. One ring covers every unread item at an address; see
[Rings](#rings).

## Rings

A ring is one prompt: "📬 You have unread items in your attn inbox. Run attn
agent inbox to read them." One ring covers every unread item at the address.
The item text stays in the inbox.

- attn rings only when the agent is at a safe prompt. While the agent works,
  needs approval, or the user types a draft, the ring waits. Waiting costs no
  attempt.
- A shell never gets a ring.
- After a ring, the address has an outstanding ring until the agent reads.
  New items do not ring while it is outstanding. The next read covers them.
- After 5 minutes with unread items, attn rings again.
- Each ring is one attempt for every unread item it covers. An item gets at
  most 3 attempts. Then attn stops ringing for it. The item stays unread, and
  the daemon log records the stop.
- A new item has 3 fresh attempts. Its ring also covers the older items.

## Wakes

When a crew member's address has unread items and the member is asleep, attn
wakes the member before it rings.

- The wake is one attempt for every unread item. The new session gets the ring
  when it first reaches a safe prompt. That ring is part of the same attempt.
  Items that arrive during the wake share it.
- A member has at most one inbox wake at a time.
- Every inbox wake counts against the member's wake limit, items from the user
  included. The limit is 8 wakes per 12 hours, set by `crew.wake_limit` and
  `crew.wake_limit_window_seconds`. A wake from the sidebar or `attn crew wake`
  does not count.
- A wake that the limit refuses still uses an attempt. attn tries again after 5
  minutes. After 3 attempts the items wait for a read or a new item.
- A member that goes back to sleep with unread items is woken again, 5 minutes
  after the last attempt.
- attn never wakes the chief.
- A manual wake does not ring for items that have no attempts left. The agent
  still finds them with `attn agent inbox`.

## Reading and receipts

`attn agent inbox` returns the unread items of every address the session holds,
oldest first. It returns 20 items by default; `--limit` allows up to 50. It
marks them read, records which session read them, and ends the outstanding
ring. It says how many unread items remain. `attn agent inbox <message-id>`
reads one peer message again.

`attn agent msg` replies at once with what happens next: the agent was rung,
the message waits for a safe prompt or for an outstanding ring to be read, a
member is being woken, the message waits for a tender or a chief, or the wake
limit is used up (with the setting to change). `attn agent msg-status <id>`
shows whether a message is queued, notified or read.

## Limits

| Limit | Value | Where | Receipt |
|---|---|---|---|
| Time between attempts | 5 minutes | `inbox.AttemptDelay` | Healthy agents read within 60 s of a ring at p99.5. |
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
restart interrupts still counts as an attempt.

## Scenarios

**A burst to an asleep member.** Alder tends four seeds and is asleep. In two
minutes, 15 events happen on those seeds. The first update wakes Alder: one
attempt, one wake against the limit. The other updates coalesce into one item
per seed and share the wake. Alder's new session gets one ring and finds four
items. If Alder does not read them, the next ring comes 5 minutes later.

**An agent ignores its ring.** A message arrives at 10:00. attn rings at 10:00,
10:05 and 10:10, then stops. A new message at 10:40 rings once and covers both.

**A busy agent.** An agent works for an hour and gets 30 messages. Nothing
rings during the work. When the agent reaches a safe prompt, one ring covers
all 30, and `attn agent inbox` returns the oldest 20.

**The tender changes.** A seed has an unread seed update for its tender, and
then another session tends it. attn withdraws the old tender's update, unless
the old tender also watches the seed. A message sent with `--seed` goes to the
new tender.

**The wake limit is used up.** A message for an asleep member arrives after the
member's 8th wake in 12 hours. The sender's receipt names `crew.wake_limit`.
attn tries at 0, 5 and 10 minutes, then stops. The message waits for the
member's next session.
