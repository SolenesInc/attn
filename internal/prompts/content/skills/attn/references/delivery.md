# Delivery

Delivery is how finished work reaches where it is used, and where your part
ends. The project's guidance, such as `AGENTS.md`, `CLAUDE.md`, or contributing
docs, says how to deliver. The task's authorization says how far the user lets
this task go: the steps to take beyond what the project's guidance allows, and
the steps to stop before. Where the two differ, follow the authorization. When
neither says how to deliver, ask the user.

## Pull requests

Plan in pull requests. Name the changes in each pull request and their order.
Commits inside a branch may break intermediate states, but what merges must
work.

Open the pull request and finish its checks before you hand off the merge. Fix failing checks until CI is green on the pull request head and the required repository checks pass. Address review, and answer it on the pull request. Evidence is valid for the pull request head.

When the task's authorization or the project's guidance lets you merge or push
to the main line, do it once the checks pass. Otherwise, when only the merge
remains, report the pull request, arm harvest with `--when-merged`, as
Harvesting on a merge (`attn skill --reference garden`) describes, and wait for
the user.

A seed that waits on a review, a merge or a rollout can wait on standby instead
of a session held open, as Standby (`attn skill --reference garden`) describes. Standby
grants nothing. A merge or a push needs the same permission as it did before
standby, so write what the user approved into the standby note.

## Trunk

Plan in landings. A landing is one push of finished commits to the main line
through the repository's landing step. Land each piece as soon as it works.
Every landing must keep the main line working. Leave unfinished work unwired or
behind an off-by-default setting.

Run the checks for your change, then run the repository's landing step and its
gate. Within the task's scope, the landing step is the authorized push. Evidence is valid for the commit the checks ran on. If the landing step only rebases those commits, the evidence stays valid. Cite the landed commits beside the commit the checks ran on. When the rebase changes the work, resolve conflicts and rerun the
affected checks.

Your work ends when the landing step pushes. Report the seed's checks with the
landed commits, and harvest.

Review on trunk happens after the work lands. When a reviewer rejects a check,
land each fix as a new landing.

## A trunk declaration

A repository's trunk declaration says that changes land directly on the main
line and names the landing command.
