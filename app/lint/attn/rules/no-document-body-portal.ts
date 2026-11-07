import { defineRule } from "@oxlint/plugins";

import type { ESTree } from "@oxlint/plugins";

function isCreatePortal(callee: ESTree.Node): boolean {
  if (callee.type === "Identifier") return callee.name === "createPortal";
  return callee.type === "MemberExpression" && !callee.computed && callee.property.type === "Identifier" && callee.property.name === "createPortal";
}

function isDocumentBody(node: ESTree.Node | undefined): boolean {
  return node?.type === "MemberExpression" && !node.computed
    && node.object.type === "Identifier" && node.object.name === "document"
    && node.property.type === "Identifier" && node.property.name === "body";
}

export const noDocumentBodyPortalRule = defineRule({
  meta: {
    type: "problem",
    docs: { description: "Disallow portals into main's document.body, which open in main when the tile is in another window." },
    messages: {
      documentBodyPortal:
        "A portal into document.body opens in the main window even when its tile is in another window. Portal into usePortalTarget() from app/src/tileWindow.",
    },
  },
  create(context) {
    return {
      CallExpression(node) {
        if (isCreatePortal(node.callee) && isDocumentBody(node.arguments[1])) context.report({ node: node.arguments[1], messageId: "documentBodyPortal" });
      },
    };
  },
});
