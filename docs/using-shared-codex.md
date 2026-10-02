# Using shared Codex

Shared Codex lets several terminals show the same agent. Native `/agents` switches
the agent in the current terminal, and `/new` creates another agent there. Attn
keeps each agent's history, inbox, name, attention and costs in its own session.

## Turn it on

In Settings → Experimental, enable **Shared Codex (experimental)** before launching a new Codex
agent. It starts off. Attn uses your configured stock Codex executable and ordinary
Codex home, including its account, model, permissions, guidance and extensions.
Choose the project or worktree through Attn's usual launch flow.

Disabling the setting affects future external launches. Existing shared agents
stay shared when attached, reopened or restarted. Native `/new` inside one of
their terminals also stays shared. Existing legacy Codex sessions keep their
original mode; other harnesses and headless tasks keep their usual behavior.

Codex's own startup, update and folder-trust prompts still apply. Complete those
prompts before expecting the terminal to display a resolved agent.

## Switch and open another view

Use native `/agents` to select an agent. The pane header follows the committed
native selection while the terminal and its drafts survive. An agent can keep
working after you switch away from its last view. The queue still lists that
agent once; selecting it opens a view when needed.

Open **Sessions and worktrees** (Command-Shift-L on macOS), find a live shared
agent, and choose **Open another view**. Both views show the same conversation.
An approval visible in both views is one approval; answer it in either view.
Names and costs belong to the agent, so an extra view doesn't add another cost
source. Native `/rename` and Attn's rename action update the same name.

Native `/new` creates a separate agent without replacing the terminal. Its
directory comes from Codex's returned metadata. Codex may use that terminal's
original launch directory even after you switch to an agent in another project.
Use Attn to launch in a different directory; native `/cd` is unavailable on this
connection.

## Send feedback and read the inbox

Attn's annotations and agent messages address the intended agent even when it
is hidden. An annotation editor keeps the agent it opened for after you switch
the terminal. API input leaves the native composer's unsent draft intact.

A peer message produces the usual generic inbox notification. The recipient
reads it with `attn agent inbox` from its tool environment. There is one inbox
per agent, shared by all of its views. A notification doesn't count as your
answer to an agent in the attention queue.

## Close, reopen and inspect

Close the focused pane (Command-W on macOS) to remove that view. If another view
shows the same agent, it continues there. Closing its last view explicitly
interrupts and archives that agent. Other agents continue, including agents
that have no view because you switched away.

If another view has unresolved identity, Attn refuses the last-view close. Resolve
or close that view first, then retry; the error names the affected view.

In **Sessions and worktrees**, choose **Reopen** on the closed agent to restore
its original Attn identity and native conversation, with its saved name/history.
Quitting the app or restarting the daemon preserves agents and terminals; it
doesn't count as explicitly closing their views.

The CLI reads the same ledger as the app:

```sh
attn session list --all --json
attn session show <session-id>
attn session reopen <session-id>
attn session rename "New name" --session <session-id>
```

For a named instance, select it with `eval "$(attn instance-env <name>)"` first.
The ledger's usage inspector shows the same owner totals as live headers.
Unknown prices and incomplete measurements remain visible.

## Current limits

- A never-used native thread can reject a second attachment with `no rollout
  found`. The failed view can be closed without harming the original agent.
- A native server crash interrupts active work. Saved identities and history
  can recover, but uncertain input is never automatically resent. Usage after
  a crash is marked incomplete.
- Final close reconciles transcript records available at that point. Native
  archive isn't a demonstrated acknowledgement that every final record reached
  disk.
- If both the native creation reply and its SessionStart hook are lost, Attn
  can't reliably recover that creation's original owner reservation. This
  limitation remains unresolved.
- Independent native agents are supported. Native subagent navigation and
  presentation are outside this rollout; child usage keeps its existing root
  attribution.

See [the runtime contracts](codex-shared.md) for implementation details and
verification boundaries.
