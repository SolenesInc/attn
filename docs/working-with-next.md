# Working with `next`

`main` remains GitHub's default branch because it is the stable, accepted
release line. Routine development opts in to `next`, where changes can settle
together before they are promoted to `main`.

This is the normal workflow for contributors and agents.

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

PRs within a multi-PR arc target their `epic/*` branch. The completed epic PR
then targets `next`.

## Merge without routine rebases

An ordinary PR to `next` is ready to merge when:

- it is ready for review rather than a draft;
- required checks pass for its exact head;
- it has the required approval; and
- GitHub reports it mergeable.

Do not rebase merely because another PR reached `next` first. Rebase when the
PR conflicts or when it depends on newer work from `next`. This avoids rerunning
the PR cycle after unrelated merges while still making every changed head earn
its own checks and approval.

After the merge, `Acceptance` runs the full suite against the resulting exact
`next` commit. That integrated result, rather than repeated rebases of every
open PR, determines whether `next` is accepted for a future release.

## Routes that are different

- Frozen `release/vX.Y.Z` candidates and urgent `hotfix/*` branches target
  `main`; follow [Making a release](making-a-release.md).
- A generated `sync/main-into-next-*` PR preserves ancestry with a merge commit.
  Never squash or rebase it.
- Ordinary feature, fix, and completed `epic/*` PRs do not target `main`.
