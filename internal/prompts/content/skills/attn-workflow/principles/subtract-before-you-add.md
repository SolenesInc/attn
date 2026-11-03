# Subtract before you add

Apply when sequencing an addition, a refactor, or a rewrite.

Before building, remove what the change would otherwise work around, such as dead code, redundant guards, unused options, and stale references, instructions, and docs. Then build on the simpler base, and leave the design simpler than you found it, behind the same or a smaller surface. Subtracting keeps the [smallest change](smallest-change.md) small as the system grows. Design for observed use, not speculative edge cases. When nothing useful remains in a file or section, delete the file or section instead of leaving a stub.
