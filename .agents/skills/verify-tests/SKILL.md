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

Run each new or changed Go test once with
`./scripts/test-go.sh --packages -v -race -count=1 -run <pattern> <pkg>`.
Do not run bare `go test`, because `scripts/test-go.sh` prepares the native VT
library and the test binaries that Go tests need. Compare each test's time with
the budgets in `docs/internal/testing.md`, allowing for `-race` overhead.
Repeat a test with `-count=10` only when the test exercises concurrency or
timing outside `synctest`, or when you reproduce a flake.

Run each new or changed native wire test from `native/` with
`cargo test -p <crate> --test wire <test name>`, and compare its time with the
same budgets. When a pattern matches more than one test, use the
`--report-time` command in `docs/internal/native-wire-tests.md` to print each
test's time.

When a test's assertions look weak or indirect, break the behavior that the
test guards and run the test to confirm that the test fails. Do that in a
throwaway worktree, never in this checkout. Create the worktree with
`git worktree add --detach <path>`, and apply any uncommitted changes there
first. When you are done, remove the worktree with
`git worktree remove --force <path>`.

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
- Does the change silence a flaky test instead of fixing its cause? Widening
  assertions to accept a broken state or error (such as adding a fallback `||`
  branch), adding retries, or raising timeouts to make a flake pass is a
  finding.
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
