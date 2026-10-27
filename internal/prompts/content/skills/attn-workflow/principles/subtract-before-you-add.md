# Subtract before you add

Apply when sequencing an addition, a refactor, or a rewrite.

Before building, remove what the change would otherwise work around, such as dead code, redundant guards, unused options, and stale references, instructions and docs. Then build on the simpler base, and leave the design simpler, behind the same or a smaller surface, than you found it. Subtraction is how the [smallest change](smallest-change.md) stays small as the system grows. Design for observed use, not speculative edge cases. When a file or section has nothing left to say, delete it instead of leaving a stub.
