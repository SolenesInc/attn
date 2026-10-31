---
name: verify-tests
description: Use before landing or opening a pull request whose changes add, change or delete tests.
---

# Verify tests

Judge whether each test this change adds, changes or deletes earns its place in
the whole suite. Report findings; do not edit.

`docs/internal/testing.md` and the "Writing tests" section of `AGENTS.md` are
attn's testing strategy. Read all of it first. This skill does not repeat it.

## Read

1. List the changed tests and the code they cover:
   `git diff --stat "$(git merge-base HEAD origin/main)"` and the new files in
   `git ls-files --others --exclude-standard`.
2. Read each changed test file whole, not only its hunks.
3. Read the code change, so you know which promises it touches.
4. For each new or changed test, search the suite for tests of the same
   behavior: the same commands, events, CLI verbs and world helpers. Read them.
   Duplication usually sits across the edge of the diff.

## Run

Go tests need the native VT library. Build it once per checkout with
`make "third_party/ghostty-vt/$(go env GOOS)_$(go env GOARCH)/lib/libghostty-vt.a"`.

Run each new or changed Go test once with `go test -v -run` and compare its
time with the budgets in `docs/internal/testing.md`. Then run it with
`go test -race -count=10 -run`: a failure in any of those runs is a flake. Run
each changed frontend test file with `pnpm --dir app exec vitest run` and
compare its time with the same budgets.

When you doubt that a test can fail, break the behavior it guards and run it.
Do that in a scratch worktree (`git worktree add --detach`, then build the
native library there), never in this checkout, and remove it when you are done.

## Judge each test

- Which promise does it guard? A test that guards none is a finding.
- Is it the right kind, the first in the order `docs/internal/testing.md` gives
  that can observe the behavior? Does it fake only what its kind allows?
- Would it break if the code were rewritten with the same behavior?
- Does another test, in this change or already in the suite, guard the same
  promise through the same entry? Name the one to keep.
- Can it fail? Does it assert only on its own fakes, check what the compiler
  guarantees, or test a helper?
- Could it fail on a good run: sleeps, polls, wall-clock deadlines, shared
  state between tests, order between tests, fixed ports or paths?
- Does it reach config paths without the test safety setup in `AGENTS.md`?
- Does a harness change model something outside attn, or does it set attn's
  own state?
- For a deleted test, does its promise keep coverage?

## Report

For each finding, give:

- the test, as `file:TestName` or the file and test title
- the rule or question above that it breaks, with evidence: the duplicate
  test, the sleep, the failing run
- the change: delete, merge into another test, move to another kind, or
  rewrite, and what the result looks like

If no test has a finding, say so in one line.
