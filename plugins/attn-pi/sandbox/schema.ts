import { Type, type Static } from "typebox";

export const bashParameterSchema = Type.Object({
  command: Type.String({ description: "Bash command to execute" }),
  timeout: Type.Optional(Type.Number({ description: "Timeout in seconds (optional, no default timeout)" })),
  sandbox_permissions: Type.Optional(Type.Union([Type.Literal("use_default"), Type.Literal("require_escalated")], {
    description: "Per-command sandbox override. Defaults to `use_default`; use `require_escalated` for unsandboxed execution.",
  })),
  justification: Type.Optional(Type.String({
    description: "User-facing approval question for `require_escalated`; omit otherwise.",
  })),
  prefix_rule: Type.Optional(Type.Array(Type.String(), {
    description: 'Reusable approval prefix for `command`, only with `sandbox_permissions: "require_escalated"`; for example ["git", "pull"].',
  })),
});

export type BashParameters = Static<typeof bashParameterSchema>;
