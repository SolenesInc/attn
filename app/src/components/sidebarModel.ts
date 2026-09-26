import type { SidebarWorkspace } from './sidebarTypes';

export function isSessionless(workspace: SidebarWorkspace): boolean {
  return workspace.sessions.length === 0;
}

interface AutomationRun {
  automation?: { definition_id: string; definition_name: string };
}

interface AutomationSessionGroup<S extends AutomationRun> {
  id: string;
  name: string;
  sessions: S[];
}

export function groupAutomationSessions<S extends AutomationRun>(
  workspaces: readonly { sessions: readonly S[] }[],
): AutomationSessionGroup<S>[] {
  const groups = new Map<string, AutomationSessionGroup<S>>();
  for (const workspace of workspaces) {
    for (const session of workspace.sessions) {
      const automation = session.automation;
      if (!automation) continue;
      const existing = groups.get(automation.definition_id);
      if (existing) {
        existing.sessions.push(session);
      } else {
        groups.set(automation.definition_id, {
          id: automation.definition_id,
          name: automation.definition_name,
          sessions: [session],
        });
      }
    }
  }
  return [...groups.values()].sort(
    (a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id),
  );
}

import { formatShortcut } from '../shortcuts/formatShortcut';
import type { ShortcutId } from '../shortcuts/registry';
export function workspaceShortcut(index: number): string | null {
  if (index < 0 || index >= 9) return null;
  return formatShortcut(`desktop.select${index + 1}` as ShortcutId);
}
