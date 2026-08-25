// Design: docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md

import { createContext, useContext } from "react"

export interface DocumentRevision {
  id: string
  rev: number
}

/** The body is still JSON text. */
export interface RawDocument {
  id: string
  body: string
  rev: number
  created_at: string
  updated_at: string
}

export interface LiveQueryRequest {
  namespace: string
  collection: string
  filters?: Array<{ field: string; op: string; value: unknown }>
  sort?: { field: string; desc?: boolean }
  limit?: number
}

/** Render `order`. Take each body from `upsert` if it is there, else from your cache.
 * Forget every cached document not named in `order`. */
export interface QueryDelivery {
  delivery: number
  asOfSeq: number
  order: string[]
  upsert: RawDocument[]
}

export interface DocumentSubscriber {
  request: LiveQueryRequest
  have: () => DocumentRevision[]
  onDelivery: (delivery: QueryDelivery) => void
  /** Terminal: the daemon will not answer this query again as written. */
  onEnded: (code: string, message: string) => void
  onLive: (live: boolean) => void
}

export interface AppViewRuntime {
  /** The document namespace this mount reads. A view cannot widen it. */
  readonly namespace: string
  /** Opens a live query; the returned function closes it and must be called. */
  subscribe: (subscriber: DocumentSubscriber) => () => void
  command: (command: string, payload?: unknown) => Promise<unknown>
}

const AppViewRuntimeContext = createContext<AppViewRuntime | null>(null)

export const AppViewRuntimeProvider = AppViewRuntimeContext.Provider

/** Null outside a host, which is a real state for a component rendered in an app's
    own tests, so the hooks report it rather than throwing. */
export function useAppViewRuntime(): AppViewRuntime | null {
  return useContext(AppViewRuntimeContext)
}
