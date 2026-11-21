// The bench's tasks. Each sets up the computer, sends one prompt per process (a later prompt resumes the session in
// a new Claude Code process), and ends with a check a program makes.
import type { World } from "./attn-mock.ts"
import type { makeAttn } from "./attn-mock.ts"

export type Computer = { sh: (command: string, stdin?: string) => { out: string; code: number | null } }
export type Ctx = { computer: Computer; attn: ReturnType<typeof makeAttn>; answers: string[] }
export type Check = { pass: boolean; why: string }
export type Task = {
  id: string
  title: string
  world: World
  setup: (computer: Computer) => { prompts: string[]; check: (ctx: Ctx) => Check }
}

const repos = {
  "github.com/acme/calc": { private: false, granted: false },
  "github.com/acme/api": { private: true, granted: true },
  "github.com/acme/web": { private: true, granted: true },
  "github.com/acme/billing": { private: true, granted: false },
}
const world: World = { member: "scout", repos }

const delegatedTo = (ctx: Ctx, repo: string) => ctx.attn.delegations.some((d) => d.repo === repo)

const delegateFix: Task = {
  id: "delegate-fix",
  title: "Delegate a fix naming only the repository",
  world,
  setup: () => ({
    prompts: ["In acme/calc, `add_all` drops the last number of the list it is given. Get it fixed."],
    check: (ctx) =>
      delegatedTo(ctx, "github.com/acme/calc")
        ? { pass: true, why: "delegated to github.com/acme/calc" }
        : { pass: false, why: `no delegation to github.com/acme/calc; delegations: ${JSON.stringify(ctx.attn.delegations.map((d) => d.repo))}` },
  }),
}

const missingGrant: Task = {
  id: "missing-grant",
  title: "Meet a refusal that names a missing grant and ask for it",
  world,
  setup: () => ({
    prompts: ["In acme/billing, invoice totals are rounded down instead of to the nearest cent. Get it fixed."],
    check: (ctx) => {
      const ask = ctx.attn.asks.find((text) => text.includes("acme/billing") && text.includes("read_repo"))
      return ask
        ? { pass: true, why: "asked for read_repo on acme/billing" }
        : { pass: false, why: `no ask names read_repo and acme/billing; asks: ${JSON.stringify(ctx.attn.asks.map((a) => a.slice(0, 200)))}` }
    },
  }),
}

const twoRepos: Task = {
  id: "two-repos",
  title: "A change that spans two repositories",
  world,
  setup: () => ({
    prompts: [
      "Add a `currency` field to the invoice response in acme/api, and show it on the invoice page in acme/web. Get both changes made.",
    ],
    check: (ctx) => {
      const api = delegatedTo(ctx, "github.com/acme/api")
      const web = delegatedTo(ctx, "github.com/acme/web")
      return api && web
        ? { pass: true, why: "delegated to acme/api and acme/web" }
        : { pass: false, why: `api=${api} web=${web}; delegations: ${JSON.stringify(ctx.attn.delegations.map((d) => d.repo))}` }
    },
  }),
}

// Logs whose ERROR codes have distinct counts; WARN lines carry codes too, more often than any error, as a trap.
const logs: Record<string, string[]> = {
  "api-1.log": ["ERROR E203 payment declined", "INFO request ok", "ERROR E203 payment declined", "WARN E900 slow upstream", "ERROR E117 timeout"],
  "api-2.log": ["INFO request ok", "WARN E900 slow upstream", "WARN E900 slow upstream", "ERROR E203 payment declined", "ERROR E450 bad input"],
  "worker-1.log": ["INFO job done", "INFO job done", "WARN E900 slow upstream"],
  "worker-2.log": ["ERROR E117 timeout", "ERROR E203 payment declined", "WARN E900 slow upstream", "ERROR E117 timeout"],
  "cron.log": ["INFO tick", "INFO tick", "WARN E901 skipped run"],
  "gateway.log": ["ERROR E450 bad input", "WARN E900 slow upstream", "WARN E900 slow upstream", "ERROR E203 payment declined"],
}

const chore: Task = {
  id: "chore",
  title: "A multi-step chore on the computer",
  world,
  setup: (computer) => {
    let n = 0
    for (const [name, lines] of Object.entries(logs)) {
      const text = lines.map((line) => `2026-11-01T10:${String(n++ % 60).padStart(2, "0")}:00Z ${line}`).join("\n") + "\n"
      computer.sh(`mkdir -p ~/logs && cat > ~/logs/${name}`, text)
    }
    const counts = new Map<string, number>()
    for (const lines of Object.values(logs))
      for (const line of lines) {
        const m = line.match(/^ERROR (\S+)/)
        if (m) counts.set(m[1]!, (counts.get(m[1]!) ?? 0) + 1)
      }
    const expected = [...counts].sort((a, b) => b[1] - a[1]).slice(0, 3).map(([code, count]) => `${code} ${count}`)
    const clean = Object.entries(logs).filter(([, lines]) => !lines.some((l) => l.startsWith("ERROR"))).map(([name]) => name).sort()
    const dirty = Object.keys(logs).filter((name) => !clean.includes(name)).sort()
    return {
      prompts: [
        "On your computer, ~/logs holds service logs. Write ~/report.txt with the three most frequent ERROR codes across all the logs, one per line as `<code> <count>`, most frequent first. Then move each log file that has no ERROR line into ~/logs/clean/.",
      ],
      check: () => {
        const report = computer.sh("cat ~/report.txt").out.trim().split("\n").map((l) => l.trim())
        const inClean = computer.sh("ls ~/logs/clean 2>/dev/null").out.trim().split("\n").filter(Boolean).sort()
        const left = computer.sh("ls -p ~/logs | grep -v /").out.trim().split("\n").filter(Boolean).sort()
        const fails: string[] = []
        if (JSON.stringify(report) !== JSON.stringify(expected)) fails.push(`report ${JSON.stringify(report)} != ${JSON.stringify(expected)}`)
        if (JSON.stringify(inClean) !== JSON.stringify(clean)) fails.push(`clean ${JSON.stringify(inClean)} != ${JSON.stringify(clean)}`)
        if (JSON.stringify(left) !== JSON.stringify(dirty)) fails.push(`left ${JSON.stringify(left)} != ${JSON.stringify(dirty)}`)
        return fails.length === 0 ? { pass: true, why: "report and moves exact" } : { pass: false, why: fails.join("; ") }
      },
    }
  },
}

const words = ["harbor", "lantern", "meadow", "quartz", "falcon", "ember", "willow", "copper"]
const resume: Task = {
  id: "resume",
  title: "Resume in a new process",
  world,
  setup: () => {
    const word = `${words[Math.floor(Math.random() * words.length)]}-${1000 + Math.floor(Math.random() * 9000)}`
    return {
      prompts: [
        `The code word for this job is ${word}. Don't write it anywhere; just reply "ok".`,
        "Write the code word I gave you to ~/codeword.txt on your computer.",
      ],
      check: (ctx) => {
        const got = ctx.computer.sh("cat ~/codeword.txt").out.trim()
        const leaked = ctx.computer.sh(`grep -rl -- '${word}' ~ 2>/dev/null | grep -v codeword.txt`).out.trim()
        if (got !== word) return { pass: false, why: `codeword.txt holds ${JSON.stringify(got.slice(0, 80))}, expected ${word}` }
        if (leaked !== "") return { pass: false, why: `the word was also written to ${leaked}` }
        return { pass: true, why: `the second process wrote ${word}` }
      },
    }
  },
}

export const TASKS: Task[] = [delegateFix, missingGrant, twoRepos, chore, resume]
