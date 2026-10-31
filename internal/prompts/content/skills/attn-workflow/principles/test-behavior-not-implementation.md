# Test behavior, not implementation

Apply when writing, changing, keeping, or reviewing a test.

Write each test to assert what its users can observe through the interface they use, such as return values, errors, state they read back, requests sent, or rows written. Implementation is what the users cannot observe: private helpers, internal call order, and intermediate data.

Judge each test by two imagined edits. Break the behavior, by returning a wrong value, skipping an effect, or doing it twice, and the test must fail. Restructure the internals with the same observable results, and the test must pass. Keep a test that catches the first edit and survives the second. Rewrite or delete the rest. A test that survives broken behavior usually asserts less than the contract, computes its expected value with the code under test, restates a constant or prompt string without running the code that reads it, or asserts on data the test built itself.

To write a test that passes both edits, drive the code through its users' interface with a concrete input, derive the expected value from the requirement, and assert what the contract promises, no less and no more. A pin that records today's behavior so a refactor can preserve the behavior, as [Refactoring](../references/refactoring.md) describes, takes its values from the code on purpose. Say so in the pin.

Judge by the contract, not by the shape of the assertion. When "an empty cart never charges" is the promise, an assertion that the payment service got no call is behavior. Pair the assertion with a case where the charge happens, so the test shows that it can see a call. The payload a mocked external service receives is behavior. A call to an internal helper is not. The error type callers branch on is the contract. The exact message usually is not. A test can also pin an invariant, such as a key that must exist in two tables. Where the repository has testing guidance, follow that guidance to choose which kinds of test to commit.
