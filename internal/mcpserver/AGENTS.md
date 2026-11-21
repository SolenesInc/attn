# attn's own MCP server

attn serves `/mcp` with this package instead of
`github.com/modelcontextprotocol/go-sdk`, to keep resource costs down. Every
`attnd` start paid for the SDK, and so did every CLI command and agent hook.
Measured on Linux on 2026-11-21, against `de51fea3e`, which served `/mcp` with
the SDK v1.8.0:

| `attnd` | With the SDK | With this package |
| --- | --- | --- |
| Binary | 57,085,160 B | 54,725,152 B |
| Package init allocations, `attnd version` | 5,074 | 4,175 |
| Resident memory at exit, transparent huge pages off | 33.1 MB | 28.3 MB |

The SDK's `mcp` package imports `encoding/gob`, its OAuth packages and
`github.com/google/jsonschema-go`, and the SDK has no narrower package. `/mcp`
needs a small part of MCP, so this package serves only that part.

## Keep it lightweight and stateless

- The server keeps no state between requests. It sets no session id and holds
  no stream open. Each POST carries one JSON-RPC message and gets one JSON body
  back. A notification gets 202, and GET and DELETE get 405.
- It answers `initialize`, `ping`, `tools/list` and `tools/call`, and refuses
  every other method with -32601. It negotiates protocol versions 2024-11-05
  to 2025-11-25 in `initialize` and ignores the `MCP-Protocol-Version` header.
- Claude Code opens with `server/discover` (protocol 2026-07-28). The -32601
  makes it fall back to `initialize`. Codex and `@earendil-works/pi-mcp` start
  with `initialize`.
- `Schema` checks only the JSON Schema keywords that `cmd/cligen` writes into
  tool input schemas, and `ParseSchema` refuses any other keyword. When the
  generator writes a new keyword, add it to `Schema` and to the drawing in
  `schema_test.go`. That test compares `Schema` with jsonschema-go over every
  catalog input.
- The SDK and jsonschema-go stay test-only. The `/mcp` wire tests connect with
  the SDK's client to prove that the server speaks MCP. `make lint-go` refuses
  both packages in `attnd`.

## Agree a departure with the user first

Before you keep state between requests, hold a stream open, serve another
method or add a dependency here, agree the change with the user. Bring the
requirement it serves and what it costs at start, measured as
[Performance testing](../../docs/internal/perf-testing.md) describes.
