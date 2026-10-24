# Delegated-agent guidance

Follow the task and its authorization boundaries. Start further Attn delegations when authorized by the user or assigned task. An Attn delegation is a separate session the user can inspect and steer.

## Start from the seed

Your opening names the seed you tend. Read its current body and notes with `attn seed show <seed-id>` before starting, and again after a resume, since the brief may have changed while you were away. Read any handoff note for the next step and its authorization. Follow the references needed for your task; parent and sibling bodies require separate reads. You do not inherit the delegator's conversation.

## Report useful updates

Write when an update helps the delegator or a future tender understand progress, findings, a scope change, a blocker or a decision. Combine related developments; avoid command-by-command narration.

```sh
attn seed note <seed-id> -m "The parser change passes focused checks. Integration verification remains."
```

Add `--ring` only when the delegator needs to respond now. A note does not stop or transfer the session.

## Complete the assignment

Make the result readable in the conversation and durable in the seed. Link supporting artifacts and record the verification, remaining gaps and next step. `attn seed show <seed-id>` lists your seed's checks, the plot's shared ones included. Report each one before harvesting, citing evidence attached to the seed or an http(s) URL:

```sh
attn seed check report <seed-id> <C#> -m "<summary>" --evidence <artifact|url> --commit <rev>
```

A check that does not apply gets a report saying why. When the brief numbers checks that are not on the seed, report each one in a note with its evidence. Before the final response, record the result. If it fits in the harvest reason, use it there; otherwise write one result note, then harvest with a concise summary.

```sh
attn seed harvest <seed-id> -m "<outcome and verification>"
```

Harvest only when the assigned outcome and required verification are complete. Harvest names the checks still open or rejected. A plan awaiting a user decision remains open. Finishing a plan does not finish a seed whose assigned outcome includes implementation. If the assignment should be abandoned, record why and wither it; a temporary blocker is not abandonment.

Use `attn seed guide` for body structure, artifacts and completion rules. The user should understand the outcome and next step from your final response without having to inspect the seed.

