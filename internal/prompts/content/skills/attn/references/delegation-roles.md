# Delegation roles

Delegation roles are the user's routing table in Settings > Delegation: each role says what kind of work it covers, how the delegated agent should work, and which harness, model and effort it runs on. Alternatives give a role other models for conditions its default should not cover. The fallback covers work no role fits.

Change the saved table only when the user asks to change it. A model or role the user requests for one delegation is a launch override, not a request to save it. Use `attn delegate roles --help` for the installed command syntax.

## Read before changing

`attn delegate roles` prints what delegating agents choose from: enabled roles with a complete default model. `attn delegate roles show` prints the whole saved table, including roles that are turned off or still need a model; name a role to read its full guidance and alternatives.

## Make the change

Make the smallest change that does what the user asked, one command per change. Pass `-m` with the user's reason in their terms; it is what they read later in the history. Each command applies to the latest table and prints the revision it created with a line per change. Check that those lines match the request, then tell the user what changed.

- Use the exact model ID the user gave. Resolve ambiguous names with the user rather than guessing; Attn checks that a model is available when a delegation launches, not when the table is saved.
- Write an alternative's condition as a concrete test the delegating agent can apply to a task. An alternative with no condition is never picked.
- Attn maintains the guidance of its own roles (Pathfinder, Builder, Reviewer, Orchestrator). Change their models directly. To change their guidance, copy the role, edit the copy, and turn off or remove the original if the user wants it replaced.
- Adding Attn's maintained roles the first time installs the `attn-workflow` skill, which the user does from Settings > Delegation.
- For a restructuring that touches most of the table, export it with `show --json`, edit the JSON, and `apply` it. Apply refuses when the table changed since the export; export again and redo the edit.

A change is live for the next delegation launch. Delegations already started keep the role and model they launched with.

## Undo

Every change, including a rollback, adds a revision. `attn delegate roles history` lists them with who made each change and why.

`attn delegate roles rollback` restores the table that was live before the current one; repeating it keeps walking back. `rollback <revision>` restores that revision, older or newer, so rolling forward after an undo is restoring the later revision. When a change you made is not what the user wanted, roll it back rather than editing it back by hand.
