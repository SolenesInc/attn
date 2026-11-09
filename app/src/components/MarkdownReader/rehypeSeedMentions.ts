/** Wraps known seed ids and slugs in prose as `span[data-seed-mention]`. Runs after
 * sanitizing, so author HTML cannot forge the attribute, and skips code and links. */

import type { Element, ElementContent, Root, RootContent } from "hast";
import type { SeedMentionIndex } from "../../utils/seedMentions";

const SKIP_TAGS = new Set(["code", "pre", "kbd", "samp", "a", "script", "style", "textarea", "svg", "math"]);

export default function rehypeSeedMentions({ index }: { index: SeedMentionIndex }) {
  const walk = (node: Root | Element): void => {
    if (node.type === "element" && SKIP_TAGS.has(node.tagName)) return;
    const children: RootContent[] = [];
    for (const child of node.children) {
      if (child.type === "text") {
        for (const piece of index.split(child.value)) {
          children.push(
            typeof piece === "string"
              ? { type: "text", value: piece }
              : {
                  type: "element",
                  tagName: "span",
                  properties: { dataSeedMention: piece.seedId },
                  children: [{ type: "text", value: piece.text }],
                },
          );
        }
        continue;
      }
      if (child.type === "element") walk(child);
      children.push(child);
    }
    node.children = children as ElementContent[];
  };
  return (tree: Root): void => walk(tree);
}
