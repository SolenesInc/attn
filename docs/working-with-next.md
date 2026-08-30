# Working with `next`

## Start from `next`

GitHub's default is `main`; ordinary PRs target `next` explicitly:

```bash
git fetch origin next
git switch -c fix/example origin/next
gh pr create --base next
```

Retarget ordinary PRs aimed at `main`; `Main route` rejects them.

## Epic branches

Use `epic/*` for multi-PR changes to existing behavior that need combined review
and experience testing. Independent additions can land directly on `next`.

Branch the epic from `origin/next`; branch its pieces from the epic and target
their PRs there. Target the completed epic at `next`, review the combined diff,
and prepare [experience testing](../AGENTS.md#experience-testing) before merging.

## Review and merge

- Open ordinary PRs ready for review, with scoped conventional-commit titles
  and a [changelog fragment](making-a-release.md#changelog-fragments).
- Meet [verification requirements](profiles.md#verification-requirements);
  include a recording for visible changes.
- Merge only a ready PR with required checks and approval for its exact head
  and GitHub reporting it mergeable. A changed head needs fresh checks/approval.
- Squash ordinary PRs, including epic pieces and completed epics.
- Rebase for conflicts or needed newer work; unrelated `next` merges do not require it.
- After merge, `Acceptance` tests the resulting exact `next` SHA for release eligibility.

Wait once with `attn pr wait-ready <pr> --repo <owner/repo> --reviewer <login>`.
Read its combined checks, reviews, and comments; do not poll them separately.
Use `--help` for baselining, resume, and exit codes.

## Releases, hotfixes, and syncs

Only frozen `release/vX.Y.Z` candidates and urgent `hotfix/*` branches target
`main`. Read [making-a-release.md](making-a-release.md) before either or the
required merge-commit sync from `main` to `next`.
