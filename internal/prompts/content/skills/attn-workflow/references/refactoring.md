# Refactoring

Principles this process relies on: [Subtract before you add](../principles/subtract-before-you-add.md), [Minimize reader load](../principles/minimize-reader-load.md), [Migrate callers, then delete the old way](../principles/migrate-callers-then-delete-the-old-way.md), [Model the domain first](../principles/model-the-domain-first.md), [Sequence verifiable units](../principles/sequence-verifiable-units.md), [Prove it works](../principles/prove-it-works.md), [Test behavior, not implementation](../principles/test-behavior-not-implementation.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Use this process for a change to structure or shape that preserves behavior: a rename, an extraction, an inlining, a move, a merge of duplicated paths, or the refactor that precedes a feature so the feature fits existing code. A redesign that changes behavior is a feature. Say so, and plan it as a feature. When you find a bug or a missing capability during the cleanup, split it out and land the structural change first.

## Pin the behavior

Before you move anything, learn the contract of the affected code: its callers, what the callers observe, and the tests that already guard it. Pin the behavior at a boundary the move leaves alone, such as what the callers' own callers observe, so the pin survives the move. Where coverage is missing at that boundary, capture current behavior first, as a characterization test, a recorded baseline, or an equivalence check that diffs old and new outputs. A type check or lint is not a pin. The repository's testing guidance says whether to commit the pin. A scratch pin is enough to hold behavior during the move.

## Name the target shape

State what the module layout, types, and call graph should be if the code were written today, and which structure the current code is missing: a state machine instead of scattered booleans, one registry instead of branching across files, one owner for a piece of state instead of synchronized copies. The reshape must delete branches, duplicated rules, or invalid states. Indirection that removes none of them is not a refactor. When the target crosses an interface boundary, shape the target with [Design](design.md) from the interface's callers.

## Subtract, then move in small steps

Before you introduce the new shape, delete dead code, collapse one-caller wrappers, and remove orphan references, so the move starts from the simplest base. Then move in behavior-preserving steps that each keep the pin green. When an interface changes, follow [Migrate callers, then delete the old way](../principles/migrate-callers-then-delete-the-old-way.md). Search the files for the old name after each rename, because uses in strings, prose, and generated files are easy to miss. A test that the move itself breaks depended on the old shape. Delete the test when the pin already covers the behavior, or replace it at the stable boundary, as the repository's testing guidance says. Do not repair the test to fit the new internals.

## Prove behavior is unchanged

Verify on the real artifact, not on compilation: rerun the pin, replay the baseline against the new code, or exercise the affected flow on the surface the flow serves. Then judge whether the change is worth keeping. The measure is reader load, as [Minimize reader load](../principles/minimize-reader-load.md) defines it: can a new reader answer "where does this value come from" and "what can change it" faster than before? For a refactor that precedes a feature, the measure is whether the feature now lands on the shared path with no parallel mechanism, and whether the refactor is the smallest one that lets the feature land there. If the diff meets neither measure, revert it.

Sequence the work as [Sequence verifiable units](../principles/sequence-verifiable-units.md) describes: first the subtraction, then the reshape, then any follow-on cleanup. Hold each step against the pin before the next step starts. Report the structure that changed, the pin you held it against, the equivalence evidence, and what you reverted.
