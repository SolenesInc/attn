# Harness behavior

How each harness behaves where attn meets it: which hooks fire and when, which
conversation id the hooks carry, what the harness's app-server and TUI do, and
how the harness updates itself. Designs that react to a harness depend on these facts.

Every entry was checked against the harness's source or a probe, and names
the version it was checked on. Do not add or rely on a claim from memory:
read the source or probe the binary first, then record it in the file for its
harness.

## Where each harness is described

- [Claude Code](harnesses/claude.md): hooks, conversation ids, idle compaction,
  composer suggestions, the agent view, auto memory, OSC 7501 and mods.
- [Codex](harnesses/codex.md): plain Codex, local memories and updates.
- [Codex app-server and `--remote`](harnesses/codex-app-server.md): the shared
  app-server that shared Codex runs on.
- [Sigma](harnesses/sigma.md): the Sigma server, named Nisse until 2026-11-14.

This file keeps the facts that hold across harnesses.

## attn identities at the harness boundary

`ATTN_TERMINAL_ID` identifies the launched terminal. Older launches carry the
same identity under `ATTN_SESSION_ID`. A terminal can change conversations with
`/clear` or `/resume`. CLI commands address the conversation shown when the
command starts. Hooks apply to the conversation shown when the report arrives.
Under shared Codex, hooks and commands run in the app-server and name their
conversation instead: a hook by the `session_id` on its stdin, a command by
`CODEX_THREAD_ID`. A `/side` conversation has no session of its own: attn sets
`ATTN_TERMINAL_ID` on its shell tools, so its commands act as the session of the
terminal that shows it, and its hooks resolve to no session. Sigma runs
commands in its server, and names the conversation people started by
`SIGMA_ROOT_CONVERSATION`. Native harness conversation ids remain separate text
values.

A session is attn's record of one conversation in one profile. A terminal
shows one session at a time, and a session can show in any number of
terminals. Removing a placement keeps the terminal addressable, including
after daemon restart. Closing a session ends its terminal associations. When a
terminal resumes the conversation of a session whose agent no longer runs, the
session moves into that terminal and the session's old tile goes away.

Showing one session in many terminals needs the session to run over a link
whose server every terminal of the conversation joins, like shared Codex's
app-server. A terminal on a link that resumes the conversation of a live
session shows that session.

An integration without a link starts one process per terminal. Claude Code
lets a second process open a conversation that another process still runs.
The second terminal keeps its own session on the conversation, so two open
sessions hold one conversation. The tiles of every live session on that
conversation warn until one session remains. Plain Codex refuses the second
process.

Plugin driver payloads retain the `session_id` wire spelling for the terminal.
Close notifications, including those completed after restart, use the original
terminal identity.

## The running version in the transcript

Each harness writes the version of the running process into its transcript, so
the version stays right after the binary on disk updates. Probed on the user's
own transcripts on 2026-10-24:

- Claude Code (2.1.302, 2.1.310): every line has a top-level `version`. The
  newest `version` is at most 38.8 KB from the end of the file, across 300
  transcripts with lines up to 1.8 MB.
- Codex (0.162.1): the first line is `session_meta`, with
  `payload.cli_version`. That line is about 23 KB.
