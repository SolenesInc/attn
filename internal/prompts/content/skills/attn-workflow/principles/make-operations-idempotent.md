# Make operations idempotent

Apply when designing a step that changes state and can be interrupted or retried.

A step that changes state and can be interrupted or retried, such as a command, a migration, a lifecycle step, or a processing loop, converges on the same end state however many times it runs and wherever the previous run stopped. Read the current state, compare it with the goal, and apply only the difference. Leave nothing the next run cannot recognize as stale: write to a temporary path and rename it into place, and compare by content rather than by order. For each step, ask what happens if it runs twice, and if the previous run crashed at each point.
