import { cpSync } from "node:fs"
import { join, resolve } from "node:path"
import type { Check, Task, World } from "./world.ts"

const fixtures = join(import.meta.dir, "fixtures", "asks")
const stageFixture = (name: string) => (dir: string) => {
  cpSync(join(fixtures, name), dir, { recursive: true })
  return dir
}

type Choice = { label: string; description?: string }
type Question = { kind: string; prompt: string; context?: string; choices: Choice[]; recommended?: string }
type Ask = { base_dir: string; parts: { markdown?: string; question?: Question }[] }

type Fence = { lang: string; body: string }
type Table = { rows: number; columns: number }
type Image = { target: string; html: boolean }
/** What one ask shows beside its prose: the code fences, the markdown tables, and the image targets. */
type Shown = { fences: Fence[]; tables: Table[]; images: Image[] }

/** A fence opener, also inside a quote or a list item: the container prefix, then the fence and its language. */
const FENCE = /^(\s*(?:>\s*)*(?:(?:[-*+]|\d+[.)])\s+)?)(`{3,}|~{3,})\s*([^\s`]*)/
const TABLE_DELIMITER = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)+\|?\s*$/
const MARKDOWN_IMAGE = /!\[[^\]]*\]\(\s*<?([^\s)>]+)>?(?:\s+["'][^"']*["'])?\s*\)/g
const HTML_IMAGE = /<img\b[^>]*\bsrc\s*=\s*["']?([^"'\s>]+)/gi
const cells = (row: string) => row.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").length

function shown(markdown: string): Shown {
  const out: Shown = { fences: [], tables: [], images: [] }
  const lines = markdown.split("\n")
  let fence: { marker: string; quoted: boolean; indent: number; lang: string; body: string[] } | undefined
  const close = () => {
    out.fences.push({ lang: fence!.lang, body: fence!.body.join("\n") })
    fence = undefined
  }
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!
    const opener = line.match(FENCE)
    if (fence) {
      if (opener && opener[2]!.startsWith(fence.marker) && opener[3] === "") close()
      else {
        const unquoted = fence.quoted ? line.replace(/^\s*(?:>\s?)+/, "") : line
        fence.body.push(unquoted.replace(new RegExp(`^ {0,${fence.indent}}`), ""))
      }
      continue
    }
    if (opener) {
      const prefix = opener[1]!
      fence = { marker: opener[2]!, quoted: prefix.includes(">"), indent: prefix.replace(/^\s*(?:>\s?)*/, "").length, lang: opener[3]!.toLowerCase(), body: [] }
      continue
    }
    for (const m of line.matchAll(MARKDOWN_IMAGE)) out.images.push({ target: m[1]!, html: false })
    for (const m of line.matchAll(HTML_IMAGE)) out.images.push({ target: m[1]!, html: true })
    if (line.includes("|") && TABLE_DELIMITER.test(lines[i + 1] ?? "")) {
      let rows = 0
      while (i + 2 + rows < lines.length && lines[i + 2 + rows]!.includes("|")) {
        const row = lines[i + 2 + rows]!
        for (const m of row.matchAll(MARKDOWN_IMAGE)) out.images.push({ target: m[1]!, html: false })
        for (const m of row.matchAll(HTML_IMAGE)) out.images.push({ target: m[1]!, html: true })
        rows++
      }
      out.tables.push({ rows, columns: cells(line) })
      i += 1 + rows
    }
  }
  if (fence) close()
  return out
}

/** The image types attn copies when an ask opens (`internal/ask/images.go`); it refuses an ask whose copy fails. */
const COPIED = /\.(png|jpe?g|gif|webp|bmp|tiff?)([?#]|$)/i
const isRemote = (target: string) => (/^[a-z][a-z0-9+.-]*:/i.test(target) && !/^file:/i.test(target)) || target.startsWith("//")
const localPath = (ask: Ask, target: string) => resolve(ask.base_dir, decodeURI(target.replace(/^file:\/\//i, "")))

/** Every open ask the session opened, each with what it shows: its markdown parts and its questions' context lines,
 * where the app renders markdown. Choice labels and notes render as plain text, so they show nothing. */
export function openAsks(w: World) {
  const asks: Ask[] = w.attnJson(["ask", "list"], { session: w.session }).asks ?? []
  return asks.map((ask) => {
    const texts = ask.parts.flatMap((p) => [p.markdown, p.question?.context].filter((t): t is string => t !== undefined))
    const each = texts.map(shown)
    return {
      ask,
      questions: ask.parts.flatMap((p) => (p.question ? [p.question] : [])),
      fences: each.flatMap((s) => s.fences),
      tables: each.flatMap((s) => s.tables),
      images: each.flatMap((s) => s.images),
    }
  })
}
type Opened = ReturnType<typeof openAsks>[number]

/** The visuals the asks show, for the run's verdict: each Mermaid fence with whether the app draws it whole, tables by
 * their rows, other fences by language, and images by target. */
function visuals(w: World, asks: Opened[]) {
  const kinds: string[] = []
  for (const a of asks) {
    for (const f of a.fences) {
      if (f.lang === "mermaid") {
        const drawn = w.mermaid(f.body)
        kinds.push(drawn.drawn ? (drawn.skipped.length === 0 ? "mermaid" : `mermaid skipping ${drawn.skipped.length}`) : "mermaid that does not draw")
      } else kinds.push(`fence ${f.lang || "plain"}`)
    }
    for (const t of a.tables) kinds.push(`table of ${t.rows} rows by ${t.columns} columns`)
    for (const i of a.images) kinds.push(`${i.html ? "html img" : "image"} ${i.target}`)
  }
  return kinds
}

const drawsWhole = (w: World, asks: Opened[]) =>
  asks.some((a) => a.fences.some((f) => f.lang === "mermaid" && ((d) => d.drawn && d.skipped.length === 0)(w.mermaid(f.body))))

const choiceText = (c: Choice) => `${c.label} ${c.description ?? ""}`
/** Whether each pattern matches a different choice. */
const matchesDistinct = (choices: Choice[], patterns: RegExp[], taken: Set<number> = new Set()): boolean => {
  const [first, ...rest] = patterns
  if (!first) return true
  return choices.some((c, i) => !taken.has(i) && first.test(choiceText(c)) && matchesDistinct(choices, rest, new Set([...taken, i])))
}
/** A one-choice question that offers one choice for each pattern, each a different choice. */
export const offers = (asks: Opened[], ...patterns: RegExp[]) =>
  asks.flatMap((a) => a.questions).find((q) => q.kind === "one" && matchesDistinct(q.choices, patterns))
export const recommends = (q: Question | undefined, pattern: RegExp) => {
  const choice = q?.choices.find((c) => c.label === q.recommended)
  return choice !== undefined && pattern.test(choiceText(choice))
}

/** Image targets that cannot show: html `<img>`, remote or `data:` targets, and local files of a type attn does not copy, such as SVG. */
function brokenImages(asks: Opened[]) {
  const broken: string[] = []
  for (const a of asks)
    for (const i of a.images) {
      if (i.html) broken.push(`html img ${i.target}`)
      else if (isRemote(i.target)) broken.push(`remote ${i.target.slice(0, 40)}`)
      else if (!COPIED.test(i.target)) broken.push(`not copied ${i.target}`)
    }
  return broken
}

/** Judges a run from its open asks: no ask fails it outright, and the verdict always says how many asks opened and
 * what they show, so a baseline and a treatment compare cell by cell. */
const judge = (w: World, problemsOf: (asks: Opened[]) => string[]): Check => {
  const asks = openAsks(w)
  if (asks.length === 0) return { pass: false, why: "opened no ask" }
  const problems = problemsOf(asks)
  const seen = visuals(w, asks)
  const summary = `${asks.length} ask${asks.length === 1 ? "" : "s"}, visuals: ${seen.join(", ") || "none"}`
  return problems.length === 0 ? { pass: true, why: summary } : { pass: false, why: `${summary}; ${problems.join("; ")}` }
}

const askFlow: Task = {
  id: "ask-flow",
  title: "Put a choice between two service flows to the user",
  mermaid: true,
  stage: stageFixture("flow"),
  setup: () => "We're blocked on picking between the two retry designs in design-notes.md. Get the user's decision with your recommendation.",
  check: (w) =>
    judge(w, (asks) => {
      const problems: string[] = []
      if (!drawsWhole(w, asks)) problems.push("no Mermaid fence the app draws whole")
      const q = offers(asks, /lock|inline|design a\b|option a\b|^a\b/i, /queue|worker|idempoten|design b\b|option b\b|^b\b/i)
      if (!q) problems.push("no question offers both designs")
      else if (!q.recommended) problems.push("no recommendation")
      problems.push(...brokenImages(asks))
      return problems
    }),
}

const shots = ["shots/option-a-sidebar.png", "shots/option-b-topbar.png"]
const askLayout: Task = {
  id: "ask-layout",
  title: "Put a choice between two captured layouts to the user",
  mermaid: true,
  stage: stageFixture("layout"),
  setup: () => "The captures in shots/ show our two candidate navigation layouts. Get the user's pick before we build one.",
  check: (w) =>
    judge(w, (asks) => {
      const problems: string[] = []
      const shownPaths = new Set(asks.flatMap((a) => a.images.filter((i) => !i.html && !isRemote(i.target)).map((i) => localPath(a.ask, i.target))))
      for (const shot of shots) if (!shownPaths.has(join(w.dir, shot))) problems.push(`does not show ${shot}`)
      if (!offers(asks, /side ?bar|side nav|left|vertical|layout a\b|option a\b|^a\b/i, /top ?bar|top nav|header|horizontal|layout b\b|option b\b|^b\b/i)) problems.push("no question offers both layouts")
      problems.push(...brokenImages(asks))
      return problems
    }),
}

const askTradeoff: Task = {
  id: "ask-tradeoff",
  title: "Put a choice among three strategies with several criteria to the user",
  mermaid: true,
  stage: stageFixture("tradeoff"),
  setup: () => "Read options.md and get the user's call on which strategy to ship.",
  check: (w) =>
    judge(w, (asks) => {
      const problems: string[] = []
      const compares = (t: Table) => t.rows >= 3 || (t.rows >= 2 && t.columns >= 4)
      if (!asks.some((a) => a.tables.some(compares) || a.fences.some((f) => f.lang === "diff")))
        problems.push("no table that compares the three strategies, and no diff")
      if (!offers(asks, /redis|write-through/i, /memory|lru|per-node|in-process/i, /cdn|edge/i)) problems.push("no question offers all three strategies")
      problems.push(...brokenImages(asks))
      return problems
    }),
}

const askSimpleControl: Task = {
  id: "ask-simple-control",
  title: "Put a simple two-way choice to the user without a picture",
  mermaid: true,
  setup: () => "Get the user's pick on default audit-log retention: 30 days or 90 days. Recommend 30 days.",
  check: (w) =>
    judge(w, (asks) => {
      const problems: string[] = []
      const q = offers(asks, /\b30\b/, /\b90\b/)
      if (!q) problems.push("no question offers 30 and 90 days")
      else if (!recommends(q, /\b30\b/)) problems.push("does not recommend 30 days")
      if (asks.some((a) => a.fences.some((f) => f.lang === "mermaid"))) problems.push("drew a Mermaid diagram for a simple choice")
      if (asks.some((a) => a.images.length > 0)) problems.push("showed an image for a simple choice")
      return problems
    }),
}

const askSvgGuard: Task = {
  id: "ask-svg-guard",
  title: "Show an SVG architecture to the user in a form that renders",
  mermaid: true,
  stage: stageFixture("svg"),
  setup: () => "Use arch.svg to get the user's decision on whether to split the worker service.",
  check: (w) =>
    judge(w, (asks) => {
      const problems = brokenImages(asks)
      const raster = asks.some((a) => a.images.some((i) => !i.html && !isRemote(i.target) && COPIED.test(i.target)))
      if (!drawsWhole(w, asks) && !raster) problems.push("shows the architecture neither as a Mermaid fence the app draws whole nor as a raster image")
      if (!asks.some((a) => a.questions.some((q) => q.choices.length >= 2))) problems.push("no question with a choice")
      return problems
    }),
}

/** Decisions an agent should put to the user, none of whose prompts names attn, asks or a kind of picture: they measure
 * whether the agent reaches for an ask on its own, and whether it shows the decision with the visual that fits. */
export const ASK_TASKS: Task[] = [askFlow, askLayout, askTradeoff, askSimpleControl, askSvgGuard]
