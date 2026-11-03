# Refactor first

Apply before building a capability that something in the codebase may already do.

Before building a capability, find every existing path that already does the same job. Search for what a path does, not for the name of the new feature. When the change needs something those paths lack, add it to the shared code in a behavior-preserving refactor. Then build the change on top. Do not grow a parallel mechanism inside the feature.

Signs that the refactor was skipped: a change that reuses only the last step of an existing path, a new store that duplicates an existing store, or a fix that works only through incidental coupling with another part of the system.
