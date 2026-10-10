# Glossary

The glossary is attn's domain model, in the sense of domain-driven design.

```text
profile                 a separate world for the user's work; like a tenant
├─ desktop (1..n)
├─ flow (1)
├─ ledger (1)
├─ crew member (0..n)   at most 1 is the chief
├─ automation (0..n)
├─ garden (1)
├─ notebook (1)        the profile's journal and knowledge base; plain markdown
└─ setting (0..n)      a saved preference; applies to one profile or all profiles
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
└─ runs 1 harness or 1 shell
harness                 the agent CLI: Claude Code, Codex, pi, Copilot
model                   the AI model a harness runs
├─ tier (at most 1)      the intelligence a task needs: light, standard or deep
└─ effort (at most 1)    how much reasoning the model uses
agent                   a harness that runs in a terminal; the user talks to
                        them; like a coding agent
├─ session (1 at a time)
└─ state (1)            what the agent is doing now; like a presence status
   ├─ launching         starting up
   ├─ working           busy
   ├─ waiting           done; waits for the user's input
   ├─ needs approval    asks the user to allow a tool call
   ├─ scheduled         will continue by itself at a set time
   ├─ idle              nothing to do
   ├─ recoverable       stopped; attn can bring it back
   └─ unknown           attn cannot tell; may be an error
session                 attn's record of one conversation; like one chat in a
                        chat app's history
├─ conversation (1)     the harness's chat history
└─ worktree (at most 1) the git worktree the agent works in; several
                        sessions can share one
```

```text
ledger (1 per profile)  every session, live and closed; like a history
├─ session (0..n)
│  ├─ live              its agent runs in a terminal
│  └─ closed            ended; the history stays
└─ kept conversation (0..n)  attn's own copy of a conversation that its
                             harness would delete; like an archive
resume                  brings a closed session back as itself, in a tile;
                        like /resume in the harness
forget                  deletes attn's copy of a conversation
```

```text
party                   a session or crew member that sends mail or holds work;
                        like an account
actor                   a party, the user or attn that did something; like an
                        audit identity
address                 who an item is for: a session or a mailbox
├─ mailbox              crew member, chief or seed tender; like a shared
│                       mailbox (support@) that whoever holds the job reads
└─ inbox (1)
   └─ item (0..n)       peer message, seed update, user message,
                        PR watch update or notice
ring                    one prompt that tells an agent that they have unread
                        items; like a push notification
```

```text
flow (1 per profile)      how the user works with attn and their agents:
                          what asks for attention and how they move to it
├─ queue flow             attn brings the next turn to the user; like working
│                         an inbox to zero
└─ desktop flow           the user goes to their agents on their desktops;
                          like spatial work across macOS Spaces
queue                     agents in order of the attention they are owed
└─ turn (at most 1 per agent)  attention that an agent is owed
snooze                    puts off a turn until a set time
unsnooze                  ends a snooze early
```

```text
garden                  a profile's issue tracker
└─ seed (0..n)          a ticket
   ├─ state (1)         planted, growing, parked, harvested or withered
   ├─ seed (0..n)       a subtask
   ├─ tender (at most 1)  the assignee: the session or crew member
   ├─ note (0..n)       a comment in the ticket's log
   └─ edge (0..n)       blocks or discovered-from
tend                    claim a seed; like assigning a ticket to yourself
plot                    an epic: a seed with child seeds
packet                  an epic template
```

```text
crew member             an agent with a permanent identity and memory
├─ name (1)             the member’s name; unique in their profile
├─ charter (1)          who the member is and what they live for
├─ member home (1)      the folder that holds the charter and the letters
├─ launch desktop (1)   the desktop where their sessions start
├─ letter (0..n)        what one of the member's sessions leaves for their
│                       next sessions; like a shift handoff
└─ session (at most 1)  the member's current session
chief                   the crew member that coordinates one profile
asleep                  the member has no session, until something wakes them
awake                   the member has a session
retired                 the member is out of service; their identity and memory stay
wake                    starts a session for the member
nap                     ends the member's session and starts a new one from
                        their letters
retire                  takes a member out of service
restore                 returns a retired member to service
```

```text
automation              a prompt that attn starts on its own
├─ trigger (1)          a schedule or an event, like a PR review request
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
home                    the daemon that keeps the profiles, gardens and crew;
                        like a main server
└─ outpost (0..n)       a daemon on another machine that runs sessions for
                        the home; like a remote worker or build agent
instance                a daemon, an app and a data folder under one name;
                        like a separate install. Production has no instance
                        name.
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
│                       automations or garden; like a side drawer
├─ popover (0..1)       a floating panel anchored to a button: notifications
├─ menu (0..1)          a short list of choices: snooze options
├─ palette (0..1)       search and run commands, agents and tiles; like the
│                       VS Code command palette (⌘K, ⌘P)
└─ dialog (0..1)        a modal window that waits for an answer: settings,
                        confirmations
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

### Changes to make

- Flow is one daemon-wide setting (`queue_mode_enabled`). Make it 1 per profile.
- Named claims let any name tend a seed. Remove them, or define who can use
  them.
- The chief is not a crew member. Make it one.
- Prompts say hold, held and holder for a claimed seed
  (internal/prompts/content/garden.md, the delegation reference). Say claim and
  tender.
- The ledger may list every profile when a request has no profile. Check, and
  fix.
- The Ledger button and action say Reopen. Rename them to Resume.
- Remove the workflow engine: internal/workflow, internal/workflowresult,
  `attn workflow` and the workflow run drawer.
