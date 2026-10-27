# Refactoring

Principles this process relies on: [Subtract before you add](../principles/subtract-before-you-add.md), [Minimize reader load](../principles/minimize-reader-load.md), [Migrate callers, then delete the old way](../principles/migrate-callers-then-delete-the-old-way.md), [Model the domain first](../principles/model-the-domain-first.md), [Sequence verifiable units](../principles/sequence-verifiable-units.md), [Prove it works](../principles/prove-it-works.md), [Test behavior, not implementation](../principles/test-behavior-not-implementation.md). Read them before you start; [the skill's index](../SKILL.md#principles) lists the rest.

Use this process for a change to structure or shape that preserves behavior: a rename, an extraction, an inlining, a move, a merge of duplicated paths, or the refactor that precedes a feature so the feature fits existing code. A redesign that changes behavior is a feature; say so and plan it as one. When the cleanup reveals a bug or a missing capability, split it out and land the structural change first.

## Pin the behavior

Learn the contract of the affected code before moving anything: its callers, what they observe, and the tests that already guard it. Pin the behavior at a boundary the move leaves alone, such as what the callers' own callers observe, so the pin survives the move. Where coverage is missing there, capture current behavior first, as a characterization test, a recorded baseline, or an equivalence check that diffs old and new outputs. A type check or lint is not a pin. Whether the pin is committed follows the repository's testing guidance; a scratch pin is enough to hold behavior during the move.

## Name the target shape

State what the module layout, types and call graph should be if the code were written today, and which structure the current code is missing: a state machine instead of scattered booleans, one registry instead of branching across files, one owner for a piece of state instead of synchronized copies. The reshape must delete branches, duplicated rules or invalid states; indirection that removes none of them is not a refactor. When the target crosses an interface boundary, shape it with [Design](design.md) from its callers.

## Subtract, then move in small steps

Delete dead code, collapse one-caller wrappers and remove orphan references before introducing the new shape, so the move starts from the simplest base. Then move in behavior-preserving steps that each keep the pin green. When an interface changes, follow [Migrate callers, then delete the old way](../principles/migrate-callers-then-delete-the-old-way.md). Check each rename against the files: renames miss uses in strings, prose and generated files. A test the move itself breaks depended on the old shape: delete it when the pin already covers the behavior, or replace it at the stable boundary, as the repository's testing guidance says; do not repair it to the new internals.

## Prove behavior is unchanged

Verify on the real artifact, not on compilation: rerun the pin, replay the baseline against the new code, or exercise the affected flow on the surface it serves. Then judge whether the change is worth keeping. Its measure is reader load: can a new reader answer "where does this value come from" and "what can change it" faster than before? For the refactor that precedes a change, the measure is what it gives the shared path: the change can now land there with no parallel mechanism, and the refactor is the smallest that does. If the diff meets neither, revert it.

Sequence the work as [Sequence verifiable units](../principles/sequence-verifiable-units.md) describes: the subtraction, then the reshape, then any follow-on cleanup, each held against the pin before the next starts. Report the structure that changed, the pin you held it against, the equivalence evidence, and what you reverted.
