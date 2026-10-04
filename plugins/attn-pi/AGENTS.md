# attn-pi plugin

Read pi's docs for the pinned version first:
`node_modules/@earendil-works/pi-coding-agent/docs/` (`bun install` if missing).

## Layout

- `approval/` decides bash commands: the user's card or the Guardian reviewer.
  Auto mode only chooses which one answers. See [automode](docs/automode.md) and
  [security](docs/security.md).
- `sandbox/` wraps commands in Seatbelt or bubblewrap; `netproxy/` decides hosts.
- Rules, hosts, and launch policy are daemon-owned. `/permissions` changes only
  the current session.
- `shell/` and `execpolicy/` are faithful ports of codex-rs with its tests
  ported alongside; keep them faithful.

## Pull request reporting

`src/pullrequest.ts` mirrors `internal/hooks/pullrequest.go`; both run
`internal/hooks/testdata/pull-request-extraction.json`. Change them together.

## Shell parsing

Call `initShellParsing()` once at startup. Delete every tree-sitter tree after
use; they leak otherwise. `receipts/shell-parse-cost.ts` guards the costs.
