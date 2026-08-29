
import { useEffect, useMemo, useState } from "react"
import type { Document, Filter } from "./index"
import {
  useAppViewRuntime,
  type DocumentRevision,
  type QueryDelivery,
  type RawDocument,
} from "./runtime"

/** What a live query takes. No `after`: a live query is a window, a cursor is a walk. */
export interface LiveQueryOptions {
  filters?: Filter[]
  sort?: { field: string; desc?: boolean }
  /** Defaults to the store's own 100, and refuses more than 1000. */
  limit?: number
}

export interface QueryError {
  // `collection_undefined` and `collection_redeclared` mean this query is over;
  // `invalid_query`, `undeclared_collection` and `subscription_limit` mean it never started.
  code: string
  message: string
}

export interface QueryResult<Body> {
  /** The window, in the server's order. */
  docs: Array<Document<Body>>
  /** The log position this window was true at. Opaque and monotonic. */
  asOfSeq: number
  /** Whether the daemon is serving this query right now. */
  live: boolean
  /** Set when the subscription ended and will not resume: a state to render. */
  error: QueryError | null
}

// How many unmounted queries keep their bodies for a resume. Same receipt as the daemon's
// per-client subscription tripwire (measured 2026-08-13 against the production database).
const RESUME_CACHE_LIMIT = 64

// Bodies by query, kept past unmount so a remount resumes with `have`. One cache serves one
// subscription at a time, or two tiles would invalidate each other's credited bodies.
const resumeCaches = new Map<string, { bodies: Map<string, RawDocument>; held: boolean }>()

// Take the cache for this query, or a private one when another subscription already holds
// it. Every checkout is paired with a `releaseCache`.
function checkoutCache(key: string): { bodies: Map<string, RawDocument>; release: () => void } {
  const existing = resumeCaches.get(key)
  if (existing && !existing.held) {
    existing.held = true
    resumeCaches.delete(key)
    resumeCaches.set(key, existing)
    return { bodies: existing.bodies, release: () => (existing.held = false) }
  }
  if (existing) {
    // A detached cache, kept out of the map so releasing it cannot clobber the holder's bodies.
    // cannot clobber the holder's bodies.
    return { bodies: new Map<string, RawDocument>(), release: () => {} }
  }
  const fresh = { bodies: new Map<string, RawDocument>(), held: true }
  resumeCaches.set(key, fresh)
  while (resumeCaches.size > RESUME_CACHE_LIMIT) {
    let evicted = false
    for (const [candidate, entry] of resumeCaches) {
      // Never evict a cache a live subscription is resuming from.
      if (entry.held) continue
      resumeCaches.delete(candidate)
      evicted = true
      break
    }
    if (!evicted) break
  }
  return { bodies: fresh.bodies, release: () => (fresh.held = false) }
}

function queryKey(namespace: string, collection: string, options: LiveQueryOptions): string {
  return JSON.stringify([
    namespace,
    collection,
    (options.filters ?? []).map((f) => [f.field, f.op, f.value]),
    options.sort ? [options.sort.field, !!options.sort.desc] : null,
    options.limit ?? null,
  ])
}

function parseBody<Body>(raw: RawDocument): Document<Body> {
  return {
    id: raw.id,
    body: JSON.parse(raw.body) as Body,
    rev: raw.rev,
    created_at: raw.created_at,
    updated_at: raw.updated_at,
  }
}

// Subscribe to a collection and stay current. The namespace comes from where the tile is
// mounted, so a view cannot read another app's documents by asking.
export function useQuery<Body = unknown>(
  collection: string,
  options: LiveQueryOptions = {},
): QueryResult<Body> {
  const runtime = useAppViewRuntime()
  const namespace = runtime?.namespace ?? ""
  const key = queryKey(namespace, collection, options)

  const [state, setState] = useState<{ docs: Array<Document<Body>>; asOfSeq: number }>({
    docs: [],
    asOfSeq: 0,
  })
  const [live, setLive] = useState(false)
  const [error, setError] = useState<QueryError | null>(null)
  // Reset during the render that changes the key, or a view renders the previous window
  // labelled as the new one, or stays `live` across the gap.
  const [describedKey, setDescribedKey] = useState(key)
  if (describedKey !== key) {
    setDescribedKey(key)
    setState({ docs: [], asOfSeq: 0 })
    setLive(false)
    setError(null)
  }
  const [generation, setGeneration] = useState(0)

  useEffect(() => {
    if (!runtime) {
      setError({
        code: "no_runtime",
        message:
          "useQuery was called outside an attn app view host, so there is no daemon to query.",
      })
      return
    }
    const after = (options as { after?: unknown }).after
    if (after !== undefined) {
      setError({
        code: "invalid_query",
        message:
          `useQuery cannot take an after cursor (${String(after)}): a live query is a window and a cursor is a walk, ` +
          "so the document it names moves out from under the subscription. Set a limit and render each window.",
      })
      return
    }

    setError(null)
    // Survives this effect and this mount: it is what `have()` reads.
    const { bodies: cache, release } = checkoutCache(key)
    let dropped = false

    const apply = (delivery: QueryDelivery) => {
      for (const doc of delivery.upsert) cache.set(doc.id, doc)
      const docs: Array<Document<Body>> = []
      for (const id of delivery.order) {
        const raw = cache.get(id)
        if (!raw) {
          cache.clear()
          if (!dropped) setGeneration((n) => n + 1)
          return
        }
        docs.push(parseBody<Body>(raw))
      }
      // The forget rule: anything not named in `order` is gone from the window.
      const named = new Set(delivery.order)
      for (const id of Array.from(cache.keys())) {
        if (!named.has(id)) cache.delete(id)
      }
      setState({ docs, asOfSeq: delivery.asOfSeq })
    }

    const unsubscribe = runtime.subscribe({
      request: {
        namespace: runtime.namespace,
        collection,
        filters: options.filters?.map((f) => ({ field: f.field, op: f.op, value: f.value })),
        sort: options.sort,
        limit: options.limit,
      },
      have: (): DocumentRevision[] =>
        Array.from(cache.values()).map((doc) => ({ id: doc.id, rev: doc.rev })),
      onDelivery: apply,
      onEnded: (code, message) => {
        setLive(false)
        setError({ code, message })
      },
      onLive: setLive,
    })

    return () => {
      dropped = true
      unsubscribe()
      release()
    }
  }, [runtime, collection, key, generation])

  return useMemo(
    () => ({ docs: state.docs, asOfSeq: state.asOfSeq, live, error }),
    [state, live, error],
  )
}
