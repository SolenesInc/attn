import { collectLayoutLeaves, leafSlotId, parseLayoutJSON, type TerminalLeaf } from '../types/desktop';
import type { Desktop } from '../types/generated';

export type ActiveLeaf =
  | { kind: 'agent'; profileId: string; desktopId: string; leafId: string; sessionId: string }
  | { kind: 'tile'; profileId: string; desktopId: string; leafId: string; tileSessionId: string | null };

export type ShowTarget =
  | { kind: 'session'; sessionId: string }
  | { kind: 'leaf'; desktopId: string; leafId: string };

export interface Arrangement {
  profileId: string;
  currentDesktopId: string | null;
  desktops: Desktop[];
}

const leavesByTree = new WeakMap<Desktop, TerminalLeaf[]>();

export function desktopLeaves(desktop: Desktop): TerminalLeaf[] {
  let leaves = leavesByTree.get(desktop);
  if (!leaves) {
    leaves = collectLayoutLeaves(parseLayoutJSON(desktop.tree_json));
    leavesByTree.set(desktop, leaves);
  }
  return leaves;
}

export function leafOn(profileId: string, desktop: Desktop, leafId: string): ActiveLeaf | null {
  const leaf = desktopLeaves(desktop).find((entry) => leafSlotId(entry) === leafId);
  if (!leaf) return null;
  if (leaf.type === 'tile') return { kind: 'tile', profileId, desktopId: desktop.id, leafId, tileSessionId: leaf.tileSessionId || null };
  const sessionId = desktop.panes.find((pane) => pane.pane_id === leafId)?.session_id;
  return sessionId ? { kind: 'agent', profileId, desktopId: desktop.id, leafId, sessionId } : null;
}

export function activeLeafOf(arrangement: Arrangement): ActiveLeaf | null {
  const desktop = arrangement.desktops.find((entry) => entry.id === arrangement.currentDesktopId);
  if (!desktop?.active_pane_id) return null;
  return leafOn(arrangement.profileId, desktop, desktop.active_pane_id);
}

export function sameLeaf(a: ActiveLeaf | null, b: ActiveLeaf | null): boolean {
  return a?.desktopId === b?.desktopId && a?.leafId === b?.leafId;
}

export function leafShows(leaf: ActiveLeaf | null, target: ShowTarget): boolean {
  if (!leaf) return false;
  if (target.kind === 'session') return leaf.kind === 'agent' && leaf.sessionId === target.sessionId;
  return leaf.desktopId === target.desktopId && leaf.leafId === target.leafId;
}
