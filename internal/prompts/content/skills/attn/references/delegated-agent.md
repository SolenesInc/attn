# Delegated-agent guidance

This reference covers an assigned delegation: how you start, report, and finish. The authorization limits in the skill's "Delegation and reporting" section apply.

## Start from the seed

Your opening names the seed you tend. You do not inherit the delegator's conversation, so start from the seed:

1. Before you start, read the seed with `attn seed show <seed-id>`. The output shows a bookmark and the newest log entries under the seed's header. When the freshest handoff is older than those entries, the output also points to it.
2. Follow any handoff for the next step and its authorization.
3. Read the parent sections, sibling results, and artifacts your task needs. `show` does not include the body of a parent or sibling. It does list the parent plot's outline. To read one part of the parent, run `attn seed show <parent> --section <heading|id>`.

After a resume, read what changed while you were away with `attn seed show <seed-id> --since <bookmark>`. When you no longer have the bookmark, read the whole seed. An `edit` entry means the brief changed, so read the body again.

## Report useful updates

Write an update when it helps the delegator or a future tender understand progress, findings, a scope change, a blocker or a decision. Combine related developments. Do not narrate command by command.

```sh
attn seed note <seed-id> -m "The parser change passes focused checks. Integration verification remains."
```

Add `--ring` only when the delegator needs to respond now. A note does not stop or transfer the session.

## Report each check as you prove it

`attn seed show <seed-id>` lists your seed's checks, including the shared checks of the plot. Report each check in one call when you prove it, and report every check before you harvest. When the check ran against a Git revision, add the commit it ran on.

```sh
attn seed check report <seed-id> <C#> -m "<summary line>" --evidence <file|artifact|url> [--commit <rev>]
```

The first line of `-m` is a summary line of at most 400 characters. Put what a reviewer needs beyond the summary on the lines after it. attn keeps those lines in the report's check note. `--evidence` takes a local file, an artifact on the seed, or an http(s) URL. attn copies a local file onto the seed and cites the file in the same call. A refused report lists every problem at once. `--commit` validates a Git revision, so omit it for a check that ran outside Git, such as research, a plan or a Notebook document, and let the evidence carry the proof.

For a check that does not apply, report why and cite what shows it. The reviewer then rules on the check.

Each later run is a new report. The new report replaces the earlier report and clears its verdict, so the seed always shows which evidence is current. When the plan or notes cite the earlier file, cite the new evidence under a new name. Otherwise, to replace the earlier file, cite the new evidence under the same name.

When the brief lists numbered checks that the seed does not have, report each of them in a note with its evidence.

## Complete the assignment

To finish the assignment, do these in order:

1. Link the supporting artifacts to the seed.
2. Record the result, the verification, the remaining gaps, and the next step on the seed. If they fit in the harvest reason, put them there. Otherwise write one result note first.
3. When the assigned outcome and required verification are complete, harvest the seed with a concise summary:

```sh
attn seed harvest <seed-id> -m "<outcome and verification>"
```

4. Write your final response in the conversation so the user understands the outcome and the next step without inspecting the seed.

When only the merge of someone else's pull request remains, release your claim with `--when-merged`, as [Harvesting on a merge](garden.md#harvesting-on-a-merge) describes. Keep tending while work remains. Follow the [Delivery](../../attn-workflow/references/delivery.md) reference in `attn-workflow` for the repository's flow and stopping point.

Harvest only when the assigned outcome and required verification are complete. The harvest command lists the checks that are still open or rejected. Leave a seed open when its plan awaits a user decision. Do not treat a finished plan as a finished seed when the assigned outcome includes implementation. To abandon the assignment, record why and wither the seed. A temporary blocker is not abandonment.

Use `attn seed guide` for body structure, artifacts, and completion rules.
