import { defineRule } from "@oxlint/plugins";

import { allowancesSchema, reporterHonoringAllowances } from "../allowances.ts";

import type { ESTree } from "@oxlint/plugins";

const pollingQuery = /^find(All)?By/;

function isTimer(callee: ESTree.Node): boolean {
  if (callee.type === "Identifier") return callee.name === "setTimeout";
  return (
    callee.type === "MemberExpression" &&
    !callee.computed &&
    callee.property.type === "Identifier" &&
    callee.property.name === "setTimeout" &&
    callee.object.type === "Identifier" &&
    (callee.object.name === "globalThis" || callee.object.name === "window")
  );
}

function callsTimer(node: ESTree.Node | null | undefined): boolean {
  if (!node || typeof node !== "object") return false;
  if (node.type === "CallExpression" && isTimer(node.callee)) return true;
  for (const [key, value] of Object.entries(node)) {
    if (key === "parent") continue;
    const children = Array.isArray(value) ? value : [value];
    for (const child of children) {
      if (child && typeof child === "object" && "type" in child && callsTimer(child as ESTree.Node)) return true;
    }
  }
  return false;
}

function isBunSleep(callee: ESTree.Node): boolean {
  return (
    callee.type === "MemberExpression" &&
    callee.object.type === "Identifier" &&
    callee.object.name === "Bun" &&
    callee.property.type === "Identifier" &&
    (callee.property.name === "sleep" || callee.property.name === "sleepSync")
  );
}

function isPollingQuery(callee: ESTree.Node, testingLibraryWaitFor: Set<string>): boolean {
  if (callee.type === "Identifier") return testingLibraryWaitFor.has(callee.name) || pollingQuery.test(callee.name);
  return (
    callee.type === "MemberExpression" &&
    !callee.computed &&
    callee.property.type === "Identifier" &&
    pollingQuery.test(callee.property.name)
  );
}

export const noRealTimeWaitsRule = defineRule({
  meta: {
    type: "problem",
    docs: { description: "Disallow tests that sleep or poll on real time." },
    schema: allowancesSchema,
    messages: {
      realTimeWait:
        "Tests control time instead of waiting for it (docs/internal/testing.md). Wait on the signal that the thing happened, or move a fake clock; the write-test skill's Gotchas name the replacement for each wait.",
      unusedAllowance: "This file no longer waits on real time; remove its allowance from .oxlintrc.json.",
    },
  },
  create(context) {
    const waits = reporterHonoringAllowances(context, "realTimeWait");
    const testingLibraryWaitFor = new Set<string>();
    return {
      ImportDeclaration(node) {
        if (!node.source.value.startsWith("@testing-library/")) return;
        for (const specifier of node.specifiers) {
          if (
            specifier.type === "ImportSpecifier" &&
            specifier.imported.type === "Identifier" &&
            specifier.imported.name === "waitFor"
          ) {
            testingLibraryWaitFor.add(specifier.local.name);
          }
        }
      },
      NewExpression(node) {
        if (node.callee.type !== "Identifier" || node.callee.name !== "Promise") return;
        const executor = node.arguments[0];
        if (executor && (executor.type === "ArrowFunctionExpression" || executor.type === "FunctionExpression") && callsTimer(executor.body)) {
          waits.report(node);
        }
      },
      CallExpression(node) {
        if (isBunSleep(node.callee) || isPollingQuery(node.callee, testingLibraryWaitFor)) waits.report(node);
      },
      "Program:exit": waits["Program:exit"],
    };
  },
});
