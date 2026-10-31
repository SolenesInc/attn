# Make operations idempotent

Apply when designing a step that changes state and can be interrupted or retried.

Design each step that changes state and can be interrupted or retried, such as a command, a migration, a lifecycle step, or a processing loop, to converge on the same end state however many times it runs and wherever the previous run stopped. Read the current state, compare the current state with the goal, and apply only the difference. Leave state that the next run can tell apart from current state: write to a temporary path and rename the file into place, and compare by content rather than by order. For each step, ask what happens if the step runs twice, and what happens if the previous run crashed at each point.
