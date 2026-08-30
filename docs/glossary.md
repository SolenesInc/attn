# Glossary

Details: [Notebook](notebook.md), [sessions](sessions.md),
[Garden/crew](garden-and-crew.md), [apps/events](apps-and-events.md),
[ownership](daemon-ownership.md), and [auto mode](maintaining.md#auto-mode).

## Workspace context

Shared working context for one workspace.

## The Notebook

Durable markdown shared across a profile.

## The journal

Dated, curated work history in the Notebook.

## Knowledge base

The Notebook's lasting knowledge.

## Note title

First H1, or the filename when absent.

## The keeper

Automates workspace narration and context maintenance.

## The chief of staff

Coordinates work across workspaces.

## Session

An attn runtime hosting a PTY or headless agent.

## Turns and the queue

- **Turn**: attention the user owes an agent.
- **Auto-settle**: settlement after the agent takes user conversation input.
- **Standing dismissal**: suppresses the next auto-settle.
- **Queue**: sidebar ordering by owed turns.
- **Pinned**: agent or workspace excluded from the queue.
- **Satellite**: a shell pane attached to an agent; an **orphan** has no live parent.

## Session input

The boundary for input to a live session. **Deferred** means untouched; placed, held by an adapter; taken, reading begun; indeterminate, uncertain outcome.

## Agent conversation

Provider history and resume target within a session.

## Session activity and presence

- **Activity**: what the agent is doing; activity cursor: the transcript position summarized.
- **Presence**: **watching** home, **present** elsewhere in the app, or **away**.

## Ticket

Archived delegation record predating seeds.

## The raw tier

Machine inputs to the keeper.

## App

- **App**: named automation in attn's shared runtime.
- **Version**: immutable build; serving history: versions served.
- **View**: app React component; tile: one layout instance.
- **Command**: view action; enabled: receiving events.
- **Applying** builds and selects a version; removing uninstalls the app.

## Plugin

External integration with its own supervised process.

## Event bus

Ordered log of domain facts.

- **Fact**: recorded change; subject: changed entity.
- **Consumer**: fact reader; cursor: reading progress.
- **Durable** consumers retain cursors across restarts; ephemeral consumers start at the head.
- **Projection**: converts a fact to app traffic; snapshot projections send whole lists.

## The retention floor, and the pin alarm

**Retention floor**: oldest protected cursor; pin: hold that floor; pin alarm: warning of a stalled cursor.

## The document store

Stores app JSON documents.

- **Namespace**: an owner's isolated data; collection: a named document group.
- **Document**: JSON object; id: unique key within its collection.
- **Revision**: write count; expectation: revision required by a write.
- **Declaration**: queryable fields and their types.
- **Query**: retrieval criteria; after cursor: pagination anchor; live query: a result subscription.

## Recoverable

**Recoverable**: conversation can be restored; reaped: session removed when recovery is impossible.

## The garden

- **Garden**: the home's work tracker; seed: a work item; slug: its readable name.
- **Artifact**: a file owned by a seed; linked artifact: an external reference.
- **Plot**: a seed with children; packet: a reusable plot template.
- **Plant/tend/park/harvest/wither/replant**: create/claim/pause/complete/abandon/reopen work.
- **Seed states**: `planted` (open), `growing` (claimed), `dormant` (paused), `harvested` (done), `withered` (abandoned).
- **Tender**: the seed's claimant.
- **Edge**: **blocks** orders work, **part-of** contains it, **discovered-from** records origin.
- **Ready**: available to claim; stale: open without recent activity.
- **Dispatch-at-plot**: delegation at an existing seed.
- **Note**: a log entry; watch: notification interest; handoff: a note for the next tender.
- Conversational aliases **ticket/todo/epic/done** mean **seed/ready/plot/harvested**.

## The crew

- **Crew**: named identities; member: one identity; day: its session.
- **Member home**: charter and handoff files; registry: their index; binding: a session launched as a member.
- **Awareness dirs**: charter's directories; priming: launch context.
- **Wake** starts a day; a **sleep request** asks the member to end it.
- **Sleep**: no live day; nap: replacement day.
- **Crew handoff**: letter for the next day.
- **Crew lifecycle**: day management using presence and cache age.
- **Heartbeat**: context refresh; wake limit: cap on autonomous starts.

## Client token

Profile protocol credential. The **browser host token** identifies the trusted WebView; the **HTTP bearer** protects exposed WebSockets.

## Home daemon

Owner of the fleet's Garden and crew.

## Outpost

- **Outpost**: enrolled daemon; enrollment: its recorded home relationship.
- **Uplink**: requests from outpost to home.
- **Hub**: dialing side; endpoint: SSH target; remote: the dialed machine.
- **Parked endpoint**: waiting for Sync.

## Conversation session

- **Conversation session**: a headless agent runtime; host: its process.
- **Envelope**: a host message; declaration: daemon-understood event; rendering: app display data.
- **State declaration**: a run-boundary state; tool call: an action; detail: its payload.
- **Run**: a prompt and response.
- **Prompt** starts a run; steer arrives at a boundary; follow-up waits for current work.
- **Input queue**: unread messages, distinct from the sidebar queue.
- **Snapshot**: current conversation; scroll-back/page: older history/a retrieval unit.
- **Epoch**: host generation; notice: interruption status.
- **Resume**: new session from history; launch prompt: opening message.

## nisse

attn's conversation agent, powered by pi's model loop.

## Auto mode, proposals, and promotion

- **Auto mode**: pi permissions; config: policy and environment context.
- **Proposal**: requested config change; promotion: the human applying it.
- **Environment template**: starting context; denial: a refused call.
