# Trunk-based development

attn is developed trunk-based. Maintainers and their agents commit straight to
`main`; no pull request stands between a change and `main`. A change is
reviewed before it lands, scaled to its risk, and again on `main` afterwards. A
release ships only commits that both CI and review on `main` have passed. Contributors working from a fork use
[pull requests](pull-requests.md).

## Landing

A landing is one push of finished commits to `main` through `scripts/land.sh`.
Work lands in slices, each as soon as it works, so no branch sits waiting and
turns into conflicts. Unfinished work lands unwired, or behind an experimental
setting that is off by default. The `land-on-main` skill is the
procedure, from the working branch to the report after the push.

After the push, everything is automatic: `main`'s CI runs the full test suites
and scenario matrix, the sheriff gets `main` green again when a landing breaks
it, the review automation reviews the commits and messages their author or
dispatches a fixer, and release notes are written from the commits.

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
When `main` moves during landing, it rebases and pushes again without
re-running its checks.

## Verifying before landing

Checks before landing are fast and local: targeted tests, the change on the
surface it touches, and a review scaled to the change's risk. The full Go and
frontend suites and the scenario matrix, macOS included, run in CI on `main`
after landing, and so do the combinations of changes that landed together.

Review before landing catches a defect while its author still holds the
context, before other slices build on it. Review on `main` still reads every
commit afterwards, with the slices of a plan side by side.

The Acceptance soak workflow repeats chosen scenarios to hunt flaky ones; it is
not a landing step.

## Commits

Commits made inside attn carry an `Attn-Session:` trailer and one `Attn-Seed:`
per seed the session works on. Work users will observe has a seed. The
trailers let review, a red `main` and release notes reach the work and the
session behind a commit.

## Review on main

A scheduled review automation runs Codex over the commits `main` has gained
since the `reviewed` branch, grouped by seed, and judges each seed's commits
against its plan. For each finding it messages the session named by
`Attn-Session:` while that session is live, and otherwise dispatches an agent
to fix it. Then it moves `reviewed` forward. The
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
