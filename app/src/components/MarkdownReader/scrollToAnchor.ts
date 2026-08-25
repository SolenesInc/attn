
const SCROLL_CONTAINER_SELECTOR = '.workspace-dock-tile-body';

function findScrollContainer(start: HTMLElement): HTMLElement | null {
  const tileBody = start.closest<HTMLElement>(SCROLL_CONTAINER_SELECTOR);
  if (tileBody) {
    return tileBody;
  }
  let node: HTMLElement | null = start.parentElement;
  while (node) {
    const overflowY = getComputedStyle(node).overflowY;
    if (overflowY === 'auto' || overflowY === 'scroll') {
      return node;
    }
    node = node.parentElement;
  }
  return null;
}

export function scrollToAnchor(root: HTMLElement | null, hash: string, stickyBar?: HTMLElement | null): boolean {
  if (!root || !hash.startsWith('#')) {
    return false;
  }
  let id: string;
  try {
    id = decodeURIComponent(hash.slice(1));
  } catch {
    id = hash.slice(1);
  }
  if (!id) {
    return false;
  }
  // Resolve WITHIN root: several tiles can render the same document, so getElementById
  // finds the first tile's heading. The attribute selector accepts ids `#id` rejects.
  const target = root.querySelector<HTMLElement>(`[id="${CSS.escape(id)}"]`);
  if (!target) {
    return false;
  }
  const viewport = findScrollContainer(root);
  if (!viewport) {
    return false;
  }
  const headerOffset = stickyBar
    ? stickyBar.getBoundingClientRect().height + (parseFloat(getComputedStyle(stickyBar).top || '0') || 0)
    : 0;
  const top = viewport.scrollTop
    + (target.getBoundingClientRect().top - viewport.getBoundingClientRect().top)
    - headerOffset;
  const reduceMotion = typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  viewport.scrollTo({ top: Math.max(0, top), behavior: reduceMotion ? 'auto' : 'smooth' });
  return true;
}
