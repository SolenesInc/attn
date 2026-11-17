# Explain the number

Apply before you measure, and before you trust, report, or act on a number you measured.

Before you measure, write down the decision the number informs and the effect that would change that decision. Then measure the baseline several times and record the spread, which is how far the number moves between runs of the same thing. When the spread is larger than the effect that would change the decision, the measurement cannot decide yet. Take more runs, or change the workload or the metric, as [Perf](../references/perf.md) describes under Reproduce and measure.

Before you report or act on the number, such as a speedup, a regression, a latency, or an eval result, name what limits the number and rule out what else it could be measuring, as [Perf](../references/perf.md) describes under Explain the number. [Prove it works](prove-it-works.md) covers whether an output is real. This principle covers what a number means. For an eval result, ask the same of the trials: did every run do the task, does the gap hold across trials and models, and does the scenario matter. You skipped this when the evidence behind a number has no run count, no spread, or no named limiter, or when the time saved is larger than the time the changed piece took.
