# Inbox

## What the inbox is

The inbox is how attn delivers information to an agent: messages from other
agents, seed updates, the user's answers and Quick Captures, and attn's own
notices. A sender saves an item for an address. attn then decides when to ring
the agent, when to wake an asleep crew member or resume a standby seed's closed
tender first, and when to stop. In a
ring, attn types what the user wrote into the conversation as the user's
message. The agent reads everything else with `attn agent inbox`.

Items are durable. They survive app, daemon and machine restarts. An item stays
unread until an agent reads it, a ring types it, or attn withdraws or replaces
it. The sections below say when attn withdraws or replaces an item. Time and
unanswered rings never remove an item.

## Who an item is for

An address names who an item is for: a session, or a mailbox that whichever
session holds it reads.

| Address | Who reads it | When nobody holds it |
|---|---|---|
| session | that session | The item waits, and attn never resumes a closed session to deliver an item. A seed update is the exception: attn withdraws it when the session closes (see [Seed updates](#seed-updates)). |
| crew member | the member's current session | attn wakes the member (see [Wakes](#wakes)). |
| chief | the profile's current chief session | The item waits for the next chief. attn never wakes a chief. With crew v2 on, the Chief is a crew member and new items for it use the Chief's member address instead. |
| seed | the seed's current tender: a session, or a crew member | The item waits for the next tender. A member tender is woken like a member address. A closed session tender is resumed only when the seed is on standby (see [Wakes](#wakes)). |

An item follows its mailbox. When a member's session ends, their next session
gets the unread items. When a seed gets a new tender, the new tender gets the
unread items addressed to the seed. attn addresses a seed update to the tender
and to each watcher directly, so a seed update does not move to a new tender.

The sender picks the address:

- `attn agent msg <member>` addresses the member.
- `attn agent msg <session>` addresses the member or the chief when that
  session holds one of those mailboxes, so the message follows the role into
  the next session. Otherwise the command addresses the session.
- `attn agent msg <seed-id>` addresses the seed.
- A seed update addresses a crew member tender as the member. It addresses a
  session tender and every watcher as their session.
- Crew sleep and auto-sleep requests address the session, because each request
  concerns that one session. attn never wakes a member to deliver a request.

A session reads all its addresses together: its own, its member's, the chief's
when it is the chief, and those of the seeds it tends.

## What puts items in

There are five kinds of item: peer messages, seed updates, user input, PR
watch updates and notices.

### Peer messages

An agent sends a peer message with `attn agent msg`. The message records its
sender: the crew member when a member's session sends it, otherwise the
session. The recipient's reply line names the member, so a reply reaches the
member's current session or wakes the member, even after the session that
sent it has ended. A session sender is named by its short id.

attn refuses a peer message, and gives the reason, when:

- the message is empty or longer than 32 KiB
- the sender sent the same text to the same address in the last 10 seconds
- the sender sent 8 messages to the same address in the last 30 seconds
- the address has 50 unread peer messages
- the message would reach the sender: the sender's own session, the sender's
  member, the chief when the sender is the chief, or a seed the sender tends

### Seed updates

Some Garden events put a seed update in the inbox of the seed's tender, the
tenders of every plot above it, and every session that watches the seed or a
seed it is part of. A plot's tender needs no watch, because the claim covers
the tender until the claim ends. Two sessions get no update, at any of their
addresses. One is the session that caused the event. The other is a session
that attn told about the event directly: the new tender of a delegation, the
new tender of a handover, or the chief that received the seed.

| Event | Seed update |
|---|---|
| tend, park, harvest, wither, replant | yes, unless the caller asks for the move to stay quiet (automation acceptance does) |
| note | only with `--ring`, or when the note is an automation run's outcome or a delegate that exited before its first turn, or when a plot's last open child closes |
| check move (add, edit, remove, restore, report, confirm, reject, uncheck, reopen) | only with `--ring` |
| harvest-when set or cleared | yes |
| unblocked: the last blocker closed | yes |
| automation work ready | yes |
| plant, body edit, artifact change, edge link or unlink | no |

A seed update contains the seed, the kind of event, and the command that shows
what changed: "s-x moved: parked. Read what changed with `attn seed show s-x
--since 2711491`". The number is the bookmark just before the event, so the
command returns the event's log entries and those of every later event on the
seed or the seeds below it. For a check move, the update gives the move and who
made it in place of the kind: "s-x moved: check C4 rejected by c9b177e1. …".
An update stored before attn added the bookmark ends "Read it with `attn seed
show s-x`" instead.

The reply to a lifecycle move, a ringing note or a check move lists who gets
the seed update: the current and covering tenders and the watchers, without the
session that caused the event. With nobody left, the reply says so, so a ring
on a closed seed with no watcher is not silent. A recipient who already has an
unread update on that seed gets the move merged into that update, and no new
ring (see [Coalescing](#coalescing)). The reply lists the audience as it stands
when the move commits. attn sends the update to the audience as it stands at
delivery, so the delivered audience differs from the listed one only when a
tender or watch changes in between.

`attn seed show` and `attn seed notes` mark the reader's unread update for that
seed as read. attn withdraws a recipient's unread update for a seed when they
stop being a current or covering tender or a watcher, or when the addressed
session closes. A new tender does not get updates from before the tender began
tending the seed.

### User input

User input is text the user wrote or picked in attn: an answer to an ask,
a Quick Capture, or a change to a seed's authorization. It stands for the user
typing into the agent, so attn types it into the agent's conversation as the
user's message in a ring (see
[Rings](#rings)), and harness auto-mode reviewers count an approval in the
message as the user's. User input does not wait for the agent to finish its
turn, for the 5 minutes between rings, or for a crew member's wake limit.
`attn agent inbox` never returns user input. Each piece of user input is its own
item.

- An answer goes to the ask's address: the asking session or its mailbox, or
  the seed when the ask is about one, so it reaches whoever tends the seed by
  then. An answer to an ask about a seed is also a note on the seed, and that
  note does not ring.
- A Quick Capture goes to the chief or the crew member the user picked, or to
  the agent session that a paired sender names with `attn send`.
- A change to a seed's authorization goes to the seed's tender, addressed as a
  seed update is: the session, or the crew member when a member tends it. attn
  writes the item only when someone other than the tender changes the words
  while the claim holds, in the same write as the words and their log entry.
  A successor after a handover reads the current words in its opening instead.
  The seed page in the app says whether the change waits for the tender or was
  typed, and changes as soon as a ring types it or the Claude mod gives the ring
  back (see [With the Claude mod](#with-the-claude-mod)).
  attn does not check where the words came from. The agent that set them
  answers for them.

### PR watch updates

A PR watch update tells the watching agent what happened on a pull request:
ready, checks failed, changes requested, merged, closed, new review feedback, a
reopened thread, or that monitoring is delayed. Feedback that attn's GitHub
account for the host wrote, and reopened threads that account started on its own
pull request, make no update unless the profile sets
`pull_requests.own_comments` to `ring`. attn withdraws the pull request's unread
updates when the watch stops. attn withdraws an unread status update when the
pull request leaves that status, and an unread delay update when monitoring
recovers.

### Notices

Notices are attn's own messages to an agent:

- a seed sent to the chief
- a reminder that came due on a seed (`attn seed remind`), addressed to the
  seed
- crew sleep and auto-sleep requests

## Coalescing

An item can have a key. What happens to a second item with the same address and
key depends on the kind of item.

Seed updates merge. The key is the seed, so an address has at most one unread
update per seed. A later event on that seed adds no new update, no new attempt,
and no new ring. The merged update keeps the first event's kind and bookmark,
so the read command returns the later events too. When the first event was a
check move, the update keeps the move and who made it. An `unblocked` event is
the exception: it replaces the kind and drops a check move's description. Once a seed update has
used its 3 attempts, later events on that seed stay silent until the agent
reads the update. The seed's log still has every event.

attn replaces other keyed items. The new item takes the place of the old item
and gets fresh attempts.

- A PR watch status update (ready, checks failed, changes requested, merged or
  closed) replaces the older unread status update for the same pull request. A
  delay update replaces the older unread delay update. New review feedback and
  reopened threads are separate items.
- A change to a seed's authorization replaces the older unread change for the
  same seed, so the tender gets only the newest words. The newer item still
  names the words the tender last received as the ones it replaces. When the
  tender changes the words itself, attn withdraws the unread change, because
  the tender already knows its own words.
- An auto-sleep request replaces the older unread auto-sleep request.

Rings coalesce too. One ring covers every unread item at an address. See
[Rings](#rings).

## Rings

A ring is one prompt that covers every unread item at the address. attn types
the text of each unread piece of user input in the ring, oldest first. When other
unread items remain, attn ends the ring with the line "📬 You have unread items
in your attn inbox. Run attn agent inbox to read them." The text of those items
stays in the inbox. A session that holds several addresses with unread items
gets one ring for each address.

Once attn has placed a ring in the session, attn records the session as the
reader of the ring's user input. attn never checks that the agent acted on
that input. A ring lost to an open menu in the harness stays lost.

- attn types user input while the agent works or is at a safe prompt, and
  adds the 📬 line only at a safe prompt. A ring placed while the agent
  works types its user input alone. The 📬 line for the other items waits for
  the prompt.
- A safe prompt is an agent that is idle or waiting, an agent parked on its
  background work, or an agent in a state attn cannot tell. A parked agent shows
  as working but has stopped at its prompt. An agent in a state attn cannot tell
  gets rings so that it does not wait forever, even though a ring can land on an
  open menu.
- attn decides from the agent's state and never reads the agent's screen. attn
  types nothing while the agent needs approval or is launching. attn also types
  nothing within 30 seconds of the user's last keystroke in the agent's
  terminal. Waiting costs no attempt.
- A shell never gets a ring.
- When a ring includes the 📬 line, the address has an outstanding ring until
  the agent reads the inbox or 5 minutes pass. While the address has an
  outstanding ring, new items wait for the read or for the next ring. attn types
  user input right away, without the 📬 line.
- After 5 minutes with unread items, attn rings again.
- Each ring that includes the 📬 line is one attempt for every unread item the
  ring covers. An item gets at most 3 attempts. Then attn stops ringing for that
  item. The item stays unread, and the daemon log records the stop. Typing
  user input uses no attempt.
- A new item has 3 fresh attempts. The ring for a new item also covers the
  older items, but the ring counts as an attempt only for items that have
  attempts left.

### With the Claude mod

With the experimental `claude_mod_enabled` setting on, a Claude session's mod
takes each ring instead of the terminal while the mod is connected. attn types as
usual while the mod is not connected, such as while Claude starts or waits on its
folder trust question, and on an outpost, where the inbox lives on another
daemon. The exception is a Claude session that attn resumed for a standby seed's
mail: attn waits for its mod and never types (see
[A seed on standby](#a-seed-on-standby)). With the mod connected:

- After the user stops the session with Esc while Claude works, the mod refuses
  every ring. Its items stay unread, the refusal costs no attempt, and attn rings
  again at the session's next state change after the user's next prompt.
- A ring the mod holds for the running turn counts as placed. When the mod gives
  it back instead of placing it, at Esc, on a reload, or when the mod stops
  polling, attn puts the ring's items back to unread, takes back the ring's
  attempt and outstanding ring, and rings again. The app shows a Quick Capture
  or a seed authorization change from that ring as read or typed while the mod
  holds it, then as waiting again once the mod gives it back, until the next
  ring places it. A daemon restart while the mod holds a ring loses that ring's
  user input, because attn keeps what the mod holds in memory.
- While a running turn waits on a permission question, the mod takes a ring it
  would take during that turn. It holds the ring until the question closes,
  because the mod's delivery cannot answer the question. Refusing the question,
  with "No" or with Esc, does not stop the session: Claude reports both the same
  way, and it takes the next prompt. A held ring then starts a new turn, and later
  rings go out as usual. A question outside a turn still holds every ring back.

## Notification runs

A notification run is a run that attn's own input started: a ring, or a crew
member's heartbeat. If the user types into the agent during the run, the run
becomes the user's.

With quiet notifications on (`queue_quiet_notifications`) and the queue flow
on, attn does not bring the agent to the user for a notification run:

- When the run stops, attn opens no turn, unless the agent needs approval or
  attn cannot tell the agent's state.
- When the run starts, attn settles a turn that opened when the agent stopped,
  not with `attn turn open`. The agent started working because of attn's own
  input, so the app leaves the user where they are.
- During the run, `attn agent inbox` ends with a note that says the run is
  quiet and points to `attn turn open`.

An agent that needs the user opens its own turn with `attn turn open`. If the
agent runs the command while it works, the turn opens when the agent stops. If
the user has snoozed the agent, the turn opens once the snooze ends and the
agent has stopped. The request survives a daemon restart. Notification runs do
not settle that turn. The user settles the turn, or the agent runs
`attn turn settle`. That command refuses a turn that opened when the agent
stopped, not with `attn turn open`. With the queue flow off, both commands
change nothing.

## Wakes

### A crew member

When a crew member's address has unread items and the member is asleep, attn
wakes the member before it rings. When a restart interrupted the member's latest
session, attn resumes that session and rings it instead of starting a new one.

- The wake is one attempt for every unread item. The new session gets the ring
  when the session first reaches a safe prompt, and gets the user input as
  soon as it works. That ring is part of the same attempt. Items that arrive
  during the wake share the attempt.
- The woken session's first prompt names what woke it: each unread seed
  update and peer message with attempts left, up to 8, then a count of the
  rest. A seed update reads as the inbox prints it, with its
  `attn seed show <id> --since <n>` command. A peer message names only its
  sender. A wake with neither, such as one for a notice, opens with the plain
  greeting. The prompt says the user did not open the
  session, and tells the member to run `attn agent inbox` first. A member with
  a charter then does what its charter says. A member without a charter, one
  with onboarding pending, and the Chief still greet the user after the cause.
  When unread user input wakes the member, the user is the cause, and the
  prompt is the plain greeting that every other wake gets.
- A member has at most one inbox wake at a time.
- An inbox wake counts against the member's wake limit: 8 wakes per 12 hours,
  set by `crew.wake_limit` and `crew.wake_limit_window_seconds`. A wake from
  the sidebar, a wake from `attn crew wake` and a wake for an unread user
  message do not count, because the user causes each of them. A wake for seed
  mail counts against the seed's wake limit instead (see
  [A seed on standby](#a-seed-on-standby)).
- A wake that fails still uses an attempt, including one the wake limit
  refuses. attn tries again after 5 minutes. After 3 attempts the items wait
  for a read or a new item.
- attn never wakes a retired member, and attn refuses `attn agent msg` to a
  retired member, by name or by the member's session. Items that were unread at
  retirement keep their attempts and wait. When someone restores the member,
  attn delivers those items, and the delivery can wake the member.
- attn wakes a member again when the member goes back to sleep with unread
  items that have attempts left. The wake comes 5 minutes after the last
  attempt, or at once for user input.
- attn never wakes a chief address. With crew v2 on, attn wakes the Chief
  member like any member.
- A manual wake does not ring for items that have no attempts left. The agent
  still finds those items with `attn agent inbox`. attn types user input
  regardless of their attempts.

### A seed on standby

A seed on standby carries a note from its tender, set with
`attn seed standby <seed> -m "<what to do next>"`. Seed mail is an item
addressed to a seed: a PR watch update, a reminder, a peer message sent to the
seed's id, and the answer to an ask about the seed.

When seed mail is unread and the seed's tender is a closed session, attn
resumes that session as itself, with the same session id and conversation. It
resumes the session only when all of these hold:

- The seed is on standby. A closed session whose seed is not on standby stays
  closed, and the mail waits for the next tender.
- The session is the one the seed names as its tender.
- `garden.tender_wake` allows the item. `any`, the default, allows all seed
  mail. `watches` allows PR watch updates and reminders only. `off` allows
  nothing, and the mail waits.
- The seed has had fewer than 48 tender wakes in the last 24 hours.

Who closed the session does not matter. The resume needs no connected app, and
the session opens on its last desktop. A crew member that tends the seed is
woken as a member under every value of `garden.tender_wake`.

The woken session gets its mail only through a path that knows when the agent
reads input. For Codex, attn rings right after the resume. For Claude, attn
waits for the reopened session's first Claude mod poll and delivers through the
mod. attn types nothing into a Claude terminal. A Claude session cannot be woken
while `claude_mod_enabled` is off, or when its mod does not poll within 30
seconds of the resume. `attn seed standby` refuses to run in a Claude session
that runs without the mod and names `claude_mod_enabled`.

The first prompt carries the mail and the standby note. It says the user may
not be watching, and names the ways to end the turn: put the seed on standby
again, harvest it, ask the user with `attn ask`, or report. A woken session that
stops owes the user a turn, as any resumed session does.

Standby ends when attn delivers seed mail to the tender, whether the session
was open or woken. It also ends when the seed is parked, harvested or withered,
and when the seed gets a new tender. attn delivers seed mail again when a
session closes and when the daemon restarts, so a seed on standby with unread
mail wakes its tender right after either.

`attn seed standby --close` and `attn agent close --self` refuse while a seed
the session tends is on standby and has unread seed mail. The refusal names
`attn agent inbox`.

With `--compact`, the close waits for the turn to end and then places the
compact in the session's input lane, through the Claude mod or the shared Codex
app server. A ring placed before the compact makes the compact wait for the next
idle. Once the compact is placed, the lane holds every later item until the
session has closed, and seed mail that arrives then wakes the closed tender as
above. A compact that meets a running turn opens the lane again and retries at
the next idle. User input before the close cancels it and opens the lane again.

Every tender wake for seed mail, of a session or of a crew member, counts
against one limit for the seed: 48 wakes per 24 hours, held in memory. The count
restarts empty when the daemon restarts. These wakes do not count against
`crew.wake_limit`, so a member that tends several seeds is not capped across
them. The daemon log records each tender wake with the seed and its cause.

A wake that fails for a reason that does not fix itself writes a note on the
seed that names the reason and the item, and rings the seed's watchers and the
tenders of the plots above it. It also raises a critical notification with an
action that opens the seed. The reasons are a removed worktree, a missing
conversation, a conversation that would reopen in another profile, a Claude
session that cannot receive the mail through its mod, and the 48-wake limit. The
seed row carries the reason as `wake_blocked`. The item stays queued. attn does
not retry these wakes, except at the 48-wake limit: attn delivers the item when
the 24-hour window has room again. A failure that can fix itself leaves the item
queued, and attn tries again after 5 minutes. Each wake uses one of the item's 3
attempts.

## Reading and receipts

`attn agent inbox` returns the unread items of every address the session holds,
oldest first. It leaves out user input, which only a ring delivers. It
returns 20 items by default, and `--limit` allows up to 50. The command marks
the items read, records which session read them, and says how many unread items
remain. The command ends the outstanding ring of every address it read, even
when `--limit` left items unread. attn rings again for those items at the next
safe prompt.

`attn agent inbox <message-id>` reads one peer message once attn has rung it,
whether or not the session read the message before.

`attn agent msg` replies at once with what happens next. The reply says one of
these:

- attn rang the agent.
- The message waits for a safe prompt or for an outstanding ring to be read.
- attn is waking a member.
- The message waits for a tender or a chief.
- The wake limit is used up. The reply also names the setting to change.

`attn agent msg-status <id>` shows the sender whether the message is queued,
notified, or read.

## Limits

| Limit | Value | Where | Receipt |
|---|---|---|---|
| Time between attempts (user input does not wait) | 5 minutes | `inbox.AttemptDelay` | Healthy agents read within 60 s of a ring at p99.5. |
| Attempts per item | 3 | `inbox.MaxAttempts` | One ring was enough for over 99% of items. The repeats were harness outages. |
| Inbox wakes per member | 8 per 12 hours | `crew.wake_limit`, `crew.wake_limit_window_seconds` | |
| Tender wakes per seed, for seed mail | 48 per 24 hours | `tenderWakeLimit`, `tenderWakeWindow` | None yet. The daemon logs each tender wake with the seed and its cause. |
| Peer message size | 32 KiB | `protocol.AgentMessageMaxChars` | |
| Same text to one address | once per 10 s | `agentMessageDedupeWindow` | |
| Peer messages from one sender to one address | 8 per 30 s | `agentMessageRateLimit` | |
| Unread peer messages per address | 50 | `agentMessageQueueCap` | |
| Items per `attn agent inbox` | 20 by default, 50 at most | `inbox.DefaultInboxLimit`, `inbox.MaxInboxLimit` | |
| Remembered delivery IDs per session input lane | 65,536 | `sessionmonitor.MaxPlacedDeliveries` | Over 14 times the 4,390 user records in the busiest of 10 sampled inbox recipients' transcripts (2026-10-29). The sample counts whole conversations, including direct input and tool results, across up to 23 days. |

attn keeps the first delivery receipt of each successful delivery until the
input lane resets on an agent reload or daemon restart. A retry with the same ID
returns that delivery receipt. At the delivery-ID limit, new deliveries fail
before attn types them, and the error names the session, the limit and the
requested count. Existing IDs still return their delivery receipts.

## Restarts

The database keeps items, attempts and outstanding rings. After a daemon
restart, attn resumes delivery for every address with unread items when
recovery finishes, and the delivery can wake the tender of a seed on standby. A restart does not give items new attempts. A wake that a
restart interrupts still counts as an attempt. The woken session's ring then
waits 5 minutes and is a second attempt.

## Scenarios

**A burst to an asleep member.** Alder tends four seeds and is asleep. In two
minutes, 15 events happen on those seeds. The first update wakes Alder: one
attempt, one wake against the limit. Later events on the same seed merge into
that seed's update. The first event on each other seed adds one update, and
that update shares the wake. Alder's new session gets one ring and finds four
updates. If Alder does not read them, the next ring comes 5 minutes later.

**An agent ignores its ring.** A message arrives at 10:00. attn rings at 10:00,
10:05 and 10:10, then stops. A new message at 10:40 has 3 fresh attempts. attn
rings at 10:40, 10:45 and 10:50, and each ring covers both messages.

**A busy agent.** An agent works for an hour and gets 30 messages. Nothing
rings during the work. When the agent reaches a safe prompt, one ring covers
all 30, and `attn agent inbox` returns the oldest 20. attn rings again for the
other 10 at the next safe prompt.

**An answer reaches a busy agent.** An agent asks the user, keeps working, and
gets two peer messages. The user answers. attn types the answer into the agent
at once, without the 📬. When the agent reaches a safe prompt, one ring covers
the two messages.

**The tender changes.** A seed has an unread update for its tender, and then
another session tends it. attn withdraws the old tender's update, unless the
old tender also tends a plot above it or watches the seed. The new tender gets
no update for the earlier event. A message sent to the seed's id goes to the
new tender.

**A reminder on standby.** An agent runs
`attn seed remind s-x --in 20m -m "Check the rollout"`, then
`attn seed standby s-x -m "Read the rollout dashboard and report" --close`. The
session closes. When the reminder comes due, the seed is on standby and
`garden.tender_wake` is `any`, so attn resumes the closed session. For a Claude
session, attn waits for the mod's first poll. The session reads the reminder and
the note, and standby ends. The agent reports, or goes on standby again.

**The wake limit is used up.** A message for an asleep member arrives after the
member's 8th wake in 12 hours. The reply to the sender names `crew.wake_limit`.
attn tries at 0, 5 and 10 minutes, then stops. The message waits for the
member's next session.
