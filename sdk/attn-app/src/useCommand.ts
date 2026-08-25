
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useAppViewRuntime } from "./runtime"

// The returned runner NEVER rejects: a view calls it from an onClick, where a rejected
// promise nobody awaited becomes an unhandled rejection, not a message in the tile.

/** What one invocation did. Never a rejection — see the file header. */
export type CommandOutcome =
  | { ok: true; value: unknown }
  | {
      ok: false
      error: string
      /** A stable name for the refusal, when attn had one. `"reconcile_owed"` is
       * worth retrying — the app is rebuilding its collections. */
      code?: string
    }

export interface CommandRunner {
  (payload?: unknown): Promise<CommandOutcome>
  readonly pending: boolean
  /** The last failure, meant to be shown, cleared by the next call. */
  readonly error: string | null
}

/** Invoke one of this app's declared commands. It must appear in a `[[commands]]` block
 * of attn-app.toml and the bundle must export a handler under `commands`. */
export function useCommand(command: string): CommandRunner {
  const runtime = useAppViewRuntime()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const live = useRef(true)
  useEffect(() => {
    live.current = true
    return () => {
      live.current = false
    }
  }, [])

  const run = useCallback(
    async (payload?: unknown): Promise<CommandOutcome> => {
      if (!runtime) {
        const message = `useCommand("${command}") was called outside an attn app view host, so there is no daemon to run it.`
        setError(message)
        return { ok: false, error: message }
      }
      setPending(true)
      setError(null)
      try {
        const value = await runtime.command(command, payload)
        return { ok: true, value }
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err)
        const code = (err as { code?: unknown } | null)?.code
        if (live.current) setError(message)
        return { ok: false, error: message, code: typeof code === "string" ? code : undefined }
      } finally {
        if (live.current) setPending(false)
      }
    },
    [runtime, command],
  )

  return useMemo(() => {
    const runner = ((payload?: unknown) => run(payload)) as {
      (payload?: unknown): Promise<CommandOutcome>
      pending: boolean
      error: string | null
    }
    runner.pending = pending
    runner.error = error
    return runner as CommandRunner
  }, [run, pending, error])
}
