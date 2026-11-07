Treat each prompt edit as a revision of the instructions an agent receives.

Before editing:

1. Read the complete relevant sources and composed prompts. Follow their uses
   across roles, conditions and input producers. Include related skills and
   references; check when agents actually load them. Resolve gaps in the
   context report that matter to the intended behavior.
2. Assess the existing instructions holistically. Identify what already covers the behavior,
   what is missing or conflicting, and where the change belongs. Decide what
   to keep, rewrite, consolidate, move or remove before adding instructions.

After editing, reread the complete affected prompts. Check their order,
priorities, terminology, examples and cross-references together. Remove stale
or redundant instructions. Check the wording against
`internal/prompts/content/skills/attn-workflow/references/technical-writing.md`.
Judge whether each recipient can understand and act on the result with the
context available in that situation.

Place each instruction by when the agent needs it. An agent knows only what is
in its context:

- Launch: how the agent should behave, and the map of what attn can do. Use this
  when the agent would otherwise fail even to look. Launch text costs every
  session on every launch.
- Output: command results, errors and notifications. Use this when attn can see
  the moment, because the agent can't skip it.
- On demand: `attn <command> --help`, `attn skill --reference <name>` and
  `attn guide`. Everything else goes here, and launch or output must name it,
  because an agent never finds text nothing points to. Adding a reference or a
  top-level command means adding it to `content/session/attn-map.md` or the
  command list in `attn --help`.

Do not make quick additions when the more appropriate change is a rewrite.
