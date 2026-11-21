import { spawnSync } from "node:child_process"

export type Check = { pass: boolean; why: string }

/** One call the agent made to attn: a CLI line (`seed note s-x -m …`) or a tool (`seed note`, with its arguments as
 * text). A code-mode script that names several tools counts once per tool, with the whole script as its text. */
export type Call = { via: "cli" | "tool"; command: string; text: string }

export type World = {
  dir: string
  profile: string
  session: string
  /** What setup leaves for check, such as the ids of the seeds it planted. */
  state: Record<string, unknown>
  /** Runs the instance's attn in this run's profile; throws with the output when it exits non-zero. */
  attn: (args: string[], opts?: { stdin?: string; session?: string; cwd?: string }) => string
  attnJson: (args: string[], opts?: { stdin?: string; session?: string }) => any
  /** A shell command in the agent's folder, on the machine the agent runs on. */
  sh: (command: string, stdin?: string) => { out: string; code: number | null }
  spawnShell: (label: string) => Promise<string>
  /** This profile's live sessions, as `attn agent list --json` shows them. */
  sessions: () => { id: string; directory: string; state: string; label: string }[]
}

export type Task = {
  id: string
  title: string
  /** `chief` starts the profile's Chief with `attn crew start chief` instead of a plain session. */
  launch?: "session" | "chief"
  settings?: Record<string, string>
  /** Prepares the agent's folder before the session starts and returns the folder to start it in, when not `dir`. */
  stage?: (dir: string) => string
  /** Prepares the world with the agent's session already at its prompt, and returns the prompt. */
  setup: (w: World) => Promise<string> | string
  check: (w: World, calls: Call[]) => Check
}

export const run = (command: string, args: string[], env: NodeJS.ProcessEnv, opts: { stdin?: string; cwd?: string } = {}) =>
  spawnSync(command, args, { env, input: opts.stdin ?? "", cwd: opts.cwd, encoding: "utf8", timeout: 120_000, maxBuffer: 64 << 20 })

const TOOL_NAME = /^mcp__attn\w*?__(\w+)$/
const TOOL_IN_SCRIPT = /mcp__attn\w*?__(\w+)/g
/** `attn` where a shell starts a command: at the start of the text or of a line, after ; && || | ( $( or an opening
 * quote, past env assignments. Prose that mentions attn inside a note's text is not a call. */
const CLI_LINE = /(?:^|[;&|(\n]|\$\(|["'])[ \t]*(?:\w+=\S*[ \t]+)*attn[ \t]+([^\n;&|"'`)<>]+)/g

/** The text of a tool call that can run: every string in its arguments except the prose a harness asks for beside a
 * command, so "List attn commands" in a description is not a call. Arguments that are not JSON, such as a code-mode
 * script, are the script. */
function runnable(text: string): string {
  let args: unknown
  try {
    args = JSON.parse(text)
  } catch {
    return text
  }
  const strings: string[] = []
  const walk = (v: unknown, key = "") => {
    if (typeof v === "string") {
      if (!["description", "justification"].includes(key)) strings.push(v)
    } else if (Array.isArray(v)) v.forEach((x) => walk(x, key))
    else if (v && typeof v === "object") for (const [k, x] of Object.entries(v)) walk(x, k)
  }
  walk(args)
  return strings.join("\n")
}

/** The attn calls in a transcript from `attn session transcript --json`, in order. */
export function attnCalls(events: any[]): Call[] {
  const calls: Call[] = []
  for (const e of events) {
    if (e.kind !== "tool_call") continue
    const name = String(e.tool_name ?? "")
    const text = String(e.text ?? "")
    const direct = name.match(TOOL_NAME)
    if (direct) {
      calls.push({ via: "tool", command: direct[1]!.replace(/_/g, " "), text })
      continue
    }
    const code = runnable(text)
    for (const m of code.matchAll(TOOL_IN_SCRIPT)) calls.push({ via: "tool", command: m[1]!.replace(/_/g, " "), text: code })
    for (const m of code.matchAll(CLI_LINE)) {
      const line = m[1]!.trim().replace(/\\$/, "")
      calls.push({ via: "cli", command: line, text: line })
    }
  }
  return calls
}

/** Whether a call is `command`: a CLI line that starts with it, or the tool of that name. */
export const isCommand = (call: Call, command: string) =>
  call.via === "tool" ? call.command === command : call.command === command || call.command.startsWith(`${command} `)

export const reached = (calls: Call[], command: string, mentions?: RegExp) =>
  calls.some((c) => isCommand(c, command) && (mentions === undefined || mentions.test(c.text)))

const SEARCH_TOOLS = new Set(["ToolSearch", "tool_search", "search_tool_bm25"])

export type Metrics = { turns: number; attnCalls: number; searches: number; helpReads: number }

export function metrics(events: any[], calls: Call[]): Metrics {
  let turns = 0
  let inAssistant = false
  let searches = 0
  for (const e of events) {
    const assistant = e.kind === "tool_call" || e.role === "assistant"
    if (assistant && !inAssistant) turns++
    inAssistant = assistant
    if (e.kind === "tool_call" && (SEARCH_TOOLS.has(e.tool_name) || /searchTools\(|describeTool\(/.test(String(e.text ?? "")))) searches++
  }
  const helpReads = calls.filter((c) => /(^|\s)(--help|-h)(\s|$)/.test(c.text) || isCommand(c, "skill") || isCommand(c, "guide")).length
  return { turns, attnCalls: calls.length, searches, helpReads }
}
