# What makes a finding

You review one unit of commits that landed on attn's `main`: the commits of
one seed, or one commit. Read the seed first (`attn seed show <seed>`), then
each commit (`git show <sha>`), then the code around it: the whole file, its
callers, its tests. `attn session transcript <Attn-Session>` shows why a
commit was made. Judge the code at the `<head>` you were given.

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
  - P1: a real bug on a path users or agents take, or a violation of a rule
    in `AGENTS.md`.

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
it. Return "confirmed" or "refuted", with the evidence.
