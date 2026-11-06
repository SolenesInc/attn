import type { ShelfDropTarget } from './leafDrag';

export function shelfDropTargetAt(desktopId: string, ownColumnKey: string | null, clientX: number, clientY: number): ShelfDropTarget | null {
  const shelf = document.querySelector(`[data-testid="desktop-shelf"][data-shelf-desktop="${desktopId}"]`);
  if (!shelf) return null;
  const area = shelf.getBoundingClientRect();
  if (clientX < area.left || clientX > area.right || clientY < area.top || clientY > area.bottom) return null;

  let nearest: { anchor: HTMLElement; rect: DOMRect; distance: number } | null = null;
  for (const anchor of shelf.querySelectorAll<HTMLElement>('[data-shelf-anchor]')) {
    const rect = anchor.getBoundingClientRect();
    const overAnchor = clientX >= rect.left && clientX <= rect.right;
    if (anchor.dataset.shelfAnchor === ownColumnKey) {
      if (overAnchor) return null;
      continue;
    }
    const distance = overAnchor ? 0 : Math.min(Math.abs(clientX - rect.left), Math.abs(clientX - rect.right));
    if (!nearest || distance < nearest.distance) nearest = { anchor, rect, distance };
  }
  if (!nearest) return null;
  const fixedEdge = nearest.anchor.dataset.shelfEdge as ShelfDropTarget['edge'] | undefined;
  return {
    anchorId: nearest.anchor.dataset.shelfAnchor!,
    edge: fixedEdge ?? (clientX < nearest.rect.left + nearest.rect.width / 2 ? 'left' : 'right'),
  };
}
