# Separate before serializing shared state

Apply when concurrent actors, such as processes or agents, might write the same file, branch, key, or object.

Before adding a lock, a retry, or another race guard, ask whether the actors need the same mutable thing at all. Usually each actor publishes independent facts. Give each actor its own file, key, branch, or state, and combine the facts where they are read. Separate keys in one object that each writer reads and rewrites whole are still one shared object.

Serialize only when one shared writer is a real invariant, and then enforce the invariant structurally, with one owner, sequential phases, or an atomic compare-and-swap, never with a convention. Agents are actors too. Give each delegation its own seed and checkout. Order plot children only where one child needs another child's result, using `after` in a plot payload or a `blocks` link added afterward.
