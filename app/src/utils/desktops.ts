import { formatShortcut } from '../shortcuts/formatShortcut';
import type { ShortcutId } from '../shortcuts/registry';
import type { Desktop, DesktopPane } from '../types/generated';
import { hasLeaf, parseLayoutJSON, type TerminalWorkspaceSnapshot, type TerminalWorkspaceState } from '../types/workspace';

export const SHORTCUT_SLOTS = [1, 2, 3, 4, 5, 6, 7, 8, 9] as const;

function byOrderKey(a: Desktop, b: Desktop): number {
  return a.order_key < b.order_key ? -1 : a.order_key > b.order_key ? 1 : 0;
}

function extraDesktops(desktops: Desktop[]): Desktop[] {
  return desktops.filter((desktop) => !desktop.shortcut_slot).sort(byOrderKey);
}

export function orderedDesktops(desktops: Desktop[]): Desktop[] {
  return [...desktops].sort(byOrderKey);
}

export function desktopNumber(desktop: Desktop, desktops: Desktop[]): number {
  if (desktop.shortcut_slot) return desktop.shortcut_slot;
  return SHORTCUT_SLOTS.length + extraDesktops(desktops).findIndex((entry) => entry.id === desktop.id) + 1;
}

export function defaultDesktopLabel(desktop: Desktop, desktops: Desktop[]): string {
  return `Desktop ${desktopNumber(desktop, desktops)}`;
}

export function desktopLabel(desktop: Desktop, desktops: Desktop[]): string {
  return desktop.name.trim() || defaultDesktopLabel(desktop, desktops);
}

export function desktopInSlot(desktops: Desktop[], slot: number): Desktop | undefined {
  return desktops.find((desktop) => desktop.shortcut_slot === slot);
}

export function firstFreeSlot(desktops: Desktop[]): number | null {
  return SHORTCUT_SLOTS.find((slot) => !desktopInSlot(desktops, slot)) ?? null;
}

export function isEmptyDesktop(desktop: Desktop): boolean {
  return desktop.tree_json.trim() === '';
}

export function slotShortcut(slot: number): string {
  return formatShortcut(`desktop.select${slot}` as ShortcutId);
}

export function desktopTerminalState(desktop: Desktop): TerminalWorkspaceState {
  return {
    agents: desktop.panes
      .filter((pane) => pane.kind === 'agent')
      .map((pane) => ({
        id: pane.pane_id,
        runtimeId: pane.session_id,
        sessionId: pane.session_id,
        title: pane.title || pane.pane_id,
        ...(pane.status !== 'ready' ? { status: pane.status } : {}),
        ...(pane.error ? { error: pane.error } : {}),
      })),
    layoutTree: parseLayoutJSON(desktop.tree_json),
  };
}

export function desktopSnapshot(desktop: Desktop): TerminalWorkspaceSnapshot {
  const workspace = desktopTerminalState(desktop);
  const firstPaneId = workspace.agents[0]?.id ?? '';
  const active = desktop.active_pane_id;
  return {
    workspace,
    daemonActivePaneId: active && workspace.layoutTree && hasLeaf(workspace.layoutTree, active) ? active : firstPaneId,
  };
}

export function desktopPaneOfAgent(desktops: Desktop[], sessionId: string): DesktopPane | undefined {
  for (const desktop of desktops) {
    const pane = desktop.panes.find((entry) => entry.session_id === sessionId);
    if (pane) return pane;
  }
  return undefined;
}
