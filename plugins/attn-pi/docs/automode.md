# pi auto mode

Auto mode decides who answers when a pi session asks to run a command: you, or
the Guardian. Everything else — the prefix rules, the sandbox, the network
proxy — applies either way. `/auto on` puts the Guardian in the chair, `/auto
off` puts you back in it, and `/auto status` says which it is.

The model is a port of Codex's sandbox and approval model. Where attn differs,
this document says so.

## What one bash call walks through

```
bash(command, sandbox_permissions=use_default|require_escalated, justification?, prefix_rule?)
  │
  ├─ parse: tree-sitter-bash. `bash -lc`/`-c` scripts made only of plain word commands joined by
  │         && || ; | become one segment per command; anything else is ONE segment (the whole argv).
  │         An executable that is not a known shell is prepended as an extra segment.
  ├─ execpolicy: each segment against prefix rules → allow | prompt | forbidden
  │     unmatched: dangerous (rm with a force flag, also via sudo/env/trap/sh -c, wrapper depth > 8
  │                is itself dangerous) → prompt (never → forbidden)
  │                else never → allow; untrusted → prompt;
  │                     on-request → allow, except when sandbox_permissions = require_escalated and
  │                                  the sandbox restricts the filesystem → prompt
  │     bypass_sandbox only when every segment matched an explicit allow rule
  │
  ├─ forbidden → tool error "rejected: <justification>". Nothing runs. No reviewer.
  ├─ allow     → run. Unsandboxed only if bypass_sandbox or (require_escalated under never);
  │              else sandboxed under sandbox_mode (danger-full-access = no sandbox wrapper).
  └─ prompt    → REVIEWER (exactly one, chosen by config: your approval card, or the Guardian)
                   approved / approved_for_session / approved_execpolicy_amendment
                       → run; unsandboxed when require_escalated was asked, else sandboxed
                   denied → tool error "rejected by user", or the Guardian's rejection text
                   abort  → the turn ends
```

After a sandboxed run fails with a sandbox denial — a nonzero exit that is not
2, 126 or 127 whose output matches `operation not permitted`, `permission
denied`, `read-only file system`, `seccomp`, `sandbox`, `landlock` or `failed to
write file`, or a SIGSYS; a keyword match beats the exit code — what happens
next depends on the approval policy:

- `on-request` and `never`: the failed output is the tool result as it stands.
  The agent decides whether to re-issue with `sandbox_permissions:
  "require_escalated"` and a justification, which is a prompt decision and so
  reaches the reviewer. Nothing retries on its own.
- `untrusted`: the harness asks the reviewer "command failed; retry without
  sandbox?" and reruns unsandboxed on approval.

While a sandboxed command is connecting to a host through the proxy, an
allowlist miss holds the connection open and asks the same reviewer. See
[Network](#network).

The agent never decides an approval. Exactly one reviewer answers, never both
in sequence.

## The bash tool's parameters

| Parameter | Meaning |
| --- | --- |
| `command` | The bash command to run. |
| `timeout` | Seconds; there is no default timeout. |
| `sandbox_permissions` | `use_default` or `require_escalated`. Escalation asks to run outside the sandbox. |
| `justification` | The approval question shown for `require_escalated`. Omit it otherwise. |
| `prefix_rule` | A reusable approval prefix for this command, such as `["git", "pull"]`. Only with `require_escalated`. |

The tool stays named `bash` (Codex calls its own `exec_command`). Codex's
`with_additional_permissions` sits behind a feature that is off by default
there, so it is not here either; it is seed s-r7y88n.

## Sandbox modes

`sandbox_mode` is one of:

- `read-only` — no writable root at all.
- `workspace-write` — the session directory, a private temp directory, `/tmp`,
  the `/security` write grants and the build-cache grants are writable.
  `/tmp` is writable because Codex adds it as a default writable root.
- `danger-full-access` — no sandbox wrapper.

The sandbox mode is what a bash command runs under, and the built-in file tools
with it: `read-only` refuses every write and edit, `workspace-write` confines
them to the `/security` paths, and `danger-full-access` lets them write
anywhere except the deny lists. `/security` owns those paths, its deny lists
and the guidance the agent is given each turn. See
[security.md](security.md).

**Deviation from Codex.** Codex keeps a `require_escalated` command sandboxed
when the profile has denied-read paths, because Codex ships with none. attn
ships a deny-read list by default (`~/.ssh`, `~/.aws` and the rest), so copying
that rule would make escalation a no-op. In attn an approved `require_escalated`
runs unsandboxed, deny list included: the reviewer is approving the escape.
Codex's macOS platform-default blocks are excluded, as Codex excludes them for
profiles with full disk read access.

## Approval policies

`approval_policy` decides what an unmatched command does:

- `untrusted` — anything without an explicit allow rule goes to the reviewer.
- `on-request` — unmatched commands run sandboxed; the agent asks for
  escalation when it needs to leave the sandbox. This is the working default.
- `never` — nothing is ever asked. An unmatched command runs; a dangerous one
  is refused outright rather than prompted.

Codex's granular policy is seed s-9f13cv.

## Prefix rules

A rule is a command prefix, one token per argument, with no wildcards. `git
push` matches every command starting `git push`. A token may be a single string
or an array of alternatives. `decision` is `allow` (the default), `prompt` or
`forbidden`; a `forbidden` rule carries a `justification`, and that text is what
the agent is told when the rule refuses.

`match` and `not_match` are optional example commands the rule must and must not
match. They are checked when rules load, and a failing example is shown as a
rule error in settings rather than silently dropping the rule.

```json
{
  "pattern": ["git", "push"],
  "decision": "prompt",
  "justification": "pushes leave the machine",
  "match": [["git", "push", "origin"]],
  "not_match": [["git", "pull"]]
}
```

Rules the migration could not turn into prefix rules from the old glob lists
arrive as `legacy_patterns`. Settings lists them so you can rewrite them by
hand; nothing enforces them.

## Network

One proxy runs per profile inside the plugin driver process, speaking plain
HTTP, HTTPS CONNECT and SOCKS5 TCP. Sessions reach it through `HTTP_PROXY`,
`HTTPS_PROXY` and `ALL_PROXY`. Each bash execution gets fresh credentials,
registered through the authenticated suite relay before the command starts.
The proxy passes those credentials back to the suite to identify the exact
command, even while sibling tools run concurrently. The sandboxed command never
holds the relay's auth. The sandbox allows outbound TCP only to the proxy's
loopback port.

Commands run concurrently; approval cards and Guardian requests share one review
queue. A cancelled command dismisses its active card and cannot open a queued
one. A network denial stops only the command that made the request. Credentials
are revoked when that execution ends. Session host approvals remain available to
later commands in the same session.

Host rules are allow and deny lists with wildcards; deny wins, and the allowlist
is consulted first. A host with a port, such as `localhost:9849`, matches only
that port; a host without one matches every port. IPv6 hosts with a port use
brackets, such as `[::1]:9849`. A name that resolves to a private address is denied unless
`allow_local_binding` is on, and the resolved address is checked again at connect
time.

An allowlist miss is decided in flight: the proxy holds the connection, the
driver asks the owning session, and the session runs the reviewer with the host,
the protocol and the running command. Allowing lets the connection proceed and
the command never notices. Denying answers 403, terminates the command, and
hands the agent `Network access to "<proto>://<host>:<port>" was blocked by
policy.` "Allow in the future" persists the host rule; the driver's proxy picks
it up at once, other sessions at their next launch.

The proxy comes back on its persisted port after a driver restart, so an
inherited session's next connection can reach its reviewer. The suite restores
its active command identities in the reconnect handshake. Credential snapshots
carry a revision so a delayed report cannot restore a completed command's access.

Bare pi's network allow is unrestricted on both platforms. An attn session
instead runs through the proxy on macOS; on Linux the sandbox has no network
at all under attn, until seed s-00asxp brings the proxy into its own private
namespace. If attn asked for network policy but its proxy is not actually
running, the sandbox has no network on either platform, and the session is
told so once at start; an escalated command is the way to the network in the
meantime. The limited (MITM) mode that would inspect payloads is seed
s-xmrk8s.

## The Guardian

Choose the daemon's default in attn Settings → Auto Mode → Guardian. The model
picker reads Pi's model catalog; reasoning choices follow the selected model's
capabilities. Reset guardian default restores Follow session model and default
reasoning. Edits apply to newly launched agents, not agents already running.

With `/auto on`, the Guardian answers approvals instead of you. Its provider,
model and reasoning come from attn's launch defaults, with a session override
available in `/security` under Guardian. An unset model follows the session's
active coding model. Default reasoning uses `low` when supported, otherwise
the provider default. Explicit selections must be available and support the
chosen effort; a configuration or credential error interrupts the turn and
names what to fix, without silently substituting another model.

`/security guardian model <provider> <model>` selects a model, `model session`
follows the coding model, and `/security guardian effort <level|default>` sets
reasoning. `/security guardian reset` restores the default captured at launch.
Both `/security status` and `/auto status` show the effective selection and
whether it came from attn or a session override.

Overrides stay in agent memory across daemon restarts. An agent `/reload` or
new session resets them to the original launch defaults; relaunching the agent
gets the daemon's current defaults. Global changes do not affect running
agents. Each review retains the model and effort it started with. Switching
models preserves the Guardian conversation through Pi's history conversion.

What it sees: the security policy carrying your configured environment, the
planned action, and a transcript trimmed to Codex's caps — 5,000 tokens per
message entry, 1,000 per tool entry, 20,000 of messages and 10,000 of tool
entries in total, and the action itself capped at 8,000 bytes. It may run
read-only sandboxed commands to look at the target before answering.

It answers Codex's JSON schema: a risk level, a user-authorization level, an
allow or deny, and a rationale. A deny goes back to the model as text; you are
not asked. Three attempts with 200 ms doubling backoff and ±10% jitter, under
one 90-second deadline covering prompt building, every attempt and every tool
call. A review that could not run at all is a rejection the circuit breaker
ignores.

The circuit breaker interrupts the turn after 3 consecutive rejections or 10 in
the last 50 reviews, once per turn. Every turn starts it over.

Every completion appends an `attn-guardian-usage` entry to the session file with
the decision id, provider, model, token and cost usage, outcome and timestamp.
attn's cost view reads it and shows a Guardian row inside the session total.

Every rejection, from any reviewer, still lands in the denial ledger and in
attn's denials list. A ledger or relay that cannot take one says so in the
session rather than turning a clean rejection into a failed tool call.

## Your approval card

With `/auto off`, pi shows a card with Codex's labels verbatim:

- Yes, proceed
- Yes, and don't ask again for this command in this session
- Yes, and don't ask again for commands that start with … (only when the model
  sent a `prefix_rule`)
- No, continue without running it
- No, and tell Codex what to do differently — this one aborts the turn

**Deviation from Codex.** Codex core offers only the first, the prefix option
and the last; the other two exist in its TUI but core never lists them. attn
lists all five, because someone working through the queue should not have to
type a sentence to say no. As in Codex, only the for-session answer is cached.

The queue sees the session as waiting while the card is open.

## Where the settings live

Rules, hosts, the approval policy, the sandbox mode and the environment live in
the daemon, and reach a session at launch as JSON. That JSON is what a session
starts with, and a running session keeps it; the exception is a network host
rule, which the driver's proxy picks up at once. `/permissions` is the other
way the pair moves: it switches the approval policy and the sandbox mode inside
the pi process for the rest of that session. Nothing is reported back to attn,
so a relaunch, or a new session in the same process, returns to the launch
choice.

The pair governs both tool surfaces. Bash runs under the sandbox mode, and so do
the native write and edit tools: `read-only` refuses a change before it reaches
the filesystem worker, and `danger-full-access` lets it land outside the
workspace. Codex does the same for `apply_patch` in `assess_patch_safety`. The
`/security` toggle's own paths still bound those tools within `workspace-write`,
and its deny lists hold under every preset.

The app's Settings is where a change takes effect. `attn automode` proposes:
`rule add`, `rule remove`, `host add`, `host remove` and `policy` all record a
proposal and nothing more. An agent can propose; only a person promotes. The
environment (`automode env`) is the one direct edit, and a shipped forbidden
rule keeps a session out of the config files.

A "don't ask again" answer inside a session is a person promoting, so the
session reports it over the relay and the daemon records the proposal and
promotes it in one move, naming the session that asked. The running session
amends its own policy at once; other sessions get it at their next launch.

## The environment

The machine description is a fixed set of slots the Guardian's policy looks up
by name: whether a domain, a bucket or an org is trusted, where sensitive data
lives, what counts as production. A slot exists because a policy rule reads it;
`internal/automode/environment.go` records which rule, and a test fails when a
slot names a rule the policy does not have. An unfilled slot renders what the
rules fall back to rather than vanishing, so nothing reads as an omission.
Prose lives beside the slots as notes, and no rule reads it: nothing can be
looked up in a paragraph.

Two slots fill themselves. `trusted_repo` is the repository the session starts
in plus every remote it pushes to, read from git at launch, and
`repo_visibility` is what a GitHub lookup said about that repository. A value
you set wins over both. The visibility lookup is a network round trip, so a
launch never waits on it: it is served from what an earlier launch learned and
refreshed in the background, and a repository nobody has looked up yet launches
on the slot's unset meaning, which assumes private. `attn automode env` shows
what a session started in the current directory would detect.

## Measuring it

`receipts/guardian-verdict.ts` replays a recorded rejection through the real
Guardian against a real model and the shipped policy prompt.
`receipts/shell-parse-cost.ts` measures the tree-sitter-bash load and one
`parseBashCommands` call. Both spend real time and, for the first, real money;
run them when asked.
