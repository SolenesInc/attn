// A mocked attn for a limited member: help and the delegation reference are frozen copies (fixture-*.txt), `delegate`
// resolves `--repo` against a small world and enforces read grants with a refusal that names the grant (D8, D9),
// and every other command gets a canned reply. Nothing reaches the real attn. Every call is recorded.
import { readFileSync } from "node:fs"
import { join } from "node:path"

export type Repo = { private: boolean; granted: boolean }
export type World = { member: string; repos: Record<string, Repo> }
export type AttnCall = { args: string[]; stdin?: string; briefFile?: string; reply: string; ok: boolean }
/** A successful delegation, as a check reads it. */
export type Delegation = { repo: string; brief: string }

const fixture = (name: string) => readFileSync(join(import.meta.dir, `fixture-${name}`), "utf8")

export function makeAttn(world: World, readComputerFile: (path: string) => string) {
  const calls: AttnCall[] = []
  const delegations: Delegation[] = []
  const asks: string[] = []

  const resolve = (spec: string): { identity: string } | { error: string } => {
    let s = spec.trim().replace(/\.git$/, "").replace(/\/$/, "")
    s = s.replace(/^git@([^:]+):/, "$1/").replace(/^(https?|ssh):\/\/(?:[^@/]+@)?/, "")
    const parts = s.split("/")
    const identity = parts.length === 2 ? `github.com/${s}` : parts.length === 3 ? s : undefined
    if (identity === undefined) {
      const matches = Object.keys(world.repos).filter((id) => id.endsWith(`/${s}`))
      if (matches.length === 1) return { identity: matches[0]! }
      if (matches.length > 1) return { error: `"${spec}" names several repositories:\n${matches.map((m) => `  ${m}`).join("\n")}\nPass one in full.` }
      return { error: `no repository named "${spec}" has been seen; pass owner/name or host/owner/name` }
    }
    if (world.repos[identity.toLowerCase()] === undefined) return { error: `${identity}: repository not found` }
    return { identity: identity.toLowerCase() }
  }

  const flag = (args: string[], name: string) => {
    const i = args.indexOf(name)
    return i >= 0 ? args[i + 1] : undefined
  }

  const delegate = (args: string[], stdin: string | undefined): { reply: string; ok: boolean; briefFile?: string } => {
    const repos = args.flatMap((a, i) => (a === "--repo" ? [args[i + 1] ?? ""] : []))
    if (repos.length > 1) return { ok: false, reply: "--repo takes one repository: a session keeps one working repository" }
    const host = flag(args, "--host")
    if (host !== undefined && host !== "sandbox")
      return { ok: false, reply: `not granted: use_host ${host} for member ${world.member}. Ask the person for it with attn ask; name the grant in the question.` }
    const briefFile = flag(args, "--brief-file")
    const brief = flag(args, "--brief") ?? (briefFile === "-" ? stdin : briefFile !== undefined ? readComputerFile(briefFile) : undefined) ?? (flag(args, "--seed") ? "(seed)" : undefined) ?? stdin
    if (brief === undefined || brief.trim() === "") return { ok: false, reply: "attn delegate needs --brief TEXT, --brief-file PATH or --seed ID", briefFile }
    if (repos.length === 0) {
      if (flag(args, "--cwd") !== undefined)
        return { ok: false, reply: `--cwd ${flag(args, "--cwd")}: no such folder on the sandbox attn starts for member ${world.member}; name a repository with --repo`, briefFile }
      return { ok: false, reply: "attn delegate needs --repo REPOSITORY or --cwd PATH", briefFile }
    }
    const resolved = resolve(repos[0]!)
    if ("error" in resolved) return { ok: false, reply: resolved.error, briefFile }
    const repo = world.repos[resolved.identity]!
    if (repo.private && !repo.granted)
      return { ok: false, reply: `not granted: read_repo ${resolved.identity} for member ${world.member}. Ask the person for it with attn ask; name the grant in the question.`, briefFile }
    const name = resolved.identity.split("/").at(-1)
    const branch = flag(args, "--branch") ?? flag(args, "--existing-branch") ?? "main"
    delegations.push({ repo: resolved.identity, brief })
    const n = delegations.length
    return {
      ok: true,
      briefFile,
      reply: `delegated: session 7c1e0f5${n} started as ${flag(args, "--role") ?? "builder"} on seed s-q4m8x${n}.\nrepository ${resolved.identity}: cloned to ~/projects/${name} on a new sandbox; worktree on branch ${branch}.\nYou will get an inbox notification when it reports.`,
    }
  }

  const canned = (args: string[]): string => {
    const [a, b] = args
    if (a === "delegate" && b === "roles") return "roles: orchestrator (claude opus), builder (claude opus), reviewer (codex), pathfinder (claude opus)"
    if (a === "seed" && b === "plant") return "s-q4m8xt  planted"
    if (a === "seed" && b === "search") return "0 seeds carry every word"
    if (a === "seed" && b === "ready") return "nothing ready"
    if (a === "seed") return "ok"
    if (a === "agent" && b === "inbox") return "no unread items"
    if (a === "agent" && b === "list") return "no other agents"
    return "ok"
  }

  const run = (args: string[], stdin?: string): string => {
    let reply: string
    let ok = true
    let briefFile: string | undefined
    const [a, b] = args
    if (args.length === 0 || (args.length === 1 && (a === "--help" || a === "help"))) reply = fixture("help.txt")
    else if (a === "delegate" && args.includes("--help")) reply = fixture("delegate-help.txt")
    else if (a === "ask" && args.includes("--help")) reply = fixture("ask-help.txt")
    else if (a === "skill" && b === "--reference") reply = args[2] === "delegation" ? fixture("delegation-ref.txt") : `no reference named ${args[2] ?? ""} in this bench`
    else if (args.includes("--help")) reply = `usage: attn ${a} ... (help for this command is not part of the bench)`
    else if (a === "delegate" && b !== "roles") ({ reply, ok, briefFile } = delegate(args, stdin))
    else if (a === "ask" && b !== "list" && b !== "close") {
      asks.push([...args, stdin ?? ""].join(" "))
      reply = "a-9k2p7d asked. The person's answer arrives in this conversation."
    } else reply = canned(args)
    calls.push({ args, ...(stdin === undefined ? {} : { stdin }), ...(briefFile === undefined ? {} : { briefFile }), reply, ok })
    return reply
  }

  return { run, calls, delegations, asks }
}

export const ATTN_DESCRIPTION =
  "Run the attn CLI with these arguments and return its output. attn runs you beside the user's other agents: it tracks work as seeds, starts other agents on work (attn delegate), messages agents and asks the user. `attn --help` lists every command, `attn <command> --help` shows a command's syntax, and `attn skill --reference <name>` prints the rules for a capability. Standard input can be passed as `stdin`."
