// Runs AFTER sanitize and BEFORE rehypeProseTransforms: slugs must come from the
// pre-transform text, or `## Deploy :rocket:` loses the id `deploy-rocket`.

import type { Element, Root, RootContent } from 'hast';
import { createSlugger } from './slugify';

const HEADING_TAGS = new Set(['h1', 'h2', 'h3', 'h4', 'h5', 'h6']);

function textOf(node: Root | RootContent): string {
  if (node.type === 'text') {
    return node.value;
  }
  if ('children' in node) {
    return node.children.map(textOf).join('');
  }
  return '';
}

export default function rehypeHeadingSlugs() {
  return (tree: Root): void => {
    const slugger = createSlugger();
    const walk = (node: Root | RootContent): void => {
      if (node.type === 'element' && HEADING_TAGS.has(node.tagName)) {
        const element = node as Element;
        const id = slugger(textOf(element));
        if (id) {
          element.properties = { ...element.properties, id };
        }
        return; // headings never nest
      }
      if ('children' in node) {
        for (const child of node.children) {
          walk(child);
        }
      }
    };
    walk(tree);
  };
}
