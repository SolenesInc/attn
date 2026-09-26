import type { CSSProperties } from 'react';
import { findDraftDesktop, nearestEdge, planWithout, type DraftDesktopView, type DraftView } from './migrationDraft';
import type { GroupDropTarget } from './useGroupDrag';

function dropLabel(target: Omit<GroupDropTarget, 'label'>, view: DraftView, desktop: DraftDesktopView): string {
  if (target.empty) return `Move to ${desktop.label}`;
  const side = target.edge === 'top' ? 'above' : target.edge === 'bottom' ? 'below' : `${target.edge} of`;
  const anchor = target.anchorGroupId ? view.groupById.get(target.anchorGroupId)?.group.title : 'the desktop';
  return `Merge ${side} ${anchor}`;
}

export function resolveDropTarget(view: DraftView, groupId: string, x: number, y: number): GroupDropTarget | null {
  const element = document.elementFromPoint(x, y) as HTMLElement | null;
  const card = element?.closest<HTMLElement>('[data-migration-desktop]');
  const desktop = card ? findDraftDesktop(view, card.dataset.migrationDesktop ?? '') : undefined;
  const preview = card?.querySelector<HTMLElement>('.mp-desk-preview');
  if (!desktop || !preview) return null;
  if (!planWithout(desktop.tree, groupId)) {
    if (desktop.groupIds.includes(groupId)) return null;
    const target = { desktopKey: desktop.desktop.key, anchorGroupId: null, edge: 'right' as const, box: preview.getBoundingClientRect(), empty: true };
    return { ...target, label: dropLabel(target, view, desktop) };
  }
  const groupElement = element?.closest<HTMLElement>('[data-migration-group]');
  const anchorGroupId = groupElement?.dataset.migrationGroup ?? null;
  if (anchorGroupId === groupId) return null;
  const box = (groupElement ?? preview).getBoundingClientRect();
  const target = { desktopKey: desktop.desktop.key, anchorGroupId, edge: nearestEdge(box, x, y), box, empty: false };
  return { ...target, label: dropLabel(target, view, desktop) };
}

export function dropOverlayStyle(target: GroupDropTarget): CSSProperties {
  let { left, top, width, height } = target.box;
  if (!target.empty) {
    if (target.edge === 'left' || target.edge === 'right') {
      width /= 2;
      if (target.edge === 'right') left += width;
    } else {
      height /= 2;
      if (target.edge === 'bottom') top += height;
    }
  }
  return { left, top, width, height };
}
