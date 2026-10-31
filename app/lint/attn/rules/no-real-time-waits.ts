import { defineRule } from "@oxlint/plugins";

import { allowancesSchema, reporterHonoringAllowances } from "../allowances.ts";

import type { ESTree } from "@oxlint/plugins";

const testingLibraryQuery =
  /^find(All)?By(Role|LabelText|PlaceholderText|Text|DisplayValue|AltText|Title|TestId)$/;
const vitestPolls = new Set(["waitFor", "waitUntil"]);
const timerModules = new Set(["timers/promises", "node:timers/promises"]);

function propertyName(callee: ESTree.Node): string | null {
  if (callee.type !== "MemberExpression" || callee.computed || callee.property.type !== "Identifier") return null;
  return callee.property.name;
}

function objectName(callee: ESTree.Node): string | null {
  if (callee.type !== "MemberExpression" || callee.object.type !== "Identifier") return null;
  return callee.object.name;
}

function isTimer(callee: ESTree.Node): boolean {
  if (callee.type === "Identifier") return callee.name === "setTimeout";
  const object = objectName(callee);
  return propertyName(callee) === "setTimeout" && (object === "globalThis" || object === "window");
}

function callsTimer(node: ESTree.Node): boolean {
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
    const testingLibraryNamespaces = new Set<string>();
    const promisedTimers = new Set<string>();
    return {
      ImportDeclaration(node) {
        const source = node.source.value;
        for (const specifier of node.specifiers) {
          if (source.startsWith("@testing-library/") && specifier.type === "ImportNamespaceSpecifier") {
            testingLibraryNamespaces.add(specifier.local.name);
          }
          if (specifier.type !== "ImportSpecifier" || specifier.imported.type !== "Identifier") continue;
          if (source.startsWith("@testing-library/") && specifier.imported.name === "waitFor") {
            testingLibraryWaitFor.add(specifier.local.name);
          }
          if (timerModules.has(source) && specifier.imported.name === "setTimeout") {
            promisedTimers.add(specifier.local.name);
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
        const callee = node.callee;
        const property = propertyName(callee);
        const object = objectName(callee);
        const wait =
          (callee.type === "Identifier" &&
            (testingLibraryWaitFor.has(callee.name) || promisedTimers.has(callee.name) || testingLibraryQuery.test(callee.name))) ||
          (property !== null && testingLibraryQuery.test(property)) ||
          (property === "waitFor" && object !== null && testingLibraryNamespaces.has(object)) ||
          (object === "vi" && property !== null && vitestPolls.has(property)) ||
          (object === "expect" && property === "poll") ||
          (object === "Bun" && (property === "sleep" || property === "sleepSync"));
        if (wait) waits.report(node);
      },
      "Program:exit": waits["Program:exit"],
    };
  },
});
