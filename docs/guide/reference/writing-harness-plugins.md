# Writing a harness plugin

A harness plugin adds an agent harness to Attn. Once the plugin is installed,
the harness shows in the new-agent picker, and in `attn delegate --agent`
when it takes an initial prompt, and Attn launches it in a terminal like Claude Code and Codex. Attn needs no change and
no restart. You write the plugin in TypeScript with
[`@victorarias/attn-plugin`](https://www.npmjs.com/package/@victorarias/attn-plugin),
or in any language that speaks the JSON-RPC described below.

What you need: `bun` on the machine that runs the Attn daemon, and an Attn
daemon built from source revision `13595cac6922bb7b2d3d89d62e4879be500f7aa3`
or later. No release carries that revision yet, so an older daemon refuses the
registration with `unsupported driver capability "attributed_input"`,
`"safe_during_question"` or `"compaction"`, and the SDK turns that refusal into
the revision to upgrade to. A terminal-only plugin that declares none of those
runs on any API 7 daemon. Declaring `agent_tools` needs a daemon built from
revision `327521b2fb82f71a2f146f51b9aa6a85b18fb969` or later. Shared servers additionally need the
`server_binding` capability described below.

## From nothing to a running harness

The checkout's `examples/plugins/sample-harness` is a complete plugin. It runs
a local echo agent, takes direct input, and has no outside dependency. To make
your own from it:

```bash
cp -R examples/plugins/sample-harness my-harness && cd my-harness
# In attn-plugin.toml, change name to your plugin's name, such as "my-harness".
# In src/index.ts, change harnessId to your harness's ID, such as "my-agent".
attn plugin install .
```

The plugin's name and the harness's ID are two things: the name is the
installed folder and what `attn plugin remove` takes, and the ID is what the
picker shows. Install runs `bun install` in a copy, so `package.json` has to
name a version of `@victorarias/attn-plugin` that exists. `attn plugin install`
also takes a `.tgz` archive or a Git URL. Use `attn plugin link .` while you
develop, so Attn runs your directory in place, and `attn plugin reload
my-harness` to pick up a change. [Replace, reload and update](#replace-reload-and-update)
says how each works while sessions run.

Open the picker with Cmd+N. Your ID is a choice in the "Session agent" group.
`attn delegate --agent my-agent` starts it from a shell, and
`attn settings set new_session_agent my-agent` makes it the harness the picker starts with.
Attn runs the argv your `spawn` handler returned, in the terminal, in the
folder you picked.

The ID is yours to choose: lowercase letters, numbers, hyphens and underscores,
starting with a letter or number. The built-in names `claude`, `codex`, and `shell` are taken, as are `codex-app`, `codex-server`, and IDs
starting with `codex-server-`. Two plugins cannot register one ID.

## What the plugin declares

One call registers the harness. A handler you provide switches its capability
on, and a harness that leaves a handler out simply does not have that
capability: Attn never calls it and never offers it.

```ts
import { AttnPluginClient, accepted, deferred } from "@victorarias/attn-plugin";

const client = new AttnPluginClient({ version: "0.1.0" });

client.driver({
  id: "my-agent",
  attributedInput: true,
  spawn: (launch, run) => ({ argv: ["my-agent", "--cwd", launch.cwd] }),
  deliver: async (input, run) => {
    const taken = await submitToMyAgent(input.conversation_id, input.input_id, input.text);
    return taken ? accepted() : deferred();
  },
});

await client.connect();
```

| You provide | Capability | Attn then |
|---|---|---|
| `spawn` (required) | | starts the harness with the argv, `env` and `cwd` you return |
| `resume` | `resume` | resumes a closed session and runs `attn agent reload` |
| `resumeAvailable` | `resume_availability` | checks that a conversation still exists before offering a resume |
| `models` | `model_discovery` | lists the harness's models in Settings |
| `deliver` | `message_delivery` | hands annotations, messages and compaction notices to the plugin instead of typing them |
| `compact` | `compaction` | compacts the conversation before a session closes itself with `--compact` |
| `attributedInput: true` | `attributed_input` | tells `deliver` who is speaking and which conversation it is for |
| `safeDuringQuestion: true` | `safe_during_question` | delivers text while a permission question that came during a turn is open, because your `deliver` cannot answer it |
| `yolo`, `initialPrompt`, `modelPin`, `effortPin`, `launchInstructions` | the same names, in snake case | passes `yolo`, `initial_prompt` (needed to delegate to the harness), `model`, `effort` and `instructions` to `spawn` and `resume` |
| `stateReporting` | `state_reporting` | takes the launch's state only from `run.state` and stops inferring it from the terminal, so report every start and end of a turn |
| `pullRequestReporting` | `pull_request_reporting` | with `launchInstructions`, leaves out the instruction that tells the agent to report its own pull requests |
| `agentTools: true` | `agent_tools` | passes `agent_tools`, attn's MCP server, to `spawn` and `resume`, as [Give the harness attn's tools](#give-the-harness-attns-tools) describes |

`attributedInput`, `safeDuringQuestion` and `compact` need `deliver`. The SDK
refuses an invalid declaration when you call `client.driver`. Attn still
accepts the capabilities `classifier` and `pending_approval` from older
plugins, and does nothing with them.

`client.handle(...)` for worktree hooks stays separate. A plugin can declare
both, and a driver adds nothing to the `hello` surfaces.

### Where your harness finds skills

Attn's own delegation roles tell the agent to load the attn-workflow skill.
Attn installs that skill only where your harness finds user skills, so it
refuses those roles on a harness whose driver doesn't say where that is. Your
users' own roles and the fallback start either way. Declare each home whose
`skills` folder your harness reads with `skillDirs`, from SDK 0.5.0:

```ts
client.driver({
  id: "my-agent",
  spawn,
  skillDirs: [{ homeEnv: "MY_AGENT_HOME", defaultHome: ".my-agent" }],
});
```

The folder is `$homeEnv/skills`, or `~/defaultHome/skills` when `homeEnv` is
unset or empty. Attn reads the variable from its own environment and your
login shell's, as it does for a server home. `defaultHome` is absolute, or
relative to your home. When a harness reads skills from several homes, such as
one per model provider, list each one. Attn keeps attn-workflow in each folder matching its
own copy: when your driver registers, when roles are added, and before each
launch of one of its roles. A daemon released before `skillDirs` ignores it
and keeps refusing those roles.

### Your harness's logo

Attn draws your harness's mark next to its sessions. Give it with `logo`: an
SVG body drawn in `currentColor`, inside a square view box whose side is
`view`. Attn colors it with the session's state, so leave colors out.

```ts
client.driver({
  id: "my-agent",
  spawn,
  logo: { view: 24, body: '<circle cx="12" cy="12" r="9" fill="currentColor"/>' },
});
```

Attn refuses the registration when `view` is zero, negative or not a finite
number, or when `body` is over 64 KiB. Without a logo, attn draws its generic
mark. A daemon released before logos ignores the field.

### Give the harness attn's tools

attn serves its tools as an MCP server. With `agentTools: true`, from SDK
0.6.0, `spawn` and `resume` receive `agent_tools` while the user has attn's
tools on (the experimental setting Agent tools, `agent_tools_enabled`). Without
the declaration, or with the setting off, the field is absent and the agent
uses the attn CLI.

A harness that runs in the terminal gets
`{url, credential, description}`. Give it to the harness however the harness
takes an MCP server: a Streamable HTTP server at `url`, with the header
`Authorization: Bearer <credential>` on every request. The credential belongs
to the terminal, not to one conversation: each call acts as the session the
terminal shows at that moment, so after the agent clears or resumes into
another conversation, its calls act as that conversation's session. The
credential lasts while the terminal does. `description` is one line saying
what the tools are for, for a harness that lists its servers before it reads
them. The server's `instructions` hold attn's intent map, the text that says
when to reach for each tool. Pass the credential in `env`, never in `argv`:
any local user can read a process's arguments, while its environment is the
user's own. `examples/plugins/sample-harness` passes the endpoint to its
harness in the environment, and the harness's `/tool <name> <json>` line calls
a tool.

When your driver also declares `launchInstructions`, a launch that carries
`agent_tools` gets launch `instructions` without the CLI teaching of the
command families the tools offer, because the intent map teaches them. Show
your agent the server's `instructions`, or it learns those families from
neither.

A shared server gets the endpoint from attn itself; see
[Shared servers and attn's tools](#shared-servers-and-attns-tools).

A daemon built before `agent_tools` refuses the registration, and the SDK
names the build to upgrade to.

## What happens at launch

A **launch** is one start of the harness in one terminal. The SDK and the wire
call it a run (`run_id`). Attn picks the launch's input route when it starts the
launch, from the registration of that moment:

- With `deliver`, the launch is **direct**. Annotations and messages go to your
  handler, never to the terminal. Your keystrokes in the terminal still reach
  the harness. While your plugin is down, Attn holds automatic input until the
  plugin is back, and it never types it into the terminal instead. That holds
  after a daemon restart.
- Without `deliver`, the launch is **terminal-only**. Attn types the input.

`deliver` returns custody, not completion. Return `accepted()` only after the
harness holds the text. When Attn asks again with the same `input_id`, return
the same answer and submit nothing: the harness keeps the receipt, because the
plugin may have restarted in between. With `attributedInput`, compare
`conversation_id` with the conversation the launch shows before you accept, and
return `targetEnded()` when they differ. Other answers: `deferred()` for "try
again later", `rejected(reason)` for "never".

`voice` is `"user"` for an annotation, a typed conversation and a ring of the
user's words, and `"attn"` for everything else, such as a ring with a notice. A harness that tells its model who is speaking uses it.

A plugin tells Attn what a launch is doing with the `run` object:

```ts
await run.reportMetadata({ conversationId }, { resumeSessionId: conversationId });
await run.state("working");
await run.state("idle");
```

`resumeSessionId` is the harness's own conversation ID. Report it first:
`attributedInput` and `compact` wait until a launch has one. Reports during
`spawn` are queued and applied once the terminal is up, except
`run.pullRequest`, which Attn refuses until then. The `run` object numbers
the reports for you.

## When the plugin restarts

Attn restarts a plugin that exits, crashes or loses its connection for more
than five seconds. The harness keeps running in its terminal. On every
connection `client.connect()` gives `recover` the launches Attn still holds for
the plugin. Rebuild what you keep in memory before `recover` returns. The SDK
then registers the driver, and Attn can deliver input again. The sample keeps
nothing in memory: its socket path comes from the `run_id`, and the harness
deduplicates input by ID.

## Replace, reload and update

These commands restart the plugin the same way, without closing the sessions it
drives:

- `attn plugin install <source>` and `attn plugin link <dir>` replace an
  installed plugin of the same name, so one command switches between a copy and
  a linked checkout.
- `attn plugin reload <name>` runs `bun install` in the installed plugin and
  restarts it from its current files. `--no-install` skips `bun install`.
- `attn plugin update <name>` installs a copied plugin again from the
  directory, archive or Git URL it was installed from. `attn plugin list` shows
  that source. A linked plugin has none: reload it instead.

When a command replaces a running plugin, it answers once the new plugin has
connected; a first install answers once the plugin is installed and started. A
replacement or update
that exits or does not connect within 15 seconds is taken back: Attn restores
the previous copy, restarts it, and the command fails with the reason. A reload
has no previous copy to restore, so it fails and Attn keeps restarting the
plugin as it would after a crash.

The restart reaches only the plugin's own process. Code the harness loaded from
the plugin's folder, such as an extension a TUI agent runs in its own process,
keeps the version it loaded until its session reloads or a new session starts.

`attn plugin remove` still refuses a plugin that owns a running session.

## Bind a shared app-server

A harness with a shared server can declare `server` instead of a `deliver`
handler. Attn joins a running server or starts one for its home, submits input
directly and follows the server's status. The plugin prepares terminal views.
An open conversation can keep receiving input after its last view moves on.

The declaration needs SDK 0.7.0, which speaks `attn-app-server-v2`, and a
daemon that speaks it too. A daemon without `server_binding` refuses that
capability, and a daemon on v2 refuses a declaration of `attn-app-server-v1`. To make your own from the sample:

```bash
cp -R examples/plugins/sample-server /tmp/my-server-plugin
# In src/index.ts, change harnessId to your own ID and adapt view() to your CLI.
attn plugin install /tmp/my-server-plugin
```

The sample expects an executable speaking `attn-app-server-v2`, supplied by
`ATTN_SAMPLE_SERVER_EXECUTABLE` or `sample-server` on PATH. Set `SAMPLE_SERVER_HOME` in the
daemon's environment to select an existing server home. The default is
`~/.sample-server`. The isolated stack check supplies the fake app-server,
packs the SDK, and installs the example outside the checkout:

```bash
scripts/verify-plugin-server.sh
```

The server declaration in the sample is:

```ts
server: {
  protocol: "attn-app-server-v2",
  executable: "sample-server",
  executableEnv: "ATTN_SAMPLE_SERVER_EXECUTABLE",
  serveArgs: ["serve"],
  homeEnv: "SAMPLE_SERVER_HOME",
  defaultHome: ".sample-server",
  socketName: "sample-server.sock",
  initialize: { server: "sample-server" },
  callerConversationEnv: "SAMPLE_SERVER_ROOT_CONVERSATION",
  onClose: "leave-running",
  recovery: "numbered-completions",
  abilities: ["targets", "watch"],
}
```

`protocol` selects Attn's RPC contract, and `attn-app-server-v2` is the only
one. `initialize.server`, when set, checks the server's identity. A server
that answers `initialize` with another identity or protocol refuses the
operation before a view starts. Use `onClose: "interrupt"` to interrupt a
conversation when its last owning session closes, or `"leave-running"` to
leave the server's run going. `recovery` is `"last-run"` or
`"numbered-completions"`.

`abilities` lists what the server can do beyond the required calls. Attn reads
them from the declaration, before any server runs, and never sends a request
for an ability the declaration leaves out. An ability Attn does not know is
ignored.

| Ability | The server answers | Attn then |
|---|---|---|
| `watch` | `conversation/watch` and `conversation/entries` | follows each open session's conversation, as the watching binding below describes. Without it, a server session has no transcript, last reply, activity line, title from its prompt, usage, recent files or pull requests from its tools |
| `targets` | `targets/list` | offers execution targets at launch |
| `compact` | `conversation/compact` | compacts the conversation when an agent closes with `--compact` or stands by |
| `steer` | `steer: true` on `conversation/submit` | sends input into the run in flight at its next step instead of after the run, and passes on another agent's `attn agent msg --steer` |
| `interrupt` | `conversation/interrupt` that stops the run in flight at once, then a `conversation/submit` that starts the next run | passes on another agent's `attn agent msg --interrupt`: it interrupts the run, submits the message without `steer`, then submits again the inputs the interrupt withdrew. Without it, Attn holds the message until the run ends |
| `question_safe` | input while a call waits on an approval or a question | delivers input during that wait instead of holding it until the wait ends. Another agent's interrupt still waits for the wait to end |

The executable, optional executable override, home variable and default home
resolve in the daemon's machine and login environment. `socketName` is a filename inside that home. `callerConversationEnv` names the
variable the server sets on its shell tools to the root conversation of the
caller's tree (the user-started conversation, or the worker itself for a worker
and its subagents), including tools of a server started outside Attn. Its name
must end in `_ROOT_CONVERSATION`, with a nonempty prefix. Without terminal or
Codex identity, the Go and Rust CLIs scan their own environment for that suffix.
They send the variable name and native conversation ID. The daemon matches both
to the owning session, adopting a worker under an open Attn session into its own
session first if needed, and keeps that session's profile. If more than one
nonempty root variable exists, commands needing a caller refuse with the
conflicting names; keep only the current server's root variable.

`executable`, when supplied, is the explicit per-session executable selection.
Use it before the plugin's environment override or default executable.

`spawn` and `resume` receive `conversation_id`, the exact native conversation
that the view must open, and optional `execution_target`. Keep that ID on every
view. A target-capable plugin forwards the target on both launch and resume.
An invalid target must fail without creating a replacement conversation or
falling back to another target.

A server binding supplies delivery, models and state, and its abilities
supply compaction and input during a question. Leave out `deliver`, `compact`,
`models`, `stateReporting`, `attributedInput` and `safeDuringQuestion`.
The terminal-driver reports described above do not apply to server views.
A server view has no active terminal driver run to recover. Attn owns the
session and view associations, and the server owns its native conversation.

A plugin restart keeps the established binding and direct delivery usable.
After a daemon restart, Attn retains ownership and waits for that plugin's
registration before reconnecting. Registration and view recovery can happen
in either order. Missing bindings remain unavailable, and input never falls
back to PTY. Uninstall refuses an owner with any open server session, including
one with no view. Close those sessions first. A different plugin cannot take
the harness ID. An open native conversation has one server-harness and profile
owner, so another harness ID cannot open it while that session stays open. To change a registered server declaration, restart the daemon
and register the changed declaration from the same owner.

### Shared servers and attn's tools

With `agentTools: true`, also set `callerConversationHeader` in the server
declaration, such as `"Sample-Server-Root-Conversation"`. While the user has
attn's tools on, attn registers its MCP server with your server, with
`mcp/register` described below, and `spawn` and `resume` get
`agent_tools: {name, description}` once your server has confirmed the
registration. A server that refuses `mcp/register` or does not answer it gets
no `agent_tools` for its views, and attn's log says why. The view starts its
conversation with that name and description and never sees a secret.

Several harness IDs can share one server. They share one registration, which
attn makes when it connects, or when a harness ID that declares the header
launches on a connection that has none. The registration lasts while one such
harness ID remains: uninstalling one leaves the tools working for the others.
attn ends it when the last one is uninstalled or the user turns the tools off.

Your server sends the registered headers on every request to attn's MCP
server, and adds the declared header with the root conversation of the caller's
tree: the conversation the user started, or the worker itself for a worker and
any subagent running under that worker. One registration covers every
conversation of the server, and attn acts as the session that holds the named
conversation. If the header names a worker under an attn session that attn has
not adopted yet, attn adopts that worker into its own session first and runs
the call as it. Any other conversation no session holds, such as a fork nobody
opened in attn yet or a conversation started outside attn, is refused with a
message that names it. Once a fork opens in attn, it is its own session, and
its calls act as that session. `examples/plugins/sample-server` passes the name
and description to its view.

### Shared server RPC contract

The server uses the existing line-delimited Effect RPC framing. A request is
`{ jsonrpc: "2.0", id, method, params }`. A unary response has the same `id`
and `result` or `error`. A streamed response has `chunk: true` and a `result`
array. The client acknowledges each chunk with `@effect/rpc/Ack` and
`{ requestId }`.

The required calls are `initialize`, `status/watch`, `conversation/list`,
`conversation/submit`, `conversation/interrupt` and `models/list`:

- `initialize {client, version, protocol: 2}` answers `{protocol: 2}`, and
  `server` when the declaration checks the identity. The answer carries the
  version only: abilities come from the declaration. Attn refuses a server
  that answers protocol 1 with a message that names the server process to
  stop. Attn never restarts a server that is running, so the user stops it and
  Attn starts a new one when it next needs one.
- `conversation/submit {conversation, requestId, author: {id, name}, text,
  steer?: true}`. `steer` comes only to a server that declares `steer`.
- `conversation/interrupt {conversation, author: {id, name}}`, sent when the
  last session holding a conversation closes and `onClose` is `"interrupt"`,
  and, to a server that declares `interrupt`, before the submit that carries
  another agent's interrupt. `onClose` does not declare `interrupt`. The
  server may answer `{withdrawn: [{requestId, author, text}]}` with the inputs
  waiting in the conversation's queue that it withdrew. After an agent's
  interrupt, Attn submits each again behind the message, with its `author` and
  without `steer`, so they run after it. When the server stops the run but refuses
  the message, Attn shows the run's interrupted end and keeps the message
  queued.

A server that declares `compact` answers
`conversation/compact {conversation, author: {id, name}}` once the compaction
has ended, with `outcome` `compacted`, `nothing_to_compact`, `failed` with a
`message`, or `stopped`. A server that declares `targets` answers
`targets/list {cwd}` with `[{id, name, available, detail}]`. A binding
with `caller_conversation_header` supports `mcp/register {name, url, headers}`,
a stream whose first item `{name}` confirms the registration. The registration
lasts while the stream does: attn ends it with `@effect/rpc/Interrupt`, and
closing the connection ends it too. `conversation/start` then takes
`mcpServers: [{name, description}]`.

`conversation/submit` takes durable custody before replying. Repeating its
`requestId` must return the original admission without submitting twice.
The `author` is the user's name, as both `id` and `name`, for user input, and
`{id: "attn", name: "attn"}` for notices.
`models/list` returns providers with `provider` and `models`, where each model
has `id`, `name`, `description` and `efforts`. Attn lists each model as
`provider/id`, with `id` as its `family`, and passes that name as `model` when
it launches a view.

`status/watch` starts with `{_tag: "trees", trees: [...]}` for busy
conversation trees (each user-started conversation and each worker is its own
tree), then streams `{conversation, status, ended?}` changes.
`status` is `working`, `waiting` for approval, or `idle`.
`ended` has `outcome` (`answered`, `interrupted`, or a failure), optional
`message`, `said`, and, with numbered recovery, `completion`.
`conversation/list` returns `[{id, title?, ended?, fork?, lineage?}]`. `ended`
recovers the last run. `fork: {of, at}` names the conversation a fork came from
and the last entry it inherits. `lineage: {parent, root, title, task?}` places
a child conversation under the conversation that started it and the root of its
tree. A conversation with `lineage.task` (such as `/root/explore`) is a worker:
its `lineage.root` is its own ID, it starts a tree of its own, takes direct
input, and gets its own Attn session titled `lineage.title` whenever its
ancestor chain reaches an open Attn session (adopting any unheld parent workers
first). Attn opens a visible tile for the adopted worker with `driver.resume`
(or `driver.spawn` carrying `conversation_id` when the driver omits `resume`).
A worker whose chain reaches no Attn session stays outside Attn. A closed worker
session resumes when its worker runs again or calls Attn while an ancestor
session is open. On an outpost, the outpost asks the home to adopt the worker
with the outpost as its host and retries when the bridge reconnects. A child with no `task` is a
subagent: its `lineage.root` is its nearest worker or user-started root, it
stays in that root's tree and takes input only through its parent.
Numbered recovery accepts `status/watch {after: {conversation: completion}}`
and replays newer idle completions. Completion numbers ascend per conversation.

A watching binding, one that declares `watch`, also supports
`conversation/watch` and `conversation/entries`. Attn then judges resume from `conversation/list`
instead of `driver.resume_available`: a conversation attn has seen entries of
and the list of a running server no longer holds cannot be resumed.

- `conversation/watch {conversation, window: {min, max, from?}}` streams
  `{_tag: "view", view: {entries, before?, docs}}`, then
  `{_tag: "change", ops}`. An op is `["r", value]`, which replaces the whole
  view, `["s", path, value]`, `["d", path]`, `["a", path, text]`, which appends
  text, `["t", path, length]`, which truncates, `["p", path, at, deleteCount,
  items]`, which splices an array, or `["m", path, order]`, which reorders one.
  New entries are spliced onto the end of `["entries"]`. `before` names the
  entry just before the window, so attn reads any gap with
  `conversation/entries`. With `from`,
  the view starts at that entry when the server still holds it. Past `max`
  entries, the window drops its oldest down to `min`.
- `conversation/entries {conversation, from?, to?, order, limit, cursor?}`
  returns `{entries, next?}`; `next` is the `cursor` of the next page.
- Attn asks for at most 8 entries a call and a window of 4 to 8, because it
  closes the connection on a reply line over 64 MiB.
- An entry is `{id, kind, model}`. Ids ascend across the server, and attn
  remembers the last one it saw. `model` holds messages with a `role` of
  `user`, `assistant` or `toolResult`, a `timestamp` in milliseconds and
  `content`, a string or blocks of `{type: "text", text}` and
  `{type: "toolCall", id, name, arguments}`. A tool result names its
  `toolCallId` and `toolName`, and `isError` when the call failed. attn reads
  the kinds `sigma.user`, `pi.assistant` and `pi.tool-result`. Entries written
  before a session launched are history: they title a resumed session and
  count toward nothing else.
- `docs["pi.usage"].models` maps `provider/model` to
  `{input, output, cacheRead, cacheWrite, cost: {total}}`, the conversation's
  totals so far. `docs["pi.live"].compactions` lists the compactions running,
  each with its `trigger`.
- Attn finds edited markdown files in `Edit`, `Write`, `MultiEdit`,
  `NotebookEdit`, `edit` and `write` calls, by `file_path`, `notebook_path` or
  `path`, and in `apply_patch` calls. It finds a pull request in a `bash`,
  `shell` or `exec_command` call that runs `gh pr create`, by the pull request
  URL in its output.
- Attn watches each subagent under a session's conversation (whether the
  session holds a user-started conversation or a worker) the same way, and
  counts its files, pull requests and `pi.usage` toward that session. A worker
  has its own session, which counts its own `pi.usage`, files and pull requests
  from zero (skipping any entries inherited up to `fork.at` for a forked
  worker) and never toward the session that started it.

## The wire, for any language

Attn starts an installed plugin with these environment variables:
`ATTN_SOCKET_PATH`, a Unix socket path; `ATTN_PLUGIN_NAME`, the manifest's
name; `ATTN_PLUGIN_GENERATION`, a token to send back in `hello`; and
`ATTN_PLUGIN_DATA_ROOT`, a folder that is yours to write. A plugin built
without the SDK uses `kind = "executable"` and `path` in `attn-plugin.toml`
instead of the Bun `entrypoint`.

The connection carries JSON-RPC 2.0, one message per line, both ways at the
same time. A message with a `method` is a request, and a message with only an
`id` answers one. Attn can send a request while yours is still waiting, so
route by shape.

Plugin to Attn, once per connection, in this order:

```json
{"jsonrpc":"2.0","id":1,"method":"hello","params":{"name":"my-harness","version":"0.1.0","attn_api_version":7,"generation":1}}
{"jsonrpc":"2.0","id":2,"method":"driver.register","params":{"agent":"my-agent","display_name":"My Agent","capabilities":{"resume":true,"message_delivery":true,"attributed_input":true}}}
```

`hello` returns `{"ok":true,"active_runs":[{"session_id":"…","run_id":"…","metadata":…, "seq":7}]}`.
Restore those runs before you send `driver.register`. Attn can send driver
requests as soon as it processes the registration, before it answers the call.
`driver.register` also returns the active runs for older clients. An older
daemon omits `active_runs` from `hello`; on that daemon, defer input while you
recover the runs from `driver.register`, then register again to resume delivery.
`session_id` is the terminal ID throughout the plugin wire. It is not the ID of
an Attn session or of a native conversation. A capability name Attn does not
know, or one that needs `message_delivery` without it, is refused.

`skillDirs` is `skill_dirs` on the wire, a list of objects with
`default_home` and optional `home_env`. `logo` is `{"view": 24, "body": "…"}`.

A shared-server registration adds `capabilities: {"server_binding": true}` and
`server` to `driver.register`. Its keys are `protocol`, `executable`, optional
`executable_env`, `serve_args`, `home_env`, `default_home`, `socket_name`,
`initialize`, `caller_conversation_env`, `on_close`, `recovery`, and optional
`caller_conversation_header` and `abilities`. Their values
match the SDK declaration above. The SDK translates camel case option names to
these snake case wire names. `server_binding` and `message_delivery` are
mutually exclusive. The server supplies models and state without
`model_discovery` or `state_reporting`.

Attn to plugin, each answered with a result or a JSON-RPC error:

| Method | Params | Result |
|---|---|---|
| `attn.health` | `now` | `{"ok":true}` or `{"ok":false,"message":"…"}`, every 15 seconds |
| `driver.spawn`, `driver.resume` | `agent`, `session_id`, `run_id`, `cwd`, and when set `label`, `executable`, `yolo`, `model`, `effort`, `initial_prompt`, `metadata`, `resume_session_id`, `instructions`, `agent_tools`, and for server views `conversation_id`, `execution_target` | `{"argv":["…"],"env":{…},"cwd":"…"}` |
| `driver.resume_available` | `conversations`: `agent`, `cwd`, `resume_session_id` | `{"availability":[` the same fields plus `available` and `reason` `]}` |
| `driver.models` | `agent` | `{"models":[…],"detail":"…"}` |
| `driver.deliver_message` | `session_id`, `run_id`, `input_id`, `text`, and with `attributed_input` `voice` and `conversation_id` | `{"ok":true}`; `{"ok":false,"status":"deferred"}`; `{"ok":false,"status":"rejected","reason":"…"}`; `{"ok":false,"status":"target_ended"}` |
| `driver.compact` | `session_id`, `run_id`, `input_id`, `conversation_id`, empty `text`, and `voice:"attn"` with `attributed_input` | `{"ok":true}`, then a `session.report_compaction` |
| `driver.session_closed` | `session_id`, `run_id`, `reason`, and when known `exit_code`, `signal` | `{"ok":true}` |

A method for a capability you did not declare never arrives.

Each `driver.models` entry has `harness` (your `agent`) and `id`, and may have
`provider`, `family`, `name`, `description`, `effort_support`, `effort_levels`,
`access`, `one_shot` and `detail`. The `id` is the exact name Attn sends back
as `model` in `driver.spawn`, `driver.resume` and background calls. Keep it
unique among your models: when a list repeats one, Attn keeps the first entry.
Attn never adds the provider to an `id` or splits one. `provider` only groups
models in the pickers, so when several providers offer one model, give each
its own `id`, such as `openrouter/deepseek-v4` and `fireworks/deepseek-v4`.
`family` is the name Attn's built-in intelligence tiers recognize, such as
`claude-opus-5` for `anthropic/claude-opus-5`. Without it, the tiers look at
the `id`.

Plugin to Attn, for a run you launched:

| Method | Params |
|---|---|
| `session.report_state` | `session_id`, `run_id`, `seq`, `state` (`unknown`, `working`, `pending_approval`, `waiting_input`, `idle`) and optionally `only_if_unknown` |
| `session.report_stop` | `session_id`, `run_id`, `seq`, `verdict` (`idle`, `waiting_input`, `unknown`) |
| `session.report_metadata` | `session_id`, `run_id`, `seq`, `metadata` (any JSON), optionally `resume_session_id` |
| `session.report_transcript_path` | `session_id`, `run_id`, `path` (absolute) |
| `session.report_title` | `session_id`, `run_id`, `title` |
| `session.report_pull_request` | `session_id`, `run_id`, `url` |
| `session.report_compaction` | `session_id`, `run_id`, `input_id`, `conversation_id`, `outcome` (`completed`, `busy`, `failed`), optionally `reason` |
| `attn.classify_stop` | `session_id`, `run_id`, `assistant_text`; returns `{"verdict":"…"}` |

`seq` is a number above zero that grows with each report of one run. Start at
1 for a new run and continue from `active_runs[].seq` for a run you got back
from `hello`. Attn ignores a report whose `seq` is not newer and
refuses a report from a plugin that does not own the run.
