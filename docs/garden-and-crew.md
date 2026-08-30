# Garden and crew

## The garden

The **Garden** holds all seeds and belongs to a home daemon. It has no workspace
dimension; plots are its only grouping. Outposts pass requests to their home
when the uplink is available.

A **seed** is a work item with an id, **slug**, title, markdown body, and state.
Its stable id is `s-` plus six Crockford base32 characters, such as `s-7k3f9m`.
Commands and agent messages use this id. It excludes `/`, allowing cross-daemon
addresses to prefix the owning daemon id. The slug uses the title's first six
key words with stop words removed, such as `mermaid-rendered-grid`. People use
it as a name, and several seeds may share one.

**Planting** creates a seed and prints its id, slug, and title. Seeds hold work
that outlives a turn, including discovered bugs, deferred follow-ups, and pieces
handed to another agent.

A seed **artifact** is a direct, visible regular file under
`<Notebook>/seeds/<seed-id>/`. Filesystem membership decides ownership; adding,
editing, renaming, or removing a file changes the artifacts. Hidden transfer
receipts and log entries do not count. Artifacts survive sessions, workspaces,
source worktrees, and seed lifecycle changes.

Move or Copy brings a file into the seed's ownership. Moving it out requires
an explicit destination and never overwrites. A **linked artifact** is an
attach-log reference to a repository path, Notebook document, or URL. The UI
keeps it separate until an explicit Bring into seed Move or Copy makes it owned.

A **plot** is a seed with children. Its body holds the execution plan, and its
children can run in parallel unless a `blocks` edge orders them. Adding a child
makes any seed a plot. A **packet** is a plot marked as a reusable template with
declared variables.

A seed begins `planted`. **Tending** atomically claims it, records its **tender**,
and makes it `growing`. **Parking** makes it `dormant` and releases the claim;
tending resumes it. **Harvesting** closes it as `harvested` with a required
reason. **Withering** closes it as `withered`, meaning abandoned. **Replanting**
returns a closed seed to `planted`.

A seed has one tender at a time. Tend, park, harvest, wither, and replant refuse
a seed held by someone else and name the holder; `--force` overrides the claim
and records who did so. A session that has ended or that the daemon no longer
knows releases its claim. Member-only claims (`--member <name>`) never expire.
`ready` and `tend` use the same ownership rule.

Workers and errand sessions can tend seeds, including under free-string member
names. A name matching a registered crew member resolves to that member's id;
crew registration is not required to tend work.

An **edge** relates two seeds and is stored on the seed it points from:

- **blocks**: `a blocks b` makes b wait for a.
- **part-of**: `b part-of c` makes b a child of plot c. Each seed has at most
  one parent plot.
- **discovered-from**: `a discovered-from b` records work found while tending b.
  A seed can have several origins; these edges do not affect order or readiness.

`sown-from` and `relates-to` remain declared but inert. Creating an ordering or
containment cycle fails with the two seed ids and the edge to remove.

**Ready** means an open seed that is not parked, blocked, held, a gate, a packet,
or under a packet. A plot itself is never ready; its children can be. Readiness
is computed on demand, so closing a blocker frees its dependents on the next
query. `attn seed ready` queries the whole Garden unless the session reports to
a seed, when it defaults to that seed's plot. `--all` queries the whole Garden.

`attn seed prime` combines standing launch guidance with the same live ready
answer that SessionStart injects, including after compaction. In a delegated
session it also points to the reporting seed's plan and shows fresh handoffs
for ready children.

In conversation, **ticket**, **todo**, **epic**, and **done** can mean **seed**,
**ready**, **plot**, and **harvested**. Agents default to Garden terms and mirror
a user's chosen alias for the rest of the exchange. These aliases create no
stored states and do not revive the retired Ticket entity.

**Dispatch-at-plot** starts a delegation at an existing seed through
`attn delegate --plot <seed>`. The delegate becomes its tender. An existing
live holder blocks dispatch before anything is created, except that the
dispatcher's own claim can pass to its delegate. Dead-session claims do not
block dispatch.

The reporting seed sets the delegate's default query scope. Delegates may still
tend or plant other seeds; several agents can share a plot by holding different
children. Each child's tender owns its claim.

A **stale** seed is open and has had no note, lifecycle move, or edge change
within a chosen window (`attn seed ls --stale`, seven days by default). This is
a query for human judgment; age never withers a seed automatically.

A **note** records what happened and what was learned on a seed's log. It is
quiet by default. `--ring` keeps the note on the log and sends a contentless
bell asking watchers to read it.

A **watch** records a session's interest in a seed. Lifecycle moves ring
watchers automatically; notes ring by choice. Watching a plot includes its
children, including children added later, through `part-of` edges. A dispatcher
automatically watches its delegated seed. `unwatch` removes an explicit watch
but leaves the automatic dispatch relationship.

One unread bell per watcher and changed seed coalesces updates until the
watcher reads `show` or `notes`. A session never rings itself for its own write.

A **handoff** is a note addressed to the next tender
(`attn seed note <id> -m "..." --handoff`). It stays quiet unless rung.
`attn seed show` displays the freshest handoff above the seed, and
`attn seed tend` prints it when claiming. Seed handoffs follow work
across tenders; crew handoffs follow a member across sessions.

## The crew

The **crew** is the roster of durable named identities owned by a home daemon.
A **crew member**, such as Keel, Alder, or Trellis, has a charter, a handoff
history, and an address. Its sessions are its **days**.

Member ids stay lowercase in paths, commands, storage, and protocol fields.
Names shown to people are capitalized: `attn crew wake trellis` wakes Trellis.

A member's **home** is `~/.attn/crew/<name>/`, containing `CHARTER.md` and dated
handoffs. These files are authoritative and hand-editable. The **registry**
indexes the member id, charter path, home directory, working directory,
harness, awareness directories, and active binding. It stores file locations
without copying their prose. A home added by hand joins the roster at the next
daemon start.

A **binding** records that a session launched as a member
(`attn <agent> --member <name>`). `attn agent list` and `attn agent peek` report
that identity. Reading a charter does not confer it. Each member has one active
binding; waking an already-live member reports its existing day.

To **wake** a member is to start a day in its recorded working directory, falling
back to its home, with access to its **awareness dirs**. `attn crew wake <name>`
launches directly. The sidebar requires one click to arm the row and a second
to confirm; an unconfirmed row disarms. Awake and sleeping members both appear
there.

The recorded harness comes from `attn crew set <name> --agent <agent>` and
defaults to Claude. `attn crew wake <name> --agent <other>` overrides it for one
day. A recorded `--model` applies to its recorded harness and does not cross a
one-day harness override. Without a member model, the launch harness's
configured default applies. Claude falls back to Fable if none is configured;
other harnesses use their own defaults.

**Priming** tells a new day its identity, where to read its charter, the freshest
handoff letter, and how to close the day.

A **sleep request**, sent by `attn crew sleep <name>` or the sidebar moon,
asks the live member to finish its letter and file `attn handoff --sleep`.
It does not kill the session. The explicit sleep request prevents a successor
from starting.

A member is also an address. `attn agent msg <member> "..."` reaches its live
day. For a sleeping member, it persists the message, wakes a day within the
wake limit, and delivers the message as the first attributed prompt after
priming. A refused wake delivers nothing and reports the limit and recovery
path.

A crew **handoff** is the current day's letter to its successor, filed by
`attn handoff -m "<letter>"`. attn writes it under the member's `handoffs/`.
The history is append-only: an existing filename is refused, and corrections
use a new letter. Only the bound session can file its day's handoff.

Filing chooses a **nap** or **sleep** from user presence. A nap closes the old
session and starts a fresh day primed by its letter, without the previous
transcript or compaction summary. It keeps the launch settings and moves the
binding atomically. Sleep leaves the member without a session until it wakes.
`--nap` and `--sleep` override presence for that handoff; user-requested sleep
uses `--sleep`.

A failed turnover leaves the letter filed and the old day running. The session
can call `attn handoff --retry` against the existing letter. The registry tracks
which letter was filed, so retry creates no second file. Trying to file again
after a failed turnover points to retry; retrying without a letter points to
handoff. A member is never torn down before its letter is filed.

Process exit releases the binding immediately, even if the session row remains
recoverable. Waking a stale binding releases it, names the exited session,
and starts a fresh day.

The **crew lifecycle** uses time since the user was last present and estimated
prompt-cache expiry. The estimate compares time since the session last called
the model with an assumed TTL for its harness; there is no cache-lifetime API.
A fresh cache needs no action. Near expiry, a present user triggers a
**heartbeat** that rereads the day's context, while an absent user triggers a
request to close the day.

A heartbeat reaches an `idle` or `waiting_input` member only after session input
proves the route or composer is safe. It counts as successful only when taken,
and its maintenance run never settles a turn. Autonomous wakes have a per-member
**wake limit**, bounding unattended use and naming that limit when refused.

## Ticket

The retired work item used for delegation before the Garden. A ticket bound a
session as its assignee and tracked reports across Todo, Working, Blocked,
In Review, and Done, with comments and artifacts on an activity thread.

Delegations now bind seeds and report through seed notes. Every `attn ticket`
write verb prints its Garden replacement and exits nonzero. Compatibility
continues in these cases:

- `attn ticket show` and `attn ticket list` read archived tickets permanently.
  `attn ticket inbox` consumes unread activity on tickets predating the Garden.
  User-created tickets and tickets of unknown origin retain their activity
  and attachments permanently.
- A delegation bound to a ticket at the transition keeps that ticket. The
  daemon mirrors its tender's seed moves and notes onto it.
- Automation runs still create daemon-internal tickets for continuation,
  retention, and crash classification. No CLI verb creates or moves them;
  seed moves mirror onto them. Only relational proof of creation by the
  Automation feature, such as a scheduled run or PR update, permits expiry.
  An agent creating or updating a ticket does not make it an automation ticket.

Unbound backlog todos became seeds with their descriptions and a log note
identifying the original ticket.

The main profile runs one create-only recovery pass for terminal tickets from
before permanent retention. It reads attn-owned local backups and eligible
native transcripts, restores missing archives without replacing live rows,
and represents each user ticket once in the Garden: `done` becomes `harvested`,
while `failed` and `crashed` become `withered`. Existing machine-proven
ticket-to-seed links are adopted without changing the seed. Legacy tickets
retain their original state, timestamps, activity, and attachments.

Codex and Claude recovery use profile-specific launch proof. Historical
Copilot transcripts use their native session envelope and an exact implicit
ticket-status receipt. The recovery pass treats those as main-profile history
because Copilot was not used with named profiles before permanent retention.

Insufficient terminal-ticket evidence creates no work. Available metadata is
preserved create-only in `legacy-ticket-recovery/fragments.json` or
`legacy-ticket-delegations.json` under the profile data directory, with one
warning naming the files. Recovery never scans other profiles or arbitrary
home directories.
