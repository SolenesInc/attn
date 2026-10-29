# Trunk-based development

attn is developed trunk-based. Maintainers and their agents commit straight to
`main`; no pull request stands between a change and `main`. Review happens on
`main` after a change lands, and a release ships only commits that both CI and
review have passed. Contributors working from a fork use
[pull requests](pull-requests.md).

## Landing

```bash
git fetch origin main
git switch -c fix/example origin/main
# commit
scripts/land.sh
```

- Rebase onto `origin/main` at the start of every step, and land each slice as
  soon as it works. Unfinished work lands unwired, or behind an experimental
  setting that is off by default, rather than waiting on a branch.
- `scripts/land.sh` rebases onto `origin/main`, runs the checks for the paths
  the landing changes, and pushes. When only protocol types or prompt catalogs
  conflict, it regenerates them. When `main` moves before the push, it rebases
  and pushes again. Any other conflict stops it: resolve it with
  `git rebase origin/main`, then run it again.
- `.githooks/pre-push` refuses a plain `git push` to `main`. Use
  `git push --no-verify` only when `scripts/land.sh` cannot run, after running
  its checks yourself.

## Verifying before landing

Run the change on the surface it touches. The drive-attn skill's feature map
lists every way into a feature.

Lifecycle, protocol, PTY, background-runner, timing and other app-observable
changes also need the packaged-app scenarios that cover them green on the
change's own commit. Push the branch and run those scenarios there:

```bash
git push origin HEAD:refs/heads/fix/example
gh workflow run acceptance-soak.yml --ref fix/example \
  -f scenarios=<scenario-id,scenario-id> -f repeat=1
```

Land once that run is green. When no scenario covers the change, add one, or
tell the user before landing. The full scenario matrix runs on `main` after
every landing.

## Commits

Commits made inside attn carry an `Attn-Session:` trailer and one `Attn-Seed:`
per seed the session works on. Work users will observe has a seed. The
trailers let review, a red `main` and release notes reach the work and the
session behind a commit.

## Review on main

A scheduled review automation runs Codex over the commits `main` has gained
since the `reviewed` branch, grouped by seed. For each finding it messages the
session named by `Attn-Session:` while that session is live, and otherwise
dispatches an agent on the commit's seed to fix it. Then it moves `reviewed`
forward.

## When main is red

CI runs on `main` after every landing; pushes that arrive during a run are
batched into the next one. A red `main` opens the
`Branch health: main acceptance` issue and starts the sheriff automation. It
re-runs the failed jobs once, finds the commit that broke them, reverts it,
and tells that commit's session or seed. Keep landing while `main` is red; a
release waits for green.

## Releases

A release is cut on demand from a `main` commit that has green Acceptance and
that `reviewed` contains. Its notes are written from the commits since the last
release, grouped by their `Attn-Seed:` trailers, so landings add no changelog
fragment. See [Making a release](making-a-release.md).
