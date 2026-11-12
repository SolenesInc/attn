# Sample harness plugin

A complete harness plugin built on `@victorarias/attn-plugin`. It adds an agent
called `sample`, a local echo agent that prints what it receives. It needs no
account, network or private dependency.

```bash
attn plugin install --path examples/plugins/sample-harness
```

Open the new-agent picker with Cmd+N and choose `sample`. Type a line in the
terminal, or send the agent an annotation or an `attn agent msg`: each one
appears as `[user]`, `[attn]` or `[you]` followed by `echo: …`.

To make your own harness from it, copy the folder, change `harnessId` in
`src/index.ts` to your ID, and replace `src/harness.ts` with your agent. The
plugin and `attn guide writing-harness-plugins` explain the rest.

## What it shows

- `spawn` and `resume` return the command attn runs in the terminal. The
  plugin reports the conversation ID first, so attributed input can be
  delivered.
- `deliver` hands direct input to the running harness over a socket named after
  the run. The harness remembers each `input_id`, so a retry after a lost reply
  is one submission. A plugin restart loses nothing, because the plugin keeps no
  state.
- It leaves out `compact` and `models`, so attn offers neither for this harness.

`src/harness.ts` is the stand-in for a native agent. A real plugin launches the
harness's own CLI and talks to its own input API instead.
