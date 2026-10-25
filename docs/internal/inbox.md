# Inbox

## What the inbox is

The inbox is how attn puts something in front of an agent: messages from other
agents, seed updates, answers to asks and attn's own notices. A sender saves an
item for an address. attn then decides when to ring the agent, when to wake an
asleep crew member first, and when to stop. The agent reads its items with
`attn agent inbox`.

Items are durable. They survive app, daemon and machine restarts. An item stays
unread until an agent reads it, or until attn withdraws or replaces it. The
sections below say when attn does that. Time and unanswered rings never remove
an item.

## Who an item is for

A party is a session or crew member. An actor is a party, the user or attn.
An address names who an item is for: a session, or a mailbox for a crew member
or seed tender. The tender is the party that claims the seed.

| Address | Who reads it | When nobody holds it |
|---|---|---|
| session | that session | The item waits. attn never resumes a closed session to deliver it. Session-addressed seed updates are withdrawn on close (see [Seed updates](#seed-updates)). |
| crew member | the member's current session | attn wakes the member (see [Wakes](#wakes)). |
| seed | the seed's current tender | The item waits for the next tender. A member tender is woken like a member address. |

The Chief has a crew member mailbox. Mail wakes them after a manual sleep.
They never auto-sleep. Until their harness is configured, mail waits; their
first wake also waits while the user is away.

An item follows its mailbox. When a member's session ends, their next session
gets the unread items. When a seed gets a new tender, the new tender gets the
unread items addressed to the seed. Seed updates are addressed to the tender
and the watchers themselves, so they do not move to a new tender.

The sender picks the address:

- `attn agent msg <member>` addresses the member.
- `attn agent msg <session>` addresses the member when that session is bound
  to one, so the message follows the member into the next session. Otherwise
  it addresses the session. An explicit `session:<id>` addresses that session.
- `attn agent msg chief` addresses the profile's Chief member, even after a
  rename.
- `attn agent msg <seed-id>` addresses the seed.
- A seed update addresses the tender and every watcher as their party. A
  member's claim and watches survive naps, `/clear` and sleep.
- Crew sleep and auto-sleep requests address the session. They are about that
  one session, and a wake to deliver them makes no sense.

A session reads all its addresses together: its own, its member's when bound
to one, and those of the seeds its party tends.

## What puts items in

The inbox carries peer messages, seed updates, quick captures, answers to
asks, PR watch updates and notices.

### Peer messages

An agent sends a peer message with `attn agent msg`. attn refuses the message,
with the reason, when:

- it is empty, or longer than 32 KiB;
- the sender sent the same text to the same address in the last 10 seconds;
- the sender sent 8 messages to the same address in the last 30 seconds;
- the address has 50 unread peer messages;
- the recipient member is retired;
- the message would reach the sender's current session, through any address.

### Seed updates

Some Garden events put a seed update in the inbox of the seed's tender and of
every party that watches the seed or a seed it is part of. attn excludes the
party that caused the event, and any party told about it directly, such
as the new tender of a delegation or handover. An event caused by the user or
attn excludes no party as its author.

| Event | Seed update |
|---|---|
| tend, park, harvest, wither, replant | yes, unless the move asks to stay quiet (automation acceptance does) |
| note | only with `--ring`, or when the note is the user's answer to an ask, an automation run's outcome, or a delegate that exited before its first turn |
| harvest-when set or cleared | yes |
| unblocked: the last blocker closed | yes |
| automation work ready | yes |
| plant, body edit, artifact change, edge link or unlink, resume identity | no |

A seed update names the seed and the kind of event. The agent reads the seed
for the details.

`attn seed show` and `attn seed notes` mark the reader's unread update for that
seed as read. attn withdraws a recipient's unread update for a seed when they
no longer have a claim or watch covering it. A session-addressed update is
also withdrawn when that session closes. A member-addressed update stays
through naps, `/clear` and sleep while the member still has that claim or
watch. A new tender does not get updates from before they tended the seed.

### Quick captures

A quick capture is a message from the user, with optional attachments, sent
to a crew member through the global shortcut. It stays in that member's
mailbox across sessions and uses the same rings and inbox wakes as other
items. `attn agent inbox` returns its text and attachments.

### Answers to asks

An answer item carries the user's answer to an ask that is not about a seed.
The ask names no seed, and the asking session has no stake. It goes to the address the
ask came from. The answer to an ask about a seed is a note on that seed, and
that note rings (see [Seed updates](#seed-updates)).

### PR watch updates

A PR watch update tells the watching agent what happened on a pull request:
ready, checks failed, changes requested, merged, closed, new review feedback, a
reopened thread, or that monitoring is delayed. attn withdraws the pull
request's unread updates when the watch stops. It withdraws an unread status
update when the pull request leaves that status, and an unread delay notice
when monitoring recovers.

### Notices

Notices are attn's own messages to an agent:

- a seed sent to the Chief;
- crew sleep and auto-sleep requests.

## Coalescing

An item can have a key. What a second item with the same address and key does
depends on its kind.

Seed updates merge. They use the seed as the key, so an address has at most
one unread update per seed. A later event on that seed leaves the unread update
as it is: same attempts, no new ring. The update keeps the first event's kind,
unless the later event is `unblocked`. So once a seed update has used its 3
attempts, later events on that seed stay silent until the agent reads it.

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

A ring is one prompt: "📬 You have unread items in your attn inbox. Run attn
agent inbox to read them." One ring covers every unread item at the address.
The item text stays in the inbox. A session that holds several addresses with
unread items gets one ring for each.

- attn rings only when the agent is at a safe prompt. While the agent works,
  needs approval, or the user types a draft, the ring waits. Waiting costs no
  attempt.
- A shell never gets a ring.
- After a ring, the address has an outstanding ring. It ends when the agent
  reads, or 5 minutes after the ring. New items do not ring while it lasts.
  They wait for the read or the next ring.
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
  included. The default limit is 8 wakes per 12 hours, set by `crew.wake_limit` and
  `crew.wake_limit_window_seconds`. A wake from the sidebar or `attn crew wake`
  does not count.
- A wake refused by the wake limit, or one whose agent fails to launch, still
  uses an attempt. attn tries again after 5 minutes. After 3 attempts the
  items wait for a read or a new item.
- A missing member home or charter prevents the wake without spending an
  attempt. attn retries while the items wait for those files to be restored.
- A member that goes back to sleep with unread items that have attempts left
  is woken again, 5 minutes after the last attempt.
- A manual wake does not ring for items that have no attempts left. The agent
  still finds them with `attn agent inbox`.

## Reading and receipts

`attn agent inbox` returns the unread items of every address the session holds,
oldest first. It returns 20 items by default; `--limit` allows up to 50. It
marks them read, records which session read them, and says how many unread
items remain. It ends the outstanding ring of every address the session holds,
even when `--limit` left items unread. Those items ring again at the next safe prompt.

`attn agent inbox <message-id>` reads one peer message once it has been rung,
whether or not it was read before.

`attn agent msg` replies at once with what happens next: the agent was rung,
the message waits for a safe prompt or for an outstanding ring to be read, a
member is being woken, the message waits for a tender or Chief configuration,
or the wake limit is used up (with the setting to change). `attn agent msg-status <id>`
shows the sender whether their message is queued, notified or read.

## Limits

| Limit | Value | Where | Receipt |
|---|---|---|---|
| Time between attempts | 5 minutes | `inbox.AttemptDelay` | Healthy agents read within 60 s of a ring at p99.5. |
| Attempts per item | 3 | `inbox.MaxAttempts` | One ring was enough for over 99% of items; the repeats were harness outages. |
| Inbox wakes per member | 8 per 12 hours by default | `crew.wake_limit`, `crew.wake_limit_window_seconds` | |
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

**The tender changes.** A seed has an unread update for its tender, and then
another party claims it. attn withdraws the old tender's update, unless the
old tender also watches the seed. A member's watch keeps that update through
naps, `/clear` and sleep. The new tender gets no update for the earlier event.
Unread messages addressed to the seed go to the new tender.

**The wake limit is used up.** A message for an asleep member arrives after the
member's 8th wake in 12 hours. The sender's receipt names `crew.wake_limit`.
attn tries at 0, 5 and 10 minutes, then stops. The message waits for the
member's next session.
