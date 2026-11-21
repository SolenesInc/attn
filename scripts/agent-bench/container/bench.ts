// Bench for limited-agent tasks. A limited agent is Claude Code driven through the Agent SDK with its own tools off
// (`tools: []`), sigma's shell and file tools served over MCP from inside a throwaway container (computer-mcp.ts),
// and a mocked attn tool (attn-mock.ts). Each run gets its own container, stopped by the id captured at start.
//
//   bun bench.ts                                  every task, both models, 5 runs per cell
//   bun bench.ts --tasks chore,resume --models claude-sonnet-5-5 --runs 1 --parallel 2
//   bun bench.ts --role bare                      the spikes' instructions, with no role that authorizes delegating
//   bun bench.ts --claude-config ~/.claude        run in your own Claude config, for a Linux sign-in (README.md)
//
// Env: SIGMA_REPO, the sigma (nisse) checkout whose packages serve the tools and whose node_modules hold the Agent
// SDK (default ~/projects/victor/sigma). Results go to ./runs/<stamp>/: one folder per run with the transcript,
// the attn calls and the check, then results.json and table.md.
import { spawnSync } from "node:child_process"
import { mkdirSync, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"
import { ATTN_DESCRIPTION, makeAttn } from "./attn-mock.ts"
import { type Computer, TASKS, type Task } from "./tasks.ts"

const here = import.meta.dir
const sigma = process.env.SIGMA_REPO ?? join(homedir(), "projects/victor/sigma")
const { query, createSdkMcpServer, tool } = await import(join(sigma, "node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs"))
const { z } = await import(join(sigma, "node_modules/zod/index.js"))

const IMAGE = "sigma-bench-computer:1"
/** Tripwires. The spikes finished in 3 to 8 turns and under 30 s; a run that reaches these is broken, and says so. */
const MAX_TURNS = 40
const RUN_DEADLINE_MS = 10 * 60_000

const arg = (name: string) => {
  const i = process.argv.indexOf(`--${name}`)
  return i >= 0 ? process.argv[i + 1] : undefined
}
const models = (arg("models") ?? "claude-sonnet-5-5,claude-opus-5-5").split(",")
const runs = Number(arg("runs") ?? 5)
const parallel = Number(arg("parallel") ?? 3)
const sharedClaudeConfig = arg("claude-config")
const tasks = arg("tasks") ? TASKS.filter((t) => arg("tasks")!.split(",").includes(t.id)) : TASKS
if (tasks.length === 0) throw new Error(`no task matches --tasks ${arg("tasks")}; tasks: ${TASKS.map((t) => t.id).join(", ")}`)

/** What the agent is told besides Claude Code's preset. `bare` is the spikes' wording; `member` adds the role a crew
 * member's charter gives it (spike4's "guided"), which authorizes delegating. */
const BARE =
  "You run on a host where you have no tools of your own. You have been given a separate computer, a Linux machine, to use through the mcp__computer__ tools, and the attn tool."
const ROLES: Record<string, string> = {
  bare: BARE,
  member:
    BARE +
    " You are a crew member who coordinates work. Your computer is for looking things up, trying things and small chores. Changes to code are made by other agents that you start through attn; you do not make them yourself.",
}
const role = arg("role") ?? "member"
const INSTRUCTIONS = ROLES[role]
if (INSTRUCTIONS === undefined) throw new Error(`no role named ${role}; roles: ${Object.keys(ROLES).join(", ")}`)

const docker = (args: string[], input?: string) =>
  spawnSync("docker", args, { input: input ?? "", encoding: "utf8", timeout: 120_000, maxBuffer: 64 << 20 })

function build() {
  const r = docker(["build", "-q", "-t", IMAGE, here])
  if (r.status !== 0) throw new Error(`docker build failed: ${r.stderr}`)
}

const sigmaCommit = spawnSync("git", ["-C", sigma, "rev-parse", "--short", "HEAD"], { encoding: "utf8" }).stdout.trim()
const sdkVersion = (await Bun.file(join(sigma, "node_modules/@anthropic-ai/claude-agent-sdk/package.json")).json()).version

type Usage = { input: number; cacheRead: number; cacheWrite: number; output: number }
type RunResult = {
  task: string
  model: string
  n: number
  pass: boolean
  why: string
  turns: number
  tokens: Usage
  cost: number
  ms: number
  ended: string[]
}

async function runOnce(task: Task, model: string, n: number, dir: string): Promise<RunResult> {
  mkdirSync(dir, { recursive: true })
  const started = Date.now()
  const up = docker(["run", "-d", "--rm", "--network", "none", "-v", `${sigma}:/sigma:ro`, "-v", `${here}:/bench:ro`, IMAGE, "sleep", "infinity"])
  const container = up.stdout.trim()
  if (up.status !== 0 || container === "") throw new Error(`docker run failed: ${up.stderr}`)
  const computer: Computer = {
    sh: (command, stdin) => {
      const r = docker(["exec", "-i", container, "bash", "-c", command], stdin)
      return { out: `${r.stdout}${r.stderr}`, code: r.status }
    },
  }
  const transcript: unknown[] = []
  const totals: Usage = { input: 0, cacheRead: 0, cacheWrite: 0, output: 0 }
  let turns = 0
  let cost = 0
  const ended: string[] = []
  const answers: string[] = []
  const attn = makeAttn(task.world, (path) => computer.sh(`cat -- ${JSON.stringify(path)}`).out)
  try {
    const { prompts, check } = task.setup(computer)
    const config = sharedClaudeConfig ?? join(dir, "claude-config")
    const loophost = join(dir, "loophost")
    mkdirSync(config, { recursive: true })
    mkdirSync(loophost, { recursive: true })
    let session: string | undefined
    for (const prompt of prompts) {
      // A new SDK MCP server per process: one instance serves one connection.
      const attnServer = createSdkMcpServer({
        name: "attn",
        version: "0.0.1",
        tools: [
          tool("attn", ATTN_DESCRIPTION, { args: z.array(z.string()), stdin: z.string().optional() }, async ({ args, stdin }: { args: string[]; stdin?: string }) => ({
            content: [{ type: "text" as const, text: attn.run(args, stdin) }],
          })),
        ],
      })
      const controller = new AbortController()
      const deadline = setTimeout(() => controller.abort(), RUN_DEADLINE_MS - (Date.now() - started))
      const messages = query({
        prompt,
        options: {
          cwd: loophost,
          model,
          abortController: controller,
          settingSources: [],
          maxTurns: MAX_TURNS,
          env: {
            PATH: process.env.PATH!,
            HOME: process.env.HOME!,
            USER: process.env.USER!,
            CLAUDE_CONFIG_DIR: config,
            ...(process.env.CLAUDE_CODE_OAUTH_TOKEN ? { CLAUDE_CODE_OAUTH_TOKEN: process.env.CLAUDE_CODE_OAUTH_TOKEN } : {}),
            CLAUDE_AGENT_SDK_CLIENT_APP: "sigma-bench",
            ENABLE_CLAUDEAI_MCP_SERVERS: "false",
          },
          tools: [],
          mcpServers: {
            computer: { type: "stdio", command: "docker", args: ["exec", "-i", "-e", "COMPUTER_CWD=/root", container, "bun", "/bench/computer-mcp.ts"] },
            attn: attnServer,
          },
          permissionMode: "bypassPermissions",
          allowDangerouslySkipPermissions: true,
          systemPrompt: { type: "preset", preset: "claude_code", append: INSTRUCTIONS },
          ...(session === undefined ? {} : { resume: session }),
        },
      })
      try {
        for await (const m of messages as AsyncIterable<any>) {
          transcript.push(m)
          if (m.type === "system" && m.subtype === "init") session = m.session_id
          if (m.type === "result") {
            turns += m.num_turns ?? 0
            cost += m.total_cost_usd ?? 0
            totals.input += m.usage?.input_tokens ?? 0
            totals.cacheRead += m.usage?.cache_read_input_tokens ?? 0
            totals.cacheWrite += m.usage?.cache_creation_input_tokens ?? 0
            totals.output += m.usage?.output_tokens ?? 0
            ended.push(m.subtype)
            if (typeof m.result === "string") answers.push(m.result)
          }
        }
      } catch (error) {
        ended.push(controller.signal.aborted ? `deadline ${RUN_DEADLINE_MS / 1000}s` : `error: ${(error as Error).message}`)
      } finally {
        clearTimeout(deadline)
      }
    }
    const checked = check({ computer, attn, answers })
    const stopped = ended.filter((e) => e !== "success")
    const verdict = stopped.length === 0 ? checked : { pass: checked.pass, why: `${stopped.join(", ")}; ${checked.why}` }
    return { task: task.id, model, n, ...verdict, turns, tokens: totals, cost, ms: Date.now() - started, ended }
  } finally {
    writeFileSync(join(dir, "transcript.jsonl"), transcript.map((m) => JSON.stringify(m)).join("\n") + "\n")
    writeFileSync(join(dir, "attn.json"), JSON.stringify(attn.calls, null, 1))
    docker(["rm", "-f", container])
  }
}

const sum = (u: Usage) => u.input + u.cacheRead + u.cacheWrite + u.output
const median = (xs: number[]) => {
  const s = [...xs].sort((a, b) => a - b)
  return s.length === 0 ? 0 : s.length % 2 ? s[(s.length - 1) / 2]! : (s[s.length / 2 - 1]! + s[s.length / 2]!) / 2
}

function table(results: RunResult[]) {
  const lines = [
    `Smoke test: ${runs} run(s) per cell, too few to separate models or wordings; a difference smaller than two runs is noise.`,
    `role \`${role}\`, sigma ${sigmaCommit}, Agent SDK ${sdkVersion}, ${new Date().toISOString().slice(0, 10)}. Tokens are per run: input + cache read + cache write + output; turns and tokens are medians, cost is the mean per run.`,
    "",
    "| task | model | pass | turns | tokens | of which output | cost/run | failures |",
    "|---|---|---|---|---|---|---|---|",
  ]
  for (const task of tasks)
    for (const model of models) {
      const cell = results.filter((r) => r.task === task.id && r.model === model)
      if (cell.length === 0) continue
      const pass = cell.filter((r) => r.pass).length
      const fails = cell.filter((r) => !r.pass).map((r) => `#${r.n}: ${r.why}`.slice(0, 160))
      lines.push(
        `| ${task.id} | ${model} | ${pass}/${cell.length} | ${median(cell.map((r) => r.turns))} | ${Math.round(median(cell.map((r) => sum(r.tokens)))).toLocaleString("en")} | ${Math.round(median(cell.map((r) => r.tokens.output))).toLocaleString("en")} | $${(cell.reduce((a, r) => a + r.cost, 0) / cell.length).toFixed(3)} | ${fails.join("<br>") || "-"} |`,
      )
    }
  return lines.join("\n")
}

build()
const out = join(here, "runs", new Date().toISOString().replace(/[:.]/g, "-"))
mkdirSync(out, { recursive: true })
const jobs = tasks.flatMap((task) => models.flatMap((model) => Array.from({ length: runs }, (_, i) => ({ task, model, n: i + 1 }))))
const results: RunResult[] = []
let next = 0
await Promise.all(
  Array.from({ length: Math.min(parallel, jobs.length) }, async () => {
    while (next < jobs.length) {
      const job = jobs[next++]!
      const r = await runOnce(job.task, job.model, job.n, join(out, job.task.id, job.model, String(job.n)))
      writeFileSync(join(out, job.task.id, job.model, String(job.n), "result.json"), JSON.stringify(r, null, 1))
      results.push(r)
      console.error(`${r.pass ? "PASS" : "FAIL"} ${r.task} ${r.model} #${r.n} turns=${r.turns} cost=$${r.cost.toFixed(3)} ${Math.round(r.ms / 1000)}s ${r.pass ? "" : r.why}`)
    }
  }),
)
writeFileSync(join(out, "results.json"), JSON.stringify({ role, sigma: sigmaCommit, sdk: sdkVersion, models, runs, results }, null, 1))
const md = table(results)
writeFileSync(join(out, "table.md"), md + "\n")
console.log(md)
console.log(`\nruns: ${out}`)
