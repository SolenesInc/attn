# Explain the number

Apply before you trust, report, or act on a number you measured.

Before you report or act on a number you measured, such as a speedup, a regression, a latency, or an eval result, name what limits it and rule out what else it could be measuring, as [Perf](../references/perf.md) describes under Explain the number. [Prove it works](prove-it-works.md) checks that an output is real; this principle checks what a number means. For an eval result, ask the same of the trials: did every run do the task, does the gap hold across trials and models, and does the scenario matter. You skipped this when the evidence behind a number has no run count, no spread, or no named limiter, or when the time saved is larger than the time the changed piece took.
