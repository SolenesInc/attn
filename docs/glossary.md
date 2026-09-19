# Glossary

## Sessions

- Session: an agent and its terminals, with history that survives restarts.
- Agent conversation: the provider's chat history. A session can start a new conversation.
- Run: one prompt and response.
- Parked run: a finished response whose background work is still running.
- Quiet window: time after the user's last keystroke when automated input must wait.
- Agent mailbox: notifications addressed to an agent.
- Inbox doorbell: a prompt telling an agent it has unread mailbox items.
- Peer message: a message from one agent to another.
- Turn: attention owed to an agent. Viewing the agent does not settle it.
- Auto-settle: closes a turn after the user's response and a period of uninterrupted agent work.
- Standing dismissal: suppresses the next auto-settle during the agent's current stretch of work.
- Queue: agents ordered by attention owed. Pinning an agent or workspace excludes it without settling its turns.
- Satellite: a shell pane attached to an agent.
- Orphan: a satellite without a live parent.
- Sliver: a pane or tile folded into a thin strip to make room.
- Pinned sliver: a pane or tile the user folded. It stays folded until the user expands it.
- Activity: a generated summary of what an agent is doing.
- Session usage: token counts and cost for a conversation and its native subagents. Delegated agents have separate sessions and usage.
- Recoverable session: a stopped session whose conversation can be restored.
- Reaped session: an unrestorable session removed from attn.
- Closed session: a session the user or an agent ended. Its history remains in the ledger.
- Final cost: a closed session's token totals and cost.
- Reopen: brings a closed session back under its original identity.
- Resume: copies a conversation into a new session.
- Reload: restores a recoverable session's own conversation.
- Session ledger: a daemon's record of its live and closed sessions.
- Ledger panel: the app's searchable lists of sessions and worktrees.
- Session repository: the repository where a session ran.
- Launch prompt: the opening message sent to a new agent.
- Session pull request: a PR an agent opened during a session.
- [PR watch](../README.md#watching-pull-requests): a durable subscription delivering PR updates to a session's agent mailbox.
- PR inbox: pull requests waiting on the user.
- Provenance line: shows where a session came from and what it produced.

- Focus mode: one workspace pane or tile occupies the shell until the user returns to the split.

## Garden and crew

- Garden: the home daemon's work tracker, shared across workspaces.
- Seed: a work item with an ID, title, body and state.
- Slug: a readable name derived from a seed's title. Slugs need not be unique.
- Plot: a seed with child seeds. Its body holds their shared plan.
- Packet: a reusable plot template.
- Plant: create a seed.
- Tend: claim a seed.
- Park: pause work and release the claim.
- Harvest: mark work complete.
- Wither: abandon work.
- Replant: reopen completed or abandoned work.
- Seed states: planted means open, growing means claimed, dormant means paused, harvested means done, and withered means abandoned.
- Seed outcome: the result and verification required before harvesting.
- Harvest condition: an instruction to harvest a seed when its PR merges. A PR closed without merging clears it instead of closing the seed.
- Tender: the agent or person claiming a seed. A seed has one tender at a time.
- Member claim: a tender recorded as a crew member with no session. It belongs to the permanent member and stays held while the member is asleep.
- Session claim: a tender recorded as a session. It counts as a crew member's work only while that session is the member's current day.
- Execution: the saved conversation and working location for a seed.
- Garden resume: reopens the seed's saved conversation in its saved directory.
- Handover: starts a new agent on the same seed and transfers the claim.
- Edge: a relationship between seeds. `blocks` orders work, `part-of` groups it, and `discovered-from` records its origin.
- Ready seed: open work an agent can claim now.
- Stale seed: work without recent activity. Age alone does not close it.
- Review Garden: a user-started review of growing seeds without an active agent.
- Garden advisor: a model that recommends actions during Review Garden. It cannot change a seed's state.
- Keep growing: dismisses a review item while leaving the seed open.
- Artifact: a file owned by a seed.
- Linked artifact: a reference to a file, Notebook document or URL.
- Note: an entry in a seed's log.
- Handoff: a note for the next tender.
- Watch: a subscription to updates about a seed and its descendants.
- Delegation preferences: saved roles and model choices for delegating work. They do not authorize delegation.
- Session delegation role: optional role identity captured at launch. Later settings changes do not relabel the agent.
- Delegation chain: an agent's dispatchers and delegates.
- Ticket: an archived work item from before the Garden.
- Crew member: an agent with a permanent charter.
- `attn`: the reserved member name the daemon uses when it moves a seed by itself. No crew home may claim it.
- Registry: the index of crew member files.
- Binding: a crew member's active session.
- Launch settings: a member's optional harness, model and effort pins. Blanks resolve through daemon and harness defaults.
- Charter token: the receipt for the exact charter bytes read. A replacement needs it and advances it, so a stale write cannot overwrite a newer one.
- Chief of staff: the agent coordinating work across workspaces.
- Day: a crew member's current session.
- Member home: the directory holding a crew member's charter and handoff.
- Wake: starts a crew member's day.
- Sleep request: asks a crew member to file a handoff and stop.
- Restart request: asks the current day to file its handoff and nap. It completes when the successor starts; an asleep member wakes directly.
- Wake limit: the cap on a crew member's autonomous starts.
- Sleep: a crew member has no active day.
- Nap: replaces the current day using its handoff.
- Heartbeat: refreshes a crew member's working context.

See [delegation preferences](delegation-preferences.md) for role settings.

## Knowledge

- Notebook: a profile's collection of Markdown documents.
- Journal: a dated record of work.
- Knowledge base: knowledge worth keeping across sessions.

## Apps

- App: a named automation running in attn.
- Plugin: an installed integration that adds capabilities to attn.
- Version: a saved app build.
- View: an app's visual interface.
- Tile: an open instance of a view.
- Command: a named action available in a view.

## Daemons and permissions

- Home: the daemon that owns the Garden and crew.
- Outpost: an enrolled daemon that runs sessions on another machine.
- Enrollment: an outpost's relationship with its home.
- Endpoint: a remote connection target.
- Parked endpoint: a connection waiting for compatible app and daemon versions.
- Headless task: a model call without an interactive session or terminal.
- Auto mode: lets the Guardian answer pi approval requests.
- Reviewer: the user or model deciding whether an operation may proceed.
- Guardian: the model reviewer for auto mode.
- Approval policy: determines which operations require approval.
- Sandbox mode: determines which resources an operation can access.
- Prefix rule: a permission rule matching the start of a command.
- Repository rules: permission rules saved with a repository.
- Host approval: permission to contact a network host.

## Worktrees

- Worktree registry: attn's inventory of worktrees.
- Worktree sweep: automatic cleanup of inactive worktrees whose work has merged.
- Integration branch: the branch a repository merges work into.
- Kept reason: why the sweep left a worktree alone.
- Keep pin: the user's instruction to preserve a worktree.
- Sweep log: a record of worktree removals and their reasons.

See [worktree sweep](worktree-sweep.md) for cleanup rules.
