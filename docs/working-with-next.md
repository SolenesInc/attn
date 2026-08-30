# Working with `next`

Normal development lands on `next`. GitHub's default branch remains `main`,
the accepted release line.

## Start from `next`

Create feature and fix branches from the latest `origin/next`, then name the
base explicitly when opening the PR:

```bash
git fetch origin next
git switch -c fix/example origin/next

# make and commit the change

gh pr create --base next
```

GitHub will otherwise suggest `main` because it is the repository default. A
normal PR opened against `main` fails the `Main route` check and should be
retargeted to `next`.

## When to use an epic branch

Use an `epic/*` branch when a multi-PR change visibly alters existing behavior
and needs review and experience testing as a whole before entering `next`.
Independent additions that leave existing behavior intact can go straight to
`next` as each PR is ready.

Create the epic from `origin/next`, branch each piece from the epic, and target
its PR at that `epic/*` branch. When the arc is complete, open the epic PR
against `next` and review the combined change. Prepare the
[experience test](../AGENTS.md#experience-testing) from that branch before
merging it.

## Prepare a PR

Open ordinary PRs ready for review. Use a scoped conventional-commit title in
plain language, such as `fix(queue): hand over the next agent however a turn closes`.
Add a [changelog fragment](making-a-release.md#changelog-fragments).

Follow the repository's [verification requirements](profiles.md#verification-requirements),
including a recording for a visible change.

## Merge without routine rebases

An ordinary PR to `next` is ready to merge when:

- it is ready for review;
- required checks pass for its exact head;
- it has the required approval; and
- GitHub reports it mergeable.

Squash ordinary PRs, including PRs within an epic and the completed epic PR.
Rebase a PR to `next` when it conflicts or depends on newer work. Unrelated
merges to `next` do not require a rebase. Each changed PR head needs its own
checks and approval.

After the merge, `Acceptance` runs the full suite against the resulting exact
`next` commit. A green result makes that commit eligible for a release candidate.

To wait on a PR, run this once:

```bash
attn pr wait-ready <pr> --repo <owner/repo> --reviewer <login>
```

It reports checks, reviews, and comment bodies together. Do not poll those
separately. Use `--help` for exit codes, baselining, and resume.

## Releases, hotfixes, and syncs

Only frozen `release/vX.Y.Z` candidates and urgent `hotfix/*` branches target
`main`. They follow [Making a release](making-a-release.md), which also owns
the required merge-commit sync back to `next` after each `main` change.
