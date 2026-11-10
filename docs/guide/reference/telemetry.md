# Telemetry

Attn can record how it is used on your home and its outposts and, if you choose,
share that record with Attn's maintainers. Both are off by default. **Settings → Analytics**
shows every record the home holds, exactly as it is stored and sent. A record
made on an outpost shows there once it reaches the home: within 5 minutes while
the outpost is connected, and after it reconnects otherwise. The page names each
outpost it can't reach.

## Settings

| Setting | Default | What it does |
|---|---|---|
| `analytics.enabled` | off | Records usage on the home and each outpost, and gives new sessions `attn vent`. Turn it on in **Settings → Analytics**. The home decides it. Experimental. |
| `analytics.sharing_enabled` | off | Uploads the outbox to Attn's maintainers. Needs `analytics.enabled`. |
| `analytics.share_vents` | off | While sharing is on, queues each new Attn vent, with its text, for the next upload. While the setting is off, a vent leaves only when you press `x` on the vent. |
| `analytics.tool_vents` | off | Lets new sessions also file tool vents, about the agent's other tools. Tool vents never leave the machine. |

With `analytics.enabled` off, the home and every connected outpost record nothing
and send nothing, regardless of the other settings. An outpost that can't reach
the home can't learn that you turned it off, so it keeps recording to its own
disk until it reconnects. It then deletes what it holds, and none of that reaches
the home or the server.

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
| `cli` | One record per `attn` command, named by its command path as typed (`seed check report`, or `_hook-state` for a harness hook): whether it succeeded, its exit code, what kind of error ended it (`refused`, `failed`, `usage`, `unknown_command`, `unknown_flag`, `missing_value`, `invalid_value`), why the daemon refused it (`invalid_input`, `limit`, `not_found`, `wrong_state`, `not_tender`, `conflict`, `unclassified`), whether an agent or a person ran it, and the agent's harness. A command that an agent runs, including a hook that the agent's harness runs, has the agent's hashed session as its subject, so the record matches that session's facts. A command from a person's shell has no subject. |
| `sample` | One `daemon_start` record each time the daemon starts, and a `process` sample every 20 minutes. [Process samples](#process-samples) lists what a sample holds. Every record of one daemon run has that run's hashed run id as its subject, a random id the daemon makes at start. `daemon_start` says how the previous run ended (`previous_end`): `stopped` on purpose, with who stopped it in `stop_reason` (`stop` for `attn daemon stop`, why `attn daemon ensure` replaced it, such as `source_fingerprint_mismatch`, or `app_recovery` when the app stopped it because `attn daemon ensure` failed), `machine_restarted`, `exited_unexpectedly`, or `unknown` when there was no previous run on this data folder. `previous_run` is the previous run's subject. |
| `run` | One record per agent run, from the moment the agent goes working to its stop, when `analytics.enabled` was on for the whole run. An approval wait does not end a run. The record says what started the run (`user`: your words; `ring` or `heartbeat`: Attn's own input; `launch`: the launch's initial prompt; `agent`: anything else, such as the agent resuming on its own; words you add while a run works do not change it), its length (`duration_ms`), the state it stopped in (`ended`), whether the stop opened a turn for you (`opened_turn`), how many approvals it waited on (`approvals`) and the session's harness, model and launch. `usage` is `known` when Attn read the transcript after the run, and the record then carries the run's `tokens` (`input`, `output`, `cache_read`, `cache_write`) and, when the models are priced, `usd`. `usage` is `unavailable` or `incomplete` when Attn could not measure the run. `quiet_stop` is true when a notification started the run and `queue_quiet_notifications` kept its stop out of your queue unless the agent asked, and `turn_requested` says whether the agent ran `attn turn open` during the run and did not withdraw it. `turn_after_stop` says what the queue holds once the run stops: `owed` when it shows the agent, including a turn that was already open, `snoozed` when your snooze will show the agent when it ends, and `none` when it does not show the agent. `user_input_ms` is how long into the run you first typed, and it is absent when you did not. The record's subject is the session's hash, and `at` is the run's end, so the `cli` records of `_hook-tool-use` between `at` minus `duration_ms` and `at` are the run's tool calls. The stream also holds one `quiet_stop` record for each quiet stop that left no turn (`turn_after_stop` is `none`) or whose agent asked for one, with the same subject and `at` as its run. See [Quiet stop verdicts](#quiet-stop-verdicts) |
| `vent` | An Attn vent you chose to send |

`seq` counts records within a stream. The server uses it to store each record once.

An outpost records its own usage and sends it to the home, and only the home
uploads. A record from an outpost names that outpost in its stream, as a hash:
`bus_m9c1e04b7aa21`. Records wait on the outpost while the home is unreachable,
and reach the home when it reconnects, unless you turned analytics off, cleared
the record or forgot the install in the meantime. Turning analytics off also
deletes what a connected outpost has not sent yet.

### Process samples

A `process` sample holds the daemon's memory, goroutines, open files and CPU
time, the database size, the bus size and the connected clients, and these
fields, so that memory can be compared across builds at a similar workload. The
build that took a sample is the build stored with its record, which an upload
batch names.

```json
{"workload":{"sessions":9,"agents":7,"terminals":9,"panes_drawn":4,"desktops":3,"seeds":812},
 "processes":[{"role":"webkit_content","count":2,"resident_bytes":614000000,"footprint_bytes":1137000000,
   "split":{"webkit_malloc_bytes":602000000,"owned_graphics_bytes":398000000,"gigacage_bytes":41000000}}]}
```

| Field | Holds |
|---|---|
| `workload.sessions`, `workload.agents` | Sessions the daemon holds, in every profile, and those among them that run a harness rather than a shell |
| `workload.terminals` | Terminals the daemon runs |
| `workload.panes_drawn` | Terminals a connected app is attached to, which are the terminals it draws while the shared PTY host is on. With the host off, the app also stays attached to some terminals it hides |
| `workload.desktops`, `workload.seeds` | Desktops, and seeds in any state, in every profile |
| `processes[].role` | `daemon`, `pty_worker`, `pty_host`, `app`, `webkit_content`, `webkit_gpu` or `webkit_networking`. A role with no running process has no entry, so an outpost, which has no app, lists only the first three |
| `processes[].count` | How many processes hold the role |
| `processes[].resident_bytes` | Their total resident memory |
| `processes[].footprint_bytes` | Their total physical footprint, as Activity Monitor's Memory column shows it. macOS only |
| `processes[].split` | For `webkit_content` on macOS, the part of that footprint in three of `footprint`'s categories, summed over the processes: `webkit_malloc_bytes` (WebKit malloc), `owned_graphics_bytes` (owned physical footprint, graphics) and `gigacage_bytes` (JS VM Gigacage). Each counts the category's dirty and compressed memory. It is absent when `footprint` could not measure every WebContent process |

The app tells the daemon its process id when it connects, and the daemon finds
the app's WebKit processes from it. WebKit processes of other apps, and of other
attn instances, are not counted. On macOS, the kernel ties the WebKit processes of
an app started from a terminal to that terminal, so that app's sample has an
`app` entry and no `webkit_*` entries. The sample reads the kernel's process tables.
On macOS it also runs `/usr/bin/footprint` once for the WebContent processes,
which took 20-40 ms for a WebContent of 0.4 to 1.9 GB. With
`analytics.enabled` off, it reads nothing.

Attn redacts records on the `bus`, `app`, `cli`, `sample` and `run` streams before
it stores them:

- Numbers, booleans and nulls are kept.
- A subject (a session, path, pull request or other id) becomes a keyed hash,
  `h:` and 12 hex digits, with a secret that never leaves your machines
  (`<data-dir>/analytics-secret` on the home, which copies it to its outposts over
  the bridge). The same subject always gives the same hash, so records about one thing group together without revealing what the thing is.
- A string becomes its length, `{"s": 24}`, unless
  `internal/analytics/fields.go` classifies its field. A field whose values
  come from a fixed set, such as a status or a kind, is kept as it is. A field
  that holds an id, such as the session that planted a seed
  (`caused_by_session_id`), becomes the same keyed hash as that id's subject,
  so a record can be joined to the records about the thing it names. A field
  that names who acted, such as who closed a session (`closed_by`), keeps
  `user` and `attn` as they are and hashes any other actor the same way.
- A failed git operation says why it failed without its error text: `failure`
  names the reason from a fixed set, such as `missing_directory`, `timeout` or
  `exited`, and `exit_code` gives git's exit code when git itself exited
  non-zero. The error text becomes its length.
- A session that leaves (`session.unregistered`) says how it ended:
  `state_reason` names why it was in its last state, such as `process_exited`
  or `stuck`, and `terminal_exit.signal` names the signal that ended its
  terminal, such as `killed`.
- `setting.changed` names the setting in plain text, because setting names come
  from a fixed set. A setting whose name carries a value, such as
  `new_session_destination_<repository>`, is named by that pattern, never by
  its concrete name.
- Session facts carry the harness, how the session was launched (user,
  delegation, automation, crew, chief), its state and two model fields.
  `model` is the model of the session's main loop, as the harness reports it,
  such as `claude-opus-5-5`: the model with the most tokens in the session's
  own transcript. Subagents never choose it. It is absent until the main loop
  reports usage. `model_requested` is the
  model attn launched the session with, as it was given, such as the alias
  `opus`. Repository facts carry three booleans: has an `AGENTS.md`, is a
  worktree, has a remote. Names and paths never leave the machine.

An agent files each vent as one of two kinds. An Attn vent is about Attn itself:
its commands, its instructions, or how it works inside the harness. A tool vent
is about the agent's other tools: a skill, another CLI or MCP server, the
harness's own permissions and approval review. Agents learn about tool vents
only when `analytics.tool_vents` was on when their session launched. Only Attn
vents can be sent. Press `s` on a vent to file it under the other kind. Filing a
queued vent under tools takes the vent out of the queue, and a sent vent stays
an Attn vent.

A vent record contains the whole vent, because a vent without its text has
no content:

```json
{"stream":"vent","seq":4,"name":"vent","at":"2026-10-24T14:02:11Z",
 "props":{"text":"...","harness":"claude","harness_version":"2.1.3","model":"claude-opus-5-5",
          "model_requested":"opus","turn":"...","experimental":["pty_shared_host_enabled"],"build":{...}}}
```

The session title, machine, repository, worktree, stake seed and transcript
stay on the machine. **Settings → Analytics** lists a vent record only in the
profile the vent belongs to.

### Quiet stop verdicts

A `quiet_stop` record measures whether agents whose run a notification started leave
results you never see. With `analytics.enabled` on, Attn makes one call per quiet
stop to the small model that judges your sessions' states, through the session's
harness or, for a harness with no classifier, Claude Code. The call sends the last
message of the run, at most 4,000 characters of it, and costs about $0.0012 on
your harness account. The message is the answer the harness reported for the run,
or else the run's last message in the transcript. Only the verdict is recorded:

- `verdict` is `read` when the model says you need to read the message, `nothing`
  when it says you don't, `no_message` when the run wrote no new message, and
  `failed` when Attn got no answer. A failure, such as a usage limit, is never
  recorded as `nothing`.
- `failure` says why: `headless_tasks_off`, `message_unavailable` (the harness
  reported no answer and the session has no transcript), `transcript`, `harness`
  or `unusable_answer`.

The verdict only records. It never opens a turn, settles one or changes what you
see. The message text stays out of the record.

## What is sent

While sharing is on, Attn uploads to `https://attn-telemetry.caketroll.cloud`
every 5 minutes, and once right away when you turn sharing on. Only the default
instance on the default data folder uploads there. Any other instance uploads
to `http://127.0.0.1:1`, where nothing listens, unless its daemon starts with
`ATTN_ANALYTICS_INGEST_URL` naming another service. The Share row in
**Settings → Analytics** shows where uploads go. Each upload is one batch of at
most 5,000 unsent records, oldest first, all recorded by the same build. The
batch starts with the details of that build and of the app connected when the
records were written, so records written before an upgrade keep the build that
wrote them. `dirty` is true when the source the build ships differs from its
commit; changes only to tests, scripts or docs the build does not embed leave
it false:

```json
{"build":{"version":"0.42.1","commit":"a3f9e1c","channel":"release","build_time":"...","fingerprint":"...",
          "protocol":"18b5be9e8339","os":"darwin","arch":"arm64","dirty":false,
          "app_version":"0.42.1","app_channel":"homebrew"}}
```

The busiest minute measured on a real install held 882 facts, so five minutes
hold at most 4,410 and one scheduled batch carries them. Records written by
another build, such as after an upgrade or once the app connects, go in the next
upload. One batch is half the service's limit of 10,000 records a minute, so scheduled uploads never reach it,
even while catching up after time offline. When you turn sharing on, Attn uploads right
away, outside the schedule. If you turn sharing on several times within a minute
while a backlog waits, the uploads can reach the limit, and Attn then waits for the retry delay that the refusal names.

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
Attn can still upload it. Attn deletes vents that you dismiss, or that their agent withdraws,
after 30 days.

After Attn has sent something, **Settings → Analytics** offers **Forget this
install** when you turn sharing off. **Forget this install** deletes what the
server holds, then starts a new install with a new install id, token and subject
secret. When Attn cannot reach the service, nothing changes and **Settings →
Analytics** reports the failure. An outpost drops what it holds when it hears of
the forget, and the home refuses those records until then. **Clear the local
record** deletes the outbox on the home and each outpost, and dismissed vents. An
outpost that is unreachable clears when it reconnects, and the home refuses what
it held.
