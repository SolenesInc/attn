# Working with `next`

Ordinary PRs branch from and target `next`; only release candidates and
`hotfix/*` branches target `main`.

```bash
git fetch origin next
git switch -c fix/example origin/next
gh pr create --base next
```

- Open PRs ready for review with a scoped conventional-commit title and a
  [changelog fragment](making-a-release.md#changelog-fragments).
- Meet the [verification requirements](profiles.md#verification-requirements).
- Squash-merge only with green checks, approval, and a mergeable exact head,
  and only with the user's permission.
- The slopradar comment is information for the reviewer, never a gate.
- Watch with a [PR watch](../README.md#watching-pull-requests) in `--mode codex`.
