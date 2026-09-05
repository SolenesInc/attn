# {{app_name}} — an attn app

This directory is one attn app. An app is an automation attn runs for you: attn
wakes it when something happens, it keeps its own documents, it can show a tile
in a workspace, and it is versioned by the content of what it builds so you can
roll it back.

You can write the whole thing from this file. Nothing else needs reading.

## The shape

    attn-app.toml         what wakes the app, what state it owns, what it shows
    src/index.ts           your handlers — subscriptions, commands, reconcile
    src/views/Sessions.tsx a view: the component attn mounts as a tile
    src/generated.ts       derived from the manifest; do not edit
    tsconfig.json          so your editor sees what apply sees
    node_modules/          one symlink to the SDK, written by apply; do not commit

## The loop

    attn app apply .        build and install this app
    attn app dev .          the same, on every save
    attn app status {{app_name}}
    attn app logs {{app_name}}        what the app printed
    attn app rollback {{app_name}}
    attn app disable {{app_name}}     stop delivering to it, keep it installed

Apply parses the manifest, regenerates the two derived files, typechecks, bundles
and installs — in that order, and it stops at the first failure with nothing
installed. **Apply never runs your code.** A module that throws at import still
applies; you find out when a handler runs.

## Writing a handler

Every pattern under `[[subscribe]]` and every `[[commands]]` block becomes a
required key of the `Handlers` type in src/generated.ts — a subscription under
`subscriptions`, keyed by its event pattern; a command under `commands`, keyed
by its bare name. `reconcile = true` adds `reconcile` beside them. The default export of
src/index.ts must `satisfies Handlers`, so the compiler — not a convention, not a
runtime check — is what tells you the manifest and the code disagree:

    import type { Ctx, Handlers } from "./generated"
    import type { AppEvent, ReconcileReason } from "{{sdk_module}}"

    export default {
      subscriptions: {
        "session.state.changed": async (event: AppEvent, ctx: Ctx) => { ... },
      },
      commands: {
        forget: async (payload: unknown, ctx: Ctx) => { ... },
      },
      reconcile: async (reason: ReconcileReason, ctx: Ctx) => { ... },
    } satisfies Handlers

Declare any of them with no handler and apply fails with
`src/index.ts(7,1): error TS1360` — the file, the line, and what is missing.

Kind is structure, not a naming convention: a command and a subscription may
share a name, and neither can be mistaken for the other.

## What a handler is given

`event` is a fact from attn's durable event bus: `name` (dotted, e.g.
`session.state.changed`), `subject` (the entity it is about), `seq`, and
`payload`, typed `unknown` until the SDK pins that fact's shape.

A fact is an invalidation, not a payload. If you need the current state of
something, read it — the fact may already be stale by the time you run.

`ctx.collections.<name>` is one of the collections declared in the manifest,
scoped to this app's own namespace. `get`, `put`, `delete`, `query`,
`count`. Only fields you declared can be filtered or sorted on; the rest of a
document body is stored and read back untouched.

## Rebuilding what you derive: reconcile

A collection is a view over facts, and a fact is only ever seen once. Two things
would leave that view quietly wrong, so attn calls `reconcile` instead:

- **a version move** — this app now derives something different from the same
  facts, and the documents the old version wrote were never recomputed;
- **a gap** — the app resumed below the oldest fact still in the log, so those
  facts are gone and no retry can bring them back.

`reconcile = true` in the manifest is what makes the export required, and it is
also a promise attn holds you to: a subscribing app *without* it is refused a
version move, in those words, before the pointer moves — so an app that cannot
rebuild can never be updated. That is deliberate. There is no silent resume.

    async function reconcile(reason: ReconcileReason, ctx: Ctx): Promise<void> {
      const current = await ctx.current.snapshot()
      // delete what current truth no longer has, then upsert what it does
    }

`ctx.current.snapshot()` is attn's current state — the same domain projection
the app itself is handed when it connects: sessions, workspaces, tickets, PRs,
repos, seeds, crew, endpoints, apps, and the `asOfSeq` they were read at. It is
the whole reconcile-time surface besides your own collections. There is no
replay: a rebuild reads what is true now, never old fact payloads.

Three rules the runtime relies on:

- **Converge.** Run it twice, or after an attempt that died halfway, and the
  collection ends up the same. attn will do both.
- **Delete too.** A rebuild that only upserts leaves rows nothing will ever
  remove. Removing what current truth no longer has is half the job.
- **Yield.** It runs on the same single event loop as every other app. Await
  attn's APIs and the loop keeps turning; a synchronous loop that never yields
  freezes every app until attn kills the shared runtime out from under it.

Re-enabling is **not** a rebuild. An installed app's facts wait for it while it
is disabled, however long that is, and enabling delivers that backlog in order.
Installing for the first time is not one either — there is nothing to rebuild.

### While a rebuild is owed

Nothing else for this app runs. No fact is delivered and every command is
refused by name, with the code `reconcile_owed` and the fence, so a view can
tell "try again when this finishes" from "this handler is broken" without
reading English. Views stay mounted and see documents change under them; if a
rebuild needs to swap atomically, write a generation marker in the collection
and switch it last.

`attn app status {{app_name}}` is where it shows: whether a rebuild is owed, the fence
and cause that triggered it, the attempt running now, and the last failure.

A reconcile that throws is retried with backoff and every attempt is recorded.
One that stays stuck for fifteen minutes disables the app with a notification —
the rebuild is still owed, so fix the handler and `attn app enable {{app_name}}`. A
restart mid-rebuild is not your fault and is not counted: the attempt is marked
interrupted, and the rebuild is simply still owed.

## Views

A `[[views]]` block declares a component attn can mount as a tile in a
workspace. The user docks it from the command menu; the tile stays where they put
it, across restarts, until they close it.

A view is a function of where it sits. It is handed `ViewProps`:

    workspaceId   the workspace this tile is in
    sessionId     the session that workspace has selected, or null
    tileId        stable for the life of this docked tile
    params        the line the user typed when docking

`params` is what makes two tiles of one view show different things — one
filtered to a session, one showing everything. It is opaque to attn: declare a
`params` table on the view and the dock asks for it with your label, and hands
you the string exactly as typed (empty when the user left it blank).

## Reading in a view: useQuery

    const { docs, live, error } = useQuery<Seen>("seen", {
      filters: [{ field: "state", op: "eq", value: "idle" }],
      sort: { field: "updated_at", desc: true },
      limit: 50,
    })

It is a live window, not a fetch: attn keeps it current as documents change, and
re-renders only what moved. The collection is this app's — a view is handed its
own namespace and cannot ask for another's.

`live` says whether the daemon is serving the query right now. `error` is set
when the subscription ended and will not resume — a collection you removed from
the manifest, for instance. Render it; do not throw.

Only declared fields can be filtered or sorted on. There is no cursor: a live
query is a window, and a walk through pages is a different thing.

## Acting from a view: commands

    const forget = useCommand("forget")
    <Button variant="danger" disabled={forget.pending} onClick={() => forget({ id })}>

A command runs your handler, in the same process every other handler runs in,
with the same document access. That is the point: the view asks, the app decides.
A view never writes documents itself, so the app's rules live in one place.

`forget(payload)` never rejects — it resolves with `{ ok: true, value }` or
`{ ok: false, error }`, and mirrors a failure into `forget.error` for the
common case of rendering it. Throwing inside the handler is how a command
refuses; the message reaches the view.

The whole payload and the whole result travel as JSON, and one command may carry
256KB in either direction. A command is an action, not a data transfer — anything
larger belongs in a document the handler reads.

## Components

The SDK ships the controls a view needs to look native: `Button`
(primary / secondary / danger), `TextInput`, `TextArea`, `List` and
`ListRow`, `EmptyState`, and `Markdown`. Everything else you draw
inherits attn's own tokens — `var(--color-text-primary)`,
`var(--font-size-md)` and friends already resolve inside a tile — so a plain
element styled with them matches the app.

There is no spinner and no relative-time label, on purpose: attn sits open all
day beside GPU terminals, and a permanently animating element is a battery bug.
A loading state is text.

## Failure

A handler that throws fails that delivery. attn retries it with backoff rather
than skipping it, records every attempt, and if the app stays stuck on the same
event for fifteen minutes, disables the app — one broken app must not hold
everyone's event log open. `attn app enable {{app_name}}` puts it back, with a clean
slate.

A command that throws fails that click and nothing else: the view is told, the
attempt is recorded, and the app stays enabled. A view that throws while
rendering shows the error in its own tile and leaves the rest of attn alone.

Every app's handlers run in one shared process, so anything you print goes to a
log attn tags per app: `attn app logs {{app_name}}` reads your lines back, and
`attn app logs runtime` shows the whole thing — which is where a runtime
that will not start says why. A handler that never returns is abandoned after
sixty seconds; anything you await that is not one of attn's own APIs needs its
own timeout.

## Rules worth knowing before you start

- The manifest is the source of truth. Change what the app subscribes to, shows
  and answers there, never in code, and let the compiler tell you what to fix.
- Never edit src/generated.ts. Apply rewrites it.
- The SDK is `{{sdk_module}}`, and it is the only package you can import.
  Apply links it into node_modules; nothing is installed from a registry. There
  is no `react` to import — a view's JSX compiles to the SDK's own runtime,
  which is how an app and attn share one React.
- An unknown table in the manifest is a hard error, not a warning. An app must
  not half-load.
- Versions are content-addressed: applying the same content twice is the same
  version, and `attn app rollback` is a pointer move, not a rebuild. What a
  docked tile may call is what the *serving* version declared, so a rollback
  takes a command away with it — and, like any version move, it rebuilds.
- attn does not remember where this directory is. It is yours; keep it wherever
  you keep code.

