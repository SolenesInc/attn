# Inbox

## What the inbox is

The inbox is how attn delivers information to an agent: messages from other
agents, seed updates, the user's answers and Quick Captures, and attn's own
notices. A sender saves an item for an address. attn then decides when to ring
the agent, when to start a session for a crew member between sessions or resume a standby seed's closed
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
| crew member | the member's current session | attn starts a session for the member (see [Starts and resumes](#starts-and-resumes)). |
| seed | the seed's current tender: a session, or a crew member | The item waits for the next tender. A member tender gets a session like a member address. A closed session tender is resumed only when the seed is on standby (see [Starts and resumes](#starts-and-resumes)). |

An item follows its mailbox. When a member's session ends, their next session
gets the unread items. When a seed gets a new tender, the new tender gets the
unread items addressed to the seed. attn addresses a seed update to the tender
and to each watcher directly, so a seed update does not move to a new tender.

The sender picks the address:

- `attn agent msg <member>` addresses the member.
- `attn agent msg <session>` addresses the member when that session holds a
  member's mailbox, so the message follows the role into the next session.
  Otherwise the command addresses the session.
- `attn agent msg <seed-id>` addresses the seed.
- A seed update addresses a crew member tender as the member. It addresses a
  session tender and every watcher as their session.
- Crew close requests, from the user or when the user is away, address the session, because each request
  concerns that one session. attn never starts a session to deliver a request.

A session reads all its addresses together: its own, its member's, and those of
the seeds it tends.

## What puts items in

There are five kinds of item: peer messages, seed updates, user input, PR
watch updates and notices.

### Peer messages

An agent sends a peer message with `attn agent msg`. The message records its
sender: the crew member when a member's session sends it, otherwise the
session. The recipient's reply line names the member, so a reply reaches the
member's current session or starts one for the member, even after the session that
sent it has ended. A session sender is named by its short id.

attn refuses a peer message, and gives the reason, when:

- the message is empty or longer than 32 KiB
- the sender sent the same text to the same address in the last 10 seconds
- the sender sent 8 messages to the same address in the last 30 seconds
- the address has 50 unread peer messages
- the message would reach the sender: the sender's own session, the sender's
  member, or a seed the sender tends

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
turn, for the 5 minutes between rings, or for a crew member's mail-start limit.
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
- crew close requests, from the user or when the user is away

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
- A close request sent because the user is away replaces the older unread one.

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
  types nothing while the agent needs approval or is launching. A new or resumed
  agent whose harness has hooks is launching until the harness itself reports,
  because a harness that has only just started loses what is typed
  ([Claude Code](harnesses/claude.md)). A resumed or reloaded agent keeps the
  state it had, but attn types nothing until its harness sets its terminal title,
  sends a program status or submits a prompt, and rings as soon as it does. attn
  also types nothing within 30 seconds of the user's last keystroke in the agent's
  terminal. Waiting costs no attempt.
- A shell never gets a ring.
- When a ring includes the 📬 line, the address has an outstanding ring until
  the agent reads the inbox or 5 minutes pass. While the address has an
  outstanding ring, new items wait for the read or for the next ring. attn types
  user input right away, without the 📬 line.
- After 5 minutes with unread items, attn rings again. Seed mail that nobody
  holds does not wait (see [A seed on standby](#a-seed-on-standby)).
- Each ring that includes the 📬 line is one attempt for every unread item the
  ring covers. An item gets at most 3 attempts. Then attn stops ringing for that
  item. The item stays unread, and the daemon log records the stop. Typing
  user input uses no attempt.
- A new item has 3 fresh attempts. The ring for a new item also covers the
  older items, but the ring counts as an attempt only for items that have
  attempts left.

### With the Claude mod

A Claude session's mod takes each ring instead of the terminal while the mod is connected, on the home
and on an outpost. attn types as usual while the mod is not connected, such as
after Claude first reports and before the mod's first poll. The exception is a
Claude session that attn resumed for a standby seed's mail: attn waits for its
mod and never types (see [A seed on standby](#a-seed-on-standby)). With the mod
connected:

- After the user stops the session with Esc while Claude works, the mod refuses
  every ring. Its items stay unread, the refusal costs no attempt, and attn rings
  again at the session's next state change after the user's next prompt.
- A ring the mod holds for the running turn counts as placed. When the mod gives
  it back instead of placing it, at Esc, on a reload, or when the mod stops
  polling, attn puts the ring's items back to unread, takes back the ring's
  attempt and outstanding ring, and rings again. The app shows a Quick Capture
  or a seed authorization change from that ring as read or typed while the mod
  holds it, then as waiting again once the mod gives it back, until the next
  ring places it. A seed stays on standby while the mod holds its mail, and when
  the mod gives the mail back, even before the home hears that the mod took it.
- The inbox lives on the home, and the daemon that runs the session reports each
  placement and give-back to the home. A report made while the home is restarting
  or its link is down waits on that daemon and reaches the home when the link comes
  back. A report the home refuses, such as for a store error, is logged and dropped.
- The mod keeps each ring it holds with the ring's record, and each message it gives
  back until attn has settled the give-back at the home. A restarted daemon counts the
  mod of each running Claude as connected until the mod polls or 5 seconds
  pass, and a give-back counts as the mod connecting. So attn types nothing into
  that session while its mod redials. If the daemon dies after the mod takes a ring
  and before the home records it, the home rings again and the mod holds both rings.
  So a restart of the daemon that runs the session loses no ring while
  Claude and its mod keep running, and no user's words the mod still holds. attn loses a held ring, or a give-back still
  waiting, only when the daemon and the mod die together: the daemon restarts and
  Claude exits or the mod reloads before the mod has handed it back, as an upgrade
  that changes the mod does. The ring's user input is then lost and its attempt
  stays spent. A restart also loses the user's words the mod gave back that wait
  for a prompt, because the daemon keeps those in memory. A
  "placed" report waiting at a restart is lost too, so a seed on standby stays on
  standby until the tender next reads its mail.
- A ring taken before the Chief became a crew member keeps the old Chief address
  in its record. When the mod gives one back after the upgrade, attn returns its
  items to the unread mail of that profile's Chief member, even when attn could
  not yet create the Chief's home. The items then wait for the daemon start that
  creates the Chief.
- While a running turn waits on a permission question, the mod takes a ring it
  would take during that turn. It holds the ring until the question closes,
  because the mod's delivery cannot answer the question. Refusing the question,
  with "No" or with Esc, does not stop the session: Claude reports both the same
  way, and it takes the next prompt. A held ring then starts a new turn, and later
  rings go out as usual. A question outside a turn still holds every ring back.

## Notification runs

A notification run is a run that attn's own input started: a ring, or a crew
member's heartbeat. A Quick Capture is the user's own input, so a ring that
carries one starts the user's run, even with other items in the same ring. If
the user types into the agent during a notification run, or a Quick Capture
reaches it, the run becomes the user's. The user's answer to an ask and a change
to a seed's authorization do not change whose run it is.

With quiet notifications on (`queue_quiet_notifications`, the default) and the
queue flow on, attn does not bring the agent to the user for a notification run:

- When the run stops, attn opens no turn, unless the agent needs approval, the
  run ended on an error, or attn cannot tell the agent's state. An agent whose
  run failed may never get the response it needs to run `attn turn open`.
- When the run starts, attn settles a turn that opened when the agent stopped,
  not with `attn turn open`. The agent started working because of attn's own
  input, so the app leaves the user where they are.
- During the run, `attn agent inbox` ends with a note that says the run is
  quiet and points to `attn turn open`.

An agent that needs the user, or that finished work the user should read, opens
its own turn with `attn turn open`. If the agent runs the command while it
works, the turn opens when the agent stops. If
the user has snoozed the agent, the turn opens once the snooze ends and the
agent has stopped. The request survives a daemon restart. Notification runs do
not settle that turn. The user settles the turn, or the agent runs
`attn turn settle`. That command refuses a turn that opened when the agent
stopped, not with `attn turn open`. With the queue flow off, both commands
change nothing.

## Starts and resumes

### A crew member

When a crew member's address has unread items and the member is between sessions, attn
starts a session for the member before it rings. When a restart interrupted the member's latest
session, attn resumes that session and rings it instead of starting a new one.

- The start is one attempt for every unread item. The new session gets the ring
  when the session first reaches a safe prompt, and gets the user input as
  soon as it works. That ring is part of the same attempt. Items that arrive
  during the start share the attempt.
- The new session's first prompt names what started it: each unread seed
  update and peer message with attempts left, up to 8, then a count of the
  rest. A seed update reads as the inbox prints it, with its
  `attn seed show <id> --since <n>` command. A peer message names only its
  sender. A start with neither, such as one for a notice, opens with the plain
  greeting. The prompt says the user did not open the
  session, and tells the member to run `attn agent inbox` first. A member with
  a charter then does what its charter says. A member without a charter, one
  with onboarding pending, and the Chief still greet the user after the cause.
  When unread user input starts the session, the user is the cause, and the
  prompt is the plain greeting that every other start gets.
- A member has at most one mail-started session starting at a time.
- A mail-started session counts against the member's mail-start limit: 8 per 12 hours,
  set by `crew.mail_start_limit` and `crew.mail_start_window_seconds`. A start from
  the sidebar, a start from `attn crew start` and a start for an unread user
  message do not count, because the user causes each of them. A start for seed
  mail counts against the seed's limit instead (see
  [A seed on standby](#a-seed-on-standby)).
- A start that fails still uses an attempt, including one the limit
  refuses. attn tries again after 5 minutes. After 3 attempts the items wait
  for a read or a new item.
- attn never starts a session for a retired member, and attn refuses `attn agent msg` to a
  retired member, by name or by the member's session. Items that were unread at
  retirement keep their attempts and wait. When someone restores the member,
  attn delivers those items, and the delivery can start a session.
- attn starts another session when a session closes with unread
  items that have attempts left. The start comes 5 minutes after the last
  attempt, or at once for user input and for seed mail.
- attn starts sessions for the Chief member like any member.
- A manual start does not ring for items that have no attempts left. The agent
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
- `garden.tender_resume` allows the item. `any`, the default, allows all seed
  mail. `watches` allows PR watch updates and reminders only. `off` allows
  nothing, and the mail waits.
- The seed has had fewer than 48 tender resumes in the last 24 hours.

Who closed the session does not matter. The resume needs no connected app, and
the session opens on its last desktop. A crew member that tends the seed
gets a session as a member under every value of `garden.tender_resume`.

The resumed session gets its mail only through a path that knows when the agent
reads input. For Codex, attn rings once the resumed Codex first reports. For Claude, attn
waits for the reopened session's first Claude mod poll and delivers through the
mod. attn types nothing into a Claude terminal. A Claude session cannot be resumed
when its mod does not poll within 30 seconds of the resume; the block's reason
names the Claude Code version.

The first prompt carries the mail and the standby note. It says the user may
not be watching, and names the ways to end the turn: put the seed on standby
again, harvest it, ask the user with `attn ask`, or report. A resumed session that
stops owes the user a turn, as any resumed session does.

Standby ends when the tender has read all of the seed's mail, whether the
session was open or resumed. The tender reads the mail with `attn agent inbox`,
or with `attn agent inbox <message-id>` for one message. A ring also reads the
mail it types, such as the user's answer to an ask. A ring that only carries the
📬 line reads nothing. Standby also ends when the seed is parked, harvested or
withered, and when the seed gets a new tender.

A tender that closes before it reads the mail leaves the seed on standby, and
the unread mail resumes the tender again. Nobody holds the seed's mailbox while
the tender session is closed, so attn does not wait 5 minutes between attempts
for seed mail. The 3 attempts per item and the seed's resume limit bound the
repeats. attn delivers seed mail again when a session closes, when the daemon
restarts, and, unless the resume is blocked, when the bridge to the tender's
outpost connects. So a seed on standby with unread mail resumes its tender right
after any of them.

`attn seed standby` without `--stay` and `attn agent close --self` refuse while
a seed the session tends is on standby and has unread seed mail. The refusal names
`attn agent inbox`.

A compacting close, which `attn agent close --self --compact` asks for and
`attn seed standby` makes when the session can compact, places the compact in
the session's input lane, through the Claude mod or the shared Codex app server,
as soon as it is asked.
The harness alone decides whether the conversation is between turns: Claude
refuses the compact while a turn runs and the mod answers `busy`, and shared
Codex reports its thread active. A refused compact opens the lane again and is
placed again when the harness reports the end of the turn: the mod sends
`ready` when its next turn completes, and shared Codex reports its thread idle.
Apart from a pending approval, attn's own reading of the session's state plays
no part, so background work the agent leaves running does not hold the close.
The exception is a compact that fails before the harness can say whether a
turn runs: shared Codex's control connection or `thread/resume` fails, or no
link can compact the session. While attn reads the session as working, that
failure opens the lane again and the compact is placed again when the session's
state changes, so the close still waits for the turn that asked for it. A
failure once the session is prompt-ready closes it. A ring placed before the
compact starts a turn, and the compact waits for that turn's end. Once the compact is
placed and taken, the lane holds every later item until the session has closed,
and seed mail that arrives then resumes the closed tender as above. Nothing the
user types or sends stops the close. User input that attn would place while the
lane is held fails and tells the sender that the session is closing.
If no outcome arrives within `compact_outcome_tripwire`, the session closes with
a reason that says so.

The machine that runs the session keeps the waiting close beside the lane, and
asks the home to close the session once the compact has an outcome. On an outpost
this means:

- A home that is unreachable or restarting loses nothing. The outpost asks again
  until the home answers, and the session stays open until then. A compact whose
  turn ended while the home was away waits for the home, which decides whether
  the close still stands.
- A restart of the machine that runs the session drops the close, as a home
  restart does for a session on the home. The session stays open with its lane
  open and takes later items, and a compact already running still finishes. The
  agent asks to close again, or the user closes it.

Every tender resume for seed mail, of a session or of a crew member, counts
against one limit for the seed: 48 per 24 hours, held in memory. The count
restarts empty when the daemon restarts. These resumes do not count against
`crew.mail_start_limit`, so a member that tends several seeds is not capped across
them. The daemon log records each tender resume with the seed and its cause.

A resume that fails for a reason that does not fix itself writes a note on the
seed that names the reason and the item, and rings the seed's watchers and the
tenders of the plots above it. It also raises a critical notification with an
action that opens the seed. The reasons are a removed worktree, a missing
conversation, a conversation that would reopen in another profile, a Claude
session that cannot receive the mail through its mod, and the limit of 48. The
seed row carries the reason as `resume_blocked`. The item stays queued. attn does
not retry these resumes, except at the limit of 48: attn delivers the item when
the 24-hour window has room again.

An outpost that is unreachable, or that does not answer while attn checks the
tender's directory or conversation, is not a failed resume. The item stays queued
and uses neither an attempt nor a resume. attn tries again when the outpost's
bridge connects, or after 5 minutes.

A failure that can fix itself leaves the item queued, and attn tries again after
5 minutes. These failures are a resume that fails to launch, an outpost that
answers with an error when attn checks the tender's directory, and a plugin that
times out or fails while attn checks whether it still has the conversation. A
plugin that answers that the conversation is gone is a missing conversation. A
failed launch closes the session again, so its row and tile do not stay open
with nothing running, and the next attempt resumes it as itself. User input waits
for the next attempt too, because nothing is running to type it into. Each resume
uses one of the item's 3 attempts. A failure on the last attempt blocks the resume
as above, naming the failure. New mail gives the resume fresh attempts.

## Reading and receipts

`attn agent inbox` returns the unread items of every address the session holds,
oldest first. It leaves out user input, which only a ring delivers. It
returns 20 items by default, and `--limit` allows up to 50. The command marks
the items read, records which session read them, and says how many unread items
remain. The command ends the outstanding ring of every address it read, even
when `--limit` left items unread. attn rings again for those items at the next
safe prompt. When a read leaves a seed's mailbox with no unread item, the seed's
standby ends.

`attn agent inbox <message-id>` reads one peer message once attn has rung it,
whether or not the session read the message before. It ends standby the same
way.

`attn agent msg` replies at once with what happens next. The reply says one of
these:

- attn rang the agent.
- The message waits for a safe prompt or for an outstanding ring to be read.
- attn is starting a session for a member.
- The message waits for a tender or a chief.
- The mail-start limit is used up. The reply also names the setting to change.

`attn agent msg-status <id>` shows the sender whether the message is queued,
notified, or read.

## Limits

| Limit | Value | Where | Receipt |
|---|---|---|---|
| Time between attempts (user input and seed mail that nobody holds do not wait) | 5 minutes | `inbox.AttemptDelay` | Healthy agents read within 60 s of a ring at p99.5. |
| Attempts per item | 3 | `inbox.MaxAttempts` | One ring was enough for over 99% of items. The repeats were harness outages. |
| Mail-started sessions per member | 8 per 12 hours | `crew.mail_start_limit`, `crew.mail_start_window_seconds` | |
| Tender resumes per seed, for seed mail | 48 per 24 hours | `tenderResumeLimit`, `tenderResumeWindow` | None yet. The daemon logs each tender resume with the seed and its cause. |
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
recovery finishes, and the delivery can resume the tender of a seed on standby. A restart does not give items new attempts. A start or resume that a
restart interrupts still counts as an attempt. That session's ring then
waits 5 minutes and is a second attempt.

## Scenarios

**A burst to a member between sessions.** Alder tends four seeds and has no session. In two
minutes, 15 events happen on those seeds. The first update starts a session: one
attempt, one start against the limit. Later events on the same seed merge into
that seed's update. The first event on each other seed adds one update, and
that update shares the start. Alder's new session gets one ring and finds four
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
`attn seed standby s-x --in 20m -m "Read the rollout dashboard and report"`,
which also sets the seed's reminder to that time with the note as its message.
The session closes. When the reminder comes due, the seed is on standby and
`garden.tender_resume` is `any`, so attn resumes the closed session. For a Claude
session, attn waits for the mod's first poll. The session gets the note and
reads the reminder with `attn agent inbox`, and standby ends. The agent
reports, or goes on standby again.

**The mail-start limit is used up.** A message for a member between sessions arrives after the
member's 8th mail-started session in 12 hours. The reply to the sender names `crew.mail_start_limit`.
attn tries at 0, 5 and 10 minutes, then stops. The message waits for the
member's next session.
