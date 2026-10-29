# Pull requests

Every PR branches from and targets `main`.

```bash
git fetch origin main
git switch -c fix/example origin/main
gh pr create
```

- Open PRs ready for review with a scoped conventional-commit title and a
  description that says what users observe; the
  [release notes](making-a-release.md#release-notes) are written from it.
- Meet the [verification requirements](instances.md#verification-requirements).
- Squash-merge only with green checks, approval, and a mergeable exact head,
  and only with the user's permission.
- The slopradar comment is information for the reviewer, never a gate.
- Watch with a [PR watch](../README.md#watching-pull-requests) in `--mode codex`.
- `next` is a read-only mirror of `main` for older checkouts. Nothing merges
  into it.
