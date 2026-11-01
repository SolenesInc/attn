# Debugging

Principles this process relies on: [Fix root causes](../principles/fix-root-causes.md), [Attack the premise](../principles/attack-the-premise.md), [Prove it works](../principles/prove-it-works.md), [Sequence verifiable units](../principles/sequence-verifiable-units.md), [Build the lever](../principles/build-the-lever.md), [Test behavior, not implementation](../principles/test-behavior-not-implementation.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Use this process to investigate bugs, test failures, and unexpected behavior.
When the symptom is a measurement, such as slowness, growth, or CPU while
nothing happens, use [Perf](perf.md) instead. Establish a supported explanation
before choosing a fix.

## Investigate

Read the errors, reproduce the behavior yourself on the surface where it was
reported, and check relevant changes. Ask the user to reproduce only when you
have a specific reason that the surface is out of your reach, and only after you
drive the surface as far as it goes. Trace the failing data through the system.
Compare the failing path with a working example, and examine the differences in
code, configuration, and dependencies.

Separate observations from hypotheses. State what remains unexplained.

When information is missing, offer the user a concrete change to gather the
information, such as targeted logging, instrumentation, or a reproduction
harness. Explain what the results would help you tell apart and where the change
would run. Keep sensitive values out of diagnostic output.

## Experiment

State a specific hypothesis and the evidence behind it. Choose a small
experiment with a predicted result. Change one relevant variable at a time,
inspect the outcome, and revise the hypothesis when it fails.

Use experiments when they can resolve uncertainty. Run isolated checks within
the task's authorization and workspace constraints. Propose to the user any
change that needs broader access or approval. Record enough detail to show what
the experiment established and what remains uncertain.

## Fix and verify

When implementation is authorized, capture the failure in a regression test or
repeatable reproduction. Make the smallest correction the evidence justifies. A
change that merely might help is a hypothesis, not a fix. When evidence refutes
a hypothesis, revert what the hypothesis motivated. Run the original reproduction on
the surface where the failure was reported. It must pass. A unit test
alone shows branch behavior, not that the bug is gone. When the reproduction is
a committed test, commit the test before the fix. You can then watch the test fail without the fix and pass with it.
When the cause is a mistake that has happened before, fix the class too: follow
[Correction](correction.md), or plant a seed for the class when the work falls
outside the task.

If two fixes that share an assumption have failed the same check, [Attack the
premise](../principles/attack-the-premise.md) before the next fix. If the
assumption fails, reassess the diagnosis and the approach.
Consider a broader design problem when the evidence supports one, and discuss
scope changes with the user.

Report what was broken, the cause, the fix, and how you verified it, with the
failing-then-passing output quoted. When the cause remains uncertain, report the
evidence and the next useful check. Explain temporary mitigations and what
remains unresolved.

Adapted from
[Systematic Debugging](https://github.com/obra/superpowers/blob/main/skills/systematic-debugging/SKILL.md),
with guidance on experiments, missing evidence, and task authorization.
