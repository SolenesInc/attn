// Sigma's own shell and file tools, served over MCP on stdio from inside the computer they act on.
// Runs in the container: `docker exec -i <id> bun /bench/computer-mcp.ts`, with sigma's checkout mounted at /sigma.
// The tools are the set sigma gives its own conversations (packages/nisse/src/tools/index.ts, `nisseTools`) on a
// LocalExecutionEnv, so they act on the container's files and processes with no code of sigma's changed.
//
// What serving them outside a sigma run needs, and this file supplies:
// - a ToolExecutionApi: a tool reaches files and processes through `api.env` and reports through `api.output`,
//   `api.retainedOutput`, `api.diagnostic` and `api.details`. A run's tool task supplies it; here it is the subset the
//   tools use. Kernel features (commit, memo, tasks, nested calls) fail if a tool reaches for them; none of these do.
// - argument validation and the model-facing result as the harness writes it: the output, or the retained output
//   when the tool returned none, then diagnostics in a `<harness>` block (appendToolResult in harness/src/tool.ts).
// Left out: `asks` approvals (no person here; Claude Code runs with permissions bypassed) and `rootConversationEnv`
// (there is no sigma conversation to name).
import { LocalExecutionEnv } from "/sigma/packages/env/src/local.ts"
import { DEFAULT_MAX_BYTES } from "/sigma/packages/env/src/truncate.ts"
import { validateToolArguments } from "/sigma/packages/models/src/index.ts"
import {
  createApplyPatchTool,
  createBashTool,
  createEditTool,
  createGlobTool,
  createGrepTool,
  createReadTool,
  createWriteTool,
} from "/sigma/packages/tools/src/index.ts"
import { Cause, Effect, Exit } from "/sigma/node_modules/effect/dist/index.js"

const cwd = process.env.COMPUTER_CWD ?? process.env.HOME ?? "/root"
const env = new LocalExecutionEnv({ cwd })
const tools = [
  createReadTool(),
  createGrepTool(),
  createGlobTool(),
  createWriteTool(),
  createEditTool(),
  createApplyPatchTool(),
  createBashTool(),
]
const byName = new Map(tools.map((tool) => [tool.name, tool as any]))

type Part = { type: "text"; text: string } | { type: "image"; data: string; mimeType: string }
type Diagnostic = { severity: string; message: string; code?: string }

const unavailable = (what: string) => () => Effect.fail(new Error(`${what} is not available outside a sigma run`))

const call = async (name: string, args: unknown): Promise<{ content: Part[]; isError: boolean }> => {
  const tool = byName.get(name)
  if (tool === undefined) return { content: [{ type: "text", text: `no tool named ${name}` }], isError: true }
  let input: unknown
  try {
    input = validateToolArguments(tool, { type: "toolCall", id: "mcp", name, arguments: args as any })
  } catch (error) {
    return { content: [{ type: "text", text: (error as Error).message }], isError: true }
  }
  let output = ""
  let dropped = false
  const diagnostics: Diagnostic[] = []
  let details: unknown
  const controller = new AbortController()
  const api = {
    taskId: "mcp",
    conversationId: "mcp",
    callId: "mcp",
    signal: controller.signal,
    env,
    now: () => Date.now(),
    outputWindow: undefined,
    output(chunk: string | Uint8Array) {
      output += typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk)
      if (output.length > DEFAULT_MAX_BYTES) {
        output = output.slice(-DEFAULT_MAX_BYTES)
        dropped = true
      }
    },
    retainedOutput: () => ({ text: output, truncated: dropped }),
    diagnostic: (diagnostic: Diagnostic) => void diagnostics.push(diagnostic),
    details: (value: unknown) => Effect.sync(() => void (details = value)),
    models: unavailable("models"),
    commit: unavailable("commit"),
    memo: unavailable("memo"),
    createTask: unavailable("createTask"),
    executeTool: unavailable("executeTool"),
    getTask: unavailable("getTask"),
    waitForTask: unavailable("waitForTask"),
    conversation: unavailable("conversation"),
  }
  const exit = await Effect.runPromiseExit(tool.execute(input, api))
  void details
  if (Exit.isFailure(exit)) {
    const error = Cause.squash(exit.cause) as Error
    return { content: [{ type: "text", text: error?.message ?? String(error) }], isError: true }
  }
  const result = exit.value as { output?: any[]; isError?: boolean; diagnostics?: Diagnostic[] }
  const content: Part[] = (result.output ?? (output === "" ? [] : [{ type: "text", text: output }])).map((part: any) =>
    part.type === "image" ? { type: "image", data: part.data, mimeType: part.mimeType } : { type: "text", text: part.text ?? "" },
  )
  const all = [...diagnostics, ...(result.diagnostics ?? [])]
  if (dropped && result.output === undefined) all.push({ severity: "info", message: "Earlier output was dropped" })
  if (all.length > 0)
    content.push({ type: "text", text: `<harness>\n${all.map((d) => `[${d.severity}] ${d.message}`).join("\n")}\n</harness>` })
  if (content.length === 0) content.push({ type: "text", text: "" })
  return { content, isError: result.isError ?? false }
}

// MCP over stdio: one JSON-RPC message per line.
const send = (message: unknown) => process.stdout.write(`${JSON.stringify(message)}\n`)
const handle = async (message: any) => {
  const { id, method, params } = message
  if (id === undefined) return // a notification
  if (method === "initialize") {
    return send({
      jsonrpc: "2.0",
      id,
      result: {
        protocolVersion: params?.protocolVersion ?? "2025-06-18",
        capabilities: { tools: {} },
        serverInfo: { name: "sigma-computer", version: "0.0.1" },
      },
    })
  }
  if (method === "tools/list") {
    return send({
      jsonrpc: "2.0",
      id,
      result: { tools: tools.map((tool: any) => ({ name: tool.name, description: tool.description, inputSchema: tool.parameters })) },
    })
  }
  if (method === "tools/call") return send({ jsonrpc: "2.0", id, result: await call(params.name, params.arguments ?? {}) })
  if (method === "ping") return send({ jsonrpc: "2.0", id, result: {} })
  send({ jsonrpc: "2.0", id, error: { code: -32601, message: `method not found: ${method}` } })
}

let buffered = ""
process.stdin.setEncoding("utf8")
process.stdin.on("data", (chunk: string) => {
  buffered += chunk
  let newline: number
  while ((newline = buffered.indexOf("\n")) >= 0) {
    const line = buffered.slice(0, newline).trim()
    buffered = buffered.slice(newline + 1)
    if (line !== "") void handle(JSON.parse(line))
  }
})
