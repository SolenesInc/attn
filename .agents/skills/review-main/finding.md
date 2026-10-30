# What makes a finding

You review one unit of attn commits: a slice about to land on `main`, or
commits that landed there, the commits of one seed or one commit. Read the
seed first (`attn seed show <seed>`) and the plan of the plot it is part of,
then each commit (`git show <sha>`), then the code around it: the whole file,
its callers, its tests. `attn session transcript <Attn-Session>` shows why a
commit was made. Judge the code at the `<head>` you were given.

A unit is often one slice of a larger plan, adding what later slices will
use. Judge it against where the plan is going, not only its diff. The
direction is in the plot's plan, the commit messages and, before landing, the
author's brief. Code that nothing uses yet is not a finding when the plan or
a commit message names what will use it.

Report a finding only when all of these hold:

- The unit introduced it, and it is still there at `<head>`.
- Its author would fix it if told. It is one discrete problem, not a pattern
  across the codebase or a taste.
- You can name the input, sequence or state that triggers it, and cite the
  `file:line` that proves each claim. A claim inferred from a name is not
  proof.
- It is P0 or P1:
  - P0: breaks attn for everyone who uses the feature, loses data, opens a
    security hole, or blocks a release.
  - P1: a real bug on a path users or agents take; a violation of a rule in
    `AGENTS.md`, including code that nothing uses and nothing explains; or a
    slice that moves away from its plan, or that a later slice will have to
    undo. A race on a rare, user-driven event that costs one stray action or
    a short delay is not one, even when a test can reproduce it.

Look in this order:

1. Design. Does the change fit attn's model (`docs/glossary.md`)? For each
   new layer, option, fallback, cache or copy of state, name the current
   requirement it serves. If none does, say what to remove and why behavior
   stays the same.
2. Behavior. Edge cases, concurrency, restarts, Linux, what the user sees.
3. Tests. Would they fail if the code broke? Do they rely only on promises
   (`docs/testing.md`)?

Leave out style, naming, anything CI checks, and what predates the unit.
When you are not sure, leave it out: no finding beats a weak one.

Return each finding as: commit sha, `file:line`, P0 or P1, a one-line title,
the trigger, why it matters, and what a fix needs. Or return "no findings".

A verifier gets one finding and tries to refute it against the code at
`<head>`, running a targeted test in a throwaway worktree when that settles
it. Return "confirmed", "refuted" or "uncertain", with the evidence.
