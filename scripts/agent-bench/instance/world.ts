import { YAML } from "bun"
import { spawnSync } from "node:child_process"
import { readdirSync, readFileSync } from "node:fs"
import { join } from "node:path"

export type Check = { pass: boolean; why: string }

/** One call the agent made to attn. `command` is the catalog's command on both surfaces, such as `seed note`; `text` is
 * the CLI line or the tool's arguments. A code-mode script that calls several tools counts once per tool, with the whole
 * script as its text. `refused` is the failed call's answer. */
export type Call = { via: "cli" | "tool"; command: string; text: string; help?: boolean; refused?: string }

export type World = {
  dir: string
  profile: string
  session: string
  /** What setup leaves for check, such as the ids of the seeds it planted. */
  state: Record<string, unknown>
  /** Runs the instance's attn as `session`, or else in this run's profile when the command takes one; throws with the output when it exits non-zero. */
  attn: (args: string[], opts?: { stdin?: string; session?: string; cwd?: string }) => string
  /** Runs a command on the instance, in no profile. */
  instance: (args: string[]) => string
  attnJson: (args: string[], opts?: { stdin?: string; session?: string }) => any
  /** A shell command in the agent's folder, on the machine the agent runs on. */
  sh: (command: string, stdin?: string) => { out: string; code: number | null }
  spawnShell: (label: string) => Promise<string>
  /** This profile's live sessions, as `attn agent list --json` shows them. */
  sessions: () => { id: string; directory: string; state: string; label: string }[]
  /** A session's screen, as `attn agent peek` shows it. */
  screen: (session: string) => string
  /** The agent's last message. */
  answer: () => string
  /** Saves `folder` as trusted in the Codex home, so a Codex the agent starts there does not ask. Only at the home. */
  trustForCodex: (folder: string) => void
  /** The markdown files the agent's session opened in tiles, newest first. */
  opened: () => string[]
  /** Waits for the session an accepted delegation starts in `directory`: the delegate tool answers before it exists. */
  delegated: (directory: string, calls: Call[]) => Promise<{ id: string; directory: string }[]>
  /** Whether the app draws a Mermaid source, and the statements it leaves out. Only for a task that sets `mermaid`. */
  mermaid: (source: string) => { drawn: boolean; skipped: string[] }
}

export type Task = {
  id: string
  title: string
  /** `chief` starts the profile's Chief with `attn crew start chief` instead of a plain session. */
  launch?: "session" | "chief"
  settings?: Record<string, string>
  /** Its check reads Mermaid through `World.mermaid`, so the bench builds the app's Mermaid check first. */
  mermaid?: boolean
  /** Its state belongs to the whole instance, so its runs go one at a time and `restore` puts the state back after each. */
  restore?: (attn: (args: string[], stdin?: string) => string) => void
  /** Prepares the agent's folder before the session starts and returns the folder to start it in, when not `dir`. */
  stage?: (dir: string) => string
  /** Prepares the world with the agent's session already at its prompt, and returns the prompt. */
  setup: (w: World) => Promise<string> | string
  check: (w: World, calls: Call[], ended: string) => Promise<Check> | Check
}

export const run = (command: string, args: string[], env: NodeJS.ProcessEnv, opts: { stdin?: string; cwd?: string } = {}) =>
  spawnSync(command, args, { env, input: opts.stdin ?? "", cwd: opts.cwd, encoding: "utf8", timeout: 120_000, maxBuffer: 64 << 20 })

const CATALOG = join(import.meta.dir, "../../../internal/catalog/content")
const TOOL_NAME = /^mcp__attn\w*?__(\w+)$/
/** A tool called in a code-mode script, as tools.mcp__attn__seed_note(…) or tools["mcp__attn__seed_note"](…). */
const TOOL_IN_SCRIPT = /mcp__attn\w*?__(\w+)["'\x60]?\]?\s*\(/g
const SHELL_TOOLS = new Set(["Bash", "bash", "shell", "local_shell", "exec_command", "unified_exec"])
/** Code-mode scripts whose inner calls the transcript does not list. Claude Code's REPL lists its own. */
const SCRIPT_TOOLS = new Set(["exec", "codemode"])

type CatalogFile = { groups?: { name: string; flags?: { name: string }[] }[]; commands?: { name: string; args?: { cli?: string }[] }[] }
const CATALOG_FILES = readdirSync(CATALOG).filter((name) => name.endsWith(".yaml")).map((name) => YAML.parse(readFileSync(join(CATALOG, name), "utf8")) as CatalogFile)
const FAMILY_FLAGS = new Map(CATALOG_FILES.flatMap((f) => f.groups ?? []).map((g) => [g.name, (g.flags ?? []).map((flag) => flag.name)]))
/** Whether the CLI takes `--profile` on a command: the flag belongs to a whole family, as the catalog says. */
export const takesProfile = (args: string[]) => FAMILY_FLAGS.get(args[0]!)?.includes("profile") === true

const catalog: { tools: { name: string }[] } = JSON.parse(readFileSync(join(import.meta.dir, "../../../internal/daemon/agent_tools_generated.json"), "utf8"))
const COMMANDS = new Set(catalog.tools.map((t) => t.name.replace(/_/g, " ")))

const KEYWORDS = new Set(["then", "do", "else", "elif", "if", "while", "until", "!", "{", "time", "exec", "command", "sudo", "nohup"])

const HEREDOC = /^<<-?(?!<)[ \t]*(['"]?)([\w.-]+)\1/
/** Where the script goes on after a heredoc body that starts at `from`. */
function afterHeredoc(script: string, from: number, tag: string) {
  const close = script.slice(from).match(new RegExp("^[ \\t]*" + tag.replace(/\./g, "\\.") + "[ \\t]*$", "m"))
  return close ? from + close.index! + close[0].length : script.length
}

const COMPOUND = new Set(["if", "for", "while", "until", "case", "select", "function", "{"])
/** The commands a shell script names, as words: quotes resolved, heredoc bodies and redirections left out, and the
 * commands inside $( ) and backquotes listed before the command that holds them. `joins` are the operators between them,
 * with `(` for a compound command. */
function shellCommands(script: string, joins = new Set<string>()): { commands: string[][]; joins: Set<string> } {
  const out: string[][] = []
  let words: string[] = []
  let word: string | undefined
  let dropNext = false
  const heredocs: string[] = []
  const flush = () => {
    if (word === undefined) return
    if (dropNext) dropNext = false
    else words.push(word)
    word = undefined
  }
  const end = () => {
    flush()
    if (words.length > 0) out.push(words)
    if (COMPOUND.has(words[0] ?? "")) joins.add("(")
    words = []
  }
  const substitution = (from: number, close: ")" | "\x60") => {
    let depth = 1
    let i = from
    const pending: string[] = []
    for (let quote = ""; i < script.length; i++) {
      const c = script[i]!
      const tag: RegExpMatchArray | null = quote || c !== "<" ? null : script.slice(i).match(HEREDOC)
      if (c === "\\") i++
      else if (quote) quote = c === quote ? "" : quote
      else if (tag) {
        pending.push(tag[2]!)
        i += tag[0].length - 1
      } else if (c === "\n" && pending.length > 0) i = pending.splice(0).reduce((at, t) => afterHeredoc(script, at, t), i + 1) - 1
      else if (c === "'" || c === '"') quote = c
      else if (close === ")" && c === "(") depth++
      else if (c === close && --depth === 0) break
    }
    out.push(...shellCommands(script.slice(from, i), joins).commands)
    word = (word ?? "") + script.slice(from, i)
    return i
  }
  for (let i = 0; i < script.length; i++) {
    const c = script[i]!
    if (c === "\\") {
      i++
      if (script[i] !== "\n") word = (word ?? "") + (script[i] ?? "")
    } else if (c === "'") {
      const close = script.indexOf("'", i + 1)
      word = (word ?? "") + script.slice(i + 1, close < 0 ? undefined : close)
      i = close < 0 ? script.length : close
    } else if (c === '"') {
      word ??= ""
      for (i++; i < script.length && script[i] !== '"'; i++) {
        if (script[i] === "\\") word += script[++i] ?? ""
        else if (script[i] === "$" && script[i + 1] === "(") i = substitution(i + 2, ")")
        else if (script[i] === "\x60") i = substitution(i + 1, "\x60")
        else word += script[i]
      }
    } else if (c === "$" && script[i + 1] === "(") i = substitution(i + 2, ")")
    else if (c === "\x60") i = substitution(i + 1, "\x60")
    else if (c === "#" && word === undefined) {
      const line = script.indexOf("\n", i)
      i = (line < 0 ? script.length : line) - 1
    } else if (c === "<" || c === ">") {
      if (/^\d*$/.test(word ?? "")) word = undefined
      flush()
      const op = script.slice(i).match(/^(<<-?(?!<)|<<<|>>|[<>]&?)/)![0]
      i += op.length - 1
      if (op.startsWith("<<") && op !== "<<<") {
        const tag = script.slice(i + 1).match(/^[ \t]*(['"]?)([\w.-]+)\1/)
        if (tag) {
          heredocs.push(tag[2]!)
          i += tag[0].length
        }
      } else dropNext = true
    } else if (c === " " || c === "\t") flush()
    else if (c === "\n") {
      if (script.slice(i).trim()) joins.add(";")
      end()
      i = heredocs.splice(0).reduce((at, tag) => afterHeredoc(script, at, tag), i + 1) - 1
    } else if (";|&".includes(c) || (word === undefined && "()".includes(c)) || c === ")") {
      const doubled = "|&".includes(c) && script[i + 1] === c
      if (doubled) i++
      joins.add(doubled ? c + c : "()".includes(c) ? "(" : c === "&" ? ";" : c)
      end()
    } else word = (word ?? "") + c
  }
  end()
  return { commands: out, joins }
}

const everyCommand = (script: string) => shellCommands(script).commands
/** The commands a shell script ran for certain: all of a plain sequence, all of one `&&` chain that succeeded, and
 * otherwise only the first, because a branch may not have been taken. */
const certainCommands = (succeeded: boolean) => (script: string) => {
  const { commands, joins } = shellCommands(script)
  const branches = joins.has("||") || joins.has("(") || (joins.has("&&") && !(succeeded && !joins.has(";")))
  return branches ? commands.slice(0, 1) : commands
}

/** The attn commands among those `pick` takes from a shell script, each as the words after attn, past env assignments,
 * keywords and wrappers such as timeout, and inside bash -c. */
function attnWords(script: string, pick: (script: string) => string[][] = everyCommand): string[][] {
  return pick(script).flatMap((words) => {
    let i = 0
    while (i < words.length) {
      const word = words[i]!
      if (/^\w+=/.test(word) || KEYWORDS.has(word)) i++
      else if (word === "timeout" || word === "env" || word === "xargs") {
        i++
        while (i < words.length && /^(-|\d+[smh]?$)/.test(words[i]!)) i++
      } else break
    }
    const [head = "", ...rest] = words.slice(i)
    if (/(^|\/)(ba|z|da)?sh$/.test(head)) {
      const c = rest.findIndex((w) => /^-\w*c$/.test(w))
      return c >= 0 ? attnWords(rest[c + 1] ?? "", pick) : []
    }
    return /(^|\/)attn$/.test(head) || /^\$\{?ATTN_WRAPPER_PATH/.test(head) ? [rest] : []
  })
}

/** The catalog command a CLI line runs: its longest leading words that name one. */
function commandOf(words: string[]): string {
  for (let n = Math.min(words.length, 4); n > 0; n--) {
    const command = words.slice(0, n).join(" ")
    if (COMMANDS.has(command)) return command
  }
  return words.slice(0, 1).join(" ")
}

const ESCAPES: Record<string, string> = { n: "\n", t: "\t", r: "\r" }
/** The strings a code-mode script gives a shell: a cmd or command property, a value assigned to a name like one, and a
 * $ template. */
function shellStrings(code: string): string[] {
  const out: string[] = []
  for (let i = 0; i < code.length; i++) {
    const quote = code[i]!
    if (!"\"'\x60".includes(quote)) continue
    const before = code.slice(Math.max(0, i - 60), i)
    let value = ""
    for (i++; i < code.length && code[i] !== quote; i++) {
      if (code[i] === "\\") {
        const c = code[++i] ?? ""
        value += ESCAPES[c] ?? c
      } else value += code[i]
    }
    if (/(\b(cmd|command|script)["']?\s*[:=]\s*(\[.*)?|\$\(?)$/i.test(before)) out.push(value)
  }
  return out
}

const shellWords = (words: string[]) => words.map((w) => "'" + String(w).replace(/'/g, "'\\''") + "'").join(" ")

function shellScripts(tool: string, text: string): string[] {
  let args: any
  try {
    args = JSON.parse(text)
  } catch {
    args = { code: text }
  }
  if (SCRIPT_TOOLS.has(tool)) return shellStrings(String(args?.code ?? text))
  if (!SHELL_TOOLS.has(tool)) return []
  const command = args?.command ?? args?.cmd ?? args?.code
  return [Array.isArray(command) ? shellWords(command) : String(command ?? "")]
}

const CLI_FAILED = /(^|\n|\\n)usage: attn [^\n]*\(attn [^\n]* --help\)|^Exit code [1-9]|exit_code\\?":\s*[1-9]|Process exited with code [1-9]|\[exit [1-9]/
const HELP = (word: string) => word === "--help" || word === "-h"

/** One line of the daemon's log for a tool call this session made, in order. */
export type ToolOutcome = { at: number; command: string; refused?: string }
const TOOL_LOG = /agent tools: (\w+) by .* \(session ([^)]+)\): (?:ok|refused ([\s\S]*))$/
export const toolOutcomes = (log: string, session: string): ToolOutcome[] =>
  log.split("\n").flatMap((line) => {
    const m = line.match(TOOL_LOG)
    const at = Date.parse(line.match(/^\[([^\]]+)\]/)?.[1] ?? "")
    return m && m[2] === session ? [{ at: Number.isNaN(at) ? 0 : at, command: m[1]!.replace(/_/g, " "), ...(m[3] ? { refused: m[3] } : {}) }] : []
  })

/** One `attn` command that the recorder in `bench.ts` saw run, with its exit code once it ended. A shell of a harness
 * whose server runs every conversation carries the conversation; the others carry the terminal. */
export type CliRun = { at: number; terminal: string; conversation: string; argv: string[]; code?: number; blocked?: true }
export function cliRuns(log: string): CliRun[] {
  const args = new Map<string, string>()
  const runs = new Map<string, CliRun>()
  for (const line of log.split("\n")) {
    const [kind, id = "", a = "", terminal = "", conversation = ""] = line.split(" ")
    if (kind === "arg") args.set(id, (args.get(id) ?? "") + a)
    if (kind === "start") {
      const argv = Buffer.from(args.get(id) ?? "", "base64").toString("utf8").split("\0").slice(0, -1)
      runs.set(id, { at: Number(a.replace(",", ".")) * 1000, terminal: terminal === "-" ? "" : terminal, conversation: conversation === "-" ? "" : conversation, argv })
    }
    const ended = kind === "end" && runs.get(id)
    if (ended) {
      ended.code = Number(a)
      if (terminal === "blocked") ended.blocked = true
    }
  }
  return [...runs.values()]
}

/** What the home recorded for a session: every tool call its daemon logged and every `attn` command it ran. */
export type Records = { tools: ToolOutcome[]; cli: CliRun[] }

export const blockedRuns = (records?: Records) => (records?.cli ?? []).filter((r) => r.blocked).length

const CLOSED_PIPE = 141
const cliCall = (argv: string[], code?: number): Call => {
  const help = argv.some(HELP)
  const failed = code !== undefined && code !== 0 && code !== CLOSED_PIPE && !help
  return { via: "cli", command: commandOf(argv), text: argv.join(" "), ...(help ? { help } : {}), ...(failed ? { refused: code === 2 ? "usage: attn exited 2" : `attn exited ${code}` } : {}) }
}

const withoutComments = (code: string) => code.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[\s;{}()])\/\/.*$/gm, "$1")

const DENIED = /^Permission (for this action|to use )/
/** Whether a tool's result is Claude Code's refusal to run it, by its auto mode classifier or a permission rule. */
const denied = (result: any) => result?.is_error === true && DENIED.test(String(result.text ?? ""))

/** The attn calls a transcript mentions, in order. A call that shares its shell command or script with other work has no
 * refusal here, because the transcript cannot say which part failed. */
function mentioned(events: any[]): Call[] {
  const calls: Call[] = []
  const results = new Map(events.filter((e) => e.kind === "tool_result").map((e) => [e.tool_call_id, e]))
  for (const e of events) {
    if (e.kind !== "tool_call") continue
    const name = String(e.tool_name ?? "")
    const text = String(e.text ?? "")
    const result = results.get(e.tool_call_id)
    if (denied(result)) continue
    const answer = String(result?.text ?? "")
    const direct = name.match(TOOL_NAME)
    if (direct) {
      calls.push({ via: "tool", command: direct[1]!.replace(/_/g, " "), text, ...(result?.is_error ? { refused: answer } : {}) })
      continue
    }
    if (SCRIPT_TOOLS.has(name)) for (const m of withoutComments(text).matchAll(TOOL_IN_SCRIPT)) calls.push({ via: "tool", command: m[1]!.replace(/_/g, " "), text })
    const scripts = shellScripts(name, text)
    for (const script of scripts) {
      const commands = attnWords(script)
      const alone = scripts.length === 1 && commands.length === 1 && everyCommand(script).length === 1
      const failed = CLI_FAILED.test(answer) || (result?.is_error && !SCRIPT_TOOLS.has(name))
      for (const words of commands) {
        const call = cliCall(words)
        calls.push({ ...call, ...(alone && !call.help && failed ? { refused: answer.slice(0, 600) } : {}) })
      }
    }
  }
  return calls
}

/** The attn calls of a session, one per execution. At the home they are what was recorded, and the transcript supplies
 * a tool call's arguments. Commands the sandbox blocked reached no attn, so they are not among them. On an outpost, which records nothing here, they are what the transcript mentions. */
export function attnCalls(events: any[], records?: Records): Call[] {
  const seen = mentioned(events)
  if (!records) return seen
  const used = new Map<string, number>()
  const tools = records.tools.map((t) => {
    const texts = seen.filter((c) => c.via === "tool" && c.command === t.command)
    const n = used.get(t.command) ?? 0
    used.set(t.command, n + 1)
    return { at: t.at, call: { via: "tool" as const, command: t.command, text: texts[Math.min(n, texts.length - 1)]?.text ?? "", ...(t.refused ? { refused: t.refused } : {}) } }
  })
  return [...tools, ...records.cli.filter((r) => !r.blocked).map((r) => ({ at: r.at, call: cliCall(r.argv, r.code) }))].sort((a, b) => a.at - b.at).map((x) => x.call)
}

/** A shell command that a harness's own record shows it ran, and whether it exited 0. */
export type Ran = { script: string; succeeded: boolean }

/** The shell commands a transcript shows ran. One without a result, or whose error is the harness's refusal rather than
 * an exit code, never ran, and a code-mode script does not say which of its commands did. */
export function shellRuns(events: any[]): Ran[] {
  const results = new Map(events.filter((e) => e.kind === "tool_result").map((e) => [e.tool_call_id, e]))
  return events.flatMap((e) => {
    const result = e.kind === "tool_call" && SHELL_TOOLS.has(e.tool_name) && results.get(e.tool_call_id)
    if (!result) return []
    const exitedNonZero = CLI_FAILED.test(String(result.text ?? ""))
    if (result.is_error && !exitedNonZero) return []
    return shellScripts(e.tool_name, String(e.text ?? "")).map((script) => ({ script, succeeded: !exitedNonZero }))
  })
}

/** The shell commands a Codex rollout shows ran since `from`: one item each, also for a command in a code-mode script. */
export const codexRuns = (rollout: string, from: number): Ran[] =>
  rollout.split("\n").flatMap((line) => {
    if (!line.includes('"CommandExecution"')) return []
    const { timestamp, payload } = JSON.parse(line)
    const { type, command, exit_code } = payload?.item ?? {}
    if (type !== "CommandExecution" || typeof exit_code !== "number" || exit_code < 0 || Date.parse(timestamp) < from) return []
    return [{ script: /^-\w*c$/.test(command[1] ?? "") ? command[2] : shellWords(command), succeeded: exit_code === 0 }]
  })

/** The attn commands that ran for certain more often than the recorder saw them: the session ran another attn. */
export function unrecorded(ran: Ran[], records: Records): string[] {
  const left = records.cli.map((r) => commandOf(r.argv))
  return ran.flatMap((r) => attnWords(r.script, certainCommands(r.succeeded))).flatMap((words) => {
    const command = commandOf(words)
    const i = left.indexOf(command)
    if (i >= 0) left.splice(i, 1)
    return i >= 0 ? [] : [command]
  })
}

/** How long Codex waited to send a conversation's requests again, from the bodies of its log's retry warnings. */
export const retryWaitMs = (warnings: string[]) => {
  const UNITS: Record<string, number> = { s: 1000, ms: 1, "µs": 0.001, ns: 0.000001 }
  return warnings.reduce((total, body) => {
    const m = body.match(/retrying sampling request \(\d+\/\d+ in ([\d.]+)(ns|µs|ms|s)\)/)
    return m ? total + Number(m[1]) * UNITS[m[2]!]! : total
  }, 0)
}

const TOOL_IN_REPL = /\btools\.(?:mcp__attn\w*?__)?(\w+)/g
/** Whether a call holds an attn command or tool: one of attn's tools, a shell command, or a Claude Code REPL script of either. */
function holdsAttn(name: string, text: string): boolean {
  if (TOOL_NAME.test(name)) return true
  if (name !== "REPL") return shellScripts(name, text).some((script) => attnWords(script).length > 0)
  const code = withoutComments(shellScripts("Bash", text)[0] ?? "")
  return shellStrings(code).some((script) => attnWords(script).length > 0) || [...code.matchAll(TOOL_IN_REPL)].some((m) => COMMANDS.has(m[1]!.replace(/_/g, " ")))
}

/** The calls holding an attn command or tool that Claude Code refused to run, as `denied` says. They reached no attn. */
const deniedCalls = (events: any[]) => {
  const results = new Map(events.filter((e) => e.kind === "tool_result").map((e) => [e.tool_call_id, e]))
  return events.filter((e) => e.kind === "tool_call" && denied(results.get(e.tool_call_id)) && holdsAttn(String(e.tool_name ?? ""), String(e.text ?? ""))).length
}

export const isHelp = (call: Call) => call.help === true
export const isCommand = (call: Call, commands: string | string[]) => [commands].flat().includes(call.command)
const USAGE_CODES = new Set(["usage"])
for (const name of readdirSync(CATALOG)) for (const m of readFileSync(join(CATALOG, name), "utf8").matchAll(/code: (\w+), usage: true/g)) USAGE_CODES.add(m[1]!)
/** A refusal because the arguments did not fit: a tool's refusal starts with its code, and the CLI prints a usage line for
 * the same codes. */
export const isUsageRefusal = (call: { refused?: string }) =>
  call.refused !== undefined && (USAGE_CODES.has(call.refused.match(/^(\w+):/)?.[1] ?? "") || /(^|\n|\\n)usage:/.test(call.refused))

export const reached = (calls: Call[], commands: string | string[], mentions?: RegExp) =>
  calls.some((c) => isCommand(c, commands) && !isHelp(c) && c.refused === undefined && (mentions === undefined || mentions.test(c.text)))

export const STAYS = /--stay\b|\bstay\\?["']?\s*:\s*true/

const SEARCH_TOOLS = new Set(["ToolSearch", "tool_search", "search_tool_bm25"])

/** Tool calls and refusals count the daemon's log, where a script's loop counts each call; on an outpost, whose log is
 * on that machine, they count the transcript. */
export type Metrics = {
  turns: number
  toolCalls: number
  toolRefusals: number
  cliCalls: number
  cliFailures: number
  sandboxBlocked: number
  harnessDenied: number
  usageRefusals: number
  searches: number
  cliHelpReads: number
  referenceReads: number
}

export function metrics(events: any[], calls: Call[], blocked = 0): Metrics {
  let turns = 0
  let inAssistant = false
  let searches = 0
  for (const e of events) {
    const assistant = e.kind === "tool_call" || e.role === "assistant"
    if (assistant && !inAssistant) turns++
    inAssistant = assistant
    if (e.kind === "tool_call" && (SEARCH_TOOLS.has(e.tool_name) || /searchTools\(|describeTool\(|ALL_TOOLS/.test(String(e.text ?? "")))) searches++
  }
  const tool = calls.filter((c) => c.via === "tool")
  const cli = calls.filter((c) => c.via === "cli")
  return {
    turns,
    toolCalls: tool.length,
    toolRefusals: tool.filter((c) => c.refused !== undefined).length,
    cliCalls: cli.length,
    cliFailures: cli.filter((c) => c.refused !== undefined).length,
    sandboxBlocked: blocked,
    harnessDenied: deniedCalls(events),
    usageRefusals: [...tool, ...cli].filter(isUsageRefusal).length,
    searches,
    cliHelpReads: calls.filter(isHelp).length,
    referenceReads: calls.filter((c) => !isHelp(c) && isCommand(c, ["skill", "guide"])).length,
  }
}
