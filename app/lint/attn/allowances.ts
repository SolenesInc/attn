import { relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

import type { Context, ESTree } from "@oxlint/plugins";

const repositoryRoot = fileURLToPath(new URL("../../..", import.meta.url));

export const allowancesSchema = [
  {
    type: "object",
    properties: { allowed: { type: "object", additionalProperties: { type: "string" } } },
    additionalProperties: false,
  },
];

export function reporterHonoringAllowances(context: Context, messageId: string) {
  const options = context.options[0] as { allowed?: Record<string, string> } | undefined;
  const path = relative(repositoryRoot, context.filename).split(sep).join("/");
  const allowed = options?.allowed?.[path] !== undefined;
  let found = 0;
  return {
    report(node: ESTree.Node) {
      found += 1;
      if (!allowed) context.report({ node, messageId });
    },
    "Program:exit"(program: ESTree.Program) {
      if (allowed && found === 0) context.report({ node: program, messageId: "unusedAllowance" });
    },
  };
}
