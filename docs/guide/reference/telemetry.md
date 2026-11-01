# Telemetry

Attn can record how it is used on your machine and, if you choose, share that
record with Attn's maintainers. Both are off by default. **Settings → Analytics**
shows every record, exactly as it is stored and sent.

## Settings

| Setting | Default | What it does |
|---|---|---|
| `analytics.enabled` | off | Records usage in the local outbox, gives new sessions `attn vent`, and shows **Settings → Analytics**. Experimental. |
| `analytics.sharing_enabled` | off | Uploads the outbox to Attn's maintainers. Needs `analytics.enabled`. |
| `analytics.share_vents` | off | While sharing is on, queues each new Attn vent, with its text, for the next upload. While the setting is off, a vent leaves only when you press `x` on the vent. |
| `analytics.tool_vents` | off | Lets new sessions also file tool vents, about the agent's other tools. Tool vents never leave the machine. |

With `analytics.enabled` off, nothing is recorded and nothing is sent, regardless of the other settings.

## What is recorded

Every record is one line of JSON:

```json
{"stream":"bus","seq":2323797,"name":"git.operation.finished","subject":"h:9c1e04b7aa21",
 "at":"2026-10-19T14:02:11Z","bytes":212,
 "props":{"kind":"refresh_repository","status":"succeeded","duration_ms":18,"path":{"s":24}}}
```

| Stream | Records |
|---|---|
| `bus` | Every fact on Attn's event bus, except `vent.*` facts |
| `app` | Shortcuts and palette commands used, Settings sections opened, queue advances, the kind of a UI error |
| `cli` | The outcome of each `attn` command, whether an agent or a person ran it, and the agent's harness. A command that an agent runs, including a hook that the agent's harness runs, has the agent's hashed session as its subject, so the record matches that session's facts. A command from a person's shell has no subject. |
| `sample` | A resource sample of the daemon every 5 minutes: memory, goroutines, open files, CPU time, database size, bus size, connected clients |
| `vent` | An Attn vent you chose to send |

`seq` counts records within a stream. The server uses it to store each record once.

Attn redacts records on the `bus`, `app`, `cli` and `sample` streams before
it stores them:

- Numbers, booleans and nulls are kept.
- A string becomes its length, `{"s": 24}`, unless its field is on the
  allowlist in `internal/analytics/allowlist.go`. The allowlist holds fields
  whose values come from a fixed set, such as a status or a kind.
- A subject (a session, path, pull request or other id) becomes a keyed hash,
  `h:` and 12 hex digits, with a secret that never leaves the machine
  (`<data-dir>/analytics-secret`). The same subject hashes the same way, so records about one thing can be grouped without revealing what the thing is.
- Session facts carry the harness, model, how the session was launched (user,
  delegation, automation, crew, chief) and its state. Repository facts carry
  three booleans: has an `AGENTS.md`, is a worktree, has a remote. Names and paths never leave the machine.

An agent files each vent as one of two kinds. An Attn vent is about Attn itself:
its commands, its instructions, or how it works inside the harness. A tool vent
is about the agent's other tools: a skill, another CLI or MCP server, the
harness's own permissions and approval review. Agents learn about tool vents
only when `analytics.tool_vents` was on when their session launched. Only Attn
vents can be sent. Press `s` on a vent to file it under the other kind. Filing a
queued vent under tools takes the vent out of the queue, and a sent vent stays
an Attn vent.

A vent record carries the vent whole, because a vent without its text says
nothing:

```json
{"stream":"vent","seq":4,"name":"vent","at":"2026-10-24T14:02:11Z",
 "props":{"text":"...","harness":"claude","harness_version":"2.1.3","model":"claude-opus-5-5",
          "turn":"...","experimental":["asks_enabled"],"build":{...}}}
```

The session title, machine, repository, worktree, stake seed and transcript
stay on the machine. **Settings → Analytics** lists a vent record only in the
profile the vent belongs to.

## What is sent

While sharing is on, Attn uploads to `https://attn-telemetry.caketroll.cloud`
every 5 minutes, and once right away when you turn sharing on. Each upload is
one batch of at most 5,000 unsent records, oldest first, preceded by the details of the build that sent it:

```json
{"build":{"version":"0.42.1","commit":"a3f9e1c","channel":"release","build_time":"...","fingerprint":"...",
          "protocol":"18b5be9e8339","os":"darwin","arch":"arm64","dirty":false,
          "app_version":"0.42.1","app_channel":"homebrew"}}
```

The busiest minute measured on a real install held 882 facts, so five minutes
hold at most 4,410 and one scheduled batch carries them. One batch is half the
service's limit of 10,000 records a minute, so scheduled uploads never reach it,
even while catching up after time offline. Turning sharing on uploads right
away, outside the schedule. Turning sharing on several times within a minute
while a backlog waits can reach the limit, and Attn then waits for the retry delay that the refusal names.

The first upload registers the install and keeps the install's token in
`<data-dir>/analytics-token`. The token identifies the install, not you. The
server also keeps a keyed hash of the address each batch came from, never the
address. [attn-ingest](../../internal/attn-ingest.md) describes the service and
its limits.

When the service refuses a batch, the records stay and **Settings → Analytics**
shows the refusal with the limit, the limit's value and the value the batch
would have reached. Attn waits for the retry delay that the refusal names before it tries again.

## Keeping and deleting

Attn deletes records from the local outbox 7 days after it writes them. One
exception: while sharing is on, a record not yet sent stays up to 30 days, so
Attn can still upload it. Vents you dismiss or that their agent withdraws are
deleted after 30 days.

After Attn has sent something, **Settings → Analytics** offers **Forget this
install** when you turn sharing off. **Forget this install** deletes what the
server holds, then starts a new install with a new install id, token and subject
secret. When Attn cannot reach the service, nothing changes and **Settings →
Analytics** says so. **Clear the local record** deletes the local outbox and
dismissed vents.
