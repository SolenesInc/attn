# Migrate callers, then delete the old way

Apply when a new interface replaces an old one that still has callers.

When a new interface replaces an old one and every caller is yours to change, migrate them all and delete the old interface in the same piece of work. Leave no compatibility shim and no parallel old and new path for the next agent to copy. When a plan splits the work across delivery units, as [Delivery](../references/delivery.md) describes, name the one that deletes the old way, and leave no caller on it after that. Move the tests to the new contract and delete the ones that only pinned the old internals. Callers you cannot change in the same piece of work, such as a released client, another team's code, or data a later version reads, need a compatibility path; name them.
