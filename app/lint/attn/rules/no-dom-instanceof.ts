import { defineRule } from "@oxlint/plugins";

import type { ESTree } from "@oxlint/plugins";

const ecmascriptBuiltins = new Set([
  "AggregateError", "Array", "ArrayBuffer", "BigInt", "BigInt64Array", "BigUint64Array", "Boolean", "DataView",
  "Date", "Error", "EvalError", "FinalizationRegistry", "Float32Array", "Float64Array", "Function", "Int16Array",
  "Int32Array", "Int8Array", "Map", "Number", "Object", "Promise", "Proxy", "RangeError", "ReferenceError",
  "RegExp", "Set", "SharedArrayBuffer", "String", "Symbol", "SyntaxError", "TypeError", "URIError", "Uint16Array",
  "Uint32Array", "Uint8Array", "Uint8ClampedArray", "WeakMap", "WeakRef", "WeakSet",
]);
const globalObjects = new Set(["window", "globalThis", "self"]);

function platformClassName(node: ESTree.Node, isGlobal: (node: ESTree.Node) => boolean): string | null {
  if (node.type === "Identifier") return isGlobal(node) && !ecmascriptBuiltins.has(node.name) ? node.name : null;
  if (node.type !== "MemberExpression" || node.computed || node.property.type !== "Identifier") return null;
  const object = node.object;
  if (object.type !== "Identifier" || !globalObjects.has(object.name) || !isGlobal(object)) return null;
  return ecmascriptBuiltins.has(node.property.name) ? null : node.property.name;
}

export const noDomInstanceofRule = defineRule({
  meta: {
    type: "problem",
    docs: { description: "Disallow instanceof against the platform's DOM classes, which belong to one window." },
    messages: {
      domInstanceof:
        "`instanceof {{name}}` is false for objects made in another window, and a tile can move to another window. Use isDomInstance(value, '{{name}}') from app/src/utils/realm.ts.",
    },
  },
  create(context) {
    const isGlobal = (node: ESTree.Node) => {
      if (node.type !== "Identifier") return false;
      for (let scope: ReturnType<typeof context.sourceCode.getScope> | null = context.sourceCode.getScope(node); scope !== null; scope = scope.upper) {
        const variable = scope.set.get(node.name);
        if (variable && variable.defs.length > 0) return false;
      }
      return true;
    };
    return {
      BinaryExpression(node) {
        if (node.operator !== "instanceof") return;
        const name = platformClassName(node.right, isGlobal);
        if (name !== null) context.report({ node, messageId: "domInstanceof", data: { name } });
      },
    };
  },
});
