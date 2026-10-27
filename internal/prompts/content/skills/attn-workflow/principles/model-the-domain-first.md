# Model the domain first

Apply before writing logic, and when code branches a lot or repeats a shape assumption across files.

Name the data shape before writing logic, meaning the core types, every access pattern that reads them, and the structure that serves the common path. Encode the domain in a structure, such as a state machine, a typed model, a table or registry, or a reducer, instead of conditionals spread across files. Signs it was skipped: a feature that adds one more branch to an existing if/else chain, a second boolean that must stay in sync with the first, or modules named for steps (load, validate, save) that each repeat the same rules. Do not force a structure onto code that is already clear and local. Build first what helps every later step, such as a test harness, shared types, or a [lever](build-the-lever.md). [Design](../references/design.md) holds the checks for the structure behind an interface.
