# Correction

Principles this process relies on: [Encode lessons in structure](../principles/encode-lessons-in-structure.md), [Parse, don't validate](../principles/parse-dont-validate.md), [Test behavior, not implementation](../principles/test-behavior-not-implementation.md), [Update what a decision overturns](../principles/update-what-a-decision-overturns.md). Read them before you start; [the skill's index](../SKILL.md#principles) lists the rest.

Use this process when a kind of mistake keeps coming back: the user or a reviewer corrects agents for it a second time, a review finding repeats an earlier one, or a bug's cause has happened before. It also applies when you find yourself writing the same instruction a second time, such as one rule repeated across briefs. Fix the class, not only the instance, so the next agent cannot make it.

Assume the next contributor is an agent that sees only the files it opened, copies the nearest example, and takes the shortest path that compiles, as [Design](design.md) describes under Check the structure behind it. An instruction that agent never opens does not reach it.

## Find the class

Name the class you arrived with, and find its earlier instances in recent commits, reverts, review comments, the repository's agent guidance, and code comments that explain a workaround. Some classes are mistakes of understanding: agents act on what the user overturned. Find the text that still states what the user overturned, such as a doc, a brief, a seed note, a code comment, or a name in the code. That text is the old way that the first level below removes. A class counts once it has happened twice; a one-off is fixed where it happened. The instances are the class's evidence, and the proof below needs one. When the user asks for a sweep instead, read the same sources for every mistake, group them into classes, and fix the most frequent first.

## Fix it at the highest level that works

Go down a level only when the level above cannot remove the class:

1. **Remove it with structure.** Give the state one owner and the task one supported way, hide internals so the wrong import fails, keep one list and derive the others, and delete the old way and the dead code an agent would copy. These are the structure checks in [Design](design.md); make the move with [Refactoring](refactoring.md).
2. **Make it unwritable with types.** Represent the rule so the wrong code does not compile, as [Parse, don't validate](../principles/parse-dont-validate.md) describes.
3. **Fail the build with a check.** When the wrong code still compiles, add a lint or CI check whose error names the file, type or function to use instead. When the pattern is already common, fail only when a change adds more of it.
4. **Test the behavior.** Add the test of the promise the mistake broke. A test that still passes when that behavior breaks, or fails when only internals change, does not guard the promise: replace it with one that does, or delete it when another test already covers the promise, following the repository's testing guidance.
5. **Write guidance last, and only for judgment calls.** Nothing fails when an agent skips a sentence. When a rule needs judgment and nothing can enforce it, put it where agents read it at the moment of the decision, with an example of the failure. When a level above covers the class, do not also add the instruction; the check's or test's error carries the fix.

## Prove it

Fix each class in its own commit. Show each new check failing on a real past instance of the mistake, then passing on the fix; a check nobody has seen fail is not known to work. Run the same command locally and in CI. An exception goes on the offending line with its reason, and with an expiry or the user's approval when the repository has a convention for either.

## Keep the record

Where the repository keeps its agent guidance, keep a table of the classes fixed this way: each class, one instance, and what now prevents it, from the owner, type, check or test to the guidance line for a judgment call. When a correction arrives, fix the mistake and check the table: a listed class has come back, so its fix did not hold; move it up the ladder in the same change. Remove a row once a structure or type makes the mistake unwritable.

Report each class with its evidence, the level you chose, and why the levels above it did not work.
