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
4. Run `scripts/land.sh`. Your work is done when it pushes. Report the checks
   with the landed commits, and harvest the seed.
5. Everything after is automatic: `main`'s CI runs the full test suites and scenario matrix,
   the sheriff gets `main` green again when a landing breaks it, the review automation
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

`scripts/land.sh` rebases onto `origin/main`, runs a fast gate for the paths the
landing changes, and pushes:

- Go: compile all packages and tests without running tests, run vet and
  staticcheck on changed packages, and check daemon lifetimes and import
  boundaries. A module change checks all packages.
- Protocol schema or prompt inputs: check that generated types or catalogs
  are current.
- `app/`: typecheck and lint, without Vitest.
- `scripts/`: run the shell script tests.

When only protocol types or prompt catalogs conflict, it regenerates them.
The full Go and frontend suites run in CI on `main`, never in the landing gate.

### When main moves

When `main` moves during landing, `scripts/land.sh` rebases and pushes again
without re-running its checks. Re-check only when the rebase stops on a real
conflict in your code: resolve it with `git rebase origin/main`, check again,
and run `scripts/land.sh` again. Combinations of changes are verified by
`main`'s CI after landing, not before.

## Verifying before landing

Run targeted tests for the behavior you changed:
`./scripts/test-go.sh --packages -run TestName ./pkg`
or `pnpm --dir app exec vitest run src/path/to/file.test.tsx`. Verify the change
on the surface it touches: the CLI, the daemon over `wsctl`, a non-production
instance, the app. The drive-attn skill's feature map lists every way into a
feature. Then land with `scripts/land.sh`.

Do not run `make test`, `make lint`, `make test-frontend`, or another full suite
as a pre-landing check. Targeted tests and the fast gate are local work; CI
runs the full suites on `main` after landing.

Running the scenarios that cover an app-observable change locally, on a
non-production instance, is optional, when the machine can take it. After
landing, `main`'s CI runs the full scenario matrix, macOS included; if a
landing breaks `main`, the sheriff dispatches a fix or a revert.
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
dispatches an agent to fix it. Then it moves `reviewed` forward. The
`review-main` skill is its procedure.

When an Orchestrator coordinates Builders, it reviews their work after landing.
A rejected seed check rings the Builder through its delegation watch, even
after harvest. Corrections are new landings.

## When main is red

CI runs on `main` after every landing; pushes that arrive during a run are
batched into the next one. A red `main` opens the
`Branch health: main acceptance` issue. The sheriff automation watches `main`'s
CI and counts a run as red when any job in it failed, even when the run itself
concluded `cancelled`. It re-runs the failed jobs once, finds the commit that
broke them, and delegates an agent to fix forward when the fix is small and
clear, or to revert the commit when the revert is clean. Every revert gets an
owner: the commit's live session, or a seed to land the change again that the
chief receives. When neither path is clearly right, the sheriff asks the user. The `sheriff` skill is its procedure. Keep landing while `main` is
red; a release waits for green.

## Releases

A release is cut on demand from a `main` commit that has green Acceptance and
that `reviewed` contains. Its notes are written from the commits since the last
release, grouped by their `Attn-Seed:` trailers, so landings add no changelog
fragment. See [Making a release](making-a-release.md).
