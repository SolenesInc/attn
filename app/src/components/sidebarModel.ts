import { formatShortcut } from '../shortcuts/formatShortcut';
import type { ShortcutId } from '../shortcuts/registry';
import type { SidebarDesktop, SidebarProps } from './sidebarTypes';

export function hasNoLeaves(desktop: SidebarDesktop): boolean {
  return desktop.children.length === 0;
}

export function desktopShortcut(index: number): string | null {
  if (index < 0 || index >= 9) return null;
  return formatShortcut(`desktop.select${index + 1}` as ShortcutId);
}

export function treeSelectionKey({
  homeActive,
  selectedTile,
  selectedId,
  selectedDesktopId,
  desktops,
}: Pick<SidebarProps, 'homeActive' | 'selectedTile' | 'selectedId' | 'selectedDesktopId' | 'desktops'>): string | null {
  if (homeActive) return null;
  if (selectedTile) return `${selectedTile.desktopId}/tile:${selectedTile.tileId}`;
  const desktop = desktops.find((desktop) => desktop.sessions.some((session) => session.id === selectedId));
  if (desktop && selectedId) return `${desktop.id}/session:${selectedId}`;
  return selectedDesktopId ? `${selectedDesktopId}/` : null;
}
