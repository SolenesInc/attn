# Delivery

Delivery is how a finished change reaches the repository's main line and where
your part ends. Read the repository's agent guidance, such as `AGENTS.md`,
`CLAUDE.md`, or contributing docs, to choose the flow. When it says nothing,
use pull requests. Follow the task's authorization if it stops before delivery.

## Pull requests

Plan in pull requests: name the changes in each one and their order. Commits
inside a branch may break intermediate states, but what merges must work.

Open the pull request and finish its checks before handing off the merge.
CI must be green on its head, required repository checks must pass, and review
must be addressed. Answer review on the pull request. Evidence counts for the
pull request head.

When only the merge remains, report the pull request and arm harvest with
`attn seed harvest <seed-id> --when-merged <pr-url> -m "<outcome and verification>"`.
The seed harvests when the pull request merges. Merging and pushing to the main
line wait for the user unless the task authorizes them.

## Trunk

Plan in landings. A landing is one push of finished commits to the main line
through the repository's landing step. Land each piece as soon as it works.
Every landing keeps the main line working; unfinished work stays unwired or
behind an off-by-default setting.

Run the checks for your change, then the repository's landing step and its gate.
Within the task's scope, that step is the authorized push. Evidence counts for
the commit it ran on. A landing that only rebases those commits keeps the
evidence valid; cite the landed commits beside the tested revision. Resolve
conflicts and rerun affected checks when the rebase changes the work.

Your work ends when the landing step pushes. Report the seed's checks with the
landed commits, and harvest.

An Orchestrator reviews a Builder's work after it lands. A rejected check rings
the Builder through its delegation watch, as [Orchestration](orchestration.md)
describes for harvested seeds. Fixes are new landings.

## A trunk declaration

A repository's trunk declaration says that changes land directly on the main
line and names the landing command.
