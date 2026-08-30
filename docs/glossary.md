# Glossary

## Sessions

- Session: attn runtime hosting an agent through a PTY or conversation host.
- Agent conversation: provider history and resume target; can change within one session.
- Conversation session: headless runtime; its host exchanges envelopes with the daemon.
- Envelope: sequenced host message. Declaration: daemon-readable session event. Rendering: app display data.
- Run: one prompt and response, from `run_started` to `run_settled`.
- Prompt: starts a run. Steer: read at the next agent boundary. Follow-up: read before settlement.
- Input queue: unread messages in a conversation host.
- Session input: ordered delivery to a live session with evidence of receipt.
- Input evidence: deferred = untouched; placed = adapter-owned; taken = reading begun; indeterminate = uncertain.
- Turn: attention owed to an agent; viewing it does not settle it.
- Auto-settle: closes a turn after proven user-conversation input and uninterrupted working time.
- Standing dismissal: suppresses the next auto-settle for the current working stretch.
- Queue: sidebar ordering by owed turns. Pinning excludes an agent/workspace without settling turns.
- Satellite: shell pane attached to an agent. Orphan: satellite without a live parent.
- Activity: generated status line. Activity cursor: transcript position already summarized.
- Presence: watching = home visible; present = recent input elsewhere in app; away = neither.
- Recoverable: runtime gone, conversation restorable. Reaped: unrestorable session removed.
- Snapshot: current conversation state. Epoch: host generation. Scroll-back: older paged history.
- Resume: copies history into a new session. Reload: reopens a recoverable session's own history.
- Launch prompt: opening message replayed only if a replacement host finds empty history.
- nisse: attn's conversation agent, powered by pi.

## Garden and crew

- Garden: home daemon's work tracker, shared across workspaces.
- Seed: work item with stable `s-...` id, title, body, and state. Slug: readable, non-unique name.
- Plot: seed with children; its body is the plan. Packet: reusable plot template.
- Plant/tend/park/harvest/wither/replant: create/claim/pause/complete/abandon/reopen.
- Seed states: planted/open, growing/claimed, dormant/paused, harvested/done, withered/abandoned.
- Tender: seed claimant; one at a time.
- Edges: blocks orders work; part-of contains it; discovered-from records origin.
- Ready: claimable open seed, excluding plots, parked/blocked/held work, gates, packets, and packet descendants.
- Stale: open without recent activity; age alone never closes work.
- Artifact: owned file under `<Notebook>/seeds/<seed-id>/`, retained across session/workspace/seed lifecycles.
- Linked artifact: reference to an external file, Notebook document, or URL.
- Note: seed log entry. Handoff: note for the next tender. Watch: interest in change notifications.
- Dispatch-at-plot: delegation bound to an existing seed as its tender.
- Ticket: archived pre-Garden work item; user tickets and their history remain permanently.
- Crew member: durable named identity with a charter. Day: its current session.
- Member home: charter/handoff directory. Registry: index of member files. Binding: member's active session.
- Awareness dirs: working context directories. Priming: launch guidance.
- Wake: start a day. Sleep request: ask it to file a handoff and stop.
- Nap: replace a day using its handoff. Sleep: no live day. Heartbeat: refresh current context.
- Wake limit: cap on autonomous starts.

## Knowledge

- Workspace context: current, potentially stale account of one workspace.
- Notebook: profile-wide durable markdown; files are authoritative.
- Journal: dated work history. Knowledge base: lasting knowledge. Raw tier: machine inputs to the keeper.
- Note title: first H1 outside fenced code; filename fallback. Frontmatter `title` is ignored.
- Keeper: maintains workspace context and journal narration.
- Chief of staff: coordinates work across workspaces.

## Apps and events

- App: named automation in the shared runtime. Plugin: integration with its own supervised process.
- Version: immutable app build. Applying: build and select a version. Serving history: versions served.
- View: app React component. Tile: one mounted instance. Command: named view action.
- Event bus: ordered domain-fact log. Fact: recorded change. Subject: changed entity.
- Consumer: fact reader. Cursor: reading progress; durable cursors survive restarts, ephemeral readers start at head.
- Projection: fact-to-app traffic. Snapshot projection: whole-list update.
- Retention floor: oldest protected cursor. Pin alarm: warning of stalled reading.
- Document store: app JSON data. Namespace: owner isolation. Collection: document group.
- Document id: key within a collection. Revision: write count. Expectation: required revision, zero means absent.
- Declaration: queryable field types. Query: retrieval criteria. After cursor: pagination anchor.
- Live query: subscription delivering complete replacement results.

## Daemons and permissions

- Home: owns fleet Garden/crew. Outpost: enrolled daemon owning local sessions. Uplink: requests home.
- Enrollment: recorded home relationship. Hub: dialing side. Endpoint: SSH target. Remote: dialed machine.
- Parked endpoint: binary/protocol mismatch awaiting Sync.
- Client token: profile protocol credential. Browser host token: trusted WebView identity.
- HTTP bearer: operator credential for exposed WebSockets.
- Auto mode: pi permissions. Config: policy/environment snapshot at launch.
- Proposal: requested policy/model change. Promotion: user applies it. Denial: refused call.
- Environment template: initial classifier context copied into config.
