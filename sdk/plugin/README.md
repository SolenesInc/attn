# `@victorarias/attn-plugin`

TypeScript SDK for attn plugins. A plugin adds a harness to attn, hooks worktree
creation, or both, over the plugin socket attn gives each installed plugin.

The SDK uses plugin API 7. Declare `attn_api_version = 7` in the plugin manifest
and run it with a daemon supporting that API. Older API declarations are
rejected at manifest or handshake validation. Driver capabilities
`attributed_input`, `safe_during_question` and `compaction` need an attn daemon
built from source revision `13595cac6922bb7b2d3d89d62e4879be500f7aa3` or
later. On an older daemon `connect()` fails and names that revision.

```bash
bun add @victorarias/attn-plugin
```

`attn guide writing-harness-plugins` covers installing a plugin, how attn runs
a harness, and the JSON-RPC wire for plugins written in other languages. The
repository's `examples/plugins/sample-harness` is a complete installable
harness.

## Add a harness

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

`client.driver` is the one registration call; a client registers one harness.
The ID is lowercase letters, numbers, hyphens and underscores. The harness shows
in attn's picker once `connect()` succeeds.

The handlers you pass decide what attn offers. The SDK registers exactly the
capabilities the handlers and promises imply, and attn never calls a handler
you left out.

| Option | Capability | Notes |
|---|---|---|
| `spawn(params, run)` | | Required. Returns `{ argv, env?, cwd? }`. |
| `resume(params, run)` | `resume` | Same shape as `spawn`; `params.resume_session_id` or `params.metadata` names the conversation. |
| `resumeAvailable(conversations)` | `resume_availability` | Needs `resume`. Returns one `{ ..., available, reason? }` per conversation. |
| `models()` | `model_discovery` | Returns `{ models, detail? }`. |
| `deliver(input, run)` | `message_delivery` | Direct input. Return `accepted()` only after taking custody. |
| `compact(input, run)` | `compaction` | Needs `deliver`. Return `accepted()`, then `run.compaction(...)`. |
| `attributedInput` | `attributed_input` | Needs `deliver`. `input.voice` is `"user"` or `"attn"`; `input.conversation_id` names the target. |
| `safeDuringQuestion` | `safe_during_question` | Needs `deliver`. Promise that delivered text cannot answer a permission question. |
| `yolo`, `initialPrompt`, `modelPin`, `effortPin`, `launchInstructions` | same, in snake case | Promise that `spawn` and `resume` honor the matching parameter. |
| `stateReporting` | `state_reporting` | The plugin reports state through `run.state`; attn stops inferring state from the terminal, so report every start and end of a turn. |
| `pullRequestReporting` | `pull_request_reporting` | With `launchInstructions`, attn leaves out the instruction that tells the agent to report its own pull requests. |
| `sessionClosed(params)` | | Notification that a run ended. |
| `recover(runs)` | | Called on connection with the runs attn still holds, before the driver handles requests. |
| `health()` | | Answers `attn.health` with `{ ok, message? }`. Defaults to `{ ok: true }`. |

`deliver` and `compact` return `accepted()`, `deferred(reason?)` for a temporary
refusal, `rejected(reason)` for a permanent one, or `targetEnded()` when the
conversation changed. A repeated `input_id` must get the same answer without a
second submission; the harness owns that receipt.

`run` reports what a launch does. Each call carries the run's next sequence
number, and `recover` hands back runs with their sequence continued.

```ts
await run.reportMetadata({ conversationId }, { resumeSessionId: conversationId });
await run.state("working");
const verdict = await run.classifyStop(lastAssistantText);
await run.stop(verdict);
await run.transcriptPath("/abs/path/transcript.jsonl");
await run.pullRequest("https://github.com/owner/repo/pull/1");
await run.compaction({ inputId, conversationId, outcome: "completed" });
```

Report the harness's own conversation ID with `resumeSessionId` first:
attributed input and compaction wait for it. Reports sent while `spawn` or
`resume` is still running are queued, except `run.pullRequest`, which attn
refuses until the launch is up.

Attn supervises installed plugin processes. A clean exit, crash, or connection
loss longer than five seconds triggers a restart with bounded backoff. Rebuild
in-memory state from durable sources in `recover` before it returns. A harness
started by a driver keeps running while its plugin restarts, and attn holds
direct input until recovery finishes.

## Bind a shared app-server

With SDK 0.3.0, `client.driver({ id, spawn, resume, server })` can bind a harness
speaking `attn-app-server-v1`. The daemon handles direct input, status and model
discovery. The plugin prepares native terminal views with the host's
`conversation_id` and optional `execution_target`. A server view does not use
`DriverRun` reports, `sessionClosed` or `recover`; the server owns the conversation.

`server` cannot be combined with `deliver`, `compact`, `models`,
`stateReporting`, `attributedInput` or `safeDuringQuestion`. The SDK negotiates
`server_binding`; an older daemon refuses that capability clearly. This addition
does not change API 7 for existing plugins.

See [Writing a harness plugin](../../docs/guide/reference/writing-harness-plugins.md#bind-a-shared-app-server)
for the declaration, RPC contract and install recipe.
`examples/plugins/sample-server` prepares views speaking the fake server's
Sigma-compatible protocol and runs
against the repository's fake app-server in the stack verification.

A plugin disconnect leaves an established server binding usable. After a daemon
restart, existing sessions wait for the owning plugin to register again. Input
never falls back to the terminal, including for an open session with no view.
Attn refuses uninstall while the plugin owns an open server session.

## Hook worktree creation

```ts
import { AttnPluginClient, decline, handled } from "@victorarias/attn-plugin";

const client = new AttnPluginClient({ version: "0.1.0" });

client.handle<"worktree.create">("worktree.create", async (params) => {
  if (!params.main_repo.includes("example")) {
    return decline();
  }
  return handled({ path: "/tmp/example-worktree", branch: params.branch });
});

client.handle<"worktree.after_create">("worktree.after_create", async (params) => {
  await bootstrapRepo(params.path);
});

await client.connect();
```

`client.handle(...)` is the declaration point for worktree surfaces: `connect()`
puts every registered surface in the `hello` handshake. A plugin can use
`client.handle` and `client.driver` together; they register independently.
`examples/plugins/worktree-deps-hook` is a complete worktree plugin.

## Connection

Managed plugins get `ATTN_SOCKET_PATH` and `ATTN_PLUGIN_NAME` from attn, plus an
`ATTN_PLUGIN_GENERATION` token that the SDK returns in the handshake. Manually
launched plugins can pass `socketPath` or `name` explicitly and use generation 1.

The SDK answers `attn.health` itself.

The connection is full duplex, and attn does not wait its turn. A request from
attn, such as a healthcheck or a `driver.session_closed` for a session whose
process just exited, can arrive while one of the plugin's own requests is still
waiting for its response. The SDK routes by shape: a message carrying a method
goes to its handler, a message carrying only an id resolves the pending request.
Plugin code that talks to the socket directly must do the same.
