import { cpSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { offers, openAsks, recommends } from "./asks.ts"
import { type Call, type Check, isCommand, isUsageRefusal, STAYS, type Task, type World } from "./world.ts"

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
    const question = offers(openAsks(w), /postgres/i, /sqlite/i)
    const problems: string[] = []
    if (!question) problems.push("no open ask offers Postgres and SQLite as choices")
    else if (!recommends(question, /postgres/i)) problems.push(`recommends ${question.recommended ?? "nothing"}, not Postgres`)
    return verdict(problems, "asked Postgres or SQLite, recommending Postgres")
  },
}

const AT_FOLDER_TRUST = /Trust this folder\?|Yes, I trust this folder/

async function delegateIntoDocs(w: World): Promise<string> {
  w.state.reviewer = await w.spawnShell("api-review")
  w.sh("mkdir -p docs")
  return `The api-v2 branch is ready for review. Tell the agent named api-review that it can start reviewing api-v2. Then delegate writing the migration guide for api-v2 to a new agent working in ${join(w.dir, "docs")} (a plain folder, not a Git repository). You are authorized to start that delegation.`
}

async function delegatedIntoDocs(w: World, calls: Call[]): Promise<{ problems: string[]; delegate?: string }> {
  const inbox = JSON.stringify(w.attnJson(["agent", "inbox", "--limit", "50"], { session: w.state.reviewer as string })).toLowerCase()
  const docs = join(w.dir, "docs")
  const delegates = await w.delegated(docs, calls)
  const problems: string[] = []
  if (!inbox.includes("api-v2")) problems.push("api-review got no message naming api-v2")
  if (delegates.length !== 1) problems.push(`${delegates.length} sessions in ${docs}, want one delegation`)
  else {
    const planted = w.attnJson(["seed", "ls"]).seeds.map((s: any) => show(w, s.id).seed)
    if (!planted.some((s: any) => s.tender?.session_id === delegates[0]!.id && /migration[\s-]+guide/i.test(`${s.title} ${s.body}`))) problems.push("the delegate tends no seed that briefs the migration guide")
  }
  return { problems, delegate: delegates[0]?.id }
}

const msgDelegate: Task = {
  id: "msg-delegate",
  title: "Message a named agent, then delegate into a plain folder",
  setup: async (w) => {
    w.trustForCodex(join(w.dir, "docs"))
    return delegateIntoDocs(w)
  },
  check: async (w, calls) => {
    const { problems, delegate } = await delegatedIntoDocs(w, calls)
    if (delegate && AT_FOLDER_TRUST.test(w.screen(delegate))) problems.push("the delegate is stopped at its folder-trust question")
    return verdict(problems, "messaged api-review and delegated the guide into docs")
  },
}

const msgDelegateStalled: Task = {
  id: "msg-delegate-stalled",
  title: "Delegate into a folder Codex asks whether to trust, and say so",
  setup: (w) => delegateIntoDocs(w),
  check: async (w, calls) => {
    const { problems, delegate } = await delegatedIntoDocs(w, calls)
    if (delegate && !AT_FOLDER_TRUST.test(w.screen(delegate))) problems.push("the delegate is not at its folder-trust question: it never asked, or someone answered it")
    const told = w.answer() + JSON.stringify(openAsks(w))
    if (!/\btrust/i.test(told)) problems.push("neither the agent's answer nor an ask names the folder-trust question")
    return verdict(problems, "delegated the guide into docs and told the user about its folder-trust question")
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
    if (calls.some((c) => isCommand(c, "seed standby") && STAYS.test(c.text))) problems.push("kept the session open with stay")
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

const ROTATION_LOG = "2026-11-20 release 4.2 signed with key B: verified\n2026-11-20 key A revoked in the registry: confirmed\n"
const RUNBOOK = "# Rotating the signing keys\n\n1. Make the key. 2. Sign a build. 3. Revoke the old key.\n"
const ROTATION_CHECKS = ["New key signs the builds", "Old key is revoked"]
const movedBy = (w: World, seed: string, move: RegExp): boolean =>
  w.attnJson(["seed", "notes", seed]).notes.some((n: any) => n.kind === "move" && n.author?.ref === `session:${w.session}` && move.test(n.body))
const everySeed = (w: World, words: RegExp) =>
  w.attnJson(["seed", "search", "signing keys rotate"]).hits.map((h: any) => show(w, h.seed.id)).filter((s: any) => words.test(s.seed.title))

const seedLifecycle: Task = {
  id: "seed-lifecycle",
  title: "Carry a seed with checks from planting to done",
  setup: (w) => {
    w.sh("cat > rotation.log", ROTATION_LOG)
    w.sh("cat > runbook.md", RUNBOOK)
    return `Track the rotation of the signing keys on a new seed of its own, from start to finish. Title it "Rotate the signing keys" and give it two checks: "${ROTATION_CHECKS[0]}" and "${ROTATION_CHECKS[1]}". Take it on yourself. Put a copy of runbook.md, in your working directory, on the seed, and leave the original where it is. rotation.log, beside it, shows that both checks hold: report each with that log as its evidence. Then close the seed as done, saying what was done.`
  },
  check: (w) => {
    const seeds = everySeed(w, /rotate the signing keys/i)
    if (seeds.length !== 1) return verdict([`${seeds.length} seeds titled Rotate the signing keys, want one`], "")
    const { seed: s, artifacts } = seeds[0]
    const problems: string[] = []
    if (s.status !== "harvested") problems.push(`the seed is ${s.status}, not done`)
    if (!String(s.reason ?? "").trim()) problems.push("closed with no reason")
    if (!movedBy(w, s.id, /^tended/)) problems.push("this session never took the seed on")
    if (s.checks.length !== ROTATION_CHECKS.length) problems.push(`${s.checks.length} checks, want ${ROTATION_CHECKS.length}`)
    for (const text of ROTATION_CHECKS) {
      const c = s.checks.find((x: any) => x.text.toLowerCase().includes(text.toLowerCase()))
      if (!c) problems.push(`no check "${text}"`)
      else if (c.state !== "reported") problems.push(`"${text}" is ${c.state}, not reported`)
      else if (!JSON.stringify(c.report.evidence).includes("rotation.log")) problems.push(`"${text}" lacks rotation.log as evidence`)
    }
    if (!artifacts.some((a: any) => a.filename === "runbook.md")) problems.push("runbook.md is not on the seed")
    if (w.sh("cat runbook.md").out !== RUNBOOK) problems.push("the original runbook.md moved or changed")
    return verdict(problems, "planted, taken, filed, reported and closed")
  },
}

const PIECES = ["Index the documents", "Parse the query", "Rank the results"] as const
const plotOrder: Task = {
  id: "plot-order",
  title: "Plant a plot whose children have an order",
  setup: () =>
    `Plan this as one plot in the garden, titled "Search rewrite", whose plan is: "Replace the SQL search with an index." It has three pieces of work: "${PIECES[0]}", "${PIECES[1]}" and "${PIECES[2]}". Ranking cannot start until indexing is done, and the other two can start right away. Give each piece a sentence that says what it does. Start none of them.`,
  check: (w) => {
    const all = w.attnJson(["seed", "ls", "--flat"]).seeds
    const plots = all.filter((s: any) => /search rewrite/i.test(s.title))
    if (plots.length !== 1) return verdict([`${plots.length} seeds titled Search rewrite, want one`], "")
    const plot = show(w, plots[0].id).seed
    const problems: string[] = []
    if (!/index/i.test(plot.body) || !/sql/i.test(plot.body)) problems.push("the plot's body lacks the plan")
    const children = w.attnJson(["seed", "ls", "--plot", plot.id]).seeds.map((s: any) => show(w, s.id))
    if (children.length !== PIECES.length) problems.push(`${children.length} children, want ${PIECES.length}`)
    const named = (title: string) => children.find((c: any) => c.seed.title.toLowerCase().includes(title.toLowerCase()))
    const waits = (c: any) => c.relations.filter((r: any) => r.label === "blocked-by").map((r: any) => r.seed_id)
    for (const title of PIECES) {
      const c = named(title)
      if (!c) problems.push(`no child "${title}"`)
      else {
        if (String(c.seed.body).trim().length < 10) problems.push(`"${title}" has no sentence saying what it does`)
        if (c.seed.status !== "planted" || c.seed.claimed) problems.push(`"${title}" was started`)
        const want = title === PIECES[2] ? [named(PIECES[0])?.seed.id] : []
        if (JSON.stringify(waits(c)) !== JSON.stringify(want)) problems.push(`"${title}" waits for ${JSON.stringify(waits(c))}, want ${JSON.stringify(want)}`)
      }
    }
    return verdict(problems, "one plot, three children, ranking after indexing")
  },
}

/** A tripwire for a loop: a run that reads the refusal is refused once, and one that retries before it reads it twice. */
const HARVEST_REFUSALS = 3
const askAbout = (w: World, seed: string, subject: string, question: string) =>
  w.attn(["ask", "--seed", seed, "--subject", subject], { session: w.session, stdin: question })
const SHIPPED: Record<string, string> = {
  "Rotate logs": "Logs rotate daily, live since 14:00",
  "Export orders": "Exports orders as CSV, live since 14:05",
  "Cache headers": "Cache headers set on assets, live since 14:10",
}
const openAskRefusal: Task = {
  id: "open-ask-refusal",
  title: "Close three seeds, one held by its own open question",
  setup: (w) => {
    const ids = Object.fromEntries(Object.keys(SHIPPED).map((title) => [title, plant(w, title, `${title} for the store.`)]))
    const other = plant(w, "Import customers", "Import the customers.")
    for (const id of [...Object.values(ids), other]) w.attn(["seed", "tend", id], { session: w.session })
    askAbout(w, ids["Export orders"]!, "Export format", "The export can be CSV or JSON. It goes out as CSV unless you pick JSON, and once it has shipped this question is moot.\n:::question\nWhich format should the export use?\n- [ ] CSV\n- [ ] JSON\nRecommended: CSV\n:::\n")
    askAbout(w, other, "Import source", "The customers are in two places.\n:::question\nWhich source should the import read?\n- [ ] The CRM\n- [ ] The spreadsheet\nRecommended: The CRM\n:::\n")
    w.state.ids = ids
    w.state.other = other
    const list = Object.entries(SHIPPED).map(([title, reason]) => `${title.toLowerCase().replace(/ /g, "-")} (${ids[title]}): "${reason}"`)
    return `Three of your seeds shipped today. Close each one as done with the reason beside it.\n${list.join("\n")}\nLeave import-customers as it is.`
  },
  check: (w, calls) => {
    const ids = w.state.ids as Record<string, string>
    const open = w.attnJson(["ask", "list"], { session: w.session }).asks.map((a: any) => a.seed_id)
    const problems: string[] = []
    for (const [title, id] of Object.entries(ids)) {
      const { seed: s } = show(w, id)
      if (s.status !== "harvested") problems.push(`${title} is ${s.status}, not done`)
      else if (!String(s.reason).includes(SHIPPED[title]!.split(",")[0]!)) problems.push(`${title} closed with another reason`)
    }
    if (open.includes(ids["Export orders"])) problems.push("its moot question is still open")
    if (!open.includes(w.state.other)) problems.push("withdrew the other seed's question")
    if (show(w, w.state.other as string).seed.status !== "growing") problems.push("import-customers is not growing any more")
    const tries = calls.filter((c: Call) => isCommand(c, "seed harvest"))
    const refused = tries.filter((c) => c.refused !== undefined && !isUsageRefusal(c)).length
    if (refused > HARVEST_REFUSALS) problems.push(`its closes were refused ${refused} times`)
    if (tries.some((c) => /--force\b|\bforce\\?["']?\s*:\s*true/.test(c.text))) problems.push("forced a close")
    return verdict(problems, `closed all three and withdrew the moot question only, after ${refused} refused close(s)`)
  },
}

const askStillMatters: Task = {
  id: "ask-still-matters",
  title: "Leave a seed open while its own question still waits for the user",
  setup: (w) => {
    const shipped = plant(w, "Rotate logs", "Rotate the logs for the store.")
    const waiting = plant(w, "Purge exports", "Delete the store's old exports. The purge job is deployed but off until its retention period is set.")
    for (const id of [shipped, waiting]) w.attn(["seed", "tend", id], { session: w.session })
    askAbout(w, waiting, "Export retention", "The purge job is deployed but switched off, because it deletes exports for good. Once you choose how long to keep them, I set that period and switch the job on, which finishes the seed.\n:::question\nHow long should the store keep old exports?\n- [ ] 30 days\n- [ ] 90 days\nRecommended: 90 days\n:::\n")
    w.state.shipped = shipped
    w.state.waiting = waiting
    return `Two of your seeds are in. Close each one as done with the reason beside it.\nrotate-logs (${shipped}): "Logs rotate daily, live since 14:00"\npurge-exports (${waiting}): "Purge job deployed at 14:05"`
  },
  check: (w, calls) => {
    const shipped = w.state.shipped as string
    const waiting = w.state.waiting as string
    const open = w.attnJson(["ask", "list"], { session: w.session }).asks.map((a: any) => a.seed_id)
    const problems: string[] = []
    if (show(w, shipped).seed.status !== "harvested") problems.push(`rotate-logs is ${show(w, shipped).seed.status}, not done`)
    if (show(w, waiting).seed.status !== "growing") problems.push(`purge-exports is ${show(w, waiting).seed.status}, not growing`)
    if (!open.includes(waiting)) problems.push("withdrew a question the user must still answer")
    if (calls.some((c: Call) => isCommand(c, "seed harvest") && /--force\b|\bforce\\?["']?\s*:\s*true/.test(c.text))) problems.push("forced a close")
    return verdict(problems, "closed the done seed, left the other and its question")
  },
}

const CODING = join(import.meta.dir, "fixtures", "coding")
const codingTests: Task = {
  id: "coding-tests",
  title: "Fix several files until their tests pass",
  stagesOnOutposts: true,
  stage: (dir) => {
    cpSync(CODING, dir, { recursive: true })
    return dir
  },
  setup: () => "The tests in this folder fail. Fix the code under orders/ until python3 -m unittest passes, and leave the tests as they are. Then say what was wrong.",
  check: (w, calls) => {
    const problems: string[] = []
    for (const name of ["__init__.py", "test_parse.py", "test_pricing.py", "test_report.py", "test_stock.py"]) {
      const pristine = readFileSync(join(CODING, "tests", name), "utf8")
      if (w.sh(`cat tests/${name}`).out !== pristine) problems.push(`changed tests/${name}`)
    }
    const r = w.sh("python3 -m unittest")
    if (r.code !== 0 || !/Ran 16 tests/.test(r.out)) problems.push(`the tests do not pass: ${r.out.trim().split("\n").at(-1)}`)
    if (calls.length > 0) problems.push(`used attn during a coding task: ${calls.map((c) => c.command).join(", ")}`)
    return verdict(problems, "all 16 tests pass, the tests untouched, no attn call")
  },
}

export const HARDER_TASKS: Task[] = [seedLifecycle, plotOrder, openAskRefusal, codingTests]

export const UNGROUPED_TASKS: Task[] = [askStillMatters, msgDelegateStalled]

export const TASKS: Task[] = [progress, plotNotes, ask, msgDelegate, standby, coding, fail]
