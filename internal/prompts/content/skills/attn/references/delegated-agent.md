# Delegated-agent guidance

Follow the task and its authorization boundaries. Start further Attn delegations when the user or the assigned task authorizes them. An Attn delegation is a separate session the user can inspect and steer.

## Start from the seed

Your opening names the seed you tend. Before you start, read the seed with `attn seed show <seed-id>`. Under the seed's header, the output shows a bookmark and the newest log entries, with a pointer to the freshest handoff when the handoff is older than those entries. After a resume, read what changed while you were away with `attn seed show <seed-id> --since <bookmark>`. When you no longer have the bookmark, read the whole seed. An `edit` entry means the brief changed, so read the body again. Follow any handoff for the next step and its authorization. Follow the references your task needs. `show` does not include a parent or sibling body, so read one separately when your task needs it. `show` lists the parent plot's outline, and `attn seed show <parent> --section <heading|id>` reads one part of the parent. You do not inherit the delegator's conversation.

## Report useful updates

Write an update when it helps the delegator or a future tender understand progress, findings, a scope change, a blocker or a decision. Combine related developments. Do not narrate command by command.

```sh
attn seed note <seed-id> -m "The parser change passes focused checks. Integration verification remains."
```

Add `--ring` only when the delegator needs to respond now. A note does not stop or transfer the session.

## Complete the assignment

Write the result in the conversation and record it on the seed. Link supporting artifacts and record the verification, remaining gaps and next step. `attn seed show <seed-id>` lists your seed's checks, including the shared checks of the plot. Before you harvest, report each check in one call. The first line of `-m` is a summary line of at most 400 characters, and any lines after it are detail. `--evidence` takes a local file (which attn copies onto the seed), an artifact on the seed, or an http(s) URL:

```sh
attn seed check report <seed-id> <C#> -m "<summary line>" --evidence <file|artifact|url> --commit <rev>
```

For a check that does not apply, report why and cite what shows it. When the brief numbers checks that are not on the seed, report each of them in a note with its evidence. Before your final response, record the result. If the result fits in the harvest reason, put it there. Otherwise write one result note, then harvest with a concise summary.

```sh
attn seed harvest <seed-id> -m "<outcome and verification>"
```

When only the merge of someone else's pull request remains, release your claim
and keep the outcome and verification as the eventual harvest reason:

```sh
attn seed harvest <seed-id> --when-merged <pr-url> -m "<outcome and verification>"
```

This command merges nothing. Keep tending while work remains, such as review notes to answer, docs to write, or verification to finish. Follow the [Delivery](../../attn-workflow/references/delivery.md) reference in `attn-workflow` for the repository's flow and stopping point.

Harvest only when the assigned outcome and required verification are complete. Harvest lists the checks still open or rejected. Leave a seed open when its plan awaits a user decision. Do not treat a finished plan as a finished seed when the assigned outcome includes implementation. To abandon the assignment, record why and wither the seed. A temporary blocker is not abandonment.

Use `attn seed guide` for body structure, artifacts and completion rules. Write your final response so the user understands the outcome and next step without inspecting the seed.

