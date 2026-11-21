import { execFileSync } from "node:child_process"
import { existsSync } from "node:fs"
import { join, resolve } from "node:path"
import { type Call, type Check, isCommand, reached, type Task, type World } from "./world.ts"

type Probe = {
  id: string
  ask: string
  command: string
  mentions?: RegExp
  /** The skill reference the agent must read before it acts. */
  reads?: string
  launch?: "chief"
  judge?: (w: World, calls: Call[]) => string
}

const probes: Probe[] = [
  { id: "message-chief", ask: "Tell the Chief that the deploy finished.", command: "agent msg", mentions: /chief/i },
  { id: "list-agents", ask: "Which other agents are running right now?", command: "agent list" },
  { id: "read-agent", ask: "What is the agent called fern doing? Read its screen.", command: "agent peek", mentions: /fern/ },
  { id: "close-self", ask: "Close yourself. There is nothing left to do.", command: "agent close", mentions: /self/, reads: "wrap-up" },
  { id: "park-seed", ask: "Stop working on seed s-abc123 for now and let someone else pick it up later.", command: "seed park", mentions: /s-abc123/ },
  { id: "remind-seed", ask: "Leave a note that whoever tends seed s-abc123 in two hours should check the deploy.", command: "seed remind", mentions: /s-abc123/ },
  {
    id: "standby-until",
    ask: "You tend seed s-abc123. Stay on standby till Friday, then rerun the backfill.",
    command: "seed standby",
    mentions: /s-abc123/,
    judge: (_w, calls) => {
      const waits = calls.filter((c) => (isCommand(c, "seed standby") || isCommand(c, "seed remind")) && !/--help/.test(c.text))
      if (waits.length === 0) return "no standby or reminder"
      if (!waits.some((c) => /(--at\s|"at")/.test(c.text))) return "no standby names a time"
      if (waits.some((c) => /(--stay\b|"stay":\s*true)/.test(c.text))) return "kept the session open with stay"
      return ""
    },
  },
  { id: "can-close", ask: "Can I close you?", command: "skill", mentions: /wrap-up/ },
  { id: "open-shell", ask: "Open me a shell in /tmp.", command: "terminal open", mentions: /\/tmp/ },
  { id: "move-terminal", ask: "Move this terminal to desktop 3.", command: "terminal move", mentions: /\b3\b/ },
  { id: "show-markdown", ask: "Show me plan.md in the app.", command: "open", mentions: /plan\.md/ },
  { id: "change-setting", ask: "Set the attn setting asks_enabled to true.", command: "settings set", mentions: /asks_enabled/ },
  { id: "add-crew", ask: "Add a permanent agent named Fern to my crew.", command: "crew create", mentions: /fern/i },
  { id: "plant-seed", ask: "Track this for later and do not start it: the login test is flaky.", command: "seed plant" },
  { id: "ask-user", ask: "Ask me, in the way that lets you keep working, whether to use Postgres or Redis.", command: "ask" },
  { id: "queue", ask: "A notification from attn started this run, and attn agent inbox says your stop will not show. You have a result I must review.", command: "turn open" },
  { id: "journal", ask: "Add to my journal that we chose Postgres.", command: "journal append" },
  { id: "browser", ask: "Open example.com in the in-app browser.", command: "browser open", mentions: /example\.com/ },
  { id: "callout", ask: "Point at the command palette in the app so I can see it.", command: "callout show" },
  {
    id: "automation",
    ask: "Every morning at nine, have an agent summarize my pull requests for this folder. Set it up now without asking me anything.",
    command: "automation",
    mentions: /apply|validate/,
  },
  { id: "delegate", ask: "I authorize you to delegate this: have another agent fix the flaky login test.", command: "delegate", reads: "delegation" },
  { id: "roles", ask: "Change the model that my Builder role uses.", command: "delegate roles" },
  { id: "guide", ask: "How do crew members work in attn? Use the documentation.", command: "guide" },
  { id: "rename-session", ask: "Rename this session to login tests.", command: "session rename" },
  { id: "chief-journal", launch: "chief", ask: "Add to my journal that we chose Postgres.", command: "journal append" },
  { id: "chief-crew", launch: "chief", ask: "I want a permanent agent named Fern for research.", command: "crew create", mentions: /fern/i },
  { id: "chief-roles", launch: "chief", ask: "Change the model that my Builder role uses.", command: "delegate roles" },
  { id: "chief-settings", launch: "chief", ask: "What attn settings do I have?", command: "settings list" },
  {
    id: "chief-automation",
    launch: "chief",
    ask: "Every morning at nine, have an agent summarize my pull requests for this folder. Set it up now without asking me anything.",
    command: "automation",
    mentions: /apply|validate/,
  },
]

const probeTask = (p: Probe): Task => ({
  id: p.id,
  title: `Probe: ${p.ask}`,
  launch: p.launch,
  setup: (w) => {
    w.sh("printf '# Plan\\n' > plan.md")
    return p.ask
  },
  check: (w, calls) => {
    const real = calls.filter((c) => !/(^|\s)--help(\s|$)/.test(c.text))
    if (!reached(real, p.command, p.mentions)) return { pass: false, why: `never reached ${p.command}${p.mentions ? ` ${p.mentions}` : ""}` }
    if (p.reads && !reached(calls, "skill", new RegExp(p.reads))) return { pass: false, why: `skipped the ${p.reads} reference` }
    const problem = p.judge?.(w, calls) ?? ""
    return problem ? { pass: false, why: problem } : { pass: true, why: `reached ${p.command}` }
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
