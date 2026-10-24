# Telemetry

attn can record how it is used on your machine and, if you choose, share that
record with attn's maintainers. Both are off by default. Settings › Analytics
shows every record, exactly as it is stored and sent.

## Settings

| Setting | Default | What it does |
|---|---|---|
| `analytics.enabled` | off | Records usage in the local outbox, gives new sessions `attn vent`, and shows Settings › Analytics. Experimental. |
| `analytics.sharing_enabled` | off | Uploads the outbox to attn's maintainers. Needs `analytics.enabled`. |
| `analytics.share_vents` | off | While sharing is on, queues each new vent, with its text, for the next upload. Without it, a vent leaves only when you press `x` on it. |

With `analytics.enabled` off, nothing is recorded and nothing is sent, whatever
the other two say.

## What is recorded

Every record is one line of JSON:

```json
{"stream":"bus","seq":2323797,"name":"git.operation.finished","subject":"h:9c1e04b7aa21",
 "at":"2026-10-19T14:02:11Z","bytes":212,
 "props":{"kind":"refresh_repository","status":"succeeded","duration_ms":18,"path":{"s":24}}}
```

| Stream | Records |
|---|---|
| `bus` | Every fact on attn's event bus, except `vent.*` facts |
| `app` | Shortcuts and palette commands used, Settings sections opened, queue advances, the kind of a UI error |
| `cli` | The outcome of each `attn` command, and whether an agent or a person ran it |
| `sample` | A resource sample of the daemon every 5 minutes: memory, goroutines, open files, CPU time, database size, bus size, connected clients |
| `vent` | A vent you chose to send |

`seq` counts records within a stream, so the server stores each record once.

Records on the `bus`, `app`, `cli` and `sample` streams are redacted before
they are stored:

- Numbers, booleans and nulls are kept.
- A string becomes its length, `{"s": 24}`, unless its field is on the
  allowlist in `internal/analytics/allowlist.go`. The allowlist holds fields
  whose values come from a fixed set, such as a status or a kind.
- A subject (a session, path, pull request or other id) becomes a keyed hash,
  `h:` and 12 hex digits, using a secret that never leaves the machine
  (`<data-dir>/analytics-secret`). The same subject hashes the same way, so
  records about one thing can be grouped without saying what it is.
- Session facts carry the harness, model, how the session was launched (user,
  delegation, automation, crew, chief) and its state. Repository facts carry
  three booleans: has an `AGENTS.md`, is a worktree, has a remote. Names and
  paths never leave.

A vent record carries the vent whole, because a vent without its text says
nothing:

```json
{"stream":"vent","seq":4,"name":"vent","at":"2026-10-24T14:02:11Z",
 "props":{"text":"...","harness":"claude","harness_version":"2.1.3","model":"claude-opus-5-5",
          "turn":"...","experimental":["asks_enabled"],"build":{...}}}
```

The session title, machine, repository, worktree, stake seed and transcript
stay on the machine.

## What is sent

While sharing is on, attn uploads to `https://attn-telemetry.caketroll.cloud`
every 5 minutes, and once right away when you turn sharing on. Each upload is
one batch of at most 5,000 unsent records, oldest first, preceded by the build
that sent it:

```json
{"build":{"version":"0.42.1","commit":"a3f9e1c","channel":"release","build_time":"...","fingerprint":"...",
          "protocol":"18b5be9e8339","os":"darwin","arch":"arm64","dirty":false,
          "app_version":"0.42.1","app_channel":"homebrew"}}
```

The busiest minute measured on a real install held 882 facts, so five minutes
hold at most 4,410 and one scheduled batch carries them. One batch is half the
service's limit of 10,000 records a minute, so scheduled uploads never reach it,
even while catching up after time offline. Turning sharing on uploads right
away, outside the schedule; turning it on several times within a minute while a
backlog waits can reach the limit, and attn then waits as the refusal asks.

The first upload registers the install and keeps its token in
`<data-dir>/analytics-token`. The token identifies the install, not you. The
server also keeps a keyed hash of the address each batch came from, never the
address. [attn-ingest](attn-ingest.md) describes the service and its limits.

When the service refuses a batch, the records stay and Settings › Analytics
shows the refusal with the limit, its value and what the batch would have
made it. attn waits as long as the refusal asks before trying again.

## Keeping and deleting

Records stay in the local outbox for 7 days, sent or not. While sharing is on,
records not yet sent may wait up to 30 days for an upload. Vents you dismiss or
that their agent withdraws are deleted after 30 days.

Turning sharing off, after something was sent, offers Forget this install.
Forget this install deletes what the server holds, then starts a new install: a new install id, token
and subject secret. When the service cannot be reached, nothing changes and the
page says so. Clear the local record deletes the local outbox and dismissed
vents.
