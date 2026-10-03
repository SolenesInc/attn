import { formatShortcut } from '../shortcuts/formatShortcut';
import type { ShortcutId } from '../shortcuts/registry';
import type { SidebarDesktop } from './sidebarTypes';

export function hasNoLeaves(desktop: SidebarDesktop): boolean {
  return desktop.children.length === 0;
}

export function desktopShortcut(index: number): string | null {
  if (index < 0 || index >= 9) return null;
  return formatShortcut(`desktop.select${index + 1}` as ShortcutId);
}
