import { defineRule } from "@oxlint/plugins";

import { allowancesSchema, reporterHonoringAllowances } from "../allowances.ts";

import type { ESTree } from "@oxlint/plugins";

const mockingMethods = new Set(["mock", "doMock"]);

function mockedModule(node: ESTree.CallExpression): string | null {
  const callee = node.callee;
  if (callee.type !== "MemberExpression" || callee.computed || callee.property.type !== "Identifier") return null;
  const mocker =
    (callee.object.type === "Identifier" && callee.object.name === "vi" && mockingMethods.has(callee.property.name)) ||
    (callee.object.type === "Identifier" && callee.object.name === "mock" && callee.property.name === "module");
  const source = node.arguments[0];
  if (!mocker || !source || source.type !== "Literal" || typeof source.value !== "string") return null;
  return source.value;
}

export const noMockingAttnModulesRule = defineRule({
  meta: {
    type: "problem",
    docs: { description: "Disallow tests that mock attn's own modules." },
    schema: allowancesSchema,
    messages: {
      ownModuleMock:
        "Tests fake only what lies outside attn (docs/internal/testing.md). Render the real module and drive it through the daemon or the Tauri host instead of mocking it.",
      unusedAllowance: "This file no longer mocks attn's own modules; remove its allowance from .oxlintrc.json.",
    },
  },
  create(context) {
    const mocks = reporterHonoringAllowances(context, "ownModuleMock");
    return {
      CallExpression(node) {
        const source = mockedModule(node);
        if (source !== null && (source.startsWith(".") || source.startsWith("/"))) mocks.report(node);
      },
      "Program:exit": mocks["Program:exit"],
    };
  },
});
