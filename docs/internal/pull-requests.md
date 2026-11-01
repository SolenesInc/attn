# Pull requests

Contributors working from a fork go through a pull request. Maintainers and
their agents follow [Trunk-based development](trunk-based-development.md).

Every PR branches from and targets `main`.

```bash
git fetch origin main
git switch -c fix/example origin/main
gh pr create
```

- Open the PR ready for review, with a scoped conventional-commit title and a
  description that says what users observe. The squashed commit carries the
  description, and the [release notes](making-a-release.md#release-notes) are
  written from the commits.
- Meet the [verification requirements](instances.md#verification-requirements).
- Squash-merge only with green checks, approval, and a mergeable exact head,
  and only with the user's permission.
- Treat the slopradar comment as information for the reviewer, not as a gate.
- After you open the PR, run `attn pr watch <url> --mode codex`. See
  [PR watch](../guide/reference/delegation.md#watching-pull-requests).
- Do not target `next`. It is a read-only mirror of `main`, described in
  [Making a release](making-a-release.md#the-next-branch).
