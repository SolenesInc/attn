# Separate before serializing shared state

Apply when concurrent actors, processes or agents, might write the same file, branch, key, or object.

Before adding a lock, a retry, or another race guard, ask whether the actors need the same mutable thing at all. Usually each publishes independent facts: give each its own file, key, branch or state, and combine them where they are read. Separate keys in one object that each writer reads and rewrites whole are still one shared object. Serialize only when one shared writer is a real invariant, and then enforce it structurally, with one owner, sequential phases, or an atomic compare-and-swap, never with a convention. Agents are actors too: give each delegation its own seed and checkout, and order plot children (`after` in a plot payload, a `blocks` link afterward) only where one needs another's result.
