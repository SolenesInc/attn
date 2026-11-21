import { join } from "node:path"
import { type Check, isCommand, type Task, type World } from "./world.ts"

const verdict = (problems: string[], ok: string): Check =>
  problems.length === 0 ? { pass: true, why: ok } : { pass: false, why: problems.join("; ") }

const plant = (w: World, title: string, body: string, extra: string[] = []): string =>
  w.attnJson(["seed", "plant", title, "-m", body, ...extra]).id

const show = (w: World, seed: string) => w.attnJson(["seed", "show", seed])

const SYSTEM_NOTE = /^(Session .* was closed|On standby\.)/
const notesBy = (w: World, seed: string, session: string): string[] =>
  w.attnJson(["seed", "notes", seed])
    .notes.filter((n: any) => n.kind === "note" && n.author?.ref === `session:${session}` && !SYSTEM_NOTE.test(n.body))
    .map((n: any) => String(n.body))

const progress: Task = {
  id: "progress",
  title: "Record progress and report one check with evidence",
  setup: (w) => {
    const seed = plant(w, "Retry failed uploads sync", "Failed uploads retry with exponential backoff.", [
      "--check",
      "Failed uploads retry with backoff",
      "--check",
      "Retries stop after five attempts",
    ])
    w.attn(["seed", "tend", seed], { session: w.session })
    w.sh("cat > retry-test.log", "PASS TestRetryBackoff (3 retries: 1s, 2s, 4s)\nPASS TestRetryGivesUpOnSuccess\n")
    w.state.seed = seed
    return `Your assignment is seed ${seed} (retry-failed-uploads-sync). The retry change is implemented, and retry-test.log in your working directory is the test run that shows failed uploads retrying with backoff. Record your progress on the seed, and report check C1 with that log as its evidence. C2 is not done yet, so leave it and the seed open.`
  },
  check: (w) => {
    const seed = w.state.seed as string
    const s = show(w, seed).seed
    const [c1, c2] = s.checks
    const problems: string[] = []
    if (notesBy(w, seed, w.session).length === 0) problems.push(`no progress note on ${seed}`)
    if (c1.state !== "reported") problems.push(`C1 is ${c1.state}, not reported`)
    else if (!JSON.stringify(c1.report.evidence).includes("retry-test.log")) problems.push("C1's evidence lacks retry-test.log")
    if (c2.state !== "open") problems.push(`C2 should stay open, is ${c2.state}`)
    if (s.status !== "growing") problems.push(`${seed} should still be growing, is ${s.status}`)
    return verdict(problems, "note, C1 reported with the log, C2 and the seed open")
  },
}

const owners: Record<string, string> = {
  "Redis cache adapter": "internal/cache/redis.go",
  "In-memory cache": "internal/cache/memory.go",
  "Cache metrics": "internal/cache/metrics.go",
  "Invalidate on deploy": "internal/deploy/invalidate.go",
}
const closedChildren: Record<string, string> = { "Cache key design": "internal/cache/keys.go", "Cache benchmark spike": "bench/cache_test.go" }

const plotNotes: Task = {
  id: "plot-notes",
  title: "Note each open child of a plot, leave closed ones alone",
  setup: (w) => {
    const child = (title: string, path: string) => ({ title, body: `This child owns ${path}. Change only that file.` })
    const plot = w.attnJson(["seed", "plot", "-f", "-"], {
      stdin: JSON.stringify({
        title: "Cache layer rollout",
        body: "Roll out a cache layer in front of the database, one owner file per child.",
        children: [...Object.entries(owners), ...Object.entries(closedChildren)].map(([title, path]) => child(title, path)),
      }),
    })
    const children: { id: string; title: string }[] = plot.children
    const ids = Object.fromEntries(children.map((c) => [c.title, c.id]))
    w.attn(["seed", "harvest", ids["Cache key design"]!, "-m", "Keys designed."])
    w.attn(["seed", "wither", ids["Cache benchmark spike"]!, "-m", "Not needed."])
    w.state.ids = ids
    return "For every open child of the plot cache-layer-rollout, read the child and add a note to it that names the file its body says it owns, in the form \"Owner file: <path>\". Leave closed children alone."
  },
  check: (w) => {
    const ids = w.state.ids as Record<string, string>
    const problems: string[] = []
    for (const [title, path] of Object.entries(owners))
      if (!notesBy(w, ids[title]!, w.session).some((n) => n.includes(path))) problems.push(`no note naming ${path} on ${ids[title]}`)
    for (const title of Object.keys(closedChildren))
      if (notesBy(w, ids[title]!, w.session).length > 0) problems.push(`noted the closed child ${ids[title]}`)
    return verdict(problems, "each open child noted with its file, closed ones untouched")
  },
}

const ask: Task = {
  id: "ask",
  title: "Put a decision to the user with a recommendation",
  setup: () =>
    "I need a decision from the user, not from you: should session data be stored in Postgres or in SQLite? Postgres is already deployed for the API; SQLite would mean no extra service for the desktop build. Recommend Postgres. Put the question to the user through attn, then you are done for now.",
  check: (w) => {
    const asks = JSON.stringify(w.attnJson(["ask", "list"], { session: w.session })).toLowerCase()
    const problems: string[] = []
    if (!asks.includes("postgres") || !asks.includes("sqlite")) problems.push(`no open ask names Postgres and SQLite: ${asks.slice(0, 200)}`)
    else if (!/"recommended[a-z_]*":\s*"[^"]*postgres/.test(asks)) problems.push("the ask recommends no Postgres choice")
    return verdict(problems, "asked Postgres or SQLite, recommending Postgres")
  },
}

const msgDelegate: Task = {
  id: "msg-delegate",
  title: "Message a named agent, then delegate into a plain folder",
  setup: async (w) => {
    w.state.reviewer = await w.spawnShell("api-review")
    w.sh("mkdir -p docs")
    return `The api-v2 branch is ready for review. Tell the agent named api-review that it can start reviewing api-v2. Then delegate writing the migration guide for api-v2 to a new agent working in ${join(w.dir, "docs")} (a plain folder, not a Git repository). You are authorized to start that delegation.`
  },
  check: (w) => {
    const inbox = JSON.stringify(w.attnJson(["agent", "inbox", "--limit", "50"], { session: w.state.reviewer as string })).toLowerCase()
    const docs = join(w.dir, "docs")
    const delegates = w.sessions().filter((s) => s.directory === docs)
    const problems: string[] = []
    if (!inbox.includes("api-v2")) problems.push("api-review got no message naming api-v2")
    if (delegates.length !== 1) problems.push(`${delegates.length} sessions in ${docs}, want one delegation`)
    else if (!JSON.stringify(w.attnJson(["seed", "search", "migration", "guide"])).toLowerCase().includes("migration guide"))
      problems.push("no seed briefs the migration guide")
    return verdict(problems, "messaged api-review and delegated the guide into docs")
  },
}

const standby: Task = {
  id: "standby",
  title: "Go on standby with a note for the next wake",
  setup: (w) => {
    const seed = plant(w, "Ship CSV importer", "Ship the CSV importer: pull request #482, review, merge, deploy.")
    w.attn(["seed", "tend", seed], { session: w.session })
    w.state.seed = seed
    return `Your seed ship-csv-importer (${seed}) is waiting on review of pull request #482, and there is nothing else to do until the reviewer replies. Do not keep this session open while you wait: arrange to be brought back when something arrives on the seed, with a note telling your future self to merge #482 once it is approved and then follow the deploy.`
  },
  check: (w, calls) => {
    const note = String(show(w, w.state.seed as string).seed.standby?.note ?? "")
    const problems: string[] = []
    if (!/482|merge/i.test(note)) problems.push(`not on standby with a note to merge #482: ${JSON.stringify(note.slice(0, 120))}`)
    if (calls.some((c) => isCommand(c, "agent close"))) problems.push("called agent close instead of relying on standby")
    return verdict(problems, "on standby with a note to merge #482")
  },
}

const coding: Task = {
  id: "coding",
  title: "A coding task that needs no attn",
  setup: () => "Write fizzbuzz.py in the current directory that prints FizzBuzz for the numbers 1 to 15, one per line, run it, and show me the output.",
  check: (w, calls) => {
    const out = w.sh("python3 fizzbuzz.py").out.trim().split("\n")
    const want = Array.from({ length: 15 }, (_, i) => ((i + 1) % 15 === 0 ? "FizzBuzz" : (i + 1) % 3 === 0 ? "Fizz" : (i + 1) % 5 === 0 ? "Buzz" : String(i + 1)))
    const problems: string[] = []
    if (JSON.stringify(out) !== JSON.stringify(want)) problems.push(`fizzbuzz.py prints ${JSON.stringify(out.slice(0, 4))}…`)
    if (calls.length > 0) problems.push(`used attn during a coding task: ${calls.map((c) => c.command).join(", ")}`)
    return verdict(problems, "fizzbuzz.py right, no attn call")
  },
}

const fail: Task = {
  id: "fail",
  title: "Recover from a slug two seeds share",
  setup: (w) => {
    w.state.safari = plant(w, "Flaky login test", "The login test times out on the Safari runner.")
    w.state.chrome = plant(w, "Flaky login test", "The login test times out on the Chrome runner.")
    return "Add a note to seed flaky-login-test saying the login timeout was raised to 30 s on the Safari runner."
  },
  check: (w) => {
    const problems: string[] = []
    if (!notesBy(w, w.state.safari as string, w.session).some((n) => n.includes("30"))) problems.push("no note about the 30 s timeout on the Safari seed")
    if (notesBy(w, w.state.chrome as string, w.session).length > 0) problems.push("noted the Chrome seed")
    return verdict(problems, "noted the Safari seed only")
  },
}

export const TASKS: Task[] = [progress, plotNotes, ask, msgDelegate, standby, coding, fail]
