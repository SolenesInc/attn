# Nisse harness behavior

How Nisse behaves where attn meets it. The rules for adding a claim are in
[Harness behavior](../harnesses.md).

Read in the source at victorarias/nisse 062ef0b.

- One server per Nisse home holds every conversation. Clients reach the server
  over `NISSE_HOME/nisse.sock`. `nisse serve` refuses a home another server
  already serves (`packages/nisse/src/main.ts`).
- The TUI and `nisse exec` start a detached server when none answers. A server
  exits 30 seconds after its last client leaves and its last run settles.
- `nisse --conversation <id>` opens that conversation, and starts a
  conversation with that id when none exists. A prompt after the flags is sent
  once the conversation opens.
- `conversation/submit` is idempotent by its `requestId`: a retried submit is
  one input.
- Every `Bash` call, and Claude Code's process, runs with
  `NISSE_ROOT_CONVERSATION` set to the id of the conversation people started.
- `conversation/start` takes `instructions`, kept with the conversation and
  not given to its subagents: OpenAI is told them after the `AGENTS.md` files,
  and Claude Code appends them to its own system prompt.
- `models/list` answers each provider's models, with the efforts each takes.
- `status/watch` opens with the busy conversation trees and then sends one
  status per change of a tree (working, waiting on an approval, idle), with
  how its root's run ended. Subagents are part of their root's tree.
- `conversation/list` carries `ended` on each conversation that has run: how
  its last run ended, the same as `status/watch` says.
