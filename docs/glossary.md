# Glossary

The glossary is attn's domain model, in the sense of domain-driven design.

```text
profile                 a separate world for the user's work; like a tenant
├─ desktop (1..n)
├─ flow (1)
├─ ledger (1)
├─ crew member (0..n)   at most 1 is the chief
├─ automation (0..n)
└─ garden (1)
```

```text
desktop                 one arrangement of tiles; like a macOS Space or a
                        virtual desktop
└─ tile (0..n)          one rectangle on a desktop; like a tile in a tiling
                        window manager
   ├─ terminal tile     shows 1 terminal
   └─ browser, markdown, seed or editor tile
```

```text
terminal                one PTY process in a terminal tile
├─ runs 1 harness or 1 shell
├─ host (at most 1)    the outpost it runs on; no host means the home's machine
└─ offline             its host is unreachable (bridge lost or refused, or
                       Outposts off); typing is not sent
harness                 the agent CLI: Claude Code, Codex, pi, Nisse
model                   the AI model a harness runs
├─ tier (at most 1)      the intelligence a task needs: light, standard or deep
└─ effort (at most 1)    how much reasoning the model uses
link                    a way attn reaches a harness besides its terminal,
                        such as the shared Codex app-server or the Nisse
                        server; a session runs over the link its
                        conversation runs on now
agent                   a harness that runs in a terminal; the user talks to
                        them; like a coding agent
├─ session (1 at a time)
└─ state (1)            what the agent is doing now; like a presence status
   ├─ launching         starting up
   ├─ working           busy
   ├─ waiting           done; waits for the user's input
   ├─ needs approval    blocked on a prompt the user must answer
   ├─ scheduled         will continue by itself at a set time
   ├─ idle              nothing to do
   ├─ recoverable       stopped; attn can bring it back
   └─ unknown           attn cannot tell; may be an error
session                 attn's record of one conversation in one profile; like
                        one chat in a chat app's history
├─ conversation (1)     the harness's chat history
├─ terminal (0..n)      the terminals showing it; a terminal shows 1 session
│                       at a time; when its agent moves to another
│                       conversation (/clear, /new, /resume, Codex /agents),
│                       the terminal shows that conversation's session
├─ worktree (at most 1) the git worktree the agent works in; several
│                       sessions can share one
├─ stake (at most 1)    the seed assigned to the session by its delegation;
│                       it tends it and reports there; like an assignment
├─ vent (0..n)          an agent's report of a problem they had; like a bug
│  │                    report
│  ├─ attn vent         about attn itself; only these can go to attn's
│  │                    maintainers
│  └─ tool vent         about the agent's other tools, such as skills, CLIs
│                       and harness permissions; stays on this machine
└─ monitor (1 while live)  watches the session's agent on the machine it
                           runs on, and keeps its live state: state, turn,
                           cost, PR links
```

```text
ledger (1 per profile)  every session, live and closed; like a history
├─ session (0..n)
│  ├─ live              its agent runs in a terminal
│  │  └─ hidden         live over its link, but no tile shows it; like a
│  │                    minimized window
│  └─ closed            ended; the history stays
└─ kept conversation (0..n)  attn's own copy of a conversation that its
                             harness would delete; like an archive
resume                  brings a closed session back as itself, in a tile;
                        like /resume in the harness
forget                  deletes attn's copy of a conversation
```

```text
address                 who an item is for: a session or a mailbox
├─ mailbox              crew member, chief or seed tender; like a shared
│                       mailbox (support@) that whoever holds the job reads
├─ inbox (1)
│  └─ item (0..n)       peer message, seed update, quick capture,
│                       PR watch update or notice
└─ ask (0..n)           a question an agent sends the user, who answers in
                        their own time; like a decision request
   ├─ seed (at most 1)  the work the ask is about; the ask is addressed to
   │                    the seed's tender
   ├─ question (0..n)   pick one, pick any, or write an answer
   └─ answer (at most 1) the user's reply, typed into the conversation of
                        the ask's addressee as the user's message; a seed
                        also keeps it as a note
ring                    one prompt for an agent with unread items: it types
                        answers and quick captures as the user's message and
                        tells of the rest; like a push notification
quick capture           a quick message (with optional attachments) the user
                        can send to chief or crew, from outside attn, using a
                        global shortcut; like macOS Quick Note
```

```text
flow (1 per profile)      how the user works with attn and their agents:
                          what asks for attention and how they move to it
├─ queue flow             attn brings the next turn to the user; like working
│                         an inbox to zero
└─ desktop flow           the user goes to their agents on their desktops;
                          like spatial work across macOS Spaces
queue                     agents in order of the attention they are owed
└─ turn (at most 1 per agent)  attention that an agent is owed; opens when the
                               agent stops, or when it asks (attn turn open)
notification run          a run that attn's own input started, such as a ring;
                          with quiet notifications its stop opens no turn
snooze                    puts off a turn until a set time
unsnooze                  ends a snooze early
```

```text
garden                  a profile's issue tracker
└─ seed (0..n)          a ticket
   ├─ state (1)         planted, growing, parked, harvested or withered
   ├─ seed (0..n)       a subtask
   ├─ tender (at most 1)  the assignee: the session or crew member
   ├─ watcher (0..n)    a session or crew member that follows the seed and
   │                    its subtasks; like a watcher on a ticket
   ├─ note (0..n)       a comment in the ticket's log
   ├─ check (0..n)      something the seed must prove, numbered like C1; like
   │  │                 an acceptance criterion
   │  ├─ report (at most 1)   the proof someone recorded for it
   │  └─ verdict (at most 1)  confirmed or rejected
   └─ edge (0..n)       blocks or discovered-from
tend                    claim a seed; like assigning a ticket to yourself
plot                    an epic: a seed with child seeds
shared check            a check on a plot that every seed under it proves
packet                  an epic template
```

```text
crew member             an agent with a permanent identity
├─ charter (1)          who the member is and what they live for
├─ member home (1)      the folder with the charter and the letters
├─ launch desktop (1)   the desktop where their sessions start
├─ letter (0..n)        what one of the member's sessions leaves for their
│                       next sessions; like a shift handoff
└─ session (at most 1)  the member's current session
chief                   the crew member that coordinates one profile
asleep                  the member has no session, until something wakes them
awake                   the member has a session
wake                    starts a session for the member
nap                     ends the member's session and starts a new one from
                        their letters
```

```text
automation              a prompt that attn starts on its own
├─ trigger (1)          a schedule or an event, like a PR review request
│  └─ guard (at most 1) a shell command that decides whether a due tick starts a run
├─ launch desktop (1)   the desktop where its sessions start
├─ continuity (1)       which session takes the next occurrence:
│                       a new one each time, one per subject, or one only
└─ occurrence (0..n)    one time the trigger fires
   └─ run (at most 1)   the work that attn starts for one occurrence
      └─ session (at most 1)
```

```text
delegation              one agent starts another agent to work on a seed
├─ seed (1)             the work
├─ role (at most 1)     a way of working for one type of work:
│                       instructions, harness, model and effort
├─ dispatcher (1)       the session that started the delegation
└─ delegate (1)         the session that does the work
handover                a new session takes over a seed and its claim
```

```text
instance                a daemon, an app and a data folder under one name;
                        like a separate install. Production has no instance
                        name.
└─ outbox (1)           the telemetry attn keeps for upload; like a mail
                        outbox
```

```text
home                    the main daemon the user's app connects to
└─ bridge (0..n)       a connection through the user's ssh to one outpost
   ├─ state (1)
   │  ├─ connecting    the home is dialing the outpost
   │  ├─ connected     heartbeats arrive (every 0.5 s)
   │  ├─ stalled       no heartbeat for 3 s on an open connection; usually clears
   │  ├─ lost          no heartbeat for 60 s, or no connection; the home redials
   │  └─ refused       ssh refused authentication or a host key, or attn
   │                   refused the outpost's identity or build
   └─ outpost (1)     a remote daemon paired to that home

setting                 a daemon option, read and set with attn settings
├─ home setting         the home decides it; outposts keep a read-only copy
└─ machine setting      each machine decides its own, like its harness paths
```

```text
window (1..n)           one macOS app window
├─ home screen          the dashboard: what asks for attention, across agents
├─ sidebar (1)          its shape follows the profile's flow
│  ├─ queue sidebar     queue flow: crew, the oldest turns, the rest of the
│  │                    agents, automations, desktop chips
│  │  └─ queue bar      the queue sidebar collapsed into a strip across the top
│  └─ desktop sidebar   desktop flow: each desktop and its tiles
│     └─ collapsed      a thin column of desktop chips
├─ drawer (0..n)        a panel that slides in from the right: attention,
│                       asks, automations or garden; like a side drawer
├─ popover (0..1)       a floating panel anchored to a button: notifications
├─ menu (0..1)          a short list of choices: snooze options
├─ palette (0..1)       search and run commands, agents and tiles; like the
│                       VS Code command palette (⌘K, ⌘P)
├─ dialog (0..1)        a modal window that waits for an answer: settings,
│                       confirmations
└─ callout (0..1)       short text from an agent that points at part of the
                        app window; like a guided tour
current desktop         the desktop a profile shows; every window shares it
active tile             the tile a desktop has selected; every window shares it
tile history            the tiles a window has shown; ⌘[ and ⌘] walk it
```

## Profile

Profiles separate everything in attn for the user. For the user and their
agents, things in one profile do not exist in another, and attn does not leak
them across profiles.

## Seed

- planted: open (todo). Plant creates a seed, and replant opens a closed one
  again.
- growing: in progress. A tender claims it. Tend claims it.
- parked: on hold, with no claim. Park puts it down.
- harvested: done. Harvest closes it.
- withered: won't do. Wither closes it.

## Tech debt

### Legacy names

Temporary debt. Read the old name as the new term. Never write the old name.

- pane, leaf → tile
- notebook (as a tile kind) → editor tile
- satellite, satellite shell → terminal tile
- workspace → desktop
- session (of a shell) → terminal
- runtime (runtime_id) → terminal
- thread (Codex) → conversation
- role (as an address), seat → mailbox
- nudge (inbox) → ring
- queue mode → queue flow
- wake (a snooze) → unsnooze
- hold (a seed) → claim
- handoff (crew) → letter
- day (crew) → session
- continuity thread → continuity
- dormant → parked
- dock panel → drawer
- active leaf, active pane → active tile
- leaf history → tile history
- waiting_input → waiting
- pending_approval → needs approval
- reopen → resume
- reporting seed → stake

### Changes to make

- Flow is one daemon-wide setting (`queue_mode_enabled`). Make it 1 per profile.
- Named claims let any name tend a seed. Remove them, or define who can use
  them.
- The chief is not a crew member. Make it one.
- A seed watch belongs to a session. Make it belong to an address, so a crew
  member's watch outlives their session.
- Prompts say hold, held and holder for a claimed seed
  (internal/prompts/content/garden.md, the delegation reference). Say claim and
  tender.
- The ledger may list every profile when a request has no profile. Check, and
  fix.
- The Ledger button and action say Reopen. Rename them to Resume.
