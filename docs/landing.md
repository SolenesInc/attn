# Landing on main

Maintainers and their agents land straight on `main`. Contributors working
from a fork follow [Pull requests](pull-requests.md).

```bash
git fetch origin main
git switch -c fix/example origin/main
# commit
scripts/land.sh
```

- Rebase onto `origin/main` at the start of every step, and land each slice as
  soon as it works. Unfinished work lands unwired, or behind an experimental
  setting that is off by default, rather than waiting on a branch.
- Work that changes what users observe has a seed, and its
  [changelog fragment](making-a-release.md#changelog-fragments) is named after
  it: `changelog.d/<seed-id>.yaml`. The first slice users can observe adds it;
  later slices update it. Invisible work adds none.
- Meet the [verification requirements](instances.md#verification-requirements)
  before landing. A change that needs packaged-app CI before it lands goes
  through a pull request.
- `scripts/land.sh` rebases onto `origin/main`, validates the changelog
  fragments, runs the checks for the paths the landing changes, and pushes.
  When only protocol types or prompt catalogs conflict, it regenerates them.
  When `main` moves before the push, it rebases and pushes again. Any other
  conflict stops it: resolve it with `git rebase origin/main`, then run it
  again.
- `.githooks/pre-push` refuses a plain `git push` to `main`. Use
  `git push --no-verify` only when `scripts/land.sh` cannot run, after running
  its checks yourself.
- CI runs on `main` after every landing. A red `main` opens the
  `Branch health: main acceptance` issue.
