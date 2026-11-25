import { Database } from "bun:sqlite"
import { spawn, spawnSync } from "node:child_process"
import { appendFileSync, constants, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs"
import { homedir, tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { ASK_TASKS } from "./asks.ts"
import { GRID_PROBES, PROBES } from "./probes.ts"
import { HARDER_TASKS, UNGROUPED_TASKS, TASKS } from "./tasks.ts"
import { attnCalls, blockedRuns, type Call, type Check, claudeRequests, cliRuns, codexRequests, codexRuns, isCommand, metrics, type Metrics, type Records, type Requests, retryWaitMs, run, shellRuns, takesProfile, type Task, toolOutcomes, unrecorded, type World } from "./world.ts"

const repository = resolve(import.meta.dir, "../../..")
/** Tripwires. The prototype's runs took 13 to 38 s and C16's under a minute; a run that reaches these is stuck. */
const LAUNCH_DEADLINE_MS = 3 * 60_000
const RUN_DEADLINE_MS = 10 * 60_000
/** Tripwire for a delegation that attn accepted: its agent starts as any session does. */
const DELEGATE_DEADLINE_MS = LAUNCH_DEADLINE_MS
/** Tripwire. Adding an outpost builds attn for it first, which took 4m25s on a cold cache. */
const OUTPOST_ADD_MS = 15 * 60_000
const POLL_MS = 500

const arg = (name: string) => {
  const i = process.argv.indexOf(`--${name}`)
  return i >= 0 ? process.argv[i + 1] : undefined
}
const flag = (name: string) => process.argv.includes(`--${name}`)
const list = (value: string | undefined, fallback: string) => (value ?? fallback).split(",").filter(Boolean)

const grid = flag("grid")
const builtAttn = resolve(arg("attn") ?? join(repository, "attn"))
if (!existsSync(builtAttn)) throw new Error(`${builtAttn} does not exist: run make build, or pass --attn <binary>`)
const withoutAttnEnv = () => Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^(ATTN_|CLAUDE|CODEX_|SIGMA_)/.test(key)))

function newInstance(requested: string | undefined) {
  const usage = "pass --instance <name>: a new instance this run creates, changes and cleans"
  if (!requested) throw new Error(usage)
  const attn = (args: string[]) => spawnSync(builtAttn, args, { env: withoutAttnEnv(), encoding: "utf8" })
  const resolved = attn(["instance", "resolve", "--instance", requested])
  if (resolved.status !== 0) throw new Error(`${usage}: ${resolved.stderr.trim()}`)
  const name: string = JSON.parse(resolved.stdout).instance
  if (name === "" || name === "dev") throw new Error(`${usage}: --instance ${requested} is attn's ${name || "default"} instance`)
  const listed = attn(["instance", "list", "--json"])
  if (listed.status !== 0) throw new Error(`attn instance list --json exited ${listed.status}: ${listed.stderr.trim()}`)
  if (JSON.parse(listed.stdout).instances.some((i: { instance: string }) => i.instance === name))
    throw new Error(`${usage}: instance ${name} already exists, so the bench leaves it alone. Pick a new name, or clean it with: attn instance clean ${name}`)
  return name
}
const instance = newInstance(arg("instance"))
const toolHome = arg("tool-home") && resolve(arg("tool-home")!)
/** The tool home on each outpost, as ssh resolves it there: an absolute path, because the outpost's own HOME may differ. */
const outpostToolHome = arg("outpost-tool-home")
if (outpostToolHome && !/^\/[\w./-]+$/.test(outpostToolHome)) throw new Error(`--outpost-tool-home takes an absolute path on the outpost, got ${outpostToolHome}`)
const parallel = Number(arg("parallel") ?? 2)
const keep = flag("keep")

const ALL = [...TASKS, ...HARDER_TASKS, ...UNGROUPED_TASKS, ...ASK_TASKS, ...PROBES]
const groups: Record<string, Task[]> = { probes: PROBES, asks: ASK_TASKS, harder: HARDER_TASKS, grid: [...TASKS, ...HARDER_TASKS, ...ASK_TASKS, ...GRID_PROBES] }
const taskIds = list(arg("tasks") ?? (grid ? "grid" : undefined), TASKS.map((t) => t.id).join(",")).flatMap((id) => groups[id]?.map((t) => t.id) ?? [id])
/** Runs per cell of a task: in the grid, three of each task and one of each probe. */
const runsOf = (task: Task) => Number(arg("runs") ?? (grid && !PROBES.includes(task) ? 3 : 1))
const tasks = taskIds.map((id) => ALL.find((t) => t.id === id) ?? (() => { throw new Error(`no task ${id}; tasks: ${ALL.map((t) => t.id).join(", ")}`) })())

/** A harness as a cell names it: the agent attn launches, whether Claude Code's code mode is on, and the model. */
type Harness = { name: string; agent: "claude" | "codex" | "sigma"; codeMode: boolean; model: string; effort: string }
const harnesses: Harness[] = list(arg("harness"), "claude,codex").map((spec) => {
  const [name, pin = ""] = spec.split("@") as [string, string?]
  const [model = "", effort = ""] = pin.split(":")
  const agent = name === "claude-code-mode" ? "claude" : name
  if (agent !== "claude" && agent !== "codex" && agent !== "sigma") throw new Error(`no harness ${name}: claude, claude-code-mode, codex or sigma, each with an optional @model or @model:effort`)
  return { name: spec, agent, codeMode: name === "claude-code-mode", model, effort }
})
const outposts = list(arg("outpost"), "").map((spec) => {
  const [name, ssh] = spec.split("=")
  if (!name || !ssh) throw new Error(`--outpost takes <outpost name>=<ssh destination>, got ${spec}`)
  return { name, ssh }
})
const tools = list(arg("tools"), grid ? "on,off" : "on")

type Place = { name: string; ssh?: string }
type Cell = { harness: Harness; tools: string; place: Place; id: string }
const cells: Cell[] = []
const places: Place[] = flag("no-home") ? outposts : [{ name: "home" }, ...outposts]
if (places.length === 0) throw new Error("--no-home runs the cells on the outposts only: pass --outpost")
for (const place of places)
  for (const harness of harnesses) {
    if (place.name !== "home" && (harness.codeMode || harnesses.find((h) => h.agent === harness.agent && !h.codeMode) !== harness)) continue
    for (const t of tools) cells.push({ harness, tools: t, place, id: `${harness.name} tools-${t} ${place.name}`.replace(/[^\w.@ -]/g, "_") })
  }
const homeOnly = tasks.filter((t) => t.stage && !t.stagesOnOutposts)
if (cells.some((c) => c.place.ssh) && homeOnly.length > 0) throw new Error(`${homeOnly.map((t) => t.id).join(", ")} stage a folder on this machine, so they run on the home only`)
const codexDelegates = (onOutpost: boolean) => tasks.some((t) => t.id === "msg-delegate") && cells.some((c) => c.harness.agent === "codex" && Boolean(c.place.ssh) === onOutpost)
if ((!toolHome && codexDelegates(false)) || (!outpostToolHome && codexDelegates(true)))
  throw new Error("msg-delegate saves its delegate's folder as trusted in the Codex home, so with codex it runs only with --tool-home <dir>, and on an outpost with --outpost-tool-home <dir>, never on your own ~/.codex")
cells.sort((a, b) => Number(a.harness.codeMode) - Number(b.harness.codeMode))

const stamp = new Date().toISOString().replace(/[:.]/g, "-")
const out = join(import.meta.dir, "runs", stamp)
const dataDir: string = JSON.parse(run(builtAttn, ["instance", "resolve", "--instance", instance], withoutAttnEnv()).stdout).dataDir
const workRoot = mkdtempSync(join(realpathSync(tmpdir()), "attn-agent-bench-"))
mkdirSync(out, { recursive: true })

/** The instance runs from a folder where `attn` is a recorder: it notes each command an agent runs, with its arguments
 * and exit code, and runs the real CLI beside it. A harness's own hook calls pass through unrecorded. It writes to a pipe because Codex's
 * read-only sandbox refuses a file, in lines short enough for the pipe to keep whole. A command's id is random, because a
 * sandbox gives each command the same few process ids. It reads what the command says on stderr, where attn names a sandbox
 * that keeps it from its socket. */
const bash = spawnSync("bash", ["-c", "command -v bash"], { encoding: "utf8" }).stdout.trim()
const cliLog = join(workRoot, "attn-cli.log")
const recorder = join(workRoot, "attn-cli.pipe")
const binaries = join(workRoot, "bin")
const attnBinary = join(binaries, "attn-cli")
mkdirSync(binaries)
copyFileSync(builtAttn, attnBinary, constants.COPYFILE_FICLONE)
copyFileSync(join(dirname(builtAttn), "attnd"), join(binaries, "attnd"), constants.COPYFILE_FICLONE)
writeFileSync(cliLog, "")
if (spawnSync("mkfifo", [recorder]).status !== 0) throw new Error(`mkfifo ${recorder} failed`)
const recording = spawn("bash", ["-c", `exec cat <>'${recorder}' >>'${cliLog}'`], { stdio: "ignore" })
recording.unref()
process.on("exit", () => recording.kill())
writeFileSync(
  join(binaries, "attn"),
  `#!${bash}
case "$1" in _*) exec '${attnBinary}' "$@" ;; esac
id=\${SRANDOM:?}.$SRANDOM
argv=$([ $# -eq 0 ] || printf '%s\\0' "$@" | base64 | tr -d '\\n')
note() { { printf '%s\\n' "$*" 1<>'${recorder}'; } 2>/dev/null; }
for ((i = 0; i < \${#argv}; i += 3000)); do note arg "$id" "\${argv:i:3000}"; done
note start "$id" "$EPOCHREALTIME" "\${ATTN_TERMINAL_ID:--}" "\${CODEX_THREAD_ID:-\${SIGMA_ROOT_CONVERSATION:--}}"
exec 3>&1 4>&2
blocked=$(
  '${attnBinary}' "$@" 2>&1 >&3 3>&- 4>&- | while IFS= read -r line || [ -n "$line" ]; do
    printf '%s\\n' "$line" >&4
    [[ $line == *"the sandbox blocks attn's socket"* ]] && printf blocked
  done
  exit "\${PIPESTATUS[0]}"
)
code=$?
[ "$code" = 0 ] && blocked=
note end "$id" "$code" "\${blocked:+blocked}"
exit $code
`,
  { mode: 0o755 },
)

/** The recorder must be proven before a run depends on it: it needs a bash 5.1 for its clock and random ids, and the pipe must reach the log. */
const recorded = () => {
  spawnSync(join(binaries, "attn"), ["version"], { env: withoutAttnEnv(), stdio: "ignore" })
  return cliRuns(readFileSync(cliLog, "utf8")).some((r) => r.at > 0 && r.code === 0)
}
for (let waited = 0; !recorded(); waited += 50) {
  if (waited > 5_000) throw new Error(`the recorder at ${join(binaries, "attn")} logs no attn command with ${bash}: it needs bash 5.1 or newer on PATH, and a system with mkfifo`)
  await new Promise((r) => setTimeout(r, 50))
}

const baseEnv = (): NodeJS.ProcessEnv => {
  return {
    ...withoutAttnEnv(),
    ATTN_INSTANCE: instance,
    ATTN_HEADLESS_TASKS: "off",
    ...(toolHome ? { ATTN_TOOL_HOME: toolHome, CLAUDE_CONFIG_DIR: join(toolHome, ".claude"), CODEX_HOME: join(toolHome, ".codex"), SIGMA_HOME: join(toolHome, ".sigma") } : {}),
  }
}
let daemonEnv = baseEnv()

function attnRaw(args: string[], opts: { stdin?: string; cwd?: string; env?: NodeJS.ProcessEnv } = {}) {
  const r = run(attnBinary, args, opts.env ?? daemonEnv, { stdin: opts.stdin, cwd: opts.cwd ?? repository })
  if (r.status !== 0) throw new Error(`attn ${args.join(" ")} exited ${r.status}: ${(r.stdout + r.stderr).trim().slice(0, 600)}`)
  return r.stdout
}
const json = (text: string) => JSON.parse(text.slice(text.search(/[[{]/)))

function column(path: string, sql: string, ...values: (string | number)[]): string[] {
  const db = new Database(path, { readonly: true })
  try {
    return db.query(sql).values(...values).map((row) => String(row[0]))
  } finally {
    db.close()
  }
}

/** The harness's own id for the conversation a session holds, which its shells carry when one server runs them all. */
const conversationOf = (session: string) => column(join(dataDir, "attn.db"), "SELECT resume_session_id FROM sessions WHERE id = ?", session)[0] ?? ""

const codexHome = toolHome ? join(toolHome, ".codex") : join(homedir(), ".codex")
const found = (folder: string, name: string, ...options: string[]) => spawnSync("find", [folder, ...options, "-name", name], { encoding: "utf8" }).stdout.split("\n").filter(Boolean)
const codexRollout = (conversation: string) =>
  ["sessions", "archived_sessions"].flatMap((folder) => found(join(codexHome, folder), `rollout-*-${conversation}.jsonl`)).map((path) => readFileSync(path, "utf8")).join("\n")

/** How long Codex waited since `from` to send a conversation's requests again after the provider refused them, which its
 * log names and its rollout does not. */
const codexRetryWaitMs = (conversation: string, from: number) =>
  retryWaitMs(
    found(codexHome, "logs_*.sqlite", "-maxdepth", "1").flatMap((path) =>
      column(path, "SELECT feedback_log_body FROM logs WHERE target = 'codex_core::responses_retry' AND thread_id = ? AND ts >= ?", conversation, Math.floor(from / 1000)),
    ),
  )

const wsctlBinary = join(out, "wsctl")
function wsctl(args: string[], timeoutMs?: number) {
  const r = run(wsctlBinary, args, daemonEnv, { timeoutMs })
  if (r.status !== 0) throw new Error(`wsctl ${args.join(" ")}: ${r.stderr.trim().slice(-400)}`)
  return r.stdout
}

async function startDaemon(codeMode: boolean) {
  const env = { ...baseEnv(), ...(codeMode ? { CLAUDE_CODE_EXPERIMENTAL_CODE_MODE: "1" } : {}) }
  if (env.CLAUDE_CODE_EXPERIMENTAL_CODE_MODE === daemonEnv.CLAUDE_CODE_EXPERIMENTAL_CODE_MODE && started) return
  if (started) attnRaw(["daemon", "stop"])
  daemonEnv = env
  attnRaw(["daemon", "ensure"])
  if (!started && outposts.length > 0) {
    setting("outposts_enabled", "true")
    for (const o of outposts) {
      const added = json(wsctl(["outposts", "--add", o.name, "--destination", o.ssh, "--until", "connected"], OUTPOST_ADD_MS).trim().split("\n").at(-1)!)
      addedOutposts.push(added.outposts.find((row: { name: string }) => row.name === o.name).id)
    }
    if (outpostToolHome) {
      setting("outposts_enabled", "false")
      for (const o of outposts) useOutpostToolHome(o, outpostToolHome)
      setting("outposts_enabled", "true")
      await outpostsConnected()
    }
  }
  started = true
}

/** attn refuses a session on an outpost until its profile's Notebook folder synced there once. */
async function afterFirstSync(start: () => void) {
  const deadline = Date.now() + LAUNCH_DEADLINE_MS
  for (;;)
    try {
      return start()
    } catch (error) {
      if (Date.now() > deadline || !/first sync of the Notebook folder/.test((error as Error).message)) throw error
      await wait(POLL_MS)
    }
}

const outpostRows = (): { id: string; state: string }[] => json(wsctl(["outposts"]).trim().split("\n").at(-1)!).outposts
const allConnected = (rows: { id: string; state: string }[]) => addedOutposts.every((id) => rows.find((row) => row.id === id)?.state === "connected")

async function outpostsConnected() {
  const deadline = Date.now() + LAUNCH_DEADLINE_MS
  for (;;) {
    const rows = outpostRows()
    if (allConnected(rows)) return
    if (Date.now() > deadline) throw new Error(`the outposts did not connect: ${JSON.stringify(rows)}`)
    await wait(POLL_MS)
  }
}

/** A dial in flight starts the daemon that a removal stops, so a removal waits for it. A setup that failed may have left
 * Outposts off, which refuses a removal. The wait is a tripwire: attn-linux connects within 2 s. */
const REMOVE_WAIT_MS = 30_000
function connectBeforeRemoving() {
  run(attnBinary, ["settings", "set", "outposts_enabled", "true"], daemonEnv)
  for (const deadline = Date.now() + REMOVE_WAIT_MS; Date.now() < deadline; Bun.sleepSync(POLL_MS)) {
    const r = run(wsctlBinary, ["outposts"], daemonEnv)
    if (r.status === 0 && allConnected(json(r.stdout.trim().split("\n").at(-1)!).outposts)) return
  }
  console.error(`the outposts did not connect before their removal: their daemons may keep running`)
}
let started = false
const addedOutposts: string[] = []

const setting = (name: string, value: string) => attnRaw(["settings", "set", name, value])

type Agent = { id: string; directory: string; state: string; label: string; profile: string }
const agents = (): Agent[] => json(attnRaw(["agent", "list", "--json"]))
const wait = (ms: number) => new Promise((r) => setTimeout(r, ms))
const screen = (session: string) => run(attnBinary, ["agent", "peek", session], daemonEnv).stdout

/** Waits for a new session to reach its prompt, answering a trust screen for the bench's own folder. Claude Code's drops the
 * keys it gets before it is ready, so each poll presses what the screen shows is next. Sigma's TUI
 * drops what is typed before its composer shows (s-rc8kfp), so a Sigma session also waits for the composer on screen. */
const SIGMA_COMPOSER = /❯ type a message/
async function atPrompt(session: string, agent: Harness["agent"]) {
  const deadline = Date.now() + LAUNCH_DEADLINE_MS
  let trusted = false
  while (Date.now() < deadline) {
    const shown = screen(session)
    if (/❯ No, exit\s+Yes, I trust this folder/.test(shown)) wsctl(["input", "--id", session, "--text", "\x1b[B"])
    else if (/❯ Yes, I trust this folder/.test(shown)) wsctl(["input", "--id", session, "--enter"])
    else if (!trusted && /Trust this folder\?|Do you trust the files in this folder\?/.test(shown)) {
      wsctl(["input", "--id", session, "--enter"])
      trusted = true
    } else {
      const me = agents().find((a) => a.id === session)
      if (me && ["idle", "waiting_input"].includes(me.state) && (agent !== "sigma" || SIGMA_COMPOSER.test(shown))) return
    }
    await wait(POLL_MS)
  }
  const state = agents().find((a) => a.id === session)?.state ?? "gone"
  throw new Error(`the session never reached its prompt: ${state}${agent === "sigma" ? ", or Sigma's composer never showed on screen (the bench waits for it until s-rc8kfp keeps Sigma's typeahead)" : ""}`)
}

/** When a turn ends, Claude Code may show a dialog, such as its offer to set up auto mode, over an idle composer or above
 * one. It takes the prompt's Enter as its choice. Esc declines it. */
const DIALOG = /· Esc to cancel\s*$/m
async function declineDialogs(session: string) {
  const deadline = Date.now() + LAUNCH_DEADLINE_MS
  for (let shown = screen(session); DIALOG.test(shown); shown = screen(session)) {
    if (Date.now() > deadline) throw new Error(`a dialog stays over the composer: ${shown.slice(-400)}`)
    wsctl(["input", "--id", session, "--text", "\x1b"])
    await wait(POLL_MS)
  }
}

const transcript = (session: string): any[] =>
  run(attnBinary, ["session", "transcript", session, "--json"], daemonEnv)
    .stdout.split("\n")
    .filter((line) => line.startsWith("{"))
    .map((line) => JSON.parse(line))

const isAnswer = (e: any) => e.role === "assistant" || e.kind === "tool_call"
const atRest = ["idle", "waiting_input", "pending_approval"]

/** Waits until the session is at rest and its transcript holds an answer after the first `before` events, or it closed
 * itself. At rest means back at its prompt, or waiting on the user in its harness's own question or approval prompt. */
async function settled(session: string, before: number): Promise<string> {
  const deadline = Date.now() + RUN_DEADLINE_MS
  let state = ""
  while (Date.now() < deadline) {
    await wait(POLL_MS)
    const me = agents().find((a) => a.id === session)
    if (!me) return "closed itself"
    state = me.state
    if (atRest.includes(state) && transcript(session).slice(before).some(isAnswer)) return state
  }
  throw new Error(`deadline ${RUN_DEADLINE_MS / 1000}s in state ${state}`)
}

/** The session's own tokens by kind, and `reviewer` for those Codex's auto-review spends beside it. `unpriced` when attn
 * has no price for a model, so `cost` leaves that model out. `incomplete` when attn could not measure all of it. */
type Usage = { input: number; cacheRead: number; cacheWrite: number; output: number; reviewer: number; cost: number; unpriced: boolean; incomplete: boolean; models: string[] }
const NO_USAGE: Usage = { input: 0, cacheRead: 0, cacheWrite: 0, output: 0, reviewer: 0, cost: 0, unpriced: false, incomplete: false, models: [] }
function readUsage(session: string): Usage {
  const entry = json(attnRaw(["session", "show", session, "--json"])).entry.usage
  const models: any[] = entry?.models ?? []
  const own = models.filter((m) => m.purpose === "agent")
  const sum = (rows: any[], f: (m: any) => number) => rows.reduce((a, m) => a + f(m), 0)
  return {
    input: sum(own, (m) => m.input_tokens),
    cacheRead: sum(own, (m) => m.cache_read_tokens),
    cacheWrite: sum(own, (m) => m.cache_write_5m_tokens + m.cache_write_1h_tokens + m.cache_write_unclassified_tokens),
    output: sum(own, (m) => m.output_tokens),
    reviewer: sum(models.filter((m) => m.purpose !== "agent"), (m) => m.total_tokens),
    cost: sum(models, (m) => m.cost_usd ?? 0),
    unpriced: models.some((m) => m.has_unpriced_usage),
    incomplete: entry?.measurement_incomplete === true,
    models: own.map((m) => m.model),
  }
}

/** attn reads usage after the harness writes it, so this waits until two reads agree on a measured total. The limit is a
 * tripwire: a harness that reports nothing leaves zeros, which the run's fault names. */
const USAGE_DEADLINE_MS = 15_000
async function steadyUsage(session: string): Promise<Usage> {
  const deadline = Date.now() + USAGE_DEADLINE_MS
  let last = readUsage(session)
  while (Date.now() < deadline) {
    await wait(1_000)
    const now = readUsage(session)
    if (now.output > 0 && JSON.stringify(now) === JSON.stringify(last)) return now
    last = now
  }
  return last
}

const since = (now: Usage, before: Usage): Usage => ({
  ...now,
  input: now.input - before.input,
  cacheRead: now.cacheRead - before.cacheRead,
  cacheWrite: now.cacheWrite - before.cacheWrite,
  output: now.output - before.output,
  reviewer: now.reviewer - before.reviewer,
  cost: now.cost - before.cost,
})

const onPlace = (place: Place, command: string) => (place.ssh ? run("ssh", [place.ssh, command], process.env) : run("bash", ["-c", command], daemonEnv)).stdout

/** What the harness recorded of the conversations in a run's folder: Claude Code, also under Sigma's Claude path, in its
 * conversation files, and Codex in its rollouts, here their tools and usage only. Sigma's own loop keeps no such file. */
function harnessRecord(cell: Cell, directory: string, startedAt: number): any[] {
  const newer = `-newermt @${Math.floor(startedAt / 1000) - 1}`
  if (cell.harness.agent === "codex") {
    const home = cell.place.ssh ? `${outpostToolHome ?? "~"}/.codex` : codexHome
    return onPlace(cell.place, `find ${home}/sessions ${home}/archived_sessions -name 'rollout-*.jsonl' ${newer} -print0 2>/dev/null | xargs -0 -r grep -lF '"cwd":"${directory}"' | xargs -r grep -hE '"type":"(session_meta|additional_tools|token_usage_record)"'`).split("\n").filter(Boolean).map((line) => JSON.parse(line))
  }
  const claudeHome = cell.place.ssh ? outpostToolHome : toolHome
  const home = claudeHome ? join(claudeHome, ".claude") : "~/.claude"
  const folder = directory.replace(/[^a-zA-Z0-9]/g, "-")
  return onPlace(cell.place, `find ${home}/projects/${folder} -maxdepth 1 -name '*.jsonl' ${newer} -print0 2>/dev/null | xargs -0 -r cat`).split("\n").filter((line) => line.startsWith("{")).map((line) => JSON.parse(line))
}

/** Whether the conversation started with attn's tools, as Claude Code's `mcp_instructions_delta` or `deferred_tools_delta`
 * or Codex's `additional_tools` shows. */
function toolsHeld(cell: Cell, record: any[]): "on" | "off" | "unknown" {
  if (cell.harness.agent === "codex") {
    const offered = record.filter((l) => l.payload?.type === "additional_tools")
    if (offered.length === 0) return "unknown"
    return offered.some((l) => JSON.stringify(l).includes("mcp__attn")) ? "on" : "off"
  }
  if (record.length === 0) return "unknown"
  const added = record.map((l) => l.attachment).filter((a) => a?.type === "mcp_instructions_delta" || a?.type === "deferred_tools_delta")
  return added.some((a) => (a.addedNames ?? []).some((n: string) => n === "attn" || n.startsWith("mcp__attn"))) ? "on" : "off"
}

/** A Codex that does not load the user's own skills, which it reads from HOME whatever CODEX_HOME says, and whose commands
 * skip the login shell, where a user's profile can put an installed attn before the one the bench runs. */
function plainCodexScript(place: Place, codex: string) {
  const skills = onPlace(place, 'find -L "$HOME/.agents/skills" -name SKILL.md 2>/dev/null').split("\n").filter(Boolean)
  const off = skills.map((path) => `{path=${JSON.stringify(path)},enabled=false}`).join(",").replace(/'/g, "'\\''")
  return `#!/bin/bash\nexec '${codex}' -c allow_login_shell=false -c 'skills.config=[${off}]' "$@"\n`
}

/** A Claude Code that runs with code mode off, whatever the user's settings say: their `env` beats the process environment,
 * and only the settings attn passes on the command line beat it. */
function plainClaudeScript(claude: string) {
  const env = '"env":{"CLAUDE_CODE_EXPERIMENTAL_CODE_MODE":"0"}'
  return `#!/bin/bash
args=("$@")
found=
for i in "\${!args[@]}"; do
  if [ "\${args[$i]}" = --settings ]; then
    s="\${args[$((i + 1))]}"
    args[$((i + 1))]='{${env},'"\${s#\\{}"
    found=1
  fi
done
[ -n "$found" ] || args+=(--settings '{${env}}')
exec '${claude}' "\${args[@]}"
`
}

const plainCodex = join(out, "codex-without-user-skills")
const plainClaude = join(out, "claude-code-mode-off")
function writeHomeWrappers() {
  const found = (name: string) => spawnSync("bash", ["-c", `command -v ${name}`], { encoding: "utf8" }).stdout.trim()
  writeFileSync(plainCodex, plainCodexScript({ name: "home" }, found("codex")), { mode: 0o755 })
  writeFileSync(plainClaude, plainClaudeScript(found("claude")), { mode: 0o755 })
}

function outpostShell(o: { name: string; ssh: string }, command: string, stdin?: string) {
  const r = run("ssh", [o.ssh, command], process.env, { stdin })
  if (r.status !== 0) throw new Error(`on outpost ${o.name}: ${command.slice(0, 200)} exited ${r.status}: ${(r.stdout + r.stderr).trim().slice(0, 600)}`)
  return r.stdout
}

/** An outpost's daemon starts over ssh with the environment a login gives it, so the bench restarts it with the tool home
 * while the home is not dialing it, and the home's next dial finds it running. Its executables are the outpost's own machine settings. */
function useOutpostToolHome(o: { name: string; ssh: string }, home: string) {
  const bin = `${home}/bench-bin`
  const env = { ATTN_INSTANCE: instance, ATTN_HEADLESS_TASKS: "off", ATTN_TOOL_HOME: home, CLAUDE_CONFIG_DIR: `${home}/.claude`, CODEX_HOME: `${home}/.codex`, SIGMA_HOME: `${home}/.sigma`, DISABLE_AUTOUPDATER: "1" }
  const attn = `env -u SSH_CONNECTION -u SSH_CLIENT -u SSH_TTY ${Object.entries(env).map(([k, v]) => `${k}='${v}'`).join(" ")} ~/.local/share/attn-outpost-instances/${instance}/attn`
  outpostShell(o, `mkdir -p '${bin}' && cat > '${bin}/claude' && chmod +x '${bin}/claude'`, plainClaudeScript(`${home}/bin/claude`))
  outpostShell(o, `cat > '${bin}/codex' && chmod +x '${bin}/codex'`, plainCodexScript(o, `${home}/bin/codex`))
  outpostShell(o, `${attn} daemon stop && ${attn} daemon ensure && ${attn} settings set claude_executable '${bin}/claude' && ${attn} settings set codex_executable '${bin}/codex'`)
}

const pinned = (harness: Harness) => harness.model.split("/").at(-1)!

/** Why a run measures something other than its cell, if it does. */
function faultOf(cell: Cell, held: string, used: Usage, events: any[]): string {
  if (cell.harness.agent === "claude" && !cell.harness.codeMode && events.some((e) => e.tool_name === "REPL")) return "Claude Code ran in code mode"
  if (held !== "unknown" && held !== cell.tools) return `the session launched with attn's tools ${held}`
  const other = used.models.filter((m) => m !== pinned(cell.harness))
  if (cell.harness.model && (used.models.length === 0 || other.length > 0)) return `pinned ${pinned(cell.harness)}, but attn reports ${used.models.join(", ") || "no model"}`
  return ""
}

let profiles = 0

/** `fault` says why a run measures something other than its cell, such as another model; such a run fails and stays out
 * of the comparisons. `launchMs` is from the run's start to the prompt being typed; `turnMs` from its Enter to settled, and
 * `retryMs` is how long Codex waited in it for the provider to take a request again. */
type RunResult = { task: string; cell: string; n: number; pass: boolean; why: string; fault: string; ended: string; tools: string; toolsHeld: string; missed: string[]; metrics: Metrics; requests?: Requests; usage: Usage; launchMs: number; turnMs: number; retryMs: number }

async function judged(task: Task, w: World, calls: Call[], ended: string): Promise<Check> {
  try {
    return await task.check(w, calls, ended)
  } catch (error) {
    return { pass: false, why: `the check failed: ${(error as Error).message}` }
  }
}

async function runOnce(cell: Cell, task: Task, n: number): Promise<RunResult> {
  const evidence = join(out, task.id, cell.id.replace(/ /g, "_"), String(n))
  mkdirSync(evidence, { recursive: true })
  const started = Date.now()
  const label = `${task.id}-${cell.harness.agent}-${n}`
  const local = join(workRoot, `${task.id}-${cell.id.replace(/\W+/g, "-")}-${n}`)
  mkdirSync(local, { recursive: true })
  const remote = cell.place.ssh && `/tmp/attn-agent-bench/${stamp}/${local.split("/").at(-1)}`
  if (remote) run("ssh", [cell.place.ssh!, "mkdir", "-p", remote], process.env)
  const folder = task.stage?.(local) ?? local
  if (remote && task.stage) {
    const copied = spawnSync("bash", ["-c", 'set -o pipefail; tar -C "$0" -cf - . | ssh "$1" tar -C "$2" -xf -', folder, cell.place.ssh!, remote], { encoding: "utf8" })
    if (copied.status !== 0) throw new Error(`staging ${task.id} on ${cell.place.name}: ${copied.stderr.trim()}`)
  }
  const dir = remote ?? folder
  const profileName = `bench ${++profiles} ${task.id} ${cell.id}`
  const mine = () => agents().filter((a) => a.profile === profileName)
  let session = ""
  let directory = dir
  let ended = ""
  let events: any[] = []
  let baseline = NO_USAGE
  let turn = { at: 0, launchMs: 0, turnMs: 0 }
  try {
    const created = json(wsctl(["request", "--json", JSON.stringify({ cmd: "profile_create", request_id: crypto.randomUUID(), name: profileName }), "--event", "profile_action_result"]))
    if (!created.success) throw new Error(`profile_create ${profileName}: ${created.error}`)
    const profile = created.profile.id
    const notebook = join(workRoot, `notebook-${profiles}`)
    mkdirSync(notebook, { recursive: true })
    attnRaw(["settings", "set", "notebook.root", notebook, "--profile", profile])
    if (task.launch === "chief") {
      attnRaw(["onboarding", "done", "--profile", profile])
      directory = json(attnRaw(["crew", "list", "--profile", profile, "--json"])).find((m: any) => m.chief).home_dir
      session = json(attnRaw(["crew", "start", "chief", "--agent", cell.harness.agent, "--profile", profile, "--json"])).session_id
    } else {
      session = crypto.randomUUID()
      const spawned = ["add-session", "--cwd", dir, "--agent", cell.harness.agent, "--profile", profile, "--label", label, "--id", session, ...(remote ? ["--host", cell.place.name] : [])]
      if (remote) await afterFirstSync(() => wsctl(spawned))
      else wsctl(spawned)
    }
    await atPrompt(session, cell.harness.agent)
    const terminals: string[] = json(attnRaw(["terminal", "ls", "--all", "--session", session, "--json"])).filter((t: any) => t.session_id === session).map((t: any) => t.terminal_id)
    if (task.launch === "chief") await settled(session, 0)
    const scoped = (args: string[], who?: string) => [...args, ...(who ? ["--session", who] : takesProfile(args) ? ["--profile", profile] : [])]
    const w: World = {
      dir,
      profile,
      session,
      state: {},
      attn: (args, opts = {}) => attnRaw(scoped(args, opts.session), { stdin: opts.stdin, cwd: opts.cwd }),
      instance: (args) => attnRaw(args),
      attnJson: (args, opts = {}) => json(attnRaw(scoped([...args, "--json"], opts.session), { stdin: opts.stdin })),
      sh: (command, stdin) => {
        const r = remote ? run("ssh", [cell.place.ssh!, `cd '${remote}' && ${command}`], process.env, { stdin }) : run("bash", ["-c", command], process.env, { stdin, cwd: dir })
        return { out: `${r.stdout}${r.stderr}`, code: r.status }
      },
      spawnShell: async (name) => {
        const id = crypto.randomUUID()
        wsctl(["add-session", "--cwd", local, "--agent", "shell", "--profile", profile, "--label", name, "--id", id])
        return id
      },
      sessions: mine,
      screen,
      answer: () => String(transcript(session).findLast((e) => e.role === "assistant")?.text ?? ""),
      trustForCodex: (folder) => {
        if (cell.harness.agent !== "codex") return
        const entry = `\n[projects.${JSON.stringify(folder)}]\ntrust_level = "trusted"\n`
        if (remote) outpostShell({ name: cell.place.name, ssh: cell.place.ssh! }, `cat >> '${outpostToolHome}/.codex/config.toml'`, entry)
        else appendFileSync(join(codexHome, "config.toml"), entry)
      },
      opened: () => column(join(dataDir, "attn.db"), "SELECT path FROM file_activity WHERE source = 'opened' AND session_id = ? ORDER BY last_at DESC", session),
      delegated: async (folder, calls) => {
        const deadline = Date.now() + DELEGATE_DEADLINE_MS
        const there = () => mine().filter((a) => a.directory === folder && a.id !== session)
        if (!calls.some((c) => isCommand(c, "delegate") && c.refused === undefined)) return there()
        while (there().length === 0 && Date.now() < deadline) await wait(POLL_MS)
        return there()
      },
      mermaid: drawsMermaid,
    }
    const prompt = await task.setup(w)
    const before = transcript(session).length
    const toolLog = () => toolOutcomes(readFileSync(join(dataDir, "daemon.log"), "utf8"), session)
    const loggedBefore = remote ? 0 : toolLog().length
    if (before > 0) baseline = await steadyUsage(session)
    await declineDialogs(session)
    wsctl(["input", "--id", session, "--text", prompt])
    await wait(500)
    turn = { at: Date.now(), launchMs: Date.now() - started, turnMs: 0 }
    wsctl(["input", "--id", session, "--enter"])
    let stopped = ""
    try {
      ended = await settled(session, before)
    } catch (error) {
      stopped = ended = (error as Error).message
    }
    turn.turnMs = Date.now() - turn.at
    events = transcript(session).slice(before)
    const conversation = remote ? "" : conversationOf(session)
    const records: Records | undefined = remote
      ? undefined
      : { tools: toolLog().slice(loggedBefore), cli: cliRuns(readFileSync(cliLog, "utf8")).filter((c) => c.at >= turn.at && (c.conversation ? c.conversation === conversation : terminals.includes(c.terminal))) }
    const calls = attnCalls(events, records)
    const verdict: Check = stopped ? { pass: false, why: stopped } : await judged(task, w, calls, ended)
    const used = since(await steadyUsage(session), baseline)
    const record = harnessRecord(cell, directory, started)
    const held = toolsHeld(cell, record)
    const requests = (cell.harness.agent === "codex" ? codexRequests : claudeRequests)(record, before > 0 ? turn.at : 0)
    const codex = cell.harness.agent === "codex" && !remote
    const retryMs = codex ? Math.min(codexRetryWaitMs(conversation, turn.at), turn.turnMs) : 0
    const missed = records ? unrecorded(codex ? codexRuns(codexRollout(conversation), turn.at) : shellRuns(events), records) : []
    const fault = !events.some((e) => e.role === "user")
      ? "the prompt never reached the agent"
      : stopped && retryMs > 0
        ? `the provider made Codex wait ${Math.round(retryMs / 1000)}s`
        : records?.cli.length === 0 && missed.length > 0
          ? "the session ran an attn the bench did not record"
          : faultOf(cell, held, used, events)
    const result: RunResult = {
      task: task.id, cell: cell.id, n, pass: verdict.pass && !fault, why: fault ? `bench: ${fault}; ${verdict.why}` : verdict.why, fault, ended,
      tools: cell.tools, toolsHeld: held, missed, metrics: metrics(events, calls, blockedRuns(records)), requests, usage: used, launchMs: turn.launchMs, turnMs: turn.turnMs, retryMs,
    }
    writeFileSync(join(evidence, "calls.json"), JSON.stringify(calls, null, 1))
    if (records) writeFileSync(join(evidence, "records.json"), JSON.stringify(records, null, 1))
    if (ended !== "closed itself") writeFileSync(join(evidence, "asks.json"), JSON.stringify(w.attnJson(["ask", "list"], { session }), null, 1))
    writeFileSync(join(evidence, "result.json"), JSON.stringify(result, null, 1))
    return result
  } catch (error) {
    const fault = (error as Error).message
    return { task: task.id, cell: cell.id, n, pass: false, why: `bench: ${fault}`, fault, ended, tools: cell.tools, toolsHeld: "unknown", missed: [], metrics: metrics(events, []), usage: NO_USAGE, launchMs: turn.launchMs, turnMs: turn.turnMs, retryMs: 0 }
  } finally {
    if (session) {
      writeFileSync(join(evidence, "screen.txt"), screen(session))
      writeFileSync(join(evidence, "transcript.jsonl"), events.map((e) => JSON.stringify(e)).join("\n") + "\n")
    }
    for (const a of mine()) run(wsctlBinary, ["rm-session", "--id", a.id], daemonEnv)
    task.restore?.((args, stdin) => attnRaw(args, { stdin }))
  }
}

const median = (xs: number[]) => {
  const s = [...xs].sort((a, b) => a - b)
  return s.length === 0 ? 0 : s.length % 2 ? s[(s.length - 1) / 2]! : (s[s.length / 2 - 1]! + s[s.length / 2]!) / 2
}

function table(results: RunResult[]) {
  const lines = [
    `${[...new Set(tasks.map(runsOf))].join(" or ")} run(s) per cell on instance ${instance}, attn ${attnRaw(["version"]).trim()}, ${new Date().toISOString().slice(0, 10)}. Turns, reqs (API requests), first k (the first request's input tokens), tokens in thousands (uncached input, cache reads, cache writes), output tokens, calls and seconds are medians per run. Tool and CLI show calls, then failed ones in parentheses. Blocked is commands a Codex sandbox kept from attn, and denied is calls holding an attn command or tool that Claude Code refused to run. Neither counts as CLI calls. Usage is calls refused because their arguments did not fit, on either surface. Help is --help reads, ref is skill and guide reads. Cost is the mean per run at API prices, as attn accounts it; "+ unpriced" marks a model attn has no price for. Runs whose fault says they measured something else fail. A run whose usage attn could not measure whole is marked and left out of tokens and cost, and one that waited for the provider out of the seconds.`,
    "",
    "| task | harness | models | tools | place | pass | turns | reqs | first k | in k | read k | write k | out | tool | CLI | blocked | denied | usage | searches | help | ref | turn s | cost/run | failures |",
    "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|",
  ]
  for (const task of tasks)
    for (const cell of cells) {
      const rs = results.filter((r) => r.task === task.id && r.cell === cell.id)
      if (rs.length === 0) continue
      const m = (f: (r: RunResult) => number) => median(rs.map(f))
      const measured = rs.filter((r) => !r.usage.incomplete && r.usage.models.length > 0)
      const used = (f: (u: Usage) => number) => median(measured.map((r) => f(r.usage)))
      const k = (f: (u: Usage) => number) => (measured.length === 0 ? "-" : (used(f) / 1000).toFixed(1))
      const requested = rs.flatMap((r) => r.requests ?? [])
      const req = (f: (q: Requests) => number) => (requested.length === 0 ? "-" : String(+median(requested.map(f)).toFixed(1)))
      const missed = rs.filter((r) => r.missed.length > 0).map((r) => `#${r.n} unrecorded: ${r.missed.join(", ")}`)
      const unmeasured = rs.filter((r) => !measured.includes(r)).map((r) => `#${r.n} usage incomplete`)
      const timed = rs.filter((r) => r.retryMs === 0)
      const waited = rs.filter((r) => r.retryMs > 0).map((r) => `#${r.n} waited ${Math.round(r.retryMs / 1000)}s for the provider`)
      const fails = rs.filter((r) => !r.pass).map((r) => `#${r.n}: ${r.why}`.slice(0, 160).replace(/\|/g, "/"))
      const denied = rs.filter((r) => r.metrics.harnessDenied > 0).map((r) => `#${r.n} Claude Code denied ${r.metrics.harnessDenied} attn call${r.metrics.harnessDenied === 1 ? "" : "s"}`)
      const cli = rs.filter((r) => cell.tools === "on" && r.metrics.cliCalls > 0).map((r) => `#${r.n} used the CLI`)
      lines.push(
        `| ${task.id} | ${cell.harness.name} | ${[...new Set(rs.flatMap((r) => r.usage.models))].join(" ") || "-"} | ${cell.tools} | ${cell.place.name} | ${rs.filter((r) => r.pass).length}/${rs.length} | ${m((r) => r.metrics.turns)} | ${req((q) => q.count)} | ${req((q) => q.firstTokens / 1000)} | ${k((u) => u.input)} | ${k((u) => u.cacheRead)} | ${k((u) => u.cacheWrite)} | ${measured.length === 0 ? "-" : used((u) => u.output)} | ${m((r) => r.metrics.toolCalls)} (${m((r) => r.metrics.toolRefusals)}) | ${m((r) => r.metrics.cliCalls)} (${m((r) => r.metrics.cliFailures)}) | ${m((r) => r.metrics.sandboxBlocked)} | ${m((r) => r.metrics.harnessDenied)} | ${m((r) => r.metrics.usageRefusals)} | ${m((r) => r.metrics.searches)} | ${m((r) => r.metrics.cliHelpReads)} | ${m((r) => r.metrics.referenceReads)} | ${timed.length === 0 ? "-" : (median(timed.map((r) => r.turnMs)) / 1000).toFixed(1)} | ${measured.length === 0 ? "-" : `$${(measured.reduce((a, r) => a + r.usage.cost, 0) / measured.length).toFixed(3)}`}${rs.some((r) => r.usage.unpriced) ? " + unpriced" : ""} | ${[...fails, ...cli, ...denied, ...unmeasured, ...waited, ...missed].join("<br>") || "-"} |`,
      )
    }
  return lines.join("\n")
}

/** A 95% percentile interval for the mean of `xs`, from 10,000 resamples with a fixed seed. */
function interval(xs: number[]): [number, number] {
  let state = 1
  const next = () => {
    state = (state * 1664525 + 1013904223) >>> 0
    return state / 2 ** 32
  }
  const means = Array.from({ length: 10_000 }, () => xs.reduce((a) => a + xs[Math.floor(next() * xs.length)]!, 0) / xs.length).sort((a, b) => a - b)
  return [means[Math.floor(means.length * 0.025)]!, means[Math.floor(means.length * 0.975)]!]
}

/** D22 asks one way, whether Sigma is worse. Five pairs all worse come by chance 0.5^5 = 3% of the time, four 6%, so a
 * verdict at 95% needs five. */
const D22_PAIRS = 5

const strata: [string, Task[]][] = [["the tasks", [...TASKS, ...HARDER_TASKS]], ["the ask tasks", ASK_TASKS], ["the probes", PROBES]]

/** D22, with the task as the paired unit: over a group of tasks, Sigma's mean pass-rate difference to Claude Code and to
 * Codex per setting and place. It fails when the 95% interval lies below zero. A task flags for a rerun only on a clear
 * gap: Sigma in at most 1 of 3 runs against every run of the other. Faulted runs are left out. */
function judgeD22(results: RunResult[]) {
  const lines: string[] = []
  const rate = (cell: Cell, task: Task) => {
    const rs = results.filter((r) => r.task === task.id && r.cell === cell.id && !r.fault)
    return rs.length === 0 ? undefined : { rate: rs.filter((r) => r.pass).length / rs.length, n: rs.length }
  }
  const points = (x: number) => `${x >= 0 ? "+" : ""}${(x * 100).toFixed(0)}`
  for (const sigma of cells.filter((c) => c.harness.agent === "sigma"))
    for (const other of cells.filter((c) => c.harness.agent !== "sigma" && c.tools === sigma.tools && c.place.name === sigma.place.name)) {
      const against = `tools ${sigma.tools}, ${sigma.place.name}: ${sigma.harness.name} against ${other.harness.name}`
      for (const [group, members] of strata) {
        const pairs = tasks.filter((t) => members.includes(t)).flatMap((task) => {
          const [s, o] = [rate(sigma, task), rate(other, task)]
          return s && o ? [{ task, s, o, gap: s.rate - o.rate }] : []
        })
        if (pairs.length === 0) continue
        const [low, high] = interval(pairs.map((p) => p.gap))
        const mean = pairs.reduce((a, p) => a + p.gap, 0) / pairs.length
        const verdict = pairs.length < D22_PAIRS ? `too few pairs for a verdict (${D22_PAIRS} needed)` : high < 0 ? "FAILS" : "holds"
        lines.push(`${verdict}  ${group}, ${against}: ${points(mean)} points (95% ${points(low)} to ${points(high)}) over ${pairs.length} tasks, better on ${pairs.filter((p) => p.gap > 0).length}, worse on ${pairs.filter((p) => p.gap < 0).length}`)
        for (const p of pairs.filter((p) => Math.min(p.s.n, p.o.n) >= 3 && p.s.rate <= 1 / 3 && p.o.rate === 1))
          lines.push(`RERUN  ${p.task.id}, ${against}: ${Math.round(p.s.rate * p.s.n)}/${p.s.n} against ${p.o.n}/${p.o.n}`)
      }
    }
  const faults = results.filter((r) => r.fault).length
  if (faults > 0) lines.push(`${faults} faulted run(s) left out`)
  return lines.length === 0 ? "D22 not judged: the run had no Sigma cell beside a Claude Code or Codex cell." : `D22 (Sigma at least as often right as Claude Code and Codex, paired by task):\n${lines.join("\n")}`
}

/** Codex saves a trust entry for each folder whose trust screen the bench answered; remove the ones under this run's
 * work root, so the harness home keeps only what it had. */
function withoutTrust(config: string, root: string) {
  const kept: string[] = []
  let dropping = false
  for (const line of config.split("\n")) {
    if (line.startsWith("[")) dropping = line.startsWith(`[projects."${root}`)
    if (!dropping) kept.push(line)
  }
  return kept.join("\n")
}
function forgetCodexTrust() {
  const config = join(codexHome, "config.toml")
  if (existsSync(config)) {
    const before = readFileSync(config, "utf8")
    const after = withoutTrust(before, workRoot)
    if (after !== before) writeFileSync(config, after)
  }
  for (const o of outposts) {
    if (!outpostToolHome) continue
    const path = `${outpostToolHome}/.codex/config.toml`
    const read = run("ssh", [o.ssh, `cat '${path}'`], process.env)
    const after = withoutTrust(read.stdout, `/tmp/attn-agent-bench/${stamp}`)
    if (read.status === 0 && after !== read.stdout) run("ssh", [o.ssh, `cat > '${path}'`], process.env, { stdin: after })
  }
}

const build = spawnSync("go", ["build", "-o", wsctlBinary, "./scripts/wsctl"], { cwd: repository, encoding: "utf8" })
if (build.status !== 0) throw new Error(`go build ./scripts/wsctl: ${build.stderr}`)
const mermaidCheck = join(process.env.CARGO_TARGET_DIR ?? join(repository, "native", "target"), "debug", "examples", "mermaid_check")
if (tasks.some((t) => t.mermaid)) {
  const built = spawnSync("cargo", ["build", "--example", "mermaid_check", "-p", "attn-app"], { cwd: join(repository, "native"), encoding: "utf8", stdio: ["ignore", "inherit", "inherit"] })
  if (built.status !== 0) throw new Error("cargo build --example mermaid_check -p attn-app failed")
}
function drawsMermaid(source: string) {
  const r = run(mermaidCheck, [], process.env, { stdin: source })
  if (r.status === 2) return { drawn: false, skipped: [] }
  if (r.status !== 0 && r.status !== 1) throw new Error(`mermaid_check exited ${r.status}: ${r.stderr.trim()}`)
  return { drawn: true, skipped: r.stdout.split("\n").filter(Boolean) }
}
writeHomeWrappers()
const taskSettings = [...new Set(tasks.flatMap((t) => Object.keys(t.settings ?? {})))]
function cleanUp() {
  forgetCodexTrust()
  if (keep) return
  if (addedOutposts.length > 0) connectBeforeRemoving()
  for (const id of addedOutposts) {
    const r = run(wsctlBinary, ["outposts", "--remove", id], daemonEnv)
    if (r.status !== 0) console.error(`wsctl outposts --remove ${id}: ${r.stderr.trim()}`)
  }
  for (const o of outposts) run("ssh", [o.ssh, `rm -rf '/tmp/attn-agent-bench/${stamp}'`], process.env)
  spawnSync(attnBinary, ["instance", "clean", instance], { env: baseEnv(), stdio: "inherit" })
}
for (const signal of ["SIGINT", "SIGTERM"] as const)
  process.once(signal, () => {
    cleanUp()
    process.exit(130)
  })

const results: RunResult[] = []
try {
  for (const codeMode of [false, true]) {
    const group = cells.filter((c) => c.harness.codeMode === codeMode)
    if (group.length === 0) continue
    await startDaemon(codeMode)
    for (const [index, task] of tasks.entries()) {
      for (const name of taskSettings) setting(name, task.settings?.[name] ?? "")
      for (const cell of index % 2 ? [...group].reverse() : group) {
        setting("agent_tools_enabled", cell.tools === "on" ? "true" : "false")
        setting(`default_model_${cell.harness.agent}`, cell.harness.model)
        setting(`default_effort_${cell.harness.agent}`, cell.harness.effort)
        if (cell.harness.agent === "claude") setting("claude_executable", cell.harness.codeMode ? "" : plainClaude)
        if (cell.harness.agent === "codex") setting("codex_executable", plainCodex)
        if (cell.harness.agent === "sigma") setting("sigma_enabled", "true")
        const jobs = Array.from({ length: runsOf(task) }, (_, i) => i + 1)
        await Promise.all(
          Array.from({ length: task.restore ? 1 : Math.min(parallel, jobs.length) }, async () => {
            for (let n = jobs.shift(); n !== undefined; n = jobs.shift()) {
              const r = await runOnce(cell, task, n)
              results.push(r)
              console.error(`${r.pass ? "PASS" : "FAIL"} ${r.task} ${r.cell} #${r.n} models=${r.usage.models.join("+")} held=${r.toolsHeld} turns=${r.metrics.turns} tool=${r.metrics.toolCalls} cli=${r.metrics.cliCalls} in=${r.usage.input}+${r.usage.cacheRead}r+${r.usage.cacheWrite}w cost=$${r.usage.cost.toFixed(3)} ${Math.round(r.turnMs / 1000)}s ${r.pass ? "" : r.why}`)
            }
          }),
        )
      }
    }
  }
} finally {
  writeFileSync(join(out, "results.json"), JSON.stringify({ instance, cells: cells.map((c) => c.id), results }, null, 1))
  const report = `${table(results)}\n${cells.some((c) => c.harness.agent === "sigma") ? `\n${judgeD22(results)}\n` : ""}`
  writeFileSync(join(out, "table.md"), report)
  console.log(report)
  cleanUp()
  console.log(`runs: ${out}\nwork folders: ${workRoot}${keep ? `\ninstance ${instance} kept; clean it with: attn instance clean ${instance}` : ""}`)
}
