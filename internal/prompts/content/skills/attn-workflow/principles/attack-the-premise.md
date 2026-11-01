# Attack the premise

Apply when two fixes that share one assumption have failed the same check, or fixes keep arriving in the same shape.

Write the assumption down and test it directly before you write a third fix. Each failed fix is evidence against the belief it rested on.

Find an observation that would come out differently if the assumption were false, and make that observation with a check you can rerun, as [Build the lever](build-the-lever.md) describes. Look at how the failures are distributed, not only at how large they are.

If the evidence contradicts the assumption, the cause is elsewhere. Look for what the failures have in common that the assumption did not predict. If the evidence confirms the assumption, ask the next why, as [Fix root causes](fix-root-causes.md) describes, because the earlier fixes may have treated a consequence instead of the cause.

Remove the condition instead of compensating for it. If one part always lands on the losing side, change what puts that part there. A mechanism that cleans up after the part leaves the cause in place and adds work on every run.

When fixes pass but keep arriving in the same shape, the design is the assumption. The shapes to look for are special cases for unrelated edge cases, casts or optional fields that are always set, a lock where the design said nothing was shared, and callers that must know the callee's internal rules. Stop patching. [Redesign](redesign-around-the-new-requirement.md) as if you had known the new constraint from the start, and aim smaller than the patched design. Bring the redesign to the coordinating agent named in the brief, or to the user when the brief names none, before more patches land.
