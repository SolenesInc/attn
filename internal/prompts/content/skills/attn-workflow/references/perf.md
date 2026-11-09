# Perf

Principles this process relies on: [Explain the number](../principles/explain-the-number.md), [Build the lever](../principles/build-the-lever.md), [Sequence verifiable units](../principles/sequence-verifiable-units.md), [Prove it works](../principles/prove-it-works.md), [Attack the premise](../principles/attack-the-premise.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Use this process when a measurement shows something slow, large, or busy when it should be idle: a slow command, a leak, a process that uses CPU while nothing happens, a number that must improve against a target. The work is a measurement before it is a change. A fix you did not measure is a hypothesis.

## Reproduce and measure

Name the workload that shows the symptom: data size, history, state, concurrency. Reproduce the workload on the surface where the symptom was reported, and pick one metric and the direction that counts as better. If no case reproduces the complaint, fix the reproduction before you change code.

Build the measurement as a lever: one repeatable command that prints the metric, the error count, and the amount of work done. Take enough samples that the spread is smaller than the effect, and report the median of several runs with the spread. Start from the repository's own measurement tooling and guidance, where it has them: benchmarks, profiling entry points, performance scenarios, and the signals the repository says are stable on this machine. Build your own command only for what they do not cover. Check that the command separates an easy case from the slow case. If it cannot, change the workload or the metric, then freeze the command. Before any change, record the baseline and a green run of the tests that must keep passing.

## Find the limiter

Profile the run or read counters during it, and map what bounds the result to the source code: a lock, a core, the disk, the network, an allocation path, the load generator itself. A guess from reading the code is not a limiter. For a leak or an idle spin, instrument what grows or keeps running, and read the instrumentation while the system runs. The result of this step is a diagnosis with evidence, and the diagnosis may be the whole assignment.

## Change one thing

Test one hypothesis at a time, and state one mechanism in each hypothesis. Make the smallest change that tests the hypothesis, measure before and after with the frozen command, and run the regression tests. Keep the change only when the metric moves past the noise and behavior holds. Otherwise, revert the change in full. Do not keep a tweak that might help. Commit each accepted change on its own, and record the before-and-after measurement that justified it in that commit.

When the assignment is to keep improving one metric toward a target, work in a loop. First, state the stop predicate: a target and a floor on attempts, so a lucky early win cannot end the run. Keep a decision log as a file attached to the seed, with one line per attempt: hypothesis, change, before, after, kept or reverted. Read the log before each attempt. The seed's notes record what you kept, not every attempt. On a stall, change category or combine near misses before you conclude that no further gain is available. Correctness and simplicity outrank the number. Revert a win that breaks behavior, and keep a simplification that holds behavior. Do not relax the stop predicate so that a result meets it.

## Explain the number

Before you report a result, say what limits the result and why it is not twice as good. List what else the number could be measuring, and rule out each item with evidence: errors, skipped or cached work, code that never ran, an untuned side, noise, or a part too small to change the end-to-end result. Report the metric and target, the baseline and final values with the delta, the run count and spread, the limiter, what you kept and what you reverted, and the next idea worth trying. A number with no run count, no spread, or no named limiter is inconclusive.
