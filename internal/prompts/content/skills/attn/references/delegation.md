# Delegation

Attn delegation starts a separate agent session the user can inspect and steer. Use it when authorized by the user or the assigned task. A subagent is a native runtime subagent that reports to its caller. Interpret the requested object: "dispatch an agent" means Attn; "use a subagent" means a native subagent.

The seed holds the work: its outcome, plan, constraints, and verification. Delegation assigns an agent to that work, selecting its role, model, effort, and checkout at launch. The agent reads the seed and keeps its plan and progress current. A later handover can assign a different agent to the same seed, with a note explaining the next step and its authorization. The agent does not receive your conversation automatically.

Use `attn delegate --help` for the installed command syntax. Read the complete brief before dispatching: state the outcome, starting context, constraints, authorization and verification. Name parent sections, sibling results and artifacts the agent needs and why. Use `attn seed guide` for seed authoring.

## Choose the assignment

For new work, pass a brief. Attn creates its seed. If you are working on a reporting seed, the new seed becomes its child.

```sh
attn delegate --brief-file question.md --role pathfinder --cwd /notes
```

This example assumes `/notes` is outside Git. `--brief "…"` works for short assignments.

For work already in a seed, read its current body and notes, then dispatch using the seed. Update the body first if the assignment has changed; do not supply a second brief.

```sh
attn seed show s-example
attn delegate --seed s-example --role builder \
  --cwd /repo --reuse-checkout --branch feature-x
```

An existing seed may be a leaf or a plot. Its relationships remain intact. Closed work must be deliberately replanted before dispatch. Garden unavailable means delegation fails; there is no seedless fallback.

## Hand work to another agent

Use handover to give the same seed to a fresh agent. Record what comes next and its authorization in a short handoff message; the plan stays in the body.

```sh
attn delegate --seed s-example --handover --role orchestrator \
  --cwd /repo --reuse-checkout --branch feature-x \
  -m "The user approved the plan in this seed. Execute it, coordinating implementation and review."
```

Attn saves the note and transfers ownership before starting the successor. The previous agent remains running. Handover is the explicit transfer choice; there is no extra force/confirm flag. It does not grant permission beyond the user's authorized task. An ordinary progress note does not invalidate handover; a holder changing during preparation or the seed closing requires reconsidering the request.

For a completed design, recommend an Orchestrator when stronger-model advice and oversight justify coordinating Builders; otherwise recommend a Builder. Carry forward authorization already given. Agreement on a plan alone does not authorize implementation.

## Choose the folder and checkout

Every delegation, including handover, requires `--cwd`. Attn does not infer it from a workspace or source session. Outside Git, the folder is enough. Inside Git, explicitly choose one mode and its branch arguments:

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

Reuse includes an existing linked worktree. It verifies the branch without switching it. New-worktree mode generates a destination and reports its path; use `--worktree-path` for a specific destination. A cwd subdirectory is preserved in the resulting worktree.

New branch mode uses the specified committed base, not dirty changes in the source checkout. An unavailable base fails; fetch explicitly if needed. For a remote-tracking branch, use new branch mode with `--from remote/branch`. Existing-branch mode names a local branch.

Two conflicts have different recoveries:

- Git already has the branch checked out elsewhere: use the reported folder with explicit reuse, or choose another branch. Attn will not turn a create request into reuse.
- An active Attn agent uses the selected checkout: add `--allow-worktree-reuse` only when sharing is intended. Same-checkout handover exempts the predecessor; any other occupants still require the flag. No further sharing approval step follows it.

## Choose the role and model

Read current configuration after deciding to delegate:

```sh
attn delegate roles
```

`--json` returns the same catalog as structured data. Choose the role matching the outcome and a model alternative whose condition fits; otherwise use its default choice. Keep coherent work together. Roles share their instructions across model alternatives. Use the configured fallback when no role fits. With no configured roles or fallback, direct model selection remains available.

```sh
attn delegate --brief-file question.md --cwd /notes --role pathfinder
attn delegate --brief-file question.md --cwd /notes \
  --role pathfinder --choice <choice-id>
attn delegate --brief-file task.md --cwd /notes --fallback
```

Use Attn's configured roles and choices by default. Honor an explicit user model/role request for that delegation without saving it as a preference. If standing instructions in AGENTS.md, skills or other files actually conflict with the configuration, explain that conflict and ask which should govern.

`--agent` chooses the harness. `--model` and `--effort` override the selected values. A model change retains role instructions and clears inherited effort unless explicitly supplied; a harness change clears inherited model/provider/effort. Use `default` to explicitly select a harness's model or effort default. `--provider` identifies a plugin model provider where supported; direct plugin selection uses its supported model identifier. Never silently substitute an unavailable model. Resolve ambiguous names before dispatch.

For direct delegation, choose the model explicitly. Available harnesses, models and effort levels depend on configuration; use the catalog and command help rather than assuming universal levels.

## Follow progress and recover

Successful launch output identifies the seed, session, folder/branch and operation. It means the agent launched, not that its work is done. Each new delegation creates an ordinary watch on its seed and descendants. Use the garden reference for watch/unwatch behavior; recovery does not recreate a watch you removed.

Read work with `attn seed show <seed-id>` and reach the current tender with `attn agent msg <seed-id> "…"`. To speak to the previous session after handover, use its session identity with the messaging syntax in `attn agent --help`. Follow the reporting reference in the delegated session. Use incoming completion, progress and advice requests to decide when to respond; do not shadow every edit.

If the CLI loses its response, check the request before starting another delegation:

```sh
attn delegate status <request-id>
```

Retry identical input with the same `--request-id` to retrieve or continue that operation. A changed request needs a new ID. A terminal failed operation stays failed; an intentional new attempt uses the existing seed and explicit checkout under a new request ID.

Launch failures leave transferred ownership and created worktrees/branches in place. Read the error's resource paths and current state, then explicitly reuse or remove the checkout. Attn does not restore the old owner or clean up the worktree automatically. An unknown outcome is not evidence that nothing started.

When the delegate's work is complete and you no longer need its session, read the seed and use the close rules in `attn`'s conversation reference. A close is immediate; it is separate from harvesting the assignment.

