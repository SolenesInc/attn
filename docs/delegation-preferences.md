# Delegation preferences

Settings > Delegation is a routing table: one row per role, and the model each
role runs on. Agents that delegate read the table and pick the row that fits
the work. The switch in the section head turns the table on and off; off keeps
every row and lets agents choose harness and model themselves.

Every edit saves as it happens. There is no Save button and no draft: a change
is live for the next `attn delegate roles` call as soon as the row settles. A
save that collides with a change made elsewhere reloads the table and shows the
daemon's reason; make the edit again on the fresh table. Deleting a role offers
Undo until the next edit.

## Rows

A role row shows its icon, name, the first line of its description, and its
model. Click the name or the details button to open the row. A custom role
edits its name, icon, "When to choose this role", instructions and stopping
point in place. A maintained role (tagged Attn) shows the same guidance
read-only; Attn keeps it current with releases. **Make an editable copy**
turns a maintained role into a custom one with the same guidance. A role can
be turned off without deleting it; an off row stays in the table and is hidden
from agents.

**Add Attn roles** installs the `attn-workflow` skill and adds Attn's
maintained Pathfinder, Builder, Reviewer and Orchestrator. When a custom role
already carries a maintained role's name, a panel asks whether to add the
maintained role beside it or replace it; replacing keeps the custom row's model
choices. A failed installation leaves the saved roles unchanged and reports
the paths involved. Removing a role or turning the table off does not
uninstall the skill. Skill files update through Attn's settings and launch
synchronization; manual edits may be overwritten.

**Anything else** is the last row: the model agents use when no role fits.
Its instructions are optional.

## Models

Click a row's model to open the model picker. Pick a harness on the left; the
harness lists its models on the right as soon as it is chosen, and the list is
kept for the rest of the app's run (refresh asks again). Picking a model saves
it. Effort appears once a model is chosen, as the levels the harness reported
or as a free field when it reported none. "Enter a model ID" saves an exact ID
the harness did not list. A harness that pins no model runs whatever its own
settings select; choosing it saves the harness alone.

Discovery never sends a model prompt. Being listed does not prove the account
can use a model; known unsupported model and effort choices are rejected at
launch. Blank model or effort fields use the harness default.

A role whose default model is not chosen yet is tagged **Needs a model** and is
not offered to agents. The foot of the table says how many roles agents see.

## Alternatives

A role can carry alternative models for conditions the default should not
cover. **+ Alternative model** adds one below the role's guidance, starting
from the default model. Each alternative has a name and a prose condition,
"When to use this instead of the default", which can run to several
paragraphs; the agent reads it to decide. An alternative without a condition
is saved but never picked. A collapsed row shows how many alternatives it
carries.

## Agents

Agents call `attn delegate roles` after delegation is authorized. It returns
only enabled roles with a complete default model, with their alternatives and
the fallback. If another instruction set also defines a delegation router,
role catalog or model policy, the agent stops and asks the user which system
should own the choice.

Launch with `--role <id> [--choice <id>]` or `--fallback`. Explicit model and
effort flags affect only that request. Changing a model clears inherited effort
unless effort is also supplied. Accepted operations keep the resolved role,
guidance and model snapshot, so retrying the same request remains stable after a
settings change. Preferences never authorize delegation or expand task scope.

Use `attn delegate --help` for the complete assignment, cwd and checkout
contract. Every launch has one seed; the opening names it instead of copying its
mutable body.

Durable multi-agent workflows have their own section, Settings > Workflows.
