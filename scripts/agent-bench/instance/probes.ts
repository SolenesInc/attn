import { execFileSync } from "node:child_process"
import { existsSync } from "node:fs"
import { join, resolve } from "node:path"
import { offers, openAsks } from "./asks.ts"
import { type Call, type Check, isCommand, isHelp, isUsageRefusal, reached, STAYS, type Task, type World } from "./world.ts"

type Probe = {
  id: string
  ask: string | ((w: World) => Promise<string> | string)
  /** The commands that can answer it, on either surface. Some call of one must succeed. */
  command: string | string[]
  mentions?: RegExp
  /** The skill reference the agent must read before it acts. */
  reads?: string
  launch?: "chief"
  restore?: Task["restore"]
  /** The command needs the app on screen, so a refusal other than for usage passes. */
  app?: true
  /** What the end state lacks, or "". */
  end?: (w: World, calls: Call[], ended: string) => Promise<string> | string
}

const seedTended = (title: string, body: string) => (w: World) => {
  const id: string = w.attnJson(["seed", "plant", title, "-m", body]).id
  w.attn(["seed", "tend", id], { session: w.session })
  w.state.seed = id
  return id
}
const seed = (w: World) => w.attnJson(["seed", "show", w.state.seed as string]).seed
const expect = (ok: boolean, problem: string) => (ok ? "" : problem)

const restoreSettings = (key: string): Task["restore"] => (attn) => attn(["settings", "set", key, ""])
const restoreRoles: Task["restore"] = (attn) => {
  const shown = attn(["delegate", "roles", "show", "--json", "--profile", "Default"])
  const table = { ...JSON.parse(shown.slice(shown.indexOf("{"))), enabled: false, roles: [] }
  attn(["delegate", "roles", "apply", "-", "-m", "bench reset", "--profile", "Default"], JSON.stringify(table))
}
const changeRole: Pick<Probe, "ask" | "command" | "restore" | "end"> = {
  ask: (w) => {
    w.attn(["delegate", "roles", "add", "bench-builder", "--name", "Builder", "--description", "Builds what a seed briefs.", "--instructions", "Build it.", "--agent", "claude", "--model", "claude-haiku-5-5", "-m", "bench"])
    return "Change the model that my Builder role uses to claude-sonnet-5-5."
  },
  command: ["delegate roles set", "delegate roles apply"],
  restore: restoreRoles,
  end: (w) => expect(JSON.stringify(w.attnJson(["delegate", "roles", "show"])).includes("claude-sonnet-5-5"), "the Builder role does not use claude-sonnet-5-5"),
}
const automation: Pick<Probe, "ask" | "command" | "end"> = {
  ask: "Every morning at nine, have an agent summarize my pull requests for this folder. Set it up now without asking me anything.",
  command: "automation apply",
  end: (w) => expect(w.attnJson(["automation", "list"]).length > 0, "no automation exists"),
}
const journal: Pick<Probe, "ask" | "command" | "mentions"> = { ask: "Add to my journal that we chose Postgres.", command: "journal append", mentions: /postgres/i }
const fernJoins = (w: World) => expect(w.attnJson(["crew", "list"]).some((m: any) => /fern/i.test(m.name)), "no crew member Fern")

const HOUR = 3_600_000
const until = (w: World) => new Date(seed(w).reminder?.at ?? 0)

const probes: Probe[] = [
  { id: "message-chief", ask: "Tell the Chief that the deploy finished.", command: "agent msg", mentions: /chief/i },
  { id: "list-agents", ask: "Which other agents are running right now?", command: ["agent list", "activity", "list"] },
  {
    id: "read-agent",
    ask: async (w) => {
      w.state.fern = await w.spawnShell("fern")
      return "What is the agent called fern doing? Read its screen."
    },
    command: ["agent peek", "terminal read"],
    end: (w, calls) => expect(calls.some((c) => isCommand(c, "terminal read") || (isCommand(c, "agent peek") && ([String(w.state.fern).slice(0, 8), w.attn(["session", "show", String(w.state.fern)]).split(" ")[0]!].some((id) => c.text.includes(id)) || /fern|\w\.id\b/.test(c.text)))), "read another screen than fern's"),
  },
  { id: "close-self", ask: "Close yourself. There is nothing left to do.", command: "agent close", reads: "wrap-up", end: (_w, _calls, ended) => expect(ended === "closed itself", "still open") },
  {
    id: "park-seed",
    ask: (w) => `Stop working on seed ${seedTended("Fix flaky checkout test", "The checkout test fails one run in ten.")(w)} for now and let someone else pick it up later.`,
    command: "seed park",
    end: (w) => expect(seed(w).status === "parked" && !seed(w).tender, "the seed is not parked with no tender"),
  },
  {
    id: "remind-seed",
    ask: (w) => `Leave a note that whoever tends seed ${seedTended("Deploy the importer", "Deploy the importer, then check the deploy.")(w)} in two hours should check the deploy.`,
    command: "seed remind",
    end: (w) => {
      const inHours = (until(w).getTime() - Date.now()) / HOUR
      return expect(/deploy/i.test(seed(w).reminder?.message ?? "") && inHours > 1.5 && inHours <= 2.01, "no reminder in two hours to check the deploy")
    },
  },
  {
    id: "standby-until",
    ask: (w) => `You tend seed ${seedTended("Rerun the backfill", "Rerun the orders backfill.")(w)}. Stay on standby till Friday, then rerun the backfill.`,
    command: "seed standby",
    end: (w, calls) => {
      if (!seed(w).standby) return "not on standby"
      const at = until(w)
      if (at.getDay() !== 5 || at.getTime() - Date.now() > 8 * 24 * HOUR) return `the reminder is ${seed(w).reminder?.at ?? "unset"}, not Friday`
      return expect(!calls.some((c) => isCommand(c, "seed standby") && STAYS.test(c.text)), "kept the session open with stay")
    },
  },
  { id: "can-close", ask: "Can I close you?", command: "skill", mentions: /wrap-up/ },
  { id: "open-shell", ask: "Open me a shell in /tmp.", command: "terminal open", end: (w) => expect(w.sessions().some((s) => s.directory === "/tmp"), "no shell runs in /tmp") },
  {
    id: "move-terminal",
    ask: "Move this terminal to desktop 3.",
    command: "terminal move",
    end: (w) => expect(JSON.stringify(w.attnJson(["terminal", "ls"], { session: w.session })).includes("desktop_3"), "the terminal is not on desktop 3"),
  },
  { id: "show-markdown", ask: "Show me plan.md in the app.", command: "open", mentions: /plan\.md/ },
  {
    id: "change-setting",
    ask: "Set the attn setting auto_settle_countdown_seconds to 7.",
    command: "settings set",
    restore: restoreSettings("auto_settle_countdown_seconds"),
    end: (w) => expect(w.instance(["settings", "get", "auto_settle_countdown_seconds"]).trim() === "7", "the setting is not 7"),
  },
  { id: "add-crew", ask: "Add a permanent agent named Fern to my crew.", command: "crew create", end: fernJoins },
  {
    id: "plant-seed",
    ask: "Track this for later and do not start it: the login test is flaky.",
    command: "seed plant",
    end: (w) => expect(w.attnJson(["seed", "ls"]).seeds.some((s: any) => s.status === "planted" && !s.claimed && /login/i.test(s.title)), "no planted, untended seed about the login test"),
  },
  {
    id: "ask-user",
    ask: "Ask me, in the way that lets you keep working, whether to use Postgres or Redis.",
    command: "ask",
    end: (w) => expect(offers(openAsks(w), /postgres/i, /redis/i) !== undefined, "no open ask offers Postgres and Redis"),
  },
  { id: "queue", ask: "A message from another agent started this run, so I will not see it when you stop. You have a result I must review: bring me to you.", command: "turn open" },
  { id: "journal", ...journal },
  { id: "browser", ask: "Open example.com in the in-app browser.", command: "browser open", mentions: /example\.com/ },
  { id: "callout", ask: "Point at the command palette in the app so I can see it.", command: ["callout show", "callout anchors"], app: true },
  { id: "automation", ...automation },
  {
    id: "delegate",
    ask: "I authorize you to delegate this: have another agent fix the flaky login test.",
    command: "delegate",
    reads: "delegation",
    end: async (w, calls) => expect((await w.delegated(w.dir, calls)).length > 0, "no delegate started"),
  },
  { id: "roles", ...changeRole },
  { id: "guide", ask: "How do crew members work in attn? Use the documentation.", command: "guide" },
  { id: "rename-session", ask: "Rename this session to login tests.", command: "session rename", end: (w) => expect(w.sessions().some((s) => s.id === w.session && s.label === "login tests"), "the session is not named login tests") },
  { id: "chief-journal", launch: "chief", ...journal },
  { id: "chief-crew", launch: "chief", ask: "I want a permanent agent named Fern for research.", command: "crew create", end: fernJoins },
  { id: "chief-roles", launch: "chief", ...changeRole },
  { id: "chief-settings", launch: "chief", ask: "What attn settings do I have?", command: "settings list" },
  { id: "chief-automation", launch: "chief", ...automation },
]

const probeTask = (p: Probe): Task => ({
  id: p.id,
  title: `Probe: ${typeof p.ask === "string" ? p.ask : p.id}`,
  launch: p.launch,
  restore: p.restore,
  setup: async (w) => {
    w.sh("printf '# Plan\\n' > plan.md")
    return typeof p.ask === "string" ? p.ask : await p.ask(w)
  },
  check: async (w, calls, ended) => {
    const mine = calls.filter((c) => isCommand(c, p.command) && !isHelp(c))
    const done = reached(calls, p.command, p.mentions) || (p.app === true && mine.some((c) => !isUsageRefusal(c)))
    const problems: string[] = []
    if (!done) problems.push(mine.length === 0 ? `never reached ${p.command}` : `no ${p.command} call succeeded: ${(mine.at(-1)!.refused ?? mine.at(-1)!.text).slice(0, 160)}`)
    if (p.reads && !reached(calls, "skill", new RegExp(p.reads))) problems.push(`skipped the ${p.reads} reference`)
    const lacking = await p.end?.(w, calls, ended)
    if (lacking) problems.push(lacking)
    return problems.length > 0 ? { pass: false, why: problems.join("; ") } : { pass: true, why: `reached ${p.command}` }
  },
})

const repository = resolve(import.meta.dir, "../../..")
const presentedChange = "3982a161b"

const presentChange: Task = {
  id: "present-change",
  title: "Probe: present an uncommitted change for review",
  settings: { code_review_enabled: "true" },
  stage: (dir) => {
    const clone = join(dir, "attn")
    execFileSync("git", ["clone", "--quiet", "--shared", "--no-checkout", repository, clone])
    execFileSync("git", ["-C", clone, "checkout", "--quiet", "--detach", `${presentedChange}~1`])
    execFileSync("git", ["-C", clone, "apply"], { input: execFileSync("git", ["-C", repository, "diff", `${presentedChange}~1`, presentedChange]) })
    return clone
  },
  setup: () => "Present the change.",
  check: (w, calls): Check => {
    const opened = calls.filter((c) => isCommand(c, "open")).at(-1)
    if (!opened) return { pass: false, why: "never opened a review document" }
    if (!reached(calls, "skill", /review/)) return { pass: false, why: "skipped the review reference" }
    const path = opened.text.match(/([^\s"']+\.md)/)?.[1]
    const clone = w.dir
    const document = path === undefined ? undefined : resolve(clone, path)
    if (document === undefined || !existsSync(document)) return { pass: false, why: `opened ${path ?? opened.text.slice(0, 80)}, which does not exist` }
    try {
      w.attn(["review", "check", document], { cwd: clone })
      return { pass: true, why: `presented ${document}` }
    } catch (error) {
      return { pass: false, why: `attn review check: ${String((error as Error).message).split("\n").slice(0, 3).join("; ")}` }
    }
  },
}

export const PROBES: Task[] = [...probes.map(probeTask), presentChange]
