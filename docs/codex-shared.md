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
`codex_views` owns runtime/display relationships. `store.LaunchIntent.CodexMode`
distinguishes shared and legacy owners. Old rows remain legacy. The frontend
reads placement from daemon workspace layouts and metadata from the global
session lookup; it never rewrites an owner's workspace when its view switches.

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

Control subscriptions hold hidden roots. Switching away to zero views keeps an
owner alive. App loss, transport loss and daemon restart do not close owners.
The native app-server runs in a recoverable PTY worker and survives daemon stop.
Reconnect restores subscriptions and saved IDs without resending input. Native
server exit marks open owners recoverable with interrupted work; it never replays
an uncertain submission.

## Close coordination

```text
explicit close
  lock view/attach/title coordination
  another resolved view of this owner -> remove requested runtime only
  last resolved view -> native thread/archive
    await transcript watcher's final available-record reconciliation
    finalize ledger and publish closed owner
  remove requested runtime and view
```

An unresolved view cannot close its previous owner. Archiving the last known
view is refused while another unresolved view could still show that root. A
failed blank attachment can be closed without archiving the original owner.
Archive is the native stop/unload operation; it does not require the TUI to exit
or a `thread/closed` event. Ledger reopening unarchives the same native ID and
preserves the Attn owner and history.

This first integration exposes the shared identity and close boundary. Attention,
mailbox/annotation policy, final accounting guarantees and naming reconciliation
have separate follow-up work. They must use these owner/view contracts and the
same awaited close pipeline.
