// Running one handler: load the version's bundle, find the handler the daemon named,
// and call it with a context scoped to the app. The daemon decides which handler runs.

import { pathToFileURL } from "node:url"
import { RpcConnection } from "./rpc.ts"

/** What the daemon sends to run one handler. */
export interface DispatchParams {
  /** The daemon's id for this dispatch. Every callback carries it back. */
  dispatch: string
  app: string
  version_id: number
  /** Absolute path to this version's bundle. */
  artifact: string
  /** The subscription to invoke — a key of the bundle's `subscriptions` map. */
  handler: string
  /** The collections this version declared, by name. */
  collections: string[]
  event: {
    name: string
    subject: string
    seq: number
    payload: unknown
    published_at: string
  }
}

/** What the daemon gets back. A handler that threw is a result, not an RPC error. */
export interface DispatchResult {
  ok: boolean
  error?: string
}

/** What the daemon sends to run one command a view invoked. */
export interface CommandParams {
  dispatch: string
  app: string
  version_id: number
  artifact: string
  /** The command to invoke, by bare name — a key of the bundle's `commands` map. */
  handler: string
  collections: string[]
  /** The caller's argument, already parsed. Absent when the command takes none. */
  payload?: unknown
}

/** What the daemon gets back from a command, plus whatever the handler returned. */
export interface CommandResult {
  ok: boolean
  error?: string
  payload?: unknown
}

export interface ReconcileReason {
  causes: ("gap" | "version_changed")[]
  version: number
  throughSeq: number
  gap?: {
    cursor: number
    earliest: number
    missed: number
  }
  previousVersions: number[]
}

/** What the daemon sends to rebuild one app through a durable fence. */
export interface ReconcileParams {
  dispatch: string
  app: string
  version_id: number
  artifact: string
  collections: string[]
  reason: ReconcileReason
}

/** A handler of either kind: the first argument is the fact or the payload. */
type Handler = (arg: unknown, ctx: unknown) => unknown

// What an app's entrypoint default-exports: one map per kind of handler, so a command
// and a subscription of the same name are two different handlers.
type Bundle = {
  subscriptions?: Record<string, Handler>
  commands?: Record<string, Handler>
  reconcile?: Handler
}

type HandlerKind = "subscriptions" | "commands"

// Bundles already imported, by absolute path. It only ever grows; a runtime restart is
// what empties it.
const modules = new Map<string, Promise<Bundle>>()

// Which app each loaded bundle belongs to: how an error that escaped every handler is
// traced back to whose code it came from.
const appByArtifact = new Map<string, string>()

// The app whose bundle appears in this stack, if any. Empty when nothing matches;
// nothing is charged then.
export function appForStack(stack: string): string {
  for (const [artifact, app] of appByArtifact) {
    if (stack.includes(artifact)) return app
  }
  return ""
}

async function loadBundle(artifact: string): Promise<Bundle> {
  let pending = modules.get(artifact)
  if (!pending) {
    pending = importBundle(artifact)
    modules.set(artifact, pending)
    // A bundle that fails to import must not be remembered as broken forever: the
    // artifact may be mid-write, and the bus is going to retry this delivery.
    pending.catch(() => modules.delete(artifact))
  }
  return pending
}

async function importBundle(artifact: string): Promise<Bundle> {
  const module = (await import(pathToFileURL(artifact).href)) as { default?: unknown }
  const bundle = module.default
  if (!bundle || typeof bundle !== "object") {
    throw new Error(
      `${artifact} has no default export; an app's entrypoint default-exports its handlers grouped by kind, as \`export default { subscriptions: { "session.state.changed": onChange }, commands: { approve } } satisfies Handlers\``,
    )
  }
  return bundle as Bundle
}

/** The handler the daemon named, or undefined with nothing guessed. */
function handlerFor(bundle: Bundle, kind: HandlerKind, key: string): Handler | undefined {
  const group = bundle[kind]
  if (!group || typeof group !== "object") return undefined
  const handler = group[key]
  return typeof handler === "function" ? handler : undefined
}

/** Builds `ctx.collections`, one object per collection the version declared. */
function collectionsFor(
  conn: RpcConnection,
  dispatch: string,
  declared: string[],
): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const name of declared) {
    // Every call carries the dispatch id and no namespace: the daemon resolves which app
    // is asking, so an app cannot name a namespace at all, let alone another's.
    const call = (op: string, params: Record<string, unknown>) =>
      conn.call(`app.collection.${op}`, { dispatch, collection: name, ...params })
    out[name] = {
      get: (id: string) => call("get", { id }),
      put: (id: string, body: unknown, options?: { ifRev?: number }) =>
        call("put", { id, body, if_rev: options?.ifRev }),
      delete: (id: string) => call("delete", { id }),
      query: (options?: unknown) => call("query", { query: options ?? {} }),
      count: (options?: unknown) => call("count", { query: options ?? {} }),
    }
  }
  return out
}

/** Builds the context shared by subscriptions, commands, and reconciliation. */
function contextFor(
  conn: RpcConnection,
  params: { dispatch: string; app: string; version_id: number; collections: string[] },
): Record<string, unknown> {
  return {
    app: params.app,
    version: params.version_id,
    collections: collectionsFor(conn, params.dispatch, params.collections),
    current: {
      snapshot: () => conn.call("app.current.snapshot", { dispatch: params.dispatch }),
    },
  }
}

// Runs one dispatch and describes what happened. A handler that throws produces
// `{ok: false, error}`; only transport errors reach the daemon as an RPC failure.
export async function runDispatch(
  conn: RpcConnection,
  params: DispatchParams,
): Promise<DispatchResult> {
  appByArtifact.set(params.artifact, params.app)

  let bundle: Bundle
  try {
    bundle = await loadBundle(params.artifact)
  } catch (err) {
    return { ok: false, error: describeFailure(err) }
  }

  const handler = handlerFor(bundle, "subscriptions", params.handler)
  if (!handler) {
    return {
      ok: false,
      error: missingHandler(bundle, "subscriptions", params.app, params.version_id, params.handler),
    }
  }

  const ctx = contextFor(conn, params)
  // Announced before the call, because a handler that never yields would keep any later
  // announcement from being written; the `left` half keeps an entry from outliving it.
  const scope = { dispatch: params.dispatch, app: params.app }
  conn.notify("app_runtime.entered", scope)
  try {
    await handler(params.event, ctx)
    return { ok: true }
  } catch (err) {
    return { ok: false, error: describeFailure(err) }
  } finally {
    conn.notify("app_runtime.left", scope)
  }
}

// Runs one command a view invoked. It is runDispatch with a different argument and an
// answer that carries a value, reading a different group of the same default export.
export async function runCommand(
  conn: RpcConnection,
  params: CommandParams,
): Promise<CommandResult> {
  appByArtifact.set(params.artifact, params.app)

  let bundle: Bundle
  try {
    bundle = await loadBundle(params.artifact)
  } catch (err) {
    return { ok: false, error: describeFailure(err) }
  }

  const handler = handlerFor(bundle, "commands", params.handler)
  if (!handler) {
    return {
      ok: false,
      error: missingHandler(bundle, "commands", params.app, params.version_id, params.handler),
    }
  }

  const ctx = contextFor(conn, params)
  const scope = { dispatch: params.dispatch, app: params.app }
  conn.notify("app_runtime.entered", scope)
  try {
    const payload = await handler(params.payload, ctx)
    // undefined is "returned nothing", and JSON has no word for it: leaving the field
    // off distinguishes it from a handler that deliberately returned null.
    return payload === undefined ? { ok: true } : { ok: true, payload }
  } catch (err) {
    return { ok: false, error: describeFailure(err) }
  } finally {
    conn.notify("app_runtime.left", scope)
  }
}

/** Runs the serving version's reconcile sibling export. */
export async function runReconcile(
  conn: RpcConnection,
  params: ReconcileParams,
): Promise<DispatchResult> {
  appByArtifact.set(params.artifact, params.app)

  let bundle: Bundle
  try {
    bundle = await loadBundle(params.artifact)
  } catch (err) {
    return { ok: false, error: describeFailure(err) }
  }

  const handler = typeof bundle.reconcile === "function" ? bundle.reconcile : undefined
  if (!handler) {
    return {
      ok: false,
      error:
        `app ${params.app} version ${params.version_id} declares reconcile but its default export has no reconcile handler. ` +
        "The generated Handlers type makes this a compile error — the bundle is out of step with its manifest.",
    }
  }

  const scope = { dispatch: params.dispatch, app: params.app }
  conn.notify("app_runtime.entered", scope)
  try {
    await handler(params.reason, contextFor(conn, params))
    return { ok: true }
  } catch (err) {
    return { ok: false, error: describeFailure(err) }
  } finally {
    conn.notify("app_runtime.left", scope)
  }
}

// What to say when the manifest declared something the bundle does not export. The
// generated Handlers type makes this a compile error at apply time.
function missingHandler(
  bundle: Bundle,
  kind: HandlerKind,
  app: string,
  version: number,
  key: string,
): string {
  const exported = Object.keys(bundle[kind] ?? {})
  return (
    `app ${app} version ${version} declares ${key} but its default export has no handler for it under ${kind}. ` +
    (exported.length > 0
      ? `Its ${kind} are: ${exported.join(", ")}.`
      : `It exports no ${kind} at all.`) +
    " The generated Handlers type makes this a compile error — the bundle is out of step with its manifest."
  )
}

function describeFailure(err: unknown): string {
  if (err instanceof Error) return err.stack?.trim() || `${err.name}: ${err.message}`
  return String(err)
}
