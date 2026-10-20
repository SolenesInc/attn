# Perf

Use this process when something is measured to be slow, large, or busy when it should be idle: a slow command, a leak, a process that burns CPU while nothing happens, a number that must improve against a target. The work is a measurement before it is a change. A fix that is not measured is a hypothesis.

## Reproduce and measure

Name the workload that shows the symptom: data size, history, state, concurrency. Reproduce it on the surface where it was reported, and pick one metric with the direction that counts as better. If no case reproduces the complaint, fix the reproduction before touching code.

Build the measurement as a lever: one repeatable command that prints the metric, its error count and the amount of work it did, sampled enough to clear the noise, as a median of several runs with the spread. Start from the repository's own measurement tooling and guidance where it has them, its benchmarks, profiling entry points, performance scenarios and the signals it says are stable on this machine, and build your own command only for what they do not cover. Check that the command separates an easy case from the slow one; if it cannot, change the workload or the metric, then freeze it. Record the baseline and a green run of the tests that must keep passing before any change.

## Find the limiter

Profile or read counters during a run and map what bounds the result to source: a lock, a core, the disk, the network, an allocation path, the load generator itself. A guess from reading the code is not a limiter. For a leak or an idle spin, instrument what grows or wakes and read it as the system runs; the deliverable of that step is a diagnosis with evidence, and it may be the whole assignment.

## Change one thing

One hypothesis names one mechanism. Make the smallest change that tests it, measure before and after with the frozen command, and run the regression tests. Keep the change only when the metric moves past the noise and behavior holds; otherwise revert it in full. A tweak that might help is not kept. Commit each accepted change on its own, so each keeps the before-and-after measurement that justified it.

When the assignment is to keep improving one metric toward a target, loop: state the stop predicate up front, a target and a floor on attempts so a lucky early win cannot end the run, keep a decision log as a file attached to the seed, one line per attempt (hypothesis, change, before, after, kept or reverted), and read it before each attempt; the seed's notes carry what was kept, not every attempt. On a stall, change category or combine near misses before concluding the hill is climbed. Correctness and simplicity outrank the number: revert a win that breaks behavior, and keep a simplification that holds it. Do not relax the predicate to meet it.

## Explain the number

Before reporting a result, say what limits it and why it is not twice as good, list what else the number could be measuring (errors, skipped or cached work, code that never ran, an untuned side, noise, a piece too small to matter end to end) and rule each out with evidence. Report the metric and target, baseline to final with the delta, run count and spread, the limiter, what was kept and what was reverted, and the next idea worth trying. A number with no run count, no spread or no named limiter is inconclusive.
