import type { SidebarWorkspace } from './sidebarTypes';

export function isSessionless(workspace: SidebarWorkspace): boolean {
  return workspace.sessions.length === 0;
}

interface AutomationSessionGroup {
  id: string;
  name: string;
  views: Extract<SidebarWorkspace['children'][number], { kind: 'session' }>[];
}

export function groupAutomationSessions(workspaces: SidebarWorkspace[]): AutomationSessionGroup[] {
  const groups = new Map<string, AutomationSessionGroup>();
  for (const workspace of workspaces) {
    for (const child of workspace.children) {
      if (child.kind !== 'session') continue;
      const { session } = child;
      const automation = session.automation;
      if (!automation) continue;
      const existing = groups.get(automation.definition_id);
      if (existing) {
        existing.views.push(child);
      } else {
        groups.set(automation.definition_id, {
          id: automation.definition_id,
          name: automation.definition_name,
          views: [child],
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
  return formatShortcut(`workspace.select${index + 1}` as ShortcutId);
}
