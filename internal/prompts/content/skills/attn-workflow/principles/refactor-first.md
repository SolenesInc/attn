# Refactor first

Apply before building a capability that something in the codebase may already do.

Before building a capability, find every existing path that already does the same job, searching for what it does rather than for the new feature's name. When the change needs something those paths lack, give it to the shared code in a behavior-preserving refactor, then build the change on top; do not grow a parallel mechanism inside the feature. Signs the refactor was skipped: reusing only the last step of an existing path, a new store that duplicates an existing one, or a fix that works only through incidental coupling with another part of the system.
