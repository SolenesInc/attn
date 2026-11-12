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
registration with `unsupported driver capability "attributed_input"` or one of
the other two capabilities listed below, and the SDK turns that refusal into
the revision to upgrade to. A terminal-only plugin that declares none of the
three runs on any API 7 daemon.

## From nothing to a running harness

The checkout's `examples/plugins/sample-harness` is a complete plugin. It runs
a local echo agent, takes direct input, and has no outside dependency. To make
your own from it:

```bash
cp -R examples/plugins/sample-harness my-harness && cd my-harness
# In attn-plugin.toml, change name to your plugin's name, such as "my-harness".
# In src/index.ts, change harnessId to your harness's ID, such as "my-agent".
attn plugin install --path .
```

The plugin's name and the harness's ID are two things: the name is the
installed folder and what `attn plugin remove` takes, and the ID is what the
picker shows. Install runs `bun install` in a copy, so `package.json` has to
name a version of `@victorarias/attn-plugin` that exists. Use
`attn plugin link --path .` while you develop, so Attn runs your directory in
place. To pick up a change, close the harness's sessions, run
`attn plugin remove my-harness` and install again: Attn refuses to remove a
plugin that still owns a running session.

Open the picker with Cmd+N. Your ID is a choice in the "Session agent" group.
`attn delegate --agent my-agent` starts it from a shell, and
`attn settings set new_session_agent my-agent` makes it the picker's default.
Attn runs the argv your `spawn` handler returned, in the terminal, in the
folder you picked.

The ID is yours to choose: lowercase letters, numbers, hyphens and underscores,
starting with a letter or number. The built-in names `claude`, `codex`, `shell`,
`nisse` and `sigma` are taken. Two plugins cannot register one ID.

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
| `resume` | `resume` | reopens a closed session and `attn agent reload` |
| `resumeAvailable` | `resume_availability` | checks that a conversation still exists before offering a resume |
| `models` | `model_discovery` | lists the harness's models in Settings |
| `deliver` | `message_delivery` | hands annotations, messages and compaction notices to the plugin instead of typing them |
| `compact` | `compaction` | compacts the conversation before a session closes itself with `--compact` |
| `attributedInput: true` | `attributed_input` | tells `deliver` who is speaking and which conversation it is for |
| `safeDuringQuestion: true` | `safe_during_question` | delivers text while a permission question is open, because your `deliver` cannot answer it |
| `yolo`, `initialPrompt`, `modelPin`, `effortPin`, `launchInstructions` | the same names, in snake case | passes `yolo`, `initial_prompt` (needed to delegate to the harness), `model`, `effort` and `instructions` to `spawn` and `resume` |
| `stateReporting` | `state_reporting` | takes the launch's state only from `run.state` and stops inferring it from the terminal, so report every start and end of a turn |
| `pullRequestReporting` | `pull_request_reporting` | with `launchInstructions`, leaves out the instruction that tells the agent to report its own pull requests |

`attributedInput`, `safeDuringQuestion` and `compact` need `deliver`. The SDK
refuses an invalid declaration when you call `client.driver`.

`client.handle(...)` for worktree hooks stays separate. A plugin can declare
both, and a driver adds nothing to the `hello` surfaces.

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

`voice` is `"user"` for an annotation, a typed conversation and a ring with
only the user's words, and `"attn"` for everything else, including a ring that
carries a notice. A harness that tells its model who is speaking uses it.

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
{"jsonrpc":"2.0","id":2,"method":"driver.register","params":{"agent":"my-agent","capabilities":{"resume":true,"message_delivery":true,"attributed_input":true}}}
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

Attn to plugin, each answered with a result or a JSON-RPC error:

| Method | Params | Result |
|---|---|---|
| `attn.health` | `now` | `{"ok":true}` or `{"ok":false,"message":"…"}`, every 15 seconds |
| `driver.spawn`, `driver.resume` | `agent`, `session_id`, `run_id`, `cwd`, and when set `label`, `yolo`, `model`, `effort`, `initial_prompt`, `metadata`, `resume_session_id`, `instructions` | `{"argv":["…"],"env":{…},"cwd":"…"}` |
| `driver.resume_available` | `conversations`: `agent`, `cwd`, `resume_session_id` | `{"availability":[` the same fields plus `available` and `reason` `]}` |
| `driver.models` | `agent` | `{"models":[…],"detail":"…"}` |
| `driver.deliver_message` | `session_id`, `run_id`, `input_id`, `text`, and with `attributed_input` `voice` and `conversation_id` | `{"ok":true}`; `{"ok":false,"status":"deferred"}`; `{"ok":false,"status":"rejected","reason":"…"}`; `{"ok":false,"status":"target_ended"}` |
| `driver.compact` | `session_id`, `run_id`, `input_id`, `conversation_id`, empty `text`, and `voice:"attn"` with `attributed_input` | `{"ok":true}`, then a `session.report_compaction` |
| `driver.session_closed` | `session_id`, `run_id`, `reason`, and when known `exit_code`, `signal` | `{"ok":true}` |

A method for a capability you did not declare never arrives.

Plugin to Attn, for a run you launched:

| Method | Params |
|---|---|
| `session.report_state` | `session_id`, `run_id`, `seq`, `state` (`unknown`, `working`, `pending_approval`, `waiting_input`, `idle`) and optionally `only_if_unknown` |
| `session.report_stop` | `session_id`, `run_id`, `seq`, `verdict` (`idle`, `waiting_input`, `unknown`) |
| `session.report_metadata` | `session_id`, `run_id`, `seq`, `metadata` (any JSON), optionally `resume_session_id` |
| `session.report_transcript_path` | `session_id`, `run_id`, `path` (absolute) |
| `session.report_pull_request` | `session_id`, `run_id`, `url` |
| `session.report_compaction` | `session_id`, `run_id`, `input_id`, `conversation_id`, `outcome` (`completed`, `busy`, `failed`), optionally `reason` |
| `attn.classify_stop` | `session_id`, `run_id`, `assistant_text`; returns `{"verdict":"…"}` |

`seq` is a number above zero that grows with each report of one run. Start at
1 for a new run and continue from `active_runs[].seq` for a run you got back
from `hello`. Attn ignores a report whose `seq` is not newer and
refuses a report from a plugin that does not own the run.
