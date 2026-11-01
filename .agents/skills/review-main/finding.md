# What makes a finding

You review one unit of attn commits. A unit is the commits of one seed, or one
commit. The commits are either a slice about to land on `main` or commits that
already landed there. Read the
seed first (`attn seed show <seed>`) and the plan of the plot that the seed is
part of, then each commit (`git show <sha>`), then the code around the commit:
the whole file, the callers, and the tests.
`attn session transcript <Attn-Session>` shows why a commit was made. Judge
the code at the `<head>` you were given.

A unit is often one slice of a larger plan, adding what later slices will
use. Judge the unit against where the plan is going, not only against the
diff. The direction is in the plot's plan, the commit messages and, before
landing, the author's brief. Code that nothing uses yet is not a finding when
the plan or a commit message names what will use it.

Report a finding only when all of these hold:

- The unit introduced the problem, and the problem is still there at `<head>`.
- The unit's author would fix the problem if told. The problem is one discrete
  problem, not a pattern across the codebase or a taste.
- You can name the input, sequence or state that triggers the problem, and
  cite the `file:line` that proves each claim. A claim inferred from a name is
  not proof.
- The problem is P0 or P1:
  - P0: breaks attn for everyone who uses the feature, loses data, opens a
    security hole, or blocks a release.
  - P1: a real bug on a path users or agents take, a violation of a rule in
    `AGENTS.md` (including code that nothing uses and nothing explains), or a
    slice that moves away from its plan or that a later slice will have to
    undo. A race on a rare, user-driven event that costs one stray action or
    a short delay is not a P1, even when a test can reproduce the race.

Look in this order:

1. Design. Does the change fit attn's model (`docs/internal/glossary.md`)? For
   each new layer, option, fallback, cache or copy of state, name the current
   requirement it serves. If none does, say what to remove and why behavior
   stays the same.
2. Behavior. Edge cases, concurrency, restarts, Linux, what the user sees.
3. Tests. Would they fail if the code broke? Do they rely only on promises
   (`docs/internal/testing.md`)?

Leave out style, naming, anything CI checks, and what predates the unit.
When you are not sure, leave the finding out. Return no finding rather than a
weak one.

Return each finding as: commit sha, `file:line`, P0 or P1, a one-line title,
the trigger, why it matters, and what a fix needs. Or return "no findings".

If you are the verifier, you get one finding. Try to refute the finding
against the code at `<head>`. When a targeted test can settle whether the
finding holds, run the test in a throwaway worktree. Return "confirmed",
"refuted" or "uncertain", with the evidence.
