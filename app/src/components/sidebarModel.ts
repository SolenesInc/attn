import { formatShortcut } from '../shortcuts/formatShortcut';
import type { ShortcutId } from '../shortcuts/registry';
import type { SidebarWorkspace } from './sidebarTypes';

export function isSessionless(workspace: SidebarWorkspace): boolean {
  return workspace.sessions.length === 0;
}

export function workspaceShortcut(index: number): string | null {
  if (index < 0 || index >= 9) return null;
  return formatShortcut(`desktop.select${index + 1}` as ShortcutId);
}
