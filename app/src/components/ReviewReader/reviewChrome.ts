import type { Element, ElementContent, Root, RootContent } from 'hast';
import type { ReviewBundle } from '../../types/generated';

/** The document lines a review renders as chrome instead of markdown: each
 * stop's heading and the note items under it, and the Not shown items. The
 * change's facts follow the title, or open the document when it has none. */
export interface ReviewChrome {
  stops: Array<{ line: number; notes: number[] }>;
  notShown: number[];
}

export function reviewChromeOf(bundle: ReviewBundle | undefined): ReviewChrome | null {
  if (!bundle) return null;
  return {
    stops: bundle.stops.map((stop) => ({ line: stop.heading_line, notes: stop.notes.map((note) => note.line) })),
    notShown: bundle.not_shown.map((entry) => entry.line),
  };
}

export function reviewChromeKey(chrome: ReviewChrome | null): string {
  return chrome ? JSON.stringify(chrome) : '';
}

function isElement(node: RootContent | ElementContent): node is Element {
  return node.type === 'element';
}

function startLine(node: Element): number | undefined {
  return node.position?.start.line;
}

function listWithin(node: Element, lines: Set<number>): boolean {
  const items = node.children.filter(isElement).filter((child) => child.tagName === 'li');
  return items.length > 0 && items.every((item) => lines.has(startLine(item) ?? -1));
}

function chromeElement(property: string, value: number): Element {
  return { type: 'element', tagName: 'div', properties: { [property]: value }, children: [] };
}

/** Runs after rehypeSourceAnchors, so the blocks around a stop keep their ids
 * and offsets: the stop is one element with no text, drawn by the reader. */
export default function rehypeReviewChrome({ chrome }: { chrome: ReviewChrome }) {
  const stopLines = new Set(chrome.stops.map((stop) => stop.line));
  const noteLines = new Set(chrome.stops.flatMap((stop) => stop.notes));
  const notShownLines = new Set(chrome.notShown);
  return (tree: Root): void => {
    const children: RootContent[] = [];
    for (const node of tree.children) {
      if (!isElement(node)) {
        children.push(node);
        continue;
      }
      const line = startLine(node);
      if (node.tagName === 'h3' && line !== undefined && stopLines.has(line)) {
        children.push(chromeElement('dataReviewStop', line));
      } else if (node.tagName === 'ul' && listWithin(node, noteLines)) {
        continue;
      } else if (node.tagName === 'ul' && listWithin(node, notShownLines)) {
        children.push(chromeElement('dataReviewNotShown', line ?? 0));
      } else {
        children.push(node);
      }
    }
    const title = children.findIndex((node) => isElement(node) && node.tagName === 'h1');
    children.splice(title + 1, 0, chromeElement('dataReviewSummary', 0));
    tree.children = children;
  };
}
