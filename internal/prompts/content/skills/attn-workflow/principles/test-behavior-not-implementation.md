# Test behavior, not implementation

Apply when writing, changing, keeping, or reviewing a test.

A test claims what its users can observe, such as return values, errors, state they read back, requests sent, or rows written. Judge each test by two imagined edits. Break the behavior, and the test must fail. Restructure the internals with the same observable results, and the test must pass. Derive expected values from the requirement, not from a run of the code. The repository's testing guidance decides which kinds of test it commits.
