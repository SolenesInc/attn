# Apps and events

## App

An **app** is automation that agents write and apply while attn runs. Its
manifest declares its behavior; it consumes event-bus facts, stores data in its
own document namespace, and runs in attn's shared supervised runtime.

The app's name is its registry key and determines its bus consumer
(`app:<name>`), document namespace (`app/<name>`), and directory. attn derives
these identities from the name.

The app's **enabled** state is its bus consumer's enabled bit, stored in the
database. `attn bus disable app:<name>` stops delivery. An installed app keeps
its unread facts under the [retention rules](#the-retention-floor-and-the-pin-alarm).

**Removing** an app stops and deletes its bus consumer and removes the registry
row. Version history, invocation logs, and documents under `app/<name>` survive.
Deleting those data requires a separate, explicit action.

A **version** is an immutable built artifact identified by its content hash,
with the manifest declaration from its build. Applying inserts a version and
moves the serving pointer; byte-identical content reuses the existing version.
The invocation log can therefore identify the version that ran.

The **serving history** is the chain of versions an app has served. Each bare
`attn app rollback <name>` steps further back and refuses at the oldest entry.
Applying a version or choosing a version by name starts the history again from
that point, with the previously running version as the way back from an apply.
Versions passed during rollback remain available by name.

A **view** is a React component declared in the manifest's `[[views]]` and built
as a browser artifact beside its handler bundle. It docks in a tile of kind
`app:<app>/<view>`. The user's docking string becomes `params`, interpreted
by the app. Views import `@victorarias/attn-app` and must not import React
separately. attn's browser import map resolves the SDK specifier after the build
so views share its React instance and hook dispatcher.

The daemon serves each view at a URL containing its version's content hash.
Changing the serving version changes the URL and remounts the view. Load,
export, and rendering failures affect only that tile and produce an invocation
log entry stamped with its version, visible through `attn app logs`.

A **tile** is a pane the user can split, drag, and size in a workspace layout.
It holds one mounted instance of a view. Two tiles of the same view have
independent mounts, their own `params`, and a `tileId` stable for each tile's
lifetime. Changing how tiles mount leaves the app's view declaration, registry
row, and artifact intact.

A **command** is an action a view invokes by name. The manifest declares it in
`[[commands]]`, and the bundle exports `command:<name>`. The mount determines
which app receives it, as it does for document access. Commands run in the
shared sidecar with the same document access, invocation log, and sixty-second
abandon rule as other handlers. The serving version defines which commands
exist, including after rollback.

A command failure returns the handler's error to the view and records that
invocation. It does not advance the stalled-consumer clock that can disable an
app, because a user command holds no event-log cursor.

**Applying** parses the manifest, generates handler types, typechecks, bundles,
hashes, writes the artifact, inserts the version, and moves the serving pointer.
It never imports or executes app code. Every failure occurs before the pointer
moves, leaving the previous version serving. `attn app dev` repeats apply on save.

## Plugin

A **plugin** connects attn to an external integration, such as an agent driver
or worktree hook. It runs as its own supervised process and connects to the
daemon. Plugins are platform integrations installed infrequently. A plugin failure
interrupts its integration. A failing app is disabled independently.

## Event bus

`internal/bus` is an append-only log of changes in the daemon. The code making
a change publishes a **fact**, and consumers read the facts in order.

- A fact has a name (`session.state.changed`, `garden.harvested`), a **subject**
  naming the changed entity, and a small JSON payload. Terminal output, attach
  data, and file contents travel separately.
- A **durable consumer** stores a **cursor**, the sequence number of the last
  fact it handled, in the database. It resumes there after a restart. An
  **ephemeral consumer**, such as the WebSocket hub, starts at the head and
  keeps no durable cursor.
- A **projection** turns a fact into WebSocket traffic for the app. The
  `wireProjections` table in `internal/daemon/bus.go` holds these rules. Most
  re-read and push one entity; a **snapshot projection** pushes a whole list.

Publisher and consumer implementation rules live in
[the maintainer reference](maintaining.md#event-bus).

## The retention floor, and the pin alarm

The **retention floor** is the lowest cursor held by an enabled durable consumer
or an installed app. That consumer **pins** the event log. Age-based trimming and
compaction preserve all unread facts above the floor.

A disabled ordinary consumer releases its hold and can lose unread facts.
Installed apps pin even while disabled, preserving their history until they
are enabled or uninstalled. A consumer that stops reading while holding the
floor lets the log keep growing.

The **pin alarm** reports a consumer whose cursor has stalled past the alarm
threshold, one hour by default. The threshold is beyond stalls attn resolves
itself. Notifications, `attn bus status`, and event-bus settings use the same
predicate. One warning covers an episode; the cursor must move before a new
episode can begin. The alarm never drops pinned facts or disables a consumer.

## The document store

The **document store** holds app data. Each record has these addresses:

- A **namespace**, in the form `owner/name` such as `app/approval-gate`, belongs
  to one author and isolates its reads and writes. Apps use the `app/` owner segment; attn uses
  `core/`. Collection names may repeat across namespaces.
- A **collection** is a named set of documents within a namespace.
- A **document** is one JSON object with a caller-chosen **id**, unique in its
  collection. The store preserves its body byte for byte and never migrates it.

A document's **revision** counts writes from 1. Reads and writes report it.
Supplying a revision as a write's **expectation** makes the write conditional;
a mismatch refuses the write and reports both revisions. An expectation of 0
requires that the document be absent. Omitting the expectation allows an
unconditional write.

A collection's **declaration** lists queryable fields and their types. Other
keys remain stored and returned unchanged. Field types control comparisons,
so `"5"` in a `number` field sorts as 5. `created_at` and `updated_at` are
always queryable without a declaration.

Each collection has its own table. Declared fields become indexed columns
computed from the body, without rewriting documents. Removing a collection or
redeclaring it without a field a live query uses terminates that query with an
error naming the missing collection or field.

A **query** names its namespace, collection, filters, sort, limit, and optional
**after cursor**. The cursor is the last document id from the preceding page.
It respects the full `(sort field, id)` order; a filter on the sort field alone
would skip ties with `>` or repeat the anchor with `>=`.

A **live query** remains open and delivers the complete current result set.
The daemon reruns it after a collection write may have changed the answer.
Subscribers replace their result with each delivery. A skipped delivery is
superseded by the next complete result.
