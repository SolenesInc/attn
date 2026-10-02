# Shared Codex runtime

Settings has a temporary shared-Codex launch default, initially off. Existing
sessions retain their stored mode when the default changes. Headless tasks and
legacy sessions keep their existing launch path. Shared interactive sessions use
stock Codex with one app-server per instance and its ordinary shared Codex home.

## Identity and placement

```text
SessionID                       durable independent agent, ledger, seed, crew
ServerID + NativeRootID          unique native root bound to that agent
WorkspaceID + PaneID             placement, focus and close action
RuntimeID                       surviving terminal process and output
RuntimeID -> SessionID           mutable displayed agent, only when resolved
```

Native lifecycle replies discover roots; they do not establish the displayed
agent. Native OSC titles do that. The invocation-only title override includes
the thread ID, resolved only when its full or truncated token uniquely identifies
an independent root. An unknown or ambiguous title clears the displayed agent.
Worker observations retain the raw title, generation and timestamp independently
of heartbeat/state claims. Layout projections carry `codex_resolution` and
`codex_revision`; changing an agent never replaces its terminal.

The database owns launch context and the unique root binding in `codex_owners`.
`codex_views` owns runtime/display relationships. Layout reads join that table
for the displayed owner, resolution and revision. Title updates publish a fresh
layout snapshot without rewriting placement, focus or pane status.
`store.LaunchIntent.CodexMode`, projected as `Session.codex_mode`,
distinguishes shared and legacy owners. Old rows remain legacy. The frontend
reads placement from daemon workspace layouts and metadata from the global
session lookup; it never rewrites an owner's workspace when its view switches.
Workspace rows retain each view; the queue counts each owner once and prefers
its saved workspace when that workspace contains a view and participates in the
queue. Moving the only view leaves owner placement unchanged; the empty source
workspace does not gain a fallback agent row. Queue navigation follows the view.

## Lifecycle contracts

The daemon's `codexRuntime` coordinates these operations:

```text
prepareLaunch         reserve the external launch owner and view
prepareRPC            inject selected owner on resume; fresh owner on New/fork
bindOwner             bind the returned root and cwd idempotently
observeTitle          resolve or clear a view, rejecting old generations/timestamps
send                  owner-addressed turn/start or turn/steer, no uncertain resend
closeView             detach one view, or close the last displayed owner's root
closeAddressedOwner   select one unambiguous view, or close an owner without views
attachOwner           unarchive/reopen the original root in another terminal
```

The first native creation consumes the launch reservation once. Later native
New and root fork reserve new owners before forwarding, including when the
launch default has since been disabled. SessionStart hooks may bind the root
before its response arrives. Resume reapplies the selected owner's stored
cwd, model, effort, permissions, routing environment and Attn instructions.
Config additions preserve request fields, nested user entries and existing hook
indices/trust. Hook commands explicitly carry the owner/socket/wrapper env.
Attached and reopened terminals retain the original launch's directory-trust choice.

A rejected initial creation keeps its launch owner and view, so retry uses the
same owner. Rejected later New/fork creations discard their unused reservation.
Preparation failures use the same rule. Discarding an unused launch releases
its runtime state and role bindings.

Owner-addressed reload reapplies native configuration and current role guidance
through resume, preserving every terminal and draft. Control subscriptions hold hidden roots. Switching away to zero views keeps an
owner alive. App loss, transport loss and daemon restart do not close owners.
The native app-server runs in a recoverable PTY worker and survives daemon stop.
Its process cwd is the instance-owned server directory; owner and TUI cwd remain
their selected projects, so deleting one checkout cannot invalidate the server.
Reconnect restores subscriptions, saved IDs and the active native turn from
resume state before owner input is accepted. A newer native turn notification
takes precedence over the resume snapshot; uncertain input is never resent. Native
server exit marks open owners recoverable with interrupted work; it never replays
an uncertain submission.

Owner input releases the shared runtime lock before awaiting Codex's reply, so
an unanswered request cannot block other clients' owner/view operations. Calls
keep their caller's cancellation and disconnect handling; there is no added
input deadline or automatic resend. Recovery uses the saved Codex executable
and reports failure rather than selecting another installation.

## Close coordination

```text
explicit close
  lock view/attach/title coordination
  another resolved view of this owner -> remove requested runtime only
  last resolved view -> native thread/archive
    await transcript watcher's final available-record reconciliation
    atomically save archived owner and closed ledger
    release native turn state and publish closed owner
  remove requested runtime and view
```

An unresolved view cannot close its previous owner. Archiving the last known
view is refused while another unresolved view could still show that root. A
failed blank attachment can be closed without archiving the original owner.
Removing a view also removes its Unix socket after stopping its runtime.
Startup removes surviving views of archived owners through the same helpers,
so a crash after saving the close cannot leave their terminals behind.
Archive is the native stop/unload operation; it does not require the TUI to exit
or a `thread/closed` event. Ledger reopening unarchives the same native ID and
preserves the Attn owner and history. Native resume also creates and saves a
replacement placement when the original workspace was removed. Workspace close
removes each successfully closed pane before attempting the next; a later error
leaves the remaining panes available for an explicit retry.
When the saved worktree is missing, ledger recreation actions restore its branch
through the normal worktree planner before attaching that same native owner.

Worktree deletion identifies owners by their stored directory, so it can archive
those roots even when a view has unknown foreground identity. It removes views
displaying the deleted owner and runtimes launched for that owner, without
archiving foreign owners. Native archive and runtime removal must succeed before
ledger finalization. Cleanup failures name the owner and let the caller retry
deletion of the already-removed path; the removal audit remains recorded.

Final accounting guarantees and naming reconciliation have separate follow-up
work. They use these owner/view contracts and the same awaited close pipeline.

## Attention and input

The control connection projects `thread/status/changed` through the root-to-owner
binding into session evidence. Native active, approval and user-input flags remain
authoritative while a root has no visible terminal. Idle status uses the existing
Stop classifier to distinguish completion from a question. A new prompt hook
invalidates the previous idle snapshot before the next active notification arrives.
View connections do not duplicate this state projection.
Losing the control connection replaces its claims with attention needing input
and clears the active turn. The next explicit operation reconnects and reconciles
native root snapshots; it does not replay input.
Traffic from a surviving native view also wakes that reconciliation, so ordinary
prompts and approval answers restore attention without an unrelated app action.

The queue retains hidden shared owners once. Selecting one attaches its native
root through the existing reopen operation; an attachment error appears to the
user immediately. Native approval requests keep their original connection routing,
so answering in either view resolves the same operation.

PTY bytes address the surviving runtime. Typing and pointer activity capture its
resolved displayed owner for composition, quiet-window and attention credit.
Unresolved views earn no owner credit. Structured input addresses the intended
owner directly, using `turn/start` while idle and `turn/steer` with the current
expected turn while active. It leaves native drafts intact. Mailbox doorbells use
the existing prompt-ready and composition gates and earn maintenance credit;
mailbox storage and `attn agent inbox` remain owner-scoped.

An open annotation editor keeps the owner selected when it opened. After a native
switch its panel names that recipient. Draft stores, notes, save generations and
send progress belong to that owner; delayed replies cannot spend another owner's
draft. The terminal itself remains mounted throughout the switch.
Returning to an owner with a pending send restores that same send guard and draft
store until its reply settles.

Explicit native input rejections are reported as failures and release that attempt
for a later explicit submission. Transport loss remains indeterminate; input is
not automatically replayed. View lifecycle generations identify Codex exits even
after the view is removed, so closing an extra view emits no owner exit event.

Initial external launch creates its pane before reserving the owner and view in
one transaction. If startup is interrupted, the saved view remains visible and
closable as disconnected; it does not replay the launch. Reopening into a replacement
workspace saves that placement for later native New/fork owners. A selected
unresolved or disconnected view still supplies its workspace for New Session.

Additional attachments also save their pane before their durable view. An
interrupted attachment stays visible and closable as disconnected. Startup
layout reconciliation drops Codex panes whose view has already been removed,
without closing the live owner. Last-pane close publishes an empty layout when
the workspace remains registered, so the running app clears the pane immediately.
