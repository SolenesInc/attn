# Trunk-based development

attn is developed trunk-based. Maintainers and their agents commit straight to
`main`; no pull request stands between a change and `main`. Review happens on
`main` after a change lands, and a release ships only commits that both CI and
review have passed. Contributors working from a fork use
[pull requests](pull-requests.md).

## Landing

1. Start a local working branch from the latest `main`:

   ```bash
   git fetch origin main && git switch -c work origin/main
   ```

   The working branch is private and short-lived. It is never a pull request
   and is never pushed under its own name.
2. Commit as usual. The commit hook adds the `Attn-Session:` and `Attn-Seed:`
   trailers (see [Commits](#commits)).
3. Check the change yourself on the surface it touches (see
   [Verifying before landing](#verifying-before-landing)).
4. Run `scripts/land.sh`.
5. Everything after is automatic: `main`'s CI runs the full scenario matrix,
   the sheriff reverts a landing that breaks `main`, the review automation
   reviews the commits and messages their author or dispatches a fixer, and
   release notes are written from the commits.

Land each slice as soon as it works, and rebase onto `origin/main` at the start
of every step. Unfinished work lands unwired, or behind an experimental setting
that is off by default, rather than waiting on the working branch.

### Pushing

The only push is `scripts/land.sh` pushing to `main`
(`git push origin HEAD:main`). `.githooks/pre-push` refuses a plain `git push`
to `main`. Use `git push --no-verify` only when `scripts/land.sh` cannot run,
after running its checks yourself.

### What land.sh does

`scripts/land.sh` rebases onto `origin/main`, runs the checks for the paths the
landing changes, and pushes. When only protocol types or prompt catalogs
conflict, it regenerates them.

### When main moves

When `main` moves during landing, `scripts/land.sh` rebases and pushes again
without re-running its checks. Re-check only when the rebase stops on a real
conflict in your code: resolve it with `git rebase origin/main`, check again,
and run `scripts/land.sh` again. Combinations of changes are verified by
`main`'s CI after landing, not before.

## Verifying before landing

Verify the change locally on the surface it touches: the CLI, the daemon over
`wsctl`, a non-production instance, the app. The drive-attn skill's feature map
lists every way into a feature. Then land.

Running the scenarios that cover an app-observable change locally, on a
non-production instance, is optional, when the machine can take it. After
landing, `main`'s CI runs the full scenario matrix, macOS included; if a
landing breaks `main`, the sheriff reverts it and tells its session or seed.
When no scenario covers an app-observable change, add one, or tell the user.

The Acceptance soak workflow repeats chosen scenarios to hunt flaky ones; it is
not a landing step.

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
