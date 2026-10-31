# Parse, don't validate

Apply when wiring validation, error handling, or adapters, and when designing types or signatures.

Establish invariants at the earliest appropriate boundary and preserve them through types, data structures, and ownership. Validate input, narrow types, and handle errors where data crosses into the system, such as command-line arguments, config files, network messages, and external APIs. Trust the types inside.

Keep decisions in pure functions that a thin shell calls, so tests can run the decisions without the framework. Do not let a boundary's private types, such as its transport, storage, or wire types, leak through the interface.

Prefer representations that make invalid states unrepresentable. Give values that must not mix their own types, even when they share a primitive. Handle every variant of a union. Derive types from the schema that owns the shape, such as a protocol definition, a migration, or an API spec, instead of writing a parallel type. Keep state-dependent checks where the relevant state is authoritative. Use the simplest representation that provides the needed guarantee.
