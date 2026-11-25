import { spawnSync } from "node:child_process"
import { readFileSync } from "node:fs"
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
  /** Runs the instance's attn in this run's profile; throws with the output when it exits non-zero. */
  attn: (args: string[], opts?: { stdin?: string; session?: string; cwd?: string }) => string
  /** Runs a command that takes no profile or session, such as a settings read. */
  instance: (args: string[]) => string
  attnJson: (args: string[], opts?: { stdin?: string; session?: string }) => any
  /** A shell command in the agent's folder, on the machine the agent runs on. */
  sh: (command: string, stdin?: string) => { out: string; code: number | null }
  spawnShell: (label: string) => Promise<string>
  /** This profile's live sessions, as `attn agent list --json` shows them. */
  sessions: () => { id: string; directory: string; state: string; label: string }[]
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

const TOOL_NAME = /^mcp__attn\w*?__(\w+)$/
/** A tool called in a code-mode script, as tools.mcp__attn__seed_note(…) or tools["mcp__attn__seed_note"](…). */
const TOOL_IN_SCRIPT = /mcp__attn\w*?__(\w+)["'\x60]?\]?\s*\(/g
const SHELL_TOOLS = new Set(["Bash", "bash", "shell", "local_shell", "exec_command", "unified_exec"])
/** Code-mode scripts whose inner calls the transcript does not list. Claude Code's REPL lists its own. */
const SCRIPT_TOOLS = new Set(["exec", "codemode"])

const catalog: { tools: { name: string }[] } = JSON.parse(readFileSync(join(import.meta.dir, "../../../internal/daemon/agent_tools_generated.json"), "utf8"))
const COMMANDS = new Set(catalog.tools.map((t) => t.name.replace(/_/g, " ")))

const KEYWORDS = new Set(["then", "do", "else", "elif", "if", "while", "until", "!", "{", "time", "exec", "command", "sudo", "nohup"])

const HEREDOC = /^<<-?(?!<)[ \t]*(['"]?)([\w.-]+)\1/
/** Where the script goes on after a heredoc body that starts at `from`. */
function afterHeredoc(script: string, from: number, tag: string) {
  const close = script.slice(from).match(new RegExp("^[ \\t]*" + tag.replace(/\./g, "\\.") + "[ \\t]*$", "m"))
  return close ? from + close.index! + close[0].length : script.length
}

/** The commands a shell script runs, as words: quotes resolved, heredoc bodies and redirections left out, and the
 * commands inside $( ) and backquotes listed before the command that holds them. */
function shellCommands(script: string): string[][] {
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
    out.push(...shellCommands(script.slice(from, i)))
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
      end()
      i = heredocs.splice(0).reduce((at, tag) => afterHeredoc(script, at, tag), i + 1) - 1
    } else if (";|&".includes(c) || (word === undefined && "()".includes(c)) || c === ")") end()
    else word = (word ?? "") + c
  }
  end()
  return out
}

/** The attn commands a shell script runs, each as the words after attn, past env assignments, keywords and wrappers such
 * as timeout, and inside bash -c. */
function attnWords(script: string): string[][] {
  return shellCommands(script).flatMap((words) => {
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
      return c >= 0 ? attnWords(rest[c + 1] ?? "") : []
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
  return Array.isArray(command) ? [command.map((w) => "'" + String(w).replace(/'/g, "'\\''") + "'").join(" ")] : [String(command ?? "")]
}

const CLI_USAGE = /(^|\n|\\n)usage: attn [^\n]*\(attn [^\n]* --help\)/
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
export type CliRun = { at: number; terminal: string; conversation: string; argv: string[]; code?: number }
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
    if (ended) ended.code = Number(a)
  }
  return [...runs.values()]
}

/** What the home recorded for a session: every tool call its daemon logged and every `attn` command it ran. */
export type Records = { tools: ToolOutcome[]; cli: CliRun[] }

const CLOSED_PIPE = 141
const cliCall = (argv: string[], code?: number): Call => {
  const help = argv.some(HELP)
  const failed = code !== undefined && code !== 0 && code !== CLOSED_PIPE && !help
  return { via: "cli", command: commandOf(argv), text: argv.join(" "), ...(help ? { help } : {}), ...(failed ? { refused: code === 2 ? "usage: attn exited 2" : `attn exited ${code}` } : {}) }
}

const withoutComments = (code: string) => code.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[\s;{}()])\/\/.*$/gm, "$1")

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
    const answer = String(result?.text ?? "")
    const direct = name.match(TOOL_NAME)
    if (direct) {
      calls.push({ via: "tool", command: direct[1]!.replace(/_/g, " "), text, ...(result?.is_error ? { refused: answer } : {}) })
      continue
    }
    if (SCRIPT_TOOLS.has(name)) for (const m of withoutComments(text).matchAll(TOOL_IN_SCRIPT)) calls.push({ via: "tool", command: m[1]!.replace(/_/g, " "), text })
    for (const script of shellScripts(name, text)) {
      const commands = attnWords(script)
      const alone = commands.length === 1 && shellCommands(script).length === 1 && !SCRIPT_TOOLS.has(name)
      for (const words of commands) {
        const call = cliCall(words)
        calls.push({ ...call, ...(alone && !call.help && (result?.is_error || CLI_USAGE.test(answer)) ? { refused: answer.slice(0, 600) } : {}) })
      }
    }
  }
  return calls
}

/** The attn calls of a session, one per execution. At the home they are what was recorded, and the transcript supplies
 * a tool call's arguments. On an outpost, which records nothing here, they are what the transcript mentions. */
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
  return [...tools, ...records.cli.map((r) => ({ at: r.at, call: cliCall(r.argv, r.code) }))].sort((a, b) => a.at - b.at).map((x) => x.call)
}

/** The attn commands a transcript shows more often than the recorder saw them: the session ran another attn, or the
 * harness refused the command before it ran. */
export function unrecorded(events: any[], records: Records): string[] {
  const left = records.cli.map((r) => commandOf(r.argv))
  return mentioned(events).flatMap((c) => {
    if (c.via !== "cli") return []
    const i = left.indexOf(c.command)
    if (i >= 0) left.splice(i, 1)
    return i >= 0 ? [] : [c.command]
  })
}

export const isHelp = (call: Call) => call.help === true
export const isCommand = (call: Call, commands: string | string[]) => [commands].flat().includes(call.command)
export const isUsageRefusal = (call: { refused?: string }) => call.refused !== undefined && /(^|\n|\\n)usage:/.test(call.refused)

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
  usageRefusals: number
  searches: number
  cliHelpReads: number
  referenceReads: number
}

export function metrics(events: any[], calls: Call[]): Metrics {
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
    usageRefusals: [...tool, ...cli].filter(isUsageRefusal).length,
    searches,
    cliHelpReads: calls.filter(isHelp).length,
    referenceReads: calls.filter((c) => !isHelp(c) && isCommand(c, ["skill", "guide"])).length,
  }
}
