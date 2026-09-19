# Debugging

Use this process to investigate bugs, test failures, and unexpected behavior. Establish a supported explanation before choosing a fix.

## Investigate

Read the errors, reproduce the behavior, and check relevant changes. Trace the failing data through the system. Compare the failing path with a working example and examine differences in code, configuration, and dependencies.

Separate observations from hypotheses. State what remains unexplained.

When information is missing, offer the user a concrete change to gather it, such as targeted logging, instrumentation, or a reproduction harness. Explain what the results would help distinguish and where the change would run. Keep sensitive values out of diagnostic output.

## Experiment

State a specific hypothesis and the evidence behind it. Choose a small experiment with a predicted result. Change one relevant variable at a time, inspect the outcome, and revise the hypothesis when it fails.

Use experiments when they can resolve uncertainty. Run isolated checks within the task's authorization and workspace constraints. Propose changes requiring broader access or approval to the user. Record enough detail to explain what the experiment established and what remains uncertain.

## Fix and verify

When implementation is authorized, capture the failure in a regression test or repeatable reproduction. Make a focused correction and check that it resolves the original failure without breaking related behavior.

If attempts keep failing, reassess the diagnosis and approach. Consider a broader design problem when the evidence supports it, and discuss scope changes with the user.

When the cause remains uncertain, report the evidence and the next useful check. Explain temporary mitigations and what remains unresolved.

Adapted from [Systematic Debugging](https://github.com/obra/superpowers/blob/main/skills/systematic-debugging/SKILL.md), with guidance on experiments, missing evidence, and task authorization.
