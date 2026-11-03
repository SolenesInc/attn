---
name: verify-tests
description: Use before landing or opening a pull request whose changes add, change or delete tests.
---

# Verify tests

Judge whether each test that this change adds, changes or deletes earns its
place in the whole suite. Report findings. Do not edit.

`docs/internal/testing.md` and the "Writing tests" section of `AGENTS.md` are
attn's testing strategy. The Gotchas in the `write-test` skill name the habits
that most often break the strategy. Read all three first, and read the page
that `write-test` names for each kind of test the change touches. This skill
does not repeat them.

## Read

1. List the changed tests and the code they cover:
   `git diff --stat "$(git merge-base HEAD origin/main)"` and the new files in
   `git ls-files --others --exclude-standard`.
2. Read each changed test file whole, not only its hunks.
3. Read the code change, so you know which promises the change touches.
4. For each new or changed test, search the suite for tests of the same
   behavior: the same commands, events, CLI verbs and world helpers. Read
   them. The duplicates are usually in tests outside the diff.

## Run

Go tests need the native VT library. Build the library once per checkout with
`make "third_party/ghostty-vt/$(go env GOOS)_$(go env GOARCH)/lib/libghostty-vt.a"`.

Run each new or changed Go test once with `go test -v -run`, and compare the
time with the budgets in `docs/internal/testing.md`. Then run the test with
`go test -race -count=10 -run`. A failure in any of those runs is a flake. Run
each changed frontend test file with `pnpm --dir app exec vitest run`, and
compare the time with the same budgets.

When you doubt that a test can fail, break the behavior that the test guards,
and run the test. Do that in a scratch worktree (`git worktree add --detach`,
then build the native library there), never in this checkout. Remove the
worktree when you are done.

## Judge each test

- Which promise does the test guard? A test that guards none is a finding.
- Is the test the right kind, the first in the order `docs/internal/testing.md`
  gives that can observe the behavior? Does the test fake only what its kind
  allows?
- Would the test break if the code were rewritten with the same behavior?
- Does another test, in this change or already in the suite, guard the same
  promise through the same entry? Name the test to keep.
- Can the test fail? Does it assert only on its own fakes, check what the
  compiler guarantees, or test a helper?
- Could the test fail on a good run? Look for sleeps, polls, wall-clock
  deadlines, shared state between tests, order between tests, and fixed ports
  or paths.
- Does the test reach config paths without the test safety setup in
  `AGENTS.md`?
- Does a harness change model something outside attn, or does it set attn's
  own state? A pause or crash point is the one allowed reach inside, when the
  promise is a moment between two steps of one action and the point sets no
  state.
- For a deleted test, does another test keep the coverage of its promise?

## Report

For each finding, give:

- the test, as `file:TestName` or the file and test title
- the rule or question above that the test breaks, with evidence: the
  duplicate test, the sleep, the failing run
- the change: delete, merge into another test, move to another kind, or
  rewrite, and what the result looks like

If no test has a finding, say so in one line.
