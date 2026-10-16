# Debugging

Use this process to investigate bugs, test failures, and unexpected behavior. Establish a supported explanation before choosing a fix.

## Investigate

Read the errors, reproduce the behavior yourself on the surface where it was reported, and check relevant changes. Ask the user to reproduce only with a specific reason that surface is out of your reach, and only after driving it as far as it goes. Trace the failing data through the system. Compare the failing path with a working example and examine differences in code, configuration, and dependencies.

Separate observations from hypotheses. State what remains unexplained.

When information is missing, offer the user a concrete change to gather it, such as targeted logging, instrumentation, or a reproduction harness. Explain what the results would help distinguish and where the change would run. Keep sensitive values out of diagnostic output.

## Experiment

State a specific hypothesis and the evidence behind it. Choose a small experiment with a predicted result. Change one relevant variable at a time, inspect the outcome, and revise the hypothesis when it fails. When two fixes that shared an assumption have failed the same check, test the assumption before a third fix, as the skill's engineering judgment describes under attack the premise.

Use experiments when they can resolve uncertainty. Run isolated checks within the task's authorization and workspace constraints. Propose changes requiring broader access or approval to the user. Record enough detail to explain what the experiment established and what remains uncertain.

## Fix and verify

When implementation is authorized, capture the failure in a regression test or repeatable reproduction. Make the smallest correction the evidence justifies; a change that merely might help is a hypothesis, not a fix, and when evidence refutes a hypothesis, revert what it motivated. Verify on the surface where the failure was reported: the original reproduction now passes. A unit test alone shows branch behavior, not the bug's absence. Where the test path is cheap, land the failing reproduction before the fix in the commit history, so the sequence proves itself.

If attempts keep failing, reassess the diagnosis and approach. Consider a broader design problem when the evidence supports it, and discuss scope changes with the user.

Report what was broken, the cause, the fix, and how you verified it, with the failing-then-passing output quoted. When the cause remains uncertain, report the evidence and the next useful check. Explain temporary mitigations and what remains unresolved.

Adapted from [Systematic Debugging](https://github.com/obra/superpowers/blob/main/skills/systematic-debugging/SKILL.md), with guidance on experiments, missing evidence, and task authorization.
