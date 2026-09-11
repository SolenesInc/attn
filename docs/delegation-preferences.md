# Delegation preferences

Settings > Delegation chooses how authorized delegations run. Enabling role
guidance uses saved roles and model choices. It does not create roles or install
a skill. Turning guidance off keeps the saved configuration.

**Add Attn roles** installs the `attn-workflow` skill and adds references to
Attn's maintained Pathfinder, Builder, Reviewer and Orchestrator guidance. Attn
supplies their behavior; you choose whether each role is enabled and which
harness, provider, model and effort each choice uses. Duplicate a maintained
role to make an editable custom copy. Existing custom roles never become
maintained automatically.

The adoption preview shows which custom rows will be replaced and which model
choices will carry over. The action saves the current draft, installs the skill
and adds the reviewed references. A failed installation leaves saved roles
unchanged and reports the paths involved; retry after correcting the error.

Skill files update through Attn's existing settings and launch synchronization
after opt-in. Manual edits may be overwritten. Removing a role or disabling
guidance does not uninstall the shared skill.

Each role has a default model choice and may have conditional alternatives. The
fallback applies only when no role fits. Blank model or effort fields use the
harness default. Discovery runs only when requested and does not send a model
prompt. Catalog membership does not prove account access; known unsupported
model and effort choices are rejected at launch.

Agents call `attn delegate roles` after delegation is authorized. It returns
only complete active roles and choices. If another instruction set also defines
a delegation router, role catalog or model policy, the agent stops and asks the
user which system should own the choice.

Launch with `--role <id> [--choice <id>]` or `--fallback`. Explicit model and
effort flags affect only that request. Changing a model clears inherited effort
unless effort is also supplied. Accepted operations keep the resolved role,
guidance and model snapshot, so retrying the same request remains stable after a
settings change. Preferences never authorize delegation or expand task scope.

Use `attn delegate --help` for the complete assignment, cwd and checkout
contract. Every launch has one seed; the opening names it instead of copying its
mutable body.
