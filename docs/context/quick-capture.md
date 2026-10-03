# Quick Capture backend contract

Captures are user-authored messages owned by the home daemon. Destinations are
stable `{kind:"chief"}` or `{kind:"crew",member_id:"…"}` identities. The daemon
maps these to inbox Chief/member addresses and commits the capture, its attachment
ownership, and its inbox item in one transaction before acknowledging. The inbox
owns delivery, role following, attempts, wake limits and restart recovery. An
absent Chief waits for the next Chief; a crew delivery wake charges the member's
wake allowance. Capture has no independent scheduler or delivery actions.

Commands and generated types are in `internal/protocol/schema/main.tsp`:

```text
capture_send(capture_id, target, content, attachment_ids)
capture_get(capture_id)
capture_list(limit, cursor?) -> items + draft_assets + next_cursor?
capture_attachment_put(capture_id, attachment_id, name, offset, data_base64, final)
capture_attachment_get(capture_id, attachment_id, offset)
capture_attachment_discard(capture_id, attachment_id)
```

All commands accept optional `request_id`. WebSocket replies use `capture_result`
with `{request_id,success,error?,error_code?,result?}`. Unix download replies use
Response.capture_result. The result contains respectively `record`, `list`,
`upload`, `download`, or `discarded`. Send returns the whole saved record. Failed
requests return an error, never a saved receipt. `capture_changed {capture_id}`
invalidates cached history when a capture is saved or read. Missing capture_get
identities return error_code=capture_not_found on the app channel.

History is newest first, ordered by creation time and ID. A record contains
content, destination, creation time, attachments and optional `read_at` from its
inbox item. A read receipt means an `attn agent inbox` fetch committed; it does
not mean the agent understood or acted on the capture. Inspection never marks a
capture read. Pass a positive page limit; the opaque next_cursor resumes after
the last returned record, including when newer captures arrive between pages.
A damaged draft or failed recovery sync stays staged and is logged; it never
blocks daemon startup or becomes a saved capture. The user can discard it.
Draft states are staged or ready. Capture and attachment IDs are caller-generated
UUIDs. Identical retries reuse the same capture ID; conflicting payloads fail.
Two intentional identical messages have different IDs. Ordered attachment IDs
are part of submission identity.

File transfer is sequential, with offset receipts and matching-byte replay.
Use 524288-byte raw upload chunks for the app WebSocket (1048576-byte frames).
Upload accepts any chunk fitting that transport; downloads use half the requesting
transport's frame size (Unix 65536-byte frames, WebSocket 1048576-byte frames).
Compact WebSocket uploads with UUID identities and `screenshot.png` measured
699309 JSON bytes at 524288 raw bytes and 43949 JSON bytes at 32768 raw bytes.
An isolated-daemon WebSocket upload of the repository's 5,260,454-byte
`docs/banner.png` took 6.394 seconds for 161 small chunks and 1.633 seconds for
11 large chunks, including the image decoding that was present then. After
finalization-only sync and removal of request re-encoding, one run measured
4.003/2.908 seconds respectively; concurrent host load makes these wall times
unsuitable for claiming a latency improvement. These are historical transport
receipts, not measurements of the current sender.

The Unix client preflights encoded requests and names the limit and actual size
on overflow; WebSocket clients must also respect the socket's read boundary.
This is a transfer chunk, never a file-size cap. Final bytes and directories are
synced before a ready receipt. Recovery repairs staged offsets and finalization
transitions; send refuses unfinished uploads or mismatched byte receipts.

Attachments accept any file as-is, without decoding or a format list. Media type
is best-effort: detect from the first bytes, then use the filename extension if
detection returns application/octet-stream, otherwise retain that default.
Native clipboard conversion belongs in the app. Reading retains committed files.
Explicit discard releases only uncommitted assets; capture_list.draft_assets
exposes interrupted uploads for reconciliation, with no silent expiry.

Capture authoring and history require the trusted app WebSocket channel: Tauri
origin, tauri-app identity and browser-host credential. A general client token
alone cannot author or inspect captures. The agent-accessible Unix socket refuses
send, get, list, upload and discard with an explicit app-only error. It retains
attachment download by IDs provided in an inbox item; inbox reads remain scoped
to the addresses the recipient holds.

The trusted-app gate is a guardrail for the application request path, not OS
isolation from an agent with a shell under the same Unix UID. Such an agent can
read the host credential and reach the daemon's SQLite/config. Quick Capture
follows attn's existing local trust model. The Garden follow-up
`decide-whether-attn-needs-os-level` (`s-6bna4w`) records the provenance question.

Home/outpost ownership follows Garden and crew. Outposts refuse capture commands
because the enrollment uplink is not built. Role delivery uses the inbox's local
holder resolution; it does not relay files to remote hosts. Recipients retrieve
files using `attn agent attachment <capture-id> <attachment-id> --out <path>` and
inspect each saved file with their tools. No home-local path is advertised as a
remote-host path. Remote delivery/retrieval requires a future home uplink.
