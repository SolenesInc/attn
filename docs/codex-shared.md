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
  last resolved view -> drain transcript watcher
    native thread/archive
    reconcile final available transcript records
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

## Names

Native generated names and `/rename` updates project through the root binding into
one Attn label, including hidden owners and repeated views. Pane headers, queue,
CLI and ledger use that label. OSC titles still identify roots independently.
Shared owners never run Attn's headless title generator; legacy and other harnesses
keep their existing naming paths. Ephemeral title threads create no ledger owners.

Attn rename writes through `thread/name/set` and reports native rejection through
the existing rename result. Failed writes retain the confirmed label. Explicit
launch and crew names are saved in the existing launch context until applied to
the native root before first work. Successful application consumes that pending
name, so restart/reopen cannot overwrite a later manual rename. Native New/fork
clears the inherited launch name. Names in native read/resume snapshots reconcile
on binding and restart; newer name events take precedence over older snapshots.

If initial naming fails after creation, the successful native creation remains
successful. A warning links to the agent and explains how to rename it. Work is
refused while the launch name remains pending; a successful rename clears it and
lets the same conversation receive work.

Confirmed native name events also consume pending initialization, so native
`/rename` recovers the same way. A manual rename replaces an existing pending
name before the native write; after a crash, recovery can retry that replacement
but cannot replay the superseded launch name. Ordinary renames create no pending
initialization. A rejected replacement retains the confirmed label and remains
the pending correction until a write succeeds.

Initial and manual name writes serialize for each owner. They release the shared
runtime lock before waiting for Codex, so a delayed name reply cannot block other
owners. Successful writes consume only the pending name field and require the
same live native root; a late result cannot revive an archived owner.

Stock Codex 0.159.3 retained an explicit name on the first turn and a manual
`/rename` while its generated title response was delayed. Native precedence owns
that decision; Attn does not classify name events as manual or automatic.

## Accounting

Each owner has one bound transcript watcher and the existing persisted usage
source cursors. Tracking initializes before initial creation or native New/fork;
binding the native path never baselines away the first turn. Hidden owners retain
their watcher across switches, attachments and daemon restart. Native child and
guardian usage keeps its existing root attribution. Native token broadcasts are
observations and never add per-view usage to the ledger.

Final close drains the watcher before archive moves its rollout, then reconciles
the bound source from its persisted cursor before saving the closed row. Tracking
resumes after the move until final close succeeds, so a failed view cleanup or
ledger close keeps the open owner observable, including after restart. An archive
failure restores the watcher. This also covers a close before the watcher's first
poll. Records available at reconciliation are included, with the existing
model/cache/pricing policy. Native archive has no demonstrated disk-flush guarantee;
records arriving afterward are outside this settlement guarantee. Native server
exit marks open owners' measurement incomplete, including owners with no usage yet.

Live headers, the ledger inspector, `session show` and `session list --json` read
the same usage ledger. Reopen retains its source history and resumes its cursors;
repeated reads, views and native notifications cannot multiply usage. The inspector
names incomplete measurement and reuses the live header's model breakdown for
complete measurements. Ledger page reads retain newer live and close events received
while the request is pending, including filter removals. Updated rows follow the
daemon’s timestamp and ID ordering. Older-page requests use the last displayed
row because the daemon resolves cursor IDs against their current timestamps.
Price and billed-as setting changes refresh open ledger views, including closed
rows and their displayed model breakdown.

Stock Codex 0.159.3 moves a root rollout into `archived_sessions` before archive
success. The usage resolver follows this relocation, retains the original dated
live path as root source identity, and discovers native children in both trees.
Unarchive restores that same live path, so the saved cursor remains valid.

Archive discovery retains only this owner's matched descendants and partial
metadata awaiting completion. It scans on first discovery, archive directory
membership changes, or expanded descendant lineage; unchanged reconciliation
checks the directory stamp and retained sources. A failed metadata read does not
certify the scan. Cold and membership-change scans still read archive metadata
to recover parent links. Native filenames use local wall time, so creation-date
cutoffs would lose valid children across clock changes.

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
