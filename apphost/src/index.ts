// attn's shared app runtime: one supervised Bun process runs every installed app's
// handlers. Not a sandbox; isolation is failure attribution. Ships as a compiled binary.

import { AsyncLocalStorage } from "node:async_hooks"
import { RpcConnection, RPC_METHOD_NOT_FOUND, describe, type RpcRequest } from "./rpc.ts"
import {
  appForStack,
  runCommand,
  runDispatch,
  runReconcile,
  type CommandParams,
  type DispatchParams,
  type ReconcileParams,
} from "./dispatch.ts"

/** The runtime contract this host speaks; the daemon refuses a host that does not match.
 * Bump it together with appRuntimeAPIVersion in internal/daemon/app_runtime.go. */
const APP_RUNTIME_API_VERSION = 5

const currentApp = new AsyncLocalStorage<string>()

/** The tag `attn app logs <name>` filters on. Kept in step with appRuntimeLogTag in Go. */
function tag(app: string | undefined): string {
  return app ? `[app ${app}] ` : "[runtime] "
}

function captureConsole(): void {
  const write = (stream: NodeJS.WriteStream, args: unknown[]) => {
    const prefix = tag(currentApp.getStore())
    const text = args.map(render).join(" ")
    for (const line of text.split("\n")) stream.write(`${prefix}${line}\n`)
  }
  console.log = (...args: unknown[]) => write(process.stdout, args)
  console.info = (...args: unknown[]) => write(process.stdout, args)
  console.debug = (...args: unknown[]) => write(process.stdout, args)
  console.warn = (...args: unknown[]) => write(process.stderr, args)
  console.error = (...args: unknown[]) => write(process.stderr, args)
}

function render(value: unknown): string {
  if (typeof value === "string") return value
  if (value instanceof Error) return value.stack?.trim() || `${value.name}: ${value.message}`
  try {
    return JSON.stringify(value) ?? String(value)
  } catch {
    return String(value)
  }
}

/** Tripwire past a localhost round trip: the same shape on a live daemon (its liveness
 * ping) measures 344-416us, so a second is ~2,500x the real cost. */
const CRASH_REPORT_WAIT_MS = 1000

/** Reports the app whose code took the process down, then exits. The stack is the only
 * witness: AsyncLocalStorage.getStore() is undefined inside these handlers, measured. */
function installCrashReporter(connection: RpcConnection): void {
  let crashing = false
  const crash = (kind: string, reason: unknown): void => {
    // A second crash while reporting the first must not restart the wait.
    if (crashing) return
    crashing = true

    const error = render(reason)
    const app = appForStack(error)
    process.stderr.write(
      `${tag(app || undefined)}unhandled ${kind}${app ? ` in app ${app}` : ""}, stopping the app runtime: ${error}\n`,
    )

    const reported = connection.call("app_runtime.crashed", { app, kind, error }).catch(() => {})
    const bounded = new Promise((resolve) => setTimeout(resolve, CRASH_REPORT_WAIT_MS))
    void Promise.race([reported, bounded]).then(() => process.exit(1))
  }

  process.on("unhandledRejection", (reason) => crash("unhandledRejection", reason))
  process.on("uncaughtException", (err) => crash("uncaughtException", err))
}

/** Says why the process cannot start, then stops. The supervisor restarts it. */
function fatal(message: string): never {
  process.stderr.write(`${tag(undefined)}${message}\n`)
  process.exit(1)
}

function requiredEnv(name: string): string {
  const value = process.env[name]?.trim()
  if (!value) {
    fatal(
      `${name} is not set. The app runtime is launched by the attn daemon, which injects it; running this binary by hand is not a supported way to start it.`,
    )
  }
  return value
}

async function main(): Promise<void> {
  captureConsole()

  const socketPath = requiredEnv("ATTN_SOCKET_PATH")
  const generation = Number(requiredEnv("ATTN_APP_RUNTIME_GENERATION"))
  if (!Number.isSafeInteger(generation) || generation <= 0) {
    fatal(
      `ATTN_APP_RUNTIME_GENERATION is ${process.env.ATTN_APP_RUNTIME_GENERATION}, which is not a positive integer. The supervisor fences stale processes by generation, so a host that cannot present its own is refused.`,
    )
  }

  // Declared before the connection so the read loop can reach it: a dispatch can
  // arrive on the same tick the hello result does.
  let connection: RpcConnection
  const serve = async (request: RpcRequest): Promise<unknown> => {
    switch (request.method) {
      case "app.dispatch": {
        const params = request.params as DispatchParams
        // The tag follows the handler through every await it makes.
        return currentApp.run(params.app, () => runDispatch(connection, params))
      }
      case "app.command": {
        const params = request.params as CommandParams
        return currentApp.run(params.app, () => runCommand(connection, params))
      }
      case "app.reconcile": {
        const params = request.params as ReconcileParams
        return currentApp.run(params.app, () => runReconcile(connection, params))
      }
      case "app.runtime.ping":
        // A liveness answer the daemon can ask for without running app code.
        return { ok: true, api_version: APP_RUNTIME_API_VERSION }
      default:
        throw Object.assign(new Error(`unknown method ${request.method}`), {
          code: RPC_METHOD_NOT_FOUND,
        })
    }
  }

  connection = new RpcConnection(socketPath, serve)
  try {
    await connection.ready()
  } catch (err) {
    fatal(`cannot reach the attn daemon at ${socketPath}: ${describe(err)}`)
  }

  try {
    await connection.call("app_runtime.hello", {
      generation,
      api_version: APP_RUNTIME_API_VERSION,
      pid: process.pid,
    })
  } catch (err) {
    fatal(`the daemon refused this app runtime: ${describe(err)}`)
  }

  installCrashReporter(connection)

  console.log(`app runtime ready (generation ${generation}, pid ${process.pid})`)

  // The process lives as long as its connection: the supervisor already owns backoff,
  // generation fencing and parking, so a reconnect loop here would fight it.
  const ended = await connection.done()
  process.stderr.write(`${tag(undefined)}connection to the daemon ended: ${ended.message}\n`)
  process.exit(1)
}

void main()
