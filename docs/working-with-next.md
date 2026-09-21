# Working with `next`

## Start from `next`

GitHub's default is `main`; ordinary PRs target `next` explicitly:

```bash
git fetch origin next
git switch -c fix/example origin/next
gh pr create --base next
```

Retarget ordinary PRs aimed at `main`; `Main route` rejects them.

## Review and merge

- Open ordinary PRs ready for review, with scoped conventional-commit titles
  and a [changelog fragment](making-a-release.md#changelog-fragments).
- Meet the [verification requirements](profiles.md#verification-requirements).
- Merge only a ready PR with required checks and approval for its exact head
  and GitHub reporting it mergeable. A changed head needs fresh checks/approval.
- Squash ordinary PRs.
- Read the slopradar comment on the PR: the source mass the PR adds to or removes
  from functions over cyclomatic complexity 10, the clone pairs it introduces or
  removes, and a twelve-month trend of erosion and clone share. It is information
  for the reviewer, never a gate; the job cannot fail on the numbers.
- After merge, `Acceptance` tests the resulting exact `next` SHA for release eligibility.

Use a [PR watch or blocking wait](../README.md#watching-pull-requests) for combined
checks, reviews, and feedback; use `--mode codex` for attn's normal review signal
and do not poll those separately. Readiness does not
replace the merge gates above or the user's permission to merge.

## Releases, hotfixes, and syncs

Only frozen `release/vX.Y.Z` candidates and urgent `hotfix/*` branches target
`main`. Read [making-a-release.md](making-a-release.md) before either or the
required merge-commit sync from `main` to `next`.
