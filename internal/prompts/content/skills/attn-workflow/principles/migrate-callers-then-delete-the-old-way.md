# Migrate callers, then delete the old way

Apply when a new interface replaces an old one that still has callers.

When a new interface replaces an old one and every caller is yours to change, migrate them all and delete the old interface in the same change. Leave no compatibility shim and no parallel old and new path for the next agent to copy. Move the tests to the new contract and delete the ones that only pinned the old internals. Callers you cannot change in the same change, such as a released client, another team's code, or data a later version reads, need a compatibility path; name them.
