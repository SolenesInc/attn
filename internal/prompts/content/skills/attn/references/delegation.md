# Delegation

Attn delegation starts a separate agent session the user can inspect and steer. Use it when the user or the assigned task authorizes it. A subagent is a native runtime subagent that reports to its caller. Read the requested object this way: "dispatch an agent" means Attn, and "use a subagent" means a native subagent.

The seed holds the work: its outcome, plan, constraints, and verification. Delegation assigns an agent to that work and selects the agent's role, model, effort, and checkout at launch. The agent reads the seed and keeps the plan and progress current. A later handover can assign a different agent to the same seed, with a note that explains the next step and its authorization. The agent does not receive your conversation automatically.

Use `attn delegate --help` for the installed command syntax. Write the brief as `attn seed guide` describes under WRITING A BODY, and read the brief back as that guide says before dispatching.

## Choose the assignment

For new work, pass a brief. Attn creates the seed for the brief. Start the brief with a `#` heading to name the seed and the agent. If you have a stake (the seed a delegation assigned you), the new seed becomes a child of the stake.

```sh
attn delegate --brief-file question.md --role pathfinder --cwd /notes
```

This example assumes `/notes` is outside Git. `--brief "…"` works for short assignments.

For work already in a seed, read it with `attn seed show`, then dispatch with the seed. If the assignment has changed, update the body first. Do not supply a second brief.

```sh
attn seed show s-example
attn delegate --seed s-example --role builder \
  --cwd /repo --reuse-checkout --branch feature-x
```

A brief cannot carry checks. When new work has checks, plant the seed first, with the brief as the body and each check as `--check "<statement>"`. Add `--part-of <stake>` when you have a stake. Then dispatch that seed. Plant prints the id that attn gave each check. To add checks to an existing seed, run `attn seed check add <id> -m "<statement>"` before you dispatch it.

An existing seed may be a leaf or a plot. Dispatch leaves the seed's relationships intact. Replant closed work deliberately before you dispatch it. If the garden is unavailable, delegation fails. There is no seedless fallback.

## Hand work to another agent

Use handover to give the same seed to a fresh agent. Record what comes next, and its authorization, in a short handoff message. The plan stays in the body.

```sh
attn delegate --seed s-example --handover --role orchestrator \
  --cwd /repo --reuse-checkout --branch feature-x \
  -m "The user approved the plan in this seed. Execute it, coordinating implementation and review."
```

Attn saves the note and transfers ownership before it starts the successor. The previous agent keeps running. Handover is the explicit transfer choice, so it has no extra force or confirm flag. Handover does not grant permission beyond the user's authorized task. An ordinary progress note does not invalidate handover. If the tender changes during preparation, or the seed closes, reconsider the request.

For a completed design, recommend an Orchestrator when the plan requires coordinated or reviewed Builder work, or benefits from mixing harnesses or models between the coordinating agent and its Builders. Otherwise recommend a Builder. Follow the planning process's checkpoint: show the proposed plan and handoff, then wait for the user's choice even when their earlier request included execution. Skip the checkpoint only when the user explicitly said to dispatch without reviewing the plan, as that process describes.

## Choose the machine, folder and checkout

Delegation defaults to your session's machine. `--host <name|id>` selects an
outpost, and `--host home` selects the home. An unknown or unreachable host
fails before acceptance. A retry or home restart keeps the accepted machine and
path.

Every delegation, including handover, requires `--cwd`. Attn does not infer it from the source session. Outside Git, the folder is enough. Inside Git, explicitly choose one mode and its branch arguments:

```sh
# Reuse a checkout on its current branch.
attn delegate --seed s-example --role builder \
  --cwd /repo --reuse-checkout --branch feature-x

# Create a new branch and worktree from an explicit base.
attn delegate --brief-file implementation.md --role builder \
  --cwd /repo --new-worktree --branch feature-x --from origin/main

# Create a worktree for an existing local branch.
attn delegate --seed s-example --role builder \
  --cwd /repo --new-worktree --existing-branch feature-x
```

`--cwd` and `--worktree-path` name paths on the target machine. attn expands
and checks them there. On your own machine, a relative cwd starts at your
shell's directory. For another machine, use an absolute path or quote `~`,
as in `--host ai-sandbox --cwd '~/project'`, so the target expands it.

Reuse mode includes an existing linked worktree. Reuse mode verifies the branch without switching it. New-worktree mode creates the worktree on that machine and reports its path. Use `--worktree-path` for a specific destination. The resulting worktree preserves a cwd subdirectory.

New branch mode uses the specified committed base, not dirty changes in the source checkout. An unavailable base fails. Fetch explicitly if needed. For a remote-tracking branch, use new branch mode with `--from remote/branch`. In existing-branch mode, `--existing-branch` names a local branch.

Two conflicts have different recoveries:

- Git already has the branch checked out elsewhere: use the reported folder with explicit reuse, or choose another branch. Attn will not turn a create request into reuse.
- An active Attn agent on that machine uses the selected checkout: add `--allow-worktree-reuse` only when sharing is intended. In a same-checkout handover, the predecessor does not count as an occupant. Any other occupants still require the flag. No further sharing approval step follows the flag.

## Choose the desktop

The new agent opens beside you on your desktop without changing what the user sees. When the user asks for the agent elsewhere, pass `--desktop` with the desktop's shortcut digit, its name as shown (case-insensitive), or its id. The agent opens beside that desktop's active pane. Only desktops of your profile are accepted, and a refusal lists them.

```sh
attn delegate --seed s-example --role builder --cwd /notes --desktop ops
```

To move a terminal later, run `attn terminal move <desktop>` for yourself, or
`attn terminal move <terminal> <desktop>` for a shell or an agent below you in
the delegation chain, including delegates' delegates. attn creates a missing
numbered desktop, such as `5`. Moving keeps the process running and never
changes the profile's current desktop. See [Terminals](terminal.md#move) for targeting and
placement.

## Choose the role and model

Read current configuration after deciding to delegate:

```sh
attn delegate roles
```

`--json` returns the same catalog as structured data. Choose the role that matches the outcome, and a model alternative whose condition fits. If no alternative fits, use the role's default choice. Keep coherent work together. Roles share their instructions across model alternatives. Use the configured fallback when no role fits. With no configured roles or fallback, direct model selection remains available.

One delegation runs one choice. When the task asks for the same work done independently on different models, such as competing design candidates, start one delegation per distinct model among the role's choices, and leave out each choice whose condition rules the task out. Keep the default choice. Its "when no alternative fits" condition is the role's fallback, not an exclusion. When the remaining choices share one model, start one delegation and say so where you report the result.

```sh
attn delegate --brief-file question.md --cwd /notes --role pathfinder
attn delegate --brief-file question.md --cwd /notes \
  --role pathfinder --choice <choice-id>
attn delegate --brief-file task.md --cwd /notes --fallback
```

Use Attn's configured roles and choices by default. Honor an explicit user request for a model or role for that delegation, and do not save it as a preference. When the user asks to change the saved roles themselves, read [delegation-roles.md](delegation-roles.md). If standing instructions in AGENTS.md, skills or other files actually conflict with the configuration, explain the conflict and ask which should govern.

Use the configured Reviewer role only when the user explicitly requests a separate Reviewer delegation. Semantically equivalent wording counts even when the user does not name the role. When the user asks you to review the current work, the current agent does that review. A general requirement that implementation be reviewed remains the Orchestrator's responsibility. A Pathfinder or Orchestrator must not add a Reviewer to a plan proactively and treat later plan approval as authorization for that delegation.

`--agent` chooses the harness. `--model` and `--effort` override the selected values. A model change keeps the role instructions and clears the inherited effort unless you supply an effort explicitly. A harness change clears the inherited model, provider, and effort. Use `default` to select a harness's model or effort default explicitly. `--provider` identifies a plugin model provider where supported. For direct plugin selection, use the model identifier that the plugin supports. Never silently substitute an unavailable model. Resolve ambiguous names before dispatch.

For direct delegation, choose the model explicitly. Available harnesses, models and effort levels depend on configuration, so use the catalog and command help rather than assuming universal levels.

## Follow progress and recover

Successful launch output identifies the seed, the session, the folder and branch, and the operation. A successful launch means the agent launched, not that the agent's work is done. Each new delegation creates an ordinary watch on the delegation's seed and its descendants. For watch and unwatch behavior, use the garden reference. Recovery does not recreate a watch you removed.

Read the work with `attn seed show <seed-id>`. Reach the seed's current or next tender with `attn agent msg <seed-id> "…"`. To speak to the previous session after handover, use that session's identity with the messaging syntax in `attn agent --help`. Follow the reporting reference in the delegated session. Decide when to respond from the completion, progress, and advice requests that arrive. Do not track every edit.

If the CLI loses the response, check the request before you start another delegation:

```sh
attn delegate status <request-id>
```

To retrieve or continue an operation, retry identical input with the same `--request-id`. A changed request needs a new ID. A terminal failed operation stays failed. For an intentional new attempt, use the existing seed and an explicit checkout under a new request ID.

After a home restart, an accepted operation waits for its outpost to reconnect.
The operation's status names that host. Removing the outpost fails the waiting
operation.

Launch failures leave transferred ownership and created worktrees and branches in place. Read the error's resource paths and the current state, then reuse or remove the checkout explicitly. Attn does not restore the old owner or clean up the worktree automatically. Do not take an unknown outcome as evidence that nothing started.

When the delegate's work is complete, you can harvest the delegate's seed
without `--force`, even while the delegate's session is live. Tend, park,
wither and replant follow the same delegation-chain rule. The profile's chief
can also act on another session's seed. The log records the move and whose claim the move ended, and the delegate's watch delivers the seed update. Everyone else
still needs `--force`.

When you no longer need the delegate's session, read the seed and use the closing rules in [converse-and-observe.md](converse-and-observe.md). Closing a session is immediate
and separate from harvesting the session's assignment.
