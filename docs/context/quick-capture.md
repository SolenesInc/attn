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

## Mac composer ownership

The main App owns the daemon connection. The lightweight capture webview talks
to that App through a dedicated, correlated event bridge. Its global shortcut
is native, separate from in-window bindings. The daemon setting is authoritative;
the native instance cache restores the acknowledged binding before reconnection.
`capture.shortcut` stores the binding; an explicit empty value means Off and
survives daemon restart. The Mac host validates native registration before the
daemon accepts the preference. Named instances default to no binding.

A composer-owned queue covers file reads, image decoding and per-file staging
through the main App's completion receipt. Overlapping additions and uploads
share that queue. Staging reads each transport-sized byte range directly from
the copied file's base64 data URL. Delayed staging persists the current draft,
so newer edits and removals survive restart.

Draft metadata and copied file data live in the instance cache. Files stage
while composing. Send waits for remaining uploads, persists an uncertain draft
identity, then requests acceptance with attachment IDs. Only an acceptance receipt clears it. Reconnection resolves that identity before
allowing a fresh send. A nonactivating NSPanel takes keyboard input without
activating the main window, and dismissal returns focus only if the user has
not switched elsewhere.
Capture uses AppKit's modal-panel level to appear above other apps' floating windows while staying below menus and system UI.

Before discarding draft uploads, the composer durably saves the retained draft
under a new capture identity. A partial deletion, lost reply or restart can then
restage the local files without reusing discarded daemon asset identities.
Recent materializes visible previews serially across openings, releases offscreen
previews, and unmounts them when hidden. Each preview still decodes its original
image; this is not a decoded-byte ceiling.

The composer has no shortcut settings screen. The main app's keyboard mapping
owns the global binding; Recent shows sent/read history and allows discarding the current draft.
Global bindings require Command, Control or Option so ordinary typing stays local.

Capture shares the main app uiScale setting. Its font shortcuts use the same
registry and customized bindings, then ask the main app to run its existing
scale actions; the daemon setting echo updates both windows. Cmd+comma does
nothing in capture. There is no separate capture font preference.

An external file drag can take AppKit key focus while the textarea keeps DOM
focus. Wry sends the drop as a webview event. Capture queues its existing panel
and embedded-webview focus sequence after that callback, without activating
the application or changing the retained draft and origin.

Native scenarios run on hosted Mac CI to preserve the user's desktop focus.
The main scenario covers immediate typing, images, focus return, full-screen
apps, restart and shortcut lifecycle, with attributed idle measurements.
For this implementation, the user waived native IME verification and left
ordinary second-Desktop behavior for a later manual check. CI has no input-source
or Mission Control probe; those two cases are not verified by the hosted scenario.

## Attachment work capacity

The composer shares two work slots between reading/decoding files and staging
individual attachments through the main App's completion receipt. More files
wait in arrival order. Removing a queued file prevents its read/upload; a failed
operation releases its slot and leaves a visible error with the retained draft.

The capacity receipt is hosted Mac CI [37131748373](https://github.com/SolenesInc/attn/actions/runs/37131748373),
revision `14376b803`, on VirtualMac2,1 with macOS 15.7.9. Each capacity starts a
fresh app process and empty draft. The workload is twenty 3168×1344 PNGs copied
from `docs/banner.png`, individually tagged with a PNG tEXt identifier so their
data URLs differ. Ten files contain 5,260,475 bytes and ten contain 5,260,476
bytes, 105,209,510 bytes total. The screenshot pixels are unchanged. Per-file
hashes and attributed process samples are in the [native artifact](https://github.com/SolenesInc/attn/actions/runs/37131748373/artifacts/11277342578).

| Work slots | All previews ready | All staged | Peak WebKit RSS | Peak app + WebKit RSS |
| --- | --- | --- | --- | --- |
| 1 | 3.455 s | 7.515 s | 869.9 MiB | 1,083.4 MiB |
| 2 | 2.412 s | 5.053 s | 961.7 MiB | 1,190.1 MiB |
| 4 | 2.359 s | 4.927 s | 991.1 MiB | 1,215.5 MiB |
| 8 | 2.324 s | 5.281 s | 921.7 MiB | 1,199.2 MiB |

Two slots capture most of the improvement over one (1.043 s). Four and eight
save only 53 and 88 ms in preview readiness while their combined sampled peaks
are higher; eight also stages more slowly than two. This is one hosted run,
with ordinary GC variation, rather than a general latency or memory ceiling.
RSS is sampled every 50 ms for the app and all WebKit services attributed through
its launchctl bootstrap domain. Sampled peaks are lower bounds on transient
peaks. Preview timing ends after read/decode; motion completion is verified
separately. Staged timing waits for daemon receipts and includes the final RSS
sample.

The retained-file workload is a 20-page screenshot PDF containing 142,323,707
bytes. Native drop, save, process restart, restore and recipient CLI retrieval
preserved SHA-256 `85175cbd833db887ce3ab269e8f4a0c7a85670902b4417d4700d5db6c2cb4943`.
The instance's per-file data URL cache remains in place. Upload reads bounded
base64 ranges directly, avoiding the whole-file URL fetch that failed on this
workload. Files have no configured size or count cap; read, retention and upload
failures remain visible and do not produce a saved receipt.
