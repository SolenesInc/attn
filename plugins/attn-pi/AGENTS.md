# attn-pi plugin guide

**Read pi's own docs before changing this plugin.** The set for the pinned
version is installed at `node_modules/@earendil-works/pi-coding-agent/docs/`
(`bun install` here if it is missing).

## Approvals, sandbox and auto mode

`approval/` is where a bash command is decided: the orchestrator, the two
reviewers (your card and the Guardian), the config loader and the rules
compiler. `sandbox/` builds the Seatbelt or bubblewrap wrapper and owns the bash
tool's parameter schema; `netproxy/` is the driver-hosted proxy that decides
hosts in flight. `automode/` is what survived the old model and is shared: the
denial ledger, the environment slots and the generated prompt catalog.

Auto mode is which reviewer answers, nothing else. `/auto on` is the Guardian,
`/auto off` is the user's card. To understand or modify any of it, read
[docs/automode.md](docs/automode.md) and [docs/security.md](docs/security.md).

Rules, hosts, the approval policy and the sandbox mode are daemon-owned. Writes
go through `PromoteAutoModeProposal` or, for a "don't ask again" answered inside
a session, the pi relay's `PromoteReportedAmendment`. The CLI proposes; the app
promotes.

## Reporting pull requests

The suite watches pi's `tool_result` and reports every `gh pr create` URL to
attn, so a pi session records the pull requests it opens with no hook and no
prompt instruction. `src/pullrequest.ts` is the twin of
`internal/hooks/pullrequest.go` and the two run the same corpus
(`internal/hooks/testdata/pull-request-extraction.json`): change one regex and
you change both.

The driver declares the `pull_request_reporting` capability, which is what stops
attn adding the `attn pr record` block to a pi session's launch guidance.

## Shell parsing and execpolicy

`shell/` and `execpolicy/` are a TypeScript port of codex-rs: `shell-command`'s
bash parser and dangerous-command check, and the prefix-rule engine that turns a
command plus the user's rules into allow, prompt or forbidden. The Codex tests
are ported alongside them (`test/shell-*.test.ts`, `test/execpolicy-*.test.ts`)
with the same inputs and outputs, so a behavior change shows up as a corpus
diff. Keep the ports faithful, and cite `file:line` where a behavior is
non-obvious.

Parsing runs on `web-tree-sitter` against the vendored
`shell/tree-sitter-bash.wasm` (grammar 0.25.1, the pin Codex builds against).
Call `initShellParsing()` once at startup; every parse entry point is
synchronous after that. That grammar and web-tree-sitter's own runtime wasm both
resolve next to the module, which is why `scripts/build-bundled-plugins.sh`
copies them beside `suite.js`. `receipts/shell-parse-cost.ts` is the receipt:
about 25ms to load the wasm once, then 0.02-0.10ms per parse, and no memory
growth once the allocator has reached its high-water mark. A load past 250ms, a
parse past 1ms, or any steady-state growth means something regressed.

Every tree-sitter tree must be deleted after its walk: web-tree-sitter 0.25.10
registers no finalizer, so a tree nobody frees leaks about 2KB of wasm heap that
no later GC reclaims.
