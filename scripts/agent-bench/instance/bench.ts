import { spawnSync } from "node:child_process"
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs"
import { homedir, tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { PROBES } from "./probes.ts"
import { TASKS } from "./tasks.ts"
import { attnCalls, type Check, metrics, type Metrics, run, type Task, type World } from "./world.ts"

const repository = resolve(import.meta.dir, "../../..")
/** Tripwires. The prototype's runs took 13 to 38 s and C16's under a minute; a run that reaches these is stuck. */
const LAUNCH_DEADLINE_MS = 3 * 60_000
const RUN_DEADLINE_MS = 10 * 60_000
const POLL_MS = 2_000

const arg = (name: string) => {
  const i = process.argv.indexOf(`--${name}`)
  return i >= 0 ? process.argv[i + 1] : undefined
}
const flag = (name: string) => process.argv.includes(`--${name}`)
const list = (value: string | undefined, fallback: string) => (value ?? fallback).split(",").filter(Boolean)

const grid = flag("grid")
const attnBinary = resolve(arg("attn") ?? join(repository, "attn"))
if (!existsSync(attnBinary)) throw new Error(`${attnBinary} does not exist: run make build, or pass --attn <binary>`)
const withoutAttnEnv = () => Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^(ATTN_|CLAUDE|CODEX_)/.test(key)))

function newInstance(requested: string | undefined) {
  const usage = "pass --instance <name>: a new instance this run creates, changes and cleans"
  if (!requested) throw new Error(usage)
  const attn = (args: string[]) => spawnSync(attnBinary, args, { env: withoutAttnEnv(), encoding: "utf8" })
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
const runs = Number(arg("runs") ?? (grid ? 2 : 1))
const parallel = Number(arg("parallel") ?? 2)
const keep = flag("keep")

const ALL = [...TASKS, ...PROBES]
const taskIds = list(arg("tasks"), TASKS.map((t) => t.id).join(",")).flatMap((id) => (id === "probes" ? PROBES.map((p) => p.id) : [id]))
const tasks = taskIds.map((id) => ALL.find((t) => t.id === id) ?? (() => { throw new Error(`no task ${id}; tasks: ${ALL.map((t) => t.id).join(", ")}`) })())

/** A harness as a cell names it: the agent attn launches, whether Claude Code's code mode is on, and the model. */
type Harness = { name: string; agent: "claude" | "codex" | "sigma"; codeMode: boolean; model: string }
const harnesses: Harness[] = list(arg("harness"), grid ? "claude,claude-code-mode,codex,sigma" : "claude,codex").map((spec) => {
  const [name, model = ""] = spec.split("@") as [string, string?]
  const agent = name === "claude-code-mode" ? "claude" : name
  if (agent !== "claude" && agent !== "codex" && agent !== "sigma") throw new Error(`no harness ${name}: claude, claude-code-mode, codex or sigma, each with an optional @model`)
  return { name: spec, agent, codeMode: name === "claude-code-mode", model }
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
for (const place of [{ name: "home" } as Place, ...outposts])
  for (const harness of harnesses) {
    if (place.name !== "home" && (harness.codeMode || harnesses.find((h) => h.agent === harness.agent && !h.codeMode) !== harness)) continue
    for (const t of tools) cells.push({ harness, tools: t, place, id: `${harness.name} tools-${t} ${place.name}`.replace(/[^\w.@ -]/g, "_") })
  }
cells.sort((a, b) => Number(a.harness.codeMode) - Number(b.harness.codeMode))

const stamp = new Date().toISOString().replace(/[:.]/g, "-")
const out = join(import.meta.dir, "runs", stamp)
const workRoot = mkdtempSync(join(realpathSync(tmpdir()), "attn-agent-bench-"))
mkdirSync(out, { recursive: true })

const baseEnv = (): NodeJS.ProcessEnv => {
  return {
    ...withoutAttnEnv(),
    ATTN_INSTANCE: instance,
    ATTN_HEADLESS_TASKS: "off",
    ...(toolHome ? { ATTN_TOOL_HOME: toolHome, CLAUDE_CONFIG_DIR: join(toolHome, ".claude"), CODEX_HOME: join(toolHome, ".codex") } : {}),
  }
}
let daemonEnv = baseEnv()

function attnRaw(args: string[], opts: { stdin?: string; cwd?: string; env?: NodeJS.ProcessEnv } = {}) {
  const r = run(attnBinary, args, opts.env ?? daemonEnv, { stdin: opts.stdin, cwd: opts.cwd ?? repository })
  if (r.status !== 0) throw new Error(`attn ${args.join(" ")} exited ${r.status}: ${(r.stdout + r.stderr).trim().slice(0, 600)}`)
  return r.stdout
}
const json = (text: string) => JSON.parse(text.slice(text.search(/[[{]/)))

const wsctlBinary = join(out, "wsctl")
function wsctl(args: string[]) {
  const r = run(wsctlBinary, args, daemonEnv)
  if (r.status !== 0) throw new Error(`wsctl ${args.join(" ")}: ${r.stderr.trim().slice(-400)}`)
  return r.stdout
}

function startDaemon(codeMode: boolean) {
  const env = { ...baseEnv(), ...(codeMode ? { CLAUDE_CODE_EXPERIMENTAL_CODE_MODE: "1" } : {}) }
  if (env.CLAUDE_CODE_EXPERIMENTAL_CODE_MODE === daemonEnv.CLAUDE_CODE_EXPERIMENTAL_CODE_MODE && started) return
  if (started) attnRaw(["daemon", "stop"])
  daemonEnv = env
  attnRaw(["daemon", "ensure"])
  if (!started && outposts.length > 0) {
    setting("outposts_enabled", "true")
    for (const o of outposts) {
      const added = json(wsctl(["outposts", "--add", o.name, "--destination", o.ssh, "--until", "connected"]).trim().split("\n").at(-1)!)
      addedOutposts.push(added.outposts.find((row: { name: string }) => row.name === o.name).id)
    }
  }
  started = true
}
let started = false
const addedOutposts: string[] = []

const setting = (name: string, value: string) => attnRaw(["settings", "set", name, value])

type Agent = { id: string; directory: string; state: string; label: string; profile: string }
const agents = (): Agent[] => json(attnRaw(["agent", "list", "--json"]))
const wait = (ms: number) => new Promise((r) => setTimeout(r, ms))
const screen = (session: string) => run(attnBinary, ["agent", "peek", session], daemonEnv).stdout

/** Waits for a new session to reach its prompt. A trust screen for the bench's own folder is answered once. */
async function atPrompt(session: string) {
  const deadline = Date.now() + LAUNCH_DEADLINE_MS
  let trusted = false
  while (Date.now() < deadline) {
    if (!trusted && /Trust this folder\?|Do you trust the files in this folder\?/.test(screen(session))) {
      wsctl(["input", "--id", session, "--enter"])
      trusted = true
      continue
    }
    const me = agents().find((a) => a.id === session)
    if (me && ["idle", "waiting_input"].includes(me.state)) return
    await wait(POLL_MS)
  }
  throw new Error(`the session never reached its prompt: ${agents().find((a) => a.id === session)?.state ?? "gone"}`)
}

const transcript = (session: string): any[] =>
  run(attnBinary, ["session", "transcript", session, "--json"], daemonEnv)
    .stdout.split("\n")
    .filter((line) => line.startsWith("{"))
    .map((line) => JSON.parse(line))

/** Waits for the turn the prompt started to end: the session is back at its prompt after answering, or it closed itself. */
async function settled(session: string): Promise<string> {
  const deadline = Date.now() + RUN_DEADLINE_MS
  let state = ""
  while (Date.now() < deadline) {
    await wait(POLL_MS)
    const me = agents().find((a) => a.id === session)
    if (!me) return "closed itself"
    state = me.state
    if (["idle", "waiting_input"].includes(state) && transcript(session).some((e) => e.role === "assistant" || e.kind === "tool_call")) return state
  }
  throw new Error(`deadline ${RUN_DEADLINE_MS / 1000}s in state ${state}`)
}

/** `unpriced` when attn has no price for a model the session used, so `cost` leaves that model out. */
type Usage = { input: number; output: number; cost: number; unpriced: boolean }
function usage(session: string): Usage {
  const models: any[] = json(attnRaw(["session", "show", session, "--json"])).entry.usage?.models ?? []
  const sum = (f: (m: any) => number) => models.reduce((a, m) => a + f(m), 0)
  return {
    input: sum((m) => m.input_tokens + m.cache_read_tokens + m.cache_write_5m_tokens + m.cache_write_1h_tokens + m.cache_write_unclassified_tokens),
    output: sum((m) => m.output_tokens),
    cost: sum((m) => m.cost_usd ?? 0),
    unpriced: models.some((m) => m.has_unpriced_usage),
  }
}

let profiles = 0

type RunResult = { task: string; cell: string; n: number; pass: boolean; why: string; ended: string; metrics: Metrics; usage: Usage; ms: number }

async function runOnce(cell: Cell, task: Task, n: number): Promise<RunResult> {
  const evidence = join(out, task.id, cell.id.replace(/ /g, "_"), String(n))
  mkdirSync(evidence, { recursive: true })
  const started = Date.now()
  const label = `${task.id}-${cell.harness.agent}-${n}`
  const local = join(workRoot, `${task.id}-${cell.id.replace(/\W+/g, "-")}-${n}`)
  mkdirSync(local, { recursive: true })
  const remote = cell.place.ssh && `/tmp/attn-agent-bench/${stamp}/${local.split("/").at(-1)}`
  if (remote) run("ssh", [cell.place.ssh!, "mkdir", "-p", remote], process.env)
  if (remote && task.stage) throw new Error(`${task.id} stages a folder on this machine, so it runs on the home only`)
  const dir = remote ?? task.stage?.(local) ?? local
  const profileName = `bench ${++profiles} ${task.id} ${cell.id}`
  const mine = () => agents().filter((a) => a.profile === profileName)
  let session = ""
  let ended = ""
  let events: any[] = []
  try {
    const created = json(wsctl(["request", "--json", JSON.stringify({ cmd: "profile_create", request_id: crypto.randomUUID(), name: profileName }), "--event", "profile_action_result"]))
    if (!created.success) throw new Error(`profile_create ${profileName}: ${created.error}`)
    const profile = created.profile.id
    if (task.launch === "chief") {
      session = json(attnRaw(["crew", "start", "chief", "--agent", cell.harness.agent, "--profile", profile, "--json"])).session_id
    } else {
      session = crypto.randomUUID()
      wsctl(["add-session", "--cwd", dir, "--agent", cell.harness.agent, "--profile", profile, "--label", label, "--id", session, ...(remote ? ["--host", cell.place.name] : [])])
    }
    await atPrompt(session)
    const scoped = (args: string[], who?: string) => [...args, ...(who ? ["--session", who] : ["--profile", profile])]
    const w: World = {
      dir,
      profile,
      session,
      state: {},
      attn: (args, opts = {}) => attnRaw(scoped(args, opts.session), { stdin: opts.stdin, cwd: opts.cwd }),
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
    }
    const prompt = await task.setup(w)
    wsctl(["input", "--id", session, "--text", prompt])
    await wait(500)
    wsctl(["input", "--id", session, "--enter"])
    let check: Check
    try {
      ended = await settled(session)
      events = transcript(session)
      check = task.check(w, attnCalls(events))
    } catch (error) {
      ended = (error as Error).message
      events = transcript(session)
      check = { pass: false, why: ended }
    }
    const calls = attnCalls(events)
    const result = { task: task.id, cell: cell.id, n, ...check, ended, metrics: metrics(events, calls), usage: usage(session), ms: Date.now() - started }
    writeFileSync(join(evidence, "calls.json"), JSON.stringify(calls, null, 1))
    writeFileSync(join(evidence, "result.json"), JSON.stringify(result, null, 1))
    return result
  } catch (error) {
    return { task: task.id, cell: cell.id, n, pass: false, why: `bench: ${(error as Error).message}`, ended, metrics: metrics(events, []), usage: { input: 0, output: 0, cost: 0, unpriced: false }, ms: Date.now() - started }
  } finally {
    if (session) {
      writeFileSync(join(evidence, "screen.txt"), screen(session))
      writeFileSync(join(evidence, "transcript.jsonl"), events.map((e) => JSON.stringify(e)).join("\n") + "\n")
    }
    for (const a of mine()) run(wsctlBinary, ["rm-session", "--id", a.id], daemonEnv)
  }
}

const median = (xs: number[]) => {
  const s = [...xs].sort((a, b) => a - b)
  return s.length === 0 ? 0 : s.length % 2 ? s[(s.length - 1) / 2]! : (s[s.length / 2 - 1]! + s[s.length / 2]!) / 2
}

function table(results: RunResult[]) {
  const lines = [
    `${runs} run(s) per cell on instance ${instance}, attn ${attnRaw(["version"]).trim()}, ${new Date().toISOString().slice(0, 10)}. Turns, input tokens (input + cache read + cache write), output tokens, attn calls, searches and help reads are medians per run; cost is the mean per run at API prices, as attn accounts it; "+ unpriced" marks a model attn has no price for.`,
    "",
    "| task | harness | tools | place | pass | turns | in k | out | attn calls | searches | help | cost/run | failures |",
    "|---|---|---|---|---|---|---|---|---|---|---|---|---|",
  ]
  for (const task of tasks)
    for (const cell of cells) {
      const rs = results.filter((r) => r.task === task.id && r.cell === cell.id)
      if (rs.length === 0) continue
      const m = (f: (r: RunResult) => number) => median(rs.map(f))
      const fails = rs.filter((r) => !r.pass).map((r) => `#${r.n}: ${r.why}`.slice(0, 160).replace(/\|/g, "/"))
      lines.push(
        `| ${task.id} | ${cell.harness.name} | ${cell.tools} | ${cell.place.name} | ${rs.filter((r) => r.pass).length}/${rs.length} | ${m((r) => r.metrics.turns)} | ${(m((r) => r.usage.input) / 1000).toFixed(1)} | ${m((r) => r.usage.output)} | ${m((r) => r.metrics.attnCalls)} | ${m((r) => r.metrics.searches)} | ${m((r) => r.metrics.helpReads)} | $${(rs.reduce((a, r) => a + r.usage.cost, 0) / rs.length).toFixed(3)}${rs.some((r) => r.usage.unpriced) ? " + unpriced" : ""} | ${fails.join("<br>") || "-"} |`,
      )
    }
  return lines.join("\n")
}

/** D22: on every task, Sigma reaches the right end state at least as often as Claude Code and as Codex, per setting
 * and place. */
function judgeD22(results: RunResult[]) {
  const lines: string[] = []
  const rate = (cell: Cell, task: string) => {
    const rs = results.filter((r) => r.task === task && r.cell === cell.id)
    return rs.length === 0 ? undefined : rs.filter((r) => r.pass).length / rs.length
  }
  for (const task of tasks)
    for (const t of tools)
      for (const place of [...new Set(cells.map((c) => c.place.name))]) {
        const here = cells.filter((c) => c.tools === t && c.place.name === place)
        const sigma = here.filter((c) => c.harness.agent === "sigma")
        const others = here.filter((c) => c.harness.agent !== "sigma")
        if (sigma.length === 0 || others.length === 0) continue
        const best = Math.max(...others.map((c) => rate(c, task.id) ?? 0))
        for (const s of sigma) {
          const r = rate(s, task.id)
          if (r === undefined) continue
          lines.push(`${r >= best ? "holds" : "FAILS"}  ${task.id}, tools ${t}, ${place}: ${s.harness.name} ${(r * 100).toFixed(0)}% against the best of the others ${(best * 100).toFixed(0)}%`)
        }
      }
  return lines.length === 0 ? "D22 not judged: the run had no Sigma cell beside a Claude Code or Codex cell." : `D22 (Sigma at least as often right as Claude Code and Codex):\n${lines.join("\n")}`
}

/** Codex saves a trust entry for each folder whose trust screen the bench answered; remove the ones under this run's
 * work root, so the harness home keeps only what it had. */
function forgetCodexTrust() {
  const config = join(toolHome ? join(toolHome, ".codex") : join(homedir(), ".codex"), "config.toml")
  if (!existsSync(config)) return
  const lines = readFileSync(config, "utf8").split("\n")
  const kept: string[] = []
  let dropping = false
  for (const line of lines) {
    if (line.startsWith("[")) dropping = line.startsWith(`[projects."${workRoot}`)
    if (!dropping) kept.push(line)
  }
  if (kept.length !== lines.length) writeFileSync(config, kept.join("\n"))
}

const build = spawnSync("go", ["build", "-o", wsctlBinary, "./scripts/wsctl"], { cwd: repository, encoding: "utf8" })
if (build.status !== 0) throw new Error(`go build ./scripts/wsctl: ${build.stderr}`)
const taskSettings = [...new Set(tasks.flatMap((t) => Object.keys(t.settings ?? {})))]
const results: RunResult[] = []
try {
  for (const codeMode of [false, true]) {
    const group = cells.filter((c) => c.harness.codeMode === codeMode)
    if (group.length === 0) continue
    startDaemon(codeMode)
    for (const cell of group) {
      setting("agent_tools_enabled", cell.tools === "on" ? "true" : "false")
      setting(`default_model_${cell.harness.agent}`, cell.harness.model)
      if (cell.harness.agent === "sigma") setting("sigma_enabled", "true")
      for (const task of tasks) {
        for (const name of taskSettings) setting(name, task.settings?.[name] ?? "")
        const jobs = Array.from({ length: runs }, (_, i) => i + 1)
        await Promise.all(
          Array.from({ length: Math.min(parallel, jobs.length) }, async () => {
            for (let n = jobs.shift(); n !== undefined; n = jobs.shift()) {
              const r = await runOnce(cell, task, n)
              results.push(r)
              console.error(`${r.pass ? "PASS" : "FAIL"} ${r.task} ${r.cell} #${r.n} turns=${r.metrics.turns} in=${r.usage.input} cost=$${r.usage.cost.toFixed(3)} ${Math.round(r.ms / 1000)}s ${r.pass ? "" : r.why}`)
            }
          }),
        )
      }
    }
  }
} finally {
  writeFileSync(join(out, "results.json"), JSON.stringify({ instance, cells: cells.map((c) => c.id), runs, results }, null, 1))
  const report = `${table(results)}\n\n${judgeD22(results)}\n`
  writeFileSync(join(out, "table.md"), report)
  console.log(report)
  forgetCodexTrust()
  if (!keep) {
    for (const id of addedOutposts) {
      const r = run(wsctlBinary, ["outposts", "--remove", id], daemonEnv)
      if (r.status !== 0) console.error(`wsctl outposts --remove ${id}: ${r.stderr.trim()}`)
    }
    spawnSync(attnBinary, ["instance", "clean", instance], { env: baseEnv(), stdio: "inherit" })
  }
  console.log(`runs: ${out}\nwork folders: ${workRoot}${keep ? `\ninstance ${instance} kept; clean it with: attn instance clean ${instance}` : ""}`)
}
